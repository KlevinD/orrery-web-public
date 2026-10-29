package todo

import (
	"context"
	"errors"
	"strings"
	"testing"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/rebac"

	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/platform/testkit"
)

type sourceDiscussionStub struct {
	unavailable bool
	notices     map[string]string
	viewers     []discussionsession.ViewerRef
}

func (p *sourceDiscussionStub) GetConversation(_ context.Context, v discussionsession.ViewerRef, id string) (discussionsession.ConversationDTO, error) {
	if v.TenantID != "tenant-a" || v.ViewerID != "agent-a" || id != "conversation-a" {
		return discussionsession.ConversationDTO{}, errors.New("forbidden")
	}
	return discussionsession.ConversationDTO{ConversationID: id}, nil
}

func (p *sourceDiscussionStub) AppendNotice(ctx context.Context, v discussionsession.ViewerRef, cmd discussionsession.AppendNoticeCommand) (discussionsession.ConversationDTO, error) {
	if p.unavailable {
		return discussionsession.ConversationDTO{}, errors.New("offline")
	}
	conversation, err := p.GetConversation(ctx, v, cmd.ConversationID)
	if err != nil {
		return conversation, err
	}
	if old, ok := p.notices[cmd.IdempotencyKey]; ok && old != cmd.Content {
		return conversation, errors.New("changed idempotency payload")
	}
	p.notices[cmd.IdempotencyKey] = cmd.Content
	p.viewers = append(p.viewers, v)
	return conversation, nil
}

