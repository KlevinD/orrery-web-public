package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"orrery_v2/internal/app/resourceadmin"
)

func TestProductionRSILLMPrimaryUsesFullEvidenceWithoutLaya(t *testing.T) {
	var modelCalls, layaCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "systemone") {
			layaCalls.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		modelCalls.Add(1)
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(raw), "FULL_SOURCE_TAIL") {
			t.Error("primary diagnosis did not read retained evidence")
		}
		io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"content":"{\"classification\":\"skill_defect\",\"reason\":\"PRIVATE_MODEL_REASON\"}"}}]}`)
	}))
	defer server.Close()
	t.Setenv("ORRERY_DECISION_MODE", "shadow")
	t.Setenv("ORRERY_DECISION_PROFILES_FILE", "")
	t.Setenv("ORRERY_LAYA_API_KEY_FILE", "") // Laya is neither resolved nor called.
	t.Setenv("ORRERY_LAYA_URL", server.URL)
	t.Setenv("ORRERY_RSI_MODE", "llm")
	t.Setenv("ORRERY_RSI_LLM_FALLBACK", "off") // Primary diagnosis is independent.
	t.Setenv("ORRERY_RSI_LLM_FALLBACK_MAX_CALLS_PER_HOUR", "1")
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
	sample := resourceadmin.RSILearningSample{Scope: scope, EvidenceRef: "project:primary", Tool: "normalize", ErrorCode: "contract_mismatch", Summary: strings.Repeat("long evidence summary ", 20), Evidence: json.RawMessage(`{"source":"FULL_SOURCE_TAIL"}`)}
	o, err := samples.Save(ctx, sample)
	if err != nil {
		t.Fatal(err)
	}
	job := resourceadmin.BuildDecisionRSISampleJob(o.Sample)
	if !observer.Submit(ctx, job) {
		t.Fatal("primary rejected")
	}
	got := waitRSIDiagnosis(t, ctx, samples, o)
	advice := resourceadmin.LearningAdvice(got)
	if got.Diagnosis.State != "completed" || got.Diagnosis.Trigger != "llm_primary" || advice.AssessmentSource != "llm_primary" || advice.NextStep != "reproduce_skill" || modelCalls.Load() != 1 || layaCalls.Load() != 0 {
		t.Fatalf("primary diagnosis failed: %+v, calls=%d, laya=%d", got, modelCalls.Load(), layaCalls.Load())
	}
	if !observer.Submit(ctx, job) {
		t.Fatal("duplicate rejected")
	}
	// Wait for the duplicate to traverse the observer, then drain the diagnosis
	// queue with a second observation. The durable budget must reject its call.
	sample.EvidenceRef = "project:budget"
	second, err := samples.Save(ctx, sample)
	if err != nil {
		t.Fatal(err)
	}
	if !observer.Submit(ctx, resourceadmin.BuildDecisionRSISampleJob(second.Sample)) {
		t.Fatal("budget sample rejected")
	}
	if budget := waitRSIDiagnosis(t, ctx, samples, second); budget.Diagnosis.State != "budget_exhausted" {
		t.Fatalf("primary bypassed durable budget: %+v", budget)
	}
	closeObserver()
	if modelCalls.Load() != 1 || layaCalls.Load() != 0 {
		t.Fatal("duplicate or budget produced another model call")
	}
	if strings.Contains(logs.String(), "PRIVATE_") || strings.Contains(logs.String(), "FULL_SOURCE_TAIL") {
		t.Fatal("private evidence leaked into logs")
	}
}

func TestProductionRSILLMPrimaryRejectsInvalidConfiguration(t *testing.T) {
	for _, tc := range []struct{ mode, decision string }{{"unknown", "shadow"}, {"llm", "off"}} {
		t.Run(tc.mode+tc.decision, func(t *testing.T) {
			t.Setenv("ORRERY_RSI_MODE", tc.mode)
			t.Setenv("ORRERY_DECISION_MODE", tc.decision)
			_, closeObserver, err := newProductionDecisionShadow(context.Background(), nil)
			closeObserver()
			if err == nil {
				t.Fatal("invalid configuration was silently ignored")
			}
		})
	}
}
