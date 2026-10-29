package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/responsibility"
	missioncore "orrery_v2/internal/mission/core"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestResponsibilityProfileHistoryLimit(t *testing.T) {
	for _, test := range []struct {
		name string
		path string
		want int
	}{
		{name: "default", path: "/api/responsibility-profiles/agents/agent-1", want: 10},
		{name: "version limit", path: "/api/responsibility-profiles/agents/agent-1?version_limit=3", want: 3},
		{name: "current only", path: "/api/responsibility-profiles/agents/agent-1?history_limit=0", want: 0},
		{name: "legacy fallback", path: "/api/responsibility-profiles/agents/agent-1?version_limit=invalid&history_limit=0", want: 0},
		{name: "bounded", path: "/api/responsibility-profiles/agents/agent-1?history_limit=200", want: 100},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, test.path, nil)
			if got := responsibilityProfileHistoryLimit(req); got != test.want {
				t.Fatalf("history limit = %d, want %d", got, test.want)
			}
		})
	}
}

func TestResponsibilityProfileRoutesExposePeopleVersionsWithProfilePermissions(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := responsibility.NewRepository(env.DBRouter, env.UnitOfWork)
	profiles := responsibilityProfileRouteProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"agent-1": {
			ID: "agent-1", DisplayName: "Agent One",
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef: agentcore.OrganizationRef{ID: "org-1"},
				DepartmentRef:   agentcore.DepartmentRef{ID: "dept-1"},
				PositionRef:     agentcore.PositionRef{ID: "position-1"},
			},
		},
	}}
	bindingID := responsibility.PeopleAgentBindingID("tenant-1", "people-tenant", "subject-1")
	portfolio := responsibility.PeopleResponsibilityPortfolio{
		ID: responsibility.PeopleResponsibilityPortfolioID(bindingID), TenantID: "tenant-1", BindingID: bindingID,
		PeopleTenantID: "people-tenant", SubjectID: "subject-1", AgentProfileID: "agent-1",
		SourceRevision: "9:4", SourceDigest: "digest-1", Status: "active",
		SourceAsOf: env.Clock.Now(), SyncedAt: env.Clock.Now(),
		Responsibilities: []responsibility.PeopleResolvedResponsibility{{
			ID: "responsibility-1", Code: "RESP-1", Title: "Operate", Revision: 1, ValidFrom: env.Clock.Now(),
			Sources: []responsibility.PeopleResponsibilityBinding{{
				BindingID: "source-binding-1", ResponsibilityID: "responsibility-1", TargetType: "position", TargetID: "people-position-1", Revision: 1, ValidFrom: env.Clock.Now(),
			}},
		}},
	}
	if _, _, err := repository.SavePeopleResponsibilityPortfolio(ctx, portfolio, env.Clock.Now()); err != nil {
		t.Fatal(err)
	}
	service := responsibility.NewOperationsService(responsibility.OperationsServiceOptions{Repository: repository, Profiles: profiles, Clock: env.Clock})
	handler := newTestRouter(RouterOptions{Responsibilities: service})

	agentReq := responsibilityRouteRequest(http.MethodGet, "/api/responsibility-profiles/agents/agent-1", "", "tenant-1", "reader-1", "user", operations.PermissionWorkforceRead)
	agentRecorder := httptest.NewRecorder()
	handler.ServeHTTP(agentRecorder, agentReq)
	if agentRecorder.Code != http.StatusOK {
		t.Fatalf("agent profile status=%d body=%s", agentRecorder.Code, agentRecorder.Body.String())
	}
	var agentProfile responsibility.PeopleResponsibilityProfileDTO
	if err := json.Unmarshal(agentRecorder.Body.Bytes(), &agentProfile); err != nil || agentProfile.ResponsibilityCount != 1 || agentProfile.VersionCount != 1 {
		t.Fatalf("agent profile=%+v err=%v", agentProfile, err)
	}
	currentOnlyReq := responsibilityRouteRequest(http.MethodGet, "/api/responsibility-profiles/agents/agent-1?history_limit=0", "", "tenant-1", "reader-1", "user", operations.PermissionWorkforceRead)
	currentOnlyRecorder := httptest.NewRecorder()
	handler.ServeHTTP(currentOnlyRecorder, currentOnlyReq)
	if currentOnlyRecorder.Code != http.StatusOK {
		t.Fatalf("current-only agent profile status=%d body=%s", currentOnlyRecorder.Code, currentOnlyRecorder.Body.String())
	}
	var currentOnlyProfile responsibility.PeopleResponsibilityProfileDTO
	if err := json.Unmarshal(currentOnlyRecorder.Body.Bytes(), &currentOnlyProfile); err != nil || currentOnlyProfile.ResponsibilityCount != 1 || currentOnlyProfile.VersionCount != 0 || len(currentOnlyProfile.Portfolios) != 1 || len(currentOnlyProfile.Portfolios[0].Versions) != 0 || len(currentOnlyProfile.Portfolios[0].Audit) != 0 {
		t.Fatalf("current-only agent profile=%+v err=%v", currentOnlyProfile, err)
	}

	systemBindingID := responsibility.PeopleAgentBindingID("system", "people-tenant", "subject-system")
	systemPortfolio := portfolio
	systemPortfolio.ID = responsibility.PeopleResponsibilityPortfolioID(systemBindingID)
	systemPortfolio.TenantID = "system"
	systemPortfolio.BindingID = systemBindingID
	systemPortfolio.SubjectID = "subject-system"
	systemPortfolio.SourceRevision = "10:1"
	systemPortfolio.SourceDigest = "digest-system"
	if _, _, err := repository.SavePeopleResponsibilityPortfolio(ctx, systemPortfolio, env.Clock.Now()); err != nil {
		t.Fatal(err)
	}
	defaultTenantReq := responsibilityRouteRequest(http.MethodGet, "/api/responsibility-profiles/agents/agent-1", "", "", "reader-1", "user", operations.PermissionWorkforceRead)
	defaultTenantRecorder := httptest.NewRecorder()
	handler.ServeHTTP(defaultTenantRecorder, defaultTenantReq)
	if defaultTenantRecorder.Code != http.StatusOK {
		t.Fatalf("default tenant profile status=%d body=%s", defaultTenantRecorder.Code, defaultTenantRecorder.Body.String())
	}
	var defaultTenantProfile responsibility.PeopleResponsibilityProfileDTO
	if err := json.Unmarshal(defaultTenantRecorder.Body.Bytes(), &defaultTenantProfile); err != nil || len(defaultTenantProfile.Portfolios) != 1 || defaultTenantProfile.Portfolios[0].SubjectID != "subject-system" {
		t.Fatalf("default tenant profile=%+v err=%v", defaultTenantProfile, err)
	}

	orgReq := responsibilityRouteRequest(http.MethodGet, "/api/responsibility-profiles/organizations/org-1", "", "tenant-1", "reader-1", "user", operations.PermissionOrgRead)
	orgRecorder := httptest.NewRecorder()
	handler.ServeHTTP(orgRecorder, orgReq)
	if orgRecorder.Code != http.StatusOK {
		t.Fatalf("organization profile status=%d body=%s", orgRecorder.Code, orgRecorder.Body.String())
	}
	var organizationProfile responsibility.PeopleResponsibilityProfileDTO
	if err := json.Unmarshal(orgRecorder.Body.Bytes(), &organizationProfile); err != nil || len(organizationProfile.Portfolios) != 1 || organizationProfile.Portfolios[0].CurrentSourceRevision != "9:4" {
		t.Fatalf("organization profile=%+v err=%v", organizationProfile, err)
	}

	deniedReq := responsibilityRouteRequest(http.MethodGet, "/api/responsibility-profiles/organizations/org-1", "", "tenant-1", "reader-1", "user", operations.PermissionWorkforceRead)
	deniedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(deniedRecorder, deniedReq)
	if deniedRecorder.Code != http.StatusForbidden {
		t.Fatalf("wrong scope status=%d body=%s", deniedRecorder.Code, deniedRecorder.Body.String())
	}
}

