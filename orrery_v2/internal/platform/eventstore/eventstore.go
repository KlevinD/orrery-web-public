package eventstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/db"
)

var ErrConflict = errors.New("event stream conflict")

type Envelope struct {
	EventID        string
	StreamID       string
	StreamVersion  int
	EventType      string
	EventVersion   int
	Payload        []byte
	Metadata       []byte
	IdempotencyKey string
	CorrelationID  string
	CausationID    string
	CreatedAt      time.Time
}

type Store struct {
	db    *db.Store
	mu    sync.Mutex
	clock clock.Clock
}

type Tx interface {
	db.Tx
	Put(table, key string, record db.Record)
	Get(table, key string) (db.Record, bool)
	Snapshot() map[string]map[string]db.Record
}

type TxStore struct {
	tx    Tx
	clock clock.Clock
}

type StoreOptions struct {
	Clock clock.Clock
}

type ListQuery struct {
	EventTypePrefix string
}

func New(store *db.Store) *Store {
	return NewWithOptions(store, StoreOptions{})
}

func NewWithOptions(store *db.Store, opts StoreOptions) *Store {
	return &Store{db: store, clock: storeClock(opts.Clock)}
}

func Open(ctx context.Context, store *db.Store) (*Store, error) {
	s := New(store)
	if err := s.init(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func NewTx(tx Tx) *TxStore {
	return NewTxWithOptions(tx, StoreOptions{})
}

func NewTxWithOptions(tx Tx, opts StoreOptions) *TxStore {
	return &TxStore{tx: tx, clock: storeClock(opts.Clock)}
}

func storeClock(c clock.Clock) clock.Clock {
	if c != nil {
		return c
	}
	return clock.Real{}
}

func (s *Store) Append(ctx context.Context, event Envelope) error {
	if err := s.db.Ping(ctx); err != nil {
		return err
	}
	if event.EventID == "" || event.StreamID == "" || event.StreamVersion <= 0 {
		return fmt.Errorf("event_id, stream_id and positive stream_version are required")
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = s.clock.Now().UTC()
	}
	if err := s.init(ctx); err != nil {
		return err
	}
	return s.db.WithTx(ctx, func(tx db.Tx) error {
		if event.IdempotencyKey != "" {
			var existing string
			err := tx.QueryRowContext(ctx, `SELECT event_id FROM platform_event_idempotency WHERE idempotency_key = ?`, event.IdempotencyKey).Scan(&existing)
			if err == nil {
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("check event idempotency: %w", err)
			}
		}
		_, err := tx.ExecContext(ctx, `
INSERT INTO platform_events (
	event_id, stream_id, stream_version, event_type, event_version,
	payload_json, metadata_json, idempotency_key, correlation_id, causation_id, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			event.EventID, event.StreamID, event.StreamVersion, event.EventType, event.EventVersion,
			string(event.Payload), string(event.Metadata), event.IdempotencyKey,
			event.CorrelationID, event.CausationID, event.CreatedAt.UTC().Format(time.RFC3339Nano),
		)
		if err != nil {
			if strings.Contains(err.Error(), "stream_id") || strings.Contains(err.Error(), "stream_version") {
				return ErrConflict
			}
			return fmt.Errorf("insert event: %w", err)
		}
		if event.IdempotencyKey != "" {
			if _, err := tx.ExecContext(ctx, `INSERT INTO platform_event_idempotency (idempotency_key, event_id) VALUES (?, ?)`, event.IdempotencyKey, event.EventID); err != nil {
				return fmt.Errorf("insert event idempotency: %w", err)
			}
		}
		return nil
	})
}

func (s *Store) Load(ctx context.Context, streamID string) ([]Envelope, error) {
	if err := s.db.Ping(ctx); err != nil {
		return nil, err
	}
	if err := s.init(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
SELECT event_id, stream_id, stream_version, event_type, event_version,
       payload_json, metadata_json, COALESCE(idempotency_key, ''),
       COALESCE(correlation_id, ''), COALESCE(causation_id, ''), created_at
FROM platform_events
WHERE stream_id = ?
ORDER BY stream_version ASC`, streamID)
	if err != nil {
		return nil, fmt.Errorf("load events: %w", err)
	}
	defer rows.Close()
	var events []Envelope
	for rows.Next() {
		var event Envelope
		var payload, metadata, createdAt string
		if err := rows.Scan(
			&event.EventID, &event.StreamID, &event.StreamVersion, &event.EventType, &event.EventVersion,
			&payload, &metadata, &event.IdempotencyKey, &event.CorrelationID, &event.CausationID, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		event.Payload = []byte(payload)
		event.Metadata = []byte(metadata)
		event.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}
	return events, nil
}

func (s *Store) GetByID(ctx context.Context, eventID string) (Envelope, error) {
	if err := s.db.Ping(ctx); err != nil {
		return Envelope{}, err
	}
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return Envelope{}, fmt.Errorf("event id is required")
	}
	if err := s.init(ctx); err != nil {
		return Envelope{}, err
	}
	var event Envelope
	var payload, metadata, createdAt string
	err := s.db.QueryRow(ctx, `
SELECT event_id, stream_id, stream_version, event_type, event_version,
       payload_json, metadata_json, COALESCE(idempotency_key, ''),
       COALESCE(correlation_id, ''), COALESCE(causation_id, ''), created_at
FROM platform_events
WHERE event_id = ?`, eventID).Scan(
		&event.EventID, &event.StreamID, &event.StreamVersion, &event.EventType, &event.EventVersion,
		&payload, &metadata, &event.IdempotencyKey, &event.CorrelationID, &event.CausationID, &createdAt,
	)
	if err != nil {
		return Envelope{}, err
	}
	event.Payload = []byte(payload)
	event.Metadata = []byte(metadata)
	event.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	return event, nil
}

func (s *Store) ListIDsByEventType(ctx context.Context, eventType string) (map[string]bool, error) {
	if err := s.db.Ping(ctx); err != nil {
		return nil, err
	}
	eventType = strings.TrimSpace(eventType)
	if eventType == "" {
		return nil, fmt.Errorf("event type is required")
	}
	if err := s.init(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `SELECT event_id FROM platform_events WHERE event_type = ?`, eventType)
	if err != nil {
		return nil, fmt.Errorf("list event ids by type: %w", err)
	}
	defer rows.Close()
	existing := make(map[string]bool)
	for rows.Next() {
		var eventID string
		if err := rows.Scan(&eventID); err != nil {
			return nil, fmt.Errorf("scan event id by type: %w", err)
		}
		existing[eventID] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate event ids by type: %w", err)
	}
	return existing, nil
}

func (s *Store) List(ctx context.Context, query ListQuery) ([]Envelope, error) {
	if err := s.db.Ping(ctx); err != nil {
		return nil, err
	}
	if err := s.init(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
SELECT event_id, stream_id, stream_version, event_type, event_version,
       payload_json, metadata_json, COALESCE(idempotency_key, ''),
       COALESCE(correlation_id, ''), COALESCE(causation_id, ''), created_at
FROM platform_events
WHERE (? = '' OR event_type LIKE ?)
ORDER BY created_at ASC, stream_id ASC, stream_version ASC, event_id ASC`, query.EventTypePrefix, query.EventTypePrefix+"%")
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()
	var events []Envelope
	for rows.Next() {
		var event Envelope
		var payload, metadata, createdAt string
		if err := rows.Scan(
			&event.EventID, &event.StreamID, &event.StreamVersion, &event.EventType, &event.EventVersion,
			&payload, &metadata, &event.IdempotencyKey, &event.CorrelationID, &event.CausationID, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		event.Payload = []byte(payload)
		event.Metadata = []byte(metadata)
		event.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}
	return events, nil
}

func (s *Store) init(ctx context.Context) error {
	if _, err := s.db.Exec(ctx, `
CREATE TABLE IF NOT EXISTS platform_events (
	event_id TEXT PRIMARY KEY,
	stream_id TEXT NOT NULL,
	stream_version INTEGER NOT NULL,
	event_type TEXT NOT NULL,
	event_version INTEGER NOT NULL,
	payload_json TEXT NOT NULL,
	metadata_json TEXT NOT NULL,
	idempotency_key TEXT,
	correlation_id TEXT,
	causation_id TEXT,
	created_at TEXT NOT NULL,
	UNIQUE(stream_id, stream_version)
)`); err != nil {
		return fmt.Errorf("create platform_events: %w", err)
	}
	if _, err := s.db.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_platform_events_stream ON platform_events (stream_id, stream_version)`); err != nil {
		return fmt.Errorf("create platform_events stream index: %w", err)
	}
	if _, err := s.db.Exec(ctx, `
CREATE TABLE IF NOT EXISTS platform_event_idempotency (
	idempotency_key TEXT PRIMARY KEY,
	event_id TEXT NOT NULL
)`); err != nil {
		return fmt.Errorf("create platform_event_idempotency: %w", err)
	}
	return nil
}

func initTx(ctx context.Context, tx db.Tx) error {
	if _, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS platform_events (
	event_id TEXT PRIMARY KEY,
	stream_id TEXT NOT NULL,
	stream_version INTEGER NOT NULL,
	event_type TEXT NOT NULL,
	event_version INTEGER NOT NULL,
	payload_json TEXT NOT NULL,
	metadata_json TEXT NOT NULL,
	idempotency_key TEXT,
	correlation_id TEXT,
	causation_id TEXT,
	created_at TEXT NOT NULL,
	UNIQUE(stream_id, stream_version)
)`); err != nil {
		return fmt.Errorf("create platform_events: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_platform_events_stream ON platform_events (stream_id, stream_version)`); err != nil {
		return fmt.Errorf("create platform_events stream index: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS platform_event_idempotency (
	idempotency_key TEXT PRIMARY KEY,
	event_id TEXT NOT NULL
)`); err != nil {
		return fmt.Errorf("create platform_event_idempotency: %w", err)
	}
	return nil
}

func (s *TxStore) Append(ctx context.Context, event Envelope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if event.EventID == "" || event.StreamID == "" || event.StreamVersion <= 0 {
		return fmt.Errorf("event_id, stream_id and positive stream_version are required")
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = s.clock.Now().UTC()
	}
	if err := initTx(ctx, s.tx); err != nil {
		return err
	}
	if event.IdempotencyKey != "" {
		var existing string
		err := s.tx.QueryRowContext(ctx, `SELECT event_id FROM platform_event_idempotency WHERE idempotency_key = ?`, event.IdempotencyKey).Scan(&existing)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("check event idempotency: %w", err)
		}
	}
	_, err := s.tx.ExecContext(ctx, `
INSERT INTO platform_events (
	event_id, stream_id, stream_version, event_type, event_version,
	payload_json, metadata_json, idempotency_key, correlation_id, causation_id, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.EventID, event.StreamID, event.StreamVersion, event.EventType, event.EventVersion,
		string(event.Payload), string(event.Metadata), event.IdempotencyKey,
		event.CorrelationID, event.CausationID, event.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		if strings.Contains(err.Error(), "stream_id") || strings.Contains(err.Error(), "stream_version") {
			return ErrConflict
		}
		return fmt.Errorf("insert event: %w", err)
	}
	if event.IdempotencyKey != "" {
		if _, err := s.tx.ExecContext(ctx, `INSERT INTO platform_event_idempotency (idempotency_key, event_id) VALUES (?, ?)`, event.IdempotencyKey, event.EventID); err != nil {
			return fmt.Errorf("insert event idempotency: %w", err)
		}
	}
	return nil
}

func (s *TxStore) Load(ctx context.Context, streamID string) ([]Envelope, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := initTx(ctx, s.tx); err != nil {
		return nil, err
	}
	rows, err := s.tx.QueryContext(ctx, `
SELECT event_id, stream_id, stream_version, event_type, event_version,
       payload_json, metadata_json, COALESCE(idempotency_key, ''),
       COALESCE(correlation_id, ''), COALESCE(causation_id, ''), created_at
FROM platform_events
WHERE stream_id = ?
ORDER BY stream_version ASC`, streamID)
	if err != nil {
		return nil, fmt.Errorf("load tx events: %w", err)
	}
	defer rows.Close()

	var events []Envelope
	for rows.Next() {
		var event Envelope
		var payload, metadata, createdAt string
		if err := rows.Scan(
			&event.EventID, &event.StreamID, &event.StreamVersion, &event.EventType, &event.EventVersion,
			&payload, &metadata, &event.IdempotencyKey, &event.CorrelationID, &event.CausationID, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan tx event: %w", err)
		}
		event.Payload = []byte(payload)
		event.Metadata = []byte(metadata)
		event.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tx events: %w", err)
	}
	return events, nil
}
