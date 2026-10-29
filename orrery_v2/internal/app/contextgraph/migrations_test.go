package contextgraph_test

import (
	"context"
	"testing"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestRegisterMigrationsCreatesContextGraphTables(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "contextgraph-migrations"},
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

	if err := contextgraph.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("RegisterMigrations returned error: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatalf("migration up: %v", err)
	}

	for _, table := range []string{
		"context_graph_objects",
		"context_graph_links",
		"context_graph_facts",
		"context_graph_evidence",
		"context_graph_projection_audits",
		"context_graph_projection_audit_commands",
		"context_graph_projection_audit_command_claims",
		"context_graph_access_ref_backfill_audit",
		"context_graph_resource_versions",
	} {
		assertContextGraphTableExists(t, core, table)
	}
	for _, index := range []string{
		"idx_context_graph_objects_tenant_type",
		"idx_context_graph_links_tenant_from",
		"idx_context_graph_links_tenant_to",
		"idx_context_graph_facts_tenant_subject",
		"idx_context_graph_evidence_tenant_source",
		"idx_context_graph_projection_audits_event",
		"idx_context_graph_projection_audits_status",
		"idx_context_graph_objects_access",
		"idx_context_graph_links_access",
		"idx_context_graph_facts_access",
		"idx_context_graph_evidence_access",
		"idx_context_graph_projection_audits_access",
		"idx_context_graph_objects_access_backfill",
		"idx_context_graph_links_access_backfill",
		"idx_context_graph_facts_access_backfill",
		"idx_context_graph_assertions_access_backfill",
		"idx_context_graph_evidence_access_backfill",
		"idx_context_graph_projection_audits_access_backfill",
		"idx_context_graph_projection_replay_access_backfill",
		"idx_context_graph_access_ref_backfill_audit_resource",
		"idx_context_graph_objects_workbench_seek",
		"idx_context_graph_links_from_workbench_seek",
		"idx_context_graph_links_to_workbench_seek",
		"idx_context_graph_facts_workbench_seek",
		"idx_context_graph_evidence_workbench_seek",
		"idx_context_graph_assertions_workbench_seek",
		"idx_context_graph_projection_audits_workbench_seek",
		"idx_context_graph_projection_replay_workbench_seek",
		"idx_context_graph_facts_lineage_seek",
		"idx_context_graph_projection_audits_lineage_seek",
		"idx_context_graph_projection_audit_commands_audit",
		"idx_context_graph_projection_audit_claims_lease",
		"idx_context_graph_resource_versions_as_of",
		"idx_context_graph_resource_versions_known_at",
	} {
		assertContextGraphIndexExists(t, core, index)
	}
	if _, err := core.Exec(ctx, `
INSERT INTO context_graph_objects (
	id, tenant_id, object_type, canonical_key, label, properties_json,
	sensitivity, confidence, status, provenance_json, evidence_refs_json, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"obj-1", "tenant-1", "project", "project:1", "Project 1", `{}`, "internal", 0.9, "active", `{}`, `[]`, int64(1),
	); err != nil {
		t.Fatalf("insert object: %v", err)
	}
	if _, err := core.Exec(ctx, `
INSERT INTO context_graph_links (
	id, tenant_id, link_type, from_object_id, to_object_id, properties_json,
	confidence, status, provenance_json, evidence_refs_json, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"link-1", "tenant-1", "depends_on", "obj-1", "obj-1", `{}`, 0.8, "active", `{}`, `[]`, int64(2),
	); err != nil {
		t.Fatalf("insert link: %v", err)
	}
	if _, err := core.Exec(ctx, `
INSERT INTO context_graph_facts (
	id, tenant_id, subject_kind, subject_id, predicate, value_json, properties_json,
	confidence, status, provenance_json, evidence_refs_json, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"fact-1", "tenant-1", "object", "obj-1", "status", `"active"`, `{}`, 0.7, "active", `{}`, `[]`, int64(3),
	); err != nil {
		t.Fatalf("insert fact: %v", err)
	}
	if _, err := core.Exec(ctx, `
INSERT INTO context_graph_evidence (
	id, tenant_id, source_kind, source_id, summary, confidence, observed_at
) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"ev-1", "tenant-1", "document", "doc-1", "evidence", 0.6, int64(4),
	); err != nil {
		t.Fatalf("insert evidence: %v", err)
	}
	if _, err := core.Exec(ctx, `
	INSERT INTO context_graph_projection_audits (
		id, tenant_id, source_event_id, source_event_type, status, reason_code, reason, details_json, observed_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"audit-1", "tenant-1", "event-1", "memory.scope.linked", "skipped", "missing_scope_target", "missing target", `{}`, int64(5),
	); err != nil {
		t.Fatalf("insert projection audit: %v", err)
	}
	for _, check := range []struct {
		table string
		id    string
	}{
		{table: "context_graph_objects", id: "obj-1"},
		{table: "context_graph_links", id: "link-1"},
		{table: "context_graph_facts", id: "fact-1"},
		{table: "context_graph_evidence", id: "ev-1"},
		{table: "context_graph_projection_audits", id: "audit-1"},
	} {
		var state string
		if err := core.QueryRow(ctx, `SELECT access_state FROM `+check.table+` WHERE tenant_id = ? AND id = ?`, "tenant-1", check.id).Scan(&state); err != nil {
			t.Fatalf("query %s access state: %v", check.table, err)
		}
		if state != "unresolved" {
			t.Fatalf("%s access state = %q", check.table, state)
		}
	}
	var revision int64
	if err := core.QueryRow(ctx, `SELECT revision FROM context_graph_projection_audits WHERE tenant_id = ? AND id = ?`, "tenant-1", "audit-1").Scan(&revision); err != nil || revision != 1 {
		t.Fatalf("projection audit revision=%d err=%v", revision, err)
	}
}

func assertContextGraphTableExists(t *testing.T, store *db.Store, table string) {
	t.Helper()
	exists, err := store.TableExists(context.Background(), table)
	if err != nil {
		t.Fatalf("TableExists(%s): %v", table, err)
	}
	if !exists {
		t.Fatalf("missing table %s", table)
	}
}

func assertContextGraphIndexExists(t *testing.T, store *db.Store, index string) {
	t.Helper()
	var name string
	err := store.QueryRow(context.Background(), `SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&name)
	if err != nil {
		t.Fatalf("missing index %s: %v", index, err)
	}
}
