package schema

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/ontologyscope"
)

type rawPage struct {
	items     []rawDefinition
	nextAfter string
	scanned   int
	hasMore   bool
}

type schemaPolicyState struct {
	decisionRevision string
	cursorRevision   string
	cacheable        bool
}

type rawReference struct {
	kind              DefinitionKind
	id, key, relation string
}

type referenceRow struct {
	item      DefinitionReference
	sourceKey string
	relation  string
	targetKey string
	direction string
}

type rawDefinition struct {
	kind                                      DefinitionKind
	id, tenantID, label, ownerDomain, version string
	status                                    DefinitionStatus
	updatedAt                                 time.Time
	attributes                                DefinitionAttributes
	referenceList                             []rawReference
	eventType                                 string
}

func (r rawDefinition) key() string { return definitionKey(r.kind, r.id) }

func (r rawDefinition) origin() DefinitionOrigin {
	if r.tenantID == "" || r.tenantID == "system" {
		return OriginSystem
	}
	return OriginTenant
}

func (r rawDefinition) listItem() DefinitionItem {
	item := DefinitionItem{Kind: r.kind, ID: r.id, Label: r.label, Origin: r.origin(), OwnerDomain: r.ownerDomain, Version: r.version, Status: r.status}
	if !r.updatedAt.IsZero() {
		value := r.updatedAt.UTC()
		item.UpdatedAt = &value
	}
	return item
}

func (r rawDefinition) detail() DefinitionDetail {
	return DefinitionDetail{DefinitionItem: r.listItem(), Attributes: cloneAttributes(r.attributes)}
}

func (r rawDefinition) references() []rawReference {
	return append([]rawReference(nil), r.referenceList...)
}

func (s *Service) readPage(ctx context.Context, tenantID string, kind DefinitionKind, after string, limit int) (rawPage, error) {
	if kind == KindAction {
		page, err := s.actions.ListActionTypePage(ctx, tenantID, after, limit)
		if err != nil {
			return rawPage{}, err
		}
		items := make([]rawDefinition, 0, len(page.Items))
		for _, value := range page.Items {
			items = append(items, rawFromAction(value))
		}
		next := after
		if len(items) > 0 {
			next = items[len(items)-1].id
		}
		scanned := len(items)
		if page.HasMore {
			scanned++
		}
		return rawPage{items: items, nextAfter: next, scanned: scanned, hasMore: page.HasMore}, nil
	}
	page, err := s.registry.ListRegistryDefinitions(ctx, ontology.RegistryDefinitionQuery{Kind: ontologyKind(kind), TenantID: tenantID, AfterID: after, Limit: limit})
	if err != nil {
		return rawPage{}, err
	}
	items := make([]rawDefinition, 0, len(page.Items))
	for _, value := range page.Items {
		items = append(items, rawFromRegistry(value))
	}
	next := after
	if len(items) > 0 {
		next = items[len(items)-1].id
	}
	scanned := len(items)
	if page.HasMore {
		scanned++
	}
	return rawPage{items: items, nextAfter: next, scanned: scanned, hasMore: page.HasMore}, nil
}

func (s *Service) readOne(ctx context.Context, tenantID string, kind DefinitionKind, id string) (rawDefinition, error) {
	if kind == KindAction {
		value, err := s.actions.GetActionType(ctx, actioncontract.ActionTypeID(id))
		if err != nil {
			return rawDefinition{}, err
		}
		row := rawFromAction(value)
		if !definitionTenantVisible(row.tenantID, tenantID) {
			return rawDefinition{}, ErrUnavailable
		}
		return row, nil
	}
	value, err := s.registry.GetRegistryDefinition(ctx, ontologyKind(kind), id)
	if err != nil {
		return rawDefinition{}, err
	}
	row := rawFromRegistry(value)
	if !definitionTenantVisible(row.tenantID, tenantID) {
		return rawDefinition{}, ErrUnavailable
	}
	return row, nil
}

