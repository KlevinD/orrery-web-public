package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/hil"
	devicedirectory "orrery_v2/internal/device/directory"
	gwbindings "orrery_v2/internal/gateway/binding"
	gwconfig "orrery_v2/internal/gateway/config"
	platformdb "orrery_v2/internal/platform/db"
)

type recordingPersonalAssistantApprovalNotificationSource struct {
	candidates       []productionPersonalAssistantApprovalCandidate
	pending          bool
	targetsByTenant  map[string]productionPersonalAssistantWeixinTarget
	requestedTenants []string
}

type recordingPersonalAssistantApprovalReviewer struct {
	inputs []productionPersonalAssistantApprovalReviewRequest
	review productionPersonalAssistantApprovalReview
	err    error
}

func (r *recordingPersonalAssistantApprovalReviewer) Review(_ context.Context, input productionPersonalAssistantApprovalReviewRequest) (productionPersonalAssistantApprovalReview, error) {
	r.inputs = append(r.inputs, input)
	return r.review, r.err
}

func (s *recordingPersonalAssistantApprovalNotificationSource) ListPending(_ context.Context, limit, offset int) ([]productionPersonalAssistantApprovalCandidate, error) {
	if offset >= len(s.candidates) {
		return nil, nil
	}
	end := offset + limit
	if end > len(s.candidates) {
		end = len(s.candidates)
	}
	return append([]productionPersonalAssistantApprovalCandidate(nil), s.candidates[offset:end]...), nil
}

func (s *recordingPersonalAssistantApprovalNotificationSource) IsPending(context.Context, productionPersonalAssistantApprovalCandidate) (bool, error) {
	return s.pending, nil
}

func (s *recordingPersonalAssistantApprovalNotificationSource) WeixinTarget(_ context.Context, tenantID, _ string) (productionPersonalAssistantWeixinTarget, bool, error) {
	s.requestedTenants = append(s.requestedTenants, tenantID)
	target, ok := s.targetsByTenant[tenantID]
	return target, ok, nil
}

func TestPersonalAssistantApprovalNotificationUsesOwnerWeixinLink(t *testing.T) {
	candidate := productionPersonalAssistantApprovalCandidate{
		DecisionID: "decision-1", ApprovalTenant: productionHILTenantID,
		Requester:    hil.ActorRef{Kind: hil.ActorAgent, ID: "marketing"},
		ActionTypeID: string(actioncontract.ActionCommunicationWechatDraftCreate),
		Purpose:      "审核公众号内容", RiskLevel: hil.RiskMedium, RouteMode: hil.ModeHIL,
		Payload: map[string]any{
			"title":        "八月公众号文章",
			"content":      "确认标题、正文和封面后发布到公众号。",
			"risk_summary": "发布后会对外可见",
		},
		WorkflowID: "workflow-1", TaskID: "task-1", HumanProfileID: "human-ding",
		ExpiresAt: time.Date(2026, 8, 29, 13, 30, 0, 0, time.UTC),
	}
	source := &recordingPersonalAssistantApprovalNotificationSource{
		candidates: []productionPersonalAssistantApprovalCandidate{candidate}, pending: true,
		targetsByTenant: map[string]productionPersonalAssistantWeixinTarget{
			"orrery": {TenantID: "orrery", OwnerID: "identity-ding", AccountID: "weixin-ding"},
		},
	}
	profiles := &recordingProductionHILProfileDirectory{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"human-ding": {
			ID: "human-ding", AgentKind: agentcore.AgentKindHuman, Enabled: true,
			OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "orrery"}},
		},
		"marketing": {ID: "marketing", AgentKind: agentcore.AgentKindAI, DisplayName: "市场运营"},
	}}
	deliveries := &recordingProductionHILGatewayDeliveries{}
	sweeper := productionPersonalAssistantApprovalNotificationSweeper{
		store: source, profiles: profiles, deliveries: deliveries,
		replies:      &agentNoticeWriterStub{body: "公众号文章的内容和封面准备提交发布，发布后将对外可见，仍需要你确认。\nhttps://orrery.example.test/approvals?mode=hil&workflow_id=workflow-1"},
		publicOrigin: "https://orrery.example.test", batchSize: 20,
	}

	if err := sweeper.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(deliveries.commands) != 1 {
		t.Fatalf("commands = %+v", deliveries.commands)
	}
	command := deliveries.commands[0]
	if command.TenantID != "orrery" || command.HumanProfileID != "human-ding" || command.AccountID != "weixin-ding" || len(command.ChannelKeys) != 1 || command.ChannelKeys[0] != "weixin" {
		t.Fatalf("command route = %+v", command)
	}
	if command.IdempotencyKey != "personal-assistant:approval:v5:"+productionHILTenantID+":decision-1:task:task-1:recipient:human-ding" {
		t.Fatalf("idempotency key = %q", command.IdempotencyKey)
	}
	for _, expected := range []string{"公众号文章", "仍需要你确认", "https://orrery.example.test/approvals?mode=hil&workflow_id=workflow-1"} {
		if !strings.Contains(command.Body, expected) {
			t.Fatalf("notification body missing %q: %s", expected, command.Body)
		}
	}
	if strings.Contains(command.Body, "marketing") || strings.Contains(command.Body, "medium") {
		t.Fatalf("notification contains internal identifiers: %s", command.Body)
	}
	if len(source.requestedTenants) != 1 || source.requestedTenants[0] != "orrery" {
		t.Fatalf("binding tenants = %v", source.requestedTenants)
	}
}

