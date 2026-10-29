package ontology

import (
	"context"
	"testing"
)

func TestRegistrySnapshotFiltersTenantScopedDefinitions(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()

	for _, objectType := range []ObjectType{
		{ID: "shared_object", TenantScope: TenantScopeGlobal, DisplayName: "Shared", CanonicalKeyRule: "shared:{id}"},
		{ID: "tenant_one_object", TenantID: "tenant-1", TenantScope: TenantScopeTenant, DisplayName: "Tenant One", CanonicalKeyRule: "tenant_one:{id}"},
		{ID: "tenant_two_object", TenantID: "tenant-2", TenantScope: TenantScopeTenant, DisplayName: "Tenant Two", CanonicalKeyRule: "tenant_two:{id}"},
	} {
		if err := registry.SaveObjectType(ctx, objectType); err != nil {
			t.Fatalf("SaveObjectType(%s): %v", objectType.ID, err)
		}
	}
	for _, linkType := range []LinkType{
		{ID: "tenant_one_link", TenantID: "tenant-1", TenantScope: TenantScopeTenant, DisplayName: "Tenant One Link", FromObjectType: "shared_object", ToObjectType: "tenant_one_object"},
		{ID: "tenant_two_link", TenantID: "tenant-2", TenantScope: TenantScopeTenant, DisplayName: "Tenant Two Link", FromObjectType: "shared_object", ToObjectType: "tenant_two_object"},
	} {
		if err := registry.SaveLinkType(ctx, linkType); err != nil {
			t.Fatalf("SaveLinkType(%s): %v", linkType.ID, err)
		}
	}
	for _, propertyType := range []PropertyType{
		{ID: "tenant_one_object.title", TenantID: "tenant-1", ObjectTypeID: "tenant_one_object", PropertyID: "title", ValueType: ValueTypeString},
		{ID: "tenant_two_object.title", TenantID: "tenant-2", ObjectTypeID: "tenant_two_object", PropertyID: "title", ValueType: ValueTypeString},
	} {
		if err := registry.SavePropertyType(ctx, propertyType); err != nil {
			t.Fatalf("SavePropertyType(%s): %v", propertyType.ID, err)
		}
	}
	for _, rule := range []MappingRule{
		{
			ID:          "tenant_one.created",
			TenantID:    "tenant-1",
			EventType:   "tenant_one.created",
			ObjectTypes: []string{"tenant_one_object"},
			Enabled:     true,
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "tenant_one_object", "id": "$.id", "canonical_key": "tenant_one:{id}"},
				},
			},
		},
		{
			ID:          "tenant_two.created",
			TenantID:    "tenant-2",
			EventType:   "tenant_two.created",
			ObjectTypes: []string{"tenant_two_object"},
			Enabled:     true,
			Rule: map[string]any{
				"objects": []any{
					map[string]any{"type": "tenant_two_object", "id": "$.id", "canonical_key": "tenant_two:{id}"},
				},
			},
		},
	} {
		if err := registry.SaveMappingRule(ctx, rule); err != nil {
			t.Fatalf("SaveMappingRule(%s): %v", rule.ID, err)
		}
	}

	snapshot, err := GetRegistrySnapshot(ctx, registry, "tenant-1")
	if err != nil {
		t.Fatalf("GetRegistrySnapshot: %v", err)
	}
	if !snapshotHasObjectType(snapshot, "shared_object") || !snapshotHasObjectType(snapshot, "tenant_one_object") {
		t.Fatalf("snapshot missing shared or tenant definition: %+v", snapshot.ObjectTypes)
	}
	if snapshotHasObjectType(snapshot, "tenant_two_object") {
		t.Fatalf("snapshot leaked tenant-2 object type: %+v", snapshot.ObjectTypes)
	}
	if snapshotHasLinkType(snapshot, "tenant_two_link") {
		t.Fatalf("snapshot leaked tenant-2 link type: %+v", snapshot.LinkTypes)
	}
	if snapshotHasPropertyType(snapshot, "tenant_two_object.title") {
		t.Fatalf("snapshot leaked tenant-2 property type: %+v", snapshot.PropertyTypes)
	}
	if snapshotHasMappingRule(snapshot, "tenant_two.created") {
		t.Fatalf("snapshot leaked tenant-2 mapping rule: %+v", snapshot.MappingRules)
	}
}

func snapshotHasObjectType(snapshot RegistrySnapshot, id string) bool {
	for _, objectType := range snapshot.ObjectTypes {
		if objectType.ID == id {
			return true
		}
	}
	return false
}

func snapshotHasLinkType(snapshot RegistrySnapshot, id string) bool {
	for _, linkType := range snapshot.LinkTypes {
		if linkType.ID == id {
			return true
		}
	}
	return false
}

func snapshotHasPropertyType(snapshot RegistrySnapshot, id string) bool {
	for _, propertyType := range snapshot.PropertyTypes {
		if propertyType.ID == id {
			return true
		}
	}
	return false
}

func snapshotHasMappingRule(snapshot RegistrySnapshot, id string) bool {
	for _, rule := range snapshot.MappingRules {
		if rule.ID == id {
			return true
		}
	}
	return false
}
