package contextgraph

import (
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func RegisterMigrations(coordinator *migration.Coordinator) error {
	if coordinator == nil {
		return nil
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, contextGraphMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, contextGraphProjectionAuditMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, contextGraphTenantIsolationMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, contextGraphAccessRefMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, contextGraphAuditAccessRefMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, contextGraphAccessRefBackfillAuditMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, contextGraphSeekIndexMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, contextGraphLineageSeekIndexMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, contextGraphProjectionCommandMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, contextGraphProjectionCommandLeaseMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, contextGraphTemporalHistoryMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, contextGraphAccessRefBoundsMigration()); err != nil {
		return err
	}
	return coordinator.RegisterInitializer(db.RoleCore, contextGraphKnowledgeReadIndexMigration())
}

func contextGraphTemporalHistoryMigration() migration.Migration {
	return migration.Migration{
		Version:     "0195_context_graph_temporal_history",
		Description: "persist bitemporal context graph resource history",
		Statements:  contextGraphTemporalHistoryStatements("TEXT"),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: contextGraphTemporalHistoryStatements("JSONB"),
		},
	}
}

func contextGraphTemporalHistoryStatements(jsonType string) []string {
	return []string{
		`ALTER TABLE context_graph_objects ADD COLUMN temporal_json ` + jsonType + ` NOT NULL DEFAULT '{}'`,
		`ALTER TABLE context_graph_links ADD COLUMN temporal_json ` + jsonType + ` NOT NULL DEFAULT '{}'`,
		`ALTER TABLE context_graph_facts ADD COLUMN temporal_json ` + jsonType + ` NOT NULL DEFAULT '{}'`,
		`ALTER TABLE context_graph_fact_assertions ADD COLUMN temporal_json ` + jsonType + ` NOT NULL DEFAULT '{}'`,
		`CREATE TABLE IF NOT EXISTS context_graph_resource_versions (
			tenant_id TEXT NOT NULL,
			resource_kind TEXT NOT NULL,
			resource_id TEXT NOT NULL,
			assertion_id TEXT NOT NULL,
			payload_json ` + jsonType + ` NOT NULL,
			temporal_mode TEXT NOT NULL DEFAULT 'static',
			business_valid_from BIGINT NOT NULL DEFAULT 0,
			business_valid_to BIGINT,
			source_recorded_at BIGINT NOT NULL DEFAULT 0,
			source_superseded_at BIGINT,
			orrery_ingested_at BIGINT NOT NULL DEFAULT 0,
			access_kind TEXT NOT NULL DEFAULT '',
			access_id TEXT NOT NULL DEFAULT '',
			access_tenant_id TEXT NOT NULL DEFAULT '',
			access_state TEXT NOT NULL DEFAULT 'unresolved',
			source_stream_id TEXT NOT NULL DEFAULT '',
			source_version BIGINT NOT NULL DEFAULT 0,
			updated_at BIGINT NOT NULL DEFAULT 0,
			PRIMARY KEY (tenant_id, resource_kind, assertion_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_context_graph_resource_versions_as_of ON context_graph_resource_versions (tenant_id, resource_kind, resource_id, business_valid_from, source_recorded_at)`,
		`CREATE INDEX IF NOT EXISTS idx_context_graph_resource_versions_known_at ON context_graph_resource_versions (tenant_id, source_recorded_at, resource_kind, resource_id)`,
	}
}

func contextGraphProjectionCommandLeaseMigration() migration.Migration {
	statements := []string{
		`ALTER TABLE context_graph_projection_audit_command_claims ADD COLUMN lease_token TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE context_graph_projection_audit_command_claims ADD COLUMN lease_expires_at BIGINT NOT NULL DEFAULT 0`,
		`CREATE INDEX IF NOT EXISTS idx_context_graph_projection_audit_claims_lease ON context_graph_projection_audit_command_claims (lease_expires_at, tenant_id, audit_id)`,
	}
	return migration.Migration{
		Version: "0175_context_graph_projection_command_leases", Description: "make projection command claims recoverable with fenced leases",
		Statements: statements, StatementsByDialect: map[db.Dialect][]string{db.DialectPostgres: append([]string(nil), statements...)},
	}
}

