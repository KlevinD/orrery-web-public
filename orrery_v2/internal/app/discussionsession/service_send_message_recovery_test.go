package discussionsession

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type legacySendMessageCommandV1ForTest struct {
	ConversationID       string
	ConversationKind     string
	MissionID            string
	MissionTaskID        string
	Content              string
	DirectAgentID        string
	DirectTargetKind     string
	DirectTargetID       string
	InitialMemberIDs     []string
	AllowMemberAdditions *bool
	AttachmentIDs        []string
	Mentions             []MessageMentionDTO
	IdempotencyKey       string
}

type commandProgressInspectingRunner struct {
	streamingDiscussionAgentRunner
	repo          *Repository
	viewer        ViewerRef
	command       SendMessageCommand
	observed      bool
	inspectionErr error
}

func TestRetryableSendMessageCommandIsBoundedAndExcludesClientFailures(t *testing.T) {
	tests := []struct {
		name    string
		command CommandRecord
		want    bool
	}{
		{name: "internal", command: CommandRecord{Status: discussionCommandFailed, ErrorCode: "internal"}, want: true},
		{name: "runtime unavailable", command: CommandRecord{Status: discussionCommandFailed, ErrorCode: "runtime_unavailable"}, want: true},
		{name: "invalid request", command: CommandRecord{Status: discussionCommandFailed, ErrorCode: "invalid_request"}},
		{name: "cancelled", command: CommandRecord{Status: discussionCommandFailed, ErrorCode: "cancelled"}},
		{name: "retry limit", command: CommandRecord{Status: discussionCommandFailed, ErrorCode: "internal", RetryCount: maxSendMessageRetries}},
		{name: "not failed", command: CommandRecord{Status: discussionCommandPending, ErrorCode: "internal"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := retryableSendMessageCommand(tt.command); got != tt.want {
				t.Fatalf("retryableSendMessageCommand(%+v) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}

func (r *commandProgressInspectingRunner) RunDiscussionStream(ctx context.Context, request DiscussionAgentRequest) (<-chan DiscussionAgentStreamEvent, error) {
	stored, err := r.repo.GetCommand(ctx, "send_message", r.viewer, r.command.IdempotencyKey)
	if err != nil {
		r.inspectionErr = err
		return nil, err
	}
	if stored.Status != discussionCommandPending || stored.ConversationID != request.ConversationID || stored.MessageID != request.MessageID || stored.TurnID == "" {
		r.inspectionErr = fmt.Errorf("runtime observed command without durable progress: %+v request=%+v", stored, request)
		return nil, r.inspectionErr
	}
	r.observed = true
	return r.streamingDiscussionAgentRunner.RunDiscussionStream(ctx, request)
}

func TestServicePersistsSendMessageProgressBeforeRuntimeStarts(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	viewer := ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, TenantID: "tenant-a", DisplayName: "User"}
	cmd := SendMessageCommand{Content: "Plan the work", IdempotencyKey: "send-progress-1"}
	runner := &commandProgressInspectingRunner{
		repo: repo, viewer: viewer, command: cmd,
		streamingDiscussionAgentRunner: streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{{
			Kind:     DiscussionAgentStreamRunCompleted,
			Response: DiscussionAgentResponse{AssistantMessage: "Done", Status: "completed", RuntimeKind: "managed"},
		}}},
	}
	service := newTestService(ServiceOptions{
		Repository: repo, DiscussionAgent: runner, Clock: fixedClock{now: testTime},
		IDGenerator: fixedIDGen("conversation-1", "message-user", "run-1", "message-assistant"),
	})
	stream, err := service.SendMessageStream(ctx, viewer, cmd)
	if err != nil {
		t.Fatal(err)
	}
	for range stream {
	}
	if runner.inspectionErr != nil || !runner.observed {
		t.Fatalf("runtime progress inspection: observed=%v err=%v", runner.observed, runner.inspectionErr)
	}
}

func TestServiceSendMessageStreamRecoversLegacyPendingCommandWithoutDuplicateUserMessage(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	viewer := ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, TenantID: "tenant-a", OwnerID: "owner-1", DisplayName: "User"}
	cmd := SendMessageCommand{ConversationKind: ConversationKindPersonalAssistant, Content: "Continue the work", IdempotencyKey: "gateway-message-1"}
	staleCommand, conversationID, source := seedLegacyPendingSendMessage(t, repo, viewer, cmd)

	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{
		{Kind: DiscussionAgentStreamTokenDelta, Delta: "Recovered response"},
		{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
			AssistantMessage: "Recovered response", Status: "completed", RuntimeKind: "managed",
		}},
	}}
	restarted := newTestService(ServiceOptions{
		Repository: repo, DiscussionAgent: runner,
		Clock: fixedClock{now: testTime.Add(41 * time.Minute)}, TurnRecoveryTimeout: 40 * time.Minute,
		IDGenerator: fixedIDGen("run-recovered", "message-assistant"),
	})

	stream, err := restarted.SendMessageStream(ctx, viewer, cmd)
	if err != nil {
		t.Fatalf("recover pending send message: %v", err)
	}
	var text strings.Builder
	for event := range stream {
		if event.Kind == StreamEventError {
			t.Fatalf("recovery stream error: %s", event.Error)
		}
		if event.Kind == StreamEventTokenDelta {
			text.WriteString(event.Delta)
		}
	}
	if text.String() != "Recovered response" {
		t.Fatalf("streamed text = %q", text.String())
	}
	if runner.request.MessageID != source.MessageID || !runner.request.ForceNewSession {
		t.Fatalf("recovery request = %+v", runner.request)
	}

	current, err := repo.GetConversation(ctx, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	userMessages := 0
	assistantMessages := 0
	for _, message := range current.Messages {
		switch strings.ToLower(strings.TrimSpace(message.Role)) {
		case "user":
			userMessages++
		case "assistant":
			assistantMessages++
		}
	}
	if userMessages != 1 || assistantMessages != 1 {
		t.Fatalf("messages after recovery: users=%d assistants=%d all=%+v", userMessages, assistantMessages, current.Messages)
	}
	oldTurn, err := repo.GetTurn(ctx, source.TurnID)
	if err != nil || oldTurn.Status != "failed" || oldTurn.ErrorCode != "executor_interrupted" {
		t.Fatalf("old turn = %+v err=%v", oldTurn, err)
	}
	stored, err := repo.GetCommand(ctx, staleCommand.Operation, viewer, cmd.IdempotencyKey)
	if err != nil || stored.Status != discussionCommandCompleted || stored.ConversationID != conversationID || stored.MessageID != source.MessageID || stored.TurnID == "" || stored.TurnID == source.TurnID {
		t.Fatalf("recovered command = %+v err=%v", stored, err)
	}
}

func TestServiceSendMessageStreamRecoversLegacyPersonalAssistantFingerprint(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	viewer := ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, TenantID: "tenant-a", OwnerID: "owner-1", DisplayName: "User"}
	cmd := SendMessageCommand{ConversationKind: ConversationKindPersonalAssistant, Content: "Continue the work", IdempotencyKey: "gateway-message-1"}
	command, conversationID, source := seedLegacyPendingSendMessage(t, repo, viewer, cmd)
	command.Fingerprint = legacySendMessageFingerprintForTest(t, cmd, conversationID)
	if err := repo.SaveCommand(ctx, command); err != nil {
		t.Fatal(err)
	}

	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{{
		Kind:     DiscussionAgentStreamRunCompleted,
		Response: DiscussionAgentResponse{AssistantMessage: "Recovered legacy command", Status: "completed", RuntimeKind: "managed"},
	}}}
	restarted := newTestService(ServiceOptions{
		Repository: repo, DiscussionAgent: runner,
		Clock: fixedClock{now: testTime.Add(41 * time.Minute)}, TurnRecoveryTimeout: 40 * time.Minute,
		IDGenerator: fixedIDGen("run-recovered", "message-assistant"),
	})
	stream, err := restarted.SendMessageStream(ctx, viewer, cmd)
	if err != nil {
		t.Fatalf("recover legacy personal-assistant fingerprint: %v", err)
	}
	for event := range stream {
		if event.Kind == StreamEventError {
			t.Fatalf("legacy fingerprint recovery stream error: %s", event.Error)
		}
	}
	if runner.request.MessageID != source.MessageID || !runner.request.ForceNewSession {
		t.Fatalf("legacy fingerprint recovery request = %+v", runner.request)
	}
	stored, err := repo.GetCommand(ctx, command.Operation, viewer, cmd.IdempotencyKey)
	if err != nil || stored.Status != discussionCommandCompleted || stored.ConversationID != conversationID || stored.MessageID != source.MessageID {
		t.Fatalf("legacy fingerprint command = %+v err=%v", stored, err)
	}
}

