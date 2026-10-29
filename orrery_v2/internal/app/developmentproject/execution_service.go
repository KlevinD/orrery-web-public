package developmentproject

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/scmconnection"
)

type executionSCMConnections interface {
	GetConnectionForExecution(context.Context, string, string) (scmconnection.Connection, error)
}

type GitWorkspacePort interface {
	Provision(context.Context, GitProvisionRequest) (GitWorkspaceResult, error)
	Resolve(context.Context, GitProvisionRequest) (GitWorkspaceResult, error)
	CreateCommit(context.Context, GitCommitRequest) (GitCommitResult, error)
	SnapshotSubmission(context.Context, GitSubmissionSnapshotRequest) (GitSubmissionSnapshot, error)
	FetchBaseHead(context.Context, GitProvisionRequest) (string, error)
	PushTodoBranch(context.Context, GitProvisionRequest, string) error
	PreviewMerge(context.Context, GitMergePreviewRequest) (GitMergePreview, error)
	MergeAuthoritative(context.Context, GitMergeRequest) (GitMergeReceipt, error)
	Export(context.Context, GitProvisionRequest, string, string) (GitExportResult, error)
	OpenExport(context.Context, GitProvisionRequest, string, string, string) (GitExportFile, error)
}

type GitProvisionRequest struct {
	TenantID, DevelopmentProjectID, TodoID, ExecutionAttemptID, RepositoryBindingID string
	SourcePath, SourceKind, DefaultBranch, BranchPrefix, BranchSlug, BaseCommit     string
}
type GitWorkspaceResult struct{ StorageKey, BranchName, BaseCommit, HeadCommit, WorktreePath string }
type GitCommitRequest struct {
	GitProvisionRequest
	PlanID, PlanDigest, ExecutionRunID, AgentID, AuthorName, AuthorEmail, Summary, SourceSubdirectory string
	AllowedFileScopes, CheckCommands                                                                  []string
}
type GitCommitResult struct {
	CommitSHA                         string
	ParentSHAs                        []string
	TreeSHA, Message, SignatureStatus string
	ChangedFiles                      []string
	Additions, Deletions              int
	DiffDigest                        string
	Checks                            []CheckEvidence
	HeadBefore                        string
	CreatedAt                         time.Time
}
type GitSubmissionSnapshotRequest struct {
	GitProvisionRequest
	HeadCommit string
}
type GitSubmissionSnapshot struct {
	BaseCommit, HeadCommit, TreeSHA string
	CommitSHAs, ChangedFiles        []string
	Additions, Deletions            int
	DiffDigest                      string
}
type GitMergePreviewRequest struct {
	GitProvisionRequest
	HeadCommit, ExpectedBaseHead, MergeStrategy, SourceSubdirectory string
	CheckCommands                                                   []string
}
type GitMergePreview struct {
	ExpectedBaseHead, HeadCommit, TreeSHA, MergeStrategy string
	Checks                                               []CheckEvidence
}
type GitMergeRequest struct {
	GitProvisionRequest
	HeadCommit, ExpectedBaseHead, ExpectedTreeSHA, MergeStrategy, Message, ActorName, ActorEmail string
}
type GitMergeReceipt struct {
	MergeCommitSHA, TreeSHA, TargetHead string
	MergedAt                            time.Time
}
type GitExportResult struct{ PatchRef, BundleRef string }
type GitExportFile struct {
	Name, ContentType string
	Size              int64
	Reader            io.ReadCloser
}

func (s *Service) SetExecutionResources(repo *ExecutionRepository, workspace GitWorkspacePort, leaseTTL, retention time.Duration) {
	if s == nil {
		return
	}
	s.executionRepo = repo
	s.gitWorkspace = workspace
	if leaseTTL <= 0 {
		leaseTTL = 2 * time.Hour
	}
	if retention <= 0 {
		retention = 14 * 24 * time.Hour
	}
	s.workspaceLeaseTTL = leaseTTL
	s.workspaceRetention = retention
}

