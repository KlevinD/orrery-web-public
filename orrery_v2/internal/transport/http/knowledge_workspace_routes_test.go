package httptransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/app/knowledgeontology"
	"orrery_v2/internal/knowledge/provider"
)

type fakeKnowledgeWorkspace struct {
	expectedRevision int
	content          string
	published        int
}

func (f *fakeKnowledgeWorkspace) GetSource(context.Context, knowledgeadmin.ViewerRef, string, string, int) (provider.SemanticSource, error) {
	return provider.SemanticSource{DocumentID: "pi-doc-1", Revision: 2, ContentHash: "sha256:two", ContentType: "text/markdown", Content: "# Policy", Editable: true}, nil
}
func (f *fakeKnowledgeWorkspace) UpdateSource(_ context.Context, _ knowledgeadmin.ViewerRef, _, _ string, expectedRevision int, content string) (provider.DocumentRevision, error) {
	f.expectedRevision, f.content = expectedRevision, content
	return provider.DocumentRevision{DocumentID: "pi-doc-1", Revision: 3, PublicationStatus: "draft"}, nil
}
func (f *fakeKnowledgeWorkspace) ListRevisions(context.Context, knowledgeadmin.ViewerRef, string, string) ([]provider.DocumentRevision, error) {
	return []provider.DocumentRevision{{DocumentID: "pi-doc-1", Revision: 2, PublicationStatus: "published"}}, nil
}
func (f *fakeKnowledgeWorkspace) PublishRevision(_ context.Context, _ knowledgeadmin.ViewerRef, _, _ string, revision int) (provider.DocumentRevision, error) {
	f.published = revision
	return provider.DocumentRevision{DocumentID: "pi-doc-1", Revision: revision, PublicationStatus: "published"}, nil
}
func (f *fakeKnowledgeWorkspace) GetOntologyProjection(context.Context, knowledgeadmin.ViewerRef, string, string, int) (knowledgeontology.Projection, error) {
	return knowledgeontology.Projection{TenantID: "tenant-1", KnowledgeBaseID: "kb-1", DocumentID: "pi-doc-1", Revision: 2}, nil
}
func (f *fakeKnowledgeWorkspace) ListDocumentRelations(context.Context, knowledgeadmin.ViewerRef, string, string, string, int) (knowledgeadmin.KnowledgeDocumentRelationPageDTO, error) {
	return knowledgeadmin.KnowledgeDocumentRelationPageDTO{Items: []knowledgeadmin.KnowledgeDocumentRelationDTO{{Direction: "in", LinkType: knowledgeontology.DocumentReferenceLinkType, Document: knowledgeadmin.KnowledgeDocumentDTO{ID: "doc-2", KnowledgeBaseID: "kb-1", Filename: "linked.md"}}}}, nil
}

func TestKnowledgeWorkspaceRoutesProxyRevisionContracts(t *testing.T) {
	workspace := &fakeKnowledgeWorkspace{}
	router := newTestRouter(RouterOptions{KnowledgeWorkspace: workspace})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, authedKnowledgeRequest(http.MethodGet, "/api/knowledge-bases/kb-1/documents/doc-1/source?revision=2", ""))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"content":"# Policy"`) {
		t.Fatalf("source status=%d body=%s", rec.Code, rec.Body.String())
	}

	req := authedKnowledgeRequest(http.MethodPut, "/api/knowledge-bases/kb-1/documents/doc-1/source", `{"expected_revision":2,"content":"# Updated"}`)
	req.Header.Set("X-Permission-Scope", knowledgeadmin.PermissionKnowledgeWrite)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || workspace.expectedRevision != 2 || workspace.content != "# Updated" {
		t.Fatalf("update status=%d expected=%d content=%q", rec.Code, workspace.expectedRevision, workspace.content)
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, authedKnowledgeRequest(http.MethodGet, "/api/knowledge-bases/kb-1/documents/doc-1/revisions", ""))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"publication_status":"published"`) {
		t.Fatalf("revisions status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = authedKnowledgeRequest(http.MethodPut, "/api/knowledge-bases/kb-1/documents/doc-1/revisions/2", "")
	req.Header.Set("X-Permission-Scope", knowledgeadmin.PermissionKnowledgeWrite)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || workspace.published != 2 {
		t.Fatalf("publish status=%d revision=%d body=%s", rec.Code, workspace.published, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, authedKnowledgeRequest(http.MethodGet, "/api/knowledge-bases/kb-1/documents/doc-1/ontology?revision=2", ""))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"tenant_id":"tenant-1"`) {
		t.Fatalf("ontology status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, authedKnowledgeRequest(http.MethodGet, "/api/knowledge-bases/kb-1/documents/doc-1/relations?direction=both", ""))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"link_type":"knowledge_document_references_document"`) {
		t.Fatalf("relations status=%d body=%s", rec.Code, rec.Body.String())
	}
}