func TestPersonalAssistantApprovalNotificationIncludesAssistantAnalysisAndFullMaterial(t *testing.T) {
	candidate := productionPersonalAssistantApprovalCandidate{
		DecisionID: "decision-reviewed", ApprovalTenant: "tenant-a", HumanProfileID: "human-ding",
		Requester: hil.ActorRef{Kind: hil.ActorAgent, ID: "marketing"}, RouteMode: hil.ModeApproval,
		Payload: map[string]any{
			"title": "发布九月客户通讯", "business_context": "九月产品更新已经完成，需要告知现有客户。",
			"decision_request": "是否同意向已订阅客户发送这期通讯？",
			"proposed_action":  "向 320 名已订阅客户发送邮件。",
			"expected_result":  "客户收到产品更新和使用指引。",
			"impact_scope":     "影响已订阅邮件的 320 名客户。",
		},
	}
	source := &recordingPersonalAssistantApprovalNotificationSource{
		candidates: []productionPersonalAssistantApprovalCandidate{candidate}, pending: true,
		targetsByTenant: map[string]productionPersonalAssistantWeixinTarget{
			"tenant-a": {TenantID: "tenant-a", OwnerID: "owner-ding", AccountID: "weixin-ding"},
		},
	}
	profiles := &recordingProductionHILProfileDirectory{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"human-ding": {
			ID: "human-ding", AgentKind: agentcore.AgentKindHuman, Enabled: true,
			OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "tenant-a"}},
		},
		"marketing": {ID: "marketing", AgentKind: agentcore.AgentKindAI, DisplayName: "市场运营"},
	}}
	reviewer := &recordingPersonalAssistantApprovalReviewer{review: productionPersonalAssistantApprovalReview{
		NotificationReason:   "它仍在等待你本人决定，未处理前发送动作会保持暂停。",
		PlainLanguageSummary: "这项审批要决定是否把九月产品通讯发给 320 名已订阅客户；同意后邮件会进入发送流程。",
		Recommendation:       "ask_for_clarification", RecommendationReason: "材料没有说明退订名单是否已经完成最终核对。",
		RiskNote: "名单不准确时可能联系到已退订客户。", MissingInformation: "最终收件人数和退订名单核对结果。",
	}}
	deliveries := &recordingProductionHILGatewayDeliveries{}
	sweeper := productionPersonalAssistantApprovalNotificationSweeper{
		store: source, profiles: profiles, deliveries: deliveries, reviewer: reviewer,
		replies:      &agentNoticeWriterStub{body: "九月通讯准备发给 320 名已订阅客户，仍需你决定。退订核对结果尚未提供，请先补齐后确认。"},
		publicOrigin: "https://orrery.example.test",
	}

	if err := sweeper.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(reviewer.inputs) != 0 || len(deliveries.commands) != 1 {
		t.Fatalf("review inputs=%+v deliveries=%+v", reviewer.inputs, deliveries.commands)
	}
	body := deliveries.commands[0].Body
	for _, expected := range []string{"320 名已订阅客户", "仍需你决定", "退订核对结果尚未提供"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("reviewed notification missing %q: %s", expected, body)
		}
	}
	if deliveries.commands[0].Title != "" || deliveries.commands[0].IdempotencyKey != "personal-assistant:approval:v5:tenant-a:decision-reviewed:recipient:human-ding" {
		t.Fatalf("reviewed notification formatting/key: %+v", deliveries.commands[0])
	}
}

