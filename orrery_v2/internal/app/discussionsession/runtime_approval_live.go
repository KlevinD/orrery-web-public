package discussionsession

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/app/runtimeapproval"
)

type runtimeApprovalContext struct {
	ConversationID   string
	RunID            string
	AgentID          string
	LogicalAgentID   string
	ConversationKind string
	RuntimeKind      string
	AssignedTo       string
	notify           func(EventRecord)
}

type runtimeApprovalContextKey struct{}

// RuntimeApprovalContext identifies the durable discussion/HIL bridge for a
// runtime permission request.
type RuntimeApprovalContext struct {
	ConversationID   string
	RunID            string
	AgentID          string
	LogicalAgentID   string
	ConversationKind string
	RuntimeKind      string
	AssignedTo       string
}

// ContextWithRuntimeApproval attaches the approval bridge identity for a
// runtime invoked outside the discussion runner, such as a scheduled task.
func ContextWithRuntimeApproval(ctx context.Context, value RuntimeApprovalContext) context.Context {
	return contextWithRuntimeApproval(ctx, runtimeApprovalContext{
		ConversationID:   value.ConversationID,
		RunID:            value.RunID,
		AgentID:          value.AgentID,
		LogicalAgentID:   value.LogicalAgentID,
		ConversationKind: value.ConversationKind,
		RuntimeKind:      value.RuntimeKind,
		AssignedTo:       value.AssignedTo,
	})
}

// RuntimeApprovalContextFrom returns the approval bridge identity carried by
// a runtime context.
func RuntimeApprovalContextFrom(ctx context.Context) (RuntimeApprovalContext, bool) {
	value, ok := runtimeApprovalContextFrom(ctx)
	return RuntimeApprovalContext{
		ConversationID:   value.ConversationID,
		RunID:            value.RunID,
		AgentID:          value.AgentID,
		LogicalAgentID:   value.LogicalAgentID,
		ConversationKind: value.ConversationKind,
		RuntimeKind:      value.RuntimeKind,
		AssignedTo:       value.AssignedTo,
	}, ok
}

func contextWithRuntimeApproval(ctx context.Context, value runtimeApprovalContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, runtimeApprovalContextKey{}, value)
}

func runtimeApprovalContextFrom(ctx context.Context) (runtimeApprovalContext, bool) {
	if ctx == nil {
		return runtimeApprovalContext{}, false
	}
	value, ok := ctx.Value(runtimeApprovalContextKey{}).(runtimeApprovalContext)
	if !ok {
		return runtimeApprovalContext{}, false
	}
	value.ConversationID = strings.TrimSpace(value.ConversationID)
	value.RunID = strings.TrimSpace(value.RunID)
	value.AgentID = strings.TrimSpace(value.AgentID)
	value.LogicalAgentID = strings.TrimSpace(value.LogicalAgentID)
	value.ConversationKind = strings.TrimSpace(value.ConversationKind)
	value.RuntimeKind = strings.TrimSpace(value.RuntimeKind)
	value.AssignedTo = strings.TrimSpace(value.AssignedTo)
	return value, value.ConversationID != "" && value.RunID != ""
}

type RuntimeApprovalCoordinatorOptions struct {
	Repository          *Repository
	RealtimePublisher   RealtimePublisher
	Clock               Clock
	ApprovalHoldTimeout time.Duration
	HumanReviewEnabled  func(context.Context) (bool, error)
	HIL                 RuntimeHILDecisionService
	Decisions           RuntimeApprovalDecisionService
}

type RuntimeApprovalDecisionService interface {
	Decide(context.Context, runtimeapproval.DecisionInput) runtimeapproval.Decision
}

type RuntimeApprovalCoordinator struct {
	repo                *Repository
	realtime            RealtimePublisher
	clock               Clock
	approvalHoldTimeout time.Duration
	humanReviewEnabled  func(context.Context) (bool, error)
	mu                  sync.Mutex
	// hilCreateMu serializes the bridge-to-HIL transition. A startup
	// reconciliation can observe the durable bridge after it is saved but
	// before its HIL decision ID is linked; only one caller may create that
	// idempotent HIL request during this interval.
	hilCreateMu         sync.Mutex
	hilReconcileMu      sync.Mutex
	pending             map[string]liveRuntimeApproval
	pendingHIL          map[string]string
	hilResolutionRetry  map[string]hil.DecisionResolution
	hilResolutionActive map[string]bool
	hil                 RuntimeHILDecisionService
	hilRuntimeResolver  RuntimeHILRuntimeApprovalResolver
	hilDetachedResolver RuntimeHILDetachedApprovalResolver
	decisions           RuntimeApprovalDecisionService
}

