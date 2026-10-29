package identity

import (
	"strings"
	"testing"

	"orrery_v2/internal/platform/db"
)

func TestIdentitySubjectsMigrationUsesPostgresBigintAndIndexes(t *testing.T) {
	migration := identitySubjectsMigration()
	statements := strings.Join(migration.StatementsByDialect[db.DialectPostgres], "\n")

	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS identity_subjects",
		"last_login_at BIGINT",
		"idx_identity_subjects_provider_subject",
		"idx_identity_subjects_local_username",
	} {
		if !strings.Contains(statements, want) {
			t.Fatalf("postgres migration missing %q:\n%s", want, statements)
		}
	}
}

func TestRetiredPMIdentityMigrationRevokesSessionsAndDeletesMapping(t *testing.T) {
	migration := retiredPMIdentityMigration()
	statements := strings.Join(migration.Statements, "\n")

	for _, want := range []string{
		"DELETE FROM auth_app_session_metadata",
		"DELETE FROM auth_app_sessions",
		"DELETE FROM identity_subjects",
		"provider = 'kratos'",
		"username = 'pm' OR identity_subject_id",
		"username = 'pm'",
		"agent_profile_id = 'human-pm'",
	} {
		if !strings.Contains(statements, want) {
			t.Fatalf("retired PM migration missing %q:\n%s", want, statements)
		}
	}
}
