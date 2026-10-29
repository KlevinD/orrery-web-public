package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"orrery_v2/internal/app/runtimeapproval"
)

type settingsSecretResolverFunc func(context.Context, string) (string, error)

func (f settingsSecretResolverFunc) ResolveSecret(ctx context.Context, ref string) (string, error) {
	return f(ctx, ref)
}

func TestSettingsRoutesReadAndWritePreferences(t *testing.T) {
	router := newTestRouter(RouterOptions{})

	get := httptest.NewRecorder()
	router.ServeHTTP(get, authedHTTPTestRequest(http.MethodGet, "/api/settings", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("GET /api/settings status = %d body=%s", get.Code, get.Body.String())
	}
	if !strings.Contains(get.Body.String(), `"platform_name":"Orrery Mission Control"`) {
		t.Fatalf("GET /api/settings missing platform defaults: %s", get.Body.String())
	}
	if !strings.Contains(get.Body.String(), `"rule_id":"approval"`) {
		t.Fatalf("GET /api/settings missing notification defaults: %s", get.Body.String())
	}
	if !strings.Contains(get.Body.String(), `"discussion_agent_pair_loop_limit":8`) {
		t.Fatalf("GET /api/settings missing discussion mention limit default: %s", get.Body.String())
	}
	if !strings.Contains(get.Body.String(), `"memory":{"enabled":false}`) {
		t.Fatalf("GET /api/settings missing disabled memory default: %s", get.Body.String())
	}
	if !strings.Contains(get.Body.String(), `"knowledge"`) || !strings.Contains(get.Body.String(), `"default_provider":"pageindex"`) || !strings.Contains(get.Body.String(), `"api_url":"https://api.pageindex.ai"`) {
		t.Fatalf("GET /api/settings missing knowledge provider defaults: %s", get.Body.String())
	}
	if !strings.Contains(get.Body.String(), `"prompt_blocks"`) || !strings.Contains(get.Body.String(), `"block_id":"system_principles"`) {
		t.Fatalf("GET /api/settings missing prompt block defaults: %s", get.Body.String())
	}
	var getSettings SettingsDTO
	if err := json.NewDecoder(strings.NewReader(get.Body.String())).Decode(&getSettings); err != nil {
		t.Fatalf("decode GET /api/settings: %v", err)
	}
	if len(getSettings.RuntimeProfiles) != 1 ||
		getSettings.RuntimeProfiles[0].RuntimeKind != "pi" ||
		getSettings.RuntimeProfiles[0].RuntimeConfig["approval_mode"] != "on-request" ||
		getSettings.RuntimeProfiles[0].RuntimeConfig["approval_timeout_ms"] != float64(600000) ||
		getSettings.RuntimeProfiles[0].RuntimeConfig["prompt_ack_timeout_ms"] != float64(60000) {
		t.Fatalf("GET /api/settings missing default PI runtime profile: %+v", getSettings.RuntimeProfiles)
	}
	workspaceInfoFound := false
	for _, block := range getSettings.PromptBlocks {
		if block.BlockID == "workspace_info" {
			workspaceInfoFound = block.Content == "<<workspace_info>>" && block.Role == "user" && block.Editable && !block.Dynamic
			break
		}
	}
	if !workspaceInfoFound {
		t.Fatalf("GET /api/settings missing workspace info prompt block default: %+v", getSettings.PromptBlocks)
	}
	for _, unexpected := range []string{
		`"block_id":"agent_profile"`,
		`"block_id":"agent_id"`,
		`"block_id":"agent_core_prompt"`,
		`"block_id":"agent_behavior_boundary"`,
		`"block_id":"runtime_system_prompt"`,
	} {
		if strings.Contains(get.Body.String(), unexpected) {
			t.Fatalf("GET /api/settings should expose agent data as variables, not default blocks %s: %s", unexpected, get.Body.String())
		}
	}
	if strings.Contains(get.Body.String(), `"integrations"`) || strings.Contains(get.Body.String(), `"integration_id"`) {
		t.Fatalf("GET /api/settings must not expose integration placeholders: %s", get.Body.String())
	}

	put := httptest.NewRecorder()
	body := strings.NewReader(`{"platform_name":"Orrery","organization_name":"Example","language":"en-US","auto_create_agent_profile":false,"auto_link_evidence":true,"hil_default_enabled":true,"discussion_agent_pair_loop_limit":3,"memory":{"enabled":true},"notification_rules":[{"rule_id":"approval","label_key":"settings.notification.approval.label","description_key":"settings.notification.approval.desc","enabled":false}]}`)
	router.ServeHTTP(put, authedHTTPTestRequest(http.MethodPut, "/api/settings", body))
	if put.Code != http.StatusOK {
		t.Fatalf("PUT /api/settings status = %d body=%s", put.Code, put.Body.String())
	}
	if !strings.Contains(put.Body.String(), `"language":"en-US"`) || !strings.Contains(put.Body.String(), `"organization_name":"Example"`) || !strings.Contains(put.Body.String(), `"discussion_agent_pair_loop_limit":3`) || !strings.Contains(put.Body.String(), `"memory":{"enabled":true}`) || !strings.Contains(put.Body.String(), `"runtime_kind":"pi"`) || strings.Contains(put.Body.String(), `"integrations"`) {
		t.Fatalf("PUT /api/settings did not echo preferences: %s", put.Body.String())
	}

	getAfterPut := httptest.NewRecorder()
	router.ServeHTTP(getAfterPut, authedHTTPTestRequest(http.MethodGet, "/api/settings", nil))
	if getAfterPut.Code != http.StatusOK {
		t.Fatalf("GET /api/settings after PUT status = %d body=%s", getAfterPut.Code, getAfterPut.Body.String())
	}
	if !strings.Contains(getAfterPut.Body.String(), `"platform_name":"Orrery"`) || !strings.Contains(getAfterPut.Body.String(), `"language":"en-US"`) || !strings.Contains(getAfterPut.Body.String(), `"discussion_agent_pair_loop_limit":3`) || !strings.Contains(getAfterPut.Body.String(), `"memory":{"enabled":true}`) || !strings.Contains(getAfterPut.Body.String(), `"runtime_kind":"pi"`) || strings.Contains(getAfterPut.Body.String(), `"integration_id"`) {
		t.Fatalf("GET /api/settings after PUT did not persist preferences: %s", getAfterPut.Body.String())
	}
}

func TestSettingsRoutesReadAndWriteKnowledgeSettings(t *testing.T) {
	router := newTestRouter(RouterOptions{})

	body := strings.NewReader(`{"knowledge":{"enabled":true,"default_provider":"pageindex","pageindex":{"api_url":" https://api.pageindex.ai/ ","api_key_ref":"secret://knowledge/pageindex","default_folder_id":"folder-123","timeout_seconds":45},"local":{"enabled":false}}}`)
	put := httptest.NewRecorder()
	router.ServeHTTP(put, authedHTTPTestRequest(http.MethodPut, "/api/settings", body))
	if put.Code != http.StatusOK {
		t.Fatalf("PUT /api/settings status = %d body=%s", put.Code, put.Body.String())
	}
	var saved SettingsDTO
	if err := json.NewDecoder(put.Body).Decode(&saved); err != nil {
		t.Fatalf("decode PUT /api/settings: %v", err)
	}
	if !saved.Knowledge.Enabled ||
		saved.Knowledge.DefaultProvider != "pageindex" ||
		saved.Knowledge.PageIndex.APIURL != "https://api.pageindex.ai" ||
		saved.Knowledge.PageIndex.APIKeyRef != "secret://knowledge/pageindex" ||
		saved.Knowledge.PageIndex.DefaultFolderID != "folder-123" ||
		saved.Knowledge.PageIndex.TimeoutSeconds != 45 ||
		saved.Knowledge.Local.Enabled {
		t.Fatalf("knowledge settings not normalized: %+v", saved.Knowledge)
	}

	get := httptest.NewRecorder()
	router.ServeHTTP(get, authedHTTPTestRequest(http.MethodGet, "/api/settings", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("GET /api/settings status = %d body=%s", get.Code, get.Body.String())
	}
	if !strings.Contains(get.Body.String(), `"api_key_ref":"secret://knowledge/pageindex"`) || !strings.Contains(get.Body.String(), `"default_folder_id":"folder-123"`) {
		t.Fatalf("GET /api/settings after PUT missing knowledge settings: %s", get.Body.String())
	}
}

func TestSettingsRoutesDoNotExposePageIndexAPIKeyAndRemoveItOnUpdate(t *testing.T) {
	initial := DefaultSettings()
	initial.Knowledge.PageIndex.APIKey = "pageindex-root-secret"
	settings := newMemorySettingsService(initial)
	router := newTestRouter(RouterOptions{Settings: settings})

	get := httptest.NewRecorder()
	router.ServeHTTP(get, authedHTTPTestRequest(http.MethodGet, "/api/settings", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("GET /api/settings status = %d body=%s", get.Code, get.Body.String())
	}
	if strings.Contains(get.Body.String(), "pageindex-root-secret") || strings.Contains(get.Body.String(), `"api_key":`) {
		t.Fatalf("GET /api/settings exposed PageIndex API key: %s", get.Body.String())
	}

	put := httptest.NewRecorder()
	router.ServeHTTP(put, authedHTTPTestRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"language":"en-US","knowledge":{"pageindex":{"api_key":""}}}`)))
	if put.Code != http.StatusOK {
		t.Fatalf("PUT /api/settings status = %d body=%s", put.Code, put.Body.String())
	}
	if strings.Contains(put.Body.String(), "pageindex-root-secret") || strings.Contains(put.Body.String(), `"api_key":`) {
		t.Fatalf("PUT /api/settings exposed PageIndex API key: %s", put.Body.String())
	}

	stored, err := settings.GetSettings(context.Background())
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if stored.Knowledge.PageIndex.APIKey != "" {
		t.Fatalf("settings update retained legacy PageIndex API key: %q", stored.Knowledge.PageIndex.APIKey)
	}

	switchToRef := httptest.NewRecorder()
	router.ServeHTTP(switchToRef, authedHTTPTestRequest(
		http.MethodPut,
		"/api/settings",
		strings.NewReader(`{"knowledge":{"pageindex":{"api_key":"","api_key_ref":"secret://knowledge/pageindex"}}}`),
	))
	if switchToRef.Code != http.StatusOK {
		t.Fatalf("PUT /api/settings with new API key ref status = %d body=%s", switchToRef.Code, switchToRef.Body.String())
	}
	stored, err = settings.GetSettings(context.Background())
	if err != nil {
		t.Fatalf("GetSettings after API key ref update: %v", err)
	}
	if stored.Knowledge.PageIndex.APIKey != "" || stored.Knowledge.PageIndex.APIKeyRef != "secret://knowledge/pageindex" {
		t.Fatalf("switching to API key ref retained direct key: %+v", stored.Knowledge.PageIndex)
	}
}

func TestSettingsRoutesRejectDirectPageIndexAPIKeyPersistence(t *testing.T) {
	router := newTestRouter(RouterOptions{})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, authedHTTPTestRequest(
		http.MethodPut,
		"/api/settings",
		strings.NewReader(`{"knowledge":{"pageindex":{"api_url":"https://pageindex.example","api_key":"pageindex-root-secret"}}}`),
	))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT /api/settings status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "knowledge.pageindex.api_key") {
		t.Fatalf("PUT /api/settings response missing plaintext secret field: %s", rec.Body.String())
	}
}

func TestSettingsRoutesClearDirectPageIndexKeyWhenRefOrURLChangesWithoutAPIKeyField(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "key ref",
			body: `{"knowledge":{"pageindex":{"api_key_ref":"secret://knowledge/pageindex-next"}}}`,
		},
		{
			name: "api url",
			body: `{"knowledge":{"pageindex":{"api_url":"https://pageindex-next.example"}}}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			initial := DefaultSettings()
			initial.Knowledge.PageIndex.APIURL = "https://pageindex.example"
			initial.Knowledge.PageIndex.APIKey = "pageindex-root-secret"
			settings := newMemorySettingsService(initial)
			router := newTestRouter(RouterOptions{Settings: settings})

			put := httptest.NewRecorder()
			router.ServeHTTP(put, authedHTTPTestRequest(http.MethodPut, "/api/settings", strings.NewReader(test.body)))
			if put.Code != http.StatusOK {
				t.Fatalf("PUT /api/settings status = %d body=%s", put.Code, put.Body.String())
			}
			stored, err := settings.GetSettings(context.Background())
			if err != nil {
				t.Fatalf("GetSettings: %v", err)
			}
			if stored.Knowledge.PageIndex.APIKey != "" {
				t.Fatalf("changed PageIndex target retained direct key: %+v", stored.Knowledge.PageIndex)
			}
		})
	}
}

