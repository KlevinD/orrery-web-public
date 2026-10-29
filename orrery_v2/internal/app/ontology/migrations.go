package ontology

import (
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func RegisterMigrations(coordinator *migration.Coordinator) error {
	if coordinator == nil {
		return nil
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, ontologyRegistryMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, ontologyMappingRuleGovernanceMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, ontologyMetadataProposalMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, ontologyMetadataProposalReviewMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, ontologyMetadataProposalSourceTaxonomyMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, ontologyActionBindingsMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, ontologyMetadataProposalPromotionReceiptMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, ontologyMetadataPromotionReceiptRequestHashMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, ontologyMetadataPromotionReceiptTenantIdempotencyMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, ontologyModelRunLedgerMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, ontologyModelRunEvaluationArtifactMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, ontologyModelRunEvaluationPolicyProfileMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, ontologyModelRunEvaluationPolicyProfileMetricThresholdMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, ontologyModelRunEvaluationAlertAuditMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, ontologyModelRunEvaluationMetricRecordingMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, ontologyModelRunEvaluationAlertNotificationHistoryMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, ontologyModelRunEvaluationAlertNotificationReceiptMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, ontologyModelRunEvaluationAlertNotificationPolicyMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, ontologyWorkbenchSeekIndexMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, ontologySchemaDefinitionSeekIndexMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, ontologyMetadataProposalRevisionHashMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, ontologyMetadataReceiptProposalBindingMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, ontologyGovernanceCursorIndexMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, ontologyMetadataPromotionPlanMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, ontologyMappingProjectionRefreshMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, ontologyMappingProjectionRefreshRetryMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, ontologyMappingProjectionRefreshCommandMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, ontologyTemporalSemanticsMigration()); err != nil {
		return err
	}
	return coordinator.RegisterInitializer(db.RoleCore, ontologyStateProjectionMigration())
}

func ontologyStateProjectionMigration() migration.Migration {
	sqliteStatements := []string{
		`CREATE TABLE IF NOT EXISTS ontology_projection_state (
	tenant_id TEXT NOT NULL,
	event_type TEXT NOT NULL,
	entity_id TEXT NOT NULL,
	semantic_hash TEXT NOT NULL,
	applied_version INTEGER NOT NULL DEFAULT 0,
	observed_version INTEGER NOT NULL DEFAULT 0,
	source_updated_at INTEGER NOT NULL DEFAULT 0,
	projected_at INTEGER NOT NULL,
	PRIMARY KEY (tenant_id, event_type, entity_id)
)`,
		`CREATE TABLE IF NOT EXISTS ontology_daily_snapshots (
	tenant_id TEXT NOT NULL,
	event_type TEXT NOT NULL,
	entity_id TEXT NOT NULL,
	snapshot_date TEXT NOT NULL,
	semantic_hash TEXT NOT NULL,
	payload_json TEXT NOT NULL,
	source_version INTEGER NOT NULL DEFAULT 0,
	source_updated_at INTEGER NOT NULL DEFAULT 0,
	projected_at INTEGER NOT NULL,
	PRIMARY KEY (tenant_id, event_type, entity_id, snapshot_date)
)`,
		`CREATE INDEX IF NOT EXISTS idx_ontology_daily_snapshots_retention ON ontology_daily_snapshots (snapshot_date, tenant_id, event_type, entity_id)`,
		`CREATE TABLE IF NOT EXISTS ontology_refresh_manifests (
	refresh_kind TEXT NOT NULL,
	fingerprint TEXT NOT NULL,
	snapshot_date TEXT NOT NULL,
	mapping_revision INTEGER NOT NULL DEFAULT 0,
	source_count INTEGER NOT NULL DEFAULT 0,
	changed_count INTEGER NOT NULL DEFAULT 0,
	snapshot_only_count INTEGER NOT NULL DEFAULT 0,
	noop_count INTEGER NOT NULL DEFAULT 0,
	stale_count INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL,
	started_at INTEGER NOT NULL,
	completed_at INTEGER NOT NULL DEFAULT 0,
	last_error TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (refresh_kind, fingerprint)
)`,
		`CREATE INDEX IF NOT EXISTS idx_ontology_refresh_manifests_status ON ontology_refresh_manifests (status, started_at, refresh_kind, fingerprint)`,
	}
	postgresStatements := []string{
		`CREATE TABLE IF NOT EXISTS ontology_projection_state (
	tenant_id TEXT NOT NULL,
	event_type TEXT NOT NULL,
	entity_id TEXT NOT NULL,
	semantic_hash TEXT NOT NULL,
	applied_version BIGINT NOT NULL DEFAULT 0,
	observed_version BIGINT NOT NULL DEFAULT 0,
	source_updated_at BIGINT NOT NULL DEFAULT 0,
	projected_at BIGINT NOT NULL,
	PRIMARY KEY (tenant_id, event_type, entity_id)
)`,
		`CREATE TABLE IF NOT EXISTS ontology_daily_snapshots (
	tenant_id TEXT NOT NULL,
	event_type TEXT NOT NULL,
	entity_id TEXT NOT NULL,
	snapshot_date TEXT NOT NULL,
	semantic_hash TEXT NOT NULL,
	payload_json TEXT NOT NULL,
	source_version BIGINT NOT NULL DEFAULT 0,
	source_updated_at BIGINT NOT NULL DEFAULT 0,
	projected_at BIGINT NOT NULL,
	PRIMARY KEY (tenant_id, event_type, entity_id, snapshot_date)
)`,
		`CREATE INDEX IF NOT EXISTS idx_ontology_daily_snapshots_retention ON ontology_daily_snapshots (snapshot_date, tenant_id, event_type, entity_id)`,
		`CREATE TABLE IF NOT EXISTS ontology_refresh_manifests (
	refresh_kind TEXT NOT NULL,
	fingerprint TEXT NOT NULL,
	snapshot_date TEXT NOT NULL,
	mapping_revision BIGINT NOT NULL DEFAULT 0,
	source_count INTEGER NOT NULL DEFAULT 0,
	changed_count INTEGER NOT NULL DEFAULT 0,
	snapshot_only_count INTEGER NOT NULL DEFAULT 0,
	noop_count INTEGER NOT NULL DEFAULT 0,
	stale_count INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL,
	started_at BIGINT NOT NULL,
	completed_at BIGINT NOT NULL DEFAULT 0,
	last_error TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (refresh_kind, fingerprint)
)`,
		`CREATE INDEX IF NOT EXISTS idx_ontology_refresh_manifests_status ON ontology_refresh_manifests (status, started_at, refresh_kind, fingerprint)`,
	}
	return migration.Migration{
		Version:     "0195_ontology_state_projection",
		Description: "add bounded ontology state projection storage",
		Statements:  sqliteStatements,
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: postgresStatements,
		},
	}
}

