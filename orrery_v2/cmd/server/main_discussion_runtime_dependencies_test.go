package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	agentlifecycle "orrery_v2/internal/agent/lifecycle"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/knowledgeadmin"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	knowledgeprovider "orrery_v2/internal/knowledge/provider"
	memaudit "orrery_v2/internal/memory/audit"
	memcapture "orrery_v2/internal/memory/capture"
	memcore "orrery_v2/internal/memory/core"
	mempageindex "orrery_v2/internal/memory/pageindex"
	memstore "orrery_v2/internal/memory/store"
	memtestkit "orrery_v2/internal/memory/testkit"
	"orrery_v2/internal/platform/filestore"
	"orrery_v2/internal/platform/testkit"
)

type testAgentResultCommitter struct{}

func (testAgentResultCommitter) SubmitAgentResult(context.Context, agentcore.AgentResult, agentcore.CommitOptions) error {
	return nil
}

func TestEnsureDefaultCoordinatorMemoryProfilesCreatesReadableProfiles(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	memory := memstore.NewPlatformStore(env.DB)

	if err := ensureDefaultCoordinatorMemoryProfiles(ctx, memory); err != nil {
		t.Fatalf("ensure default memory profiles: %v", err)
	}

	profiles, err := memory.ListProfiles(ctx)
	if err != nil {
		t.Fatalf("list memory profiles: %v", err)
	}
	if len(profiles) != 4 {
		t.Fatalf("profile count = %d, profiles = %+v", len(profiles), profiles)
	}
	for _, agentID := range []string{defaultLeadAgentID, defaultCoordinatorAgentID, defaultQAPlanAgentID, defaultQACheckAgentID} {
		profile, err := memory.GetProfile(ctx, agentcore.MemoryProfileID("memory-"+agentID))
		if err != nil {
			t.Fatalf("get %s memory profile: %v", agentID, err)
		}
		if profile.OwnerRef.AgentID != agentcore.AgentID(agentID) ||
			profile.OwnerRef.OrganizationID != defaultCoordinatorOrganizationID ||
			profile.DefaultScope.Kind != memcore.ScopeAgent ||
			profile.DefaultScope.ID != agentID ||
			profile.MemoryKind != agentcore.MemoryKindPersonal ||
			!profile.Enabled {
			t.Fatalf("%s memory profile = %+v", agentID, profile)
		}
	}
}

func TestEnsureAgentMemoryProfilesRepairsExistingWorkforceProfileRefs(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	memory := memstore.NewPlatformStore(env.DB)
	agentProfile := agentcore.AgentProfile{
		ID:          "profile-coder",
		RealName:    "写代码的cc",
		DisplayName: "写代码的cc",
		AgentKind:   agentcore.AgentKindAI,
		OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef:   agentcore.OrganizationRef{ID: "org-1"},
			DepartmentRef:     agentcore.DepartmentRef{ID: "dept-1"},
			PositionRef:       agentcore.PositionRef{ID: "pos-1"},
			RoleRefs:          []agentcore.RoleRef{{ID: "developer"}},
			AuthorityScope:    "implementation",
			EmploymentType:    agentcore.EmploymentTypeAIAgent,
			SupervisionPolicy: agentcore.SupervisionPolicyRoot,
		},
		Persona: agentcore.AgentPersona{ResponsibilityBoundary: "负责实现代码并提交证据。"},
		BaseAgent: agentcore.BaseAgentSpec{
			InheritsBaseAgent: true,
			AuditRequired:     true,
			SystemPrompt:      "你是测试 Agent。",
			LLMAPIKeyRef:      "secret://system/llm-api-key",
			SkillScope:        "profile_bound",
			ToolScope:         "profile_bound",
			MCPScope:          "profile_bound",
		},
		PersonalMemoryProfileRef: agentcore.MemoryProfileRef{
			MemoryProfileID: "memory-profile-coder",
			OwnerAgentID:    "profile-coder",
			MemoryKind:      agentcore.MemoryKindPersonal,
		},
		SkillRefs:                 []agentcore.SkillRef{},
		ToolRefs:                  []agentcore.ToolRef{},
		CollectiveMemoryScopeRefs: []agentcore.MemoryProfileRef{},
		RuntimeSpec:               agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged},
		Enabled:                   true,
	}
	if err := profiles.SaveProfile(ctx, agentProfile); err != nil {
		t.Fatalf("save workforce profile: %v", err)
	}

	if err := ensureAgentMemoryProfiles(ctx, profiles, memory); err != nil {
		t.Fatalf("ensure agent memory profiles: %v", err)
	}

	memoryProfile, err := memory.GetProfile(ctx, "memory-profile-coder")
	if err != nil {
		t.Fatalf("get repaired memory profile: %v", err)
	}
	if memoryProfile.OwnerRef.AgentID != "profile-coder" ||
		memoryProfile.DefaultScope.Kind != memcore.ScopeAgent ||
		memoryProfile.DefaultScope.ID != "profile-coder" ||
		!memoryProfile.Enabled {
		t.Fatalf("repaired memory profile should be readable by owner: %+v", memoryProfile)
	}
	repaired, err := profiles.GetProfile(ctx, "profile-coder")
	if err != nil {
		t.Fatalf("get repaired workforce profile: %v", err)
	}
	if len(repaired.CollectiveMemoryScopeRefs) != 2 {
		t.Fatalf("collective memory refs = %+v", repaired.CollectiveMemoryScopeRefs)
	}
	for _, profileID := range []agentcore.MemoryProfileID{"memory-organization-org-1", "memory-department-dept-1"} {
		if _, err := memory.GetProfile(ctx, profileID); err != nil {
			t.Fatalf("get collective profile %s: %v", profileID, err)
		}
	}
}

