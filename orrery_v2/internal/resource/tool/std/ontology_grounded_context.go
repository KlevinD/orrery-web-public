package std

import (
	"time"

	"orrery_v2/internal/resource/tool"
)

func groundedOntologyContextTool(now time.Time) tool.ToolDefinition {
	rules := ontologyInferenceInputSchema()["properties"].(map[string]any)["rules"]
	support := objectSchema("exact host-checked support", map[string]any{
		"assertion_ref": stringSchema("Exact assertion_ref from a previous grounded context support."),
		"version":       stringSchema("Exact host-generated assertion version; another source cannot substitute for it."),
	}, []string{"assertion_ref", "version"})
	candidate := objectSchema("explicit context dependencies", map[string]any{
		"id":                stringSchema("Caller candidate identifier; host does not infer text-to-claim alignment."),
		"required_claims":   arraySchema("All these claim IDs must still be supported. Any independent proof may maintain a claim.", stringSchema("Claim ID returned by this tool.")),
		"required_supports": arraySchema("All these exact assertion versions must remain current.", support),
	}, []string{"id"})
	input := objectSchema("grounded current ontology context", map[string]any{
		"tenant_id":  stringSchema("Runtime tenant takes precedence."),
		"root_id":    stringSchema("Visible ontology object anchoring a bounded two-hop context."),
		"rules":      rules,
		"candidates": arraySchema("Optional prior context items with explicit dependencies. Rechecked against this new host snapshot before use; unsupported dependencies are ineligible.", candidate),
	}, []string{"tenant_id", "root_id", "rules"})
	definition := systemTool(OntologyGroundedContext, "Grounded ontology context", "Read current authorized ontology assertions and structurally valid rule proofs with exact support versions. Recomputes AND/OR support, time eligibility and conflicts; validates declared cached context dependencies. Raw knowledge material and unknown or disputed text are not definite premises. Read-only: creates no inference run, proposal or writeback. Judge source certainty and rule meaning yourself; no human review is needed.", PermissionOntologyRead, "Reads ontology context within the calling identity's existing scope and source permissions.", tool.RiskLow, now)
	definition.InputSchema, definition.OutputSchema = input, objectSchema("grounded ontology output", map[string]any{"context": map[string]any{"type": "object"}}, []string{"context"})
	definition.Tags = []string{"system", "ontology", "context", "mcp", "std"}
	return definition
}
