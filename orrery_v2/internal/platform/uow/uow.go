package uow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/eventstore"
	"orrery_v2/internal/platform/idgen"
	"orrery_v2/internal/platform/logging"
	"orrery_v2/internal/platform/outbox"
)

type Tx interface {
	db.Tx
	Put(table, key string, record db.Record)
	Get(table, key string) (db.Record, bool)
	Snapshot() map[string]map[string]db.Record
	EventStore() *eventstore.TxStore
	RecordEvent(context.Context, EventMessage) (string, error)
	Outbox() *outbox.TxStore
	PublishOutbox(context.Context, OutboxMessage) (string, error)
	AfterCommit(fn func(context.Context) error)
}

type ExplicitTx interface {
	Tx
	Commit(context.Context) error
	Rollback(context.Context) error
}

type EventMessage struct {
	StreamID       string
	StreamVersion  int
	EventType      string
	EventVersion   int
	Payload        []byte
	Metadata       []byte
	IdempotencyKey string
	CorrelationID  string
	CausationID    string
}

type OutboxMessage struct {
	Topic          string
	Payload        []byte
	IdempotencyKey string
	Delay          time.Duration
	MaxAttempts    int
}

type Factory[T any] func(Tx) T

type UnitOfWork struct {
	store       *db.Store
	logger      logging.Logger
	clock       clock.Clock
	idGenerator idgen.ErrorGenerator
}

type memoryTx struct {
	err              error
	sqlTx            db.Tx
	ctx              context.Context
	clock            clock.Clock
	idGenerator      idgen.ErrorGenerator
	afterCommitHooks []func(context.Context) error
}

type explicitTx struct {
	*memoryTx
	u     *UnitOfWork
	sqlTx interface {
		db.Tx
		Commit() error
		Rollback() error
	}
	done bool
}

func New(store *db.Store) *UnitOfWork {
	return &UnitOfWork{store: store}
}

func (u *UnitOfWork) WithLogger(logger logging.Logger) *UnitOfWork {
	u.logger = logger
	return u
}

func (u *UnitOfWork) WithClock(clock clock.Clock) *UnitOfWork {
	u.clock = clock
	return u
}

func (u *UnitOfWork) WithIDGenerator(generator idgen.ErrorGenerator) *UnitOfWork {
	u.idGenerator = generator
	return u
}

func (u *UnitOfWork) Do(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error {
	if err := u.store.Ping(ctx); err != nil {
		return err
	}
	var tx *memoryTx
	var callbackErr error
	err := u.store.WithTx(ctx, func(sqlTx db.Tx) error {
		tx = &memoryTx{sqlTx: sqlTx, ctx: ctx, clock: u.clock, idGenerator: u.idGenerator}
		callbackErr = fn(ctx, tx)
		if tx.err != nil {
			callbackErr = tx.err
		}
		if callbackErr != nil {
			return callbackErr
		}
		return nil
	})
	if callbackErr != nil {
		u.logError(ctx, "transaction_rollback", callbackErr)
		return callbackErr
	}
	if err != nil {
		u.logError(ctx, "transaction_commit_failed", err)
		return fmt.Errorf("commit transaction: %w", err)
	}
	if tx != nil {
		for _, hook := range tx.afterCommitHooks {
			if hook == nil {
				continue
			}
			if err := hook(ctx); err != nil {
				u.logError(ctx, "after_commit_failed", err)
				return fmt.Errorf("after commit hook: %w", err)
			}
		}
	}
	return nil
}

func (u *UnitOfWork) Begin(ctx context.Context) (ExplicitTx, error) {
	if u == nil || u.store == nil {
		return nil, fmt.Errorf("unit of work store is not configured")
	}
	if err := u.store.Ping(ctx); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sqlTx, err := u.store.BeginTx(ctx)
	if err != nil {
		return nil, err
	}
	return &explicitTx{
		memoryTx: &memoryTx{sqlTx: sqlTx, ctx: ctx, clock: u.clock, idGenerator: u.idGenerator},
		u:        u,
		sqlTx:    sqlTx,
	}, nil
}

func (u *UnitOfWork) logError(ctx context.Context, code string, err error) {
	if u.logger == nil {
		return
	}
	u.logger.Error(ctx, "unit of work failed",
		logging.String("operation", "uow.do"),
		logging.String("error_code", code),
		logging.String("error", err.Error()),
	)
}

func (tx *explicitTx) Commit(ctx context.Context) error {
	if tx == nil || tx.sqlTx == nil {
		return fmt.Errorf("unit of work transaction is not configured")
	}
	if tx.done {
		return fmt.Errorf("unit of work transaction is already closed")
	}
	tx.done = true
	if err := ctx.Err(); err != nil {
		_ = tx.sqlTx.Rollback()
		tx.u.logError(ctx, "transaction_commit_failed", err)
		return fmt.Errorf("commit transaction: %w", err)
	}
	if tx.err != nil {
		_ = tx.sqlTx.Rollback()
		return tx.err
	}
	if err := tx.sqlTx.Commit(); err != nil {
		tx.u.logError(ctx, "transaction_commit_failed", err)
		return fmt.Errorf("commit transaction: %w", err)
	}
	for _, hook := range tx.afterCommitHooks {
		if hook == nil {
			continue
		}
		if err := hook(ctx); err != nil {
			tx.u.logError(ctx, "after_commit_failed", err)
			return fmt.Errorf("after commit hook: %w", err)
		}
	}
	return nil
}

func (tx *explicitTx) Rollback(ctx context.Context) error {
	if tx == nil || tx.sqlTx == nil {
		return fmt.Errorf("unit of work transaction is not configured")
	}
	if tx.done {
		return fmt.Errorf("unit of work transaction is already closed")
	}
	tx.done = true
	if err := tx.sqlTx.Rollback(); err != nil {
		tx.u.logError(ctx, "transaction_rollback_failed", err)
		return fmt.Errorf("rollback transaction: %w", err)
	}
	return nil
}

func (tx *memoryTx) Put(table, key string, record db.Record) {
	if tx.err == nil {
		tx.err = putSQLRecord(tx.ctx, tx.sqlTx, table, key, record)
	}
}

func (tx *memoryTx) Get(table, key string) (db.Record, bool) {
	var payload string
	err := tx.sqlTx.QueryRowContext(tx.ctx, `SELECT payload_json FROM platform_records WHERE table_name = ? AND record_key = ?`, table, key).Scan(&payload)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) && tx.err == nil {
			tx.err = fmt.Errorf("read record %s/%s: %w", table, key, err)
		}
		return nil, false
	}
	var record db.Record
	if err := json.Unmarshal([]byte(payload), &record); err != nil {
		if tx.err == nil {
			tx.err = fmt.Errorf("decode record %s/%s: %w", table, key, err)
		}
		return nil, false
	}
	return cloneRecord(record), true
}

