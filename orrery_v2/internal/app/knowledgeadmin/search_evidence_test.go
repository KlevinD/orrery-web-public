package knowledgeadmin_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/provider"
	"orrery_v2/internal/knowledge/testkit"
	doccore "orrery_v2/internal/platform/document/core"
	"orrery_v2/internal/retrievalevidence"
)

type evidenceReranker func([]knowledgeadmin.KnowledgeSearchResultDTO) []knowledgeadmin.KnowledgeSearchResultDTO

func (r evidenceReranker) Rerank(_ context.Context, _ string, in []knowledgeadmin.KnowledgeSearchResultDTO, _ int) ([]knowledgeadmin.KnowledgeSearchResultDTO, error) {
	return r(in), nil
}

func searchEvidenceFixture(t *testing.T) (*knowledgeadmin.SearchService, *testkit.FakeRepository, *fixedSearchProvider, knowledgeadmin.ViewerRef, core.KnowledgeBase, core.KnowledgeDocument) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	repo := testkit.NewFakeRepository()
	base := core.KnowledgeBase{ID: "kb-pageindex", Name: "source", OwnerType: core.OwnerAgent, OwnerID: "owner", Visibility: core.VisibilityPublic, Provider: core.ProviderPageIndex,
		VectorBackend: core.VectorBackendLanceDB, VectorStorePath: "D:/Orrery/.tmp/search-evidence/lancedb", EmbeddingModel: "test", EmbeddingDimensions: 3,
		ChunkPolicy: doccore.ChunkPolicy{MaxRunes: 80, MinRunes: 1}, Status: core.KnowledgeActive, CreatedAt: now, UpdatedAt: now}
	if err := repo.SaveKnowledgeBase(ctx, base); err != nil {
		t.Fatal(err)
	}
	doc := core.KnowledgeDocument{ID: "orrery-doc", KnowledgeBaseID: base.ID, ProviderDocumentID: "provider-doc", Checksum: "source-checksum", Status: core.DocumentIndexed, UpdatedAt: now}
	if err := repo.SaveDocument(ctx, doc); err != nil {
		t.Fatal(err)
	}
	remote := &fixedSearchProvider{blockingSearchProvider: blockingSearchProvider{name: core.ProviderPageIndex}, page: provider.SearchResultPage{Results: []provider.SearchResult{
		{KnowledgeBaseID: string(base.ID), DocumentID: doc.ProviderDocumentID, ChunkID: "chunk", Score: 1, Snippet: "This contract ended last year; raw material remains searchable."},
	}}}
	service := knowledgeadmin.NewSearchService(repo, nil, nil).WithProviderRegistry(provider.NewStaticRegistry(core.ProviderPageIndex, remote)).WithClock(func() time.Time { return now })
	viewer := knowledgeadmin.ViewerRef{ViewerID: "reader", AgentIDs: []string{"agent"}, PermissionScope: []string{knowledgeadmin.PermissionKnowledgeRead}}
	return service, repo, remote, viewer, base, doc
}

func TestSearchEvidencePreservesMaterialAndSeparatesSourceIdentities(t *testing.T) {
	service, _, remote, viewer, _, doc := searchEvidenceFixture(t)
	remote.page.Results = append([]provider.SearchResult{{KnowledgeBaseID: "unrequested-private-kb", DocumentID: "secret", ChunkID: "secret", Score: 2, Snippet: "private"}}, remote.page.Results...)
	remote.page.Results = append(remote.page.Results, provider.SearchResult{KnowledgeBaseID: "kb-pageindex", DocumentID: "legacy-unmapped", ChunkID: "legacy", Score: 0.5, Snippet: "legacy material"})
	page, err := service.SearchKnowledge(context.Background(), viewer, knowledgeadmin.SearchKnowledgeCommand{Query: "contract", Limit: 8})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Results) != 2 {
		t.Fatalf("results=%+v", page.Results)
	}
	item := page.Results[0].Evidence
	if item == nil || item.Purpose != retrievalevidence.PurposeMaterial || item.TruthState != retrievalevidence.TruthUnknown || item.SourceState != "catalog_verified" || item.Source.ID != string(doc.ID) || item.Source.ProviderID != doc.ProviderDocumentID || item.Source.Version == "" || item.AccessHash == "" {
		t.Fatalf("evidence=%+v", item)
	}
	legacy := page.Results[1].Evidence
	if legacy == nil || legacy.SourceState != "unversioned" || legacy.Source.Version != "" || legacy.TruthState != retrievalevidence.TruthUnknown {
		t.Fatalf("legacy=%+v", legacy)
	}
}

