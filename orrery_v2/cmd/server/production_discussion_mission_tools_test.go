package main

import (
	"context"
	"errors"
	"testing"
	"time"

	discussionsession "orrery_v2/internal/app/discussionsession"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
	missionprojection "orrery_v2/internal/mission/projection"
)

func TestProductionDiscussionMissionToolCommandsDerivesPAOriginFromTrustedConversation(t *testing.T) {
	cases := []struct {
		name            string
		conversation    discussionsession.ConversationDTO
		wantOriginRoute bool
	}{
		{
			name: "personal assistant conversation",
			conversation: discussionsession.ConversationDTO{
				ConversationID: "conv-pa", ConversationKind: discussionsession.ConversationKindPersonalAssistant,
			},
			wantOriginRoute: true,
		},
		{
			name: "PA-origin mission group",
			conversation: discussionsession.ConversationDTO{
				ConversationID: "conv-pa-mission", ConversationKind: discussionsession.ConversationKindGroup, PersonalAssistantRuntime: true,
			},
			wantOriginRoute: true,
		},
		{
			name: "ordinary group",
			conversation: discussionsession.ConversationDTO{
				ConversationID: "conv-group", ConversationKind: discussionsession.ConversationKindGroup,
			},
			wantOriginRoute: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			missionID := missioncore.MissionID("mission-tool-origin")
			repo := &productionDiscussionMissionToolTestRepo{model: missionprojection.ReadModel{
				TenantID: "tenant-a", Mission: missionprojection.MissionView{TenantID: "tenant-a", MissionID: missionID, Title: "Mission", Status: missionprojection.MissionStatusActive},
				Checkpoint: missionprojection.Checkpoint{MissionID: missionID, UpdatedAt: time.Now().UTC()},
			}}
			sessions := &productionDiscussionMissionToolTestSessions{source: tc.conversation}
			missions := &productionDiscussionMissionToolTestMissionPort{mission: missioncontrol.MissionDTO{MissionID: missionID.String(), Title: "Mission", Status: "active", CreatedAt: time.Now().UTC()}}
			commands := missioncontrol.NewCommandServiceWithMissionPorts(nil, nil, nil, nil, missions)
			api := &productionAPI{missionCommand: commands, missionRepo: repo, discussions: sessions}
			adapter := productionDiscussionMissionToolCommands{api: api}

			_, err := adapter.CreateMissionFromDiscussion(context.Background(), missioncontrol.ViewerRef{
				ViewerID: "account-1", AuthorizationSubjectID: "user-1", ViewerKind: missioncontrol.ViewerKindUser, TenantID: "tenant-a",
			}, tc.conversation.ConversationID, missioncontrol.CreateMissionCommand{
				IdempotencyKey: "tool-origin-" + tc.conversation.ConversationID, Title: "Mission", FinalGoal: "Keep trusted origin",
			})
			if err != nil {
				t.Fatalf("CreateMissionFromDiscussion: %v", err)
			}
			if tc.wantOriginRoute {
				if sessions.resolvedConversationID != tc.conversation.ConversationID || !missions.originCalled || missions.genericCalled {
					t.Fatalf("PA source route resolver=%q origin=%v generic=%v", sessions.resolvedConversationID, missions.originCalled, missions.genericCalled)
				}
				if sessions.ensure.PersonalAssistantOriginOwnerID != "account-1" || sessions.ensure.PersonalAssistantOriginSubjectID != "user-1" {
					t.Fatalf("EnsureMissionConversation provenance = %+v", sessions.ensure)
				}
			} else if sessions.resolvedConversationID != "" || missions.originCalled || !missions.genericCalled {
				t.Fatalf("ordinary group must not become PA source: resolver=%q origin=%v generic=%v", sessions.resolvedConversationID, missions.originCalled, missions.genericCalled)
			}
		})
	}
}

