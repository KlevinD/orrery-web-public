package discussionsession

import (
	"context"
	"strings"
	"time"
)

func (s *Service) commandTargetsDirectConversation(ctx context.Context, cmd SendMessageCommand) bool {
	if directTargetFromCommand(cmd).ID != "" {
		return true
	}
	conversationID := strings.TrimSpace(cmd.ConversationID)
	if conversationID == "" || s == nil || s.repo == nil {
		return false
	}
	return s.isDirectConversation(ctx, conversationID)
}

func (s *Service) isDirectConversation(ctx context.Context, conversationID string) bool {
	if s == nil || s.repo == nil {
		return false
	}
	view, err := s.repo.GetConversationRecord(ctx, strings.TrimSpace(conversationID))
	if err != nil {
		return false
	}
	return strings.TrimSpace(view.ConversationKind) == "direct"
}

func (s *Service) findExistingDirectConversation(ctx context.Context, viewer ViewerRef, target directConversationTargetRef) (ConversationRecordView, bool, error) {
	if s == nil || s.repo == nil {
		return ConversationRecordView{}, false, ErrServiceDependency
	}
	target.Kind = normalizeDirectTargetKind(target.Kind)
	target.ID = strings.TrimSpace(target.ID)
	viewerID := strings.TrimSpace(viewer.ViewerID)
	viewerTenantID := strings.TrimSpace(viewer.TenantID)
	if target.ID == "" || viewerID == "" {
		return ConversationRecordView{}, false, ErrInvalidRequest
	}
	if target.Kind == "" {
		resolved, err := s.resolveDirectConversationTargetRef(ctx, target.ID)
		if err != nil {
			return ConversationRecordView{}, false, err
		}
		target = resolved
	}
	if err := s.rejectCurrentViewerDirectTarget(ctx, viewer, target); err != nil {
		return ConversationRecordView{}, false, err
	}
	records, err := s.repo.ListConversations(ctx)
	if err != nil {
		return ConversationRecordView{}, false, err
	}
	for _, record := range records {
		if strings.TrimSpace(record.ConversationKind) != "direct" {
			continue
		}
		if strings.TrimSpace(record.CreatedBy) != viewerID {
			continue
		}
		view, err := s.repo.GetConversation(ctx, record.ConversationID)
		if err != nil {
			return ConversationRecordView{}, false, err
		}
		if tenantID := strings.TrimSpace(view.TenantID); tenantID != "" && tenantID != viewerTenantID {
			continue
		}
		member, ok := directConversationTargetMember(view)
		if !ok {
			continue
		}
		existingTarget := directTargetRefFromMember(member)
		if existingTarget.Kind != target.Kind || existingTarget.ID != target.ID {
			continue
		}
		if conversationMemberName(view.Members, conversationViewerMemberKind(viewer.ViewerKind), viewerID) == "" {
			continue
		}
		return view, true, nil
	}
	return ConversationRecordView{}, false, nil
}

func legacyDirectConversationTenant(view ConversationRecordView, viewer ViewerRef) (string, bool) {
	tenantID := strings.TrimSpace(viewer.TenantID)
	viewerID := strings.TrimSpace(viewer.ViewerID)
	if tenantID == "" || viewerID == "" || strings.TrimSpace(view.TenantID) != "" || strings.TrimSpace(view.ConversationKind) != "direct" {
		return "", false
	}
	if strings.TrimSpace(view.CreatedBy) != viewerID {
		return "", false
	}
	if conversationMemberName(view.Members, conversationViewerMemberKind(viewer.ViewerKind), viewerID) == "" {
		return "", false
	}
	return tenantID, true
}

