package missioncontrol_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
	discussion "orrery_v2/internal/mission/discussion"
	missionteam "orrery_v2/internal/mission/team"
	"orrery_v2/internal/platform/testkit"
)

func TestLifecycleServiceCreatesDiscussionGroupForNewMission(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	discussions := &fakeDiscussionInitializer{}
	authzSink := &missionAuthzSink{}
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{Clock: env.Clock, IDGenerator: env.IDGenerator, Discussion: discussions, AuthorizationRelations: authorization.NewRelationProjector(authzSink)})
	viewer := missioncontrol.ViewerRef{ViewerID: "planner-1", ViewerKind: missioncontrol.ViewerKindUser}

	mission, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{IdempotencyKey: "mission-key", Title: "Launch", FinalGoal: "Ship v2"})
	if err != nil {
		t.Fatalf("CreateMission returned error: %v", err)
	}
	view, err := newAuthorizedQueryService(repo).GetMissionControlView(ctx, viewer, missioncore.MissionID(mission.MissionID))
	if err != nil {
		t.Fatalf("GetMissionControlView returned error: %v", err)
	}
	if view.Discussion.DiscussionGroupID != "discussion-1" {
		t.Fatalf("discussion = %+v", view.Discussion)
	}
	if discussions.missionID != missioncore.MissionID(mission.MissionID) || discussions.goal != "Ship v2" {
		t.Fatalf("created group mission=%q goal=%q", discussions.missionID, discussions.goal)
	}
	if len(discussions.members) != 1 || discussions.members[0].UserID != "planner-1" || !discussions.members[0].CanPost {
		t.Fatalf("discussion members = %+v", discussions.members)
	}
	if !missionAuthzTupleExists(authzSink.written, "mission", mission.MissionID, "mission", "discussion", "discussion-1") ||
		!missionAuthzTupleExists(authzSink.written, "user", "planner-1", "owner", "discussion", "discussion-1") {
		t.Fatalf("discussion authz tuples missing: %+v", authzSink.written)
	}
}

func TestSyncSchedulerMissionAddsAssignedHumanReviewerToExistingMission(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	discussions := &fakeDiscussionInitializer{}
	humans := &fakeHumanProfileBootstrap{profileID: "human-ding"}
	agents := &fakeLifecycleAgentDirectory{entries: []agentcore.AgentDirectoryEntry{{
		AgentProfileID: "jordan-belfort", AgentInstanceID: "jordan-belfort", AgentKind: agentcore.AgentKindAI, Enabled: true,
	}}}
	teamBootstrap := &fakeTeamBootstrap{assignments: []missionteam.MissionAssignment{
		{ID: "assignment-jordan", AgentInstanceID: "jordan-belfort", AgentProfileID: "jordan-belfort", AgentKind: agentcore.AgentKindAI, Role: missionteam.MissionRoleWorker, AssignedAt: env.Clock.Now().UTC()},
		{ID: "assignment-human-ding", AgentInstanceID: "human-ding", AgentProfileID: "human-ding", AgentKind: agentcore.AgentKindHuman, Role: missionteam.MissionRoleRealHumanContributor, AssignedAt: env.Clock.Now().UTC()},
	}}
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{
		Clock: env.Clock, IDGenerator: env.IDGenerator, AgentDirectory: agents, Discussion: discussions, TeamBootstrap: teamBootstrap,
		HumanProfiles: humans, HumanBootstrap: missioncontrol.HumanAgentBootstrap{Enabled: true},
	})
	viewer := missioncontrol.ViewerRef{
		ViewerID: "scheduler-agent-task", ViewerKind: missioncontrol.ViewerKindSystem, TenantID: "tenant-a",
		AuthorizationSubjectID: "scheduler-agent-task", PermissionScope: []string{"mission.write"},
	}
	missionID := missioncore.MissionID("scheduled-mission-reviewer")
	if _, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{
		MissionID: missionID.String(), IdempotencyKey: "scheduled-task-reviewer", Title: "Scheduled content", FinalGoal: "Publish reviewed content",
		OriginKind: "scheduler", OriginID: "scheduled-task-reviewer", InitialAgentProfileID: "jordan-belfort",
	}); err != nil {
		t.Fatal(err)
	}
	command := missioncontrol.SyncSchedulerMissionCommand{
		IdempotencyKey: "sync-scheduled-task-reviewer", MissionID: missionID, ScheduledTaskID: "scheduled-task-reviewer",
		Title: "Scheduled content", FinalGoal: "Publish reviewed content", ScheduledTaskStatus: "enabled", ApprovalAssigneeID: "human-ding",
	}
	if _, err := service.SyncSchedulerMission(ctx, viewer, command); err != nil {
		t.Fatalf("SyncSchedulerMission: %v", err)
	}
	if _, err := service.SyncSchedulerMission(ctx, viewer, command); err != nil {
		t.Fatalf("idempotent SyncSchedulerMission: %v", err)
	}
	stored, err := repo.GetMissionControlForTenant(ctx, viewer.Tenant(), missionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Team.Members) != 2 || stored.Team.Members[1].AgentProfileID != "human-ding" || stored.Team.Members[1].AgentKind != agentcore.AgentKindHuman {
		t.Fatalf("scheduled Mission team = %+v", stored.Team.Members)
	}
	if len(humans.requests) != 1 || humans.requests[0].HumanAgentID != "human-ding" || humans.requests[0].DisplayName != "ding" || len(teamBootstrap.commands) != 2 {
		t.Fatalf("reviewer bootstrap requests=%+v appointments=%+v", humans.requests, teamBootstrap.commands)
	}
	dingMembers := 0
	for _, member := range discussions.members {
		if member.UserID == "human-ding" {
			dingMembers++
		}
	}
	if dingMembers != 1 {
		t.Fatalf("discussion members = %+v", discussions.members)
	}
}

