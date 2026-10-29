package ontology

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/platform/graphprotocol"
)

func materializedContextGraphObjects(objects []contextgraph.Object) []contextgraph.Object {
	materialized := make([]contextgraph.Object, 0, len(objects))
	for _, object := range objects {
		if object.Status == "reference_pending" && object.Properties["_orrery_reference"] == "pending" {
			continue
		}
		materialized = append(materialized, object)
	}
	return materialized
}

func TestAuditDefaultOntologyDefinitionsDoesNotMutateRegistry(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	custom := ObjectType{
		ID:               "tenant_case",
		TenantID:         "tenant-a",
		TenantScope:      TenantScopeTenant,
		DisplayName:      "Case",
		CanonicalKeyRule: "case:{id}",
	}
	if err := registry.SaveObjectType(ctx, custom); err != nil {
		t.Fatal(err)
	}

	report, err := AuditDefaultOntologyDefinitions(ctx, registry)
	if err != nil {
		t.Fatal(err)
	}
	if report.MissingObjectTypes == 0 || report.MissingLinkTypes == 0 || report.MissingMappingRules == 0 {
		t.Fatalf("missing report = %+v", report)
	}
	if _, err := registry.GetObjectType(ctx, "organization"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("audit mutated registry: %v", err)
	}
	got, err := registry.GetObjectType(ctx, custom.ID)
	if err != nil || got.TenantID != custom.TenantID {
		t.Fatalf("custom definition changed: %+v %v", got, err)
	}
}

func TestInstallDefaultOntologyDefinitions(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()

	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions second run: %v", err)
	}

	for _, id := range []string{"organization", "mission", "project", "task", "person", "agent", "tool", "mcp_server", "knowledge_document", "knowledge_chunk", "knowledge_page", "knowledge_anchor", "memory_record", "iris_snapshot", "action_run"} {
		if _, err := registry.GetObjectType(ctx, id); err != nil {
			t.Fatalf("default object type %s missing: %v", id, err)
		}
	}
	for _, id := range []string{"mission_has_task", "project_belongs_to_organization", "task_assigned_to_person", "agent_can_use_tool", "tool_backed_by_mcp_server", "knowledge_document_supports_mission", "knowledge_document_references_document", "knowledge_document_mentions_project", "knowledge_document_mentions_repository", "knowledge_chunk_belongs_to_document", "knowledge_page_belongs_to_document", "knowledge_anchor_belongs_to_page", "memory_cites_knowledge_document", "memory_cites_knowledge_chunk", "memory_cites_knowledge_anchor", "memory_distilled_from_memory", "memory_about_mission", "memory_about_project", "memory_about_task", "memory_mentions_person", "memory_mentions_agent", "memory_mentions_organization", "memory_mentions_mission", "memory_mentions_project", "memory_mentions_task", "memory_mentions_tool", "iris_snapshot_supports_action_run"} {
		if _, err := registry.GetLinkType(ctx, id); err != nil {
			t.Fatalf("default link type %s missing: %v", id, err)
		}
	}
	properties, err := registry.ListPropertyTypes(ctx, "mission")
	if err != nil {
		t.Fatalf("ListPropertyTypes: %v", err)
	}
	if len(properties) == 0 {
		t.Fatalf("mission property types should be seeded")
	}
	rules, err := registry.ListMappingRules(ctx, "")
	if err != nil {
		t.Fatalf("ListMappingRules: %v", err)
	}
	if len(rules) == 0 {
		t.Fatalf("default mapping rules should be seeded")
	}
}

func TestDefaultOntologyIncludesActionRunLifecycleGraph(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}

	linkType, err := registry.GetLinkType(ctx, "action_run_targets_task")
	if err != nil {
		t.Fatalf("GetLinkType(action_run_targets_task): %v", err)
	}
	if linkType.FromObjectType != "action_run" || linkType.ToObjectType != "task" {
		t.Fatalf("action_run_targets_task endpoints = %s -> %s", linkType.FromObjectType, linkType.ToObjectType)
	}
	if linkType.DisplayName != "动作运行指向任务" {
		t.Fatalf("action_run_targets_task display name = %q", linkType.DisplayName)
	}

	rules, err := registry.ListMappingRules(ctx, "")
	if err != nil {
		t.Fatalf("ListMappingRules: %v", err)
	}
	for _, id := range []string{"action.run.started", "action.run.completed", "action.writeback.succeeded", "action.writeback.failed"} {
		if !mappingRuleExists(rules, id) {
			t.Fatalf("mapping rule %s missing in %+v", id, rules)
		}
	}
}

func TestDefaultLinkTypesUseChineseDisplayNames(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}

	cases := map[string]string{
		"mission_has_task":                    "使命包含任务",
		"project_belongs_to_organization":     "项目归属组织",
		"task_assigned_to_person":             "任务分配给人员",
		"person_member_of_team":               "人员属于团队",
		"agent_can_use_tool":                  "Agent 可使用工具",
		"tool_backed_by_mcp_server":           "工具由 MCP 服务支撑",
		"knowledge_chunk_belongs_to_document": "知识片段归属文档",
		"memory_cites_knowledge_document":     "记忆引用知识文档",
		"memory_about_project":                "记忆关联项目",
		"iris_snapshot_supports_action_run":   "IRIS 快照支撑动作运行",
	}
	for id, want := range cases {
		linkType, err := registry.GetLinkType(ctx, id)
		if err != nil {
			t.Fatalf("GetLinkType(%s): %v", id, err)
		}
		if linkType.DisplayName != want {
			t.Fatalf("GetLinkType(%s).DisplayName = %q, want %q", id, linkType.DisplayName, want)
		}
	}
}

func TestInstallDefaultOntologyDefinitionsUpgradesLegacyEnglishLinkNames(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	legacy, err := registry.GetLinkType(ctx, "mission_has_task")
	if err != nil {
		t.Fatal(err)
	}
	legacy.DisplayName = "Mission has task"
	if _, err := registry.SaveLinkTypeWithExpectedVersion(ctx, legacy, legacy.Version); err != nil {
		t.Fatal(err)
	}
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions upgrade: %v", err)
	}
	upgraded, err := registry.GetLinkType(ctx, "mission_has_task")
	if err != nil {
		t.Fatal(err)
	}
	if upgraded.DisplayName != "使命包含任务" {
		t.Fatalf("DisplayName = %q", upgraded.DisplayName)
	}
}

func TestInstallDefaultOntologyDefinitionsPreservesAllowedActions(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()

	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	task, err := registry.GetObjectType(ctx, "task")
	if err != nil {
		t.Fatalf("GetObjectType(task): %v", err)
	}
	task.AllowedActions = append(task.AllowedActions, "operator.review")
	if err := registry.SaveObjectType(ctx, task); err != nil {
		t.Fatalf("SaveObjectType(task): %v", err)
	}

	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions second run: %v", err)
	}
	task, err = registry.GetObjectType(ctx, "task")
	if err != nil {
		t.Fatalf("GetObjectType(task) after reinstall: %v", err)
	}
	if !stringSliceContainsNormalized(task.AllowedActions, "operator.review") {
		t.Fatalf("task allowed actions after reinstall = %+v", task.AllowedActions)
	}
}

func TestInstallDefaultOntologyDefinitionsUpgradesManagedConversionRule(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("initial install: %v", err)
	}
	legacy := withDefaultMappingRuleGovernance([]MappingRule{legacyPlanningIdeaConvertedToTodoMappingRule()})[0]
	if err := registry.SaveMappingRule(ctx, legacy); err != nil {
		t.Fatalf("seed legacy rule: %v", err)
	}
	before := mappingRuleByID(t, ctx, registry, legacy.ID)

	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("upgrade install: %v", err)
	}
	after := mappingRuleByID(t, ctx, registry, legacy.ID)
	if len(after.ObjectTypes) != 0 || len(after.LinkTypes) != 1 || after.LinkTypes[0] != "idea_converted_to_todo" {
		t.Fatalf("upgraded conversion rule = %+v", after)
	}
	if after.Version <= before.Version {
		t.Fatalf("upgraded version = %d, want > %d", after.Version, before.Version)
	}
}

