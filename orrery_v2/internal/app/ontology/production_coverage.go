package ontology

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type ProductionCoverageStatus string

const (
	ProductionCoverageProductionConnected ProductionCoverageStatus = "production_connected"
	ProductionCoveragePartialConnected    ProductionCoverageStatus = "partial_connected"
	ProductionCoverageSeededOnly          ProductionCoverageStatus = "seeded_only"
	ProductionCoverageNotConnected        ProductionCoverageStatus = "not_connected"
	ProductionCoverageConsumerDeferred    ProductionCoverageStatus = "consumer_deferred"
)

type ProductionCoverageManifest struct {
	Records []ProductionCoverageRecord
}

type ProductionCoverageRecord struct {
	Domain                       string
	Feature                      string
	EventType                    string
	MappingRuleID                string
	Status                       ProductionCoverageStatus
	RequiredEventMetadata        []string
	RequiredPayloadFields        []string
	ProductionPublishers         []string
	DefinitionGovernanceEvidence []string
	BusinessSourceEvidence       []string
	TestEvidence                 []ProductionTestEvidence
	ContextGraphAssertions       []string
	KnownGaps                    []string
	Exemption                    *ProductionCoverageExemption
}

type ProductionTestEvidenceKind string

const (
	ProductionTestEvidenceSuccess         ProductionTestEvidenceKind = "success"
	ProductionTestEvidenceFailure         ProductionTestEvidenceKind = "failure"
	ProductionTestEvidenceReplay          ProductionTestEvidenceKind = "replay"
	ProductionTestEvidenceTenantIsolation ProductionTestEvidenceKind = "tenant_isolation"
)

type ProductionTestEvidence struct {
	Kind   ProductionTestEvidenceKind
	Path   string
	Reason string
}

type ProductionCoverageExemption struct {
	Owner     string
	Reason    string
	ExpiresAt time.Time
}

type ProductionCoverageExemptionSummary struct {
	Domain    string
	Feature   string
	EventType string
	Status    ProductionCoverageStatus
	Owner     string
	Reason    string
	ExpiresAt time.Time
}

type ProductionCoverageAuditRecord struct {
	Domain                string
	Feature               string
	EventType             string
	Status                ProductionCoverageStatus
	MappingRuleID         string
	ProductionPublisher   int
	TestEvidence          int
	ContextGraphAssertion int
	HasExemption          bool
}

type ProductionCoverageAuditReport struct {
	GeneratedAt         time.Time
	TotalRecords        int
	RequiredModuleCount int
	StatusCounts        map[ProductionCoverageStatus]int
	Records             []ProductionCoverageAuditRecord
	ActiveExemptions    []ProductionCoverageExemptionSummary
	ExpiredExemptions   []ProductionCoverageExemptionSummary
	UndatedExemptions   []ProductionCoverageExemptionSummary
}

type ProductionCoverageGateReport struct {
	Issues []ProductionCoverageGateIssue
}

type ProductionCoverageGateIssue struct {
	Domain    string
	EventType string
	Code      string
	Message   string
}

type ProductionEventPublisherEvidence struct {
	EventType string
	Path      string
}

type ProductionCoverageModule struct {
	Domain                 string
	Name                   string
	EventTypes             []string
	BusinessSourcePrefixes []string
}

func (r ProductionCoverageGateReport) OK() bool {
	return len(r.Issues) == 0
}

func (r ProductionCoverageGateReport) Error() string {
	if len(r.Issues) == 0 {
		return ""
	}
	messages := make([]string, 0, len(r.Issues))
	for _, issue := range r.Issues {
		messages = append(messages, issue.Message)
	}
	sort.Strings(messages)
	return strings.Join(messages, "\n")
}

