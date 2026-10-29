package main

import (
	"context"
	"strings"
	"testing"
	"time"

	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform/testkit"
)

func TestEnsureDefaultLegalScheduledTasksSeedsDisabledAgentTasks(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := schedulerapp.NewRepository(env.DBRouter)
	now := time.Date(2026, 6, 21, 8, 0, 0, 0, time.UTC)

	if err := ensureDefaultLegalScheduledTasks(ctx, repo, now); err != nil {
		t.Fatalf("ensure default legal schedules: %v", err)
	}

	wantAgents := map[string]string{
		"legal-schedule-policy-intelligence-daily": defaultLegalKnowledgeAgentID.String(),
		"legal-schedule-contract-queue-daily":      defaultLegalContractAgentID.String(),
		"legal-schedule-compliance-risk-weekly":    defaultLegalComplianceAgentID.String(),
		"legal-schedule-operations-summary-daily":  defaultLegalOrchestratorAgentID.String(),
	}
	for taskID, agentID := range wantAgents {
		task, err := repo.GetTask(ctx, taskID)
		if err != nil {
			t.Fatalf("get task %s: %v", taskID, err)
		}
		if task.Enabled || task.Status != schedulerapp.TaskStatusDisabled {
			t.Fatalf("task %s should be seeded disabled: %+v", taskID, task)
		}
		if task.Target.Mode != schedulerapp.TargetModeAgentTask || task.Target.AgentID != agentID {
			t.Fatalf("task %s target = %+v, want agent %s", taskID, task.Target, agentID)
		}
		if task.CreatorProfileID != agentID || task.CreatedByKind != schedulerapp.ViewerKindSystem {
			t.Fatalf("task %s creator = profile:%q kind:%q, want system profile %q", taskID, task.CreatorProfileID, task.CreatedByKind, agentID)
		}
		if task.Schedule.Type != schedulerapp.ScheduleTypeCron || task.Schedule.Timezone != "Asia/Shanghai" {
			t.Fatalf("task %s schedule = %+v", taskID, task.Schedule)
		}
		if task.Target.TaskGoal == "" || !strings.Contains(task.Target.Prompt, "Legal MCP") {
			t.Fatalf("task %s prompt/goal = %+v", taskID, task.Target)
		}
		if task.Policy.Concurrency != schedulerapp.ConcurrencyAllowParallel || task.Policy.Misfire != schedulerapp.MisfireRunOnce {
			t.Fatalf("task %s policy = %+v", taskID, task.Policy)
		}
	}

	page, err := repo.ListTasks(ctx, true)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(page) != len(wantAgents) {
		t.Fatalf("seeded task count = %d, want %d: %+v", len(page), len(wantAgents), page)
	}
}

func TestEnsureDefaultLegalScheduledTasksDoesNotOverwriteExistingTask(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := schedulerapp.NewRepository(env.DBRouter)
	now := time.Date(2026, 6, 21, 8, 0, 0, 0, time.UTC)

	if err := ensureDefaultLegalScheduledTasks(ctx, repo, now); err != nil {
		t.Fatalf("ensure default legal schedules: %v", err)
	}
	task, err := repo.GetTask(ctx, "legal-schedule-policy-intelligence-daily")
	if err != nil {
		t.Fatalf("get seeded task: %v", err)
	}
	task.Title = "Custom legal policy schedule"
	task.Enabled = true
	task.Status = schedulerapp.TaskStatusEnabled
	task.Target.Prompt = "custom prompt"
	task.CreatorProfileID = ""
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatalf("save customized task: %v", err)
	}

	if err := ensureDefaultLegalScheduledTasks(ctx, repo, now.Add(time.Hour)); err != nil {
		t.Fatalf("ensure default legal schedules again: %v", err)
	}
	got, err := repo.GetTask(ctx, "legal-schedule-policy-intelligence-daily")
	if err != nil {
		t.Fatalf("get customized task: %v", err)
	}
	if got.Title != "Custom legal policy schedule" || !got.Enabled || got.Target.Prompt != "custom prompt" || got.CreatorProfileID != defaultLegalKnowledgeAgentID.String() {
		t.Fatalf("customized task was overwritten: %+v", got)
	}
	page, err := repo.ListTasks(ctx, true)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(page) != 4 {
		t.Fatalf("task count after second seed = %d, want 4: %+v", len(page), page)
	}
}