func (tx *memoryTx) Snapshot() map[string]map[string]db.Record {
	rows, err := tx.sqlTx.QueryContext(tx.ctx, `SELECT table_name, record_key, payload_json FROM platform_records ORDER BY table_name, record_key`)
	if err != nil {
		if tx.err == nil {
			tx.err = fmt.Errorf("read record snapshot: %w", err)
		}
		return map[string]map[string]db.Record{}
	}
	defer rows.Close()
	tables := map[string]map[string]db.Record{}
	for rows.Next() {
		var table, key, payload string
		if err := rows.Scan(&table, &key, &payload); err != nil {
			if tx.err == nil {
				tx.err = fmt.Errorf("scan record snapshot: %w", err)
			}
			return map[string]map[string]db.Record{}
		}
		var record db.Record
		if err := json.Unmarshal([]byte(payload), &record); err != nil {
			if tx.err == nil {
				tx.err = fmt.Errorf("decode snapshot record %s/%s: %w", table, key, err)
			}
			return map[string]map[string]db.Record{}
		}
		if tables[table] == nil {
			tables[table] = map[string]db.Record{}
		}
		tables[table][key] = cloneRecord(record)
	}
	if err := rows.Err(); err != nil && tx.err == nil {
		tx.err = fmt.Errorf("iterate record snapshot: %w", err)
	}
	return tables
}

// ListRecords reads one logical table with bounded keyset pagination inside tx.
func ListRecords(ctx context.Context, tx Tx, table string) (map[string]db.Record, error) {
	if tx == nil {
		return nil, fmt.Errorf("unit of work transaction is not configured")
	}
	records := make(map[string]db.Record)
	afterKey := ""
	for {
		page, err := db.ListRecordsTx(ctx, tx, table, afterKey, db.MaxRecordPageLimit)
		if err != nil {
			return nil, fmt.Errorf("list unit of work records for %s: %w", table, err)
		}
		for _, item := range page.Items {
			records[item.Key] = item.Record
			afterKey = item.Key
		}
		if !page.HasMore {
			return records, nil
		}
		if len(page.Items) == 0 {
			return nil, fmt.Errorf("list unit of work records for %s made no progress", table)
		}
	}
}

func (tx *memoryTx) EventStore() *eventstore.TxStore {
	return eventstore.NewTxWithOptions(tx, eventstore.StoreOptions{Clock: tx.clock})
}

func (tx *memoryTx) RecordEvent(ctx context.Context, message EventMessage) (string, error) {
	if tx == nil {
		return "", fmt.Errorf("unit of work transaction is not configured")
	}
	event, err := tx.buildEventEnvelope(message)
	if err != nil {
		return "", err
	}
	if err := tx.EventStore().Append(ctx, event); err != nil {
		return "", err
	}
	return event.EventID, nil
}