func (s *Service) readMany(ctx context.Context, tenantID string, kind DefinitionKind, ids []string) ([]rawDefinition, error) {
	if len(ids) == 0 || kind == KindAction {
		return nil, nil
	}
	values, err := s.registry.GetRegistryDefinitions(ctx, ontologyKind(kind), ids)
	if err != nil {
		return nil, err
	}
	rows := make([]rawDefinition, 0, len(values))
	for _, value := range values {
		row := rawFromRegistry(value)
		if definitionTenantVisible(row.tenantID, tenantID) {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func (s *Service) visibleRows(ctx context.Context, principal identity.Principal, tenantID string, state schemaPolicyState, rows []rawDefinition) ([]rawDefinition, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	descriptors := make([]datavisibility.ResourceDescriptor, 0, len(rows))
	for _, row := range rows {
		fields := []string{"label", "owner_domain", "version", "status", "updated_at"}
		fields = append(fields, attributeFieldPaths(row.kind)...)
		fields = append(fields, referenceFieldPaths(row.references())...)
		sort.Strings(fields)
		descriptors = append(descriptors, datavisibility.ResourceDescriptor{
			Key:        row.key(),
			Kind:       datavisibility.ResourceDefinition,
			TenantID:   definitionPolicyTenant(row.tenantID),
			Type:       string(row.kind),
			FieldPaths: fields,
		})
	}
	decision, err := s.visibility.Evaluate(ctx, datavisibility.EvaluateRequest{Principal: principal, Purpose: "ontology.schema.read", Resources: descriptors})
	if err != nil {
		return nil, dependencyError(err)
	}
	if strings.TrimSpace(decision.PolicyRevision) != state.decisionRevision {
		return nil, dependencyError(errors.New("schema visibility revision changed during query"))
	}
	out := make([]rawDefinition, 0, len(rows))
	for _, row := range rows {
		resourceDecision, ok := decision.Resource(row.key())
		if !ok {
			return nil, ontologyscope.ErrDependencyUnavailable
		}
		if resourceDecision.Hidden || fieldIsMasked(resourceDecision, "status") || !definitionTenantVisible(row.tenantID, tenantID) {
			continue
		}
		out = append(out, applyFieldDecision(row, resourceDecision))
	}
	return out, nil
}

func applyFieldDecision(row rawDefinition, decision datavisibility.ResourceDecision) rawDefinition {
	masked := map[string]bool{}
	for _, field := range decision.Fields.Masked {
		masked[field] = true
	}
	if masked["label"] {
		row.label = row.id
	}
	if masked["owner_domain"] {
		row.ownerDomain = ""
	}
	if masked["version"] {
		row.version = ""
	}
	if masked["status"] {
		row.status = ""
	}
	if masked["updated_at"] {
		row.updatedAt = time.Time{}
	}
	for field := range masked {
		if strings.HasPrefix(field, "attributes.") {
			attribute := strings.TrimPrefix(field, "attributes.")
			maskDefinitionAttribute(&row.attributes, attribute)
			if attribute == "event_type" {
				row.eventType = ""
			}
		}
		if strings.HasPrefix(field, "references.") {
			row.referenceList = removeReferencesByRelation(row.referenceList, strings.TrimPrefix(field, "references."))
		}
	}
	return row
}

func rawFromRegistry(value ontology.RegistryDefinition) rawDefinition {
	row := rawDefinition{kind: DefinitionKind(value.Kind), id: value.ID, tenantID: value.TenantID, status: StatusActive}
	if v := value.ObjectType; v != nil {
		row.label = first(v.DisplayName, v.ID)
		row.ownerDomain = v.OwnerDomain
		row.version = strconv.Itoa(v.Version)
		row.updatedAt = v.UpdatedAt
		row.attributes = DefinitionAttributes{
			TenantScope: string(v.TenantScope), CanonicalKeyRule: v.CanonicalKeyRule,
			Sensitivity: v.Sensitivity, Lifecycle: v.Lifecycle, TemporalMode: string(v.TemporalMode), Properties: definitionProperties(v.Properties),
		}
		for _, id := range v.AllowedLinkTypes {
			row.referenceList = append(row.referenceList, newRef(KindLink, id, "allows_link"))
		}
		for _, id := range v.AllowedActions {
			row.referenceList = append(row.referenceList, newRef(KindAction, id, "allows_action"))
		}
	}
	if v := value.LinkType; v != nil {
		row.label = first(v.DisplayName, v.ID)
		row.version = strconv.Itoa(v.Version)
		row.updatedAt = v.UpdatedAt
		row.attributes = DefinitionAttributes{
			FromObjectType: v.FromObjectType, ToObjectType: v.ToObjectType, Cardinality: string(v.Cardinality),
			TemporalMode: string(v.TemporalMode), EvidenceRequired: boolValue(v.EvidenceRequired), SuggestedAllowed: boolValue(v.SuggestedAllowed),
		}
		row.referenceList = []rawReference{newRef(KindObject, v.FromObjectType, "from"), newRef(KindObject, v.ToObjectType, "to")}
	}
	if v := value.PropertyType; v != nil {
		row.label = first(v.DisplayName, v.ID)
		row.version = strconv.Itoa(v.Version)
		row.updatedAt = v.UpdatedAt
		row.attributes = DefinitionAttributes{
			ObjectTypeID: v.ObjectTypeID, PropertyID: v.PropertyID, ValueType: string(v.ValueType),
			TemporalMode: string(v.TemporalMode), AuthoritySource: v.AuthoritySource, ConflictPolicy: string(v.ConflictPolicy), Sensitivity: v.Sensitivity,
		}
		row.referenceList = []rawReference{newRef(KindObject, v.ObjectTypeID, "property_of")}
	}
	if v := value.MappingRule; v != nil {
		row.label = v.ID
		row.ownerDomain = v.SourceDomain
		row.version = strconv.Itoa(v.Version)
		row.updatedAt = v.UpdatedAt
		row.eventType = v.EventType
		if !v.Enabled {
			row.status = StatusDisabled
		}
		row.attributes = DefinitionAttributes{
			EventType: v.EventType, SourceDomain: v.SourceDomain, SourceRef: v.SourceRef,
			EventSchemaVersion: v.EventSchemaVersion, RuleHash: v.RuleHash, Enabled: boolValue(v.Enabled),
		}
		for _, id := range v.ObjectTypes {
			row.referenceList = append(row.referenceList, newRef(KindObject, id, "projects_object"))
		}
		for _, id := range v.LinkTypes {
			row.referenceList = append(row.referenceList, newRef(KindLink, id, "projects_link"))
		}
	}
	return row
}

func rawFromAction(value actioncontract.ActionType) rawDefinition {
	row := rawDefinition{
		kind: KindAction, id: string(value.ID), tenantID: value.TenantID, label: first(value.Name, string(value.ID)),
		ownerDomain: string(value.ProviderKind), version: value.Version, status: StatusActive, updatedAt: value.UpdatedAt,
		attributes: DefinitionAttributes{
			Category: string(value.Category), Description: value.Description, ProviderKind: string(value.ProviderKind),
			ProviderRef: value.ProviderRef, PermissionScope: value.PermissionScope, RiskLevel: string(value.RiskLevel),
			DryRunSupported: boolValue(value.DryRunSupported),
		},
	}
	if !value.Enabled {
		row.status = StatusDisabled
	}
	for _, id := range value.ObjectTypeRefs {
		row.referenceList = append(row.referenceList, newRef(KindObject, id, "applies_to"))
	}
	return row
}

func newRef(kind DefinitionKind, id, relation string) rawReference {
	return rawReference{kind: kind, id: id, key: definitionKey(kind, id), relation: relation}
}

func definitionKey(kind DefinitionKind, id string) string {
	return string(kind) + ":" + strings.TrimSpace(id)
}

func ontologyKind(kind DefinitionKind) ontology.RegistryDefinitionKind {
	return ontology.RegistryDefinitionKind(kind)
}

func definitionTenantVisible(definitionTenant, activeTenant string) bool {
	definitionTenant = strings.TrimSpace(definitionTenant)
	activeTenant = strings.TrimSpace(activeTenant)
	return activeTenant != "" && (definitionTenant == "" || definitionTenant == "system" || definitionTenant == activeTenant)
}

func definitionPolicyTenant(definitionTenant string) string {
	definitionTenant = strings.TrimSpace(definitionTenant)
	if definitionTenant == "" || definitionTenant == "system" {
		return "system"
	}
	return definitionTenant
}

func cloneAttributes(in DefinitionAttributes) DefinitionAttributes {
	out := in
	out.Properties = append([]DefinitionProperty(nil), in.Properties...)
	if in.EvidenceRequired != nil {
		out.EvidenceRequired = boolValue(*in.EvidenceRequired)
	}
	if in.SuggestedAllowed != nil {
		out.SuggestedAllowed = boolValue(*in.SuggestedAllowed)
	}
	if in.Enabled != nil {
		out.Enabled = boolValue(*in.Enabled)
	}
	if in.DryRunSupported != nil {
		out.DryRunSupported = boolValue(*in.DryRunSupported)
	}
	return out
}

func boolValue(value bool) *bool {
	return &value
}

func definitionProperties(values []ontology.PropertyDefinition) []DefinitionProperty {
	properties := make([]DefinitionProperty, 0, len(values))
	for _, value := range values {
		properties = append(properties, DefinitionProperty{
			PropertyID: value.PropertyID, DisplayName: value.DisplayName, ValueType: string(value.ValueType),
			TemporalMode: string(value.TemporalMode), Required: value.Required, Sensitivity: value.Sensitivity,
		})
	}
	return properties
}

func attributeFieldPaths(kind DefinitionKind) []string {
	var names []string
	switch kind {
	case KindObject:
		names = []string{"tenant_scope", "canonical_key_rule", "sensitivity", "lifecycle", "temporal_mode", "properties"}
	case KindLink:
		names = []string{"from_object_type", "to_object_type", "cardinality", "temporal_mode", "evidence_required", "suggested_allowed"}
	case KindProperty:
		names = []string{"object_type_id", "property_id", "value_type", "temporal_mode", "authority_source", "conflict_policy", "sensitivity"}
	case KindMapping:
		names = []string{"event_type", "source_domain", "source_ref", "event_schema_version", "rule_hash", "enabled"}
	case KindAction:
		names = []string{"category", "description", "provider_kind", "provider_ref", "permission_scope", "risk_level", "dry_run_supported"}
	}
	paths := make([]string, len(names))
	for index, name := range names {
		paths[index] = "attributes." + name
	}
	return paths
}

func referenceFieldPaths(references []rawReference) []string {
	seen := map[string]struct{}{}
	paths := make([]string, 0)
	for _, reference := range references {
		path := "references." + reference.relation
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}

func removeReferencesByRelation(references []rawReference, relation string) []rawReference {
	out := make([]rawReference, 0, len(references))
	for _, reference := range references {
		if reference.relation != relation {
			out = append(out, reference)
		}
	}
	return out
}

func fieldIsMasked(decision datavisibility.ResourceDecision, field string) bool {
	for _, masked := range decision.Fields.Masked {
		if masked == field {
			return true
		}
	}
	return false
}

func maskDefinitionAttribute(attributes *DefinitionAttributes, field string) {
	if attributes == nil {
		return
	}
	switch field {
	case "tenant_scope":
		attributes.TenantScope = ""
	case "canonical_key_rule":
		attributes.CanonicalKeyRule = ""
	case "sensitivity":
		attributes.Sensitivity = ""
	case "lifecycle":
		attributes.Lifecycle = ""
	case "temporal_mode":
		attributes.TemporalMode = ""
	case "properties":
		attributes.Properties = nil
	case "from_object_type":
		attributes.FromObjectType = ""
	case "to_object_type":
		attributes.ToObjectType = ""
	case "cardinality":
		attributes.Cardinality = ""
	case "evidence_required":
		attributes.EvidenceRequired = nil
	case "suggested_allowed":
		attributes.SuggestedAllowed = nil
	case "object_type_id":
		attributes.ObjectTypeID = ""
	case "property_id":
		attributes.PropertyID = ""
	case "value_type":
		attributes.ValueType = ""
	case "authority_source":
		attributes.AuthoritySource = ""
	case "conflict_policy":
		attributes.ConflictPolicy = ""
	case "event_type":
		attributes.EventType = ""
	case "source_domain":
		attributes.SourceDomain = ""
	case "source_ref":
		attributes.SourceRef = ""
	case "event_schema_version":
		attributes.EventSchemaVersion = ""
	case "rule_hash":
		attributes.RuleHash = ""
	case "enabled":
		attributes.Enabled = nil
	case "category":
		attributes.Category = ""
	case "description":
		attributes.Description = ""
	case "provider_kind":
		attributes.ProviderKind = ""
	case "provider_ref":
		attributes.ProviderRef = ""
	case "permission_scope":
		attributes.PermissionScope = ""
	case "risk_level":
		attributes.RiskLevel = ""
	case "dry_run_supported":
		attributes.DryRunSupported = nil
	}
}

func first(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
