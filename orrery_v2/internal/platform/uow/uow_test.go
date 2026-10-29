package uow_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/eventstore"
	"orrery_v2/internal/platform/logging"
	"orrery_v2/internal/platform/outbox"
	"orrery_v2/internal/platform/uow"
)

func TestDoCommitsWhenCallbackSucceeds(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	defer store.Close()

	unit := uow.New(store)
	if err := unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		tx.Put("items", "ok", map[string]any{"id": "ok"})
		return nil
	}); err != nil {
		t.Fatalf("Do returned error: %v", err)
	}

	if _, ok := store.Get("items", "ok"); !ok {
		t.Fatalf("committed item not found")
	}
}

func TestListRecordsReadsOnlyTargetTableAcrossPages(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	defer store.Close()

	unit := uow.New(store)
	err := unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		for index := 0; index <= db.MaxRecordPageLimit; index++ {
			key := fmt.Sprintf("item-%04d", index)
			tx.Put("items", key, db.Record{"id": key})
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO platform_records (table_name, record_key, payload_json) VALUES (?, ?, ?)`, "aaa_unrelated", "broken", "{"); err != nil {
			return err
		}

		records, err := uow.ListRecords(ctx, tx, "items")
		if err != nil {
			return err
		}
		if len(records) != db.MaxRecordPageLimit+1 {
			t.Fatalf("record count = %d, want %d", len(records), db.MaxRecordPageLimit+1)
		}
		if records["item-0000"]["id"] != "item-0000" || records["item-1000"]["id"] != "item-1000" {
			t.Fatalf("record boundaries missing: first=%+v last=%+v", records["item-0000"], records["item-1000"])
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
}

func TestDoRollsBackWhenCallbackFails(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	defer store.Close()

	unit := uow.New(store)
	errBoom := errors.New("boom")
	err := unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		tx.Put("items", "rollback", map[string]any{"id": "rollback"})
		return errBoom
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("Do error = %v, want boom", err)
	}
	if _, ok := store.Get("items", "rollback"); ok {
		t.Fatalf("rolled back item was found")
	}
}

func TestDoRunsAfterCommitHooksOnlyAfterSuccessfulCommit(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	defer store.Close()

	unit := uow.New(store)
	var calls []string
	if err := unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		tx.Put("items", "ok", map[string]any{"id": "ok"})
		tx.AfterCommit(func(ctx context.Context) error {
			calls = append(calls, "published")
			if _, ok := store.Get("items", "ok"); !ok {
				t.Fatalf("after commit hook ran before transaction was visible")
			}
			return nil
		})
		return nil
	}); err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	if len(calls) != 1 || calls[0] != "published" {
		t.Fatalf("after commit calls = %+v, want one published call", calls)
	}
}

func TestDoSkipsAfterCommitHooksWhenTransactionRollsBack(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	defer store.Close()

	unit := uow.New(store)
	errBoom := errors.New("boom")
	called := false
	err := unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		tx.Put("items", "rollback", map[string]any{"id": "rollback"})
		tx.AfterCommit(func(ctx context.Context) error {
			called = true
			return nil
		})
		return errBoom
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("Do error = %v, want boom", err)
	}
	if called {
		t.Fatalf("after commit hook ran despite rollback")
	}
}

func TestDoReturnsAfterCommitErrorAfterTransactionIsCommitted(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	defer store.Close()

	unit := uow.New(store)
	errPublish := errors.New("publish failed")
	err := unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		tx.Put("items", "committed", map[string]any{"id": "committed"})
		tx.AfterCommit(func(ctx context.Context) error {
			return errPublish
		})
		return nil
	})
	if !errors.Is(err, errPublish) {
		t.Fatalf("Do error = %v, want publish failed", err)
	}
	if _, ok := store.Get("items", "committed"); !ok {
		t.Fatalf("record was not committed before after commit error")
	}
}

func TestDoBuildsTxScopedRepositoryFromFactory(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	defer store.Close()

	type itemRepo struct {
		tx uow.Tx
	}
	save := func(repo itemRepo, id string) {
		repo.tx.Put("items", id, map[string]any{"id": id})
	}
	buildRepo := uow.Factory[itemRepo](func(tx uow.Tx) itemRepo {
		return itemRepo{tx: tx}
	})

	unit := uow.New(store)
	if err := unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		repo := buildRepo(tx)
		save(repo, "factory-item")
		return nil
	}); err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	if record, ok := store.Get("items", "factory-item"); !ok || record["id"] != "factory-item" {
		t.Fatalf("factory-scoped repo record=%+v ok=%v", record, ok)
	}
}

func TestBeginBuildsTxScopedRepositoryAndRunsAfterCommitOnCommit(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	defer store.Close()

	type itemRepo struct {
		tx uow.Tx
	}
	save := func(repo itemRepo, id string) {
		repo.tx.Put("items", id, map[string]any{"id": id})
	}

	unit := uow.New(store)
	tx, err := unit.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin returned error: %v", err)
	}
	repo := uow.Factory[itemRepo](func(tx uow.Tx) itemRepo {
		return itemRepo{tx: tx}
	})(tx)
	save(repo, "explicit-item")
	afterCommitCalled := false
	tx.AfterCommit(func(ctx context.Context) error {
		afterCommitCalled = true
		if _, ok := store.Get("items", "explicit-item"); !ok {
			t.Fatalf("after commit hook ran before explicit transaction was visible")
		}
		return nil
	})
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit returned error: %v", err)
	}
	if !afterCommitCalled {
		t.Fatalf("after commit hook was not called")
	}
}

func TestBeginRollbackSkipsAfterCommitAndDiscardsWrites(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	defer store.Close()

	unit := uow.New(store)
	tx, err := unit.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin returned error: %v", err)
	}
	tx.Put("items", "rollback", map[string]any{"id": "rollback"})
	afterCommitCalled := false
	tx.AfterCommit(func(ctx context.Context) error {
		afterCommitCalled = true
		return nil
	})
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("Rollback returned error: %v", err)
	}
	if afterCommitCalled {
		t.Fatalf("after commit hook ran after rollback")
	}
	if _, ok := store.Get("items", "rollback"); ok {
		t.Fatalf("rolled back explicit transaction write was committed")
	}
}

func TestDoLogsRollbackErrorWithOperationCorrelationAndErrorCode(t *testing.T) {
	var logs bytes.Buffer
	ctx := logging.WithCorrelationID(context.Background(), "corr-uow")
	store := openStore(t)
	defer store.Close()

	unit := uow.New(store).WithLogger(logging.NewJSONLogger(&logs, logging.LevelDebug))
	errBoom := errors.New("boom")
	err := unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		tx.Put("items", "rollback", map[string]any{"id": "rollback"})
		return errBoom
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("Do error = %v, want boom", err)
	}
	got := logs.String()
	for _, want := range []string{`"operation":"uow.do"`, `"correlation_id":"corr-uow"`, `"error_code":"transaction_rollback"`, `"level":"ERROR"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("log missing %s in %s", want, got)
		}
	}
}