func TestPeopleResponsibilityProfileRoutesEnforceAgentSelfAndManagerReads(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := responsibility.NewRepository(env.DBRouter, env.UnitOfWork)
	now := env.Clock.Now().UTC()
	bindingID := responsibility.PeopleAgentBindingID("tenant-1", "people-1", "subject-1")
	if _, _, err := repository.UpsertPeopleAgentBinding(context.Background(), responsibility.PeopleAgentBindingRecord{
		ID: bindingID, TenantID: "tenant-1", PeopleTenantID: "people-1", SubjectID: "subject-1", AgentProfileID: "agent-1",
	}, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.SavePeopleResponsibilityPortfolio(context.Background(), responsibility.PeopleResponsibilityPortfolio{
		ID: responsibility.PeopleResponsibilityPortfolioID(bindingID), TenantID: "tenant-1", BindingID: bindingID,
		PeopleTenantID: "people-1", SubjectID: "subject-1", AgentProfileID: "agent-1", SourceRevision: "5:2", SourceDigest: "digest-1", SourceAsOf: now,
		Responsibilities: []responsibility.PeopleResolvedResponsibility{{ID: "responsibility-1", Code: "DELIVERY", Title: "Deliver", ValidFrom: now, Revision: 1}},
	}, now); err != nil {
		t.Fatal(err)
	}
	profiles := responsibilityRouteProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"agent-1": {ID: "agent-1", DisplayName: "Agent One", OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "org-1"}}},
	}}
	service := responsibility.NewOperationsService(responsibility.OperationsServiceOptions{Repository: repository, Profiles: profiles, Clock: env.Clock})
	handler := newTestRouter(RouterOptions{Responsibilities: service})

	for _, test := range []struct {
		name, path, viewerID, viewerKind, scopes string
		status                                   int
	}{
		{name: "agent self", path: "/api/responsibility-profiles/agents/agent-1?version_limit=3", viewerID: "agent-1", viewerKind: "agent", status: http.StatusOK},
		{name: "other agent", path: "/api/responsibility-profiles/agents/agent-1", viewerID: "agent-2", viewerKind: "agent", status: http.StatusForbidden},
		{name: "manager agent", path: "/api/responsibility-profiles/agents/agent-1", viewerID: "manager-1", viewerKind: "user", scopes: "operations.read", status: http.StatusOK},
		{name: "manager organization", path: "/api/responsibility-profiles/organizations/org-1", viewerID: "manager-1", viewerKind: "user", scopes: "operations.read", status: http.StatusOK},
		{name: "unprivileged organization", path: "/api/responsibility-profiles/organizations/org-1", viewerID: "agent-1", viewerKind: "agent", status: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := responsibilityRouteRequest(http.MethodGet, test.path, "", "tenant-1", test.viewerID, test.viewerKind, test.scopes)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.status {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, test.status, recorder.Body.String())
			}
		})
	}
}