type liveRuntimeApproval struct {
	Context       runtimeApprovalContext
	Request       runtimecore.ApprovalRequest
	Responder     runtimecore.ApprovalResponder
	Event         EventRecord
	HILDecisionID string
}

type liveRuntimeApprovalDecision struct {
	Event        EventRecord
	Run          RunRecord
	Metadata     map[string]string
	Continuation runtimecore.ApprovalContinuation
	Status       string
	Viewer       ViewerRef
	// DurableHILApplied means the Service has already recorded the outcome.
	// TryDecide should only deliver the in-memory provider response in that
	// case, without writing a competing legacy transition.
	DurableHILApplied bool
}

func NewRuntimeApprovalCoordinator(opts RuntimeApprovalCoordinatorOptions) *RuntimeApprovalCoordinator {
	approvalHoldTimeout := opts.ApprovalHoldTimeout
	if approvalHoldTimeout <= 0 {
		approvalHoldTimeout = defaultRuntimeApprovalHoldTimeout
	}
	return &RuntimeApprovalCoordinator{
		repo:                opts.Repository,
		realtime:            opts.RealtimePublisher,
		clock:               opts.Clock,
		approvalHoldTimeout: approvalHoldTimeout,
		humanReviewEnabled:  opts.HumanReviewEnabled,
		pending:             map[string]liveRuntimeApproval{},
		pendingHIL:          map[string]string{},
		hilResolutionRetry:  map[string]hil.DecisionResolution{},
		hilResolutionActive: map[string]bool{},
		hil:                 opts.HIL,
		decisions:           opts.Decisions,
	}
}

const defaultRuntimeApprovalHoldTimeout = 10 * time.Minute

func (c *RuntimeApprovalCoordinator) RuntimeApprovalHoldTimeout() time.Duration {
	if c == nil || c.approvalHoldTimeout <= 0 {
		return defaultRuntimeApprovalHoldTimeout
	}
	return c.approvalHoldTimeout
}

