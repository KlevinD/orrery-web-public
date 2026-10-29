package task

import (
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
)

func TestValidateTaskContractAcceptsCompleteContract(t *testing.T) {
	contract := validTaskContract()

	if errs := ValidateTaskContract(contract); len(errs) != 0 {
		t.Fatalf("ValidateTaskContract returned errors: %#v", errs)
	}
}

func TestValidateTaskContractRequiresGoalAndDeliverable(t *testing.T) {
	contract := TaskContract{
		ID:        TaskID("task-001"),
		MissionID: missioncore.MissionID("mission-001"),
	}

	errs := ValidateTaskContract(contract)

	assertValidationError(t, errs, CodeMissingRequiredField, "goal")
	assertValidationError(t, errs, CodeMissingDeliverable, "expected_deliverables")
}

func TestValidateTaskContractRequiresWorkerGoalForAssignedTask(t *testing.T) {
	contract := validTaskContract()
	contract.WorkerPersonalGoal = ""
	contract.WorkerPersonalGoalSources = nil

	errs := ValidateTaskContract(contract)

	assertValidationError(t, errs, CodeMissingRequiredField, "worker_personal_goal")
}

func TestValidateTaskContractAcceptsUnassignedRootContract(t *testing.T) {
	contract := TaskContract{
		ID:        TaskID("task-root"),
		MissionID: missioncore.MissionID("mission-001"),
		Goal:      "团队目标：交付单 HTML 文件穿墙贪吃蛇",
		ExpectedDeliverables: []ExpectedDeliverable{{
			ID:    ExpectedDeliverableID("expected-html"),
			Title: "可运行的 HTML 文件",
		}},
		ContextPolicy: ContextPolicy{
			RequireMissionGoal:        true,
			RequireDiscussionGoal:     true,
			RequireWorkerPersonalGoal: true,
		},
	}

	if errs := ValidateTaskContract(contract); len(errs) != 0 {
		t.Fatalf("ValidateTaskContract returned errors for unassigned root contract: %#v", errs)
	}
}

func TestValidateTaskContractRejectsToolRefAssignee(t *testing.T) {
	contract := validTaskContract()
	contract.Assignee = TaskAssigneeRef{ToolRef: &agentcore.ToolRef{ToolID: agentcore.ToolID("search")}}

	errs := ValidateTaskContract(contract)

	assertValidationError(t, errs, CodeToolRefNotAgent, "assignee")
}

func TestValidateTaskContractRequiresExactlyOneAgentAssignee(t *testing.T) {
	contract := validTaskContract()
	contract.Assignee = TaskAssigneeRef{
		AgentProfileRef:      &agentcore.AgentProfileRef{ID: agentcore.AgentProfileID("agent-worker-001")},
		MissionAssignmentRef: &MissionAssignmentRef{ID: MissionAssignmentID("assignment-001")},
	}

	errs := ValidateTaskContract(contract)

	assertValidationError(t, errs, CodeInvalidRef, "assignee")
}

func TestValidateTaskContractRejectsScopedRuntimeAssigneeIdentity(t *testing.T) {
	contract := validTaskContract()
	contract.Assignee = TaskAssigneeRef{AgentProfileRef: &agentcore.AgentProfileRef{ID: agentcore.AgentProfileID("agent-worker-001:mission-001")}}

	errs := ValidateTaskContract(contract)

	assertValidationError(t, errs, CodeInvalidRef, "assignee.agent_profile_ref")
}

func TestValidateTaskContractRequiresWorkerGoalDerivation(t *testing.T) {
	contract := validTaskContract()
	contract.WorkerPersonalGoalSources = nil

	errs := ValidateTaskContract(contract)

	assertValidationError(t, errs, CodeMissingRequiredField, "worker_personal_goal_sources")
}

func TestValidateTaskContractRejectsWorkerGoalSourceForUnknownDeliverable(t *testing.T) {
	contract := validTaskContract()
	contract.WorkerPersonalGoalSources = []WorkerPersonalGoalSourceRef{
		{Kind: WorkerPersonalGoalSourceExpectedDeliverable, ExpectedDeliverableRef: ExpectedDeliverableRef{ID: ExpectedDeliverableID("ghost")}},
	}

	errs := ValidateTaskContract(contract)

	assertValidationError(t, errs, CodeInvalidRef, "worker_personal_goal_sources[0].expected_deliverable_id")
}

func TestValidateTaskContractRequiresTraceableDeliverablesAcceptanceAndEvidence(t *testing.T) {
	contract := validTaskContract()
	contract.ExpectedDeliverables = []ExpectedDeliverable{{Description: "missing identity"}}
	contract.AcceptanceCriteria = []AcceptanceCriterion{{Description: ""}}
	contract.EvidenceRequirements = []EvidenceRequirement{{ID: EvidenceRequirementID("evidence-required-001"), Required: true}}

	errs := ValidateTaskContract(contract)

	assertValidationError(t, errs, CodeInvalidRef, "expected_deliverables[0].id")
	assertValidationError(t, errs, CodeMissingRequiredField, "expected_deliverables[0].title")
	assertValidationError(t, errs, CodeInvalidRef, "acceptance_criteria[0].id")
	assertValidationError(t, errs, CodeMissingRequiredField, "acceptance_criteria[0].description")
	assertValidationError(t, errs, CodeInvalidRef, "evidence_requirements[0].kind")
}