func TestSettingsRoutesConnectionTestReusesStoredPageIndexAPIKey(t *testing.T) {
	pageIndex := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if got := req.Header.Get("Authorization"); got != "Bearer pageindex-root-secret" {
			t.Fatalf("PageIndex probe authorization = %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer pageIndex.Close()

	initial := DefaultSettings()
	initial.Knowledge.PageIndex.APIURL = pageIndex.URL
	initial.Knowledge.PageIndex.APIKey = "pageindex-root-secret"
	router := newTestRouter(RouterOptions{Settings: newMemorySettingsService(initial)})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, authedHTTPTestRequest(
		http.MethodPost,
		"/api/settings/knowledge/pageindex/test",
		strings.NewReader(`{"pageindex":{"api_url":"`+pageIndex.URL+`","api_key":"","timeout_seconds":3}}`),
	))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST PageIndex connection test status = %d body=%s", rec.Code, rec.Body.String())
	}
	var result SettingsPageIndexConnectionTestResultDTO
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode PageIndex connection test result: %v", err)
	}
	if result.Status != "ok" || result.HTTPStatus != http.StatusOK {
		t.Fatalf("PageIndex connection result = %+v", result)
	}
}

func TestSettingsRoutesConnectionTestResolvesNewPageIndexKeyRefForSavedURL(t *testing.T) {
	pageIndex := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if got := req.Header.Get("Authorization"); got != "Bearer resolved-pageindex-secret" {
			t.Fatalf("PageIndex probe authorization = %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer pageIndex.Close()

	initial := DefaultSettings()
	initial.Knowledge.PageIndex.APIURL = pageIndex.URL
	resolver := settingsSecretResolverFunc(func(_ context.Context, ref string) (string, error) {
		if ref != "secret://knowledge/pageindex-next" {
			t.Fatalf("ResolveSecret ref = %q", ref)
		}
		return "resolved-pageindex-secret", nil
	})
	router := newTestRouter(RouterOptions{
		Settings:        newMemorySettingsService(initial),
		SettingsSecrets: resolver,
	})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, authedHTTPTestRequest(
		http.MethodPost,
		"/api/settings/knowledge/pageindex/test",
		strings.NewReader(`{"pageindex":{"api_url":"`+pageIndex.URL+`","api_key_ref":"secret://knowledge/pageindex-next","timeout_seconds":3}}`),
	))

	var result SettingsPageIndexConnectionTestResultDTO
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode PageIndex connection test result: %v", err)
	}
	if rec.Code != http.StatusOK || result.Status != "ok" {
		t.Fatalf("PageIndex connection result status=%d body=%+v", rec.Code, result)
	}
}

