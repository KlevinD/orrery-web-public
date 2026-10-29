package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func TestACPRealProcessPromptPermissionAndControls(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	connection, err := NewConnection(Config{
		HandshakeTimeout: 5 * time.Second,
		RequestTimeout:   5 * time.Second,
		ResolveWorkspace: WorkspaceResolverFunc(func(context.Context, string) (Workspace, error) {
			return Workspace{
				Directory: workspace, Executable: executable,
				Args: []string{"-test.run=TestACPHelperProcess"}, Environment: map[string]string{"GO_WANT_ACP_HELPER": "1"}, Version: "fixture-1",
			}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	backend, err := connection.OpenSession(context.Background(), agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "acp-real"}, agentcore.SessionConfig{WorkspaceRef: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	capabilities := backend.Capabilities()
	if capabilities.CurrentModel != "fixture-model" || capabilities.CurrentMode != "code" || !capabilities.SupportedCommands.SetModel || !capabilities.SupportsResume {
		t.Fatalf("negotiated capabilities = %#v", capabilities)
	}

	receipt, err := backend.Dispatch(context.Background(), agentcore.RuntimeCommand{
		CommandID: "prompt-1", Kind: agentcore.RuntimeCommandSend,
		Send: &agentcore.SendCommand{Content: []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: "hello"}}, Metadata: agentcore.CommandMetadata{ClientMsgID: "client-1"}},
	})
	if err != nil || !receipt.Accepted || receipt.TurnGeneration != 1 {
		t.Fatalf("prompt receipt = %#v, %v", receipt, err)
	}

	var permissionID string
	var sawText bool
	deadline := time.After(5 * time.Second)
	for permissionID == "" {
		select {
		case envelope := <-backend.Events():
			switch envelope.Event.Kind {
			case agentcore.EventMessageDelta:
				sawText = strings.Contains(envelope.Event.MessageDelta.Text, "fixture")
			case agentcore.EventPermission:
				permissionID = envelope.Event.Permission.RequestID
			}
		case <-deadline:
			t.Fatal("timed out waiting for ACP permission")
		}
	}
	if !sawText || permissionID != "tool-1" {
		t.Fatalf("text/permission = %v/%q", sawText, permissionID)
	}
	if pending := backend.PendingPermissionRequests(); len(pending) != 1 || pending[0].RequestID != permissionID {
		t.Fatalf("pending permissions = %#v", pending)
	}

	if _, err := backend.Dispatch(context.Background(), agentcore.RuntimeCommand{
		CommandID: "permission-1", Kind: agentcore.RuntimeCommandAnswerPermission,
		AnswerPermission: &agentcore.AnswerPermissionCommand{RequestID: permissionID, Decision: agentcore.PermissionApproved},
	}); err != nil {
		t.Fatal(err)
	}

	var terminal *agentcore.TurnResultEvent
	var resolved bool
	deadline = time.After(5 * time.Second)
	for terminal == nil {
		select {
		case envelope := <-backend.Events():
			switch envelope.Event.Kind {
			case agentcore.EventPermissionResolved:
				resolved = envelope.Event.PermissionResolved.RequestID == permissionID
			case agentcore.EventTurnResult:
				terminal = envelope.Event.TurnResult
			}
		case <-deadline:
			t.Fatal("timed out waiting for ACP terminal")
		}
	}
	if !resolved || terminal.IsError || terminal.Outcome.Kind != agentcore.TurnOutcomeCompleted {
		t.Fatalf("resolved/terminal = %v/%#v", resolved, terminal)
	}

	for _, command := range []agentcore.RuntimeCommand{
		{CommandID: "mode", Kind: agentcore.RuntimeCommandSetMode, SetMode: &agentcore.SetModeCommand{Mode: "review"}},
		{CommandID: "model", Kind: agentcore.RuntimeCommandSetModel, SetModel: &agentcore.SetModelCommand{Model: "fixture-model-2"}},
		{CommandID: "config", Kind: agentcore.RuntimeCommandSetConfigOption, SetConfigOption: &agentcore.SetConfigOptionCommand{OptionID: "effort", Value: "high"}},
	} {
		if _, err := backend.Dispatch(context.Background(), command); err != nil {
			t.Fatalf("dispatch %s: %v", command.Kind, err)
		}
	}

	if err := connection.CloseSession(context.Background(), "acp-real"); err != nil {
		t.Fatal(err)
	}
	resumed, err := connection.OpenSession(context.Background(), agentcore.SessionSpec{Mode: agentcore.SessionOpenResume, SessionID: "acp-resume", BackendSessionID: "fixture-session"}, agentcore.SessionConfig{WorkspaceRef: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	if !resumed.Capabilities().SupportsResume {
		t.Fatal("resumed ACP session lost negotiated load capability")
	}
	if err := connection.CloseSession(context.Background(), "acp-resume"); err != nil {
		t.Fatal(err)
	}
}

func TestACPHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_ACP_HELPER") != "1" {
		return
	}
	runACPHelper()
	os.Exit(0)
}

func runACPHelper() {
	reader := bufio.NewScanner(os.Stdin)
	reader.Buffer(make([]byte, 64*1024), defaultMaxRecordBytes)
	writer := bufio.NewWriter(os.Stdout)
	conformanceMode := os.Getenv("ACP_HELPER_MODE") == "conformance"
	var promptID any
	write := func(frame any) {
		raw, _ := json.Marshal(frame)
		_, _ = writer.Write(append(raw, '\n'))
		_ = writer.Flush()
	}
	for reader.Scan() {
		var frame map[string]any
		if json.Unmarshal(reader.Bytes(), &frame) != nil {
			continue
		}
		method, _ := frame["method"].(string)
		id := frame["id"]
		params, _ := frame["params"].(map[string]any)
		switch method {
		case "initialize":
			write(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{
				"protocolVersion": 1, "agentCapabilities": map[string]any{"loadSession": true},
			}})
		case "session/new", "session/load":
			write(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{
				"sessionId":     "fixture-session",
				"models":        map[string]any{"currentModelId": "fixture-model", "availableModels": []any{map[string]any{"modelId": "fixture-model", "name": "Fixture Model"}, map[string]any{"modelId": "fixture-model-2", "name": "Fixture Model 2"}}},
				"modes":         map[string]any{"currentModeId": "code", "availableModes": []any{map[string]any{"id": "code", "name": "Code"}, map[string]any{"id": "review", "name": "Review"}}},
				"configOptions": []any{map[string]any{"id": "effort", "name": "Effort", "currentValue": "low", "options": []any{map[string]any{"value": "low"}, map[string]any{"value": "high"}}}},
			}})
		case "session/prompt":
			promptID = id
			if conformanceMode {
				continue
			}
			write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "fixture-session", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "fixture response"}}}})
			write(map[string]any{"jsonrpc": "2.0", "id": "fixture-permission", "method": "session/request_permission", "params": map[string]any{
				"sessionId": "fixture-session", "toolCall": map[string]any{"toolCallId": "tool-1", "title": "Write file", "kind": "edit"},
				"options": []any{map[string]any{"optionId": "allow", "kind": "allow_once", "name": "Allow"}, map[string]any{"optionId": "reject", "kind": "reject_once", "name": "Reject"}},
			}})
		case "session/set_mode", "session/set_model", "session/set_config_option":
			write(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}})
		case "session/cancel":
			if promptID != nil {
				write(map[string]any{"jsonrpc": "2.0", "id": promptID, "result": map[string]any{"stopReason": "cancelled"}})
				promptID = nil
			}
		default:
			if method == "" && fmt.Sprint(id) == "fixture-permission" && promptID != nil {
				result, _ := frame["result"].(map[string]any)
				outcome, _ := result["outcome"].(map[string]any)
				if outcome["optionId"] == "allow" {
					write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "fixture-session", "update": map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "tool-1", "status": "completed", "content": []any{map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": "written"}}}}}})
					write(map[string]any{"jsonrpc": "2.0", "id": promptID, "result": map[string]any{"stopReason": "end_turn"}})
					promptID = nil
				}
			}
		}
		_ = params
	}
}
