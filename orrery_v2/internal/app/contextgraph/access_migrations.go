package contextgraph

import (
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func contextGraphAccessRefBoundsMigration() migration.Migration {
	sqliteByteLength := func(column string) string {
		return "length(CAST(" + column + " AS BLOB))"
	}
	postgresByteLength := func(column string) string {
		return "octet_length(" + column + ")"
	}
	return migration.Migration{
		Version:     "0204_context_graph_access_ref_bounds",
		Description: "conceal context graph resources whose authorization references exceed policy limits",
		Statements:  contextGraphAccessRefBoundsStatements(sqliteByteLength),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: contextGraphAccessRefBoundsStatements(postgresByteLength),
		},
	}
}

func contextGraphAccessRefBoundsStatements(byteLength func(string) string) []string {
	invalid := func(kind, id, tenantID string) string {
		return byteLength(kind) + " > 64 OR " + byteLength(id) + " > 512 OR " + byteLength(tenantID) + " > 256"
	}
	return []string{
		`UPDATE context_graph_objects SET access_state = 'unresolved' WHERE access_state = 'resolved' AND (` + invalid("owner_access_kind", "owner_access_id", "owner_access_tenant_id") + `)`,
		`UPDATE context_graph_links SET access_state = 'unresolved' WHERE access_state = 'resolved' AND (` + invalid("source_access_kind", "source_access_id", "source_access_tenant_id") + `)`,
		`UPDATE context_graph_facts SET access_state = 'unresolved' WHERE access_state = 'resolved' AND (` + invalid("source_access_kind", "source_access_id", "source_access_tenant_id") + `)`,
		`UPDATE context_graph_fact_assertions SET access_state = 'unresolved' WHERE access_state = 'resolved' AND (` + invalid("source_access_kind", "source_access_id", "source_access_tenant_id") + `)`,
		`UPDATE context_graph_evidence SET access_state = 'unresolved' WHERE access_state = 'resolved' AND (` + invalid("source_access_kind", "source_access_id", "source_access_tenant_id") + `)`,
		`UPDATE context_graph_projection_audits SET access_state = 'unresolved' WHERE access_state = 'resolved' AND (` + invalid("source_access_kind", "source_access_id", "source_access_tenant_id") + `)`,
		`UPDATE context_graph_projection_audit_replay_history SET access_state = 'unresolved' WHERE access_state = 'resolved' AND (` + invalid("source_access_kind", "source_access_id", "source_access_tenant_id") + `)`,
		`UPDATE context_graph_resource_versions SET access_state = 'unresolved' WHERE access_state = 'resolved' AND (` + invalid("access_kind", "access_id", "access_tenant_id") + `)`,
	}
}

func contextGraphAccessRefMigration() migration.Migration {
	statements := []string{
		`ALTER TABLE context_graph_objects ADD COLUMN owner_access_kind TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE context_graph_objects ADD COLUMN owner_access_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE context_graph_objects ADD COLUMN owner_access_tenant_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE context_graph_objects ADD COLUMN access_state TEXT NOT NULL DEFAULT 'unresolved'`,
		`ALTER TABLE context_graph_links ADD COLUMN source_access_kind TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE context_graph_links ADD COLUMN source_access_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE context_graph_links ADD COLUMN source_access_tenant_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE context_graph_links ADD COLUMN access_state TEXT NOT NULL DEFAULT 'unresolved'`,
		`ALTER TABLE context_graph_facts ADD COLUMN source_access_kind TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE context_graph_facts ADD COLUMN source_access_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE context_graph_facts ADD COLUMN source_access_tenant_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE context_graph_facts ADD COLUMN access_state TEXT NOT NULL DEFAULT 'unresolved'`,
		`ALTER TABLE context_graph_fact_assertions ADD COLUMN source_access_kind TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE context_graph_fact_assertions ADD COLUMN source_access_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE context_graph_fact_assertions ADD COLUMN source_access_tenant_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE context_graph_fact_assertions ADD COLUMN access_state TEXT NOT NULL DEFAULT 'unresolved'`,
		`ALTER TABLE context_graph_evidence ADD COLUMN source_access_kind TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE context_graph_evidence ADD COLUMN source_access_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE context_graph_evidence ADD COLUMN source_access_tenant_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE context_graph_evidence ADD COLUMN access_state TEXT NOT NULL DEFAULT 'unresolved'`,
		`CREATE INDEX IF NOT EXISTS idx_context_graph_objects_access ON context_graph_objects (tenant_id, access_state, owner_access_kind, owner_access_id)`,
		`CREATE INDEX IF NOT EXISTS idx_context_graph_links_access ON context_graph_links (tenant_id, access_state, source_access_kind, source_access_id)`,
		`CREATE INDEX IF NOT EXISTS idx_context_graph_facts_access ON context_graph_facts (tenant_id, access_state, source_access_kind, source_access_id)`,
		`CREATE INDEX IF NOT EXISTS idx_context_graph_assertions_access ON context_graph_fact_assertions (tenant_id, fact_id, access_state, observed_at, assertion_id)`,
		`CREATE INDEX IF NOT EXISTS idx_context_graph_evidence_access ON context_graph_evidence (tenant_id, access_state, source_access_kind, source_access_id)`,
	}
	return migration.Migration{
		Version:     "0161_context_graph_access_refs",
		Description: "persist source-domain authorization references for context graph resources",
		Statements:  statements,
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: append([]string(nil), statements...),
		},
	}
}

