package website

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"orrery_v2/internal/gateway/protocol"
)

type fakeGateway struct {
	mu        sync.Mutex
	inputs    []protocol.SubmitTurnInput
	block     chan struct{}
	responses [][]protocol.Event
}

func (f *fakeGateway) SubmitTurn(ctx context.Context, input protocol.SubmitTurnInput) <-chan protocol.Event {
	f.mu.Lock()
	attempt := len(f.inputs)
	f.inputs = append(f.inputs, input)
	events := []protocol.Event{
		{Type: protocol.EventAssistantTextDelta, Text: "Orrery 是公开咨询智能体。"},
		{Type: protocol.EventTurnCompleted, FinishReason: "completed"},
	}
	if attempt < len(f.responses) {
		events = append([]protocol.Event(nil), f.responses[attempt]...)
	}
	f.mu.Unlock()
	ch := make(chan protocol.Event, 4)
	go func() {
		defer close(ch)
		if f.block != nil {
			select {
			case <-f.block:
			case <-ctx.Done():
				return
			}
		}
		for _, event := range events {
			ch <- event
		}
	}()
	return ch
}

func (f *fakeGateway) ActiveTurnSnapshot(sessionKey string) protocol.ActiveTurnSnapshot {
	return protocol.ActiveTurnSnapshot{SessionKey: sessionKey}
}

func (f *fakeGateway) lastInput() protocol.SubmitTurnInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.inputs) == 0 {
		return protocol.SubmitTurnInput{}
	}
	return f.inputs[len(f.inputs)-1]
}

func (f *fakeGateway) inputCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.inputs)
}

func (f *fakeGateway) inputsSnapshot() []protocol.SubmitTurnInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]protocol.SubmitTurnInput(nil), f.inputs...)
}

func TestHandlerCreatesUniqueOpaqueSessionsAndAuthenticatesByToken(t *testing.T) {
	handler := NewHandler(&fakeGateway{}, Options{AllowedOrigins: []string{"https://www.minvia.cn"}})
	first := createSession(t, handler, "https://www.minvia.cn")
	second := createSession(t, handler, "https://www.minvia.cn")

	if first.SessionID == second.SessionID || first.AccessToken == second.AccessToken {
		t.Fatalf("sessions must be unique: first=%+v second=%+v", first, second)
	}
	if !strings.HasPrefix(first.SessionID, "adv_") || len(first.AccessToken) < 32 || first.ExpiresAt.IsZero() {
		t.Fatalf("session is not opaque or expiring: %+v", first)
	}

	wrong := sendMessage(handler, first.SessionID, "wrong-token", "https://www.minvia.cn", `{"message":"Orrery 是什么？"}`)
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token status = %d body=%s", wrong.Code, wrong.Body.String())
	}
	valid := sendMessage(handler, first.SessionID, first.AccessToken, "https://www.minvia.cn", `{"message":"Orrery 是什么？"}`)
	if valid.Code != http.StatusOK {
		t.Fatalf("valid token status = %d body=%s", valid.Code, valid.Body.String())
	}
}

func TestHandlerPassesBoundedWebsiteConversationContextToAgent(t *testing.T) {
	store := NewMemoryWebsiteHistoryStore()
	gateway := &fakeGateway{}
	handler := NewHandler(gateway, Options{
		AllowedOrigins: []string{"https://www.minvia.cn"},
		HistoryStore:   store,
	})
	session := createSession(t, handler, "https://www.minvia.cn")

	first := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"请分析用友 ERP 与飞书审批的接入路径","client_message_id":"context-first"}`)
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d body=%s", first.Code, first.Body.String())
	}
	secondQuestion := "请基于刚才的方案收敛为三个实施阶段"
	second := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"`+secondQuestion+`","client_message_id":"context-second"}`)
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d body=%s", second.Code, second.Body.String())
	}

	input := gateway.lastInput()
	for _, want := range []string{
		"请分析用友 ERP 与飞书审批的接入路径",
		"Orrery 是公开咨询智能体。",
		secondQuestion,
	} {
		if !strings.Contains(input.Message, want) {
			t.Fatalf("agent input does not contain %q: %s", want, input.Message)
		}
	}
	if strings.Count(input.Message, secondQuestion) != 1 {
		t.Fatalf("current question must appear once: %s", input.Message)
	}
}