func TestLifecycleServiceCreatesAgentDiscussionMemberForAgentViewer(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	discussions := &fakeDiscussionInitializer{}
	agents := &fakeLifecycleAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
		{AgentProfileID: "planner-agent", AgentInstanceID: "planner-agent", AgentKind: agentcore.AgentKindAI, Enabled: true},
	}}
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{Clock: env.Clock, IDGenerator: env.IDGenerator, AgentDirectory: agents, Discussion: discussions})
	viewer := missioncontrol.ViewerRef{ViewerID: "planner-agent", ViewerKind: missioncontrol.ViewerKindAgent}

	if _, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{IdempotencyKey: "mission-agent-key", Title: "Launch", FinalGoal: "Ship v2"}); err != nil {
		t.Fatalf("CreateMission returned error: %v", err)
	}

	if len(discussions.members) != 1 {
		t.Fatalf("discussion members = %+v", discussions.members)
	}
	member := discussions.members[0]
	if member.AgentProfileID != "planner-agent" || member.AgentInstanceID != "planner-agent" || member.UserID != "" || member.MemberKind != discussion.DiscussionMemberAgent || !member.CanPost {
		t.Fatalf("agent discussion member = %+v", member)
	}
}

func TestLifecycleServiceRejectsAgentDiscussionMemberRuntimeInstanceBinding(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	discussions := &fakeDiscussionInitializer{}
	agents := &fakeLifecycleAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
		{AgentProfileID: "planner-agent", AgentInstanceID: "planner-agent:default", AgentKind: agentcore.AgentKindAI, Enabled: true},
	}}
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{Clock: env.Clock, IDGenerator: env.IDGenerator, AgentDirectory: agents, Discussion: discussions})
	viewer := missioncontrol.ViewerRef{ViewerID: "planner-agent", ViewerKind: missioncontrol.ViewerKindAgent}

	_, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{IdempotencyKey: "mission-agent-key", Title: "Launch", FinalGoal: "Ship v2"})
	if !errors.Is(err, missioncontrol.ErrInvalidRequest) {
		t.Fatalf("CreateMission error = %v, want ErrInvalidRequest", err)
	}
	if len(discussions.members) != 0 {
		t.Fatalf("discussion members = %+v", discussions.members)
	}
}