func TestDoPropagatesCommitFailure(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	defer store.Close()
	store.InjectReplaceError(errors.New("disk full"))
	unit := uow.New(store)

	err := unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		tx.Put("items", "x", map[string]any{"id": "x"})
		return nil
	})
	if err == nil || !errors.Is(err, db.ErrPersist) {
		t.Fatalf("Do error = %v, want persist error", err)
	}
	if _, ok := store.Get("items", "x"); ok {
		t.Fatalf("record committed despite persist failure")
	}
}

func TestDoCommitsBusinessEventAndOutboxAtomically(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	defer store.Close()

	unit := uow.New(store)
	err := unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		tx.Put("items", "item-1", map[string]any{"id": "item-1"})
		if err := eventstore.NewTx(tx).Append(ctx, eventstore.Envelope{
			EventID:       "evt-1",
			StreamID:      "stream-1",
			StreamVersion: 1,
			EventType:     "sample.created",
			EventVersion:  1,
		}); err != nil {
			return err
		}
		return outbox.NewTx(tx).Enqueue(ctx, outbox.Job{ID: "job-1", Topic: "projection"})
	})
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	if _, ok := store.Get("items", "item-1"); !ok {
		t.Fatalf("business record was not committed")
	}
	events, err := eventstore.New(store).Load(ctx, "stream-1")
	if err != nil || len(events) != 1 {
		t.Fatalf("events=%+v err=%v, want one committed event", events, err)
	}
	if job, ok := outbox.New(store).Get(ctx, "job-1"); !ok || job.ID != "job-1" {
		t.Fatalf("outbox job ok=%v job=%+v, want committed job", ok, job)
	}
}

