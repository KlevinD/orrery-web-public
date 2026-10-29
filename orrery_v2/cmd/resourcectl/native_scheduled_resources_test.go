package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/migration/nativescheduled"
	"orrery_v2/internal/migration/openclawshadow"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/uow"
	skillres "orrery_v2/internal/resource/skill"
)

func TestNativeScheduledResourcesReconcilesSkillsProfilesAndWorkspaces(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dbURL := "file://" + filepath.ToSlash(filepath.Join(root, "core.db"))
	for _, id := range nativeScheduledTestSkillIDs() {
		name := strings.TrimPrefix(string(id), "skill.")
		dir := filepath.Join(root, "skills", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\nskill_id: " + string(id) + "\nname: " + name + "\ndescription: migrated\n---\n# Migrated\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if id == "skill.tiedan-ai-intelligence" {
			if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "scripts", "collect.py"), []byte("print('ok')\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}

	registry, err := db.OpenRegistryFromURLs(ctx, map[string]string{db.RoleCore: dbURL})
	if err != nil {
		t.Fatal(err)
	}
	store, err := registry.Store(db.RoleCore)
	if err != nil {
		t.Fatal(err)
	}
	profiles := agentcore.NewPlatformProfileRepository(uow.New(store))
	profileIDs := nativeScheduledTestProfileIDs()
	for _, id := range profileIDs {
		profile := nativeScheduledTestProfile(id)
		if err := profiles.SaveProfile(ctx, profile); err != nil {
			t.Fatalf("save profile %s: %v", id, err)
		}
	}
	oldDir := filepath.Join(root, "old", "image-generate")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "SKILL.md"), []byte("---\nskill_id: skill.image-generate\nname: image-generate\ndescription: old\n---\n# Old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldDefinition, err := skillres.HydrateFromDirectory(ctx, oldDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := skillres.NewPlatformRegistry(store).Register(ctx, oldDefinition); err != nil {
		t.Fatal(err)
	}
	if err := registry.Close(); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	jobsPath, sourceFingerprint := writeNativeScheduledJobsFixture(t, root)
	err = runNativeScheduled(ctx, []string{"resources", "--jobs", jobsPath, "--source-fingerprint", sourceFingerprint, "--db", dbURL, "--base-dir", root, "--apply"}, &out)
	if err != nil {
		t.Fatalf("run resources: %v", err)
	}
	if !strings.Contains(out.String(), fmt.Sprintf(`"skill_count": %d`, len(nativeScheduledTestSkillIDs()))) ||
		!strings.Contains(out.String(), fmt.Sprintf(`"profile_count": %d`, len(profileIDs))) {
		t.Fatalf("output = %s", out.String())
	}

	registry, err = db.OpenRegistryFromURLs(ctx, map[string]string{db.RoleCore: dbURL})
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	store, err = registry.Store(db.RoleCore)
	if err != nil {
		t.Fatal(err)
	}
	skills := skillres.NewPlatformRegistry(store)
	for _, id := range nativeScheduledTestSkillIDs() {
		definition, err := skills.Get(ctx, id)
		if err != nil {
			t.Fatalf("get skill %s: %v", id, err)
		}
		if !strings.HasPrefix(definition.SourceRef, "file://"+filepath.ToSlash(filepath.Join(root, "data", "skills"))) {
			t.Fatalf("skill %s source_ref = %q", id, definition.SourceRef)
		}
	}
	packageStore := skillres.NewPackageStore(filepath.Join(root, "data", "skills"))
	if _, raw, err := packageStore.ReadFile(ctx, "skill.tiedan-ai-intelligence", "scripts/collect.py"); err != nil {
		t.Fatalf("read installed skill script: %v", err)
	} else if string(raw) != "print('ok')\n" {
		t.Fatalf("installed skill script = %q", raw)
	}
	profiles = agentcore.NewPlatformProfileRepository(uow.New(store))
	for _, id := range profileIDs {
		profile, err := profiles.GetProfile(ctx, id)
		if err != nil {
			t.Fatalf("get profile %s: %v", id, err)
		}
		want := filepath.Join(root, "workspaces", string(id))
		if profile.WorkspaceRoot != want {
			t.Fatalf("profile %s workspace_root = %q, want %q", id, profile.WorkspaceRoot, want)
		}
	}
}

func writeNativeScheduledJobsFixture(t *testing.T, root string) (string, string) {
	t.Helper()
	jobs := make([]openclawshadow.SourceJob, 0, len(nativescheduled.Catalog()))
	for _, entry := range nativescheduled.Catalog() {
		jobs = append(jobs, openclawshadow.SourceJob{
			ID: entry.SourceJobID, Name: entry.SourceJobID, AgentID: entry.AgentID,
			Schedule: openclawshadow.SourceSchedule{Kind: "cron", Expr: "0 1 * * *", Timezone: "Asia/Shanghai"},
			Payload:  openclawshadow.SourcePayload{Kind: "agentTurn", Message: "run native task", Model: entry.SourceModel, TimeoutSeconds: 1200},
		})
	}
	raw, err := json.Marshal(openclawshadow.SourceExport{Jobs: jobs})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := nativescheduled.BuildPlan(raw)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "jobs.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, plan.SourceFingerprint
}

func nativeScheduledTestSkillIDs() []agentcore.SkillID {
	rawIDs := nativescheduled.ProductionSkillIDs()
	ids := make([]agentcore.SkillID, 0, len(rawIDs))
	for _, id := range rawIDs {
		ids = append(ids, agentcore.SkillID(id))
	}
	return ids
}

func nativeScheduledTestProfileIDs() []agentcore.AgentProfileID {
	seen := make(map[agentcore.AgentProfileID]struct{})
	ids := make([]agentcore.AgentProfileID, 0)
	for _, entry := range nativescheduled.Catalog() {
		if entry.Disposition != nativescheduled.CatalogDispositionAgentTask {
			continue
		}
		id := agentcore.AgentProfileID(strings.TrimSpace(entry.AgentID))
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func nativeScheduledTestProfile(id agentcore.AgentProfileID) agentcore.AgentProfile {
	return agentcore.AgentProfile{
		ID: id, RealName: "企业员工", DisplayName: string(id), RoleName: "Company Worker", AgentKind: agentcore.AgentKindAI,
		OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef: agentcore.OrganizationRef{ID: "minvia"}, DepartmentRef: agentcore.DepartmentRef{ID: "operations"},
			PositionRef: agentcore.PositionRef{ID: "worker"}, ManagerAgentRef: agentcore.AgentProfileRef{ID: "manager"},
			EmploymentType: agentcore.EmploymentTypeAIAgent, SupervisionPolicy: agentcore.SupervisionPolicyManaged,
		},
		Persona: agentcore.AgentPersona{ResponsibilityBoundary: "负责执行企业定时任务并保留审计证据。"},
		BaseAgent: agentcore.BaseAgentSpec{
			InheritsBaseAgent: true, AuditRequired: true, SystemPrompt: "你是企业任务员工，必须接受审计。",
			LLMAPIKeyRef: "secret://system/llm-api-key", SkillScope: "profile_bound", ToolScope: "profile_bound", MCPScope: "profile_bound",
		},
		PersonalMemoryProfileRef: agentcore.MemoryProfileRef{MemoryProfileID: agentcore.MemoryProfileID("memory-" + string(id)), OwnerAgentID: agentcore.AgentID(id), MemoryKind: agentcore.MemoryKindPersonal},
		SkillRefs:                []agentcore.SkillRef{}, ToolRefs: []agentcore.ToolRef{}, RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindOpenClaw}, Enabled: true,
	}
}
