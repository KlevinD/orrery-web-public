package discussionsession

import (
	"fmt"
	"strings"
)

func discussionAgentRequestFromConversation(view ConversationRecordView, run RunRecord, viewer ViewerRef, message MessageRecord, history []MessageDTO) DiscussionAgentRequest {
	attachmentDTOs := mapAttachments(view.Attachments)
	currentAttachments := attachmentsForMessage(view.Attachments, message.MessageID)
	attachDTOAttachments(history, attachmentDTOs)
	request := DiscussionAgentRequest{
		ConversationID:                   view.ConversationID,
		ConversationKind:                 view.ConversationKind,
		ConversationOwnerID:              firstNonBlank(view.OwnerID, viewer.OwnerID, view.CreatedBy),
		ConversationTenantID:             view.TenantID,
		HILMode:                          normalizeDiscussionHILMode(view.HILMode),
		PersonalAssistantOriginOwnerID:   view.PersonalAssistantOriginOwnerID,
		PersonalAssistantOriginSubjectID: view.PersonalAssistantOriginSubjectID,
		MessageID:                        message.MessageID,
		MissionID:                        firstNonBlank(message.MissionID, run.MissionID),
		MissionTaskID:                    firstNonBlank(message.MissionTaskID, run.MissionTaskID),
		RunID:                            run.RunID,
		AgentID:                          run.AgentID,
		LogicalAgentID:                   run.LogicalAgentID,
		Viewer:                           viewer,
		OutputLanguage:                   normalizeDiscussionOutputLanguage(viewer.Language),
		Content:                          message.Content,
		TriggerMessage:                   triggerMessageDTOFromRecord(message),
		History:                          history,
		Runs:                             append([]RunRecord(nil), view.Runs...),
		Members:                          mapConversationMembers(view.Members),
		LinkedMissions:                   mapMissionLinks(view.LinkedMissions),
		Attachments:                      currentAttachments,
	}
	request.TriggerMessage.Attachments = append([]AttachmentDTO(nil), currentAttachments...)
	applyConversationContextToAgentRequest(&request, view)
	ensurePersonalAssistantRequestIdentity(&request)
	return request
}

func discussionAgentRequestFromMentionRequest(request DiscussionAgentMentionRequest) DiscussionAgentRequest {
	trigger := request.SourceMessage
	out := DiscussionAgentRequest{
		ConversationID:       request.ConversationID,
		ConversationKind:     request.ConversationKind,
		ConversationOwnerID:  request.ConversationOwnerID,
		ConversationTenantID: request.ConversationTenantID,
		HILMode:              request.HILMode,
		MessageID:            firstNonBlank(trigger.MessageID, request.RunID),
		MissionID:            trigger.MissionID,
		MissionTaskID:        trigger.MissionTaskID,
		RunID:                request.RunID,
		AgentID:              request.TargetAgent.ID,
		OutputLanguage:       normalizeDiscussionOutputLanguage(request.OutputLanguage),
		Content:              firstNonBlank(trigger.Content, request.Content),
		TriggerMessage:       trigger,
		History:              discussionHistoryWithoutMessage(request.History, trigger.MessageID),
		Runs:                 append([]RunRecord(nil), request.Runs...),
		Members:              append([]ConversationMemberDTO(nil), request.Members...),
		LinkedMissions:       append([]LinkedMissionDTO(nil), request.LinkedMissions...),
		Attachments:          append([]AttachmentDTO(nil), request.Attachments...),
		BoundTools:           append([]DiscussionAgentBoundTool(nil), request.BoundTools...),
		BoundToolsPolicy:     request.BoundToolsPolicy,
		ProviderSpecific:     cloneStringMap(request.ProviderSpecific),
		ResumeSessionID:      request.ResumeSessionID,
		ForceNewSession:      request.ForceNewSession,
		Mode:                 request.Mode,
	}
	if request.PersonalAssistantHost {
		out.PersonalAssistantOriginOwnerID = request.PersonalAssistantOriginOwnerID
		out.PersonalAssistantOriginSubjectID = request.PersonalAssistantOriginSubjectID
		out.LogicalAgentID = request.LogicalAgentID
		out.Viewer = ViewerRef{
			ViewerID:       firstNonBlank(request.PersonalAssistantOriginSubjectID, request.SourceMessage.AuthorID),
			OwnerID:        firstNonBlank(request.PersonalAssistantOriginOwnerID, request.ConversationOwnerID),
			AgentProfileID: firstNonBlank(request.PersonalAssistantOriginSubjectID, request.SourceMessage.AuthorID),
			ViewerKind:     ViewerKindUser,
			DisplayName:    request.SourceMessage.AuthorName,
			TenantID:       request.ConversationTenantID,
		}
		ensurePersonalAssistantRequestIdentity(&out)
	}
	if len(out.Members) == 0 && strings.TrimSpace(request.TargetAgent.ID) != "" {
		out.Members = append(out.Members, request.TargetAgent)
	}
	return out
}