func (s *Service) ProvisionTodoWorkspace(ctx context.Context, cmd ProvisionTodoWorkspaceCommand) (ProvisionTodoWorkspaceResult, error) {
	if s == nil || s.repo == nil || s.executionRepo == nil || s.gitWorkspace == nil || s.connections == nil {
		return ProvisionTodoWorkspaceResult{}, ErrDependency
	}
	cmd.TenantID, cmd.DevelopmentProjectID, cmd.TodoID = normalizeTenantID(cmd.TenantID), strings.TrimSpace(cmd.DevelopmentProjectID), strings.TrimSpace(cmd.TodoID)
	cmd.RepositoryBindingID, cmd.BaseCommit = strings.TrimSpace(cmd.RepositoryBindingID), strings.ToLower(strings.TrimSpace(cmd.BaseCommit))
	cmd.ResourceSnapshotDigest, cmd.NetworkMode = strings.TrimSpace(cmd.ResourceSnapshotDigest), strings.TrimSpace(cmd.NetworkMode)
	if !validTenantKeySegment(cmd.TenantID) || !validResourceID(cmd.DevelopmentProjectID) || !validResourceID(cmd.TodoID) ||
		!validResourceID(cmd.PlanID) || strings.TrimSpace(cmd.PlanDigest) == "" || !validResourceID(cmd.MissionID) ||
		!validResourceID(cmd.RepositoryBindingID) || !validGitObjectID(cmd.BaseCommit) || cmd.ResourceSnapshotDigest == "" ||
		len(cmd.AllowedFileScopes) == 0 || len(cmd.PlannedCommands) == 0 ||
		len(cmd.MissionTaskIDs) == 0 || len(cmd.MissionTaskIDs) != len(cmd.AgentIDs) {
		return ProvisionTodoWorkspaceResult{}, ErrInvalidRequest
	}
	project, err := s.repo.Get(ctx, cmd.TenantID, cmd.DevelopmentProjectID)
	if err != nil {
		return ProvisionTodoWorkspaceResult{}, err
	}
	if project.Status != StatusActive {
		return ProvisionTodoWorkspaceResult{}, ErrArchived
	}
	snapshot, err := s.ResolveExecutionPlanSnapshot(ctx, cmd.TenantID, cmd.DevelopmentProjectID)
	if err != nil {
		return ProvisionTodoWorkspaceResult{}, err
	}
	if snapshot.SnapshotDigest != cmd.ResourceSnapshotDigest {
		return ProvisionTodoWorkspaceResult{}, ErrApprovalStale
	}
	binding, err := repositoryBindingByID(project, cmd.RepositoryBindingID)
	if err != nil {
		return ProvisionTodoWorkspaceResult{}, err
	}
	if binding.Role != RepositoryRolePrimary || binding.AccessMode != AccessReadWrite || binding.Status != BindingStatusActive ||
		binding.LastFetchedCommit != cmd.BaseCommit {
		return ProvisionTodoWorkspaceResult{}, ErrApprovalStale
	}
	connections, ok := s.connections.(executionSCMConnections)
	if !ok {
		return ProvisionTodoWorkspaceResult{}, ErrDependency
	}
	connection, err := connections.GetConnectionForExecution(ctx, cmd.TenantID, binding.ConnectionID)
	if err != nil {
		return ProvisionTodoWorkspaceResult{}, err
	}
	request, err := gitRequestForExecution(project, binding, connection, cmd.TodoID, cmd.MissionID, cmd.TodoTitle, cmd.BaseCommit)
	if err != nil {
		return ProvisionTodoWorkspaceResult{}, err
	}
	workspaceID := deterministicResourceID("workspace", cmd.TenantID, project.DevelopmentProjectID+"\x00"+cmd.TodoID+"\x00"+cmd.MissionID+"\x00"+binding.BindingID)
	now := s.now()
	workspace, getErr := s.executionRepo.GetWorkspace(ctx, cmd.TenantID, workspaceID)
	if getErr != nil && !errors.Is(getErr, ErrNotFound) {
		return ProvisionTodoWorkspaceResult{}, getErr
	}
	if getErr == nil {
		if workspace.DevelopmentProjectID != project.DevelopmentProjectID || workspace.TodoID != cmd.TodoID || workspace.ExecutionAttemptID != cmd.MissionID || workspace.RepositoryBindingID != binding.BindingID || workspace.BaseCommit != cmd.BaseCommit {
			return ProvisionTodoWorkspaceResult{}, ErrWorkspaceConflict
		}
		if workspace.LeaseOwnerRunID != "" && workspace.LeaseExpiresAt.After(now) {
			return ProvisionTodoWorkspaceResult{}, ErrWorkspaceLease
		}
	} else {
		workspace = DevelopmentWorkspace{
			TenantID: cmd.TenantID, WorkspaceID: workspaceID, DevelopmentProjectID: project.DevelopmentProjectID,
			TodoID: cmd.TodoID, ExecutionAttemptID: cmd.MissionID, RepositoryBindingID: binding.BindingID, SourceKind: request.SourceKind,
			BaseCommit: cmd.BaseCommit, Status: WorkspaceStatusProvisioning, CreatedAt: now,
			LastActiveAt: now, CleanupAfter: now.Add(s.workspaceRetention), Version: 1,
		}
		created, createErr := s.executionRepo.CreateWorkspace(ctx, workspace)
		if createErr != nil {
			return ProvisionTodoWorkspaceResult{}, createErr
		}
		if !created {
			return s.ProvisionTodoWorkspace(ctx, cmd)
		}
	}
	provisioned, err := s.gitWorkspace.Provision(ctx, request)
	if err != nil {
		expected := workspace.Version
		workspace.Status, workspace.FailureReason, workspace.LastActiveAt, workspace.Version = WorkspaceStatusFailed, publicExecutionFailure(err), now, workspace.Version+1
		_ = s.executionRepo.UpdateWorkspace(context.WithoutCancel(ctx), workspace, expected)
		return ProvisionTodoWorkspaceResult{}, err
	}
	expected := workspace.Version
	workspace.StorageKey, workspace.BranchName, workspace.HeadCommit = provisioned.StorageKey, provisioned.BranchName, provisioned.HeadCommit
	workspace.SourceKind, workspace.Status, workspace.LeaseOwnerRunID = request.SourceKind, WorkspaceStatusReady, ""
	workspace.LeaseExpiresAt, workspace.LastActiveAt, workspace.CleanupAfter, workspace.FailureReason = time.Time{}, now, now.Add(s.workspaceRetention), ""
	workspace.Version++
	if err := s.executionRepo.UpdateWorkspace(ctx, workspace, expected); err != nil {
		return ProvisionTodoWorkspaceResult{}, err
	}
	runs := make([]TodoExecutionRun, 0, len(cmd.MissionTaskIDs))
	for index, taskID := range cmd.MissionTaskIDs {
		runID := deterministicResourceID("devrun", cmd.TenantID, cmd.MissionID+"\x00"+taskID)
		run := TodoExecutionRun{
			TenantID: cmd.TenantID, ExecutionRunID: runID, DevelopmentProjectID: project.DevelopmentProjectID,
			TodoID: cmd.TodoID, PlanID: cmd.PlanID, PlanDigest: cmd.PlanDigest, ApprovalID: cmd.ApprovalID,
			MissionID: cmd.MissionID, MissionTaskID: taskID, WorkspaceID: workspace.WorkspaceID, AgentID: strings.TrimSpace(cmd.AgentIDs[index]),
			ResourceSnapshotDigest: cmd.ResourceSnapshotDigest, AllowedFileScopes: append([]string(nil), cmd.AllowedFileScopes...),
			PlannedCommands: append([]string(nil), cmd.PlannedCommands...), NetworkMode: cmd.NetworkMode,
			AllowedHosts: append([]string(nil), cmd.AllowedHosts...), RequiredSecretRefs: append([]string(nil), cmd.RequiredSecretRefs...),
			Status: ExecutionRunStatusReady, CommitBefore: workspace.HeadCommit, CreatedAt: now, UpdatedAt: now, Version: 1,
		}
		created, createErr := s.executionRepo.CreateRun(ctx, run)
		if createErr != nil {
			return ProvisionTodoWorkspaceResult{}, createErr
		}
		if !created {
			existing, existingErr := s.executionRepo.GetRun(ctx, cmd.TenantID, runID)
			if existingErr != nil {
				return ProvisionTodoWorkspaceResult{}, existingErr
			}
			if existing.MissionID != run.MissionID || existing.MissionTaskID != run.MissionTaskID || existing.WorkspaceID != run.WorkspaceID || existing.AgentID != run.AgentID || existing.PlanDigest != run.PlanDigest {
				return ProvisionTodoWorkspaceResult{}, ErrIdempotency
			}
			run = existing
		}
		runs = append(runs, run)
	}
	if err := s.recordExecutionAudit(ctx, cmd.TenantID, cmd.MissionID, workspace.WorkspaceID, "development_workspace.provisioned", "succeeded"); err != nil {
		return ProvisionTodoWorkspaceResult{}, err
	}
	return ProvisionTodoWorkspaceResult{Workspace: workspace, Runs: runs}, nil
}

