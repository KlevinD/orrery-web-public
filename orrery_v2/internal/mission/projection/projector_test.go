package projection_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/mission/projection"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/logging"
)

func TestProjectorBuildsMissionControlReadModelAcrossViews(t *testing.T) {
	ctx := context.Background()
	projector := projection.NewProjector()
	model := projection.NewReadModel("mission-1")

	for _, event := range missionScenarioEvents() {
		if err := projector.Apply(ctx, model, event); err != nil {
			t.Fatalf("Apply(%s) returned error: %v", event.EventType, err)
		}
	}

	if model.Mission.Title != "Launch stage 4" || model.Mission.FinalGoal != "Production event projection" || model.Mission.Status != projection.MissionStatusActive {
		t.Fatalf("unexpected mission view: %+v", model.Mission)
	}
	if len(model.Team.Members) != 1 || model.Team.Members[0].DisplayName != "Ari" || model.Team.Members[0].Status != projection.TeamMemberJoined {
		t.Fatalf("unexpected team members: %+v", model.Team.Members)
	}
	if len(model.Team.Assignments) != 1 || model.Team.Assignments[0].MissionRole != "worker" {
		t.Fatalf("unexpected assignments: %+v", model.Team.Assignments)
	}
	if len(model.TaskBoard.Tasks) != 1 {
		t.Fatalf("task count = %d, want 1", len(model.TaskBoard.Tasks))
	}
	task := model.TaskBoard.Tasks[0]
	if task.TaskID != "task-1" || task.Status != projection.TaskStatusReworkRequested || task.QAStatus != projection.QAStatusRejected {
		t.Fatalf("unexpected task card: %+v", task)
	}
	if task.LatestResultRef != "result-1" || len(task.BlockerRefs) != 1 {
		t.Fatalf("task did not retain result/blocker refs: %+v", task)
	}
	if task.Priority != "high" || !task.DueAt.Equal(time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("task did not retain priority/due metadata: %+v", task)
	}
	if len(model.Evidence.EvidenceItems) != 1 || model.Evidence.EvidenceItems[0].EvidenceID != "evidence-1" {
		t.Fatalf("unexpected evidence view: %+v", model.Evidence)
	}
	if len(model.QA.Checks) != 1 || len(model.QA.Rejections) != 1 || len(model.QA.ReworkRequests) != 1 {
		t.Fatalf("unexpected qa view: %+v", model.QA)
	}
	if model.Discussion.DiscussionGroupID != "discussion-1" || len(model.Discussion.Messages) != 1 || len(model.Discussion.PendingMentions) != 1 {
		t.Fatalf("unexpected discussion view: %+v", model.Discussion)
	}
	if model.Discussion.Messages[0].AuthorType != "agent" {
		t.Fatalf("discussion message author type = %q, want agent", model.Discussion.Messages[0].AuthorType)
	}
	if model.InboxSummary.OpenCount != 1 || len(model.InboxSummary.Items) != 1 {
		t.Fatalf("unexpected inbox summary: %+v", model.InboxSummary)
	}
	if len(model.LatestDecisions) != 1 || len(model.Blockers) != 1 {
		t.Fatalf("missing decisions/blockers: decisions=%+v blockers=%+v", model.LatestDecisions, model.Blockers)
	}
	if len(model.ProgressGraph.Nodes) != len(missionScenarioEvents()) {
		t.Fatalf("progress node count = %d, want one per real event", len(model.ProgressGraph.Nodes))
	}
	for _, node := range model.ProgressGraph.Nodes {
		if node.EventType == "TaskStarted" {
			t.Fatalf("progress graph pre-generated unobserved TaskStarted node: %+v", model.ProgressGraph.Nodes)
		}
	}
}

func TestProjectorRebuildsTodoMissionPauseFromDurableEvent(t *testing.T) {
	at := time.Date(2026, 6, 6, 10, 6, 0, 0, time.UTC)
	model := projection.NewReadModel("mission-1")
	projector := projection.NewProjector()
	for _, item := range []missionevent.Envelope{
		event(1, "evt-mission-created", missionevent.EventMissionCreated, missionevent.MissionCreatedPayload{Title: "Todo mission", FinalGoal: "Execute approved Todo"}, at),
		event(2, "evt-mission-paused", missionevent.EventMissionPaused, missionevent.MissionPausedPayload{TodoID: "todo-1", Reason: "Todo scope changed"}, at.Add(time.Minute)),
	} {
		if err := projector.Apply(context.Background(), model, item); err != nil {
			t.Fatalf("Apply(%s): %v", item.EventType, err)
		}
	}
	if model.Mission.Status != projection.MissionStatusPaused {
		t.Fatalf("Mission status = %s, want paused", model.Mission.Status)
	}
	last := model.ProgressGraph.Nodes[len(model.ProgressGraph.Nodes)-1]
	if last.EventType != string(missionevent.EventMissionPaused) || last.RefType != "todo" || last.RefID != "todo-1" || last.Details != "Todo scope changed" || last.Status != "blocked" {
		t.Fatalf("pause progress node = %+v", last)
	}
}

func TestProjectorCancelsNonTerminalTasksWithMission(t *testing.T) {
	at := time.Date(2026, 8, 24, 11, 0, 0, 0, time.UTC)
	model := projection.NewReadModel("mission-1")
	projector := projection.NewProjector()
	if err := projector.Apply(context.Background(), model, event(1, "evt-created", missionevent.EventMissionCreated, missionevent.MissionCreatedPayload{Title: "Canary", FinalGoal: "Verify cancellation"}, at)); err != nil {
		t.Fatal(err)
	}
	model.TaskBoard.Tasks = []projection.TaskCardView{
		{TaskID: "task-active", Status: projection.TaskStatusAssigned, UpdatedAt: at},
		{TaskID: "task-approved", Status: projection.TaskStatusApproved, UpdatedAt: at},
		{TaskID: "task-rejected", Status: projection.TaskStatusRejected, UpdatedAt: at},
	}
	cancelledAt := at.Add(time.Minute)
	if err := projector.Apply(context.Background(), model, event(2, "evt-cancelled", missionevent.EventMissionCancelled, missionevent.MissionClosedPayload{Reason: "polluted Mission"}, cancelledAt)); err != nil {
		t.Fatal(err)
	}

	if model.Mission.Status != projection.MissionStatusCancelled {
		t.Fatalf("Mission status = %s", model.Mission.Status)
	}
	if model.TaskBoard.Tasks[0].Status != projection.TaskStatusCancelled || !model.TaskBoard.Tasks[0].UpdatedAt.Equal(cancelledAt) {
		t.Fatalf("active task = %+v", model.TaskBoard.Tasks[0])
	}
	if model.TaskBoard.Tasks[1].Status != projection.TaskStatusApproved || model.TaskBoard.Tasks[2].Status != projection.TaskStatusRejected {
		t.Fatalf("terminal tasks changed = %+v", model.TaskBoard.Tasks[1:])
	}
	last := model.ProgressGraph.Nodes[len(model.ProgressGraph.Nodes)-1]
	if last.Status != "cancelled" || last.Details != "polluted Mission" {
		t.Fatalf("cancel progress = %+v", last)
	}
}

