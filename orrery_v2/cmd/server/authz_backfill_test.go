package main

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	authorizationapp "orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/developmentproject"
	"orrery_v2/internal/app/identity"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/projectadmin"
	"orrery_v2/internal/app/scmconnection"
	todoapp "orrery_v2/internal/app/todo"
	missioncore "orrery_v2/internal/mission/core"
	missionprojection "orrery_v2/internal/mission/projection"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/testkit"
	toolres "orrery_v2/internal/resource/tool"
)

func TestBuildAuthorizationRelationBackfillSnapshotIncludesMissionOwnerAndResources(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC)
	missions := &authzBackfillMissionRepo{models: []missionprojection.ReadModel{{
		Mission: missionprojection.MissionView{
			MissionID:     "mission-1",
			Title:         "Mission",
			FinalGoal:     "Ship",
			Status:        missionprojection.MissionStatusActive,
			CreatedByID:   "planner",
			CreatedByKind: "user",
			CreatedAt:     at,
		},
		Discussion: missionprojection.DiscussionView{DiscussionGroupID: "discussion-1"},
		Team: missionprojection.TeamView{Members: []missionprojection.TeamMemberView{
			{AgentID: "agent-1", AgentKind: agentcore.AgentKindAI, MissionRole: "Lead"},
			{AgentID: "human-1", AgentProfileID: "human-planner", AgentKind: agentcore.AgentKindHuman, MissionRole: "RealHumanContributor"},
			{AgentID: "worker-1", AgentKind: agentcore.AgentKindAI, MissionRole: "Worker"},
		}},
	}}}
	projects := &authzBackfillProjectRepo{projects: []projectadmin.Project{{
		TenantID:       "org-1",
		ProjectID:      "project-1",
		OrganizationID: "org-1",
		Title:          "Project",
		Status:         projectadmin.ProjectStatusActive,
		CreatedBy:      "planner",
		CreatedAt:      at,
		UpdatedAt:      at,
	}}}
	agents := authzBackfillAgentDirectory{page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{
		{
			AgentProfileID: "agent-1",
			AgentKind:      agentcore.AgentKindAI,
			ToolRefs:       []agentcore.ToolRef{{ToolID: "tool.deploy"}},
			SkillRefs:      []agentcore.SkillRef{{SkillID: "skill.review"}},
		},
		{
			AgentProfileID: "human-planner",
			AgentKind:      agentcore.AgentKindHuman,
			ToolRefs:       []agentcore.ToolRef{{ToolID: "tool.review"}},
		},
	}}}
	ops := authzBackfillOperations{
		graph: operations.OrgGraphDTO{
			Organizations: []operations.OrganizationDTO{{OrganizationID: "org-1", OwnerProfileID: "owner-1"}},
			Departments:   []operations.DepartmentDTO{{DepartmentID: "dept-1", OrganizationID: "org-1", OwnerProfileID: "manager-1"}},
			Members: []operations.WorkforceMemberDTO{
				{
					AgentProfileID: "agent-1",
					AgentKind:      "ai",
					OrganizationID: "org-1",
					DepartmentID:   "dept-1",
					RoleIDs:        []string{"org.manager"},
				},
				{
					AgentProfileID: "human-planner",
					AgentKind:      "human",
					UserAccount:    operations.WorkforceUserAccountDTO{Username: "planner"},
					OrganizationID: "org-1",
					DepartmentID:   "dept-1",
					RoleIDs:        []string{"member"},
				},
			},
		},
		assignments: []operations.RoleAssignmentDTO{{
			Actor:              operations.ActorRefDTO{ActorType: "human", ActorID: "owner-1"},
			RoleID:             "org.owner",
			OrganizationNodeID: "org-1",
			OrganizationCode:   "01",
		}},
	}

	snapshot, err := buildAuthorizationRelationBackfillSnapshot(ctx, missions, nil, projects, nil, nil, nil, ops, agents)
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}

	if len(snapshot.Projects) != 1 || snapshot.Projects[0].Owner.ID != "human-planner" || snapshot.Projects[0].OrganizationID != "org-1" || snapshot.Projects[0].TenantID != "org-1" {
		t.Fatalf("projects = %+v", snapshot.Projects)
	}
	if len(snapshot.Missions) != 1 || snapshot.Missions[0].Owner.ID != "human-planner" || snapshot.Missions[0].Owner.Kind != "user" {
		t.Fatalf("missions = %+v", snapshot.Missions)
	}
	if len(snapshot.Missions[0].Owners) != 2 || snapshot.Missions[0].Owners[0] != (authorizationapp.SubjectRef{Kind: "agent", ID: "agent-1"}) || snapshot.Missions[0].Owners[1] != (authorizationapp.SubjectRef{Kind: "user", ID: "human-planner"}) {
		t.Fatalf("mission owners = %+v", snapshot.Missions[0].Owners)
	}
	if len(snapshot.Missions[0].Participants) != 3 {
		t.Fatalf("participants = %+v", snapshot.Missions[0].Participants)
	}
	if len(snapshot.Missions[0].Automation) != 1 || snapshot.Missions[0].Automation[0] != (authorizationapp.SubjectRef{Kind: "system", ID: qaPlanSystemViewerID}) {
		t.Fatalf("configured Mission automation = %+v", snapshot.Missions[0].Automation)
	}
	if len(snapshot.Discussions) != 1 || snapshot.Discussions[0].DiscussionID != "discussion-1" || snapshot.Discussions[0].MissionID != "mission-1" || len(snapshot.Discussions[0].Members) != 3 {
		t.Fatalf("discussions = %+v", snapshot.Discussions)
	}
	if len(snapshot.Organizations) != 1 || snapshot.Organizations[0].Admin.ID != "owner-1" {
		t.Fatalf("organizations = %+v", snapshot.Organizations)
	}
	if len(snapshot.Departments) != 1 || snapshot.Departments[0].OrganizationID != "org-1" || len(snapshot.Departments[0].Managers) != 1 {
		t.Fatalf("departments = %+v", snapshot.Departments)
	}
	if len(snapshot.WorkforceMembers) != 2 || snapshot.WorkforceMembers[0].Subject.Kind != "agent" || snapshot.WorkforceMembers[0].RoleIDs[0] != "org.manager" || snapshot.WorkforceMembers[1].Subject != (authorizationapp.SubjectRef{Kind: "user", ID: "human-planner"}) {
		t.Fatalf("workforce members = %+v", snapshot.WorkforceMembers)
	}
	if len(snapshot.RoleAssignments) != 1 || snapshot.RoleAssignments[0].Assignee.Kind != "user" || snapshot.RoleAssignments[0].OrganizationID != "org-1" {
		t.Fatalf("role assignments = %+v", snapshot.RoleAssignments)
	}
	if len(snapshot.ResourceBindings) != 3 ||
		snapshot.ResourceBindings[0].Subject != (authorizationapp.SubjectRef{Kind: "agent", ID: "agent-1"}) ||
		snapshot.ResourceBindings[2].Subject != (authorizationapp.SubjectRef{Kind: "user", ID: "human-planner"}) {
		t.Fatalf("resource bindings = %+v", snapshot.ResourceBindings)
	}
}

