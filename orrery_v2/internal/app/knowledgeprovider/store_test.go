package knowledgeprovider

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestStoreRoundTripsBindingAndFindsExternalMapping(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "knowledge-provider-store"})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	if err := migration.NewRunner(Migrations()).Up(ctx, database); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	store := NewStore(database)
	now := time.Date(2026, 7, 24, 9, 30, 0, 0, time.UTC)
	binding := Binding{
		ID:            "kprovider-pageindex",
		Name:          "Minvia PageIndex",
		MCPServerID:   "mcp.pageindex",
		Mode:          ModeReadWrite,
		Status:        StatusActive,
		ToolMapping:   DefaultToolMapping(),
		FolderInclude: []string{"minvia-core"},
		FolderExclude: []string{"MinviaBrain"},
		CreatedAt:     now,
		UpdatedAt:     now,
		CreatedBy:     "superadmin",
		UpdatedBy:     "superadmin",
	}
	if err := store.SaveBinding(ctx, binding); err != nil {
		t.Fatalf("save binding: %v", err)
	}
	got, err := store.GetBinding(ctx, binding.ID)
	if err != nil {
		t.Fatalf("get binding: %v", err)
	}
	if got.MCPServerID != binding.MCPServerID || got.ToolMapping[CapabilityListFolders] != "list_folders" {
		t.Fatalf("binding mismatch: %+v", got)
	}

	mapping := ExternalMapping{BindingID: binding.ID, Kind: ResourceFolder, ExternalID: "folder-1", LocalID: "kb-1", UpdatedAt: now}
	if err := store.SaveMapping(ctx, mapping); err != nil {
		t.Fatalf("save mapping: %v", err)
	}
	found, err := store.FindMapping(ctx, binding.ID, ResourceFolder, "folder-1")
	if err != nil {
		t.Fatalf("find mapping: %v", err)
	}
	if found.LocalID != "kb-1" {
		t.Fatalf("mapping mismatch: %+v", found)
	}
}

func TestStoreUpsertsExternalMappingByExternalIdentity(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "knowledge-provider-mapping-upsert"})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	if err := migration.NewRunner(Migrations()).Up(ctx, database); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	store := NewStore(database)

	first := ExternalMapping{BindingID: "binding-1", Kind: ResourceDocument, ExternalID: "doc/unsafe id", LocalID: "doc-1"}
	second := first
	second.LocalID = "doc-2"
	if err := store.SaveMapping(ctx, first); err != nil {
		t.Fatalf("save first mapping: %v", err)
	}
	if err := store.SaveMapping(ctx, second); err != nil {
		t.Fatalf("save second mapping: %v", err)
	}

	found, err := store.FindMapping(ctx, second.BindingID, second.Kind, second.ExternalID)
	if err != nil {
		t.Fatalf("find mapping: %v", err)
	}
	if found.LocalID != "doc-2" {
		t.Fatalf("expected updated local id, got %+v", found)
	}
	var count int
	if err := database.QueryRow(ctx, `SELECT COUNT(*) FROM knowledge_external_mappings`).Scan(&count); err != nil {
		t.Fatalf("count mappings: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one mapping row, got %d", count)
	}
}
