package peopledirectory

import (
	"context"
	"strings"
	"testing"

	"orrery_v2/internal/platform/migration"
	"orrery_v2/internal/platform/testkit"
)

func TestDirectoryMigrationCreatesSnapshotStagingAndReviewTablesWithoutCredentials(t *testing.T) {
	ctx := context.Background()
	database := testkit.NewTestDB(t)
	if err := migration.NewRunner([]migration.Migration{directoryMigration()}).Up(ctx, database); err != nil {
		t.Fatalf("apply directory migration: %v", err)
	}
	for _, table := range []string{directorySnapshotTable, directoryStagedUserTable, directoryMatchTable} {
		exists, err := database.TableExists(ctx, table)
		if err != nil {
			t.Fatalf("TableExists(%s): %v", table, err)
		}
		if !exists {
			t.Fatalf("table %s was not created", table)
		}
	}
	statements := strings.ToLower(strings.Join(directoryMigration().Statements, "\n"))
	for _, forbidden := range []string{"access_token", "refresh_token", "app_secret", "password"} {
		if strings.Contains(statements, forbidden) {
			t.Fatalf("directory migration persists forbidden credential %q:\n%s", forbidden, statements)
		}
	}
}
