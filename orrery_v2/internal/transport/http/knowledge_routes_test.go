package httptransport

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/knowledgeadmin"
	doccore "orrery_v2/internal/platform/document/core"
)

func TestHTTPRoutesKnowledgeCRUDAndDocuments(t *testing.T) {
	service := &fakeKnowledgeService{
		base:  knowledgeadmin.KnowledgeBaseDTO{ID: "kb-1", Name: "Product", Status: "active"},
		bases: knowledgeadmin.KnowledgeBasePageDTO{Items: []knowledgeadmin.KnowledgeBaseDTO{{ID: "kb-1", Name: "Product"}}, Total: 1},
		doc:   knowledgeadmin.KnowledgeDocumentDTO{ID: "doc-1", KnowledgeBaseID: "kb-1", Filename: "notes.md", Status: "uploaded", JobID: "job-1"},
		docs:  knowledgeadmin.KnowledgeDocumentPageDTO{Items: []knowledgeadmin.KnowledgeDocumentDTO{{ID: "doc-1", Filename: "notes.md"}}, Total: 1},
		processingResult: knowledgeadmin.KnowledgeDocumentProcessingResultDTO{
			Document: knowledgeadmin.KnowledgeDocumentDTO{ID: "doc-1", KnowledgeBaseID: "kb-1", Filename: "notes.md", Status: "indexed"},
			Status:   "indexed",
			Progress: 100,
			Stage:    "indexed",
			Pages:    []map[string]any{{"page": float64(1), "text": "release process"}},
		},
		job:     knowledgeadmin.KnowledgeJobDTO{ID: "job-1", KnowledgeBaseID: "kb-1", Topic: "knowledge.ingest_document", Status: "completed"},
		jobs:    knowledgeadmin.KnowledgeJobPageDTO{Items: []knowledgeadmin.KnowledgeJobDTO{{ID: "job-1", KnowledgeBaseID: "kb-1", Topic: "knowledge.ingest_document", Status: "completed"}}, Total: 1},
		binding: knowledgeadmin.KnowledgeBindingDTO{ID: "bind-1", KnowledgeBaseID: "kb-1", BoundToID: "agent-pm"},
		bindings: knowledgeadmin.KnowledgeBindingPageDTO{Items: []knowledgeadmin.KnowledgeBindingDTO{{
			ID:              "bind-1",
			KnowledgeBaseID: "kb-1",
			BoundToType:     "agent",
			BoundToID:       "agent-pm",
			Access:          "read",
		}}, Total: 1},
	}
	handler := newTestRouter(RouterOptions{KnowledgeQuery: service, KnowledgeSearch: service, KnowledgeCommand: service})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authedKnowledgeRequest(http.MethodGet, "/api/knowledge-bases?owner_type=organization&owner_id=org-1", ""))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"kb-1"`) {
		t.Fatalf("list bases status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.listQuery.OwnerType != "organization" || service.listQuery.OwnerID != "org-1" {
		t.Fatalf("list query = %+v", service.listQuery)
	}

	create := authedKnowledgeRequest(http.MethodPost, "/api/knowledge-bases", `{"name":"Product","owner_type":"organization","owner_id":"org-1","visibility":"scoped","chunk_policy":{"max_runes":100,"overlap_runes":10}}`)
	create.Header.Set("X-Permission-Scope", knowledgeadmin.PermissionKnowledgeManage)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, create)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create base status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.createBase.Name != "Product" || service.createBase.ChunkPolicy.MaxRunes != 100 {
		t.Fatalf("create command = %+v", service.createBase)
	}

	patch := authedKnowledgeRequest(http.MethodPatch, "/api/knowledge-bases/kb-1", `{"name":"Updated","status":"disabled"}`)
	patch.Header.Set("X-Permission-Scope", knowledgeadmin.PermissionKnowledgeManage)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, patch)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch base status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.updateBase.ID != "kb-1" || service.updateBase.Name != "Updated" || service.updateBase.Status != "disabled" {
		t.Fatalf("update command = %+v", service.updateBase)
	}
	if !hasString(service.updateBase.UpdateMask, "name") || !hasString(service.updateBase.UpdateMask, "status") || hasString(service.updateBase.UpdateMask, "description") {
		t.Fatalf("update mask = %+v", service.updateBase.UpdateMask)
	}

	bind := authedKnowledgeRequest(http.MethodPost, "/api/knowledge-bases/kb-1/bindings", `{"bound_to_type":"agent","bound_to_id":"agent-pm","access":"read"}`)
	bind.Header.Set("X-Permission-Scope", knowledgeadmin.PermissionKnowledgeManage)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, bind)
	if rec.Code != http.StatusCreated {
		t.Fatalf("bind status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.bind.KnowledgeBaseID != "kb-1" || service.bind.BoundToID != "agent-pm" {
		t.Fatalf("bind command = %+v", service.bind)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, authedKnowledgeRequest(http.MethodGet, "/api/knowledge-bases/kb-1/bindings", ""))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"agent-pm"`) {
		t.Fatalf("list bindings status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.listBindingsBaseID != "kb-1" {
		t.Fatalf("list bindings base id = %q", service.listBindingsBaseID)
	}

	docReq := authedKnowledgeRequest(http.MethodPost, "/api/knowledge-bases/kb-1/documents", `{"artifact_id":"artifact-1","filename":"notes.md","content_type":"text/markdown","size_bytes":42,"checksum":"sum"}`)
	docReq.Header.Set("X-Permission-Scope", knowledgeadmin.PermissionKnowledgeWrite)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, docReq)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("document status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.createDoc.KnowledgeBaseID != "kb-1" || service.createDoc.Filename != "notes.md" {
		t.Fatalf("doc command = %+v", service.createDoc)
	}

	var upload bytes.Buffer
	writer := multipart.NewWriter(&upload)
	file, err := writer.CreateFormFile("file", "upload.md")
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := file.Write([]byte("# upload")); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	uploadReq := authedKnowledgeRequest(http.MethodPost, "/api/knowledge-bases/kb-1/documents", upload.String())
	uploadReq.Header.Set("Content-Type", writer.FormDataContentType())
	uploadReq.Header.Set("X-Permission-Scope", knowledgeadmin.PermissionKnowledgeWrite)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, uploadReq)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("upload document status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.uploadDoc.KnowledgeBaseID != "kb-1" || service.uploadDoc.Filename != "upload.md" {
		t.Fatalf("upload command = %+v", service.uploadDoc)
	}

	deleteDoc := authedKnowledgeRequest(http.MethodDelete, "/api/knowledge-bases/kb-1/documents/doc-1", "")
	deleteDoc.Header.Set("X-Permission-Scope", knowledgeadmin.PermissionKnowledgeManage)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, deleteDoc)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("delete document status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.deleteDoc.KnowledgeBaseID != "kb-1" || service.deleteDoc.DocumentID != "doc-1" {
		t.Fatalf("delete document command = %+v", service.deleteDoc)
	}

	reindex := authedKnowledgeRequest(http.MethodPost, "/api/knowledge-bases/kb-1/reindex", "")
	reindex.Header.Set("X-Permission-Scope", knowledgeadmin.PermissionKnowledgeManage)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, reindex)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("reindex status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.reindex.KnowledgeBaseID != "kb-1" {
		t.Fatalf("reindex command = %+v", service.reindex)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, authedKnowledgeRequest(http.MethodGet, "/api/knowledge-jobs/job-1", ""))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"completed"`) {
		t.Fatalf("get job status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.getJobID != "job-1" {
		t.Fatalf("get job id = %q", service.getJobID)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, authedKnowledgeRequest(http.MethodGet, "/api/knowledge-bases/kb-1/documents", ""))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"notes.md"`) {
		t.Fatalf("list docs status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, authedKnowledgeRequest(http.MethodGet, "/api/knowledge-bases/kb-1/documents/doc-1", ""))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"doc-1"`) {
		t.Fatalf("get document status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.getDocBaseID != "kb-1" || service.getDocID != "doc-1" {
		t.Fatalf("get document ids = %q %q", service.getDocBaseID, service.getDocID)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, authedKnowledgeRequest(http.MethodGet, "/api/knowledge-bases/kb-1/documents/doc-1/status", ""))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"uploaded"`) {
		t.Fatalf("refresh document status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.refreshDoc.KnowledgeBaseID != "kb-1" || service.refreshDoc.DocumentID != "doc-1" {
		t.Fatalf("refresh document command = %+v", service.refreshDoc)
	}

	reprocessDoc := authedKnowledgeRequest(http.MethodPost, "/api/knowledge-bases/kb-1/documents/doc-1/reprocess", "")
	reprocessDoc.Header.Set("X-Permission-Scope", knowledgeadmin.PermissionKnowledgeManage)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, reprocessDoc)
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"job-1"`) {
		t.Fatalf("reprocess document status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.reprocessDoc.KnowledgeBaseID != "kb-1" || service.reprocessDoc.DocumentID != "doc-1" {
		t.Fatalf("reprocess document command = %+v", service.reprocessDoc)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, authedKnowledgeRequest(http.MethodGet, "/api/knowledge-bases/kb-1/documents/doc-1/result?pages=1", ""))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"release process"`) {
		t.Fatalf("document result status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.processingResultCommand.KnowledgeBaseID != "kb-1" || service.processingResultCommand.DocumentID != "doc-1" || service.processingResultCommand.Pages != "1" {
		t.Fatalf("processing result command = %+v", service.processingResultCommand)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, authedKnowledgeRequest(http.MethodGet, "/api/knowledge-bases/kb-1/jobs", ""))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"job-1"`) {
		t.Fatalf("list jobs status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.listJobsBaseID != "kb-1" {
		t.Fatalf("list jobs base id = %q", service.listJobsBaseID)
	}

	searchReq := authedKnowledgeRequest(http.MethodPost, "/api/knowledge-bases/kb-1/search", `{"query":"mission","limit":3}`)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, searchReq)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"chunk-1"`) {
		t.Fatalf("single base search status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.search.KnowledgeBaseIDs[0] != "kb-1" || service.search.Query != "mission" || service.search.Limit != 3 {
		t.Fatalf("single base search command = %+v", service.search)
	}

	globalSearch := authedKnowledgeRequest(http.MethodPost, "/api/knowledge/search", `{"query":"mission"}`)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, globalSearch)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"chunk-1"`) {
		t.Fatalf("global search status=%d body=%s", rec.Code, rec.Body.String())
	}

	globalSearch = authedKnowledgeRequest(http.MethodPost, "/api/knowledge/search", `{"query":"mission","owner_filter":{"owner_type":"organization","owner_id":"org-2"}}`)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, globalSearch)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner-filtered search status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.search.OwnerFilter.OwnerType != "organization" || service.search.OwnerFilter.OwnerID != "org-2" {
		t.Fatalf("owner-filtered search command = %+v", service.search)
	}
}

