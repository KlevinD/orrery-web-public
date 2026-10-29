package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/migration/nativememory"
	"orrery_v2/internal/platform/db"
)

type nativeMemorySchedulesOutput struct {
	Action    string `json:"action"`
	Applied   bool   `json:"applied"`
	TaskCount int    `json:"task_count"`
	RunCount  int    `json:"run_count"`
}

func runNativeMemorySchedules(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: resourcectl native-memory-schedules <plan|apply|verify|rollback> [flags]")
	}
	action := args[0]
	if action != "plan" && action != "apply" && action != "verify" && action != "rollback" {
		return fmt.Errorf("unknown native-memory-schedules action %q", action)
	}
	fs := flag.NewFlagSet("native-memory-schedules "+action, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbURL := fs.String("db", "", "Orrery core database URL")
	confirmed := fs.Bool("apply", false, "confirm a mutating operation")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if strings.TrimSpace(*dbURL) == "" {
		return fmt.Errorf("--db is required")
	}
	if (action == "apply" || action == "rollback") && !*confirmed {
		return fmt.Errorf("native-memory-schedules %s requires explicit --apply", action)
	}
	result := nativeMemorySchedulesOutput{Action: action, TaskCount: len(nativememory.Catalog())}
	if action == "plan" {
		return writeNativeMemorySchedulesOutput(out, result)
	}

	registry, err := db.OpenRegistryFromURLs(ctx, map[string]string{db.RoleCore: *dbURL})
	if err != nil {
		return err
	}
	defer registry.Close()
	applier := nativememory.NewApplier(scheduler.NewRepository(db.NewRouter(registry)), time.Now().UTC())
	switch action {
	case "apply":
		report, err := applier.Apply(ctx, true)
		if err != nil {
			return err
		}
		result.Applied = report.Applied
		result.TaskCount = report.TaskCount
	case "verify":
		report, err := applier.Verify(ctx)
		if err != nil {
			return err
		}
		result.TaskCount = report.TaskCount
		result.RunCount = report.RunCount
	case "rollback":
		report, err := applier.Rollback(ctx)
		if err != nil {
			return err
		}
		result.Applied = true
		result.TaskCount = report.TaskCount
		result.RunCount = report.RunCount
	}
	return writeNativeMemorySchedulesOutput(out, result)
}

func writeNativeMemorySchedulesOutput(out io.Writer, result nativeMemorySchedulesOutput) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}