func (s *Service) ResolveWorkspaceForRun(ctx context.Context, tenantID, workspaceID, runID string) (string, error) {
	resolved, err := s.ResolveManagedWorkspace(ctx, tenantID, workspaceID, runID)
	return resolved.WorkDir, err
}

func (s *Service) PrepareAgentRun(ctx context.Context, tenantID, missionID, missionTaskID, agentID, agentRunID string) (PreparedAgentRun, bool, error) {
	if s == nil || s.executionRepo == nil {
		return PreparedAgentRun{}, false, ErrDependency
	}
	tenantID, missionID, missionTaskID = normalizeTenantID(tenantID), strings.TrimSpace(missionID), strings.TrimSpace(missionTaskID)
	agentID, agentRunID = strings.TrimSpace(agentID), strings.TrimSpace(agentRunID)
	if !validTenantKeySegment(tenantID) || !validResourceID(missionID) || !validResourceID(missionTaskID) || !validResourceID(agentID) || !validResourceID(agentRunID) {
		return PreparedAgentRun{}, false, ErrInvalidRequest
	}
	run, err := s.executionRepo.FindRunForMissionTask(ctx, tenantID, missionID, missionTaskID)
	if errors.Is(err, ErrNotFound) {
		return PreparedAgentRun{}, false, nil
	}
	if err != nil {
		return PreparedAgentRun{}, false, err
	}
	if run.AgentID != agentID {
		return PreparedAgentRun{}, true, ErrNotFound
	}
	if run.AgentRunID != "" && run.AgentRunID != agentRunID && run.Status == ExecutionRunStatusRunning {
		return PreparedAgentRun{}, true, ErrWorkspaceLease
	}
	if run.Status == ExecutionRunStatusReview || run.Status == ExecutionRunStatusCompleted || run.Status == ExecutionRunStatusCanceled {
		return PreparedAgentRun{}, true, ErrInvalidState
	}
	workspace, err := s.executionRepo.GetWorkspace(ctx, tenantID, run.WorkspaceID)
	if err != nil {
		return PreparedAgentRun{}, true, err
	}
	now := s.now()
	if workspace.Status != WorkspaceStatusReady && workspace.Status != WorkspaceStatusReview && workspace.Status != WorkspaceStatusLeased {
		return PreparedAgentRun{}, true, ErrInvalidState
	}
	if workspace.LeaseOwnerRunID != "" && workspace.LeaseOwnerRunID != run.ExecutionRunID && workspace.LeaseExpiresAt.After(now) {
		return PreparedAgentRun{}, true, ErrWorkspaceLease
	}
	expectedWorkspace := workspace.Version
	workspace.Status, workspace.LeaseOwnerRunID = WorkspaceStatusLeased, run.ExecutionRunID
	workspace.LeaseExpiresAt, workspace.LastActiveAt, workspace.Version = now.Add(s.workspaceLeaseTTL), now, workspace.Version+1
	if err := s.executionRepo.UpdateWorkspace(ctx, workspace, expectedWorkspace); err != nil {
		return PreparedAgentRun{}, true, err
	}
	expectedRun := run.Version
	run.AgentRunID, run.Status, run.CommitBefore = agentRunID, ExecutionRunStatusRunning, workspace.HeadCommit
	if run.StartedAt.IsZero() {
		run.StartedAt = now
	}
	run.FailureReason, run.UpdatedAt, run.Version = "", now, run.Version+1
	if err := s.executionRepo.UpdateRun(ctx, run, expectedRun); err != nil {
		return PreparedAgentRun{}, true, err
	}
	resolved, err := s.ResolveManagedWorkspace(ctx, tenantID, workspace.WorkspaceID, run.ExecutionRunID)
	if err != nil {
		return PreparedAgentRun{}, true, err
	}
	return PreparedAgentRun{
		ExecutionRunID: run.ExecutionRunID, WorkspaceID: workspace.WorkspaceID,
		RootDir: resolved.RootDir, WorkDir: resolved.WorkDir, NetworkMode: run.NetworkMode,
		AllowedHosts: append([]string(nil), run.AllowedHosts...),
	}, true, nil
}

