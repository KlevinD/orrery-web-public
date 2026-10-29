package discussionsession

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func (s *Service) applyDiscussionMentionToolCalls(ctx context.Context, conversationID string, agentID string, source MessageRecord, target ConversationMemberRecord, calls []ToolCallDTO, at time.Time) ([]LinkedMissionDTO, error) {
	if err := validateBoundToolCalls(calls, nil); err != nil {
		return nil, err
	}
	return s.applyBoundDiscussionMentionToolCalls(ctx, conversationID, agentID, source, target, calls, nil, at)
}

func (s *Service) applyDiscussionRunLocalToolCalls(ctx context.Context, conversationID string, agentID string, source MessageRecord, members []ConversationMemberRecord, calls []ToolCallDTO, at time.Time) ([]ToolCallDTO, error) {
	if len(calls) == 0 {
		return calls, nil
	}
	out := append([]ToolCallDTO(nil), calls...)
	target, hasTarget := conversationAgentMember(members, agentID)
	for i := range out {
		call := &out[i]
		switch normalizeToolName(call.Name) {
		case string(toolstd.DiscussionTalentList):
			listed, err := s.listDiscussionTalentFromToolCall(ctx, conversationID, *call)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				*call = toolCallWithFailure(*call, err)
				continue
			}
			if call.Result == nil {
				call.Result = map[string]string{}
			}
			writeDiscussionTalentListResult(call.Result, listed)
			call.Status = firstNonBlank(call.Status, "completed")
		case string(toolstd.DiscussionAck):
			if !hasTarget || strings.TrimSpace(source.MessageID) == "" {
				*call = toolCallWithFailure(*call, fmt.Errorf("%w: discussion_ack requires a source message and persisted agent member", ErrConversationMember))
				continue
			}
			emoji, err := s.saveDiscussionAcknowledgement(ctx, conversationID, source, target, *call, at)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				*call = toolCallWithFailure(*call, err)
				continue
			}
			if call.Result == nil {
				call.Result = map[string]string{}
			}
			call.Result["emoji"] = emoji
			call.Status = firstNonBlank(call.Status, "completed")
		case string(toolstd.DiscussionMemberAdd):
			added, err := s.addDiscussionMembersFromToolCall(ctx, conversationID, *call, at)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				*call = toolCallWithFailure(*call, err)
				continue
			}
			if call.Result == nil {
				call.Result = map[string]string{}
			}
			writeDiscussionMemberAddResult(call.Result, added)
			call.Status = firstNonBlank(call.Status, "completed")
		}
	}
	return out, nil
}

func (s *Service) applyBoundDiscussionMentionToolCalls(ctx context.Context, conversationID string, agentID string, source MessageRecord, target ConversationMemberRecord, calls []ToolCallDTO, boundTools []DiscussionAgentBoundTool, at time.Time, mentionRequests ...DiscussionAgentMentionRequest) ([]LinkedMissionDTO, error) {
	if err := validateBoundToolCalls(calls, boundTools); err != nil {
		return nil, err
	}
	var missionCalls []ToolCallDTO
	for i := range calls {
		call := &calls[i]
		switch normalizeToolName(call.Name) {
		case string(toolstd.DiscussionTalentList):
			listed, err := s.listDiscussionTalentFromToolCall(ctx, conversationID, *call)
			if err != nil {
				return nil, err
			}
			if call.Result == nil {
				call.Result = map[string]string{}
			}
			writeDiscussionTalentListResult(call.Result, listed)
			call.Status = firstNonBlank(call.Status, "completed")
		case string(toolstd.DiscussionAck):
			emoji, err := s.saveDiscussionAcknowledgement(ctx, conversationID, source, target, *call, at)
			if err != nil {
				return nil, err
			}
			if call.Result == nil {
				call.Result = map[string]string{}
			}
			call.Result["emoji"] = emoji
			call.Status = firstNonBlank(call.Status, "completed")
		case string(toolstd.DiscussionMemberAdd):
			added, err := s.addDiscussionMembersFromToolCall(ctx, conversationID, *call, at)
			if err != nil {
				return nil, err
			}
			if call.Result == nil {
				call.Result = map[string]string{}
			}
			writeDiscussionMemberAddResult(call.Result, added)
			call.Status = firstNonBlank(call.Status, "completed")
		default:
			if isDiscussionMissionTool(call.Name) {
				missionCalls = append(missionCalls, *call)
			}
		}
	}
	if len(missionCalls) == 0 {
		return nil, nil
	}
	if s == nil || s.missionTooling == nil {
		return nil, fmt.Errorf("%w: mission tooling is required for %s", ErrServiceDependency, missionCalls[0].Name)
	}
	view, err := s.repo.GetConversation(ctx, strings.TrimSpace(conversationID))
	if err != nil {
		return nil, err
	}
	viewer := discussionMentionMissionToolViewer(agentID, view, mentionRequests...)
	human := firstHumanAgent(view.Messages)
	if len(mentionRequests) > 0 && mentionRequests[0].PersonalAssistantHost {
		request := mentionRequests[0]
		human.AgentID = firstNonBlank(request.PersonalAssistantOriginSubjectID, view.PersonalAssistantOriginSubjectID, human.AgentID)
		human.DisplayName = firstNonBlank(request.SourceMessage.AuthorName, human.DisplayName)
	}
	toolResults := toolCallResultsWithInitialHumanAgent(missionCalls, human.AgentID, human.DisplayName)
	applied, err := s.missionTooling.Apply(ctx, viewer, conversationID, toolResults, mapMissionLinks(view.LinkedMissions)...)
	if err != nil {
		return nil, missionToolingError(missionCalls, err)
	}
	appliedCalls := toolCallsWithAppliedResults(missionCalls, toolResults)
	appliedByID := map[string]ToolCallDTO{}
	for _, call := range appliedCalls {
		appliedByID[firstNonBlank(call.ToolCallID, call.Name)] = call
	}
	for i := range calls {
		key := firstNonBlank(calls[i].ToolCallID, calls[i].Name)
		if appliedCall, ok := appliedByID[key]; ok {
			calls[i] = appliedCall
		}
	}
	return applied, nil
}

