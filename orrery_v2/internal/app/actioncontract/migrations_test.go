package actioncontract_test

import (
	"context"
	"testing"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestRegisterMigrationsCreatesActionContractTables(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "actioncontract-migrations"},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	defer databases.Close()
	core, err := databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core database: %v", err)
	}
	coordinator := migration.NewCoordinator(databases)

	if err := actioncontract.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("RegisterMigrations returned error: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatalf("migration up: %v", err)
	}

	for _, table := range []string{"action_types", "action_runs", "action_writeback_receipts"} {
		assertActionContractTableExists(t, core, table)
	}
	for _, index := range []string{
		"idx_action_types_tenant_category",
		"idx_action_runs_tenant_action_type",
		"idx_action_runs_tenant_status",
		"idx_action_writebacks_run",
		"idx_action_types_schema_seek",
	} {
		assertActionContractIndexExists(t, core, index)
	}
	if _, err := core.Exec(ctx, `
INSERT INTO action_types (
	id, tenant_id, name, category, provider_kind, input_schema_json, output_schema_json,
	risk_level, enabled, version, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"act-1", "tenant-1", "Call MCP Tool", "tool", "mcp_tool", `{}`, `{}`, "medium", true, "v1", int64(1), int64(1),
	); err != nil {
		t.Fatalf("insert action type: %v", err)
	}
	var semantics string
	if err := core.QueryRow(ctx, `SELECT semantics_json FROM action_types WHERE id = ?`, "act-1").Scan(&semantics); err != nil || semantics != "{}" {
		t.Fatalf("read action semantics: value=%q err=%v", semantics, err)
	}
	if _, err := core.Exec(ctx, `
INSERT INTO action_runs (
	id, tenant_id, workspace_id, action_type_id, action_category, actor_kind, actor_id,
	effective_user_kind, effective_user_id, runtime_kind, runtime_id, object_refs_json,
	requested_fields_json, args_hash, risk_level, status, evidence_refs_json, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"run-1", "tenant-1", "workspace-1", "act-1", "tool", "agent", "agent-1",
		"human", "human-1", "mcp_gateway", "gateway", `[]`, `[]`, "sha256:abc", "medium", "started", `[]`, int64(2),
	); err != nil {
		t.Fatalf("insert action run: %v", err)
	}
	var executionMode string
	if err := core.QueryRow(ctx, `SELECT execution_mode FROM action_runs WHERE id = ?`, "run-1").Scan(&executionMode); err != nil {
		t.Fatalf("read action run execution mode: %v", err)
	}
	if executionMode != "apply" {
		t.Fatalf("legacy action run execution mode = %q, want apply", executionMode)
	}
	if _, err := core.Exec(ctx, `
INSERT INTO action_writeback_receipts (
	id, tenant_id, action_run_id, target_system, target_resource_refs_json,
	changed_fields_json, status, evidence_refs_json, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"receipt-1", "tenant-1", "run-1", "mcp", `[]`, `[]`, "succeeded", `[]`, int64(3),
	); err != nil {
		t.Fatalf("insert writeback receipt: %v", err)
	}
}

func assertActionContractTableExists(t *testing.T, store *db.Store, table string) {
	t.Helper()
	exists, err := store.TableExists(context.Background(), table)
	if err != nil {
		t.Fatalf("TableExists(%s): %v", table, err)
	}
	if !exists {
		t.Fatalf("missing table %s", table)
	}
}

func assertActionContractIndexExists(t *testing.T, store *db.Store, index string) {
	t.Helper()
	var name string
	err := store.QueryRow(context.Background(), `SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&name)
	if err != nil {
		t.Fatalf("missing index %s: %v", index, err)
	}
}