func TestHumanHandoffCreationCompletionAndDurableDelivery(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	service := newServiceForTest(NewRepository(env.DBRouter))
	port := &sourceDiscussionStub{unavailable: true, notices: map[string]string{}}
	service.sourceDiscussion = port
	agent := testViewer("tenant-a", "agent-a")
	agent.ViewerKind = ViewerKindAgent
	request := CreateTodoRequest{Title: "Need customer confirmation", Description: "The quote is missing; provide the approved amount.", Status: TodoStatusPlanned, AssigneeID: "human-a", AssigneeKind: AssigneeKindHuman, SourceConversationID: "conversation-a"}
	ctx := context.Background()
	item, err := service.CreateTodo(ctx, agent, request)
	if err != nil {
		t.Fatal(err)
	}
	if item.Source == nil || item.Source.DeliveryStatus != "pending" {
		t.Fatalf("source=%+v", item.Source)
	}
	stored, err := service.repo.GetTodoForTenant(ctx, agent.TenantID, item.TodoID)
	if err != nil || len(stored.sourceNotices) != 1 {
		t.Fatalf("pending=%+v err=%v", stored.sourceNotices, err)
	}
	// A fresh service recovers the outbox rather than relying on process memory.
	service = newServiceForTest(service.repo)
	service.sourceDiscussion = port
	port.unavailable = false
	if err := service.reconcileTodoSourceNotices(ctx); err != nil {
		t.Fatal(err)
	}
	if err := service.reconcileTodoSourceNotices(ctx); err != nil {
		t.Fatal(err)
	}
	if len(port.notices) != 1 {
		t.Fatalf("notices=%v", port.notices)
	}
	item, _ = service.repo.GetTodoForTenant(ctx, agent.TenantID, item.TodoID)
	human := testViewer("tenant-a", "human-a")
	if _, err := service.CompleteTodo(ctx, agent, CompleteTodoRequest{TodoID: item.TodoID, Result: "pretend human result"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("agent completion=%v", err)
	}
	if _, err := service.CompleteTodo(ctx, human, CompleteTodoRequest{TodoID: item.TodoID}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("empty result=%v", err)
	}
	port.unavailable = true
	done, err := service.CompleteTodo(ctx, human, CompleteTodoRequest{TodoID: item.TodoID, Result: "Approved amount: 500", Version: &item.Version})
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != TodoStatusDone || done.Source.ResultBy != "human-a" || done.Source.DeliveryStatus != "pending" {
		t.Fatalf("done=%+v", done)
	}
	pendingPage, err := service.ListTodos(ctx, human, ListTodosQuery{Mine: true, IncludePendingDelivery: true})
	if err != nil || len(pendingPage.Todos) != 1 {
		t.Fatalf("pending delivery hidden: %+v %v", pendingPage, err)
	}
	if _, err := service.CompleteTodo(ctx, human, CompleteTodoRequest{TodoID: item.TodoID, Result: "overwrite"}); err == nil {
		t.Fatal("duplicate completion overwrote result")
	}
	port.unavailable = false
	if err := service.reconcileTodoSourceNotices(ctx); err != nil {
		t.Fatal(err)
	}
	if len(port.notices) != 2 {
		t.Fatalf("notices=%v", port.notices)
	}
	pendingPage, err = service.ListTodos(ctx, human, ListTodosQuery{Mine: true, IncludePendingDelivery: true})
	if err != nil || len(pendingPage.Todos) != 0 {
		t.Fatalf("delivered task still pending: %+v %v", pendingPage, err)
	}
	found := false
	for _, content := range port.notices {
		found = found || strings.Contains(content, "Approved amount: 500")
	}
	if !found {
		t.Fatal("result not returned")
	}
	detail, err := service.GetTodo(ctx, human, item.TodoID)
	if err != nil || detail.Source.CanOpen {
		t.Fatalf("private source exposed: %+v %v", detail.Source, err)
	}
	detail, err = service.GetTodo(ctx, agent, item.TodoID)
	if err != nil || !detail.Source.CanOpen {
		t.Fatalf("requester source missing: %+v %v", detail.Source, err)
	}
	if _, err := service.GetTodo(ctx, testViewer("tenant-b", "human-a"), item.TodoID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross tenant=%v", err)
	}
	login := testViewer("tenant-a", "ding-login")
	login.AuthorizationSubjectID = "human-a"
	page, err := service.ListTodos(ctx, login, ListTodosQuery{Mine: true, IncludeCompleted: true})
	if err != nil || len(page.Todos) != 1 {
		t.Fatalf("canonical assignee missing: %+v %v", page, err)
	}
	page, err = service.ListTodos(ctx, testViewer("tenant-a", "other"), ListTodosQuery{Mine: true, IncludeCompleted: true})
	if err != nil || len(page.Todos) != 0 {
		t.Fatalf("unrelated mine result: %+v %v", page, err)
	}
}

func TestHumanHandoffRejectsInvalidSourcesAndAutomation(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	service := newServiceForTest(NewRepository(env.DBRouter))
	service.sourceDiscussion = &sourceDiscussionStub{notices: map[string]string{}}
	agent := testViewer("tenant-a", "agent-a")
	agent.ViewerKind = ViewerKindAgent
	base := CreateTodoRequest{Title: "Human input", Description: "Required context", AssigneeID: "human-a", AssigneeKind: AssigneeKindHuman, SourceConversationID: "conversation-a"}
	cases := []CreateTodoRequest{base, base, base, base, base}
	cases[0].SourceConversationID = "private-other"
	cases[1].AssigneeKind = AssigneeKindAgent
	cases[2].AssigneeID = ""
	cases[3].Description = ""
	cases[4].AutomationMode = TodoAutomationModeManaged
	for _, req := range cases {
		if _, err := service.CreateTodo(context.Background(), agent, req); err == nil {
			t.Fatalf("accepted %+v", req)
		}
	}
	item, err := service.CreateTodo(context.Background(), agent, base)
	if err != nil {
		t.Fatal(err)
	}
	agentKind := AssigneeKindAgent
	if _, err := service.UpdateTodo(context.Background(), agent, UpdateTodoRequest{TodoID: item.TodoID, AssigneeKind: &agentKind}); err == nil {
		t.Fatal("human handoff reassigned to Agent")
	}
	if err := service.DeleteTodo(context.Background(), agent, DeleteTodoRequest{TodoID: item.TodoID}); err == nil {
		t.Fatal("source history deleted")
	}
}

func TestHumanHandoffAuthorizationAndCanonicalInbox(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	service := newServiceForTest(NewRepository(env.DBRouter))
	access := authorization.NewService(authorization.NewReBACAdapter(rebac.NewMemoryStore(), nil))
	service.SetAuthorization(access, authorization.NewRelationProjector(access))
	service.sourceDiscussion = &sourceDiscussionStub{notices: map[string]string{}}
	agent := authorizedTodoViewer("tenant-a", "agent-a", "agent-a", "todo.read", "todo.write")
	agent.ViewerKind = ViewerKindAgent
	human := authorizedTodoViewer("tenant-a", "ding", "human-a", "todo.read", "todo.write")
	outsider := authorizedTodoViewer("tenant-a", "outsider", "human-other", "todo.read", "todo.write")
	ctx := context.Background()
	item, err := service.CreateTodo(ctx, agent, CreateTodoRequest{Title: "Need input", Description: "Provide confirmation", Status: TodoStatusPlanned, AssigneeID: "human-a", AssigneeKind: AssigneeKindHuman, SourceConversationID: "conversation-a"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := service.ListTodos(ctx, human, ListTodosQuery{Mine: true})
	if err != nil || len(page.Todos) != 1 {
		t.Fatalf("assignee inbox=%+v err=%v", page, err)
	}
	if _, err := service.CompleteTodo(ctx, outsider, CompleteTodoRequest{TodoID: item.TodoID, Result: "forged"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider completion=%v", err)
	}
	if _, err := service.CompleteTodo(ctx, human, CompleteTodoRequest{TodoID: item.TodoID, Result: "Confirmed"}); err != nil {
		t.Fatal(err)
	}
}