func TestBuildAuthorizationRelationBackfillSnapshotUsesSystemMissionAutomation(t *testing.T) {
	model := missionprojection.ReadModel{
		TenantID: "tenant-a",
		Mission: missionprojection.MissionView{
			TenantID: "tenant-a", MissionID: "mission-scheduled", CreatedByID: schedulerAgentTaskViewerID, CreatedByKind: "system",
		},
		Discussion: missionprojection.DiscussionView{DiscussionGroupID: "discussion-scheduled"},
		Team:       missionprojection.TeamView{Members: []missionprojection.TeamMemberView{{AgentID: "worker", AgentKind: agentcore.AgentKindAI}}},
	}
	snapshot, err := buildAuthorizationRelationBackfillSnapshot(context.Background(), &authzBackfillMissionRepo{models: []missionprojection.ReadModel{model}}, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}
	if len(snapshot.Missions) != 1 || len(snapshot.Missions[0].Automation) != 2 {
		t.Fatalf("system Mission snapshot = %+v", snapshot.Missions)
	}
	if len(snapshot.Discussions) != 1 || snapshot.Discussions[0].Owner != (authorizationapp.SubjectRef{}) {
		t.Fatalf("system discussion snapshot = %+v", snapshot.Discussions)
	}
	plan := authorizationapp.NewRelationProjector(&authzRelationSink{}).PlanBackfill(snapshot)
	for _, tuple := range plan.Tuples {
		if tuple.Subject.Kind == "system" && tuple.Relation == "owner" {
			t.Fatalf("backfill planned invalid system owner: %+v", tuple)
		}
	}
	assertAuthzPlanTuple(t, plan.Tuples, "system", schedulerAgentTaskViewerID, "automation", "mission", "mission-scheduled")
	assertAuthzPlanTuple(t, plan.Tuples, "system", qaPlanSystemViewerID, "automation", "mission", "mission-scheduled")
}

