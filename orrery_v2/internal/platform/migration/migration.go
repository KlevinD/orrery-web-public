package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/db"
)

const tableName = "schema_migrations"

type Migration struct {
	Version             string
	Description         string
	Statements          []string
	StatementsByDialect map[db.Dialect][]string
}

type Runner struct {
	migrations []Migration
	clock      clock.Clock
}

type RunnerOptions struct {
	Clock clock.Clock
}

func NewRunner(migrations []Migration) *Runner {
	return NewRunnerWithOptions(migrations, RunnerOptions{})
}

func NewRunnerWithOptions(migrations []Migration, opts RunnerOptions) *Runner {
	copied := append([]Migration(nil), migrations...)
	return &Runner{migrations: copied, clock: migrationClock(opts.Clock)}
}

func (r *Runner) Up(ctx context.Context, store *db.Store) error {
	migrations, err := sortedMigrations(r.migrations)
	if err != nil {
		return err
	}
	if err := store.Ping(ctx); err != nil {
		return err
	}
	if err := ensureSchemaMigrations(ctx, store); err != nil {
		return err
	}
	for _, migration := range migrations {
		if err := r.applyMigration(ctx, store, migration); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) LatestApplied(ctx context.Context, store *db.Store) (bool, error) {
	migrations, err := sortedMigrations(r.migrations)
	if err != nil {
		return false, err
	}
	if err := store.Ping(ctx); err != nil {
		return false, err
	}
	if err := ensureSchemaMigrations(ctx, store); err != nil {
		return false, err
	}
	if len(migrations) == 0 {
		return true, nil
	}
	latest := strings.TrimSpace(migrations[len(migrations)-1].Version)
	if latest == "" {
		return false, fmt.Errorf("migration version is empty")
	}
	var version string
	err = store.QueryRow(ctx, `SELECT version FROM schema_migrations WHERE version = ?`, latest).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query latest migration: %w", err)
	}
	return true, nil
}

func PlatformMigrations() []Migration {
	var migrations []Migration
	migrations = append(migrations, platformEventMigrations()...)
	migrations = append(migrations, platformJobMigrations()...)
	migrations = append(migrations, platformRuntimeMigrations()...)
	return migrations
}

func PlatformMigrationRegistry() *Registry {
	return PlatformMigrationRegistryWithOptions(RunnerOptions{})
}

func PlatformMigrationRegistryWithOptions(opts RunnerOptions) *Registry {
	registry := NewRegistryWithOptions(opts)
	for _, migration := range platformEventMigrations() {
		registry.Register(db.RoleEvents, migration)
	}
	for _, migration := range platformJobMigrations() {
		registry.Register(db.RoleJobs, migration)
	}
	for _, migration := range platformRuntimeMigrations() {
		registry.Register(db.RoleRuntime, migration)
	}
	return registry
}

func platformEventMigrations() []Migration {
	return []Migration{
		{
			Version:     "0001_platform_events",
			Description: "create platform event store tables",
			Statements: []string{
				`CREATE TABLE IF NOT EXISTS platform_events (
					event_id TEXT PRIMARY KEY,
					stream_id TEXT NOT NULL,
					stream_version INTEGER NOT NULL,
					event_type TEXT NOT NULL,
					event_version INTEGER NOT NULL,
					payload_json TEXT NOT NULL,
					metadata_json TEXT NOT NULL,
					correlation_id TEXT,
					causation_id TEXT,
					idempotency_key TEXT,
					created_at TEXT NOT NULL,
					UNIQUE(stream_id, stream_version)
				)`,
				`CREATE INDEX IF NOT EXISTS idx_platform_events_stream ON platform_events (stream_id, stream_version)`,
				`CREATE TABLE IF NOT EXISTS platform_event_idempotency (
					idempotency_key TEXT PRIMARY KEY,
					event_id TEXT NOT NULL
				)`,
			},
		},
	}
}

