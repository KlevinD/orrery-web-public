package developmentproject

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/scmconnection"
	"orrery_v2/internal/platform/idgen"
)

type IDGenerator interface{ NewIDContext() (string, error) }
type Clock interface{ Now() time.Time }

type SCMConnections interface {
	GetConnectionForUse(ctx context.Context, viewer scmconnection.ViewerRef, connectionID string) (scmconnection.Connection, error)
	ListRepositories(ctx context.Context, viewer scmconnection.ViewerRef, connectionID string) ([]scmconnection.RepositoryMetadata, error)
}

type AuditRecorder interface {
	RecordDevelopmentProjectAudit(ctx context.Context, event AuditEvent) error
}

type SecretReferenceValidator interface {
	ValidateSecretReference(ctx context.Context, ref string) error
}

type ExecutionMonitor interface {
	ListProjectExecutions(ctx context.Context, viewer ViewerRef, project Project, canMonitor bool, query ListExecutionsQuery) (ExecutionPage, error)
}

type Service struct {
	repo               Repository
	ids                IDGenerator
	clock              Clock
	access             *authorization.Service
	relations          *authorization.RelationProjector
	connections        SCMConnections
	audit              AuditRecorder
	secrets            SecretReferenceValidator
	executions         ExecutionMonitor
	executionRepo      *ExecutionRepository
	gitWorkspace       GitWorkspacePort
	workspaceLeaseTTL  time.Duration
	workspaceRetention time.Duration
	reconcileMu        sync.Mutex
	reconcileAt        string
}

func (s *Service) SetExecutionMonitor(monitor ExecutionMonitor) {
	if s != nil {
		s.executions = monitor
	}
}

func (s *Service) ListExecutions(ctx context.Context, viewer ViewerRef, projectID string, query ListExecutionsQuery) (ExecutionPage, error) {
	project, err := s.getAuthorized(ctx, viewer, projectID, "development_project.view")
	if err != nil {
		return ExecutionPage{}, err
	}
	if s.executions == nil {
		return ExecutionPage{}, ErrDependency
	}
	capabilities, err := s.capabilities(ctx, viewer, project)
	if err != nil {
		return ExecutionPage{}, err
	}
	return s.executions.ListProjectExecutions(ctx, viewer, project, capabilities.CanMonitor, query)
}

func NewService(repo Repository, ids IDGenerator, clock Clock) *Service {
	return &Service{repo: repo, ids: ids, clock: clock}
}

func (s *Service) SetAuthorization(access *authorization.Service, relations *authorization.RelationProjector) {
	if s != nil {
		s.access = access
		s.relations = relations
	}
}

func (s *Service) SetSCMConnections(connections SCMConnections) {
	if s != nil {
		s.connections = connections
	}
}

func (s *Service) SetAuditRecorder(audit AuditRecorder) {
	if s != nil {
		s.audit = audit
	}
}

func (s *Service) SetSecretReferenceValidator(validator SecretReferenceValidator) {
	if s != nil {
		s.secrets = validator
	}
}

