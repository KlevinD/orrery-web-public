package ontology

import "context"
import "strings"

type RegistrySnapshot struct {
	ObjectTypes   []ObjectType        `json:"object_types"`
	LinkTypes     []LinkType          `json:"link_types"`
	PropertyTypes []PropertyType      `json:"property_types"`
	MappingRules  []MappingRule       `json:"mapping_rules"`
	ActionTypes   []ActionTypeSummary `json:"action_types,omitempty"`
}

type ActionTypeSummary struct {
	ID               string   `json:"id"`
	TenantID         string   `json:"tenant_id,omitempty"`
	Name             string   `json:"name,omitempty"`
	Category         string   `json:"category,omitempty"`
	Description      string   `json:"description,omitempty"`
	ObjectTypeRefs   []string `json:"object_type_refs,omitempty"`
	ProviderKind     string   `json:"provider_kind,omitempty"`
	ProviderRef      string   `json:"provider_ref,omitempty"`
	ResourceResolver string   `json:"resource_resolver,omitempty"`
	PermissionScope  string   `json:"permission_scope,omitempty"`
	FieldProfile     string   `json:"field_profile,omitempty"`
	DataPolicy       string   `json:"data_policy,omitempty"`
	PurposePolicy    string   `json:"purpose_policy,omitempty"`
	RiskLevel        string   `json:"risk_level,omitempty"`
	DryRunSupported  bool     `json:"dry_run_supported,omitempty"`
	Enabled          bool     `json:"enabled"`
	Version          string   `json:"version,omitempty"`
}

func GetRegistrySnapshot(ctx context.Context, registry Registry, tenantID string) (RegistrySnapshot, error) {
	if err := ctx.Err(); err != nil {
		return RegistrySnapshot{}, err
	}
	if registry == nil {
		return RegistrySnapshot{}, ErrInvalidDefinition
	}
	objectTypes, err := registry.ListObjectTypes(ctx)
	if err != nil {
		return RegistrySnapshot{}, err
	}
	linkTypes, err := registry.ListLinkTypes(ctx)
	if err != nil {
		return RegistrySnapshot{}, err
	}
	propertyTypes, err := registry.ListPropertyTypes(ctx, "")
	if err != nil {
		return RegistrySnapshot{}, err
	}
	mappingRules, err := registry.ListMappingRules(ctx, tenantID)
	if err != nil {
		return RegistrySnapshot{}, err
	}
	return RegistrySnapshot{
		ObjectTypes:   filterSnapshotObjectTypes(objectTypes, tenantID),
		LinkTypes:     filterSnapshotLinkTypes(linkTypes, tenantID),
		PropertyTypes: filterSnapshotPropertyTypes(propertyTypes, tenantID),
		MappingRules:  mappingRules,
	}, nil
}

func filterSnapshotObjectTypes(objectTypes []ObjectType, tenantID string) []ObjectType {
	if strings.TrimSpace(tenantID) == "" {
		return objectTypes
	}
	filtered := make([]ObjectType, 0, len(objectTypes))
	for _, objectType := range objectTypes {
		if snapshotDefinitionVisibleToTenant(objectType.TenantID, tenantID) {
			filtered = append(filtered, objectType)
		}
	}
	return filtered
}

func filterSnapshotLinkTypes(linkTypes []LinkType, tenantID string) []LinkType {
	if strings.TrimSpace(tenantID) == "" {
		return linkTypes
	}
	filtered := make([]LinkType, 0, len(linkTypes))
	for _, linkType := range linkTypes {
		if snapshotDefinitionVisibleToTenant(linkType.TenantID, tenantID) {
			filtered = append(filtered, linkType)
		}
	}
	return filtered
}

func filterSnapshotPropertyTypes(propertyTypes []PropertyType, tenantID string) []PropertyType {
	if strings.TrimSpace(tenantID) == "" {
		return propertyTypes
	}
	filtered := make([]PropertyType, 0, len(propertyTypes))
	for _, propertyType := range propertyTypes {
		if snapshotDefinitionVisibleToTenant(propertyType.TenantID, tenantID) {
			filtered = append(filtered, propertyType)
		}
	}
	return filtered
}

func snapshotDefinitionVisibleToTenant(definitionTenantID string, requestedTenantID string) bool {
	definitionTenantID = strings.TrimSpace(definitionTenantID)
	requestedTenantID = strings.TrimSpace(requestedTenantID)
	return requestedTenantID == "" ||
		definitionTenantID == "" ||
		definitionTenantID == "system" ||
		definitionTenantID == requestedTenantID
}