func ontologyTemporalSemanticsMigration() migration.Migration {
	statements := []string{
		`ALTER TABLE ontology_object_types ADD COLUMN temporal_mode TEXT NOT NULL DEFAULT 'static'`,
		`ALTER TABLE ontology_link_types ADD COLUMN temporal_mode TEXT NOT NULL DEFAULT 'static'`,
		`ALTER TABLE ontology_property_types ADD COLUMN temporal_mode TEXT NOT NULL DEFAULT 'static'`,
		`CREATE INDEX IF NOT EXISTS idx_ontology_object_types_temporal_mode ON ontology_object_types (tenant_id, temporal_mode, id)`,
		`CREATE INDEX IF NOT EXISTS idx_ontology_link_types_temporal_mode ON ontology_link_types (tenant_id, temporal_mode, id)`,
		`CREATE INDEX IF NOT EXISTS idx_ontology_property_types_temporal_mode ON ontology_property_types (tenant_id, temporal_mode, object_type_id, id)`,
	}
	return migration.Migration{
		Version:     "0194_ontology_temporal_semantics",
		Description: "add explicit temporal semantics to ontology definitions",
		Statements:  statements,
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: append([]string(nil), statements...),
		},
	}
}

func ontologyWorkbenchSeekIndexMigration() migration.Migration {
	statements := []string{
		`CREATE INDEX IF NOT EXISTS idx_ontology_object_types_workbench_seek ON ontology_object_types (tenant_id, updated_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_ontology_link_types_workbench_seek ON ontology_link_types (tenant_id, updated_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_ontology_property_types_workbench_seek ON ontology_property_types (tenant_id, updated_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_ontology_metadata_proposals_workbench_seek ON ontology_metadata_proposals (tenant_id, status, target_kind, updated_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_ontology_metadata_receipts_workbench_seek ON ontology_metadata_promotion_receipts (tenant_id, proposal_id, updated_at, receipt_id)`,
	}
	return migration.Migration{
		Version:     "0168_ontology_workbench_seek_indexes",
		Description: "add bounded ontology registry and governance seek indexes",
		Statements:  statements,
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: append([]string(nil), statements...),
		},
	}
}

func ontologySchemaDefinitionSeekIndexMigration() migration.Migration {
	statements := []string{
		`CREATE INDEX IF NOT EXISTS idx_ontology_object_types_schema_seek ON ontology_object_types (tenant_id, id)`,
		`CREATE INDEX IF NOT EXISTS idx_ontology_link_types_schema_seek ON ontology_link_types (tenant_id, id)`,
		`CREATE INDEX IF NOT EXISTS idx_ontology_property_types_schema_seek ON ontology_property_types (tenant_id, id)`,
		`CREATE INDEX IF NOT EXISTS idx_ontology_mapping_rules_schema_seek ON ontology_mapping_rules (tenant_id, id)`,
	}
	return migration.Migration{
		Version:     "0169_ontology_schema_definition_seek_indexes",
		Description: "add tenant and id seek indexes for bounded schema definition reads",
		Statements:  statements,
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: append([]string(nil), statements...),
		},
	}
}

func ontologyMetadataProposalRevisionHashMigration() migration.Migration {
	statements := []string{
		`ALTER TABLE ontology_metadata_proposals ADD COLUMN proposal_revision INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE ontology_metadata_proposals ADD COLUMN proposal_content_hash TEXT NOT NULL DEFAULT ''`,
		`UPDATE ontology_metadata_proposals SET proposal_revision = 1 WHERE proposal_revision <= 0`,
		`CREATE INDEX IF NOT EXISTS idx_ontology_metadata_proposals_revision_hash ON ontology_metadata_proposals (tenant_id, id, proposal_revision, proposal_content_hash)`,
	}
	return migration.Migration{
		Version:     "0170_ontology_metadata_proposal_revision_hash",
		Description: "add monotonic revision and canonical content hash to ontology metadata proposals",
		Statements:  statements,
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: {
				`ALTER TABLE ontology_metadata_proposals ADD COLUMN proposal_revision BIGINT NOT NULL DEFAULT 0`,
				`ALTER TABLE ontology_metadata_proposals ADD COLUMN proposal_content_hash TEXT NOT NULL DEFAULT ''`,
				`UPDATE ontology_metadata_proposals SET proposal_revision = 1 WHERE proposal_revision <= 0`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_metadata_proposals_revision_hash ON ontology_metadata_proposals (tenant_id, id, proposal_revision, proposal_content_hash)`,
			},
		},
	}
}

func ontologyMetadataReceiptProposalBindingMigration() migration.Migration {
	sqliteStatements := []string{
		`ALTER TABLE ontology_metadata_promotion_receipts ADD COLUMN proposal_revision INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE ontology_metadata_promotion_receipts ADD COLUMN proposal_content_hash TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE ontology_metadata_promotion_receipts ADD COLUMN proposal_binding_status TEXT NOT NULL DEFAULT 'legacy-unresolved'`,
		`CREATE INDEX IF NOT EXISTS idx_ontology_metadata_receipts_proposal_binding ON ontology_metadata_promotion_receipts (tenant_id, proposal_id, proposal_revision, proposal_content_hash, proposal_binding_status, status, updated_at, receipt_id)`,
	}
	return migration.Migration{
		Version:     "0171_ontology_metadata_receipt_proposal_binding",
		Description: "bind ontology promotion receipts to canonical proposal revisions",
		Statements:  sqliteStatements,
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: {
				`ALTER TABLE ontology_metadata_promotion_receipts ADD COLUMN proposal_revision BIGINT NOT NULL DEFAULT 0`,
				`ALTER TABLE ontology_metadata_promotion_receipts ADD COLUMN proposal_content_hash TEXT NOT NULL DEFAULT ''`,
				`ALTER TABLE ontology_metadata_promotion_receipts ADD COLUMN proposal_binding_status TEXT NOT NULL DEFAULT 'legacy-unresolved'`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_metadata_receipts_proposal_binding ON ontology_metadata_promotion_receipts (tenant_id, proposal_id, proposal_revision, proposal_content_hash, proposal_binding_status, status, updated_at, receipt_id)`,
			},
		},
	}
}

func ontologyGovernanceCursorIndexMigration() migration.Migration {
	statements := []string{
		`CREATE INDEX IF NOT EXISTS idx_ontology_metadata_proposals_governance_seek ON ontology_metadata_proposals (tenant_id, updated_at DESC, id ASC)`,
		`CREATE INDEX IF NOT EXISTS idx_ontology_metadata_receipts_governance_seek ON ontology_metadata_promotion_receipts (tenant_id, proposal_id, updated_at DESC, receipt_id ASC)`,
	}
	return migration.Migration{
		Version:     "0172_ontology_governance_cursor_indexes",
		Description: "add stable cursor indexes for ontology governance proposal and receipt reads",
		Statements:  statements,
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: append([]string(nil), statements...),
		},
	}
}

