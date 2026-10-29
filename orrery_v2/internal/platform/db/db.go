package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

type Driver string

const DriverMemory Driver = "memory"
const DriverFile Driver = "file"
const DriverPostgres Driver = "postgres"

type Dialect string

const DialectSQLite Dialect = "sqlite"
const DialectPostgres Dialect = "postgres"

var ErrPersist = errors.New("persist database")

func (d Driver) String() string {
	return string(d)
}

type Config struct {
	Driver Driver
	Name   string
	URL    string
}

type Store struct {
	mu         sync.RWMutex
	txGateOnce sync.Once
	txGate     chan struct{}
	driver     Driver
	dialect    Dialect
	name       string
	path       string
	sqlDB      *sql.DB
	closed     bool
	replaceErr error
}

type explicitTx struct {
	Tx
	store *Store
	done  bool
}

type preparedTx struct {
	tx      *sql.Tx
	dialect Dialect
}

type Record map[string]any

const MaxRecordPageLimit = 1000

const maxRecordLookupBatch = 500

type KeyedRecord struct {
	Key    string
	Record Record
}

type RecordPage struct {
	Items   []KeyedRecord
	HasMore bool
}

type Tx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (tx *preparedTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return tx.tx.ExecContext(ctx, prepareSQLForDialect(tx.dialect, query), args...)
}

func (tx *preparedTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return tx.tx.QueryContext(ctx, prepareSQLForDialect(tx.dialect, query), args...)
}

func (tx *preparedTx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return tx.tx.QueryRowContext(ctx, prepareSQLForDialect(tx.dialect, query), args...)
}

func (tx *preparedTx) Dialect() Dialect {
	if tx == nil || tx.dialect == "" {
		return DialectSQLite
	}
	return tx.dialect
}

func (tx *preparedTx) Commit() error {
	return tx.tx.Commit()
}

func (tx *preparedTx) Rollback() error {
	return tx.tx.Rollback()
}

func (tx *explicitTx) Dialect() Dialect {
	if tx == nil || tx.Tx == nil {
		return DialectSQLite
	}
	if dialecter, ok := tx.Tx.(interface{ Dialect() Dialect }); ok {
		return dialecter.Dialect()
	}
	return DialectSQLite
}

func (tx *explicitTx) Commit() error {
	if tx == nil || tx.store == nil {
		return fmt.Errorf("database transaction is not configured")
	}
	if tx.done {
		return fmt.Errorf("database transaction is already closed")
	}
	defer tx.store.unlockTransaction()
	tx.done = true
	if tx.store.replaceErr != nil {
		_ = tx.Tx.(interface{ Rollback() error }).Rollback()
		return fmt.Errorf("%w: %v", ErrPersist, tx.store.replaceErr)
	}
	if err := tx.Tx.(interface{ Commit() error }).Commit(); err != nil {
		return fmt.Errorf("commit %s transaction: %w", tx.store.Dialect(), err)
	}
	return nil
}

func (tx *explicitTx) Rollback() error {
	if tx == nil || tx.store == nil {
		return fmt.Errorf("database transaction is not configured")
	}
	if tx.done {
		return fmt.Errorf("database transaction is already closed")
	}
	defer tx.store.unlockTransaction()
	tx.done = true
	if err := tx.Tx.(interface{ Rollback() error }).Rollback(); err != nil {
		return fmt.Errorf("rollback %s transaction: %w", tx.store.Dialect(), err)
	}
	return nil
}

type ExplicitTx interface {
	Tx
	Commit() error
	Rollback() error
}

// PutRecordTx writes a platform record through an existing transaction. Keeping
// record encoding here lets application repositories compose atomic aggregate
// updates without depending on the physical platform_records representation.
func PutRecordTx(ctx context.Context, tx Tx, table, key string, record Record) error {
	if tx == nil {
		return fmt.Errorf("database transaction is required")
	}
	table = SanitizeText(table)
	key = SanitizeText(key)
	record = SanitizeRecordText(record)
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode record: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO platform_records (table_name, record_key, payload_json)
VALUES (?, ?, ?)
ON CONFLICT(table_name, record_key) DO UPDATE SET payload_json=excluded.payload_json
WHERE platform_records.payload_json <> excluded.payload_json`, table, key, string(payload))
	if err != nil {
		return fmt.Errorf("upsert record: %w", err)
	}
	return nil
}

// InsertRecordTx claims a record key without replacing an existing value. The
// returned boolean is false when another transaction already owns the key.
func InsertRecordTx(ctx context.Context, tx Tx, table, key string, record Record) (bool, error) {
	if tx == nil {
		return false, fmt.Errorf("database transaction is required")
	}
	table = SanitizeText(table)
	key = SanitizeText(key)
	record = SanitizeRecordText(record)
	payload, err := json.Marshal(record)
	if err != nil {
		return false, fmt.Errorf("encode record: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
INSERT INTO platform_records (table_name, record_key, payload_json)
VALUES (?, ?, ?)
ON CONFLICT(table_name, record_key) DO NOTHING`, table, key, string(payload))
	if err != nil {
		return false, fmt.Errorf("insert record: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read inserted record count: %w", err)
	}
	return affected == 1, nil
}