func (s *Service) CommitAgentRunResult(ctx context.Context, tenantID, missionID, missionTaskID, agentID, agentRunID, summary string) (TodoCommitRecord, bool, error) {
	if s == nil || s.executionRepo == nil {
		return TodoCommitRecord{}, false, ErrDependency
	}
	run, err := s.executionRepo.FindRunForMissionTask(ctx, tenantID, missionID, missionTaskID)
	if errors.Is(err, ErrNotFound) {
		return TodoCommitRecord{}, false, nil
	}
	if err != nil {
		return TodoCommitRecord{}, false, err
	}
	if run.AgentID != strings.TrimSpace(agentID) || run.AgentRunID != strings.TrimSpace(agentRunID) || (run.Status != ExecutionRunStatusRunning && run.Status != ExecutionRunStatusReview) {
		return TodoCommitRecord{}, true, ErrNotFound
	}
	viewer := ViewerRef{
		ViewerID: run.AgentID, ViewerKind: identity.ViewerKindAgent, TenantID: run.TenantID,
		AuthorizationSubjectID: run.AgentID, PermissionScope: []string{"development.execute"},
	}
	record, err := s.CreateControlledCommit(ctx, viewer, run.TodoID, CreateCommitCommand{
		IdempotencyKey: "agent-result:" + strings.TrimSpace(agentRunID), ExecutionRunID: run.ExecutionRunID,
		ExpectedRunVersion: run.Version, Summary: strings.TrimSpace(summary),
	})
	return record, true, err
}