func DefaultProductionCoverageManifest() ProductionCoverageManifest {
	// Keep unconnected production scope visible while assigning a shorter deadline
	// to event families that still have neither a mapping rule nor a publisher.
	expiresSeeded := time.Date(2026, 9, 30, 23, 59, 59, 0, time.UTC)
	expiresNotConnected := time.Date(2026, 9, 15, 23, 59, 59, 0, time.UTC)
	peopleProjectionPath := "cmd/server/production_people_ingestion.go"
	peopleProjectionTestPath := "cmd/server/production_people_ingestion_test.go"
	peopleProjectionTests := tests(
		peopleProjectionTestPath,
		failureTest(peopleProjectionTestPath, "mapped People payloads missing required fields are rejected"),
		replayTest(peopleProjectionTestPath, "completed People delivery batches replay without duplicate source or ontology events"),
		tenantTest(peopleProjectionTestPath, "People source event identity and graph projections are scoped by tenant"),
	)
	peopleProjectionGaps := gaps("the remaining People v2 metadata and governance events are retained durably but do not yet have Context Graph mappings")
	records := []ProductionCoverageRecord{
		partialCoverage("mission_control", "mission lifecycle subset", "mission.created",
			payload("id", "title", "status"),
			publishers("cmd/server/production_api.go:48", "cmd/server/production_project_ontology_api.go:42"),
			tests("cmd/server/production_memory_scope_ontology_test.go",
				eventReplayCoverageFailureTest(),
				eventReplayCoverageReplayTest(),
				tenantTest("cmd/server/production_memory_scope_ontology_test.go", "mission events are scoped by tenant")),
			assertions("object:mission", "fact:mission.title", "fact:mission.status", "evidence:mission-created"),
			gaps("mission status changes, membership, discussion, approval, and delivery lifecycle are not fully projected")),
		partialCoverage("mission_control", "mission task creation subset", "mission.task.created",
			payload("event_id", "mission_id", "task_id", "title", "status"),
			publishers("cmd/server/production_api.go:172", "cmd/server/production_api.go:178"),
			tests("cmd/server/production_memory_scope_ontology_test.go",
				eventReplayCoverageFailureTest(),
				eventReplayCoverageReplayTest(),
				tenantTest("cmd/server/production_memory_scope_ontology_test.go", "task events are scoped by mission tenant")),
			assertions("object:task", "link:mission_has_task", "fact:task.title", "fact:task.status", "evidence:mission-task-created"),
			gaps("task assignment, dependency, QA, blocker, and completion events are not fully projected")),
		partialCoverage("mission_control", "project creation subset", "project.created",
			payload("id", "title", "status"),
			publishers("cmd/server/production_project_api.go:20", "cmd/server/production_project_ontology_api.go:76"),
			tests("cmd/server/production_project_ontology_test.go",
				eventReplayCoverageFailureTest(),
				eventReplayCoverageReplayTest(),
				tenantTest("cmd/server/production_project_ontology_test.go", "project events are scoped by project tenant")),
			assertions("object:project", "fact:project.title", "fact:project.status", "evidence:project-created"),
			gaps("project links to tasks, knowledge, tools, agents, discussions, and approvals are not fully projected")),
		partialCoverage("mission_control", "project update subset", "project.updated",
			payload("id", "title"),
			publishers("cmd/server/production_project_api.go:71", "cmd/server/production_project_ontology_api.go:97"),
			tests("cmd/server/production_project_update_ontology_test.go",
				eventReplayCoverageFailureTest(),
				eventReplayCoverageReplayTest(),
				tenantTest("cmd/server/production_project_update_ontology_test.go", "project update events are scoped by project tenant")),
			assertions("fact:project.title", "evidence:project-updated"),
			gaps("project relationship changes beyond organization link are not fully projected")),
		partialCoverage("mission_control", "project archive subset", "project.archived",
			payload("id", "status"),
			publishers("cmd/server/production_project_api.go:88", "cmd/server/production_project_ontology_api.go:117"),
			tests("cmd/server/production_project_update_ontology_test.go",
				eventReplayCoverageFailureTest(),
				eventReplayCoverageReplayTest(),
				tenantTest("cmd/server/production_project_update_ontology_test.go", "project archive events are scoped by project tenant")),
			assertions("fact:project.status", "evidence:project-archived"),
			gaps("archive side effects for tasks, discussions, and knowledge references are not fully projected")),
		partialCoverage("mission_control", "project organization link subset", "project.organization.linked",
			payload("project_id", "project_ref_id", "organization_id", "title", "status"),
			publishers("cmd/server/production_project_api.go:23", "cmd/server/production_project_api.go:74", "cmd/server/production_project_api.go:91", "cmd/server/production_project_ontology_api.go:137"),
			tests("cmd/server/production_project_ontology_test.go", "cmd/server/production_ontology_projection_api_test.go",
				failureTest("cmd/server/production_ontology_projection_api_test.go", "missing organization projection target is audited"),
				replayTest("cmd/server/production_ontology_projection_api_test.go", "project organization projection audit can be replayed through the scoped command API"),
				tenantTest("cmd/server/production_project_ontology_test.go", "project organization events are scoped by project tenant")),
			assertions("object:project", "link:project_belongs_to_organization", "fact:project.title", "fact:project.status", "evidence:project-organization-linked"),
			gaps("organization hierarchy and membership relations are not fully projected")),
		partialCoverage("operations", "organization creation subset", "organization.created",
			payload("id", "name", "status", "code", "knowledge_base_id"),
			publishers("cmd/server/production_resource_operations_api.go:231", "cmd/server/production_resource_operations_api.go:242"),
			tests("cmd/server/production_organization_ontology_test.go",
				eventReplayCoverageFailureTest(),
				eventReplayCoverageReplayTest(),
				tenantTest("cmd/server/production_organization_ontology_test.go", "organization events are scoped by organization tenant")),
			assertions("object:organization", "fact:organization.name", "fact:organization.status", "fact:organization.code", "fact:organization.knowledge_base_id", "evidence:organization-created"),
			gaps("roles, hierarchy, membership, and ReBAC policy relations are not fully projected")),
		partialCoverage("knowledge", "knowledge document metadata", "knowledge.document.metadata.updated",
			payload("id", "title", "source_uri", "provider", "provider_document_id", "knowledge_base_id", "content_type", "size_bytes", "checksum", "status"),
			publishers("internal/app/knowledgeadmin/command_service.go:741", "internal/knowledge/ingest/service.go:302", "cmd/server/production_ontology.go:492"),
			tests("cmd/server/production_knowledge_ontology_test.go", "cmd/server/production_knowledge_ontology_replay_test.go",
				failureTest("cmd/server/production_knowledge_ontology_replay_test.go", "missing knowledge projection target is audited"),
				replayTest("cmd/server/production_knowledge_ontology_replay_test.go", "knowledge projection audit can be replayed"),
				tenantTest("cmd/server/production_knowledge_ontology_test.go", "knowledge document events are scoped by tenant")),
			assertions("object:knowledge_document", "fact:knowledge_document.title", "fact:knowledge_document.knowledge_base_id", "fact:knowledge_document.status", "evidence:knowledge-document-metadata"),
			gaps("knowledge base owner, binding, visibility, and permission relations are not fully projected")),
		partialCoverage("knowledge", "knowledge chunk metadata", "knowledge.chunk.metadata.updated",
			payload("id", "document_id", "knowledge_base_id", "ordinal", "heading_path", "start_offset", "end_offset", "content_hash", "vector_id", "token_estimate"),
			publishers("internal/knowledge/ingest/service.go:338", "cmd/server/production_ontology.go:500"),
			tests("cmd/server/production_knowledge_ontology_test.go", "cmd/server/production_knowledge_ontology_replay_test.go",
				failureTest("cmd/server/production_knowledge_ontology_replay_test.go", "missing chunk document target is audited"),
				replayTest("cmd/server/production_knowledge_ontology_replay_test.go", "knowledge chunk projection audit can be replayed"),
				tenantTest("cmd/server/production_knowledge_ontology_test.go", "knowledge chunk events are scoped by tenant")),
			assertions("object:knowledge_chunk", "link:knowledge_chunk_belongs_to_document", "fact:knowledge_chunk.content_hash", "evidence:knowledge-chunk-metadata"),
			gaps("chunk usage by retrieval, IRIS, and agents is not projected in this phase")),
		partialCoverage("knowledge", "knowledge page metadata", "knowledge.page.metadata.updated",
			payload("id", "document_id", "knowledge_base_id", "page_number", "provider", "provider_document_id", "heading_path", "source_uri"),
			publishers("internal/app/knowledgeadmin/command_service.go:817", "cmd/server/production_ontology.go:504"),
			tests("cmd/server/production_knowledge_ontology_test.go", "cmd/server/production_knowledge_ontology_replay_test.go",
				failureTest("cmd/server/production_knowledge_ontology_replay_test.go", "missing page document target is audited"),
				replayTest("cmd/server/production_knowledge_ontology_replay_test.go", "knowledge page projection audit can be replayed"),
				tenantTest("cmd/server/production_knowledge_ontology_test.go", "knowledge page events are scoped by tenant")),
			assertions("object:knowledge_page", "link:knowledge_page_belongs_to_document", "fact:knowledge_page.page_number", "evidence:knowledge-page-metadata"),
			gaps("page-level permission and retrieval usage relations are not projected in this phase")),
		partialCoverage("knowledge", "knowledge anchor metadata", "knowledge.anchor.metadata.updated",
			payload("id", "knowledge_base_id", "document_id", "page_id", "page_number", "provider", "provider_document_id", "provider_node_id", "heading_path", "structure_path", "bbox", "image_ref", "content_hash", "snippet_hash", "source_uri"),
			publishers("internal/app/knowledgeadmin/command_service.go:913", "cmd/server/production_ontology.go:508"),
			tests("cmd/server/production_knowledge_ontology_test.go", "cmd/server/production_knowledge_ontology_replay_test.go",
				failureTest("cmd/server/production_knowledge_ontology_replay_test.go", "missing anchor page target is audited"),
				replayTest("cmd/server/production_knowledge_ontology_replay_test.go", "knowledge anchor projection audit can be replayed"),
				tenantTest("cmd/server/production_knowledge_ontology_test.go", "knowledge anchor events are scoped by tenant")),
			assertions("object:knowledge_anchor", "link:knowledge_anchor_belongs_to_page", "fact:knowledge_anchor.snippet_hash", "evidence:knowledge-anchor-metadata"),
			gaps("anchor use by memory and runtime citation surfaces is only partially projected")),
		partialCoverage("knowledge", "knowledge document status", "knowledge.document.status.updated",
			payload("id", "status"),
			publishers("internal/app/knowledgeadmin/command_service.go:772", "internal/knowledge/ingest/service.go:392", "cmd/server/production_ontology.go:496"),
			tests("cmd/server/production_knowledge_ontology_test.go", "cmd/server/production_knowledge_ontology_replay_test.go",
				failureTest("cmd/server/production_knowledge_ontology_replay_test.go", "missing status target is audited"),
				replayTest("cmd/server/production_knowledge_ontology_replay_test.go", "knowledge status projection audit can be replayed"),
				tenantTest("cmd/server/production_knowledge_ontology_test.go", "knowledge status events are scoped by tenant")),
			assertions("fact:knowledge_document.status", "evidence:knowledge-document-status"),
			gaps("status side effects for visibility/search readiness are not fully projected")),
		partialCoverage("memory", "memory accepted record", "memory.record.accepted",
			payload("id", "summary", "layer", "topic", "scope_kind", "scope_id", "lifecycle_status", "confidence", "source_refs", "evidence_refs"),
			publishers("internal/app/memoryadmin/service.go:248", "cmd/server/production_ontology.go:53"),
			tests("cmd/server/production_memory_ontology_test.go",
				eventReplayCoverageFailureTest(),
				eventReplayCoverageReplayTest(),
				tenantTest("cmd/server/production_memory_ontology_test.go", "memory accepted events are scoped by tenant")),
			assertions("object:memory_record", "fact:memory_record.summary", "fact:memory_record.layer", "evidence:memory-record-accepted"),
			gaps("runtime, discussion, and task execution relations are not fully projected")),
		partialCoverage("memory", "memory knowledge document citation", "memory.knowledge.citation.added",
			payload("memory_record_id", "knowledge_document_id"),
			publishers("internal/app/memoryadmin/service.go:293", "cmd/server/production_ontology.go:53"),
			tests("cmd/server/production_memory_citation_ontology_test.go",
				failureTest("cmd/server/production_memory_citation_ontology_test.go", "missing citation target is audited"),
				replayTest("cmd/server/production_memory_citation_ontology_test.go", "memory citation projection audit can be replayed"),
				tenantTest("cmd/server/production_memory_citation_ontology_test.go", "memory citation events are scoped by tenant")),
			assertions("link:memory_cites_knowledge_document", "evidence:memory-knowledge-citation-added"),
			gaps("citation usage by consumers is deferred")),
		partialCoverage("memory", "memory knowledge chunk citation", "memory.knowledge_chunk.citation.added",
			payload("memory_record_id", "knowledge_chunk_id"),
			publishers("internal/app/memoryadmin/service.go:317", "cmd/server/production_ontology.go:53"),
			tests("cmd/server/production_memory_citation_ontology_test.go",
				failureTest("cmd/server/production_memory_citation_ontology_test.go", "missing citation target is audited"),
				replayTest("cmd/server/production_memory_citation_ontology_test.go", "memory chunk citation projection audit can be replayed"),
				tenantTest("cmd/server/production_memory_citation_ontology_test.go", "memory chunk citation events are scoped by tenant")),
			assertions("link:memory_cites_knowledge_chunk", "evidence:memory-knowledge-chunk-citation-added"),
			gaps("citation usage by consumers is deferred")),
		partialCoverage("memory", "memory knowledge anchor citation", "memory.knowledge_anchor.citation.added",
			payload("memory_record_id", "knowledge_anchor_id"),
			publishers("internal/app/memoryadmin/service.go:341", "cmd/server/production_ontology.go:53"),
			tests("cmd/server/production_memory_citation_ontology_test.go",
				failureTest("cmd/server/production_memory_citation_ontology_test.go", "missing citation target is audited"),
				replayTest("cmd/server/production_memory_citation_ontology_test.go", "memory anchor citation projection audit can be replayed"),
				tenantTest("cmd/server/production_memory_citation_ontology_test.go", "memory anchor citation events are scoped by tenant")),
			assertions("link:memory_cites_knowledge_anchor", "evidence:memory-knowledge-anchor-citation-added"),
			gaps("citation usage by consumers is deferred")),
		partialCoverage("memory", "memory lineage link", "memory.lineage.linked",
			payload("memory_record_id", "source_memory_record_id"),
			publishers("internal/app/memoryadmin/service.go:400", "cmd/server/production_ontology.go:53"),
			tests("cmd/server/production_memory_lifecycle_ontology_test.go",
				failureTest("cmd/server/production_memory_lifecycle_ontology_test.go", "missing lineage target is audited"),
				replayTest("cmd/server/production_memory_lifecycle_ontology_test.go", "memory lineage projection audit can be replayed"),
				tenantTest("cmd/server/production_memory_lifecycle_ontology_test.go", "memory lineage events are scoped by tenant")),
			assertions("link:memory_distilled_from_memory", "evidence:memory-lineage-linked"),
			gaps("lineage across agent runtime and discussion sources is not fully projected")),
		partialCoverage("memory", "memory scope link", "memory.scope.linked",
			payload("memory_record_id"),
			publishers("internal/app/memoryadmin/service.go:376", "cmd/server/production_ontology.go:53"),
			tests("cmd/server/production_memory_scope_ontology_test.go",
				failureTest("cmd/server/production_memory_scope_ontology_test.go", "missing scope target is audited"),
				eventReplayCoverageReplayTest(),
				tenantTest("cmd/server/production_memory_scope_ontology_test.go", "memory scope events are scoped by tenant")),
			assertions("link:memory_about_mission", "link:memory_about_project", "link:memory_about_task", "evidence:memory-scope-linked"),
			gaps("scope targets beyond mission/project/task are not fully projected")),
		partialCoverage("memory", "memory lifecycle update", "memory.record.lifecycle.updated",
			payload("id", "summary", "lifecycle_status", "reference_count", "last_referenced_at"),
			publishers("internal/app/memoryadmin/service.go:272", "cmd/server/production_ontology.go:53"),
			tests("cmd/server/production_memory_lifecycle_ontology_test.go",
				failureTest("cmd/server/production_memory_lifecycle_ontology_test.go", "missing lifecycle target is audited"),
				replayTest("cmd/server/production_memory_lifecycle_ontology_test.go", "memory lifecycle projection audit can be replayed"),
				tenantTest("cmd/server/production_memory_lifecycle_ontology_test.go", "memory lifecycle events are scoped by tenant")),
			assertions("object:memory_record", "fact:memory_record.lifecycle_status", "fact:memory_record.reference_count", "evidence:memory-record-lifecycle-updated"),
			gaps("runtime references that drive lifecycle updates are not fully projected")),

		seededOnlyCoverage("operations", "legacy organization unit team rule without production publisher", "people.team.created",
			payload("id", "name", "status"),
			exemption("platform-ontology", "people v2 production path now publishes department and position events; legacy team event remains a seeded compatibility mapping only", expiresSeeded)),
		partialCoverage("people", "People v2 workforce subject bitemporal projection", "people.subject.v2.changed",
			payload("Metadata", "Value", "id", "type", "subject_type", "display_name", "status", "lifecycle_status", "revision", "valid_from", "valid_to", "recorded_at", "superseded_at"),
			publishers(peopleProjectionPath), peopleProjectionTests,
			assertions("object:workforce_subject", "fact:workforce_subject.display_name", "fact:workforce_subject.lifecycle_status", "evidence:people-subject-v2"), peopleProjectionGaps),
		partialCoverage("people", "People v2 organization unit bitemporal projection", "people.org_unit.v2.changed",
			payload("Metadata", "Value", "id", "code", "display_name", "parent_id", "status", "lifecycle_status", "revision", "valid_from", "valid_to", "recorded_at", "superseded_at"),
			publishers(peopleProjectionPath), peopleProjectionTests,
			assertions("object:organization_unit", "link:organization_unit_part_of_organization_unit", "fact:organization_unit.display_name", "evidence:people-org-unit-v2"), peopleProjectionGaps),
		partialCoverage("people", "People v2 job profile bitemporal projection", "people.job_profile.v2.changed",
			payload("Metadata", "Value", "id", "code", "display_name", "job_family", "job_level", "status", "lifecycle_status", "revision", "valid_from", "valid_to", "recorded_at", "superseded_at"),
			publishers(peopleProjectionPath), peopleProjectionTests,
			assertions("object:job_profile", "fact:job_profile.display_name", "evidence:people-job-profile-v2"), peopleProjectionGaps),
		partialCoverage("people", "People v2 position bitemporal projection", "people.position.v2.changed",
			payload("Metadata", "Value", "id", "code", "display_name", "org_unit_id", "job_profile_id", "approved_headcount", "status", "lifecycle_status", "revision", "valid_from", "valid_to", "recorded_at", "superseded_at"),
			publishers(peopleProjectionPath), peopleProjectionTests,
			assertions("object:position", "link:position_in_organization_unit", "link:position_uses_job_profile", "fact:position.name", "evidence:people-position-v2"), peopleProjectionGaps),
		partialCoverage("people", "People v2 workforce assignment bitemporal projection", "people.assignment.v2.changed",
			payload("id", "subject_id", "org_unit_id", "position_id", "assignment_kind", "allocation_bps", "valid_from", "valid_to", "revision"),
			publishers(peopleProjectionPath), peopleProjectionTests,
			assertions("object:workforce_assignment", "link:workforce_assignment_for_subject", "link:workforce_assignment_in_organization_unit", "link:workforce_assignment_to_position", "fact:workforce_assignment.assignment_kind", "evidence:people-assignment-v2"), peopleProjectionGaps),
		partialCoverage("people", "People v2 reporting relationship bitemporal projection", "people.reporting_relationship.v2.changed",
			payload("id", "subject_id", "manager_id", "reporting_type", "valid_from", "valid_to", "revision"),
			publishers(peopleProjectionPath), peopleProjectionTests,
			assertions("object:reporting_relationship", "link:reporting_relationship_for_subject", "link:reporting_relationship_to_manager", "fact:reporting_relationship.reporting_type", "evidence:people-reporting-v2"), peopleProjectionGaps),
		partialCoverage("people", "People v2 responsibility bitemporal projection", "people.responsibility.v2.changed",
			payload("id", "code", "title", "description", "valid_from", "valid_to", "revision"),
			publishers(peopleProjectionPath), peopleProjectionTests,
			assertions("object:responsibility", "fact:responsibility.title", "evidence:people-responsibility-v2"), peopleProjectionGaps),
		partialCoverage("people", "People v2 responsibility binding bitemporal projection", "people.responsibility_binding.v2.changed",
			payload("id", "responsibility_id", "target_type", "target_id", "valid_from", "valid_to", "revision"),
			publishers(peopleProjectionPath), peopleProjectionTests,
			assertions("object:responsibility_binding", "link:responsibility_binding_for_responsibility", "fact:responsibility_binding.target_type", "evidence:people-responsibility-binding-v2"), peopleProjectionGaps),
		partialCoverage("operations", "workforce person profile update subset", "people.person.updated",
			payload("id", "display_name", "role", "email", "status", "skills"),
			publishers("cmd/server/production_resource_operations_api.go"),
			tests("cmd/server/production_people_ontology_test.go",
				eventReplayCoverageFailureTest(),
				eventReplayCoverageReplayTest(),
				tenantTest("cmd/server/production_people_ontology_test.go", "person update events are scoped by organization tenant")),
			assertions("object:person", "fact:person.display_name", "fact:person.role", "fact:person.email", "fact:person.status", "evidence:people-person-updated"),
			gaps("workforce release/disable lifecycle and ReBAC role relations are not fully projected")),
		partialCoverage("operations", "workforce agent runtime profile update subset", "people.agent.updated",
			payload("id", "display_name", "runtime_kind"),
			publishers("cmd/server/production_resource_operations_api.go", "cmd/server/production_agent_execution_ontology.go"),
			tests("cmd/server/production_people_ontology_test.go",
				eventReplayCoverageFailureTest(),
				eventReplayCoverageReplayTest(),
				tenantTest("cmd/server/production_people_ontology_test.go", "agent update events are scoped by organization tenant")),
			assertions("object:agent", "fact:agent.display_name", "fact:agent.runtime_kind", "evidence:people-agent-updated"),
			gaps("agent runtime availability, model/provider capability, and permission relations are not fully projected")),
		seededOnlyCoverage("operations", "legacy workforce team membership rule without production publisher", "people.membership.linked",
			payload("person_id", "team_id"),
			exemption("platform-ontology", "people v2 production path now publishes position assignments; legacy team membership event remains a seeded compatibility mapping only", expiresSeeded)),
		partialCoverage("operations", "organization department creation subset", "people.department.created",
			payload("id", "name", "status", "owner_profile_id", "organization_id"),
			publishers("cmd/server/production_resource_operations_api.go"),
			tests("cmd/server/production_people_ontology_test.go",
				eventReplayCoverageFailureTest(),
				eventReplayCoverageReplayTest(),
				tenantTest("cmd/server/production_people_ontology_test.go", "department events are scoped by organization tenant")),
			assertions("object:department", "link:department_part_of_organization", "fact:department.name", "fact:department.status", "evidence:people-department-created"),
			gaps("department hierarchy, ownership transfer, and ReBAC policy relations are not fully projected")),
		partialCoverage("operations", "organization position creation subset", "people.position.created",
			payload("id", "name", "status", "department_id"),
			publishers("cmd/server/production_resource_operations_api.go"),
			tests("cmd/server/production_people_ontology_test.go",
				eventReplayCoverageFailureTest(),
				eventReplayCoverageReplayTest(),
				tenantTest("cmd/server/production_people_ontology_test.go", "position events are scoped by organization tenant")),
			assertions("object:position", "link:position_belongs_to_department", "fact:position.name", "fact:position.status", "evidence:people-position-created"),
			gaps("position level, job family lifecycle, reporting lines, and ReBAC policy relations are not fully projected")),
		partialCoverage("operations", "workforce position assignment subset", "people.position.assigned",
			payload("id", "person_id", "agent_id", "position_id", "role_id", "scope", "status"),
			publishers("cmd/server/production_resource_operations_api.go"),
			tests("cmd/server/production_people_ontology_test.go",
				eventReplayCoverageFailureTest(),
				eventReplayCoverageReplayTest(),
				tenantTest("cmd/server/production_people_ontology_test.go", "position assignment events are scoped by organization tenant")),
			assertions("object:role_assignment", "link:person_holds_position", "link:agent_fulfills_position", "fact:role_assignment.role_id", "fact:role_assignment.status", "evidence:people-position-assigned"),
			gaps("assignment release/move lifecycle, reporting lines, and ReBAC policy relations are not fully projected")),
		seededOnlyCoverage("planning", "requirement rule without production publisher", "planning.requirement.created",
			payload("id", "title", "status", "priority", "acceptance_criteria"),
			exemption("platform-ontology", "planning requirement production module is not yet connected to ontology DomainEvent", expiresSeeded)),
		partialCoverage("planning", "idea lifecycle subset", "planning.idea.captured",
			payload("id", "issue_number", "title", "summary", "status", "source", "captured_by", "proposer_name", "updated_by"),
			publishers("cmd/server/production_todo_ontology.go"),
			tests("cmd/server/production_todo_ontology_test.go",
				eventReplayCoverageFailureTest(),
				eventReplayCoverageReplayTest(),
				tenantTest("cmd/server/production_todo_ontology_test.go", "idea capture events are scoped by planning tenant")),
			assertions("object:idea", "fact:idea.title", "fact:idea.status", "evidence:planning-idea-captured"),
			gaps("requirement links and planning decision relations are not fully projected")),
		partialCoverage("planning", "idea conversion lineage subset", "planning.idea.converted_to_todo",
			payload("idea_id", "idea_status", "todo_id", "title", "summary", "status", "source", "created_by"),
			publishers("cmd/server/production_todo_ontology.go"),
			tests("cmd/server/production_todo_ontology_test.go",
				eventReplayCoverageFailureTest(),
				eventReplayCoverageReplayTest(),
				tenantTest("cmd/server/production_todo_ontology_test.go", "idea conversion events are scoped by planning tenant")),
			assertions("object:idea", "object:todo", "link:idea_converted_to_todo", "fact:todo.upstream_idea_id", "fact:idea.downstream_todo_id", "evidence:planning-idea-converted-to-todo"),
			gaps("cross-planning requirement links are not fully projected")),
		partialCoverage("planning", "todo lifecycle subset", "planning.todo.changed",
			payload("id", "issue_number", "title", "summary", "status", "source", "proposer_name", "created_by", "updated_by", "assignee_id", "assignee_name"),
			publishers("cmd/server/production_todo_ontology.go"),
			tests("cmd/server/production_todo_ontology_test.go",
				eventReplayCoverageFailureTest(),
				eventReplayCoverageReplayTest(),
				tenantTest("cmd/server/production_todo_ontology_test.go", "todo lifecycle events are scoped by planning tenant")),
			assertions("object:todo", "fact:todo.title", "fact:todo.status", "fact:todo.source", "evidence:planning-todo-changed"),
			gaps("cross-planning requirement links are not fully projected")),
		partialCoverage("planning", "todo comment lineage subset", "planning.todo.comment.linked",
			payload("comment_id", "issue_id", "body", "source", "author_id", "author_name"),
			publishers("cmd/server/production_todo_ontology.go"),
			tests("cmd/server/production_todo_ontology_test.go",
				eventReplayCoverageFailureTest(),
				eventReplayCoverageReplayTest(),
				tenantTest("cmd/server/production_todo_ontology_test.go", "todo comment events are scoped by planning tenant")),
			assertions("object:issue_comment", "fact:issue_comment.issue_kind", "fact:issue_comment.issue_object_id", "evidence:planning-issue-comment-linked"),
			gaps("comment deletion and reactions are not projected")),
		partialCoverage("planning", "idea comment lineage subset", "planning.idea.comment.linked",
			payload("comment_id", "issue_id", "body", "source", "author_id", "author_name"),
			publishers("cmd/server/production_todo_ontology.go"),
			tests("cmd/server/production_todo_ontology_test.go",
				eventReplayCoverageFailureTest(),
				eventReplayCoverageReplayTest(),
				tenantTest("cmd/server/production_todo_ontology_test.go", "idea comment events are scoped by planning tenant")),
			assertions("object:issue_comment", "fact:issue_comment.issue_kind", "fact:issue_comment.issue_object_id", "evidence:planning-issue-comment-linked"),
			gaps("comment deletion and reactions are not projected")),
		seededOnlyCoverage("planning", "decision rule without production publisher", "planning.decision.recorded",
			payload("id", "title", "status", "rationale", "decided_by"),
			exemption("platform-ontology", "planning decision production module is not yet connected to ontology DomainEvent", expiresSeeded)),
		seededOnlyCoverage("planning", "design artifact rule without production publisher", "planning.design_artifact.created",
			payload("id", "title", "artifact_type", "status", "uri"),
			exemption("platform-ontology", "planning design artifact production module is not yet connected to ontology DomainEvent", expiresSeeded)),
		seededOnlyCoverage("legal", "legal entity rule without production publisher", "legal.entity.created",
			payload("id", "name"),
			exemption("platform-ontology", "legal entity production module is not yet connected to ontology DomainEvent", expiresSeeded)),
		seededOnlyCoverage("legal", "legal contract rule without production publisher", "legal.contract.created",
			payload("id", "title"),
			exemption("platform-ontology", "contract production module is not yet connected to ontology DomainEvent", expiresSeeded)),
		seededOnlyCoverage("legal", "legal policy rule without production publisher", "legal.policy.created",
			payload("id", "title"),
			exemption("platform-ontology", "policy production module is not yet connected to ontology DomainEvent", expiresSeeded)),
		seededOnlyCoverage("legal", "approval case rule without production publisher", "legal.approval_case.opened",
			payload("id", "title"),
			exemption("platform-ontology", "approval center currently records business audit but not ontology DomainEvent", expiresSeeded)),
		seededOnlyCoverage("development", "repository rule without production publisher", "development.repository.registered",
			payload("id", "name"),
			exemption("platform-ontology", "development repository production module is not yet connected to ontology DomainEvent", expiresSeeded)),
		seededOnlyCoverage("development", "issue rule without production publisher", "development.issue.created",
			payload("id", "title"),
			exemption("platform-ontology", "development issue production module is not yet connected to ontology DomainEvent", expiresSeeded)),
		seededOnlyCoverage("development", "pull request rule without production publisher", "development.pull_request.opened",
			payload("id", "title"),
			exemption("platform-ontology", "pull request production module is not yet connected to ontology DomainEvent", expiresSeeded)),
		seededOnlyCoverage("development", "release rule without production publisher", "development.release.created",
			payload("id", "version"),
			exemption("platform-ontology", "release production module is not yet connected to ontology DomainEvent", expiresSeeded)),
		seededOnlyCoverage("value", "objective rule without production publisher", "value.objective.created",
			payload("id", "title"),
			exemption("platform-ontology", "value objective production module is not yet connected to ontology DomainEvent", expiresSeeded)),
		seededOnlyCoverage("value", "metric rule without production publisher", "value.metric.recorded",
			payload("id", "name"),
			exemption("platform-ontology", "value metric production module is not yet connected to ontology DomainEvent", expiresSeeded)),
		seededOnlyCoverage("value", "initiative rule without production publisher", "value.initiative.created",
			payload("id", "title"),
			exemption("platform-ontology", "initiative production module is not yet connected to ontology DomainEvent", expiresSeeded)),
		seededOnlyCoverage("value", "experiment rule without production publisher", "value.experiment.created",
			payload("id", "title"),
			exemption("platform-ontology", "experiment production module is not yet connected to ontology DomainEvent", expiresSeeded)),

		partialCoverage("todo", "task creation origin facts", "todo.created",
			payload("id", "created_at", "created_by", "creation_source"),
			publishers("cmd/server/todo_creation_ontology.go"),
			tests("cmd/server/production_todo_ontology_test.go",
				failureTest("cmd/server/todo_creation_outbox_test.go", "transactional creation intent survives failures before append, after append and before acknowledgement; repository tests cover atomic rollback"),
				replayTest("cmd/server/todo_creation_outbox_test.go", "registered platform sweeper recovers immutable origins after restart without reverting current task state or duplicating events"),
				tenantTest("cmd/server/todo_creation_ontology_test.go", "same task identity in different tenants retains separate events, origin facts and source ACL")),
			assertions("fact:todo.created_at", "fact:todo.creation_actor_id", "fact:todo.creation_source", "evidence:todo-created"),
			gaps("creation origin facts supplement planning.todo.changed; repository creation and conversion persist a same-transaction creation outbox with bounded runtime recovery; legacy tasks are not backfilled, direct raw database writes are outside the repository contract, and mutable planning events still have separate delivery gaps")),
		notConnectedCoverage("discussion", "discussion message missing mapping rule and publisher", "discussion.message.created",
			businessSources("internal/app/discussionsession/service_direct_chat.go", "internal/app/discussionsession/service_run.go", "cmd/server/discussion_realtime.go"),
			exemption("platform-ontology", "0618 discussion scope currently has realtime/audit events but no ontology mapping rule or DomainEvent publisher", expiresNotConnected)),
		notConnectedCoverage("permission", "permission/ReBAC missing mapping rule and publisher", "permission.binding.updated",
			businessSources("cmd/server/production_resource_operations_api.go", "internal/app/operations/command_roles.go", "internal/app/operations/permission_decision.go", "internal/app/operations/query_permissions.go", "internal/app/rebac/types.go"),
			exemption("platform-ontology", "0618 permission/ReBAC graph scope has no ontology mapping rule or production publisher yet; must land before consumer-side authorization graph use", expiresNotConnected)),
	}
	records = append(records, catalogInitializationProductionCoverageRecords()...)
	records = append(records, agentExecutionProductionCoverageRecords()...)
	records = append(records, reliabilityProductionCoverageRecords()...)
	records = append(records, problemHubProductionCoverageRecords()...)
	records = append(records, improvementProductionCoverageRecords()...)
	return ProductionCoverageManifest{Records: records}
}

