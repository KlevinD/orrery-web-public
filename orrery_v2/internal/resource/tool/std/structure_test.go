package std

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStdToolProductionCodeUsesStandardResourceConcepts(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read std tool package: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(".", entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(data)
		for _, forbidden := range []string{"Builtin", "builtin", "built-in"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s contains %q; std tools must be managed as standard resources", path, forbidden)
			}
		}
	}
}
