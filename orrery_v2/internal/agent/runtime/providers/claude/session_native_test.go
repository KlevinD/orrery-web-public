package claude

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
	claudeHelperOnce sync.Once
	claudeHelperDir  string
	claudeHelperExe  string
	claudeHelperErr  error
)

type claudeTestSession struct {
	connection *NativeConnection
	backend    *NativeSession
	workspace  string
	capture    string
	args       string
}

func claudeNativeTestHelper(t *testing.T) string {
	t.Helper()
	claudeHelperOnce.Do(func() {
		claudeHelperDir, claudeHelperErr = os.MkdirTemp("", "orrery-claude-native-helper-")
		if claudeHelperErr != nil {
			return
		}
		name := "claude-native-helper"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		claudeHelperExe = filepath.Join(claudeHelperDir, name)
		command := exec.Command("go", "build", "-o", claudeHelperExe, "./testdata/native-helper")
		_, file, _, _ := runtime.Caller(0)
		command.Dir = filepath.Dir(file)
		if output, err := command.CombinedOutput(); err != nil {
			claudeHelperErr = errors.New(err.Error() + ": " + string(output))
		}
	})
	if claudeHelperErr != nil {
		t.Fatal(claudeHelperErr)
	}
	return claudeHelperExe
}

func cleanupClaudeNativeTestHelper() {
	if claudeHelperDir != "" {
		_ = os.RemoveAll(claudeHelperDir)
	}
}

func openClaudeTestSession(t *testing.T, mode string, spec agentcore.SessionSpec, config agentcore.SessionConfig, customize func(*NativeWorkspace)) claudeTestSession {
	t.Helper()
	workspace := t.TempDir()
	capture := filepath.Join(t.TempDir(), "wire.jsonl")
	args := filepath.Join(t.TempDir(), "args.json")
	resolved := NativeWorkspace{Directory: workspace, Environment: map[string]string{}, MCPServers: map[string]NativeMCPServer{}}
	if customize != nil {
		customize(&resolved)
	}
	connection, err := NewNativeConnection(NativeConfig{
		Executable: claudeNativeTestHelper(t), Version: "2.1.220 (Claude Code)", HandshakeTimeout: 3 * time.Second,
		Environment:      map[string]string{"ORRERY_CLAUDE_HELPER_MODE": mode, "ORRERY_CLAUDE_HELPER_CAPTURE": capture, "ORRERY_CLAUDE_HELPER_ARGS": args},
		ResolveWorkspace: NativeWorkspaceResolverFunc(func(context.Context, string) (NativeWorkspace, error) { return resolved, nil }),
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
	return claudeTestSession{connection: connection, backend: native, workspace: workspace, capture: capture, args: args}
}

func claudeFreshSpec() agentcore.SessionSpec {
	return agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "claude-native-test"}
}

func claudeNativeConfig() agentcore.SessionConfig {
	return agentcore.SessionConfig{WorkspaceRef: "workspace-claude-native"}
}

func claudeSend(id, clientID, text string) agentcore.RuntimeCommand {
	return agentcore.RuntimeCommand{CommandID: id, Kind: agentcore.RuntimeCommandSend, Send: &agentcore.SendCommand{
		Content: []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: text}}, Metadata: agentcore.CommandMetadata{ClientMsgID: clientID},
	}}
}

func TestNativeClaudeVersionArgumentsFreshResumeAndWorkspace(t *testing.T) {
	for _, version := range []string{"2.1.168", "2.1.220 (Claude Code)"} {
		if err := validateClaudeVersion(version); err != nil {
			t.Fatalf("supported %s: %v", version, err)
		}
	}
	for _, version := range []string{"2.1.167", "2.1.221", "1.9.220", "unknown"} {
		if err := validateClaudeVersion(version); err == nil {
			t.Fatalf("unsupported version accepted: %s", version)
		}
	}
	basic := openClaudeTestSession(t, "normal", agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "claude-args-basic"}, claudeNativeConfig(), nil)
	basicArgs := readClaudeArgs(t, basic.args)
	wantBasic := []string{
		"--print", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--include-partial-messages",
		"--replay-user-messages", "--permission-prompt-tool", "stdio", "--session-id", basic.backend.claudeID,
		"--permission-mode", "default", "--allow-dangerously-skip-permissions",
	}
	if !reflect.DeepEqual(basicArgs, wantBasic) {
		t.Fatalf("process argument byte shape\n got: %#v\nwant: %#v", basicArgs, wantBasic)
	}

	config := claudeNativeConfig()
	config.Model = "claude-test"
	config.Mode = "plan"
	config.Preset = "repository instructions"
	config.MCPServers = []agentcore.MCPServerSpec{{Name: "docs", Transport: agentcore.MCPTransportStdio, CommandRef: "docs-command"}}
	session := openClaudeTestSession(t, "normal", claudeFreshSpec(), config, func(workspace *NativeWorkspace) {
		workspace.MCPServers["docs"] = NativeMCPServer{Command: "docs-helper", Args: []string{"serve"}, Environment: map[string]string{"PRIVATE": "value"}}
	})
	args := readClaudeArgs(t, session.args)
	assertClaudeArgsContain(t, args,
		"--print", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--include-partial-messages",
		"--replay-user-messages", "--permission-prompt-tool", "stdio", "--session-id", session.backend.claudeID,
		"--system-prompt", "repository instructions", "--strict-mcp-config", "--permission-mode", "plan",
		"--allow-dangerously-skip-permissions", "--model", "claude-test",
	)
	if containsClaudeArg(args, "--disallowed-tools") {
		t.Fatal("AskUserQuestion was disabled by process arguments")
	}
	if index := indexClaudeArg(args, "--mcp-config"); index < 0 || index+1 >= len(args) {
		t.Fatal("MCP config argument missing")
	} else {
		var mcp map[string]any
		if json.Unmarshal([]byte(args[index+1]), &mcp) != nil || mcp["mcpServers"] == nil {
			t.Fatalf("MCP config shape = %q", args[index+1])
		}
	}

	resumeID := "00000000-0000-0000-0000-000000000123"
	resume := openClaudeTestSession(t, "normal", agentcore.SessionSpec{Mode: agentcore.SessionOpenResume, SessionID: "claude-resume", BackendSessionID: resumeID}, claudeNativeConfig(), nil)
	resumeArgs := readClaudeArgs(t, resume.args)
	assertClaudeArgsContain(t, resumeArgs, "--resume", resumeID)
	if containsClaudeArg(resumeArgs, "--session-id") {
		t.Fatal("resume created a fresh Claude session")
	}
	if _, _, err := claudeSessionID(agentcore.SessionSpec{Mode: agentcore.SessionOpenResume, SessionID: "bad", BackendSessionID: "not-a-uuid"}); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeInvalidPayload) {
		t.Fatalf("invalid resume id error = %v", err)
	}
}

