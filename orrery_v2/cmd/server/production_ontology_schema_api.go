package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datapolicy"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontology"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	ontologyschema "orrery_v2/internal/app/ontologyview/schema"
	"orrery_v2/internal/platform/db"
)

type productionSchemaDefinitionPolicy struct{}

type productionSchemaDataPolicy struct {
	provider  contextgraph.PolicyProvider
	revisions datavisibility.PolicyRevisionSource
}

var productionSchemaSafeFields = map[string]map[string]struct{}{
	"object": safeSchemaFields(
		"label", "owner_domain", "version", "status", "updated_at",
		"attributes.tenant_scope", "attributes.canonical_key_rule", "attributes.sensitivity",
		"attributes.lifecycle", "attributes.properties",
		"references.allows_link", "references.allows_action",
	),
	"link": safeSchemaFields(
		"label", "owner_domain", "version", "status", "updated_at",
		"attributes.from_object_type", "attributes.to_object_type", "attributes.cardinality",
		"attributes.evidence_required", "attributes.suggested_allowed",
		"references.from", "references.to",
	),
	"property": safeSchemaFields(
		"label", "owner_domain", "version", "status", "updated_at",
		"attributes.object_type_id", "attributes.property_id", "attributes.value_type",
		"attributes.authority_source", "attributes.conflict_policy", "attributes.sensitivity",
		"references.property_of",
	),
	"mapping": safeSchemaFields(
		"label", "owner_domain", "version", "status", "updated_at",
		"attributes.event_type", "attributes.source_domain", "attributes.source_ref",
		"attributes.event_schema_version", "attributes.rule_hash", "attributes.enabled",
		"references.projects_object", "references.projects_link",
	),
	"action": safeSchemaFields(
		"label", "owner_domain", "version", "status", "updated_at",
		"attributes.category", "attributes.description", "attributes.provider_kind",
		"attributes.provider_ref", "attributes.permission_scope", "attributes.risk_level",
		"attributes.dry_run_supported",
		"references.applies_to",
	),
}

func (productionSchemaDefinitionPolicy) Evaluate(_ context.Context, req datavisibility.AdapterRequest) (datavisibility.AdapterDecision, error) {
	decision := datavisibility.AdapterDecision{Coverage: datavisibility.CoverageAll, PolicyRevision: "schema-safe-fields-v1"}
	for _, resource := range req.Resources {
		allowedSet := productionSchemaSafeFields[resource.Type]
		allowed := make([]string, 0, len(resource.FieldPaths))
		masked := make([]string, 0)
		for _, field := range resource.FieldPaths {
			if _, safe := allowedSet[field]; safe {
				allowed = append(allowed, field)
			} else {
				masked = append(masked, field)
			}
		}
		decision.Resources = append(decision.Resources, datavisibility.AdapterResourceDecision{
			Key: resource.Key, AllowedFieldPaths: allowed, MaskedFieldPaths: masked, ExistenceDisclosure: datavisibility.ExistenceVisible,
		})
	}
	return decision, nil
}

func safeSchemaFields(fields ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		set[field] = struct{}{}
	}
	return set
}