func ontologyMetadataPromotionPlanMigration() migration.Migration {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS ontology_metadata_promotion_plans (
	plan_id TEXT PRIMARY KEY,
	principal_key TEXT NOT NULL,
	tenant_id TEXT NOT NULL,
	binding_version INTEGER NOT NULL,
	proposal_id TEXT NOT NULL,
	proposal_revision INTEGER NOT NULL,
	proposal_content_hash TEXT NOT NULL,
	targets_json TEXT NOT NULL,
	diff_hash TEXT NOT NULL,
	diff_json TEXT NOT NULL,
	authorization_revision TEXT NOT NULL,
	policy_revision TEXT NOT NULL,
	expires_at INTEGER NOT NULL,
	state TEXT NOT NULL,
	idempotency_key TEXT NOT NULL DEFAULT '',
	receipt_id TEXT NOT NULL DEFAULT '',
	invalidation_reason TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
)`,
		`CREATE INDEX IF NOT EXISTS idx_ontology_metadata_promotion_plans_active ON ontology_metadata_promotion_plans (tenant_id, principal_key, state, expires_at, plan_id)`,
		`CREATE INDEX IF NOT EXISTS idx_ontology_metadata_promotion_plans_proposal ON ontology_metadata_promotion_plans (tenant_id, proposal_id, proposal_revision, state, created_at, plan_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_ontology_metadata_promotion_plans_idempotency ON ontology_metadata_promotion_plans (tenant_id, principal_key, idempotency_key) WHERE idempotency_key <> ''`,
		`CREATE INDEX IF NOT EXISTS idx_ontology_metadata_promotion_plans_receipt ON ontology_metadata_promotion_plans (receipt_id) WHERE receipt_id <> ''`,
	}
	postgres := append([]string(nil), statements...)
	postgres[0] = `CREATE TABLE IF NOT EXISTS ontology_metadata_promotion_plans (
	plan_id TEXT PRIMARY KEY,
	principal_key TEXT NOT NULL,
	tenant_id TEXT NOT NULL,
	binding_version BIGINT NOT NULL,
	proposal_id TEXT NOT NULL,
	proposal_revision BIGINT NOT NULL,
	proposal_content_hash TEXT NOT NULL,
	targets_json TEXT NOT NULL,
	diff_hash TEXT NOT NULL,
	diff_json TEXT NOT NULL,
	authorization_revision TEXT NOT NULL,
	policy_revision TEXT NOT NULL,
	expires_at BIGINT NOT NULL,
	state TEXT NOT NULL,
	idempotency_key TEXT NOT NULL DEFAULT '',
	receipt_id TEXT NOT NULL DEFAULT '',
	invalidation_reason TEXT NOT NULL DEFAULT '',
	created_at BIGINT NOT NULL,
	updated_at BIGINT NOT NULL
)`
	return migration.Migration{
		Version:     "0173_ontology_metadata_promotion_plans",
		Description: "persist principal-bound single-use ontology metadata promotion plans",
		Statements:  statements,
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: postgres,
		},
	}
}

func ontologyMappingProjectionRefreshMigration() migration.Migration {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS ontology_mapping_projection_refreshes (
	receipt_id TEXT PRIMARY KEY,
	tenant_id TEXT NOT NULL,
	state TEXT NOT NULL,
	attempts INTEGER NOT NULL DEFAULT 0,
	last_error_code TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
)`,
		`CREATE INDEX IF NOT EXISTS idx_ontology_mapping_projection_refresh_pending ON ontology_mapping_projection_refreshes (state, created_at, receipt_id)`,
	}
	postgres := append([]string(nil), statements...)
	postgres[0] = `CREATE TABLE IF NOT EXISTS ontology_mapping_projection_refreshes (
	receipt_id TEXT PRIMARY KEY,
	tenant_id TEXT NOT NULL,
	state TEXT NOT NULL,
	attempts INTEGER NOT NULL DEFAULT 0,
	last_error_code TEXT NOT NULL DEFAULT '',
	created_at BIGINT NOT NULL,
	updated_at BIGINT NOT NULL
)`
	return migration.Migration{
		Version: "0174_ontology_mapping_projection_refreshes", Description: "persist mapping projection refresh delivery after governance apply",
		Statements: statements, StatementsByDialect: map[db.Dialect][]string{db.DialectPostgres: postgres},
	}
}

func ontologyMappingProjectionRefreshRetryMigration() migration.Migration {
	statements := []string{
		`DROP INDEX IF EXISTS idx_ontology_mapping_projection_refresh_pending`,
		`ALTER TABLE ontology_mapping_projection_refreshes ADD COLUMN next_attempt_at INTEGER NOT NULL DEFAULT 0`,
		`CREATE INDEX IF NOT EXISTS idx_ontology_mapping_projection_refresh_pending ON ontology_mapping_projection_refreshes (state, next_attempt_at, created_at, receipt_id)`,
	}
	postgres := append([]string(nil), statements...)
	postgres[1] = `ALTER TABLE ontology_mapping_projection_refreshes ADD COLUMN next_attempt_at BIGINT NOT NULL DEFAULT 0`
	return migration.Migration{
		Version: "0177_ontology_mapping_projection_refresh_retries", Description: "bound and schedule mapping projection refresh retries",
		Statements: statements, StatementsByDialect: map[db.Dialect][]string{db.DialectPostgres: postgres},
	}
}

func ontologyMappingProjectionRefreshCommandMigration() migration.Migration {
	statements := mappingProjectionRefreshCommandStatements("INTEGER")
	return migration.Migration{
		Version: "0183_ontology_mapping_projection_refresh_commands", Description: "persist idempotent mapping projection refresh retry commands",
		Statements: statements, StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: mappingProjectionRefreshCommandStatements("BIGINT"),
		},
	}
}

func mappingProjectionRefreshCommandStatements(timeType string) []string {
	return []string{`CREATE TABLE IF NOT EXISTS ontology_mapping_projection_refresh_commands (
		tenant_id TEXT NOT NULL,
		idempotency_key TEXT NOT NULL,
		receipt_id TEXT NOT NULL,
		request_hash TEXT NOT NULL,
		state TEXT NOT NULL,
		result_status TEXT NOT NULL DEFAULT '',
		created_at ` + timeType + ` NOT NULL,
		updated_at ` + timeType + ` NOT NULL,
		PRIMARY KEY (tenant_id, idempotency_key)
	)`}
}

