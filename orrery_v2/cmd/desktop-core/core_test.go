package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	agentcore "orrery_v2/internal/agent/core"
)

type testBackend struct {
	mu       sync.Mutex
	commands []agentcore.RuntimeCommand
	reject   agentcore.RuntimeCommandKind
	events   chan agentcore.SessionEnvelope
	closed   sync.Once
}

func (b *testBackend) Dispatch(_ context.Context, command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.commands = append(b.commands, command)
	if command.Kind == b.reject {
		return agentcore.CommandReceipt{}, errors.New("rejected")
	}
	return agentcore.CommandReceipt{Accepted: true, TurnGeneration: 1}, nil
}

func (b *testBackend) Events() <-chan agentcore.SessionEnvelope { return b.events }
func (b *testBackend) Capabilities() agentcore.Capabilities     { return agentcore.Capabilities{} }
func (b *testBackend) PendingPermissionRequests() []agentcore.PendingPermissionView {
	return nil
}

type testConnection struct {
	mu       sync.Mutex
	opens    int
	backends []*testBackend
	openErr  bool
}

func (c *testConnection) OpenSession(_ context.Context, _ agentcore.SessionSpec, _ agentcore.SessionConfig) (agentcore.SessionBackend, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.opens++
	if c.openErr {
		return nil, errors.New("not available")
	}
	b := &testBackend{events: make(chan agentcore.SessionEnvelope, 16)}
	c.backends = append(c.backends, b)
	return b, nil
}
func (c *testConnection) CloseSession(context.Context, agentcore.SessionID) error { return nil }
func (c *testConnection) Capabilities(context.Context) agentcore.Capabilities {
	return agentcore.Capabilities{}
}
func (c *testConnection) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, backend := range c.backends {
		backend.closed.Do(func() { close(backend.events) })
	}
	return nil
}

func testCore(t *testing.T) (*localCore, *testConnection) {
	t.Helper()
	connection := &testConnection{}
	c := &localCore{
		conversations: make(map[string]*conversation), workspaceRefs: make(map[string]string),
		clients: make(map[*websocketClient]struct{}), claude: connection,
	}
	t.Cleanup(func() { _ = c.close() })
	return c, connection
}

func requestCore(t *testing.T, c *localCore, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	c.ServeHTTP(w, httptest.NewRequest(method, "http://127.0.0.1"+path, strings.NewReader(body)))
	return w
}

func createTestConversation(t *testing.T, c *localCore, id, workspace string) *conversation {
	t.Helper()
	data, err := json.Marshal(map[string]any{"id": id, "extra": map[string]any{"workspace": workspace}})
	if err != nil {
		t.Fatal(err)
	}
	w := requestCore(t, c, http.MethodPost, "/api/conversations", string(data))
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	return c.conversations[id]
}

func TestCreateConversationPreservesExistingSession(t *testing.T) {
	c, _ := testCore(t)
	workspace := t.TempDir()
	conv := createTestConversation(t, c, "same", workspace)
	conv.state = "running"
	if repeated := createTestConversation(t, c, "same", workspace); repeated != conv || repeated.state != "running" {
		t.Fatal("retry replaced the active conversation")
	}
	body, _ := json.Marshal(map[string]any{"id": "same", "extra": map[string]any{"workspace": t.TempDir()}})
	if w := requestCore(t, c, http.MethodPost, "/api/conversations", string(body)); w.Code != http.StatusConflict {
		t.Fatalf("workspace change: %d %s", w.Code, w.Body.String())
	}
	if w := requestCore(t, c, http.MethodPost, "/api/conversations", `{"assistant":{"id":"unknown"}}`); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown assistant accepted: %d", w.Code)
	}
}

func TestConcurrentSendOpensOneSession(t *testing.T) {
	c, connection := testCore(t)
	createTestConversation(t, c, "concurrent", t.TempDir())
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- requestCore(t, c, http.MethodPost, "/api/conversations/concurrent/messages", `{"content":"hello"}`).Code
		}()
	}
	wg.Wait()
	close(results)
	counts := map[int]int{}
	for status := range results {
		counts[status]++
	}
	if counts[http.StatusOK] != 1 || counts[http.StatusConflict] != 1 || connection.opens != 1 {
		t.Fatalf("statuses=%v opens=%d", counts, connection.opens)
	}
}