func TestPersonalAssistantApprovalNotificationWithholdsWhenAgentFails(t *testing.T) {
	candidate := productionPersonalAssistantApprovalCandidate{
		DecisionID: "decision-fallback", ApprovalTenant: "tenant-a", HumanProfileID: "human-ding", RouteMode: hil.ModeApproval,
		Payload: map[string]any{"title": "核对客户名单", "decision_request": "是否允许继续处理客户名单？", "content": "核对 12 条客户记录。"},
	}
	source := &recordingPersonalAssistantApprovalNotificationSource{
		candidates: []productionPersonalAssistantApprovalCandidate{candidate}, pending: true,
		targetsByTenant: map[string]productionPersonalAssistantWeixinTarget{
			"tenant-a": {TenantID: "tenant-a", OwnerID: "owner-ding", AccountID: "weixin-ding"},
		},
	}
	profiles := &recordingProductionHILProfileDirectory{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"human-ding": {
			ID: "human-ding", AgentKind: agentcore.AgentKindHuman, Enabled: true,
			OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "tenant-a"}},
		},
	}}
	reviewer := &recordingPersonalAssistantApprovalReviewer{err: errors.New("model unavailable")}
	deliveries := &recordingProductionHILGatewayDeliveries{}
	sweeper := productionPersonalAssistantApprovalNotificationSweeper{store: source, profiles: profiles, deliveries: deliveries, reviewer: reviewer, replies: &agentNoticeWriterStub{err: errors.New("Agent unavailable")}}

	if err := sweeper.Sweep(context.Background()); err == nil {
		t.Fatal("Agent failure must remain observable")
	}
	if len(reviewer.inputs) != 0 || len(deliveries.commands) != 0 {
		t.Fatalf("fallback delivery = %+v review inputs=%+v", deliveries.commands, reviewer.inputs)
	}
}

func TestPersonalAssistantApprovalNotificationDoesNotReviewExistingDelivery(t *testing.T) {
	candidate := productionPersonalAssistantApprovalCandidate{
		DecisionID: "decision-existing", ApprovalTenant: "tenant-a", HumanProfileID: "human-ding", RouteMode: hil.ModeApproval,
	}
	key := productionPersonalAssistantApprovalNotificationKey(candidate)
	source := &recordingPersonalAssistantApprovalNotificationSource{
		candidates: []productionPersonalAssistantApprovalCandidate{candidate}, pending: true,
		targetsByTenant: map[string]productionPersonalAssistantWeixinTarget{
			"tenant-a": {TenantID: "tenant-a", OwnerID: "owner-ding", AccountID: "weixin-ding"},
		},
	}
	profiles := &recordingProductionHILProfileDirectory{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"human-ding": {
			ID: "human-ding", AgentKind: agentcore.AgentKindHuman, Enabled: true,
			OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "tenant-a"}},
		},
	}}
	reviewer := &recordingPersonalAssistantApprovalReviewer{}
	deliveries := &recordingProductionHILGatewayDeliveries{existingKeys: map[string]bool{key: true}}
	sweeper := productionPersonalAssistantApprovalNotificationSweeper{store: source, profiles: profiles, deliveries: deliveries, reviewer: reviewer, replies: &agentNoticeWriterStub{}}

	if err := sweeper.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(reviewer.inputs) != 0 || len(deliveries.commands) != 0 || len(deliveries.idempotencyLookups) != 1 {
		t.Fatalf("existing delivery was reprocessed: review=%+v deliveries=%+v lookups=%+v", reviewer.inputs, deliveries.commands, deliveries.idempotencyLookups)
	}
}

