package identity

import (
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func RegisterMigrations(coordinator *migration.Coordinator) error {
	if coordinator == nil {
		return nil
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, identitySubjectsMigration()); err != nil {
		return err
	}
	return coordinator.RegisterInitializer(db.RoleCore, retiredPMIdentityMigration())
}

func identitySubjectsMigration() migration.Migration {
	return migration.Migration{
		Version:     "0131_identity_subjects",
		Description: "create identity subject mapping table",
		Statements: []string{
			identitySubjectsTableSQL("INTEGER"),
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_identity_subjects_provider_subject ON identity_subjects (provider, subject)`,
			`CREATE INDEX IF NOT EXISTS idx_identity_subjects_agent_profile ON identity_subjects (provider, agent_profile_id)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_identity_subjects_local_username ON identity_subjects (provider, username) WHERE provider = 'local' AND username <> ''`,
		},
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: {
				identitySubjectsTableSQL("BIGINT"),
				`CREATE UNIQUE INDEX IF NOT EXISTS idx_identity_subjects_provider_subject ON identity_subjects (provider, subject)`,
				`CREATE INDEX IF NOT EXISTS idx_identity_subjects_agent_profile ON identity_subjects (provider, agent_profile_id)`,
				`CREATE UNIQUE INDEX IF NOT EXISTS idx_identity_subjects_local_username ON identity_subjects (provider, username) WHERE provider = 'local' AND username <> ''`,
			},
		},
	}
}

func identitySubjectsTableSQL(timeType string) string {
	return `CREATE TABLE IF NOT EXISTS identity_subjects (
		id TEXT PRIMARY KEY,
		provider TEXT NOT NULL,
		subject TEXT NOT NULL,
		user_id TEXT NOT NULL DEFAULT '',
		username TEXT NOT NULL DEFAULT '',
		email TEXT NOT NULL DEFAULT '',
		display_name TEXT NOT NULL DEFAULT '',
		agent_profile_id TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'active',
		last_login_at ` + timeType + ` NOT NULL DEFAULT 0,
		created_at ` + timeType + ` NOT NULL,
		updated_at ` + timeType + ` NOT NULL
	)`
}

func retiredPMIdentityMigration() migration.Migration {
	retiredSubjectWhere := `provider = 'kratos' AND username = 'pm' AND agent_profile_id = 'human-pm'`
	retiredSubjectIDs := `SELECT id FROM identity_subjects WHERE ` + retiredSubjectWhere
	retiredSessionsWhere := `username = 'pm' OR identity_subject_id IN (` + retiredSubjectIDs + `)`
	return migration.Migration{
		Version:     "0185_retire_default_pm_identity",
		Description: "revoke sessions and remove the retired built-in PM identity mapping",
		Statements: []string{
			`DELETE FROM auth_app_session_metadata WHERE token_hash IN (` +
				`SELECT token_hash FROM auth_app_sessions WHERE ` + retiredSessionsWhere + `)`,
			`DELETE FROM auth_app_sessions WHERE ` + retiredSessionsWhere,
			`DELETE FROM identity_subjects WHERE ` + retiredSubjectWhere,
		},
	}
}
