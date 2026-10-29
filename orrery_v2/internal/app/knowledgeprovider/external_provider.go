package knowledgeprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	knowledgecore "orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/provider"
)

type MCPKnowledgePort interface {
	SearchDocuments(context.Context, Binding, string, string, string, int, provider.RetrievalMode) ([]ExternalSearchResult, error)
	GetPageContent(context.Context, Binding, string, string, string) ([]map[string]any, error)
}

type ExternalProvider struct {
	bindings interface {
		GetBinding(context.Context, string) (Binding, error)
		FindMapping(context.Context, string, ResourceKind, string) (ExternalMapping, error)
	}
	mcp MCPKnowledgePort
}

const externalSearchConcurrency = 4

type externalBaseSearchResult struct {
	binding Binding
	items   []ExternalSearchResult
	err     error
}

func NewExternalProvider(bindings interface {
	GetBinding(context.Context, string) (Binding, error)
	FindMapping(context.Context, string, ResourceKind, string) (ExternalMapping, error)
}, mcp MCPKnowledgePort) *ExternalProvider {
	return &ExternalProvider{bindings: bindings, mcp: mcp}
}

var (
	_ provider.Provider                     = (*ExternalProvider)(nil)
	_ provider.RevisionProvider             = (*ExternalProvider)(nil)
	_ provider.ScopedSemanticSourceProvider = (*ExternalProvider)(nil)
)

func (p *ExternalProvider) Name() knowledgecore.KnowledgeProvider {
	return knowledgecore.ProviderMCP
}

func (p *ExternalProvider) EnsureKnowledgeBase(ctx context.Context, req provider.EnsureKnowledgeBaseRequest) (provider.EnsureKnowledgeBaseResult, error) {
	if err := ctx.Err(); err != nil {
		return provider.EnsureKnowledgeBaseResult{}, err
	}
	return provider.EnsureKnowledgeBaseResult{ProviderResourceID: req.Base.ProviderResourceID, Metadata: cloneStringMetadata(req.Base.ProviderMetadata)}, nil
}

func (p *ExternalProvider) DeleteKnowledgeBase(context.Context, provider.DeleteKnowledgeBaseRequest) error {
	return knowledgecore.ErrPermissionDenied
}

func (p *ExternalProvider) UploadDocument(context.Context, provider.UploadDocumentRequest) (provider.UploadDocumentResult, error) {
	return provider.UploadDocumentResult{}, knowledgecore.ErrPermissionDenied
}

func (p *ExternalProvider) CreateDocumentMetadata(ctx context.Context, req provider.CreateDocumentMetadataRequest) (provider.UploadDocumentResult, error) {
	if err := ctx.Err(); err != nil {
		return provider.UploadDocumentResult{}, err
	}
	return provider.UploadDocumentResult{
		ArtifactID: req.Document.ArtifactID, ProviderDocumentID: req.Document.ProviderDocumentID,
		Filename: req.Document.Filename, ContentType: req.Document.ContentType, SizeBytes: req.Document.SizeBytes,
		Checksum: req.Document.Checksum, Status: req.Document.Status, ProviderMetadata: cloneStringMetadata(req.Document.ProviderMetadata),
	}, nil
}

func (p *ExternalProvider) DeleteDocument(context.Context, provider.DeleteDocumentRequest) error {
	return knowledgecore.ErrPermissionDenied
}

func (p *ExternalProvider) RefreshDocumentStatus(ctx context.Context, req provider.DocumentStatusRequest) (provider.DocumentStatusResult, error) {
	if err := ctx.Err(); err != nil {
		return provider.DocumentStatusResult{}, err
	}
	return provider.DocumentStatusResult{
		ProviderDocumentID: req.Document.ProviderDocumentID, Filename: req.Document.Filename,
		ContentType: req.Document.ContentType, SizeBytes: req.Document.SizeBytes, Checksum: req.Document.Checksum,
		Status: req.Document.Status, ErrorMessage: req.Document.ErrorMessage, ProviderMetadata: cloneStringMetadata(req.Document.ProviderMetadata),
	}, nil
}