func TestServiceSendMessageStreamRejectsUnrelatedLegacyFingerprint(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	viewer := ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, TenantID: "tenant-a", OwnerID: "owner-1", DisplayName: "User"}
	cmd := SendMessageCommand{ConversationKind: ConversationKindPersonalAssistant, Content: "Continue the work", IdempotencyKey: "gateway-message-1"}
	command, conversationID, _ := seedLegacyPendingSendMessage(t, repo, viewer, cmd)
	command.Fingerprint = legacySendMessageFingerprintForTest(t, cmd, conversationID+"-other")
	if err := repo.SaveCommand(ctx, command); err != nil {
		t.Fatal(err)
	}
	runner := &streamingDiscussionAgentRunner{}
	restarted := newTestService(ServiceOptions{
		Repository: repo, DiscussionAgent: runner,
		Clock: fixedClock{now: testTime.Add(41 * time.Minute)}, TurnRecoveryTimeout: 40 * time.Minute,
	})

	_, err := restarted.SendMessageStream(ctx, viewer, cmd)
	if !errors.Is(err, ErrIdempotencyMismatch) {
		t.Fatalf("unrelated legacy fingerprint error = %v", err)
	}
	stored, getErr := repo.GetCommand(ctx, command.Operation, viewer, cmd.IdempotencyKey)
	if getErr != nil || stored.Status != discussionCommandPending || !stored.UpdatedAt.Equal(command.UpdatedAt) {
		t.Fatalf("unrelated command changed = %+v err=%v", stored, getErr)
	}
	if runner.request.MessageID != "" {
		t.Fatalf("unrelated legacy fingerprint started runtime: %+v", runner.request)
	}
}

