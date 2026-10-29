package discussionsession

import (
	"context"
	"sort"
)

func (r *Repository) ListApprovalConversations(ctx context.Context) ([]ConversationRecordView, error) {
	views, err := r.ListConversations(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*ConversationRecordView, len(views))
	for i := range views {
		byID[views[i].ConversationID] = &views[i]
	}
	eventRecords, err := snapshot(ctx, r.router, "core.discussion_session_events")
	if err != nil {
		return nil, err
	}
	for _, record := range eventRecords {
		conversationID, _ := record["conversation_id"].(string)
		view := byID[conversationID]
		if view == nil {
			continue
		}
		var event EventRecord
		if err := decodePayload(record, &event); err != nil {
			return nil, err
		}
		if event.Kind == discussionRuntimeApprovalEventKind {
			view.Events = append(view.Events, event)
		}
	}
	approvalViews := make([]ConversationRecordView, 0, len(views))
	for _, view := range views {
		if len(view.Events) == 0 {
			continue
		}
		approvalViews = append(approvalViews, view)
	}
	approvalByID := make(map[string]*ConversationRecordView, len(approvalViews))
	for i := range approvalViews {
		approvalByID[approvalViews[i].ConversationID] = &approvalViews[i]
	}
	runRecords, err := snapshot(ctx, r.router, runTable)
	if err != nil {
		return nil, err
	}
	for _, record := range runRecords {
		conversationID, _ := record["conversation_id"].(string)
		view := approvalByID[conversationID]
		if view == nil {
			continue
		}
		var run RunRecord
		if err := decodePayload(record, &run); err != nil {
			return nil, err
		}
		view.Runs = append(view.Runs, normalizeRunDebug(run))
	}
	for i := range approvalViews {
		sort.SliceStable(approvalViews[i].Events, func(left, right int) bool {
			return approvalViews[i].Events[left].CreatedAt.Before(approvalViews[i].Events[right].CreatedAt)
		})
		sort.SliceStable(approvalViews[i].Runs, func(left, right int) bool {
			return approvalViews[i].Runs[left].CreatedAt.Before(approvalViews[i].Runs[right].CreatedAt)
		})
	}
	return approvalViews, nil
}
