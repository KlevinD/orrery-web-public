package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	projectadmin "orrery_v2/internal/app/projectadmin"
)

func TestHTTPRoutesCreateProjectThroughProjectCommandService(t *testing.T) {
	command := &fakeProjectCommand{project: projectadmin.Project{ProjectID: "project-1", OrganizationID: "org-1", Title: "Ontology project", Status: projectadmin.ProjectStatusActive, CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(1, 0).UTC()}}
	handler := newTestRouter(RouterOptions{ProjectCommand: command})

	req := authedRequest(http.MethodPost, "/api/projects", `{"organization_id":"org-1","title":"Ontology project"}`)
	req.Header.Set("X-Request-ID", "project-corr")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create project status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.viewer.ViewerID != "agent-1" || command.viewer.ViewerKind != projectadmin.ViewerKindAgent {
		t.Fatalf("viewer = %+v", command.viewer)
	}
	if command.cmd.OrganizationID != "org-1" || command.cmd.Title != "Ontology project" {
		t.Fatalf("cmd = %+v", command.cmd)
	}
}

func TestHTTPRoutesListProjectsThroughProjectCommandService(t *testing.T) {
	command := &fakeProjectCommand{
		page: projectadmin.ProjectPage{
			Projects: []projectadmin.ProjectView{{
				Project: projectadmin.Project{ProjectID: "project-1", OrganizationID: "org-1", Title: "Ontology project", Status: projectadmin.ProjectStatusActive, CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(2, 0).UTC()},
				ContextGraph: projectadmin.ProjectGraphStatus{
					ProjectTenantProjected:      true,
					OrganizationObjectProjected: true,
					OrganizationViewProjected:   true,
				},
			}},
			Page: projectadmin.Page{Limit: 10, Total: 1, Returned: 1},
		},
	}
	handler := newTestRouter(RouterOptions{ProjectCommand: command})

	req := authedRequest(http.MethodGet, "/api/projects?organization_id=org-1&status=active&limit=10&offset=20", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list project status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.listQuery.OrganizationID != "org-1" || command.listQuery.Status != "active" || command.listQuery.Limit != 10 || command.listQuery.Offset != 20 {
		t.Fatalf("list query = %+v", command.listQuery)
	}
	var page projectadmin.ProjectPage
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decode project page: %v", err)
	}
	if len(page.Projects) != 1 || page.Projects[0].Project.ProjectID != "project-1" || !page.Projects[0].ContextGraph.OrganizationViewProjected {
		t.Fatalf("project page = %+v", page)
	}
}

func TestHTTPRoutesGetProjectThroughProjectCommandService(t *testing.T) {
	command := &fakeProjectCommand{
		view: projectadmin.ProjectView{
			Project: projectadmin.Project{ProjectID: "project-1", OrganizationID: "org-1", Title: "Ontology project", Status: projectadmin.ProjectStatusActive, CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(2, 0).UTC()},
			ContextGraph: projectadmin.ProjectGraphStatus{
				ProjectTenantProjected:      true,
				OrganizationObjectProjected: true,
				OrganizationViewProjected:   true,
			},
		},
	}
	handler := newTestRouter(RouterOptions{ProjectCommand: command})

	req := authedRequest(http.MethodGet, "/api/projects/project-1", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get project status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.getProjectID != "project-1" {
		t.Fatalf("get project id = %s", command.getProjectID)
	}
	var view projectadmin.ProjectView
	if err := json.NewDecoder(rec.Body).Decode(&view); err != nil {
		t.Fatalf("decode project view: %v", err)
	}
	if view.Project.ProjectID != "project-1" || !view.ContextGraph.ProjectTenantProjected {
		t.Fatalf("project view = %+v", view)
	}
}

func TestHTTPRoutesUpdateProjectThroughProjectCommandService(t *testing.T) {
	command := &fakeProjectCommand{project: projectadmin.Project{ProjectID: "project-1", OrganizationID: "org-1", Title: "Updated project", Status: projectadmin.ProjectStatusActive, CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(2, 0).UTC()}}
	handler := newTestRouter(RouterOptions{ProjectCommand: command})

	req := authedRequest(http.MethodPatch, "/api/projects/project-1", `{"title":"Updated project"}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update project status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.updateCmd.ProjectID != "project-1" || command.updateCmd.Title != "Updated project" {
		t.Fatalf("update cmd = %+v", command.updateCmd)
	}
}

func TestHTTPRoutesArchiveProjectThroughProjectCommandService(t *testing.T) {
	command := &fakeProjectCommand{project: projectadmin.Project{ProjectID: "project-1", OrganizationID: "org-1", Title: "Ontology project", Status: projectadmin.ProjectStatusArchived, CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(2, 0).UTC()}}
	handler := newTestRouter(RouterOptions{ProjectCommand: command})

	req := authedRequest(http.MethodPost, "/api/projects/project-1/archive", `{}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("archive project status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.archiveCmd.ProjectID != "project-1" {
		t.Fatalf("archive cmd = %+v", command.archiveCmd)
	}
}

type fakeProjectCommand struct {
	project      projectadmin.Project
	page         projectadmin.ProjectPage
	view         projectadmin.ProjectView
	viewer       projectadmin.ViewerRef
	cmd          projectadmin.CreateProjectCommand
	listQuery    projectadmin.ListProjectsQuery
	getProjectID string
	updateCmd    projectadmin.UpdateProjectCommand
	archiveCmd   projectadmin.ArchiveProjectCommand
}

func (f *fakeProjectCommand) CreateProject(ctx context.Context, viewer projectadmin.ViewerRef, cmd projectadmin.CreateProjectCommand) (projectadmin.Project, error) {
	f.viewer = viewer
	f.cmd = cmd
	return f.project, nil
}

func (f *fakeProjectCommand) ListProjects(ctx context.Context, viewer projectadmin.ViewerRef, query projectadmin.ListProjectsQuery) (projectadmin.ProjectPage, error) {
	f.viewer = viewer
	f.listQuery = query
	return f.page, nil
}

func (f *fakeProjectCommand) GetProjectView(ctx context.Context, viewer projectadmin.ViewerRef, projectID string) (projectadmin.ProjectView, error) {
	f.viewer = viewer
	f.getProjectID = projectID
	return f.view, nil
}

func (f *fakeProjectCommand) UpdateProject(ctx context.Context, viewer projectadmin.ViewerRef, cmd projectadmin.UpdateProjectCommand) (projectadmin.Project, error) {
	f.viewer = viewer
	f.updateCmd = cmd
	return f.project, nil
}

func (f *fakeProjectCommand) ArchiveProject(ctx context.Context, viewer projectadmin.ViewerRef, cmd projectadmin.ArchiveProjectCommand) (projectadmin.Project, error) {
	f.viewer = viewer
	f.archiveCmd = cmd
	return f.project, nil
}