func (c *RuntimeApprovalCoordinator) RequestApproval(ctx context.Context, req runtimecore.ApprovalRequest, respond runtimecore.ApprovalResponder) error {
	if c == nil || c.repo == nil || respond == nil {
		return runtimecore.DeferredApprovalBroker{}.RequestApproval(ctx, req, respond)
	}
	meta, ok := runtimeApprovalContextFrom(ctx)
	if !ok {
		return runtimecore.DeferredApprovalBroker{}.RequestApproval(ctx, req, respond)
	}
	req.RequestID = strings.TrimSpace(req.RequestID)
	if req.RequestID == "" {
		return runtimecore.DeferredApprovalBroker{}.RequestApproval(ctx, req, respond)
	}
	if strings.TrimSpace(req.SessionID) == "" {
		req.SessionID = strings.TrimSpace(req.Metadata[runtimecore.ApprovalMetadataSessionID])
	}
	if strings.TrimSpace(req.TurnID) == "" {
		req.TurnID = strings.TrimSpace(req.Metadata[runtimecore.ApprovalMetadataTurnID])
	}
	approvalMetadata := runtimecore.ApprovalMetadata(req)
	putRuntimeApprovalMetadata(approvalMetadata, "approval_conversation_id", meta.ConversationID)
	putRuntimeApprovalMetadata(approvalMetadata, "approval_run_id", meta.RunID)
	putRuntimeApprovalMetadata(approvalMetadata, "approval_agent_id", meta.AgentID)
	putRuntimeApprovalMetadata(approvalMetadata, "approval_logical_agent_id", meta.LogicalAgentID)
	putRuntimeApprovalMetadata(approvalMetadata, "approval_conversation_kind", meta.ConversationKind)
	putRuntimeApprovalMetadata(approvalMetadata, "approval_runtime_kind", meta.RuntimeKind)
	conversation, err := c.repo.GetConversation(ctx, meta.ConversationID)
	if err != nil {
		return err
	}
	meta.AssignedTo = runtimeHILApprovalAssignee(meta.AssignedTo, conversation)
	putRuntimeApprovalMetadata(approvalMetadata, "approval_assigned_to", meta.AssignedTo)
	humanReviewEnabled, err := c.runtimeHILHumanReviewEnabled(ctx)
	if err != nil {
		return err
	}
	// A provider may retry delivery after the process has persisted the local
	// event but before its responder returns. Reuse the durable event and only
	// replace its live responder; the original stream already received it.
	if existing, ok := findRuntimeApprovalEventForRun(conversation.Events, meta.RunID, req.RequestID); humanReviewEnabled && ok && runtimeApprovalIsPending(existing.Status) {
		var created *hil.CreateDecisionResult
		hilBacked := runtimeApprovalIsHILBacked(existing)
		if humanReviewEnabled && hilBacked && strings.TrimSpace(existing.Metadata["hil_decision_id"]) == "" {
			var ensureErr error
			created, existing, ensureErr = c.ensureRuntimeHILDecision(runtimeApprovalDurableContext(ctx), conversation.ConversationRecord, meta, req, existing)
			if ensureErr != nil {
				return ensureErr
			}
		}
		key := liveRuntimeApprovalKey(meta.ConversationID, meta.RunID, req.RequestID)
		hilDecisionID := strings.TrimSpace(existing.Metadata["hil_decision_id"])
		alreadyLive := c.remember(key, liveRuntimeApproval{Context: meta, Request: req, Responder: respond, Event: existing, HILDecisionID: hilDecisionID})
		go c.removeWhenDone(ctx, key, meta.RunID, req.RequestID)
		if hilDecisionID != "" && strings.EqualFold(strings.TrimSpace(existing.Metadata["hil_decision_status"]), string(hil.DecisionPending)) {
			go c.expireWhenDone(meta.ConversationID, meta.RunID, req.RequestID, hilDecisionID, runtimeApprovalHILExpiry(existing))
		}
		if created != nil {
			c.applyCreatedRuntimeHILDecision(runtimeApprovalDurableContext(ctx), conversation.ConversationRecord, existing, *created)
		} else if hilBacked {
			if err := c.reconcileHILRuntimeApproval(runtimeApprovalDurableContext(ctx), conversation.ConversationRecord, existing); err != nil && !errors.Is(err, errRuntimeHILResolutionPending) {
				return err
			}
		}
		if !alreadyLive {
			_ = c.appendSnapshotInvalidated(ctx, meta.ConversationID, "runtime_approval_requested", req.RequestID)
			c.notifyRuntimeApprovalRequested(ctx, meta, existing)
		}
		return nil
	}
	material, err := buildRuntimeApprovalMaterial(req)
	if err != nil {
		return err
	}
	if material != nil {
		encoded, err := json.Marshal(material)
		if err != nil {
			return fmt.Errorf("encode approval material: %w", err)
		}
		approvalMetadata[approvalMaterialMetadataKey] = string(encoded)
		putRuntimeApprovalMaterialBinding(approvalMetadata, *material)
	}
	conversationKind := firstNonBlank(meta.ConversationKind, conversation.ConversationKind)
	// The discussion API and header render legacy empty values as manual. Keep
	// the runtime decision input aligned with that visible conversation mode.
	conversationHILMode := normalizeDiscussionHILMode(conversation.HILMode)
	explicitHIL := conversationHILMode
	explicitApprovalPolicy := ""
	if conversationHILMode == DiscussionHILModeNone {
		explicitHIL = DiscussionHILModeAuto
		explicitApprovalPolicy = string(runtimeapproval.ApprovalPolicyNever)
	}
	decision := runtimeapproval.Decision{
		Outcome:                    runtimeapproval.DecisionOutcomeHuman,
		EffectiveHIL:               runtimeapproval.HILMode(explicitHIL),
		EffectiveApprovalPolicy:    runtimeapproval.ApprovalPolicyOnRequest,
		EffectiveApprovalsReviewer: runtimeapproval.ApprovalsReviewerHuman,
		DecisionSource:             "human",
		Reason:                     "decision_service_unavailable",
		Intent:                     runtimeapproval.ClassifyActionIntent(req),
	}
	if explicitHIL == DiscussionHILModeAuto {
		decision.EffectiveApprovalsReviewer = runtimeapproval.ApprovalsReviewerAI
	}
	if c.decisions != nil {
		decision = c.decisions.Decide(ctx, runtimeapproval.DecisionInput{
			Request:                            req,
			LogicalAgentID:                     meta.LogicalAgentID,
			ConversationKind:                   conversationKind,
			ExplicitConversationHIL:            explicitHIL,
			ExplicitConversationApprovalPolicy: explicitApprovalPolicy,
		})
	}
	decision.Intent = runtimeapproval.EnsureActionIntent(req, decision.Intent)
	if !humanReviewEnabled && decision.Outcome == runtimeapproval.DecisionOutcomeHuman {
		decision.Outcome = runtimeapproval.DecisionOutcomeApproveOnce
		decision.EffectiveHIL = runtimeapproval.HILModeAuto
		decision.EffectiveApprovalPolicy = runtimeapproval.ApprovalPolicyNever
		decision.DecisionSource = "approval_policy_never"
		decision.Reason = "human_review_disabled"
	}
	applyRuntimeApprovalDecisionMetadata(approvalMetadata, decision)
	event := EventRecord{
		EventID:        discussionRuntimeApprovalEventID(meta.ConversationID, meta.RunID, req.RequestID),
		ConversationID: meta.ConversationID,
		Kind:           discussionRuntimeApprovalEventKind,
		Status:         "pending",
		Summary:        discussionRuntimeApprovalSummary(approvalMetadata),
		Metadata:       approvalMetadata,
		CreatedAt:      c.now(),
	}
	if decision.DecisionSource == "forced_human" {
		event.Metadata["approval_effective_mode"] = "manual"
	}
	if decision.Outcome == runtimeapproval.DecisionOutcomeApproveOnce {
		event.Status = "approved"
		event.Metadata["approval_decision"] = "approve_once"
		event.Metadata["approval_decided_by"] = "runtime:" + firstNonBlank(decision.DecisionSource, "automatic")
		event.Metadata["approval_decision_scope"] = "once"
		if err := c.repo.SaveEvent(ctx, event); err != nil {
			return err
		}
		if err := respond(runtimecore.ApprovalResponse{Decision: runtimecore.ApprovalDecisionApproved}); err != nil {
			event.Status = "denied"
			event.Metadata["approval_decision"] = "denied"
			event.Metadata["approval_error"] = strings.TrimSpace(err.Error())
			_ = c.repo.SaveEvent(ctx, event)
			return err
		}
		_ = c.appendSnapshotInvalidated(ctx, meta.ConversationID, "runtime_approval_auto_decided", req.RequestID)
		return nil
	}
	if decision.Outcome == runtimeapproval.DecisionOutcomeDeny {
		event.Status = "denied"
		event.Metadata["approval_decision"] = "denied"
		event.Metadata["approval_decided_by"] = "runtime:policy"
		if err := c.repo.SaveEvent(ctx, event); err != nil {
			return err
		}
		if err := respond(runtimecore.ApprovalResponse{Decision: runtimecore.ApprovalDecisionDenied, Reason: decision.Reason}); err != nil {
			event.Metadata["approval_error"] = strings.TrimSpace(err.Error())
			_ = c.repo.SaveEvent(ctx, event)
			return err
		}
		_ = c.appendSnapshotInvalidated(ctx, meta.ConversationID, "runtime_approval_policy_denied", req.RequestID)
		return nil
	}
	c.mu.Lock()
	hilService := c.hil
	c.mu.Unlock()
	var hilDecision *hil.CreateDecisionResult
	if hilService != nil {
		// Persist the recoverable HIL bridge before calling HIL. A restart after
		// this point retries the immutable idempotency key during reconciliation.
		event = c.prepareRuntimeHILBridge(event, meta, req)
		if err := c.repo.SaveEvent(ctx, event); err != nil {
			return err
		}
		created, linked, createErr := c.ensureRuntimeHILDecision(runtimeApprovalDurableContext(ctx), conversation.ConversationRecord, meta, req, event)
		if createErr != nil {
			return createErr
		}
		hilDecision = created
		event = linked
	} else if err := c.repo.SaveEvent(ctx, event); err != nil {
		// Preserve legacy/manual approval behavior for deployments that have
		// not composed a HIL service yet.
		return err
	}
	key := liveRuntimeApprovalKey(meta.ConversationID, meta.RunID, req.RequestID)
	hilDecisionID := strings.TrimSpace(event.Metadata["hil_decision_id"])
	c.mu.Lock()
	if c.pending == nil {
		c.pending = map[string]liveRuntimeApproval{}
	}
	if c.pendingHIL == nil {
		c.pendingHIL = map[string]string{}
	}
	c.pending[key] = liveRuntimeApproval{Context: meta, Request: req, Responder: respond, Event: event, HILDecisionID: hilDecisionID}
	if hilDecisionID != "" {
		c.pendingHIL[hilDecisionID] = key
	}
	c.mu.Unlock()
	_ = c.appendSnapshotInvalidated(ctx, meta.ConversationID, "runtime_approval_requested", req.RequestID)
	go c.removeWhenDone(ctx, key, meta.RunID, req.RequestID)
	if hilDecision != nil {
		c.applyCreatedRuntimeHILDecision(runtimeApprovalDurableContext(ctx), conversation.ConversationRecord, event, *hilDecision)
	}
	c.notifyRuntimeApprovalRequested(ctx, meta, event)
	if hilDecisionID != "" && strings.EqualFold(strings.TrimSpace(event.Metadata["hil_decision_status"]), string(hil.DecisionPending)) {
		go c.expireWhenDone(meta.ConversationID, meta.RunID, req.RequestID, hilDecisionID, runtimeApprovalHILExpiry(event))
	}
	return nil
}

