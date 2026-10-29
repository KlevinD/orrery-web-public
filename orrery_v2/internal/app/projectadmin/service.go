package projectadmin

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/platform/idgen"
)

type Repository interface {
	CreateProject(ctx context.Context, tenantID string, project Project) error
	UpdateProject(ctx context.Context, tenantID string, project Project, expectedVersion int64) error
	DeleteProject(ctx context.Context, tenantID string, project Project) error
	GetProject(ctx context.Context, tenantID string, projectID string) (Project, error)
	ListProjects(ctx context.Context, tenantID string) ([]Project, error)
	ListProjectCatalog(ctx context.Context, afterID string, limit int) ([]Project, bool, error)
}

type IDGenerator interface {
	NewIDContext() (string, error)
}

type Clock interface {
	Now() time.Time
}

type Service struct {
	repo   Repository
	ids    IDGenerator
	clock  Clock
	authz  *authorization.RelationProjector
	access *authorization.Service
}

func NewService(repo Repository, ids IDGenerator, clock Clock) *Service {
	return &Service{repo: repo, ids: ids, clock: clock}
}

func (s *Service) SetAuthorizationRelations(projector *authorization.RelationProjector) {
	if s != nil {
		s.authz = projector
	}
}

func (s *Service) SetAuthorizationService(service *authorization.Service) {
	if s != nil {
		s.access = service
	}
}

func (s *Service) CreateProject(ctx context.Context, viewer ViewerRef, cmd CreateProjectCommand) (Project, error) {
	if s == nil || s.repo == nil || s.access == nil || s.authz == nil {
		return Project{}, ErrServiceDependency
	}
	if err := validateViewer(viewer); err != nil {
		return Project{}, err
	}
	title := strings.TrimSpace(cmd.Title)
	if title == "" || len(title) > 160 || containsProjectControl(title) {
		return Project{}, fmt.Errorf("%w: title is required", ErrInvalidRequest)
	}
	projectID := strings.TrimSpace(cmd.ProjectID)
	if projectID == "" {
		id, err := s.newID()
		if err != nil {
			return Project{}, err
		}
		projectID = id
	}
	tenant := projectTenantID(viewer)
	organizationID := strings.TrimSpace(cmd.OrganizationID)
	if !validProjectIdentity(tenant, projectID) || (organizationID != "" && !validProjectResourceID(organizationID)) {
		return Project{}, fmt.Errorf("%w: project identity is invalid", ErrInvalidRequest)
	}
	now := s.now()
	project := Project{
		TenantID:       tenant,
		ProjectID:      projectID,
		OrganizationID: organizationID,
		Title:          title,
		Status:         ProjectStatusActive,
		Version:        1,
		CreatedBy:      strings.TrimSpace(viewer.ViewerID),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.authorizeCreate(ctx, viewer, project.OrganizationID); err != nil {
		return Project{}, err
	}
	if err := s.repo.CreateProject(ctx, tenant, project); err != nil {
		return Project{}, err
	}
	if err := s.authz.WriteProjectCreated(ctx, projectSubjectFromViewer(viewer), project.ProjectID, project.OrganizationID, tenant); err != nil {
		return Project{}, errors.Join(err, s.repo.DeleteProject(ctx, tenant, project))
	}
	return project, nil
}

func (s *Service) ListProjects(ctx context.Context, viewer ViewerRef, query ListProjectsQuery) (ProjectPage, error) {
	if s == nil || s.repo == nil || s.access == nil {
		return ProjectPage{}, ErrServiceDependency
	}
	if err := validateViewer(viewer); err != nil {
		return ProjectPage{}, err
	}
	tenant := projectTenantID(viewer)
	projects, err := s.repo.ListProjects(ctx, tenant)
	if err != nil {
		return ProjectPage{}, err
	}
	filtered := make([]Project, 0, len(projects))
	organizationID := strings.TrimSpace(query.OrganizationID)
	status := strings.ToLower(strings.TrimSpace(query.Status))
	if (organizationID != "" && !validProjectResourceID(organizationID)) ||
		(status != "" && status != ProjectStatusActive && status != ProjectStatusArchived) ||
		query.Offset < 0 || query.Limit < 0 {
		return ProjectPage{}, ErrInvalidRequest
	}
	for _, project := range projects {
		if organizationID != "" && project.OrganizationID != organizationID {
			continue
		}
		if status != "" && project.Status != status {
			continue
		}
		capabilities, allowed, err := s.projectCapabilities(ctx, viewer, project)
		if err != nil {
			return ProjectPage{}, err
		}
		if !allowed {
			continue
		}
		_ = capabilities
		filtered = append(filtered, project)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if !filtered[i].UpdatedAt.Equal(filtered[j].UpdatedAt) {
			return filtered[i].UpdatedAt.After(filtered[j].UpdatedAt)
		}
		return filtered[i].ProjectID < filtered[j].ProjectID
	})
	offset := query.Offset
	if offset < 0 {
		offset = 0
	}
	total := len(filtered)
	if offset > total {
		offset = total
	}
	limit := query.Limit
	if limit <= 0 || limit > total-offset {
		limit = total - offset
	}
	page := ProjectPage{Page: Page{Limit: query.Limit, Offset: query.Offset, Total: total}}
	for _, project := range filtered[offset : offset+limit] {
		capabilities, _, err := s.projectCapabilities(ctx, viewer, project)
		if err != nil {
			return ProjectPage{}, err
		}
		page.Projects = append(page.Projects, ProjectView{Project: project, Capabilities: capabilities})
	}
	page.Page.Returned = len(page.Projects)
	page.Page.HasMore = offset+limit < total
	return page, nil
}