func TestPersonalAssistantApprovalNotificationForwardsAssignedSystemToolDecision(t *testing.T) {
	source := &recordingPersonalAssistantApprovalNotificationSource{
		candidates: []productionPersonalAssistantApprovalCandidate{{
			DecisionID: "decision-system-tool", ApprovalTenant: "orrery", HumanProfileID: "human-ding", RouteMode: hil.ModeApproval,
			ActionTypeID: string(actioncontract.ActionSystemFileChange),
			Payload:      map[string]any{"title": "更新月度报告", "path": "workspace/reports/monthly.md"},
		}},
		pending: true,
		targetsByTenant: map[string]productionPersonalAssistantWeixinTarget{
			"orrery": {TenantID: "orrery", OwnerID: "identity-ding", AccountID: "weixin-ding"},
		},
	}
	profiles := &recordingProductionHILProfileDirectory{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"human-ding": {
			ID: "human-ding", AgentKind: agentcore.AgentKindHuman, Enabled: true,
			OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "orrery"}},
		},
	}}
	deliveries := &recordingProductionHILGatewayDeliveries{}
	sweeper := productionPersonalAssistantApprovalNotificationSweeper{store: source, profiles: profiles, deliveries: deliveries, replies: &agentNoticeWriterStub{body: "月度报告更新还待你确认，请核对修改范围。"}}

	if err := sweeper.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(source.requestedTenants) != 1 || len(deliveries.commands) != 1 {
		t.Fatalf("assigned system tool was not forwarded: tenants=%v commands=%+v", source.requestedTenants, deliveries.commands)
	}
	body := deliveries.commands[0].Body
	for _, expected := range []string{"月度报告", "待你确认", "修改范围"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("system tool notification missing %q: %s", expected, body)
		}
	}
}

func TestPersonalAssistantApprovalNotificationPagesPastUnassignedRows(t *testing.T) {
	source := &recordingPersonalAssistantApprovalNotificationSource{
		candidates: []productionPersonalAssistantApprovalCandidate{
			{DecisionID: "unassigned", ApprovalTenant: "orrery", RouteMode: hil.ModeApproval},
			{DecisionID: "decision-a", ApprovalTenant: "orrery", HumanProfileID: "human-ding", RouteMode: hil.ModeApproval,
				ActionTypeID: "tool.unclassified.execute", Payload: map[string]any{"content": "整理本周客户反馈"}},
			{DecisionID: "decision-b", ApprovalTenant: "orrery", HumanProfileID: "human-ding", RouteMode: hil.ModeApproval,
				ActionTypeID: "tool.unclassified.execute", Payload: map[string]any{"content": "生成下周工作安排"}},
		},
		pending: true,
		targetsByTenant: map[string]productionPersonalAssistantWeixinTarget{
			"orrery": {TenantID: "orrery", OwnerID: "identity-ding", AccountID: "weixin-ding"},
		},
	}
	profiles := &recordingProductionHILProfileDirectory{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"human-ding": {
			ID: "human-ding", AgentKind: agentcore.AgentKindHuman, Enabled: true,
			OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "orrery"}},
		},
	}}
	deliveries := &recordingProductionHILGatewayDeliveries{}
	sweeper := productionPersonalAssistantApprovalNotificationSweeper{
		store: source, profiles: profiles, deliveries: deliveries, batchSize: 1,
		replies: &agentNoticeWriterStub{body: "有一项工作需要你确认范围。"},
	}

	if err := sweeper.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(deliveries.commands) != 2 {
		t.Fatalf("paged commands = %+v", deliveries.commands)
	}
	if deliveries.commands[0].Title != "" || deliveries.commands[1].Title != "" ||
		deliveries.commands[0].IdempotencyKey == deliveries.commands[1].IdempotencyKey {
		t.Fatalf("paged notification bodies = %+v", deliveries.commands)
	}
}

