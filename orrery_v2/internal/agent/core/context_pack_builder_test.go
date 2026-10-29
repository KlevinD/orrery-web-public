package core

import "testing"

func TestContextPackBuilderBuildsCompleteDigestOnlyPack(t *testing.T) {
	builder := NewContextPackBuilder()
	pack, err := builder.Build(ContextPackBuildRequest{
		ContextPackID:        "ctx-1",
		RunID:                "run-1",
		MissionID:            "mission-1",
		DiscussionGroupID:    "discussion-1",
		DiscussionGroupGoal:  "Coordinate runtime implementation",
		MissionFinalGoal:     "Ship production Stage 7",
		AgentID:              "agent-1",
		AgentOrganization:    validOrgIdentity(),
		AgentMissionRole:     "runtime engineer",
		WorkerPersonalGoal:   "Build context and execenv",
		Task:                 sampleTaskInput(),
		PersonalMemoryDigest: MemoryDigestRef{ID: "mem-personal", MemoryKind: MemoryKindPersonal, SummaryRef: "prefers tests"},
		CollectiveMemoryDigests: []MemoryDigestRef{
			{ID: "mem-team", MemoryKind: MemoryKindProject, SummaryRef: "stage contracts"},
		},
		DiscussionDigest:      DiscussionDigestRef{ID: "discussion-digest-1", SummaryRef: "latest review notes"},
		Tools:                 []RuntimeToolBinding{{ToolID: "shell", Alias: "sh", Summary: "Run commands", GrantedPermissions: []string{"test"}, SecretEnv: map[string]string{"API_TOKEN": "secret/ref"}}},
		Skills:                []RuntimeSkillBinding{{SkillID: "tdd", Alias: "tdd", Version: "1.0.0", Summary: "Write tests first"}},
		PermissionConstraints: []string{"no raw secrets"},
	})
	if err != nil {
		t.Fatalf("build context pack: %v", err)
	}

	if pack.MissionFinalGoal != "Ship production Stage 7" || pack.DiscussionGroupGoal == "" || pack.WorkerPersonalGoal == "" {
		t.Fatalf("goals not populated: %#v", pack)
	}
	if pack.TaskContractSummary == "" || len(pack.AcceptanceCriteria) != 2 || len(pack.RequiredEvidence) != 2 {
		t.Fatalf("task contract fields missing: %#v", pack)
	}
	if pack.PersonalMemoryDigestSummary != "prefers tests" || len(pack.CollectiveMemoryDigestSummaries) != 1 || pack.DiscussionDigestSummary == "" {
		t.Fatalf("digest summaries missing: %#v", pack)
	}
	if len(pack.ToolSummaries) != 1 || pack.ToolSummaries[0].SecretEnv["API_TOKEN"] != "secret/ref" {
		t.Fatalf("tool summaries missing secret refs: %#v", pack.ToolSummaries)
	}
	if len(pack.SkillSummaries) != 1 || pack.SkillSummaries[0].Summary == "" {
		t.Fatalf("skill summaries missing: %#v", pack.SkillSummaries)
	}
	if pack.RawMemory != "" || len(pack.RawDiscussionMessages) != 0 {
		t.Fatalf("raw context leaked into pack: %#v", pack)
	}
	if errs := ValidateContextPack(pack); len(errs) != 0 {
		t.Fatalf("ValidateContextPack returned errors: %#v", errs)
	}
	assertSource(t, pack.SourceRefs, ContextSourceKindTaskContract, true)
	assertSource(t, pack.SourceRefs, ContextSourceKindMemoryDigest, false)
	assertSource(t, pack.SourceRefs, ContextSourceKindDiscussionDigest, false)
}

func TestContextPackBuilderRejectsRawMemoryAndDiscussionMessages(t *testing.T) {
	builder := NewContextPackBuilder()
	_, err := builder.Build(ContextPackBuildRequest{
		ContextPackID:       "ctx-1",
		MissionFinalGoal:    "Ship",
		DiscussionGroupGoal: "Coordinate",
		AgentID:             "agent-1",
		Task:                sampleTaskInput(),
		RawMemory:           "full raw memory row",
	})
	if err == nil {
		t.Fatalf("expected raw memory rejection")
	}
	_, err = builder.Build(ContextPackBuildRequest{
		ContextPackID:           "ctx-1",
		MissionFinalGoal:        "Ship",
		DiscussionGroupGoal:     "Coordinate",
		AgentID:                 "agent-1",
		Task:                    sampleTaskInput(),
		RawDiscussionMessages:   []string{"full discussion message"},
		PersonalMemoryDigest:    MemoryDigestRef{ID: "mem-personal", MemoryKind: MemoryKindPersonal, SummaryRef: "digest"},
		DiscussionDigest:        DiscussionDigestRef{ID: "discussion-digest-1", SummaryRef: "digest"},
		AgentOrganization:       validOrgIdentity(),
		PermissionConstraints:   []string{"bounded"},
		WorkerPersonalGoal:      "Build",
		AgentMissionRole:        "engineer",
		DiscussionGroupID:       "discussion-1",
		CollectiveMemoryDigests: nil,
	})
	if err == nil {
		t.Fatalf("expected raw discussion rejection")
	}
}