func (s *Service) Create(ctx context.Context, viewer ViewerRef, cmd CreateCommand) (ProjectDTO, error) {
	if err := s.requireMutationDependencies(false); err != nil {
		return ProjectDTO{}, err
	}
	if err := validateViewer(viewer); err != nil {
		return ProjectDTO{}, err
	}
	cmd = normalizeCreateCommand(cmd)
	if cmd.IdempotencyKey != "" && !validIdempotencyKey(cmd.IdempotencyKey) {
		return ProjectDTO{}, fmt.Errorf("%w: idempotency key is invalid", ErrInvalidRequest)
	}
	if err := validateProjectValues(cmd.Name, cmd.DefaultAutomationMode, cmd.MaxConcurrentRuns, cmd.ExecutionPolicy); err != nil {
		return ProjectDTO{}, err
	}
	if cmd.DevelopmentProjectID != "" && !validResourceID(cmd.DevelopmentProjectID) {
		return ProjectDTO{}, fmt.Errorf("%w: development_project_id is invalid", ErrInvalidRequest)
	}
	if err := s.validateSecretReferences(ctx, cmd.ExecutionPolicy.AllowedSecretRefs); err != nil {
		return ProjectDTO{}, err
	}
	if err := s.authorizeCreate(ctx, viewer, cmd.OrganizationID); err != nil {
		return ProjectDTO{}, err
	}
	requestHash, err := developmentProjectCreateRequestHash(cmd)
	if err != nil {
		return ProjectDTO{}, err
	}
	projectID := cmd.DevelopmentProjectID
	if cmd.IdempotencyKey != "" && projectID == "" {
		projectID = deterministicResourceID("devproj", viewerTenant(viewer), cmd.IdempotencyKey)
	}
	if projectID == "" {
		var err error
		projectID, err = s.newID("devproj")
		if err != nil {
			return ProjectDTO{}, err
		}
	}
	if !validResourceID(projectID) {
		return ProjectDTO{}, fmt.Errorf("%w: development_project_id is invalid", ErrInvalidRequest)
	}
	memberID, err := s.newID("member")
	if err != nil {
		return ProjectDTO{}, err
	}
	now := s.now()
	ownerID := viewerSubjectID(viewer)
	if err := validateMember(viewer.ViewerKind, ownerID, MemberRoleOwner); err != nil {
		return ProjectDTO{}, err
	}
	project := Project{
		TenantID: viewerTenant(viewer), DevelopmentProjectID: projectID, OrganizationID: cmd.OrganizationID,
		Name: cmd.Name, Description: cmd.Description, Status: StatusActive,
		DefaultAutomationMode: cmd.DefaultAutomationMode, MaxConcurrentRuns: cmd.MaxConcurrentRuns,
		ExecutionPolicy: cloneExecutionPolicy(cmd.ExecutionPolicy), CreatedBy: ownerID,
		CreatedAt: now, UpdatedAt: now, Version: 1,
		Members:              []Member{{MemberID: memberID, SubjectKind: viewer.ViewerKind, SubjectID: ownerID, Role: MemberRoleOwner, CreatedBy: ownerID, CreatedAt: now, UpdatedAt: now, Version: 1}},
		CreateIdempotencyKey: cmd.IdempotencyKey, CreateRequestHash: requestHash,
	}
	appendProjectEffects(&project,
		projectCreatedEffect(project, viewerSubject(viewer)),
		s.projectAuditEffect(project, viewer, "development_project.created", "succeeded", "created development project", nil),
	)
	if err := s.repo.Create(ctx, project.TenantID, project); err != nil {
		if !errors.Is(err, ErrConflict) || cmd.IdempotencyKey == "" {
			return ProjectDTO{}, err
		}
		existing, getErr := s.repo.Get(ctx, project.TenantID, project.DevelopmentProjectID)
		if getErr != nil {
			return ProjectDTO{}, getErr
		}
		if existing.CreateIdempotencyKey != cmd.IdempotencyKey || existing.CreateRequestHash != requestHash {
			return ProjectDTO{}, ErrIdempotency
		}
		project = existing
	}
	project, err = s.reconcileProjectEffects(ctx, project.TenantID, project.DevelopmentProjectID)
	if err != nil {
		return ProjectDTO{}, err
	}
	return s.dto(ctx, viewer, project)
}

func (s *Service) List(ctx context.Context, viewer ViewerRef, query ListQuery) (ProjectPage, error) {
	if s == nil || s.repo == nil || s.access == nil {
		return ProjectPage{}, ErrDependency
	}
	if err := validateViewer(viewer); err != nil {
		return ProjectPage{}, err
	}
	query.OrganizationID = strings.TrimSpace(query.OrganizationID)
	query.Status = strings.ToLower(strings.TrimSpace(query.Status))
	if (query.OrganizationID != "" && !validSubjectID(query.OrganizationID)) ||
		(query.Status != "" && !oneOf(query.Status, StatusActive, StatusArchived)) || query.Offset < 0 || query.Limit < 0 {
		return ProjectPage{}, ErrInvalidRequest
	}
	projects, err := s.repo.List(ctx, viewerTenant(viewer))
	if err != nil {
		return ProjectPage{}, err
	}
	items := make([]ProjectDTO, 0, len(projects))
	for _, project := range projects {
		if query.OrganizationID != "" && project.OrganizationID != strings.TrimSpace(query.OrganizationID) {
			continue
		}
		if query.Status != "" && project.Status != strings.ToLower(strings.TrimSpace(query.Status)) {
			continue
		}
		dto, err := s.dto(ctx, viewer, project)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return ProjectPage{}, err
		}
		items = append(items, dto)
	}
	offset, limit := pageBounds(query.Offset, query.Limit, len(items))
	pageItems := append([]ProjectDTO(nil), items[offset:offset+limit]...)
	return ProjectPage{Items: pageItems, Page: PageDTO{Limit: limit, Offset: offset, Total: len(items), Returned: len(pageItems), HasMore: offset+limit < len(items)}}, nil
}