func TestProductionDiscussionAgentDependenciesUseLifecycleSignalBusAndStoreTraceRecorder(t *testing.T) {
	metrics := &capturingLifecycleMetricSink{}
	bus := agentlifecycle.NewInMemorySignalBus(agentlifecycle.SignalBusOptions{Metrics: metrics})
	traceStore := &capturingProductionTraceStore{}
	deps := newProductionDiscussionAgentDependencies(bus, traceStore, nil)

	if _, ok := deps.SignalSink.(agentlifecycle.CoreSignalSink); !ok {
		t.Fatalf("signal sink = %T, want lifecycle CoreSignalSink", deps.SignalSink)
	}
	trace, ok := deps.TraceRecorder.(agentcore.StoreTraceRecorder)
	if !ok {
		t.Fatalf("trace recorder = %T, want StoreTraceRecorder", deps.TraceRecorder)
	}
	if trace.Store == nil {
		t.Fatalf("trace store is nil")
	}

	if err := deps.SignalSink.EmitSignal(context.Background(), agentcore.Signal{
		Name:           agentcore.SignalBeforeTaskReceived,
		RunID:          "run-1",
		AgentID:        agentcore.AgentID(defaultLeadAgentID),
		AgentProfileID: agentcore.AgentProfileID(defaultLeadAgentID),
		MissionID:      "mission-1",
		TaskID:         "task-1",
		At:             time.Date(2026, 6, 3, 1, 2, 3, 0, time.UTC),
	}); err != nil {
		t.Fatalf("emit signal: %v", err)
	}
	if len(metrics.names) != 1 || metrics.names[0] != "agent.lifecycle.signal.emit" {
		t.Fatalf("metrics = %+v, want lifecycle emit metric", metrics.names)
	}

	ref, err := deps.TraceRecorder.RecordTrace(context.Background(), agentcore.AgentInput{
		RunID: "run-1",
		ContextPack: agentcore.ContextPack{
			AgentID:   agentcore.AgentID(defaultLeadAgentID),
			MissionID: "mission-1",
			TaskID:    "task-1",
		},
	}, agentcore.RuntimeResponse{
		RawOutputRef: "provider://raw",
		Messages:     []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("record trace: %v", err)
	}
	if ref != "artifact://agent-runs/run-1/runtime-trace.json" {
		t.Fatalf("trace ref = %q", ref)
	}
	if !strings.Contains(string(traceStore.data), `"raw_output_ref":"provider://raw"`) {
		t.Fatalf("trace payload missing runtime output: %s", string(traceStore.data))
	}
}

func TestProductionDiscussionAgentDependenciesWireDurableTraceIndex(t *testing.T) {
	index := &capturingProductionTraceIndexSink{}
	deps := newProductionDiscussionAgentDependencies(nil, productionTraceStore{indexSink: index}, nil)
	recorder, ok := deps.TraceRecorder.(agentcore.StoreTraceRecorder)
	if !ok {
		t.Fatalf("trace recorder=%T", deps.TraceRecorder)
	}
	if recorder.IndexSink != index {
		t.Fatalf("trace index sink=%T, want capturing sink", recorder.IndexSink)
	}
}

func testProductionDiscussionAgentDependencies(deps productionDiscussionAgentDependencies) productionDiscussionAgentDependencies {
	if deps.TraceRecorder == nil {
		deps.TraceRecorder = agentcore.StoreTraceRecorder{Store: &capturingProductionTraceStore{}}
	}
	return deps
}

func TestProductionDiscussionAgentDependenciesSkipsMemoryCaptureWhenDisabled(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	deps := productionDiscussionAgentDependencies{
		Committer:        testAgentResultCommitter{},
		MemoryRepository: store,
		MemoryAudit:      &memtestkit.MemoryAuditRecorder{},
		MemorySettings:   fixedProductionMemorySettings{enabled: false},
	}

	committer := productionMemoryCaptureCommitter(deps)
	if _, ok := committer.(memcapture.AgentResultCaptureCommitter); !ok {
		t.Fatalf("committer = %T, want memory capture wrapper with runtime toggle", committer)
	}
	if err := committer.SubmitAgentResult(ctx, agentcore.AgentResult{
		ID:        "result-1",
		MissionID: "mission-1",
		TaskID:    "task-1",
		AgentID:   agentcore.AgentID("agent-1"),
		RunID:     "run-1",
		Summary:   agentcore.Summary{Text: "Tests passed."},
		CreatedAt: time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC),
	}, agentcore.CommitOptions{IdempotencyKey: "run-1"}); err != nil {
		t.Fatalf("SubmitAgentResult: %v", err)
	}
	candidates, err := store.ListCandidates(ctx, memstore.CandidateQuery{MissionID: "mission-1"})
	if err != nil {
		t.Fatalf("ListCandidates: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("disabled memory should not capture candidates: %+v", candidates)
	}
}

func TestProductionDiscussionAgentDependenciesMaterializesResultMemoryWhenEnabled(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	if err := store.SaveProfile(ctx, memcore.MemoryProfile{
		ID:         "memory-agent-1",
		OwnerRef:   memcore.OwnerRef{AgentID: "agent-1"},
		MemoryKind: agentcore.MemoryKindPersonal,
		DefaultScope: memcore.MemoryScope{
			Kind: memcore.ScopeAgent,
			ID:   "agent-1",
		},
		VisibilityPolicy: memcore.VisibilityPolicy{Mode: memcore.VisibilityOwnerOnly},
		Enabled:          true,
	}); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	audit := &memtestkit.MemoryAuditRecorder{}
	deps := productionDiscussionAgentDependencies{
		Committer:        testAgentResultCommitter{},
		MemoryRepository: store,
		MemoryAudit:      audit,
		MemorySettings:   fixedProductionMemorySettings{enabled: true},
	}

	committer := productionMemoryCaptureCommitter(deps)
	if _, ok := committer.(memcapture.AgentResultCaptureCommitter); !ok {
		t.Fatalf("committer = %T, want memory capture wrapper", committer)
	}
	err := committer.SubmitAgentResult(ctx, agentcore.AgentResult{
		ID:        "result-1",
		MissionID: "mission-1",
		TaskID:    "task-1",
		AgentID:   agentcore.AgentID("agent-1"),
		RunID:     "run-1",
		Summary:   agentcore.Summary{Text: "Tests passed."},
		Evidence: []agentcore.Evidence{{
			ID:          agentcore.EvidenceID("evidence-1"),
			Kind:        agentcore.EvidenceKindTestLog,
			ArtifactRef: agentcore.ArtifactRef{ID: "artifact-1"},
		}},
		CreatedAt: time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC),
	}, agentcore.CommitOptions{IdempotencyKey: "run-1"})
	if err != nil {
		t.Fatalf("SubmitAgentResult: %v", err)
	}
	candidates, err := store.ListCandidates(ctx, memstore.CandidateQuery{MissionID: "mission-1"})
	if err != nil {
		t.Fatalf("ListCandidates: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v", candidates)
	}
	if candidates[0].ID != "candidate.agent_result.run-1" {
		t.Fatalf("candidates = %+v", candidates)
	}
	records, err := store.FindRecords(ctx, memstore.RecordQuery{ProfileIDs: []agentcore.MemoryProfileID{"memory-agent-1"}})
	if err != nil {
		t.Fatalf("FindRecords: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("records = %+v", records)
	}
	missionProfileID := memcore.ScopeMemoryProfileID(memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"})
	missionRecords, err := store.FindRecords(ctx, memstore.RecordQuery{ProfileIDs: []agentcore.MemoryProfileID{missionProfileID}})
	if err != nil {
		t.Fatalf("FindRecords mission: %v", err)
	}
	if len(missionRecords) != 1 || missionRecords[0].CandidateID != candidates[0].ID {
		t.Fatalf("mission records = %+v", missionRecords)
	}
	if events := audit.Snapshot(); len(events) != 2 ||
		events[0].Type != memaudit.EventMemoryCandidateCreated ||
		events[1].Type != memaudit.EventMemoryRecordCreated {
		t.Fatalf("audit events = %+v", events)
	}
}

type fixedProductionMemorySettings struct {
	enabled bool
}

func (s fixedProductionMemorySettings) MemoryEnabled(context.Context) (bool, error) {
	return s.enabled, nil
}

func TestProductionDiscussionArtifactCleanerDeletesConversationAndLegacyRunPrefixes(t *testing.T) {
	store := &capturingDeletePrefixStore{}
	cleaner := productionDiscussionArtifactCleaner{store: store}

	if err := cleaner.DeleteConversationArtifacts(context.Background(), "conv-1"); err != nil {
		t.Fatalf("DeleteConversationArtifacts: %v", err)
	}
	if err := cleaner.DeleteRunArtifacts(context.Background(), "conv-1", "run-1"); err != nil {
		t.Fatalf("DeleteRunArtifacts: %v", err)
	}

	want := []string{
		"discussion-conversations/conv-1/",
		"discussion-conversations/conv-1/runs/run-1/",
		"agent-runs/run-1/",
	}
	if strings.Join(store.prefixes, "|") != strings.Join(want, "|") {
		t.Fatalf("deleted prefixes = %v, want %v", store.prefixes, want)
	}
}

func TestDiscussionAgentConversationResultCommitterSubmitsRealMissionTaskResults(t *testing.T) {
	commands := &capturingAgentResultCommands{}
	committer := discussionAgentConversationResultCommitter{commands: commands}
	result := agentcore.AgentResult{
		ID:        "result-1",
		MissionID: "mission-1",
		TaskID:    "task-1",
		AgentID:   agentcore.AgentID(defaultLeadAgentID),
		RunID:     "run-1",
		Summary:   agentcore.Summary{Text: "done"},
	}

	if err := committer.SubmitAgentResult(context.Background(), result, agentcore.CommitOptions{IdempotencyKey: "run-1"}); err != nil {
		t.Fatalf("SubmitAgentResult: %v", err)
	}

	if commands.calls != 1 {
		t.Fatalf("calls = %d, want 1", commands.calls)
	}
	if commands.viewer.ViewerID != defaultLeadAgentID || commands.viewer.ViewerKind != missioncontrol.ViewerKindAgent {
		t.Fatalf("viewer = %+v", commands.viewer)
	}
	if commands.cmd.MissionID != "mission-1" || commands.cmd.TaskID != "task-1" || commands.cmd.IdempotencyKey != "run-1" {
		t.Fatalf("cmd = %+v", commands.cmd)
	}
}

func TestDiscussionAgentConversationResultCommitterSkipsConversationPlaceholderResults(t *testing.T) {
	commands := &capturingAgentResultCommands{}
	committer := discussionAgentConversationResultCommitter{commands: commands}

	err := committer.SubmitAgentResult(context.Background(), agentcore.AgentResult{
		ID:               "result-run-1",
		MissionID:        "discussion-conv-1",
		TaskID:           "conv-1",
		AgentID:          agentcore.AgentID(defaultLeadAgentID),
		RunID:            "run-1",
		Summary:          agentcore.Summary{Text: "intake reply"},
		InternalTraceRef: "artifact://discussion-conversations/conv-1/runs/run-1/runtime-trace.json",
	}, agentcore.CommitOptions{IdempotencyKey: "run-1"})

	if err != nil {
		t.Fatalf("SubmitAgentResult: %v", err)
	}
	if commands.calls != 0 {
		t.Fatalf("calls = %d, want placeholder discussion result to skip mission ledger", commands.calls)
	}
}

func TestDiscussionAgentConversationResultCommitterSkipsMissionMentionResults(t *testing.T) {
	commands := &capturingAgentResultCommands{}
	committer := discussionAgentConversationResultCommitter{commands: commands}

	err := committer.SubmitAgentResult(context.Background(), agentcore.AgentResult{
		ID:               "result-run-1",
		MissionID:        "mission-1",
		TaskID:           "mention-inbox-1",
		AgentID:          agentcore.AgentID(defaultLeadAgentID),
		RunID:            "run-1",
		Summary:          agentcore.Summary{Text: "mention reply"},
		InternalTraceRef: "artifact://discussion-conversations/group-1/runs/run-1/runtime-trace.json",
	}, agentcore.CommitOptions{
		IdempotencyKey: "run-1",
		ContextSourceRefs: []agentcore.ContextSourceRef{{
			Kind: agentcore.ContextSourceKindUserInstruction,
			Ref: agentcore.ContextSourceTypedRef{UserInstructionRef: &agentcore.UserInstructionRef{
				ID: agentcore.UserInstructionID(missioncontrol.MentionAgentRunSource),
			}},
		}},
	})

	if err != nil {
		t.Fatalf("SubmitAgentResult: %v", err)
	}
	if commands.calls != 0 {
		t.Fatalf("calls = %d, want mission mention result to skip mission task ledger", commands.calls)
	}
}

func TestMissionTaskAgentResultCommitterDoesNotSkipScheduledMissionTasks(t *testing.T) {
	committer := missionTaskAgentResultCommitter{}

	err := committer.SubmitAgentResult(context.Background(), agentcore.AgentResult{
		ID:        "result-run-1",
		MissionID: "mission-run-1",
		TaskID:    "task-run-1",
		AgentID:   agentcore.AgentID(defaultLeadAgentID),
		RunID:     "run-1",
		Summary:   agentcore.Summary{Text: "scheduled direct run complete"},
	}, agentcore.CommitOptions{
		IdempotencyKey: "run-1",
		ContextSourceRefs: []agentcore.ContextSourceRef{{
			Kind: agentcore.ContextSourceKindUserInstruction,
			Ref: agentcore.ContextSourceTypedRef{
				UserInstructionRef: &agentcore.UserInstructionRef{ID: agentcore.UserInstructionID(schedulerAgentTaskRunSource)},
			},
		}},
	})

	if err == nil || !strings.Contains(err.Error(), "delivery is not configured") {
		t.Fatalf("SubmitAgentResult error = %v, want real Mission Task delivery requirement", err)
	}
}

func TestProductionPMMemoryProviderBuildsDigestFromSeededProfileRecords(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	memory := memstore.NewPlatformStore(env.DB)
	pmTemplate, err := readDefaultAgentTemplate("lead-agent.json")
	if err != nil {
		t.Fatalf("read lead template: %v", err)
	}
	profile := defaultCoordinatorAgentProfile(defaultLeadAgentID, "pm", agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}, pmTemplate)
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save profile: %v", err)
	}
	if err := ensureDefaultCoordinatorMemoryProfiles(ctx, memory); err != nil {
		t.Fatalf("ensure memory profiles: %v", err)
	}
	now := time.Date(2026, 6, 2, 1, 2, 3, 0, time.UTC)
	if err := memory.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:            "candidate-memory-1",
		SourceType:    memcore.SourceDiscussionMessage,
		SourceRef:     "message-1",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: defaultLeadAgentID},
		ProposedKind:  agentcore.MemoryKindPersonal,
		Summary:       "The operator prefers PM answers with owner/date/risk sections.",
		CreatedBy:     agentcore.AgentID(defaultLeadAgentID),
		Status:        memcore.CandidateAccepted,
	}); err != nil {
		t.Fatalf("save memory candidate: %v", err)
	}
	if err := memory.SaveRecord(ctx, memcore.MemoryRecord{
		ID:          "memory-record-1",
		CandidateID: "candidate-memory-1",
		ProfileID:   agentcore.MemoryProfileID("memory-" + defaultLeadAgentID),
		Scope:       memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: defaultLeadAgentID},
		Summary:     "The operator prefers PM answers with owner/date/risk sections.",
		Visibility: memcore.Visibility{
			Mode: memcore.VisibilityOwnerOnly,
		},
		Confidence: 0.9,
		CreatedBy:  agentcore.AgentID(defaultLeadAgentID),
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("save memory record: %v", err)
	}

	context, err := newProductionDiscussionMemoryProvider(profiles, memory).DiscussionAgentMemoryContext(ctx, defaultLeadAgentID)
	if err != nil {
		t.Fatalf("DiscussionAgentMemoryContext: %v", err)
	}

	if context.PersonalDigest.MemoryKind != "personal" ||
		!strings.Contains(context.PersonalDigest.Summary, "Facts:") ||
		!strings.Contains(context.PersonalDigest.Summary, "The operator prefers PM answers with owner/date/risk sections.") ||
		len(context.PersonalDigest.SourceMemory) != 1 ||
		context.PersonalDigest.SourceMemory[0] != "memory-record-1" {
		t.Fatalf("memory context = %+v", context)
	}
	if !strings.HasPrefix(context.PersonalDigest.DigestID, "digest.") {
		t.Fatalf("digest id = %q", context.PersonalDigest.DigestID)
	}
}

