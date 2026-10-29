package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
)

var (
	nativeHelperOnce sync.Once
	nativeHelperDir  string
	nativeHelperExe  string
	nativeHelperErr  error
)

func nativeTestHelper(t *testing.T) string {
	t.Helper()
	nativeHelperOnce.Do(func() {
		nativeHelperDir, nativeHelperErr = os.MkdirTemp("", "orrery-codex-native-helper-")
		if nativeHelperErr != nil {
			return
		}
		name := "codex-native-helper"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		nativeHelperExe = filepath.Join(nativeHelperDir, name)
		command := exec.Command("go", "build", "-o", nativeHelperExe, "./testdata/native-helper")
		_, file, _, _ := runtime.Caller(0)
		command.Dir = filepath.Dir(file)
		if output, err := command.CombinedOutput(); err != nil {
			nativeHelperErr = errors.New(err.Error() + ": " + string(output))
		}
	})
	if nativeHelperErr != nil {
		t.Fatal(nativeHelperErr)
	}
	return nativeHelperExe
}

func cleanupNativeTestHelper() {
	if nativeHelperDir != "" {
		_ = os.RemoveAll(nativeHelperDir)
	}
}

type nativeTestSession struct {
	connection *NativeConnection
	backend    *NativeSession
	workspace  string
	capture    string
}

func openNativeTestSession(t *testing.T, mode string, spec agentcore.SessionSpec, config agentcore.SessionConfig, customize func(*NativeWorkspace)) nativeTestSession {
	t.Helper()
	workspace := t.TempDir()
	capture := filepath.Join(t.TempDir(), "wire.jsonl")
	resolved := NativeWorkspace{Directory: workspace, Environment: map[string]string{}, MCPServers: map[string]NativeMCPServer{}}
	if customize != nil {
		customize(&resolved)
	}
	connection, err := NewNativeConnection(NativeConfig{
		Executable: nativeTestHelper(t), Version: "codex-cli 0.145.0", HandshakeTimeout: 5 * time.Second,
		Environment: map[string]string{"ORRERY_CODEX_HELPER_MODE": mode, "ORRERY_CODEX_HELPER_CAPTURE": capture},
		ResolveWorkspace: WorkspaceResolverFunc(func(context.Context, string) (NativeWorkspace, error) {
			return resolved, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := connection.OpenSession(context.Background(), spec, config)
	if err != nil {
		t.Fatal(err)
	}
	native := backend.(*NativeSession)
	t.Cleanup(func() { _ = connection.CloseSession(context.Background(), spec.SessionID) })
	return nativeTestSession{connection: connection, backend: native, workspace: workspace, capture: capture}
}

func freshNativeSpec() agentcore.SessionSpec {
	return agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "native-test"}
}

func nativeConfig() agentcore.SessionConfig {
	return agentcore.SessionConfig{WorkspaceRef: "workspace-native-test"}
}

func nativeSend(id, clientID, text string) agentcore.RuntimeCommand {
	return agentcore.RuntimeCommand{CommandID: id, Kind: agentcore.RuntimeCommandSend, Send: &agentcore.SendCommand{
		Content: []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: text}}, Metadata: agentcore.CommandMetadata{ClientMsgID: clientID},
	}}
}

func TestNativeHandshakeCapturedByteShape(t *testing.T) {
	session := openNativeTestSession(t, "normal", freshNativeSpec(), nativeConfig(), nil)
	actual := readNativeCapture(t, session.capture)
	want := readNativeFixture(t, "native_handshake_requests.jsonl")
	if len(actual) < len(want) {
		t.Fatalf("captured %d handshake frames, want at least %d", len(actual), len(want))
	}
	actual = actual[:len(want)]
	for index := range actual {
		actual[index] = normalizeNativeWorkspace(t, actual[index])
		if actual[index] != want[index] {
			t.Fatalf("handshake frame %d\n got: %s\nwant: %s", index, actual[index], want[index])
		}
	}
	var initialize map[string]any
	if err := json.Unmarshal([]byte(actual[0]), &initialize); err != nil {
		t.Fatal(err)
	}
	params := initialize["params"].(map[string]any)
	capabilities := params["capabilities"].(map[string]any)
	if capabilities["experimentalApi"] != true || initialize["experimentalApi"] != nil {
		t.Fatalf("experimental capability nesting drifted: %#v", initialize)
	}
}

func TestNativeFreshMultiTurnAndPromptCorrelation(t *testing.T) {
	session := openNativeTestSession(t, "normal", freshNativeSpec(), nativeConfig(), nil)
	first, err := session.backend.Dispatch(context.Background(), nativeSend("send-1", "client-1", "one"))
	if err != nil || first.Admission != agentcore.AdmissionStarted || first.TurnGeneration != 1 {
		t.Fatalf("first receipt = %#v, %v", first, err)
	}
	firstEvents := collectThroughNativeEvent(t, session.backend.Events(), agentcore.EventTurnResult)
	assertNativePromptAndTerminal(t, firstEvents, "client-1", 1)
	second, err := session.backend.Dispatch(context.Background(), nativeSend("send-2", "client-2", "two"))
	if err != nil || second.TurnGeneration != 2 {
		t.Fatalf("second receipt = %#v, %v", second, err)
	}
	secondEvents := collectThroughNativeEvent(t, session.backend.Events(), agentcore.EventTurnResult)
	assertNativePromptAndTerminal(t, secondEvents, "client-2", 2)
}

func TestNativeRejectedSendHasNoPromptAcceptedOrPhantomTurn(t *testing.T) {
	session := openNativeTestSession(t, "reject-send", freshNativeSpec(), nativeConfig(), nil)
	if _, err := session.backend.Dispatch(context.Background(), nativeSend("rejected", "client-rejected", "no")); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeBackendRejected) {
		t.Fatalf("rejected send error = %v", err)
	}
	if session.backend.turnActive() {
		t.Fatal("rejected send left an active turn")
	}
	deadline := time.After(100 * time.Millisecond)
	for {
		select {
		case event := <-session.backend.Events():
			if event.Event.Kind == agentcore.EventPromptAccepted || event.Event.Kind == agentcore.EventTurnResult {
				t.Fatalf("rejected send emitted %s", event.Event.Kind)
			}
		case <-deadline:
			return
		}
	}
}