func (s *Service) Get(ctx context.Context, viewer ViewerRef, projectID string) (ProjectDTO, error) {
	project, err := s.getAuthorized(ctx, viewer, projectID, "development_project.view")
	if err != nil {
		return ProjectDTO{}, err
	}
	return s.dto(ctx, viewer, project)
}

func (s *Service) Update(ctx context.Context, viewer ViewerRef, cmd UpdateCommand) (ProjectDTO, error) {
	if err := s.requireMutationDependencies(false); err != nil {
		return ProjectDTO{}, err
	}
	cmd.IdempotencyKey = strings.TrimSpace(cmd.IdempotencyKey)
	if cmd.IdempotencyKey != "" && !validIdempotencyKey(cmd.IdempotencyKey) {
		return ProjectDTO{}, fmt.Errorf("%w: idempotency key is invalid", ErrInvalidRequest)
	}
	hashCommand := cmd
	hashCommand.IdempotencyKey = ""
	requestHash, err := projectMutationRequestHash("update", hashCommand)
	if err != nil {
		return ProjectDTO{}, err
	}
	action := "development_project.edit"
	if cmd.DefaultAutomationMode != nil || cmd.MaxConcurrentRuns != nil || cmd.ExecutionPolicy != nil {
		action = "development_project.configure"
	}
	project, err := s.getAuthorized(ctx, viewer, cmd.DevelopmentProjectID, action)
	if err != nil {
		return ProjectDTO{}, err
	}
	if _, replay, err := projectMutationReplay(project, cmd.IdempotencyKey, "update", requestHash); err != nil {
		return ProjectDTO{}, err
	} else if replay {
		project, err = s.reconcileProjectEffects(ctx, project.TenantID, project.DevelopmentProjectID)
		if err != nil {
			return ProjectDTO{}, err
		}
		return s.dto(ctx, viewer, project)
	}
	if project.Status == StatusArchived {
		return ProjectDTO{}, ErrArchived
	}
	if cmd.ExpectedVersion <= 0 || project.Version != cmd.ExpectedVersion {
		return ProjectDTO{}, ErrConflict
	}
	if cmd.Name != nil {
		project.Name = strings.TrimSpace(*cmd.Name)
	}
	if cmd.Description != nil {
		project.Description = strings.TrimSpace(*cmd.Description)
	}
	if cmd.DefaultAutomationMode != nil {
		project.DefaultAutomationMode = strings.ToLower(strings.TrimSpace(*cmd.DefaultAutomationMode))
	}
	if cmd.MaxConcurrentRuns != nil {
		project.MaxConcurrentRuns = *cmd.MaxConcurrentRuns
	}
	if cmd.ExecutionPolicy != nil {
		project.ExecutionPolicy = applyExecutionPolicyPatch(project.ExecutionPolicy, *cmd.ExecutionPolicy)
	}
	if err := validateProjectValues(project.Name, project.DefaultAutomationMode, project.MaxConcurrentRuns, project.ExecutionPolicy); err != nil {
		return ProjectDTO{}, err
	}
	if err := s.validateSecretReferences(ctx, project.ExecutionPolicy.AllowedSecretRefs); err != nil {
		return ProjectDTO{}, err
	}
	project.Version++
	project.UpdatedAt = s.now()
	appendProjectMutationReceipt(&project, MutationReceipt{IdempotencyKey: cmd.IdempotencyKey, Operation: "update", RequestHash: requestHash})
	appendProjectEffects(&project, s.projectAuditEffect(project, viewer, "development_project.updated", "succeeded", "updated development project", nil))
	if err := s.repo.Update(ctx, project.TenantID, project, cmd.ExpectedVersion); err != nil {
		return ProjectDTO{}, err
	}
	project, err = s.reconcileProjectEffects(ctx, project.TenantID, project.DevelopmentProjectID)
	if err != nil {
		return ProjectDTO{}, err
	}
	return s.dto(ctx, viewer, project)
}

