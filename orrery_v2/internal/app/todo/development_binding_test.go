package todo

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/idgen"
	"orrery_v2/internal/platform/testkit"
)

func TestTodoDevelopmentProjectBindingPreservesAuthorizationPrincipal(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	guard := &todoDevelopmentGuardStub{}
	service := NewService(ServiceOptions{
		Clock: clock.NewFake(todoTestTime), IDGenerator: idgen.NewDeterministic("task"),
		Repository: NewRepository(env.DBRouter), DevelopmentExecution: guard,
	})
	viewer := ViewerRef{
		TenantID: "tenant-a", ViewerID: "owner-login", ViewerKind: ViewerKindUser,
		AuthorizationSubjectID: "subject-owner", PermissionScope: []string{"todo.write", "development.write"},
	}
	item, err := service.CreateTodo(context.Background(), viewer, CreateTodoRequest{
		Title: "Development work", WorkKind: TodoWorkKindDevelopment, DevelopmentProjectID: "project-1",
	})
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	if item.DevelopmentProjectID != "project-1" || len(guard.bindingRequests) != 1 {
		t.Fatalf("item=%+v requests=%+v", item, guard.bindingRequests)
	}
	request := guard.bindingRequests[0]
	if request.TenantID != "tenant-a" || request.DevelopmentProjectID != "project-1" || request.RequesterID != "owner-login" || request.RequesterSubjectID != "subject-owner" || request.RequesterKind != ViewerKindUser {
		t.Fatalf("binding authorization = %+v", request)
	}
}

func TestTodoDevelopmentProjectBindingDenialDoesNotPersistCreateOrUpdate(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	guard := &todoDevelopmentGuardStub{bindingErr: ErrForbidden}
	service := NewService(ServiceOptions{
		Clock: clock.NewFake(todoTestTime), IDGenerator: idgen.NewDeterministic("task"),
		Repository: repo, DevelopmentExecution: guard,
	})
	viewer := testViewer("tenant-a", "owner")
	if _, err := service.CreateTodo(ctx, viewer, CreateTodoRequest{
		Title: "Denied development work", WorkKind: TodoWorkKindDevelopment, DevelopmentProjectID: "project-denied",
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("CreateTodo error = %v, want ErrForbidden", err)
	}
	page, err := repo.QueryTodos(ctx, viewer, ListTodosQuery{IncludeCompleted: true})
	if err != nil || page.Total != 0 {
		t.Fatalf("denied create persisted: page=%+v err=%v", page, err)
	}

	guard.bindingErr = nil
	general := mustCreateTask(t, service, ctx, viewer, CreateTodoRequest{Title: "General work"})
	guard.bindingErr = ErrForbidden
	workKind := TodoWorkKindDevelopment
	projectID := "project-denied"
	if _, err := service.UpdateTodo(ctx, viewer, UpdateTodoRequest{
		TodoID: general.TodoID, WorkKind: &workKind, DevelopmentProjectID: &projectID, Version: &general.Version,
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("UpdateTodo error = %v, want ErrForbidden", err)
	}
	persisted, err := repo.GetTodoForTenant(ctx, viewer.TenantID, general.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.WorkKind != TodoWorkKindGeneral || persisted.DevelopmentProjectID != "" || persisted.Version != general.Version {
		t.Fatalf("denied update mutated Todo: %+v", persisted)
	}
}

type todoDevelopmentGuardStub struct {
	bindingRequests   []TodoDevelopmentExecutionAuthorization
	approvalRequests  []TodoDevelopmentExecutionAuthorization
	executionRequests []TodoDevelopmentExecutionAuthorization
	planRequests      []TodoDevelopmentExecutionPlanContextRequest
	bindingErr        error
	approvalErr       error
	executionErr      error
	planErr           error
	planContext       TodoDevelopmentExecutionPlanContext
}

func (s *todoDevelopmentGuardStub) EnsureTodoBindingAllowed(_ context.Context, request TodoDevelopmentExecutionAuthorization) error {
	s.bindingRequests = append(s.bindingRequests, request)
	return s.bindingErr
}

func (s *todoDevelopmentGuardStub) EnsureTodoExecutionAllowed(_ context.Context, request TodoDevelopmentExecutionAuthorization) error {
	s.executionRequests = append(s.executionRequests, request)
	return s.executionErr
}

func (s *todoDevelopmentGuardStub) EnsureTodoPlanApprovalAllowed(_ context.Context, request TodoDevelopmentExecutionAuthorization) error {
	s.approvalRequests = append(s.approvalRequests, request)
	return s.approvalErr
}

func (s *todoDevelopmentGuardStub) ResolveTodoExecutionPlanContext(_ context.Context, request TodoDevelopmentExecutionPlanContextRequest) (TodoDevelopmentExecutionPlanContext, error) {
	s.planRequests = append(s.planRequests, request)
	if s.planErr != nil {
		return TodoDevelopmentExecutionPlanContext{}, s.planErr
	}
	if s.planContext.DevelopmentProjectID != "" {
		return s.planContext, nil
	}
	return TodoDevelopmentExecutionPlanContext{
		DevelopmentProjectID:   request.DevelopmentProjectID,
		ResourceSnapshotDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Repositories: []TodoDevelopmentPlanRepository{{
			BindingID: "binding-primary", DisplayName: "Primary", Role: "primary", AccessMode: "read_write",
			DefaultBranch: "main", SourceSubdirectory: "orrery_v2", TestCommands: []string{"go test ./..."},
			BaseCommit: "0123456789abcdef0123456789abcdef01234567",
		}},
		AllowedFileScopes: []string{"orrery_v2"}, AllowedNetworkMode: "none",
	}, nil
}