func TestProjectorRebuildsTodoMissionExecutionMetadata(t *testing.T) {
	at := time.Date(2026, 7, 25, 10, 6, 0, 0, time.UTC)
	model := projection.NewReadModel("mission-1")
	payload := missionevent.MissionCreatedPayload{
		Title: "Todo mission", FinalGoal: "Execute approved Todo", OriginKind: "todo", OriginID: "todo-1",
		OriginTodoID: "todo-1", ExecutionAttempt: 3, DiscussionMode: "existing_conversation", OriginDevelopmentProjectID: "project-1",
		PersonalAssistantOriginOwnerID: "account-1", PersonalAssistantOriginSubjectID: "user-1",
	}
	if err := projection.NewProjector().Apply(context.Background(), model, event(1, "evt-mission-created", missionevent.EventMissionCreated, payload, at)); err != nil {
		t.Fatal(err)
	}
	if model.Mission.OriginKind != "todo" || model.Mission.OriginID != "todo-1" || model.Mission.OriginTodoID != "todo-1" || model.Mission.ExecutionAttempt != 3 || model.Mission.DiscussionMode != "existing_conversation" || model.Mission.OriginDevelopmentProjectID != "project-1" || model.Mission.PersonalAssistantOriginOwnerID != "account-1" || model.Mission.PersonalAssistantOriginSubjectID != "user-1" {
		t.Fatalf("mission execution metadata = %+v", model.Mission)
	}
	encoded, err := json.Marshal(model.Mission)
	if err != nil || !strings.Contains(string(encoded), `"personal_assistant_origin_owner_id":"account-1"`) || !strings.Contains(string(encoded), `"personal_assistant_origin_subject_id":"user-1"`) {
		t.Fatalf("MissionView JSON = %s, err=%v", encoded, err)
	}
}

func TestProjectorRejectsPartialPersonalAssistantMissionOrigin(t *testing.T) {
	model := projection.NewReadModel("mission-1")
	err := projection.NewProjector().Apply(context.Background(), model, event(1, "evt-mission-created", missionevent.EventMissionCreated, missionevent.MissionCreatedPayload{
		Title: "Mission", FinalGoal: "Reject partial PA origin", PersonalAssistantOriginOwnerID: "account-1",
	}, time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)))
	if !errors.Is(err, projection.ErrInvalidEventSequence) {
		t.Fatalf("Apply error = %v, want ErrInvalidEventSequence", err)
	}
}

func TestProjectorRejectsSecondMissionCreatedWithoutReplacingOrigin(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 7, 25, 10, 6, 0, 0, time.UTC)
	model := projection.NewReadModel("mission-1")
	projector := projection.NewProjector()
	created := event(1, "evt-mission-created", missionevent.EventMissionCreated, missionevent.MissionCreatedPayload{
		Title: "Todo mission", FinalGoal: "Execute approved Todo", OriginKind: "todo", OriginID: "todo-1", OriginTodoID: "todo-1",
		ExecutionAttempt: 3, DiscussionMode: "existing_conversation", OriginDevelopmentProjectID: "project-1",
	}, at)
	if err := projector.Apply(ctx, model, created); err != nil {
		t.Fatal(err)
	}
	if err := projector.Apply(ctx, model, created); err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	second := event(2, "evt-mission-created-forged", missionevent.EventMissionCreated, missionevent.MissionCreatedPayload{
		Title: "Forged", FinalGoal: "Replace origin", OriginKind: "todo", OriginID: "todo-2", OriginTodoID: "todo-2",
		ExecutionAttempt: 99, DiscussionMode: "new_conversation", OriginDevelopmentProjectID: "project-2",
	}, at.Add(time.Minute))
	if err := projector.Apply(ctx, model, second); !errors.Is(err, projection.ErrInvalidEventSequence) {
		t.Fatalf("second MissionCreated error = %v, want ErrInvalidEventSequence", err)
	}
	if model.Checkpoint.Version != 1 || model.Mission.OriginTodoID != "todo-1" || model.Mission.ExecutionAttempt != 3 || model.Mission.OriginDevelopmentProjectID != "project-1" {
		t.Fatalf("mission origin changed after rejected event: %+v checkpoint=%+v", model.Mission, model.Checkpoint)
	}
}

func TestProjectorMapsStaffingFactEventsIntoTeamView(t *testing.T) {
	ctx := context.Background()
	projector := projection.NewProjector()
	model := projection.NewReadModel("mission-1")
	at := time.Date(2026, 5, 31, 9, 0, 0, 0, time.UTC)
	events := []missionevent.Envelope{
		event(1, "evt-mission-created", missionevent.EventMissionCreated, missionevent.MissionCreatedPayload{Title: "Mission", FinalGoal: "Bootstrap system roles"}, at),
		event(2, "evt-lead-joined", missionevent.EventAgentJoined, missionevent.StaffingFactPayload{
			TeamID: "team-mission-1", MemberID: "member-lead", AgentProfileID: "pm-agent", AgentInstanceID: "pm-agent", MissionRole: "Lead", Reason: "Default Mission system role bootstrap",
		}, at.Add(time.Minute)),
		event(3, "evt-lead-assignment", missionevent.EventMissionAssignmentCreated, missionevent.StaffingFactPayload{
			TeamID: "team-mission-1", MemberID: "member-lead", AssignmentID: "assignment-lead", AgentProfileID: "pm-agent", AgentInstanceID: "pm-agent", MissionRole: "Lead", Reason: "Default Mission system role bootstrap",
		}, at.Add(2*time.Minute)),
	}

	for _, event := range events {
		if err := projector.Apply(ctx, model, event); err != nil {
			t.Fatalf("Apply(%s) returned error: %v", event.EventType, err)
		}
	}

	if len(model.Team.Members) != 1 || model.Team.Members[0].AgentID != "pm-agent" || model.Team.Members[0].MissionRole != "Lead" || model.Team.Members[0].Status != projection.TeamMemberJoined {
		t.Fatalf("staffing fact member was not projected: %+v", model.Team.Members)
	}
	if len(model.Team.Assignments) != 1 || model.Team.Assignments[0].AssignmentID != "assignment-lead" || model.Team.Assignments[0].AgentID != "pm-agent" || model.Team.Assignments[0].MissionRole != "Lead" {
		t.Fatalf("staffing fact assignment was not projected: %+v", model.Team.Assignments)
	}
	if len(model.TaskBoard.Tasks) != 0 {
		t.Fatalf("mission-level system role assignment should not create a task card: %+v", model.TaskBoard.Tasks)
	}
}

