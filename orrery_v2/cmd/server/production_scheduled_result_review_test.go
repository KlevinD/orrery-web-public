package main

import (
	"context"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/resultreview"
)

type scheduledResultReviewFunc func(context.Context, resultreview.Input) resultreview.Result

func (f scheduledResultReviewFunc) Review(ctx context.Context, input resultreview.Input) resultreview.Result {
	return f(ctx, input)
}

func scheduledReviewInput() agentcore.AgentInput {
	return agentcore.AgentInput{
		RunID: "run-review-1",
		Task: agentcore.TaskInput{
			ID: "task-review-1", MissionID: "mission-review-1", Goal: "Produce the report",
			ExpectedDeliverables: []agentcore.TaskDeliverableInput{{ID: "deliverable-1", Title: "Report"}},
			EvidenceRequirements: []agentcore.TaskEvidenceInput{{ID: "evidence-1", Kind: string(agentcore.EvidenceKindToolLog), Required: true}},
			AcceptanceCriteria:   []agentcore.TaskAcceptanceInput{{ID: "criterion-1", Description: "Report is complete"}},
		},
		RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{
			"run_source":                       schedulerAgentTaskRunSource,
			scheduledResultAIReviewProviderKey: "true",
		}},
	}
}

func scheduledReviewResult() agentcore.AgentResult {
	return agentcore.AgentResult{
		ID: "result-review-1", Summary: agentcore.Summary{Text: "Report complete"},
		Deliverables: []agentcore.Deliverable{{ID: "deliverable-result", ExpectedDeliverableID: "deliverable-1", ArtifactRef: agentcore.ArtifactRef{ID: "artifact-1", URI: "artifact://safe"}}},
		Evidence:     []agentcore.Evidence{{ID: "evidence-result", EvidenceRequirementID: "evidence-1", AcceptanceCriterionID: "criterion-1", Kind: agentcore.EvidenceKindToolLog, ToolLogRef: agentcore.ToolLogRef{ID: "call-1"}}},
	}
}

func TestReviewScheduledAgentResultKeepsApprovedResult(t *testing.T) {
	input := scheduledReviewInput()
	result := reviewScheduledAgentResult(context.Background(), input, scheduledReviewResult(), scheduledResultReviewFunc(func(_ context.Context, packet resultreview.Input) resultreview.Result {
		if packet.TaskID != input.Task.ID || len(packet.Evidence) != 1 || !packet.Evidence[0].SourcePresent {
			t.Fatalf("review packet = %+v", packet)
		}
		return resultreview.Result{Decision: resultreview.DecisionApprove, Rationale: "All mappings are present.", InputDigest: "digest-1"}
	}))
	if result.Blocker != nil || len(result.Deliverables) != 1 || len(result.Evidence) != 1 || len(result.Decisions) != 1 {
		t.Fatalf("approved result = %+v", result)
	}
}

func TestReviewScheduledAgentResultTurnsHoldIntoDurableBlocker(t *testing.T) {
	input := scheduledReviewInput()
	result := reviewScheduledAgentResult(context.Background(), input, scheduledReviewResult(), scheduledResultReviewFunc(func(context.Context, resultreview.Input) resultreview.Result {
		return resultreview.Result{Decision: resultreview.DecisionHold, Rationale: "The required evidence mapping is ambiguous.", Missing: []string{"evidence-1"}, InputDigest: "digest-2"}
	}))
	if result.Blocker == nil || len(result.Deliverables) != 0 || len(result.Evidence) != 0 || len(result.Decisions) != 1 {
		t.Fatalf("held result = %+v", result)
	}
	if result.Summary.Text == "" || result.Blocker.Summary == "" {
		t.Fatalf("held result explanation = %+v", result)
	}
	if !strings.Contains(result.Blocker.Summary, "evidence-1") {
		t.Fatalf("held result omitted missing item: %s", result.Blocker.Summary)
	}
}

func TestReviewScheduledAgentResultDoesNothingWhenDisabled(t *testing.T) {
	input := scheduledReviewInput()
	input.RuntimeOptions.ProviderSpecific[scheduledResultAIReviewProviderKey] = "false"
	calls := 0
	result := reviewScheduledAgentResult(context.Background(), input, scheduledReviewResult(), scheduledResultReviewFunc(func(context.Context, resultreview.Input) resultreview.Result {
		calls++
		return resultreview.Result{Decision: resultreview.DecisionHold, Rationale: "hold"}
	}))
	if calls != 0 || result.Blocker != nil || len(result.Deliverables) != 1 {
		t.Fatalf("disabled review result = %+v calls=%d", result, calls)
	}
}

func TestScheduledResultReviewIncludesConfiguredCompletionBoundary(t *testing.T) {
	input := scheduledReviewInput()
	input.Task.Goal = "微信公众号正式稿接口草稿箱"
	input.Task.WorkerPersonalGoal = "质量验收通过并持久化正式审批后立即退出。Marketing 等待人工审批并在批准后创建草稿。任务完成时不能宣称已有草稿。"
	input.ContextPack.WorkerPersonalGoal = "stale context must not override the task contract"
	packet := scheduledResultReviewInput(input, scheduledReviewResult())
	if packet.ExecutionContract != input.Task.WorkerPersonalGoal {
		t.Fatalf("configured completion boundary missing: %+v", packet)
	}
	input.Task.WorkerPersonalGoal = ""
	input.ContextPack.WorkerPersonalGoal = "Submit for approval; a human owns the later decision."
	if packet = scheduledResultReviewInput(input, scheduledReviewResult()); packet.ExecutionContract != input.ContextPack.WorkerPersonalGoal {
		t.Fatalf("context completion contract missing: %+v", packet)
	}
}

