package main

import (
	"testing"
	"time"

	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/retrievalevidence"
)

func TestProductionMemorySearchKeepsHostEvidence(t *testing.T) {
	item := retrievalevidence.Seal(retrievalevidence.Item{Source: retrievalevidence.Source{Kind: "memory_record", ID: "record", Version: "v1"}, Purpose: retrievalevidence.PurposeMemory, TruthState: retrievalevidence.TruthUnknown}, time.Now())
	input := discussionsession.DiscussionAgentMemoryDigest{DigestID: "digest", Summary: "summary", Evidence: []retrievalevidence.Item{item}}
	output := productionMemorySearchDigest(input)
	items, ok := output["evidence"].([]retrievalevidence.Item)
	if !ok || len(items) != 1 || items[0].InputHash != item.InputHash {
		t.Fatalf("output=%+v", output)
	}
	items[0].Source.Version = "mutated"
	if input.Evidence[0].Source.Version != "v1" {
		t.Fatal("tool output aliases provider evidence")
	}
}

func TestProductionDiscussionKnowledgeEvidenceIsolation(t *testing.T) {
	item := retrievalevidence.Seal(retrievalevidence.Item{Source: retrievalevidence.Source{Kind: "knowledge_document", ID: "document", Version: "v1"}, Purpose: retrievalevidence.PurposeMaterial, TruthState: retrievalevidence.TruthUnknown}, time.Now())
	input := []knowledgeadmin.KnowledgeSearchResultDTO{{Snippet: "material", Evidence: &item, HeadingPath: []string{"heading"}}}
	out := mapDiscussionKnowledgeContext(input)
	if len(out.Results) != 1 || out.Results[0].Evidence == nil || out.Results[0].Evidence.InputHash != item.InputHash {
		t.Fatalf("output=%+v", out)
	}
	out.Results[0].Evidence.Source.Version = "changed"
	out.Results[0].HeadingPath[0] = "changed"
	if input[0].Evidence.Source.Version != "v1" || input[0].HeadingPath[0] != "heading" {
		t.Fatal("discussion context aliases host search results")
	}
}
