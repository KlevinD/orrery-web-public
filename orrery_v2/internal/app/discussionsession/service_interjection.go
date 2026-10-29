package discussionsession

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	discussionInterjectionDeliveryMetadataKey = "delivery"
	discussionInterjectionDelivery            = "runtime_steer"
)

// InterjectMessage persists a user's message in the active turn and steers the
// specifically mentioned Agent without waiting for that turn to finish.
func (s *Service) InterjectMessage(ctx context.Context, viewer ViewerRef, cmd InterjectMessageCommand) (MessageDTO, error) {
	if s == nil || s.repo == nil {
		return MessageDTO{}, ErrServiceDependency
	}
	cmd.ConversationID = strings.TrimSpace(cmd.ConversationID)
	cmd.Content = strings.TrimSpace(cmd.Content)
	cmd.ExpectedTurnID = strings.TrimSpace(cmd.ExpectedTurnID)
	cmd.ExpectedRunID = strings.TrimSpace(cmd.ExpectedRunID)
	cmd.IdempotencyKey = strings.TrimSpace(cmd.IdempotencyKey)
	if strings.TrimSpace(viewer.ViewerID) == "" || cmd.ConversationID == "" || cmd.Content == "" || cmd.IdempotencyKey == "" {
		return MessageDTO{}, ErrInvalidRequest
	}

	command, claimed, err := s.claimDiscussionCommand(ctx, "interject_message", viewer, cmd.IdempotencyKey, cmd)
	if err != nil {
		return MessageDTO{}, err
	}
	if !claimed {
		return s.replayDiscussionInterjection(ctx, command)
	}
	failCommand := func(cause error) {
		_ = s.failDiscussionCommand(context.WithoutCancel(ctx), command, cause)
	}

	view, err := s.requireConversationAccess(ctx, viewer, cmd.ConversationID)
	if err != nil {
		failCommand(err)
		return MessageDTO{}, err
	}
	mentions, err := normalizeMessageMentions(cmd.Mentions, view.Members)
	if err != nil {
		failCommand(err)
		return MessageDTO{}, err
	}
	target, err := discussionInterjectionTarget(view, mentions)
	if err != nil {
		failCommand(err)
		return MessageDTO{}, err
	}
	run, ok := latestRunningDiscussionRunForAgent(view.Runs, target.MemberID)
	if !ok {
		failCommand(ErrRunNotActive)
		return MessageDTO{}, ErrRunNotActive
	}
	if cmd.ExpectedTurnID != "" && strings.TrimSpace(run.TurnID) != cmd.ExpectedTurnID ||
		cmd.ExpectedRunID != "" && strings.TrimSpace(run.RunID) != cmd.ExpectedRunID {
		failCommand(ErrRunNotActive)
		return MessageDTO{}, ErrRunNotActive
	}
	steerer, err := s.discussionRunSteerer(run)
	if err != nil {
		failCommand(err)
		return MessageDTO{}, err
	}

	messageID, err := s.newID()
	if err != nil {
		failCommand(err)
		return MessageDTO{}, err
	}
	authorName, err := requiredConversationViewerName(view.Members, viewer)
	if err != nil {
		failCommand(err)
		return MessageDTO{}, err
	}
	createdAt := firstTimeAfter(s.now(), latestDiscussionOccurrenceAt(view))
	message := MessageRecord{
		MessageID:            messageID,
		ConversationID:       cmd.ConversationID,
		TurnID:               run.TurnID,
		RunID:                run.RunID,
		Role:                 "user",
		AuthorID:             viewer.ViewerID,
		AuthorName:           authorName,
		Content:              cmd.Content,
		Mentions:             mentions,
		RoutingSchemaVersion: 1,
		Metadata: map[string]string{
			discussionInterjectionDeliveryMetadataKey: discussionInterjectionDelivery,
			"target_agent_id":                         target.MemberID,
			"target_run_id":                           run.RunID,
		},
		CreatedAt: createdAt,
	}
	event := EventRecord{
		EventID:        "discussion-run-interjected-" + messageID,
		ConversationID: cmd.ConversationID,
		Kind:           "discussion_run_interjected",
		Status:         "accepted",
		Summary:        "用户在 Agent 工作过程中插入了消息",
		Metadata: map[string]string{
			"message_id":      messageID,
			"run_id":          run.RunID,
			"target_agent_id": target.MemberID,
			"interjected_by":  viewer.ViewerID,
		},
		CreatedAt: firstTimeAfter(createdAt, createdAt),
	}
	if err := s.repo.SaveMessage(ctx, message); err != nil {
		failCommand(err)
		return MessageDTO{}, err
	}
	if err := s.repo.SaveEvent(ctx, event); err != nil {
		_ = s.repo.DeleteMessage(context.WithoutCancel(ctx), messageID)
		failCommand(err)
		return MessageDTO{}, err
	}
	if err := steerer.SteerDiscussionRun(ctx, run, cmd.Content); err != nil {
		_ = s.repo.DeleteEvent(context.WithoutCancel(ctx), event.EventID)
		_ = s.repo.DeleteMessage(context.WithoutCancel(ctx), messageID)
		controlErr := fmt.Errorf("%w: %v", ErrRuntimeControl, err)
		failCommand(controlErr)
		return MessageDTO{}, controlErr
	}
	if err := s.completeDiscussionCommand(context.WithoutCancel(ctx), command, cmd.ConversationID, messageID, run.TurnID); err != nil {
		return MessageDTO{}, err
	}
	if err := s.appendRealtimeMessageCompleted(ctx, message); err != nil {
		return MessageDTO{}, err
	}
	return mapMessages([]MessageRecord{message})[0], nil
}

