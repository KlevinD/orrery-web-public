package projectadmin

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/platform/db"
)

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time {
	return c.now
}

type sequenceIDGenerator struct {
	ids []string
}

type allowAllProjectAdapter struct{}

func (allowAllProjectAdapter) Check(context.Context, authorization.CheckRequest) (authorization.Decision, error) {
	return authorization.Decision{Allowed: true, Source: "test"}, nil
}

func (a allowAllProjectAdapter) BatchCheck(ctx context.Context, requests []authorization.CheckRequest) ([]authorization.Decision, error) {
	out := make([]authorization.Decision, len(requests))
	for i := range requests {
		out[i] = authorization.Decision{Allowed: true, Source: "test"}
	}
	return out, nil
}

func (allowAllProjectAdapter) ListObjects(context.Context, identity.Principal, string, string, string) ([]identity.ObjectRef, error) {
	return nil, nil
}

func (allowAllProjectAdapter) WriteRelations(context.Context, []authorization.RelationTuple) error {
	return nil
}
func (allowAllProjectAdapter) DeleteRelations(context.Context, []authorization.RelationTuple) error {
	return nil
}

func newTestProjectService(repo Repository, ids IDGenerator, clock Clock, sink *projectAuthzSink) *Service {
	if sink == nil {
		sink = &projectAuthzSink{}
	}
	service := NewService(repo, ids, clock)
	service.SetAuthorizationService(authorization.NewService(allowAllProjectAdapter{}))
	service.SetAuthorizationRelations(authorization.NewRelationProjector(sink))
	return service
}

func (g *sequenceIDGenerator) NewIDContext() (string, error) {
	if len(g.ids) == 0 {
		return "", nil
	}
	id := g.ids[0]
	g.ids = g.ids[1:]
	return id, nil
}

