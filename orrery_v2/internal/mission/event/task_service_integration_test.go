package event

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/eventstore"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestDeliveryServicePersistsTaskSubmissionsAndMissionEventsThroughPlatform(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missiontask.NewPlatformRepository()
	sink := NewTaskEventSink()
	record := integrationTaskRecord(missiontask.TaskStatusInProgress)
	seedIntegrationTask(t, ctx, env, repo, record)
	service := missiontask.NewDeliveryService(env.UnitOfWork, repo, sink, missiontask.DeliveryServiceOptions{Clock: env.Clock, IDGenerator: env.IDGenerator})

	submission, err := service.SubmitAgentResult(ctx, record.Contract.ID, integrationAgentResult(), missiontask.SubmitOptions{IdempotencyKey: "submit-platform-001"})
	if err != nil {
		t.Fatalf("SubmitAgentResult: %v", err)
	}
	if submission.ID == "" {
		t.Fatalf("submission id was not generated: %#v", submission)
	}

	events, err := NewStoreReader(eventstore.New(env.DB)).LoadMission(ctx, record.Contract.MissionID)
	if err != nil {
		t.Fatalf("load mission events: %v", err)
	}
	wantTypes := []EventType{EventAgentResultSubmitted, EventDeliverableSubmitted, EventEvidenceSubmitted, EventQASkipped}
	if len(events) != len(wantTypes) {
		t.Fatalf("event count = %d, want %d: %+v", len(events), len(wantTypes), events)
	}
	for i, want := range wantTypes {
		if events[i].EventType != want {
			t.Fatalf("event[%d] type = %s, want %s", i, events[i].EventType, want)
		}
	}
}

func TestQAAndReworkServicesPersistMissionEventsThroughPlatform(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missiontask.NewPlatformRepository()
	sink := NewTaskEventSink()
	record := integrationTaskRecord(missiontask.TaskStatusInProgress)
	record.Contract.QACheckRequired = true
	seedIntegrationTask(t, ctx, env, repo, record)
	delivery := missiontask.NewDeliveryService(env.UnitOfWork, repo, sink, missiontask.DeliveryServiceOptions{Clock: env.Clock, IDGenerator: env.IDGenerator})
	submission, err := delivery.SubmitAgentResult(ctx, record.Contract.ID, integrationAgentResult(), missiontask.SubmitOptions{IdempotencyKey: "submit-platform-qa-001"})
	if err != nil {
		t.Fatalf("SubmitAgentResult: %v", err)
	}
	qa := missiontask.NewQAService(env.UnitOfWork, repo, sink, missiontask.QAServiceOptions{Clock: env.Clock, IDGenerator: env.IDGenerator})
	result, err := qa.SubmitQAResult(ctx, record.Contract.ID, missiontask.QAResultRecord{
		ID:           missiontask.QAResultID("qa-result-001"),
		SubmissionID: string(submission.ID),
		CheckedBy:    agentcore.AgentID("qa-agent-001"),
		Status:       missiontask.QAStatusRejected,
		CheckedCriteria: []missiontask.CheckedCriterion{{
			CriterionID: missiontask.AcceptanceCriterionID("acceptance-001"),
			Status:      missiontask.CheckStatusFailed,
			Reason:      "missing edge case coverage",
		}},
	}, missiontask.QASubmitOptions{IdempotencyKey: "qa-platform-001"})
	if err != nil {
		t.Fatalf("SubmitQAResult: %v", err)
	}
	if result.ReworkRequestRef == "" {
		t.Fatalf("QA rejection did not create a rework ref: %#v", result)
	}
	rework := missiontask.NewReworkService(env.UnitOfWork, repo, sink, missiontask.ReworkServiceOptions{Clock: env.Clock})
	if _, err := rework.ReassignOpenRework(ctx, record.Contract.ID, agentcore.AgentID("review-coordinator-001")); err != nil {
		t.Fatalf("ReassignOpenRework: %v", err)
	}
	if err := delivery.AcceptTask(ctx, record.Contract.ID, agentcore.AgentInstanceRef{ID: agentcore.AgentInstanceID("agent-worker-001")}); err != nil {
		t.Fatalf("AcceptTask after rework: %v", err)
	}
	if _, err := rework.ResolveRework(ctx, record.Contract.ID, agentcore.AgentID("agent-worker-001")); err != nil {
		t.Fatalf("ResolveRework: %v", err)
	}

	events, err := NewStoreReader(eventstore.New(env.DB)).LoadMission(ctx, record.Contract.MissionID)
	if err != nil {
		t.Fatalf("load mission events: %v", err)
	}
	assertEventTypesInOrder(t, events, []EventType{
		EventAgentResultSubmitted,
		EventDeliverableSubmitted,
		EventEvidenceSubmitted,
		EventQARequested,
		EventQARejected,
		EventReworkRequested,
		EventReworkAssigned,
		EventTaskAccepted,
		EventReworkResolved,
	})
	for i, event := range events {
		if event.StreamVersion != i+1 {
			t.Fatalf("event[%d] stream version = %d, want %d", i, event.StreamVersion, i+1)
		}
	}
	if events[6].Actor.ID != "review-coordinator-001" {
		t.Fatalf("rework assignment actor = %s, want stable coordinator profile", events[6].Actor.ID)
	}
}

