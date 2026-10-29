package missioncontrol_test

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/mission/projection"
	"orrery_v2/internal/platform/testkit"
)

func TestLifecycleServiceCreatesMissionTaskAndListsProjection(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	agents := &fakeLifecycleAgentDirectory{entries: []agentcore.AgentDirectoryEntry{{AgentProfileID: "agent-1", AgentInstanceID: "agent-1", AgentKind: agentcore.AgentKindAI, Enabled: true}}}
	authzSink := &missionAuthzSink{}
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{Clock: env.Clock, IDGenerator: env.IDGenerator, AgentDirectory: agents, AuthorizationRelations: authorization.NewRelationProjector(authzSink)})
	viewer := missioncontrol.ViewerRef{ViewerID: "planner-1", ViewerKind: missioncontrol.ViewerKindUser, AuthorizationSubjectID: "human-planner-1"}

	mission, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{IdempotencyKey: "mission-key", Title: "Launch", FinalGoal: "Ship v2"})
	if err != nil {
		t.Fatalf("CreateMission returned error: %v", err)
	}
	if mission.MissionID == "" || mission.Status != "active" {
		t.Fatalf("mission = %+v", mission)
	}

	task, err := service.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{
		IdempotencyKey:     "task-key",
		MissionID:          missioncore.MissionID(mission.MissionID),
		AssigneeID:         "agent-1",
		Goal:               "Implement route",
		WorkerPersonalGoal: "Deliver working route",
		Scope:              "backend",
		Priority:           "high",
		DueAt:              time.Date(2026, 6, 3, 9, 30, 0, 0, time.UTC),
		ExpectedDeliverables: []missioncontrol.ExpectedDeliverableCommand{{
			ID:    "expected-brief",
			Title: "Launch brief",
		}},
	})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}
	if task.TaskID == "" || task.Status != "created" || task.Assignee != "agent-1" {
		t.Fatalf("task = %+v", task)
	}
	if task.Priority != "high" || !task.DueAt.Equal(time.Date(2026, 6, 3, 9, 30, 0, 0, time.UTC)) {
		t.Fatalf("task metadata = %+v", task)
	}
	if len(task.ExpectedDeliverables) != 1 || task.ExpectedDeliverables[0].Title != "Launch brief" {
		t.Fatalf("task deliverables = %+v", task.ExpectedDeliverables)
	}
	if len(authzSink.written) != 2 {
		t.Fatalf("authz relations = %+v", authzSink.written)
	}
	assertMissionAuthzTuple(t, authzSink.written[0], "user", "human-planner-1", "owner", mission.MissionID)
	assertMissionAuthzTuple(t, authzSink.written[1], "agent", "agent-1", "participant", mission.MissionID)

	list, err := service.ListMissions(ctx, viewer, missioncontrol.ListMissionsQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListMissions returned error: %v", err)
	}
	if len(list.Missions) != 1 || list.Missions[0].MissionID != mission.MissionID {
		t.Fatalf("mission list = %+v", list)
	}
	if len(list.Summaries) != 1 || list.Summaries[0].MissionID != mission.MissionID || list.Summaries[0].ActiveTaskCount != 0 || list.Summaries[0].TeamSize != 1 {
		t.Fatalf("mission summaries = %+v", list.Summaries)
	}
}

func TestLifecycleServiceTodoOriginMissionWritesOriginsAndParticipantsWithoutOwner(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	authzSink := &missionAuthzSink{}
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{
		Clock: env.Clock, IDGenerator: env.IDGenerator, AuthorizationRelations: authorization.NewRelationProjector(authzSink),
	})
	viewer := missioncontrol.ViewerRef{
		ViewerID: "todo-automation-agent", ViewerKind: missioncontrol.ViewerKindSystem,
		TenantID: "tenant-a", AuthorizationSubjectID: "todo-automation-agent",
	}
	mission, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{
		IdempotencyKey: "todo-mission-key", Title: "Todo mission", FinalGoal: "Complete the approved Todo",
		OriginKind: "todo", OriginID: "todo-1", OriginTodoID: "todo-1", ExecutionAttempt: 2,
		DiscussionMode: "existing_conversation", OriginDevelopmentProjectID: "project-1", ExistingConversationID: "todo-conversation-1",
	})
	if err != nil {
		t.Fatalf("CreateMission: %v", err)
	}
	if mission.OriginKind != "todo" || mission.OriginID != "todo-1" || mission.OriginTodoID != "todo-1" || mission.ExecutionAttempt != 2 || mission.DiscussionMode != "existing_conversation" || mission.OriginDevelopmentProjectID != "project-1" {
		t.Fatalf("Todo Mission execution metadata = %+v", mission)
	}
	for _, tuple := range authzSink.written {
		if tuple.Object.Type == "mission" && tuple.Object.ID == mission.MissionID && tuple.Relation == "owner" {
			t.Fatalf("Todo-origin Mission wrote a direct owner relation: %+v", tuple)
		}
		if tuple.Object.Type == "discussion" {
			t.Fatalf("Todo-origin Mission wrote standalone discussion relations: %+v", tuple)
		}
	}
	if !missionAuthzTupleExists(authzSink.written, "todo", "todo-1", "origin_todo", "mission", mission.MissionID) ||
		!missionAuthzTupleExists(authzSink.written, "development_project", "project-1", "origin_development_project", "mission", mission.MissionID) {
		t.Fatalf("Todo-origin relations missing: %+v", authzSink.written)
	}
}

