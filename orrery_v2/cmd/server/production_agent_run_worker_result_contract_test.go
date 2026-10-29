package main

import (
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
)

func TestAgentInputForProductionAgentRunAddsExactFormalResultContract(t *testing.T) {
	run := runtimedaemon.AgentRun{Task: agentcore.TaskInput{
		ID:                   "autoplan-task-1",
		AcceptanceCriteria:   []agentcore.TaskAcceptanceInput{{ID: "acceptance-1", Description: "Assessment is traceable."}},
		EvidenceRequirements: []agentcore.TaskEvidenceInput{{ID: "evidence-1", Description: "Assessment is traceable.", Kind: string(agentcore.EvidenceKindToolLog), Required: true}},
		ExpectedDeliverables: []agentcore.TaskDeliverableInput{{ID: "deliverable-step-1", Title: "Assessment"}},
	}}

	input := agentInputForProductionAgentRun(run)
	blocks := input.RuntimeOptions.PromptBlocks
	if len(blocks) == 0 {
		t.Fatal("prompt blocks are empty")
	}
	contract := blocks[len(blocks)-1]
	if contract.BlockID != formalMissionTaskResultPromptBlockID || contract.Role != agentcore.RuntimePromptRoleUser {
		t.Fatalf("formal result prompt block = %+v", contract)
	}
	for _, required := range []string{
		"```orrery-agent-result", `"outcome":"completed"`, `"expected_deliverable_id":"deliverable-step-1"`,
		`"evidence_requirement_id":"evidence-1"`, `"acceptance_criterion_id":"acceptance-1"`,
		`"source_kind":"tool_log"`, "成功工具输出中的真实 ID/ref/URI", "否则本次运行会失败并重试",
		"最终代码块必须与下面的 JSON 逐字段一致", "outcome 只能是 completed 或 blocked", "blocked 结果只允许清空两个数组", "根对象只允许",
		"status、deliverable_id、title、agent_result、context_bundle 或 memory_digest",
	} {
		if !strings.Contains(contract.Content, required) {
			t.Fatalf("formal result prompt block missing %q: %s", required, contract.Content)
		}
	}
	manifest, found, err := parseAgentResultManifest(contract.Content)
	if err != nil || !found || len(manifest.Deliverables) != 1 || len(manifest.Evidence) != 1 {
		t.Fatalf("formal result manifest template = %+v, found=%t, err=%v", manifest, found, err)
	}
	messages := agentcore.BuildAgentPromptMessages(agentcore.AgentPromptMessagesRequest{Task: input.Task, Blocks: blocks})
	if len(messages) == 0 || messages[len(messages)-1].Role != "user" || !strings.Contains(messages[len(messages)-1].Content, contract.Content) {
		t.Fatalf("formal result contract is not the final runtime instruction: %+v", messages)
	}
}

func TestAgentInputForProductionAgentRunAddsOutcomeContractForUnstructuredScheduledTask(t *testing.T) {
	run := runtimedaemon.AgentRun{Request: agentcore.RuntimeRequest{RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{
		"run_source": schedulerAgentTaskRunSource,
	}}}}
	input := agentInputForProductionAgentRun(run)
	var contract agentcore.RuntimePromptBlock
	for _, block := range input.RuntimeOptions.PromptBlocks {
		if block.BlockID == formalMissionTaskResultPromptBlockID {
			contract = block
			break
		}
	}
	if contract.BlockID == "" || !strings.Contains(contract.Content, `"outcome":"completed"`) ||
		!strings.Contains(contract.Content, `"deliverables":[]`) || !strings.Contains(contract.Content, `"evidence":[]`) {
		t.Fatalf("unstructured scheduled result contract = %+v", contract)
	}
}

func TestAgentInputForProductionAgentRunAddsScheduledGenerationContractOnce(t *testing.T) {
	run := runtimedaemon.AgentRun{Request: agentcore.RuntimeRequest{RuntimeOptions: agentcore.RuntimeOptions{
		ProviderSpecific: map[string]string{"generation_id": "scheduled-run-generation-1", "scheduled_trigger_type": "manual"},
	}}}

	input := agentInputForProductionAgentRun(run)
	if !strings.Contains(input.ContextPack.WorkerPersonalGoal, "generation_id=scheduled-run-generation-1") ||
		!strings.Contains(input.ContextPack.WorkerPersonalGoal, "trigger_type=manual") ||
		!strings.Contains(input.Task.WorkerPersonalGoal, "generation_id=scheduled-run-generation-1") {
		t.Fatalf("scheduled generation worker goals = context %q task %q", input.ContextPack.WorkerPersonalGoal, input.Task.WorkerPersonalGoal)
	}
	input = agentInputForProductionAgentRun(runtimedaemon.AgentRun{Request: agentcore.RuntimeRequest{RuntimeOptions: input.RuntimeOptions}})
	var found []agentcore.RuntimePromptBlock
	for _, block := range input.RuntimeOptions.PromptBlocks {
		if block.BlockID == scheduledGenerationPromptBlockID {
			found = append(found, block)
		}
	}
	if len(found) != 1 {
		t.Fatalf("scheduled generation prompt blocks = %+v", found)
	}
	if found[0].Role != agentcore.RuntimePromptRoleUser ||
		!strings.Contains(found[0].Content, "generation_id=scheduled-run-generation-1") ||
		!strings.Contains(found[0].Content, "trigger_type=manual") ||
		!strings.Contains(found[0].Content, "Never resume another generation") {
		t.Fatalf("scheduled generation prompt block = %+v", found[0])
	}
}
