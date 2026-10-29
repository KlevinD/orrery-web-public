package discussionsession

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/app/runtimeapproval"
)

type recordingRuntimeHILService struct {
	mu     sync.Mutex
	input  hil.CreateDecisionInput
	result hil.CreateDecisionResult
	calls  int
	err    error
}

type recordingRuntimeHILResolutionService struct {
	recordingRuntimeHILService
	resolution hil.DecisionResolution
	lookupErr  error
}

type blockingRuntimeHILResolutionService struct {
	recordingRuntimeHILService
	entered chan struct{}
	release chan struct{}
}

type recordingDetachedRuntimeHILResolver struct {
	calls        int
	resolution   hil.DecisionResolution
	conversation ConversationRecordView
	event        EventRecord
}

func (r *recordingDetachedRuntimeHILResolver) ResumeDetachedHILRuntimeApproval(_ context.Context, resolution hil.DecisionResolution, conversation ConversationRecordView, event EventRecord) error {
	r.calls++
	r.resolution = resolution
	r.conversation = conversation
	r.event = event
	return nil
}

func (s *blockingRuntimeHILResolutionService) GetDecisionResolution(_ context.Context, _, _ string) (hil.DecisionResolution, error) {
	s.entered <- struct{}{}
	<-s.release
	return hil.DecisionResolution{
		Decision: hil.DecisionRequest{ID: "hil-reconcile-serial", Status: hil.DecisionPending},
	}, nil
}

func TestRuntimeApprovalCoordinatorSerializesHILReconciliation(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	saveRuntimeHILTestConversation(t, repo, "conv-reconcile-serial", "run-reconcile-serial")
	if err := repo.SaveEvent(ctx, EventRecord{
		EventID: "approval-reconcile-serial", ConversationID: "conv-reconcile-serial", Kind: discussionRuntimeApprovalEventKind, Status: "pending",
		Metadata: map[string]string{
			"approval_hil_required": "true", "hil_decision_id": "hil-reconcile-serial", "approval_run_id": "run-reconcile-serial",
		}, CreatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	hilService := &blockingRuntimeHILResolutionService{
		entered: make(chan struct{}, 2),
		release: make(chan struct{}, 2),
	}
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{
		Repository: repo, Clock: fixedClock{now: testTime}, HIL: hilService,
	})

	results := make(chan error, 2)
	for range 2 {
		go func() { results <- coordinator.ReconcileHILRuntimeApprovals(ctx) }()
	}
	select {
	case <-hilService.entered:
	case <-time.After(time.Second):
		t.Fatal("first reconciliation did not reach HIL lookup")
	}
	select {
	case <-hilService.entered:
		t.Fatal("concurrent reconciliation entered HIL lookup before the first completed")
	case <-time.After(50 * time.Millisecond):
	}
	hilService.release <- struct{}{}
	select {
	case <-hilService.entered:
	case <-time.After(time.Second):
		t.Fatal("second reconciliation did not run after the first completed")
	}
	hilService.release <- struct{}{}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("ReconcileHILRuntimeApprovals: %v", err)
		}
	}
}

func TestRuntimeApprovalCoordinatorPersistsWechatMaterialBeforeHILCreate(t *testing.T) {
	root := t.TempDir()
	prepareWechatMaterialFiles(t, root)
	repo := newMemoryRepository(t)
	hilService := &recordingRuntimeHILService{result: hil.CreateDecisionResult{Decision: hil.DecisionRequest{
		ID: "hil-wechat-material", Status: hil.DecisionPending, Route: hil.RouteDecision{Mode: hil.ModeHIL}, ExpiresAt: testTime.Add(time.Hour),
	}}}
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{
		Repository: repo, Clock: fixedClock{now: testTime}, HIL: hilService,
	})
	saveRuntimeHILTestConversation(t, repo, "conv-wechat-material", "run-wechat-material")
	request := runtimeWechatApprovalRequest(t, root, nil)
	request.Source = "pi"
	request.RequestID = "approval-wechat-material"
	request.Kind = runtimecore.ApprovalKindTool
	request.Metadata = map[string]string{"approval_force_manual": "true"}
	ctx := contextWithRuntimeApproval(context.Background(), runtimeApprovalContext{
		ConversationID: "conv-wechat-material", RunID: "run-wechat-material", AgentID: "jordan", RuntimeKind: "pi",
	})
	if err := coordinator.RequestApproval(ctx, request, func(runtimecore.ApprovalResponse) error { return nil }); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}

	event, ok := findRuntimeApprovalEvent(mustConversationView(t, repo, "conv-wechat-material").Events, request.RequestID)
	if !ok {
		t.Fatal("persisted approval event not found")
	}
	encoded := event.Metadata[approvalMaterialMetadataKey]
	if encoded == "" {
		t.Fatalf("approval material was not persisted: %+v", event.Metadata)
	}
	var material runtimeApprovalMaterial
	if err := json.Unmarshal([]byte(encoded), &material); err != nil {
		t.Fatalf("decode persisted material: %v", err)
	}
	if material.Content != "# Reviewed article\n" || material.MaterialHash == "" || event.Metadata["hil_decision_args_hash"] == "" {
		t.Fatalf("persisted material = %+v metadata=%+v", material, event.Metadata)
	}
	preview := hilService.Input().PayloadPreview
	if preview["material_kind"] != "wechat_draft" || preview["content"] != "# Reviewed article\n" || preview["material_hash"] != material.MaterialHash {
		t.Fatalf("HIL material preview = %+v", preview)
	}
	businessMaterial, ok := preview["business_approval_material"].(map[string]any)
	if !ok || businessMaterial["decision_request"] != "是否允许营销智能体把这篇文章写入指定公众号的草稿箱？" ||
		businessMaterial["expected_result"] != "草稿创建后可以继续编辑或由运营人员发布；本次审批不会直接发布文章。" {
		t.Fatalf("HIL business approval material = %#v", preview["business_approval_material"])
	}
	if _, ok := preview["artifacts"].([]any); !ok {
		t.Fatalf("HIL artifact preview = %#v", preview["artifacts"])
	}
	input := hilService.Input()
	if !input.ApprovalRequired || input.HILRequired || input.PreferredMode != hil.ModeApproval {
		t.Fatalf("WeChat business decision route = approval_required %v, hil_required %v, preferred %q", input.ApprovalRequired, input.HILRequired, input.PreferredMode)
	}
	if input.BusinessKey == "" || event.Metadata[approvalBusinessKeyMetadataKey] != input.BusinessKey || !strings.HasPrefix(input.IdempotencyKey, "runtime-approval-business:sha256:") {
		t.Fatalf("HIL business binding input=%+v metadata=%+v", input, event.Metadata)
	}
}

