package main

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
	missionprojection "orrery_v2/internal/mission/projection"
	"orrery_v2/internal/platform/testkit"
	wstransport "orrery_v2/internal/transport/websocket"
)

func TestTodoOriginMissionSnapshotAndPatchFilterRestrictedDiscussion(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	const tenantID = "tenant-stream"
	const missionID = "mission-todo-stream"
	const viewerID = "project-maintainer"
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	model := restrictedTodoMissionModel(tenantID, missionID, env.Clock.Now())
	if err := repo.SaveForTenant(ctx, tenantID, model); err != nil {
		t.Fatal(err)
	}
	authz := newProductionMissionAuthorizationTestFixture()
	if err := authz.relations.WriteMissionParticipant(ctx, authorization.SubjectRef{Kind: "user", ID: viewerID}, missionID, tenantID); err != nil {
		t.Fatal(err)
	}
	query := missioncontrol.NewQueryService(repo)
	authz.wireQuery(query)
	api := productionAPI{
		missionQuery: query, missionRepo: repo, stream: missioncontrol.NewStreamService(),
		discussions: deniedProductionDiscussionSessions{}, authorizationService: authz.service,
	}
	req := wstransport.SubscribeRequest{
		MissionID: missionID, ViewerID: viewerID, ViewerKind: string(missioncontrol.ViewerKindUser), TenantID: tenantID,
		AuthorizationSubjectID: viewerID, PermissionScope: []string{"mission.read"},
	}

	snapshot, err := api.Snapshot(ctx, missionID, req)
	if err != nil {
		t.Fatal(err)
	}
	assertRestrictedTodoMissionView(t, snapshot)
	patches, err := api.Patches(ctx, missionID, req, model.Checkpoint.Version-1)
	if err != nil {
		t.Fatal(err)
	}
	if len(patches) != 1 {
		t.Fatalf("patches = %+v", patches)
	}
	assertRestrictedTodoMissionView(t, patches[0].Payload)
}

func TestTodoOriginMissionOpsFiltersRestrictedDiscussion(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	const tenantID = "tenant-ops"
	const missionID = "mission-todo-ops"
	const viewerID = "project-maintainer"
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	model := restrictedTodoMissionModel(tenantID, missionID, env.Clock.Now())
	model.Team.Members = []missionprojection.TeamMemberView{{AgentID: "agent-2", DisplayName: "Agent 2"}}
	model.RuntimeSessions = []missionprojection.RuntimeSessionView{{RunID: "run-secret", AgentID: "agent-2", Status: missionprojection.RuntimeSessionFailed, ErrorSummary: "secret prompt content"}}
	if err := repo.SaveForTenant(ctx, tenantID, model); err != nil {
		t.Fatal(err)
	}
	authz := newProductionMissionAuthorizationTestFixture()
	if err := authz.relations.WriteMissionParticipant(ctx, authorization.SubjectRef{Kind: "user", ID: viewerID}, missionID, tenantID); err != nil {
		t.Fatal(err)
	}
	query := missioncontrol.NewQueryService(repo)
	authz.wireQuery(query)
	api := productionAPI{missionQuery: query, discussions: deniedProductionDiscussionSessions{}}

	view, err := api.GetMissionOpsView(ctx, missioncontrol.ViewerRef{
		ViewerID: viewerID, ViewerKind: missioncontrol.ViewerKindUser, TenantID: tenantID,
		AuthorizationSubjectID: viewerID, PermissionScope: []string{"mission.read"},
	}, missioncore.MissionID(missionID))
	if err != nil {
		t.Fatal(err)
	}
	for _, focus := range view.CurrentFocus {
		if todoMissionOpsDiscussionSource(focus.Kind, focus.SourceType) {
			t.Fatalf("restricted Ops focus leaked discussion data: %+v", focus)
		}
	}
	for _, action := range view.ActionCards {
		if todoMissionOpsDiscussionSource(action.Kind, action.SourceType) {
			t.Fatalf("restricted Ops action leaked discussion data: %+v", action)
		}
	}
	for _, lane := range view.AgentLanes {
		if lane.InboxOpenCount != 0 {
			t.Fatalf("restricted Ops lane leaked inbox count: %+v", lane)
		}
		for _, session := range lane.RuntimeSessions {
			if session.ErrorSummary != "" {
				t.Fatalf("restricted Ops lane leaked runtime error details: %+v", session)
			}
		}
	}
	for _, session := range view.RuntimeSessions {
		if session.ErrorSummary != "" {
			t.Fatalf("restricted Ops view leaked runtime error details: %+v", session)
		}
	}
	foundBlocker := false
	for _, focus := range view.CurrentFocus {
		foundBlocker = foundBlocker || focus.Kind == "blocker" || focus.SourceType == "blocker"
	}
	if !foundBlocker {
		t.Fatalf("restricted Ops view omitted structured blocker focus: %+v", view.CurrentFocus)
	}
}

type deniedProductionDiscussionSessions struct{}

func (deniedProductionDiscussionSessions) EnsureMissionConversation(context.Context, discussionsession.ViewerRef, discussionsession.EnsureMissionConversationCommand) (discussionsession.ConversationDTO, error) {
	return discussionsession.ConversationDTO{}, discussionsession.ErrForbidden
}

func (deniedProductionDiscussionSessions) EnsureMissionAgentMember(context.Context, missioncontrol.CanonicalConversationMembershipRequest) error {
	return discussionsession.ErrForbidden
}

