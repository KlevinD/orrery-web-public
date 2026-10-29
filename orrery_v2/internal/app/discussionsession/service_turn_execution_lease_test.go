package discussionsession

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type memoryDiscussionExecutionClaims struct {
	mu              sync.Mutex
	owners          map[string]string
	renewed         chan struct{}
	loseRenewPrefix string
	loseRenewAfter  <-chan struct{}
}

func newMemoryDiscussionExecutionClaims() *memoryDiscussionExecutionClaims {
	return &memoryDiscussionExecutionClaims{owners: map[string]string{}}
}

func (c *memoryDiscussionExecutionClaims) Acquire(_ context.Context, claimID, workerID string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.owners[claimID]; ok {
		return false, nil
	}
	c.owners[claimID] = workerID
	return true, nil
}

func (c *memoryDiscussionExecutionClaims) Renew(_ context.Context, claimID, workerID string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.owners[claimID] != workerID {
		return false, nil
	}
	if c.loseRenewPrefix != "" && strings.HasPrefix(claimID, c.loseRenewPrefix) {
		if c.loseRenewAfter != nil {
			select {
			case <-c.loseRenewAfter:
			default:
				return true, nil
			}
		}
		delete(c.owners, claimID)
		return false, nil
	}
	if c.renewed != nil {
		select {
		case c.renewed <- struct{}{}:
		default:
		}
	}
	return true, nil
}

type leaseCancellationDiscussionAgentRunner struct {
	enableLeaseLoss chan struct{}
}

func (r *leaseCancellationDiscussionAgentRunner) RunDiscussion(ctx context.Context, _ DiscussionAgentRequest) (DiscussionAgentResponse, error) {
	close(r.enableLeaseLoss)
	<-ctx.Done()
	return DiscussionAgentResponse{}, ctx.Err()
}

func (*leaseCancellationDiscussionAgentRunner) DiscussionAgentID() string {
	return "lead-agent"
}

func (c *memoryDiscussionExecutionClaims) Release(_ context.Context, claimID, workerID string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.owners[claimID] != workerID {
		return false, nil
	}
	delete(c.owners, claimID)
	return true, nil
}

func TestServiceSendMessageStreamRecoversInterruptedTurnFromExpiredLease(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	claims := newMemoryDiscussionExecutionClaims()
	viewer := ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, TenantID: "tenant-a", OwnerID: "owner-1", DisplayName: "User"}
	cmd := SendMessageCommand{ConversationKind: ConversationKindPersonalAssistant, Content: "Continue the work", IdempotencyKey: "gateway-restart-1"}
	command, conversationID, source := seedLegacyPendingSendMessage(t, repo, viewer, cmd)
	command.ConversationID = conversationID
	command.MessageID = source.MessageID
	command.TurnID = source.TurnID
	command.UpdatedAt = testTime.Add(time.Second)
	if err := repo.SavePendingCommandProgress(ctx, command); err != nil {
		t.Fatal(err)
	}

	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{{
		Kind: DiscussionAgentStreamRunCompleted,
		Response: DiscussionAgentResponse{
			AssistantMessage: "Recovered after restart", Status: "completed", RuntimeKind: "managed",
		},
	}}}
	restarted := newTestService(ServiceOptions{
		Repository: repo, DiscussionAgent: runner, Clock: fixedClock{now: testTime.Add(time.Minute)},
		TurnRecoveryTimeout: 40 * time.Minute, DiscussionExecutionClaims: claims, DiscussionExecutorID: "executor-new",
		DiscussionLeaseRenew: time.Second, IDGenerator: fixedIDGen("run-recovered", "message-assistant"),
	})
	stream, err := restarted.SendMessageStream(ctx, viewer, cmd)
	if err != nil {
		t.Fatalf("recover interrupted command before timestamp timeout: %v", err)
	}
	for event := range stream {
		if event.Kind == StreamEventError {
			t.Fatalf("recovery stream error: %s", event.Error)
		}
	}

	current, err := repo.GetConversation(ctx, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	var userMessages, assistantMessages int
	for _, message := range current.Messages {
		switch strings.ToLower(strings.TrimSpace(message.Role)) {
		case "user":
			userMessages++
		case "assistant":
			assistantMessages++
		}
	}
	if userMessages != 1 || assistantMessages != 1 {
		t.Fatalf("messages after lease recovery: users=%d assistants=%d all=%+v", userMessages, assistantMessages, current.Messages)
	}
	oldTurn, err := repo.GetTurn(ctx, source.TurnID)
	if err != nil || oldTurn.Status != "failed" || oldTurn.ErrorCode != "executor_interrupted" {
		t.Fatalf("interrupted turn = %+v err=%v", oldTurn, err)
	}
	stored, err := repo.GetCommand(ctx, command.Operation, viewer, cmd.IdempotencyKey)
	if err != nil || stored.Status != discussionCommandCompleted || stored.TurnID == source.TurnID {
		t.Fatalf("recovered command = %+v err=%v", stored, err)
	}
	if acquired, err := claims.Acquire(ctx, discussionCommandExecutionClaimPrefix+stored.CommandID, "executor-after-completion"); err != nil || !acquired {
		t.Fatalf("completed command lease was not released: acquired=%v err=%v", acquired, err)
	}
}

