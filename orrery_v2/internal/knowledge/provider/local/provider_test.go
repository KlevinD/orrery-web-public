package local_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/ingest"
	"orrery_v2/internal/knowledge/provider"
	"orrery_v2/internal/knowledge/provider/local"
	"orrery_v2/internal/knowledge/testkit"
	"orrery_v2/internal/knowledge/vector"
	doccore "orrery_v2/internal/platform/document/core"
)

func TestProviderEnsuresBaseAndUploadsThroughLocalPipeline(t *testing.T) {
	repo := testkit.NewFakeRepository()
	vectorStore := testkit.NewFakeVectorStore("D:/tmp/local-provider")
	ids := testkit.NewFakeIDGenerator()
	clock := testkit.NewFakeClock(time.Date(2026, 6, 11, 10, 0, 0, 0, time.UTC))
	artifacts := &fakeArtifactWriter{}
	execution := &fakeExecutionSubmitter{}
	p := local.New(local.Options{
		Repository: repo,
		Vector:     vectorStore,
		IDs:        ids,
		Clock:      clock,
		Artifacts:  artifacts,
		Execution:  execution,
	})
	base := core.KnowledgeBase{
		ID:                  "kb-1",
		Name:                "Local Docs",
		OwnerType:           core.OwnerOrganization,
		OwnerID:             "org-1",
		Visibility:          core.VisibilityScoped,
		Provider:            core.ProviderLocal,
		VectorBackend:       core.VectorBackendLanceDB,
		EmbeddingDimensions: 3,
		ChunkPolicy:         doccore.ChunkPolicy{MaxRunes: 120, OverlapRunes: 10, MinRunes: 1},
		Status:              core.KnowledgeActive,
	}

	ensured, err := p.EnsureKnowledgeBase(context.Background(), provider.EnsureKnowledgeBaseRequest{Base: base, VectorRoot: vectorStore.Root})
	if err != nil {
		t.Fatalf("EnsureKnowledgeBase: %v", err)
	}
	if ensured.VectorStorePath == "" || len(vectorStore.Ensured) != 1 {
		t.Fatalf("ensure result=%+v ensured=%+v", ensured, vectorStore.Ensured)
	}
	base.VectorStorePath = ensured.VectorStorePath
	if err := repo.SaveKnowledgeBase(context.Background(), base); err != nil {
		t.Fatalf("save base: %v", err)
	}

	uploaded, err := p.UploadDocument(context.Background(), provider.UploadDocumentRequest{
		Base:        base,
		DocumentID:  "kdoc-1",
		Filename:    "notes.md",
		ContentType: "text/markdown",
		Reader:      strings.NewReader("# hello"),
	})
	if err != nil {
		t.Fatalf("UploadDocument: %v", err)
	}
	if uploaded.ArtifactID != "artifact-1" || uploaded.Status != core.DocumentUploaded || !uploaded.IngestJobQueued || uploaded.IngestJobID == "" {
		t.Fatalf("uploaded result = %+v", uploaded)
	}
	if artifacts.name != "notes.md" || artifacts.content != "# hello" {
		t.Fatalf("artifact write = %+v", artifacts)
	}
	if len(execution.submitted) != 1 || execution.submitted[0].topic != ingest.TopicIngestDocument {
		t.Fatalf("execution submissions = %+v", execution.submitted)
	}
	var payload ingest.DocumentIngestPayload
	if err := json.Unmarshal(execution.submitted[0].payload, &payload); err != nil {
		t.Fatalf("decode execution payload: %v", err)
	}
	if payload.DocumentID != "kdoc-1" || payload.KnowledgeBaseID != "kb-1" {
		t.Fatalf("execution payload = %+v", payload)
	}
}