func TestNativeCancelAndSteerUseActiveTurn(t *testing.T) {
	t.Run("cancel", func(t *testing.T) {
		session := openNativeTestSession(t, "hold", freshNativeSpec(), nativeConfig(), nil)
		if _, err := session.backend.Dispatch(context.Background(), nativeSend("send", "client", "hold")); err != nil {
			t.Fatal(err)
		}
		command := agentcore.RuntimeCommand{CommandID: "cancel", Kind: agentcore.RuntimeCommandCancel, Cancel: &agentcore.CancelCommand{Target: agentcore.CancelTargetTurn}}
		if _, err := session.backend.Dispatch(context.Background(), command); err != nil {
			t.Fatal(err)
		}
		events := collectThroughNativeEvent(t, session.backend.Events(), agentcore.EventTurnResult)
		terminal := findNativeEvent(events, agentcore.EventTurnResult)
		if terminal.Event.TurnResult.Outcome.Kind != agentcore.TurnOutcomeCancelled {
			t.Fatalf("cancel terminal = %#v", terminal.Event.TurnResult)
		}
		before := countNativeMethod(t, session.capture, "turn/interrupt")
		if _, err := session.backend.Dispatch(context.Background(), agentcore.RuntimeCommand{CommandID: "idle-cancel", Kind: agentcore.RuntimeCommandCancel, Cancel: &agentcore.CancelCommand{Target: agentcore.CancelTargetTurn}}); err != nil {
			t.Fatal(err)
		}
		if after := countNativeMethod(t, session.capture, "turn/interrupt"); after != before {
			t.Fatalf("idle cancel wrote a frame: %d -> %d", before, after)
		}
	})
	t.Run("steer", func(t *testing.T) {
		session := openNativeTestSession(t, "hold", freshNativeSpec(), nativeConfig(), nil)
		if _, err := session.backend.Dispatch(context.Background(), nativeSend("send", "client", "hold")); err != nil {
			t.Fatal(err)
		}
		command := agentcore.RuntimeCommand{CommandID: "steer", Kind: agentcore.RuntimeCommandSteer, Steer: &agentcore.SteerCommand{Content: []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: "redirect"}}}}
		if _, err := session.backend.Dispatch(context.Background(), command); err != nil {
			t.Fatal(err)
		}
		events := collectThroughNativeEvent(t, session.backend.Events(), agentcore.EventTurnResult)
		if !strings.Contains(nativeMessageText(events), "steered") {
			t.Fatalf("steer delta missing: %#v", events)
		}
		frame := lastNativeMethod(t, session.capture, "turn/steer")
		params := frame["params"].(map[string]any)
		if params["expectedTurnId"] != "turn-1" {
			t.Fatalf("steer expected turn = %#v", params["expectedTurnId"])
		}
	})
}

