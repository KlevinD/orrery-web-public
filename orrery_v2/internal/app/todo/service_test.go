package todo

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/idgen"
	"orrery_v2/internal/platform/testkit"
)

var todoTestTime = time.Date(2026, 7, 11, 9, 30, 0, 0, time.UTC)

func TestDeleteTodoPreservesDiscussionAndExecutionHistory(t *testing.T) {
	for _, test := range []struct {
		name string
		seed func(*testing.T, *Repository, TodoItem)
	}{
		{
			name: "discussion binding",
			seed: func(t *testing.T, repo *Repository, item TodoItem) {
				t.Helper()
				_, err := repo.SaveTodoDiscussionBinding(context.Background(), TodoDiscussionBinding{
					TenantID: item.TenantID, TodoID: item.TodoID, ConversationID: "conversation-history", ManagedSource: "todo:" + item.TenantID + "/" + item.TodoID,
					CreatedAt: todoTestTime, UpdatedAt: todoTestTime,
				})
				if err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "execution attempt",
			seed: func(t *testing.T, repo *Repository, item TodoItem) {
				t.Helper()
				if err := repo.SaveTodoExecutionAttempt(context.Background(), TodoExecutionAttempt{
					TenantID: item.TenantID, TodoID: item.TodoID, ExecutionAttempt: 1, ProposalID: "proposal-history", TodoVersion: item.Version,
					PlanDigest: "digest-history", Status: TodoMissionBindingStatusFailed, FailureReason: "historical failure", StartedAt: todoTestTime, UpdatedAt: todoTestTime,
				}); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			repo := NewRepository(env.DBRouter)
			service := newServiceForTest(repo)
			viewer := testViewer("tenant-a", "owner")
			item := mustCreateTask(t, service, context.Background(), viewer, CreateTodoRequest{Title: "Retain history"})
			test.seed(t, repo, item)
			if err := service.DeleteTodo(context.Background(), viewer, DeleteTodoRequest{TodoID: item.TodoID}); !errors.Is(err, ErrConflict) {
				t.Fatalf("DeleteTodo error = %v, want ErrConflict", err)
			}
			stored, err := repo.GetTodoForTenant(context.Background(), item.TenantID, item.TodoID)
			if err != nil {
				t.Fatalf("Todo was deleted: %v", err)
			}
			if stored.DiscussionBinding == nil && (stored.Execution == nil || len(stored.Execution.Attempts) == 0) {
				t.Fatalf("history was deleted: %+v", stored)
			}
		})
	}
}

func TestTaskServiceManagesExecutionMetadataAndLifecycle(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTest(repo)
	ctx := context.Background()
	viewer := testViewer("tenant-a", "operator")
	dueAt := todoTestTime.Add(48 * time.Hour)

	created, err := service.CreateTodo(ctx, viewer, CreateTodoRequest{
		Title:              "  Prepare quarterly review  ",
		Description:        "  Summarize goals and outcomes.  ",
		Status:             TodoStatusPlanned,
		Priority:           TodoPriorityHigh,
		OrganizationID:     "org-1",
		DepartmentID:       "dept-1",
		ProjectID:          "project-1",
		AssigneeID:         "agent-research",
		AssigneeName:       "Research Agent",
		AssigneeKind:       AssigneeKindAgent,
		ReviewerID:         "operator",
		ReviewerName:       "Operator",
		DueAt:              dueAt,
		AcceptanceCriteria: "A reviewable decision memo exists.",
		Tags:               []string{" planning ", "Review", "planning"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != TodoStatusPlanned || created.Priority != TodoPriorityHigh || created.Version != 1 {
		t.Fatalf("created task = %+v", created)
	}
	if created.Title != "Prepare quarterly review" || created.Description != "Summarize goals and outcomes." {
		t.Fatalf("normalized task = %+v", created)
	}
	if created.AssigneeKind != AssigneeKindAgent || created.ReviewerID != "operator" || !created.DueAt.Equal(dueAt) {
		t.Fatalf("execution metadata = %+v", created)
	}
	if !sameStrings(created.Tags, []string{"planning", "Review"}) || created.IssueNumber == "" {
		t.Fatalf("task identity/tags = %+v", created)
	}

	priority := TodoPriorityUrgent
	acceptance := "Leadership approved the review."
	updated, err := service.UpdateTodo(ctx, viewer, UpdateTodoRequest{
		TodoID:             created.TodoID,
		Priority:           &priority,
		AcceptanceCriteria: &acceptance,
		Version:            int64Pointer(created.Version),
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Priority != TodoPriorityUrgent || updated.AcceptanceCriteria != acceptance || updated.Version != 2 {
		t.Fatalf("updated task = %+v", updated)
	}
	_, err = service.UpdateTodo(ctx, viewer, UpdateTodoRequest{TodoID: created.TodoID, Version: int64Pointer(1)})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update error = %v, want ErrConflict", err)
	}

	_, err = service.MoveTodo(ctx, viewer, MoveTodoRequest{TodoID: created.TodoID, Status: TodoStatusBlocked, Version: int64Pointer(updated.Version)})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("block without reason error = %v, want ErrInvalidRequest", err)
	}
	blocked, err := service.MoveTodo(ctx, viewer, MoveTodoRequest{
		TodoID:        created.TodoID,
		Status:        TodoStatusBlocked,
		BlockedReason: "Waiting for finance data",
		Version:       int64Pointer(updated.Version),
	})
	if err != nil {
		t.Fatal(err)
	}
	if blocked.BlockedReason == "" || blocked.Status != TodoStatusBlocked {
		t.Fatalf("blocked task = %+v", blocked)
	}
	renamed := "Prepare blocked quarterly review"
	blocked, err = service.UpdateTodo(ctx, viewer, UpdateTodoRequest{TodoID: blocked.TodoID, Title: &renamed, Version: int64Pointer(blocked.Version)})
	if err != nil {
		t.Fatal(err)
	}
	if blocked.BlockedReason != "Waiting for finance data" || blocked.Status != TodoStatusBlocked {
		t.Fatalf("generic update changed blocked lifecycle fields: %+v", blocked)
	}
	inProgress, err := service.MoveTodo(ctx, viewer, MoveTodoRequest{TodoID: created.TodoID, Status: TodoStatusInProgress, Version: int64Pointer(blocked.Version)})
	if err != nil {
		t.Fatal(err)
	}
	if inProgress.BlockedReason != "" {
		t.Fatalf("resolved task retained blocker: %+v", inProgress)
	}
	inReview, err := service.MoveTodo(ctx, viewer, MoveTodoRequest{TodoID: created.TodoID, Status: TodoStatusReview, Version: int64Pointer(inProgress.Version)})
	if err != nil {
		t.Fatal(err)
	}
	done, err := service.CompleteTodo(ctx, viewer, CompleteTodoRequest{TodoID: created.TodoID, Version: int64Pointer(inReview.Version)})
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != TodoStatusDone || done.CompletedAt.IsZero() {
		t.Fatalf("completed task = %+v", done)
	}
	reopened, err := service.ReopenTodo(ctx, viewer, ReopenTodoRequest{TodoID: created.TodoID, Version: int64Pointer(done.Version)})
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Status != TodoStatusPlanned || !reopened.CompletedAt.IsZero() {
		t.Fatalf("reopened task = %+v", reopened)
	}
}

func TestTaskServiceRejectsLegacyAndInvalidInitialStatuses(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	service := newServiceForTest(NewRepository(env.DBRouter))
	viewer := testViewer("tenant-a", "operator")

	for _, status := range []TodoStatus{"backlog", "todo", TodoStatusBlocked, TodoStatusReview, TodoStatusDone, TodoStatusCanceled} {
		_, err := service.CreateTodo(context.Background(), viewer, CreateTodoRequest{Title: "Task", Status: status})
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("status %q error = %v, want ErrInvalidRequest", status, err)
		}
	}
}

func TestServiceEnforcesTenantIsolationAndOptimisticVersioning(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	service := newServiceForTest(NewRepository(env.DBRouter))
	ctx := context.Background()
	tenantA := testViewer("tenant-a", "owner-a")
	tenantB := testViewer("tenant-b", "owner-b")

	task, err := service.CreateTodo(ctx, tenantA, CreateTodoRequest{Title: "Tenant A task"})
	if err != nil {
		t.Fatal(err)
	}
	idea, err := service.CreateIdea(ctx, tenantA, CreateIdeaRequest{Title: "Tenant A idea"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetTodo(ctx, tenantB, task.TodoID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant task read error = %v, want ErrNotFound", err)
	}
	if _, err := service.GetIdea(ctx, tenantB, idea.IdeaID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant idea read error = %v, want ErrNotFound", err)
	}
	tasks, err := service.ListTodos(ctx, tenantB, ListTodosQuery{IncludeCompleted: true})
	if err != nil || tasks.Total != 0 {
		t.Fatalf("tenant B tasks = %+v, err = %v", tasks, err)
	}
	ideas, err := service.ListIdeas(ctx, tenantB, ListIdeasQuery{})
	if err != nil || ideas.Total != 0 {
		t.Fatalf("tenant B ideas = %+v, err = %v", ideas, err)
	}
}

func TestIdeaServiceEnforcesVisibilityLifecycleDecisionsAndVoting(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	service := newServiceForTest(NewRepository(env.DBRouter))
	ctx := context.Background()
	owner := testViewer("tenant-a", "owner")
	owner.OrganizationID = "org-1"
	owner.DepartmentID = "dept-1"
	peer := testViewer("tenant-a", "peer")
	peer.OrganizationID = "org-1"
	peer.DepartmentID = "dept-2"
	sameDepartment := testViewer("tenant-a", "department-peer")
	sameDepartment.OrganizationID = "org-1"
	sameDepartment.DepartmentID = "dept-1"
	otherOrganization := testViewer("tenant-a", "other-org-peer")
	otherOrganization.OrganizationID = "org-2"
	otherOrganization.DepartmentID = "dept-1"

	departmentIdea, err := service.CreateIdea(ctx, owner, CreateIdeaRequest{
		Title:      "Department operating model",
		Visibility: IdeaVisibilityDepartment,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetIdea(ctx, sameDepartment, departmentIdea.IdeaID); err != nil {
		t.Fatalf("same-department read error = %v", err)
	}
	sameDepartmentIdeas, err := service.ListIdeas(ctx, sameDepartment, ListIdeasQuery{DepartmentID: "dept-1"})
	if err != nil || sameDepartmentIdeas.Total != 1 || sameDepartmentIdeas.Ideas[0].IdeaID != departmentIdea.IdeaID {
		t.Fatalf("same-department list = %+v, err = %v", sameDepartmentIdeas, err)
	}
	if _, err := service.GetIdea(ctx, peer, departmentIdea.IdeaID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("other-department read error = %v, want ErrForbidden", err)
	}
	spoofedList, err := service.ListIdeas(ctx, peer, ListIdeasQuery{DepartmentID: "dept-1"})
	if err != nil || spoofedList.Total != 0 {
		t.Fatalf("other-department filtered list = %+v, err = %v", spoofedList, err)
	}

	privateIdea, err := service.CreateIdea(ctx, owner, CreateIdeaRequest{
		Title:      "Private hypothesis",
		Status:     IdeaStatusDraft,
		Visibility: IdeaVisibilityPrivate,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetIdea(ctx, peer, privateIdea.IdeaID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("private idea read error = %v, want ErrForbidden", err)
	}

	idea, err := service.CreateIdea(ctx, owner, CreateIdeaRequest{
		Title:      "Project risk radar",
		Content:    "Surface delivery risks earlier.",
		Visibility: IdeaVisibilityOrganization,
		Impact:     IdeaImpactHigh,
		Effort:     IdeaEffortMedium,
	})
	if err != nil {
		t.Fatal(err)
	}
	if idea.Status != IdeaStatusSubmitted || idea.OwnerID != owner.ViewerID || idea.SubmittedAt.IsZero() {
		t.Fatalf("submitted idea = %+v", idea)
	}
	if _, err := service.GetIdea(ctx, otherOrganization, idea.IdeaID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("other-organization read error = %v, want ErrForbidden", err)
	}
	otherOrganizationIdeas, err := service.ListIdeas(ctx, otherOrganization, ListIdeasQuery{})
	if err != nil || otherOrganizationIdeas.Total != 0 {
		t.Fatalf("other-organization list = %+v, err = %v", otherOrganizationIdeas, err)
	}

	voted, err := service.VoteIdea(ctx, peer, VoteIdeaRequest{IdeaID: idea.IdeaID})
	if err != nil {
		t.Fatal(err)
	}
	if voted.VoteCount != 1 || !voted.ViewerHasVoted {
		t.Fatalf("voted idea = %+v", voted)
	}
	unvoted, err := service.VoteIdea(ctx, peer, VoteIdeaRequest{IdeaID: idea.IdeaID})
	if err != nil {
		t.Fatal(err)
	}
	if unvoted.VoteCount != 0 || unvoted.ViewerHasVoted {
		t.Fatalf("unvoted idea = %+v", unvoted)
	}

	inReview, err := service.TransitionIdea(ctx, owner, TransitionIdeaRequest{IdeaID: idea.IdeaID, Status: IdeaStatusUnderReview, Version: int64Pointer(unvoted.Version)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.TransitionIdea(ctx, owner, TransitionIdeaRequest{IdeaID: idea.IdeaID, Status: IdeaStatusAccepted, Version: int64Pointer(inReview.Version)})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("decision without reason error = %v, want ErrInvalidRequest", err)
	}
	accepted, err := service.TransitionIdea(ctx, owner, TransitionIdeaRequest{
		IdeaID:         idea.IdeaID,
		Status:         IdeaStatusAccepted,
		DecisionReason: "High impact with a bounded pilot.",
		Version:        int64Pointer(inReview.Version),
	})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Status != IdeaStatusAccepted || accepted.DecisionReason == "" || accepted.ReviewerID != owner.ViewerID {
		t.Fatalf("accepted idea = %+v", accepted)
	}
	experiment, err := service.TransitionIdea(ctx, owner, TransitionIdeaRequest{
		IdeaID:  idea.IdeaID,
		Status:  IdeaStatusExperiment,
		Version: int64Pointer(accepted.Version),
	})
	if err != nil {
		t.Fatal(err)
	}
	if experiment.DecisionReason != accepted.DecisionReason {
		t.Fatalf("experiment transition discarded decision record: before=%q after=%q", accepted.DecisionReason, experiment.DecisionReason)
	}
}

func TestIdeaConversionIsAtomicPreservesIdeaAndComments(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTest(repo)
	ctx := context.Background()
	viewer := testViewer("tenant-a", "operator")

	idea, err := service.CreateIdea(ctx, viewer, CreateIdeaRequest{
		Title:   "Internal expert assistant",
		Content: "Answer questions with internal evidence.",
		Impact:  IdeaImpactHigh,
		Effort:  IdeaEffortSmall,
		Tags:    []string{"knowledge", "assistant"},
	})
	if err != nil {
		t.Fatal(err)
	}
	comment, err := service.CreateIssueComment(ctx, viewer, CreateIssueCommentRequest{IssueKind: IssueKindIdea, IssueID: idea.IdeaID, Body: "Pilot with support."})
	if err != nil {
		t.Fatal(err)
	}
	inReview, err := service.TransitionIdea(ctx, viewer, TransitionIdeaRequest{IdeaID: idea.IdeaID, Status: IdeaStatusUnderReview, Version: int64Pointer(idea.Version)})
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := service.TransitionIdea(ctx, viewer, TransitionIdeaRequest{
		IdeaID:         idea.IdeaID,
		Status:         IdeaStatusAccepted,
		DecisionReason: "The pilot has a clear owner and measurable outcome.",
		Version:        int64Pointer(inReview.Version),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ConvertIdeaToTodo(ctx, viewer, ConvertIdeaToTodoRequest{
		IdeaID:             idea.IdeaID,
		AssigneeID:         "agent-research",
		AssigneeName:       "Research Agent",
		AssigneeKind:       AssigneeKindAgent,
		AcceptanceCriteria: "The pilot answers ten verified questions.",
		Version:            int64Pointer(accepted.Version),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Todo.TodoID == result.Idea.IdeaID || result.Todo.SourceIdeaID != result.Idea.IdeaID {
		t.Fatalf("conversion identity = %+v", result)
	}
	if result.Todo.Status != TodoStatusPlanned || result.Todo.Priority != TodoPriorityUrgent {
		t.Fatalf("converted task = %+v", result.Todo)
	}
	if result.Idea.Status != IdeaStatusConverted || result.Idea.ConvertedTodoID != result.Todo.TodoID {
		t.Fatalf("converted idea = %+v", result.Idea)
	}
	loadedIdea, err := service.GetIdea(ctx, viewer, idea.IdeaID)
	if err != nil || loadedIdea.ConvertedTodoID != result.Todo.TodoID {
		t.Fatalf("preserved idea = %+v, err = %v", loadedIdea, err)
	}
	comments, err := service.ListIssueComments(ctx, viewer, ListIssueCommentsQuery{IssueKind: IssueKindIdea, IssueID: idea.IdeaID})
	if err != nil {
		t.Fatal(err)
	}
	if comments.Total != 1 || comments.Comments[0].CommentID != comment.CommentID {
		t.Fatalf("preserved idea comments = %+v", comments)
	}
	taskComments, err := service.ListIssueComments(ctx, viewer, ListIssueCommentsQuery{IssueKind: IssueKindTodo, IssueID: result.Todo.TodoID})
	if err != nil || taskComments.Total != 0 {
		t.Fatalf("new task comments = %+v, err = %v", taskComments, err)
	}
}

func TestIdeaConversionRequiresApprovedLifecycleAndIsTerminal(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	service := newServiceForTest(NewRepository(env.DBRouter))
	ctx := context.Background()
	viewer := testViewer("tenant-a", "operator")

	idea := mustCreateIdea(t, service, ctx, viewer, "Validate the workflow")
	if _, err := service.ConvertIdeaToTodo(ctx, viewer, ConvertIdeaToTodoRequest{
		IdeaID:         idea.IdeaID,
		DecisionReason: "Skip governance",
	}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("submitted conversion error = %v, want ErrInvalidTransition", err)
	}
	if _, err := service.TransitionIdea(ctx, viewer, TransitionIdeaRequest{
		IdeaID:         idea.IdeaID,
		Status:         IdeaStatusConverted,
		DecisionReason: "Skip conversion transaction",
	}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("direct converted transition error = %v, want ErrInvalidTransition", err)
	}

	experiment, err := service.TransitionIdea(ctx, viewer, TransitionIdeaRequest{
		IdeaID: idea.IdeaID,
		Status: IdeaStatusExperiment,
	})
	if err != nil {
		t.Fatal(err)
	}
	converted, err := service.ConvertIdeaToTodo(ctx, viewer, ConvertIdeaToTodoRequest{
		IdeaID:         idea.IdeaID,
		DecisionReason: "The experiment established a viable delivery path.",
		Version:        int64Pointer(experiment.Version),
	})
	if err != nil {
		t.Fatal(err)
	}
	if converted.Idea.Status != IdeaStatusConverted {
		t.Fatalf("converted idea = %+v", converted.Idea)
	}

	terminalCases := []struct {
		name string
		run  func() error
	}{
		{name: "convert again", run: func() error {
			_, err := service.ConvertIdeaToTodo(ctx, viewer, ConvertIdeaToTodoRequest{IdeaID: idea.IdeaID})
			return err
		}},
		{name: "archive", run: func() error {
			_, err := service.ArchiveIdea(ctx, viewer, ArchiveIdeaRequest{IdeaID: idea.IdeaID})
			return err
		}},
		{name: "restore", run: func() error {
			_, err := service.RestoreIdea(ctx, viewer, RestoreIdeaRequest{IdeaID: idea.IdeaID})
			return err
		}},
		{name: "transition", run: func() error {
			_, err := service.TransitionIdea(ctx, viewer, TransitionIdeaRequest{IdeaID: idea.IdeaID, Status: IdeaStatusSubmitted})
			return err
		}},
	}
	for _, tc := range terminalCases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("error = %v, want ErrInvalidTransition", err)
			}
		})
	}
}

func TestWorkHubSummaryAggregatesPersonalAndReviewQueues(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	service := newServiceForTest(NewRepository(env.DBRouter))
	ctx := context.Background()
	viewer := testViewer("tenant-a", "operator")
	other := testViewer("tenant-a", "manager")

	mustCreateTask(t, service, ctx, viewer, CreateTodoRequest{Title: "Overdue", Status: TodoStatusPlanned, AssigneeID: viewer.ViewerID, DueAt: todoTestTime.Add(-time.Hour)})
	mustCreateTask(t, service, ctx, viewer, CreateTodoRequest{Title: "Due today", Status: TodoStatusPlanned, AssigneeID: viewer.ViewerID, DueAt: todoTestTime.Add(2 * time.Hour)})
	blocked := mustCreateTask(t, service, ctx, viewer, CreateTodoRequest{Title: "Blocked", Status: TodoStatusPlanned, AssigneeID: other.ViewerID})
	if _, err := service.MoveTodo(ctx, other, MoveTodoRequest{TodoID: blocked.TodoID, Status: TodoStatusBlocked, BlockedReason: "Dependency"}); err != nil {
		t.Fatal(err)
	}
	review := mustCreateTask(t, service, ctx, viewer, CreateTodoRequest{Title: "Review", Status: TodoStatusInProgress, AssigneeID: other.ViewerID})
	if _, err := service.MoveTodo(ctx, other, MoveTodoRequest{TodoID: review.TodoID, Status: TodoStatusReview}); err != nil {
		t.Fatal(err)
	}
	mustCreateTask(t, service, ctx, viewer, CreateTodoRequest{Title: "Unassigned", Status: TodoStatusInbox})

	mustCreateIdea(t, service, ctx, viewer, "Awaiting review")
	underReview := mustCreateIdea(t, service, ctx, viewer, "Under review")
	if _, err := service.TransitionIdea(ctx, viewer, TransitionIdeaRequest{IdeaID: underReview.IdeaID, Status: IdeaStatusUnderReview}); err != nil {
		t.Fatal(err)
	}
	experiment := mustCreateIdea(t, service, ctx, viewer, "Experiment")
	if _, err := service.TransitionIdea(ctx, viewer, TransitionIdeaRequest{IdeaID: experiment.IdeaID, Status: IdeaStatusExperiment}); err != nil {
		t.Fatal(err)
	}
	convert := mustCreateIdea(t, service, ctx, viewer, "Convert")
	accepted, err := service.TransitionIdea(ctx, viewer, TransitionIdeaRequest{IdeaID: convert.IdeaID, Status: IdeaStatusAccepted, DecisionReason: "Approved"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConvertIdeaToTodo(ctx, viewer, ConvertIdeaToTodoRequest{IdeaID: convert.IdeaID, Version: int64Pointer(accepted.Version)}); err != nil {
		t.Fatal(err)
	}

	summary, err := service.GetWorkHubSummary(ctx, viewer)
	if err != nil {
		t.Fatal(err)
	}
	want := WorkHubSummary{
		MyOpenTasks:         4,
		DueToday:            2,
		Overdue:             1,
		Blocked:             1,
		AwaitingTaskReview:  1,
		TasksInbox:          1,
		TasksPlanned:        3,
		IdeasAwaitingReview: 2,
		IdeasSubmitted:      1,
		IdeasUnderReview:    1,
		IdeasInExperiment:   1,
		IdeasConverted:      1,
		UnassignedTeamTasks: 2,
	}
	if summary != want {
		t.Fatalf("summary = %+v, want %+v", summary, want)
	}
}

func TestWorkHubSummaryUsesViewerTimeZoneAndCreatorOwnedUnassignedTasks(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	now := time.Date(2026, 7, 11, 16, 30, 0, 0, time.UTC)
	service := NewService(ServiceOptions{
		Clock:           clock.NewFake(now),
		IDGenerator:     idgen.NewDeterministic("task"),
		IdeaIDGenerator: idgen.NewDeterministic("idea"),
		Repository:      NewRepository(env.DBRouter),
	})
	viewer := testViewer("tenant-a", "operator")
	viewer.TimeZone = "Asia/Shanghai"
	ctx := context.Background()

	mustCreateTask(t, service, ctx, viewer, CreateTodoRequest{Title: "Creator owned", Status: TodoStatusInbox})
	mustCreateTask(t, service, ctx, viewer, CreateTodoRequest{Title: "Previous local day", Status: TodoStatusPlanned, AssigneeID: viewer.ViewerID, DueAt: time.Date(2026, 7, 11, 15, 59, 0, 0, time.UTC)})
	mustCreateTask(t, service, ctx, viewer, CreateTodoRequest{Title: "Earlier today", Status: TodoStatusPlanned, AssigneeID: viewer.ViewerID, DueAt: time.Date(2026, 7, 11, 16, 10, 0, 0, time.UTC)})
	mustCreateTask(t, service, ctx, viewer, CreateTodoRequest{Title: "Later today", Status: TodoStatusPlanned, AssigneeID: viewer.ViewerID, DueAt: time.Date(2026, 7, 11, 17, 0, 0, 0, time.UTC)})

	summary, err := service.GetWorkHubSummary(ctx, viewer)
	if err != nil {
		t.Fatal(err)
	}
	if summary.MyOpenTasks != 4 || summary.DueToday != 2 || summary.Overdue != 2 {
		t.Fatalf("timezone summary = %+v", summary)
	}
	mine, err := service.ListTodos(ctx, viewer, ListTodosQuery{Mine: true})
	if err != nil {
		t.Fatal(err)
	}
	if mine.Total != summary.MyOpenTasks {
		t.Fatalf("mine page total = %d, summary my_open_tasks = %d", mine.Total, summary.MyOpenTasks)
	}
}

func TestListQueriesPageAndSearchBeyondTheFirstPage(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	service := newServiceForTest(NewRepository(env.DBRouter))
	ctx := context.Background()
	viewer := testViewer("tenant-a", "operator")

	for index := 0; index < 55; index++ {
		title := fmt.Sprintf("Task %02d", index)
		if index == 54 {
			title = "Needle after first page"
		}
		mustCreateTask(t, service, ctx, viewer, CreateTodoRequest{Title: title, Status: TodoStatusPlanned})
	}
	for index := 0; index < 25; index++ {
		title := fmt.Sprintf("Idea %02d", index)
		if index == 24 {
			title = "Hidden gem after first page"
		}
		mustCreateIdea(t, service, ctx, viewer, title)
	}

	firstTasks, err := service.ListTodos(ctx, viewer, ListTodosQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	secondTasks, err := service.ListTodos(ctx, viewer, ListTodosQuery{Limit: 20, Offset: 20})
	if err != nil {
		t.Fatal(err)
	}
	foundTask, err := service.ListTodos(ctx, viewer, ListTodosQuery{Q: "needle", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if firstTasks.Total != 55 || !firstTasks.HasMore || len(secondTasks.Todos) != 20 || foundTask.Total != 1 || foundTask.Todos[0].Title != "Needle after first page" {
		t.Fatalf("task pages: first=%+v second=%+v search=%+v", firstTasks, secondTasks, foundTask)
	}

	firstIdeas, err := service.ListIdeas(ctx, viewer, ListIdeasQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	secondIdeas, err := service.ListIdeas(ctx, viewer, ListIdeasQuery{Limit: 20, Offset: 20})
	if err != nil {
		t.Fatal(err)
	}
	foundIdea, err := service.ListIdeas(ctx, viewer, ListIdeasQuery{Q: "hidden gem", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if firstIdeas.Total != 25 || !firstIdeas.HasMore || len(secondIdeas.Ideas) != 5 || foundIdea.Total != 1 || foundIdea.Ideas[0].Title != "Hidden gem after first page" {
		t.Fatalf("idea pages: first=%+v second=%+v search=%+v", firstIdeas, secondIdeas, foundIdea)
	}
}

func TestCommentsAreTenantScopedAndVisibilityAware(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	service := newServiceForTest(NewRepository(env.DBRouter))
	ctx := context.Background()
	owner := testViewer("tenant-a", "owner")
	otherTenant := testViewer("tenant-b", "other")
	task := mustCreateTask(t, service, ctx, owner, CreateTodoRequest{Title: "Discuss me"})

	comment, err := service.CreateIssueComment(ctx, owner, CreateIssueCommentRequest{IssueKind: IssueKindTodo, IssueID: task.TodoID, Body: "  First reply  "})
	if err != nil {
		t.Fatal(err)
	}
	if comment.Body != "First reply" || comment.AuthorID != owner.ViewerID {
		t.Fatalf("comment = %+v", comment)
	}
	if _, err := service.ListIssueComments(ctx, otherTenant, ListIssueCommentsQuery{IssueKind: IssueKindTodo, IssueID: task.TodoID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant comments error = %v, want ErrNotFound", err)
	}
}

func TestDraftSuggestionNormalizesRequestAndResponse(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	suggester := &draftSuggesterStub{response: DraftSuggestion{Title: "  Refined title  ", Tags: []string{"AI", " ai ", "planning"}}}
	service := NewService(ServiceOptions{
		Clock:          clock.NewFake(todoTestTime),
		IDGenerator:    idgen.NewDeterministic("task"),
		Repository:     NewRepository(env.DBRouter),
		DraftSuggester: suggester,
	})

	result, err := service.SuggestDraft(context.Background(), testViewer("tenant-a", "operator"), SuggestDraftRequest{
		Kind:        " idea ",
		Title:       " Original ",
		Content:     " Detail ",
		CurrentTags: []string{" input ", "INPUT"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if suggester.request.Kind != "idea" || !sameStrings(suggester.request.CurrentTags, []string{"input"}) {
		t.Fatalf("suggester request = %+v", suggester.request)
	}
	if result.Title != "Refined title" || !sameStrings(result.Tags, []string{"AI", "planning"}) {
		t.Fatalf("suggestion = %+v", result)
	}
}

func newServiceForTest(repo *Repository) *Service {
	return NewService(ServiceOptions{
		Clock:           clock.NewFake(todoTestTime),
		IDGenerator:     idgen.NewDeterministic("task"),
		IdeaIDGenerator: idgen.NewDeterministic("idea"),
		Repository:      repo,
	})
}

func testViewer(tenantID, viewerID string) ViewerRef {
	return ViewerRef{
		TenantID:    tenantID,
		ViewerID:    viewerID,
		ViewerKind:  ViewerKindUser,
		DisplayName: viewerID,
	}
}

func mustCreateTask(t *testing.T, service *Service, ctx context.Context, viewer ViewerRef, req CreateTodoRequest) TodoItem {
	t.Helper()
	item, err := service.CreateTodo(ctx, viewer, req)
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func mustCreateIdea(t *testing.T, service *Service, ctx context.Context, viewer ViewerRef, title string) IdeaItem {
	t.Helper()
	item, err := service.CreateIdea(ctx, viewer, CreateIdeaRequest{Title: title})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func int64Pointer(value int64) *int64 {
	return &value
}

func sameStrings(got []string, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

type draftSuggesterStub struct {
	request  SuggestDraftRequest
	response DraftSuggestion
	err      error
}

func (s *draftSuggesterStub) SuggestDraft(_ context.Context, req SuggestDraftRequest) (DraftSuggestion, error) {
	s.request = req
	return s.response, s.err
}
