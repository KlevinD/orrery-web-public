package main

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
	missionprojection "orrery_v2/internal/mission/projection"
	missiontask "orrery_v2/internal/mission/task"
)

func TestRefreshMissionProjectionRetriesConflict(t *testing.T) {
	ctx := context.Background()
	missionID := missioncore.MissionID("mission-refresh-conflict")
	base := missioncontrol.NewMemoryProjectionRepository()
	model := missionprojection.NewReadModel(missionID)
	model.Checkpoint.Version = 1
	if err := base.SaveForTenant(ctx, "global", *model); err != nil {
		t.Fatalf("seed projection: %v", err)
	}
	repo := &conflictOnceProductionProjectionRepository{MemoryProjectionRepository: base}
	events := missionprojection.NewMemoryEventReader([]missionevent.Envelope{{
		EventID:       "event-mission-refresh-conflict",
		MissionID:     missionID,
		StreamVersion: 1,
		EventType:     missionevent.EventMissionCreated,
		EventVersion:  1,
		Actor:         missionevent.ActorRef{Kind: missionevent.ActorKindSystem, ID: "system"},
		OccurredAt:    time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC),
		Payload: missionevent.MissionCreatedPayload{
			Title: "Retry projection", FinalGoal: "Persist refreshed view", PersonalAssistantOriginOwnerID: "account-1", PersonalAssistantOriginSubjectID: "user-1",
		},
	}})
	api := productionAPI{missionRepo: repo, eventReader: events}

	if err := api.refreshMissionProjection(ctx, "global", missionID); err != nil {
		t.Fatalf("refreshMissionProjection: %v", err)
	}
	if repo.saveAttempts != 2 {
		t.Fatalf("save attempts = %d, want 2", repo.saveAttempts)
	}
	updated, err := base.GetMissionControlForTenant(ctx, "global", missionID)
	if err != nil || updated.Mission.PersonalAssistantOriginOwnerID != "account-1" || updated.Mission.PersonalAssistantOriginSubjectID != "user-1" {
		t.Fatalf("refreshed PA origin = %+v err=%v", updated.Mission, err)
	}
}

func TestMergeReplayedDiscussionProjectionRejectsConflictingPersonalAssistantOrigin(t *testing.T) {
	model := missionprojection.NewReadModel("mission-refresh-conflict")
	model.Mission.PersonalAssistantOriginOwnerID = "account-1"
	model.Mission.PersonalAssistantOriginSubjectID = "user-1"
	replayed := missionprojection.NewReadModel("mission-refresh-conflict")
	replayed.Mission.PersonalAssistantOriginOwnerID = "account-2"
	replayed.Mission.PersonalAssistantOriginSubjectID = "user-1"
	if _, _, err := mergeReplayedDiscussionProjection(*model, *replayed); err != missioncontrol.ErrInvalidRequest {
		t.Fatalf("merge error = %v, want ErrInvalidRequest", err)
	}
}

func TestRuntimeTaskProjectionFromRecordIncludesCanonicalContractAndResult(t *testing.T) {
	at := time.Date(2026, 8, 30, 5, 10, 0, 0, time.UTC)
	profile := agentcore.AgentProfileRef{ID: "agent-validation"}
	record := missiontask.TaskRecord{
		Contract: missiontask.TaskContract{
			ID: "task-validation", MissionID: "mission-game", Assignee: missiontask.TaskAssigneeRef{AgentProfileRef: &profile},
			Goal: "Validate the playable game", WorkerPersonalGoal: "Verify a complete game loop", Priority: "high",
			ExpectedDeliverables: []missiontask.ExpectedDeliverable{{ID: "expected-1", Title: "Validation report"}},
		},
		Status: missiontask.TaskStatusApproved, StatusChangedAt: at,
	}
	submission := missiontask.TaskSubmission{AgentResultRef: "result-validation", SubmittedAt: at.Add(time.Minute)}

	card := runtimeTaskProjectionFromRecord(record, submission, true)
	if card.TaskID != "task-validation" || card.Assignee != "agent-validation" || card.Goal != "Validate the playable game" || card.Status != missionprojection.TaskStatusApproved || card.QAStatus != missionprojection.QAStatusApproved || card.LatestResultRef != "result-validation" || len(card.ExpectedDeliverables) != 1 || card.ExpectedDeliverables[0].ID != "expected-1" || !card.UpdatedAt.Equal(submission.SubmittedAt) {
		t.Fatalf("task projection = %+v", card)
	}
}

