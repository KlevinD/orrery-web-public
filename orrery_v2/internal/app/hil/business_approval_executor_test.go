package hil

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBusinessApprovalToolExecutorRejectsIncompleteMaterialBeforeCreatingDecision(t *testing.T) {
	creator := &recordingBusinessApprovalDecisionCreator{}
	executor := NewBusinessApprovalToolExecutor(creator, func() time.Time { return testRepositoryNow })
	arguments := validBusinessApprovalArguments()
	delete(arguments, "decision_request")

	_, err := executor.Execute(context.Background(), BusinessApprovalToolRequest{
		TenantID: "system", AgentID: "agent-marketing", Arguments: arguments,
	})
	if !errors.Is(err, ErrApprovalMaterialRequired) {
		t.Fatalf("Execute error = %v, want ErrApprovalMaterialRequired", err)
	}
	if creator.calls != 0 {
		t.Fatalf("CreateDecision calls = %d, want 0", creator.calls)
	}
}

func TestBusinessApprovalToolExecutorCreatesBoundHumanReadableDecision(t *testing.T) {
	creator := &recordingBusinessApprovalDecisionCreator{result: CreateDecisionResult{Decision: DecisionRequest{
		ID: "decision-1", Status: DecisionPending, Route: RouteDecision{Mode: ModeApproval},
	}}}
	executor := NewBusinessApprovalToolExecutor(creator, func() time.Time { return testRepositoryNow })

	result, err := executor.Execute(context.Background(), BusinessApprovalToolRequest{
		TenantID: "system", PrincipalID: "principal-marketing", AgentID: "agent-marketing", Arguments: validBusinessApprovalArguments(),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if creator.calls != 1 {
		t.Fatalf("CreateDecision calls = %d, want 1", creator.calls)
	}
	input := creator.input
	if input.Requester != (ActorRef{Kind: ActorAgent, ID: "agent-marketing"}) || input.Resource != (ObjectRef{Kind: "marketing_campaign", ID: "campaign-2026-08"}) {
		t.Fatalf("CreateDecision input identity/resource = %+v", input)
	}
	if input.Action != "business.approval.request" || input.ActionTypeID != "business.approval.request" || !input.ApprovalRequired || input.CouncilRequired || input.PreferredMode != "" {
		t.Fatalf("CreateDecision routing input = %+v", input)
	}
	if input.RiskLevel != RiskHigh || input.ArgsHash == "" || input.ExpiresAt != testRepositoryNow.Add(7*24*time.Hour) {
		t.Fatalf("CreateDecision risk/hash/expiry = %+v", input)
	}
	material, ok := input.PayloadPreview["business_approval_material"].(map[string]any)
	if !ok || material["title"] != "公众号文章发布审批" || material["language"] != "zh-CN" {
		t.Fatalf("business material payload = %#v", input.PayloadPreview["business_approval_material"])
	}
	if input.PayloadPreview["approval_request_kind"] != "business" || input.PayloadPreview["approval_tool_name"] != "request_business_approval" || input.PayloadPreview["material_hash"] == "" {
		t.Fatalf("approval metadata = %#v", input.PayloadPreview)
	}
	if result.DecisionID != "decision-1" || result.RouteMode != ModeApproval || result.MaterialHash != input.PayloadPreview["material_hash"] || result.Revision != 1 {
		t.Fatalf("result = %+v", result)
	}
}

func TestBusinessApprovalToolExecutorBindsArgsHashToMaterialAndArtifacts(t *testing.T) {
	creator := &recordingBusinessApprovalDecisionCreator{result: CreateDecisionResult{Decision: DecisionRequest{ID: "decision-1", Status: DecisionPending}}}
	executor := NewBusinessApprovalToolExecutor(creator, func() time.Time { return testRepositoryNow })
	request := BusinessApprovalToolRequest{TenantID: "system", AgentID: "agent-marketing", Arguments: validBusinessApprovalArguments()}
	if _, err := executor.Execute(context.Background(), request); err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	firstHash := creator.input.ArgsHash
	request.Arguments["artifacts"] = []any{map[string]any{"label": "终稿", "url": "https://example.test/revised", "kind": "document"}}
	if _, err := executor.Execute(context.Background(), request); err != nil {
		t.Fatalf("second Execute: %v", err)
	}
	if creator.input.ArgsHash == firstHash {
		t.Fatalf("args hash = %q after artifact change, want a different hash", creator.input.ArgsHash)
	}
}

type recordingBusinessApprovalDecisionCreator struct {
	calls  int
	input  CreateDecisionInput
	result CreateDecisionResult
}

func (c *recordingBusinessApprovalDecisionCreator) CreateDecision(_ context.Context, input CreateDecisionInput) (CreateDecisionResult, error) {
	c.calls++
	c.input = input
	return c.result, nil
}

func validBusinessApprovalArguments() map[string]any {
	return map[string]any{
		"title":            "公众号文章发布审批",
		"decision_request": "是否批准发布本期公众号文章？",
		"business_context": "本周产品更新需要对外同步。",
		"proposed_action":  "使用主账号发布已完成校对的文章。",
		"expected_result":  "用户能在公众号看到本期更新。",
		"impact_scope":     "公众号主账号及其订阅用户。",
		"risks": []any{map[string]any{
			"description": "内容表述可能产生歧义。",
			"mitigation":  "发布前由市场负责人完成终审。",
		}},
		"resource_kind":   "marketing_campaign",
		"resource_id":     "campaign-2026-08",
		"evidence_refs":   []any{"knowledge:campaign-brief"},
		"artifacts":       []any{map[string]any{"label": "终稿", "url": "https://example.test/final", "kind": "document"}},
		"idempotency_key": "approval:campaign-2026-08:v1",
	}
}
