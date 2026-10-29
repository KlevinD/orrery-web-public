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
	"testing"
	"time"

	"orrery_v2/internal/agent/runtime/llmprofile"
	"orrery_v2/internal/app/resourceadmin"
	"orrery_v2/internal/decision"
	decisionllm "orrery_v2/internal/decision/llm"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/logging"
)

func TestProductionDecisionProfilesUseLLMWithoutLayaAndPersistModel(t *testing.T) {
	ctx := context.Background()
	registry, err := db.OpenRegistryFromURLs(ctx, map[string]string{db.RoleCore: "memory://" + t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	var logs bytes.Buffer
	rt := &platform.Runtime{Databases: registry, DBRouter: db.NewRouter(registry), Logger: logging.NewJSONLogger(&logs, logging.LevelInfo)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Error(r.URL.Path)
		}
		io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"content":"{\"decisions\":[{\"choice\":\"environment\",\"confidence\":0.7,\"answer_confidence\":0.8}]}"}}]}`)
	}))
	defer server.Close()
	settings := map[string]any{"llm_profiles": []map[string]any{{"profile_id": "test-llm", "provider": "custom_openai", "model": "test-model", "base_url": server.URL + "/v1", "api_key": "test-key", "enabled": true, "global_default": true}}}
	payload, _ := json.Marshal(settings)
	if err := rt.DBRouter.PutRecord(ctx, productionSettingsTable, "default", db.Record{"payload": string(payload)}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "profiles.json")
	c := decision.Config{DefaultProfileID: "llm-choice", Profiles: []decision.Profile{{ProfileID: "llm-choice", Adapter: "llm", Enabled: true, LLMProfileID: "platform-default", TimeoutMS: 1000}}}
	raw, _ := json.Marshal(c)
	os.WriteFile(path, raw, 0600)
	t.Setenv("ORRERY_DECISION_MODE", "shadow")
	t.Setenv("ORRERY_DECISION_PROFILES_FILE", path)
	t.Setenv("ORRERY_LAYA_API_KEY_FILE", "")
	o, closeObserver, err := newProductionDecisionShadow(ctx, rt)
	if err != nil {
		t.Fatal(err)
	}
	defer closeObserver()
	store, err := productionRSISampleStore(rt)
	if err != nil {
		t.Fatal(err)
	}
	scope, _ := resourceadmin.RSILearningScope("tenant", "agent")
	observation, err := store.Save(ctx, resourceadmin.RSILearningSample{Scope: scope, EvidenceRef: "run:call", Tool: "lookup", ErrorCode: "timeout", Summary: "network timed out", Evidence: json.RawMessage(`{"private":"RETAIN_IN_STORAGE"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !o.Submit(ctx, resourceadmin.BuildDecisionRSISampleJob(observation.Sample)) {
		t.Fatal("job rejected")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		got, err := store.Get(ctx, scope, observation.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Assessment != nil {
			r := got.Assessment
			if r.Fallback != "" || r.Adapter != "llm" || r.ModelRevision != "test-model" || r.ProfileID != "llm-choice" || r.LLMProfileID != "test-llm" || r.Decisions[0].Choice != "environment" {
				t.Fatalf("incorrect assessment %+v", r)
			}
			if strings.Contains(logs.String(), "test-key") || strings.Contains(logs.String(), "RETAIN_IN_STORAGE") {
				t.Fatal("private data leaked")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no assessment persisted; logs: %s", logs.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestProductionDecisionModeOverridesLegacyMode(t *testing.T) {
	t.Setenv("ORRERY_LAYA_MODE", "shadow")
	t.Setenv("ORRERY_DECISION_MODE", "off")
	o, close, err := newProductionDecisionShadow(context.Background(), nil)
	defer close()
	if err != nil || o != nil {
		t.Fatal("explicit off did not override legacy mode")
	}
}

// This probe uses synthetic evidence and no production task or message channel.
func TestDecisionModelsLiveLLMCanary(t *testing.T) {
	keyFile := os.Getenv("ORRERY_DECISION_CANARY_KEY_FILE")
	if keyFile == "" {
		t.Skip("explicit live model credentials required")
	}
	key, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal("cannot read canary credential")
	}
	profile := llmprofile.LLMProfile{ProfileID: "canary", Enabled: true, GlobalDefault: true, Provider: os.Getenv("ORRERY_DECISION_CANARY_PROVIDER"), Model: os.Getenv("ORRERY_DECISION_CANARY_MODEL"), BaseURL: os.Getenv("ORRERY_DECISION_CANARY_BASE_URL"), APIKey: strings.TrimSpace(string(key))}
	a := decisionllm.Adapter{Profiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{profile}}}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	r, err := a.Resolve(ctx, decision.Profile{})
	if err != nil {
		t.Fatal(err)
	}
	q := decision.Question{Type: "choice", Instructions: "Classify the observed failure.", Criteria: map[string]string{"environment": "network or infrastructure failure", "tool_usage": "invalid arguments or wrong tool"}}
	result, err := r.Judge.Choose(ctx, []string{"Network request timed out before receiving a response."}, q)
	if err != nil || len(result) != 1 {
		t.Fatalf("live decision failed: %v", err)
	}
	if result[0].Choice != "environment" {
		t.Fatalf("unexpected synthetic classification %s", result[0].Choice)
	}
	t.Logf("live choice verified: model=%s provider=%s choice=%s confidence_kind=%s", r.Metadata.Model, r.Metadata.Provider, result[0].Choice, r.Metadata.ConfidenceKind)
}
