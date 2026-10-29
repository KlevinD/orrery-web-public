package main

import (
	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"strings"
	"testing"
)

func TestProductionAgentRunWorkerExposesScheduledOccurrenceIdentity(t *testing.T) {
	run := runtimedaemon.AgentRun{Request: agentcore.RuntimeRequest{RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{"generation_id": "generation-current", "scheduled_task_id": "schedule-current", "scheduled_run_id": "generation-current", "scheduled_trigger_type": "due"}}}}
	input := agentInputForProductionAgentRun(run)
	for _, text := range []string{input.ContextPack.WorkerPersonalGoal, input.Task.WorkerPersonalGoal} {
		for _, identity := range []string{"generation_id=generation-current", "scheduled_task_id=schedule-current", "scheduled_run_id=generation-current"} {
			if !strings.Contains(text, identity) {
				t.Fatalf("missing %s in task context", identity)
			}
		}
	}
	found := false
	for _, block := range input.RuntimeOptions.PromptBlocks {
		if block.BlockID == scheduledGenerationPromptBlockID {
			found = true
			if !strings.Contains(block.Content, "scheduled_task_id=schedule-current") {
				t.Fatal("protected generation block lost scheduled identity")
			}
		}
	}
	if !found {
		t.Fatal("generation prompt block missing")
	}
}