func (p productionSchemaDataPolicy) Evaluate(ctx context.Context, req datavisibility.AdapterRequest) (datavisibility.AdapterDecision, error) {
	if p.provider == nil || p.revisions == nil {
		return datavisibility.AdapterDecision{}, datavisibility.ErrDependencyUnavailable
	}
	principal := identity.NormalizePrincipal(req.Principal)
	beforeRevision, err := p.revisions.Revision(ctx, principal)
	if err != nil {
		return datavisibility.AdapterDecision{}, err
	}
	beforeRevision = strings.TrimSpace(beforeRevision)
	type tenantResources struct {
		objects   []contextgraph.Object
		resources []datavisibility.ResourceDescriptor
	}
	byTenant := map[string]*tenantResources{}
	for _, resource := range req.Resources {
		batch := byTenant[resource.TenantID]
		if batch == nil {
			batch = &tenantResources{}
			byTenant[resource.TenantID] = batch
		}
		batch.objects = append(batch.objects, contextgraph.Object{ID: resource.Key, TenantID: resource.TenantID, ObjectType: resource.Type})
		batch.resources = append(batch.resources, resource)
	}
	tenants := make([]string, 0, len(byTenant))
	for tenantID := range byTenant {
		tenants = append(tenants, tenantID)
	}
	sort.Strings(tenants)
	actor := schemaPolicyActor(req.Principal)
	decisions := map[string]datavisibility.AdapterResourceDecision{}
	for _, tenantID := range tenants {
		batch := byTenant[tenantID]
		policy, err := p.provider.DecideContext(ctx, contextgraph.BuildContextBundleRequest{
			TenantID: tenantID, Consumer: "ontology_workbench", Purpose: req.Purpose, ActorRef: actor,
		}, batch.objects)
		if err != nil {
			return datavisibility.AdapterDecision{}, err
		}
		hidden := stringSet(policy.HiddenObjectIDs)
		for _, resource := range batch.resources {
			_, conceal := hidden[resource.Key]
			masked := requestedMaskedFields(resource.FieldPaths, policy.MaskedFields[resource.Key])
			item := datavisibility.AdapterResourceDecision{
				Key: resource.Key, Hidden: conceal, ExistenceDisclosure: datavisibility.ExistenceVisible,
				AllowedFieldPaths: subtractSchemaFields(resource.FieldPaths, masked), MaskedFieldPaths: masked,
			}
			if conceal {
				item.ExistenceDisclosure = datavisibility.ExistenceConceal
				item.AllowedFieldPaths = nil
				item.MaskedFieldPaths = nil
			}
			decisions[resource.Key] = item
		}
	}
	afterRevision, err := p.revisions.Revision(ctx, principal)
	if err != nil {
		return datavisibility.AdapterDecision{}, err
	}
	if beforeRevision == "" || beforeRevision != strings.TrimSpace(afterRevision) {
		return datavisibility.AdapterDecision{}, fmt.Errorf("%w: schema policy revision changed during visibility evaluation", datavisibility.ErrDependencyUnavailable)
	}
	out := datavisibility.AdapterDecision{Coverage: datavisibility.CoverageFilter | datavisibility.CoverageFields, PolicyRevision: beforeRevision}
	for _, resource := range req.Resources {
		out.Resources = append(out.Resources, decisions[resource.Key])
	}
	return out, nil
}

func schemaPolicyActor(principal identity.Principal) contextgraph.Ref {
	principal = identity.NormalizePrincipal(principal)
	id := principal.UserID
	if principal.ViewerKind == identity.ViewerKindAgent && principal.AgentProfileID != "" {
		id = principal.AgentProfileID
	}
	if id == "" {
		id = principal.Subject
	}
	return contextgraph.Ref{Kind: principal.ViewerKind, ID: id}
}

func requestedMaskedFields(requested, masked []string) []string {
	maskedSet := stringSet(masked)
	out := make([]string, 0)
	for _, field := range requested {
		if _, exists := maskedSet[field]; exists {
			out = append(out, field)
		}
	}
	sort.Strings(out)
	return out
}

func subtractSchemaFields(fields, masked []string) []string {
	maskedSet := stringSet(masked)
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if _, exists := maskedSet[field]; !exists {
			out = append(out, field)
		}
	}
	sort.Strings(out)
	return out
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[strings.TrimSpace(value)] = struct{}{}
	}
	return set
}

func newProductionOntologySchemaAPI(core *db.Store, scopes *productionOntologyScopeState, cursors *productionOntologyCursorState, concurrency *ontologycontract.ConcurrencyLimiter, policy *datapolicy.PlatformStore) (*ontologyschema.Service, error) {
	if core == nil || scopes == nil || scopes.Gate == nil || cursors == nil || cursors.Stateful == nil || concurrency == nil || policy == nil {
		return nil, fmt.Errorf("ontology schema API dependencies are required")
	}
	visibility := datavisibility.NewService(
		datavisibility.Register(datavisibility.ResourceDefinition, productionSchemaDefinitionPolicy{}),
		datavisibility.Register(datavisibility.ResourceDefinition, productionSchemaDataPolicy{provider: policy, revisions: authorization.NewPolicyRevisionSource(policy)}),
	)
	return ontologyschema.NewService(
		scopes.Gate,
		ontology.NewPlatformRegistry(core),
		actioncontract.NewPlatformRepository(core),
		visibility,
		ontologyschema.CursorSet{Encrypted: cursors.Encrypted, Stateful: cursors.Stateful},
		nil,
		concurrency,
	)
}
