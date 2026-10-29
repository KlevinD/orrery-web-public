package discussionsession

import (
	"context"
	"sort"
	"strings"

	"orrery_v2/internal/platform/db"
)

const (
	conversationTable     = "core.discussion_sessions"
	messageTable          = "core.discussion_session_messages"
	runTable              = "core.discussion_session_runs"
	turnTable             = "core.discussion_session_turns"
	commandTable          = "core.discussion_session_commands"
	memberTable           = "core.discussion_session_members"
	missionLinkTable      = "core.discussion_session_mission_links"
	attachmentTable       = "core.discussion_session_attachments"
	conversationPayload   = "payload"
	timeFormatRFC3339Nano = "2006-01-02T15:04:05.999999999Z07:00"
)

type Repository struct {
	router *db.Router
}

func NewRepository(router *db.Router) *Repository {
	return &Repository{router: router}
}

func (r *Repository) SaveConversation(ctx context.Context, conversation ConversationRecord) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	conversation.ConversationID = strings.TrimSpace(conversation.ConversationID)
	if conversation.ConversationID == "" {
		return ErrInvalidRequest
	}
	store, table, err := r.router.StoreForTable(conversationTable)
	if err != nil {
		return err
	}
	tx, err := store.BeginTx(ctx)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := db.LockRecordKeyTx(ctx, tx, table, conversation.ConversationID); err != nil {
		return err
	}
	if currentRecord, ok, getErr := db.GetRecordTx(ctx, tx, table, conversation.ConversationID); getErr != nil {
		return getErr
	} else if ok {
		var current ConversationRecord
		if err := decodePayload(currentRecord, &current); err != nil {
			return err
		}
		if conversation.Revision > 0 && current.Revision > 0 && conversation.Revision != current.Revision {
			return ErrRevisionConflict
		}
		if strings.TrimSpace(current.PurposeKind) != "" && (strings.TrimSpace(current.PurposeKind) != strings.TrimSpace(conversation.PurposeKind) || strings.TrimSpace(current.PurposeID) != strings.TrimSpace(conversation.PurposeID) || strings.TrimSpace(current.TenantID) != strings.TrimSpace(conversation.TenantID)) {
			return ErrRevisionConflict
		}
		conversation.Revision = current.Revision + 1
		if conversation.Revision <= 0 {
			conversation.Revision = 1
		}
	} else {
		conversation.Revision = 1
	}
	record, err := conversationDBRecord(conversation)
	if err != nil {
		return err
	}
	if err := db.PutRecordTx(ctx, tx, table, conversation.ConversationID, record); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (r *Repository) CompareAndSwapConversation(ctx context.Context, expected, replacement ConversationRecord) (bool, error) {
	if r == nil || r.router == nil {
		return false, ErrServiceDependency
	}
	expected.ConversationID = strings.TrimSpace(expected.ConversationID)
	replacement.ConversationID = strings.TrimSpace(replacement.ConversationID)
	if expected.ConversationID == "" || replacement.ConversationID != expected.ConversationID {
		return false, ErrInvalidRequest
	}
	expectedRecord, err := conversationDBRecord(expected)
	if err != nil {
		return false, err
	}
	replacementRecord, err := conversationDBRecord(replacement)
	if err != nil {
		return false, err
	}
	return r.router.CompareAndSwapRecord(ctx, conversationTable, expected.ConversationID, expectedRecord, replacementRecord)
}

func conversationDBRecord(conversation ConversationRecord) (db.Record, error) {
	payload, err := encodePayload(conversation)
	if err != nil {
		return nil, err
	}
	return db.Record{
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
	}, nil
}

func (r *Repository) SaveMessage(ctx context.Context, message MessageRecord) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	payload, err := encodePayload(message)
	if err != nil {
		return err
	}
	return r.router.PutRecord(ctx, messageTable, message.MessageID, db.Record{
		"message_id":        message.MessageID,
		"conversation_id":   message.ConversationID,
		"role":              message.Role,
		"created_at":        formatTime(message.CreatedAt),
		conversationPayload: payload,
	})
}