func agentExecutionProductionCoverageRecords() []ProductionCoverageRecord {
	const testPath = "cmd/server/production_agent_execution_ontology_test.go"
	coverage := func(domain, feature, eventType string, requiredPayload, graphAssertions []string) ProductionCoverageRecord {
		return partialCoverage(
			domain,
			feature,
			eventType,
			requiredPayload,
			publishers(agentExecutionProductionPublisherPath(eventType)),
			tests(
				testPath,
				failureTest(testPath, "the production event matrix rejects missing required projection payload"),
				replayTest(testPath, "retained agent execution events can be reapplied idempotently"),
				tenantTest(testPath, "agent execution projections remain isolated by tenant"),
			),
			graphAssertions,
			gaps("downstream business consumers adopt the execution graph incrementally"),
		)
	}
	records := []ProductionCoverageRecord{
		coverage("action_contract", "governed action run start", "action.run.started",
			payload("id", "action_type_id", "status"),
			assertions("object:action_run", "fact:action_run.status", "evidence:action-run-started")),
		coverage("action_contract", "governed action run completion", "action.run.completed",
			payload("id", "status"),
			assertions("fact:action_run.status", "evidence:action-run-completed")),
		coverage("action_contract", "action writeback success", "action.writeback.succeeded",
			payload("action_run_id", "receipt_id", "status"),
			assertions("fact:action_run.writeback_receipt_id", "fact:action_run.writeback_status", "evidence:action-writeback-succeeded")),
		coverage("action_contract", "action writeback failure", "action.writeback.failed",
			payload("action_run_id", "receipt_id", "status"),
			assertions("fact:action_run.writeback_receipt_id", "fact:action_run.writeback_status", "evidence:action-writeback-failed")),
		coverage("action_contract", "action policy decision", "action.run.policy.decided",
			payload("id", "decision", "action_run_id"),
			assertions("object:policy_decision", "link:action_run_authorized_by_policy", "fact:policy_decision.decision", "evidence:action-policy-decided")),
		coverage("action_contract", "action evidence attachment", "action.run.evidence.attached",
			payload("action_run_id", "evidence_id"),
			assertions("link:evidence_item_supports_action_run", "evidence:action-evidence-attached")),
		coverage("scheduler", "scheduled task creation", "scheduler.task.created",
			payload("id", "title", "enabled", "agent_task_id"),
			assertions("object:scheduled_task", "link:scheduled_task_schedules_agent_task", "fact:scheduled_task.title", "evidence:scheduler-task-created")),
		coverage("scheduler", "scheduled task update", "scheduler.task.updated",
			payload("id", "title", "enabled", "agent_task_id"),
			assertions("object:scheduled_task", "link:scheduled_task_schedules_agent_task", "fact:scheduled_task.title", "evidence:scheduler-task-updated")),
		coverage("scheduler", "scheduled task cancellation", "scheduler.task.cancelled",
			payload("id", "title", "enabled", "agent_task_id"),
			assertions("object:scheduled_task", "link:scheduled_task_schedules_agent_task", "fact:scheduled_task.enabled", "evidence:scheduler-task-cancelled")),
		coverage("scheduler", "scheduled task target agent", "scheduler.task.agent_targeted",
			payload("scheduled_task_id", "agent_id"),
			assertions("link:scheduled_task_targets_agent", "evidence:scheduler-task-agent-targeted")),
		coverage("scheduler", "scheduled run start", "scheduler.run.started",
			payload("id", "scheduled_task_id", "status"),
			assertions("object:scheduled_run", "link:scheduled_task_has_run", "fact:scheduled_run.status", "evidence:scheduler-run-started")),
		coverage("scheduler", "scheduled run completion", "scheduler.run.completed",
			payload("id", "scheduled_task_id", "status"),
			assertions("object:scheduled_run", "link:scheduled_task_has_run", "fact:scheduled_run.status", "evidence:scheduler-run-completed")),
		coverage("scheduler", "scheduled run agent resolution", "scheduler.run.agent_resolved",
			payload("id", "agent_run_id"),
			assertions("object:scheduled_run", "link:scheduled_run_triggers_agent_run", "fact:scheduled_run.agent_run_ref_status", "evidence:scheduler-run-agent-resolved")),
		coverage("agent_runtime", "agent version observation", "agent.version.observed",
			payload("id", "agent_id", "version"),
			assertions("object:agent_version", "link:agent_has_version", "fact:agent_version.version", "evidence:agent-version-observed")),
		coverage("agent_runtime", "agent task materialization", "agent.task.upserted",
			payload("id", "title", "status", "source_kind"),
			assertions("object:agent_task", "fact:agent_task.status", "evidence:agent-task-upserted")),
		coverage("agent_runtime", "agent run start", "agent.runtime.run.started",
			payload("id", "runtime_run_id", "status", "agent_task_id"),
			assertions("object:agent_run", "link:agent_run_executes_agent_task", "fact:agent_run.status", "evidence:agent-runtime-started")),
		coverage("agent_runtime", "agent run completion", "agent.runtime.run.completed",
			payload("id", "status"),
			assertions("fact:agent_run.status", "evidence:agent-runtime-completed")),
		coverage("agent_runtime", "agent context attachment", "agent.runtime.context.attached",
			payload("id", "agent_run_id"),
			assertions("object:context_snapshot", "link:agent_run_uses_context", "fact:context_snapshot.summary", "evidence:agent-context-attached")),
		coverage("agent_runtime", "responsibility context attachment", "agent.runtime.context.responsibility.attached",
			payload("id", "context_snapshot_id", "responsibility_id", "responsibility_version", "source", "actor_member_id", "captured_at", "effective_at"),
			assertions("object:responsibility_context_snapshot", "link:context_snapshot_uses_responsibility_context", "fact:responsibility_context_snapshot.responsibility_version", "evidence:agent-responsibility-context-attached")),
		coverage("agent_runtime", "agent context memory attachment", "agent.runtime.context.memory.attached",
			payload("context_snapshot_id", "memory_digest_id"),
			assertions("object:memory_digest", "link:context_snapshot_uses_memory_digest", "fact:memory_digest.memory_kind", "evidence:agent-context-memory-attached")),
		coverage("agent_runtime", "agent context knowledge document attachment", "agent.runtime.context.knowledge_document.attached",
			payload("context_snapshot_id", "knowledge_ref_id"),
			assertions("link:context_snapshot_uses_knowledge", "evidence:agent-context-knowledge-document-attached")),
		coverage("agent_runtime", "agent context knowledge chunk attachment", "agent.runtime.context.knowledge_chunk.attached",
			payload("context_snapshot_id", "knowledge_ref_id"),
			assertions("link:context_snapshot_uses_knowledge_chunk", "evidence:agent-context-knowledge-chunk-attached")),
		coverage("agent_runtime", "agent context knowledge anchor attachment", "agent.runtime.context.knowledge_anchor.attached",
			payload("context_snapshot_id", "knowledge_ref_id"),
			assertions("link:context_snapshot_uses_knowledge_anchor", "evidence:agent-context-knowledge-anchor-attached")),
		coverage("agent_runtime", "agent execution trace", "agent.runtime.trace.recorded",
			payload("id", "trace_id", "agent_run_id"),
			assertions("object:execution_trace", "link:agent_run_has_trace", "fact:execution_trace.trace_id", "evidence:agent-trace-recorded")),
		coverage("agent_runtime", "agent evidence output", "agent.runtime.evidence.produced",
			payload("id", "evidence_kind", "summary", "agent_run_id"),
			assertions("object:evidence_item", "link:agent_run_produces_evidence", "fact:evidence_item.evidence_kind", "evidence:agent-evidence-produced")),
		coverage("agent_runtime", "agent artifact output", "agent.runtime.artifact.produced",
			payload("id", "title", "artifact_type", "agent_run_id"),
			assertions("object:artifact", "link:agent_run_produces_artifact", "fact:artifact.artifact_type", "evidence:agent-artifact-produced")),
		coverage("agent_runtime", "agent action request", "agent.runtime.action.requested",
			payload("agent_run_id", "action_run_id"),
			assertions("link:agent_run_requests_action_run", "evidence:agent-action-requested")),
		coverage("agent_runtime", "agent HIL approval", "agent.runtime.approval.required",
			payload("approval_case_id", "title", "status", "risk_level", "action_run_id"),
			assertions("object:approval_case", "link:action_run_requires_approval_case", "fact:approval_case.status", "evidence:agent-approval-required")),
	}
	for _, objectType := range actionRunTargetObjectTypes() {
		records = append(records, coverage(
			"action_contract",
			"action target "+objectType,
			"action.run.targeted."+objectType,
			payload("action_run_id", "object_id"),
			assertions("link:action_run_targets_"+objectType, "evidence:action-run-targeted"),
		))
	}
	return records
}

