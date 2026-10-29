package discussionsession

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestServiceSendMessageRecoversStaleActiveTurn(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	seedConversationWithActiveTurn(t, repo, testTime)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "Recovered and continued.",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	service := newTestService(ServiceOptions{
		Repository:          repo,
		DiscussionAgent:     runner,
		Clock:               fixedClock{now: testTime.Add(41 * time.Minute)},
		TurnRecoveryTimeout: 40 * time.Minute,
		IDGenerator:         fixedIDGen("msg-new", "run-new", "msg-assistant"),
	})

	detail, err := service.SendMessage(ctx, ViewerRef{
		ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户",
	}, SendMessageCommand{
		ConversationID: "conv-stale",
		Content:        "Continue",
		IdempotencyKey: "send-after-stale-turn",
	})
	if err != nil {
		t.Fatalf("SendMessage after stale turn: %v", err)
	}
	if !runner.called || len(detail.Messages) != 2 || !strings.HasSuffix(detail.Messages[1].Content, "Recovered and continued.") {
		t.Fatalf("continued discussion = %+v, runtime called = %v", detail, runner.called)
	}
	staleTurn, err := repo.GetTurn(ctx, "turn-stale")
	if err != nil || staleTurn.Status != "failed" || staleTurn.ErrorCode != "executor_interrupted" {
		t.Fatalf("stale turn = %+v, error = %v", staleTurn, err)
	}
	current, err := repo.GetConversation(ctx, "conv-stale")
	if err != nil || current.ActiveTurnID != "" || current.Status != "completed" {
		t.Fatalf("recovered conversation = %+v, error = %v", current.ConversationRecord, err)
	}
}

func TestServiceSendMessageKeepsFreshActiveTurnFenced(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	seedConversationWithActiveTurn(t, repo, testTime)
	runner := &capturingDiscussionAgentRunner{}
	service := newTestService(ServiceOptions{
		Repository:          repo,
		DiscussionAgent:     runner,
		Clock:               fixedClock{now: testTime.Add(39 * time.Minute)},
		TurnRecoveryTimeout: 40 * time.Minute,
		IDGenerator:         fixedIDGen("msg-new"),
	})

	_, err := service.SendMessage(ctx, ViewerRef{
		ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户",
	}, SendMessageCommand{
		ConversationID: "conv-stale",
		Content:        "Do not overlap",
		IdempotencyKey: "send-during-fresh-turn",
	})
	if !errors.Is(err, ErrCommandInProgress) {
		t.Fatalf("SendMessage error = %v, want ErrCommandInProgress", err)
	}
	if runner.called {
		t.Fatal("fresh active turn must not start another runtime")
	}
	current, getErr := repo.GetConversation(ctx, "conv-stale")
	if getErr != nil || current.ActiveTurnID != "turn-stale" {
		t.Fatalf("fresh active turn changed = %+v, error = %v", current.ConversationRecord, getErr)
	}
}

func TestServiceSendMessageDoesNotRecoverStaleTurnWaitingOnApproval(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	seedConversationWithActiveTurn(t, repo, testTime)
	if err := repo.SaveEvent(ctx, EventRecord{
		EventID:        "approval-old",
		ConversationID: "conv-stale",
		Kind:           discussionRuntimeApprovalEventKind,
		Status:         "pending",
		Metadata: map[string]string{
			"approval_run_id":       "run-old",
			"approval_hil_required": "true",
		},
		CreatedAt: testTime.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	runner := &capturingDiscussionAgentRunner{}
	service := newTestService(ServiceOptions{
		Repository:          repo,
		DiscussionAgent:     runner,
		Clock:               fixedClock{now: testTime.Add(41 * time.Minute)},
		TurnRecoveryTimeout: 40 * time.Minute,
		IDGenerator:         fixedIDGen("msg-new"),
	})

	_, err := service.SendMessage(ctx, ViewerRef{
		ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "Local user",
	}, SendMessageCommand{
		ConversationID: "conv-stale",
		Content:        "Do not interrupt the approval",
		IdempotencyKey: "send-during-stale-approval",
	})
	if !errors.Is(err, ErrCommandInProgress) {
		t.Fatalf("SendMessage error = %v, want ErrCommandInProgress", err)
	}
	if runner.called {
		t.Fatal("approval-waiting turn must not start another runtime")
	}
	turn, getErr := repo.GetTurn(ctx, "turn-stale")
	if getErr != nil || turn.Status != "running" {
		t.Fatalf("approval-waiting turn = %+v, error = %v", turn, getErr)
	}
	current, getErr := repo.GetConversation(ctx, "conv-stale")
	if getErr != nil || current.ActiveTurnID != "turn-stale" {
		t.Fatalf("approval-waiting conversation = %+v, error = %v", current.ConversationRecord, getErr)
	}
}

func TestRepositoryRecoversStaleTurnWithNonDurableApproval(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	seedConversationWithActiveTurn(t, repo, testTime)
	if err := repo.SaveEvent(ctx, EventRecord{
		EventID:        "approval-memory-only",
		ConversationID: "conv-stale",
		Kind:           discussionRuntimeApprovalEventKind,
		Status:         "pending",
		Metadata:       map[string]string{"approval_run_id": "run-old"},
		CreatedAt:      testTime.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	recovered, err := repo.FailStaleTurn(ctx, "conv-stale", "turn-stale", testTime.Add(time.Minute), testTime.Add(41*time.Minute))
	if err != nil || !recovered {
		t.Fatalf("recover memory-only approval turn: recovered=%v error=%v", recovered, err)
	}
}

func seedConversationWithActiveTurn(t *testing.T, repo *Repository, updatedAt time.Time) {
	t.Helper()
	ctx := context.Background()
	conversation := ConversationRecord{
		ConversationID: "conv-stale",
		Title:          "Existing discussion",
		Status:         "completed",
		CreatedBy:      "user-1",
		CreatedAt:      updatedAt,
		UpdatedAt:      updatedAt,
	}
	if err := repo.SaveConversation(ctx, conversation); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, conversation.ConversationID, "user-1")
	current, err := repo.GetConversation(ctx, conversation.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	turn := TurnRecord{
		TurnID:         "turn-stale",
		ConversationID: conversation.ConversationID,
		RootMessageID:  "msg-old",
		Status:         "running",
		StartedAt:      updatedAt,
		CreatedAt:      updatedAt,
		UpdatedAt:      updatedAt,
	}
	run := RunRecord{
		RunID:          "run-old",
		ConversationID: conversation.ConversationID,
		TurnID:         turn.TurnID,
		AgentID:        "lead-agent",
		AgentName:      "顾明澈",
		Status:         "completed",
		CreatedAt:      updatedAt,
		StartedAt:      updatedAt,
		CompletedAt:    updatedAt.Add(time.Minute),
	}
	if _, err := repo.ApplyTurnMutation(ctx, TurnAggregateMutation{
		Conversation:     current.ConversationRecord,
		ExpectedRevision: current.Revision,
		Turn:             &turn,
		Runs:             []RunRecord{run},
	}); err != nil {
		t.Fatal(err)
	}
}
