package discussionsession

import (
	"context"
	"errors"
	"testing"
)

type steeringDiscussionAgentRunner struct {
	capturingDiscussionAgentRunner
	steerCalls   int
	steeredRun   RunRecord
	steeredInput string
	steerErr     error
}

func (r *steeringDiscussionAgentRunner) SteerDiscussionRun(_ context.Context, run RunRecord, message string) error {
	r.steerCalls++
	r.steeredRun = run
	r.steeredInput = message
	return r.steerErr
}

func TestServiceInterjectMessageSteersMentionedActiveAgentAndPersistsMessage(t *testing.T) {
	repo := newMemoryRepository(t)
	seedRunningDiscussionRun(t, repo, "conv-1", "run-1", testTime)
	run, err := repo.GetRun(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	run.TurnID = "turn-1"
	run.Mode = "group"
	if err := repo.SaveRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	runner := &steeringDiscussionAgentRunner{capturingDiscussionAgentRunner: capturingDiscussionAgentRunner{agentID: "lead-agent"}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("msg-interject"),
	})
	viewer := ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}
	cmd := InterjectMessageCommand{
		ConversationID: "conv-1",
		Content:        "@负责人 改用新的验收标准",
		Mentions:       []MessageMentionDTO{{TargetKind: "agent", TargetID: "lead-agent", DisplayName: "伪造名称"}},
		ExpectedTurnID: "turn-1",
		ExpectedRunID:  "run-1",
		IdempotencyKey: "interject-1",
	}

	message, err := service.InterjectMessage(context.Background(), viewer, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if runner.steerCalls != 1 || runner.steeredRun.RunID != "run-1" || runner.steeredInput != cmd.Content {
		t.Fatalf("steering = calls:%d run:%+v input:%q", runner.steerCalls, runner.steeredRun, runner.steeredInput)
	}
	if message.MessageID != "msg-interject" || message.TurnID != "turn-1" || message.RunID != "run-1" || len(message.Mentions) != 1 || message.Mentions[0].DisplayName != "负责人" {
		t.Fatalf("message = %+v", message)
	}
	view := mustConversationView(t, repo, "conv-1")
	if len(view.Messages) != 2 || view.Messages[1].Metadata[discussionInterjectionDeliveryMetadataKey] != discussionInterjectionDelivery {
		t.Fatalf("persisted messages = %+v", view.Messages)
	}
	if len(view.Events) != 1 || view.Events[0].Kind != "discussion_run_interjected" || view.Events[0].Metadata["target_agent_id"] != "lead-agent" {
		t.Fatalf("events = %+v", view.Events)
	}

	replayed, err := service.InterjectMessage(context.Background(), viewer, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.MessageID != message.MessageID || runner.steerCalls != 1 {
		t.Fatalf("replay = %+v steer calls=%d", replayed, runner.steerCalls)
	}
}

func TestServiceInterjectMessageRejectsStaleRunPrecondition(t *testing.T) {
	repo := newMemoryRepository(t)
	seedRunningDiscussionRun(t, repo, "conv-1", "run-current", testTime)
	run, err := repo.GetRun(context.Background(), "run-current")
	if err != nil {
		t.Fatal(err)
	}
	run.TurnID = "turn-current"
	if err := repo.SaveRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	runner := &steeringDiscussionAgentRunner{capturingDiscussionAgentRunner: capturingDiscussionAgentRunner{agentID: "lead-agent"}}
	service := newTestService(ServiceOptions{Repository: repo, DiscussionAgent: runner, Clock: fixedClock{now: testTime}, IDGenerator: fixedIDGen("unused")})

	_, err = service.InterjectMessage(context.Background(), ViewerRef{
		ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户",
	}, InterjectMessageCommand{
		ConversationID: "conv-1",
		Content:        "@负责人 改一下",
		Mentions:       []MessageMentionDTO{{TargetKind: "agent", TargetID: "lead-agent"}},
		ExpectedTurnID: "turn-stale",
		ExpectedRunID:  "run-stale",
		IdempotencyKey: "interject-stale",
	})
	if !errors.Is(err, ErrRunNotActive) {
		t.Fatalf("error = %v, want run not active", err)
	}
	if runner.steerCalls != 0 || len(mustConversationView(t, repo, "conv-1").Messages) != 1 {
		t.Fatalf("stale interjection changed state")
	}
}

func TestServiceInterjectMessageInfersActiveAgentInDirectChat(t *testing.T) {
	repo := newMemoryRepository(t)
	seedRunningDiscussionRun(t, repo, "conv-direct", "run-direct", testTime)
	view := mustConversationView(t, repo, "conv-direct")
	view.ConversationKind = "direct"
	view.DirectTargetKind = "agent"
	view.DirectTargetID = "lead-agent"
	view.DirectAgentID = "lead-agent"
	if err := repo.SaveConversation(context.Background(), view.ConversationRecord); err != nil {
		t.Fatal(err)
	}
	runner := &steeringDiscussionAgentRunner{capturingDiscussionAgentRunner: capturingDiscussionAgentRunner{agentID: "lead-agent"}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("msg-direct-interject"),
	})

	message, err := service.InterjectMessage(context.Background(), ViewerRef{
		ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户",
	}, InterjectMessageCommand{
		ConversationID: "conv-direct",
		Content:        "改用新的验收标准",
		IdempotencyKey: "interject-direct-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if runner.steerCalls != 1 || runner.steeredRun.RunID != "run-direct" || message.RunID != "run-direct" {
		t.Fatalf("message=%+v steering calls=%d run=%+v", message, runner.steerCalls, runner.steeredRun)
	}
}

func TestServiceInterjectMessageRequiresOneMentionedRunningAgent(t *testing.T) {
	repo := newMemoryRepository(t)
	seedRunningDiscussionRun(t, repo, "conv-1", "run-1", testTime)
	runner := &steeringDiscussionAgentRunner{capturingDiscussionAgentRunner: capturingDiscussionAgentRunner{agentID: "lead-agent"}}
	service := newTestService(ServiceOptions{Repository: repo, DiscussionAgent: runner, Clock: fixedClock{now: testTime}, IDGenerator: fixedIDGen("unused")})
	viewer := ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}

	_, err := service.InterjectMessage(context.Background(), viewer, InterjectMessageCommand{
		ConversationID: "conv-1",
		Content:        "改一下",
		IdempotencyKey: "interject-no-target",
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("error = %v, want invalid request", err)
	}
	if runner.steerCalls != 0 || len(mustConversationView(t, repo, "conv-1").Messages) != 1 {
		t.Fatalf("invalid interjection changed state")
	}
}

func TestServiceInterjectMessageRollsBackWhenRuntimeRejectsControl(t *testing.T) {
	repo := newMemoryRepository(t)
	seedRunningDiscussionRun(t, repo, "conv-1", "run-1", testTime)
	runner := &steeringDiscussionAgentRunner{
		capturingDiscussionAgentRunner: capturingDiscussionAgentRunner{agentID: "lead-agent"},
		steerErr:                       errors.New("runtime already finished"),
	}
	service := newTestService(ServiceOptions{Repository: repo, DiscussionAgent: runner, Clock: fixedClock{now: testTime}, IDGenerator: fixedIDGen("msg-rejected")})
	viewer := ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}

	_, err := service.InterjectMessage(context.Background(), viewer, InterjectMessageCommand{
		ConversationID: "conv-1",
		Content:        "@负责人 改一下",
		Mentions:       []MessageMentionDTO{{TargetKind: "agent", TargetID: "lead-agent"}},
		IdempotencyKey: "interject-rejected",
	})
	if !errors.Is(err, ErrRuntimeControl) {
		t.Fatalf("error = %v, want runtime control error", err)
	}
	view := mustConversationView(t, repo, "conv-1")
	if len(view.Messages) != 1 || len(view.Events) != 0 {
		t.Fatalf("rejected interjection persisted state: messages=%+v events=%+v", view.Messages, view.Events)
	}
}
