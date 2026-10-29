package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/userauth"
)

func TestHTTPHILDecisionRoutesUseAuthenticatedIdentity(t *testing.T) {
	service := &fakeHILService{createResult: hil.CreateDecisionResult{Decision: hil.DecisionRequest{ID: "decision-1", Route: hil.RouteDecision{Mode: hil.ModeHIL}}}}
	handler := newTestRouter(RouterOptions{HIL: service})

	req := authedRequest(http.MethodPost, "/api/hil/decisions", `{"action":"customer.update","resource":{"kind":"customer","id":"customer-1"},"object_refs":[{"kind":"customer","id":"customer-1"}],"subject_refs":[{"kind":"customer","id":"customer-1"}],"args_hash":"sha256:args","risk_level":"medium","purpose":"correct record","expires_at":"2026-08-09T12:00:00Z"}`)
	req.Header.Set("Idempotency-Key", "decision-key-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}
	if service.createInput.TenantID != "system" || service.createInput.Requester != (hil.ActorRef{Kind: hil.ActorAgent, ID: "agent-1"}) || service.createInput.IdempotencyKey != "decision-key-1" {
		t.Fatalf("create input = %+v", service.createInput)
	}

	list := authedRequest(http.MethodGet, "/api/hil/decisions?mode=council&status=pending&limit=10&offset=3", "")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, list)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", rec.Code, rec.Body.String())
	}
	if service.listQuery.TenantID != "system" || service.listQuery.Mode != hil.ModeCouncil || service.listQuery.Status != hil.DecisionPending || service.listQuery.Limit != 10 || service.listQuery.Offset != 3 {
		t.Fatalf("list query = %+v", service.listQuery)
	}
}

func TestHTTPHILDecisionListScopesActionableCouncilToAuthenticatedParticipant(t *testing.T) {
	service := &fakeHILService{}
	handler := newTestRouter(RouterOptions{HIL: service})
	req := authedRequest(http.MethodGet, "/api/hil/decisions?mode=council&status=pending&mine=true", "")
	req.Header.Set("X-User-ID", "operator")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Agent-Profile-ID", "profile-operator")
	req.Header.Set("X-Tenant-ID", "tenant-a")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !service.listQuery.Mine || service.listQuery.ActionableBy != (hil.ActorRef{Kind: hil.ActorHuman, ID: "profile-operator"}) {
		t.Fatalf("list query = %+v", service.listQuery)
	}
}

func TestHTTPHILDecisionListForcesOwnQueueForApprovalReader(t *testing.T) {
	service := &fakeHILService{}
	handler := newTestRouter(RouterOptions{HIL: service})
	req := authedRequest(http.MethodGet, "/api/hil/decisions?mode=hil&status=pending&mine=false", "")
	req.Header.Set("X-User-ID", "operator")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Agent-Profile-ID", "human-operator")
	req.Header.Set("X-Permission-Scope", "approvals.read")
	req.Header.Set("X-Tenant-ID", "tenant-a")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !service.listQuery.Mine || service.listQuery.ActionableBy != (hil.ActorRef{Kind: hil.ActorHuman, ID: "human-operator"}) {
		t.Fatalf("list query = %+v, want own queue", service.listQuery)
	}
}