func TestNativeClaudeFreshMultiTurnPromptCorrelationAndPartialDedup(t *testing.T) {
	session := openClaudeTestSession(t, "normal", claudeFreshSpec(), claudeNativeConfig(), nil)
	catalog := waitClaudeEvent(t, session.backend.Events(), agentcore.EventCatalogUpdated)
	if len(catalog.Event.CatalogUpdated.Models) != 2 || len(catalog.Event.CatalogUpdated.SlashCommands) != 2 {
		t.Fatalf("catalog = %#v", catalog.Event.CatalogUpdated)
	}
	if !claudeContains(catalog.Event.CatalogUpdated.Models[0].ReasoningEfforts, "ultracode") {
		t.Fatalf("xhigh model omitted ultracode: %#v", catalog.Event.CatalogUpdated.Models[0])
	}
	for turn := 1; turn <= 2; turn++ {
		clientID := fmt.Sprintf("client-%d", turn)
		receipt, err := session.backend.Dispatch(context.Background(), claudeSend(fmt.Sprintf("send-%d", turn), clientID, "hello"))
		if err != nil || receipt.Admission != agentcore.AdmissionStarted || receipt.TurnGeneration != uint64(turn) {
			t.Fatalf("turn %d receipt = %#v, %v", turn, receipt, err)
		}
		events := collectThroughClaudeEvent(t, session.backend.Events(), agentcore.EventUsageDelta)
		prompt := findClaudeEvent(events, agentcore.EventPromptAccepted)
		terminal := findClaudeEvent(events, agentcore.EventTurnResult)
		if prompt.Event.PromptAccepted == nil || prompt.Event.PromptAccepted.ClientMsgID != clientID || prompt.TurnGeneration != uint64(turn) {
			t.Fatalf("turn %d prompt = %#v", turn, prompt)
		}
		if terminal.Event.TurnResult == nil || terminal.Event.TurnResult.Epoch != uint64(turn) || terminal.Event.TurnResult.IsError {
			t.Fatalf("turn %d terminal = %#v", turn, terminal)
		}
		if text := claudeMessageText(events); text != fmt.Sprintf("reply-%d", turn) {
			t.Fatalf("partial/consolidated dedup text = %q", text)
		}
		usage := findClaudeEvent(events, agentcore.EventUsageDelta).Event.UsageDelta
		if usage.TotalTokens != 14 || usage.InputTokens != 3 || usage.OutputTokens != 2 {
			t.Fatalf("usage = %#v", usage)
		}
	}
	unknownReplay := []byte(`{"type":"user","uuid":"provider-generated","message":{"role":"user","content":[{"type":"text","text":"history"}]}}`)
	if err := session.backend.handleClaudeLine(unknownReplay); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-session.backend.Events():
		if event.Event.Kind == agentcore.EventPromptAccepted {
			t.Fatalf("unsubmitted replay was accepted: %#v", event)
		}
	case <-time.After(50 * time.Millisecond):
	}
}

func TestNativeClaudeProactiveInputIsFIFO(t *testing.T) {
	session := openClaudeTestSession(t, "manual", claudeFreshSpec(), claudeNativeConfig(), nil)
	waitClaudeEvent(t, session.backend.Events(), agentcore.EventCatalogUpdated)
	for turn := 1; turn <= 2; turn++ {
		if _, err := session.backend.Dispatch(context.Background(), claudeSend(fmt.Sprintf("send-%d", turn), fmt.Sprintf("fifo-%d", turn), fmt.Sprintf("prompt-%d", turn))); err != nil {
			t.Fatal(err)
		}
	}
	accepted := []agentcore.SessionEnvelope{
		waitClaudeEvent(t, session.backend.Events(), agentcore.EventPromptAccepted),
		waitClaudeEvent(t, session.backend.Events(), agentcore.EventPromptAccepted),
	}
	if accepted[0].TurnGeneration != 1 || accepted[0].Event.PromptAccepted.ClientMsgID != "fifo-1" || accepted[1].TurnGeneration != 2 || accepted[1].Event.PromptAccepted.ClientMsgID != "fifo-2" {
		t.Fatalf("proactive acceptance order = %#v", accepted)
	}
	users := make([]string, 0, 2)
	for _, frame := range readClaudeCapture(t, session.capture) {
		if frame["type"] == "user" {
			users = append(users, frame["uuid"].(string))
		}
	}
	if !reflect.DeepEqual(users, []string{"fifo-1", "fifo-2"}) {
		t.Fatalf("proactive wire order = %#v", users)
	}
}

