package discussionsession

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

// RetryFailedCommand atomically reopens a failed idempotent command. The
// bounded counter prevents repeated provider delivery from rerunning a
// permanently failing command forever.
func (r *Repository) RetryFailedCommand(ctx context.Context, command CommandRecord, maxRetries int, retriedAt time.Time) (CommandRecord, bool, error) {
	if r == nil || r.router == nil {
		return CommandRecord{}, false, ErrServiceDependency
	}
	command = normalizeCommandRecord(command)
	if command.CommandID == "" || command.Fingerprint == "" || maxRetries <= 0 || retriedAt.IsZero() {
		return CommandRecord{}, false, ErrInvalidRequest
	}
	store, table, err := r.router.StoreForTable(commandTable)
	if err != nil {
		return CommandRecord{}, false, err
	}
	tx, err := store.BeginTx(ctx)
	if err != nil {
		return CommandRecord{}, false, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := db.LockRecordKeyTx(ctx, tx, table, command.CommandID); err != nil {
		return CommandRecord{}, false, err
	}
	record, ok, err := db.GetRecordTx(ctx, tx, table, command.CommandID)
	if err != nil {
		return CommandRecord{}, false, err
	}
	if !ok {
		return CommandRecord{}, false, ErrNotFound
	}
	var current CommandRecord
	if err := decodePayload(record, &current); err != nil {
		return CommandRecord{}, false, err
	}
	current = normalizeCommandRecord(current)
	if current.Fingerprint != command.Fingerprint || current.Operation != command.Operation || current.ViewerID != command.ViewerID || current.ViewerKind != command.ViewerKind || current.IdempotencyKey != command.IdempotencyKey {
		return CommandRecord{}, false, ErrIdempotencyMismatch
	}
	if current.Status != discussionCommandFailed || current.RetryCount >= maxRetries {
		if err := tx.Commit(); err != nil {
			return CommandRecord{}, false, err
		}
		committed = true
		return current, false, nil
	}
	current.Status = discussionCommandPending
	current.ErrorCode = ""
	current.ErrorMessage = ""
	current.RetryCount++
	current.UpdatedAt = retriedAt
	payload, err := encodePayload(current)
	if err != nil {
		return CommandRecord{}, false, err
	}
	if err := db.PutRecordTx(ctx, tx, table, current.CommandID, db.Record{
		"command_id": current.CommandID, "operation": current.Operation, "viewer_id": current.ViewerID,
		"status": current.Status, "updated_at": formatTime(current.UpdatedAt), conversationPayload: payload,
	}); err != nil {
		return CommandRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return CommandRecord{}, false, err
	}
	committed = true
	return current, true, nil
}

// ReclaimStalePendingCommand renews ownership of a pending command after its
// previous worker stopped updating it. The record lock makes the lease renewal
// exclusive across service instances.
func (r *Repository) ReclaimStalePendingCommand(ctx context.Context, command CommandRecord, staleBefore, reclaimedAt time.Time) (CommandRecord, bool, error) {
	if r == nil || r.router == nil {
		return CommandRecord{}, false, ErrServiceDependency
	}
	command = normalizeCommandRecord(command)
	if command.CommandID == "" || command.Fingerprint == "" || staleBefore.IsZero() || reclaimedAt.IsZero() {
		return CommandRecord{}, false, ErrInvalidRequest
	}
	store, table, err := r.router.StoreForTable(commandTable)
	if err != nil {
		return CommandRecord{}, false, err
	}
	tx, err := store.BeginTx(ctx)
	if err != nil {
		return CommandRecord{}, false, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := db.LockRecordKeyTx(ctx, tx, table, command.CommandID); err != nil {
		return CommandRecord{}, false, err
	}
	record, ok, err := db.GetRecordTx(ctx, tx, table, command.CommandID)
	if err != nil {
		return CommandRecord{}, false, err
	}
	if !ok {
		return CommandRecord{}, false, ErrNotFound
	}
	var current CommandRecord
	if err := decodePayload(record, &current); err != nil {
		return CommandRecord{}, false, err
	}
	current = normalizeCommandRecord(current)
	if current.Fingerprint != command.Fingerprint || current.Operation != command.Operation || current.ViewerID != command.ViewerID || current.ViewerKind != command.ViewerKind || current.IdempotencyKey != command.IdempotencyKey {
		return CommandRecord{}, false, ErrIdempotencyMismatch
	}
	if current.Status != discussionCommandPending {
		if err := tx.Commit(); err != nil {
			return CommandRecord{}, false, err
		}
		committed = true
		return current, false, nil
	}
	lastActivity := current.UpdatedAt
	if lastActivity.IsZero() {
		lastActivity = current.CreatedAt
	}
	if !lastActivity.IsZero() && lastActivity.After(staleBefore) {
		if err := tx.Commit(); err != nil {
			return CommandRecord{}, false, err
		}
		committed = true
		return current, false, nil
	}
	current.UpdatedAt = reclaimedAt
	payload, err := encodePayload(current)
	if err != nil {
		return CommandRecord{}, false, err
	}
	if err := db.PutRecordTx(ctx, tx, table, current.CommandID, db.Record{
		"command_id": current.CommandID, "operation": current.Operation, "viewer_id": current.ViewerID,
		"status": current.Status, "updated_at": formatTime(current.UpdatedAt), conversationPayload: payload,
	}); err != nil {
		return CommandRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return CommandRecord{}, false, err
	}
	committed = true
	return current, true, nil
}

// ClaimCommand atomically owns an idempotency scope. A false claimed result
// returns the previously stored command and never replaces it.
func (r *Repository) ClaimCommand(ctx context.Context, command CommandRecord) (existing CommandRecord, claimed bool, err error) {
	if r == nil || r.router == nil {
		return CommandRecord{}, false, ErrServiceDependency
	}
	command = normalizeCommandRecord(command)
	if command.Operation == "" || command.ViewerID == "" || command.IdempotencyKey == "" || command.Fingerprint == "" {
		return CommandRecord{}, false, ErrInvalidRequest
	}
	command.CommandID = discussionCommandID(command.Operation, command.ViewerKind, command.ViewerID, command.IdempotencyKey)
	store, table, err := r.router.StoreForTable(commandTable)
	if err != nil {
		return CommandRecord{}, false, err
	}
	tx, err := store.BeginTx(ctx)
	if err != nil {
		return CommandRecord{}, false, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	payload, err := encodePayload(command)
	if err != nil {
		return CommandRecord{}, false, err
	}
	inserted, err := db.InsertRecordTx(ctx, tx, table, command.CommandID, db.Record{
		"command_id":        command.CommandID,
		"operation":         command.Operation,
		"viewer_id":         command.ViewerID,
		"status":            command.Status,
		"updated_at":        formatTime(command.UpdatedAt),
		conversationPayload: payload,
	})
	if err != nil {
		return CommandRecord{}, false, err
	}
	if inserted {
		if err := tx.Commit(); err != nil {
			return CommandRecord{}, false, err
		}
		committed = true
		return command, true, nil
	}
	record, ok, err := db.GetRecordTx(ctx, tx, table, command.CommandID)
	if err != nil {
		return CommandRecord{}, false, err
	}
	if !ok {
		return CommandRecord{}, false, ErrNotFound
	}
	if err := decodePayload(record, &existing); err != nil {
		return CommandRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return CommandRecord{}, false, err
	}
	committed = true
	return normalizeCommandRecord(existing), false, nil
}

func (r *Repository) SaveCommand(ctx context.Context, command CommandRecord) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	command = normalizeCommandRecord(command)
	if command.CommandID == "" {
		command.CommandID = discussionCommandID(command.Operation, command.ViewerKind, command.ViewerID, command.IdempotencyKey)
	}
	if command.CommandID == "" || command.Status == "" {
		return ErrInvalidRequest
	}
	payload, err := encodePayload(command)
	if err != nil {
		return err
	}
	return r.router.PutRecord(ctx, commandTable, command.CommandID, db.Record{
		"command_id":        command.CommandID,
		"operation":         command.Operation,
		"viewer_id":         command.ViewerID,
		"status":            command.Status,
		"updated_at":        formatTime(command.UpdatedAt),
		conversationPayload: payload,
	})
}

// SavePendingCommandProgress durably records aggregate identities before a
// claimed command performs notifications or runtime work. It never reopens a
// terminal command and fences concurrent workers by fingerprint.
func (r *Repository) SavePendingCommandProgress(ctx context.Context, command CommandRecord) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	command = normalizeCommandRecord(command)
	if command.CommandID == "" || command.Fingerprint == "" || command.Status != discussionCommandPending || command.ConversationID == "" || command.MessageID == "" || command.TurnID == "" {
		return ErrInvalidRequest
	}
	store, table, err := r.router.StoreForTable(commandTable)
	if err != nil {
		return err
	}
	return store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, table, command.CommandID); err != nil {
			return err
		}
		record, ok, err := db.GetRecordTx(ctx, tx, table, command.CommandID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		var current CommandRecord
		if err := decodePayload(record, &current); err != nil {
			return err
		}
		current = normalizeCommandRecord(current)
		if current.Fingerprint != command.Fingerprint || current.Operation != command.Operation || current.ViewerID != command.ViewerID || current.ViewerKind != command.ViewerKind || current.IdempotencyKey != command.IdempotencyKey {
			return ErrIdempotencyMismatch
		}
		if current.Status != discussionCommandPending {
			return ErrRevisionConflict
		}
		command.CreatedAt = current.CreatedAt
		payload, err := encodePayload(command)
		if err != nil {
			return err
		}
		return db.PutRecordTx(ctx, tx, table, command.CommandID, db.Record{
			"command_id": command.CommandID, "operation": command.Operation, "viewer_id": command.ViewerID,
			"status": command.Status, "updated_at": formatTime(command.UpdatedAt), conversationPayload: payload,
		})
	})
}

