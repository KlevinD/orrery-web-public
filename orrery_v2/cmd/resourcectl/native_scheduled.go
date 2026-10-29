package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/migration/nativememory"
	"orrery_v2/internal/migration/nativescheduled"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/uow"
	skillres "orrery_v2/internal/resource/skill"
)

type nativeScheduledOutput struct {
	Action              string `json:"action"`
	Applied             bool   `json:"applied"`
	SourceFingerprint   string `json:"source_fingerprint"`
	AssetFingerprint    string `json:"asset_fingerprint,omitempty"`
	SelectedTasks       int    `json:"selected_tasks"`
	ExcludedTasks       int    `json:"excluded_tasks"`
	AssetFiles          int    `json:"asset_files,omitempty"`
	SkillCount          int    `json:"skill_count,omitempty"`
	ProfileCount        int    `json:"profile_count,omitempty"`
	LLMProfileCount     int    `json:"llm_profile_count,omitempty"`
	RuntimeProfileCount int    `json:"runtime_profile_count,omitempty"`
	TaskCount           int    `json:"task_count,omitempty"`
	MemoryTaskCount     int    `json:"memory_task_count,omitempty"`
	SourceDisabled      int    `json:"source_disabled,omitempty"`
	TargetsEnabled      int    `json:"targets_enabled,omitempty"`
}

