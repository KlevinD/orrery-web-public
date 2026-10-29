package resourceadmin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResourceAdminTestFilesStayFocused(t *testing.T) {
	assertGoFilesUnderLineLimit(t, func(entry os.DirEntry) bool {
		return !entry.IsDir() && strings.HasSuffix(entry.Name(), "_test.go")
	}, 800, "resourceadmin test files exceed 800 lines")
}

func TestResourceAdminQueryFilesStayFocused(t *testing.T) {
	assertGoFilesUnderLineLimit(t, func(entry os.DirEntry) bool {
		return !entry.IsDir() && strings.HasPrefix(entry.Name(), "query_") && strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go")
	}, 360, "resourceadmin query files exceed 360 lines")
}

func assertGoFilesUnderLineLimit(t *testing.T, include func(os.DirEntry) bool, limit int, message string) {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read resourceadmin dir: %v", err)
	}
	var oversized []string
	for _, entry := range entries {
		if !include(entry) {
			continue
		}
		lineCount, err := countFileLines(filepath.Join(".", entry.Name()))
		if err != nil {
			t.Fatalf("count %s: %v", entry.Name(), err)
		}
		if lineCount > limit {
			oversized = append(oversized, entry.Name())
		}
	}
	if len(oversized) > 0 {
		t.Fatalf("%s: %v", message, oversized)
	}
}

func countFileLines(path string) (int, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if len(content) == 0 {
		return 0, nil
	}
	return len(strings.Split(string(content), "\n")), nil
}