func TestHandlerRejectsExpiredSessionToken(t *testing.T) {
	now := time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC)
	handler := NewHandler(&fakeGateway{}, Options{
		AllowedOrigins: []string{"https://www.minvia.cn"},
		Now:            func() time.Time { return now },
	})
	session := createSession(t, handler, "https://www.minvia.cn")
	now = now.Add(31 * time.Minute)

	rec := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"Orrery 如何连接 SAP 并保留审计证据？"}`)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "advisor_session_expired") {
		t.Fatalf("expired session response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerRejectsDisallowedOriginAndOversizedMessage(t *testing.T) {
	handler := NewHandler(&fakeGateway{}, Options{AllowedOrigins: []string{"https://www.minvia.cn"}})
	forbidden := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/advisor/sessions", nil)
	req.Header.Set("Origin", "https://attacker.example")
	handler.ServeHTTP(forbidden, req)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("forbidden origin status = %d", forbidden.Code)
	}

	session := createSession(t, handler, "https://www.minvia.cn")
	payload, _ := json.Marshal(map[string]string{"message": strings.Repeat("问", 2001)})
	tooLong := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", string(payload))
	if tooLong.Code != http.StatusBadRequest || !strings.Contains(tooLong.Body.String(), "advisor_message_too_long") {
		t.Fatalf("oversized message response = %d %s", tooLong.Code, tooLong.Body.String())
	}
}

func TestHandlerRejectsConcurrentTurnForSameSession(t *testing.T) {
	gateway := &fakeGateway{block: make(chan struct{})}
	handler := NewHandler(gateway, Options{AllowedOrigins: []string{"https://www.minvia.cn"}})
	session := createSession(t, handler, "https://www.minvia.cn")
	server := httptest.NewServer(handler)
	defer server.Close()

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/advisor/sessions/"+session.SessionID+"/messages", strings.NewReader(`{"message":"first","client_message_id":"concurrent-first"}`))
		req.Header.Set("Origin", "https://www.minvia.cn")
		req.Header.Set("Authorization", "Bearer "+session.AccessToken)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	waitForGatewayInput(t, gateway)

	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/advisor/sessions/"+session.SessionID+"/messages", strings.NewReader(`{"message":"second","client_message_id":"concurrent-second"}`))
	req.Header.Set("Origin", "https://www.minvia.cn")
	req.Header.Set("Authorization", "Bearer "+session.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("second message: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("concurrent status = %d", resp.StatusCode)
	}
	close(gateway.block)
	<-firstDone
}

func TestHandlerStreamsSafeGatewayEventsAndFixesWebsiteInput(t *testing.T) {
	gateway := &fakeGateway{}
	handler := NewHandler(gateway, Options{AllowedOrigins: []string{"https://www.minvia.cn"}})
	session := createSession(t, handler, "https://www.minvia.cn")
	rec := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"Orrery 可以做什么？","page_path":"/zh-CN","language":"zh-CN","agent_id":"other","folder_id":"internal"}`)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream response = %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	var eventTypes []string
	scanner := bufio.NewScanner(bytes.NewReader(rec.Body.Bytes()))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			t.Fatalf("decode event: %v", err)
		}
		eventTypes = append(eventTypes, event["type"].(string))
	}
	if strings.Join(eventTypes, ",") != "assistant_text_delta,turn_completed" {
		t.Fatalf("event types = %+v body=%s", eventTypes, rec.Body.String())
	}
	input := gateway.lastInput()
	if input.ChannelKey != protocol.ChannelWebsite || input.SessionKey != "website:"+session.SessionID || input.Message != "Orrery 可以做什么？" {
		t.Fatalf("gateway input = %+v", input)
	}
	if input.Telemetry["page_path"] != "/zh-CN" || input.Telemetry["language"] != "zh-CN" || input.Identity == nil || !input.Identity.Verified || input.Identity.ChatKind != "direct" {
		t.Fatalf("gateway telemetry/identity = %+v %+v", input.Telemetry, input.Identity)
	}
	if input.AdvisorTier != protocol.AdvisorTierPublic || input.Identity.AccountID != "" || input.Identity.ExternalTenantID != "" {
		t.Fatalf("public website authority = tier=%q identity=%+v", input.AdvisorTier, input.Identity)
	}
}

