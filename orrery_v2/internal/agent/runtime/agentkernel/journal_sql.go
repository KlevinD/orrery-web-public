package agentkernel

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/db"
)

const toolJournalTable = "runtime_agent_kernel_tool_executions"

// SQLToolJournal uses the existing platform_records table in the host's runtime
// database. It owns neither database lifecycle nor application authorization.
// It does not create tables or activate the managed runtime at server startup.
type SQLToolJournal struct{ store *db.Store }

func NewSQLToolJournal(store *db.Store) (*SQLToolJournal, error) {
	if store == nil || (store.Driver() != db.DriverFile && store.Driver() != db.DriverPostgres) {
		return nil, ErrConfiguration
	}
	return &SQLToolJournal{store: store}, nil
}

func journalPrefix(tenantID string, runID agentcore.AgentRunID) string {
	hash, _ := digest([]string{tenantID, string(runID)})
	return hash + ":"
}

func journalKey(key ToolExecutionKey) string {
	hash, _ := digest(key.CallID)
	return journalPrefix(key.TenantID, key.RunID) + hash
}

func encodeExecution(record ToolExecution) (db.Record, error) {
	raw, err := json.Marshal(record)
	if err != nil {
		return nil, ErrJournal
	}
	var encoded db.Record
	if json.Unmarshal(raw, &encoded) != nil {
		return nil, ErrJournal
	}
	return encoded, nil
}

func validDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func decodeExecution(encoded db.Record) (ToolExecution, error) {
	raw, err := json.Marshal(encoded)
	if err != nil {
		return ToolExecution{}, ErrJournal
	}
	var record ToolExecution
	if json.Unmarshal(raw, &record) != nil || !record.ToolExecutionKey.valid() || record.AgentID.IsZero() || record.ToolID == "" || record.ClaimID == "" || !validDigest(record.Fingerprint) || record.CreatedAt.IsZero() || record.UpdatedAt.Before(record.CreatedAt) {
		return ToolExecution{}, ErrJournal
	}
	if !(record.State == ToolExecutionStarted && record.Reason == "") && !finalToolState(record.State, record.Reason) {
		return ToolExecution{}, ErrJournal
	}
	return record, nil
}

func journalError(ctx context.Context, err error) error {
	if err == nil || errors.Is(err, ErrToolRecorded) || errors.Is(err, ErrToolConflict) || errors.Is(err, ErrRunUnresolved) || errors.Is(err, ErrInput) {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// SQL errors can contain paths, connection strings or payloads.
	return ErrJournal
}

func sameExecution(a, b ToolExecution) bool {
	return a.ToolExecutionKey == b.ToolExecutionKey && a.AgentID == b.AgentID && a.ToolID == b.ToolID && a.Fingerprint == b.Fingerprint
}

func (j *SQLToolJournal) Begin(ctx context.Context, intent ToolExecution) (ToolExecution, error) {
	if !intent.ToolExecutionKey.valid() || intent.AgentID.IsZero() || intent.ToolID == "" || !validDigest(intent.Fingerprint) {
		return ToolExecution{}, ErrInput
	}
	now := time.Now().UTC()
	intent.ClaimID, intent.State, intent.Reason = uuid.NewString(), ToolExecutionStarted, ""
	intent.CreatedAt, intent.UpdatedAt = now, now
	encoded, err := encodeExecution(intent)
	if err != nil {
		return ToolExecution{}, err
	}
	err = j.store.WithTx(ctx, func(tx db.Tx) error {
		prefix := journalPrefix(intent.TenantID, intent.RunID)
		if err := db.LockRecordKeyTx(ctx, tx, toolJournalTable, prefix); err != nil {
			return err
		}
		created, err := db.InsertRecordTx(ctx, tx, toolJournalTable, journalKey(intent.ToolExecutionKey), encoded)
		if err != nil {
			return err
		}
		if created {
			// Insert first to acquire SQLite's writer lock before reading. Returning
			// an error rolls the new claim back together with this transaction.
			return checkRunExecutions(ctx, tx, intent)
		}
		existing, found, err := db.GetRecordTx(ctx, tx, toolJournalTable, journalKey(intent.ToolExecutionKey))
		if err != nil || !found {
			return ErrJournal
		}
		record, err := decodeExecution(existing)
		if err != nil {
			return err
		}
		if !sameExecution(record, intent) {
			return ErrToolConflict
		}
		return ErrToolRecorded
	})
	if err != nil {
		return ToolExecution{}, journalError(ctx, err)
	}
	return intent, nil
}

func (j *SQLToolJournal) Finish(ctx context.Context, claim ToolExecution, state ToolExecutionState, reason string) error {
	if !claim.ToolExecutionKey.valid() || claim.ClaimID == "" || !finalToolState(state, reason) {
		return ErrInput
	}
	err := j.store.WithTx(ctx, func(tx db.Tx) error {
		key := journalKey(claim.ToolExecutionKey)
		if err := db.LockRecordKeyTx(ctx, tx, toolJournalTable, journalPrefix(claim.TenantID, claim.RunID)); err != nil {
			return err
		}
		encoded, found, err := db.GetRecordTx(ctx, tx, toolJournalTable, key)
		if err != nil || !found {
			return ErrJournal
		}
		record, err := decodeExecution(encoded)
		if err != nil {
			return err
		}
		if !sameExecution(record, claim) || record.ClaimID != claim.ClaimID {
			return ErrToolConflict
		}
		if record.State != ToolExecutionStarted {
			if record.State == state && record.Reason == reason {
				return nil
			}
			return ErrToolConflict
		}
		record.State, record.Reason, record.UpdatedAt = state, reason, time.Now().UTC()
		encoded, err = encodeExecution(record)
		if err != nil {
			return err
		}
		return db.PutRecordTx(ctx, tx, toolJournalTable, key, encoded)
	})
	return journalError(ctx, err)
}

func checkRunExecutions(ctx context.Context, tx db.Tx, intent ToolExecution) error {
	return checkRunExecutionBatch(ctx, tx, intent, map[string]bool{journalKey(intent.ToolExecutionKey): true})
}

func checkRunExecutionBatch(ctx context.Context, tx db.Tx, intent ToolExecution, batch map[string]bool) error {
	prefix := journalPrefix(intent.TenantID, intent.RunID)
	// The prefix contains only a digest and ':', so LIKE has no wildcard input.
	// Do not use an upper '~' sentinel: PostgreSQL locale collations can sort
	// punctuation before hex digits, excluding records that belong to this run.
	rows, err := tx.QueryContext(ctx, `SELECT record_key, payload_json FROM platform_records WHERE table_name = ? AND record_key LIKE ?`, toolJournalTable, prefix+"%")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key, payload string
		if rows.Scan(&key, &payload) != nil {
			return ErrJournal
		}
		if batch[key] {
			continue
		}
		var encoded db.Record
		if json.Unmarshal([]byte(payload), &encoded) != nil {
			return ErrJournal
		}
		record, err := decodeExecution(encoded)
		if err != nil || record.TenantID != intent.TenantID || record.RunID != intent.RunID || journalKey(record.ToolExecutionKey) != key {
			return ErrJournal
		}
		if record.State == ToolExecutionStarted || record.State == ToolExecutionUnknown {
			return ErrRunUnresolved
		}
	}
	return rows.Err()
}

