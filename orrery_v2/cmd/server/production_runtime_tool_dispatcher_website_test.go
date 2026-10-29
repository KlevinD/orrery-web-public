package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	websitechannel "orrery_v2/internal/gateway/channel/website"
	toolres "orrery_v2/internal/resource/tool"
	mcptransport "orrery_v2/internal/transport/mcp"
)

func TestProductionRuntimeToolDispatcherClampsOrreryPublicSearchLimit(t *testing.T) {
	gateway := &productionRuntimeTestMCPGateway{}
	dispatcher := productionRuntimeToolDispatcher{mcpGateway: gateway}

	_, err := dispatcher.CallRuntimeTool(context.Background(), runtimecore.RuntimeToolCallRequest{
		TenantID: "tenant-website",
		Name:     "mcp_orrery_public_readonly_search_documents",
		AgentID:  agentcore.AgentID(defaultWebsiteAdvisorAgentID),
		Input:    map[string]any{"query": "Orrery", "limit": float64(10)},
		ToolBindings: []agentcore.RuntimeToolBinding{{
			ToolID:             "mcp.orrery-public-readonly.search_documents",
			Alias:              "mcp_orrery_public_readonly_search_documents",
			Source:             "mcp_server",
			ServerToolID:       string(defaultOrreryPublicMCPServerToolID),
			DownstreamToolName: "search_documents",
		}},
	})
	if err != nil {
		t.Fatalf("CallRuntimeTool(public search): %v", err)
	}
	if gateway.request.Arguments["limit"] != 6 {
		t.Fatalf("public search limit = %#v, want 6", gateway.request.Arguments["limit"])
	}
	if gateway.request.TenantID != "tenant-website" {
		t.Fatalf("gateway tenant id = %q", gateway.request.TenantID)
	}
}

func TestProductionRuntimeToolDispatcherRoutesRestrictedWebsiteInputsThroughGateway(t *testing.T) {
	ctx := context.Background()
	registry := toolres.NewMemoryRegistry()
	for _, definition := range defaultWebsiteKnowledgeCurationToolDefinitions(time.Now()) {
		if err := registry.Register(ctx, definition); err != nil {
			t.Fatal(err)
		}
	}
	store := &fakeWebsiteKnowledgeCurationStore{emptyInputs: true}
	publisher := &fakeWebsiteLowRiskPublisher{}
	router := mcptransport.NewToolRouter(mcptransport.ToolRouterOptions{Registry: registry,
		WebsiteKnowledgeExecutor: productionWebsiteKnowledgeCuration{store: store, catalog: websitechannel.NewWebsiteFAQCatalog(nil), publisher: publisher},
	})
	dispatcher := productionRuntimeToolDispatcher{mcpGateway: router}
	refs := []agentcore.ToolRef{defaultWebsiteDistillationCandidatesListToolRef(), defaultWebsiteFAQCatalogListToolRef(), defaultWebsiteFAQMetricsListToolRef()}
	for _, ref := range refs {
		t.Run(string(ref.ToolID), func(t *testing.T) {
			allowed, _ := json.Marshal([]string{string(ref.ToolID)})
			call := runtimecore.RuntimeToolCallRequest{
				TenantID: "system", AgentID: "A000009", RunID: "website-run", Name: ref.Alias,
				CallID:           "read-call-" + string(ref.ToolID),
				ProviderSpecific: map[string]string{agentcore.RuntimeAllowedToolIDsProviderKey: string(allowed)},
				ToolBindings:     []agentcore.RuntimeToolBinding{{ToolID: ref.ToolID, Alias: ref.Alias, GrantedPermissions: []string{ref.PermissionScope}}},
			}
			result, err := dispatcher.callRuntimeTool(ctx, call)
			if err != nil || len(result.Output) == 0 {
				t.Fatalf("bound website input could not reach its gateway: %+v %v", result, err)
			}
			if result.Output["evidence_ref"] != call.CallID {
				t.Fatalf("website input must expose its actual evidence reference: %+v", result.Output)
			}
			raw, err := json.Marshal(result.Output)
			if err != nil {
				t.Fatal(err)
			}
			calls := []agentcore.RuntimeToolCall{{ID: "recorded-call", Name: ref.Alias, Output: string(raw)}}
			if resolved, err := runtimeToolCallByEvidenceRef(calls, call.CallID); err != nil || resolved.ID != "recorded-call" {
				t.Fatalf("host evidence reference cannot resolve its actual tool result: %+v %v", resolved, err)
			}
			call.ToolBindings[0].GrantedPermissions = nil
			if _, err := dispatcher.callRuntimeTool(ctx, call); !errors.Is(err, mcptransport.ErrPermissionDenied) {
				t.Fatalf("missing permission was accepted: %v", err)
			}
			call.ToolBindings = nil
			if _, err := dispatcher.callRuntimeTool(ctx, call); !errors.Is(err, mcptransport.ErrPermissionDenied) {
				t.Fatalf("unbound website tool was accepted: %v", err)
			}
		})
	}
	if !store.deletedBefore.IsZero() || publisher.calls != 0 || store.createdInput.Draft != "" {
		t.Fatalf("website input reads performed writes: store=%+v publisher=%+v", store, publisher)
	}
	store.readErr = errors.New("website input read unavailable")
	result, err := dispatcher.CallRuntimeTool(ctx, runtimecore.RuntimeToolCallRequest{
		TenantID: "system", AgentID: "A000009", Name: "website_faq_metrics",
		ToolBindings: []agentcore.RuntimeToolBinding{{ToolID: defaultWebsiteFAQMetricsListToolID, Alias: "website_faq_metrics", GrantedPermissions: []string{defaultWebsiteKnowledgeCurationMCPPermissionScope}}},
	})
	if err != nil || result.Output["status"] != "failed" || result.Output["error"] != store.readErr.Error() {
		t.Fatalf("input failure must stay observable: %+v %v", result, err)
	}
	if _, exists := result.Output["evidence_ref"]; exists {
		t.Fatalf("failed input must not expose a successful evidence reference: %+v", result.Output)
	}
}

func TestProductionRuntimeToolDispatcherEnforcesAllowlistBeforeSpecialTools(t *testing.T) {
	dispatcher := productionRuntimeToolDispatcher{}
	for _, name := range []string{"load_skill", "personal_assistant_shell_exec", "website_knowledge_proposal_create", "knowledge_add"} {
		_, err := dispatcher.callRuntimeTool(context.Background(), runtimecore.RuntimeToolCallRequest{
			Name: name, ProviderSpecific: map[string]string{agentcore.RuntimeAllowedToolIDsProviderKey: `["website_faq_catalog_list"]`},
			ToolBindings: []agentcore.RuntimeToolBinding{{ToolID: agentcore.ToolID(name), Alias: name}},
		})
		if !errors.Is(err, mcptransport.ErrPermissionDenied) {
			t.Fatalf("non-allowlisted special tool %s bypassed task policy: %v", name, err)
		}
	}
}
