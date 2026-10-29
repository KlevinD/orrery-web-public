package responsibility

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

type capturingResponsibilityContextProvider struct {
	request ContextRequest
	value   agentcore.ResponsibilityContext
}

func (p *capturingResponsibilityContextProvider) ResolveResponsibilityContext(_ context.Context, request ContextRequest) (agentcore.ResponsibilityContext, error) {
	p.request = request
	return p.value, nil
}

func TestCoordinatorResolvesResponsibilityContextBeforeDispatch(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	tasks := missiontask.NewPlatformRepository()
	task := testTaskRecord("task-context", "mission-1", "agent-1", missiontask.TaskStatusAssigned)
	task.Contract.ExecutionPermissionScopes = []string{"repo.read"}
	task.Contract.RequireExplicitToolScopes = true
	saveTaskRecord(t, env, tasks, task)
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	policy := DefaultPolicy("tenant-1", "agent-1", WorkKindMissionTask)
	policy.ToolScopes = []string{"repo.read"}
	policy, err := repository.SavePolicy(context.Background(), policy, "manager-1", "policy-context-1", policy.Version, env.Clock.Now())
	if err != nil {
		t.Fatalf("save policy: %v", err)
	}
	var assignment AssignmentRecord
	err = env.UnitOfWork.Do(context.Background(), func(ctx context.Context, tx uow.Tx) error {
		created, _, err := repository.AssignMissionTaskTx(ctx, tx, MissionTaskAssignmentInput{
			TenantID: "tenant-1", MissionID: "mission-1", TaskID: "task-context", ActorMemberID: "member-1", AgentProfileID: "agent-1",
			Priority: 50, ActivationCause: ActivationCauseAssigned,
		}, env.Clock.Now())
		assignment = created
		return err
	})
	if err != nil {
		t.Fatalf("assign responsibility: %v", err)
	}
	if assignment.ActorMemberID != "member-1" {
		t.Fatalf("assignment actor member = %q", assignment.ActorMemberID)
	}
	service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
	provider := &capturingResponsibilityContextProvider{value: agentcore.ResponsibilityContext{
		ContextID: "people-snapshot-1", ActorMemberID: "member-1", AgentProfileID: "agent-1", Source: "people.responsibility_context",
		AllowedTools: []string{"repo.read"}, AllowedDataScopes: []string{"mission"},
	}}
	dispatcher := &capturingResponsibilityDispatcher{}
	coordinator := NewCoordinator(CoordinatorOptions{
		Repository: repository, Service: service,
		Profiles: staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"agent-1": {ID: "agent-1", Enabled: true, RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindPi}},
		}},
		ContextProvider: provider, Dispatcher: dispatcher, Clock: env.Clock, IDGenerator: env.IDGenerator,
	})
	stats, err := coordinator.Reconcile(context.Background(), 10)
	if err != nil || stats.Dispatched != 1 || len(dispatcher.requests) != 1 {
		t.Fatalf("reconcile stats=%+v requests=%+v err=%v", stats, dispatcher.requests, err)
	}
	if provider.request.ActorMemberID != "member-1" || provider.request.AgentProfileID != "agent-1" || provider.request.ResponsibilityID != assignment.ID || provider.request.AssignmentVersion != assignment.Version {
		t.Fatalf("context request = %+v", provider.request)
	}
	dispatched := dispatcher.requests[0].Context
	if dispatched.ContextID != "people-snapshot-1" || dispatched.ResponsibilityID != assignment.ID || dispatched.ActorMemberID != "member-1" || dispatched.MissionID != "mission-1" || dispatched.TaskID != "task-context" {
		t.Fatalf("dispatched context = %+v", dispatched)
	}
	if dispatched.PolicyID != policy.ID || dispatched.PolicyVersion != policy.Version || len(dispatched.PolicyToolScopes) != 1 || dispatched.PolicyToolScopes[0] != "repo.read" || len(dispatched.TaskPermissionScopes) != 1 || dispatched.TaskPermissionScopes[0] != "repo.read" || !dispatched.RequireExplicitToolScopes {
		t.Fatalf("dispatched policy authority = %+v", dispatched)
	}
	policySourceRef := "orrery:responsibility-policy:" + policy.ID + ":v2"
	if !containsString(dispatched.SourceRefs, policySourceRef) {
		t.Fatalf("dispatched source refs = %+v, want %q", dispatched.SourceRefs, policySourceRef)
	}
}

func TestCoordinatorBlocksExplicitScopeTaskOnUnfencedRuntime(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	tasks := missiontask.NewPlatformRepository()
	task := testTaskRecord("task-unfenced-runtime", "mission-1", "agent-1", missiontask.TaskStatusAssigned)
	task.Contract.ExecutionPermissionScopes = []string{"repo.read"}
	task.Contract.RequireExplicitToolScopes = true
	saveTaskRecord(t, env, tasks, task)
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	policy := DefaultPolicy("tenant-1", "agent-1", WorkKindMissionTask)
	policy.ToolScopes = []string{"repo.read"}
	if _, err := repository.SavePolicy(context.Background(), policy, "manager-1", "policy-unfenced-runtime", policy.Version, env.Clock.Now()); err != nil {
		t.Fatalf("save policy: %v", err)
	}
	var assignment AssignmentRecord
	if err := env.UnitOfWork.Do(context.Background(), func(ctx context.Context, tx uow.Tx) error {
		created, _, err := repository.AssignMissionTaskTx(ctx, tx, MissionTaskAssignmentInput{
			TenantID: "tenant-1", MissionID: "mission-1", TaskID: string(task.Contract.ID), ActorMemberID: "member-1", AgentProfileID: "agent-1",
			Priority: 50, ActivationCause: ActivationCauseAssigned,
		}, env.Clock.Now())
		assignment = created
		return err
	}); err != nil {
		t.Fatalf("assign responsibility: %v", err)
	}
	dispatcher := &capturingResponsibilityDispatcher{}
	coordinator := NewCoordinator(CoordinatorOptions{
		Repository: repository, Service: NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock}),
		Profiles: staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"agent-1": {ID: "agent-1", Enabled: true, RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}},
		}},
		Dispatcher: dispatcher, Clock: env.Clock, IDGenerator: env.IDGenerator,
	})
	stats, err := coordinator.Reconcile(context.Background(), 10)
	if err != nil || stats.PolicyBlocked != 1 || stats.Dispatched != 0 || len(dispatcher.requests) != 0 {
		t.Fatalf("reconcile stats=%+v requests=%+v err=%v", stats, dispatcher.requests, err)
	}
	blockers, err := repository.ListBlockers(context.Background(), assignment.TenantID, assignment.ID, true)
	if err != nil || len(blockers) != 1 || blockers[0].Kind != BlockerKindPermission || blockers[0].DedupeKey != "dispatch-runtime-tool-scope-fence" {
		t.Fatalf("runtime scope fence blockers=%+v err=%v", blockers, err)
	}
}