func TestServiceSendMessageStreamRecoversCommandBeforeTurnProgressWasSaved(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	claims := newMemoryDiscussionExecutionClaims()
	viewer := ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, TenantID: "tenant-a", OwnerID: "owner-1", DisplayName: "User"}
	cmd := SendMessageCommand{ConversationKind: ConversationKindPersonalAssistant, Content: "Start after restart", IdempotencyKey: "gateway-pre-turn-restart"}
	setup := newTestService(ServiceOptions{Repository: repo, DiscussionAgent: &capturingDiscussionAgentRunner{}, Clock: fixedClock{now: testTime}})
	command, claimed, err := setup.claimDiscussionCommand(ctx, "send_message", viewer, cmd.IdempotencyKey, cmd)
	if err != nil || !claimed {
		t.Fatalf("seed command before progress: command=%+v claimed=%v err=%v", command, claimed, err)
	}

	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{{
		Kind: DiscussionAgentStreamRunCompleted,
		Response: DiscussionAgentResponse{
			AssistantMessage: "Started after restart", Status: "completed", RuntimeKind: "managed",
		},
	}}}
	restarted := newTestService(ServiceOptions{
		Repository: repo, DiscussionAgent: runner, Clock: fixedClock{now: testTime.Add(time.Minute)},
		TurnRecoveryTimeout: 40 * time.Minute, DiscussionExecutionClaims: claims, DiscussionExecutorID: "executor-new",
		IDGenerator: fixedIDGen("conversation-recovered", "message-user", "run-recovered", "message-assistant"),
	})
	stream, err := restarted.SendMessageStream(ctx, viewer, cmd)
	if err != nil {
		t.Fatalf("recover command before turn progress: %v", err)
	}
	for event := range stream {
		if event.Kind == StreamEventError {
			t.Fatalf("pre-turn recovery stream error: %s", event.Error)
		}
	}
	stored, err := repo.GetCommand(ctx, command.Operation, viewer, cmd.IdempotencyKey)
	if err != nil || stored.Status != discussionCommandCompleted || stored.ConversationID == "" || stored.MessageID == "" || stored.TurnID == "" {
		t.Fatalf("pre-turn recovered command = %+v err=%v", stored, err)
	}
}

func TestServiceSendMessageStreamKeepsLiveTurnLeaseFenced(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	claims := newMemoryDiscussionExecutionClaims()
	viewer := ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, TenantID: "tenant-a", OwnerID: "owner-1", DisplayName: "User"}
	cmd := SendMessageCommand{ConversationKind: ConversationKindPersonalAssistant, Content: "Continue the work", IdempotencyKey: "gateway-live-1"}
	command, conversationID, source := seedLegacyPendingSendMessage(t, repo, viewer, cmd)
	command.ConversationID = conversationID
	command.MessageID = source.MessageID
	command.TurnID = source.TurnID
	command.UpdatedAt = testTime.Add(time.Second)
	if err := repo.SavePendingCommandProgress(ctx, command); err != nil {
		t.Fatal(err)
	}
	claimID := discussionTurnExecutionClaimPrefix + source.TurnID
	if acquired, err := claims.Acquire(ctx, claimID, "executor-old"); err != nil || !acquired {
		t.Fatalf("hold original executor lease: acquired=%v err=%v", acquired, err)
	}

	runner := &streamingDiscussionAgentRunner{}
	restarted := newTestService(ServiceOptions{
		Repository: repo, DiscussionAgent: runner, Clock: fixedClock{now: testTime.Add(time.Minute)},
		TurnRecoveryTimeout: 40 * time.Minute, DiscussionExecutionClaims: claims, DiscussionExecutorID: "executor-new",
	})
	_, err := restarted.SendMessageStream(ctx, viewer, cmd)
	if !errors.Is(err, ErrCommandInProgress) {
		t.Fatalf("live execution lease error = %v, want ErrCommandInProgress", err)
	}
	if runner.request.MessageID != "" {
		t.Fatalf("live execution lease started a second runtime: %+v", runner.request)
	}
	turn, getErr := repo.GetTurn(ctx, source.TurnID)
	if getErr != nil || turn.Status != "running" {
		t.Fatalf("live leased turn changed = %+v err=%v", turn, getErr)
	}
}