type responsibilityRouteProfiles struct {
	profiles map[agentcore.AgentProfileID]agentcore.AgentProfile
}

func (p responsibilityRouteProfiles) GetProfile(_ context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	profile, ok := p.profiles[id]
	if !ok {
		return agentcore.AgentProfile{}, errors.New("profile not found")
	}
	return profile, nil
}

func TestResponsibilityRoutesAllowOnlyTenantScopedAgentSelfReads(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	service, repository, tasks := responsibilityRouteFixture(t, env)
	own := seedResponsibilityRouteAssignment(t, env, repository, tasks, "tenant-1", "mission-1", "task-own", "agent-1")
	other := seedResponsibilityRouteAssignment(t, env, repository, tasks, "tenant-1", "mission-1", "task-other", "agent-2")
	crossTenant := seedResponsibilityRouteAssignment(t, env, repository, tasks, "tenant-2", "mission-2", "task-cross", "agent-1")
	handler := newTestRouter(RouterOptions{Responsibilities: service})

	listRequest := responsibilityRouteRequest(http.MethodGet, "/api/responsibilities?agent_id=agent-2", "", "tenant-1", "agent-1", "agent", "")
	listRecorder := httptest.NewRecorder()
	handler.ServeHTTP(listRecorder, listRequest)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("self list status=%d body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	var page responsibility.ResponsibilityInboxPageDTO
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ResponsibilityID != own.ID {
		t.Fatalf("self list items=%+v", page.Items)
	}

	for _, test := range []struct {
		name   string
		id     string
		status int
	}{
		{name: "own", id: own.ID, status: http.StatusOK},
		{name: "other agent", id: other.ID, status: http.StatusForbidden},
		{name: "other tenant", id: crossTenant.ID, status: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := responsibilityRouteRequest(http.MethodGet, "/api/responsibilities/"+test.id, "", "tenant-1", "agent-1", "agent", "")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			if recorder.Code != test.status {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, test.status, recorder.Body.String())
			}
		})
	}

	humanRequest := responsibilityRouteRequest(http.MethodGet, "/api/responsibilities", "", "tenant-1", "human-1", "user", "")
	humanRecorder := httptest.NewRecorder()
	handler.ServeHTTP(humanRecorder, humanRequest)
	if humanRecorder.Code != http.StatusForbidden {
		t.Fatalf("unprivileged human status=%d body=%s", humanRecorder.Code, humanRecorder.Body.String())
	}
}