func TestProjectorKeepsStaffingProfileAsStableTeamMemberIdentity(t *testing.T) {
	ctx := context.Background()
	projector := projection.NewProjector()
	model := projection.NewReadModel("mission-1")
	at := time.Date(2026, 5, 31, 9, 0, 0, 0, time.UTC)
	events := []missionevent.Envelope{
		event(1, "evt-mission-created", missionevent.EventMissionCreated, missionevent.MissionCreatedPayload{Title: "Mission", FinalGoal: "Keep stable agent identity"}, at),
		event(2, "evt-worker-joined", missionevent.EventAgentJoined, missionevent.StaffingFactPayload{
			TeamID: "team-mission-1", MemberID: "member-worker", AgentProfileID: "profile-worker", AgentInstanceID: "profile-worker", AgentKind: "ai", MissionRole: "Worker",
		}, at.Add(time.Minute)),
		event(3, "evt-worker-assignment", missionevent.EventMissionAssignmentCreated, missionevent.StaffingFactPayload{
			TeamID: "team-mission-1", MemberID: "member-worker", AssignmentID: "assignment-worker", AgentProfileID: "profile-worker", AgentInstanceID: "profile-worker", AgentKind: "ai", MissionRole: "Worker",
		}, at.Add(2*time.Minute)),
	}

	for _, event := range events {
		if err := projector.Apply(ctx, model, event); err != nil {
			t.Fatalf("Apply(%s) returned error: %v", event.EventType, err)
		}
	}

	if len(model.Team.Members) != 1 {
		t.Fatalf("team member count = %d, want 1: %+v", len(model.Team.Members), model.Team.Members)
	}
	member := model.Team.Members[0]
	if member.AgentID != "profile-worker" || member.AgentProfileID != "profile-worker" || member.AgentInstanceID != "profile-worker" || member.AgentKind != "ai" {
		t.Fatalf("team member identity = %+v, want stable profile identity", member)
	}
	if len(model.Team.Assignments) != 1 || model.Team.Assignments[0].AgentID != "profile-worker" || model.Team.Assignments[0].AgentInstanceID != "profile-worker" {
		t.Fatalf("assignment identity = %+v, want stable profile identity", model.Team.Assignments)
	}
}

func TestProjectorDoesNotProjectStaffingIdentityFromScopedInstance(t *testing.T) {
	ctx := context.Background()
	projector := projection.NewProjector()
	model := projection.NewReadModel("mission-1")
	at := time.Date(2026, 5, 31, 9, 0, 0, 0, time.UTC)
	events := []missionevent.Envelope{
		event(1, "evt-worker-joined", missionevent.EventAgentJoined, missionevent.StaffingFactPayload{
			TeamID: "team-mission-1", MemberID: "member-worker", AgentProfileID: "profile-worker", AgentInstanceID: "profile-worker:mission-1", AgentKind: "ai", MissionRole: "Worker",
		}, at),
		event(2, "evt-worker-assignment", missionevent.EventMissionAssignmentCreated, missionevent.StaffingFactPayload{
			TeamID: "team-mission-1", MemberID: "member-worker", AssignmentID: "assignment-worker", AgentProfileID: "profile-worker", AgentInstanceID: "profile-worker:mission-1", AgentKind: "ai", MissionRole: "Worker",
		}, at.Add(time.Minute)),
	}

	for _, event := range events {
		if err := projector.Apply(ctx, model, event); err != nil {
			t.Fatalf("Apply(%s) returned error: %v", event.EventType, err)
		}
	}

	if len(model.Team.Members) != 0 || len(model.Team.Assignments) != 0 {
		t.Fatalf("scoped instance staffing facts were projected: members=%+v assignments=%+v", model.Team.Members, model.Team.Assignments)
	}
}

func TestProjectorDoesNotProjectStaffingIdentityFromInstanceOnly(t *testing.T) {
	ctx := context.Background()
	projector := projection.NewProjector()
	model := projection.NewReadModel("mission-1")
	at := time.Date(2026, 5, 31, 9, 0, 0, 0, time.UTC)
	events := []missionevent.Envelope{
		event(1, "evt-worker-joined", missionevent.EventAgentJoined, missionevent.StaffingFactPayload{
			TeamID: "team-mission-1", MemberID: "member-worker", AgentInstanceID: "run-worker-1", AgentKind: "ai", MissionRole: "Worker",
		}, at),
		event(2, "evt-worker-assignment", missionevent.EventMissionAssignmentCreated, missionevent.StaffingFactPayload{
			TeamID: "team-mission-1", MemberID: "member-worker", AssignmentID: "assignment-worker", AgentInstanceID: "run-worker-1", AgentKind: "ai", MissionRole: "Worker",
		}, at.Add(time.Minute)),
	}

	for _, event := range events {
		if err := projector.Apply(ctx, model, event); err != nil {
			t.Fatalf("Apply(%s) returned error: %v", event.EventType, err)
		}
	}

	if len(model.Team.Members) != 0 || len(model.Team.Assignments) != 0 {
		t.Fatalf("instance-only staffing facts were projected: members=%+v assignments=%+v", model.Team.Members, model.Team.Assignments)
	}
}

func TestProjectorCarriesReportDetailsOnProgressNodes(t *testing.T) {
	ctx := context.Background()
	projector := projection.NewProjector()
	model := projection.NewReadModel("mission-1")
	at := time.Date(2026, 5, 31, 9, 0, 0, 0, time.UTC)
	events := []missionevent.Envelope{
		event(1, "evt-mission-created", missionevent.EventMissionCreated, missionevent.MissionCreatedPayload{Title: "Mission", FinalGoal: "Ship report details"}, at),
		event(2, "evt-result", missionevent.EventAgentResultSubmitted, &missionevent.AgentResultSubmittedPayload{TaskID: "task-1", AgentID: "agent-1", ResultRef: "result-1", Summary: "Worker report streamed summary"}, at.Add(time.Minute)),
		event(3, "evt-acceptance-report", missionevent.EventAcceptanceReportCreated, &missionevent.QAPayload{TaskID: "task-1", QARef: "qa-1", Status: "acceptance_report_created", Reason: "Acceptance report streamed summary"}, at.Add(2*time.Minute)),
	}

	for _, event := range events {
		if err := projector.Apply(ctx, model, event); err != nil {
			t.Fatalf("Apply(%s) returned error: %v", event.EventType, err)
		}
	}

	resultNode := model.ProgressGraph.Nodes[1]
	if resultNode.Details != "Worker report streamed summary" {
		t.Fatalf("result progress details = %q", resultNode.Details)
	}
	reportNode := model.ProgressGraph.Nodes[2]
	if reportNode.RefType != "qa" || reportNode.RefID != "qa-1" || reportNode.Details != "Acceptance report streamed summary" {
		t.Fatalf("acceptance report node = %+v", reportNode)
	}
}