func TestHTTPRoutesSyncKnowledgeDocuments(t *testing.T) {
	service := &fakeKnowledgeService{syncResult: knowledgeadmin.CatalogReconcileResult{
		Created: 12, Updated: 3, Unchanged: 40, Removed: 2, Rejected: 1,
	}}
	handler := newTestRouter(RouterOptions{KnowledgeCommand: service})
	request := authedKnowledgeRequest(http.MethodPost, "/api/knowledge-bases/kb-personal/documents/sync", "")
	request.Header.Set("X-Permission-Scope", knowledgeadmin.PermissionKnowledgeManage)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"created":12`) || !strings.Contains(recorder.Body.String(), `"unchanged":40`) {
		t.Fatalf("sync status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.syncBaseID != "kb-personal" {
		t.Fatalf("sync base id = %q", service.syncBaseID)
	}
}

func TestPermissionCapabilitiesIncludeKnowledgeScopes(t *testing.T) {
	capabilities := defaultPermissionCapabilities()
	assertCapability(t, capabilities, http.MethodGet, "/api/knowledge-bases", knowledgeadmin.PermissionKnowledgeRead)
	assertCapability(t, capabilities, http.MethodPost, "/api/knowledge-bases", knowledgeadmin.PermissionKnowledgeManage)
	assertCapability(t, capabilities, http.MethodPatch, "/api/knowledge-bases/{knowledge_base_id}", knowledgeadmin.PermissionKnowledgeManage)
	assertCapability(t, capabilities, http.MethodPost, "/api/knowledge-bases/{knowledge_base_id}/documents", knowledgeadmin.PermissionKnowledgeWrite)
	assertCapability(t, capabilities, http.MethodGet, "/api/knowledge-bases/{knowledge_base_id}/documents/{document_id}/status", knowledgeadmin.PermissionKnowledgeRead)
	assertCapability(t, capabilities, http.MethodPost, "/api/knowledge-bases/{knowledge_base_id}/documents/{document_id}/reprocess", knowledgeadmin.PermissionKnowledgeManage)
	assertCapability(t, capabilities, http.MethodGet, "/api/knowledge-bases/{knowledge_base_id}/documents/{document_id}/result", knowledgeadmin.PermissionKnowledgeRead)
	assertCapability(t, capabilities, http.MethodGet, "/api/knowledge-bases/{knowledge_base_id}/documents/{document_id}/relations", knowledgeadmin.PermissionKnowledgeRead)
}

func TestKnowledgeViewerFromRequestCapturesIdentityScopes(t *testing.T) {
	req := authedKnowledgeRequest(http.MethodGet, "/api/knowledge-bases", "")
	req.Header.Set("X-Agent-ID", "agent-1")
	req.Header.Set("X-Agent-IDs", "agent-2, agent-3")
	req.Header.Set("X-Human-ID", "human-1")
	req.Header.Set("X-Human-IDs", "human-2")
	req.Header.Set("X-Organization-ID", "org-1")
	req.Header.Set("X-Organization-IDs", "org-2, org-3")
	req.Header.Set("X-Position-ID", "position-1")
	req.Header.Set("X-Role-IDs", "role-1, role-2")

	viewer := knowledgeViewerFromRequest(req)
	if !hasString(viewer.AgentIDs, "agent-1") || !hasString(viewer.AgentIDs, "agent-2") || !hasString(viewer.AgentIDs, "agent-3") {
		t.Fatalf("agent identities = %+v", viewer.AgentIDs)
	}
	if !hasString(viewer.HumanIDs, "human-1") || !hasString(viewer.HumanIDs, "human-2") {
		t.Fatalf("human identities = %+v", viewer.HumanIDs)
	}
	if !hasString(viewer.OrganizationIDs, "org-1") || !hasString(viewer.OrganizationIDs, "org-2") || !hasString(viewer.OrganizationIDs, "org-3") {
		t.Fatalf("organization identities = %+v", viewer.OrganizationIDs)
	}
	if !hasString(viewer.PositionIDs, "position-1") {
		t.Fatalf("position identities = %+v", viewer.PositionIDs)
	}
	if !hasString(viewer.RoleIDs, "role-1") || !hasString(viewer.RoleIDs, "role-2") {
		t.Fatalf("role identities = %+v", viewer.RoleIDs)
	}
}

func TestKnowledgeViewerFromRequestIgnoresUntrustedIdentityScopes(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/knowledge-bases", nil)
	req.Header.Set("X-User-ID", "operator")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Permission-Scope", knowledgeadmin.PermissionKnowledgeRead)
	req.Header.Set("X-Agent-IDs", "attacker-agent")
	req.Header.Set("X-Human-IDs", "attacker-human")
	req.Header.Set("X-Organization-IDs", "attacker-org")
	req.Header.Set("X-Role-IDs", "attacker-role")

	viewer := knowledgeViewerFromRequest(req)
	if len(viewer.AgentIDs) != 0 || len(viewer.HumanIDs) != 0 || len(viewer.OrganizationIDs) != 0 || len(viewer.RoleIDs) != 0 {
		t.Fatalf("untrusted identity scopes were accepted: %+v", viewer)
	}
}

func TestKnowledgeViewerFromRequestExpandsAgentProfileIdentity(t *testing.T) {
	req := authedKnowledgeRequest(http.MethodGet, "/api/knowledge-bases", "")
	req.Header.Set("X-Agent-Profile-ID", "agent-pm")
	router := &Router{knowledgeProfiles: fakeKnowledgeProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"agent-pm": {
			ID:        "agent-pm",
			AgentKind: agentcore.AgentKindAI,
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef: agentcore.OrganizationRef{ID: "org-product"},
				DepartmentRef:   agentcore.DepartmentRef{ID: "dept-product"},
				PositionRef:     agentcore.PositionRef{ID: "position-pm"},
				RoleRefs:        []agentcore.RoleRef{{ID: "role-reviewer"}, {ID: "role-writer"}},
			},
		},
	}}}

	viewer := router.knowledgeViewerFromRequest(req)
	if !hasString(viewer.AgentIDs, "agent-pm") {
		t.Fatalf("agent identities = %+v", viewer.AgentIDs)
	}
	if !hasString(viewer.OrganizationIDs, "org-product") {
		t.Fatalf("organization identities = %+v", viewer.OrganizationIDs)
	}
	if !hasString(viewer.PositionIDs, "position-pm") {
		t.Fatalf("position identities = %+v", viewer.PositionIDs)
	}
	if !hasString(viewer.RoleIDs, "role-reviewer") || !hasString(viewer.RoleIDs, "role-writer") {
		t.Fatalf("role identities = %+v", viewer.RoleIDs)
	}
}

func TestKnowledgeViewerFromRequestExpandsHumanProfileIdentity(t *testing.T) {
	req := authedKnowledgeRequest(http.MethodGet, "/api/knowledge-bases", "")
	req.Header.Set("X-Agent-Profile-ID", "human-superadmin")
	router := &Router{knowledgeProfiles: fakeKnowledgeProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"human-superadmin": {
			ID:        "human-superadmin",
			AgentKind: agentcore.AgentKindHuman,
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef: agentcore.OrganizationRef{ID: "org-system"},
				PositionRef:     agentcore.PositionRef{ID: "position-superadmin"},
				RoleRefs:        []agentcore.RoleRef{{ID: "superadmin"}},
			},
		},
	}}}

	viewer := router.knowledgeViewerFromRequest(req)
	if !hasString(viewer.HumanIDs, "human-superadmin") {
		t.Fatalf("human identities = %+v", viewer.HumanIDs)
	}
	if !hasString(viewer.RoleIDs, "superadmin") {
		t.Fatalf("role identities = %+v", viewer.RoleIDs)
	}
}

func authedKnowledgeRequest(method, path, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("X-User-ID", "operator")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Permission-Scope", knowledgeadmin.PermissionKnowledgeRead)
	authorizeInternalViewerTestRequest(req)
	return req
}

func hasString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type fakeKnowledgeService struct {
	base                    knowledgeadmin.KnowledgeBaseDTO
	bases                   knowledgeadmin.KnowledgeBasePageDTO
	doc                     knowledgeadmin.KnowledgeDocumentDTO
	docs                    knowledgeadmin.KnowledgeDocumentPageDTO
	processingResult        knowledgeadmin.KnowledgeDocumentProcessingResultDTO
	job                     knowledgeadmin.KnowledgeJobDTO
	jobs                    knowledgeadmin.KnowledgeJobPageDTO
	searchPage              knowledgeadmin.KnowledgeSearchPageDTO
	binding                 knowledgeadmin.KnowledgeBindingDTO
	bindings                knowledgeadmin.KnowledgeBindingPageDTO
	listQuery               knowledgeadmin.ListKnowledgeBasesQuery
	createBase              knowledgeadmin.CreateKnowledgeBaseCommand
	updateBase              knowledgeadmin.UpdateKnowledgeBaseCommand
	bind                    knowledgeadmin.BindKnowledgeBaseCommand
	createDoc               knowledgeadmin.CreateDocumentCommand
	uploadDoc               knowledgeadmin.UploadDocumentCommand
	deleteDoc               knowledgeadmin.DeleteDocumentCommand
	refreshDoc              knowledgeadmin.DocumentActionCommand
	reprocessDoc            knowledgeadmin.DocumentActionCommand
	processingResultCommand knowledgeadmin.DocumentProcessingResultCommand
	reindex                 knowledgeadmin.ReindexKnowledgeBaseCommand
	search                  knowledgeadmin.SearchKnowledgeCommand
	getJobID                string
	getDocBaseID            string
	getDocID                string
	listJobsBaseID          string
	listBindingsBaseID      string
	syncResult              knowledgeadmin.CatalogReconcileResult
	syncBaseID              string
}

type fakeKnowledgeProfileStore struct {
	profiles map[agentcore.AgentProfileID]agentcore.AgentProfile
}

func (s fakeKnowledgeProfileStore) GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	profile, ok := s.profiles[id]
	if !ok {
		return agentcore.AgentProfile{}, fmt.Errorf("profile %s not found", id)
	}
	return profile, nil
}

func (f *fakeKnowledgeService) ListKnowledgeBases(ctx context.Context, viewer knowledgeadmin.ViewerRef, query knowledgeadmin.ListKnowledgeBasesQuery) (knowledgeadmin.KnowledgeBasePageDTO, error) {
	f.listQuery = query
	return f.bases, nil
}

func (f *fakeKnowledgeService) GetKnowledgeBase(ctx context.Context, viewer knowledgeadmin.ViewerRef, id string) (knowledgeadmin.KnowledgeBaseDTO, error) {
	return f.base, nil
}

func (f *fakeKnowledgeService) ListDocuments(ctx context.Context, viewer knowledgeadmin.ViewerRef, knowledgeBaseID string) (knowledgeadmin.KnowledgeDocumentPageDTO, error) {
	return f.docs, nil
}

func (f *fakeKnowledgeService) GetDocument(ctx context.Context, viewer knowledgeadmin.ViewerRef, knowledgeBaseID string, documentID string) (knowledgeadmin.KnowledgeDocumentDTO, error) {
	f.getDocBaseID = knowledgeBaseID
	f.getDocID = documentID
	return f.doc, nil
}

func (f *fakeKnowledgeService) ListBindings(ctx context.Context, viewer knowledgeadmin.ViewerRef, knowledgeBaseID string) (knowledgeadmin.KnowledgeBindingPageDTO, error) {
	f.listBindingsBaseID = knowledgeBaseID
	return f.bindings, nil
}

func (f *fakeKnowledgeService) ListJobs(ctx context.Context, viewer knowledgeadmin.ViewerRef, knowledgeBaseID string) (knowledgeadmin.KnowledgeJobPageDTO, error) {
	f.listJobsBaseID = knowledgeBaseID
	return f.jobs, nil
}

func (f *fakeKnowledgeService) GetKnowledgeJob(ctx context.Context, viewer knowledgeadmin.ViewerRef, id string) (knowledgeadmin.KnowledgeJobDTO, error) {
	f.getJobID = id
	return f.job, nil
}

func (f *fakeKnowledgeService) SearchKnowledge(ctx context.Context, viewer knowledgeadmin.ViewerRef, cmd knowledgeadmin.SearchKnowledgeCommand) (knowledgeadmin.KnowledgeSearchPageDTO, error) {
	f.search = cmd
	if len(f.searchPage.Results) == 0 {
		return knowledgeadmin.KnowledgeSearchPageDTO{Results: []knowledgeadmin.KnowledgeSearchResultDTO{{
			KnowledgeBaseID: "kb-1",
			DocumentID:      "doc-1",
			ChunkID:         "chunk-1",
			Snippet:         "mission",
			DocumentName:    "notes.md",
		}}}, nil
	}
	return f.searchPage, nil
}

func (f *fakeKnowledgeService) CreateKnowledgeBase(ctx context.Context, viewer knowledgeadmin.ViewerRef, cmd knowledgeadmin.CreateKnowledgeBaseCommand) (knowledgeadmin.KnowledgeBaseDTO, error) {
	f.createBase = cmd
	return f.base, nil
}

func (f *fakeKnowledgeService) UpdateKnowledgeBase(ctx context.Context, viewer knowledgeadmin.ViewerRef, cmd knowledgeadmin.UpdateKnowledgeBaseCommand) (knowledgeadmin.KnowledgeBaseDTO, error) {
	f.updateBase = cmd
	return f.base, nil
}

func (f *fakeKnowledgeService) DeleteKnowledgeBase(ctx context.Context, viewer knowledgeadmin.ViewerRef, id string) error {
	return nil
}

func (f *fakeKnowledgeService) BindKnowledgeBase(ctx context.Context, viewer knowledgeadmin.ViewerRef, cmd knowledgeadmin.BindKnowledgeBaseCommand) (knowledgeadmin.KnowledgeBindingDTO, error) {
	f.bind = cmd
	return f.binding, nil
}

func (f *fakeKnowledgeService) UnbindKnowledgeBase(ctx context.Context, viewer knowledgeadmin.ViewerRef, bindingID string) error {
	return nil
}

func (f *fakeKnowledgeService) CreateDocumentMetadata(ctx context.Context, viewer knowledgeadmin.ViewerRef, cmd knowledgeadmin.CreateDocumentCommand) (knowledgeadmin.KnowledgeDocumentDTO, error) {
	f.createDoc = cmd
	return f.doc, nil
}

func (f *fakeKnowledgeService) UploadDocument(ctx context.Context, viewer knowledgeadmin.ViewerRef, cmd knowledgeadmin.UploadDocumentCommand) (knowledgeadmin.KnowledgeDocumentDTO, error) {
	f.uploadDoc = cmd
	return f.doc, nil
}

func (f *fakeKnowledgeService) DeleteDocument(ctx context.Context, viewer knowledgeadmin.ViewerRef, cmd knowledgeadmin.DeleteDocumentCommand) error {
	f.deleteDoc = cmd
	return nil
}

func (f *fakeKnowledgeService) RefreshDocumentStatus(ctx context.Context, viewer knowledgeadmin.ViewerRef, cmd knowledgeadmin.DocumentActionCommand) (knowledgeadmin.KnowledgeDocumentDTO, error) {
	f.refreshDoc = cmd
	return f.doc, nil
}

func (f *fakeKnowledgeService) ReprocessDocument(ctx context.Context, viewer knowledgeadmin.ViewerRef, cmd knowledgeadmin.DocumentActionCommand) (knowledgeadmin.KnowledgeDocumentDTO, error) {
	f.reprocessDoc = cmd
	return f.doc, nil
}

func (f *fakeKnowledgeService) GetDocumentProcessingResult(ctx context.Context, viewer knowledgeadmin.ViewerRef, cmd knowledgeadmin.DocumentProcessingResultCommand) (knowledgeadmin.KnowledgeDocumentProcessingResultDTO, error) {
	f.processingResultCommand = cmd
	return f.processingResult, nil
}

func (f *fakeKnowledgeService) CreateReindexJob(ctx context.Context, viewer knowledgeadmin.ViewerRef, cmd knowledgeadmin.ReindexKnowledgeBaseCommand) (knowledgeadmin.KnowledgeJobDTO, error) {
	f.reindex = cmd
	return knowledgeadmin.KnowledgeJobDTO{ID: "job-reindex", KnowledgeBaseID: cmd.KnowledgeBaseID, Topic: "knowledge.reindex_base", Status: "queued"}, nil
}

func (f *fakeKnowledgeService) SyncKnowledgeDocuments(ctx context.Context, viewer knowledgeadmin.ViewerRef, knowledgeBaseID string) (knowledgeadmin.CatalogReconcileResult, error) {
	f.syncBaseID = knowledgeBaseID
	return f.syncResult, nil
}

var _ = doccore.ChunkPolicy{}
