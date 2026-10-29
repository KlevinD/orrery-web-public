package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/migration/openclawshadow"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/uow"
	skillres "orrery_v2/internal/resource/skill"
)

type openClawShadowOutput struct {
	Action            string                                 `json:"action"`
	Applied           bool                                   `json:"applied"`
	SourceFingerprint string                                 `json:"source_fingerprint"`
	AssetFingerprint  string                                 `json:"asset_fingerprint,omitempty"`
	SelectedTasks     int                                    `json:"selected_tasks"`
	ExcludedTasks     int                                    `json:"excluded_tasks"`
	RetiredTasks      int                                    `json:"retired_tasks"`
	AssetFiles        int                                    `json:"asset_files,omitempty"`
	ApplyReport       *openclawshadow.ApplyReport            `json:"apply_report,omitempty"`
	VerifyReport      *openclawshadow.VerifyReport           `json:"verify_report,omitempty"`
	ProfileReport     *openclawshadow.ProfileReconcileReport `json:"profile_report,omitempty"`
}

func runOpenClawShadow(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: resourcectl openclaw-shadow <plan|apply|verify|rollback|reconcile-profiles> [flags]")
	}
	action := args[0]
	if action != "plan" && action != "apply" && action != "verify" && action != "rollback" && action != "reconcile-profiles" {
		return fmt.Errorf("unknown openclaw-shadow action %q", action)
	}
	fs := flag.NewFlagSet("openclaw-shadow "+action, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbURL := fs.String("db", "", "Orrery core database URL")
	jobsPath := fs.String("jobs", "", "OpenClaw cron JSON export")
	baseDir := fs.String("base-dir", "", "Orrery shared data root")
	confirmed := fs.Bool("apply", false, "confirm a mutating operation")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if strings.TrimSpace(*dbURL) == "" || strings.TrimSpace(*baseDir) == "" || (action != "reconcile-profiles" && strings.TrimSpace(*jobsPath) == "") {
		return fmt.Errorf("--db and --base-dir are required; --jobs is also required unless reconciling profiles")
	}
	if (action == "apply" || action == "rollback" || action == "reconcile-profiles") && !*confirmed {
		return fmt.Errorf("openclaw-shadow %s requires explicit --apply", action)
	}
	result := openClawShadowOutput{
		Action: action,
	}
	var plan openclawshadow.Plan
	if action != "reconcile-profiles" {
		raw, err := readOpenClawJobs(*jobsPath, os.Stdin)
		if err != nil {
			return fmt.Errorf("read jobs export: %w", err)
		}
		plan, err = openclawshadow.BuildPlan(raw)
		if err != nil {
			return err
		}
		result.SourceFingerprint = plan.SourceFingerprint
		result.SelectedTasks = len(plan.Tasks)
		result.ExcludedTasks = len(plan.ExcludedSourceJobIDs)
		result.RetiredTasks = len(openclawshadow.RetiredSourceJobIDs())
	}
	if action == "plan" {
		inventory, err := openclawshadow.InventoryAssets(*baseDir, openclawshadow.ProductionAssetSpecs())
		if err != nil {
			return err
		}
		result.AssetFingerprint = inventory.Fingerprint
		result.AssetFiles = len(inventory.Files)
		return writeOpenClawShadowOutput(out, result)
	}
	if action == "apply" {
		inventory, err := openclawshadow.StageAssets(*baseDir, openclawshadow.ProductionAssetSpecs())
		if err != nil {
			return err
		}
		result.AssetFingerprint = inventory.Fingerprint
		result.AssetFiles = len(inventory.Files)
	}

	registry, err := db.OpenRegistryFromURLs(ctx, map[string]string{db.RoleCore: *dbURL})
	if err != nil {
		return err
	}
	defer registry.Close()
	coreStore, err := registry.Store(db.RoleCore)
	if err != nil {
		return err
	}
	applier := openclawshadow.NewApplier(
		agentcore.NewPlatformProfileRepository(uow.New(coreStore)),
		skillres.NewPlatformRegistry(coreStore),
		scheduler.NewRepository(db.NewRouter(registry)),
		time.Now().UTC(),
	)
	switch action {
	case "apply":
		report, err := applier.Apply(ctx, plan, *baseDir, true)
		if err != nil {
			return err
		}
		result.Applied = true
		result.ApplyReport = &report
	case "verify":
		report, err := applier.Verify(ctx, plan, *baseDir)
		if err != nil {
			return err
		}
		result.VerifyReport = &report
	case "rollback":
		report, err := applier.Rollback(ctx, plan, *baseDir)
		if err != nil {
			return err
		}
		result.Applied = true
		result.VerifyReport = &report
	case "reconcile-profiles":
		report, err := applier.ReconcileProfiles(ctx, *baseDir, true)
		if err != nil {
			return err
		}
		result.Applied = true
		result.ProfileReport = &report
	}
	return writeOpenClawShadowOutput(out, result)
}

func readOpenClawJobs(path string, in io.Reader) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(in)
	}
	return os.ReadFile(path)
}

func writeOpenClawShadowOutput(out io.Writer, result openClawShadowOutput) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}