func runNativeScheduled(ctx context.Context, args []string, out io.Writer) error {
	allowedActions := map[string]bool{"plan": true, "stage": true, "apply": true, "resources": true, "verify": true, "cutover": true, "rollback": true}
	if len(args) == 0 || !allowedActions[args[0]] {
		return fmt.Errorf("usage: resourcectl native-scheduled <plan|stage|apply|resources|verify|cutover|rollback> --jobs <path|-> --base-dir <path> [--db <url>] [--source-fingerprint <sha256>] [--apply]")
	}
	action := args[0]
	fs := flag.NewFlagSet("native-scheduled "+action, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbURL := fs.String("db", "", "Orrery core database URL")
	jobsPath := fs.String("jobs", "", "OpenClaw cron JSON export")
	baseDir := fs.String("base-dir", "", "Orrery shared data root")
	sourceFingerprint := fs.String("source-fingerprint", "", "approved source plan fingerprint")
	openClawBinary := fs.String("openclaw-bin", "openclaw", "OpenClaw CLI used only during cutover or rollback")
	confirmed := fs.Bool("apply", false, "confirm mutation")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if strings.TrimSpace(*baseDir) == "" {
		return fmt.Errorf("--base-dir is required")
	}
	if strings.TrimSpace(*jobsPath) == "" {
		return fmt.Errorf("--jobs is required")
	}
	mutation := action == "stage" || action == "apply" || action == "resources" || action == "cutover" || action == "rollback"
	if mutation && (!*confirmed || strings.TrimSpace(*sourceFingerprint) == "") {
		return fmt.Errorf("native-scheduled %s requires explicit --apply and --source-fingerprint", action)
	}
	needsDB := action == "apply" || action == "resources" || action == "verify" || action == "cutover" || action == "rollback"
	if needsDB && strings.TrimSpace(*dbURL) == "" {
		return fmt.Errorf("native-scheduled %s requires --db", action)
	}
	raw, err := readOpenClawJobs(*jobsPath, os.Stdin)
	if err != nil {
		return err
	}
	livePlan, err := nativescheduled.BuildPlan(raw)
	if err != nil {
		return err
	}
	plan, err := resolveNativeScheduledActionPlan(action, *baseDir, livePlan)
	if err != nil {
		return err
	}
	result := nativeScheduledOutput{Action: action, SourceFingerprint: plan.SourceFingerprint, SelectedTasks: len(plan.Tasks), ExcludedTasks: len(plan.ExcludedSourceJobIDs)}
	if action == "plan" {
		return encodeNativeScheduledOutput(out, result)
	}
	if mutation && strings.TrimSpace(*sourceFingerprint) != plan.SourceFingerprint {
		return fmt.Errorf("source fingerprint mismatch: got %q, want %q", strings.TrimSpace(*sourceFingerprint), plan.SourceFingerprint)
	}

	now := time.Now().UTC()
	if action == "stage" {
		manifest, err := nativescheduled.StageAssets(*baseDir, now)
		if err != nil {
			return err
		}
		result.Applied = true
		result.AssetFingerprint = manifest.Fingerprint
		result.AssetFiles = len(manifest.Files)
		return encodeNativeScheduledOutput(out, result)
	}
	registry, err := db.OpenRegistryFromURLs(ctx, map[string]string{db.RoleCore: *dbURL})
	if err != nil {
		return err
	}
	defer registry.Close()
	if action == "resources" {
		skillCount, profileCount, runtimeProfileCount, err := reconcileNativeScheduledResources(ctx, registry, *baseDir)
		if err != nil {
			return err
		}
		result.Applied = true
		result.SkillCount = skillCount
		result.ProfileCount = profileCount
		result.RuntimeProfileCount = runtimeProfileCount
		return encodeNativeScheduledOutput(out, result)
	}
	if action == "verify" {
		verify, err := verifyNativeScheduledResources(ctx, registry, *baseDir, plan)
		if err != nil {
			return err
		}
		result.SkillCount = verify.SkillCount
		result.ProfileCount = verify.ProfileCount
		result.TaskCount = verify.TaskCount
		return encodeNativeScheduledOutput(out, result)
	}
	if action == "cutover" || action == "rollback" {
		if action == "cutover" {
			if _, err := verifyNativeScheduledResources(ctx, registry, *baseDir, plan); err != nil {
				return err
			}
		}
		source, err := newOpenClawCronSource(*openClawBinary)
		if err != nil {
			return err
		}
		release, err := acquireNativeScheduledLock(*baseDir)
		if err != nil {
			return err
		}
		defer release()
		taskRepo := scheduler.NewRepository(db.NewRouter(registry))
		var cutover nativescheduled.CutoverReport
		if action == "cutover" {
			if _, err := nativescheduled.Cutover(ctx, source, taskRepo, plan, now, false); err != nil {
				return err
			}
			if err := nativescheduled.SaveCutoverPlan(*baseDir, plan, now); err != nil {
				return err
			}
			cutover, err = nativescheduled.Cutover(ctx, source, taskRepo, plan, now, true)
		} else {
			cutover, err = nativescheduled.RollbackCutover(ctx, source, taskRepo, plan, now, true)
		}
		if err != nil {
			return err
		}
		result.Applied = cutover.Applied
		result.SourceDisabled = cutover.SourceDisabled
		result.TargetsEnabled = cutover.TargetsEnabled
		return encodeNativeScheduledOutput(out, result)
	}

	manifest, err := nativescheduled.StageAssets(*baseDir, now)
	if err != nil {
		return err
	}
	llmProfile, err := nativescheduled.OpenAICodexProfile()
	if err != nil {
		return err
	}
	llmProfileCount, err := nativescheduled.ReconcileLLMProfile(ctx, db.NewRouter(registry), llmProfile, true)
	if err != nil {
		return err
	}
	skillCount, profileCount, runtimeProfileCount, err := reconcileNativeScheduledResources(ctx, registry, *baseDir)
	if err != nil {
		return err
	}
	taskRepo := scheduler.NewRepository(db.NewRouter(registry))
	memoryReport, err := nativememory.NewApplier(taskRepo, now).Apply(ctx, true)
	if err != nil {
		return err
	}
	taskCount, err := nativescheduled.ApplyTasks(ctx, taskRepo, plan, now, true)
	if err != nil {
		return err
	}
	result.Applied = true
	result.AssetFingerprint = manifest.Fingerprint
	result.AssetFiles = len(manifest.Files)
	result.SkillCount = skillCount
	result.ProfileCount = profileCount
	result.LLMProfileCount = llmProfileCount
	result.RuntimeProfileCount = runtimeProfileCount
	result.TaskCount = taskCount
	result.MemoryTaskCount = memoryReport.TaskCount
	return encodeNativeScheduledOutput(out, result)
}

func resolveNativeScheduledActionPlan(action, baseDir string, livePlan nativescheduled.Plan) (nativescheduled.Plan, error) {
	if action != "rollback" {
		return livePlan, nil
	}
	return nativescheduled.LoadCutoverPlan(baseDir)
}

func encodeNativeScheduledOutput(out io.Writer, result nativeScheduledOutput) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func verifyNativeScheduledResources(ctx context.Context, registry *db.Registry, baseDir string, plan nativescheduled.Plan) (nativescheduled.StagedResourceVerifyReport, error) {
	core, err := registry.Store(db.RoleCore)
	if err != nil {
		return nativescheduled.StagedResourceVerifyReport{}, err
	}
	return nativescheduled.VerifyStagedResources(
		ctx,
		baseDir,
		agentcore.NewPlatformProfileRepository(uow.New(core)),
		skillres.NewPlatformRegistry(core),
		scheduler.NewRepository(db.NewRouter(registry)),
		plan,
	)
}

func acquireNativeScheduledLock(baseDir string) (func(), error) {
	lockDir := filepath.Join(baseDir, "migrations")
	if err := os.MkdirAll(lockDir, 0o750); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(lockDir, "orrery-native-cutover.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockNativeScheduledFile(file); err != nil {
		file.Close()
		return nil, fmt.Errorf("acquire Orrery native cutover lock: %w", err)
	}
	return func() {
		_ = unlockNativeScheduledFile(file)
		_ = file.Close()
	}, nil
}

func reconcileNativeScheduledResources(ctx context.Context, registry *db.Registry, baseDir string) (int, int, int, error) {
	core, err := registry.Store(db.RoleCore)
	if err != nil {
		return 0, 0, 0, err
	}
	runtimeProfileCount, err := nativescheduled.ReconcileRuntimeProfile(ctx, db.NewRouter(registry), nativescheduled.NativeScheduledRuntimeProfile(), true)
	if err != nil {
		return 0, 0, 0, err
	}
	skillCount, err := upsertNativeScheduledSkills(ctx, skillres.NewPlatformRegistry(core), skillres.NewPackageStore(filepath.Join(baseDir, "data", "skills")), baseDir)
	if err != nil {
		return 0, 0, 0, err
	}
	profileCount, err := nativescheduled.ReconcileProfiles(ctx, agentcore.NewPlatformProfileRepository(uow.New(core)), baseDir, true)
	if err != nil {
		return 0, 0, 0, err
	}
	return skillCount, profileCount, runtimeProfileCount, nil
}

func upsertNativeScheduledSkills(ctx context.Context, registry skillres.Registry, store *skillres.PackageStore, base string) (int, error) {
	ids := nativescheduled.ProductionSkillIDs()
	for _, rawID := range ids {
		id := agentcore.SkillID(rawID)
		dir := filepath.Join(base, "skills", strings.TrimPrefix(string(id), "skill."))
		if err := importNativeScheduledSkill(ctx, registry, store, dir, id); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

func importNativeScheduledSkill(ctx context.Context, registry skillres.Registry, store *skillres.PackageStore, dir string, id agentcore.SkillID) error {
	if registry == nil || store == nil {
		return fmt.Errorf("skill registry and package store are required")
	}
	packageBytes, err := archiveSkillDirectory(ctx, dir)
	if err != nil {
		return fmt.Errorf("archive %s: %w", id, err)
	}
	definition, err := store.ImportPackage(ctx, bytes.NewReader(packageBytes), int64(len(packageBytes)))
	if err != nil {
		return fmt.Errorf("import %s: %w", id, err)
	}
	if definition.ID != id {
		return fmt.Errorf("skill directory %s hydrated as %s", dir, definition.ID)
	}
	definition, err = store.FreezeDefinition(ctx, definition)
	if err != nil {
		return fmt.Errorf("freeze %s: %w", id, err)
	}
	if _, err := registry.Get(ctx, id); err == nil {
		return skillres.ApplyLegacyManagedDefinition(ctx, registry, definition, skillres.ManagedUpdateSourceNativeScheduledMigration)
	} else if errors.Is(err, skillres.ErrNotFound) {
		return registry.Register(ctx, definition)
	} else {
		return err
	}
}

func archiveSkillDirectory(ctx context.Context, dir string) ([]byte, error) {
	var paths []string
	if err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("skill package contains symlink %s", path)
		}
		paths = append(paths, path)
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Strings(paths)
	var buffer bytes.Buffer
	zw := zip.NewWriter(&buffer)
	for _, path := range paths {
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			_ = zw.Close()
			return nil, err
		}
		entry, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			_ = zw.Close()
			return nil, err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			_ = zw.Close()
			return nil, err
		}
		if _, err := entry.Write(raw); err != nil {
			_ = zw.Close()
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