func (s *Service) Archive(ctx context.Context, viewer ViewerRef, cmd ArchiveCommand) (ProjectDTO, error) {
	if err := s.requireMutationDependencies(false); err != nil {
		return ProjectDTO{}, err
	}
	cmd.IdempotencyKey = strings.TrimSpace(cmd.IdempotencyKey)
	if cmd.IdempotencyKey != "" && !validIdempotencyKey(cmd.IdempotencyKey) {
		return ProjectDTO{}, fmt.Errorf("%w: idempotency key is invalid", ErrInvalidRequest)
	}
	hashCommand := cmd
	hashCommand.IdempotencyKey = ""
	requestHash, err := projectMutationRequestHash("archive", hashCommand)
	if err != nil {
		return ProjectDTO{}, err
	}
	project, err := s.getAuthorized(ctx, viewer, cmd.DevelopmentProjectID, "development_project.manage")
	if err != nil {
		return ProjectDTO{}, err
	}
	if _, replay, err := projectMutationReplay(project, cmd.IdempotencyKey, "archive", requestHash); err != nil {
		return ProjectDTO{}, err
	} else if replay {
		project, err = s.reconcileProjectEffects(ctx, project.TenantID, project.DevelopmentProjectID)
		if err != nil {
			return ProjectDTO{}, err
		}
		return s.dto(ctx, viewer, project)
	}
	if cmd.ExpectedVersion <= 0 || project.Version != cmd.ExpectedVersion {
		return ProjectDTO{}, ErrConflict
	}
	if project.Status != StatusArchived {
		project.Status = StatusArchived
		project.Version++
		project.UpdatedAt = s.now()
		appendProjectMutationReceipt(&project, MutationReceipt{IdempotencyKey: cmd.IdempotencyKey, Operation: "archive", RequestHash: requestHash})
		appendProjectEffects(&project, s.projectAuditEffect(project, viewer, "development_project.archived", "succeeded", "archived development project", nil))
		if err := s.repo.Update(ctx, project.TenantID, project, cmd.ExpectedVersion); err != nil {
			return ProjectDTO{}, err
		}
		project, err = s.reconcileProjectEffects(ctx, project.TenantID, project.DevelopmentProjectID)
		if err != nil {
			return ProjectDTO{}, err
		}
	}
	return s.dto(ctx, viewer, project)
}

func (s *Service) EnsureExecutionAllowed(ctx context.Context, viewer ViewerRef, projectID string) error {
	project, err := s.getAuthorized(ctx, viewer, projectID, "development_project.run")
	if err != nil {
		return err
	}
	if project.Status != StatusActive {
		return ErrArchived
	}
	return nil
}

func (s *Service) EnsureTodoBindingAllowed(ctx context.Context, viewer ViewerRef, projectID string) error {
	project, err := s.getAuthorized(ctx, viewer, projectID, "development_project.edit")
	if err != nil {
		return err
	}
	if project.Status != StatusActive {
		return ErrArchived
	}
	return nil
}

func (s *Service) EnsureTodoPlanApprovalAllowed(ctx context.Context, viewer ViewerRef, projectID string) error {
	project, err := s.getAuthorized(ctx, viewer, projectID, "development_project.approve")
	if err != nil {
		return err
	}
	if project.Status != StatusActive {
		return ErrArchived
	}
	return nil
}

