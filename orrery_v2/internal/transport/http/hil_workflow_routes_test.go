package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"orrery_v2/internal/app/hil"
)

func TestHTTPHILWorkflowTaskDecisionUsesAuthenticatedHuman(t *testing.T) {
	service := &fakeHILWorkflowService{detail: hil.WorkflowInstanceDetail{Instance: hil.WorkflowInstance{ID: "workflow-1", TenantID: "tenant-a"}}}
	handler := newTestRouter(RouterOptions{HIL: service})
	req := authedRequest(http.MethodPost, "/api/hil/workflow-tasks/task-1/decision", `{"decision":"approve","reason":"reviewed actual article","expected_instance_version":3}`)
	req.Header.Set("X-User-ID", "operator")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Agent-Profile-ID", "profile-operator")
	req.Header.Set("X-Tenant-ID", "tenant-a")
	req.Header.Set("Idempotency-Key", "workflow-decision-1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.decisionTenant != "tenant-a" || service.decisionTaskID != "task-1" || service.decisionActor != (hil.ActorRef{Kind: hil.ActorHuman, ID: "profile-operator"}) {
		t.Fatalf("decision identity tenant=%q task=%q actor=%+v", service.decisionTenant, service.decisionTaskID, service.decisionActor)
	}
	if service.decisionInput.Decision != hil.WorkflowTaskApprove || service.decisionInput.Reason != "reviewed actual article" || service.decisionInput.ExpectedInstanceVersion != 3 || service.decisionInput.IdempotencyKey != "workflow-decision-1" {
		t.Fatalf("decision input=%+v", service.decisionInput)
	}
}

func TestHTTPHILWorkflowTemplateCreateRequiresManageScope(t *testing.T) {
	service := &fakeHILWorkflowService{template: hil.WorkflowTemplate{ID: "template-1", TenantID: "tenant-a"}}
	handler := newTestRouter(RouterOptions{HIL: service})
	body := `{"name":"Marketing approval","description":"Review WeChat drafts","definition":{"risk_levels":["high"],"stages":[{"key":"review","name":"Review","order":1,"type":"approval","approval":{"strategy":"single","assignees":[{"kind":"explicit_human","id":"human-ding"}],"deadline_minutes":60}}]}}`

	denied := authedRequest(http.MethodPost, "/api/hil/workflow-templates", body)
	denied.Header.Set("X-User-ID", "operator")
	denied.Header.Set("X-Viewer-Kind", "user")
	denied.Header.Set("X-Agent-Profile-ID", "profile-operator")
	denied.Header.Set("X-Tenant-ID", "tenant-a")
	denied.Header.Set("X-Permission-Scope", "approvals.read,approvals.decide")
	denied.Header.Set("Idempotency-Key", "create-template-denied")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, denied)
	if rec.Code != http.StatusForbidden || service.saveTemplateCalls != 0 {
		t.Fatalf("denied status=%d calls=%d body=%s", rec.Code, service.saveTemplateCalls, rec.Body.String())
	}

	allowed := authedRequest(http.MethodPost, "/api/hil/workflow-templates", body)
	allowed.Header.Set("X-User-ID", "operator")
	allowed.Header.Set("X-Viewer-Kind", "user")
	allowed.Header.Set("X-Agent-Profile-ID", "profile-operator")
	allowed.Header.Set("X-Tenant-ID", "tenant-a")
	allowed.Header.Set("X-Permission-Scope", "approvals.manage")
	allowed.Header.Set("Idempotency-Key", "create-template-1")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, allowed)
	if rec.Code != http.StatusCreated {
		t.Fatalf("allowed status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.saveTemplateActor != (hil.ActorRef{Kind: hil.ActorHuman, ID: "profile-operator"}) || service.saveTemplateInput.TenantID != "tenant-a" || service.saveTemplateInput.IdempotencyKey != "create-template-1" || service.saveTemplateInput.Name != "Marketing approval" {
		t.Fatalf("template actor=%+v input=%+v", service.saveTemplateActor, service.saveTemplateInput)
	}
}

