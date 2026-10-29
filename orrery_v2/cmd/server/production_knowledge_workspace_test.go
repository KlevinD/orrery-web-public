package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/app/knowledgeontology"
	knowledgecore "orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/provider"
	"orrery_v2/internal/knowledge/testkit"
	doccore "orrery_v2/internal/platform/document/core"
	httptransport "orrery_v2/internal/transport/http"
)

type workspaceRevisionRecorder struct {
	provider.Provider
	provider.RevisionProvider
	updates   []provider.UpdateDocumentContentRequest
	publishes []provider.PublishDocumentRevisionRequest
	conflict  bool
}

func (*workspaceRevisionRecorder) Name() knowledgecore.KnowledgeProvider {
	return knowledgecore.ProviderPageIndex
}

func (*workspaceRevisionRecorder) GetSemanticSource(_ context.Context, req provider.SemanticSourceRequest) (provider.SemanticSource, error) {
	return provider.SemanticSource{DocumentID: req.DocumentID, Revision: 2, Content: "# Current", Editable: true}, nil
}

func (p *workspaceRevisionRecorder) UpdateDocumentContent(_ context.Context, req provider.UpdateDocumentContentRequest) (provider.DocumentRevision, error) {
	p.updates = append(p.updates, req)
	if p.conflict {
		return provider.DocumentRevision{}, knowledgecore.ErrConflict
	}
	return provider.DocumentRevision{DocumentID: req.DocumentID, Revision: req.ExpectedRevision + 1, PublicationStatus: "draft"}, nil
}

func (p *workspaceRevisionRecorder) PublishDocumentRevision(_ context.Context, req provider.PublishDocumentRevisionRequest) (provider.DocumentRevision, error) {
	p.publishes = append(p.publishes, req)
	return provider.DocumentRevision{DocumentID: req.DocumentID, Revision: req.Revision, PublicationStatus: "published"}, nil
}