func ontologyRegistryMigration() migration.Migration {
	return migration.Migration{
		Version:     "0100_ontology_registry",
		Description: "create ontology registry tables",
		Statements: []string{
			ontologyObjectTypesTableSQL("TEXT", "INTEGER"),
			`CREATE INDEX IF NOT EXISTS idx_ontology_object_types_tenant ON ontology_object_types (tenant_id, tenant_scope, id)`,
			ontologyLinkTypesTableSQL("INTEGER"),
			`CREATE INDEX IF NOT EXISTS idx_ontology_link_types_from_to ON ontology_link_types (from_object_type, to_object_type)`,
			ontologyPropertyTypesTableSQL("INTEGER"),
			`CREATE INDEX IF NOT EXISTS idx_ontology_property_types_object ON ontology_property_types (object_type_id, property_id)`,
			ontologyMappingRulesTableSQL("TEXT", "INTEGER"),
			`CREATE INDEX IF NOT EXISTS idx_ontology_mapping_rules_tenant_event ON ontology_mapping_rules (tenant_id, event_type, enabled)`,
		},
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: {
				ontologyObjectTypesTableSQL("JSONB", "BIGINT"),
				`CREATE INDEX IF NOT EXISTS idx_ontology_object_types_tenant ON ontology_object_types (tenant_id, tenant_scope, id)`,
				ontologyLinkTypesTableSQL("BIGINT"),
				`CREATE INDEX IF NOT EXISTS idx_ontology_link_types_from_to ON ontology_link_types (from_object_type, to_object_type)`,
				ontologyPropertyTypesTableSQL("BIGINT"),
				`CREATE INDEX IF NOT EXISTS idx_ontology_property_types_object ON ontology_property_types (object_type_id, property_id)`,
				ontologyMappingRulesTableSQL("JSONB", "BIGINT"),
				`CREATE INDEX IF NOT EXISTS idx_ontology_mapping_rules_tenant_event ON ontology_mapping_rules (tenant_id, event_type, enabled)`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_mapping_rules_rule_gin ON ontology_mapping_rules USING GIN (rule_json)`,
			},
		},
	}
}

func ontologyMappingRuleGovernanceMigration() migration.Migration {
	return migration.Migration{
		Version:     "0101_ontology_mapping_rule_governance",
		Description: "add governance metadata to ontology mapping rules",
		Statements: []string{
			`ALTER TABLE ontology_mapping_rules ADD COLUMN source_domain TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE ontology_mapping_rules ADD COLUMN source_ref TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE ontology_mapping_rules ADD COLUMN event_schema_version TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE ontology_mapping_rules ADD COLUMN rule_hash TEXT NOT NULL DEFAULT ''`,
			`CREATE INDEX IF NOT EXISTS idx_ontology_mapping_rules_source_domain ON ontology_mapping_rules (source_domain)`,
			`CREATE INDEX IF NOT EXISTS idx_ontology_mapping_rules_event_schema_version ON ontology_mapping_rules (event_schema_version)`,
		},
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: {
				`ALTER TABLE ontology_mapping_rules ADD COLUMN source_domain TEXT NOT NULL DEFAULT ''`,
				`ALTER TABLE ontology_mapping_rules ADD COLUMN source_ref TEXT NOT NULL DEFAULT ''`,
				`ALTER TABLE ontology_mapping_rules ADD COLUMN event_schema_version TEXT NOT NULL DEFAULT ''`,
				`ALTER TABLE ontology_mapping_rules ADD COLUMN rule_hash TEXT NOT NULL DEFAULT ''`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_mapping_rules_source_domain ON ontology_mapping_rules (source_domain)`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_mapping_rules_event_schema_version ON ontology_mapping_rules (event_schema_version)`,
			},
		},
	}
}

func ontologyMetadataProposalMigration() migration.Migration {
	return migration.Migration{
		Version:     "0102_ontology_metadata_proposals",
		Description: "create ontology metadata proposal table",
		Statements: []string{
			ontologyMetadataProposalsTableSQL("TEXT", "INTEGER"),
			`CREATE INDEX IF NOT EXISTS idx_ontology_metadata_proposals_target ON ontology_metadata_proposals (tenant_id, target_kind, target_id, metadata_key)`,
			`CREATE INDEX IF NOT EXISTS idx_ontology_metadata_proposals_status ON ontology_metadata_proposals (tenant_id, status, created_at)`,
		},
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: {
				ontologyMetadataProposalsTableSQL("JSONB", "BIGINT"),
				`CREATE INDEX IF NOT EXISTS idx_ontology_metadata_proposals_target ON ontology_metadata_proposals (tenant_id, target_kind, target_id, metadata_key)`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_metadata_proposals_status ON ontology_metadata_proposals (tenant_id, status, created_at)`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_metadata_proposals_evidence_gin ON ontology_metadata_proposals USING GIN (evidence_refs_json)`,
			},
		},
	}
}

func ontologyMetadataProposalReviewMigration() migration.Migration {
	return migration.Migration{
		Version:     "0103_ontology_metadata_proposal_review",
		Description: "add review metadata to ontology metadata proposals",
		Statements: []string{
			`ALTER TABLE ontology_metadata_proposals ADD COLUMN previous_status TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE ontology_metadata_proposals ADD COLUMN reviewed_by TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE ontology_metadata_proposals ADD COLUMN reviewed_by_kind TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE ontology_metadata_proposals ADD COLUMN reviewed_at INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE ontology_metadata_proposals ADD COLUMN review_reason TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE ontology_metadata_proposals ADD COLUMN review_evidence_refs_json TEXT NOT NULL DEFAULT '[]'`,
			`CREATE INDEX IF NOT EXISTS idx_ontology_metadata_proposals_reviewed ON ontology_metadata_proposals (tenant_id, reviewed_by, reviewed_at)`,
		},
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: {
				`ALTER TABLE ontology_metadata_proposals ADD COLUMN previous_status TEXT NOT NULL DEFAULT ''`,
				`ALTER TABLE ontology_metadata_proposals ADD COLUMN reviewed_by TEXT NOT NULL DEFAULT ''`,
				`ALTER TABLE ontology_metadata_proposals ADD COLUMN reviewed_by_kind TEXT NOT NULL DEFAULT ''`,
				`ALTER TABLE ontology_metadata_proposals ADD COLUMN reviewed_at BIGINT NOT NULL DEFAULT 0`,
				`ALTER TABLE ontology_metadata_proposals ADD COLUMN review_reason TEXT NOT NULL DEFAULT ''`,
				`ALTER TABLE ontology_metadata_proposals ADD COLUMN review_evidence_refs_json JSONB NOT NULL DEFAULT '[]'`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_metadata_proposals_reviewed ON ontology_metadata_proposals (tenant_id, reviewed_by, reviewed_at)`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_metadata_proposals_review_evidence_gin ON ontology_metadata_proposals USING GIN (review_evidence_refs_json)`,
			},
		},
	}
}

func ontologyMetadataProposalSourceTaxonomyMigration() migration.Migration {
	return migration.Migration{
		Version:     "0104_ontology_metadata_proposal_source_taxonomy",
		Description: "add source taxonomy to ontology metadata proposals",
		Statements: []string{
			`ALTER TABLE ontology_metadata_proposals ADD COLUMN source_kind TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE ontology_metadata_proposals ADD COLUMN source_ref TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE ontology_metadata_proposals ADD COLUMN source_version TEXT NOT NULL DEFAULT ''`,
			`CREATE INDEX IF NOT EXISTS idx_ontology_metadata_proposals_source_kind ON ontology_metadata_proposals (tenant_id, source_kind, created_at)`,
		},
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: {
				`ALTER TABLE ontology_metadata_proposals ADD COLUMN source_kind TEXT NOT NULL DEFAULT ''`,
				`ALTER TABLE ontology_metadata_proposals ADD COLUMN source_ref TEXT NOT NULL DEFAULT ''`,
				`ALTER TABLE ontology_metadata_proposals ADD COLUMN source_version TEXT NOT NULL DEFAULT ''`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_metadata_proposals_source_kind ON ontology_metadata_proposals (tenant_id, source_kind, created_at)`,
			},
		},
	}
}