func TestSettingsRoutesConnectionTestDoesNotResolveKeyRefForChangedURL(t *testing.T) {
	pageIndex := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if got := req.Header.Get("Authorization"); got != "" {
			t.Fatalf("changed PageIndex URL received resolved authorization: %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer pageIndex.Close()

	initial := DefaultSettings()
	initial.Knowledge.PageIndex.APIURL = "https://pageindex.internal.example"
	resolver := settingsSecretResolverFunc(func(_ context.Context, ref string) (string, error) {
		t.Fatalf("ResolveSecret must not be called for changed URL: %q", ref)
		return "", nil
	})
	router := newTestRouter(RouterOptions{
		Settings:        newMemorySettingsService(initial),
		SettingsSecrets: resolver,
	})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, authedHTTPTestRequest(
		http.MethodPost,
		"/api/settings/knowledge/pageindex/test",
		strings.NewReader(`{"pageindex":{"api_url":"`+pageIndex.URL+`","api_key_ref":"secret://knowledge/pageindex-next","timeout_seconds":3}}`),
	))

	if rec.Code != http.StatusOK {
		t.Fatalf("POST changed PageIndex connection test status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSettingsRoutesConnectionTestDoesNotSendStoredPageIndexAPIKeyToChangedURL(t *testing.T) {
	pageIndex := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if got := req.Header.Get("Authorization"); got != "" {
			t.Fatalf("changed PageIndex URL received stored authorization: %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer pageIndex.Close()

	initial := DefaultSettings()
	initial.Knowledge.PageIndex.APIURL = "https://pageindex.internal.example"
	initial.Knowledge.PageIndex.APIKey = "pageindex-root-secret"
	router := newTestRouter(RouterOptions{Settings: newMemorySettingsService(initial)})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, authedHTTPTestRequest(
		http.MethodPost,
		"/api/settings/knowledge/pageindex/test",
		strings.NewReader(`{"pageindex":{"api_url":"`+pageIndex.URL+`","api_key":"","timeout_seconds":3}}`),
	))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST changed PageIndex connection test status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSettingsRoutesTestPageIndexConnection(t *testing.T) {
	pageIndex := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/healthz" {
			t.Fatalf("PageIndex probe path = %q, want /healthz", req.URL.Path)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer local-dev" {
			t.Fatalf("PageIndex probe authorization = %q", got)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer pageIndex.Close()

	router := newTestRouter(RouterOptions{})
	body, err := json.Marshal(SettingsPageIndexConnectionTestRequestDTO{
		PageIndex: SettingsPageIndexKnowledgeDTO{
			APIURL:         pageIndex.URL + "/",
			APIKey:         "local-dev",
			TimeoutSeconds: 3,
		},
	})
	if err != nil {
		t.Fatalf("marshal PageIndex connection test body: %v", err)
	}

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, authedHTTPTestRequest(http.MethodPost, "/api/settings/knowledge/pageindex/test", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/settings/knowledge/pageindex/test status = %d body=%s", rec.Code, rec.Body.String())
	}
	var result SettingsPageIndexConnectionTestResultDTO
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode PageIndex connection test result: %v", err)
	}
	if result.Status != "ok" || result.APIURL != pageIndex.URL || result.Endpoint != pageIndex.URL+"/healthz" || result.HTTPStatus != http.StatusOK {
		t.Fatalf("PageIndex connection result = %+v", result)
	}
	if result.LatencyMS < 0 {
		t.Fatalf("PageIndex connection latency must not be negative: %+v", result)
	}
}

func TestSettingsRoutesRejectInvalidPageIndexConnectionTest(t *testing.T) {
	router := newTestRouter(RouterOptions{})
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, authedHTTPTestRequest(http.MethodPost, "/api/settings/knowledge/pageindex/test", strings.NewReader(`{"pageindex":{"api_url":"ftp://127.0.0.1:8091","timeout_seconds":30}}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/settings/knowledge/pageindex/test status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"invalid_request"`) || !strings.Contains(rec.Body.String(), "knowledge.pageindex.api_url") {
		t.Fatalf("invalid PageIndex connection test response missing field: %s", rec.Body.String())
	}
}

func TestPageIndexConnectionTestTreatsAuthorizationFailuresAsFailed(t *testing.T) {
	for _, statusCode := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			pageIndex := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(statusCode)
			}))
			defer pageIndex.Close()

			result := testPageIndexConnection(context.Background(), SettingsPageIndexKnowledgeDTO{
				APIURL:         pageIndex.URL,
				APIKey:         "wrong-key",
				TimeoutSeconds: 3,
			})
			if result.Status != "failed" || result.HTTPStatus != statusCode {
				t.Fatalf("PageIndex authorization failure result = %+v", result)
			}
		})
	}
}

func TestSettingsRoutesRejectInvalidKnowledgeSettings(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantField string
	}{
		{name: "provider", body: `{"knowledge":{"default_provider":"mcp"}}`, wantField: "knowledge.default_provider"},
		{name: "pageindex url", body: `{"knowledge":{"default_provider":"pageindex","pageindex":{"api_url":"ftp://api.pageindex.ai","api_key_ref":"secret://knowledge/pageindex"}}}`, wantField: "knowledge.pageindex.api_url"},
		{name: "pageindex api key", body: `{"knowledge":{"default_provider":"pageindex","pageindex":{"api_url":"https://api.pageindex.ai","api_key":"中文"}}}`, wantField: "knowledge.pageindex.api_key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := newTestRouter(RouterOptions{})
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, authedHTTPTestRequest(http.MethodPut, "/api/settings", strings.NewReader(tt.body)))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("PUT /api/settings status = %d body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"code":"invalid_request"`) || !strings.Contains(rec.Body.String(), tt.wantField) {
				t.Fatalf("PUT /api/settings invalid knowledge response missing field %s: %s", tt.wantField, rec.Body.String())
			}
		})
	}
}

func TestSettingsRoutesMigratesLegacyAgentProfilePromptBlock(t *testing.T) {
	router := newTestRouter(RouterOptions{})

	body := strings.NewReader(`{"prompt_blocks":[{"block_id":"system_principles","title":"系统原则","role":"system","content":"平台原则","dynamic":false,"editable":true,"enabled":true},{"block_id":"agent_profile","title":"Agent 角色设定","role":"system","placeholder":"{{agent_profile}}","dynamic":true,"editable":false,"enabled":true},{"block_id":"current_user_input","title":"当前输入","role":"user","placeholder":"{{current_user_input}}","dynamic":true,"editable":false,"enabled":true}]}`)
	put := httptest.NewRecorder()
	router.ServeHTTP(put, authedHTTPTestRequest(http.MethodPut, "/api/settings", body))
	if put.Code != http.StatusOK {
		t.Fatalf("PUT /api/settings status = %d body=%s", put.Code, put.Body.String())
	}
	var saved SettingsDTO
	if err := json.NewDecoder(put.Body).Decode(&saved); err != nil {
		t.Fatalf("decode PUT /api/settings: %v", err)
	}
	ids := map[string]int{}
	for i, block := range saved.PromptBlocks {
		ids[block.BlockID] = i
		if block.BlockID == "agent_profile" {
			t.Fatalf("legacy agent_profile block should not persist: %+v", saved.PromptBlocks)
		}
	}
	for _, want := range []string{"agent_profile_template"} {
		if _, ok := ids[want]; !ok {
			t.Fatalf("migrated prompt blocks missing %q: %+v", want, saved.PromptBlocks)
		}
	}
	if ids["agent_profile_template"] < ids["system_principles"] || ids["agent_profile_template"] > ids["current_user_input"] {
		t.Fatalf("migration should preserve legacy block location: %+v", saved.PromptBlocks)
	}
	if saved.PromptBlocks[ids["agent_profile_template"]].Content == "" ||
		!strings.Contains(saved.PromptBlocks[ids["agent_profile_template"]].Content, "<<agent_display_name>>") ||
		strings.Contains(saved.PromptBlocks[ids["agent_profile_template"]].Content, "<<agent_core_prompt>>") ||
		strings.Contains(saved.PromptBlocks[ids["agent_profile_template"]].Content, "核心指令") {
		t.Fatalf("legacy migration should create a variable-based profile template: %+v", saved.PromptBlocks[ids["agent_profile_template"]])
	}
}

func TestSettingsRoutesAllowCustomPromptBlocks(t *testing.T) {
	router := newTestRouter(RouterOptions{})

	body := strings.NewReader(`{"prompt_blocks":[{"block_id":"custom-tone","code":"tone","title":"输出语气","description":"自定义静态提示词","role":"system","content":"回复要简洁。","dynamic":false,"editable":true,"variable_only":true,"enabled":true},{"block_id":"current_user_input","code":"current_user_input","title":"当前输入","role":"user","placeholder":"<<current_user_input>>","dynamic":true,"editable":false,"enabled":true}]}`)
	put := httptest.NewRecorder()
	router.ServeHTTP(put, authedHTTPTestRequest(http.MethodPut, "/api/settings", body))
	if put.Code != http.StatusOK {
		t.Fatalf("PUT /api/settings status = %d body=%s", put.Code, put.Body.String())
	}
	var saved SettingsDTO
	if err := json.NewDecoder(put.Body).Decode(&saved); err != nil {
		t.Fatalf("decode PUT /api/settings: %v", err)
	}
	if len(saved.PromptBlocks) != 2 ||
		saved.PromptBlocks[0].BlockID != "custom-tone" ||
		saved.PromptBlocks[0].Code != "tone" ||
		saved.PromptBlocks[0].Role != "system" ||
		saved.PromptBlocks[0].Content != "回复要简洁。" ||
		!saved.PromptBlocks[0].Editable ||
		!saved.PromptBlocks[0].VariableOnly ||
		saved.PromptBlocks[0].Dynamic {
		t.Fatalf("custom prompt block not preserved: %+v", saved.PromptBlocks)
	}
}

