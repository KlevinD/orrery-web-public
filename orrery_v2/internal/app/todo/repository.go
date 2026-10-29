package todo

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

const (
	todoTable                  = "core.todos"
	todoAutomationTable        = "core.todo_automation"
	todoExecutionPlanTable     = "core.todo_execution_plans"
	todoDiscussionBindingTable = "core.todo_discussion_bindings"
	todoMissionBindingTable    = "core.todo_mission_bindings"
	todoMissionOwnerTable      = "core.todo_mission_owners"
	todoExecutionAttemptTable  = "core.todo_execution_attempts"
	todoExecutionProgressTable = "core.todo_execution_progress"
	ideaTable                  = "core.ideas"
	issueCommentTable          = "core.issue_comments"
	payloadField               = "payload"
	timeFormat                 = "2006-01-02T15:04:05.999999999Z07:00"
	defaultTenantID            = "system"
)

type Repository struct {
	router *db.Router
}

func NewRepository(router *db.Router) *Repository {
	return &Repository{router: router}
}

func normalizedTenantID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultTenantID
	}
	return value
}

func scopedRecordKey(tenantID, id string) string {
	return normalizedTenantID(tenantID) + "::" + strings.TrimSpace(id)
}

func snapshotForTenant(ctx context.Context, router *db.Router, logicalTable, tenantID string) ([]db.Record, error) {
	if router == nil {
		return nil, ErrServiceDependency
	}
	store, table, err := router.StoreForTable(logicalTable)
	if err != nil {
		return nil, err
	}
	tenantID = normalizedTenantID(tenantID)
	prefix := tenantID + "::"
	rows, err := store.Query(ctx, `
SELECT payload_json
FROM platform_records
WHERE table_name = ? AND record_key LIKE ? ESCAPE '!'
ORDER BY record_key ASC`, table, escapeLikePattern(prefix)+"%")
	if err != nil {
		return nil, fmt.Errorf("query tenant work records: %w", err)
	}
	defer rows.Close()
	out := []db.Record{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan tenant work record: %w", err)
		}
		var record db.Record
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			return nil, fmt.Errorf("decode tenant work record: %w", err)
		}
		if recordString(record, "tenant_id") != tenantID {
			continue
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tenant work records: %w", err)
	}
	return out, nil
}

func encodePayload(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode work hub payload: %w", err)
	}
	return string(payload), nil
}

func decodePayload(record db.Record, out any) error {
	raw, ok := record[payloadField].(string)
	if !ok || raw == "" {
		return fmt.Errorf("work hub repository payload missing")
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return fmt.Errorf("decode work hub payload: %w", err)
	}
	return nil
}

func recordString(record db.Record, key string) string {
	value, _ := record[key].(string)
	return strings.TrimSpace(value)
}

func recordStrings(record db.Record, key string) []string {
	values, ok := record[key].([]string)
	if ok {
		return append([]string(nil), values...)
	}
	raw, ok := record[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, value := range raw {
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			out = append(out, strings.TrimSpace(text))
		}
	}
	return out
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(timeFormat)
}

func deleteRecordTx(ctx context.Context, tx db.Tx, table, key string) error {
	result, err := tx.ExecContext(ctx, `DELETE FROM platform_records WHERE table_name = ? AND record_key = ?`, table, key)
	if err != nil {
		return fmt.Errorf("delete work hub record: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted work hub record count: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func sortIssueComments(items []IssueComment) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].CommentID < items[j].CommentID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
}