func TestNativeClaudePermissionAndAskUserQuestion(t *testing.T) {
	t.Run("routine allow always", func(t *testing.T) {
		session := openClaudeTestSession(t, "permission", claudeFreshSpec(), claudeNativeConfig(), nil)
		_, _ = session.backend.Dispatch(context.Background(), claudeSend("send", "client", "permission"))
		permission := waitClaudeEvent(t, session.backend.Events(), agentcore.EventPermission)
		if permission.Event.Permission.ToolName != "Bash" || len(session.backend.PendingPermissionRequests()) != 1 {
			t.Fatalf("permission = %#v", permission.Event.Permission)
		}
		approval, ok := session.backend.PendingRuntimeApprovalRequest(permission.Event.Permission.RequestID)
		if !ok || approval.Kind != runtimecore.ApprovalKindCommandExecution || approval.ToolName != "exec_command" ||
			approval.Metadata["command"] != "echo ok" || approval.Metadata["approval_native_tool_name"] != "Bash" {
			t.Fatalf("runtime approval = %#v, ok=%v", approval, ok)
		}
		answer := agentcore.RuntimeCommand{CommandID: "answer", Kind: agentcore.RuntimeCommandAnswerPermission, AnswerPermission: &agentcore.AnswerPermissionCommand{RequestID: "perm-1", Decision: agentcore.PermissionAllowAlways}}
		if _, err := session.backend.Dispatch(context.Background(), answer); err != nil {
			t.Fatal(err)
		}
		terminal := waitClaudeEvent(t, session.backend.Events(), agentcore.EventTurnResult)
		if terminal.Event.TurnResult.IsError || len(session.backend.PendingPermissionRequests()) != 0 {
			t.Fatalf("permission terminal/pending = %#v / %#v", terminal.Event.TurnResult, session.backend.PendingPermissionRequests())
		}
		response := findClaudeCapturedType(t, session.capture, "control_response")
		result := response["response"].(map[string]any)["response"].(map[string]any)
		if result["behavior"] != "allow" || result["toolUseID"] != "tool-1" {
			t.Fatalf("permission response = %#v", response)
		}
		if _, err := session.backend.Dispatch(context.Background(), answer); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeUnknownPermissionRequest) {
			t.Fatalf("unknown permission error = %v", err)
		}
	})

	t.Run("multi-question multi-select", func(t *testing.T) {
		session := openClaudeTestSession(t, "questions", claudeFreshSpec(), claudeNativeConfig(), nil)
		_, _ = session.backend.Dispatch(context.Background(), claudeSend("send", "client", "questions"))
		permission := waitClaudeEvent(t, session.backend.Events(), agentcore.EventPermission)
		if len(permission.Event.Permission.Questions) != 2 || !permission.Event.Permission.Questions[1].MultiSelect {
			t.Fatalf("questions = %#v", permission.Event.Permission.Questions)
		}
		incomplete := agentcore.RuntimeCommand{CommandID: "incomplete", Kind: agentcore.RuntimeCommandAnswerPermission, AnswerPermission: &agentcore.AnswerPermissionCommand{
			RequestID: "question-1", Decision: agentcore.PermissionApproved, Answers: []agentcore.QuestionAnswer{{Question: "Pick one", Labels: []string{"A"}}},
		}}
		if _, err := session.backend.Dispatch(context.Background(), incomplete); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeInvalidPayload) {
			t.Fatalf("incomplete answers error = %v", err)
		}
		answer := agentcore.RuntimeCommand{CommandID: "answer", Kind: agentcore.RuntimeCommandAnswerPermission, AnswerPermission: &agentcore.AnswerPermissionCommand{
			RequestID: "question-1", Decision: agentcore.PermissionApproved,
			Answers: []agentcore.QuestionAnswer{{Question: "Pick one", Labels: []string{"B"}}, {Question: "Pick many", Labels: []string{"X", "Y"}}},
		}}
		if _, err := session.backend.Dispatch(context.Background(), answer); err != nil {
			t.Fatal(err)
		}
		waitClaudeEvent(t, session.backend.Events(), agentcore.EventTurnResult)
		response := findClaudeCapturedType(t, session.capture, "control_response")
		updated := response["response"].(map[string]any)["response"].(map[string]any)["updatedInput"].(map[string]any)
		answers := updated["answers"].(map[string]any)
		if answers["Pick one"] != "B" || !reflect.DeepEqual(answers["Pick many"], []any{"X", "Y"}) {
			t.Fatalf("question response = %#v", answers)
		}
	})

	t.Run("provider cancellation", func(t *testing.T) {
		backend := newClaudeMapperSession(t)
		request := []byte(`{"type":"control_request","request_id":"cancel-me","request":{"subtype":"can_use_tool","tool_name":"Write","tool_use_id":"tool-c","input":{}}}`)
		if err := backend.handleClaudeLine(request); err != nil {
			t.Fatal(err)
		}
		waitClaudeEvent(t, backend.Events(), agentcore.EventPermission)
		if err := backend.handleClaudeLine([]byte(`{"type":"control_cancel_request","request_id":"cancel-me"}`)); err != nil {
			t.Fatal(err)
		}
		resolved := waitClaudeEvent(t, backend.Events(), agentcore.EventPermissionResolved)
		if resolved.Event.PermissionResolved.RequestID != "cancel-me" || len(backend.PendingPermissionRequests()) != 0 {
			t.Fatalf("permission cancellation = %#v / %#v", resolved, backend.PendingPermissionRequests())
		}
	})
}

