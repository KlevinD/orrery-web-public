package discussionsession

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

// ApplyTurnMutation is the aggregate write boundary for a discussion turn. All
// canonical conversation state is committed in one core database transaction;
// realtime delivery remains a notification of this authoritative snapshot.
func (r *Repository) ApplyTurnMutation(ctx context.Context, mutation TurnAggregateMutation) (ConversationRecord, error) {
	if r == nil || r.router == nil {
		return ConversationRecord{}, ErrServiceDependency
	}
	conversation := mutation.Conversation
	conversation.ConversationID = strings.TrimSpace(conversation.ConversationID)
	if conversation.ConversationID == "" {
		return ConversationRecord{}, ErrInvalidRequest
	}
	store, conversationPhysicalTable, err := r.router.StoreForTable(conversationTable)
	if err != nil {
		return ConversationRecord{}, err
	}
	physicalTables := map[string]string{conversationTable: conversationPhysicalTable}
	for _, logicalTable := range []string{messageTable, memberTable, turnTable, runTable, "core.discussion_session_events", missionLinkTable, attachmentTable} {
		candidateStore, table, tableErr := r.router.StoreForTable(logicalTable)
		if tableErr != nil {
			return ConversationRecord{}, tableErr
		}
		if candidateStore != store {
			return ConversationRecord{}, fmt.Errorf("discussion aggregate table %q is routed to a different database", logicalTable)
		}
		physicalTables[logicalTable] = table
	}
	tx, err := store.BeginTx(ctx)
	if err != nil {
		return ConversationRecord{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := db.LockRecordKeyTx(ctx, tx, conversationPhysicalTable, conversation.ConversationID); err != nil {
		return ConversationRecord{}, err
	}
	currentRevision := int64(0)
	currentActiveTurnID := ""
	if currentRecord, ok, getErr := db.GetRecordTx(ctx, tx, conversationPhysicalTable, conversation.ConversationID); getErr != nil {
		return ConversationRecord{}, getErr
	} else if ok {
		if mutation.RequireAbsent {
			return ConversationRecord{}, ErrRevisionConflict
		}
		var current ConversationRecord
		if err := decodePayload(currentRecord, &current); err != nil {
			return ConversationRecord{}, err
		}
		currentRevision = current.Revision
		currentActiveTurnID = strings.TrimSpace(current.ActiveTurnID)
	}
	if mutation.ExpectedRevision > 0 && mutation.ExpectedRevision != currentRevision {
		return ConversationRecord{}, ErrRevisionConflict
	}
	if mutation.RequireNoActiveTurn && currentActiveTurnID != "" {
		return ConversationRecord{}, ErrCommandInProgress
	}
	if mutation.Turn != nil && strings.EqualFold(strings.TrimSpace(mutation.Turn.Status), "running") {
		turnID := strings.TrimSpace(mutation.Turn.TurnID)
		if turnID == "" {
			return ConversationRecord{}, ErrInvalidRequest
		}
		if currentActiveTurnID != "" && currentActiveTurnID != turnID && !mutation.SupersedeActiveTurn {
			return ConversationRecord{}, ErrCommandInProgress
		}
		conversation.ActiveTurnID = turnID
	}
	if completeTurnID := strings.TrimSpace(mutation.CompleteTurnID); completeTurnID != "" {
		if currentActiveTurnID != completeTurnID {
			return ConversationRecord{}, ErrNotFound
		}
		conversation.ActiveTurnID = ""
	}
	conversation.Revision = currentRevision + 1
	if conversation.Revision <= 0 {
		conversation.Revision = 1
	}
	if err := putConversationRecordTx(ctx, tx, physicalTables[conversationTable], conversation); err != nil {
		return ConversationRecord{}, err
	}
	if mutation.Turn != nil {
		turn := *mutation.Turn
		if turn.ConversationID == "" {
			turn.ConversationID = conversation.ConversationID
		}
		if strings.TrimSpace(turn.ConversationID) != conversation.ConversationID {
			return ConversationRecord{}, ErrForbidden
		}
		if err := putPayloadRecordTx(ctx, tx, physicalTables[turnTable], turn.TurnID, turn, db.Record{"turn_id": turn.TurnID, "conversation_id": turn.ConversationID, "status": turn.Status, "updated_at": formatTime(turn.UpdatedAt)}); err != nil {
			return ConversationRecord{}, err
		}
	}
	for _, message := range mutation.Messages {
		if strings.TrimSpace(message.ConversationID) != conversation.ConversationID {
			return ConversationRecord{}, ErrForbidden
		}
		if err := putPayloadRecordTx(ctx, tx, physicalTables[messageTable], message.MessageID, message, db.Record{"message_id": message.MessageID, "conversation_id": message.ConversationID, "role": message.Role, "created_at": formatTime(message.CreatedAt)}); err != nil {
			return ConversationRecord{}, err
		}
	}
	for _, member := range mutation.Members {
		member = normalizeConversationMemberManagedSources(member)
		if strings.TrimSpace(member.ConversationID) != conversation.ConversationID {
			return ConversationRecord{}, ErrForbidden
		}
		key := member.ConversationID + "|" + member.MemberKind + "|" + member.MemberID
		if err := putPayloadRecordTx(ctx, tx, physicalTables[memberTable], key, member, db.Record{"conversation_id": member.ConversationID, "member_kind": member.MemberKind, "member_id": member.MemberID, "created_at": formatTime(member.CreatedAt)}); err != nil {
			return ConversationRecord{}, err
		}
	}
	for _, run := range mutation.Runs {
		if strings.TrimSpace(run.ConversationID) != conversation.ConversationID {
			return ConversationRecord{}, ErrForbidden
		}
		run = normalizeRunDebug(run)
		if err := putPayloadRecordTx(ctx, tx, physicalTables[runTable], run.RunID, run, db.Record{"run_id": run.RunID, "conversation_id": run.ConversationID, "agent_id": run.AgentID, "status": run.Status, "runtime_kind": run.RuntimeKind, "created_at": formatTime(run.CreatedAt)}); err != nil {
			return ConversationRecord{}, err
		}
	}
	for _, event := range mutation.Events {
		if strings.TrimSpace(event.ConversationID) != conversation.ConversationID {
			return ConversationRecord{}, ErrForbidden
		}
		if err := putPayloadRecordTx(ctx, tx, physicalTables["core.discussion_session_events"], event.EventID, event, db.Record{"event_id": event.EventID, "conversation_id": event.ConversationID, "kind": event.Kind, "status": event.Status, "created_at": formatTime(event.CreatedAt)}); err != nil {
			return ConversationRecord{}, err
		}
	}
	for _, link := range mutation.MissionLinks {
		if strings.TrimSpace(link.ConversationID) != conversation.ConversationID {
			return ConversationRecord{}, ErrForbidden
		}
		key := link.ConversationID + "|" + link.MissionID
		if err := putPayloadRecordTx(ctx, tx, physicalTables[missionLinkTable], key, link, db.Record{"conversation_id": link.ConversationID, "mission_id": link.MissionID, "status": link.Status, "created_at": formatTime(link.CreatedAt)}); err != nil {
			return ConversationRecord{}, err
		}
	}
	for _, attachment := range mutation.Attachments {
		if strings.TrimSpace(attachment.ConversationID) != conversation.ConversationID {
			return ConversationRecord{}, ErrForbidden
		}
		if err := putPayloadRecordTx(ctx, tx, physicalTables[attachmentTable], attachment.AttachmentID, attachment, db.Record{"attachment_id": attachment.AttachmentID, "conversation_id": attachment.ConversationID, "message_id": attachment.MessageID, "idempotency_key": attachment.IdempotencyKey, "created_at": formatTime(attachment.CreatedAt)}); err != nil {
			return ConversationRecord{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return ConversationRecord{}, err
	}
	committed = true
	return conversation, nil
}

// CreateConversationWithMembers creates the access-bearing portion of a
// conversation aggregate atomically. A conversation is never observable
// without the members that grant its initial access.
func (r *Repository) CreateConversationWithMembers(ctx context.Context, conversation ConversationRecord, members []ConversationMemberRecord) (ConversationRecord, error) {
	return r.ApplyTurnMutation(ctx, TurnAggregateMutation{
		Conversation:  conversation,
		Members:       append([]ConversationMemberRecord(nil), members...),
		RequireAbsent: true,
	})
}

func (r *Repository) SaveTurn(ctx context.Context, turn TurnRecord) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	turn.TurnID = strings.TrimSpace(turn.TurnID)
	turn.ConversationID = strings.TrimSpace(turn.ConversationID)
	if turn.TurnID == "" || turn.ConversationID == "" {
		return ErrInvalidRequest
	}
	payload, err := encodePayload(turn)
	if err != nil {
		return err
	}
	return r.router.PutRecord(ctx, turnTable, turn.TurnID, db.Record{
		"turn_id":           turn.TurnID,
		"conversation_id":   turn.ConversationID,
		"status":            turn.Status,
		"updated_at":        formatTime(turn.UpdatedAt),
		conversationPayload: payload,
	})
}

func (r *Repository) GetTurn(ctx context.Context, turnID string) (TurnRecord, error) {
	if r == nil || r.router == nil {
		return TurnRecord{}, ErrServiceDependency
	}
	record, ok, err := r.router.GetRecord(ctx, turnTable, strings.TrimSpace(turnID))
	if err != nil {
		return TurnRecord{}, err
	}
	if !ok {
		return TurnRecord{}, ErrNotFound
	}
	var turn TurnRecord
	if err := decodePayload(record, &turn); err != nil {
		return TurnRecord{}, err
	}
	return turn, nil
}

func (r *Repository) findTurnsByIdempotencyKey(ctx context.Context, idempotencyKey string) ([]TurnRecord, error) {
	if r == nil || r.router == nil {
		return nil, ErrServiceDependency
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return nil, ErrInvalidRequest
	}
	records, err := snapshot(ctx, r.router, turnTable)
	if err != nil {
		return nil, err
	}
	turns := make([]TurnRecord, 0)
	for _, record := range records {
		var turn TurnRecord
		if err := decodePayload(record, &turn); err != nil {
			return nil, err
		}
		if strings.TrimSpace(turn.IdempotencyKey) == idempotencyKey {
			turns = append(turns, turn)
		}
	}
	sort.SliceStable(turns, func(i, j int) bool {
		left := firstTime(turns[i].UpdatedAt, turns[i].StartedAt, turns[i].CreatedAt)
		right := firstTime(turns[j].UpdatedAt, turns[j].StartedAt, turns[j].CreatedAt)
		return left.After(right)
	})
	return turns, nil
}

// FailStaleTurn closes a durable turn left running after its executor stopped.
// The conversation revision fences a live finisher from this recovery path.
func (r *Repository) FailStaleTurn(ctx context.Context, conversationID, turnID string, staleBefore, failedAt time.Time) (bool, error) {
	conversationID = strings.TrimSpace(conversationID)
	turnID = strings.TrimSpace(turnID)
	if r == nil || r.router == nil || conversationID == "" || turnID == "" || staleBefore.IsZero() || failedAt.IsZero() {
		return false, ErrInvalidRequest
	}
	view, err := r.GetConversation(ctx, conversationID)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(view.ActiveTurnID) != turnID {
		return false, nil
	}
	turn, err := r.GetTurn(ctx, turnID)
	if err != nil {
		return false, err
	}
	if turn.ConversationID != conversationID {
		return false, ErrForbidden
	}
	status := strings.ToLower(strings.TrimSpace(turn.Status))
	if status == "completed" || status == "failed" || status == "cancelled" {
		return false, nil
	}
	lastActivity := turn.UpdatedAt
	if lastActivity.IsZero() {
		lastActivity = turn.StartedAt
	}
	if !lastActivity.IsZero() && lastActivity.After(staleBefore) {
		return false, nil
	}
	if turnHasPendingRuntimeApproval(view, turnID) {
		return false, nil
	}

	runs := make([]RunRecord, 0)
	for _, run := range view.Runs {
		if strings.TrimSpace(run.TurnID) != turnID {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(run.Status)) {
		case "completed", "failed", "cancelled", "blocked":
			continue
		}
		run.Status = "failed"
		run.CompletedAt = failedAt
		runs = append(runs, run)
	}
	turn.Status = "failed"
	turn.ErrorCode = "executor_interrupted"
	turn.ErrorMessage = "discussion executor stopped before the durable turn reached a terminal state"
	turn.CompletedAt = failedAt
	turn.UpdatedAt = failedAt
	view.Status = "failed"
	view.UpdatedAt = failedAt
	_, err = r.ApplyTurnMutation(ctx, TurnAggregateMutation{
		Conversation: view.ConversationRecord, ExpectedRevision: view.Revision,
		Turn: &turn, Runs: runs, CompleteTurnID: turnID,
		Events: []EventRecord{{
			EventID: "discussion-turn-recovered-" + turnID, ConversationID: conversationID,
			Kind: "turn_recovery", Status: "failed", Summary: "Interrupted discussion turn was closed for retry", CreatedAt: failedAt,
		}},
	})
	if err != nil {
		if errors.Is(err, ErrRevisionConflict) || errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func turnHasPendingRuntimeApproval(view ConversationRecordView, turnID string) bool {
	runIDs := make(map[string]struct{})
	for _, run := range view.Runs {
		if strings.TrimSpace(run.TurnID) == turnID {
			if runID := strings.TrimSpace(run.RunID); runID != "" {
				runIDs[runID] = struct{}{}
			}
		}
	}
	for _, event := range view.Events {
		if event.Kind != discussionRuntimeApprovalEventKind || !runtimeApprovalIsPending(event.Status) || !runtimeApprovalIsHILBacked(event) {
			continue
		}
		if _, ok := runIDs[strings.TrimSpace(event.Metadata["approval_run_id"])]; ok {
			return true
		}
	}
	return false
}

func (r *Repository) listTurns(ctx context.Context, conversationID string) ([]TurnRecord, error) {
	records, err := r.conversationRecords(ctx, turnTable, conversationID)
	if err != nil {
		return nil, err
	}
	out := make([]TurnRecord, 0)
	for _, record := range records {
		if got, _ := record["conversation_id"].(string); got != conversationID {
			continue
		}
		var item TurnRecord
		if err := decodePayload(record, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func putConversationRecordTx(ctx context.Context, tx db.Tx, table string, conversation ConversationRecord) error {
	payload, err := encodePayload(conversation)
	if err != nil {
		return err
	}
	return db.PutRecordTx(ctx, tx, table, conversation.ConversationID, db.Record{
		"conversation_id":   conversation.ConversationID,
		"tenant_id":         conversation.TenantID,
		"purpose_kind":      conversation.PurposeKind,
		"purpose_id":        conversation.PurposeID,
		"status":            conversation.Status,
		"created_by":        conversation.CreatedBy,
		"owner_id":          conversation.OwnerID,
		"updated_at":        formatTime(conversation.UpdatedAt),
		"revision":          conversation.Revision,
		conversationPayload: payload,
	})
}

func putPayloadRecordTx(ctx context.Context, tx db.Tx, table, key string, value any, index db.Record) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return ErrInvalidRequest
	}
	payload, err := encodePayload(value)
	if err != nil {
		return err
	}
	if index == nil {
		index = db.Record{}
	}
	index[conversationPayload] = payload
	return db.PutRecordTx(ctx, tx, table, key, index)
}
