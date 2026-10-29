package ontology

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type MemoryRegistry struct {
	mu         sync.RWMutex
	objects    map[string]ObjectType
	links      map[string]LinkType
	properties map[string]PropertyType
	mappings   map[string]MappingRule
	now        func() time.Time
}

func (r *MemoryRegistry) GetRegistryDefinitions(ctx context.Context, kind RegistryDefinitionKind, ids []string) ([]RegistryDefinition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > 500 {
		return nil, fmt.Errorf("%w: too many registry definition ids", ErrInvalidDefinition)
	}
	if kind != RegistryDefinitionObject && kind != RegistryDefinitionLink && kind != RegistryDefinitionProperty && kind != RegistryDefinitionMapping {
		return nil, fmt.Errorf("%w: unsupported registry definition kind %q", ErrInvalidDefinition, kind)
	}

	seen := make(map[string]struct{}, len(ids))
	normalizedIDs := make([]string, 0, len(ids))
	for _, raw := range ids {
		id := normalizeID(raw)
		if id == "" || len(id) > 256 {
			return nil, fmt.Errorf("%w: definition id is invalid", ErrInvalidDefinition)
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		normalizedIDs = append(normalizedIDs, id)
	}
	sort.Strings(normalizedIDs)

	r.mu.RLock()
	defer r.mu.RUnlock()
	definitions := make([]RegistryDefinition, 0, len(normalizedIDs))
	for _, id := range normalizedIDs {
		switch kind {
		case RegistryDefinitionObject:
			value, ok := r.objects[id]
			if ok {
				value = cloneObjectType(value)
				definitions = append(definitions, RegistryDefinition{Kind: kind, ID: value.ID, TenantID: value.TenantID, ObjectType: &value})
			}
		case RegistryDefinitionLink:
			value, ok := r.links[id]
			if ok {
				definitions = append(definitions, RegistryDefinition{Kind: kind, ID: value.ID, TenantID: value.TenantID, LinkType: &value})
			}
		case RegistryDefinitionProperty:
			value, ok := r.properties[id]
			if ok {
				definitions = append(definitions, RegistryDefinition{Kind: kind, ID: value.ID, TenantID: value.TenantID, PropertyType: &value})
			}
		case RegistryDefinitionMapping:
			value, ok := r.mappings[id]
			if ok {
				value = cloneMappingRule(value)
				definitions = append(definitions, RegistryDefinition{Kind: kind, ID: value.ID, TenantID: value.TenantID, MappingRule: &value})
			}
		}
	}
	return definitions, nil
}

func NewMemoryRegistry() *MemoryRegistry {
	return &MemoryRegistry{
		objects:    map[string]ObjectType{},
		links:      map[string]LinkType{},
		properties: map[string]PropertyType{},
		mappings:   map[string]MappingRule{},
		now:        time.Now,
	}
}

func (r *MemoryRegistry) SaveObjectType(ctx context.Context, objectType ObjectType) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	normalized, err := normalizeObjectType(objectType)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	stampOntologyDefinition(&normalized.CreatedAt, &normalized.UpdatedAt, &normalized.Version, r.now)
	r.objects[normalized.ID] = normalized
	return nil
}