// ResolveExecutionPlanSnapshot binds a trusted Todo approval to immutable
// repository and execution-policy inputs after user authorization is checked.
func (s *Service) ResolveExecutionPlanSnapshot(ctx context.Context, tenantID, projectID string) (ExecutionPlanSnapshot, error) {
	if s == nil || s.repo == nil {
		return ExecutionPlanSnapshot{}, ErrDependency
	}
	tenantID = normalizeTenantID(tenantID)
	projectID = strings.TrimSpace(projectID)
	if !validTenantKeySegment(tenantID) || !validResourceID(projectID) {
		return ExecutionPlanSnapshot{}, ErrInvalidRequest
	}
	project, err := s.repo.Get(ctx, tenantID, projectID)
	if err != nil {
		return ExecutionPlanSnapshot{}, err
	}
	if project.Status != StatusActive {
		return ExecutionPlanSnapshot{}, ErrArchived
	}

	type repositoryDigestInput struct {
		BindingID          string   `json:"binding_id"`
		ConnectionID       string   `json:"connection_id"`
		RepositoryLocator  string   `json:"repository_locator"`
		Role               string   `json:"role"`
		AccessMode         string   `json:"access_mode"`
		DefaultBranch      string   `json:"default_branch"`
		SourceSubdirectory string   `json:"source_subdirectory"`
		BuildCommands      []string `json:"build_commands"`
		TestCommands       []string `json:"test_commands"`
		BranchPrefix       string   `json:"branch_prefix"`
		Status             string   `json:"status"`
		LastFetchedCommit  string   `json:"last_fetched_commit"`
		Version            int64    `json:"version"`
	}
	bindings := cloneRepositoryBindings(project.RepositoryBindings)
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].BindingID < bindings[j].BindingID })
	digestRepositories := make([]repositoryDigestInput, 0, len(bindings))
	repositories := make([]ExecutionPlanRepository, 0, len(bindings))
	hasWritablePrimary := false
	for _, binding := range bindings {
		digestRepositories = append(digestRepositories, repositoryDigestInput{
			BindingID: binding.BindingID, ConnectionID: binding.ConnectionID, RepositoryLocator: binding.RepositoryLocator,
			Role: binding.Role, AccessMode: binding.AccessMode, DefaultBranch: binding.DefaultBranch,
			SourceSubdirectory: binding.SourceSubdirectory, BuildCommands: append([]string(nil), binding.BuildCommands...),
			TestCommands: append([]string(nil), binding.TestCommands...), BranchPrefix: binding.BranchPrefix,
			Status: binding.Status, LastFetchedCommit: binding.LastFetchedCommit, Version: binding.Version,
		})
		if binding.Status != BindingStatusActive || strings.TrimSpace(binding.LastFetchedCommit) == "" {
			continue
		}
		if binding.Role == RepositoryRolePrimary && binding.AccessMode == AccessReadWrite {
			hasWritablePrimary = true
		}
		repositories = append(repositories, ExecutionPlanRepository{
			BindingID: binding.BindingID, DisplayName: binding.DisplayName, Role: binding.Role, AccessMode: binding.AccessMode,
			DefaultBranch: binding.DefaultBranch, SourceSubdirectory: binding.SourceSubdirectory,
			BuildCommands: append([]string(nil), binding.BuildCommands...), TestCommands: append([]string(nil), binding.TestCommands...),
			BaseCommit: binding.LastFetchedCommit,
		})
	}
	if !hasWritablePrimary {
		return ExecutionPlanSnapshot{}, fmt.Errorf("%w: an active verified read-write primary repository is required", ErrRepositoryPolicy)
	}
	policy := cloneExecutionPolicy(project.ExecutionPolicy)
	payload, err := json.Marshal(struct {
		ProjectID    string                  `json:"project_id"`
		Repositories []repositoryDigestInput `json:"repositories"`
		Policy       ExecutionPolicy         `json:"policy"`
	}{ProjectID: project.DevelopmentProjectID, Repositories: digestRepositories, Policy: policy})
	if err != nil {
		return ExecutionPlanSnapshot{}, fmt.Errorf("%w: encode execution plan snapshot", ErrDependency)
	}
	digest := sha256.Sum256(payload)
	return ExecutionPlanSnapshot{
		DevelopmentProjectID: project.DevelopmentProjectID,
		SnapshotDigest:       fmt.Sprintf("sha256:%x", digest[:]),
		Repositories:         repositories,
		ExecutionPolicy:      policy,
	}, nil
}

