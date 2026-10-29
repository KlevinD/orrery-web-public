package pi

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

var (
	piNativeHelperOnce sync.Once
	piNativeHelperPath string
	piNativeHelperErr  error
)

func buildPiNativeHelper(t *testing.T) string {
	t.Helper()
	piNativeHelperOnce.Do(func() {
		directory, err := os.MkdirTemp("", "orrery-pi-native-helper-")
		if err != nil {
			piNativeHelperErr = err
			return
		}
		piNativeHelperPath = filepath.Join(directory, "pi-native-helper")
		if runtime.GOOS == "windows" {
			piNativeHelperPath += ".exe"
		}
		command := exec.Command("go", "build", "-o", piNativeHelperPath, "./testdata/native-helper")
		command.Dir = "."
		if output, err := command.CombinedOutput(); err != nil {
			piNativeHelperErr = &piNativeBuildError{err: err, output: string(output)}
		}
	})
	if piNativeHelperErr != nil {
		t.Fatal(piNativeHelperErr)
	}
	return piNativeHelperPath
}

type piNativeBuildError struct {
	err    error
	output string
}

func (e *piNativeBuildError) Error() string { return e.err.Error() + ": " + e.output }

func openPiNativeTestSession(t *testing.T, manual bool, spec agentcore.SessionSpec) (*NativeConnection, *NativeSession) {
	return openPiNativeTestSessionWithContext(t, context.Background(), manual, spec)
}

