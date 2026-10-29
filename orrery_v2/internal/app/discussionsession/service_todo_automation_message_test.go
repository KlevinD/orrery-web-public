package discussionsession

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestServiceTodoAutomationMessageUsesSystemAuthorshipAndStartsAgentTurn(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{AssistantMessage: "Structured proposal", Status: "completed"}}
	service := newTestService(ServiceOptions{Repository: repo, DiscussionAgent: runner, Clock: fixedClock{now: testTime}})
	owner := ViewerRef{ViewerID: "owner-1", ViewerKind: ViewerKindUser, TenantID: "tenant-a"}
	conversation, err := service.EnsureTodoConversation(ctx, owner, EnsureTodoConversationCommand{
		TenantID: "tenant-a", TodoID: "todo-1", Title: "Todo 1", ManagedSource: "todo:tenant-a/todo-1",
		InitialMemberIDs: []string{"lead-agent"},
	})
	if err != nil {
		t.Fatalf("EnsureTodoConversation: %v", err)
	}
	system := ViewerRef{ViewerID: "todo-automation-agent", ViewerKind: ViewerKindSystem, TenantID: "tenant-a"}
	command := SendTodoAutomationMessageCommand{
		ConversationID: conversation.ConversationID, TodoID: "todo-1", ManagedSource: "todo:tenant-a/todo-1",
		Content: "Assess this Todo", IdempotencyKey: "plan-1",
	}
	first, err := service.SendTodoAutomationMessage(ctx, system, command)
	if err != nil {
		t.Fatalf("SendTodoAutomationMessage: %v", err)
	}
	second, err := service.SendTodoAutomationMessage(ctx, system, command)
	if err != nil {
		t.Fatalf("SendTodoAutomationMessage replay: %v", err)
	}
	if !runner.called || runner.request.Viewer.ViewerKind != ViewerKindSystem || runner.request.Viewer.ViewerID != system.ViewerID {
		t.Fatalf("runtime trigger viewer = %+v", runner.request.Viewer)
	}
	if len(first.Messages) != 2 || len(second.Messages) != 2 {
		t.Fatalf("idempotent messages first=%+v second=%+v", first.Messages, second.Messages)
	}
	message := first.Messages[0]
	if message.Role != "system" || message.AuthorID != todoAutomationMessageAuthorID || message.AuthorName != todoAutomationMessageAuthorName || message.Content != command.Content {
		t.Fatalf("automation message = %+v", message)
	}
	if first.CreatedBy != owner.ViewerID || second.Revision != first.Revision {
		t.Fatalf("conversation creator/revisions first=%+v second=%+v", first, second)
	}
}

func TestServiceTodoAutomationMessageRejectsUntrustedOrMismatchedScope(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{Repository: repo, DiscussionAgent: &capturingDiscussionAgentRunner{}, Clock: fixedClock{now: testTime}})
	owner := ViewerRef{ViewerID: "owner-1", ViewerKind: ViewerKindUser, TenantID: "tenant-a"}
	conversation, err := service.EnsureTodoConversation(ctx, owner, EnsureTodoConversationCommand{
		TenantID: "tenant-a", TodoID: "todo-1", ManagedSource: "todo:tenant-a/todo-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	base := SendTodoAutomationMessageCommand{ConversationID: conversation.ConversationID, TodoID: "todo-1", ManagedSource: "todo:tenant-a/todo-1", Content: "Assess", IdempotencyKey: "plan-1"}
	tests := []struct {
		name   string
		viewer ViewerRef
		cmd    SendTodoAutomationMessageCommand
	}{
		{name: "human caller", viewer: owner, cmd: base},
		{name: "other tenant", viewer: ViewerRef{ViewerID: "automation", ViewerKind: ViewerKindSystem, TenantID: "tenant-b"}, cmd: base},
		{name: "other Todo", viewer: ViewerRef{ViewerID: "automation", ViewerKind: ViewerKindSystem, TenantID: "tenant-a"}, cmd: func() SendTodoAutomationMessageCommand { value := base; value.TodoID = "todo-2"; return value }()},
		{name: "forged source", viewer: ViewerRef{ViewerID: "automation", ViewerKind: ViewerKindSystem, TenantID: "tenant-a"}, cmd: func() SendTodoAutomationMessageCommand {
			value := base
			value.ManagedSource = "todo:tenant-a/todo-2"
			return value
		}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.SendTodoAutomationMessage(ctx, test.viewer, test.cmd); !errors.Is(err, ErrInvalidRequest) && !errors.Is(err, ErrForbidden) {
				t.Fatalf("error = %v, want invalid request or forbidden", err)
			}
		})
	}
}

