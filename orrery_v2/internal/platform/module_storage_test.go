package platform_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/eventstore"
	"orrery_v2/internal/platform/idgen"
	"orrery_v2/internal/platform/outbox"
)

func TestBusinessModuleUsesRegistrarUnitOfWorkTxStores(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	now := time.Date(2026, 5, 31, 18, 20, 0, 0, time.UTC)
	var eventID string
	var jobID string
	module := platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
		return registry.UnitOfWork().Do(ctx, func(ctx context.Context, tx platform.Tx) error {
			var err error
			eventID, err = tx.RecordEvent(ctx, platform.TxEventMessage{
				StreamID:      "business-uow-stream-1",
				StreamVersion: 1,
				EventType:     "business.uow.created",
				EventVersion:  1,
			})
			if err != nil {
				return err
			}
			jobID, err = tx.PublishOutbox(ctx, platform.TxOutboxMessage{Topic: "business.uow.created"})
			return err
		})
	})

	rt, err := platform.OpenWithOptionsAndModules(ctx, cfg, platform.RuntimeOptions{
		Clock:       clock.NewFake(now),
		IDGenerator: idgen.NewDeterministic("uow"),
	}, module)
	if err != nil {
		t.Fatalf("OpenWithOptionsAndModules returned error: %v", err)
	}
	defer rt.Close()

	if eventID != "uow-000001" || jobID != "uow-000002" {
		t.Fatalf("generated ids event=%q job=%q, want deterministic platform IDs", eventID, jobID)
	}
	events, err := eventstore.New(mustStore(t, rt.Databases, db.RoleCore)).Load(ctx, "business-uow-stream-1")
	if err != nil {
		t.Fatalf("load tx events from core db: %v", err)
	}
	if len(events) != 1 || events[0].EventID != eventID || !events[0].CreatedAt.Equal(now) {
		t.Fatalf("events = %+v, want CreatedAt %s", events, now)
	}
	job, ok := outbox.New(mustStore(t, rt.Databases, db.RoleCore)).Get(ctx, jobID)
	if !ok || !job.NextAttemptAt.Equal(now) {
		t.Fatalf("outbox job=%+v ok=%v, want NextAttemptAt %s", job, ok, now)
	}
}

func TestBusinessModuleUsesRegistrarRoutedUnitOfWorkExplicitTransaction(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	module := platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
		tx, err := registry.RoutedUOW().Begin(ctx)
		if err != nil {
			return err
		}
		if err := tx.PutRecord(ctx, "events.business_audit", "event-1", db.Record{"kind": "business"}); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		return tx.Commit(ctx)
	})

	rt, err := platform.OpenWithModules(ctx, cfg, module)
	if err != nil {
		t.Fatalf("OpenWithModules returned error: %v", err)
	}
	defer rt.Close()

	record, ok := mustStore(t, rt.Databases, db.RoleEvents).Get("business_audit", "event-1")
	if !ok || record["kind"] != "business" {
		t.Fatalf("events database record=%+v ok=%v, want business audit record", record, ok)
	}
	if _, ok := mustStore(t, rt.Databases, db.RoleCore).Get("business_audit", "event-1"); ok {
		t.Fatalf("business audit record leaked into core database")
	}
}

func TestBusinessModulePublishesOutboxInsideRegistrarUnitOfWork(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}
	now := time.Date(2026, 5, 31, 17, 0, 0, 0, time.UTC)

	var publishedID string
	module := platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
		return registry.UnitOfWork().Do(ctx, func(ctx context.Context, tx platform.Tx) error {
			var err error
			publishedID, err = tx.PublishOutbox(ctx, platform.TxOutboxMessage{
				Topic:          "business.uow.published",
				Payload:        []byte(`{"from":"uow"}`),
				IdempotencyKey: "uow-1",
				Delay:          time.Minute,
			})
			return err
		})
	})

	rt, err := platform.OpenWithOptionsAndModules(ctx, cfg, platform.RuntimeOptions{
		Clock:       clock.NewFake(now),
		IDGenerator: idgen.NewDeterministic("uow-job"),
	}, module)
	if err != nil {
		t.Fatalf("OpenWithOptionsAndModules returned error: %v", err)
	}
	defer rt.Close()

	if publishedID != "uow-job-000001" {
		t.Fatalf("published id = %q, want platform-generated id", publishedID)
	}
	job, ok := outbox.New(mustStore(t, rt.Databases, db.RoleCore)).Get(ctx, publishedID)
	if !ok {
		t.Fatalf("published job %q not found", publishedID)
	}
	if job.Topic != "business.uow.published" || string(job.Payload) != `{"from":"uow"}` || job.IdempotencyKey != "uow-1" {
		t.Fatalf("job = %+v", job)
	}
	if !job.NextAttemptAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("job NextAttemptAt = %s, want %s", job.NextAttemptAt, now.Add(time.Minute))
	}
}