func TestInstallDefaultOntologyDefinitionsPreservesCustomizedConversionRule(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("initial install: %v", err)
	}
	custom := withDefaultMappingRuleGovernance([]MappingRule{legacyPlanningIdeaConvertedToTodoMappingRule()})[0]
	custom.Enabled = false
	if err := registry.SaveMappingRule(ctx, custom); err != nil {
		t.Fatalf("seed customized rule: %v", err)
	}

	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("repeat install: %v", err)
	}
	got := mappingRuleByID(t, ctx, registry, custom.ID)
	if got.Enabled || len(got.ObjectTypes) != 2 {
		t.Fatalf("customized conversion rule was overwritten: %+v", got)
	}
}

func TestInstallDefaultOntologyDefinitionsUpgradesManagedSchedulerTaskCreatedRule(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("initial install: %v", err)
	}
	legacy := legacySchedulerTaskCreatedRuleForTest(t)
	if err := registry.SaveMappingRule(ctx, legacy); err != nil {
		t.Fatalf("seed legacy rule: %v", err)
	}
	before := mappingRuleByID(t, ctx, registry, legacy.ID)

	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("upgrade install: %v", err)
	}
	after := mappingRuleByID(t, ctx, registry, legacy.ID)
	if len(after.LinkTypes) != 1 || after.LinkTypes[0] != "scheduled_task_schedules_agent_task" {
		t.Fatalf("upgraded scheduler task rule = %+v", after)
	}
	links, ok := after.Rule["links"].([]any)
	if !ok || len(links) != 1 {
		t.Fatalf("upgraded scheduler task links = %#v", after.Rule["links"])
	}
	if after.Version <= before.Version {
		t.Fatalf("upgraded version = %d, want > %d", after.Version, before.Version)
	}
}

func TestInstallDefaultOntologyDefinitionsPreservesCustomizedSchedulerTaskCreatedRule(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("initial install: %v", err)
	}
	custom := legacySchedulerTaskCreatedRuleForTest(t)
	custom.Enabled = false
	if err := registry.SaveMappingRule(ctx, custom); err != nil {
		t.Fatalf("seed customized rule: %v", err)
	}

	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("repeat install: %v", err)
	}
	got := mappingRuleByID(t, ctx, registry, custom.ID)
	if got.Enabled || len(got.LinkTypes) != 0 {
		t.Fatalf("customized scheduler task rule was overwritten: %+v", got)
	}
}

func TestInstallDefaultOntologyDefinitionsUpgradesManagedDynamicScopeDefinitions(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("initial install: %v", err)
	}
	for _, desired := range defaultObjectTypes() {
		if !supportsDynamicScopeProperties(desired.ID) {
			continue
		}
		legacy := desired
		legacy.Properties = nil
		for _, property := range desired.Properties {
			if !isDynamicScopeProperty(property.PropertyID) {
				legacy.Properties = append(legacy.Properties, property)
			}
		}
		current, err := registry.GetObjectType(ctx, legacy.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := registry.SaveObjectTypeWithExpectedVersion(ctx, legacy, current.Version); err != nil {
			t.Fatalf("seed legacy object type %s: %v", legacy.ID, err)
		}
	}
	for _, desired := range defaultMappingRules() {
		switch desired.ID {
		case "agent.task.upserted", "scheduler.task.created", "scheduler.task.updated", "scheduler.task.cancelled":
			current := mappingRuleByID(t, ctx, registry, desired.ID)
			if _, err := registry.SaveMappingRuleWithExpectedVersion(ctx, mappingRuleWithoutDynamicScopeProperties(desired), current.Version); err != nil {
				t.Fatalf("seed legacy mapping rule %s: %v", desired.ID, err)
			}
		}
	}

	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("upgrade install: %v", err)
	}
	for _, objectTypeID := range []string{"agent_task", "scheduled_task"} {
		objectType, err := registry.GetObjectType(ctx, objectTypeID)
		if err != nil {
			t.Fatal(err)
		}
		for _, propertyID := range []string{"target_mode", "source_scope_kind", "source_scope_id", "target_scope_kind", "target_scope_id"} {
			found := false
			for _, property := range objectType.Properties {
				found = found || property.PropertyID == propertyID
			}
			if !found {
				t.Errorf("upgraded object type %s is missing %s", objectTypeID, propertyID)
			}
		}
	}
	for _, ruleID := range []string{"agent.task.upserted", "scheduler.task.created", "scheduler.task.updated", "scheduler.task.cancelled"} {
		rule := mappingRuleByID(t, ctx, registry, ruleID)
		found := false
		properties, _ := rule.Rule["properties"].([]any)
		for _, raw := range properties {
			property, _ := raw.(map[string]any)
			found = found || property["property_id"] == "target_mode"
		}
		if !found {
			t.Errorf("mapping rule %s was not upgraded with dynamic scope properties", ruleID)
		}
	}
}

func legacySchedulerTaskCreatedRuleForTest(t *testing.T) MappingRule {
	t.Helper()
	for _, rule := range defaultMappingRules() {
		if rule.ID != "scheduler.task.created" {
			continue
		}
		rule.LinkTypes = nil
		delete(rule.Rule, "links")
		rule = mappingRuleWithoutDynamicScopeProperties(rule)
		rule.SourceDomain = "ontology"
		rule.SourceRef = "internal/app/ontology/defaults"
		rule.RuleHash = ""
		legacy, err := normalizeMappingRule(rule)
		if err != nil {
			t.Fatalf("normalize legacy scheduler task rule: %v", err)
		}
		return legacy
	}
	t.Fatal("default scheduler.task.created rule not found")
	return MappingRule{}
}

func mappingRuleByID(t *testing.T, ctx context.Context, registry Registry, id string) MappingRule {
	t.Helper()
	rules, err := registry.ListMappingRules(ctx, "")
	if err != nil {
		t.Fatalf("list mapping rules: %v", err)
	}
	for _, rule := range rules {
		if rule.ID == id {
			return rule
		}
	}
	t.Fatalf("mapping rule %s not found", id)
	return MappingRule{}
}

func TestDefaultOntologyMappingRulesCarryGovernanceMetadata(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()

	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}

	rules, err := registry.ListMappingRules(ctx, "")
	if err != nil {
		t.Fatalf("ListMappingRules: %v", err)
	}
	if len(rules) == 0 {
		t.Fatalf("default mapping rules should be seeded")
	}
	for _, rule := range rules {
		if rule.SourceDomain == "" || rule.SourceRef == "" || rule.EventSchemaVersion == "" || rule.RuleHash == "" {
			t.Fatalf("default mapping rule %s missing governance metadata: %+v", rule.ID, rule)
		}
	}
}

func TestDefaultOntologyIncludesProjectOrganizationLink(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}

	linkType, err := registry.GetLinkType(ctx, "project_belongs_to_organization")
	if err != nil {
		t.Fatalf("GetLinkType project_belongs_to_organization: %v", err)
	}
	if linkType.FromObjectType != "project" || linkType.ToObjectType != "organization" {
		t.Fatalf("link endpoints = %s -> %s", linkType.FromObjectType, linkType.ToObjectType)
	}
	if linkType.Cardinality != CardinalityManyToOne || !linkType.EvidenceRequired {
		t.Fatalf("link type = %+v", linkType)
	}
	rules, err := registry.ListMappingRules(ctx, "")
	if err != nil {
		t.Fatalf("ListMappingRules: %v", err)
	}
	var rule MappingRule
	for _, candidate := range rules {
		if candidate.ID == "project.organization.linked" {
			rule = candidate
			break
		}
	}
	if rule.ID == "" {
		t.Fatalf("project.organization.linked rules = %+v", rules)
	}
	if !reflect.DeepEqual(rule.ObjectTypes, []string{"project"}) {
		t.Fatalf("project.organization.linked object types = %+v", rule.ObjectTypes)
	}
	if !reflect.DeepEqual(rule.LinkTypes, []string{"project_belongs_to_organization"}) {
		t.Fatalf("project.organization.linked link types = %+v", rule.LinkTypes)
	}
}

