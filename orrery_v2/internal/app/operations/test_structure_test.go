package operations_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOperationsTestFilesStayFocused(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read operations dir: %v", err)
	}
	var oversized []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		lineCount, err := countOperationsTestFileLines(filepath.Join(".", entry.Name()))
		if err != nil {
			t.Fatalf("count %s: %v", entry.Name(), err)
		}
		if lineCount > 800 {
			oversized = append(oversized, entry.Name())
		}
	}
	if len(oversized) > 0 {
		t.Fatalf("operations test files exceed 800 lines: %v", oversized)
	}
}

func countOperationsTestFileLines(path string) (int, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if len(content) == 0 {
		return 0, nil
	}
	return len(strings.Split(string(content), "\n")), nil
}