func TestTodoBackfillDoesNotRestoreSystemPrincipalsAsWorkforceSubjects(t *testing.T) {
	if got := relationSubjectFromTodoActor(todoapp.ViewerKindSystem, agentcore.SystemSuperadminPrincipalID); got != (authorizationapp.SubjectRef{}) {
		t.Fatalf("system superadmin proposer = %+v, want empty", got)
	}
	if got := relationSubjectFromTodoActor(todoapp.ViewerKindSystem, "scheduler-agent-task"); got != (authorizationapp.SubjectRef{}) {
		t.Fatalf("automation proposer = %+v, want empty", got)
	}
}

func assertAuthzPlanTuple(t *testing.T, tuples []authorizationapp.RelationTuple, subjectKind, subjectID, relation, objectType, objectID string) {
	t.Helper()
	for _, tuple := range tuples {
		if tuple.Subject.Kind == subjectKind && tuple.Subject.ID == subjectID && tuple.Relation == relation && tuple.Object.Type == objectType && tuple.Object.ID == objectID {
			return
		}
	}
	t.Fatalf("missing tuple %s:%s %s %s:%s in %+v", subjectKind, subjectID, relation, objectType, objectID, tuples)
}

func TestBuildAuthorizationRelationBackfillSnapshotKeepsDevelopmentResourcesTenantScoped(t *testing.T) {
	ctx := context.Background()
	projects := &authzBackfillDevelopmentProjectRepo{projects: []developmentproject.Project{
		{
			TenantID: "tenant-a", DevelopmentProjectID: "shared-id", OrganizationID: "org-a", CreatedBy: "owner-a",
			Members:            []developmentproject.Member{{SubjectKind: "user", SubjectID: "maintainer-a", Role: developmentproject.MemberRoleMaintainer}},
			RepositoryBindings: []developmentproject.RepositoryBinding{{ConnectionID: "shared-connection"}},
		},
		{
			TenantID: "tenant-b", DevelopmentProjectID: "shared-id", OrganizationID: "org-b", CreatedBy: "owner-b",
			Members:            []developmentproject.Member{{SubjectKind: "agent", SubjectID: "executor-b", Role: developmentproject.MemberRoleExecutor}},
			RepositoryBindings: []developmentproject.RepositoryBinding{{ConnectionID: "shared-connection"}},
		},
	}}
	connections := &authzBackfillSCMConnectionRepo{connections: []scmconnection.Connection{
		{TenantID: "tenant-a", ConnectionID: "shared-connection", OrganizationID: "org-a", OwnerID: "owner-a"},
		{TenantID: "tenant-b", ConnectionID: "shared-connection", OrganizationID: "org-b", OwnerID: "owner-b"},
	}}

	snapshot, err := buildAuthorizationRelationBackfillSnapshot(ctx, nil, nil, nil, projects, connections, nil, nil, nil)
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}
	if len(snapshot.DevelopmentProjects) != 2 || len(snapshot.SCMConnections) != 2 {
		t.Fatalf("snapshot development projects=%+v connections=%+v", snapshot.DevelopmentProjects, snapshot.SCMConnections)
	}
	plan := authorizationapp.NewRelationProjector(&authzRelationSink{}).PlanBackfill(snapshot)
	seenTenants := map[string]bool{}
	for _, tuple := range plan.Tuples {
		if tuple.TenantID == "" || tuple.Object.TenantID == "" {
			t.Fatalf("backfill emitted an empty tenant tuple: %+v", tuple)
		}
		if tuple.TenantID != tuple.Object.TenantID {
			t.Fatalf("tuple tenant mismatch: %+v", tuple)
		}
		seenTenants[tuple.TenantID] = true
	}
	if !seenTenants["tenant-a"] || !seenTenants["tenant-b"] {
		t.Fatalf("tenant-scoped tuples = %+v", plan.Tuples)
	}
	if plan.DevelopmentProjects != 2 || plan.SCMConnections != 2 {
		t.Fatalf("backfill plan counts = %+v", plan)
	}
}