func (s *Service) completeDirectConversation(ctx context.Context, conversationID string, source MessageRecord, onEvent func(StreamEventDTO) bool) (ConversationDTO, error) {
	if s == nil || s.repo == nil {
		return ConversationDTO{}, ErrServiceDependency
	}
	if s.mentionResponder == nil {
		return ConversationDTO{}, ErrRuntimeUnavailable
	}
	view, err := s.repo.GetConversation(ctx, strings.TrimSpace(conversationID))
	if err != nil {
		return ConversationDTO{}, err
	}
	target, ok := directConversationAgentTarget(view)
	if !ok {
		return ConversationDTO{}, ErrInvalidRequest
	}
	latest := source.CreatedAt
	runCtx := ctx
	var cancel context.CancelFunc
	opts := discussionMentionDispatchOptions{}
	if onEvent != nil {
		runCtx, cancel = context.WithCancel(ctx)
		defer cancel()
		opts.OnEvent = onEvent
		opts.ActiveRunCancel = cancel
	}
	messages, err := s.respondToDirectConversationTarget(runCtx, view, source, target, latest, opts, func(message MessageDTO) bool {
		if message.CreatedAt.After(latest) {
			latest = message.CreatedAt
		}
		if onEvent == nil {
			return true
		}
		return onEvent(StreamEventDTO{Kind: StreamEventMessageCompleted, Message: &message})
	})
	if err != nil {
		return ConversationDTO{}, err
	}
	for _, message := range messages {
		if message.CreatedAt.After(latest) {
			latest = message.CreatedAt
		}
	}
	updated, err := s.repo.GetConversation(ctx, strings.TrimSpace(conversationID))
	if err != nil {
		return ConversationDTO{}, err
	}
	applyDirectTargetMetadata(&updated.ConversationRecord, target)
	updated.Status = latestDirectMentionStatus(updated.Runs)
	updated.UpdatedAt = firstTimeAfter(latest, source.CreatedAt)
	if isCompletedConversationStatus(updated.Status) {
		updated.ViewedAt = updated.UpdatedAt
	}
	if err := s.repo.SaveConversation(ctx, updated.ConversationRecord); err != nil {
		return ConversationDTO{}, err
	}
	updated, err = s.repo.GetConversation(ctx, strings.TrimSpace(conversationID))
	if err != nil {
		return ConversationDTO{}, err
	}
	if err := s.appendRealtimeConversationListInvalidated(ctx, updated, "direct-completed-"+conversationID); err != nil {
		return ConversationDTO{}, err
	}
	dto := mapConversationDTO(updated)
	if onEvent != nil {
		_ = onEvent(StreamEventDTO{Kind: StreamEventSnapshot, Conversation: &dto})
	}
	return dto, nil
}

func (s *Service) completePassiveDirectConversation(ctx context.Context, conversationID string, source MessageRecord, onEvent func(StreamEventDTO) bool) (ConversationDTO, error) {
	if s == nil || s.repo == nil {
		return ConversationDTO{}, ErrServiceDependency
	}
	updated, err := s.repo.GetConversation(ctx, strings.TrimSpace(conversationID))
	if err != nil {
		return ConversationDTO{}, err
	}
	if !strings.EqualFold(strings.TrimSpace(updated.ConversationKind), "direct") {
		return ConversationDTO{}, ErrInvalidRequest
	}
	target, ok := directConversationTargetMember(updated)
	if !ok {
		return ConversationDTO{}, ErrInvalidRequest
	}
	applyDirectTargetMetadata(&updated.ConversationRecord, target)
	updated.Status = "completed"
	updated.UpdatedAt = firstTime(source.CreatedAt, s.now())
	updated.ViewedAt = updated.UpdatedAt
	if err := s.repo.SaveConversation(ctx, updated.ConversationRecord); err != nil {
		return ConversationDTO{}, err
	}
	updated, err = s.repo.GetConversation(ctx, strings.TrimSpace(conversationID))
	if err != nil {
		return ConversationDTO{}, err
	}
	if err := s.appendRealtimeConversationListInvalidated(ctx, updated, "direct-passive-"+conversationID); err != nil {
		return ConversationDTO{}, err
	}
	dto := mapConversationDTO(updated)
	if onEvent != nil {
		_ = onEvent(StreamEventDTO{Kind: StreamEventSnapshot, Conversation: &dto})
	}
	return dto, nil
}

