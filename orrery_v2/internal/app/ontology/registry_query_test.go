package ontology

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestRegistryQueryUsesBoundedTenantAwareKeysetPages(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner([]migration.Migration{ontologyRegistryMigration(), ontologyTemporalSemanticsMigration(), ontologyMappingRuleGovernanceMigration(), ontologyActionBindingsMigration()}).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	registry := NewPlatformRegistry(store)
	for _, item := range []ObjectType{
		{ID: "a-global", DisplayName: "Global", CanonicalKeyRule: "global:{id}"},
		{ID: "b-one", TenantID: "tenant-1", TenantScope: TenantScopeTenant, DisplayName: "One", CanonicalKeyRule: "one:{id}"},
		{ID: "c-two", TenantID: "tenant-2", TenantScope: TenantScopeTenant, DisplayName: "Two", CanonicalKeyRule: "two:{id}"},
		{ID: "d-one", TenantID: "tenant-1", TenantScope: TenantScopeTenant, DisplayName: "One D", CanonicalKeyRule: "one-d:{id}"},
	} {
		if err := registry.SaveObjectType(ctx, item); err != nil {
			t.Fatalf("SaveObjectType(%s): %v", item.ID, err)
		}
	}
	first, err := registry.ListRegistryDefinitions(ctx, RegistryDefinitionQuery{Kind: RegistryDefinitionObject, TenantID: "tenant-1", Limit: 2})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if !first.HasMore || len(first.Items) != 2 || first.Items[0].ID != "a-global" || first.Items[1].ID != "b-one" {
		t.Fatalf("first page = %+v", first)
	}
	second, err := registry.ListRegistryDefinitions(ctx, RegistryDefinitionQuery{Kind: RegistryDefinitionObject, TenantID: "tenant-1", AfterID: "b-one", Limit: 2})
	if err != nil || second.HasMore || len(second.Items) != 1 || second.Items[0].ID != "d-one" {
		t.Fatalf("second page = %+v, err=%v", second, err)
	}
	wrongTenant, err := registry.GetRegistryDefinition(ctx, RegistryDefinitionObject, "c-two")
	if err != nil || wrongTenant.TenantID != "tenant-2" {
		t.Fatalf("exact load must preserve definition-owned tenant for caller validation: %+v, err=%v", wrongTenant, err)
	}
	if _, err := registry.GetRegistryDefinition(ctx, RegistryDefinitionObject, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing exact error = %v", err)
	}
	batch, err := registry.GetRegistryDefinitions(ctx, RegistryDefinitionObject, []string{"c-two", "a-global", "missing", "a-global"})
	if err != nil || len(batch) != 2 || batch[0].ID != "a-global" || batch[1].ID != "c-two" || batch[1].TenantID != "tenant-2" {
		t.Fatalf("batch exact definitions = %+v, err=%v", batch, err)
	}
}

func TestRegistryQueryListsEnabledMappingRulesByTenantAndEvent(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner([]migration.Migration{ontologyRegistryMigration(), ontologyTemporalSemanticsMigration(), ontologyMappingRuleGovernanceMigration()}).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	registry := NewPlatformRegistry(store)
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "mission", DisplayName: "Mission", CanonicalKeyRule: "mission:{id}"}); err != nil {
		t.Fatalf("SaveObjectType: %v", err)
	}
	template := map[string]any{"objects": []any{map[string]any{"type": "mission", "id": "$.id", "canonical_key": "mission:{id}"}}}
	for _, rule := range []MappingRule{
		{ID: "a-global", EventType: "mission.updated", ObjectTypes: []string{"mission"}, Rule: template, Enabled: true},
		{ID: "b-tenant", TenantID: "tenant-1", EventType: "mission.updated", ObjectTypes: []string{"mission"}, Rule: template, Enabled: true},
		{ID: "c-other", TenantID: "tenant-2", EventType: "mission.updated", ObjectTypes: []string{"mission"}, Rule: template, Enabled: true},
		{ID: "d-disabled", TenantID: "tenant-1", EventType: "mission.updated", ObjectTypes: []string{"mission"}, Rule: template, Enabled: false},
		{ID: "e-other-event", TenantID: "tenant-1", EventType: "mission.created", ObjectTypes: []string{"mission"}, Rule: template, Enabled: true},
	} {
		if err := registry.SaveMappingRule(ctx, rule); err != nil {
			t.Fatalf("SaveMappingRule(%s): %v", rule.ID, err)
		}
	}
	page, err := registry.ListEnabledMappingRulesByEventType(ctx, "tenant-1", "mission.updated", 1)
	if err != nil {
		t.Fatalf("ListEnabledMappingRulesByEventType: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "a-global" || !page.HasMore {
		t.Fatalf("bounded mapping candidates = %+v", page)
	}
	page, err = registry.ListEnabledMappingRulesByEventType(ctx, "tenant-1", "mission.updated", 10)
	if err != nil || page.HasMore || len(page.Items) != 2 || page.Items[0].ID != "a-global" || page.Items[1].ID != "b-tenant" {
		t.Fatalf("mapping candidates = %+v, err=%v", page, err)
	}
	if _, err := registry.ListEnabledMappingRulesByEventType(ctx, "", "mission.updated", 10); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("missing tenant error = %v", err)
	}
}
