package resourceadmin

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	toolres "orrery_v2/internal/resource/tool"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestMCPToolCacheSyncerUpdatesEnabledMCPServerToolCaches(t *testing.T) {
	ctx := context.Background()
	registry := toolres.NewMemoryRegistry()
	registerMCPDefinitionForSync(t, ctx, registry, "mcp.github", true)
	registerMCPDefinitionForSync(t, ctx, registry, "mcp.disabled", false)
	if err := registry.Register(ctx, toolres.ToolDefinition{
		ID:        "tool.local",
		Kind:      toolres.KindInternalServiceTool,
		Name:      "Local",
		Version:   "1.0.0",
		Enabled:   true,
		RiskLevel: toolres.RiskLow,
	}); err != nil {
		t.Fatalf("register local: %v", err)
	}
	runtime := &fakeMCPRuntime{
		tools: []string{"search_issues", "create_issue"},
		toolDetails: []toolres.MCPExposedToolDefinition{{
			Name:        "search_issues",
			Description: "Search GitHub issues by title, label, assignee, or milestone",
		}},
		latency: 15 * time.Millisecond,
	}
	syncer := NewMCPToolCacheSyncer(MCPToolCacheSyncerOptions{Registry: registry, Runtime: runtime})

	if err := syncer.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	github, err := registry.Get(ctx, agentcore.ToolID("mcp.github"))
	if err != nil {
		t.Fatalf("get synced mcp: %v", err)
	}
	if len(github.MCP.ExposedTools) != 2 || github.MCP.ExposedTools[0] != "search_issues" {
		t.Fatalf("github exposed tools = %+v", github.MCP.ExposedTools)
	}
	if len(github.MCP.ExposedToolDetails) != 1 ||
		github.MCP.ExposedToolDetails[0].Name != "search_issues" ||
		github.MCP.ExposedToolDetails[0].Description == "" {
		t.Fatalf("github exposed tool details = %+v", github.MCP.ExposedToolDetails)
	}
	if github.MCP.Availability.Status != toolres.MCPAvailabilityAvailable {
		t.Fatalf("github availability = %+v", github.MCP.Availability)
	}
	disabled, err := registry.Get(ctx, agentcore.ToolID("mcp.disabled"))
	if err != nil {
		t.Fatalf("get disabled mcp: %v", err)
	}
	if len(disabled.MCP.ExposedTools) != 1 || disabled.MCP.ExposedTools[0] != "stale" {
		t.Fatalf("disabled server should not sync: %+v", disabled.MCP.ExposedTools)
	}
	if runtime.syncToolID != "mcp.github" {
		t.Fatalf("runtime synced tool id = %q", runtime.syncToolID)
	}
}

func TestMCPToolCacheSyncerContinuesAfterOneServerFails(t *testing.T) {
	ctx := context.Background()
	registry := toolres.NewMemoryRegistry()
	registerMCPDefinitionForSync(t, ctx, registry, "mcp.first", true)
	registerMCPDefinitionForSync(t, ctx, registry, "mcp.second", true)
	runtime := &perServerMCPRuntime{
		toolsByID: map[string][]string{"mcp.second": []string{"list_projects"}},
		errByID:   map[string]error{"mcp.first": errors.New("connection refused")},
	}
	syncer := NewMCPToolCacheSyncer(MCPToolCacheSyncerOptions{Registry: registry, Runtime: runtime})

	err := syncer.Sweep(ctx)
	if err == nil || !errors.Is(err, ErrPartialMCPSync) {
		t.Fatalf("Sweep error = %v, want ErrPartialMCPSync", err)
	}

	second, err := registry.Get(ctx, agentcore.ToolID("mcp.second"))
	if err != nil {
		t.Fatalf("get second: %v", err)
	}
	if len(second.MCP.ExposedTools) != 1 || second.MCP.ExposedTools[0] != "list_projects" {
		t.Fatalf("second should still sync after first failure: %+v", second.MCP.ExposedTools)
	}
	if second.MCP.Availability.Status != toolres.MCPAvailabilityAvailable {
		t.Fatalf("second availability = %+v", second.MCP.Availability)
	}
	first, err := registry.Get(ctx, agentcore.ToolID("mcp.first"))
	if err != nil {
		t.Fatalf("get first: %v", err)
	}
	if first.MCP.Availability.Status != toolres.MCPAvailabilityUnavailable || first.MCP.Availability.FailureReason == "" {
		t.Fatalf("first availability = %+v", first.MCP.Availability)
	}
}