func TestResponsibilityRoutesHonorOperationsPermissionsAndControlCAS(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	service, repository, tasks := responsibilityRouteFixture(t, env)
	assignment := seedResponsibilityRouteAssignment(t, env, repository, tasks, "tenant-1", "mission-1", "task-managed", "agent-2")
	handler := newTestRouter(RouterOptions{Responsibilities: service})

	listRequest := responsibilityRouteRequest(http.MethodGet, "/api/responsibilities?agent_id=agent-2", "", "tenant-1", "manager-1", "user", "operations.read")
	listRecorder := httptest.NewRecorder()
	handler.ServeHTTP(listRecorder, listRequest)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("manager list status=%d body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	var page responsibility.ResponsibilityInboxPageDTO
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Items[0].ResponsibilityID != assignment.ID {
		t.Fatalf("manager list page=%+v err=%v", page, err)
	}

	pause := responsibilityRouteRequest(http.MethodPost, "/api/responsibilities/"+assignment.ID+"/pause", `{"reason":"maintenance"}`, "tenant-1", "manager-1", "user", "operations.manage")
	pause.Header.Set("Idempotency-Key", "pause-managed")
	pause.Header.Set("If-Match", `"1"`)
	pauseRecorder := httptest.NewRecorder()
	handler.ServeHTTP(pauseRecorder, pause)
	if pauseRecorder.Code != http.StatusOK {
		t.Fatalf("pause status=%d body=%s", pauseRecorder.Code, pauseRecorder.Body.String())
	}
	stored, err := repository.GetAssignment(context.Background(), assignment.ID)
	if err != nil || !stored.Paused || stored.Version != 2 {
		t.Fatalf("paused assignment=%+v err=%v", stored, err)
	}

	stale := responsibilityRouteRequest(http.MethodPost, "/api/responsibilities/"+assignment.ID+"/terminate", `{"reason":"stop"}`, "tenant-1", "manager-1", "user", "operations.manage")
	stale.Header.Set("Idempotency-Key", "terminate-stale")
	stale.Header.Set("If-Match", `"1"`)
	staleRecorder := httptest.NewRecorder()
	handler.ServeHTTP(staleRecorder, stale)
	if staleRecorder.Code != http.StatusConflict {
		t.Fatalf("stale control status=%d body=%s", staleRecorder.Code, staleRecorder.Body.String())
	}
}

func responsibilityRouteFixture(t *testing.T, env *testkit.Env) (*responsibility.OperationsService, *responsibility.Repository, *missiontask.PlatformRepository) {
	t.Helper()
	repository := responsibility.NewRepository(env.DBRouter, env.UnitOfWork)
	tasks := missiontask.NewPlatformRepository()
	domain := responsibility.NewService(responsibility.ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
	return responsibility.NewOperationsService(responsibility.OperationsServiceOptions{Repository: repository, Service: domain, Clock: env.Clock}), repository, tasks
}

func seedResponsibilityRouteAssignment(t *testing.T, env *testkit.Env, repository *responsibility.Repository, tasks *missiontask.PlatformRepository, tenantID, missionID, taskID, agentID string) responsibility.AssignmentRecord {
	t.Helper()
	record := missiontask.TaskRecord{Contract: missiontask.TaskContract{ID: missiontask.TaskID(taskID), MissionID: missioncore.MissionID(missionID)}, Status: missiontask.TaskStatusAssigned, Version: 1}
	record.Contract.Title = taskID
	record.Contract.Goal = taskID
	record.Contract.ExpectedDeliverables = []missiontask.ExpectedDeliverable{{ID: missiontask.ExpectedDeliverableID("deliverable-" + taskID), Title: taskID}}
	if err := env.UnitOfWork.Do(context.Background(), func(ctx context.Context, tx uow.Tx) error {
		if err := tasks.SaveTask(ctx, tx, record); err != nil {
			return err
		}
		assignment, _, err := repository.AssignMissionTaskTx(ctx, tx, responsibility.MissionTaskAssignmentInput{TenantID: tenantID, MissionID: missionID,
			TaskID: taskID, AgentProfileID: agentID, Priority: 50, AcceptBy: env.Clock.Now().Add(time.Minute), ActivationCause: responsibility.ActivationCauseAssigned}, env.Clock.Now())
		_ = assignment
		return err
	}); err != nil {
		t.Fatal(err)
	}
	assignment, err := repository.GetAssignmentForWork(context.Background(), tenantID, responsibility.WorkKindMissionTask, taskID)
	if err != nil {
		t.Fatal(err)
	}
	return assignment
}

func responsibilityRouteRequest(method, path, body, tenantID, viewerID, viewerKind, scopes string) *http.Request {
	req := authedRequest(method, path, body)
	req.Header.Set("X-Tenant-ID", tenantID)
	req.Header.Set("X-User-ID", viewerID)
	req.Header.Set("X-Viewer-Kind", viewerKind)
	req.Header.Set("X-Permission-Scope", scopes)
	return req
}

type responsibilityProfileRouteProfiles struct {
	profiles map[agentcore.AgentProfileID]agentcore.AgentProfile
}

func (p responsibilityProfileRouteProfiles) GetProfile(_ context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	profile, ok := p.profiles[id]
	if !ok {
		return agentcore.AgentProfile{}, agentcore.ErrNotFound
	}
	return profile, nil
}