func agentExecutionProductionPublisherPath(eventType string) string {
	switch strings.TrimSpace(eventType) {
	case "action.writeback.succeeded", "action.writeback.failed":
		return "cmd/server/production_action_writeback_ontology.go"
	case "agent.runtime.context.attached", "agent.runtime.context.responsibility.attached":
		return "cmd/server/production_agent_execution_responsibility_ontology.go"
	default:
		return "cmd/server/production_agent_execution_ontology.go"
	}
}

func reliabilityProductionCoverageRecords() []ProductionCoverageRecord {
	const (
		publisherPath = "cmd/server/production_reliability_ontology.go"
		testPath      = "cmd/server/production_reliability_ontology_test.go"
	)
	coverage := func(feature, eventType string, requiredPayload, graphAssertions []string) ProductionCoverageRecord {
		return partialCoverage(
			"reliability",
			feature,
			eventType,
			requiredPayload,
			publishers(publisherPath),
			tests(
				testPath,
				failureTest(testPath, "reliability mappings reject missing required payload"),
				replayTest(testPath, "reliability events replay without duplicating graph state"),
				tenantTest(testPath, "failure incidents and repair rules remain tenant isolated"),
			),
			graphAssertions,
			gaps("HIL-approved mission agent retry apply is enabled after exact dry-run preflight; restart, fallback, patch, reconfigure, permission, data, and unattended apply remain disabled"),
		)
	}
	return []ProductionCoverageRecord{
		coverage("failure incident observation", "reliability.failure.observed",
			payload("id", "fingerprint", "source", "component", "category", "severity", "status", "error_code", "redacted_summary", "retryable", "first_seen_at", "last_seen_at", "occurrence_count", "incident_version"),
			assertions("object:failure_incident", "object:evidence_item", "link:evidence_item_supports_failure_incident", "fact:failure_incident.status", "evidence:reliability-failure-observed")),
		coverage("failure incident status transition", "reliability.failure.status_changed",
			payload("id", "status", "incident_version"),
			assertions("fact:failure_incident.status", "evidence:reliability-failure-status-changed")),
		coverage("evidence-backed diagnosis case", "reliability.diagnosis_case.upserted",
			payload("id", "incident_id", "incident_version", "root_cause_category", "redacted_summary", "confidence", "status", "evidence_ids", "primary_evidence_id", "diagnosis_version", "proposed_by_kind", "proposed_by_id", "updated_at"),
			assertions("object:diagnosis_case", "link:diagnosis_case_diagnoses_failure_incident", "link:evidence_item_supports_diagnosis_case", "fact:diagnosis_case.status", "evidence:reliability-diagnosis-case-upserted")),
		coverage("learned repair rule", "reliability.repair_rule.upserted",
			payload("id", "selector_fingerprint", "strategy", "action_type_id", "risk_level", "status", "confidence", "support_count", "success_count", "failure_count", "reversible", "idempotent", "auto_apply", "rule_version", "source_incident_id"),
			assertions("object:repair_rule", "link:repair_rule_learned_from_failure_incident", "fact:repair_rule.confidence", "evidence:reliability-repair-rule-upserted")),
		coverage("governed repair dry-run evidence", "reliability.repair_dry_run.checked",
			payload("id", "action_run_id", "failure_incident_id", "repair_rule_id", "evidence_kind", "summary", "source_ref", "content_ref", "checks", "strategy", "status"),
			assertions("object:evidence_item", "link:evidence_item_supports_action_run", "link:evidence_item_supports_failure_incident", "link:evidence_item_supports_repair_rule", "fact:evidence_item.checks", "fact:evidence_item.target_run_id", "evidence:repair-dry-run-checked")),
		coverage("terminal repair verification evidence", "reliability.repair_verification.checked",
			payload("id", "action_run_id", "failure_incident_id", "repair_rule_id", "evidence_kind", "summary", "source_ref", "content_ref", "status", "original_run_id", "retry_run_id", "retry_agent_run_id", "queue_status", "failure_code"),
			assertions("object:evidence_item", "link:evidence_item_supports_action_run", "link:evidence_item_supports_failure_incident", "link:evidence_item_supports_repair_rule", "link:evidence_item_verifies_agent_run", "fact:evidence_item.queue_status", "evidence:repair-verification-checked")),
	}
}

func problemHubProductionCoverageRecords() []ProductionCoverageRecord {
	const testPath = "cmd/server/production_problem_hub_ontology_test.go"
	coverage := func(feature, eventType, publisherPath string, requiredPayload, graphAssertions []string) ProductionCoverageRecord {
		return partialCoverage(
			"problem_hub",
			feature,
			eventType,
			requiredPayload,
			publishers(publisherPath),
			tests(
				testPath,
				failureTest(testPath, "problem hub mappings reject incomplete required payload"),
				replayTest(testPath, "problem hub events replay without duplicating cases or lesson candidates"),
				tenantTest(testPath, "problem cases, lesson candidates, and accepted memories remain tenant isolated"),
			),
			graphAssertions,
			gaps("reliability is connected; approval, task, development, and business-domain problem adapters still need to use the generic problem hub ingress"),
		)
	}
	return []ProductionCoverageRecord{
		coverage("company problem case", "problem.case.upserted", "cmd/server/production_problem_hub_ontology.go",
			payload("id", "fingerprint", "source_domain", "category", "severity", "status", "title", "primary_source_kind", "primary_source_id", "evidence_ids", "occurrence_count", "problem_version", "first_seen_at", "last_seen_at", "updated_at"),
			assertions("object:problem_case", "fact:problem_case.status", "link:problem_case_tracks_failure_incident", "evidence:problem-case")),
		coverage("governed lesson candidate", "problem.lesson_candidate.proposed", "cmd/server/production_problem_hub_ontology.go",
			payload("id", "memory_candidate_id", "problem_case_id", "status", "proposed_layer", "topic", "summary", "confidence", "review_required", "evidence_ids", "created_at"),
			assertions("object:lesson_candidate", "fact:lesson_candidate.status", "link:problem_case_proposes_lesson_candidate", "evidence:problem-lesson")),
		coverage("accepted problem lesson memory", "problem.memory_record.accepted", "internal/app/memoryadmin/service.go",
			payload("id", "problem_case_id", "problem_lesson_candidate_id", "status"),
			assertions("fact:lesson_candidate.review_outcome", "link:memory_about_problem_case", "evidence:problem-memory-record-accepted")),
	}
}