func TestProductionMemoryProviderKeepsQueryMissesSeparateFromBackgroundMemory(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	memory := memstore.NewPlatformStore(env.DB)
	pmTemplate, err := readDefaultAgentTemplate("lead-agent.json")
	if err != nil {
		t.Fatal(err)
	}
	profile := defaultCoordinatorAgentProfile(defaultLeadAgentID, "pm", agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}, pmTemplate)
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	if err := ensureDefaultCoordinatorMemoryProfiles(ctx, memory); err != nil {
		t.Fatal(err)
	}
	if err := memory.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:            "candidate-fallback",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: defaultLeadAgentID},
		ProposedKind:  agentcore.MemoryKindPersonal,
		Status:        memcore.CandidateAccepted,
	}); err != nil {
		t.Fatal(err)
	}
	fallbackRecord := memcore.MemoryRecord{
		ID:          "memory-fallback",
		CandidateID: "candidate-fallback",
		ProfileID:   agentcore.MemoryProfileID("memory-" + defaultLeadAgentID),
		Scope:       memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: defaultLeadAgentID},
		Summary:     "Deterministic fallback memory.",
		CreatedBy:   agentcore.AgentID(defaultLeadAgentID),
	}
	if err := memory.SaveRecord(ctx, fallbackRecord); err != nil {
		t.Fatal(err)
	}
	semanticRecord := fallbackRecord
	semanticRecord.ID = "memory-semantic"
	semanticRecord.Summary = "Semantically relevant launch preference."
	semanticRecord.CandidateID = "candidate-semantic"
	if err := memory.SaveCandidate(ctx, memcore.MemoryCandidate{ID: semanticRecord.CandidateID, ProposedScope: semanticRecord.Scope, ProposedKind: agentcore.MemoryKindPersonal, Status: memcore.CandidateAccepted}); err != nil {
		t.Fatal(err)
	}
	if err := memory.SaveRecord(ctx, semanticRecord); err != nil {
		t.Fatal(err)
	}
	semanticRecord, err = memory.GetRecord(ctx, semanticRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	recall := &capturingSemanticMemoryRecall{records: []memcore.MemoryRecord{semanticRecord}}
	provider := newProductionDiscussionMemoryProvider(profiles, memory, productionDiscussionMemoryProviderOptions{Recall: recall})

	memoryContext, err := provider.DiscussionAgentMemoryContextForQuery(ctx, defaultLeadAgentID, "How should the launch plan be structured?")
	if err != nil {
		t.Fatal(err)
	}
	if recall.request.Query != "How should the launch plan be structured?" || recall.request.Subject.AgentID != defaultLeadAgentID ||
		len(recall.request.Subject.OrganizationIDs) != 1 || recall.request.Subject.OrganizationIDs[0] != defaultCoordinatorOrganizationID {
		t.Fatalf("semantic recall request = %+v", recall.request)
	}
	if !strings.Contains(memoryContext.PersonalDigest.Summary, semanticRecord.Summary) || strings.Contains(memoryContext.PersonalDigest.Summary, fallbackRecord.Summary) {
		t.Fatalf("semantic memory context = %+v", memoryContext)
	}

	recall.records = nil
	memoryContext, err = provider.DiscussionAgentMemoryContextForQuery(ctx, defaultLeadAgentID, "How should the launch plan be structured?")
	if err != nil {
		t.Fatalf("empty PageIndex selection: %v", err)
	}
	if memoryContext.PersonalDigest.Summary != "" || len(memoryContext.PersonalDigest.SourceMemory) != 0 {
		t.Fatalf("query miss returned unrelated background: %+v", memoryContext)
	}
	recall.err = errors.New("PageIndex unavailable")
	if _, err = provider.DiscussionAgentMemoryContextForQuery(ctx, defaultLeadAgentID, "launch plan"); !errors.Is(err, recall.err) {
		t.Fatalf("query error was hidden: %v", err)
	}
	memoryContext, err = provider.DiscussionAgentMemoryContext(ctx, defaultLeadAgentID)
	if err != nil || !strings.Contains(memoryContext.PersonalDigest.Summary, fallbackRecord.Summary) {
		t.Fatalf("default background context unavailable: %+v, %v", memoryContext, err)
	}
}

