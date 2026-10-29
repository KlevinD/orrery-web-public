package execenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestPrepareWritesCompleteContextFilesFromContextPackOnly(t *testing.T) {
	env, err := Prepare(PrepareRequest{
		Root:    t.TempDir(),
		RunID:   "run-ctx",
		Runtime: "codex",
		Context: richContextPack(),
		Tools:   []agentcore.RuntimeToolBinding{{ToolID: "shell", Summary: "Run tests", GrantedPermissions: []string{"test"}}},
		Skills:  []agentcore.RuntimeSkillBinding{{SkillID: "tdd", Summary: "Write tests first"}},
	})
	if err != nil {
		t.Fatalf("prepare execenv: %v", err)
	}
	contextText := readFile(t, env.ContextPath)
	for _, want := range []string{
		"Mission Final Goal", "Ship production Stage 7",
		"Discussion Group Goal", "Coordinate",
		"Agent Organization Identity", "org-1",
		"Task Contract", "Implement 7.4",
		"Acceptance Criteria", "acc-1",
		"Required Evidence", "ev-1",
		"Memory Digests", "prefers tests",
		"Discussion Digest", "review notes",
		"Permission Constraints", "no raw secrets",
		"Source Refs", "task_contract",
	} {
		if !strings.Contains(contextText, want) {
			t.Fatalf("context missing %q:\n%s", want, contextText)
		}
	}
	if strings.Contains(contextText, "raw memory") || strings.Contains(contextText, "raw discussion") {
		t.Fatalf("raw context leaked into file:\n%s", contextText)
	}
}

func TestPrepareWritesProviderHomesAndGCMetadata(t *testing.T) {
	env, err := Prepare(PrepareRequest{
		Root:    t.TempDir(),
		RunID:   "run-meta",
		Runtime: "codex",
		Context: richContextPack(),
	})
	if err != nil {
		t.Fatalf("prepare execenv: %v", err)
	}
	if env.RuntimeEnv["CODEX_HOME"] == "" {
		t.Fatalf("CODEX_HOME missing: %#v", env.RuntimeEnv)
	}
	codexConfig := readFile(t, filepath.Join(env.RuntimeHome, "config.toml"))
	if !strings.Contains(codexConfig, "[features]") || !strings.Contains(codexConfig, "memories = false") ||
		!strings.Contains(codexConfig, "[memories]") || !strings.Contains(codexConfig, "generate_memories = false") ||
		!strings.Contains(codexConfig, "use_memories = false") {
		t.Fatalf("Codex native memories must be disabled in the run-scoped config:\n%s", codexConfig)
	}
	for _, path := range []string{env.OutputDir, env.LogsDir, env.RuntimeHome, env.MetadataPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected path %s: %v", path, err)
		}
	}
	meta := readFile(t, env.MetadataPath)
	for _, want := range []string{`"run_id":"run-meta"`, `"agent_run"`} {
		if !strings.Contains(meta, want) {
			t.Fatalf("metadata missing %q:\n%s", want, meta)
		}
	}
}

func TestPrepareKeepsCodexModelProviderWithoutCopyingUserIntegrations(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	config := `model_provider = "custom"
model = "custom-model"
notify = ["do-not-copy"]

[model_providers.custom]
name = "Custom"
base_url = "https://example.test/v1"
wire_api = "responses"
requires_openai_auth = true

[mcp_servers.user_server]
url = "http://127.0.0.1:9999/mcp"

[plugins.user_plugin]
enabled = true
`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ORRERY_CODEX_CONFIG_FILE", configPath)

	env, err := Prepare(PrepareRequest{
		Root:    t.TempDir(),
		RunID:   "run-codex-provider",
		Runtime: "codex",
	})
	if err != nil {
		t.Fatalf("prepare execenv: %v", err)
	}
	text := readFile(t, filepath.Join(env.RuntimeHome, "config.toml"))
	for _, want := range []string{`model_provider = 'custom'`, `model = 'custom-model'`, `[model_providers.custom]`, `base_url = 'https://example.test/v1'`, `memories = false`} {
		if !strings.Contains(text, want) {
			t.Fatalf("Codex runtime config missing %q:\n%s", want, text)
		}
	}
	for _, forbidden := range []string{"do-not-copy", "user_server", "user_plugin"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("Codex runtime config copied user integration %q:\n%s", forbidden, text)
		}
	}
}

func richContextPack() agentcore.ContextPack {
	return agentcore.ContextPack{
		ID:                          "ctx-1",
		MissionID:                   "mission-1",
		DiscussionGroupID:           "discussion-1",
		AgentID:                     "agent-1",
		TaskID:                      "task-1",
		MissionFinalGoal:            "Ship production Stage 7",
		DiscussionGroupGoal:         "Coordinate",
		AgentOrganizationIdentity:   agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "org-1"}, DepartmentRef: agentcore.DepartmentRef{ID: "dep-1"}, PositionRef: agentcore.PositionRef{ID: "pos-1"}},
		AgentMissionRole:            "runtime engineer",
		WorkerPersonalGoal:          "Build context and execenv",
		TaskContractRef:             agentcore.TaskContractRef{ID: "task-1"},
		TaskContractSummary:         "Implement 7.4",
		AcceptanceCriteriaRefs:      []agentcore.AcceptanceCriterionRef{{ID: "acc-1"}},
		AcceptanceCriteria:          []agentcore.TaskAcceptanceInput{{ID: "acc-1", Description: "context files"}},
		RequiredEvidenceRefs:        []agentcore.EvidenceRequirementRef{{ID: "ev-1"}},
		RequiredEvidence:            []agentcore.TaskEvidenceInput{{ID: "ev-1", Kind: "test", Description: "execenv tests"}},
		PersonalMemoryDigestRef:     agentcore.MemoryDigestRef{ID: "mem-personal", MemoryKind: agentcore.MemoryKindPersonal, SummaryRef: "prefers tests"},
		PersonalMemoryDigestSummary: "prefers tests",
		DiscussionDigestRef:         agentcore.DiscussionDigestRef{ID: "discussion-digest-1", SummaryRef: "review notes"},
		DiscussionDigestSummary:     "review notes",
		PermissionConstraints:       []string{"no raw secrets"},
		ToolSummaries:               []agentcore.ContextToolSummary{{ToolID: "shell", Summary: "Run tests"}},
		SkillSummaries:              []agentcore.ContextSkillSummary{{SkillID: "tdd", Summary: "Write tests first"}},
		SourceRefs: []agentcore.ContextSourceRef{
			{Kind: agentcore.ContextSourceKindTaskContract, Ref: agentcore.ContextSourceTypedRef{TaskContractRef: &agentcore.TaskContractRef{ID: "task-1"}}, Priority: agentcore.SourcePriorityContract, Immutable: true},
			{Kind: agentcore.ContextSourceKindMemoryDigest, Ref: agentcore.ContextSourceTypedRef{MemoryDigestRef: &agentcore.MemoryDigestRef{ID: "mem-personal"}}, Priority: agentcore.SourcePriorityMemory},
			{Kind: agentcore.ContextSourceKindDiscussionDigest, Ref: agentcore.ContextSourceTypedRef{DiscussionDigestRef: &agentcore.DiscussionDigestRef{ID: "discussion-digest-1"}}, Priority: agentcore.SourcePriorityContext},
		},
		OverridePolicy: agentcore.OverridePolicyPreserveProtected,
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