func TestProductionDiscussionMissionToolCommandsDoesNotDerivePAOriginForWorkerAgent(t *testing.T) {
	missionID := missioncore.MissionID("mission-tool-worker")
	repo := &productionDiscussionMissionToolTestRepo{model: missionprojection.ReadModel{
		TenantID:   "tenant-a",
		Mission:    missionprojection.MissionView{TenantID: "tenant-a", MissionID: missionID, Title: "Worker mission", Status: missionprojection.MissionStatusActive},
		Checkpoint: missionprojection.Checkpoint{MissionID: missionID, UpdatedAt: time.Now().UTC()},
	}}
	sessions := &productionDiscussionMissionToolTestSessions{source: discussionsession.ConversationDTO{
		ConversationID: "conv-pa-origin-group", ConversationKind: discussionsession.ConversationKindGroup, PersonalAssistantRuntime: true,
	}}
	missions := &productionDiscussionMissionToolTestMissionPort{mission: missioncontrol.MissionDTO{
		MissionID: missionID.String(), Title: "Worker mission", Status: "active", CreatedAt: time.Now().UTC(),
	}}
	commands := missioncontrol.NewCommandServiceWithMissionPorts(nil, nil, nil, nil, missions)
	adapter := productionDiscussionMissionToolCommands{api: &productionAPI{missionCommand: commands, missionRepo: repo, discussions: sessions}}

	_, err := adapter.CreateMissionFromDiscussion(context.Background(), missioncontrol.ViewerRef{
		ViewerID: "worker-1", ViewerKind: missioncontrol.ViewerKindAgent, TenantID: "tenant-a",
	}, sessions.source.ConversationID, missioncontrol.CreateMissionCommand{
		IdempotencyKey: "worker-in-pa-origin-group", Title: "Worker mission", FinalGoal: "Keep worker identity",
	})
	if err != nil {
		t.Fatalf("CreateMissionFromDiscussion: %v", err)
	}
	if sessions.resolvedConversationID != "" || missions.originCalled || !missions.genericCalled {
		t.Fatalf("worker must use generic creation: resolver=%q origin=%v generic=%v", sessions.resolvedConversationID, missions.originCalled, missions.genericCalled)
	}
	if sessions.ensure.PersonalAssistantOriginOwnerID != "" || sessions.ensure.PersonalAssistantOriginSubjectID != "" || sessions.ensureViewer.ViewerID != "worker-1" {
		t.Fatalf("worker creation inherited PA origin: viewer=%+v ensure=%+v", sessions.ensureViewer, sessions.ensure)
	}
}

func TestProductionAPIRecoversPersonalAssistantOriginWhenMissionConversationIsMissing(t *testing.T) {
	ctx := context.Background()
	missionID := missioncore.MissionID("mission-pa-recover")
	createdAt := time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)
	repo := &productionDiscussionMissionToolTestRepo{model: missionprojection.ReadModel{
		TenantID: "tenant-a",
		Mission: missionprojection.MissionView{
			TenantID: "tenant-a", MissionID: missionID, Title: "PA mission", Status: missionprojection.MissionStatusActive, CreatedAt: createdAt,
			PersonalAssistantOriginOwnerID: "account-1", PersonalAssistantOriginSubjectID: "user-1",
		},
		Checkpoint: missionprojection.Checkpoint{MissionID: missionID, Version: 1, UpdatedAt: createdAt},
	}}
	sessions := &productionDiscussionMissionToolTestSessions{}
	query := &productionDiscussionMissionToolTestQuery{view: missioncontrol.MissionControlViewDTO{
		Mission: missioncontrol.MissionDTO{MissionID: missionID.String(), Title: "PA mission", Status: "active", CreatedAt: createdAt},
	}}
	api := productionAPI{missionQuery: query, missionRepo: repo, discussions: sessions}
	view, err := api.GetMissionControlView(ctx, missioncontrol.ViewerRef{
		ViewerID: "account-1", AuthorizationSubjectID: "user-1", ViewerKind: missioncontrol.ViewerKindUser, TenantID: "tenant-a",
	}, missionID)
	if err != nil {
		t.Fatalf("GetMissionControlView: %v", err)
	}
	if view.Discussion.ConversationID != "conv-mission" || sessions.ensureViewer.ViewerID != "user-1" || sessions.ensureViewer.OwnerID != "account-1" || sessions.ensure.PersonalAssistantOriginOwnerID != "account-1" || sessions.ensure.PersonalAssistantOriginSubjectID != "user-1" {
		t.Fatalf("PA recovery conversation=%+v viewer=%+v ensure=%+v", view.Discussion, sessions.ensureViewer, sessions.ensure)
	}
}