func TestDefaultProjectOrganizationLinkedMappingRuleAppliesOrganizationView(t *testing.T) {
	ctx := context.Background()
	ontologyRegistry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, ontologyRegistry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	projectionRegistry := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)))
	if err := RegisterMappingRuleProjections(ctx, ontologyRegistry, projectionRegistry, ""); err != nil {
		t.Fatalf("RegisterMappingRuleProjections: %v", err)
	}
	graphStore := contextgraph.NewMemoryStore()
	runner := NewMappingRuleProjectionRunner(projectionRegistry, graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)), graphStore)
	if _, err := runner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "organization-created:org-1",
		TenantID: "org-1",
		Type:     "organization.created",
		StreamID: "org-1",
		Version:  1,
		Payload: map[string]any{
			"id":                "org-1",
			"name":              "Platform",
			"status":            "active",
			"code":              "01",
			"knowledge_base_id": "kb-org-1",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "user", ID: "admin"},
		OccurredAt:  time.Unix(49, 0).UTC(),
	}); err != nil {
		t.Fatalf("ApplyEvent organization.created: %v", err)
	}

	report, err := runner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "project-organization-linked:project-1:org-1:rev-1",
		TenantID: "org-1",
		Type:     "project.organization.linked",
		StreamID: "project-1",
		Version:  1,
		Payload: map[string]any{
			"project_id":      "project-1",
			"project_ref_id":  "project:project-1",
			"organization_id": "org-1",
			"title":           "Ontology project",
			"status":          "active",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "project"},
		OccurredAt:  time.Unix(50, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("ApplyEvent project.organization.linked: %v", err)
	}
	if !report.OK || report.AppliedProjectionCount != 1 {
		t.Fatalf("report = %+v", report)
	}
	objects, links, facts, evidence, err := graphStore.Snapshot(ctx, "org-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	assertContextGraphObject(t, objects, "organization", "org-1")
	assertContextGraphObject(t, objects, "project", "project:project-1")
	assertContextGraphLink(t, links, "project_belongs_to_organization", "project:project-1", "org-1")
	assertContextGraphFact(t, facts, "project", "project:project-1", "title", "Ontology project")
	assertContextGraphFact(t, facts, "project", "project:project-1", "status", "active")
	assertContextGraphEvidence(t, evidence, "project-organization-linked:project-1:org-1:rev-1")
}

func TestDefaultOntologyIncludesMemoryRecord(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}

	properties, err := registry.ListPropertyTypes(ctx, "memory_record")
	if err != nil {
		t.Fatalf("ListPropertyTypes: %v", err)
	}
	want := map[string]bool{
		"summary":          false,
		"layer":            false,
		"topic":            false,
		"scope_kind":       false,
		"scope_id":         false,
		"lifecycle_status": false,
		"confidence":       false,
		"source_refs":      false,
		"evidence_refs":    false,
	}
	for _, property := range properties {
		if _, ok := want[property.PropertyID]; ok {
			want[property.PropertyID] = true
		}
	}
	for property, seen := range want {
		if !seen {
			t.Fatalf("memory_record property %s missing in %+v", property, properties)
		}
	}
}

func TestDefaultOntologyIncludesProjectCreatedMappingRule(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}

	rules, err := registry.ListMappingRules(ctx, "")
	if err != nil {
		t.Fatalf("ListMappingRules: %v", err)
	}
	var rule MappingRule
	for _, candidate := range rules {
		if candidate.ID == "project.created" {
			rule = candidate
			break
		}
	}
	if rule.ID == "" {
		t.Fatalf("project.created rules = %+v", rules)
	}
	if len(rule.ObjectTypes) != 1 || rule.ObjectTypes[0] != "project" {
		t.Fatalf("project.created object types = %+v", rule.ObjectTypes)
	}
}

func TestDefaultOntologyIncludesOrganizationCreatedMappingRule(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}

	objectType, err := registry.GetObjectType(ctx, "organization")
	if err != nil {
		t.Fatalf("GetObjectType organization: %v", err)
	}
	if objectType.OwnerDomain != "operations" {
		t.Fatalf("organization owner domain = %q", objectType.OwnerDomain)
	}
	rules, err := registry.ListMappingRules(ctx, "")
	if err != nil {
		t.Fatalf("ListMappingRules: %v", err)
	}
	var rule MappingRule
	for _, candidate := range rules {
		if candidate.ID == "organization.created" {
			rule = candidate
			break
		}
	}
	if rule.ID == "" {
		t.Fatalf("organization.created rules = %+v", rules)
	}
}

func TestDefaultProjectCreatedMappingRuleAppliesProject(t *testing.T) {
	ctx := context.Background()
	ontologyRegistry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, ontologyRegistry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	projectionRegistry := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)))
	if err := RegisterMappingRuleProjections(ctx, ontologyRegistry, projectionRegistry, ""); err != nil {
		t.Fatalf("RegisterMappingRuleProjections: %v", err)
	}
	graphStore := contextgraph.NewMemoryStore()
	runner := NewMappingRuleProjectionRunner(projectionRegistry, graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)), graphStore)

	report, err := runner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "project-created:project-1",
		TenantID: "org-1",
		Type:     "project.created",
		StreamID: "project-1",
		Version:  1,
		Payload: map[string]any{
			"id":     "project-1",
			"title":  "Ontology project",
			"status": "active",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "project"},
		OccurredAt:  time.Unix(45, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("ApplyEvent: %v", err)
	}
	if !report.OK || report.AppliedProjectionCount != 1 {
		t.Fatalf("report = %+v", report)
	}
	objects, _, facts, evidence, err := graphStore.Snapshot(ctx, "org-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	assertContextGraphObject(t, objects, "project", "project-1")
	assertContextGraphFact(t, facts, "project", "project-1", "title", "Ontology project")
	assertContextGraphFact(t, facts, "project", "project-1", "status", "active")
	assertContextGraphEvidence(t, evidence, "project-created:project-1")
}

func TestDefaultOrganizationCreatedMappingRuleAppliesOrganization(t *testing.T) {
	ctx := context.Background()
	ontologyRegistry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, ontologyRegistry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	projectionRegistry := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)))
	if err := RegisterMappingRuleProjections(ctx, ontologyRegistry, projectionRegistry, ""); err != nil {
		t.Fatalf("RegisterMappingRuleProjections: %v", err)
	}
	graphStore := contextgraph.NewMemoryStore()
	runner := NewMappingRuleProjectionRunner(projectionRegistry, graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)), graphStore)

	report, err := runner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "organization-created:org-1",
		TenantID: "org-1",
		Type:     "organization.created",
		StreamID: "org-1",
		Version:  1,
		Payload: map[string]any{
			"id":                "org-1",
			"name":              "Platform",
			"status":            "active",
			"code":              "01",
			"knowledge_base_id": "kb-org-1",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "user", ID: "admin"},
		OccurredAt:  time.Unix(48, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("ApplyEvent: %v", err)
	}
	if !report.OK || report.AppliedProjectionCount != 1 {
		t.Fatalf("report = %+v", report)
	}
	objects, _, facts, evidence, err := graphStore.Snapshot(ctx, "org-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	assertContextGraphObject(t, objects, "organization", "org-1")
	assertContextGraphFact(t, facts, "organization", "org-1", "name", "Platform")
	assertContextGraphFact(t, facts, "organization", "org-1", "status", "active")
	assertContextGraphFact(t, facts, "organization", "org-1", "code", "01")
	assertContextGraphFact(t, facts, "organization", "org-1", "knowledge_base_id", "kb-org-1")
	assertContextGraphEvidence(t, evidence, "organization-created:org-1")
}

