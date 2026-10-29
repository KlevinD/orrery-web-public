package missioncontrol

import (
	"context"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	missioncore "orrery_v2/internal/mission/core"
	discussion "orrery_v2/internal/mission/discussion"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/mission/projection"
	missionteam "orrery_v2/internal/mission/team"
	"orrery_v2/internal/platform/eventstore"
	"orrery_v2/internal/platform/testkit"
)

type sagaAuthorizationAdapter struct{}

func (sagaAuthorizationAdapter) Check(context.Context, authorization.CheckRequest) (authorization.Decision, error) {
	return authorization.Decision{Allowed: true}, nil
}

func (sagaAuthorizationAdapter) BatchCheck(_ context.Context, requests []authorization.CheckRequest) ([]authorization.Decision, error) {
	out := make([]authorization.Decision, len(requests))
	for index := range out {
		out[index].Allowed = true
	}
	return out, nil
}

func (sagaAuthorizationAdapter) ListObjects(context.Context, identity.Principal, string, string, string) ([]identity.ObjectRef, error) {
	return nil, nil
}

func (sagaAuthorizationAdapter) WriteRelations(context.Context, []authorization.RelationTuple) error {
	return nil
}

func (sagaAuthorizationAdapter) DeleteRelations(context.Context, []authorization.RelationTuple) error {
	return nil
}

type sagaDiscussion struct {
	missionID missioncore.MissionID
	createErr error
	members   map[string]discussion.DiscussionMember
}

func (d *sagaDiscussion) CreateGroup(ctx context.Context, missionID missioncore.MissionID, goal string) (discussion.DiscussionGroup, error) {
	return d.CreateGroupForTenant(ctx, "global", missionID, goal)
}

func (d *sagaDiscussion) CreateGroupForTenant(_ context.Context, tenantID string, missionID missioncore.MissionID, goal string) (discussion.DiscussionGroup, error) {
	d.missionID = missionID
	if d.createErr != nil {
		return discussion.DiscussionGroup{}, d.createErr
	}
	return discussion.DiscussionGroup{ID: discussion.DiscussionGroupID("discussion-" + missionID.String()), TenantID: tenantID, MissionID: missionID, Goal: goal}, nil
}

func (d *sagaDiscussion) AddMember(_ context.Context, groupID discussion.DiscussionGroupID, member discussion.DiscussionMember) error {
	if d.members == nil {
		d.members = map[string]discussion.DiscussionMember{}
	}
	member.DiscussionGroupID = groupID
	key := member.UserID
	if key == "" {
		key = member.AgentProfileID.String()
	}
	d.members[key] = member
	return nil
}

type sagaTeamBootstrap struct {
	missionID missioncore.MissionID
	err       error
}

func (b *sagaTeamBootstrap) Appoint(_ context.Context, cmd missionteam.AppointmentCommand) (missionteam.MissionAssignment, error) {
	b.missionID = cmd.MissionID
	if b.err != nil {
		return missionteam.MissionAssignment{}, b.err
	}
	return missionteam.MissionAssignment{
		ID: missionteam.MissionAssignmentID(cmd.IDempotencyKey), MissionID: cmd.MissionID,
		AgentProfileID: cmd.ProfileID, AgentInstanceID: agentcore.AgentInstanceID(cmd.ProfileID), AgentKind: agentcore.AgentKindAI, Role: cmd.Role,
	}, nil
}

type sagaProjectionRepository struct {
	*DBProjectionRepository
	saveErr   error
	missionID missioncore.MissionID
}

func (r *sagaProjectionRepository) Save(ctx context.Context, model projection.ReadModel) error {
	return r.SaveForTenant(ctx, missionModelTenantID(model), model)
}

func (r *sagaProjectionRepository) SaveForTenant(ctx context.Context, tenantID string, model projection.ReadModel) error {
	r.missionID = model.Mission.MissionID
	if r.saveErr != nil {
		return r.saveErr
	}
	return r.DBProjectionRepository.SaveForTenant(ctx, tenantID, model)
}

type sagaRelationSink struct {
	err       error
	missionID string
	tuples    map[string]authorization.RelationTuple
}

type sagaAuditRecorder struct {
	err    error
	events map[string]AuditEvent
}

