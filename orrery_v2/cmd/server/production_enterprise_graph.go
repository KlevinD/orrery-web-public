package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/contextgraph"
	contextconsumer "orrery_v2/internal/app/contextgraph/consumer"
	"orrery_v2/internal/app/datapolicy"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform/db"
)

func newProductionEnterpriseGraphConsumer(core *db.Store, visibility ...contextgraph.ObjectVisibilityProvider) *contextconsumer.Service {
	return contextconsumer.NewService(newProductionContextGraphService(core, visibility...))
}

func newProductionContextGraphService(core *db.Store, visibility ...contextgraph.ObjectVisibilityProvider) *contextgraph.Service {
	schema := newProductionEnterpriseGraphSchemaCache(productionOntologySchemaProvider{
		registry: ontology.NewPlatformRegistry(core),
		actions:  productionActionSchemaProvider{repo: actioncontract.NewPlatformRepository(core)},
	}, time.Now, ontology.NewPlatformMappingProjectionRefreshStore(core))
	serviceOptions := []contextgraph.Option{
		contextgraph.WithSchemaProvider(schema),
		contextgraph.WithPolicyProvider(datapolicy.NewPlatformStore(core)),
	}
	if len(visibility) > 0 && visibility[0] != nil {
		serviceOptions = append(serviceOptions, contextgraph.WithObjectVisibilityProvider(visibility[0]))
	}
	return contextgraph.NewService(contextgraph.NewPlatformStore(core), serviceOptions...)
}

type productionOntologySchemaProvider struct {
	registry ontology.Registry
	actions  productionActionSchemaProvider
}

func (p productionOntologySchemaProvider) SchemaDictionary(ctx context.Context, tenantID string) (contextgraph.SchemaDictionary, error) {
	if p.registry == nil {
		return contextgraph.SchemaDictionary{}, nil
	}
	snapshot, err := ontology.GetRegistrySnapshot(ctx, p.registry, tenantID)
	if err != nil {
		return contextgraph.SchemaDictionary{}, err
	}
	actions, err := p.actions.ListActionSchema(ctx, tenantID)
	if err != nil {
		return contextgraph.SchemaDictionary{}, err
	}
	dictionary := contextgraph.SchemaDictionary{
		ObjectTypes:   schemaObjectTypes(snapshot.ObjectTypes),
		LinkTypes:     schemaLinkTypes(snapshot.LinkTypes),
		PropertyTypes: schemaPropertyTypes(snapshot.PropertyTypes),
		ActionTypes:   actions,
	}
	dictionary.VersionHash = schemaDictionaryHash(dictionary)
	return dictionary, nil
}

type productionActionSchemaProvider struct {
	repo *actioncontract.PlatformRepository
}

func (p productionActionSchemaProvider) ListActionSchema(ctx context.Context, tenantID string) ([]contextgraph.SchemaActionType, error) {
	if p.repo == nil {
		return nil, nil
	}
	actionTypes, err := p.repo.ListActionTypes(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]contextgraph.SchemaActionType, 0, len(actionTypes))
	for _, actionType := range actionTypes {
		if !enterpriseGraphDefinitionVisibleToTenant(actionType.TenantID, tenantID) {
			continue
		}
		out = append(out, contextgraph.SchemaActionType{
			ID:               string(actionType.ID),
			TenantID:         actionType.TenantID,
			Name:             actionType.Name,
			Category:         string(actionType.Category),
			Description:      actionType.Description,
			ObjectTypeRefs:   append([]string(nil), actionType.ObjectTypeRefs...),
			ProviderKind:     string(actionType.ProviderKind),
			ProviderRef:      actionType.ProviderRef,
			ResourceResolver: actionType.ResourceResolver,
			PermissionScope:  actionType.PermissionScope,
			FieldProfile:     actionType.FieldProfile,
			DataPolicy:       actionType.DataPolicy,
			PurposePolicy:    actionType.PurposePolicy,
			RiskLevel:        string(actionType.RiskLevel),
			DryRunSupported:  actionType.DryRunSupported,
			Verbs:            append([]string(nil), actionType.Semantics.Verbs...),
			Roles:            schemaActionRoles(actionType.Semantics.Roles),
			Preconditions:    append([]string(nil), actionType.Semantics.Preconditions...),
			Effects:          append([]string(nil), actionType.Semantics.Effects...),
			DecomposesInto:   actionTypeIDsToStrings(actionType.Semantics.DecomposesInto),
			KnowledgeTerms:   append([]string(nil), actionType.Semantics.KnowledgeTerms...),
			MemoryTerms:      append([]string(nil), actionType.Semantics.MemoryTerms...),
			Enabled:          actionType.Enabled,
			Version:          actionType.Version,
		})
	}
	return out, nil
}