func (s *Service) EnsureTodoExecutionAllowed(ctx context.Context, viewer ViewerRef, projectID string, agentProfileIDs []string) error {
	project, err := s.getAuthorized(ctx, viewer, projectID, "development_project.run")
	if err != nil {
		return err
	}
	if project.Status != StatusActive {
		return ErrArchived
	}
	hasWritablePrimary := false
	for _, binding := range project.RepositoryBindings {
		if binding.Role == RepositoryRolePrimary && binding.AccessMode == AccessReadWrite && binding.Status == BindingStatusActive && strings.TrimSpace(binding.LastFetchedCommit) != "" {
			hasWritablePrimary = true
			break
		}
	}
	if !hasWritablePrimary {
		return fmt.Errorf("%w: an active verified read-write primary repository is required", ErrRepositoryPolicy)
	}
	for _, agentID := range normalizeStrings(agentProfileIDs) {
		agent := ViewerRef{
			ViewerID: agentID, ViewerKind: "agent", TenantID: project.TenantID,
			AuthorizationSubjectID: agentID, PermissionScope: []string{"development.execute"},
		}
		if _, err := s.getAuthorized(ctx, agent, projectID, "development_project.run"); err != nil {
			return fmt.Errorf("%w: Agent %s is not an executor for the development project", ErrRepositoryPolicy, agentID)
		}
	}
	return nil
}

func normalizeCreateCommand(cmd CreateCommand) CreateCommand {
	cmd.IdempotencyKey = strings.TrimSpace(cmd.IdempotencyKey)
	cmd.DevelopmentProjectID = strings.TrimSpace(cmd.DevelopmentProjectID)
	cmd.OrganizationID = strings.TrimSpace(cmd.OrganizationID)
	cmd.Name = strings.TrimSpace(cmd.Name)
	cmd.Description = strings.TrimSpace(cmd.Description)
	cmd.DefaultAutomationMode = strings.ToLower(strings.TrimSpace(cmd.DefaultAutomationMode))
	if cmd.DefaultAutomationMode == "" {
		cmd.DefaultAutomationMode = AutomationManual
	}
	if cmd.MaxConcurrentRuns == 0 {
		cmd.MaxConcurrentRuns = 1
	}
	cmd.ExecutionPolicy = normalizeExecutionPolicy(cmd.ExecutionPolicy)
	return cmd
}

func developmentProjectCreateRequestHash(cmd CreateCommand) (string, error) {
	canonical := cmd
	canonical.IdempotencyKey = ""
	canonical.DevelopmentProjectID = ""
	payload, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("%w: encode create request", ErrDependency)
	}
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("%x", digest[:]), nil
}

func projectMutationRequestHash(operation string, command any) (string, error) {
	payload, err := json.Marshal(struct {
		Operation string `json:"operation"`
		Command   any    `json:"command"`
	}{Operation: operation, Command: command})
	if err != nil {
		return "", fmt.Errorf("%w: encode mutation request", ErrDependency)
	}
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("%x", digest[:]), nil
}

func projectMutationReplay(project Project, idempotencyKey, operation, requestHash string) (MutationReceipt, bool, error) {
	if idempotencyKey == "" {
		return MutationReceipt{}, false, nil
	}
	for _, receipt := range project.MutationReceipts {
		if receipt.IdempotencyKey != idempotencyKey {
			continue
		}
		if receipt.Operation != operation || receipt.RequestHash != requestHash {
			return MutationReceipt{}, false, ErrIdempotency
		}
		return receipt, true, nil
	}
	return MutationReceipt{}, false, nil
}

func appendProjectMutationReceipt(project *Project, receipt MutationReceipt) {
	if project == nil || receipt.IdempotencyKey == "" {
		return
	}
	if receipt.Member != nil {
		member := *receipt.Member
		receipt.Member = &member
	}
	if receipt.Repository != nil {
		repository := cloneRepositoryBindings([]RepositoryBinding{*receipt.Repository})[0]
		receipt.Repository = &repository
	}
	project.MutationReceipts = append(project.MutationReceipts, receipt)
	const maxReceipts = 128
	if len(project.MutationReceipts) > maxReceipts {
		project.MutationReceipts = append([]MutationReceipt(nil), project.MutationReceipts[len(project.MutationReceipts)-maxReceipts:]...)
	}
}