func TestBuildAuthorizationRelationBackfillSnapshotCanonicalizesLegacyTodoParticipants(t *testing.T) {
	todos := &authzBackfillTodoRepo{todos: []todoapp.TodoItem{{
		TenantID: "org-1", TodoID: "todo-1", CreatedBy: "planner", CreatedByKind: todoapp.ViewerKindUser,
		AssigneeID: "agent-1", AssigneeKind: todoapp.AssigneeKindAgent, ReviewerID: "reviewer",
	}}}
	ops := authzBackfillOperations{graph: operations.OrgGraphDTO{Members: []operations.WorkforceMemberDTO{
		{AgentProfileID: "human-planner", AgentKind: "human", UserAccount: operations.WorkforceUserAccountDTO{Username: "planner"}},
		{AgentProfileID: "human-reviewer", AgentKind: "human", UserAccount: operations.WorkforceUserAccountDTO{Username: "reviewer"}},
	}}}

	snapshot, err := buildAuthorizationRelationBackfillSnapshot(context.Background(), nil, nil, nil, nil, nil, todos, ops, nil)
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}
	if len(snapshot.Todos) != 1 {
		t.Fatalf("todos = %+v", snapshot.Todos)
	}
	item := snapshot.Todos[0]
	if item.Proposer != (authorizationapp.SubjectRef{Kind: "user", ID: "human-planner"}) ||
		item.Assignee != (authorizationapp.SubjectRef{Kind: "agent", ID: "agent-1"}) ||
		item.Reviewer != (authorizationapp.SubjectRef{Kind: "user", ID: "human-reviewer"}) {
		t.Fatalf("todo relations = %+v", item)
	}
	plan := authorizationapp.NewRelationProjector(&authzRelationSink{}).PlanBackfill(snapshot)
	if plan.Todos != 1 || len(plan.Tuples) != 3 {
		t.Fatalf("todo backfill plan = %+v", plan)
	}
}

func TestDevelopmentProjectBackfillDoesNotRestoreRemovedCreatorOwnership(t *testing.T) {
	projects := &authzBackfillDevelopmentProjectRepo{projects: []developmentproject.Project{{
		TenantID: "tenant-a", DevelopmentProjectID: "project-1", CreatedBy: "original-owner",
		Members: []developmentproject.Member{
			{SubjectKind: "user", SubjectID: "current-owner", Role: developmentproject.MemberRoleOwner},
			{SubjectKind: "user", SubjectID: "maintainer", Role: developmentproject.MemberRoleMaintainer},
		},
	}}}

	snapshot, err := buildAuthorizationRelationBackfillSnapshot(context.Background(), nil, nil, nil, projects, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}
	if len(snapshot.DevelopmentProjects) != 1 {
		t.Fatalf("development projects = %+v", snapshot.DevelopmentProjects)
	}
	project := snapshot.DevelopmentProjects[0]
	if project.Owner != (authorizationapp.SubjectRef{}) {
		t.Fatalf("creator fallback owner was restored despite current owner membership: %+v", project.Owner)
	}
	plan := authorizationapp.NewRelationProjector(&authzRelationSink{}).PlanBackfill(snapshot)
	for _, tuple := range plan.Tuples {
		if tuple.Object.Type == "development_project" && tuple.Relation == "owner" && tuple.Subject.ID == "original-owner" {
			t.Fatalf("backfill restored original creator ownership: %+v", tuple)
		}
	}
}

func TestAuthorizationBackfillAdvancesCursorsPastInvalidCatalogEntries(t *testing.T) {
	projects := &cursorCheckingProjectRepo{}
	missions := &cursorCheckingMissionRepo{}

	snapshot, err := buildAuthorizationRelationBackfillSnapshot(context.Background(), missions, nil, projects, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}
	if projects.calls != 2 || missions.calls != 2 {
		t.Fatalf("catalog calls: projects=%d missions=%d", projects.calls, missions.calls)
	}
	if len(snapshot.Projects) != 1 || snapshot.Projects[0].ProjectID != "valid-project" {
		t.Fatalf("projects = %+v", snapshot.Projects)
	}
	if len(snapshot.Missions) != 1 || snapshot.Missions[0].MissionID != "valid-mission" {
		t.Fatalf("missions = %+v", snapshot.Missions)
	}
}