func TestServiceTodoAutomationMessageReconcilesPersistedPromptWithoutDuplicatingTurn(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{AssistantMessage: "Structured proposal", Status: "completed"}}
	service := newTestService(ServiceOptions{Repository: repo, DiscussionAgent: runner, Clock: fixedClock{now: testTime}})
	owner := ViewerRef{ViewerID: "owner-1", ViewerKind: ViewerKindUser, TenantID: "tenant-a"}
	conversation, err := service.EnsureTodoConversation(ctx, owner, EnsureTodoConversationCommand{
		TenantID: "tenant-a", TodoID: "todo-1", Title: "Todo 1", ManagedSource: "todo:tenant-a/todo-1", InitialMemberIDs: []string{"lead-agent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	system := ViewerRef{ViewerID: "todo-automation-agent", ViewerKind: ViewerKindSystem, TenantID: "tenant-a"}
	cmd := SendTodoAutomationMessageCommand{
		ConversationID: conversation.ConversationID, TodoID: "todo-1", ManagedSource: "todo:tenant-a/todo-1", Content: "Assess this Todo", IdempotencyKey: "plan-persisted",
	}
	command, claimed, err := service.claimDiscussionCommand(ctx, "send_todo_automation_message", system, cmd.IdempotencyKey, cmd)
	if err != nil || !claimed {
		t.Fatalf("claim command = %+v claimed=%v err=%v", command, claimed, err)
	}
	messageID := todoAutomationMessageID(command.CommandID)
	turnID := stableDiscussionTurnID(DiscussionTurnOriginMessage, cmd.ConversationID, messageID, cmd.IdempotencyKey)
	command.ConversationID, command.MessageID, command.TurnID = cmd.ConversationID, messageID, turnID
	command.UpdatedAt = testTime
	if err := repo.SavePendingCommandProgress(ctx, command); err != nil {
		t.Fatal(err)
	}
	view, err := repo.GetConversation(ctx, cmd.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	message := MessageRecord{
		MessageID: messageID, ConversationID: cmd.ConversationID, TurnID: turnID, Role: "system",
		AuthorID: todoAutomationMessageAuthorID, AuthorName: todoAutomationMessageAuthorName, Content: cmd.Content, CreatedAt: testTime,
	}
	if _, err := repo.ApplyTurnMutation(ctx, TurnAggregateMutation{Conversation: view.ConversationRecord, ExpectedRevision: view.Revision, Messages: []MessageRecord{message}}); err != nil {
		t.Fatal(err)
	}

	result, err := service.SendTodoAutomationMessage(ctx, system, cmd)
	if err != nil {
		t.Fatalf("reconcile persisted prompt: %v", err)
	}
	replayed, err := service.SendTodoAutomationMessage(ctx, system, cmd)
	if err != nil {
		t.Fatalf("replay completed command: %v", err)
	}
	if len(runner.requests) != 1 || len(result.Messages) != 2 || len(replayed.Messages) != 2 || result.Messages[0].MessageID != messageID {
		t.Fatalf("reconciled result=%+v replay=%+v runner requests=%d", result.Messages, replayed.Messages, len(runner.requests))
	}
	stored, err := repo.GetCommand(ctx, "send_todo_automation_message", system, cmd.IdempotencyKey)
	if err != nil || stored.Status != discussionCommandCompleted || stored.MessageID != messageID || stored.TurnID != turnID {
		t.Fatalf("stored command = %+v err=%v", stored, err)
	}
}

func TestServiceTodoAutomationMessageDoesNotRerunTerminalFailedTurn(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	runtimeFailure := errors.New("discussion runtime failed after turn persistence")
	runner := &capturingDiscussionAgentRunner{err: runtimeFailure}
	service := newTestService(ServiceOptions{Repository: repo, DiscussionAgent: runner, Clock: fixedClock{now: testTime}})
	owner := ViewerRef{ViewerID: "owner-1", ViewerKind: ViewerKindUser, TenantID: "tenant-a"}
	conversation, err := service.EnsureTodoConversation(ctx, owner, EnsureTodoConversationCommand{
		TenantID: "tenant-a", TodoID: "todo-1", Title: "Todo 1", ManagedSource: "todo:tenant-a/todo-1", InitialMemberIDs: []string{"lead-agent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	system := ViewerRef{ViewerID: "todo-automation-agent", ViewerKind: ViewerKindSystem, TenantID: "tenant-a"}
	cmd := SendTodoAutomationMessageCommand{
		ConversationID: conversation.ConversationID, TodoID: "todo-1", ManagedSource: "todo:tenant-a/todo-1", Content: "Assess this Todo", IdempotencyKey: "plan-failed-turn",
	}
	first, firstErr := service.SendTodoAutomationMessage(ctx, system, cmd)
	if firstErr != nil {
		t.Fatalf("terminal turn should reconcile durable state: %v", firstErr)
	}
	second, err := service.SendTodoAutomationMessage(ctx, system, cmd)
	if err != nil {
		t.Fatalf("replay terminal turn: %v", err)
	}
	if len(runner.requests) != 1 || len(first.Messages) != 1 || len(second.Messages) != 1 {
		t.Fatalf("terminal replay first=%+v second=%+v runner requests=%d", first.Messages, second.Messages, len(runner.requests))
	}
	stored, err := repo.GetCommand(ctx, "send_todo_automation_message", system, cmd.IdempotencyKey)
	if err != nil || stored.Status != discussionCommandCompleted || stored.MessageID == "" || stored.TurnID == "" {
		t.Fatalf("terminal command = %+v err=%v", stored, err)
	}
	turn, err := repo.GetTurn(ctx, stored.TurnID)
	if err != nil || turn.Status != "failed" {
		t.Fatalf("terminal turn = %+v err=%v", turn, err)
	}
}

func TestServiceTodoAutomationMessageRecoversStaleTurnAfterRestart(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	owner := ViewerRef{ViewerID: "owner-1", ViewerKind: ViewerKindUser, TenantID: "tenant-a"}
	setup := newTestService(ServiceOptions{Repository: repo, DiscussionAgent: &capturingDiscussionAgentRunner{}, Clock: fixedClock{now: testTime}})
	conversation, err := setup.EnsureTodoConversation(ctx, owner, EnsureTodoConversationCommand{
		TenantID: "tenant-a", TodoID: "todo-1", Title: "Todo 1", ManagedSource: "todo:tenant-a/todo-1", InitialMemberIDs: []string{"lead-agent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	system := ViewerRef{ViewerID: "todo-automation-agent", ViewerKind: ViewerKindSystem, TenantID: "tenant-a"}
	cmd := SendTodoAutomationMessageCommand{
		ConversationID: conversation.ConversationID, TodoID: "todo-1", ManagedSource: "todo:tenant-a/todo-1", Content: "Assess this Todo", IdempotencyKey: "plan-stale-turn",
	}
	command, claimed, err := setup.claimDiscussionCommand(ctx, "send_todo_automation_message", system, cmd.IdempotencyKey, cmd)
	if err != nil || !claimed {
		t.Fatalf("claim command = %+v claimed=%v err=%v", command, claimed, err)
	}
	messageID := todoAutomationMessageID(command.CommandID)
	turnID := stableDiscussionTurnID(DiscussionTurnOriginMessage, cmd.ConversationID, messageID, cmd.IdempotencyKey)
	command.ConversationID, command.MessageID, command.TurnID = cmd.ConversationID, messageID, turnID
	command.UpdatedAt = testTime
	if err := repo.SavePendingCommandProgress(ctx, command); err != nil {
		t.Fatal(err)
	}
	view, err := repo.GetConversation(ctx, cmd.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	message := MessageRecord{
		MessageID: messageID, ConversationID: cmd.ConversationID, TurnID: turnID, Role: "system",
		AuthorID: todoAutomationMessageAuthorID, AuthorName: todoAutomationMessageAuthorName, Content: cmd.Content, CreatedAt: testTime,
	}
	turn := TurnRecord{
		TurnID: turnID, ConversationID: cmd.ConversationID, RootMessageID: messageID, IdempotencyKey: cmd.IdempotencyKey,
		Mode: string(discussionTurnTargetGroup), Status: "running", StartedAt: testTime, CreatedAt: testTime, UpdatedAt: testTime,
	}
	if _, err := repo.ApplyTurnMutation(ctx, TurnAggregateMutation{
		Conversation: view.ConversationRecord, ExpectedRevision: view.Revision, Messages: []MessageRecord{message}, Turn: &turn,
	}); err != nil {
		t.Fatal(err)
	}

	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{AssistantMessage: "must not run", Status: "completed"}}
	restarted := newTestService(ServiceOptions{
		Repository: repo, DiscussionAgent: runner, Clock: fixedClock{now: testTime.Add(41 * time.Minute)}, TurnRecoveryTimeout: 40 * time.Minute,
	})
	result, err := restarted.SendTodoAutomationMessage(ctx, system, cmd)
	if err != nil {
		t.Fatalf("recover stale turn: %v", err)
	}
	if len(runner.requests) != 0 {
		t.Fatalf("runtime requests = %d, want 0", len(runner.requests))
	}
	if len(result.Messages) != 1 || result.Messages[0].MessageID != messageID {
		t.Fatalf("recovered messages = %+v", result.Messages)
	}
	storedTurn, err := repo.GetTurn(ctx, turnID)
	if err != nil || storedTurn.Status != "failed" || storedTurn.ErrorCode != "executor_interrupted" {
		t.Fatalf("recovered turn = %+v err=%v", storedTurn, err)
	}
	storedConversation, err := repo.GetConversation(ctx, cmd.ConversationID)
	if err != nil || storedConversation.ActiveTurnID != "" {
		t.Fatalf("recovered conversation = %+v err=%v", storedConversation.ConversationRecord, err)
	}
	storedCommand, err := repo.GetCommand(ctx, "send_todo_automation_message", system, cmd.IdempotencyKey)
	if err != nil || storedCommand.Status != discussionCommandCompleted {
		t.Fatalf("recovered command = %+v err=%v", storedCommand, err)
	}
}

func TestServiceTodoAutomationMessageContinuesWhenInitialRealtimeNotificationFails(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{AssistantMessage: "Structured proposal", Status: "completed"}}
	realtime := &failOnceTodoAutomationRealtime{failure: errors.New("realtime unavailable")}
	service := newTestService(ServiceOptions{Repository: repo, DiscussionAgent: runner, Clock: fixedClock{now: testTime}})
	owner := ViewerRef{ViewerID: "owner-1", ViewerKind: ViewerKindUser, TenantID: "tenant-a"}
	conversation, err := service.EnsureTodoConversation(ctx, owner, EnsureTodoConversationCommand{
		TenantID: "tenant-a", TodoID: "todo-1", Title: "Todo 1", ManagedSource: "todo:tenant-a/todo-1", InitialMemberIDs: []string{"lead-agent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	service.realtime = realtime
	system := ViewerRef{ViewerID: "todo-automation-agent", ViewerKind: ViewerKindSystem, TenantID: "tenant-a"}
	cmd := SendTodoAutomationMessageCommand{
		ConversationID: conversation.ConversationID, TodoID: "todo-1", ManagedSource: "todo:tenant-a/todo-1", Content: "Assess this Todo", IdempotencyKey: "plan-realtime",
	}
	first, err := service.SendTodoAutomationMessage(ctx, system, cmd)
	if err != nil {
		t.Fatalf("SendTodoAutomationMessage: %v", err)
	}
	second, err := service.SendTodoAutomationMessage(ctx, system, cmd)
	if err != nil {
		t.Fatalf("SendTodoAutomationMessage replay: %v", err)
	}
	if realtime.calls < 2 || len(runner.requests) != 1 || len(first.Messages) != 2 || len(second.Messages) != 2 {
		t.Fatalf("realtime calls=%d runner requests=%d first=%+v second=%+v", realtime.calls, len(runner.requests), first.Messages, second.Messages)
	}
}

type failOnceTodoAutomationRealtime struct {
	calls   int
	failure error
}

func (p *failOnceTodoAutomationRealtime) AppendRealtimeEvent(_ context.Context, _ RealtimeEvent) error {
	p.calls++
	if p.calls == 1 {
		return p.failure
	}
	return nil
}