func TestHandlerPreservesApprovedSourceMetadata(t *testing.T) {
	gateway := &fakeGateway{responses: [][]protocol.Event{{
		{Type: protocol.EventAssistantTextDelta, Text: "Orrery 提供企业智能体治理能力。", Sources: []string{overviewSource}},
		{Type: protocol.EventTurnCompleted, FinishReason: "completed"},
	}}}
	handler := NewHandler(gateway, Options{AllowedOrigins: []string{"https://www.minvia.cn"}})
	session := createSession(t, handler, "https://www.minvia.cn")
	response := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"请结合公开资料说明 Orrery 的治理能力","language":"zh-CN"}`)

	var assistant struct {
		Type    string   `json:"type"`
		Text    string   `json:"text"`
		Sources []string `json:"sources"`
	}
	for _, line := range strings.Split(response.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var candidate struct {
			Type    string   `json:"type"`
			Text    string   `json:"text"`
			Sources []string `json:"sources"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &candidate); err != nil {
			t.Fatal(err)
		}
		if candidate.Type == string(protocol.EventAssistantTextDelta) {
			assistant = candidate
		}
	}
	if assistant.Text != "Orrery 提供企业智能体治理能力。" || len(assistant.Sources) != 1 || assistant.Sources[0] != overviewSource {
		t.Fatalf("assistant event = %+v body=%s", assistant, response.Body.String())
	}
}

func TestHandlerUsesVerifiedCustomerTierWithoutTenantAuthority(t *testing.T) {
	now := time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC)
	assertionKey := []byte("website-assertion-key-0123456789abcdef")
	ownerKey := []byte("website-owner-hmac-key-0123456789abcdef")
	verifier, err := NewWebsiteIdentityAssertionVerifier(assertionKey, func() time.Time { return now })
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	gateway := &fakeGateway{}
	handler := NewHandler(gateway, Options{
		AllowedOrigins: []string{"https://www.minvia.cn"}, IdentityAssertions: verifier,
		OwnerHMACKey: ownerKey, HistoryStore: NewMemoryWebsiteHistoryStore(), Now: func() time.Time { return now },
	})
	assertion := signWebsiteClaims(t, assertionKey, validWebsiteClaims(now, protocol.AdvisorTierCustomer, "customer-create"))
	session := createSessionWithAssertion(t, handler, "https://www.minvia.cn", assertion)
	rec := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"请详细介绍私有部署"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("customer message = %d %s", rec.Code, rec.Body.String())
	}
	input := gateway.lastInput()
	if input.AdvisorTier != protocol.AdvisorTierCustomer || input.Identity == nil || !input.Identity.Verified {
		t.Fatalf("customer route = tier=%q identity=%+v", input.AdvisorTier, input.Identity)
	}
	if input.Identity.AccountID != "" || input.Identity.ExternalTenantID != "" || input.Identity.ExternalUserID == "customer-1" {
		t.Fatalf("customer authority crossed boundary: %+v", input.Identity)
	}
}

func TestHandlerPublicOnlyKeepsVerifiedCustomerAssertionsOnPublicTier(t *testing.T) {
	now := time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC)
	assertionKey := []byte("website-assertion-key-0123456789abcdef")
	ownerKey := []byte("website-owner-hmac-key-0123456789abcdef")
	verifier, err := NewWebsiteIdentityAssertionVerifier(assertionKey, func() time.Time { return now })
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	gateway := &fakeGateway{}
	handler := NewHandler(gateway, Options{
		AllowedOrigins: []string{"https://www.minvia.cn"}, IdentityAssertions: verifier,
		OwnerHMACKey: ownerKey, HistoryStore: NewMemoryWebsiteHistoryStore(), Now: func() time.Time { return now },
		PublicOnly: true,
	})
	assertion := signWebsiteClaims(t, assertionKey, validWebsiteClaims(now, protocol.AdvisorTierCustomer, "public-only-create"))
	session := createSessionWithAssertion(t, handler, "https://www.minvia.cn", assertion)
	rec := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"请详细介绍私有部署"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("public-only message = %d %s", rec.Code, rec.Body.String())
	}
	input := gateway.lastInput()
	if input.AdvisorTier != protocol.AdvisorTierPublic || input.Identity == nil || input.Identity.ExternalUserID != session.SessionID {
		t.Fatalf("public-only route = tier=%q identity=%+v", input.AdvisorTier, input.Identity)
	}

	historyAssertion := signWebsiteClaims(t, assertionKey, validWebsiteClaims(now, protocol.AdvisorTierCustomer, "public-only-history"))
	historyRequest := httptest.NewRequest(http.MethodGet, "/api/advisor/conversations", nil)
	historyRequest.Header.Set("Origin", "https://www.minvia.cn")
	historyRequest.Header.Set("X-Website-Identity", historyAssertion)
	history := httptest.NewRecorder()
	handler.ServeHTTP(history, historyRequest)
	if history.Code != http.StatusForbidden {
		t.Fatalf("public-only history = %d %s", history.Code, history.Body.String())
	}
}