func TestRunAuthorizationRelationBackfillSupportsDryRunAndOff(t *testing.T) {
	ctx := context.Background()
	sink := &authzRelationSink{}
	projector := authorizationapp.NewRelationProjector(sink)
	projects := &authzBackfillProjectRepo{projects: []projectadmin.Project{{ProjectID: "project-1", CreatedBy: "planner"}}}

	result, err := runAuthorizationRelationBackfill(ctx, projector, nil, nil, projects, nil, nil, nil, nil, nil, authorizationRelationBackfillOptions{Mode: authorizationRelationBackfillModeDryRun, DryRun: true, Diff: true})
	if err != nil {
		t.Fatalf("run dry-run backfill: %v", err)
	}
	if !result.DryRun || result.Projects != 1 || result.Tuples != 1 || result.Diff.Checked != 1 || result.Diff.Missing != 1 {
		t.Fatalf("dry-run result = %+v", result)
	}
	if result.Report.ByObjectType["project"] != 1 || result.Report.ByRelation["owner"] != 1 {
		t.Fatalf("dry-run report = %+v", result.Report)
	}
	if len(sink.checked) != 0 {
		t.Fatalf("dry-run tuple diff must not use computed relation checks: %+v", sink.checked)
	}
	if len(sink.written) != 0 {
		t.Fatalf("dry-run should not write tuples: %+v", sink.written)
	}

	result, err = runAuthorizationRelationBackfill(ctx, projector, nil, nil, projects, nil, nil, nil, nil, nil, authorizationRelationBackfillOptions{Mode: authorizationRelationBackfillModeOff, Disabled: true})
	if err != nil {
		t.Fatalf("run disabled backfill: %v", err)
	}
	if result.Tuples != 0 || len(sink.written) != 0 {
		t.Fatalf("disabled result=%+v writes=%+v", result, sink.written)
	}
}

func TestAuthorizationRelationReconcilerRepairsMissingWithoutPruningConcurrentTuples(t *testing.T) {
	ctx := context.Background()
	desiredProject := projectadmin.Project{ProjectID: "project-1", CreatedBy: "planner"}
	sink := &authzRelationSink{existing: []authorizationapp.RelationTuple{{
		TenantID: "global",
		Subject:  authorizationapp.SubjectRef{Kind: "user", ID: "former-owner"},
		Relation: "owner",
		Object:   identity.ObjectRef{Type: "project", ID: "project-1", TenantID: "global"},
	}}}
	reconciler := &authorizationRelationReconciler{
		projector: authorizationapp.NewRelationProjector(sink),
		projects:  &authzBackfillProjectRepo{projects: []projectadmin.Project{desiredProject}},
		opts: authorizationRelationBackfillOptions{
			Mode:  authorizationRelationBackfillModeWrite,
			Diff:  true,
			Prune: true,
		},
	}
	if err := reconciler.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(sink.existing) != 2 || len(sink.written) != 1 || len(sink.deleted) != 0 {
		t.Fatalf("existing=%+v written=%+v deleted=%+v", sink.existing, sink.written, sink.deleted)
	}
	writes, deletes := len(sink.written), len(sink.deleted)
	if err := reconciler.Sweep(ctx); err != nil {
		t.Fatalf("second Sweep: %v", err)
	}
	if len(sink.written) != writes || len(sink.deleted) != deletes {
		t.Fatalf("idempotent sweep wrote=%d/%d deleted=%d/%d", len(sink.written), writes, len(sink.deleted), deletes)
	}
}

func TestProductionAuthorizationEnforceTransitionDiagnosticsCombinesShadowAndBackfill(t *testing.T) {
	ctx := context.Background()
	recorder := authorizationapp.NewLoggingShadowRecorder(authzReadinessTestLogger{})
	recorder.RecordAuthorizationShadowDecision(ctx, authorizationapp.ShadowDecisionRecord{
		Request:  authorizationapp.CheckRequest{Principal: identity.Principal{UserID: "planner", ViewerKind: identity.ViewerKindUser}, Action: "organization.manage", Object: identity.ObjectRef{Type: "organization", ID: "org-1"}},
		Primary:  authorizationapp.Decision{Allowed: true},
		Shadow:   authorizationapp.Decision{Allowed: false},
		Mismatch: true,
	})
	api := productionAPI{
		authorizationRelations: authorizationapp.NewRelationProjector(&authzRelationSink{}),
		authorizationShadow:    recorder,
	}

	diagnostics, err := api.GetAuthorizationEnforceTransitionDiagnostics(ctx)
	if err != nil {
		t.Fatalf("GetAuthorizationEnforceTransitionDiagnostics: %v", err)
	}
	if diagnostics.Ready || !diagnostics.BackfillValidated {
		t.Fatalf("diagnostics readiness = %+v", diagnostics)
	}
	if diagnostics.Shadow.Mismatches != 1 || diagnostics.Shadow.ByObjectType["organization"].Mismatches != 1 || diagnostics.Shadow.ByAction["organization.manage"].Mismatches != 1 {
		t.Fatalf("shadow diagnostics = %+v", diagnostics.Shadow)
	}
	if !diagnostics.Backfill.DryRun || diagnostics.Backfill.Diff.Checked != 0 || diagnostics.Backfill.Diff.Missing != 0 {
		t.Fatalf("backfill diagnostics = %+v", diagnostics.Backfill)
	}
}

