package developmentproject

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"orrery_v2/internal/platform/db"
)

const projectTable = "core.development_projects"

type Repository interface {
	Create(ctx context.Context, tenantID string, project Project) error
	Update(ctx context.Context, tenantID string, project Project, expectedVersion int64) error
	Get(ctx context.Context, tenantID, projectID string) (Project, error)
	List(ctx context.Context, tenantID string) ([]Project, error)
	ListCatalog(ctx context.Context, afterKey string, limit int) ([]Project, bool, error)
	Delete(ctx context.Context, tenantID, projectID string) error
}

type PlatformRepository struct{ router *db.Router }

func NewPlatformRepository(router *db.Router) *PlatformRepository {
	return &PlatformRepository{router: router}
}

func (r *PlatformRepository) Create(ctx context.Context, tenantID string, project Project) error {
	if r == nil || r.router == nil {
		return ErrDependency
	}
	project.TenantID = normalizeTenantID(tenantID)
	if !validTenantKeySegment(project.TenantID) || !validResourceID(project.DevelopmentProjectID) {
		return ErrInvalidRequest
	}
	created, err := r.router.CreateRecord(ctx, projectTable, projectKey(project.TenantID, project.DevelopmentProjectID), projectRecord(project))
	if err != nil {
		return err
	}
	if !created {
		return ErrConflict
	}
	return nil
}

func (r *PlatformRepository) Update(ctx context.Context, tenantID string, project Project, expectedVersion int64) error {
	if r == nil || r.router == nil {
		return ErrDependency
	}
	tenantID = normalizeTenantID(tenantID)
	if !validTenantKeySegment(tenantID) || !validResourceID(project.DevelopmentProjectID) {
		return ErrInvalidRequest
	}
	key := projectKey(tenantID, project.DevelopmentProjectID)
	current, ok, err := r.router.GetRecord(ctx, projectTable, key)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	if expectedVersion <= 0 || recordInt64(current["version"]) != expectedVersion {
		return ErrConflict
	}
	project.TenantID = tenantID
	swapped, err := r.router.CompareAndSwapRecord(ctx, projectTable, key, current, projectRecord(project))
	if err != nil {
		return err
	}
	if !swapped {
		return ErrConflict
	}
	return nil
}

func (r *PlatformRepository) Get(ctx context.Context, tenantID, projectID string) (Project, error) {
	if r == nil || r.router == nil {
		return Project{}, ErrDependency
	}
	tenantID = normalizeTenantID(tenantID)
	projectID = strings.TrimSpace(projectID)
	if !validTenantKeySegment(tenantID) || !validResourceID(projectID) {
		return Project{}, ErrInvalidRequest
	}
	record, ok, err := r.router.GetRecord(ctx, projectTable, projectKey(tenantID, projectID))
	if err != nil {
		return Project{}, err
	}
	if !ok {
		return Project{}, ErrNotFound
	}
	return projectFromRecord(record, tenantID, projectID)
}