func TestProviderFailuresAreNotSuccessfulResponses(t *testing.T) {
	c, connection := testCore(t)
	conv := createTestConversation(t, c, "failures", t.TempDir())
	connection.openErr = true
	if w := requestCore(t, c, http.MethodPost, "/api/conversations/failures/messages", `{"content":"hello"}`); w.Code != http.StatusBadGateway {
		t.Fatalf("failed launch: %d", w.Code)
	}
	connection.openErr = false
	if w := requestCore(t, c, http.MethodPost, "/api/conversations/failures/messages", `{"content":"hello"}`); w.Code != http.StatusOK {
		t.Fatalf("retry: %d", w.Code)
	}
	backend := conv.backend.(*testBackend)
	backend.reject = agentcore.RuntimeCommandCancel
	body, _ := json.Marshal(map[string]string{"turn_id": conv.turnID})
	if w := requestCore(t, c, http.MethodPost, "/api/conversations/failures/cancel", string(body)); w.Code != http.StatusBadGateway || conv.state != "running" {
		t.Fatalf("failed cancel: %d state=%s", w.Code, conv.state)
	}
	backend.reject = ""
	if w := requestCore(t, c, http.MethodPost, "/api/conversations/failures/cancel", `{"turn_id":"stale"}`); w.Code != http.StatusConflict {
		t.Fatalf("stale cancel: %d", w.Code)
	}
	if w := requestCore(t, c, http.MethodPost, "/api/conversations/failures/cancel", string(body)); w.Code != http.StatusOK || conv.state != "cancelling" {
		t.Fatalf("accepted cancel: %d state=%s", w.Code, conv.state)
	}
}

func TestPermissionDecisionsFailClosed(t *testing.T) {
	permission := agentcore.PermissionEvent{RequestID: "request", Kind: agentcore.PermissionKindTool}
	for _, data := range []string{"", "null", "true", "false", `"unknown"`, `{}`, `{"decision":"approvd"}`, `{"decision":"approved","unexpected":true}`} {
		if answer, err := permissionAnswer(permission, json.RawMessage(data), false); err == nil {
			t.Fatalf("ambiguous %q accepted: %#v", data, answer)
		}
	}
	for _, test := range []struct {
		data     string
		always   bool
		expected agentcore.PermissionDecision
	}{
		{`"approved"`, false, agentcore.PermissionApproved},
		{`"approved"`, true, agentcore.PermissionAllowAlways},
		{`"allow_always"`, false, agentcore.PermissionAllowAlways},
		{`"denied"`, true, agentcore.PermissionDenied},
		{`{"decision":"denied"}`, true, agentcore.PermissionDenied},
	} {
		answer, err := permissionAnswer(permission, json.RawMessage(test.data), test.always)
		if err != nil || answer.Decision != test.expected {
			t.Fatalf("%s: answer=%#v err=%v", test.data, answer, err)
		}
	}
	permission.Context = &agentcore.PermissionContext{Options: []agentcore.PermissionOption{
		{OptionID: "yes", Kind: "allow_once"}, {OptionID: "no", Kind: "reject_once"},
	}}
	answer, err := permissionAnswer(permission, json.RawMessage(`"no"`), true)
	if err != nil || answer.Decision != agentcore.PermissionDenied {
		t.Fatalf("provider rejection: %#v %v", answer, err)
	}
	for _, data := range []string{`"approved"`, `"missing"`, `{"decision":"approved"}`} {
		if _, err := permissionAnswer(permission, json.RawMessage(data), false); err == nil {
			t.Fatalf("unadvertised provider decision accepted: %s", data)
		}
	}
	permission.Kind = agentcore.PermissionKindAuth
	if _, err := permissionAnswer(permission, json.RawMessage(`"approved"`), false); err == nil {
		t.Fatal("tool confirmation accepted an authentication request")
	}
}

func TestQuestionsRequireCompleteExplicitChoices(t *testing.T) {
	permission := agentcore.PermissionEvent{RequestID: "question", Kind: agentcore.PermissionKindTool, Questions: []agentcore.PermissionQuestion{
		{Question: "Which?", Options: []agentcore.PermissionQuestionOption{{Label: "A"}, {Label: "B"}}},
	}}
	for _, data := range []string{`"approved"`, `{"decision":"approved"}`, `{"decision":"approved","selected":"unknown"}`} {
		if _, err := permissionAnswer(permission, json.RawMessage(data), false); err == nil {
			t.Fatalf("implicit choice accepted: %s", data)
		}
	}
	answer, err := permissionAnswer(permission, json.RawMessage(`{"decision":"approved","selected":"B"}`), false)
	if err != nil || len(answer.Answers) != 1 || answer.Answers[0].Labels[0] != "B" {
		t.Fatalf("explicit choice: %#v %v", answer, err)
	}
	permission.Questions = append(permission.Questions, agentcore.PermissionQuestion{
		Question: "Second?", Options: []agentcore.PermissionQuestionOption{{Label: "C"}},
	})
	if _, err := permissionAnswer(permission, json.RawMessage(`{"decision":"approved","answers":[{"question":"Which?","labels":["A"]}]}`), false); err == nil {
		t.Fatal("partial multiple-question response accepted")
	}
	if _, err := permissionAnswer(permission, json.RawMessage(`{"decision":"approved","answers":[{"question":"Which?","labels":["A"]},{"question":"Second?","labels":["C"]}]}`), false); err != nil {
		t.Fatalf("complete response rejected: %v", err)
	}
}