func TestNativeApprovalAuthAndQuestionRecovery(t *testing.T) {
	t.Run("approval", func(t *testing.T) {
		session := openNativeTestSession(t, "approval", freshNativeSpec(), nativeConfig(), nil)
		_, _ = session.backend.Dispatch(context.Background(), nativeSend("send", "client", "approve"))
		permission := waitNativeEvent(t, session.backend.Events(), agentcore.EventPermission)
		pending := session.backend.PendingPermissionRequests()
		if len(pending) != 1 || pending[0].RequestID != permission.Event.Permission.RequestID {
			t.Fatalf("pending permission = %#v", pending)
		}
		approval, ok := session.backend.PendingRuntimeApprovalRequest(permission.Event.Permission.RequestID)
		if !ok || approval.Kind != runtimecore.ApprovalKindCommandExecution || approval.ToolName != "exec_command" ||
			approval.Metadata["approval_native_tool_name"] != "CommandExecution" || !approval.CommandMaterialUnavailable {
			t.Fatalf("runtime approval = %#v, ok=%v", approval, ok)
		}
		answer := agentcore.RuntimeCommand{CommandID: "answer", Kind: agentcore.RuntimeCommandAnswerPermission, AnswerPermission: &agentcore.AnswerPermissionCommand{RequestID: pending[0].RequestID, Decision: agentcore.PermissionAllowAlways}}
		if _, err := session.backend.Dispatch(context.Background(), answer); err != nil {
			t.Fatal(err)
		}
		collectThroughNativeEvent(t, session.backend.Events(), agentcore.EventTurnResult)
		response := lastNativeResponse(t, session.capture, 700)
		if response["result"].(map[string]any)["decision"] != "acceptForSession" {
			t.Fatalf("approval response = %#v", response)
		}
		if len(session.backend.PendingPermissionRequests()) != 0 {
			t.Fatal("answered approval remained recoverable")
		}
		unknown := answer
		unknown.CommandID = "answer-unknown"
		if _, err := session.backend.Dispatch(context.Background(), unknown); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeUnknownPermissionRequest) {
			t.Fatalf("unknown approval error = %v", err)
		}
	})
	t.Run("auth", func(t *testing.T) {
		session := openNativeTestSession(t, "auth", freshNativeSpec(), nativeConfig(), nil)
		_, _ = session.backend.Dispatch(context.Background(), nativeSend("send", "client", "auth"))
		permission := waitNativeEvent(t, session.backend.Events(), agentcore.EventPermission)
		credentials := json.RawMessage(`{"access_token":"private-token","chatgpt_account_id":"acct"}`)
		answer := agentcore.RuntimeCommand{CommandID: "auth-answer", Kind: agentcore.RuntimeCommandAnswerAuth, AnswerAuth: &agentcore.AnswerAuthCommand{RequestID: permission.Event.Permission.RequestID, MethodID: "refresh", Credentials: credentials}}
		if _, err := session.backend.Dispatch(context.Background(), answer); err != nil {
			t.Fatal(err)
		}
		events := collectThroughNativeEvent(t, session.backend.Events(), agentcore.EventTurnResult)
		encoded, _ := json.Marshal(events)
		if strings.Contains(string(encoded), "private-token") || strings.Contains(string(encoded), "acct") {
			t.Fatal("auth credentials escaped into canonical events")
		}
		response := lastNativeResponse(t, session.capture, 701)
		result := response["result"].(map[string]any)
		if result["accessToken"] != "private-token" || result["chatgptAccountId"] != "acct" {
			t.Fatalf("auth response shape = %#v", result)
		}
	})
	t.Run("request user input", func(t *testing.T) {
		session := openNativeTestSession(t, "question", freshNativeSpec(), nativeConfig(), nil)
		_, _ = session.backend.Dispatch(context.Background(), nativeSend("send", "client", "question"))
		permission := waitNativeEvent(t, session.backend.Events(), agentcore.EventPermission)
		if permission.Event.Permission.ToolName != "AskUserQuestion" || len(permission.Event.Permission.Questions) != 1 {
			t.Fatalf("question projection = %#v", permission.Event.Permission)
		}
		answer := agentcore.RuntimeCommand{CommandID: "question-answer", Kind: agentcore.RuntimeCommandAnswerPermission, AnswerPermission: &agentcore.AnswerPermissionCommand{
			RequestID: permission.Event.Permission.RequestID, Decision: agentcore.PermissionApproved, Answers: []agentcore.QuestionAnswer{{Question: "Choose", Labels: []string{"A"}}},
		}}
		if _, err := session.backend.Dispatch(context.Background(), answer); err != nil {
			t.Fatal(err)
		}
		collectThroughNativeEvent(t, session.backend.Events(), agentcore.EventTurnResult)
		response := lastNativeResponse(t, session.capture, 702)
		answers := response["result"].(map[string]any)["answers"].(map[string]any)
		if !reflect.DeepEqual(answers["q1"].(map[string]any)["answers"], []any{"A"}) {
			t.Fatalf("question response = %#v", response)
		}
	})
	t.Run("MCP elicitation", func(t *testing.T) {
		session := openNativeTestSession(t, "elicitation", freshNativeSpec(), nativeConfig(), nil)
		_, _ = session.backend.Dispatch(context.Background(), nativeSend("send", "client", "elicit"))
		permission := waitNativeEvent(t, session.backend.Events(), agentcore.EventPermission)
		if permission.Event.Permission.ToolName != "McpElicitation" || permission.Event.Permission.Context == nil || permission.Event.Permission.Context.ServerName != "docs" {
			t.Fatalf("elicitation projection = %#v", permission.Event.Permission)
		}
		answer := agentcore.RuntimeCommand{CommandID: "elicit-answer", Kind: agentcore.RuntimeCommandAnswerPermission, AnswerPermission: &agentcore.AnswerPermissionCommand{RequestID: permission.Event.Permission.RequestID, Decision: agentcore.PermissionDenied}}
		if _, err := session.backend.Dispatch(context.Background(), answer); err != nil {
			t.Fatal(err)
		}
		collectThroughNativeEvent(t, session.backend.Events(), agentcore.EventTurnResult)
		response := lastNativeResponse(t, session.capture, 703)
		if response["result"].(map[string]any)["action"] != "decline" {
			t.Fatalf("elicitation response = %#v", response)
		}
	})
}