func (r *Repository) SaveMember(ctx context.Context, member ConversationMemberRecord) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	member.ConversationID = strings.TrimSpace(member.ConversationID)
	member.MemberKind = strings.TrimSpace(member.MemberKind)
	member.MemberID = strings.TrimSpace(member.MemberID)
	if member.ConversationID == "" || member.MemberKind == "" || member.MemberID == "" {
		return ErrInvalidRequest
	}
	member = normalizeConversationMemberManagedSources(member)
	store, table, err := r.router.StoreForTable(memberTable)
	if err != nil {
		return err
	}
	key := conversationMemberKey(member)
	return store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, table, key); err != nil {
			return err
		}
		if currentRecord, ok, err := db.GetRecordTx(ctx, tx, table, key); err != nil {
			return err
		} else if ok {
			var current ConversationMemberRecord
			if err := decodePayload(currentRecord, &current); err != nil {
				return err
			}
			current = normalizeConversationMemberManagedSources(current)
			if strings.TrimSpace(member.ExplicitGrantorID) == "" {
				member.ManagedSources = append(conversationMemberManagedSources(current), conversationMemberManagedSources(member)...)
				member.ManagedSource = ""
				member.ExplicitGrantorID = current.ExplicitGrantorID
				member.ExplicitGrantorKind = current.ExplicitGrantorKind
				member.ExplicitGrantedAt = current.ExplicitGrantedAt
			}
			if member.DisplayName == "" {
				member.DisplayName = current.DisplayName
			}
			if member.Role == "" {
				member.Role = current.Role
			}
			if !current.CreatedAt.IsZero() {
				member.CreatedAt = current.CreatedAt
			}
			member = normalizeConversationMemberManagedSources(member)
		}
		record, err := conversationMemberDBRecord(member)
		if err != nil {
			return err
		}
		return db.PutRecordTx(ctx, tx, table, key, record)
	})
}

func (r *Repository) DeleteMember(ctx context.Context, conversationID, memberKind, memberID string) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	return r.router.DeleteRecord(ctx, memberTable, strings.TrimSpace(conversationID)+"|"+strings.TrimSpace(memberKind)+"|"+strings.TrimSpace(memberID))
}

func (r *Repository) SaveRun(ctx context.Context, run RunRecord) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	run = normalizeRunDebug(run)
	payload, err := encodePayload(run)
	if err != nil {
		return err
	}
	return r.router.PutRecord(ctx, runTable, run.RunID, db.Record{
		"run_id":            run.RunID,
		"conversation_id":   run.ConversationID,
		"agent_id":          run.AgentID,
		"status":            run.Status,
		"runtime_kind":      run.RuntimeKind,
		"created_at":        formatTime(run.CreatedAt),
		conversationPayload: payload,
	})
}

func (r *Repository) GetRun(ctx context.Context, runID string) (RunRecord, error) {
	if r == nil || r.router == nil {
		return RunRecord{}, ErrServiceDependency
	}
	record, ok, err := r.router.GetRecord(ctx, runTable, runID)
	if err != nil {
		return RunRecord{}, err
	}
	if !ok {
		return RunRecord{}, ErrNotFound
	}
	var out RunRecord
	if err := decodePayload(record, &out); err != nil {
		return RunRecord{}, err
	}
	return normalizeRunDebug(out), nil
}

func (r *Repository) SaveEvent(ctx context.Context, event EventRecord) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	payload, err := encodePayload(event)
	if err != nil {
		return err
	}
	return r.router.PutRecord(ctx, "core.discussion_session_events", event.EventID, db.Record{
		"event_id":          event.EventID,
		"conversation_id":   event.ConversationID,
		"kind":              event.Kind,
		"status":            event.Status,
		"created_at":        formatTime(event.CreatedAt),
		conversationPayload: payload,
	})
}

func (r *Repository) SaveMissionLink(ctx context.Context, link LinkedMissionRecord) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	payload, err := encodePayload(link)
	if err != nil {
		return err
	}
	return r.router.PutRecord(ctx, missionLinkTable, link.ConversationID+"|"+link.MissionID, db.Record{
		"conversation_id":   link.ConversationID,
		"mission_id":        link.MissionID,
		"status":            link.Status,
		"created_at":        formatTime(link.CreatedAt),
		conversationPayload: payload,
	})
}

func (r *Repository) DeleteMessage(ctx context.Context, messageID string) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	return r.router.DeleteRecord(ctx, messageTable, messageID)
}

func (r *Repository) DeleteRun(ctx context.Context, runID string) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	return r.router.DeleteRecord(ctx, runTable, runID)
}

func (r *Repository) DeleteEvent(ctx context.Context, eventID string) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	return r.router.DeleteRecord(ctx, "core.discussion_session_events", eventID)
}

func (r *Repository) DeleteMissionLink(ctx context.Context, conversationID string, missionID string) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	return r.router.DeleteRecord(ctx, missionLinkTable, conversationID+"|"+missionID)
}

