package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/agentkernel"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/llmprofile"
	"orrery_v2/internal/platform/db"
	toolstd "orrery_v2/internal/resource/tool/std"
	httptransport "orrery_v2/internal/transport/http"
	mcptransport "orrery_v2/internal/transport/mcp"
)

type productionAgentCoreTestGateway struct{ request mcptransport.CallToolRequest }

func (g *productionAgentCoreTestGateway) CallTool(_ context.Context, request mcptransport.CallToolRequest) (mcptransport.CallToolResult, error) {
	g.request = request
	return mcptransport.CallToolResult{Output: map[string]any{"ok": true}}, nil
}

func TestProductionAgentCoreLiveModel(t *testing.T) {
	raw := os.Getenv("ORRERY_AGENTCORE_LIVE_MODEL_PROFILE")
	if raw == "" {
		t.Skip("live model profile not configured")
	}
	var profile llmprofile.LLMProfile
	if json.Unmarshal([]byte(raw), &profile) != nil {
		t.Fatal("invalid live profile")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + filepath.Join(t.TempDir(), "journal.db")})
	if err != nil {
		t.Fatal("test journal unavailable")
	}
	defer store.Close()
	journal, _ := agentkernel.NewSQLToolJournal(store)
	checkpoints, err := agentkernel.NewSQLCheckpointStore(store)
	if err != nil {
		t.Fatal("test checkpoints unavailable")
	}
	registry := newProductionRuntimeRegistry(productionDiscussionAgentDependencies{KernelJournal: journal, KernelCheckpoints: checkpoints,
		LLMProfiles:    llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{profile}},
		ToolDispatcher: productionRuntimeToolDispatcher{},
	})
	defer registry.Close()
	runtime, err := registry.ResolveRuntime(ctx, agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged})
	if err != nil {
		t.Fatal("live runtime unavailable")
	}
	capabilities := runtime.Capabilities(ctx)
	if !capabilities.SupportsResume || !capabilities.SupportedCommands.AnswerPermission {
		t.Fatal("production AgentCore did not advertise its durable/approval capabilities")
	}
	response, err := runtime.RunSession(ctx, agentcore.RuntimeRequest{
		TenantID: "agentcore-deployment-check", AgentID: "agentcore-deployment-check", RunID: "agentcore-deployment-check",
		ContextPack:    agentcore.ContextPack{AgentID: "agentcore-deployment-check"},
		InputMessages:  []agentcore.RuntimeChatMessage{{Role: "user", Content: "Reply with exactly ORRERY_AGENTCORE_LIVE_OK. Do not call tools."}},
		RuntimeOptions: agentcore.RuntimeOptions{WorkingDirectory: t.TempDir(), ProviderSpecific: map[string]string{"llm_profile_id": llmprofile.PlatformDefaultProfileID}},
	}, nil)
	if err != nil || response.Status != agentcore.RuntimeStatusCompleted {
		t.Fatal("live model execution failed")
	}
	if response.ProviderMetadata["session_id"] == "" {
		t.Fatal("live model response lost its durable session identity")
	}
	found := false
	for _, message := range response.Messages {
		found = found || strings.Contains(message.Content, "ORRERY_AGENTCORE_LIVE_OK")
	}
	if !found {
		t.Fatal("live model returned an unexpected response")
	}
}

func TestProductionAgentCoreRegisteredRunsAndPersistsToolOutcome(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var payload map[string]any
		if json.NewDecoder(r.Body).Decode(&payload) != nil || payload["model"] != "selected-model" {
			t.Error("wrong model request")
		}
		message := map[string]any{"role": "assistant", "content": "answer"}
		finish := "stop"
		if requests == 1 {
			message["content"] = ""
			message["tool_calls"] = []any{map[string]any{"id": "call-1", "type": "function", "function": map[string]any{"name": "knowledge_search", "arguments": `{"query":"public"}`}}}
			finish = "tool_calls"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message, "finish_reason": finish}}})
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
	checkpoints, err := agentkernel.NewSQLCheckpointStore(store)
	if err != nil {
		t.Fatal(err)
	}
	registry := newProductionRuntimeRegistry(productionDiscussionAgentDependencies{
		KernelJournal: journal, KernelCheckpoints: checkpoints, ToolDispatcher: productionRuntimeToolDispatcher{mcpGateway: gateway},
		LLMProfiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{
			{ProfileID: "wrong-default", Provider: "openai", BaseURL: "http://127.0.0.1:1/v1", Model: "wrong", Enabled: true, GlobalDefault: true},
			{ProfileID: "selected", Provider: "openai", BaseURL: server.URL + "/v1", Model: "selected-model", APIKey: "test", Enabled: true},
		}},
	})
	defer registry.Close()
	runtime, err := registry.ResolveRuntime(ctx, agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged})
	if err != nil {
		t.Fatal(err)
	}
	request := agentcore.RuntimeRequest{
		TenantID: "tenant-a", AgentID: "agent-a", RunID: "production-kernel-test",
		ContextPack:        agentcore.ContextPack{AgentID: "agent-a"},
		ExecutionPrincipal: agentcore.ExecutionPrincipal{Kind: agentcore.ExecutionPrincipalAgent, ID: "agent-a", TenantID: "tenant-a"},
		InputMessages:      []agentcore.RuntimeChatMessage{{Role: "user", Content: "hello"}},
		ToolBindings:       []agentcore.RuntimeToolBinding{{ToolID: toolstd.KnowledgeSearch, GrantedPermissions: []string{toolstd.PermissionKnowledgeRead}, InputSchema: map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}}}},
		RuntimeOptions:     agentcore.RuntimeOptions{WorkingDirectory: t.TempDir(), ProviderSpecific: map[string]string{"llm_profile_id": "selected"}},
	}
	response, err := runtime.RunSession(ctx, request, func(_ context.Context, event agentcore.SessionEnvelope) error { return event.Validate() })
	if err != nil || response.Status != agentcore.RuntimeStatusCompleted || requests != 2 {
		t.Fatalf("run=%+v err=%v requests=%d", response, err, requests)
	}
	if gateway.request.TenantID != "tenant-a" || gateway.request.AgentID != "agent-a" || gateway.request.Arguments["query"] != "public" {
		t.Fatalf("gateway identity lost: %+v", gateway.request)
	}
	record, found, err := journal.Get(ctx, agentkernel.ToolExecutionKey{TenantID: "tenant-a", RunID: request.RunID, CallID: "call-1"})
	if err != nil || !found || record.State != agentkernel.ToolExecutionCompleted {
		t.Fatalf("journal=%+v found=%v err=%v", record, found, err)
	}
	backendID := response.ProviderMetadata["session_id"]
	if backendID == "" {
		t.Fatal("durable checkpoint identity missing from runtime response")
	}
	request.RunID = "production-kernel-followup"
	request.RuntimeOptions.ResumeSessionID = backendID
	request.InputMessages = []agentcore.RuntimeChatMessage{{Role: "user", Content: "follow up"}}
	followup, err := runtime.RunSession(ctx, request, func(_ context.Context, event agentcore.SessionEnvelope) error { return event.Validate() })
	if err != nil || followup.Status != agentcore.RuntimeStatusCompleted || requests != 3 || followup.ProviderMetadata["session_id"] != backendID {
		t.Fatalf("checkpoint followup failed: %+v %v requests=%d", followup, err, requests)
	}
	request.RunID = "unknown-model-profile"
	request.RuntimeOptions.ProviderSpecific["llm_profile_id"] = "missing"
	_, err = runtime.RunSession(ctx, request, nil)
	if err == nil || requests != 3 {
		t.Fatalf("unknown profile fell back: %v requests=%d", err, requests)
	}
}

