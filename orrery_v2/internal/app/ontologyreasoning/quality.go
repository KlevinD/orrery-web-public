package ontologyreasoning

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"orrery_v2/internal/app/ontology"
)

// QualityCheck applies the same registered types used by candidate validation.
// It validates structure and values; it makes no OWL consistency claim.
func QualityCheck(pack ContextPack, candidates []Candidate) []ValidationResult {
	index, err := indexFromContextPack(pack)
	if err != nil {
		return []ValidationResult{{Status: ValidationRejected, Issues: []ValidationIssue{newIssue("", "invalid_context", issueSeverityError, err.Error())}}}
	}
	service := &Service{}
	results := make([]ValidationResult, 0, len(candidates))
	for _, c := range candidates {
		issues := service.validateCandidate(c.ID, c, index)
		issues = append(issues, entityPropertyIssues(c, index)...)
		results = append(results, ValidationResult{CandidateID: c.ID, Status: validationStatusForIssues(issues), Issues: issues})
	}
	return results
}

func entityPropertyIssues(c Candidate, index validationIndex) []ValidationIssue {
	if c.Kind != CandidateEntity {
		return nil
	}
	var issues []ValidationIssue
	typeDef := index.objectTypes[c.ObjectType]
	for _, p := range typeDef.Properties {
		value, present := c.Properties[p.PropertyID]
		if p.Required && (!present || value == nil) {
			issues = append(issues, newIssue(c.ID, "missing_required_property", issueSeverityError, "required property is missing: "+p.PropertyID))
		}
		if present && !valueMatchesType(value, p.ValueType) {
			issues = append(issues, newIssue(c.ID, "entity_property_type_mismatch", issueSeverityError, "property type mismatch: "+p.PropertyID))
		}
	}
	for key, value := range c.Properties {
		p, ok := index.propertyTypes[c.ObjectType+"."+key]
		if !ok {
			issues = append(issues, newIssue(c.ID, "unknown_property_type", issueSeverityError, "entity property is not registered: "+key))
		} else if !valueMatchesType(value, p.ValueType) {
			issues = append(issues, newIssue(c.ID, "entity_property_type_mismatch", issueSeverityError, "property type mismatch: "+key))
		}
	}
	return issues
}

type SchemaExport struct {
	TenantID      string                  `json:"tenant_id"`
	OWL           string                  `json:"owl_turtle"`
	SHACL         string                  `json:"shacl_turtle"`
	ObjectTypes   []ontology.ObjectType   `json:"object_types"`
	LinkTypes     []ontology.LinkType     `json:"link_types"`
	PropertyTypes []ontology.PropertyType `json:"property_types"`
	Limitations   []string                `json:"limitations"`
}

