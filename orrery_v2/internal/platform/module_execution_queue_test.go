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
	"orrery_v2/internal/platform/execution"
	"orrery_v2/internal/platform/idgen"
)

func TestBusinessModuleSubmitsExecutionJobThroughRegistrarQueue(t *testing.T) {
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
	now := time.Date(2026, 5, 31, 19, 0, 0, 0, time.UTC)

	var submittedID string
	module := platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
		var err error
		submittedID, err = registry.ExecutionQueue().Submit(ctx, execution.Message{
			Topic:       "business.worker.sync",
			Payload:     []byte(`{"worker":"sync"}`),
			Priority:    7,
			MaxAttempts: 4,
		})
		return err
	})

	rt, err := platform.OpenWithOptionsAndModules(ctx, cfg, platform.RuntimeOptions{
		Clock:       clock.NewFake(now),
		IDGenerator: idgen.NewDeterministic("exec"),
	}, module)
	if err != nil {
		t.Fatalf("OpenWithOptionsAndModules returned error: %v", err)
	}
	defer rt.Close()

	if submittedID != "exec-000001" {
		t.Fatalf("submitted id = %q, want platform-generated id", submittedID)
	}
	job, ok, err := rt.ExecutionQueue.GetStatus(ctx, submittedID)
	if err != nil {
		t.Fatalf("GetStatus returned error: %v", err)
	}
	if !ok {
		t.Fatalf("submitted execution job %q not found", submittedID)
	}
	if job.Topic != "business.worker.sync" || string(job.Payload) != `{"worker":"sync"}` {
		t.Fatalf("job = %+v", job)
	}
	if job.Priority != 7 || job.MaxAttempts != 4 {
		t.Fatalf("job priority/max attempts = %d/%d, want 7/4", job.Priority, job.MaxAttempts)
	}
}