func TestDefaultProjectUpdatedMappingRuleUpdatesTitleFactOnly(t *testing.T) {
	ctx := context.Background()
	ontologyRegistry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, ontologyRegistry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	projectionRegistry := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)))
	if err := RegisterMappingRuleProjections(ctx, ontologyRegistry, projectionRegistry, ""); err != nil {
		t.Fatalf("RegisterMappingRuleProjections: %v", err)
	}
	graphStore := contextgraph.NewMemoryStore()
	runner := NewMappingRuleProjectionRunner(projectionRegistry, graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)), graphStore)

	report, err := runner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "project-updated:project-1:rev-2",
		TenantID: "project:project-1",
		Type:     "project.updated",
		StreamID: "project-1",
		Version:  2,
		Payload: map[string]any{
			"id":    "project-1",
			"title": "Updated project",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "user", ID: "planner-1"},
		OccurredAt:  time.Unix(46, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("ApplyEvent: %v", err)
	}
	if !report.OK || report.AppliedProjectionCount != 1 {
		t.Fatalf("report = %+v", report)
	}
	objects, links, facts, evidence, err := graphStore.Snapshot(ctx, "project:project-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(objects) != 0 || len(links) != 0 {
		t.Fatalf("project.updated should not create objects or links: objects=%+v links=%+v", objects, links)
	}
	assertContextGraphFact(t, facts, "project", "project-1", "title", "Updated project")
	assertContextGraphEvidence(t, evidence, "project-updated:project-1:rev-2")
}

func TestDefaultProjectArchivedMappingRuleUpdatesStatusFactOnly(t *testing.T) {
	ctx := context.Background()
	ontologyRegistry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, ontologyRegistry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	projectionRegistry := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)))
	if err := RegisterMappingRuleProjections(ctx, ontologyRegistry, projectionRegistry, ""); err != nil {
		t.Fatalf("RegisterMappingRuleProjections: %v", err)
	}
	graphStore := contextgraph.NewMemoryStore()
	runner := NewMappingRuleProjectionRunner(projectionRegistry, graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)), graphStore)

	report, err := runner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "project-archived:project-1:rev-3",
		TenantID: "project:project-1",
		Type:     "project.archived",
		StreamID: "project-1",
		Version:  3,
		Payload: map[string]any{
			"id":     "project-1",
			"status": "archived",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "user", ID: "planner-1"},
		OccurredAt:  time.Unix(47, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("ApplyEvent: %v", err)
	}
	if !report.OK || report.AppliedProjectionCount != 1 {
		t.Fatalf("report = %+v", report)
	}
	objects, links, facts, evidence, err := graphStore.Snapshot(ctx, "project:project-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(objects) != 0 || len(links) != 0 {
		t.Fatalf("project.archived should not create objects or links: objects=%+v links=%+v", objects, links)
	}
	assertContextGraphFact(t, facts, "project", "project-1", "status", "archived")
	assertContextGraphEvidence(t, evidence, "project-archived:project-1:rev-3")
}

func TestDefaultOntologyIncludesMemoryDistilledFromMemoryLink(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}

	linkType, err := registry.GetLinkType(ctx, "memory_distilled_from_memory")
	if err != nil {
		t.Fatalf("GetLinkType: %v", err)
	}
	if linkType.FromObjectType != "memory_record" || linkType.ToObjectType != "memory_record" {
		t.Fatalf("link endpoints = %s -> %s", linkType.FromObjectType, linkType.ToObjectType)
	}
	if linkType.Cardinality != CardinalityManyToMany || !linkType.EvidenceRequired {
		t.Fatalf("link type = %+v", linkType)
	}

	rules, err := registry.ListMappingRules(ctx, "memory.lineage.linked")
	if err != nil {
		t.Fatalf("ListMappingRules: %v", err)
	}
	var rule MappingRule
	for _, candidate := range rules {
		if candidate.ID == "memory.lineage.linked" {
			rule = candidate
			break
		}
	}
	if rule.ID == "" || len(rule.LinkTypes) != 1 || rule.LinkTypes[0] != "memory_distilled_from_memory" {
		t.Fatalf("lineage mapping rule = %+v", rules)
	}
}

func TestDefaultOntologyIncludesMemoryScopeLinks(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}

	for _, tc := range []struct {
		linkType string
		toType   string
	}{
		{linkType: "memory_about_mission", toType: "mission"},
		{linkType: "memory_about_project", toType: "project"},
		{linkType: "memory_about_task", toType: "task"},
	} {
		linkType, err := registry.GetLinkType(ctx, tc.linkType)
		if err != nil {
			t.Fatalf("GetLinkType %s: %v", tc.linkType, err)
		}
		if linkType.FromObjectType != "memory_record" || linkType.ToObjectType != tc.toType {
			t.Fatalf("%s endpoints = %s -> %s", tc.linkType, linkType.FromObjectType, linkType.ToObjectType)
		}
		if linkType.Cardinality != CardinalityManyToMany || !linkType.EvidenceRequired {
			t.Fatalf("%s link type = %+v", tc.linkType, linkType)
		}
	}

	rules, err := registry.ListMappingRules(ctx, "memory.scope.linked")
	if err != nil {
		t.Fatalf("ListMappingRules: %v", err)
	}
	var rule MappingRule
	for _, candidate := range rules {
		if candidate.ID == "memory.scope.linked" {
			rule = candidate
			break
		}
	}
	if rule.ID == "" {
		t.Fatalf("memory.scope.linked rule missing in %+v", rules)
	}
	want := map[string]bool{"memory_about_mission": false, "memory_about_project": false, "memory_about_task": false}
	for _, linkType := range rule.LinkTypes {
		if _, ok := want[linkType]; ok {
			want[linkType] = true
		}
	}
	for linkType, seen := range want {
		if !seen {
			t.Fatalf("scope mapping rule link type %s missing in %+v", linkType, rule.LinkTypes)
		}
	}
}

func TestDefaultOntologyIncludesMemoryMentionLinks(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}

	targets := map[string]string{
		"person":             "记忆提及人员",
		"agent":              "记忆提及智能体",
		"organization":       "记忆提及组织",
		"team":               "记忆提及团队",
		"department":         "记忆提及部门",
		"mission":            "记忆提及使命",
		"project":            "记忆提及项目",
		"task":               "记忆提及任务",
		"tool":               "记忆提及工具",
		"idea":               "记忆提及灵感",
		"todo":               "记忆提及待办",
		"requirement":        "记忆提及需求",
		"decision":           "记忆提及决策",
		"design_artifact":    "记忆提及设计产物",
		"repository":         "记忆提及代码仓库",
		"issue":              "记忆提及问题",
		"release":            "记忆提及发布",
		"knowledge_document": "记忆提及知识文档",
	}
	for target, displayName := range targets {
		id := "memory_mentions_" + target
		linkType, err := registry.GetLinkType(ctx, id)
		if err != nil {
			t.Fatalf("GetLinkType(%s): %v", id, err)
		}
		if linkType.FromObjectType != "memory_record" || linkType.ToObjectType != target {
			t.Fatalf("%s endpoints = %s -> %s", id, linkType.FromObjectType, linkType.ToObjectType)
		}
		if linkType.DisplayName != displayName || !linkType.EvidenceRequired {
			t.Fatalf("%s = %+v", id, linkType)
		}
	}
}

func TestDefaultOntologyIncludesMemoryKnowledgeCitationLink(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}

	linkType, err := registry.GetLinkType(ctx, "memory_cites_knowledge_document")
	if err != nil {
		t.Fatalf("GetLinkType: %v", err)
	}
	if linkType.FromObjectType != "memory_record" || linkType.ToObjectType != "knowledge_document" {
		t.Fatalf("link endpoints = %s -> %s", linkType.FromObjectType, linkType.ToObjectType)
	}
	if linkType.Cardinality != CardinalityManyToMany || !linkType.EvidenceRequired {
		t.Fatalf("link type = %+v", linkType)
	}

	rules, err := registry.ListMappingRules(ctx, "memory.record.accepted")
	if err != nil {
		t.Fatalf("ListMappingRules: %v", err)
	}
	var rule MappingRule
	for _, candidate := range rules {
		if candidate.ID == "memory.record.accepted" {
			rule = candidate
			break
		}
	}
	if rule.ID == "" {
		t.Fatalf("memory.record.accepted mapping rule missing in %+v", rules)
	}
	if len(rule.LinkTypes) != 0 {
		t.Fatalf("memory mapping link types = %+v", rule.LinkTypes)
	}
	rules, err = registry.ListMappingRules(ctx, "memory.knowledge.citation.added")
	if err != nil {
		t.Fatalf("ListMappingRules citation: %v", err)
	}
	var citationRule MappingRule
	for _, candidate := range rules {
		if candidate.ID == "memory.knowledge.citation.added" {
			citationRule = candidate
			break
		}
	}
	if citationRule.ID == "" || len(citationRule.LinkTypes) != 1 || citationRule.LinkTypes[0] != "memory_cites_knowledge_document" {
		t.Fatalf("citation mapping link types = %+v", rules)
	}
}