func TestRuntimeHILIdempotencyKeyUsesBusinessIntentAcrossRuns(t *testing.T) {
	metadata := map[string]string{approvalBusinessKeyMetadataKey: "marketing.wechat_draft.intent:sha256:stable"}
	first := runtimeHILIdempotencyKey(runtimeApprovalContext{ConversationID: "conv-1", RunID: "run-1"}, runtimecore.ApprovalRequest{RequestID: "call-1"}, metadata)
	second := runtimeHILIdempotencyKey(runtimeApprovalContext{ConversationID: "conv-2", RunID: "run-2"}, runtimecore.ApprovalRequest{RequestID: "call-2"}, metadata)
	if first != second || !strings.HasPrefix(first, "runtime-approval-business:sha256:") {
		t.Fatalf("business idempotency keys = %q and %q", first, second)
	}
	legacyFirst := runtimeHILIdempotencyKey(runtimeApprovalContext{ConversationID: "conv-1", RunID: "run-1"}, runtimecore.ApprovalRequest{RequestID: "call-1"})
	legacySecond := runtimeHILIdempotencyKey(runtimeApprovalContext{ConversationID: "conv-2", RunID: "run-2"}, runtimecore.ApprovalRequest{RequestID: "call-2"})
	if legacyFirst == legacySecond {
		t.Fatalf("generic runtime approvals unexpectedly share key %q", legacyFirst)
	}
}

func TestRuntimeHILArgsHashUsesWechatMaterialAcrossRuns(t *testing.T) {
	metadata := map[string]string{
		approvalBusinessKeyMetadataKey: "marketing.wechat_draft.intent:sha256:stable",
		"approval_material_hash":       "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	first := runtimeHILArgsHash(runtimeApprovalContext{ConversationID: "conv-1", RunID: "run-1"}, runtimecore.ApprovalRequest{RequestID: "call-1", ToolName: wechatDraftApprovalTool}, metadata)
	second := runtimeHILArgsHash(runtimeApprovalContext{ConversationID: "conv-2", RunID: "run-2"}, runtimecore.ApprovalRequest{RequestID: "call-2", ToolName: wechatDraftApprovalTool}, metadata)
	if first != second {
		t.Fatalf("identical WeChat material hashes differ across runs: %q != %q", first, second)
	}
	changed := cloneStringMap(metadata)
	changed["approval_material_hash"] = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if runtimeHILArgsHash(runtimeApprovalContext{}, runtimecore.ApprovalRequest{ToolName: wechatDraftApprovalTool}, changed) == first {
		t.Fatal("changed WeChat material reused the same args hash")
	}
}

func TestRuntimeApprovalContinuationRejectsExpiredAuthorization(t *testing.T) {
	resolution := hil.DecisionResolution{
		Decision: hil.DecisionRequest{ID: "decision-expired", Status: hil.DecisionResolved},
		Receipt:  hil.DecisionReceipt{ID: "receipt-expired", Effect: hil.EffectAuthorize, ExpiresAt: testTime.Add(-time.Minute)},
	}
	status, continuation := runtimeApprovalContinuationFromHILResolution(resolution, testTime)
	if status != "denied" || continuation.Decision != runtimecore.ApprovalDecisionDenied || continuation.ActionID != "deny" || !strings.Contains(continuation.Message, "expired") {
		t.Fatalf("expired authorization continuation = status %q, %+v", status, continuation)
	}
	resolution.Receipt.ExpiresAt = testTime.Add(time.Minute)
	status, continuation = runtimeApprovalContinuationFromHILResolution(resolution, testTime)
	if status != "approved" || continuation.Decision != runtimecore.ApprovalDecisionApproved || continuation.ActionID != "approve_once" {
		t.Fatalf("current authorization continuation = status %q, %+v", status, continuation)
	}
}

func TestFindRuntimeApprovalEventByHILDecisionIDPrefersPendingRetry(t *testing.T) {
	events := []EventRecord{
		{EventID: "old-approved", Kind: discussionRuntimeApprovalEventKind, Status: "approved", Metadata: map[string]string{"hil_decision_id": "hil-shared"}, CreatedAt: testTime},
		{EventID: "new-pending", Kind: discussionRuntimeApprovalEventKind, Status: "pending", Metadata: map[string]string{"hil_decision_id": "hil-shared"}, CreatedAt: testTime.Add(time.Minute)},
	}
	event, ok := findRuntimeApprovalEventByHILDecisionID(events, "hil-shared")
	if !ok || event.EventID != "new-pending" {
		t.Fatalf("selected event = %+v, ok=%v", event, ok)
	}
}

func TestRuntimeApprovalCoordinatorRejectsWechatApprovalWithoutMaterial(t *testing.T) {
	repo := newMemoryRepository(t)
	hilService := &recordingRuntimeHILService{}
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{
		Repository: repo, Clock: fixedClock{now: testTime}, HIL: hilService,
	})
	saveRuntimeHILTestConversation(t, repo, "conv-wechat-missing", "run-wechat-missing")
	ctx := contextWithRuntimeApproval(context.Background(), runtimeApprovalContext{
		ConversationID: "conv-wechat-missing", RunID: "run-wechat-missing", AgentID: "jordan", RuntimeKind: "pi",
	})
	err := coordinator.RequestApproval(ctx, runtimecore.ApprovalRequest{
		Source: "pi", RequestID: "approval-wechat-missing", Kind: runtimecore.ApprovalKindTool,
		ToolName: wechatDraftApprovalTool, Metadata: map[string]string{"approval_force_manual": "true"},
	}, func(runtimecore.ApprovalResponse) error { return nil })
	if err == nil {
		t.Fatal("missing WeChat approval material was accepted")
	}
	if hilService.Calls() != 0 {
		t.Fatalf("HIL create calls = %d, want 0", hilService.Calls())
	}
	if _, ok := findRuntimeApprovalEvent(mustConversationView(t, repo, "conv-wechat-missing").Events, "approval-wechat-missing"); ok {
		t.Fatal("approval event was persisted before material validation")
	}
}

