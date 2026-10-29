package httptransport

import (
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
)

type discussionPageDTO struct {
	Discussions []discussionSummaryDTO    `json:"discussions"`
	Page        discussionsession.PageDTO `json:"page"`
}

type discussionSummaryDTO struct {
	DiscussionID             string    `json:"discussion_id"`
	Revision                 int64     `json:"revision"`
	ConversationKind         string    `json:"conversation_kind,omitempty"`
	PersonalAssistantRuntime bool      `json:"personal_assistant_runtime,omitempty"`
	DirectAgentID            string    `json:"direct_agent_id,omitempty"`
	DirectTargetKind         string    `json:"direct_target_kind,omitempty"`
	DirectTargetID           string    `json:"direct_target_id,omitempty"`
	WorkspaceRootDir         string    `json:"workspace_root_dir,omitempty"`
	HILMode                  string    `json:"hil_mode"`
	Title                    string    `json:"title"`
	Status                   string    `json:"status"`
	LastMessage              string    `json:"last_message,omitempty"`
	LinkedMissions           int       `json:"linked_missions"`
	AttachmentCount          int       `json:"attachment_count"`
	UpdatedAt                time.Time `json:"updated_at"`
	ViewedAt                 time.Time `json:"viewed_at,omitempty"`
	UnreadCompleted          bool      `json:"unread_completed"`
}

type discussionDTO struct {
	DiscussionID             string                                    `json:"discussion_id"`
	Revision                 int64                                     `json:"revision"`
	ConversationKind         string                                    `json:"conversation_kind,omitempty"`
	PersonalAssistantRuntime bool                                      `json:"personal_assistant_runtime,omitempty"`
	DirectAgentID            string                                    `json:"direct_agent_id,omitempty"`
	DirectTargetKind         string                                    `json:"direct_target_kind,omitempty"`
	DirectTargetID           string                                    `json:"direct_target_id,omitempty"`
	WorkspaceRootDir         string                                    `json:"workspace_root_dir,omitempty"`
	HILMode                  string                                    `json:"hil_mode"`
	Title                    string                                    `json:"title"`
	Status                   string                                    `json:"status"`
	CreatedBy                string                                    `json:"created_by"`
	CreatedByName            string                                    `json:"created_by_name,omitempty"`
	CreatedAt                time.Time                                 `json:"created_at"`
	UpdatedAt                time.Time                                 `json:"updated_at"`
	ViewedAt                 time.Time                                 `json:"viewed_at,omitempty"`
	UnreadCompleted          bool                                      `json:"unread_completed"`
	Messages                 []discussionMessageDTO                    `json:"messages"`
	MessagePage              *discussionsession.MessagePageMetaDTO     `json:"message_page,omitempty"`
	Members                  []discussionsession.ConversationMemberDTO `json:"members"`
	Turns                    []discussionTurnDTO                       `json:"turns,omitempty"`
	Runs                     []discussionRunDTO                        `json:"runs"`
	Events                   []discussionEventDTO                      `json:"events,omitempty"`
	LinkedMissions           []discussionsession.LinkedMissionDTO      `json:"linked_missions"`
	Attachments              []discussionAttachmentDTO                 `json:"attachments"`
}

