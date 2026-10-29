package eventstore_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/eventstore"
)

func TestAppendAndLoadEventsInStreamOrder(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	es := eventstore.New(store)

	err := es.Append(ctx, eventstore.Envelope{
		EventID:       "evt-1",
		StreamID:      "mission-1",
		StreamVersion: 1,
		EventType:     "sample.created",
		EventVersion:  1,
		Payload:       []byte(`{"ok":true}`),
		Metadata:      []byte(`{"source":"test"}`),
		CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("Append returned error: %v", err)
	}
	events, err := es.Load(ctx, "mission-1")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(events) != 1 || events[0].EventID != "evt-1" || string(events[0].Payload) != `{"ok":true}` {
		t.Fatalf("unexpected events: %+v", events)
	}
}

func TestListEventsAcrossStreamsInCreationOrder(t *testing.T) {
	ctx := context.Background()
	es := eventstore.New(newStore(t))
	events := []eventstore.Envelope{
		{EventID: "evt-2", StreamID: "stream-b", StreamVersion: 1, EventType: "ontology.project.created", EventVersion: 1, CreatedAt: time.Unix(2, 0).UTC()},
		{EventID: "evt-1", StreamID: "stream-a", StreamVersion: 1, EventType: "ontology.organization.created", EventVersion: 1, CreatedAt: time.Unix(1, 0).UTC()},
		{EventID: "evt-3", StreamID: "stream-a", StreamVersion: 2, EventType: "mission.created", EventVersion: 1, CreatedAt: time.Unix(3, 0).UTC()},
	}
	for _, event := range events {
		if err := es.Append(ctx, event); err != nil {
			t.Fatalf("Append %s returned error: %v", event.EventID, err)
		}
	}

	listed, err := es.List(ctx, eventstore.ListQuery{EventTypePrefix: "ontology."})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(listed) != 2 || listed[0].EventID != "evt-1" || listed[1].EventID != "evt-2" {
		t.Fatalf("listed ontology events = %+v", listed)
	}
}

func TestStoreGetByIDReturnsExactEnvelope(t *testing.T) {
	ctx := context.Background()
	es := eventstore.New(newStore(t))
	want := eventstore.Envelope{
		EventID: "evt-1", StreamID: "stream-a", StreamVersion: 1, EventType: "ontology.project.created", EventVersion: 2,
		Payload: []byte(`{"id":"project-1"}`), Metadata: []byte(`{"tenant_id":"project:project-1"}`), CreatedAt: time.Unix(1, 0).UTC(),
	}
	if err := es.Append(ctx, want); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, err := es.GetByID(ctx, want.EventID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.EventID != want.EventID || got.StreamID != want.StreamID || got.EventVersion != want.EventVersion || string(got.Payload) != string(want.Payload) || string(got.Metadata) != string(want.Metadata) {
		t.Fatalf("GetByID = %+v, want %+v", got, want)
	}
	if _, err := es.GetByID(ctx, ""); err == nil {
		t.Fatal("GetByID empty id error = nil")
	}
}

func TestStoreListIDsByEventTypeReturnsOnlyExactMatches(t *testing.T) {
	ctx := context.Background()
	es := eventstore.New(newStore(t))
	for _, event := range []eventstore.Envelope{
		{EventID: "checkpoint-1", StreamID: "stream-1", StreamVersion: 1, EventType: "ontology.startup.backfill.completed", EventVersion: 1},
		{EventID: "checkpoint-2", StreamID: "stream-2", StreamVersion: 1, EventType: "ontology.startup.backfill.completed", EventVersion: 1},
		{EventID: "other-1", StreamID: "stream-3", StreamVersion: 1, EventType: "ontology.project.created", EventVersion: 1},
	} {
		if err := es.Append(ctx, event); err != nil {
			t.Fatalf("Append %s: %v", event.EventID, err)
		}
	}
	existing, err := es.ListIDsByEventType(ctx, "ontology.startup.backfill.completed")
	if err != nil {
		t.Fatalf("ListIDsByEventType: %v", err)
	}
	if len(existing) != 2 || !existing["checkpoint-1"] || !existing["checkpoint-2"] {
		t.Fatalf("existing IDs = %v, want checkpoint-1 and checkpoint-2", existing)
	}
	if _, err := es.ListIDsByEventType(ctx, ""); err == nil {
		t.Fatal("ListIDsByEventType empty type error = nil")
	}
}

func TestAppendUsesInjectedClockWhenCreatedAtMissing(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 31, 13, 0, 0, 0, time.UTC)
	es := eventstore.NewWithOptions(newStore(t), eventstore.StoreOptions{
		Clock: clock.NewFake(now),
	})

	if err := es.Append(ctx, eventstore.Envelope{
		EventID:       "evt-1",
		StreamID:      "stream-1",
		StreamVersion: 1,
		EventType:     "created",
		EventVersion:  1,
	}); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}

	events, err := es.Load(ctx, "stream-1")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(events) != 1 || !events[0].CreatedAt.Equal(now) {
		t.Fatalf("events = %+v, want CreatedAt %s", events, now)
	}
}