func TestMCPToolCacheSyncerSkipsSystemMCPServer(t *testing.T) {
	ctx := context.Background()
	registry := toolres.NewMemoryRegistry()
	systemMCP := toolstd.SystemMCPServerDefinition(time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC))
	if err := registry.Register(ctx, systemMCP); err != nil {
		t.Fatalf("register system mcp: %v", err)
	}
	runtime := &fakeMCPRuntime{err: errors.New("should not sync system mcp")}
	syncer := NewMCPToolCacheSyncer(MCPToolCacheSyncerOptions{Registry: registry, Runtime: runtime})

	if err := syncer.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	if runtime.syncToolID != "" {
		t.Fatalf("system mcp should not call runtime sync, got %q", runtime.syncToolID)
	}
	got, err := registry.Get(ctx, toolstd.SystemMCPServer)
	if err != nil {
		t.Fatalf("get system mcp: %v", err)
	}
	if len(got.MCP.ExposedTools) != len(systemMCP.MCP.ExposedTools) {
		t.Fatalf("system mcp cache changed: got=%+v want=%+v", got.MCP.ExposedTools, systemMCP.MCP.ExposedTools)
	}
}

func TestMCPToolCacheSyncerPreservesLockedToolSurface(t *testing.T) {
	ctx := context.Background()
	registry := toolres.NewMemoryRegistry()
	registerMCPDefinitionForSync(t, ctx, registry, "mcp.restricted", true)
	definition, err := registry.Get(ctx, "mcp.restricted")
	if err != nil {
		t.Fatalf("get restricted mcp: %v", err)
	}
	definition.Tags = []string{toolres.TagMCPToolSurfaceLocked}
	if err := registry.UpdateDefinition(ctx, definition); err != nil {
		t.Fatalf("lock restricted mcp: %v", err)
	}
	runtime := &fakeMCPRuntime{tools: []string{"stale", "evaluate_script"}}
	syncer := NewMCPToolCacheSyncer(MCPToolCacheSyncerOptions{Registry: registry, Runtime: runtime})

	if err := syncer.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	got, err := registry.Get(ctx, "mcp.restricted")
	if err != nil {
		t.Fatalf("get locked mcp: %v", err)
	}
	if len(got.MCP.ExposedTools) != 1 || got.MCP.ExposedTools[0] != "stale" || runtime.syncToolID != "" {
		t.Fatalf("locked mcp surface changed: definition=%+v runtime=%+v", got.MCP.ExposedTools, runtime)
	}
}

func TestMCPToolCacheSyncerRotatesBoundedBatches(t *testing.T) {
	ctx := context.Background()
	registry := toolres.NewMemoryRegistry()
	for _, id := range []string{"mcp.fifth", "mcp.first", "mcp.fourth", "mcp.second", "mcp.third"} {
		registerMCPDefinitionForSync(t, ctx, registry, id, true)
	}
	runtime := &recordingMCPRuntime{}
	syncer := NewMCPToolCacheSyncer(MCPToolCacheSyncerOptions{
		Registry:  registry,
		Runtime:   runtime,
		BatchSize: 2,
	})

	for i := 0; i < 3; i++ {
		if err := syncer.Sweep(ctx); err != nil {
			t.Fatalf("Sweep %d: %v", i+1, err)
		}
	}

	want := []string{"mcp.fifth", "mcp.first", "mcp.fourth", "mcp.second", "mcp.third", "mcp.fifth"}
	if len(runtime.ids) != len(want) {
		t.Fatalf("synced ids = %v, want %v", runtime.ids, want)
	}
	for i := range want {
		if runtime.ids[i] != want[i] {
			t.Fatalf("synced ids = %v, want %v", runtime.ids, want)
		}
	}
}