func TestRuntimeApprovalCoordinatorCreatesDurableHILBridgeWithOntologySubjects(t *testing.T) {
	repo := newMemoryRepository(t)
	hilService := &recordingRuntimeHILService{result: hil.CreateDecisionResult{Decision: hil.DecisionRequest{
		ID:        "hil-runtime-1",
		Status:    hil.DecisionPending,
		Route:     hil.RouteDecision{Mode: hil.ModeHIL},
		ExpiresAt: testTime.Add(time.Hour),
	}}}
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{
		Repository:          repo,
		Clock:               fixedClock{now: testTime},
		ApprovalHoldTimeout: time.Hour,
		HIL:                 hilService,
	})
	ctx := context.Background()
	saveRuntimeHILTestConversation(t, repo, "conv-hil-bridge", "run-hil-bridge")
	runCtx := contextWithRuntimeApproval(ctx, runtimeApprovalContext{
		ConversationID: "conv-hil-bridge", RunID: "run-hil-bridge", AgentID: "agent-hil", RuntimeKind: "codex", AssignedTo: "reviewer-hil",
	})
	if err := coordinator.RequestApproval(runCtx, runtimecore.ApprovalRequest{
		Source: "codex", RequestID: "approval-hil-bridge", Kind: runtimecore.ApprovalKindCommandExecution,
		ToolName: "exec_command", Metadata: map[string]string{
			"command":                                "echo hi",
			"approval_summary":                       "发布本期产品更新前需要负责人确认。",
			"business_direction":                     "向订阅用户同步本期产品更新。",
			"approval_internal_key":                  "must-not-be-forwarded",
			runtimeapproval.MetadataIntentVersion:    "1",
			runtimeapproval.MetadataActionTypeID:     string(actioncontract.ActionInformationRead),
			runtimeapproval.MetadataIntentRisk:       string(actioncontract.RiskLow),
			runtimeapproval.MetadataIntentRecognized: "true",
		},
	}, func(runtimecore.ApprovalResponse) error { return nil }); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	if hilService.Calls() != 1 {
		t.Fatalf("HIL create calls = %d, want 1", hilService.Calls())
	}
	input := hilService.Input()
	if len(input.SubjectRefs) != 2 || input.SubjectRefs[0] != (hil.ObjectRef{Kind: "conversation", ID: "conv-hil-bridge"}) || input.SubjectRefs[1] != (hil.ObjectRef{Kind: "agent_run", ID: "run-hil-bridge"}) {
		t.Fatalf("HIL subject refs = %+v", input.SubjectRefs)
	}
	if input.PayloadPreview["approval_conversation_id"] != "conv-hil-bridge" || input.PayloadPreview["approval_run_id"] != "run-hil-bridge" || input.PayloadPreview["approval_assigned_to"] != "reviewer-hil" {
		t.Fatalf("HIL recovery payload = %+v", input.PayloadPreview)
	}
	if input.Action != string(actioncontract.ActionSystemCommandExecute) || input.ActionTypeID != string(actioncontract.ActionSystemCommandExecute) || input.RiskLevel != hil.RiskHigh {
		t.Fatalf("canonical HIL intent = action %q type %q risk %q", input.Action, input.ActionTypeID, input.RiskLevel)
	}
	if input.ArgsHash == "" || input.PayloadPreview["material_hash"] != input.ArgsHash ||
		input.PayloadPreview[runtimeapproval.MetadataActionTypeID] != string(actioncontract.ActionSystemCommandExecute) ||
		input.PayloadPreview[runtimeapproval.MetadataIntentRisk] != string(actioncontract.RiskHigh) {
		t.Fatalf("canonical HIL payload = %+v args_hash=%q", input.PayloadPreview, input.ArgsHash)
	}
	if input.PayloadPreview["approval_summary"] != "发布本期产品更新前需要负责人确认。" || input.PayloadPreview["business_direction"] != "向订阅用户同步本期产品更新。" {
		t.Fatalf("HIL readable approval payload = %+v", input.PayloadPreview)
	}
	if _, ok := input.PayloadPreview["approval_internal_key"]; ok {
		t.Fatalf("HIL payload leaked non-display metadata: %+v", input.PayloadPreview)
	}
	event, ok := findRuntimeApprovalEvent(mustConversationView(t, repo, "conv-hil-bridge").Events, "approval-hil-bridge")
	if !ok || event.Status != "pending" || event.Metadata["hil_decision_id"] != "hil-runtime-1" || event.Metadata["hil_decision_expires_at"] == "" {
		t.Fatalf("durable HIL event = %+v, ok=%v", event, ok)
	}
	if event.Metadata[runtimeapproval.MetadataActionTypeID] != string(actioncontract.ActionSystemCommandExecute) ||
		event.Metadata[runtimeapproval.MetadataIntentRisk] != string(actioncontract.RiskHigh) {
		t.Fatalf("provider metadata replaced durable intent: %+v", event.Metadata)
	}
}