func TestProjectorDoesNotExposeNonReportEventReasonsAsProgressDetails(t *testing.T) {
	ctx := context.Background()
	projector := projection.NewProjector()
	model := projection.NewReadModel("mission-1")
	at := time.Date(2026, 5, 31, 9, 0, 0, 0, time.UTC)
	events := []missionevent.Envelope{
		event(1, "evt-mission-created", missionevent.EventMissionCreated, missionevent.MissionCreatedPayload{Title: "Mission", FinalGoal: "Keep event reasons scoped"}, at),
		event(2, "evt-qa-rejected", missionevent.EventQARejected, &missionevent.QAPayload{TaskID: "task-1", QARef: "qa-1", Status: "rejected", Reason: "internal review reason"}, at.Add(time.Minute)),
	}

	for _, event := range events {
		if err := projector.Apply(ctx, model, event); err != nil {
			t.Fatalf("Apply(%s) returned error: %v", event.EventType, err)
		}
	}

	if model.ProgressGraph.Nodes[1].Details != "" {
		t.Fatalf("non-report progress details = %q", model.ProgressGraph.Nodes[1].Details)
	}
}

func TestProjectorClosesRequestedQACheckWhenResultUsesSameReference(t *testing.T) {
	ctx := context.Background()
	projector := projection.NewProjector()
	model := projection.NewReadModel("mission-1")
	at := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)
	events := []missionevent.Envelope{
		event(1, "evt-qa-request", missionevent.EventQARequested, missionevent.QAPayload{TaskID: "task-1", QARef: "qa-request-task-1", Status: "requested", Message: "Review"}, at),
		event(2, "evt-qa-approved", missionevent.EventQAApproved, missionevent.QAPayload{TaskID: "task-1", QARef: "qa-request-task-1", Status: "approved", Message: "Approved"}, at.Add(time.Minute)),
	}
	for _, event := range events {
		if err := projector.Apply(ctx, model, event); err != nil {
			t.Fatalf("Apply(%s): %v", event.EventType, err)
		}
	}
	if len(model.QA.Checks) != 1 || model.QA.Checks[0].Status != projection.QAStatusApproved || model.QA.Checks[0].Message != "Approved" {
		t.Fatalf("QA checks = %#v", model.QA.Checks)
	}
}

func TestProjectorProjectsAgentRunLifecycleIntoRuntimeSessions(t *testing.T) {
	ctx := context.Background()
	projector := projection.NewProjector()
	model := projection.NewReadModel("mission-1")
	at := time.Date(2026, 6, 6, 9, 0, 0, 0, time.UTC)

	events := []missionevent.Envelope{
		event(1, "evt-mission-created", missionevent.EventMissionCreated, missionevent.MissionCreatedPayload{Title: "Mission", FinalGoal: "Run agents"}, at),
		event(2, "evt-task-created", missionevent.EventTaskContractCreated, missionevent.TaskContractCreatedPayload{Contract: missiontask.TaskContract{
			ID: "task-1", MissionID: "mission-1", Goal: "Run implementation",
		}}, at.Add(time.Minute)),
		event(3, "evt-run-queued", missionevent.EventAgentRunQueued, missionevent.RuntimeLifecyclePayload{
			TaskID: "task-1", AgentID: "agent-1", RunID: "run-1", RuntimeKind: "codex", QueueID: "mission:mission-1", Status: "queued",
		}, at.Add(2*time.Minute)),
		event(4, "evt-run-started", missionevent.EventAgentRunStarted, missionevent.RuntimeLifecyclePayload{
			TaskID: "task-1", AgentID: "agent-1", RunID: "run-1", RuntimeKind: "codex", QueueID: "mission:mission-1", Status: "running", WorkerID: "worker-1", Message: "claimed by worker",
		}, at.Add(3*time.Minute)),
		event(5, "evt-run-completed", missionevent.EventAgentRunCompleted, missionevent.RuntimeLifecyclePayload{
			TaskID: "task-1", AgentID: "agent-1", RunID: "run-1", RuntimeKind: "codex", QueueID: "mission:mission-1", Status: "completed", Message: "done",
			Usage: map[string]agentcore.TokenUsage{
				"codex": {InputTokens: 1200, OutputTokens: 340, CacheReadTokens: 60},
			},
		}, at.Add(4*time.Minute)),
	}

	for _, event := range events {
		if err := projector.Apply(ctx, model, event); err != nil {
			t.Fatalf("Apply(%s) returned error: %v", event.EventType, err)
		}
	}

	if len(model.RuntimeSessions) != 1 {
		t.Fatalf("runtime sessions = %+v, want one session", model.RuntimeSessions)
	}
	session := model.RuntimeSessions[0]
	if session.RunID != "run-1" || session.AgentID != "agent-1" || session.TaskID != "task-1" || session.RuntimeKind != "codex" || session.QueueID != "mission:mission-1" {
		t.Fatalf("runtime session identity = %+v", session)
	}
	if session.Status != projection.RuntimeSessionCompleted || session.LastEvent != string(missionevent.EventAgentRunCompleted) || session.WorkerID != "worker-1" || session.LastMessage != "done" {
		t.Fatalf("runtime session state = %+v", session)
	}
	if session.StartedAt.IsZero() || !session.UpdatedAt.Equal(at.Add(4*time.Minute)) {
		t.Fatalf("runtime session timestamps = %+v", session)
	}
	usage := session.Usage["codex"]
	if usage.InputTokens != 1200 || usage.OutputTokens != 340 || usage.CacheReadTokens != 60 {
		t.Fatalf("runtime session usage = %+v", session.Usage)
	}
}

func TestProjectorKeepsTerminalRuntimeSessionWhenLateLifecycleEventArrives(t *testing.T) {
	ctx := context.Background()
	projector := projection.NewProjector()
	model := projection.NewReadModel("mission-1")
	at := time.Date(2026, 6, 6, 13, 0, 0, 0, time.UTC)
	events := []missionevent.Envelope{
		event(1, "evt-run-queued", missionevent.EventAgentRunQueued, missionevent.RuntimeLifecyclePayload{
			TaskID: "task-1", AgentID: "agent-1", RunID: "run-1", RuntimeKind: "codex", Status: "queued", Message: "queued",
		}, at),
		event(2, "evt-run-cancelled", missionevent.EventAgentRunCancelled, missionevent.RuntimeFailurePayload{
			TaskID: "task-1", AgentID: "agent-1", RunID: "run-1", Code: "agent_runtime_cancelled", Message: "operator cancelled run",
		}, at.Add(time.Second)),
		event(3, "evt-run-completed-late", missionevent.EventAgentRunCompleted, missionevent.RuntimeLifecyclePayload{
			TaskID: "task-1", AgentID: "agent-1", RunID: "run-1", RuntimeKind: "codex", Status: "completed", Message: "late completion",
		}, at.Add(2*time.Second)),
	}

	for _, event := range events {
		if err := projector.Apply(ctx, model, event); err != nil {
			t.Fatalf("Apply(%s) returned error: %v", event.EventType, err)
		}
	}

	if len(model.RuntimeSessions) != 1 {
		t.Fatalf("runtime sessions = %+v, want one", model.RuntimeSessions)
	}
	session := model.RuntimeSessions[0]
	if session.Status != projection.RuntimeSessionCancelled || session.LastEvent != string(missionevent.EventAgentRunCancelled) || session.ErrorSummary != "operator cancelled run" {
		t.Fatalf("runtime session = %+v, want cancelled terminal state", session)
	}
}

