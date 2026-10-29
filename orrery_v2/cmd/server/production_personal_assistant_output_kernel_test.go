package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/agentkernel"
	"orrery_v2/internal/agent/runtime/llmprofile"
	"orrery_v2/internal/platform/db"
	toolstd "orrery_v2/internal/resource/tool/std"
)

// Actual production runtime + policy + dispatcher + Shell, with a local model
// fixture driving range reads. No paid model or production business data.
func TestPersonalAssistantShellOutputKernelReadsCompleteFileOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX range-read integration; run on Linux")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	root := t.TempDir()
	original := strings.Repeat("段落🙂", 30000) + "COMPLETE_TAIL"
	if err := os.WriteFile(filepath.Join(root, "document.txt"), []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	requests := 0
	collected, savedPath := "", ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var payload struct {
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if requests > 1 {
			last := payload.Messages[len(payload.Messages)-1]
			var receipt struct {
				Stdout       string `json:"stdout"`
				StdoutOutput struct {
					Path   string `json:"path"`
					Offset int    `json:"next_byte_offset"`
				} `json:"stdout_output"`
			}
			if last.Role != "tool" || len(last.Content) > personalAssistantShellReceiptBytes || json.Unmarshal([]byte(last.Content), &receipt) != nil {
				t.Error("model did not receive a bounded structured tool receipt")
			}
			if !utf8.ValidString(receipt.Stdout) {
				t.Error("invalid UTF-8 reached the model")
			}
			collected += receipt.Stdout
			if requests == 2 {
				savedPath = receipt.StdoutOutput.Path
				if savedPath == "" || receipt.StdoutOutput.Offset != len(receipt.Stdout) {
					t.Errorf("fixture missing continuation: %s", last.Content)
				}
			}
		}
		message := map[string]any{"role": "assistant", "content": "FULL_OUTPUT_OK"}
		finish := "stop"
		if len(collected) < len(original) && requests < 12 {
			command := "printf once >> command-count; cat document.txt"
			if requests > 1 {
				quoted := "'" + strings.ReplaceAll(savedPath, "'", "'\"'\"'") + "'"
				command = fmt.Sprintf("dd if=%s bs=1 skip=%d count=65540 status=none", quoted, len(collected))
			}
			arguments, _ := json.Marshal(map[string]any{"command": command, "max_output_len": 300000})
			message = map[string]any{"role": "assistant", "content": "", "tool_calls": []any{
				map[string]any{"id": fmt.Sprintf("read-%d", requests), "type": "function",
					"function": map[string]any{"name": "Shell", "arguments": string(arguments)}},
			}}
			finish = "tool_calls"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message, "finish_reason": finish}}})
	}))
	defer server.Close()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + filepath.Join(t.TempDir(), "journal.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	journal, err := agentkernel.NewSQLToolJournal(store)
	if err != nil {
		t.Fatal(err)
	}
	envelope := &agentcore.ExecutionEnvelope{TenantID: "fixture", RunID: "shell-output-test", AgentID: "A000017", AgentProfileID: "A000017",
		PrincipalID: "fixture-owner", PrincipalKind: agentcore.ExecutionPrincipalUser,
		PermissionScopes: []string{toolstd.PermissionPersonalAssistantShell}, AllowedToolIDs: []string{string(toolstd.PersonalAssistantShellExec)}}
	if err := envelope.SealAt(time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	registry := newProductionRuntimeRegistry(productionDiscussionAgentDependencies{KernelJournal: journal,
		ToolDispatcher: productionRuntimeToolDispatcher{personalShell: productionPersonalAssistantShellExecutor{}},
		LLMProfiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{ProfileID: "fixture", Provider: "openai",
			BaseURL: server.URL + "/v1", Model: "fixture", APIKey: "fixture", ContextLength: 262144, Enabled: true}}},
	})
	defer registry.Close()
	managed, err := registry.ResolveRuntime(ctx, agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged})
	if err != nil {
		t.Fatal(err)
	}
	var binding agentcore.RuntimeToolBinding
	for _, definition := range toolstd.Definitions(time.Now().UTC()) {
		if definition.ID == toolstd.PersonalAssistantShellExec {
			binding = agentcore.RuntimeToolBinding{ToolID: definition.ID, Alias: "Shell", InputSchema: definition.InputSchema,
				GrantedPermissions: []string{toolstd.PermissionPersonalAssistantShell}}
		}
	}
	result, err := managed.RunSession(ctx, agentcore.RuntimeRequest{TenantID: "fixture", AgentID: "A000017", RunID: "shell-output-test",
		ExecutionEnvelope: envelope, ExecutionPrincipal: agentcore.ExecutionPrincipal{ID: envelope.PrincipalID, Kind: envelope.PrincipalKind,
			TenantID: envelope.TenantID, PermissionScope: envelope.PermissionScopes},
		ContextPack: agentcore.ContextPack{AgentID: "A000017"}, ToolBindings: []agentcore.RuntimeToolBinding{binding},
		InputMessages: []agentcore.RuntimeChatMessage{{Role: "user", Content: "Read the complete fixture without repeating the original command."}},
		RuntimeOptions: agentcore.RuntimeOptions{WorkingDirectory: root,
			WorkspacePolicy:  agentcore.RuntimeWorkspacePolicy{Root: root, WorkDir: root, EnforceBoundary: true},
			ProviderSpecific: map[string]string{"llm_profile_id": "fixture"}},
	}, nil)
	if err != nil || result.Status != agentcore.RuntimeStatusCompleted || collected != original || requests < 3 {
		t.Fatalf("incomplete file interpretation: status=%s failure=%+v collected=%d want=%d requests=%d err=%v", result.Status, result.Error, len(collected), len(original), requests, err)
	}
	count, err := os.ReadFile(filepath.Join(root, "command-count"))
	if err != nil || string(count) != "once" {
		t.Fatal("original command was repeated")
	}
	record, found, err := journal.Get(ctx, agentkernel.ToolExecutionKey{TenantID: "fixture", RunID: "shell-output-test", CallID: "read-1"})
	if err != nil || !found || record.State != agentkernel.ToolExecutionCompleted {
		t.Fatal("completed file read marked uncertain")
	}
	t.Logf("complete_source_bytes=%d reconstructed_bytes=%d model_fixture_requests=%d original_command_executions=1", len(original), len(collected), requests)
}
