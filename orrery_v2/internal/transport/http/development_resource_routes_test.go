package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/developmentproject"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/scmconnection"
)

func TestDevelopmentProjectRoutesPropagateViewerTenantAndIfMatch(t *testing.T) {
	service := &developmentProjectRouteStub{}
	router := &Router{developmentProjects: service}

	list := httptest.NewRequest(http.MethodGet, "/api/development-projects?status=active&limit=20", nil)
	list = developmentResourceViewerRequest(list)
	listRecorder := httptest.NewRecorder()
	router.handleDevelopmentProjects(listRecorder, list, developmentResourceRouteParts(list))
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	if service.viewer.TenantID != "tenant-a" || service.viewer.ViewerID != "account-a" || service.viewer.AuthorizationSubjectID != "profile-a" {
		t.Fatalf("development project viewer = %+v", service.viewer)
	}
	if service.query.Status != "active" || service.query.Limit != 20 {
		t.Fatalf("development project query = %+v", service.query)
	}

	update := httptest.NewRequest(http.MethodPatch, "/api/development-projects/project-1", strings.NewReader(`{"name":"Updated"}`))
	update = developmentResourceViewerRequest(update)
	update.Header.Set("If-Match", `"7"`)
	updateRecorder := httptest.NewRecorder()
	router.handleDevelopmentProjects(updateRecorder, update, developmentResourceRouteParts(update))
	if updateRecorder.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", updateRecorder.Code, updateRecorder.Body.String())
	}
	if service.update.IdempotencyKey != "development-resource-test" || service.update.DevelopmentProjectID != "project-1" || service.update.ExpectedVersion != 7 || service.update.Name == nil || *service.update.Name != "Updated" {
		t.Fatalf("development project update = %+v", service.update)
	}

	missingVersion := httptest.NewRequest(http.MethodPatch, "/api/development-projects/project-1", strings.NewReader(`{"name":"Ignored"}`))
	missingVersion = developmentResourceViewerRequest(missingVersion)
	missingVersionRecorder := httptest.NewRecorder()
	router.handleDevelopmentProjects(missingVersionRecorder, missingVersion, developmentResourceRouteParts(missingVersion))
	if missingVersionRecorder.Code != http.StatusBadRequest {
		t.Fatalf("missing If-Match status=%d body=%s", missingVersionRecorder.Code, missingVersionRecorder.Body.String())
	}
}

func TestSCMConnectionCreateRouteAcceptsSecretReferenceButNeverReturnsIt(t *testing.T) {
	service := &scmConnectionRouteStub{}
	router := &Router{scmConnections: service}
	req := httptest.NewRequest(http.MethodPost, "/api/scm-connections", strings.NewReader(`{"scope":"personal","provider":"generic_git","base_url":"https://git.example/repo","secret_ref":"secret://scm/token"}`))
	req = developmentResourceViewerRequest(req)
	recorder := httptest.NewRecorder()

	router.handleSCMConnections(recorder, req, developmentResourceRouteParts(req))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.viewer.TenantID != "tenant-a" || service.viewer.AuthorizationSubjectID != "profile-a" {
		t.Fatalf("SCM viewer = %+v", service.viewer)
	}
	if service.create.SecretRef != "secret://scm/token" {
		t.Fatalf("SCM create command = %+v", service.create)
	}
	body := strings.ToLower(recorder.Body.String())
	if strings.Contains(body, "secret_ref") || strings.Contains(body, "secret://") {
		t.Fatalf("SCM response leaked secret reference: %s", recorder.Body.String())
	}
	var dto scmconnection.ConnectionDTO
	if err := json.NewDecoder(recorder.Body).Decode(&dto); err != nil || dto.ConnectionID != "connection-1" {
		t.Fatalf("decode response: dto=%+v err=%v", dto, err)
	}
}

