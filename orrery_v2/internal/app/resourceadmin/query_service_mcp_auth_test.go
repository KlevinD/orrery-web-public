package resourceadmin

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

func TestQueryServiceIncludesMaskedMCPAgentAKBindingSummary(t *testing.T) {
	ctx := context.Background()
	toolRegistry := toolres.NewMemoryRegistry()
	skillRegistry := skillres.NewMemoryRegistry()
	if err := toolRegistry.Register(ctx, toolres.ToolDefinition{
		ID:        agentcore.ToolID("mcp.private"),
		Kind:      toolres.KindMCPServer,
		Name:      "private",
		Version:   "1.0.0",
		Enabled:   true,
		RiskLevel: toolres.RiskMedium,
		MCP: &toolres.MCPServerDefinition{
			ServerName: "private",
			Transport:  toolres.TransportStreamHTTP,
			URL:        "http://127.0.0.1:5000/api/mcp",
			Auth: toolres.MCPAuthPolicy{
				Required:   true,
				Mode:       toolres.MCPAuthModePerAgent,
				HeaderName: "Authorization",
			},
			Availability: toolres.MCPAvailability{Status: toolres.MCPAvailabilityRequiresAuth},
		},
	}); err != nil {
		t.Fatalf("register mcp: %v", err)
	}
	agents := &fakeResourceAgentDirectory{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"profile-ops": {
				ID:       "profile-ops",
				ToolRefs: []agentcore.ToolRef{{ToolID: "mcp.private"}},
				MCPAuthorizations: []agentcore.MCPAuthorization{{
					ServerToolID:    "mcp.private",
					HeaderName:      "Authorization",
					CredentialValue: "Bearer agent-token",
				}},
			},
		},
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID: "profile-ops",
			DisplayName:    "Ops Agent",
			ToolRefs:       []agentcore.ToolRef{{ToolID: "mcp.private"}},
			Enabled:        true,
		}}},
	}
	service := NewQueryServiceWithAgents(toolRegistry, skillRegistry, agents)

	view, err := service.ListResourcesFiltered(ctx, ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{PermissionResourceRead},
	}, ListResourcesRequest{Kind: "mcp_server", IncludeBindings: true})
	if err != nil {
		t.Fatalf("ListResourcesFiltered: %v", err)
	}
	if len(view.Bindings) != 1 {
		t.Fatalf("bindings = %+v", view.Bindings)
	}
	binding := view.Bindings[0]
	if !binding.AgentAKConfigured || binding.AgentAKMasked != "Bear****oken" || binding.AuthHeaderName != "Authorization" {
		t.Fatalf("binding auth summary = %+v", binding)
	}
	if binding.AgentAKMasked == "Bearer agent-token" {
		t.Fatalf("binding leaked raw agent ak: %+v", binding)
	}
}
