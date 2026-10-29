package discussionsession

import (
	"context"
	"strings"
	"time"
)

func (s *Service) beginRun(ctx context.Context, viewer ViewerRef, cmd SendMessageCommand) (string, MessageRecord, RunRecord, error) {
	conversationID, userMessage, err := s.beginUserMessage(ctx, viewer, cmd)
	if err != nil {
		return "", MessageRecord{}, RunRecord{}, err
	}
	conversation, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return "", MessageRecord{}, RunRecord{}, err
	}
	runAgentID, err := s.configuredRunAgentIDForConversation(ctx, conversation)
	if err != nil {
		return "", MessageRecord{}, RunRecord{}, err
	}
	run, err := s.createDiscussionRun(ctx, conversationID, runAgentID, userMessage.CreatedAt)
	if err != nil {
		return "", MessageRecord{}, RunRecord{}, err
	}
	return conversationID, userMessage, run, nil
}

func (s *Service) beginUserMessage(ctx context.Context, viewer ViewerRef, cmd SendMessageCommand) (string, MessageRecord, error) {
	now := s.now()
	conversationID := strings.TrimSpace(cmd.ConversationID)
	createdConversation := false
	var pendingConversation ConversationRecord
	var pendingMembers []ConversationMemberRecord
	if conversationID == "" {
		if isPersonalAssistantConversationKind(cmd.ConversationKind) && !cmd.ForceNewConversation {
			if existing, found, err := s.findLatestPersonalAssistantConversation(ctx, viewer); err != nil {
				return "", MessageRecord{}, err
			} else if found {
				conversationID = existing.ConversationID
			}
		}
		if target := directTargetFromCommand(cmd); target.ID != "" && !cmd.ForceNewConversation {
			if existing, found, err := s.findExistingDirectConversation(ctx, viewer, target); err != nil {
				return "", MessageRecord{}, err
			} else if found {
				conversationID = existing.ConversationID
			}
		}
		if conversationID == "" {
			id, err := s.newID()
			if err != nil {
				return "", MessageRecord{}, err
			}
			conversationID = id
			conversation, members, err := s.newConversationWithMembers(ctx, conversationID, viewer, cmd, now)
			if err != nil {
				return "", MessageRecord{}, err
			}
			if creatorName := conversationMemberName(members, "user", viewer.ViewerID); creatorName != "" {
				conversation.CreatedByName = creatorName
			}
			if err := s.applyRequestedMessageWorkspace(ctx, viewer, cmd, &conversation); err != nil {
				return "", MessageRecord{}, err
			}
			pendingConversation = conversation
			pendingMembers = members
			createdConversation = true
		}
	} else if _, err := s.requireConversationAccess(ctx, viewer, conversationID); err != nil {
		return "", MessageRecord{}, err
	}
	var current ConversationRecordView
	if createdConversation {
		current = ConversationRecordView{ConversationRecord: pendingConversation, Members: pendingMembers}
	} else {
		var err error
		current, err = s.repo.GetConversation(ctx, conversationID)
		if err != nil {
			return "", MessageRecord{}, err
		}
	}
	if err := s.applyRequestedMessageWorkspace(ctx, viewer, cmd, &current.ConversationRecord); err != nil {
		return "", MessageRecord{}, err
	}
	if tenantID, ok := legacyDirectConversationTenant(current, viewer); ok {
		current.TenantID = tenantID
		if strings.TrimSpace(current.HILMode) == "" {
			current.HILMode = DiscussionHILModeManual
		}
	}
	if strings.TrimSpace(current.OwnerID) == "" {
		current.OwnerID = firstNonBlank(viewer.OwnerID, current.CreatedBy)
	}
	if strings.TrimSpace(cmd.ConversationKind) != "" {
		conversationKind, ok := normalizeCreatableConversationKind(cmd.ConversationKind)
		if !ok || normalizedStoredConversationKind(current.ConversationKind) != conversationKind {
			return "", MessageRecord{}, ErrInvalidRequest
		}
	}
	if !createdConversation {
		if err := s.recoverStaleActiveTurn(ctx, &current, now); err != nil {
			return "", MessageRecord{}, err
		}
		if conversationHasPendingRuntimeApproval(current) {
			return "", MessageRecord{}, ErrRuntimeApprovalPending
		}
	}
	materializedDraft := strings.EqualFold(strings.TrimSpace(current.Status), "draft")
	if materializedDraft {
		if !isPersonalAssistantConversationKind(current.ConversationKind) {
			if title := trimmedDiscussionTitle(cmd.Content); title != "" {
				current.Title = title
			}
		}
		current.Status = "running"
		current.UpdatedAt = now
	}
	userAuthorName, err := requiredConversationViewerName(current.Members, viewer)
	if err != nil {
		return "", MessageRecord{}, err
	}
	userMessageID, err := s.newID()
	if err != nil {
		return "", MessageRecord{}, err
	}
	attachmentIDs, err := s.messageAttachmentIDs(ctx, conversationID, userMessageID, cmd.AttachmentIDs)
	if err != nil {
		return "", MessageRecord{}, err
	}
	mentions, err := normalizeMessageMentions(cmd.Mentions, current.Members)
	if err != nil {
		return "", MessageRecord{}, err
	}
	userMessage := MessageRecord{
		MessageID:            userMessageID,
		ConversationID:       conversationID,
		TurnID:               stableDiscussionTurnID(DiscussionTurnOriginMessage, conversationID, userMessageID, cmd.IdempotencyKey),
		MissionID:            strings.TrimSpace(cmd.MissionID),
		MissionTaskID:        strings.TrimSpace(cmd.MissionTaskID),
		Role:                 "user",
		AuthorID:             viewer.ViewerID,
		AuthorName:           userAuthorName,
		Content:              strings.TrimSpace(cmd.Content),
		Mentions:             mentions,
		RoutingSchemaVersion: 1,
		CreatedAt:            now,
	}
	boundAttachments := make([]AttachmentRecord, 0, len(attachmentIDs))
	for _, attachmentID := range attachmentIDs {
		attachment, getErr := s.repo.GetAttachment(ctx, attachmentID)
		if getErr != nil {
			return "", MessageRecord{}, getErr
		}
		attachment.MessageID = userMessageID
		boundAttachments = append(boundAttachments, attachment)
	}
	committed, err := s.repo.ApplyTurnMutation(ctx, TurnAggregateMutation{
		Conversation:        current.ConversationRecord,
		ExpectedRevision:    current.Revision,
		Messages:            []MessageRecord{userMessage},
		Members:             pendingMembers,
		Attachments:         boundAttachments,
		RequireNoActiveTurn: true,
	})
	if err != nil {
		return "", MessageRecord{}, err
	}
	current.ConversationRecord = committed
	if createdConversation {
		s.writeConversationAuthorization(ctx, conversationID)
	}
	if err := s.appendRealtimeMessageCreated(ctx, userMessage, viewer, createdConversation || materializedDraft); err != nil {
		return "", MessageRecord{}, err
	}
	return conversationID, userMessage, nil
}

