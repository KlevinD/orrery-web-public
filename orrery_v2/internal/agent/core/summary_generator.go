package core

import (
	"errors"
	"fmt"
	"strings"
)

var ErrSummaryUnavailable = errors.New("summary cannot be generated without runtime output, deliverables, or evidence")

type SummaryGenerator interface {
	GenerateSummary(input AgentInput, response RuntimeResponse, result AgentResult) (Summary, error)
}

type DefaultSummaryGenerator struct{}

func (DefaultSummaryGenerator) GenerateSummary(_ AgentInput, response RuntimeResponse, result AgentResult) (Summary, error) {
	if !result.Summary.IsZero() {
		return result.Summary, nil
	}
	if text := finalRuntimeMessageText(response.Messages); text != "" {
		return Summary{Text: text}, nil
	}
	var textParts []string
	for _, message := range response.Messages {
		if message.Type == RuntimeMessageText && strings.TrimSpace(message.Content) != "" {
			textParts = append(textParts, message.Content)
		}
	}
	if text := mergedRuntimeText(textParts); text != "" {
		return Summary{Text: text}, nil
	}
	if plan := completedRuntimePlan(response.Messages); plan != nil {
		return Summary{Text: fmt.Sprintf("Completed %d plan step(s).", len(plan.Items))}, nil
	}
	if len(result.Deliverables) > 0 || len(result.Evidence) > 0 {
		return Summary{Text: fmt.Sprintf("Produced %d deliverable(s) and %d evidence item(s).", len(result.Deliverables), len(result.Evidence))}, nil
	}
	return Summary{}, ErrSummaryUnavailable
}

func completedRuntimePlan(messages []RuntimeMessage) *RuntimePlanState {
	for i := len(messages) - 1; i >= 0; i-- {
		plan := messages[i].Plan
		if messages[i].Type == RuntimeMessagePlan && plan != nil && strings.TrimSpace(plan.Status) == "completed" {
			return plan
		}
	}
	return nil
}