func platformJobMigrations() []Migration {
	return []Migration{
		{
			Version:     "0002_platform_outbox",
			Description: "create platform outbox tables",
			Statements: []string{
				`CREATE TABLE IF NOT EXISTS platform_outbox_jobs (
					id TEXT PRIMARY KEY,
					topic TEXT NOT NULL,
					payload BLOB,
					idempotency_key TEXT,
					status TEXT NOT NULL,
					worker_id TEXT,
					attempts INTEGER NOT NULL DEFAULT 0,
					max_attempts INTEGER NOT NULL,
					next_attempt_at INTEGER NOT NULL DEFAULT 0,
					locked_until INTEGER,
					last_error TEXT,
					created_at INTEGER NOT NULL,
					updated_at INTEGER NOT NULL
				)`,
				`CREATE INDEX IF NOT EXISTS idx_platform_outbox_pending ON platform_outbox_jobs (next_attempt_at, created_at) WHERE status = 'pending'`,
				`CREATE INDEX IF NOT EXISTS idx_platform_outbox_locked_until ON platform_outbox_jobs (locked_until) WHERE status = 'running'`,
				`CREATE UNIQUE INDEX IF NOT EXISTS idx_platform_outbox_idempotency ON platform_outbox_jobs (topic, idempotency_key) WHERE idempotency_key IS NOT NULL AND idempotency_key != ''`,
			},
			StatementsByDialect: map[db.Dialect][]string{
				db.DialectPostgres: {
					`CREATE TABLE IF NOT EXISTS platform_outbox_jobs (
						id TEXT PRIMARY KEY,
						topic TEXT NOT NULL,
						payload BYTEA,
						idempotency_key TEXT,
						status TEXT NOT NULL,
						worker_id TEXT,
						attempts INTEGER NOT NULL DEFAULT 0,
						max_attempts INTEGER NOT NULL,
						next_attempt_at BIGINT NOT NULL DEFAULT 0,
						locked_until BIGINT,
						last_error TEXT,
						created_at BIGINT NOT NULL,
						updated_at BIGINT NOT NULL
					)`,
					`CREATE INDEX IF NOT EXISTS idx_platform_outbox_pending ON platform_outbox_jobs (next_attempt_at, created_at) WHERE status = 'pending'`,
					`CREATE INDEX IF NOT EXISTS idx_platform_outbox_locked_until ON platform_outbox_jobs (locked_until) WHERE status = 'running'`,
					`CREATE UNIQUE INDEX IF NOT EXISTS idx_platform_outbox_idempotency ON platform_outbox_jobs (topic, idempotency_key) WHERE idempotency_key IS NOT NULL AND idempotency_key != ''`,
				},
			},
		},
		{
			Version:     "0003_platform_queue",
			Description: "create platform queue tables",
			Statements: []string{
				`CREATE TABLE IF NOT EXISTS platform_queue_jobs (
					id TEXT PRIMARY KEY,
					topic TEXT NOT NULL,
					payload BLOB,
					queue_key TEXT NOT NULL DEFAULT 'default',
					status TEXT NOT NULL,
					priority INTEGER NOT NULL DEFAULT 0,
					attempts INTEGER NOT NULL DEFAULT 0,
					max_attempts INTEGER NOT NULL,
					next_attempt_at INTEGER NOT NULL DEFAULT 0,
					worker_id TEXT,
					locked_until INTEGER,
					last_error TEXT,
					failure_reason TEXT,
					created_at INTEGER NOT NULL,
					updated_at INTEGER NOT NULL,
					dispatched_at INTEGER,
					started_at INTEGER,
					completed_at INTEGER
				)`,
				`CREATE INDEX IF NOT EXISTS idx_platform_queue_claim ON platform_queue_jobs (priority DESC, created_at ASC) WHERE status = 'queued'`,
				`CREATE INDEX IF NOT EXISTS idx_platform_queue_ready ON platform_queue_jobs (next_attempt_at, priority DESC, created_at ASC) WHERE status = 'queued'`,
				`CREATE INDEX IF NOT EXISTS idx_platform_queue_key_active ON platform_queue_jobs (queue_key, status) WHERE status IN ('dispatched', 'running')`,
				`CREATE INDEX IF NOT EXISTS idx_platform_queue_locked_until ON platform_queue_jobs (locked_until) WHERE status IN ('dispatched', 'running')`,
			},
			StatementsByDialect: map[db.Dialect][]string{
				db.DialectPostgres: {
					`CREATE TABLE IF NOT EXISTS platform_queue_jobs (
						id TEXT PRIMARY KEY,
						topic TEXT NOT NULL,
						payload BYTEA,
						queue_key TEXT NOT NULL DEFAULT 'default',
						status TEXT NOT NULL,
						priority INTEGER NOT NULL DEFAULT 0,
						attempts INTEGER NOT NULL DEFAULT 0,
						max_attempts INTEGER NOT NULL,
						next_attempt_at BIGINT NOT NULL DEFAULT 0,
						worker_id TEXT,
						locked_until BIGINT,
						last_error TEXT,
						failure_reason TEXT,
						created_at BIGINT NOT NULL,
						updated_at BIGINT NOT NULL,
						dispatched_at BIGINT,
						started_at BIGINT,
						completed_at BIGINT
					)`,
					`CREATE INDEX IF NOT EXISTS idx_platform_queue_claim ON platform_queue_jobs (priority DESC, created_at ASC) WHERE status = 'queued'`,
					`CREATE INDEX IF NOT EXISTS idx_platform_queue_ready ON platform_queue_jobs (next_attempt_at, priority DESC, created_at ASC) WHERE status = 'queued'`,
					`CREATE INDEX IF NOT EXISTS idx_platform_queue_key_active ON platform_queue_jobs (queue_key, status) WHERE status IN ('dispatched', 'running')`,
					`CREATE INDEX IF NOT EXISTS idx_platform_queue_locked_until ON platform_queue_jobs (locked_until) WHERE status IN ('dispatched', 'running')`,
				},
			},
		},
	}
}

