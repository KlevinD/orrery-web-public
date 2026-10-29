package ontology

import (
	"context"
	"errors"
	"sort"
	"strings"
)

type DefaultDefinitionAudit struct {
	ExpectedObjectTypes  int      `json:"expected_object_types"`
	ExpectedLinkTypes    int      `json:"expected_link_types"`
	ExpectedProperties   int      `json:"expected_property_types"`
	ExpectedMappingRules int      `json:"expected_mapping_rules"`
	MissingObjectTypes   int      `json:"missing_object_types"`
	MissingLinkTypes     int      `json:"missing_link_types"`
	MissingProperties    int      `json:"missing_property_types"`
	MissingMappingRules  int      `json:"missing_mapping_rules"`
	MissingIDs           []string `json:"missing_ids,omitempty"`
}

func AuditDefaultOntologyDefinitions(ctx context.Context, registry Registry) (DefaultDefinitionAudit, error) {
	if registry == nil {
		return DefaultDefinitionAudit{}, ErrInvalidDefinition
	}
	objects, links := defaultObjectTypes(), defaultLinkTypes()
	properties, rules := defaultPropertyTypes(), defaultMappingRules()
	report := DefaultDefinitionAudit{
		ExpectedObjectTypes:  len(objects),
		ExpectedLinkTypes:    len(links),
		ExpectedProperties:   len(properties),
		ExpectedMappingRules: len(rules),
	}
	recordMissing := func(kind, id string, err error) error {
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		report.MissingIDs = append(report.MissingIDs, kind+":"+id)
		return nil
	}
	for _, item := range objects {
		if _, err := registry.GetObjectType(ctx, item.ID); err != nil {
			report.MissingObjectTypes++
			if err := recordMissing("object_type", item.ID, err); err != nil {
				return DefaultDefinitionAudit{}, err
			}
		}
	}
	for _, item := range links {
		if _, err := registry.GetLinkType(ctx, item.ID); err != nil {
			report.MissingLinkTypes++
			if err := recordMissing("link_type", item.ID, err); err != nil {
				return DefaultDefinitionAudit{}, err
			}
		}
	}
	for _, item := range properties {
		if _, err := registry.GetPropertyType(ctx, item.ID); err != nil {
			report.MissingProperties++
			if err := recordMissing("property_type", item.ID, err); err != nil {
				return DefaultDefinitionAudit{}, err
			}
		}
	}
	rulesByTenant := make(map[string]map[string]struct{})
	for _, item := range rules {
		if rulesByTenant[item.TenantID] == nil {
			rulesByTenant[item.TenantID] = make(map[string]struct{})
		}
		rulesByTenant[item.TenantID][item.ID] = struct{}{}
	}
	for tenantID, expected := range rulesByTenant {
		existing, err := registry.ListMappingRules(ctx, tenantID)
		if err != nil {
			return DefaultDefinitionAudit{}, err
		}
		for _, item := range existing {
			delete(expected, item.ID)
		}
		for id := range expected {
			report.MissingMappingRules++
			report.MissingIDs = append(report.MissingIDs, "mapping_rule:"+tenantID+":"+id)
		}
	}
	sort.Strings(report.MissingIDs)
	return report, nil
}

