package platform_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlatformRootTestFilesStayFocused(t *testing.T) {
	const maxTestLines = 800
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read platform package: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, "_test.go") {
			continue
		}
		lineCount, err := countPlatformRootFileLines(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		if lineCount > maxTestLines {
			t.Fatalf("%s has %d lines, want <= %d", name, lineCount, maxTestLines)
		}
	}
}

func countPlatformRootFileLines(path string) (int, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if len(content) == 0 {
		return 0, nil
	}
	return len(strings.Split(string(content), "\n")), nil
}