func TestMCPToolCacheSyncerBoundsEachServerProbe(t *testing.T) {
	ctx := context.Background()
	registry := toolres.NewMemoryRegistry()
	registerMCPDefinitionForSync(t, ctx, registry, "mcp.blocked", true)
	syncer := NewMCPToolCacheSyncer(MCPToolCacheSyncerOptions{
		Registry:      registry,
		Runtime:       blockingMCPRuntime{},
		ServerTimeout: 20 * time.Millisecond,
	})

	startedAt := time.Now()
	err := syncer.Sweep(ctx)
	if err == nil || !errors.Is(err, ErrPartialMCPSync) {
		t.Fatalf("Sweep error = %v, want ErrPartialMCPSync", err)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("Sweep elapsed = %v, want bounded server probe", elapsed)
	}
	definition, getErr := registry.Get(ctx, "mcp.blocked")
	if getErr != nil {
		t.Fatalf("get blocked mcp: %v", getErr)
	}
	if definition.MCP.Availability.Status != toolres.MCPAvailabilityUnavailable ||
		definition.MCP.Availability.FailureReason != context.DeadlineExceeded.Error() {
		t.Fatalf("blocked availability = %+v", definition.MCP.Availability)
	}
}

func registerMCPDefinitionForSync(t *testing.T, ctx context.Context, registry toolres.Registry, id string, enabled bool) {
	t.Helper()
	if err := registry.Register(ctx, toolres.ToolDefinition{
		ID:          agentcore.ToolID(id),
		Kind:        toolres.KindMCPServer,
		Name:        id,
		Description: "External MCP",
		Version:     "1.0.0",
		Enabled:     enabled,
		RiskLevel:   toolres.RiskMedium,
		MCP: &toolres.MCPServerDefinition{
			ServerName:   id,
			Transport:    toolres.TransportStdio,
			Command:      "server",
			ExposedTools: []string{"stale"},
			HealthCheck:  toolres.HealthCheckPolicy{Mode: toolres.HealthCheckStartup},
		},
	}); err != nil {
		t.Fatalf("register %s: %v", id, err)
	}
}

type perServerMCPRuntime struct {
	toolsByID map[string][]string
	errByID   map[string]error
}

type recordingMCPRuntime struct {
	ids []string
}

func (r *recordingMCPRuntime) CheckHealth(context.Context, toolres.ToolDefinition) (MCPRuntimeHealth, error) {
	return MCPRuntimeHealth{Status: "available"}, nil
}

func (r *recordingMCPRuntime) ListTools(_ context.Context, definition toolres.ToolDefinition) (MCPRuntimeToolList, error) {
	r.ids = append(r.ids, string(definition.ID))
	return MCPRuntimeToolList{Tools: []string{"list"}}, nil
}

type blockingMCPRuntime struct{}

func (blockingMCPRuntime) CheckHealth(context.Context, toolres.ToolDefinition) (MCPRuntimeHealth, error) {
	return MCPRuntimeHealth{Status: "available"}, nil
}

func (blockingMCPRuntime) ListTools(ctx context.Context, _ toolres.ToolDefinition) (MCPRuntimeToolList, error) {
	<-ctx.Done()
	return MCPRuntimeToolList{Latency: 20 * time.Millisecond}, ctx.Err()
}

func (r *perServerMCPRuntime) CheckHealth(ctx context.Context, definition toolres.ToolDefinition) (MCPRuntimeHealth, error) {
	return MCPRuntimeHealth{Status: "available"}, nil
}

func (r *perServerMCPRuntime) ListTools(ctx context.Context, definition toolres.ToolDefinition) (MCPRuntimeToolList, error) {
	id := string(definition.ID)
	if err := r.errByID[id]; err != nil {
		return MCPRuntimeToolList{}, err
	}
	return MCPRuntimeToolList{Tools: append([]string(nil), r.toolsByID[id]...)}, nil
}
