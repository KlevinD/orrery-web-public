package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeMemorySchedulesRequiresExplicitMutationConfirmation(t *testing.T) {
	err := run(context.Background(), []string{"native-memory-schedules", "apply", "--db", "memory://native-memory-cli-confirm"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "requires explicit --apply") {
		t.Fatalf("error = %v", err)
	}
}

func TestNativeMemorySchedulesPlanApplyAndVerify(t *testing.T) {
	ctx := context.Background()
	dbURL := "file://" + filepath.ToSlash(filepath.Join(t.TempDir(), "core.db"))

	var planOut bytes.Buffer
	if err := run(ctx, []string{"native-memory-schedules", "plan", "--db", dbURL}, &planOut); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !strings.Contains(planOut.String(), `"task_count": 3`) || !strings.Contains(planOut.String(), `"applied": false`) {
		t.Fatalf("plan output = %s", planOut.String())
	}

	var applyOut bytes.Buffer
	if err := run(ctx, []string{"native-memory-schedules", "apply", "--db", dbURL, "--apply"}, &applyOut); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !strings.Contains(applyOut.String(), `"applied": true`) || !strings.Contains(applyOut.String(), `"task_count": 3`) {
		t.Fatalf("apply output = %s", applyOut.String())
	}

	var verifyOut bytes.Buffer
	if err := run(ctx, []string{"native-memory-schedules", "verify", "--db", dbURL}, &verifyOut); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !strings.Contains(verifyOut.String(), `"task_count": 3`) || !strings.Contains(verifyOut.String(), `"run_count": 0`) {
		t.Fatalf("verify output = %s", verifyOut.String())
	}
}