func TestMergeReplayedDiscussionProjectionPreservesCanonicalTaskContract(t *testing.T) {
	at := time.Date(2026, 8, 30, 5, 0, 0, 0, time.UTC)
	model := missionprojection.NewReadModel("mission-contract-merge")
	model.TaskBoard.Tasks = []missionprojection.TaskCardView{
		{
			TaskID: "task-validation", Assignee: "agent-validation", Goal: "Validate game", WorkerPersonalGoalSummary: "Verify one complete game",
			Status: missionprojection.TaskStatusAssigned, ExpectedDeliverables: []missionprojection.ExpectedDeliverableView{{ID: "expected-1", Title: "Validation report"}},
			UpdatedAt: at,
		},
		{
			TaskID: "task-approved", Assignee: "agent-builder", Goal: "Build game", Status: missionprojection.TaskStatusApproved,
			ExpectedDeliverables: []missionprojection.ExpectedDeliverableView{{ID: "expected-game", Title: "Playable game"}},
			LatestResultRef:      "result-game", QAStatus: missionprojection.QAStatusApproved, UpdatedAt: at,
		},
		{TaskID: "task-not-in-runtime-stream", Goal: "Preserve me", Status: missionprojection.TaskStatusCreated, UpdatedAt: at},
	}
	model.TaskBoard.Dependencies = []missionprojection.TaskDependencyView{{TaskID: "task-validation", DependsOn: "task-approved"}}
	replayed := missionprojection.NewReadModel("mission-contract-merge")
	replayed.TaskBoard.Tasks = []missionprojection.TaskCardView{
		{TaskID: "task-validation", Status: missionprojection.TaskStatusBlocked, BlockerRefs: []string{"runtime-failure:run-validation"}, UpdatedAt: at.Add(time.Minute)},
		{TaskID: "task-approved", Status: missionprojection.TaskStatusBlocked, BlockerRefs: []string{"runtime-failure:stale"}, UpdatedAt: at.Add(time.Minute)},
	}

	merged, changed, err := mergeReplayedDiscussionProjection(*model, *replayed)
	if err != nil || !changed {
		t.Fatalf("merge changed=%v err=%v", changed, err)
	}
	if len(merged.TaskBoard.Tasks) != 3 || len(merged.TaskBoard.Dependencies) != 1 {
		t.Fatalf("merged task board = %+v", merged.TaskBoard)
	}
	validation := merged.TaskBoard.Tasks[0]
	if validation.Goal != "Validate game" || validation.WorkerPersonalGoalSummary != "Verify one complete game" || validation.Assignee != "agent-validation" || validation.Status != missionprojection.TaskStatusBlocked || len(validation.ExpectedDeliverables) != 1 || validation.ExpectedDeliverables[0].ID != "expected-1" {
		t.Fatalf("validation task = %+v", validation)
	}
	approved := merged.TaskBoard.Tasks[1]
	if approved.Status != missionprojection.TaskStatusApproved || approved.LatestResultRef != "result-game" || approved.QAStatus != missionprojection.QAStatusApproved || len(approved.BlockerRefs) != 0 || len(approved.ExpectedDeliverables) != 1 {
		t.Fatalf("approved task regressed = %+v", approved)
	}
}

