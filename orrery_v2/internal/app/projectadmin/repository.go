package projectadmin

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"orrery_v2/internal/platform/db"
)

const projectTable = "core.projects"

type MemoryRepository struct {
	mu       sync.RWMutex
	projects map[string]Project
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{projects: map[string]Project{}}
}

func (r *MemoryRepository) SaveProject(ctx context.Context, project Project) error {
	if r == nil {
		return ErrServiceDependency
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	project.TenantID = tenantID(project.TenantID)
	if !validProjectIdentity(project.TenantID, project.ProjectID) {
		return ErrInvalidRequest
	}
	r.projects[projectKey(project.TenantID, project.ProjectID)] = project
	return nil
}

func (r *MemoryRepository) CreateProject(ctx context.Context, tenant string, project Project) error {
	if r == nil {
		return ErrServiceDependency
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	project.TenantID = tenantID(tenant)
	if !validProjectIdentity(project.TenantID, project.ProjectID) {
		return ErrInvalidRequest
	}
	key := projectKey(project.TenantID, project.ProjectID)
	if _, exists := r.projects[key]; exists {
		return ErrConflict
	}
	r.projects[key] = project
	return nil
}

func (r *MemoryRepository) UpdateProject(ctx context.Context, tenant string, project Project, expectedVersion int64) error {
	if r == nil {
		return ErrServiceDependency
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	project.TenantID = tenantID(tenant)
	if !validProjectIdentity(project.TenantID, project.ProjectID) {
		return ErrInvalidRequest
	}
	key := projectKey(project.TenantID, project.ProjectID)
	current, exists := r.projects[key]
	if !exists {
		return ErrNotFound
	}
	if expectedVersion <= 0 || current.Version != expectedVersion {
		return ErrConflict
	}
	r.projects[key] = project
	return nil
}

func (r *MemoryRepository) DeleteProject(ctx context.Context, tenant string, project Project) error {
	if r == nil {
		return ErrServiceDependency
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := projectKey(tenant, project.ProjectID)
	if !validProjectIdentity(tenantID(tenant), project.ProjectID) {
		return ErrInvalidRequest
	}
	current, exists := r.projects[key]
	if !exists {
		return nil
	}
	if current.Version != project.Version {
		return ErrConflict
	}
	delete(r.projects, key)
	return nil
}

func (r *MemoryRepository) GetProject(ctx context.Context, tenant string, projectID string) (Project, error) {
	if r == nil {
		return Project{}, ErrServiceDependency
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !validProjectIdentity(tenantID(tenant), projectID) {
		return Project{}, ErrInvalidRequest
	}
	project, ok := r.projects[projectKey(tenant, projectID)]
	if !ok {
		return Project{}, ErrNotFound
	}
	return project, nil
}

func (r *MemoryRepository) ListProjects(ctx context.Context, tenant string) ([]Project, error) {
	if r == nil {
		return nil, ErrServiceDependency
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	projects := make([]Project, 0, len(r.projects))
	for _, project := range r.projects {
		if project.TenantID != tenantID(tenant) {
			continue
		}
		projects = append(projects, project)
	}
	return projects, nil
}

func (r *MemoryRepository) ListProjectCatalog(ctx context.Context, afterID string, limit int) ([]Project, bool, error) {
	if r == nil {
		return nil, false, ErrServiceDependency
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if limit <= 0 {
		return nil, false, nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	keys := make([]string, 0, len(r.projects))
	for key := range r.projects {
		if key > afterID {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	hasMore := len(keys) > limit
	if hasMore {
		keys = keys[:limit]
	}
	projects := make([]Project, 0, len(keys))
	for _, key := range keys {
		projects = append(projects, r.projects[key])
	}
	return projects, hasMore, nil
}

type PlatformRepository struct {
	router *db.Router
}

func NewPlatformRepository(router *db.Router) *PlatformRepository {
	return &PlatformRepository{router: router}
}

func (r *PlatformRepository) SaveProject(ctx context.Context, project Project) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	project.TenantID = tenantID(project.TenantID)
	if !validProjectIdentity(project.TenantID, project.ProjectID) {
		return ErrInvalidRequest
	}
	return r.router.PutRecord(ctx, projectTable, projectKey(project.TenantID, project.ProjectID), projectRecord(project))
}

func (r *PlatformRepository) CreateProject(ctx context.Context, tenant string, project Project) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	project.TenantID = tenantID(tenant)
	if !validProjectIdentity(project.TenantID, project.ProjectID) {
		return ErrInvalidRequest
	}
	created, err := r.router.CreateRecord(ctx, projectTable, projectKey(project.TenantID, project.ProjectID), projectRecord(project))
	if err != nil {
		return err
	}
	if !created {
		return ErrConflict
	}
	return nil
}

func (r *PlatformRepository) UpdateProject(ctx context.Context, tenant string, project Project, expectedVersion int64) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	project.TenantID = tenantID(tenant)
	if !validProjectIdentity(project.TenantID, project.ProjectID) {
		return ErrInvalidRequest
	}
	key := projectKey(project.TenantID, project.ProjectID)
	current, ok, err := r.router.GetRecord(ctx, projectTable, key)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	if int64Value(current["version"]) != expectedVersion || expectedVersion <= 0 {
		return ErrConflict
	}
	swapped, err := r.router.CompareAndSwapRecord(ctx, projectTable, key, current, projectRecord(project))
	if err != nil {
		return err
	}
	if !swapped {
		return ErrConflict
	}
	return nil
}

func (r *PlatformRepository) DeleteProject(ctx context.Context, tenant string, project Project) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	project.TenantID = tenantID(tenant)
	if !validProjectIdentity(project.TenantID, project.ProjectID) {
		return ErrInvalidRequest
	}
	key := projectKey(project.TenantID, project.ProjectID)
	deleted, err := r.router.CompareAndDeleteRecord(ctx, projectTable, key, projectRecord(project))
	if err != nil {
		return err
	}
	if deleted {
		return nil
	}
	current, ok, err := r.router.GetRecord(ctx, projectTable, key)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if int64Value(current["version"]) != project.Version {
		return ErrConflict
	}
	return ErrServiceDependency
}

func (r *PlatformRepository) GetProject(ctx context.Context, tenant string, projectID string) (Project, error) {
	if r == nil || r.router == nil {
		return Project{}, ErrServiceDependency
	}
	tenant = tenantID(tenant)
	projectID = strings.TrimSpace(projectID)
	if !validProjectIdentity(tenant, projectID) {
		return Project{}, ErrInvalidRequest
	}
	record, ok, err := r.router.GetRecord(ctx, projectTable, projectKey(tenant, projectID))
	if err != nil {
		return Project{}, err
	}
	if !ok {
		return Project{}, ErrNotFound
	}
	return projectFromRecord(record, tenant, projectID)
}

func (r *PlatformRepository) ListProjects(ctx context.Context, tenant string) ([]Project, error) {
	if r == nil || r.router == nil {
		return nil, ErrServiceDependency
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
	projects := make([]Project, 0, len(records))
	for key, record := range records {
		project, err := projectFromRecord(record, "", "")
		if err != nil || projectKey(project.TenantID, project.ProjectID) != key {
			return nil, fmt.Errorf("%w: project record identity is inconsistent", ErrServiceDependency)
		}
		if project.TenantID == tenantID(tenant) {
			projects = append(projects, project)
		}
	}
	return projects, nil
}

func (r *PlatformRepository) ListProjectCatalog(ctx context.Context, afterID string, limit int) ([]Project, bool, error) {
	if r == nil || r.router == nil {
		return nil, false, ErrServiceDependency
	}
	page, err := r.router.ListRecords(ctx, projectTable, afterID, limit)
	if err != nil {
		return nil, false, err
	}
	projects := make([]Project, 0, len(page.Items))
	for _, item := range page.Items {
		project, decodeErr := projectFromRecord(item.Record, "", "")
		if decodeErr != nil || projectKey(project.TenantID, project.ProjectID) != item.Key {
			return nil, false, ErrServiceDependency
		}
		projects = append(projects, project)
	}
	return projects, page.HasMore, nil
}

func projectRecord(project Project) db.Record {
	return db.Record{
		"tenant_id":       tenantID(project.TenantID),
		"project_id":      project.ProjectID,
		"organization_id": project.OrganizationID,
		"title":           project.Title,
		"status":          project.Status,
		"version":         project.Version,
		"created_by":      project.CreatedBy,
		"created_at":      project.CreatedAt.Format(time.RFC3339Nano),
		"updated_at":      project.UpdatedAt.Format(time.RFC3339Nano),
	}
}

func projectFromRecord(record db.Record, expectedTenant, expectedProjectID string) (Project, error) {
	createdAt, _ := time.Parse(time.RFC3339Nano, stringValue(record["created_at"]))
	updatedAt, _ := time.Parse(time.RFC3339Nano, stringValue(record["updated_at"]))
	project := Project{
		TenantID:       tenantID(stringValue(record["tenant_id"])),
		ProjectID:      stringValue(record["project_id"]),
		OrganizationID: stringValue(record["organization_id"]),
		Title:          stringValue(record["title"]),
		Status:         stringValue(record["status"]),
		Version:        int64Value(record["version"]),
		CreatedBy:      stringValue(record["created_by"]),
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
	}
	recordTenant := tenantID(stringValue(record["tenant_id"]))
	recordProjectID := strings.TrimSpace(stringValue(record["project_id"]))
	if !validProjectIdentity(project.TenantID, project.ProjectID) || project.TenantID != recordTenant || project.ProjectID != recordProjectID ||
		(expectedTenant != "" && project.TenantID != tenantID(expectedTenant)) ||
		(expectedProjectID != "" && project.ProjectID != strings.TrimSpace(expectedProjectID)) {
		return Project{}, fmt.Errorf("%w: project record identity is inconsistent", ErrServiceDependency)
	}
	return project, nil
}

func projectKey(tenant, projectID string) string {
	return tenantID(tenant) + "/" + strings.TrimSpace(projectID)
}

func tenantID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "global"
	}
	return value
}

func validProjectIdentity(tenant, projectID string) bool {
	tenant = strings.TrimSpace(tenant)
	projectID = strings.TrimSpace(projectID)
	return validProjectTenant(tenant) && validProjectResourceID(projectID)
}

func validProjectTenant(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 256 && !containsProjectControl(value) && !strings.ContainsAny(value, "/\\")
}

func validProjectResourceID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 256 && !containsProjectControl(value) && !strings.ContainsAny(value, "/\\")
}

func validProjectSubjectID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 256 && !containsProjectControl(value)
}

func containsProjectControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}

func stringValue(raw any) string {
	if value, ok := raw.(string); ok {
		return value
	}
	return ""
}

func int64Value(raw any) int64 {
	switch value := raw.(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	default:
		return 0
	}
}
