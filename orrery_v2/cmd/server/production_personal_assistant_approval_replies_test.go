package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/hil"
	gwconfig "orrery_v2/internal/gateway/config"
	gwoutbound "orrery_v2/internal/gateway/outbound"
	gwprotocol "orrery_v2/internal/gateway/protocol"
	platformdb "orrery_v2/internal/platform/db"
)

type recordingPersonalAssistantApprovalReplyHandler struct {
	input  productionPersonalAssistantApprovalReplyInput
	result productionPersonalAssistantApprovalReplyResult
	err    error
}

type recordingPersonalAssistantApprovalContextProvider struct {
	input    productionPersonalAssistantApprovalContextInput
	approval productionPersonalAssistantApprovalContext
	found    bool
	err      error
	calls    int
}

func (p *recordingPersonalAssistantApprovalContextProvider) CurrentApproval(_ context.Context, input productionPersonalAssistantApprovalContextInput) (productionPersonalAssistantApprovalContext, bool, error) {
	p.calls++
	p.input = input
	return p.approval, p.found, p.err
}

func (h *recordingPersonalAssistantApprovalReplyHandler) ResolveReply(_ context.Context, input productionPersonalAssistantApprovalReplyInput) (productionPersonalAssistantApprovalReplyResult, error) {
	h.input = input
	if h.result.Decision == "" {
		h.result.Decision = input.Decision
	}
	return h.result, h.err
}

func personalAssistantApprovalReplyTestRunner(t *testing.T, discussions *fakeProductionGatewayDiscussionService, handler productionPersonalAssistantApprovalReplyHandler) *productionMessageGatewayRunner {
	t.Helper()
	bindingKey := gwconfig.WeixinBindingKey("tenant-a", "wx-user")
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.Weixin.Extra = map[string]any{
		"weixinUserBindings":        map[string]any{bindingKey: "human-alice"},
		"personalAssistantBindings": map[string]any{bindingKey: "alice-account"},
		"weixinAccounts": map[string]any{
			bindingKey: map[string]any{"tenantId": "tenant-a", "userId": "wx-user", "botId": "bot-a", "token": "secret"},
		},
	}
	runner := newProductionMessageGatewayRunner(discussions, cfg)
	runner.SetAgentDirectory(&fakeProductionGatewayAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
		gatewayTenantDirectoryEntry("human-alice", agentcore.AgentKindHuman, "tenant-a"),
	}})
	runner.SetPersonalAssistantApprovalReplyHandler(handler)
	return runner
}