func TestRuntimeApprovalCoordinatorPersistsReadableRequestPayloadForHILReplay(t *testing.T) {
	repo := newMemoryRepository(t)
	hilService := &recordingRuntimeHILService{result: hil.CreateDecisionResult{Decision: hil.DecisionRequest{
		ID: "hil-runtime-payload", Status: hil.DecisionPending, Route: hil.RouteDecision{Mode: hil.ModeHIL}, ExpiresAt: testTime.Add(time.Hour),
	}}}
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{
		Repository: repo, Clock: fixedClock{now: testTime}, HIL: hilService,
	})
	saveRuntimeHILTestConversation(t, repo, "conv-hil-payload", "run-hil-payload")
	request := runtimecore.ApprovalRequest{
		Source: "codex", RequestID: "approval-hil-payload", Kind: runtimecore.ApprovalKindTool, ToolName: "discussion_talent_list",
		Payload: json.RawMessage(`{"input":{"query":"查找适合负责本期发布复核的人才"},"request_id":"req_secret_12345678"}`),
	}
	ctx := contextWithRuntimeApproval(context.Background(), runtimeApprovalContext{
		ConversationID: "conv-hil-payload", RunID: "run-hil-payload", AgentID: "agent-hil", RuntimeKind: "codex",
	})
	if err := coordinator.RequestApproval(ctx, request, func(runtimecore.ApprovalResponse) error { return nil }); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}

	event, ok := findRuntimeApprovalEvent(mustConversationView(t, repo, "conv-hil-payload").Events, request.RequestID)
	if !ok {
		t.Fatal("persisted approval event not found")
	}
	if event.Metadata["query"] != "查找适合负责本期发布复核的人才" {
		t.Fatalf("persisted readable payload = %+v", event.Metadata)
	}
	if _, leaked := event.Metadata["request_id"]; leaked {
		t.Fatalf("opaque request metadata leaked into event: %+v", event.Metadata)
	}
	preview := hilService.Input().PayloadPreview
	if preview["query"] != "查找适合负责本期发布复核的人才" {
		t.Fatalf("HIL payload preview = %+v", preview)
	}
}

func TestRuntimeApprovalCoordinatorKeepsPendingHILDecisionOpenWhenLookupFails(t *testing.T) {
	repo := newMemoryRepository(t)
	hilService := &recordingRuntimeHILResolutionService{
		recordingRuntimeHILService: recordingRuntimeHILService{result: hil.CreateDecisionResult{Decision: hil.DecisionRequest{
			ID: "hil-runtime-pending-lookup", TenantID: "tenant-hil", Status: hil.DecisionPending, Route: hil.RouteDecision{Mode: hil.ModeHIL},
		}}},
		lookupErr: errors.New("temporary HIL receipt lookup failure"),
	}
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{Repository: repo, Clock: fixedClock{now: testTime}, HIL: hilService})
	service := newTestService(ServiceOptions{Repository: repo, RuntimeApprovalCoordinator: coordinator, Clock: fixedClock{now: testTime}})
	coordinator.SetHILRuntimeApprovalResolver(service)
	ctx := context.Background()
	saveRuntimeHILTestConversation(t, repo, "conv-hil-pending-lookup", "run-hil-pending-lookup")
	responseCh := make(chan runtimecore.ApprovalResponse, 1)
	if err := coordinator.RequestApproval(contextWithRuntimeApproval(ctx, runtimeApprovalContext{
		ConversationID: "conv-hil-pending-lookup", RunID: "run-hil-pending-lookup", AgentID: "agent-hil", RuntimeKind: "codex",
	}), runtimecore.ApprovalRequest{
		Source: "codex", RequestID: "approval-hil-pending-lookup", Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command",
	}, func(response runtimecore.ApprovalResponse) error {
		responseCh <- response
		return nil
	}); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}

	// The bounded lookup retry finishes after 100ms + 200ms + 400ms. A failed
	// lookup must never turn its still-pending CreateDecision result into deny.
	time.Sleep(time.Second)
	select {
	case response := <-responseCh:
		t.Fatalf("pending HIL lookup unexpectedly responded: %#v", response)
	default:
	}
	view := mustConversationView(t, repo, "conv-hil-pending-lookup")
	event, ok := findRuntimeApprovalEvent(view.Events, "approval-hil-pending-lookup")
	if !ok || event.Status != "pending" {
		t.Fatalf("pending HIL event = %+v, ok=%v", event, ok)
	}
	run, ok := findRuntimeApprovalRun(view.Runs, event.Metadata)
	if !ok || run.Status != "running" {
		t.Fatalf("pending HIL run = %+v, ok=%v", run, ok)
	}
	coordinator.mu.Lock()
	_, deferred := coordinator.hilResolutionRetry["hil-runtime-pending-lookup"]
	coordinator.mu.Unlock()
	if deferred {
		t.Fatal("pending HIL decision was incorrectly queued as a terminal resolution")
	}
}