func TestQAServicePersistsQAPlanCompletedMissionEventThroughPlatform(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missiontask.NewPlatformRepository()
	sink := NewTaskEventSink()
	record := integrationTaskRecord(missiontask.TaskStatusAccepted)
	record.Contract.QAEnabled = true
	record.Contract.QAPlanRequired = true
	record.Contract.QAPlanCompleted = false
	seedIntegrationTask(t, ctx, env, repo, record)
	service := missiontask.NewQAService(env.UnitOfWork, repo, sink, missiontask.QAServiceOptions{Clock: env.Clock, IDGenerator: env.IDGenerator})

	if _, err := service.SubmitQAPlan(ctx, record.Contract.ID, missiontask.QAPlanRecord{
		ID:          missiontask.QAPlanID("qa-plan-001"),
		CreatedBy:   agentcore.AgentID("qa-plan-agent-001"),
		ArtifactRef: agentcore.ArtifactRef{ID: "artifact-qa-plan-001", Kind: agentcore.ArtifactKindLog, URI: "artifact://qa-plan-001"},
		Summary:     "QA plan generated",
	}, missiontask.QAPlanSubmitOptions{IdempotencyKey: "qa-plan-platform-001"}); err != nil {
		t.Fatalf("SubmitQAPlan: %v", err)
	}

	events, err := NewStoreReader(eventstore.New(env.DB)).LoadMission(ctx, record.Contract.MissionID)
	if err != nil {
		t.Fatalf("load mission events: %v", err)
	}
	assertEventTypesInOrder(t, events, []EventType{EventQAPlanCompleted})
	payload, ok := events[0].Payload.(QAPayload)
	if !ok {
		t.Fatalf("payload type = %T, want QAPayload", events[0].Payload)
	}
	if payload.QARef != "qa-plan-001" || payload.ArtifactRef.ID != "artifact-qa-plan-001" {
		t.Fatalf("QA plan event did not retain ref/artifact: %#v", payload)
	}
}

func seedIntegrationTask(t *testing.T, ctx context.Context, env *testkit.Env, repo *missiontask.PlatformRepository, record missiontask.TaskRecord) {
	t.Helper()
	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		return repo.SaveTask(ctx, tx, record)
	}); err != nil {
		t.Fatalf("seed task: %v", err)
	}
}

func assertEventTypesInOrder(t *testing.T, events []Envelope, want []EventType) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("event count = %d, want %d: %+v", len(events), len(want), events)
	}
	for i, event := range events {
		if event.EventType != want[i] {
			t.Fatalf("event[%d] type = %s, want %s", i, event.EventType, want[i])
		}
	}
}