func TestDoAllowsReadingEventAndOutboxWritesInsideSameTransaction(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	defer store.Close()

	unit := uow.New(store)
	err := unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		events := eventstore.NewTx(tx)
		if err := events.Append(ctx, eventstore.Envelope{
			EventID:       "evt-1",
			StreamID:      "stream-1",
			StreamVersion: 1,
			EventType:     "sample.created",
			EventVersion:  1,
		}); err != nil {
			return err
		}
		loaded, err := events.Load(ctx, "stream-1")
		if err != nil {
			return err
		}
		if len(loaded) != 1 || loaded[0].EventID != "evt-1" {
			t.Fatalf("tx events = %+v, want evt-1 visible before commit", loaded)
		}

		jobs := outbox.NewTx(tx)
		if err := jobs.Enqueue(ctx, outbox.Job{ID: "job-1", Topic: "projection"}); err != nil {
			return err
		}
		job, ok := jobs.Get(ctx, "job-1")
		if !ok || job.ID != "job-1" || job.Topic != "projection" {
			t.Fatalf("tx outbox job ok=%v job=%+v, want job visible before commit", ok, job)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
}

func TestTxOutboxUsesInjectedClockWhenNextAttemptAtMissing(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	defer store.Close()
	now := time.Date(2026, 5, 31, 14, 30, 0, 0, time.UTC)

	err := uow.New(store).Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		return outbox.NewTxWithOptions(tx, outbox.StoreOptions{
			Clock: clock.NewFake(now),
		}).Enqueue(ctx, outbox.Job{ID: "job-1", Topic: "projection"})
	})
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}

	job, ok := outbox.New(store).Get(ctx, "job-1")
	if !ok {
		t.Fatalf("Get returned no job")
	}
	if !job.NextAttemptAt.Equal(now) {
		t.Fatalf("NextAttemptAt = %s, want injected clock %s", job.NextAttemptAt, now)
	}
}

func TestDoProvidesTxScopedEventStoreAndOutboxWithUnitClock(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	defer store.Close()
	now := time.Date(2026, 5, 31, 15, 45, 0, 0, time.UTC)

	err := uow.New(store).WithClock(clock.NewFake(now)).Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := tx.EventStore().Append(ctx, eventstore.Envelope{
			EventID:       "evt-facade",
			StreamID:      "stream-facade",
			StreamVersion: 1,
			EventType:     "sample.created",
			EventVersion:  1,
		}); err != nil {
			return err
		}
		return tx.Outbox().Enqueue(ctx, outbox.Job{ID: "job-facade", Topic: "projection"})
	})
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}

	events, err := eventstore.New(store).Load(ctx, "stream-facade")
	if err != nil {
		t.Fatalf("load events: %v", err)
	}
	if len(events) != 1 || !events[0].CreatedAt.Equal(now) {
		t.Fatalf("events = %+v, want CreatedAt %s", events, now)
	}
	job, ok := outbox.New(store).Get(ctx, "job-facade")
	if !ok || !job.NextAttemptAt.Equal(now) {
		t.Fatalf("outbox job=%+v ok=%v, want NextAttemptAt %s", job, ok, now)
	}
}

func TestDoProvidesTxScopedOutboxPublisherWithUnitClockAndIDGenerator(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	defer store.Close()
	now := time.Date(2026, 5, 31, 16, 30, 0, 0, time.UTC)

	var jobID string
	err := uow.New(store).
		WithClock(clock.NewFake(now)).
		WithIDGenerator(testIDGenerator{next: "tx-job-1"}).
		Do(ctx, func(ctx context.Context, tx uow.Tx) error {
			var err error
			jobID, err = tx.PublishOutbox(ctx, uow.OutboxMessage{
				Topic:          "projection.tx",
				Payload:        []byte(`{"tx":true}`),
				IdempotencyKey: "tx-1",
				Delay:          5 * time.Minute,
				MaxAttempts:    4,
			})
			return err
		})
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	if jobID != "tx-job-1" {
		t.Fatalf("job id = %q, want generated tx-job-1", jobID)
	}
	job, ok := outbox.New(store).Get(ctx, jobID)
	if !ok {
		t.Fatalf("outbox job %q not found", jobID)
	}
	if job.Topic != "projection.tx" || string(job.Payload) != `{"tx":true}` || job.IdempotencyKey != "tx-1" {
		t.Fatalf("job = %+v", job)
	}
	if job.MaxAttempts != 4 {
		t.Fatalf("job MaxAttempts = %d, want 4", job.MaxAttempts)
	}
	if !job.NextAttemptAt.Equal(now.Add(5 * time.Minute)) {
		t.Fatalf("job NextAttemptAt = %s, want %s", job.NextAttemptAt, now.Add(5*time.Minute))
	}
}

