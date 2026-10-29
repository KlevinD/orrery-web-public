package contextgraph

import (
	"context"
	"strings"
	"testing"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestAccessRefBoundsMigrationConcealsOversizedResolvedRows(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "contextgraph-access-ref-bounds"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner([]migration.Migration{
		contextGraphMigration(), contextGraphProjectionAuditMigration(), contextGraphAccessRefMigration(),
		contextGraphAuditAccessRefMigration(), contextGraphTemporalHistoryMigration(),
	}).Up(ctx, store); err != nil {
		t.Fatalf("base migration up: %v", err)
	}

	graph := NewPlatformStore(store)
	for _, id := range []string{"valid", "oversized"} {
		if err := graph.SaveObject(ctx, Object{
			ID: id, TenantID: "tenant-1", ObjectType: "memory_record", CanonicalKey: "memory_record:" + id,
			OwnerAccessRef: AccessRef{Kind: "memory_record", ID: id, TenantID: "tenant-1"},
		}); err != nil {
			t.Fatalf("save %s object: %v", id, err)
		}
	}
	oversizedID := strings.Repeat("x", MaxAccessRefIDBytes+1)
	if _, err := store.Exec(ctx, `UPDATE context_graph_objects SET owner_access_id = ?, access_state = 'resolved' WHERE tenant_id = ? AND id = ?`, oversizedID, "tenant-1", "oversized"); err != nil {
		t.Fatalf("seed oversized resolved row: %v", err)
	}
	if err := migration.NewRunner([]migration.Migration{contextGraphAccessRefBoundsMigration()}).Up(ctx, store); err != nil {
		t.Fatalf("bounds migration up: %v", err)
	}

	for id, want := range map[string]AccessState{"valid": AccessStateResolved, "oversized": AccessStateUnresolved} {
		var state AccessState
		if err := store.QueryRow(ctx, `SELECT access_state FROM context_graph_objects WHERE tenant_id = ? AND id = ?`, "tenant-1", id).Scan(&state); err != nil {
			t.Fatalf("read %s access state: %v", id, err)
		}
		if state != want {
			t.Fatalf("%s access state = %q, want %q", id, state, want)
		}
	}
}

func TestMigrationPreservesExistingRowsWithUnresolvedAccessRefs(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "contextgraph-access-migration-preserve"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner([]migration.Migration{
		contextGraphMigration(),
		contextGraphProjectionAuditMigration(),
	}).Up(ctx, store); err != nil {
		t.Fatalf("base migration up: %v", err)
	}
	if _, err := store.Exec(ctx, `
INSERT INTO context_graph_objects (
	id, tenant_id, object_type, canonical_key, label, properties_json,
	sensitivity, confidence, status, provenance_json, evidence_refs_json, source_stream_id, source_version, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"object-1", "tenant-1", "task", "task:1", "Preserved", `{}`, "internal", 0.8, "active", `{}`, `[]`, "task:1", int64(1), int64(10),
	); err != nil {
		t.Fatalf("insert legacy object: %v", err)
	}
	if _, err := store.Exec(ctx, `
INSERT INTO context_graph_projection_audits (
	id, tenant_id, source_event_id, source_event_type, status, reason_code, reason, details_json, observed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"audit-1", "tenant-1", "event-1", "task.updated", "skipped", "missing_target", "Preserved reason", `{}`, int64(11),
	); err != nil {
		t.Fatalf("insert legacy audit: %v", err)
	}
	if err := migration.NewRunner([]migration.Migration{
		contextGraphAccessRefMigration(),
		contextGraphAuditAccessRefMigration(),
	}).Up(ctx, store); err != nil {
		t.Fatalf("access migration up: %v", err)
	}

	var label, ownerKind, objectState string
	if err := store.QueryRow(ctx, `
SELECT label, owner_access_kind, access_state
FROM context_graph_objects
WHERE tenant_id = ? AND id = ?`, "tenant-1", "object-1").Scan(&label, &ownerKind, &objectState); err != nil {
		t.Fatalf("read migrated object: %v", err)
	}
	if label != "Preserved" || ownerKind != "" || objectState != string(AccessStateUnresolved) {
		t.Fatalf("migrated object label=%q owner=%q state=%q", label, ownerKind, objectState)
	}

	var reason, sourceKind, auditState string
	if err := store.QueryRow(ctx, `
SELECT reason, source_access_kind, access_state
FROM context_graph_projection_audits
WHERE tenant_id = ? AND id = ?`, "tenant-1", "audit-1").Scan(&reason, &sourceKind, &auditState); err != nil {
		t.Fatalf("read migrated audit: %v", err)
	}
	if reason != "Preserved reason" || sourceKind != "" || auditState != string(AccessStateUnresolved) {
		t.Fatalf("migrated audit reason=%q source=%q state=%q", reason, sourceKind, auditState)
	}
}
