package main

import (
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestProductionAgentValidatorsRequireDurableContractEvidence(t *testing.T) {
	input, result := validProductionAgentValidationFixture()
	result.Evidence = nil

	err := productionEvidenceResultValidator{}.Validate(input, result)
	var validationErr agentcore.ResultValidationError
	if !errors.As(err, &validationErr) || !hasProductionValidationCode(validationErr.Errors, agentcore.CodeMissingRequiredEvidence) {
		t.Fatalf("missing evidence error = %T %[1]v", err)
	}
}

func TestProductionAgentPolicyRejectsSyntheticRunArtifacts(t *testing.T) {
	input, result := validProductionAgentValidationFixture()
	result.Deliverables[0].ArtifactRef.URI = "agent-run://run-1/output"
	result.Evidence[0].ArtifactRef.URI = "agent-run://run-1/output"

	err := productionPolicyResultValidator{}.Validate(input, result)
	var validationErr agentcore.ResultValidationError
	if !errors.As(err, &validationErr) || len(validationErr.Errors) < 2 {
		t.Fatalf("synthetic artifact error = %T %[1]v", err)
	}
}

func TestProductionAgentValidatorsAcceptExactDurableContractResult(t *testing.T) {
	input, result := validProductionAgentValidationFixture()
	for name, validator := range map[string]agentcore.ResultValidator{
		"evidence": productionEvidenceResultValidator{},
		"qa":       productionQAResultValidator{},
		"policy":   productionPolicyResultValidator{},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validator.Validate(input, result); err != nil {
				t.Fatalf("validate: %v", err)
			}
		})
	}
}

func TestProductionAgentValidatorsAcceptBlockedResultWithoutCompletedWork(t *testing.T) {
	input, result := validProductionAgentValidationFixture()
	result.Summary.Text = "Visual quality review rejected the cover crop; no approval was submitted."
	result.Blocker = &agentcore.Blocker{Summary: result.Summary.Text}
	result.Deliverables = nil
	result.Evidence = nil
	for name, validator := range map[string]agentcore.ResultValidator{
		"evidence": productionEvidenceResultValidator{},
		"qa":       productionQAResultValidator{},
		"policy":   productionPolicyResultValidator{},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validator.Validate(input, result); err != nil {
				t.Fatalf("blocked result must not require completed work: %#v", err)
			}
		})
	}
}

func TestProductionBlockedResultRetainsEvidenceAndIdentityValidation(t *testing.T) {
	for name, mutate := range map[string]func(*agentcore.AgentResult){
		"wrong mission":           func(r *agentcore.AgentResult) { r.MissionID = "other" },
		"wrong task":              func(r *agentcore.AgentResult) { r.TaskID = "other" },
		"wrong agent":             func(r *agentcore.AgentResult) { r.AgentID = "other" },
		"wrong run":               func(r *agentcore.AgentResult) { r.RunID = "other" },
		"missing trace":           func(r *agentcore.AgentResult) { r.InternalTraceRef = "" },
		"missing summary":         func(r *agentcore.AgentResult) { r.Summary.Text = "" },
		"missing blocker reason":  func(r *agentcore.AgentResult) { r.Blocker.Summary = " " },
		"unknown evidence":        func(r *agentcore.AgentResult) { r.Evidence[0].EvidenceRequirementID = "other" },
		"unknown criterion":       func(r *agentcore.AgentResult) { r.Evidence[0].AcceptanceCriterionID = "other" },
		"wrong evidence kind":     func(r *agentcore.AgentResult) { r.Evidence[0].Kind = agentcore.EvidenceKindToolLog },
		"untraceable evidence":    func(r *agentcore.AgentResult) { r.Evidence[0].ArtifactRef = agentcore.ArtifactRef{} },
		"synthetic evidence":      func(r *agentcore.AgentResult) { r.Evidence[0].ArtifactRef.URI = "agent-run://run-1/output" },
		"untraceable deliverable": func(r *agentcore.AgentResult) { r.Deliverables[0].ArtifactRef = agentcore.ArtifactRef{} },
		"unknown deliverable":     func(r *agentcore.AgentResult) { r.Deliverables[0].ExpectedDeliverableID = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			input, result := validProductionAgentValidationFixture()
			result.Blocker = &agentcore.Blocker{Summary: "Quality review failed."}
			mutate(&result)
			for _, validator := range []agentcore.ResultValidator{productionEvidenceResultValidator{}, productionQAResultValidator{}, productionPolicyResultValidator{}} {
				if validator.Validate(input, result) != nil {
					return
				}
			}
			t.Fatal("blocked outcome must not bypass validation of supplied work or runtime identity")
		})
	}
}

func validProductionAgentValidationFixture() (agentcore.AgentInput, agentcore.AgentResult) {
	input := agentcore.AgentInput{
		RunID: "run-1", TenantID: "tenant-1",
		ContextPack: agentcore.ContextPack{MissionID: "mission-1", TaskID: "task-1", AgentID: "agent-1"},
		Task: agentcore.TaskInput{
			ID: "task-1", MissionID: "mission-1", Goal: "Ship the result", WorkerPersonalGoal: "Ship the result",
			AcceptanceCriteria:   []agentcore.TaskAcceptanceInput{{ID: "acceptance-1", Description: "Focused tests pass"}},
			EvidenceRequirements: []agentcore.TaskEvidenceInput{{ID: "evidence-1", Description: "Focused test log", Kind: string(agentcore.EvidenceKindTestLog), Required: true}},
			ExpectedDeliverables: []agentcore.TaskDeliverableInput{{ID: "deliverable-1", Title: "Patch"}},
		},
	}
	result := agentcore.AgentResult{
		ID: "result-1", MissionID: "mission-1", TaskID: "task-1", AgentID: "agent-1", RunID: "run-1",
		Summary: agentcore.Summary{Text: "Implemented and verified."}, InternalTraceRef: "artifact://trace-1",
		Deliverables: []agentcore.Deliverable{{
			ID: "delivered-1", ExpectedDeliverableID: "deliverable-1", Title: "Patch",
			ArtifactRef: agentcore.ArtifactRef{ID: "artifact-patch", Kind: agentcore.ArtifactKindCode, URI: "artifact://artifact-patch"},
		}},
		Evidence: []agentcore.Evidence{{
			ID: "evidence-result-1", EvidenceRequirementID: "evidence-1", AcceptanceCriterionID: "acceptance-1", Kind: agentcore.EvidenceKindTestLog,
			ArtifactRef: agentcore.ArtifactRef{ID: "artifact-test-log", Kind: agentcore.ArtifactKindLog, URI: "artifact://artifact-test-log"},
		}},
	}
	return input, result
}

func hasProductionValidationCode(errors []agentcore.ValidationError, code agentcore.ValidationCode) bool {
	for _, item := range errors {
		if item.Code == code {
			return true
		}
	}
	return false
}
