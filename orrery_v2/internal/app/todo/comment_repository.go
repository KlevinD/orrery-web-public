package todo

import (
	"context"
	"encoding/json"
	"strings"

	"orrery_v2/internal/platform/db"
)

func (r *Repository) SaveIssueComment(ctx context.Context, comment IssueComment) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	comment.TenantID = normalizedTenantID(comment.TenantID)
	payload, err := encodePayload(comment)
	if err != nil {
		return err
	}
	return r.router.PutRecord(ctx, issueCommentTable, scopedRecordKey(comment.TenantID, comment.CommentID), db.Record{
		"tenant_id":  comment.TenantID,
		"comment_id": comment.CommentID,
		"issue_kind": string(comment.IssueKind),
		"issue_id":   comment.IssueID,
		"author_id":  comment.AuthorID,
		"created_at": formatTime(comment.CreatedAt),
		"updated_at": formatTime(comment.UpdatedAt),
		payloadField: payload,
	})
}

func (r *Repository) ListIssueComments(ctx context.Context, kind IssueKind, issueID string) ([]IssueComment, error) {
	return r.ListIssueCommentsForTenant(ctx, defaultTenantID, kind, issueID)
}

func (r *Repository) ListIssueCommentsForTenant(ctx context.Context, tenantID string, kind IssueKind, issueID string) ([]IssueComment, error) {
	if r == nil || r.router == nil {
		return nil, ErrServiceDependency
	}
	records, err := snapshotForTenant(ctx, r.router, issueCommentTable, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]IssueComment, 0, len(records))
	for _, record := range records {
		if recordString(record, "issue_kind") != string(kind) || recordString(record, "issue_id") != strings.TrimSpace(issueID) {
			continue
		}
		var comment IssueComment
		if err := decodePayload(record, &comment); err != nil {
			return nil, err
		}
		comment.TenantID = normalizedTenantID(comment.TenantID)
		out = append(out, comment)
	}
	sortIssueComments(out)
	return out, nil
}

func deleteIssueCommentsTx(ctx context.Context, tx db.Tx, table, tenantID string, kind IssueKind, issueID string) error {
	rows, err := tx.QueryContext(ctx, `SELECT record_key, payload_json FROM platform_records WHERE table_name = ?`, table)
	if err != nil {
		return err
	}
	defer rows.Close()
	keys := make([]string, 0)
	for rows.Next() {
		var key string
		var raw string
		if err := rows.Scan(&key, &raw); err != nil {
			return err
		}
		var record db.Record
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			return err
		}
		if recordString(record, "tenant_id") == tenantID && recordString(record, "issue_kind") == string(kind) && recordString(record, "issue_id") == strings.TrimSpace(issueID) {
			keys = append(keys, key)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, key := range keys {
		if _, err := tx.ExecContext(ctx, `DELETE FROM platform_records WHERE table_name = ? AND record_key = ?`, table, key); err != nil {
			return err
		}
	}
	return nil
}
