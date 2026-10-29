package rebac

import (
	"context"
	"strings"
	"testing"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestRelationTupleMigrationUsesPostgresJSONBAndIndexes(t *testing.T) {
	migration := relationTupleMigration()
	postgresStatements := strings.Join(migration.StatementsByDialect[db.DialectPostgres], "\n")
	for _, want := range []string{
		"rebac_relation_tuples",
		"subject_ref_json JSONB",
		"object_ref_json JSONB",
		"USING GIN (subject_ref_json)",
		"idx_rebac_relation_tenant_object",
	} {
		if !strings.Contains(postgresStatements, want) {
			t.Fatalf("postgres migration missing %q:\n%s", want, postgresStatements)
		}
	}
}

func TestEffectiveAuthorizationRevisionMigrationTracksLocalAuthorizationInputs(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "rebac-effective-authorization-revision"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner([]migration.Migration{authorizationRevisionMigration(), effectiveAuthorizationRevisionMigration(), idempotentAuthorizationRevisionMigration()}).Up(ctx, store); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	put := func(table, key string) {
		t.Helper()
		if err := store.PutRecord(ctx, table, key, db.Record{"id": key}); err != nil {
			t.Fatalf("put %s: %v", table, err)
		}
	}
	revision := func() int64 {
		t.Helper()
		var value int64
		if err := store.QueryRow(ctx, `SELECT revision FROM rebac_authorization_revision WHERE id = 'global'`).Scan(&value); err != nil {
			t.Fatalf("read revision: %v", err)
		}
		return value
	}

	put("core.operations_role_assignments", "assignment-1")
	if got := revision(); got != 1 {
		t.Fatalf("revision after role assignment = %d, want 1", got)
	}
	put("core.operations_role_assignments", "assignment-1")
	if got := revision(); got != 1 {
		t.Fatalf("revision after unchanged role assignment = %d, want 1", got)
	}
	if err := store.PutRecord(ctx, "core.operations_role_assignments", "assignment-1", db.Record{"id": "assignment-1", "role": "operator"}); err != nil {
		t.Fatalf("update role assignment: %v", err)
	}
	if got := revision(); got != 2 {
		t.Fatalf("revision after changed role assignment = %d, want 2", got)
	}
	put("agent_profiles", "profile-1")
	if got := revision(); got != 3 {
		t.Fatalf("revision after profile = %d, want 3", got)
	}
	put("unrelated.records", "record-1")
	if got := revision(); got != 3 {
		t.Fatalf("revision after unrelated record = %d, want 3", got)
	}
	if err := store.DeleteRecord(ctx, "core.operations_role_assignments", "assignment-1"); err != nil {
		t.Fatalf("delete role assignment: %v", err)
	}
	if got := revision(); got != 4 {
		t.Fatalf("revision after role assignment delete = %d, want 4", got)
	}
}

func TestIdempotentAuthorizationRevisionMigrationReplacesPostgresTriggerFunction(t *testing.T) {
	migration := idempotentAuthorizationRevisionMigration()
	if migration.Version != "0177_rebac_idempotent_authorization_revision" {
		t.Fatalf("migration version = %q", migration.Version)
	}
	statements := strings.Join(migration.StatementsByDialect[db.DialectPostgres], "\n")
	for _, want := range []string{"CREATE OR REPLACE FUNCTION", "IS DISTINCT FROM", "NEW.payload_json", "OLD.payload_json"} {
		if !strings.Contains(statements, want) {
			t.Fatalf("postgres migration missing %q:\n%s", want, statements)
		}
	}
}

func TestAuthorizationRevisionMigrationUsesMonotonicState(t *testing.T) {
	migration := authorizationRevisionMigration()
	if migration.Version != "0164_rebac_authorization_revision" {
		t.Fatalf("revision migration version = %q", migration.Version)
	}
	statements := strings.Join(migration.StatementsByDialect[db.DialectPostgres], "\n")
	if !strings.Contains(statements, "rebac_authorization_revision") || !strings.Contains(statements, "revision BIGINT NOT NULL") {
		t.Fatalf("revision migration statements = %s", statements)
	}
}