func contextGraphAuditAccessRefMigration() migration.Migration {
	statements := []string{
		`ALTER TABLE context_graph_projection_audits ADD COLUMN source_access_kind TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE context_graph_projection_audits ADD COLUMN source_access_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE context_graph_projection_audits ADD COLUMN source_access_tenant_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE context_graph_projection_audits ADD COLUMN access_state TEXT NOT NULL DEFAULT 'unresolved'`,
		`ALTER TABLE context_graph_projection_audit_replay_history ADD COLUMN source_access_kind TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE context_graph_projection_audit_replay_history ADD COLUMN source_access_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE context_graph_projection_audit_replay_history ADD COLUMN source_access_tenant_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE context_graph_projection_audit_replay_history ADD COLUMN access_state TEXT NOT NULL DEFAULT 'unresolved'`,
		`CREATE INDEX IF NOT EXISTS idx_context_graph_projection_audits_access ON context_graph_projection_audits (tenant_id, access_state, source_access_kind, source_access_id)`,
	}
	return migration.Migration{
		Version:     "0162_context_graph_audit_access_refs",
		Description: "persist source-domain authorization references for projection audits",
		Statements:  statements,
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: append([]string(nil), statements...),
		},
	}
}

func contextGraphAccessRefBackfillAuditMigration() migration.Migration {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS context_graph_access_ref_backfill_audit (
			run_id TEXT NOT NULL,
			resource_kind TEXT NOT NULL,
			tenant_id TEXT NOT NULL,
			resource_id TEXT NOT NULL,
			access_kind TEXT NOT NULL,
			access_id TEXT NOT NULL,
			access_tenant_id TEXT NOT NULL,
			source_kind TEXT NOT NULL DEFAULT '',
			source_id TEXT NOT NULL DEFAULT '',
			changed_by TEXT NOT NULL,
			changed_at BIGINT NOT NULL,
			PRIMARY KEY (run_id, resource_kind, tenant_id, resource_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_context_graph_access_ref_backfill_audit_resource ON context_graph_access_ref_backfill_audit (resource_kind, tenant_id, resource_id, changed_at)`,
		`CREATE INDEX IF NOT EXISTS idx_context_graph_objects_access_backfill ON context_graph_objects (access_state, tenant_id, id)`,
		`CREATE INDEX IF NOT EXISTS idx_context_graph_links_access_backfill ON context_graph_links (access_state, tenant_id, id)`,
		`CREATE INDEX IF NOT EXISTS idx_context_graph_facts_access_backfill ON context_graph_facts (access_state, tenant_id, id)`,
		`CREATE INDEX IF NOT EXISTS idx_context_graph_assertions_access_backfill ON context_graph_fact_assertions (access_state, tenant_id, assertion_id)`,
		`CREATE INDEX IF NOT EXISTS idx_context_graph_evidence_access_backfill ON context_graph_evidence (access_state, tenant_id, id)`,
		`CREATE INDEX IF NOT EXISTS idx_context_graph_projection_audits_access_backfill ON context_graph_projection_audits (access_state, tenant_id, id)`,
		`CREATE INDEX IF NOT EXISTS idx_context_graph_projection_replay_access_backfill ON context_graph_projection_audit_replay_history (access_state, tenant_id, id)`,
	}
	return migration.Migration{
		Version:     "0163_context_graph_access_ref_backfill_audit",
		Description: "audit deterministic context graph access reference backfills",
		Statements:  statements,
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: append([]string(nil), statements...),
		},
	}
}