func ontologyActionBindingsMigration() migration.Migration {
	return migration.Migration{
		Version:     "0105_ontology_action_bindings",
		Description: "add native allowed actions to ontology link and property types",
		Statements: []string{
			`ALTER TABLE ontology_link_types ADD COLUMN allowed_actions_json TEXT NOT NULL DEFAULT '[]'`,
			`ALTER TABLE ontology_property_types ADD COLUMN allowed_actions_json TEXT NOT NULL DEFAULT '[]'`,
		},
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: {
				`ALTER TABLE ontology_link_types ADD COLUMN allowed_actions_json JSONB NOT NULL DEFAULT '[]'`,
				`ALTER TABLE ontology_property_types ADD COLUMN allowed_actions_json JSONB NOT NULL DEFAULT '[]'`,
			},
		},
	}
}

func ontologyMetadataProposalPromotionReceiptMigration() migration.Migration {
	return migration.Migration{
		Version:     "0106_ontology_metadata_promotion_receipts",
		Description: "create ontology metadata promotion receipt ledger",
		Statements: []string{
			ontologyMetadataPromotionReceiptsTableSQL("TEXT", "INTEGER"),
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_ontology_metadata_promotion_receipts_idempotency ON ontology_metadata_promotion_receipts (proposal_id, idempotency_key)`,
			`CREATE INDEX IF NOT EXISTS idx_ontology_metadata_promotion_receipts_proposal ON ontology_metadata_promotion_receipts (tenant_id, proposal_id, created_at)`,
			`CREATE INDEX IF NOT EXISTS idx_ontology_metadata_promotion_receipts_actor ON ontology_metadata_promotion_receipts (tenant_id, actor_id, created_at)`,
		},
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: {
				ontologyMetadataPromotionReceiptsTableSQL("JSONB", "BIGINT"),
				`CREATE UNIQUE INDEX IF NOT EXISTS idx_ontology_metadata_promotion_receipts_idempotency ON ontology_metadata_promotion_receipts (proposal_id, idempotency_key)`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_metadata_promotion_receipts_proposal ON ontology_metadata_promotion_receipts (tenant_id, proposal_id, created_at)`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_metadata_promotion_receipts_actor ON ontology_metadata_promotion_receipts (tenant_id, actor_id, created_at)`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_metadata_promotion_receipts_changes_gin ON ontology_metadata_promotion_receipts USING GIN (changes_json)`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_metadata_promotion_receipts_issues_gin ON ontology_metadata_promotion_receipts USING GIN (issues_json)`,
			},
		},
	}
}

func ontologyMetadataPromotionReceiptRequestHashMigration() migration.Migration {
	return migration.Migration{
		Version:     "0107_ontology_metadata_promotion_receipt_request_hash",
		Description: "add request fingerprint to ontology metadata promotion receipts",
		Statements: []string{
			`ALTER TABLE ontology_metadata_promotion_receipts ADD COLUMN request_hash TEXT NOT NULL DEFAULT ''`,
			`CREATE INDEX IF NOT EXISTS idx_ontology_metadata_promotion_receipts_request_hash ON ontology_metadata_promotion_receipts (tenant_id, proposal_id, idempotency_key, request_hash)`,
		},
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: {
				`ALTER TABLE ontology_metadata_promotion_receipts ADD COLUMN request_hash TEXT NOT NULL DEFAULT ''`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_metadata_promotion_receipts_request_hash ON ontology_metadata_promotion_receipts (tenant_id, proposal_id, idempotency_key, request_hash)`,
			},
		},
	}
}

func ontologyMetadataPromotionReceiptTenantIdempotencyMigration() migration.Migration {
	return migration.Migration{
		Version:     "0108_ontology_metadata_promotion_receipt_tenant_idempotency",
		Description: "make ontology metadata promotion receipt idempotency tenant-aware",
		Statements: []string{
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_ontology_metadata_promotion_receipts_tenant_idempotency ON ontology_metadata_promotion_receipts (tenant_id, proposal_id, idempotency_key)`,
			`DROP INDEX IF EXISTS idx_ontology_metadata_promotion_receipts_idempotency`,
		},
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: {
				`CREATE UNIQUE INDEX IF NOT EXISTS idx_ontology_metadata_promotion_receipts_tenant_idempotency ON ontology_metadata_promotion_receipts (tenant_id, proposal_id, idempotency_key)`,
				`DROP INDEX IF EXISTS idx_ontology_metadata_promotion_receipts_idempotency`,
			},
		},
	}
}

func ontologyModelRunLedgerMigration() migration.Migration {
	return migration.Migration{
		Version:     "0109_ontology_model_run_ledger",
		Description: "create ontology model inference run ledger",
		Statements: []string{
			ontologyModelRunsTableSQL("INTEGER"),
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_ontology_model_runs_tenant_run ON ontology_model_runs (tenant_id, engine, run_id)`,
			`CREATE INDEX IF NOT EXISTS idx_ontology_model_runs_status ON ontology_model_runs (tenant_id, status, recorded_at)`,
		},
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: {
				ontologyModelRunsTableSQL("BIGINT"),
				`CREATE UNIQUE INDEX IF NOT EXISTS idx_ontology_model_runs_tenant_run ON ontology_model_runs (tenant_id, engine, run_id)`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_model_runs_status ON ontology_model_runs (tenant_id, status, recorded_at)`,
			},
		},
	}
}

func ontologyModelRunEvaluationArtifactMigration() migration.Migration {
	return migration.Migration{
		Version:     "0110_ontology_model_run_evaluation_artifacts",
		Description: "create ontology model run evaluation artifact references",
		Statements: []string{
			ontologyModelRunEvaluationArtifactsTableSQL("TEXT", "INTEGER"),
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_ontology_model_run_eval_artifacts_key ON ontology_model_run_evaluation_artifacts (tenant_id, engine, run_id, artifact_kind, artifact_uri)`,
			`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_artifacts_run ON ontology_model_run_evaluation_artifacts (tenant_id, engine, run_id, recorded_at)`,
			`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_artifacts_kind ON ontology_model_run_evaluation_artifacts (tenant_id, artifact_kind, recorded_at)`,
		},
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: {
				ontologyModelRunEvaluationArtifactsTableSQL("JSONB", "BIGINT"),
				`CREATE UNIQUE INDEX IF NOT EXISTS idx_ontology_model_run_eval_artifacts_key ON ontology_model_run_evaluation_artifacts (tenant_id, engine, run_id, artifact_kind, artifact_uri)`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_artifacts_run ON ontology_model_run_evaluation_artifacts (tenant_id, engine, run_id, recorded_at)`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_artifacts_kind ON ontology_model_run_evaluation_artifacts (tenant_id, artifact_kind, recorded_at)`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_artifacts_metadata_gin ON ontology_model_run_evaluation_artifacts USING GIN (metadata_json)`,
			},
		},
	}
}

func ontologyModelRunEvaluationPolicyProfileMigration() migration.Migration {
	return migration.Migration{
		Version:     "0111_ontology_model_run_evaluation_policy_profiles",
		Description: "create ontology model run evaluation policy profiles",
		Statements: []string{
			ontologyModelRunEvaluationPolicyProfilesTableSQL("TEXT", "INTEGER"),
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_ontology_model_run_eval_policy_profiles_scope ON ontology_model_run_evaluation_policy_profiles (tenant_id, engine, model, model_version)`,
			`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_policy_profiles_enabled ON ontology_model_run_evaluation_policy_profiles (tenant_id, engine, enabled, updated_at)`,
		},
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: {
				ontologyModelRunEvaluationPolicyProfilesTableSQL("JSONB", "BIGINT"),
				`CREATE UNIQUE INDEX IF NOT EXISTS idx_ontology_model_run_eval_policy_profiles_scope ON ontology_model_run_evaluation_policy_profiles (tenant_id, engine, model, model_version)`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_policy_profiles_enabled ON ontology_model_run_evaluation_policy_profiles (tenant_id, engine, enabled, updated_at)`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_policy_profiles_metadata_gin ON ontology_model_run_evaluation_policy_profiles USING GIN (metadata_json)`,
			},
		},
	}
}

