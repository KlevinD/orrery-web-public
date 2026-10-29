package ontology

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type RegistryDefinitionKind string

const (
	RegistryDefinitionObject   RegistryDefinitionKind = "object"
	RegistryDefinitionLink     RegistryDefinitionKind = "link"
	RegistryDefinitionProperty RegistryDefinitionKind = "property"
	RegistryDefinitionMapping  RegistryDefinitionKind = "mapping"
)

type RegistryDefinition struct {
	Kind         RegistryDefinitionKind
	ID           string
	TenantID     string
	ObjectType   *ObjectType
	LinkType     *LinkType
	PropertyType *PropertyType
	MappingRule  *MappingRule
}

type RegistryDefinitionPage struct {
	Items   []RegistryDefinition
	HasMore bool
}

type RegistryDefinitionQuery struct {
	Kind     RegistryDefinitionKind
	TenantID string
	AfterID  string
	Limit    int
}

type MappingRuleCandidatePage struct {
	Items   []MappingRule
	HasMore bool
}

func (r *PlatformRegistry) ListEnabledMappingRulesByEventType(ctx context.Context, tenantID, eventType string, limit int) (MappingRuleCandidatePage, error) {
	if err := r.ready(); err != nil {
		return MappingRuleCandidatePage{}, err
	}
	tenantID = strings.TrimSpace(tenantID)
	eventType = strings.TrimSpace(eventType)
	if tenantID == "" || len(tenantID) > 256 || eventType == "" || len(eventType) > 256 || limit <= 0 || limit > 100 {
		return MappingRuleCandidatePage{}, fmt.Errorf("%w: mapping rule candidate query is invalid", ErrInvalidDefinition)
	}
	rows, err := r.query(ctx, mappingRuleSelectSQL()+`
WHERE (tenant_id = ? OR tenant_id = '' OR tenant_id = 'system') AND event_type = ? AND enabled = ?
ORDER BY id ASC LIMIT ?`, tenantID, eventType, true, limit+1)
	if err != nil {
		return MappingRuleCandidatePage{}, fmt.Errorf("list mapping rule candidates: %w", err)
	}
	defer rows.Close()
	items := make([]MappingRule, 0, limit+1)
	for rows.Next() {
		item, err := scanMappingRule(rows)
		if err != nil {
			return MappingRuleCandidatePage{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return MappingRuleCandidatePage{}, err
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	return MappingRuleCandidatePage{Items: items, HasMore: hasMore}, nil
}

func (r *PlatformRegistry) ListRegistryDefinitions(ctx context.Context, query RegistryDefinitionQuery) (RegistryDefinitionPage, error) {
	if err := r.ready(); err != nil {
		return RegistryDefinitionPage{}, err
	}
	query.TenantID = strings.TrimSpace(query.TenantID)
	query.AfterID = strings.TrimSpace(query.AfterID)
	if query.TenantID == "" || len(query.TenantID) > 256 || len(query.AfterID) > 256 || query.Limit <= 0 || query.Limit > 1000 {
		return RegistryDefinitionPage{}, fmt.Errorf("%w: registry definition query is invalid", ErrInvalidDefinition)
	}
	selectSQL, scan, err := registryDefinitionQueryPlan(query.Kind)
	if err != nil {
		return RegistryDefinitionPage{}, err
	}
	rows, err := r.query(ctx, selectSQL+` WHERE id > ? AND (tenant_id = '' OR tenant_id = 'system' OR tenant_id = ?) ORDER BY id ASC LIMIT ?`, query.AfterID, query.TenantID, query.Limit+1)
	if err != nil {
		return RegistryDefinitionPage{}, fmt.Errorf("list %s registry definitions: %w", query.Kind, err)
	}
	defer rows.Close()
	items := make([]RegistryDefinition, 0, query.Limit+1)
	for rows.Next() {
		definition, err := scan(rows)
		if err != nil {
			return RegistryDefinitionPage{}, err
		}
		items = append(items, definition)
	}
	if err := rows.Err(); err != nil {
		return RegistryDefinitionPage{}, err
	}
	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	return RegistryDefinitionPage{Items: items, HasMore: hasMore}, nil
}

func (r *PlatformRegistry) GetRegistryDefinition(ctx context.Context, kind RegistryDefinitionKind, id string) (RegistryDefinition, error) {
	if err := r.ready(); err != nil {
		return RegistryDefinition{}, err
	}
	id = normalizeID(id)
	if id == "" || len(id) > 256 {
		return RegistryDefinition{}, fmt.Errorf("%w: definition id is invalid", ErrInvalidDefinition)
	}
	selectSQL, scan, err := registryDefinitionQueryPlan(kind)
	if err != nil {
		return RegistryDefinition{}, err
	}
	definition, err := scan(r.queryRow(ctx, selectSQL+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return RegistryDefinition{}, fmt.Errorf("%w: definition is unavailable", ErrNotFound)
	}
	return definition, err
}

func (r *PlatformRegistry) GetRegistryDefinitions(ctx context.Context, kind RegistryDefinitionKind, ids []string) ([]RegistryDefinition, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > 500 {
		return nil, fmt.Errorf("%w: too many registry definition ids", ErrInvalidDefinition)
	}
	selectSQL, scan, err := registryDefinitionQueryPlan(kind)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(ids))
	placeholders := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids))
	for _, raw := range ids {
		id := normalizeID(raw)
		if id == "" || len(id) > 256 {
			return nil, fmt.Errorf("%w: definition id is invalid", ErrInvalidDefinition)
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	rows, err := r.query(ctx, selectSQL+` WHERE id IN (`+strings.Join(placeholders, ",")+`) ORDER BY id`, args...)
	if err != nil {
		return nil, fmt.Errorf("get %s registry definitions: %w", kind, err)
	}
	defer rows.Close()
	definitions := make([]RegistryDefinition, 0, len(args))
	for rows.Next() {
		definition, err := scan(rows)
		if err != nil {
			return nil, err
		}
		definitions = append(definitions, definition)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return definitions, nil
}

type definitionScanner func(rowScanner) (RegistryDefinition, error)

func registryDefinitionQueryPlan(kind RegistryDefinitionKind) (string, definitionScanner, error) {
	switch kind {
	case RegistryDefinitionObject:
		return objectTypeSelectSQL(), func(row rowScanner) (RegistryDefinition, error) {
			value, err := scanObjectType(row)
			if err != nil {
				return RegistryDefinition{}, err
			}
			return RegistryDefinition{Kind: kind, ID: value.ID, TenantID: value.TenantID, ObjectType: &value}, nil
		}, nil
	case RegistryDefinitionLink:
		return linkTypeSelectSQL(), func(row rowScanner) (RegistryDefinition, error) {
			value, err := scanLinkType(row)
			if err != nil {
				return RegistryDefinition{}, err
			}
			return RegistryDefinition{Kind: kind, ID: value.ID, TenantID: value.TenantID, LinkType: &value}, nil
		}, nil
	case RegistryDefinitionProperty:
		return propertyTypeSelectSQL(), func(row rowScanner) (RegistryDefinition, error) {
			value, err := scanPropertyType(row)
			if err != nil {
				return RegistryDefinition{}, err
			}
			return RegistryDefinition{Kind: kind, ID: value.ID, TenantID: value.TenantID, PropertyType: &value}, nil
		}, nil
	case RegistryDefinitionMapping:
		return mappingRuleSelectSQL(), func(row rowScanner) (RegistryDefinition, error) {
			value, err := scanMappingRule(row)
			if err != nil {
				return RegistryDefinition{}, err
			}
			return RegistryDefinition{Kind: kind, ID: value.ID, TenantID: value.TenantID, MappingRule: &value}, nil
		}, nil
	default:
		return "", nil, fmt.Errorf("%w: unsupported registry definition kind %q", ErrInvalidDefinition, kind)
	}
}