func TestSettingsRoutesGetMigratesLegacyDynamicVariableBlocks(t *testing.T) {
	settings := newMemorySettingsService(DefaultSettings())
	_, err := settings.SaveSettings(context.Background(), SettingsDTO{
		PlatformName:                 "Orrery Mission Control",
		OrganizationName:             "Example Technology",
		Language:                     "zh-CN",
		AutoCreateAgentProfile:       true,
		AutoLinkEvidence:             true,
		HILDefaultEnabled:            true,
		DiscussionAgentPairLoopLimit: 8,
		PromptBlocks: []SettingsPromptBlockDTO{{
			BlockID:     "agent_real_name",
			Code:        "agent_real_name",
			Title:       "Agent 真实姓名",
			Role:        "system",
			Placeholder: "<<agent_real_name>>",
			Dynamic:     true,
			Editable:    false,
			Enabled:     true,
		}, {
			BlockID:     "conversation_history",
			Code:        "conversation_history",
			Title:       "对话历史",
			Role:        "user",
			Placeholder: "<<conversation_history>>",
			Dynamic:     true,
			Editable:    false,
			Enabled:     true,
		}},
		NotificationRules: DefaultSettings().NotificationRules,
	})
	if err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	router := newTestRouter(RouterOptions{Settings: settings})

	get := httptest.NewRecorder()
	router.ServeHTTP(get, authedHTTPTestRequest(http.MethodGet, "/api/settings", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("GET /api/settings status = %d body=%s", get.Code, get.Body.String())
	}
	var got SettingsDTO
	if err := json.NewDecoder(get.Body).Decode(&got); err != nil {
		t.Fatalf("decode GET /api/settings: %v", err)
	}
	byID := map[string]SettingsPromptBlockDTO{}
	for _, block := range got.PromptBlocks {
		byID[block.BlockID] = block
	}
	if block := byID["agent_real_name"]; block.Dynamic || !block.Editable || block.Content != "<<agent_real_name>>" {
		t.Fatalf("legacy agent_real_name should become editable template: %+v", block)
	}
	if block := byID["conversation_history"]; !block.Dynamic || block.Editable {
		t.Fatalf("conversation_history should stay locked dynamic: %+v", block)
	}
	if block := byID["workspace_info"]; block.Content != "<<workspace_info>>" || !block.Editable || block.Dynamic {
		t.Fatalf("legacy default settings should receive workspace_info block: %+v", block)
	}
}

func TestSettingsRoutesReadAndWritePromptBlocks(t *testing.T) {
	router := newTestRouter(RouterOptions{})

	body := strings.NewReader(`{"prompt_blocks":[{"block_id":"current_user_input","title":"当前输入","description":"运行时注入","role":"user","content":"","placeholder":"{{current_user_input}}","dynamic":true,"editable":false,"enabled":true},{"block_id":"system_principles","title":"系统原则","description":"平台级规则","role":"system","content":"平台原则：不要模拟说话人前缀。","placeholder":"","dynamic":false,"editable":true,"enabled":true}]}`)
	put := httptest.NewRecorder()
	router.ServeHTTP(put, authedHTTPTestRequest(http.MethodPut, "/api/settings", body))
	if put.Code != http.StatusOK {
		t.Fatalf("PUT /api/settings status = %d body=%s", put.Code, put.Body.String())
	}
	var saved SettingsDTO
	if err := json.NewDecoder(put.Body).Decode(&saved); err != nil {
		t.Fatalf("decode PUT /api/settings: %v", err)
	}
	if len(saved.PromptBlocks) != 2 ||
		saved.PromptBlocks[0].BlockID != "current_user_input" ||
		saved.PromptBlocks[1].Content != "平台原则：不要模拟说话人前缀。" ||
		!saved.PromptBlocks[0].Editable ||
		saved.PromptBlocks[0].Dynamic ||
		saved.PromptBlocks[0].Content != "<<current_user_input>>" {
		t.Fatalf("saved prompt blocks = %+v", saved.PromptBlocks)
	}

	get := httptest.NewRecorder()
	router.ServeHTTP(get, authedHTTPTestRequest(http.MethodGet, "/api/settings", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("GET /api/settings status = %d body=%s", get.Code, get.Body.String())
	}
	var got SettingsDTO
	if err := json.NewDecoder(get.Body).Decode(&got); err != nil {
		t.Fatalf("decode GET /api/settings: %v", err)
	}
	if len(got.PromptBlocks) != 2 ||
		got.PromptBlocks[0].BlockID != "current_user_input" ||
		got.PromptBlocks[0].Dynamic ||
		!got.PromptBlocks[0].Editable ||
		got.PromptBlocks[0].Content != "<<current_user_input>>" ||
		got.PromptBlocks[1].Content != "平台原则：不要模拟说话人前缀。" {
		t.Fatalf("GET prompt blocks = %+v", got.PromptBlocks)
	}
}

func TestSettingsRoutesKeepsCustomPromptBlockListWithoutAutoAppendingWorkspaceInfo(t *testing.T) {
	router := newTestRouter(RouterOptions{})

	body := strings.NewReader(`{"prompt_blocks":[{"block_id":"custom-minimal","code":"custom_minimal","title":"Custom minimal","role":"user","content":"只发送当前输入：<<current_user_input>>","editable":true,"removable":true,"enabled":true}]}`)
	put := httptest.NewRecorder()
	router.ServeHTTP(put, authedHTTPTestRequest(http.MethodPut, "/api/settings", body))
	if put.Code != http.StatusOK {
		t.Fatalf("PUT /api/settings status = %d body=%s", put.Code, put.Body.String())
	}
	var saved SettingsDTO
	if err := json.NewDecoder(put.Body).Decode(&saved); err != nil {
		t.Fatalf("decode PUT /api/settings: %v", err)
	}
	if len(saved.PromptBlocks) != 1 || saved.PromptBlocks[0].BlockID != "custom-minimal" {
		t.Fatalf("custom prompt block list should stay explicit: %+v", saved.PromptBlocks)
	}
}

func TestSettingsRoutesRejectDuplicatePromptBlocks(t *testing.T) {
	router := newTestRouter(RouterOptions{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, authedHTTPTestRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"prompt_blocks":[{"block_id":"system_principles","title":"A","role":"system","enabled":true},{"block_id":"system_principles","title":"B","role":"system","enabled":true}]}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT /api/settings status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"invalid_request"`) || !strings.Contains(rec.Body.String(), "prompt_blocks[1].block_id") {
		t.Fatalf("duplicate prompt block response missing field: %s", rec.Body.String())
	}
}

func TestSettingsRoutesKeepInvalidBlocksVisibleToValidation(t *testing.T) {
	for name, blocks := range map[string]string{
		"blank id":                 `[{"block_id":"","title":"A","role":"system","content":"A","enabled":true}]`,
		"blank title":              `[{"block_id":"custom-policy","title":"","role":"system","content":"A","enabled":true}]`,
		"duplicate legacy profile": `[{"block_id":"agent_profile","enabled":true},{"block_id":"agent_profile","enabled":true}]`,
	} {
		t.Run(name, func(t *testing.T) {
			router := newTestRouter(RouterOptions{})
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, authedHTTPTestRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"prompt_blocks":`+blocks+`}`)))
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"invalid_request"`) {
				t.Fatalf("invalid prompt block accepted: status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestSettingsRoutesRejectDuplicatePromptBlockCodes(t *testing.T) {
	router := newTestRouter(RouterOptions{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, authedHTTPTestRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"prompt_blocks":[{"block_id":"custom-a","code":"shared","title":"A","role":"system","content":"A","dynamic":false,"editable":true,"enabled":true},{"block_id":"custom-b","code":"shared","title":"B","role":"system","content":"B","dynamic":false,"editable":true,"enabled":true}]}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT /api/settings status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"invalid_request"`) || !strings.Contains(rec.Body.String(), "prompt_blocks[1].code") {
		t.Fatalf("duplicate prompt block code response missing field: %s", rec.Body.String())
	}
}

func TestSettingsRoutesRejectInvalidDiscussionAgentPairLoopLimit(t *testing.T) {
	router := newTestRouter(RouterOptions{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, authedHTTPTestRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"discussion_agent_pair_loop_limit":0}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT /api/settings status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"invalid_request"`) || !strings.Contains(rec.Body.String(), "discussion_agent_pair_loop_limit") {
		t.Fatalf("PUT /api/settings invalid discussion loop limit response missing field: %s", rec.Body.String())
	}
}

func TestSettingsRoutesRejectRemovedDiscussionMentionLimit(t *testing.T) {
	router := newTestRouter(RouterOptions{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, authedHTTPTestRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"pm_conversation_mention_max_responses":4}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT /api/settings status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"invalid_request"`) || !strings.Contains(rec.Body.String(), "pm_conversation_mention_max_responses") {
		t.Fatalf("PUT /api/settings must reject removed discussion mention fields: %s", rec.Body.String())
	}
}

