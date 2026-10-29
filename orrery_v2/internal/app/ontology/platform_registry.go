package ontology

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/graphprotocol"
)

type PlatformRegistry struct {
	store *db.Store
	tx    db.Tx
	now   func() time.Time
}

func NewPlatformRegistry(store *db.Store) *PlatformRegistry {
	return &PlatformRegistry{store: store, now: time.Now}
}

func NewPlatformRegistryTx(tx db.Tx) *PlatformRegistry {
	return &PlatformRegistry{tx: tx, now: time.Now}
}

func (r *PlatformRegistry) ready() error {
	if r == nil || (r.store == nil && r.tx == nil) {
		return fmt.Errorf("%w: store is required", ErrInvalidDefinition)
	}
	return nil
}

func (r *PlatformRegistry) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if r.tx != nil {
		return r.tx.ExecContext(ctx, query, args...)
	}
	return r.store.Exec(ctx, query, args...)
}

func (r *PlatformRegistry) query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if r.tx != nil {
		return r.tx.QueryContext(ctx, query, args...)
	}
	return r.store.Query(ctx, query, args...)
}

func (r *PlatformRegistry) queryRow(ctx context.Context, query string, args ...any) rowScanner {
	if r.tx != nil {
		return r.tx.QueryRowContext(ctx, query, args...)
	}
	return r.store.QueryRow(ctx, query, args...)
}

