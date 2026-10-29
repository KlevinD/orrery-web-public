package discussionsession

import (
	"context"
	"strings"
	"time"
)

func terminalDiscussionTurnRuns(runs []RunRecord, turnID, turnStatus string, completedAt time.Time) []RunRecord {
	turnStatus = strings.ToLower(strings.TrimSpace(turnStatus))
	if turnStatus != "failed" && turnStatus != "cancelled" && turnStatus != "canceled" {
		return nil
	}
	terminalStatus := "failed"
	if turnStatus == "cancelled" || turnStatus == "canceled" {
		terminalStatus = "cancelled"
	}
	terminal := make([]RunRecord, 0)
	for _, run := range runs {
		if strings.TrimSpace(run.TurnID) != strings.TrimSpace(turnID) || !progressStatusIsActive(run.Status) {
			continue
		}
		run.Status = terminalStatus
		if terminalStatus == "cancelled" {
			cancelRunProgress(&run)
		} else {
			failRunProgress(&run)
		}
		if run.CompletedAt.IsZero() {
			run.CompletedAt = completedAt
		}
		if run.DurationMs <= 0 && !run.StartedAt.IsZero() && run.CompletedAt.After(run.StartedAt) {
			run.DurationMs = run.CompletedAt.Sub(run.StartedAt).Milliseconds()
		}
		terminal = append(terminal, run)
	}
	return terminal
}

func discussionResponseHasPlan(response DiscussionAgentResponse) bool {
	for _, segment := range response.DisplayTimeline {
		if segment.Plan != nil || strings.EqualFold(strings.TrimSpace(segment.Kind), DiscussionAgentStreamPlan) {
			return true
		}
	}
	return false
}

func (s *Service) createDiscussionTurnRun(ctx context.Context, turnID string, mode string, conversationID string, agentID string, source MessageRecord, after time.Time) (RunRecord, error) {
	runID, err := s.newID()
	if err != nil {
		return RunRecord{}, err
	}
	runStartedAt := firstTimeAfter(s.now(), after)
	run := RunRecord{
		RunID:          runID,
		ConversationID: conversationID,
		TurnID:         strings.TrimSpace(turnID),
		MissionID:      strings.TrimSpace(source.MissionID),
		MissionTaskID:  strings.TrimSpace(source.MissionTaskID),
		Mode:           strings.TrimSpace(mode),
		AgentID:        agentID,
		Status:         "running",
		CreatedAt:      runStartedAt,
		StartedAt:      runStartedAt,
	}
	return s.saveDiscussionTargetRun(ctx, run)
}

func (s *Service) createDiscussionTargetRun(ctx context.Context, conversationID string, agentID string, source MessageRecord, after time.Time, initialLatestAt time.Time) (RunRecord, error) {
	return s.createDiscussionTargetRunForTurn(ctx, source.TurnID, "mention", conversationID, agentID, source, after, initialLatestAt)
}

func (s *Service) createDiscussionTargetRunForTurn(ctx context.Context, turnID string, mode string, conversationID string, agentID string, source MessageRecord, after time.Time, initialLatestAt time.Time) (RunRecord, error) {
	runID, err := s.newID()
	if err != nil {
		return RunRecord{}, err
	}
	runStartedAt := firstTimeAfter(firstTime(initialLatestAt, after, s.now()), source.CreatedAt)
	run := RunRecord{
		RunID:          runID,
		ConversationID: conversationID,
		TurnID:         strings.TrimSpace(turnID),
		MissionID:      strings.TrimSpace(source.MissionID),
		MissionTaskID:  strings.TrimSpace(source.MissionTaskID),
		Mode:           strings.TrimSpace(mode),
		AgentID:        agentID,
		Status:         "running",
		CreatedAt:      runStartedAt,
		StartedAt:      runStartedAt,
	}
	return s.saveDiscussionTargetRun(ctx, run)
}