func (deniedProductionDiscussionSessions) ReconcileMissionAgentMembers(context.Context, missioncontrol.CanonicalConversationMembershipSetRequest) error {
	return discussionsession.ErrForbidden
}

func (deniedProductionDiscussionSessions) ReconcileTodoMissionAgentMembers(context.Context, missioncontrol.CanonicalConversationMembershipSetRequest) error {
	return discussionsession.ErrForbidden
}

func (deniedProductionDiscussionSessions) GetConversation(context.Context, discussionsession.ViewerRef, string) (discussionsession.ConversationDTO, error) {
	return discussionsession.ConversationDTO{}, discussionsession.ErrForbidden
}

func restrictedTodoMissionModel(tenantID, missionID string, at time.Time) missionprojection.ReadModel {
	return missionprojection.ReadModel{
		TenantID:  tenantID,
		Mission:   missionprojection.MissionView{TenantID: tenantID, MissionID: missioncore.MissionID(missionID), Title: "Todo execution", Status: missionprojection.MissionStatusActive, OriginKind: "todo", OriginTodoID: "todo-1", CreatedAt: at},
		TaskBoard: missionprojection.TaskBoardView{Tasks: []missionprojection.TaskCardView{{TaskID: "task-1", Status: missionprojection.TaskStatusBlocked, BlockerRefs: []string{"blocker-secret"}, UpdatedAt: at}}},
		Discussion: missionprojection.DiscussionView{
			DiscussionGroupID: "group-secret", ConversationID: "conversation-secret", DiscussionDigestRef: "digest-secret",
			Messages:        []missionprojection.MessageView{{MessageID: "message-secret", DiscussionGroupID: "group-secret", AuthorID: "agent-1", Body: "secret body", PostedAt: at}},
			PendingMentions: []missionprojection.MentionView{{MessageID: "message-secret", AgentID: "agent-2", CreatedAt: at}},
			Decisions:       []missionprojection.DecisionView{{DecisionID: "decision-secret", Summary: "secret decision", RecordedAt: at}},
			Blockers:        []missionprojection.BlockerView{{BlockerID: "blocker-secret", TaskID: "task-1", RaisedBy: "agent-1", Summary: "secret blocker", RaisedAt: at}},
		},
		InboxSummary:    missionprojection.InboxSummaryView{OpenCount: 1, Items: []missionprojection.InboxItemView{{InboxItemID: "inbox-secret", OwnerID: agentcore.AgentID("agent-2"), MessageID: "message-secret", Reason: "secret reason", Status: "open", CreatedAt: at}}},
		LatestDecisions: []missionprojection.DecisionView{{DecisionID: "decision-secret", Summary: "secret decision", RecordedAt: at}},
		Blockers:        []missionprojection.BlockerView{{BlockerID: "blocker-secret", TaskID: "task-1", RaisedBy: "agent-1", Summary: "secret blocker", RaisedAt: at}},
		ProgressGraph: missionprojection.ProgressGraphView{
			Nodes: []missionprojection.ProgressNodeView{
				{ID: "node-message", EventID: "event-message", EventType: "MessagePosted", Label: "secret body", Details: "secret body", OccurredAt: at},
				{ID: "node-task", EventID: "event-task", EventType: "TaskStarted", Label: "Task started", OccurredAt: at},
			},
			Edges: []missionprojection.ProgressEdgeView{{From: "node-message", To: "node-task", Kind: "observed_after"}}, CurrentFocus: []string{"node-message", "node-task"},
		},
		Checkpoint: missionprojection.Checkpoint{MissionID: missioncore.MissionID(missionID), Version: 7, UpdatedAt: at}, UpdatedAt: at,
	}
}

func assertRestrictedTodoMissionView(t *testing.T, view missioncontrol.MissionControlViewDTO) {
	t.Helper()
	if !view.Discussion.Restricted || view.Discussion.ConversationID != "" || view.Discussion.DiscussionGroupID != "" || view.Discussion.DiscussionDigestRef != "" {
		t.Fatalf("discussion identity leaked: %+v", view.Discussion)
	}
	if len(view.Discussion.Messages) != 0 || len(view.Discussion.PendingMentions) != 0 || len(view.Discussion.Decisions) != 0 || len(view.Discussion.Blockers) != 0 || len(view.LatestDecisions) != 0 || len(view.InboxSummary.Items) != 0 {
		t.Fatalf("discussion content leaked: discussion=%+v decisions=%+v blockers=%+v inbox=%+v", view.Discussion, view.LatestDecisions, view.Blockers, view.InboxSummary)
	}
	if len(view.Blockers) != 1 || view.Blockers[0].BlockerID != "blocker-secret" || view.Blockers[0].Summary != "secret blocker" {
		t.Fatalf("structured Mission blockers were removed: %+v", view.Blockers)
	}
	if len(view.TaskBoard.Tasks) != 1 || len(view.TaskBoard.Tasks[0].BlockerRefs) != 1 || view.TaskBoard.Tasks[0].BlockerRefs[0] != "blocker-secret" || view.TaskBoard.Tasks[0].Status != "blocked" {
		t.Fatalf("structured task blocker state was removed: %+v", view.TaskBoard.Tasks)
	}
	if len(view.ProgressGraph.Nodes) != 1 || view.ProgressGraph.Nodes[0].EventType != "TaskStarted" || len(view.ProgressGraph.Edges) != 0 || len(view.ProgressGraph.CurrentFocus) != 1 || view.ProgressGraph.CurrentFocus[0] != "node-task" {
		t.Fatalf("discussion timeline was not filtered: %+v", view.ProgressGraph)
	}
}