func discussionInterjectionTarget(view ConversationRecordView, mentions []MessageMentionRecord) (ConversationMemberRecord, error) {
	agents := make([]MessageMentionRecord, 0, len(mentions))
	for _, mention := range mentions {
		if strings.TrimSpace(mention.TargetKind) == "agent" {
			agents = append(agents, mention)
		}
	}
	if strings.TrimSpace(view.ConversationKind) == "direct" {
		target, ok := directConversationTargetMember(view)
		if !ok || strings.TrimSpace(target.MemberKind) != "agent" {
			return ConversationMemberRecord{}, ErrInvalidRequest
		}
		if len(agents) > 1 || len(agents) == 1 && strings.TrimSpace(agents[0].TargetID) != strings.TrimSpace(target.MemberID) {
			return ConversationMemberRecord{}, ErrInvalidRequest
		}
		return target, nil
	}
	if len(agents) != 1 {
		return ConversationMemberRecord{}, fmt.Errorf("%w: mention exactly one working Agent", ErrInvalidRequest)
	}
	target, ok := conversationAgentMemberByID(view.Members, agents[0].TargetID)
	if !ok {
		return ConversationMemberRecord{}, ErrForbidden
	}
	return target, nil
}

func latestRunningDiscussionRunForAgent(runs []RunRecord, agentID string) (RunRecord, bool) {
	agentID = strings.TrimSpace(agentID)
	for i := len(runs) - 1; i >= 0; i-- {
		if strings.TrimSpace(runs[i].AgentID) == agentID && runningDiscussionStatus(runs[i].Status) {
			return runs[i], true
		}
	}
	return RunRecord{}, false
}

func (s *Service) discussionRunSteerer(run RunRecord) (DiscussionAgentRunSteerer, error) {
	leadID := s.discussionAgentID()
	if strings.TrimSpace(run.AgentID) == leadID {
		if steerer, ok := s.discussionAgent.(DiscussionAgentRunSteerer); ok {
			return steerer, nil
		}
	}
	if steerer, ok := s.mentionResponder.(DiscussionAgentRunSteerer); ok {
		return steerer, nil
	}
	if steerer, ok := s.discussionAgent.(DiscussionAgentRunSteerer); ok {
		return steerer, nil
	}
	return nil, ErrRuntimeControl
}

func (s *Service) replayDiscussionInterjection(ctx context.Context, command CommandRecord) (MessageDTO, error) {
	view, err := s.repo.GetConversation(ctx, command.ConversationID)
	if err != nil {
		return MessageDTO{}, err
	}
	for _, message := range view.Messages {
		if message.MessageID == command.MessageID {
			return mapMessagesWithAttachments([]MessageRecord{message}, view.Attachments)[0], nil
		}
	}
	return MessageDTO{}, ErrNotFound
}

func latestDiscussionOccurrenceAt(view ConversationRecordView) time.Time {
	latest := latestTime(view.CreatedAt, view.UpdatedAt)
	for _, message := range view.Messages {
		latest = latestTime(latest, message.CreatedAt)
	}
	for _, run := range view.Runs {
		latest = latestTime(latest, run.CreatedAt, run.StartedAt, run.CompletedAt)
	}
	for _, event := range view.Events {
		latest = latestTime(latest, event.CreatedAt)
	}
	return latest
}