func TestPersonalAssistantApprovalNotificationRequiresPersonalAssistantWeixinTarget(t *testing.T) {
	source := &recordingPersonalAssistantApprovalNotificationSource{
		candidates: []productionPersonalAssistantApprovalCandidate{{
			DecisionID: "decision-2", ApprovalTenant: "orrery", HumanProfileID: "human-ding", RouteMode: hil.ModeApproval,
		}},
		pending: true, targetsByTenant: map[string]productionPersonalAssistantWeixinTarget{},
	}
	profiles := &recordingProductionHILProfileDirectory{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"human-ding": {
			ID: "human-ding", AgentKind: agentcore.AgentKindHuman, Enabled: true,
			OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "orrery"}},
		},
	}}
	deliveries := &recordingProductionHILGatewayDeliveries{}
	sweeper := productionPersonalAssistantApprovalNotificationSweeper{store: source, profiles: profiles, deliveries: deliveries}

	if err := sweeper.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(deliveries.commands) != 0 {
		t.Fatalf("unexpected commands = %+v", deliveries.commands)
	}
}

func TestPersonalAssistantApprovalNotificationRechecksPendingState(t *testing.T) {
	source := &recordingPersonalAssistantApprovalNotificationSource{
		candidates: []productionPersonalAssistantApprovalCandidate{{
			DecisionID: "decision-3", ApprovalTenant: "orrery", HumanProfileID: "human-ding", RouteMode: hil.ModeApproval,
		}},
		pending: false,
		targetsByTenant: map[string]productionPersonalAssistantWeixinTarget{
			"orrery": {TenantID: "orrery", OwnerID: "identity-ding", AccountID: "weixin-ding"},
		},
	}
	profiles := &recordingProductionHILProfileDirectory{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"human-ding": {
			ID: "human-ding", AgentKind: agentcore.AgentKindHuman, Enabled: true,
			OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "orrery"}},
		},
	}}
	deliveries := &recordingProductionHILGatewayDeliveries{}
	sweeper := productionPersonalAssistantApprovalNotificationSweeper{store: source, profiles: profiles, deliveries: deliveries}

	if err := sweeper.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(deliveries.commands) != 0 {
		t.Fatalf("unexpected commands = %+v", deliveries.commands)
	}
}

func TestPersonalAssistantApprovalNotificationUsesNaturalFallbacks(t *testing.T) {
	candidate := productionPersonalAssistantApprovalCandidate{
		Requester: hil.ActorRef{Kind: hil.ActorAgent, ID: "internal-agent-id"},
		RiskLevel: hil.RiskLevel("high"),
		RouteMode: hil.ModeApproval,
		Payload:   map[string]any{},
	}
	candidate.RequesterName = productionPersonalAssistantApprovalRequesterName(context.Background(), candidate, nil)
	title, body := productionPersonalAssistantApprovalNotificationContent(candidate, "https://orrery.example.test")
	for _, expected := range []string{
		"待审批：需要处理的审批", "申请人：Orrery 智能体", "风险等级：高",
		"材料提示：审批请求没有提供可核对的具体内容",
	} {
		if !strings.Contains(title+"\n"+body, expected) {
			t.Fatalf("natural notification missing %q: title=%s body=%s", expected, title, body)
		}
	}
	if strings.Contains(body, "internal-agent-id") || strings.Contains(body, "high") {
		t.Fatalf("natural notification contains internal values: %s", body)
	}
}

func TestPersonalAssistantApprovalNotificationHidesRuntimeIdentifiers(t *testing.T) {
	candidate := productionPersonalAssistantApprovalCandidate{
		Requester:     hil.ActorRef{Kind: hil.ActorAgent, ID: "agent-internal-id"},
		RequesterName: "大头",
		Action:        "tool.unclassified.execute",
		ActionTypeID:  "tool.unclassified.execute",
		Purpose:       "Runtime approval required for memory_diary_append",
		RiskLevel:     hil.RiskCritical,
		RouteMode:     hil.ModeApproval,
		WorkflowID:    "workflow-123",
		Payload: map[string]any{
			"approval_tool_name":         "memory_diary_append",
			"content":                    "## Ontology review\n- Evidence: `call_OkY123|fc_456`\n- Material: sha256:0123456789abcdef0123456789abcdef",
			"material_hash":              "sha256:material",
			"approval_request_id":        "call_OkY123|fc_456",
			"approval_run_id":            "responsibility-run-123",
			"approval_conversation_id":   "conversation-123",
			"approval_intent_recognized": false,
		},
	}

	title, body := productionPersonalAssistantApprovalNotificationContent(candidate, "https://orrery.example.test")
	combined := title + "\n" + body
	for _, expected := range []string{
		"待审批：写入工作记忆",
		"审批事项：写入工作记忆",
		"申请人：大头",
		"需要你决定：是否允许保存本次任务的工作总结，供后续追踪和复盘？",
		"具体操作：内容摘要：Ontology review",
		"影响范围：会新增或更新该智能体的内部工作记录，不会直接对外发布。",
		"风险等级：严重",
	} {
		if !strings.Contains(combined, expected) {
			t.Fatalf("readable notification missing %q: title=%s body=%s", expected, title, body)
		}
	}
	for _, forbidden := range []string{
		"Runtime approval", "memory_diary_append", "call_", "|fc_", "sha256:",
		"responsibility-run-", "approval_request_id", "0123456789abcdef",
	} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("readable notification contains internal value %q: title=%s body=%s", forbidden, title, body)
		}
	}
}