func TestDefaultMemoryRecordAcceptedMappingRuleAppliesRecord(t *testing.T) {
	ctx := context.Background()
	ontologyRegistry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, ontologyRegistry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	projectionRegistry := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)))
	if err := RegisterMappingRuleProjections(ctx, ontologyRegistry, projectionRegistry, ""); err != nil {
		t.Fatalf("RegisterMappingRuleProjections: %v", err)
	}
	graphStore := contextgraph.NewMemoryStore()
	runner := NewMappingRuleProjectionRunner(projectionRegistry, graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)), graphStore)

	report, err := runner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "event-memory-1",
		TenantID: "tenant-1",
		Type:     "memory.record.accepted",
		Version:  1,
		Payload: map[string]any{
			"id":               "memory-1",
			"summary":          "Repeated release review pattern.",
			"layer":            "experience",
			"topic":            "release",
			"scope_kind":       "project",
			"scope_id":         "project-1",
			"lifecycle_status": "active",
			"confidence":       0.8,
			"source_refs":      []any{"memory:fact-1"},
			"evidence_refs":    []any{"knowledge_document:kdoc-1"},
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "memory"},
		OccurredAt:  time.Unix(50, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("ApplyEvent: %v", err)
	}
	if !report.OK || report.AppliedProjectionCount != 1 {
		t.Fatalf("report = %+v", report)
	}
	objects, _, facts, evidence, err := graphStore.Snapshot(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(objects) != 1 || objects[0].ObjectType != "memory_record" || objects[0].ID != "memory-1" {
		t.Fatalf("objects = %+v", objects)
	}
	if len(facts) < 9 {
		t.Fatalf("facts = %+v", facts)
	}
	if len(evidence) != 1 || evidence[0].ID != "event-memory-1" {
		t.Fatalf("evidence = %+v", evidence)
	}
}

func TestDefaultMemoryRecordAcceptedMappingRuleDoesNotWriteCitationLink(t *testing.T) {
	ctx := context.Background()
	ontologyRegistry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, ontologyRegistry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	projectionRegistry := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)))
	if err := RegisterMappingRuleProjections(ctx, ontologyRegistry, projectionRegistry, ""); err != nil {
		t.Fatalf("RegisterMappingRuleProjections: %v", err)
	}
	graphStore := contextgraph.NewMemoryStore()
	runner := NewMappingRuleProjectionRunner(projectionRegistry, graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)), graphStore)

	report, err := runner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "event-memory-2",
		TenantID: "tenant-1",
		Type:     "memory.record.accepted",
		Version:  1,
		Payload: map[string]any{
			"id":                    "memory-2",
			"summary":               "Release reviews cite the handbook.",
			"layer":                 "fact",
			"topic":                 "release",
			"scope_kind":            "project",
			"scope_id":              "project-1",
			"lifecycle_status":      "active",
			"confidence":            0.9,
			"source_refs":           []any{"discussion_message:message-1"},
			"evidence_refs":         []any{"knowledge_document:kdoc-1"},
			"knowledge_document_id": "kdoc-1",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "memory"},
		OccurredAt:  time.Unix(60, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("ApplyEvent: %v", err)
	}
	if !report.OK || report.AppliedProjectionCount != 1 {
		t.Fatalf("report = %+v", report)
	}
	objects, links, facts, evidence, err := graphStore.Snapshot(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(objects) != 1 || objects[0].ObjectType != "memory_record" || objects[0].ID != "memory-2" {
		t.Fatalf("objects = %+v", objects)
	}
	if len(facts) < 9 {
		t.Fatalf("facts = %+v", facts)
	}
	if len(evidence) != 1 || evidence[0].ID != "event-memory-2" {
		t.Fatalf("evidence = %+v", evidence)
	}
	if len(links) != 0 {
		t.Fatalf("links = %+v", links)
	}
}

func TestDefaultMemoryRecordAcceptedMappingRuleSkipsKnowledgeLinkWithoutDocumentID(t *testing.T) {
	ctx := context.Background()
	ontologyRegistry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, ontologyRegistry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	projectionRegistry := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)))
	if err := RegisterMappingRuleProjections(ctx, ontologyRegistry, projectionRegistry, ""); err != nil {
		t.Fatalf("RegisterMappingRuleProjections: %v", err)
	}
	graphStore := contextgraph.NewMemoryStore()
	runner := NewMappingRuleProjectionRunner(projectionRegistry, graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)), graphStore)

	report, err := runner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "event-memory-3",
		TenantID: "tenant-1",
		Type:     "memory.record.accepted",
		Version:  1,
		Payload: map[string]any{
			"id":               "memory-3",
			"summary":          "Standalone release memory.",
			"layer":            "fact",
			"topic":            "release",
			"scope_kind":       "project",
			"scope_id":         "project-1",
			"lifecycle_status": "active",
			"confidence":       0.7,
			"source_refs":      []any{"discussion_message:message-2"},
			"evidence_refs":    []any{"evidence:evidence-2"},
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "memory"},
		OccurredAt:  time.Unix(70, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("ApplyEvent: %v", err)
	}
	if !report.OK || report.AppliedProjectionCount != 1 {
		t.Fatalf("report = %+v", report)
	}
	_, links, _, _, err := graphStore.Snapshot(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(links) != 0 {
		t.Fatalf("links = %+v", links)
	}
}

func TestDefaultMemoryKnowledgeCitationAddedMappingRuleAddsCitationLinkOnly(t *testing.T) {
	ctx := context.Background()
	ontologyRegistry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, ontologyRegistry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	projectionRegistry := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)))
	if err := RegisterMappingRuleProjections(ctx, ontologyRegistry, projectionRegistry, ""); err != nil {
		t.Fatalf("RegisterMappingRuleProjections: %v", err)
	}
	graphStore := contextgraph.NewMemoryStore()
	runner := NewMappingRuleProjectionRunner(projectionRegistry, graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)), graphStore)

	report, err := runner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "event-memory-citation-1",
		TenantID: "tenant-1",
		Type:     "memory.knowledge.citation.added",
		Version:  1,
		Payload: map[string]any{
			"memory_record_id":      "memory-1",
			"knowledge_document_id": "kdoc-1",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "memory"},
		OccurredAt:  time.Unix(75, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("ApplyEvent: %v", err)
	}
	if !report.OK || report.AppliedProjectionCount != 1 {
		t.Fatalf("report = %+v", report)
	}
	objects, links, facts, evidence, err := graphStore.Snapshot(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if materialized := materializedContextGraphObjects(objects); len(materialized) != 0 {
		t.Fatalf("citation event should not materialize objects: %+v", materialized)
	}
	if len(facts) != 0 {
		t.Fatalf("citation event should not upsert facts: %+v", facts)
	}
	if len(links) != 1 {
		t.Fatalf("links = %+v", links)
	}
	link := links[0]
	if link.LinkType != "memory_cites_knowledge_document" || link.FromObjectID != "memory-1" || link.ToObjectID != "kdoc-1" {
		t.Fatalf("link = %+v", link)
	}
	if len(evidence) != 1 || evidence[0].ID != "event-memory-citation-1" {
		t.Fatalf("evidence = %+v", evidence)
	}
}