func TestProjectorDoesNotRegressApprovedTaskForLateRuntimeRetryEvents(t *testing.T) {
	ctx := context.Background()
	projector := projection.NewProjector()
	model := projection.NewReadModel("mission-1")
	at := time.Date(2026, 6, 6, 13, 30, 0, 0, time.UTC)
	events := []missionevent.Envelope{
		event(1, "evt-mission-created", missionevent.EventMissionCreated, missionevent.MissionCreatedPayload{Title: "Mission", FinalGoal: "Keep completed work terminal"}, at),
		event(2, "evt-task-contract", missionevent.EventTaskContractCreated, missionevent.TaskContractCreatedPayload{Contract: missiontask.TaskContract{
			ID: "task-1", MissionID: "mission-1", Goal: "Build game", WorkerPersonalGoal: "Deliver playable game",
		}}, at.Add(time.Minute)),
		event(3, "evt-task-approved", missionevent.EventTaskApproved, missionevent.TaskStatusPayload{TaskID: "task-1"}, at.Add(2*time.Minute)),
		event(4, "evt-stale-run-queued", missionevent.EventAgentRunQueued, missionevent.RuntimeLifecyclePayload{
			TaskID: "task-1", AgentID: "agent-1", RunID: "run-stale", RuntimeKind: "codex", Status: "queued",
		}, at.Add(3*time.Minute)),
		event(5, "evt-stale-run-started", missionevent.EventAgentRunStarted, missionevent.RuntimeLifecyclePayload{
			TaskID: "task-1", AgentID: "agent-1", RunID: "run-stale", RuntimeKind: "codex", Status: "running",
		}, at.Add(4*time.Minute)),
		event(6, "evt-stale-run-failed", missionevent.EventAgentRunFailed, missionevent.RuntimeFailurePayload{
			TaskID: "task-1", AgentID: "agent-1", RunID: "run-stale", Code: "agent_runtime_failed", Message: "stale retry failed",
		}, at.Add(5*time.Minute)),
	}

	for _, event := range events {
		if err := projector.Apply(ctx, model, event); err != nil {
			t.Fatalf("Apply(%s) returned error: %v", event.EventType, err)
		}
	}

	if len(model.TaskBoard.Tasks) != 1 || model.TaskBoard.Tasks[0].Status != projection.TaskStatusApproved || len(model.TaskBoard.Tasks[0].BlockerRefs) != 0 {
		t.Fatalf("late runtime retry regressed approved task: %+v", model.TaskBoard.Tasks)
	}
	if len(model.Blockers) != 0 || len(model.Discussion.Blockers) != 0 {
		t.Fatalf("late runtime retry created active blockers: blockers=%+v discussion=%+v", model.Blockers, model.Discussion.Blockers)
	}
	if len(model.RuntimeSessions) != 1 || model.RuntimeSessions[0].Status != projection.RuntimeSessionFailed {
		t.Fatalf("stale retry history was not retained: %+v", model.RuntimeSessions)
	}
}

func TestProjectorTreatsQASkippedAsDefaultApproval(t *testing.T) {
	ctx := context.Background()
	projector := projection.NewProjector()
	model := projection.NewReadModel("mission-1")
	at := time.Date(2026, 5, 31, 9, 0, 0, 0, time.UTC)
	events := []missionevent.Envelope{
		event(1, "evt-mission-created", missionevent.EventMissionCreated, missionevent.MissionCreatedPayload{Title: "Mission", FinalGoal: "Approve QA-disabled task"}, at),
		event(2, "evt-task-contract", missionevent.EventTaskContractCreated, missionevent.TaskContractCreatedPayload{Contract: missiontask.TaskContract{
			ID: "task-1", MissionID: "mission-1", Goal: "Build route", WorkerPersonalGoal: "Deliver route",
		}}, at.Add(time.Minute)),
		event(3, "evt-result", missionevent.EventAgentResultSubmitted, missionevent.AgentResultSubmittedPayload{TaskID: "task-1", AgentID: "agent-1", ResultRef: "result-1"}, at.Add(2*time.Minute)),
		event(4, "evt-qa-skipped", missionevent.EventQASkipped, missionevent.QAPayload{TaskID: "task-1", QARef: "qa-skip-submission-1", Status: "approved", Reason: "qa check disabled"}, at.Add(3*time.Minute)),
	}

	for _, event := range events {
		if err := projector.Apply(ctx, model, event); err != nil {
			t.Fatalf("Apply(%s) returned error: %v", event.EventType, err)
		}
	}

	if len(model.TaskBoard.Tasks) != 1 || model.TaskBoard.Tasks[0].Status != projection.TaskStatusApproved || model.TaskBoard.Tasks[0].QAStatus != projection.QAStatusApproved {
		t.Fatalf("QA skip should approve task by default: %+v", model.TaskBoard.Tasks)
	}
	if len(model.QA.Checks) != 0 || len(model.QA.Approvals) != 1 || model.QA.Approvals[0].QARef != "qa-skip-submission-1" {
		t.Fatalf("QA skip should create one approval and no pending checks: %+v", model.QA)
	}
}

func TestProjectorClosesReworkRequestWithoutCreatingAnotherRequest(t *testing.T) {
	ctx := context.Background()
	projector := projection.NewProjector()
	model := projection.NewReadModel("mission-1")
	at := time.Date(2026, 5, 31, 9, 0, 0, 0, time.UTC)
	events := []missionevent.Envelope{
		event(1, "evt-mission-created", missionevent.EventMissionCreated, missionevent.MissionCreatedPayload{Title: "Mission", FinalGoal: "Resolve rework"}, at),
		event(2, "evt-rework-requested", missionevent.EventReworkRequested, missionevent.ReworkRequestedPayload{TaskID: "task-1", QARef: "rework-1", Reason: "missing evidence"}, at.Add(time.Minute)),
		event(3, "evt-rework-resolved", missionevent.EventReworkResolved, missionevent.ReworkResolvedPayload{TaskID: "task-1", ReworkRef: "rework-1", Reason: "evidence added"}, at.Add(2*time.Minute)),
	}

	for _, event := range events {
		if err := projector.Apply(ctx, model, event); err != nil {
			t.Fatalf("Apply(%s) returned error: %v", event.EventType, err)
		}
	}

	if len(model.QA.ReworkRequests) != 1 {
		t.Fatalf("rework request count = %d, want 1: %+v", len(model.QA.ReworkRequests), model.QA.ReworkRequests)
	}
	if model.QA.ReworkRequests[0].Status != projection.ReworkStatusResolved {
		t.Fatalf("rework status = %s, want resolved: %+v", model.QA.ReworkRequests[0].Status, model.QA.ReworkRequests[0])
	}
	if len(model.TaskBoard.Tasks) != 1 || model.TaskBoard.Tasks[0].Status == projection.TaskStatusReworkRequested {
		t.Fatalf("task should leave rework_requested after resolution: %+v", model.TaskBoard.Tasks)
	}
}