func TestProductionAgentCoreToolPolicyPublicIsolationAndWritesDenied(t *testing.T) {
	ctx := context.Background()
	call := runtimecore.RuntimeToolCallRequest{TenantID: "tenant-a", AgentID: agentcore.AgentID(defaultWebsiteAdvisorAgentID), RunID: "run-a"}
	for _, id := range []agentcore.ToolID{toolstd.KnowledgeSearch, toolstd.MemorySearch, toolstd.EmailSend, toolstd.PersonalAssistantShellExec} {
		call.Name = string(id)
		call.ToolBindings = []agentcore.RuntimeToolBinding{{ToolID: id, GrantedPermissions: []string{toolstd.PermissionKnowledgeRead, toolstd.PermissionMemoryRead}}}
		if err := productionAgentCoreToolPolicy(ctx, call); err == nil {
			t.Fatalf("website tool allowed: %s", id)
		}
	}
	for _, server := range []string{string(defaultOrreryPublicMCPServerToolID), "internal-management"} {
		call.Name = "search"
		call.ToolBindings = []agentcore.RuntimeToolBinding{{ToolID: agentcore.ToolID(server + ".search_documents"), Alias: "search", Source: "mcp_server", ServerToolID: server, DownstreamToolName: "search_documents"}}
		err := productionAgentCoreToolPolicy(ctx, call)
		if (err == nil) != (server == string(defaultOrreryPublicMCPServerToolID)) {
			t.Fatalf("public boundary server=%s err=%v", server, err)
		}
	}
	call.AgentID = "internal-agent"
	call.Name = string(toolstd.EmailSend)
	call.ToolBindings = []agentcore.RuntimeToolBinding{{ToolID: toolstd.EmailSend, Source: "mcp_server", ServerToolID: string(defaultOrreryPublicMCPServerToolID), DownstreamToolName: "search_documents"}}
	if productionAgentCoreToolPolicy(ctx, call) == nil {
		t.Fatal("standard write tool disguised as public MCP was allowed")
	}
	call.Name = string(toolstd.EmailSend)
	call.ToolBindings = []agentcore.RuntimeToolBinding{{ToolID: toolstd.EmailSend, GrantedPermissions: []string{toolstd.PermissionEmailSend}}}
	if productionAgentCoreToolPolicy(ctx, call) == nil {
		t.Fatal("write tool was allowed")
	}
	call.Name = string(toolstd.KnowledgeSearch)
	call.ToolBindings = []agentcore.RuntimeToolBinding{{ToolID: toolstd.KnowledgeSearch}}
	if productionAgentCoreToolPolicy(ctx, call) == nil {
		t.Fatal("ungranted read was allowed")
	}
}

func TestProductionAgentCoreProfilePreservesExistingSelectionsAndDisable(t *testing.T) {
	t.Setenv("ORRERY_BASE_DIR", t.TempDir())
	settings := httptransport.DefaultSettings()
	original := settings.RuntimeProfiles[0]
	ensureProductionAgentCoreProfile(&settings)
	ensureProductionAgentCoreProfile(&settings)
	if len(settings.RuntimeProfiles) != 2 || settings.RuntimeProfiles[0].ProfileID != original.ProfileID {
		t.Fatalf("existing selection changed: %+v", settings.RuntimeProfiles)
	}
	settings.RuntimeProfiles[1].Enabled = false
	ensureProductionAgentCoreProfile(&settings)
	if settings.RuntimeProfiles[1].Enabled {
		t.Fatal("disabled profile was re-enabled")
	}
}