func (s *Service) recoverStaleActiveTurn(ctx context.Context, current *ConversationRecordView, now time.Time) error {
	if s == nil || s.repo == nil || current == nil || strings.TrimSpace(current.ActiveTurnID) == "" {
		return nil
	}
	_, err := s.recoverInterruptedTurn(ctx, current.ConversationID, current.ActiveTurnID, now)
	if err != nil {
		return err
	}
	refreshed, err := s.repo.GetConversation(ctx, current.ConversationID)
	if err != nil {
		return err
	}
	*current = refreshed
	if strings.TrimSpace(current.ActiveTurnID) != "" {
		return ErrCommandInProgress
	}
	return nil
}

func (s *Service) messageAttachmentIDs(ctx context.Context, conversationID string, messageID string, attachmentIDs []string) ([]string, error) {
	if len(attachmentIDs) == 0 {
		return nil, nil
	}
	if s == nil || s.repo == nil {
		return nil, ErrServiceDependency
	}
	seen := map[string]bool{}
	ids := make([]string, 0, len(attachmentIDs))
	for _, raw := range attachmentIDs {
		attachmentID := strings.TrimSpace(raw)
		if attachmentID == "" || seen[attachmentID] {
			continue
		}
		seen[attachmentID] = true
		ids = append(ids, attachmentID)
	}
	for _, attachmentID := range ids {
		record, err := s.repo.GetAttachment(ctx, attachmentID)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(record.ConversationID) != strings.TrimSpace(conversationID) {
			return nil, ErrForbidden
		}
		if strings.TrimSpace(record.MessageID) != "" && strings.TrimSpace(record.MessageID) != strings.TrimSpace(messageID) {
			return nil, ErrForbidden
		}
	}
	return ids, nil
}