func BuildProductionCoverageAuditReport(manifest ProductionCoverageManifest, asOf time.Time) ProductionCoverageAuditReport {
	report := ProductionCoverageAuditReport{
		GeneratedAt:         asOf.UTC(),
		TotalRecords:        len(manifest.Records),
		RequiredModuleCount: len(required0618ProductionModules()),
		StatusCounts:        make(map[ProductionCoverageStatus]int),
		Records:             make([]ProductionCoverageAuditRecord, 0, len(manifest.Records)),
		ActiveExemptions:    ActiveProductionCoverageExemptions(manifest, asOf),
		ExpiredExemptions: productionCoverageExemptions(manifest, func(expiry time.Time) bool {
			return !expiry.IsZero() && !expiry.After(asOf)
		}),
		UndatedExemptions: productionCoverageExemptions(manifest, func(expiry time.Time) bool {
			return expiry.IsZero()
		}),
	}
	for _, record := range manifest.Records {
		report.StatusCounts[record.Status]++
		report.Records = append(report.Records, ProductionCoverageAuditRecord{
			Domain:                strings.TrimSpace(record.Domain),
			Feature:               strings.TrimSpace(record.Feature),
			EventType:             strings.TrimSpace(record.EventType),
			Status:                record.Status,
			MappingRuleID:         strings.TrimSpace(record.MappingRuleID),
			ProductionPublisher:   len(record.ProductionPublishers),
			TestEvidence:          len(record.TestEvidence),
			ContextGraphAssertion: len(record.ContextGraphAssertions),
			HasExemption:          record.Exemption != nil,
		})
	}
	sort.Slice(report.Records, func(i, j int) bool {
		if report.Records[i].Domain != report.Records[j].Domain {
			return report.Records[i].Domain < report.Records[j].Domain
		}
		return report.Records[i].EventType < report.Records[j].EventType
	})
	return report
}

func (r ProductionCoverageAuditReport) LogLine() string {
	return fmt.Sprintf(
		"context graph production gate audit: generated_at=%s total_records=%d required_modules=%d production_connected=%d partial_connected=%d seeded_only=%d not_connected=%d consumer_deferred=%d active_exemptions=%d expired_exemptions=%d undated_exemptions=%d",
		r.GeneratedAt.Format(time.RFC3339),
		r.TotalRecords,
		r.RequiredModuleCount,
		r.StatusCounts[ProductionCoverageProductionConnected],
		r.StatusCounts[ProductionCoveragePartialConnected],
		r.StatusCounts[ProductionCoverageSeededOnly],
		r.StatusCounts[ProductionCoverageNotConnected],
		r.StatusCounts[ProductionCoverageConsumerDeferred],
		len(r.ActiveExemptions),
		len(r.ExpiredExemptions),
		len(r.UndatedExemptions),
	)
}

func ActiveProductionCoverageExemptions(manifest ProductionCoverageManifest, asOf time.Time) []ProductionCoverageExemptionSummary {
	return productionCoverageExemptions(manifest, func(expiry time.Time) bool {
		return !expiry.IsZero() && expiry.After(asOf)
	})
}

func productionCoverageExemptions(manifest ProductionCoverageManifest, include func(time.Time) bool) []ProductionCoverageExemptionSummary {
	out := make([]ProductionCoverageExemptionSummary, 0)
	for _, record := range manifest.Records {
		if record.Exemption == nil || !include(record.Exemption.ExpiresAt) {
			continue
		}
		out = append(out, ProductionCoverageExemptionSummary{
			Domain:    strings.TrimSpace(record.Domain),
			Feature:   strings.TrimSpace(record.Feature),
			EventType: strings.TrimSpace(record.EventType),
			Status:    record.Status,
			Owner:     strings.TrimSpace(record.Exemption.Owner),
			Reason:    strings.TrimSpace(record.Exemption.Reason),
			ExpiresAt: record.Exemption.ExpiresAt,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Domain != out[j].Domain {
			return out[i].Domain < out[j].Domain
		}
		return out[i].EventType < out[j].EventType
	})
	return out
}

func KnownProductionOntologyPublishers() []ProductionEventPublisherEvidence {
	publishers := []ProductionEventPublisherEvidence{
		{EventType: "mission.created", Path: "cmd/server/production_api.go:48"},
		{EventType: "mission.created", Path: "cmd/server/production_project_ontology_api.go:42"},
		{EventType: "mission.task.created", Path: "cmd/server/production_api.go:172"},
		{EventType: "mission.task.created", Path: "cmd/server/production_api.go:178"},
		{EventType: "project.created", Path: "cmd/server/production_project_api.go:20"},
		{EventType: "project.created", Path: "cmd/server/production_project_ontology_api.go:76"},
		{EventType: "project.updated", Path: "cmd/server/production_project_api.go:71"},
		{EventType: "project.updated", Path: "cmd/server/production_project_ontology_api.go:97"},
		{EventType: "project.archived", Path: "cmd/server/production_project_api.go:88"},
		{EventType: "project.archived", Path: "cmd/server/production_project_ontology_api.go:117"},
		{EventType: "project.organization.linked", Path: "cmd/server/production_project_api.go:23"},
		{EventType: "project.organization.linked", Path: "cmd/server/production_project_api.go:74"},
		{EventType: "project.organization.linked", Path: "cmd/server/production_project_api.go:91"},
		{EventType: "project.organization.linked", Path: "cmd/server/production_project_ontology_api.go:137"},
		{EventType: "organization.created", Path: "cmd/server/production_resource_operations_api.go"},
		{EventType: "people.subject.v2.changed", Path: "cmd/server/production_people_ingestion.go"},
		{EventType: "people.org_unit.v2.changed", Path: "cmd/server/production_people_ingestion.go"},
		{EventType: "people.job_profile.v2.changed", Path: "cmd/server/production_people_ingestion.go"},
		{EventType: "people.position.v2.changed", Path: "cmd/server/production_people_ingestion.go"},
		{EventType: "people.assignment.v2.changed", Path: "cmd/server/production_people_ingestion.go"},
		{EventType: "people.reporting_relationship.v2.changed", Path: "cmd/server/production_people_ingestion.go"},
		{EventType: "people.responsibility.v2.changed", Path: "cmd/server/production_people_ingestion.go"},
		{EventType: "people.responsibility_binding.v2.changed", Path: "cmd/server/production_people_ingestion.go"},
		{EventType: "people.department.created", Path: "cmd/server/production_resource_operations_api.go"},
		{EventType: "people.position.created", Path: "cmd/server/production_resource_operations_api.go"},
		{EventType: "people.person.updated", Path: "cmd/server/production_resource_operations_api.go"},
		{EventType: "people.agent.updated", Path: "cmd/server/production_resource_operations_api.go"},
		{EventType: "people.agent.updated", Path: "cmd/server/production_agent_execution_ontology.go"},
		{EventType: "people.position.assigned", Path: "cmd/server/production_resource_operations_api.go"},
		{EventType: "planning.idea.captured", Path: "cmd/server/production_todo_ontology.go"},
		{EventType: "planning.idea.converted_to_todo", Path: "cmd/server/production_todo_ontology.go"},
		{EventType: "planning.todo.changed", Path: "cmd/server/production_todo_ontology.go"},
		{EventType: "todo.created", Path: "cmd/server/todo_creation_ontology.go"},
		{EventType: "planning.todo.comment.linked", Path: "cmd/server/production_todo_ontology.go"},
		{EventType: "planning.idea.comment.linked", Path: "cmd/server/production_todo_ontology.go"},
		{EventType: "knowledge.document.metadata.updated", Path: "internal/app/knowledgeadmin/command_service.go:741"},
		{EventType: "knowledge.document.metadata.updated", Path: "internal/knowledge/ingest/service.go:302"},
		{EventType: "knowledge.document.metadata.updated", Path: "cmd/server/production_ontology.go:492"},
		{EventType: "knowledge.document.status.updated", Path: "internal/app/knowledgeadmin/command_service.go:772"},
		{EventType: "knowledge.document.status.updated", Path: "internal/knowledge/ingest/service.go:392"},
		{EventType: "knowledge.document.status.updated", Path: "cmd/server/production_ontology.go:496"},
		{EventType: "knowledge.chunk.metadata.updated", Path: "internal/knowledge/ingest/service.go:338"},
		{EventType: "knowledge.chunk.metadata.updated", Path: "cmd/server/production_ontology.go:500"},
		{EventType: "knowledge.page.metadata.updated", Path: "internal/app/knowledgeadmin/command_service.go:817"},
		{EventType: "knowledge.page.metadata.updated", Path: "cmd/server/production_ontology.go:504"},
		{EventType: "knowledge.anchor.metadata.updated", Path: "internal/app/knowledgeadmin/command_service.go:913"},
		{EventType: "knowledge.anchor.metadata.updated", Path: "cmd/server/production_ontology.go:508"},
		{EventType: "memory.record.accepted", Path: "internal/app/memoryadmin/service.go:248"},
		{EventType: "memory.record.accepted", Path: "cmd/server/production_ontology.go:53"},
		{EventType: "memory.record.lifecycle.updated", Path: "internal/app/memoryadmin/service.go:272"},
		{EventType: "memory.record.lifecycle.updated", Path: "cmd/server/production_ontology.go:53"},
		{EventType: "memory.knowledge.citation.added", Path: "internal/app/memoryadmin/service.go:293"},
		{EventType: "memory.knowledge.citation.added", Path: "cmd/server/production_ontology.go:53"},
		{EventType: "memory.knowledge_chunk.citation.added", Path: "internal/app/memoryadmin/service.go:317"},
		{EventType: "memory.knowledge_chunk.citation.added", Path: "cmd/server/production_ontology.go:53"},
		{EventType: "memory.knowledge_anchor.citation.added", Path: "internal/app/memoryadmin/service.go:341"},
		{EventType: "memory.knowledge_anchor.citation.added", Path: "cmd/server/production_ontology.go:53"},
		{EventType: "memory.scope.linked", Path: "internal/app/memoryadmin/service.go:376"},
		{EventType: "memory.scope.linked", Path: "cmd/server/production_ontology.go:53"},
		{EventType: "memory.lineage.linked", Path: "internal/app/memoryadmin/service.go:400"},
		{EventType: "memory.lineage.linked", Path: "cmd/server/production_ontology.go:53"},
	}
	for _, record := range catalogInitializationProductionCoverageRecords() {
		publishers = append(publishers, ProductionEventPublisherEvidence{EventType: record.EventType, Path: evidenceFilePath(record.ProductionPublishers[0])})
	}
	for _, record := range agentExecutionProductionCoverageRecords() {
		publishers = append(publishers, ProductionEventPublisherEvidence{EventType: record.EventType, Path: agentExecutionProductionPublisherPath(record.EventType)})
	}
	for _, record := range reliabilityProductionCoverageRecords() {
		publishers = append(publishers, ProductionEventPublisherEvidence{EventType: record.EventType, Path: "cmd/server/production_reliability_ontology.go"})
	}
	for _, record := range problemHubProductionCoverageRecords() {
		publishers = append(publishers, ProductionEventPublisherEvidence{EventType: record.EventType, Path: evidenceFilePath(record.ProductionPublishers[0])})
	}
	for _, record := range improvementProductionCoverageRecords() {
		publishers = append(publishers, ProductionEventPublisherEvidence{EventType: record.EventType, Path: evidenceFilePath(record.ProductionPublishers[0])})
	}
	return publishers
}

func ValidateProductionCoverageGate(rules []MappingRule, manifest ProductionCoverageManifest, asOf time.Time) ProductionCoverageGateReport {
	return ValidateProductionCoverageGateWithPublishers(rules, manifest, nil, asOf)
}

func ValidateProductionCoverageGateWithPublishers(rules []MappingRule, manifest ProductionCoverageManifest, publishers []ProductionEventPublisherEvidence, asOf time.Time) ProductionCoverageGateReport {
	return validateProductionCoverageGate(rules, manifest, publishers, asOf, required0618CoverageRecords())
}

func validateProductionCoverageGate(rules []MappingRule, manifest ProductionCoverageManifest, publishers []ProductionEventPublisherEvidence, asOf time.Time, requiredRecords []ProductionCoverageRecord) ProductionCoverageGateReport {
	enabledRules := make(map[string]MappingRule)
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		enabledRules[strings.TrimSpace(rule.EventType)] = rule
	}

	records := make(map[string]ProductionCoverageRecord)
	var report ProductionCoverageGateReport
	for _, record := range manifest.Records {
		eventType := strings.TrimSpace(record.EventType)
		if eventType == "" {
			report.addIssue(record, "missing_event_type", "context graph production gate: record missing event type")
			continue
		}
		validateCoverageRecordShape(&report, record)
		if _, exists := records[eventType]; exists {
			report.addIssue(record, "duplicate_coverage_record", fmt.Sprintf("context graph production gate: %s duplicate coverage record", eventType))
			continue
		}
		records[eventType] = record
	}

	for eventType := range enabledRules {
		if _, exists := records[eventType]; !exists {
			report.addIssue(ProductionCoverageRecord{EventType: eventType}, "missing_coverage_record", fmt.Sprintf("context graph production gate: %s missing coverage record", eventType))
		}
	}
	for _, required := range requiredRecords {
		record, exists := records[required.EventType]
		if !exists {
			report.addIssue(required, "missing_0618_coverage_record", gateMessage(required, "missing 0618 coverage record"))
			continue
		}
		if strings.TrimSpace(record.Domain) != strings.TrimSpace(required.Domain) {
			report.addIssue(record, "mismatched_0618_coverage_domain", gateMessage(record, "expected 0618 coverage domain "+required.Domain))
		}
		if record.Status == ProductionCoverageConsumerDeferred {
			report.addIssue(record, "consumer_deferred_production_coverage", gateMessage(record, "status consumer_deferred cannot classify 0618 production coverage"))
		}
	}
	validateRequired0618ProductionModules(&report, manifest)

	for _, publisher := range publishers {
		eventType := strings.TrimSpace(publisher.EventType)
		if eventType == "" {
			continue
		}
		if path := missingEvidenceFile(publisher.Path); path != "" {
			report.addIssue(ProductionCoverageRecord{EventType: eventType}, "missing_known_production_publisher_file", fmt.Sprintf("context graph production gate: %s known production publisher file missing %s", eventType, path))
		}
		if _, exists := records[eventType]; !exists {
			report.addIssue(ProductionCoverageRecord{EventType: eventType}, "publisher_missing_coverage_record", fmt.Sprintf("context graph production gate: %s production publisher missing coverage record", eventType))
		}
	}
	knownPublisherPaths := knownPublisherPathSet(publishers)
	for _, publisher := range publishers {
		eventType := strings.TrimSpace(publisher.EventType)
		record, exists := records[eventType]
		if eventType == "" || !exists {
			continue
		}
		path := evidenceFilePath(publisher.Path)
		if path == "" {
			continue
		}
		if !containsEvidencePath(record.ProductionPublishers, path) {
			report.addIssue(record, "known_production_publisher_not_declared", gateMessage(record, "known production publisher not declared "+path))
		}
	}

	for _, record := range manifest.Records {
		rule, hasRule := enabledRules[record.EventType]
		switch record.Status {
		case ProductionCoverageProductionConnected, ProductionCoveragePartialConnected:
			if !hasRule {
				report.addIssue(record, "missing_mapping_rule", gateMessage(record, "missing mapping rule"))
			}
			validateConnectedStatusHasNoExemption(&report, record)
			validateMappingRuleID(&report, record, rule, hasRule)
			if len(record.ProductionPublishers) == 0 {
				report.addIssue(record, "missing_production_publisher", gateMessage(record, "missing production publisher"))
			}
			for _, evidence := range record.ProductionPublishers {
				if path := missingEvidenceFile(evidence); path != "" {
					report.addIssue(record, "missing_production_publisher_file", gateMessage(record, "production publisher evidence file missing "+path))
				}
				if len(publishers) > 0 && !knownPublisherPaths[record.EventType][evidenceFilePath(evidence)] {
					report.addIssue(record, "production_publisher_not_registered", gateMessage(record, "production publisher evidence not registered "+evidenceFilePath(evidence)))
				}
			}
			if !publisherEvidenceContainsEventType(record.EventType, record.ProductionPublishers) {
				report.addIssue(record, "production_publisher_missing_event_type", gateMessage(record, "production publisher evidence does not contain event type "+record.EventType))
			}
			if len(record.TestEvidence) == 0 {
				report.addIssue(record, "missing_production_test_evidence", gateMessage(record, "missing production test evidence"))
			}
			for _, evidence := range record.TestEvidence {
				if path := missingEvidenceFile(evidence.Path); path != "" {
					report.addIssue(record, "missing_production_test_file", gateMessage(record, "production test evidence file missing "+path))
					continue
				}
				path := evidenceFilePath(evidence.Path)
				if path != "" && !strings.HasSuffix(path, "_test.go") {
					report.addIssue(record, "production_test_evidence_not_test_file", gateMessage(record, "production test evidence must point to a _test.go file "+path))
				}
				if path != "" && !testEvidenceContainsEventType(record.EventType, evidence.Path) {
					report.addIssue(record, "production_test_evidence_missing_event_type", gateMessage(record, "production "+string(evidence.Kind)+" test evidence does not contain event type "+record.EventType))
				}
			}
			for _, kind := range requiredProductionTestEvidenceKinds() {
				evidence, ok := testEvidenceByKind(record.TestEvidence, kind)
				if !ok {
					report.addIssue(record, "missing_production_test_evidence_kind", gateMessage(record, "missing production test evidence kind "+string(kind)))
					continue
				}
				if kind != ProductionTestEvidenceSuccess && strings.TrimSpace(evidence.Reason) == "" {
					report.addIssue(record, "missing_production_test_evidence_reason", gateMessage(record, "production test evidence kind "+string(kind)+" missing reason"))
				}
			}
			if len(record.ContextGraphAssertions) == 0 {
				report.addIssue(record, "missing_contextgraph_assertion_evidence", gateMessage(record, "missing contextgraph assertion evidence"))
			}
			validateContextGraphAssertions(&report, record, rule, hasRule)
			validateDefinitionGovernanceEvidence(&report, record)
			validateRequiredEventMetadata(&report, record)
			if record.Status == ProductionCoverageProductionConnected && len(record.KnownGaps) > 0 {
				report.addIssue(record, "unexpected_known_gap", gateMessage(record, "status production_connected cannot declare known gaps"))
			}
			if record.Status == ProductionCoveragePartialConnected && len(record.KnownGaps) == 0 {
				report.addIssue(record, "missing_known_gap", gateMessage(record, "partial connection missing known gap"))
			}
		case ProductionCoverageSeededOnly:
			if !hasRule {
				report.addIssue(record, "missing_mapping_rule", gateMessage(record, "missing mapping rule"))
			}
			validateNoProductionEvidenceForIncompleteStatus(&report, record)
			validateMappingRuleID(&report, record, rule, hasRule)
			validateDefinitionGovernanceEvidence(&report, record)
			validateRequiredEventMetadata(&report, record)
			validateCoverageExemption(&report, record, asOf)
		case ProductionCoverageNotConnected:
			if hasRule {
				report.addIssue(record, "unexpected_mapping_rule", gateMessage(record, "status not_connected but mapping rule exists"))
			}
			validateNoProductionEvidenceForIncompleteStatus(&report, record)
			validateNoMappingEvidenceForNotConnectedStatus(&report, record)
			validateBusinessSourceEvidence(&report, record)
			validateCoverageExemption(&report, record, asOf)
		case ProductionCoverageConsumerDeferred:
			validateCoverageExemption(&report, record, asOf)
		default:
			report.addIssue(record, "missing_status", gateMessage(record, "missing coverage status"))
		}
		if hasRule {
			for _, field := range requiredPayloadFields(rule) {
				if !containsString(record.RequiredPayloadFields, field) {
					report.addIssue(record, "missing_required_payload_field", gateMessage(record, "missing required payload field "+field))
				}
			}
		}
	}
	return report
}