func TestHandlerRejectsInvalidOrReplayedAssertionInsteadOfDowngrading(t *testing.T) {
	now := time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC)
	assertionKey := []byte("website-assertion-key-0123456789abcdef")
	verifier, _ := NewWebsiteIdentityAssertionVerifier(assertionKey, func() time.Time { return now })
	handler := NewHandler(&fakeGateway{}, Options{
		AllowedOrigins: []string{"https://www.minvia.cn"}, IdentityAssertions: verifier,
		OwnerHMACKey: []byte("website-owner-hmac-key-0123456789abcdef"), HistoryStore: NewMemoryWebsiteHistoryStore(), Now: func() time.Time { return now },
	})
	invalid := createSessionWithAssertionRecorder(handler, "https://www.minvia.cn", "forged")
	if invalid.Code != http.StatusUnauthorized || !strings.Contains(invalid.Body.String(), "advisor_identity_invalid") {
		t.Fatalf("invalid assertion = %d %s", invalid.Code, invalid.Body.String())
	}
	assertion := signWebsiteClaims(t, assertionKey, validWebsiteClaims(now, protocol.AdvisorTierCustomer, "replayed-create"))
	first := createSessionWithAssertionRecorder(handler, "https://www.minvia.cn", assertion)
	if first.Code != http.StatusCreated {
		t.Fatalf("first assertion = %d %s", first.Code, first.Body.String())
	}
	replay := createSessionWithAssertionRecorder(handler, "https://www.minvia.cn", assertion)
	if replay.Code != http.StatusUnauthorized || !strings.Contains(replay.Body.String(), "advisor_identity_invalid") {
		t.Fatalf("replayed assertion = %d %s", replay.Code, replay.Body.String())
	}
}

func TestHandlerRetriesOneRecoverableFailureBeforeStreaming(t *testing.T) {
	gateway := &fakeGateway{responses: [][]protocol.Event{
		{{Type: protocol.EventError, Code: protocol.ErrorCodeGatewayRuntimeUnavailable}},
		{
			{Type: protocol.EventAssistantTextDelta, Text: "公开回答\n来源：minviabrain://08-Wiki/Public/Orrery/integration-smoke.md"},
			{Type: protocol.EventTurnCompleted, FinishReason: "completed"},
		},
	}}
	handler := NewHandler(gateway, Options{AllowedOrigins: []string{"https://www.minvia.cn"}})
	session := createSession(t, handler, "https://www.minvia.cn")

	rec := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"Orrery 如何连接 SAP 并保留审计证据？"}`)

	if gateway.inputCount() != 2 {
		t.Fatalf("gateway attempts = %d, want 2", gateway.inputCount())
	}
	inputs := gateway.inputsSnapshot()
	if inputs[0].RunID == "" || inputs[0].RunID != inputs[1].RunID {
		t.Fatalf("retry run IDs = %q, %q", inputs[0].RunID, inputs[1].RunID)
	}
	if strings.Contains(rec.Body.String(), `"type":"error"`) || !strings.Contains(rec.Body.String(), `"type":"turn_completed"`) {
		t.Fatalf("retry stream = %s", rec.Body.String())
	}
}

func TestHandlerStopsAfterSecondRecoverableFailure(t *testing.T) {
	gateway := &fakeGateway{responses: [][]protocol.Event{
		{{Type: protocol.EventError, Code: protocol.ErrorCodeGatewayRuntimeUnavailable}},
		{{Type: protocol.EventError, Code: protocol.ErrorCodeGatewayRuntimeUnavailable}},
	}}
	handler := NewHandler(gateway, Options{AllowedOrigins: []string{"https://www.minvia.cn"}})
	session := createSession(t, handler, "https://www.minvia.cn")

	rec := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"Orrery 如何连接 SAP 并保留审计证据？"}`)

	if gateway.inputCount() != 2 || strings.Count(rec.Body.String(), `"type":"error"`) != 1 {
		t.Fatalf("retry attempts/body = %d %s", gateway.inputCount(), rec.Body.String())
	}
}

