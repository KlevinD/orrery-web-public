package main

import (
	"context"
	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/improvement"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
	"testing"
	"time"
)

func TestProductionTaskBusinessFormalServicesCaptureOriginalAttempt(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	rt := &platform.Runtime{UnitOfWork: env.UnitOfWork, Clock: env.Clock, IDGenerator: env.IDGenerator}
	repo, _, delivery, qa := newProductionMissionTaskServices(rt)
	envelope := &agentcore.ExecutionEnvelope{TenantID: "org-a", RunID: "run-a", AgentID: "agent-a", AgentProfileID: "profile-a", MissionID: "mission-a", TaskID: "task-a"}
	if err := envelope.SealAt(env.Clock.Now()); err != nil {
		t.Fatal(err)
	}
	task := missiontask.TaskRecord{Version: 1, Status: missiontask.TaskStatusInProgress, Contract: missiontask.TaskContract{ID: "task-a", MissionID: "mission-a", Assignee: missiontask.TaskAssigneeRef{AgentProfileRef: &agentcore.AgentProfileRef{ID: "agent-a"}}, Goal: "original goal", QACheckRequired: true, ExecutionEnvelope: envelope}}
	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error { return repo.SaveTask(ctx, tx, task) }); err != nil {
		t.Fatal(err)
	}
	recorder := productionRuntimeEvidenceRecorder(agentcore.StoreTraceRecorder{Store: runtimeEvidenceTraceObjects{}}, productionTraceStore{evidenceCore: env.DB})
	input := agentcore.AgentInput{TenantID: "org-a", RunID: "run-a", Task: agentcore.TaskInput{ID: "task-a", MissionID: "mission-a", Goal: "original goal"}, ContextPack: agentcore.ContextPack{AgentID: "agent-a", TaskID: "task-a", MissionID: "mission-a"}}
	session, err := recorder.(agentcore.DurableTraceRecorder).BeginTrace(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	req := agentcore.RuntimeRequest{TenantID: "org-a", RunID: "run-a", AgentID: "agent-a", TaskID: "task-a", MissionID: "mission-a"}
	if err := session.RecordRequest(ctx, req); err != nil {
		t.Fatal(err)
	}
	attempt := session.(agentcore.RuntimeAttemptTraceSession).RuntimeAttemptID()
	if attempt == "" {
		t.Fatal("missing host attempt")
	}
	// The formal domain clock can be behind the machine clock in this fixture.
	env.Clock.Advance(time.Now().UTC().Sub(env.Clock.Now()) + time.Second)
	submitted, err := delivery.SubmitAgentResult(ctx, "task-a", agentcore.AgentResult{ID: "result-a", RunID: "run-a", AgentID: "agent-a", TaskID: "task-a", MissionID: "mission-a", Summary: agentcore.Summary{Text: "actual agent report"}, InternalTraceRef: session.Ref(), RuntimeAttemptID: attempt}, missiontask.SubmitOptions{IdempotencyKey: "submit"})
	if err != nil {
		t.Fatal(err)
	}
	if submitted.RuntimeAttemptID != attempt {
		t.Fatal("formal submission discarded attempt identity")
	}
	if _, err := qa.SubmitQAResult(ctx, "task-a", missiontask.QAResultRecord{CheckedBy: "reviewer-a"}, missiontask.QASubmitOptions{IdempotencyKey: "qa"}); err != nil {
		t.Fatal(err)
	}
	if err := session.Finish(ctx, agentcore.TraceTerminal{Status: agentcore.RuntimeStatusCompleted, CompletedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	evidence := improvement.NewRuntimeEvidenceStore(env.DB)
	got, err := evidence.BusinessAssessments(ctx, "mission:org-a/mission-a", attempt)
	if err != nil || len(got) != 2 || got[1].BusinessStatus != "qa_approved" {
		t.Fatalf("production business association failed: %+v %v", got, err)
	}
	if got[1].Outcome.ExecutionTenantID != "org-a" || got[1].Outcome.RuntimeAttemptID != attempt {
		t.Fatal("scope or attempt changed")
	}
}