func (s *Service) respondToDirectConversationTarget(ctx context.Context, view ConversationRecordView, source MessageRecord, target ConversationMemberRecord, after time.Time, opts discussionMentionDispatchOptions, onMessage func(MessageDTO) bool) ([]MessageDTO, error) {
	if s == nil || s.repo == nil || s.mentionResponder == nil {
		return nil, nil
	}
	mode := firstNonBlank(opts.Mode, "direct")
	run, err := s.createDiscussionTargetRunForTurn(ctx, opts.TurnID, mode, view.ConversationID, target.MemberID, source, after, opts.InitialLatestAt)
	if err != nil {
		return nil, err
	}
	if opts.ActiveRunCancel != nil {
		s.registerActiveRun(run.RunID, opts.ActiveRunCancel)
		defer s.unregisterActiveRun(run.RunID)
	}
	if opts.OnEvent != nil {
		runDTO := mapRuns([]RunRecord{run})[0]
		if !opts.OnEvent(StreamEventDTO{Kind: StreamEventRunStarted, Run: &runDTO}) {
			return nil, nil
		}
	}
	request := discussionTargetRequestFromRun("direct", view, source, target, run)
	request.ForceNewSession = opts.ForceNewSession
	request.OutputLanguage = normalizeDiscussionOutputLanguage(opts.OutputLanguage)
	request.BoundTools = s.discussionAgentBoundToolsForConversationAgent(ctx, view.ConversationID, target.MemberID)
	request.BoundToolsPolicy = true
	if err := s.attachDiscussionMentionWorkspace(ctx, &request); err != nil {
		return nil, err
	}
	response, err := s.respondToDiscussionMention(ctx, &run, request, opts.OnEvent)
	completedAt := firstTimeAfter(s.now(), run.StartedAt)
	if err != nil {
		emitEvent := opts.OnEvent
		cause := err
		if ctx.Err() != nil {
			emitEvent = nil
			cause = nil
		}
		_, saveErr := s.failDiscussionMentionRun(context.WithoutCancel(ctx), view.ConversationID, run, source, target, response, cause, completedAt, emitEvent)
		return nil, saveErr
	}
	linkedMissions := mergeLinkedMissions(response.LinkedMissions)
	if !response.ToolLoopCompleted {
		linkedMissions, err = s.applyBoundDiscussionMentionToolCalls(ctx, view.ConversationID, target.MemberID, source, target, response.ToolCalls, request.BoundTools, completedAt, request)
		if err != nil {
			_, saveErr := s.failDiscussionMentionRun(ctx, view.ConversationID, run, source, target, response, err, completedAt, opts.OnEvent)
			return nil, saveErr
		}
	}
	var stop bool
	response, linkedMissions, stop, err = s.continueDiscussionMentionTools(ctx, view.ConversationID, &run, request, source, target, response, linkedMissions, completedAt, opts.OnEvent, func(RunRecord) {})
	if err != nil || stop {
		return nil, err
	}
	if run.Status == "failed" {
		return nil, nil
	}
	for _, mission := range linkedMissions {
		if strings.TrimSpace(mission.MissionID) == "" {
			continue
		}
		if saveErr := s.repo.SaveMissionLink(ctx, LinkedMissionRecord{ConversationID: view.ConversationID, MissionID: mission.MissionID, Title: mission.Title, Status: mission.Status, CreatedAt: firstTime(mission.CreatedAt, completedAt)}); saveErr != nil {
			return nil, saveErr
		}
	}
	run.Status = firstNonBlank(response.Status, "completed")
	applyResponseRunProgress(&run, response)
	run.RuntimeKind = response.RuntimeKind
	run.SessionID = response.SessionID
	run.ProviderMetadata = mergeOutputPrivacyMetadata(run.ProviderMetadata, response.ProviderMetadata)
	run.CompletedAt = completedAt
	applyResponseTelemetry(&run, response)
	if err := s.saveRunAndRuntimeApprovalEvent(ctx, view.ConversationID, run, response, completedAt); err != nil {
		return nil, err
	}
	if err := s.appendRealtimeToolEvents(ctx, view.ConversationID, run.RunID, run.ToolCalls); err != nil {
		return nil, err
	}
	reply := strings.TrimSpace(response.AssistantMessage)
	if reply == "" {
		if err := s.appendRealtimeRunCompleted(ctx, run); err != nil {
			return nil, err
		}
		if opts.OnEvent != nil {
			runDTO := mapRuns([]RunRecord{run})[0]
			if !opts.OnEvent(StreamEventDTO{Kind: StreamEventRunCompleted, Run: &runDTO}) {
				return nil, nil
			}
		}
		return nil, nil
	}
	messageID, err := s.newID()
	if err != nil {
		return nil, err
	}
	replyViewer := ViewerRef{ViewerID: source.AuthorID, ViewerKind: ViewerKindUser, DisplayName: source.AuthorName}
	record := correlateMessageToRun(assistantMessageRecordWithDisplayTimeline(messageID, view.ConversationID, target.MemberID, target.DisplayName, reply, response, firstTimeAfter(completedAt, source.CreatedAt), replyViewer), run)
	dto, err := s.persistAssistantMessageWithWorkspaceFiles(ctx, record, request.Workspace)
	if err != nil {
		return nil, err
	}
	if onMessage != nil && !onMessage(dto) {
		return []MessageDTO{dto}, nil
	}
	if opts.OnEvent != nil {
		runDTO := mapRuns([]RunRecord{run})[0]
		if !opts.OnEvent(StreamEventDTO{Kind: StreamEventRunCompleted, Run: &runDTO}) {
			return []MessageDTO{dto}, nil
		}
	}
	if err := s.appendRealtimeRunCompleted(ctx, run); err != nil {
		return nil, err
	}
	return []MessageDTO{dto}, nil
}