func TestHTTPHILDecisionListRequiresModeForApprovalReader(t *testing.T) {
	service := &fakeHILService{}
	handler := newTestRouter(RouterOptions{HIL: service})
	req := authedRequest(http.MethodGet, "/api/hil/decisions?limit=100", "")
	req.Header.Set("X-User-ID", "operator")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Agent-Profile-ID", "human-operator")
	req.Header.Set("X-Permission-Scope", "approvals.read")
	req.Header.Set("X-Tenant-ID", "tenant-a")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("list status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHILDecisionListUsesSystemTenantForBrowserSessionWithoutTenant(t *testing.T) {
	service := &fakeHILService{}
	router := &Router{hil: service}
	principal := userauth.PrincipalFromSession(userauth.SessionDTO{
		SubjectProvider: "kratos",
		Subject:         "subject-ding",
		User: userauth.UserDTO{
			UserID:         "ding",
			Username:       "ding",
			AgentProfileID: "human-ding",
		},
	})
	if principal.TenantID != "" {
		t.Fatalf("browser session tenant = %q, want empty before HIL routing", principal.TenantID)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/hil/decisions?mode=hil&status=pending&mine=true", nil)
	req = req.WithContext(identity.ContextWithPrincipal(req.Context(), principal))
	rec := httptest.NewRecorder()
	router.handleListHILDecisions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", rec.Code, rec.Body.String())
	}
	if service.listQuery.TenantID != "system" || service.listQuery.ActionableBy != (hil.ActorRef{Kind: hil.ActorHuman, ID: "human-ding"}) {
		t.Fatalf("list query = %+v", service.listQuery)
	}
}

func TestHILDecisionListKeepsSystemPersonalQueueAfterBrowserOrganizationScope(t *testing.T) {
	service := &fakeHILService{}
	principal := userauth.PrincipalFromSession(userauth.SessionDTO{
		SubjectProvider: "kratos",
		Subject:         "subject-ding",
		PermissionScope: []string{"approvals.read"},
		User: userauth.UserDTO{
			UserID:         "ding",
			Username:       "ding",
			AgentProfileID: "H000001",
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/hil/decisions?mode=approval&status=pending&mine=true", nil)
	req = req.WithContext(identity.ContextWithPrincipal(req.Context(), principal))
	applyPrincipalViewer(req, principal)
	router := &Router{
		hil: service,
		agentProfiles: todoViewerProfileDirectory{profile: agentcore.AgentProfile{
			ID:        "H000001",
			AgentKind: agentcore.AgentKindHuman,
			Enabled:   true,
			Placement: agentcore.TalentPlacement{Status: agentcore.TalentPlacementInternal},
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef: agentcore.OrganizationRef{ID: "minvia"},
			},
		}},
	}
	router.applyPrincipalOrganizationScope(req, principal)
	rec := httptest.NewRecorder()
	router.handleListHILDecisions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", rec.Code, rec.Body.String())
	}
	if service.listQuery.TenantID != "minvia" || len(service.listQuery.AdditionalTenantIDs) != 1 || service.listQuery.AdditionalTenantIDs[0] != hil.BuiltInWorkflowTenantID {
		t.Fatalf("list query tenants = %+v, want minvia plus system", service.listQuery)
	}
	if !service.listQuery.Mine || service.listQuery.ActionableBy != (hil.ActorRef{Kind: hil.ActorHuman, ID: "H000001"}) {
		t.Fatalf("list query = %+v, want Ding personal queue", service.listQuery)
	}
}

func TestHTTPHILDecisionListBackfillsReadableContentFromLinkedDiscussion(t *testing.T) {
	service := &fakeHILService{listPage: hil.DecisionPage{Items: []hil.DecisionRequest{{
		ID: "decision-missing-content", TenantID: "system", Requester: hil.ActorRef{Kind: hil.ActorAgent, ID: "publisher"},
		Action: "runtime.approval", ActionTypeID: "tool_use",
		ObjectRefs: []hil.ObjectRef{{Kind: "conversation", ID: "conversation-1"}, {Kind: "agent_run", ID: "run-1"}},
		PayloadPreview: map[string]any{
			"approval_conversation_id": "conversation-1", "approval_run_id": "run-1",
			"approval_request_id": "tool-call-1", "approval_tool_name": "discussion_talent_list",
			"summary": "Runtime approval required for discussion_talent_list",
		},
		Route: hil.RouteDecision{Mode: hil.ModeHIL}, Status: hil.DecisionPending,
	}}}}
	discussions := &fakeDiscussionService{conversation: discussionsession.ConversationDTO{
		ConversationID: "conversation-1", Title: "发布任务复核",
		Messages: []discussionsession.MessageDTO{{
			MessageID: "message-1", TurnID: "turn-1", Role: "user", Content: "查找适合复核本期发布内容的人才。",
		}},
		Runs: []discussionsession.RunDTO{{
			RunID: "run-1", TurnID: "turn-1", ToolCalls: []discussionsession.ToolCallDTO{{
				ToolCallID: "tool-call-1", Name: "discussion_talent_list", Arguments: map[string]string{"query": "内容发布复核"},
			}},
		}},
	}}
	handler := newTestRouter(RouterOptions{HIL: service, Discussions: discussions})
	req := authedRequest(http.MethodGet, "/api/hil/decisions?mode=hil&status=pending", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", rec.Code, rec.Body.String())
	}
	var page hil.DecisionPage
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("items = %+v", page.Items)
	}
	payload := page.Items[0].PayloadPreview
	if payload["content"] != "内容发布复核" || payload["request_context"] != "查找适合复核本期发布内容的人才。" {
		t.Fatalf("enriched payload = %+v", payload)
	}
	if payload["title"] != "发布任务复核" || !discussions.getCalled || discussions.getConversationID != "conversation-1" {
		t.Fatalf("discussion lookup title=%v called=%v id=%q", payload["title"], discussions.getCalled, discussions.getConversationID)
	}
}

func TestHTTPHILDecisionListKeepsStoredMaterialWithoutDiscussionLookup(t *testing.T) {
	service := &fakeHILService{listPage: hil.DecisionPage{Items: []hil.DecisionRequest{{
		ID: "decision-with-content", TenantID: "system", Requester: hil.ActorRef{Kind: hil.ActorAgent, ID: "publisher"},
		Action: "runtime.approval", ObjectRefs: []hil.ObjectRef{{Kind: "conversation", ID: "conversation-1"}},
		PayloadPreview: map[string]any{"content": "已经持久化的完整审批内容", "approval_conversation_id": "conversation-1"},
		Route:          hil.RouteDecision{Mode: hil.ModeHIL}, Status: hil.DecisionPending,
	}}}}
	discussions := &fakeDiscussionService{conversation: discussionsession.ConversationDTO{ConversationID: "conversation-1"}}
	handler := newTestRouter(RouterOptions{HIL: service, Discussions: discussions})
	req := authedRequest(http.MethodGet, "/api/hil/decisions?mode=hil&status=pending", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", rec.Code, rec.Body.String())
	}
	if discussions.getCalled {
		t.Fatal("stored readable material unexpectedly triggered a discussion lookup")
	}
}

func TestHTTPHILDecisionListScopesAssignedFixedApprovalToAuthenticatedHuman(t *testing.T) {
	service := &fakeHILService{}
	handler := newTestRouter(RouterOptions{HIL: service})
	req := authedRequest(http.MethodGet, "/api/hil/decisions?mode=approval&status=pending&mine=true", "")
	req.Header.Set("X-User-ID", "operator")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Agent-Profile-ID", "profile-operator")
	req.Header.Set("X-Tenant-ID", "tenant-a")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !service.listQuery.Mine || service.listQuery.Mode != hil.ModeApproval || service.listQuery.ActionableBy != (hil.ActorRef{Kind: hil.ActorHuman, ID: "profile-operator"}) {
		t.Fatalf("list query = %+v", service.listQuery)
	}
	if len(service.listQuery.AdditionalTenantIDs) != 1 || service.listQuery.AdditionalTenantIDs[0] != hil.BuiltInWorkflowTenantID {
		t.Fatalf("additional list tenants = %+v, want system personal queue", service.listQuery.AdditionalTenantIDs)
	}
}

func TestHTTPHILDecisionListHonorsManagerMineFilter(t *testing.T) {
	service := &fakeHILService{}
	handler := newTestRouter(RouterOptions{HIL: service})
	req := authedRequest(http.MethodGet, "/api/hil/decisions?mode=hil&status=pending&mine=true", "")
	req.Header.Set("X-User-ID", "admin")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Agent-Profile-ID", "human-admin")
	req.Header.Set("X-Permission-Scope", "approvals.read,approvals.manage")
	req.Header.Set("X-Tenant-ID", "tenant-a")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !service.listQuery.Mine || service.listQuery.ActionableBy != (hil.ActorRef{Kind: hil.ActorHuman, ID: "human-admin"}) {
		t.Fatalf("approval manager list query = %+v, want personal queue", service.listQuery)
	}

	all := authedRequest(http.MethodGet, "/api/hil/decisions?mode=hil&status=pending", "")
	all.Header.Set("X-User-ID", "admin")
	all.Header.Set("X-Viewer-Kind", "user")
	all.Header.Set("X-Agent-Profile-ID", "human-admin")
	all.Header.Set("X-Permission-Scope", "approvals.read,approvals.manage")
	all.Header.Set("X-Tenant-ID", "tenant-a")
	allRec := httptest.NewRecorder()
	handler.ServeHTTP(allRec, all)
	if allRec.Code != http.StatusOK || service.listQuery.Mine {
		t.Fatalf("approval manager tenant queue status=%d query=%+v", allRec.Code, service.listQuery)
	}
	if len(service.listQuery.AdditionalTenantIDs) != 0 {
		t.Fatalf("tenant-wide manager query leaked shared tenants: %+v", service.listQuery.AdditionalTenantIDs)
	}

	invalid := authedRequest(http.MethodGet, "/api/hil/decisions?mode=auto&mine=true", "")
	invalid.Header.Set("X-User-ID", "admin")
	invalid.Header.Set("X-Viewer-Kind", "user")
	invalid.Header.Set("X-Agent-Profile-ID", "human-admin")
	invalid.Header.Set("X-Permission-Scope", "approvals.read,approvals.manage")
	invalid.Header.Set("X-Tenant-ID", "tenant-a")
	invalidRec := httptest.NewRecorder()
	handler.ServeHTTP(invalidRec, invalid)
	if invalidRec.Code != http.StatusBadRequest {
		t.Fatalf("invalid manager filter status = %d body=%s", invalidRec.Code, invalidRec.Body.String())
	}
}

func TestHTTPHILDecisionListRejectsInvalidMineFilter(t *testing.T) {
	service := &fakeHILService{}
	handler := newTestRouter(RouterOptions{HIL: service})
	for _, path := range []string{
		"/api/hil/decisions?mode=auto&mine=true",
		"/api/hil/decisions?mine=true",
		"/api/hil/decisions?mode=council&mine=not-a-boolean",
	} {
		req := authedRequest(http.MethodGet, path, "")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("path %s status = %d body=%s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestHTTPHILDecisionResolveRouteUsesAuthenticatedIdentity(t *testing.T) {
	service := &fakeHILService{resolution: hil.DecisionResolution{Decision: hil.DecisionRequest{ID: "decision-1", Status: hil.DecisionResolved}}}
	handler := newTestRouter(RouterOptions{HIL: service})
	req := authedRequest(http.MethodPost, "/api/hil/decisions/decision-1/decision", `{"decision":"approve","reason":"reviewed"}`)
	req.Header.Set("X-User-ID", "operator")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Agent-Profile-ID", "profile-operator")
	req.Header.Set("X-Tenant-ID", "tenant-a")
	req.Header.Set("Idempotency-Key", "resolve-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve status = %d body=%s", rec.Code, rec.Body.String())
	}
	if service.resolveTenant != "tenant-a" || service.resolveID != "decision-1" || service.resolveActor != (hil.ActorRef{Kind: hil.ActorHuman, ID: "profile-operator"}) || service.resolveInput.IdempotencyKey != "resolve-1" || service.resolveInput.Decision != "approve" {
		t.Fatalf("resolve call tenant=%q id=%q actor=%+v input=%+v", service.resolveTenant, service.resolveID, service.resolveActor, service.resolveInput)
	}
}

func TestHTTPHILDecisionResolveDelegatesWorkflowAssignmentToService(t *testing.T) {
	service := &fakeHILService{
		decision: hil.DecisionRequest{
			ID: "workflow-decision-1", TenantID: "tenant-a", Route: hil.RouteDecision{Mode: hil.ModeApproval}, Status: hil.DecisionPending,
		},
		resolution: hil.DecisionResolution{Decision: hil.DecisionRequest{ID: "workflow-decision-1", Status: hil.DecisionResolved}},
	}
	handler := newTestRouter(RouterOptions{HIL: service})
	req := authedRequest(http.MethodPost, "/api/hil/decisions/workflow-decision-1/decision", `{"decision":"approve"}`)
	req.Header.Set("X-User-ID", "operator")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Agent-Profile-ID", "workflow-task-assignee")
	req.Header.Set("X-Tenant-ID", "tenant-a")
	req.Header.Set("Idempotency-Key", "resolve-workflow-assignment")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve status = %d body=%s", rec.Code, rec.Body.String())
	}
	if service.resolveCalls != 1 || service.resolveActor.ID != "workflow-task-assignee" {
		t.Fatalf("ResolveDecision calls=%d actor=%+v", service.resolveCalls, service.resolveActor)
	}
}

func TestHTTPHILDecisionResolveHonorsAssignedApprover(t *testing.T) {
	for _, test := range []struct {
		name       string
		payload    map[string]any
		profile    string
		status     int
		resolveErr error
	}{
		{
			name:       "generic decision rejects a different approver",
			payload:    map[string]any{"approval_assigned_to": "profile-owner"},
			profile:    "profile-other",
			status:     http.StatusForbidden,
			resolveErr: hil.ErrParticipantRequired,
		},
		{
			name:    "generic decision accepts the assigned approver",
			payload: map[string]any{"approval_assigned_to": "profile-owner"},
			profile: "profile-owner",
			status:  http.StatusOK,
		},
		{
			name:       "Todo requires an explicit assigned approver",
			payload:    map[string]any{"source": "todo_automation"},
			profile:    "profile-owner",
			status:     http.StatusForbidden,
			resolveErr: hil.ErrParticipantRequired,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeHILService{
				decision:   hil.DecisionRequest{ID: "decision-1", PayloadPreview: test.payload},
				resolution: hil.DecisionResolution{Decision: hil.DecisionRequest{ID: "decision-1", Status: hil.DecisionResolved}},
				resolveErr: test.resolveErr,
			}
			handler := newTestRouter(RouterOptions{HIL: service})
			req := authedRequest(http.MethodPost, "/api/hil/decisions/decision-1/decision", `{"decision":"approve"}`)
			req.Header.Set("X-User-ID", "operator")
			req.Header.Set("X-Viewer-Kind", "user")
			req.Header.Set("X-Agent-Profile-ID", test.profile)
			req.Header.Set("X-Tenant-ID", "tenant-a")
			req.Header.Set("Idempotency-Key", "test-idempotency-key")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != test.status {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
			if service.resolveCalls != 1 {
				t.Fatalf("ResolveDecision calls = %d, want 1", service.resolveCalls)
			}
		})
	}
}

func TestHTTPHILDecisionResolveFallsBackToActionableSystemAssignment(t *testing.T) {
	decision := hil.DecisionRequest{
		ID: "decision-system", TenantID: hil.BuiltInWorkflowTenantID, Route: hil.RouteDecision{Mode: hil.ModeApproval},
		PayloadPreview: map[string]any{"approval_assigned_to": "profile-operator"}, Status: hil.DecisionPending,
	}
	service := &fakeHILService{
		listPage:               hil.DecisionPage{Items: []hil.DecisionRequest{decision}, Total: 1},
		decisionsByTenant:      map[string]hil.DecisionRequest{hil.BuiltInWorkflowTenantID: decision},
		decisionErrorsByTenant: map[string]error{"tenant-a": hil.ErrNotFound},
		resolution:             hil.DecisionResolution{Decision: hil.DecisionRequest{ID: decision.ID, Status: hil.DecisionResolved}},
	}
	handler := newTestRouter(RouterOptions{HIL: service})
	req := authedRequest(http.MethodPost, "/api/hil/decisions/decision-system/decision", `{"decision":"approve"}`)
	req.Header.Set("X-User-ID", "operator")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Agent-Profile-ID", "profile-operator")
	req.Header.Set("X-Tenant-ID", "tenant-a")
	req.Header.Set("Idempotency-Key", "resolve-system-assignment")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve status = %d body=%s", rec.Code, rec.Body.String())
	}
	if service.resolveTenant != hil.BuiltInWorkflowTenantID || service.resolveCalls != 1 {
		t.Fatalf("resolve tenant=%q calls=%d, want system", service.resolveTenant, service.resolveCalls)
	}
	if service.listQuery.TenantID != hil.BuiltInWorkflowTenantID || service.listQuery.DecisionID != decision.ID || !service.listQuery.Mine {
		t.Fatalf("system actionability query = %+v", service.listQuery)
	}
}