func contextGraphProjectionCommandMigration() migration.Migration {
	statements := []string{
		`ALTER TABLE context_graph_projection_audits ADD COLUMN revision BIGINT NOT NULL DEFAULT 1`,
		`CREATE TABLE IF NOT EXISTS context_graph_projection_audit_commands (
			tenant_id TEXT NOT NULL,
			idempotency_key TEXT NOT NULL,
			audit_id TEXT NOT NULL,
			operation TEXT NOT NULL,
			request_hash TEXT NOT NULL,
			expected_audit_revision BIGINT NOT NULL,
			state TEXT NOT NULL,
			result_status TEXT NOT NULL DEFAULT '',
			result_reason_code TEXT NOT NULL DEFAULT '',
			result_observed_at BIGINT,
			failure_code TEXT NOT NULL DEFAULT '',
			created_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL,
			PRIMARY KEY (tenant_id, idempotency_key),
			FOREIGN KEY (tenant_id, audit_id) REFERENCES context_graph_projection_audits(tenant_id, id)
		)`,
		`CREATE TABLE IF NOT EXISTS context_graph_projection_audit_command_claims (
			tenant_id TEXT NOT NULL,
			audit_id TEXT NOT NULL,
			idempotency_key TEXT NOT NULL,
			operation TEXT NOT NULL,
			expected_audit_revision BIGINT NOT NULL,
			claimed_at BIGINT NOT NULL,
			PRIMARY KEY (tenant_id, audit_id),
			FOREIGN KEY (tenant_id, audit_id) REFERENCES context_graph_projection_audits(tenant_id, id),
			FOREIGN KEY (tenant_id, idempotency_key) REFERENCES context_graph_projection_audit_commands(tenant_id, idempotency_key)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_context_graph_projection_audit_commands_audit ON context_graph_projection_audit_commands (tenant_id, audit_id, created_at)`,
	}
	return migration.Migration{
		Version:     "0174_context_graph_projection_commands",
		Description: "add durable projection command claims and idempotency records",
		Statements:  statements,
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: append([]string(nil), statements...),
		},
	}
}

func contextGraphLineageSeekIndexMigration() migration.Migration {
	statements := []string{
		`CREATE INDEX IF NOT EXISTS idx_context_graph_facts_lineage_seek ON context_graph_facts (tenant_id, access_state, updated_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_context_graph_projection_audits_lineage_seek ON context_graph_projection_audits (tenant_id, source_event_type, source_event_id, access_state, observed_at, id)`,
	}
	return migration.Migration{
		Version:     "0168_context_graph_lineage_seek_indexes",
		Description: "add bounded context graph lineage seek indexes",
		Statements:  statements,
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: append([]string(nil), statements...),
		},
	}
}

func contextGraphSeekIndexMigration() migration.Migration {
	statements := []string{
		`CREATE INDEX IF NOT EXISTS idx_context_graph_objects_workbench_seek ON context_graph_objects (tenant_id, access_state, object_type, status, updated_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_context_graph_links_from_workbench_seek ON context_graph_links (tenant_id, from_object_id, access_state, link_type, status, updated_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_context_graph_links_to_workbench_seek ON context_graph_links (tenant_id, to_object_id, access_state, link_type, status, updated_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_context_graph_facts_workbench_seek ON context_graph_facts (tenant_id, subject_kind, subject_id, access_state, status, updated_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_context_graph_evidence_workbench_seek ON context_graph_evidence (tenant_id, access_state, source_kind, source_id, observed_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_context_graph_assertions_workbench_seek ON context_graph_fact_assertions (tenant_id, fact_id, access_state, observed_at, assertion_id)`,
		`CREATE INDEX IF NOT EXISTS idx_context_graph_projection_audits_workbench_seek ON context_graph_projection_audits (tenant_id, access_state, status, reason_code, observed_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_context_graph_projection_replay_workbench_seek ON context_graph_projection_audit_replay_history (tenant_id, audit_id, access_state, attempted_at, id)`,
	}
	return migration.Migration{
		Version:     "0167_context_graph_workbench_seek_indexes",
		Description: "add bounded context graph workbench seek indexes",
		Statements:  statements,
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: append([]string(nil), statements...),
		},
	}
}