func TestRuntimeApprovalCoordinatorReconnectsPendingHILRetry(t *testing.T) {
	repo := newMemoryRepository(t)
	hilService := &recordingRuntimeHILResolutionService{
		recordingRuntimeHILService: recordingRuntimeHILService{result: hil.CreateDecisionResult{Decision: hil.DecisionRequest{
			ID: "hil-runtime-retry-pending", TenantID: "tenant-hil", Status: hil.DecisionPending, Route: hil.RouteDecision{Mode: hil.ModeHIL},
		}}},
	}
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{Repository: repo, Clock: fixedClock{now: testTime}, HIL: hilService})
	service := newTestService(ServiceOptions{Repository: repo, RuntimeApprovalCoordinator: coordinator, Clock: fixedClock{now: testTime}})
	coordinator.SetHILRuntimeApprovalResolver(service)
	ctx := context.Background()
	saveRuntimeHILTestConversation(t, repo, "conv-hil-retry-pending", "run-hil-retry-pending")
	runCtx := contextWithRuntimeApproval(ctx, runtimeApprovalContext{
		ConversationID: "conv-hil-retry-pending", RunID: "run-hil-retry-pending", AgentID: "agent-hil", RuntimeKind: "codex",
	})
	request := runtimecore.ApprovalRequest{
		Source: "codex", RequestID: "approval-hil-retry-pending", Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command",
	}
	if err := coordinator.RequestApproval(runCtx, request, func(runtimecore.ApprovalResponse) error { return nil }); err != nil {
		t.Fatalf("first RequestApproval: %v", err)
	}
	if err := coordinator.RequestApproval(runCtx, request, func(runtimecore.ApprovalResponse) error { return nil }); err != nil {
		t.Fatalf("pending HIL retry RequestApproval: %v", err)
	}
	if hilService.Calls() != 1 {
		t.Fatalf("HIL create calls = %d, want 1", hilService.Calls())
	}
	event, ok := findRuntimeApprovalEvent(mustConversationView(t, repo, "conv-hil-retry-pending").Events, request.RequestID)
	if !ok || event.Status != "pending" || event.Metadata["hil_decision_id"] != "hil-runtime-retry-pending" {
		t.Fatalf("pending retry event = %+v, ok=%v", event, ok)
	}
}

func TestRuntimeApprovalCoordinatorResolvesHILAfterContextCancellationAndRecreation(t *testing.T) {
	repo := newMemoryRepository(t)
	hilService := &recordingRuntimeHILService{result: hil.CreateDecisionResult{Decision: hil.DecisionRequest{
		ID: "hil-runtime-restart", Status: hil.DecisionPending, Route: hil.RouteDecision{Mode: hil.ModeHIL}, ExpiresAt: testTime.Add(time.Hour),
	}}}
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{Repository: repo, Clock: fixedClock{now: testTime}, HIL: hilService})
	service := newTestService(ServiceOptions{Repository: repo, RuntimeApprovalCoordinator: coordinator, Clock: fixedClock{now: testTime}})
	ctx := context.Background()
	saveRuntimeHILTestConversation(t, repo, "conv-hil-restart", "run-hil-restart")
	runCtx, cancel := context.WithCancel(contextWithRuntimeApproval(ctx, runtimeApprovalContext{
		ConversationID: "conv-hil-restart", RunID: "run-hil-restart", AgentID: "agent-hil", RuntimeKind: "codex",
	}))
	if err := coordinator.RequestApproval(runCtx, runtimecore.ApprovalRequest{
		Source: "codex", RequestID: "approval-hil-restart", Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command",
	}, func(runtimecore.ApprovalResponse) error { return nil }); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	_, err := service.DecideRuntimeApproval(ctx, ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser}, RuntimeApprovalDecisionCommand{
		ConversationID: "conv-hil-restart", ApprovalID: "approval-hil-restart", ActionID: "deny", IdempotencyKey: "legacy-must-not-decide",
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("legacy HIL decision error = %v, want ErrForbidden", err)
	}
	cancel()
	coordinatorB := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{Repository: repo, Clock: fixedClock{now: testTime}})
	coordinatorB.SetHILRuntimeApprovalResolver(service)
	coordinatorB.DecisionResolved(ctx, hil.DecisionResolution{
		Decision: hil.DecisionRequest{ID: "hil-runtime-restart", Status: hil.DecisionDenied, Route: hil.RouteDecision{Mode: hil.ModeHIL}},
		Receipt:  hil.DecisionReceipt{ID: "receipt-restart", Effect: hil.EffectDeny},
		Actor:    hil.ActorRef{Kind: hil.ActorHuman, ID: "local-user"},
		Reason:   "provider context ended",
	})
	assertRuntimeHILTerminal(t, repo, "conv-hil-restart", "approval-hil-restart", "denied", "receipt-restart")
}

