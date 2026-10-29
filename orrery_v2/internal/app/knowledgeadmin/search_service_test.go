package knowledgeadmin_test

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/decision/laya"
	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/ingest"
	"orrery_v2/internal/knowledge/provider"
	"orrery_v2/internal/knowledge/testkit"
	"orrery_v2/internal/knowledge/vector"
	"orrery_v2/internal/knowledge/vector/jsonstore"
	doccore "orrery_v2/internal/platform/document/core"
)

type searchPrivacyLayaJudge struct{ states chan []string }

func (j searchPrivacyLayaJudge) Choose(ctx context.Context, states []string, q laya.Question) ([]laya.Decision, error) {
	j.states <- states
	out := make([]laya.Decision, len(states))
	for i := range out {
		out[i].Choice = "direct"
	}
	return out, nil
}

func TestSearchPreventsPrivateLeak(t *testing.T) {
	repo := testkit.NewFakeRepository()
	vectorStore := testkit.NewFakeVectorStore("D:/tmp/knowledge-search")
	vectorStore.SearchPage = vector.SearchResultPage{Results: []vector.SearchResult{
		{ChunkID: "chunk-public", KnowledgeBaseID: "kb-public", DocumentID: "doc-public", Score: 0.9, Text: "public snippet", HeadingPath: []string{"Public"}},
		{ChunkID: "chunk-private", KnowledgeBaseID: "kb-private", DocumentID: "doc-private", Score: 0.99, Text: "private snippet", HeadingPath: []string{"Private"}},
	}}
	embedding := testkit.FakeEmbeddingProvider{Response: ingest.EmbedResponse{Model: "test-embedding", Dimensions: 3, Vectors: [][]float32{{1, 0, 0}}}}
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	saveBase := func(base core.KnowledgeBase) {
		t.Helper()
		if err := repo.SaveKnowledgeBase(context.Background(), base); err != nil {
			t.Fatalf("save base: %v", err)
		}
	}
	saveBase(core.KnowledgeBase{
		ID:                  "kb-public",
		Name:                "Public",
		OwnerType:           core.OwnerOrganization,
		OwnerID:             "org-1",
		Visibility:          core.VisibilityPublic,
		VectorBackend:       core.VectorBackendLanceDB,
		VectorStorePath:     "D:/tmp/knowledge-search/kb_public/lancedb",
		EmbeddingModel:      "test-embedding",
		EmbeddingDimensions: 3,
		ChunkPolicy:         doccore.ChunkPolicy{MaxRunes: 80, OverlapRunes: 0, MinRunes: 1},
		Status:              core.KnowledgeActive,
		CreatedAt:           now,
		UpdatedAt:           now,
	})
	saveBase(core.KnowledgeBase{
		ID:                  "kb-private",
		Name:                "Private",
		OwnerType:           core.OwnerHuman,
		OwnerID:             "human-owner",
		Visibility:          core.VisibilityPrivate,
		VectorBackend:       core.VectorBackendLanceDB,
		VectorStorePath:     "D:/tmp/knowledge-search/kb_private/lancedb",
		EmbeddingModel:      "test-embedding",
		EmbeddingDimensions: 3,
		ChunkPolicy:         doccore.ChunkPolicy{MaxRunes: 80, OverlapRunes: 0, MinRunes: 1},
		Status:              core.KnowledgeActive,
		CreatedAt:           now,
		UpdatedAt:           now,
	})
	_ = repo.SaveDocument(context.Background(), core.KnowledgeDocument{ID: "doc-public", KnowledgeBaseID: "kb-public", ArtifactID: "artifact-public", Filename: "public.md", Status: core.DocumentIndexed})
	_ = repo.SaveDocument(context.Background(), core.KnowledgeDocument{ID: "doc-private", KnowledgeBaseID: "kb-private", ArtifactID: "artifact-private", Filename: "private.md", Status: core.DocumentIndexed})

	states := make(chan []string, 1)
	observer := laya.NewObserver(context.Background(), searchPrivacyLayaJudge{states}, nil)
	defer observer.Close()
	service := knowledgeadmin.NewSearchService(repo, vectorStore, embedding).WithReranker(knowledgeadmin.NewLayaShadowReranker(observer))
	page, err := service.SearchKnowledge(context.Background(), knowledgeadmin.ViewerRef{
		ViewerID:        "human-reader",
		PermissionScope: []string{knowledgeadmin.PermissionKnowledgeRead},
	}, knowledgeadmin.SearchKnowledgeCommand{Query: "mission", Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(page.Results) != 1 {
		t.Fatalf("result count = %d, want 1: %#v", len(page.Results), page.Results)
	}
	got := page.Results[0]
	if got.KnowledgeBaseID != "kb-public" || got.DocumentID != "doc-public" || got.DocumentName != "public.md" || got.Snippet != "public snippet" {
		t.Fatalf("unexpected result: %#v", got)
	}
	select {
	case observed := <-states:
		if len(observed) != 1 || strings.Contains(strings.Join(observed, "\n"), "private snippet") {
			t.Fatalf("private evidence reached model: %v", observed)
		}
	case <-time.After(time.Second):
		t.Fatal("authorized search did not reach shadow observer")
	}
}

func TestSearchUsesPerBaseVectorStoreRoot(t *testing.T) {
	repo := testkit.NewFakeRepository()
	vectorStore := jsonstore.NewStore("D:/default-root")
	embedding := testkit.FakeEmbeddingProvider{Response: ingest.EmbedResponse{Model: "test-embedding", Dimensions: 2, Vectors: [][]float32{{1, 0}}}}
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	customRoot := filepath.Join(t.TempDir(), "custom-root")
	base := core.KnowledgeBase{
		ID:                  "kb-custom",
		Name:                "Custom",
		OwnerType:           core.OwnerOrganization,
		OwnerID:             "org-1",
		Visibility:          core.VisibilityPublic,
		VectorBackend:       core.VectorBackendLanceDB,
		VectorStorePath:     filepath.Join(customRoot, "kb_kb-custom", "lancedb"),
		EmbeddingModel:      "test-embedding",
		EmbeddingDimensions: 2,
		ChunkPolicy:         doccore.ChunkPolicy{MaxRunes: 100, OverlapRunes: 10, MinRunes: 1},
		Status:              core.KnowledgeActive,
		CreatedBy:           "admin",
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if err := repo.SaveKnowledgeBase(context.Background(), base); err != nil {
		t.Fatalf("save base: %v", err)
	}
	if err := repo.SaveDocument(context.Background(), core.KnowledgeDocument{
		ID:              "doc-custom",
		KnowledgeBaseID: base.ID,
		ArtifactID:      "artifact-1",
		Filename:        "notes.md",
		ContentType:     "text/markdown",
		Status:          core.DocumentIndexed,
		CreatedBy:       "admin",
		CreatedAt:       now,
		UpdatedAt:       now,
	}); err != nil {
		t.Fatalf("save doc: %v", err)
	}
	descriptor, err := jsonstore.DescriptorForKnowledgeBase(customRoot, base.ID, base.EmbeddingDimensions)
	if err != nil {
		t.Fatalf("descriptor: %v", err)
	}
	if err := vectorStore.UpsertChunks(context.Background(), descriptor, []vector.ChunkVector{{
		ChunkID:         "chunk-custom",
		KnowledgeBaseID: string(base.ID),
		DocumentID:      "doc-custom",
		Text:            "custom root result",
		Vector:          []float32{1, 0},
	}}); err != nil {
		t.Fatalf("upsert chunks: %v", err)
	}

	service := knowledgeadmin.NewSearchService(repo, vectorStore, embedding)
	page, err := service.SearchKnowledge(context.Background(), knowledgeadmin.ViewerRef{
		ViewerID:        "reader",
		PermissionScope: []string{knowledgeadmin.PermissionKnowledgeRead},
	}, knowledgeadmin.SearchKnowledgeCommand{Query: "custom", Limit: 1})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(page.Results) != 1 || page.Results[0].ChunkID != "chunk-custom" {
		t.Fatalf("search results mismatch: %#v", page.Results)
	}
}

func TestSearchAggregatesReranksAndLimitsAcrossKnowledgeBases(t *testing.T) {
	repo := testkit.NewFakeRepository()
	vectorStore := testkit.NewFakeVectorStore("D:/tmp/knowledge-search")
	vectorStore.SearchPages = map[string]vector.SearchResultPage{
		"kb-a-low": {Results: []vector.SearchResult{
			{ChunkID: "chunk-low", KnowledgeBaseID: "kb-a-low", DocumentID: "doc-low", Score: 0.2, Text: "low score", HeadingPath: []string{"Low"}},
		}},
		"kb-b-high": {Results: []vector.SearchResult{
			{ChunkID: "chunk-high", KnowledgeBaseID: "kb-b-high", DocumentID: "doc-high", Score: 0.95, Text: "high score", HeadingPath: []string{"High"}},
		}},
	}
	embedding := testkit.FakeEmbeddingProvider{Response: ingest.EmbedResponse{Model: "test-embedding", Dimensions: 3, Vectors: [][]float32{{1, 0, 0}}}}
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	for _, seed := range []struct {
		baseID core.KnowledgeBaseID
		docID  core.KnowledgeDocumentID
		name   string
	}{
		{baseID: "kb-a-low", docID: "doc-low", name: "low.md"},
		{baseID: "kb-b-high", docID: "doc-high", name: "high.md"},
	} {
		if err := repo.SaveKnowledgeBase(context.Background(), core.KnowledgeBase{
			ID:                  seed.baseID,
			Name:                string(seed.baseID),
			OwnerType:           core.OwnerOrganization,
			OwnerID:             "org-1",
			Visibility:          core.VisibilityPublic,
			VectorBackend:       core.VectorBackendLanceDB,
			VectorStorePath:     "D:/tmp/knowledge-search/kb_" + string(seed.baseID) + "/lancedb",
			EmbeddingModel:      "test-embedding",
			EmbeddingDimensions: 3,
			ChunkPolicy:         doccore.ChunkPolicy{MaxRunes: 80, OverlapRunes: 0, MinRunes: 1},
			Status:              core.KnowledgeActive,
			CreatedAt:           now,
			UpdatedAt:           now,
		}); err != nil {
			t.Fatalf("save base %s: %v", seed.baseID, err)
		}
		if err := repo.SaveDocument(context.Background(), core.KnowledgeDocument{
			ID:              seed.docID,
			KnowledgeBaseID: seed.baseID,
			ArtifactID:      "artifact-" + string(seed.docID),
			Filename:        seed.name,
			Status:          core.DocumentIndexed,
		}); err != nil {
			t.Fatalf("save doc %s: %v", seed.docID, err)
		}
	}

	service := knowledgeadmin.NewSearchService(repo, vectorStore, embedding)
	page, err := service.SearchKnowledge(context.Background(), knowledgeadmin.ViewerRef{
		ViewerID:        "reader",
		OrganizationIDs: []string{"org-1"},
		PermissionScope: []string{knowledgeadmin.PermissionKnowledgeRead},
	}, knowledgeadmin.SearchKnowledgeCommand{
		Query: "mission", Limit: 1, RetrievalMode: provider.RetrievalModeAgentic,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if vectorStore.SearchCallCount() != 2 {
		t.Fatalf("search call count = %d, want both knowledge bases searched", vectorStore.SearchCallCount())
	}
	if len(page.Results) != 1 || page.Results[0].ChunkID != "chunk-high" || page.Results[0].DocumentName != "high.md" {
		t.Fatalf("global reranked top result mismatch: %#v", page.Results)
	}
}

func TestSearchRequestsCandidateLimitBeforeReranking(t *testing.T) {
	repo := testkit.NewFakeRepository()
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	base := core.KnowledgeBase{
		ID:                  "kb-pageindex",
		Name:                "PageIndex",
		OwnerType:           core.OwnerGlobal,
		Visibility:          core.VisibilityPublic,
		Provider:            core.ProviderPageIndex,
		VectorBackend:       core.VectorBackendLanceDB,
		VectorStorePath:     "D:/tmp/knowledge-search/kb_pageindex/lancedb",
		EmbeddingModel:      "test-embedding",
		EmbeddingDimensions: 3,
		ChunkPolicy:         doccore.ChunkPolicy{MaxRunes: 80, OverlapRunes: 0, MinRunes: 1},
		Status:              core.KnowledgeActive,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if err := repo.SaveKnowledgeBase(context.Background(), base); err != nil {
		t.Fatalf("save base: %v", err)
	}
	searchProvider := &candidateLimitSearchProvider{
		blockingSearchProvider: blockingSearchProvider{name: core.ProviderPageIndex},
		results: []provider.SearchResult{
			{
				KnowledgeBaseID: string(base.ID),
				DocumentID:      "doc-low",
				ChunkID:         "chunk-low",
				Score:           0.1,
				Snippet:         "low candidate",
			},
			{
				KnowledgeBaseID: string(base.ID),
				DocumentID:      "doc-high",
				ChunkID:         "chunk-high",
				Score:           0.95,
				Snippet:         "high candidate",
			},
		},
	}
	service := knowledgeadmin.NewSearchService(repo, nil, nil).WithProviderRegistry(
		provider.NewStaticRegistry(core.ProviderPageIndex, searchProvider),
	)
	page, err := service.SearchKnowledge(context.Background(), knowledgeadmin.ViewerRef{
		ViewerID:        "reader",
		PermissionScope: []string{knowledgeadmin.PermissionKnowledgeRead},
	}, knowledgeadmin.SearchKnowledgeCommand{
		Query: "mission", Limit: 1, RetrievalMode: provider.RetrievalModeAgentic,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if searchProvider.lastLimit <= 1 {
		t.Fatalf("provider candidate limit = %d, want more than final result limit", searchProvider.lastLimit)
	}
	if searchProvider.lastMode != provider.RetrievalModeAgentic {
		t.Fatalf("provider retrieval mode = %q", searchProvider.lastMode)
	}
	if len(page.Executions) != 1 || page.Executions[0].ActualMode != "agentic_tree_search" {
		t.Fatalf("search executions = %+v", page.Executions)
	}
	if len(page.Results) != 1 || page.Results[0].ChunkID != "chunk-high" {
		t.Fatalf("reranked result = %#v, want high-scoring candidate from provider's expanded candidate set", page.Results)
	}
}

func TestSearchIncludesPositionAndRoleAccessibleKnowledgeBases(t *testing.T) {
	repo := testkit.NewFakeRepository()
	vectorStore := testkit.NewFakeVectorStore("D:/tmp/knowledge-search")
	vectorStore.SearchPages = map[string]vector.SearchResultPage{
		"kb-position": {Results: []vector.SearchResult{{
			ChunkID: "chunk-position", KnowledgeBaseID: "kb-position", DocumentID: "doc-position", Score: 0.8, Text: "position result",
		}}},
		"kb-role": {Results: []vector.SearchResult{{
			ChunkID: "chunk-role", KnowledgeBaseID: "kb-role", DocumentID: "doc-role", Score: 0.9, Text: "role result",
		}}},
	}
	embedding := testkit.FakeEmbeddingProvider{Response: ingest.EmbedResponse{Model: "test-embedding", Dimensions: 3, Vectors: [][]float32{{1, 0, 0}}}}
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	for _, seed := range []struct {
		baseID core.KnowledgeBaseID
		docID  core.KnowledgeDocumentID
	}{
		{baseID: "kb-position", docID: "doc-position"},
		{baseID: "kb-role", docID: "doc-role"},
	} {
		if err := repo.SaveKnowledgeBase(context.Background(), core.KnowledgeBase{
			ID:                  seed.baseID,
			Name:                string(seed.baseID),
			OwnerType:           core.OwnerOrganization,
			OwnerID:             "org-private",
			Visibility:          core.VisibilityPrivate,
			VectorBackend:       core.VectorBackendLanceDB,
			VectorStorePath:     "D:/tmp/knowledge-search/kb_" + string(seed.baseID) + "/lancedb",
			EmbeddingModel:      "test-embedding",
			EmbeddingDimensions: 3,
			ChunkPolicy:         doccore.ChunkPolicy{MaxRunes: 80, OverlapRunes: 0, MinRunes: 1},
			Status:              core.KnowledgeActive,
			CreatedAt:           now,
			UpdatedAt:           now,
		}); err != nil {
			t.Fatalf("save base %s: %v", seed.baseID, err)
		}
		if err := repo.SaveDocument(context.Background(), core.KnowledgeDocument{ID: seed.docID, KnowledgeBaseID: seed.baseID, ArtifactID: "artifact-" + string(seed.docID), Filename: string(seed.docID) + ".md", Status: core.DocumentIndexed}); err != nil {
			t.Fatalf("save doc %s: %v", seed.docID, err)
		}
	}
	saveAccessBinding(t, repo, "bind-position-search", "kb-position", core.BindingSubjectPosition, "position-pm", core.AccessRead)
	saveAccessBinding(t, repo, "bind-role-search", "kb-role", core.BindingSubjectRole, "role-reviewer", core.AccessRead)

	service := knowledgeadmin.NewSearchService(repo, vectorStore, embedding)
	page, err := service.SearchKnowledge(context.Background(), knowledgeadmin.ViewerRef{
		ViewerID:        "agent-pm",
		PositionIDs:     []string{"position-pm"},
		RoleIDs:         []string{"role-reviewer"},
		PermissionScope: []string{knowledgeadmin.PermissionKnowledgeRead},
	}, knowledgeadmin.SearchKnowledgeCommand{Query: "mission", Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if vectorStore.SearchCallCount() != 2 {
		t.Fatalf("search call count = %d, want position and role bases searched", vectorStore.SearchCallCount())
	}
	if len(page.Results) != 2 {
		t.Fatalf("results = %#v", page.Results)
	}
}

func TestSearchQueriesProviderGroupsInParallel(t *testing.T) {
	repo := testkit.NewFakeRepository()
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	for _, seed := range []struct {
		id       core.KnowledgeBaseID
		provider core.KnowledgeProvider
	}{
		{id: "kb-local", provider: core.ProviderLocal},
		{id: "kb-pageindex", provider: core.ProviderPageIndex},
	} {
		if err := repo.SaveKnowledgeBase(context.Background(), core.KnowledgeBase{
			ID:                  seed.id,
			Name:                string(seed.id),
			OwnerType:           core.OwnerGlobal,
			Visibility:          core.VisibilityPublic,
			Provider:            seed.provider,
			VectorBackend:       core.VectorBackendLanceDB,
			VectorStorePath:     "D:/tmp/knowledge-search/kb_" + string(seed.id) + "/lancedb",
			EmbeddingModel:      "test-embedding",
			EmbeddingDimensions: 3,
			ChunkPolicy:         doccore.ChunkPolicy{MaxRunes: 80, OverlapRunes: 0, MinRunes: 1},
			Status:              core.KnowledgeActive,
			CreatedAt:           now,
			UpdatedAt:           now,
		}); err != nil {
			t.Fatalf("save base %s: %v", seed.id, err)
		}
	}

	started := make(chan core.KnowledgeProvider, 2)
	release := make(chan struct{})
	registry := provider.NewStaticRegistry(core.ProviderLocal,
		&blockingSearchProvider{name: core.ProviderLocal, started: started, release: release},
		&blockingSearchProvider{name: core.ProviderPageIndex, started: started, release: release},
	)
	service := knowledgeadmin.NewSearchService(repo, nil, nil).WithProviderRegistry(registry)
	done := make(chan error, 1)
	go func() {
		_, err := service.SearchKnowledge(context.Background(), knowledgeadmin.ViewerRef{
			ViewerID:        "reader",
			PermissionScope: []string{knowledgeadmin.PermissionKnowledgeRead},
		}, knowledgeadmin.SearchKnowledgeCommand{Query: "mission", Limit: 10})
		done <- err
	}()

	waitForProviderStart(t, started)
	waitForProviderStart(t, started)
	close(release)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("search: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("search did not finish after both provider groups were released")
	}
}

func TestSearchReturnsCompletedResultsAtOverallDeadline(t *testing.T) {
	repo := testkit.NewFakeRepository()
	base := core.KnowledgeBase{
		ID: "kb-pageindex", Name: "PageIndex", OwnerType: core.OwnerGlobal,
		Visibility: core.VisibilityPublic, Provider: core.ProviderPageIndex,
		VectorBackend: core.VectorBackendLanceDB, VectorStorePath: "D:/tmp/knowledge-search/kb_pageindex/lancedb",
		EmbeddingModel: "test-embedding", EmbeddingDimensions: 3,
		ChunkPolicy: doccore.ChunkPolicy{MaxRunes: 80, MinRunes: 1}, Status: core.KnowledgeActive,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := repo.SaveKnowledgeBase(context.Background(), base); err != nil {
		t.Fatalf("save base: %v", err)
	}
	searchProvider := &deadlinePartialSearchProvider{blockingSearchProvider: blockingSearchProvider{name: core.ProviderPageIndex}}
	service := knowledgeadmin.NewSearchService(repo, nil, nil).WithProviderRegistry(
		provider.NewStaticRegistry(core.ProviderPageIndex, searchProvider),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	page, err := service.SearchKnowledge(ctx, knowledgeadmin.ViewerRef{
		ViewerID: "reader", PermissionScope: []string{knowledgeadmin.PermissionKnowledgeRead},
	}, knowledgeadmin.SearchKnowledgeCommand{Query: "mission", Limit: 1})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(page.Results) != 1 || page.Results[0].ChunkID != "chunk-completed" {
		t.Fatalf("partial deadline results = %+v", page.Results)
	}
}

func TestSearchReturnsCompletedProviderResultsWhenAnotherProviderTimesOut(t *testing.T) {
	repo := testkit.NewFakeRepository()
	now := time.Now().UTC()
	for _, base := range []core.KnowledgeBase{
		{
			ID: "kb-pageindex", Name: "PageIndex", OwnerType: core.OwnerGlobal,
			Visibility: core.VisibilityPublic, Provider: core.ProviderPageIndex,
			VectorBackend: core.VectorBackendLanceDB, VectorStorePath: "D:/tmp/knowledge-search/kb_pageindex/lancedb",
			EmbeddingModel: "test-embedding", EmbeddingDimensions: 3,
			ChunkPolicy: doccore.ChunkPolicy{MaxRunes: 80, MinRunes: 1}, Status: core.KnowledgeActive,
			CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "kb-mcp", Name: "MCP", OwnerType: core.OwnerGlobal,
			Visibility: core.VisibilityPublic, Provider: core.ProviderMCP,
			VectorBackend: core.VectorBackendLanceDB, VectorStorePath: "D:/tmp/knowledge-search/kb_mcp/lancedb",
			EmbeddingModel: "test-embedding", EmbeddingDimensions: 3,
			ChunkPolicy: doccore.ChunkPolicy{MaxRunes: 80, MinRunes: 1}, Status: core.KnowledgeActive,
			CreatedAt: now, UpdatedAt: now,
		},
	} {
		if err := repo.SaveKnowledgeBase(context.Background(), base); err != nil {
			t.Fatalf("save base %s: %v", base.ID, err)
		}
	}
	completed := &fixedSearchProvider{
		blockingSearchProvider: blockingSearchProvider{name: core.ProviderPageIndex},
		page: provider.SearchResultPage{Results: []provider.SearchResult{{
			KnowledgeBaseID: "kb-pageindex", DocumentID: "doc-completed", ChunkID: "chunk-completed",
			Score: 0.9, Snippet: "completed provider result",
		}}},
	}
	timedOut := &deadlineErrorSearchProvider{blockingSearchProvider: blockingSearchProvider{name: core.ProviderMCP}}
	service := knowledgeadmin.NewSearchService(repo, nil, nil).WithProviderRegistry(
		provider.NewStaticRegistry(core.ProviderPageIndex, completed, timedOut),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	page, err := service.SearchKnowledge(ctx, knowledgeadmin.ViewerRef{
		ViewerID: "reader", PermissionScope: []string{knowledgeadmin.PermissionKnowledgeRead},
	}, knowledgeadmin.SearchKnowledgeCommand{Query: "mission", Limit: 1})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(page.Results) != 1 || page.Results[0].ChunkID != "chunk-completed" {
		t.Fatalf("completed provider results = %+v", page.Results)
	}
}

func TestSearchReturnsCompletedEmptyProviderResultsWhenAnotherProviderTimesOut(t *testing.T) {
	repo := testkit.NewFakeRepository()
	now := time.Now().UTC()
	for _, base := range []core.KnowledgeBase{
		{
			ID: "kb-pageindex", Name: "PageIndex", OwnerType: core.OwnerGlobal,
			Visibility: core.VisibilityPublic, Provider: core.ProviderPageIndex,
			VectorBackend: core.VectorBackendLanceDB, VectorStorePath: "D:/tmp/knowledge-search/kb_pageindex/lancedb",
			EmbeddingModel: "test-embedding", EmbeddingDimensions: 3,
			ChunkPolicy: doccore.ChunkPolicy{MaxRunes: 80, MinRunes: 1}, Status: core.KnowledgeActive,
			CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "kb-mcp", Name: "MCP", OwnerType: core.OwnerGlobal,
			Visibility: core.VisibilityPublic, Provider: core.ProviderMCP,
			VectorBackend: core.VectorBackendLanceDB, VectorStorePath: "D:/tmp/knowledge-search/kb_mcp/lancedb",
			EmbeddingModel: "test-embedding", EmbeddingDimensions: 3,
			ChunkPolicy: doccore.ChunkPolicy{MaxRunes: 80, MinRunes: 1}, Status: core.KnowledgeActive,
			CreatedAt: now, UpdatedAt: now,
		},
	} {
		if err := repo.SaveKnowledgeBase(context.Background(), base); err != nil {
			t.Fatalf("save base %s: %v", base.ID, err)
		}
	}
	completed := &fixedSearchProvider{
		blockingSearchProvider: blockingSearchProvider{name: core.ProviderPageIndex},
		page:                   provider.SearchResultPage{Results: []provider.SearchResult{}},
	}
	timedOut := &deadlineErrorSearchProvider{blockingSearchProvider: blockingSearchProvider{name: core.ProviderMCP}}
	service := knowledgeadmin.NewSearchService(repo, nil, nil).WithProviderRegistry(
		provider.NewStaticRegistry(core.ProviderPageIndex, completed, timedOut),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	page, err := service.SearchKnowledge(ctx, knowledgeadmin.ViewerRef{
		ViewerID: "reader", PermissionScope: []string{knowledgeadmin.PermissionKnowledgeRead},
	}, knowledgeadmin.SearchKnowledgeCommand{Query: "mission", Limit: 1})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(page.Results) != 0 {
		t.Fatalf("completed empty provider results = %+v", page.Results)
	}
}

func TestSearchHonorsOwnerFilter(t *testing.T) {
	repo := testkit.NewFakeRepository()
	vectorStore := testkit.NewFakeVectorStore("D:/tmp/knowledge-search")
	vectorStore.SearchPage = vector.SearchResultPage{Results: []vector.SearchResult{
		{ChunkID: "chunk-org-1", KnowledgeBaseID: "kb-org-1", DocumentID: "doc-org-1", Score: 0.9, Text: "org one", HeadingPath: []string{"Org 1"}},
		{ChunkID: "chunk-org-2", KnowledgeBaseID: "kb-org-2", DocumentID: "doc-org-2", Score: 0.8, Text: "org two", HeadingPath: []string{"Org 2"}},
	}}
	embedding := testkit.FakeEmbeddingProvider{Response: ingest.EmbedResponse{Model: "test-embedding", Dimensions: 3, Vectors: [][]float32{{1, 0, 0}}}}
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	for _, seed := range []struct {
		id      core.KnowledgeBaseID
		ownerID string
		docID   core.KnowledgeDocumentID
	}{
		{id: "kb-org-1", ownerID: "org-1", docID: "doc-org-1"},
		{id: "kb-org-2", ownerID: "org-2", docID: "doc-org-2"},
	} {
		if err := repo.SaveKnowledgeBase(context.Background(), core.KnowledgeBase{
			ID:                  seed.id,
			Name:                string(seed.id),
			OwnerType:           core.OwnerOrganization,
			OwnerID:             seed.ownerID,
			Visibility:          core.VisibilityPublic,
			VectorBackend:       core.VectorBackendLanceDB,
			VectorStorePath:     "D:/tmp/knowledge-search/kb_" + string(seed.id) + "/lancedb",
			EmbeddingModel:      "test-embedding",
			EmbeddingDimensions: 3,
			ChunkPolicy:         doccore.ChunkPolicy{MaxRunes: 80, OverlapRunes: 0, MinRunes: 1},
			Status:              core.KnowledgeActive,
			CreatedAt:           now,
			UpdatedAt:           now,
		}); err != nil {
			t.Fatalf("save base %s: %v", seed.id, err)
		}
		if err := repo.SaveDocument(context.Background(), core.KnowledgeDocument{ID: seed.docID, KnowledgeBaseID: seed.id, ArtifactID: "artifact-" + string(seed.docID), Filename: string(seed.docID) + ".md", Status: core.DocumentIndexed}); err != nil {
			t.Fatalf("save doc %s: %v", seed.docID, err)
		}
	}

	service := knowledgeadmin.NewSearchService(repo, vectorStore, embedding)
	page, err := service.SearchKnowledge(context.Background(), knowledgeadmin.ViewerRef{
		ViewerID:        "reader",
		PermissionScope: []string{knowledgeadmin.PermissionKnowledgeRead},
	}, knowledgeadmin.SearchKnowledgeCommand{
		Query:       "mission",
		OwnerFilter: knowledgeadmin.OwnerFilter{OwnerType: string(core.OwnerOrganization), OwnerID: "org-2"},
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(page.Results) != 1 || page.Results[0].KnowledgeBaseID != "kb-org-2" {
		t.Fatalf("filtered search results = %#v", page.Results)
	}
}

type blockingSearchProvider struct {
	name    core.KnowledgeProvider
	started chan<- core.KnowledgeProvider
	release <-chan struct{}
}

type candidateLimitSearchProvider struct {
	blockingSearchProvider
	results   []provider.SearchResult
	lastLimit int
	lastMode  provider.RetrievalMode
}

type deadlinePartialSearchProvider struct {
	blockingSearchProvider
}

type fixedSearchProvider struct {
	blockingSearchProvider
	page provider.SearchResultPage
}

type deadlineErrorSearchProvider struct {
	blockingSearchProvider
}

func (p *deadlinePartialSearchProvider) Search(ctx context.Context, _ provider.SearchRequest) (provider.SearchResultPage, error) {
	<-ctx.Done()
	return provider.SearchResultPage{Results: []provider.SearchResult{{
		KnowledgeBaseID: "kb-pageindex",
		DocumentID:      "doc-completed",
		ChunkID:         "chunk-completed",
		Score:           0.9,
		Snippet:         "completed before deadline",
	}}}, nil
}

func (p *fixedSearchProvider) Search(context.Context, provider.SearchRequest) (provider.SearchResultPage, error) {
	return p.page, nil
}

func (p *deadlineErrorSearchProvider) Search(ctx context.Context, _ provider.SearchRequest) (provider.SearchResultPage, error) {
	<-ctx.Done()
	return provider.SearchResultPage{}, ctx.Err()
}

func (p *blockingSearchProvider) Name() core.KnowledgeProvider {
	return p.name
}

func (p *blockingSearchProvider) EnsureKnowledgeBase(ctx context.Context, req provider.EnsureKnowledgeBaseRequest) (provider.EnsureKnowledgeBaseResult, error) {
	return provider.EnsureKnowledgeBaseResult{}, nil
}

func (p *blockingSearchProvider) DeleteKnowledgeBase(ctx context.Context, req provider.DeleteKnowledgeBaseRequest) error {
	return nil
}

func (p *blockingSearchProvider) UploadDocument(ctx context.Context, req provider.UploadDocumentRequest) (provider.UploadDocumentResult, error) {
	if req.Reader != nil {
		_, _ = io.Copy(io.Discard, req.Reader)
	}
	return provider.UploadDocumentResult{}, nil
}

func (p *blockingSearchProvider) CreateDocumentMetadata(ctx context.Context, req provider.CreateDocumentMetadataRequest) (provider.UploadDocumentResult, error) {
	return provider.UploadDocumentResult{}, nil
}

func (p *blockingSearchProvider) DeleteDocument(ctx context.Context, req provider.DeleteDocumentRequest) error {
	return nil
}

func (p *blockingSearchProvider) RefreshDocumentStatus(ctx context.Context, req provider.DocumentStatusRequest) (provider.DocumentStatusResult, error) {
	return provider.DocumentStatusResult{}, nil
}

func (p *blockingSearchProvider) ReprocessDocument(ctx context.Context, req provider.ReprocessDocumentRequest) (provider.ReprocessDocumentResult, error) {
	return provider.ReprocessDocumentResult{}, nil
}

func (p *blockingSearchProvider) GetDocumentProcessingResult(ctx context.Context, req provider.DocumentProcessingResultRequest) (provider.DocumentProcessingResult, error) {
	return provider.DocumentProcessingResult{}, nil
}

func (p *blockingSearchProvider) ReindexKnowledgeBase(ctx context.Context, req provider.ReindexKnowledgeBaseRequest) (provider.ReindexKnowledgeBaseResult, error) {
	return provider.ReindexKnowledgeBaseResult{}, nil
}

func (p *blockingSearchProvider) Search(ctx context.Context, req provider.SearchRequest) (provider.SearchResultPage, error) {
	p.started <- p.name
	select {
	case <-p.release:
		return provider.SearchResultPage{Results: []provider.SearchResult{{
			KnowledgeBaseID: string(req.Bases[0].ID),
			DocumentID:      "doc-" + string(req.Bases[0].ID),
			ChunkID:         "chunk-" + string(req.Bases[0].ID),
			Score:           0.5,
			Snippet:         "result",
		}}}, nil
	case <-ctx.Done():
		return provider.SearchResultPage{}, ctx.Err()
	}
}

func (p *candidateLimitSearchProvider) Search(ctx context.Context, req provider.SearchRequest) (provider.SearchResultPage, error) {
	if err := ctx.Err(); err != nil {
		return provider.SearchResultPage{}, err
	}
	p.lastLimit = req.Limit
	p.lastMode = req.RetrievalMode
	results := append([]provider.SearchResult(nil), p.results...)
	if req.Limit > 0 && len(results) > req.Limit {
		results = results[:req.Limit]
	}
	return provider.SearchResultPage{
		Results: results,
		Executions: []provider.SearchExecution{{
			Provider: core.ProviderPageIndex, KnowledgeBaseID: string(req.Bases[0].ID),
			RequestedMode: req.RetrievalMode, ActualMode: "agentic_tree_search",
		}},
	}, nil
}

func waitForProviderStart(t *testing.T, started <-chan core.KnowledgeProvider) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("timed out waiting for provider search to start")
	}
}
