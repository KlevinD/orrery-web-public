package discussionsession

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/app/runtimeapproval"
)

// RuntimeHILDecisionService is the small portion of HIL needed by a live
// runtime approval. Keeping the coordinator behind this boundary lets the
// production composition root share one HIL service with the HTTP router.
type RuntimeHILDecisionService interface {
	CreateDecision(context.Context, hil.CreateDecisionInput) (hil.CreateDecisionResult, error)
}

// RuntimeHILDecisionResolutionLookup is optional so narrow test doubles and
// non-HIL deployments can keep using the producer-only service boundary.
// Production HIL implements it for recovery after an observer race or a
// process restart.
type RuntimeHILDecisionResolutionLookup interface {
	GetDecisionResolution(context.Context, string, string) (hil.DecisionResolution, error)
}

// RuntimeHILRuntimeApprovalResolver is the durable half of the runtime/HIL
// bridge. It is set after the discussion Service is composed because the HIL
// observer and the discussion service intentionally share one coordinator.
type RuntimeHILRuntimeApprovalResolver interface {
	DecideHILRuntimeApproval(context.Context, ViewerRef, RuntimeApprovalDecisionCommand) (ConversationDTO, error)
	ExpireHILRuntimeApproval(context.Context, string, string, string) (ConversationDTO, error)
}

type RuntimeHILRuntimeApprovalRecovery interface {
	RecoverExpiredHILRuntimeApprovals(context.Context, time.Time) error
}

// RuntimeHILDetachedApprovalResolver resumes a runtime whose original
// provider responder no longer exists. The durable HIL receipt and local
// approval event are passed together so the consumer can preserve the exact
// material binding when it creates a continuation run.
type RuntimeHILDetachedApprovalResolver interface {
	ResumeDetachedHILRuntimeApproval(context.Context, hil.DecisionResolution, ConversationRecordView, EventRecord) error
}

const (
	runtimeHILResolutionRetryDelay    = 100 * time.Millisecond
	runtimeHILResolutionRetryAttempts = 3
)

var (
	errRuntimeHILBridgeUnavailable = errors.New("runtime HIL bridge unavailable")
	errRuntimeHILResolutionPending = errors.New("runtime HIL decision is pending")
)

// SetHILDecisionService finishes the production wiring after the shared HIL
// service is created with this coordinator as its resolution observer.
func (c *RuntimeApprovalCoordinator) SetHILDecisionService(service RuntimeHILDecisionService) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.hil = service
	resolver := c.hilRuntimeResolver
	c.mu.Unlock()
	if resolver != nil {
		go func() {
			_ = c.ReconcileHILRuntimeApprovals(context.Background())
		}()
		go c.retryDeferredHILResolutions(context.Background())
	}
}

// SetHILRuntimeApprovalResolver connects the durable discussion event store
// after the Service has been constructed. Existing HIL decisions can then be
// applied even when their original provider goroutine is gone.
func (c *RuntimeApprovalCoordinator) SetHILRuntimeApprovalResolver(resolver RuntimeHILRuntimeApprovalResolver) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.hilRuntimeResolver = resolver
	c.mu.Unlock()
	if recovery, ok := resolver.(RuntimeHILRuntimeApprovalRecovery); ok {
		go func() {
			_ = recovery.RecoverExpiredHILRuntimeApprovals(context.Background(), c.now())
		}()
	}
	go func() {
		_ = c.ReconcileHILRuntimeApprovals(context.Background())
	}()
	go c.retryDeferredHILResolutions(context.Background())
}

// SetHILDetachedApprovalResolver connects mission-owned runtimes to the HIL
// bridge. It is intentionally separate from the discussion resolver because
// mission runs have no discussion RunRecord to resume.
func (c *RuntimeApprovalCoordinator) SetHILDetachedApprovalResolver(resolver RuntimeHILDetachedApprovalResolver) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.hilDetachedResolver = resolver
	c.mu.Unlock()
}