func TestProjectorDoesNotRegressCompletedTaskWhenLateReworkResolutionArrives(t *testing.T) {
	ctx := context.Background()
	projector := projection.NewProjector()
	model := projection.NewReadModel("mission-1")
	at := time.Date(2026, 5, 31, 9, 0, 0, 0, time.UTC)
	events := []missionevent.Envelope{
		event(1, "evt-mission-created", missionevent.EventMissionCreated, missionevent.MissionCreatedPayload{Title: "Mission", FinalGoal: "Resolve rework"}, at),
		event(2, "evt-rework-requested", missionevent.EventReworkRequested, missionevent.ReworkRequestedPayload{TaskID: "task-1", QARef: "rework-1", Reason: "missing evidence"}, at.Add(time.Minute)),
		event(3, "evt-approved", missionevent.EventTaskApproved, missionevent.TaskStatusPayload{TaskID: "task-1"}, at.Add(2*time.Minute)),
		event(4, "evt-rework-resolved", missionevent.EventReworkResolved, missionevent.ReworkResolvedPayload{TaskID: "task-1", ReworkRef: "rework-1", Reason: "evidence added"}, at.Add(3*time.Minute)),
	}

	for _, event := range events {
		if err := projector.Apply(ctx, model, event); err != nil {
			t.Fatalf("Apply(%s) returned error: %v", event.EventType, err)
		}
	}

	if len(model.TaskBoard.Tasks) != 1 || model.TaskBoard.Tasks[0].Status != projection.TaskStatusApproved {
		t.Fatalf("task status regressed after late rework resolution: %+v", model.TaskBoard.Tasks)
	}
	if len(model.QA.ReworkRequests) != 1 || model.QA.ReworkRequests[0].Status != projection.ReworkStatusResolved {
		t.Fatalf("rework request should still close: %+v", model.QA.ReworkRequests)
	}
}

func TestProjectorDeduplicatesByEventIDAndIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	projector := projection.NewProjector()
	model := projection.NewReadModel("mission-1")
	events := missionScenarioEvents()

	if err := projector.Apply(ctx, model, events[0]); err != nil {
		t.Fatalf("mission Apply returned error: %v", err)
	}
	if err := projector.Apply(ctx, model, events[1]); err != nil {
		t.Fatalf("first Apply returned error: %v", err)
	}
	duplicateID := events[1]
	duplicateID.StreamVersion = 3
	if err := projector.Apply(ctx, model, duplicateID); err != nil {
		t.Fatalf("duplicate event id returned error: %v", err)
	}
	duplicateKey := events[1]
	duplicateKey.EventID = "evt-agent-joined-copy"
	duplicateKey.StreamVersion = 4
	if err := projector.Apply(ctx, model, duplicateKey); err != nil {
		t.Fatalf("duplicate idempotency key returned error: %v", err)
	}

	if len(model.Team.Members) != 1 {
		t.Fatalf("team member count = %d, want 1: %+v", len(model.Team.Members), model.Team.Members)
	}
}

func TestProjectorRejectsOutOfOrderAndUnknownVersionEvents(t *testing.T) {
	ctx := context.Background()
	projector := projection.NewProjector()
	model := projection.NewReadModel("mission-1")
	events := missionScenarioEvents()

	if err := projector.Apply(ctx, model, events[1]); !errors.Is(err, projection.ErrOutOfOrderEvent) {
		t.Fatalf("out-of-order Apply error = %v, want ErrOutOfOrderEvent", err)
	}

	model = projection.NewReadModel("mission-1")
	unknownVersion := events[0]
	unknownVersion.EventVersion = 99
	if err := projector.Apply(ctx, model, unknownVersion); !errors.Is(err, projection.ErrUnknownEventVersion) {
		t.Fatalf("unknown version error = %v, want ErrUnknownEventVersion", err)
	}
}

func TestProjectorRejectsUnknownEventType(t *testing.T) {
	ctx := context.Background()
	projector := projection.NewProjector()
	model := projection.NewReadModel("mission-1")
	unknown := missionScenarioEvents()[0]
	unknown.EventType = missionevent.EventType("UnknownBusinessFact")

	if err := projector.Apply(ctx, model, unknown); !errors.Is(err, projection.ErrUnknownEventType) {
		t.Fatalf("unknown event type error = %v, want ErrUnknownEventType", err)
	}
	if model.Checkpoint.Version != 0 {
		t.Fatalf("checkpoint advanced to %d for unknown event", model.Checkpoint.Version)
	}
}

func TestPersistedReadModelCanContinueProjectionAfterRuntimeStateRestored(t *testing.T) {
	ctx := context.Background()
	projector := projection.NewProjector()
	model := projection.NewReadModel("mission-1")
	events := missionScenarioEvents()
	if err := projector.Apply(ctx, model, events[0]); err != nil {
		t.Fatalf("first Apply returned error: %v", err)
	}
	persisted := *model

	if err := projector.Apply(ctx, &persisted, events[1]); err != nil {
		t.Fatalf("Apply after persistence returned error: %v", err)
	}
	if persisted.Checkpoint.Version != 2 {
		t.Fatalf("checkpoint = %d, want 2", persisted.Checkpoint.Version)
	}
}

