package platform_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/idgen"
)

func TestBusinessModuleStoresArtifactsThroughRegistrarArtifactStore(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	now := time.Date(2026, 5, 31, 23, 30, 0, 0, time.UTC)
	ids := idgen.NewDeterministic("artifact")
	storedID := make(chan string, 1)
	module := platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
		ref, err := registry.Artifacts().Put(ctx, "report.txt", "text/plain", strings.NewReader("hello"))
		if err != nil {
			return err
		}
		if ref.LocalPath != "" {
			t.Fatalf("artifact ref leaked local path: %+v", ref)
		}
		if ref.ID != "artifact-000001" || !ref.CreatedAt.Equal(now) {
			t.Fatalf("artifact ref = %+v, want platform id and clock", ref)
		}
		storedID <- ref.ID
		return nil
	})

	rt, err := platform.OpenWithOptionsAndModules(ctx, cfg, platform.RuntimeOptions{
		Clock:       clock.NewFake(now),
		IDGenerator: ids,
	}, module)
	if err != nil {
		t.Fatalf("OpenWithOptionsAndModules returned error: %v", err)
	}
	defer rt.Close()

	id := <-storedID
	artifact, err := rt.Artifacts.Get(ctx, id)
	if err != nil {
		t.Fatalf("get stored artifact: %v", err)
	}
	if string(artifact.Content) != "hello" || artifact.Ref.ID != id {
		t.Fatalf("artifact = %+v, want stored content", artifact)
	}
}