func TestAppendRejectsStreamVersionConflict(t *testing.T) {
	ctx := context.Background()
	es := eventstore.New(newStore(t))
	first := eventstore.Envelope{EventID: "evt-1", StreamID: "mission-1", StreamVersion: 1, EventType: "a", EventVersion: 1}
	second := eventstore.Envelope{EventID: "evt-2", StreamID: "mission-1", StreamVersion: 1, EventType: "b", EventVersion: 1}

	if err := es.Append(ctx, first); err != nil {
		t.Fatalf("first append: %v", err)
	}
	err := es.Append(ctx, second)
	if !errors.Is(err, eventstore.ErrConflict) {
		t.Fatalf("second append error = %v, want conflict", err)
	}
}

func TestConcurrentAppendOnlyOneEventWinsStreamVersion(t *testing.T) {
	ctx := context.Background()
	es := eventstore.New(newStore(t))

	const writers = 100
	var ready sync.WaitGroup
	ready.Add(writers)
	start := make(chan struct{})
	errs := make(chan error, writers)

	for i := 0; i < writers; i++ {
		i := i
		go func() {
			ready.Done()
			<-start
			errs <- es.Append(ctx, eventstore.Envelope{
				EventID:       fmt.Sprintf("evt-%03d", i),
				StreamID:      "mission-1",
				StreamVersion: 1,
				EventType:     "sample.created",
				EventVersion:  1,
			})
		}()
	}
	ready.Wait()
	close(start)

	successes := 0
	conflicts := 0
	for i := 0; i < writers; i++ {
		err := <-errs
		switch {
		case err == nil:
			successes++
		case errors.Is(err, eventstore.ErrConflict):
			conflicts++
		default:
			t.Fatalf("Append returned unexpected error: %v", err)
		}
	}
	if successes != 1 || conflicts != writers-1 {
		t.Fatalf("successes=%d conflicts=%d, want 1/%d", successes, conflicts, writers-1)
	}
	events, err := es.Load(ctx, "mission-1")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("loaded %d events, want 1: %+v", len(events), events)
	}
}

func TestAppendWithSameIdempotencyKeyIsIdempotent(t *testing.T) {
	ctx := context.Background()
	es := eventstore.New(newStore(t))
	event := eventstore.Envelope{EventID: "evt-1", StreamID: "mission-1", StreamVersion: 1, EventType: "a", EventVersion: 1, IdempotencyKey: "idem-1"}

	if err := es.Append(ctx, event); err != nil {
		t.Fatalf("first append: %v", err)
	}
	if err := es.Append(ctx, event); err != nil {
		t.Fatalf("second append with same idempotency key returned error: %v", err)
	}
	events, err := es.Load(ctx, "mission-1")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
}