func TestNativeClaudeConfigQueueDiscoveryAndSessionInfo(t *testing.T) {
	session := openClaudeTestSession(t, "hold", claudeFreshSpec(), claudeNativeConfig(), nil)
	waitClaudeEvent(t, session.backend.Events(), agentcore.EventCatalogUpdated)
	_, _ = session.backend.Dispatch(context.Background(), claudeSend("send", "client", "hold"))
	waitClaudeEvent(t, session.backend.Events(), agentcore.EventPromptAccepted)
	settings := []agentcore.RuntimeCommand{
		{CommandID: "model-old", Kind: agentcore.RuntimeCommandSetModel, SetModel: &agentcore.SetModelCommand{Model: "claude-fast"}},
		{CommandID: "model-new", Kind: agentcore.RuntimeCommandSetModel, SetModel: &agentcore.SetModelCommand{Model: "claude-test"}},
		{CommandID: "mode", Kind: agentcore.RuntimeCommandSetMode, SetMode: &agentcore.SetModeCommand{Mode: "plan"}},
		{CommandID: "effort", Kind: agentcore.RuntimeCommandSetConfigOption, SetConfigOption: &agentcore.SetConfigOptionCommand{OptionID: "effort", Value: "ultracode"}},
	}
	for _, command := range settings {
		if _, err := session.backend.Dispatch(context.Background(), command); err != nil {
			t.Fatal(err)
		}
	}
	if countClaudeControl(t, session.capture, "set_model") != 0 {
		t.Fatal("mid-turn setting was written before the next prompt")
	}
	cancel := agentcore.RuntimeCommand{CommandID: "cancel", Kind: agentcore.RuntimeCommandCancel, Cancel: &agentcore.CancelCommand{Target: agentcore.CancelTargetTurn}}
	if _, err := session.backend.Dispatch(context.Background(), cancel); err != nil {
		t.Fatal(err)
	}
	oldTerminal := waitClaudeEvent(t, session.backend.Events(), agentcore.EventTurnResult)
	if oldTerminal.TurnGeneration != 1 || oldTerminal.Event.TurnResult.Outcome.Kind != agentcore.TurnOutcomeCancelled {
		t.Fatalf("cancel terminal = %#v", oldTerminal)
	}
	if _, err := session.backend.Dispatch(context.Background(), claudeSend("send-2", "client-2", "next")); err != nil {
		t.Fatal(err)
	}
	waitClaudeControlCounts(t, session.capture, map[string]int{"set_model": 1, "set_permission_mode": 1, "apply_flag_settings": 1})
	modelFrame := lastClaudeControl(t, session.capture, "set_model")
	if modelFrame["request"].(map[string]any)["model"] != "claude-test" {
		t.Fatalf("latest-wins model = %#v", modelFrame)
	}

	for _, info := range []struct {
		kind agentcore.SessionInfoKind
		want agentcore.SessionEventKind
	}{{agentcore.SessionInfoContextUsage, agentcore.EventSessionInfo}, {agentcore.SessionInfoSessionCost, agentcore.EventSessionInfo}} {
		command := agentcore.RuntimeCommand{CommandID: "info-" + string(info.kind), Kind: agentcore.RuntimeCommandQuerySessionInfo, QuerySessionInfo: &agentcore.QuerySessionInfoCommand{Kind: info.kind}}
		if _, err := session.backend.Dispatch(context.Background(), command); err != nil {
			t.Fatal(err)
		}
		waitClaudeEvent(t, session.backend.Events(), info.want)
	}
	invalid := agentcore.RuntimeCommand{CommandID: "invalid-model", Kind: agentcore.RuntimeCommandSetModel, SetModel: &agentcore.SetModelCommand{Model: "missing"}}
	if _, err := session.backend.Dispatch(context.Background(), invalid); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeUnsupportedCommand) {
		t.Fatalf("invalid model error = %v", err)
	}
	fast := session.backend.Capabilities()
	fast.CurrentModel = "claude-fast"
	if claudeHasEffort(fast, "xhigh") {
		t.Fatal("effort validation used the union instead of the current model")
	}
}