func (s *Service) FailAgentRun(ctx context.Context, tenantID, missionID, missionTaskID, agentID, agentRunID string, cause error) error {
	if s == nil || s.executionRepo == nil {
		return ErrDependency
	}
	run, err := s.executionRepo.FindRunForMissionTask(ctx, tenantID, missionID, missionTaskID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if run.AgentID != strings.TrimSpace(agentID) || run.AgentRunID != strings.TrimSpace(agentRunID) {
		return ErrNotFound
	}
	now := s.now()
	expectedRun := run.Version
	run.Status, run.FailureReason, run.FinishedAt, run.UpdatedAt, run.Version = ExecutionRunStatusFailed, publicExecutionFailure(cause), now, now, run.Version+1
	if err := s.executionRepo.UpdateRun(ctx, run, expectedRun); err != nil && !errors.Is(err, ErrConflict) {
		return err
	}
	workspace, err := s.executionRepo.GetWorkspace(ctx, tenantID, run.WorkspaceID)
	if err != nil {
		return err
	}
	if workspace.LeaseOwnerRunID == run.ExecutionRunID {
		expectedWorkspace := workspace.Version
		workspace.Status, workspace.LeaseOwnerRunID, workspace.LeaseExpiresAt = WorkspaceStatusReady, "", time.Time{}
		workspace.LastActiveAt, workspace.Version = now, workspace.Version+1
		if err := s.executionRepo.UpdateWorkspace(ctx, workspace, expectedWorkspace); err != nil && !errors.Is(err, ErrConflict) {
			return err
		}
	}
	return nil
}

func (s *Service) ResolveManagedWorkspace(ctx context.Context, tenantID, workspaceID, runID string) (ResolvedManagedWorkspace, error) {
	if s == nil || s.executionRepo == nil || s.gitWorkspace == nil || s.repo == nil || s.connections == nil {
		return ResolvedManagedWorkspace{}, ErrDependency
	}
	workspace, err := s.executionRepo.GetWorkspace(ctx, tenantID, workspaceID)
	if err != nil {
		return ResolvedManagedWorkspace{}, err
	}
	if workspace.Status != WorkspaceStatusLeased && workspace.Status != WorkspaceStatusReady && workspace.Status != WorkspaceStatusReview {
		return ResolvedManagedWorkspace{}, ErrInvalidState
	}
	if workspace.LeaseOwnerRunID != "" && workspace.LeaseExpiresAt.Before(s.now()) {
		return ResolvedManagedWorkspace{}, ErrWorkspaceLease
	}
	var selectedRun *TodoExecutionRun
	if runID != "" {
		run, runErr := s.executionRepo.GetRun(ctx, tenantID, runID)
		if runErr != nil || run.WorkspaceID != workspace.WorkspaceID || run.Status != ExecutionRunStatusRunning || workspace.LeaseOwnerRunID != run.ExecutionRunID {
			return ResolvedManagedWorkspace{}, ErrNotFound
		}
		selectedRun = &run
	}
	project, err := s.repo.Get(ctx, tenantID, workspace.DevelopmentProjectID)
	if err != nil {
		return ResolvedManagedWorkspace{}, err
	}
	binding, err := repositoryBindingByID(project, workspace.RepositoryBindingID)
	if err != nil {
		return ResolvedManagedWorkspace{}, err
	}
	connections, ok := s.connections.(executionSCMConnections)
	if !ok {
		return ResolvedManagedWorkspace{}, ErrDependency
	}
	connection, err := connections.GetConnectionForExecution(ctx, tenantID, binding.ConnectionID)
	if err != nil {
		return ResolvedManagedWorkspace{}, err
	}
	attemptID := firstNonBlank(workspace.ExecutionAttemptID, workspace.TodoID)
	request, err := gitRequestForExecution(project, binding, connection, workspace.TodoID, attemptID, workspace.TodoID, workspace.BaseCommit)
	if err != nil {
		return ResolvedManagedWorkspace{}, err
	}
	resolved, err := s.gitWorkspace.Resolve(ctx, request)
	if err != nil {
		return ResolvedManagedWorkspace{}, err
	}
	if resolved.StorageKey != workspace.StorageKey {
		return ResolvedManagedWorkspace{}, ErrWorkspaceConflict
	}
	workDir := resolved.WorktreePath
	if subdirectory := strings.TrimSpace(binding.SourceSubdirectory); subdirectory != "" {
		workDir = filepath.Join(resolved.WorktreePath, filepath.FromSlash(subdirectory))
	}
	canonicalRoot, rootErr := filepath.EvalSymlinks(resolved.WorktreePath)
	canonicalWorkDir, workErr := filepath.EvalSymlinks(workDir)
	if rootErr != nil || workErr != nil {
		return ResolvedManagedWorkspace{}, ErrWorkspaceConflict
	}
	relative, relErr := filepath.Rel(canonicalRoot, canonicalWorkDir)
	if relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return ResolvedManagedWorkspace{}, ErrWorkspaceConflict
	}
	network := project.ExecutionPolicy.NetworkMode
	if selectedRun != nil {
		network = selectedRun.NetworkMode
	}
	return ResolvedManagedWorkspace{RootDir: canonicalRoot, WorkDir: canonicalWorkDir, NetworkMode: network}, nil
}

