package todo

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/rebac"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/idgen"
	"orrery_v2/internal/platform/testkit"
)

func TestSystemSuperadminRetainsTodoAccessWithoutWorkforceProfile(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	access := authorization.NewService(authorization.NewReBACAdapter(rebac.NewMemoryStore(), nil))
	service := NewService(ServiceOptions{
		Clock:       clock.NewFake(todoTestTime),
		IDGenerator: idgen.NewDeterministic("system-task"),
		Repository:  repo,
	})
	service.SetAuthorization(access, authorization.NewRelationProjector(access))
	viewer := ViewerRef{
		TenantID:               "system",
		ViewerID:               agentcore.SystemSuperadminPrincipalID,
		ViewerKind:             ViewerKindSystem,
		AuthorizationSubjectID: agentcore.SystemSuperadminPrincipalID,
		PermissionScope:        []string{"todo.read", "todo.write"},
	}

	item, err := service.CreateTodo(ctx, viewer, CreateTodoRequest{Title: "Break-glass task"})
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	if item.CreatedByKind != ViewerKindSystem {
		t.Fatalf("CreatedByKind = %q, want system", item.CreatedByKind)
	}
	if _, err := service.GetTodo(ctx, viewer, item.TodoID); err != nil {
		t.Fatalf("GetTodo: %v", err)
	}
	page, err := service.ListTodos(ctx, viewer, ListTodosQuery{IncludeCompleted: true})
	if err != nil || page.Total != 1 || len(page.Todos) != 1 || page.Todos[0].TodoID != item.TodoID {
		t.Fatalf("ListTodos = %+v, err=%v", page, err)
	}
	if _, err := service.CreateIssueComment(ctx, viewer, CreateIssueCommentRequest{IssueKind: IssueKindTodo, IssueID: item.TodoID, Body: "Authorized follow-up"}); err != nil {
		t.Fatalf("CreateIssueComment: %v", err)
	}
}

func TestOtherSystemViewersDoNotBypassTodoAuthorization(t *testing.T) {
	viewer := ViewerRef{ViewerID: "scheduler-agent-task", ViewerKind: ViewerKindSystem, AuthorizationSubjectID: "scheduler-agent-task"}
	if isBreakglassTodoViewer(viewer) {
		t.Fatal("scheduler system viewer received break-glass access")
	}
}

