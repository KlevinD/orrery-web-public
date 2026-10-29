package operations

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOperationsProductionFilesStayFocused(t *testing.T) {
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
		if lines := countOperationFileLines(data); lines > maxLines {
			t.Fatalf("%s has %d lines, want <= %d; split large operations files by responsibility", name, lines, maxLines)
		}
	}
}

func TestOperationsCommandServiceDoesNotExposeWorkforceDelete(t *testing.T) {
	for _, name := range []string{"command_workforce.go", "service.go"} {
		data, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(data), "func (s *CommandService) DeleteWorkforceMember") {
			t.Fatalf("%s exposes CommandService.DeleteWorkforceMember; workforce members must be released to talent market instead", name)
		}
		if strings.Contains(string(data), "func (s *Service) DeleteWorkforceMember") {
			t.Fatalf("%s exposes Service.DeleteWorkforceMember; workforce members must be released to talent market instead", name)
		}
	}
}

func TestOperationsQueryFilesStayFocused(t *testing.T) {
	for _, name := range []string{"query_service.go", "query_permissions.go"} {
		if _, err := os.Stat(filepath.Clean(name)); err != nil {
			t.Fatalf("operations query responsibility file %s is required: %v", name, err)
		}
		data, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if lines := countOperationFileLines(data); lines > 650 {
			t.Fatalf("%s has %d lines, want <= 650; split workforce, permission, and organization query responsibilities", name, lines)
		}
	}
}

func TestOperationsWorkforceQueryProjectionIsSplitFromService(t *testing.T) {
	for _, name := range []string{"query_workforce_dto.go", "query_workforce_member_mapper.go"} {
		if _, err := os.Stat(filepath.Clean(name)); err != nil {
			t.Fatalf("operations workforce query file %s is required: %v", name, err)
		}
	}
	data, err := os.ReadFile(filepath.Clean("query_service.go"))
	if err != nil {
		t.Fatalf("read query_service.go: %v", err)
	}
	source := string(data)
	for _, symbol := range []string{
		"type WorkforceMemberDTO",
		"type WorkforceBaseAgentDTO",
		"func mapMember(",
		"func mapMemberFromProfile(",
		"func mapSkillRefs(",
		"func mapToolRefs(",
	} {
		if strings.Contains(source, symbol) {
			t.Fatalf("query_service.go still contains %s; move workforce projection details into dedicated query files", symbol)
		}
	}
}

func TestOperationsEvaluationProjectionIsSplitFromStores(t *testing.T) {
	if _, err := os.Stat(filepath.Clean("evaluation_projection.go")); err != nil {
		t.Fatalf("operations evaluation projection file is required: %v", err)
	}
	data, err := os.ReadFile(filepath.Clean("audit_evaluation.go"))
	if err != nil {
		t.Fatalf("read audit_evaluation.go: %v", err)
	}
	source := string(data)
	for _, symbol := range []string{
		"func aggregateEvaluationItems(",
		"func evaluationsFromMission(",
		"func stableEvaluationAgentID(",
		"func qualityScore(",
	} {
		if strings.Contains(source, symbol) {
			t.Fatalf("audit_evaluation.go still contains %s; move evaluation scoring and identity projection into evaluation_projection.go", symbol)
		}
	}
}

func countOperationFileLines(data []byte) int {
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
