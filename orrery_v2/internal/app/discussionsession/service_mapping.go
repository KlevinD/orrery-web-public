package discussionsession

import (
	"net/url"
	"strings"
	"time"
)

func mapConversationDTO(view ConversationRecordView) ConversationDTO {
	return mapConversationDTOWithMessagePage(view, nil)
}

func mapConversationDTOWithMessagePage(view ConversationRecordView, messagePage *MessagePageMetaDTO) ConversationDTO {
	unreadCompleted := isUnreadCompletedConversation(view.ConversationRecord)
	directTarget := directTargetFromRecord(view.ConversationRecord)
	messages := mapMessages(view.Messages)
	attachMessageAttachments(messages, view.Attachments)
	return ConversationDTO{
		ConversationID:           view.ConversationID,
		TenantID:                 strings.TrimSpace(view.TenantID),
		PurposeKind:              strings.TrimSpace(view.PurposeKind),
		PurposeID:                strings.TrimSpace(view.PurposeID),
		WorkspaceID:              strings.TrimSpace(view.WorkspaceID),
		Revision:                 view.Revision,
		ConversationKind:         strings.TrimSpace(view.ConversationKind),
		PersonalAssistantRuntime: isPersonalAssistantRuntimeConversation(view.ConversationRecord),
		DirectAgentID:            directAgentIDFromRecord(view.ConversationRecord),
		DirectTargetKind:         directTarget.Kind,
		DirectTargetID:           directTarget.ID,
		MemberAdditionsAllowed:   conversationMemberAdditionsAllowed(view.ConversationRecord),
		WorkspaceRootDir:         strings.TrimSpace(view.WorkspaceRootDir),
		HILMode:                  normalizeDiscussionHILMode(view.HILMode),
		Title:                    view.Title,
		Status:                   view.Status,
		CreatedBy:                view.CreatedBy,
		CreatedByName:            firstNonBlank(view.CreatedByName, view.CreatedBy),
		CreatedAt:                view.CreatedAt,
		UpdatedAt:                view.UpdatedAt,
		ViewedAt:                 view.ViewedAt,
		UnreadCompleted:          unreadCompleted,
		Messages:                 messages,
		MessagePage:              messagePage,
		Members:                  mapConversationMembers(view.Members),
		Turns:                    mapTurns(view.Turns),
		Runs:                     mapRuns(view.Runs, view.Members),
		Events:                   mapEventsForConversation(view),
		LinkedMissions:           mapMissionLinks(view.LinkedMissions),
		Attachments:              mapAttachments(view.Attachments),
	}
}

func mapConversationSummaryDTO(view ConversationRecordView) ConversationSummaryDTO {
	lastMessage := ""
	if len(view.Messages) > 0 {
		lastMessage = publicMessageContent(view.Messages[len(view.Messages)-1])
	}
	unreadCompleted := isUnreadCompletedConversation(view.ConversationRecord)
	directTarget := directTargetFromRecord(view.ConversationRecord)
	return ConversationSummaryDTO{
		ConversationID:           view.ConversationID,
		PurposeKind:              strings.TrimSpace(view.PurposeKind),
		PurposeID:                strings.TrimSpace(view.PurposeID),
		Revision:                 view.Revision,
		ConversationKind:         strings.TrimSpace(view.ConversationKind),
		PersonalAssistantRuntime: isPersonalAssistantRuntimeConversation(view.ConversationRecord),
		DirectAgentID:            directAgentIDFromRecord(view.ConversationRecord),
		DirectTargetKind:         directTarget.Kind,
		DirectTargetID:           directTarget.ID,
		MemberAdditionsAllowed:   conversationMemberAdditionsAllowed(view.ConversationRecord),
		WorkspaceRootDir:         strings.TrimSpace(view.WorkspaceRootDir),
		HILMode:                  normalizeDiscussionHILMode(view.HILMode),
		Title:                    view.Title,
		Status:                   view.Status,
		LastMessage:              lastMessage,
		LinkedMissions:           len(view.LinkedMissions),
		AttachmentCount:          len(view.Attachments),
		UpdatedAt:                view.UpdatedAt,
		ViewedAt:                 view.ViewedAt,
		UnreadCompleted:          unreadCompleted,
	}
}