func platformRuntimeMigrations() []Migration {
	return []Migration{
		{
			Version:     "0004_platform_runtime_state",
			Description: "create platform lock claim and liveness tables",
			Statements: []string{
				`CREATE TABLE IF NOT EXISTS platform_locks (
					lock_key TEXT PRIMARY KEY,
					owner TEXT NOT NULL,
					expires_at INTEGER NOT NULL,
					updated_at INTEGER NOT NULL
				)`,
				`CREATE INDEX IF NOT EXISTS idx_platform_locks_expires_at ON platform_locks (expires_at)`,
				`CREATE TABLE IF NOT EXISTS platform_claim_leases (
					run_id TEXT PRIMARY KEY,
					worker_id TEXT NOT NULL,
					expires_at INTEGER NOT NULL,
					updated_at INTEGER NOT NULL
				)`,
				`CREATE INDEX IF NOT EXISTS idx_platform_claim_leases_expires_at ON platform_claim_leases (expires_at)`,
				`CREATE TABLE IF NOT EXISTS platform_negative_claim_cache (
					cache_key TEXT PRIMARY KEY,
					version INTEGER NOT NULL DEFAULT 0,
					empty_version INTEGER,
					updated_at INTEGER
				)`,
				`CREATE TABLE IF NOT EXISTS platform_liveness (
					runtime_id TEXT PRIMARY KEY,
					last_seen_at INTEGER NOT NULL,
					expires_at INTEGER NOT NULL
				)`,
				`CREATE INDEX IF NOT EXISTS idx_platform_liveness_expires_at ON platform_liveness (expires_at)`,
			},
			StatementsByDialect: map[db.Dialect][]string{
				db.DialectPostgres: {
					`CREATE TABLE IF NOT EXISTS platform_locks (
						lock_key TEXT PRIMARY KEY,
						owner TEXT NOT NULL,
						expires_at BIGINT NOT NULL,
						updated_at BIGINT NOT NULL
					)`,
					`CREATE INDEX IF NOT EXISTS idx_platform_locks_expires_at ON platform_locks (expires_at)`,
					`CREATE TABLE IF NOT EXISTS platform_claim_leases (
						run_id TEXT PRIMARY KEY,
						worker_id TEXT NOT NULL,
						expires_at BIGINT NOT NULL,
						updated_at BIGINT NOT NULL
					)`,
					`CREATE INDEX IF NOT EXISTS idx_platform_claim_leases_expires_at ON platform_claim_leases (expires_at)`,
					`CREATE TABLE IF NOT EXISTS platform_negative_claim_cache (
						cache_key TEXT PRIMARY KEY,
						version INTEGER NOT NULL DEFAULT 0,
						empty_version INTEGER,
						updated_at BIGINT
					)`,
					`CREATE TABLE IF NOT EXISTS platform_liveness (
						runtime_id TEXT PRIMARY KEY,
						last_seen_at BIGINT NOT NULL,
						expires_at BIGINT NOT NULL
					)`,
					`CREATE INDEX IF NOT EXISTS idx_platform_liveness_expires_at ON platform_liveness (expires_at)`,
				},
			},
		},
	}
}