func TestHandlerDoesNotRetryAfterAssistantText(t *testing.T) {
	gateway := &fakeGateway{responses: [][]protocol.Event{
		{
			{Type: protocol.EventAssistantTextDelta, Text: "partial"},
			{Type: protocol.EventError, Code: protocol.ErrorCodeGatewayRuntimeUnavailable},
		},
		{
			{Type: protocol.EventAssistantTextDelta, Text: "unexpected retry"},
			{Type: protocol.EventTurnCompleted, FinishReason: "completed"},
		},
	}}
	handler := NewHandler(gateway, Options{AllowedOrigins: []string{"https://www.minvia.cn"}})
	session := createSession(t, handler, "https://www.minvia.cn")

	rec := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"Orrery 如何连接 SAP 并保留审计证据？"}`)

	if gateway.inputCount() != 1 || !strings.Contains(rec.Body.String(), "partial") || strings.Count(rec.Body.String(), `"type":"error"`) != 1 {
		t.Fatalf("partial response attempts/body = %d %s", gateway.inputCount(), rec.Body.String())
	}
}

func TestHandlerLimitsMessagesPerMinute(t *testing.T) {
	now := time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC)
	handler := NewHandler(&fakeGateway{}, Options{AllowedOrigins: []string{"https://www.minvia.cn"}, Now: func() time.Time { return now }})
	session := createSession(t, handler, "https://www.minvia.cn")
	for i := 0; i < 10; i++ {
		rec := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"Orrery 是什么？"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("message %d status = %d body=%s", i, rec.Code, rec.Body.String())
		}
	}
	limited := sendMessage(handler, session.SessionID, session.AccessToken, "https://www.minvia.cn", `{"message":"Orrery 是什么？"}`)
	if limited.Code != http.StatusTooManyRequests || !strings.Contains(limited.Body.String(), "advisor_rate_limited") {
		t.Fatalf("rate limited response = %d %s", limited.Code, limited.Body.String())
	}
}

func createSession(t *testing.T, handler http.Handler, origin string) SessionResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/advisor/sessions", nil)
	req.Header.Set("Origin", origin)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create session status = %d body=%s", rec.Code, rec.Body.String())
	}
	var response SessionResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	return response
}

func createSessionWithAssertion(t *testing.T, handler http.Handler, origin, assertion string) SessionResponse {
	t.Helper()
	rec := createSessionWithAssertionRecorder(handler, origin, assertion)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create asserted session status = %d body=%s", rec.Code, rec.Body.String())
	}
	var response SessionResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode asserted session: %v", err)
	}
	return response
}

func createSessionWithAssertionRecorder(handler http.Handler, origin, assertion string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/advisor/sessions", nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("X-Website-Identity", assertion)
	handler.ServeHTTP(rec, req)
	return rec
}

func sendMessage(handler http.Handler, sessionID, token, origin, body string) *httptest.ResponseRecorder {
	var payload map[string]any
	if json.Unmarshal([]byte(body), &payload) == nil {
		if _, exists := payload["client_message_id"]; !exists {
			payload["client_message_id"] = fmt.Sprintf("test-message-%d", testClientMessageCounter.Add(1))
			if encoded, err := json.Marshal(payload); err == nil {
				body = string(encoded)
			}
		}
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/advisor/sessions/"+sessionID+"/messages", strings.NewReader(body))
	req.Header.Set("Origin", origin)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	return rec
}

var testClientMessageCounter atomic.Uint64

func waitForGatewayInput(t *testing.T, gateway *fakeGateway) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		gateway.mu.Lock()
		count := len(gateway.inputs)
		gateway.mu.Unlock()
		if count > 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for gateway input")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}
