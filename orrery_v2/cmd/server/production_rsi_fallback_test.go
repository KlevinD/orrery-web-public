package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"orrery_v2/internal/app/resourceadmin"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/logging"
)

func waitRSIDiagnosis(t *testing.T, ctx context.Context, store *resourceadmin.PlatformRSILearningSampleStore, sample resourceadmin.RSILearningObservation) resourceadmin.RSILearningObservation {
	t.Helper()
	for {
		got, err := store.Get(ctx, sample.Sample.Scope, sample.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Diagnosis != nil && got.Diagnosis.State != "running" {
			return got
		}
		select {
		case <-ctx.Done():
			t.Fatal("automatic diagnosis did not finish")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func fallbackRuntime(t *testing.T, profile map[string]any) (*platform.Runtime, *bytes.Buffer) {
	t.Helper()
	ctx := context.Background()
	registry, err := db.OpenRegistryFromURLs(ctx, map[string]string{db.RoleCore: "memory://" + t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { registry.Close() })
	var logs bytes.Buffer
	rt := &platform.Runtime{Databases: registry, DBRouter: db.NewRouter(registry), Logger: logging.NewJSONLogger(&logs, logging.LevelInfo)}
	raw, _ := json.Marshal(map[string]any{"llm_profiles": []any{profile}})
	if err := rt.DBRouter.PutRecord(ctx, productionSettingsTable, "default", db.Record{"payload": string(raw)}); err != nil {
		t.Fatal(err)
	}
	return rt, &logs
}

func TestProductionRSIAutomaticFallbackAfterLayaFailure(t *testing.T) {
	var modelCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "systemone") {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		modelCalls.Add(1)
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(raw), "FULL_SOURCE_TAIL") {
			t.Error("fallback did not read retained evidence")
		}
		io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"content":"{\"classification\":\"skill_defect\",\"reason\":\"PRIVATE_MODEL_REASON\"}"}}]}`)
	}))
	defer server.Close()
	key := filepath.Join(t.TempDir(), "laya.key")
	if err := os.WriteFile(key, []byte("local-test-key"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ORRERY_DECISION_MODE", "shadow")
	t.Setenv("ORRERY_DECISION_PROFILES_FILE", "")
	t.Setenv("ORRERY_LAYA_API_KEY_FILE", key)
	t.Setenv("ORRERY_LAYA_URL", server.URL)
	t.Setenv("ORRERY_RSI_LLM_FALLBACK", "on")
	t.Setenv("ORRERY_RSI_LLM_FALLBACK_MAX_CALLS_PER_HOUR", "2")
	t.Setenv("ORRERY_RSI_LLM_FALLBACK_PROFILE", "")
	rt, logs := fallbackRuntime(t, map[string]any{"profile_id": "test", "provider": "custom_openai", "model": "diagnostic-model", "base_url": server.URL + "/v1", "api_key": "PRIVATE_KEY", "enabled": true, "global_default": true})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	observer, closeObserver, err := newProductionDecisionShadow(ctx, rt)
	if err != nil {
		t.Fatal(err)
	}
	defer closeObserver()
	samples, _ := productionRSISampleStore(rt)
	scope, _ := resourceadmin.RSILearningScope("tenant", "agent")
	o, err := samples.Save(ctx, resourceadmin.RSILearningSample{Scope: scope, EvidenceRef: "project:failure", Tool: "normalize", ErrorCode: "contract_mismatch", Summary: "Order state alias rejected", Evidence: json.RawMessage(`{"source":"FULL_SOURCE_TAIL"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !observer.Submit(ctx, resourceadmin.BuildDecisionRSISampleJob(o.Sample)) {
		t.Fatal("primary rejected")
	}
	got := waitRSIDiagnosis(t, ctx, samples, o)
	if got.Diagnosis.State != "completed" || got.Assessment.Adapter != "laya" || got.Assessment.Fallback != "inference_unavailable" || resourceadmin.LearningAdvice(got).AssessmentSource != "llm_fallback" || modelCalls.Load() != 1 {
		t.Fatalf("automatic fallback failed: %+v", got)
	}
	closeObserver()
	if strings.Contains(logs.String(), "PRIVATE_") || strings.Contains(logs.String(), "FULL_SOURCE_TAIL") {
		t.Fatal("private evidence leaked into logs")
	}
}

// Uses the actual order-project failure retained by the previous experiment.
// Only an isolated database is written; there is no task/action executor.
func TestRSIFallbackLiveCanary(t *testing.T) {
	inputFile, keyFile := os.Getenv("ORRERY_RSI_FALLBACK_CANARY_INPUT"), os.Getenv("ORRERY_DECISION_CANARY_KEY_FILE")
	if inputFile == "" || keyFile == "" {
		t.Skip("explicit live model credentials and project evidence required")
	}
	input, err := os.ReadFile(inputFile)
	if err != nil {
		t.Fatal("read project evidence failed")
	}
	var samples []resourceadmin.RSILearningSample
	if json.Unmarshal(input, &samples) != nil || len(samples) < 1 {
		t.Fatal("invalid project sample")
	}
	key, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal("read model credential failed")
	}
	t.Setenv("ORRERY_DECISION_MODE", "shadow")
	t.Setenv("ORRERY_DECISION_PROFILES_FILE", "")
	t.Setenv("ORRERY_LAYA_API_KEY_FILE", os.Getenv("ORRERY_LAYA_CANARY_KEY_FILE"))
	t.Setenv("ORRERY_LAYA_URL", "http://127.0.0.1:18490")
	t.Setenv("ORRERY_RSI_LLM_FALLBACK", "on")
	t.Setenv("ORRERY_RSI_LLM_FALLBACK_MAX_CALLS_PER_HOUR", "2")
	t.Setenv("ORRERY_RSI_LLM_FALLBACK_PROFILE", "")
	rt, _ := fallbackRuntime(t, map[string]any{"profile_id": "live-project-default", "provider": os.Getenv("ORRERY_DECISION_CANARY_PROVIDER"), "model": os.Getenv("ORRERY_DECISION_CANARY_MODEL"), "base_url": os.Getenv("ORRERY_DECISION_CANARY_BASE_URL"), "api_key": strings.TrimSpace(string(key)), "enabled": true, "global_default": true})
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()
	observer, closeObserver, err := newProductionDecisionShadow(ctx, rt)
	if err != nil {
		t.Fatal(err)
	}
	defer closeObserver()
	store, _ := productionRSISampleStore(rt)
	sample := samples[0]
	sample.Scope, _ = resourceadmin.RSILearningScope("synthetic-fallback", "project-agent")
	sample.EvidenceRef = `["runtime_tool","fallback-project","fulfilled"]`
	// Stored RawMessage values are compacted and HTML-escaped by json.Marshal;
	// compare the full canonical JSON rather than its original whitespace.
	expectedEvidence, err := json.Marshal(sample.Evidence)
	if err != nil {
		t.Fatal("invalid retained project evidence")
	}
	o, err := store.Save(ctx, sample)
	if err != nil {
		t.Fatal(err)
	}
	if !observer.Submit(ctx, resourceadmin.BuildDecisionRSISampleJob(sample)) {
		t.Fatal("primary rejected")
	}
	got := waitRSIDiagnosis(t, ctx, store, o)
	if got.Diagnosis.State != "completed" || got.Diagnosis.Model.Adapter != "llm" || got.Diagnosis.Model.Model != os.Getenv("ORRERY_DECISION_CANARY_MODEL") {
		t.Fatalf("live fallback state=%s failure=%s model=%s", got.Diagnosis.State, got.Diagnosis.Failure, got.Diagnosis.Model.Model)
	}
	if string(got.Sample.Evidence) != string(expectedEvidence) {
		t.Fatal("retained evidence content changed")
	}
	advice := resourceadmin.LearningAdvice(got)
	primaryChoice, probability := "", 0.0
	if os.Getenv("ORRERY_RSI_MODE") == "llm" && (got.Diagnosis.Trigger != "llm_primary" || advice.AssessmentSource != "llm_primary" || len(got.Assessment.Decisions) != 0) {
		t.Fatal("live canary did not use full-evidence LLM primary routing")
	}
	if len(got.Assessment.Decisions) == 1 {
		primaryChoice, probability = got.Assessment.Decisions[0].Choice, got.Assessment.Decisions[0].AnswerConfidence
	}
	t.Logf("primary_model=%s primary_choice=%s primary_probability=%.4f trigger=%s fallback_model=%s diagnosis=%s next_step=%s original_evidence_retained=true advisory_only=true", got.Assessment.ModelRevision, primaryChoice, probability, got.Diagnosis.Trigger, got.Diagnosis.Model.Model, got.Diagnosis.Result.Classification, advice.NextStep)
	closeObserver()
	// Reconstructing the production worker must reuse the durable attempt.
	observer, closeAgain, err := newProductionDecisionShadow(ctx, rt)
	if err != nil {
		t.Fatal(err)
	}
	defer closeAgain()
	observer.Submit(ctx, resourceadmin.BuildDecisionRSISampleJob(sample))
	for {
		reopened, err := store.Get(ctx, sample.Scope, o.ID)
		if err != nil {
			t.Fatal(err)
		}
		if reopened.Assessment != nil && reopened.Assessment.At.After(got.Assessment.At) {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("restarted primary did not finish")
		case <-time.After(10 * time.Millisecond):
		}
	}
	closeAgain()
	reopened, err := store.Get(ctx, sample.Scope, o.ID)
	if err != nil || !reopened.Diagnosis.CompletedAt.Equal(got.Diagnosis.CompletedAt) {
		t.Fatal("restart repeated the model diagnosis")
	}
}
