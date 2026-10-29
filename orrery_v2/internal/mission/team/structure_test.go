package team

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMissionTeamServiceFilesStayFocused(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read team dir: %v", err)
	}
	var oversized []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "service") || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		lineCount, err := countTeamFileLines(filepath.Join(".", entry.Name()))
		if err != nil {
			t.Fatalf("count %s: %v", entry.Name(), err)
		}
		if lineCount > 360 {
			oversized = append(oversized, entry.Name())
		}
	}
	if len(oversized) > 0 {
		t.Fatalf("mission team service files exceed 360 lines: %v", oversized)
	}
}

func countTeamFileLines(path string) (int, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if len(content) == 0 {
		return 0, nil
	}
	return len(strings.Split(string(content), "\n")), nil
}
