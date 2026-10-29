package team

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMissionTeamFixturesUseNeutralStaffingActors(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatalf("glob team tests: %v", err)
	}
	for _, file := range files {
		if file == "role_test.go" || file == "fixture_identity_test.go" {
			continue
		}
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		text := string(body)
		for _, forbidden := range []string{"hr-001", "pm-001"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s should use neutral staffing actor fixtures instead of %q", file, forbidden)
			}
		}
	}
}