func applyRuntimeApprovalDecisionMetadata(metadata map[string]string, decision runtimeapproval.Decision) {
	putRuntimeApprovalMetadata(metadata, "approval_hil_mode", string(decision.EffectiveHIL))
	putRuntimeApprovalMetadata(metadata, "approval_effective_hil_mode", string(decision.EffectiveHIL))
	putRuntimeApprovalMetadata(metadata, "approval_effective_mode", string(decision.EffectiveHIL))
	putRuntimeApprovalMetadata(metadata, "approval_policy", string(decision.EffectiveApprovalPolicy))
	putRuntimeApprovalMetadata(metadata, "approval_reviewer", string(decision.EffectiveApprovalsReviewer))
	policySource := strings.Join([]string{
		"hil=" + strings.TrimSpace(decision.PolicySource.HILMode),
		"approval_policy=" + strings.TrimSpace(decision.PolicySource.ApprovalPolicy),
		"approval_reviewer=" + strings.TrimSpace(decision.PolicySource.ApprovalsReviewer),
		"exec=" + strings.TrimSpace(decision.PolicySource.Exec),
		"tools=" + strings.TrimSpace(decision.PolicySource.Tools),
	}, ";")
	if policySource != "hil=;approval_policy=;approval_reviewer=;exec=;tools=" {
		putRuntimeApprovalMetadata(metadata, "approval_policy_source", policySource)
	}
	putRuntimeApprovalMetadata(metadata, "approval_policy_digest", decision.PolicyDigest)
	putRuntimeApprovalMetadata(metadata, "approval_decision_source", decision.DecisionSource)
	putRuntimeApprovalMetadata(metadata, "approval_decision_reason", decision.Reason)
	putRuntimeApprovalMetadata(metadata, "approval_rule_id", decision.RuleID)
	putRuntimeApprovalMetadata(metadata, "approval_input_digest", decision.InputDigest)
	// Intent fields cross a trust boundary: provider metadata must never replace
	// the classification produced by the server-owned action registry.
	metadata[runtimeapproval.MetadataIntentVersion] = "1"
	metadata[runtimeapproval.MetadataActionTypeID] = string(decision.Intent.ActionTypeID)
	metadata[runtimeapproval.MetadataIntentEffects] = strings.Join(decision.Intent.Effects, ",")
	metadata[runtimeapproval.MetadataIntentRisk] = string(decision.Intent.RiskLevel)
	metadata[runtimeapproval.MetadataIntentSource] = decision.Intent.Source
	metadata[runtimeapproval.MetadataIntentRecognized] = strconv.FormatBool(decision.Intent.Recognized)
	metadata[runtimeapproval.MetadataResourceKind] = decision.Intent.ResourceKind
	if decision.Review == nil {
		return
	}
	putRuntimeApprovalMetadata(metadata, "approval_reviewer_profile_id", decision.Review.ModelProfile)
	putRuntimeApprovalMetadata(metadata, "approval_reviewer_prompt_version", decision.Review.PromptVersion)
	putRuntimeApprovalMetadata(metadata, "approval_reviewer_latency_ms", strconv.FormatInt(decision.Review.Latency.Milliseconds(), 10))
	putRuntimeApprovalMetadata(metadata, "approval_reviewer_risk", decision.Review.Risk)
	putRuntimeApprovalMetadata(metadata, "approval_reviewer_rationale", decision.Review.Rationale)
	putRuntimeApprovalMetadata(metadata, "approval_reviewer_reason", decision.Review.ReasonCode)
}