func TestLifecycleServiceRejectsAgentDiscussionMemberWithoutAgentDirectory(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	discussions := &fakeDiscussionInitializer{}
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{Clock: env.Clock, IDGenerator: env.IDGenerator, Discussion: discussions})
	viewer := missioncontrol.ViewerRef{ViewerID: "planner-agent", ViewerKind: missioncontrol.ViewerKindAgent}

	_, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{IdempotencyKey: "mission-agent-key", Title: "Launch", FinalGoal: "Ship v2"})
	if !errors.Is(err, missioncontrol.ErrServiceDependency) {
		t.Fatalf("CreateMission error = %v, want ErrServiceDependency", err)
	}
	if len(discussions.members) != 0 {
		t.Fatalf("discussion members = %+v", discussions.members)
	}
}

func TestLifecycleServiceRejectsAgentDiscussionMemberWithoutProfileIdentity(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	discussions := &fakeDiscussionInitializer{}
	agents := &fakeLifecycleAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
		{AgentInstanceID: "planner-agent:default", AgentKind: agentcore.AgentKindAI, Enabled: true},
	}}
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{Clock: env.Clock, IDGenerator: env.IDGenerator, AgentDirectory: agents, Discussion: discussions})
	viewer := missioncontrol.ViewerRef{ViewerID: "planner-agent:default", ViewerKind: missioncontrol.ViewerKindAgent}

	_, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{IdempotencyKey: "mission-agent-key", Title: "Launch", FinalGoal: "Ship v2"})
	if !errors.Is(err, missioncontrol.ErrInvalidRequest) {
		t.Fatalf("CreateMission error = %v, want ErrInvalidRequest", err)
	}
	if len(discussions.members) != 0 {
		t.Fatalf("discussion members = %+v", discussions.members)
	}
}

func TestLifecycleServiceBootstrapsSystemRolesThroughTeamAssignments(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	discussions := &fakeDiscussionInitializer{}
	teamBootstrap := &fakeTeamBootstrap{
		assignments: []missionteam.MissionAssignment{
			{ID: "assignment-lead", AgentInstanceID: "lead-agent", AgentProfileID: "lead-agent", Role: missionteam.MissionRoleLead, AssignedAt: env.Clock.Now().UTC()},
		},
	}
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{
		Clock:         env.Clock,
		IDGenerator:   env.IDGenerator,
		Discussion:    discussions,
		TeamBootstrap: teamBootstrap,
		SystemRoles: []missioncontrol.SystemRoleBootstrap{
			{ProfileID: "lead-agent", Role: missionteam.MissionRoleLead, IdempotencySuffix: "lead"},
		},
	})

	_, err := service.CreateMission(ctx, missioncontrol.ViewerRef{ViewerID: "planner-1", ViewerKind: missioncontrol.ViewerKindUser}, missioncontrol.CreateMissionCommand{IdempotencyKey: "mission-key", Title: "Launch", FinalGoal: "Ship v2"})
	if err != nil {
		t.Fatalf("CreateMission returned error: %v", err)
	}

	if len(teamBootstrap.commands) != 1 {
		t.Fatalf("appointments = %+v", teamBootstrap.commands)
	}
	lead := teamBootstrap.commands[0]
	if lead.ProfileID != "lead-agent" || lead.Role != missionteam.MissionRoleLead || lead.AppointedBy.Kind != missionteam.ActorKindSystem {
		t.Fatalf("lead appointment = %+v", lead)
	}
	if lead.IDempotencyKey != "mission-key|system-role|lead" {
		t.Fatalf("lead idempotency key = %q", lead.IDempotencyKey)
	}
	byAgent := map[string]discussion.DiscussionMember{}
	for _, member := range discussions.members {
		if member.AgentInstanceID != "" {
			byAgent[member.AgentInstanceID.String()] = member
		}
	}
	if byAgent["lead-agent"].MissionAssignmentRef != "assignment-lead" || byAgent["lead-agent"].MissionRole != missionteam.MissionRoleLead {
		t.Fatalf("lead discussion member = %+v", byAgent["lead-agent"])
	}
	if _, ok := byAgent["staffing-agent"]; ok {
		t.Fatalf("dedicated staffing member should not be bootstrapped: %+v", byAgent["staffing-agent"])
	}
}