func TestRuntimeApprovalCoordinatorReplaysTerminalCreateDecisionAfterLocalPersistence(t *testing.T) {
	repo := newMemoryRepository(t)
	hilService := &recordingRuntimeHILResolutionService{
		recordingRuntimeHILService: recordingRuntimeHILService{result: hil.CreateDecisionResult{Decision: hil.DecisionRequest{
			ID: "hil-runtime-terminal", TenantID: "tenant-hil", Status: hil.DecisionDenied, Route: hil.RouteDecision{Mode: hil.ModeDeny},
		}}},
		resolution: hil.DecisionResolution{
			Decision: hil.DecisionRequest{ID: "hil-runtime-terminal", TenantID: "tenant-hil", Status: hil.DecisionDenied, Route: hil.RouteDecision{Mode: hil.ModeDeny}},
			Receipt:  hil.DecisionReceipt{ID: "receipt-terminal", Effect: hil.EffectDeny}, Actor: hil.ActorRef{Kind: hil.ActorService, ID: "hil:replay"}, Reason: "replayed committed HIL decision",
		},
	}
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{Repository: repo, Clock: fixedClock{now: testTime}, HIL: hilService})
	service := newTestService(ServiceOptions{Repository: repo, RuntimeApprovalCoordinator: coordinator, Clock: fixedClock{now: testTime}})
	coordinator.SetHILRuntimeApprovalResolver(service)
	ctx := context.Background()
	saveRuntimeHILTestConversation(t, repo, "conv-hil-terminal", "run-hil-terminal")
	responseCh := make(chan runtimecore.ApprovalResponse, 1)
	if err := coordinator.RequestApproval(contextWithRuntimeApproval(ctx, runtimeApprovalContext{
		ConversationID: "conv-hil-terminal", RunID: "run-hil-terminal", AgentID: "agent-hil", RuntimeKind: "codex",
	}), runtimecore.ApprovalRequest{
		Source: "codex", RequestID: "approval-hil-terminal", Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command",
	}, func(response runtimecore.ApprovalResponse) error {
		responseCh <- response
		return nil
	}); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	select {
	case response := <-responseCh:
		if response.Decision != runtimecore.ApprovalDecisionDenied {
			t.Fatalf("terminal replay response = %#v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for terminal replay response")
	}
	assertRuntimeHILTerminal(t, repo, "conv-hil-terminal", "approval-hil-terminal", "denied", "receipt-terminal")
}

func TestRuntimeApprovalCoordinatorReconcilesResolvedHILAfterObserverWindow(t *testing.T) {
	repo := newMemoryRepository(t)
	hilService := &recordingRuntimeHILResolutionService{
		recordingRuntimeHILService: recordingRuntimeHILService{result: hil.CreateDecisionResult{Decision: hil.DecisionRequest{
			ID: "hil-runtime-observer-window", TenantID: "tenant-hil", Status: hil.DecisionPending, Route: hil.RouteDecision{Mode: hil.ModeHIL},
		}}},
		resolution: hil.DecisionResolution{
			Decision: hil.DecisionRequest{ID: "hil-runtime-observer-window", TenantID: "tenant-hil", Status: hil.DecisionDenied, Route: hil.RouteDecision{Mode: hil.ModeHIL}},
			Receipt:  hil.DecisionReceipt{ID: "receipt-observer-window", Effect: hil.EffectDeny}, Actor: hil.ActorRef{Kind: hil.ActorService, ID: "hil:replay"}, Reason: "observer arrived before local event",
		},
	}
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{Repository: repo, Clock: fixedClock{now: testTime}, HIL: hilService})
	service := newTestService(ServiceOptions{Repository: repo, RuntimeApprovalCoordinator: coordinator, Clock: fixedClock{now: testTime}})
	coordinator.SetHILRuntimeApprovalResolver(service)
	ctx := context.Background()
	saveRuntimeHILTestConversation(t, repo, "conv-hil-window", "run-hil-window")
	if err := coordinator.RequestApproval(contextWithRuntimeApproval(ctx, runtimeApprovalContext{
		ConversationID: "conv-hil-window", RunID: "run-hil-window", AgentID: "agent-hil", RuntimeKind: "codex",
	}), runtimecore.ApprovalRequest{
		Source: "codex", RequestID: "approval-hil-window", Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command",
	}, func(runtimecore.ApprovalResponse) error { return nil }); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	assertRuntimeHILTerminal(t, repo, "conv-hil-window", "approval-hil-window", "denied", "receipt-observer-window")
}