func deterministicResourceID(prefix, tenantID, idempotencyKey string) string {
	digest := sha256.Sum256([]byte(prefix + "\x00" + tenantID + "\x00" + idempotencyKey))
	return fmt.Sprintf("%s_%x", prefix, digest[:16])
}

func normalizeExecutionPolicy(policy ExecutionPolicy) ExecutionPolicy {
	policy.NetworkMode = strings.ToLower(strings.TrimSpace(policy.NetworkMode))
	if policy.NetworkMode == "" {
		policy.NetworkMode = NetworkNone
	}
	policy.AllowedHosts = normalizeHosts(policy.AllowedHosts)
	policy.AllowedCommandPrefixes = normalizeStrings(policy.AllowedCommandPrefixes)
	policy.AllowedFileScopes = normalizeRelativeScopes(policy.AllowedFileScopes)
	policy.AllowedSecretRefs = normalizeStrings(policy.AllowedSecretRefs)
	return policy
}

func applyExecutionPolicyPatch(policy ExecutionPolicy, patch ExecutionPolicyPatch) ExecutionPolicy {
	policy = cloneExecutionPolicy(policy)
	if patch.ApprovalRequired != nil {
		policy.ApprovalRequired = *patch.ApprovalRequired
	}
	if patch.NetworkMode != nil {
		policy.NetworkMode = *patch.NetworkMode
	}
	if patch.AllowedHosts != nil {
		policy.AllowedHosts = append([]string(nil), (*patch.AllowedHosts)...)
	}
	if patch.AllowedCommandPrefixes != nil {
		policy.AllowedCommandPrefixes = append([]string(nil), (*patch.AllowedCommandPrefixes)...)
	}
	if patch.AllowedFileScopes != nil {
		policy.AllowedFileScopes = append([]string(nil), (*patch.AllowedFileScopes)...)
	}
	if patch.AllowedSecretRefs != nil {
		policy.AllowedSecretRefs = append([]string(nil), (*patch.AllowedSecretRefs)...)
	}
	return normalizeExecutionPolicy(policy)
}

func validateProjectValues(name, automationMode string, maxConcurrentRuns int, policy ExecutionPolicy) error {
	if name == "" || len(name) > 160 || containsControl(name) || !oneOf(automationMode, AutomationManual, AutomationSuggest, AutomationManaged) {
		return ErrInvalidRequest
	}
	if maxConcurrentRuns < 1 || maxConcurrentRuns > 64 || !oneOf(policy.NetworkMode, NetworkNone, NetworkRestricted, NetworkAllowed) {
		return ErrInvalidRequest
	}
	if policy.NetworkMode == NetworkRestricted && len(policy.AllowedHosts) == 0 {
		return fmt.Errorf("%w: restricted network policy requires allowed hosts", ErrInvalidRequest)
	}
	if len(policy.AllowedHosts) > 128 || len(policy.AllowedCommandPrefixes) > 128 || len(policy.AllowedFileScopes) > 128 || len(policy.AllowedSecretRefs) > 128 {
		return fmt.Errorf("%w: execution policy contains too many entries", ErrInvalidRequest)
	}
	for _, host := range policy.AllowedHosts {
		if !validAllowedHost(host) {
			return fmt.Errorf("%w: allowed host is invalid", ErrInvalidRequest)
		}
	}
	for _, prefix := range policy.AllowedCommandPrefixes {
		if len(prefix) > 512 || containsControl(prefix) || unsafeCommandPolicy(prefix) {
			return fmt.Errorf("%w: allowed command prefix is invalid", ErrInvalidRequest)
		}
	}
	for _, scope := range policy.AllowedFileScopes {
		if len(scope) > 512 || containsControl(scope) || !validRelativePath(scope) {
			return fmt.Errorf("%w: allowed file scopes must be relative", ErrInvalidRequest)
		}
	}
	for _, ref := range policy.AllowedSecretRefs {
		if !validSecretRef(ref) {
			return fmt.Errorf("%w: allowed secret reference is invalid", ErrInvalidRequest)
		}
	}
	return nil
}