func TestServiceTurnLeaseRecoveryPreservesPendingHIL(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	seedConversationWithActiveTurn(t, repo, testTime)
	if err := repo.SaveEvent(ctx, EventRecord{
		EventID: "approval-old", ConversationID: "conv-stale", Kind: discussionRuntimeApprovalEventKind, Status: "pending",
		Metadata: map[string]string{"approval_run_id": "run-old", "approval_hil_required": "true"}, CreatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	runner := &capturingDiscussionAgentRunner{}
	service := newTestService(ServiceOptions{
		Repository: repo, DiscussionAgent: runner, Clock: fixedClock{now: testTime.Add(time.Minute)},
		TurnRecoveryTimeout: 40 * time.Minute, DiscussionExecutionClaims: newMemoryDiscussionExecutionClaims(), DiscussionExecutorID: "executor-new",
		IDGenerator: fixedIDGen("message-new"),
	})
	_, err := service.SendMessage(ctx, ViewerRef{
		ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "User",
	}, SendMessageCommand{ConversationID: "conv-stale", Content: "Do not interrupt approval", IdempotencyKey: "send-during-hil"})
	if !errors.Is(err, ErrCommandInProgress) {
		t.Fatalf("pending HIL recovery error = %v, want ErrCommandInProgress", err)
	}
	turn, getErr := repo.GetTurn(ctx, "turn-stale")
	if getErr != nil || turn.Status != "running" {
		t.Fatalf("pending HIL turn changed = %+v err=%v", turn, getErr)
	}
	if runner.called {
		t.Fatal("pending HIL must not start another runtime")
	}
}

func TestTurnExecutionLeaseRenewsAndReleases(t *testing.T) {
	claims := newMemoryDiscussionExecutionClaims()
	claims.renewed = make(chan struct{}, 1)
	service := newTestService(ServiceOptions{
		Repository: newMemoryRepository(t), DiscussionExecutionClaims: claims, DiscussionExecutorID: "executor-one",
		DiscussionLeaseRenew: time.Millisecond,
	})
	_, release, acquired, err := service.acquireTurnExecutionLease(context.Background(), "turn-one")
	if err != nil || !acquired {
		t.Fatalf("acquire turn lease: acquired=%v err=%v", acquired, err)
	}
	select {
	case <-claims.renewed:
	case <-time.After(time.Second):
		t.Fatal("turn lease was not renewed")
	}
	if acquired, err := claims.Acquire(context.Background(), discussionTurnExecutionClaimPrefix+"turn-one", "executor-two"); err != nil || acquired {
		t.Fatalf("live lease takeover: acquired=%v err=%v", acquired, err)
	}
	release()
	if acquired, err := claims.Acquire(context.Background(), discussionTurnExecutionClaimPrefix+"turn-one", "executor-two"); err != nil || !acquired {
		t.Fatalf("released lease takeover: acquired=%v err=%v", acquired, err)
	}
}

func TestServiceFinalizesRunningRunWhenTurnLeaseIsLost(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	claims := newMemoryDiscussionExecutionClaims()
	claims.loseRenewPrefix = discussionTurnExecutionClaimPrefix
	lossEnabled := make(chan struct{})
	claims.loseRenewAfter = lossEnabled
	runner := &leaseCancellationDiscussionAgentRunner{enableLeaseLoss: lossEnabled}
	service := newTestService(ServiceOptions{
		Repository: repo, DiscussionAgent: runner, Clock: fixedClock{now: testTime},
		DiscussionExecutionClaims: claims, DiscussionExecutorID: "executor-one", DiscussionLeaseRenew: time.Millisecond,
		IDGenerator: fixedIDGen("conv-lease-loss", "msg-user", "run-lease-loss"),
	})
	viewer := ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}

	_, err := service.SendMessage(ctx, viewer, SendMessageCommand{Content: "Run until lease loss", IdempotencyKey: "lease-loss-1"})
	if err == nil || !strings.Contains(err.Error(), "execution lease lost") {
		t.Fatalf("SendMessage error = %v, want execution lease loss", err)
	}
	detail, getErr := service.GetConversation(ctx, viewer, "conv-lease-loss")
	if getErr != nil {
		t.Fatal(getErr)
	}
	if detail.Status != "failed" || len(detail.Runs) != 1 || detail.Runs[0].Status != "failed" || detail.Runs[0].CompletedAt.IsZero() {
		t.Fatalf("failed lease-loss aggregate = %+v", detail)
	}
	turn, getErr := repo.GetTurn(ctx, detail.Runs[0].TurnID)
	if getErr != nil || turn.Status != "failed" {
		t.Fatalf("lease-loss turn = %+v err=%v", turn, getErr)
	}
}
