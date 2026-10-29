package datapolicy

import (
	"strings"
	"testing"

	"orrery_v2/internal/platform/db"
)

func TestDataPolicyMigrationCreatesFieldAndPurposePolicyTables(t *testing.T) {
	migration := dataPolicyMigration()
	postgresStatements := strings.Join(migration.StatementsByDialect[db.DialectPostgres], "\n")
	for _, want := range []string{
		"field_policies",
		"purpose_policies",
		"idx_field_policies_tenant_scope",
		"idx_purpose_policies_tenant_scope",
		"BIGINT",
	} {
		if !strings.Contains(postgresStatements, want) {
			t.Fatalf("postgres migration missing %q:\n%s", want, postgresStatements)
		}
	}
}

func TestDataPolicyRevisionMigrationUsesMonotonicState(t *testing.T) {
	migration := dataPolicyRevisionMigration()
	if migration.Version != "0165_data_policy_revision" {
		t.Fatalf("revision migration version = %q", migration.Version)
	}
	statements := strings.Join(migration.StatementsByDialect[db.DialectPostgres], "\n")
	if !strings.Contains(statements, "data_policy_revision") || !strings.Contains(statements, "revision BIGINT NOT NULL") {
		t.Fatalf("revision migration statements = %s", statements)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
