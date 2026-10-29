package peopleaccount

import (
	"context"
	"strings"
	"testing"

	"orrery_v2/internal/platform/migration"
	"orrery_v2/internal/platform/testkit"
)

func TestProjectionMigrationCreatesStateAndOutboxConstraints(t *testing.T) {
	ctx := context.Background()
	store := testkit.NewTestDB(t)
	if err := migration.NewRunner([]migration.Migration{projectionMigration()}).Up(ctx, store); err != nil {
		t.Fatalf("apply projection migration: %v", err)
	}
	for _, table := range []string{projectionStateTable, projectionOutboxTable} {
		exists, err := store.TableExists(ctx, table)
		if err != nil {
			t.Fatalf("TableExists(%s): %v", table, err)
		}
		if !exists {
			t.Fatalf("table %s was not created", table)
		}
	}
	statements := strings.Join(projectionMigration().Statements, "\n")
	if !strings.Contains(statements, "UNIQUE (person_id, revision)") {
		t.Fatalf("projection migration lacks person revision uniqueness:\n%s", statements)
	}
}