func GetRecordTx(ctx context.Context, tx Tx, table, key string) (Record, bool, error) {
	if tx == nil {
		return nil, false, fmt.Errorf("database transaction is required")
	}
	var payload string
	err := tx.QueryRowContext(ctx, `SELECT payload_json FROM platform_records WHERE table_name = ? AND record_key = ?`, SanitizeText(table), SanitizeText(key)).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("get record: %w", err)
	}
	record, err := decodeRecord(payload)
	if err != nil {
		return nil, false, err
	}
	return record, true, nil
}

// DeleteRecordTx removes a platform record through an existing transaction.
func DeleteRecordTx(ctx context.Context, tx Tx, table, key string) error {
	if tx == nil {
		return fmt.Errorf("database transaction is required")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM platform_records WHERE table_name = ? AND record_key = ?`, SanitizeText(table), SanitizeText(key)); err != nil {
		return fmt.Errorf("delete record: %w", err)
	}
	return nil
}

// LockRecordKeyTx serializes a logical record mutation across application
// instances when PostgreSQL is used. SQLite writes are already serialized by
// Store.WithTx.
func LockRecordKeyTx(ctx context.Context, tx Tx, table, key string) error {
	if tx == nil {
		return fmt.Errorf("database transaction is required")
	}
	dialecter, ok := tx.(interface{ Dialect() Dialect })
	if !ok || dialecter.Dialect() != DialectPostgres {
		return nil
	}
	sum := sha256.Sum256([]byte(SanitizeText(table) + "\x00" + SanitizeText(key)))
	lockID := int64(binary.BigEndian.Uint64(sum[:8]))
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(?)`, lockID); err != nil {
		return fmt.Errorf("lock platform record key: %w", err)
	}
	return nil
}

