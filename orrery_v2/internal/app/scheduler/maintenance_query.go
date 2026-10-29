package scheduler

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

func postgresTx(tx db.Tx) bool {
	dialect, ok := tx.(interface{ Dialect() db.Dialect })
	return ok && dialect.Dialect() == db.DialectPostgres
}

func runRecordField(tx db.Tx, field string) string {
	if postgresTx(tx) {
		return "(payload_json::jsonb ->> '" + field + "')"
	}
	return "json_extract(payload_json, '$." + field + "')"
}

func runPayloadField(tx db.Tx, field string) string {
	if postgresTx(tx) {
		return "((payload_json::jsonb ->> 'payload')::jsonb #>> '{" + strings.ReplaceAll(field, ".", ",") + "}')"
	}
	return "json_extract(json_extract(payload_json, '$.payload'), '$." + field + "')"
}

func runTimeExpr(tx db.Tx, fields ...string) string {
	parts := []string{}
	for _, field := range fields {
		parts = append(parts, "NULLIF(NULLIF("+runPayloadField(tx, field)+", '0001-01-01T00:00:00Z'), '')")
	}
	value := "COALESCE(" + strings.Join(parts, ", ") + ")"
	if postgresTx(tx) {
		return "CAST(" + value + " AS TIMESTAMPTZ)"
	}
	return "julianday(" + value + ")"
}

func runTimeParameter(tx db.Tx) string {
	if postgresTx(tx) {
		return "CAST(? AS TIMESTAMPTZ)"
	}
	return "julianday(?)"
}

func emptyTodoScanExpr(tx db.Tx) string {
	parts := []string{runPayloadField(tx, "todo_automation_scan") + " IS NOT NULL"}
	for _, field := range []string{"scanned_count", "eligible_count", "claimed_count", "discussion_sync_count", "planned_count", "recovered_count", "skipped_count", "failed_count"} {
		value := runPayloadField(tx, "todo_automation_scan."+field)
		if postgresTx(tx) {
			value = "CAST(" + value + " AS BIGINT)"
		}
		parts = append(parts, "COALESCE("+value+", 0) = 0")
	}
	return "(" + strings.Join(parts, " AND ") + ")"
}

func taskRetentionRows(ctx context.Context, tx db.Tx, table, taskID string, now time.Time, completedRetention, diagnosticRetention time.Duration, limit int) (*sql.Rows, error) {
	anchor := runTimeExpr(tx, "completed_at", "created_at")
	status := runRecordField(tx, "status")
	query := "SELECT record_key, payload_json FROM platform_records WHERE table_name = ? AND " + runRecordField(tx, "scheduled_task_id") + " = ? AND ((" + status + " = ? AND " + anchor + " < " + runTimeParameter(tx) + ") OR (" + status + " IN (?, ?) AND " + anchor + " < " + runTimeParameter(tx) + ")) ORDER BY " + anchor + ", record_key LIMIT ?"
	return tx.QueryContext(ctx, query, table, taskID, RunStatusCompleted, now.Add(-completedRetention).Format(time.RFC3339Nano), RunStatusFailed, RunStatusSkipped, now.Add(-diagnosticRetention).Format(time.RFC3339Nano), min(limit, 1000))
}

func todoRetentionRows(ctx context.Context, tx db.Tx, table string, now time.Time, emptyRetention, diagnosticRetention time.Duration) (*sql.Rows, error) {
	empty := "(" + runRecordField(tx, "status") + " = 'completed' AND " + emptyTodoScanExpr(tx) + ")"
	anchor := "CASE WHEN " + empty + " THEN " + runTimeExpr(tx, "last_occurrence_at", "completed_at", "created_at") + " ELSE " + runTimeExpr(tx, "completed_at", "created_at") + " END"
	cutoff := "CASE WHEN " + empty + " THEN " + runTimeParameter(tx) + " ELSE " + runTimeParameter(tx) + " END"
	query := "SELECT record_key, payload_json FROM platform_records WHERE table_name = ? AND " + runRecordField(tx, "target_mode") + " = ? AND " + runRecordField(tx, "status") + " IN (?, ?, ?) AND " + anchor + " < " + cutoff + " ORDER BY " + anchor + ", record_key LIMIT 100"
	return tx.QueryContext(ctx, query, table, TargetModeTodoAutomationScan, RunStatusCompleted, RunStatusFailed, RunStatusSkipped, now.Add(-emptyRetention).Format(time.RFC3339Nano), now.Add(-diagnosticRetention).Format(time.RFC3339Nano))
}

func emptyAggregationRows(ctx context.Context, tx db.Tx, table string, run ScheduledRun, at time.Time, window time.Duration) (*sql.Rows, error) {
	activity := runTimeExpr(tx, "last_occurrence_at", "completed_at", "created_at")
	distance := "ABS(" + activity + " - " + runTimeParameter(tx) + ")"
	if postgresTx(tx) {
		distance = "ABS(EXTRACT(EPOCH FROM (" + activity + " - " + runTimeParameter(tx) + ")))"
	}
	query := "SELECT record_key, payload_json FROM platform_records WHERE table_name = ? AND " + runRecordField(tx, "scheduled_task_id") + " = ? AND " + runRecordField(tx, "status") + " = ? AND " + runRecordField(tx, "target_mode") + " = ? AND record_key <> ? AND COALESCE(" + runPayloadField(tx, "aggregated_into_run_id") + ", '') = '' AND " + emptyTodoScanExpr(tx) + " AND " + activity + " >= " + runTimeParameter(tx) + " AND " + activity + " <= " + runTimeParameter(tx) + " ORDER BY " + distance + ", " + activity + " DESC, record_key DESC LIMIT 1"
	return tx.QueryContext(ctx, query, table, run.ScheduledTaskID, RunStatusCompleted, TargetModeTodoAutomationScan, run.ScheduledRunID, at.Add(-window).Format(time.RFC3339Nano), at.Add(window).Format(time.RFC3339Nano), at.Format(time.RFC3339Nano))
}
