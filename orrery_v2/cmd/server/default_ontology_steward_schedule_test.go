package main

import (
	"context"
	"strings"
	"testing"
	"time"

	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform/testkit"
)

func TestEnsureDefaultOntologyStewardScheduledTasksCreatesThreeEnabledAgentTasks(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := schedulerapp.NewRepository(env.DBRouter)
	now := time.Date(2026, 8, 28, 1, 0, 0, 0, time.UTC)
	if err := ensureDefaultOntologyStewardScheduledTasks(context.Background(), repo, now); err != nil {
		t.Fatal(err)
	}
	if err := ensureDefaultOntologyStewardScheduledTasks(context.Background(), repo, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, seed := range ontologyStewardScheduleSeeds() {
		task, err := repo.GetTask(context.Background(), seed.id)
		if err != nil {
			t.Fatal(err)
		}
		if !task.Enabled || task.Status != schedulerapp.TaskStatusEnabled || task.Target.Mode != schedulerapp.TargetModeAgentTask ||
			task.Target.AgentID != defaultOntologyStewardAgentID || task.CreatorProfileID != defaultOntologyStewardAgentID || task.NextRunAt.IsZero() {
			t.Fatalf("ontology steward task %s = %+v", seed.id, task)
		}
		if !strings.Contains(task.Target.Prompt, ontologyStewardResultProtocolMarker) {
			t.Fatalf("ontology steward task %s missing result protocol: %q", seed.id, task.Target.Prompt)
		}
		for _, expected := range []string{
			"```orrery-agent-result",
			`"outcome":"completed"`,
			`"expected_deliverable_id":"deliverable-result","source_kind":"runtime_output","source_ref":""`,
			`"evidence_requirement_id":"evidence-acceptance-1","acceptance_criterion_id":"acceptance-1","source_kind":"tool_log"`,
			"ToolCall.ID in source_ref; it starts with call_",
		} {
			if !strings.Contains(task.Target.Prompt, expected) {
				t.Fatalf("ontology steward task %s result protocol missing %q: %q", seed.id, expected, task.Target.Prompt)
			}
		}
	}
}

func TestOntologyStewardResultProtocolExampleIsParseable(t *testing.T) {
	manifest, found, err := parseAgentResultManifest(ontologyStewardResultProtocolPrompt)
	if err != nil || !found {
		t.Fatalf("parseAgentResultManifest() found=%t err=%v", found, err)
	}
	if manifest.Version != 1 || manifest.Outcome != agentResultOutcomeCompleted || len(manifest.Deliverables) != 1 || len(manifest.Evidence) != 1 {
		t.Fatalf("manifest = %+v", manifest)
	}
	deliverable := manifest.Deliverables[0]
	if deliverable.ExpectedDeliverableID != "deliverable-result" || deliverable.SourceKind != "runtime_output" || deliverable.SourceRef != "" {
		t.Fatalf("deliverable = %+v", deliverable)
	}
	evidence := manifest.Evidence[0]
	if evidence.EvidenceRequirementID != "evidence-acceptance-1" || evidence.AcceptanceCriterionID != "acceptance-1" || evidence.SourceKind != "tool_log" {
		t.Fatalf("evidence = %+v", evidence)
	}
}

func TestOntologyStewardPromptWithResultProtocolUpgradesV2(t *testing.T) {
	prompt := "Operator context.\n\n" + ontologyStewardResultProtocolV2Prompt
	updated := ontologyStewardPromptWithResultProtocol(prompt)
	if !strings.HasPrefix(updated, "Operator context.") ||
		strings.Contains(updated, ontologyStewardResultProtocolV2Prompt) ||
		strings.Count(updated, ontologyStewardResultProtocolMarker) != 1 {
		t.Fatalf("v2 result protocol was not upgraded: %q", updated)
	}
	if again := ontologyStewardPromptWithResultProtocol(updated); again != updated {
		t.Fatalf("v4 result protocol upgrade is not idempotent:\nfirst=%q\nsecond=%q", updated, again)
	}
}

func TestOntologyStewardPromptWithResultProtocolUpgradesV3(t *testing.T) {
	prompt := "Operator context.\n\n" + ontologyStewardResultProtocolV3Prompt
	updated := ontologyStewardPromptWithResultProtocol(prompt)
	if !strings.HasPrefix(updated, "Operator context.") ||
		strings.Contains(updated, ontologyStewardResultProtocolV3Prompt) ||
		!strings.Contains(updated, `"outcome":"completed"`) ||
		strings.Count(updated, ontologyStewardResultProtocolMarker) != 1 {
		t.Fatalf("v3 result protocol was not upgraded: %q", updated)
	}
}

func TestEnsureDefaultOntologyStewardScheduledTasksUpgradesOnlyManagedPrompt(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := schedulerapp.NewRepository(env.DBRouter)
	ctx := context.Background()
	createdAt := time.Date(2026, 8, 28, 1, 0, 0, 0, time.UTC)
	if err := ensureDefaultOntologyStewardScheduledTasks(ctx, repo, createdAt); err != nil {
		t.Fatal(err)
	}

	seed := ontologyStewardScheduleSeeds()[0]
	task, err := repo.GetTask(ctx, seed.id)
	if err != nil {
		t.Fatal(err)
	}
	task.Title = "Operator customized title"
	task.Enabled = false
	task.Target.Prompt = "Legacy ontology review prompt.\n\n" + legacyOntologyStewardResultProtocolPrompt
	task.UpdatedAt = createdAt
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	unmanagedSeed := ontologyStewardScheduleSeeds()[1]
	unmanaged, err := repo.GetTask(ctx, unmanagedSeed.id)
	if err != nil {
		t.Fatal(err)
	}
	unmanaged.CreatedBy = "ding"
	unmanaged.Target.Prompt = "Operator-owned prompt."
	if err := repo.SaveTask(ctx, unmanaged); err != nil {
		t.Fatal(err)
	}

	updatedAt := createdAt.Add(time.Hour)
	if err := ensureDefaultOntologyStewardScheduledTasks(ctx, repo, updatedAt); err != nil {
		t.Fatal(err)
	}
	if err := ensureDefaultOntologyStewardScheduledTasks(ctx, repo, updatedAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	updated, err := repo.GetTask(ctx, seed.id)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != task.Title || updated.Enabled {
		t.Fatalf("operator settings changed during prompt upgrade: %+v", updated)
	}
	if !strings.HasPrefix(updated.Target.Prompt, "Legacy ontology review prompt.") ||
		strings.Contains(updated.Target.Prompt, legacyOntologyStewardResultProtocolPrompt) ||
		strings.Count(updated.Target.Prompt, ontologyStewardResultProtocolMarker) != 1 {
		t.Fatalf("result protocol upgrade is not idempotent: %q", updated.Target.Prompt)
	}
	if !updated.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("updated_at = %s, want %s", updated.UpdatedAt, updatedAt)
	}
	unmanagedAfter, err := repo.GetTask(ctx, unmanagedSeed.id)
	if err != nil {
		t.Fatal(err)
	}
	if unmanagedAfter.Target.Prompt != unmanaged.Target.Prompt {
		t.Fatalf("operator-owned prompt changed: %q", unmanagedAfter.Target.Prompt)
	}
}
