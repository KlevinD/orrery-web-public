package platform_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"orrery_v2/internal/platform"
)

func TestBusinessModuleRegistersConfigSectionWithoutConfigTypes(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	appendBusinessConfigSection(t, configPath, `
business:
  enabled: true
  max_parallel_tasks: 3
`)
	validated := make(chan int, 1)
	module := platform.ModuleFunc(func(ctx context.Context, registrar *platform.ModuleRegistrar) error {
		return registrar.RegisterConfigValidator("business", func(section platform.ConfigSection) error {
			var business struct {
				Enabled          bool `yaml:"enabled"`
				MaxParallelTasks int  `yaml:"max_parallel_tasks"`
			}
			if err := section.Decode(&business); err != nil {
				return err
			}
			if !business.Enabled {
				return section.Invalid("enabled must be true")
			}
			if business.MaxParallelTasks <= 0 {
				return section.Invalid("max_parallel_tasks must be positive")
			}
			validated <- business.MaxParallelTasks
			return nil
		})
	})

	rt, err := platform.OpenFromYAMLWithModules(ctx, configPath, module)
	if err != nil {
		t.Fatalf("OpenFromYAMLWithModules returned error: %v", err)
	}
	defer rt.Close()

	select {
	case got := <-validated:
		if got != 3 {
			t.Fatalf("validated max_parallel_tasks = %d, want 3", got)
		}
	default:
		t.Fatalf("business config validator was not called")
	}
}

func appendBusinessConfigSection(t *testing.T, path, content string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open yaml for append: %v", err)
	}
	defer file.Close()
	if _, err := file.WriteString(content); err != nil {
		t.Fatalf("append yaml: %v", err)
	}
}