func (c *RuntimeApprovalCoordinator) notifyRuntimeApprovalRequested(ctx context.Context, meta runtimeApprovalContext, event EventRecord) {
	if c != nil && c.repo != nil {
		view, err := c.repo.GetConversation(runtimeApprovalDurableContext(ctx), event.ConversationID)
		if err == nil {
			requestID := strings.TrimSpace(event.Metadata[runtimecore.ApprovalMetadataRequestID])
			if current, ok := findRuntimeApprovalEventForRun(view.Events, meta.RunID, requestID); ok {
				event = current
			}
		}
	}
	if meta.notify == nil || !runtimeApprovalIsPending(event.Status) {
		return
	}
	meta.notify(event)
}

func (c *RuntimeApprovalCoordinator) conversationHILMode(ctx context.Context, conversationID string) (string, error) {
	view, err := c.repo.GetConversation(ctx, strings.TrimSpace(conversationID))
	if err != nil {
		return "", err
	}
	return normalizeDiscussionHILMode(view.HILMode), nil
}

func (c *RuntimeApprovalCoordinator) TryDecide(ctx context.Context, decision liveRuntimeApprovalDecision) (bool, error) {
	if c == nil {
		return false, nil
	}
	requestID := strings.TrimSpace(decision.Metadata[runtimecore.ApprovalMetadataRequestID])
	if requestID == "" {
		return false, nil
	}
	runID := strings.TrimSpace(decision.Metadata["approval_run_id"])
	key := liveRuntimeApprovalKey(decision.Event.ConversationID, runID, requestID)
	c.mu.Lock()
	pending, ok := c.pending[key]
	if ok {
		delete(c.pending, key)
		if pending.HILDecisionID != "" {
			if mappedKey, exists := c.pendingHIL[pending.HILDecisionID]; exists && mappedKey == key {
				delete(c.pendingHIL, pending.HILDecisionID)
			}
		}
	}
	c.mu.Unlock()
	if !ok {
		return false, nil
	}
	at := firstTimeAfter(c.now(), decision.Event.CreatedAt)
	response := runtimecore.ApprovalResponse{
		Decision: liveRuntimeApprovalResponseDecision(decision.Continuation.Decision, decision.Status),
		Reason:   strings.TrimSpace(decision.Continuation.Message),
		Remember: decision.Continuation.Remember,
		Metadata: map[string]any{
			"selected":            strings.TrimSpace(decision.Continuation.ChoiceID),
			"modified_input":      strings.TrimSpace(decision.Continuation.ModifiedInput),
			"approval_id":         strings.TrimSpace(decision.Event.EventID),
			"approval_mode":       runtimeApprovalResponseMode(decision.Metadata),
			"approval_action_id":  strings.TrimSpace(decision.Continuation.ActionID),
			"approval_decided_by": strings.TrimSpace(decision.Viewer.ViewerID),
			"approval_decided_at": at.UTC().Format(time.RFC3339Nano),
		},
	}
	if modified := strings.TrimSpace(decision.Continuation.ModifiedInput); modified != "" {
		if liveRuntimeApprovalSupportsModifiedInput(pending.Request) {
			response.Content = modified
		} else {
			response.Decision = runtimecore.ApprovalDecisionDenied
			response.Reason = unsupportedModifiedLiveApprovalMessage
			decision.Status = "denied"
			decision.Metadata = cloneStringMap(decision.Metadata)
			if decision.Metadata == nil {
				decision.Metadata = map[string]string{}
			}
			decision.Metadata["approval_error"] = unsupportedModifiedLiveApprovalMessage
		}
	}
	if err := pending.Responder(response); err != nil {
		// HIL has already committed the authority. A failed live callback must
		// not transform an authorize receipt into a local denial; leave the
		// bridge retryable for the durable resolver instead.
		if strings.TrimSpace(pending.HILDecisionID) != "" {
			c.restorePending(key, pending)
			return false, err
		}
		metadata := cloneStringMap(decision.Metadata)
		if metadata == nil {
			metadata = map[string]string{}
		}
		metadata["approval_error"] = strings.TrimSpace(err.Error())
		decided := decidedRuntimeApprovalEvent(decision.Event, metadata, decision.Continuation, "denied", decision.Viewer, "", at)
		if saveErr := c.repo.SaveEvent(ctx, decided); saveErr != nil {
			return true, saveErr
		}
		_ = c.appendSnapshotInvalidated(ctx, decision.Event.ConversationID, "runtime_approval_decided", requestID)
		return true, nil
	}
	if decision.DurableHILApplied {
		return true, nil
	}
	decided := decidedRuntimeApprovalEvent(decision.Event, decision.Metadata, decision.Continuation, decision.Status, decision.Viewer, "", at)
	if err := c.repo.SaveEvent(ctx, decided); err != nil {
		if strings.TrimSpace(pending.HILDecisionID) != "" {
			c.restorePending(key, pending)
			return false, err
		}
		return true, err
	}
	_ = c.appendSnapshotInvalidated(ctx, decision.Event.ConversationID, "runtime_approval_decided", requestID)
	return true, nil
}