// prepareRuntimeHILBridge creates a recoverable local record before HIL is
// called. If the process exits after this write, startup reconciliation calls
// HIL with the same idempotency key and fills in the decision ID.
func (c *RuntimeApprovalCoordinator) prepareRuntimeHILBridge(event EventRecord, meta runtimeApprovalContext, req runtimecore.ApprovalRequest) EventRecord {
	metadata := cloneStringMap(event.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	// Persist only the runtime core's display-safe payload fields. The bridge is
	// replayed from this event after restarts, so values kept only on req.Payload
	// would otherwise disappear from the HIL decision shown to reviewers.
	for key, value := range runtimecore.ApprovalPayloadMetadata(req.Payload) {
		if strings.TrimSpace(metadata[key]) == "" {
			metadata[key] = value
		}
	}
	argsHash := runtimeHILArgsHash(meta, req, metadata)
	expiresAt := event.CreatedAt.Add(c.RuntimeApprovalHoldTimeout())
	metadata["approval_hil_required"] = "true"
	metadata["hil_decision_idempotency_key"] = runtimeHILIdempotencyKey(meta, req, metadata)
	metadata["hil_decision_args_hash"] = argsHash
	metadata["hil_decision_expires_at"] = expiresAt.UTC().Format(time.RFC3339Nano)
	event.Metadata = metadata
	return event
}

// ensureRuntimeHILDecision creates or replays the HIL request represented by
// event, then atomically links the durable local bridge to its decision ID.
func (c *RuntimeApprovalCoordinator) ensureRuntimeHILDecision(ctx context.Context, conversation ConversationRecord, meta runtimeApprovalContext, req runtimecore.ApprovalRequest, event EventRecord) (*hil.CreateDecisionResult, EventRecord, error) {
	if strings.TrimSpace(event.Metadata["hil_decision_id"]) != "" {
		return nil, event, nil
	}
	if c == nil || c.repo == nil {
		return nil, event, errRuntimeHILBridgeUnavailable
	}
	c.hilCreateMu.Lock()
	defer c.hilCreateMu.Unlock()

	// Re-read after acquiring the single-flight lock. The caller may have been
	// blocked while another RequestApproval or startup reconciliation created
	// and linked the same durable bridge.
	view, err := c.repo.GetConversation(runtimeApprovalDurableContext(ctx), conversation.ConversationID)
	if err != nil {
		return nil, event, err
	}
	if persisted, found := findRuntimeApprovalEvent(view.Events, event.EventID); found {
		event = persisted
	}
	if strings.TrimSpace(event.Metadata["hil_decision_id"]) != "" {
		return nil, event, nil
	}
	meta.AssignedTo = runtimeHILApprovalAssignee(meta.AssignedTo, view)
	metadata := cloneStringMap(event.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	putRuntimeApprovalMetadata(metadata, "approval_assigned_to", meta.AssignedTo)
	event.Metadata = metadata
	result, err := c.createRuntimeHILDecision(ctx, view, meta, req, event)
	if err != nil {
		return nil, event, err
	}
	linked := linkRuntimeHILDecision(event, result.Decision, c.RuntimeApprovalHoldTimeout())
	if err := c.repo.SaveEvent(runtimeApprovalDurableContext(ctx), linked); err != nil {
		return nil, event, err
	}
	return &result, linked, nil
}

func linkRuntimeHILDecision(event EventRecord, decision hil.DecisionRequest, holdTimeout time.Duration) EventRecord {
	metadata := cloneStringMap(event.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata["approval_hil_required"] = "true"
	metadata["hil_decision_id"] = strings.TrimSpace(decision.ID)
	metadata["hil_decision_mode"] = string(decision.Route.Mode)
	metadata["hil_decision_status"] = string(decision.Status)
	expiresAt := decision.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = event.CreatedAt.Add(holdTimeout)
	}
	metadata["hil_decision_expires_at"] = expiresAt.UTC().Format(time.RFC3339Nano)
	event.Metadata = metadata
	return event
}

func (c *RuntimeApprovalCoordinator) applyCreatedRuntimeHILDecision(ctx context.Context, conversation ConversationRecord, event EventRecord, result hil.CreateDecisionResult) {
	c.mu.Lock()
	service := c.hil
	c.mu.Unlock()
	if resolution, terminal := runtimeHILTerminalResolution(result.Decision); terminal {
		if _, ok := service.(RuntimeHILDecisionResolutionLookup); ok {
			if err := c.reconcileHILRuntimeApproval(ctx, conversation, event); err == nil {
				return
			} else if runtimeHILDecisionNeedsReceipt(result.Decision) {
				// A committed HIL/approval decision has a durable receipt. Do not
				// substitute the still-incomplete CreateDecision result when its
				// receipt lookup is temporarily unavailable.
				go c.retryHILResolutionLookup(conversation, event)
				return
			}
		}
		if err := c.applyHILResolution(ctx, resolution); err != nil {
			c.deferHILResolution(resolution)
		}
		return
	}
	if _, ok := service.(RuntimeHILDecisionResolutionLookup); ok {
		if err := c.reconcileHILRuntimeApproval(ctx, conversation, event); err != nil && !errors.Is(err, errRuntimeHILResolutionPending) {
			// A normal pending HIL request is not a failed resolution. Only actual
			// lookup failures receive bounded retries.
			go c.retryHILResolutionLookup(conversation, event)
		}
	}
}

func runtimeHILDecisionNeedsReceipt(decision hil.DecisionRequest) bool {
	switch decision.Route.Mode {
	case hil.ModeHIL, hil.ModeApproval, hil.ModeCouncil:
		return true
	default:
		return false
	}
}

func (c *RuntimeApprovalCoordinator) createRuntimeHILDecision(ctx context.Context, conversation ConversationRecordView, meta runtimeApprovalContext, req runtimecore.ApprovalRequest, event EventRecord) (hil.CreateDecisionResult, error) {
	if c == nil {
		return hil.CreateDecisionResult{}, errRuntimeHILBridgeUnavailable
	}
	c.mu.Lock()
	service := c.hil
	c.mu.Unlock()
	if service == nil {
		return hil.CreateDecisionResult{}, errRuntimeHILBridgeUnavailable
	}
	metadata := cloneStringMap(event.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	putRuntimeApprovalMetadata(metadata, "approval_assigned_to", runtimeHILApprovalAssignee(meta.AssignedTo, conversation))
	tenantID := strings.TrimSpace(conversation.TenantID)
	if tenantID == "" {
		return hil.CreateDecisionResult{}, fmt.Errorf("%w: runtime HIL conversation tenant is required", ErrInvalidRequest)
	}
	requesterID := firstNonBlank(meta.AgentID, "runtime")
	argsHash := firstNonBlank(strings.TrimSpace(metadata["hil_decision_args_hash"]), runtimeHILArgsHash(meta, req, metadata))
	expiresAt := runtimeHILBridgeExpiry(event, c.RuntimeApprovalHoldTimeout())
	intent := runtimeHILActionIntent(req, metadata)
	payloadPreview := runtimeHILPayloadPreview(metadata)
	materialHash, _ := payloadPreview["material_hash"].(string)
	if strings.TrimSpace(materialHash) == "" {
		payloadPreview["material_hash"] = argsHash
	}
	actionTypeID := string(intent.ActionTypeID)
	preferredMode, hilRequired, approvalRequired := runtimeHILDecisionRoute(intent)
	input := hil.CreateDecisionInput{
		TenantID:         tenantID,
		Requester:        hil.ActorRef{Kind: hil.ActorAgent, ID: requesterID},
		Action:           firstNonBlank(actionTypeID, "runtime.approval"),
		ActionTypeID:     firstNonBlank(actionTypeID, metadata[runtimecore.ApprovalMetadataType], string(req.Kind), "runtime.approval"),
		Resource:         hil.ObjectRef{Kind: "runtime_approval", ID: runtimeHILResourceID(meta, req)},
		ObjectRefs:       []hil.ObjectRef{{Kind: "conversation", ID: meta.ConversationID}, {Kind: "agent_run", ID: meta.RunID}},
		ArgsHash:         argsHash,
		Purpose:          event.Summary,
		RiskLevel:        runtimeHILRiskLevel(intent),
		PreferredMode:    preferredMode,
		HILRequired:      hilRequired,
		ApprovalRequired: approvalRequired,
		SubjectRefs:      []hil.ObjectRef{{Kind: "conversation", ID: meta.ConversationID}, {Kind: "agent_run", ID: meta.RunID}},
		PayloadPreview:   payloadPreview,
		BusinessKey:      strings.TrimSpace(metadata[approvalBusinessKeyMetadataKey]),
		IdempotencyKey:   firstNonBlank(metadata["hil_decision_idempotency_key"], runtimeHILIdempotencyKey(meta, req, metadata)),
		ExpiresAt:        expiresAt,
	}
	return service.CreateDecision(ctx, input)
}

func runtimeHILBridgeExpiry(event EventRecord, holdTimeout time.Duration) time.Time {
	if value := strings.TrimSpace(event.Metadata["hil_decision_expires_at"]); value != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
			return parsed.UTC()
		}
	}
	return event.CreatedAt.Add(holdTimeout).UTC()
}

func runtimeHILBridgeRequest(event EventRecord) (runtimeApprovalContext, runtimecore.ApprovalRequest, bool) {
	metadata := cloneStringMap(event.Metadata)
	meta := runtimeApprovalContext{
		ConversationID:   firstNonBlank(event.ConversationID, metadata["approval_conversation_id"]),
		RunID:            strings.TrimSpace(metadata["approval_run_id"]),
		AgentID:          strings.TrimSpace(metadata["approval_agent_id"]),
		LogicalAgentID:   strings.TrimSpace(metadata["approval_logical_agent_id"]),
		ConversationKind: strings.TrimSpace(metadata["approval_conversation_kind"]),
		RuntimeKind:      strings.TrimSpace(metadata["approval_runtime_kind"]),
		AssignedTo:       strings.TrimSpace(metadata["approval_assigned_to"]),
	}
	request := runtimecore.ApprovalRequest{
		Source:    firstNonBlank(metadata[runtimecore.ApprovalMetadataSource], metadata[runtimecore.ApprovalMetadataProvider]),
		RequestID: strings.TrimSpace(metadata[runtimecore.ApprovalMetadataRequestID]),
		SessionID: strings.TrimSpace(metadata[runtimecore.ApprovalMetadataSessionID]),
		TurnID:    strings.TrimSpace(metadata[runtimecore.ApprovalMetadataTurnID]),
		Kind:      runtimecore.ApprovalKind(strings.TrimSpace(metadata[runtimecore.ApprovalMetadataKind])),
		ToolName:  strings.TrimSpace(metadata[runtimecore.ApprovalMetadataToolName]),
		Metadata:  metadata,
	}
	return meta, request, meta.ConversationID != "" && meta.RunID != "" && request.RequestID != ""
}

func runtimeHILResourceID(meta runtimeApprovalContext, req runtimecore.ApprovalRequest) string {
	return "conversation:" + strings.TrimSpace(meta.ConversationID) + ":run:" + strings.TrimSpace(meta.RunID) + ":approval:" + strings.TrimSpace(req.RequestID)
}

func runtimeHILIdempotencyKey(meta runtimeApprovalContext, req runtimecore.ApprovalRequest, metadata ...map[string]string) string {
	if len(metadata) > 0 {
		if businessKey := strings.TrimSpace(metadata[0][approvalBusinessKeyMetadataKey]); businessKey != "" {
			digest := sha256.Sum256([]byte(businessKey))
			return "runtime-approval-business:sha256:" + hex.EncodeToString(digest[:])
		}
	}
	return "runtime-approval:" + strings.TrimSpace(meta.ConversationID) + ":" + strings.TrimSpace(meta.RunID) + ":" + strings.TrimSpace(req.RequestID)
}

func runtimeHILArgsHash(meta runtimeApprovalContext, req runtimecore.ApprovalRequest, metadata map[string]string) string {
	if materialHash := runtimeHILWechatMaterialArgsHash(req, metadata); materialHash != "" {
		return materialHash
	}
	payload, err := json.Marshal(struct {
		ConversationID string                   `json:"conversation_id"`
		RunID          string                   `json:"run_id"`
		RequestID      string                   `json:"request_id"`
		Kind           runtimecore.ApprovalKind `json:"kind"`
		ToolName       string                   `json:"tool_name"`
		Payload        json.RawMessage          `json:"payload,omitempty"`
		Metadata       map[string]string        `json:"metadata,omitempty"`
	}{
		ConversationID: strings.TrimSpace(meta.ConversationID),
		RunID:          strings.TrimSpace(meta.RunID),
		RequestID:      strings.TrimSpace(req.RequestID),
		Kind:           req.Kind,
		ToolName:       strings.TrimSpace(req.ToolName),
		Payload:        req.Payload,
		Metadata:       metadata,
	})
	if err != nil {
		payload = []byte(runtimeHILResourceID(meta, req))
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func runtimeHILPayloadPreview(metadata map[string]string) map[string]any {
	preview := map[string]any{}
	for _, key := range []string{
		runtimecore.ApprovalMetadataType,
		runtimecore.ApprovalMetadataKind,
		runtimecore.ApprovalMetadataToolName,
		runtimeapproval.MetadataIntentVersion,
		runtimeapproval.MetadataActionTypeID,
		runtimeapproval.MetadataIntentEffects,
		runtimeapproval.MetadataIntentRisk,
		runtimeapproval.MetadataIntentSource,
		runtimeapproval.MetadataIntentRecognized,
		runtimeapproval.MetadataResourceKind,
		"approval_runtime_kind",
		"approval_assigned_to",
		"approval_summary",
		"approval_description",
		"summary",
		"description",
		"message",
		"prompt",
		"question",
		"final_goal",
		"root_goal",
		"goal",
		"task_goal",
		"business_direction",
		"plan_summary",
		"query",
		"content",
		"reason",
		"role",
		"capability",
		"command",
		"path",
		"url",
		"payload_preview",
	} {
		if value := strings.TrimSpace(metadata[key]); value != "" {
			preview[key] = value
		}
	}
	for _, key := range []string{"approval_conversation_id", "approval_run_id", runtimecore.ApprovalMetadataRequestID} {
		if value := strings.TrimSpace(metadata[key]); value != "" {
			preview[key] = value
		}
	}
	if encoded := strings.TrimSpace(metadata[approvalMaterialMetadataKey]); encoded != "" {
		var material map[string]any
		if err := json.Unmarshal([]byte(encoded), &material); err == nil {
			for key, value := range material {
				preview[key] = value
			}
		}
	}
	preserveRuntimeHILDelegationPolicyMetadata(preview, metadata)
	return preview
}

// DecisionResolved is the HIL post-commit observer. The legacy discussion
// event remains only as the local bridge to a provider responder held in
// memory; the durable HIL receipt is the decision authority.
func (c *RuntimeApprovalCoordinator) DecisionResolved(ctx context.Context, resolution hil.DecisionResolution) {
	if !runtimeHILResolutionIsTerminal(resolution) {
		return
	}
	enabled, err := c.runtimeHILHumanReviewEnabled(ctx)
	if err != nil || !enabled {
		return
	}
	if !c.runtimeHILResolutionTargetsRuntimeApproval(ctx, resolution) {
		return
	}
	if err := c.applyHILResolution(ctx, resolution); err != nil {
		c.deferHILResolution(resolution)
	}
}

func runtimeHILResolutionIsRuntimeApproval(resolution hil.DecisionResolution) bool {
	return strings.EqualFold(strings.TrimSpace(resolution.Decision.Action), "runtime.approval") ||
		strings.EqualFold(strings.TrimSpace(resolution.Decision.Resource.Kind), "runtime_approval")
}

func (c *RuntimeApprovalCoordinator) runtimeHILResolutionTargetsRuntimeApproval(ctx context.Context, resolution hil.DecisionResolution) bool {
	decisionID := strings.TrimSpace(resolution.Decision.ID)
	if decisionID == "" {
		return false
	}
	c.mu.Lock()
	_, pending := c.pendingHIL[decisionID]
	c.mu.Unlock()
	if pending {
		return true
	}
	// Older observers carried only a decision ID. In both that compatibility
	// case and the ordinary runtime action case, prove the ID belongs to a
	// local bridge before queuing or applying the callback.
	_, _, found := c.findDurableHILRuntimeApproval(runtimeApprovalDurableContext(ctx), decisionID)
	return found
}

func runtimeHILResolutionIsTerminal(resolution hil.DecisionResolution) bool {
	if strings.TrimSpace(resolution.Decision.ID) == "" || strings.TrimSpace(resolution.Receipt.ID) == "" {
		return false
	}
	switch resolution.Decision.Status {
	case hil.DecisionResolved:
		return resolution.Receipt.Effect == hil.EffectAuthorize
	case hil.DecisionDenied:
		return resolution.Receipt.Effect == hil.EffectDeny
	default:
		return false
	}
}

// applyHILResolution commits the HIL result to the durable discussion bridge
// before touching a live provider responder. A cancelled provider, failed
// callback, or restarted coordinator therefore cannot discard a committed
// HIL receipt.
func (c *RuntimeApprovalCoordinator) applyHILResolution(ctx context.Context, resolution hil.DecisionResolution) error {
	if c == nil || strings.TrimSpace(resolution.Decision.ID) == "" || !runtimeHILResolutionIsTerminal(resolution) {
		return nil
	}
	decisionID := strings.TrimSpace(resolution.Decision.ID)
	c.mu.Lock()
	key := c.pendingHIL[decisionID]
	pending, pendingOK := c.pending[key]
	resolver := c.hilRuntimeResolver
	detachedResolver := c.hilDetachedResolver
	c.mu.Unlock()
	durableCtx := runtimeApprovalDurableContext(ctx)
	durableConversationID := ""
	durableEvent := EventRecord{}
	found := false
	if pendingOK {
		durableConversationID = pending.Event.ConversationID
		durableEvent = pending.Event
		found = true
	} else {
		// The provider may resolve after its original runtime context (or
		// process) is gone. Look up the local bridge from durable storage.
		durableConversationID, durableEvent, found = c.findDurableHILRuntimeApproval(durableCtx, decisionID)
	}
	if !found {
		return errRuntimeHILBridgeUnavailable
	}
	if resolver == nil {
		return errRuntimeHILBridgeUnavailable
	}
	status, continuation := runtimeApprovalContinuationFromHILResolution(resolution, c.now())
	viewer := ViewerRef{ViewerID: firstNonBlank(strings.TrimSpace(resolution.Actor.ID), "hil"), ViewerKind: runtimeApprovalViewerKind(resolution.Actor.Kind)}
	if _, err := resolver.DecideHILRuntimeApproval(durableCtx, viewer, RuntimeApprovalDecisionCommand{
		ConversationID:             durableConversationID,
		ApprovalID:                 durableEvent.EventID,
		HILDecisionID:              decisionID,
		HILDecisionStatus:          string(resolution.Decision.Status),
		HILReceiptID:               strings.TrimSpace(resolution.Receipt.ID),
		HILReceiptEffect:           string(resolution.Receipt.Effect),
		ResumeWithoutLiveResponder: !pendingOK,
		ActionID:                   continuation.ActionID,
		Message:                    continuation.Message,
		IdempotencyKey:             "hil-runtime-resolution:" + decisionID + ":" + firstNonBlank(strings.TrimSpace(resolution.Receipt.ID), string(resolution.Decision.Status)) + ":" + durableEvent.EventID,
	}); err != nil {
		return err
	}
	view := ConversationRecordView{}
	shouldResumeDetached := detachedResolver != nil && continuation.Decision == runtimecore.ApprovalDecisionApproved
	if shouldResumeDetached {
		var err error
		view, err = c.repo.GetConversation(durableCtx, durableConversationID)
		if err != nil {
			return err
		}
		if refreshed, ok := findRuntimeApprovalEventByHILDecisionID(view.Events, decisionID); ok {
			durableEvent = refreshed
		}
		shouldResumeDetached = strings.EqualFold(strings.TrimSpace(durableEvent.Metadata["approval_resume_required"]), "true")
	}
	if !pendingOK {
		if !shouldResumeDetached {
			return nil
		}
		return detachedResolver.ResumeDetachedHILRuntimeApproval(durableCtx, resolution, view, durableEvent)
	}
	metadata := cloneStringMap(pending.Event.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata["hil_decision_id"] = decisionID
	metadata["hil_decision_status"] = string(resolution.Decision.Status)
	metadata["hil_receipt_id"] = strings.TrimSpace(resolution.Receipt.ID)
	metadata["hil_receipt_effect"] = string(resolution.Receipt.Effect)
	metadata["approval_hil_resolution_reason"] = strings.TrimSpace(resolution.Reason)
	_, err := c.TryDecide(durableCtx, liveRuntimeApprovalDecision{
		Event:             pending.Event,
		Metadata:          metadata,
		Continuation:      continuation,
		Status:            status,
		Viewer:            viewer,
		DurableHILApplied: true,
	})
	if err != nil || !shouldResumeDetached {
		return err
	}
	return detachedResolver.ResumeDetachedHILRuntimeApproval(durableCtx, resolution, view, durableEvent)
}

func (c *RuntimeApprovalCoordinator) deferHILResolution(resolution hil.DecisionResolution) {
	if c == nil || strings.TrimSpace(resolution.Decision.ID) == "" || !runtimeHILResolutionIsTerminal(resolution) {
		return
	}
	decisionID := strings.TrimSpace(resolution.Decision.ID)
	c.mu.Lock()
	if c.hilResolutionRetry == nil {
		c.hilResolutionRetry = map[string]hil.DecisionResolution{}
	}
	c.hilResolutionRetry[decisionID] = resolution
	if c.hilResolutionActive == nil {
		c.hilResolutionActive = map[string]bool{}
	}
	if c.hilResolutionActive[decisionID] {
		c.mu.Unlock()
		return
	}
	c.hilResolutionActive[decisionID] = true
	c.mu.Unlock()
	go c.retryDeferredHILResolution(decisionID)
}

func (c *RuntimeApprovalCoordinator) retryDeferredHILResolution(decisionID string) {
	for attempt := 0; attempt < runtimeHILResolutionRetryAttempts; attempt++ {
		delay := runtimeHILResolutionRetryDelay << attempt
		timer := time.NewTimer(delay)
		<-timer.C
		timer.Stop()
		c.mu.Lock()
		resolution, ok := c.hilResolutionRetry[decisionID]
		c.mu.Unlock()
		if !ok {
			break
		}
		enabled, err := c.runtimeHILHumanReviewEnabled(context.Background())
		if err != nil || !enabled {
			break
		}
		if c.applyHILResolution(context.Background(), resolution) != nil {
			continue
		}
		c.mu.Lock()
		delete(c.hilResolutionRetry, decisionID)
		c.mu.Unlock()
		break
	}
	c.mu.Lock()
	// Do not retain a terminal callback indefinitely after its bounded retry
	// budget. Production reconciliation re-reads the receipt from HIL; this
	// prevents a permanently unavailable resolver from growing this process's
	// in-memory queue.
	delete(c.hilResolutionRetry, decisionID)
	delete(c.hilResolutionActive, decisionID)
	c.mu.Unlock()
}

// retryHILResolutionLookup gives a transient repository error a few chances
// to recover without turning one missing lookup into an unbounded goroutine.
// Durable startup reconciliation remains the fallback after this bounded run.
func (c *RuntimeApprovalCoordinator) retryHILResolutionLookup(conversation ConversationRecord, event EventRecord) {
	if c == nil || strings.TrimSpace(event.Metadata["hil_decision_id"]) == "" {
		return
	}
	for attempt := 0; attempt < runtimeHILResolutionRetryAttempts; attempt++ {
		timer := time.NewTimer(runtimeHILResolutionRetryDelay << attempt)
		<-timer.C
		timer.Stop()
		err := c.reconcileHILRuntimeApproval(context.Background(), conversation, event)
		if err == nil || errors.Is(err, errRuntimeHILResolutionPending) {
			return
		}
	}
}

// retryDeferredHILResolutions is called during startup/re-wiring and by the
// normal reconciliation sweep. Failed records remain queued; HIL's durable
// receipt is still scanned on the next startup even if this process exits.
func (c *RuntimeApprovalCoordinator) retryDeferredHILResolutions(ctx context.Context) error {
	if c == nil {
		return nil
	}
	enabled, err := c.runtimeHILHumanReviewEnabled(ctx)
	if err != nil || !enabled {
		return err
	}
	c.mu.Lock()
	queued := make([]hil.DecisionResolution, 0, len(c.hilResolutionRetry))
	for _, resolution := range c.hilResolutionRetry {
		queued = append(queued, resolution)
	}
	c.mu.Unlock()
	var firstErr error
	for _, resolution := range queued {
		decisionID := strings.TrimSpace(resolution.Decision.ID)
		if err := c.applyHILResolution(ctx, resolution); err != nil {
			firstErr = firstNonNilRuntimeHILError(firstErr, err)
			continue
		}
		c.mu.Lock()
		delete(c.hilResolutionRetry, decisionID)
		c.mu.Unlock()
	}
	return firstErr
}

func (c *RuntimeApprovalCoordinator) reconcileHILRuntimeApproval(ctx context.Context, conversation ConversationRecord, event EventRecord) error {
	if c == nil || strings.TrimSpace(event.Metadata["hil_decision_id"]) == "" {
		return nil
	}
	c.mu.Lock()
	service := c.hil
	c.mu.Unlock()
	lookup, ok := service.(RuntimeHILDecisionResolutionLookup)
	if !ok {
		return errRuntimeHILResolutionPending
	}
	tenantID := firstNonBlank(strings.TrimSpace(conversation.TenantID), "global")
	resolution, err := lookup.GetDecisionResolution(ctx, tenantID, strings.TrimSpace(event.Metadata["hil_decision_id"]))
	if err != nil {
		// A pending HIL decision has no terminal receipt. It is not a recovery
		// failure, so keep the local bridge open for the eventual observer.
		if errors.Is(err, hil.ErrConflict) || errors.Is(err, hil.ErrNotFound) {
			return errRuntimeHILResolutionPending
		}
		return err
	}
	if !runtimeHILResolutionIsTerminal(resolution) {
		return nil
	}
	return c.applyHILResolution(ctx, resolution)
}

func runtimeApprovalDurableContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}

func runtimeHILTerminalResolution(decision hil.DecisionRequest) (hil.DecisionResolution, bool) {
	if strings.TrimSpace(decision.ID) == "" {
		return hil.DecisionResolution{}, false
	}
	effect := hil.EffectDeny
	switch decision.Status {
	case hil.DecisionResolved:
		effect = hil.EffectAuthorize
	case hil.DecisionDenied:
		effect = hil.EffectDeny
	default:
		return hil.DecisionResolution{}, false
	}
	return hil.DecisionResolution{
		Decision: decision,
		Receipt: hil.DecisionReceipt{
			TenantID:  decision.TenantID,
			RequestID: decision.ID,
			ArgsHash:  decision.ArgsHash,
			Effect:    effect,
		},
		Actor:  hil.ActorRef{Kind: hil.ActorService, ID: "hil:replay"},
		Reason: "replayed terminal HIL decision",
	}, true
}

func runtimeApprovalContinuationFromHILResolution(resolution hil.DecisionResolution, now time.Time) (string, runtimecore.ApprovalContinuation) {
	status := "denied"
	continuation := runtimecore.ApprovalContinuation{
		Decision: runtimecore.ApprovalDecisionDenied,
		ActionID: "deny",
		Effect:   string(resolution.Receipt.Effect),
		Message:  strings.TrimSpace(resolution.Reason),
	}
	authorizationCurrent := resolution.Receipt.ExpiresAt.IsZero() || now.UTC().Before(resolution.Receipt.ExpiresAt.UTC())
	if resolution.Decision.Status == hil.DecisionResolved && resolution.Receipt.Effect == hil.EffectAuthorize && authorizationCurrent {
		status = "approved"
		continuation.Decision = runtimecore.ApprovalDecisionApproved
		continuation.ActionID = "approve_once"
	} else if resolution.Decision.Status == hil.DecisionResolved && resolution.Receipt.Effect == hil.EffectAuthorize {
		continuation.Effect = string(hil.EffectDeny)
		continuation.Message = "approval authorization expired; create a new business intent before retrying"
	}
	return status, continuation
}

func (c *RuntimeApprovalCoordinator) findDurableHILRuntimeApproval(ctx context.Context, decisionID string) (string, EventRecord, bool) {
	if c == nil || c.repo == nil || strings.TrimSpace(decisionID) == "" {
		return "", EventRecord{}, false
	}
	conversations, err := c.repo.ListConversations(ctx)
	if err != nil {
		return "", EventRecord{}, false
	}
	var latestConversationID string
	var latest EventRecord
	found := false
	for _, summary := range conversations {
		view, err := c.repo.GetConversation(ctx, summary.ConversationID)
		if err != nil {
			continue
		}
		if event, ok := findRuntimeApprovalEventByHILDecisionID(view.Events, decisionID); ok {
			if runtimeApprovalIsPending(event.Status) {
				if !found || !runtimeApprovalIsPending(latest.Status) || event.CreatedAt.After(latest.CreatedAt) {
					latestConversationID, latest, found = view.ConversationID, event, true
				}
			} else if !found || (!runtimeApprovalIsPending(latest.Status) && event.CreatedAt.After(latest.CreatedAt)) {
				latestConversationID, latest, found = view.ConversationID, event, true
			}
		}
	}
	return latestConversationID, latest, found
}

func runtimeApprovalViewerKind(kind hil.ActorKind) ViewerKind {
	switch kind {
	case hil.ActorHuman:
		return ViewerKindHuman
	case hil.ActorAgent:
		return ViewerKindAgent
	default:
		return ViewerKindSystem
	}
}
