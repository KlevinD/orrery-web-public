# Ontology catalog projection coverage

The production coverage manifest distinguishes an operator-invoked catalog
snapshot from live business mutation delivery. The existing
`runProductionOntologyInitialization` command composes the real platform tool,
skill, knowledge and memory registries with the production mapping runner.

Five event families have this production entry point:

| Event | Projected object | Scope |
| --- | --- | --- |
| `resource.tool.registered` | `tool` | System resource catalog |
| `resource.skill.registered` | `skill` | System resource catalog |
| `resource.mcp_server.registered` | `mcp_server` | System resource catalog |
| `knowledge.base.created` | `knowledge_base` | Source knowledge owner |
| `memory.profile.created` | `memory_profile` | Source memory owner and scope; personal assistant memory excluded |

Their manifest status is `partial_connected`. Initialization does not establish
live registration/update/deletion delivery, a durable incremental outbox, or
business consumer adoption. No bootstrap command or business history replay is
performed by this coverage change.

`production_ontology_catalog_coverage_test.go` exercises the production catalog
adapters and mapping runner against an initialized platform database. It checks
objects, source-bound facts and evidence, identical replay, missing source
identity rejection, identical IDs in different tenants, and the system resource
boundary. Existing initialization tests cover personal assistant memory
exclusion and omission of memory content from the graph.

Replay compares semantic payload, source/business times, ACLs and provenance.
`OrreryIngestedAt` records the latest projection processing time for these static
rows and may advance. Replaying the catalog must not add resource-history or
fact-assertion-history rows; this follows the existing static-history contract.

The remaining 20 expired exemptions continue to fail the release gate using
the current UTC time. They cover three planning, four legal, four development,
four value, two legacy People, and the todo/discussion/permission event families.
They require separate producer/consumer integration or an explicit decision
about obsolete event contracts. They are not renewed or treated as completed by
this change. RSI admission remains unpublishable while this mandatory gate fails.

The audit now includes active, expired and undated exemptions. The PowerShell
wrapper preserves the Go test exit status. These audit/wrapper fixes and their
regressions are incorporated from `b4dd076416579e7e475804a4f5d59026d36adffc`.
This document covers ORR-ONTOLOGY-01, ORR-RSI-01 and ORR-DEPLOY-01; it does not
change runtime authority, public Agent text, task QA, approval or channel delivery.
