package discussionsession

import (
	"context"
	"testing"
	"time"
)

func TestServiceRunLifecycleAllowsDifferentDiscussionsToCompleteInParallel(t *testing.T) {
	repo := newMemoryRepository(t)
	blockingTooling := newBlockingMissionToolApplier()
	service := newTestService(ServiceOptions{
		Repository:      repo,
		MissionTooling:  blockingTooling,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("msg-a", "msg-b"),
		DiscussionAgent: &capturingDiscussionAgentRunner{agentID: "lead-agent"},
	})
	ctx := context.Background()
	seedRunningDiscussionRun(t, repo, "conv-a", "run-a", testTime)
	seedRunningDiscussionRun(t, repo, "conv-b", "run-b", testTime.Add(time.Nanosecond))

	errCh := make(chan error, 1)
	go func() {
		_, _, err := service.completeRun(ctx, "conv-a", RunRecord{RunID: "run-a", ConversationID: "conv-a", AgentID: "lead-agent", Status: "running", CreatedAt: testTime, StartedAt: testTime}, DiscussionAgentResponse{
			Status:           "completed",
			RuntimeKind:      "managed",
			AssistantMessage: "我会创建执行记录。",
			ToolCalls: []ToolCallDTO{{
				ToolCallID: "tool-a",
				Name:       "mission_brief_create",
				Arguments:  map[string]string{"title": "A"},
			}},
		})
		errCh <- err
	}()

	select {
	case <-blockingTooling.entered:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("first discussion did not enter blocking mission tooling")
	}

	doneB := make(chan error, 1)
	go func() {
		_, _, err := service.completeRun(ctx, "conv-b", RunRecord{RunID: "run-b", ConversationID: "conv-b", AgentID: "lead-agent", Status: "running", CreatedAt: testTime.Add(time.Nanosecond), StartedAt: testTime.Add(time.Nanosecond)}, DiscussionAgentResponse{
			Status:           "completed",
			RuntimeKind:      "managed",
			AssistantMessage: "B 组先完成。",
		})
		doneB <- err
	}()

	select {
	case err := <-doneB:
		if err != nil {
			t.Fatalf("second discussion completion failed: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("second discussion completion was blocked by another discussion lifecycle")
	}

	blockingTooling.Release()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("first discussion completion failed after release: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("first discussion did not finish after releasing tooling")
	}
}

func TestServiceSendMessageStreamQueuesSameDiscussionUntilStreamFinishes(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-1", Title: "Launch plan", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-1", "user-1")
	streamEvents := make(chan DiscussionAgentStreamEvent, 1)
	runner := newBlockingDiscussionAgentRunner(DiscussionAgentResponse{AssistantMessage: "排队后的回复", Status: "completed", RuntimeKind: "managed"})
	runner.streamEvents = streamEvents
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime.Add(time.Minute)},
		IDGenerator:     fixedIDGen("msg-stream", "run-stream", "msg-after", "run-after", "msg-after-reply", "msg-stream-reply"),
	})

	events, err := service.SendMessageStream(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{
		ConversationID: "conv-1",
		Content:        "先跑流式",
		IdempotencyKey: "send-stream",
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.streamStarted:
	case <-time.After(time.Second):
		t.Fatal("stream run did not start")
	}

	sendDone := make(chan error, 1)
	go func() {
		_, err := service.SendMessage(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{
			ConversationID: "conv-1",
			Content:        "流结束后继续",
			IdempotencyKey: "send-after-stream",
		})
		sendDone <- err
	}()
	select {
	case err := <-sendDone:
		t.Fatalf("same discussion send completed before stream released: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if got := runner.RequestCount(); got != 0 {
		t.Fatalf("sync runner requests before stream completion = %d, want queued send", got)
	}

	streamEvents <- DiscussionAgentStreamEvent{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{AssistantMessage: "流式回复", Status: "completed", RuntimeKind: "managed"}}
	close(streamEvents)
	drainLeadStream(events)
	runner.Release()
	if err := <-sendDone; err != nil {
		t.Fatal(err)
	}
	if got := runner.RequestCount(); got != 1 {
		t.Fatalf("sync runner requests after stream completion = %d, want queued send", got)
	}
	if got := runner.StreamRequestCount(); got != 1 {
		t.Fatalf("stream runner requests = %d, want initial stream", got)
	}
}

func TestServiceSendMessageStreamQueuesSameDiscussionBeforePersistingNextTurn(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-1", Title: "Launch plan", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-1", "user-1")
	streamEvents := make(chan DiscussionAgentStreamEvent, 1)
	runner := newBlockingDiscussionAgentRunner(DiscussionAgentResponse{AssistantMessage: "unused", Status: "completed", RuntimeKind: "managed"})
	runner.streamEvents = streamEvents
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime.Add(time.Minute)},
		IDGenerator:     fixedIDGen("msg-stream", "run-stream", "msg-stream-reply", "msg-queued", "run-queued", "msg-queued-reply"),
	})
	viewer := ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}

	firstEvents, err := service.SendMessageStream(ctx, viewer, SendMessageCommand{
		ConversationID: "conv-1",
		Content:        "先跑流式",
		IdempotencyKey: "send-stream",
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.streamStarted:
	case <-time.After(time.Second):
		t.Fatal("stream run did not start")
	}

	secondDone := make(chan error, 1)
	go func() {
		secondEvents, err := service.SendMessageStream(ctx, viewer, SendMessageCommand{
			ConversationID: "conv-1",
			Content:        "排队中的下一轮",
			IdempotencyKey: "send-queued",
		})
		if err == nil {
			streamEvents <- DiscussionAgentStreamEvent{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{AssistantMessage: "排队回复", Status: "completed", RuntimeKind: "managed"}}
			drainLeadStream(secondEvents)
		}
		secondDone <- err
	}()
	select {
	case err := <-secondDone:
		t.Fatalf("same discussion stream completed before first stream released: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	queued, err := repo.GetConversation(ctx, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(queued.Messages) != 1 || queued.Messages[0].Content != "先跑流式" {
		t.Fatalf("queued same-discussion stream persisted messages before prior stream finished: %+v", queued.Messages)
	}
	if len(queued.Runs) != 1 || queued.Runs[0].Status != "running" {
		t.Fatalf("queued same-discussion stream persisted runs before prior stream finished: %+v", queued.Runs)
	}

	streamEvents <- DiscussionAgentStreamEvent{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{AssistantMessage: "流式回复", Status: "completed", RuntimeKind: "managed"}}
	drainLeadStream(firstEvents)
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
}

func TestServiceSendMessageStreamQueuesReusedPersonalAssistantConversation(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	seedPersonalAssistantConversation(t, repo, "conv-pa", "tenant-a", "user-1", "account-1", testTime)
	streamEvents := make(chan DiscussionAgentStreamEvent, 2)
	runner := newBlockingDiscussionAgentRunner(DiscussionAgentResponse{AssistantMessage: "unused", Status: "completed", RuntimeKind: "managed"})
	runner.streamEvents = streamEvents
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime.Add(time.Minute)},
		IDGenerator:     fixedIDGen("msg-first", "run-first", "msg-first-reply", "msg-second", "run-second", "msg-second-reply"),
	})
	viewer := ViewerRef{ViewerID: "user-1", OwnerID: "account-1", ViewerKind: ViewerKindUser, TenantID: "tenant-a"}
	command := func(content, key string) SendMessageCommand {
		return SendMessageCommand{ConversationKind: ConversationKindPersonalAssistant, Content: content, IdempotencyKey: key}
	}

	firstEvents, err := service.SendMessageStream(ctx, viewer, command("first", "send-first"))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.streamStarted:
	case <-time.After(time.Second):
		t.Fatal("first personal assistant stream did not start")
	}

	secondReady := make(chan (<-chan StreamEventDTO), 1)
	secondDone := make(chan error, 1)
	go func() {
		events, sendErr := service.SendMessageStream(ctx, viewer, command("second", "send-second"))
		if sendErr == nil {
			secondReady <- events
		}
		secondDone <- sendErr
	}()
	select {
	case err := <-secondDone:
		t.Fatalf("reused personal assistant send completed before first stream released: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	queued, err := repo.GetConversation(ctx, "conv-pa")
	if err != nil {
		t.Fatal(err)
	}
	if len(queued.Messages) != 1 || queued.Messages[0].Content != "first" {
		t.Fatalf("queued personal assistant send persisted too early: %+v", queued.Messages)
	}

	streamEvents <- DiscussionAgentStreamEvent{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{AssistantMessage: "first reply", Status: "completed", RuntimeKind: "managed"}}
	drainLeadStream(firstEvents)
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	secondEvents := <-secondReady
	streamEvents <- DiscussionAgentStreamEvent{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{AssistantMessage: "second reply", Status: "completed", RuntimeKind: "managed"}}
	drainLeadStream(secondEvents)

	completed, err := repo.GetConversation(ctx, "conv-pa")
	if err != nil {
		t.Fatal(err)
	}
	messageCounts := map[string]int{}
	for _, message := range completed.Messages {
		messageCounts[message.Role+":"+message.Content]++
	}
	if len(completed.Messages) != 4 || messageCounts["user:first"] != 1 || messageCounts["assistant:first reply"] != 1 || messageCounts["user:second"] != 1 || messageCounts["assistant:second reply"] != 1 {
		t.Fatalf("personal assistant messages = %+v", completed.Messages)
	}
}