func writablePrimaryBinding(project Project) (RepositoryBinding, error) {
	for _, binding := range project.RepositoryBindings {
		if binding.Role == RepositoryRolePrimary && binding.AccessMode == AccessReadWrite && binding.Status == BindingStatusActive && validGitObjectID(binding.LastFetchedCommit) {
			return binding, nil
		}
	}
	return RepositoryBinding{}, fmt.Errorf("%w: active writable primary repository is required", ErrRepositoryPolicy)
}

func repositoryBindingByID(project Project, bindingID string) (RepositoryBinding, error) {
	for _, binding := range project.RepositoryBindings {
		if binding.BindingID == bindingID {
			return binding, nil
		}
	}
	return RepositoryBinding{}, ErrNotFound
}

func gitRequestForExecution(project Project, binding RepositoryBinding, connection scmconnection.Connection, todoID, attemptID, title, baseCommit string) (GitProvisionRequest, error) {
	if connection.Provider != scmconnection.ProviderManagedLocal || !strings.HasPrefix(binding.SourceKind, "managed_") {
		return GitProvisionRequest{}, fmt.Errorf("%w: repository provider workspace adapter is unavailable", ErrDependency)
	}
	return GitProvisionRequest{
		TenantID: project.TenantID, DevelopmentProjectID: project.DevelopmentProjectID, TodoID: todoID, ExecutionAttemptID: attemptID,
		RepositoryBindingID: binding.BindingID, SourcePath: connection.BaseURL, SourceKind: binding.SourceKind,
		DefaultBranch: binding.DefaultBranch, BranchPrefix: binding.BranchPrefix, BranchSlug: title, BaseCommit: baseCommit,
	}, nil
}

func publicExecutionFailure(err error) string {
	switch {
	case errors.Is(err, ErrWorkspaceLease):
		return "workspace is leased by another active execution"
	case errors.Is(err, ErrRepositoryPolicy):
		return "repository policy rejected the operation"
	default:
		return "managed workspace operation failed"
	}
}

func (s *Service) recordExecutionAudit(ctx context.Context, tenantID, actorID, entityID, action, result string) error {
	if s == nil || s.audit == nil {
		return ErrDependency
	}
	auditID := deterministicResourceID("audit", normalizeTenantID(tenantID), strings.TrimSpace(entityID)+"\x00"+strings.TrimSpace(action)+"\x00"+strings.TrimSpace(result))
	return s.audit.RecordDevelopmentProjectAudit(ctx, AuditEvent{AuditID: auditID, TenantID: tenantID, ActorID: actorID, EntityID: entityID, Action: action, Result: result, Summary: action, OccurredAt: s.now()})
}

func hashValue(value any) string {
	payload, _ := json.Marshal(value)
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func humanExecutionViewer(viewer ViewerRef) bool {
	return strings.EqualFold(viewer.ViewerKind, identity.ViewerKindUser)
}