func TestLifecycleServiceBridgesScheduledAgentOrganizationalRoleIntoMission(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	teamBootstrap := &fakeTeamBootstrap{}
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{
		Clock: env.Clock, IDGenerator: env.IDGenerator, TeamBootstrap: teamBootstrap,
	})

	_, err := service.CreateMission(ctx, missioncontrol.ViewerRef{
		ViewerID: "scheduler-agent-task", ViewerKind: missioncontrol.ViewerKindSystem, TenantID: "tenant-a",
	}, missioncontrol.CreateMissionCommand{
		MissionID: "scheduled-mission-legal", IdempotencyKey: "scheduled-task-legal", Title: "Legal responsibility pulse", FinalGoal: "Advance due legal responsibilities",
		OriginKind: "scheduler", OriginID: "scheduled-task-legal", InitialAgentProfileID: "legal-orchestrator-agent",
	})
	if err != nil {
		t.Fatalf("CreateMission: %v", err)
	}
	if len(teamBootstrap.commands) != 1 {
		t.Fatalf("appointments = %+v", teamBootstrap.commands)
	}
	cmd := teamBootstrap.commands[0]
	if cmd.ProfileID != "legal-orchestrator-agent" || cmd.Role != missionteam.MissionRoleWorker || cmd.AppointedBy.Kind != missionteam.ActorKindSystem || !cmd.AllowOrganizationalRoleBridge {
		t.Fatalf("scheduled Agent appointment = %+v", cmd)
	}
}