func TestNativeDiscoveryConfigRewindAndCheckpoints(t *testing.T) {
	session := openNativeTestSession(t, "normal", freshNativeSpec(), nativeConfig(), nil)
	caps := session.backend.Capabilities()
	if !nativeHasModel(caps.AvailableModels, "gpt-test") || !nativeHasMode(caps.AvailableModes, "auto") || len(caps.ConfigOptions) != 1 {
		t.Fatalf("discovered capabilities = %#v", caps)
	}
	commands := []agentcore.RuntimeCommand{
		{CommandID: "model", Kind: agentcore.RuntimeCommandSetModel, SetModel: &agentcore.SetModelCommand{Model: "gpt-test"}},
		{CommandID: "mode", Kind: agentcore.RuntimeCommandSetMode, SetMode: &agentcore.SetModeCommand{Mode: "auto"}},
		{CommandID: "effort", Kind: agentcore.RuntimeCommandSetConfigOption, SetConfigOption: &agentcore.SetConfigOptionCommand{OptionID: "effort", Value: "high"}},
	}
	for _, command := range commands {
		if _, err := session.backend.Dispatch(context.Background(), command); err != nil {
			t.Fatal(err)
		}
		waitNativeEvent(t, session.backend.Events(), agentcore.EventConfigChanged)
	}
	caps = session.backend.Capabilities()
	if caps.CurrentModel != "gpt-test" || caps.CurrentMode != "auto" || caps.ConfigOptions[0].Current != "high" {
		t.Fatalf("confirmed config = %#v", caps)
	}
	if _, err := session.backend.Dispatch(context.Background(), agentcore.RuntimeCommand{CommandID: "checkpoints", Kind: agentcore.RuntimeCommandListCheckpoints, ListCheckpoints: &agentcore.ListCheckpointsCommand{}}); err != nil {
		t.Fatal(err)
	}
	checkpoints := waitNativeEvent(t, session.backend.Events(), agentcore.EventCheckpointList)
	if len(checkpoints.Event.CheckpointList.Entries) != 2 {
		t.Fatalf("checkpoints = %#v", checkpoints.Event.CheckpointList)
	}
	if _, err := session.backend.Dispatch(context.Background(), agentcore.RuntimeCommand{CommandID: "rewind", Kind: agentcore.RuntimeCommandRewind, Rewind: &agentcore.RewindCommand{NumTurns: 1}}); err != nil {
		t.Fatal(err)
	}
	wound := waitNativeEvent(t, session.backend.Events(), agentcore.EventRewound)
	if wound.Event.Rewound.ToTurn != 0 {
		t.Fatalf("rewound target = %d", wound.Event.Rewound.ToTurn)
	}
}

func TestNativeDiscoveryReconcilesOnlyAdvertisedSelections(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		config := nativeConfig()
		config.Model = "gpt-test"
		config.Mode = "auto"
		session := openNativeTestSession(t, "normal", freshNativeSpec(), config, nil)
		if countNativeMethod(t, session.capture, "thread/settings/update") != 2 {
			t.Fatalf("valid persisted selections were not reconciled: %v", readNativeCapture(t, session.capture))
		}
	})
	t.Run("invalid", func(t *testing.T) {
		config := nativeConfig()
		config.Model = "missing-model"
		config.Mode = ":missing-profile"
		session := openNativeTestSession(t, "normal", freshNativeSpec(), config, nil)
		caps := session.backend.Capabilities()
		if caps.CurrentModel != "" || caps.CurrentMode != "" || countNativeMethod(t, session.capture, "thread/settings/update") != 0 {
			t.Fatalf("invalid persisted selection was applied: %#v", caps)
		}
	})
}

func TestNativePromptBlocksAndSlashCatalogMatchWire(t *testing.T) {
	t.Run("content blocks", func(t *testing.T) {
		session := openNativeTestSession(t, "normal", freshNativeSpec(), nativeConfig(), nil)
		command := agentcore.RuntimeCommand{CommandID: "blocks", Kind: agentcore.RuntimeCommandSend, Send: &agentcore.SendCommand{Content: []agentcore.ContentBlock{
			{Kind: agentcore.ContentBlockText, Text: "inspect"},
			{Kind: agentcore.ContentBlockImage, Data: []byte("png"), MediaType: "image/png"},
			{Kind: agentcore.ContentBlockResourceLink, URI: "docs/guide.md"},
		}, Metadata: agentcore.CommandMetadata{ClientMsgID: "blocks-client"}}}
		if _, err := session.backend.Dispatch(context.Background(), command); err != nil {
			t.Fatal(err)
		}
		collectThroughNativeEvent(t, session.backend.Events(), agentcore.EventTurnResult)
		frame := lastNativeMethod(t, session.capture, "turn/start")
		input := frame["params"].(map[string]any)["input"].([]any)
		if input[1].(map[string]any)["url"] != "data:image/png;base64,cG5n" || input[2].(map[string]any)["text"] != "[Attached file: docs/guide.md]" {
			t.Fatalf("native input = %#v", input)
		}
	})
	for _, test := range []struct {
		text, method string
	}{
		{"/init", "turn/start"},
		{"/compact", "thread/compact/start"},
		{"/review", "review/start"},
		{"/review-branch main", "review/start"},
		{"/review-commit abc123", "review/start"},
		{"/logout", "account/logout"},
	} {
		t.Run(test.text, func(t *testing.T) {
			session := openNativeTestSession(t, "normal", freshNativeSpec(), nativeConfig(), nil)
			receipt, err := session.backend.Dispatch(context.Background(), nativeSend("slash", "slash-client", test.text))
			if err != nil {
				t.Fatal(err)
			}
			if test.text == "/logout" {
				if receipt.Admission != agentcore.AdmissionNoTurn {
					t.Fatalf("logout receipt = %#v", receipt)
				}
			} else {
				collectThroughNativeEvent(t, session.backend.Events(), agentcore.EventTurnResult)
			}
			if countNativeMethod(t, session.capture, test.method) == 0 {
				t.Fatalf("slash %s did not route to %s", test.text, test.method)
			}
		})
	}
}