func TestConfirmIsScopedAndDoesNotDefaultToApproval(t *testing.T) {
	c, _ := testCore(t)
	conv := createTestConversation(t, c, "permission", t.TempDir())
	backend := &testBackend{events: make(chan agentcore.SessionEnvelope)}
	conv.backend = backend
	conv.pending["request"] = agentcore.PermissionEvent{RequestID: "request", Kind: agentcore.PermissionKindTool}
	path := "/api/conversations/permission/confirmations/request/confirm"
	for _, body := range []string{`{}`, `{"data":null}`, `{"msg_id":"other","data":"approved"}`} {
		if w := requestCore(t, c, http.MethodPost, path, body); w.Code != http.StatusBadRequest {
			t.Fatalf("invalid confirmation %s: %d", body, w.Code)
		}
	}
	if len(backend.commands) != 0 {
		t.Fatal("invalid confirmation reached the provider")
	}
	if w := requestCore(t, c, http.MethodPost, path, `{"msg_id":"request","data":"denied","always_allow":true}`); w.Code != http.StatusOK {
		t.Fatalf("deny failed: %d %s", w.Code, w.Body.String())
	}
	if len(conv.pending) != 0 || len(backend.commands) != 1 || backend.commands[0].AnswerPermission.Decision != agentcore.PermissionDenied {
		t.Fatal("denial was not consumed exactly once")
	}
	if w := requestCore(t, c, http.MethodPost, path, `{"data":"approved"}`); w.Code != http.StatusNotFound {
		t.Fatalf("replayed approval: %d", w.Code)
	}
}

func TestWebSocketEventsKeepConversationAndTerminalOutcome(t *testing.T) {
	c, _ := testCore(t)
	conv := createTestConversation(t, c, "events", t.TempDir())
	server := httptest.NewServer(c)
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	if conn, response, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"Origin": {"https://unrelated.example"}}); err == nil {
		conn.Close()
		t.Fatal("cross-origin websocket accepted")
	} else if response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin response: %v %v", response, err)
	}
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if w := requestCore(t, c, http.MethodPost, "/api/conversations/events/messages", `{"content":"hello"}`); w.Code != http.StatusOK {
		t.Fatalf("send: %d %s", w.Code, w.Body.String())
	}
	backend := conv.backend.(*testBackend)
	emit := func(sequence, generation uint64, event agentcore.SessionEvent) {
		backend.events <- agentcore.SessionEnvelope{
			SessionID: conv.sessionID, Sequence: sequence, TurnGeneration: generation, Timestamp: time.Now(),
			Source: agentcore.EventSourceProvider, Projection: agentcore.EventProjectionCanonical, Event: event,
		}
	}
	emit(1, 1, agentcore.SessionEvent{Kind: agentcore.EventPermission, Permission: &agentcore.PermissionEvent{
		RequestID: "ask", Kind: agentcore.PermissionKindTool, ToolName: "Read",
	}})
	emit(2, 1, agentcore.SessionEvent{Kind: agentcore.EventTurnResult, TurnResult: &agentcore.TurnResultEvent{
		IsError: true, Outcome: agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeCancelled, CancelReason: agentcore.CancelReasonUserCancel},
	}})
	emit(3, 0, agentcore.SessionEvent{Kind: agentcore.EventDetached, Detached: &agentcore.DetachedEvent{RedactedSummary: "closed"}})
	for _, expected := range []string{"confirmation.add", "confirmation.remove", "turn.completed"} {
		_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
		var wire struct {
			Name string         `json:"name"`
			Data map[string]any `json:"data"`
		}
		if err := ws.ReadJSON(&wire); err != nil {
			t.Fatal(err)
		}
		if wire.Name != expected || wire.Data["conversation_id"] != conv.id || wire.Data["turn_id"] != conv.turnID {
			t.Fatalf("event lost its scope: %#v", wire)
		}
		if expected == "turn.completed" && wire.Data["state"] != "cancelled" {
			t.Fatalf("cancellation projected as failure: %#v", wire)
		}
	}
	// The detached event cannot overwrite the terminal outcome, even after the
	// provider stream has been torn down and its session closed.
	conv.eventMu.Lock()
	c.mu.RLock()
	state := conv.state
	c.mu.RUnlock()
	conv.eventMu.Unlock()
	if state != "cancelled" {
		t.Fatalf("late disconnect changed terminal state: %s", state)
	}
}

func TestInvalidEventsAndLateTerminalDoNotChangeState(t *testing.T) {
	c, _ := testCore(t)
	conv := createTestConversation(t, c, "terminal", t.TempDir())
	conv.state = "completed"
	for _, event := range []agentcore.SessionEvent{
		{Kind: agentcore.EventMessageDelta},
		{Kind: agentcore.EventDetached, Detached: &agentcore.DetachedEvent{}},
		{Kind: agentcore.EventTurnResult, TurnResult: &agentcore.TurnResultEvent{Outcome: agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeFailed}}},
	} {
		if wire := c.eventWireLocked(conv, event); len(wire) != 0 || conv.state != "completed" {
			t.Fatalf("invalid or late event changed completion: %#v %s", wire, conv.state)
		}
	}
}
