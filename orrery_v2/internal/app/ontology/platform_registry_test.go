package ontology

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestPlatformRegistryPersistsOntologyDefinitions(t *testing.T) {
	ctx := context.Background()
	sqlStore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "ontology-platform-registry"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer sqlStore.Close()
	if err := migration.NewRunner([]migration.Migration{ontologyRegistryMigration(), ontologyTemporalSemanticsMigration(), ontologyMappingRuleGovernanceMigration(), ontologyActionBindingsMigration()}).Up(ctx, sqlStore); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	registry := NewPlatformRegistry(sqlStore)

	if err := seedMissionTaskOntology(ctx, registry); err != nil {
		t.Fatalf("seed ontology: %v", err)
	}

	reopened := NewPlatformRegistry(sqlStore)
	objects, err := reopened.ListObjectTypes(ctx)
	if err != nil {
		t.Fatalf("ListObjectTypes: %v", err)
	}
	if len(objects) != 2 || objects[0].ID != "mission" || objects[1].ID != "task" {
		t.Fatalf("objects = %+v", objects)
	}
	link, err := reopened.GetLinkType(ctx, "mission_has_task")
	if err != nil {
		t.Fatalf("GetLinkType: %v", err)
	}
	if link.FromObjectType != "mission" || link.ToObjectType != "task" || len(link.AllowedActions) != 1 || link.AllowedActions[0] != "mission.plan" {
		t.Fatalf("link = %+v", link)
	}
	properties, err := reopened.ListPropertyTypes(ctx, "mission")
	if err != nil {
		t.Fatalf("ListPropertyTypes: %v", err)
	}
	if len(properties) != 1 || properties[0].ID != "mission.title" || len(properties[0].AllowedActions) != 1 || properties[0].AllowedActions[0] != "mission.plan" {
		t.Fatalf("properties = %+v", properties)
	}
	rules, err := reopened.ListMappingRules(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("ListMappingRules: %v", err)
	}
	if len(rules) != 1 || rules[0].EventType != "mission.created" || rules[0].Rule["objects"] == nil {
		t.Fatalf("rules = %+v", rules)
	}
	if rules[0].SourceDomain != "mission_control" || rules[0].SourceRef != "internal/mission/event" || rules[0].EventSchemaVersion != "v1" {
		t.Fatalf("rule governance metadata = %+v", rules[0])
	}
	if rules[0].RuleHash == "" {
		t.Fatalf("rule hash should be generated")
	}
}

func TestPlatformRegistryRejectsUnknownReferencedTypes(t *testing.T) {
	ctx := context.Background()
	sqlStore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "ontology-platform-registry-references"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer sqlStore.Close()
	if err := migration.NewRunner([]migration.Migration{ontologyRegistryMigration(), ontologyTemporalSemanticsMigration(), ontologyMappingRuleGovernanceMigration(), ontologyActionBindingsMigration()}).Up(ctx, sqlStore); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	registry := NewPlatformRegistry(sqlStore)
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "mission", DisplayName: "Mission", CanonicalKeyRule: "mission:{id}"}); err != nil {
		t.Fatalf("SaveObjectType: %v", err)
	}
	if err := registry.SaveLinkType(ctx, LinkType{ID: "mission_has_task", FromObjectType: "mission", ToObjectType: "task"}); !errors.Is(err, ErrUnknownObjectType) {
		t.Fatalf("SaveLinkType error = %v, want ErrUnknownObjectType", err)
	}
	if err := registry.SavePropertyType(ctx, PropertyType{ID: "task.title", ObjectTypeID: "task", PropertyID: "title"}); !errors.Is(err, ErrUnknownObjectType) {
		t.Fatalf("SavePropertyType error = %v, want ErrUnknownObjectType", err)
	}
}