func TestNativeResumeReplaysFullStartSurface(t *testing.T) {
	config := nativeConfig()
	config.Preset = "repository instructions"
	config.MCPServers = []agentcore.MCPServerSpec{{Name: "docs", Transport: agentcore.MCPTransportStdio, CommandRef: "docs-command"}}
	spec := agentcore.SessionSpec{Mode: agentcore.SessionOpenResume, SessionID: "native-resume", BackendSessionID: "thread-resume"}
	session := openNativeTestSession(t, "normal", spec, config, func(workspace *NativeWorkspace) {
		workspace.Sandbox = "read-only"
		workspace.ApprovalPolicy = "never"
		workspace.MCPServers["docs"] = NativeMCPServer{Command: "docs-helper", Args: []string{"serve"}, Environment: map[string]string{"PRIVATE": "value"}}
	})
	frame := lastNativeMethod(t, session.capture, "thread/resume")
	params := frame["params"].(map[string]any)
	if params["threadId"] != "thread-resume" || params["sandbox"] != "read-only" || params["approvalPolicy"] != "never" || params["baseInstructions"] != "repository instructions" || params["model"] != nil {
		t.Fatalf("resume surface = %#v", params)
	}
	if params["config"].(map[string]any)["mcp_servers"] == nil {
		t.Fatalf("resume omitted MCP configuration: %#v", params)
	}
	if countNativeMethod(t, session.capture, "thread/start") != 0 {
		t.Fatal("resume created a second fresh thread")
	}
}