func ontologyModelRunEvaluationPolicyProfileMetricThresholdMigration() migration.Migration {
	return migration.Migration{
		Version:     "0112_ontology_model_run_evaluation_policy_profile_metric_thresholds",
		Description: "add metric thresholds to ontology model run evaluation policy profiles",
		Statements: []string{
			`ALTER TABLE ontology_model_run_evaluation_policy_profiles ADD COLUMN metric_thresholds_json TEXT NOT NULL DEFAULT '[]'`,
		},
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: {
				`ALTER TABLE ontology_model_run_evaluation_policy_profiles ADD COLUMN metric_thresholds_json JSONB NOT NULL DEFAULT '[]'`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_policy_profiles_thresholds_gin ON ontology_model_run_evaluation_policy_profiles USING GIN (metric_thresholds_json)`,
			},
		},
	}
}

func ontologyModelRunEvaluationAlertAuditMigration() migration.Migration {
	return migration.Migration{
		Version:     "0113_ontology_model_run_evaluation_alert_audits",
		Description: "create ontology model run evaluation alert audit ledger",
		Statements: []string{
			ontologyModelRunEvaluationAlertAuditsTableSQL("TEXT", "INTEGER"),
			`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_alert_audits_scope ON ontology_model_run_evaluation_alert_audits (tenant_id, rule_id, observed_at)`,
			`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_alert_audits_status ON ontology_model_run_evaluation_alert_audits (tenant_id, status, observed_at)`,
		},
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: {
				ontologyModelRunEvaluationAlertAuditsTableSQL("JSONB", "BIGINT"),
				`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_alert_audits_scope ON ontology_model_run_evaluation_alert_audits (tenant_id, rule_id, observed_at)`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_alert_audits_status ON ontology_model_run_evaluation_alert_audits (tenant_id, status, observed_at)`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_alert_audits_labels_gin ON ontology_model_run_evaluation_alert_audits USING GIN (labels_json)`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_alert_audits_details_gin ON ontology_model_run_evaluation_alert_audits USING GIN (details_json)`,
			},
		},
	}
}

func ontologyModelRunEvaluationMetricRecordingMigration() migration.Migration {
	return migration.Migration{
		Version:     "0114_ontology_model_run_evaluation_metric_recordings",
		Description: "create ontology model run evaluation metric recordings",
		Statements: []string{
			ontologyModelRunEvaluationMetricRecordingsTableSQL("TEXT", "INTEGER"),
			`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_metric_recordings_scope ON ontology_model_run_evaluation_metric_recordings (tenant_id, engine, recorded_at)`,
			`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_metric_recordings_metric ON ontology_model_run_evaluation_metric_recordings (tenant_id, metric_name, recorded_at)`,
		},
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: {
				ontologyModelRunEvaluationMetricRecordingsTableSQL("JSONB", "BIGINT"),
				`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_metric_recordings_scope ON ontology_model_run_evaluation_metric_recordings (tenant_id, engine, recorded_at)`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_metric_recordings_metric ON ontology_model_run_evaluation_metric_recordings (tenant_id, metric_name, recorded_at)`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_metric_recordings_labels_gin ON ontology_model_run_evaluation_metric_recordings USING GIN (labels_json)`,
			},
		},
	}
}

func ontologyModelRunEvaluationAlertNotificationHistoryMigration() migration.Migration {
	return migration.Migration{
		Version:     "0115_ontology_model_run_evaluation_alert_notification_history",
		Description: "create ontology model run evaluation alert notification history",
		Statements: []string{
			ontologyModelRunEvaluationAlertNotificationHistoryTableSQL("TEXT", "INTEGER"),
			`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_alert_notification_history_scope ON ontology_model_run_evaluation_alert_notification_history (tenant_id, rule_id, updated_at)`,
		},
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: {
				ontologyModelRunEvaluationAlertNotificationHistoryTableSQL("JSONB", "BIGINT"),
				`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_alert_notification_history_scope ON ontology_model_run_evaluation_alert_notification_history (tenant_id, rule_id, updated_at)`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_alert_notification_history_labels_gin ON ontology_model_run_evaluation_alert_notification_history USING GIN (labels_json)`,
			},
		},
	}
}

func ontologyModelRunEvaluationAlertNotificationReceiptMigration() migration.Migration {
	return migration.Migration{
		Version:     "0116_ontology_model_run_evaluation_alert_notification_receipts",
		Description: "create ontology model run evaluation alert notification receipt ledger",
		Statements: []string{
			ontologyModelRunEvaluationAlertNotificationReceiptsTableSQL("TEXT", "INTEGER"),
			`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_alert_notification_receipts_scope ON ontology_model_run_evaluation_alert_notification_receipts (tenant_id, rule_id, created_at)`,
			`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_alert_notification_receipts_status ON ontology_model_run_evaluation_alert_notification_receipts (tenant_id, delivery_status, created_at)`,
			`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_alert_notification_receipts_channel ON ontology_model_run_evaluation_alert_notification_receipts (tenant_id, channel, created_at)`,
			`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_alert_notification_receipts_retry ON ontology_model_run_evaluation_alert_notification_receipts (tenant_id, retryable, next_retry_at)`,
		},
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: {
				ontologyModelRunEvaluationAlertNotificationReceiptsTableSQL("JSONB", "BIGINT"),
				`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_alert_notification_receipts_scope ON ontology_model_run_evaluation_alert_notification_receipts (tenant_id, rule_id, created_at)`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_alert_notification_receipts_status ON ontology_model_run_evaluation_alert_notification_receipts (tenant_id, delivery_status, created_at)`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_alert_notification_receipts_channel ON ontology_model_run_evaluation_alert_notification_receipts (tenant_id, channel, created_at)`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_alert_notification_receipts_retry ON ontology_model_run_evaluation_alert_notification_receipts (tenant_id, retryable, next_retry_at)`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_alert_notification_receipts_labels_gin ON ontology_model_run_evaluation_alert_notification_receipts USING GIN (labels_json)`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_alert_notification_receipts_metadata_gin ON ontology_model_run_evaluation_alert_notification_receipts USING GIN (metadata_json)`,
			},
		},
	}
}

