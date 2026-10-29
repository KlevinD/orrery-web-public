package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/agentkernel"
	"orrery_v2/internal/agent/runtime/llmprofile"
	"orrery_v2/internal/platform/db"
	toolstd "orrery_v2/internal/resource/tool/std"
)

// Exercise the deployed registry, workspace broker, model selector, kernel,
// dispatcher and session events with the same default reference as live agents.
func TestProductionAgentCorePlatformDefaultRepliesThroughResponses(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || r.URL.Path != "/v1/responses" || body["model"] != "current-model" || body["store"] != false || body["stream"] != true {
			t.Error("default model protocol or request was incorrect")
		}
		if r.Header.Get("Authorization") != "Bearer fixture-key" {
			t.Error("credential was not resolved")
		}
		output := []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": "reply restored"}}}}
		if requests == 1 {
			tools, _ := body["tools"].([]any)
			if len(tools) != 1 || tools[0].(map[string]any)["name"] != "knowledge_search" {
				t.Error("Responses tools were not flattened")
			}
			output = []any{
				map[string]any{"type": "reasoning", "id": "rs-fixture", "summary": []any{}, "encrypted_content": "opaque-fixture"},
				map[string]any{"type": "function_call", "call_id": "call-fixture", "name": "knowledge_search", "arguments": `{"query":"public"}`},
			}
		} else {
			foundResult, foundReasoning := false, false
			for _, raw := range body["input"].([]any) {
				item := raw.(map[string]any)
				foundResult = foundResult || item["type"] == "function_call_output" && item["call_id"] == "call-fixture"
				foundReasoning = foundReasoning || item["type"] == "reasoning" && item["encrypted_content"] == "opaque-fixture"
			}
			if !foundResult || !foundReasoning {
				t.Error("tool continuation lost its call identity or reasoning state")
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(event any) {
			_, _ = w.Write([]byte("data: "))
			_ = json.NewEncoder(w).Encode(event)
			_, _ = w.Write([]byte("\n"))
			w.(http.Flusher).Flush()
		}
		if requests == 2 {
			for _, delta := range []string{"reply ", "restored"} {
				send(map[string]any{"type": "response.output_text.delta", "delta": delta, "output_index": 0, "content_index": 0})
			}
		}
		send(map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "output": output, "usage": map[string]any{"input_tokens": 5, "output_tokens": 3, "total_tokens": 8}}})
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + filepath.Join(t.TempDir(), "journal.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	journal, err := agentkernel.NewSQLToolJournal(store)
	if err != nil {
		t.Fatal(err)
	}
	gateway := &productionAgentCoreTestGateway{}
	registry := newProductionRuntimeRegistry(productionDiscussionAgentDependencies{
		KernelJournal: journal, ToolDispatcher: productionRuntimeToolDispatcher{mcpGateway: gateway},
		LLMProfiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{
			{ProfileID: "wrong-first", Enabled: true, Model: "wrong", BaseURL: "http://127.0.0.1:1"},
			{ProfileID: "current-default", Enabled: true, GlobalDefault: true, Model: "current-model", Provider: "custom-openai-responses", BaseURL: server.URL + "/v1", APIKeyRef: "fixture"},
		}}, Secrets: llmprofile.StaticSecretResolver{Values: map[string]string{"fixture": "fixture-key"}},
	})
	defer registry.Close()
	runtime, err := registry.ResolveRuntime(ctx, agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged})
	if err != nil {
		t.Fatal(err)
	}
	var deltas []string
	finalized := 0
	response, err := runtime.RunSession(ctx, agentcore.RuntimeRequest{
		TenantID: "tenant-a", AgentID: "agent-a", RunID: "default-reply-fixture",
		ContextPack:        agentcore.ContextPack{AgentID: "agent-a"},
		ExecutionPrincipal: agentcore.ExecutionPrincipal{Kind: agentcore.ExecutionPrincipalAgent, ID: "agent-a", TenantID: "tenant-a"},
		InputMessages:      []agentcore.RuntimeChatMessage{{Role: "user", Content: "hello"}},
		ToolBindings:       []agentcore.RuntimeToolBinding{{ToolID: toolstd.KnowledgeSearch, GrantedPermissions: []string{toolstd.PermissionKnowledgeRead}, InputSchema: map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}}}},
		RuntimeOptions:     agentcore.RuntimeOptions{WorkingDirectory: t.TempDir(), ProviderSpecific: map[string]string{"llm_profile_id": llmprofile.PlatformDefaultProfileID}},
	}, func(_ context.Context, event agentcore.SessionEnvelope) error {
		if event.Event.Kind == agentcore.EventMessageDelta && event.Event.MessageDelta != nil {
			deltas = append(deltas, event.Event.MessageDelta.Text)
		}
		if event.Event.Kind == agentcore.EventMessageFinalized {
			finalized++
		}
		return event.Validate()
	})
	if err != nil || response.Status != agentcore.RuntimeStatusCompleted || requests != 2 {
		t.Fatalf("reply failed: %v status=%s requests=%d", err, response.Status, requests)
	}
	if len(deltas) != 2 || deltas[0] != "reply " || deltas[1] != "restored" || finalized != 1 {
		t.Fatalf("streaming or finalization changed: deltas=%v finalized=%d", deltas, finalized)
	}
	found := false
	for _, m := range response.Messages {
		found = found || m.Content == "reply restored"
	}
	if !found || gateway.request.AgentID != "agent-a" || gateway.request.TenantID != "tenant-a" {
		t.Fatal("reply or scoped tool delivery was lost")
	}
	record, exists, err := journal.Get(ctx, agentkernel.ToolExecutionKey{TenantID: "tenant-a", RunID: "default-reply-fixture", CallID: "call-fixture"})
	if err != nil || !exists || record.State != agentkernel.ToolExecutionCompleted {
		t.Fatal("tool outcome was not durably recorded")
	}
}