func mapMessages(records []MessageRecord) []MessageDTO {
	out := make([]MessageDTO, 0, len(records))
	for _, record := range records {
		mentions := make([]MessageMentionDTO, 0, len(record.Mentions))
		for _, mention := range record.Mentions {
			mentions = append(mentions, MessageMentionDTO{TargetKind: mention.TargetKind, TargetID: mention.TargetID, DisplayName: mention.DisplayName})
		}
		out = append(out, MessageDTO{MessageID: record.MessageID, ConversationID: record.ConversationID, TurnID: record.TurnID, RunID: record.RunID, MissionID: record.MissionID, MissionTaskID: record.MissionTaskID, Role: record.Role, AuthorID: record.AuthorID, AuthorName: strings.TrimSpace(record.AuthorName), Content: publicMessageContent(record), Mentions: mentions, Metadata: publicMessageMetadata(record), CreatedAt: record.CreatedAt})
	}
	return out
}

func mapMessagesWithAttachments(records []MessageRecord, attachments []AttachmentRecord) []MessageDTO {
	messages := mapMessages(records)
	attachMessageAttachments(messages, attachments)
	return messages
}

func attachMessageAttachments(messages []MessageDTO, attachments []AttachmentRecord) {
	if len(messages) == 0 || len(attachments) == 0 {
		return
	}
	byMessage := map[string][]AttachmentDTO{}
	for _, attachment := range attachments {
		messageID := strings.TrimSpace(attachment.MessageID)
		if messageID == "" {
			continue
		}
		byMessage[messageID] = append(byMessage[messageID], mapAttachment(attachment))
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

func mapConversationMembers(records []ConversationMemberRecord) []ConversationMemberDTO {
	out := make([]ConversationMemberDTO, 0, len(records))
	for _, record := range records {
		if strings.TrimSpace(record.MemberKind) == "" || strings.TrimSpace(record.MemberID) == "" {
			continue
		}
		out = append(out, ConversationMemberDTO{
			Kind:          strings.TrimSpace(record.MemberKind),
			ID:            strings.TrimSpace(record.MemberID),
			DisplayName:   strings.TrimSpace(record.DisplayName),
			Role:          strings.TrimSpace(record.Role),
			ManagedSource: strings.TrimSpace(record.ManagedSource),
		})
	}
	return out
}

func viewerDisplayName(viewer ViewerRef) string {
	return firstNonBlank(viewer.DisplayName, viewer.ViewerID)
}

func promptHistoryWithoutMessage(records []MessageRecord, messageID string) []MessageDTO {
	if strings.TrimSpace(messageID) == "" {
		return mapMessages(promptHistoryMessageRecords(records))
	}
	filtered := make([]MessageRecord, 0, len(records))
	for _, record := range records {
		if record.MessageID == messageID {
			continue
		}
		if isDiscussionToolHistoryRecord(record) {
			continue
		}
		filtered = append(filtered, record)
	}
	return mapMessages(filtered)
}

func retryPromptHistoryBeforeMessage(records []MessageRecord, messageID string) []MessageDTO {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return mapMessages(promptHistoryMessageRecords(records))
	}
	filtered := make([]MessageRecord, 0, len(records))
	for _, record := range records {
		if record.MessageID == messageID {
			break
		}
		if isDiscussionToolHistoryRecord(record) {
			continue
		}
		filtered = append(filtered, record)
	}
	return mapMessages(filtered)
}

func promptHistoryMessageRecords(records []MessageRecord) []MessageRecord {
	out := make([]MessageRecord, 0, len(records))
	for _, record := range records {
		if isDiscussionToolHistoryRecord(record) {
			continue
		}
		out = append(out, record)
	}
	return out
}

func isDiscussionToolHistoryRecord(record MessageRecord) bool {
	return isDiscussionToolHistoryMessage(MessageDTO{
		MessageID:      record.MessageID,
		ConversationID: record.ConversationID,
		Role:           record.Role,
		AuthorID:       record.AuthorID,
		AuthorName:     record.AuthorName,
		Content:        record.Content,
		Metadata:       record.Metadata,
		CreatedAt:      record.CreatedAt,
	})
}

func mapRuns(records []RunRecord, memberSets ...[]ConversationMemberRecord) []RunDTO {
	var members []ConversationMemberRecord
	if len(memberSets) > 0 {
		members = memberSets[0]
	}
	out := make([]RunDTO, 0, len(records))
	for _, record := range records {
		agentName := strings.TrimSpace(record.AgentName)
		if agentName == "" {
			agentName = conversationMemberName(members, "agent", record.AgentID)
		}
		plan := cloneRuntimePlanStatePointer(record.Plan)
		if plan == nil || strings.TrimSpace(plan.PlanID) == "" {
			plan = recoverClaudeTaskPlan(record.ToolCalls, record.Status)
		}
		out = append(out, RunDTO{RunID: record.RunID, ConversationID: record.ConversationID, TurnID: record.TurnID, MissionID: record.MissionID, MissionTaskID: record.MissionTaskID, Mode: record.Mode, HasPlan: record.HasPlan || plan != nil, Plan: plan, AgentID: record.AgentID, LogicalAgentID: record.LogicalAgentID, AgentName: agentName, Status: record.Status, RuntimeKind: record.RuntimeKind, SessionID: record.SessionID, ThinkingSummary: publicRunThinkingSummary(record), ToolCalls: append([]ToolCallDTO(nil), record.ToolCalls...), Usage: cloneModelUsageMap(record.Usage), DurationMs: record.DurationMs, TraceAvailable: strings.TrimSpace(record.TraceRef) != "" && record.TraceVersion > 0, TraceStatus: strings.TrimSpace(record.TraceStatus), StartedAt: record.StartedAt, CompletedAt: record.CompletedAt, CreatedAt: record.CreatedAt})
	}
	return out
}

func mapTurns(records []TurnRecord) []TurnDTO {
	out := make([]TurnDTO, 0, len(records))
	for _, record := range records {
		out = append(out, TurnDTO{
			TurnID:         record.TurnID,
			ConversationID: record.ConversationID,
			RootMessageID:  record.RootMessageID,
			Mode:           record.Mode,
			Status:         record.Status,
			ErrorCode:      record.ErrorCode,
			ErrorMessage:   publicOptionalText(record.ErrorMessage, "任务执行失败，详细原因已记录到 Orrery。"),
			RunIDs:         append([]string(nil), record.RunIDs...),
			StartedAt:      record.StartedAt,
			CompletedAt:    record.CompletedAt,
			CreatedAt:      record.CreatedAt,
			UpdatedAt:      record.UpdatedAt,
		})
	}
	return out
}

func mapEvents(records []EventRecord) []EventDTO {
	out := make([]EventDTO, 0, len(records))
	for _, record := range records {
		out = append(out, mapEvent(record))
	}
	return out
}

func mapEvent(record EventRecord) EventDTO {
	return mapEventForConversation(record, "", nil)
}

func mapEventsForConversation(view ConversationRecordView) []EventDTO {
	out := make([]EventDTO, 0, len(view.Events))
	for _, record := range view.Events {
		out = append(out, mapEventForConversation(record, view.Status, view.Runs))
	}
	return out
}

func mapEventForConversation(record EventRecord, conversationStatus string, runs []RunRecord) EventDTO {
	dto := EventDTO{
		EventID:        record.EventID,
		ConversationID: record.ConversationID,
		Kind:           record.Kind,
		Status:         record.Status,
		Summary:        publicOptionalText(record.Summary, "事件已记录到 Orrery。"),
		Metadata:       cloneStringMap(record.Metadata),
		CreatedAt:      record.CreatedAt,
	}
	if record.Kind == discussionRuntimeApprovalEventKind {
		metadata := record.Metadata
		displayKind := RuntimeApprovalDisplayKind(firstNonBlank(metadata["approval_kind"], metadata["approval_type"], "permission_request"))
		path := RuntimeApprovalDecisionPath(record.ConversationID, record.EventID)
		if runtimeApprovalUnavailableForConversation(record, conversationStatus, runs) {
			dto.Status = "unavailable"
		}
		if !runtimeApprovalIsHILBacked(record) && runtimeApprovalCanBeDecided(record, conversationStatus, runs) {
			dto.AvailableActions = RuntimeApprovalActions(path, displayKind, record.Status, metadata["approval_allowed_actions"])
		}
	}
	return dto
}

func runtimeApprovalUnavailableForConversation(record EventRecord, conversationStatus string, runs []RunRecord) bool {
	if !runtimeApprovalIsPending(record.Status) {
		return false
	}
	if runtimeApprovalTerminalConversationUnavailable(conversationStatus) {
		return true
	}
	runID := strings.TrimSpace(record.Metadata["approval_run_id"])
	if runID != "" && len(runs) > 0 {
		for _, run := range runs {
			if !strings.EqualFold(strings.TrimSpace(run.RunID), runID) {
				continue
			}
			status := strings.TrimSpace(strings.ToLower(run.Status))
			if strings.TrimSpace(strings.ToLower(conversationStatus)) == "completed" && (status == "blocked" || status == "running" || status == "started") {
				return false
			}
			if terminalConversationStatus(conversationStatus) {
				return true
			}
			return terminalRuntimeStatus(run.Status)
		}
	}
	return terminalConversationStatus(conversationStatus)
}

func runtimeApprovalCanBeDecided(record EventRecord, conversationStatus string, runs []RunRecord) bool {
	if !runtimeApprovalIsPending(record.Status) || runtimeApprovalUnavailableForConversation(record, conversationStatus, runs) {
		return false
	}
	runID := strings.TrimSpace(record.Metadata["approval_run_id"])
	if runID == "" || len(runs) == 0 {
		return true
	}
	for _, run := range runs {
		if !strings.EqualFold(strings.TrimSpace(run.RunID), runID) {
			continue
		}
		status := strings.TrimSpace(strings.ToLower(run.Status))
		return status == "blocked" || status == "running"
	}
	return true
}

func RuntimeApprovalDecisionPath(conversationID string, approvalID string) string {
	conversationID = strings.TrimSpace(conversationID)
	approvalID = strings.TrimSpace(approvalID)
	if conversationID == "" || approvalID == "" {
		return ""
	}
	return "/api/discussions/" + url.PathEscape(conversationID) + "/approvals/" + url.PathEscape(approvalID) + "/decision"
}

func RuntimeApprovalDisplayKind(value string) string {
	switch strings.TrimSpace(value) {
	case "mcp_elicitation", "user_input":
		return "user_input"
	case "tool_access", "tool_use":
		return "tool_use"
	case "permission_request":
		return "permission"
	default:
		if strings.TrimSpace(value) == "" {
			return "permission"
		}
		return strings.TrimSpace(value)
	}
}

func RuntimeApprovalActions(path, displayKind, status string, allowedValues ...string) []ApprovalActionDTO {
	if !runtimeApprovalIsPending(status) || strings.TrimSpace(path) == "" {
		return nil
	}
	actions := []ApprovalActionDTO{
		runtimeApprovalAction("approve_once", "Approve once", path, false, "primary", "approved", "once", "message", false, false),
		runtimeApprovalAction("approve_session", "Approve for session", path, false, "primary", "approved", "session", "message", false, true),
		runtimeApprovalAction("deny", "Deny", path, false, "danger", "denied", "deny", "message", false, false),
		runtimeApprovalAction("other", "Other", path, false, "secondary", "approved", "once", "message", false, false),
	}
	if len(allowedValues) == 0 || strings.TrimSpace(allowedValues[0]) == "" {
		return actions
	}
	allowed := map[string]bool{}
	for _, actionID := range strings.Split(allowedValues[0], ",") {
		if actionID = strings.TrimSpace(actionID); actionID != "" {
			allowed[actionID] = true
		}
	}
	filtered := make([]ApprovalActionDTO, 0, len(actions))
	for _, action := range actions {
		if allowed[action.ActionID] {
			filtered = append(filtered, action)
		}
	}
	return filtered
}

func runtimeApprovalAction(actionID, label, path string, reasonRequired bool, style, decision, effect, inputKind string, allowsModification bool, rememberAllowed bool) ApprovalActionDTO {
	return ApprovalActionDTO{
		ActionID:             actionID,
		Label:                label,
		Style:                style,
		Decision:             decision,
		Effect:               effect,
		InputKind:            inputKind,
		Method:               "POST",
		Path:                 path,
		RequiresBody:         true,
		ConfirmationRequired: true,
		ReasonRequired:       reasonRequired,
		AllowsModification:   allowsModification,
		RememberAllowed:      rememberAllowed,
	}
}

func runtimeApprovalIsPending(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "pending", "open":
		return true
	default:
		return false
	}
}