func ontologyModelRunEvaluationAlertNotificationPolicyMigration() migration.Migration {
	return migration.Migration{
		Version:     "0117_ontology_model_run_evaluation_alert_notification_policies",
		Description: "create ontology model run evaluation alert notification policies",
		Statements: []string{
			ontologyModelRunEvaluationAlertNotificationPoliciesTableSQL("TEXT", "INTEGER"),
			`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_alert_notification_policies_scope ON ontology_model_run_evaluation_alert_notification_policies (tenant_id, rule_id, enabled)`,
			`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_alert_notification_policies_channel ON ontology_model_run_evaluation_alert_notification_policies (tenant_id, channel, enabled)`,
		},
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: {
				ontologyModelRunEvaluationAlertNotificationPoliciesTableSQL("JSONB", "BIGINT"),
				`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_alert_notification_policies_scope ON ontology_model_run_evaluation_alert_notification_policies (tenant_id, rule_id, enabled)`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_alert_notification_policies_channel ON ontology_model_run_evaluation_alert_notification_policies (tenant_id, channel, enabled)`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_model_run_eval_alert_notification_policies_headers_gin ON ontology_model_run_evaluation_alert_notification_policies USING GIN (headers_json)`,
			},
		},
	}
}

func ontologyObjectTypesTableSQL(jsonType string, timeType string) string {
	return `CREATE TABLE IF NOT EXISTS ontology_object_types (
		id TEXT PRIMARY KEY,
		tenant_id TEXT NOT NULL DEFAULT '',
		tenant_scope TEXT NOT NULL,
		display_name TEXT NOT NULL,
		canonical_key_rule TEXT NOT NULL,
		owner_domain TEXT NOT NULL DEFAULT '',
		sensitivity TEXT NOT NULL DEFAULT '',
		lifecycle TEXT NOT NULL DEFAULT '',
		properties_json ` + jsonType + ` NOT NULL DEFAULT '[]',
		allowed_link_types_json ` + jsonType + ` NOT NULL DEFAULT '[]',
		allowed_actions_json ` + jsonType + ` NOT NULL DEFAULT '[]',
		version INTEGER NOT NULL DEFAULT 1,
		created_at ` + timeType + ` NOT NULL,
		updated_at ` + timeType + ` NOT NULL
	)`
}

func ontologyLinkTypesTableSQL(timeType string) string {
	return `CREATE TABLE IF NOT EXISTS ontology_link_types (
		id TEXT PRIMARY KEY,
		tenant_id TEXT NOT NULL DEFAULT '',
		tenant_scope TEXT NOT NULL,
		display_name TEXT NOT NULL DEFAULT '',
		from_object_type TEXT NOT NULL,
		to_object_type TEXT NOT NULL,
		cardinality TEXT NOT NULL,
		evidence_required BOOLEAN NOT NULL DEFAULT FALSE,
		suggested_allowed BOOLEAN NOT NULL DEFAULT FALSE,
		version INTEGER NOT NULL DEFAULT 1,
		created_at ` + timeType + ` NOT NULL,
		updated_at ` + timeType + ` NOT NULL
	)`
}

func ontologyPropertyTypesTableSQL(timeType string) string {
	return `CREATE TABLE IF NOT EXISTS ontology_property_types (
		id TEXT PRIMARY KEY,
		tenant_id TEXT NOT NULL DEFAULT '',
		object_type_id TEXT NOT NULL,
		property_id TEXT NOT NULL,
		display_name TEXT NOT NULL DEFAULT '',
		value_type TEXT NOT NULL,
		authority_source TEXT NOT NULL DEFAULT '',
		conflict_policy TEXT NOT NULL DEFAULT '',
		sensitivity TEXT NOT NULL DEFAULT '',
		version INTEGER NOT NULL DEFAULT 1,
		created_at ` + timeType + ` NOT NULL,
		updated_at ` + timeType + ` NOT NULL
	)`
}

func ontologyMappingRulesTableSQL(jsonType string, timeType string) string {
	return `CREATE TABLE IF NOT EXISTS ontology_mapping_rules (
		id TEXT PRIMARY KEY,
		tenant_id TEXT NOT NULL DEFAULT '',
		event_type TEXT NOT NULL,
		object_types_json ` + jsonType + ` NOT NULL DEFAULT '[]',
		link_types_json ` + jsonType + ` NOT NULL DEFAULT '[]',
		rule_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		enabled BOOLEAN NOT NULL DEFAULT TRUE,
		version INTEGER NOT NULL DEFAULT 1,
		created_at ` + timeType + ` NOT NULL,
		updated_at ` + timeType + ` NOT NULL
	)`
}

func ontologyMetadataProposalsTableSQL(jsonType string, timeType string) string {
	return `CREATE TABLE IF NOT EXISTS ontology_metadata_proposals (
		id TEXT PRIMARY KEY,
		tenant_id TEXT NOT NULL DEFAULT '',
		target_kind TEXT NOT NULL,
		target_id TEXT NOT NULL,
		metadata_key TEXT NOT NULL,
		proposed_value TEXT NOT NULL,
		source TEXT NOT NULL,
		confidence REAL NOT NULL DEFAULT 1,
		status TEXT NOT NULL DEFAULT 'proposed',
		reason TEXT NOT NULL DEFAULT '',
		evidence_refs_json ` + jsonType + ` NOT NULL DEFAULT '[]',
		created_at ` + timeType + ` NOT NULL,
		updated_at ` + timeType + ` NOT NULL
	)`
}

func ontologyMetadataPromotionReceiptsTableSQL(jsonType string, timeType string) string {
	return `CREATE TABLE IF NOT EXISTS ontology_metadata_promotion_receipts (
		receipt_id TEXT PRIMARY KEY,
		tenant_id TEXT NOT NULL DEFAULT '',
		proposal_id TEXT NOT NULL,
		actor_id TEXT NOT NULL,
		actor_kind TEXT NOT NULL DEFAULT '',
		idempotency_key TEXT NOT NULL,
		status TEXT NOT NULL,
		applied BOOLEAN NOT NULL DEFAULT FALSE,
		changes_json ` + jsonType + ` NOT NULL DEFAULT '[]',
		issues_json ` + jsonType + ` NOT NULL DEFAULT '[]',
		error_message TEXT NOT NULL DEFAULT '',
		applied_at ` + timeType + ` NOT NULL,
		created_at ` + timeType + ` NOT NULL,
		updated_at ` + timeType + ` NOT NULL
	)`
}