func runtimeApprovalResponseMode(metadata map[string]string) string {
	if strings.TrimSpace(metadata["hil_decision_id"]) != "" {
		return "hil"
	}
	return "manual"
}

func (c *RuntimeApprovalCoordinator) remember(key string, approval liveRuntimeApproval) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending == nil {
		c.pending = map[string]liveRuntimeApproval{}
	}
	if c.pendingHIL == nil {
		c.pendingHIL = map[string]string{}
	}
	_, alreadyLive := c.pending[key]
	c.pending[key] = approval
	if approval.HILDecisionID != "" {
		c.pendingHIL[approval.HILDecisionID] = key
	}
	return alreadyLive
}

func (c *RuntimeApprovalCoordinator) restorePending(key string, approval liveRuntimeApproval) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending == nil {
		c.pending = map[string]liveRuntimeApproval{}
	}
	if c.pendingHIL == nil {
		c.pendingHIL = map[string]string{}
	}
	if _, exists := c.pending[key]; exists {
		return
	}
	c.pending[key] = approval
	if approval.HILDecisionID != "" {
		if _, exists := c.pendingHIL[approval.HILDecisionID]; !exists {
			c.pendingHIL[approval.HILDecisionID] = key
		}
	}
}

func (c *RuntimeApprovalCoordinator) removeWhenDone(ctx context.Context, key string, runID string, requestID string) {
	if ctx == nil {
		return
	}
	<-ctx.Done()
	c.mu.Lock()
	pending, ok := c.pending[key]
	if ok &&
		strings.EqualFold(strings.TrimSpace(pending.Context.RunID), strings.TrimSpace(runID)) &&
		strings.EqualFold(strings.TrimSpace(pending.Request.RequestID), strings.TrimSpace(requestID)) {
		delete(c.pending, key)
		if pending.HILDecisionID != "" {
			if mappedKey, exists := c.pendingHIL[pending.HILDecisionID]; exists && mappedKey == key {
				delete(c.pendingHIL, pending.HILDecisionID)
			}
		}
	}
	c.mu.Unlock()
}