func TestTodoAuthorizationProtectsReadsMutationsCommentsListsApprovalsAndSummary(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	access := authorization.NewService(authorization.NewReBACAdapter(rebac.NewMemoryStore(), nil))
	service := NewService(ServiceOptions{
		Clock:       clock.NewFake(todoTestTime),
		IDGenerator: idgen.NewDeterministic("task"),
		Repository:  repo,
	})
	service.SetAuthorization(access, authorization.NewRelationProjector(access))

	owner := authorizedTodoViewer("tenant-a", "owner-login", "human-owner", "todo.read", "todo.write")
	assignee := authorizedTodoViewer("tenant-a", "assignee-login", "human-assignee", "todo.read", "todo.write")
	reviewer := authorizedTodoViewer("tenant-a", "reviewer-login", "human-reviewer", "todo.read", "todo.write")
	outsider := authorizedTodoViewer("tenant-a", "outsider-login", "human-outsider", "todo.read", "todo.write")
	otherTenant := authorizedTodoViewer("tenant-b", "owner-login", "human-owner", "todo.read", "todo.write")

	item, err := service.CreateTodo(ctx, owner, CreateTodoRequest{
		Title:        "Authorization boundary",
		AssigneeID:   assignee.AuthorizationSubjectID,
		AssigneeKind: AssigneeKindHuman,
		ReviewerID:   reviewer.AuthorizationSubjectID,
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, viewer := range map[string]ViewerRef{"owner": owner, "assignee": assignee, "reviewer": reviewer} {
		t.Run(name+" can read", func(t *testing.T) {
			if _, err := service.GetTodo(ctx, viewer, item.TodoID); err != nil {
				t.Fatalf("GetTodo: %v", err)
			}
		})
	}
	for name, viewer := range map[string]ViewerRef{"same tenant outsider": outsider, "other tenant": otherTenant} {
		t.Run(name+" cannot guess id", func(t *testing.T) {
			if _, err := service.GetTodo(ctx, viewer, item.TodoID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("GetTodo error = %v, want ErrNotFound", err)
			}
			changed := "Unauthorized change"
			if _, err := service.UpdateTodo(ctx, viewer, UpdateTodoRequest{TodoID: item.TodoID, Title: &changed}); !errors.Is(err, ErrNotFound) {
				t.Fatalf("UpdateTodo error = %v, want ErrNotFound", err)
			}
			if _, err := service.CreateIssueComment(ctx, viewer, CreateIssueCommentRequest{IssueKind: IssueKindTodo, IssueID: item.TodoID, Body: "hidden"}); !errors.Is(err, ErrNotFound) {
				t.Fatalf("CreateIssueComment error = %v, want ErrNotFound", err)
			}
			if _, err := service.ListIssueComments(ctx, viewer, ListIssueCommentsQuery{IssueKind: IssueKindTodo, IssueID: item.TodoID}); !errors.Is(err, ErrNotFound) {
				t.Fatalf("ListIssueComments error = %v, want ErrNotFound", err)
			}
			page, err := service.ListTodos(ctx, viewer, ListTodosQuery{IncludeCompleted: true})
			if err != nil || page.Total != 0 || len(page.Todos) != 0 {
				t.Fatalf("ListTodos = %+v, err=%v", page, err)
			}
			summary, err := service.GetWorkHubSummary(ctx, viewer)
			if err != nil {
				t.Fatalf("GetWorkHubSummary: %v", err)
			}
			if summary.MyOpenTasks != 0 || summary.TasksInbox != 0 || summary.UnassignedTeamTasks != 0 {
				t.Fatalf("summary leaked todo data: %+v", summary)
			}
		})
	}

	if err := repo.SaveTodoAutomation(ctx, TodoAutomationState{
		TenantID: item.TenantID, ProposalID: "proposal-1", ApprovalID: "approval-1", TodoID: item.TodoID,
		TodoVersion: item.Version, Status: TodoAutomationStatusPendingApproval, RequestedBy: todoAutomationActorID,
		ApproverID: outsider.ViewerID, CreatedAt: todoTestTime, UpdatedAt: todoTestTime,
	}); err != nil {
		t.Fatal(err)
	}
	approvals, err := service.ListTodoAutomationApprovals(ctx, outsider, ListTodoAutomationApprovalsQuery{})
	if err != nil || len(approvals) != 0 {
		t.Fatalf("outsider approvals = %+v, err=%v", approvals, err)
	}

	newAssignee := authorizedTodoViewer("tenant-a", "new-assignee-login", "human-new-assignee", "todo.read", "todo.write")
	newAssigneeID := newAssignee.AuthorizationSubjectID
	updated, err := service.UpdateTodo(ctx, owner, UpdateTodoRequest{TodoID: item.TodoID, AssigneeID: &newAssigneeID, Version: &item.Version})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetTodo(ctx, assignee, item.TodoID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("former assignee GetTodo error = %v, want ErrNotFound", err)
	}
	if _, err := service.GetTodo(ctx, newAssignee, item.TodoID); err != nil {
		t.Fatalf("new assignee GetTodo: %v", err)
	}
	if err := service.DeleteTodo(ctx, owner, DeleteTodoRequest{TodoID: updated.TodoID, Version: &updated.Version}); err != nil {
		t.Fatal(err)
	}
	objects, err := access.ListObjects(ctx, todoPrincipal(newAssignee), "reader", "todo", "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != 0 {
		t.Fatalf("deleted todo relations remain: %+v", objects)
	}
}

func TestTodoAuthorizationFailsClosedWhenRelationBackendIsUnavailable(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	access := authorization.NewService(authorization.NewReBACAdapter(nil, nil))
	service := NewService(ServiceOptions{
		Clock:       clock.NewFake(todoTestTime),
		IDGenerator: idgen.NewDeterministic("task"),
		Repository:  repo,
	})
	service.SetAuthorization(access, authorization.NewRelationProjector(access))
	viewer := authorizedTodoViewer("tenant-a", "owner", "human-owner", "todo.read", "todo.write")

	if _, err := service.CreateTodo(ctx, viewer, CreateTodoRequest{Title: "Must not persist"}); !errors.Is(err, authorization.ErrServiceDependency) {
		t.Fatalf("CreateTodo error = %v, want authorization dependency error", err)
	}
	page, err := repo.QueryTodos(ctx, viewer, ListTodosQuery{IncludeCompleted: true})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 {
		t.Fatalf("failed create persisted todo: %+v", page)
	}
	if _, err := service.ListTodos(ctx, viewer, ListTodosQuery{}); !errors.Is(err, authorization.ErrServiceDependency) {
		t.Fatalf("ListTodos error = %v, want authorization dependency error", err)
	}
}

func TestAuthorizedSummaryMatchesLegacyAndCanonicalCreatorIdentifiers(t *testing.T) {
	viewer := authorizedTodoViewer("tenant-a", "owner-login", "human-owner", "todo.read")
	for name, item := range map[string]TodoItem{
		"legacy":    {CreatedBy: viewer.ViewerID, Status: TodoStatusInbox},
		"canonical": {CreatedBy: "old-login", CreatedBySubjectID: viewer.AuthorizationSubjectID, Status: TodoStatusInbox},
	} {
		t.Run(name, func(t *testing.T) {
			summary := WorkHubSummary{}
			addTodoToSummary(&summary, viewer, item, todoTestTime, todoTestTime.Add(-time.Hour), todoTestTime.Add(24*time.Hour))
			if summary.MyOpenTasks != 1 {
				t.Fatalf("summary = %+v", summary)
			}
		})
	}
}

func authorizedTodoViewer(tenantID, viewerID, subjectID string, scopes ...string) ViewerRef {
	return ViewerRef{
		TenantID:               tenantID,
		ViewerID:               viewerID,
		ViewerKind:             ViewerKindUser,
		AuthorizationSubjectID: subjectID,
		DisplayName:            viewerID,
		PermissionScope:        append([]string(nil), scopes...),
	}
}