func TestValidateTaskContractRejectsInvalidInputDueAndContextPolicy(t *testing.T) {
	contract := validTaskContract()
	contract.Inputs = []InputRef{{ID: "input-001", Kind: InputKind("spreadsheet")}}
	contract.DuePolicy = DuePolicy{Policy: DuePolicyKind("whenever")}
	contract.ContextPolicy = ContextPolicy{}

	errs := ValidateTaskContract(contract)

	assertValidationError(t, errs, CodeInvalidRef, "inputs[0].kind")
	assertValidationError(t, errs, CodeInvalidRef, "due_policy.policy")
	assertValidationError(t, errs, CodeInvalidRef, "context_policy")
}

func TestValidateTaskContractRequiresDueAtForBoundedDuePolicies(t *testing.T) {
	contract := validTaskContract()
	contract.DuePolicy = DuePolicy{Policy: DuePolicyFixed}

	errs := ValidateTaskContract(contract)

	assertValidationError(t, errs, CodeMissingRequiredField, "due_policy.due_at")
}

func TestValidateTaskContractRequiresExplicitUniqueExecutionPermissionScopes(t *testing.T) {
	contract := validTaskContract()
	contract.ExecutionPermissionScopes = []string{"todo.read", "", "todo.read", "*", "todo write"}

	errs := ValidateTaskContract(contract)

	assertValidationError(t, errs, CodeInvalidRef, "execution_permission_scopes[1].scope")
	assertValidationError(t, errs, CodeInvalidRef, "execution_permission_scopes[2].scope")
	assertValidationError(t, errs, CodeInvalidRef, "execution_permission_scopes[3].scope")
	assertValidationError(t, errs, CodeInvalidRef, "execution_permission_scopes[4].scope")
}

func TestTaskContractRequiresExplicitToolScopesForAutoplanSource(t *testing.T) {
	contract := validTaskContract()
	contract.SourceKind = SourceKindResponsibilityAutoplan
	if !contract.RequiresExplicitToolScopes() {
		t.Fatal("responsibility autoplan source must require explicit tool scopes")
	}

	contract.SourceKind = "manual"
	if contract.RequiresExplicitToolScopes() {
		t.Fatal("ordinary task unexpectedly requires explicit tool scopes")
	}
	contract.RequireExplicitToolScopes = true
	if !contract.RequiresExplicitToolScopes() {
		t.Fatal("durable explicit tool scope marker was not honored")
	}
}

func TestEvidenceRequirementKindsCoverEvidenceSources(t *testing.T) {
	kinds := []EvidenceRequirementKind{
		EvidenceRequirementKindArtifact,
		EvidenceRequirementKindToolLog,
		EvidenceRequirementKindTestLog,
		EvidenceRequirementKindExternalURL,
		EvidenceRequirementKindApprovalRecord,
		EvidenceRequirementKindHumanSubmission,
	}

	for _, kind := range kinds {
		if !kind.IsValid() {
			t.Fatalf("expected valid evidence requirement kind: %s", kind)
		}
	}
}

func TestTaskConflictErrorCarriesStableCodeAndFields(t *testing.T) {
	err := NewConflictError("task_contract_conflict", "task_id", "task contract changed")

	if err.Code != CodeConflict || err.ConflictCode != "task_contract_conflict" || err.Field != "task_id" {
		t.Fatalf("unexpected conflict error: %#v", err)
	}
}

func validTaskContract() TaskContract {
	return TaskContract{
		ID:                 TaskID("task-001"),
		MissionID:          missioncore.MissionID("mission-001"),
		Assignee:           TaskAssigneeRef{AgentProfileRef: &agentcore.AgentProfileRef{ID: agentcore.AgentProfileID("agent-worker-001")}},
		Title:              "Implement core contracts",
		Goal:               "Create pure mission and agent contract packages",
		WorkerPersonalGoal: "Implement the contract model and validation rules",
		WorkerPersonalGoalSources: []WorkerPersonalGoalSourceRef{
			{Kind: WorkerPersonalGoalSourceGoal, ContractFieldRef: ContractFieldRef{Field: "goal"}},
			{Kind: WorkerPersonalGoalSourceScope, ContractFieldRef: ContractFieldRef{Field: "scope"}},
			{Kind: WorkerPersonalGoalSourceExpectedDeliverable, ExpectedDeliverableRef: ExpectedDeliverableRef{ID: ExpectedDeliverableID("expected-001")}},
		},
		Scope:  "internal/agent/core and internal/mission",
		Inputs: []InputRef{{ID: "goal-readme", Kind: InputKindDocument}},
		ExpectedDeliverables: []ExpectedDeliverable{{
			ID:          ExpectedDeliverableID("expected-001"),
			Title:       "Contract packages",
			Description: "Go packages with pure contract types",
		}},
		AcceptanceCriteria: []AcceptanceCriterion{{
			ID:          AcceptanceCriterionID("acceptance-001"),
			Description: "go test passes for core contract packages",
		}},
		EvidenceRequirements: []EvidenceRequirement{{
			ID:          EvidenceRequirementID("evidence-required-001"),
			Description: "test log proving validation behavior",
			Kind:        EvidenceRequirementKindTestLog,
			Required:    true,
		}},
		Dependencies: []TaskDependencyRef{{TaskID: TaskID("task-000")}},
		Constraints:  []Constraint{{Description: "do not import runtime or stores"}},
		DuePolicy:    DuePolicy{Policy: DuePolicyBestEffort},
		ContextPolicy: ContextPolicy{
			RequireMissionGoal:        true,
			RequireDiscussionGoal:     true,
			RequireWorkerPersonalGoal: true,
		},
	}
}
