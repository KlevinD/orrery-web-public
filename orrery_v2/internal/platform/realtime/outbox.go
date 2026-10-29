package realtime

import (
	"encoding/json"
	"time"

	"orrery_v2/internal/platform/db"
)

type Status string

const (
	StatusPending    Status = "pending"
	StatusPublishing Status = "publishing"
	StatusPublished  Status = "published"
	StatusFailed     Status = "failed"
)

type StoreOptions struct {
	Now         func() time.Time
	LeaseTTL    time.Duration
	MaxAttempts int
}

type AppendEvent struct {
	EventID       string
	Type          string
	Channel       string
	AggregateType string
	AggregateID   string
	Payload       json.RawMessage
}

type OutboxEvent struct {
	ID          string
	Envelope    Envelope
	Status      Status
	Attempts    int
	MaxAttempts int
	WorkerID    string
	LeaseUntil  time.Time
	LastError   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	PublishedAt time.Time
}

type SQLMigration struct {
	Version             string
	Description         string
	Statements          []string
	StatementsByDialect map[db.Dialect][]string
}

func OutboxMigration() SQLMigration {
	return SQLMigration{
		Version:     realtimeOutboxMigrationVersion,
		Description: realtimeOutboxMigrationDescription,
		Statements:  outboxSchemaStatements(db.DialectSQLite),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: outboxSchemaStatements(db.DialectPostgres),
		},
	}
}

const (
	realtimeOutboxMigrationVersion     = "030_realtime_outbox"
	realtimeOutboxMigrationDescription = "create realtime outbox table"
)