func TestServiceSendMessageStreamRecoversStaleRecoveryTurnAfterSecondRestart(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	viewer := ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, TenantID: "tenant-a", OwnerID: "owner-1", DisplayName: "User"}
	cmd := SendMessageCommand{ConversationKind: ConversationKindPersonalAssistant, Content: "Continue the work", IdempotencyKey: "gateway-message-1"}
	command, conversationID, source := seedLegacyPendingSendMessage(t, repo, viewer, cmd)

	firstRecoveryAt := testTime.Add(41 * time.Minute)
	recovered, err := repo.FailStaleTurn(ctx, conversationID, source.TurnID, firstRecoveryAt.Add(-40*time.Minute), firstRecoveryAt)
	if err != nil || !recovered {
		t.Fatalf("close first stale turn: recovered=%v err=%v", recovered, err)
	}
	current, err := repo.GetConversation(ctx, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	recoveryKey := sendMessageRecoveryIdempotencyKey(command)
	recoveryTurn := TurnRecord{
		TurnID:         stableDiscussionTurnID(DiscussionTurnOriginRetry, conversationID, source.MessageID, recoveryKey),
		ConversationID: conversationID, RootMessageID: source.MessageID, IdempotencyKey: recoveryKey,
		Mode: string(discussionTurnTargetGroup), Status: "running",
		StartedAt: firstRecoveryAt, CreatedAt: firstRecoveryAt, UpdatedAt: firstRecoveryAt,
	}
	recoveryRun := RunRecord{
		RunID: "run-first-recovery", ConversationID: conversationID, TurnID: recoveryTurn.TurnID,
		AgentID: "lead-agent", Status: "running", StartedAt: firstRecoveryAt, CreatedAt: firstRecoveryAt,
	}
	current.Status = "running"
	current.UpdatedAt = firstRecoveryAt
	if _, err := repo.ApplyTurnMutation(ctx, TurnAggregateMutation{
		Conversation: current.ConversationRecord, ExpectedRevision: current.Revision,
		Turn: &recoveryTurn, Runs: []RunRecord{recoveryRun}, RequireNoActiveTurn: true,
	}); err != nil {
		t.Fatal(err)
	}
	command.ConversationID = conversationID
	command.MessageID = source.MessageID
	command.TurnID = recoveryTurn.TurnID
	command.UpdatedAt = firstRecoveryAt
	if err := repo.SavePendingCommandProgress(ctx, command); err != nil {
		t.Fatal(err)
	}

	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{{
		Kind:     DiscussionAgentStreamRunCompleted,
		Response: DiscussionAgentResponse{AssistantMessage: "Recovered after a second restart", Status: "completed", RuntimeKind: "managed"},
	}}}
	restarted := newTestService(ServiceOptions{
		Repository: repo, DiscussionAgent: runner,
		Clock: fixedClock{now: firstRecoveryAt.Add(41 * time.Minute)}, TurnRecoveryTimeout: 40 * time.Minute,
		IDGenerator: fixedIDGen("run-second-recovery", "message-assistant"),
	})
	stream, err := restarted.SendMessageStream(ctx, viewer, cmd)
	if err != nil {
		t.Fatalf("recover second pending attempt: %v", err)
	}
	for event := range stream {
		if event.Kind == StreamEventError {
			t.Fatalf("second recovery stream error: %s", event.Error)
		}
	}

	current, err = repo.GetConversation(ctx, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	userMessages := 0
	assistantMessages := 0
	for _, message := range current.Messages {
		switch strings.ToLower(strings.TrimSpace(message.Role)) {
		case "user":
			userMessages++
		case "assistant":
			assistantMessages++
		}
	}
	if userMessages != 1 || assistantMessages != 1 {
		t.Fatalf("messages after second recovery: users=%d assistants=%d all=%+v", userMessages, assistantMessages, current.Messages)
	}
	stored, err := repo.GetCommand(ctx, command.Operation, viewer, cmd.IdempotencyKey)
	if err != nil || stored.Status != discussionCommandCompleted || stored.TurnID != recoveryTurn.TurnID {
		t.Fatalf("command after second recovery = %+v err=%v", stored, err)
	}
}

