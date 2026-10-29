package hil

import (
	"errors"
	"testing"
)

func TestValidateWorkflowDefinitionAcceptsApprovalStrategies(t *testing.T) {
	tests := []struct {
		name      string
		strategy  ApprovalStrategy
		threshold int
		selectors int
	}{
		{name: "single", strategy: ApprovalSingle, selectors: 1},
		{name: "any one", strategy: ApprovalAny, selectors: 2},
		{name: "all", strategy: ApprovalAll, selectors: 2},
		{name: "threshold", strategy: ApprovalThreshold, threshold: 2, selectors: 3},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			definition := validWorkflowDefinition()
			definition.Stages[0].Approval.Strategy = test.strategy
			definition.Stages[0].Approval.Threshold = test.threshold
			definition.Stages[0].Approval.Assignees = make([]WorkflowActorSelector, test.selectors)
			for i := range definition.Stages[0].Approval.Assignees {
				definition.Stages[0].Approval.Assignees[i] = WorkflowActorSelector{Kind: WorkflowSelectorHuman, ID: "human-" + string(rune('a'+i))}
			}
			if err := ValidateWorkflowDefinition(definition); err != nil {
				t.Fatalf("ValidateWorkflowDefinition: %v", err)
			}
		})
	}
}

func TestValidateWorkflowDefinitionRejectsInvalidApprovalDefinitions(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*WorkflowDefinition)
	}{
		{name: "no stages", mutate: func(definition *WorkflowDefinition) { definition.Stages = nil }},
		{name: "distinct approvers with one approval stage", mutate: func(definition *WorkflowDefinition) { definition.RequireDistinctApprovers = true }},
		{name: "duplicate stage key", mutate: func(definition *WorkflowDefinition) {
			definition.Stages = append(definition.Stages, WorkflowStageDefinition{
				Key: "review", Name: "Duplicate", Order: 2, Type: WorkflowStageApproval,
				Approval: &ApprovalStagePolicy{Strategy: ApprovalSingle, Assignees: []WorkflowActorSelector{{Kind: WorkflowSelectorHuman, ID: "human-b"}}, DeadlineMinutes: 60},
			})
		}},
		{name: "non sequential order", mutate: func(definition *WorkflowDefinition) { definition.Stages[0].Order = 2 }},
		{name: "unknown strategy", mutate: func(definition *WorkflowDefinition) { definition.Stages[0].Approval.Strategy = "unknown" }},
		{name: "single has multiple assignees", mutate: func(definition *WorkflowDefinition) {
			definition.Stages[0].Approval.Assignees = append(definition.Stages[0].Approval.Assignees, WorkflowActorSelector{Kind: WorkflowSelectorHuman, ID: "human-b"})
		}},
		{name: "threshold is zero", mutate: func(definition *WorkflowDefinition) {
			definition.Stages[0].Approval.Strategy = ApprovalThreshold
			definition.Stages[0].Approval.Threshold = 0
		}},
		{name: "threshold exceeds assignees", mutate: func(definition *WorkflowDefinition) {
			definition.Stages[0].Approval.Strategy = ApprovalThreshold
			definition.Stages[0].Approval.Threshold = 2
		}},
		{name: "council selector in approval", mutate: func(definition *WorkflowDefinition) {
			definition.Stages[0].Approval.Assignees[0] = WorkflowActorSelector{Kind: WorkflowSelectorOntologyRecommended}
		}},
		{name: "missing deadline", mutate: func(definition *WorkflowDefinition) { definition.Stages[0].Approval.DeadlineMinutes = 0 }},
		{name: "reminder after deadline", mutate: func(definition *WorkflowDefinition) { definition.Stages[0].Approval.ReminderOffsetsMinutes = []int{60} }},
		{name: "escalation has no actor", mutate: func(definition *WorkflowDefinition) {
			definition.Stages[0].Approval.Escalation = []WorkflowEscalationRung{{DeadlineMinutes: 30}}
		}},
		{name: "escalation has multiple actors", mutate: func(definition *WorkflowDefinition) {
			definition.Stages[0].Approval.Escalation = []WorkflowEscalationRung{{
				Actors: []WorkflowActorSelector{{Kind: WorkflowSelectorHuman, ID: "human-b"}, {Kind: WorkflowSelectorHuman, ID: "human-c"}}, DeadlineMinutes: 30,
			}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			definition := validWorkflowDefinition()
			test.mutate(&definition)
			if err := ValidateWorkflowDefinition(definition); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("error = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestValidateWorkflowDefinitionRejectsInvalidCouncilPolicy(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*CouncilStagePolicy)
	}{
		{name: "zero quorum", mutate: func(policy *CouncilStagePolicy) { policy.Quorum = 0 }},
		{name: "quorum exceeds participants", mutate: func(policy *CouncilStagePolicy) { policy.Quorum = 3 }},
		{name: "threshold exceeds quorum", mutate: func(policy *CouncilStagePolicy) { policy.ApprovalThreshold = 3 }},
		{name: "missing deadline", mutate: func(policy *CouncilStagePolicy) { policy.DeadlineMinutes = 0 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			definition := validCouncilWorkflowDefinition(RiskLow)
			test.mutate(definition.Stages[0].Council)
			if err := ValidateWorkflowDefinition(definition); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("error = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestValidateWorkflowDefinitionRejectsHighRiskCouncilTerminal(t *testing.T) {
	for _, risk := range []RiskLevel{RiskHigh, RiskCritical} {
		definition := validCouncilWorkflowDefinition(risk)
		if err := ValidateWorkflowDefinition(definition); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("risk=%s error=%v, want ErrInvalidRequest", risk, err)
		}
	}

	definition := validCouncilWorkflowDefinition(RiskHigh)
	definition.Stages = append(definition.Stages, WorkflowStageDefinition{
		Key: "final-approval", Name: "Final approval", Order: 2, Type: WorkflowStageApproval,
		Approval: &ApprovalStagePolicy{Strategy: ApprovalSingle, Assignees: []WorkflowActorSelector{{Kind: WorkflowSelectorHuman, ID: "human-final"}}, DeadlineMinutes: 60},
	})
	if err := ValidateWorkflowDefinition(definition); err != nil {
		t.Fatalf("high-risk workflow with final human approval: %v", err)
	}
}

func validWorkflowDefinition() WorkflowDefinition {
	return WorkflowDefinition{
		RiskLevels: []RiskLevel{RiskLow},
		Stages: []WorkflowStageDefinition{{
			Key: "review", Name: "Review", Order: 1, Type: WorkflowStageApproval,
			Approval: &ApprovalStagePolicy{
				Strategy: ApprovalSingle, Assignees: []WorkflowActorSelector{{Kind: WorkflowSelectorHuman, ID: "human-a"}},
				DeadlineMinutes: 60, ReminderOffsetsMinutes: []int{30},
			},
		}},
	}
}

func validCouncilWorkflowDefinition(risk RiskLevel) WorkflowDefinition {
	return WorkflowDefinition{
		RiskLevels: []RiskLevel{risk},
		Stages: []WorkflowStageDefinition{{
			Key: "council", Name: "Council", Order: 1, Type: WorkflowStageCouncil,
			Council: &CouncilStagePolicy{
				Participants: []WorkflowActorSelector{{Kind: WorkflowSelectorHuman, ID: "human-a"}, {Kind: WorkflowSelectorOntologyRecommended}},
				Quorum:       2, ApprovalThreshold: 2, DeadlineMinutes: 60,
			},
		}},
	}
}