func TestRefreshMissionProjectionReconcilesCanonicalApprovedTask(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 8, 30, 5, 20, 0, 0, time.UTC)
	missionID := missioncore.MissionID("mission-canonical-task")
	base := missioncontrol.NewMemoryProjectionRepository()
	model := missionprojection.NewReadModel(missionID)
	model.Checkpoint.Version = 1
	model.TaskBoard.Tasks = []missionprojection.TaskCardView{{
		TaskID: "task-1", Status: missionprojection.TaskStatusBlocked,
		BlockerRefs: []string{"runtime-failure:stale-run"}, UpdatedAt: at,
	}}
	model.Blockers = []missionprojection.BlockerView{{BlockerID: "runtime-failure:stale-run", TaskID: "task-1"}}
	model.Discussion.Blockers = append([]missionprojection.BlockerView(nil), model.Blockers...)
	if err := base.SaveForTenant(ctx, "global", *model); err != nil {
		t.Fatal(err)
	}
	repo := &conflictOnceProductionProjectionRepository{MemoryProjectionRepository: base}
	events := missionprojection.NewMemoryEventReader([]missionevent.Envelope{{
		EventID: "event-runtime-started", MissionID: missionID, StreamVersion: 1,
		EventType: missionevent.EventAgentRunStarted, EventVersion: 1, OccurredAt: at.Add(time.Minute),
		Actor:   missionevent.ActorRef{Kind: missionevent.ActorKindAgent, ID: "agent-1"},
		Payload: missionevent.RuntimeLifecyclePayload{TaskID: "task-1", AgentID: "agent-1", RunID: "run-1", Status: "running"},
	}})
	api := productionAPI{
		missionRepo: repo, eventReader: events,
		runtimeTasks: staticRuntimeTaskProjectionReader{task: missionprojection.TaskCardView{
			TaskID: "task-1", Assignee: "agent-1", Goal: "Validate the game", Status: missionprojection.TaskStatusApproved,
			ExpectedDeliverables: []missionprojection.ExpectedDeliverableView{{ID: "expected-1", Title: "Validation report"}},
			LatestResultRef:      "result-1", QAStatus: missionprojection.QAStatusApproved, UpdatedAt: at.Add(2 * time.Minute),
		}},
	}

	if err := api.refreshMissionProjection(ctx, "global", missionID); err != nil {
		t.Fatalf("refreshMissionProjection: %v", err)
	}
	updated, err := base.GetMissionControl(ctx, missionID)
	if err != nil {
		t.Fatal(err)
	}
	task := updated.TaskBoard.Tasks[0]
	if task.Status != missionprojection.TaskStatusApproved || task.Goal != "Validate the game" || task.LatestResultRef != "result-1" || len(task.ExpectedDeliverables) != 1 || len(task.BlockerRefs) != 0 {
		t.Fatalf("reconciled task = %+v", task)
	}
	if len(updated.Blockers) != 0 || len(updated.Discussion.Blockers) != 0 {
		t.Fatalf("stale blockers = top:%+v discussion:%+v", updated.Blockers, updated.Discussion.Blockers)
	}
}

