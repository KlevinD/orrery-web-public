package realtime

import (
	"strings"
	"testing"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestRegisterMigrationsRegistersRealtimeOutboxInJobsRole(t *testing.T) {
	registrar := &capturingMigrationRegistrar{}

	if err := RegisterMigrations(registrar); err != nil {
		t.Fatalf("RegisterMigrations returned error: %v", err)
	}

	if registrar.role != db.RoleJobs {
		t.Fatalf("role = %q, want jobs role", registrar.role)
	}
	if registrar.migration.Version != "030_realtime_outbox" {
		t.Fatalf("migration version = %q, want realtime outbox migration", registrar.migration.Version)
	}
	postgresStatements := registrar.migration.StatementsByDialect[db.DialectPostgres]
	if len(postgresStatements) == 0 {
		t.Fatalf("postgres statements were not registered")
	}
	if strings.Contains(strings.Join(postgresStatements, "\n"), "payload_json BLOB") {
		t.Fatalf("postgres statements still contain BLOB: %#v", postgresStatements)
	}
	if !strings.Contains(strings.Join(postgresStatements, "\n"), "created_at BIGINT") {
		t.Fatalf("postgres statements missing BIGINT timestamp columns: %#v", postgresStatements)
	}
}

type capturingMigrationRegistrar struct {
	role      string
	migration migration.Migration
}

func (r *capturingMigrationRegistrar) RegisterMigration(role string, migration migration.Migration) error {
	r.role = role
	r.migration = migration
	return nil
}