func (j *SQLToolJournal) Get(ctx context.Context, key ToolExecutionKey) (ToolExecution, bool, error) {
	if !key.valid() {
		return ToolExecution{}, false, ErrInput
	}
	encoded, found, err := j.store.GetRecord(ctx, toolJournalTable, journalKey(key))
	if err != nil || !found {
		return ToolExecution{}, false, journalError(ctx, err)
	}
	record, err := decodeExecution(encoded)
	if err != nil || record.ToolExecutionKey != key {
		return ToolExecution{}, false, ErrJournal
	}
	return record, true, nil
}

// ListRun returns a bounded page within one explicit tenant/run scope. The host
// must authorize that scope before exposing diagnostics to a user or API.
func (j *SQLToolJournal) ListRun(ctx context.Context, tenantID string, runID agentcore.AgentRunID, cursor string, limit int) (ToolExecutionPage, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(string(runID)) == "" || limit < 1 || limit > 100 {
		return ToolExecutionPage{}, ErrInput
	}
	prefix := journalPrefix(tenantID, runID)
	if cursor != "" && (!strings.HasPrefix(cursor, prefix) || !validDigest(strings.TrimPrefix(cursor, prefix))) {
		return ToolExecutionPage{}, ErrInput
	}
	if cursor == "" {
		cursor = prefix
	}
	rows, err := j.store.Query(ctx, `SELECT record_key, payload_json FROM platform_records WHERE table_name = ? AND record_key LIKE ? AND record_key > ? ORDER BY record_key LIMIT ?`, toolJournalTable, prefix+"%", cursor, limit+1)
	if err != nil {
		return ToolExecutionPage{}, journalError(ctx, err)
	}
	defer rows.Close()
	var page ToolExecutionPage
	lastKey := ""
	for rows.Next() {
		var key, payload string
		if rows.Scan(&key, &payload) != nil {
			return ToolExecutionPage{}, ErrJournal
		}
		if len(page.Items) == limit {
			page.NextCursor = lastKey
			break
		}
		var encoded db.Record
		if json.Unmarshal([]byte(payload), &encoded) != nil {
			return ToolExecutionPage{}, ErrJournal
		}
		record, err := decodeExecution(encoded)
		if err != nil || record.TenantID != tenantID || record.RunID != runID || journalKey(record.ToolExecutionKey) != key {
			return ToolExecutionPage{}, ErrJournal
		}
		page.Items, lastKey = append(page.Items, record), key
	}
	if err := rows.Err(); err != nil {
		return ToolExecutionPage{}, journalError(ctx, err)
	}
	return page, nil
}

var _ ToolJournal = (*SQLToolJournal)(nil)