func (s *Service) createDiscussionRun(ctx context.Context, conversationID string, agentID string, after time.Time) (RunRecord, error) {
	runID, err := s.newID()
	if err != nil {
		return RunRecord{}, err
	}
	runStartedAt := firstTimeAfter(s.now(), after)
	current, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return RunRecord{}, err
	}
	run := RunRecord{RunID: runID, ConversationID: conversationID, AgentID: agentID, Status: "running", CreatedAt: runStartedAt, StartedAt: runStartedAt}
	if isPersonalAssistantRuntimeConversation(current.ConversationRecord) {
		run.LogicalAgentID = PersonalAssistantAgentID(current.TenantID, personalAssistantRuntimeOwner(current.ConversationRecord))
	}
	run.AgentName = conversationRunAgentName(current, run)
	if isPersonalAssistantRuntimeRun(run) {
		displayName, err := s.configuredRunAgentDisplayNameForConversation(ctx, current)
		if err != nil {
			return RunRecord{}, err
		}
		run.AgentName = firstNonBlank(displayName, run.AgentName)
		if isPersonalAssistantConversationKind(current.ConversationKind) && displayName != "" {
			current.Title = displayName
		}
	}
	if err := s.repo.SaveRun(ctx, run); err != nil {
		return RunRecord{}, err
	}
	current.Status = "running"
	current.UpdatedAt = runStartedAt
	if err := s.repo.SaveConversation(ctx, current.ConversationRecord); err != nil {
		return RunRecord{}, err
	}
	if err := s.appendRealtimeRunStarted(ctx, run); err != nil {
		return RunRecord{}, err
	}
	return run, nil
}

func (s *Service) persistFailedRun(ctx context.Context, conversationID string, run RunRecord, reason error) error {
	_, err := s.persistFailedRunWithResponse(ctx, conversationID, run, DiscussionAgentResponse{}, reason)
	return err
}

func (s *Service) persistFailedRunWithResponse(ctx context.Context, conversationID string, run RunRecord, response DiscussionAgentResponse, reason error) (*MessageDTO, error) {
	if !s.isRunCurrent(ctx, run) {
		return nil, nil
	}
	applyDiscussionResponseOutputPolicy(&response)
	run.ProviderMetadata = mergeOutputPrivacyMetadata(run.ProviderMetadata, response.ProviderMetadata)
	s.applyResponseDebug(&run, response)
	applyResponseRunProgress(&run, response)
	failRunProgress(&run)
	terminateResponseProgress(&response, "failed")
	failedAt := s.now()
	partialMessage, err := s.persistPartialAssistantMessage(ctx, conversationID, run, response, failedAt, nil, nil)
	if err != nil {
		return nil, err
	}
	run.Status = "failed"
	run.CompletedAt = failedAt
	if err := s.repo.SaveRun(ctx, run); err != nil {
		return partialMessage, err
	}
	failed, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return partialMessage, err
	}
	failed.Status = "failed"
	failed.UpdatedAt = failedAt
	if err := s.repo.SaveConversation(ctx, failed.ConversationRecord); err != nil {
		return partialMessage, err
	}
	if err := s.appendRealtimeRunFailed(ctx, run); err != nil {
		return partialMessage, err
	}
	if err := s.saveRuntimeFailureEvent(ctx, conversationID, run, reason, failedAt); err != nil {
		return partialMessage, err
	}
	if err := s.appendRealtimeConversationListInvalidated(ctx, failed, "run-failed-"+run.RunID); err != nil {
		return partialMessage, err
	}
	return partialMessage, nil
}

