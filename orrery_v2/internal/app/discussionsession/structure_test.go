package discussionsession

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestDiscussionSessionProductionFilesStayFocused(t *testing.T) {
	const maxProductionLines = 800
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read discussionsession package: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		lines := countDiscussionSessionLines(t, filepath.Join(".", name))
		if lines > maxProductionLines {
			t.Fatalf("%s has %d lines, want <= %d", name, lines, maxProductionLines)
		}
	}
}

func TestDiscussionSessionTestFilesStayFocused(t *testing.T) {
	const maxTestLines = 800
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read discussionsession package: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, "_test.go") {
			continue
		}
		lines := countDiscussionSessionLines(t, filepath.Join(".", name))
		if lines > maxTestLines {
			t.Fatalf("%s has %d lines, want <= %d", name, lines, maxTestLines)
		}
	}
}

func TestDiscussionSessionPackagePathUsesDiscussionNaming(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	if strings.Contains(filepath.ToSlash(wd), "/pmconversation") {
		t.Fatalf("discussion session app package must not keep legacy pmconversation path: %s", wd)
	}
}

func TestDiscussionSessionProductionTypesUseDiscussionAgentNaming(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read discussionsession package: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(data), "PMAgent") {
			t.Fatalf("%s still uses PMAgent production identifiers; use DiscussionAgent naming for the shared runner base", name)
		}
	}
}

func TestDiscussionMentionProductionFilesStayFocused(t *testing.T) {
	for _, name := range []string{"service_mentions.go", "service_mention_tools.go", "service_mention_members.go"} {
		if _, err := os.Stat(filepath.Clean(name)); err != nil {
			t.Fatalf("discussion mention responsibility file %s is required: %v", name, err)
		}
		if lines := countDiscussionSessionLines(t, filepath.Join(".", name)); lines > 360 {
			t.Fatalf("%s has %d lines, want <= 360; split mention dispatch, tool calls, and member lookup helpers", name, lines)
		}
	}
}

func TestDiscussionServiceLifecycleRunsStaySeparated(t *testing.T) {
	if _, err := os.Stat(filepath.Clean("service_lifecycle.go")); err != nil {
		t.Fatalf("discussion run lifecycle responsibility file is required: %v", err)
	}
	if lines := countDiscussionSessionLines(t, filepath.Join(".", "service.go")); lines > 520 {
		t.Fatalf("service.go has %d lines, want <= 520; keep run lifecycle state and locks outside the core discussion flow", lines)
	}
}

func TestDiscussionRetryProductionFilesStayFocused(t *testing.T) {
	for _, name := range []string{"service_retry.go", "service_retry_execution.go", "service_retry_rollback.go", "service_session_control.go"} {
		if _, err := os.Stat(filepath.Clean(name)); err != nil {
			t.Fatalf("discussion retry responsibility file %s is required: %v", name, err)
		}
		if lines := countDiscussionSessionLines(t, filepath.Join(".", name)); lines > 360 {
			t.Fatalf("%s has %d lines, want <= 360; split retry entrypoints, execution, rollback, and session control", name, lines)
		}
	}
}

func TestDiscussionSessionProductionIdentifiersUseDiscussionNaming(t *testing.T) {
	forbidden := []string{
		"PMConversation",
		"pmConversation",
		"PostPMMessage",
		"EnsurePMConversation",
		"PMHuman",
		"pmconversation",
		"pm-conversation",
		"pm-",
		"pm_conversation",
		"pm_discussion",
		"core.pm_conversation",
		`"pm"`,
		"BuiltinToolNames(",
		"DefaultAgentMember",
		"defaultAgentMembers",
		"validateDefaultAgentMembers",
	}
	forbiddenPatterns := []*regexp.Regexp{
		regexp.MustCompile(`\bpm[A-Z_]`),
		regexp.MustCompile(`\bPM[A-Za-z0-9_]*`),
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read discussionsession package: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(data)
		for _, token := range forbidden {
			if strings.Contains(text, token) {
				t.Fatalf("%s still uses legacy internal discussion identifier %q", name, token)
			}
		}
		for _, pattern := range forbiddenPatterns {
			if pattern.MatchString(text) {
				t.Fatalf("%s still uses legacy internal discussion identifier matching %q", name, pattern.String())
			}
		}
	}
}

func countDiscussionSessionLines(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(data) == 0 {
		return 0
	}
	lines := strings.Count(string(data), "\n")
	if data[len(data)-1] != '\n' {
		lines++
	}
	return lines
}