func (s *Service) GetProjectView(ctx context.Context, viewer ViewerRef, projectID string) (ProjectView, error) {
	if s == nil || s.repo == nil {
		return ProjectView{}, ErrServiceDependency
	}
	if err := validateViewer(viewer); err != nil {
		return ProjectView{}, err
	}
	projectID = strings.TrimSpace(projectID)
	if !validProjectResourceID(projectID) {
		return ProjectView{}, fmt.Errorf("%w: project_id is required", ErrInvalidRequest)
	}
	project, err := s.repo.GetProject(ctx, projectTenantID(viewer), projectID)
	if err != nil {
		return ProjectView{}, err
	}
	capabilities, allowed, err := s.projectCapabilities(ctx, viewer, project)
	if err != nil {
		return ProjectView{}, err
	}
	if !allowed {
		return ProjectView{}, ErrNotFound
	}
	return ProjectView{Project: project, Capabilities: capabilities}, nil
}

func (s *Service) UpdateProject(ctx context.Context, viewer ViewerRef, cmd UpdateProjectCommand) (Project, error) {
	if s == nil || s.repo == nil {
		return Project{}, ErrServiceDependency
	}
	if err := validateViewer(viewer); err != nil {
		return Project{}, err
	}
	projectID := strings.TrimSpace(cmd.ProjectID)
	if !validProjectResourceID(projectID) {
		return Project{}, fmt.Errorf("%w: project_id is required", ErrInvalidRequest)
	}
	if cmd.ExpectedVersion <= 0 {
		return Project{}, fmt.Errorf("%w: expected_version is required", ErrInvalidRequest)
	}
	title := strings.TrimSpace(cmd.Title)
	if title == "" || len(title) > 160 || containsProjectControl(title) {
		return Project{}, fmt.Errorf("%w: title is required", ErrInvalidRequest)
	}
	tenant := projectTenantID(viewer)
	project, err := s.repo.GetProject(ctx, tenant, projectID)
	if err != nil {
		return Project{}, err
	}
	if err := s.authorizeProject(ctx, viewer, project, "project.edit"); err != nil {
		return Project{}, ErrNotFound
	}
	if project.Version != cmd.ExpectedVersion {
		return Project{}, ErrConflict
	}
	project.Title = title
	project.Version = nextProjectVersion(project.Version)
	project.UpdatedAt = s.now()
	if err := s.repo.UpdateProject(ctx, tenant, project, cmd.ExpectedVersion); err != nil {
		return Project{}, err
	}
	return project, nil
}

func (s *Service) ArchiveProject(ctx context.Context, viewer ViewerRef, cmd ArchiveProjectCommand) (Project, error) {
	if s == nil || s.repo == nil {
		return Project{}, ErrServiceDependency
	}
	if err := validateViewer(viewer); err != nil {
		return Project{}, err
	}
	projectID := strings.TrimSpace(cmd.ProjectID)
	if !validProjectResourceID(projectID) {
		return Project{}, fmt.Errorf("%w: project_id is required", ErrInvalidRequest)
	}
	if cmd.ExpectedVersion <= 0 {
		return Project{}, fmt.Errorf("%w: expected_version is required", ErrInvalidRequest)
	}
	tenant := projectTenantID(viewer)
	project, err := s.repo.GetProject(ctx, tenant, projectID)
	if err != nil {
		return Project{}, err
	}
	if err := s.authorizeProject(ctx, viewer, project, "project.manage"); err != nil {
		return Project{}, ErrNotFound
	}
	if project.Version != cmd.ExpectedVersion {
		return Project{}, ErrConflict
	}
	project.Status = ProjectStatusArchived
	project.Version = nextProjectVersion(project.Version)
	project.UpdatedAt = s.now()
	if err := s.repo.UpdateProject(ctx, tenant, project, cmd.ExpectedVersion); err != nil {
		return Project{}, err
	}
	return project, nil
}