func TestNativeClaudeCapturedMapperSafetyWorkflowTerminalAndEpoch(t *testing.T) {
	backend := newClaudeMapperSession(t)
	// The provider's first confirmation must bind an already requested ID.
	backend.backendSessionID = "00000000-0000-0000-0000-000000000999"
	workspacePath := filepath.Join(backend.workspace.Directory, "safe.txt")
	frames := []map[string]any{
		{"type": "system", "subtype": "init", "session_id": "00000000-0000-0000-0000-000000000999", "model": "claude-test", "permissionMode": "normal"},
		{"type": "assistant", "message": map[string]any{"id": "message", "content": []any{
			map[string]any{"type": "thinking", "thinking": "reason"},
			map[string]any{"type": "text", "text": "answer"},
			map[string]any{"type": "tool_use", "id": "tool-1", "name": "Read", "input": map[string]any{"file_path": workspacePath, "outside_path": `C:\\private\\secret.txt`, "env": map[string]any{"TOKEN": "must-not-escape"}, "command": "run --api-key must-not-escape"}},
		}}},
		{"type": "user", "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "tool-1", "content": "output /private/secret/file TOKEN=must-not-escape", "is_error": false}}}},
		{"type": "system", "subtype": "task_progress", "task_id": "workflow-1", "tool_use_id": "tool-1", "workflow_name": "Build", "workflow_progress": []any{map[string]any{"type": "workflow_agent", "agentId": "agent-a", "label": "A", "state": "progress", "model": "claude-test", "tokens": 9, "toolCalls": 2, "lastToolName": "Read"}}},
		{"type": "system", "subtype": "task_notification", "task_id": "workflow-1", "status": "failed"},
		{"type": "stream_event", "event": map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "refusal"}}},
		{"type": "result", "subtype": "success", "is_error": false, "result": "", "stop_reason": "end_turn", "usage": map[string]any{"input_tokens": 1, "output_tokens": 2}},
	}
	for _, frame := range frames {
		encoded, _ := json.Marshal(frame)
		if err := backend.handleClaudeLine(encoded); err != nil {
			t.Fatal(err)
		}
	}
	events := collectThroughClaudeEvent(t, backend.Events(), agentcore.EventUsageDelta)
	counts := map[agentcore.SessionEventKind]int{}
	var thoughtKind agentcore.ReasoningContentKind
	for _, event := range events {
		counts[event.Event.Kind]++
		if event.Event.ThoughtDelta != nil {
			thoughtKind = event.Event.ThoughtDelta.ReasoningKind
		}
		if err := event.Validate(); err != nil {
			t.Fatalf("invalid event %#v: %v", event, err)
		}
		encoded, _ := json.Marshal(event)
		if strings.Contains(string(encoded), "must-not-escape") || strings.Contains(string(encoded), `C:\\private`) || strings.Contains(string(encoded), "/private/secret") || strings.Contains(string(encoded), `"env"`) {
			t.Fatalf("unsafe payload escaped: %s", encoded)
		}
	}
	for _, kind := range []agentcore.SessionEventKind{agentcore.EventBackendBound, agentcore.EventThoughtDelta, agentcore.EventMessageDelta, agentcore.EventToolCall, agentcore.EventToolResult, agentcore.EventSubagentUpdate, agentcore.EventSubagentDetail, agentcore.EventTurnResult, agentcore.EventUsageDelta} {
		if counts[kind] == 0 {
			t.Fatalf("missing mapped %s: %#v", kind, counts)
		}
	}
	if counts[agentcore.EventTurnResult] != 1 {
		t.Fatalf("duplicate terminal escaped: %#v", counts)
	}
	if thoughtKind != agentcore.ReasoningContentRaw {
		t.Fatalf("Claude thought kind = %q, want raw", thoughtKind)
	}
	terminal := findClaudeEvent(events, agentcore.EventTurnResult)
	if terminal.Event.TurnResult.Outcome.StopReason == nil || terminal.Event.TurnResult.Outcome.StopReason.Kind != agentcore.StopReasonRefused {
		t.Fatalf("early outcome = %#v", terminal.Event.TurnResult)
	}

	backend.mu.Lock()
	backend.turnGeneration = 2
	backend.turnOpen = 2
	backend.active[2] = true
	backend.terminated[2] = false
	backend.resultQueue = []uint64{1, 2}
	backend.mu.Unlock()
	late := []byte(`{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["[ede_diagnostic] hidden","cancelled"]}`)
	if err := backend.handleClaudeLine(late); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-backend.Events():
		if event.Event.Kind == agentcore.EventTurnResult && event.TurnGeneration == 2 {
			t.Fatalf("late old result ended new generation: %#v", event)
		}
	case <-time.After(50 * time.Millisecond):
	}
	if !backend.turnActive() {
		t.Fatal("late old result cleared the new turn")
	}
}

func TestNativeClaudeResultEmitsUsageBeforeTerminal(t *testing.T) {
	backend := newClaudeMapperSession(t)
	backend.mapClaudeResult(map[string]any{
		"type": "result", "subtype": "success", "is_error": false, "result": "done",
		"usage": map[string]any{"input_tokens": float64(2), "output_tokens": float64(3)},
	})
	events := collectThroughClaudeEvent(t, backend.Events(), agentcore.EventTurnResult)
	usageIndex, terminalIndex := -1, -1
	for index, envelope := range events {
		switch envelope.Event.Kind {
		case agentcore.EventUsageDelta:
			usageIndex = index
		case agentcore.EventTurnResult:
			terminalIndex = index
		}
	}
	if usageIndex < 0 || terminalIndex < 0 || usageIndex >= terminalIndex {
		t.Fatalf("event order = %#v", events)
	}
}

