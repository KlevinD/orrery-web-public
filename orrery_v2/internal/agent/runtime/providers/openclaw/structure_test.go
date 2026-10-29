package openclaw

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenClawProviderFilesStayFocused(t *testing.T) {
	const maxLines = 320
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read openclaw provider dir: %v", err)
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
		if lines := strings.Count(string(data), "\n"); lines > maxLines {
			t.Fatalf("%s has %d lines, want <= %d; split OpenClaw provider code by transport, parser, config, and version responsibilities", name, lines, maxLines)
		}
	}
}