func TestProductionAPIRejectsHardDeletionOfTodoOriginMission(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	now := time.Date(2026, 7, 2, 11, 0, 0, 0, time.UTC)
	model := missionprojection.ReadModel{
		TenantID: "tenant-a",
		Mission: missionprojection.MissionView{
			TenantID: "tenant-a", MissionID: "mission-todo-history", Title: "Todo execution",
			Status: missionprojection.MissionStatusActive, CreatedByID: "planner", CreatedByKind: "user",
			OriginKind: "todo", OriginTodoID: "todo-1", CreatedAt: now,
		},
		Checkpoint: missionprojection.Checkpoint{MissionID: "mission-todo-history", Version: 1, UpdatedAt: now},
		UpdatedAt:  now,
	}
	if err := repo.SaveForTenant(ctx, "tenant-a", model); err != nil {
		t.Fatal(err)
	}
	api := productionAPI{
		missionRepo: repo,
		authorizationService: authorizationapp.NewService(fixedProductionAuthorizationAdapter{
			decision: authorizationapp.Decision{Allowed: true, Source: authorizationapp.DecisionSourceOpenFGA},
		}),
	}
	viewer := missioncontrol.ViewerRef{ViewerID: "planner", ViewerKind: missioncontrol.ViewerKindUser, TenantID: "tenant-a"}
	if err := api.DeleteMission(ctx, viewer, "mission-todo-history"); !errors.Is(err, missioncontrol.ErrConflict) {
		t.Fatalf("DeleteMission error = %v, want ErrConflict", err)
	}
	if _, err := repo.GetMissionControlForTenant(ctx, "tenant-a", "mission-todo-history"); err != nil {
		t.Fatalf("Todo-origin Mission history was deleted: %v", err)
	}
}

func TestProductionAPIDeleteSystemMissionRemovesAutomationWithoutInvalidOwners(t *testing.T) {
	ctx := context.Background()
	env := testkit.NewInitializedTestRuntime(t, OntologyModule())
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	model := missionprojection.ReadModel{
		TenantID: "tenant-a",
		Mission: missionprojection.MissionView{
			TenantID: "tenant-a", MissionID: "mission-system-delete", Status: missionprojection.MissionStatusActive,
			CreatedByID: schedulerAgentTaskViewerID, CreatedByKind: "system", CreatedAt: now,
		},
		Team:       missionprojection.TeamView{Members: []missionprojection.TeamMemberView{{AgentID: "worker", AgentKind: agentcore.AgentKindAI}}},
		Discussion: missionprojection.DiscussionView{DiscussionGroupID: "discussion-system-delete"},
		Checkpoint: missionprojection.Checkpoint{MissionID: "mission-system-delete", Version: 1, UpdatedAt: now}, UpdatedAt: now,
	}
	if err := repo.SaveForTenant(ctx, "tenant-a", model); err != nil {
		t.Fatalf("save projection: %v", err)
	}
	core, err := env.Databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	authorizationService := authorizationapp.NewService(fixedProductionAuthorizationAdapter{decision: authorizationapp.Decision{Allowed: true, Source: authorizationapp.DecisionSourceOpenFGA}})
	ontologyScopes, err := newProductionOntologyScopeState(core, operations.NewOrganizationCatalogStore(env.DBRouter), projectadmin.NewMemoryRepository(), repo, toolres.NewMemoryRegistry(), authorizationService, authorizationapp.NewSnapshotService(nil, nil), &fixedProductionRoleAuthorizer{allowed: true})
	if err != nil {
		t.Fatalf("new ontology scopes: %v", err)
	}
	sink := &authzRelationSink{}
	api := productionAPI{missionRepo: repo, unitOfWork: env.UnitOfWork, authorizationService: authorizationService, authorizationRelations: authorizationapp.NewRelationProjector(sink), ontologySecurity: &productionOntologySecurityState{Scopes: ontologyScopes}}
	viewer := missioncontrol.ViewerRef{ViewerID: "admin", ViewerKind: missioncontrol.ViewerKindUser, TenantID: "tenant-a"}
	if err := api.DeleteMission(ctx, viewer, "mission-system-delete"); err != nil {
		t.Fatalf("DeleteMission: %v", err)
	}
	assertAuthzPlanTuple(t, sink.deleted, "system", schedulerAgentTaskViewerID, "automation", "mission", "mission-system-delete")
	for _, tuple := range sink.deleted {
		if tuple.Subject.Kind == "system" && tuple.Relation == "owner" {
			t.Fatalf("deletion emitted invalid system owner: %+v", tuple)
		}
	}
}