func TestServiceSendMessageStreamRetriesFailedCommandWithoutDuplicateUserMessage(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	viewer := ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, TenantID: "tenant-a", OwnerID: "owner-1", DisplayName: "User"}
	cmd := SendMessageCommand{ConversationKind: ConversationKindPersonalAssistant, Content: "Use the knowledge base", IdempotencyKey: "gateway-message-failed"}
	command, conversationID, source := seedLegacyPendingSendMessage(t, repo, viewer, cmd)
	command.ConversationID = conversationID
	command.MessageID = source.MessageID
	command.TurnID = source.TurnID
	command.UpdatedAt = testTime.Add(time.Second)
	if err := repo.SavePendingCommandProgress(ctx, command); err != nil {
		t.Fatal(err)
	}
	if recovered, err := repo.FailStaleTurn(ctx, conversationID, source.TurnID, testTime.Add(time.Hour), testTime.Add(time.Minute)); err != nil || !recovered {
		t.Fatalf("fail original turn: recovered=%v err=%v", recovered, err)
	}
	command.Status = discussionCommandFailed
	command.ErrorCode = "internal"
	command.ErrorMessage = "knowledge provider temporarily unavailable"
	command.UpdatedAt = testTime.Add(time.Minute)
	if err := repo.TransitionCommand(ctx, command, discussionCommandPending); err != nil {
		t.Fatal(err)
	}

	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{{
		Kind:     DiscussionAgentStreamRunCompleted,
		Response: DiscussionAgentResponse{AssistantMessage: "Recovered with knowledge", Status: "completed", RuntimeKind: "managed"},
	}}}
	restarted := newTestService(ServiceOptions{
		Repository: repo, DiscussionAgent: runner, Clock: fixedClock{now: testTime.Add(2 * time.Minute)},
		TurnRecoveryTimeout: 40 * time.Minute, IDGenerator: fixedIDGen("run-recovered", "message-assistant"),
	})
	stream, err := restarted.SendMessageStream(ctx, viewer, cmd)
	if err != nil {
		t.Fatalf("retry failed send message: %v", err)
	}
	for event := range stream {
		if event.Kind == StreamEventError {
			t.Fatalf("retry stream error: %s", event.Error)
		}
	}

	current, err := repo.GetConversation(ctx, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	userMessages := 0
	assistantMessages := 0
	for _, message := range current.Messages {
		switch strings.ToLower(strings.TrimSpace(message.Role)) {
		case "user":
			userMessages++
		case "assistant":
			assistantMessages++
		}
	}
	if userMessages != 1 || assistantMessages != 1 {
		t.Fatalf("messages after failed-command retry: users=%d assistants=%d all=%+v", userMessages, assistantMessages, current.Messages)
	}
	stored, err := repo.GetCommand(ctx, command.Operation, viewer, cmd.IdempotencyKey)
	if err != nil || stored.Status != discussionCommandCompleted || stored.RetryCount != 1 || stored.MessageID != source.MessageID {
		t.Fatalf("retried command = %+v err=%v", stored, err)
	}
}