func (p *ExternalProvider) ReprocessDocument(context.Context, provider.ReprocessDocumentRequest) (provider.ReprocessDocumentResult, error) {
	return provider.ReprocessDocumentResult{}, knowledgecore.ErrPermissionDenied
}

func (p *ExternalProvider) GetDocumentProcessingResult(ctx context.Context, req provider.DocumentProcessingResultRequest) (provider.DocumentProcessingResult, error) {
	binding, err := p.bindingForBase(ctx, req.Base)
	if err != nil {
		return provider.DocumentProcessingResult{}, err
	}
	pages, err := p.mcp.GetPageContent(ctx, binding, providerActor(ctx), req.Document.ProviderDocumentID, pageSelection(req.Pages))
	if err != nil {
		return provider.DocumentProcessingResult{}, err
	}
	return provider.DocumentProcessingResult{Filename: req.Document.Filename, Status: knowledgecore.DocumentIndexed, Progress: 100, Stage: "indexed", Pages: pages}, nil
}

func (p *ExternalProvider) ReindexKnowledgeBase(context.Context, provider.ReindexKnowledgeBaseRequest) (provider.ReindexKnowledgeBaseResult, error) {
	return provider.ReindexKnowledgeBaseResult{Supported: false}, knowledgecore.ErrPermissionDenied
}

func (p *ExternalProvider) Search(ctx context.Context, req provider.SearchRequest) (provider.SearchResultPage, error) {
	if p == nil || p.bindings == nil || p.mcp == nil {
		return provider.SearchResultPage{}, knowledgecore.ErrDependencyUnavailable
	}
	searches := make([]externalBaseSearchResult, len(req.Bases))
	completed := make(chan int, len(req.Bases))
	semaphore := make(chan struct{}, externalSearchConcurrency)
	for index, base := range req.Bases {
		index, base := index, base
		go func() {
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				searches[index].err = ctx.Err()
				completed <- index
				return
			}
			binding, err := p.bindingForBase(ctx, base)
			if err == nil {
				searches[index].binding = binding
				searches[index].items, err = p.mcp.SearchDocuments(ctx, binding, providerActor(ctx), base.ProviderResourceID, req.Query, req.Limit, req.RetrievalMode)
			}
			searches[index].err = err
			completed <- index
		}()
	}
	var firstErr error
	succeeded := 0
	for range req.Bases {
		index := <-completed
		if searches[index].err != nil {
			if firstErr == nil {
				firstErr = searches[index].err
			}
			continue
		}
		succeeded++
	}
	if firstErr != nil && succeeded == 0 {
		return provider.SearchResultPage{}, firstErr
	}
	mappingCtx := ctx
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		mappingCtx = context.WithoutCancel(ctx)
	}
	results := []provider.SearchResult{}
	for index, base := range req.Bases {
		if searches[index].err != nil {
			continue
		}
		binding := searches[index].binding
		for _, item := range searches[index].items {
			localDocumentID := externalLocalID("doc", binding.ID, item.DocumentID)
			if mapping, mapErr := p.bindings.FindMapping(mappingCtx, binding.ID, ResourceDocument, item.DocumentID); mapErr == nil {
				localDocumentID = mapping.LocalID
			} else if !errors.Is(mapErr, ErrNotFound) {
				return provider.SearchResultPage{}, mapErr
			}
			results = append(results, provider.SearchResult{
				KnowledgeBaseID: string(base.ID), DocumentID: localDocumentID, ChunkID: item.ChunkID,
				Score: item.Score, Snippet: item.Snippet, HeadingPath: append([]string(nil), item.HeadingPath...), DocumentName: item.Filename,
			})
		}
	}
	return provider.SearchResultPage{Results: results}, nil
}