func TestServiceCreateProjectPersistsAuthoritativeProject(t *testing.T) {
	ctx := context.Background()
	registry, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "projectadmin-create"},
	}})
	if err != nil {
		t.Fatalf("open registry: %v", err)
	}
	defer registry.Close()
	router := db.NewRouter(registry)
	now := time.Date(2026, 6, 20, 14, 0, 0, 0, time.UTC)
	service := newTestProjectService(NewPlatformRepository(router), &sequenceIDGenerator{ids: []string{"project-1"}}, fixedClock{now: now}, nil)

	project, err := service.CreateProject(ctx, ViewerRef{ViewerID: "planner-1", ViewerKind: ViewerKindUser}, CreateProjectCommand{
		Title:          "Ontology project",
		OrganizationID: "org-1",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if project.ProjectID != "project-1" || project.Title != "Ontology project" || project.OrganizationID != "org-1" || project.Status != ProjectStatusActive || project.Version != 1 {
		t.Fatalf("project = %+v", project)
	}
	if !project.CreatedAt.Equal(now) || !project.UpdatedAt.Equal(now) {
		t.Fatalf("timestamps = %s / %s, want %s", project.CreatedAt, project.UpdatedAt, now)
	}

	reloaded, err := NewPlatformRepository(router).GetProject(ctx, "global", "project-1")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if reloaded != project {
		t.Fatalf("reloaded = %+v, want %+v", reloaded, project)
	}
}

func TestServiceCreateProjectWritesAuthorizationOwnerRelation(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRepository()
	sink := &projectAuthzSink{}
	now := time.Date(2026, 6, 20, 14, 0, 0, 0, time.UTC)
	service := newTestProjectService(repo, &sequenceIDGenerator{ids: []string{"project-1"}}, fixedClock{now: now}, sink)

	if _, err := service.CreateProject(ctx, ViewerRef{ViewerID: "planner-1", ViewerKind: ViewerKindUser, AuthorizationSubjectID: "human-planner-1"}, CreateProjectCommand{Title: "Ontology project", OrganizationID: "org-1"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if len(sink.written) != 2 {
		t.Fatalf("written relations = %+v", sink.written)
	}
	owner := sink.written[0]
	if owner.Subject.Kind != "user" || owner.Subject.ID != "human-planner-1" || owner.Relation != "owner" || owner.Object.Type != "project" || owner.Object.ID != "project-1" || owner.TenantID != "global" {
		t.Fatalf("owner relation tuple = %+v", owner)
	}
	organization := sink.written[1]
	if organization.Subject.Kind != "organization" || organization.Subject.ID != "org-1" || organization.Relation != "org" || organization.Object.Type != "project" || organization.Object.ID != "project-1" || organization.TenantID != "global" {
		t.Fatalf("organization relation tuple = %+v", organization)
	}
}

func TestServiceCreateProjectRequiresTitle(t *testing.T) {
	ctx := context.Background()
	service := newTestProjectService(NewMemoryRepository(), &sequenceIDGenerator{ids: []string{"project-1"}}, fixedClock{now: time.Unix(1, 0).UTC()}, nil)

	_, err := service.CreateProject(ctx, ViewerRef{ViewerID: "planner-1", ViewerKind: ViewerKindUser}, CreateProjectCommand{})
	if err == nil {
		t.Fatal("CreateProject returned nil error for missing title")
	}
}

func TestServiceRejectsUnsafeProjectAndQueryIdentifiers(t *testing.T) {
	service := newTestProjectService(NewMemoryRepository(), &sequenceIDGenerator{ids: []string{"project-generated"}}, fixedClock{now: time.Unix(1, 0).UTC()}, nil)
	viewer := ViewerRef{ViewerID: "planner-1", ViewerKind: ViewerKindUser, TenantID: "tenant-a"}
	for _, projectID := range []string{"nested/project", `nested\project`, "project\nother", strings.Repeat("p", 257)} {
		if _, err := service.CreateProject(context.Background(), viewer, CreateProjectCommand{ProjectID: projectID, Title: "Project"}); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("CreateProject(%q) error = %v, want ErrInvalidRequest", projectID, err)
		}
		if _, err := service.GetProjectView(context.Background(), viewer, projectID); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("GetProjectView(%q) error = %v, want ErrInvalidRequest", projectID, err)
		}
	}
	for _, organizationID := range []string{"nested/org", `nested\org`, "org\nother"} {
		if _, err := service.ListProjects(context.Background(), viewer, ListProjectsQuery{OrganizationID: organizationID}); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("ListProjects(%q) error = %v, want ErrInvalidRequest", organizationID, err)
		}
	}
	if _, err := service.ListProjects(context.Background(), viewer, ListProjectsQuery{Status: "unknown"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("ListProjects status error = %v, want ErrInvalidRequest", err)
	}
	invalidViewer := viewer
	invalidViewer.TenantID = "tenant/ambiguous"
	if _, err := service.ListProjects(context.Background(), invalidViewer, ListProjectsQuery{}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("ListProjects invalid viewer error = %v, want ErrInvalidRequest", err)
	}
}

type projectAuthzSink struct {
	written  []authorization.RelationTuple
	deleted  []authorization.RelationTuple
	writeErr error
}

func (s *projectAuthzSink) WriteRelations(_ context.Context, tuples []authorization.RelationTuple) error {
	s.written = append(s.written, tuples...)
	return s.writeErr
}

func TestServiceFailsClosedWithoutAuthorizationDependencies(t *testing.T) {
	service := NewService(NewMemoryRepository(), &sequenceIDGenerator{ids: []string{"project-1"}}, fixedClock{now: time.Unix(1, 0).UTC()})
	viewer := ViewerRef{ViewerID: "planner-1", ViewerKind: ViewerKindUser}
	if _, err := service.CreateProject(context.Background(), viewer, CreateProjectCommand{Title: "Project"}); !errors.Is(err, ErrServiceDependency) {
		t.Fatalf("CreateProject error = %v, want dependency error", err)
	}
	if _, err := service.ListProjects(context.Background(), viewer, ListProjectsQuery{}); !errors.Is(err, ErrServiceDependency) {
		t.Fatalf("ListProjects error = %v, want dependency error", err)
	}
}

func TestServiceCreateProjectCompensatesRelationProjectionFailure(t *testing.T) {
	repo := NewMemoryRepository()
	projectionFailure := errors.New("projection unavailable")
	service := newTestProjectService(repo, &sequenceIDGenerator{ids: []string{"project-1"}}, fixedClock{now: time.Unix(1, 0).UTC()}, &projectAuthzSink{writeErr: projectionFailure})
	viewer := ViewerRef{ViewerID: "planner-1", ViewerKind: ViewerKindUser}

	_, err := service.CreateProject(context.Background(), viewer, CreateProjectCommand{Title: "Project"})
	if !errors.Is(err, projectionFailure) {
		t.Fatalf("CreateProject error = %v, want projection failure", err)
	}
	if _, err := repo.GetProject(context.Background(), "global", "project-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("compensated project lookup error = %v, want not found", err)
	}
}

func (s *projectAuthzSink) DeleteRelations(_ context.Context, tuples []authorization.RelationTuple) error {
	s.deleted = append(s.deleted, tuples...)
	return nil
}

func TestServiceListProjectsFiltersOrganizationStatusAndPaginates(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRepository()
	seedProjects(t, ctx, repo, []Project{
		{ProjectID: "project-old", OrganizationID: "org-1", Title: "Old project", Status: ProjectStatusActive, CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(2, 0).UTC(), Version: 1},
		{ProjectID: "project-archived", OrganizationID: "org-1", Title: "Archived project", Status: ProjectStatusArchived, CreatedAt: time.Unix(3, 0).UTC(), UpdatedAt: time.Unix(4, 0).UTC(), Version: 1},
		{ProjectID: "project-new", OrganizationID: "org-1", Title: "New project", Status: ProjectStatusActive, CreatedAt: time.Unix(5, 0).UTC(), UpdatedAt: time.Unix(6, 0).UTC(), Version: 1},
		{ProjectID: "project-other-org", OrganizationID: "org-2", Title: "Other org", Status: ProjectStatusActive, CreatedAt: time.Unix(7, 0).UTC(), UpdatedAt: time.Unix(8, 0).UTC(), Version: 1},
	})
	service := newTestProjectService(repo, nil, fixedClock{now: time.Unix(10, 0).UTC()}, nil)

	page, err := service.ListProjects(ctx, ViewerRef{ViewerID: "reader-1", ViewerKind: ViewerKindUser}, ListProjectsQuery{
		OrganizationID: "org-1",
		Status:         ProjectStatusActive,
		Limit:          1,
		Offset:         0,
	})
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if page.Page.Total != 2 || page.Page.Returned != 1 || !page.Page.HasMore {
		t.Fatalf("page meta = %+v", page.Page)
	}
	if len(page.Projects) != 1 || page.Projects[0].Project.ProjectID != "project-new" {
		t.Fatalf("projects = %+v", page.Projects)
	}

	next, err := service.ListProjects(ctx, ViewerRef{ViewerID: "reader-1", ViewerKind: ViewerKindUser}, ListProjectsQuery{
		OrganizationID: "org-1",
		Status:         ProjectStatusActive,
		Limit:          1,
		Offset:         1,
	})
	if err != nil {
		t.Fatalf("ListProjects next page: %v", err)
	}
	if next.Page.Total != 2 || next.Page.Returned != 1 || next.Page.HasMore {
		t.Fatalf("next page meta = %+v", next.Page)
	}
	if len(next.Projects) != 1 || next.Projects[0].Project.ProjectID != "project-old" {
		t.Fatalf("next projects = %+v", next.Projects)
	}
}

func TestServiceGetProjectViewReturnsAuthoritativeProject(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRepository()
	seedProjects(t, ctx, repo, []Project{
		{ProjectID: "project-1", OrganizationID: "org-1", Title: "Ontology project", Status: ProjectStatusActive, CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(2, 0).UTC(), Version: 1},
	})
	service := newTestProjectService(repo, nil, fixedClock{now: time.Unix(10, 0).UTC()}, nil)

	view, err := service.GetProjectView(ctx, ViewerRef{ViewerID: "reader-1", ViewerKind: ViewerKindUser}, "project-1")
	if err != nil {
		t.Fatalf("GetProjectView: %v", err)
	}
	if view.Project.ProjectID != "project-1" || view.Project.OrganizationID != "org-1" || view.Project.Title != "Ontology project" {
		t.Fatalf("view = %+v", view)
	}
}

func TestServiceUpdateProjectPersistsAuthoritativeProject(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRepository()
	createdAt := time.Unix(1, 0).UTC()
	if err := repo.SaveProject(ctx, Project{
		ProjectID:      "project-1",
		OrganizationID: "org-1",
		Title:          "Old title",
		Status:         ProjectStatusActive,
		Version:        1,
		CreatedBy:      "planner-1",
		CreatedAt:      createdAt,
		UpdatedAt:      createdAt,
	}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	now := time.Date(2026, 6, 20, 16, 0, 0, 0, time.UTC)
	service := newTestProjectService(repo, nil, fixedClock{now: now}, nil)

	project, err := service.UpdateProject(ctx, ViewerRef{ViewerID: "planner-2", ViewerKind: ViewerKindUser}, UpdateProjectCommand{
		ProjectID:       "project-1",
		Title:           "Updated title",
		ExpectedVersion: 1,
	})
	if err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}
	if project.Title != "Updated title" || project.Status != ProjectStatusActive || project.Version != 2 {
		t.Fatalf("project = %+v", project)
	}
	if !project.CreatedAt.Equal(createdAt) || !project.UpdatedAt.Equal(now) {
		t.Fatalf("timestamps = %s / %s", project.CreatedAt, project.UpdatedAt)
	}
	reloaded, err := repo.GetProject(ctx, "global", "project-1")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if reloaded != project {
		t.Fatalf("reloaded = %+v, want %+v", reloaded, project)
	}
}

func TestServiceArchiveProjectPersistsArchivedStatus(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRepository()
	createdAt := time.Unix(1, 0).UTC()
	if err := repo.SaveProject(ctx, Project{
		ProjectID:      "project-1",
		OrganizationID: "org-1",
		Title:          "Ontology project",
		Status:         ProjectStatusActive,
		Version:        1,
		CreatedBy:      "planner-1",
		CreatedAt:      createdAt,
		UpdatedAt:      createdAt,
	}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	now := time.Date(2026, 6, 20, 17, 0, 0, 0, time.UTC)
	service := newTestProjectService(repo, nil, fixedClock{now: now}, nil)

	project, err := service.ArchiveProject(ctx, ViewerRef{ViewerID: "planner-2", ViewerKind: ViewerKindUser}, ArchiveProjectCommand{
		ProjectID:       "project-1",
		ExpectedVersion: 1,
	})
	if err != nil {
		t.Fatalf("ArchiveProject: %v", err)
	}
	if project.Status != ProjectStatusArchived || project.Title != "Ontology project" || project.Version != 2 {
		t.Fatalf("project = %+v", project)
	}
	if !project.CreatedAt.Equal(createdAt) || !project.UpdatedAt.Equal(now) {
		t.Fatalf("timestamps = %s / %s", project.CreatedAt, project.UpdatedAt)
	}
}

func seedProjects(t *testing.T, ctx context.Context, repo *MemoryRepository, projects []Project) {
	t.Helper()
	for _, project := range projects {
		if err := repo.SaveProject(ctx, project); err != nil {
			t.Fatalf("SaveProject %s: %v", project.ProjectID, err)
		}
	}
}
