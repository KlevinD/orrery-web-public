package scheduler

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/platform/testkit"
)

func TestResultNotificationsExcludeRetryableFailuresAndRetainExplicitReplay(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	now := time.Date(2026, 10, 15, 5, 0, 0, 0, time.UTC)
	runs := []ScheduledRun{
		{ScheduledRunID: "running", Status: RunStatusRunning, CreatedAt: now},
		{ScheduledRunID: "retryable", Status: RunStatusFailed, CreatedAt: now},
		{ScheduledRunID: "terminal-failure", Status: RunStatusFailed, CompletedAt: now, CreatedAt: now},
		{ScheduledRunID: "done", Status: RunStatusCompleted, CompletedAt: now, CreatedAt: now},
		{ScheduledRunID: "skipped", Status: RunStatusSkipped, CreatedAt: now},
		{ScheduledRunID: "old", Status: RunStatusCompleted, CompletedAt: now.Add(-24 * time.Hour), CreatedAt: now.Add(-24 * time.Hour)},
		{ScheduledRunID: "replay", Status: RunStatusCompleted, CompletedAt: now.Add(-24 * time.Hour), CreatedAt: now.Add(-24 * time.Hour)},
	}
	for _, run := range runs {
		run.ScheduledTaskID = "task"
		run.TargetMode = TargetModeToolCall
		if err := repo.SaveRun(context.Background(), run); err != nil {
			t.Fatal(err)
		}
	}
	got, err := repo.ListResultNotificationRuns(context.Background(), now.Add(-time.Minute), map[string]bool{"replay": true})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"terminal-failure": true, "done": true, "skipped": true, "replay": true}
	if len(got) != len(want) {
		t.Fatalf("got %d runs, want %d", len(got), len(want))
	}
	for _, run := range got {
		if !want[run.ScheduledRunID] {
			t.Fatalf("unexpected result: %s", run.ScheduledRunID)
		}
	}
}