func (s *Service) authorizeCreate(ctx context.Context, viewer ViewerRef, organizationID string) error {
	if s == nil || s.access == nil {
		return ErrServiceDependency
	}
	objectType := "organization"
	objectID := strings.TrimSpace(organizationID)
	if objectID == "" {
		objectType = "platform"
		objectID = "global"
	}
	return s.access.Can(ctx, projectPrincipal(viewer), "project.create", identity.ObjectRef{Type: objectType, ID: objectID, TenantID: projectTenantID(viewer)})
}

func (s *Service) authorizeProject(ctx context.Context, viewer ViewerRef, project Project, action string) error {
	if s == nil || s.access == nil {
		return ErrServiceDependency
	}
	return s.access.Can(ctx, projectPrincipal(viewer), action, identity.ObjectRef{Type: "project", ID: project.ProjectID, TenantID: project.TenantID})
}

func (s *Service) projectCapabilities(ctx context.Context, viewer ViewerRef, project Project) (ProjectCapabilities, bool, error) {
	if s == nil || s.access == nil {
		return ProjectCapabilities{}, false, ErrServiceDependency
	}
	checks := []authorization.CheckRequest{
		{Principal: projectPrincipal(viewer), Action: "project.view", Object: identity.ObjectRef{Type: "project", ID: project.ProjectID, TenantID: project.TenantID}},
		{Principal: projectPrincipal(viewer), Action: "project.edit", Object: identity.ObjectRef{Type: "project", ID: project.ProjectID, TenantID: project.TenantID}},
		{Principal: projectPrincipal(viewer), Action: "project.manage", Object: identity.ObjectRef{Type: "project", ID: project.ProjectID, TenantID: project.TenantID}},
	}
	decisions, err := s.access.BatchCan(ctx, checks)
	if err != nil {
		return ProjectCapabilities{}, false, err
	}
	if len(decisions) != len(checks) {
		return ProjectCapabilities{}, false, ErrServiceDependency
	}
	capabilities := ProjectCapabilities{CanView: decisions[0].Allowed, CanUpdate: decisions[1].Allowed, CanArchive: decisions[2].Allowed}
	return capabilities, capabilities.CanView, nil
}

func projectPrincipal(viewer ViewerRef) identity.Principal {
	subjectID := strings.TrimSpace(viewer.AuthorizationSubjectID)
	if subjectID == "" {
		subjectID = strings.TrimSpace(viewer.ViewerID)
	}
	return identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "internal",
		Subject:         subjectID,
		UserID:          subjectID,
		Username:        subjectID,
		ViewerKind:      string(viewer.ViewerKind),
		TenantID:        projectTenantID(viewer),
		CoarseScopes:    append([]string(nil), viewer.PermissionScope...),
	})
}

func projectTenantID(viewer ViewerRef) string {
	return tenantID(viewer.TenantID)
}

func validateViewer(viewer ViewerRef) error {
	viewerID := strings.TrimSpace(viewer.ViewerID)
	subjectID := strings.TrimSpace(viewer.AuthorizationSubjectID)
	if subjectID == "" {
		subjectID = viewerID
	}
	if !validProjectSubjectID(viewerID) || !validProjectSubjectID(subjectID) || !validProjectTenant(projectTenantID(viewer)) ||
		(viewer.ViewerKind != ViewerKindUser && viewer.ViewerKind != ViewerKindAgent && viewer.ViewerKind != ViewerKindSystem && viewer.ViewerKind != ViewerKindService) {
		return fmt.Errorf("%w: viewer_id is required", ErrInvalidRequest)
	}
	return nil
}

func projectSubjectFromViewer(viewer ViewerRef) authorization.SubjectRef {
	subjectID := strings.TrimSpace(viewer.AuthorizationSubjectID)
	if subjectID == "" {
		subjectID = strings.TrimSpace(viewer.ViewerID)
	}
	return authorization.SubjectRef{Kind: string(viewer.ViewerKind), ID: subjectID}
}

func nextProjectVersion(current int64) int64 {
	if current < 1 {
		return 1
	}
	return current + 1
}

func (s *Service) newID() (string, error) {
	if s.ids == nil {
		return idgen.NewRandom("project").NewIDContext()
	}
	id, err := s.ids.NewIDContext()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("%w: generated project id is empty", ErrInvalidRequest)
	}
	return strings.TrimSpace(id), nil
}

func (s *Service) now() time.Time {
	if s.clock == nil {
		return time.Now().UTC()
	}
	return s.clock.Now().UTC()
}
