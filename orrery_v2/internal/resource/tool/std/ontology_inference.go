package std

func ontologyInferenceWorkflowInputSchema() map[string]any {
	return objectSchema("model inference decision and execution", map[string]any{
		"operation":         enumSchema("Review the evidence, inspect a plan, or execute that plan.", "review", "plan", "apply"),
		"tenant_id":         stringSchema("Runtime tenant takes precedence."),
		"run_id":            stringSchema("Inference run ID returned by ontology_infer."),
		"candidate_id":      stringSchema("Exact current candidate ID."),
		"expected_revision": map[string]any{"type": "integer", "minimum": 1},
		"support_hash":      stringSchema("Support hash returned for this candidate."),
		"decision":          enumSchema("Required for review; approve new/current support, reject an unapplied claim, or retract your active owned assertion.", "approve", "reject", "retract"),
		"reason":            map[string]any{"type": "string", "maxLength": 2000, "description": "Required for review. Explain why the evidence supports or fails to support this relationship."},
		"plan_id":           stringSchema("Exact ID returned by plan; required for apply."),
		"idempotency_key":   map[string]any{"type": "string", "maxLength": 128, "description": "Stable key for this apply operation; reuse on retry."},
		"confirmed":         map[string]any{"type": "boolean", "description": "Set true after the model has checked the planned relation. Human confirmation is not required."},
	}, []string{"operation", "tenant_id", "run_id", "candidate_id", "expected_revision", "support_hash"})
}

func ontologyInferenceInputSchema() map[string]any {
	atom := objectSchema("positive rule atom", map[string]any{
		"kind":          enumSchema("Statement kind.", "object", "link", "fact"),
		"type":          stringSchema("Object or link type; omit for fact."),
		"subject":       stringSchema("Case-sensitive object ID or ?variable."),
		"object":        stringSchema("Link endpoint ID or ?variable."),
		"predicate":     stringSchema("Registered fact property."),
		"relation_code": enumSchema("Optional exact meaning for a knowledge_entity_related_to link premise. Omit to match any relation; forbidden in conclusions. Unresolved conflicting facts are withheld from rule premises.", "subsidiary_of", "owns", "supplies", "customer_of", "works_for", "party_to", "produces", "located_in", "depends_on", "related_to"),
		"value":         map[string]any{"description": "Literal JSON fact value; values are never executable."},
	}, []string{"kind", "subject"})
	rule := objectSchema("versioned inference rule", map[string]any{
		"id": stringSchema("Rule ID."), "version": stringSchema("Immutable rule version."),
		"when": arraySchema("Positive conjunctive body with at most six atoms.", atom), "then": atom,
	}, []string{"id", "version", "when", "then"})
	ref := objectSchema("visible object reference", map[string]any{"type": stringSchema("Registered object type."), "id": stringSchema("Exact visible ontology object ID.")}, []string{"type", "id"})
	candidate := objectSchema("document claim candidate", map[string]any{
		"kind":        enumSchema("Claim kind; entity IDs are assigned by the server.", "entity", "link", "fact"),
		"object_type": stringSchema("Registered type for an entity claim."), "canonical_key": stringSchema("Stable canonical key for an entity claim."), "label": stringSchema("Proposed entity label."),
		"link_type": stringSchema("Registered link type."), "from": ref, "to": ref, "subject": ref,
		"predicate": stringSchema("Registered property for a fact claim."), "value": map[string]any{}, "properties": map[string]any{"type": "object"},
		"confidence": map[string]any{"type": "number", "minimum": 0, "maximum": 1},
	}, []string{"kind"})
	claim := objectSchema("quoted extracted claim", map[string]any{"quote": stringSchema("Exact literal excerpt from document text, at most 4000 UTF-8 bytes."), "candidate": candidate}, []string{"quote", "candidate"})
	document := objectSchema("requester-submitted source material", map[string]any{
		"text":              stringSchema("Source text, at most 128 KiB UTF-8. Submitted material is not an attested knowledge-store revision."),
		"extractor_version": stringSchema("Version of the agent/model or deterministic extractor."),
		"claims":            arraySchema("Extract factual entity, link and property statements with literal quotes. Existing endpoints must be visible exact IDs; unsupported claims are rejected, and accepted claims require review.", claim),
	}, []string{"text", "extractor_version"})
	return objectSchema("ontology_infer input", map[string]any{
		"rule_set_id":      stringSchema("Optional published rule set owned by this Agent; its stored root and rules take precedence."),
		"rule_set_version": map[string]any{"type": "integer", "minimum": 1},
		"tenant_id":        stringSchema("Scope locator; server runtime tenant takes precedence."),
		"root_id":          stringSchema("Visible ontology object anchoring the two-hop context."),
		"rules":            arraySchema("At most 32 versioned rules; empty for document-only extraction.", rule),
		"previous_run_id":  stringSchema("Previous run by this requester for this root, to compare supported conclusions."),
		"business_as_of":   map[string]any{"type": "string", "format": "date-time", "description": "Optional RFC3339 business time; requires known_at. Explicit times create a historical read-only run that cannot enter current writeback."},
		"known_at":         map[string]any{"type": "string", "format": "date-time", "description": "What the system had recorded by this RFC3339 time; requires business_as_of, cannot be in the future. Current permissions still apply. Omit previous_run_id and documents."},
		"documents":        arraySchema("Source material and quoted claims extracted by the calling agent. Ontology frontmatter assertions are also extracted by the server.", document),
	}, []string{"tenant_id", "root_id", "rules"})
}

func ontologyInferenceRulesInputSchema() map[string]any {
	rules := ontologyInferenceInputSchema()["properties"].(map[string]any)["rules"]
	return objectSchema("publish, list or pause continuous inference", map[string]any{
		"operation":        enumSchema("Manage this Agent's published rule sets.", "publish", "list", "pause"),
		"tenant_id":        stringSchema("Runtime tenant takes precedence."),
		"rule_set_id":      stringSchema("Stable rule set ID, at most 128 bytes; required for publish/pause."),
		"expected_version": map[string]any{"type": "integer", "minimum": 0, "description": "0 for creation, current returned version for a change or pause."},
		"root_id":          stringSchema("Required for publication; immutable for this rule set."),
		"rules":            rules,
	}, []string{"operation", "tenant_id"})
}

func ontologyDocumentInferenceInputSchema() map[string]any {
	return objectSchema("model-driven document knowledge instances", map[string]any{
		"operation":        enumSchema("Manage this Agent's document watches.", "publish", "list", "status", "retry", "pause"),
		"tenant_id":        stringSchema("Runtime tenant takes precedence."),
		"watch_id":         stringSchema("Stable watch ID, at most 128 bytes; required except for list."),
		"expected_version": map[string]any{"type": "integer", "minimum": 0},
		"root_id":          stringSchema("Visible knowledge_document object ID; required for publish and immutable thereafter."),
	}, []string{"operation", "tenant_id"})
}
