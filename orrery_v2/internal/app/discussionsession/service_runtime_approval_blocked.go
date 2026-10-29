package discussionsession

import (
	"context"
	"strings"
	"time"

	runtimecore "orrery_v2/internal/agent/runtime/core"
)

func discussionRunIsBlockedForRuntimeApproval(run RunRecord, response DiscussionAgentResponse) bool {
	if !strings.EqualFold(strings.TrimSpace(run.Status), "blocked") && !strings.EqualFold(strings.TrimSpace(response.Status), "blocked") {
		return false
	}
	metadata := discussionRuntimeApprovalMetadata(run.ConversationID, run, response)
	return strings.TrimSpace(metadata[runtimecore.ApprovalMetadataRequestID]) != ""
}

func (s *Service) finishBlockedRuntimeApprovalRun(ctx context.Context, conversationID string, current ConversationRecordView, run RunRecord, response DiscussionAgentResponse, completedAt time.Time, workspace DiscussionWorkspaceContext) (*MessageDTO, *RunDTO, error) {
	var assistantMessage *MessageDTO
	if strings.TrimSpace(response.AssistantMessage) != "" {
		assistantMessageID, err := s.newID()
		if err != nil {
			return nil, nil, err
		}
		currentViewer := runHumanViewer(current.Messages, run)
		assistantAuthorID, assistantAuthorName, err := requiredConversationRunMessageAuthor(current, run)
		if err != nil {
			return nil, nil, err
		}
		content := conversationAssistantReplyContent(current, run, response.AssistantMessage)
		record := correlateMessageToRun(assistantMessageRecordWithDisplayTimeline(assistantMessageID, conversationID, assistantAuthorID, assistantAuthorName, content, response, completedAt, currentViewer), run)
		dto, err := s.persistAssistantMessageWithWorkspaceFiles(ctx, record, workspace)
		if err != nil {
			return nil, nil, err
		}
		assistantMessage = &dto
	}
	if err := s.recordEvaluationReviewUsage(ctx, run); err != nil {
		return nil, nil, err
	}
	if err := s.appendRealtimeRunCompleted(ctx, run); err != nil {
		return nil, nil, err
	}
	runDTO := mapRuns([]RunRecord{run})[0]
	return assistantMessage, &runDTO, nil
}