func TestPersonalAssistantApprovalNotificationShowsCompleteBusinessMaterial(t *testing.T) {
	candidate := productionPersonalAssistantApprovalCandidate{
		RequesterName: "市场运营",
		RiskLevel:     hil.RiskHigh,
		RouteMode:     hil.ModeApproval,
		Payload: map[string]any{"business_approval_material": map[string]any{
			"language": "zh-CN", "title": "发布九月客户通讯",
			"decision_request": "是否同意向已订阅客户发送这期通讯？",
			"business_context": "九月产品更新已经完成，需要告知现有客户。",
			"proposed_action":  "向 320 名已订阅客户发送邮件；api_key=do-not-send",
			"expected_result":  "客户收到产品更新和使用指引。",
			"impact_scope":     "影响已订阅邮件的 320 名客户。",
			"risks": []any{map[string]any{
				"description": "可能有客户不希望继续接收邮件。", "mitigation": "邮件中保留退订入口。",
			}},
		}},
	}

	_, body := productionPersonalAssistantApprovalNotificationContent(candidate, "")
	for _, expected := range []string{
		"审批事项：发布九月客户通讯", "申请人：市场运营", "业务背景：九月产品更新已经完成",
		"需要你决定：是否同意向已订阅客户发送这期通讯", "具体操作：向 320 名已订阅客户发送邮件",
		"同意后结果：客户收到产品更新和使用指引", "影响范围：影响已订阅邮件的 320 名客户",
		"风险与应对：风险 1：可能有客户不希望继续接收邮件；应对：邮件中保留退订入口",
		"我会结合上面的完整材料解释", "决定后，请单独回复“同意”或“拒绝”",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("business approval notification missing %q: %s", expected, body)
		}
	}
	if strings.Contains(body, "do-not-send") {
		t.Fatalf("business approval notification exposed a secret: %s", body)
	}
	for _, separated := range []string{
		"审批事项：发布九月客户通讯\n\n申请人：市场运营",
		"业务背景：九月产品更新已经完成，需要告知现有客户\n\n需要你决定：",
		"具体操作：向 320 名已订阅客户发送邮件；api_key=[已隐藏]\n\n同意后结果：",
		"影响范围：影响已订阅邮件的 320 名客户\n\n风险与应对：",
		"我会结合上面的完整材料解释。\n\n决定后，请单独回复“同意”或“拒绝”。",
	} {
		if !strings.Contains(body, separated) {
			t.Fatalf("plain-text approval paragraphs missing %q: %s", separated, body)
		}
	}
	material := productionPersonalAssistantApprovalConversationMaterial(candidate)
	for _, expected := range []string{"准备执行：向 320 名已订阅客户发送邮件", "风险与应对：风险 1", "同意后结果：客户收到产品更新"} {
		if !strings.Contains(material, expected) {
			t.Fatalf("private approval material missing %q: %s", expected, material)
		}
	}
	if strings.Contains(material, "do-not-send") {
		t.Fatalf("private approval material exposed a secret: %s", material)
	}
}