func (p *ExternalProvider) GetSemanticSourceForDocument(ctx context.Context, base knowledgecore.KnowledgeBase, document knowledgecore.KnowledgeDocument, revision int) (provider.SemanticSource, error) {
	binding, err := p.bindingForBase(ctx, base)
	if err != nil {
		return provider.SemanticSource{}, err
	}
	pages, err := p.mcp.GetPageContent(ctx, binding, providerActor(ctx), document.ProviderDocumentID, "1-100")
	if err != nil {
		return provider.SemanticSource{}, err
	}
	content := joinPageContent(pages)
	sum := sha256.Sum256([]byte(content))
	if revision < 1 {
		revision = 1
	}
	return provider.SemanticSource{
		DocumentID: document.ProviderDocumentID, Revision: revision, ContentHash: hex.EncodeToString(sum[:]),
		ContentType: document.ContentType, Content: content, Editable: false,
	}, nil
}

func (p *ExternalProvider) UpdateDocumentContent(context.Context, provider.UpdateDocumentContentRequest) (provider.DocumentRevision, error) {
	return provider.DocumentRevision{}, knowledgecore.ErrPermissionDenied
}

func (p *ExternalProvider) ListDocumentRevisions(ctx context.Context, req provider.DocumentRevisionsRequest) ([]provider.DocumentRevision, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []provider.DocumentRevision{{DocumentID: req.DocumentID, Revision: 1, PublicationStatus: "published"}}, nil
}

func (p *ExternalProvider) GetDocumentRevision(context.Context, provider.DocumentRevisionRequest) (provider.DocumentRevision, error) {
	return provider.DocumentRevision{}, knowledgecore.ErrPermissionDenied
}

func (p *ExternalProvider) GetSemanticSource(context.Context, provider.SemanticSourceRequest) (provider.SemanticSource, error) {
	return provider.SemanticSource{}, fmt.Errorf("%w: scoped MCP knowledge base is required", knowledgecore.ErrInvalid)
}

func (p *ExternalProvider) PublishDocumentRevision(context.Context, provider.PublishDocumentRevisionRequest) (provider.DocumentRevision, error) {
	return provider.DocumentRevision{}, knowledgecore.ErrPermissionDenied
}

func (p *ExternalProvider) ListKnowledgeChanges(context.Context, provider.KnowledgeChangesRequest) (provider.KnowledgeChangePage, error) {
	return provider.KnowledgeChangePage{}, knowledgecore.ErrPermissionDenied
}

func (p *ExternalProvider) bindingForBase(ctx context.Context, base knowledgecore.KnowledgeBase) (Binding, error) {
	if p == nil || p.bindings == nil || p.mcp == nil {
		return Binding{}, knowledgecore.ErrDependencyUnavailable
	}
	bindingID := strings.TrimSpace(base.ProviderMetadata["binding_id"])
	if bindingID == "" {
		return Binding{}, fmt.Errorf("%w: MCP binding metadata is missing", knowledgecore.ErrInvalid)
	}
	return p.bindings.GetBinding(ctx, bindingID)
}

func providerActor(ctx context.Context) string {
	actor := strings.TrimSpace(provider.RequestMetadataFromContext(ctx).Actor)
	if _, value, ok := strings.Cut(actor, ":"); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	if actor == "" {
		return "orrery"
	}
	return actor
}

func pageSelection(pages string) string {
	if strings.TrimSpace(pages) == "" || strings.TrimSpace(pages) == "all" {
		return "1-100"
	}
	return strings.TrimSpace(pages)
}

func joinPageContent(pages []map[string]any) string {
	parts := make([]string, 0, len(pages))
	for _, page := range pages {
		for _, key := range []string{"text", "content", "markdown"} {
			if value, ok := page[key].(string); ok && strings.TrimSpace(value) != "" {
				parts = append(parts, strings.TrimSpace(value))
				break
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func cloneStringMetadata(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