func TestServiceSendMessageKeepsFreshPendingCommandFenced(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	viewer := ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, TenantID: "tenant-a", OwnerID: "owner-1", DisplayName: "User"}
	cmd := SendMessageCommand{ConversationKind: ConversationKindPersonalAssistant, Content: "Continue the work", IdempotencyKey: "gateway-message-1"}
	seedLegacyPendingSendMessage(t, repo, viewer, cmd)
	runner := &streamingDiscussionAgentRunner{}
	restarted := newTestService(ServiceOptions{
		Repository: repo, DiscussionAgent: runner,
		Clock: fixedClock{now: testTime.Add(39 * time.Minute)}, TurnRecoveryTimeout: 40 * time.Minute,
	})

	_, err := restarted.SendMessageStream(ctx, viewer, cmd)
	if !errors.Is(err, ErrCommandInProgress) {
		t.Fatalf("fresh pending command error = %v", err)
	}
	if runner.request.MessageID != "" {
		t.Fatalf("fresh pending command started runtime: %+v", runner.request)
	}
}

func seedLegacyPendingSendMessage(t *testing.T, repo *Repository, viewer ViewerRef, cmd SendMessageCommand) (CommandRecord, string, MessageRecord) {
	t.Helper()
	ctx := context.Background()
	setup := newTestService(ServiceOptions{
		Repository: repo, DiscussionAgent: &capturingDiscussionAgentRunner{}, Clock: fixedClock{now: testTime},
		IDGenerator: fixedIDGen("conversation-stale", "message-user"),
	})
	command, claimed, err := setup.claimDiscussionCommand(ctx, "send_message", viewer, cmd.IdempotencyKey, cmd)
	if err != nil || !claimed {
		t.Fatalf("claim legacy command = %+v claimed=%v err=%v", command, claimed, err)
	}
	conversationID, source, err := setup.beginUserMessage(ctx, viewer, cmd)
	if err != nil {
		t.Fatal(err)
	}
	current, err := repo.GetConversation(ctx, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	turn := TurnRecord{
		TurnID: source.TurnID, ConversationID: conversationID, RootMessageID: source.MessageID,
		IdempotencyKey: cmd.IdempotencyKey, Mode: string(discussionTurnTargetGroup), Status: "running",
		StartedAt: testTime.Add(time.Nanosecond), CreatedAt: testTime.Add(time.Nanosecond), UpdatedAt: testTime.Add(time.Nanosecond),
	}
	run := RunRecord{
		RunID: "run-stale", ConversationID: conversationID, TurnID: turn.TurnID,
		AgentID: "lead-agent", Status: "running", StartedAt: testTime.Add(2 * time.Nanosecond), CreatedAt: testTime.Add(2 * time.Nanosecond),
	}
	current.Status = "running"
	current.UpdatedAt = testTime.Add(2 * time.Nanosecond)
	if _, err := repo.ApplyTurnMutation(ctx, TurnAggregateMutation{
		Conversation: current.ConversationRecord, ExpectedRevision: current.Revision, Turn: &turn, Runs: []RunRecord{run},
	}); err != nil {
		t.Fatal(err)
	}
	return command, conversationID, source
}

func legacySendMessageFingerprintForTest(t *testing.T, cmd SendMessageCommand, conversationID string) string {
	t.Helper()
	fingerprint, err := discussionCommandFingerprint("send_message", legacySendMessageCommandV1ForTest{
		ConversationID:       conversationID,
		ConversationKind:     cmd.ConversationKind,
		MissionID:            cmd.MissionID,
		MissionTaskID:        cmd.MissionTaskID,
		Content:              cmd.Content,
		DirectAgentID:        cmd.DirectAgentID,
		DirectTargetKind:     cmd.DirectTargetKind,
		DirectTargetID:       cmd.DirectTargetID,
		InitialMemberIDs:     cmd.InitialMemberIDs,
		AllowMemberAdditions: cmd.AllowMemberAdditions,
		AttachmentIDs:        cmd.AttachmentIDs,
		Mentions:             cmd.Mentions,
		IdempotencyKey:       cmd.IdempotencyKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	return fingerprint
}
