package main

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestDefaultResourceCatalogSeedsTodoToolsInSystemMCPServer(t *testing.T) {
	ctx := context.Background()
	tools := toolres.NewMemoryRegistry()
	skills := skillres.NewMemoryRegistry()
	if err := ensureDefaultResourceCatalog(ctx, tools, skills); err != nil {
		t.Fatalf("ensure defaults: %v", err)
	}

	definition, err := tools.Get(ctx, agentcore.ToolID("mcp.system"))
	if err != nil {
		t.Fatalf("default system mcp server was not registered: %v", err)
	}
	if definition.MCP == nil {
		t.Fatalf("system mcp definition missing MCP config: %+v", definition)
	}
	exposed := map[string]bool{}
	for _, name := range definition.MCP.ExposedTools {
		exposed[name] = true
	}
	for _, want := range []agentcore.ToolID{
		toolstd.TodoCreate,
		toolstd.TodoList,
		toolstd.TodoUpdate,
		toolstd.TodoDelete,
		toolstd.TodoClaim,
		toolstd.TodoComplete,
		toolstd.TodoReopen,
	} {
		if !exposed[string(want)] {
			t.Fatalf("system mcp server missing exposed todo tool %s: %+v", want, definition.MCP.ExposedTools)
		}
	}
}