func TestHTTPHILWorkflowTemplateReadRequiresReadScope(t *testing.T) {
	service := &fakeHILWorkflowService{template: hil.WorkflowTemplate{ID: "template-1", TenantID: "tenant-a"}}
	handler := newTestRouter(RouterOptions{HIL: service})

	for _, path := range []string{"/api/hil/workflow-templates", "/api/hil/workflow-templates/template-1"} {
		allowed := authedRequest(http.MethodGet, path, "")
		allowed.Header.Set("X-Tenant-ID", "tenant-a")
		allowed.Header.Set("X-Permission-Scope", "approvals.read,approvals.decide")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, allowed)
		if rec.Code != http.StatusOK {
			t.Fatalf("read scope path=%s status=%d body=%s", path, rec.Code, rec.Body.String())
		}

		denied := authedRequest(http.MethodGet, path, "")
		denied.Header.Set("X-Tenant-ID", "tenant-a")
		denied.Header.Set("X-Permission-Scope", "settings.read")
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, denied)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("missing read scope path=%s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestHTTPHILWorkflowTemplateReadInheritsOnlyBuiltInSystemTemplates(t *testing.T) {
	service := &fakeHILWorkflowService{
		templatesByTenant: map[string][]hil.WorkflowTemplateSummary{
			"tenant-a": {{ID: "tenant-template", TenantID: "tenant-a", Name: "Tenant workflow"}},
			hil.BuiltInWorkflowTenantID: {
				{ID: hil.BuiltInBusinessWorkflowTemplateID, TenantID: hil.BuiltInWorkflowTenantID, Name: "Business approval"},
				{ID: "system-private-template", TenantID: hil.BuiltInWorkflowTenantID, Name: "Private system workflow"},
			},
		},
		templateByTenant: map[string]map[string]hil.WorkflowTemplate{
			hil.BuiltInWorkflowTenantID: {
				hil.BuiltInBusinessWorkflowTemplateID: {ID: hil.BuiltInBusinessWorkflowTemplateID, TenantID: hil.BuiltInWorkflowTenantID, Name: "Business approval"},
				"system-private-template":             {ID: "system-private-template", TenantID: hil.BuiltInWorkflowTenantID, Name: "Private system workflow"},
			},
		},
	}
	handler := newTestRouter(RouterOptions{HIL: service})
	request := func(path string) *http.Request {
		req := authedRequest(http.MethodGet, path, "")
		req.Header.Set("X-Tenant-ID", "tenant-a")
		req.Header.Set("X-Permission-Scope", "approvals.read")
		return req
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("/api/hil/workflow-templates"))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}
	var page struct {
		Items []hil.WorkflowTemplateSummary `json:"items"`
		Total int                           `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Items) != 2 || page.Items[0].ID != "tenant-template" || page.Items[1].ID != hil.BuiltInBusinessWorkflowTemplateID {
		t.Fatalf("inherited templates=%+v total=%d", page.Items, page.Total)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, request("/api/hil/workflow-templates/"+hil.BuiltInBusinessWorkflowTemplateID))
	if rec.Code != http.StatusOK {
		t.Fatalf("built-in detail status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(service.getTemplateTenants) < 2 || service.getTemplateTenants[len(service.getTemplateTenants)-2] != "tenant-a" || service.getTemplateTenants[len(service.getTemplateTenants)-1] != hil.BuiltInWorkflowTenantID {
		t.Fatalf("built-in detail tenants=%v", service.getTemplateTenants)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, request("/api/hil/workflow-templates/system-private-template"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("private system detail status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHTTPHILWorkflowInstanceReadUsesSystemFallbackAndVisibility(t *testing.T) {
	service := &fakeHILWorkflowService{detail: hil.WorkflowInstanceDetail{
		Case:     hil.WorkflowCase{Requester: hil.ActorRef{Kind: hil.ActorAgent, ID: "agent-1"}},
		Instance: hil.WorkflowInstance{ID: "workflow-1", TenantID: "system"},
	}}
	handler := newTestRouter(RouterOptions{HIL: service})
	req := authedRequest(http.MethodGet, "/api/hil/workflow-instances/workflow-1", "")
	req.Header.Del("X-Tenant-ID")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || service.getInstanceTenant != "system" || service.getInstanceID != "workflow-1" {
		t.Fatalf("visible status=%d tenant=%q id=%q body=%s", rec.Code, service.getInstanceTenant, service.getInstanceID, rec.Body.String())
	}

	service.detail.Case.Requester = hil.ActorRef{Kind: hil.ActorAgent, ID: "other-agent"}
	req = authedRequest(http.MethodGet, "/api/hil/workflow-instances/workflow-1", "")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("hidden status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHTTPHILWorkflowTransferAndReassignScopes(t *testing.T) {
	service := &fakeHILWorkflowService{detail: hil.WorkflowInstanceDetail{Instance: hil.WorkflowInstance{ID: "workflow-1"}}}
	handler := newTestRouter(RouterOptions{HIL: service})
	transfer := authedRequest(http.MethodPost, "/api/hil/workflow-tasks/task-1/transfer", `{"new_assignee":{"kind":"human","id":"human-2"},"reason":"channel owner","expected_instance_version":2}`)
	transfer.Header.Set("X-User-ID", "operator")
	transfer.Header.Set("X-Viewer-Kind", "user")
	transfer.Header.Set("X-Agent-Profile-ID", "human-1")
	transfer.Header.Set("Idempotency-Key", "transfer-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, transfer)
	if rec.Code != http.StatusOK || service.transferActor.ID != "human-1" || service.transferInput.NewAssignee.ID != "human-2" || service.transferInput.IdempotencyKey != "transfer-1" {
		t.Fatalf("transfer status=%d actor=%+v input=%+v body=%s", rec.Code, service.transferActor, service.transferInput, rec.Body.String())
	}

	reassign := authedRequest(http.MethodPost, "/api/hil/workflow-tasks/task-1/reassign", `{"new_assignee":{"kind":"human","id":"human-3"},"reason":"approver unavailable","expected_instance_version":3}`)
	reassign.Header.Set("X-User-ID", "admin")
	reassign.Header.Set("X-Viewer-Kind", "user")
	reassign.Header.Set("X-Agent-Profile-ID", "approval-admin")
	reassign.Header.Set("Idempotency-Key", "reassign-1")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, reassign)
	if rec.Code != http.StatusForbidden || service.reassignCalls != 0 {
		t.Fatalf("reassign without manage status=%d calls=%d body=%s", rec.Code, service.reassignCalls, rec.Body.String())
	}
	reassign.Header.Set("X-Permission-Scope", "approvals.manage")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, reassign)
	if rec.Code != http.StatusOK || service.reassignCalls != 1 || service.reassignActor.ID != "approval-admin" || service.reassignInput.NewAssignee.ID != "human-3" {
		t.Fatalf("reassign status=%d calls=%d actor=%+v input=%+v body=%s", rec.Code, service.reassignCalls, service.reassignActor, service.reassignInput, rec.Body.String())
	}
}

func TestHTTPHILWorkflowTemplateAndInstanceLifecycleRoutes(t *testing.T) {
	service := &fakeHILWorkflowService{
		template: hil.WorkflowTemplate{
			ID: "template-1", TenantID: "tenant-a", Name: "Marketing approval", Status: hil.WorkflowTemplateActive,
			LatestPublishedVersion: 1, DraftDefinition: testHTTPWorkflowDefinition(),
		},
		detail: hil.WorkflowInstanceDetail{
			Case:     hil.WorkflowCase{Requester: hil.ActorRef{Kind: hil.ActorHuman, ID: "profile-operator"}},
			Instance: hil.WorkflowInstance{ID: "workflow-1", TenantID: "tenant-a"},
		},
	}
	handler := newTestRouter(RouterOptions{HIL: service})
	request := func(method, path, body, key string) *http.Request {
		req := authedRequest(method, path, body)
		req.Header.Set("X-User-ID", "operator")
		req.Header.Set("X-Viewer-Kind", "user")
		req.Header.Set("X-Agent-Profile-ID", "profile-operator")
		req.Header.Set("X-Tenant-ID", "tenant-a")
		req.Header.Set("X-Permission-Scope", "approvals.read,approvals.decide,approvals.manage")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		return req
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request(http.MethodGet, "/api/hil/workflow-templates/template-1", "", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("get template status=%d body=%s", rec.Code, rec.Body.String())
	}

	draftBody := `{"name":"Marketing approval v2","description":"Updated","definition":{"risk_levels":["high"],"stages":[{"key":"review","name":"Review","order":1,"type":"approval","approval":{"strategy":"single","assignees":[{"kind":"explicit_human","id":"human-ding"}],"deadline_minutes":90}}]}}`
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, request(http.MethodPatch, "/api/hil/workflow-templates/template-1/draft", draftBody, "draft-2"))
	if rec.Code != http.StatusOK || service.saveTemplateInput.ID != "template-1" || service.saveTemplateInput.Status != hil.WorkflowTemplateActive || service.saveTemplateInput.LatestPublishedVersion != 1 {
		t.Fatalf("patch template status=%d input=%+v body=%s", rec.Code, service.saveTemplateInput, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, request(http.MethodPost, "/api/hil/workflow-templates/template-1/publish", `{"bindings":[{"action_type_id":"tool_use","tool_name":"mcp_marketing_marketing_wechat_draft_create","risk_level":"high","active":true}]}`, "publish-2"))
	if rec.Code != http.StatusOK || service.publishTenant != "tenant-a" || service.publishTemplateID != "template-1" || service.publishIdempotencyKey != "publish-2" || len(service.publishBindings) != 1 || service.publishBindings[0].ToolName != "mcp_marketing_marketing_wechat_draft_create" {
		t.Fatalf("publish status=%d tenant=%q template=%q bindings=%+v body=%s", rec.Code, service.publishTenant, service.publishTemplateID, service.publishBindings, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, request(http.MethodPut, "/api/hil/workflow-template-bindings/tool_use", `{"template_id":"template-1","tool_name":"mcp_marketing_marketing_wechat_draft_create","risk_level":"high","active":true,"reason":"bind marketing"}`, "binding-1"))
	if rec.Code != http.StatusOK || service.bindingInput.Binding.ActionTypeID != "tool_use" || service.bindingInput.Binding.TenantID != "tenant-a" {
		t.Fatalf("binding status=%d input=%+v body=%s", rec.Code, service.bindingInput, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, request(http.MethodGet, "/api/hil/workflow-instances?view=my_tasks&status=running", "", ""))
	if rec.Code != http.StatusOK || service.listInstancesQuery.ActionableBy.ID != "profile-operator" || service.listInstancesQuery.Status != hil.WorkflowRunning {
		t.Fatalf("list instances status=%d query=%+v body=%s", rec.Code, service.listInstancesQuery, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, request(http.MethodPost, "/api/hil/workflow-instances/workflow-1/viewed", `{}`, "view-1"))
	if rec.Code != http.StatusNoContent || service.viewedInstanceID != "workflow-1" || service.viewedActor.ID != "profile-operator" {
		t.Fatalf("viewed status=%d instance=%q actor=%+v body=%s", rec.Code, service.viewedInstanceID, service.viewedActor, rec.Body.String())
	}

	expiresAt := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, request(http.MethodPost, "/api/hil/workflow-instances/workflow-1/resubmit", `{"args_hash":"sha256:new-args","material_hash":"sha256:new-material","payload_preview":{"title":"Revised"},"expires_at":"2026-08-17T12:00:00Z","expected_instance_version":2}`, "resubmit-1"))
	if rec.Code != http.StatusOK || service.resubmitInstanceID != "workflow-1" || service.resubmitActor.ID != "profile-operator" || service.resubmitInput.ExpiresAt != expiresAt {
		t.Fatalf("resubmit status=%d instance=%q actor=%+v input=%+v body=%s", rec.Code, service.resubmitInstanceID, service.resubmitActor, service.resubmitInput, rec.Body.String())
	}
}

func testHTTPWorkflowDefinition() hil.WorkflowDefinition {
	return hil.WorkflowDefinition{RiskLevels: []hil.RiskLevel{hil.RiskHigh}, Stages: []hil.WorkflowStageDefinition{{
		Key: "review", Name: "Review", Order: 1, Type: hil.WorkflowStageApproval,
		Approval: &hil.ApprovalStagePolicy{Strategy: hil.ApprovalSingle, Assignees: []hil.WorkflowActorSelector{{Kind: hil.WorkflowSelectorHuman, ID: "human-ding"}}, DeadlineMinutes: 60},
	}}}
}

type fakeHILWorkflowService struct {
	fakeHILService
	detail                hil.WorkflowInstanceDetail
	template              hil.WorkflowTemplate
	decisionTenant        string
	decisionTaskID        string
	decisionActor         hil.ActorRef
	decisionInput         hil.DecideWorkflowTaskInput
	saveTemplateActor     hil.ActorRef
	saveTemplateInput     hil.SaveTemplateDraftInput
	saveTemplateCalls     int
	getInstanceTenant     string
	getInstanceID         string
	transferActor         hil.ActorRef
	transferInput         hil.TransferWorkflowTaskInput
	reassignActor         hil.ActorRef
	reassignInput         hil.ReassignWorkflowTaskInput
	reassignCalls         int
	publishTenant         string
	publishTemplateID     string
	publishBindings       []hil.WorkflowTemplateBinding
	publishIdempotencyKey string
	bindingInput          hil.PutTemplateBindingInput
	templatesByTenant     map[string][]hil.WorkflowTemplateSummary
	templateByTenant      map[string]map[string]hil.WorkflowTemplate
	getTemplateTenants    []string
	listInstancesQuery    hil.WorkflowListQuery
	viewedInstanceID      string
	viewedActor           hil.ActorRef
	resubmitInstanceID    string
	resubmitActor         hil.ActorRef
	resubmitInput         hil.ResubmitWorkflowInput
}

func (f *fakeHILWorkflowService) DecideWorkflowTask(_ context.Context, tenantID, taskID string, actor hil.ActorRef, input hil.DecideWorkflowTaskInput) (hil.WorkflowInstanceDetail, error) {
	f.decisionTenant, f.decisionTaskID, f.decisionActor, f.decisionInput = tenantID, taskID, actor, input
	return f.detail, nil
}

func (f *fakeHILWorkflowService) SaveWorkflowTemplateDraft(_ context.Context, actor hil.ActorRef, input hil.SaveTemplateDraftInput) (hil.WorkflowTemplate, error) {
	f.saveTemplateCalls++
	f.saveTemplateActor, f.saveTemplateInput = actor, input
	return f.template, nil
}

func (f *fakeHILWorkflowService) ListWorkflowTemplates(_ context.Context, query hil.WorkflowTemplateListQuery) ([]hil.WorkflowTemplateSummary, error) {
	if f.templatesByTenant != nil {
		return append([]hil.WorkflowTemplateSummary(nil), f.templatesByTenant[query.TenantID]...), nil
	}
	return nil, nil
}

func (f *fakeHILWorkflowService) GetWorkflowTemplate(_ context.Context, tenantID, templateID string) (hil.WorkflowTemplate, error) {
	f.getTemplateTenants = append(f.getTemplateTenants, tenantID)
	if f.templateByTenant != nil {
		if template, ok := f.templateByTenant[tenantID][templateID]; ok {
			return template, nil
		}
		return hil.WorkflowTemplate{}, hil.ErrNotFound
	}
	return f.template, nil
}

func (f *fakeHILWorkflowService) PublishWorkflowTemplate(_ context.Context, _ hil.ActorRef, tenantID, templateID string, bindings []hil.WorkflowTemplateBinding, idempotencyKey string) (hil.WorkflowTemplateVersion, error) {
	f.publishTenant, f.publishTemplateID = tenantID, templateID
	f.publishBindings = append([]hil.WorkflowTemplateBinding(nil), bindings...)
	f.publishIdempotencyKey = idempotencyKey
	return hil.WorkflowTemplateVersion{ID: "version-2", TemplateID: templateID, Version: 2}, nil
}

func (f *fakeHILWorkflowService) DisableWorkflowTemplate(context.Context, hil.ActorRef, hil.DisableTemplateInput) error {
	return nil
}

func (f *fakeHILWorkflowService) PutWorkflowTemplateBinding(_ context.Context, _ hil.ActorRef, input hil.PutTemplateBindingInput) (hil.WorkflowTemplateBinding, error) {
	f.bindingInput = input
	return input.Binding, nil
}

func (f *fakeHILWorkflowService) ListWorkflowInstances(_ context.Context, query hil.WorkflowListQuery) (hil.WorkflowInstancePage, error) {
	f.listInstancesQuery = query
	return hil.WorkflowInstancePage{Items: []hil.WorkflowInstanceDetail{f.detail}, Total: 1}, nil
}

func (f *fakeHILWorkflowService) GetWorkflowInstance(_ context.Context, tenantID, instanceID string) (hil.WorkflowInstanceDetail, error) {
	f.getInstanceTenant, f.getInstanceID = tenantID, instanceID
	return f.detail, nil
}

func (f *fakeHILWorkflowService) RecordWorkflowViewed(_ context.Context, _ string, instanceID string, actor hil.ActorRef, _ string) error {
	f.viewedInstanceID, f.viewedActor = instanceID, actor
	return nil
}

func (f *fakeHILWorkflowService) TransferWorkflowTask(_ context.Context, _ string, _ string, actor hil.ActorRef, input hil.TransferWorkflowTaskInput) (hil.WorkflowInstanceDetail, error) {
	f.transferActor, f.transferInput = actor, input
	return f.detail, nil
}

func (f *fakeHILWorkflowService) ReassignWorkflowTask(_ context.Context, _ string, _ string, actor hil.ActorRef, input hil.ReassignWorkflowTaskInput) (hil.WorkflowInstanceDetail, error) {
	f.reassignCalls++
	f.reassignActor, f.reassignInput = actor, input
	return f.detail, nil
}

func (f *fakeHILWorkflowService) ResubmitWorkflow(_ context.Context, _ string, instanceID string, actor hil.ActorRef, input hil.ResubmitWorkflowInput) (hil.WorkflowInstanceDetail, error) {
	f.resubmitInstanceID, f.resubmitActor, f.resubmitInput = instanceID, actor, input
	return f.detail, nil
}
