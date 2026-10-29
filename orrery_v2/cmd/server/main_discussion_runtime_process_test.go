package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

func TestLiveDiscussionSendUsesConfiguredRuntime(t *testing.T) {
	if !strings.EqualFold(os.Getenv("ORRERY_PM_LIVE_E2E"), "true") {
		t.Skip("set ORRERY_PM_LIVE_E2E=true and ORRERY_CONFIG to run live discussion runtime e2e")
	}
	configPath := strings.TrimSpace(os.Getenv("ORRERY_CONFIG"))
	if configPath == "" {
		t.Skip("ORRERY_CONFIG is required for live discussion runtime e2e")
	}
	handler, cleanup, err := newServerHandlerWithConfig(context.Background(), false, configPath)
	if err != nil {
		t.Fatalf("newServerHandler production: %v", err)
	}
	defer cleanup()

	send := httptest.NewRequest(http.MethodPost, "/api/discussions/messages", jsonBody(`{"content":"We need a short launch plan for v2. Ask one follow-up if required."}`))
	send.Header.Set("X-User-ID", "superadmin")
	send.Header.Set("X-Viewer-Kind", "user")
	send.Header.Set("X-User-Display-Name", "superadmin")
	send.Header.Set("Idempotency-Key", "live-pm-send-1")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, send)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("pm live send status = %d body = %s", rec.Code, rec.Body.String())
	}
	var conversation map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&conversation); err != nil {
		t.Fatalf("decode discussion: %v", err)
	}
	conversationID, _ := conversation["discussion_id"].(string)
	if conversationID == "" {
		t.Fatalf("conversation missing id: %+v", conversation)
	}
	messages, _ := conversation["messages"].([]any)
	if len(messages) < 2 {
		t.Fatalf("live discussion response did not persist user and assistant messages: %+v", conversation)
	}

	get := httptest.NewRequest(http.MethodGet, "/api/discussions/"+conversationID, nil)
	get.Header.Set("X-User-ID", "superadmin")
	get.Header.Set("X-Viewer-Kind", "user")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, get)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), conversationID) {
		t.Fatalf("pm live get status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestProductionDiscussionSendUsesConfiguredCodexRuntimeProcess(t *testing.T) {
	root := t.TempDir()
	executable := writeCodexRuntimeFixture(t)
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", productionTestDatabaseURLs(root))
	setTestConfigOverride(t, "discussion.agent.runtime_kind", "codex")
	setTestConfigOverride(t, "discussion.agent.runtime_config", `{"executable":"`+jsonPath(executable)+`","extra_args":["--model","gpt-5-codex"]}`)
	setTestConfigOverride(t, "discussion.agent.timeout", "2s")
	setTestConfigOverride(t, "auth.local.enabled", "true")
	configPath := selectTestServerConfig(t)
	loaded, err := loadProductionConfig(configPath)
	if err != nil {
		t.Fatalf("load production test config: %v", err)
	}
	var runtimeConfig struct {
		Executable string `json:"executable"`
	}
	if err := json.Unmarshal(configuredConversationAgentRuntimeOptions(loaded, defaultLeadAgentID).RuntimeSpec.Config, &runtimeConfig); err != nil {
		t.Fatalf("decode configured discussion runtime: %v", err)
	}
	if runtimeConfig.Executable != executable {
		t.Fatalf("configured discussion executable = %q, want %q", runtimeConfig.Executable, executable)
	}
	handler, cleanup, err := newServerHandler(context.Background(), false)
	if err != nil {
		t.Fatalf("newServerHandler production: %v", err)
	}
	defer cleanup()
	configureRuntimeFixtureLLMProfile(t, handler, "gpt-5-codex")
	org := createProductionWorkerOrgTree(t, handler)
	loginCookie := loginProductionSuperadmin(t, handler)

	worker := httptest.NewRequest(http.MethodPut, "/api/workforce/members/agent-worker", jsonBody(`{"agent_instance_id":"agent-worker","real_name":"Worker Agent","agent_kind":"ai","employment_type":"ai_agent","organization_id":"`+org.OrganizationID+`","department_id":"`+org.DepartmentID+`","position_id":"`+org.PositionID+`","persona":{"backstory":"Launch coordination worker","responsibility_boundary":"Own launch execution tasks"},"enabled":true}`))
	worker.AddCookie(loginCookie)
	worker.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, worker)
	if rec.Code != http.StatusOK {
		t.Fatalf("create worker status = %d body = %s", rec.Code, rec.Body.String())
	}

	send := httptest.NewRequest(http.MethodPost, "/api/discussions/messages", jsonBody(`{"content":"Plan a launch"}`))
	send.Header.Set("X-User-ID", "superadmin")
	send.Header.Set("X-Viewer-Kind", "user")
	send.Header.Set("X-User-Display-Name", "superadmin")
	send.Header.Set("Idempotency-Key", "codex-pm-send-1")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, send)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("discussion send status = %d body = %s", rec.Code, rec.Body.String())
	}

	var conversation map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&conversation); err != nil {
		t.Fatalf("decode discussion: %v", err)
	}
	conversationID, _ := conversation["discussion_id"].(string)
	if conversationID == "" {
		t.Fatalf("conversation id missing from discussion response: %+v", conversation)
	}
	if conversation["status"] != "completed" {
		t.Fatalf("conversation status = %#v body = %s", conversation["status"], rec.Body.String())
	}
	messages, _ := conversation["messages"].([]any)
	if len(messages) < 2 {
		t.Fatalf("messages = %+v, want user and assistant messages", messages)
	}
	assistant, _ := messages[len(messages)-1].(map[string]any)
	if assistant["content"] != "[@superadmin](mention://user/system-superadmin) What deadline should I optimize for?" {
		t.Fatalf("assistant message = %+v", assistant)
	}
	// Native Codex notifications are projections, including empty tool results.
	// They must never be replayed as application commands after turn completion.
	// Real Mission creation and coordinator lanes are exercised by
	// TestDiscussionSessionToolCallsCreateRealMissionNativeRecords.
	linkedMissions, _ := conversation["linked_missions"].([]any)
	if len(linkedMissions) != 0 {
		t.Fatalf("native tool notifications were replayed: %+v", linkedMissions)
	}
	assertCodexRuntimeFixtureHasNoMissions(t, handler, loginCookie)

	deleteDiscussion := httptest.NewRequest(http.MethodDelete, "/api/discussions/"+conversationID, nil)
	deleteDiscussion.Header.Set("X-User-ID", "superadmin")
	deleteDiscussion.Header.Set("X-Viewer-Kind", "user")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, deleteDiscussion)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete discussion status = %d body = %s", rec.Code, rec.Body.String())
	}

	assertCodexRuntimeFixtureHasNoMissions(t, handler, loginCookie)
}