func (r *Repository) DeleteConversation(ctx context.Context, conversationID string) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	view, err := r.GetConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	for _, message := range view.Messages {
		if err := r.DeleteMessage(ctx, message.MessageID); err != nil {
			return err
		}
	}
	for _, member := range view.Members {
		if err := r.router.DeleteRecord(ctx, memberTable, member.ConversationID+"|"+member.MemberKind+"|"+member.MemberID); err != nil {
			return err
		}
	}
	for _, turn := range view.Turns {
		if err := r.router.DeleteRecord(ctx, turnTable, turn.TurnID); err != nil {
			return err
		}
	}
	for _, run := range view.Runs {
		if err := r.DeleteRun(ctx, run.RunID); err != nil {
			return err
		}
	}
	for _, event := range view.Events {
		if err := r.DeleteEvent(ctx, event.EventID); err != nil {
			return err
		}
	}
	for _, link := range view.LinkedMissions {
		if err := r.DeleteMissionLink(ctx, link.ConversationID, link.MissionID); err != nil {
			return err
		}
	}
	for _, attachment := range view.Attachments {
		if err := r.DeleteAttachment(ctx, attachment.AttachmentID); err != nil {
			return err
		}
	}
	return r.router.DeleteRecord(ctx, conversationTable, conversationID)
}

func (r *Repository) GetConversation(ctx context.Context, conversationID string) (ConversationRecordView, error) {
	conversationID = strings.TrimSpace(conversationID)
	conversation, err := r.GetConversationRecord(ctx, conversationID)
	if err != nil {
		return ConversationRecordView{}, err
	}
	view := ConversationRecordView{ConversationRecord: conversation}
	if view.Messages, err = r.listMessages(ctx, conversationID); err != nil {
		return ConversationRecordView{}, err
	}
	if view.Members, err = r.listMembers(ctx, conversationID); err != nil {
		return ConversationRecordView{}, err
	}
	if view.Turns, err = r.listTurns(ctx, conversationID); err != nil {
		return ConversationRecordView{}, err
	}
	if view.Runs, err = r.listRuns(ctx, conversationID); err != nil {
		return ConversationRecordView{}, err
	}
	if view.Events, err = r.listEvents(ctx, conversationID); err != nil {
		return ConversationRecordView{}, err
	}
	if view.LinkedMissions, err = r.listMissionLinks(ctx, conversationID); err != nil {
		return ConversationRecordView{}, err
	}
	if view.Attachments, err = r.listAttachments(ctx, conversationID); err != nil {
		return ConversationRecordView{}, err
	}
	return view, nil
}

func (r *Repository) GetConversationRecord(ctx context.Context, conversationID string) (ConversationRecord, error) {
	if r == nil || r.router == nil {
		return ConversationRecord{}, ErrServiceDependency
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return ConversationRecord{}, ErrInvalidRequest
	}
	record, ok, err := r.router.GetRecord(ctx, conversationTable, conversationID)
	if err != nil {
		return ConversationRecord{}, err
	}
	if !ok {
		return ConversationRecord{}, ErrNotFound
	}
	var conversation ConversationRecord
	if err := decodePayload(record, &conversation); err != nil {
		return ConversationRecord{}, err
	}
	return conversation, nil
}

func (r *Repository) GetConversationMessagePage(ctx context.Context, conversationID string, limit int, offset int) (ConversationRecordView, MessagePageMetaDTO, error) {
	if r == nil || r.router == nil {
		return ConversationRecordView{}, MessagePageMetaDTO{}, ErrServiceDependency
	}
	record, ok, err := r.router.GetRecord(ctx, conversationTable, conversationID)
	if err != nil {
		return ConversationRecordView{}, MessagePageMetaDTO{}, err
	}
	if !ok {
		return ConversationRecordView{}, MessagePageMetaDTO{}, ErrNotFound
	}
	var conversation ConversationRecord
	if err := decodePayload(record, &conversation); err != nil {
		return ConversationRecordView{}, MessagePageMetaDTO{}, err
	}
	view := ConversationRecordView{ConversationRecord: conversation}
	messages, page, err := r.ListMessagesPage(ctx, conversationID, limit, offset)
	if err != nil {
		return ConversationRecordView{}, MessagePageMetaDTO{}, err
	}
	view.Messages = messages
	if view.Members, err = r.listMembers(ctx, conversationID); err != nil {
		return ConversationRecordView{}, MessagePageMetaDTO{}, err
	}
	if view.Turns, err = r.listTurns(ctx, conversationID); err != nil {
		return ConversationRecordView{}, MessagePageMetaDTO{}, err
	}
	if view.Runs, err = r.listRuns(ctx, conversationID); err != nil {
		return ConversationRecordView{}, MessagePageMetaDTO{}, err
	}
	if view.Events, err = r.listEvents(ctx, conversationID); err != nil {
		return ConversationRecordView{}, MessagePageMetaDTO{}, err
	}
	if view.LinkedMissions, err = r.listMissionLinks(ctx, conversationID); err != nil {
		return ConversationRecordView{}, MessagePageMetaDTO{}, err
	}
	if view.Attachments, err = r.listAttachments(ctx, conversationID); err != nil {
		return ConversationRecordView{}, MessagePageMetaDTO{}, err
	}
	return view, page, nil
}