func openPiNativeTestSessionWithContext(t *testing.T, parent context.Context, manual bool, spec agentcore.SessionSpec) (*NativeConnection, *NativeSession) {
	t.Helper()
	workDir := t.TempDir()
	providerConfig, err := json.Marshal(map[string]any{"executable": buildPiNativeHelper(t), "env": map[string]string{"PI_NATIVE_HELPER_MANUAL": boolString(manual)}})
	if err != nil {
		t.Fatal(err)
	}
	request := agentcore.RuntimeRequest{
		TenantID: "tenant-pi-native", RunID: "run-pi-native", AgentID: "agent-pi-native",
		RuntimeOptions: agentcore.RuntimeOptions{
			WorkingDirectory: workDir, ProviderConfig: providerConfig,
			WorkspacePolicy: agentcore.RuntimeWorkspacePolicy{Root: workDir, WorkDir: workDir, EnforceBoundary: true},
		},
	}
	connection, err := NewNativeConnection(NativeConfig{ResolveWorkspace: NativeWorkspaceResolverFunc(func(context.Context, string) (NativeWorkspace, error) {
		return NativeWorkspace{Request: request}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := connection.OpenSession(parent, spec, agentcore.SessionConfig{WorkspaceRef: "ws-pi-native"})
	if err != nil {
		connection.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return connection, backend.(*NativeSession)
}

func boolString(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func TestNativePiVersionCacheIgnoresPerRunEnvironment(t *testing.T) {
	connection, err := NewNativeConnection(NativeConfig{})
	if err != nil {
		t.Fatal(err)
	}
	executable := buildPiNativeHelper(t)
	for _, environment := range []map[string]string{
		{"PI_CODING_AGENT_DIR": filepath.Join(t.TempDir(), "first")},
		{"PI_CODING_AGENT_DIR": filepath.Join(t.TempDir(), "second"), "OPENAI_API_KEY": "different"},
	} {
		if _, err := connection.resolveVersion(context.Background(), executable, environment, ""); err != nil {
			t.Fatal(err)
		}
	}
	connection.mu.Lock()
	cacheEntries := len(connection.versions)
	connection.mu.Unlock()
	if cacheEntries != 1 {
		t.Fatalf("version cache entries = %d, want one per executable", cacheEntries)
	}
}

func TestNativePiVersionProbePreservesCancelledContext(t *testing.T) {
	connection, err := NewNativeConnection(NativeConfig{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = connection.resolveVersion(ctx, buildPiNativeHelper(t), nil, "cancelled-probe")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("resolve version error = %v, want context.Canceled", err)
	}
}

func TestNativePiHandshakePreservesProcessExitDiagnostic(t *testing.T) {
	workDir := t.TempDir()
	providerConfig, err := json.Marshal(map[string]any{
		"executable": buildPiNativeHelper(t),
		"env":        map[string]string{"PI_NATIVE_HELPER_EXIT_BEFORE_HANDSHAKE": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := agentcore.RuntimeRequest{
		TenantID: "tenant-pi-native", RunID: "run-pi-native-exit", AgentID: "agent-pi-native",
		RuntimeOptions: agentcore.RuntimeOptions{
			WorkingDirectory: workDir, ProviderConfig: providerConfig,
			WorkspacePolicy: agentcore.RuntimeWorkspacePolicy{Root: workDir, WorkDir: workDir, EnforceBoundary: true},
		},
	}
	connection, err := NewNativeConnection(NativeConfig{
		HandshakeTimeout: 5 * time.Second,
		ResolveWorkspace: NativeWorkspaceResolverFunc(func(context.Context, string) (NativeWorkspace, error) {
			return NativeWorkspace{Request: request}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	_, err = connection.OpenSession(context.Background(), agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "pi-native-handshake-exit"}, agentcore.SessionConfig{WorkspaceRef: "ws-pi-native"})
	if err == nil {
		t.Fatal("handshake unexpectedly succeeded")
	}
	var bridgeErr *agentcore.RuntimeBridgeError
	if !errors.As(err, &bridgeErr) || bridgeErr.Code != agentcore.RuntimeBridgeTransportClosed {
		t.Fatalf("handshake error = %v, want transport closed", err)
	}
	message := err.Error()
	for _, want := range []string{"exit code 17", "fixture exited before handshake", "[credential-redacted]", "[local-path-redacted]"} {
		if !strings.Contains(message, want) {
			t.Fatalf("handshake error %q is missing %q", message, want)
		}
	}
	for _, forbidden := range []string{"handshake_timeout", "fixture-sensitive-value", "/tmp/pi-private/config.json"} {
		if strings.Contains(message, forbidden) {
			t.Fatalf("handshake error leaked or misclassified %q: %s", forbidden, message)
		}
	}
}

func TestNativePiSessionPreservesParentContextForBridgeCallbacks(t *testing.T) {
	type contextKey struct{}
	key := contextKey{}
	parent := context.WithValue(context.Background(), key, "runtime-approval-context")
	_, session := openPiNativeTestSessionWithContext(t, parent, false, agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "pi-native-context"})
	if got := session.ctx.Value(key); got != "runtime-approval-context" {
		t.Fatalf("session context value = %v, want parent runtime approval context", got)
	}
}

func TestNativePiPersistentRPCMultiTurnAndControls(t *testing.T) {
	_, session := openPiNativeTestSession(t, false, agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "pi-native-multiturn"})
	drainPiHandshake(t, session.Events())

	first, err := session.Dispatch(context.Background(), piNativeSend("turn-1", "client-1", "first"))
	if err != nil || first.TurnGeneration != 1 {
		t.Fatalf("first dispatch = %#v, %v", first, err)
	}
	firstEvents := receivePiTerminal(t, session.Events(), 1)
	if !piEventsContainText(firstEvents, "turn 1 complete") || !piEventsContainKind(firstEvents, agentcore.EventUsageDelta) {
		t.Fatalf("first turn events = %#v", firstEvents)
	}

	second, err := session.Dispatch(context.Background(), piNativeSend("turn-2", "client-2", "second"))
	if err != nil || second.TurnGeneration != 2 {
		t.Fatalf("second dispatch = %#v, %v", second, err)
	}
	secondEvents := receivePiTerminal(t, session.Events(), 2)
	if !piEventsContainText(secondEvents, "turn 2 complete") {
		t.Fatalf("second turn events = %#v", secondEvents)
	}

	if _, err := session.Dispatch(context.Background(), piNativeSend("turn-3", "client-3", "hold")); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Dispatch(context.Background(), agentcore.RuntimeCommand{CommandID: "steer", Kind: agentcore.RuntimeCommandSteer, Steer: &agentcore.SteerCommand{Content: []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: "finish"}}}}); err != nil {
		t.Fatal(err)
	}
	steered := receivePiTerminal(t, session.Events(), 3)
	if !piEventsContainText(steered, "steered complete") {
		t.Fatalf("steered events = %#v", steered)
	}

	if _, err := session.Dispatch(context.Background(), agentcore.RuntimeCommand{CommandID: "set-model", Kind: agentcore.RuntimeCommandSetModel, SetModel: &agentcore.SetModelCommand{Model: "fixture/model-1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Dispatch(context.Background(), agentcore.RuntimeCommand{CommandID: "set-effort", Kind: agentcore.RuntimeCommandSetConfigOption, SetConfigOption: &agentcore.SetConfigOptionCommand{OptionID: "reasoning_effort", Value: "high"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Dispatch(context.Background(), agentcore.RuntimeCommand{CommandID: "stats", Kind: agentcore.RuntimeCommandQuerySessionInfo, QuerySessionInfo: &agentcore.QuerySessionInfoCommand{Kind: agentcore.SessionInfoContextUsage}}); err != nil {
		t.Fatal(err)
	}
	if capabilities := session.Capabilities(); capabilities.CurrentModel != "fixture/model-1" || capabilities.ConfigOptions[0].Current != "high" {
		t.Fatalf("capabilities = %#v", capabilities)
	}
}

func TestNativePiPromptAcceptedPrecedesTurnTerminal(t *testing.T) {
	_, session := openPiNativeTestSession(t, false, agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "pi-native-prompt-order"})
	drainPiHandshake(t, session.Events())
	if _, err := session.Dispatch(context.Background(), piNativeSend("turn", "client-order", "complete immediately")); err != nil {
		t.Fatal(err)
	}
	events := receivePiTerminal(t, session.Events(), 1)
	promptIndex := -1
	terminalIndex := -1
	for index, event := range events {
		switch event.Event.Kind {
		case agentcore.EventPromptAccepted:
			if event.Event.PromptAccepted == nil || event.Event.PromptAccepted.ClientMsgID != "client-order" {
				t.Fatalf("prompt acceptance = %#v", event.Event.PromptAccepted)
			}
			promptIndex = index
		case agentcore.EventTurnResult:
			terminalIndex = index
		}
	}
	if promptIndex < 0 || terminalIndex < 0 || promptIndex >= terminalIndex {
		t.Fatalf("prompt/terminal ordering = prompt:%d terminal:%d events:%#v", promptIndex, terminalIndex, events)
	}
}

func TestNativePiCancelProducesAuthoritativeCancelledTerminal(t *testing.T) {
	_, session := openPiNativeTestSession(t, false, agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "pi-native-cancel"})
	drainPiHandshake(t, session.Events())
	if _, err := session.Dispatch(context.Background(), piNativeSend("turn", "client", "hold")); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Dispatch(context.Background(), agentcore.RuntimeCommand{CommandID: "cancel", Kind: agentcore.RuntimeCommandCancel, Cancel: &agentcore.CancelCommand{Target: agentcore.CancelTargetTurn}}); err != nil {
		t.Fatal(err)
	}
	events := receivePiTerminal(t, session.Events(), 1)
	terminal := piTerminal(events)
	if terminal == nil || terminal.Outcome.Kind != agentcore.TurnOutcomeCancelled || terminal.Outcome.CancelReason != agentcore.CancelReasonUserCancel {
		t.Fatalf("terminal = %#v events=%#v", terminal, events)
	}
}

func TestNativePiResumeBindsExactBackendSession(t *testing.T) {
	_, session := openPiNativeTestSession(t, false, agentcore.SessionSpec{Mode: agentcore.SessionOpenResume, SessionID: "pi-native-resume", BackendSessionID: "resume-session-1"})
	events := drainPiHandshake(t, session.Events())
	var bound string
	for _, event := range events {
		if event.Event.Kind == agentcore.EventBackendBound {
			bound = event.Event.BackendBound.BackendSessionID
		}
	}
	if bound != "resume-session-1" {
		t.Fatalf("backend binding = %q", bound)
	}
}

func TestNativePiMapperRedactsPathsAndCredentials(t *testing.T) {
	session := &NativeSession{
		sessionID: "pi-redaction", now: time.Now, ctx: context.Background(), events: make(chan agentcore.SessionEnvelope, 16), eventIn: make(chan agentcore.SessionEnvelope, 16),
		eventAbort: make(chan struct{}), eventPumpDone: make(chan struct{}), terminated: map[uint64]bool{}, clientIDs: map[string]uint64{}, pending: map[string]piPendingRequest{}, approvals: map[string]piNativeApproval{}, ui: map[string]piNativeUIRequest{},
		generation: 1, active: true, native: NativeConfig{EventCapacity: 16, BackpressureWait: time.Second},
	}
	session.mapper.reset(1)
	go session.runEventPump()
	if err := session.handlePiLine([]byte(`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"D:\\private\\file.txt /srv/private/file token=abcdefghijk"}}`)); err != nil {
		t.Fatal(err)
	}
	session.closeEvents()
	var encoded strings.Builder
	for event := range session.Events() {
		raw, _ := json.Marshal(event)
		encoded.Write(raw)
	}
	text := encoded.String()
	for _, forbidden := range []string{"D:\\\\private", "/srv/private", "abcdefghijk"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("canonical event leaked %q: %s", forbidden, text)
		}
	}
}

func TestNativePiMessageLifecycleProjectsFinalTextAndUsageOnce(t *testing.T) {
	session := &NativeSession{
		sessionID: "pi-lifecycle", now: time.Now, ctx: context.Background(), events: make(chan agentcore.SessionEnvelope, 16), eventIn: make(chan agentcore.SessionEnvelope, 16),
		eventAbort: make(chan struct{}), eventPumpDone: make(chan struct{}), terminated: map[uint64]bool{}, clientIDs: map[string]uint64{}, pending: map[string]piPendingRequest{}, approvals: map[string]piNativeApproval{}, ui: map[string]piNativeUIRequest{},
		generation: 1, active: true, native: NativeConfig{EventCapacity: 16, BackpressureWait: time.Second},
	}
	session.mapper.reset(1)
	go session.runEventPump()
	message := `{"role":"assistant","content":[{"type":"text","text":"done"}],"provider":"fixture","model":"model-1","usage":{"input":11,"output":5,"cacheRead":2,"cacheWrite":1},"stopReason":"stop"}`
	for _, frame := range []string{
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"done"}}`,
		`{"type":"message_end","message":` + message + `}`,
		`{"type":"turn_end","message":` + message + `,"toolResults":[]}`,
		`{"type":"agent_end","messages":[` + message + `],"willRetry":false}`,
		`{"type":"agent_settled","aborted":false}`,
	} {
		if err := session.handlePiLine([]byte(frame)); err != nil {
			t.Fatalf("handle frame %s: %v", frame, err)
		}
	}
	session.closeEvents()
	var finalized, usageEvents int
	for event := range session.Events() {
		switch event.Event.Kind {
		case agentcore.EventMessageFinalized:
			finalized++
		case agentcore.EventUsageDelta:
			usageEvents++
			usage := event.Event.UsageDelta
			if usage.InputTokens != 11 || usage.OutputTokens != 5 || usage.TotalTokens != 19 {
				t.Fatalf("usage = %#v", usage)
			}
		}
	}
	if finalized != 1 || usageEvents != 1 {
		t.Fatalf("finalized=%d usage_events=%d, want one of each", finalized, usageEvents)
	}
}

func TestNativePiRejectedPromptDoesNotConsumeGeneration(t *testing.T) {
	_, session := openPiNativeTestSession(t, false, agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "pi-native-reject"})
	drainPiHandshake(t, session.Events())
	if _, err := session.Dispatch(context.Background(), piNativeSend("reject", "client-reject", "reject")); err == nil {
		t.Fatal("rejected prompt unexpectedly succeeded")
	}
	receipt, err := session.Dispatch(context.Background(), piNativeSend("accepted", "client-accepted", "accepted"))
	if err != nil || receipt.TurnGeneration != 1 {
		t.Fatalf("accepted prompt after rejection = %#v, %v", receipt, err)
	}
	receivePiTerminal(t, session.Events(), 1)
}

func TestNativePiAmbiguousPromptCancellationRetiresProcess(t *testing.T) {
	_, session := openPiNativeTestSession(t, false, agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "pi-native-noack"})
	drainPiHandshake(t, session.Events())
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := session.Dispatch(ctx, piNativeSend("noack", "client-noack", "noack")); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeHandshakeTimeout) && !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeTransportClosed) {
		t.Fatalf("ambiguous prompt error = %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !session.dead.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !session.dead.Load() || session.currentGeneration() != 0 {
		t.Fatalf("ambiguous admission left session usable: dead=%v generation=%d", session.dead.Load(), session.currentGeneration())
	}
}

func TestNativePiExtensionSelectValidatesAnswerAndUsesWireID(t *testing.T) {
	_, session := openPiNativeTestSession(t, true, agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "pi-native-ui"})
	drainPiHandshake(t, session.Events())
	if err := session.handlePiLine([]byte(`{"type":"extension_ui_request","id":"wire/request\\id","method":"select","title":"Proceed?","options":["Allow","Deny"]}`)); err != nil {
		t.Fatal(err)
	}
	requestID := safePiIdentifier(`wire/request\id`, "pi-ui")
	invalid := agentcore.RuntimeCommand{CommandID: "invalid-ui", Kind: agentcore.RuntimeCommandAnswerPermission, AnswerPermission: &agentcore.AnswerPermissionCommand{RequestID: requestID, Decision: agentcore.PermissionApproved, Selected: "Other"}}
	if _, err := session.Dispatch(context.Background(), invalid); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeInvalidPayload) {
		t.Fatalf("invalid extension selection = %v", err)
	}
	if len(session.PendingPermissionRequests()) != 1 {
		t.Fatal("invalid selection consumed the pending UI request")
	}
	valid := invalid
	valid.CommandID = "valid-ui"
	valid.AnswerPermission = &agentcore.AnswerPermissionCommand{RequestID: requestID, Decision: agentcore.PermissionApproved, Selected: "Allow"}
	if _, err := session.Dispatch(context.Background(), valid); err != nil {
		t.Fatal(err)
	}
	if len(session.PendingPermissionRequests()) != 0 {
		t.Fatal("valid selection did not resolve the UI request")
	}
}

func TestNativePiUnsupportedApprovalEventFailsClosed(t *testing.T) {
	_, session := openPiNativeTestSession(t, true, agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "pi-native-unsupported-approval"})
	drainPiHandshake(t, session.Events())
	if _, err := session.Dispatch(context.Background(), piNativeSend("turn", "client", "hold")); err != nil {
		t.Fatal(err)
	}
	err := session.handlePiLine([]byte(`{"type":"approval_request","request_id":"unsupported-approval"}`))
	if !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeProtocolError) {
		t.Fatalf("unsupported approval event error = %v", err)
	}
	session.handlePiProtocolError(err)
	terminal := piTerminal(receivePiTerminal(t, session.Events(), 1))
	if terminal == nil || terminal.Outcome.Kind != agentcore.TurnOutcomeFailed {
		t.Fatalf("unsupported approval terminal = %#v", terminal)
	}
	select {
	case <-session.done:
	case <-time.After(5 * time.Second):
		t.Fatal("protocol error did not retire the PI process")
	}
}

func piNativeSend(commandID, clientID, text string) agentcore.RuntimeCommand {
	return agentcore.RuntimeCommand{CommandID: commandID, Kind: agentcore.RuntimeCommandSend, Send: &agentcore.SendCommand{Content: []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: text}}, Metadata: agentcore.CommandMetadata{ClientMsgID: clientID}}}
}

func drainPiHandshake(t *testing.T, events <-chan agentcore.SessionEnvelope) []agentcore.SessionEnvelope {
	t.Helper()
	var out []agentcore.SessionEnvelope
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-events:
			out = append(out, event)
			if event.Event.Kind == agentcore.EventCatalogUpdated {
				return out
			}
		case <-deadline:
			t.Fatal("timed out waiting for pi handshake events")
		}
	}
}

func receivePiTerminal(t *testing.T, events <-chan agentcore.SessionEnvelope, generation uint64) []agentcore.SessionEnvelope {
	t.Helper()
	var out []agentcore.SessionEnvelope
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-events:
			out = append(out, event)
			if event.TurnGeneration == generation && event.Event.Kind == agentcore.EventTurnResult {
				return out
			}
		case <-deadline:
			t.Fatalf("timed out waiting for pi terminal generation %d: %#v", generation, out)
		}
	}
}

func piEventsContainText(events []agentcore.SessionEnvelope, want string) bool {
	for _, event := range events {
		if event.Event.MessageDelta != nil && strings.Contains(event.Event.MessageDelta.Text, want) {
			return true
		}
		if event.Event.MessageFinalized != nil && strings.Contains(event.Event.MessageFinalized.Message.Content, want) {
			return true
		}
	}
	return false
}

func piEventsContainKind(events []agentcore.SessionEnvelope, kind agentcore.SessionEventKind) bool {
	for _, event := range events {
		if event.Event.Kind == kind {
			return true
		}
	}
	return false
}

func piTerminal(events []agentcore.SessionEnvelope) *agentcore.TurnResultEvent {
	for _, event := range events {
		if event.Event.TurnResult != nil {
			return event.Event.TurnResult
		}
	}
	return nil
}