func (r *PlatformRegistry) SaveObjectType(ctx context.Context, objectType ObjectType) error {
	if err := r.ready(); err != nil {
		return err
	}
	normalized, err := normalizeObjectType(objectType)
	if err != nil {
		return err
	}
	if err := r.ensureDefinitionTenant(ctx, "ontology_object_types", normalized.ID, normalized.TenantID); err != nil {
		return err
	}
	stampOntologyDefinition(&normalized.CreatedAt, &normalized.UpdatedAt, &normalized.Version, r.now)
	_, err = r.exec(ctx, `
INSERT INTO ontology_object_types (
	id, tenant_id, tenant_scope, display_name, canonical_key_rule, owner_domain,
	sensitivity, lifecycle, temporal_mode, properties_json, allowed_link_types_json, allowed_actions_json,
	version, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	tenant_id = excluded.tenant_id,
	tenant_scope = excluded.tenant_scope,
	display_name = excluded.display_name,
	canonical_key_rule = excluded.canonical_key_rule,
	owner_domain = excluded.owner_domain,
	sensitivity = excluded.sensitivity,
	lifecycle = excluded.lifecycle,
	temporal_mode = excluded.temporal_mode,
	properties_json = excluded.properties_json,
	allowed_link_types_json = excluded.allowed_link_types_json,
	allowed_actions_json = excluded.allowed_actions_json,
	version = excluded.version,
	updated_at = excluded.updated_at`,
		normalized.ID,
		normalized.TenantID,
		normalized.TenantScope,
		normalized.DisplayName,
		normalized.CanonicalKeyRule,
		normalized.OwnerDomain,
		normalized.Sensitivity,
		normalized.Lifecycle,
		normalized.TemporalMode,
		mustJSON(normalized.Properties),
		mustJSON(normalized.AllowedLinkTypes),
		mustJSON(normalized.AllowedActions),
		normalized.Version,
		timeToUnixNano(normalized.CreatedAt),
		timeToUnixNano(normalized.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("save ontology object type: %w", err)
	}
	return nil
}

func (r *PlatformRegistry) SaveObjectTypeWithExpectedVersion(ctx context.Context, objectType ObjectType, expectedVersion int) (ObjectType, error) {
	if err := r.ready(); err != nil {
		return ObjectType{}, err
	}
	if err := r.ensureDefinitionTenant(ctx, "ontology_object_types", normalizeID(objectType.ID), strings.TrimSpace(objectType.TenantID)); err != nil {
		return ObjectType{}, err
	}
	current, err := r.GetObjectType(ctx, objectType.ID)
	if err != nil {
		return ObjectType{}, err
	}
	if expectedVersion > 0 && current.Version != expectedVersion {
		return ObjectType{}, fmt.Errorf("%w: object type %s expected version %d, current version %d", ErrVersionConflict, normalizeID(objectType.ID), expectedVersion, current.Version)
	}
	normalized, err := normalizeObjectType(objectType)
	if err != nil {
		return ObjectType{}, err
	}
	normalized.CreatedAt = current.CreatedAt
	normalized.Version = expectedVersion + 1
	stampOntologyDefinition(&normalized.CreatedAt, &normalized.UpdatedAt, &normalized.Version, r.now)
	result, err := r.exec(ctx, `
UPDATE ontology_object_types SET
	tenant_id = ?,
	tenant_scope = ?,
	display_name = ?,
	canonical_key_rule = ?,
	owner_domain = ?,
	sensitivity = ?,
	lifecycle = ?,
	temporal_mode = ?,
	properties_json = ?,
	allowed_link_types_json = ?,
	allowed_actions_json = ?,
	version = ?,
	updated_at = ?
WHERE id = ? AND version = ?`,
		normalized.TenantID,
		normalized.TenantScope,
		normalized.DisplayName,
		normalized.CanonicalKeyRule,
		normalized.OwnerDomain,
		normalized.Sensitivity,
		normalized.Lifecycle,
		normalized.TemporalMode,
		mustJSON(normalized.Properties),
		mustJSON(normalized.AllowedLinkTypes),
		mustJSON(normalized.AllowedActions),
		normalized.Version,
		timeToUnixNano(normalized.UpdatedAt),
		normalized.ID,
		expectedVersion,
	)
	if err != nil {
		return ObjectType{}, fmt.Errorf("save ontology object type with expected version: %w", err)
	}
	if err := requireOntologyRowsAffected(result, "object type", normalized.ID, expectedVersion); err != nil {
		return ObjectType{}, err
	}
	return r.GetObjectType(ctx, normalized.ID)
}

func (r *PlatformRegistry) GetObjectType(ctx context.Context, id string) (ObjectType, error) {
	if err := r.ready(); err != nil {
		return ObjectType{}, err
	}
	row := r.queryRow(ctx, objectTypeSelectSQL()+` WHERE id = ?`, normalizeID(id))
	objectType, err := scanObjectType(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ObjectType{}, fmt.Errorf("%w: object type %s", ErrNotFound, id)
	}
	return objectType, err
}

func (r *PlatformRegistry) ListObjectTypes(ctx context.Context) ([]ObjectType, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	rows, err := r.query(ctx, objectTypeSelectSQL()+` ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list ontology object types: %w", err)
	}
	defer rows.Close()
	var out []ObjectType
	for rows.Next() {
		objectType, err := scanObjectType(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, objectType)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list ontology object types: %w", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (r *PlatformRegistry) HasObjectType(ctx context.Context, id string) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	var found string
	err := r.queryRow(ctx, `SELECT id FROM ontology_object_types WHERE id = ?`, normalizeID(id)).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return found != "", nil
}

func (r *PlatformRegistry) SaveLinkType(ctx context.Context, linkType LinkType) error {
	if err := r.ready(); err != nil {
		return err
	}
	normalized, err := normalizeLinkType(linkType)
	if err != nil {
		return err
	}
	if err := r.ensureDefinitionTenant(ctx, "ontology_link_types", normalized.ID, normalized.TenantID); err != nil {
		return err
	}
	if err := r.requireObjectType(ctx, normalized.FromObjectType); err != nil {
		return err
	}
	if err := r.requireObjectType(ctx, normalized.ToObjectType); err != nil {
		return err
	}
	stampOntologyDefinition(&normalized.CreatedAt, &normalized.UpdatedAt, &normalized.Version, r.now)
	_, err = r.exec(ctx, `
INSERT INTO ontology_link_types (
	id, tenant_id, tenant_scope, display_name, from_object_type, to_object_type,
	cardinality, temporal_mode, evidence_required, suggested_allowed, allowed_actions_json, version, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	tenant_id = excluded.tenant_id,
	tenant_scope = excluded.tenant_scope,
	display_name = excluded.display_name,
	from_object_type = excluded.from_object_type,
	to_object_type = excluded.to_object_type,
	cardinality = excluded.cardinality,
	temporal_mode = excluded.temporal_mode,
	evidence_required = excluded.evidence_required,
	suggested_allowed = excluded.suggested_allowed,
	allowed_actions_json = excluded.allowed_actions_json,
	version = excluded.version,
	updated_at = excluded.updated_at`,
		normalized.ID,
		normalized.TenantID,
		normalized.TenantScope,
		normalized.DisplayName,
		normalized.FromObjectType,
		normalized.ToObjectType,
		normalized.Cardinality,
		normalized.TemporalMode,
		normalized.EvidenceRequired,
		normalized.SuggestedAllowed,
		mustJSON(normalized.AllowedActions),
		normalized.Version,
		timeToUnixNano(normalized.CreatedAt),
		timeToUnixNano(normalized.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("save ontology link type: %w", err)
	}
	return nil
}

func (r *PlatformRegistry) SaveLinkTypeWithExpectedVersion(ctx context.Context, linkType LinkType, expectedVersion int) (LinkType, error) {
	if err := r.ready(); err != nil {
		return LinkType{}, err
	}
	if err := r.ensureDefinitionTenant(ctx, "ontology_link_types", normalizeID(linkType.ID), strings.TrimSpace(linkType.TenantID)); err != nil {
		return LinkType{}, err
	}
	current, err := r.GetLinkType(ctx, linkType.ID)
	if err != nil {
		return LinkType{}, err
	}
	if expectedVersion > 0 && current.Version != expectedVersion {
		return LinkType{}, fmt.Errorf("%w: link type %s expected version %d, current version %d", ErrVersionConflict, normalizeID(linkType.ID), expectedVersion, current.Version)
	}
	normalized, err := normalizeLinkType(linkType)
	if err != nil {
		return LinkType{}, err
	}
	if err := r.requireObjectType(ctx, normalized.FromObjectType); err != nil {
		return LinkType{}, err
	}
	if err := r.requireObjectType(ctx, normalized.ToObjectType); err != nil {
		return LinkType{}, err
	}
	normalized.CreatedAt = current.CreatedAt
	normalized.Version = expectedVersion + 1
	stampOntologyDefinition(&normalized.CreatedAt, &normalized.UpdatedAt, &normalized.Version, r.now)
	result, err := r.exec(ctx, `
UPDATE ontology_link_types SET
	tenant_id = ?,
	tenant_scope = ?,
	display_name = ?,
	from_object_type = ?,
	to_object_type = ?,
	cardinality = ?,
	temporal_mode = ?,
	evidence_required = ?,
	suggested_allowed = ?,
	allowed_actions_json = ?,
	version = ?,
	updated_at = ?
WHERE id = ? AND version = ?`,
		normalized.TenantID,
		normalized.TenantScope,
		normalized.DisplayName,
		normalized.FromObjectType,
		normalized.ToObjectType,
		normalized.Cardinality,
		normalized.TemporalMode,
		normalized.EvidenceRequired,
		normalized.SuggestedAllowed,
		mustJSON(normalized.AllowedActions),
		normalized.Version,
		timeToUnixNano(normalized.UpdatedAt),
		normalized.ID,
		expectedVersion,
	)
	if err != nil {
		return LinkType{}, fmt.Errorf("save ontology link type with expected version: %w", err)
	}
	if err := requireOntologyRowsAffected(result, "link type", normalized.ID, expectedVersion); err != nil {
		return LinkType{}, err
	}
	return r.GetLinkType(ctx, normalized.ID)
}

func (r *PlatformRegistry) GetLinkType(ctx context.Context, id string) (LinkType, error) {
	if err := r.ready(); err != nil {
		return LinkType{}, err
	}
	row := r.queryRow(ctx, linkTypeSelectSQL()+` WHERE id = ?`, normalizeID(id))
	linkType, err := scanLinkType(row)
	if errors.Is(err, sql.ErrNoRows) {
		return LinkType{}, fmt.Errorf("%w: link type %s", ErrNotFound, id)
	}
	return linkType, err
}

func (r *PlatformRegistry) ListLinkTypes(ctx context.Context) ([]LinkType, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	rows, err := r.query(ctx, linkTypeSelectSQL()+` ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list ontology link types: %w", err)
	}
	defer rows.Close()
	var out []LinkType
	for rows.Next() {
		linkType, err := scanLinkType(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, linkType)
	}
	return out, rows.Err()
}

func (r *PlatformRegistry) HasLinkType(ctx context.Context, id string) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	var found string
	err := r.queryRow(ctx, `SELECT id FROM ontology_link_types WHERE id = ?`, normalizeID(id)).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return found != "", nil
}

func (r *PlatformRegistry) LinkConstraint(ctx context.Context, id string) (graphprotocol.OntologyLinkConstraint, bool, error) {
	linkType, err := r.GetLinkType(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return graphprotocol.OntologyLinkConstraint{}, false, nil
	}
	if err != nil {
		return graphprotocol.OntologyLinkConstraint{}, false, err
	}
	return graphprotocol.OntologyLinkConstraint{
		FromObjectType:   linkType.FromObjectType,
		ToObjectType:     linkType.ToObjectType,
		EvidenceRequired: linkType.EvidenceRequired,
	}, true, nil
}

func (r *PlatformRegistry) PropertyConstraint(ctx context.Context, objectType string, predicate string) (graphprotocol.OntologyPropertyConstraint, bool, error) {
	row := r.queryRow(ctx, propertyTypeSelectSQL()+` WHERE object_type_id = ? AND property_id = ?`, normalizeID(objectType), normalizeID(predicate))
	propertyType, err := scanPropertyType(row)
	if errors.Is(err, sql.ErrNoRows) {
		return graphprotocol.OntologyPropertyConstraint{}, false, nil
	}
	if err != nil {
		return graphprotocol.OntologyPropertyConstraint{}, false, err
	}
	return graphprotocol.OntologyPropertyConstraint{ValueType: string(propertyType.ValueType)}, true, nil
}

func (r *PlatformRegistry) SavePropertyType(ctx context.Context, propertyType PropertyType) error {
	if err := r.ready(); err != nil {
		return err
	}
	normalized, err := normalizePropertyType(propertyType)
	if err != nil {
		return err
	}
	if err := r.ensureDefinitionTenant(ctx, "ontology_property_types", normalized.ID, normalized.TenantID); err != nil {
		return err
	}
	if err := r.requireObjectType(ctx, normalized.ObjectTypeID); err != nil {
		return err
	}
	stampOntologyDefinition(&normalized.CreatedAt, &normalized.UpdatedAt, &normalized.Version, r.now)
	_, err = r.exec(ctx, `
INSERT INTO ontology_property_types (
	id, tenant_id, object_type_id, property_id, display_name, value_type,
	temporal_mode, authority_source, conflict_policy, sensitivity, allowed_actions_json, version, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	tenant_id = excluded.tenant_id,
	object_type_id = excluded.object_type_id,
	property_id = excluded.property_id,
	display_name = excluded.display_name,
	value_type = excluded.value_type,
	temporal_mode = excluded.temporal_mode,
	authority_source = excluded.authority_source,
	conflict_policy = excluded.conflict_policy,
	sensitivity = excluded.sensitivity,
	allowed_actions_json = excluded.allowed_actions_json,
	version = excluded.version,
	updated_at = excluded.updated_at`,
		normalized.ID,
		normalized.TenantID,
		normalized.ObjectTypeID,
		normalized.PropertyID,
		normalized.DisplayName,
		normalized.ValueType,
		normalized.TemporalMode,
		normalized.AuthoritySource,
		normalized.ConflictPolicy,
		normalized.Sensitivity,
		mustJSON(normalized.AllowedActions),
		normalized.Version,
		timeToUnixNano(normalized.CreatedAt),
		timeToUnixNano(normalized.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("save ontology property type: %w", err)
	}
	return nil
}

func (r *PlatformRegistry) SavePropertyTypeWithExpectedVersion(ctx context.Context, propertyType PropertyType, expectedVersion int) (PropertyType, error) {
	if err := r.ready(); err != nil {
		return PropertyType{}, err
	}
	if err := r.ensureDefinitionTenant(ctx, "ontology_property_types", normalizeID(propertyType.ID), strings.TrimSpace(propertyType.TenantID)); err != nil {
		return PropertyType{}, err
	}
	current, err := r.GetPropertyType(ctx, propertyType.ID)
	if err != nil {
		return PropertyType{}, err
	}
	if expectedVersion > 0 && current.Version != expectedVersion {
		return PropertyType{}, fmt.Errorf("%w: property type %s expected version %d, current version %d", ErrVersionConflict, normalizeID(propertyType.ID), expectedVersion, current.Version)
	}
	normalized, err := normalizePropertyType(propertyType)
	if err != nil {
		return PropertyType{}, err
	}
	if err := r.requireObjectType(ctx, normalized.ObjectTypeID); err != nil {
		return PropertyType{}, err
	}
	normalized.CreatedAt = current.CreatedAt
	normalized.Version = expectedVersion + 1
	stampOntologyDefinition(&normalized.CreatedAt, &normalized.UpdatedAt, &normalized.Version, r.now)
	result, err := r.exec(ctx, `
UPDATE ontology_property_types SET
	tenant_id = ?,
	object_type_id = ?,
	property_id = ?,
	display_name = ?,
	value_type = ?,
	temporal_mode = ?,
	authority_source = ?,
	conflict_policy = ?,
	sensitivity = ?,
	allowed_actions_json = ?,
	version = ?,
	updated_at = ?
WHERE id = ? AND version = ?`,
		normalized.TenantID,
		normalized.ObjectTypeID,
		normalized.PropertyID,
		normalized.DisplayName,
		normalized.ValueType,
		normalized.TemporalMode,
		normalized.AuthoritySource,
		normalized.ConflictPolicy,
		normalized.Sensitivity,
		mustJSON(normalized.AllowedActions),
		normalized.Version,
		timeToUnixNano(normalized.UpdatedAt),
		normalized.ID,
		expectedVersion,
	)
	if err != nil {
		return PropertyType{}, fmt.Errorf("save ontology property type with expected version: %w", err)
	}
	if err := requireOntologyRowsAffected(result, "property type", normalized.ID, expectedVersion); err != nil {
		return PropertyType{}, err
	}
	return r.GetPropertyType(ctx, normalized.ID)
}

func (r *PlatformRegistry) GetPropertyType(ctx context.Context, id string) (PropertyType, error) {
	if err := r.ready(); err != nil {
		return PropertyType{}, err
	}
	row := r.queryRow(ctx, propertyTypeSelectSQL()+` WHERE id = ?`, normalizeID(id))
	propertyType, err := scanPropertyType(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PropertyType{}, fmt.Errorf("%w: property type %s", ErrNotFound, id)
	}
	return propertyType, err
}

func (r *PlatformRegistry) ListPropertyTypes(ctx context.Context, objectTypeID string) ([]PropertyType, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	query := propertyTypeSelectSQL()
	args := []any{}
	if id := normalizeID(objectTypeID); id != "" {
		query += ` WHERE object_type_id = ?`
		args = append(args, id)
	}
	query += ` ORDER BY id`
	rows, err := r.query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list ontology property types: %w", err)
	}
	defer rows.Close()
	var out []PropertyType
	for rows.Next() {
		propertyType, err := scanPropertyType(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, propertyType)
	}
	return out, rows.Err()
}

func (r *PlatformRegistry) SaveMappingRule(ctx context.Context, rule MappingRule) error {
	if err := r.ready(); err != nil {
		return err
	}
	normalized, err := normalizeMappingRule(rule)
	if err != nil {
		return err
	}
	if err := r.ensureDefinitionTenant(ctx, "ontology_mapping_rules", normalized.ID, normalized.TenantID); err != nil {
		return err
	}
	for _, objectType := range normalized.ObjectTypes {
		if err := r.requireObjectType(ctx, objectType); err != nil {
			return err
		}
	}
	for _, linkType := range normalized.LinkTypes {
		if err := r.requireLinkType(ctx, linkType); err != nil {
			return err
		}
	}
	if err := validateMappingRuleTemplates(normalized, mappingRuleLookup{
		objectTypeExists: func(id string) (bool, error) {
			return r.HasObjectType(ctx, id)
		},
		linkType: func(id string) (LinkType, error) {
			linkType, err := r.GetLinkType(ctx, id)
			if errors.Is(err, ErrNotFound) {
				return LinkType{}, fmt.Errorf("%w: %s", ErrUnknownLinkType, id)
			}
			return linkType, err
		},
		propertyExists: func(objectTypeID string, propertyID string) (bool, error) {
			return r.hasPropertyType(ctx, objectTypeID, propertyID)
		},
	}); err != nil {
		return err
	}
	stampOntologyDefinition(&normalized.CreatedAt, &normalized.UpdatedAt, &normalized.Version, r.now)
	_, err = r.exec(ctx, `
INSERT INTO ontology_mapping_rules (
	id, tenant_id, event_type, source_domain, source_ref, event_schema_version, rule_hash,
	object_types_json, link_types_json, rule_json,
	enabled, version, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	tenant_id = excluded.tenant_id,
	event_type = excluded.event_type,
	source_domain = excluded.source_domain,
	source_ref = excluded.source_ref,
	event_schema_version = excluded.event_schema_version,
	rule_hash = excluded.rule_hash,
	object_types_json = excluded.object_types_json,
	link_types_json = excluded.link_types_json,
	rule_json = excluded.rule_json,
	enabled = excluded.enabled,
	version = excluded.version,
	updated_at = excluded.updated_at`,
		normalized.ID,
		normalized.TenantID,
		normalized.EventType,
		normalized.SourceDomain,
		normalized.SourceRef,
		normalized.EventSchemaVersion,
		normalized.RuleHash,
		mustJSON(normalized.ObjectTypes),
		mustJSON(normalized.LinkTypes),
		mustJSON(normalized.Rule),
		normalized.Enabled,
		normalized.Version,
		timeToUnixNano(normalized.CreatedAt),
		timeToUnixNano(normalized.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("save ontology mapping rule: %w", err)
	}
	return nil
}

func (r *PlatformRegistry) SaveMappingRuleWithExpectedVersion(ctx context.Context, rule MappingRule, expectedVersion int) (MappingRule, error) {
	if err := r.ready(); err != nil {
		return MappingRule{}, err
	}
	if err := r.ensureDefinitionTenant(ctx, "ontology_mapping_rules", normalizeID(rule.ID), strings.TrimSpace(rule.TenantID)); err != nil {
		return MappingRule{}, err
	}
	current, err := r.getMappingRuleByID(ctx, rule.ID, rule.TenantID)
	if err != nil {
		return MappingRule{}, err
	}
	if expectedVersion > 0 && current.Version != expectedVersion {
		return MappingRule{}, fmt.Errorf("%w: mapping rule %s expected version %d, current version %d", ErrVersionConflict, normalizeID(rule.ID), expectedVersion, current.Version)
	}
	normalized, err := normalizeMappingRule(rule)
	if err != nil {
		return MappingRule{}, err
	}
	for _, objectType := range normalized.ObjectTypes {
		if err := r.requireObjectType(ctx, objectType); err != nil {
			return MappingRule{}, err
		}
	}
	for _, linkType := range normalized.LinkTypes {
		if err := r.requireLinkType(ctx, linkType); err != nil {
			return MappingRule{}, err
		}
	}
	normalized.CreatedAt = current.CreatedAt
	normalized.Version = expectedVersion + 1
	stampOntologyDefinition(&normalized.CreatedAt, &normalized.UpdatedAt, &normalized.Version, r.now)
	result, err := r.exec(ctx, `
UPDATE ontology_mapping_rules SET
	tenant_id = ?,
	event_type = ?,
	source_domain = ?,
	source_ref = ?,
	event_schema_version = ?,
	rule_hash = ?,
	object_types_json = ?,
	link_types_json = ?,
	rule_json = ?,
	enabled = ?,
	version = ?,
	updated_at = ?
WHERE id = ? AND version = ?`,
		normalized.TenantID,
		normalized.EventType,
		normalized.SourceDomain,
		normalized.SourceRef,
		normalized.EventSchemaVersion,
		normalized.RuleHash,
		mustJSON(normalized.ObjectTypes),
		mustJSON(normalized.LinkTypes),
		mustJSON(normalized.Rule),
		normalized.Enabled,
		normalized.Version,
		timeToUnixNano(normalized.UpdatedAt),
		normalized.ID,
		expectedVersion,
	)
	if err != nil {
		return MappingRule{}, fmt.Errorf("save ontology mapping rule with expected version: %w", err)
	}
	if err := requireOntologyRowsAffected(result, "mapping rule", normalized.ID, expectedVersion); err != nil {
		return MappingRule{}, err
	}
	return r.getMappingRuleByID(ctx, normalized.ID, normalized.TenantID)
}

func (r *PlatformRegistry) ListMappingRules(ctx context.Context, tenantID string) ([]MappingRule, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	tenantID = strings.TrimSpace(tenantID)
	query := mappingRuleSelectSQL()
	args := []any{}
	if tenantID != "" {
		query += ` WHERE tenant_id = ? OR tenant_id = ''`
		args = append(args, tenantID)
	}
	query += ` ORDER BY id`
	rows, err := r.query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list ontology mapping rules: %w", err)
	}
	defer rows.Close()
	var out []MappingRule
	for rows.Next() {
		rule, err := scanMappingRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, rows.Err()
}

func (r *PlatformRegistry) getMappingRuleByID(ctx context.Context, id string, tenantID string) (MappingRule, error) {
	tenantID = strings.TrimSpace(tenantID)
	query := mappingRuleSelectSQL() + ` WHERE id = ?`
	args := []any{normalizeID(id)}
	if tenantID != "" {
		query += ` AND (tenant_id = ? OR tenant_id = '')`
		args = append(args, tenantID)
	}
	row := r.queryRow(ctx, query, args...)
	rule, err := scanMappingRule(row)
	if errors.Is(err, sql.ErrNoRows) {
		return MappingRule{}, fmt.Errorf("%w: mapping rule %s", ErrNotFound, id)
	}
	return rule, err
}

func (r *PlatformRegistry) requireObjectType(ctx context.Context, id string) error {
	if _, err := r.GetObjectType(ctx, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return fmt.Errorf("%w: %s", ErrUnknownObjectType, id)
		}
		return err
	}
	return nil
}

func (r *PlatformRegistry) requireLinkType(ctx context.Context, id string) error {
	if _, err := r.GetLinkType(ctx, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return fmt.Errorf("%w: %s", ErrUnknownLinkType, id)
		}
		return err
	}
	return nil
}

