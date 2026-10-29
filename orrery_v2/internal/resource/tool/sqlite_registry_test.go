package tool

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/db"
)

func TestPlatformRegistryPersistsDefinitions(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "tool-registry-test"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	registry := NewPlatformRegistry(store)
	definition := ToolDefinition{
		ID:        agentcore.ToolID("tool.persisted.mcp"),
		Kind:      KindMCPServer,
		Name:      "Persisted MCP",
		Version:   "3.0.0",
		Enabled:   true,
		RiskLevel: RiskMedium,
		MCP: &MCPServerDefinition{
			ServerName: "persisted",
			Transport:  TransportStdio,
			Command:    "persisted-mcp",
			EnvSecretRefs: []SecretRef{
				{Name: "persisted-token", Env: "PERSISTED_TOKEN"},
			},
		},
		Tags: []string{"seed", "mcp"},
	}
	if err := registry.Register(ctx, definition); err != nil {
		t.Fatalf("register: %v", err)
	}

	reopened := NewPlatformRegistry(store)
	got, err := reopened.Get(ctx, definition.ID)
	if err != nil {
		t.Fatalf("get persisted definition: %v", err)
	}
	if got.ID != definition.ID || got.MCP == nil || got.MCP.EnvSecretRefs[0].Name != "persisted-token" {
		t.Fatalf("definition not persisted faithfully: %+v", got)
	}
	if err := reopened.Disable(ctx, definition.ID); err != nil {
		t.Fatalf("disable: %v", err)
	}
	disabled, err := registry.Get(ctx, definition.ID)
	if err != nil {
		t.Fatalf("get disabled: %v", err)
	}
	if disabled.Enabled {
		t.Fatalf("disabled definition still enabled: %+v", disabled)
	}

	if err := reopened.Delete(ctx, definition.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := registry.Get(ctx, definition.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted definition error = %v, want ErrNotFound", err)
	}
	deleted, err := registry.IsDeleted(ctx, definition.ID)
	if err != nil {
		t.Fatalf("is deleted: %v", err)
	}
	if !deleted {
		t.Fatalf("deleted definition should keep registry tombstone")
	}
	if listed, err := registry.List(ctx); err != nil {
		t.Fatalf("list after delete: %v", err)
	} else if len(listed) != 0 {
		t.Fatalf("deleted definition should be hidden from list: %+v", listed)
	}
	if err := registry.Register(ctx, definition); err != nil {
		t.Fatalf("explicit re-register after delete: %v", err)
	}
	deleted, err = registry.IsDeleted(ctx, definition.ID)
	if err != nil {
		t.Fatalf("is deleted after re-register: %v", err)
	}
	if deleted {
		t.Fatalf("explicit re-register should clear registry tombstone")
	}
}

func TestPlatformRegistryGetsDefinitionsInBatches(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "tool-registry-batch"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	registry := NewPlatformRegistry(store)
	for _, id := range []agentcore.ToolID{"tool-a", "tool-b"} {
		if err := registry.Register(ctx, ToolDefinition{ID: id, Kind: KindAPITool, Name: string(id), Version: "1"}); err != nil {
			t.Fatalf("Register(%s): %v", id, err)
		}
	}
	definitions, err := registry.GetMany(ctx, []agentcore.ToolID{"tool-b", "missing", "tool-a", "tool-b"})
	if err != nil {
		t.Fatalf("GetMany: %v", err)
	}
	if len(definitions) != 2 || definitions["tool-a"].ID != "tool-a" || definitions["tool-b"].ID != "tool-b" {
		t.Fatalf("definitions = %+v", definitions)
	}
}

func TestPlatformRegistryListsLightweightCatalogEntries(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "tool-registry-lightweight-catalog"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	registry := NewPlatformRegistry(store)
	for index, id := range []agentcore.ToolID{"tool-a", "tool-b", "tool-c"} {
		if err := registry.Register(ctx, ToolDefinition{ID: id, Kind: KindAPITool, Name: "Name " + string(id), Version: "1", UpdatedAt: time.Unix(int64(index+1), 0).UTC()}); err != nil {
			t.Fatalf("Register(%s): %v", id, err)
		}
	}
	if err := registry.Delete(ctx, "tool-b"); err != nil {
		t.Fatalf("Delete(tool-b): %v", err)
	}

	first, err := registry.ListCatalogEntries(ctx, "", 2)
	if err != nil {
		t.Fatalf("ListCatalogEntries first page: %v", err)
	}
	if !first.HasMore || first.Scanned != 3 || first.NextAfterID != "tool-b" || len(first.Entries) != 1 || first.Entries[0].ID != "tool-a" || first.Entries[0].Name != "Name tool-a" || !first.Entries[0].UpdatedAt.Equal(time.Unix(1, 0).UTC()) {
		t.Fatalf("first page = %+v", first)
	}
	second, err := registry.ListCatalogEntries(ctx, first.NextAfterID, 2)
	if err != nil {
		t.Fatalf("ListCatalogEntries second page: %v", err)
	}
	if second.HasMore || second.Scanned != 1 || second.NextAfterID != "tool-c" || len(second.Entries) != 1 || second.Entries[0].ID != "tool-c" {
		t.Fatalf("second page = %+v", second)
	}
}

func TestPlatformRegistryUpdatesToolDefinitions(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "tool-registry-update-test"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	registry := NewPlatformRegistry(store)
	definition := ToolDefinition{
		ID:      agentcore.ToolID("tool.update.mcp"),
		Kind:    KindMCPServer,
		Name:    "Update MCP",
		Version: "1.0.0",
		Enabled: true,
		MCP: &MCPServerDefinition{
			ServerName:   "update",
			Transport:    TransportStdio,
			Command:      "update-mcp",
			ExposedTools: []string{"read"},
		},
		Tags: []string{"old"},
	}
	if err := registry.Register(ctx, definition); err != nil {
		t.Fatalf("register: %v", err)
	}

	updated := definition.Clone()
	updated.Name = "Updated MCP"
	updated.Version = "2.0.0"
	updated.Tags = []string{"current"}
	updated.MCP.ExposedTools = []string{"read", "write"}
	if err := registry.UpdateDefinition(ctx, updated); err != nil {
		t.Fatalf("update: %v", err)
	}

	reopened := NewPlatformRegistry(store)
	got, err := reopened.Get(ctx, definition.ID)
	if err != nil {
		t.Fatalf("get updated: %v", err)
	}
	if got.Name != "Updated MCP" || got.Version != "2.0.0" || len(got.MCP.ExposedTools) != 2 {
		t.Fatalf("updated definition not persisted: %+v", got)
	}
	oldTag, err := reopened.ListByTag(ctx, "old")
	if err != nil {
		t.Fatalf("list old tag: %v", err)
	}
	if len(oldTag) != 0 {
		t.Fatalf("old tag should not retain updated tool: %+v", oldTag)
	}
	currentTag, err := reopened.ListByTag(ctx, "current")
	if err != nil {
		t.Fatalf("list current tag: %v", err)
	}
	if len(currentTag) != 1 || currentTag[0].ID != definition.ID {
		t.Fatalf("current tag should include updated tool: %+v", currentTag)
	}
}
