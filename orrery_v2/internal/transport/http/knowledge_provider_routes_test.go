package httptransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/app/knowledgeprovider"
)

type fakeKnowledgeProviderService struct {
	binding knowledgeprovider.Binding
	created knowledgeprovider.Binding
	updated knowledgeprovider.Binding
	syncReq knowledgeprovider.SyncRequest
}

func (f *fakeKnowledgeProviderService) ListBindings(context.Context) ([]knowledgeprovider.Binding, error) {
	return []knowledgeprovider.Binding{f.binding}, nil
}

func (f *fakeKnowledgeProviderService) GetBinding(context.Context, string) (knowledgeprovider.Binding, error) {
	return f.binding, nil
}

func (f *fakeKnowledgeProviderService) CreateBinding(_ context.Context, binding knowledgeprovider.Binding, _ string) (knowledgeprovider.Binding, error) {
	f.created = binding
	f.binding = binding
	return binding, nil
}

func (f *fakeKnowledgeProviderService) UpdateBinding(_ context.Context, binding knowledgeprovider.Binding, _ string) (knowledgeprovider.Binding, error) {
	f.updated = binding
	f.binding = binding
	return binding, nil
}

func (f *fakeKnowledgeProviderService) TestBinding(context.Context, string, string) (knowledgeprovider.CapabilityTestResult, error) {
	return knowledgeprovider.CapabilityTestResult{BindingID: f.binding.ID, Capabilities: knowledgeprovider.DefaultToolMapping()}, nil
}

func (f *fakeKnowledgeProviderService) Sync(_ context.Context, _ string, req knowledgeprovider.SyncRequest) (knowledgeprovider.SyncResult, error) {
	f.syncReq = req
	return knowledgeprovider.SyncResult{FoldersSynced: 7, DocumentsSynced: 131, CompletedAt: time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)}, nil
}

func TestKnowledgeProviderRoutesLifecycle(t *testing.T) {
	service := &fakeKnowledgeProviderService{binding: knowledgeprovider.Binding{
		ID: "binding-1", Name: "Minvia PageIndex", MCPServerID: "mcp.pageindex", Mode: knowledgeprovider.ModeReadOnly, Status: knowledgeprovider.StatusActive,
	}}
	handler := newTestRouter(RouterOptions{KnowledgeProviders: service})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authedKnowledgeRequest(http.MethodGet, "/api/knowledge-providers", ""))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"binding-1"`) {
		t.Fatalf("list providers status=%d body=%s", rec.Code, rec.Body.String())
	}

	create := authedKnowledgeRequest(http.MethodPost, "/api/knowledge-providers", `{"id":"binding-2","name":"PageIndex","mcp_server_id":"mcp.pageindex","mode":"read_only","folder_exclude":["MinviaBrain"]}`)
	create.Header.Set("X-Permission-Scope", knowledgeadmin.PermissionKnowledgeManage)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, create)
	if rec.Code != http.StatusCreated || service.created.MCPServerID != "mcp.pageindex" || len(service.created.FolderExclude) != 1 {
		t.Fatalf("create provider status=%d body=%s binding=%+v", rec.Code, rec.Body.String(), service.created)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, authedKnowledgeRequest(http.MethodGet, "/api/knowledge-providers/binding-2", ""))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"mcp.pageindex"`) {
		t.Fatalf("get provider status=%d body=%s", rec.Code, rec.Body.String())
	}

	patch := authedKnowledgeRequest(http.MethodPatch, "/api/knowledge-providers/binding-2", `{"mode":"read_write","folder_include":["minvia-core"]}`)
	patch.Header.Set("X-Permission-Scope", knowledgeadmin.PermissionKnowledgeManage)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, patch)
	if rec.Code != http.StatusOK || service.updated.Mode != knowledgeprovider.ModeReadWrite || service.updated.Name != "PageIndex" {
		t.Fatalf("patch provider status=%d body=%s binding=%+v", rec.Code, rec.Body.String(), service.updated)
	}

	testReq := authedKnowledgeRequest(http.MethodPost, "/api/knowledge-providers/binding-2/test", "")
	testReq.Header.Set("X-Permission-Scope", knowledgeadmin.PermissionKnowledgeManage)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, testReq)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"list_folders"`) {
		t.Fatalf("test provider status=%d body=%s", rec.Code, rec.Body.String())
	}

	syncReq := authedKnowledgeRequest(http.MethodPost, "/api/knowledge-providers/binding-2/sync", `{"owner_id":"minvia"}`)
	syncReq.Header.Set("X-Permission-Scope", knowledgeadmin.PermissionKnowledgeManage)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, syncReq)
	if rec.Code != http.StatusOK || service.syncReq.OwnerID != "minvia" || service.syncReq.PrincipalID != "operator" || !strings.Contains(rec.Body.String(), `"documents_synced":131`) {
		t.Fatalf("sync provider status=%d body=%s request=%+v", rec.Code, rec.Body.String(), service.syncReq)
	}
}

func TestKnowledgeProviderMutationRequiresManagePermission(t *testing.T) {
	handler := newTestRouter(RouterOptions{KnowledgeProviders: &fakeKnowledgeProviderService{}})
	req := authedKnowledgeRequest(http.MethodPost, "/api/knowledge-providers", `{"id":"binding-1"}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden, got status=%d body=%s", rec.Code, rec.Body.String())
	}
}
