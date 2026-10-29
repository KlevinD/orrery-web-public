package directory

import (
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func RegisterMigrations(coordinator *migration.Coordinator) error {
	if coordinator == nil {
		return nil
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, migration.Migration{
		Version:     "0193_owned_agent_directory",
		Description: "persist user-owned agent descriptors and personal assistant hosting selection",
		Statements:  schemaStatements(db.DialectSQLite),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: schemaStatements(db.DialectPostgres),
		},
	}); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, migration.Migration{
		Version:     "0194_personal_assistant_avatar",
		Description: "persist user-scoped personal assistant avatars independently from agent profiles",
		Statements:  avatarSchemaStatements(db.DialectSQLite),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: avatarSchemaStatements(db.DialectPostgres),
		},
	}); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, migration.Migration{
		Version:     "0197_personal_assistant_assigned_agent",
		Description: "persist the organization agent assigned to each personal assistant",
		Statements: []string{
			`ALTER TABLE personal_assistant_hosting ADD COLUMN assigned_agent_id TEXT NOT NULL DEFAULT ''`,
		},
	}); err != nil {
		return err
	}
	return coordinator.RegisterInitializer(db.RoleCore, migration.Migration{
		Version:     "0205_personal_assistant_notifications",
		Description: "persist owner-scoped personal assistant approval notification preferences",
		Statements:  personalAssistantNotificationSchemaStatements(db.DialectSQLite),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: personalAssistantNotificationSchemaStatements(db.DialectPostgres),
		},
	})
}

func avatarSchemaStatements(dialect db.Dialect) []string {
	statements := schemaStatements(dialect)
	return append([]string(nil), statements[len(statements)-1:]...)
}