func TestProjectorCreatesApprovalRequestFromBlockedRuntimeMetadata(t *testing.T) {
	ctx := context.Background()
	projector := projection.NewProjector()
	at := time.Date(2026, 6, 6, 13, 0, 0, 0, time.UTC)
	model := projection.NewReadModel("mission-1")
	created := event(1, "evt-mission-created", missionevent.EventMissionCreated, missionevent.MissionCreatedPayload{Title: "Runtime approvals", FinalGoal: "Route provider approvals"}, at)
	blocked := event(2, "evt-runtime-blocked", missionevent.EventAgentRunBlocked, missionevent.RuntimeFailurePayload{
		TaskID:  "task-1",
		AgentID: "agent-1",
		RunID:   "run-1",
		Code:    "agent_runtime_blocked",
		Message: "codex waiting for user approval",
		Metadata: map[string]string{
			"approval_source":     "codex",
			"approval_request_id": "cmd-1",
			"approval_session_id": "thread-1",
			"approval_turn_id":    "turn-1",
			"approval_kind":       "command_execution",
			"approval_tool_name":  "exec_command",
			"approval_type":       "command_execution",
		},
	}, at.Add(time.Minute))

	if err := projector.Apply(ctx, model, created); err != nil {
		t.Fatalf("Apply mission created returned error: %v", err)
	}
	if err := projector.Apply(ctx, model, blocked); err != nil {
		t.Fatalf("Apply runtime blocked returned error: %v", err)
	}

	if len(model.ApprovalRequests) != 1 {
		t.Fatalf("approval requests = %+v, want one provider approval", model.ApprovalRequests)
	}
	request := model.ApprovalRequests[0]
	if request.RequestID != "cmd-1" || request.ApprovalType != "command_execution" || request.Status != "pending" {
		t.Fatalf("approval request identity = %+v", request)
	}
	if request.MissionID != "mission-1" || request.TaskID != "task-1" || request.RequestedBy != "agent-1" {
		t.Fatalf("approval request ownership = %+v", request)
	}
	if request.SourceType != "provider_tool" || request.SourceID != "codex:exec_command" {
		t.Fatalf("approval source = %+v", request)
	}
	if request.Metadata["approval_session_id"] != "thread-1" || request.Metadata["approval_turn_id"] != "turn-1" {
		t.Fatalf("approval metadata = %+v", request.Metadata)
	}
}

func TestPersistedReadModelRetainsDeduplicationStateAfterJSONRoundTrip(t *testing.T) {
	ctx := context.Background()
	projector := projection.NewProjector()
	model := projection.NewReadModel("mission-1")
	events := missionScenarioEvents()
	if err := projector.Apply(ctx, model, events[0]); err != nil {
		t.Fatalf("first Apply returned error: %v", err)
	}
	if err := projector.Apply(ctx, model, events[1]); err != nil {
		t.Fatalf("second Apply returned error: %v", err)
	}
	payload, err := json.Marshal(model)
	if err != nil {
		t.Fatalf("marshal persisted read model: %v", err)
	}
	var restored projection.ReadModel
	if err := json.Unmarshal(payload, &restored); err != nil {
		t.Fatalf("unmarshal persisted read model: %v", err)
	}

	duplicateID := events[1]
	duplicateID.StreamVersion = 3
	if err := projector.Apply(ctx, &restored, duplicateID); err != nil {
		t.Fatalf("duplicate event id after restore returned error: %v", err)
	}
	duplicateKey := events[1]
	duplicateKey.EventID = "evt-agent-joined-copy"
	duplicateKey.StreamVersion = 3
	if err := projector.Apply(ctx, &restored, duplicateKey); err != nil {
		t.Fatalf("duplicate idempotency key after restore returned error: %v", err)
	}
	if len(restored.Team.Members) != 1 {
		t.Fatalf("restored team member count = %d, want 1: %+v", len(restored.Team.Members), restored.Team.Members)
	}
	if restored.Checkpoint.Version != 2 {
		t.Fatalf("checkpoint advanced on restored duplicate to %d, want 2", restored.Checkpoint.Version)
	}
}

func TestReplayMissionIsStable(t *testing.T) {
	ctx := context.Background()
	reader := projection.NewMemoryEventReader(missionScenarioEvents())
	replay := projection.NewReplayService(reader, projection.NewProjector())

	first, err := replay.ReplayMission(ctx, "mission-1")
	if err != nil {
		t.Fatalf("first replay returned error: %v", err)
	}
	second, err := replay.ReplayMission(ctx, "mission-1")
	if err != nil {
		t.Fatalf("second replay returned error: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replay not stable:\nfirst=%+v\nsecond=%+v", first, second)
	}
}

func TestReplayMissionRetainsPersonalAssistantOrigin(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)
	replay := projection.NewReplayService(projection.NewMemoryEventReader([]missionevent.Envelope{
		event(1, "evt-mission-created", missionevent.EventMissionCreated, missionevent.MissionCreatedPayload{
			Title: "PA mission", FinalGoal: "Retain assistant origin", PersonalAssistantOriginOwnerID: "account-1", PersonalAssistantOriginSubjectID: "user-1",
		}, at),
	}), projection.NewProjector())
	model, err := replay.ReplayMission(ctx, "mission-1")
	if err != nil {
		t.Fatalf("ReplayMission: %v", err)
	}
	if model.Mission.PersonalAssistantOriginOwnerID != "account-1" || model.Mission.PersonalAssistantOriginSubjectID != "user-1" {
		t.Fatalf("replayed PA origin = %+v", model.Mission)
	}
}

func TestProjectorLogsDiagnosticEventFields(t *testing.T) {
	ctx := context.Background()
	var buf bytes.Buffer
	projector := projection.NewProjectorWithLogger(logging.NewJSONLogger(&buf, logging.LevelDebug))
	model := projection.NewReadModel("mission-1")

	if err := projector.Apply(ctx, model, missionScenarioEvents()[0]); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}

	logged := buf.String()
	for _, want := range []string{
		`"mission_id":"mission-1"`,
		`"event_id":"evt-mission-created"`,
		`"event_type":"MissionCreated"`,
		`"stream_version":1`,
		`"correlation_id":"corr-1"`,
	} {
		if !strings.Contains(logged, want) {
			t.Fatalf("projector log missing %s in %s", want, logged)
		}
	}
}

