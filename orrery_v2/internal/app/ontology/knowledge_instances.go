package ontology

import "context"

// Upgrade only host-owned identity metadata and assertion time modes.
// Customized values, actions and tenant definitions survive.
func upgradeKnowledgeEntityIdentityProperty(ctx context.Context, registry Registry, current, desired ObjectType) error {
	if current.TenantID != "" || current.ID != desired.ID || current.OwnerDomain != desired.OwnerDomain {
		return nil
	}
	current.Properties = append(current.Properties[:0:0], current.Properties...)
	changed := false
	for _, wanted := range desired.Properties {
		found := false
		for i, p := range current.Properties {
			if p.PropertyID != wanted.PropertyID {
				continue
			}
			found = true
			if (p.TemporalMode == "" || p.TemporalMode == TemporalModeStatic) && wanted.TemporalMode != TemporalModeStatic {
				current.Properties[i].TemporalMode = wanted.TemporalMode
				changed = true
			}
		}
		if !found && wanted.PropertyID == "identity_keys" {
			current.Properties = append(current.Properties, wanted)
			changed = true
		}
	}
	if changed {
		if versioned, ok := registry.(versionedDefaultObjectTypeRegistry); ok {
			_, err := versioned.SaveObjectTypeWithExpectedVersion(ctx, current, current.Version)
			return err
		}
	}
	return nil
}

func knowledgeEntityObjectType() ObjectType {
	o := objectType("knowledge_entity", "Knowledge entity", "knowledge_entity:{id}", "knowledge_instances",
		property("name", ValueTypeString, false), property("entity_kind", ValueTypeString, false),
		property("aliases", ValueTypeArray, false), property("description", ValueTypeString, false),
		property("country", ValueTypeString, false), property("registration_id", ValueTypeString, false),
		property("effective_date", ValueTypeDate, false), property("expiration_date", ValueTypeDate, false),
		property("amount", ValueTypeNumber, false), property("currency", ValueTypeString, false),
		property("role", ValueTypeString, false), property("identity_keys", ValueTypeArray, false), property("conflicting_properties", ValueTypeArray, false))
	o.AllowedActions = []string{"ontology.knowledge_instances.apply"}
	o.AllowedLinkTypes = []string{"knowledge_document_describes_entity", "knowledge_entity_related_to"}
	for i := range o.Properties {
		if o.Properties[i].PropertyID != "identity_keys" && o.Properties[i].PropertyID != "conflicting_properties" {
			o.Properties[i].TemporalMode = TemporalModeStateInterval
		}
	}
	return o
}

func knowledgeEntityLinkTypes() []LinkType {
	links := []LinkType{
		linkType("knowledge_document_describes_entity", "Document describes knowledge entity", "knowledge_document", "knowledge_entity", CardinalityManyToMany, true),
		linkType("knowledge_entity_related_to", "Knowledge entity relation", "knowledge_entity", "knowledge_entity", CardinalityManyToMany, true),
	}
	for i := range links {
		links[i].TemporalMode = TemporalModeStateInterval
		links[i].SuggestedAllowed = true
		links[i].AllowedActions = []string{"ontology.knowledge_instances.apply"}
	}
	return links
}