func TestLifecycleServiceFiltersMissionsByOriginAfterObjectAuthorization(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	at := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)
	models := []projection.ReadModel{
		missionListReadModel("mission-visible", "todo", "todo-1", at.Add(2*time.Minute)),
		missionListReadModel("mission-hidden", "todo", "todo-1", at.Add(time.Minute)),
		missionListReadModel("mission-other-origin", "todo", "todo-2", at),
	}
	models[0].Discussion.ConversationID = "conversation-visible"
	for _, model := range models {
		if err := repo.Save(ctx, model); err != nil {
			t.Fatalf("Save(%s): %v", model.Mission.MissionID, err)
		}
	}
	authz := authorization.NewService(missionListAuthorizationAdapter{allowedMissionIDs: map[string]bool{
		"mission-visible":      true,
		"mission-other-origin": true,
	}})
	service := missioncontrol.NewLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{AuthorizationService: authz})
	viewer := missioncontrol.ViewerRef{ViewerID: "viewer-1", ViewerKind: missioncontrol.ViewerKindUser}

	page, err := service.ListMissions(ctx, viewer, missioncontrol.ListMissionsQuery{OriginKind: "todo", OriginID: "todo-1", Limit: 20})
	if err != nil {
		t.Fatalf("ListMissions: %v", err)
	}
	if page.Page.Total != 1 || page.Page.Returned != 1 || len(page.Missions) != 1 || page.Missions[0].MissionID != "mission-visible" || page.Missions[0].ConversationID != "conversation-visible" {
		t.Fatalf("origin-filtered authorized page = %+v", page)
	}
}

func missionListReadModel(missionID string, originKind string, originID string, createdAt time.Time) projection.ReadModel {
	return projection.ReadModel{
		Mission: projection.MissionView{
			TenantID: "global", MissionID: missioncore.MissionID(missionID), Title: missionID,
			FinalGoal: "Complete mission", Status: projection.MissionStatusActive,
			OriginKind: originKind, OriginID: originID, CreatedAt: createdAt,
		},
		UpdatedAt:  createdAt,
		Checkpoint: projection.Checkpoint{MissionID: missioncore.MissionID(missionID), Version: 1, UpdatedAt: createdAt},
	}
}

type missionListAuthorizationAdapter struct {
	allowedMissionIDs map[string]bool
}

func (a missionListAuthorizationAdapter) Check(_ context.Context, request authorization.CheckRequest) (authorization.Decision, error) {
	return authorization.Decision{Allowed: request.Action == "mission.view" && a.allowedMissionIDs[request.Object.ID], Source: "test"}, nil
}

func (a missionListAuthorizationAdapter) BatchCheck(ctx context.Context, requests []authorization.CheckRequest) ([]authorization.Decision, error) {
	out := make([]authorization.Decision, 0, len(requests))
	for _, request := range requests {
		decision, err := a.Check(ctx, request)
		if err != nil {
			return nil, err
		}
		out = append(out, decision)
	}
	return out, nil
}

func (missionListAuthorizationAdapter) ListObjects(context.Context, identity.Principal, string, string, string) ([]identity.ObjectRef, error) {
	return nil, nil
}

func (missionListAuthorizationAdapter) WriteRelations(context.Context, []authorization.RelationTuple) error {
	return nil
}

func (missionListAuthorizationAdapter) DeleteRelations(context.Context, []authorization.RelationTuple) error {
	return nil
}

