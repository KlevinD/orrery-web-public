package todo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"orrery_v2/internal/platform/db"
)

// This logical table shares the Todo database and transaction. Pending origins
// survive task edits/deletion and contain no mutable task state.
const todoCreationOutboxTable = "todo_creation_outbox"
const creationRetryLease = time.Minute
const creationAttemptFormat = "2006-01-02T15:04:05.000000000Z"

type CreationEvent struct {
	TenantID           string
	TodoID             string
	CreatedBy          string
	CreatedByKind      ViewerKind
	CreatedBySubjectID string
	CreatedAt          time.Time
	Source             string
}

type CreationPublisher func(context.Context, CreationEvent) error

func enqueueTodoCreationTx(ctx context.Context, tx db.Tx, item TodoItem, source string) error {
	event := CreationEvent{TenantID: item.TenantID, TodoID: item.TodoID,
		CreatedBy: item.CreatedBy, CreatedByKind: item.CreatedByKind,
		CreatedBySubjectID: item.CreatedBySubjectID, CreatedAt: item.CreatedAt, Source: source}
	payload, err := encodePayload(event)
	if err != nil {
		return err
	}
	inserted, err := db.InsertRecordTx(ctx, tx, todoCreationOutboxTable,
		scopedRecordKey(item.TenantID, item.TodoID), db.Record{payloadField: payload, "next_attempt_at": ""})
	if err != nil {
		return err
	}
	if !inserted {
		return ErrConflict
	}
	return nil
}

// DeliverCreationEvent acknowledges only a successful publication. A crash or
// failed publisher leaves a durable retry after a bounded lease.
func (s *Service) DeliverCreationEvent(ctx context.Context, tenantID, todoID string, publish CreationPublisher) error {
	if s == nil || s.repo == nil || s.repo.router == nil || publish == nil {
		return ErrServiceDependency
	}
	store, _, err := s.repo.router.StoreForTable(todoTable)
	if err != nil {
		return err
	}
	key := scopedRecordKey(tenantID, todoID)
	record, found, err := store.GetRecord(ctx, todoCreationOutboxTable, key)
	if err != nil || !found {
		return err
	}
	return deliverCreationRecord(ctx, store, key, record, s.clock.Now(), publish)
}

func deliverCreationRecord(ctx context.Context, store *db.Store, key string, record db.Record, now time.Time, publish CreationPublisher) error {
	next := recordString(record, "next_attempt_at")
	if next != "" {
		at, err := time.Parse(creationAttemptFormat, next)
		if err != nil {
			return fmt.Errorf("decode Todo creation retry time: %w", err)
		}
		if at.After(now) {
			return nil
		}
	}
	claimed := db.Record{payloadField: record[payloadField], "next_attempt_at": now.Add(creationRetryLease).UTC().Format(creationAttemptFormat)}
	ok, err := store.CompareAndSwapRecord(ctx, todoCreationOutboxTable, key, record, claimed)
	if err != nil || !ok {
		return err
	}
	var event CreationEvent
	if err := decodePayload(claimed, &event); err != nil {
		return err
	}
	if scopedRecordKey(event.TenantID, event.TodoID) != key {
		return ErrInvalidRequest
	}
	if err := publish(ctx, event); err != nil {
		return err
	}
	_, err = store.CompareAndDeleteRecord(ctx, todoCreationOutboxTable, key, claimed)
	return err
}

// ReconcileCreationEvents uses the platform sweeper, not a user scheduled task.
// Due rows are bounded and failed rows yield to other tasks for one minute.
func (s *Service) ReconcileCreationEvents(ctx context.Context, limit int, publish CreationPublisher) error {
	if s == nil || s.repo == nil || s.repo.router == nil || publish == nil {
		return ErrServiceDependency
	}
	if limit < 1 || limit > 100 {
		return ErrInvalidRequest
	}
	store, _, err := s.repo.router.StoreForTable(todoTable)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	expr := recordTextExpr(store.Dialect(), "next_attempt_at")
	rows, err := store.Query(ctx, "SELECT record_key, payload_json FROM platform_records WHERE table_name = ? AND "+expr+" <= ? ORDER BY "+expr+", record_key LIMIT ?", todoCreationOutboxTable, now.UTC().Format(creationAttemptFormat), limit)
	if err != nil {
		return err
	}
	type pending struct {
		key    string
		record db.Record
	}
	var batch []pending
	for rows.Next() {
		var key, raw string
		if err = rows.Scan(&key, &raw); err != nil {
			break
		}
		var record db.Record
		if err = json.Unmarshal([]byte(raw), &record); err != nil {
			break
		}
		batch = append(batch, pending{key, record})
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	var failures []error
	for _, item := range batch {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		if err := deliverCreationRecord(ctx, store, item.key, item.record, now, publish); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
