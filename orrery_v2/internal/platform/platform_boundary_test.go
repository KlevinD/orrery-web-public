package platform_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPlatformDoesNotImportBusinessPackages(t *testing.T) {
	forbidden := []string{
		`"orrery_v2/internal/agent`,
		`"orrery_v2/internal/mission`,
		`"orrery_v2/internal/app`,
		`"orrery_v2/internal/transport`,
	}
	root := "."
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imported := range file.Imports {
			for _, forbiddenImport := range forbidden {
				if strings.HasPrefix(imported.Path.Value, forbiddenImport) {
					t.Fatalf("%s imports forbidden business package prefix %s", path, forbiddenImport)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk platform: %v", err)
	}
}

func TestExecutionInfrastructureIsNotSplitIntoLegacyPackages(t *testing.T) {
	for _, dir := range []string{"queue", "claim", "liveness", "sweeper"} {
		if _, err := os.Stat(filepath.Join(".", dir)); err == nil {
			t.Fatalf("execution infrastructure should live in internal/platform/execution, found legacy package directory %s", dir)
		} else if !os.IsNotExist(err) {
			t.Fatalf("inspect legacy package directory %s: %v", dir, err)
		}
	}
}

func TestPlatformRootPackageFilesStayWithinBootstrapAllowlist(t *testing.T) {
	allowed := []string{
		"artifacts.go",
		"config_section.go",
		"database.go",
		"event_recorder.go",
		"handlers.go",
		"initializer.go",
		"locks.go",
		"metric_recorder.go",
		"metric_relay.go",
		"metrics_handler.go",
		"module.go",
		"outbox_publisher.go",
		"readiness.go",
		"runtime.go",
		"runtime_config.go",
		"runtime_metrics_server.go",
		"runtime_reload.go",
		"runtime_workers.go",
		"secrets.go",
		"tracer.go",
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read platform root: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if !slices.Contains(allowed, name) {
			t.Fatalf("new platform root file %s is not allowed; add new infrastructure under internal/platform/<capability> and keep root as bootstrap wiring only", name)
		}
	}
}