func TestProductionAPIRetriesMissionEnsureWithPersistedPersonalAssistantOrigin(t *testing.T) {
	ctx := context.Background()
	missionID := missioncore.MissionID("mission-pa-retry")
	createdAt := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	repo := &productionDiscussionMissionToolTestRepo{model: missionprojection.ReadModel{
		TenantID: "tenant-a",
		Mission: missionprojection.MissionView{
			TenantID: "tenant-a", MissionID: missionID, Title: "PA mission", Status: missionprojection.MissionStatusActive, CreatedAt: createdAt,
			PersonalAssistantOriginOwnerID: "account-1", PersonalAssistantOriginSubjectID: "user-1",
		},
		Checkpoint: missionprojection.Checkpoint{MissionID: missionID, Version: 1, UpdatedAt: createdAt},
	}}
	ensureFailure := errors.New("ensure discussion unavailable")
	sessions := &productionDiscussionMissionToolTestSessions{ensureErr: ensureFailure}
	missions := &productionDiscussionMissionToolTestMissionPort{mission: missioncontrol.MissionDTO{
		MissionID: missionID.String(), Title: "PA mission", Status: "active", CreatedAt: createdAt,
	}}
	api := productionAPI{
		missionCommand: missioncontrol.NewCommandServiceWithMissionPorts(nil, nil, nil, nil, missions),
		missionRepo:    repo,
		discussions:    sessions,
	}
	viewer := missioncontrol.ViewerRef{
		ViewerID: "account-1", AuthorizationSubjectID: "user-1", ViewerKind: missioncontrol.ViewerKindUser, TenantID: "tenant-a",
	}
	command := missioncontrol.CreateMissionCommand{
		IdempotencyKey: "pa-retry-after-ensure-failure", Title: "PA mission", FinalGoal: "Retain PA origin",
		SourcePersonalAssistantConversationID: "conv-pa-source",
	}
	if _, err := api.CreateMission(ctx, viewer, command); !errors.Is(err, ensureFailure) {
		t.Fatalf("first CreateMission error = %v, want %v", err, ensureFailure)
	}
	if !missions.originCalled || sessions.ensureCalls != 1 {
		t.Fatalf("first create origin route=%v ensure calls=%d", missions.originCalled, sessions.ensureCalls)
	}

	sessions.ensureErr = nil
	command.SourcePersonalAssistantConversationID = ""
	if _, err := api.CreateMission(ctx, viewer, command); err != nil {
		t.Fatalf("source-less retry CreateMission: %v", err)
	}
	if !missions.genericCalled || sessions.ensureCalls != 2 {
		t.Fatalf("source-less retry generic route=%v ensure calls=%d", missions.genericCalled, sessions.ensureCalls)
	}
	if sessions.ensureViewer.ViewerID != "user-1" || sessions.ensureViewer.OwnerID != "account-1" || sessions.ensure.PersonalAssistantOriginOwnerID != "account-1" || sessions.ensure.PersonalAssistantOriginSubjectID != "user-1" {
		t.Fatalf("source-less retry lost PA origin: viewer=%+v ensure=%+v", sessions.ensureViewer, sessions.ensure)
	}
}