func (r *PlatformRepository) List(ctx context.Context, tenantID string) ([]Project, error) {
	if r == nil || r.router == nil {
		return nil, ErrDependency
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	store, table, err := r.router.StoreForTable(projectTable)
	if err != nil {
		return nil, err
	}
	records, err := store.ListRecordTable(ctx, table)
	if err != nil {
		return nil, err
	}
	tenantID = normalizeTenantID(tenantID)
	out := make([]Project, 0)
	for key, record := range records {
		project, err := projectFromRecord(record, "", "")
		if err != nil {
			return nil, err
		}
		if projectKey(project.TenantID, project.DevelopmentProjectID) != key {
			return nil, fmt.Errorf("%w: development project record key does not match its identity", ErrDependency)
		}
		if project.TenantID == tenantID {
			out = append(out, project)
		}
	}
	sortProjects(out)
	return out, nil
}

func (r *PlatformRepository) ListCatalog(ctx context.Context, afterKey string, limit int) ([]Project, bool, error) {
	if r == nil || r.router == nil {
		return nil, false, ErrDependency
	}
	if limit <= 0 {
		return nil, false, nil
	}
	page, err := r.router.ListRecords(ctx, projectTable, strings.TrimSpace(afterKey), limit)
	if err != nil {
		return nil, false, err
	}
	out := make([]Project, 0, len(page.Items))
	for _, item := range page.Items {
		project, err := projectFromRecord(item.Record, "", "")
		if err != nil {
			return nil, false, err
		}
		if projectKey(project.TenantID, project.DevelopmentProjectID) != item.Key {
			return nil, false, fmt.Errorf("%w: development project record key does not match its identity", ErrDependency)
		}
		out = append(out, project)
	}
	return out, page.HasMore, nil
}

func (r *PlatformRepository) Delete(ctx context.Context, tenantID, projectID string) error {
	if r == nil || r.router == nil {
		return ErrDependency
	}
	return r.router.DeleteRecord(ctx, projectTable, projectKey(tenantID, projectID))
}

func projectRecord(project Project) db.Record {
	payload, _ := json.Marshal(project)
	return db.Record{
		"tenant_id": normalizeTenantID(project.TenantID), "development_project_id": strings.TrimSpace(project.DevelopmentProjectID),
		"organization_id": strings.TrimSpace(project.OrganizationID), "status": project.Status,
		"version": project.Version, "payload": string(payload),
	}
}

func projectFromRecord(record db.Record, expectedTenantID, expectedProjectID string) (Project, error) {
	payload, _ := record["payload"].(string)
	if payload == "" {
		return Project{}, fmt.Errorf("%w: development project payload is missing", ErrDependency)
	}
	var project Project
	if err := json.Unmarshal([]byte(payload), &project); err != nil {
		return Project{}, fmt.Errorf("decode development project: %w", err)
	}
	project.TenantID = normalizeTenantID(project.TenantID)
	project.DevelopmentProjectID = strings.TrimSpace(project.DevelopmentProjectID)
	recordTenantID, _ := record["tenant_id"].(string)
	recordTenantID = normalizeTenantID(recordTenantID)
	recordProjectID, _ := record["development_project_id"].(string)
	recordProjectID = strings.TrimSpace(recordProjectID)
	if !validTenantKeySegment(project.TenantID) || !validResourceID(project.DevelopmentProjectID) ||
		project.TenantID != recordTenantID || project.DevelopmentProjectID != recordProjectID ||
		strings.TrimSpace(project.OrganizationID) != recordString(record, "organization_id") ||
		project.Status != recordString(record, "status") || project.Version <= 0 || project.Version != recordInt64(record["version"]) ||
		(expectedTenantID != "" && project.TenantID != normalizeTenantID(expectedTenantID)) ||
		(expectedProjectID != "" && project.DevelopmentProjectID != strings.TrimSpace(expectedProjectID)) {
		return Project{}, fmt.Errorf("%w: development project record identity is inconsistent", ErrDependency)
	}
	if !validSubjectID(project.CreatedBy) || (project.OrganizationID != "" && !validSubjectID(project.OrganizationID)) {
		return Project{}, fmt.Errorf("%w: development project ownership identity is inconsistent", ErrDependency)
	}
	if !oneOf(project.Status, StatusActive, StatusArchived) ||
		validateProjectValues(project.Name, project.DefaultAutomationMode, project.MaxConcurrentRuns, project.ExecutionPolicy) != nil ||
		len(project.Description) > 8192 || strings.Contains(project.Description, "\x00") ||
		project.CreatedAt.IsZero() || project.UpdatedAt.Before(project.CreatedAt) {
		return Project{}, fmt.Errorf("%w: development project aggregate is invalid", ErrDependency)
	}
	memberIDs := make(map[string]struct{}, len(project.Members))
	memberSubjects := make(map[string]struct{}, len(project.Members))
	owners := 0
	for _, member := range project.Members {
		if validatePersistedMember(member) != nil {
			return Project{}, fmt.Errorf("%w: development project member identity is inconsistent", ErrDependency)
		}
		if member.Role == MemberRoleOwner {
			owners++
		}
		subjectKey := member.SubjectKind + "\x00" + member.SubjectID
		if _, exists := memberIDs[member.MemberID]; exists {
			return Project{}, fmt.Errorf("%w: development project member identity is duplicated", ErrDependency)
		}
		if _, exists := memberSubjects[subjectKey]; exists {
			return Project{}, fmt.Errorf("%w: development project member subject is duplicated", ErrDependency)
		}
		memberIDs[member.MemberID] = struct{}{}
		memberSubjects[subjectKey] = struct{}{}
	}
	if owners == 0 {
		return Project{}, fmt.Errorf("%w: development project must retain an owner", ErrDependency)
	}
	bindingIDs := make(map[string]struct{}, len(project.RepositoryBindings))
	bindingRepositories := make(map[string]struct{}, len(project.RepositoryBindings))
	writablePrimaries := 0
	for _, binding := range project.RepositoryBindings {
		if validatePersistedRepositoryBinding(binding) != nil {
			return Project{}, fmt.Errorf("%w: development project repository identity is inconsistent", ErrDependency)
		}
		if _, exists := bindingIDs[binding.BindingID]; exists {
			return Project{}, fmt.Errorf("%w: development project repository identity is duplicated", ErrDependency)
		}
		bindingIDs[binding.BindingID] = struct{}{}
		repositoryKey := binding.ConnectionID + "\x00" + binding.RepositoryLocator
		if _, exists := bindingRepositories[repositoryKey]; exists {
			return Project{}, fmt.Errorf("%w: development project repository is duplicated", ErrDependency)
		}
		bindingRepositories[repositoryKey] = struct{}{}
		if binding.Role == RepositoryRolePrimary && binding.AccessMode == AccessReadWrite {
			writablePrimaries++
		}
	}
	if writablePrimaries > 1 {
		return Project{}, fmt.Errorf("%w: development project has multiple writable primary repositories", ErrDependency)
	}
	effectIDs := make(map[string]struct{}, len(project.PendingEffects))
	for _, effect := range project.PendingEffects {
		if validateProjectPendingEffect(project, effect) != nil {
			return Project{}, fmt.Errorf("%w: development project pending effect is invalid", ErrDependency)
		}
		if _, exists := effectIDs[effect.EffectID]; exists {
			return Project{}, fmt.Errorf("%w: development project pending effect is duplicated", ErrDependency)
		}
		effectIDs[effect.EffectID] = struct{}{}
	}
	if len(project.MutationReceipts) > 128 {
		return Project{}, fmt.Errorf("%w: development project has too many mutation receipts", ErrDependency)
	}
	receiptKeys := make(map[string]struct{}, len(project.MutationReceipts))
	for _, receipt := range project.MutationReceipts {
		if validateProjectMutationReceipt(receipt) != nil {
			return Project{}, fmt.Errorf("%w: development project mutation receipt is invalid", ErrDependency)
		}
		if _, exists := receiptKeys[receipt.IdempotencyKey]; exists {
			return Project{}, fmt.Errorf("%w: development project mutation receipt is duplicated", ErrDependency)
		}
		receiptKeys[receipt.IdempotencyKey] = struct{}{}
	}
	if (project.CreateRequestHash != "" && !validRequestHash(project.CreateRequestHash)) ||
		(project.CreateIdempotencyKey != "" && (!validIdempotencyKey(project.CreateIdempotencyKey) || project.CreateRequestHash == "")) {
		return Project{}, fmt.Errorf("%w: development project create receipt is invalid", ErrDependency)
	}
	return project, nil
}

func validatePersistedMember(member Member) error {
	if !validResourceID(member.MemberID) || member.Version <= 0 ||
		validateMember(member.SubjectKind, member.SubjectID, member.Role) != nil ||
		!validSubjectID(member.CreatedBy) || member.CreatedAt.IsZero() || member.UpdatedAt.Before(member.CreatedAt) {
		return ErrDependency
	}
	return nil
}

func validatePersistedRepositoryBinding(binding RepositoryBinding) error {
	if !validResourceID(binding.BindingID) || binding.Version <= 0 ||
		!oneOf(binding.Status, BindingStatusActive, BindingStatusError) ||
		binding.DisplayName == "" || len(binding.DisplayName) > 256 || containsControl(binding.DisplayName) ||
		!validGitRefPart(binding.DefaultBranch) || !validGitObjectID(binding.LastFetchedCommit) ||
		binding.LastVerifiedAt.IsZero() || binding.CreatedAt.IsZero() || binding.UpdatedAt.Before(binding.CreatedAt) {
		return ErrDependency
	}
	command := BindRepositoryCommand{
		ConnectionID: binding.ConnectionID, RepositoryLocator: binding.RepositoryLocator,
		Role: binding.Role, AccessMode: binding.AccessMode, DefaultBranch: binding.DefaultBranch,
		SourceSubdirectory: binding.SourceSubdirectory, BuildCommands: binding.BuildCommands,
		TestCommands: binding.TestCommands, BranchPrefix: binding.BranchPrefix,
	}
	return validateRepositoryCommand(command)
}

func validateProjectPendingEffect(project Project, effect PendingEffect) error {
	if strings.TrimSpace(effect.EffectID) == "" || len(effect.EffectID) > 2048 || containsControl(effect.EffectID) {
		return ErrDependency
	}
	switch effect.Kind {
	case effectWriteProjectCreated:
		if validateMember(effect.SubjectKind, effect.SubjectID, MemberRoleContributor) != nil || effect.Relation != "" || effect.ConnectionID != "" || effect.Audit != nil {
			return ErrDependency
		}
	case effectWriteMember, effectDeleteMember:
		if validateMember(effect.SubjectKind, effect.SubjectID, effect.Relation) != nil || effect.ConnectionID != "" || effect.Audit != nil {
			return ErrDependency
		}
	case effectWriteConnectionUser, effectDeleteConnectionUser:
		if !validResourceID(effect.ConnectionID) || effect.SubjectKind != "" || effect.SubjectID != "" || effect.Relation != "" || effect.Audit != nil {
			return ErrDependency
		}
	case effectRecordAudit:
		if effect.SubjectKind != "" || effect.SubjectID != "" || effect.Relation != "" || effect.ConnectionID != "" || validateProjectAudit(project, effect) != nil {
			return ErrDependency
		}
	default:
		return ErrDependency
	}
	return nil
}

func validateProjectAudit(project Project, effect PendingEffect) error {
	if effect.Audit == nil {
		return ErrDependency
	}
	audit := effect.Audit
	if audit.AuditID != effect.EffectID || normalizeTenantID(audit.TenantID) != project.TenantID ||
		audit.EntityID != project.DevelopmentProjectID || !validSubjectID(audit.ActorID) ||
		strings.TrimSpace(audit.Action) == "" || strings.TrimSpace(audit.Result) == "" ||
		strings.TrimSpace(audit.Summary) == "" || audit.OccurredAt.IsZero() {
		return ErrDependency
	}
	for key, value := range audit.Metadata {
		if strings.TrimSpace(key) == "" || containsControl(key) || containsControl(value) {
			return ErrDependency
		}
	}
	return nil
}

func validateProjectMutationReceipt(receipt MutationReceipt) error {
	if !validIdempotencyKey(receipt.IdempotencyKey) || !validRequestHash(receipt.RequestHash) {
		return ErrDependency
	}
	hasFailure := receipt.VerificationFail != "" || receipt.VerificationFailureCode != ""
	if (receipt.VerificationFail == "") != (receipt.VerificationFailureCode == "") {
		return ErrDependency
	}
	switch receipt.Operation {
	case "update", "archive", "member.remove", "repository.remove":
		if receipt.Member != nil || receipt.Repository != nil || hasFailure {
			return ErrDependency
		}
	case "member.add", "member.update":
		if receipt.Member == nil || receipt.Repository != nil || hasFailure || validatePersistedMember(*receipt.Member) != nil {
			return ErrDependency
		}
	case "repository.bind", "repository.update":
		if receipt.Member != nil || receipt.Repository == nil || hasFailure || validatePersistedRepositoryBinding(*receipt.Repository) != nil {
			return ErrDependency
		}
	case "repository.verify":
		if receipt.Member != nil || receipt.Repository == nil || validatePersistedRepositoryBinding(*receipt.Repository) != nil {
			return ErrDependency
		}
		if hasFailure {
			if _, err := repositoryVerificationError(receipt.VerificationFailureCode, receipt.VerificationFail); err != nil {
				return ErrDependency
			}
		}
	default:
		return ErrDependency
	}
	return nil
}

func validRequestHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func projectKey(tenantID, projectID string) string {
	return normalizeTenantID(tenantID) + "/" + strings.TrimSpace(projectID)
}

func normalizeTenantID(value string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return "global"
}

func validTenantKeySegment(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 256 && !strings.ContainsAny(value, "/\x00\r\n")
}

func sortProjects(projects []Project) {
	sort.Slice(projects, func(i, j int) bool {
		if projects[i].UpdatedAt.Equal(projects[j].UpdatedAt) {
			return projects[i].DevelopmentProjectID < projects[j].DevelopmentProjectID
		}
		return projects[i].UpdatedAt.After(projects[j].UpdatedAt)
	})
}

func recordInt64(value any) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	default:
		return 0
	}
}

func recordString(record db.Record, field string) string {
	value, _ := record[field].(string)
	return strings.TrimSpace(value)
}