func (s *Service) markRunOrchestrationFailed(ctx context.Context, conversationID string, run RunRecord, failedAt time.Time, reason error) error {
	run.Status = "failed"
	failRunProgress(&run)
	if run.CompletedAt.IsZero() {
		run.CompletedAt = failedAt
	}
	if err := s.repo.SaveRun(ctx, run); err != nil {
		return err
	}
	failed, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	failed.Status = "failed"
	failed.UpdatedAt = firstTime(failedAt, s.now())
	if err := s.repo.SaveConversation(ctx, failed.ConversationRecord); err != nil {
		return err
	}
	if err := s.appendRealtimeRunFailed(ctx, run); err != nil {
		return err
	}
	if err := s.appendRealtimeConversationListInvalidated(ctx, failed, "run-failed-"+run.RunID); err != nil {
		return err
	}
	if reason == nil {
		return nil
	}
	event := EventRecord{
		EventID:        "discussion-orchestration-incomplete-" + run.RunID,
		ConversationID: conversationID,
		Kind:           "discussion_orchestration_incomplete",
		Status:         "failed",
		Summary:        reason.Error(),
		Metadata: map[string]string{
			"run_id":   run.RunID,
			"agent_id": run.AgentID,
		},
		CreatedAt: failed.UpdatedAt,
	}
	return s.repo.SaveEvent(ctx, event)
}

func (s *Service) markCompletedConversationViewed(ctx context.Context, view *ConversationRecordView) error {
	if s == nil || s.repo == nil || view == nil || !isUnreadCompletedConversation(view.ConversationRecord) {
		return nil
	}
	view.ViewedAt = firstTimeAfter(s.now(), view.UpdatedAt)
	return s.repo.SaveConversation(ctx, view.ConversationRecord)
}

func (s *Service) forwardDirectHumanMentionStream(ctx context.Context, out chan<- StreamEventDTO, conversationID string, source MessageRecord, releaseRunLifecycle func()) {
	defer close(out)
	defer releaseRunLifecycle()
	send := func(event StreamEventDTO) bool {
		select {
		case <-ctx.Done():
			return false
		case out <- event:
			if s.streams != nil {
				s.streams.publish(conversationID, event)
			}
			return true
		}
	}
	if snapshot, err := s.repo.GetConversation(ctx, conversationID); err == nil {
		dto := mapConversationDTO(snapshot)
		if !send(StreamEventDTO{Kind: StreamEventSnapshot, Conversation: &dto}) {
			return
		}
	}
	if _, err := s.completeDirectHumanMentions(context.WithoutCancel(ctx), conversationID, source, send); err != nil {
		_ = send(StreamEventDTO{Kind: StreamEventError, Error: err.Error()})
		return
	}
}

func (s *Service) completeRun(ctx context.Context, conversationID string, run RunRecord, response DiscussionAgentResponse) (*MessageDTO, *RunDTO, error) {
	unlock := s.lockRunLifecycle(conversationID)
	defer unlock()
	request := DiscussionAgentRequest{AgentID: run.AgentID, BoundTools: response.BoundTools}
	if err := s.prepareDiscussionAgentRequest(ctx, &request); err != nil {
		return nil, nil, err
	}
	return s.completeRunLocked(ctx, conversationID, run, response, request)
}

