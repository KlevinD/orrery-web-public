package core_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentCoreDoesNotImportMissionTask(t *testing.T) {
	root := "."
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imported := range file.Imports {
			if strings.Contains(imported.Path.Value, "orrery_v2/internal/mission/task") {
				t.Fatalf("%s imports mission/task; keep task-specific adapters under agent/component/*", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("inspect agent core package: %v", err)
	}
}
