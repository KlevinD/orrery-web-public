package userauth

import (
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func RegisterMigrations(coordinator *migration.Coordinator) error {
	if coordinator == nil {
		return nil
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, authStateMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, authThrottleMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, authSessionMetadataMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, authSessionCredentialVersionMigration()); err != nil {
		return err
	}
	return coordinator.RegisterInitializer(db.RoleCore, authMFAReplayCounterMigration())
}

func authMFAReplayCounterMigration() migration.Migration {
	return migration.Migration{
		Version:     "0138_userauth_mfa_replay_counters",
		Description: "persist accepted TOTP counters to reject replay across instances",
		Statements:  authMFAReplayCounterMigrationStatements("INTEGER"),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: authMFAReplayCounterMigrationStatements("BIGINT"),
		},
	}
}

func authMFAReplayCounterMigrationStatements(integerType string) []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS auth_mfa_replay_counters (
			username TEXT PRIMARY KEY,
			last_accepted_counter ` + integerType + ` NOT NULL,
			updated_at ` + integerType + ` NOT NULL
		)`,
	}
}

func authSessionCredentialVersionMigration() migration.Migration {
	return migration.Migration{
		Version:     "0137_userauth_session_credential_version",
		Description: "bind app sessions to the current account credential version",
		Statements: []string{
			`ALTER TABLE auth_app_session_metadata ADD COLUMN credential_version TEXT NOT NULL DEFAULT ''`,
		},
	}
}

func authSessionMetadataMigration() migration.Migration {
	return migration.Migration{
		Version:     "0136_userauth_session_metadata",
		Description: "add idle timeout, rotation and device metadata for app sessions",
		Statements: []string{
			`CREATE TABLE IF NOT EXISTS auth_app_session_metadata (
				token_hash TEXT PRIMARY KEY,
				session_id TEXT NOT NULL UNIQUE,
				created_at INTEGER NOT NULL,
				last_seen_at INTEGER NOT NULL,
				must_change_password INTEGER NOT NULL DEFAULT 0,
				ip_address TEXT NOT NULL DEFAULT '',
				user_agent TEXT NOT NULL DEFAULT ''
			)`,
			`CREATE INDEX IF NOT EXISTS idx_auth_app_session_metadata_last_seen ON auth_app_session_metadata (last_seen_at)`,
		},
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: {
				`CREATE TABLE IF NOT EXISTS auth_app_session_metadata (
					token_hash TEXT PRIMARY KEY,
					session_id TEXT NOT NULL UNIQUE,
					created_at BIGINT NOT NULL,
					last_seen_at BIGINT NOT NULL,
					must_change_password INTEGER NOT NULL DEFAULT 0,
					ip_address TEXT NOT NULL DEFAULT '',
					user_agent TEXT NOT NULL DEFAULT ''
				)`,
				`CREATE INDEX IF NOT EXISTS idx_auth_app_session_metadata_last_seen ON auth_app_session_metadata (last_seen_at)`,
			},
		},
	}
}

func authThrottleMigration() migration.Migration {
	return migration.Migration{
		Version:     "0135_userauth_account_throttles",
		Description: "create persistent account login throttle table",
		Statements: []string{
			`CREATE TABLE IF NOT EXISTS auth_login_account_throttles (
				username TEXT PRIMARY KEY,
				status TEXT NOT NULL,
				failure_count INTEGER NOT NULL,
				last_reason TEXT NOT NULL DEFAULT '',
				locked_at INTEGER NOT NULL DEFAULT 0,
				updated_at INTEGER NOT NULL
			)`,
			`CREATE INDEX IF NOT EXISTS idx_auth_login_account_throttles_updated_at ON auth_login_account_throttles (updated_at)`,
		},
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: {
				`CREATE TABLE IF NOT EXISTS auth_login_account_throttles (
					username TEXT PRIMARY KEY,
					status TEXT NOT NULL,
					failure_count INTEGER NOT NULL,
					last_reason TEXT NOT NULL DEFAULT '',
					locked_at BIGINT NOT NULL DEFAULT 0,
					updated_at BIGINT NOT NULL
				)`,
				`CREATE INDEX IF NOT EXISTS idx_auth_login_account_throttles_updated_at ON auth_login_account_throttles (updated_at)`,
			},
		},
	}
}

func authStateMigration() migration.Migration {
	return migration.Migration{
		Version:     "0134_userauth_app_sessions",
		Description: "create persistent login challenge and app session tables",
		Statements:  authStateMigrationStatements("INTEGER"),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: authStateMigrationStatements("BIGINT"),
		},
	}
}

func authStateMigrationStatements(timeType string) []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS auth_login_challenges (
			challenge_id TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			ip_address TEXT NOT NULL DEFAULT '',
			salt TEXT NOT NULL,
			expires_at ` + timeType + ` NOT NULL,
			created_at ` + timeType + ` NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_auth_login_challenges_expires_at ON auth_login_challenges (expires_at)`,
		`CREATE TABLE IF NOT EXISTS auth_app_sessions (
			token_hash TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			subject_provider TEXT NOT NULL DEFAULT '',
			subject TEXT NOT NULL DEFAULT '',
			identity_subject_id TEXT NOT NULL DEFAULT '',
			expires_at ` + timeType + ` NOT NULL,
			created_at ` + timeType + ` NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_auth_app_sessions_username ON auth_app_sessions (username)`,
		`CREATE INDEX IF NOT EXISTS idx_auth_app_sessions_identity_subject ON auth_app_sessions (subject_provider, subject)`,
		`CREATE INDEX IF NOT EXISTS idx_auth_app_sessions_expires_at ON auth_app_sessions (expires_at)`,
	}
}