func (tx *memoryTx) Outbox() *outbox.TxStore {
	return outbox.NewTxWithOptions(tx, outbox.StoreOptions{Clock: tx.clock})
}

func (tx *memoryTx) PublishOutbox(ctx context.Context, message OutboxMessage) (string, error) {
	if tx == nil {
		return "", fmt.Errorf("unit of work transaction is not configured")
	}
	topic := strings.TrimSpace(message.Topic)
	if topic == "" {
		return "", fmt.Errorf("outbox topic is required")
	}
	id, err := tx.newOutboxID()
	if err != nil {
		return "", err
	}
	nextAttemptAt := tx.now()
	if message.Delay > 0 {
		nextAttemptAt = nextAttemptAt.Add(message.Delay)
	}
	if err := tx.Outbox().Enqueue(ctx, outbox.Job{
		ID:             id,
		Topic:          topic,
		Payload:        append([]byte(nil), message.Payload...),
		IdempotencyKey: message.IdempotencyKey,
		MaxAttempts:    message.MaxAttempts,
		NextAttemptAt:  nextAttemptAt,
	}); err != nil {
		return "", err
	}
	return id, nil
}

func (tx *memoryTx) buildEventEnvelope(message EventMessage) (eventstore.Envelope, error) {
	if strings.TrimSpace(message.StreamID) == "" {
		return eventstore.Envelope{}, fmt.Errorf("event stream id is required")
	}
	if strings.TrimSpace(message.EventType) == "" {
		return eventstore.Envelope{}, fmt.Errorf("event type is required")
	}
	if message.StreamVersion <= 0 {
		return eventstore.Envelope{}, fmt.Errorf("event stream version must be positive")
	}
	if message.EventVersion <= 0 {
		return eventstore.Envelope{}, fmt.Errorf("event version must be positive")
	}
	id, err := tx.newID("event")
	if err != nil {
		return eventstore.Envelope{}, err
	}
	return eventstore.Envelope{
		EventID:        id,
		StreamID:       strings.TrimSpace(message.StreamID),
		StreamVersion:  message.StreamVersion,
		EventType:      strings.TrimSpace(message.EventType),
		EventVersion:   message.EventVersion,
		Payload:        append([]byte(nil), message.Payload...),
		Metadata:       append([]byte(nil), message.Metadata...),
		IdempotencyKey: message.IdempotencyKey,
		CorrelationID:  message.CorrelationID,
		CausationID:    message.CausationID,
	}, nil
}

func (tx *memoryTx) newOutboxID() (string, error) {
	return tx.newID("outbox job")
}

func (tx *memoryTx) newID(kind string) (string, error) {
	if tx == nil || tx.idGenerator == nil {
		return "", fmt.Errorf("platform id generator is not configured")
	}
	id, err := tx.idGenerator.NewIDContext()
	if err != nil {
		return "", fmt.Errorf("generate %s id: %w", kind, err)
	}
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("generated %s id is empty", kind)
	}
	return id, nil
}

func (tx *memoryTx) now() time.Time {
	if tx == nil || tx.clock == nil {
		return time.Now().UTC()
	}
	return tx.clock.Now().UTC()
}

func (tx *memoryTx) AfterCommit(fn func(context.Context) error) {
	if fn == nil {
		return
	}
	tx.afterCommitHooks = append(tx.afterCommitHooks, fn)
}

func (tx *memoryTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return tx.sqlTx.ExecContext(ctx, query, args...)
}

func (tx *memoryTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return tx.sqlTx.QueryContext(ctx, query, args...)
}

func (tx *memoryTx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return tx.sqlTx.QueryRowContext(ctx, query, args...)
}

func (tx *memoryTx) Dialect() db.Dialect {
	if tx == nil || tx.sqlTx == nil {
		return db.DialectSQLite
	}
	if dialecter, ok := tx.sqlTx.(interface{ Dialect() db.Dialect }); ok {
		return dialecter.Dialect()
	}
	return db.DialectSQLite
}

func putSQLRecord(ctx context.Context, tx db.Tx, table, key string, record db.Record) error {
	table = db.SanitizeText(table)
	key = db.SanitizeText(key)
	record = db.SanitizeRecordText(record)
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO platform_records (table_name, record_key, payload_json)
VALUES (?, ?, ?)
ON CONFLICT(table_name, record_key) DO UPDATE SET payload_json=excluded.payload_json
WHERE platform_records.payload_json <> excluded.payload_json`, table, key, string(payload))
	return err
}

func cloneTables(src map[string]map[string]db.Record) map[string]map[string]db.Record {
	dst := make(map[string]map[string]db.Record, len(src))
	for table, records := range src {
		dst[table] = make(map[string]db.Record, len(records))
		for key, record := range records {
			dst[table][key] = cloneRecord(record)
		}
	}
	return dst
}

func cloneRecord(src db.Record) db.Record {
	dst := make(db.Record, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