func validateRequired0618ProductionModules(report *ProductionCoverageGateReport, manifest ProductionCoverageManifest) {
	modulesByEventType := required0618ProductionModulesByEventType()
	for _, record := range manifest.Records {
		module, ok := modulesByEventType[strings.TrimSpace(record.EventType)]
		if !ok {
			continue
		}
		if record.Status != ProductionCoverageNotConnected {
			continue
		}
		for _, evidence := range record.BusinessSourceEvidence {
			path := evidenceFilePath(evidence)
			if path == "" || missingEvidenceFile(evidence) != "" {
				continue
			}
			if !productionCoverageEvidenceInPrefixes(path, module.BusinessSourcePrefixes) {
				report.addIssue(record, "business_source_evidence_outside_required_module", gateMessage(record, fmt.Sprintf("business source evidence %s outside required 0618 production module %s", path, module.Name)))
			}
		}
	}
}

func validateConnectedStatusHasNoExemption(report *ProductionCoverageGateReport, record ProductionCoverageRecord) {
	if record.Exemption != nil {
		report.addIssue(record, "unexpected_exemption", gateMessage(record, "status "+string(record.Status)+" cannot declare exemption"))
	}
}

func validateNoProductionEvidenceForIncompleteStatus(report *ProductionCoverageGateReport, record ProductionCoverageRecord) {
	if len(record.ProductionPublishers) > 0 {
		report.addIssue(record, "unexpected_production_publisher", gateMessage(record, "status "+string(record.Status)+" cannot declare production publisher"))
	}
	if len(record.TestEvidence) > 0 {
		report.addIssue(record, "unexpected_production_test_evidence", gateMessage(record, "status "+string(record.Status)+" cannot declare production test evidence"))
	}
	if len(record.ContextGraphAssertions) > 0 {
		report.addIssue(record, "unexpected_contextgraph_assertion", gateMessage(record, "status "+string(record.Status)+" cannot declare contextgraph assertions"))
	}
}

func validateNoMappingEvidenceForNotConnectedStatus(report *ProductionCoverageGateReport, record ProductionCoverageRecord) {
	if strings.TrimSpace(record.MappingRuleID) != "" {
		report.addIssue(record, "unexpected_mapping_rule_id", gateMessage(record, "status not_connected cannot declare mapping rule id"))
	}
	if len(record.RequiredEventMetadata) > 0 {
		report.addIssue(record, "unexpected_required_event_metadata", gateMessage(record, "status not_connected cannot declare required event metadata"))
	}
	if len(record.RequiredPayloadFields) > 0 {
		report.addIssue(record, "unexpected_required_payload_fields", gateMessage(record, "status not_connected cannot declare required payload fields"))
	}
	if len(record.DefinitionGovernanceEvidence) > 0 {
		report.addIssue(record, "unexpected_definition_governance_evidence", gateMessage(record, "status not_connected cannot declare definition governance evidence"))
	}
}

func knownPublisherPathSet(publishers []ProductionEventPublisherEvidence) map[string]map[string]bool {
	out := make(map[string]map[string]bool)
	for _, publisher := range publishers {
		eventType := strings.TrimSpace(publisher.EventType)
		path := evidenceFilePath(publisher.Path)
		if eventType == "" || path == "" {
			continue
		}
		if out[eventType] == nil {
			out[eventType] = make(map[string]bool)
		}
		out[eventType][path] = true
	}
	return out
}

func partialCoverage(domain string, feature string, eventType string, payloadFields []string, publishers []string, testEvidence []ProductionTestEvidence, assertions []string, gaps []string) ProductionCoverageRecord {
	return ProductionCoverageRecord{
		Domain:                       domain,
		Feature:                      feature,
		EventType:                    eventType,
		MappingRuleID:                eventType,
		Status:                       ProductionCoveragePartialConnected,
		RequiredEventMetadata:        requiredProductionEventMetadata(),
		RequiredPayloadFields:        payloadFields,
		ProductionPublishers:         publishers,
		DefinitionGovernanceEvidence: defaultDefinitionGovernanceEvidence(),
		TestEvidence:                 testEvidence,
		ContextGraphAssertions:       assertions,
		KnownGaps:                    gaps,
	}
}

func seededOnlyCoverage(domain string, feature string, eventType string, payloadFields []string, exemption ProductionCoverageExemption) ProductionCoverageRecord {
	return ProductionCoverageRecord{
		Domain:                       domain,
		Feature:                      feature,
		EventType:                    eventType,
		MappingRuleID:                eventType,
		Status:                       ProductionCoverageSeededOnly,
		RequiredEventMetadata:        requiredProductionEventMetadata(),
		RequiredPayloadFields:        payloadFields,
		DefinitionGovernanceEvidence: defaultDefinitionGovernanceEvidence(),
		Exemption:                    &exemption,
	}
}

func notConnectedCoverage(domain string, feature string, eventType string, businessSourceEvidence []string, exemption ProductionCoverageExemption) ProductionCoverageRecord {
	return ProductionCoverageRecord{
		Domain:                 domain,
		Feature:                feature,
		EventType:              eventType,
		Status:                 ProductionCoverageNotConnected,
		BusinessSourceEvidence: businessSourceEvidence,
		Exemption:              &exemption,
	}
}

