package httptransport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTransportHTTPDoesNotImportForbiddenInfrastructure(t *testing.T) {
	root := "."
	forbidden := []string{
		"internal/" + "repository",
		"pkg/" + "db",
		"internal/mission/" + "event",
		"internal/mission/" + "projection",
		"internal/agent/" + "runtime",
		"agent_task" + "_queue",
	}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		content := string(data)
		for _, needle := range forbidden {
			if strings.Contains(content, needle) {
				t.Fatalf("%s contains forbidden dependency %q", path, needle)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