func TestPlatformRegistryRejectsCrossTenantDefinitionOverwrite(t *testing.T) {
	ctx := context.Background()
	sqlStore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "ontology-platform-registry-tenant-owner"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer sqlStore.Close()
	if err := migration.NewRunner([]migration.Migration{ontologyRegistryMigration(), ontologyTemporalSemanticsMigration(), ontologyMappingRuleGovernanceMigration(), ontologyActionBindingsMigration()}).Up(ctx, sqlStore); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	registry := NewPlatformRegistry(sqlStore)
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "case", TenantID: "tenant-1", TenantScope: TenantScopeTenant, DisplayName: "Tenant one case", CanonicalKeyRule: "case:{id}"}); err != nil {
		t.Fatalf("SaveObjectType tenant-1: %v", err)
	}
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "case", TenantID: "tenant-2", TenantScope: TenantScopeTenant, DisplayName: "Tenant two case", CanonicalKeyRule: "case:{id}"}); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("cross-tenant SaveObjectType error = %v, want ErrInvalidDefinition", err)
	}
	stored, err := registry.GetObjectType(ctx, "case")
	if err != nil {
		t.Fatalf("GetObjectType: %v", err)
	}
	if stored.TenantID != "tenant-1" || stored.DisplayName != "Tenant one case" {
		t.Fatalf("cross-tenant save changed definition: %+v", stored)
	}
}

func TestPlatformRegistryRejectsInvalidMappingRuleTemplate(t *testing.T) {
	ctx := context.Background()
	sqlStore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "ontology-platform-registry-invalid-rule"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer sqlStore.Close()
	if err := migration.NewRunner([]migration.Migration{ontologyRegistryMigration(), ontologyTemporalSemanticsMigration(), ontologyMappingRuleGovernanceMigration(), ontologyActionBindingsMigration()}).Up(ctx, sqlStore); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	registry := NewPlatformRegistry(sqlStore)
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "mission", DisplayName: "Mission", CanonicalKeyRule: "mission:{id}"}); err != nil {
		t.Fatalf("SaveObjectType: %v", err)
	}

	err = registry.SaveMappingRule(ctx, MappingRule{
		ID:          "mission.created",
		EventType:   "mission.created",
		ObjectTypes: []string{"mission"},
		Rule: map[string]any{
			"properties": []any{
				map[string]any{"object_type": "mission", "object_id": "$.id", "property_id": "title", "value": "$.title"},
			},
		},
		Enabled: true,
	})
	if !errors.Is(err, ErrUnknownPropertyType) {
		t.Fatalf("SaveMappingRule error = %v, want ErrUnknownPropertyType", err)
	}
}

func seedMissionTaskOntology(ctx context.Context, registry Registry) error {
	if err := registry.SaveObjectType(ctx, ObjectType{
		ID:               "mission",
		DisplayName:      "Mission",
		CanonicalKeyRule: "mission:{id}",
		Properties: []PropertyDefinition{{
			PropertyID: "title",
			ValueType:  ValueTypeString,
			Required:   true,
		}},
	}); err != nil {
		return err
	}
	if err := registry.SaveObjectType(ctx, ObjectType{
		ID:               "task",
		DisplayName:      "Task",
		CanonicalKeyRule: "task:{id}",
	}); err != nil {
		return err
	}
	if err := registry.SaveLinkType(ctx, LinkType{
		ID:             "mission_has_task",
		FromObjectType: "mission",
		ToObjectType:   "task",
		Cardinality:    CardinalityOneToMany,
		AllowedActions: []string{"mission.plan"},
	}); err != nil {
		return err
	}
	if err := registry.SavePropertyType(ctx, PropertyType{
		ID:              "mission.title",
		ObjectTypeID:    "mission",
		PropertyID:      "title",
		ValueType:       ValueTypeString,
		AuthoritySource: "mission_control",
		ConflictPolicy:  ConflictPolicyAuthorityWins,
		AllowedActions:  []string{"mission.plan"},
	}); err != nil {
		return err
	}
	return registry.SaveMappingRule(ctx, MappingRule{
		ID:                 "mission.created",
		TenantID:           "tenant-1",
		EventType:          "mission.created",
		SourceDomain:       "mission_control",
		SourceRef:          "internal/mission/event",
		EventSchemaVersion: "v1",
		ObjectTypes:        []string{"mission"},
		LinkTypes:          []string{"mission_has_task"},
		Rule: map[string]any{
			"objects": []any{
				map[string]any{"type": "mission", "id": "$.mission_id", "canonical_key": "mission:{mission_id}"},
				map[string]any{"type": "task", "id": "$.task_id", "canonical_key": "task:{task_id}"},
			},
			"properties": []any{
				map[string]any{"object_type": "mission", "object_id": "$.mission_id", "property_id": "title", "value": "$.title"},
			},
			"links": []any{
				map[string]any{"type": "mission_has_task", "from_type": "mission", "from_id": "$.mission_id", "to_type": "task", "to_id": "$.task_id"},
			},
		},
		Enabled: true,
	})
}
