package knowledgeinstances

import (
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func RegisterMigrations(c *migration.Coordinator) error {
	if c == nil {
		return nil
	}
	if err := c.RegisterInitializer(db.RoleCore, migration.Migration{
		Version: "0241_ontology_document_instances", Description: "document watches, model previews and source-owned knowledge instances",
		Statements: []string{
			`CREATE TABLE IF NOT EXISTS ontology_document_watches (tenant_id TEXT NOT NULL,id TEXT NOT NULL,agent_id TEXT NOT NULL,version BIGINT NOT NULL,active BOOLEAN NOT NULL,body_json TEXT NOT NULL,observed_stamp TEXT NOT NULL DEFAULT '',sequence BIGINT NOT NULL DEFAULT 0,next_check_at BIGINT NOT NULL DEFAULT 0,PRIMARY KEY(tenant_id,id))`,
			`CREATE INDEX IF NOT EXISTS idx_ontology_document_watches_due ON ontology_document_watches(active,next_check_at,tenant_id,id)`,
			`CREATE TABLE IF NOT EXISTS ontology_document_watch_versions (tenant_id TEXT NOT NULL,watch_id TEXT NOT NULL,version BIGINT NOT NULL,body_json TEXT NOT NULL,PRIMARY KEY(tenant_id,watch_id,version))`,
			`CREATE TABLE IF NOT EXISTS ontology_document_batches (tenant_id TEXT NOT NULL,id TEXT NOT NULL,watch_id TEXT NOT NULL,status TEXT NOT NULL,body_json TEXT NOT NULL,created_at BIGINT NOT NULL,PRIMARY KEY(tenant_id,id))`,
			`CREATE INDEX IF NOT EXISTS idx_ontology_document_batches_watch ON ontology_document_batches(tenant_id,watch_id,id)`,
			`CREATE TABLE IF NOT EXISTS knowledge_instances (tenant_id TEXT NOT NULL,id TEXT NOT NULL,access_hash TEXT NOT NULL,version BIGINT NOT NULL,body_json TEXT NOT NULL,PRIMARY KEY(tenant_id,id))`,
			`CREATE INDEX IF NOT EXISTS idx_knowledge_instances_access ON knowledge_instances(tenant_id,access_hash,id)`,
			`CREATE TABLE IF NOT EXISTS knowledge_instance_claims (tenant_id TEXT NOT NULL,watch_id TEXT NOT NULL,id TEXT NOT NULL,entity_id TEXT NOT NULL,body_json TEXT NOT NULL,PRIMARY KEY(tenant_id,id))`,
			`CREATE INDEX IF NOT EXISTS idx_knowledge_instance_claims_entity ON knowledge_instance_claims(tenant_id,entity_id,watch_id)`,
			`CREATE TABLE IF NOT EXISTS ontology_document_receipts (tenant_id TEXT NOT NULL,batch_id TEXT NOT NULL,plan_hash TEXT NOT NULL,body_json TEXT NOT NULL,PRIMARY KEY(tenant_id,batch_id))`,
		},
	}); err != nil {
		return err
	}
	if err := c.RegisterInitializer(db.RoleCore, migration.Migration{Version: "0244_ontology_source_withdrawals", Description: "durable source withdrawal command receipts", Statements: []string{
		`CREATE TABLE IF NOT EXISTS ontology_document_withdrawals (tenant_id TEXT NOT NULL,event_id TEXT NOT NULL,input_hash TEXT NOT NULL,body_json TEXT NOT NULL,PRIMARY KEY(tenant_id,event_id))`,
		`CREATE TABLE IF NOT EXISTS knowledge_instance_claim_sources (tenant_id TEXT NOT NULL,claim_id TEXT NOT NULL,knowledge_base_id TEXT NOT NULL,document_id TEXT NOT NULL,PRIMARY KEY(tenant_id,claim_id),FOREIGN KEY(tenant_id,claim_id) REFERENCES knowledge_instance_claims(tenant_id,id) ON DELETE CASCADE)`,
		`CREATE INDEX IF NOT EXISTS idx_knowledge_instance_claim_sources_document ON knowledge_instance_claim_sources(tenant_id,knowledge_base_id,document_id,claim_id)`,
	}}); err != nil {
		return err
	}
	if err := c.RegisterInitializer(db.RoleCore, identityMigration()); err != nil {
		return err
	}
	return c.RegisterInitializer(db.RoleCore, migration.Migration{Version: "0246_ontology_assertion_time", Description: "durable assertion validity boundaries and atomic maintenance receipts", Statements: []string{
		`CREATE TABLE IF NOT EXISTS ontology_document_boundaries (tenant_id TEXT NOT NULL,watch_id TEXT NOT NULL,watch_version BIGINT NOT NULL,plan_hash TEXT NOT NULL,source_stamp TEXT NOT NULL,due_at BIGINT NOT NULL,dispatch_after BIGINT NOT NULL,sequence BIGINT NOT NULL DEFAULT 0,PRIMARY KEY(tenant_id,watch_id))`,
		`CREATE INDEX IF NOT EXISTS idx_ontology_document_boundaries_due ON ontology_document_boundaries(dispatch_after,due_at,tenant_id,watch_id)`,
		`CREATE TABLE IF NOT EXISTS ontology_boundary_receipts (tenant_id TEXT NOT NULL,event_id TEXT NOT NULL,body_json TEXT NOT NULL,PRIMARY KEY(tenant_id,event_id))`,
	}})
}
