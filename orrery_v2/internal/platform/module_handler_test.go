package platform_test

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/execution"
)

func TestBusinessModuleRegistersOutboxHandlerWithoutOutboxTypes(t *testing.T) {
	ctx := context.Background()
	cfg := handlerRuntimeConfig(t, "outbox-handler")
	delivered := make(chan platform.OutboxDelivery, 1)
	module := platform.ModuleFunc(func(ctx context.Context, registrar *platform.ModuleRegistrar) error {
		return registrar.RegisterOutboxDeliveryHandler("business.profile.created", func(ctx context.Context, delivery platform.OutboxDelivery) error {
			delivered <- delivery
			return nil
		})
	})

	rt, err := platform.OpenWithModules(ctx, cfg, module)
	if err != nil {
		t.Fatalf("OpenWithModules returned error: %v", err)
	}
	defer rt.Close()

	id, err := rt.OutboxPublisher.Publish(ctx, platform.OutboxMessage{Topic: "business.profile.created", Payload: []byte(`{"id":"profile-1"}`)})
	if err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}
	if err := rt.StartOutboxRelay(ctx, platform.OutboxRelayRuntimeConfig{PollInterval: time.Millisecond}); err != nil {
		t.Fatalf("StartOutboxRelay returned error: %v", err)
	}

	select {
	case delivery := <-delivered:
		if delivery.ID != id || delivery.Topic != "business.profile.created" || string(delivery.Payload) != `{"id":"profile-1"}` {
			t.Fatalf("delivery = %+v", delivery)
		}
	case <-time.After(time.Second):
		t.Fatalf("handler did not receive delivery")
	}
}

func TestBusinessModuleRegistersExecutionHandlerWithoutExecutionTypes(t *testing.T) {
	ctx := context.Background()
	cfg := handlerRuntimeConfig(t, "execution-handler")
	dispatched := make(chan platform.ExecutionDelivery, 1)
	module := platform.ModuleFunc(func(ctx context.Context, registrar *platform.ModuleRegistrar) error {
		return registrar.RegisterExecutionDeliveryHandler("business.worker.sync", func(ctx context.Context, delivery platform.ExecutionDelivery) error {
			dispatched <- delivery
			return nil
		})
	})

	rt, err := platform.OpenWithModules(ctx, cfg, module)
	if err != nil {
		t.Fatalf("OpenWithModules returned error: %v", err)
	}
	defer rt.Close()

	id, err := rt.ExecutionQueue.Submit(ctx, execution.Message{Topic: "business.worker.sync", Payload: []byte(`{"id":"task-1"}`), QueueKey: "discussion:group-1"})
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	if err := rt.StartExecutionRunner(ctx, platform.ExecutionRunnerRuntimeConfig{PollInterval: time.Millisecond}); err != nil {
		t.Fatalf("StartExecutionRunner returned error: %v", err)
	}

	select {
	case delivery := <-dispatched:
		if delivery.ID != id || delivery.Topic != "business.worker.sync" || delivery.QueueKey != "discussion:group-1" || string(delivery.Payload) != `{"id":"task-1"}` {
			t.Fatalf("delivery = %+v", delivery)
		}
	case <-time.After(time.Second):
		t.Fatalf("handler did not receive delivery")
	}
}

func TestExecutionDeliveryExtendsLeaseWithoutExecutionTypes(t *testing.T) {
	ctx := context.Background()
	cfg := handlerRuntimeConfig(t, "execution-lease-handler")
	now := time.Date(2026, 5, 31, 14, 0, 0, 0, time.UTC)
	fakeClock := clock.NewFake(now)
	processed := make(chan string, 1)
	module := platform.ModuleFunc(func(ctx context.Context, registrar *platform.ModuleRegistrar) error {
		if err := registrar.RegisterExecutionDeliveryHandler("business.long", func(ctx context.Context, delivery platform.ExecutionDelivery) error {
			if err := delivery.ExtendLease(ctx, now.Add(5*time.Minute)); err != nil {
				t.Fatalf("ExtendLease returned error: %v", err)
			}
			job, ok, err := registrar.ExecutionQueue().GetStatus(ctx, delivery.ID)
			if err != nil || !ok {
				t.Fatalf("get execution job during handler ok=%v err=%v", ok, err)
			}
			if !job.LeaseUntil.Equal(now.Add(5 * time.Minute)) {
				t.Fatalf("LeaseUntil = %s, want %s", job.LeaseUntil, now.Add(5*time.Minute))
			}
			processed <- delivery.ID
			return nil
		}); err != nil {
			return err
		}
		_, err := registrar.ExecutionQueue().Submit(ctx, execution.Message{Topic: "business.long"})
		return err
	})

	rt, err := platform.OpenWithOptionsAndModules(ctx, cfg, platform.RuntimeOptions{Clock: fakeClock}, module)
	if err != nil {
		t.Fatalf("OpenWithOptionsAndModules returned error: %v", err)
	}
	defer rt.Close()
	if err := rt.StartExecutionRunner(ctx, platform.ExecutionRunnerRuntimeConfig{WorkerID: "worker-1", PollInterval: time.Millisecond}); err != nil {
		t.Fatalf("StartExecutionRunner returned error: %v", err)
	}

	var id string
	select {
	case id = <-processed:
	case <-time.After(time.Second):
		t.Fatalf("execution delivery handler did not process job")
	}
	waitFor(t, time.Second, func() bool {
		status, ok, err := rt.ExecutionQueue.GetStatus(ctx, id)
		return err == nil && ok && status.Status == execution.StatusCompleted
	})
}

func handlerRuntimeConfig(t *testing.T, name string) config.Config {
	t.Helper()
	cfg := config.Config{Environment: "test"}
	cfg.DatabaseURLs = map[string]string{
		"core":          "memory://" + name + "-core",
		"events":        "memory://" + name + "-events",
		"jobs":          "memory://" + name + "-jobs",
		"runtime":       "memory://" + name + "-runtime",
		"observability": "memory://" + name + "-observability",
	}
	cfg.FileStore.Backend = "local"
	cfg.FileStore.Root = t.TempDir()
	cfg.Logging.Console = true
	return cfg
}
