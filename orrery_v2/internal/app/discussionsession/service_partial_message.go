package discussionsession

import (
	"context"
	"strings"
	"time"
)

const discussionPartialMessageMetadataKey = "stream_partial"

func (s *Service) persistPartialAssistantMessage(ctx context.Context, conversationID string, run RunRecord, response DiscussionAgentResponse, at time.Time, source *MessageRecord, target *ConversationMemberRecord) (*MessageDTO, error) {
	if s == nil || s.repo == nil || strings.TrimSpace(response.AssistantMessage) == "" {
		return nil, nil
	}
	current, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	record, ok, err := partialAssistantMessageRecord(current, run, response, at, source, target)
	if err != nil || !ok {
		return nil, err
	}
	if err := s.repo.SaveMessage(ctx, record); err != nil {
		return nil, err
	}
	if err := s.appendRealtimeMessageCompleted(ctx, record); err != nil {
		return nil, err
	}
	dto := mapMessages([]MessageRecord{record})[0]
	return &dto, nil
}

func partialAssistantMessageRecord(current ConversationRecordView, run RunRecord, response DiscussionAgentResponse, at time.Time, source *MessageRecord, target *ConversationMemberRecord) (MessageRecord, bool, error) {
	if strings.TrimSpace(response.AssistantMessage) == "" {
		return MessageRecord{}, false, nil
	}
	for _, message := range current.Messages {
		if message.Role == "assistant" && strings.TrimSpace(message.RunID) == strings.TrimSpace(run.RunID) {
			return MessageRecord{}, false, nil
		}
	}

	authorID, authorName, err := requiredConversationRunMessageAuthor(current, run)
	if err != nil {
		return MessageRecord{}, false, err
	}
	viewer := runHumanViewer(current.Messages, run)
	content := response.AssistantMessage
	if target != nil && !isPersonalAssistantApprovalHost(current, string(run.Mode), *target, run) {
		authorID = strings.TrimSpace(target.MemberID)
		authorName = strings.TrimSpace(target.DisplayName)
		if authorName == "" {
			authorName, err = requiredConversationMemberName(current.Members, "agent", authorID)
			if err != nil {
				return MessageRecord{}, false, err
			}
		}
	}
	if source != nil {
		viewer = ViewerRef{ViewerID: source.AuthorID, ViewerKind: ViewerKindUser, DisplayName: source.AuthorName}
	} else if strings.EqualFold(strings.TrimSpace(run.Mode), string(discussionTurnTargetGroup)) {
		content = conversationAssistantReplyContent(current, run, content)
	}

	record := correlateMessageToRun(assistantMessageRecordWithDisplayTimeline(
		"discussion-partial-message-"+run.RunID,
		current.ConversationID,
		authorID,
		authorName,
		content,
		response,
		firstTimeAfter(at, run.StartedAt),
		viewer,
	), run)
	if record.Metadata == nil {
		record.Metadata = map[string]string{}
	}
	record.Metadata[discussionPartialMessageMetadataKey] = "true"
	return record, true, nil
}