func TestLifecycleServiceBootstrapsInitialHumanAgentForAgentCreatedMissionThroughTeamAssignment(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	discussions := &fakeDiscussionInitializer{}
	humans := &fakeHumanProfileBootstrap{profileID: "human-local-user"}
	teamBootstrap := &fakeTeamBootstrap{
		assignments: []missionteam.MissionAssignment{
			{ID: "assignment-lead", AgentInstanceID: "lead-agent", AgentProfileID: "lead-agent", Role: missionteam.MissionRoleLead, AssignedAt: env.Clock.Now().UTC()},
			{ID: "assignment-human", AgentInstanceID: "human-local-user", AgentProfileID: "human-local-user", Role: missionteam.MissionRoleRealHumanContributor, AssignedAt: env.Clock.Now().UTC()},
		},
	}
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{
		Clock:         env.Clock,
		IDGenerator:   env.IDGenerator,
		Discussion:    discussions,
		TeamBootstrap: teamBootstrap,
		SystemRoles: []missioncontrol.SystemRoleBootstrap{
			{ProfileID: "lead-agent", Role: missionteam.MissionRoleLead, IdempotencySuffix: "lead"},
		},
		HumanProfiles:  humans,
		HumanBootstrap: missioncontrol.HumanAgentBootstrap{Enabled: true},
	})

	mission, err := service.CreateMission(ctx, missioncontrol.ViewerRef{ViewerID: "lead-agent", ViewerKind: missioncontrol.ViewerKindAgent}, missioncontrol.CreateMissionCommand{
		IdempotencyKey:          "mission-key",
		Title:                   "Launch",
		FinalGoal:               "Ship v2",
		InitialHumanAgentID:     "local-user",
		InitialHumanDisplayName: "本地用户",
	})
	if err != nil {
		t.Fatalf("CreateMission returned error: %v", err)
	}

	if len(humans.requests) != 1 || humans.requests[0].HumanAgentID != "local-user" || humans.requests[0].DisplayName != "本地用户" {
		t.Fatalf("human profile requests = %+v", humans.requests)
	}
	if len(teamBootstrap.commands) != 2 {
		t.Fatalf("appointments = %+v", teamBootstrap.commands)
	}
	cmd := teamBootstrap.commands[1]
	if cmd.ProfileID != "human-local-user" || cmd.Role != missionteam.MissionRoleRealHumanContributor || !cmd.AllowHuman || cmd.AllowAI || !cmd.AllowOrganizationalRoleBridge || cmd.AppointedBy.Kind != missionteam.ActorKindSystem {
		t.Fatalf("human appointment = %+v", cmd)
	}
	if cmd.IDempotencyKey != "mission-key|human|initial" {
		t.Fatalf("human idempotency key = %q", cmd.IDempotencyKey)
	}
	var member discussion.DiscussionMember
	for _, candidate := range discussions.members {
		if candidate.UserID == "local-user" {
			member = candidate
			break
		}
	}
	if member.UserID != "local-user" || member.AgentInstanceID != "" || member.MissionAssignmentRef != "assignment-human" || member.MissionRole != missionteam.MissionRoleRealHumanContributor || member.MemberKind != discussion.DiscussionMemberHuman || !member.CanPost || !member.CanMention {
		t.Fatalf("human discussion member = %+v", member)
	}
	for _, candidate := range discussions.members {
		if candidate.AgentInstanceID == "lead-agent" && candidate.MissionAssignmentRef == "" {
			t.Fatalf("lead viewer should not bypass staffing with a direct discussion member: %+v", discussions.members)
		}
	}
	view, err := newAuthorizedQueryService(repo).GetMissionControlView(ctx, missioncontrol.ViewerRef{ViewerID: "local-user", ViewerKind: missioncontrol.ViewerKindUser}, missioncore.MissionID(mission.MissionID))
	if err != nil {
		t.Fatalf("GetMissionControlView returned error: %v", err)
	}
	memberRoles := map[string]string{}
	memberInstances := map[string]string{}
	for _, member := range view.Team.Members {
		memberRoles[member.AgentID] = member.MissionRole
		memberInstances[member.AgentID] = member.AgentInstanceID
	}
	if memberRoles["human-local-user"] != string(missionteam.MissionRoleRealHumanContributor) || memberInstances["human-local-user"] != "human-local-user" {
		t.Fatalf("team members = %+v", view.Team.Members)
	}
	assignmentRoles := map[string]string{}
	for _, assignment := range view.Team.Assignments {
		assignmentRoles[assignment.AssignmentID] = assignment.MissionRole
	}
	if assignmentRoles["assignment-human"] != string(missionteam.MissionRoleRealHumanContributor) {
		t.Fatalf("team assignments = %+v", view.Team.Assignments)
	}
}