func TestProductionDiscussionMissionToolCloseReadiness(t *testing.T) {
	cases := []struct {
		name   string
		viewer missioncontrol.ViewerRef
		tasks  []missioncontrol.TaskCardDTO
		runs   []missioncontrol.RuntimeSessionDTO
		want   string
	}{
		{
			name:   "worker run is active",
			viewer: missioncontrol.ViewerRef{ViewerID: "account-1", ViewerKind: missioncontrol.ViewerKindUser, TenantID: "tenant-a"},
			tasks:  []missioncontrol.TaskCardDTO{{TaskID: "task-worker", Assignee: "agent-1", Status: "started"}},
			runs:   []missioncontrol.RuntimeSessionDTO{{RunID: "run-1", TaskID: "task-worker", AgentID: "agent-1", Status: "running"}},
			want:   discussionsession.MissionClosePending,
		},
		{
			name:   "worker task is approved",
			viewer: missioncontrol.ViewerRef{ViewerID: "account-1", ViewerKind: missioncontrol.ViewerKindUser, TenantID: "tenant-a"},
			tasks:  []missioncontrol.TaskCardDTO{{TaskID: "task-worker", Assignee: "agent-1", Status: "approved", LatestResultRef: "result-1"}},
			want:   discussionsession.MissionCloseCompleted,
		},
		{
			name:   "assigned worker task cannot be submitted by PA",
			viewer: missioncontrol.ViewerRef{ViewerID: "account-1", ViewerKind: missioncontrol.ViewerKindUser, TenantID: "tenant-a"},
			tasks:  []missioncontrol.TaskCardDTO{{TaskID: "task-worker", Assignee: "agent-1", Status: "started"}},
			want:   discussionsession.MissionClosePending,
		},
		{
			name:   "executing agent can submit own started task",
			viewer: missioncontrol.ViewerRef{ViewerID: "agent-1", ViewerKind: missioncontrol.ViewerKindAgent, TenantID: "tenant-a"},
			tasks:  []missioncontrol.TaskCardDTO{{TaskID: "task-worker", Assignee: "agent-1", Status: "started"}},
			want:   discussionsession.MissionCloseReady,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			query := &productionDiscussionMissionToolTestQuery{
				view: missioncontrol.MissionControlViewDTO{
					Mission:   missioncontrol.MissionDTO{MissionID: "mission-1", Status: "active"},
					TaskBoard: missioncontrol.TaskBoardDTO{Tasks: tc.tasks},
				},
				ops: missioncontrol.MissionOpsViewDTO{MissionID: "mission-1", RuntimeSessions: tc.runs},
			}
			adapter := productionDiscussionMissionToolCommands{api: &productionAPI{missionQuery: query}}
			got, err := adapter.CheckMissionCloseReadiness(context.Background(), tc.viewer, "mission-1", "task-worker")
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != tc.want {
				t.Fatalf("readiness = %+v, want status %q", got, tc.want)
			}
		})
	}
}

type productionDiscussionMissionToolTestMissionPort struct {
	mission       missioncontrol.MissionDTO
	genericCalled bool
	originCalled  bool
}

func (p *productionDiscussionMissionToolTestMissionPort) CreateMission(context.Context, missioncontrol.ViewerRef, missioncontrol.CreateMissionCommand) (missioncontrol.MissionDTO, error) {
	p.genericCalled = true
	return p.mission, nil
}

func (p *productionDiscussionMissionToolTestMissionPort) CreateMissionWithPersonalAssistantOrigin(context.Context, missioncontrol.ViewerRef, missioncontrol.CreateMissionCommand, missioncontrol.PersonalAssistantMissionOrigin) (missioncontrol.MissionDTO, error) {
	p.originCalled = true
	return p.mission, nil
}

func (p *productionDiscussionMissionToolTestMissionPort) UpdateMissionBrief(context.Context, missioncontrol.ViewerRef, missioncontrol.UpdateMissionBriefCommand) (missioncontrol.MissionDTO, error) {
	return p.mission, nil
}

func (*productionDiscussionMissionToolTestMissionPort) CreateTask(context.Context, missioncontrol.ViewerRef, missioncontrol.CreateTaskCommand) (missioncontrol.TaskCardDTO, error) {
	return missioncontrol.TaskCardDTO{}, nil
}

