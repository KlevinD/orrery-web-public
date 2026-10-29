package discussionsession

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"orrery_v2/internal/platform/db"
)

// Only fixed field names from this file may be used in these SQL expressions.
func historyJSONField(dialect db.Dialect, field string, nested bool) string {
	if dialect == db.DialectPostgres {
		base := "payload_json::jsonb"
		if nested {
			base = "(payload_json::jsonb->>'payload')::jsonb"
		}
		return "COALESCE(" + base + "->>'" + field + "', '')"
	}
	base := "payload_json"
	if nested {
		base = "json_extract(payload_json, '$.payload')"
	}
	return "COALESCE(json_extract(" + base + ", '$." + field + "'), '')"
}

func historyTimestamp(dialect db.Dialect, expression string) string {
	if dialect == db.DialectPostgres {
		return "CAST(NULLIF(" + expression + ", '') AS TIMESTAMPTZ)"
	}
	return "julianday(" + expression + ")"
}

func (r *Repository) historyRecords(ctx context.Context, logicalTable, predicate, order string, args []any, limit, offset int) ([]db.Record, error) {
	store, table, err := r.router.StoreForTable(logicalTable)
	if err != nil {
		return nil, err
	}
	query := "SELECT payload_json FROM platform_records WHERE table_name = ? AND (" + predicate + ")"
	values := append([]any{table}, args...)
	if order != "" {
		query += " ORDER BY " + order
	}
	if limit > 0 {
		query += " LIMIT ? OFFSET ?"
		values = append(values, limit, offset)
	}
	rows, err := store.Query(ctx, query, values...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []db.Record
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var record db.Record
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, rows.Err()
}

func historyIDPredicate(field string, ids []string) (string, []any) {
	marks, values := make([]string, len(ids)), make([]any, len(ids))
	for i, id := range ids {
		marks[i], values[i] = "?", id
	}
	return field + " IN (" + strings.Join(marks, ",") + ")", values
}

func (r *Repository) historyConversationViews(ctx context.Context, tenantID string) ([]ConversationRecordView, error) {
	store, _, err := r.router.StoreForTable(conversationTable)
	if err != nil {
		return nil, err
	}
	tenant := historyJSONField(store.Dialect(), "tenant_id", false)
	predicate := "COALESCE(NULLIF(" + tenant + ", ''), 'global') = ?"
	records, err := r.historyRecords(ctx, conversationTable, predicate, "record_key ASC", []any{tenantID}, 0, 0)
	if err != nil {
		return nil, err
	}
	views := make([]ConversationRecordView, 0, len(records))
	for _, record := range records {
		var conversation ConversationRecord
		if err := decodePayload(record, &conversation); err != nil {
			return nil, err
		}
		if firstNonBlank(conversation.TenantID, "global") != tenantID {
			continue
		}
		views = append(views, ConversationRecordView{ConversationRecord: conversation})
	}
	memberStore, _, err := r.router.StoreForTable(memberTable)
	if err != nil {
		return nil, err
	}
	// Batch only authorized-tenant metadata; never load all transcripts or traces.
	for start := 0; start < len(views); start += 200 {
		end := min(start+200, len(views))
		ids, indexes := []string{}, map[string]int{}
		for i := start; i < end; i++ {
			ids = append(ids, views[i].ConversationID)
			indexes[views[i].ConversationID] = i
		}
		predicate, values := historyIDPredicate(historyJSONField(memberStore.Dialect(), "conversation_id", false), ids)
		members, err := r.historyRecords(ctx, memberTable, predicate, "", values, 0, 0)
		if err != nil {
			return nil, err
		}
		for _, record := range members {
			var member ConversationMemberRecord
			if err := decodePayload(record, &member); err != nil {
				return nil, err
			}
			if i, ok := indexes[member.ConversationID]; ok {
				views[i].Members = append(views[i].Members, member)
			}
		}
	}
	return views, nil
}

func (r *Repository) historyMessageSQL() (*db.Store, string, string, error) {
	store, _, err := r.router.StoreForTable(messageTable)
	if err != nil {
		return nil, "", "", err
	}
	dialect := store.Dialect()
	role := historyJSONField(dialect, "Role", true)
	filter := role + " IN ('user', 'assistant')"
	order := historyTimestamp(dialect, historyJSONField(dialect, "created_at", false))
	return store, filter, order, nil
}

func decodeHistoryMessages(records []db.Record, err error) ([]MessageRecord, error) {
	if err != nil {
		return nil, err
	}
	messages := make([]MessageRecord, 0, len(records))
	for _, record := range records {
		var message MessageRecord
		if err := decodePayload(record, &message); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, nil
}

func (r *Repository) searchHistoryMessages(ctx context.Context, views []ConversationRecordView, args historyArguments) ([]MessageRecord, error) {
	if len(views) == 0 {
		return nil, nil
	}
	store, filter, date, err := r.historyMessageSQL()
	if err != nil {
		return nil, err
	}
	// SQLite and PostgreSQL both support these bounded parameterized predicates.
	// A scope this large should be narrowed explicitly, never silently truncated.
	if len(views) > 2000 {
		return nil, fmt.Errorf("%w: narrow the search with session_id", ErrInvalidRequest)
	}
	ids := make([]string, 0, len(views))
	titleIDs := []string{}
	for _, view := range views {
		ids = append(ids, view.ConversationID)
		if strings.Contains(strings.ToLower(view.Title), strings.ToLower(args.Query)) {
			titleIDs = append(titleIDs, view.ConversationID)
		}
	}
	convField := historyJSONField(store.Dialect(), "conversation_id", false)
	scope, values := historyIDPredicate(convField, ids)
	content := historyJSONField(store.Dialect(), "Content", true)
	match := "LOWER(" + content + ") LIKE LOWER(?) ESCAPE '!'"
	pattern := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(args.Query)
	values = append(values, "%"+pattern+"%")
	if len(titleIDs) > 0 {
		titleMatch, titleValues := historyIDPredicate(convField, titleIDs)
		match += " OR " + titleMatch
		values = append(values, titleValues...)
	}
	filter += " AND " + scope + " AND (" + match + ")"
	if args.After != "" {
		filter += " AND " + date + " >= " + historyTimestamp(store.Dialect(), "?")
		values = append(values, args.After)
	}
	if args.Before != "" {
		filter += " AND " + date + " < " + historyTimestamp(store.Dialect(), "?")
		values = append(values, args.Before)
	}
	return decodeHistoryMessages(r.historyRecords(ctx, messageTable, filter, date+" DESC, record_key DESC", values, args.Limit+1, args.Offset))
}

func (r *Repository) readHistoryMessages(ctx context.Context, conversationID string, offset, limit int) ([]MessageRecord, error) {
	store, filter, date, err := r.historyMessageSQL()
	if err != nil {
		return nil, err
	}
	filter += " AND " + historyJSONField(store.Dialect(), "conversation_id", false) + " = ?"
	return decodeHistoryMessages(r.historyRecords(ctx, messageTable, filter, date+" ASC, record_key ASC", []any{conversationID}, limit, offset))
}

func (r *Repository) historyMessagePosition(ctx context.Context, conversationID, messageID string) (int, error) {
	store, filter, date, err := r.historyMessageSQL()
	if err != nil {
		return 0, err
	}
	_, table, err := r.router.StoreForTable(messageTable)
	if err != nil {
		return 0, err
	}
	filter += " AND " + historyJSONField(store.Dialect(), "conversation_id", false) + " = ?"
	query := "SELECT position FROM (SELECT record_key, ROW_NUMBER() OVER (ORDER BY " + date + " ASC, record_key ASC) - 1 AS position FROM platform_records WHERE table_name = ? AND " + filter + ") messages WHERE record_key = ?"
	rows, err := store.Query(ctx, query, table, conversationID, messageID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return 0, err
		}
		return 0, ErrNotFound
	}
	var position int
	if err := rows.Scan(&position); err != nil {
		return 0, err
	}
	return position, rows.Err()
}
