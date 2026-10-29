package platform_test

import (
	"context"
	"path/filepath"
	"testing"

	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/config"
)

func TestBusinessModuleRegistersReadyProbeWithoutHealthTypes(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	module := platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
		return registry.RegisterReadyProbe("business.index", func(ctx context.Context) platform.ReadyStatus {
			return platform.ReadyStatus{OK: false, Message: "index warming"}
		})
	})

	rt, err := platform.OpenWithModules(ctx, cfg, module)
	if err != nil {
		t.Fatalf("OpenWithModules returned error: %v", err)
	}
	defer rt.Close()

	ready := rt.Ready(ctx)
	check, ok := findPlatformReadyCheck(ready, "business.index")
	if !ok {
		t.Fatalf("ready report missing business.index: %+v", ready)
	}
	if check.OK || check.Message != "index warming" {
		t.Fatalf("business.index check = %+v, want not ready with message", check)
	}
}

func findPlatformReadyCheck(report platform.ReadyReport, name string) (platform.ReadyCheck, bool) {
	for _, check := range report.Checks {
		if check.Name == name {
			return check, true
		}
	}
	return platform.ReadyCheck{}, false
}
