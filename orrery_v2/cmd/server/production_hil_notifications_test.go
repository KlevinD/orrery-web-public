package main

import (
	"strings"
	"testing"

	"orrery_v2/internal/app/hil"
)

func TestProductionHILNotificationDeliveryKeyIsStableAcrossRetries(t *testing.T) {
	first := productionHILNotificationDeliveryKey(hil.WorkflowNotification{IdempotencyKey: " approval-workflow-1 ", AttemptCount: 1})
	retried := productionHILNotificationDeliveryKey(hil.WorkflowNotification{IdempotencyKey: "approval-workflow-1", AttemptCount: 7})
	if first != "approval-workflow-1" || retried != first {
		t.Fatalf("notification delivery keys = first %q, retried %q", first, retried)
	}
}

func TestProductionHILApprovalNotificationUsesReadableBusinessMaterial(t *testing.T) {
	detail := hil.WorkflowInstanceDetail{
		Case:     hil.WorkflowCase{Requester: hil.ActorRef{Kind: hil.ActorAgent, ID: "jordan-belfort"}},
		Instance: hil.WorkflowInstance{ID: "workflow-1", MaterialHash: "sha256:material"},
		Decision: hil.DecisionRequest{
			RiskLevel: hil.RiskHigh,
			PayloadPreview: map[string]any{
				"approval_request_kind": "business", "material_hash": "sha256:material",
				"business_approval_material": map[string]any{
					"language": "zh-CN", "title": "公众号文章发布审批", "decision_request": "是否批准发布本期公众号文章？",
					"business_context": "本周产品更新需要对外同步。", "proposed_action": "使用主账号发布已完成校对的文章。",
					"expected_result": "用户能在公众号看到本期更新。", "impact_scope": "公众号主账号及其订阅用户。",
					"risks": []any{map[string]any{"description": "内容表述可能产生歧义。", "mitigation": "发布前由市场负责人完成终审。"}},
				},
				"artifacts": []any{map[string]any{"label": "公众号终稿", "url": "https://example.test/final", "kind": "document"}},
			},
		},
	}
	title, body := productionHILApprovalNotificationContent(detail, hil.WorkflowNotification{}, "https://orrery.test")
	if title != "待审批：公众号文章发布审批" {
		t.Fatalf("title = %q", title)
	}
	for _, expected := range []string{
		"**审批内容**", "需要你决定：是否批准发布本期公众号文章？", "业务背景：本周产品更新需要对外同步。",
		"拟执行动作：使用主账号发布已完成校对的文章。", "预期结果：用户能在公众号看到本期更新。",
		"影响范围：公众号主账号及其订阅用户。", "**风险与应对**", "内容表述可能产生歧义。", "发布前由市场负责人完成终审。",
		"[公众号终稿](https://example.test/final)", "/approvals?mode=approval&workflow_id=workflow-1",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("body missing %q:\n%s", expected, body)
		}
	}
	if strings.Contains(body, "view=my_tasks") {
		t.Fatalf("notification retained workflow-primary route: %s", body)
	}
	for _, raw := range []string{"decision_request", "business_context", "材料哈希：sha256:material"} {
		if strings.Contains(body, raw) {
			t.Fatalf("body contains raw technical field %q:\n%s", raw, body)
		}
	}
}