func (r *Repository) ListConversations(ctx context.Context) ([]ConversationRecordView, error) {
	if r == nil || r.router == nil {
		return nil, ErrServiceDependency
	}
	records, err := snapshot(ctx, r.router, conversationTable)
	if err != nil {
		return nil, err
	}
	out := make([]ConversationRecordView, 0, len(records))
	for _, record := range records {
		var conversation ConversationRecord
		if err := decodePayload(record, &conversation); err != nil {
			return nil, err
		}
		out = append(out, ConversationRecordView{ConversationRecord: conversation})
	}
	if err := r.attachConversationSummaryFields(ctx, out); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].ConversationID < out[j].ConversationID
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

func (r *Repository) attachConversationSummaryFields(ctx context.Context, views []ConversationRecordView) error {
	byID := make(map[string]*ConversationRecordView, len(views))
	for i := range views {
		byID[views[i].ConversationID] = &views[i]
	}
	messageRecords, err := snapshot(ctx, r.router, messageTable)
	if err != nil {
		return err
	}
	latestMessageRecords := make(map[string]db.Record, len(views))
	for _, record := range messageRecords {
		conversationID, _ := record["conversation_id"].(string)
		view := byID[conversationID]
		if view == nil {
			continue
		}
		if current, ok := latestMessageRecords[conversationID]; !ok || messageRecordLess(current, record) {
			latestMessageRecords[conversationID] = record
		}
	}
	for conversationID, record := range latestMessageRecords {
		var item MessageRecord
		if err := decodePayload(record, &item); err != nil {
			return err
		}
		byID[conversationID].Messages = []MessageRecord{item}
	}
	turnRecords, err := snapshot(ctx, r.router, turnTable)
	if err != nil {
		return err
	}
	for _, record := range turnRecords {
		conversationID, _ := record["conversation_id"].(string)
		view := byID[conversationID]
		if view == nil {
			continue
		}
		var item TurnRecord
		if err := decodePayload(record, &item); err != nil {
			return err
		}
		if strings.HasPrefix(strings.TrimSpace(item.IdempotencyKey), "gwin_") {
			view.Turns = append(view.Turns, item)
		}
	}
	memberRecords, err := snapshot(ctx, r.router, memberTable)
	if err != nil {
		return err
	}
	for _, record := range memberRecords {
		conversationID, _ := record["conversation_id"].(string)
		view := byID[conversationID]
		if view == nil {
			continue
		}
		var item ConversationMemberRecord
		if err := decodePayload(record, &item); err != nil {
			return err
		}
		view.Members = append(view.Members, normalizeConversationMemberManagedSources(item))
	}
	for _, view := range byID {
		sort.SliceStable(view.Members, func(i, j int) bool {
			return view.Members[i].CreatedAt.Before(view.Members[j].CreatedAt)
		})
	}
	linkRecords, err := snapshot(ctx, r.router, missionLinkTable)
	if err != nil {
		return err
	}
	for _, record := range linkRecords {
		conversationID, _ := record["conversation_id"].(string)
		view := byID[conversationID]
		if view == nil {
			continue
		}
		var item LinkedMissionRecord
		if err := decodePayload(record, &item); err != nil {
			return err
		}
		view.LinkedMissions = append(view.LinkedMissions, item)
	}
	attachmentRecords, err := snapshot(ctx, r.router, attachmentTable)
	if err != nil {
		return err
	}
	for _, record := range attachmentRecords {
		conversationID, _ := record["conversation_id"].(string)
		view := byID[conversationID]
		if view == nil {
			continue
		}
		var item AttachmentRecord
		if err := decodePayload(record, &item); err != nil {
			return err
		}
		view.Attachments = append(view.Attachments, item)
	}
	return nil
}

