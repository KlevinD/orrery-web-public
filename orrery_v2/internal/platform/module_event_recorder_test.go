package platform_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/idgen"
)

func TestBusinessModuleRecordsEventThroughRegistrarRecorder(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "artifacts"), 0o755); err != nil {
		t.Fatalf("create artifacts dir: %v", err)
	}
	now := time.Date(2026, 5, 31, 20, 0, 0, 0, time.UTC)

	var eventID string
	module := platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
		var err error
		eventID, err = registry.EventRecorder().Record(ctx, platform.EventMessage{
			StreamID:       "business-stream-recorder",
			StreamVersion:  1,
			EventType:      "business.profile.recorded",
			EventVersion:   1,
			Payload:        []byte(`{"profile":"p1"}`),
			IdempotencyKey: "profile-p1-recorded",
		})
		return err
	})

	rt, err := platform.OpenWithOptionsAndModules(ctx, cfg, platform.RuntimeOptions{
		Clock:       clock.NewFake(now),
		IDGenerator: idgen.NewDeterministic("evt"),
	}, module)
	if err != nil {
		t.Fatalf("OpenWithOptionsAndModules returned error: %v", err)
	}
	defer rt.Close()

	if eventID != "evt-000001" {
		t.Fatalf("event id = %q, want platform-generated id", eventID)
	}
	events, err := rt.EventRecorder.Load(ctx, "business-stream-recorder")
	if err != nil {
		t.Fatalf("load events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %+v, want one recorded event", events)
	}
	event := events[0]
	if event.EventID != eventID || event.EventType != "business.profile.recorded" || string(event.Payload) != `{"profile":"p1"}` {
		t.Fatalf("event = %+v", event)
	}
	if !event.CreatedAt.Equal(now) {
		t.Fatalf("CreatedAt = %s, want runtime clock %s", event.CreatedAt, now)
	}
	coreDB, ok := rt.Database(platform.DatabaseRoleCore)
	if !ok {
		t.Fatalf("core database not available")
	}
	exists, err := coreDB.TableExists(ctx, "platform_events")
	if err != nil {
		t.Fatalf("check core events table: %v", err)
	}
	if exists {
		t.Fatalf("platform_events table exists in core database, want recorder routed to events db only")
	}
}
