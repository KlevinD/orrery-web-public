package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/migration/nativescheduled"
	"orrery_v2/internal/migration/openclawshadow"
	skillres "orrery_v2/internal/resource/skill"
)

func TestRunNativeScheduledResourcesRequiresExplicitApply(t *testing.T) {
	var out bytes.Buffer
	err := runNativeScheduled(context.Background(), []string{
		"resources", "--jobs", "jobs.json", "--db", "file:///tmp/unused.db", "--base-dir", t.TempDir(),
	}, &out)
	if err == nil || !strings.Contains(err.Error(), "requires explicit --apply and --source-fingerprint") {
		t.Fatalf("resources error = %v", err)
	}
}

func TestRollbackUsesSavedCutoverPlanInsteadOfCurrentDisabledState(t *testing.T) {
	root := t.TempDir()
	jobsPath, _ := writeNativeScheduledJobsFixture(t, root)
	raw, err := os.ReadFile(jobsPath)
	if err != nil {
		t.Fatal(err)
	}
	livePlan, err := nativescheduled.BuildPlan(raw)
	if err != nil {
		t.Fatal(err)
	}
	enabledID := livePlan.Tasks[0].SourceJobID
	savedRaw, err := json.Marshal(struct {
		Jobs []openclawshadow.SourceJob `json:"jobs"`
	}{Jobs: nativeScheduledJobsWithEnabledSource(t, enabledID)})
	if err != nil {
		t.Fatal(err)
	}
	savedPlan, err := nativescheduled.BuildPlan(savedRaw)
	if err != nil {
		t.Fatal(err)
	}
	if err := nativescheduled.SaveCutoverPlan(root, savedPlan, time.Now()); err != nil {
		t.Fatal(err)
	}

	rollbackPlan, err := resolveNativeScheduledActionPlan("rollback", root, livePlan)
	if err != nil {
		t.Fatal(err)
	}
	if !rollbackPlan.Tasks[0].SourceEnabled || livePlan.Tasks[0].SourceEnabled {
		t.Fatalf("rollback enabled=%t live enabled=%t", rollbackPlan.Tasks[0].SourceEnabled, livePlan.Tasks[0].SourceEnabled)
	}
}

func nativeScheduledJobsWithEnabledSource(t *testing.T, enabledID string) []openclawshadow.SourceJob {
	t.Helper()
	jobs := make([]openclawshadow.SourceJob, 0, len(nativescheduled.Catalog()))
	for _, entry := range nativescheduled.Catalog() {
		jobs = append(jobs, openclawshadow.SourceJob{
			ID: entry.SourceJobID, Name: entry.SourceJobID, AgentID: entry.AgentID, Enabled: entry.SourceJobID == enabledID,
			Schedule: openclawshadow.SourceSchedule{Kind: "cron", Expr: "0 1 * * *", Timezone: "Asia/Shanghai"},
			Payload:  openclawshadow.SourcePayload{Kind: "agentTurn", Message: "run native task", Model: entry.SourceModel, TimeoutSeconds: 1200},
		})
	}
	return jobs
}

func TestRunNativeScheduledMutationActionsRequireApplyAndFingerprint(t *testing.T) {
	for _, action := range []string{"stage", "apply", "resources", "cutover", "rollback"} {
		t.Run(action, func(t *testing.T) {
			var out bytes.Buffer
			err := runNativeScheduled(context.Background(), []string{
				action, "--jobs", "jobs.json", "--db", "file:///tmp/unused.db", "--base-dir", t.TempDir(),
			}, &out)
			if err == nil || !strings.Contains(err.Error(), "requires explicit --apply and --source-fingerprint") {
				t.Fatalf("%s error = %v", action, err)
			}
		})
	}
}

func TestImportNativeScheduledSkillInstallsRuntimePackage(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "staged", "tiedan-ai-intelligence")
	if err := os.MkdirAll(filepath.Join(source, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir staged skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nskill_id: skill.tiedan-ai-intelligence\nname: Tiedan AI Intelligence\nversion: 1.0.0\n---\n\n# Tiedan AI Intelligence\n"), 0o600); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "scripts", "collect.py"), []byte("print('ok')\n"), 0o600); err != nil {
		t.Fatalf("write script: %v", err)
	}

	registry := skillres.NewMemoryRegistry()
	store := skillres.NewPackageStore(filepath.Join(root, "runtime", "skills"))
	id := agentcore.SkillID("skill.tiedan-ai-intelligence")
	if err := importNativeScheduledSkill(ctx, registry, store, source, id); err != nil {
		t.Fatalf("import native scheduled skill: %v", err)
	}
	before, err := registry.Get(ctx, id)
	if err != nil {
		t.Fatalf("get registered skill: %v", err)
	}
	if before.PackageSnapshot == nil {
		t.Fatal("imported skill has no executable snapshot")
	}
	if _, raw, err := store.ReadFile(ctx, id, "scripts/collect.py"); err != nil {
		t.Fatalf("read installed script: %v", err)
	} else if string(raw) != "print('ok')\n" {
		t.Fatalf("installed script = %q", raw)
	}
	if err := os.WriteFile(filepath.Join(source, "scripts", "collect.py"), []byte("print('updated')\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := importNativeScheduledSkill(ctx, registry, store, source, id); err != nil {
		t.Fatalf("reimport native scheduled skill: %v", err)
	}
	after, err := registry.Get(ctx, id)
	if err != nil || after.PackageSnapshot == nil || after.PackageSnapshot.Digest == before.PackageSnapshot.Digest {
		t.Fatalf("reimport did not bind updated content: %+v, err=%v", after, err)
	}
	if raw, err := store.ReadSnapshotFile(ctx, before, "scripts/collect.py"); err != nil || string(raw) != "print('ok')\n" {
		t.Fatalf("reimport changed historical snapshot: %q, err=%v", raw, err)
	}
}