func TestContextPackBuilderUsesPolicyProvider(t *testing.T) {
	policy := &fixedContextPackPolicy{
		permissionConstraints: []string{"deploy.write"},
		sourceRefs: []ContextSourceRef{{
			Kind:      ContextSourceKindUserInstruction,
			Ref:       ContextSourceTypedRef{UserInstructionRef: &UserInstructionRef{ID: "instruction-1"}},
			Priority:  SourcePriorityUserInstruction,
			Immutable: true,
		}},
		overridePolicy: OverridePolicyPreserveProtected,
	}
	builder := NewContextPackBuilderWithPolicy(policy)
	pack, err := builder.Build(ContextPackBuildRequest{
		ContextPackID:       "ctx-1",
		RunID:               "run-1",
		MissionID:           "mission-1",
		DiscussionGroupID:   "discussion-1",
		DiscussionGroupGoal: "Coordinate runtime implementation",
		MissionFinalGoal:    "Ship production Stage 7",
		AgentID:             "agent-1",
		AgentOrganization:   validOrgIdentity(),
		AgentMissionRole:    "runtime engineer",
		WorkerPersonalGoal:  "Build context and execenv",
		Task:                sampleTaskInput(),
		Tools:               []RuntimeToolBinding{{ToolID: "shell", Summary: "Run commands"}},
	})
	if err != nil {
		t.Fatalf("build context pack with policy: %v", err)
	}
	if pack.PermissionConstraints[0] != "deploy.write" || pack.PermissionScope != "deploy.write" {
		t.Fatalf("permission constraints were not policy-provided: %#v", pack.PermissionConstraints)
	}
	assertSource(t, pack.SourceRefs, ContextSourceKindUserInstruction, true)
	assertSource(t, pack.SourceRefs, ContextSourceKindTaskContract, true)
	if policy.requests != 1 {
		t.Fatalf("policy requests = %d", policy.requests)
	}
}

func assertSource(t *testing.T, refs []ContextSourceRef, kind ContextSourceKind, immutable bool) {
	t.Helper()
	for _, ref := range refs {
		if ref.Kind == kind && ref.Immutable == immutable {
			return
		}
	}
	t.Fatalf("missing source kind %s immutable=%v in %#v", kind, immutable, refs)
}

type fixedContextPackPolicy struct {
	requests              int
	permissionConstraints []string
	sourceRefs            []ContextSourceRef
	overridePolicy        OverridePolicy
}

func (p *fixedContextPackPolicy) ApplyContextPolicy(req ContextPackBuildRequest) (ContextPackPolicyDecision, error) {
	p.requests++
	return ContextPackPolicyDecision{
		PermissionConstraints: p.permissionConstraints,
		SourceRefs:            p.sourceRefs,
		OverridePolicy:        p.overridePolicy,
	}, nil
}

func sampleTaskInput() TaskInput {
	return TaskInput{
		ID:                 "task-1",
		MissionID:          "mission-1",
		Goal:               "Implement 7.4",
		WorkerPersonalGoal: "Build context and execenv",
		AcceptanceCriteria: []TaskAcceptanceInput{
			{ID: "acc-1", Description: "context contains all bounded inputs"},
			{ID: "acc-2", Description: "raw history is rejected"},
		},
		EvidenceRequirements: []TaskEvidenceInput{
			{ID: "ev-1", Kind: "test", Description: "context tests"},
			{ID: "ev-2", Kind: "artifact", Description: "execenv files"},
		},
		ExpectedDeliverables: []TaskDeliverableInput{{ID: "del-1", Title: "code", Description: "implementation"}},
	}
}

func validOrgIdentity() AgentOrgIdentity {
	return AgentOrgIdentity{
		OrganizationRef:   OrganizationRef{ID: "org-1"},
		DepartmentRef:     DepartmentRef{ID: "dep-1"},
		PositionRef:       PositionRef{ID: "pos-1"},
		EmploymentType:    EmploymentTypeAIAgent,
		SupervisionPolicy: SupervisionPolicyRoot,
	}
}