func TestPersonalAssistantApprovalNotificationUsesDedicatedDisplayMetadata(t *testing.T) {
	candidate := productionPersonalAssistantApprovalCandidate{
		ActionTypeID: "todo.execution_plan.approve", RouteMode: hil.ModeApproval,
		Payload: map[string]any{
			"todo_id": "internal-todo-id", "plan_digest": "sha256:0123456789abcdef0123456789abcdef",
			hil.ApprovalDisplayPayloadKey: map[string]any{
				"title": "准备客户发布说明", "approval_summary": "是否同意按方案准备客户发布说明？",
				"summary": "核对更新内容并生成发布说明。", "expected_result": "形成可供客户阅读的发布说明。",
				"impact_scope": "同意后执行 2 个步骤；拒绝后不会开始执行。",
				"steps": []any{
					map[string]any{"title": "核对更新内容", "description": "确认本次发布包含的功能。"},
					map[string]any{"title": "生成发布说明", "description": "整理为客户可读内容。"},
				},
			},
		},
	}

	_, body := productionPersonalAssistantApprovalNotificationContent(candidate, "")
	for _, expected := range []string{
		"审批事项：准备客户发布说明", "需要你决定：是否同意按方案准备客户发布说明",
		"具体操作：方案摘要：核对更新内容并生成发布说明", "执行步骤：共 2 个步骤",
		"同意后结果：形成可供客户阅读的发布说明", "影响范围：同意后执行 2 个步骤",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("display metadata notification missing %q: %s", expected, body)
		}
	}
	for _, forbidden := range []string{"internal-todo-id", "sha256:"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("display metadata notification exposed %q: %s", forbidden, body)
		}
	}
}