// TransitionCommand atomically moves a claimed command out of its expected
// state. A late worker cannot overwrite a terminal outcome produced by another
// process.
func (r *Repository) TransitionCommand(ctx context.Context, command CommandRecord, expectedStatus string) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	command = normalizeCommandRecord(command)
	expectedStatus = strings.TrimSpace(expectedStatus)
	if command.CommandID == "" || command.Status == "" || expectedStatus == "" || command.Fingerprint == "" {
		return ErrInvalidRequest
	}
	store, table, err := r.router.StoreForTable(commandTable)
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
	if err := db.LockRecordKeyTx(ctx, tx, table, command.CommandID); err != nil {
		return err
	}
	record, ok, err := db.GetRecordTx(ctx, tx, table, command.CommandID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	var current CommandRecord
	if err := decodePayload(record, &current); err != nil {
		return err
	}
	current = normalizeCommandRecord(current)
	if current.Fingerprint != command.Fingerprint {
		return ErrIdempotencyMismatch
	}
	if current.Status == command.Status {
		if err := tx.Commit(); err != nil {
			return err
		}
		committed = true
		return nil
	}
	if current.Status != expectedStatus {
		return ErrRevisionConflict
	}
	payload, err := encodePayload(command)
	if err != nil {
		return err
	}
	if err := db.PutRecordTx(ctx, tx, table, command.CommandID, db.Record{
		"command_id":        command.CommandID,
		"operation":         command.Operation,
		"viewer_id":         command.ViewerID,
		"status":            command.Status,
		"updated_at":        formatTime(command.UpdatedAt),
		conversationPayload: payload,
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (r *Repository) GetCommand(ctx context.Context, operation string, viewer ViewerRef, idempotencyKey string) (CommandRecord, error) {
	if r == nil || r.router == nil {
		return CommandRecord{}, ErrServiceDependency
	}
	id := discussionCommandID(operation, string(viewer.ViewerKind), viewer.ViewerID, idempotencyKey)
	record, ok, err := r.router.GetRecord(ctx, commandTable, id)
	if err != nil {
		return CommandRecord{}, err
	}
	if !ok {
		return CommandRecord{}, ErrNotFound
	}
	var command CommandRecord
	if err := decodePayload(record, &command); err != nil {
		return CommandRecord{}, err
	}
	return normalizeCommandRecord(command), nil
}

func normalizeCommandRecord(command CommandRecord) CommandRecord {
	command.CommandID = strings.TrimSpace(command.CommandID)
	command.Operation = strings.TrimSpace(command.Operation)
	command.ViewerID = strings.TrimSpace(command.ViewerID)
	command.ViewerKind = strings.TrimSpace(command.ViewerKind)
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	command.Fingerprint = strings.TrimSpace(command.Fingerprint)
	command.ConversationID = strings.TrimSpace(command.ConversationID)
	command.MessageID = strings.TrimSpace(command.MessageID)
	command.TurnID = strings.TrimSpace(command.TurnID)
	command.Status = strings.TrimSpace(command.Status)
	command.ErrorCode = strings.TrimSpace(command.ErrorCode)
	command.ErrorMessage = strings.TrimSpace(command.ErrorMessage)
	return command
}

func discussionCommandID(operation, viewerKind, viewerID, idempotencyKey string) string {
	operation = strings.TrimSpace(operation)
	viewerKind = strings.TrimSpace(viewerKind)
	viewerID = strings.TrimSpace(viewerID)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if operation == "" || viewerID == "" || idempotencyKey == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(operation + "\x00" + viewerKind + "\x00" + viewerID + "\x00" + idempotencyKey))
	return fmt.Sprintf("command-%x", sum[:])
}