func TestNativeCapturedMapperTraceIsSafeAndTerminalOnce(t *testing.T) {
	backend := newNativeMapperSession(t)
	file, err := os.Open(filepath.Join("testdata", "native_turn_trace.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if err := backend.handleNativeLine(scanner.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	events := collectThroughNativeEvent(t, backend.Events(), agentcore.EventTurnResult)
	counts := make(map[agentcore.SessionEventKind]int)
	var reasoningKinds []agentcore.ReasoningContentKind
	for _, envelope := range events {
		counts[envelope.Event.Kind]++
		if envelope.Event.ThoughtDelta != nil {
			reasoningKinds = append(reasoningKinds, envelope.Event.ThoughtDelta.ReasoningKind)
		}
		if err := envelope.Validate(); err != nil {
			t.Fatalf("invalid mapped envelope %#v: %v", envelope, err)
		}
		encoded, _ := json.Marshal(envelope)
		if strings.Contains(string(encoded), "must-not-escape") || strings.Contains(string(encoded), `C:\\private`) || strings.Contains(string(encoded), `"env"`) {
			t.Fatalf("unsafe native payload escaped: %s", encoded)
		}
	}
	for _, kind := range []agentcore.SessionEventKind{agentcore.EventMessageDelta, agentcore.EventThoughtDelta, agentcore.EventToolCall, agentcore.EventToolResult, agentcore.EventToolOutputDelta, agentcore.EventPlan, agentcore.EventTurnDiffUpdated, agentcore.EventUsageDelta, agentcore.EventSubagentUpdate, agentcore.EventProvisioning} {
		if counts[kind] == 0 {
			t.Fatalf("fixture did not map %s: %#v", kind, counts)
		}
	}
	if counts[agentcore.EventTurnResult] != 1 || counts[agentcore.EventAdapterSpecific] != 2 {
		t.Fatalf("terminal/unknown counts = %#v", counts)
	}
	if len(reasoningKinds) != 1 || reasoningKinds[0] != agentcore.ReasoningContentSummary {
		t.Fatalf("fixture reasoning kinds = %v", reasoningKinds)
	}
}

func TestNativeCodexSeparatesRawReasoningFromSummary(t *testing.T) {
	backend := newNativeMapperSession(t)
	frames := []string{
		`{"jsonrpc":"2.0","method":"item/reasoning/textDelta","params":{"threadId":"thread-fixture","turnId":"turn-fixture","itemId":"reason-1","delta":"private"}}`,
		`{"jsonrpc":"2.0","method":"item/reasoning/summaryTextDelta","params":{"threadId":"thread-fixture","turnId":"turn-fixture","itemId":"reason-1","delta":"summary"}}`,
	}
	want := []agentcore.ReasoningContentKind{agentcore.ReasoningContentRaw, agentcore.ReasoningContentSummary}
	for index, frame := range frames {
		if err := backend.handleNativeLine([]byte(frame)); err != nil {
			t.Fatal(err)
		}
		event := waitNativeEvent(t, backend.Events(), agentcore.EventThoughtDelta)
		if event.Event.ThoughtDelta.ReasoningKind != want[index] {
			t.Fatalf("frame %d reasoning kind = %q, want %q", index, event.Event.ThoughtDelta.ReasoningKind, want[index])
		}
	}
}

func TestNativeTerminalReconciliationPreservesEpochAndAbsorbsDuplicates(t *testing.T) {
	backend := newNativeMapperSession(t)
	frames := []string{
		`{"jsonrpc":"2.0","method":"thread/status/changed","params":{"threadId":"thread-fixture","status":{"type":"idle"}}}`,
		`{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"thread-fixture","turn":{"id":"turn-fixture","status":"failed","error":{"message":"unexpected status 503 Service Unavailable"}}}}`,
		`{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"thread-fixture","turn":{"id":"turn-fixture","status":"completed"}}}`,
	}
	for _, frame := range frames {
		if err := backend.handleNativeLine([]byte(frame)); err != nil {
			t.Fatal(err)
		}
	}
	terminal := waitNativeEvent(t, backend.Events(), agentcore.EventTurnResult)
	if terminal.TurnGeneration != 1 || !terminal.Event.TurnResult.IsError || terminal.Event.TurnResult.APIErrorStatus == nil || *terminal.Event.TurnResult.APIErrorStatus != 503 {
		t.Fatalf("authoritative terminal = %#v", terminal)
	}
	select {
	case event := <-backend.Events():
		if event.Event.Kind == agentcore.EventTurnResult {
			t.Fatalf("duplicate terminal escaped absorption: %#v", event)
		}
	case <-time.After(100 * time.Millisecond):
	}
	backend.mu.Lock()
	backend.turnGeneration = 2
	backend.turnInFlight = true
	backend.activeTurnID = "turn-new"
	backend.turnGenerations["turn-new"] = 2
	backend.terminated[2] = false
	backend.mu.Unlock()
	late := `{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"thread-fixture","turn":{"id":"turn-fixture","status":"interrupted"}}}`
	if err := backend.handleNativeLine([]byte(late)); err != nil {
		t.Fatal(err)
	}
	if !backend.turnActive() || backend.currentGeneration() != 2 {
		t.Fatal("late old-generation terminal ended the next turn")
	}
}

func TestNativeEOFAndCrashTerminalSemantics(t *testing.T) {
	for _, test := range []struct {
		name, mode string
		wantError  bool
	}{
		{name: "idle EOF", mode: "idle", wantError: false},
		{name: "crash", mode: "crash", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := openNativeTestSession(t, test.mode, freshNativeSpec(), nativeConfig(), nil)
			_, _ = session.backend.Dispatch(context.Background(), nativeSend("send", "client", "end"))
			events := collectThroughNativeEvent(t, session.backend.Events(), agentcore.EventDetached)
			terminal := findNativeEvent(events, agentcore.EventTurnResult)
			if terminal.Event.TurnResult == nil || terminal.Event.TurnResult.IsError != test.wantError {
				t.Fatalf("terminal = %#v", terminal.Event.TurnResult)
			}
			detached := findNativeEvent(events, agentcore.EventDetached)
			if detached.Event.Detached == nil || strings.Contains(detached.Event.Detached.RedactedSummary, session.workspace) {
				t.Fatalf("detached = %#v", detached.Event.Detached)
			}
		})
	}
}

func TestNativeSuspendWakeAndCloseJoinProcess(t *testing.T) {
	config := nativeConfig()
	config.IdleTTL = 40 * time.Millisecond
	session := openNativeTestSession(t, "normal", freshNativeSpec(), config, nil)
	firstBinding := session.backend.currentBinding()
	waitNativeEvent(t, session.backend.Events(), agentcore.EventBackendSuspended)
	select {
	case <-firstBinding.done:
	case <-time.After(3 * time.Second):
		t.Fatal("suspended process reader was not joined")
	}
	command := agentcore.RuntimeCommand{CommandID: "wake-model", Kind: agentcore.RuntimeCommandSetModel, SetModel: &agentcore.SetModelCommand{Model: "gpt-test"}}
	if _, err := session.backend.Dispatch(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	waitNativeEvent(t, session.backend.Events(), agentcore.EventConfigChanged)
	secondBinding := session.backend.currentBinding()
	if secondBinding == firstBinding || session.backend.boundThread() != "thread-native-1" {
		t.Fatal("wake did not resume the same logical thread with a new process")
	}
	if err := session.connection.CloseSession(context.Background(), freshNativeSpec().SessionID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondBinding.done:
	case <-time.After(3 * time.Second):
		t.Fatal("close did not join process reader")
	}
	if secondBinding.cmd.ProcessState == nil || !secondBinding.cmd.ProcessState.Exited() {
		t.Fatalf("process state after close = %#v", secondBinding.cmd.ProcessState)
	}
	session.backend.mu.Lock()
	pending := len(session.backend.pendingRPC)
	session.backend.mu.Unlock()
	if pending != 0 {
		t.Fatalf("pending RPC leaked: %d", pending)
	}
}

func TestNativeEventBackpressureIsBoundedAndExplicit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	backend := &NativeSession{
		sessionID: "backpressure", now: time.Now, ctx: ctx, cancel: cancel,
		native: NativeConfig{EventCapacity: 1, BackpressureWait: 20 * time.Millisecond},
		events: make(chan agentcore.SessionEnvelope, 3), eventIn: make(chan agentcore.SessionEnvelope, 1), eventAbort: make(chan struct{}), eventPumpDone: make(chan struct{}), done: make(chan struct{}),
		turnGeneration: 1, turnInFlight: true, terminated: map[uint64]bool{}, idlePending: map[uint64]bool{}, pendingRPC: map[string]*nativePendingRPC{}, pendingPermissions: map[string]nativePendingPermission{}, pendingAuth: map[string]nativePendingAuth{}, resolvedRequests: map[string]agentcore.PermissionKind{},
	}
	go backend.runEventPump()
	for index := 0; index < 8; index++ {
		backend.emit(1, agentcore.SessionEvent{Kind: agentcore.EventNotice, Notice: &agentcore.NoticeEvent{Level: agentcore.NoticeWarning, Message: fmt.Sprintf("notice-%d", index)}})
	}
	deadline := time.Now().Add(time.Second)
	for !backend.dead.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !backend.dead.Load() {
		t.Fatal("event backpressure did not detach the provider")
	}

	var terminal, detached int
	for envelope := range backend.Events() {
		switch envelope.Event.Kind {
		case agentcore.EventTurnResult:
			terminal++
			if !envelope.Event.TurnResult.IsError || envelope.Event.TurnResult.Outcome.Kind != agentcore.TurnOutcomeFailed {
				t.Fatalf("backpressure terminal = %#v", envelope.Event.TurnResult)
			}
		case agentcore.EventDetached:
			detached++
			if !strings.Contains(envelope.Event.Detached.RedactedSummary, "backpressure") {
				t.Fatalf("backpressure detached summary = %q", envelope.Event.Detached.RedactedSummary)
			}
		}
	}
	if terminal != 1 || detached != 1 {
		t.Fatalf("backpressure terminal=%d detached=%d", terminal, detached)
	}
	cancel()
	backend.closeEvents()
	<-backend.eventPumpDone
}

func TestNativeVersionArgsAndWorkspaceFailClosed(t *testing.T) {
	for _, version := range []string{"codex-cli 0.137.0", "0.145.99"} {
		if err := validateCodexVersion(version); err != nil {
			t.Fatalf("supported %s: %v", version, err)
		}
	}
	for _, version := range []string{"0.136.9", "0.146.0", "unknown"} {
		if err := validateCodexVersion(version); err == nil {
			t.Fatalf("unsupported version accepted: %s", version)
		}
	}
	args := nativeCodexArgs([]string{"--listen", "ws://remote", "-c", "sandbox=danger-full-access", "--strict-config", "--enable", "feature", "--disable=other"})
	if !reflect.DeepEqual(args, []string{"--strict-config", "--enable", "feature", "--disable=other"}) {
		t.Fatalf("native arg allowlist = %#v", args)
	}
	config := agentcore.SessionConfig{WorkspaceRef: "opaque", MCPServers: []agentcore.MCPServerSpec{{Name: "remote", Transport: agentcore.MCPTransportHTTP, Endpoint: "https://example.test", HeaderRefs: map[string]string{"Authorization": "credential-ref"}}}}
	workspace := NativeWorkspace{Directory: t.TempDir(), Environment: map[string]string{}, MCPServers: map[string]NativeMCPServer{"remote": {URL: "https://example.test", BearerTokenEnvVar: "TOKEN"}}}
	if err := validateNativeWorkspace(workspace, config); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeWorkspaceUnavailable) {
		t.Fatalf("unresolved credential error = %v", err)
	}
}

