package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProductionPiExecutableUsesStableBaseDirectoryRuntime(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "runtime", "pi-coding-agent", "node_modules", ".bin", "pi")
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("test"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ORRERY_BASE_DIR", root)
	if got := productionPiExecutable(); got != executable {
		t.Fatalf("production Pi executable = %q, want %q", got, executable)
	}
}

func TestProductionPiExecutableFallsBackWhenRuntimeIsAbsent(t *testing.T) {
	t.Setenv("ORRERY_BASE_DIR", t.TempDir())
	if got := productionPiExecutable(); got != "" {
		t.Fatalf("missing production Pi executable = %q", got)
	}
}
