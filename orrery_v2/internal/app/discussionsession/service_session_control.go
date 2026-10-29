package discussionsession

import (
	"context"
	"strings"
	"time"
)

func (s *Service) AbortConversation(ctx context.Context, viewer ViewerRef, conversationID string) (ConversationDTO, error) {
	if s == nil || s.repo == nil {
		return ConversationDTO{}, ErrServiceDependency
	}
	conversationID = strings.TrimSpace(conversationID)
	if strings.TrimSpace(viewer.ViewerID) == "" || conversationID == "" {
		return ConversationDTO{}, ErrInvalidRequest
	}
	view, err := s.requireConversationAccess(ctx, viewer, conversationID)
	if err != nil {
		return ConversationDTO{}, err
	}
	runs := runningDiscussionRuns(view.Runs)
	if len(runs) == 0 {
		return mapConversationDTO(view), nil
	}
	abortedAt := s.now()
	runIDs := make([]string, 0, len(runs))
	cancelledRuns := make([]RunRecord, 0, len(runs))
	var latestRun RunRecord
	for i, run := range runs {
		runIDs = append(runIDs, run.RunID)
		if i == len(runs)-1 {
			latestRun = run
		}
		run.Status = "cancelled"
		cancelRunProgress(&run)
		run.CompletedAt = firstTimeAfter(abortedAt, run.StartedAt)
		if run.CompletedAt.After(abortedAt) {
			abortedAt = run.CompletedAt
		}
		cancelledRuns = append(cancelledRuns, run)
	}
	closedApprovals := closedPendingRuntimeApprovalsForRuns(view, runIDs, viewer, "cancelled", abortedAt)
	activeTurnID := strings.TrimSpace(view.ActiveTurnID)
	view.Status = "cancelled"
	view.UpdatedAt = abortedAt
	view.ActiveTurnID = ""
	eventID, err := s.newID()
	if err != nil {
		return ConversationDTO{}, err
	}
	abortedBy := strings.TrimSpace(viewer.DisplayName)
	if abortedBy == "" {
		abortedBy = strings.TrimSpace(viewer.ViewerID)
	}
	event := EventRecord{
		EventID:        eventID,
		ConversationID: conversationID,
		Kind:           "discussion_session_aborted",
		Status:         "cancelled",
		Summary:        "用户中止了讨论流程",
		Metadata: map[string]string{
			"run_id":     latestRun.RunID,
			"run_ids":    strings.Join(runIDs, ","),
			"agent_id":   latestRun.AgentID,
			"aborted_by": viewer.ViewerID,
			"actor_name": abortedBy,
		},
		CreatedAt: abortedAt,
	}
	events := append(closedApprovals, event)
	partialMessages := make([]MessageRecord, 0, len(runs))
	for i, run := range runs {
		response, ok := s.cachedDiscussionStreamResponse(run.RunID)
		if !ok {
			continue
		}
		terminateResponseProgress(&response, "cancelled")
		record, ok, err := partialAssistantMessageRecord(view, run, response, cancelledRuns[i].CompletedAt, nil, nil)
		if err != nil {
			return ConversationDTO{}, err
		}
		if ok {
			partialMessages = append(partialMessages, record)
		}
	}
	mutation := TurnAggregateMutation{
		Conversation:     view.ConversationRecord,
		ExpectedRevision: view.Revision,
		Runs:             cancelledRuns,
		Messages:         partialMessages,
		Events:           events,
	}
	if activeTurnID != "" {
		mutation.CompleteTurnID = activeTurnID
	}
	committed, err := s.repo.ApplyTurnMutation(ctx, mutation)
	if err != nil {
		return ConversationDTO{}, err
	}
	view.ConversationRecord = committed
	for _, message := range partialMessages {
		if err := s.appendRealtimeMessageCompleted(ctx, message); err != nil {
			return ConversationDTO{}, err
		}
	}
	for _, run := range cancelledRuns {
		s.invalidateRun(run.RunID)
		if err := s.appendRealtimeRunFailed(ctx, run); err != nil {
			return ConversationDTO{}, err
		}
	}
	if err := s.appendRealtimeSnapshotInvalidated(ctx, conversationID, "abort", event.EventID); err != nil {
		return ConversationDTO{}, err
	}
	updated, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return ConversationDTO{}, err
	}
	if err := s.appendRealtimeConversationListInvalidated(ctx, updated, "abort-"+event.EventID); err != nil {
		return ConversationDTO{}, err
	}
	dto := mapConversationDTO(updated)
	if s.streams != nil {
		s.streams.publish(conversationID, StreamEventDTO{Kind: StreamEventSnapshot, Conversation: &dto})
	}
	return dto, nil
}