func newNativeMapperSession(t *testing.T) *NativeSession {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	backend := &NativeSession{
		sessionID: "mapper", workspace: NativeWorkspace{Directory: t.TempDir()}, now: time.Now, ctx: ctx, cancel: cancel,
		native: NativeConfig{EventCapacity: 64, BackpressureWait: time.Second}, events: make(chan agentcore.SessionEnvelope, 66), eventIn: make(chan agentcore.SessionEnvelope, 64), eventAbort: make(chan struct{}), eventPumpDone: make(chan struct{}), done: make(chan struct{}),
		turnGeneration: 1, turnInFlight: true, activeTurnID: "turn-fixture", turnGenerations: map[string]uint64{"turn-fixture": 1}, terminated: map[uint64]bool{}, idlePending: map[uint64]bool{}, pendingRPC: map[string]*nativePendingRPC{}, pendingPermissions: map[string]nativePendingPermission{}, pendingAuth: map[string]nativePendingAuth{}, resolvedRequests: map[string]agentcore.PermissionKind{},
	}
	go backend.runEventPump()
	t.Cleanup(func() {
		cancel()
		backend.closeEvents()
		<-backend.eventPumpDone
	})
	return backend
}

func waitNativeEvent(t *testing.T, events <-chan agentcore.SessionEnvelope, kind agentcore.SessionEventKind) agentcore.SessionEnvelope {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case envelope, ok := <-events:
			if !ok {
				t.Fatalf("event stream closed before %s", kind)
			}
			if envelope.Event.Kind == kind {
				return envelope
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", kind)
		}
	}
}

