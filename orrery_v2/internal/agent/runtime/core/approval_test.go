package core

import (
	"context"
	"strings"
	"testing"
)

func TestApprovalMetadataNormalizesProviderRequest(t *testing.T) {
	metadata := ApprovalMetadata(ApprovalRequest{
		Source:    "codex",
		RequestID: "cmd-1",
		SessionID: "thread-1",
		TurnID:    "turn-1",
		Kind:      ApprovalKindCommandExecution,
		ToolName:  "exec_command",
		Metadata:  map[string]string{"scope": "repo:write"},
	})

	for key, want := range map[string]string{
		"approval_source":     "codex",
		"approval_provider":   "codex",
		"approval_request_id": "cmd-1",
		"approval_session_id": "thread-1",
		"approval_turn_id":    "turn-1",
		"approval_kind":       string(ApprovalKindCommandExecution),
		"approval_tool_name":  "exec_command",
		"approval_type":       "command_execution",
		"scope":               "repo:write",
	} {
		if metadata[key] != want {
			t.Fatalf("metadata[%s] = %q, want %q in %#v", key, metadata[key], want, metadata)
		}
	}
}

func TestApprovalMetadataProjectsCanonicalSourceAsProvider(t *testing.T) {
	metadata := ApprovalMetadata(ApprovalRequest{
		Source:    "claude",
		RequestID: "perm-1",
		Kind:      ApprovalKindTool,
		ToolName:  "exec_command",
	})

	if metadata["approval_source"] != "claude" {
		t.Fatalf("approval source = %q, want claude in %#v", metadata["approval_source"], metadata)
	}
	if metadata["approval_provider"] != "claude" {
		t.Fatalf("approval provider projection should mirror canonical source, got %#v", metadata)
	}
}

func TestDeferredApprovalBrokerLeavesProviderWaiting(t *testing.T) {
	called := false
	err := DeferredApprovalBroker{}.RequestApproval(context.Background(), ApprovalRequest{RequestID: "approval-1"}, func(ApprovalResponse) error {
		called = true
		return nil
	})

	if err != nil {
		t.Fatalf("RequestApproval returned error: %v", err)
	}
	if called {
		t.Fatalf("deferred approval broker should not respond synchronously")
	}
}

func TestApprovalContinuationBuildsProviderAgnosticPromptAndMetadata(t *testing.T) {
	metadata := map[string]string{
		"approval_source":     "codex",
		"approval_request_id": "cmd-1",
		"approval_session_id": "thread-1",
		"approval_kind":       "command_execution",
		"approval_tool_name":  "exec_command",
	}
	continuation := ApprovalContinuation{
		Decision:      ApprovalDecisionApproved,
		ActionID:      "approve_edited",
		Effect:        "modified",
		Message:       "Use the narrowed command.",
		ModifiedInput: "go test ./internal/app/missioncontrol",
		Remember:      true,
	}

	prompt := ApprovalContinuationPrompt(metadata, continuation)
	if !strings.Contains(prompt, "approved") || !strings.Contains(prompt, "go test ./internal/app/missioncontrol") || !strings.Contains(prompt, "Resume from the prior provider session") {
		t.Fatalf("continuation prompt = %q", prompt)
	}
	providerSpecific := ApprovalContinuationProviderSpecific(metadata, continuation)
	if providerSpecific["approval_continuation"] != "true" ||
		providerSpecific["approval_request_id"] != "cmd-1" ||
		providerSpecific["approval_effect"] != "modified" ||
		providerSpecific["approval_remember"] != "true" {
		t.Fatalf("provider specific = %+v", providerSpecific)
	}
}

func TestApprovalKindAndPayloadMetadataNormalizeProviderDifferences(t *testing.T) {
	for _, nativeTool := range []string{"Bash", "PowerShell", "CommandExecution", "exec_command", "run_command"} {
		kind, tool := ApprovalKindAndToolName(nativeTool)
		if kind != ApprovalKindCommandExecution || tool != "exec_command" {
			t.Fatalf("%s approval kind/tool = %s/%s", nativeTool, kind, tool)
		}
	}
	kind, tool := ApprovalKindAndToolName("apply_patch")
	if kind != ApprovalKindFileChange || tool != "patch_apply" {
		t.Fatalf("patch approval kind/tool = %s/%s", kind, tool)
	}
	kind, tool = ApprovalKindAndToolName("FileChange")
	if kind != ApprovalKindFileChange || tool != "patch_apply" {
		t.Fatalf("file change approval kind/tool = %s/%s", kind, tool)
	}
	kind, tool = ApprovalKindAndToolName("AskUserQuestion")
	if kind != ApprovalKindMCPElicitation || tool != "AskUserQuestion" {
		t.Fatalf("elicitation approval kind/tool = %s/%s", kind, tool)
	}
	metadata := ApprovalPayloadMetadata([]byte(`{"parameters":{"command":"npm test","url":"https://example.com"}}`))
	if metadata["command"] != "npm test" || metadata["payload_kind"] != "command_execution" || metadata["url"] != "https://example.com" {
		t.Fatalf("payload metadata = %+v", metadata)
	}
	intent := ApprovalPayloadMetadata([]byte(`{"input":{"title":"Enterprise agent platform","final_goal":"Deliver a deployable release","root_goal":"Coordinate the delivery","idempotency_key":"do-not-expose","api_token":"secret"}}`))
	if intent["title"] != "Enterprise agent platform" || intent["final_goal"] != "Deliver a deployable release" || intent["root_goal"] != "Coordinate the delivery" {
		t.Fatalf("intent metadata = %+v", intent)
	}
	if intent["idempotency_key"] != "" || intent["api_token"] != "" {
		t.Fatalf("internal or secret arguments leaked into intent metadata: %+v", intent)
	}
}

func TestApprovalMetadataDoesNotExposeWorkspaceRoot(t *testing.T) {
	metadata := ApprovalMetadata(ApprovalRequest{
		Source:        "pi",
		RequestID:     "approval-workspace",
		WorkspaceRoot: "/srv/private/runtime-workspace",
	})
	for key, value := range metadata {
		if strings.Contains(key, "workspace") || strings.Contains(value, "/srv/private/runtime-workspace") {
			t.Fatalf("approval metadata exposed server-owned workspace root: %q=%q", key, value)
		}
	}
}