func directConversationTargetMember(view ConversationRecordView) (ConversationMemberRecord, bool) {
	target := directTargetFromRecord(view.ConversationRecord)
	if target.ID == "" {
		return inferDirectConversationTargetMember(view)
	}
	memberKind := directTargetMemberKind(target.Kind)
	for _, member := range view.Members {
		if strings.TrimSpace(member.MemberKind) == memberKind && strings.TrimSpace(member.MemberID) == target.ID {
			return member, true
		}
	}
	return ConversationMemberRecord{}, false
}

func directConversationAgentTarget(view ConversationRecordView) (ConversationMemberRecord, bool) {
	member, ok := directConversationTargetMember(view)
	if !ok || strings.TrimSpace(member.MemberKind) != "agent" {
		return ConversationMemberRecord{}, false
	}
	return member, true
}

func inferDirectConversationTargetMember(view ConversationRecordView) (ConversationMemberRecord, bool) {
	if strings.TrimSpace(view.ConversationKind) != "direct" {
		return ConversationMemberRecord{}, false
	}
	creatorID := strings.TrimSpace(view.CreatedBy)
	var candidates []ConversationMemberRecord
	for _, member := range view.Members {
		memberKind := strings.TrimSpace(member.MemberKind)
		memberID := strings.TrimSpace(member.MemberID)
		if memberID == "" {
			continue
		}
		if creatorID != "" && memberKind == "user" && memberID == creatorID {
			continue
		}
		if memberKind == "agent" || memberKind == "user" {
			candidates = append(candidates, member)
		}
	}
	if len(candidates) == 1 {
		return candidates[0], true
	}
	var agent ConversationMemberRecord
	agentCount := 0
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.MemberKind) == "agent" {
			agent = candidate
			agentCount++
		}
	}
	if agentCount == 1 {
		return agent, true
	}
	return ConversationMemberRecord{}, false
}

func directTargetRefFromMember(member ConversationMemberRecord) directConversationTargetRef {
	id := strings.TrimSpace(member.MemberID)
	switch strings.TrimSpace(member.MemberKind) {
	case "agent":
		return directConversationTargetRef{Kind: directTargetKindAgent, ID: id}
	case "user":
		return directConversationTargetRef{Kind: directTargetKindUser, ID: id}
	default:
		return directConversationTargetRef{}
	}
}

func applyDirectTargetMetadata(record *ConversationRecord, target ConversationMemberRecord) {
	if record == nil {
		return
	}
	if strings.TrimSpace(record.ConversationKind) != "direct" {
		return
	}
	ref := directTargetRefFromMember(target)
	if ref.ID == "" {
		return
	}
	record.ConversationKind = "direct"
	record.DirectTargetKind = ref.Kind
	record.DirectTargetID = ref.ID
	record.DirectAgentID = directAgentIDForTarget(ref.Kind, ref.ID)
}

func (s *Service) forwardDirectConversationStream(ctx context.Context, out chan<- StreamEventDTO, conversationID string, source MessageRecord, targets discussionTurnTargetPlan, releaseRunLifecycle func()) {
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
	var err error
	if targets.Mode == discussionTurnTargetDirectAgent {
		_, err = s.completeDirectConversation(ctx, conversationID, source, send)
	} else {
		_, err = s.completePassiveDirectConversation(ctx, conversationID, source, send)
	}
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		_ = send(StreamEventDTO{Kind: StreamEventError, Error: err.Error()})
		return
	}
}