func publishers(paths ...string) []string {
	return compactStrings(paths)
}

func tests(values ...any) []ProductionTestEvidence {
	out := make([]ProductionTestEvidence, 0, len(values))
	for _, value := range values {
		switch typed := value.(type) {
		case string:
			path := strings.TrimSpace(typed)
			if path != "" {
				out = append(out, ProductionTestEvidence{Kind: ProductionTestEvidenceSuccess, Path: path})
			}
		case ProductionTestEvidence:
			if strings.TrimSpace(typed.Path) != "" {
				out = append(out, typed)
			}
		}
	}
	return out
}

func failureTest(path string, reason string) ProductionTestEvidence {
	return ProductionTestEvidence{Kind: ProductionTestEvidenceFailure, Path: path, Reason: reason}
}

func replayTest(path string, reason string) ProductionTestEvidence {
	return ProductionTestEvidence{Kind: ProductionTestEvidenceReplay, Path: path, Reason: reason}
}

func eventReplayCoverageFailureTest() ProductionTestEvidence {
	return failureTest("cmd/server/production_ontology_projection_coverage_test.go", "projection coverage matrix verifies missing required payload failures for this event type")
}

func eventReplayCoverageReplayTest() ProductionTestEvidence {
	return replayTest("cmd/server/production_ontology_projection_coverage_test.go", "projection coverage matrix reapplies a retained event directly through the projection runner without a principal-less replay facade")
}

func tenantTest(path string, reason string) ProductionTestEvidence {
	return ProductionTestEvidence{Kind: ProductionTestEvidenceTenantIsolation, Path: path, Reason: reason}
}

func assertions(values ...string) []string {
	return compactStrings(values)
}

func businessSources(paths ...string) []string {
	return compactStrings(paths)
}

func gaps(values ...string) []string {
	return compactStrings(values)
}

func payload(fields ...string) []string {
	return compactStrings(fields)
}

func exemption(owner string, reason string, expiresAt time.Time) ProductionCoverageExemption {
	return ProductionCoverageExemption{Owner: owner, Reason: reason, ExpiresAt: expiresAt}
}

func validateCoverageExemption(report *ProductionCoverageGateReport, record ProductionCoverageRecord, asOf time.Time) {
	if record.Exemption == nil {
		report.addIssue(record, "missing_exemption", gateMessage(record, "requires explicit exemption for status "+string(record.Status)))
		return
	}
	if strings.TrimSpace(record.Exemption.Owner) == "" {
		report.addIssue(record, "missing_exemption_owner", gateMessage(record, "exemption missing owner"))
	}
	if strings.TrimSpace(record.Exemption.Reason) == "" {
		report.addIssue(record, "missing_exemption_reason", gateMessage(record, "exemption missing reason"))
	}
	if record.Exemption.ExpiresAt.IsZero() {
		report.addIssue(record, "missing_exemption_expiry", gateMessage(record, "exemption missing expiry"))
		return
	}
	if !record.Exemption.ExpiresAt.After(asOf) {
		report.addIssue(record, "expired_exemption", gateMessage(record, fmt.Sprintf("exemption expired owner=%s expires_at=%s", record.Exemption.Owner, record.Exemption.ExpiresAt.Format("2006-01-02"))))
	}
}

func validateBusinessSourceEvidence(report *ProductionCoverageGateReport, record ProductionCoverageRecord) {
	if len(record.BusinessSourceEvidence) == 0 {
		report.addIssue(record, "missing_business_source_evidence", gateMessage(record, "missing business source evidence"))
		return
	}
	for _, evidence := range record.BusinessSourceEvidence {
		if path := missingEvidenceFile(evidence); path != "" {
			report.addIssue(record, "missing_business_source_evidence_file", gateMessage(record, "business source evidence file missing "+path))
		}
	}
}

func validateMappingRuleID(report *ProductionCoverageGateReport, record ProductionCoverageRecord, rule MappingRule, hasRule bool) {
	mappingRuleID := strings.TrimSpace(record.MappingRuleID)
	if mappingRuleID == "" {
		report.addIssue(record, "missing_mapping_rule_id", gateMessage(record, "missing mapping rule id"))
		return
	}
	if mappingRuleID != strings.TrimSpace(record.EventType) {
		report.addIssue(record, "mapping_rule_id_event_type_mismatch", gateMessage(record, "mapping rule id "+mappingRuleID+" does not match event type "+record.EventType))
	}
	if hasRule && strings.TrimSpace(rule.ID) != "" && mappingRuleID != strings.TrimSpace(rule.ID) {
		report.addIssue(record, "mapping_rule_id_registry_mismatch", gateMessage(record, "mapping rule id "+mappingRuleID+" does not match registry rule id "+rule.ID))
	}
}

func validateRequiredEventMetadata(report *ProductionCoverageGateReport, record ProductionCoverageRecord) {
	for _, metadata := range requiredProductionEventMetadata() {
		if !containsString(record.RequiredEventMetadata, metadata) {
			report.addIssue(record, "missing_required_event_metadata", gateMessage(record, "missing required event metadata "+metadata))
		}
	}
}

func validateDefinitionGovernanceEvidence(report *ProductionCoverageGateReport, record ProductionCoverageRecord) {
	if len(record.DefinitionGovernanceEvidence) == 0 {
		report.addIssue(record, "missing_definition_governance_evidence", gateMessage(record, "missing definition governance evidence"))
		return
	}
	for _, evidence := range record.DefinitionGovernanceEvidence {
		if path := missingEvidenceFile(evidence); path != "" {
			report.addIssue(record, "missing_definition_governance_file", gateMessage(record, "definition governance evidence file missing "+path))
		}
	}
	if !definitionGovernanceEvidenceProvesPromotion(record.DefinitionGovernanceEvidence) {
		report.addIssue(record, "definition_governance_missing_promotion", gateMessage(record, "definition governance evidence does not prove 621 metadata proposal definition promotion"))
	}
}

func validateContextGraphAssertions(report *ProductionCoverageGateReport, record ProductionCoverageRecord, rule MappingRule, hasRule bool) {
	if len(record.ContextGraphAssertions) == 0 {
		return
	}
	if hasRule && mappingRuleHasTemplates(rule, "objects") && !hasAssertionPrefix(record.ContextGraphAssertions, "object:") {
		report.addIssue(record, "missing_contextgraph_object_assertion", gateMessage(record, "missing contextgraph object assertion"))
	}
	if hasRule && mappingRuleHasRequiredTemplates(rule, "links") && !hasAssertionPrefix(record.ContextGraphAssertions, "link:") {
		report.addIssue(record, "missing_contextgraph_link_assertion", gateMessage(record, "missing contextgraph link assertion"))
	}
	if hasRule && (mappingRuleHasTemplates(rule, "properties") || mappingRuleHasTemplates(rule, "facts")) && !hasAssertionPrefix(record.ContextGraphAssertions, "fact:") {
		report.addIssue(record, "missing_contextgraph_fact_assertion", gateMessage(record, "missing contextgraph fact assertion"))
	}
	if !hasAssertionPrefix(record.ContextGraphAssertions, "evidence:") {
		report.addIssue(record, "missing_contextgraph_evidence_assertion", gateMessage(record, "missing contextgraph evidence assertion"))
	}
}

func mappingRuleHasTemplates(rule MappingRule, key string) bool {
	if rule.Rule == nil {
		return false
	}
	value, ok := rule.Rule[key]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case []any:
		return len(typed) > 0
	case []map[string]any:
		return len(typed) > 0
	default:
		return false
	}
}

func mappingRuleHasRequiredTemplates(rule MappingRule, key string) bool {
	if rule.Rule == nil {
		return false
	}
	value, ok := rule.Rule[key]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			if !templateOptional(item) {
				return true
			}
		}
	case []map[string]any:
		for _, item := range typed {
			if !templateOptional(item) {
				return true
			}
		}
	}
	return false
}

func templateOptional(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		return boolValue(typed["optional"])
	default:
		return false
	}
}

func hasAssertionPrefix(assertions []string, prefix string) bool {
	for _, assertion := range assertions {
		if strings.HasPrefix(strings.TrimSpace(assertion), prefix) {
			return true
		}
	}
	return false
}

func definitionGovernanceEvidenceProvesPromotion(evidence []string) bool {
	for _, item := range evidence {
		path := evidenceFilePath(item)
		if path == "" || missingEvidenceFile(item) != "" {
			continue
		}
		diskPath := filepath.Join(moduleRootDir(), filepath.FromSlash(path))
		content, err := os.ReadFile(diskPath)
		if err != nil {
			continue
		}
		text := string(content)
		if strings.Contains(text, `MetadataKey: "definition"`) &&
			strings.Contains(text, "MetadataProposalPromotion") &&
			strings.Contains(text, "mapping_rule") {
			return true
		}
	}
	return false
}

func validateCoverageRecordShape(report *ProductionCoverageGateReport, record ProductionCoverageRecord) {
	if strings.TrimSpace(record.Domain) == "" {
		report.addIssue(record, "missing_domain", gateMessage(record, "missing domain"))
	}
	if strings.TrimSpace(record.Feature) == "" {
		report.addIssue(record, "missing_feature", gateMessage(record, "missing feature"))
	}
	reportDuplicateStrings(report, record, "production publisher evidence", record.ProductionPublishers, evidenceIdentity)
	reportDuplicateStrings(report, record, "definition governance evidence", record.DefinitionGovernanceEvidence, evidenceIdentity)
	reportDuplicateStrings(report, record, "business source evidence", record.BusinessSourceEvidence, evidenceIdentity)
	reportDuplicateStrings(report, record, "contextgraph assertion evidence", record.ContextGraphAssertions, func(value string) string {
		return strings.TrimSpace(value)
	})
	reportDuplicateTestEvidence(report, record)
}

func reportDuplicateStrings(report *ProductionCoverageGateReport, record ProductionCoverageRecord, label string, values []string, normalize func(string) string) {
	seen := map[string]bool{}
	for _, value := range values {
		normalized := normalize(value)
		if normalized == "" {
			continue
		}
		if seen[normalized] {
			report.addIssue(record, "duplicate_"+strings.ReplaceAll(label, " ", "_"), gateMessage(record, "duplicate "+label+" "+normalized))
			continue
		}
		seen[normalized] = true
	}
}

func evidenceIdentity(evidence string) string {
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.TrimSpace(evidence))))
}

func reportDuplicateTestEvidence(report *ProductionCoverageGateReport, record ProductionCoverageRecord) {
	seen := map[string]bool{}
	for _, evidence := range record.TestEvidence {
		path := evidenceFilePath(evidence.Path)
		if path == "" {
			continue
		}
		key := string(evidence.Kind) + " " + path
		if seen[key] {
			report.addIssue(record, "duplicate_production_test_evidence", gateMessage(record, "duplicate production test evidence "+key))
			continue
		}
		seen[key] = true
	}
}

func (r *ProductionCoverageGateReport) addIssue(record ProductionCoverageRecord, code string, message string) {
	r.Issues = append(r.Issues, ProductionCoverageGateIssue{
		Domain:    record.Domain,
		EventType: record.EventType,
		Code:      code,
		Message:   message,
	})
}

func gateMessage(record ProductionCoverageRecord, reason string) string {
	domain := strings.TrimSpace(record.Domain)
	eventType := strings.TrimSpace(record.EventType)
	if domain == "" {
		return fmt.Sprintf("context graph production gate: %s %s", eventType, reason)
	}
	return fmt.Sprintf("context graph production gate: %s/%s %s", domain, eventType, reason)
}

func requiredProductionEventMetadata() []string {
	return []string{"tenant_id", "actor", "stream_id", "version", "idempotency_or_correlation"}
}

func requiredProductionTestEvidenceKinds() []ProductionTestEvidenceKind {
	return []ProductionTestEvidenceKind{
		ProductionTestEvidenceSuccess,
		ProductionTestEvidenceFailure,
		ProductionTestEvidenceReplay,
		ProductionTestEvidenceTenantIsolation,
	}
}

func defaultDefinitionGovernanceEvidence() []string {
	return []string{
		"internal/app/ontology/metadata_proposal_promotion_test.go",
		"cmd/server/production_ontology_governance_command_api_test.go",
	}
}

