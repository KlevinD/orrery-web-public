package identity

import (
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func RegisterMigrations(coordinator *migration.Coordinator) error {
	if coordinator == nil {
		return nil
	}
	return coordinator.RegisterInitializer(db.RoleCore, migration.Migration{
		Version:     "0191_device_delivery_identity",
		Description: "persist device PKCE grants, rotating credentials, registrations, and connection challenges",
		Statements:  schemaStatements(db.DialectSQLite),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: schemaStatements(db.DialectPostgres),
		},
	})
}