func TestSettingsRoutesReadAndWriteWorkspaceRoot(t *testing.T) {
	router := newTestRouter(RouterOptions{})
	root := filepath.Join(t.TempDir(), "workspace")
	body, err := json.Marshal(SettingsDTO{
		PlatformName:           "Orrery",
		OrganizationName:       "Example",
		Language:               "en-US",
		AutoCreateAgentProfile: true,
		AutoLinkEvidence:       true,
		HILDefaultEnabled:      true,
		WorkspaceRoot:          "  " + root + "  ",
	})
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}

	put := httptest.NewRecorder()
	router.ServeHTTP(put, authedHTTPTestRequest(http.MethodPut, "/api/settings", bytes.NewReader(body)))
	if put.Code != http.StatusOK {
		t.Fatalf("PUT /api/settings status = %d body=%s", put.Code, put.Body.String())
	}
	var saved SettingsDTO
	if err := json.NewDecoder(put.Body).Decode(&saved); err != nil {
		t.Fatalf("decode PUT /api/settings: %v", err)
	}
	if saved.WorkspaceRoot != filepath.Clean(root) {
		t.Fatalf("PUT /api/settings workspace_root = %q, want %q", saved.WorkspaceRoot, filepath.Clean(root))
	}

	get := httptest.NewRecorder()
	router.ServeHTTP(get, authedHTTPTestRequest(http.MethodGet, "/api/settings", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("GET /api/settings status = %d body=%s", get.Code, get.Body.String())
	}
	var got SettingsDTO
	if err := json.NewDecoder(get.Body).Decode(&got); err != nil {
		t.Fatalf("decode GET /api/settings: %v", err)
	}
	if got.WorkspaceRoot != filepath.Clean(root) {
		t.Fatalf("GET /api/settings workspace_root = %q, want %q", got.WorkspaceRoot, filepath.Clean(root))
	}
}