func schemaActionRoles(values []actioncontract.SemanticRole) []contextgraph.SchemaActionRole {
	out := make([]contextgraph.SchemaActionRole, 0, len(values))
	for _, value := range values {
		out = append(out, contextgraph.SchemaActionRole{
			Name: value.Name, ObjectType: value.ObjectType, Required: value.Required,
		})
	}
	return out
}

func actionTypeIDsToStrings(values []actioncontract.ActionTypeID) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, string(value))
	}
	return out
}

func schemaObjectTypes(values []ontology.ObjectType) []contextgraph.SchemaObjectType {
	out := make([]contextgraph.SchemaObjectType, 0, len(values))
	for _, value := range values {
		out = append(out, contextgraph.SchemaObjectType{
			ID:               value.ID,
			TenantID:         value.TenantID,
			DisplayName:      value.DisplayName,
			CanonicalKeyRule: value.CanonicalKeyRule,
			OwnerDomain:      value.OwnerDomain,
			TemporalMode:     string(value.TemporalMode),
			Sensitivity:      value.Sensitivity,
			AllowedLinkTypes: append([]string(nil), value.AllowedLinkTypes...),
			AllowedActions:   append([]string(nil), value.AllowedActions...),
		})
	}
	return out
}

func schemaLinkTypes(values []ontology.LinkType) []contextgraph.SchemaLinkType {
	out := make([]contextgraph.SchemaLinkType, 0, len(values))
	for _, value := range values {
		out = append(out, contextgraph.SchemaLinkType{
			ID:               value.ID,
			TenantID:         value.TenantID,
			DisplayName:      value.DisplayName,
			FromObjectType:   value.FromObjectType,
			ToObjectType:     value.ToObjectType,
			Cardinality:      string(value.Cardinality),
			TemporalMode:     string(value.TemporalMode),
			EvidenceRequired: value.EvidenceRequired,
			AllowedActions:   append([]string(nil), value.AllowedActions...),
		})
	}
	return out
}

func schemaPropertyTypes(values []ontology.PropertyType) []contextgraph.SchemaPropertyType {
	out := make([]contextgraph.SchemaPropertyType, 0, len(values))
	for _, value := range values {
		out = append(out, contextgraph.SchemaPropertyType{
			ID:              value.ID,
			TenantID:        value.TenantID,
			ObjectTypeID:    value.ObjectTypeID,
			PropertyID:      value.PropertyID,
			DisplayName:     value.DisplayName,
			ValueType:       string(value.ValueType),
			TemporalMode:    string(value.TemporalMode),
			AuthoritySource: value.AuthoritySource,
			ConflictPolicy:  string(value.ConflictPolicy),
			Sensitivity:     value.Sensitivity,
			AllowedActions:  append([]string(nil), value.AllowedActions...),
		})
	}
	return out
}

func schemaDictionaryHash(dictionary contextgraph.SchemaDictionary) string {
	dictionary.VersionHash = ""
	payload, err := json.Marshal(dictionary)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func enterpriseGraphDefinitionVisibleToTenant(definitionTenantID string, requestedTenantID string) bool {
	definitionTenantID = strings.TrimSpace(definitionTenantID)
	requestedTenantID = strings.TrimSpace(requestedTenantID)
	return requestedTenantID == "" ||
		definitionTenantID == "" ||
		definitionTenantID == "system" ||
		definitionTenantID == requestedTenantID
}