type discussionHILModeDTO struct {
	DiscussionID string    `json:"discussion_id"`
	Revision     int64     `json:"revision"`
	HILMode      string    `json:"hil_mode"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type discussionMessagePageDTO struct {
	Messages []discussionMessageDTO               `json:"messages"`
	Page     discussionsession.MessagePageMetaDTO `json:"page"`
}

type discussionMessageDTO struct {
	MessageID       string                                `json:"message_id"`
	DiscussionID    string                                `json:"discussion_id"`
	TurnID          string                                `json:"turn_id,omitempty"`
	RunID           string                                `json:"run_id,omitempty"`
	Role            string                                `json:"role"`
	AuthorID        string                                `json:"author_id"`
	AuthorName      string                                `json:"author_name,omitempty"`
	IsCurrentViewer *bool                                 `json:"is_current_viewer,omitempty"`
	Content         string                                `json:"content"`
	Mentions        []discussionsession.MessageMentionDTO `json:"mentions,omitempty"`
	Attachments     []discussionAttachmentDTO             `json:"attachments,omitempty"`
	Metadata        map[string]string                     `json:"metadata,omitempty"`
	CreatedAt       time.Time                             `json:"created_at"`
}

type discussionRunDTO struct {
	RunID           string                          `json:"run_id"`
	DiscussionID    string                          `json:"discussion_id"`
	TurnID          string                          `json:"turn_id,omitempty"`
	Mode            string                          `json:"mode,omitempty"`
	HasPlan         bool                            `json:"has_plan,omitempty"`
	Plan            *agentcore.RuntimePlanState     `json:"plan,omitempty"`
	AgentID         string                          `json:"agent_id"`
	LogicalAgentID  string                          `json:"logical_agent_id,omitempty"`
	AgentName       string                          `json:"agent_name,omitempty"`
	Status          string                          `json:"status"`
	RuntimeKind     string                          `json:"runtime_kind"`
	SessionID       string                          `json:"session_id,omitempty"`
	ThinkingSummary string                          `json:"thinking_summary,omitempty"`
	ToolCalls       []discussionsession.ToolCallDTO `json:"tool_calls,omitempty"`
	Usage           discussionsession.ModelUsageMap `json:"usage,omitempty"`
	DurationMs      int64                           `json:"duration_ms,omitempty"`
	StartedAt       time.Time                       `json:"started_at,omitempty"`
	CompletedAt     time.Time                       `json:"completed_at,omitempty"`
	CreatedAt       time.Time                       `json:"created_at"`
}

type discussionTurnDTO struct {
	TurnID        string    `json:"turn_id"`
	DiscussionID  string    `json:"discussion_id"`
	RootMessageID string    `json:"root_message_id"`
	Mode          string    `json:"mode"`
	Status        string    `json:"status"`
	ErrorCode     string    `json:"error_code,omitempty"`
	ErrorMessage  string    `json:"error_message,omitempty"`
	RunIDs        []string  `json:"run_ids,omitempty"`
	StartedAt     time.Time `json:"started_at,omitempty"`
	CompletedAt   time.Time `json:"completed_at,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type discussionRunDebugDTO struct {
	DiscussionID     string                                `json:"discussion_id"`
	RunID            string                                `json:"run_id"`
	OutboundPrompt   string                                `json:"outbound_prompt"`
	RuntimeResponse  string                                `json:"runtime_response"`
	ProviderMetadata map[string]string                     `json:"provider_metadata,omitempty"`
	Selected         discussionsession.RunDebugSelectedDTO `json:"selected"`
	CreatedAt        time.Time                             `json:"created_at"`
	Redacted         bool                                  `json:"redacted"`
}

type discussionEventDTO struct {
	EventID          string                                `json:"event_id"`
	DiscussionID     string                                `json:"discussion_id"`
	Kind             string                                `json:"kind"`
	Status           string                                `json:"status,omitempty"`
	Summary          string                                `json:"summary,omitempty"`
	Metadata         map[string]string                     `json:"metadata,omitempty"`
	AvailableActions []discussionsession.ApprovalActionDTO `json:"available_actions,omitempty"`
	CreatedAt        time.Time                             `json:"created_at"`
}

type discussionAttachmentPageDTO struct {
	Attachments []discussionAttachmentDTO `json:"attachments"`
}

type discussionAttachmentDraftDTO struct {
	Discussion discussionDTO           `json:"discussion"`
	Attachment discussionAttachmentDTO `json:"attachment"`
}

