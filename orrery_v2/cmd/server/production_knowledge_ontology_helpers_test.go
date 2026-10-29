package main

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/provider"
	"orrery_v2/internal/platform/graphprotocol"
)

type productionOntologyKnowledgeProvider struct {
	ensureResult     provider.EnsureKnowledgeBaseResult
	uploadResult     provider.UploadDocumentResult
	statusResult     provider.DocumentStatusResult
	reprocessResult  provider.ReprocessDocumentResult
	processingResult provider.DocumentProcessingResult
}

func contextGraphEvidenceIDs(t *testing.T, evidence []contextgraph.Evidence, wantPrefix string) []string {
	t.Helper()
	var ids []string
	for _, item := range evidence {
		if strings.HasPrefix(item.ID, wantPrefix) {
			ids = append(ids, item.ID)
		}
	}
	if len(ids) == 0 {
		t.Fatalf("evidence prefix %s missing in %+v", wantPrefix, evidence)
	}
	return ids
}

func publishKnowledgeReplayDocument(t *testing.T, ctx context.Context, sink productionDocumentOntologySink, occurredAt time.Time, documentID string, providerDocumentID string) {
	t.Helper()
	if err := sink.PublishKnowledgeDocumentMetadata(ctx, graphprotocol.DomainEvent{
		ID:         "knowledge-document-metadata:" + documentID + ":rev-1",
		TenantID:   "org-1",
		Type:       "knowledge.document.metadata.updated",
		StreamID:   "knowledge_document:" + documentID,
		Version:    1,
		OccurredAt: occurredAt,
		Payload: map[string]any{
			"id":                   documentID,
			"knowledge_base_id":    "kb-1",
			"title":                "Replay document " + documentID,
			"source_uri":           "pageindex://document/" + providerDocumentID,
			"provider":             string(core.ProviderPageIndex),
			"provider_document_id": providerDocumentID,
			"content_type":         "text/markdown",
			"size_bytes":           4096,
			"checksum":             "sha256:" + documentID,
			"status":               string(core.DocumentUploaded),
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge"},
	}); err != nil {
		t.Fatalf("PublishKnowledgeDocumentMetadata %s: %v", documentID, err)
	}
}

func publishKnowledgeReplayPage(t *testing.T, ctx context.Context, sink productionDocumentOntologySink, occurredAt time.Time, documentID string, providerDocumentID string) {
	t.Helper()
	if err := sink.PublishKnowledgePageMetadata(ctx, graphprotocol.DomainEvent{
		ID:         "knowledge-page-metadata:" + documentID + ":page:1:rev-1",
		TenantID:   "org-1",
		Type:       "knowledge.page.metadata.updated",
		StreamID:   "knowledge_document:" + documentID,
		Version:    1,
		OccurredAt: occurredAt,
		Payload: map[string]any{
			"id":                   documentID + ":page:1",
			"knowledge_base_id":    "kb-1",
			"document_id":          documentID,
			"page_number":          1,
			"provider":             string(core.ProviderPageIndex),
			"provider_document_id": providerDocumentID,
			"heading_path":         []any{"Replay"},
			"source_uri":           "pageindex://document/" + providerDocumentID + "#page=1",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge"},
	}); err != nil {
		t.Fatalf("PublishKnowledgePageMetadata %s: %v", documentID, err)
	}
}

func (p productionOntologyKnowledgeProvider) Name() core.KnowledgeProvider {
	return core.ProviderPageIndex
}

func (p productionOntologyKnowledgeProvider) EnsureKnowledgeBase(ctx context.Context, req provider.EnsureKnowledgeBaseRequest) (provider.EnsureKnowledgeBaseResult, error) {
	return p.ensureResult, ctx.Err()
}

func (p productionOntologyKnowledgeProvider) DeleteKnowledgeBase(ctx context.Context, req provider.DeleteKnowledgeBaseRequest) error {
	return ctx.Err()
}

func (p productionOntologyKnowledgeProvider) UploadDocument(ctx context.Context, req provider.UploadDocumentRequest) (provider.UploadDocumentResult, error) {
	if _, err := io.ReadAll(req.Reader); err != nil {
		return provider.UploadDocumentResult{}, err
	}
	return p.uploadResult, ctx.Err()
}

func (p productionOntologyKnowledgeProvider) CreateDocumentMetadata(ctx context.Context, req provider.CreateDocumentMetadataRequest) (provider.UploadDocumentResult, error) {
	return p.uploadResult, ctx.Err()
}

func (p productionOntologyKnowledgeProvider) DeleteDocument(ctx context.Context, req provider.DeleteDocumentRequest) error {
	return ctx.Err()
}

func (p productionOntologyKnowledgeProvider) RefreshDocumentStatus(ctx context.Context, req provider.DocumentStatusRequest) (provider.DocumentStatusResult, error) {
	return p.statusResult, ctx.Err()
}

func (p productionOntologyKnowledgeProvider) ReprocessDocument(ctx context.Context, req provider.ReprocessDocumentRequest) (provider.ReprocessDocumentResult, error) {
	return p.reprocessResult, ctx.Err()
}

func (p productionOntologyKnowledgeProvider) GetDocumentProcessingResult(ctx context.Context, req provider.DocumentProcessingResultRequest) (provider.DocumentProcessingResult, error) {
	return p.processingResult, ctx.Err()
}

func (p productionOntologyKnowledgeProvider) ReindexKnowledgeBase(ctx context.Context, req provider.ReindexKnowledgeBaseRequest) (provider.ReindexKnowledgeBaseResult, error) {
	return provider.ReindexKnowledgeBaseResult{}, ctx.Err()
}

func (p productionOntologyKnowledgeProvider) Search(ctx context.Context, req provider.SearchRequest) (provider.SearchResultPage, error) {
	return provider.SearchResultPage{}, ctx.Err()
}

type productionOntologyArtifactReader struct {
	content string
}

func (r productionOntologyArtifactReader) GetReader(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(r.content)), nil
}