func TestNativeClaudePinnedWriteFixtureZeroDiff(t *testing.T) {
	backend := newClaudeMapperSession(t)
	file, err := os.Open(filepath.Join("testdata", "claude_write_tool_turn.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if err := backend.handleClaudeLine(scanner.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	events := collectThroughClaudeEvent(t, backend.Events(), agentcore.EventTurnResult)
	call := findClaudeEvent(events, agentcore.EventToolCall)
	result := findClaudeEvent(events, agentcore.EventToolResult)
	terminal := findClaudeEvent(events, agentcore.EventTurnResult)
	if call.Event.ToolCall == nil || call.Event.ToolCall.Name != "Write" || string(call.Event.ToolCall.Input) != `{"content":"hello world"}` {
		t.Fatalf("fixture tool call = %#v", call.Event.ToolCall)
	}
	if result.Event.ToolResult == nil || len(result.Event.ToolResult.Content) != 1 || strings.Contains(result.Event.ToolResult.Content[0].Text, "/tmp/") {
		t.Fatalf("fixture tool result = %#v", result.Event.ToolResult)
	}
	if terminal.Event.TurnResult == nil || terminal.Event.TurnResult.IsError || terminal.Event.TurnResult.ResultText != "Done - wrote hello.txt." {
		t.Fatalf("fixture terminal = %#v", terminal.Event.TurnResult)
	}
}

func TestNativeClaudeImageAndPlanMapping(t *testing.T) {
	backend := newClaudeMapperSession(t)
	frames := []string{
		`{"type":"assistant","message":{"id":"plan-message","content":[{"type":"tool_use","id":"task-create","name":"TaskCreate","input":{"subject":"Inspect bridge","description":"Review the runtime bridge"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"task-create","content":"Task #1 created","is_error":false}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"image-tool","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"cG5n"}}],"is_error":false}]}}`,
	}
	for _, frame := range frames {
		if err := backend.handleClaudeLine([]byte(frame)); err != nil {
			t.Fatal(err)
		}
	}
	var plan *agentcore.PlanEvent
	var image *agentcore.ToolResultEvent
	deadline := time.After(time.Second)
	for plan == nil || image == nil {
		select {
		case event := <-backend.Events():
			if event.Event.Plan != nil {
				plan = event.Event.Plan
			}
			if event.Event.ToolResult != nil && event.Event.ToolResult.ToolUseID == "image-tool" {
				image = event.Event.ToolResult
			}
		case <-deadline:
			t.Fatalf("plan/image mapping timed out: plan=%#v image=%#v", plan, image)
		}
	}
	if len(plan.Entries) != 1 || plan.Entries[0].Content != "Inspect bridge" || plan.Entries[0].Status != agentcore.PlanPending {
		t.Fatalf("plan = %#v", plan)
	}
	if len(image.Content) != 1 || image.Content[0].Kind != agentcore.ToolResultImage || image.Content[0].MediaType != "image/png" || string(image.Content[0].Data) != "png" {
		t.Fatalf("image = %#v", image)
	}
}

func TestNativeClaudeResultSemantics(t *testing.T) {
	for _, test := range []struct {
		name       string
		frame      string
		isError    bool
		text       string
		outcome    agentcore.TurnOutcomeKind
		truncation agentcore.TruncationKind
	}{
		{"subtype does not route", `{"type":"result","subtype":"success","is_error":true,"result":"failed"}`, true, "failed", agentcore.TurnOutcomeCompleted, ""},
		{"error fallback", `{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["[ede_diagnostic] hidden","rate limit"]}`, true, "rate limit", agentcore.TurnOutcomeCompleted, ""},
		{"structural fallback", `{"type":"result","subtype":"error_during_execution","is_error":true}`, true, "error_during_execution", agentcore.TurnOutcomeCompleted, ""},
		{"empty success", `{"type":"result","subtype":"success","is_error":false}`, false, "", agentcore.TurnOutcomeCompleted, ""},
		{"max turns", `{"type":"result","is_error":false,"terminal_reason":"max_turns"}`, false, "", agentcore.TurnOutcomeCompleted, agentcore.TruncationMaxTurns},
		{"cancel", `{"type":"result","is_error":false,"terminal_reason":"aborted_streaming"}`, false, "", agentcore.TurnOutcomeCancelled, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := newClaudeMapperSession(t)
			if err := backend.handleClaudeLine([]byte(test.frame)); err != nil {
				t.Fatal(err)
			}
			terminal := waitClaudeEvent(t, backend.Events(), agentcore.EventTurnResult).Event.TurnResult
			if terminal.IsError != test.isError || terminal.ResultText != test.text || terminal.Outcome.Kind != test.outcome {
				t.Fatalf("terminal = %#v", terminal)
			}
			if test.truncation != "" && (terminal.Outcome.StopReason == nil || terminal.Outcome.StopReason.Truncation != test.truncation) {
				t.Fatalf("truncation = %#v", terminal.Outcome)
			}
		})
	}
}

func TestNativeClaudeEOFStartupCrashSuspendAndBackpressure(t *testing.T) {
	t.Run("valid final frame without newline", func(t *testing.T) {
		session := openClaudeTestSession(t, "tail-valid", claudeFreshSpec(), claudeNativeConfig(), nil)
		_, _ = session.backend.Dispatch(context.Background(), claudeSend("send", "client", "tail"))
		terminal := waitClaudeEvent(t, session.backend.Events(), agentcore.EventTurnResult)
		if terminal.Event.TurnResult.IsError || terminal.Event.TurnResult.ResultText != "tail" {
			t.Fatalf("tail terminal = %#v", terminal.Event.TurnResult)
		}
	})
	t.Run("invalid truncated tail", func(t *testing.T) {
		session := openClaudeTestSession(t, "tail-invalid", claudeFreshSpec(), claudeNativeConfig(), nil)
		_, _ = session.backend.Dispatch(context.Background(), claudeSend("send", "client", "tail"))
		events := collectThroughClaudeEvent(t, session.backend.Events(), agentcore.EventDetached)
		terminal := findClaudeEvent(events, agentcore.EventTurnResult)
		if terminal.Event.TurnResult == nil || !terminal.Event.TurnResult.IsError || findClaudeEvent(events, agentcore.EventAdapterSpecific).Event.AdapterSpecific == nil {
			t.Fatalf("truncated tail events = %#v", events)
		}
	})
	t.Run("startup timeout", func(t *testing.T) {
		workspace := t.TempDir()
		connection, _ := NewNativeConnection(NativeConfig{Executable: claudeNativeTestHelper(t), Version: "2.1.220", HandshakeTimeout: 3 * time.Second, Environment: map[string]string{"ORRERY_CLAUDE_HELPER_MODE": "startup-timeout"}, ResolveWorkspace: NativeWorkspaceResolverFunc(func(context.Context, string) (NativeWorkspace, error) {
			return NativeWorkspace{Directory: workspace}, nil
		})})
		_, err := connection.OpenSession(context.Background(), claudeFreshSpec(), claudeNativeConfig())
		if !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeHandshakeTimeout) {
			t.Fatalf("startup timeout error = %v", err)
		}
	})
	t.Run("crash", func(t *testing.T) {
		session := openClaudeTestSession(t, "crash", claudeFreshSpec(), claudeNativeConfig(), nil)
		_, _ = session.backend.Dispatch(context.Background(), claudeSend("send", "client", "crash"))
		events := collectThroughClaudeEvent(t, session.backend.Events(), agentcore.EventDetached)
		if terminal := findClaudeEvent(events, agentcore.EventTurnResult); terminal.Event.TurnResult == nil || !terminal.Event.TurnResult.IsError {
			t.Fatalf("crash terminal = %#v", terminal)
		}
	})
	t.Run("suspend wake joins process", func(t *testing.T) {
		config := claudeNativeConfig()
		config.IdleTTL = 40 * time.Millisecond
		session := openClaudeTestSession(t, "normal", claudeFreshSpec(), config, nil)
		first := session.backend.currentBinding()
		waitClaudeEvent(t, session.backend.Events(), agentcore.EventBackendSuspended)
		select {
		case <-first.done:
		case <-time.After(3 * time.Second):
			t.Fatal("suspended reader was not joined")
		}
		command := agentcore.RuntimeCommand{CommandID: "wake", Kind: agentcore.RuntimeCommandSetModel, SetModel: &agentcore.SetModelCommand{Model: "claude-test"}}
		if _, err := session.backend.Dispatch(context.Background(), command); err != nil {
			t.Fatal(err)
		}
		waitClaudeEvent(t, session.backend.Events(), agentcore.EventConfigChanged)
		if second := session.backend.currentBinding(); second == first {
			t.Fatal("wake did not create a resumed process")
		}
	})
	t.Run("bounded backpressure", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		backend := &NativeSession{
			sessionID: "backpressure", now: time.Now, ctx: ctx, cancel: cancel,
			native: NativeConfig{EventCapacity: 1, BackpressureWait: 20 * time.Millisecond}, events: make(chan agentcore.SessionEnvelope, 3), eventIn: make(chan agentcore.SessionEnvelope, 1), eventAbort: make(chan struct{}), eventPumpDone: make(chan struct{}), done: make(chan struct{}),
			turnGeneration: 1, turnOpen: 1, active: map[uint64]bool{1: true}, terminated: map[uint64]bool{}, pendingControl: map[string]*claudePendingControl{}, pendingSettings: map[string]claudePendingSetting{}, pendingPerms: map[string]claudePendingPermission{},
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
			t.Fatal("backpressure did not detach")
		}
		var terminal, detached int
		for event := range backend.Events() {
			switch event.Event.Kind {
			case agentcore.EventTurnResult:
				terminal++
			case agentcore.EventDetached:
				detached++
			}
		}
		if terminal != 1 || detached != 1 {
			t.Fatalf("terminal=%d detached=%d", terminal, detached)
		}
		cancel()
		backend.closeEvents()
		<-backend.eventPumpDone
	})
}