func TestSettingsRoutesRejectRelativeWorkspaceRoot(t *testing.T) {
	router := newTestRouter(RouterOptions{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, authedHTTPTestRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"workspace_root":"relative/workspace"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT /api/settings status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"invalid_request"`) || !strings.Contains(rec.Body.String(), "workspace_root") {
		t.Fatalf("PUT /api/settings invalid workspace response missing field workspace_root: %s", rec.Body.String())
	}
}

func TestSettingsRoutesReadAndWriteWorkspaceAllowlist(t *testing.T) {
	router := newTestRouter(RouterOptions{})
	root := filepath.Join(t.TempDir(), "allowed")
	nested := filepath.Join(root, "nested")
	body, err := json.Marshal(SettingsDTO{
		PlatformName:           "Orrery",
		OrganizationName:       "Example",
		Language:               "en-US",
		AutoCreateAgentProfile: true,
		AutoLinkEvidence:       true,
		HILDefaultEnabled:      true,
		WorkspaceAllowlist:     []string{"  " + root + "  ", nested, root},
	})
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}

	put := httptest.NewRecorder()
	router.ServeHTTP(put, authedHTTPTestRequest(http.MethodPut, "/api/settings", bytes.NewReader(body)))
	if put.Code != http.StatusOK {
		t.Fatalf("PUT /api/settings status = %d body=%s", put.Code, put.Body.String())
	}
	var saved SettingsDTO
	if err := json.NewDecoder(put.Body).Decode(&saved); err != nil {
		t.Fatalf("decode PUT /api/settings: %v", err)
	}
	want := []string{filepath.Clean(root), filepath.Clean(nested)}
	if !reflect.DeepEqual(saved.WorkspaceAllowlist, want) {
		t.Fatalf("PUT /api/settings workspace_allowlist = %#v, want %#v", saved.WorkspaceAllowlist, want)
	}

	get := httptest.NewRecorder()
	router.ServeHTTP(get, authedHTTPTestRequest(http.MethodGet, "/api/settings", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("GET /api/settings status = %d body=%s", get.Code, get.Body.String())
	}
	var got SettingsDTO
	if err := json.NewDecoder(get.Body).Decode(&got); err != nil {
		t.Fatalf("decode GET /api/settings: %v", err)
	}
	if !reflect.DeepEqual(got.WorkspaceAllowlist, want) {
		t.Fatalf("GET /api/settings workspace_allowlist = %#v, want %#v", got.WorkspaceAllowlist, want)
	}
}

func TestSettingsRoutesRejectRelativeWorkspaceAllowlistEntry(t *testing.T) {
	router := newTestRouter(RouterOptions{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, authedHTTPTestRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"workspace_allowlist":["relative/workspace"]}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT /api/settings status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"invalid_request"`) || !strings.Contains(rec.Body.String(), "workspace_allowlist[0]") {
		t.Fatalf("PUT /api/settings invalid workspace allowlist response missing field workspace_allowlist[0]: %s", rec.Body.String())
	}
}

func TestSettingsRoutesReadAndWriteLLMProfiles(t *testing.T) {
	router := newTestRouter(RouterOptions{})

	body := strings.NewReader(`{"platform_name":"Orrery","organization_name":"Example","language":"en-US","auto_create_agent_profile":true,"auto_link_evidence":true,"hil_default_enabled":true,"llm_profiles":[{"profile_id":"llm-openai","display_name":"OpenAI 主模型","provider":"openai","base_url":"https://api.openai.com/v1","api_key_ref":"secret://llm/openai","model":"gpt-5.4","temperature":0.2,"context_length":128000,"enabled":true},{"profile_id":"llm-deepseek","display_name":"DeepSeek 备用","provider":"deepseek","base_url":"https://api.deepseek.com/v1","api_key_ref":"secret://llm/deepseek","model":"deepseek-chat","temperature":0.3,"context_length":64000,"enabled":false}]}`)
	put := httptest.NewRecorder()
	router.ServeHTTP(put, authedHTTPTestRequest(http.MethodPut, "/api/settings", body))
	if put.Code != http.StatusOK {
		t.Fatalf("PUT /api/settings status = %d body=%s", put.Code, put.Body.String())
	}
	for _, want := range []string{
		`"llm_profiles":[`,
		`"provider":"openai"`,
		`"base_url":"https://api.openai.com/v1"`,
		`"api_key_ref":"secret://llm/openai"`,
		`"model":"gpt-5.4"`,
		`"temperature":0.2`,
		`"context_length":128000`,
		`"provider":"deepseek"`,
		`"api_key_ref":"secret://llm/deepseek"`,
	} {
		if !strings.Contains(put.Body.String(), want) {
			t.Fatalf("PUT /api/settings missing llm field %s: %s", want, put.Body.String())
		}
	}

	get := httptest.NewRecorder()
	router.ServeHTTP(get, authedHTTPTestRequest(http.MethodGet, "/api/settings", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("GET /api/settings status = %d body=%s", get.Code, get.Body.String())
	}
	for _, want := range []string{
		`"profile_id":"llm-openai"`,
		`"profile_id":"llm-deepseek"`,
		`"provider":"deepseek"`,
		`"context_length":64000`,
	} {
		if !strings.Contains(get.Body.String(), want) {
			t.Fatalf("GET /api/settings after PUT missing llm field %s: %s", want, get.Body.String())
		}
	}
}

func TestSettingsRoutesDoNotExposeLegacyLLMAPIKeys(t *testing.T) {
	initial := DefaultSettings()
	initial.LLMProfiles = []SettingsLLMProfileDTO{{
		ProfileID: "legacy",
		BaseURL:   "https://llm.example/v1",
		APIKey:    "legacy-plaintext-secret",
	}}
	router := newTestRouter(RouterOptions{Settings: newMemorySettingsService(initial)})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, authedHTTPTestRequest(http.MethodGet, "/api/settings", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/settings status = %d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "legacy-plaintext-secret") || strings.Contains(rec.Body.String(), `"api_key":`) {
		t.Fatalf("GET /api/settings exposed legacy LLM API key: %s", rec.Body.String())
	}
}

func TestSettingsRoutesKeepsOnlyOneGlobalDefaultLLMProfile(t *testing.T) {
	router := newTestRouter(RouterOptions{})

	body := strings.NewReader(`{"llm_profiles":[{"profile_id":"llm-openai","display_name":"OpenAI","provider":"openai","base_url":"https://api.openai.com/v1","api_key_ref":"secret://llm/openai","model":"gpt-5","enabled":true,"global_default":true},{"profile_id":"llm-deepseek","display_name":"DeepSeek","provider":"deepseek","base_url":"https://api.deepseek.com/v1","api_key_ref":"secret://llm/deepseek","model":"deepseek-chat","enabled":true,"global_default":true}]}`)
	put := httptest.NewRecorder()
	router.ServeHTTP(put, authedHTTPTestRequest(http.MethodPut, "/api/settings", body))
	if put.Code != http.StatusOK {
		t.Fatalf("PUT /api/settings status = %d body=%s", put.Code, put.Body.String())
	}
	var saved SettingsDTO
	if err := json.NewDecoder(put.Body).Decode(&saved); err != nil {
		t.Fatalf("decode PUT /api/settings: %v", err)
	}
	if len(saved.LLMProfiles) != 2 || saved.LLMProfiles[0].GlobalDefault || !saved.LLMProfiles[1].GlobalDefault {
		t.Fatalf("PUT /api/settings should keep the last submitted global default: %+v", saved.LLMProfiles)
	}

	get := httptest.NewRecorder()
	router.ServeHTTP(get, authedHTTPTestRequest(http.MethodGet, "/api/settings", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("GET /api/settings status = %d body=%s", get.Code, get.Body.String())
	}
	var got SettingsDTO
	if err := json.NewDecoder(get.Body).Decode(&got); err != nil {
		t.Fatalf("decode GET /api/settings: %v", err)
	}
	if len(got.LLMProfiles) != 2 || got.LLMProfiles[0].GlobalDefault || !got.LLMProfiles[1].GlobalDefault || got.LLMProfiles[1].ProfileID != "llm-deepseek" {
		t.Fatalf("GET /api/settings should persist the last global default profile: %+v", got.LLMProfiles)
	}
}

func TestSettingsRoutesMergeLLMProfileUpdatesWithExistingSettings(t *testing.T) {
	router := newTestRouter(RouterOptions{})

	initial := strings.NewReader(`{"platform_name":"Custom Platform","organization_name":"Custom Org","language":"en-US","auto_create_agent_profile":false,"auto_link_evidence":false,"hil_default_enabled":false,"notification_rules":[{"rule_id":"approval","label_key":"settings.notification.approval.label","description_key":"settings.notification.approval.desc","enabled":false}]}`)
	putInitial := httptest.NewRecorder()
	router.ServeHTTP(putInitial, authedHTTPTestRequest(http.MethodPut, "/api/settings", initial))
	if putInitial.Code != http.StatusOK {
		t.Fatalf("initial PUT /api/settings status = %d body=%s", putInitial.Code, putInitial.Body.String())
	}

	llmOnly := strings.NewReader(`{"llm_profiles":[{"profile_id":"llm-openai","display_name":"OpenAI","provider":"openai","base_url":"https://api.openai.com/v1","api_key_ref":"secret://llm/openai","model":"gpt-5","enabled":true}]}`)
	putLLM := httptest.NewRecorder()
	router.ServeHTTP(putLLM, authedHTTPTestRequest(http.MethodPut, "/api/settings", llmOnly))
	if putLLM.Code != http.StatusOK {
		t.Fatalf("LLM PUT /api/settings status = %d body=%s", putLLM.Code, putLLM.Body.String())
	}

	get := httptest.NewRecorder()
	router.ServeHTTP(get, authedHTTPTestRequest(http.MethodGet, "/api/settings", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("GET /api/settings status = %d body=%s", get.Code, get.Body.String())
	}
	for _, want := range []string{
		`"platform_name":"Custom Platform"`,
		`"organization_name":"Custom Org"`,
		`"auto_create_agent_profile":false`,
		`"rule_id":"approval"`,
		`"profile_id":"llm-openai"`,
	} {
		if !strings.Contains(get.Body.String(), want) {
			t.Fatalf("GET /api/settings after LLM update missing %s: %s", want, get.Body.String())
		}
	}
}

func TestSettingsRoutesRejectInvalidLLMProfiles(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantField string
	}{
		{
			name:      "missing base url",
			body:      `{"llm_profiles":[{"profile_id":"llm-openai","display_name":"OpenAI","provider":"openai","model":"gpt-5","api_key":"sk-test","enabled":true}]}`,
			wantField: "llm_profiles[0].base_url",
		},
		{
			name:      "missing api key and ref",
			body:      `{"llm_profiles":[{"profile_id":"llm-openai","display_name":"OpenAI","provider":"openai","base_url":"https://api.openai.com/v1","model":"gpt-5","enabled":true}]}`,
			wantField: "llm_profiles[0].api_key_ref",
		},
		{
			name:      "direct api key",
			body:      `{"llm_profiles":[{"profile_id":"llm-glm","display_name":"GLM","provider":"glm","base_url":"https://open.bigmodel.cn/api/paas/v4","api_key":"检查你像：剪贴板上有一些不可见的白色字符。","model":"glm-5.1","enabled":true}]}`,
			wantField: "llm_profiles[0].api_key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := newTestRouter(RouterOptions{})
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, authedHTTPTestRequest(http.MethodPut, "/api/settings", strings.NewReader(tt.body)))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("PUT /api/settings status = %d body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"code":"invalid_request"`) || !strings.Contains(rec.Body.String(), tt.wantField) {
				t.Fatalf("PUT /api/settings invalid LLM profile response missing field %s: %s", tt.wantField, rec.Body.String())
			}
		})
	}
}

func TestSettingsRoutesReadAndWriteRuntimeProfiles(t *testing.T) {
	router := newTestRouter(RouterOptions{})

	body := strings.NewReader(`{"platform_name":"Orrery","organization_name":"Example","language":"en-US","auto_create_agent_profile":true,"auto_link_evidence":true,"hil_default_enabled":true,"runtime_profiles":[{"profile_id":"runtime-openclaw-local","display_name":"OpenClaw","runtime_kind":"openclaw","runtime_config_ref":"runtime-openclaw-local","runtime_config":{"executable":"openclaw","agent_id":"planner","config_path":"/home/me/.openclaw/config.yaml","extra_args":["--profile","prod"]},"enabled":true},{"profile_id":"runtime-codex-local","display_name":"Codex","runtime_kind":"codex","runtime_config_ref":"runtime-codex-local","runtime_config":{"executable":"codex"},"enabled":false}]}`)
	put := httptest.NewRecorder()
	router.ServeHTTP(put, authedHTTPTestRequest(http.MethodPut, "/api/settings", body))
	if put.Code != http.StatusOK {
		t.Fatalf("PUT /api/settings status = %d body=%s", put.Code, put.Body.String())
	}
	for _, want := range []string{
		`"runtime_profiles":[`,
		`"profile_id":"runtime-openclaw-local"`,
		`"runtime_kind":"openclaw"`,
		`"executable":"openclaw"`,
		`"agent_id":"planner"`,
		`"config_path":"/home/me/.openclaw/config.yaml"`,
		`"extra_args":["--profile","prod"]`,
		`"profile_id":"runtime-codex-local"`,
		`"enabled":false`,
	} {
		if !strings.Contains(put.Body.String(), want) {
			t.Fatalf("PUT /api/settings missing runtime field %s: %s", want, put.Body.String())
		}
	}

	get := httptest.NewRecorder()
	router.ServeHTTP(get, authedHTTPTestRequest(http.MethodGet, "/api/settings", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("GET /api/settings status = %d body=%s", get.Code, get.Body.String())
	}
	for _, want := range []string{
		`"runtime_profiles":[`,
		`"runtime_kind":"openclaw"`,
		`"runtime_kind":"codex"`,
		`"agent_id":"planner"`,
	} {
		if !strings.Contains(get.Body.String(), want) {
			t.Fatalf("GET /api/settings after PUT missing runtime field %s: %s", want, get.Body.String())
		}
	}
}

func TestSettingsRoutesReadAndWriteACPRuntimeProfile(t *testing.T) {
	router := newTestRouter(RouterOptions{})
	body := strings.NewReader(`{"runtime_profiles":[{"profile_id":"runtime-acp-custom","display_name":"ACP","runtime_kind":"acp","runtime_config_ref":"runtime-acp-custom","runtime_config":{"executable":"custom-acp-agent","extra_args":["--profile","production"]},"enabled":true}]}`)

	put := httptest.NewRecorder()
	router.ServeHTTP(put, authedHTTPTestRequest(http.MethodPut, "/api/settings", body))
	if put.Code != http.StatusOK {
		t.Fatalf("PUT /api/settings status = %d body=%s", put.Code, put.Body.String())
	}
	for _, want := range []string{`"runtime_kind":"acp"`, `"executable":"custom-acp-agent"`, `"extra_args":["--profile","production"]`} {
		if !strings.Contains(put.Body.String(), want) {
			t.Fatalf("PUT /api/settings missing ACP runtime field %s: %s", want, put.Body.String())
		}
	}
}

func TestSettingsRoutesMigratesMissingRuntimeProfilesToDefaultPi(t *testing.T) {
	settings := newMemorySettingsService(DefaultSettings())
	legacy := DefaultSettings()
	legacy.RuntimeProfiles = nil
	if _, err := settings.SaveSettings(context.Background(), legacy); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	router := newTestRouter(RouterOptions{Settings: settings})

	get := httptest.NewRecorder()
	router.ServeHTTP(get, authedHTTPTestRequest(http.MethodGet, "/api/settings", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("GET /api/settings status = %d body=%s", get.Code, get.Body.String())
	}
	var got SettingsDTO
	if err := json.NewDecoder(get.Body).Decode(&got); err != nil {
		t.Fatalf("decode GET /api/settings: %v", err)
	}
	if len(got.RuntimeProfiles) != 1 || got.RuntimeProfiles[0].RuntimeKind != "pi" ||
		got.RuntimeProfiles[0].RuntimeConfig["approval_mode"] != "on-request" {
		t.Fatalf("legacy settings should receive default PI runtime: %+v", got.RuntimeProfiles)
	}
}

func TestSettingsRoutesKeepsExplicitEmptyRuntimeProfiles(t *testing.T) {
	router := newTestRouter(RouterOptions{})

	put := httptest.NewRecorder()
	router.ServeHTTP(put, authedHTTPTestRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"runtime_profiles":[]}`)))
	if put.Code != http.StatusOK {
		t.Fatalf("PUT /api/settings status = %d body=%s", put.Code, put.Body.String())
	}
	var saved SettingsDTO
	if err := json.NewDecoder(put.Body).Decode(&saved); err != nil {
		t.Fatalf("decode PUT /api/settings: %v", err)
	}
	if len(saved.RuntimeProfiles) != 0 {
		t.Fatalf("explicit empty runtime profiles should be preserved: %+v", saved.RuntimeProfiles)
	}
}

func TestSettingsRoutesDefaultMissingRuntimeApprovalPolicy(t *testing.T) {
	legacy := DefaultSettings()
	legacy.RuntimeApprovals = runtimeapproval.Policy{}
	settings := newMemorySettingsService(legacy)
	router := newTestRouter(RouterOptions{Settings: settings})

	get := httptest.NewRecorder()
	router.ServeHTTP(get, authedHTTPTestRequest(http.MethodGet, "/api/settings", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("GET /api/settings status = %d body=%s", get.Code, get.Body.String())
	}
	var got SettingsDTO
	if err := json.NewDecoder(get.Body).Decode(&got); err != nil {
		t.Fatalf("decode GET /api/settings: %v", err)
	}
	resolved := got.RuntimeApprovals.Resolve(runtimeapproval.ResolveInput{ConversationKind: "personal_assistant"})
	if resolved.HILMode != runtimeapproval.HILModeAuto || resolved.ApprovalPolicy != runtimeapproval.ApprovalPolicyOnRequest || resolved.ApprovalsReviewer != runtimeapproval.ApprovalsReviewerAI || resolved.Exec.ApprovalPolicy != runtimeapproval.ApprovalPolicyNever || got.RuntimeApprovals.Reviewer.TimeoutMS != 30000 || got.RuntimeApprovals.Reviewer.MaxTokens != 360 {
		t.Fatalf("default runtime approval policy = %+v", got.RuntimeApprovals)
	}
}

func TestNormalizeSettingsDisablesRuntimeHumanReview(t *testing.T) {
	settings := DefaultSettings()
	settings.HILDefaultEnabled = false
	settings.RuntimeApprovals.ForcedHuman = runtimeapproval.ForcedHumanPolicy{ToolIDs: []string{"publish"}, ExecExecutables: []string{"rm"}}
	settings.RuntimeApprovals.AgentOverrides = []runtimeapproval.AgentOverride{{
		LogicalAgentID: "agent-1",
		PolicyOverride: runtimeapproval.PolicyOverride{ApprovalPolicy: runtimeapproval.ApprovalPolicyOnRequest},
	}}
	settings.RuntimeProfiles = append(settings.RuntimeProfiles, SettingsRuntimeProfileDTO{
		ProfileID: "runtime-claude", RuntimeKind: "claude", RuntimeConfig: map[string]any{"mode": "agent_sdk", "permission_mode": "default"}, Enabled: true,
	})

	NormalizeSettings(&settings)
	for _, input := range []runtimeapproval.ResolveInput{{}, {LogicalAgentID: "agent-1"}} {
		resolved := settings.RuntimeApprovals.Resolve(input)
		if resolved.ApprovalPolicy != runtimeapproval.ApprovalPolicyNever || resolved.Exec.ApprovalPolicy != runtimeapproval.ApprovalPolicyNever || len(resolved.ForcedHuman.ToolIDs) != 0 || len(resolved.ForcedHuman.ExecExecutables) != 0 {
			t.Fatalf("resolved policy = %+v", resolved)
		}
	}
	if settings.RuntimeProfiles[0].RuntimeConfig["approval_mode"] != "never" || settings.RuntimeProfiles[1].RuntimeConfig["permission_mode"] != "bypassPermissions" {
		t.Fatalf("runtime profiles = %+v", settings.RuntimeProfiles)
	}
}

func TestSettingsRoutesRoundTripRuntimeApprovalPolicy(t *testing.T) {
	router := newTestRouter(RouterOptions{})
	body := strings.NewReader(`{
		"runtime_approvals": {
			"default_hil_mode": "manual",
			"default_approval_policy": "on-request",
			"default_approvals_reviewer": "human",
			"reviewer": {"llm_profile_id":"reviewer-profile","timeout_ms":25000,"max_tokens":300},
			"exec": {"approval_policy":"on-request","security":"allowlist","allowlist":[{"executable":"git","subcommands":["status","diff"],"workspace_only":true}]},
			"tools": {"security":"allowlist","allowlist":[{"tool_id":"discussion_talent_list","effects":["read"],"risk":"low"}]},
			"forced_human": {"tool_ids":["marketing_wechat_draft_create"],"exec_executables":["rm"]},
			"conversation_defaults": {"personal_assistant":{"hil_mode":"auto","approval_policy":"on-request","approvals_reviewer":"ai"}},
			"agent_overrides": [{"logical_agent_id":"assistant:human-1","approval_policy":"never","tools":{"security":"deny"}}]
		}
	}`)

	put := httptest.NewRecorder()
	request := authedHTTPTestRequest(http.MethodPut, "/api/settings", body)
	request.Header.Set("X-Permission-Scope", "settings.manage,approvals.manage")
	router.ServeHTTP(put, request)
	if put.Code != http.StatusOK {
		t.Fatalf("PUT /api/settings status = %d body=%s", put.Code, put.Body.String())
	}
	var got SettingsDTO
	if err := json.NewDecoder(put.Body).Decode(&got); err != nil {
		t.Fatalf("decode PUT /api/settings: %v", err)
	}
	if got.RuntimeApprovals.Reviewer.LLMProfileID != "reviewer-profile" || len(got.RuntimeApprovals.Exec.Allowlist) != 1 || len(got.RuntimeApprovals.AgentOverrides) != 1 {
		t.Fatalf("runtime approval policy did not round trip: %+v", got.RuntimeApprovals)
	}
	if got.RuntimeApprovals.DefaultApprovalPolicy != runtimeapproval.ApprovalPolicyOnRequest || got.RuntimeApprovals.DefaultApprovalsReviewer != runtimeapproval.ApprovalsReviewerHuman || got.RuntimeApprovals.Exec.ApprovalPolicy != runtimeapproval.ApprovalPolicyOnRequest || got.RuntimeApprovals.AgentOverrides[0].ApprovalPolicy != runtimeapproval.ApprovalPolicyNever {
		t.Fatalf("runtime approval modes did not round trip: %+v", got.RuntimeApprovals)
	}
}

func TestSettingsRoutesReadsLegacyApprovalBypassAsNever(t *testing.T) {
	legacy := DefaultSettings()
	bypass := true
	legacy.RuntimeApprovals.AgentOverrides = []runtimeapproval.AgentOverride{{
		LogicalAgentID: "personal-assistant-ding",
		PolicyOverride: runtimeapproval.PolicyOverride{ApprovalBypass: &bypass},
	}}
	router := newTestRouter(RouterOptions{Settings: newMemorySettingsService(legacy)})

	get := httptest.NewRecorder()
	router.ServeHTTP(get, authedHTTPTestRequest(http.MethodGet, "/api/settings", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("GET /api/settings status = %d body=%s", get.Code, get.Body.String())
	}
	var got SettingsDTO
	if err := json.NewDecoder(get.Body).Decode(&got); err != nil {
		t.Fatalf("decode GET /api/settings: %v", err)
	}
	resolved := got.RuntimeApprovals.Resolve(runtimeapproval.ResolveInput{LogicalAgentID: "personal-assistant-ding"})
	if resolved.ApprovalPolicy != runtimeapproval.ApprovalPolicyNever {
		t.Fatalf("legacy approval bypass resolved policy = %+v", resolved)
	}
}

func TestSettingsRoutesRequiresApprovalsManageForNeverPolicy(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "new policy",
			body: `{"runtime_approvals":{"agent_overrides":[{"logical_agent_id":"personal-assistant-ding","approval_policy":"never"}]}}`,
		},
		{
			name: "legacy bypass",
			body: `{"runtime_approvals":{"agent_overrides":[{"logical_agent_id":"personal-assistant-ding","approval_bypass":true}]}}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := newTestRouter(RouterOptions{})
			forbidden := httptest.NewRecorder()
			request := authedHTTPTestRequest(http.MethodPut, "/api/settings", strings.NewReader(test.body))
			request.Header.Set("X-Permission-Scope", "settings.manage")
			router.ServeHTTP(forbidden, request)
			if forbidden.Code != http.StatusForbidden || !strings.Contains(forbidden.Body.String(), "approvals.manage") {
				t.Fatalf("PUT without approvals.manage status = %d body=%s", forbidden.Code, forbidden.Body.String())
			}

			allowed := httptest.NewRecorder()
			request = authedHTTPTestRequest(http.MethodPut, "/api/settings", strings.NewReader(test.body))
			request.Header.Set("X-Permission-Scope", "settings.manage,approvals.manage")
			router.ServeHTTP(allowed, request)
			if allowed.Code != http.StatusOK {
				t.Fatalf("PUT with approvals.manage status = %d body=%s", allowed.Code, allowed.Body.String())
			}
		})
	}
}