func required0618CoverageRecords() []ProductionCoverageRecord {
	return []ProductionCoverageRecord{
		{Domain: "mission_control", EventType: "mission.created"},
		{Domain: "mission_control", EventType: "mission.task.created"},
		{Domain: "mission_control", EventType: "project.created"},
		{Domain: "mission_control", EventType: "project.updated"},
		{Domain: "mission_control", EventType: "project.archived"},
		{Domain: "mission_control", EventType: "project.organization.linked"},
		{Domain: "operations", EventType: "organization.created"},
		{Domain: "operations", EventType: "people.team.created"},
		{Domain: "operations", EventType: "people.person.updated"},
		{Domain: "operations", EventType: "people.membership.linked"},
		{Domain: "operations", EventType: "people.agent.updated"},
		{Domain: "operations", EventType: "people.department.created"},
		{Domain: "operations", EventType: "people.position.created"},
		{Domain: "operations", EventType: "people.position.assigned"},
		{Domain: "resource_admin", EventType: "resource.tool.registered"},
		{Domain: "planning", EventType: "planning.requirement.created"},
		{Domain: "planning", EventType: "planning.idea.captured"},
		{Domain: "planning", EventType: "planning.idea.converted_to_todo"},
		{Domain: "planning", EventType: "planning.todo.changed"},
		{Domain: "planning", EventType: "planning.todo.comment.linked"},
		{Domain: "planning", EventType: "planning.idea.comment.linked"},
		{Domain: "planning", EventType: "planning.decision.recorded"},
		{Domain: "planning", EventType: "planning.design_artifact.created"},
		{Domain: "action_contract", EventType: "action.run.started"},
		{Domain: "action_contract", EventType: "action.run.completed"},
		{Domain: "action_contract", EventType: "action.writeback.succeeded"},
		{Domain: "knowledge", EventType: "knowledge.document.metadata.updated"},
		{Domain: "knowledge", EventType: "knowledge.chunk.metadata.updated"},
		{Domain: "knowledge", EventType: "knowledge.page.metadata.updated"},
		{Domain: "knowledge", EventType: "knowledge.anchor.metadata.updated"},
		{Domain: "knowledge", EventType: "knowledge.document.status.updated"},
		{Domain: "memory", EventType: "memory.record.accepted"},
		{Domain: "memory", EventType: "memory.record.lifecycle.updated"},
		{Domain: "memory", EventType: "memory.scope.linked"},
		{Domain: "memory", EventType: "memory.lineage.linked"},
		{Domain: "memory", EventType: "memory.knowledge.citation.added"},
		{Domain: "memory", EventType: "memory.knowledge_chunk.citation.added"},
		{Domain: "memory", EventType: "memory.knowledge_anchor.citation.added"},
		{Domain: "legal", EventType: "legal.entity.created"},
		{Domain: "legal", EventType: "legal.contract.created"},
		{Domain: "legal", EventType: "legal.policy.created"},
		{Domain: "legal", EventType: "legal.approval_case.opened"},
		{Domain: "development", EventType: "development.repository.registered"},
		{Domain: "development", EventType: "development.issue.created"},
		{Domain: "development", EventType: "development.pull_request.opened"},
		{Domain: "development", EventType: "development.release.created"},
		{Domain: "value", EventType: "value.objective.created"},
		{Domain: "value", EventType: "value.metric.recorded"},
		{Domain: "value", EventType: "value.initiative.created"},
		{Domain: "value", EventType: "value.experiment.created"},
		{Domain: "todo", EventType: "todo.created"},
		{Domain: "discussion", EventType: "discussion.message.created"},
		{Domain: "scheduler", EventType: "scheduler.run.completed"},
		{Domain: "agent_runtime", EventType: "agent.runtime.run.completed"},
		{Domain: "permission", EventType: "permission.binding.updated"},
	}
}

func required0618ProductionModules() []ProductionCoverageModule {
	return []ProductionCoverageModule{
		{
			Domain:                 "mission_control",
			Name:                   "mission_control",
			EventTypes:             []string{"mission.created", "mission.task.created", "project.created", "project.updated", "project.archived", "project.organization.linked"},
			BusinessSourcePrefixes: []string{"cmd/server/production_api.go", "cmd/server/production_project_api.go", "cmd/server/production_project_ontology_api.go", "internal/app/missioncontrol/"},
		},
		{
			Domain:                 "operations",
			Name:                   "operations",
			EventTypes:             []string{"organization.created", "people.team.created", "people.person.updated", "people.membership.linked", "people.agent.updated", "people.department.created", "people.position.created", "people.position.assigned"},
			BusinessSourcePrefixes: []string{"cmd/server/production_resource_operations_api.go", "internal/app/operations/"},
		},
		{
			Domain:                 "resource_admin",
			Name:                   "resource_admin",
			EventTypes:             []string{"resource.tool.registered"},
			BusinessSourcePrefixes: []string{"cmd/server/production_resource_operations_api.go", "internal/app/resourceadmin/", "internal/resource/tool/"},
		},
		{
			Domain:                 "planning",
			Name:                   "planning",
			EventTypes:             []string{"planning.requirement.created", "planning.idea.captured", "planning.idea.converted_to_todo", "planning.todo.changed", "planning.todo.comment.linked", "planning.idea.comment.linked", "planning.decision.recorded", "planning.design_artifact.created"},
			BusinessSourcePrefixes: []string{"cmd/server/production_todo_ontology.go", "internal/app/todo/"},
		},
		{
			Domain:                 "action_contract",
			Name:                   "action_contract",
			EventTypes:             []string{"action.run.started", "action.run.completed", "action.writeback.succeeded"},
			BusinessSourcePrefixes: []string{"internal/app/actioncontract/"},
		},
		{
			Domain:                 "knowledge",
			Name:                   "knowledge",
			EventTypes:             []string{"knowledge.document.metadata.updated", "knowledge.chunk.metadata.updated", "knowledge.page.metadata.updated", "knowledge.anchor.metadata.updated", "knowledge.document.status.updated"},
			BusinessSourcePrefixes: []string{"cmd/server/production_ontology.go", "internal/app/knowledgeadmin/", "internal/knowledge/ingest/"},
		},
		{
			Domain:                 "memory",
			Name:                   "memory",
			EventTypes:             []string{"memory.record.accepted", "memory.record.lifecycle.updated", "memory.scope.linked", "memory.lineage.linked", "memory.knowledge.citation.added", "memory.knowledge_chunk.citation.added", "memory.knowledge_anchor.citation.added"},
			BusinessSourcePrefixes: []string{"cmd/server/production_ontology.go", "internal/app/memoryadmin/"},
		},
		{
			Domain:                 "legal",
			Name:                   "legal",
			EventTypes:             []string{"legal.entity.created", "legal.contract.created", "legal.policy.created", "legal.approval_case.opened"},
			BusinessSourcePrefixes: []string{"internal/app/missioncontrol/approval_service.go", "internal/app/operations/approval_policy.go"},
		},
		{
			Domain:                 "development",
			Name:                   "development",
			EventTypes:             []string{"development.repository.registered", "development.issue.created", "development.pull_request.opened", "development.release.created"},
			BusinessSourcePrefixes: []string{"cmd/server/agent_templates/", "internal/app/missioncontrol/"},
		},
		{
			Domain:                 "value",
			Name:                   "value",
			EventTypes:             []string{"value.objective.created", "value.metric.recorded", "value.initiative.created", "value.experiment.created"},
			BusinessSourcePrefixes: []string{"internal/app/operations/evaluation_", "cmd/server/production_ontology_metric_sweeper.go"},
		},
		{
			Domain:                 "todo",
			Name:                   "todo",
			EventTypes:             []string{"todo.created"},
			BusinessSourcePrefixes: []string{"internal/app/todo/", "cmd/server/todo_", "internal/resource/tool/std/todo_"},
		},
		{
			Domain:                 "discussion",
			Name:                   "discussion",
			EventTypes:             []string{"discussion.message.created"},
			BusinessSourcePrefixes: []string{"internal/app/discussionsession/", "internal/mission/discussion/", "cmd/server/discussion_"},
		},
		{
			Domain:                 "scheduler",
			Name:                   "scheduler",
			EventTypes:             []string{"scheduler.run.completed"},
			BusinessSourcePrefixes: []string{"internal/app/scheduler/", "cmd/server/production_scheduler_agent_task.go"},
		},
		{
			Domain:                 "agent_runtime",
			Name:                   "agent_runtime",
			EventTypes:             []string{"agent.runtime.run.completed"},
			BusinessSourcePrefixes: []string{"cmd/server/production_agent_", "cmd/server/default_agent_runtime.go", "cmd/server/production_runtime_tool_dispatcher.go", "internal/agent/runtime/"},
		},
		{
			Domain:                 "permission",
			Name:                   "permission",
			EventTypes:             []string{"permission.binding.updated"},
			BusinessSourcePrefixes: []string{"internal/app/operations/command_roles.go", "internal/app/operations/permission_decision.go", "internal/app/operations/query_permissions.go", "internal/app/rebac/", "cmd/server/production_resource_operations_api.go"},
		},
	}
}

func required0618ProductionModulesByEventType() map[string]ProductionCoverageModule {
	out := make(map[string]ProductionCoverageModule)
	for _, module := range required0618ProductionModules() {
		for _, eventType := range module.EventTypes {
			out[strings.TrimSpace(eventType)] = module
		}
	}
	return out
}

func productionCoverageEvidenceInPrefixes(path string, prefixes []string) bool {
	path = evidenceFilePath(path)
	for _, prefix := range prefixes {
		prefix = evidenceFilePath(prefix)
		if prefix == "" {
			continue
		}
		if strings.HasSuffix(prefix, "/") {
			if strings.HasPrefix(path, prefix) {
				return true
			}
			continue
		}
		if path == prefix || strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func testEvidenceByKind(evidence []ProductionTestEvidence, kind ProductionTestEvidenceKind) (ProductionTestEvidence, bool) {
	for _, item := range evidence {
		if item.Kind == kind {
			return item, true
		}
	}
	return ProductionTestEvidence{}, false
}

func requiredPayloadFields(rule MappingRule) []string {
	fields := make(map[string]struct{})
	collectRequiredPayloadFields(rule.Rule, false, fields)
	out := make([]string, 0, len(fields))
	for field := range fields {
		out = append(out, field)
	}
	sort.Strings(out)
	return out
}

func collectRequiredPayloadFields(value any, optional bool, fields map[string]struct{}) {
	switch typed := value.(type) {
	case map[string]any:
		currentOptional := optional || boolValue(typed["optional"])
		for key, nested := range typed {
			if key == "optional" {
				continue
			}
			collectRequiredPayloadFields(nested, currentOptional, fields)
		}
	case []any:
		for _, nested := range typed {
			collectRequiredPayloadFields(nested, optional, fields)
		}
	case string:
		if optional {
			return
		}
		for _, field := range payloadFieldRefs(typed) {
			fields[field] = struct{}{}
		}
	}
}

func payloadFieldRefs(value string) []string {
	refs := make(map[string]struct{})
	for _, match := range payloadRefPattern.FindAllStringSubmatch(value, -1) {
		if len(match) > 1 && match[1] != "" {
			refs[match[1]] = struct{}{}
		}
	}
	for _, match := range canonicalRefPattern.FindAllStringSubmatch(value, -1) {
		if len(match) > 1 && match[1] != "" {
			refs[match[1]] = struct{}{}
		}
	}
	out := make([]string, 0, len(refs))
	for ref := range refs {
		out = append(out, ref)
	}
	sort.Strings(out)
	return out
}

var (
	payloadRefPattern   = regexp.MustCompile(`\$\.([A-Za-z0-9_]+)`)
	canonicalRefPattern = regexp.MustCompile(`\{([A-Za-z0-9_]+)\}`)
)

func boolValue(value any) bool {
	typed, ok := value.(bool)
	return ok && typed
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}

func compactStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func containsEvidencePath(values []string, want string) bool {
	want = evidenceFilePath(want)
	for _, value := range values {
		if evidenceFilePath(value) == want {
			return true
		}
	}
	return false
}

func missingEvidenceFile(evidence string) string {
	path := evidenceFilePath(evidence)
	if path == "" {
		return ""
	}
	diskPath := filepath.Join(moduleRootDir(), filepath.FromSlash(path))
	if _, err := os.Stat(diskPath); err == nil {
		return ""
	}
	return path
}

func publisherEvidenceContainsEventType(eventType string, evidence []string) bool {
	eventType = strings.TrimSpace(eventType)
	if eventType == "" {
		return true
	}
	for _, item := range evidence {
		path := evidenceFilePath(item)
		if path == "" || missingEvidenceFile(item) != "" {
			continue
		}
		diskPath := filepath.Join(moduleRootDir(), filepath.FromSlash(path))
		content, err := os.ReadFile(diskPath)
		if err != nil {
			continue
		}
		if strings.Contains(string(content), eventType) {
			return true
		}
	}
	return false
}

func testEvidenceContainsEventType(eventType string, evidence string) bool {
	eventType = strings.TrimSpace(eventType)
	if eventType == "" {
		return true
	}
	path := evidenceFilePath(evidence)
	if path == "" || missingEvidenceFile(evidence) != "" {
		return true
	}
	diskPath := filepath.Join(moduleRootDir(), filepath.FromSlash(path))
	content, err := os.ReadFile(diskPath)
	if err != nil {
		return true
	}
	return strings.Contains(string(content), eventType)
}

func evidenceFilePath(evidence string) string {
	evidence = strings.TrimSpace(evidence)
	if evidence == "" {
		return ""
	}
	if idx := strings.LastIndex(evidence, ":"); idx > 1 {
		suffix := evidence[idx+1:]
		if allDigits(suffix) {
			evidence = evidence[:idx]
		}
	}
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(evidence)))
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func moduleRootDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			return "."
		}
		wd = parent
	}
}
