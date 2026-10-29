package main

import (
	"context"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestMissionTaskAgentResultCommitterClassifiesProjectionFailureAsPostCommit(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	projectionErr := errors.New("projection store unavailable")
	delivery := &recordingMissionTaskDelivery{}
	committer := missionTaskAgentResultCommitter{
		delivery: delivery,
		tasks: fixedMissionTaskReader{record: missiontask.TaskRecord{
			Contract: missiontask.TaskContract{ID: "task-1", MissionID: "mission-1"}, Status: missiontask.TaskStatusInProgress,
		}},
		unit: env.UnitOfWork, refresher: failingMissionProjectionRefresher{err: projectionErr},
	}
	result := agentcore.AgentResult{
		ID: "result-1", MissionID: "mission-1", TaskID: "task-1", AgentID: "agent-1", RunID: "run-1",
		Summary: agentcore.Summary{Text: "Committed."},
	}

	err := committer.SubmitAgentResult(ctx, result, agentcore.CommitOptions{TenantID: "tenant-1", IdempotencyKey: "run-1"})
	var postCommitErr agentcore.PostCommitPipelineError
	if !errors.As(err, &postCommitErr) || postCommitErr.Stage != "mission_projection_refresh" || !errors.Is(err, projectionErr) {
		t.Fatalf("error = %T %[1]v, want mission_projection_refresh PostCommitPipelineError", err)
	}
	if delivery.submissions != 1 || delivery.lastResult.ID != result.ID {
		t.Fatalf("committed submissions = %d result=%+v", delivery.submissions, delivery.lastResult)
	}
}

func TestMissionTaskAgentResultCommitterSkipsApprovedContinuationSubmission(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	delivery := &recordingMissionTaskDelivery{}
	committer := missionTaskAgentResultCommitter{
		delivery: delivery,
		tasks: fixedMissionTaskReader{record: missiontask.TaskRecord{
			Contract: missiontask.TaskContract{ID: "task-approved", MissionID: "mission-1"}, Status: missiontask.TaskStatusApproved,
		}},
		unit: env.UnitOfWork,
	}
	result := agentcore.AgentResult{
		ID: "result-approved-continuation", MissionID: "mission-1", TaskID: "task-approved", AgentID: "agent-1", RunID: "run-approved-continuation",
		Summary: agentcore.Summary{Text: "Approved side effect completed."},
	}

	if err := committer.SubmitAgentResult(ctx, result, agentcore.CommitOptions{ApprovedContinuation: true}); err != nil {
		t.Fatalf("SubmitAgentResult: %v", err)
	}
	if delivery.submissions != 0 {
		t.Fatalf("submissions = %d, want no duplicate submission", delivery.submissions)
	}
	if err := committer.SubmitAgentResult(ctx, result, agentcore.CommitOptions{}); err == nil {
		t.Fatal("ordinary result unexpectedly bypassed approved task status")
	}
}

type fixedMissionTaskReader struct{ record missiontask.TaskRecord }

func (r fixedMissionTaskReader) GetTask(context.Context, uow.Tx, missiontask.TaskID) (missiontask.TaskRecord, error) {
	return r.record, nil
}

type recordingMissionTaskDelivery struct {
	submissions int
	lastResult  agentcore.AgentResult
}

func (*recordingMissionTaskDelivery) AcceptTask(context.Context, missiontask.TaskID, agentcore.AgentInstanceRef) error {
	return nil
}

func (*recordingMissionTaskDelivery) StartTask(context.Context, missiontask.TaskID, agentcore.AgentInstanceRef) error {
	return nil
}

func (d *recordingMissionTaskDelivery) SubmitAgentResult(_ context.Context, _ missiontask.TaskID, result agentcore.AgentResult, _ missiontask.SubmitOptions) (missiontask.TaskSubmission, error) {
	d.submissions++
	d.lastResult = result
	return missiontask.TaskSubmission{ID: "submission-1"}, nil
}

type failingMissionProjectionRefresher struct{ err error }

func (r failingMissionProjectionRefresher) refreshMissionProjection(context.Context, string, missioncore.MissionID) error {
	return r.err
}
