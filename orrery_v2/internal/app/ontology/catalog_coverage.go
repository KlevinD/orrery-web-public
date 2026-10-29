package ontology

// Catalog initialization is an operator-invoked production projection, not a
// live registration publisher. Keep that limitation explicit rather than
// claiming complete lifecycle coverage or extending a seeded-only exemption.
func catalogInitializationProductionCoverageRecords() []ProductionCoverageRecord {
	const publisherPath = "cmd/server/production_ontology_initialization_catalog_adapters.go"
	const testPath = "cmd/server/production_ontology_catalog_coverage_test.go"
	coverage := func(domain, eventType string, fields, graphAssertions []string) ProductionCoverageRecord {
		return partialCoverage(domain, "operator-invoked catalog initialization subset", eventType,
			fields, publishers(publisherPath),
			tests(testPath,
				failureTest(testPath, "catalog initialization rejects malformed identity before graph mutation"),
				replayTest(testPath, "repeated catalog initialization preserves object, fact and evidence identity"),
				tenantTest(testPath, "catalog snapshots preserve source tenant and system resource boundaries")),
			graphAssertions,
			gaps("operator-invoked current catalog snapshots only; normal registration, update, deletion and durable incremental delivery are not covered by this publisher"))
	}
	return []ProductionCoverageRecord{
		coverage("resource_admin", "resource.tool.registered", payload("id", "name", "risk_level"),
			assertions("object:tool", "fact:tool.name", "fact:tool.risk_level", "evidence:catalog-tool")),
		coverage("resource_admin", "resource.skill.registered", payload("id", "name", "description", "version", "risk_level", "enabled"),
			assertions("object:skill", "fact:skill.name", "fact:skill.version", "fact:skill.enabled", "evidence:catalog-skill")),
		coverage("resource_admin", "resource.mcp_server.registered", payload("id", "server_name", "transport"),
			assertions("object:mcp_server", "fact:mcp_server.server_name", "fact:mcp_server.transport", "evidence:catalog-mcp-server")),
		coverage("knowledge", "knowledge.base.created", payload("id", "name", "description", "owner_type", "owner_id", "visibility", "status"),
			assertions("object:knowledge_base", "fact:knowledge_base.name", "fact:knowledge_base.owner_id", "evidence:catalog-knowledge-base")),
		coverage("memory", "memory.profile.created", payload("id", "owner_agent_id", "memory_kind", "scope_kind", "scope_id", "enabled"),
			assertions("object:memory_profile", "fact:memory_profile.owner_agent_id", "fact:memory_profile.enabled", "evidence:catalog-memory-profile")),
	}
}