func (s *Service) saveDiscussionTargetRun(ctx context.Context, run RunRecord) (RunRecord, error) {
	current, err := s.repo.GetConversation(ctx, run.ConversationID)
	if err != nil {
		return RunRecord{}, err
	}
	if isPersonalAssistantConversationKind(current.ConversationKind) {
		run.LogicalAgentID = PersonalAssistantAgentID(current.TenantID, personalAssistantRuntimeOwner(current.ConversationRecord))
	} else if isPersonalAssistantRuntimeConversation(current.ConversationRecord) && (strings.EqualFold(strings.TrimSpace(run.Mode), string(discussionTurnTargetGroup)) || isPersonalAssistantApprovalHostRun(current, run)) {
		run.LogicalAgentID = PersonalAssistantAgentID(current.TenantID, personalAssistantRuntimeOwner(current.ConversationRecord))
	} else if isPersonalAssistantRuntimeConversation(current.ConversationRecord) {
		// Do not inherit a host assistant identity into a worker/approval target.
		run.LogicalAgentID = ""
	}
	if isPersonalAssistantRuntimeRun(run) {
		run.AgentName = conversationRunAgentName(current, run)
		displayName, err := s.configuredRunAgentDisplayNameForConversation(ctx, current)
		if err != nil {
			return RunRecord{}, err
		}
		run.AgentName = firstNonBlank(displayName, run.AgentName)
	} else if strings.TrimSpace(run.AgentName) == "" {
		run.AgentName = conversationRunAgentName(current, run)
	}
	if err := s.repo.SaveRun(ctx, run); err != nil {
		return RunRecord{}, err
	}
	if err := s.appendRealtimeRunStarted(ctx, run); err != nil {
		return RunRecord{}, err
	}
	return run, nil
}

func discussionTargetRequestFromRun(mode string, view ConversationRecordView, source MessageRecord, target ConversationMemberRecord, run RunRecord) DiscussionAgentMentionRequest {
	request := DiscussionAgentMentionRequest{
		ConversationID:                   view.ConversationID,
		ConversationKind:                 view.ConversationKind,
		ConversationOwnerID:              firstNonBlank(view.OwnerID, view.CreatedBy),
		ConversationTenantID:             view.TenantID,
		HILMode:                          normalizeDiscussionHILMode(view.HILMode),
		PersonalAssistantOriginOwnerID:   view.PersonalAssistantOriginOwnerID,
		PersonalAssistantOriginSubjectID: view.PersonalAssistantOriginSubjectID,
		RunID:                            run.RunID,
		Mode:                             mode,
		Content:                          source.Content,
		SourceMessage:                    mapMessagesWithAttachments([]MessageRecord{source}, view.Attachments)[0],
		TargetAgent:                      mapConversationMembers([]ConversationMemberRecord{target})[0],
		History:                          mapMessagesWithAttachments(view.Messages, view.Attachments),
		Runs:                             append([]RunRecord(nil), view.Runs...),
		Members:                          mapConversationMembers(view.Members),
		LinkedMissions:                   mapMissionLinks(view.LinkedMissions),
		Attachments:                      attachmentsForMessage(view.Attachments, source.MessageID),
	}
	if isPersonalAssistantApprovalHost(view, mode, target, run) {
		request.PersonalAssistantHost = true
		request.LogicalAgentID = PersonalAssistantAgentID(view.TenantID, personalAssistantRuntimeOwner(view.ConversationRecord))
	}
	return request
}

// A PA-origin Mission remains a group, but approval continuation for its
// default Lead host must resume through the user's PA runtime. Direct and
// worker targets intentionally remain physical-agent requests.
func isPersonalAssistantApprovalHost(view ConversationRecordView, mode string, target ConversationMemberRecord, run RunRecord) bool {
	if !strings.EqualFold(strings.TrimSpace(mode), discussionApprovalContinuationMode) || !isPersonalAssistantRuntimeConversation(view.ConversationRecord) {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(target.MemberKind), string(ViewerKindAgent)) || !strings.EqualFold(strings.TrimSpace(target.Role), "Lead") {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(target.MemberID), strings.TrimSpace(run.AgentID))
}

func isPersonalAssistantApprovalHostRun(view ConversationRecordView, run RunRecord) bool {
	target, ok := conversationAgentMember(view.Members, run.AgentID)
	return ok && isPersonalAssistantApprovalHost(view, run.Mode, target, run)
}