func TestRefreshMissionProjectionRestoresRuntimeApprovalRequests(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)
	missionID := missioncore.MissionID("mission-refresh-approval")
	base := missioncontrol.NewMemoryProjectionRepository()
	repo := &conflictOnceProductionProjectionRepository{MemoryProjectionRepository: base}
	model := missionprojection.NewReadModel(missionID)
	model.Checkpoint.Version = 1
	if err := base.SaveForTenant(ctx, "global", *model); err != nil {
		t.Fatalf("seed projection: %v", err)
	}
	events := missionprojection.NewMemoryEventReader([]missionevent.Envelope{{
		EventID: "event-runtime-blocked", MissionID: missionID, StreamVersion: 1,
		EventType: missionevent.EventAgentRunBlocked, EventVersion: 1, OccurredAt: at,
		Payload: missionevent.RuntimeFailurePayload{
			TaskID: "task-1", AgentID: "agent-1", RunID: "run-1", Code: "approval_required", Message: "Tool approval required",
			Metadata: map[string]string{"approval_request_id": "approval-1", "approval_type": "permission_request", "approval_tool_name": "shell"},
		},
		Actor: missionevent.ActorRef{Kind: missionevent.ActorKindAgent, ID: "agent-1"},
	}})
	api := productionAPI{missionRepo: repo, eventReader: events}

	if err := api.refreshMissionProjection(ctx, "global", missionID); err != nil {
		t.Fatalf("refreshMissionProjection: %v", err)
	}
	updated, err := base.GetMissionControl(ctx, missionID)
	if err != nil {
		t.Fatalf("load refreshed projection: %v", err)
	}
	if len(updated.ApprovalRequests) != 1 || updated.ApprovalRequests[0].RequestID != "approval-1" || updated.ApprovalRequests[0].RequestedBy != agentcore.AgentID("agent-1") {
		t.Fatalf("approval requests = %#v", updated.ApprovalRequests)
	}
}

func TestRefreshMissionProjectionClearsStaleRuntimeApprovalRequests(t *testing.T) {
	ctx := context.Background()
	missionID := missioncore.MissionID("mission-refresh-clear-approval")
	base := missioncontrol.NewMemoryProjectionRepository()
	model := missionprojection.NewReadModel(missionID)
	model.Checkpoint.Version = 1
	model.ApprovalRequests = []missionprojection.ApprovalRequestView{{RequestID: "stale-approval", ApprovalType: "permission_request", Status: "pending"}}
	if err := base.SaveForTenant(ctx, "global", *model); err != nil {
		t.Fatalf("seed projection: %v", err)
	}
	repo := &conflictOnceProductionProjectionRepository{MemoryProjectionRepository: base}
	events := missionprojection.NewMemoryEventReader([]missionevent.Envelope{{
		EventID: "event-refresh-without-approval", MissionID: missionID, StreamVersion: 1,
		EventType: missionevent.EventMissionCreated, EventVersion: 1,
		Actor:      missionevent.ActorRef{Kind: missionevent.ActorKindSystem, ID: "system"},
		OccurredAt: time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC),
		Payload:    missionevent.MissionCreatedPayload{Title: "Clear stale approval", FinalGoal: "Project the replayed stream"},
	}})
	api := productionAPI{missionRepo: repo, eventReader: events}
	if err := api.refreshMissionProjection(ctx, "global", missionID); err != nil {
		t.Fatalf("refreshMissionProjection: %v", err)
	}
	updated, err := base.GetMissionControl(ctx, missionID)
	if err != nil {
		t.Fatalf("load refreshed projection: %v", err)
	}
	if len(updated.ApprovalRequests) != 0 {
		t.Fatalf("approval requests = %#v, want cleared", updated.ApprovalRequests)
	}
}

type conflictOnceProductionProjectionRepository struct {
	*missioncontrol.MemoryProjectionRepository
	saveAttempts int
}

type staticRuntimeTaskProjectionReader struct {
	task missionprojection.TaskCardView
	err  error
}

func (r staticRuntimeTaskProjectionReader) ResolveRuntimeTask(context.Context, string, missioncore.MissionID, missioncore.TaskID) (missionprojection.TaskCardView, error) {
	return r.task, r.err
}

func (r *conflictOnceProductionProjectionRepository) SaveForTenant(ctx context.Context, tenantID string, model missionprojection.ReadModel) error {
	r.saveAttempts++
	if r.saveAttempts == 1 {
		return missioncontrol.ErrConflict
	}
	return r.MemoryProjectionRepository.SaveForTenant(ctx, tenantID, model)
}

func (r *conflictOnceProductionProjectionRepository) Delete(context.Context, missioncore.MissionID) error {
	return nil
}