func TestServiceRecoversExpiredHILRuntimeApprovalAfterRestart(t *testing.T) {
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{Repository: repo, Clock: fixedClock{now: testTime.Add(time.Hour)}})
	ctx := context.Background()
	saveRuntimeHILTestConversation(t, repo, "conv-hil-expired", "run-hil-expired")
	if err := repo.SaveRun(ctx, RunRecord{RunID: "run-hil-expired", ConversationID: "conv-hil-expired", AgentID: "agent-hil", Status: "blocked", RuntimeKind: "codex", CreatedAt: testTime, StartedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveEvent(ctx, EventRecord{
		EventID: "approval-hil-expired", ConversationID: "conv-hil-expired", Kind: discussionRuntimeApprovalEventKind, Status: "pending",
		Metadata: map[string]string{
			"hil_decision_id": "hil-runtime-expired", "hil_decision_expires_at": testTime.Add(-time.Minute).UTC().Format(time.RFC3339Nano), "approval_run_id": "run-hil-expired",
		}, CreatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.RecoverExpiredHILRuntimeApprovals(ctx, testTime.Add(time.Hour)); err != nil {
		t.Fatalf("RecoverExpiredHILRuntimeApprovals: %v", err)
	}
	view := mustConversationView(t, repo, "conv-hil-expired")
	event, ok := findRuntimeApprovalEvent(view.Events, "approval-hil-expired")
	if !ok || event.Status != "expired" {
		t.Fatalf("expired event = %+v, ok=%v", event, ok)
	}
	run, ok := findRuntimeApprovalRun(view.Runs, event.Metadata)
	if !ok || run.Status != "completed" {
		t.Fatalf("expired run = %+v, ok=%v", run, ok)
	}
}

func TestRuntimeApprovalCoordinatorExpiryReleasesLiveResponder(t *testing.T) {
	repo := newMemoryRepository(t)
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{Repository: repo, Clock: fixedClock{now: testTime}})
	service := newTestService(ServiceOptions{Repository: repo, RuntimeApprovalCoordinator: coordinator, Clock: fixedClock{now: testTime}})
	coordinator.SetHILRuntimeApprovalResolver(service)
	ctx := context.Background()
	saveRuntimeHILTestConversation(t, repo, "conv-hil-live-expiry", "run-hil-live-expiry")
	if err := repo.SaveRun(ctx, RunRecord{
		RunID: "run-hil-live-expiry", ConversationID: "conv-hil-live-expiry", AgentID: "agent-hil",
		Status: "blocked", RuntimeKind: "codex", CreatedAt: testTime, StartedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	event := EventRecord{
		EventID: "approval-hil-live-expiry", ConversationID: "conv-hil-live-expiry", Kind: discussionRuntimeApprovalEventKind, Status: "pending",
		Metadata: map[string]string{
			runtimecore.ApprovalMetadataRequestID: "request-hil-live-expiry",
			"hil_decision_id":                     "hil-runtime-live-expiry",
			"approval_run_id":                     "run-hil-live-expiry",
		},
		CreatedAt: testTime,
	}
	if err := repo.SaveEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	responses := make(chan runtimecore.ApprovalResponse, 1)
	request := runtimecore.ApprovalRequest{RequestID: "request-hil-live-expiry", Kind: runtimecore.ApprovalKindCommandExecution}
	key := liveRuntimeApprovalKey(event.ConversationID, "run-hil-live-expiry", request.RequestID)
	coordinator.remember(key, liveRuntimeApproval{
		Context: runtimeApprovalContext{ConversationID: event.ConversationID, RunID: "run-hil-live-expiry"},
		Request: request,
		Responder: func(response runtimecore.ApprovalResponse) error {
			responses <- response
			return nil
		},
		Event: event, HILDecisionID: "hil-runtime-live-expiry",
	})
	if err := coordinator.expireLiveHILRuntimeApproval(event.ConversationID, "run-hil-live-expiry", request.RequestID, "hil-runtime-live-expiry"); err != nil {
		t.Fatalf("expireLiveHILRuntimeApproval: %v", err)
	}
	select {
	case response := <-responses:
		if response.Decision != runtimecore.ApprovalDecisionDenied || response.Reason != "approval window expired" {
			t.Fatalf("response = %+v", response)
		}
	default:
		t.Fatal("live responder was not released")
	}
	coordinator.mu.Lock()
	_, stillPending := coordinator.pending[key]
	coordinator.mu.Unlock()
	if stillPending {
		t.Fatal("expired live approval remained pending")
	}
	assertRuntimeHILTerminal(t, repo, event.ConversationID, event.EventID, "expired", "")
}

func TestRuntimeApprovalCoordinatorRetriesObserverFailure(t *testing.T) {
	repo := newMemoryRepository(t)
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{Repository: repo, Clock: fixedClock{now: testTime}})
	ctx := context.Background()
	saveRuntimeHILTestConversation(t, repo, "conv-hil-retry", "run-hil-retry")
	if err := repo.SaveEvent(ctx, EventRecord{
		EventID: "approval-hil-retry", ConversationID: "conv-hil-retry", Kind: discussionRuntimeApprovalEventKind, Status: "pending",
		Metadata: map[string]string{"hil_decision_id": "hil-runtime-retry", "approval_run_id": "run-hil-retry"}, CreatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	resolution := hil.DecisionResolution{
		Decision: hil.DecisionRequest{ID: "hil-runtime-retry", Action: "runtime.approval", Status: hil.DecisionDenied},
		Receipt:  hil.DecisionReceipt{ID: "receipt-retry", Effect: hil.EffectDeny}, Actor: hil.ActorRef{Kind: hil.ActorHuman, ID: "local-user"},
	}
	coordinator.DecisionResolved(ctx, resolution)
	coordinator.mu.Lock()
	queued := coordinator.hilResolutionRetry["hil-runtime-retry"]
	coordinator.mu.Unlock()
	if queued.Decision.ID != "hil-runtime-retry" {
		t.Fatalf("deferred resolution = %#v", queued)
	}
	service := newTestService(ServiceOptions{Repository: repo, RuntimeApprovalCoordinator: coordinator, Clock: fixedClock{now: testTime}})
	coordinator.SetHILRuntimeApprovalResolver(service)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		view := mustConversationView(t, repo, "conv-hil-retry")
		event, _ := findRuntimeApprovalEvent(view.Events, "approval-hil-retry")
		if event.Status == "denied" {
			assertRuntimeHILTerminal(t, repo, "conv-hil-retry", "approval-hil-retry", "denied", "receipt-retry")
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out retrying deferred HIL resolution")
}

func TestRuntimeApprovalCoordinatorResumesDetachedMissionOnlyAfterDurableHILPersistence(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID: "conv-detached", TenantID: "tenant-hil", Title: "Detached mission runtime",
		Status: "completed", CreatedBy: "agent-hil", CreatedAt: testTime, UpdatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveEvent(ctx, EventRecord{
		EventID: "approval-detached", ConversationID: "conv-detached", Kind: discussionRuntimeApprovalEventKind, Status: "pending",
		Metadata: map[string]string{
			"hil_decision_id": "hil-detached", "approval_run_id": "responsibility-run-1",
			"approval_request_id": "tool-call-1", "approval_tool_name": wechatDraftApprovalTool,
		}, CreatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{Repository: repo, Clock: fixedClock{now: testTime}})
	service := newTestService(ServiceOptions{Repository: repo, RuntimeApprovalCoordinator: coordinator, Clock: fixedClock{now: testTime}})
	detached := &recordingDetachedRuntimeHILResolver{}
	coordinator.SetHILRuntimeApprovalResolver(service)
	coordinator.SetHILDetachedApprovalResolver(detached)
	resolution := hil.DecisionResolution{
		Decision: hil.DecisionRequest{ID: "hil-detached", TenantID: "tenant-hil", Action: "runtime.approval", Status: hil.DecisionResolved},
		Receipt:  hil.DecisionReceipt{ID: "receipt-detached", RequestID: "hil-detached", Effect: hil.EffectAuthorize, SingleUse: true},
		Actor:    hil.ActorRef{Kind: hil.ActorHuman, ID: "ding"},
	}
	if err := coordinator.applyHILResolution(ctx, resolution); err != nil {
		t.Fatalf("applyHILResolution: %v", err)
	}
	if detached.calls != 1 || detached.resolution.Receipt.ID != "receipt-detached" || detached.conversation.ConversationID != "conv-detached" {
		t.Fatalf("detached resolver call = %+v", detached)
	}
	if detached.event.Status != "approved" || detached.event.Metadata["approval_resume_required"] != "true" || detached.event.Metadata["hil_receipt_id"] != "receipt-detached" {
		t.Fatalf("detached resolver observed event before durable approval persistence: %+v", detached.event)
	}
}

func TestRuntimeApprovalCoordinatorQueuesDetachedMissionWhenLiveResponderStillExists(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	conversationID, runID, requestID, decisionID := "conv-live-detached", "responsibility-run-live", "tool-call-live", "hil-live-detached"
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: conversationID, TenantID: "tenant-hil", Title: "Live detached mission runtime", Status: "active", CreatedBy: "agent-hil", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	event := EventRecord{EventID: requestID, ConversationID: conversationID, Kind: discussionRuntimeApprovalEventKind, Status: "pending", Metadata: map[string]string{"hil_decision_id": decisionID, "approval_run_id": runID, "approval_request_id": requestID, "approval_tool_name": wechatDraftApprovalTool}, CreatedAt: testTime}
	if err := repo.SaveEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{Repository: repo, Clock: fixedClock{now: testTime}})
	service := newTestService(ServiceOptions{Repository: repo, RuntimeApprovalCoordinator: coordinator, Clock: fixedClock{now: testTime}})
	detached := &recordingDetachedRuntimeHILResolver{}
	coordinator.SetHILRuntimeApprovalResolver(service)
	coordinator.SetHILDetachedApprovalResolver(detached)
	responses := make(chan runtimecore.ApprovalResponse, 1)
	key := liveRuntimeApprovalKey(conversationID, runID, requestID)
	coordinator.pending[key] = liveRuntimeApproval{Context: runtimeApprovalContext{ConversationID: conversationID, RunID: runID}, Request: runtimecore.ApprovalRequest{RequestID: requestID, ToolName: wechatDraftApprovalTool}, Responder: func(response runtimecore.ApprovalResponse) error { responses <- response; return nil }, Event: event, HILDecisionID: decisionID}
	coordinator.pendingHIL[decisionID] = key
	resolution := hil.DecisionResolution{Decision: hil.DecisionRequest{ID: decisionID, TenantID: "tenant-hil", Action: "runtime.approval", Status: hil.DecisionResolved}, Receipt: hil.DecisionReceipt{ID: "receipt-live-detached", RequestID: decisionID, Effect: hil.EffectAuthorize, SingleUse: true}, Actor: hil.ActorRef{Kind: hil.ActorHuman, ID: "ding"}}
	if err := coordinator.applyHILResolution(ctx, resolution); err != nil {
		t.Fatalf("applyHILResolution: %v", err)
	}
	select {
	case response := <-responses:
		if response.Decision != runtimecore.ApprovalDecisionApproved {
			t.Fatalf("live response = %+v", response)
		}
	default:
		t.Fatal("live responder was not released")
	}
	if detached.calls != 1 || detached.event.Metadata["approval_resume_required"] != "true" {
		t.Fatalf("detached resolver call = %+v", detached)
	}
}

func (s *recordingRuntimeHILService) CreateDecision(_ context.Context, input hil.CreateDecisionInput) (hil.CreateDecisionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.input = input
	s.calls++
	if s.err != nil {
		return hil.CreateDecisionResult{}, s.err
	}
	return s.result, nil
}

func (s *recordingRuntimeHILService) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *recordingRuntimeHILService) Input() hil.CreateDecisionInput {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.input
}

func (s *recordingRuntimeHILResolutionService) GetDecisionResolution(_ context.Context, _ string, _ string) (hil.DecisionResolution, error) {
	if s.lookupErr != nil {
		return hil.DecisionResolution{}, s.lookupErr
	}
	if s.resolution.Decision.ID == "" {
		return hil.DecisionResolution{}, hil.ErrConflict
	}
	return s.resolution, nil
}

func saveRuntimeHILTestConversation(t *testing.T, repo *Repository, conversationID, runID string) {
	t.Helper()
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID: conversationID, TenantID: "tenant-hil", Title: "HIL runtime", Status: "running", CreatedBy: "local-user", CreatedAt: testTime, UpdatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{
		RunID: runID, ConversationID: conversationID, AgentID: "agent-hil", Status: "running", RuntimeKind: "codex", SessionID: "session-hil", CreatedAt: testTime, StartedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
}

func assertRuntimeHILTerminal(t *testing.T, repo *Repository, conversationID, approvalID, status, receiptID string) {
	t.Helper()
	view := mustConversationView(t, repo, conversationID)
	event, ok := findRuntimeApprovalEvent(view.Events, approvalID)
	if !ok || event.Status != status || event.Metadata["hil_receipt_id"] != receiptID {
		t.Fatalf("HIL event = %+v, ok=%v", event, ok)
	}
	run, ok := findRuntimeApprovalRun(view.Runs, event.Metadata)
	if !ok || run.Status != "completed" {
		t.Fatalf("HIL run = %+v, ok=%v", run, ok)
	}
}