func TestSearchRejectsSourceAndAuthorizationChangesDuringRerank(t *testing.T) {
	for _, kind := range []string{"source revision", "delete source", "revoke base", "revoke binding", "change snippet"} {
		t.Run(kind, func(t *testing.T) {
			service, repo, _, viewer, base, doc := searchEvidenceFixture(t)
			ctx := context.Background()
			if kind == "revoke binding" {
				base.Visibility = core.VisibilityPrivate
				if err := repo.SaveKnowledgeBase(ctx, base); err != nil {
					t.Fatal(err)
				}
				if err := repo.SaveBinding(ctx, core.KnowledgeBinding{ID: "grant", KnowledgeBaseID: base.ID, BoundToType: "agent", BoundToID: "agent", Access: core.AccessRead}); err != nil {
					t.Fatal(err)
				}
			}
			service.WithReranker(evidenceReranker(func(in []knowledgeadmin.KnowledgeSearchResultDTO) []knowledgeadmin.KnowledgeSearchResultDTO {
				var err error
				switch kind {
				case "source revision":
					doc.Checksum = "replacement"
					doc.UpdatedAt = doc.UpdatedAt.Add(time.Second)
					err = repo.SaveDocument(ctx, doc)
				case "delete source":
					err = repo.DeleteDocumentMetadata(ctx, doc.ID)
				case "revoke base":
					base.Visibility = core.VisibilityPrivate
					err = repo.SaveKnowledgeBase(ctx, base)
				case "revoke binding":
					err = repo.DeleteBinding(ctx, "grant")
				case "change snippet":
					in[0].Snippet = "invented current fact"
				}
				if err != nil {
					t.Fatal(err)
				}
				return in
			}))
			page, err := service.SearchKnowledge(ctx, viewer, knowledgeadmin.SearchKnowledgeCommand{Query: "contract", Limit: 1})
			if !errors.Is(err, retrievalevidence.ErrInputChanged) || len(page.Results) != 0 {
				t.Fatalf("page=%+v err=%v", page, err)
			}
		})
	}
}

func TestSearchDiscardsRerankerSuppliedVerification(t *testing.T) {
	service, _, _, viewer, _, _ := searchEvidenceFixture(t)
	service.WithReranker(evidenceReranker(func(in []knowledgeadmin.KnowledgeSearchResultDTO) []knowledgeadmin.KnowledgeSearchResultDTO {
		in[0].Evidence.TruthState = "supported"
		in[0].Evidence.InputHash = "forged"
		in[0].Score = 0.25
		return in
	}))
	page, err := service.SearchKnowledge(context.Background(), viewer, knowledgeadmin.SearchKnowledgeCommand{Query: "contract", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Results) != 1 || page.Results[0].Evidence.TruthState != retrievalevidence.TruthUnknown || page.Results[0].Evidence.InputHash == "forged" || page.Results[0].Score != 0.25 {
		t.Fatalf("results=%+v", page.Results)
	}
}

func TestSearchCheckTimeDoesNotChangeRerankerInputVersion(t *testing.T) {
	service, _, _, viewer, _, _ := searchEvidenceFixture(t)
	now := time.Now().UTC()
	service.WithClock(func() time.Time { return now })
	var hashes []string
	service.WithReranker(evidenceReranker(func(in []knowledgeadmin.KnowledgeSearchResultDTO) []knowledgeadmin.KnowledgeSearchResultDTO {
		if in[0].Evidence == nil || !in[0].Evidence.CheckedAt.IsZero() {
			t.Fatalf("reranker received volatile check time: %+v", in)
		}
		hashes = append(hashes, retrievalevidence.Fingerprint(in))
		return in
	}))
	first, err := service.SearchKnowledge(context.Background(), viewer, knowledgeadmin.SearchKnowledgeCommand{Query: "contract", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	second, err := service.SearchKnowledge(context.Background(), viewer, knowledgeadmin.SearchKnowledgeCommand{Query: "contract", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(hashes) != 2 || hashes[0] != hashes[1] {
		t.Fatal("unchanged candidates invalidated shadow cache", hashes)
	}
	if !second.Results[0].Evidence.CheckedAt.After(first.Results[0].Evidence.CheckedAt) || second.Results[0].Evidence.InputHash != first.Results[0].Evidence.InputHash {
		t.Fatal("host did not refresh check time independently from the input version")
	}
}
