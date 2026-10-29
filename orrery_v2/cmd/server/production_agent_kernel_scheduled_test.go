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
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/llmprofile"
	"orrery_v2/internal/platform/db"
)

func TestProductionAgentCoreScheduledBusinessGrantReachesGateway(t *testing.T) {
	const toolID = "mcp.marketing.marketing_wechat_article_submit_for_approval"
	const alias = "submit_for_approval"
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		message := map[string]any{"role": "assistant", "content": "Submitted once for human approval."}
		finish := "stop"
		if requests == 1 {
			message["tool_calls"] = []any{map[string]any{"id": "submit-once", "type": "function", "function": map[string]any{"name": alias, "arguments": `{}`}}}
			finish = "tool_calls"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message, "finish_reason": finish}}})
	}))
	defer server.Close()
	ctx := context.Background()
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
		LLMProfiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{ProfileID: "selected", Provider: "openai", BaseURL: server.URL + "/v1", Model: "test", APIKey: "test", Enabled: true}}},
	})
	defer registry.Close()
	runtime, err := registry.ResolveRuntime(ctx, agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged})
	if err != nil {
		t.Fatal(err)
	}
	envelope := &agentcore.ExecutionEnvelope{TenantID: "tenant-a", AgentID: "A000005", AgentProfileID: "A000005", RunID: "scheduled-business",
		PermissionScopes: []string{"marketing.operate"}, AllowedToolIDs: []string{toolID}, ExpiresAt: time.Now().Add(time.Hour)}
	if err := envelope.Seal(); err != nil {
		t.Fatal(err)
	}
	binding := agentcore.RuntimeToolBinding{ToolID: toolID, Alias: alias, Source: "mcp_server", ServerToolID: "mcp.marketing", DownstreamToolName: "marketing_wechat_article_submit_for_approval", GrantedPermissions: []string{"marketing.operate"}}
	request := agentcore.RuntimeRequest{TenantID: "tenant-a", AgentID: "A000005", RunID: "scheduled-business",
		ContextPack: agentcore.ContextPack{AgentID: "A000005"}, ExecutionEnvelope: envelope,
		ExecutionPrincipal: agentcore.ExecutionPrincipal{Kind: agentcore.ExecutionPrincipalAgent, ID: "A000005", TenantID: "tenant-a"},
		InputMessages:      []agentcore.RuntimeChatMessage{{Role: "user", Content: "Submit for approval, then stop."}}, ToolBindings: []agentcore.RuntimeToolBinding{binding},
		RuntimeOptions: agentcore.RuntimeOptions{WorkingDirectory: t.TempDir(), ProviderSpecific: map[string]string{"llm_profile_id": "selected"}}}
	response, err := runtime.RunSession(ctx, request, nil)
	if err != nil || response.Status != agentcore.RuntimeStatusCompleted || requests != 2 {
		t.Fatalf("response=%+v err=%v requests=%d", response, err, requests)
	}
	if gateway.request.ExecutionEnvelope == nil || gateway.request.ExecutionEnvelope.EnvelopeDigest != envelope.EnvelopeDigest || gateway.request.TenantID != "tenant-a" {
		t.Fatal("sealed task authority did not reach the existing gateway")
	}
	call := runtimecore.RuntimeToolCallRequest{TenantID: "tenant-a", AgentID: "A000005", RunID: "scheduled-business", ExecutionEnvelope: envelope, Name: alias, ToolBindings: []agentcore.RuntimeToolBinding{binding}}
	for _, mode := range []string{"missing", "expired", "tenant", "unlisted", "tampered"} {
		t.Run(mode, func(t *testing.T) {
			bad := call
			bad.ExecutionEnvelope = agentcore.CloneExecutionEnvelope(envelope)
			switch mode {
			case "missing":
				bad.ExecutionEnvelope = nil
			case "tenant":
				bad.TenantID = "tenant-b"
			case "tampered":
				bad.ExecutionEnvelope.PermissionScopes = append(bad.ExecutionEnvelope.PermissionScopes, "admin")
			case "expired":
				bad.ExecutionEnvelope.ExpiresAt = time.Now().Add(-time.Minute)
				_ = bad.ExecutionEnvelope.Seal()
			case "unlisted":
				bad.ExecutionEnvelope.AllowedToolIDs = []string{"another.tool"}
				_ = bad.ExecutionEnvelope.Seal()
			}
			if productionAgentCoreToolPolicy(ctx, bad) == nil {
				t.Fatal("invalid task grant allowed")
			}
		})
	}
}

func TestProductionAgentCoreLegacyMCPGrantStillHonorsNarrowTaskFence(t *testing.T) {
	binding := agentcore.RuntimeToolBinding{ToolID: "finance-adapter.finance_portfolio_snapshot", Source: "mcp_server", ServerToolID: "finance-adapter", DownstreamToolName: "finance_portfolio_snapshot"}
	envelope := &agentcore.ExecutionEnvelope{TenantID: "tenant-a", AgentID: "A000002", AgentProfileID: "A000002", RunID: "finance", PermissionScopes: []string{"tool.call"}}
	if err := envelope.Seal(); err != nil {
		t.Fatal(err)
	}
	call := runtimecore.RuntimeToolCallRequest{TenantID: "tenant-a", AgentID: "A000002", RunID: "finance", Name: string(binding.ToolID), ToolBindings: []agentcore.RuntimeToolBinding{binding}, ExecutionEnvelope: envelope}
	if productionAgentCoreToolPolicy(context.Background(), call) != nil {
		t.Fatal("existing explicit gateway permission lost")
	}
	envelope.AllowedToolIDs = []string{"another.tool"}
	_ = envelope.Seal()
	if productionAgentCoreToolPolicy(context.Background(), call) == nil {
		t.Fatal("legacy permission bypassed the narrower task fence")
	}
}