func TestHTTPHILDecisionSystemFallbackHidesUnassignedDecision(t *testing.T) {
	decision := hil.DecisionRequest{
		ID: "decision-system", TenantID: hil.BuiltInWorkflowTenantID, Route: hil.RouteDecision{Mode: hil.ModeApproval},
		PayloadPreview: map[string]any{"approval_assigned_to": "profile-other"}, Status: hil.DecisionPending,
	}
	service := &fakeHILService{
		decisionsByTenant:      map[string]hil.DecisionRequest{hil.BuiltInWorkflowTenantID: decision},
		decisionErrorsByTenant: map[string]error{"tenant-a": hil.ErrNotFound},
	}
	handler := newTestRouter(RouterOptions{HIL: service})
	req := authedRequest(http.MethodGet, "/api/hil/decisions/decision-system", "")
	req.Header.Set("X-User-ID", "operator")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Agent-Profile-ID", "profile-operator")
	req.Header.Set("X-Tenant-ID", "tenant-a")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get status = %d body=%s, want hidden system decision", rec.Code, rec.Body.String())
	}
	if service.resolveCalls != 0 {
		t.Fatalf("ResolveDecision calls = %d, want 0", service.resolveCalls)
	}
}

func TestHTTPHILDecisionResolveRejectsAgentForAssignedHumanApproval(t *testing.T) {
	service := &fakeHILService{
		decision:   hil.DecisionRequest{ID: "decision-1", PayloadPreview: map[string]any{"approval_assigned_to": "profile-owner"}},
		resolution: hil.DecisionResolution{Decision: hil.DecisionRequest{ID: "decision-1", Status: hil.DecisionResolved}},
		resolveErr: hil.ErrParticipantRequired,
	}
	handler := newTestRouter(RouterOptions{HIL: service})
	req := authedRequest(http.MethodPost, "/api/hil/decisions/decision-1/decision", `{"decision":"approve"}`)
	req.Header.Set("X-User-ID", "profile-owner")
	req.Header.Set("X-Viewer-Kind", "agent")
	req.Header.Set("X-Tenant-ID", "tenant-a")
	req.Header.Set("Idempotency-Key", "resolve-assigned-agent-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if service.resolveCalls != 1 {
		t.Fatalf("ResolveDecision calls = %d, want 1", service.resolveCalls)
	}
}

