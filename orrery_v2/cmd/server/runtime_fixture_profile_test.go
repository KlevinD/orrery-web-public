package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	httptransport "orrery_v2/internal/transport/http"
)

// Configure only tests that intentionally execute a local CLI fixture. Other
// handler tests retain an empty model configuration and its production errors.
func configureRuntimeFixtureLLMProfile(t *testing.T, handler http.Handler, model string) {
	t.Helper()
	t.Setenv("ORRERY_SECRET_LLM_FIXTURE", "fixture-test-value")
	payload, err := json.Marshal(httptransport.SettingsDTO{
		PlatformName:     "Orrery Production",
		OrganizationName: "Acme Production",
		Language:         "zh-CN",
		LLMProfiles: []httptransport.SettingsLLMProfileDTO{{
			ProfileID: "llm-fixture", DisplayName: "Local CLI Fixture",
			Provider: "openai", BaseURL: "http://127.0.0.1:1/v1",
			APIKeyRef: "secret://llm/fixture", Model: model,
			Enabled: true, GlobalDefault: true,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", "superadmin")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Permission-Scope", "settings.manage")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("configure local runtime fixture model: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestProductionDiscussionRequiresEnabledLLMProfileBeforeCLIFixture(t *testing.T) {
	root := t.TempDir()
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", productionTestDatabaseURLs(root))
	setTestConfigOverride(t, "discussion.agent.runtime_kind", "codex")
	executable := writeCodexTextRuntimeFixture(t)
	setTestConfigOverride(t, "discussion.agent.runtime_config", `{"executable":"`+jsonPath(executable)+`"}`)
	selectTestServerConfig(t)
	handler, cleanup, err := newServerHandler(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	req := httptest.NewRequest(http.MethodPost, "/api/discussions/messages", jsonBody(`{"content":"Plan a launch"}`))
	req.Header.Set("X-User-ID", "superadmin")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("Idempotency-Key", "missing-fixture-model-profile")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, req)
	if rec.Code != http.StatusServiceUnavailable ||
		!strings.Contains(rec.Body.String(), `"code":"runtime_unavailable"`) ||
		!strings.Contains(rec.Body.String(), "no enabled llm profile is configured") {
		t.Fatalf("missing profile must remain unavailable: status=%d body=%s", rec.Code, rec.Body.String())
	}
}
