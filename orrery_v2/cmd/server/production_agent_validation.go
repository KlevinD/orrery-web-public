package main

import (
	"fmt"
	"strings"

	deliveryvalidation "orrery_v2/internal/agent/component/delivery"
	evidencevalidation "orrery_v2/internal/agent/component/evidence"
	qavalidation "orrery_v2/internal/agent/component/qa"
	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
	missiontask "orrery_v2/internal/mission/task"
)

type productionEvidenceResultValidator struct{}

func (productionEvidenceResultValidator) Validate(input agentcore.AgentInput, result agentcore.AgentResult) error {
	contract := missionTaskContractFromAgentInput(input)
	if result.Blocker != nil {
		// Blocked work may be incomplete; any supplied evidence must remain valid.
		contract.AcceptanceCriteria = nil
		for index := range contract.EvidenceRequirements {
			contract.EvidenceRequirements[index].Required = false
		}
	}
	issues := evidencevalidation.NewValidator(contract.EvidenceRequirements, contract.AcceptanceCriteria).Validate(result.Evidence)
	if len(issues) == 0 {
		return nil
	}
	errs := make([]agentcore.ValidationError, 0, len(issues))
	for _, issue := range issues {
		code := agentcore.CodeInvalidRef
		switch issue.Code {
		case evidencevalidation.CodeMissingRequiredEvidence:
			code = agentcore.CodeMissingRequiredEvidence
		case evidencevalidation.CodeMissingAcceptanceMapping:
			code = agentcore.CodeMissingAcceptanceMapping
		}
		errs = append(errs, agentcore.ValidationError{Code: code, Field: issue.Field, Message: issue.Message})
	}
	return agentcore.ResultValidationError{Errors: errs}
}

type productionQAResultValidator struct{}

func (productionQAResultValidator) Validate(input agentcore.AgentInput, result agentcore.AgentResult) error {
	if result.Blocker != nil {
		return nil
	}
	contract := missionTaskContractFromAgentInput(input)
	if len(contract.AcceptanceCriteria) == 0 && len(contract.EvidenceRequirements) == 0 {
		return nil
	}
	required := make([]missiontask.EvidenceRequirement, 0, len(contract.EvidenceRequirements))
	for _, requirement := range contract.EvidenceRequirements {
		if requirement.Required {
			required = append(required, requirement)
		}
	}
	checker := qavalidation.NewChecker(qavalidation.QAContract{
		ID:                 "runtime-result-" + qavalidation.QAContractID(input.Task.ID),
		AcceptanceCriteria: contract.AcceptanceCriteria, EvidenceRequirements: required,
	}, qavalidation.QAPolicy{EvidenceStrictness: qavalidation.EvidenceStrictnessRequired, RequireReasonOnReject: true})
	checked := checker.Check(qavalidation.QACheckInput{
		TaskID: missiontask.TaskID(input.Task.ID), SubmissionID: result.ID.String(), CheckedBy: result.AgentID, Evidence: result.Evidence,
	})
	if checked.Status == qavalidation.QAStatusApproved {
		return nil
	}
	message := strings.Join(checked.RejectionReasons, "; ")
	if strings.TrimSpace(message) == "" {
		message = "runtime result did not satisfy the task QA contract"
	}
	return agentcore.ResultValidationError{Errors: []agentcore.ValidationError{{Code: agentcore.CodeConflict, Field: "qa", Message: message}}}
}

type productionPolicyResultValidator struct{}

