package hil

import (
	"fmt"
	"strings"
)

func ValidateWorkflowDefinition(definition WorkflowDefinition) error {
	if len(definition.RiskLevels) == 0 {
		return invalidWorkflowDefinition("at least one risk level is required")
	}
	highRisk := false
	seenRisks := map[RiskLevel]bool{}
	for _, risk := range definition.RiskLevels {
		switch risk {
		case RiskLow, RiskMedium:
		case RiskHigh, RiskCritical:
			highRisk = true
		default:
			return invalidWorkflowDefinition("unsupported risk level %q", risk)
		}
		if seenRisks[risk] {
			return invalidWorkflowDefinition("duplicate risk level %q", risk)
		}
		seenRisks[risk] = true
	}
	if len(definition.Stages) == 0 {
		return invalidWorkflowDefinition("at least one stage is required")
	}
	if definition.RequireDistinctApprovers {
		approvalStages := 0
		for _, stage := range definition.Stages {
			if stage.Type == WorkflowStageApproval {
				approvalStages++
			}
		}
		if approvalStages < 2 {
			return invalidWorkflowDefinition("distinct approvers require at least two approval stages")
		}
	}
	seenStageKeys := map[string]bool{}
	for index, stage := range definition.Stages {
		key := strings.TrimSpace(stage.Key)
		if key == "" || strings.TrimSpace(stage.Name) == "" {
			return invalidWorkflowDefinition("stage %d requires a key and name", index+1)
		}
		if seenStageKeys[key] {
			return invalidWorkflowDefinition("duplicate stage key %q", key)
		}
		seenStageKeys[key] = true
		if stage.Order != index+1 {
			return invalidWorkflowDefinition("stage %q order must be %d", key, index+1)
		}
		switch stage.Type {
		case WorkflowStageApproval:
			if stage.Approval == nil || stage.Council != nil {
				return invalidWorkflowDefinition("approval stage %q has invalid policy", key)
			}
			if err := validateApprovalStagePolicy(key, *stage.Approval); err != nil {
				return err
			}
		case WorkflowStageCouncil:
			if stage.Council == nil || stage.Approval != nil {
				return invalidWorkflowDefinition("council stage %q has invalid policy", key)
			}
			if err := validateCouncilStagePolicy(key, *stage.Council); err != nil {
				return err
			}
		default:
			return invalidWorkflowDefinition("stage %q has unsupported type %q", key, stage.Type)
		}
	}
	if highRisk && definition.Stages[len(definition.Stages)-1].Type != WorkflowStageApproval {
		return invalidWorkflowDefinition("high-risk workflows must end with human approval")
	}
	return nil
}

func validateApprovalStagePolicy(stageKey string, policy ApprovalStagePolicy) error {
	if policy.DeadlineMinutes <= 0 {
		return invalidWorkflowDefinition("approval stage %q requires a deadline", stageKey)
	}
	if len(policy.Assignees) == 0 {
		return invalidWorkflowDefinition("approval stage %q requires assignees", stageKey)
	}
	for _, selector := range policy.Assignees {
		if err := validateWorkflowSelector(selector, false); err != nil {
			return invalidWorkflowDefinition("approval stage %q: %v", stageKey, err)
		}
	}
	switch policy.Strategy {
	case ApprovalSingle:
		if len(policy.Assignees) != 1 {
			return invalidWorkflowDefinition("single approval stage %q requires one assignee selector", stageKey)
		}
	case ApprovalAny, ApprovalAll:
	case ApprovalThreshold:
		if policy.Threshold <= 0 || policy.Threshold > len(policy.Assignees) {
			return invalidWorkflowDefinition("approval stage %q threshold is out of bounds", stageKey)
		}
	default:
		return invalidWorkflowDefinition("approval stage %q has unsupported strategy %q", stageKey, policy.Strategy)
	}
	seenReminders := map[int]bool{}
	for _, offset := range policy.ReminderOffsetsMinutes {
		if offset <= 0 || offset >= policy.DeadlineMinutes || seenReminders[offset] {
			return invalidWorkflowDefinition("approval stage %q has invalid reminder offset", stageKey)
		}
		seenReminders[offset] = true
	}
	for index, rung := range policy.Escalation {
		if len(rung.Actors) != 1 {
			return invalidWorkflowDefinition("approval stage %q escalation rung %d requires exactly one actor", stageKey, index+1)
		}
		if rung.DeadlineMinutes <= 0 {
			return invalidWorkflowDefinition("approval stage %q escalation rung %d requires a deadline", stageKey, index+1)
		}
		if err := validateWorkflowSelector(rung.Actors[0], false); err != nil {
			return invalidWorkflowDefinition("approval stage %q escalation rung %d: %v", stageKey, index+1, err)
		}
	}
	return nil
}

func validateCouncilStagePolicy(stageKey string, policy CouncilStagePolicy) error {
	if policy.DeadlineMinutes <= 0 {
		return invalidWorkflowDefinition("council stage %q requires a deadline", stageKey)
	}
	if len(policy.Participants) == 0 {
		return invalidWorkflowDefinition("council stage %q requires participants", stageKey)
	}
	for _, selector := range policy.Participants {
		if err := validateWorkflowSelector(selector, true); err != nil {
			return invalidWorkflowDefinition("council stage %q: %v", stageKey, err)
		}
	}
	if policy.Quorum <= 0 || policy.Quorum > len(policy.Participants) {
		return invalidWorkflowDefinition("council stage %q quorum is out of bounds", stageKey)
	}
	if policy.ApprovalThreshold <= 0 || policy.ApprovalThreshold > policy.Quorum {
		return invalidWorkflowDefinition("council stage %q approval threshold is out of bounds", stageKey)
	}
	return nil
}

func validateWorkflowSelector(selector WorkflowActorSelector, allowOntology bool) error {
	switch selector.Kind {
	case WorkflowSelectorHuman, WorkflowSelectorPeopleRole, WorkflowSelectorOrganizationPosition:
		if strings.TrimSpace(selector.ID) == "" {
			return fmt.Errorf("selector %q requires an id", selector.Kind)
		}
	case WorkflowSelectorRequesterManager, WorkflowSelectorResourceOwner:
	case WorkflowSelectorOntologyRecommended:
		if !allowOntology {
			return fmt.Errorf("ontology-recommended selectors are council-only")
		}
	default:
		return fmt.Errorf("unsupported selector kind %q", selector.Kind)
	}
	return nil
}

func invalidWorkflowDefinition(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidRequest, fmt.Sprintf(format, args...))
}
