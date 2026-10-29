package platform_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/idgen"
)

func TestBusinessModulePublishesOutboxJobThroughRegistrarPublisher(t *testing.T) {
	ctx := context.Background()
	fakeClock := clock.NewFake(time.Date(2026, 5, 30, 10, 0, 0, 0, time.UTC))
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := platformConfigFromYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "artifacts"), 0o755); err != nil {
		t.Fatalf("create artifacts dir: %v", err)
	}

	var publishedID string
	module := platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
		publisher := registry.OutboxPublisher()
		id, err := publisher.Publish(ctx, platform.OutboxMessage{
			Topic:          "business.profile.created",
			Payload:        []byte(`{"profile_id":"profile-1"}`),
			IdempotencyKey: "profile-1",
			Delay:          2 * time.Minute,
			MaxAttempts:    5,
		})
		if err != nil {
			return err
		}
		publishedID = id
		return nil
	})

	rt, err := platform.OpenWithOptionsAndModules(ctx, cfg, platform.RuntimeOptions{
		Clock:       fakeClock,
		IDGenerator: idgen.NewDeterministic("outbox"),
	}, module)
	if err != nil {
		t.Fatalf("OpenWithOptionsAndModules returned error: %v", err)
	}
	defer rt.Close()

	if publishedID != "outbox-000001" {
		t.Fatalf("published id = %q, want deterministic platform-generated id", publishedID)
	}
	job, ok := rt.OutboxPublisher.GetStatus(ctx, publishedID)
	if !ok {
		t.Fatalf("published outbox job %q was not stored", publishedID)
	}
	if job.Topic != "business.profile.created" {
		t.Fatalf("job topic = %q", job.Topic)
	}
	if string(job.Payload) != `{"profile_id":"profile-1"}` {
		t.Fatalf("job payload = %s", job.Payload)
	}
	if job.IdempotencyKey != "profile-1" {
		t.Fatalf("job idempotency key = %q", job.IdempotencyKey)
	}
	if job.MaxAttempts != 5 {
		t.Fatalf("job max attempts = %d, want 5", job.MaxAttempts)
	}
	if !job.NextAttemptAt.Equal(fakeClock.Now().Add(2 * time.Minute)) {
		t.Fatalf("job next attempt = %s, want fake clock plus delay", job.NextAttemptAt)
	}
}
