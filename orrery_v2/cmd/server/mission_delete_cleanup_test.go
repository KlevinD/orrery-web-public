package main

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	authorizationapp "orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/discussionsession"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/projectadmin"
	missioncore "orrery_v2/internal/mission/core"
	missionprojection "orrery_v2/internal/mission/projection"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/testkit"
	toolres "orrery_v2/internal/resource/tool"
)

func TestProductionAPIDeleteMissionDeletesAuthorizationRelations(t *testing.T) {
	ctx := context.Background()
	env := testkit.NewInitializedTestRuntime(t, OntologyModule())

	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	now := time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC)
	model := missionprojection.ReadModel{
		Mission: missionprojection.MissionView{
			MissionID:     "mission-delete-authz",
			Title:         "Delete Mission",
			FinalGoal:     "Clean authz tuples",
			Status:        missionprojection.MissionStatusActive,
			CreatedByID:   "planner",
			CreatedByKind: "user",
			CreatedAt:     now,
		},
		Team: missionprojection.TeamView{Members: []missionprojection.TeamMemberView{
			{AgentID: "agent-1", AgentKind: agentcore.AgentKindAI, MissionRole: "Lead"},
			{AgentID: "human-owner", AgentProfileID: "human-owner", AgentKind: agentcore.AgentKindHuman, MissionRole: "RealHumanContributor"},
		}},
		Discussion: missionprojection.DiscussionView{DiscussionGroupID: "discussion-delete-authz"},
		Checkpoint: missionprojection.Checkpoint{
			MissionID: "mission-delete-authz",
			Version:   1,
			UpdatedAt: now,
		},
		UpdatedAt: now,
	}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatalf("save projection: %v", err)
	}
	core, err := env.Databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	organizations := operations.NewOrganizationCatalogStore(env.DBRouter)
	authorizationService := authorizationapp.NewService(fixedProductionAuthorizationAdapter{decision: authorizationapp.Decision{Allowed: true, Source: authorizationapp.DecisionSourceOpenFGA}})
	ontologyScopes, err := newProductionOntologyScopeState(
		core, organizations, projectadmin.NewMemoryRepository(), repo, toolres.NewMemoryRegistry(),
		authorizationService,
		authorizationapp.NewSnapshotService(nil, nil), &fixedProductionRoleAuthorizer{allowed: true},
	)
	if err != nil {
		t.Fatalf("new ontology scopes: %v", err)
	}
	sink := &authzRelationSink{}
	discussionRepository := discussionsession.NewRepository(env.DBRouter)
	if err := discussionRepository.SaveConversation(ctx, discussionsession.ConversationRecord{
		ConversationID: "discussion-delete-authz", TenantID: "global", Title: "Delete Mission", Status: "active",
		CreatedBy: "planner", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := discussionRepository.SaveMissionLink(ctx, discussionsession.LinkedMissionRecord{
		ConversationID: "discussion-delete-authz", MissionID: "mission-delete-authz", Title: "Delete Mission", Status: "active", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	responsibilityCleaner := &recordingMissionResponsibilityCleaner{}
	api := productionAPI{
		missionRepo:                  repo,
		missionLinkCleaner:           discussionRepository,
		missionResponsibilityCleaner: responsibilityCleaner,
		unitOfWork:                   env.UnitOfWork,
		taskRepo:                     nil,
		authorizationService:         authorizationService,
		authorizationRelations:       authorizationapp.NewRelationProjector(sink),
		ontologySecurity:             &productionOntologySecurityState{Scopes: ontologyScopes},
	}

	viewer := missioncontrol.ViewerRef{ViewerID: "planner", ViewerKind: missioncontrol.ViewerKindUser}
	if err := api.DeleteMission(ctx, viewer, missioncore.MissionID("mission-delete-authz")); err != nil {
		t.Fatalf("DeleteMission: %v", err)
	}

	if _, err := repo.GetMissionControl(ctx, "mission-delete-authz"); err == nil {
		t.Fatal("expected projection to be deleted")
	}
	discussion, err := discussionRepository.GetConversation(ctx, "discussion-delete-authz")
	if err != nil {
		t.Fatal(err)
	}
	if len(discussion.LinkedMissions) != 0 {
		t.Fatalf("deleted Mission links = %+v, want none", discussion.LinkedMissions)
	}
	if responsibilityCleaner.calls != 1 || responsibilityCleaner.tenantID != "global" || responsibilityCleaner.missionID != "mission-delete-authz" {
		t.Fatalf("responsibility cleanup = %+v", responsibilityCleaner)
	}
	if len(sink.deleted) != 10 {
		t.Fatalf("deleted authz tuples = %+v", sink.deleted)
	}
	assertAuthzPlanTuple(t, sink.deleted, "user", "planner", "owner", "mission", "mission-delete-authz")
	assertAuthzPlanTuple(t, sink.deleted, "agent", "agent-1", "owner", "mission", "mission-delete-authz")
	assertAuthzPlanTuple(t, sink.deleted, "user", "human-owner", "owner", "mission", "mission-delete-authz")
	assertAuthzPlanTuple(t, sink.deleted, "agent", "agent-1", "participant", "mission", "mission-delete-authz")
	assertAuthzPlanTuple(t, sink.deleted, "user", "human-owner", "participant", "mission", "mission-delete-authz")
	assertAuthzPlanTuple(t, sink.deleted, "system", qaPlanSystemViewerID, "automation", "mission", "mission-delete-authz")
	assertAuthzPlanTuple(t, sink.deleted, "mission", "mission-delete-authz", "mission", "discussion", "discussion-delete-authz")
	assertAuthzPlanTuple(t, sink.deleted, "user", "planner", "owner", "discussion", "discussion-delete-authz")
	assertAuthzPlanTuple(t, sink.deleted, "agent", "agent-1", "member", "discussion", "discussion-delete-authz")
	assertAuthzPlanTuple(t, sink.deleted, "user", "human-owner", "member", "discussion", "discussion-delete-authz")
}

type recordingMissionResponsibilityCleaner struct {
	calls     int
	tenantID  string
	missionID string
}

func (r *recordingMissionResponsibilityCleaner) TerminateMissionAssignments(_ context.Context, tenantID, missionID, _, _ string, _ time.Time) (int, error) {
	r.calls++
	r.tenantID = tenantID
	r.missionID = missionID
	return 1, nil
}