func (*productionDiscussionMissionToolTestMissionPort) AssignTask(context.Context, missioncontrol.ViewerRef, missioncontrol.AssignTaskCommand) (missioncontrol.CommandReceiptDTO, error) {
	return missioncontrol.CommandReceiptDTO{}, nil
}

type productionDiscussionMissionToolTestSessions struct {
	source                 discussionsession.ConversationDTO
	resolvedConversationID string
	ensureViewer           discussionsession.ViewerRef
	ensure                 discussionsession.EnsureMissionConversationCommand
	ensureErr              error
	ensureCalls            int
}

func (s *productionDiscussionMissionToolTestSessions) GetConversation(context.Context, discussionsession.ViewerRef, string) (discussionsession.ConversationDTO, error) {
	return s.source, nil
}

func (s *productionDiscussionMissionToolTestSessions) ResolvePersonalAssistantOrigin(_ context.Context, _ discussionsession.ViewerRef, conversationID string) (discussionsession.PersonalAssistantOrigin, error) {
	s.resolvedConversationID = conversationID
	return discussionsession.PersonalAssistantOrigin{OwnerID: "account-1", AuthorizationSubjectID: "user-1"}, nil
}

func (s *productionDiscussionMissionToolTestSessions) EnsureMissionConversation(_ context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.EnsureMissionConversationCommand) (discussionsession.ConversationDTO, error) {
	s.ensureCalls++
	s.ensureViewer = viewer
	s.ensure = cmd
	if s.ensureErr != nil {
		return discussionsession.ConversationDTO{}, s.ensureErr
	}
	return discussionsession.ConversationDTO{ConversationID: "conv-mission"}, nil
}

type productionDiscussionMissionToolTestQuery struct {
	view missioncontrol.MissionControlViewDTO
	ops  missioncontrol.MissionOpsViewDTO
}

func (q *productionDiscussionMissionToolTestQuery) GetMissionControlView(context.Context, missioncontrol.ViewerRef, missioncore.MissionID) (missioncontrol.MissionControlViewDTO, error) {
	return q.view, nil
}

func (q *productionDiscussionMissionToolTestQuery) GetMissionOpsView(context.Context, missioncontrol.ViewerRef, missioncore.MissionID) (missioncontrol.MissionOpsViewDTO, error) {
	return q.ops, nil
}

func (*productionDiscussionMissionToolTestQuery) ListMissions(context.Context, missioncontrol.ViewerRef, missioncontrol.ListMissionsQuery) (missioncontrol.MissionListDTO, error) {
	return missioncontrol.MissionListDTO{}, nil
}

func (*productionDiscussionMissionToolTestSessions) EnsureMissionAgentMember(context.Context, missioncontrol.CanonicalConversationMembershipRequest) error {
	return nil
}

func (*productionDiscussionMissionToolTestSessions) ReconcileMissionAgentMembers(context.Context, missioncontrol.CanonicalConversationMembershipSetRequest) error {
	return nil
}

func (*productionDiscussionMissionToolTestSessions) ReconcileTodoMissionAgentMembers(context.Context, missioncontrol.CanonicalConversationMembershipSetRequest) error {
	return nil
}

type productionDiscussionMissionToolTestRepo struct {
	model missionprojection.ReadModel
}

func (r *productionDiscussionMissionToolTestRepo) Save(_ context.Context, model missionprojection.ReadModel) error {
	r.model = model
	return nil
}

func (r *productionDiscussionMissionToolTestRepo) SaveForTenant(_ context.Context, _ string, model missionprojection.ReadModel) error {
	r.model = model
	return nil
}

func (r *productionDiscussionMissionToolTestRepo) GetMissionControl(context.Context, missioncore.MissionID) (missionprojection.ReadModel, error) {
	return r.model, nil
}

func (r *productionDiscussionMissionToolTestRepo) GetMissionControlForTenant(context.Context, string, missioncore.MissionID) (missionprojection.ReadModel, error) {
	return r.model, nil
}

func (*productionDiscussionMissionToolTestRepo) Delete(context.Context, missioncore.MissionID) error {
	return nil
}
