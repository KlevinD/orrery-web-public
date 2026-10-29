package main

import (
	"encoding/json"
	"testing"
	"time"
)

func TestWebsiteToolSchemasNeverEncodeNullRequired(t *testing.T) {
	definitions := defaultWebsiteKnowledgeCurationToolDefinitions(time.Unix(0, 0))
	for _, definition := range definitions {
		t.Run(string(definition.ID), func(t *testing.T) {
			raw, err := json.Marshal(definition.InputSchema)
			if err != nil {
				t.Fatal(err)
			}
			var schema map[string]any
			if err := json.Unmarshal(raw, &schema); err != nil {
				t.Fatal(err)
			}
			required, ok := schema["required"].([]any)
			if !ok {
				t.Fatalf("required must encode as an array, got %s", raw)
			}
			if definition.ID != defaultWebsiteKnowledgeProposalCreateToolID && len(required) != 0 {
				t.Fatalf("read tool unexpectedly requires parameters: %v", required)
			}
			if definition.ID == defaultWebsiteKnowledgeProposalCreateToolID && len(required) != 5 {
				t.Fatalf("proposal requirements changed: %v", required)
			}
		})
	}
}
