package discussionsession

import (
	"context"
	"fmt"
	"strings"
)

func (s *Service) capturePersonalAssistantMemoryExchange(
	ctx context.Context,
	conversation ConversationRecordView,
	run RunRecord,
	request DiscussionAgentRequest,
	source MessageRecord,
	assistant MessageDTO,
) error {
	if s == nil || !isPersonalAssistantRuntimeRun(run) {
		return nil
	}
	capturer, ok := s.memory.(PersonalAssistantMemoryCapturePort)
	if !ok || capturer == nil {
		return nil
	}
	ownerID := firstNonBlank(personalAssistantRuntimeOwner(conversation.ConversationRecord), request.PersonalAssistantOriginOwnerID, request.ConversationOwnerID, request.Viewer.OwnerID, request.Viewer.ViewerID)
	tenantID := firstNonBlank(conversation.TenantID, request.ConversationTenantID, request.Viewer.TenantID, "global")
	expectedAgentID := PersonalAssistantAgentID(tenantID, ownerID)
	logicalAgentID := firstNonBlank(request.LogicalAgentID, run.LogicalAgentID, expectedAgentID)
	if expectedAgentID == "" || logicalAgentID != expectedAgentID {
		return fmt.Errorf("personal assistant memory identity does not match conversation owner")
	}
	assistantContent := strings.TrimSpace(assistant.Content)
	if assistantContent == "" {
		return nil
	}
	return capturer.CapturePersonalAssistantMemoryExchange(ctx, PersonalAssistantMemoryExchange{
		PreferenceExtractionAllowed: isPersonalAssistantConversationKind(conversation.ConversationKind) && source.Role == "user",
		TenantID:                    tenantID,
		OwnerID:                     ownerID,
		AgentID:                     logicalAgentID,
		ConversationID:              conversation.ConversationID,
		RunID:                       run.RunID,
		UserMessageID:               source.MessageID,
		AssistantMessageID:          assistant.MessageID,
		UserContent:                 firstNonBlank(source.Content, request.Content, request.TriggerMessage.Content),
		AssistantContent:            assistantContent,
		OccurredAt:                  firstTimeAfter(assistant.CreatedAt, run.StartedAt),
	})
}