func TestLifecycleServiceProjectsReadableNamesForBootstrappedMissionMembers(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	humans := &fakeHumanProfileBootstrap{profileID: "human-local-user", profile: agentcore.AgentProfile{ID: "human-local-user", AgentKind: agentcore.AgentKindHuman, RealName: "本地真人", DisplayName: "本地真人"}}
	agents := &fakeLifecycleAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
		{AgentProfileID: "lead-agent", AgentInstanceID: "lead-agent", RealName: "流程负责人", DisplayName: "流程负责人", RoleName: "Lead", AgentKind: agentcore.AgentKindAI, Enabled: true},
		{AgentProfileID: "human-local-user", AgentInstanceID: "human-local-user", RealName: "本地真人", DisplayName: "本地真人", RoleName: "RealHumanContributor", AgentKind: agentcore.AgentKindHuman, Enabled: true},
	}}
	teamBootstrap := &fakeTeamBootstrap{
		assignments: []missionteam.MissionAssignment{
			{ID: "assignment-lead", AgentInstanceID: "lead-agent", AgentProfileID: "lead-agent", Role: missionteam.MissionRoleLead, AssignedAt: env.Clock.Now().UTC()},
			{ID: "assignment-human", AgentInstanceID: "human-local-user", AgentProfileID: "human-local-user", Role: missionteam.MissionRoleRealHumanContributor, AssignedAt: env.Clock.Now().UTC()},
		},
	}
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{
		Clock:          env.Clock,
		IDGenerator:    env.IDGenerator,
		AgentDirectory: agents,
		TeamBootstrap:  teamBootstrap,
		SystemRoles: []missioncontrol.SystemRoleBootstrap{
			{ProfileID: "lead-agent", Role: missionteam.MissionRoleLead, IdempotencySuffix: "lead"},
		},
		HumanProfiles:  humans,
		HumanBootstrap: missioncontrol.HumanAgentBootstrap{Enabled: true},
	})

	mission, err := service.CreateMission(ctx, missioncontrol.ViewerRef{ViewerID: "lead-agent", ViewerKind: missioncontrol.ViewerKindAgent}, missioncontrol.CreateMissionCommand{
		IdempotencyKey:      "mission-key",
		Title:               "Launch",
		FinalGoal:           "Ship v2",
		InitialHumanAgentID: "local-user",
	})
	if err != nil {
		t.Fatalf("CreateMission returned error: %v", err)
	}
	view, err := newAuthorizedQueryService(repo).GetMissionControlView(ctx, missioncontrol.ViewerRef{ViewerID: "local-user", ViewerKind: missioncontrol.ViewerKindUser}, missioncore.MissionID(mission.MissionID))
	if err != nil {
		t.Fatalf("GetMissionControlView returned error: %v", err)
	}

	names := map[string]string{}
	instances := map[string]string{}
	for _, member := range view.Team.Members {
		names[member.AgentID] = member.DisplayName
		instances[member.AgentID] = member.AgentInstanceID
		if strings.Contains(member.DisplayName, ":") {
			t.Fatalf("team member leaked internal id as display name: %+v", member)
		}
	}
	if names["lead-agent"] != "流程负责人" || names["human-local-user"] != "本地真人" {
		t.Fatalf("team member display names = %+v, team=%+v", names, view.Team.Members)
	}
	if instances["lead-agent"] != "lead-agent" || instances["human-local-user"] != "human-local-user" {
		t.Fatalf("team member instance refs = %+v, team=%+v", instances, view.Team.Members)
	}
}

func TestLifecycleServiceFailsMissionCreationWhenSystemRoleAppointmentFails(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	discussions := &fakeDiscussionInitializer{}
	teamBootstrap := &fakeTeamBootstrap{err: errors.New("staffing unavailable")}
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{
		Clock:         env.Clock,
		IDGenerator:   env.IDGenerator,
		Discussion:    discussions,
		TeamBootstrap: teamBootstrap,
		SystemRoles: []missioncontrol.SystemRoleBootstrap{
			{ProfileID: "lead-agent", Role: missionteam.MissionRoleLead, IdempotencySuffix: "lead"},
		},
	})

	_, err := service.CreateMission(ctx, missioncontrol.ViewerRef{ViewerID: "planner-1", ViewerKind: missioncontrol.ViewerKindUser}, missioncontrol.CreateMissionCommand{IdempotencyKey: "mission-key", Title: "Launch", FinalGoal: "Ship v2"})
	if err == nil {
		t.Fatal("expected CreateMission to fail when system role appointment fails")
	}
	if len(teamBootstrap.commands) != 1 {
		t.Fatalf("appointments = %+v", teamBootstrap.commands)
	}
	if len(discussions.members) != 0 {
		t.Fatalf("discussion members should not be added after failed bootstrap: %+v", discussions.members)
	}
	if _, getErr := repo.GetMissionControl(ctx, teamBootstrap.commands[0].MissionID); !errors.Is(getErr, missioncontrol.ErrProjectionNotFound) {
		t.Fatalf("mission projection should not be saved after failed bootstrap, got %v", getErr)
	}
}