func TestDoProvidesTxScopedEventRecorderWithUnitClockAndIDGenerator(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	defer store.Close()
	now := time.Date(2026, 5, 31, 20, 30, 0, 0, time.UTC)

	var eventID string
	err := uow.New(store).
		WithClock(clock.NewFake(now)).
		WithIDGenerator(testIDGenerator{next: "tx-event-1"}).
		Do(ctx, func(ctx context.Context, tx uow.Tx) error {
			var err error
			eventID, err = tx.RecordEvent(ctx, uow.EventMessage{
				StreamID:       "stream-tx-recorder",
				StreamVersion:  1,
				EventType:      "sample.tx.recorded",
				EventVersion:   1,
				Payload:        []byte(`{"tx":true}`),
				IdempotencyKey: "tx-event-idem",
			})
			return err
		})
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	if eventID != "tx-event-1" {
		t.Fatalf("event id = %q, want generated tx-event-1", eventID)
	}
	events, err := eventstore.New(store).Load(ctx, "stream-tx-recorder")
	if err != nil {
		t.Fatalf("load events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %+v, want one recorded event", events)
	}
	event := events[0]
	if event.EventID != eventID || event.EventType != "sample.tx.recorded" || string(event.Payload) != `{"tx":true}` {
		t.Fatalf("event = %+v", event)
	}
	if !event.CreatedAt.Equal(now) {
		t.Fatalf("CreatedAt = %s, want %s", event.CreatedAt, now)
	}
}

func TestDoRollsBackBusinessEventAndOutboxTogether(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	defer store.Close()

	unit := uow.New(store)
	errBoom := errors.New("boom")
	err := unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		tx.Put("items", "item-1", map[string]any{"id": "item-1"})
		if err := eventstore.NewTx(tx).Append(ctx, eventstore.Envelope{
			EventID:       "evt-1",
			StreamID:      "stream-1",
			StreamVersion: 1,
			EventType:     "sample.created",
			EventVersion:  1,
		}); err != nil {
			return err
		}
		if err := outbox.NewTx(tx).Enqueue(ctx, outbox.Job{ID: "job-1", Topic: "projection"}); err != nil {
			return err
		}
		return errBoom
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("Do error = %v, want boom", err)
	}
	if _, ok := store.Get("items", "item-1"); ok {
		t.Fatalf("business record was committed after rollback")
	}
	events, err := eventstore.New(store).Load(ctx, "stream-1")
	if err != nil || len(events) != 0 {
		t.Fatalf("events=%+v err=%v, want rollback", events, err)
	}
	if job, ok := outbox.New(store).Get(ctx, "job-1"); ok {
		t.Fatalf("outbox job committed after rollback: %+v", job)
	}
}