type discussionAttachmentDTO struct {
	AttachmentID   string    `json:"attachment_id"`
	DiscussionID   string    `json:"discussion_id"`
	MessageID      string    `json:"message_id,omitempty"`
	FileName       string    `json:"file_name"`
	ContentType    string    `json:"content_type,omitempty"`
	SizeBytes      int64     `json:"size_bytes"`
	UploadedBy     string    `json:"uploaded_by"`
	UploadedByKind string    `json:"uploaded_by_kind,omitempty"`
	UploadedByName string    `json:"uploaded_by_name,omitempty"`
	DownloadURL    string    `json:"download_url"`
	PreviewURL     string    `json:"preview_url,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

type discussionStreamEventDTO struct {
	Kind         string                         `json:"kind"`
	EventID      string                         `json:"event_id,omitempty"`
	TurnID       string                         `json:"turn_id,omitempty"`
	DiscussionID string                         `json:"discussion_id,omitempty"`
	Revision     int64                          `json:"revision,omitempty"`
	Discussion   *discussionDTO                 `json:"discussion,omitempty"`
	Run          *discussionRunDTO              `json:"run,omitempty"`
	Message      *discussionMessageDTO          `json:"message,omitempty"`
	ToolCall     *discussionsession.ToolCallDTO `json:"tool_call,omitempty"`
	Approval     *discussionEventDTO            `json:"approval,omitempty"`
	Plan         *agentcore.RuntimePlanState    `json:"plan,omitempty"`
	Delta        string                         `json:"delta,omitempty"`
	RunID        string                         `json:"run_id,omitempty"`
	Sequence     int                            `json:"sequence,omitempty"`
	Error        string                         `json:"error,omitempty"`
}

func toDiscussionPageDTO(page discussionsession.ConversationPageDTO) discussionPageDTO {
	out := discussionPageDTO{
		Discussions: make([]discussionSummaryDTO, 0, len(page.Conversations)),
		Page:        page.Page,
	}
	for _, conversation := range page.Conversations {
		out.Discussions = append(out.Discussions, discussionSummaryDTO{
			DiscussionID:             conversation.ConversationID,
			Revision:                 conversation.Revision,
			ConversationKind:         conversation.ConversationKind,
			PersonalAssistantRuntime: conversation.PersonalAssistantRuntime,
			DirectAgentID:            conversation.DirectAgentID,
			DirectTargetKind:         conversation.DirectTargetKind,
			DirectTargetID:           conversation.DirectTargetID,
			WorkspaceRootDir:         conversation.WorkspaceRootDir,
			HILMode:                  conversation.HILMode,
			Title:                    conversation.Title,
			Status:                   conversation.Status,
			LastMessage:              conversation.LastMessage,
			LinkedMissions:           conversation.LinkedMissions,
			AttachmentCount:          conversation.AttachmentCount,
			UpdatedAt:                conversation.UpdatedAt,
			ViewedAt:                 conversation.ViewedAt,
			UnreadCompleted:          conversation.UnreadCompleted,
		})
	}
	return out
}

func toDiscussionDTO(conversation discussionsession.ConversationDTO) discussionDTO {
	out := discussionDTO{
		DiscussionID:             conversation.ConversationID,
		Revision:                 conversation.Revision,
		ConversationKind:         conversation.ConversationKind,
		PersonalAssistantRuntime: conversation.PersonalAssistantRuntime,
		DirectAgentID:            conversation.DirectAgentID,
		DirectTargetKind:         conversation.DirectTargetKind,
		DirectTargetID:           conversation.DirectTargetID,
		WorkspaceRootDir:         conversation.WorkspaceRootDir,
		HILMode:                  conversation.HILMode,
		Title:                    conversation.Title,
		Status:                   conversation.Status,
		CreatedBy:                conversation.CreatedBy,
		CreatedByName:            conversation.CreatedByName,
		CreatedAt:                conversation.CreatedAt,
		UpdatedAt:                conversation.UpdatedAt,
		ViewedAt:                 conversation.ViewedAt,
		UnreadCompleted:          conversation.UnreadCompleted,
		Messages:                 make([]discussionMessageDTO, 0, len(conversation.Messages)),
		MessagePage:              conversation.MessagePage,
		Members:                  conversation.Members,
		Turns:                    make([]discussionTurnDTO, 0, len(conversation.Turns)),
		Runs:                     make([]discussionRunDTO, 0, len(conversation.Runs)),
		Events:                   make([]discussionEventDTO, 0, len(conversation.Events)),
		LinkedMissions:           conversation.LinkedMissions,
		Attachments:              make([]discussionAttachmentDTO, 0, len(conversation.Attachments)),
	}
	for _, message := range conversation.Messages {
		out.Messages = append(out.Messages, toDiscussionMessageDTO(message))
	}
	for _, run := range conversation.Runs {
		out.Runs = append(out.Runs, toDiscussionRunDTO(run))
	}
	for _, turn := range conversation.Turns {
		out.Turns = append(out.Turns, toDiscussionTurnDTO(turn))
	}
	for _, event := range conversation.Events {
		out.Events = append(out.Events, toDiscussionEventDTO(event))
	}
	for _, attachment := range conversation.Attachments {
		out.Attachments = append(out.Attachments, toDiscussionAttachmentDTO(attachment))
	}
	return out
}

func toDiscussionDTOForViewer(conversation discussionsession.ConversationDTO, viewer discussionsession.ViewerRef) discussionDTO {
	out := toDiscussionDTO(conversation)
	for index, message := range conversation.Messages {
		out.Messages[index] = toDiscussionMessageDTOForViewer(message, viewer)
	}
	return out
}

func toDiscussionHILModeDTO(update discussionsession.ConversationHILModeDTO) discussionHILModeDTO {
	return discussionHILModeDTO{
		DiscussionID: update.ConversationID,
		Revision:     update.Revision,
		HILMode:      update.HILMode,
		UpdatedAt:    update.UpdatedAt,
	}
}

func toDiscussionTurnDTO(turn discussionsession.TurnDTO) discussionTurnDTO {
	return discussionTurnDTO{
		TurnID:        turn.TurnID,
		DiscussionID:  turn.ConversationID,
		RootMessageID: turn.RootMessageID,
		Mode:          turn.Mode,
		Status:        turn.Status,
		ErrorCode:     turn.ErrorCode,
		ErrorMessage:  turn.ErrorMessage,
		RunIDs:        turn.RunIDs,
		StartedAt:     turn.StartedAt,
		CompletedAt:   turn.CompletedAt,
		CreatedAt:     turn.CreatedAt,
		UpdatedAt:     turn.UpdatedAt,
	}
}

func toDiscussionMessagePageDTO(page discussionsession.MessagePageDTO) discussionMessagePageDTO {
	out := discussionMessagePageDTO{
		Messages: make([]discussionMessageDTO, 0, len(page.Messages)),
		Page:     page.Page,
	}
	for _, message := range page.Messages {
		out.Messages = append(out.Messages, toDiscussionMessageDTO(message))
	}
	return out
}

func toDiscussionMessagePageDTOForViewer(page discussionsession.MessagePageDTO, viewer discussionsession.ViewerRef) discussionMessagePageDTO {
	out := toDiscussionMessagePageDTO(page)
	for index, message := range page.Messages {
		out.Messages[index] = toDiscussionMessageDTOForViewer(message, viewer)
	}
	return out
}

func toDiscussionMessageDTO(message discussionsession.MessageDTO) discussionMessageDTO {
	out := discussionMessageDTO{
		MessageID:    message.MessageID,
		DiscussionID: message.ConversationID,
		TurnID:       message.TurnID,
		RunID:        message.RunID,
		Role:         message.Role,
		AuthorID:     message.AuthorID,
		AuthorName:   message.AuthorName,
		Content:      message.Content,
		Mentions:     message.Mentions,
		Metadata:     message.Metadata,
		CreatedAt:    message.CreatedAt,
	}
	for _, attachment := range message.Attachments {
		out.Attachments = append(out.Attachments, toDiscussionAttachmentDTO(attachment))
	}
	return out
}

func toDiscussionMessageDTOForViewer(message discussionsession.MessageDTO, viewer discussionsession.ViewerRef) discussionMessageDTO {
	out := toDiscussionMessageDTO(message)
	isCurrentViewer := discussionMessageBelongsToViewer(message, viewer)
	out.IsCurrentViewer = &isCurrentViewer
	return out
}

func discussionMessageBelongsToViewer(message discussionsession.MessageDTO, viewer discussionsession.ViewerRef) bool {
	if !strings.EqualFold(strings.TrimSpace(message.Role), "user") || strings.TrimSpace(message.AuthorID) == "" {
		return false
	}
	authorID := strings.TrimSpace(message.AuthorID)
	for _, identityID := range append([]string{viewer.ViewerID, viewer.OwnerID}, viewer.IdentityIDs...) {
		if strings.EqualFold(authorID, strings.TrimSpace(identityID)) {
			return true
		}
	}
	return false
}

func toDiscussionRunDTO(run discussionsession.RunDTO) discussionRunDTO {
	return discussionRunDTO{
		RunID:           run.RunID,
		DiscussionID:    run.ConversationID,
		TurnID:          run.TurnID,
		Mode:            run.Mode,
		HasPlan:         run.HasPlan,
		Plan:            run.Plan,
		AgentID:         run.AgentID,
		LogicalAgentID:  run.LogicalAgentID,
		AgentName:       run.AgentName,
		Status:          run.Status,
		RuntimeKind:     run.RuntimeKind,
		SessionID:       run.SessionID,
		ThinkingSummary: run.ThinkingSummary,
		ToolCalls:       run.ToolCalls,
		Usage:           run.Usage,
		DurationMs:      run.DurationMs,
		StartedAt:       run.StartedAt,
		CompletedAt:     run.CompletedAt,
		CreatedAt:       run.CreatedAt,
	}
}

func toDiscussionRunDebugDTO(debug discussionsession.RunDebugDTO) discussionRunDebugDTO {
	out := discussionRunDebugDTO{
		DiscussionID:     debug.ConversationID,
		RunID:            debug.RunID,
		OutboundPrompt:   debug.OutboundPrompt,
		RuntimeResponse:  debug.RuntimeResponse,
		ProviderMetadata: debug.ProviderMetadata,
		Selected:         debug.Selected,
		CreatedAt:        debug.CreatedAt,
		Redacted:         debug.Redacted,
	}
	return out
}

func toDiscussionEventDTO(event discussionsession.EventDTO) discussionEventDTO {
	return discussionEventDTO{
		EventID:          event.EventID,
		DiscussionID:     event.ConversationID,
		Kind:             event.Kind,
		Status:           event.Status,
		Summary:          event.Summary,
		Metadata:         event.Metadata,
		AvailableActions: event.AvailableActions,
		CreatedAt:        event.CreatedAt,
	}
}

func toDiscussionAttachmentPageDTO(page discussionsession.AttachmentPageDTO) discussionAttachmentPageDTO {
	out := discussionAttachmentPageDTO{Attachments: make([]discussionAttachmentDTO, 0, len(page.Attachments))}
	for _, attachment := range page.Attachments {
		out.Attachments = append(out.Attachments, toDiscussionAttachmentDTO(attachment))
	}
	return out
}

func toDiscussionAttachmentDraftDTO(draft discussionsession.AttachmentDraftDTO) discussionAttachmentDraftDTO {
	return discussionAttachmentDraftDTO{
		Discussion: toDiscussionDTO(draft.Conversation),
		Attachment: toDiscussionAttachmentDTO(draft.Attachment),
	}
}

func toDiscussionAttachmentDraftDTOForViewer(draft discussionsession.AttachmentDraftDTO, viewer discussionsession.ViewerRef) discussionAttachmentDraftDTO {
	out := toDiscussionAttachmentDraftDTO(draft)
	discussion := toDiscussionDTOForViewer(draft.Conversation, viewer)
	out.Discussion = discussion
	return out
}

func toDiscussionAttachmentDTO(attachment discussionsession.AttachmentDTO) discussionAttachmentDTO {
	return discussionAttachmentDTO{
		AttachmentID:   attachment.AttachmentID,
		DiscussionID:   attachment.ConversationID,
		MessageID:      attachment.MessageID,
		FileName:       attachment.FileName,
		ContentType:    attachment.ContentType,
		SizeBytes:      attachment.SizeBytes,
		UploadedBy:     attachment.UploadedBy,
		UploadedByKind: attachment.UploadedByKind,
		UploadedByName: attachment.UploadedByName,
		DownloadURL:    attachment.DownloadURL,
		PreviewURL:     attachment.PreviewURL,
		CreatedAt:      attachment.CreatedAt,
	}
}

func toDiscussionStreamEventDTO(event discussionsession.StreamEventDTO) discussionStreamEventDTO {
	out := discussionStreamEventDTO{
		Kind:         event.Kind,
		EventID:      event.EventID,
		TurnID:       event.TurnID,
		DiscussionID: event.ConversationID,
		Revision:     event.Revision,
		ToolCall:     event.ToolCall,
		Plan:         event.Plan,
		Delta:        event.Delta,
		RunID:        event.RunID,
		Sequence:     event.Sequence,
		Error:        event.Error,
	}
	if event.Approval != nil {
		approval := toDiscussionEventDTO(*event.Approval)
		out.Approval = &approval
	}
	if event.Conversation != nil {
		discussion := toDiscussionDTO(*event.Conversation)
		out.Discussion = &discussion
	}
	if event.Run != nil {
		run := toDiscussionRunDTO(*event.Run)
		out.Run = &run
	}
	if event.Message != nil {
		message := toDiscussionMessageDTO(*event.Message)
		out.Message = &message
	}
	return out
}

func toDiscussionStreamEventDTOForViewer(event discussionsession.StreamEventDTO, viewer discussionsession.ViewerRef) discussionStreamEventDTO {
	out := toDiscussionStreamEventDTO(event)
	if event.Conversation != nil {
		discussion := toDiscussionDTOForViewer(*event.Conversation, viewer)
		out.Discussion = &discussion
	}
	if event.Message != nil {
		message := toDiscussionMessageDTOForViewer(*event.Message, viewer)
		out.Message = &message
	}
	return out
}