func TestDevelopmentResourceMutationsRequireIdempotencyKey(t *testing.T) {
	projectService := &developmentProjectRouteStub{}
	router := &Router{developmentProjects: projectService}
	req := httptest.NewRequest(http.MethodPost, "/api/development-projects", strings.NewReader(`{"name":"Project"}`))
	req = developmentResourceViewerRequest(req)
	req.Header.Del("Idempotency-Key")
	recorder := httptest.NewRecorder()
	router.handleDevelopmentProjects(recorder, req, developmentResourceRouteParts(req))
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "Idempotency-Key") {
		t.Fatalf("missing idempotency key status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestSCMConnectionCreateRouteRejectsTrailingJSONValue(t *testing.T) {
	service := &scmConnectionRouteStub{}
	router := &Router{scmConnections: service}
	req := httptest.NewRequest(http.MethodPost, "/api/scm-connections", strings.NewReader(
		`{"scope":"personal","provider":"generic_git","base_url":"https://git.example/repo"}{"scope":"platform"}`,
	))
	req = developmentResourceViewerRequest(req)
	recorder := httptest.NewRecorder()

	router.handleSCMConnections(recorder, req, developmentResourceRouteParts(req))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("trailing JSON status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.create.Scope != "" {
		t.Fatalf("Create was called with trailing JSON: %+v", service.create)
	}
}

func TestDevelopmentResourceCreateRoutesRejectClientSuppliedResourceIDs(t *testing.T) {
	projectRouter := &Router{developmentProjects: &developmentProjectRouteStub{}}
	projectRequest := httptest.NewRequest(http.MethodPost, "/api/development-projects", strings.NewReader(`{"development_project_id":"chosen","name":"Project"}`))
	projectRequest = developmentResourceViewerRequest(projectRequest)
	projectRecorder := httptest.NewRecorder()
	projectRouter.handleDevelopmentProjects(projectRecorder, projectRequest, developmentResourceRouteParts(projectRequest))
	if projectRecorder.Code != http.StatusBadRequest {
		t.Fatalf("project client ID status=%d body=%s", projectRecorder.Code, projectRecorder.Body.String())
	}

	connectionRouter := &Router{scmConnections: &scmConnectionRouteStub{}}
	connectionRequest := httptest.NewRequest(http.MethodPost, "/api/scm-connections", strings.NewReader(`{"connection_id":"chosen","scope":"personal","provider":"generic_git","base_url":"https://git.example/repo"}`))
	connectionRequest = developmentResourceViewerRequest(connectionRequest)
	connectionRecorder := httptest.NewRecorder()
	connectionRouter.handleSCMConnections(connectionRecorder, connectionRequest, developmentResourceRouteParts(connectionRequest))
	if connectionRecorder.Code != http.StatusBadRequest {
		t.Fatalf("connection client ID status=%d body=%s", connectionRecorder.Code, connectionRecorder.Body.String())
	}
}

func TestDevelopmentResourceRoutesMapAuthorizationDenialAndHiddenObjects(t *testing.T) {
	service := &developmentProjectRouteStub{getErr: authorization.ErrDenied}
	router := &Router{developmentProjects: service}
	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/development-projects/project-1", nil)
		req = developmentResourceViewerRequest(req)
		recorder := httptest.NewRecorder()
		router.handleDevelopmentProjects(recorder, req, developmentResourceRouteParts(req))
		return recorder
	}
	if recorder := request(); recorder.Code != http.StatusForbidden {
		t.Fatalf("authorization denial status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	service.getErr = developmentproject.ErrNotFound
	if recorder := request(); recorder.Code != http.StatusNotFound {
		t.Fatalf("hidden object status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func developmentResourceViewerRequest(req *http.Request) *http.Request {
	req.Header.Set("Idempotency-Key", "development-resource-test")
	principal := identity.Principal{
		SubjectProvider: "internal", Subject: "account-a", UserID: "account-a", Username: "account-a",
		AgentProfileID: "profile-a", ViewerKind: identity.ViewerKindUser, TenantID: "tenant-a",
		CoarseScopes: []string{"development.read", "development.manage", "connection.read"},
	}
	return req.WithContext(identity.ContextWithPrincipal(req.Context(), principal))
}

func developmentResourceRouteParts(req *http.Request) []string {
	return strings.Split(strings.Trim(req.URL.Path, "/"), "/")
}

type developmentProjectRouteStub struct {
	viewer developmentproject.ViewerRef
	query  developmentproject.ListQuery
	update developmentproject.UpdateCommand
	getErr error
}

func (s *developmentProjectRouteStub) Create(context.Context, developmentproject.ViewerRef, developmentproject.CreateCommand) (developmentproject.ProjectDTO, error) {
	return developmentproject.ProjectDTO{}, errors.New("unexpected Create call")
}

func (s *developmentProjectRouteStub) List(_ context.Context, viewer developmentproject.ViewerRef, query developmentproject.ListQuery) (developmentproject.ProjectPage, error) {
	s.viewer, s.query = viewer, query
	return developmentproject.ProjectPage{Items: []developmentproject.ProjectDTO{}, Page: developmentproject.PageDTO{Limit: query.Limit}}, nil
}

func (s *developmentProjectRouteStub) Get(_ context.Context, viewer developmentproject.ViewerRef, projectID string) (developmentproject.ProjectDTO, error) {
	s.viewer = viewer
	return developmentproject.ProjectDTO{DevelopmentProjectID: projectID}, s.getErr
}

func (s *developmentProjectRouteStub) ListExecutions(_ context.Context, viewer developmentproject.ViewerRef, _ string, query developmentproject.ListExecutionsQuery) (developmentproject.ExecutionPage, error) {
	s.viewer = viewer
	return developmentproject.ExecutionPage{Items: []developmentproject.ExecutionSummary{}, Page: developmentproject.PageDTO{Limit: query.Limit}}, nil
}

func (s *developmentProjectRouteStub) Update(_ context.Context, viewer developmentproject.ViewerRef, cmd developmentproject.UpdateCommand) (developmentproject.ProjectDTO, error) {
	s.viewer, s.update = viewer, cmd
	return developmentproject.ProjectDTO{DevelopmentProjectID: cmd.DevelopmentProjectID, Version: cmd.ExpectedVersion + 1}, nil
}

func (s *developmentProjectRouteStub) Archive(context.Context, developmentproject.ViewerRef, developmentproject.ArchiveCommand) (developmentproject.ProjectDTO, error) {
	return developmentproject.ProjectDTO{}, errors.New("unexpected Archive call")
}

func (s *developmentProjectRouteStub) ListMembers(context.Context, developmentproject.ViewerRef, string) ([]developmentproject.Member, error) {
	return nil, errors.New("unexpected ListMembers call")
}

func (s *developmentProjectRouteStub) AddMember(context.Context, developmentproject.ViewerRef, developmentproject.AddMemberCommand) (developmentproject.Member, developmentproject.ProjectDTO, error) {
	return developmentproject.Member{}, developmentproject.ProjectDTO{}, errors.New("unexpected AddMember call")
}

func (s *developmentProjectRouteStub) UpdateMember(context.Context, developmentproject.ViewerRef, developmentproject.UpdateMemberCommand) (developmentproject.Member, developmentproject.ProjectDTO, error) {
	return developmentproject.Member{}, developmentproject.ProjectDTO{}, errors.New("unexpected UpdateMember call")
}

func (s *developmentProjectRouteStub) RemoveMember(context.Context, developmentproject.ViewerRef, developmentproject.RemoveMemberCommand) (developmentproject.ProjectDTO, error) {
	return developmentproject.ProjectDTO{}, errors.New("unexpected RemoveMember call")
}

func (s *developmentProjectRouteStub) ListRepositories(context.Context, developmentproject.ViewerRef, string) ([]developmentproject.RepositoryBinding, error) {
	return nil, errors.New("unexpected ListRepositories call")
}

func (s *developmentProjectRouteStub) BindRepository(context.Context, developmentproject.ViewerRef, developmentproject.BindRepositoryCommand) (developmentproject.RepositoryBinding, developmentproject.ProjectDTO, error) {
	return developmentproject.RepositoryBinding{}, developmentproject.ProjectDTO{}, errors.New("unexpected BindRepository call")
}

func (s *developmentProjectRouteStub) UpdateRepository(context.Context, developmentproject.ViewerRef, developmentproject.UpdateRepositoryCommand) (developmentproject.RepositoryBinding, developmentproject.ProjectDTO, error) {
	return developmentproject.RepositoryBinding{}, developmentproject.ProjectDTO{}, errors.New("unexpected UpdateRepository call")
}

func (s *developmentProjectRouteStub) VerifyRepository(context.Context, developmentproject.ViewerRef, developmentproject.VerifyRepositoryCommand) (developmentproject.RepositoryBinding, developmentproject.ProjectDTO, error) {
	return developmentproject.RepositoryBinding{}, developmentproject.ProjectDTO{}, errors.New("unexpected VerifyRepository call")
}

func (s *developmentProjectRouteStub) RemoveRepository(context.Context, developmentproject.ViewerRef, developmentproject.RemoveRepositoryCommand) (developmentproject.ProjectDTO, error) {
	return developmentproject.ProjectDTO{}, errors.New("unexpected RemoveRepository call")
}

type scmConnectionRouteStub struct {
	viewer scmconnection.ViewerRef
	create scmconnection.CreateCommand
}

func (s *scmConnectionRouteStub) Create(_ context.Context, viewer scmconnection.ViewerRef, cmd scmconnection.CreateCommand) (scmconnection.ConnectionDTO, error) {
	s.viewer, s.create = viewer, cmd
	return scmconnection.ConnectionDTO{ConnectionID: "connection-1", Scope: cmd.Scope, Provider: cmd.Provider, Status: scmconnection.StatusActive, Version: 1}, nil
}

func (s *scmConnectionRouteStub) List(context.Context, scmconnection.ViewerRef, scmconnection.ListQuery) (scmconnection.ConnectionPage, error) {
	return scmconnection.ConnectionPage{}, errors.New("unexpected List call")
}

func (s *scmConnectionRouteStub) Get(context.Context, scmconnection.ViewerRef, string) (scmconnection.ConnectionDTO, error) {
	return scmconnection.ConnectionDTO{}, errors.New("unexpected Get call")
}

func (s *scmConnectionRouteStub) Update(context.Context, scmconnection.ViewerRef, scmconnection.UpdateCommand) (scmconnection.ConnectionDTO, error) {
	return scmconnection.ConnectionDTO{}, errors.New("unexpected Update call")
}

func (s *scmConnectionRouteStub) Rotate(context.Context, scmconnection.ViewerRef, scmconnection.RotateCommand) (scmconnection.ConnectionDTO, error) {
	return scmconnection.ConnectionDTO{}, errors.New("unexpected Rotate call")
}

func (s *scmConnectionRouteStub) Revoke(context.Context, scmconnection.ViewerRef, scmconnection.ActionCommand) (scmconnection.ConnectionDTO, error) {
	return scmconnection.ConnectionDTO{}, errors.New("unexpected Revoke call")
}

func (s *scmConnectionRouteStub) Verify(context.Context, scmconnection.ViewerRef, scmconnection.ActionCommand) (scmconnection.ConnectionDTO, scmconnection.Verification, error) {
	return scmconnection.ConnectionDTO{}, scmconnection.Verification{}, errors.New("unexpected Verify call")
}

func (s *scmConnectionRouteStub) ListRepositories(context.Context, scmconnection.ViewerRef, string) ([]scmconnection.RepositoryMetadata, error) {
	return nil, errors.New("unexpected ListRepositories call")
}