// ExportSchema exports a scoped Registry snapshot. The JSON sidecar retains
// Orrery-specific governance fields that have no lossless OWL/SHACL equivalent.
func ExportSchema(pack ContextPack) (SchemaExport, error) {
	if pack.TenantID == "" {
		return SchemaExport{}, fmt.Errorf("%w: tenant is required", ErrInvalidRequest)
	}
	objects := append([]ontology.ObjectType(nil), pack.AllowedObjectTypes...)
	links := append([]ontology.LinkType(nil), pack.AllowedLinkTypes...)
	properties := append([]ontology.PropertyType(nil), pack.AllowedPropertyTypes...)
	propertyKeys := map[string]bool{}
	for _, p := range properties {
		propertyKeys[p.ObjectTypeID+"/"+p.PropertyID] = true
	}
	for _, o := range objects {
		for _, p := range o.Properties {
			key := o.ID + "/" + p.PropertyID
			if !propertyKeys[key] {
				properties = append(properties, ontology.PropertyType{ID: key, TenantID: o.TenantID, ObjectTypeID: o.ID, PropertyID: p.PropertyID, ValueType: p.ValueType})
				propertyKeys[key] = true
			}
		}
	}
	sort.Slice(objects, func(i, j int) bool { return objects[i].ID < objects[j].ID })
	sort.Slice(links, func(i, j int) bool { return links[i].ID < links[j].ID })
	sort.Slice(properties, func(i, j int) bool { return properties[i].ID < properties[j].ID })
	classes := map[string]ontology.ObjectType{}
	for _, o := range objects {
		if !definitionVisibleToTenant(o.TenantID, pack.TenantID) {
			return SchemaExport{}, fmt.Errorf("%w: foreign schema definition", ErrInvalidRequest)
		}
		classes[o.ID] = o
	}
	iri := func(kind string, ids ...string) string {
		parts := []string{"urn", "orrery", url.QueryEscape(pack.TenantID), kind}
		for _, id := range ids {
			parts = append(parts, url.QueryEscape(id))
		}
		return "<" + strings.Join(parts, ":") + ">"
	}
	header := "@prefix owl: <http://www.w3.org/2002/07/owl#> .\n@prefix rdf: <http://www.w3.org/1999/02/22-rdf-syntax-ns#> .\n@prefix rdfs: <http://www.w3.org/2000/01/rdf-schema#> .\n@prefix xsd: <http://www.w3.org/2001/XMLSchema#> .\n@prefix sh: <http://www.w3.org/ns/shacl#> .\n"
	var owl, shapes strings.Builder
	owl.WriteString(header)
	shapes.WriteString(header)
	for _, o := range objects {
		fmt.Fprintf(&owl, "%s a owl:Class .\n", iri("class", o.ID))
		fmt.Fprintf(&shapes, "%s a sh:NodeShape ; sh:targetClass %s .\n", iri("shape", o.ID), iri("class", o.ID))
	}
	for _, l := range links {
		if !definitionVisibleToTenant(l.TenantID, pack.TenantID) || classes[l.FromObjectType].ID == "" || classes[l.ToObjectType].ID == "" {
			return SchemaExport{}, fmt.Errorf("%w: unresolved link domain or range", ErrValidationFailed)
		}
		fmt.Fprintf(&owl, "%s a owl:ObjectProperty ; rdfs:domain %s ; rdfs:range %s .\n", iri("link", l.ID), iri("class", l.FromObjectType), iri("class", l.ToObjectType))
		fmt.Fprintf(&shapes, "%s sh:property [ sh:path %s ; sh:class %s", iri("shape", l.FromObjectType), iri("link", l.ID), iri("class", l.ToObjectType))
		if l.Cardinality == ontology.CardinalityOneToOne || l.Cardinality == ontology.CardinalityManyToOne {
			shapes.WriteString(" ; sh:maxCount 1")
		}
		shapes.WriteString(" ] .\n")
	}
	for _, p := range properties {
		if !definitionVisibleToTenant(p.TenantID, pack.TenantID) || classes[p.ObjectTypeID].ID == "" {
			return SchemaExport{}, fmt.Errorf("%w: unresolved property owner", ErrValidationFailed)
		}
		datatype := ""
		switch string(p.ValueType) {
		case "string", "enum":
			datatype = "xsd:string"
		case "number":
			datatype = ""
		case "integer":
			datatype = "xsd:integer"
		case "boolean":
			datatype = "xsd:boolean"
		case "datetime", "timestamp":
			datatype = "xsd:dateTime"
		case "date":
			datatype = "xsd:date"
		}
		fmt.Fprintf(&owl, "%s a owl:DatatypeProperty ; rdfs:domain %s", iri("property", p.ObjectTypeID, p.PropertyID), iri("class", p.ObjectTypeID))
		if datatype != "" {
			fmt.Fprintf(&owl, " ; rdfs:range %s", datatype)
		}
		if p.ValueType == ontology.ValueTypeNumber {
			owl.WriteString(" ; rdfs:range [ a rdfs:Datatype ; owl:unionOf ( xsd:integer xsd:decimal xsd:double xsd:float ) ]")
		}
		owl.WriteString(" .\n")
		fmt.Fprintf(&shapes, "%s sh:property [ sh:path %s", iri("shape", p.ObjectTypeID), iri("property", p.ObjectTypeID, p.PropertyID))
		if datatype != "" {
			fmt.Fprintf(&shapes, " ; sh:datatype %s", datatype)
		}
		if p.ValueType == ontology.ValueTypeNumber {
			shapes.WriteString(" ; sh:or ( [ sh:datatype xsd:integer ] [ sh:datatype xsd:decimal ] [ sh:datatype xsd:double ] [ sh:datatype xsd:float ] )")
		}
		for _, def := range classes[p.ObjectTypeID].Properties {
			if def.PropertyID == p.PropertyID && def.Required {
				shapes.WriteString(" ; sh:minCount 1")
				break
			}
		}
		shapes.WriteString(" ] .\n")
	}
	return SchemaExport{TenantID: pack.TenantID, OWL: owl.String(), SHACL: shapes.String(), ObjectTypes: objects, LinkTypes: links, PropertyTypes: properties, Limitations: []string{"Structural export only; no OWL consistency proof.", "JSON sidecar is authoritative for tenant, sensitivity, authority, temporal and action governance semantics.", "JSON/array value shapes, evidence requirements and inverse cardinalities require host validation."}}, nil
}