func closedPendingRuntimeApprovalsForRuns(view ConversationRecordView, runIDs []string, viewer ViewerRef, status string, at time.Time) []EventRecord {
	if len(runIDs) == 0 {
		return nil
	}
	runIDSet := make(map[string]struct{}, len(runIDs))
	for _, runID := range runIDs {
		runID = strings.TrimSpace(runID)
		if runID != "" {
			runIDSet[runID] = struct{}{}
		}
	}
	if len(runIDSet) == 0 {
		return nil
	}
	status = strings.TrimSpace(strings.ToLower(status))
	if status == "" {
		status = "cancelled"
	}
	closedEvents := make([]EventRecord, 0)
	for _, event := range view.Events {
		if event.Kind != discussionRuntimeApprovalEventKind || !runtimeApprovalIsPending(event.Status) {
			continue
		}
		if _, ok := runIDSet[strings.TrimSpace(event.Metadata["approval_run_id"])]; !ok {
			continue
		}
		metadata := cloneStringMap(event.Metadata)
		if metadata == nil {
			metadata = map[string]string{}
		}
		metadata["approval_decision"] = status
		metadata["approval_closed_reason"] = "conversation_cancelled"
		if viewer.ViewerID != "" {
			metadata["approval_decided_by"] = viewer.ViewerID
		}
		closed := event
		closed.Status = status
		closed.Metadata = metadata
		closed.CreatedAt = firstTime(firstTimeAfter(at, event.CreatedAt), event.CreatedAt)
		closedEvents = append(closedEvents, closed)
	}
	return closedEvents
}

func (s *Service) SubscribeConversationStream(ctx context.Context, viewer ViewerRef, conversationID string) (<-chan StreamEventDTO, error) {
	if s == nil || s.repo == nil {
		return nil, ErrServiceDependency
	}
	if s.streams == nil {
		return nil, ErrServiceDependency
	}
	conversationID = strings.TrimSpace(conversationID)
	if strings.TrimSpace(viewer.ViewerID) == "" || conversationID == "" {
		return nil, ErrInvalidRequest
	}
	view, err := s.requireConversationAccess(ctx, viewer, conversationID)
	if err != nil {
		return nil, err
	}
	if err := s.markCompletedConversationViewed(ctx, &view); err != nil {
		return nil, err
	}
	out := make(chan StreamEventDTO, 64)
	s.streams.subscribe(conversationID, out)
	dto := mapConversationDTO(view)
	out <- StreamEventDTO{Kind: StreamEventSnapshot, Conversation: &dto}
	go func() {
		<-ctx.Done()
		s.streams.unsubscribe(conversationID, out)
		close(out)
	}()
	return out, nil
}

func (s *Service) GetLatestRunDebug(ctx context.Context, viewer ViewerRef, conversationID string) (RunDebugDTO, error) {
	if s == nil || s.repo == nil {
		return RunDebugDTO{}, ErrServiceDependency
	}
	if strings.TrimSpace(viewer.ViewerID) == "" || strings.TrimSpace(conversationID) == "" {
		return RunDebugDTO{}, ErrInvalidRequest
	}
	view, err := s.requireConversationAccess(ctx, viewer, conversationID)
	if err != nil {
		return RunDebugDTO{}, err
	}
	for i := len(view.Runs) - 1; i >= 0; i-- {
		if view.Runs[i].Debug != nil {
			return publicRunDebug(*view.Runs[i].Debug), nil
		}
	}
	return RunDebugDTO{}, ErrNotFound
}

func (s *Service) GetRunDebug(ctx context.Context, viewer ViewerRef, conversationID string, runID string) (RunDebugDTO, error) {
	if s == nil || s.repo == nil {
		return RunDebugDTO{}, ErrServiceDependency
	}
	conversationID = strings.TrimSpace(conversationID)
	runID = strings.TrimSpace(runID)
	if strings.TrimSpace(viewer.ViewerID) == "" || conversationID == "" || runID == "" {
		return RunDebugDTO{}, ErrInvalidRequest
	}
	view, err := s.requireConversationAccess(ctx, viewer, conversationID)
	if err != nil {
		return RunDebugDTO{}, err
	}
	for _, run := range view.Runs {
		if run.RunID == runID && run.Debug != nil {
			return publicRunDebug(*run.Debug), nil
		}
	}
	return RunDebugDTO{}, ErrNotFound
}