func (r *MemoryRegistry) SaveObjectTypeWithExpectedVersion(ctx context.Context, objectType ObjectType, expectedVersion int) (ObjectType, error) {
	if err := ctx.Err(); err != nil {
		return ObjectType{}, err
	}
	normalized, err := normalizeObjectType(objectType)
	if err != nil {
		return ObjectType{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.objects[normalized.ID]
	if !ok {
		return ObjectType{}, fmt.Errorf("%w: object type %s", ErrNotFound, normalized.ID)
	}
	if expectedVersion > 0 && current.Version != expectedVersion {
		return ObjectType{}, fmt.Errorf("%w: object type %s expected version %d, current version %d", ErrVersionConflict, normalized.ID, expectedVersion, current.Version)
	}
	normalized.CreatedAt = current.CreatedAt
	normalized.Version = expectedVersion + 1
	stampOntologyDefinition(&normalized.CreatedAt, &normalized.UpdatedAt, &normalized.Version, r.now)
	r.objects[normalized.ID] = normalized
	return cloneObjectType(normalized), nil
}

func (r *MemoryRegistry) GetObjectType(ctx context.Context, id string) (ObjectType, error) {
	if err := ctx.Err(); err != nil {
		return ObjectType{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	objectType, ok := r.objects[normalizeID(id)]
	if !ok {
		return ObjectType{}, fmt.Errorf("%w: object type %s", ErrNotFound, id)
	}
	return cloneObjectType(objectType), nil
}

func (r *MemoryRegistry) ListObjectTypes(ctx context.Context) ([]ObjectType, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ObjectType, 0, len(r.objects))
	for _, objectType := range r.objects {
		out = append(out, cloneObjectType(objectType))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (r *MemoryRegistry) HasObjectType(ctx context.Context, id string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.objects[normalizeID(id)]
	return ok, nil
}

func (r *MemoryRegistry) SaveLinkType(ctx context.Context, linkType LinkType) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	normalized, err := normalizeLinkType(linkType)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.objects[normalized.FromObjectType]; !ok {
		return fmt.Errorf("%w: %s", ErrUnknownObjectType, normalized.FromObjectType)
	}
	if _, ok := r.objects[normalized.ToObjectType]; !ok {
		return fmt.Errorf("%w: %s", ErrUnknownObjectType, normalized.ToObjectType)
	}
	stampOntologyDefinition(&normalized.CreatedAt, &normalized.UpdatedAt, &normalized.Version, r.now)
	r.links[normalized.ID] = normalized
	return nil
}

func (r *MemoryRegistry) SaveLinkTypeWithExpectedVersion(ctx context.Context, linkType LinkType, expectedVersion int) (LinkType, error) {
	if err := ctx.Err(); err != nil {
		return LinkType{}, err
	}
	normalized, err := normalizeLinkType(linkType)
	if err != nil {
		return LinkType{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.links[normalized.ID]
	if !ok {
		return LinkType{}, fmt.Errorf("%w: link type %s", ErrNotFound, normalized.ID)
	}
	if expectedVersion > 0 && current.Version != expectedVersion {
		return LinkType{}, fmt.Errorf("%w: link type %s expected version %d, current version %d", ErrVersionConflict, normalized.ID, expectedVersion, current.Version)
	}
	if _, ok := r.objects[normalized.FromObjectType]; !ok {
		return LinkType{}, fmt.Errorf("%w: %s", ErrUnknownObjectType, normalized.FromObjectType)
	}
	if _, ok := r.objects[normalized.ToObjectType]; !ok {
		return LinkType{}, fmt.Errorf("%w: %s", ErrUnknownObjectType, normalized.ToObjectType)
	}
	normalized.CreatedAt = current.CreatedAt
	normalized.Version = expectedVersion + 1
	stampOntologyDefinition(&normalized.CreatedAt, &normalized.UpdatedAt, &normalized.Version, r.now)
	r.links[normalized.ID] = normalized
	return normalized, nil
}

func (r *MemoryRegistry) GetLinkType(ctx context.Context, id string) (LinkType, error) {
	if err := ctx.Err(); err != nil {
		return LinkType{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	linkType, ok := r.links[normalizeID(id)]
	if !ok {
		return LinkType{}, fmt.Errorf("%w: link type %s", ErrNotFound, id)
	}
	return linkType, nil
}

func (r *MemoryRegistry) ListLinkTypes(ctx context.Context) ([]LinkType, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]LinkType, 0, len(r.links))
	for _, linkType := range r.links {
		out = append(out, linkType)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (r *MemoryRegistry) HasLinkType(ctx context.Context, id string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.links[normalizeID(id)]
	return ok, nil
}

func (r *MemoryRegistry) SavePropertyType(ctx context.Context, propertyType PropertyType) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	normalized, err := normalizePropertyType(propertyType)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.objects[normalized.ObjectTypeID]; !ok {
		return fmt.Errorf("%w: %s", ErrUnknownObjectType, normalized.ObjectTypeID)
	}
	stampOntologyDefinition(&normalized.CreatedAt, &normalized.UpdatedAt, &normalized.Version, r.now)
	r.properties[normalized.ID] = normalized
	return nil
}

func (r *MemoryRegistry) SavePropertyTypeWithExpectedVersion(ctx context.Context, propertyType PropertyType, expectedVersion int) (PropertyType, error) {
	if err := ctx.Err(); err != nil {
		return PropertyType{}, err
	}
	normalized, err := normalizePropertyType(propertyType)
	if err != nil {
		return PropertyType{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.properties[normalized.ID]
	if !ok {
		return PropertyType{}, fmt.Errorf("%w: property type %s", ErrNotFound, normalized.ID)
	}
	if expectedVersion > 0 && current.Version != expectedVersion {
		return PropertyType{}, fmt.Errorf("%w: property type %s expected version %d, current version %d", ErrVersionConflict, normalized.ID, expectedVersion, current.Version)
	}
	if _, ok := r.objects[normalized.ObjectTypeID]; !ok {
		return PropertyType{}, fmt.Errorf("%w: %s", ErrUnknownObjectType, normalized.ObjectTypeID)
	}
	normalized.CreatedAt = current.CreatedAt
	normalized.Version = expectedVersion + 1
	stampOntologyDefinition(&normalized.CreatedAt, &normalized.UpdatedAt, &normalized.Version, r.now)
	r.properties[normalized.ID] = normalized
	return normalized, nil
}

func (r *MemoryRegistry) GetPropertyType(ctx context.Context, id string) (PropertyType, error) {
	if err := ctx.Err(); err != nil {
		return PropertyType{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	propertyType, ok := r.properties[normalizeID(id)]
	if !ok {
		return PropertyType{}, fmt.Errorf("%w: property type %s", ErrNotFound, id)
	}
	return propertyType, nil
}

func (r *MemoryRegistry) ListPropertyTypes(ctx context.Context, objectTypeID string) ([]PropertyType, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	objectTypeID = normalizeID(objectTypeID)
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]PropertyType, 0, len(r.properties))
	for _, propertyType := range r.properties {
		if objectTypeID != "" && propertyType.ObjectTypeID != objectTypeID {
			continue
		}
		out = append(out, propertyType)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (r *MemoryRegistry) SaveMappingRule(ctx context.Context, rule MappingRule) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	normalized, err := normalizeMappingRule(rule)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, objectType := range normalized.ObjectTypes {
		if _, ok := r.objects[objectType]; !ok {
			return fmt.Errorf("%w: %s", ErrUnknownObjectType, objectType)
		}
	}
	for _, linkType := range normalized.LinkTypes {
		if _, ok := r.links[linkType]; !ok {
			return fmt.Errorf("%w: %s", ErrUnknownLinkType, linkType)
		}
	}
	if err := validateMappingRuleTemplates(normalized, mappingRuleLookup{
		objectTypeExists: func(id string) (bool, error) {
			_, ok := r.objects[normalizeID(id)]
			return ok, nil
		},
		linkType: func(id string) (LinkType, error) {
			linkType, ok := r.links[normalizeID(id)]
			if !ok {
				return LinkType{}, fmt.Errorf("%w: %s", ErrUnknownLinkType, id)
			}
			return linkType, nil
		},
		propertyExists: func(objectTypeID string, propertyID string) (bool, error) {
			objectTypeID = normalizeID(objectTypeID)
			propertyID = normalizeID(propertyID)
			for _, propertyType := range r.properties {
				if propertyType.ObjectTypeID == objectTypeID && propertyType.PropertyID == propertyID {
					return true, nil
				}
			}
			return false, nil
		},
	}); err != nil {
		return err
	}
	stampOntologyDefinition(&normalized.CreatedAt, &normalized.UpdatedAt, &normalized.Version, r.now)
	r.mappings[normalized.ID] = normalized
	return nil
}

func (r *MemoryRegistry) SaveMappingRuleWithExpectedVersion(ctx context.Context, rule MappingRule, expectedVersion int) (MappingRule, error) {
	if err := ctx.Err(); err != nil {
		return MappingRule{}, err
	}
	normalized, err := normalizeMappingRule(rule)
	if err != nil {
		return MappingRule{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.mappings[normalized.ID]
	if !ok {
		return MappingRule{}, fmt.Errorf("%w: mapping rule %s", ErrNotFound, normalized.ID)
	}
	if expectedVersion > 0 && current.Version != expectedVersion {
		return MappingRule{}, fmt.Errorf("%w: mapping rule %s expected version %d, current version %d", ErrVersionConflict, normalized.ID, expectedVersion, current.Version)
	}
	for _, objectType := range normalized.ObjectTypes {
		if _, ok := r.objects[objectType]; !ok {
			return MappingRule{}, fmt.Errorf("%w: %s", ErrUnknownObjectType, objectType)
		}
	}
	for _, linkType := range normalized.LinkTypes {
		if _, ok := r.links[linkType]; !ok {
			return MappingRule{}, fmt.Errorf("%w: %s", ErrUnknownLinkType, linkType)
		}
	}
	normalized.CreatedAt = current.CreatedAt
	normalized.Version = expectedVersion + 1
	stampOntologyDefinition(&normalized.CreatedAt, &normalized.UpdatedAt, &normalized.Version, r.now)
	r.mappings[normalized.ID] = normalized
	return cloneMappingRule(normalized), nil
}

func (r *MemoryRegistry) ListMappingRules(ctx context.Context, tenantID string) ([]MappingRule, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tenantID = strings.TrimSpace(tenantID)
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]MappingRule, 0, len(r.mappings))
	for _, rule := range r.mappings {
		if tenantID != "" && rule.TenantID != "" && rule.TenantID != tenantID {
			continue
		}
		out = append(out, cloneMappingRule(rule))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func normalizeObjectType(objectType ObjectType) (ObjectType, error) {
	objectType.ID = normalizeID(objectType.ID)
	objectType.TenantID = strings.TrimSpace(objectType.TenantID)
	objectType.DisplayName = strings.TrimSpace(objectType.DisplayName)
	objectType.CanonicalKeyRule = strings.TrimSpace(objectType.CanonicalKeyRule)
	objectType.OwnerDomain = strings.TrimSpace(objectType.OwnerDomain)
	objectType.Sensitivity = strings.TrimSpace(objectType.Sensitivity)
	objectType.Lifecycle = strings.TrimSpace(objectType.Lifecycle)
	if objectType.TenantScope == "" {
		objectType.TenantScope = TenantScopeTenant
	}
	var err error
	objectType.TemporalMode, err = normalizeTemporalMode(objectType.TemporalMode)
	if err != nil {
		return ObjectType{}, err
	}
	if objectType.ID == "" || objectType.DisplayName == "" || objectType.CanonicalKeyRule == "" {
		return ObjectType{}, fmt.Errorf("%w: object type id, display_name, and canonical_key_rule are required", ErrInvalidDefinition)
	}
	for i := range objectType.Properties {
		objectType.Properties[i].PropertyID = normalizeID(objectType.Properties[i].PropertyID)
		objectType.Properties[i].DisplayName = strings.TrimSpace(objectType.Properties[i].DisplayName)
		objectType.Properties[i].Sensitivity = strings.TrimSpace(objectType.Properties[i].Sensitivity)
		if objectType.Properties[i].ValueType == "" {
			objectType.Properties[i].ValueType = ValueTypeString
		}
		if !isSupportedValueType(objectType.Properties[i].ValueType) {
			return ObjectType{}, fmt.Errorf("%w: object property value_type %q is not supported", ErrInvalidDefinition, objectType.Properties[i].ValueType)
		}
		if objectType.Properties[i].TemporalMode == "" {
			objectType.Properties[i].TemporalMode = objectType.TemporalMode
		}
		objectType.Properties[i].TemporalMode, err = normalizeTemporalMode(objectType.Properties[i].TemporalMode)
		if err != nil {
			return ObjectType{}, err
		}
		if objectType.Properties[i].PropertyID == "" {
			return ObjectType{}, fmt.Errorf("%w: property_id is required", ErrInvalidDefinition)
		}
	}
	objectType.AllowedLinkTypes = normalizeIDs(objectType.AllowedLinkTypes)
	objectType.AllowedActions = normalizeIDs(objectType.AllowedActions)
	return objectType, nil
}

func normalizeLinkType(linkType LinkType) (LinkType, error) {
	linkType.ID = normalizeID(linkType.ID)
	linkType.TenantID = strings.TrimSpace(linkType.TenantID)
	linkType.DisplayName = strings.TrimSpace(linkType.DisplayName)
	linkType.FromObjectType = normalizeID(linkType.FromObjectType)
	linkType.ToObjectType = normalizeID(linkType.ToObjectType)
	if linkType.TenantScope == "" {
		linkType.TenantScope = TenantScopeTenant
	}
	if linkType.Cardinality == "" {
		linkType.Cardinality = CardinalityManyToMany
	}
	var err error
	linkType.TemporalMode, err = normalizeTemporalMode(linkType.TemporalMode)
	if err != nil {
		return LinkType{}, err
	}
	if linkType.ID == "" || linkType.FromObjectType == "" || linkType.ToObjectType == "" {
		return LinkType{}, fmt.Errorf("%w: link type id, from_object_type, and to_object_type are required", ErrInvalidDefinition)
	}
	linkType.AllowedActions = normalizeIDs(linkType.AllowedActions)
	return linkType, nil
}

func normalizePropertyType(propertyType PropertyType) (PropertyType, error) {
	propertyType.ID = normalizeID(propertyType.ID)
	propertyType.TenantID = strings.TrimSpace(propertyType.TenantID)
	propertyType.ObjectTypeID = normalizeID(propertyType.ObjectTypeID)
	propertyType.PropertyID = normalizeID(propertyType.PropertyID)
	propertyType.DisplayName = strings.TrimSpace(propertyType.DisplayName)
	propertyType.AuthoritySource = strings.TrimSpace(propertyType.AuthoritySource)
	propertyType.Sensitivity = strings.TrimSpace(propertyType.Sensitivity)
	if propertyType.ValueType == "" {
		propertyType.ValueType = ValueTypeString
	}
	if !isSupportedValueType(propertyType.ValueType) {
		return PropertyType{}, fmt.Errorf("%w: property type value_type %q is not supported", ErrInvalidDefinition, propertyType.ValueType)
	}
	var err error
	propertyType.TemporalMode, err = normalizeTemporalMode(propertyType.TemporalMode)
	if err != nil {
		return PropertyType{}, err
	}
	if propertyType.ConflictPolicy == "" {
		propertyType.ConflictPolicy = ConflictPolicyManualReview
	}
	if propertyType.ID == "" || propertyType.ObjectTypeID == "" || propertyType.PropertyID == "" {
		return PropertyType{}, fmt.Errorf("%w: property type id, object_type_id, and property_id are required", ErrInvalidDefinition)
	}
	propertyType.AllowedActions = normalizeIDs(propertyType.AllowedActions)
	return propertyType, nil
}

func normalizeTemporalMode(mode TemporalMode) (TemporalMode, error) {
	if mode == "" {
		return TemporalModeStatic, nil
	}
	switch mode {
	case TemporalModeStatic, TemporalModeStateInterval, TemporalModeEvent, TemporalModeMeasurement:
		return mode, nil
	default:
		return "", fmt.Errorf("%w: temporal_mode %q is not supported", ErrInvalidDefinition, mode)
	}
}

func isSupportedValueType(valueType ValueType) bool {
	switch valueType {
	case ValueTypeString, ValueTypeNumber, ValueTypeBoolean, ValueTypeObject, ValueTypeArray, ValueTypeRef,
		ValueTypeDate, ValueTypeDateTime, ValueTypeDuration, ValueTypeEnum:
		return true
	default:
		return false
	}
}

func normalizeMappingRule(rule MappingRule) (MappingRule, error) {
	rule.ID = normalizeID(rule.ID)
	rule.TenantID = strings.TrimSpace(rule.TenantID)
	rule.EventType = strings.TrimSpace(rule.EventType)
	rule.SourceDomain = normalizeID(rule.SourceDomain)
	rule.SourceRef = strings.TrimSpace(rule.SourceRef)
	rule.EventSchemaVersion = strings.TrimSpace(rule.EventSchemaVersion)
	rule.RuleHash = strings.TrimSpace(rule.RuleHash)
	rule.ObjectTypes = normalizeIDs(rule.ObjectTypes)
	rule.LinkTypes = normalizeIDs(rule.LinkTypes)
	if rule.Rule == nil {
		rule.Rule = map[string]any{}
	}
	if rule.ID == "" || rule.EventType == "" {
		return MappingRule{}, fmt.Errorf("%w: mapping rule id and event_type are required", ErrInvalidDefinition)
	}
	if rule.RuleHash == "" {
		rule.RuleHash = hashMappingRule(rule.Rule)
	}
	return rule, nil
}

func hashMappingRule(rule map[string]any) string {
	raw, err := json.Marshal(rule)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func normalizeID(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizeIDs(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		id := normalizeID(value)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func stampOntologyDefinition(createdAt *time.Time, updatedAt *time.Time, version *int, now func() time.Time) {
	if now == nil {
		now = time.Now
	}
	at := now().UTC()
	if createdAt.IsZero() {
		*createdAt = at
	}
	*updatedAt = at
	if *version <= 0 {
		*version = 1
	}
}

func cloneObjectType(src ObjectType) ObjectType {
	src.Properties = append([]PropertyDefinition(nil), src.Properties...)
	src.AllowedLinkTypes = append([]string(nil), src.AllowedLinkTypes...)
	src.AllowedActions = append([]string(nil), src.AllowedActions...)
	return src
}

func cloneMappingRule(src MappingRule) MappingRule {
	src.ObjectTypes = append([]string(nil), src.ObjectTypes...)
	src.LinkTypes = append([]string(nil), src.LinkTypes...)
	if src.Rule != nil {
		dst := make(map[string]any, len(src.Rule))
		for key, value := range src.Rule {
			dst[key] = value
		}
		src.Rule = dst
	}
	return src
}
