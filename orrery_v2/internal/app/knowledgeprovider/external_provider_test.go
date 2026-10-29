package knowledgeprovider

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	knowledgecore "orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/provider"
)

type fakeMCPKnowledgePort struct {
	search []ExternalSearchResult
	pages  []map[string]any
}

func (f fakeMCPKnowledgePort) SearchDocuments(context.Context, Binding, string, string, string, int, provider.RetrievalMode) ([]ExternalSearchResult, error) {
	return f.search, nil
}

func (f fakeMCPKnowledgePort) GetPageContent(context.Context, Binding, string, string, string) ([]map[string]any, error) {
	return f.pages, nil
}

func TestExternalProviderMapsSearchToLocalCatalog(t *testing.T) {
	ctx := provider.WithRequestMetadata(context.Background(), provider.RequestMetadata{Actor: "viewer:operator"})
	store := newTestProviderStore(t, "external-provider-search")
	binding := Binding{ID: "binding-1", Name: "PageIndex", MCPServerID: "mcp.pageindex", Mode: ModeReadOnly, Status: StatusActive}
	if err := store.SaveBinding(ctx, binding); err != nil {
		t.Fatalf("save binding: %v", err)
	}
	if err := store.SaveMapping(ctx, ExternalMapping{BindingID: binding.ID, Kind: ResourceDocument, ExternalID: "external-doc", LocalID: "local-doc"}); err != nil {
		t.Fatalf("save mapping: %v", err)
	}
	external := NewExternalProvider(store, fakeMCPKnowledgePort{search: []ExternalSearchResult{{
		DocumentID: "external-doc", ChunkID: "external-doc:1", Score: 0.8, Snippet: "release checklist", Filename: "release.md",
	}}})
	page, err := external.Search(ctx, provider.SearchRequest{Query: "release", Limit: 8, Bases: []knowledgecore.KnowledgeBase{{
		ID: "kb-1", Provider: knowledgecore.ProviderMCP, ProviderResourceID: "folder-1", ProviderMetadata: map[string]string{"binding_id": binding.ID},
	}}})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(page.Results) != 1 || page.Results[0].KnowledgeBaseID != "kb-1" || page.Results[0].DocumentID != "local-doc" {
		t.Fatalf("mapped search results: %+v", page.Results)
	}
}

type blockingMCPKnowledgePort struct {
	started chan string
	release chan struct{}
}

type selectiveMCPKnowledgePort struct {
	results map[string][]ExternalSearchResult
	errors  map[string]error
}

type deadlineMCPKnowledgePort struct{}

