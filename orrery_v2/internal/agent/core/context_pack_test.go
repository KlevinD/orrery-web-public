package core

import (
	"testing"

	missioncore "orrery_v2/internal/mission/core"
)

func TestValidateContextPackRequiresSharedGoalsAndSources(t *testing.T) {
	pack := validContextPack()

	if errs := ValidateContextPack(pack); len(errs) != 0 {
		t.Fatalf("ValidateContextPack returned errors: %#v", errs)
	}
}

func TestValidateContextPackRequiresWorkerPersonalGoalForTasks(t *testing.T) {
	pack := validContextPack()
	pack.WorkerPersonalGoal = ""

	errs := ValidateContextPack(pack)

	assertValidationError(t, errs, CodeMissingRequiredField, "worker_personal_goal")
}

func TestValidateContextPackRejectsFullMemoryInjection(t *testing.T) {
	pack := validContextPack()
	pack.RawMemory = "full memory transcript"

	errs := ValidateContextPack(pack)

	assertValidationError(t, errs, CodeMemoryDigestRequired, "raw_memory")
}

func TestValidateContextPackRequiresProtectedSourcePrecedence(t *testing.T) {
	pack := validContextPack()
	pack.SourceRefs = []ContextSourceRef{{
		Ref:       ContextSourceTypedRef{TaskContractRef: &TaskContractRef{ID: "task-001"}},
		Kind:      ContextSourceKindTaskContract,
		Priority:  SourcePriorityBackground,
		Immutable: true,
	}}

	errs := ValidateContextPack(pack)

	assertValidationError(t, errs, CodeInvalidRef, "source_refs")
}

func TestValidateContextPackRejectsOverridingProtectedSources(t *testing.T) {
	pack := validContextPack()
	pack.OverridePolicy = OverridePolicyAllowContextOverride

	errs := ValidateContextPack(pack)

	assertValidationError(t, errs, CodeInvalidRef, "override_policy")
}

func TestValidateContextPackRequiresStrongSourceRefs(t *testing.T) {
	pack := validContextPack()
	pack.SourceRefs = []ContextSourceRef{{
		Kind:      ContextSourceKindTaskContract,
		Priority:  SourcePriorityContract,
		Immutable: true,
	}}

	errs := ValidateContextPack(pack)

	assertValidationError(t, errs, CodeInvalidRef, "source_refs[0].ref")
}

func TestValidateContextPackRejectsAmbiguousSourceRefs(t *testing.T) {
	pack := validContextPack()
	pack.SourceRefs = []ContextSourceRef{{
		Ref: ContextSourceTypedRef{
			TaskContractRef: &TaskContractRef{ID: "task-001"},
			MissionEventRef: &MissionEventRef{ID: MissionEventID("event-001")},
		},
		Kind:      ContextSourceKindTaskContract,
		Priority:  SourcePriorityContract,
		Immutable: true,
	}}

	errs := ValidateContextPack(pack)

	assertValidationError(t, errs, CodeInvalidRef, "source_refs[0].ref")
}

func TestValidateContextPackRequiresCompleteResponsibilityAuthority(t *testing.T) {
	pack := validContextPack()
	pack.ResponsibilityContext = &ResponsibilityContext{
		ContextID: "context-1", ResponsibilityID: "responsibility-1", ResponsibilityVersion: 1,
		PolicyID: "policy-1", PolicyVersion: 1, AgentProfileID: "profile-1",
	}
	if errs := ValidateContextPack(pack); len(errs) != 0 {
		t.Fatalf("complete responsibility authority errors = %+v", errs)
	}

	pack.ResponsibilityContext.PolicyID = ""
	assertValidationError(t, ValidateContextPack(pack), CodeInvalidRef, "responsibility_context.policy")
	pack.ResponsibilityContext.PolicyID = "policy-1"
	pack.ResponsibilityContext.AgentProfileID = ""
	assertValidationError(t, ValidateContextPack(pack), CodeInvalidRef, "responsibility_context.agent_profile_id")
}

func validContextPack() ContextPack {
	return ContextPack{
		ID:                     ContextPackID("context-001"),
		MissionID:              missioncore.MissionID("mission-001"),
		DiscussionGroupID:      DiscussionGroupID("discussion-001"),
		AgentID:                AgentID("agent-worker-001"),
		TaskID:                 missioncore.TaskID("task-001"),
		MissionFinalGoal:       "Ship mission control",
		DiscussionGroupGoal:    "Coordinate implementation",
		WorkerPersonalGoal:     "Implement core contracts",
		TaskContractRef:        TaskContractRef{ID: "task-001"},
		AcceptanceCriteriaRefs: []AcceptanceCriterionRef{{ID: "acceptance-001"}},
		RequiredEvidenceRefs:   []EvidenceRequirementRef{{ID: "evidence-001"}},
		PersonalMemoryDigestRef: MemoryDigestRef{
			ID:         MemoryDigestID("digest-personal-001"),
			MemoryKind: MemoryKindPersonal,
		},
		CollectiveMemoryDigestRefs: []MemoryDigestRef{{
			ID:         MemoryDigestID("digest-team-001"),
			MemoryKind: MemoryKindCollective,
		}},
		DiscussionDigestRef: DiscussionDigestRef{ID: DiscussionDigestID("discussion-digest-001")},
		PermissionScope:     "task",
		ToolScope:           "repo",
		SourceRefs: []ContextSourceRef{
			{Ref: ContextSourceTypedRef{UserInstructionRef: &UserInstructionRef{ID: UserInstructionID("source-user-001")}}, Kind: ContextSourceKindUserInstruction, Priority: SourcePriorityUserInstruction, Immutable: true},
			{Ref: ContextSourceTypedRef{TaskContractRef: &TaskContractRef{ID: "task-001"}}, Kind: ContextSourceKindTaskContract, Priority: SourcePriorityContract, Immutable: true},
		},
		OverridePolicy: OverridePolicyPreserveProtected,
	}
}