func (c *RuntimeApprovalCoordinator) expireWhenDone(conversationID string, runID string, requestID string, hilDecisionID string, expiresAt time.Time) {
	if c == nil || strings.TrimSpace(hilDecisionID) == "" {
		return
	}
	if expiresAt.IsZero() {
		expiresAt = c.now().Add(c.RuntimeApprovalHoldTimeout())
	}
	if delay := expiresAt.Sub(c.now()); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		<-timer.C
	}
	_ = c.expireLiveHILRuntimeApproval(conversationID, runID, requestID, hilDecisionID)
}

func (c *RuntimeApprovalCoordinator) expireLiveHILRuntimeApproval(conversationID string, runID string, requestID string, hilDecisionID string) error {
	if c == nil || strings.TrimSpace(hilDecisionID) == "" {
		return nil
	}
	c.mu.Lock()
	resolver := c.hilRuntimeResolver
	c.mu.Unlock()
	if resolver == nil {
		return errRuntimeHILBridgeUnavailable
	}
	durableCtx := context.Background()
	if _, err := resolver.ExpireHILRuntimeApproval(durableCtx, conversationID, hilDecisionID, "hil_decision_expired"); err != nil {
		return err
	}
	key := liveRuntimeApprovalKey(conversationID, runID, requestID)
	c.mu.Lock()
	pending, ok := c.pending[key]
	c.mu.Unlock()
	if !ok {
		return nil
	}
	metadata := cloneStringMap(pending.Event.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata["approval_decision"] = "expired"
	metadata["approval_closed_reason"] = "hil_decision_expired"
	metadata["hil_decision_status"] = "expired"
	_, err := c.TryDecide(durableCtx, liveRuntimeApprovalDecision{
		Event:    pending.Event,
		Metadata: metadata,
		Continuation: runtimecore.ApprovalContinuation{
			Decision: runtimecore.ApprovalDecisionDenied,
			ActionID: "deny",
			Effect:   string(hil.EffectDeny),
			Message:  "approval window expired",
		},
		Status:            "expired",
		Viewer:            ViewerRef{ViewerID: "hil:expiry", ViewerKind: ViewerKindSystem},
		DurableHILApplied: true,
	})
	return err
}

func (c *RuntimeApprovalCoordinator) appendSnapshotInvalidated(ctx context.Context, conversationID string, reason string, requestID string) error {
	if c == nil || c.realtime == nil {
		return nil
	}
	return appendRealtimeConversationEvent(ctx, c.realtime, RealtimeEventDiscussionSnapshotInvalidated, "discussion-snapshot-invalidated-"+conversationID+"-"+reason+"-"+discussionRuntimeApprovalIDSegment(requestID), conversationID, map[string]string{
		"conversation_id": conversationID,
		"reason":          reason,
	})
}

func (c *RuntimeApprovalCoordinator) now() time.Time {
	if c != nil && c.clock != nil {
		return c.clock.Now().UTC()
	}
	return time.Now().UTC()
}

func liveRuntimeApprovalKey(conversationID string, runID string, requestID string) string {
	return strings.TrimSpace(conversationID) + "\x00" + strings.TrimSpace(runID) + "\x00" + strings.TrimSpace(requestID)
}

func liveRuntimeApprovalResponseDecision(decision runtimecore.ApprovalDecision, status string) runtimecore.ApprovalDecision {
	if decision == runtimecore.ApprovalDecisionApproved && strings.EqualFold(strings.TrimSpace(status), "approved") {
		return runtimecore.ApprovalDecisionApproved
	}
	return runtimecore.ApprovalDecisionDenied
}

const unsupportedModifiedLiveApprovalMessage = "modified approval is not supported for this live approval request"

func liveRuntimeApprovalSupportsModifiedInput(req runtimecore.ApprovalRequest) bool {
	if !strings.EqualFold(runtimecore.ApprovalSource(req), "codex") {
		return true
	}
	return req.Kind == runtimecore.ApprovalKindMCPElicitation
}

func appendRealtimeConversationEvent(ctx context.Context, realtime RealtimePublisher, eventType string, eventID string, conversationID string, payload map[string]string) error {
	if realtime == nil {
		return nil
	}
	conversationID = strings.TrimSpace(conversationID)
	raw, err := realtimePayload(payload)
	if err != nil {
		return err
	}
	return realtime.AppendRealtimeEvent(ctx, RealtimeEvent{
		EventID:       eventID,
		Type:          eventType,
		Channel:       "conversation:" + conversationID,
		AggregateType: RealtimeAggregateConversation,
		AggregateID:   conversationID,
		Payload:       raw,
	})
}