type capturingSemanticMemoryRecall struct {
	request mempageindex.RecallRequest
	records []memcore.MemoryRecord
	err     error
	calls   int
}

func (r *capturingSemanticMemoryRecall) Recall(_ context.Context, request mempageindex.RecallRequest) ([]memcore.MemoryRecord, error) {
	r.calls++
	r.request = request
	return append([]memcore.MemoryRecord(nil), r.records...), r.err
}

func TestProductionDiscussionKnowledgeProviderSearchesCurrentMessage(t *testing.T) {
	search := &capturingProductionKnowledgeSearch{page: knowledgeadmin.KnowledgeSearchPageDTO{Results: []knowledgeadmin.KnowledgeSearchResultDTO{{
		KnowledgeBaseID: "kb-release",
		DocumentID:      "doc-release",
		ChunkID:         "chunk-release",
		DocumentName:    "release-handbook.md",
		Score:           0.91,
		Snippet:         "发布流程必须先完成灰度验收，然后通知运营。",
		HeadingPath:     []string{"发布", "灰度"},
	}}}}

	profiles := fixedKnowledgeProfileLookup{profile: agentcore.AgentProfile{
		ID:        "lead-agent",
		AgentKind: agentcore.AgentKindAI,
		Enabled:   true,
		OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef: agentcore.OrganizationRef{ID: "org-1"},
			PositionRef:     agentcore.PositionRef{ID: "position-1"},
			RoleRefs:        []agentcore.RoleRef{{ID: "role-1"}},
		},
	}}
	context, err := newProductionDiscussionKnowledgeProvider(profiles, search).DiscussionAgentKnowledgeContext(context.Background(), discussionsession.DiscussionAgentRequest{
		AgentID: "lead-agent",
		Content: "发布流程是什么？",
	})
	if err != nil {
		t.Fatalf("DiscussionAgentKnowledgeContext: %v", err)
	}
	if search.cmd.Query != "发布流程是什么？" || search.cmd.Limit != 6 ||
		search.cmd.RetrievalMode != knowledgeprovider.RetrievalModeAuto || search.viewer.ViewerID != "lead-agent" ||
		len(search.viewer.AgentIDs) != 1 || search.viewer.AgentIDs[0] != "lead-agent" ||
		len(search.viewer.OrganizationIDs) != 1 || search.viewer.OrganizationIDs[0] != "org-1" ||
		len(search.viewer.PositionIDs) != 1 || search.viewer.PositionIDs[0] != "position-1" ||
		len(search.viewer.RoleIDs) != 1 || search.viewer.RoleIDs[0] != "role-1" {
		t.Fatalf("search call viewer=%+v cmd=%+v", search.viewer, search.cmd)
	}
	if len(context.Results) != 1 ||
		context.Results[0].DocumentName != "release-handbook.md" ||
		context.Results[0].Snippet != "发布流程必须先完成灰度验收，然后通知运营。" ||
		len(context.Results[0].HeadingPath) != 2 {
		t.Fatalf("knowledge context = %+v", context)
	}
}