func unsafeCommandPolicy(command string) bool {
	if strings.ContainsAny(command, ";&|<>`$%^!") {
		return true
	}
	for _, token := range strings.Fields(strings.ToLower(command)) {
		flag := token
		if index := strings.IndexByte(flag, '='); index >= 0 {
			flag = flag[:index]
		}
		switch flag {
		case "-c", "/c", "-command", "-encodedcommand", "-exec", "--exec", "--eval", "--script-shell", "--require", "--import", "--loader":
			return true
		}
	}
	return false
}

func validRelativePath(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || containsControl(value) || strings.Contains(value, ":") {
		return false
	}
	value = path.Clean(strings.ReplaceAll(value, "\\", "/"))
	return value != "." && value != "" && value != ".." && !strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "../")
}

func normalizeRelativeScopes(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			normalized = append(normalized, path.Clean(strings.ReplaceAll(value, "\\", "/")))
		}
	}
	return normalizeStrings(normalized)
}

func normalizeHosts(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.ToLower(strings.TrimSpace(value)); value != "" {
			normalized = append(normalized, value)
		}
	}
	return normalizeStrings(normalized)
}

func validAllowedHost(value string) bool {
	if value == "" || len(value) > 255 || containsControl(value) || strings.ContainsAny(value, " /?#@") || strings.Contains(value, "://") {
		return false
	}
	host := value
	if strings.HasPrefix(value, "[") || strings.Count(value, ":") == 1 {
		parsedHost, rawPort, err := net.SplitHostPort(value)
		if err != nil {
			return false
		}
		port, err := strconv.Atoi(rawPort)
		if err != nil || port < 1 || port > 65535 {
			return false
		}
		host = parsedHost
	} else if strings.Contains(value, ":") {
		return net.ParseIP(value) != nil
	}
	if net.ParseIP(host) != nil {
		return true
	}
	if strings.HasPrefix(host, "*.") {
		host = strings.TrimPrefix(host, "*.")
	}
	if host == "" || len(host) > 253 || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func containsControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}

func validResourceID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 256 && !containsControl(value) && !strings.ContainsAny(value, "/\\")
}

func validSubjectID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 256 && !containsControl(value)
}

func validIdempotencyKey(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 256 && !containsControl(value)
}

func validSecretRef(value string) bool {
	if value == "" || len(value) > 256 || strings.ContainsAny(value, "\r\n\t @?") {
		return false
	}
	return strings.HasPrefix(value, "secret://") || strings.HasPrefix(value, "settings://")
}

func (s *Service) validateSecretReferences(ctx context.Context, refs []string) error {
	if len(refs) == 0 {
		return nil
	}
	if s == nil || s.secrets == nil {
		return ErrDependency
	}
	for _, ref := range refs {
		if err := s.secrets.ValidateSecretReference(ctx, ref); err != nil {
			return fmt.Errorf("%w: secret reference cannot be resolved: %v", ErrInvalidRequest, err)
		}
	}
	return nil
}

func normalizeStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func cloneExecutionPolicy(policy ExecutionPolicy) ExecutionPolicy {
	policy.AllowedHosts = append([]string(nil), policy.AllowedHosts...)
	policy.AllowedCommandPrefixes = append([]string(nil), policy.AllowedCommandPrefixes...)
	policy.AllowedFileScopes = append([]string(nil), policy.AllowedFileScopes...)
	policy.AllowedSecretRefs = append([]string(nil), policy.AllowedSecretRefs...)
	return policy
}

func oneOf(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if value == candidate {
			return true
		}
	}
	return false
}

func pageBounds(offset, limit, total int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset+limit > total {
		limit = total - offset
	}
	return offset, limit
}

func (s *Service) newID(prefix string) (string, error) {
	if s.ids == nil {
		return idgen.NewRandom(prefix).NewIDContext()
	}
	return s.ids.NewIDContext()
}

func (s *Service) now() time.Time {
	if s.clock == nil {
		return time.Now().UTC()
	}
	return s.clock.Now().UTC()
}
