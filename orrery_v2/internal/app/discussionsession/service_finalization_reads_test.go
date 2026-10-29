package discussionsession

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/platform/db"
)

func TestPostPolicyWithoutAgentMentionsDoesNotLoadUnrelatedPayloads(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-fast", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "host-reply", ConversationID: "conv-fast", Role: "assistant", AuthorID: "host", Content: "Ordinary final reply", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.router.PutRecord(ctx, attachmentTable, "invalid-attachment", db.Record{
		"conversation_id": "conv-fast", conversationPayload: "{invalid",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetConversation(ctx, "conv-fast"); err == nil {
		t.Fatal("fixture must reject a full aggregate read")
	}
	responder := &capturingDiscussionAgentMentionResponder{}
	service := newTestService(ServiceOptions{Repository: repo, MentionResponder: responder})
	executor := AgentTurnExecutor{service: service}
	sources := []MessageRecord{
		{Content: "ordinary text"},
		{Content: "mention://user/user-2"},
		{Content: "mention://agent/user-1"},
		{Role: "user", RoutingSchemaVersion: 1, Content: "mention://agent/staffing-agent"},
		{Content: "mention://agent/staffing-agent", Mentions: []MessageMentionRecord{{TargetKind: "user", TargetID: "user-2"}}},
	}
	for i, source := range sources {
		source.MessageID, source.ConversationID, source.AuthorID = "root", "conv-fast", "user-1"
		command := DiscussionTurnCommand{ConversationID: "conv-fast", TurnID: "turn-fast", SourceMessage: source}
		// Only the persisted final reply is authoritative for host mentions.
		err := executor.executeGroupPostPolicy(ctx, command, RunRecord{AgentID: "host", CompletedAt: testTime}, DiscussionAgentResponse{AssistantMessage: "mention://agent/staffing-agent"})
		if err != nil || responder.called {
			t.Fatalf("no-op source %d dispatched or loaded unrelated state: called=%v err=%v", i, responder.called, err)
		}
	}
	// A real mention must still load current membership and full request context.
	_, err := service.respondToDiscussionMentionsFromMessage(ctx, "conv-fast", MessageRecord{
		MessageID: "real-mention", ConversationID: "conv-fast", AuthorID: "user-1", Content: "mention://agent/staffing-agent",
	}, nil, testTime, discussionMentionDispatchOptions{})
	if err == nil || responder.called {
		t.Fatalf("real mention bypassed context failure: called=%v err=%v", responder.called, err)
	}
}

type finalizationHookLogger struct{ hook func(string) }

func (*finalizationHookLogger) Debug(context.Context, string, ...any) {}
func (*finalizationHookLogger) Warn(context.Context, string, ...any)  {}
func (l *finalizationHookLogger) Info(_ context.Context, _ string, fields ...any) {
	for i := 0; i+1 < len(fields); i += 2 {
		if fields[i] == "stage" {
			l.hook(fields[i+1].(string))
			return
		}
	}
}

func TestTurnFinishKeepsFreshSnapshotAndRevisionGuard(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh_snapshot", true: "revision_conflict"}[conflict], func(t *testing.T) {
			ctx := context.Background()
			repo := newMemoryRepository(t)
			if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-finish", Status: "running", ActiveTurnID: "turn-finish", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
				t.Fatal(err)
			}
			if err := repo.SaveRun(ctx, RunRecord{RunID: "run-finish", ConversationID: "conv-finish", TurnID: "turn-finish", Status: "completed", CreatedAt: testTime, CompletedAt: testTime}); err != nil {
				t.Fatal(err)
			}
			injected := false
			logger := &finalizationHookLogger{hook: func(stage string) {
				if injected || stage != map[bool]string{false: "turn_finalize_commit", true: "turn_finalize_load"}[conflict] {
					return
				}
				injected = true
				if conflict {
					current, err := repo.GetConversationRecord(ctx, "conv-finish")
					if err != nil {
						t.Fatal(err)
					}
					current.Title = "Concurrent edit"
					if err := repo.SaveConversation(ctx, current); err != nil {
						t.Fatal(err)
					}
				} else if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "concurrent-message", ConversationID: "conv-finish", Role: "user", Content: "Arrived after commit", CreatedAt: testTime.Add(1)}); err != nil {
					t.Fatal(err)
				}
			}}
			service := newTestService(ServiceOptions{Repository: repo, Logger: logger, Clock: fixedClock{now: testTime}})
			var events []StreamEventDTO
			command := DiscussionTurnCommand{ConversationID: "conv-finish", TurnID: "turn-finish", SourceMessage: MessageRecord{MessageID: "root", CreatedAt: testTime}, OnEvent: func(event StreamEventDTO) bool {
				events = append(events, event)
				return true
			}}
			dto, err := (AgentTurnExecutor{service: service}).finish(ctx, command, nil)
			if !injected {
				t.Fatal("concurrent write was not injected")
			}
			if conflict {
				if !errors.Is(err, ErrRevisionConflict) || len(events) != 0 {
					t.Fatalf("revision conflict emitted a false terminal snapshot: err=%v events=%v", err, events)
				}
				current, getErr := repo.GetConversationRecord(ctx, "conv-finish")
				if getErr != nil || current.ActiveTurnID != "turn-finish" || current.Title != "Concurrent edit" {
					t.Fatalf("concurrent state overwritten: %+v err=%v", current, getErr)
				}
				return
			}
			if err != nil || dto.Status != "completed" || len(dto.Messages) != 1 || dto.Messages[0].MessageID != "concurrent-message" {
				t.Fatalf("final snapshot lost fresh data: %+v err=%v", dto, err)
			}
			if len(events) != 2 || events[0].Kind != StreamEventTurnCompleted || events[1].Kind != StreamEventSnapshot || events[1].Conversation == nil || len(events[1].Conversation.Messages) != 1 {
				t.Fatalf("terminal event ordering or fresh snapshot changed: %+v", events)
			}
		})
	}
}