func newClaudeMapperSession(t *testing.T) *NativeSession {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	backend := &NativeSession{
		sessionID: "mapper", workspace: NativeWorkspace{Directory: t.TempDir()}, now: time.Now, ctx: ctx, cancel: cancel,
		native: NativeConfig{EventCapacity: 128, BackpressureWait: time.Second}, events: make(chan agentcore.SessionEnvelope, 130), eventIn: make(chan agentcore.SessionEnvelope, 128), eventAbort: make(chan struct{}), eventPumpDone: make(chan struct{}), done: make(chan struct{}),
		turnGeneration: 1, turnOpen: 1, active: map[uint64]bool{1: true}, terminated: map[uint64]bool{}, resultQueue: []uint64{1}, clientGeneration: map[string]uint64{}, pendingControl: map[string]*claudePendingControl{}, pendingSettings: map[string]claudePendingSetting{}, pendingPerms: map[string]claudePendingPermission{},
		capabilities: nativeClaudeCapabilities("2.1.220"), stream: claudeStreamState{blockKinds: map[uint64]string{}}, plan: newClaudePlanTracker(),
	}
	go backend.runEventPump()
	t.Cleanup(func() {
		cancel()
		backend.closeEvents()
		<-backend.eventPumpDone
	})
	return backend
}

func waitClaudeEvent(t *testing.T, events <-chan agentcore.SessionEnvelope, kind agentcore.SessionEventKind) agentcore.SessionEnvelope {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatalf("event stream closed before %s", kind)
			}
			if event.Event.Kind == kind {
				return event
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for %s", kind)
		}
	}
}

func collectThroughClaudeEvent(t *testing.T, events <-chan agentcore.SessionEnvelope, terminal agentcore.SessionEventKind) []agentcore.SessionEnvelope {
	t.Helper()
	out := make([]agentcore.SessionEnvelope, 0)
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatalf("event stream closed before %s: %#v", terminal, out)
			}
			out = append(out, event)
			if event.Event.Kind == terminal {
				return out
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for %s: %#v", terminal, out)
		}
	}
}