func missionScenarioEvents() []missionevent.Envelope {
	at := time.Date(2026, 5, 31, 9, 0, 0, 0, time.UTC)
	return []missionevent.Envelope{
		event(1, "evt-mission-created", missionevent.EventMissionCreated, missionevent.MissionCreatedPayload{Title: "Launch stage 4", FinalGoal: "Production event projection"}, at),
		event(2, "evt-agent-joined", missionevent.EventAgentJoined, missionevent.AgentPayload{
			AgentID: "agent-1", DisplayName: "Ari", RealName: "Ari Chen", RoleName: "Engineer", Department: "R&D", Position: "Senior Engineer",
		}, at.Add(time.Minute)),
		event(3, "evt-assignment", missionevent.EventMissionAssignmentCreated, missionevent.MissionAssignmentPayload{
			AssignmentID: "assignment-1", AgentID: "agent-1", TaskID: "task-1", MissionRole: "worker",
		}, at.Add(2*time.Minute)),
		event(4, "evt-task-contract", missionevent.EventTaskContractCreated, missionevent.TaskContractCreatedPayload{Contract: missiontask.TaskContract{
			ID: "task-1", MissionID: "mission-1", Title: "Build projection", Goal: "Build read model", WorkerPersonalGoal: "Project events",
			ExpectedDeliverables: []missiontask.ExpectedDeliverable{{ID: "expected-1", Title: "Projection package"}},
			EvidenceRequirements: []missiontask.EvidenceRequirement{{ID: "evidence-required-1", Description: "Go tests"}},
			Priority:             "high",
			DuePolicy:            missiontask.DuePolicy{Policy: missiontask.DuePolicyFixed, DueAt: at.Add(24 * time.Hour)},
		}}, at.Add(3*time.Minute)),
		event(5, "evt-task-accepted", missionevent.EventTaskAccepted, missionevent.TaskAcceptedPayload{TaskID: "task-1", AcceptedBy: "agent-1"}, at.Add(4*time.Minute)),
		event(6, "evt-result", missionevent.EventAgentResultSubmitted, missionevent.AgentResultSubmittedPayload{TaskID: "task-1", AgentID: "agent-1", ResultRef: "result-1", Summary: "Projection implemented"}, at.Add(5*time.Minute)),
		event(7, "evt-deliverable", missionevent.EventDeliverableSubmitted, missionevent.DeliverableSubmittedPayload{TaskID: "task-1", AgentID: "agent-1", DeliverableID: "deliverable-1", ExpectedDeliverableID: "expected-1", Title: "Projection package", ArtifactRef: agentcore.ArtifactRef{ID: "artifact-1", Kind: agentcore.ArtifactKindCode, URI: "artifact://projection"}}, at.Add(6*time.Minute)),
		event(8, "evt-evidence", missionevent.EventEvidenceSubmitted, missionevent.EvidenceSubmittedPayload{TaskID: "task-1", AgentID: "agent-1", EvidenceID: "evidence-1", EvidenceRequirementID: "evidence-required-1", Kind: agentcore.EvidenceKindTestLog, ArtifactRef: agentcore.ArtifactRef{ID: "log-1", Kind: agentcore.ArtifactKindLog, URI: "artifact://test-log"}}, at.Add(7*time.Minute)),
		event(9, "evt-qa-requested", missionevent.EventQARequested, missionevent.QAPayload{TaskID: "task-1", QARef: "qa-1", Status: "requested", Message: "Check evidence"}, at.Add(8*time.Minute)),
		event(10, "evt-qa-rejected", missionevent.EventQARejected, missionevent.QAPayload{TaskID: "task-1", QARef: "qa-1", Status: "rejected", Reason: "missing edge case", Message: "Need duplicate coverage"}, at.Add(9*time.Minute)),
		event(11, "evt-rework", missionevent.EventReworkRequested, missionevent.ReworkRequestedPayload{TaskID: "task-1", QARef: "qa-1", Reason: "missing edge case", Message: "Add duplicate coverage"}, at.Add(10*time.Minute)),
		event(12, "evt-discussion", missionevent.EventDiscussionGroupCreated, missionevent.DiscussionGroupCreatedPayload{DiscussionGroupID: "discussion-1", MemberIDs: []agentcore.AgentID{"agent-1"}}, at.Add(11*time.Minute)),
		event(13, "evt-message", missionevent.EventMessagePosted, missionevent.MessagePostedPayload{MessageID: "message-1", DiscussionGroupID: "discussion-1", AuthorID: "agent-1", AuthorType: "agent", Body: "Need QA clarification", Mentions: []agentcore.AgentID{"agent-2"}}, at.Add(12*time.Minute)),
		event(14, "evt-inbox", missionevent.EventInboxItemCreated, missionevent.InboxItemPayload{InboxItemID: "inbox-1", OwnerID: "agent-2", MessageID: "message-1", Reason: "mention"}, at.Add(13*time.Minute)),
		event(15, "evt-decision", missionevent.EventDecisionRecorded, missionevent.DecisionRecordedPayload{DecisionID: "decision-1", Summary: "Reject until duplicate coverage lands", SourceRefs: []string{"qa-1"}}, at.Add(14*time.Minute)),
		event(16, "evt-blocker", missionevent.EventBlockerRaised, missionevent.BlockerRaisedPayload{BlockerID: "blocker-1", TaskID: "task-1", RaisedBy: "agent-1", Summary: "Waiting on QA details"}, at.Add(15*time.Minute)),
	}
}

func event(version int, id string, eventType missionevent.EventType, payload missionevent.Payload, at time.Time) missionevent.Envelope {
	return missionevent.Envelope{
		EventID:        id,
		MissionID:      "mission-1",
		StreamID:       "mission-1",
		StreamVersion:  version,
		EventType:      eventType,
		EventVersion:   1,
		Actor:          missionevent.ActorRef{Kind: missionevent.ActorKindAgent, ID: "agent-1"},
		CorrelationID:  "corr-1",
		IdempotencyKey: id + ":idem",
		OccurredAt:     at,
		Payload:        payload,
	}
}

func TestProjectorBlocksFailedRuntimeAndClearsGeneratedBlockerOnRetry(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	model := projection.NewReadModel("mission-1")
	projector := projection.NewProjector()
	events := []missionevent.Envelope{
		event(1, "mission-created", missionevent.EventMissionCreated, missionevent.MissionCreatedPayload{Title: "Runtime repair", FinalGoal: "Finish task"}, at),
		event(2, "task-assigned", missionevent.EventTaskAssigned, missionevent.TaskAssignedPayload{TaskID: "task-1", AgentID: "agent-1", Goal: "Run"}, at.Add(time.Minute)),
		event(3, "run-failed", missionevent.EventAgentRunFailed, missionevent.RuntimeFailurePayload{
			TaskID: "task-1", AgentID: "agent-1", RunID: "run-1", Code: "agent_runtime_failed", Message: "provider rejected tool name",
		}, at.Add(2*time.Minute)),
	}
	for _, item := range events {
		if err := projector.Apply(ctx, model, item); err != nil {
			t.Fatalf("Apply(%s): %v", item.EventType, err)
		}
	}

	task := model.TaskBoard.Tasks[0]
	if task.Status != projection.TaskStatusBlocked || !reflect.DeepEqual(task.BlockerRefs, []string{"runtime-failure:run-1"}) {
		t.Fatalf("failed task = %+v", task)
	}
	if len(model.Blockers) != 1 || len(model.Discussion.Blockers) != 1 || model.Blockers[0].Summary != "provider rejected tool name" {
		t.Fatalf("runtime blockers = %+v discussion=%+v", model.Blockers, model.Discussion.Blockers)
	}

	started := event(4, "run-retry-started", missionevent.EventAgentRunStarted, missionevent.RuntimeLifecyclePayload{
		TaskID: "task-1", AgentID: "agent-1", RunID: "run-2", Status: "running",
	}, at.Add(3*time.Minute))
	if err := projector.Apply(ctx, model, started); err != nil {
		t.Fatalf("Apply retry start: %v", err)
	}
	task = model.TaskBoard.Tasks[0]
	if task.Status != projection.TaskStatusStarted || len(task.BlockerRefs) != 0 || len(model.Blockers) != 0 || len(model.Discussion.Blockers) != 0 {
		t.Fatalf("retried task = %+v blockers=%+v discussion=%+v", task, model.Blockers, model.Discussion.Blockers)
	}
}