type capturingLifecycleMetricSink struct {
	names []string
}

type capturingProductionKnowledgeSearch struct {
	viewer knowledgeadmin.ViewerRef
	cmd    knowledgeadmin.SearchKnowledgeCommand
	page   knowledgeadmin.KnowledgeSearchPageDTO
	err    error
}

func (s *capturingLifecycleMetricSink) CaptureContext(_ context.Context, name string, _ map[string]any) bool {
	s.names = append(s.names, name)
	return true
}

func (s *capturingProductionKnowledgeSearch) SearchKnowledge(_ context.Context, viewer knowledgeadmin.ViewerRef, cmd knowledgeadmin.SearchKnowledgeCommand) (knowledgeadmin.KnowledgeSearchPageDTO, error) {
	s.viewer = viewer
	s.cmd = cmd
	return s.page, s.err
}

type capturingProductionTraceStore struct {
	key  string
	data []byte
}

type capturingProductionTraceIndexSink struct{}

func (*capturingProductionTraceIndexSink) SaveTraceIndex(context.Context, agentcore.TraceIndexRecord) error {
	return nil
}

func (s *capturingProductionTraceStore) PutTrace(_ context.Context, key string, data []byte) (string, error) {
	s.key = key
	s.data = append([]byte(nil), data...)
	return "artifact://" + key, nil
}