func TestLifecycleServiceRestrictsInternalMissionOriginFieldsToSystemAutomation(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{
		Clock: env.Clock, IDGenerator: env.IDGenerator,
	})

	tests := []struct {
		name   string
		viewer missioncontrol.ViewerRef
		cmd    missioncontrol.CreateMissionCommand
	}{
		{
			name:   "user cannot forge Todo origin",
			viewer: missioncontrol.ViewerRef{ViewerID: "planner", ViewerKind: missioncontrol.ViewerKindUser, TenantID: "tenant-a"},
			cmd: missioncontrol.CreateMissionCommand{
				IdempotencyKey: "forged-todo", FinalGoal: "Forge origin", OriginKind: "todo", OriginTodoID: "todo-1", ExistingConversationID: "conversation-1",
			},
		},
		{
			name:   "origin identifiers require an origin kind",
			viewer: missioncontrol.ViewerRef{ViewerID: "automation", ViewerKind: missioncontrol.ViewerKindSystem, TenantID: "tenant-a"},
			cmd: missioncontrol.CreateMissionCommand{
				IdempotencyKey: "missing-origin-kind", FinalGoal: "Invalid origin", OriginTodoID: "todo-1", ExistingConversationID: "conversation-1",
			},
		},
		{
			name:   "unsupported origin kind is rejected",
			viewer: missioncontrol.ViewerRef{ViewerID: "automation", ViewerKind: missioncontrol.ViewerKindSystem, TenantID: "tenant-a"},
			cmd: missioncontrol.CreateMissionCommand{
				IdempotencyKey: "unsupported-origin", FinalGoal: "Invalid origin", OriginKind: "project", ExistingConversationID: "conversation-1",
			},
		},
		{
			name:   "Todo origin requires the canonical conversation",
			viewer: missioncontrol.ViewerRef{ViewerID: "automation", ViewerKind: missioncontrol.ViewerKindSystem, TenantID: "tenant-a"},
			cmd: missioncontrol.CreateMissionCommand{
				IdempotencyKey: "missing-conversation", FinalGoal: "Invalid origin", OriginKind: "todo", OriginTodoID: "todo-1",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.CreateMission(ctx, test.viewer, test.cmd); !errors.Is(err, missioncontrol.ErrInvalidRequest) {
				t.Fatalf("CreateMission error = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

type missionAuthzSink struct {
	written []authorization.RelationTuple
	deleted []authorization.RelationTuple
}

func (s *missionAuthzSink) WriteRelations(_ context.Context, tuples []authorization.RelationTuple) error {
	s.written = append(s.written, tuples...)
	return nil
}

func (s *missionAuthzSink) DeleteRelations(_ context.Context, tuples []authorization.RelationTuple) error {
	s.deleted = append(s.deleted, tuples...)
	return nil
}

func assertMissionAuthzTuple(t *testing.T, tuple authorization.RelationTuple, subjectKind string, subjectID string, relation string, missionID string) {
	t.Helper()
	if tuple.Subject.Kind != subjectKind || tuple.Subject.ID != subjectID || tuple.Relation != relation || tuple.Object.Type != "mission" || tuple.Object.ID != missionID || tuple.TenantID != "global" {
		t.Fatalf("relation tuple = %+v", tuple)
	}
}

func TestLifecycleServiceUpdatesStableRootTaskWithoutDuplicating(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{Clock: env.Clock, IDGenerator: env.IDGenerator})
	viewer := missioncontrol.ViewerRef{ViewerID: "planner-1", ViewerKind: missioncontrol.ViewerKindUser}

	mission, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{IdempotencyKey: "mission-key", Title: "Snake", FinalGoal: "Build snake"})
	if err != nil {
		t.Fatalf("CreateMission returned error: %v", err)
	}
	rootTaskID := missioncore.TaskID("root-" + mission.MissionID)
	first, err := service.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{
		IdempotencyKey:       "root-task-key-1",
		MissionID:            missioncore.MissionID(mission.MissionID),
		TaskID:               rootTaskID,
		Goal:                 "团队目标：交付一个单 HTML 贪吃蛇小游戏",
		ExpectedDeliverables: []missioncontrol.ExpectedDeliverableCommand{{Title: "snake.html"}},
	})
	if err != nil {
		t.Fatalf("CreateTask first root returned error: %v", err)
	}
	second, err := service.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{
		IdempotencyKey:       "root-task-key-2",
		MissionID:            missioncore.MissionID(mission.MissionID),
		TaskID:               rootTaskID,
		Goal:                 "团队目标：交付一个单 HTML、支持穿墙的贪吃蛇小游戏",
		ExpectedDeliverables: []missioncontrol.ExpectedDeliverableCommand{{Title: "wraparound-snake.html"}},
	})
	if err != nil {
		t.Fatalf("CreateTask updated root returned error: %v", err)
	}

	if first.TaskID != rootTaskID.String() || second.TaskID != rootTaskID.String() {
		t.Fatalf("stable root task ids first=%q second=%q want %q", first.TaskID, second.TaskID, rootTaskID)
	}
	view, err := newAuthorizedQueryService(repo).GetMissionControlView(ctx, viewer, missioncore.MissionID(mission.MissionID))
	if err != nil {
		t.Fatalf("GetMissionControlView returned error: %v", err)
	}
	if len(view.TaskBoard.Tasks) != 1 {
		t.Fatalf("stable root update should not duplicate tasks: %+v", view.TaskBoard.Tasks)
	}
	root := view.TaskBoard.Tasks[0]
	if root.TaskID != rootTaskID.String() || root.Goal != "团队目标：交付一个单 HTML、支持穿墙的贪吃蛇小游戏" {
		t.Fatalf("root task was not updated in place: %+v", root)
	}
	if len(root.ExpectedDeliverables) != 1 || root.ExpectedDeliverables[0].Title != "wraparound-snake.html" {
		t.Fatalf("root deliverables were not updated in place: %+v", root.ExpectedDeliverables)
	}
	if root.Status != "created" || root.Assignee != "" {
		t.Fatalf("stable root task status/assignee changed unexpectedly: %+v", root)
	}
}

func TestLifecycleServiceCreatesUnassignedRootTask(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{Clock: env.Clock, IDGenerator: env.IDGenerator})
	viewer := missioncontrol.ViewerRef{ViewerID: "planner-1", ViewerKind: missioncontrol.ViewerKindUser}

	mission, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{IdempotencyKey: "mission-key", Title: "Snake", FinalGoal: "Build snake"})
	if err != nil {
		t.Fatalf("CreateMission returned error: %v", err)
	}

	task, err := service.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{
		IdempotencyKey:       "root-task-key",
		MissionID:            missioncore.MissionID(mission.MissionID),
		Goal:                 "团队目标：交付一个单 HTML 文件穿墙贪吃蛇小游戏",
		ExpectedDeliverables: []missioncontrol.ExpectedDeliverableCommand{{Title: "可运行的 HTML 文件"}},
	})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}
	if task.TaskID == "" || task.Status != "created" {
		t.Fatalf("unassigned root task = %+v", task)
	}
	if task.Assignee != "" || task.AssigneeDisplayName != "" || task.WorkerPersonalGoalSummary != "" {
		t.Fatalf("root task should stay unassigned team goal: %+v", task)
	}
	if len(task.ExpectedDeliverables) != 1 || task.ExpectedDeliverables[0].Title != "可运行的 HTML 文件" {
		t.Fatalf("root task deliverables = %+v", task.ExpectedDeliverables)
	}

	view, err := newAuthorizedQueryService(repo).GetMissionControlView(ctx, viewer, missioncore.MissionID(mission.MissionID))
	if err != nil {
		t.Fatalf("GetMissionControlView returned error: %v", err)
	}
	if len(view.TaskBoard.Tasks) != 1 || view.TaskBoard.Tasks[0].Assignee != "" || view.Summary.TeamSize != 0 {
		t.Fatalf("unassigned root task should not add team members: view=%+v summary=%+v", view.TaskBoard.Tasks, view.Summary)
	}
}

