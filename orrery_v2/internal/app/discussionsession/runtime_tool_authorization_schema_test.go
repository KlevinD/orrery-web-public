package discussionsession

import (
	"testing"

	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestRuntimeToolBindingsHideHostOwnedAuthorizationInputs(t *testing.T) {
	bindings := runtimeToolBindingsFromDiscussionTools([]DiscussionAgentBoundTool{
		{ToolID: string(toolstd.EnterpriseGraphBuildContextBundle)},
		{ToolID: string(toolstd.EnterpriseGraphSearchObjects)},
		{ToolID: string(toolstd.KnowledgeSearch)},
	}, nil)
	if len(bindings) != 3 {
		t.Fatalf("runtime bindings = %d", len(bindings))
	}

	assertRuntimeSchemaFields(t, bindings[0].InputSchema,
		[]string{"subject_refs", "include_kinds", "max_depth", "token_budget"},
		[]string{"tenant_id", "actor_ref", "consumer", "purpose"},
	)
	assertRuntimeSchemaFields(t, bindings[1].InputSchema,
		[]string{"query", "include_kinds", "limit", "cursor"},
		[]string{"tenant_id", "actor_ref"},
	)
	assertRuntimeSchemaFields(t, bindings[2].InputSchema,
		[]string{"query", "retrieval_mode", "limit"},
		[]string{"knowledge_base_ids", "owner_type", "owner_id"},
	)
}

func assertRuntimeSchemaFields(t *testing.T, schema map[string]any, present, absent []string) {
	t.Helper()
	properties, _ := schema["properties"].(map[string]any)
	for _, field := range present {
		if _, ok := properties[field]; !ok {
			t.Errorf("runtime schema missing %q: %+v", field, schema)
		}
	}
	for _, field := range absent {
		if _, ok := properties[field]; ok {
			t.Errorf("runtime schema exposes host-owned field %q: %+v", field, schema)
		}
		for _, required := range runtimeSchemaRequired(schema) {
			if required == field {
				t.Errorf("runtime schema still requires host-owned field %q: %+v", field, schema)
			}
		}
	}
}

func runtimeSchemaRequired(schema map[string]any) []string {
	values, _ := schema["required"].([]any)
	out := make([]string, 0, len(values))
	for _, value := range values {
		if field, ok := value.(string); ok {
			out = append(out, field)
		}
	}
	return out
}
