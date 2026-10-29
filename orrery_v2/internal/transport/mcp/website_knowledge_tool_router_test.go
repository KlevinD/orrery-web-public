package mcp

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	toolres "orrery_v2/internal/resource/tool"
)

type fakeWebsiteKnowledgeExecutor struct {
	toolID  string
	request WebsiteKnowledgeToolRequest
}

func (f *fakeWebsiteKnowledgeExecutor) ExecuteWebsiteKnowledgeTool(_ context.Context, toolID string, request WebsiteKnowledgeToolRequest) (WebsiteKnowledgeToolResult, error) {
	f.toolID, f.request = toolID, request
	return WebsiteKnowledgeToolResult{Output: map[string]any{"status": "draft"}}, nil
}

func TestToolRouterExposesOnlyFixedWebsiteKnowledgeCurationTools(t *testing.T) {
	ctx := context.Background()
	registry := toolres.NewMemoryRegistry()
	for _, id := range []agentcore.ToolID{"website_knowledge_gap_list", "website_distillation_candidates_list", "website_faq_catalog_list", "website_faq_metrics_list", "website_knowledge_proposal_create"} {
		if err := registry.Register(ctx, toolres.ToolDefinition{
			ID: id, Kind: toolres.KindInternalServiceTool, Name: string(id), Version: "1.0.0", Enabled: true,
			Permissions: []toolres.PermissionDeclaration{{Scope: "knowledge.website_gap.propose", Risk: toolres.RiskLow, Required: true}},
			CreatedAt:   time.Now(), UpdatedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	executor := &fakeWebsiteKnowledgeExecutor{}
	router := NewToolRouter(ToolRouterOptions{Registry: registry, WebsiteKnowledgeExecutor: executor})

	result, err := router.CallTool(ctx, CallToolRequest{
		ToolID: "website_knowledge_proposal_create", PrincipalID: "website-knowledge-curator",
		Permissions: []string{"knowledge.website_gap.propose"},
		Arguments: map[string]any{
			"gap_id": "wkg_1", "target_knowledge_key": "08-Wiki/Public/Orrery/faq.md", "draft": "Draft",
			"source_refs": []any{"minviabrain://08-Wiki/Public/Orrery/overview.md"}, "rationale": "Demand",
		},
	})
	if err != nil || result.Output["status"] != "draft" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if executor.toolID != "website_knowledge_proposal_create" || executor.request.PrincipalID != "website-knowledge-curator" || len(executor.request.SourceRefs) != 1 {
		t.Fatalf("request=%+v", executor.request)
	}
	if _, err := router.CallTool(ctx, CallToolRequest{
		ToolID: "website_faq_metrics_list", PrincipalID: "website-knowledge-curator",
		Permissions: []string{"knowledge.website_gap.propose"},
	}); err != nil || executor.toolID != "website_faq_metrics_list" {
		t.Fatalf("metrics tool=%q err=%v", executor.toolID, err)
	}
	for _, readTool := range []string{"website_distillation_candidates_list", "website_faq_catalog_list"} {
		if _, err := router.CallTool(ctx, CallToolRequest{ToolID: readTool, PrincipalID: "website-knowledge-curator", Permissions: []string{"knowledge.website_gap.propose"}}); err != nil || executor.toolID != readTool {
			t.Fatalf("read tool=%q dispatched=%q err=%v", readTool, executor.toolID, err)
		}
	}
	for _, forbidden := range []string{"website_knowledge_proposal_approve", "website_knowledge_proposal_publish"} {
		if _, err := router.CallTool(ctx, CallToolRequest{ToolID: forbidden, Permissions: []string{"knowledge.website_gap.propose"}}); err == nil {
			t.Fatalf("forbidden curator tool %s was callable", forbidden)
		}
	}
}
