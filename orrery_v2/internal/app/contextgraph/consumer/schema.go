package consumer

type JSONSchema struct {
	Type       string                `json:"type"`
	Required   []string              `json:"required,omitempty"`
	Properties map[string]JSONSchema `json:"properties,omitempty"`
	Items      *JSONSchema           `json:"items,omitempty"`
}

type ToolDescriptor struct {
	Name         string     `json:"name"`
	Description  string     `json:"description"`
	InputSchema  JSONSchema `json:"input_schema"`
	OutputSchema JSONSchema `json:"output_schema,omitempty"`
	ReadOnly     bool       `json:"read_only"`
}

func ToolDescriptors() []ToolDescriptor {
	return []ToolDescriptor{
		buildContextBundleTool(),
		tool("enterprise_graph.search_objects", "Search visible enterprise graph objects.", true, []string{"tenant_id", "actor_ref", "query"}),
		tool("enterprise_graph.get_neighbors", "Read visible neighbors for an object.", true, []string{"tenant_id", "actor_ref", "object_ref"}),
		tool("enterprise_graph.explain_evidence", "Explain provenance and evidence for a fact or object.", true, []string{"tenant_id", "actor_ref", "evidence_ref"}),
		tool("enterprise_graph.submit_observation", "Submit a suggested observation for review.", false, []string{"tenant_id", "actor_ref", "source_ref", "object_refs", "observation"}),
		tool("enterprise_graph.request_writeback", "Request an Action Contract writeback decision.", false, []string{"tenant_id", "actor_ref", "action_type_id", "object_refs", "args"}),
	}
}

func buildContextBundleTool() ToolDescriptor {
	descriptor := tool("enterprise_graph.build_context_bundle", "Build a permission-trimmed context bundle with ontology schema dictionary.", true, []string{"tenant_id", "consumer", "purpose", "actor_ref"})
	descriptor.OutputSchema = JSONSchema{
		Type: "object",
		Properties: map[string]JSONSchema{
			"bundle_id":          {Type: "string"},
			"tenant_id":          {Type: "string"},
			"consumer":           {Type: "string"},
			"purpose":            {Type: "string"},
			"actor_ref":          refSchema(),
			"schema":             schemaDictionarySchema(),
			"objects":            {Type: "array", Items: schemaPtr(objectSchema())},
			"links":              {Type: "array", Items: schemaPtr(linkSchema())},
			"facts":              {Type: "array", Items: schemaPtr(factSchema())},
			"evidence":           {Type: "array", Items: schemaPtr(evidenceSchema())},
			"knowledge_evidence": {Type: "array", Items: schemaPtr(knowledgeEvidenceSchema())},
			"partial":            {Type: "boolean"},
			"partial_reason":     {Type: "string"},
			"truncated": {Type: "object", Properties: map[string]JSONSchema{
				"objects": {Type: "integer"}, "links": {Type: "integer"},
				"facts": {Type: "integer"}, "evidence": {Type: "integer"},
			}},
		},
	}
	return descriptor
}

func knowledgeEvidenceSchema() JSONSchema {
	return JSONSchema{Type: "object", Properties: map[string]JSONSchema{
		"knowledge_base_id": {Type: "string"}, "document_id": {Type: "string"},
		"chunk_id": {Type: "string"}, "revision": {Type: "integer"},
		"content_hash": {Type: "string"}, "snippet": {Type: "string"},
		"heading_path": {Type: "array", Items: schemaPtr(JSONSchema{Type: "string"})},
	}}
}

func tool(name string, description string, readOnly bool, required []string) ToolDescriptor {
	return ToolDescriptor{
		Name:        name,
		Description: description,
		ReadOnly:    readOnly,
		InputSchema: JSONSchema{
			Type:     "object",
			Required: append([]string(nil), required...),
			Properties: map[string]JSONSchema{
				"tenant_id":      {Type: "string"},
				"consumer":       {Type: "string"},
				"purpose":        {Type: "string"},
				"actor_ref":      refSchema(),
				"subject_refs":   {Type: "array", Items: schemaPtr(refSchema())},
				"query":          {Type: "string"},
				"object_ref":     refSchema(),
				"evidence_ref":   refSchema(),
				"source_ref":     refSchema(),
				"object_refs":    {Type: "array", Items: schemaPtr(refSchema())},
				"observation":    {Type: "object"},
				"action_type_id": {Type: "string"},
				"args":           {Type: "object"},
			},
		},
	}
}

