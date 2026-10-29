package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/knowledge/core"
	knowledgestore "orrery_v2/internal/knowledge/store"
	"orrery_v2/internal/platform"
	doccore "orrery_v2/internal/platform/document/core"
	"orrery_v2/internal/platform/metrics"
	"orrery_v2/internal/platform/testkit"
	httptransport "orrery_v2/internal/transport/http"
)

func TestKnowledgeModuleRegistersMetadataAndReadyProbe(t *testing.T) {
	rt := testkit.NewInitializedTestRuntime(t, KnowledgeModuleWithRoot(t.TempDir()))

	if ready, err := rt.Migrations.LatestApplied(context.Background()); err != nil || !ready {
		t.Fatalf("knowledge migrations not applied: ready=%v err=%v", ready, err)
	}
	report := rt.Health.Ready(context.Background())
	found := false
	for _, check := range report.Checks {
		if check.Name == "knowledge" {
			found = true
			if !check.OK || check.Message != "knowledge disabled" {
				t.Fatalf("knowledge ready check mismatch: %#v", check)
			}
		}
	}
	if !found {
		t.Fatalf("knowledge ready check not registered: %#v", report.Checks)
	}
}

func TestKnowledgeModuleReportsMetadataStatusMetrics(t *testing.T) {
	ctx := context.Background()
	rt := testkit.NewInitializedTestRuntime(t, KnowledgeModule())
	coreStore, err := rt.Databases.Store("core")
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	repo := knowledgestore.NewPlatformStore(coreStore)
	active := moduleTestBase("kb-active", core.KnowledgeActive)
	deleting := moduleTestBase("kb-deleting", core.KnowledgeDeleting)
	if err := repo.SaveKnowledgeBase(ctx, active); err != nil {
		t.Fatalf("save active base: %v", err)
	}
	if err := repo.SaveKnowledgeBase(ctx, deleting); err != nil {
		t.Fatalf("save deleting base: %v", err)
	}
	if err := repo.SaveDocument(ctx, moduleTestDocument("doc-indexed", active.ID, core.DocumentIndexed)); err != nil {
		t.Fatalf("save indexed doc: %v", err)
	}
	if err := repo.SaveDocument(ctx, moduleTestDocument("doc-failed", deleting.ID, core.DocumentFailed)); err != nil {
		t.Fatalf("save failed doc: %v", err)
	}
	if err := repo.SaveIngestJob(ctx, moduleTestJob("job-queued", active.ID, core.JobQueued)); err != nil {
		t.Fatalf("save queued job: %v", err)
	}
	if err := repo.SaveIngestJob(ctx, moduleTestJob("job-failed", deleting.ID, core.JobFailed)); err != nil {
		t.Fatalf("save failed job: %v", err)
	}

	rec := httptest.NewRecorder()
	platform.NewMetricsHandler(rt).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	text := string(body)
	for _, want := range []string{
		`orrery_knowledge_bases_total{role="core",status="active"} 1`,
		`orrery_knowledge_bases_total{role="core",status="deleting"} 1`,
		`orrery_knowledge_documents_total{role="core",status="failed"} 1`,
		`orrery_knowledge_ingest_jobs_total{role="core",status="queued"} 1`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("metrics output missing %q: %s", want, text)
		}
	}
}

func TestKnowledgeReadyProbeReportsMetadataFailure(t *testing.T) {
	status := knowledgeReadyProbe(failingKnowledgeReadyRepo{}, t.TempDir())(context.Background())
	if status.OK || status.Message != "knowledge metadata unavailable" {
		t.Fatalf("metadata failure ready status mismatch: %#v", status)
	}
}

func TestKnowledgeModuleReadyProbeReportsDisabledWithoutActiveBase(t *testing.T) {
	status := knowledgeReadyProbe(fixedKnowledgeReadyRepo{
		counts: map[core.KnowledgeStatus]int64{core.KnowledgeDisabled: 1},
	}, t.TempDir())(context.Background())
	if !status.OK || status.Message != "knowledge disabled" {
		t.Fatalf("disabled ready status mismatch: %#v", status)
	}
}