func TestProductionKnowledgeWorkspaceHTTPEnforcesBaseWriteAccess(t *testing.T) {
	for _, scenario := range []struct {
		name, organization, agent, scope string
		visibility                       knowledgecore.Visibility
		binding                          knowledgecore.AccessLevel
		status                           int
	}{
		{"owner", "org-owner", "", "knowledge.read,knowledge.write", knowledgecore.VisibilityPrivate, "", http.StatusOK},
		{"write-binding", "org-other", "writer", "knowledge.read,knowledge.write", knowledgecore.VisibilityPrivate, knowledgecore.AccessWrite, http.StatusOK},
		{"manage-binding", "org-other", "writer", "knowledge.read,knowledge.write,knowledge.manage", knowledgecore.VisibilityPrivate, knowledgecore.AccessManage, http.StatusOK},
		{"read-binding-global-write", "org-other", "reader", "knowledge.read,knowledge.write", knowledgecore.VisibilityPrivate, knowledgecore.AccessRead, http.StatusForbidden},
		{"read-binding-global-manage", "org-other", "reader", "knowledge.read,knowledge.write,knowledge.manage", knowledgecore.VisibilityPrivate, knowledgecore.AccessRead, http.StatusForbidden},
		{"public-read-global-write", "org-other", "", "knowledge.read,knowledge.write", knowledgecore.VisibilityPublic, "", http.StatusForbidden},
		{"owner-without-global-write", "org-owner", "", "knowledge.read", knowledgecore.VisibilityPrivate, "", http.StatusForbidden},
		{"other-organization", "org-other", "", "knowledge.read,knowledge.write,knowledge.manage", knowledgecore.VisibilityScoped, "", http.StatusNotFound},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			repo := testkit.NewFakeRepository()
			now := time.Now().UTC()
			base := knowledgecore.KnowledgeBase{ID: "kb-owner", Name: "Owner library", OwnerType: knowledgecore.OwnerOrganization, OwnerID: "org-owner", Provider: knowledgecore.ProviderPageIndex, Visibility: scenario.visibility, VectorBackend: knowledgecore.VectorBackendLanceDB, ChunkPolicy: doccore.ChunkPolicy{MaxRunes: 100, OverlapRunes: 10}, Status: knowledgecore.KnowledgeActive, CreatedAt: now, UpdatedAt: now}
			if err := repo.SaveKnowledgeBase(context.Background(), base); err != nil {
				t.Fatal(err)
			}
			if scenario.binding != "" {
				if err := repo.SaveBinding(context.Background(), knowledgecore.KnowledgeBinding{ID: "binding", KnowledgeBaseID: base.ID, BoundToType: knowledgecore.BindingSubjectAgent, BoundToID: scenario.agent, Access: scenario.binding, CreatedBy: "owner", CreatedAt: now}); err != nil {
					t.Fatal(err)
				}
			}
			if err := repo.SaveDocument(context.Background(), knowledgecore.KnowledgeDocument{ID: "doc-owner", KnowledgeBaseID: base.ID, ProviderDocumentID: "pageindex-doc", Filename: "current.md", Status: knowledgecore.DocumentIndexed}); err != nil {
				t.Fatal(err)
			}
			if err := repo.SaveDocument(context.Background(), knowledgecore.KnowledgeDocument{ID: "doc-other", KnowledgeBaseID: "kb-other", ProviderDocumentID: "private-other-doc"}); err != nil {
				t.Fatal(err)
			}
			backend := &workspaceRevisionRecorder{}
			workspace := productionKnowledgeWorkspace{query: knowledgeadmin.NewQueryService(repo), repo: repo, registry: provider.NewStaticRegistry(knowledgecore.ProviderPageIndex, backend), bridge: &knowledgeontology.PlatformStore{}}
			router := httptransport.NewRouter(httptransport.RouterOptions{KnowledgeWorkspace: workspace, TrustedViewerHeaders: true, InternalViewerAuth: "Bearer workspace-test"})
			request := func(method, suffix, body string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(method, "/api/knowledge-bases/kb-owner/documents/"+suffix, strings.NewReader(body))
				req.RemoteAddr = "127.0.0.1:12345"
				req.Header.Set("X-Orrery-Internal-Viewer-Authorization", "Bearer workspace-test")
				req.Header.Set("X-User-ID", "editor")
				req.Header.Set("X-Viewer-Kind", "user")
				req.Header.Set("X-Organization-ID", scenario.organization)
				req.Header.Set("X-Tenant-ID", scenario.organization)
				req.Header.Set("X-Agent-ID", scenario.agent)
				req.Header.Set("X-Permission-Scope", scenario.scope)
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				return rec
			}
			readStatus := http.StatusOK
			if scenario.status == http.StatusNotFound {
				readStatus = http.StatusNotFound
			}
			if rec := request(http.MethodGet, "doc-owner/source?revision=2", ""); rec.Code != readStatus {
				t.Fatalf("read status=%d body=%s", rec.Code, rec.Body.String())
			}
			for _, operation := range []struct{ path, body string }{{"doc-owner/source", `{"expected_revision":2,"content":"# Updated"}`}, {"doc-owner/revisions/2", `{}`}} {
				if rec := request(http.MethodPut, operation.path, operation.body); rec.Code != scenario.status {
					t.Fatalf("%s status=%d want=%d body=%s", operation.path, rec.Code, scenario.status, rec.Body.String())
				}
			}
			if scenario.status != http.StatusOK {
				if len(backend.updates)+len(backend.publishes) != 0 {
					t.Fatal("denied request reached PageIndex mutation")
				}
				return
			}
			if len(backend.updates) != 1 || backend.updates[0].DocumentID != "pageindex-doc" || backend.updates[0].ExpectedRevision != 2 || backend.updates[0].CreatedBy != "editor" || backend.updates[0].Content != "# Updated" || len(backend.publishes) != 1 || backend.publishes[0].PublishedBy != "editor" || backend.publishes[0].Revision != 2 {
				t.Fatalf("revision contract changed: updates=%+v publishes=%+v", backend.updates, backend.publishes)
			}
			for _, operation := range []struct{ path, body string }{{"doc-other/source", `{"expected_revision":2,"content":"# Wrong base"}`}, {"doc-other/revisions/2", `{}`}} {
				if rec := request(http.MethodPut, operation.path, operation.body); rec.Code != http.StatusNotFound {
					t.Fatalf("cross-base document status=%d body=%s", rec.Code, rec.Body.String())
				}
			}
			if len(backend.updates) != 1 || len(backend.publishes) != 1 {
				t.Fatal("cross-base document reached PageIndex mutation")
			}
			backend.conflict = true
			if rec := request(http.MethodPut, "doc-owner/source", `{"expected_revision":1,"content":"# Stale"}`); rec.Code != http.StatusConflict || backend.updates[1].ExpectedRevision != 1 {
				t.Fatalf("revision conflict status=%d body=%s", rec.Code, rec.Body.String())
			}
			if scenario.binding != "" {
				if err := repo.SaveBinding(context.Background(), knowledgecore.KnowledgeBinding{ID: "binding", KnowledgeBaseID: base.ID, BoundToType: knowledgecore.BindingSubjectAgent, BoundToID: scenario.agent, Access: knowledgecore.AccessRead, CreatedBy: "owner", CreatedAt: now}); err != nil {
					t.Fatal(err)
				}
				if rec := request(http.MethodPut, "doc-owner/revisions/2", `{}`); rec.Code != http.StatusForbidden || len(backend.publishes) != 1 {
					t.Fatalf("revoked binding publish status=%d body=%s", rec.Code, rec.Body.String())
				}
			}
		})
	}
}