type capturingDeletePrefixStore struct {
	prefixes []string
}

func (s *capturingDeletePrefixStore) Put(context.Context, string, string, io.Reader) (filestore.ArtifactRef, error) {
	return filestore.ArtifactRef{}, nil
}

func (s *capturingDeletePrefixStore) Get(context.Context, string) (filestore.Artifact, error) {
	return filestore.Artifact{}, nil
}

func (s *capturingDeletePrefixStore) GetReader(context.Context, string) (io.ReadCloser, error) {
	return nil, nil
}

func (s *capturingDeletePrefixStore) Upload(context.Context, string, []byte, string, string) (string, error) {
	return "", nil
}

func (s *capturingDeletePrefixStore) Delete(context.Context, string) error {
	return nil
}

func (s *capturingDeletePrefixStore) DeleteKeys(context.Context, []string) error {
	return nil
}

func (s *capturingDeletePrefixStore) DeletePrefix(_ context.Context, prefix string) error {
	s.prefixes = append(s.prefixes, prefix)
	return nil
}

func (s *capturingDeletePrefixStore) URL(context.Context, string) (string, error) {
	return "", nil
}

func (s *capturingDeletePrefixStore) KeyFromURL(rawURL string) string {
	return rawURL
}

func (s *capturingDeletePrefixStore) CdnDomain() string {
	return ""
}
