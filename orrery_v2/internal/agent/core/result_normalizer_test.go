package core

import (
	"testing"
	"time"
)

func TestNormalizeRuntimeResponseMapsArtifactsToDeliverablesAndToolCallsToEvidence(t *testing.T) {
	input := validAgentInput()
	result, err := normalizeRuntimeResponse(input, RuntimeResponse{
		Messages:  []RuntimeMessage{{Type: RuntimeMessageText, Content: "done"}},
		Artifacts: []ArtifactRef{{ID: "artifact-1", Kind: ArtifactKindCode, URI: "file://diff.patch"}},
		ToolCalls: []RuntimeToolCall{{ID: "evidence-1", Name: "go test", Output: "ok"}},
	}, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(result.Deliverables) != 1 || result.Deliverables[0].ArtifactRef.ID != "artifact-1" {
		t.Fatalf("deliverables = %#v", result.Deliverables)
	}
	if len(result.Evidence) != 1 || result.Evidence[0].ToolLogRef.ID != "evidence-1" {
		t.Fatalf("evidence = %#v", result.Evidence)
	}
}

func TestNormalizeRuntimeResponseUsesFinalProviderTextAfterStreamDeltas(t *testing.T) {
	result, err := normalizeRuntimeResponse(validAgentInput(), RuntimeResponse{
		Messages: []RuntimeMessage{
			{Type: RuntimeMessageText, Content: "What "},
			{Type: RuntimeMessageText, Content: "deadline?"},
			{Type: RuntimeMessageText, Content: "What deadline?"},
		},
	}, time.Now())
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if result.Summary.Text != "What deadline?" {
		t.Fatalf("summary = %q, want final provider text", result.Summary.Text)
	}
}

func TestNormalizeRuntimeResponseDoesNotInventEvidenceFromArtifacts(t *testing.T) {
	result, err := normalizeRuntimeResponse(validAgentInput(), RuntimeResponse{
		Messages:  []RuntimeMessage{{Type: RuntimeMessageText, Content: "done"}},
		Artifacts: []ArtifactRef{{ID: "artifact-1", Kind: ArtifactKindLog, URI: "file://test.log"}},
	}, time.Now())
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(result.Evidence) != 0 {
		t.Fatalf("evidence was invented from artifact index: %#v", result.Evidence)
	}
}

func TestNormalizeRuntimeResponseDoesNotTreatUnmappedToolCallsAsEvidence(t *testing.T) {
	result, err := normalizeRuntimeResponse(validAgentInput(), RuntimeResponse{
		Messages:  []RuntimeMessage{{Type: RuntimeMessageText, Content: "done"}},
		ToolCalls: []RuntimeToolCall{{ID: "provider-call-1", Name: "lookup", Output: "ok"}},
	}, time.Now())
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(result.Evidence) != 0 {
		t.Fatalf("unmapped tool call was promoted to evidence: %#v", result.Evidence)
	}
}
