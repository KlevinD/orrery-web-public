package missioncontrol

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMissionControlProductionFilesStayFocused(t *testing.T) {
	const maxLines = 800
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if lines := countMissionControlFileLines(data); lines > maxLines {
			t.Fatalf("%s has %d lines, want <= %d; split mission control files by responsibility", name, lines, maxLines)
		}
	}
}

func TestMissionControlTestFilesStayFocused(t *testing.T) {
	const maxLines = 800
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if lines := countMissionControlFileLines(data); lines > maxLines {
			t.Fatalf("%s has %d lines, want <= %d; split mission control test files by responsibility", name, lines, maxLines)
		}
	}
}

func countMissionControlFileLines(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	lines := 1
	for _, b := range data {
		if b == '\n' {
			lines++
		}
	}
	return lines
}