func (r *sagaAuditRecorder) RecordAuditEvent(_ context.Context, event AuditEvent) error {
	if r.err != nil {
		return r.err
	}
	if r.events == nil {
		r.events = map[string]AuditEvent{}
	}
	r.events[event.AuditID] = event
	return nil
}

func (s *sagaRelationSink) WriteRelations(_ context.Context, tuples []authorization.RelationTuple) error {
	for _, tuple := range tuples {
		if tuple.Object.Type == "mission" {
			s.missionID = tuple.Object.ID
		}
	}
	if s.err != nil {
		return s.err
	}
	if s.tuples == nil {
		s.tuples = map[string]authorization.RelationTuple{}
	}
	for _, tuple := range tuples {
		key := tuple.Subject.Kind + ":" + tuple.Subject.ID + "|" + tuple.Relation + "|" + tuple.Object.Type + ":" + tuple.Object.ID
		s.tuples[key] = tuple
	}
	return nil
}

func (s *sagaRelationSink) DeleteRelations(context.Context, []authorization.RelationTuple) error {
	return nil
}

func TestMissionCreationSagaRecoversEveryExternalStageAfterRestart(t *testing.T) {
	stages := []string{"discussion", "team", "relations", "projection"}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			ctx := context.Background()
			env := testkit.New(t)
			defer env.Close()
			failure := errors.New(stage + " unavailable")
			discussions := &sagaDiscussion{}
			team := &sagaTeamBootstrap{}
			relations := &sagaRelationSink{}
			repo := &sagaProjectionRepository{DBProjectionRepository: NewDBProjectionRepository(env.DBRouter)}
			switch stage {
			case "discussion":
				discussions.createErr = failure
			case "team":
				team.err = failure
			case "relations":
				relations.err = failure
			case "projection":
				repo.saveErr = failure
			}
			options := LifecycleOptions{
				Clock: env.Clock, IDGenerator: env.IDGenerator, Discussion: discussions, TeamBootstrap: team,
				SystemRoles:            []SystemRoleBootstrap{{ProfileID: "lead-agent", Role: missionteam.MissionRoleLead, IdempotencySuffix: "lead"}},
				AuthorizationService:   authorization.NewService(sagaAuthorizationAdapter{}),
				AuthorizationRelations: authorization.NewRelationProjector(relations),
			}
			service := NewLifecycleService(env.UnitOfWork, env.DBRouter, repo, options)
			viewer := ViewerRef{ViewerID: "planner", ViewerKind: ViewerKindUser, TenantID: "global"}
			command := CreateMissionCommand{IdempotencyKey: "durable-create", Title: "Durable mission", FinalGoal: "Recover every stage"}
			if _, err := service.CreateMission(ctx, viewer, command); !errors.Is(err, failure) {
				t.Fatalf("CreateMission error = %v, want %v", err, failure)
			}

			missionID := discussions.missionID
			if missionID.IsZero() {
				missionID = team.missionID
			}
			if missionID.IsZero() && relations.missionID != "" {
				missionID = missioncore.MissionID(relations.missionID)
			}
			if missionID.IsZero() {
				missionID = repo.missionID
			}
			if missionID.IsZero() {
				t.Fatal("failed stage did not retain the stable mission id")
			}
			if _, err := repo.GetMissionControlForTenant(ctx, "global", missionID); !errors.Is(err, ErrProjectionNotFound) {
				t.Fatalf("incomplete mission was published: %v", err)
			}

			discussions.createErr = nil
			team.err = nil
			relations.err = nil
			repo.saveErr = nil
			restarted := NewLifecycleService(env.UnitOfWork, env.DBRouter, repo, options)
			if err := restarted.ReconcilePendingMissionCreations(ctx, 100); err != nil {
				t.Fatalf("ReconcilePendingMissionCreations: %v", err)
			}
			model, err := repo.GetMissionControlForTenant(ctx, "global", missionID)
			if err != nil || model.Mission.MissionID != missionID || model.Discussion.DiscussionGroupID == "" || len(model.Team.Members) != 1 {
				t.Fatalf("recovered model = %+v, err=%v", model, err)
			}
			if !relationTupleMapExists(relations.tuples, "agent", "lead-agent", "owner", "mission", missionID.String()) {
				t.Fatalf("configured Mission Lead was not granted ownership: %+v", relations.tuples)
			}
			retried, err := restarted.CreateMission(ctx, viewer, command)
			if err != nil || retried.MissionID != missionID.String() {
				t.Fatalf("idempotent retry = %+v, err=%v", retried, err)
			}
		})
	}
}