func discussionMentionMissionToolViewer(agentID string, view ConversationRecordView, mentionRequests ...DiscussionAgentMentionRequest) missioncontrol.ViewerRef {
	if len(mentionRequests) == 0 || !mentionRequests[0].PersonalAssistantHost {
		return discussionAgentViewer(agentID, view.TenantID)
	}
	request := mentionRequests[0]
	principalID := firstNonBlank(request.PersonalAssistantOriginSubjectID, view.PersonalAssistantOriginSubjectID)
	if principalID == "" {
		return discussionAgentViewer(agentID, view.TenantID)
	}
	viewer := missionViewer(ViewerRef{
		ViewerID:       principalID,
		AgentProfileID: principalID,
		OwnerID:        firstNonBlank(request.PersonalAssistantOriginOwnerID, view.PersonalAssistantOriginOwnerID, request.ConversationOwnerID),
		ViewerKind:     ViewerKindUser,
		TenantID:       firstNonBlank(request.ConversationTenantID, view.TenantID),
	})
	viewer.AuthorizationSubjectID = principalID
	return viewer
}

func conversationAgentMember(members []ConversationMemberRecord, agentID string) (ConversationMemberRecord, bool) {
	agentID = strings.TrimSpace(agentID)
	for _, member := range members {
		if strings.TrimSpace(member.MemberKind) == "agent" && strings.TrimSpace(member.MemberID) == agentID {
			return member, true
		}
	}
	return ConversationMemberRecord{}, false
}

func isDiscussionMissionTool(name string) bool {
	switch normalizeToolName(name) {
	case string(toolstd.MissionContextList),
		string(toolstd.MissionContextSelect),
		string(toolstd.MissionPrepare),
		string(toolstd.MissionBriefCreate),
		string(toolstd.MissionTaskCreateRoot),
		string(toolstd.MissionTaskDelegate),
		string(toolstd.MissionCoordinatorPlanParallel),
		string(toolstd.DiscussionClose):
		return true
	default:
		return false
	}
}

func discussionMissionToolCalls(calls []ToolCallDTO) []ToolCallDTO {
	out := make([]ToolCallDTO, 0, len(calls))
	for _, call := range calls {
		if isDiscussionMissionTool(call.Name) {
			out = append(out, call)
		}
	}
	return out
}

func (s *Service) saveDiscussionAcknowledgement(ctx context.Context, conversationID string, source MessageRecord, target ConversationMemberRecord, call ToolCallDTO, at time.Time) (string, error) {
	if s == nil || s.repo == nil {
		return "", ErrServiceDependency
	}
	emoji := discussionAckEmoji(call)
	eventID, err := s.newID()
	if err != nil {
		return "", err
	}
	display := strings.TrimSpace(target.DisplayName)
	if display == "" {
		display = strings.TrimSpace(target.MemberID)
	}
	event := EventRecord{
		EventID:        eventID,
		ConversationID: conversationID,
		Kind:           "discussion_agent_acknowledged",
		Status:         "completed",
		Summary:        display + " 已收到",
		Metadata: map[string]string{
			"tool_call_id":      strings.TrimSpace(call.ToolCallID),
			"source_message_id": strings.TrimSpace(source.MessageID),
			"source_agent_id":   strings.TrimSpace(source.AuthorID),
			"target_agent_id":   strings.TrimSpace(target.MemberID),
			"target_name":       display,
			"emoji":             emoji,
		},
		CreatedAt: at,
	}
	if err := s.repo.SaveEvent(ctx, event); err != nil {
		return "", err
	}
	return emoji, nil
}

