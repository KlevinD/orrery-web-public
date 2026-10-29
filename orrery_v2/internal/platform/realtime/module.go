package realtime

import (
	"fmt"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

type MigrationRegistrar interface {
	RegisterMigration(role string, migration migration.Migration) error
}

func RegisterMigrations(registrar MigrationRegistrar) error {
	if registrar == nil {
		return fmt.Errorf("realtime migration registrar is required")
	}
	migrationDef := OutboxMigration()
	return registrar.RegisterMigration(db.RoleJobs, migration.Migration{
		Version:             migrationDef.Version,
		Description:         migrationDef.Description,
		Statements:          append([]string(nil), migrationDef.Statements...),
		StatementsByDialect: cloneStatementsByDialect(migrationDef.StatementsByDialect),
	})
}

func cloneStatementsByDialect(src map[db.Dialect][]string) map[db.Dialect][]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[db.Dialect][]string, len(src))
	for dialect, statements := range src {
		dst[dialect] = append([]string(nil), statements...)
	}
	return dst
}