func TestHTTPHILDecisionRejectsBodyIdentityAndMissingIdempotency(t *testing.T) {
	service := &fakeHILService{}
	handler := newTestRouter(RouterOptions{HIL: service})

	for _, test := range []struct {
		name string
		body string
		key  string
	}{
		{name: "body tenant", body: `{"tenant_id":"other","action":"customer.update","resource":{"kind":"customer","id":"customer-1"},"subject_refs":[{"kind":"customer","id":"customer-1"}],"args_hash":"sha256:args"}`, key: "decision-key"},
		{name: "missing key", body: `{"action":"customer.update","resource":{"kind":"customer","id":"customer-1"},"subject_refs":[{"kind":"customer","id":"customer-1"}],"args_hash":"sha256:args"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := authedRequest(http.MethodPost, "/api/hil/decisions", test.body)
			if test.key != "" {
				req.Header.Set("Idempotency-Key", test.key)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
	if service.createCalls != 0 {
		t.Fatalf("CreateDecision calls = %d, want 0", service.createCalls)
	}
}

func TestHTTPHILCouncilMutationUsesHumanAuthorizationSubject(t *testing.T) {
	service := &fakeHILService{council: hil.CouncilSession{ID: "council-1", TenantID: "tenant-a", Status: hil.CouncilOpen}}
	handler := newTestRouter(RouterOptions{HIL: service})

	req := authedRequest(http.MethodPost, "/api/hil/councils/council-1/contributions", `{"position":"approve","reasoning":"evidence supports it","evidence_refs":["evidence-1"]}`)
	req.Header.Set("X-User-ID", "operator")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Agent-Profile-ID", "profile-operator")
	req.Header.Set("X-Tenant-ID", "tenant-a")
	req.Header.Set("Idempotency-Key", "contribution-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("contribution status = %d body=%s", rec.Code, rec.Body.String())
	}
	if service.contributionActor != (hil.ActorRef{Kind: hil.ActorHuman, ID: "profile-operator"}) || service.contribution.IdempotencyKey != "contribution-1" {
		t.Fatalf("contribution actor=%+v input=%+v", service.contributionActor, service.contribution)
	}

	vote := authedRequest(http.MethodPost, "/api/hil/councils/council-1/votes", `{"choice":"approve","reason":"supported"}`)
	vote.Header.Set("X-User-ID", "operator")
	vote.Header.Set("X-Viewer-Kind", "user")
	vote.Header.Set("X-Agent-Profile-ID", "profile-operator")
	vote.Header.Set("X-Tenant-ID", "tenant-a")
	vote.Header.Set("Idempotency-Key", "vote-1")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, vote)
	if rec.Code != http.StatusOK {
		t.Fatalf("vote status = %d body=%s", rec.Code, rec.Body.String())
	}
	if service.voteActor != (hil.ActorRef{Kind: hil.ActorHuman, ID: "profile-operator"}) || service.vote.IdempotencyKey != "vote-1" {
		t.Fatalf("vote actor=%+v input=%+v", service.voteActor, service.vote)
	}
}

func TestHTTPHILErrorMapping(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status int
	}{
		{name: "non participant", err: hil.ErrParticipantRequired, status: http.StatusForbidden},
		{name: "immutable vote", err: hil.ErrVoteImmutable, status: http.StatusConflict},
		{name: "idempotency", err: hil.ErrIdempotencyConflict, status: http.StatusConflict},
		{name: "context unavailable", err: hil.ErrContextUnavailable, status: http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeHILService{voteErr: test.err}
			handler := newTestRouter(RouterOptions{HIL: service})
			req := authedRequest(http.MethodPost, "/api/hil/councils/council-1/votes", `{"choice":"approve"}`)
			req.Header.Set("Idempotency-Key", "vote-1")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != test.status {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHTTPHILCouncilHidesOtherOpinionsUntilContributionPhaseCompletes(t *testing.T) {
	service := &fakeHILService{council: hil.CouncilSession{
		ID: "council-1", TenantID: "system", Status: hil.CouncilOpen,
		Participants: []hil.CouncilParticipant{
			{Actor: hil.ActorRef{Kind: hil.ActorAgent, ID: "agent-1"}},
			{Actor: hil.ActorRef{Kind: hil.ActorAgent, ID: "agent-2"}},
			{Actor: hil.ActorRef{Kind: hil.ActorHuman, ID: "ding"}, Required: true},
		},
		Contributions: []hil.CouncilContribution{
			{Actor: hil.ActorRef{Kind: hil.ActorAgent, ID: "agent-1"}, Position: "approve", Reasoning: "mine"},
			{Actor: hil.ActorRef{Kind: hil.ActorAgent, ID: "agent-2"}, Position: "reject", Reasoning: "hidden"},
		},
		Votes: []hil.CouncilVote{
			{Actor: hil.ActorRef{Kind: hil.ActorAgent, ID: "agent-1"}, Choice: hil.VoteApprove},
			{Actor: hil.ActorRef{Kind: hil.ActorAgent, ID: "agent-2"}, Choice: hil.VoteReject},
		},
	}}
	handler := newTestRouter(RouterOptions{HIL: service})
	req := authedRequest(http.MethodGet, "/api/hil/councils/council-1", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var council hil.CouncilSession
	if err := json.NewDecoder(rec.Body).Decode(&council); err != nil {
		t.Fatalf("decode council: %v", err)
	}
	if len(council.Contributions) != 1 || council.Contributions[0].Reasoning != "mine" || len(council.Votes) != 1 || council.Votes[0].Actor.ID != "agent-1" {
		t.Fatalf("redacted council = %+v", council)
	}
}

type fakeHILService struct {
	createResult           hil.CreateDecisionResult
	createInput            hil.CreateDecisionInput
	createCalls            int
	listPage               hil.DecisionPage
	listQuery              hil.DecisionListQuery
	listQueries            []hil.DecisionListQuery
	decision               hil.DecisionRequest
	decisionsByTenant      map[string]hil.DecisionRequest
	decisionErrorsByTenant map[string]error
	getTenants             []string
	council                hil.CouncilSession
	contributionActor      hil.ActorRef
	contribution           hil.SubmitContributionInput
	voteActor              hil.ActorRef
	vote                   hil.CastVoteInput
	voteErr                error
	resolution             hil.DecisionResolution
	resolveTenant          string
	resolveID              string
	resolveActor           hil.ActorRef
	resolveInput           hil.ResolveDecisionInput
	resolveCalls           int
	resolveErr             error
}

func (f *fakeHILService) CreateDecision(_ context.Context, input hil.CreateDecisionInput) (hil.CreateDecisionResult, error) {
	f.createCalls++
	f.createInput = input
	return f.createResult, nil
}

func (f *fakeHILService) ListDecisions(_ context.Context, query hil.DecisionListQuery) (hil.DecisionPage, error) {
	f.listQuery = query
	f.listQueries = append(f.listQueries, query)
	if f.listPage.Items != nil || f.listPage.Total != 0 {
		return f.listPage, nil
	}
	return hil.DecisionPage{Items: []hil.DecisionRequest{}, Total: 0}, nil
}

func (f *fakeHILService) GetDecision(_ context.Context, tenantID, _ string) (hil.DecisionRequest, error) {
	f.getTenants = append(f.getTenants, tenantID)
	if err, ok := f.decisionErrorsByTenant[tenantID]; ok {
		return hil.DecisionRequest{}, err
	}
	if decision, ok := f.decisionsByTenant[tenantID]; ok {
		return decision, nil
	}
	return f.decision, nil
}

func (f *fakeHILService) GetCouncil(context.Context, string, string) (hil.CouncilSession, error) {
	return f.council, nil
}

func (f *fakeHILService) SubmitContribution(_ context.Context, _ string, _ string, actor hil.ActorRef, input hil.SubmitContributionInput) error {
	f.contributionActor = actor
	f.contribution = input
	return nil
}

func (f *fakeHILService) CastVote(_ context.Context, _ string, _ string, actor hil.ActorRef, input hil.CastVoteInput) (hil.CouncilSession, error) {
	f.voteActor = actor
	f.vote = input
	return f.council, f.voteErr
}

func (f *fakeHILService) ResolveDecision(_ context.Context, tenantID, decisionID string, actor hil.ActorRef, input hil.ResolveDecisionInput) (hil.DecisionResolution, error) {
	f.resolveCalls++
	f.resolveTenant, f.resolveID, f.resolveActor, f.resolveInput = tenantID, decisionID, actor, input
	return f.resolution, f.resolveErr
}

func TestHTTPHILDecisionRequestedByMeUsesOnlyAuthenticatedRequester(t *testing.T) {
	service := &fakeHILService{}
	handler := newTestRouter(RouterOptions{HIL: service})
	req := authedRequest(http.MethodGet, "/api/hil/decisions?mode=hil&requested_by_me=true&mine=false&requester_id=someone-else", "")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-User-ID", "operator")
	req.Header.Set("X-Agent-Profile-ID", "human-operator")
	req.Header.Set("X-Permission-Scope", "approvals.read")
	req.Header.Set("X-Tenant-ID", "tenant-a")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || service.listQuery.Mine || service.listQuery.RequestedBy != (hil.ActorRef{Kind: hil.ActorHuman, ID: "human-operator"}) || service.listQuery.TenantID != "tenant-a" {
		t.Fatalf("status=%d query=%+v body=%s", rec.Code, service.listQuery, rec.Body.String())
	}
}