func TestSettingsRoutesRejectInvalidRuntimeApprovalPolicy(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		field string
	}{
		{name: "security", body: `{"runtime_approvals":{"exec":{"security":"sometimes"}}}`, field: "runtime_approvals.exec.security"},
		{name: "exec approval policy", body: `{"runtime_approvals":{"exec":{"approval_policy":"sometimes"}}}`, field: "runtime_approvals.exec.approval_policy"},
		{name: "timeout", body: `{"runtime_approvals":{"reviewer":{"timeout_ms":999}}}`, field: "runtime_approvals.reviewer.timeout_ms"},
		{name: "empty tool id", body: `{"runtime_approvals":{"tools":{"security":"allowlist","allowlist":[{"tool_id":""}]}}}`, field: "runtime_approvals.tools.allowlist[0].tool_id"},
		{name: "duplicate agent", body: `{"runtime_approvals":{"agent_overrides":[{"logical_agent_id":"same"},{"logical_agent_id":"same"}]}}`, field: "runtime_approvals.agent_overrides[1].logical_agent_id"},
		{name: "invalid approval policy", body: `{"runtime_approvals":{"default_approval_policy":"sometimes"}}`, field: "runtime_approvals.default_approval_policy"},
		{name: "conflicting approval fields", body: `{"runtime_approvals":{"agent_overrides":[{"logical_agent_id":"same","approval_policy":"never","approval_bypass":true}]}}`, field: "runtime_approvals.agent_overrides[0].approval_policy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := newTestRouter(RouterOptions{})
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, authedHTTPTestRequest(http.MethodPut, "/api/settings", strings.NewReader(tt.body)))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("PUT /api/settings status = %d body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tt.field) {
				t.Fatalf("PUT /api/settings response missing %q: %s", tt.field, rec.Body.String())
			}
		})
	}
}