func TestKnowledgeModuleSweeperCleansDeletingBase(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	rt := testkit.NewInitializedTestRuntime(t, KnowledgeModuleWithRoot(root))
	coreStore, err := rt.Databases.Store("core")
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	repo := knowledgestore.NewPlatformStore(coreStore)
	base := moduleTestBase("kb-sweep-delete", core.KnowledgeDeleting)
	if err := repo.SaveKnowledgeBase(ctx, base); err != nil {
		t.Fatalf("save deleting base: %v", err)
	}
	storePath := filepath.Join(root, "kb_"+string(base.ID), "lancedb")
	if err := os.MkdirAll(storePath, 0o755); err != nil {
		t.Fatalf("create vector store path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(storePath, "chunks.json"), []byte(`{"chunks":[]}`), 0o600); err != nil {
		t.Fatalf("write vector chunks: %v", err)
	}

	report := rt.Sweeper.SweepOnce(ctx)
	if report.Failed != 0 {
		t.Fatalf("sweeper report should be clean: %#v", report)
	}
	if _, err := repo.GetKnowledgeBase(ctx, base.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("deleting base metadata should be removed, got %v", err)
	}
	if _, err := os.Stat(filepath.Dir(storePath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("vector base directory should be removed, got %v", err)
	}
}

func TestKnowledgeModuleReadyProbeChecksRootWritable(t *testing.T) {
	ctx := context.Background()
	blockingFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockingFile, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}
	rt := testkit.NewInitializedTestRuntime(t, KnowledgeModuleWithRoot(blockingFile))

	report := rt.Health.Ready(ctx)
	found := false
	for _, check := range report.Checks {
		if check.Name == "knowledge" {
			found = true
			if check.OK || strings.Contains(check.Message, blockingFile) {
				t.Fatalf("knowledge ready check should fail without leaking path: %#v", check)
			}
		}
	}
	if !found {
		t.Fatalf("knowledge ready check not registered: %#v", report.Checks)
	}
}

func TestKnowledgeModuleUsesConfiguredRoot(t *testing.T) {
	ctx := context.Background()
	configRoot := filepath.Join(t.TempDir(), "configured-knowledge")
	defaultRoot := defaultKnowledgeRoot()
	_, defaultRootErr := os.Stat(defaultRoot)
	defaultRootExisted := defaultRootErr == nil
	t.Cleanup(func() {
		if !defaultRootExisted {
			_ = os.RemoveAll(defaultRoot)
		}
	})
	cfg := testkit.NewTestConfigFromYAML(t, writeKnowledgeModuleRuntimeYAML(t, configRoot))
	rt, err := platform.OpenWithModules(ctx, cfg, KnowledgeModule())
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	defer rt.Close()
	if err := rt.Initialize(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}

	report := rt.Health.Ready(ctx)
	for _, check := range report.Checks {
		if check.Name == "knowledge" && (!check.OK || check.Message != "knowledge disabled") {
			t.Fatalf("knowledge ready check mismatch: %#v", check)
		}
	}
	if info, err := os.Stat(configRoot); err != nil || !info.IsDir() {
		t.Fatalf("configured knowledge root was not prepared: info=%#v err=%v", info, err)
	}
}

type failingKnowledgeReadyRepo struct{}

func (failingKnowledgeReadyRepo) CountKnowledgeBasesByStatus(context.Context) (map[core.KnowledgeStatus]int64, error) {
	return nil, errors.New("metadata unavailable")
}

type fixedKnowledgeReadyRepo struct {
	counts map[core.KnowledgeStatus]int64
}

func (r fixedKnowledgeReadyRepo) CountKnowledgeBasesByStatus(context.Context) (map[core.KnowledgeStatus]int64, error) {
	return r.counts, nil
}

func TestProductionKnowledgeUploadExecutionAndSearch(t *testing.T) {
	ctx := context.Background()
	t.Setenv("ORRERY_SECRET_LLM_EMBEDDING", "sk-test")
	cfg := testkit.NewTestConfigFromYAML(t, writeKnowledgeModuleRuntimeYAML(t, t.TempDir()))
	rt, err := platform.OpenWithModules(ctx, cfg, KnowledgeModule(), OntologyModule(), AuthModule())
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	defer rt.Close()
	if err := rt.Initialize(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}

	embeddingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Fatalf("embedding path = %s", r.URL.Path)
		}
		var body struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode embedding request: %v", err)
		}
		data := make([]map[string][]float32, 0, len(body.Input))
		for _, input := range body.Input {
			data = append(data, map[string][]float32{"embedding": testEmbedding(input)})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "test-embedding",
			"data":  data,
		})
	}))
	defer embeddingServer.Close()

	settings := productionSettingsStore{router: rt.DBRouter}
	if _, err := settings.SaveSettings(ctx, httptransport.SettingsDTO{
		LLMProfiles: []httptransport.SettingsLLMProfileDTO{{
			ProfileID:     "embedding",
			DisplayName:   "Embedding",
			Provider:      "openai",
			BaseURL:       embeddingServer.URL + "/v1",
			APIKeyRef:     "secret://llm/embedding",
			Model:         "test-embedding",
			Enabled:       true,
			GlobalDefault: true,
		}},
	}); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	handler, cleanup, err := newProductionHandlerFromRuntime(context.Background(), rt)
	if err != nil {
		t.Fatalf("new production handler: %v", err)
	}
	t.Cleanup(cleanup)

	createBase := httptest.NewRequest(http.MethodPost, "/api/knowledge-bases", strings.NewReader(`{"name":"Product","owner_type":"organization","owner_id":"org-1","visibility":"public","embedding_profile_ref":"embedding","embedding_model":"test-embedding","embedding_dimensions":2,"chunk_policy":{"max_runes":120,"overlap_runes":0,"min_runes":1}}`))
	createBase.Header.Set("X-User-ID", "operator")
	createBase.Header.Set("X-Organization-ID", "org-1")
	createBase.Header.Set("X-Permission-Scope", knowledgeadmin.PermissionKnowledgeManage)
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, createBase)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create knowledge base status=%d body=%s", rec.Code, rec.Body.String())
	}
	var base knowledgeadmin.KnowledgeBaseDTO
	if err := json.NewDecoder(rec.Body).Decode(&base); err != nil {
		t.Fatalf("decode base: %v", err)
	}

	var upload bytes.Buffer
	writer := multipart.NewWriter(&upload)
	file, err := writer.CreateFormFile("file", "launch.md")
	if err != nil {
		t.Fatalf("create file: %v", err)
	}
	if _, err := file.Write([]byte("# Launch\n\nmission launch plan and onboarding checklist")); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	uploadReq := httptest.NewRequest(http.MethodPost, "/api/knowledge-bases/"+base.ID+"/documents", &upload)
	uploadReq.Header.Set("Content-Type", writer.FormDataContentType())
	uploadReq.Header.Set("X-User-ID", "operator")
	uploadReq.Header.Set("X-Organization-ID", "org-1")
	uploadReq.Header.Set("X-Permission-Scope", knowledgeadmin.PermissionKnowledgeWrite)
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, uploadReq)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("upload document status=%d body=%s", rec.Code, rec.Body.String())
	}
	var doc knowledgeadmin.KnowledgeDocumentDTO
	if err := json.NewDecoder(rec.Body).Decode(&doc); err != nil {
		t.Fatalf("decode doc: %v", err)
	}

	if err := rt.StartExecutionRunner(ctx, platformKnowledgeTestRunnerConfig()); err != nil {
		t.Fatalf("start execution runner: %v", err)
	}
	waitForKnowledgeDocumentIndexed(t, handler, base.ID, doc.ID)

	search := httptest.NewRequest(http.MethodPost, "/api/knowledge/search", strings.NewReader(`{"query":"mission launch","limit":3}`))
	search.Header.Set("X-User-ID", "operator")
	search.Header.Set("X-Organization-ID", "org-1")
	search.Header.Set("X-Permission-Scope", knowledgeadmin.PermissionKnowledgeRead)
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, search)
	if rec.Code != http.StatusOK {
		t.Fatalf("search status=%d body=%s", rec.Code, rec.Body.String())
	}
	var page knowledgeadmin.KnowledgeSearchPageDTO
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decode search: %v", err)
	}
	if len(page.Results) == 0 || page.Results[0].DocumentID != doc.ID || !strings.Contains(page.Results[0].Snippet, "mission launch") {
		t.Fatalf("search results mismatch: %#v", page.Results)
	}
	events := waitForKnowledgeMetrics(t, rt.Metrics,
		"knowledge_ingest_jobs_total",
		"knowledge_embedding_batch_size",
		"knowledge_search_duration_ms",
		"knowledge_search_results_total",
		"knowledge_vector_upsert_duration_ms",
	)
	assertKnowledgeMetric(t, events, "knowledge_ingest_jobs_total")
	assertKnowledgeMetric(t, events, "knowledge_embedding_batch_size")
	assertKnowledgeMetric(t, events, "knowledge_search_duration_ms")
	assertKnowledgeMetric(t, events, "knowledge_search_results_total")
	assertKnowledgeMetric(t, events, "knowledge_vector_upsert_duration_ms")
}