func findClaudeEvent(events []agentcore.SessionEnvelope, kind agentcore.SessionEventKind) agentcore.SessionEnvelope {
	for _, event := range events {
		if event.Event.Kind == kind {
			return event
		}
	}
	return agentcore.SessionEnvelope{}
}

func claudeMessageText(events []agentcore.SessionEnvelope) string {
	var out strings.Builder
	for _, event := range events {
		if event.Event.MessageDelta != nil {
			out.WriteString(event.Event.MessageDelta.Text)
		}
	}
	return out.String()
}

func readClaudeArgs(t *testing.T, path string) []string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			var args []string
			if json.Unmarshal(data, &args) == nil {
				return args
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("argument capture unavailable: %s", path)
	return nil
}

func assertClaudeArgsContain(t *testing.T, args []string, values ...string) {
	t.Helper()
	for _, value := range values {
		if !containsClaudeArg(args, value) {
			t.Fatalf("argument %q missing from %#v", value, args)
		}
	}
}

func containsClaudeArg(args []string, value string) bool { return indexClaudeArg(args, value) >= 0 }
func indexClaudeArg(args []string, value string) int {
	for index, candidate := range args {
		if candidate == value {
			return index
		}
	}
	return -1
}

func readClaudeCapture(t *testing.T, path string) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && len(data) != 0 {
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			out := make([]map[string]any, 0, len(lines))
			for _, line := range lines {
				var frame map[string]any
				if json.Unmarshal([]byte(line), &frame) == nil {
					out = append(out, frame)
				}
			}
			return out
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

func TestNativeClaudeArgsFiltersProtocolCriticalForms(t *testing.T) {
	got := nativeClaudeArgs([]string{
		"--model", "claude-other",
		"--permission-mode=plan",
		"--resume", "00000000-0000-0000-0000-000000000999",
		"--strict-mcp-config=true",
		"--allow-dangerously-skip-permissions",
		"--debug", "allowed-value",
	})
	want := []string{"--debug", "allowed-value"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("nativeClaudeArgs = %#v, want %#v", got, want)
	}
}

func TestNativeClaudeVersionCacheIsScopedToLaunchIdentityAndVerifiesClaims(t *testing.T) {
	args := []string{"--debug"}
	environment := map[string]string{"BASE": "one"}
	connection, err := NewNativeConnection(NativeConfig{ExtraArgs: args, Environment: environment})
	if err != nil {
		t.Fatal(err)
	}
	args[0] = "mutated"
	environment["BASE"] = "mutated"
	if connection.config.ExtraArgs[0] != "--debug" || connection.config.Environment["BASE"] != "one" {
		t.Fatalf("native config was not defensively copied: %#v", connection.config)
	}
	countPath := filepath.Join(t.TempDir(), "version-count.log")
	native := NativeConfig{Executable: claudeNativeTestHelper(t)}
	environmentA := map[string]string{"ORRERY_CLAUDE_VERSION_COUNT": countPath, "ORRERY_LAUNCH_IDENTITY": "a"}
	for index := 0; index < 2; index++ {
		version, resolveErr := connection.resolveVersion(context.Background(), native, environmentA)
		if resolveErr != nil || !strings.Contains(version, "2.1.220") {
			t.Fatalf("resolve version %d = %q, %v", index, version, resolveErr)
		}
	}
	if count := claudeLineCount(t, countPath); count != 1 {
		t.Fatalf("same launch version detections = %d, want 1", count)
	}
	environmentB := map[string]string{"ORRERY_CLAUDE_VERSION_COUNT": countPath, "ORRERY_LAUNCH_IDENTITY": "b"}
	if _, err := connection.resolveVersion(context.Background(), native, environmentB); err != nil {
		t.Fatal(err)
	}
	if count := claudeLineCount(t, countPath); count != 2 {
		t.Fatalf("distinct launch version detections = %d, want 2", count)
	}
	native.Version = "2.1.219"
	if _, err := connection.resolveVersion(context.Background(), native, environmentB); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeProtocolError) {
		t.Fatalf("mismatched version claim error = %v", err)
	}
}

func claudeLineCount(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return len(strings.Fields(string(data)))
}

func countClaudeControl(t *testing.T, path, subtype string) int {
	count := 0
	for _, frame := range readClaudeCapture(t, path) {
		request, _ := frame["request"].(map[string]any)
		if request["subtype"] == subtype {
			count++
		}
	}
	return count
}

func waitClaudeControlCounts(t *testing.T, path string, want map[string]int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		counts := make(map[string]int, len(want))
		for _, frame := range readClaudeCapture(t, path) {
			request, _ := frame["request"].(map[string]any)
			if subtype, _ := request["subtype"].(string); subtype != "" {
				counts[subtype]++
			}
		}
		matched := true
		for subtype, count := range want {
			if counts[subtype] != count {
				matched = false
				break
			}
		}
		if matched {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("control counts did not reach %v: %v", want, readClaudeCapture(t, path))
}

func lastClaudeControl(t *testing.T, path, subtype string) map[string]any {
	t.Helper()
	frames := readClaudeCapture(t, path)
	for index := len(frames) - 1; index >= 0; index-- {
		request, _ := frames[index]["request"].(map[string]any)
		if request["subtype"] == subtype {
			return frames[index]
		}
	}
	t.Fatalf("control %s not captured", subtype)
	return nil
}

func findClaudeCapturedType(t *testing.T, path, frameType string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		frames := readClaudeCapture(t, path)
		for index := len(frames) - 1; index >= 0; index-- {
			if frames[index]["type"] == frameType {
				return frames[index]
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("frame type %s not captured", frameType)
	return nil
}