func contextGraphMigration() migration.Migration {
	return migration.Migration{
		Version:     "0120_context_graph",
		Description: "create ontology context graph tables",
		Statements: []string{
			contextGraphObjectsTableSQL("TEXT", "INTEGER"),
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_context_graph_objects_tenant_key ON context_graph_objects (tenant_id, object_type, canonical_key)`,
			`CREATE INDEX IF NOT EXISTS idx_context_graph_objects_tenant_type ON context_graph_objects (tenant_id, object_type, updated_at)`,
			contextGraphLinksTableSQL("TEXT", "INTEGER"),
			`CREATE INDEX IF NOT EXISTS idx_context_graph_links_tenant_from ON context_graph_links (tenant_id, from_object_id, link_type)`,
			`CREATE INDEX IF NOT EXISTS idx_context_graph_links_tenant_to ON context_graph_links (tenant_id, to_object_id, link_type)`,
			contextGraphFactsTableSQL("TEXT", "INTEGER"),
			`CREATE INDEX IF NOT EXISTS idx_context_graph_facts_tenant_subject ON context_graph_facts (tenant_id, subject_kind, subject_id, predicate)`,
			contextGraphEvidenceTableSQL("INTEGER"),
			`CREATE INDEX IF NOT EXISTS idx_context_graph_evidence_tenant_source ON context_graph_evidence (tenant_id, source_kind, source_id)`,
			contextGraphFactAssertionsTableSQL("TEXT", "INTEGER"),
			`CREATE INDEX IF NOT EXISTS idx_context_graph_fact_assertions_subject ON context_graph_fact_assertions (tenant_id, subject_kind, subject_id, predicate, observed_at)`,
		},
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: {
				contextGraphObjectsTableSQL("JSONB", "BIGINT"),
				`CREATE UNIQUE INDEX IF NOT EXISTS idx_context_graph_objects_tenant_key ON context_graph_objects (tenant_id, object_type, canonical_key)`,
				`CREATE INDEX IF NOT EXISTS idx_context_graph_objects_tenant_type ON context_graph_objects (tenant_id, object_type, updated_at)`,
				`CREATE INDEX IF NOT EXISTS idx_context_graph_objects_properties_gin ON context_graph_objects USING GIN (properties_json)`,
				contextGraphLinksTableSQL("JSONB", "BIGINT"),
				`CREATE INDEX IF NOT EXISTS idx_context_graph_links_tenant_from ON context_graph_links (tenant_id, from_object_id, link_type)`,
				`CREATE INDEX IF NOT EXISTS idx_context_graph_links_tenant_to ON context_graph_links (tenant_id, to_object_id, link_type)`,
				contextGraphFactsTableSQL("JSONB", "BIGINT"),
				`CREATE INDEX IF NOT EXISTS idx_context_graph_facts_tenant_subject ON context_graph_facts (tenant_id, subject_kind, subject_id, predicate)`,
				`CREATE INDEX IF NOT EXISTS idx_context_graph_facts_value_gin ON context_graph_facts USING GIN (value_json)`,
				contextGraphEvidenceTableSQL("BIGINT"),
				`CREATE INDEX IF NOT EXISTS idx_context_graph_evidence_tenant_source ON context_graph_evidence (tenant_id, source_kind, source_id)`,
				contextGraphFactAssertionsTableSQL("JSONB", "BIGINT"),
				`CREATE INDEX IF NOT EXISTS idx_context_graph_fact_assertions_subject ON context_graph_fact_assertions (tenant_id, subject_kind, subject_id, predicate, observed_at)`,
			},
		},
	}
}

func contextGraphProjectionAuditMigration() migration.Migration {
	return migration.Migration{
		Version:     "0121_context_graph_projection_audit",
		Description: "create context graph projection audit tables",
		Statements: []string{
			contextGraphProjectionAuditsTableSQL("TEXT", "INTEGER"),
			`CREATE INDEX IF NOT EXISTS idx_context_graph_projection_audits_event ON context_graph_projection_audits (tenant_id, source_event_type, source_event_id)`,
			`CREATE INDEX IF NOT EXISTS idx_context_graph_projection_audits_status ON context_graph_projection_audits (tenant_id, status, observed_at)`,
			contextGraphProjectionAuditReplayHistoryTableSQL("TEXT", "INTEGER"),
			`CREATE INDEX IF NOT EXISTS idx_context_graph_projection_audit_replay_history_audit ON context_graph_projection_audit_replay_history (tenant_id, audit_id, attempted_at)`,
			`CREATE INDEX IF NOT EXISTS idx_context_graph_projection_audit_replay_history_status ON context_graph_projection_audit_replay_history (tenant_id, status, attempted_at)`,
		},
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: {
				contextGraphProjectionAuditsTableSQL("JSONB", "BIGINT"),
				`CREATE INDEX IF NOT EXISTS idx_context_graph_projection_audits_event ON context_graph_projection_audits (tenant_id, source_event_type, source_event_id)`,
				`CREATE INDEX IF NOT EXISTS idx_context_graph_projection_audits_status ON context_graph_projection_audits (tenant_id, status, observed_at)`,
				`CREATE INDEX IF NOT EXISTS idx_context_graph_projection_audits_details_gin ON context_graph_projection_audits USING GIN (details_json)`,
				contextGraphProjectionAuditReplayHistoryTableSQL("JSONB", "BIGINT"),
				`CREATE INDEX IF NOT EXISTS idx_context_graph_projection_audit_replay_history_audit ON context_graph_projection_audit_replay_history (tenant_id, audit_id, attempted_at)`,
				`CREATE INDEX IF NOT EXISTS idx_context_graph_projection_audit_replay_history_status ON context_graph_projection_audit_replay_history (tenant_id, status, attempted_at)`,
				`CREATE INDEX IF NOT EXISTS idx_context_graph_projection_audit_replay_history_details_gin ON context_graph_projection_audit_replay_history USING GIN (details_json)`,
			},
		},
	}
}

func contextGraphTenantIsolationMigration() migration.Migration {
	return migration.Migration{
		Version:     "0122_context_graph_tenant_isolation",
		Description: "make context graph identities tenant scoped and preserve fact assertions",
		Statements:  contextGraphTenantIsolationStatements("TEXT", "INTEGER"),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: contextGraphTenantIsolationStatements("JSONB", "BIGINT"),
		},
	}
}

func contextGraphTenantIsolationStatements(jsonType, timeType string) []string {
	statements := []string{
		contextGraphObjectsNamedTableSQL("context_graph_objects_v2", jsonType, timeType),
		contextGraphLinksNamedTableSQL("context_graph_links_v2", "context_graph_objects_v2", jsonType, timeType),
		contextGraphFactsNamedTableSQL("context_graph_facts_v2", jsonType, timeType),
		contextGraphEvidenceNamedTableSQL("context_graph_evidence_v2", timeType),
		contextGraphProjectionAuditsNamedTableSQL("context_graph_projection_audits_v2", jsonType, timeType),
		contextGraphProjectionAuditReplayHistoryNamedTableSQL("context_graph_projection_audit_replay_history_v2", jsonType, timeType),
		`INSERT INTO context_graph_objects_v2 (id, tenant_id, object_type, canonical_key, label, properties_json, sensitivity, confidence, status, provenance_json, evidence_refs_json, updated_at) SELECT id, tenant_id, object_type, canonical_key, label, properties_json, sensitivity, confidence, status, provenance_json, evidence_refs_json, updated_at FROM context_graph_objects`,
		`INSERT INTO context_graph_links_v2 (id, tenant_id, link_type, from_object_id, to_object_id, properties_json, confidence, status, provenance_json, evidence_refs_json, updated_at) SELECT id, tenant_id, link_type, from_object_id, to_object_id, properties_json, confidence, status, provenance_json, evidence_refs_json, updated_at FROM context_graph_links`,
		`INSERT INTO context_graph_facts_v2 (id, tenant_id, subject_kind, subject_id, predicate, value_json, properties_json, confidence, status, provenance_json, evidence_refs_json, updated_at) SELECT id, tenant_id, subject_kind, subject_id, predicate, value_json, properties_json, confidence, status, provenance_json, evidence_refs_json, updated_at FROM context_graph_facts`,
		`INSERT INTO context_graph_evidence_v2 SELECT * FROM context_graph_evidence`,
		`INSERT INTO context_graph_projection_audits_v2 SELECT * FROM context_graph_projection_audits`,
		`INSERT INTO context_graph_projection_audit_replay_history_v2 SELECT * FROM context_graph_projection_audit_replay_history`,
		`DROP TABLE context_graph_links`,
		`DROP TABLE context_graph_facts`,
		`DROP TABLE context_graph_evidence`,
		`DROP TABLE context_graph_projection_audit_replay_history`,
		`DROP TABLE context_graph_projection_audits`,
		`DROP TABLE context_graph_objects`,
		`ALTER TABLE context_graph_objects_v2 RENAME TO context_graph_objects`,
		`ALTER TABLE context_graph_links_v2 RENAME TO context_graph_links`,
		`ALTER TABLE context_graph_facts_v2 RENAME TO context_graph_facts`,
		`ALTER TABLE context_graph_evidence_v2 RENAME TO context_graph_evidence`,
		`ALTER TABLE context_graph_projection_audits_v2 RENAME TO context_graph_projection_audits`,
		`ALTER TABLE context_graph_projection_audit_replay_history_v2 RENAME TO context_graph_projection_audit_replay_history`,
		`CREATE UNIQUE INDEX idx_context_graph_objects_tenant_key ON context_graph_objects (tenant_id, object_type, canonical_key)`,
		`CREATE INDEX idx_context_graph_objects_tenant_type ON context_graph_objects (tenant_id, object_type, updated_at)`,
		`CREATE INDEX idx_context_graph_links_tenant_from ON context_graph_links (tenant_id, from_object_id, link_type)`,
		`CREATE INDEX idx_context_graph_links_tenant_to ON context_graph_links (tenant_id, to_object_id, link_type)`,
		`CREATE INDEX idx_context_graph_facts_tenant_subject ON context_graph_facts (tenant_id, subject_kind, subject_id, predicate)`,
		`CREATE INDEX idx_context_graph_evidence_tenant_source ON context_graph_evidence (tenant_id, source_kind, source_id)`,
		`CREATE INDEX idx_context_graph_projection_audits_event ON context_graph_projection_audits (tenant_id, source_event_type, source_event_id)`,
		`CREATE INDEX idx_context_graph_projection_audits_status ON context_graph_projection_audits (tenant_id, status, observed_at)`,
		`CREATE INDEX idx_context_graph_projection_audit_replay_history_audit ON context_graph_projection_audit_replay_history (tenant_id, audit_id, attempted_at)`,
		`CREATE INDEX idx_context_graph_projection_audit_replay_history_status ON context_graph_projection_audit_replay_history (tenant_id, status, attempted_at)`,
		contextGraphFactAssertionsTableSQL(jsonType, timeType),
		`CREATE INDEX IF NOT EXISTS idx_context_graph_fact_assertions_subject ON context_graph_fact_assertions (tenant_id, subject_kind, subject_id, predicate, observed_at)`,
	}
	if jsonType == "JSONB" {
		statements = append(statements,
			`CREATE INDEX idx_context_graph_objects_properties_gin ON context_graph_objects USING GIN (properties_json)`,
			`CREATE INDEX idx_context_graph_facts_value_gin ON context_graph_facts USING GIN (value_json)`,
			`CREATE INDEX idx_context_graph_projection_audits_details_gin ON context_graph_projection_audits USING GIN (details_json)`,
			`CREATE INDEX idx_context_graph_projection_audit_replay_history_details_gin ON context_graph_projection_audit_replay_history USING GIN (details_json)`,
		)
	}
	return statements
}

func contextGraphFactAssertionsTableSQL(jsonType, timeType string) string {
	return `CREATE TABLE IF NOT EXISTS context_graph_fact_assertions (
		assertion_id TEXT NOT NULL,
		tenant_id TEXT NOT NULL,
		fact_id TEXT NOT NULL,
		subject_kind TEXT NOT NULL,
		subject_id TEXT NOT NULL,
		predicate TEXT NOT NULL,
		value_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		properties_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		confidence REAL NOT NULL DEFAULT 0,
		status TEXT NOT NULL DEFAULT '',
		provenance_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		evidence_refs_json ` + jsonType + ` NOT NULL DEFAULT '[]',
		source_stream_id TEXT NOT NULL DEFAULT '',
		source_version ` + timeType + ` NOT NULL DEFAULT 0,
		observed_at ` + timeType + ` NOT NULL,
		PRIMARY KEY (tenant_id, assertion_id)
	)`
}

func contextGraphObjectsTableSQL(jsonType, timeType string) string {
	return contextGraphObjectsNamedTableSQL("context_graph_objects", jsonType, timeType)
}

func contextGraphObjectsNamedTableSQL(table, jsonType, timeType string) string {
	return `CREATE TABLE IF NOT EXISTS ` + table + ` (
			id TEXT NOT NULL,
			tenant_id TEXT NOT NULL,
		object_type TEXT NOT NULL,
		canonical_key TEXT NOT NULL,
		label TEXT NOT NULL,
		properties_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		sensitivity TEXT NOT NULL DEFAULT '',
		confidence REAL NOT NULL DEFAULT 0,
		status TEXT NOT NULL DEFAULT '',
		provenance_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		evidence_refs_json ` + jsonType + ` NOT NULL DEFAULT '[]',
		source_stream_id TEXT NOT NULL DEFAULT '',
		source_version ` + timeType + ` NOT NULL DEFAULT 0,
			updated_at ` + timeType + ` NOT NULL,
			PRIMARY KEY (tenant_id, id)
		)`
}

func contextGraphLinksTableSQL(jsonType, timeType string) string {
	return contextGraphLinksNamedTableSQL("context_graph_links", "context_graph_objects", jsonType, timeType)
}

func contextGraphLinksNamedTableSQL(table, objectTable, jsonType, timeType string) string {
	return `CREATE TABLE IF NOT EXISTS ` + table + ` (
			id TEXT NOT NULL,
			tenant_id TEXT NOT NULL,
		link_type TEXT NOT NULL,
		from_object_id TEXT NOT NULL,
		to_object_id TEXT NOT NULL,
		properties_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		confidence REAL NOT NULL DEFAULT 0,
		status TEXT NOT NULL DEFAULT '',
		provenance_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		evidence_refs_json ` + jsonType + ` NOT NULL DEFAULT '[]',
		source_stream_id TEXT NOT NULL DEFAULT '',
		source_version ` + timeType + ` NOT NULL DEFAULT 0,
			updated_at ` + timeType + ` NOT NULL,
			PRIMARY KEY (tenant_id, id),
			FOREIGN KEY (tenant_id, from_object_id) REFERENCES ` + objectTable + `(tenant_id, id),
			FOREIGN KEY (tenant_id, to_object_id) REFERENCES ` + objectTable + `(tenant_id, id)
		)`
}

func contextGraphFactsTableSQL(jsonType, timeType string) string {
	return contextGraphFactsNamedTableSQL("context_graph_facts", jsonType, timeType)
}

func contextGraphFactsNamedTableSQL(table, jsonType, timeType string) string {
	return `CREATE TABLE IF NOT EXISTS ` + table + ` (
			id TEXT NOT NULL,
		tenant_id TEXT NOT NULL,
		subject_kind TEXT NOT NULL,
		subject_id TEXT NOT NULL,
		predicate TEXT NOT NULL,
		value_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		properties_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		confidence REAL NOT NULL DEFAULT 0,
		status TEXT NOT NULL DEFAULT '',
		provenance_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		evidence_refs_json ` + jsonType + ` NOT NULL DEFAULT '[]',
		source_stream_id TEXT NOT NULL DEFAULT '',
		source_version ` + timeType + ` NOT NULL DEFAULT 0,
			updated_at ` + timeType + ` NOT NULL,
			PRIMARY KEY (tenant_id, id)
		)`
}

func contextGraphEvidenceTableSQL(timeType string) string {
	return contextGraphEvidenceNamedTableSQL("context_graph_evidence", timeType)
}

func contextGraphEvidenceNamedTableSQL(table, timeType string) string {
	return `CREATE TABLE IF NOT EXISTS ` + table + ` (
			id TEXT NOT NULL,
		tenant_id TEXT NOT NULL,
		source_kind TEXT NOT NULL,
		source_id TEXT NOT NULL,
		summary TEXT NOT NULL DEFAULT '',
		confidence REAL NOT NULL DEFAULT 0,
			observed_at ` + timeType + ` NOT NULL,
			PRIMARY KEY (tenant_id, id)
		)`
}

func contextGraphProjectionAuditsTableSQL(jsonType, timeType string) string {
	return contextGraphProjectionAuditsNamedTableSQL("context_graph_projection_audits", jsonType, timeType)
}

func contextGraphProjectionAuditsNamedTableSQL(table, jsonType, timeType string) string {
	return `CREATE TABLE IF NOT EXISTS ` + table + ` (
			id TEXT NOT NULL,
		tenant_id TEXT NOT NULL,
		source_event_id TEXT NOT NULL,
		source_event_type TEXT NOT NULL,
		status TEXT NOT NULL,
		reason_code TEXT NOT NULL,
		reason TEXT NOT NULL DEFAULT '',
		details_json ` + jsonType + ` NOT NULL DEFAULT '{}',
			observed_at ` + timeType + ` NOT NULL,
			PRIMARY KEY (tenant_id, id)
		)`
}

func contextGraphProjectionAuditReplayHistoryTableSQL(jsonType, timeType string) string {
	return contextGraphProjectionAuditReplayHistoryNamedTableSQL("context_graph_projection_audit_replay_history", jsonType, timeType)
}

func contextGraphProjectionAuditReplayHistoryNamedTableSQL(table, jsonType, timeType string) string {
	return `CREATE TABLE IF NOT EXISTS ` + table + ` (
			id TEXT NOT NULL,
		audit_id TEXT NOT NULL,
		tenant_id TEXT NOT NULL,
		source_event_id TEXT NOT NULL,
		source_event_type TEXT NOT NULL,
		status TEXT NOT NULL,
		error TEXT NOT NULL DEFAULT '',
		details_json ` + jsonType + ` NOT NULL DEFAULT '{}',
			attempted_at ` + timeType + ` NOT NULL,
			PRIMARY KEY (tenant_id, id)
		)`
}