func TestPostgresUnitOfWorkCommitsAndRollsBackEventRecords(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{
		Driver: db.DriverPostgres,
		URL:    postgresTestURL(t),
		Name:   "postgres-uow",
	})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	dropUOWTables(t, store)
	unit := uow.New(store)

	if err := unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		tx.Put("items", "committed", db.Record{"id": "committed"})
		if err := tx.EventStore().Append(ctx, eventstore.Envelope{
			EventID:       "evt-postgres-uow-1",
			StreamID:      "stream-postgres-uow",
			StreamVersion: 1,
			EventType:     "sample.created",
			EventVersion:  1,
		}); err != nil {
			return err
		}
		return tx.Outbox().Enqueue(ctx, outbox.Job{
			ID:             "job-postgres-uow-1",
			Topic:          "projection",
			IdempotencyKey: "job-postgres-uow-idem",
			Payload:        []byte(`{"source":"postgres-uow"}`),
		})
	}); err != nil {
		t.Fatalf("Do commit returned error: %v", err)
	}
	if record, ok, err := store.GetRecord(ctx, "items", "committed"); err != nil || !ok || record["id"] != "committed" {
		t.Fatalf("committed record=%+v ok=%v err=%v", record, ok, err)
	}
	events, err := eventstore.New(store).Load(ctx, "stream-postgres-uow")
	if err != nil || len(events) != 1 {
		t.Fatalf("committed events=%+v err=%v", events, err)
	}
	job, ok := outbox.New(store).Get(ctx, "job-postgres-uow-1")
	if !ok || job.Topic != "projection" || string(job.Payload) != `{"source":"postgres-uow"}` {
		t.Fatalf("committed outbox job ok=%v job=%+v", ok, job)
	}

	errBoom := errors.New("rollback")
	err = unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		tx.Put("items", "rolled-back", db.Record{"id": "rolled-back"})
		if err := tx.EventStore().Append(ctx, eventstore.Envelope{
			EventID:       "evt-postgres-uow-2",
			StreamID:      "stream-postgres-uow-rollback",
			StreamVersion: 1,
			EventType:     "sample.created",
			EventVersion:  1,
		}); err != nil {
			return err
		}
		if err := tx.Outbox().Enqueue(ctx, outbox.Job{
			ID:             "job-postgres-uow-rollback",
			Topic:          "projection",
			IdempotencyKey: "job-postgres-uow-rollback-idem",
		}); err != nil {
			return err
		}
		return errBoom
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("Do rollback error = %v, want rollback", err)
	}
	if _, ok, err := store.GetRecord(ctx, "items", "rolled-back"); err != nil || ok {
		t.Fatalf("rolled back record ok=%v err=%v", ok, err)
	}
	events, err = eventstore.New(store).Load(ctx, "stream-postgres-uow-rollback")
	if err != nil || len(events) != 0 {
		t.Fatalf("rolled back events=%+v err=%v, want none", events, err)
	}
	if job, ok := outbox.New(store).Get(ctx, "job-postgres-uow-rollback"); ok {
		t.Fatalf("rolled back outbox job committed: %+v", job)
	}
}

func TestConcurrentUnitOfWorkWritesDoNotLoseCommittedRecords(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	defer store.Close()
	unit := uow.New(store)

	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		i := i
		go func() {
			errs <- unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
				tx.Put("items", fmt.Sprintf("item-%02d", i), map[string]any{"id": i})
				return nil
			})
		}()
	}
	for i := 0; i < 10; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("Do returned error: %v", err)
		}
	}
	for i := 0; i < 10; i++ {
		if _, ok := store.Get("items", fmt.Sprintf("item-%02d", i)); !ok {
			t.Fatalf("missing committed item %02d", i)
		}
	}
}

func TestConcurrentUnitOfWorkWritesPreserveEveryCommittedRecord(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	defer store.Close()
	unit := uow.New(store)

	const writes = 100
	var ready sync.WaitGroup
	ready.Add(writes)
	start := make(chan struct{})
	errs := make(chan error, writes)

	for i := 0; i < writes; i++ {
		i := i
		go func() {
			ready.Done()
			<-start
			errs <- unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
				time.Sleep(2 * time.Millisecond)
				tx.Put("items", fmt.Sprintf("concurrent-%03d", i), map[string]any{"id": i})
				return nil
			})
		}()
	}
	ready.Wait()
	close(start)

	for i := 0; i < writes; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("Do returned error: %v", err)
		}
	}
	for i := 0; i < writes; i++ {
		if _, ok := store.Get("items", fmt.Sprintf("concurrent-%03d", i)); !ok {
			t.Fatalf("missing committed item %03d", i)
		}
	}
}

func openStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(context.Background(), db.Config{Driver: db.DriverMemory, Name: "stage0"})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	return store
}

type testIDGenerator struct {
	next string
}

func (g testIDGenerator) NewIDContext() (string, error) {
	return g.next, nil
}

func postgresTestURL(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("ORRERY_TEST_POSTGRES_URL")
	if raw == "" {
		t.Skip("ORRERY_TEST_POSTGRES_URL is not set")
	}
	return raw
}

func dropUOWTables(t *testing.T, store *db.Store) {
	t.Helper()
	if store.Dialect() != db.DialectPostgres {
		t.Fatalf("dropUOWTables requires postgres store, got %s", store.Dialect())
	}
	for _, table := range []string{"platform_outbox_jobs", "platform_event_idempotency", "platform_events", "platform_records"} {
		if _, err := store.Exec(context.Background(), `DROP TABLE IF EXISTS `+table+` CASCADE`); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}
	if _, err := store.Exec(context.Background(), `
CREATE TABLE IF NOT EXISTS platform_records (
	table_name TEXT NOT NULL,
	record_key TEXT NOT NULL,
	payload_json TEXT NOT NULL,
	PRIMARY KEY (table_name, record_key)
)`); err != nil {
		t.Fatalf("recreate platform_records: %v", err)
	}
}