func discussionHistoryWithoutMessage(messages []MessageDTO, messageID string) []MessageDTO {
	messageID = strings.TrimSpace(messageID)
	out := make([]MessageDTO, 0, len(messages))
	for _, message := range messages {
		if messageID != "" && strings.EqualFold(strings.TrimSpace(message.MessageID), messageID) {
			continue
		}
		if isDiscussionToolHistoryMessage(message) {
			continue
		}
		out = append(out, message)
	}
	return out
}

func isDiscussionToolHistoryMessage(message MessageDTO) bool {
	switch strings.ToLower(strings.TrimSpace(message.Role)) {
	case "tool":
		return true
	}
	for _, key := range []string{"kind", "type", "event", "event_type", "message_kind", "message_type"} {
		switch strings.ToLower(strings.TrimSpace(message.Metadata[key])) {
		case "tool_call", "tool-call", "tool_use", "tool-use", "tool_result", "tool-result":
			return true
		}
	}
	if strings.TrimSpace(message.Metadata["tool_call_id"]) != "" {
		return true
	}
	return false
}

func triggerMessageDTOFromRecord(message MessageRecord) MessageDTO {
	return MessageDTO{
		MessageID:      message.MessageID,
		ConversationID: message.ConversationID,
		TurnID:         message.TurnID,
		RunID:          message.RunID,
		MissionID:      message.MissionID,
		MissionTaskID:  message.MissionTaskID,
		Role:           message.Role,
		AuthorID:       message.AuthorID,
		AuthorName:     strings.TrimSpace(message.AuthorName),
		Content:        message.Content,
		Metadata:       cloneStringMap(message.Metadata),
		CreatedAt:      message.CreatedAt,
	}
}

func attachDTOAttachments(messages []MessageDTO, attachments []AttachmentDTO) {
	if len(messages) == 0 || len(attachments) == 0 {
		return
	}
	byMessage := map[string][]AttachmentDTO{}
	for _, attachment := range attachments {
		messageID := strings.TrimSpace(attachment.MessageID)
		if messageID == "" {
			continue
		}
		byMessage[messageID] = append(byMessage[messageID], attachment)
	}
	if len(byMessage) == 0 {
		return
	}
	for i := range messages {
		messageID := strings.TrimSpace(messages[i].MessageID)
		if len(byMessage[messageID]) == 0 {
			continue
		}
		messages[i].Attachments = append([]AttachmentDTO(nil), byMessage[messageID]...)
	}
}

func applyConversationContextToAgentRequest(request *DiscussionAgentRequest, view ConversationRecordView) {
	if request == nil {
		return
	}
	if strings.TrimSpace(request.DiscussionTitle) == "" {
		request.DiscussionTitle = strings.TrimSpace(view.Title)
	}
	if strings.TrimSpace(request.ConversationKind) == "" {
		request.ConversationKind = strings.TrimSpace(view.ConversationKind)
	}
	if strings.TrimSpace(request.ConversationOwnerID) == "" {
		request.ConversationOwnerID = firstNonBlank(view.OwnerID, view.CreatedBy)
	}
	if strings.TrimSpace(request.PersonalAssistantOriginOwnerID) == "" {
		request.PersonalAssistantOriginOwnerID = strings.TrimSpace(view.PersonalAssistantOriginOwnerID)
	}
	if strings.TrimSpace(request.PersonalAssistantOriginSubjectID) == "" {
		request.PersonalAssistantOriginSubjectID = strings.TrimSpace(view.PersonalAssistantOriginSubjectID)
	}
	if strings.TrimSpace(request.ConversationTenantID) == "" {
		request.ConversationTenantID = strings.TrimSpace(view.TenantID)
	}
	if _, ok := validDiscussionHILMode(request.HILMode); !ok {
		request.HILMode = normalizeDiscussionHILMode(view.HILMode)
	}
	if strings.TrimSpace(request.DiscussionGoal) == "" {
		request.DiscussionGoal = discussionGoalFromConversation(view)
	}
}

func discussionGoalFromConversation(view ConversationRecordView) string {
	links := mapMissionLinks(view.LinkedMissions)
	if len(links) == 0 {
		return ""
	}
	lines := make([]string, 0, len(links))
	for _, mission := range links {
		if strings.TrimSpace(mission.MissionID) == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s：%s（%s）", mission.MissionID, firstNonBlank(mission.Title, mission.MissionID), firstNonBlank(mission.Status, "unknown")))
	}
	if len(lines) == 0 {
		return ""
	}
	return "已绑定 Mission：" + strings.Join(lines, "；")
}