func TestLifecycleServiceUpdatesMissionBrief(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{Clock: env.Clock, IDGenerator: env.IDGenerator})
	viewer := missioncontrol.ViewerRef{ViewerID: "lead-agent", ViewerKind: missioncontrol.ViewerKindAgent}
	mission, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{IdempotencyKey: "mission-key", Title: "Launch", FinalGoal: "Ship v2"})
	if err != nil {
		t.Fatalf("CreateMission returned error: %v", err)
	}

	updated, err := service.UpdateMissionBrief(ctx, viewer, missioncontrol.UpdateMissionBriefCommand{
		IdempotencyKey: "mission-brief-update",
		MissionID:      missioncore.MissionID(mission.MissionID),
		Title:          "Adjusted launch",
		FinalGoal:      "Ship clarified v2",
	})
	if err != nil {
		t.Fatalf("UpdateMissionBrief returned error: %v", err)
	}

	if updated.MissionID != mission.MissionID || updated.Title != "Adjusted launch" || updated.FinalGoal != "Ship clarified v2" {
		t.Fatalf("updated mission = %+v", updated)
	}
	view, err := newAuthorizedQueryService(repo).GetMissionControlView(ctx, viewer, missioncore.MissionID(mission.MissionID))
	if err != nil {
		t.Fatalf("GetMissionControlView returned error: %v", err)
	}
	if view.Mission.Title != "Adjusted launch" || view.Mission.FinalGoal != "Ship clarified v2" {
		t.Fatalf("mission view = %+v", view.Mission)
	}
	list, err := service.ListMissions(ctx, viewer, missioncontrol.ListMissionsQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListMissions returned error: %v", err)
	}
	if len(list.Missions) != 1 || list.Missions[0].MissionID != mission.MissionID || list.Missions[0].Title != "Adjusted launch" {
		t.Fatalf("mission list = %+v", list.Missions)
	}
}