func TestDefaultMemoryKnowledgeChunkCitationAddedMappingRuleAddsCitationLinkOnly(t *testing.T) {
	ctx := context.Background()
	ontologyRegistry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, ontologyRegistry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	projectionRegistry := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)))
	if err := RegisterMappingRuleProjections(ctx, ontologyRegistry, projectionRegistry, ""); err != nil {
		t.Fatalf("RegisterMappingRuleProjections: %v", err)
	}
	graphStore := contextgraph.NewMemoryStore()
	runner := NewMappingRuleProjectionRunner(projectionRegistry, graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)), graphStore)

	report, err := runner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "event-memory-chunk-citation-1",
		TenantID: "tenant-1",
		Type:     "memory.knowledge_chunk.citation.added",
		Version:  1,
		Payload: map[string]any{
			"memory_record_id":   "memory-1",
			"knowledge_chunk_id": "kdoc-1:0",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "memory"},
		OccurredAt:  time.Unix(76, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("ApplyEvent: %v", err)
	}
	if !report.OK || report.AppliedProjectionCount != 1 {
		t.Fatalf("report = %+v", report)
	}
	objects, links, facts, evidence, err := graphStore.Snapshot(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if materialized := materializedContextGraphObjects(objects); len(materialized) != 0 {
		t.Fatalf("citation event should not materialize objects: %+v", materialized)
	}
	if len(facts) != 0 {
		t.Fatalf("citation event should not upsert facts: %+v", facts)
	}
	if len(links) != 1 {
		t.Fatalf("links = %+v", links)
	}
	link := links[0]
	if link.LinkType != "memory_cites_knowledge_chunk" || link.FromObjectID != "memory-1" || link.ToObjectID != "kdoc-1:0" {
		t.Fatalf("link = %+v", link)
	}
	if len(evidence) != 1 || evidence[0].ID != "event-memory-chunk-citation-1" {
		t.Fatalf("evidence = %+v", evidence)
	}
}

func TestDefaultMemoryKnowledgeAnchorCitationAddedMappingRuleAddsCitationLinkOnly(t *testing.T) {
	ctx := context.Background()
	ontologyRegistry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, ontologyRegistry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	projectionRegistry := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)))
	if err := RegisterMappingRuleProjections(ctx, ontologyRegistry, projectionRegistry, ""); err != nil {
		t.Fatalf("RegisterMappingRuleProjections: %v", err)
	}
	graphStore := contextgraph.NewMemoryStore()
	runner := NewMappingRuleProjectionRunner(projectionRegistry, graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)), graphStore)

	report, err := runner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "event-memory-anchor-citation-1",
		TenantID: "tenant-1",
		Type:     "memory.knowledge_anchor.citation.added",
		Version:  1,
		Payload: map[string]any{
			"memory_record_id":    "memory-1",
			"knowledge_anchor_id": "kdoc-1:page:1:anchor:0007",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "memory"},
		OccurredAt:  time.Unix(77, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("ApplyEvent: %v", err)
	}
	if !report.OK || report.AppliedProjectionCount != 1 {
		t.Fatalf("report = %+v", report)
	}
	objects, links, facts, evidence, err := graphStore.Snapshot(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if materialized := materializedContextGraphObjects(objects); len(materialized) != 0 {
		t.Fatalf("citation event should not materialize objects: %+v", materialized)
	}
	if len(facts) != 0 {
		t.Fatalf("citation event should not upsert facts: %+v", facts)
	}
	if len(links) != 1 {
		t.Fatalf("links = %+v", links)
	}
	link := links[0]
	if link.LinkType != "memory_cites_knowledge_anchor" || link.FromObjectID != "memory-1" || link.ToObjectID != "kdoc-1:page:1:anchor:0007" {
		t.Fatalf("link = %+v", link)
	}
	if len(evidence) != 1 || evidence[0].ID != "event-memory-anchor-citation-1" {
		t.Fatalf("evidence = %+v", evidence)
	}
}

func TestDefaultMemoryLineageLinkedMappingRuleAddsLineageLinkOnly(t *testing.T) {
	ctx := context.Background()
	ontologyRegistry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, ontologyRegistry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	projectionRegistry := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)))
	if err := RegisterMappingRuleProjections(ctx, ontologyRegistry, projectionRegistry, ""); err != nil {
		t.Fatalf("RegisterMappingRuleProjections: %v", err)
	}
	graphStore := contextgraph.NewMemoryStore()
	runner := NewMappingRuleProjectionRunner(projectionRegistry, graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)), graphStore)

	report, err := runner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "event-memory-lineage-1",
		TenantID: "tenant-1",
		Type:     "memory.lineage.linked",
		Version:  1,
		Payload: map[string]any{
			"memory_record_id":        "memory-experience-1",
			"source_memory_record_id": "memory-fact-1",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "memory"},
		OccurredAt:  time.Unix(76, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("ApplyEvent: %v", err)
	}
	if !report.OK || report.AppliedProjectionCount != 1 {
		t.Fatalf("report = %+v", report)
	}
	objects, links, facts, evidence, err := graphStore.Snapshot(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if materialized := materializedContextGraphObjects(objects); len(materialized) != 0 {
		t.Fatalf("lineage event should not materialize objects: %+v", materialized)
	}
	if len(facts) != 0 {
		t.Fatalf("lineage event should not upsert facts: %+v", facts)
	}
	if len(links) != 1 {
		t.Fatalf("links = %+v", links)
	}
	link := links[0]
	if link.LinkType != "memory_distilled_from_memory" || link.FromObjectID != "memory-experience-1" || link.ToObjectID != "memory-fact-1" {
		t.Fatalf("link = %+v", link)
	}
	if len(evidence) != 1 || evidence[0].ID != "event-memory-lineage-1" {
		t.Fatalf("evidence = %+v", evidence)
	}
}

func TestDefaultMemoryScopeLinkedMappingRuleAddsScopeLinksOnly(t *testing.T) {
	ctx := context.Background()
	ontologyRegistry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, ontologyRegistry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	projectionRegistry := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)))
	if err := RegisterMappingRuleProjections(ctx, ontologyRegistry, projectionRegistry, ""); err != nil {
		t.Fatalf("RegisterMappingRuleProjections: %v", err)
	}
	graphStore := contextgraph.NewMemoryStore()
	runner := NewMappingRuleProjectionRunner(projectionRegistry, graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)), graphStore)

	report, err := runner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "event-memory-scope-1",
		TenantID: "mission:mission-1",
		Type:     "memory.scope.linked",
		Version:  1,
		Payload: map[string]any{
			"memory_record_id": "memory-mission-1",
			"mission_id":       "mission-1",
			"task_id":          "task-1",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "memory"},
		OccurredAt:  time.Unix(77, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("ApplyEvent: %v", err)
	}
	if !report.OK || report.AppliedProjectionCount != 1 {
		t.Fatalf("report = %+v", report)
	}
	objects, links, facts, evidence, err := graphStore.Snapshot(ctx, "mission:mission-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if materialized := materializedContextGraphObjects(objects); len(materialized) != 0 {
		t.Fatalf("scope event should not materialize objects: %+v", materialized)
	}
	if len(facts) != 0 {
		t.Fatalf("scope event should not upsert facts: %+v", facts)
	}
	if len(links) != 2 {
		t.Fatalf("links = %+v", links)
	}
	assertMemoryScopeLink(t, links, "memory_about_mission", "memory-mission-1", "mission-1")
	assertMemoryScopeLink(t, links, "memory_about_task", "memory-mission-1", "task-1")
	if len(evidence) != 1 || evidence[0].ID != "event-memory-scope-1" {
		t.Fatalf("evidence = %+v", evidence)
	}
}

func assertMemoryScopeLink(t *testing.T, links []contextgraph.Link, linkType string, fromID string, toID string) {
	t.Helper()
	for _, link := range links {
		if link.LinkType == linkType && link.FromObjectID == fromID && link.ToObjectID == toID {
			return
		}
	}
	t.Fatalf("link %s %s -> %s missing in %+v", linkType, fromID, toID, links)
}