func collectThroughNativeEvent(t *testing.T, events <-chan agentcore.SessionEnvelope, terminal agentcore.SessionEventKind) []agentcore.SessionEnvelope {
	t.Helper()
	out := make([]agentcore.SessionEnvelope, 0)
	deadline := time.After(5 * time.Second)
	for {
		select {
		case envelope, ok := <-events:
			if !ok {
				t.Fatalf("event stream closed before %s; got %#v", terminal, out)
			}
			out = append(out, envelope)
			if envelope.Event.Kind == terminal {
				return out
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s; got %#v", terminal, out)
		}
	}
}

func findNativeEvent(events []agentcore.SessionEnvelope, kind agentcore.SessionEventKind) agentcore.SessionEnvelope {
	for _, event := range events {
		if event.Event.Kind == kind {
			return event
		}
	}
	return agentcore.SessionEnvelope{}
}

func assertNativePromptAndTerminal(t *testing.T, events []agentcore.SessionEnvelope, clientID string, generation uint64) {
	t.Helper()
	prompt := findNativeEvent(events, agentcore.EventPromptAccepted)
	terminal := findNativeEvent(events, agentcore.EventTurnResult)
	if prompt.Event.PromptAccepted == nil || prompt.Event.PromptAccepted.ClientMsgID != clientID {
		t.Fatalf("prompt correlation = %#v", prompt)
	}
	if terminal.Event.TurnResult == nil || terminal.TurnGeneration != generation || terminal.Event.TurnResult.Epoch != generation || terminal.Event.TurnResult.IsError {
		t.Fatalf("terminal = %#v", terminal)
	}
}

func nativeMessageText(events []agentcore.SessionEnvelope) string {
	var out strings.Builder
	for _, event := range events {
		if event.Event.MessageDelta != nil {
			out.WriteString(event.Event.MessageDelta.Text)
		}
	}
	return out.String()
}

func readNativeCapture(t *testing.T, path string) []string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil && len(data) != 0 {
			lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n")), "\n")
			return lines
		}
		if time.Now().After(deadline) {
			t.Fatalf("capture unavailable: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestNativeToolCallUsesProviderToolNameAndFlattensArguments(t *testing.T) {
	for _, test := range []struct {
		typeName string
		toolName string
	}{
		{typeName: "dynamicToolCall", toolName: "memory.diary.append"},
		{typeName: "mcpToolCall", toolName: "docs.search"},
	} {
		event := nativeToolCall(map[string]any{
			"type": test.typeName, "id": "call-a", "tool": test.toolName,
			"arguments": map[string]any{"query": "bridge", "limit": float64(3)},
		}, test.typeName, "call-a")
		if event.ToolCall == nil || event.ToolCall.Name != test.toolName {
			t.Fatalf("%s tool call = %#v", test.typeName, event.ToolCall)
		}
		var input map[string]any
		if err := json.Unmarshal(event.ToolCall.Input, &input); err != nil {
			t.Fatal(err)
		}
		if input["query"] != "bridge" || input["limit"] != float64(3) || input["tool"] != nil || input["arguments"] != nil {
			t.Fatalf("%s flattened input = %#v", test.typeName, input)
		}
	}
}

func TestNativeVersionCacheIsScopedToLaunchIdentityAndVerifiesClaims(t *testing.T) {
	args := []string{"--strict-config"}
	environment := map[string]string{"BASE": "one"}
	connection, err := NewNativeConnection(NativeConfig{ExtraArgs: args, Environment: environment})
	if err != nil {
		t.Fatal(err)
	}
	args[0] = "mutated"
	environment["BASE"] = "mutated"
	if connection.config.ExtraArgs[0] != "--strict-config" || connection.config.Environment["BASE"] != "one" {
		t.Fatalf("native config was not defensively copied: %#v", connection.config)
	}
	countPath := filepath.Join(t.TempDir(), "version-count.log")
	native := NativeConfig{Executable: nativeTestHelper(t)}
	environmentA := map[string]string{"ORRERY_CODEX_VERSION_COUNT": countPath, "ORRERY_LAUNCH_IDENTITY": "a"}
	for index := 0; index < 2; index++ {
		version, resolveErr := connection.resolveVersion(context.Background(), native, environmentA)
		if resolveErr != nil || !strings.Contains(version, "0.145.0") {
			t.Fatalf("resolve version %d = %q, %v", index, version, resolveErr)
		}
	}
	if count := lineCount(t, countPath); count != 1 {
		t.Fatalf("same launch version detections = %d, want 1", count)
	}
	environmentB := map[string]string{"ORRERY_CODEX_VERSION_COUNT": countPath, "ORRERY_LAUNCH_IDENTITY": "b"}
	if _, err := connection.resolveVersion(context.Background(), native, environmentB); err != nil {
		t.Fatal(err)
	}
	if count := lineCount(t, countPath); count != 2 {
		t.Fatalf("distinct launch version detections = %d, want 2", count)
	}
	native.Version = "0.144.0"
	if _, err := connection.resolveVersion(context.Background(), native, environmentB); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeProtocolError) {
		t.Fatalf("mismatched version claim error = %v", err)
	}
}

func lineCount(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return len(strings.Fields(string(data)))
}

func readNativeFixture(t *testing.T, name string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n")), "\n")
}

func normalizeNativeWorkspace(t *testing.T, line string) string {
	t.Helper()
	var frame map[string]any
	if err := json.Unmarshal([]byte(line), &frame); err != nil {
		t.Fatal(err)
	}
	if params, ok := frame["params"].(map[string]any); ok {
		if _, exists := params["cwd"]; exists {
			params["cwd"] = "<WORKSPACE>"
		}
	}
	encoded, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func countNativeMethod(t *testing.T, path, method string) int {
	t.Helper()
	count := 0
	for _, line := range readNativeCapture(t, path) {
		var frame map[string]any
		_ = json.Unmarshal([]byte(line), &frame)
		if frame["method"] == method {
			count++
		}
	}
	return count
}

func lastNativeMethod(t *testing.T, path, method string) map[string]any {
	t.Helper()
	lines := readNativeCapture(t, path)
	for index := len(lines) - 1; index >= 0; index-- {
		var frame map[string]any
		_ = json.Unmarshal([]byte(lines[index]), &frame)
		if frame["method"] == method {
			return frame
		}
	}
	t.Fatalf("method %s not captured", method)
	return nil
}

func lastNativeResponse(t *testing.T, path string, id float64) map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		lines := readNativeCapture(t, path)
		for index := len(lines) - 1; index >= 0; index-- {
			var frame map[string]any
			_ = json.Unmarshal([]byte(lines[index]), &frame)
			if frame["method"] == nil && frame["id"] == id {
				return frame
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("response %v not captured", id)
	return nil
}