func TestProviderSearchUsesEmbeddingAndVectorStore(t *testing.T) {
	repo := testkit.NewFakeRepository()
	vectorStore := testkit.NewFakeVectorStore("D:/tmp/local-provider")
	vectorStore.SearchPage = vector.SearchResultPage{Results: []vector.SearchResult{{
		ChunkID:         "chunk-1",
		KnowledgeBaseID: "kb-1",
		DocumentID:      "kdoc-1",
		Score:           0.8,
		Text:            "release notes",
		HeadingPath:     []string{"Release"},
	}}}
	embedding := testkit.FakeEmbeddingProvider{Response: ingest.EmbedResponse{Model: "test-embedding", Dimensions: 3, Vectors: [][]float32{{1, 0, 0}}}}
	p := local.New(local.Options{Repository: repo, Vector: vectorStore, Embedding: embedding})
	base := core.KnowledgeBase{
		ID:                  "kb-1",
		Name:                "Local Docs",
		OwnerType:           core.OwnerOrganization,
		OwnerID:             "org-1",
		Visibility:          core.VisibilityPublic,
		Provider:            core.ProviderLocal,
		VectorBackend:       core.VectorBackendLanceDB,
		VectorStorePath:     "D:/tmp/local-provider/kb_kb-1/lancedb",
		EmbeddingModel:      "test-embedding",
		EmbeddingDimensions: 3,
		ChunkPolicy:         doccore.ChunkPolicy{MaxRunes: 120, OverlapRunes: 10, MinRunes: 1},
		Status:              core.KnowledgeActive,
	}
	if err := repo.SaveKnowledgeBase(context.Background(), base); err != nil {
		t.Fatalf("save base: %v", err)
	}
	if err := repo.SaveDocument(context.Background(), core.KnowledgeDocument{ID: "kdoc-1", KnowledgeBaseID: "kb-1", ArtifactID: "artifact-1", Filename: "notes.md", Status: core.DocumentIndexed}); err != nil {
		t.Fatalf("save document: %v", err)
	}

	page, err := p.Search(context.Background(), provider.SearchRequest{Query: "release", Bases: []core.KnowledgeBase{base}, Limit: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(page.Results) != 1 || page.Results[0].DocumentName != "notes.md" || page.Results[0].Snippet != "release notes" {
		t.Fatalf("search page = %+v", page)
	}
	if vectorStore.SearchCallCount() != 1 {
		t.Fatalf("search call count = %d", vectorStore.SearchCallCount())
	}
}

func TestProviderSearchSkipsEmbeddingForEmptyBase(t *testing.T) {
	repo := testkit.NewFakeRepository()
	vectorStore := testkit.NewFakeVectorStore("D:/tmp/local-provider")
	p := local.New(local.Options{
		Repository: repo,
		Vector:     vectorStore,
		Embedding:  testkit.FakeEmbeddingProvider{},
	})
	base := core.KnowledgeBase{
		ID:                  "kb-empty",
		Provider:            core.ProviderLocal,
		VectorBackend:       core.VectorBackendLanceDB,
		VectorStorePath:     "D:/tmp/local-provider/kb_kb-empty/lancedb",
		EmbeddingModel:      "not-an-embedding-model",
		EmbeddingDimensions: 3,
		Status:              core.KnowledgeActive,
	}

	page, err := p.Search(context.Background(), provider.SearchRequest{
		Query: "release",
		Bases: []core.KnowledgeBase{base},
		Limit: 5,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(page.Results) != 0 || vectorStore.SearchCallCount() != 0 {
		t.Fatalf("empty search page = %+v, vector calls = %d", page, vectorStore.SearchCallCount())
	}
}

type fakeArtifactWriter struct {
	name        string
	contentType string
	content     string
}

func (f *fakeArtifactWriter) PutArtifact(ctx context.Context, name, contentType string, reader io.Reader) (local.ArtifactRef, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return local.ArtifactRef{}, err
	}
	f.name = name
	f.contentType = contentType
	f.content = string(data)
	return local.ArtifactRef{
		ID:          "artifact-1",
		Name:        name,
		ContentType: contentType,
		SizeBytes:   int64(len(data)),
		Checksum:    checksum(data),
	}, nil
}

type fakeExecutionSubmitter struct {
	submitted []submittedExecution
}

type submittedExecution struct {
	topic       string
	payload     []byte
	maxAttempts int
}

func (f *fakeExecutionSubmitter) Submit(ctx context.Context, topic string, payload []byte, maxAttempts int) error {
	f.submitted = append(f.submitted, submittedExecution{topic: topic, payload: append([]byte(nil), payload...), maxAttempts: maxAttempts})
	return nil
}

func checksum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