func Open(ctx context.Context, cfg Config) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cfg.Driver == "" {
		cfg.Driver = DriverMemory
	}
	if cfg.Driver != DriverMemory && cfg.Driver != DriverFile && cfg.Driver != DriverPostgres {
		return nil, fmt.Errorf("unsupported database driver %q", cfg.Driver)
	}
	if cfg.Name == "" {
		cfg.Name = "orrery-v2"
	}

	sqlDriver, dsn, path, dialect, err := openConfig(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.Driver == DriverFile {
		if err := ensureParentDir(path); err != nil {
			return nil, err
		}
	}
	sqlDB, err := sql.Open(sqlDriver, dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s database: %w", dialect, err)
	}
	if dialect == DialectSQLite {
		sqlDB.SetMaxOpenConns(1)
	} else {
		sqlDB.SetMaxOpenConns(10)
		sqlDB.SetMaxIdleConns(2)
	}

	store := &Store{
		driver:  cfg.Driver,
		dialect: dialect,
		name:    cfg.Name,
		path:    path,
		sqlDB:   sqlDB,
	}
	if err := store.init(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Ping(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return fmt.Errorf("database is closed")
	}
	if err := s.sqlDB.PingContext(ctx); err != nil {
		return fmt.Errorf("ping %s database: %w", s.Dialect(), err)
	}
	return nil
}

// CheckWritable verifies that the database can execute a write without
// retaining health-check data. Connectivity alone does not detect exhausted
// PostgreSQL storage or a read-only database.
func (s *Store) CheckWritable(ctx context.Context) error {
	tx, err := s.BeginTx(ctx)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO platform_records (table_name, record_key, payload_json)
VALUES (?, ?, ?)
ON CONFLICT(table_name, record_key) DO UPDATE SET payload_json=excluded.payload_json`,
		"platform_health", "write_probe", `{"status":"ok"}`); err != nil {
		return errors.Join(fmt.Errorf("write probe %s database: %w", s.Dialect(), err), tx.Rollback())
	}
	if err := tx.Rollback(); err != nil {
		return fmt.Errorf("rollback write probe: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.sqlDB.Close()
}

func (s *Store) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if err := s.checkReady(ctx); err != nil {
		return nil, err
	}
	return s.sqlDB.ExecContext(ctx, s.prepare(query), args...)
}

func (s *Store) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if err := s.checkReady(ctx); err != nil {
		return nil, err
	}
	return s.sqlDB.QueryContext(ctx, s.prepare(query), args...)
}

func (s *Store) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return s.sqlDB.QueryRowContext(ctx, s.prepare(query), args...)
}

func (s *Store) WithTx(ctx context.Context, fn func(Tx) error) error {
	if fn == nil {
		return fmt.Errorf("transaction function is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.lockTransaction(ctx); err != nil {
		return err
	}
	defer s.releaseTransactionGate()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return fmt.Errorf("database is closed")
	}
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin %s transaction: %w", s.Dialect(), err)
	}
	// Also release SQL locks if a callback panics and an outer worker recovers.
	defer tx.Rollback()
	prepared := &preparedTx{tx: tx, dialect: s.Dialect()}
	if err := fn(prepared); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := ctx.Err(); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("commit %s transaction: %w", s.Dialect(), err)
	}
	if s.replaceErr != nil {
		_ = tx.Rollback()
		return fmt.Errorf("%w: %v", ErrPersist, s.replaceErr)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit %s transaction: %w", s.Dialect(), err)
	}
	return nil
}

func (s *Store) BeginTx(ctx context.Context) (ExplicitTx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.lockTransaction(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	if s.closed {
		s.unlockTransaction()
		return nil, fmt.Errorf("database is closed")
	}
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		s.unlockTransaction()
		return nil, fmt.Errorf("begin %s transaction: %w", s.Dialect(), err)
	}
	return &explicitTx{Tx: &preparedTx{tx: tx, dialect: s.Dialect()}, store: s}, nil
}

// WithReadTx gives PostgreSQL readers a consistent snapshot without joining the
// process-wide write queue. SQLite keeps its single-connection serialization.
func (s *Store) WithReadTx(ctx context.Context, fn func(Tx) error) error {
	if fn == nil {
		return fmt.Errorf("transaction function is required")
	}
	if s.Dialect() != DialectPostgres {
		return s.WithTx(ctx, fn)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return fmt.Errorf("database is closed")
	}
	tx, err := s.sqlDB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return fmt.Errorf("begin read transaction: %w", err)
	}
	defer tx.Rollback()
	if err := fn(&preparedTx{tx: tx, dialect: s.Dialect()}); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) unlockTransaction() {
	s.mu.RUnlock()
	s.releaseTransactionGate()
}

// Keep transaction serialization, but let queued callers honor cancellation.
func (s *Store) lockTransaction(ctx context.Context) error {
	s.txGateOnce.Do(func() { s.txGate = make(chan struct{}, 1) })
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case s.txGate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			s.releaseTransactionGate()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Store) releaseTransactionGate() {
	<-s.txGate
}

func (s *Store) lockWriteOperation(ctx context.Context) error {
	if err := s.lockTransaction(ctx); err != nil {
		return err
	}
	s.mu.RLock()
	if s.closed {
		s.unlockTransaction()
		return fmt.Errorf("database is closed")
	}
	return nil
}

func (s *Store) Name() string {
	return s.name
}

func (s *Store) Driver() Driver {
	return s.driver
}

func (s *Store) Dialect() Dialect {
	if s == nil || s.dialect == "" {
		return DialectSQLite
	}
	return s.dialect
}

func (s *Store) Stats() sql.DBStats {
	if s == nil || s.sqlDB == nil {
		return sql.DBStats{}
	}
	return s.sqlDB.Stats()
}

func (s *Store) TableExists(ctx context.Context, table string) (bool, error) {
	if err := s.checkReady(ctx); err != nil {
		return false, err
	}
	table = strings.TrimSpace(table)
	if table == "" {
		return false, fmt.Errorf("database table name is required")
	}
	schema, tableName, err := splitSafeTableIdentifier(table)
	if err != nil {
		return false, err
	}
	var foundTableName string
	query := `SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`
	args := []any{tableName}
	if s.Dialect() == DialectSQLite && schema != "" {
		query = fmt.Sprintf(`SELECT name FROM %s.sqlite_master WHERE type = 'table' AND name = ?`, schema)
	}
	if s.Dialect() == DialectPostgres {
		query = `
SELECT table_name
FROM information_schema.tables
WHERE table_schema = current_schema() AND table_name = ?`
		if schema != "" {
			query = `
SELECT table_name
FROM information_schema.tables
WHERE table_schema = ? AND table_name = ?`
			args = []any{schema, tableName}
		}
	}
	err = s.sqlDB.QueryRowContext(ctx, s.prepare(query), args...).Scan(&foundTableName)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check database table %s: %w", table, err)
	}
	return true, nil
}

func (s *Store) prepare(query string) string {
	return prepareSQLForDialect(s.Dialect(), query)
}

func (s *Store) checkReady(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return fmt.Errorf("database is closed")
	}
	return nil
}

func (s *Store) Put(table, key string, record Record) {
	_ = s.PutRecord(context.Background(), table, key, record)
}

func (s *Store) PutRecord(ctx context.Context, table, key string, record Record) error {
	if err := s.lockWriteOperation(ctx); err != nil {
		return err
	}
	defer s.unlockTransaction()
	return s.putLocked(ctx, nil, table, key, record)
}

// CreateRecord inserts a record only when the logical key does not already exist.
// The uniqueness check is enforced by the database primary key.
func (s *Store) CreateRecord(ctx context.Context, table, key string, record Record) (bool, error) {
	if err := s.lockWriteOperation(ctx); err != nil {
		return false, err
	}
	defer s.unlockTransaction()
	table = SanitizeText(table)
	key = SanitizeText(key)
	payload, err := json.Marshal(SanitizeRecordText(record))
	if err != nil {
		return false, fmt.Errorf("encode record: %w", err)
	}
	result, err := s.sqlDB.ExecContext(ctx, s.prepare(`
INSERT INTO platform_records (table_name, record_key, payload_json)
VALUES (?, ?, ?)
ON CONFLICT(table_name, record_key) DO NOTHING`), table, key, string(payload))
	if err != nil {
		return false, fmt.Errorf("create record: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read create record result: %w", err)
	}
	return affected == 1, nil
}

// CompareAndSwapRecord replaces a record only when its complete persisted value
// still matches the caller's previously read value.
func (s *Store) CompareAndSwapRecord(ctx context.Context, table, key string, expected, replacement Record) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return false, fmt.Errorf("database is closed")
	}
	table = SanitizeText(table)
	key = SanitizeText(key)
	expectedPayload, err := json.Marshal(SanitizeRecordText(expected))
	if err != nil {
		return false, fmt.Errorf("encode expected record: %w", err)
	}
	replacementPayload, err := json.Marshal(SanitizeRecordText(replacement))
	if err != nil {
		return false, fmt.Errorf("encode replacement record: %w", err)
	}
	result, err := s.sqlDB.ExecContext(ctx, s.prepare(`
UPDATE platform_records
SET payload_json = ?
WHERE table_name = ? AND record_key = ? AND `+recordPayloadMatchPredicate(s.Dialect())), string(replacementPayload), table, key, string(expectedPayload))
	if err != nil {
		return false, fmt.Errorf("compare and swap record: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read compare and swap result: %w", err)
	}
	return affected == 1, nil
}

// CompareAndDeleteRecord removes a record only when its complete persisted
// value still matches the caller's previously read value.
func (s *Store) CompareAndDeleteRecord(ctx context.Context, table, key string, expected Record) (bool, error) {
	if err := s.lockWriteOperation(ctx); err != nil {
		return false, err
	}
	defer s.unlockTransaction()
	table = SanitizeText(table)
	key = SanitizeText(key)
	expectedPayload, err := json.Marshal(SanitizeRecordText(expected))
	if err != nil {
		return false, fmt.Errorf("encode expected record: %w", err)
	}
	result, err := s.sqlDB.ExecContext(ctx, s.prepare(`
DELETE FROM platform_records
WHERE table_name = ? AND record_key = ? AND `+recordPayloadMatchPredicate(s.Dialect())), table, key, string(expectedPayload))
	if err != nil {
		return false, fmt.Errorf("compare and delete record: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read compare and delete result: %w", err)
	}
	return affected == 1, nil
}

func recordPayloadMatchPredicate(dialect Dialect) string {
	if dialect == DialectPostgres {
		return "payload_json::jsonb = CAST(? AS jsonb)"
	}
	return "payload_json = ?"
}

func (s *Store) Get(table, key string) (Record, bool) {
	record, ok, _ := s.GetRecord(context.Background(), table, key)
	return record, ok
}

func (s *Store) GetRecord(ctx context.Context, table, key string) (Record, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if s.closed {
		return nil, false, fmt.Errorf("database is closed")
	}
	return s.getLocked(ctx, nil, table, key)
}

// GetRecords returns the requested logical records with a bounded number of
// SQL parameters per query. Missing keys are omitted from the result.
func (s *Store) GetRecords(ctx context.Context, table string, keys []string) (map[string]Record, error) {
	if s == nil {
		return nil, fmt.Errorf("database store is required")
	}
	table = SanitizeText(strings.TrimSpace(table))
	if table == "" || len(table) > 256 || len(keys) > MaxRecordPageLimit {
		return nil, fmt.Errorf("record lookup table or key count is invalid")
	}
	if err := s.checkReady(ctx); err != nil {
		return nil, err
	}

	unique := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		key = SanitizeText(key)
		if key == "" || len(key) > 512 {
			return nil, fmt.Errorf("record lookup key is invalid")
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, key)
	}

	records := make(map[string]Record, len(unique))
	for start := 0; start < len(unique); start += maxRecordLookupBatch {
		end := min(start+maxRecordLookupBatch, len(unique))
		batch := unique[start:end]
		args := make([]any, 0, len(batch)+1)
		args = append(args, table)
		placeholders := make([]string, 0, len(batch))
		for _, key := range batch {
			placeholders = append(placeholders, "?")
			args = append(args, key)
		}
		rows, err := s.sqlDB.QueryContext(ctx, s.prepare(`
SELECT record_key, payload_json
FROM platform_records
WHERE table_name = ? AND record_key IN (`+strings.Join(placeholders, ",")+`)
ORDER BY record_key ASC`), args...)
		if err != nil {
			return nil, fmt.Errorf("get records: %w", err)
		}
		for rows.Next() {
			var key string
			var payload string
			if err := rows.Scan(&key, &payload); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan record lookup: %w", err)
			}
			record, err := decodeRecord(payload)
			if err != nil {
				rows.Close()
				return nil, fmt.Errorf("decode record %q: %w", key, err)
			}
			records[key] = record
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("iterate record lookup: %w", err)
		}
		rows.Close()
	}
	return records, nil
}

// ListRecordsTx returns a stable keyset page from one logical record table
// within an existing transaction.
func ListRecordsTx(ctx context.Context, tx Tx, table, afterKey string, limit int) (RecordPage, error) {
	if tx == nil {
		return RecordPage{}, fmt.Errorf("database transaction is required")
	}
	table = strings.TrimSpace(table)
	afterKey = strings.TrimSpace(afterKey)
	if table == "" || len(table) > 256 || len(afterKey) > 512 || limit <= 0 || limit > MaxRecordPageLimit {
		return RecordPage{}, fmt.Errorf("record page table, seek key, or limit is invalid")
	}
	if err := ctx.Err(); err != nil {
		return RecordPage{}, err
	}
	rows, err := tx.QueryContext(ctx, `
SELECT record_key, payload_json
FROM platform_records
WHERE table_name = ? AND record_key > ?
ORDER BY record_key ASC
LIMIT ?`, table, afterKey, limit+1)
	if err != nil {
		return RecordPage{}, fmt.Errorf("list transaction records: %w", err)
	}
	defer rows.Close()
	items := make([]KeyedRecord, 0, limit+1)
	for rows.Next() {
		var key string
		var payload string
		if err := rows.Scan(&key, &payload); err != nil {
			return RecordPage{}, fmt.Errorf("scan transaction record page: %w", err)
		}
		record, err := decodeRecord(payload)
		if err != nil {
			return RecordPage{}, fmt.Errorf("decode record %q: %w", key, err)
		}
		items = append(items, KeyedRecord{Key: key, Record: record})
	}
	if err := rows.Err(); err != nil {
		return RecordPage{}, fmt.Errorf("iterate transaction record page: %w", err)
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	return RecordPage{Items: items, HasMore: hasMore}, nil
}

// ListRecords returns a stable keyset page from a single logical record table.
// It is the bounded alternative to Snapshot for read-side catalogs.
func (s *Store) ListRecords(ctx context.Context, table, afterKey string, limit int) (RecordPage, error) {
	if s == nil {
		return RecordPage{}, fmt.Errorf("database store is required")
	}
	table = strings.TrimSpace(table)
	afterKey = strings.TrimSpace(afterKey)
	if table == "" || len(table) > 256 || len(afterKey) > 512 || limit <= 0 || limit > MaxRecordPageLimit {
		return RecordPage{}, fmt.Errorf("record page table, seek key, or limit is invalid")
	}
	if err := s.checkReady(ctx); err != nil {
		return RecordPage{}, err
	}
	rows, err := s.sqlDB.QueryContext(ctx, s.prepare(`
SELECT record_key, payload_json
FROM platform_records
WHERE table_name = ? AND record_key > ?
ORDER BY record_key ASC
LIMIT ?`), table, afterKey, limit+1)
	if err != nil {
		return RecordPage{}, fmt.Errorf("list records: %w", err)
	}
	defer rows.Close()
	items := make([]KeyedRecord, 0, limit+1)
	for rows.Next() {
		var key string
		var payload string
		if err := rows.Scan(&key, &payload); err != nil {
			return RecordPage{}, fmt.Errorf("scan record page: %w", err)
		}
		record, err := decodeRecord(payload)
		if err != nil {
			return RecordPage{}, fmt.Errorf("decode record %q: %w", key, err)
		}
		items = append(items, KeyedRecord{Key: key, Record: record})
	}
	if err := rows.Err(); err != nil {
		return RecordPage{}, fmt.Errorf("iterate record page: %w", err)
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	return RecordPage{Items: items, HasMore: hasMore}, nil
}

// ListRecordTable reads one logical table without materializing unrelated
// platform_records rows. Callers that need every record should use this instead
// of Snapshot, which exists only for whole-store compatibility operations.
func (s *Store) ListRecordTable(ctx context.Context, table string) (map[string]Record, error) {
	records := make(map[string]Record)
	afterKey := ""
	for {
		page, err := s.ListRecords(ctx, table, afterKey, MaxRecordPageLimit)
		if err != nil {
			return nil, err
		}
		for _, item := range page.Items {
			records[item.Key] = item.Record
			afterKey = item.Key
		}
		if !page.HasMore {
			return records, nil
		}
		if len(page.Items) == 0 {
			return nil, fmt.Errorf("record table pagination for %s made no progress", table)
		}
	}
}

func (s *Store) DeleteRecord(ctx context.Context, table, key string) error {
	if err := s.lockWriteOperation(ctx); err != nil {
		return err
	}
	defer s.unlockTransaction()
	_, err := s.sqlDB.ExecContext(ctx, s.prepare(`DELETE FROM platform_records WHERE table_name = ? AND record_key = ?`), table, key)
	if err != nil {
		return fmt.Errorf("delete record: %w", err)
	}
	return nil
}

func (s *Store) Snapshot() map[string]map[string]Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return map[string]map[string]Record{}
	}
	tables, _ := s.snapshotLocked(context.Background(), nil)
	return tables
}

func (s *Store) Replace(snapshot map[string]map[string]Record) error {
	if err := s.lockWriteOperation(context.Background()); err != nil {
		return fmt.Errorf("%w: %v", ErrPersist, err)
	}
	defer s.unlockTransaction()
	if s.replaceErr != nil {
		return fmt.Errorf("%w: %v", ErrPersist, s.replaceErr)
	}
	tx, err := s.sqlDB.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("%w: begin replace transaction: %v", ErrPersist, err)
	}
	if _, err := tx.ExecContext(context.Background(), `DELETE FROM platform_records`); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("%w: clear records: %v", ErrPersist, err)
	}
	for table, records := range snapshot {
		for key, record := range records {
			if err := s.putLocked(context.Background(), tx, table, key, record); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("%w: %v", ErrPersist, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%w: commit replace transaction: %v", ErrPersist, err)
	}
	return nil
}

func (s *Store) InjectReplaceError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replaceErr = err
}

func (s *Store) init(ctx context.Context) error {
	if s.Dialect() == DialectPostgres {
		return s.initPostgres(ctx)
	}
	return s.initSQLite(ctx)
}

func (s *Store) initSQLite(ctx context.Context) error {
	if _, err := s.sqlDB.ExecContext(ctx, `PRAGMA journal_mode=WAL`); err != nil {
		return fmt.Errorf("enable sqlite wal: %w", err)
	}
	if _, err := s.sqlDB.ExecContext(ctx, `PRAGMA busy_timeout=5000`); err != nil {
		return fmt.Errorf("enable sqlite busy timeout: %w", err)
	}
	if _, err := s.sqlDB.ExecContext(ctx, `PRAGMA foreign_keys=ON`); err != nil {
		return fmt.Errorf("enable sqlite foreign keys: %w", err)
	}
	_, err := s.sqlDB.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS platform_records (
	table_name TEXT NOT NULL,
	record_key TEXT NOT NULL,
	payload_json TEXT NOT NULL,
	PRIMARY KEY (table_name, record_key)
)`)
	if err != nil {
		return fmt.Errorf("create platform_records: %w", err)
	}
	return nil
}