func ontologyModelRunsTableSQL(timeType string) string {
	return `CREATE TABLE IF NOT EXISTS ontology_model_runs (
		id TEXT PRIMARY KEY,
		tenant_id TEXT NOT NULL DEFAULT '',
		engine TEXT NOT NULL,
		model TEXT NOT NULL,
		model_version TEXT NOT NULL,
		run_id TEXT NOT NULL,
		input_hash TEXT NOT NULL,
		prompt_hash TEXT NOT NULL,
		source_ref TEXT NOT NULL DEFAULT '',
		source_version TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL,
		candidate_count INTEGER NOT NULL DEFAULT 0,
		valid_candidate_count INTEGER NOT NULL DEFAULT 0,
		skipped_candidate_count INTEGER NOT NULL DEFAULT 0,
		proposal_count INTEGER NOT NULL DEFAULT 0,
		analysis_total INTEGER NOT NULL DEFAULT 0,
		analysis_blocking_count INTEGER NOT NULL DEFAULT 0,
		analysis_warning_count INTEGER NOT NULL DEFAULT 0,
		error_message TEXT NOT NULL DEFAULT '',
		recorded_at ` + timeType + ` NOT NULL,
		created_at ` + timeType + ` NOT NULL,
		updated_at ` + timeType + ` NOT NULL
	)`
}

func ontologyModelRunEvaluationArtifactsTableSQL(jsonType string, timeType string) string {
	return `CREATE TABLE IF NOT EXISTS ontology_model_run_evaluation_artifacts (
		id TEXT PRIMARY KEY,
		tenant_id TEXT NOT NULL,
		engine TEXT NOT NULL,
		run_id TEXT NOT NULL,
		artifact_kind TEXT NOT NULL,
		artifact_uri TEXT NOT NULL,
		artifact_hash TEXT NOT NULL DEFAULT '',
		summary TEXT NOT NULL DEFAULT '',
		metadata_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		recorded_at ` + timeType + ` NOT NULL,
		created_at ` + timeType + ` NOT NULL,
		updated_at ` + timeType + ` NOT NULL
	)`
}

func ontologyModelRunEvaluationPolicyProfilesTableSQL(jsonType string, timeType string) string {
	return `CREATE TABLE IF NOT EXISTS ontology_model_run_evaluation_policy_profiles (
		id TEXT PRIMARY KEY,
		tenant_id TEXT NOT NULL,
		engine TEXT NOT NULL,
		model TEXT NOT NULL DEFAULT '',
		model_version TEXT NOT NULL DEFAULT '',
		required_artifact_kind TEXT NOT NULL,
		required_dataset_ref TEXT NOT NULL DEFAULT '',
		required_policy_version TEXT NOT NULL,
		enabled BOOLEAN NOT NULL DEFAULT TRUE,
		metadata_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		created_at ` + timeType + ` NOT NULL,
		updated_at ` + timeType + ` NOT NULL
	)`
}

func ontologyModelRunEvaluationAlertAuditsTableSQL(jsonType string, timeType string) string {
	return `CREATE TABLE IF NOT EXISTS ontology_model_run_evaluation_alert_audits (
		id TEXT PRIMARY KEY,
		tenant_id TEXT NOT NULL,
		event_type TEXT NOT NULL,
		rule_id TEXT NOT NULL,
		severity TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL,
		labels_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		details_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		observed_at ` + timeType + ` NOT NULL,
		created_at ` + timeType + ` NOT NULL,
		updated_at ` + timeType + ` NOT NULL
	)`
}

func ontologyModelRunEvaluationMetricRecordingsTableSQL(jsonType string, timeType string) string {
	return `CREATE TABLE IF NOT EXISTS ontology_model_run_evaluation_metric_recordings (
		id TEXT PRIMARY KEY,
		tenant_id TEXT NOT NULL,
		engine TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		model_version TEXT NOT NULL DEFAULT '',
		metric_name TEXT NOT NULL,
		value REAL NOT NULL DEFAULT 0,
		labels_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		recorded_at ` + timeType + ` NOT NULL,
		created_at ` + timeType + ` NOT NULL,
		updated_at ` + timeType + ` NOT NULL
	)`
}

func ontologyModelRunEvaluationAlertNotificationHistoryTableSQL(jsonType string, timeType string) string {
	return `CREATE TABLE IF NOT EXISTS ontology_model_run_evaluation_alert_notification_history (
		id TEXT PRIMARY KEY,
		tenant_id TEXT NOT NULL,
		rule_id TEXT NOT NULL,
		labels_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		last_notification_status TEXT NOT NULL,
		last_notification_at ` + timeType + ` NOT NULL,
		created_at ` + timeType + ` NOT NULL,
		updated_at ` + timeType + ` NOT NULL
	)`
}

func ontologyModelRunEvaluationAlertNotificationReceiptsTableSQL(jsonType string, timeType string) string {
	return `CREATE TABLE IF NOT EXISTS ontology_model_run_evaluation_alert_notification_receipts (
		id TEXT PRIMARY KEY,
		tenant_id TEXT NOT NULL,
		rule_id TEXT NOT NULL,
		delivery_status TEXT NOT NULL,
		alert_status TEXT NOT NULL,
		reason TEXT NOT NULL DEFAULT '',
		channel TEXT NOT NULL DEFAULT '',
		message_id TEXT NOT NULL DEFAULT '',
		attempt INTEGER NOT NULL DEFAULT 1,
		retryable BOOLEAN NOT NULL DEFAULT FALSE,
		next_retry_at ` + timeType + ` NOT NULL DEFAULT 0,
		labels_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		metadata_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		error_message TEXT NOT NULL DEFAULT '',
		sent_at ` + timeType + ` NOT NULL,
		created_at ` + timeType + ` NOT NULL,
		updated_at ` + timeType + ` NOT NULL
	)`
}

func ontologyModelRunEvaluationAlertNotificationPoliciesTableSQL(jsonType string, timeType string) string {
	return `CREATE TABLE IF NOT EXISTS ontology_model_run_evaluation_alert_notification_policies (
		id TEXT PRIMARY KEY,
		tenant_id TEXT NOT NULL,
		rule_id TEXT NOT NULL DEFAULT '',
		channel TEXT NOT NULL,
		endpoint TEXT NOT NULL DEFAULT '',
		headers_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		repeat_window_seconds INTEGER NOT NULL DEFAULT 0,
		recovery_window_seconds INTEGER NOT NULL DEFAULT 0,
		max_attempts INTEGER NOT NULL DEFAULT 3,
		enabled BOOLEAN NOT NULL DEFAULT TRUE,
		created_at ` + timeType + ` NOT NULL,
		updated_at ` + timeType + ` NOT NULL
	)`
}