func integrationTaskRecord(status missiontask.TaskStatus) missiontask.TaskRecord {
	return missiontask.TaskRecord{
		Contract:        integrationTaskContract(),
		Status:          status,
		Version:         1,
		StatusChangedAt: time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC),
	}
}

func integrationTaskContract() missiontask.TaskContract {
	return missiontask.TaskContract{
		ID:                 missiontask.TaskID("task-001"),
		MissionID:          missioncore.MissionID("mission-001"),
		Assignee:           missiontask.TaskAssigneeRef{AgentProfileRef: &agentcore.AgentProfileRef{ID: agentcore.AgentProfileID("agent-worker-001")}},
		Title:              "Implement core contracts",
		Goal:               "Create pure mission and agent contract packages",
		WorkerPersonalGoal: "Implement the contract model and validation rules",
		WorkerPersonalGoalSources: []missiontask.WorkerPersonalGoalSourceRef{
			{Kind: missiontask.WorkerPersonalGoalSourceGoal, ContractFieldRef: missiontask.ContractFieldRef{Field: missiontask.ContractFieldGoal}},
			{Kind: missiontask.WorkerPersonalGoalSourceScope, ContractFieldRef: missiontask.ContractFieldRef{Field: missiontask.ContractFieldScope}},
			{Kind: missiontask.WorkerPersonalGoalSourceExpectedDeliverable, ExpectedDeliverableRef: missiontask.ExpectedDeliverableRef{ID: missiontask.ExpectedDeliverableID("expected-001")}},
		},
		Scope: "internal/agent/core and internal/mission",
		ExpectedDeliverables: []missiontask.ExpectedDeliverable{{
			ID:          missiontask.ExpectedDeliverableID("expected-001"),
			Title:       "Contract packages",
			Description: "Go packages with pure contract types",
		}},
		AcceptanceCriteria: []missiontask.AcceptanceCriterion{{
			ID:          missiontask.AcceptanceCriterionID("acceptance-001"),
			Description: "go test passes for core contract packages",
		}},
		EvidenceRequirements: []missiontask.EvidenceRequirement{{
			ID:          missiontask.EvidenceRequirementID("evidence-required-001"),
			Description: "test log proving validation behavior",
			Kind:        missiontask.EvidenceRequirementKindTestLog,
			Required:    true,
		}},
		ContextPolicy: missiontask.ContextPolicy{
			RequireMissionGoal:        true,
			RequireDiscussionGoal:     true,
			RequireWorkerPersonalGoal: true,
		},
	}
}

func integrationAgentResult() agentcore.AgentResult {
	return agentcore.AgentResult{
		ID:        agentcore.AgentResultID("result-001"),
		MissionID: missioncore.MissionID("mission-001"),
		TaskID:    missioncore.TaskID("task-001"),
		AgentID:   agentcore.AgentID("agent-worker-001"),
		RunID:     agentcore.AgentRunID("run-001"),
		Summary:   agentcore.Summary{Text: "Implemented contract packages."},
		Deliverables: []agentcore.Deliverable{{
			ID:                    agentcore.DeliverableID("deliverable-001"),
			ExpectedDeliverableID: "expected-001",
			Title:                 "Contract packages",
			ArtifactRef:           agentcore.ArtifactRef{ID: "artifact-001", Kind: agentcore.ArtifactKindCode},
		}},
		Evidence: []agentcore.Evidence{{
			ID:                    agentcore.EvidenceID("evidence-001"),
			EvidenceRequirementID: "evidence-required-001",
			AcceptanceCriterionID: "acceptance-001",
			Kind:                  agentcore.EvidenceKindTestLog,
			ToolLogRef:            agentcore.ToolLogRef{ID: agentcore.ToolLogID("test-log-001")},
		}},
	}
}