func discussionAckEmoji(call ToolCallDTO) string {
	emoji := strings.TrimSpace(firstNonBlank(call.Arguments["emoji"], call.Arguments["reaction"], call.Arguments["status_emoji"]))
	if emoji == "" {
		return "👍"
	}
	runes := []rune(emoji)
	if len(runes) > 8 {
		return strings.TrimSpace(string(runes[:8]))
	}
	return emoji
}

func writeDiscussionMemberAddResult(result map[string]string, added discussionMemberAddResult) {
	result["added_count"] = strconv.Itoa(added.Added)
	if len(added.IDs) > 0 {
		result["added_agent_ids"] = strings.Join(added.IDs, ",")
	}
	if len(added.Names) > 0 {
		result["added_agent_names"] = strings.Join(added.Names, ",")
	}
	if len(added.Roles) > 0 {
		result["added_agent_roles"] = strings.Join(added.Roles, ",")
	}
}

func (s *Service) applyRuntimeToolCall(ctx context.Context, conversationID string, runID string, agentID string, principal ViewerRef, sourceMessageID string, mode string, call ToolCallDTO, boundTools []DiscussionAgentBoundTool, at time.Time) (ToolCallDTO, []LinkedMissionDTO, error) {
	if s == nil || s.repo == nil {
		return call, nil, ErrServiceDependency
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return call, nil, ErrInvalidRequest
	}
	view, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return call, nil, err
	}
	principal, err = runtimeToolPrincipalForConversation(view, principal)
	if err != nil {
		return call, nil, err
	}
	if at.IsZero() {
		at = s.now()
	}
	boundTools = appendDiscussionAgentBoundTools(boundTools, nil)
	if err := validateBoundToolCalls([]ToolCallDTO{call}, boundTools); err != nil {
		return call, nil, err
	}
	source := sourceMessageByID(view.Messages, sourceMessageID)
	members := append([]ConversationMemberRecord(nil), view.Members...)
	agentID = firstNonBlank(agentID, runAgentIDByID(view.Runs, runID))
	if strings.EqualFold(strings.TrimSpace(mode), "mention") {
		target, ok := conversationAgentMember(members, agentID)
		if !ok {
			return call, nil, fmt.Errorf("%w: agent %s is not a persisted discussion member", ErrConversationMember, agentID)
		}
		calls := []ToolCallDTO{call}
		linkedMissions, err := s.applyBoundDiscussionMentionToolCalls(ctx, conversationID, agentID, source, target, calls, boundTools, at)
		if err != nil {
			return call, nil, err
		}
		if len(calls) > 0 {
			call = calls[0]
		}
		return call, linkedMissions, nil
	}
	calls, err := s.applyDiscussionRunLocalToolCalls(ctx, conversationID, agentID, source, members, []ToolCallDTO{call}, at)
	if err != nil {
		return call, nil, err
	}
	if len(calls) > 0 {
		call = calls[0]
	}
	if !isDiscussionMissionTool(call.Name) {
		return call, nil, nil
	}
	if s.missionTooling == nil {
		return call, nil, fmt.Errorf("%w: mission tooling is required for %s", ErrServiceDependency, call.Name)
	}
	afterView, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return call, nil, err
	}
	human := firstHumanAgent(afterView.Messages)
	toolResults := toolCallResultsWithInitialHumanAgent([]ToolCallDTO{call}, human.AgentID, human.DisplayName)
	executionViewer := runtimeToolMissionViewer(agentID, view.TenantID, principal)
	toolCtx := withMissionToolParentRun(ctx, runID)
	linkedMissions, err := s.missionTooling.Apply(toolCtx, executionViewer, conversationID, toolResults, mapMissionLinks(afterView.LinkedMissions)...)
	if err != nil {
		return call, nil, missionToolingError([]ToolCallDTO{call}, err)
	}
	applied := toolCallsWithAppliedResults([]ToolCallDTO{call}, toolResults)
	if len(applied) > 0 {
		call = applied[0]
	}
	if err := s.persistRuntimeToolCallSideEffects(ctx, conversationID, runID, call, linkedMissions, at); err != nil {
		return call, nil, err
	}
	return call, linkedMissions, nil
}
