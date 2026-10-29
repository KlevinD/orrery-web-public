package core

import (
	"errors"
	"testing"
)

func TestDefaultSummaryGeneratorUsesProviderTextOrStructuredOutputs(t *testing.T) {
	generator := DefaultSummaryGenerator{}
	summary, err := generator.GenerateSummary(AgentInput{}, RuntimeResponse{
		Messages: []RuntimeMessage{{Type: RuntimeMessageText, Content: " provider text "}},
	}, AgentResult{})
	if err != nil {
		t.Fatalf("generate from text: %v", err)
	}
	if summary.Text != "provider text" {
		t.Fatalf("summary = %#v", summary)
	}

	summary, err = generator.GenerateSummary(AgentInput{}, RuntimeResponse{}, AgentResult{
		Deliverables: []Deliverable{{ID: "deliverable-1"}},
		Evidence:     []Evidence{{ID: "evidence-1"}},
	})
	if err != nil {
		t.Fatalf("generate from structured outputs: %v", err)
	}
	if summary.Text == "" {
		t.Fatalf("summary was empty")
	}
}

func TestDefaultSummaryGeneratorUsesFinalProviderTextAfterStreamDeltas(t *testing.T) {
	summary, err := DefaultSummaryGenerator{}.GenerateSummary(AgentInput{}, RuntimeResponse{
		Messages: []RuntimeMessage{
			{Type: RuntimeMessageText, Content: "What "},
			{Type: RuntimeMessageText, Content: "deadline?"},
			{Type: RuntimeMessageText, Content: "What deadline?"},
		},
	}, AgentResult{})
	if err != nil {
		t.Fatalf("generate from streamed text: %v", err)
	}
	if summary.Text != "What deadline?" {
		t.Fatalf("summary = %q, want final provider text", summary.Text)
	}
}

func TestDefaultSummaryGeneratorPrefersMarkedFinalTextSnapshot(t *testing.T) {
	summary, err := DefaultSummaryGenerator{}.GenerateSummary(AgentInput{}, RuntimeResponse{
		Messages: []RuntimeMessage{
			{Type: RuntimeMessageText, Content: "draft "},
			{Type: RuntimeMessageText, Content: "tokens"},
			{Type: RuntimeMessageText, Content: "final answer", Status: RuntimeMessageStatusFinal},
		},
	}, AgentResult{})
	if err != nil {
		t.Fatalf("generate from final text: %v", err)
	}
	if summary.Text != "final answer" {
		t.Fatalf("summary = %q, want final provider text", summary.Text)
	}
}

func TestDefaultSummaryGeneratorUsesCompletedPlanWhenRuntimeHasNoText(t *testing.T) {
	summary, err := DefaultSummaryGenerator{}.GenerateSummary(AgentInput{}, RuntimeResponse{
		Messages: []RuntimeMessage{{
			Type: RuntimeMessagePlan,
			Plan: &RuntimePlanState{
				PlanID: "plan-1",
				Status: "completed",
				Items: []RuntimePlanItem{
					{ID: "step-1", Text: "Inspect runtime bridge", Status: "completed"},
					{ID: "step-2", Text: "Persist progress", Status: "completed"},
				},
			},
		}},
	}, AgentResult{})
	if err != nil {
		t.Fatalf("generate summary: %v", err)
	}
	if summary.Text != "Completed 2 plan step(s)." {
		t.Fatalf("summary = %q", summary.Text)
	}
}

func TestDefaultSummaryGeneratorRejectsUnsupportedEmptyOutput(t *testing.T) {
	_, err := DefaultSummaryGenerator{}.GenerateSummary(AgentInput{}, RuntimeResponse{}, AgentResult{})
	if !errors.Is(err, ErrSummaryUnavailable) {
		t.Fatalf("error = %v, want ErrSummaryUnavailable", err)
	}
}