func TestDefaultMemoryRecordLifecycleUpdatedMappingRuleUpdatesLifecycleFacts(t *testing.T) {
	ctx := context.Background()
	ontologyRegistry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, ontologyRegistry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	projectionRegistry := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)))
	if err := RegisterMappingRuleProjections(ctx, ontologyRegistry, projectionRegistry, ""); err != nil {
		t.Fatalf("RegisterMappingRuleProjections: %v", err)
	}
	graphStore := contextgraph.NewMemoryStore()
	runner := NewMappingRuleProjectionRunner(projectionRegistry, graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)), graphStore)

	report, err := runner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "event-memory-lifecycle-1",
		TenantID: "tenant-1",
		Type:     "memory.record.lifecycle.updated",
		Version:  1,
		Payload: map[string]any{
			"id":                 "memory-1",
			"summary":            "Repeated release review pattern.",
			"lifecycle_status":   "archived",
			"reference_count":    3,
			"last_referenced_at": "2026-06-14T12:00:00Z",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "memory"},
		OccurredAt:  time.Unix(80, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("ApplyEvent: %v", err)
	}
	if !report.OK || report.AppliedProjectionCount != 1 {
		t.Fatalf("report = %+v", report)
	}
	objects, _, facts, evidence, err := graphStore.Snapshot(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(objects) != 1 || objects[0].ObjectType != "memory_record" || objects[0].ID != "memory-1" || objects[0].Label != "Repeated release review pattern." {
		t.Fatalf("objects = %+v", objects)
	}
	assertFactValue(t, facts, "memory_record", "memory-1", "lifecycle_status", "archived")
	assertFactValue(t, facts, "memory_record", "memory-1", "reference_count", 3)
	assertFactValue(t, facts, "memory_record", "memory-1", "last_referenced_at", "2026-06-14T12:00:00Z")
	if len(evidence) != 1 || evidence[0].ID != "event-memory-lifecycle-1" {
		t.Fatalf("evidence = %+v", evidence)
	}
}

func TestDefaultOntologyIncludesPageIndexKnowledgeDocumentMetadata(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}

	properties, err := registry.ListPropertyTypes(ctx, "knowledge_document")
	if err != nil {
		t.Fatalf("ListPropertyTypes: %v", err)
	}
	want := map[string]bool{
		"title":                false,
		"source_uri":           false,
		"provider":             false,
		"provider_document_id": false,
		"knowledge_base_id":    false,
		"content_type":         false,
		"size_bytes":           false,
		"checksum":             false,
		"status":               false,
	}
	for _, property := range properties {
		if _, ok := want[property.PropertyID]; ok {
			want[property.PropertyID] = true
		}
	}
	for property, seen := range want {
		if !seen {
			t.Fatalf("knowledge_document property %s missing in %+v", property, properties)
		}
	}
}

func assertFactValue(t *testing.T, facts []contextgraph.Fact, subjectKind string, subjectID string, predicate string, want any) {
	t.Helper()
	for _, fact := range facts {
		if fact.Subject.Kind == subjectKind && fact.Subject.ID == subjectID && fact.Predicate == predicate {
			if fact.Value != want {
				t.Fatalf("fact %s/%s %s = %#v, want %#v", subjectKind, subjectID, predicate, fact.Value, want)
			}
			return
		}
	}
	t.Fatalf("fact %s/%s %s missing in %+v", subjectKind, subjectID, predicate, facts)
}