func ensureSchemaMigrations(ctx context.Context, store *db.Store) error {
	appliedAtType := "INTEGER"
	if store.Dialect() == db.DialectPostgres {
		appliedAtType = "BIGINT"
	}
	_, err := store.Exec(ctx, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS schema_migrations (
	version TEXT PRIMARY KEY,
	description TEXT NOT NULL,
	applied_at %s NOT NULL
)`, appliedAtType))
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	return nil
}

func validateMigrationList(migrations []Migration) error {
	seen := map[string]struct{}{}
	for _, migration := range migrations {
		version := strings.TrimSpace(migration.Version)
		if version == "" {
			return fmt.Errorf("migration version is empty")
		}
		if _, ok := seen[version]; ok {
			return fmt.Errorf("duplicate migration version %q", version)
		}
		seen[version] = struct{}{}
	}
	return nil
}

func sortedMigrations(migrations []Migration) ([]Migration, error) {
	copied := append([]Migration(nil), migrations...)
	sort.SliceStable(copied, func(i, j int) bool {
		return copied[i].Version < copied[j].Version
	})
	if err := validateMigrationList(copied); err != nil {
		return nil, err
	}
	return copied, nil
}

func statementsForDialect(migration Migration, dialect db.Dialect) []string {
	if migration.StatementsByDialect != nil {
		if statements, ok := migration.StatementsByDialect[dialect]; ok {
			return append([]string(nil), statements...)
		}
	}
	return append([]string(nil), migration.Statements...)
}

func (r *Runner) applyMigration(ctx context.Context, store *db.Store, migration Migration) error {
	version := strings.TrimSpace(migration.Version)
	if version == "" {
		return fmt.Errorf("migration version is empty")
	}
	var existing string
	err := store.QueryRow(ctx, `SELECT version FROM schema_migrations WHERE version = ?`, version).Scan(&existing)
	if err == nil {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("query migration %s: %w", version, err)
	}
	return store.WithTx(ctx, func(tx db.Tx) error {
		for _, statement := range statementsForDialect(migration, store.Dialect()) {
			statement = strings.TrimSpace(statement)
			if statement == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply migration %s: %w", version, err)
			}
		}
		_, err := tx.ExecContext(ctx, `
INSERT INTO schema_migrations (version, description, applied_at)
VALUES (?, ?, ?)`, version, migration.Description, r.now().UnixNano())
		if err != nil {
			return fmt.Errorf("record migration %s: %w", version, err)
		}
		return nil
	})
}

func (r *Runner) now() time.Time {
	if r == nil || r.clock == nil {
		return time.Now().UTC()
	}
	return r.clock.Now().UTC()
}

func migrationClock(c clock.Clock) clock.Clock {
	if c != nil {
		return c
	}
	return clock.Real{}
}