func TestMissionControlOwnersFromTeamIncludesLeadsAndHumanContributors(t *testing.T) {
	owners := missionControlOwnersFromTeam([]projection.TeamMemberView{
		{AgentID: "lead-agent", AgentKind: agentcore.AgentKindAI, MissionRole: string(missionteam.MissionRoleLead)},
		{AgentID: "worker-agent", AgentKind: agentcore.AgentKindAI, MissionRole: string(missionteam.MissionRoleWorker)},
		{AgentID: "legacy-human", AgentProfileID: "human-owner", AgentKind: agentcore.AgentKindHuman, MissionRole: string(missionteam.MissionRoleRealHumanContributor)},
	})
	if len(owners) != 2 || owners[0] != (authorization.SubjectRef{Kind: "agent", ID: "lead-agent"}) || owners[1] != (authorization.SubjectRef{Kind: "user", ID: "human-owner"}) {
		t.Fatalf("Mission control owners = %+v", owners)
	}
}

func TestMissionCreationIdempotencyKeyRejectsDifferentPayload(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := NewDBProjectionRepository(env.DBRouter)
	relations := &sagaRelationSink{}
	service := NewLifecycleService(env.UnitOfWork, env.DBRouter, repo, LifecycleOptions{
		Clock: env.Clock, IDGenerator: env.IDGenerator,
		AuthorizationService: authorization.NewService(sagaAuthorizationAdapter{}), AuthorizationRelations: authorization.NewRelationProjector(relations),
	})
	viewer := ViewerRef{ViewerID: "planner", ViewerKind: ViewerKindUser}
	if _, err := service.CreateMission(ctx, viewer, CreateMissionCommand{IdempotencyKey: "same-key", FinalGoal: "First"}); err != nil {
		t.Fatalf("first CreateMission: %v", err)
	}
	if _, err := service.CreateMission(ctx, viewer, CreateMissionCommand{IdempotencyKey: "same-key", FinalGoal: "Different"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("reused idempotency key error = %v", err)
	}
}

func TestMissionCreationPersonalAssistantOriginSurvivesReconcileAndScopesIdempotency(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	failure := errors.New("projection unavailable")
	repo := &sagaProjectionRepository{DBProjectionRepository: NewDBProjectionRepository(env.DBRouter), saveErr: failure}
	events := missionevent.NewStoreSink(eventstore.New(env.DB))
	service := NewLifecycleService(env.UnitOfWork, env.DBRouter, repo, LifecycleOptions{
		Clock: env.Clock, IDGenerator: env.IDGenerator,
		AuthorizationService: authorization.NewService(sagaAuthorizationAdapter{}), AuthorizationRelations: authorization.NewRelationProjector(&sagaRelationSink{}),
		MissionEvents: events,
	})
	viewer := ViewerRef{ViewerID: "account-1", AuthorizationSubjectID: "user-1", ViewerKind: ViewerKindUser, TenantID: "tenant-a"}
	origin, err := NewPersonalAssistantMissionOrigin("account-1", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	command := CreateMissionCommand{IdempotencyKey: "pa-durable", Title: "PA mission", FinalGoal: "Keep PA provenance"}
	if _, err := service.CreateMissionWithPersonalAssistantOrigin(ctx, viewer, command, origin); !errors.Is(err, failure) {
		t.Fatalf("CreateMissionWithPersonalAssistantOrigin error = %v, want %v", err, failure)
	}
	missionID := repo.missionID
	if missionID.IsZero() {
		t.Fatal("projection failure did not retain mission id")
	}
	intent, _, ok, err := service.loadMissionCreationIntent(ctx, missionCreationIntentKey(viewer, command.IdempotencyKey))
	if err != nil || !ok || !intent.MissionCreatedEventRequired || !intent.MissionCreatedEventPublished || intent.PersonalAssistantOriginOwnerID != "account-1" || intent.PersonalAssistantOriginSubjectID != "user-1" {
		t.Fatalf("persisted creation intent = %+v ok=%v err=%v", intent, ok, err)
	}
	reader := missionevent.NewStoreReader(eventstore.New(env.DB))
	storedEvents, err := reader.LoadMissionForTenant(ctx, "tenant-a", missionID)
	if err != nil || len(storedEvents) != 1 || storedEvents[0].StreamVersion != 1 || storedEvents[0].EventType != missionevent.EventMissionCreated {
		t.Fatalf("stored MissionCreated events = %+v err=%v", storedEvents, err)
	}
	created, ok := storedEvents[0].Payload.(missionevent.MissionCreatedPayload)
	if !ok || created.PersonalAssistantOriginOwnerID != "account-1" || created.PersonalAssistantOriginSubjectID != "user-1" {
		t.Fatalf("stored MissionCreated payload = %#v", storedEvents[0].Payload)
	}

	repo.saveErr = nil
	if err := service.ReconcilePendingMissionCreations(ctx, 100); err != nil {
		t.Fatalf("ReconcilePendingMissionCreations: %v", err)
	}
	model, err := repo.GetMissionControlForTenant(ctx, "tenant-a", missionID)
	if err != nil || model.Mission.PersonalAssistantOriginOwnerID != "account-1" || model.Mission.PersonalAssistantOriginSubjectID != "user-1" {
		t.Fatalf("recovered PA origin = %+v err=%v", model.Mission, err)
	}
	storedEvents, err = reader.LoadMissionForTenant(ctx, "tenant-a", missionID)
	if err != nil || len(storedEvents) != 1 {
		t.Fatalf("MissionCreated event after reconcile = %+v err=%v", storedEvents, err)
	}
	replayed, err := projection.NewReplayService(projection.NewMemoryEventReader(storedEvents), projection.NewProjector()).ReplayMission(ctx, missionID)
	if err != nil || replayed.Mission.PersonalAssistantOriginOwnerID != "account-1" || replayed.Mission.PersonalAssistantOriginSubjectID != "user-1" {
		t.Fatalf("replayed PA origin = %+v err=%v", replayed.Mission, err)
	}
	otherOrigin, err := NewPersonalAssistantMissionOrigin("account-2", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateMissionWithPersonalAssistantOrigin(ctx, viewer, command, otherOrigin); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("same idempotency key with different PA origin error = %v, want ErrInvalidRequest", err)
	}
	retried, err := service.CreateMission(ctx, viewer, command)
	if err != nil || retried.MissionID != missionID.String() {
		t.Fatalf("same idempotency key without transport origin = %+v err=%v", retried, err)
	}
}

func TestTodoMissionCreationSagaRecoversAuditWithoutGrantingAutomationOwnership(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	failure := errors.New("audit unavailable")
	repo := &sagaProjectionRepository{DBProjectionRepository: NewDBProjectionRepository(env.DBRouter)}
	relations := &sagaRelationSink{}
	audit := &sagaAuditRecorder{err: failure}
	options := LifecycleOptions{
		Clock: env.Clock, IDGenerator: env.IDGenerator,
		AuthorizationService: authorization.NewService(sagaAuthorizationAdapter{}), AuthorizationRelations: authorization.NewRelationProjector(relations),
		SystemAutomation: []authorization.SubjectRef{{Kind: "system", ID: "responsibility-coordinator"}},
		AuditRecorder:    audit,
	}
	service := NewLifecycleService(env.UnitOfWork, env.DBRouter, repo, options)
	viewer := ViewerRef{ViewerID: "todo-automation-agent", ViewerKind: ViewerKindSystem, TenantID: "tenant-a", AuthorizationSubjectID: "todo-automation-agent"}
	command := CreateMissionCommand{
		RequestID: "todo-run-1", IdempotencyKey: "todo-run-1", Title: "Todo mission", FinalGoal: "Implement the approved plan",
		RequesterID: "approver-login", InitialHumanAgentID: "human-approver", OriginKind: "todo", OriginID: "todo-1", OriginTodoID: "todo-1",
		ExecutionAttempt: 1, DiscussionMode: "existing_conversation", OriginDevelopmentProjectID: "development-project-1", ExistingConversationID: "conversation-todo-1",
	}
	if _, err := service.CreateMission(ctx, viewer, command); !errors.Is(err, failure) {
		t.Fatalf("CreateMission audit error = %v, want %v", err, failure)
	}
	missionID := repo.missionID
	model, err := repo.GetMissionControlForTenant(ctx, "tenant-a", missionID)
	if err != nil || model.Discussion.ConversationID != "conversation-todo-1" || model.Discussion.DiscussionGroupID != "" {
		t.Fatalf("projection before audit recovery = %+v err=%v", model, err)
	}
	for _, tuple := range relations.tuples {
		if tuple.Object.Type == "mission" && tuple.Object.ID == missionID.String() && tuple.Relation == "owner" {
			t.Fatalf("Todo automation principal became Mission owner: %+v", tuple)
		}
	}
	assertSagaMissionOriginTuple(t, relations.tuples, missionID.String(), "origin_todo", "todo", "todo-1")
	assertSagaMissionOriginTuple(t, relations.tuples, missionID.String(), "origin_development_project", "development_project", "development-project-1")
	if !relationTupleMapExists(relations.tuples, "system", "responsibility-coordinator", "automation", "mission", missionID.String()) {
		t.Fatalf("Todo Mission did not grant coordinator automation: %+v", relations.tuples)
	}

	audit.err = nil
	restarted := NewLifecycleService(env.UnitOfWork, env.DBRouter, repo, options)
	if err := restarted.ReconcilePendingMissionCreations(ctx, 100); err != nil {
		t.Fatalf("ReconcilePendingMissionCreations: %v", err)
	}
	auditID := missionCreationAuditID("tenant-a", missionID.String())
	event, ok := audit.events[auditID]
	if !ok || len(audit.events) != 1 || event.ActorID != "todo-automation-agent" || event.Metadata["origin_id"] != "todo-1" || event.Metadata["origin_todo_id"] != "todo-1" || event.Metadata["execution_attempt"] != "1" || event.Metadata["discussion_mode"] != "existing_conversation" || event.Metadata["initial_human_agent_profile_id"] != "human-approver" {
		t.Fatalf("recovered mission audit = %+v all=%+v", event, audit.events)
	}
	if _, err := restarted.CreateMission(ctx, viewer, command); err != nil {
		t.Fatalf("idempotent CreateMission after recovery: %v", err)
	}
	if len(audit.events) != 1 {
		t.Fatalf("idempotent replay duplicated audit records: %+v", audit.events)
	}
}

func TestSystemMissionCreationUsesAutomationWithoutDiscussionOwner(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := NewDBProjectionRepository(env.DBRouter)
	relations := &sagaRelationSink{}
	service := NewLifecycleService(env.UnitOfWork, env.DBRouter, repo, LifecycleOptions{
		Clock: env.Clock, IDGenerator: env.IDGenerator, Discussion: &sagaDiscussion{},
		AuthorizationService: authorization.NewService(sagaAuthorizationAdapter{}), AuthorizationRelations: authorization.NewRelationProjector(relations),
		SystemAutomation: []authorization.SubjectRef{{Kind: "system", ID: "responsibility-coordinator"}},
	})
	viewer := ViewerRef{ViewerID: "scheduler-agent-task", ViewerKind: ViewerKindSystem, TenantID: "tenant-a", AuthorizationSubjectID: "scheduler-agent-task"}
	mission, err := service.CreateMission(ctx, viewer, CreateMissionCommand{IdempotencyKey: "scheduled-run-1", Title: "Scheduled", FinalGoal: "Run scheduled work"})
	if err != nil {
		t.Fatalf("CreateMission: %v", err)
	}
	if !relationTupleMapExists(relations.tuples, "system", "scheduler-agent-task", "automation", "mission", mission.MissionID) {
		t.Fatalf("system creator was not projected as Mission automation: %+v", relations.tuples)
	}
	if !relationTupleMapExists(relations.tuples, "system", "responsibility-coordinator", "automation", "mission", mission.MissionID) {
		t.Fatalf("configured coordinator was not projected as Mission automation: %+v", relations.tuples)
	}
	for _, tuple := range relations.tuples {
		if tuple.Subject.Kind == "system" && tuple.Relation == "owner" {
			t.Fatalf("system creator became owner: %+v", tuple)
		}
	}
	if !relationTupleMapExists(relations.tuples, "mission", mission.MissionID, "mission", "discussion", "discussion-"+mission.MissionID) {
		t.Fatalf("system Mission discussion was not linked: %+v", relations.tuples)
	}
}

func TestSchedulerMissionCreationUsesSystemProvidedIdentity(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	service := NewLifecycleService(env.UnitOfWork, env.DBRouter, NewDBProjectionRepository(env.DBRouter), LifecycleOptions{
		Clock: env.Clock, IDGenerator: env.IDGenerator,
		AuthorizationService: authorization.NewService(sagaAuthorizationAdapter{}), AuthorizationRelations: authorization.NewRelationProjector(&sagaRelationSink{}),
	})
	viewer := ViewerRef{ViewerID: "scheduler-agent-task", ViewerKind: ViewerKindSystem, TenantID: "tenant-a", AuthorizationSubjectID: "scheduler-agent-task"}
	mission, err := service.CreateMission(ctx, viewer, CreateMissionCommand{
		MissionID: "scheduled-mission-stable", IdempotencyKey: "scheduled-task-1", Title: "Scheduled", FinalGoal: "Run scheduled work",
		OriginKind: "scheduler", OriginID: "scheduled-task-1", InitialAgentProfileID: "agent-1",
	})
	if err != nil {
		t.Fatalf("CreateMission: %v", err)
	}
	if mission.MissionID != "scheduled-mission-stable" {
		t.Fatalf("Mission ID = %q, want system-provided stable ID", mission.MissionID)
	}
}

func TestUserMissionCreationCannotChooseIdentity(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	service := NewLifecycleService(env.UnitOfWork, env.DBRouter, NewDBProjectionRepository(env.DBRouter), LifecycleOptions{
		Clock: env.Clock, IDGenerator: env.IDGenerator,
		AuthorizationService: authorization.NewService(sagaAuthorizationAdapter{}), AuthorizationRelations: authorization.NewRelationProjector(&sagaRelationSink{}),
	})
	_, err := service.CreateMission(ctx, ViewerRef{ViewerID: "planner", ViewerKind: ViewerKindUser, TenantID: "tenant-a"}, CreateMissionCommand{
		MissionID: "forged-mission", IdempotencyKey: "user-chosen-mission", Title: "User Mission", FinalGoal: "Must not forge identity",
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("CreateMission error = %v, want ErrInvalidRequest", err)
	}
}

func TestHumanMissionCreationGrantsConfiguredSystemAutomation(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	relations := &sagaRelationSink{}
	service := NewLifecycleService(env.UnitOfWork, env.DBRouter, NewDBProjectionRepository(env.DBRouter), LifecycleOptions{
		Clock: env.Clock, IDGenerator: env.IDGenerator,
		AuthorizationService: authorization.NewService(sagaAuthorizationAdapter{}), AuthorizationRelations: authorization.NewRelationProjector(relations),
		SystemAutomation: []authorization.SubjectRef{{Kind: "system", ID: "responsibility-coordinator"}},
	})
	mission, err := service.CreateMission(ctx, ViewerRef{ViewerID: "planner", ViewerKind: ViewerKindUser, TenantID: "tenant-a"}, CreateMissionCommand{
		IdempotencyKey: "human-mission-system-automation", Title: "Human mission", FinalGoal: "Coordinate work",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !relationTupleMapExists(relations.tuples, "user", "planner", "owner", "mission", mission.MissionID) ||
		!relationTupleMapExists(relations.tuples, "system", "responsibility-coordinator", "automation", "mission", mission.MissionID) {
		t.Fatalf("human Mission relations = %+v", relations.tuples)
	}
}

func relationTupleMapExists(tuples map[string]authorization.RelationTuple, subjectKind, subjectID, relation, objectType, objectID string) bool {
	for _, tuple := range tuples {
		if tuple.Subject.Kind == subjectKind && tuple.Subject.ID == subjectID && tuple.Relation == relation && tuple.Object.Type == objectType && tuple.Object.ID == objectID {
			return true
		}
	}
	return false
}

func assertSagaMissionOriginTuple(t *testing.T, tuples map[string]authorization.RelationTuple, missionID, relation, subjectKind, subjectID string) {
	t.Helper()
	for _, tuple := range tuples {
		if tuple.Object.Type == "mission" && tuple.Object.ID == missionID && tuple.Relation == relation && tuple.Subject.Kind == subjectKind && tuple.Subject.ID == subjectID {
			return
		}
	}
	t.Fatalf("missing %s relation from %s:%s to mission %s: %+v", relation, subjectKind, subjectID, missionID, tuples)
}