func TestSettingsRoutesMigratesClaudeSDKToNative(t *testing.T) {
	router := newTestRouter(RouterOptions{})

	body := strings.NewReader(`{"runtime_profiles":[{"profile_id":"runtime-claude-sdk","display_name":"Claude Code SDK","runtime_kind":"claude","runtime_config_ref":"runtime-claude-sdk","runtime_config":{"mode":"agent_sdk","node_executable":"node","sidecar_entry":"custom-sidecar.mjs","path_to_claude_code_executable":"D:/runtime/claude.exe","executable":"claude","extra_args":["--model","sonnet"]},"enabled":true}]}`)
	put := httptest.NewRecorder()
	router.ServeHTTP(put, authedHTTPTestRequest(http.MethodPut, "/api/settings", body))
	if put.Code != http.StatusOK {
		t.Fatalf("PUT /api/settings status = %d body=%s", put.Code, put.Body.String())
	}
	if !strings.Contains(put.Body.String(), `"approval_timeout_ms":600000`) {
		t.Fatalf("PUT /api/settings should default Claude Agent SDK approval timeout: %s", put.Body.String())
	}
	if !strings.Contains(put.Body.String(), `"permission_mode":"default"`) {
		t.Fatalf("PUT /api/settings should default Claude Agent SDK permission mode: %s", put.Body.String())
	}
	if !strings.Contains(put.Body.String(), `"mode":"native"`) || !strings.Contains(put.Body.String(), `"executable":"claude"`) || !strings.Contains(put.Body.String(), `"extra_args"`) {
		t.Fatal("native launch configuration was not preserved")
	}
	for _, removed := range []string{"node_executable", "sidecar_entry", "path_to_claude_code_executable", `"mode":"agent_sdk"`} {
		if strings.Contains(put.Body.String(), removed) {
			t.Fatalf("PUT /api/settings should remove unused Claude Agent SDK config %q: %s", removed, put.Body.String())
		}
	}
}

func TestSettingsRoutesDefaultsPiRuntimeControlTimeouts(t *testing.T) {
	router := newTestRouter(RouterOptions{})

	body := strings.NewReader(`{"runtime_profiles":[{"profile_id":"runtime-pi-local","display_name":"PI","runtime_kind":"pi","runtime_config_ref":"runtime-pi-local","runtime_config":{"executable":"pi"},"enabled":true}]}`)
	put := httptest.NewRecorder()
	router.ServeHTTP(put, authedHTTPTestRequest(http.MethodPut, "/api/settings", body))
	if put.Code != http.StatusOK {
		t.Fatalf("PUT /api/settings status = %d body=%s", put.Code, put.Body.String())
	}
	if !strings.Contains(put.Body.String(), `"approval_mode":"on-request"`) ||
		!strings.Contains(put.Body.String(), `"approval_timeout_ms":600000`) ||
		!strings.Contains(put.Body.String(), `"prompt_ack_timeout_ms":60000`) {
		t.Fatalf("PUT /api/settings should default PI control timeouts: %s", put.Body.String())
	}
}

func TestSettingsRoutesRejectRemovedLegacyRuntimeRegistration(t *testing.T) {
	router := newTestRouter(RouterOptions{})

	body := strings.NewReader(`{"platform_name":"Orrery","organization_name":"Example","language":"en-US","auto_create_agent_profile":true,"auto_link_evidence":true,"hil_default_enabled":true,"runtime_kind":"openclaw","runtime_config_ref":"runtime-openclaw-local","runtime_config":{"executable":"openclaw","agent_id":"planner"}}`)
	put := httptest.NewRecorder()
	router.ServeHTTP(put, authedHTTPTestRequest(http.MethodPut, "/api/settings", body))
	if put.Code != http.StatusBadRequest {
		t.Fatalf("PUT /api/settings status = %d body=%s", put.Code, put.Body.String())
	}
	if !strings.Contains(put.Body.String(), `"code":"invalid_request"`) || !strings.Contains(put.Body.String(), "runtime_kind") {
		t.Fatalf("PUT /api/settings must reject removed legacy runtime fields: %s", put.Body.String())
	}
}

func TestSettingsIntegrationTestEndpointIsRemoved(t *testing.T) {
	router := newTestRouter(RouterOptions{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, authedHTTPTestRequest(http.MethodPost, "/api/settings/integrations/github/test", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("integration test endpoint must be removed, status = %d body=%s", rec.Code, rec.Body.String())
	}
}
