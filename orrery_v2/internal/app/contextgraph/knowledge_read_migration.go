package contextgraph

import "orrery_v2/internal/platform/migration"

func contextGraphKnowledgeReadIndexMigration() migration.Migration {
	return migration.Migration{
		Version:     "0213_context_graph_knowledge_read_indexes",
		Description: "index knowledge document metadata and revision relation reads",
		Statements: []string{
			`CREATE INDEX IF NOT EXISTS idx_context_graph_knowledge_metadata ON context_graph_facts (tenant_id, LOWER(subject_kind), LOWER(TRIM(predicate)), subject_id) WHERE LOWER(subject_kind) = 'knowledge_document' AND LOWER(TRIM(predicate)) IN ('provider_document_id', 'knowledge_base_id')`,
			`CREATE INDEX IF NOT EXISTS idx_context_graph_links_source_stream ON context_graph_links (tenant_id, source_stream_id, id)`,
		},
	}
}