type authzBackfillMissionRepo struct {
	models []missionprojection.ReadModel
}

type cursorCheckingProjectRepo struct {
	calls int
}

func (r *cursorCheckingProjectRepo) ListProjectCatalog(_ context.Context, after string, _ int) ([]projectadmin.Project, bool, error) {
	r.calls++
	switch r.calls {
	case 1:
		if after != "" {
			return nil, false, errors.New("unexpected initial project cursor")
		}
		return []projectadmin.Project{{TenantID: "global", ProjectID: "invalid-project"}}, true, nil
	case 2:
		if after != "global/invalid-project" {
			return nil, false, errors.New("project cursor did not advance past invalid entry")
		}
		return []projectadmin.Project{{TenantID: "global", ProjectID: "valid-project", CreatedBy: "owner"}}, false, nil
	default:
		return nil, false, errors.New("project catalog requested too many pages")
	}
}

type cursorCheckingMissionRepo struct {
	calls int
}

func (r *cursorCheckingMissionRepo) GetMissionControl(context.Context, missioncore.MissionID) (missionprojection.ReadModel, error) {
	return missionprojection.ReadModel{}, missioncontrol.ErrProjectionNotFound
}

func (r *cursorCheckingMissionRepo) ListMissionCatalog(_ context.Context, after string, _ int) ([]missionprojection.ReadModel, bool, error) {
	r.calls++
	switch r.calls {
	case 1:
		if after != "" {
			return nil, false, errors.New("unexpected initial mission cursor")
		}
		return []missionprojection.ReadModel{{Mission: missionprojection.MissionView{TenantID: "global"}}}, true, nil
	case 2:
		if after != "global/" {
			return nil, false, errors.New("mission cursor did not advance past invalid entry")
		}
		return []missionprojection.ReadModel{{Mission: missionprojection.MissionView{
			TenantID: "global", MissionID: "valid-mission", CreatedByID: "owner", CreatedByKind: "user",
		}}}, false, nil
	default:
		return nil, false, errors.New("mission catalog requested too many pages")
	}
}

func (r *authzBackfillMissionRepo) GetMissionControl(context.Context, missioncore.MissionID) (missionprojection.ReadModel, error) {
	return missionprojection.ReadModel{}, missioncontrol.ErrProjectionNotFound
}

func (r *authzBackfillMissionRepo) ListMissionCatalog(_ context.Context, after string, limit int) ([]missionprojection.ReadModel, bool, error) {
	start := 0
	if after != "" {
		for i, model := range r.models {
			if missionBackfillTenantID(model)+"/"+model.Mission.MissionID.String() == after {
				start = i + 1
				break
			}
		}
	}
	end := start + limit
	if end > len(r.models) {
		end = len(r.models)
	}
	return append([]missionprojection.ReadModel(nil), r.models[start:end]...), end < len(r.models), nil
}

type authzBackfillProjectRepo struct {
	projects []projectadmin.Project
}

type authzBackfillDevelopmentProjectRepo struct {
	projects []developmentproject.Project
}