func TestDefaultPageIndexKnowledgeDocumentMappingRuleAppliesMetadata(t *testing.T) {
	ctx := context.Background()
	ontologyRegistry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, ontologyRegistry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	projectionRegistry := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)))
	if err := RegisterMappingRuleProjections(ctx, ontologyRegistry, projectionRegistry, ""); err != nil {
		t.Fatalf("RegisterMappingRuleProjections: %v", err)
	}
	graphStore := contextgraph.NewMemoryStore()
	runner := NewMappingRuleProjectionRunner(projectionRegistry, graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)), graphStore)

	report, err := runner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "event-doc-1",
		TenantID: "tenant-1",
		Type:     "knowledge.document.metadata.updated",
		Version:  1,
		Payload: map[string]any{
			"id":                   "kdoc-1",
			"knowledge_base_id":    "kb-1",
			"title":                "release-handbook.md",
			"source_uri":           "pageindex://folder-123/doc-456",
			"provider":             "pageindex",
			"provider_document_id": "doc-456",
			"content_type":         "text/markdown",
			"size_bytes":           int64(2048),
			"checksum":             "sha256:abc",
			"status":               "indexed",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge"},
		OccurredAt:  time.Unix(40, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("ApplyEvent: %v", err)
	}
	if !report.OK || report.AppliedProjectionCount != 1 {
		t.Fatalf("report = %+v", report)
	}
	objects, _, facts, evidence, err := graphStore.Snapshot(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(objects) != 1 || objects[0].ObjectType != "knowledge_document" || objects[0].ID != "kdoc-1" {
		t.Fatalf("objects = %+v", objects)
	}
	if len(facts) < 7 {
		t.Fatalf("facts = %+v", facts)
	}
	if len(evidence) != 1 || evidence[0].ID != "event-doc-1" {
		t.Fatalf("evidence = %+v", evidence)
	}
}

func TestDefaultKnowledgeChunkMetadataMappingRuleAppliesLightweightMetadata(t *testing.T) {
	ctx := context.Background()
	ontologyRegistry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, ontologyRegistry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	projectionRegistry := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)))
	if err := RegisterMappingRuleProjections(ctx, ontologyRegistry, projectionRegistry, ""); err != nil {
		t.Fatalf("RegisterMappingRuleProjections: %v", err)
	}
	graphStore := contextgraph.NewMemoryStore()
	runner := NewMappingRuleProjectionRunner(projectionRegistry, graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)), graphStore)

	report, err := runner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "event-chunk-1",
		TenantID: "tenant-1",
		Type:     "knowledge.chunk.metadata.updated",
		Version:  1,
		Payload: map[string]any{
			"id":                "kdoc-1:0",
			"document_id":       "kdoc-1",
			"knowledge_base_id": "kb-1",
			"ordinal":           0,
			"heading_path":      []any{"Release"},
			"start_offset":      0,
			"end_offset":        128,
			"content_hash":      "sha256:chunk",
			"vector_id":         "kdoc-1:0",
			"token_estimate":    128,
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge.ingest"},
		OccurredAt:  time.Unix(42, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("ApplyEvent: %v", err)
	}
	if !report.OK || report.AppliedProjectionCount != 1 {
		t.Fatalf("report = %+v", report)
	}
	objects, links, facts, evidence, err := graphStore.Snapshot(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	materialized := materializedContextGraphObjects(objects)
	if len(materialized) != 1 || materialized[0].ObjectType != "knowledge_chunk" || materialized[0].ID != "kdoc-1:0" {
		t.Fatalf("objects = %+v", objects)
	}
	if len(links) != 1 || links[0].LinkType != "knowledge_chunk_belongs_to_document" || links[0].FromObjectID != "kdoc-1:0" || links[0].ToObjectID != "kdoc-1" {
		t.Fatalf("links = %+v", links)
	}
	assertFactValue(t, facts, "knowledge_chunk", "kdoc-1:0", "content_hash", "sha256:chunk")
	assertFactValue(t, facts, "knowledge_chunk", "kdoc-1:0", "ordinal", 0)
	if len(evidence) != 1 || evidence[0].ID != "event-chunk-1" {
		t.Fatalf("evidence = %+v", evidence)
	}
}

func TestDefaultKnowledgePageMetadataMappingRuleAppliesLightweightProvenance(t *testing.T) {
	ctx := context.Background()
	ontologyRegistry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, ontologyRegistry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	projectionRegistry := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)))
	if err := RegisterMappingRuleProjections(ctx, ontologyRegistry, projectionRegistry, ""); err != nil {
		t.Fatalf("RegisterMappingRuleProjections: %v", err)
	}
	graphStore := contextgraph.NewMemoryStore()
	runner := NewMappingRuleProjectionRunner(projectionRegistry, graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)), graphStore)

	report, err := runner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "event-page-1",
		TenantID: "tenant-1",
		Type:     "knowledge.page.metadata.updated",
		Version:  1,
		Payload: map[string]any{
			"id":                   "kdoc-1:page:1",
			"document_id":          "kdoc-1",
			"knowledge_base_id":    "kb-1",
			"page_number":          1,
			"provider":             "pageindex",
			"provider_document_id": "pi-doc-1",
			"heading_path":         []any{"Release"},
			"source_uri":           "pageindex://folder-123/pi-doc-1#page=1",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge"},
		OccurredAt:  time.Unix(43, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("ApplyEvent: %v", err)
	}
	if !report.OK || report.AppliedProjectionCount != 1 {
		t.Fatalf("report = %+v", report)
	}
	objects, links, facts, evidence, err := graphStore.Snapshot(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	materialized := materializedContextGraphObjects(objects)
	if len(materialized) != 1 || materialized[0].ObjectType != "knowledge_page" || materialized[0].ID != "kdoc-1:page:1" {
		t.Fatalf("objects = %+v", objects)
	}
	if len(links) != 1 || links[0].LinkType != "knowledge_page_belongs_to_document" || links[0].FromObjectID != "kdoc-1:page:1" || links[0].ToObjectID != "kdoc-1" {
		t.Fatalf("links = %+v", links)
	}
	assertFactValue(t, facts, "knowledge_page", "kdoc-1:page:1", "page_number", 1)
	assertFactValue(t, facts, "knowledge_page", "kdoc-1:page:1", "provider_document_id", "pi-doc-1")
	if len(evidence) != 1 || evidence[0].ID != "event-page-1" {
		t.Fatalf("evidence = %+v", evidence)
	}
}

func TestDefaultKnowledgeAnchorMetadataMappingRuleAppliesLightweightCitationAnchor(t *testing.T) {
	ctx := context.Background()
	ontologyRegistry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, ontologyRegistry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	projectionRegistry := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)))
	if err := RegisterMappingRuleProjections(ctx, ontologyRegistry, projectionRegistry, ""); err != nil {
		t.Fatalf("RegisterMappingRuleProjections: %v", err)
	}
	graphStore := contextgraph.NewMemoryStore()
	runner := NewMappingRuleProjectionRunner(projectionRegistry, graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)), graphStore)

	report, err := runner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "event-anchor-1",
		TenantID: "tenant-1",
		Type:     "knowledge.anchor.metadata.updated",
		Version:  1,
		Payload: map[string]any{
			"id":                   "kdoc-1:page:1:anchor:0007",
			"knowledge_base_id":    "kb-1",
			"document_id":          "kdoc-1",
			"page_id":              "kdoc-1:page:1",
			"page_number":          1,
			"provider":             "pageindex",
			"provider_document_id": "pi-doc-1",
			"provider_node_id":     "0007",
			"heading_path":         []any{"Release"},
			"structure_path":       []any{"Handbook", "Release"},
			"bbox":                 map[string]any{"x": 0.1, "y": 0.2, "width": 0.3, "height": 0.4},
			"image_ref":            "pageindex://folder-123/pi-doc-1#page=1&image=preview",
			"content_hash":         "",
			"snippet_hash":         "sha256:abc",
			"source_uri":           "pageindex://folder-123/pi-doc-1#page=1&anchor=0007",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge"},
		OccurredAt:  time.Unix(44, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("ApplyEvent: %v", err)
	}
	if !report.OK || report.AppliedProjectionCount != 1 {
		t.Fatalf("report = %+v", report)
	}
	objects, links, facts, evidence, err := graphStore.Snapshot(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	anchorID := "kdoc-1:page:1:anchor:0007"
	assertContextGraphObject(t, objects, "knowledge_anchor", anchorID)
	assertContextGraphLink(t, links, "knowledge_anchor_belongs_to_page", anchorID, "kdoc-1:page:1")
	assertContextGraphFact(t, facts, "knowledge_anchor", anchorID, "provider_node_id", "0007")
	assertContextGraphFact(t, facts, "knowledge_anchor", anchorID, "snippet_hash", "sha256:abc")
	assertContextGraphFact(t, facts, "knowledge_anchor", anchorID, "source_uri", "pageindex://folder-123/pi-doc-1#page=1&anchor=0007")
	if len(evidence) != 1 || evidence[0].ID != "event-anchor-1" {
		t.Fatalf("evidence = %+v", evidence)
	}
}

func TestDefaultKnowledgeDocumentStatusUpdatedMappingRuleUpdatesStatusFactOnly(t *testing.T) {
	ctx := context.Background()
	ontologyRegistry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, ontologyRegistry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	projectionRegistry := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)))
	if err := RegisterMappingRuleProjections(ctx, ontologyRegistry, projectionRegistry, ""); err != nil {
		t.Fatalf("RegisterMappingRuleProjections: %v", err)
	}
	graphStore := contextgraph.NewMemoryStore()
	runner := NewMappingRuleProjectionRunner(projectionRegistry, graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)), graphStore)

	report, err := runner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "event-doc-status-1",
		TenantID: "tenant-1",
		Type:     "knowledge.document.status.updated",
		Version:  1,
		Payload: map[string]any{
			"id":     "kdoc-1",
			"status": "indexed",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge"},
		OccurredAt:  time.Unix(41, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("ApplyEvent: %v", err)
	}
	if !report.OK || report.AppliedProjectionCount != 1 {
		t.Fatalf("report = %+v", report)
	}
	objects, links, facts, evidence, err := graphStore.Snapshot(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(objects) != 0 || len(links) != 0 {
		t.Fatalf("status event should not create objects or links: objects=%+v links=%+v", objects, links)
	}
	if len(facts) != 1 ||
		facts[0].Subject.Kind != "knowledge_document" ||
		facts[0].Subject.ID != "kdoc-1" ||
		facts[0].Predicate != "status" ||
		facts[0].Value != "indexed" {
		t.Fatalf("facts = %+v", facts)
	}
	if len(evidence) != 1 || evidence[0].ID != "event-doc-status-1" {
		t.Fatalf("evidence = %+v", evidence)
	}
}

func assertContextGraphObject(t *testing.T, objects []contextgraph.Object, objectType string, id string) contextgraph.Object {
	t.Helper()
	for _, object := range objects {
		if object.ObjectType == objectType && object.ID == id {
			return object
		}
	}
	t.Fatalf("object %s/%s missing in %+v", objectType, id, objects)
	return contextgraph.Object{}
}

func assertContextGraphLink(t *testing.T, links []contextgraph.Link, linkType string, fromID string, toID string) contextgraph.Link {
	t.Helper()
	for _, link := range links {
		if link.LinkType == linkType && link.FromObjectID == fromID && link.ToObjectID == toID {
			return link
		}
	}
	t.Fatalf("link %s %s -> %s missing in %+v", linkType, fromID, toID, links)
	return contextgraph.Link{}
}

func assertContextGraphFact(t *testing.T, facts []contextgraph.Fact, subjectKind string, subjectID string, predicate string, want any) {
	t.Helper()
	for _, fact := range facts {
		if fact.Subject.Kind == subjectKind && fact.Subject.ID == subjectID && fact.Predicate == predicate {
			if !contextGraphFactValueEqual(fact.Value, want) {
				t.Fatalf("fact %s/%s %s = %#v, want %#v", subjectKind, subjectID, predicate, fact.Value, want)
			}
			return
		}
	}
	t.Fatalf("fact %s/%s %s missing in %+v", subjectKind, subjectID, predicate, facts)
}

func assertContextGraphEvidence(t *testing.T, evidence []contextgraph.Evidence, wantID string) {
	t.Helper()
	for _, item := range evidence {
		if item.ID == wantID {
			return
		}
	}
	t.Fatalf("evidence %s missing in %+v", wantID, evidence)
}

func mappingRuleExists(rules []MappingRule, id string) bool {
	for _, rule := range rules {
		if rule.ID == id {
			return true
		}
	}
	return false
}

func contextGraphFactValueEqual(got any, want any) bool {
	if reflect.DeepEqual(got, want) {
		return true
	}
	gotNumber, gotOK := numericFactValue(got)
	wantNumber, wantOK := numericFactValue(want)
	return gotOK && wantOK && gotNumber == wantNumber
}

func numericFactValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case float64:
		return typed, true
	default:
		return 0, false
	}
}
