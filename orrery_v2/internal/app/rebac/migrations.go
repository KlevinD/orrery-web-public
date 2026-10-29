package rebac

import (
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func RegisterMigrations(coordinator *migration.Coordinator) error {
	if coordinator == nil {
		return nil
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, relationTupleMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, authorizationRevisionMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, effectiveAuthorizationRevisionMigration()); err != nil {
		return err
	}
	return coordinator.RegisterInitializer(db.RoleCore, idempotentAuthorizationRevisionMigration())
}

func idempotentAuthorizationRevisionMigration() migration.Migration {
	trackedTables := `'core.operations_role_definitions', 'core.operations_role_assignments', 'core.operations_organizations', 'core.operations_departments', 'core.operations_positions', 'agent_profiles'`
	sqlite := []string{
		`DROP TRIGGER IF EXISTS trg_effective_authorization_revision_insert`,
		`DROP TRIGGER IF EXISTS trg_effective_authorization_revision_update`,
		`DROP TRIGGER IF EXISTS trg_effective_authorization_revision_delete`,
		`CREATE TRIGGER trg_effective_authorization_revision_insert
		AFTER INSERT ON platform_records
		WHEN NEW.table_name IN (` + trackedTables + `)
		BEGIN
			UPDATE rebac_authorization_revision SET revision = revision + 1 WHERE id = 'global';
		END`,
		`CREATE TRIGGER trg_effective_authorization_revision_update
		AFTER UPDATE ON platform_records
		WHEN (NEW.table_name IN (` + trackedTables + `) OR OLD.table_name IN (` + trackedTables + `))
			AND (NEW.table_name <> OLD.table_name OR NEW.record_key <> OLD.record_key OR NEW.payload_json <> OLD.payload_json)
		BEGIN
			UPDATE rebac_authorization_revision SET revision = revision + 1 WHERE id = 'global';
		END`,
		`CREATE TRIGGER trg_effective_authorization_revision_delete
		AFTER DELETE ON platform_records
		WHEN OLD.table_name IN (` + trackedTables + `)
		BEGIN
			UPDATE rebac_authorization_revision SET revision = revision + 1 WHERE id = 'global';
		END`,
	}
	postgres := []string{
		`CREATE OR REPLACE FUNCTION advance_effective_authorization_revision() RETURNS trigger AS $$
		BEGIN
			IF TG_OP = 'DELETE' THEN
				IF OLD.table_name IN (` + trackedTables + `) THEN
					UPDATE rebac_authorization_revision SET revision = revision + 1 WHERE id = 'global';
				END IF;
				RETURN OLD;
			ELSIF TG_OP = 'INSERT' THEN
				IF NEW.table_name IN (` + trackedTables + `) THEN
					UPDATE rebac_authorization_revision SET revision = revision + 1 WHERE id = 'global';
				END IF;
				RETURN NEW;
			END IF;
			IF (NEW.table_name IN (` + trackedTables + `) OR OLD.table_name IN (` + trackedTables + `))
				AND (NEW.table_name IS DISTINCT FROM OLD.table_name
					OR NEW.record_key IS DISTINCT FROM OLD.record_key
					OR NEW.payload_json IS DISTINCT FROM OLD.payload_json) THEN
				UPDATE rebac_authorization_revision SET revision = revision + 1 WHERE id = 'global';
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql`,
	}
	return migration.Migration{
		Version: "0177_rebac_idempotent_authorization_revision", Description: "advance authorization revision only when effective inputs change",
		Statements: sqlite, StatementsByDialect: map[db.Dialect][]string{db.DialectPostgres: postgres},
	}
}

func effectiveAuthorizationRevisionMigration() migration.Migration {
	trackedTables := `'core.operations_role_definitions', 'core.operations_role_assignments', 'core.operations_organizations', 'core.operations_departments', 'core.operations_positions', 'agent_profiles'`
	sqlite := []string{
		`CREATE TRIGGER IF NOT EXISTS trg_effective_authorization_revision_insert
		AFTER INSERT ON platform_records
		WHEN NEW.table_name IN (` + trackedTables + `)
		BEGIN
			UPDATE rebac_authorization_revision SET revision = revision + 1 WHERE id = 'global';
		END`,
		`CREATE TRIGGER IF NOT EXISTS trg_effective_authorization_revision_update
		AFTER UPDATE ON platform_records
		WHEN NEW.table_name IN (` + trackedTables + `) OR OLD.table_name IN (` + trackedTables + `)
		BEGIN
			UPDATE rebac_authorization_revision SET revision = revision + 1 WHERE id = 'global';
		END`,
		`CREATE TRIGGER IF NOT EXISTS trg_effective_authorization_revision_delete
		AFTER DELETE ON platform_records
		WHEN OLD.table_name IN (` + trackedTables + `)
		BEGIN
			UPDATE rebac_authorization_revision SET revision = revision + 1 WHERE id = 'global';
		END`,
	}
	postgres := []string{
		`CREATE OR REPLACE FUNCTION advance_effective_authorization_revision() RETURNS trigger AS $$
		BEGIN
			IF (TG_OP = 'DELETE' AND OLD.table_name IN (` + trackedTables + `))
				OR (TG_OP = 'INSERT' AND NEW.table_name IN (` + trackedTables + `))
				OR (TG_OP = 'UPDATE' AND (NEW.table_name IN (` + trackedTables + `) OR OLD.table_name IN (` + trackedTables + `))) THEN
				UPDATE rebac_authorization_revision SET revision = revision + 1 WHERE id = 'global';
			END IF;
			IF TG_OP = 'DELETE' THEN
				RETURN OLD;
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql`,
		`DROP TRIGGER IF EXISTS trg_effective_authorization_revision ON platform_records`,
		`CREATE TRIGGER trg_effective_authorization_revision
		AFTER INSERT OR UPDATE OR DELETE ON platform_records
		FOR EACH ROW EXECUTE FUNCTION advance_effective_authorization_revision()`,
	}
	return migration.Migration{
		Version: "0176_rebac_effective_authorization_revision", Description: "advance authorization revision for local role and placement changes",
		Statements: sqlite, StatementsByDialect: map[db.Dialect][]string{db.DialectPostgres: postgres},
	}
}

func authorizationRevisionMigration() migration.Migration {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS rebac_authorization_revision (
			id TEXT PRIMARY KEY,
			revision BIGINT NOT NULL
		)`,
		`INSERT INTO rebac_authorization_revision (id, revision) VALUES ('global', 0) ON CONFLICT(id) DO NOTHING`,
	}
	return migration.Migration{
		Version:     "0164_rebac_authorization_revision",
		Description: "create monotonic ReBAC authorization revision",
		Statements:  statements,
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: append([]string(nil), statements...),
		},
	}
}

func relationTupleMigration() migration.Migration {
	return migration.Migration{
		Version:     "0130_rebac_relation_tuple",
		Description: "create rebac relation tuple tables",
		Statements: []string{
			relationTupleTableSQL("TEXT", "INTEGER"),
			`CREATE INDEX IF NOT EXISTS idx_rebac_relation_tenant_subject ON rebac_relation_tuples (tenant_id, subject_kind, subject_id, relation)`,
			`CREATE INDEX IF NOT EXISTS idx_rebac_relation_tenant_object ON rebac_relation_tuples (tenant_id, object_kind, object_id, relation)`,
		},
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: {
				relationTupleTableSQL("JSONB", "BIGINT"),
				`CREATE INDEX IF NOT EXISTS idx_rebac_relation_tenant_subject ON rebac_relation_tuples (tenant_id, subject_kind, subject_id, relation)`,
				`CREATE INDEX IF NOT EXISTS idx_rebac_relation_tenant_object ON rebac_relation_tuples (tenant_id, object_kind, object_id, relation)`,
				`CREATE INDEX IF NOT EXISTS idx_rebac_relation_subject_ref_gin ON rebac_relation_tuples USING GIN (subject_ref_json)`,
				`CREATE INDEX IF NOT EXISTS idx_rebac_relation_object_ref_gin ON rebac_relation_tuples USING GIN (object_ref_json)`,
			},
		},
	}
}

func relationTupleTableSQL(jsonType, timeType string) string {
	return `CREATE TABLE IF NOT EXISTS rebac_relation_tuples (
		id TEXT PRIMARY KEY,
		tenant_id TEXT NOT NULL,
		subject_kind TEXT NOT NULL,
		subject_id TEXT NOT NULL,
		subject_ref_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		relation TEXT NOT NULL,
		object_kind TEXT NOT NULL,
		object_id TEXT NOT NULL,
		object_ref_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		source_ref_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		valid_from ` + timeType + `,
		valid_to ` + timeType + `,
		created_at ` + timeType + ` NOT NULL
	)`
}