func TestPersonalAssistantWeixinTargetRequiresPrivateBindingAndEnabledPreference(t *testing.T) {
	ctx := context.Background()
	core, err := platformdb.Open(ctx, platformdb.Config{Driver: platformdb.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open core database: %v", err)
	}
	t.Cleanup(func() { _ = core.Close() })

	bindingKey := gwconfig.WeixinBindingKey("tenant-a", "wx-alice")
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.Weixin.Enabled = true
	cfg.Adapters.Weixin.Extra = map[string]any{
		"weixinUserBindings":        map[string]any{bindingKey: "human-alice"},
		"personalAssistantBindings": map[string]any{bindingKey: "identity-alice"},
		"weixinAccounts": map[string]any{
			bindingKey: map[string]any{
				"tenantId": "tenant-a", "userId": "wx-alice", "botId": "bot-alice", "token": "secret",
			},
		},
	}
	configStore := gwconfig.NewDatabaseStore(core, gwconfig.DatabaseStoreOptions{
		SecretKeyPath: filepath.Join(t.TempDir(), "gateway-config-key"),
	})
	if _, err := configStore.Save(cfg); err != nil {
		t.Fatalf("save Weixin config: %v", err)
	}

	bindingStore := gwbindings.NewStore(core)
	if err := bindingStore.EnsureSchema(ctx); err != nil {
		t.Fatalf("ensure common binding schema: %v", err)
	}
	if _, err := bindingStore.UpsertVerified(ctx, gwbindings.UpsertInput{
		TenantID: "tenant-a", ChannelKey: "weixin", AccountID: "bot-alice", ExternalTenantID: "tenant-a",
		ExternalUserID: "wx-alice", HumanProfileID: "human-alice",
	}); err != nil {
		t.Fatalf("upsert common Weixin binding: %v", err)
	}

	directoryStore, err := devicedirectory.NewSQLStore(ctx, core)
	if err != nil {
		t.Fatalf("initialize personal assistant store: %v", err)
	}
	directoryService, err := devicedirectory.NewService(devicedirectory.Options{
		Store: directoryStore,
		Now:   func() time.Time { return time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("initialize personal assistant service: %v", err)
	}
	source := productionPersonalAssistantApprovalNotificationStore{store: core}

	target, found, err := source.WeixinTarget(ctx, "tenant-a", "human-alice")
	if err != nil || !found || target.OwnerID != "identity-alice" || target.AccountID != "bot-alice" {
		t.Fatalf("private Weixin target = %+v, found=%v, err=%v", target, found, err)
	}

	if _, err := core.Exec(ctx, `DELETE FROM gateway_weixin_personal_assistant_bindings WHERE tenant_id = ? AND weixin_user_id = ?`, "tenant-a", "wx-alice"); err != nil {
		t.Fatalf("remove private binding: %v", err)
	}
	if _, found, err := source.WeixinTarget(ctx, "tenant-a", "human-alice"); err != nil || found {
		t.Fatalf("ordinary Weixin binding resolved as personal assistant target: found=%v err=%v", found, err)
	}
	if _, err := core.Exec(ctx, `
INSERT INTO gateway_weixin_personal_assistant_bindings (tenant_id, weixin_user_id, owner_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?)`, "tenant-a", "wx-alice", "identity-alice", int64(1), int64(1)); err != nil {
		t.Fatalf("restore private binding: %v", err)
	}

	prefs, err := directoryService.UpdatePersonalAssistantNotifications(ctx, devicedirectory.OwnerScope{
		TenantID: "tenant-a", OwnerID: "identity-alice",
	}, devicedirectory.UpdatePersonalAssistantNotificationsInput{ApprovalNotificationsEnabled: false, Revision: 0})
	if err != nil || prefs.ApprovalNotificationsEnabled {
		t.Fatalf("disable notifications = %+v, err=%v", prefs, err)
	}
	if _, found, err := source.WeixinTarget(ctx, "tenant-a", "human-alice"); err != nil || found {
		t.Fatalf("disabled preference resolved a target: found=%v err=%v", found, err)
	}
	if _, found, err := source.weixinTarget(ctx, "tenant-a", "human-alice", true); err != nil || !found {
		t.Fatalf("approval preference incorrectly disabled separately configured scheduled results: found=%v err=%v", found, err)
	}
}

func TestPersonalAssistantApprovalCandidatesPrioritizeNewAssignments(t *testing.T) {
	ctx := context.Background()
	core, err := platformdb.Open(ctx, platformdb.Config{Driver: platformdb.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open core database: %v", err)
	}
	t.Cleanup(func() { _ = core.Close() })
	for _, statement := range []string{
		`CREATE TABLE hil_decision_requests (
			id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, requester_kind TEXT NOT NULL, requester_id TEXT NOT NULL,
			action TEXT NOT NULL, action_type_id TEXT NOT NULL, purpose TEXT NOT NULL, risk_level TEXT NOT NULL,
			route_mode TEXT NOT NULL, payload_preview_json TEXT NOT NULL, created_at INTEGER NOT NULL,
			expires_at INTEGER, status TEXT NOT NULL
		)`,
		`CREATE TABLE hil_workflow_instances (id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, decision_request_id TEXT NOT NULL)`,
		`CREATE TABLE hil_workflow_tasks (
			id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, workflow_instance_id TEXT NOT NULL, status TEXT NOT NULL,
			current_assignee_kind TEXT NOT NULL, current_assignee_id TEXT NOT NULL, created_at INTEGER NOT NULL
		)`,
	} {
		if _, err := core.Exec(ctx, statement); err != nil {
			t.Fatalf("create HIL query fixture: %v", err)
		}
	}
	for _, fixture := range []struct {
		id, assignee string
		createdAt    int64
	}{
		{id: "decision-old", assignee: "human-alice", createdAt: 1},
		{id: "decision-new", assignee: "human-bob", createdAt: 2},
	} {
		payload := `{"approval_assigned_to":"` + fixture.assignee + `","title":"待审批事项"}`
		if _, err := core.Exec(ctx, `
INSERT INTO hil_decision_requests (
 id, tenant_id, requester_kind, requester_id, action, action_type_id, purpose, risk_level,
 route_mode, payload_preview_json, created_at, expires_at, status
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?)`, fixture.id, "tenant-a", hil.ActorAgent, "marketing",
			"tool.unclassified.execute", "tool.unclassified.execute", "审核未分类工具操作", hil.RiskMedium, hil.ModeApproval, payload, fixture.createdAt, hil.DecisionPending); err != nil {
			t.Fatalf("insert HIL decision %s: %v", fixture.id, err)
		}
	}

	candidates, err := (productionPersonalAssistantApprovalNotificationStore{store: core}).ListPending(ctx, 1, 0)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(candidates) != 1 || candidates[0].DecisionID != "decision-new" || candidates[0].HumanProfileID != "human-bob" {
		t.Fatalf("candidates = %+v", candidates)
	}
}