func (s *Store) initPostgres(ctx context.Context) error {
	_, err := s.sqlDB.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS platform_records (
	table_name TEXT NOT NULL,
	record_key TEXT NOT NULL,
	payload_json TEXT NOT NULL,
	PRIMARY KEY (table_name, record_key)
)`)
	if err != nil {
		return fmt.Errorf("create platform_records: %w", err)
	}
	return nil
}

func (s *Store) putLocked(ctx context.Context, tx *sql.Tx, table, key string, record Record) error {
	table = SanitizeText(table)
	key = SanitizeText(key)
	record = SanitizeRecordText(record)
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode record: %w", err)
	}
	exec := func(query string, args ...any) (sql.Result, error) {
		if tx != nil {
			return tx.ExecContext(ctx, prepareSQLForDialect(s.Dialect(), query), args...)
		}
		return s.sqlDB.ExecContext(ctx, s.prepare(query), args...)
	}
	_, err = exec(`
INSERT INTO platform_records (table_name, record_key, payload_json)
VALUES (?, ?, ?)
ON CONFLICT(table_name, record_key) DO UPDATE SET payload_json=excluded.payload_json
WHERE platform_records.payload_json <> excluded.payload_json`, table, key, string(payload))
	if err != nil {
		return fmt.Errorf("upsert record: %w", err)
	}
	return nil
}

func SanitizeRecordText(src Record) Record {
	out := make(Record, len(src))
	for key, value := range src {
		out[SanitizeText(key)] = SanitizeValueText(value)
	}
	return out
}

func SanitizeValueText(value any) any {
	switch typed := value.(type) {
	case string:
		return SanitizeText(typed)
	case []string:
		out := make([]string, len(typed))
		for i, item := range typed {
			out[i] = SanitizeText(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = SanitizeValueText(item)
		}
		return out
	case map[string]string:
		out := make(map[string]string, len(typed))
		for key, item := range typed {
			out[SanitizeText(key)] = SanitizeText(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[SanitizeText(key)] = SanitizeValueText(item)
		}
		return out
	case Record:
		return SanitizeRecordText(typed)
	default:
		return value
	}
}

func SanitizeText(value string) string {
	if !strings.Contains(value, "\x00") {
		return value
	}
	return strings.ReplaceAll(value, "\x00", "")
}

func (s *Store) getLocked(ctx context.Context, tx *sql.Tx, table, key string) (Record, bool, error) {
	query := func(query string, args ...any) *sql.Row {
		if tx != nil {
			return tx.QueryRowContext(ctx, prepareSQLForDialect(s.Dialect(), query), args...)
		}
		return s.sqlDB.QueryRowContext(ctx, s.prepare(query), args...)
	}
	var payload string
	err := query(`SELECT payload_json FROM platform_records WHERE table_name = ? AND record_key = ?`, table, key).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("get record: %w", err)
	}
	record, err := decodeRecord(payload)
	if err != nil {
		return nil, false, err
	}
	return record, true, nil
}

func (s *Store) snapshotLocked(ctx context.Context, tx *sql.Tx) (map[string]map[string]Record, error) {
	query := func(query string, args ...any) (*sql.Rows, error) {
		if tx != nil {
			return tx.QueryContext(ctx, prepareSQLForDialect(s.Dialect(), query), args...)
		}
		return s.sqlDB.QueryContext(ctx, s.prepare(query), args...)
	}
	rows, err := query(`SELECT table_name, record_key, payload_json FROM platform_records ORDER BY table_name, record_key`)
	if err != nil {
		return nil, fmt.Errorf("snapshot records: %w", err)
	}
	defer rows.Close()

	tables := map[string]map[string]Record{}
	for rows.Next() {
		var table, key, payload string
		if err := rows.Scan(&table, &key, &payload); err != nil {
			return nil, fmt.Errorf("scan snapshot record: %w", err)
		}
		record, err := decodeRecord(payload)
		if err != nil {
			return nil, err
		}
		if tables[table] == nil {
			tables[table] = map[string]Record{}
		}
		tables[table][key] = record
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate snapshot records: %w", err)
	}
	return tables, nil
}

func decodeRecord(payload string) (Record, error) {
	var record Record
	if err := json.Unmarshal([]byte(payload), &record); err != nil {
		return nil, fmt.Errorf("decode record: %w", err)
	}
	return cloneRecord(record), nil
}

func cloneRecord(src Record) Record {
	dst := make(Record, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func prepareSQLForDialect(dialect Dialect, query string) string {
	if dialect != DialectPostgres || !strings.Contains(query, "?") {
		return query
	}
	var out strings.Builder
	out.Grow(len(query) + 8)
	placeholder := 1
	for i := 0; i < len(query); i++ {
		ch := query[i]
		switch ch {
		case '\'':
			i = writeQuotedSQL(&out, query, i, '\'')
		case '"':
			i = writeQuotedSQL(&out, query, i, '"')
		case '-':
			if i+1 < len(query) && query[i+1] == '-' {
				i = writeLineCommentSQL(&out, query, i)
				continue
			}
			out.WriteByte(ch)
		case '/':
			if i+1 < len(query) && query[i+1] == '*' {
				i = writeBlockCommentSQL(&out, query, i)
				continue
			}
			out.WriteByte(ch)
		case '?':
			out.WriteByte('$')
			out.WriteString(strconv.Itoa(placeholder))
			placeholder++
		default:
			out.WriteByte(ch)
		}
	}
	return out.String()
}

func writeQuotedSQL(out *strings.Builder, query string, start int, quote byte) int {
	out.WriteByte(query[start])
	for i := start + 1; i < len(query); i++ {
		out.WriteByte(query[i])
		if query[i] != quote {
			continue
		}
		if i+1 < len(query) && query[i+1] == quote {
			i++
			out.WriteByte(query[i])
			continue
		}
		return i
	}
	return len(query) - 1
}

func writeLineCommentSQL(out *strings.Builder, query string, start int) int {
	out.WriteString("--")
	for i := start + 2; i < len(query); i++ {
		out.WriteByte(query[i])
		if query[i] == '\n' {
			return i
		}
	}
	return len(query) - 1
}

func writeBlockCommentSQL(out *strings.Builder, query string, start int) int {
	out.WriteString("/*")
	for i := start + 2; i < len(query); i++ {
		out.WriteByte(query[i])
		if query[i] == '*' && i+1 < len(query) && query[i+1] == '/' {
			i++
			out.WriteByte(query[i])
			return i
		}
	}
	return len(query) - 1
}

func openConfig(cfg Config) (sqlDriver string, dsn string, path string, dialect Dialect, err error) {
	switch cfg.Driver {
	case DriverMemory, DriverFile:
		dsn, path, err := sqliteDSN(cfg)
		if err != nil {
			return "", "", "", "", err
		}
		return "sqlite", dsn, path, DialectSQLite, nil
	case DriverPostgres:
		if strings.TrimSpace(cfg.URL) == "" {
			return "", "", "", "", fmt.Errorf("postgres database url is empty")
		}
		return "pgx", cfg.URL, "", DialectPostgres, nil
	default:
		return "", "", "", "", fmt.Errorf("unsupported database driver %q", cfg.Driver)
	}
}

func sqliteDSN(cfg Config) (dsn string, path string, err error) {
	switch cfg.Driver {
	case DriverMemory:
		name := sanitizeMemoryName(cfg.Name)
		return "file:" + name + "?mode=memory&cache=shared", "", nil
	case DriverFile:
		path, err := filePath(cfg.URL)
		if err != nil {
			return "", "", err
		}
		return path, path, nil
	default:
		return "", "", fmt.Errorf("unsupported sqlite database driver %q", cfg.Driver)
	}
}

func sanitizeMemoryName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "orrery-v2"
	}
	replacer := strings.NewReplacer("\\", "_", "/", "_", " ", "_", ":", "_")
	return replacer.Replace(name)
}

func filePath(rawURL string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", fmt.Errorf("file database url is empty")
	}
	if strings.HasPrefix(rawURL, "file://") {
		return filepath.FromSlash(strings.TrimPrefix(rawURL, "file://")), nil
	}
	if strings.HasPrefix(rawURL, "sqlite://") {
		rawPath := strings.TrimPrefix(rawURL, "sqlite://")
		if filepath.IsAbs(rawPath) {
			return filepath.Clean(rawPath), nil
		}
		parsed, err := url.Parse(rawURL)
		if err != nil {
			return "", fmt.Errorf("parse sqlite database url: %w", err)
		}
		if parsed.Host != "" {
			return filepath.FromSlash(parsed.Host + parsed.Path), nil
		}
		return filepath.FromSlash(parsed.Path), nil
	}
	return "", fmt.Errorf("file database url must use file:// or sqlite://")
}

func ensureParentDir(path string) error {
	if path == "" {
		return fmt.Errorf("file database path is empty")
	}
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return fmt.Errorf("file database path is a directory: %s", path)
	}
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create database parent directory: %w", err)
	}
	return nil
}

func splitSafeTableIdentifier(identifier string) (string, string, error) {
	parts := strings.Split(identifier, ".")
	if len(parts) > 2 {
		return "", "", fmt.Errorf("database table name %q must be table or schema.table", identifier)
	}
	for _, part := range parts {
		if !isSafeIdentifierPart(part) {
			return "", "", fmt.Errorf("database table name %q is not a safe identifier", identifier)
		}
	}
	if len(parts) == 2 {
		return parts[0], parts[1], nil
	}
	return "", parts[0], nil
}

func isSafeIdentifierPart(part string) bool {
	if part == "" {
		return false
	}
	for _, r := range part {
		if r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}