func (r *Repository) listMessages(ctx context.Context, conversationID string) ([]MessageRecord, error) {
	records, err := r.conversationRecords(ctx, messageTable, conversationID)
	if err != nil {
		return nil, err
	}
	out := make([]MessageRecord, 0)
	for _, record := range records {
		if got, _ := record["conversation_id"].(string); got != conversationID {
			continue
		}
		var item MessageRecord
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

func (r *Repository) ListMessagesPage(ctx context.Context, conversationID string, limit int, offset int) ([]MessageRecord, MessagePageMetaDTO, error) {
	if r == nil || r.router == nil {
		return nil, MessagePageMetaDTO{}, ErrServiceDependency
	}
	if _, ok, err := r.router.GetRecord(ctx, conversationTable, conversationID); err != nil {
		return nil, MessagePageMetaDTO{}, err
	} else if !ok {
		return nil, MessagePageMetaDTO{}, ErrNotFound
	}
	records, err := r.conversationRecords(ctx, messageTable, conversationID)
	if err != nil {
		return nil, MessagePageMetaDTO{}, err
	}
	conversationRecords := make([]db.Record, 0)
	for _, record := range records {
		if got, _ := record["conversation_id"].(string); got != conversationID {
			continue
		}
		conversationRecords = append(conversationRecords, record)
	}
	sort.SliceStable(conversationRecords, func(i, j int) bool {
		return messageRecordLess(conversationRecords[i], conversationRecords[j])
	})
	total := len(conversationRecords)
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	end := total - offset
	if end < 0 {
		end = 0
	}
	start := end - limit
	if start < 0 {
		start = 0
	}
	selected := conversationRecords[start:end]
	pageMessages := make([]MessageRecord, 0, len(selected))
	for _, record := range selected {
		var item MessageRecord
		if err := decodePayload(record, &item); err != nil {
			return nil, MessagePageMetaDTO{}, err
		}
		pageMessages = append(pageMessages, item)
	}
	page := MessagePageMetaDTO{
		Limit:    limit,
		Offset:   offset,
		Total:    total,
		Returned: len(pageMessages),
		HasMore:  start > 0,
	}
	return pageMessages, page, nil
}

func (r *Repository) listMembers(ctx context.Context, conversationID string) ([]ConversationMemberRecord, error) {
	records, err := r.conversationRecords(ctx, memberTable, conversationID)
	if err != nil {
		return nil, err
	}
	out := make([]ConversationMemberRecord, 0)
	for _, record := range records {
		if got, _ := record["conversation_id"].(string); got != conversationID {
			continue
		}
		var item ConversationMemberRecord
		if err := decodePayload(record, &item); err != nil {
			return nil, err
		}
		item = normalizeConversationMemberManagedSources(item)
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (r *Repository) listRuns(ctx context.Context, conversationID string) ([]RunRecord, error) {
	records, err := r.conversationRecords(ctx, runTable, conversationID)
	if err != nil {
		return nil, err
	}
	out := make([]RunRecord, 0)
	for _, record := range records {
		if got, _ := record["conversation_id"].(string); got != conversationID {
			continue
		}
		var item RunRecord
		if err := decodePayload(record, &item); err != nil {
			return nil, err
		}
		out = append(out, normalizeRunDebug(item))
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (r *Repository) listEvents(ctx context.Context, conversationID string) ([]EventRecord, error) {
	records, err := r.conversationRecords(ctx, "core.discussion_session_events", conversationID)
	if err != nil {
		return nil, err
	}
	out := make([]EventRecord, 0)
	for _, record := range records {
		if got, _ := record["conversation_id"].(string); got != conversationID {
			continue
		}
		var item EventRecord
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

func (r *Repository) listMissionLinks(ctx context.Context, conversationID string) ([]LinkedMissionRecord, error) {
	records, err := r.conversationRecords(ctx, missionLinkTable, conversationID)
	if err != nil {
		return nil, err
	}
	out := make([]LinkedMissionRecord, 0)
	for _, record := range records {
		if got, _ := record["conversation_id"].(string); got != conversationID {
			continue
		}
		var item LinkedMissionRecord
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

func normalizeRunDebug(run RunRecord) RunRecord {
	if run.Debug == nil {
		run.Debug = &RunDebugDTO{}
	}
	debug := *run.Debug
	if debug.ConversationID == "" {
		debug.ConversationID = run.ConversationID
	}
	if debug.RunID == "" {
		debug.RunID = run.RunID
	}
	if strings.TrimSpace(run.AgentID) != "" {
		debug.Selected.AgentID = run.AgentID
	}
	if strings.TrimSpace(run.RuntimeKind) != "" {
		debug.Selected.RuntimeKind = run.RuntimeKind
	}
	if len(debug.ProviderMetadata) == 0 && len(run.ProviderMetadata) > 0 {
		debug.ProviderMetadata = cloneStringMap(run.ProviderMetadata)
	}
	if debug.CreatedAt.IsZero() {
		debug.CreatedAt = firstTime(run.StartedAt, run.CreatedAt, run.CompletedAt)
	}
	run.Debug = &debug
	return run
}