func TestEventsRemainReadableAfterFileDatabaseReopen(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/events.json"
	store, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + path})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	es := eventstore.New(store)
	if err := es.Append(ctx, eventstore.Envelope{EventID: "evt-1", StreamID: "mission-1", StreamVersion: 1, EventType: "created", EventVersion: 1}); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}
	_ = store.Close()

	reopened, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + path})
	if err != nil {
		t.Fatalf("reopen returned error: %v", err)
	}
	defer reopened.Close()
	events, err := eventstore.New(reopened).Load(ctx, "mission-1")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(events) != 1 || events[0].EventID != "evt-1" {
		t.Fatalf("events after reopen = %+v", events)
	}
}

func TestEventStoreUsesDedicatedSQLiteSchema(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	es := eventstore.New(store)

	if err := es.Append(ctx, eventstore.Envelope{
		EventID:       "evt-1",
		StreamID:      "mission-1",
		StreamVersion: 1,
		EventType:     "created",
		EventVersion:  1,
		Payload:       []byte(`{"ok":true}`),
	}); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}

	var count int
	if err := store.QueryRow(ctx, `SELECT COUNT(*) FROM platform_events WHERE event_id = ?`, "evt-1").Scan(&count); err != nil {
		t.Fatalf("query dedicated event table: %v", err)
	}
	if count != 1 {
		t.Fatalf("platform_events count = %d, want 1", count)
	}
}

func TestPostgresAppendLoadConflictAndIdempotency(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{
		Driver: db.DriverPostgres,
		URL:    postgresTestURL(t),
		Name:   "postgres-eventstore",
	})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	dropEventStoreTables(t, store)
	es := eventstore.New(store)
	event := eventstore.Envelope{
		EventID:        "evt-postgres-1",
		StreamID:       "stream-postgres-1",
		StreamVersion:  1,
		EventType:      "sample.created",
		EventVersion:   1,
		Payload:        []byte(`{"ok":true}`),
		Metadata:       []byte(`{"source":"postgres"}`),
		IdempotencyKey: "idem-postgres-1",
	}

	if err := es.Append(ctx, event); err != nil {
		t.Fatalf("first Append returned error: %v", err)
	}
	if err := es.Append(ctx, event); err != nil {
		t.Fatalf("idempotent Append returned error: %v", err)
	}
	err = es.Append(ctx, eventstore.Envelope{
		EventID:       "evt-postgres-2",
		StreamID:      "stream-postgres-1",
		StreamVersion: 1,
		EventType:     "sample.updated",
		EventVersion:  1,
	})
	if !errors.Is(err, eventstore.ErrConflict) {
		t.Fatalf("conflicting Append error = %v, want ErrConflict", err)
	}

	events, err := es.Load(ctx, "stream-postgres-1")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(events) != 1 || events[0].EventID != event.EventID || string(events[0].Payload) != `{"ok":true}` {
		t.Fatalf("events = %+v, want one postgres event", events)
	}
}

func TestOpenReturnsInitializationError(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	if _, err := eventstore.Open(ctx, store); err == nil {
		t.Fatalf("Open returned nil error for closed database")
	}
}

func newStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(context.Background(), db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func postgresTestURL(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("ORRERY_TEST_POSTGRES_URL")
	if raw == "" {
		t.Skip("ORRERY_TEST_POSTGRES_URL is not set")
	}
	return raw
}

func dropEventStoreTables(t *testing.T, store *db.Store) {
	t.Helper()
	if store.Dialect() != db.DialectPostgres {
		t.Fatalf("dropEventStoreTables requires postgres store, got %s", store.Dialect())
	}
	for _, table := range []string{"platform_event_idempotency", "platform_events"} {
		if _, err := store.Exec(context.Background(), `DROP TABLE IF EXISTS `+table+` CASCADE`); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}
}