func (r *authzBackfillDevelopmentProjectRepo) ListCatalog(_ context.Context, after string, limit int) ([]developmentproject.Project, bool, error) {
	start := 0
	if after != "" {
		for i, project := range r.projects {
			if project.TenantID+"/"+project.DevelopmentProjectID == after {
				start = i + 1
				break
			}
		}
	}
	end := start + limit
	if end > len(r.projects) {
		end = len(r.projects)
	}
	return append([]developmentproject.Project(nil), r.projects[start:end]...), end < len(r.projects), nil
}

type authzBackfillSCMConnectionRepo struct {
	connections []scmconnection.Connection
}

type authzBackfillTodoRepo struct {
	todos []todoapp.TodoItem
}

func (r *authzBackfillTodoRepo) ListTodoCatalog(_ context.Context, after string, limit int) ([]todoapp.TodoItem, bool, error) {
	start := 0
	if after != "" {
		for i, item := range r.todos {
			if item.TenantID+"::"+item.TodoID == after {
				start = i + 1
				break
			}
		}
	}
	end := start + limit
	if end > len(r.todos) {
		end = len(r.todos)
	}
	return append([]todoapp.TodoItem(nil), r.todos[start:end]...), end < len(r.todos), nil
}

func (r *authzBackfillSCMConnectionRepo) ListCatalog(_ context.Context, after string, limit int) ([]scmconnection.Connection, bool, error) {
	start := 0
	if after != "" {
		for i, connection := range r.connections {
			if connection.TenantID+"/"+connection.ConnectionID == after {
				start = i + 1
				break
			}
		}
	}
	end := start + limit
	if end > len(r.connections) {
		end = len(r.connections)
	}
	return append([]scmconnection.Connection(nil), r.connections[start:end]...), end < len(r.connections), nil
}

func (r *authzBackfillProjectRepo) ListProjectCatalog(_ context.Context, after string, limit int) ([]projectadmin.Project, bool, error) {
	start := 0
	if after != "" {
		for i, project := range r.projects {
			if project.TenantID+"/"+project.ProjectID == after {
				start = i + 1
				break
			}
		}
	}
	end := start + limit
	if end > len(r.projects) {
		end = len(r.projects)
	}
	return append([]projectadmin.Project(nil), r.projects[start:end]...), end < len(r.projects), nil
}

type authzBackfillAgentDirectory struct {
	page agentcore.AgentDirectoryPage
}

func (d authzBackfillAgentDirectory) QueryDirectory(context.Context, agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	return d.page, nil
}

type authzBackfillOperations struct {
	graph       operations.OrgGraphDTO
	assignments []operations.RoleAssignmentDTO
}

func (o authzBackfillOperations) GetOrgGraph(context.Context, operations.ViewerRef, operations.OrgGraphQuery) (operations.OrgGraphDTO, error) {
	return o.graph, nil
}

func (o authzBackfillOperations) ListRoleAssignments(context.Context, operations.ViewerRef, operations.RoleAssignmentQuery) (operations.RoleAssignmentPageDTO, error) {
	return operations.RoleAssignmentPageDTO{Items: append([]operations.RoleAssignmentDTO(nil), o.assignments...)}, nil
}

type authzRelationSink struct {
	written  []authorizationapp.RelationTuple
	deleted  []authorizationapp.RelationTuple
	checked  []authorizationapp.RelationTuple
	existing []authorizationapp.RelationTuple
}

func (s *authzRelationSink) WriteRelations(_ context.Context, tuples []authorizationapp.RelationTuple) error {
	s.written = append(s.written, tuples...)
	for _, tuple := range tuples {
		if !authzRelationTupleContains(s.existing, tuple) {
			s.existing = append(s.existing, tuple)
		}
	}
	return nil
}

func (s *authzRelationSink) DeleteRelations(_ context.Context, tuples []authorizationapp.RelationTuple) error {
	s.deleted = append(s.deleted, tuples...)
	remaining := s.existing[:0]
	for _, existing := range s.existing {
		if authzRelationTupleContains(tuples, existing) {
			continue
		}
		remaining = append(remaining, existing)
	}
	s.existing = remaining
	return nil
}

func (s *authzRelationSink) CheckRelation(_ context.Context, tuple authorizationapp.RelationTuple) (bool, error) {
	s.checked = append(s.checked, tuple)
	return authzRelationTupleContains(s.existing, tuple), nil
}

func (s *authzRelationSink) ListRelations(context.Context) ([]authorizationapp.RelationTuple, error) {
	return append([]authorizationapp.RelationTuple(nil), s.existing...), nil
}