func InstallDefaultOntologyDefinitions(ctx context.Context, registry Registry) error {
	if registry == nil {
		return ErrInvalidDefinition
	}
	for _, objectType := range defaultObjectTypes() {
		if current, err := registry.GetObjectType(ctx, objectType.ID); err == nil {
			if objectType.ID == "todo" {
				if err := upgradeTodoCreationProperties(ctx, registry, current, objectType); err != nil {
					return err
				}
			}
			if objectType.ID == "improvement_runtime_review" {
				if err := upgradeRuntimeReviewExecutionProperties(ctx, registry, current, objectType); err != nil {
					return err
				}
			}
			if err := upgradeManagedDefaultObjectType(ctx, registry, current, objectType); err != nil {
				return err
			}
			continue
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		if err := registry.SaveObjectType(ctx, objectType); err != nil {
			return err
		}
	}
	for _, linkType := range defaultLinkTypes() {
		if current, err := registry.GetLinkType(ctx, linkType.ID); err == nil {
			if err := upgradeManagedDefaultLinkType(ctx, registry, current, linkType); err != nil {
				return err
			}
			continue
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		if err := registry.SaveLinkType(ctx, linkType); err != nil {
			return err
		}
	}
	for _, propertyType := range defaultPropertyTypes() {
		if current, err := registry.GetPropertyType(ctx, propertyType.ID); err == nil {
			if err := upgradeManagedDefaultPropertyType(ctx, registry, current, propertyType); err != nil {
				return err
			}
			continue
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		if err := registry.SavePropertyType(ctx, propertyType); err != nil {
			return err
		}
	}
	for _, rule := range defaultMappingRules() {
		existing, err := registry.ListMappingRules(ctx, rule.TenantID)
		if err != nil {
			return err
		}
		var found *MappingRule
		for _, item := range existing {
			if item.ID == rule.ID && item.TenantID == rule.TenantID {
				matched := item
				found = &matched
				break
			}
		}
		if found != nil {
			if err := upgradeManagedDefaultMappingRule(ctx, registry, *found, rule); err != nil {
				return err
			}
			continue
		}
		if err := registry.SaveMappingRule(ctx, rule); err != nil {
			return err
		}
	}
	return nil
}

type versionedDefaultObjectTypeRegistry interface {
	SaveObjectTypeWithExpectedVersion(context.Context, ObjectType, int) (ObjectType, error)
}

// Only add the three origin properties to the global Todo definition. Existing
// tenant definitions and custom fields, labels and action grants remain owned
// by their existing governance paths.
func upgradeTodoCreationProperties(ctx context.Context, registry Registry, current, desired ObjectType) error {
	if current.TenantID != "" || current.ID != "todo" || desired.ID != "todo" {
		return nil
	}
	updated := current
	updated.Properties = append([]PropertyDefinition(nil), current.Properties...)
	existing := make(map[string]bool, len(current.Properties))
	for _, field := range current.Properties {
		existing[normalizeID(field.PropertyID)] = true
	}
	for _, field := range desired.Properties {
		switch field.PropertyID {
		case "created_at", "creation_actor_id", "creation_source":
			if !existing[field.PropertyID] {
				updated.Properties = append(updated.Properties, field)
			}
		}
	}
	if len(updated.Properties) == len(current.Properties) {
		return nil
	}
	versioned, ok := registry.(versionedDefaultObjectTypeRegistry)
	if !ok {
		return ErrInvalidDefinition
	}
	_, err := versioned.SaveObjectTypeWithExpectedVersion(ctx, updated, current.Version)
	return err
}

func upgradeManagedDefaultObjectType(ctx context.Context, registry Registry, current, desired ObjectType) error {
	if desired.ID == "knowledge_entity" {
		return upgradeKnowledgeEntityIdentityProperty(ctx, registry, current, desired)
	}
	staticScopeExtension := desired.TemporalMode == TemporalModeStatic && supportsDynamicScopeProperties(desired.ID)
	if current.TenantID != "" || current.ID != desired.ID || (desired.TemporalMode == TemporalModeStatic && !staticScopeExtension) {
		return nil
	}
	updated := current
	changed := false
	if current.TemporalMode == "" || current.TemporalMode == TemporalModeStatic {
		updated.TemporalMode = desired.TemporalMode
		changed = true
	}
	if current.OwnerDomain == "operations" && desired.OwnerDomain == "people" {
		updated.OwnerDomain = desired.OwnerDomain
		changed = true
	}
	properties := make(map[string]int, len(updated.Properties))
	for index := range updated.Properties {
		properties[normalizeID(updated.Properties[index].PropertyID)] = index
	}
	for _, wanted := range desired.Properties {
		index, exists := properties[normalizeID(wanted.PropertyID)]
		if !exists {
			if staticScopeExtension && !isDynamicScopeProperty(wanted.PropertyID) {
				continue
			}
			updated.Properties = append(updated.Properties, wanted)
			changed = true
			continue
		}
		if updated.Properties[index].TemporalMode == "" || (updated.Properties[index].TemporalMode == TemporalModeStatic && wanted.TemporalMode != TemporalModeStatic) {
			updated.Properties[index].TemporalMode = wanted.TemporalMode
			changed = true
		}
		if updated.Properties[index].ValueType == ValueTypeString && wanted.ValueType != ValueTypeString {
			updated.Properties[index].ValueType = wanted.ValueType
			changed = true
		}
	}
	if !changed {
		return nil
	}
	versioned, ok := registry.(versionedDefaultObjectTypeRegistry)
	if !ok {
		return nil
	}
	_, err := versioned.SaveObjectTypeWithExpectedVersion(ctx, updated, current.Version)
	return err
}

func supportsDynamicScopeProperties(objectTypeID string) bool {
	switch normalizeID(objectTypeID) {
	case "agent_task", "scheduled_task":
		return true
	default:
		return false
	}
}

func isDynamicScopeProperty(propertyID string) bool {
	switch normalizeID(propertyID) {
	case "target_mode", "source_scope_kind", "source_scope_id", "target_scope_kind", "target_scope_id":
		return true
	default:
		return false
	}
}

type versionedDefaultLinkTypeRegistry interface {
	SaveLinkTypeWithExpectedVersion(context.Context, LinkType, int) (LinkType, error)
}

func upgradeManagedDefaultLinkType(ctx context.Context, registry Registry, current, desired LinkType) error {
	if current.TenantID != "" || current.FromObjectType != desired.FromObjectType || current.ToObjectType != desired.ToObjectType ||
		current.Cardinality != desired.Cardinality {
		return nil
	}
	updated := current
	changed := false
	if current.DisplayName != desired.DisplayName && !containsHan(current.DisplayName) {
		updated.DisplayName = desired.DisplayName
		changed = true
	}
	if (current.TemporalMode == "" || current.TemporalMode == TemporalModeStatic) && desired.TemporalMode != TemporalModeStatic {
		updated.TemporalMode = desired.TemporalMode
		changed = true
	}
	if !changed {
		return nil
	}
	versioned, ok := registry.(versionedDefaultLinkTypeRegistry)
	if !ok {
		return nil
	}
	_, err := versioned.SaveLinkTypeWithExpectedVersion(ctx, updated, current.Version)
	if errors.Is(err, ErrVersionConflict) {
		latest, getErr := registry.GetLinkType(ctx, desired.ID)
		if getErr != nil {
			return getErr
		}
		if latest.DisplayName == desired.DisplayName {
			return nil
		}
	}
	return err
}

type versionedDefaultPropertyTypeRegistry interface {
	SavePropertyTypeWithExpectedVersion(context.Context, PropertyType, int) (PropertyType, error)
}

func upgradeManagedDefaultPropertyType(ctx context.Context, registry Registry, current, desired PropertyType) error {
	if current.TenantID != "" || current.ID != desired.ID || desired.TemporalMode == TemporalModeStatic {
		return nil
	}
	updated := current
	changed := false
	if current.TemporalMode == "" || current.TemporalMode == TemporalModeStatic {
		updated.TemporalMode = desired.TemporalMode
		changed = true
	}
	if current.AuthoritySource == "operations" && desired.AuthoritySource == "people" {
		updated.AuthoritySource = desired.AuthoritySource
		changed = true
	}
	if current.ValueType == ValueTypeString && desired.ValueType != ValueTypeString {
		updated.ValueType = desired.ValueType
		changed = true
	}
	if !changed {
		return nil
	}
	versioned, ok := registry.(versionedDefaultPropertyTypeRegistry)
	if !ok {
		return nil
	}
	_, err := versioned.SavePropertyTypeWithExpectedVersion(ctx, updated, current.Version)
	return err
}

func containsHan(value string) bool {
	for _, char := range value {
		if char >= '\u4e00' && char <= '\u9fff' {
			return true
		}
	}
	return false
}

type versionedMappingRuleRegistry interface {
	SaveMappingRuleWithExpectedVersion(context.Context, MappingRule, int) (MappingRule, error)
}

func upgradeManagedDefaultMappingRule(ctx context.Context, registry Registry, current, desired MappingRule) error {
	if desired.ID != current.ID {
		return nil
	}
	legacyDefinitions := make([]MappingRule, 0, 2)
	switch current.ID {
	case "planning.idea.converted_to_todo":
		legacyDefinitions = append(legacyDefinitions, legacyPlanningIdeaConvertedToTodoMappingRule())
	case "scheduler.task.created":
		legacyDefinitions = append(legacyDefinitions, legacySchedulerTaskCreatedMappingRule(), mappingRuleWithoutDynamicScopeProperties(desired))
	case "scheduler.task.updated", "scheduler.task.cancelled", "agent.task.upserted":
		legacyDefinitions = append(legacyDefinitions, mappingRuleWithoutDynamicScopeProperties(desired))
	default:
		return nil
	}
	var err error
	current, err = normalizeMappingRule(current)
	if err != nil {
		return err
	}
	desired, err = normalizeMappingRule(desired)
	if err != nil {
		return err
	}
	if sameManagedMappingRuleDefinition(current, desired) {
		return nil
	}
	managedLegacy := false
	for _, legacyDefinition := range legacyDefinitions {
		legacy, normalizeErr := normalizeMappingRule(withDefaultMappingRuleGovernance([]MappingRule{legacyDefinition})[0])
		if normalizeErr != nil {
			return normalizeErr
		}
		if sameManagedMappingRuleDefinition(current, legacy) {
			managedLegacy = true
			break
		}
	}
	if !managedLegacy {
		return nil
	}
	versioned, ok := registry.(versionedMappingRuleRegistry)
	if !ok {
		return nil
	}
	_, err = versioned.SaveMappingRuleWithExpectedVersion(ctx, desired, current.Version)
	if errors.Is(err, ErrVersionConflict) {
		rules, listErr := registry.ListMappingRules(ctx, desired.TenantID)
		if listErr != nil {
			return listErr
		}
		for _, rule := range rules {
			if rule.ID != desired.ID || rule.TenantID != desired.TenantID {
				continue
			}
			rule, normalizeErr := normalizeMappingRule(rule)
			if normalizeErr != nil {
				return normalizeErr
			}
			if sameManagedMappingRuleDefinition(rule, desired) {
				return nil
			}
			break
		}
	}
	return err
}

func mappingRuleWithoutDynamicScopeProperties(rule MappingRule) MappingRule {
	withoutScope := rule
	withoutScope.Rule = make(map[string]any, len(rule.Rule))
	for key, value := range rule.Rule {
		withoutScope.Rule[key] = value
	}
	properties, ok := rule.Rule["properties"].([]any)
	if !ok {
		return withoutScope
	}
	filtered := make([]any, 0, len(properties))
	for _, raw := range properties {
		property, ok := raw.(map[string]any)
		if !ok {
			filtered = append(filtered, raw)
			continue
		}
		propertyID, _ := property["property_id"].(string)
		if isDynamicScopeProperty(propertyID) {
			continue
		}
		filtered = append(filtered, raw)
	}
	withoutScope.Rule["properties"] = filtered
	withoutScope.RuleHash = ""
	return withoutScope
}

func sameManagedMappingRuleDefinition(left, right MappingRule) bool {
	return left.ID == right.ID &&
		left.TenantID == right.TenantID &&
		left.EventType == right.EventType &&
		left.SourceDomain == right.SourceDomain &&
		left.SourceRef == right.SourceRef &&
		left.EventSchemaVersion == right.EventSchemaVersion &&
		left.RuleHash == right.RuleHash &&
		strings.Join(left.ObjectTypes, "\x1f") == strings.Join(right.ObjectTypes, "\x1f") &&
		strings.Join(left.LinkTypes, "\x1f") == strings.Join(right.LinkTypes, "\x1f") &&
		left.Enabled == right.Enabled
}

func legacyPlanningIdeaConvertedToTodoMappingRule() MappingRule {
	return MappingRule{
		ID:          "planning.idea.converted_to_todo",
		EventType:   "planning.idea.converted_to_todo",
		ObjectTypes: []string{"idea", "todo"},
		LinkTypes:   []string{"idea_converted_to_todo"},
		Rule: map[string]any{
			"objects": []any{
				map[string]any{"type": "idea", "id": "$.idea_id", "canonical_key": "idea:{idea_id}", "label": "$.title"},
				map[string]any{"type": "todo", "id": "todo:{todo_id}", "canonical_key": "todo:{todo_id}", "label": "$.title"},
			},
			"properties": []any{
				map[string]any{"object_type": "todo", "object_id": "todo:{todo_id}", "property_id": "title", "value": "$.title"},
				map[string]any{"object_type": "todo", "object_id": "todo:{todo_id}", "property_id": "summary", "value": "$.summary", "optional": true},
				map[string]any{"object_type": "todo", "object_id": "todo:{todo_id}", "property_id": "status", "value": "$.status", "optional": true},
				map[string]any{"object_type": "todo", "object_id": "todo:{todo_id}", "property_id": "source", "value": "$.source", "optional": true},
				map[string]any{"object_type": "todo", "object_id": "todo:{todo_id}", "property_id": "created_by", "value": "$.created_by", "optional": true},
				map[string]any{"object_type": "todo", "object_id": "todo:{todo_id}", "property_id": "upstream_idea_id", "value": "$.idea_id"},
				map[string]any{"object_type": "idea", "object_id": "$.idea_id", "property_id": "status", "value": "$.idea_status"},
				map[string]any{"object_type": "idea", "object_id": "$.idea_id", "property_id": "downstream_todo_id", "value": "$.todo_id"},
			},
			"links": []any{
				map[string]any{"type": "idea_converted_to_todo", "from_type": "idea", "from_id": "$.idea_id", "to_type": "todo", "to_id": "todo:{todo_id}"},
			},
		},
		Enabled: true,
	}
}

func legacySchedulerTaskCreatedMappingRule() MappingRule {
	return MappingRule{
		ID:                 "scheduler.task.created",
		EventType:          "scheduler.task.created",
		SourceDomain:       "ontology",
		SourceRef:          "internal/app/ontology/defaults",
		EventSchemaVersion: "v1",
		ObjectTypes:        []string{"scheduled_task"},
		Rule: map[string]any{
			"objects": []any{map[string]any{"type": "scheduled_task", "id": "$.id", "canonical_key": "scheduled_task:{id}", "label": "$.title"}},
			"properties": []any{
				map[string]any{"object_type": "scheduled_task", "object_id": "$.id", "property_id": "title", "value": "$.title"},
				map[string]any{"object_type": "scheduled_task", "object_id": "$.id", "property_id": "description", "value": "$.description", "optional": true},
				map[string]any{"object_type": "scheduled_task", "object_id": "$.id", "property_id": "cron", "value": "$.cron", "optional": true},
				map[string]any{"object_type": "scheduled_task", "object_id": "$.id", "property_id": "agent_id", "value": "$.agent_id", "optional": true},
				map[string]any{"object_type": "scheduled_task", "object_id": "$.id", "property_id": "status", "value": "$.status", "optional": true},
				map[string]any{"object_type": "scheduled_task", "object_id": "$.id", "property_id": "enabled", "value": "$.enabled"},
			},
		},
		Enabled: true,
	}
}

func mergeNormalizedIDs(base []string, extra []string) []string {
	merged := normalizeIDs(base)
	for _, value := range normalizeIDs(extra) {
		if value == "" || stringSliceContainsNormalized(merged, value) {
			continue
		}
		merged = append(merged, value)
	}
	return merged
}

func defaultObjectTypes() []ObjectType {
	objects := []ObjectType{
		knowledgeEntityObjectType(),
		objectType("organization", "Organization", "organization:{id}", "operations",
			property("name", ValueTypeString, true),
			property("status", ValueTypeString, false),
			property("code", ValueTypeString, false),
			property("knowledge_base_id", ValueTypeString, false),
		),
		objectType("mission", "Mission", "mission:{id}", "mission_control", property("title", ValueTypeString, true), property("status", ValueTypeString, false)),
		objectType("project", "Project", "project:{id}", "mission_control", property("title", ValueTypeString, false), property("status", ValueTypeString, false)),
		objectType("task", "Task", "task:{id}", "mission_control", property("title", ValueTypeString, true), property("status", ValueTypeString, false)),
		objectType("person", "Person", "person:{id}", "operations",
			property("display_name", ValueTypeString, true),
			property("role", ValueTypeString, false),
			property("email", ValueTypeString, false),
			property("status", ValueTypeString, false),
			property("skills", ValueTypeArray, false),
			property("availability", ValueTypeString, false),
			property("authority_scope", ValueTypeString, false),
		),
		objectType("team", "Team", "team:{id}", "operations",
			property("name", ValueTypeString, true),
			property("status", ValueTypeString, false),
			property("purpose", ValueTypeString, false),
		),
		objectType("department", "Department", "department:{id}", "operations",
			property("name", ValueTypeString, true),
			property("status", ValueTypeString, false),
			property("owner_profile_id", ValueTypeString, false),
		),
		temporalObjectType("position", "Position", "position:{id}", "people", TemporalModeStateInterval,
			property("name", ValueTypeString, true),
			property("level", ValueTypeString, false),
			property("job_family", ValueTypeString, false),
			property("status", ValueTypeString, false),
			property("code", ValueTypeString, false),
			property("display_name", ValueTypeString, false),
			property("org_unit_id", ValueTypeRef, false),
			property("job_profile_id", ValueTypeRef, false),
			property("approved_headcount", ValueTypeNumber, false),
			property("is_frozen", ValueTypeBoolean, false),
			property("lifecycle_status", ValueTypeEnum, false),
			property("revision", ValueTypeNumber, false),
		),
		objectType("role_assignment", "Role Assignment", "role_assignment:{id}", "operations",
			property("role_id", ValueTypeString, true),
			property("scope", ValueTypeString, false),
			property("status", ValueTypeString, false),
		),
		temporalObjectType("workforce_subject", "Workforce Subject", "workforce_subject:{id}", "people", TemporalModeStateInterval,
			property("subject_type", ValueTypeEnum, true),
			property("display_name", ValueTypeString, true),
			property("lifecycle_status", ValueTypeEnum, true),
			property("revision", ValueTypeNumber, true),
		),
		temporalObjectType("organization_unit", "Organization Unit", "organization_unit:{id}", "people", TemporalModeStateInterval,
			property("code", ValueTypeString, true),
			property("display_name", ValueTypeString, true),
			property("lifecycle_status", ValueTypeEnum, true),
			property("revision", ValueTypeNumber, true),
		),
		temporalObjectType("job_profile", "Job Profile", "job_profile:{id}", "people", TemporalModeStateInterval,
			property("code", ValueTypeString, true),
			property("display_name", ValueTypeString, true),
			property("job_family", ValueTypeString, false),
			property("job_level", ValueTypeString, false),
			property("lifecycle_status", ValueTypeEnum, true),
			property("revision", ValueTypeNumber, true),
		),
		temporalObjectType("workforce_assignment", "Workforce Assignment", "workforce_assignment:{id}", "people", TemporalModeStateInterval,
			property("subject_id", ValueTypeRef, true),
			property("org_unit_id", ValueTypeRef, true),
			property("position_id", ValueTypeRef, true),
			property("assignment_kind", ValueTypeEnum, true),
			property("allocation_bps", ValueTypeNumber, true),
			property("predecessor_assignment_id", ValueTypeRef, false),
			property("transition_kind", ValueTypeEnum, false),
			property("revision", ValueTypeNumber, true),
		),
		temporalObjectType("reporting_relationship", "Reporting Relationship", "reporting_relationship:{id}", "people", TemporalModeStateInterval,
			property("subject_id", ValueTypeRef, true),
			property("manager_id", ValueTypeRef, true),
			property("reporting_type", ValueTypeEnum, true),
			property("revision", ValueTypeNumber, true),
		),
		temporalObjectType("responsibility", "Responsibility", "responsibility:{id}", "people", TemporalModeStateInterval,
			property("code", ValueTypeString, true),
			property("title", ValueTypeString, true),
			property("description", ValueTypeString, false),
			property("revision", ValueTypeNumber, true),
		),
		temporalObjectType("responsibility_binding", "Responsibility Binding", "responsibility_binding:{id}", "people", TemporalModeStateInterval,
			property("responsibility_id", ValueTypeRef, true),
			property("target_type", ValueTypeEnum, true),
			property("target_id", ValueTypeRef, true),
			property("revision", ValueTypeNumber, true),
		),
		temporalObjectType("organization_change", "Organization Change", "organization_change:{id}", "people", TemporalModeEvent,
			property("change_kind", ValueTypeEnum, true),
			property("effective_at", ValueTypeDateTime, true),
			property("status", ValueTypeEnum, true),
		),
		objectType("agent", "Agent", "agent:{id}", "operations", property("display_name", ValueTypeString, true), property("runtime_kind", ValueTypeString, false)),
		objectType("agent_version", "Agent Version", "agent_version:{id}", "agent_runtime",
			property("agent_id", ValueTypeString, true),
			property("version", ValueTypeString, true),
			property("runtime_kind", ValueTypeString, false),
			property("configuration_ref", ValueTypeString, false),
			property("status", ValueTypeString, false),
		),
		objectType("agent_task", "Agent Task", "agent_task:{id}", "agent_runtime",
			property("title", ValueTypeString, true),
			property("description", ValueTypeString, false),
			property("status", ValueTypeString, true),
			property("source_kind", ValueTypeString, true),
			property("source_ref", ValueTypeString, false),
			property("input_ref", ValueTypeString, false),
			property("target_mode", ValueTypeString, false),
			property("source_scope_kind", ValueTypeString, false),
			property("source_scope_id", ValueTypeString, false),
			property("target_scope_kind", ValueTypeString, false),
			property("target_scope_id", ValueTypeString, false),
			property("risk_level", ValueTypeString, false),
			property("created_by", ValueTypeString, false),
		),
		objectType("tool", "Tool", "tool:{id}", "resource_admin",
			property("name", ValueTypeString, true),
			property("risk_level", ValueTypeString, false),
			property("provider", ValueTypeString, false),
			property("provider_tool_id", ValueTypeString, false),
			property("description", ValueTypeString, false),
			property("input_schema", ValueTypeObject, false),
			property("output_schema", ValueTypeObject, false),
		),
		objectType("mcp_server", "MCP Server", "mcp_server:{id}", "resource_admin", property("server_name", ValueTypeString, true), property("transport", ValueTypeString, false)),
		objectType("skill", "Skill", "skill:{id}", "resource_admin",
			property("name", ValueTypeString, true),
			property("description", ValueTypeString, false),
			property("version", ValueTypeString, false),
			property("risk_level", ValueTypeString, false),
			property("enabled", ValueTypeBoolean, false),
		),
		objectType("idea", "Idea", "idea:{id}", "planning",
			property("issue_number", ValueTypeString, false),
			property("title", ValueTypeString, true),
			property("summary", ValueTypeString, false),
			property("status", ValueTypeString, false),
			property("source", ValueTypeString, false),
			property("captured_by", ValueTypeString, false),
			property("proposer_name", ValueTypeString, false),
			property("updated_by", ValueTypeString, false),
			property("downstream_todo_id", ValueTypeString, false),
		),
		objectType("todo", "Todo", "todo:{id}", "planning",
			property("issue_number", ValueTypeString, false),
			property("title", ValueTypeString, true),
			property("summary", ValueTypeString, false),
			property("status", ValueTypeString, false),
			property("source", ValueTypeString, false),
			property("proposer_name", ValueTypeString, false),
			property("created_by", ValueTypeString, false),
			property("created_at", ValueTypeDateTime, false),
			property("creation_actor_id", ValueTypeString, false),
			property("creation_source", ValueTypeString, false),
			property("updated_by", ValueTypeString, false),
			property("assignee_id", ValueTypeString, false),
			property("assignee_name", ValueTypeString, false),
			property("upstream_idea_id", ValueTypeString, false),
		),
		objectType("issue_comment", "Issue Comment", "issue_comment:{id}", "planning",
			property("body", ValueTypeString, true),
			property("issue_kind", ValueTypeString, true),
			property("issue_id", ValueTypeString, true),
			property("issue_object_id", ValueTypeString, true),
			property("source", ValueTypeString, false),
			property("author_id", ValueTypeString, false),
			property("author_name", ValueTypeString, false),
		),
		objectType("requirement", "Requirement", "requirement:{id}", "planning",
			property("title", ValueTypeString, true),
			property("status", ValueTypeString, false),
			property("priority", ValueTypeString, false),
			property("acceptance_criteria", ValueTypeArray, false),
		),
		objectType("decision", "Decision", "decision:{id}", "planning",
			property("title", ValueTypeString, true),
			property("status", ValueTypeString, false),
			property("rationale", ValueTypeString, false),
			property("decided_by", ValueTypeString, false),
		),
		objectType("design_artifact", "Design Artifact", "design_artifact:{id}", "planning",
			property("title", ValueTypeString, true),
			property("artifact_type", ValueTypeString, false),
			property("status", ValueTypeString, false),
			property("uri", ValueTypeString, false),
		),
		objectType("legal_entity", "Legal Entity", "legal_entity:{id}", "legal",
			property("name", ValueTypeString, true),
			property("jurisdiction", ValueTypeString, false),
			property("status", ValueTypeString, false),
		),
		objectType("contract", "Contract", "contract:{id}", "legal",
			property("title", ValueTypeString, true),
			property("status", ValueTypeString, false),
			property("counterparty", ValueTypeString, false),
			property("effective_date", ValueTypeString, false),
			property("expires_at", ValueTypeString, false),
		),
		objectType("policy", "Policy", "policy:{id}", "legal",
			property("title", ValueTypeString, true),
			property("policy_type", ValueTypeString, false),
			property("status", ValueTypeString, false),
			property("owner_domain", ValueTypeString, false),
		),
		objectType("approval_case", "Approval Case", "approval_case:{id}", "legal",
			property("title", ValueTypeString, true),
			property("status", ValueTypeString, false),
			property("risk_level", ValueTypeString, false),
		),
		objectType("repository", "Repository", "repository:{id}", "development",
			property("name", ValueTypeString, true),
			property("provider", ValueTypeString, false),
			property("default_branch", ValueTypeString, false),
		),
		objectType("issue", "Issue", "issue:{id}", "development",
			property("title", ValueTypeString, true),
			property("status", ValueTypeString, false),
			property("priority", ValueTypeString, false),
		),
		objectType("pull_request", "Pull Request", "pull_request:{id}", "development",
			property("title", ValueTypeString, true),
			property("status", ValueTypeString, false),
			property("branch", ValueTypeString, false),
		),
		objectType("release", "Release", "release:{id}", "development",
			property("version", ValueTypeString, true),
			property("status", ValueTypeString, false),
			property("environment", ValueTypeString, false),
		),
		objectType("value_objective", "Value Objective", "value_objective:{id}", "value",
			property("title", ValueTypeString, true),
			property("status", ValueTypeString, false),
			property("owner_domain", ValueTypeString, false),
			property("target_metric", ValueTypeString, false),
		),
		temporalObjectType("metric", "Metric", "metric:{id}", "value", TemporalModeMeasurement,
			property("name", ValueTypeString, true),
			property("value", ValueTypeNumber, false),
			property("unit", ValueTypeString, false),
			property("observed_at", ValueTypeDateTime, false),
		),
		objectType("initiative", "Initiative", "initiative:{id}", "value",
			property("title", ValueTypeString, true),
			property("status", ValueTypeString, false),
			property("expected_value", ValueTypeString, false),
			property("risk_level", ValueTypeString, false),
		),
		objectType("experiment", "Experiment", "experiment:{id}", "value",
			property("title", ValueTypeString, true),
			property("status", ValueTypeString, false),
			property("hypothesis", ValueTypeString, false),
			property("outcome", ValueTypeString, false),
		),
		objectType("knowledge_document", "Knowledge Document", "knowledge_document:{id}", "knowledge",
			property("title", ValueTypeString, true),
			property("source_uri", ValueTypeString, false),
			property("provider", ValueTypeString, false),
			property("provider_document_id", ValueTypeString, false),
			property("knowledge_base_id", ValueTypeString, true),
			property("content_type", ValueTypeString, false),
			property("size_bytes", ValueTypeNumber, false),
			property("checksum", ValueTypeString, false),
			property("status", ValueTypeString, false),
		),
		objectType("knowledge_base", "Knowledge Base", "knowledge_base:{id}", "knowledge",
			property("name", ValueTypeString, true),
			property("description", ValueTypeString, false),
			property("owner_type", ValueTypeString, false),
			property("owner_id", ValueTypeString, false),
			property("visibility", ValueTypeString, false),
			property("status", ValueTypeString, false),
		),
		objectType("knowledge_chunk", "Knowledge Chunk", "knowledge_chunk:{id}", "knowledge",
			property("knowledge_base_id", ValueTypeString, true),
			property("document_id", ValueTypeString, true),
			property("ordinal", ValueTypeNumber, true),
			property("heading_path", ValueTypeArray, false),
			property("start_offset", ValueTypeNumber, false),
			property("end_offset", ValueTypeNumber, false),
			property("content_hash", ValueTypeString, false),
			property("vector_id", ValueTypeString, false),
			property("token_estimate", ValueTypeNumber, false),
		),
		objectType("knowledge_page", "Knowledge Page", "knowledge_page:{id}", "knowledge",
			property("knowledge_base_id", ValueTypeString, true),
			property("document_id", ValueTypeString, true),
			property("page_number", ValueTypeNumber, true),
			property("provider", ValueTypeString, false),
			property("provider_document_id", ValueTypeString, false),
			property("heading_path", ValueTypeArray, false),
			property("source_uri", ValueTypeString, false),
		),
		objectType("knowledge_anchor", "Knowledge Citation Anchor", "knowledge_anchor:{id}", "knowledge",
			property("knowledge_base_id", ValueTypeString, true),
			property("document_id", ValueTypeString, true),
			property("page_id", ValueTypeString, true),
			property("page_number", ValueTypeNumber, true),
			property("provider", ValueTypeString, false),
			property("provider_document_id", ValueTypeString, false),
			property("provider_node_id", ValueTypeString, false),
			property("heading_path", ValueTypeArray, false),
			property("structure_path", ValueTypeArray, false),
			property("bbox", ValueTypeObject, false),
			property("image_ref", ValueTypeString, false),
			property("content_hash", ValueTypeString, false),
			property("snippet_hash", ValueTypeString, false),
			property("source_uri", ValueTypeString, false),
		),
		objectType("memory_profile", "Memory Profile", "memory_profile:{id}", "memory",
			property("owner_agent_id", ValueTypeString, true),
			property("memory_kind", ValueTypeString, false),
			property("scope_kind", ValueTypeString, false),
			property("scope_id", ValueTypeString, false),
			property("enabled", ValueTypeBoolean, false),
		),
		objectType("scheduled_task", "Scheduled Task", "scheduled_task:{id}", "operations",
			property("title", ValueTypeString, true),
			property("description", ValueTypeString, false),
			property("cron", ValueTypeString, false),
			property("agent_id", ValueTypeString, false),
			property("target_mode", ValueTypeString, false),
			property("source_scope_kind", ValueTypeString, false),
			property("source_scope_id", ValueTypeString, false),
			property("target_scope_kind", ValueTypeString, false),
			property("target_scope_id", ValueTypeString, false),
			property("status", ValueTypeString, false),
			property("enabled", ValueTypeBoolean, false),
		),
		objectType("scheduled_run", "Scheduled Run", "scheduled_run:{id}", "scheduler",
			property("scheduled_task_id", ValueTypeString, true),
			property("status", ValueTypeString, true),
			property("scheduled_for", ValueTypeString, false),
			property("started_at", ValueTypeString, false),
			property("completed_at", ValueTypeString, false),
			property("agent_run_id", ValueTypeString, false),
			property("agent_run_ref_status", ValueTypeString, false),
			property("error_summary", ValueTypeString, false),
		),
		objectType("agent_run", "Agent Run", "agent_run:{id}", "agent_runtime",
			property("runtime_run_id", ValueTypeString, true),
			property("status", ValueTypeString, true),
			property("agent_id", ValueTypeString, false),
			property("agent_task_id", ValueTypeString, true),
			property("source_kind", ValueTypeString, false),
			property("source_ref", ValueTypeString, false),
			property("trace_ref", ValueTypeString, false),
			property("summary", ValueTypeString, false),
			property("outcome", ValueTypeString, false),
			property("started_at", ValueTypeString, false),
			property("completed_at", ValueTypeString, false),
			property("duration_ms", ValueTypeNumber, false),
			property("error_code", ValueTypeString, false),
		),
		objectType("responsibility_assignment", "Responsibility Assignment", "responsibility_assignment:{id}", "people",
			property("responsibility_version", ValueTypeNumber, true),
			property("agent_profile_id", ValueTypeString, true),
			property("work_kind", ValueTypeString, false),
			property("work_id", ValueTypeString, false),
			property("state", ValueTypeString, false),
		),
		objectType("responsibility_context", "Responsibility Context", "responsibility_context:{id}", "people",
			property("responsibility_id", ValueTypeString, true),
			property("responsibility_version", ValueTypeNumber, true),
			property("agent_profile_id", ValueTypeString, true),
			property("source", ValueTypeString, false),
			property("source_refs", ValueTypeArray, false),
			property("captured_at", ValueTypeString, false),
			property("expires_at", ValueTypeString, false),
		),
		objectType("responsibility_policy", "Responsibility Policy", "responsibility_policy:{id}", "responsibility",
			property("policy_id", ValueTypeString, true),
			property("policy_version", ValueTypeNumber, true),
			property("agent_profile_id", ValueTypeString, true),
			property("tool_scopes", ValueTypeArray, false),
			property("data_scopes", ValueTypeArray, false),
			property("approval_policy", ValueTypeString, false),
		),
		objectType("context_snapshot", "Context Snapshot", "context_snapshot:{id}", "agent_runtime",
			property("summary", ValueTypeString, false),
			property("input_refs", ValueTypeArray, false),
			property("memory_refs", ValueTypeArray, false),
			property("knowledge_refs", ValueTypeArray, false),
			property("retrieval_query", ValueTypeString, false),
			property("captured_at", ValueTypeString, false),
		),
		temporalObjectType("responsibility_context_snapshot", "Responsibility Context Snapshot", "responsibility_context_snapshot:{id}", "agent_runtime", TemporalModeEvent,
			property("responsibility_id", ValueTypeString, true),
			property("responsibility_version", ValueTypeNumber, true),
			property("source", ValueTypeString, true),
			property("actor_member_id", ValueTypeString, true),
			property("agent_profile_id", ValueTypeString, false),
			property("mission_id", ValueTypeString, false),
			property("task_id", ValueTypeString, false),
			property("organization_id", ValueTypeString, false),
			property("organization_name", ValueTypeString, false),
			property("position_id", ValueTypeString, false),
			property("position_name", ValueTypeString, false),
			property("summary", ValueTypeString, false),
			property("goals", ValueTypeArray, false),
			property("boundaries", ValueTypeArray, false),
			property("evidence_requirements", ValueTypeArray, false),
			property("source_refs", ValueTypeArray, false),
			property("captured_at", ValueTypeDateTime, true),
			property("effective_at", ValueTypeDateTime, true),
			property("expires_at", ValueTypeDateTime, false),
		),
		objectType("memory_digest", "Memory Digest", "memory_digest:{id}", "agent_runtime",
			property("memory_kind", ValueTypeString, false),
			property("summary_ref", ValueTypeString, false),
			property("sensitivity", ValueTypeString, false),
			property("captured_at", ValueTypeString, false),
		),
		objectType("execution_trace", "Execution Trace", "execution_trace:{id}", "observability",
			property("trace_id", ValueTypeString, true),
			property("trace_ref", ValueTypeString, false),
			property("summary", ValueTypeString, false),
			property("span_count", ValueTypeNumber, false),
			property("tool_call_count", ValueTypeNumber, false),
			property("started_at", ValueTypeString, false),
			property("completed_at", ValueTypeString, false),
		),
		objectType("evidence_item", "Evidence Item", "evidence_item:{id}", "agent_runtime",
			property("evidence_kind", ValueTypeString, true),
			property("summary", ValueTypeString, false),
			property("source_ref", ValueTypeString, false),
			property("content_ref", ValueTypeString, false),
			property("checksum", ValueTypeString, false),
			property("status", ValueTypeString, false),
			property("checks", ValueTypeArray, false),
			property("strategy", ValueTypeString, false),
			property("target_kind", ValueTypeString, false),
			property("target_observation_id", ValueTypeString, false),
			property("target_mission_id", ValueTypeString, false),
			property("target_run_id", ValueTypeString, false),
			property("original_run_id", ValueTypeString, false),
			property("retry_run_id", ValueTypeString, false),
			property("queue_status", ValueTypeString, false),
			property("failure_code", ValueTypeString, false),
		),
		objectType("artifact", "Artifact", "artifact:{id}", "agent_runtime",
			property("title", ValueTypeString, false),
			property("artifact_type", ValueTypeString, true),
			property("uri", ValueTypeString, false),
			property("checksum", ValueTypeString, false),
			property("status", ValueTypeString, false),
		),
		objectType("policy_decision", "Policy Decision", "policy_decision:{id}", "action_contract",
			property("decision", ValueTypeString, true),
			property("policy_id", ValueTypeString, false),
			property("reason", ValueTypeString, false),
			property("evaluated_at", ValueTypeString, false),
		),
		objectType("memory_record", "Memory Record", "memory_record:{id}", "memory",
			property("summary", ValueTypeString, true),
			property("layer", ValueTypeString, true),
			property("topic", ValueTypeString, false),
			property("scope_kind", ValueTypeString, true),
			property("scope_id", ValueTypeString, true),
			property("lifecycle_status", ValueTypeString, false),
			property("reference_count", ValueTypeNumber, false),
			property("last_referenced_at", ValueTypeString, false),
			property("confidence", ValueTypeNumber, false),
			property("source_refs", ValueTypeArray, false),
			property("evidence_refs", ValueTypeArray, false),
		),
		objectType("problem_case", "Problem Case", "problem_case:{id}", "problem_hub",
			property("fingerprint", ValueTypeString, true),
			property("source_domain", ValueTypeString, true),
			property("category", ValueTypeString, true),
			property("severity", ValueTypeString, true),
			property("status", ValueTypeString, true),
			property("title", ValueTypeString, true),
			property("redacted_summary", ValueTypeString, false),
			property("owner_kind", ValueTypeString, false),
			property("owner_id", ValueTypeString, false),
			property("primary_source_kind", ValueTypeString, true),
			property("primary_source_id", ValueTypeString, true),
			property("evidence_ids", ValueTypeArray, true),
			property("occurrence_count", ValueTypeNumber, true),
			property("latest_outcome_id", ValueTypeString, false),
			property("latest_outcome_success", ValueTypeBoolean, false),
			property("latest_outcome_summary", ValueTypeString, false),
			property("latest_resolution_kind", ValueTypeString, false),
			property("latest_resolution_id", ValueTypeString, false),
			property("lesson_candidate_id", ValueTypeString, false),
			property("problem_version", ValueTypeNumber, true),
			property("first_seen_at", ValueTypeDateTime, true),
			property("last_seen_at", ValueTypeDateTime, true),
			property("resolved_at", ValueTypeDateTime, false),
			property("updated_at", ValueTypeDateTime, true),
		),
		objectType("lesson_candidate", "Lesson Candidate", "lesson_candidate:{id}", "problem_hub",
			property("memory_candidate_id", ValueTypeString, true),
			property("status", ValueTypeString, true),
			property("review_outcome", ValueTypeString, false),
			property("proposed_layer", ValueTypeString, true),
			property("topic", ValueTypeString, true),
			property("summary", ValueTypeString, true),
			property("confidence", ValueTypeNumber, true),
			property("review_required", ValueTypeBoolean, true),
			property("evidence_ids", ValueTypeArray, true),
			property("accepted_memory_id", ValueTypeString, false),
			property("created_at", ValueTypeDateTime, true),
		),
		objectType("iris_snapshot", "IRIS Snapshot", "iris_snapshot:{id}", "iris", property("purpose", ValueTypeString, true), property("confidence", ValueTypeNumber, false)),
		objectType("failure_incident", "Failure Incident", "failure_incident:{id}", "reliability",
			property("fingerprint", ValueTypeString, true),
			property("source", ValueTypeString, true),
			property("component", ValueTypeString, true),
			property("category", ValueTypeString, true),
			property("severity", ValueTypeString, true),
			property("status", ValueTypeString, true),
			property("error_code", ValueTypeString, true),
			property("redacted_summary", ValueTypeString, false),
			property("retryable", ValueTypeBoolean, true),
			property("first_seen_at", ValueTypeDateTime, true),
			property("last_seen_at", ValueTypeDateTime, true),
			property("occurrence_count", ValueTypeNumber, true),
			property("incident_version", ValueTypeNumber, true),
		),
		objectType("diagnosis_case", "Diagnosis Case", "diagnosis_case:{id}", "reliability",
			property("incident_version", ValueTypeNumber, true),
			property("root_cause_category", ValueTypeString, true),
			property("redacted_summary", ValueTypeString, true),
			property("confidence", ValueTypeNumber, true),
			property("status", ValueTypeString, true),
			property("evidence_ids", ValueTypeArray, true),
			property("diagnosis_version", ValueTypeNumber, true),
			property("proposed_by_kind", ValueTypeString, true),
			property("proposed_by_id", ValueTypeString, true),
			property("reviewed_by_kind", ValueTypeString, false),
			property("reviewed_by_id", ValueTypeString, false),
			property("review_reason", ValueTypeString, false),
			property("updated_at", ValueTypeDateTime, true),
		),
		objectType("repair_rule", "Repair Rule", "repair_rule:{id}", "reliability",
			property("selector_fingerprint", ValueTypeString, true),
			property("strategy", ValueTypeString, true),
			property("action_type_id", ValueTypeString, true),
			property("risk_level", ValueTypeString, true),
			property("status", ValueTypeString, true),
			property("confidence", ValueTypeNumber, true),
			property("support_count", ValueTypeNumber, true),
			property("success_count", ValueTypeNumber, true),
			property("failure_count", ValueTypeNumber, true),
			property("reversible", ValueTypeBoolean, true),
			property("idempotent", ValueTypeBoolean, true),
			property("auto_apply", ValueTypeBoolean, true),
			property("rule_version", ValueTypeNumber, true),
			property("diagnosis_case_id", ValueTypeString, false),
			property("activation_decision_id", ValueTypeString, false),
			property("activated_by_kind", ValueTypeString, false),
			property("activated_by_id", ValueTypeString, false),
			property("activated_at", ValueTypeDateTime, false),
			property("last_verified_at", ValueTypeDateTime, false),
			property("expires_at", ValueTypeDateTime, false),
		),
		objectType("action_run", "Action Run", "action_run:{id}", "action_contract",
			property("action_type_id", ValueTypeString, true),
			property("status", ValueTypeString, true),
			property("risk_level", ValueTypeString, false),
			property("execution_mode", ValueTypeString, false),
			property("policy_decision", ValueTypeString, false),
			property("approval_case_id", ValueTypeString, false),
			property("hil_token_id", ValueTypeString, false),
			property("actor_kind", ValueTypeString, false),
			property("actor_id", ValueTypeString, false),
			property("effective_user_kind", ValueTypeString, false),
			property("effective_user_id", ValueTypeString, false),
			property("runtime_kind", ValueTypeString, false),
			property("runtime_id", ValueTypeString, false),
			property("args_hash", ValueTypeString, false),
			property("provider_call_ref_id", ValueTypeString, false),
			property("provider_call_ref_kind", ValueTypeString, false),
			property("result_ref_id", ValueTypeString, false),
			property("result_ref_kind", ValueTypeString, false),
			property("error_code", ValueTypeString, false),
			property("writeback_receipt_id", ValueTypeString, false),
			property("writeback_status", ValueTypeString, false),
			property("writeback_target_system", ValueTypeString, false),
		),
	}
	return append(objects, defaultImprovementObjectTypes()...)
}

func defaultLinkTypes() []LinkType {
	linkTypes := []LinkType{
		linkType("context_snapshot_uses_responsibility_context", "Context snapshot uses responsibility context", "context_snapshot", "responsibility_context_snapshot", CardinalityManyToOne, true),
		linkType("mission_has_task", "使命包含任务", "mission", "task", CardinalityOneToMany, true),
		linkType("project_belongs_to_organization", "项目归属组织", "project", "organization", CardinalityManyToOne, true),
		linkType("task_assigned_to_person", "任务分配给人员", "task", "person", CardinalityManyToOne, false),
		linkType("person_member_of_team", "人员属于团队", "person", "team", CardinalityManyToMany, false),
		linkType("team_owns_objective", "团队负责目标", "team", "value_objective", CardinalityOneToMany, false),
		linkType("department_part_of_organization", "部门隶属组织", "department", "organization", CardinalityManyToOne, false),
		linkType("position_belongs_to_department", "岗位隶属部门", "position", "department", CardinalityManyToOne, false),
		linkType("person_holds_position", "人员担任岗位", "person", "position", CardinalityManyToMany, false),
		linkType("agent_fulfills_position", "Agent 履行岗位", "agent", "position", CardinalityManyToMany, false),
		temporalLinkType("organization_unit_part_of_organization_unit", "Organization unit belongs to parent", "organization_unit", "organization_unit", CardinalityManyToOne, true, TemporalModeStateInterval),
		temporalLinkType("position_in_organization_unit", "Position belongs to organization unit", "position", "organization_unit", CardinalityManyToOne, true, TemporalModeStateInterval),
		temporalLinkType("position_uses_job_profile", "Position uses job profile", "position", "job_profile", CardinalityManyToOne, true, TemporalModeStateInterval),
		temporalLinkType("workforce_assignment_for_subject", "Assignment belongs to subject", "workforce_assignment", "workforce_subject", CardinalityManyToOne, true, TemporalModeStateInterval),
		temporalLinkType("workforce_assignment_in_organization_unit", "Assignment belongs to organization unit", "workforce_assignment", "organization_unit", CardinalityManyToOne, true, TemporalModeStateInterval),
		temporalLinkType("workforce_assignment_to_position", "Assignment fills position", "workforce_assignment", "position", CardinalityManyToOne, true, TemporalModeStateInterval),
		temporalLinkType("workforce_assignment_succeeds_assignment", "Assignment succeeds predecessor", "workforce_assignment", "workforce_assignment", CardinalityManyToOne, true, TemporalModeStateInterval),
		temporalLinkType("reporting_relationship_for_subject", "Reporting relationship belongs to subject", "reporting_relationship", "workforce_subject", CardinalityManyToOne, true, TemporalModeStateInterval),
		temporalLinkType("reporting_relationship_to_manager", "Reporting relationship points to manager", "reporting_relationship", "workforce_subject", CardinalityManyToOne, true, TemporalModeStateInterval),
		temporalLinkType("responsibility_binding_for_responsibility", "Binding assigns responsibility", "responsibility_binding", "responsibility", CardinalityManyToOne, true, TemporalModeStateInterval),
		temporalLinkType("responsibility_binding_targets_organization_unit", "Responsibility binding targets organization unit", "responsibility_binding", "organization_unit", CardinalityManyToOne, true, TemporalModeStateInterval),
		temporalLinkType("responsibility_binding_targets_job_profile", "Responsibility binding targets job profile", "responsibility_binding", "job_profile", CardinalityManyToOne, true, TemporalModeStateInterval),
		temporalLinkType("responsibility_binding_targets_position", "Responsibility binding targets position", "responsibility_binding", "position", CardinalityManyToOne, true, TemporalModeStateInterval),
		temporalLinkType("organization_change_has_predecessor", "Organization change has predecessor", "organization_change", "organization_unit", CardinalityOneToMany, true, TemporalModeEvent),
		temporalLinkType("organization_change_has_successor", "Organization change has successor", "organization_change", "organization_unit", CardinalityOneToMany, true, TemporalModeEvent),
		linkType("agent_has_version", "Agent 包含版本", "agent", "agent_version", CardinalityOneToMany, true),
		linkType("agent_assigned_to_task", "Agent 承担智能体任务", "agent", "agent_task", CardinalityManyToMany, true),
		linkType("scheduled_task_targets_agent", "定时任务面向 Agent", "scheduled_task", "agent", CardinalityManyToOne, true),
		linkType("agent_task_realizes_task", "智能体任务实现使命任务", "agent_task", "task", CardinalityManyToOne, true),
		linkType("agent_task_supports_mission", "智能体任务支撑使命", "agent_task", "mission", CardinalityManyToOne, true),
		linkType("scheduled_task_schedules_agent_task", "定时任务调度智能体任务", "scheduled_task", "agent_task", CardinalityManyToOne, true),
		linkType("scheduled_task_has_run", "定时任务包含执行", "scheduled_task", "scheduled_run", CardinalityOneToMany, true),
		linkType("scheduled_run_triggers_agent_run", "定时执行触发智能体运行", "scheduled_run", "agent_run", CardinalityOneToOne, true),
		linkType("agent_run_executes_agent_task", "智能体运行执行任务", "agent_run", "agent_task", CardinalityManyToOne, true),
		linkType("agent_run_performed_by_agent", "智能体运行由 Agent 执行", "agent_run", "agent", CardinalityManyToOne, true),
		linkType("agent_run_uses_agent_version", "智能体运行使用 Agent 版本", "agent_run", "agent_version", CardinalityManyToOne, true),
		linkType("agent_run_uses_context", "智能体运行使用上下文", "agent_run", "context_snapshot", CardinalityOneToMany, true),
		linkType("context_snapshot_uses_memory_digest", "上下文使用记忆摘要", "context_snapshot", "memory_digest", CardinalityManyToMany, true),
		linkType("context_snapshot_uses_knowledge", "上下文使用知识文档", "context_snapshot", "knowledge_document", CardinalityManyToMany, true),
		linkType("context_snapshot_uses_knowledge_chunk", "上下文使用知识片段", "context_snapshot", "knowledge_chunk", CardinalityManyToMany, true),
		linkType("context_snapshot_uses_knowledge_anchor", "上下文使用知识锚点", "context_snapshot", "knowledge_anchor", CardinalityManyToMany, true),
		linkType("agent_run_has_trace", "智能体运行包含追踪", "agent_run", "execution_trace", CardinalityOneToOne, true),
		linkType("agent_run_produces_evidence", "智能体运行产出证据", "agent_run", "evidence_item", CardinalityOneToMany, true),
		linkType("agent_run_produces_artifact", "智能体运行产出制品", "agent_run", "artifact", CardinalityOneToMany, true),
		linkType("agent_run_requests_action_run", "智能体运行请求动作", "agent_run", "action_run", CardinalityOneToMany, true),
		linkType("action_run_requires_approval_case", "动作运行需要审批", "action_run", "approval_case", CardinalityManyToOne, true),
		linkType("action_run_authorized_by_policy", "动作运行通过策略决策授权", "action_run", "policy_decision", CardinalityManyToOne, true),
		linkType("evidence_item_supports_action_run", "证据支撑动作运行", "evidence_item", "action_run", CardinalityManyToMany, true),
		linkType("failure_incident_observed_in_agent_run", "故障在 Agent 运行中被观察", "failure_incident", "agent_run", CardinalityManyToMany, true),
		linkType("failure_incident_observed_in_action_run", "故障在动作运行中被观察", "failure_incident", "action_run", CardinalityManyToMany, true),
		linkType("evidence_item_supports_failure_incident", "证据支撑故障事件", "evidence_item", "failure_incident", CardinalityManyToMany, true),
		linkType("diagnosis_case_diagnoses_failure_incident", "诊断案例分析故障事件", "diagnosis_case", "failure_incident", CardinalityManyToOne, true),
		linkType("evidence_item_supports_diagnosis_case", "证据支撑诊断案例", "evidence_item", "diagnosis_case", CardinalityManyToMany, true),
		linkType("repair_rule_supported_by_diagnosis_case", "修复规则由诊断案例支撑", "repair_rule", "diagnosis_case", CardinalityManyToOne, true),
		linkType("evidence_item_supports_repair_rule", "证据支撑修复规则", "evidence_item", "repair_rule", CardinalityManyToMany, true),
		linkType("evidence_item_verifies_agent_run", "Evidence verifies agent run", "evidence_item", "agent_run", CardinalityManyToMany, true),
		linkType("repair_rule_learned_from_failure_incident", "修复规则从故障事件中学习", "repair_rule", "failure_incident", CardinalityManyToOne, true),
		linkType("problem_case_tracks_failure_incident", "问题案例跟踪故障事件", "problem_case", "failure_incident", CardinalityManyToOne, true),
		linkType("evidence_item_supports_problem_case", "证据支撑问题案例", "evidence_item", "problem_case", CardinalityManyToMany, true),
		linkType("problem_case_resolved_by_repair_rule", "问题案例由修复规则解决", "problem_case", "repair_rule", CardinalityManyToOne, true),
		linkType("problem_case_proposes_lesson_candidate", "问题案例沉淀经验候选", "problem_case", "lesson_candidate", CardinalityOneToMany, true),
		linkType("evidence_item_supports_lesson_candidate", "证据支撑经验候选", "evidence_item", "lesson_candidate", CardinalityManyToMany, true),
		linkType("memory_about_problem_case", "记忆关联问题案例", "memory_record", "problem_case", CardinalityManyToMany, true),
		linkType("memory_realizes_lesson_candidate", "记忆落实经验候选", "memory_record", "lesson_candidate", CardinalityOneToOne, true),
		linkType("agent_can_use_tool", "Agent 可使用工具", "agent", "tool", CardinalityManyToMany, false),
		linkType("tool_backed_by_mcp_server", "工具由 MCP 服务支撑", "tool", "mcp_server", CardinalityManyToOne, false),
		linkType("idea_refines_requirement", "想法细化需求", "idea", "requirement", CardinalityManyToOne, true),
		linkType("idea_converted_to_todo", "灵感转化为待办", "idea", "todo", CardinalityOneToOne, true),
		linkType("decision_resolves_requirement", "决策解决需求", "decision", "requirement", CardinalityManyToOne, true),
		linkType("design_artifact_addresses_requirement", "设计产物响应需求", "design_artifact", "requirement", CardinalityManyToOne, true),
		linkType("contract_with_legal_entity", "合同关联法律实体", "contract", "legal_entity", CardinalityManyToOne, true),
		linkType("approval_case_reviews_contract", "审批单审核合同", "approval_case", "contract", CardinalityManyToOne, true),
		linkType("repository_has_issue", "仓库包含问题", "repository", "issue", CardinalityOneToMany, false),
		linkType("repository_has_pull_request", "仓库包含拉取请求", "repository", "pull_request", CardinalityOneToMany, false),
		linkType("pull_request_resolves_issue", "拉取请求解决问题", "pull_request", "issue", CardinalityManyToMany, false),
		linkType("release_includes_pull_request", "发布包含拉取请求", "release", "pull_request", CardinalityManyToMany, true),
		linkType("initiative_supports_objective", "举措支撑目标", "initiative", "value_objective", CardinalityManyToOne, false),
		linkType("metric_tracks_objective", "指标跟踪目标", "metric", "value_objective", CardinalityManyToOne, false),
		linkType("experiment_validates_initiative", "实验验证举措", "experiment", "initiative", CardinalityManyToOne, true),
		linkType("knowledge_document_supports_mission", "知识文档支撑使命", "knowledge_document", "mission", CardinalityManyToMany, true),
		linkType("knowledge_document_references_document", "知识文档引用知识文档", "knowledge_document", "knowledge_document", CardinalityManyToMany, true),
		linkType("knowledge_chunk_belongs_to_document", "知识片段归属文档", "knowledge_chunk", "knowledge_document", CardinalityManyToOne, true),
		linkType("knowledge_page_belongs_to_document", "知识页面归属文档", "knowledge_page", "knowledge_document", CardinalityManyToOne, true),
		linkType("knowledge_anchor_belongs_to_page", "知识锚点归属页面", "knowledge_anchor", "knowledge_page", CardinalityManyToOne, true),
		linkType("memory_cites_knowledge_document", "记忆引用知识文档", "memory_record", "knowledge_document", CardinalityManyToMany, true),
		linkType("memory_cites_knowledge_chunk", "记忆引用知识片段", "memory_record", "knowledge_chunk", CardinalityManyToMany, true),
		linkType("memory_cites_knowledge_anchor", "记忆引用知识锚点", "memory_record", "knowledge_anchor", CardinalityManyToMany, true),
		linkType("memory_distilled_from_memory", "记忆由记忆提炼", "memory_record", "memory_record", CardinalityManyToMany, true),
		linkType("memory_about_mission", "记忆关联使命", "memory_record", "mission", CardinalityManyToMany, true),
		linkType("memory_about_project", "记忆关联项目", "memory_record", "project", CardinalityManyToMany, true),
		linkType("memory_about_task", "记忆关联任务", "memory_record", "task", CardinalityManyToMany, true),
		linkType("memory_mentions_person", "记忆提及人员", "memory_record", "person", CardinalityManyToMany, true),
		linkType("memory_mentions_agent", "记忆提及智能体", "memory_record", "agent", CardinalityManyToMany, true),
		linkType("memory_mentions_organization", "记忆提及组织", "memory_record", "organization", CardinalityManyToMany, true),
		linkType("memory_mentions_team", "记忆提及团队", "memory_record", "team", CardinalityManyToMany, true),
		linkType("memory_mentions_department", "记忆提及部门", "memory_record", "department", CardinalityManyToMany, true),
		linkType("memory_mentions_mission", "记忆提及使命", "memory_record", "mission", CardinalityManyToMany, true),
		linkType("memory_mentions_project", "记忆提及项目", "memory_record", "project", CardinalityManyToMany, true),
		linkType("memory_mentions_task", "记忆提及任务", "memory_record", "task", CardinalityManyToMany, true),
		linkType("memory_mentions_tool", "记忆提及工具", "memory_record", "tool", CardinalityManyToMany, true),
		linkType("memory_mentions_idea", "记忆提及灵感", "memory_record", "idea", CardinalityManyToMany, true),
		linkType("memory_mentions_todo", "记忆提及待办", "memory_record", "todo", CardinalityManyToMany, true),
		linkType("memory_mentions_requirement", "记忆提及需求", "memory_record", "requirement", CardinalityManyToMany, true),
		linkType("memory_mentions_decision", "记忆提及决策", "memory_record", "decision", CardinalityManyToMany, true),
		linkType("memory_mentions_design_artifact", "记忆提及设计产物", "memory_record", "design_artifact", CardinalityManyToMany, true),
		linkType("memory_mentions_repository", "记忆提及代码仓库", "memory_record", "repository", CardinalityManyToMany, true),
		linkType("memory_mentions_issue", "记忆提及问题", "memory_record", "issue", CardinalityManyToMany, true),
		linkType("memory_mentions_release", "记忆提及发布", "memory_record", "release", CardinalityManyToMany, true),
		linkType("memory_mentions_knowledge_document", "记忆提及知识文档", "memory_record", "knowledge_document", CardinalityManyToMany, true),
		linkType("iris_snapshot_supports_action_run", "IRIS 快照支撑动作运行", "iris_snapshot", "action_run", CardinalityOneToMany, true),
	}
	for _, objectType := range knowledgeMentionTargetObjectTypes() {
		linkTypes = append(linkTypes, linkType(
			"knowledge_document_mentions_"+objectType,
			"Knowledge document mentions "+objectType,
			"knowledge_document", objectType, CardinalityManyToMany, true,
		))
	}
	for _, objectType := range actionRunTargetObjectTypes() {
		linkTypes = append(linkTypes, linkType("action_run_targets_"+objectType, "动作运行指向"+actionRunTargetDisplayName(objectType), "action_run", objectType, CardinalityManyToMany, true))
	}
	linkTypes = append(linkTypes, defaultImprovementLinkTypes()...)
	return append(linkTypes, knowledgeEntityLinkTypes()...)
}

func knowledgeMentionTargetObjectTypes() []string {
	return []string{
		"person", "agent", "organization", "team", "department",
		"mission", "project", "task", "tool", "idea", "todo",
		"requirement", "decision", "design_artifact", "repository",
		"issue", "release", "contract", "policy",
	}
}

func actionRunTargetObjectTypes() []string {
	return []string{
		"mission",
		"task",
		"person",
		"team",
		"department",
		"position",
		"role_assignment",
		"agent_run",
		"responsibility_assignment",
		"responsibility_context",
		"responsibility_policy",
		"tool",
		"mcp_server",
		"idea",
		"requirement",
		"decision",
		"design_artifact",
		"iris_snapshot",
		"contract",
		"policy",
		"approval_case",
		"repository",
		"issue",
		"pull_request",
		"release",
		"metric",
		"value_objective",
		"initiative",
		"failure_incident",
		"repair_rule",
	}
}

func actionRunTargetDisplayName(objectType string) string {
	switch objectType {
	case "mission":
		return "使命"
	case "task":
		return "任务"
	case "person":
		return "人员"
	case "team":
		return "团队"
	case "department":
		return "部门"
	case "position":
		return "岗位"
	case "role_assignment":
		return "角色分配"
	case "tool":
		return "工具"
	case "mcp_server":
		return "MCP 服务"
	case "idea":
		return "想法"
	case "requirement":
		return "需求"
	case "decision":
		return "决策"
	case "design_artifact":
		return "设计产物"
	case "iris_snapshot":
		return "IRIS 快照"
	case "contract":
		return "合同"
	case "policy":
		return "政策"
	case "approval_case":
		return "审批单"
	case "repository":
		return "仓库"
	case "issue":
		return "问题"
	case "pull_request":
		return "拉取请求"
	case "release":
		return "发布"
	case "metric":
		return "指标"
	case "value_objective":
		return "价值目标"
	case "initiative":
		return "举措"
	case "failure_incident":
		return "故障事件"
	case "repair_rule":
		return "修复规则"
	default:
		return objectType
	}
}

func defaultPropertyTypes() []PropertyType {
	var out []PropertyType
	for _, objectType := range defaultObjectTypes() {
		for _, prop := range objectType.Properties {
			out = append(out, PropertyType{
				ID:              objectType.ID + "." + prop.PropertyID,
				ObjectTypeID:    objectType.ID,
				PropertyID:      prop.PropertyID,
				DisplayName:     prop.DisplayName,
				ValueType:       prop.ValueType,
				TemporalMode:    prop.TemporalMode,
				AuthoritySource: objectType.OwnerDomain,
				ConflictPolicy:  ConflictPolicyAuthorityWins,
				Sensitivity:     prop.Sensitivity,
			})
			if objectType.OwnerDomain == "knowledge_instances" {
				out[len(out)-1].AllowedActions = []string{"ontology.knowledge_instances.apply"}
			}
		}
	}
	return out
}

func defaultMappingRules() []MappingRule {
	rules := []MappingRule{
		{
			ID:          "mission.created",
			EventType:   "mission.created",
			ObjectTypes: []string{"mission"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "mission", "id": "$.id", "canonical_key": "mission:{id}"},
				},
				"properties": []any{
					map[string]any{"object_type": "mission", "object_id": "$.id", "property_id": "title", "value": "$.title"},
					map[string]any{"object_type": "mission", "object_id": "$.id", "property_id": "status", "value": "$.status"},
				},
			},
			Enabled: true,
		},
		{
			ID:          "organization.created",
			EventType:   "organization.created",
			ObjectTypes: []string{"organization"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "organization", "id": "$.id", "canonical_key": "organization:{id}", "label": "$.name"},
				},
				"properties": []any{
					map[string]any{"object_type": "organization", "object_id": "$.id", "property_id": "name", "value": "$.name"},
					map[string]any{"object_type": "organization", "object_id": "$.id", "property_id": "status", "value": "$.status"},
					map[string]any{"object_type": "organization", "object_id": "$.id", "property_id": "code", "value": "$.code"},
					map[string]any{"object_type": "organization", "object_id": "$.id", "property_id": "knowledge_base_id", "value": "$.knowledge_base_id"},
				},
			},
			Enabled: true,
		},
		{
			ID:          "project.created",
			EventType:   "project.created",
			ObjectTypes: []string{"project"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "project", "id": "$.id", "canonical_key": "project:{id}"},
				},
				"properties": []any{
					map[string]any{"object_type": "project", "object_id": "$.id", "property_id": "title", "value": "$.title"},
					map[string]any{"object_type": "project", "object_id": "$.id", "property_id": "status", "value": "$.status"},
				},
			},
			Enabled: true,
		},
		{
			ID:        "project.updated",
			EventType: "project.updated",
			Rule: map[string]any{
				"properties": []any{
					map[string]any{"object_type": "project", "object_id": "$.id", "property_id": "title", "value": "$.title"},
				},
			},
			Enabled: true,
		},
		{
			ID:        "project.archived",
			EventType: "project.archived",
			Rule: map[string]any{
				"properties": []any{
					map[string]any{"object_type": "project", "object_id": "$.id", "property_id": "status", "value": "$.status"},
				},
			},
			Enabled: true,
		},
		{
			ID:          "project.organization.linked",
			EventType:   "project.organization.linked",
			ObjectTypes: []string{"project"},
			LinkTypes:   []string{"project_belongs_to_organization"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "project", "id": "$.project_ref_id", "canonical_key": "project:{project_id}", "label": "$.title"},
				},
				"properties": []any{
					map[string]any{"object_type": "project", "object_id": "$.project_ref_id", "property_id": "title", "value": "$.title"},
					map[string]any{"object_type": "project", "object_id": "$.project_ref_id", "property_id": "status", "value": "$.status"},
				},
				"links": []any{
					map[string]any{"type": "project_belongs_to_organization", "from_type": "project", "from_id": "$.project_ref_id", "to_type": "organization", "to_id": "$.organization_id"},
				},
			},
			Enabled: true,
		},
		{
			ID:          "mission.task.created",
			EventType:   "mission.task.created",
			ObjectTypes: []string{"task"},
			LinkTypes:   []string{"mission_has_task"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "task", "id": "$.task_id", "canonical_key": "task:{task_id}"},
				},
				"properties": []any{
					map[string]any{"object_type": "task", "object_id": "$.task_id", "property_id": "title", "value": "$.title"},
					map[string]any{"object_type": "task", "object_id": "$.task_id", "property_id": "status", "value": "$.status"},
				},
				"links": []any{
					map[string]any{"type": "mission_has_task", "from_type": "mission", "from_id": "$.mission_id", "to_type": "task", "to_id": "$.task_id", "evidence_id": "$.event_id"},
				},
			},
			Enabled: true,
		},
		{
			ID:          "resource.tool.registered",
			EventType:   "resource.tool.registered",
			ObjectTypes: []string{"tool"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "tool", "id": "$.id", "canonical_key": "tool:{id}"},
				},
				"properties": []any{
					map[string]any{"object_type": "tool", "object_id": "$.id", "property_id": "name", "value": "$.name"},
					map[string]any{"object_type": "tool", "object_id": "$.id", "property_id": "risk_level", "value": "$.risk_level"},
				},
			},
			Enabled: true,
		},
		{
			ID:          "resource.skill.registered",
			EventType:   "resource.skill.registered",
			ObjectTypes: []string{"skill"},
			Rule: map[string]any{
				"objects": []any{map[string]any{"type": "skill", "id": "$.id", "canonical_key": "skill:{id}", "label": "$.name"}},
				"properties": []any{
					map[string]any{"object_type": "skill", "object_id": "$.id", "property_id": "name", "value": "$.name"},
					map[string]any{"object_type": "skill", "object_id": "$.id", "property_id": "description", "value": "$.description", "optional": true},
					map[string]any{"object_type": "skill", "object_id": "$.id", "property_id": "version", "value": "$.version", "optional": true},
					map[string]any{"object_type": "skill", "object_id": "$.id", "property_id": "risk_level", "value": "$.risk_level", "optional": true},
					map[string]any{"object_type": "skill", "object_id": "$.id", "property_id": "enabled", "value": "$.enabled"},
				},
			},
			Enabled: true,
		},
		{
			ID:          "resource.mcp_server.registered",
			EventType:   "resource.mcp_server.registered",
			ObjectTypes: []string{"mcp_server"},
			Rule: map[string]any{
				"objects": []any{map[string]any{"type": "mcp_server", "id": "$.id", "canonical_key": "mcp_server:{id}", "label": "$.server_name"}},
				"properties": []any{
					map[string]any{"object_type": "mcp_server", "object_id": "$.id", "property_id": "server_name", "value": "$.server_name"},
					map[string]any{"object_type": "mcp_server", "object_id": "$.id", "property_id": "transport", "value": "$.transport", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:          "knowledge.base.created",
			EventType:   "knowledge.base.created",
			ObjectTypes: []string{"knowledge_base"},
			Rule: map[string]any{
				"objects": []any{map[string]any{"type": "knowledge_base", "id": "$.id", "canonical_key": "knowledge_base:{id}", "label": "$.name"}},
				"properties": []any{
					map[string]any{"object_type": "knowledge_base", "object_id": "$.id", "property_id": "name", "value": "$.name"},
					map[string]any{"object_type": "knowledge_base", "object_id": "$.id", "property_id": "description", "value": "$.description", "optional": true},
					map[string]any{"object_type": "knowledge_base", "object_id": "$.id", "property_id": "owner_type", "value": "$.owner_type", "optional": true},
					map[string]any{"object_type": "knowledge_base", "object_id": "$.id", "property_id": "owner_id", "value": "$.owner_id", "optional": true},
					map[string]any{"object_type": "knowledge_base", "object_id": "$.id", "property_id": "visibility", "value": "$.visibility", "optional": true},
					map[string]any{"object_type": "knowledge_base", "object_id": "$.id", "property_id": "status", "value": "$.status", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:          "memory.profile.created",
			EventType:   "memory.profile.created",
			ObjectTypes: []string{"memory_profile"},
			Rule: map[string]any{
				"objects": []any{map[string]any{"type": "memory_profile", "id": "$.id", "canonical_key": "memory_profile:{id}", "label": "$.owner_agent_id"}},
				"properties": []any{
					map[string]any{"object_type": "memory_profile", "object_id": "$.id", "property_id": "owner_agent_id", "value": "$.owner_agent_id"},
					map[string]any{"object_type": "memory_profile", "object_id": "$.id", "property_id": "memory_kind", "value": "$.memory_kind", "optional": true},
					map[string]any{"object_type": "memory_profile", "object_id": "$.id", "property_id": "scope_kind", "value": "$.scope_kind", "optional": true},
					map[string]any{"object_type": "memory_profile", "object_id": "$.id", "property_id": "scope_id", "value": "$.scope_id", "optional": true},
					map[string]any{"object_type": "memory_profile", "object_id": "$.id", "property_id": "enabled", "value": "$.enabled"},
				},
			},
			Enabled: true,
		},
		{
			ID:          "scheduler.task.created",
			EventType:   "scheduler.task.created",
			ObjectTypes: []string{"scheduled_task"},
			LinkTypes:   []string{"scheduled_task_schedules_agent_task"},
			Rule: map[string]any{
				"objects": []any{map[string]any{"type": "scheduled_task", "id": "$.id", "canonical_key": "scheduled_task:{id}", "label": "$.title"}},
				"properties": []any{
					map[string]any{"object_type": "scheduled_task", "object_id": "$.id", "property_id": "title", "value": "$.title"},
					map[string]any{"object_type": "scheduled_task", "object_id": "$.id", "property_id": "description", "value": "$.description", "optional": true},
					map[string]any{"object_type": "scheduled_task", "object_id": "$.id", "property_id": "cron", "value": "$.cron", "optional": true},
					map[string]any{"object_type": "scheduled_task", "object_id": "$.id", "property_id": "agent_id", "value": "$.agent_id", "optional": true},
					map[string]any{"object_type": "scheduled_task", "object_id": "$.id", "property_id": "target_mode", "value": "$.target_mode", "optional": true},
					map[string]any{"object_type": "scheduled_task", "object_id": "$.id", "property_id": "source_scope_kind", "value": "$.source_scope_kind", "optional": true},
					map[string]any{"object_type": "scheduled_task", "object_id": "$.id", "property_id": "source_scope_id", "value": "$.source_scope_id", "optional": true},
					map[string]any{"object_type": "scheduled_task", "object_id": "$.id", "property_id": "target_scope_kind", "value": "$.target_scope_kind", "optional": true},
					map[string]any{"object_type": "scheduled_task", "object_id": "$.id", "property_id": "target_scope_id", "value": "$.target_scope_id", "optional": true},
					map[string]any{"object_type": "scheduled_task", "object_id": "$.id", "property_id": "status", "value": "$.status", "optional": true},
					map[string]any{"object_type": "scheduled_task", "object_id": "$.id", "property_id": "enabled", "value": "$.enabled"},
				},
				"links": []any{
					map[string]any{"type": "scheduled_task_schedules_agent_task", "from_type": "scheduled_task", "from_id": "$.id", "to_type": "agent_task", "to_id": "$.agent_task_id", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:        "scheduler.task.agent_targeted",
			EventType: "scheduler.task.agent_targeted",
			LinkTypes: []string{"scheduled_task_targets_agent"},
			Rule: map[string]any{
				"links": []any{
					map[string]any{"type": "scheduled_task_targets_agent", "from_type": "scheduled_task", "from_id": "$.scheduled_task_id", "to_type": "agent", "to_id": "$.agent_id"},
				},
			},
			Enabled: true,
		},
		{
			ID:          "action.run.started",
			EventType:   "action.run.started",
			ObjectTypes: []string{"action_run"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "action_run", "id": "$.id", "canonical_key": "action_run:{id}"},
				},
				"properties": []any{
					map[string]any{"object_type": "action_run", "object_id": "$.id", "property_id": "action_type_id", "value": "$.action_type_id"},
					map[string]any{"object_type": "action_run", "object_id": "$.id", "property_id": "status", "value": "$.status"},
					map[string]any{"object_type": "action_run", "object_id": "$.id", "property_id": "risk_level", "value": "$.risk_level", "optional": true},
					map[string]any{"object_type": "action_run", "object_id": "$.id", "property_id": "execution_mode", "value": "$.execution_mode", "optional": true},
					map[string]any{"object_type": "action_run", "object_id": "$.id", "property_id": "policy_decision", "value": "$.policy_decision_id", "optional": true},
					map[string]any{"object_type": "action_run", "object_id": "$.id", "property_id": "approval_case_id", "value": "$.approval_case_id", "optional": true},
					map[string]any{"object_type": "action_run", "object_id": "$.id", "property_id": "hil_token_id", "value": "$.hil_token_id", "optional": true},
					map[string]any{"object_type": "action_run", "object_id": "$.id", "property_id": "actor_kind", "value": "$.actor_kind", "optional": true},
					map[string]any{"object_type": "action_run", "object_id": "$.id", "property_id": "actor_id", "value": "$.actor_id", "optional": true},
					map[string]any{"object_type": "action_run", "object_id": "$.id", "property_id": "effective_user_kind", "value": "$.effective_user_kind", "optional": true},
					map[string]any{"object_type": "action_run", "object_id": "$.id", "property_id": "effective_user_id", "value": "$.effective_user_id", "optional": true},
					map[string]any{"object_type": "action_run", "object_id": "$.id", "property_id": "runtime_kind", "value": "$.runtime_kind", "optional": true},
					map[string]any{"object_type": "action_run", "object_id": "$.id", "property_id": "runtime_id", "value": "$.runtime_id", "optional": true},
					map[string]any{"object_type": "action_run", "object_id": "$.id", "property_id": "args_hash", "value": "$.args_hash", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:        "action.run.completed",
			EventType: "action.run.completed",
			Rule: map[string]any{
				"properties": []any{
					map[string]any{"object_type": "action_run", "object_id": "$.id", "property_id": "status", "value": "$.status"},
					map[string]any{"object_type": "action_run", "object_id": "$.id", "property_id": "provider_call_ref_id", "value": "$.provider_call_ref_id", "optional": true},
					map[string]any{"object_type": "action_run", "object_id": "$.id", "property_id": "provider_call_ref_kind", "value": "$.provider_call_ref_kind", "optional": true},
					map[string]any{"object_type": "action_run", "object_id": "$.id", "property_id": "result_ref_id", "value": "$.result_ref_id", "optional": true},
					map[string]any{"object_type": "action_run", "object_id": "$.id", "property_id": "result_ref_kind", "value": "$.result_ref_kind", "optional": true},
					map[string]any{"object_type": "action_run", "object_id": "$.id", "property_id": "error_code", "value": "$.error_code", "optional": true},
				},
			},
			Enabled: true,
		},
		actionWritebackMappingRule("action.writeback.succeeded"),
		actionWritebackMappingRule("action.writeback.failed"),
		{
			ID:          "people.team.created",
			EventType:   "people.team.created",
			ObjectTypes: []string{"team"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "team", "id": "$.id", "canonical_key": "team:{id}", "label": "$.name"},
				},
				"properties": []any{
					map[string]any{"object_type": "team", "object_id": "$.id", "property_id": "name", "value": "$.name"},
					map[string]any{"object_type": "team", "object_id": "$.id", "property_id": "status", "value": "$.status", "optional": true},
					map[string]any{"object_type": "team", "object_id": "$.id", "property_id": "purpose", "value": "$.purpose", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:          "people.person.updated",
			EventType:   "people.person.updated",
			ObjectTypes: []string{"person"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "person", "id": "$.id", "canonical_key": "person:{id}", "label": "$.display_name"},
				},
				"properties": []any{
					map[string]any{"object_type": "person", "object_id": "$.id", "property_id": "display_name", "value": "$.display_name"},
					map[string]any{"object_type": "person", "object_id": "$.id", "property_id": "role", "value": "$.role", "optional": true},
					map[string]any{"object_type": "person", "object_id": "$.id", "property_id": "email", "value": "$.email", "optional": true},
					map[string]any{"object_type": "person", "object_id": "$.id", "property_id": "status", "value": "$.status", "optional": true},
					map[string]any{"object_type": "person", "object_id": "$.id", "property_id": "skills", "value": "$.skills", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:          "people.agent.updated",
			EventType:   "people.agent.updated",
			ObjectTypes: []string{"agent"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "agent", "id": "$.id", "canonical_key": "agent:{id}", "label": "$.display_name"},
				},
				"properties": []any{
					map[string]any{"object_type": "agent", "object_id": "$.id", "property_id": "display_name", "value": "$.display_name"},
					map[string]any{"object_type": "agent", "object_id": "$.id", "property_id": "runtime_kind", "value": "$.runtime_kind", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:        "people.membership.linked",
			EventType: "people.membership.linked",
			LinkTypes: []string{
				"person_member_of_team",
			},
			Rule: map[string]any{
				"links": []any{
					map[string]any{"type": "person_member_of_team", "from_type": "person", "from_id": "$.person_id", "to_type": "team", "to_id": "$.team_id"},
				},
			},
			Enabled: true,
		},
		{
			ID:          "people.department.created",
			EventType:   "people.department.created",
			ObjectTypes: []string{"department"},
			LinkTypes:   []string{"department_part_of_organization"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "department", "id": "$.id", "canonical_key": "department:{id}", "label": "$.name"},
				},
				"properties": []any{
					map[string]any{"object_type": "department", "object_id": "$.id", "property_id": "name", "value": "$.name"},
					map[string]any{"object_type": "department", "object_id": "$.id", "property_id": "status", "value": "$.status", "optional": true},
					map[string]any{"object_type": "department", "object_id": "$.id", "property_id": "owner_profile_id", "value": "$.owner_profile_id", "optional": true},
				},
				"links": []any{
					map[string]any{"type": "department_part_of_organization", "from_type": "department", "from_id": "$.id", "to_type": "organization", "to_id": "$.organization_id", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:          "people.position.created",
			EventType:   "people.position.created",
			ObjectTypes: []string{"position"},
			LinkTypes:   []string{"position_belongs_to_department"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "position", "id": "$.id", "canonical_key": "position:{id}", "label": "$.name"},
				},
				"properties": []any{
					map[string]any{"object_type": "position", "object_id": "$.id", "property_id": "name", "value": "$.name"},
					map[string]any{"object_type": "position", "object_id": "$.id", "property_id": "level", "value": "$.level", "optional": true},
					map[string]any{"object_type": "position", "object_id": "$.id", "property_id": "job_family", "value": "$.job_family", "optional": true},
					map[string]any{"object_type": "position", "object_id": "$.id", "property_id": "status", "value": "$.status", "optional": true},
				},
				"links": []any{
					map[string]any{"type": "position_belongs_to_department", "from_type": "position", "from_id": "$.id", "to_type": "department", "to_id": "$.department_id", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:          "people.position.assigned",
			EventType:   "people.position.assigned",
			ObjectTypes: []string{"role_assignment"},
			LinkTypes:   []string{"person_holds_position", "agent_fulfills_position"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "role_assignment", "id": "$.id", "canonical_key": "role_assignment:{id}", "label": "$.role_id"},
				},
				"properties": []any{
					map[string]any{"object_type": "role_assignment", "object_id": "$.id", "property_id": "role_id", "value": "$.role_id"},
					map[string]any{"object_type": "role_assignment", "object_id": "$.id", "property_id": "scope", "value": "$.scope", "optional": true},
					map[string]any{"object_type": "role_assignment", "object_id": "$.id", "property_id": "status", "value": "$.status", "optional": true},
				},
				"links": []any{
					map[string]any{"type": "person_holds_position", "from_type": "person", "from_id": "$.person_id", "to_type": "position", "to_id": "$.position_id", "optional": true},
					map[string]any{"type": "agent_fulfills_position", "from_type": "agent", "from_id": "$.agent_id", "to_type": "position", "to_id": "$.position_id", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:          "planning.requirement.created",
			EventType:   "planning.requirement.created",
			ObjectTypes: []string{"requirement"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "requirement", "id": "$.id", "canonical_key": "requirement:{id}", "label": "$.title"},
				},
				"properties": []any{
					map[string]any{"object_type": "requirement", "object_id": "$.id", "property_id": "title", "value": "$.title"},
					map[string]any{"object_type": "requirement", "object_id": "$.id", "property_id": "status", "value": "$.status", "optional": true},
					map[string]any{"object_type": "requirement", "object_id": "$.id", "property_id": "priority", "value": "$.priority", "optional": true},
					map[string]any{"object_type": "requirement", "object_id": "$.id", "property_id": "acceptance_criteria", "value": "$.acceptance_criteria", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:          "planning.idea.captured",
			EventType:   "planning.idea.captured",
			ObjectTypes: []string{"idea"},
			LinkTypes:   []string{"idea_refines_requirement"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "idea", "id": "$.id", "canonical_key": "idea:{id}", "label": "$.title"},
				},
				"properties": []any{
					map[string]any{"object_type": "idea", "object_id": "$.id", "property_id": "issue_number", "value": "$.issue_number", "optional": true},
					map[string]any{"object_type": "idea", "object_id": "$.id", "property_id": "title", "value": "$.title"},
					map[string]any{"object_type": "idea", "object_id": "$.id", "property_id": "summary", "value": "$.summary", "optional": true},
					map[string]any{"object_type": "idea", "object_id": "$.id", "property_id": "status", "value": "$.status", "optional": true},
					map[string]any{"object_type": "idea", "object_id": "$.id", "property_id": "source", "value": "$.source", "optional": true},
					map[string]any{"object_type": "idea", "object_id": "$.id", "property_id": "captured_by", "value": "$.captured_by", "optional": true},
					map[string]any{"object_type": "idea", "object_id": "$.id", "property_id": "proposer_name", "value": "$.proposer_name", "optional": true},
					map[string]any{"object_type": "idea", "object_id": "$.id", "property_id": "updated_by", "value": "$.updated_by", "optional": true},
				},
				"links": []any{
					map[string]any{"type": "idea_refines_requirement", "from_type": "idea", "from_id": "$.id", "to_type": "requirement", "to_id": "$.requirement_id", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:        "planning.idea.converted_to_todo",
			EventType: "planning.idea.converted_to_todo",
			LinkTypes: []string{"idea_converted_to_todo"},
			Rule: map[string]any{
				"properties": []any{
					map[string]any{"object_type": "todo", "object_id": "todo:{todo_id}", "property_id": "upstream_idea_id", "value": "$.idea_id"},
					map[string]any{"object_type": "idea", "object_id": "$.idea_id", "property_id": "downstream_todo_id", "value": "$.todo_id"},
				},
				"links": []any{
					map[string]any{"type": "idea_converted_to_todo", "from_type": "idea", "from_id": "$.idea_id", "to_type": "todo", "to_id": "todo:{todo_id}"},
				},
			},
			Enabled: true,
		},
		{
			ID:          "planning.todo.changed",
			EventType:   "planning.todo.changed",
			ObjectTypes: []string{"todo"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "todo", "id": "todo:{id}", "canonical_key": "todo:{id}", "label": "$.title"},
				},
				"properties": []any{
					map[string]any{"object_type": "todo", "object_id": "todo:{id}", "property_id": "issue_number", "value": "$.issue_number", "optional": true},
					map[string]any{"object_type": "todo", "object_id": "todo:{id}", "property_id": "title", "value": "$.title"},
					map[string]any{"object_type": "todo", "object_id": "todo:{id}", "property_id": "summary", "value": "$.summary", "optional": true},
					map[string]any{"object_type": "todo", "object_id": "todo:{id}", "property_id": "status", "value": "$.status"},
					map[string]any{"object_type": "todo", "object_id": "todo:{id}", "property_id": "source", "value": "$.source", "optional": true},
					map[string]any{"object_type": "todo", "object_id": "todo:{id}", "property_id": "proposer_name", "value": "$.proposer_name", "optional": true},
					map[string]any{"object_type": "todo", "object_id": "todo:{id}", "property_id": "created_by", "value": "$.created_by", "optional": true},
					map[string]any{"object_type": "todo", "object_id": "todo:{id}", "property_id": "updated_by", "value": "$.updated_by", "optional": true},
					map[string]any{"object_type": "todo", "object_id": "todo:{id}", "property_id": "assignee_id", "value": "$.assignee_id", "optional": true},
					map[string]any{"object_type": "todo", "object_id": "todo:{id}", "property_id": "assignee_name", "value": "$.assignee_name", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:        "todo.created",
			EventType: "todo.created",
			Rule: map[string]any{
				"properties": []any{
					map[string]any{"object_type": "todo", "object_id": "todo:{id}", "property_id": "created_at", "value": "$.created_at"},
					map[string]any{"object_type": "todo", "object_id": "todo:{id}", "property_id": "creation_actor_id", "value": "$.created_by"},
					map[string]any{"object_type": "todo", "object_id": "todo:{id}", "property_id": "creation_source", "value": "$.creation_source"},
				},
			},
			Enabled: true,
		},
		{
			ID:          "planning.todo.comment.linked",
			EventType:   "planning.todo.comment.linked",
			ObjectTypes: []string{"issue_comment"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "issue_comment", "id": "issue_comment:{comment_id}", "canonical_key": "issue_comment:{comment_id}", "label": "$.body"},
				},
				"properties": []any{
					map[string]any{"object_type": "issue_comment", "object_id": "issue_comment:{comment_id}", "property_id": "body", "value": "$.body"},
					map[string]any{"object_type": "issue_comment", "object_id": "issue_comment:{comment_id}", "property_id": "issue_kind", "value": "todo"},
					map[string]any{"object_type": "issue_comment", "object_id": "issue_comment:{comment_id}", "property_id": "issue_id", "value": "$.issue_id"},
					map[string]any{"object_type": "issue_comment", "object_id": "issue_comment:{comment_id}", "property_id": "issue_object_id", "value": "todo:{issue_id}"},
					map[string]any{"object_type": "issue_comment", "object_id": "issue_comment:{comment_id}", "property_id": "source", "value": "$.source", "optional": true},
					map[string]any{"object_type": "issue_comment", "object_id": "issue_comment:{comment_id}", "property_id": "author_id", "value": "$.author_id", "optional": true},
					map[string]any{"object_type": "issue_comment", "object_id": "issue_comment:{comment_id}", "property_id": "author_name", "value": "$.author_name", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:          "planning.idea.comment.linked",
			EventType:   "planning.idea.comment.linked",
			ObjectTypes: []string{"issue_comment"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "issue_comment", "id": "issue_comment:{comment_id}", "canonical_key": "issue_comment:{comment_id}", "label": "$.body"},
				},
				"properties": []any{
					map[string]any{"object_type": "issue_comment", "object_id": "issue_comment:{comment_id}", "property_id": "body", "value": "$.body"},
					map[string]any{"object_type": "issue_comment", "object_id": "issue_comment:{comment_id}", "property_id": "issue_kind", "value": "idea"},
					map[string]any{"object_type": "issue_comment", "object_id": "issue_comment:{comment_id}", "property_id": "issue_id", "value": "$.issue_id"},
					map[string]any{"object_type": "issue_comment", "object_id": "issue_comment:{comment_id}", "property_id": "issue_object_id", "value": "$.issue_id"},
					map[string]any{"object_type": "issue_comment", "object_id": "issue_comment:{comment_id}", "property_id": "source", "value": "$.source", "optional": true},
					map[string]any{"object_type": "issue_comment", "object_id": "issue_comment:{comment_id}", "property_id": "author_id", "value": "$.author_id", "optional": true},
					map[string]any{"object_type": "issue_comment", "object_id": "issue_comment:{comment_id}", "property_id": "author_name", "value": "$.author_name", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:          "planning.decision.recorded",
			EventType:   "planning.decision.recorded",
			ObjectTypes: []string{"decision"},
			LinkTypes:   []string{"decision_resolves_requirement"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "decision", "id": "$.id", "canonical_key": "decision:{id}", "label": "$.title"},
				},
				"properties": []any{
					map[string]any{"object_type": "decision", "object_id": "$.id", "property_id": "title", "value": "$.title"},
					map[string]any{"object_type": "decision", "object_id": "$.id", "property_id": "status", "value": "$.status", "optional": true},
					map[string]any{"object_type": "decision", "object_id": "$.id", "property_id": "rationale", "value": "$.rationale", "optional": true},
					map[string]any{"object_type": "decision", "object_id": "$.id", "property_id": "decided_by", "value": "$.decided_by", "optional": true},
				},
				"links": []any{
					map[string]any{"type": "decision_resolves_requirement", "from_type": "decision", "from_id": "$.id", "to_type": "requirement", "to_id": "$.requirement_id", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:          "planning.design_artifact.created",
			EventType:   "planning.design_artifact.created",
			ObjectTypes: []string{"design_artifact"},
			LinkTypes:   []string{"design_artifact_addresses_requirement"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "design_artifact", "id": "$.id", "canonical_key": "design_artifact:{id}", "label": "$.title"},
				},
				"properties": []any{
					map[string]any{"object_type": "design_artifact", "object_id": "$.id", "property_id": "title", "value": "$.title"},
					map[string]any{"object_type": "design_artifact", "object_id": "$.id", "property_id": "artifact_type", "value": "$.artifact_type", "optional": true},
					map[string]any{"object_type": "design_artifact", "object_id": "$.id", "property_id": "status", "value": "$.status", "optional": true},
					map[string]any{"object_type": "design_artifact", "object_id": "$.id", "property_id": "uri", "value": "$.uri", "optional": true},
				},
				"links": []any{
					map[string]any{"type": "design_artifact_addresses_requirement", "from_type": "design_artifact", "from_id": "$.id", "to_type": "requirement", "to_id": "$.requirement_id", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:          "legal.entity.created",
			EventType:   "legal.entity.created",
			ObjectTypes: []string{"legal_entity"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "legal_entity", "id": "$.id", "canonical_key": "legal_entity:{id}", "label": "$.name"},
				},
				"properties": []any{
					map[string]any{"object_type": "legal_entity", "object_id": "$.id", "property_id": "name", "value": "$.name"},
					map[string]any{"object_type": "legal_entity", "object_id": "$.id", "property_id": "jurisdiction", "value": "$.jurisdiction", "optional": true},
					map[string]any{"object_type": "legal_entity", "object_id": "$.id", "property_id": "status", "value": "$.status", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:          "legal.contract.created",
			EventType:   "legal.contract.created",
			ObjectTypes: []string{"contract"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "contract", "id": "$.id", "canonical_key": "contract:{id}", "label": "$.title"},
				},
				"properties": []any{
					map[string]any{"object_type": "contract", "object_id": "$.id", "property_id": "title", "value": "$.title"},
					map[string]any{"object_type": "contract", "object_id": "$.id", "property_id": "status", "value": "$.status", "optional": true},
					map[string]any{"object_type": "contract", "object_id": "$.id", "property_id": "counterparty", "value": "$.counterparty", "optional": true},
					map[string]any{"object_type": "contract", "object_id": "$.id", "property_id": "effective_date", "value": "$.effective_date", "optional": true},
					map[string]any{"object_type": "contract", "object_id": "$.id", "property_id": "expires_at", "value": "$.expires_at", "optional": true},
				},
				"links": []any{
					map[string]any{"type": "contract_with_legal_entity", "from_type": "contract", "from_id": "$.id", "to_type": "legal_entity", "to_id": "$.legal_entity_id", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:          "legal.policy.created",
			EventType:   "legal.policy.created",
			ObjectTypes: []string{"policy"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "policy", "id": "$.id", "canonical_key": "policy:{id}", "label": "$.title"},
				},
				"properties": []any{
					map[string]any{"object_type": "policy", "object_id": "$.id", "property_id": "title", "value": "$.title"},
					map[string]any{"object_type": "policy", "object_id": "$.id", "property_id": "policy_type", "value": "$.policy_type", "optional": true},
					map[string]any{"object_type": "policy", "object_id": "$.id", "property_id": "status", "value": "$.status", "optional": true},
					map[string]any{"object_type": "policy", "object_id": "$.id", "property_id": "owner_domain", "value": "$.owner_domain", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:          "legal.approval_case.opened",
			EventType:   "legal.approval_case.opened",
			ObjectTypes: []string{"approval_case"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "approval_case", "id": "$.id", "canonical_key": "approval_case:{id}", "label": "$.title"},
				},
				"properties": []any{
					map[string]any{"object_type": "approval_case", "object_id": "$.id", "property_id": "title", "value": "$.title"},
					map[string]any{"object_type": "approval_case", "object_id": "$.id", "property_id": "status", "value": "$.status", "optional": true},
					map[string]any{"object_type": "approval_case", "object_id": "$.id", "property_id": "risk_level", "value": "$.risk_level", "optional": true},
				},
				"links": []any{
					map[string]any{"type": "approval_case_reviews_contract", "from_type": "approval_case", "from_id": "$.id", "to_type": "contract", "to_id": "$.contract_id", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:          "development.repository.registered",
			EventType:   "development.repository.registered",
			ObjectTypes: []string{"repository"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "repository", "id": "$.id", "canonical_key": "repository:{id}", "label": "$.name"},
				},
				"properties": []any{
					map[string]any{"object_type": "repository", "object_id": "$.id", "property_id": "name", "value": "$.name"},
					map[string]any{"object_type": "repository", "object_id": "$.id", "property_id": "provider", "value": "$.provider", "optional": true},
					map[string]any{"object_type": "repository", "object_id": "$.id", "property_id": "default_branch", "value": "$.default_branch", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:          "development.issue.created",
			EventType:   "development.issue.created",
			ObjectTypes: []string{"issue"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "issue", "id": "$.id", "canonical_key": "issue:{id}", "label": "$.title"},
				},
				"properties": []any{
					map[string]any{"object_type": "issue", "object_id": "$.id", "property_id": "title", "value": "$.title"},
					map[string]any{"object_type": "issue", "object_id": "$.id", "property_id": "status", "value": "$.status", "optional": true},
					map[string]any{"object_type": "issue", "object_id": "$.id", "property_id": "priority", "value": "$.priority", "optional": true},
				},
				"links": []any{
					map[string]any{"type": "repository_has_issue", "from_type": "repository", "from_id": "$.repository_id", "to_type": "issue", "to_id": "$.id", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:          "development.pull_request.opened",
			EventType:   "development.pull_request.opened",
			ObjectTypes: []string{"pull_request"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "pull_request", "id": "$.id", "canonical_key": "pull_request:{id}", "label": "$.title"},
				},
				"properties": []any{
					map[string]any{"object_type": "pull_request", "object_id": "$.id", "property_id": "title", "value": "$.title"},
					map[string]any{"object_type": "pull_request", "object_id": "$.id", "property_id": "status", "value": "$.status", "optional": true},
					map[string]any{"object_type": "pull_request", "object_id": "$.id", "property_id": "branch", "value": "$.branch", "optional": true},
				},
				"links": []any{
					map[string]any{"type": "repository_has_pull_request", "from_type": "repository", "from_id": "$.repository_id", "to_type": "pull_request", "to_id": "$.id", "optional": true},
					map[string]any{"type": "pull_request_resolves_issue", "from_type": "pull_request", "from_id": "$.id", "to_type": "issue", "to_id": "$.issue_id", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:          "development.release.created",
			EventType:   "development.release.created",
			ObjectTypes: []string{"release"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "release", "id": "$.id", "canonical_key": "release:{id}", "label": "$.version"},
				},
				"properties": []any{
					map[string]any{"object_type": "release", "object_id": "$.id", "property_id": "version", "value": "$.version"},
					map[string]any{"object_type": "release", "object_id": "$.id", "property_id": "status", "value": "$.status", "optional": true},
					map[string]any{"object_type": "release", "object_id": "$.id", "property_id": "environment", "value": "$.environment", "optional": true},
				},
				"links": []any{
					map[string]any{"type": "release_includes_pull_request", "from_type": "release", "from_id": "$.id", "to_type": "pull_request", "to_id": "$.pull_request_id", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:          "value.objective.created",
			EventType:   "value.objective.created",
			ObjectTypes: []string{"value_objective"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "value_objective", "id": "$.id", "canonical_key": "value_objective:{id}", "label": "$.title"},
				},
				"properties": []any{
					map[string]any{"object_type": "value_objective", "object_id": "$.id", "property_id": "title", "value": "$.title"},
					map[string]any{"object_type": "value_objective", "object_id": "$.id", "property_id": "status", "value": "$.status", "optional": true},
					map[string]any{"object_type": "value_objective", "object_id": "$.id", "property_id": "owner_domain", "value": "$.owner_domain", "optional": true},
					map[string]any{"object_type": "value_objective", "object_id": "$.id", "property_id": "target_metric", "value": "$.target_metric", "optional": true},
				},
				"links": []any{
					map[string]any{"type": "team_owns_objective", "from_type": "team", "from_id": "$.team_id", "to_type": "value_objective", "to_id": "$.id", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:          "value.metric.recorded",
			EventType:   "value.metric.recorded",
			ObjectTypes: []string{"metric"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "metric", "id": "$.id", "canonical_key": "metric:{id}", "label": "$.name"},
				},
				"properties": []any{
					map[string]any{"object_type": "metric", "object_id": "$.id", "property_id": "name", "value": "$.name"},
					map[string]any{"object_type": "metric", "object_id": "$.id", "property_id": "value", "value": "$.value", "optional": true},
					map[string]any{"object_type": "metric", "object_id": "$.id", "property_id": "unit", "value": "$.unit", "optional": true},
					map[string]any{"object_type": "metric", "object_id": "$.id", "property_id": "observed_at", "value": "$.observed_at", "optional": true},
				},
				"links": []any{
					map[string]any{"type": "metric_tracks_objective", "from_type": "metric", "from_id": "$.id", "to_type": "value_objective", "to_id": "$.value_objective_id", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:          "value.initiative.created",
			EventType:   "value.initiative.created",
			ObjectTypes: []string{"initiative"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "initiative", "id": "$.id", "canonical_key": "initiative:{id}", "label": "$.title"},
				},
				"properties": []any{
					map[string]any{"object_type": "initiative", "object_id": "$.id", "property_id": "title", "value": "$.title"},
					map[string]any{"object_type": "initiative", "object_id": "$.id", "property_id": "status", "value": "$.status", "optional": true},
					map[string]any{"object_type": "initiative", "object_id": "$.id", "property_id": "expected_value", "value": "$.expected_value", "optional": true},
					map[string]any{"object_type": "initiative", "object_id": "$.id", "property_id": "risk_level", "value": "$.risk_level", "optional": true},
				},
				"links": []any{
					map[string]any{"type": "initiative_supports_objective", "from_type": "initiative", "from_id": "$.id", "to_type": "value_objective", "to_id": "$.value_objective_id", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:          "value.experiment.created",
			EventType:   "value.experiment.created",
			ObjectTypes: []string{"experiment"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "experiment", "id": "$.id", "canonical_key": "experiment:{id}", "label": "$.title"},
				},
				"properties": []any{
					map[string]any{"object_type": "experiment", "object_id": "$.id", "property_id": "title", "value": "$.title"},
					map[string]any{"object_type": "experiment", "object_id": "$.id", "property_id": "status", "value": "$.status", "optional": true},
					map[string]any{"object_type": "experiment", "object_id": "$.id", "property_id": "hypothesis", "value": "$.hypothesis", "optional": true},
					map[string]any{"object_type": "experiment", "object_id": "$.id", "property_id": "outcome", "value": "$.outcome", "optional": true},
				},
				"links": []any{
					map[string]any{"type": "experiment_validates_initiative", "from_type": "experiment", "from_id": "$.id", "to_type": "initiative", "to_id": "$.initiative_id", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:          "memory.record.accepted",
			EventType:   "memory.record.accepted",
			ObjectTypes: []string{"memory_record"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "memory_record", "id": "$.id", "canonical_key": "memory_record:{id}", "label": "$.summary"},
				},
				"properties": []any{
					map[string]any{"object_type": "memory_record", "object_id": "$.id", "property_id": "summary", "value": "$.summary"},
					map[string]any{"object_type": "memory_record", "object_id": "$.id", "property_id": "layer", "value": "$.layer"},
					map[string]any{"object_type": "memory_record", "object_id": "$.id", "property_id": "topic", "value": "$.topic"},
					map[string]any{"object_type": "memory_record", "object_id": "$.id", "property_id": "scope_kind", "value": "$.scope_kind"},
					map[string]any{"object_type": "memory_record", "object_id": "$.id", "property_id": "scope_id", "value": "$.scope_id"},
					map[string]any{"object_type": "memory_record", "object_id": "$.id", "property_id": "lifecycle_status", "value": "$.lifecycle_status"},
					map[string]any{"object_type": "memory_record", "object_id": "$.id", "property_id": "confidence", "value": "$.confidence"},
					map[string]any{"object_type": "memory_record", "object_id": "$.id", "property_id": "source_refs", "value": "$.source_refs"},
					map[string]any{"object_type": "memory_record", "object_id": "$.id", "property_id": "evidence_refs", "value": "$.evidence_refs"},
				},
			},
			Enabled: true,
		},
		{
			ID:          "knowledge.document.metadata.updated",
			EventType:   "knowledge.document.metadata.updated",
			ObjectTypes: []string{"knowledge_document"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "knowledge_document", "id": "$.id", "canonical_key": "knowledge_document:{id}", "label": "$.title"},
				},
				"properties": []any{
					map[string]any{"object_type": "knowledge_document", "object_id": "$.id", "property_id": "title", "value": "$.title"},
					map[string]any{"object_type": "knowledge_document", "object_id": "$.id", "property_id": "source_uri", "value": "$.source_uri"},
					map[string]any{"object_type": "knowledge_document", "object_id": "$.id", "property_id": "provider", "value": "$.provider"},
					map[string]any{"object_type": "knowledge_document", "object_id": "$.id", "property_id": "provider_document_id", "value": "$.provider_document_id"},
					map[string]any{"object_type": "knowledge_document", "object_id": "$.id", "property_id": "knowledge_base_id", "value": "$.knowledge_base_id"},
					map[string]any{"object_type": "knowledge_document", "object_id": "$.id", "property_id": "content_type", "value": "$.content_type"},
					map[string]any{"object_type": "knowledge_document", "object_id": "$.id", "property_id": "size_bytes", "value": "$.size_bytes"},
					map[string]any{"object_type": "knowledge_document", "object_id": "$.id", "property_id": "checksum", "value": "$.checksum"},
					map[string]any{"object_type": "knowledge_document", "object_id": "$.id", "property_id": "status", "value": "$.status"},
				},
			},
			Enabled: true,
		},
		{
			ID:          "knowledge.chunk.metadata.updated",
			EventType:   "knowledge.chunk.metadata.updated",
			ObjectTypes: []string{"knowledge_chunk"},
			LinkTypes:   []string{"knowledge_chunk_belongs_to_document"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "knowledge_chunk", "id": "$.id", "canonical_key": "knowledge_chunk:{id}", "label": "$.id"},
				},
				"properties": []any{
					map[string]any{"object_type": "knowledge_chunk", "object_id": "$.id", "property_id": "knowledge_base_id", "value": "$.knowledge_base_id"},
					map[string]any{"object_type": "knowledge_chunk", "object_id": "$.id", "property_id": "document_id", "value": "$.document_id"},
					map[string]any{"object_type": "knowledge_chunk", "object_id": "$.id", "property_id": "ordinal", "value": "$.ordinal"},
					map[string]any{"object_type": "knowledge_chunk", "object_id": "$.id", "property_id": "heading_path", "value": "$.heading_path"},
					map[string]any{"object_type": "knowledge_chunk", "object_id": "$.id", "property_id": "start_offset", "value": "$.start_offset"},
					map[string]any{"object_type": "knowledge_chunk", "object_id": "$.id", "property_id": "end_offset", "value": "$.end_offset"},
					map[string]any{"object_type": "knowledge_chunk", "object_id": "$.id", "property_id": "content_hash", "value": "$.content_hash"},
					map[string]any{"object_type": "knowledge_chunk", "object_id": "$.id", "property_id": "vector_id", "value": "$.vector_id"},
					map[string]any{"object_type": "knowledge_chunk", "object_id": "$.id", "property_id": "token_estimate", "value": "$.token_estimate"},
				},
				"links": []any{
					map[string]any{"type": "knowledge_chunk_belongs_to_document", "from_type": "knowledge_chunk", "from_id": "$.id", "to_type": "knowledge_document", "to_id": "$.document_id"},
				},
			},
			Enabled: true,
		},
		{
			ID:          "knowledge.page.metadata.updated",
			EventType:   "knowledge.page.metadata.updated",
			ObjectTypes: []string{"knowledge_page"},
			LinkTypes:   []string{"knowledge_page_belongs_to_document"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "knowledge_page", "id": "$.id", "canonical_key": "knowledge_page:{id}", "label": "$.id"},
				},
				"properties": []any{
					map[string]any{"object_type": "knowledge_page", "object_id": "$.id", "property_id": "knowledge_base_id", "value": "$.knowledge_base_id"},
					map[string]any{"object_type": "knowledge_page", "object_id": "$.id", "property_id": "document_id", "value": "$.document_id"},
					map[string]any{"object_type": "knowledge_page", "object_id": "$.id", "property_id": "page_number", "value": "$.page_number"},
					map[string]any{"object_type": "knowledge_page", "object_id": "$.id", "property_id": "provider", "value": "$.provider"},
					map[string]any{"object_type": "knowledge_page", "object_id": "$.id", "property_id": "provider_document_id", "value": "$.provider_document_id"},
					map[string]any{"object_type": "knowledge_page", "object_id": "$.id", "property_id": "heading_path", "value": "$.heading_path"},
					map[string]any{"object_type": "knowledge_page", "object_id": "$.id", "property_id": "source_uri", "value": "$.source_uri"},
				},
				"links": []any{
					map[string]any{"type": "knowledge_page_belongs_to_document", "from_type": "knowledge_page", "from_id": "$.id", "to_type": "knowledge_document", "to_id": "$.document_id"},
				},
			},
			Enabled: true,
		},
		{
			ID:          "knowledge.anchor.metadata.updated",
			EventType:   "knowledge.anchor.metadata.updated",
			ObjectTypes: []string{"knowledge_anchor"},
			LinkTypes:   []string{"knowledge_anchor_belongs_to_page"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "knowledge_anchor", "id": "$.id", "canonical_key": "knowledge_anchor:{id}", "label": "$.id"},
				},
				"properties": []any{
					map[string]any{"object_type": "knowledge_anchor", "object_id": "$.id", "property_id": "knowledge_base_id", "value": "$.knowledge_base_id"},
					map[string]any{"object_type": "knowledge_anchor", "object_id": "$.id", "property_id": "document_id", "value": "$.document_id"},
					map[string]any{"object_type": "knowledge_anchor", "object_id": "$.id", "property_id": "page_id", "value": "$.page_id"},
					map[string]any{"object_type": "knowledge_anchor", "object_id": "$.id", "property_id": "page_number", "value": "$.page_number"},
					map[string]any{"object_type": "knowledge_anchor", "object_id": "$.id", "property_id": "provider", "value": "$.provider"},
					map[string]any{"object_type": "knowledge_anchor", "object_id": "$.id", "property_id": "provider_document_id", "value": "$.provider_document_id"},
					map[string]any{"object_type": "knowledge_anchor", "object_id": "$.id", "property_id": "provider_node_id", "value": "$.provider_node_id"},
					map[string]any{"object_type": "knowledge_anchor", "object_id": "$.id", "property_id": "heading_path", "value": "$.heading_path"},
					map[string]any{"object_type": "knowledge_anchor", "object_id": "$.id", "property_id": "structure_path", "value": "$.structure_path"},
					map[string]any{"object_type": "knowledge_anchor", "object_id": "$.id", "property_id": "bbox", "value": "$.bbox"},
					map[string]any{"object_type": "knowledge_anchor", "object_id": "$.id", "property_id": "image_ref", "value": "$.image_ref"},
					map[string]any{"object_type": "knowledge_anchor", "object_id": "$.id", "property_id": "content_hash", "value": "$.content_hash"},
					map[string]any{"object_type": "knowledge_anchor", "object_id": "$.id", "property_id": "snippet_hash", "value": "$.snippet_hash"},
					map[string]any{"object_type": "knowledge_anchor", "object_id": "$.id", "property_id": "source_uri", "value": "$.source_uri"},
				},
				"links": []any{
					map[string]any{"type": "knowledge_anchor_belongs_to_page", "from_type": "knowledge_anchor", "from_id": "$.id", "to_type": "knowledge_page", "to_id": "$.page_id"},
				},
			},
			Enabled: true,
		},
		{
			ID:        "knowledge.document.status.updated",
			EventType: "knowledge.document.status.updated",
			Rule: map[string]any{
				"properties": []any{
					map[string]any{"object_type": "knowledge_document", "object_id": "$.id", "property_id": "status", "value": "$.status"},
				},
			},
			Enabled: true,
		},
		{
			ID:        "memory.knowledge.citation.added",
			EventType: "memory.knowledge.citation.added",
			LinkTypes: []string{
				"memory_cites_knowledge_document",
			},
			Rule: map[string]any{
				"links": []any{
					map[string]any{
						"type":      "memory_cites_knowledge_document",
						"from_type": "memory_record",
						"from_id":   "$.memory_record_id",
						"to_type":   "knowledge_document",
						"to_id":     "$.knowledge_document_id",
					},
				},
			},
			Enabled: true,
		},
		{
			ID:        "memory.knowledge_chunk.citation.added",
			EventType: "memory.knowledge_chunk.citation.added",
			LinkTypes: []string{
				"memory_cites_knowledge_chunk",
			},
			Rule: map[string]any{
				"links": []any{
					map[string]any{
						"type":      "memory_cites_knowledge_chunk",
						"from_type": "memory_record",
						"from_id":   "$.memory_record_id",
						"to_type":   "knowledge_chunk",
						"to_id":     "$.knowledge_chunk_id",
					},
				},
			},
			Enabled: true,
		},
		{
			ID:        "memory.knowledge_anchor.citation.added",
			EventType: "memory.knowledge_anchor.citation.added",
			LinkTypes: []string{
				"memory_cites_knowledge_anchor",
			},
			Rule: map[string]any{
				"links": []any{
					map[string]any{
						"type":      "memory_cites_knowledge_anchor",
						"from_type": "memory_record",
						"from_id":   "$.memory_record_id",
						"to_type":   "knowledge_anchor",
						"to_id":     "$.knowledge_anchor_id",
					},
				},
			},
			Enabled: true,
		},
		{
			ID:        "memory.lineage.linked",
			EventType: "memory.lineage.linked",
			LinkTypes: []string{
				"memory_distilled_from_memory",
			},
			Rule: map[string]any{
				"links": []any{
					map[string]any{
						"type":      "memory_distilled_from_memory",
						"from_type": "memory_record",
						"from_id":   "$.memory_record_id",
						"to_type":   "memory_record",
						"to_id":     "$.source_memory_record_id",
					},
				},
			},
			Enabled: true,
		},
		{
			ID:        "memory.scope.linked",
			EventType: "memory.scope.linked",
			LinkTypes: []string{
				"memory_about_mission",
				"memory_about_project",
				"memory_about_task",
			},
			Rule: map[string]any{
				"links": []any{
					map[string]any{
						"type":      "memory_about_mission",
						"from_type": "memory_record",
						"from_id":   "$.memory_record_id",
						"to_type":   "mission",
						"to_id":     "$.mission_id",
						"optional":  true,
					},
					map[string]any{
						"type":      "memory_about_project",
						"from_type": "memory_record",
						"from_id":   "$.memory_record_id",
						"to_type":   "project",
						"to_id":     "$.project_id",
						"optional":  true,
					},
					map[string]any{
						"type":      "memory_about_task",
						"from_type": "memory_record",
						"from_id":   "$.memory_record_id",
						"to_type":   "task",
						"to_id":     "$.task_id",
						"optional":  true,
					},
				},
			},
			Enabled: true,
		},
		{
			ID:          "memory.record.lifecycle.updated",
			EventType:   "memory.record.lifecycle.updated",
			ObjectTypes: []string{"memory_record"},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "memory_record", "id": "$.id", "canonical_key": "memory_record:{id}", "label": "$.summary"},
				},
				"properties": []any{
					map[string]any{"object_type": "memory_record", "object_id": "$.id", "property_id": "lifecycle_status", "value": "$.lifecycle_status"},
					map[string]any{"object_type": "memory_record", "object_id": "$.id", "property_id": "reference_count", "value": "$.reference_count"},
					map[string]any{"object_type": "memory_record", "object_id": "$.id", "property_id": "last_referenced_at", "value": "$.last_referenced_at"},
				},
			},
			Enabled: true,
		},
	}
	rules = append(rules, defaultPeopleV2MappingRules()...)
	rules = append(rules, defaultAgentExecutionMappingRules()...)
	rules = append(rules, defaultReliabilityMappingRules()...)
	rules = append(rules, defaultProblemHubMappingRules()...)
	rules = append(rules, defaultImprovementMappingRules()...)
	return withDefaultMappingRuleGovernance(rules)
}

func defaultProblemHubMappingRules() []MappingRule {
	return []MappingRule{
		{
			ID:          "problem.case.upserted",
			EventType:   "problem.case.upserted",
			ObjectTypes: []string{"problem_case"},
			LinkTypes: []string{
				"problem_case_tracks_failure_incident",
				"evidence_item_supports_problem_case",
				"problem_case_resolved_by_repair_rule",
			},
			Rule: map[string]any{
				"objects": []any{map[string]any{
					"type": "problem_case", "id": "$.id", "canonical_key": "problem_case:{id}", "label": "$.title",
				}},
				"properties": []any{
					map[string]any{"object_type": "problem_case", "object_id": "$.id", "property_id": "fingerprint", "value": "$.fingerprint"},
					map[string]any{"object_type": "problem_case", "object_id": "$.id", "property_id": "source_domain", "value": "$.source_domain"},
					map[string]any{"object_type": "problem_case", "object_id": "$.id", "property_id": "category", "value": "$.category"},
					map[string]any{"object_type": "problem_case", "object_id": "$.id", "property_id": "severity", "value": "$.severity"},
					map[string]any{"object_type": "problem_case", "object_id": "$.id", "property_id": "status", "value": "$.status"},
					map[string]any{"object_type": "problem_case", "object_id": "$.id", "property_id": "title", "value": "$.title"},
					map[string]any{"object_type": "problem_case", "object_id": "$.id", "property_id": "redacted_summary", "value": "$.redacted_summary", "optional": true},
					map[string]any{"object_type": "problem_case", "object_id": "$.id", "property_id": "owner_kind", "value": "$.owner_kind", "optional": true},
					map[string]any{"object_type": "problem_case", "object_id": "$.id", "property_id": "owner_id", "value": "$.owner_id", "optional": true},
					map[string]any{"object_type": "problem_case", "object_id": "$.id", "property_id": "primary_source_kind", "value": "$.primary_source_kind"},
					map[string]any{"object_type": "problem_case", "object_id": "$.id", "property_id": "primary_source_id", "value": "$.primary_source_id"},
					map[string]any{"object_type": "problem_case", "object_id": "$.id", "property_id": "evidence_ids", "value": "$.evidence_ids"},
					map[string]any{"object_type": "problem_case", "object_id": "$.id", "property_id": "occurrence_count", "value": "$.occurrence_count"},
					map[string]any{"object_type": "problem_case", "object_id": "$.id", "property_id": "latest_outcome_id", "value": "$.latest_outcome_id", "optional": true},
					map[string]any{"object_type": "problem_case", "object_id": "$.id", "property_id": "latest_outcome_success", "value": "$.latest_outcome_success", "optional": true},
					map[string]any{"object_type": "problem_case", "object_id": "$.id", "property_id": "latest_outcome_summary", "value": "$.latest_outcome_summary", "optional": true},
					map[string]any{"object_type": "problem_case", "object_id": "$.id", "property_id": "latest_resolution_kind", "value": "$.latest_resolution_kind", "optional": true},
					map[string]any{"object_type": "problem_case", "object_id": "$.id", "property_id": "latest_resolution_id", "value": "$.latest_resolution_id", "optional": true},
					map[string]any{"object_type": "problem_case", "object_id": "$.id", "property_id": "lesson_candidate_id", "value": "$.lesson_candidate_id", "optional": true},
					map[string]any{"object_type": "problem_case", "object_id": "$.id", "property_id": "problem_version", "value": "$.problem_version"},
					map[string]any{"object_type": "problem_case", "object_id": "$.id", "property_id": "first_seen_at", "value": "$.first_seen_at"},
					map[string]any{"object_type": "problem_case", "object_id": "$.id", "property_id": "last_seen_at", "value": "$.last_seen_at"},
					map[string]any{"object_type": "problem_case", "object_id": "$.id", "property_id": "resolved_at", "value": "$.resolved_at", "optional": true},
					map[string]any{"object_type": "problem_case", "object_id": "$.id", "property_id": "updated_at", "value": "$.updated_at"},
				},
				"links": []any{
					map[string]any{"type": "problem_case_tracks_failure_incident", "from_type": "problem_case", "from_id": "$.id", "to_type": "failure_incident", "to_id": "$.source_failure_incident_id", "optional": true},
					map[string]any{"type": "evidence_item_supports_problem_case", "from_type": "evidence_item", "from_id": "$.primary_evidence_id", "to_type": "problem_case", "to_id": "$.id", "optional": true},
					map[string]any{"type": "problem_case_resolved_by_repair_rule", "from_type": "problem_case", "from_id": "$.id", "to_type": "repair_rule", "to_id": "$.resolution_repair_rule_id", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:          "problem.lesson_candidate.proposed",
			EventType:   "problem.lesson_candidate.proposed",
			ObjectTypes: []string{"lesson_candidate"},
			LinkTypes: []string{
				"problem_case_proposes_lesson_candidate",
				"evidence_item_supports_lesson_candidate",
			},
			Rule: map[string]any{
				"objects": []any{map[string]any{
					"type": "lesson_candidate", "id": "$.id", "canonical_key": "lesson_candidate:{id}", "label": "$.summary",
				}},
				"properties": []any{
					map[string]any{"object_type": "lesson_candidate", "object_id": "$.id", "property_id": "memory_candidate_id", "value": "$.memory_candidate_id"},
					map[string]any{"object_type": "lesson_candidate", "object_id": "$.id", "property_id": "status", "value": "$.status"},
					map[string]any{"object_type": "lesson_candidate", "object_id": "$.id", "property_id": "proposed_layer", "value": "$.proposed_layer"},
					map[string]any{"object_type": "lesson_candidate", "object_id": "$.id", "property_id": "topic", "value": "$.topic"},
					map[string]any{"object_type": "lesson_candidate", "object_id": "$.id", "property_id": "summary", "value": "$.summary"},
					map[string]any{"object_type": "lesson_candidate", "object_id": "$.id", "property_id": "confidence", "value": "$.confidence"},
					map[string]any{"object_type": "lesson_candidate", "object_id": "$.id", "property_id": "review_required", "value": "$.review_required"},
					map[string]any{"object_type": "lesson_candidate", "object_id": "$.id", "property_id": "evidence_ids", "value": "$.evidence_ids"},
					map[string]any{"object_type": "lesson_candidate", "object_id": "$.id", "property_id": "created_at", "value": "$.created_at"},
				},
				"links": []any{
					map[string]any{"type": "problem_case_proposes_lesson_candidate", "from_type": "problem_case", "from_id": "$.problem_case_id", "to_type": "lesson_candidate", "to_id": "$.id"},
					map[string]any{"type": "evidence_item_supports_lesson_candidate", "from_type": "evidence_item", "from_id": "$.primary_evidence_id", "to_type": "lesson_candidate", "to_id": "$.id", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:        "problem.memory_record.accepted",
			EventType: "problem.memory_record.accepted",
			LinkTypes: []string{"memory_about_problem_case", "memory_realizes_lesson_candidate"},
			Rule: map[string]any{
				"properties": []any{
					map[string]any{"object_type": "lesson_candidate", "object_id": "$.problem_lesson_candidate_id", "property_id": "review_outcome", "value": "$.status", "optional": true},
					map[string]any{"object_type": "lesson_candidate", "object_id": "$.problem_lesson_candidate_id", "property_id": "accepted_memory_id", "value": "$.id", "optional": true},
				},
				"links": []any{
					map[string]any{"type": "memory_about_problem_case", "from_type": "memory_record", "from_id": "$.id", "to_type": "problem_case", "to_id": "$.problem_case_id", "optional": true},
					map[string]any{"type": "memory_realizes_lesson_candidate", "from_type": "memory_record", "from_id": "$.id", "to_type": "lesson_candidate", "to_id": "$.problem_lesson_candidate_id", "optional": true},
				},
			},
			Enabled: true,
		},
	}
}

func defaultPeopleV2MappingRules() []MappingRule {
	catalogID := "$.Value.ID || $.id"
	catalogRevision := "$.Value.Revision || $.revision || $.Metadata.Revision"
	catalogTemporal := map[string]any{
		"mode":                 string(TemporalModeStateInterval),
		"business_valid_from":  "$.Metadata.Effective.Start || $.valid_from",
		"business_valid_to":    "$.Metadata.Effective.End || $.valid_to",
		"source_recorded_at":   "$.Metadata.RecordedAt || $.recorded_at",
		"source_superseded_at": "$.Metadata.SupersededAt || $.superseded_at",
	}
	relationshipTemporal := map[string]any{
		"mode":                string(TemporalModeStateInterval),
		"business_valid_from": "$.valid_from",
		"business_valid_to":   "$.valid_to",
	}
	return []MappingRule{
		peopleV2MappingRule(
			"people.subject.v2.changed",
			"workforce_subject",
			catalogID,
			"$.Value.DisplayName || $.display_name",
			catalogTemporal,
			[]any{
				map[string]any{"object_type": "workforce_subject", "object_id": catalogID, "property_id": "subject_type", "value": "$.Value.Type || $.type || $.subject_type"},
				map[string]any{"object_type": "workforce_subject", "object_id": catalogID, "property_id": "display_name", "value": "$.Value.DisplayName || $.display_name"},
				map[string]any{"object_type": "workforce_subject", "object_id": catalogID, "property_id": "lifecycle_status", "value": "$.Value.Status || $.status || $.lifecycle_status"},
				map[string]any{"object_type": "workforce_subject", "object_id": catalogID, "property_id": "revision", "value": catalogRevision},
			},
			nil,
		),
		peopleV2MappingRule(
			"people.org_unit.v2.changed",
			"organization_unit",
			catalogID,
			"$.Value.Name || $.Value.DisplayName || $.display_name",
			catalogTemporal,
			[]any{
				map[string]any{"object_type": "organization_unit", "object_id": catalogID, "property_id": "code", "value": "$.Value.Code || $.code"},
				map[string]any{"object_type": "organization_unit", "object_id": catalogID, "property_id": "display_name", "value": "$.Value.Name || $.Value.DisplayName || $.display_name"},
				map[string]any{"object_type": "organization_unit", "object_id": catalogID, "property_id": "lifecycle_status", "value": "$.Value.Status || $.status || $.lifecycle_status"},
				map[string]any{"object_type": "organization_unit", "object_id": catalogID, "property_id": "revision", "value": catalogRevision},
			},
			[]any{
				map[string]any{
					"type": "organization_unit_part_of_organization_unit", "identity": catalogID,
					"from_type": "organization_unit", "from_id": catalogID,
					"to_type": "organization_unit", "to_id": "$.Value.ParentID || $.parent_id || $.parent_org_unit_id",
					"optional": true,
				},
			},
		),
		peopleV2MappingRule(
			"people.job_profile.v2.changed",
			"job_profile",
			catalogID,
			"$.Value.Name || $.Value.DisplayName || $.display_name",
			catalogTemporal,
			[]any{
				map[string]any{"object_type": "job_profile", "object_id": catalogID, "property_id": "code", "value": "$.Value.Code || $.code"},
				map[string]any{"object_type": "job_profile", "object_id": catalogID, "property_id": "display_name", "value": "$.Value.Name || $.Value.DisplayName || $.display_name"},
				map[string]any{"object_type": "job_profile", "object_id": catalogID, "property_id": "job_family", "value": "$.Value.JobFamily || $.job_family", "optional": true},
				map[string]any{"object_type": "job_profile", "object_id": catalogID, "property_id": "job_level", "value": "$.Value.JobLevel || $.job_level", "optional": true},
				map[string]any{"object_type": "job_profile", "object_id": catalogID, "property_id": "lifecycle_status", "value": "$.Value.Status || $.status || $.lifecycle_status"},
				map[string]any{"object_type": "job_profile", "object_id": catalogID, "property_id": "revision", "value": catalogRevision},
			},
			nil,
		),
		peopleV2MappingRule(
			"people.position.v2.changed",
			"position",
			catalogID,
			"$.Value.Name || $.Value.DisplayName || $.display_name",
			catalogTemporal,
			[]any{
				map[string]any{"object_type": "position", "object_id": catalogID, "property_id": "code", "value": "$.Value.Code || $.code"},
				map[string]any{"object_type": "position", "object_id": catalogID, "property_id": "name", "value": "$.Value.Name || $.Value.DisplayName || $.display_name"},
				map[string]any{"object_type": "position", "object_id": catalogID, "property_id": "display_name", "value": "$.Value.Name || $.Value.DisplayName || $.display_name"},
				map[string]any{"object_type": "position", "object_id": catalogID, "property_id": "org_unit_id", "value": "$.Value.OrgUnitID || $.org_unit_id"},
				map[string]any{"object_type": "position", "object_id": catalogID, "property_id": "job_profile_id", "value": "$.Value.JobProfileID || $.job_profile_id"},
				map[string]any{"object_type": "position", "object_id": catalogID, "property_id": "approved_headcount", "value": "$.Value.ApprovedHeadcount || $.approved_headcount"},
				map[string]any{"object_type": "position", "object_id": catalogID, "property_id": "is_frozen", "value": "$.Value.Frozen || $.is_frozen", "optional": true},
				map[string]any{"object_type": "position", "object_id": catalogID, "property_id": "status", "value": "$.Value.Status || $.status || $.lifecycle_status"},
				map[string]any{"object_type": "position", "object_id": catalogID, "property_id": "lifecycle_status", "value": "$.Value.Status || $.status || $.lifecycle_status"},
				map[string]any{"object_type": "position", "object_id": catalogID, "property_id": "revision", "value": catalogRevision},
			},
			[]any{
				map[string]any{
					"type": "position_in_organization_unit", "identity": catalogID,
					"from_type": "position", "from_id": catalogID,
					"to_type": "organization_unit", "to_id": "$.Value.OrgUnitID || $.org_unit_id",
				},
				map[string]any{
					"type": "position_uses_job_profile", "identity": catalogID,
					"from_type": "position", "from_id": catalogID,
					"to_type": "job_profile", "to_id": "$.Value.JobProfileID || $.job_profile_id",
				},
			},
		),
		peopleV2MappingRule(
			"people.assignment.v2.changed",
			"workforce_assignment",
			"$.id",
			"$.id",
			relationshipTemporal,
			[]any{
				map[string]any{"object_type": "workforce_assignment", "object_id": "$.id", "property_id": "subject_id", "value": "$.subject_id"},
				map[string]any{"object_type": "workforce_assignment", "object_id": "$.id", "property_id": "org_unit_id", "value": "$.org_unit_id"},
				map[string]any{"object_type": "workforce_assignment", "object_id": "$.id", "property_id": "position_id", "value": "$.position_id"},
				map[string]any{"object_type": "workforce_assignment", "object_id": "$.id", "property_id": "assignment_kind", "value": "$.assignment_kind"},
				map[string]any{"object_type": "workforce_assignment", "object_id": "$.id", "property_id": "allocation_bps", "value": "$.allocation_bps"},
				map[string]any{"object_type": "workforce_assignment", "object_id": "$.id", "property_id": "predecessor_assignment_id", "value": "$.predecessor_assignment_id", "optional": true},
				map[string]any{"object_type": "workforce_assignment", "object_id": "$.id", "property_id": "transition_kind", "value": "$.transition_kind", "optional": true},
				map[string]any{"object_type": "workforce_assignment", "object_id": "$.id", "property_id": "revision", "value": "$.revision"},
			},
			[]any{
				map[string]any{"type": "workforce_assignment_for_subject", "identity": "$.id", "from_type": "workforce_assignment", "from_id": "$.id", "to_type": "workforce_subject", "to_id": "$.subject_id"},
				map[string]any{"type": "workforce_assignment_in_organization_unit", "identity": "$.id", "from_type": "workforce_assignment", "from_id": "$.id", "to_type": "organization_unit", "to_id": "$.org_unit_id"},
				map[string]any{"type": "workforce_assignment_to_position", "identity": "$.id", "from_type": "workforce_assignment", "from_id": "$.id", "to_type": "position", "to_id": "$.position_id"},
				map[string]any{"type": "workforce_assignment_succeeds_assignment", "identity": "$.id", "from_type": "workforce_assignment", "from_id": "$.id", "to_type": "workforce_assignment", "to_id": "$.predecessor_assignment_id", "optional": true},
			},
		),
		peopleV2MappingRule(
			"people.reporting_relationship.v2.changed",
			"reporting_relationship",
			"$.id",
			"$.id",
			relationshipTemporal,
			[]any{
				map[string]any{"object_type": "reporting_relationship", "object_id": "$.id", "property_id": "subject_id", "value": "$.subject_id"},
				map[string]any{"object_type": "reporting_relationship", "object_id": "$.id", "property_id": "manager_id", "value": "$.manager_id"},
				map[string]any{"object_type": "reporting_relationship", "object_id": "$.id", "property_id": "reporting_type", "value": "$.reporting_type"},
				map[string]any{"object_type": "reporting_relationship", "object_id": "$.id", "property_id": "revision", "value": "$.revision"},
			},
			[]any{
				map[string]any{"type": "reporting_relationship_for_subject", "identity": "$.id", "from_type": "reporting_relationship", "from_id": "$.id", "to_type": "workforce_subject", "to_id": "$.subject_id"},
				map[string]any{"type": "reporting_relationship_to_manager", "identity": "$.id", "from_type": "reporting_relationship", "from_id": "$.id", "to_type": "workforce_subject", "to_id": "$.manager_id"},
			},
		),
		peopleV2MappingRule(
			"people.responsibility.v2.changed",
			"responsibility",
			"$.id",
			"$.title",
			relationshipTemporal,
			[]any{
				map[string]any{"object_type": "responsibility", "object_id": "$.id", "property_id": "code", "value": "$.code"},
				map[string]any{"object_type": "responsibility", "object_id": "$.id", "property_id": "title", "value": "$.title"},
				map[string]any{"object_type": "responsibility", "object_id": "$.id", "property_id": "description", "value": "$.description", "optional": true},
				map[string]any{"object_type": "responsibility", "object_id": "$.id", "property_id": "revision", "value": "$.revision"},
			},
			nil,
		),
		peopleV2MappingRule(
			"people.responsibility_binding.v2.changed",
			"responsibility_binding",
			"$.id",
			"$.id",
			relationshipTemporal,
			[]any{
				map[string]any{"object_type": "responsibility_binding", "object_id": "$.id", "property_id": "responsibility_id", "value": "$.responsibility_id"},
				map[string]any{"object_type": "responsibility_binding", "object_id": "$.id", "property_id": "target_type", "value": "$.target_type"},
				map[string]any{"object_type": "responsibility_binding", "object_id": "$.id", "property_id": "target_id", "value": "$.target_id"},
				map[string]any{"object_type": "responsibility_binding", "object_id": "$.id", "property_id": "revision", "value": "$.revision"},
			},
			[]any{
				map[string]any{"type": "responsibility_binding_for_responsibility", "identity": "$.id", "from_type": "responsibility_binding", "from_id": "$.id", "to_type": "responsibility", "to_id": "$.responsibility_id"},
			},
		),
	}
}

func peopleV2MappingRule(
	eventType string,
	objectType string,
	objectID string,
	label string,
	temporal map[string]any,
	properties []any,
	links []any,
) MappingRule {
	object := map[string]any{
		"type": objectType, "id": objectID, "canonical_key": objectID,
	}
	if label != "" {
		object["label"] = label
	}
	rule := map[string]any{
		"temporal": temporal,
		"objects":  []any{object},
	}
	if len(properties) > 0 {
		rule["properties"] = properties
	}
	if len(links) > 0 {
		rule["links"] = links
	}
	return MappingRule{
		ID:                 eventType,
		EventType:          eventType,
		SourceDomain:       "people",
		SourceRef:          "people.outbox.v2",
		EventSchemaVersion: "2",
		ObjectTypes:        []string{objectType},
		Rule:               rule,
		Enabled:            true,
	}
}

func defaultAgentExecutionMappingRules() []MappingRule {
	rules := []MappingRule{
		agentVersionObservedMappingRule(),
		schedulerTaskStateMappingRule("scheduler.task.updated"),
		schedulerTaskStateMappingRule("scheduler.task.cancelled"),
		{
			ID:          "agent.task.upserted",
			EventType:   "agent.task.upserted",
			ObjectTypes: []string{"agent_task"},
			LinkTypes: []string{
				"agent_assigned_to_task",
				"agent_task_realizes_task",
				"agent_task_supports_mission",
			},
			Rule: map[string]any{
				"objects": []any{map[string]any{"type": "agent_task", "id": "$.id", "canonical_key": "agent_task:{id}", "label": "$.title"}},
				"properties": []any{
					map[string]any{"object_type": "agent_task", "object_id": "$.id", "property_id": "title", "value": "$.title"},
					map[string]any{"object_type": "agent_task", "object_id": "$.id", "property_id": "description", "value": "$.description", "optional": true},
					map[string]any{"object_type": "agent_task", "object_id": "$.id", "property_id": "status", "value": "$.status"},
					map[string]any{"object_type": "agent_task", "object_id": "$.id", "property_id": "source_kind", "value": "$.source_kind"},
					map[string]any{"object_type": "agent_task", "object_id": "$.id", "property_id": "source_ref", "value": "$.source_ref", "optional": true},
					map[string]any{"object_type": "agent_task", "object_id": "$.id", "property_id": "input_ref", "value": "$.input_ref", "optional": true},
					map[string]any{"object_type": "agent_task", "object_id": "$.id", "property_id": "target_mode", "value": "$.target_mode", "optional": true},
					map[string]any{"object_type": "agent_task", "object_id": "$.id", "property_id": "source_scope_kind", "value": "$.source_scope_kind", "optional": true},
					map[string]any{"object_type": "agent_task", "object_id": "$.id", "property_id": "source_scope_id", "value": "$.source_scope_id", "optional": true},
					map[string]any{"object_type": "agent_task", "object_id": "$.id", "property_id": "target_scope_kind", "value": "$.target_scope_kind", "optional": true},
					map[string]any{"object_type": "agent_task", "object_id": "$.id", "property_id": "target_scope_id", "value": "$.target_scope_id", "optional": true},
					map[string]any{"object_type": "agent_task", "object_id": "$.id", "property_id": "risk_level", "value": "$.risk_level", "optional": true},
					map[string]any{"object_type": "agent_task", "object_id": "$.id", "property_id": "created_by", "value": "$.created_by", "optional": true},
				},
				"links": []any{
					map[string]any{"type": "agent_assigned_to_task", "from_type": "agent", "from_id": "$.agent_id", "to_type": "agent_task", "to_id": "$.id", "optional": true},
					map[string]any{"type": "agent_task_realizes_task", "from_type": "agent_task", "from_id": "$.id", "to_type": "task", "to_id": "$.mission_task_id", "optional": true},
					map[string]any{"type": "agent_task_supports_mission", "from_type": "agent_task", "from_id": "$.id", "to_type": "mission", "to_id": "$.mission_id", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:          "scheduler.run.started",
			EventType:   "scheduler.run.started",
			ObjectTypes: []string{"scheduled_run"},
			LinkTypes:   []string{"scheduled_task_has_run", "scheduled_run_triggers_agent_run"},
			Rule: map[string]any{
				"objects": []any{map[string]any{"type": "scheduled_run", "id": "$.id", "canonical_key": "scheduled_run:{id}"}},
				"properties": []any{
					map[string]any{"object_type": "scheduled_run", "object_id": "$.id", "property_id": "scheduled_task_id", "value": "$.scheduled_task_id"},
					map[string]any{"object_type": "scheduled_run", "object_id": "$.id", "property_id": "status", "value": "$.status"},
					map[string]any{"object_type": "scheduled_run", "object_id": "$.id", "property_id": "scheduled_for", "value": "$.scheduled_for", "optional": true},
					map[string]any{"object_type": "scheduled_run", "object_id": "$.id", "property_id": "started_at", "value": "$.started_at", "optional": true},
					map[string]any{"object_type": "scheduled_run", "object_id": "$.id", "property_id": "agent_run_id", "value": "$.agent_run_id", "optional": true},
					map[string]any{"object_type": "scheduled_run", "object_id": "$.id", "property_id": "agent_run_ref_status", "value": "$.agent_run_ref_status", "optional": true},
				},
				"links": []any{
					map[string]any{"type": "scheduled_task_has_run", "from_type": "scheduled_task", "from_id": "$.scheduled_task_id", "to_type": "scheduled_run", "to_id": "$.id"},
					map[string]any{"type": "scheduled_run_triggers_agent_run", "from_type": "scheduled_run", "from_id": "$.id", "to_type": "agent_run", "to_id": "$.resolved_agent_run_id", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:          "scheduler.run.completed",
			EventType:   "scheduler.run.completed",
			ObjectTypes: []string{"scheduled_run"},
			LinkTypes:   []string{"scheduled_task_has_run", "scheduled_run_triggers_agent_run"},
			Rule: map[string]any{
				"objects": []any{map[string]any{"type": "scheduled_run", "id": "$.id", "canonical_key": "scheduled_run:{id}"}},
				"properties": []any{
					map[string]any{"object_type": "scheduled_run", "object_id": "$.id", "property_id": "scheduled_task_id", "value": "$.scheduled_task_id"},
					map[string]any{"object_type": "scheduled_run", "object_id": "$.id", "property_id": "status", "value": "$.status"},
					map[string]any{"object_type": "scheduled_run", "object_id": "$.id", "property_id": "completed_at", "value": "$.completed_at", "optional": true},
					map[string]any{"object_type": "scheduled_run", "object_id": "$.id", "property_id": "agent_run_id", "value": "$.agent_run_id", "optional": true},
					map[string]any{"object_type": "scheduled_run", "object_id": "$.id", "property_id": "agent_run_ref_status", "value": "$.agent_run_ref_status", "optional": true},
					map[string]any{"object_type": "scheduled_run", "object_id": "$.id", "property_id": "error_summary", "value": "$.error_summary", "optional": true},
				},
				"links": []any{
					map[string]any{"type": "scheduled_task_has_run", "from_type": "scheduled_task", "from_id": "$.scheduled_task_id", "to_type": "scheduled_run", "to_id": "$.id"},
					map[string]any{"type": "scheduled_run_triggers_agent_run", "from_type": "scheduled_run", "from_id": "$.id", "to_type": "agent_run", "to_id": "$.resolved_agent_run_id", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:          "scheduler.run.agent_resolved",
			EventType:   "scheduler.run.agent_resolved",
			ObjectTypes: []string{"scheduled_run"},
			LinkTypes:   []string{"scheduled_run_triggers_agent_run"},
			Rule: map[string]any{
				"objects": []any{map[string]any{"type": "scheduled_run", "id": "$.id", "canonical_key": "scheduled_run:{id}"}},
				"properties": []any{
					map[string]any{"object_type": "scheduled_run", "object_id": "$.id", "property_id": "agent_run_id", "value": "$.agent_run_id"},
					map[string]any{"object_type": "scheduled_run", "object_id": "$.id", "property_id": "agent_run_ref_status", "value": "resolved"},
				},
				"links": []any{
					map[string]any{"type": "scheduled_run_triggers_agent_run", "from_type": "scheduled_run", "from_id": "$.id", "to_type": "agent_run", "to_id": "$.agent_run_id"},
				},
			},
			Enabled: true,
		},
		{
			ID:          "agent.runtime.run.started",
			EventType:   "agent.runtime.run.started",
			ObjectTypes: []string{"agent_run"},
			LinkTypes: []string{
				"agent_run_executes_agent_task",
				"agent_run_performed_by_agent",
				"agent_run_uses_agent_version",
			},
			Rule: map[string]any{
				"objects": []any{map[string]any{"type": "agent_run", "id": "$.id", "canonical_key": "agent_run:{id}"}},
				"properties": []any{
					map[string]any{"object_type": "agent_run", "object_id": "$.id", "property_id": "runtime_run_id", "value": "$.runtime_run_id"},
					map[string]any{"object_type": "agent_run", "object_id": "$.id", "property_id": "status", "value": "$.status"},
					map[string]any{"object_type": "agent_run", "object_id": "$.id", "property_id": "agent_id", "value": "$.agent_id", "optional": true},
					map[string]any{"object_type": "agent_run", "object_id": "$.id", "property_id": "agent_task_id", "value": "$.agent_task_id"},
					map[string]any{"object_type": "agent_run", "object_id": "$.id", "property_id": "source_kind", "value": "$.source_kind", "optional": true},
					map[string]any{"object_type": "agent_run", "object_id": "$.id", "property_id": "source_ref", "value": "$.source_ref", "optional": true},
					map[string]any{"object_type": "agent_run", "object_id": "$.id", "property_id": "trace_ref", "value": "$.trace_ref", "optional": true},
					map[string]any{"object_type": "agent_run", "object_id": "$.id", "property_id": "started_at", "value": "$.started_at", "optional": true},
				},
				"links": []any{
					map[string]any{"type": "agent_run_executes_agent_task", "from_type": "agent_run", "from_id": "$.id", "to_type": "agent_task", "to_id": "$.agent_task_id"},
					map[string]any{"type": "agent_run_performed_by_agent", "from_type": "agent_run", "from_id": "$.id", "to_type": "agent", "to_id": "$.agent_id", "optional": true},
					map[string]any{"type": "agent_run_uses_agent_version", "from_type": "agent_run", "from_id": "$.id", "to_type": "agent_version", "to_id": "$.agent_version_id", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:        "agent.runtime.run.completed",
			EventType: "agent.runtime.run.completed",
			Rule: map[string]any{
				"properties": []any{
					map[string]any{"object_type": "agent_run", "object_id": "$.id", "property_id": "status", "value": "$.status"},
					map[string]any{"object_type": "agent_run", "object_id": "$.id", "property_id": "summary", "value": "$.summary", "optional": true},
					map[string]any{"object_type": "agent_run", "object_id": "$.id", "property_id": "outcome", "value": "$.outcome", "optional": true},
					map[string]any{"object_type": "agent_run", "object_id": "$.id", "property_id": "completed_at", "value": "$.completed_at", "optional": true},
					map[string]any{"object_type": "agent_run", "object_id": "$.id", "property_id": "duration_ms", "value": "$.duration_ms", "optional": true},
					map[string]any{"object_type": "agent_run", "object_id": "$.id", "property_id": "error_code", "value": "$.error_code", "optional": true},
				},
			},
			Enabled: true,
		},
		contextSnapshotMappingRule(),
		responsibilityContextSnapshotMappingRule(),
		contextSnapshotMemoryMappingRule(),
		contextSnapshotKnowledgeMappingRule("knowledge_document", "context_snapshot_uses_knowledge"),
		contextSnapshotKnowledgeMappingRule("knowledge_chunk", "context_snapshot_uses_knowledge_chunk"),
		contextSnapshotKnowledgeMappingRule("knowledge_anchor", "context_snapshot_uses_knowledge_anchor"),
		traceMappingRule(),
		evidenceMappingRule(),
		artifactMappingRule(),
		actionRequestMappingRule(),
		approvalRequiredMappingRule(),
		actionPolicyDecisionMappingRule(),
		actionEvidenceAttachedMappingRule(),
	}
	for _, objectType := range actionRunTargetObjectTypes() {
		rules = append(rules, actionRunTargetMappingRule(objectType))
	}
	return rules
}

func defaultReliabilityMappingRules() []MappingRule {
	return []MappingRule{
		{
			ID:          "reliability.failure.observed",
			EventType:   "reliability.failure.observed",
			ObjectTypes: []string{"failure_incident", "evidence_item"},
			LinkTypes: []string{
				"failure_incident_observed_in_agent_run",
				"failure_incident_observed_in_action_run",
				"evidence_item_supports_failure_incident",
			},
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "failure_incident", "id": "$.id", "canonical_key": "failure_incident:{id}", "label": "$.redacted_summary"},
					map[string]any{"type": "evidence_item", "id": "failure-observation-evidence:{id}:{incident_version}", "canonical_key": "evidence_item:failure-observation-evidence:{id}:{incident_version}", "label": "$.redacted_summary"},
				},
				"properties": []any{
					map[string]any{"object_type": "failure_incident", "object_id": "$.id", "property_id": "fingerprint", "value": "$.fingerprint"},
					map[string]any{"object_type": "failure_incident", "object_id": "$.id", "property_id": "source", "value": "$.source"},
					map[string]any{"object_type": "failure_incident", "object_id": "$.id", "property_id": "component", "value": "$.component"},
					map[string]any{"object_type": "failure_incident", "object_id": "$.id", "property_id": "category", "value": "$.category"},
					map[string]any{"object_type": "failure_incident", "object_id": "$.id", "property_id": "severity", "value": "$.severity"},
					map[string]any{"object_type": "failure_incident", "object_id": "$.id", "property_id": "status", "value": "$.status"},
					map[string]any{"object_type": "failure_incident", "object_id": "$.id", "property_id": "error_code", "value": "$.error_code"},
					map[string]any{"object_type": "failure_incident", "object_id": "$.id", "property_id": "redacted_summary", "value": "$.redacted_summary"},
					map[string]any{"object_type": "failure_incident", "object_id": "$.id", "property_id": "retryable", "value": "$.retryable"},
					map[string]any{"object_type": "failure_incident", "object_id": "$.id", "property_id": "first_seen_at", "value": "$.first_seen_at"},
					map[string]any{"object_type": "failure_incident", "object_id": "$.id", "property_id": "last_seen_at", "value": "$.last_seen_at"},
					map[string]any{"object_type": "failure_incident", "object_id": "$.id", "property_id": "occurrence_count", "value": "$.occurrence_count"},
					map[string]any{"object_type": "failure_incident", "object_id": "$.id", "property_id": "incident_version", "value": "$.incident_version"},
					map[string]any{"object_type": "evidence_item", "object_id": "failure-observation-evidence:{id}:{incident_version}", "property_id": "evidence_kind", "value": "failure_observation"},
					map[string]any{"object_type": "evidence_item", "object_id": "failure-observation-evidence:{id}:{incident_version}", "property_id": "summary", "value": "$.redacted_summary"},
					map[string]any{"object_type": "evidence_item", "object_id": "failure-observation-evidence:{id}:{incident_version}", "property_id": "source_ref", "value": "failure-observation:{id}:{incident_version}"},
					map[string]any{"object_type": "evidence_item", "object_id": "failure-observation-evidence:{id}:{incident_version}", "property_id": "status", "value": "observed"},
				},
				"links": []any{
					map[string]any{"type": "failure_incident_observed_in_agent_run", "from_type": "failure_incident", "from_id": "$.id", "to_type": "agent_run", "to_id": "$.agent_run_id", "optional": true},
					map[string]any{"type": "failure_incident_observed_in_action_run", "from_type": "failure_incident", "from_id": "$.id", "to_type": "action_run", "to_id": "$.action_run_id", "optional": true},
					map[string]any{"type": "evidence_item_supports_failure_incident", "from_type": "evidence_item", "from_id": "$.evidence_id", "to_type": "failure_incident", "to_id": "$.id", "optional": true},
					map[string]any{"type": "evidence_item_supports_failure_incident", "from_type": "evidence_item", "from_id": "failure-observation-evidence:{id}:{incident_version}", "to_type": "failure_incident", "to_id": "$.id"},
				},
			},
			Enabled: true,
		},
		{
			ID:        "reliability.failure.status_changed",
			EventType: "reliability.failure.status_changed",
			Rule: map[string]any{
				"properties": []any{
					map[string]any{"object_type": "failure_incident", "object_id": "$.id", "property_id": "status", "value": "$.status"},
					map[string]any{"object_type": "failure_incident", "object_id": "$.id", "property_id": "incident_version", "value": "$.incident_version"},
				},
			},
			Enabled: true,
		},
		{
			ID:          "reliability.diagnosis_case.upserted",
			EventType:   "reliability.diagnosis_case.upserted",
			ObjectTypes: []string{"diagnosis_case"},
			LinkTypes: []string{
				"diagnosis_case_diagnoses_failure_incident",
				"evidence_item_supports_diagnosis_case",
			},
			Rule: map[string]any{
				"objects": []any{map[string]any{
					"type": "diagnosis_case", "id": "$.id", "canonical_key": "diagnosis_case:{id}", "label": "$.redacted_summary",
				}},
				"properties": []any{
					map[string]any{"object_type": "diagnosis_case", "object_id": "$.id", "property_id": "incident_version", "value": "$.incident_version"},
					map[string]any{"object_type": "diagnosis_case", "object_id": "$.id", "property_id": "root_cause_category", "value": "$.root_cause_category"},
					map[string]any{"object_type": "diagnosis_case", "object_id": "$.id", "property_id": "redacted_summary", "value": "$.redacted_summary"},
					map[string]any{"object_type": "diagnosis_case", "object_id": "$.id", "property_id": "confidence", "value": "$.confidence"},
					map[string]any{"object_type": "diagnosis_case", "object_id": "$.id", "property_id": "status", "value": "$.status"},
					map[string]any{"object_type": "diagnosis_case", "object_id": "$.id", "property_id": "evidence_ids", "value": "$.evidence_ids"},
					map[string]any{"object_type": "diagnosis_case", "object_id": "$.id", "property_id": "diagnosis_version", "value": "$.diagnosis_version"},
					map[string]any{"object_type": "diagnosis_case", "object_id": "$.id", "property_id": "proposed_by_kind", "value": "$.proposed_by_kind"},
					map[string]any{"object_type": "diagnosis_case", "object_id": "$.id", "property_id": "proposed_by_id", "value": "$.proposed_by_id"},
					map[string]any{"object_type": "diagnosis_case", "object_id": "$.id", "property_id": "reviewed_by_kind", "value": "$.reviewed_by_kind", "optional": true},
					map[string]any{"object_type": "diagnosis_case", "object_id": "$.id", "property_id": "reviewed_by_id", "value": "$.reviewed_by_id", "optional": true},
					map[string]any{"object_type": "diagnosis_case", "object_id": "$.id", "property_id": "review_reason", "value": "$.review_reason", "optional": true},
					map[string]any{"object_type": "diagnosis_case", "object_id": "$.id", "property_id": "updated_at", "value": "$.updated_at"},
				},
				"links": []any{
					map[string]any{"type": "diagnosis_case_diagnoses_failure_incident", "from_type": "diagnosis_case", "from_id": "$.id", "to_type": "failure_incident", "to_id": "$.incident_id"},
					map[string]any{"type": "evidence_item_supports_diagnosis_case", "from_type": "evidence_item", "from_id": "$.primary_evidence_id", "to_type": "diagnosis_case", "to_id": "$.id"},
				},
			},
			Enabled: true,
		},
		{
			ID:          "reliability.repair_rule.upserted",
			EventType:   "reliability.repair_rule.upserted",
			ObjectTypes: []string{"repair_rule"},
			LinkTypes:   []string{"repair_rule_learned_from_failure_incident", "repair_rule_supported_by_diagnosis_case"},
			Rule: map[string]any{
				"objects": []any{map[string]any{
					"type": "repair_rule", "id": "$.id", "canonical_key": "repair_rule:{id}", "label": "$.strategy",
				}},
				"properties": []any{
					map[string]any{"object_type": "repair_rule", "object_id": "$.id", "property_id": "selector_fingerprint", "value": "$.selector_fingerprint"},
					map[string]any{"object_type": "repair_rule", "object_id": "$.id", "property_id": "strategy", "value": "$.strategy"},
					map[string]any{"object_type": "repair_rule", "object_id": "$.id", "property_id": "action_type_id", "value": "$.action_type_id"},
					map[string]any{"object_type": "repair_rule", "object_id": "$.id", "property_id": "risk_level", "value": "$.risk_level"},
					map[string]any{"object_type": "repair_rule", "object_id": "$.id", "property_id": "status", "value": "$.status"},
					map[string]any{"object_type": "repair_rule", "object_id": "$.id", "property_id": "confidence", "value": "$.confidence"},
					map[string]any{"object_type": "repair_rule", "object_id": "$.id", "property_id": "support_count", "value": "$.support_count"},
					map[string]any{"object_type": "repair_rule", "object_id": "$.id", "property_id": "success_count", "value": "$.success_count"},
					map[string]any{"object_type": "repair_rule", "object_id": "$.id", "property_id": "failure_count", "value": "$.failure_count"},
					map[string]any{"object_type": "repair_rule", "object_id": "$.id", "property_id": "reversible", "value": "$.reversible"},
					map[string]any{"object_type": "repair_rule", "object_id": "$.id", "property_id": "idempotent", "value": "$.idempotent"},
					map[string]any{"object_type": "repair_rule", "object_id": "$.id", "property_id": "auto_apply", "value": "$.auto_apply"},
					map[string]any{"object_type": "repair_rule", "object_id": "$.id", "property_id": "rule_version", "value": "$.rule_version"},
					map[string]any{"object_type": "repair_rule", "object_id": "$.id", "property_id": "diagnosis_case_id", "value": "$.diagnosis_case_id", "optional": true},
					map[string]any{"object_type": "repair_rule", "object_id": "$.id", "property_id": "activation_decision_id", "value": "$.activation_decision_id", "optional": true},
					map[string]any{"object_type": "repair_rule", "object_id": "$.id", "property_id": "activated_by_kind", "value": "$.activated_by_kind", "optional": true},
					map[string]any{"object_type": "repair_rule", "object_id": "$.id", "property_id": "activated_by_id", "value": "$.activated_by_id", "optional": true},
					map[string]any{"object_type": "repair_rule", "object_id": "$.id", "property_id": "activated_at", "value": "$.activated_at", "optional": true},
					map[string]any{"object_type": "repair_rule", "object_id": "$.id", "property_id": "last_verified_at", "value": "$.last_verified_at", "optional": true},
					map[string]any{"object_type": "repair_rule", "object_id": "$.id", "property_id": "expires_at", "value": "$.expires_at", "optional": true},
				},
				"links": []any{
					map[string]any{"type": "repair_rule_learned_from_failure_incident", "from_type": "repair_rule", "from_id": "$.id", "to_type": "failure_incident", "to_id": "$.source_incident_id"},
					map[string]any{"type": "repair_rule_supported_by_diagnosis_case", "from_type": "repair_rule", "from_id": "$.id", "to_type": "diagnosis_case", "to_id": "$.diagnosis_case_id", "optional": true},
				},
			},
			Enabled: true,
		},
		{
			ID:          "reliability.repair_dry_run.checked",
			EventType:   "reliability.repair_dry_run.checked",
			ObjectTypes: []string{"evidence_item"},
			LinkTypes: []string{
				"evidence_item_supports_action_run",
				"evidence_item_supports_failure_incident",
				"evidence_item_supports_repair_rule",
			},
			Rule: map[string]any{
				"objects": []any{map[string]any{
					"type": "evidence_item", "id": "$.id", "canonical_key": "evidence_item:{id}", "label": "$.summary",
				}},
				"properties": []any{
					map[string]any{"object_type": "evidence_item", "object_id": "$.id", "property_id": "evidence_kind", "value": "$.evidence_kind"},
					map[string]any{"object_type": "evidence_item", "object_id": "$.id", "property_id": "summary", "value": "$.summary"},
					map[string]any{"object_type": "evidence_item", "object_id": "$.id", "property_id": "source_ref", "value": "$.source_ref"},
					map[string]any{"object_type": "evidence_item", "object_id": "$.id", "property_id": "content_ref", "value": "$.content_ref"},
					map[string]any{"object_type": "evidence_item", "object_id": "$.id", "property_id": "status", "value": "$.status"},
					map[string]any{"object_type": "evidence_item", "object_id": "$.id", "property_id": "checks", "value": "$.checks"},
					map[string]any{"object_type": "evidence_item", "object_id": "$.id", "property_id": "strategy", "value": "$.strategy"},
					map[string]any{"object_type": "evidence_item", "object_id": "$.id", "property_id": "target_kind", "value": "$.target_kind", "optional": true},
					map[string]any{"object_type": "evidence_item", "object_id": "$.id", "property_id": "target_observation_id", "value": "$.target_observation_id", "optional": true},
					map[string]any{"object_type": "evidence_item", "object_id": "$.id", "property_id": "target_mission_id", "value": "$.target_mission_id", "optional": true},
					map[string]any{"object_type": "evidence_item", "object_id": "$.id", "property_id": "target_run_id", "value": "$.target_run_id", "optional": true},
				},
				"links": []any{
					map[string]any{"type": "evidence_item_supports_action_run", "from_type": "evidence_item", "from_id": "$.id", "to_type": "action_run", "to_id": "$.action_run_id"},
					map[string]any{"type": "evidence_item_supports_failure_incident", "from_type": "evidence_item", "from_id": "$.id", "to_type": "failure_incident", "to_id": "$.failure_incident_id"},
					map[string]any{"type": "evidence_item_supports_repair_rule", "from_type": "evidence_item", "from_id": "$.id", "to_type": "repair_rule", "to_id": "$.repair_rule_id"},
				},
			},
			Enabled: true,
		},
		{
			ID:          "reliability.repair_verification.checked",
			EventType:   "reliability.repair_verification.checked",
			ObjectTypes: []string{"evidence_item"},
			LinkTypes: []string{
				"evidence_item_supports_action_run",
				"evidence_item_supports_failure_incident",
				"evidence_item_supports_repair_rule",
				"evidence_item_verifies_agent_run",
			},
			Rule: map[string]any{
				"objects": []any{map[string]any{
					"type": "evidence_item", "id": "$.id", "canonical_key": "evidence_item:{id}", "label": "$.summary",
				}},
				"properties": []any{
					map[string]any{"object_type": "evidence_item", "object_id": "$.id", "property_id": "evidence_kind", "value": "$.evidence_kind"},
					map[string]any{"object_type": "evidence_item", "object_id": "$.id", "property_id": "summary", "value": "$.summary"},
					map[string]any{"object_type": "evidence_item", "object_id": "$.id", "property_id": "source_ref", "value": "$.source_ref"},
					map[string]any{"object_type": "evidence_item", "object_id": "$.id", "property_id": "content_ref", "value": "$.content_ref"},
					map[string]any{"object_type": "evidence_item", "object_id": "$.id", "property_id": "status", "value": "$.status"},
					map[string]any{"object_type": "evidence_item", "object_id": "$.id", "property_id": "original_run_id", "value": "$.original_run_id"},
					map[string]any{"object_type": "evidence_item", "object_id": "$.id", "property_id": "retry_run_id", "value": "$.retry_run_id"},
					map[string]any{"object_type": "evidence_item", "object_id": "$.id", "property_id": "queue_status", "value": "$.queue_status"},
					map[string]any{"object_type": "evidence_item", "object_id": "$.id", "property_id": "failure_code", "value": "$.failure_code", "optional": true},
				},
				"links": []any{
					map[string]any{"type": "evidence_item_supports_action_run", "from_type": "evidence_item", "from_id": "$.id", "to_type": "action_run", "to_id": "$.action_run_id"},
					map[string]any{"type": "evidence_item_supports_failure_incident", "from_type": "evidence_item", "from_id": "$.id", "to_type": "failure_incident", "to_id": "$.failure_incident_id"},
					map[string]any{"type": "evidence_item_supports_repair_rule", "from_type": "evidence_item", "from_id": "$.id", "to_type": "repair_rule", "to_id": "$.repair_rule_id"},
					map[string]any{"type": "evidence_item_verifies_agent_run", "from_type": "evidence_item", "from_id": "$.id", "to_type": "agent_run", "to_id": "$.retry_agent_run_id"},
				},
			},
			Enabled: true,
		},
	}
}

func agentVersionObservedMappingRule() MappingRule {
	return MappingRule{
		ID:          "agent.version.observed",
		EventType:   "agent.version.observed",
		ObjectTypes: []string{"agent_version"},
		LinkTypes:   []string{"agent_has_version"},
		Rule: map[string]any{
			"objects": []any{map[string]any{"type": "agent_version", "id": "$.id", "canonical_key": "agent_version:{id}", "label": "$.version"}},
			"properties": []any{
				map[string]any{"object_type": "agent_version", "object_id": "$.id", "property_id": "agent_id", "value": "$.agent_id"},
				map[string]any{"object_type": "agent_version", "object_id": "$.id", "property_id": "version", "value": "$.version"},
				map[string]any{"object_type": "agent_version", "object_id": "$.id", "property_id": "runtime_kind", "value": "$.runtime_kind", "optional": true},
				map[string]any{"object_type": "agent_version", "object_id": "$.id", "property_id": "configuration_ref", "value": "$.configuration_ref", "optional": true},
				map[string]any{"object_type": "agent_version", "object_id": "$.id", "property_id": "status", "value": "$.status", "optional": true},
			},
			"links": []any{map[string]any{"type": "agent_has_version", "from_type": "agent", "from_id": "$.agent_id", "to_type": "agent_version", "to_id": "$.id"}},
		},
		Enabled: true,
	}
}

func schedulerTaskStateMappingRule(eventType string) MappingRule {
	return MappingRule{
		ID:          eventType,
		EventType:   eventType,
		ObjectTypes: []string{"scheduled_task"},
		LinkTypes:   []string{"scheduled_task_schedules_agent_task"},
		Rule: map[string]any{
			"objects": []any{map[string]any{"type": "scheduled_task", "id": "$.id", "canonical_key": "scheduled_task:{id}", "label": "$.title"}},
			"properties": []any{
				map[string]any{"object_type": "scheduled_task", "object_id": "$.id", "property_id": "title", "value": "$.title"},
				map[string]any{"object_type": "scheduled_task", "object_id": "$.id", "property_id": "description", "value": "$.description", "optional": true},
				map[string]any{"object_type": "scheduled_task", "object_id": "$.id", "property_id": "cron", "value": "$.cron", "optional": true},
				map[string]any{"object_type": "scheduled_task", "object_id": "$.id", "property_id": "agent_id", "value": "$.agent_id", "optional": true},
				map[string]any{"object_type": "scheduled_task", "object_id": "$.id", "property_id": "target_mode", "value": "$.target_mode", "optional": true},
				map[string]any{"object_type": "scheduled_task", "object_id": "$.id", "property_id": "source_scope_kind", "value": "$.source_scope_kind", "optional": true},
				map[string]any{"object_type": "scheduled_task", "object_id": "$.id", "property_id": "source_scope_id", "value": "$.source_scope_id", "optional": true},
				map[string]any{"object_type": "scheduled_task", "object_id": "$.id", "property_id": "target_scope_kind", "value": "$.target_scope_kind", "optional": true},
				map[string]any{"object_type": "scheduled_task", "object_id": "$.id", "property_id": "target_scope_id", "value": "$.target_scope_id", "optional": true},
				map[string]any{"object_type": "scheduled_task", "object_id": "$.id", "property_id": "status", "value": "$.status", "optional": true},
				map[string]any{"object_type": "scheduled_task", "object_id": "$.id", "property_id": "enabled", "value": "$.enabled"},
			},
			"links": []any{
				map[string]any{"type": "scheduled_task_schedules_agent_task", "from_type": "scheduled_task", "from_id": "$.id", "to_type": "agent_task", "to_id": "$.agent_task_id"},
			},
		},
		Enabled: true,
	}
}

func contextSnapshotMappingRule() MappingRule {
	return MappingRule{
		ID:          "agent.runtime.context.attached",
		EventType:   "agent.runtime.context.attached",
		ObjectTypes: []string{"context_snapshot"},
		LinkTypes:   []string{"agent_run_uses_context"},
		Rule: map[string]any{
			"objects": []any{map[string]any{"type": "context_snapshot", "id": "$.id", "canonical_key": "context_snapshot:{id}"}},
			"properties": []any{
				map[string]any{"object_type": "context_snapshot", "object_id": "$.id", "property_id": "summary", "value": "$.summary", "optional": true},
				map[string]any{"object_type": "context_snapshot", "object_id": "$.id", "property_id": "input_refs", "value": "$.input_refs", "optional": true},
				map[string]any{"object_type": "context_snapshot", "object_id": "$.id", "property_id": "memory_refs", "value": "$.memory_refs", "optional": true},
				map[string]any{"object_type": "context_snapshot", "object_id": "$.id", "property_id": "knowledge_refs", "value": "$.knowledge_refs", "optional": true},
				map[string]any{"object_type": "context_snapshot", "object_id": "$.id", "property_id": "retrieval_query", "value": "$.retrieval_query", "optional": true},
				map[string]any{"object_type": "context_snapshot", "object_id": "$.id", "property_id": "captured_at", "value": "$.captured_at", "optional": true},
			},
			"links": []any{
				map[string]any{"type": "agent_run_uses_context", "from_type": "agent_run", "from_id": "$.agent_run_id", "to_type": "context_snapshot", "to_id": "$.id"},
			},
		},
		Enabled: true,
	}
}

func responsibilityContextSnapshotMappingRule() MappingRule {
	return MappingRule{
		ID:          "agent.runtime.context.responsibility.attached",
		EventType:   "agent.runtime.context.responsibility.attached",
		ObjectTypes: []string{"responsibility_context_snapshot"},
		LinkTypes:   []string{"context_snapshot_uses_responsibility_context"},
		Rule: map[string]any{
			"objects": []any{map[string]any{
				"type": "responsibility_context_snapshot", "id": "$.id",
				"canonical_key": "responsibility_context_snapshot:{id}",
			}},
			"properties": []any{
				map[string]any{"object_type": "responsibility_context_snapshot", "object_id": "$.id", "property_id": "responsibility_id", "value": "$.responsibility_id"},
				map[string]any{"object_type": "responsibility_context_snapshot", "object_id": "$.id", "property_id": "responsibility_version", "value": "$.responsibility_version"},
				map[string]any{"object_type": "responsibility_context_snapshot", "object_id": "$.id", "property_id": "source", "value": "$.source"},
				map[string]any{"object_type": "responsibility_context_snapshot", "object_id": "$.id", "property_id": "actor_member_id", "value": "$.actor_member_id"},
				map[string]any{"object_type": "responsibility_context_snapshot", "object_id": "$.id", "property_id": "agent_profile_id", "value": "$.agent_profile_id", "optional": true},
				map[string]any{"object_type": "responsibility_context_snapshot", "object_id": "$.id", "property_id": "mission_id", "value": "$.mission_id", "optional": true},
				map[string]any{"object_type": "responsibility_context_snapshot", "object_id": "$.id", "property_id": "task_id", "value": "$.task_id", "optional": true},
				map[string]any{"object_type": "responsibility_context_snapshot", "object_id": "$.id", "property_id": "organization_id", "value": "$.organization_id", "optional": true},
				map[string]any{"object_type": "responsibility_context_snapshot", "object_id": "$.id", "property_id": "organization_name", "value": "$.organization_name", "optional": true},
				map[string]any{"object_type": "responsibility_context_snapshot", "object_id": "$.id", "property_id": "position_id", "value": "$.position_id", "optional": true},
				map[string]any{"object_type": "responsibility_context_snapshot", "object_id": "$.id", "property_id": "position_name", "value": "$.position_name", "optional": true},
				map[string]any{"object_type": "responsibility_context_snapshot", "object_id": "$.id", "property_id": "summary", "value": "$.summary", "optional": true},
				map[string]any{"object_type": "responsibility_context_snapshot", "object_id": "$.id", "property_id": "goals", "value": "$.goals", "optional": true},
				map[string]any{"object_type": "responsibility_context_snapshot", "object_id": "$.id", "property_id": "boundaries", "value": "$.boundaries", "optional": true},
				map[string]any{"object_type": "responsibility_context_snapshot", "object_id": "$.id", "property_id": "evidence_requirements", "value": "$.evidence_requirements", "optional": true},
				map[string]any{"object_type": "responsibility_context_snapshot", "object_id": "$.id", "property_id": "source_refs", "value": "$.source_refs", "optional": true},
				map[string]any{"object_type": "responsibility_context_snapshot", "object_id": "$.id", "property_id": "captured_at", "value": "$.captured_at"},
				map[string]any{"object_type": "responsibility_context_snapshot", "object_id": "$.id", "property_id": "effective_at", "value": "$.effective_at"},
				map[string]any{"object_type": "responsibility_context_snapshot", "object_id": "$.id", "property_id": "expires_at", "value": "$.expires_at", "optional": true},
			},
			"links": []any{map[string]any{
				"type":      "context_snapshot_uses_responsibility_context",
				"from_type": "context_snapshot", "from_id": "$.context_snapshot_id",
				"to_type": "responsibility_context_snapshot", "to_id": "$.id",
			}},
			"temporal": map[string]any{
				"mode": string(TemporalModeEvent), "business_valid_from": "$.effective_at", "source_recorded_at": "$.captured_at",
			},
		},
		Enabled: true,
	}
}

func contextSnapshotMemoryMappingRule() MappingRule {
	return MappingRule{
		ID:          "agent.runtime.context.memory.attached",
		EventType:   "agent.runtime.context.memory.attached",
		ObjectTypes: []string{"memory_digest"},
		LinkTypes:   []string{"context_snapshot_uses_memory_digest"},
		Rule: map[string]any{
			"objects": []any{map[string]any{"type": "memory_digest", "id": "$.memory_digest_id", "canonical_key": "memory_digest:{memory_digest_id}"}},
			"properties": []any{
				map[string]any{"object_type": "memory_digest", "object_id": "$.memory_digest_id", "property_id": "memory_kind", "value": "$.memory_kind", "optional": true},
				map[string]any{"object_type": "memory_digest", "object_id": "$.memory_digest_id", "property_id": "summary_ref", "value": "$.summary_ref", "optional": true},
				map[string]any{"object_type": "memory_digest", "object_id": "$.memory_digest_id", "property_id": "sensitivity", "value": "$.sensitivity", "optional": true},
				map[string]any{"object_type": "memory_digest", "object_id": "$.memory_digest_id", "property_id": "captured_at", "value": "$.captured_at", "optional": true},
			},
			"links": []any{
				map[string]any{"type": "context_snapshot_uses_memory_digest", "from_type": "context_snapshot", "from_id": "$.context_snapshot_id", "to_type": "memory_digest", "to_id": "$.memory_digest_id"},
			},
		},
		Enabled: true,
	}
}

func contextSnapshotKnowledgeMappingRule(objectType, linkTypeID string) MappingRule {
	eventType := "agent.runtime.context." + objectType + ".attached"
	return MappingRule{
		ID: eventType, EventType: eventType, LinkTypes: []string{linkTypeID},
		Rule: map[string]any{
			"links": []any{
				map[string]any{"type": linkTypeID, "from_type": "context_snapshot", "from_id": "$.context_snapshot_id", "to_type": objectType, "to_id": "$.knowledge_ref_id"},
			},
		},
		Enabled: true,
	}
}

func traceMappingRule() MappingRule {
	return MappingRule{
		ID:          "agent.runtime.trace.recorded",
		EventType:   "agent.runtime.trace.recorded",
		ObjectTypes: []string{"execution_trace"},
		LinkTypes:   []string{"agent_run_has_trace"},
		Rule: map[string]any{
			"objects": []any{map[string]any{"type": "execution_trace", "id": "$.id", "canonical_key": "execution_trace:{id}"}},
			"properties": []any{
				map[string]any{"object_type": "execution_trace", "object_id": "$.id", "property_id": "trace_id", "value": "$.trace_id"},
				map[string]any{"object_type": "execution_trace", "object_id": "$.id", "property_id": "trace_ref", "value": "$.trace_ref", "optional": true},
				map[string]any{"object_type": "execution_trace", "object_id": "$.id", "property_id": "summary", "value": "$.summary", "optional": true},
				map[string]any{"object_type": "execution_trace", "object_id": "$.id", "property_id": "span_count", "value": "$.span_count", "optional": true},
				map[string]any{"object_type": "execution_trace", "object_id": "$.id", "property_id": "tool_call_count", "value": "$.tool_call_count", "optional": true},
				map[string]any{"object_type": "execution_trace", "object_id": "$.id", "property_id": "started_at", "value": "$.started_at", "optional": true},
				map[string]any{"object_type": "execution_trace", "object_id": "$.id", "property_id": "completed_at", "value": "$.completed_at", "optional": true},
			},
			"links": []any{map[string]any{"type": "agent_run_has_trace", "from_type": "agent_run", "from_id": "$.agent_run_id", "to_type": "execution_trace", "to_id": "$.id"}},
		},
		Enabled: true,
	}
}

func evidenceMappingRule() MappingRule {
	return MappingRule{
		ID:          "agent.runtime.evidence.produced",
		EventType:   "agent.runtime.evidence.produced",
		ObjectTypes: []string{"evidence_item"},
		LinkTypes:   []string{"agent_run_produces_evidence", "evidence_item_supports_action_run"},
		Rule: map[string]any{
			"objects": []any{map[string]any{"type": "evidence_item", "id": "$.id", "canonical_key": "evidence_item:{id}", "label": "$.summary"}},
			"properties": []any{
				map[string]any{"object_type": "evidence_item", "object_id": "$.id", "property_id": "evidence_kind", "value": "$.evidence_kind"},
				map[string]any{"object_type": "evidence_item", "object_id": "$.id", "property_id": "summary", "value": "$.summary", "optional": true},
				map[string]any{"object_type": "evidence_item", "object_id": "$.id", "property_id": "source_ref", "value": "$.source_ref", "optional": true},
				map[string]any{"object_type": "evidence_item", "object_id": "$.id", "property_id": "content_ref", "value": "$.content_ref", "optional": true},
				map[string]any{"object_type": "evidence_item", "object_id": "$.id", "property_id": "checksum", "value": "$.checksum", "optional": true},
				map[string]any{"object_type": "evidence_item", "object_id": "$.id", "property_id": "status", "value": "$.status", "optional": true},
			},
			"links": []any{
				map[string]any{"type": "agent_run_produces_evidence", "from_type": "agent_run", "from_id": "$.agent_run_id", "to_type": "evidence_item", "to_id": "$.id"},
				map[string]any{"type": "evidence_item_supports_action_run", "from_type": "evidence_item", "from_id": "$.id", "to_type": "action_run", "to_id": "$.action_run_id", "optional": true},
			},
		},
		Enabled: true,
	}
}

func artifactMappingRule() MappingRule {
	return MappingRule{
		ID:          "agent.runtime.artifact.produced",
		EventType:   "agent.runtime.artifact.produced",
		ObjectTypes: []string{"artifact"},
		LinkTypes:   []string{"agent_run_produces_artifact"},
		Rule: map[string]any{
			"objects": []any{map[string]any{"type": "artifact", "id": "$.id", "canonical_key": "artifact:{id}", "label": "$.title"}},
			"properties": []any{
				map[string]any{"object_type": "artifact", "object_id": "$.id", "property_id": "title", "value": "$.title", "optional": true},
				map[string]any{"object_type": "artifact", "object_id": "$.id", "property_id": "artifact_type", "value": "$.artifact_type"},
				map[string]any{"object_type": "artifact", "object_id": "$.id", "property_id": "uri", "value": "$.uri", "optional": true},
				map[string]any{"object_type": "artifact", "object_id": "$.id", "property_id": "checksum", "value": "$.checksum", "optional": true},
				map[string]any{"object_type": "artifact", "object_id": "$.id", "property_id": "status", "value": "$.status", "optional": true},
			},
			"links": []any{map[string]any{"type": "agent_run_produces_artifact", "from_type": "agent_run", "from_id": "$.agent_run_id", "to_type": "artifact", "to_id": "$.id"}},
		},
		Enabled: true,
	}
}

func actionRequestMappingRule() MappingRule {
	return MappingRule{
		ID:        "agent.runtime.action.requested",
		EventType: "agent.runtime.action.requested",
		LinkTypes: []string{"agent_run_requests_action_run"},
		Rule: map[string]any{
			"links": []any{map[string]any{"type": "agent_run_requests_action_run", "from_type": "agent_run", "from_id": "$.agent_run_id", "to_type": "action_run", "to_id": "$.action_run_id"}},
		},
		Enabled: true,
	}
}

func approvalRequiredMappingRule() MappingRule {
	return MappingRule{
		ID:          "agent.runtime.approval.required",
		EventType:   "agent.runtime.approval.required",
		ObjectTypes: []string{"approval_case"},
		LinkTypes:   []string{"action_run_requires_approval_case"},
		Rule: map[string]any{
			"objects": []any{map[string]any{"type": "approval_case", "id": "$.approval_case_id", "canonical_key": "approval_case:{approval_case_id}", "label": "$.title"}},
			"properties": []any{
				map[string]any{"object_type": "approval_case", "object_id": "$.approval_case_id", "property_id": "title", "value": "$.title"},
				map[string]any{"object_type": "approval_case", "object_id": "$.approval_case_id", "property_id": "status", "value": "$.status"},
				map[string]any{"object_type": "approval_case", "object_id": "$.approval_case_id", "property_id": "risk_level", "value": "$.risk_level"},
			},
			"links": []any{map[string]any{"type": "action_run_requires_approval_case", "from_type": "action_run", "from_id": "$.action_run_id", "to_type": "approval_case", "to_id": "$.approval_case_id"}},
		},
		Enabled: true,
	}
}

func actionPolicyDecisionMappingRule() MappingRule {
	return MappingRule{
		ID:          "action.run.policy.decided",
		EventType:   "action.run.policy.decided",
		ObjectTypes: []string{"policy_decision"},
		LinkTypes:   []string{"action_run_authorized_by_policy"},
		Rule: map[string]any{
			"objects": []any{map[string]any{"type": "policy_decision", "id": "$.id", "canonical_key": "policy_decision:{id}", "label": "$.id"}},
			"properties": []any{
				map[string]any{"object_type": "policy_decision", "object_id": "$.id", "property_id": "decision", "value": "$.decision"},
				map[string]any{"object_type": "policy_decision", "object_id": "$.id", "property_id": "policy_id", "value": "$.policy_id", "optional": true},
				map[string]any{"object_type": "policy_decision", "object_id": "$.id", "property_id": "reason", "value": "$.reason", "optional": true},
				map[string]any{"object_type": "policy_decision", "object_id": "$.id", "property_id": "evaluated_at", "value": "$.evaluated_at", "optional": true},
			},
			"links": []any{map[string]any{"type": "action_run_authorized_by_policy", "from_type": "action_run", "from_id": "$.action_run_id", "to_type": "policy_decision", "to_id": "$.id"}},
		},
		Enabled: true,
	}
}

func actionEvidenceAttachedMappingRule() MappingRule {
	return MappingRule{
		ID:        "action.run.evidence.attached",
		EventType: "action.run.evidence.attached",
		LinkTypes: []string{"evidence_item_supports_action_run"},
		Rule: map[string]any{
			"links": []any{map[string]any{"type": "evidence_item_supports_action_run", "from_type": "evidence_item", "from_id": "$.evidence_id", "to_type": "action_run", "to_id": "$.action_run_id"}},
		},
		Enabled: true,
	}
}

func actionRunTargetMappingRule(objectType string) MappingRule {
	eventType := "action.run.targeted." + objectType
	linkType := "action_run_targets_" + objectType
	return MappingRule{
		ID: eventType, EventType: eventType, LinkTypes: []string{linkType},
		Rule: map[string]any{"links": []any{map[string]any{
			"type": linkType, "from_type": "action_run", "from_id": "$.action_run_id", "to_type": objectType, "to_id": "$.object_id",
		}}},
		Enabled: true,
	}
}

func actionWritebackMappingRule(eventType string) MappingRule {
	return MappingRule{
		ID: eventType, EventType: eventType,
		Rule: map[string]any{
			"properties": []any{
				map[string]any{"object_type": "action_run", "object_id": "$.action_run_id", "property_id": "writeback_receipt_id", "value": "$.receipt_id"},
				map[string]any{"object_type": "action_run", "object_id": "$.action_run_id", "property_id": "writeback_status", "value": "$.status"},
				map[string]any{"object_type": "action_run", "object_id": "$.action_run_id", "property_id": "writeback_target_system", "value": "$.target_system", "optional": true},
			},
		},
		Enabled: true,
	}
}

func withDefaultMappingRuleGovernance(rules []MappingRule) []MappingRule {
	for i := range rules {
		if rules[i].SourceDomain == "" {
			rules[i].SourceDomain = defaultMappingRuleSourceDomain(rules[i].EventType)
		}
		if rules[i].SourceRef == "" {
			rules[i].SourceRef = defaultMappingRuleSourceRef(rules[i].SourceDomain)
		}
		if rules[i].EventSchemaVersion == "" {
			rules[i].EventSchemaVersion = "v1"
		}
	}
	return rules
}

func defaultMappingRuleSourceDomain(eventType string) string {
	switch {
	case strings.HasPrefix(eventType, "knowledge."):
		return "knowledge"
	case strings.HasPrefix(eventType, "memory."):
		return "memory"
	case strings.HasPrefix(eventType, "project."), strings.HasPrefix(eventType, "mission."):
		return "mission_control"
	case strings.HasPrefix(eventType, "people."):
		return "operations"
	case strings.HasPrefix(eventType, "planning."):
		return "planning"
	case strings.HasPrefix(eventType, "organization."):
		return "operations"
	case strings.HasPrefix(eventType, "legal."):
		return "legal"
	case strings.HasPrefix(eventType, "development."):
		return "development"
	case strings.HasPrefix(eventType, "value."):
		return "value"
	case strings.HasPrefix(eventType, "resource."):
		return "resource_admin"
	case strings.HasPrefix(eventType, "action."):
		return "action_contract"
	case strings.HasPrefix(eventType, "scheduler."):
		return "scheduler"
	case strings.HasPrefix(eventType, "agent."):
		return "agent_runtime"
	case strings.HasPrefix(eventType, "reliability."):
		return "reliability"
	case strings.HasPrefix(eventType, "problem."):
		return "problem_hub"
	case strings.HasPrefix(eventType, "improvement."):
		return "improvement"
	default:
		return "ontology"
	}
}

func defaultMappingRuleSourceRef(sourceDomain string) string {
	switch sourceDomain {
	case "knowledge":
		return "internal/app/knowledgeadmin"
	case "memory":
		return "internal/app/memoryadmin"
	case "problem_hub":
		return "internal/app/problemhub"
	case "improvement":
		return "internal/app/improvement"
	case "mission_control":
		return "internal/app/projectadmin"
	case "operations":
		return "internal/app/operations"
	case "planning":
		return "internal/app/ontology/defaults"
	case "legal":
		return "internal/app/legaladmin"
	case "development":
		return "internal/app/developmentadmin"
	case "value":
		return "internal/app/valueadmin"
	case "resource_admin":
		return "internal/app/resourceadmin"
	case "action_contract":
		return "internal/app/actioncontract"
	case "scheduler":
		return "internal/app/scheduler"
	case "agent_runtime":
		return "internal/agent/runtime"
	case "reliability":
		return "internal/app/reliability"
	default:
		return "internal/app/ontology/defaults"
	}
}

func objectType(id string, displayName string, canonicalKeyRule string, ownerDomain string, properties ...PropertyDefinition) ObjectType {
	value := ObjectType{
		ID:               id,
		TenantScope:      TenantScopeTenant,
		DisplayName:      displayName,
		CanonicalKeyRule: canonicalKeyRule,
		OwnerDomain:      ownerDomain,
		Sensitivity:      "internal",
		Lifecycle:        "active",
		TemporalMode:     TemporalModeStatic,
		Properties:       properties,
	}
	for index := range value.Properties {
		if value.Properties[index].TemporalMode == "" {
			value.Properties[index].TemporalMode = TemporalModeStatic
		}
	}
	return value
}

func temporalObjectType(id string, displayName string, canonicalKeyRule string, ownerDomain string, mode TemporalMode, properties ...PropertyDefinition) ObjectType {
	value := objectType(id, displayName, canonicalKeyRule, ownerDomain, properties...)
	value.TemporalMode = mode
	for index := range value.Properties {
		value.Properties[index].TemporalMode = mode
	}
	return value
}

func property(id string, valueType ValueType, required bool) PropertyDefinition {
	return PropertyDefinition{
		PropertyID:   id,
		DisplayName:  id,
		ValueType:    valueType,
		TemporalMode: TemporalModeStatic,
		Required:     required,
		Sensitivity:  "internal",
	}
}

func linkType(id string, displayName string, from string, to string, cardinality Cardinality, evidenceRequired bool) LinkType {
	return LinkType{
		ID:               id,
		TenantScope:      TenantScopeTenant,
		DisplayName:      displayName,
		FromObjectType:   from,
		ToObjectType:     to,
		Cardinality:      cardinality,
		TemporalMode:     TemporalModeStatic,
		EvidenceRequired: evidenceRequired,
		SuggestedAllowed: true,
	}
}

func temporalLinkType(id string, displayName string, from string, to string, cardinality Cardinality, evidenceRequired bool, mode TemporalMode) LinkType {
	value := linkType(id, displayName, from, to, cardinality, evidenceRequired)
	value.TemporalMode = mode
	return value
}
