package main

import (
	"encoding/json"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

const formalMissionTaskResultPromptBlockID = "formal_mission_task_result_contract"

func formalMissionTaskResultInstruction(task agentcore.TaskInput) string {
	return missionTaskResultInstruction(task, false)
}

func scheduledMissionTaskResultInstruction(task agentcore.TaskInput) string {
	return missionTaskResultInstruction(task, true)
}

func missionTaskResultInstruction(task agentcore.TaskInput, required bool) string {
	if !required && len(task.ExpectedDeliverables) == 0 && len(task.EvidenceRequirements) == 0 && len(task.AcceptanceCriteria) == 0 {
		return ""
	}

	type deliverableMapping struct {
		ExpectedDeliverableID string `json:"expected_deliverable_id"`
		SourceKind            string `json:"source_kind"`
		SourceRef             string `json:"source_ref"`
	}
	type evidenceMapping struct {
		EvidenceRequirementID string `json:"evidence_requirement_id"`
		AcceptanceCriterionID string `json:"acceptance_criterion_id"`
		SourceKind            string `json:"source_kind"`
		SourceRef             string `json:"source_ref"`
	}
	template := struct {
		Version      int                  `json:"version"`
		Outcome      string               `json:"outcome"`
		Summary      string               `json:"summary"`
		Deliverables []deliverableMapping `json:"deliverables"`
		Evidence     []evidenceMapping    `json:"evidence"`
	}{
		Version:      1,
		Outcome:      agentResultOutcomeCompleted,
		Summary:      "用一句话总结本次结果",
		Deliverables: make([]deliverableMapping, 0, len(task.ExpectedDeliverables)),
		Evidence:     make([]evidenceMapping, 0, len(task.EvidenceRequirements)),
	}
	for _, expected := range task.ExpectedDeliverables {
		template.Deliverables = append(template.Deliverables, deliverableMapping{
			ExpectedDeliverableID: strings.TrimSpace(expected.ID),
			SourceKind:            "runtime_output",
			SourceRef:             "",
		})
	}
	for _, requirement := range task.EvidenceRequirements {
		sourceKind, sourceRef := formalResultEvidenceSource(requirement.Kind)
		template.Evidence = append(template.Evidence, evidenceMapping{
			EvidenceRequirementID: strings.TrimSpace(requirement.ID),
			AcceptanceCriterionID: formalResultAcceptanceID(requirement, task.AcceptanceCriteria),
			SourceKind:            sourceKind,
			SourceRef:             sourceRef,
		})
	}
	templateJSON, err := json.Marshal(template)
	if err != nil {
		return "正式 Mission Task 的最终回复必须包含 orrery-agent-result JSON 代码块。"
	}
	return "## 正式任务结果回执（必须执行）\n" +
		"以下合同约束最终停止执行时的回执，不是让你提前停止。只要仍有原授权范围内可执行的下一步且预算与期限允许，就继续完成任务；尚未撰写产物、阶段未完成、首次验证未通过，都不自动构成阻塞。\n" +
		"工具返回可修正的参数、格式或质量问题时，依据实际反馈作有界修正并重新验证，保留已成功的产物与回执；不能只描述应该如何修正就结束，也不能重复无变化的失败调用。不得扩大权限、跳过质量门槛或代替人工审批。\n" +
		"完成任务内容后，最终回复必须以一个且仅一个 orrery-agent-result JSON 代码块结束；即使正文已经说明结论也不能省略，否则本次运行会失败并重试。\n" +
		"这是本任务的可执行结果合同，不是格式示例。最终代码块必须与下面的 JSON 逐字段一致，只替换 summary 以及尖括号中的 source_ref。\n" +
		"根对象只允许 version、outcome、summary、deliverables、evidence；deliverables 项只允许 expected_deliverable_id、source_kind、source_ref；evidence 项只允许 evidence_requirement_id、acceptance_criterion_id、source_kind、source_ref。\n" +
		"outcome 只能是 completed 或 blocked。只有全部验收条件均已满足时才能使用 completed；只有缺少具体必要输入、权限不足、依赖无法自行恢复、合理修正后仍无法通过验证，或预算与期限已耗尽而无法继续时，才以 blocked 结束。此时在 summary 中写明实际阻塞、已尝试的修正及仍未完成的目标，同时把 deliverables 和 evidence 都设为空数组。不得把尚未尝试的修正说成已失败，不得为了 completed 编造结果。\n" +
		"completed 结果严禁新增、删除或重命名字段和数组项，严禁修改已填入的 ID 与 source_kind；blocked 结果只允许清空两个数组。尤其不要输出 status、deliverable_id、title、agent_result、context_bundle 或 memory_digest。不得编造引用。\n" +
		"tool_log 的 source_ref 优先使用真实 tool call ID；如果运行时未显示 call ID，可使用该成功工具输出中真实存在的 ID、ref 或 URI。\n\n" +
		"```orrery-agent-result\n" + string(templateJSON) + "\n```"
}

func formalResultAcceptanceID(requirement agentcore.TaskEvidenceInput, criteria []agentcore.TaskAcceptanceInput) string {
	description := strings.Join(strings.Fields(requirement.Description), " ")
	for _, criterion := range criteria {
		if description != "" && strings.EqualFold(description, strings.Join(strings.Fields(criterion.Description), " ")) {
			return strings.TrimSpace(criterion.ID)
		}
	}
	if len(criteria) == 1 {
		return strings.TrimSpace(criteria[0].ID)
	}
	return "<从验收标准中选择匹配的精确 ID>"
}

func formalResultEvidenceSource(kind string) (string, string) {
	switch agentcore.EvidenceKind(strings.TrimSpace(kind)) {
	case agentcore.EvidenceKindToolLog, agentcore.EvidenceKindTestLog:
		return "tool_log", "<真实 tool call ID 或成功工具输出中的真实 ID/ref/URI>"
	case agentcore.EvidenceKindArtifact:
		return "runtime_artifact", "<本次运行产生的真实 artifact ID>"
	default:
		return "external_url", "<真实且可审计的 http/https URL>"
	}
}