func (s *Service) completeRunLocked(ctx context.Context, conversationID string, run RunRecord, response DiscussionAgentResponse, request DiscussionAgentRequest) (resultMessage *MessageDTO, resultRun *RunDTO, err error) {
	finishStage := s.startDiscussionStage(ctx, conversationID, run.TurnID, run.RunID, "run_finalize")
	defer func() { finishStage(err) }()
	if !s.isRunCurrent(ctx, run) {
		return nil, nil, ErrNotFound
	}
	applyDiscussionResponseOutputPolicy(&response)
	response.ProviderMetadata = mergeOutputPrivacyMetadata(run.ProviderMetadata, response.ProviderMetadata)
	completedAt := firstTimeAfter(s.now(), run.StartedAt)
	run.Status = firstNonBlank(response.Status, "completed")
	applyResponseRunProgress(&run, response)
	run.RuntimeKind = response.RuntimeKind
	run.SessionID = response.SessionID
	run.ProviderMetadata = cloneStringMap(response.ProviderMetadata)
	run.CompletedAt = completedAt
	s.applyResponseDebug(&run, response)
	applyResponseTelemetry(&run, response)
	if !s.isRunCurrent(ctx, run) {
		return nil, nil, ErrNotFound
	}
	if err := s.saveRunAndRuntimeApprovalEvent(ctx, conversationID, run, response, completedAt); err != nil {
		return nil, nil, err
	}
	if err := s.appendRealtimeToolEvents(ctx, conversationID, run.RunID, run.ToolCalls); err != nil {
		return nil, nil, err
	}
	current, getErr := s.repo.GetConversation(ctx, conversationID)
	if getErr != nil {
		return nil, nil, getErr
	}
	if discussionRunIsBlockedForRuntimeApproval(run, response) {
		return s.finishBlockedRuntimeApprovalRun(ctx, conversationID, current, run, response, completedAt, request.Workspace)
	}
	var orchestrationErr error
	var orchestrationWarning string
	if len(response.ToolCalls) > 0 {
		if !s.isRunCurrent(ctx, run) {
			return nil, nil, ErrNotFound
		}
		if err := validateBoundToolCalls(response.ToolCalls, boundToolsForResponse(request, response)); err != nil {
			if markErr := s.markRunOrchestrationFailed(ctx, conversationID, run, completedAt, err); markErr != nil {
				return nil, nil, markErr
			}
			s.logDiscussionRunFailure(ctx, run, err)
			return nil, nil, err
		}
	}
	sourceMessage, hasSourceMessage, err := s.userSourceMessageForRun(ctx, conversationID, run)
	if err != nil {
		return nil, nil, err
	}
	if len(response.ToolCalls) > 0 && !response.ToolLoopCompleted {
		source := MessageRecord{}
		if hasSourceMessage {
			source = sourceMessage
		}
		updated, err := s.applyDiscussionRunLocalToolsOrMarkFailed(ctx, conversationID, &run, &response, source, current.Members, completedAt)
		if err != nil {
			return nil, nil, err
		}
		current = updated
	}
	linkedMissions := mergeLinkedMissions(mapMissionLinks(current.LinkedMissions), response.LinkedMissions)
	if !response.ToolLoopCompleted {
		missionCalls := discussionMissionToolCalls(response.ToolCalls)
		if s.missionTooling != nil && len(missionCalls) > 0 {
			if !s.isRunCurrent(ctx, run) {
				return nil, nil, ErrNotFound
			}
			human := firstHumanAgent(current.Messages)
			toolResults := toolCallResultsWithInitialHumanAgent(missionCalls, human.AgentID, human.DisplayName)
			toolCtx := withMissionToolParentRun(ctx, run.RunID)
			applied, err := s.missionTooling.Apply(toolCtx, discussionMissionToolViewer(run, request, current), conversationID, toolResults, mapMissionLinks(current.LinkedMissions)...)
			if err != nil {
				diagnosticErr := missionToolingError(missionCalls, err)
				s.logDiscussionRunFailure(ctx, run, diagnosticErr)
				failedCalls := toolCallsWithFailure(missionCalls, diagnosticErr)
				response.ToolCalls = mergeAppliedContinuationToolCalls(response.ToolCalls, failedCalls)
				run.ToolCalls = mergeAppliedContinuationToolCalls(run.ToolCalls, failedCalls)
			} else {
				appliedCalls := toolCallsWithAppliedResults(missionCalls, toolResults)
				response.ToolCalls = mergeAppliedContinuationToolCalls(response.ToolCalls, appliedCalls)
				run.ToolCalls = mergeAppliedContinuationToolCalls(run.ToolCalls, appliedCalls)
				linkedMissions = append(linkedMissions, applied...)
			}
			if err := s.repo.SaveRun(ctx, run); err != nil {
				return nil, nil, err
			}
			if err := s.appendRealtimeToolEvents(ctx, conversationID, run.RunID, run.ToolCalls); err != nil {
				return nil, nil, err
			}
		}
	}
	if len(response.ToolCalls) > 0 {
		response, err = s.continueDiscussionRunAndSave(ctx, conversationID, &run, request, response, linkedMissions, completedAt)
		if err != nil {
			return nil, nil, err
		}
		linkedMissions = mergeLinkedMissions(linkedMissions, response.LinkedMissions)
		orchestrationWarning = orchestrationStopWarning(linkedMissions, response.ToolCalls, response.AssistantMessage)
		if err := orchestrationStopError(linkedMissions, response.ToolCalls, response.AssistantMessage); err != nil {
			orchestrationErr = err
		}
	}
	applyDiscussionResponseOutputPolicy(&response)
	run.ProviderMetadata = mergeOutputPrivacyMetadata(run.ProviderMetadata, response.ProviderMetadata)
	s.applyResponseDebug(&run, response)
	if !s.isRunCurrent(ctx, run) {
		return nil, nil, ErrNotFound
	}
	if err := s.saveRunMissionLinks(ctx, conversationID, linkedMissions, completedAt); err != nil {
		return nil, nil, err
	}
	if orchestrationErr != nil {
		if err := s.markRunOrchestrationFailed(ctx, conversationID, run, completedAt, orchestrationErr); err != nil {
			return nil, nil, err
		}
		s.logDiscussionRunFailure(ctx, run, orchestrationErr)
		return nil, nil, orchestrationErr
	}
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
		if orchestrationWarning != "" {
			if record.Metadata == nil {
				record.Metadata = map[string]string{}
			}
			record.Metadata[discussionOrchestrationWarningMetadataKey] = orchestrationWarning
		}
		finishMessage := s.startDiscussionStage(ctx, conversationID, run.TurnID, run.RunID, "assistant_message_save")
		dto, err := s.persistAssistantMessageWithWorkspaceFiles(ctx, record, request.Workspace)
		finishMessage(err)
		if err != nil {
			return nil, nil, err
		}
		assistantMessage = &dto
		finishMemory := s.startDiscussionStage(ctx, conversationID, run.TurnID, run.RunID, "memory_capture")
		memoryErr := s.capturePersonalAssistantMemoryExchange(context.WithoutCancel(ctx), current, run, request, sourceMessage, dto)
		finishMemory(memoryErr)
		if err := memoryErr; err != nil && s.logger != nil {
			s.logger.Warn(ctx, "personal assistant memory capture failed",
				"conversation_id", conversationID,
				"run_id", run.RunID,
				"logical_agent_id", firstNonBlank(request.LogicalAgentID, run.LogicalAgentID),
				"error", err,
			)
		}
	}
	if err := s.postDiscussionMentionMessage(context.WithoutCancel(ctx), run.AgentID, conversationID, response.AssistantMessage, linkedMissions, completedAt); err != nil {
		return nil, nil, err
	}
	if !s.isRunCurrent(ctx, run) {
		return nil, nil, ErrNotFound
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

func (s *Service) applyDiscussionRunLocalToolsAndSave(ctx context.Context, conversationID string, run *RunRecord, response *DiscussionAgentResponse, source MessageRecord, members []ConversationMemberRecord, at time.Time) (ConversationRecordView, error) {
	if run == nil || response == nil {
		return ConversationRecordView{}, ErrInvalidRequest
	}
	localCalls, err := s.applyDiscussionRunLocalToolCalls(ctx, conversationID, run.AgentID, source, members, response.ToolCalls, at)
	if err != nil {
		return ConversationRecordView{}, err
	}
	response.ToolCalls = localCalls
	run.ToolCalls = append([]ToolCallDTO(nil), localCalls...)
	if !s.isRunCurrent(ctx, *run) {
		return ConversationRecordView{}, ErrNotFound
	}
	if err := s.repo.SaveRun(ctx, *run); err != nil {
		return ConversationRecordView{}, err
	}
	if err := s.appendRealtimeToolEvents(ctx, conversationID, run.RunID, run.ToolCalls); err != nil {
		return ConversationRecordView{}, err
	}
	return s.repo.GetConversation(ctx, conversationID)
}

func (s *Service) applyDiscussionRunLocalToolsOrMarkFailed(ctx context.Context, conversationID string, run *RunRecord, response *DiscussionAgentResponse, source MessageRecord, members []ConversationMemberRecord, at time.Time) (ConversationRecordView, error) {
	updated, err := s.applyDiscussionRunLocalToolsAndSave(ctx, conversationID, run, response, source, members, at)
	if err == nil {
		return updated, nil
	}
	if run != nil {
		if markErr := s.markRunOrchestrationFailed(ctx, conversationID, *run, at, err); markErr != nil {
			return ConversationRecordView{}, markErr
		}
		s.logDiscussionRunFailure(ctx, *run, err)
	}
	return ConversationRecordView{}, err
}

func (s *Service) continueDiscussionRunAndSave(ctx context.Context, conversationID string, run *RunRecord, request DiscussionAgentRequest, response DiscussionAgentResponse, linkedMissions []LinkedMissionDTO, at time.Time) (DiscussionAgentResponse, error) {
	if run == nil {
		return response, ErrInvalidRequest
	}
	for {
		previousCalls := append([]ToolCallDTO(nil), response.ToolCalls...)
		continued, ok, err := s.continueDiscussionRunAfterToolResults(ctx, run, request, response, linkedMissions)
		if err != nil {
			if markErr := s.markRunOrchestrationFailed(ctx, conversationID, *run, at, err); markErr != nil {
				return response, markErr
			}
			s.logDiscussionRunFailure(ctx, *run, err)
			return response, err
		}
		if !ok {
			break
		}
		response = continued
		applyDiscussionResponseOutputPolicy(&response)
		response.ProviderMetadata = mergeOutputPrivacyMetadata(run.ProviderMetadata, response.ProviderMetadata)
		newCalls := newToolCallsAfterContinuation(previousCalls, response.ToolCalls)
		if len(newCalls) == 0 {
			response.ToolCalls = previousCalls
			break
		}
		sourceMessage, hasSourceMessage, err := s.userSourceMessageForRun(ctx, conversationID, *run)
		if err != nil {
			return response, err
		}
		source := MessageRecord{}
		if hasSourceMessage {
			source = sourceMessage
		}
		current, err := s.repo.GetConversation(ctx, conversationID)
		if err != nil {
			return response, err
		}
		localCalls, err := s.applyDiscussionRunLocalToolCalls(ctx, conversationID, run.AgentID, source, current.Members, newCalls, at)
		if err != nil {
			if markErr := s.markRunOrchestrationFailed(ctx, conversationID, *run, at, err); markErr != nil {
				return response, markErr
			}
			s.logDiscussionRunFailure(ctx, *run, err)
			return response, err
		}
		response.ToolCalls = mergeAppliedContinuationToolCalls(response.ToolCalls, localCalls)
		missionCalls := discussionMissionToolCalls(localCalls)
		if s.missionTooling != nil && len(missionCalls) > 0 {
			current, err = s.repo.GetConversation(ctx, conversationID)
			if err != nil {
				return response, err
			}
			human := firstHumanAgent(current.Messages)
			toolResults := toolCallResultsWithInitialHumanAgent(missionCalls, human.AgentID, human.DisplayName)
			toolCtx := withMissionToolParentRun(ctx, run.RunID)
			applied, err := s.missionTooling.Apply(toolCtx, discussionMissionToolViewer(*run, request, current), conversationID, toolResults, mapMissionLinks(current.LinkedMissions)...)
			if err != nil {
				diagnosticErr := missionToolingError(missionCalls, err)
				s.logDiscussionRunFailure(ctx, *run, diagnosticErr)
				failedCalls := toolCallsWithFailure(missionCalls, diagnosticErr)
				response.ToolCalls = mergeAppliedContinuationToolCalls(response.ToolCalls, failedCalls)
			} else {
				appliedCalls := toolCallsWithAppliedResults(missionCalls, toolResults)
				response.ToolCalls = mergeAppliedContinuationToolCalls(response.ToolCalls, appliedCalls)
				linkedMissions = mergeLinkedMissions(linkedMissions, applied)
			}
		}
		run.ToolCalls = mergeRunToolCalls(run.ToolCalls, response.ToolCalls)
		run.RuntimeKind = firstNonBlank(response.RuntimeKind, run.RuntimeKind)
		run.SessionID = firstNonBlank(response.SessionID, run.SessionID)
		run.ProviderMetadata = mergeOutputPrivacyMetadata(run.ProviderMetadata, response.ProviderMetadata)
		if err := s.repo.SaveRun(ctx, *run); err != nil {
			return response, err
		}
		if err := s.appendRealtimeToolEvents(ctx, conversationID, run.RunID, run.ToolCalls); err != nil {
			return response, err
		}
	}
	response.LinkedMissions = mergeLinkedMissions(response.LinkedMissions, linkedMissions)
	run.ToolCalls = mergeRunToolCalls(run.ToolCalls, response.ToolCalls)
	run.RuntimeKind = firstNonBlank(response.RuntimeKind, run.RuntimeKind)
	run.SessionID = firstNonBlank(response.SessionID, run.SessionID)
	run.ProviderMetadata = mergeOutputPrivacyMetadata(run.ProviderMetadata, response.ProviderMetadata)
	if err := s.repo.SaveRun(ctx, *run); err != nil {
		return response, err
	}
	if err := s.appendRealtimeToolEvents(ctx, conversationID, run.RunID, run.ToolCalls); err != nil {
		return response, err
	}
	return response, nil
}

func (s *Service) continueDiscussionRunAndMergeLinks(ctx context.Context, conversationID string, run *RunRecord, request DiscussionAgentRequest, response DiscussionAgentResponse, linkedMissions []LinkedMissionDTO, at time.Time) (DiscussionAgentResponse, []LinkedMissionDTO, error) {
	continued, err := s.continueDiscussionRunAndSave(ctx, conversationID, run, request, response, linkedMissions, at)
	if err != nil {
		return response, linkedMissions, err
	}
	return continued, mergeLinkedMissions(linkedMissions, continued.LinkedMissions), nil
}

func (s *Service) continueDiscussionRunAndCheckOrchestration(ctx context.Context, conversationID string, run *RunRecord, request DiscussionAgentRequest, response DiscussionAgentResponse, linkedMissions []LinkedMissionDTO, at time.Time) (DiscussionAgentResponse, []LinkedMissionDTO, error, error) {
	continued, missions, err := s.continueDiscussionRunAndMergeLinks(ctx, conversationID, run, request, response, linkedMissions, at)
	if err != nil {
		return response, linkedMissions, nil, err
	}
	return continued, missions, orchestrationStopError(missions, continued.ToolCalls, continued.AssistantMessage), nil
}

func (s *Service) saveRunMissionLinks(ctx context.Context, conversationID string, missions []LinkedMissionDTO, at time.Time) error {
	for _, mission := range missions {
		if strings.TrimSpace(mission.MissionID) == "" {
			continue
		}
		if replacedID := strings.TrimSpace(mission.ReplacesMissionID); replacedID != "" && replacedID != strings.TrimSpace(mission.MissionID) {
			if err := s.repo.DeleteMissionLink(ctx, conversationID, replacedID); err != nil {
				return err
			}
		}
		if err := s.repo.SaveMissionLink(ctx, LinkedMissionRecord{ConversationID: conversationID, MissionID: mission.MissionID, Title: mission.Title, Status: mission.Status, CreatedAt: firstTime(mission.CreatedAt, at)}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) userSourceMessageForRun(ctx context.Context, conversationID string, run RunRecord) (MessageRecord, bool, error) {
	if s == nil || s.repo == nil {
		return MessageRecord{}, false, ErrServiceDependency
	}
	view, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return MessageRecord{}, false, err
	}
	for i := len(view.Messages) - 1; i >= 0; i-- {
		message := view.Messages[i]
		if message.Role != "user" {
			continue
		}
		if !run.CreatedAt.IsZero() && message.CreatedAt.After(run.CreatedAt) {
			continue
		}
		return message, true, nil
	}
	return MessageRecord{}, false, nil
}

func (s *Service) isRunCurrent(ctx context.Context, run RunRecord) bool {
	if s == nil || s.repo == nil || strings.TrimSpace(run.RunID) == "" {
		return false
	}
	if s.isRunInvalidated(run.RunID) {
		return false
	}
	current, err := s.repo.GetRun(ctx, run.RunID)
	status := strings.TrimSpace(strings.ToLower(current.Status))
	return err == nil && current.ConversationID == run.ConversationID && current.RunID == run.RunID && status != "cancelled" && status != "canceled"
}