func (productionPolicyResultValidator) Validate(input agentcore.AgentInput, result agentcore.AgentResult) error {
	contract := missionTaskContractFromAgentInput(input)
	errs := agentcore.ValidateAgentResult(result)
	expectedMissionID := missioncore.MissionID(firstNonEmptyText(input.ContextPack.MissionID.String(), input.Task.MissionID))
	expectedTaskID := missioncore.TaskID(firstNonEmptyText(input.ContextPack.TaskID.String(), input.Task.ID))
	expectedAgentID := input.ContextPack.AgentID
	if result.MissionID != expectedMissionID {
		errs = append(errs, productionResultIssue("mission_id", "result mission does not match the runtime task"))
	}
	if result.TaskID != expectedTaskID {
		errs = append(errs, productionResultIssue("task_id", "result task does not match the runtime task"))
	}
	if result.AgentID != expectedAgentID {
		errs = append(errs, productionResultIssue("agent_id", "result agent does not match the running agent"))
	}
	if result.RunID != input.RunID {
		errs = append(errs, productionResultIssue("run_id", "result run does not match the runtime invocation"))
	}
	if strings.TrimSpace(result.InternalTraceRef) == "" {
		errs = append(errs, productionResultIssue("internal_trace_ref", "result must reference its persisted runtime trace"))
	}

	for _, issue := range deliveryvalidation.NewValidator(contract).Validate(result) {
		code := agentcore.CodeInvalidRef
		switch issue.Code {
		case deliveryvalidation.CodeMissingSummary:
			code = agentcore.CodeMissingSummary
		case deliveryvalidation.CodeMissingExpectedDeliverable:
			code = agentcore.CodeMissingDeliverable
		}
		errs = append(errs, agentcore.ValidationError{Code: code, Field: issue.Field, Message: issue.Message})
	}
	expectedEvidence := map[string]bool{}
	for _, item := range input.Task.EvidenceRequirements {
		expectedEvidence[strings.TrimSpace(item.ID)] = true
	}
	expectedCriteria := map[string]bool{}
	for _, item := range input.Task.AcceptanceCriteria {
		expectedCriteria[strings.TrimSpace(item.ID)] = true
	}
	for index, item := range result.Evidence {
		field := fmt.Sprintf("evidence[%d]", index)
		if len(expectedEvidence) > 0 && !expectedEvidence[strings.TrimSpace(item.EvidenceRequirementID)] {
			errs = append(errs, productionResultIssue(field+".evidence_requirement_id", "evidence does not map to the task contract"))
		}
		if len(expectedCriteria) > 0 && !expectedCriteria[strings.TrimSpace(item.AcceptanceCriterionID)] {
			errs = append(errs, productionResultIssue(field+".acceptance_criterion_id", "evidence does not map to an acceptance criterion"))
		}
		if artifactRefUsesSyntheticRunURI(item.ArtifactRef) {
			errs = append(errs, productionResultIssue(field+".artifact_ref", "synthetic agent-run artifact references are not durable evidence"))
		}
	}
	for index, item := range result.Deliverables {
		if artifactRefUsesSyntheticRunURI(item.ArtifactRef) {
			errs = append(errs, productionResultIssue(fmt.Sprintf("deliverables[%d].artifact_ref", index), "synthetic agent-run artifact references are not durable deliverables"))
		}
	}
	if len(errs) > 0 {
		return agentcore.ResultValidationError{Errors: errs}
	}
	return nil
}

func missionTaskContractFromAgentInput(input agentcore.AgentInput) missiontask.TaskContract {
	contract := missiontask.TaskContract{
		ID: missiontask.TaskID(input.Task.ID), MissionID: missioncore.MissionID(input.Task.MissionID),
		Goal: input.Task.Goal, WorkerPersonalGoal: input.Task.WorkerPersonalGoal,
	}
	for _, item := range input.Task.AcceptanceCriteria {
		contract.AcceptanceCriteria = append(contract.AcceptanceCriteria, missiontask.AcceptanceCriterion{ID: missiontask.AcceptanceCriterionID(item.ID), Description: item.Description})
	}
	for _, item := range input.Task.EvidenceRequirements {
		contract.EvidenceRequirements = append(contract.EvidenceRequirements, missiontask.EvidenceRequirement{
			ID: missiontask.EvidenceRequirementID(item.ID), Description: item.Description,
			Kind: missiontask.EvidenceRequirementKind(item.Kind), Required: item.Required,
		})
	}
	for _, item := range input.Task.ExpectedDeliverables {
		contract.ExpectedDeliverables = append(contract.ExpectedDeliverables, missiontask.ExpectedDeliverable{ID: missiontask.ExpectedDeliverableID(item.ID), Title: item.Title})
	}
	return contract
}

func productionResultIssue(field, message string) agentcore.ValidationError {
	return agentcore.ValidationError{Code: agentcore.CodeInvalidRef, Field: field, Message: message}
}

func artifactRefUsesSyntheticRunURI(ref agentcore.ArtifactRef) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(ref.URI)), "agent-run://")
}