func moduleTestBase(id core.KnowledgeBaseID, status core.KnowledgeStatus) core.KnowledgeBase {
	return core.KnowledgeBase{
		ID:            id,
		Name:          "Module Test Knowledge",
		OwnerType:     core.OwnerOrganization,
		OwnerID:       "org-1",
		Visibility:    core.VisibilityScoped,
		VectorBackend: core.VectorBackendLanceDB,
		ChunkPolicy:   doccore.ChunkPolicy{MaxRunes: 2400, OverlapRunes: 240, MinRunes: 400},
		Status:        status,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
}

func moduleTestDocument(id core.KnowledgeDocumentID, baseID core.KnowledgeBaseID, status core.DocumentStatus) core.KnowledgeDocument {
	return core.KnowledgeDocument{
		ID:              id,
		KnowledgeBaseID: baseID,
		ArtifactID:      "artifact-" + string(id),
		Filename:        string(id) + ".md",
		ContentType:     "text/markdown",
		SizeBytes:       12,
		Checksum:        "sum-" + string(id),
		Status:          status,
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}
}

func moduleTestJob(id core.KnowledgeJobID, baseID core.KnowledgeBaseID, status core.JobStatus) core.KnowledgeIngestJob {
	return core.KnowledgeIngestJob{
		ID:              id,
		KnowledgeBaseID: baseID,
		Topic:           "knowledge.ingest_document",
		Status:          status,
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}
}

func platformKnowledgeTestRunnerConfig() platform.ExecutionRunnerRuntimeConfig {
	return platform.ExecutionRunnerRuntimeConfig{WorkerID: "knowledge-test-worker", PollInterval: time.Millisecond}
}

func waitForKnowledgeDocumentIndexed(t *testing.T, handler http.Handler, knowledgeBaseID, documentID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		req := httptest.NewRequest(http.MethodGet, "/api/knowledge-bases/"+knowledgeBaseID+"/documents", nil)
		req.Header.Set("X-User-ID", "operator")
		req.Header.Set("X-Organization-ID", "org-1")
		req.Header.Set("X-Permission-Scope", knowledgeadmin.PermissionKnowledgeRead)
		rec := httptest.NewRecorder()
		serveServerTestHTTP(t, handler, rec, req)
		if rec.Code == http.StatusOK {
			var docs knowledgeadmin.KnowledgeDocumentPageDTO
			if err := json.NewDecoder(rec.Body).Decode(&docs); err == nil {
				for _, doc := range docs.Items {
					if doc.ID == documentID && doc.Status == string(core.DocumentIndexed) {
						return
					}
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("document %s was not indexed before deadline", documentID)
}

func testEmbedding(input string) []float32 {
	input = strings.ToLower(input)
	if strings.Contains(input, "mission") || strings.Contains(input, "launch") {
		return []float32{1, 0}
	}
	return []float32{0, 1}
}

func assertKnowledgeMetric(t *testing.T, events []metrics.Event, name string) {
	t.Helper()
	for _, event := range events {
		if event.Name == name {
			return
		}
	}
	t.Fatalf("metric %s not recorded in %#v", name, events)
}

func waitForKnowledgeMetrics(t *testing.T, sink interface{ Drain(int) []metrics.Event }, names ...string) []metrics.Event {
	t.Helper()
	want := make(map[string]struct{}, len(names))
	for _, name := range names {
		want[name] = struct{}{}
	}
	seen := make(map[string]struct{}, len(names))
	events := make([]metrics.Event, 0, len(names))
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, event := range sink.Drain(20) {
			events = append(events, event)
			if _, ok := want[event.Name]; ok {
				seen[event.Name] = struct{}{}
			}
		}
		if len(seen) == len(want) {
			return events
		}
		time.Sleep(20 * time.Millisecond)
	}
	missing := make([]string, 0, len(want)-len(seen))
	for name := range want {
		if _, ok := seen[name]; !ok {
			missing = append(missing, name)
		}
	}
	t.Fatalf("metrics %v not recorded in %#v", missing, events)
	return nil
}

func writeKnowledgeModuleRuntimeYAML(t *testing.T, knowledgeRoot string) string {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "orrery.yaml")
	raw := `
environment: test
server:
  trusted_viewer_headers_enabled: true
paths:
  base_dir: ` + root + `
database:
  urls:
    core: file://data/core.db
    audit: file://data/audit.db
    events: file://data/events.db
    jobs: file://data/jobs.db
    runtime: file://data/runtime.db
    observability: file://data/observability.db
filestore:
  backend: local
  root: artifacts
logging:
  console: false
  file: logs/platform.jsonl
health:
  readiness_cache_ttl: 1s
knowledge:
  root: ` + knowledgeRoot + `
  vector_backend: lancedb
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write knowledge runtime yaml: %v", err)
	}
	return path
}