func (r *PlatformRegistry) hasPropertyType(ctx context.Context, objectTypeID string, propertyID string) (bool, error) {
	var found string
	err := r.queryRow(ctx, `SELECT id FROM ontology_property_types WHERE object_type_id = ? AND property_id = ?`, normalizeID(objectTypeID), normalizeID(propertyID)).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return found != "", nil
}

func (r *PlatformRegistry) ensureDefinitionTenant(ctx context.Context, table string, id string, tenantID string) error {
	var existingTenant string
	err := r.queryRow(ctx, `SELECT tenant_id FROM `+table+` WHERE id = ?`, id).Scan(&existingTenant)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(existingTenant) != strings.TrimSpace(tenantID) {
		return fmt.Errorf("%w: definition %s is owned by another tenant scope", ErrInvalidDefinition, id)
	}
	return nil
}

func objectTypeSelectSQL() string {
	return `SELECT id, tenant_id, tenant_scope, display_name, canonical_key_rule, owner_domain,
sensitivity, lifecycle, temporal_mode, properties_json, allowed_link_types_json, allowed_actions_json,
version, created_at, updated_at FROM ontology_object_types`
}

func linkTypeSelectSQL() string {
	return `SELECT id, tenant_id, tenant_scope, display_name, from_object_type, to_object_type,
cardinality, temporal_mode, evidence_required, suggested_allowed, allowed_actions_json, version, created_at, updated_at FROM ontology_link_types`
}