func TestProductionMessageGatewayRunnerHandlesPersonalAssistantApprovalReply(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{}
	handler := &recordingPersonalAssistantApprovalReplyHandler{result: productionPersonalAssistantApprovalReplyResult{Title: "发布八月公众号文章"}}
	runner := personalAssistantApprovalReplyTestRunner(t, discussions, handler)
	contexts := &recordingPersonalAssistantApprovalContextProvider{found: true}
	runner.SetPersonalAssistantApprovalContextProvider(contexts)
	input := gwprotocol.SubmitTurnInput{
		SessionKey: "weixin:wx-user", ChannelKey: gwprotocol.ChannelWeixin,
		Message: "同意。", RunID: "weixin-run-message-1", Telemetry: verifiedWeixinTelemetry("wx-user"),
	}
	input.Telemetry["weixin_account_id"] = "bot-a"
	input.Telemetry["weixin_message_id"] = "message-1"
	events, err := runner.RunTurn(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	got := collectGatewayEvents(events)
	if len(got) != 2 || got[0].Text != "已同意：发布八月公众号文章。" || got[1].FinishReason != "approval_reply_approved" {
		t.Fatalf("approval reply events = %+v", got)
	}
	if handler.input.TenantID != "tenant-a" || handler.input.HumanProfileID != "human-alice" ||
		handler.input.AccountID != "bot-a" || handler.input.MessageID != "message-1" || handler.input.Decision != hil.WorkflowTaskApprove {
		t.Fatalf("approval reply input = %+v", handler.input)
	}
	if len(discussions.cmds) != 0 || contexts.calls != 0 {
		t.Fatalf("approval reply was sent to the personal assistant runtime: %+v", discussions.cmds)
	}
}

func TestProductionMessageGatewayRunnerHandlesQuotedApprovalRejection(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{}
	handler := &recordingPersonalAssistantApprovalReplyHandler{result: productionPersonalAssistantApprovalReplyResult{Title: "发送客户邮件"}}
	runner := personalAssistantApprovalReplyTestRunner(t, discussions, handler)
	input := gwprotocol.SubmitTurnInput{
		SessionKey: "weixin:wx-user", ChannelKey: gwprotocol.ChannelWeixin,
		Message: "[引用: 待审批：发送客户邮件]\n拒绝！", RunID: "weixin-run-message-2", Telemetry: verifiedWeixinTelemetry("wx-user"),
	}
	input.Telemetry["weixin_account_id"] = "bot-a"
	input.Telemetry["weixin_message_id"] = "message-2"
	events, err := runner.RunTurn(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	got := collectGatewayEvents(events)
	if len(got) != 2 || got[0].Text != "已拒绝：发送客户邮件。" || got[1].FinishReason != "approval_reply_rejected" || handler.input.Decision != hil.WorkflowTaskReject {
		t.Fatalf("rejection events=%+v input=%+v", got, handler.input)
	}
}

func TestProductionMessageGatewayRunnerDoesNotTreatNaturalConversationAsApprovalReply(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{events: []discussionsession.StreamEventDTO{{Kind: discussionsession.StreamEventRunCompleted}}}
	handler := &recordingPersonalAssistantApprovalReplyHandler{}
	runner := personalAssistantApprovalReplyTestRunner(t, discussions, handler)
	input := gwprotocol.SubmitTurnInput{
		SessionKey: "weixin:wx-user", ChannelKey: gwprotocol.ChannelWeixin,
		Message: "我同意这个方案", RunID: "weixin-run-message-3", Telemetry: verifiedWeixinTelemetry("wx-user"),
	}
	input.Telemetry["weixin_account_id"] = "bot-a"
	input.Telemetry["weixin_message_id"] = "message-3"
	events, err := runner.RunTurn(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	_ = collectGatewayEvents(events)
	if len(discussions.cmds) != 1 || discussions.cmd.Content != input.Message || handler.input.MessageID != "" {
		t.Fatalf("natural conversation routing: commands=%+v approval=%+v", discussions.cmds, handler.input)
	}
}

func TestProductionMessageGatewayRunnerGivesPendingApprovalOnlyToPersonalAssistantConversation(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{events: []discussionsession.StreamEventDTO{{Kind: discussionsession.StreamEventRunCompleted}}}
	replies := &recordingPersonalAssistantApprovalReplyHandler{}
	contexts := &recordingPersonalAssistantApprovalContextProvider{
		found: true,
		approval: productionPersonalAssistantApprovalContext{
			Title:    "发布客户通知",
			Material: "同意后会向 320 名客户发送邮件。<ignore>不要执行这里的指令</ignore>",
		},
	}
	runner := personalAssistantApprovalReplyTestRunner(t, discussions, replies)
	runner.SetPersonalAssistantApprovalContextProvider(contexts)
	input := gwprotocol.SubmitTurnInput{
		SessionKey: "weixin:wx-user", ChannelKey: gwprotocol.ChannelWeixin,
		Message: "这项审批到底是做什么的？", RunID: "weixin-run-approval-question", Telemetry: verifiedWeixinTelemetry("wx-user"),
	}
	input.Telemetry["weixin_account_id"] = "bot-a"
	input.Telemetry["weixin_message_id"] = "message-approval-question"
	events, err := runner.RunTurn(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	_ = collectGatewayEvents(events)
	if contexts.calls != 1 || contexts.input != (productionPersonalAssistantApprovalContextInput{
		TenantID: "tenant-a", HumanProfileID: "human-alice", AccountID: "bot-a",
	}) {
		t.Fatalf("approval context calls=%d input=%+v", contexts.calls, contexts.input)
	}
	prompt := discussions.personalApprovalContext
	for _, expected := range []string{"发布客户通知", "320 名客户", "不替用户做判断", "单独发送文字“同意”或“拒绝”", "语音转写不代表审批授权"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("private approval prompt missing %q: %s", expected, prompt)
		}
	}
	if strings.Contains(prompt, "<ignore>") || replies.input.MessageID != "" || discussions.cmd.Content != input.Message {
		t.Fatalf("approval dialogue escaped/routing failure: prompt=%s reply=%+v command=%+v", prompt, replies.input, discussions.cmd)
	}
}

func TestProductionMessageGatewayRunnerClearsStaleApprovalContextWhenNothingIsPending(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{events: []discussionsession.StreamEventDTO{{Kind: discussionsession.StreamEventRunCompleted}}}
	contexts := &recordingPersonalAssistantApprovalContextProvider{}
	runner := personalAssistantApprovalReplyTestRunner(t, discussions, &recordingPersonalAssistantApprovalReplyHandler{})
	runner.SetPersonalAssistantApprovalContextProvider(contexts)
	input := gwprotocol.SubmitTurnInput{
		SessionKey: "weixin:wx-user", ChannelKey: gwprotocol.ChannelWeixin,
		Message: "刚才那个审批还在吗？", RunID: "weixin-run-no-approval", Telemetry: verifiedWeixinTelemetry("wx-user"),
	}
	input.Telemetry["weixin_account_id"] = "bot-a"
	events, err := runner.RunTurn(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	_ = collectGatewayEvents(events)
	if contexts.calls != 1 || !strings.Contains(discussions.personalApprovalContext, `state="none"`) ||
		!strings.Contains(discussions.personalApprovalContext, "不要依据旧对话") {
		t.Fatalf("no-pending approval prompt=%q calls=%d", discussions.personalApprovalContext, contexts.calls)
	}
}

func TestProductionMessageGatewayRunnerDoesNotLetUnavailableApprovalContextBecomeGuesswork(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{events: []discussionsession.StreamEventDTO{{Kind: discussionsession.StreamEventRunCompleted}}}
	contexts := &recordingPersonalAssistantApprovalContextProvider{err: errors.New("database unavailable")}
	runner := personalAssistantApprovalReplyTestRunner(t, discussions, &recordingPersonalAssistantApprovalReplyHandler{})
	runner.SetPersonalAssistantApprovalContextProvider(contexts)
	input := gwprotocol.SubmitTurnInput{
		SessionKey: "weixin:wx-user", ChannelKey: gwprotocol.ChannelWeixin,
		Message: "帮我解释待审批内容", RunID: "weixin-run-approval-unavailable", Telemetry: verifiedWeixinTelemetry("wx-user"),
	}
	input.Telemetry["weixin_account_id"] = "bot-a"
	events, err := runner.RunTurn(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	_ = collectGatewayEvents(events)
	if !strings.Contains(discussions.personalApprovalContext, `state="unavailable"`) ||
		!strings.Contains(discussions.personalApprovalContext, "不要猜测审批内容") {
		t.Fatalf("unavailable approval prompt=%q", discussions.personalApprovalContext)
	}
}

func TestProductionMessageGatewayRunnerDoesNotAllowUnverifiedApprovalReply(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{}
	handler := &recordingPersonalAssistantApprovalReplyHandler{}
	runner := personalAssistantApprovalReplyTestRunner(t, discussions, handler)
	input := gwprotocol.SubmitTurnInput{
		SessionKey: "weixin:wx-user", ChannelKey: gwprotocol.ChannelWeixin,
		Message: "同意", RunID: "weixin-run-unverified", Telemetry: map[string]string{
			"weixin_sender_id":  "wx-user",
			"weixin_account_id": "bot-a",
			"weixin_message_id": "message-unverified",
		},
	}
	events, err := runner.RunTurn(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	got := collectGatewayEvents(events)
	if len(got) != 2 || got[0].Code != gwprotocol.ErrorCodeGatewayPermissionDenied || got[1].FinishReason != "unregistered" {
		t.Fatalf("unverified approval reply events = %+v", got)
	}
	if handler.input.MessageID != "" || len(discussions.cmds) != 0 {
		t.Fatalf("unverified approval reply reached protected services: approval=%+v commands=%+v", handler.input, discussions.cmds)
	}
}

func TestProductionMessageGatewayRunnerLeavesDigitalEmployeeApprovalWordsInConversation(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{events: []discussionsession.StreamEventDTO{{Kind: discussionsession.StreamEventRunCompleted}}}
	handler := &recordingPersonalAssistantApprovalReplyHandler{}
	bindingKey := gwconfig.WeixinBindingKey("tenant-a", "wx-user")
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.Weixin.Extra = map[string]any{
		"agentBindings":      map[string]any{bindingKey: "marketing"},
		"weixinUserBindings": map[string]any{bindingKey: "human-alice"},
		"weixinAccounts": map[string]any{
			bindingKey: map[string]any{"tenantId": "tenant-a", "userId": "wx-user", "botId": "bot-a", "token": "secret"},
		},
	}
	runner := newProductionMessageGatewayRunner(discussions, cfg)
	runner.SetAgentDirectory(&fakeProductionGatewayAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
		gatewayTenantDirectoryEntry("human-alice", agentcore.AgentKindHuman, "tenant-a"),
		gatewayTenantDirectoryEntry("marketing", agentcore.AgentKindAI, "tenant-a"),
	}})
	runner.SetPersonalAssistantApprovalReplyHandler(handler)
	contexts := &recordingPersonalAssistantApprovalContextProvider{
		found: true, approval: productionPersonalAssistantApprovalContext{Title: "不得共享", Material: "私有审批材料"},
	}
	runner.SetPersonalAssistantApprovalContextProvider(contexts)
	input := gwprotocol.SubmitTurnInput{
		SessionKey: "weixin:wx-user", ChannelKey: gwprotocol.ChannelWeixin,
		Message: "同意", RunID: "weixin-run-digital-employee", Telemetry: verifiedWeixinTelemetry("wx-user"),
	}
	input.Telemetry["weixin_account_id"] = "bot-a"
	input.Telemetry["weixin_message_id"] = "message-digital-employee"
	events, err := runner.RunTurn(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	_ = collectGatewayEvents(events)
	if handler.input.MessageID != "" || len(discussions.cmds) != 1 || discussions.cmd.Content != "同意" || discussions.cmd.DirectTargetID != "marketing" ||
		contexts.calls != 0 || discussions.personalApprovalContext != "" {
		t.Fatalf("digital employee routing: approval=%+v commands=%+v", handler.input, discussions.cmds)
	}
}

func TestProductionMessageGatewayRunnerMarksPersonalAssistantApprovalPromptForNaturalReply(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{events: []discussionsession.StreamEventDTO{
		{Kind: discussionsession.StreamEventApprovalRequested, Approval: &discussionsession.EventDTO{
			EventID: "approval-event-1", ConversationID: "conversation-1", Status: "pending",
			Metadata: map[string]string{"approval_request_id": "internal-call-1", "approval_tool_name": "internal_tool_name"},
		}},
		{Kind: discussionsession.StreamEventRunCompleted},
	}}
	runner := personalAssistantApprovalReplyTestRunner(t, discussions, &recordingPersonalAssistantApprovalReplyHandler{})
	input := gwprotocol.SubmitTurnInput{
		SessionKey: "weixin:wx-user", ChannelKey: gwprotocol.ChannelWeixin,
		Message: "执行公众号任务", RunID: "weixin-run-message-4", Telemetry: verifiedWeixinTelemetry("wx-user"),
	}
	input.Telemetry["weixin_account_id"] = "bot-a"
	input.Telemetry["weixin_message_id"] = "message-4"
	events, err := runner.RunTurn(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	got := collectGatewayEvents(events)
	if len(got) != 2 || got[0].Type != gwprotocol.EventPermissionRequest || got[1].Type != gwprotocol.EventTurnCompleted {
		t.Fatalf("personal assistant approval prompt events = %+v", got)
	}
	payload, _ := got[0].Payload.(map[string]string)
	if payload["personal_assistant_reply"] != "notification" {
		t.Fatalf("personal assistant approval prompt events = %+v", got)
	}
}

func TestPersonalAssistantApprovalReplyDecisionRequiresExactAnswer(t *testing.T) {
	for _, test := range []struct {
		message  string
		decision hil.WorkflowTaskDecision
		ok       bool
	}{
		{message: "同意", decision: hil.WorkflowTaskApprove, ok: true},
		{message: "拒绝。", decision: hil.WorkflowTaskReject, ok: true},
		{message: "[引用: 审批内容]\n同意", decision: hil.WorkflowTaskApprove, ok: true},
		{message: "我同意", ok: false},
		{message: "不同意", ok: false},
		{message: "同意，但先等等", ok: false},
	} {
		decision, ok := productionPersonalAssistantApprovalReplyDecision(test.message)
		if decision != test.decision || ok != test.ok {
			t.Fatalf("message %q => decision=%q ok=%v", test.message, decision, ok)
		}
	}
}

type staticPersonalAssistantApprovalReplyClaimStore struct {
	claim productionPersonalAssistantApprovalReplyClaim
	found bool
	err   error
	input productionPersonalAssistantApprovalReplyInput
}

func (s *staticPersonalAssistantApprovalReplyClaimStore) ClaimLatest(_ context.Context, input productionPersonalAssistantApprovalReplyInput) (productionPersonalAssistantApprovalReplyClaim, bool, error) {
	s.input = input
	return s.claim, s.found, s.err
}

type recordingPersonalAssistantApprovalDecisionService struct {
	resolveTenant string
	resolveID     string
	resolveActor  hil.ActorRef
	resolveInput  hil.ResolveDecisionInput
	taskTenant    string
	taskID        string
	taskActor     hil.ActorRef
	taskInput     hil.DecideWorkflowTaskInput
	err           error
}

func (s *recordingPersonalAssistantApprovalDecisionService) ResolveDecision(_ context.Context, tenantID, decisionID string, actor hil.ActorRef, input hil.ResolveDecisionInput) (hil.DecisionResolution, error) {
	s.resolveTenant, s.resolveID, s.resolveActor, s.resolveInput = tenantID, decisionID, actor, input
	return hil.DecisionResolution{}, s.err
}

func (s *recordingPersonalAssistantApprovalDecisionService) DecideWorkflowTask(_ context.Context, tenantID, taskID string, actor hil.ActorRef, input hil.DecideWorkflowTaskInput) (hil.WorkflowInstanceDetail, error) {
	s.taskTenant, s.taskID, s.taskActor, s.taskInput = tenantID, taskID, actor, input
	return hil.WorkflowInstanceDetail{}, s.err
}

func TestPersonalAssistantApprovalReplyServiceResolvesWorkflowRejection(t *testing.T) {
	claims := &staticPersonalAssistantApprovalReplyClaimStore{found: true, claim: productionPersonalAssistantApprovalReplyClaim{
		ApprovalTenant: "tenant-a", DecisionID: "decision-1", TaskID: "task-1", ExpectedWorkflowVersion: 7,
		Decision: hil.WorkflowTaskReject, Title: "发布公众号文章", IdempotencyKey: "reply-key-1",
	}}
	decisions := &recordingPersonalAssistantApprovalDecisionService{}
	service := productionPersonalAssistantApprovalReplyService{claims: claims, decisions: decisions}
	result, err := service.ResolveReply(context.Background(), productionPersonalAssistantApprovalReplyInput{HumanProfileID: "human-alice"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Title != "发布公众号文章" || result.Decision != hil.WorkflowTaskReject || decisions.taskTenant != "tenant-a" ||
		decisions.taskID != "task-1" || decisions.taskActor != (hil.ActorRef{Kind: hil.ActorHuman, ID: "human-alice"}) ||
		decisions.taskInput.Decision != hil.WorkflowTaskReject || decisions.taskInput.Reason != "用户通过微信回复拒绝" ||
		decisions.taskInput.IdempotencyKey != "reply-key-1" || decisions.taskInput.ExpectedInstanceVersion != 7 {
		t.Fatalf("result=%+v task actor=%+v input=%+v", result, decisions.taskActor, decisions.taskInput)
	}
}

func TestPersonalAssistantApprovalReplyServiceResolvesDirectApproval(t *testing.T) {
	claims := &staticPersonalAssistantApprovalReplyClaimStore{found: true, claim: productionPersonalAssistantApprovalReplyClaim{
		ApprovalTenant: productionHILTenantID, DecisionID: "decision-2", Decision: hil.WorkflowTaskApprove,
		Title: "发送客户邮件", IdempotencyKey: "reply-key-2",
	}}
	decisions := &recordingPersonalAssistantApprovalDecisionService{}
	service := productionPersonalAssistantApprovalReplyService{claims: claims, decisions: decisions}
	_, err := service.ResolveReply(context.Background(), productionPersonalAssistantApprovalReplyInput{HumanProfileID: "human-alice"})
	if err != nil {
		t.Fatal(err)
	}
	if decisions.resolveTenant != productionHILTenantID || decisions.resolveID != "decision-2" ||
		decisions.resolveActor != (hil.ActorRef{Kind: hil.ActorHuman, ID: "human-alice"}) ||
		decisions.resolveInput.Decision != "approve" || decisions.resolveInput.Reason != "" || decisions.resolveInput.IdempotencyKey != "reply-key-2" {
		t.Fatalf("direct actor=%+v input=%+v", decisions.resolveActor, decisions.resolveInput)
	}
}

func TestPersonalAssistantApprovalReplyServiceReportsMissingAndStaleTargets(t *testing.T) {
	service := productionPersonalAssistantApprovalReplyService{
		claims: &staticPersonalAssistantApprovalReplyClaimStore{}, decisions: &recordingPersonalAssistantApprovalDecisionService{},
	}
	if _, err := service.ResolveReply(context.Background(), productionPersonalAssistantApprovalReplyInput{}); !errors.Is(err, errPersonalAssistantApprovalReplyNotFound) {
		t.Fatalf("missing target error = %v", err)
	}
	service.claims = &staticPersonalAssistantApprovalReplyClaimStore{found: true, claim: productionPersonalAssistantApprovalReplyClaim{
		ApprovalTenant: "tenant-a", DecisionID: "decision-stale", Decision: hil.WorkflowTaskApprove, IdempotencyKey: "reply-stale",
	}}
	service.decisions = &recordingPersonalAssistantApprovalDecisionService{err: hil.ErrConflict}
	if _, err := service.ResolveReply(context.Background(), productionPersonalAssistantApprovalReplyInput{HumanProfileID: "human-alice"}); !errors.Is(err, errPersonalAssistantApprovalReplyNotActionable) {
		t.Fatalf("stale target error = %v", err)
	}
}

func TestPersonalAssistantApprovalReplyStoreClaimsLatestPendingDeliveryIdempotently(t *testing.T) {
	ctx := context.Background()
	core, err := platformdb.Open(ctx, platformdb.Config{Driver: platformdb.DriverMemory, Name: t.Name() + "-core"})
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := platformdb.Open(ctx, platformdb.Config{Driver: platformdb.DriverMemory, Name: t.Name() + "-jobs"})
	if err != nil {
		_ = core.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close(); _ = jobs.Close() })
	for _, statement := range []string{
		`CREATE TABLE hil_decision_requests (
		 id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, requester_kind TEXT NOT NULL, requester_id TEXT NOT NULL,
		 action TEXT NOT NULL, action_type_id TEXT NOT NULL, purpose TEXT NOT NULL, risk_level TEXT NOT NULL,
		 route_mode TEXT NOT NULL, payload_preview_json TEXT NOT NULL, created_at INTEGER NOT NULL,
		 expires_at INTEGER, status TEXT NOT NULL
		)`,
		`CREATE TABLE hil_workflow_instances (
		 id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, decision_request_id TEXT NOT NULL,
		 status TEXT NOT NULL, version INTEGER NOT NULL
		)`,
		`CREATE TABLE hil_workflow_tasks (
		 id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, workflow_instance_id TEXT NOT NULL,
		 current_assignee_kind TEXT NOT NULL, current_assignee_id TEXT NOT NULL,
		 status TEXT NOT NULL, deadline_at INTEGER
		)`,
	} {
		if _, err := core.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := gwoutbound.NewService(jobs, nil, nil).EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	for index, fixture := range []struct {
		id, title string
	}{
		{id: "decision-old", title: "较早审批"},
		{id: "decision-new", title: "最新审批"},
	} {
		payload := `{"approval_assigned_to":"human-alice","title":"` + fixture.title + `"}`
		if _, err := core.Exec(ctx, `INSERT INTO hil_decision_requests (
			id, tenant_id, requester_kind, requester_id, action, action_type_id, purpose, risk_level,
			route_mode, payload_preview_json, created_at, expires_at, status
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?)`, fixture.id, "tenant-a", hil.ActorAgent, "marketing",
			"tool.unclassified.execute", "tool.unclassified.execute", fixture.title, hil.RiskMedium,
			hil.ModeApproval, payload, int64(index+1), hil.DecisionPending); err != nil {
			t.Fatal(err)
		}
		candidate := productionPersonalAssistantApprovalCandidate{
			ApprovalTenant: "tenant-a", DecisionID: fixture.id, HumanProfileID: "human-alice",
		}
		deliveryBody := fixture.title
		if fixture.id == "decision-new" {
			deliveryBody = "我已经看过这项审批。\n\n我的理解：这是最新审批的通俗解释。\n\n我的建议：根据现有材料，建议拒绝。"
		}
		if _, err := jobs.Exec(ctx, `INSERT INTO gateway_message_deliveries (
			id, tenant_id, channel_key, account_id, binding_id, human_profile_id, external_tenant_id, recipient_id,
			event_type, title, body, message_format, idempotency_key, status, attempts, external_message_id,
			last_error, created_at, updated_at, delivered_at
		) VALUES (?, ?, 'weixin', ?, ?, ?, '', ?, 'approval', ?, ?, 'text', ?, 'delivered', 1, ?, '', ?, ?, ?)`,
			"delivery-"+fixture.id, "tenant-a", "bot-a", "binding-a", "human-alice", "wx-user",
			fixture.title, deliveryBody, productionPersonalAssistantApprovalNotificationKey(candidate), "receipt-"+fixture.id,
			int64(index+1), int64(index+1), int64(index+1)); err != nil {
			t.Fatal(err)
		}
	}
	store := newProductionPersonalAssistantApprovalReplyStore(core, jobs)
	store.now = func() time.Time { return time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC) }
	contextInput := productionPersonalAssistantApprovalContextInput{
		TenantID: "tenant-a", HumanProfileID: "human-alice", AccountID: "bot-a",
	}
	current, found, err := store.CurrentApproval(ctx, contextInput)
	if err != nil || !found || current.Title != "最新审批" || !strings.Contains(current.Material, "审批事项：最新审批") ||
		!strings.Contains(current.Material, "我的理解：这是最新审批的通俗解释") {
		t.Fatalf("current approval=%+v found=%v err=%v", current, found, err)
	}
	for _, outsideScope := range []productionPersonalAssistantApprovalContextInput{
		{TenantID: "tenant-b", HumanProfileID: "human-alice", AccountID: "bot-a"},
		{TenantID: "tenant-a", HumanProfileID: "human-bob", AccountID: "bot-a"},
		{TenantID: "tenant-a", HumanProfileID: "human-alice", AccountID: "bot-b"},
	} {
		if leaked, found, err := store.CurrentApproval(ctx, outsideScope); err != nil || found || leaked.Material != "" {
			t.Fatalf("approval context leaked outside scope: input=%+v context=%+v found=%v err=%v", outsideScope, leaked, found, err)
		}
	}
	firstInput := productionPersonalAssistantApprovalReplyInput{
		TenantID: "tenant-a", HumanProfileID: "human-alice", AccountID: "bot-a", MessageID: "message-1", Decision: hil.WorkflowTaskApprove,
	}
	first, found, err := store.ClaimLatest(ctx, firstInput)
	if err != nil || !found || first.DecisionID != "decision-new" || first.Title != "最新审批" || first.Decision != hil.WorkflowTaskApprove {
		t.Fatalf("first claim=%+v found=%v err=%v", first, found, err)
	}
	if _, err := core.Exec(ctx, `UPDATE hil_decision_requests SET status = ? WHERE id = ?`, hil.DecisionResolved, "decision-new"); err != nil {
		t.Fatal(err)
	}
	current, found, err = store.CurrentApproval(ctx, contextInput)
	if err != nil || !found || current.Title != "较早审批" {
		t.Fatalf("next current approval=%+v found=%v err=%v", current, found, err)
	}
	replayedInput := firstInput
	replayedInput.Decision = hil.WorkflowTaskReject
	replayed, found, err := store.ClaimLatest(ctx, replayedInput)
	if err != nil || !found || replayed.DecisionID != first.DecisionID || replayed.Decision != hil.WorkflowTaskApprove || replayed.IdempotencyKey != first.IdempotencyKey {
		t.Fatalf("replayed claim=%+v found=%v err=%v", replayed, found, err)
	}
	secondInput := firstInput
	secondInput.MessageID = "message-2"
	secondInput.Decision = hil.WorkflowTaskReject
	second, found, err := store.ClaimLatest(ctx, secondInput)
	if err != nil || !found || second.DecisionID != "decision-old" || second.Decision != hil.WorkflowTaskReject {
		t.Fatalf("second claim=%+v found=%v err=%v", second, found, err)
	}

	workflowPayload := `{"approval_assigned_to":"human-alice","title":"工作流审批"}`
	if _, err := core.Exec(ctx, `INSERT INTO hil_decision_requests (
		id, tenant_id, requester_kind, requester_id, action, action_type_id, purpose, risk_level,
		route_mode, payload_preview_json, created_at, expires_at, status
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?)`, "decision-workflow", "tenant-a", hil.ActorAgent, "marketing",
		"business.approval.request", "business.approval.request", "工作流审批", hil.RiskHigh,
		hil.ModeApproval, workflowPayload, int64(3), hil.DecisionPending); err != nil {
		t.Fatal(err)
	}
	if _, err := core.Exec(ctx, `INSERT INTO hil_workflow_instances (id, tenant_id, decision_request_id, status, version)
		VALUES (?, ?, ?, ?, ?)`, "workflow-1", "tenant-a", "decision-workflow", hil.WorkflowRunning, int64(9)); err != nil {
		t.Fatal(err)
	}
	if _, err := core.Exec(ctx, `INSERT INTO hil_workflow_tasks (
		id, tenant_id, workflow_instance_id, current_assignee_kind, current_assignee_id, status, deadline_at
	) VALUES (?, ?, ?, ?, ?, ?, NULL)`, "task-1", "tenant-a", "workflow-1", hil.ActorHuman, "human-alice", hil.TaskPending); err != nil {
		t.Fatal(err)
	}
	workflowCandidate := productionPersonalAssistantApprovalCandidate{
		ApprovalTenant: "tenant-a", DecisionID: "decision-workflow", TaskID: "task-1", HumanProfileID: "human-alice",
	}
	if _, err := jobs.Exec(ctx, `INSERT INTO gateway_message_deliveries (
		id, tenant_id, channel_key, account_id, binding_id, human_profile_id, external_tenant_id, recipient_id,
		event_type, title, body, message_format, idempotency_key, status, attempts, external_message_id,
		last_error, created_at, updated_at, delivered_at
	) VALUES (?, ?, 'weixin', ?, ?, ?, '', ?, 'approval', ?, ?, 'text', ?, 'delivered', 1, ?, '', ?, ?, ?)`,
		"delivery-workflow", "tenant-a", "bot-a", "binding-a", "human-alice", "wx-user", "工作流审批", "工作流审批",
		productionPersonalAssistantApprovalNotificationKey(workflowCandidate), "receipt-workflow", int64(3), int64(3), int64(3)); err != nil {
		t.Fatal(err)
	}
	workflowInput := firstInput
	workflowInput.MessageID = "message-3"
	workflow, found, err := store.ClaimLatest(ctx, workflowInput)
	if err != nil || !found || workflow.DecisionID != "decision-workflow" || workflow.TaskID != "task-1" ||
		workflow.ExpectedWorkflowVersion != 9 || workflow.Title != "工作流审批" {
		t.Fatalf("workflow claim=%+v found=%v err=%v", workflow, found, err)
	}
}

func TestParsePersonalAssistantApprovalNotificationKey(t *testing.T) {
	for _, key := range []string{
		"personal-assistant:approval:v4:tenant:division:decision-1:task:task-1:recipient:human-alice",
		"personal-assistant:approval:v3:tenant:division:decision-1:task:task-1:recipient:human-alice",
		"personal-assistant:approval:v2:tenant:division:decision-1:task:task-1:recipient:human-alice",
		"personal-assistant:approval:tenant:division:decision-1:task:task-1:recipient:human-alice",
	} {
		reference, ok := parseProductionPersonalAssistantApprovalNotificationKey(key)
		if !ok || reference.ApprovalTenant != "tenant:division" || reference.DecisionID != "decision-1" || reference.TaskID != "task-1" || reference.HumanProfileID != "human-alice" {
			t.Fatalf("key=%q reference=%+v ok=%v", key, reference, ok)
		}
	}
	for _, invalid := range []string{"", "other:key", "personal-assistant:approval:tenant-only"} {
		if reference, ok := parseProductionPersonalAssistantApprovalNotificationKey(invalid); ok || strings.TrimSpace(reference.DecisionID) != "" {
			t.Fatalf("invalid key %q parsed as %+v", invalid, reference)
		}
	}
}