func TestProductionDiscussionStreamSendUsesConfiguredCodexRuntimeProcess(t *testing.T) {
	root := t.TempDir()
	executable := writeCodexRuntimeFixture(t)
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", productionTestDatabaseURLs(root))
	setTestConfigOverride(t, "discussion.agent.runtime_kind", "codex")
	setTestConfigOverride(t, "discussion.agent.runtime_config", `{"executable":"`+jsonPath(executable)+`","extra_args":["--model","gpt-5-codex"]}`)
	setTestConfigOverride(t, "discussion.agent.timeout", "2s")
	setTestConfigOverride(t, "auth.local.enabled", "true")
	selectTestServerConfig(t)
	handler, cleanup, err := newServerHandler(context.Background(), false)
	if err != nil {
		t.Fatalf("newServerHandler production: %v", err)
	}
	defer cleanup()
	configureRuntimeFixtureLLMProfile(t, handler, "gpt-5-codex")
	org := createProductionWorkerOrgTree(t, handler)
	loginCookie := loginProductionSuperadmin(t, handler)

	worker := httptest.NewRequest(http.MethodPut, "/api/workforce/members/agent-worker", jsonBody(`{"agent_instance_id":"agent-worker","real_name":"Worker Agent","agent_kind":"ai","employment_type":"ai_agent","organization_id":"`+org.OrganizationID+`","department_id":"`+org.DepartmentID+`","position_id":"`+org.PositionID+`","persona":{"backstory":"Launch coordination worker","responsibility_boundary":"Own launch execution tasks"},"enabled":true}`))
	worker.AddCookie(loginCookie)
	worker.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, worker)
	if rec.Code != http.StatusOK {
		t.Fatalf("create worker status = %d body = %s", rec.Code, rec.Body.String())
	}

	send := httptest.NewRequest(http.MethodPost, "/api/discussions/messages", jsonBody(`{"content":"Plan a launch"}`))
	send.Header.Set("X-User-ID", "superadmin")
	send.Header.Set("X-Viewer-Kind", "user")
	send.Header.Set("X-User-Display-Name", "superadmin")
	send.Header.Set("Idempotency-Key", "codex-pm-stream-send-1")
	send.Header.Set("Accept", "text/event-stream")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, send)
	if rec.Code != http.StatusOK {
		t.Fatalf("pm stream send status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"event: snapshot", "event: run_started", "event: tool_call", "event: message_completed", "event: run_completed"} {
		if !strings.Contains(body, want) {
			t.Fatalf("pm stream missing %q: %s", want, body)
		}
	}
	if !strings.Contains(body, "What deadline should I optimize for?") || !strings.Contains(body, "mission_prepare") {
		t.Fatalf("pm stream did not include configured runtime output and tool call: %s", body)
	}

	list := httptest.NewRequest(http.MethodGet, "/api/discussions", nil)
	list.Header.Set("X-User-ID", "superadmin")
	list.Header.Set("X-Viewer-Kind", "user")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, list)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"linked_missions":0`) {
		t.Fatalf("native tool notifications created a linked mission after stream send, status = %d body = %s", rec.Code, rec.Body.String())
	}
	assertCodexRuntimeFixtureHasNoMissions(t, handler, loginCookie)
}

func assertCodexRuntimeFixtureHasNoMissions(t *testing.T, handler http.Handler, cookie *http.Cookie) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/missions?limit=10", nil)
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list missions status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	var page missioncontrol.MissionListDTO
	if err := json.NewDecoder(recorder.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if len(page.Missions) != 0 {
		t.Fatalf("native tool display events produced business side effects: %+v", page.Missions)
	}
}

func TestProductionDiscussionSendPersistsConfiguredAgentIdentity(t *testing.T) {
	root := t.TempDir()
	executable := writeCodexTextRuntimeFixture(t)
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", productionTestDatabaseURLs(root))
	setTestConfigOverride(t, "discussion.agent_id", "custom-discussion-agent")
	setTestConfigOverride(t, "discussion.agent.runtime_kind", "codex")
	setTestConfigOverride(t, "discussion.agent.runtime_config", `{"executable":"`+jsonPath(executable)+`"}`)
	setTestConfigOverride(t, "discussion.agent.timeout", "2s")
	selectTestServerConfig(t)
	handler, cleanup, err := newServerHandler(context.Background(), false)
	if err != nil {
		t.Fatalf("newServerHandler production: %v", err)
	}
	defer cleanup()
	configureRuntimeFixtureLLMProfile(t, handler, "gpt-5-codex")

	send := httptest.NewRequest(http.MethodPost, "/api/discussions/messages", jsonBody(`{"content":"Plan a launch"}`))
	send.Header.Set("X-User-ID", "superadmin")
	send.Header.Set("X-Viewer-Kind", "user")
	send.Header.Set("X-User-Display-Name", "superadmin")
	send.Header.Set("Idempotency-Key", "custom-pm-send-1")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, send)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("discussion send status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"agent_id":"custom-discussion-agent"`) || !strings.Contains(body, `"author_id":"custom-discussion-agent"`) {
		t.Fatalf("discussion did not persist configured agent identity: %s", body)
	}
	var conversation map[string]any
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&conversation); err != nil {
		t.Fatalf("decode discussion: %v", err)
	}
	conversationID, _ := conversation["discussion_id"].(string)
	debugReq := httptest.NewRequest(http.MethodGet, "/api/discussions/"+conversationID+"/debug/latest", nil)
	debugReq.Header.Set("X-User-ID", "superadmin")
	debugReq.Header.Set("X-Viewer-Kind", "user")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, debugReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("pm debug status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"agent_id":"custom-discussion-agent"`) {
		t.Fatalf("pm debug did not capture configured agent identity: %s", rec.Body.String())
	}
}