func propertyTypeSelectSQL() string {
	return `SELECT id, tenant_id, object_type_id, property_id, display_name, value_type,
temporal_mode, authority_source, conflict_policy, sensitivity, allowed_actions_json, version, created_at, updated_at FROM ontology_property_types`
}

func mappingRuleSelectSQL() string {
	return `SELECT id, tenant_id, event_type, source_domain, source_ref, event_schema_version, rule_hash,
object_types_json, link_types_json, rule_json,
enabled, version, created_at, updated_at FROM ontology_mapping_rules`
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanObjectType(row rowScanner) (ObjectType, error) {
	var objectType ObjectType
	var propertiesJSON, allowedLinksJSON, allowedActionsJSON string
	var createdAt, updatedAt int64
	if err := row.Scan(&objectType.ID, &objectType.TenantID, &objectType.TenantScope, &objectType.DisplayName, &objectType.CanonicalKeyRule, &objectType.OwnerDomain, &objectType.Sensitivity, &objectType.Lifecycle, &objectType.TemporalMode, &propertiesJSON, &allowedLinksJSON, &allowedActionsJSON, &objectType.Version, &createdAt, &updatedAt); err != nil {
		return ObjectType{}, err
	}
	mustDecodeJSON(propertiesJSON, &objectType.Properties)
	mustDecodeJSON(allowedLinksJSON, &objectType.AllowedLinkTypes)
	mustDecodeJSON(allowedActionsJSON, &objectType.AllowedActions)
	objectType.CreatedAt = unixNanoToTime(createdAt)
	objectType.UpdatedAt = unixNanoToTime(updatedAt)
	return objectType, nil
}

func scanLinkType(row rowScanner) (LinkType, error) {
	var linkType LinkType
	var allowedActionsJSON string
	var createdAt, updatedAt int64
	if err := row.Scan(&linkType.ID, &linkType.TenantID, &linkType.TenantScope, &linkType.DisplayName, &linkType.FromObjectType, &linkType.ToObjectType, &linkType.Cardinality, &linkType.TemporalMode, &linkType.EvidenceRequired, &linkType.SuggestedAllowed, &allowedActionsJSON, &linkType.Version, &createdAt, &updatedAt); err != nil {
		return LinkType{}, err
	}
	mustDecodeJSON(allowedActionsJSON, &linkType.AllowedActions)
	linkType.CreatedAt = unixNanoToTime(createdAt)
	linkType.UpdatedAt = unixNanoToTime(updatedAt)
	return linkType, nil
}

func scanPropertyType(row rowScanner) (PropertyType, error) {
	var propertyType PropertyType
	var allowedActionsJSON string
	var createdAt, updatedAt int64
	if err := row.Scan(&propertyType.ID, &propertyType.TenantID, &propertyType.ObjectTypeID, &propertyType.PropertyID, &propertyType.DisplayName, &propertyType.ValueType, &propertyType.TemporalMode, &propertyType.AuthoritySource, &propertyType.ConflictPolicy, &propertyType.Sensitivity, &allowedActionsJSON, &propertyType.Version, &createdAt, &updatedAt); err != nil {
		return PropertyType{}, err
	}
	mustDecodeJSON(allowedActionsJSON, &propertyType.AllowedActions)
	propertyType.CreatedAt = unixNanoToTime(createdAt)
	propertyType.UpdatedAt = unixNanoToTime(updatedAt)
	return propertyType, nil
}

func scanMappingRule(row rowScanner) (MappingRule, error) {
	var rule MappingRule
	var objectTypesJSON, linkTypesJSON, ruleJSON string
	var createdAt, updatedAt int64
	if err := row.Scan(&rule.ID, &rule.TenantID, &rule.EventType, &rule.SourceDomain, &rule.SourceRef, &rule.EventSchemaVersion, &rule.RuleHash, &objectTypesJSON, &linkTypesJSON, &ruleJSON, &rule.Enabled, &rule.Version, &createdAt, &updatedAt); err != nil {
		return MappingRule{}, err
	}
	mustDecodeJSON(objectTypesJSON, &rule.ObjectTypes)
	mustDecodeJSON(linkTypesJSON, &rule.LinkTypes)
	mustDecodeJSON(ruleJSON, &rule.Rule)
	rule.CreatedAt = unixNanoToTime(createdAt)
	rule.UpdatedAt = unixNanoToTime(updatedAt)
	return rule, nil
}

func mustJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func mustDecodeJSON(raw string, dst any) {
	if strings.TrimSpace(raw) == "" {
		return
	}
	_ = json.Unmarshal([]byte(raw), dst)
}

func timeToUnixNano(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().UnixNano()
}

func unixNanoToTime(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(0, value).UTC()
}

func requireOntologyRowsAffected(result sql.Result, targetKind string, targetID string, expectedVersion int) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("%w: %s %s expected version %d", ErrVersionConflict, targetKind, targetID, expectedVersion)
	}
	return nil
}