func mapMissionLinks(records []LinkedMissionRecord) []LinkedMissionDTO {
	out := make([]LinkedMissionDTO, 0, len(records))
	for _, record := range records {
		out = append(out, LinkedMissionDTO{MissionID: record.MissionID, Title: record.Title, Status: record.Status, CreatedAt: record.CreatedAt})
	}
	return out
}

func mapAttachments(records []AttachmentRecord) []AttachmentDTO {
	out := make([]AttachmentDTO, 0, len(records))
	for _, record := range records {
		out = append(out, mapAttachment(record))
	}
	return out
}

func mapAttachment(record AttachmentRecord) AttachmentDTO {
	return AttachmentDTO{
		AttachmentID:   strings.TrimSpace(record.AttachmentID),
		ConversationID: strings.TrimSpace(record.ConversationID),
		MessageID:      strings.TrimSpace(record.MessageID),
		FileName:       strings.TrimSpace(record.FileName),
		ContentType:    strings.TrimSpace(record.ContentType),
		SizeBytes:      record.SizeBytes,
		UploadedBy:     strings.TrimSpace(record.UploadedBy),
		UploadedByKind: strings.TrimSpace(record.UploadedByKind),
		UploadedByName: strings.TrimSpace(record.UploadedByName),
		StoragePath:    strings.TrimSpace(record.StoragePath),
		DownloadURL:    discussionAttachmentDownloadURL(record.ConversationID, record.AttachmentID),
		PreviewURL:     discussionAttachmentPreviewURL(record),
		CreatedAt:      record.CreatedAt,
	}
}