func (deadlineMCPKnowledgePort) SearchDocuments(ctx context.Context, _ Binding, _ string, folderID, _ string, _ int, _ provider.RetrievalMode) ([]ExternalSearchResult, error) {
	if folderID == "folder-fast" {
		return []ExternalSearchResult{{DocumentID: "doc-fast", Snippet: "completed"}}, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (deadlineMCPKnowledgePort) GetPageContent(context.Context, Binding, string, string, string) ([]map[string]any, error) {
	return nil, nil
}

func (p selectiveMCPKnowledgePort) SearchDocuments(_ context.Context, _ Binding, _ string, folderID, _ string, _ int, _ provider.RetrievalMode) ([]ExternalSearchResult, error) {
	if err := p.errors[folderID]; err != nil {
		return nil, err
	}
	return p.results[folderID], nil
}

func (selectiveMCPKnowledgePort) GetPageContent(context.Context, Binding, string, string, string) ([]map[string]any, error) {
	return nil, nil
}

func (p blockingMCPKnowledgePort) SearchDocuments(ctx context.Context, _ Binding, _ string, folderID, _ string, _ int, _ provider.RetrievalMode) ([]ExternalSearchResult, error) {
	p.started <- folderID
	select {
	case <-p.release:
		return []ExternalSearchResult{{DocumentID: "doc-" + folderID}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (blockingMCPKnowledgePort) GetPageContent(context.Context, Binding, string, string, string) ([]map[string]any, error) {
	return nil, nil
}

func TestExternalProviderSearchesBasesConcurrently(t *testing.T) {
	ctx := context.Background()
	store := newTestProviderStore(t, "external-provider-concurrent-search")
	binding := Binding{ID: "binding-1", Name: "PageIndex", MCPServerID: "mcp.pageindex", Mode: ModeReadOnly, Status: StatusActive}
	if err := store.SaveBinding(ctx, binding); err != nil {
		t.Fatalf("save binding: %v", err)
	}
	started := make(chan string, 2)
	release := make(chan struct{})
	external := NewExternalProvider(store, blockingMCPKnowledgePort{started: started, release: release})
	done := make(chan error, 1)
	go func() {
		page, err := external.Search(ctx, provider.SearchRequest{Query: "release", Limit: 8, Bases: []knowledgecore.KnowledgeBase{
			{ID: "kb-1", Provider: knowledgecore.ProviderMCP, ProviderResourceID: "folder-1", ProviderMetadata: map[string]string{"binding_id": binding.ID}},
			{ID: "kb-2", Provider: knowledgecore.ProviderMCP, ProviderResourceID: "folder-2", ProviderMetadata: map[string]string{"binding_id": binding.ID}},
		}})
		if err == nil && len(page.Results) != 2 {
			err = fmt.Errorf("result count = %d, want 2", len(page.Results))
		}
		done <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("first folder search did not start")
	}
	select {
	case <-started:
		close(release)
	case <-time.After(200 * time.Millisecond):
		close(release)
		<-done
		t.Fatal("second folder search did not start concurrently")
	}
	if err := <-done; err != nil {
		t.Fatalf("search: %v", err)
	}
}

func TestExternalProviderReturnsCompletedResultsWhenOneBaseFails(t *testing.T) {
	ctx := context.Background()
	store := newTestProviderStore(t, "external-provider-partial-search")
	binding := Binding{ID: "binding-1", Name: "PageIndex", MCPServerID: "mcp.pageindex", Mode: ModeReadOnly, Status: StatusActive}
	if err := store.SaveBinding(ctx, binding); err != nil {
		t.Fatalf("save binding: %v", err)
	}
	external := NewExternalProvider(store, selectiveMCPKnowledgePort{
		results: map[string][]ExternalSearchResult{"folder-1": {{DocumentID: "doc-1", Snippet: "release"}}},
		errors:  map[string]error{"folder-2": context.DeadlineExceeded},
	})

	page, err := external.Search(ctx, provider.SearchRequest{Query: "release", Limit: 8, Bases: []knowledgecore.KnowledgeBase{
		{ID: "kb-1", Provider: knowledgecore.ProviderMCP, ProviderResourceID: "folder-1", ProviderMetadata: map[string]string{"binding_id": binding.ID}},
		{ID: "kb-2", Provider: knowledgecore.ProviderMCP, ProviderResourceID: "folder-2", ProviderMetadata: map[string]string{"binding_id": binding.ID}},
	}})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(page.Results) != 1 || page.Results[0].KnowledgeBaseID != "kb-1" || page.Results[0].DocumentID == "" || page.Results[0].Snippet != "release" {
		t.Fatalf("partial results: %+v", page.Results)
	}
}

func TestExternalProviderReturnsErrorWhenAllBasesFail(t *testing.T) {
	ctx := context.Background()
	store := newTestProviderStore(t, "external-provider-all-searches-fail")
	binding := Binding{ID: "binding-1", Name: "PageIndex", MCPServerID: "mcp.pageindex", Mode: ModeReadOnly, Status: StatusActive}
	if err := store.SaveBinding(ctx, binding); err != nil {
		t.Fatalf("save binding: %v", err)
	}
	external := NewExternalProvider(store, selectiveMCPKnowledgePort{errors: map[string]error{
		"folder-1": context.DeadlineExceeded,
		"folder-2": context.DeadlineExceeded,
	}})

	_, err := external.Search(ctx, provider.SearchRequest{Query: "release", Limit: 8, Bases: []knowledgecore.KnowledgeBase{
		{ID: "kb-1", Provider: knowledgecore.ProviderMCP, ProviderResourceID: "folder-1", ProviderMetadata: map[string]string{"binding_id": binding.ID}},
		{ID: "kb-2", Provider: knowledgecore.ProviderMCP, ProviderResourceID: "folder-2", ProviderMetadata: map[string]string{"binding_id": binding.ID}},
	}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("search error = %v, want deadline exceeded", err)
	}
}

func TestExternalProviderReturnsCompletedResultsAtOverallDeadline(t *testing.T) {
	store := newTestProviderStore(t, "external-provider-deadline-partial-search")
	binding := Binding{ID: "binding-1", Name: "PageIndex", MCPServerID: "mcp.pageindex", Mode: ModeReadOnly, Status: StatusActive}
	if err := store.SaveBinding(context.Background(), binding); err != nil {
		t.Fatalf("save binding: %v", err)
	}
	external := NewExternalProvider(store, deadlineMCPKnowledgePort{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	page, err := external.Search(ctx, provider.SearchRequest{Query: "release", Limit: 8, Bases: []knowledgecore.KnowledgeBase{
		{ID: "kb-fast", Provider: knowledgecore.ProviderMCP, ProviderResourceID: "folder-fast", ProviderMetadata: map[string]string{"binding_id": binding.ID}},
		{ID: "kb-slow", Provider: knowledgecore.ProviderMCP, ProviderResourceID: "folder-slow", ProviderMetadata: map[string]string{"binding_id": binding.ID}},
	}})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(page.Results) != 1 || page.Results[0].KnowledgeBaseID != "kb-fast" || page.Results[0].Snippet != "completed" {
		t.Fatalf("partial deadline results: %+v", page.Results)
	}
}

func TestExternalProviderReadsScopedSemanticSource(t *testing.T) {
	ctx := context.Background()
	store := newTestProviderStore(t, "external-provider-source")
	binding := Binding{ID: "binding-1", Name: "PageIndex", MCPServerID: "mcp.pageindex", Mode: ModeReadOnly, Status: StatusActive}
	if err := store.SaveBinding(ctx, binding); err != nil {
		t.Fatalf("save binding: %v", err)
	}
	external := NewExternalProvider(store, fakeMCPKnowledgePort{pages: []map[string]any{
		{"page": 1, "text": "first"}, {"page": 2, "content": "second"},
	}})
	source, err := external.GetSemanticSourceForDocument(ctx,
		knowledgecore.KnowledgeBase{ProviderMetadata: map[string]string{"binding_id": binding.ID}},
		knowledgecore.KnowledgeDocument{ProviderDocumentID: "external-doc", ContentType: "text/markdown"}, 0,
	)
	if err != nil {
		t.Fatalf("get semantic source: %v", err)
	}
	if source.Content != "first\n\nsecond" || source.DocumentID != "external-doc" || source.Editable {
		t.Fatalf("semantic source: %+v", source)
	}
}