func TestScheduledReviewContractOmitsLocationsAndCredentials(t *testing.T) {
	value := "Submit for approval, not a later draft. /data/private/article.md https://private.invalid/review?token=example Bearer private-credential " + strings.Repeat("尾", 9000)
	redacted := scheduledReviewContractText(value)
	for _, private := range []string{"/data/private", "private.invalid", "private-credential"} {
		if strings.Contains(redacted, private) {
			t.Fatalf("private data leaked: %s", private)
		}
	}
	if !strings.Contains(redacted, "not a later draft") || len([]rune(redacted)) > 8000 {
		t.Fatal("completion boundary lost or packet unbounded")
	}
}

func TestScheduledResultReviewUsesPublicExplanationWithoutChangingCommittedSummary(t *testing.T) {
	input := scheduledReviewInput()
	original := scheduledReviewResult()
	public := "结论：已完成本轮结构检查及索引刷新。\n范围与数量：候选文档274份，结构检查125份，链接903条。\n具体结果：\n1. 发现10份孤立文档。\n2. 发现1个异常中心节点。\n限制与下一步：本轮未自动修复孤立文档或新增实体关系。"
	output := public + "\n```orrery-agent-result\n{\"version\":1,\"summary\":\"short receipt\",\"private\":\"hidden-manifest\"}\n```"
	normalizer := missionTaskAgentResultNormalizer{resultReviewer: scheduledResultReviewFunc(func(_ context.Context, packet resultreview.Input) resultreview.Result {
		if packet.Summary != public || len(packet.Deliverables) != 1 || len(packet.Evidence) != 1 {
			t.Fatalf("review omitted public facts or evidence: %+v", packet)
		}
		return resultreview.Result{Decision: resultreview.DecisionHold, Rationale: "An independent check is still required."}
	})}
	result, err := normalizer.finalizeScheduledResultArtifacts(context.Background(), input, original, output)
	if err != nil || result.Blocker == nil || len(result.Deliverables) != 0 || original.Summary.Text != "Report complete" {
		t.Fatalf("review must retain its hold boundary: result=%+v err=%v", result, err)
	}
}

func TestScheduledResultReviewFallsBackForPrivateOnlyOutput(t *testing.T) {
	input := scheduledReviewInput()
	result := reviewScheduledAgentResult(context.Background(), input, scheduledReviewResult(), scheduledResultReviewFunc(func(_ context.Context, packet resultreview.Input) resultreview.Result {
		if packet.Summary != "Report complete" {
			t.Fatalf("private output replaced committed summary: %q", packet.Summary)
		}
		return resultreview.Result{Decision: resultreview.DecisionApprove, Rationale: "Evidence confirmed."}
	}), "<think>private-reasoning</think>\n```orrery-agent-result\n{\"version\":1,\"summary\":\"hidden-manifest\"}\n```")
	if result.Blocker != nil || result.Summary.Text != "Report complete" {
		t.Fatalf("committed Agent result changed: %+v", result)
	}
}

func TestScheduledResultReviewPreservesOnlyVerifiedPublicSummaryFence(t *testing.T) {
	public := "结论：本次仅验收已完成的维护结果。\n范围与数量：候选文档274份，结构检查125份，链接903条。\n具体结果：\n1. 本轮结构检查发现10份孤立文档，尚未自动修复。\n2. 本轮结构检查发现1个异常中心节点，需要另行核查。\n限制与下一步：" + strings.Repeat("本轮仅检查结构并刷新索引，未自动修复孤立文档或新增实体关系。", 5)
	fenced := scheduledFeishuPublicSummaryFence + "\n" + public + "\n```"
	if _, ok := extractScheduledFeishuPublicSummary(fenced); !ok {
		t.Fatal("fixture must meet the public summary contract")
	}
	for _, test := range []struct {
		name, output, expected string
	}{
		{"verified fence", "<think>private-reasoning</think>\n" + fenced + "\n```orrery-agent-result\n{\"private\":\"hidden-manifest\"}\n```", fenced},
		{"missing fence", public, public},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := reviewScheduledAgentResult(context.Background(), scheduledReviewInput(), scheduledReviewResult(), scheduledResultReviewFunc(func(_ context.Context, packet resultreview.Input) resultreview.Result {
				if packet.Summary != test.expected || len([]rune(packet.Summary)) > 1000 {
					t.Fatalf("review changed the actual public format: %q", packet.Summary)
				}
				return resultreview.Result{Decision: resultreview.DecisionApprove, Rationale: "Public format and evidence verified."}
			}), test.output)
			if result.Blocker != nil || result.Summary.Text != "Report complete" || len(result.Deliverables) != 1 {
				t.Fatalf("review changed the committed result: %+v", result)
			}
		})
	}
}