func discussionAttachmentDownloadURL(conversationID string, attachmentID string) string {
	conversationID = strings.TrimSpace(conversationID)
	attachmentID = strings.TrimSpace(attachmentID)
	if conversationID == "" || attachmentID == "" {
		return ""
	}
	return "/api/discussions/" + conversationID + "/attachments/" + attachmentID + "/download"
}

func discussionAttachmentPreviewURL(record AttachmentRecord) string {
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(record.ContentType, ";")[0]))
	fileName := strings.ToLower(strings.TrimSpace(record.FileName))
	if contentType != "text/html" && !strings.HasSuffix(fileName, ".html") && !strings.HasSuffix(fileName, ".htm") {
		return ""
	}
	conversationID := strings.TrimSpace(record.ConversationID)
	attachmentID := strings.TrimSpace(record.AttachmentID)
	if conversationID == "" || attachmentID == "" {
		return ""
	}
	return "/api/discussions/" + conversationID + "/attachments/" + attachmentID + "/preview"
}

func findMessageRecord(records []MessageRecord, messageID string) (MessageRecord, bool) {
	for _, record := range records {
		if record.MessageID == messageID {
			return record, true
		}
	}
	return MessageRecord{}, false
}

func attachmentsForMessage(records []AttachmentRecord, messageID string) []AttachmentDTO {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return nil
	}
	out := make([]AttachmentDTO, 0)
	for _, record := range records {
		if strings.TrimSpace(record.MessageID) != messageID {
			continue
		}
		out = append(out, mapAttachment(record))
	}
	return out
}

func findPendingRetryEvent(records []EventRecord, messageID string) (EventRecord, bool) {
	for i := len(records) - 1; i >= 0; i-- {
		record := records[i]
		if record.Kind == "message_retry_requested" && record.Status == "pending" && record.Metadata["message_id"] == messageID {
			return record, true
		}
	}
	return EventRecord{}, false
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func firstTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func firstTimeAfter(value, floor time.Time) time.Time {
	if value.IsZero() {
		return floor.Add(time.Nanosecond)
	}
	if !floor.IsZero() && !value.After(floor) {
		return floor.Add(time.Nanosecond)
	}
	return value
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