func schemaDictionarySchema() JSONSchema {
	return JSONSchema{
		Type: "object",
		Properties: map[string]JSONSchema{
			"object_types":   {Type: "array", Items: schemaPtr(schemaObjectTypeSchema())},
			"link_types":     {Type: "array", Items: schemaPtr(schemaLinkTypeSchema())},
			"property_types": {Type: "array", Items: schemaPtr(schemaPropertyTypeSchema())},
			"action_types":   {Type: "array", Items: schemaPtr(schemaActionTypeSchema())},
			"version_hash":   {Type: "string"},
		},
	}
}

func schemaObjectTypeSchema() JSONSchema {
	return JSONSchema{
		Type: "object",
		Properties: map[string]JSONSchema{
			"id":                 {Type: "string"},
			"display_name":       {Type: "string"},
			"canonical_key_rule": {Type: "string"},
			"owner_domain":       {Type: "string"},
			"allowed_link_types": {Type: "array", Items: schemaPtr(JSONSchema{Type: "string"})},
			"allowed_actions":    {Type: "array", Items: schemaPtr(JSONSchema{Type: "string"})},
		},
	}
}

func schemaLinkTypeSchema() JSONSchema {
	return JSONSchema{
		Type: "object",
		Properties: map[string]JSONSchema{
			"id":               {Type: "string"},
			"display_name":     {Type: "string"},
			"from_object_type": {Type: "string"},
			"to_object_type":   {Type: "string"},
			"cardinality":      {Type: "string"},
		},
	}
}

func schemaPropertyTypeSchema() JSONSchema {
	return JSONSchema{
		Type: "object",
		Properties: map[string]JSONSchema{
			"id":             {Type: "string"},
			"object_type_id": {Type: "string"},
			"property_id":    {Type: "string"},
			"display_name":   {Type: "string"},
			"value_type":     {Type: "string"},
		},
	}
}

func schemaActionTypeSchema() JSONSchema {
	return JSONSchema{
		Type: "object",
		Properties: map[string]JSONSchema{
			"id":               {Type: "string"},
			"name":             {Type: "string"},
			"category":         {Type: "string"},
			"object_type_refs": {Type: "array", Items: schemaPtr(JSONSchema{Type: "string"})},
			"provider_kind":    {Type: "string"},
			"risk_level":       {Type: "string"},
			"enabled":          {Type: "boolean"},
		},
	}
}

func objectSchema() JSONSchema {
	return JSONSchema{
		Type: "object",
		Properties: map[string]JSONSchema{
			"id":            {Type: "string"},
			"tenant_id":     {Type: "string"},
			"object_type":   {Type: "string"},
			"canonical_key": {Type: "string"},
			"label":         {Type: "string"},
			"properties":    {Type: "object"},
		},
	}
}

func linkSchema() JSONSchema {
	return JSONSchema{
		Type: "object",
		Properties: map[string]JSONSchema{
			"id":             {Type: "string"},
			"tenant_id":      {Type: "string"},
			"link_type":      {Type: "string"},
			"from_object_id": {Type: "string"},
			"to_object_id":   {Type: "string"},
			"properties":     {Type: "object"},
		},
	}
}

func factSchema() JSONSchema {
	return JSONSchema{
		Type: "object",
		Properties: map[string]JSONSchema{
			"id":        {Type: "string"},
			"tenant_id": {Type: "string"},
			"subject":   refSchema(),
			"predicate": {Type: "string"},
			"value":     {Type: "object"},
		},
	}
}

func evidenceSchema() JSONSchema {
	return JSONSchema{
		Type: "object",
		Properties: map[string]JSONSchema{
			"id":         {Type: "string"},
			"tenant_id":  {Type: "string"},
			"source_ref": refSchema(),
			"summary":    {Type: "string"},
		},
	}
}

func refSchema() JSONSchema {
	return JSONSchema{
		Type:     "object",
		Required: []string{"kind", "id"},
		Properties: map[string]JSONSchema{
			"kind": {Type: "string"},
			"id":   {Type: "string"},
		},
	}
}

func schemaPtr(schema JSONSchema) *JSONSchema {
	return &schema
}
