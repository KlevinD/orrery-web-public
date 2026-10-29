package hil

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// ApprovalDisplayPayloadKey contains user-facing approval context that does
// not change the identity or executable scope of a decision request.
const ApprovalDisplayPayloadKey = "approval_display"

type ContextProvider interface {
	BuildDecisionContext(context.Context, DecisionContextRequest) (OntologyContextSnapshot, error)
}

type IDGenerator interface {
	NewIDContext() (string, error)
}

type DecisionContextRequest struct {
	TenantID       string
	Actor          ActorRef
	Action         string
	ActionTypeID   string
	Purpose        string
	Subjects       []ObjectRef
	Query          string
	PayloadPreview map[string]any
}

type CreateDecisionInput struct {
	TenantID         string
	Requester        ActorRef
	Action           string
	ActionTypeID     string
	Resource         ObjectRef
	ObjectRefs       []ObjectRef
	ArgsHash         string
	Purpose          string
	RiskLevel        RiskLevel
	PreferredMode    DecisionMode
	ExplicitDeny     bool
	HILRequired      bool
	ApprovalRequired bool
	CouncilRequired  bool
	SubjectRefs      []ObjectRef
	OntologyQuery    string
	PayloadPreview   map[string]any
	BusinessKey      string
	IdempotencyKey   string
	ExpiresAt        time.Time
}

type CreateDecisionResult struct {
	Decision DecisionRequest `json:"decision"`
	Council  *CouncilSession `json:"council,omitempty"`
}

type SubmitContributionInput struct {
	Position       string
	Reasoning      string
	EvidenceRefs   []string
	IdempotencyKey string
}

type CastVoteInput struct {
	Choice         VoteChoice
	Reason         string
	IdempotencyKey string
}

type Service struct {
	repository       Repository
	context          ContextProvider
	ids              IDGenerator
	now              func() time.Time
	observer         ResolutionObserver
	workflow         *WorkflowService
	delegations      DelegationAuthority
	notificationWake atomic.Pointer[func()]
}

// SetNotificationWake installs a nonblocking, notification-only wakeup. Durable
// pending records remain the source of truth when a wakeup is lost on restart.
func (s *Service) SetNotificationWake(wake func()) {
	s.notificationWake.Store(&wake)
	if s.workflow != nil {
		s.workflow.notificationWake.Store(&wake)
	}
}

func (s *Service) notifyPending() {
	if wake := s.notificationWake.Load(); wake != nil && *wake != nil {
		(*wake)()
	}
}

// ResolutionObserver is called after a pending HIL/approval decision and its
// receipt have been durably committed. Observers should use the receipt to
// resume or authorize the waiting runtime operation. The callback is
// deliberately notification-only because the database commit cannot be
// rolled back if an observer fails.
type ResolutionObserver interface {
	DecisionResolved(context.Context, DecisionResolution)
}

type ResolutionObserverFunc func(context.Context, DecisionResolution)

func (f ResolutionObserverFunc) DecisionResolved(ctx context.Context, resolution DecisionResolution) {
	if f != nil {
		f(ctx, resolution)
	}
}

type Option func(*Service)

func WithClock(now func() time.Time) Option {
	return func(service *Service) {
		if now != nil {
			service.now = now
		}
	}
}

func WithResolutionObserver(observer ResolutionObserver) Option {
	return func(service *Service) {
		service.observer = observer
	}
}

func WithWorkflowService(workflow *WorkflowService) Option {
	return func(service *Service) {
		service.workflow = workflow
	}
}

func NewService(repository Repository, contextProvider ContextProvider, ids IDGenerator, options ...Option) *Service {
	service := &Service{
		repository: repository,
		context:    contextProvider,
		ids:        ids,
		now:        func() time.Time { return time.Now().UTC() },
	}
	for _, option := range options {
		option(service)
	}
	if service.workflow != nil {
		service.workflow.setResolutionObserver(service.observer)
	}
	return service
}

func (s *Service) CreateDecision(ctx context.Context, input CreateDecisionInput) (CreateDecisionResult, error) {
	if s == nil || s.repository == nil || s.context == nil || s.ids == nil || !validCreateDecisionInput(input) {
		return CreateDecisionResult{}, ErrInvalidRequest
	}
	input.PayloadPreview = normalizeApprovalAssigneePayload(input.PayloadPreview)
	input.BusinessKey = strings.TrimSpace(input.BusinessKey)
	if len(input.BusinessKey) > 512 {
		return CreateDecisionResult{}, ErrInvalidRequest
	}
	if input.BusinessKey != "" {
		if input.PayloadPreview == nil {
			input.PayloadPreview = map[string]any{}
		}
		input.PayloadPreview[approvalBusinessKeyPayloadKey] = input.BusinessKey
	}
	requestFingerprint := stableFingerprint(createDecisionFingerprintInput(input))
	if input.BusinessKey != "" {
		existing, found, err := s.findDecisionByBusinessKey(ctx, input.TenantID, input.BusinessKey)
		if err != nil {
			return CreateDecisionResult{}, err
		}
		if found {
			if legacyRuntimeApprovalAssigneeReplayMatches(existing, input, true) {
				existing, err = s.reconcileLegacyRuntimeApprovalAssignee(ctx, existing, input, requestFingerprint)
				if err != nil {
					return CreateDecisionResult{}, err
				}
			}
			return s.resultForDecision(ctx, existing)
		}
	}
	existing, err := s.repository.GetDecisionByIdempotencyKey(ctx, strings.TrimSpace(input.TenantID), strings.TrimSpace(input.IdempotencyKey))
	if err == nil {
		if existing.RequestFingerprint != requestFingerprint {
			if !legacyRuntimeApprovalAssigneeReplayMatches(existing, input, false) {
				return CreateDecisionResult{}, ErrIdempotencyConflict
			}
			existing, err = s.reconcileLegacyRuntimeApprovalAssignee(ctx, existing, input, requestFingerprint)
			if err != nil {
				return CreateDecisionResult{}, err
			}
		}
		if !validDecisionApprovalAssignee(existing) && s.workflow == nil {
			return CreateDecisionResult{}, ErrInvalidRequest
		}
		return s.resultForDecision(ctx, existing)
	}
	if !errors.Is(err, ErrNotFound) {
		return CreateDecisionResult{}, err
	}
	snapshot, err := s.context.BuildDecisionContext(ctx, DecisionContextRequest{
		TenantID:       input.TenantID,
		Actor:          input.Requester,
		Action:         input.Action,
		ActionTypeID:   input.ActionTypeID,
		Purpose:        input.Purpose,
		Subjects:       append([]ObjectRef(nil), input.SubjectRefs...),
		Query:          input.OntologyQuery,
		PayloadPreview: cloneMap(input.PayloadPreview),
	})
	if err != nil {
		return CreateDecisionResult{}, fmt.Errorf("%w: %v", ErrContextUnavailable, err)
	}
	now := s.now().UTC()
	decisionID, err := s.ids.NewIDContext()
	if err != nil {
		return CreateDecisionResult{}, err
	}
	route := DecideRoute(RouteInput{
		SnapshotValid:    true,
		ExplicitDeny:     input.ExplicitDeny,
		ContextComplete:  snapshot.Complete,
		ContextConflict:  snapshot.Conflict,
		RiskLevel:        input.RiskLevel,
		CouncilRequired:  input.CouncilRequired,
		HILRequired:      input.HILRequired,
		ApprovalRequired: input.ApprovalRequired,
		PreferredMode:    input.PreferredMode,
	})
	if !validApprovalAssigneeForRoute(route, input.PayloadPreview) && s.workflow == nil {
		return CreateDecisionResult{}, ErrInvalidRequest
	}
	status := DecisionPending
	if route.Mode == ModeAuto {
		status = DecisionResolved
	} else if route.Mode == ModeDeny {
		status = DecisionDenied
	}
	decision := DecisionRequest{
		ID:             decisionID,
		TenantID:       strings.TrimSpace(input.TenantID),
		Requester:      normalizeActor(input.Requester),
		Action:         strings.TrimSpace(input.Action),
		ActionTypeID:   strings.TrimSpace(input.ActionTypeID),
		Resource:       normalizeObjectRef(input.Resource),
		ObjectRefs:     normalizeObjectRefs(input.ObjectRefs),
		ArgsHash:       strings.TrimSpace(input.ArgsHash),
		Purpose:        strings.TrimSpace(input.Purpose),
		RiskLevel:      normalizeRisk(input.RiskLevel),
		PreferredMode:  input.PreferredMode,
		Route:          route,
		Context:        snapshot,
		PayloadPreview: cloneMap(input.PayloadPreview),
		IdempotencyKey: strings.TrimSpace(input.IdempotencyKey),
		Status:         status,
		CreatedAt:      now,
		ExpiresAt:      input.ExpiresAt.UTC(),
	}
	decision.RequestFingerprint = requestFingerprint
	if s.workflow != nil {
		prepared, managed, err := s.workflow.PrepareDecisionWorkflow(ctx, decision)
		if err != nil {
			return CreateDecisionResult{}, err
		}
		if managed {
			workflowRepository, ok := s.repository.(WorkflowRepository)
			if !ok {
				return CreateDecisionResult{}, ErrInvalidRequest
			}
			if err := workflowRepository.CreateDecisionWorkflow(ctx, decision, prepared.Case, prepared.Instance, prepared.Stages, prepared.Tasks, prepared.Audit, prepared.Notifications); err != nil {
				if input.BusinessKey != "" && errors.Is(err, ErrConflict) {
					existing, getErr := s.repository.GetDecisionByIdempotencyKey(ctx, decision.TenantID, decision.IdempotencyKey)
					if getErr == nil && decisionPayloadString(existing, approvalBusinessKeyPayloadKey) == input.BusinessKey {
						return s.resultForDecision(ctx, existing)
					}
				}
				return CreateDecisionResult{}, err
			}
			persisted, err := s.repository.GetDecisionByIdempotencyKey(ctx, decision.TenantID, decision.IdempotencyKey)
			if err != nil {
				return CreateDecisionResult{}, err
			}
			return s.resultForDecision(ctx, persisted)
		}
	}
	if !validDecisionApprovalAssignee(decision) {
		return CreateDecisionResult{}, ErrInvalidRequest
	}
	var council *CouncilSession
	if route.Mode == ModeCouncil {
		participants, err := normalizeCouncilParticipants(snapshot.RecommendedParticipants)
		if err != nil {
			return CreateDecisionResult{}, err
		}
		councilID, err := s.ids.NewIDContext()
		if err != nil {
			return CreateDecisionResult{}, err
		}
		threshold := majorityThreshold(len(participants))
		council = &CouncilSession{
			ID:                councilID,
			TenantID:          decision.TenantID,
			RequestID:         decision.ID,
			ArgsHash:          decision.ArgsHash,
			RiskLevel:         decision.RiskLevel,
			Status:            CouncilOpen,
			Quorum:            threshold,
			ApprovalThreshold: threshold,
			Participants:      participants,
			CreatedAt:         now,
			UpdatedAt:         now,
			ExpiresAt:         decision.ExpiresAt,
		}
	}
	if err := s.repository.CreateDecisionWithCouncil(ctx, decision, council); err != nil {
		return CreateDecisionResult{}, err
	}
	persisted, err := s.repository.GetDecisionByIdempotencyKey(ctx, decision.TenantID, decision.IdempotencyKey)
	if err != nil {
		return CreateDecisionResult{}, err
	}
	if persisted.RequestFingerprint != requestFingerprint {
		return CreateDecisionResult{}, ErrIdempotencyConflict
	}
	return s.resultForDecision(ctx, persisted)
}

const approvalBusinessKeyPayloadKey = "approval_business_key"

func (s *Service) findDecisionByBusinessKey(ctx context.Context, tenantID, businessKey string) (DecisionRequest, bool, error) {
	tenantID = strings.TrimSpace(tenantID)
	businessKey = strings.TrimSpace(businessKey)
	if tenantID == "" || businessKey == "" {
		return DecisionRequest{}, false, nil
	}
	for offset := 0; ; {
		page, err := s.repository.ListDecisions(ctx, DecisionListQuery{TenantID: tenantID, Limit: 100, Offset: offset})
		if err != nil {
			return DecisionRequest{}, false, err
		}
		for _, decision := range page.Items {
			if decisionPayloadString(decision, approvalBusinessKeyPayloadKey) == businessKey {
				return decision, true, nil
			}
		}
		offset += len(page.Items)
		if len(page.Items) == 0 || offset >= page.Total {
			return DecisionRequest{}, false, nil
		}
	}
}

func (s *Service) resultForDecision(ctx context.Context, decision DecisionRequest) (CreateDecisionResult, error) {
	result := CreateDecisionResult{Decision: decision}
	if decision.Status == DecisionPending && !decision.ExpiresAt.IsZero() && !s.now().UTC().Before(decision.ExpiresAt) {
		if err := s.expirePendingDecision(ctx, decision, s.now().UTC()); err == nil {
			if updated, getErr := s.repository.GetDecision(ctx, decision.TenantID, decision.ID); getErr == nil {
				decision = updated
				result.Decision = updated
			}
		}
	}
	council, err := s.repository.GetCouncilByRequestID(ctx, decision.TenantID, decision.ID)
	if err == nil {
		result.Council = &council
		result.Decision.CouncilID = council.ID
	} else if decision.Route.Mode == ModeCouncil {
		return CreateDecisionResult{}, err
	}
	if result.Decision.Status == DecisionPending {
		s.notifyPending()
	}
	return result, nil
}

func (s *Service) ListDecisions(ctx context.Context, query DecisionListQuery) (DecisionPage, error) {
	if s == nil || s.repository == nil {
		return DecisionPage{}, ErrInvalidRequest
	}
	// Expiry is reconciled on reads as well as on mutation. This keeps a
	// decision from remaining visibly pending forever when no approver returns
	// after its deadline and gives the runtime/Todo observers a terminal event.
	for _, tenantID := range decisionListTenantIDs(query) {
		pending, pendingErr := s.repository.ListDecisions(ctx, DecisionListQuery{
			TenantID: tenantID, Status: DecisionPending, Limit: 100, Offset: 0,
		})
		if pendingErr != nil {
			return DecisionPage{}, pendingErr
		}
		now := s.now().UTC()
		for _, decision := range pending.Items {
			if decision.ExpiresAt.IsZero() || now.Before(decision.ExpiresAt) {
				continue
			}
			workflowManaged, workflowErr := s.workflowOwnsDecision(ctx, decision)
			if workflowErr != nil {
				return DecisionPage{}, workflowErr
			}
			if workflowManaged {
				continue
			}
			if decision.Route.Mode == ModeCouncil {
				if err := s.expireCouncil(ctx, decision, now); err != nil && !errors.Is(err, ErrConflict) {
					return DecisionPage{}, err
				}
				continue
			}
			if _, err := s.expireDecision(ctx, decision, now); err != nil && !errors.Is(err, ErrConflict) {
				return DecisionPage{}, err
			}
		}
	}
	page, err := s.repository.ListDecisions(ctx, query)
	if err != nil {
		return DecisionPage{}, err
	}
	for index := range page.Items {
		council, err := s.repository.GetCouncilByRequestID(ctx, page.Items[index].TenantID, page.Items[index].ID)
		if err == nil {
			page.Items[index].CouncilID = council.ID
			continue
		}
		if page.Items[index].Route.Mode == ModeCouncil {
			return DecisionPage{}, err
		}
	}
	return page, nil
}

func (s *Service) GetDecision(ctx context.Context, tenantID, decisionID string) (DecisionRequest, error) {
	if s == nil || s.repository == nil {
		return DecisionRequest{}, ErrInvalidRequest
	}
	decision, err := s.repository.GetDecision(ctx, tenantID, decisionID)
	if err != nil {
		return decision, err
	}
	if decision.Status == DecisionPending && !decision.ExpiresAt.IsZero() && !s.now().UTC().Before(decision.ExpiresAt) {
		if expireErr := s.expirePendingDecision(ctx, decision, s.now().UTC()); expireErr == nil {
			if updated, getErr := s.repository.GetDecision(ctx, tenantID, decisionID); getErr == nil {
				decision = updated
			}
		}
	}
	council, err := s.repository.GetCouncilByRequestID(ctx, tenantID, decisionID)
	if err != nil && decision.Route.Mode == ModeCouncil {
		return DecisionRequest{}, err
	}
	if err == nil {
		decision.CouncilID = council.ID
	}
	return decision, nil
}

func (s *Service) expirePendingDecision(ctx context.Context, decision DecisionRequest, now time.Time) error {
	workflowManaged, err := s.workflowOwnsDecision(ctx, decision)
	if err != nil {
		return err
	}
	if workflowManaged {
		return nil
	}
	if decision.Route.Mode == ModeCouncil {
		return s.expireCouncil(ctx, decision, now)
	}
	_, err = s.expireDecision(ctx, decision, now)
	return err
}

func (s *Service) workflowOwnsDecision(ctx context.Context, decision DecisionRequest) (bool, error) {
	if s == nil || s.workflow == nil || s.workflow.repository == nil {
		return false, nil
	}
	_, err := s.workflow.repository.GetWorkflowInstanceByDecisionID(ctx, decision.TenantID, decision.ID)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return false, err
}

// GetDecisionResolution exposes a committed terminal decision together with
// its receipt for durable business consumers that were unavailable when the
// post-commit observer first ran. It deliberately does not invent an actor or
// reason: those are audit details of the original resolver, while the receipt
// and decision are the authoritative replay contract.
func (s *Service) GetDecisionResolution(ctx context.Context, tenantID, decisionID string) (DecisionResolution, error) {
	decision, err := s.GetDecision(ctx, tenantID, decisionID)
	if err != nil {
		return DecisionResolution{}, err
	}
	if decision.Status == DecisionPending {
		return DecisionResolution{}, ErrConflict
	}
	receipts, err := s.repository.ListDecisionReceipts(ctx, decision.TenantID, decision.ID)
	if err != nil {
		return DecisionResolution{}, err
	}
	if len(receipts) == 0 {
		return DecisionResolution{}, ErrConflict
	}
	return DecisionResolution{
		Decision: decision,
		Receipt:  receipts[len(receipts)-1],
		Actor:    ActorRef{Kind: ActorService, ID: "hil:replay"},
		Reason:   "replayed committed HIL decision",
	}, nil
}

// ResolveDecision commits a human decision for a pending runtime HIL or fixed
// approval request. Council requests must be resolved through votes so their
// quorum and contribution invariants remain intact.
func (s *Service) ResolveDecision(ctx context.Context, tenantID, decisionID string, actor ActorRef, input ResolveDecisionInput) (DecisionResolution, error) {
	if s == nil || s.repository == nil || s.ids == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(decisionID) == "" || strings.TrimSpace(input.IdempotencyKey) == "" {
		return DecisionResolution{}, ErrInvalidRequest
	}
	status, effect, ok := normalizeResolution(input.Decision)
	actor = normalizeActor(actor)
	if !ok || actor.ID == "" || actor.Kind == "" {
		return DecisionResolution{}, ErrInvalidRequest
	}
	decision, err := s.repository.GetDecision(ctx, tenantID, decisionID)
	if err != nil {
		return DecisionResolution{}, err
	}
	if decision.Route.Mode != ModeHIL && decision.Route.Mode != ModeApproval {
		return DecisionResolution{}, ErrConflict
	}
	if s.workflow != nil && s.workflow.repository != nil {
		detail, workflowErr := s.workflow.repository.GetWorkflowInstanceByDecisionID(ctx, tenantID, decisionID)
		if workflowErr == nil {
			return s.resolveWorkflowDecision(ctx, detail, actor, input, status, effect)
		}
		if !errors.Is(workflowErr, ErrNotFound) {
			return DecisionResolution{}, workflowErr
		}
	}
	if !DecisionResolutionActorAllowed(decision, actor) {
		return DecisionResolution{}, ErrParticipantRequired
	}
	if decision.Status != DecisionPending {
		if decision.Status != status {
			return DecisionResolution{}, ErrConflict
		}
		return s.resolutionForExisting(ctx, decision, actor, input.Reason)
	}
	now := s.now().UTC()
	if !decision.ExpiresAt.IsZero() && !now.Before(decision.ExpiresAt) {
		workflowManaged, workflowErr := s.workflowOwnsDecision(ctx, decision)
		if workflowErr != nil {
			return DecisionResolution{}, workflowErr
		}
		if workflowManaged {
			return DecisionResolution{}, ErrConflict
		}
		_, _ = s.expireDecision(ctx, decision, now)
		return DecisionResolution{}, ErrConflict
	}
	receiptID, err := s.ids.NewIDContext()
	if err != nil {
		return DecisionResolution{}, err
	}
	receipt := DecisionReceipt{
		ID: receiptID, TenantID: decision.TenantID, RequestID: decision.ID,
		ArgsHash: decision.ArgsHash, Effect: effect, SingleUse: status == DecisionResolved,
		CreatedAt: now,
	}
	if receipt.SingleUse {
		receipt.ExpiresAt = now.Add(15 * time.Minute)
	}
	decision.Status = status
	if err := s.repository.ResolveDecision(ctx, decision, receipt); err != nil {
		if errors.Is(err, ErrConflict) {
			// A concurrent resolver may have committed the same logical answer.
			latest, getErr := s.repository.GetDecision(ctx, tenantID, decisionID)
			if getErr == nil && latest.Status == status {
				return s.resolutionForExisting(ctx, latest, actor, input.Reason)
			}
		}
		return DecisionResolution{}, err
	}
	persisted, err := s.repository.GetDecision(ctx, tenantID, decisionID)
	if err != nil {
		return DecisionResolution{}, err
	}
	resolution := DecisionResolution{Decision: persisted, Receipt: receipt, Actor: normalizeActor(actor), Reason: strings.TrimSpace(input.Reason)}
	if s.observer != nil {
		s.observer.DecisionResolved(ctx, resolution)
	}
	return resolution, nil
}

func (s *Service) resolveWorkflowDecision(ctx context.Context, detail WorkflowInstanceDetail, actor ActorRef, input ResolveDecisionInput, status DecisionStatus, effect DecisionEffect) (DecisionResolution, error) {
	if actor.Kind != ActorHuman || actor.ID == "" {
		return DecisionResolution{}, ErrParticipantRequired
	}
	if detail.Instance.Status != WorkflowRunning {
		allowed := false
		for _, task := range detail.Tasks {
			if actorKey(task.CurrentAssignee) == actorKey(actor) {
				allowed = true
				break
			}
		}
		if !allowed {
			return DecisionResolution{}, ErrParticipantRequired
		}
		if detail.Decision.Status != status {
			return DecisionResolution{}, ErrConflict
		}
		return workflowDecisionResolution(detail, actor, input.Reason, effect)
	}
	if detail.Decision.Status != DecisionPending && detail.Decision.Status != status {
		return DecisionResolution{}, ErrConflict
	}
	taskDecision := WorkflowTaskApprove
	if status == DecisionDenied {
		if strings.TrimSpace(input.Reason) == "" {
			return DecisionResolution{}, ErrInvalidRequest
		}
		taskDecision = WorkflowTaskReject
	}

	assignedTasks := 0
	candidates := make([]WorkflowTask, 0, 1)
	for _, task := range detail.Tasks {
		if task.Status != TaskPending || actorKey(task.CurrentAssignee) != actorKey(actor) {
			continue
		}
		assignedTasks++
		stageIndex := workflowStageIndex(detail.Stages, task.StageInstanceID)
		if stageIndex < 0 {
			continue
		}
		stage := detail.Stages[stageIndex]
		if stage.Status != WorkflowStageActive || stage.Definition.Type != WorkflowStageApproval || stage.Definition.Approval == nil {
			continue
		}
		changed := task
		if taskDecision == WorkflowTaskApprove {
			changed.Status = TaskApproved
		} else {
			changed.Status = TaskRejected
		}
		outcome := ResolveApprovalStage(stage.Definition.Approval.Strategy, stage.Definition.Approval.Threshold, workflowStageTaskStatuses(detail.Tasks, stage.ID, changed))
		terminal := status == DecisionDenied && outcome == StageFailed
		if status == DecisionResolved && outcome == StagePassed {
			terminal = nextWorkflowStageIndex(detail.Stages, stage.Definition.Order) < 0
		}
		if terminal {
			candidates = append(candidates, task)
		}
	}
	if len(candidates) != 1 {
		if assignedTasks == 0 {
			return DecisionResolution{}, ErrParticipantRequired
		}
		return DecisionResolution{}, ErrConflict
	}

	updated, err := s.workflow.DecideTask(ctx, detail.Instance.TenantID, candidates[0].ID, actor, DecideWorkflowTaskInput{
		Decision: taskDecision, Reason: input.Reason, IdempotencyKey: input.IdempotencyKey,
		ExpectedInstanceVersion: detail.Instance.Version,
	})
	if err != nil {
		return DecisionResolution{}, err
	}
	if updated.Decision.Status != status || updated.Instance.Status == WorkflowRunning {
		return DecisionResolution{}, ErrConflict
	}
	return workflowDecisionResolution(updated, actor, input.Reason, effect)
}

func workflowDecisionResolution(detail WorkflowInstanceDetail, actor ActorRef, reason string, effect DecisionEffect) (DecisionResolution, error) {
	for index := len(detail.Receipts) - 1; index >= 0; index-- {
		receipt := detail.Receipts[index]
		if receipt.RequestID != detail.Decision.ID || receipt.Effect != effect {
			continue
		}
		return DecisionResolution{
			Decision: detail.Decision,
			Receipt:  receipt,
			Actor:    normalizeActor(actor),
			Reason:   strings.TrimSpace(reason),
		}, nil
	}
	return DecisionResolution{}, ErrConflict
}

// SupersedeDecision is an internal lifecycle operation for a durable producer
// that has invalidated its pending work (for example, an autonomous-plan owner
// reassignment). It has no HTTP route. A pending decision becomes a terminal
// denial with a receipt so it leaves actionable inboxes and cannot be resolved
// later; an already-terminal decision is returned unchanged for idempotency.
func (s *Service) SupersedeDecision(ctx context.Context, tenantID, decisionID, reason string) (DecisionResolution, error) {
	if s == nil || s.repository == nil || s.ids == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(decisionID) == "" {
		return DecisionResolution{}, ErrInvalidRequest
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "decision superseded"
	}
	decision, err := s.repository.GetDecision(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(decisionID))
	if err != nil {
		return DecisionResolution{}, err
	}
	if decision.Route.Mode != ModeHIL && decision.Route.Mode != ModeApproval {
		return DecisionResolution{}, ErrConflict
	}
	actor := ActorRef{Kind: ActorService, ID: "hil:supersede"}
	if decision.Status != DecisionPending {
		if decision.Status != DecisionResolved && decision.Status != DecisionDenied {
			return DecisionResolution{}, ErrConflict
		}
		return s.resolutionForExisting(ctx, decision, actor, reason)
	}
	now := s.now().UTC()
	receiptID, err := s.ids.NewIDContext()
	if err != nil {
		return DecisionResolution{}, err
	}
	decision.Status = DecisionDenied
	receipt := DecisionReceipt{
		ID: receiptID, TenantID: decision.TenantID, RequestID: decision.ID,
		ArgsHash: decision.ArgsHash, Effect: EffectDeny, CreatedAt: now,
	}
	if err := s.repository.ResolveDecision(ctx, decision, receipt); err != nil {
		if errors.Is(err, ErrConflict) {
			latest, getErr := s.repository.GetDecision(ctx, tenantID, decisionID)
			if getErr == nil && latest.Status != DecisionPending {
				return s.resolutionForExisting(ctx, latest, actor, reason)
			}
		}
		return DecisionResolution{}, err
	}
	persisted, err := s.repository.GetDecision(ctx, decision.TenantID, decision.ID)
	if err != nil {
		return DecisionResolution{}, err
	}
	resolution := DecisionResolution{
		Decision: persisted, Receipt: receipt, Actor: actor,
		Reason: reason,
	}
	if s.observer != nil {
		s.observer.DecisionResolved(ctx, resolution)
	}
	return resolution, nil
}

const approvalAssigneePayloadKey = "approval_assigned_to"

// DecisionResolutionActorAllowed enforces the fixed-approval assignee and a
// human-only resolution boundary. It is deliberately owned by the domain
// service so HTTP and future transports cannot resolve a decision through a
// bypass.
func DecisionResolutionActorAllowed(decision DecisionRequest, actor ActorRef) bool {
	actor = normalizeActor(actor)
	assigned, assignedOK := decisionApprovalAssignee(decision)
	if decisionRequiresFixedApprovalAssignee(decision) {
		return assignedOK && actor.Kind == ActorHuman && actor.ID == assigned
	}
	if decisionHasApprovalAssignee(decision.PayloadPreview) {
		return assignedOK && actor.Kind == ActorHuman && actor.ID == assigned
	}
	return actor.Kind == ActorHuman && !strings.EqualFold(decisionPayloadString(decision, "source"), "todo_automation")
}

func decisionApprovalAssignee(decision DecisionRequest) (string, bool) {
	return canonicalApprovalAssignee(decision.PayloadPreview)
}

func decisionHasApprovalAssignee(payload map[string]any) bool {
	if payload == nil {
		return false
	}
	_, ok := payload[approvalAssigneePayloadKey]
	return ok
}

func canonicalApprovalAssignee(payload map[string]any) (string, bool) {
	if !decisionHasApprovalAssignee(payload) {
		return "", false
	}
	value, ok := payload[approvalAssigneePayloadKey]
	if !ok {
		return "", false
	}
	assignee, ok := value.(string)
	assignee = strings.TrimSpace(assignee)
	return assignee, ok && assignee != ""
}

func normalizeApprovalAssigneePayload(payload map[string]any) map[string]any {
	normalized := cloneMap(payload)
	if normalized == nil {
		return nil
	}
	value, ok := normalized[approvalAssigneePayloadKey]
	if !ok {
		return normalized
	}
	assignee, ok := value.(string)
	if !ok {
		return normalized
	}
	normalized[approvalAssigneePayloadKey] = strings.TrimSpace(assignee)
	return normalized
}

func routeRequiresFixedApprovalAssignee(route RouteDecision) bool {
	return route.Mode == ModeApproval || (route.Mode == ModeCouncil && route.CouncilEffect == EffectRequiresApproval)
}

func decisionRequiresFixedApprovalAssignee(decision DecisionRequest) bool {
	if routeRequiresFixedApprovalAssignee(decision.Route) {
		return true
	}
	return decision.Route.Mode == ModeCouncil && (normalizeRisk(decision.RiskLevel) == RiskHigh || normalizeRisk(decision.RiskLevel) == RiskCritical)
}

func validApprovalAssigneeForRoute(route RouteDecision, payload map[string]any) bool {
	_, valid := canonicalApprovalAssignee(payload)
	if routeRequiresFixedApprovalAssignee(route) {
		return valid
	}
	return !decisionHasApprovalAssignee(payload) || valid
}

func validDecisionApprovalAssignee(decision DecisionRequest) bool {
	_, valid := canonicalApprovalAssignee(decision.PayloadPreview)
	if decisionRequiresFixedApprovalAssignee(decision) {
		return valid
	}
	return !decisionHasApprovalAssignee(decision.PayloadPreview) || valid
}

func decisionPayloadString(decision DecisionRequest, key string) string {
	if decision.PayloadPreview == nil {
		return ""
	}
	value, ok := decision.PayloadPreview[key]
	if !ok {
		return ""
	}
	if typed, ok := value.(string); ok {
		return strings.TrimSpace(typed)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

// ConsumeGatewayDecision validates and atomically consumes the latest
// successful receipt for a gateway retry. It intentionally looks up the
// decision by the original idempotency key so a client cannot substitute an
// arbitrary decision ID.
func (s *Service) ConsumeGatewayDecision(ctx context.Context, input GatewayReceiptConsumeInput) (DecisionReceipt, error) {
	if s == nil || s.repository == nil || strings.TrimSpace(input.TenantID) == "" || strings.TrimSpace(input.IdempotencyKey) == "" || strings.TrimSpace(input.Actor.ID) == "" || strings.TrimSpace(input.Resource.Kind) == "" || strings.TrimSpace(input.Resource.ID) == "" || strings.TrimSpace(input.ArgsHash) == "" {
		return DecisionReceipt{}, ErrInvalidRequest
	}
	decision, err := s.repository.GetDecisionByIdempotencyKey(ctx, input.TenantID, input.IdempotencyKey)
	if err != nil {
		return DecisionReceipt{}, err
	}
	if decision.Route.Mode != ModeHIL && decision.Route.Mode != ModeApproval && decision.Route.Mode != ModeCouncil {
		return DecisionReceipt{}, ErrNotFound
	}
	if !strings.EqualFold(strings.TrimSpace(decision.Requester.ID), strings.TrimSpace(input.Actor.ID)) || decision.Requester.Kind != input.Actor.Kind {
		return DecisionReceipt{}, ErrConflict
	}
	if decision.Resource != normalizeObjectRef(input.Resource) || strings.TrimSpace(decision.ArgsHash) != strings.TrimSpace(input.ArgsHash) {
		return DecisionReceipt{}, ErrConflict
	}
	if decision.Status == DecisionPending {
		return DecisionReceipt{}, ErrNotFound
	}
	if decision.Status == DecisionDenied {
		return DecisionReceipt{}, ErrDecisionDenied
	}
	receipts, err := s.repository.ListDecisionReceipts(ctx, input.TenantID, decision.ID)
	if err != nil {
		return DecisionReceipt{}, err
	}
	var receipt DecisionReceipt
	for index := len(receipts) - 1; index >= 0; index-- {
		if receipts[index].Effect == EffectAuthorize {
			receipt = receipts[index]
			break
		}
	}
	if receipt.ID == "" {
		return DecisionReceipt{}, ErrNotFound
	}
	now := s.now().UTC()
	if !receipt.ExpiresAt.IsZero() && !now.Before(receipt.ExpiresAt) {
		return DecisionReceipt{}, ErrConflict
	}
	if !receipt.SingleUse || !receipt.UsedAt.IsZero() {
		return DecisionReceipt{}, ErrConflict
	}
	consumer, ok := s.repository.(DecisionReceiptConsumer)
	if !ok {
		return DecisionReceipt{}, ErrInvalidRequest
	}
	if err := consumer.ConsumeDecisionReceipt(ctx, input.TenantID, decision.ID, receipt.ID, now); err != nil {
		return DecisionReceipt{}, err
	}
	receipt.UsedAt = now
	return receipt, nil
}

// ConsumeActionDecision validates the complete immutable action material and
// atomically consumes the named authorization receipt. Unlike gateway retries,
// governed actions may target multiple ontology objects, so every target is
// included in the binding.
func (s *Service) ConsumeActionDecision(ctx context.Context, input ActionReceiptConsumeInput) (DecisionReceipt, error) {
	if s == nil || s.repository == nil || strings.TrimSpace(input.TenantID) == "" ||
		strings.TrimSpace(input.DecisionID) == "" || strings.TrimSpace(input.ReceiptID) == "" ||
		strings.TrimSpace(input.Actor.ID) == "" || strings.TrimSpace(input.Action) == "" ||
		strings.TrimSpace(input.ActionTypeID) == "" || strings.TrimSpace(input.ArgsHash) == "" || len(input.ObjectRefs) == 0 {
		return DecisionReceipt{}, ErrInvalidRequest
	}
	boundRefs, valid := canonicalActionObjectRefs(input.ObjectRefs)
	if !valid {
		return DecisionReceipt{}, ErrInvalidRequest
	}
	decision, err := s.repository.GetDecision(ctx, strings.TrimSpace(input.TenantID), strings.TrimSpace(input.DecisionID))
	if err != nil {
		return DecisionReceipt{}, err
	}
	if decision.Route.Mode != ModeHIL && decision.Route.Mode != ModeApproval && decision.Route.Mode != ModeCouncil {
		return DecisionReceipt{}, ErrNotFound
	}
	if decision.Requester != normalizeActor(input.Actor) ||
		strings.TrimSpace(decision.Action) != strings.TrimSpace(input.Action) ||
		strings.TrimSpace(decision.ActionTypeID) != strings.TrimSpace(input.ActionTypeID) ||
		strings.TrimSpace(decision.ArgsHash) != strings.TrimSpace(input.ArgsHash) ||
		strings.TrimSpace(decision.Purpose) != strings.TrimSpace(input.Purpose) ||
		normalizeRisk(decision.RiskLevel) != normalizeRisk(input.RiskLevel) {
		return DecisionReceipt{}, ErrConflict
	}
	decisionRefs, valid := canonicalActionObjectRefs(decision.ObjectRefs)
	if !valid || !sameActionObjectRefs(decisionRefs, boundRefs) || !actionObjectRefsContain(boundRefs, decision.Resource) {
		return DecisionReceipt{}, ErrConflict
	}
	if decision.Status == DecisionPending {
		return DecisionReceipt{}, ErrNotFound
	}
	if decision.Status == DecisionDenied {
		return DecisionReceipt{}, ErrDecisionDenied
	}
	now := s.now().UTC()
	if decision.ExpiresAt.IsZero() || !now.Before(decision.ExpiresAt) {
		return DecisionReceipt{}, ErrConflict
	}
	receipts, err := s.repository.ListDecisionReceipts(ctx, decision.TenantID, decision.ID)
	if err != nil {
		return DecisionReceipt{}, err
	}
	var receipt DecisionReceipt
	for index := len(receipts) - 1; index >= 0; index-- {
		if receipts[index].ID == strings.TrimSpace(input.ReceiptID) {
			receipt = receipts[index]
			break
		}
	}
	if receipt.ID == "" {
		return DecisionReceipt{}, ErrNotFound
	}
	if receipt.Effect != EffectAuthorize || strings.TrimSpace(receipt.ArgsHash) != strings.TrimSpace(input.ArgsHash) ||
		!receipt.SingleUse || !receipt.UsedAt.IsZero() || receipt.ExpiresAt.IsZero() || !now.Before(receipt.ExpiresAt) {
		return DecisionReceipt{}, ErrConflict
	}
	consumer, ok := s.repository.(DecisionReceiptConsumer)
	if !ok {
		return DecisionReceipt{}, ErrInvalidRequest
	}
	if err := consumer.ConsumeDecisionReceipt(ctx, decision.TenantID, decision.ID, receipt.ID, now); err != nil {
		return DecisionReceipt{}, err
	}
	receipt.UsedAt = now
	return receipt, nil
}

// GetActionDecisionReceipt returns the current unused authorization material
// only to the actor that originally requested the governed action. Consumption
// remains atomic in ConsumeActionDecision.
func (s *Service) GetActionDecisionReceipt(ctx context.Context, tenantID, decisionID string, actor ActorRef) (DecisionReceipt, error) {
	if s == nil || s.repository == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(decisionID) == "" {
		return DecisionReceipt{}, ErrInvalidRequest
	}
	actor = normalizeActor(actor)
	if actor.Kind == "" || actor.ID == "" {
		return DecisionReceipt{}, ErrInvalidRequest
	}
	decision, err := s.repository.GetDecision(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(decisionID))
	if err != nil {
		return DecisionReceipt{}, err
	}
	if decision.Requester != actor {
		return DecisionReceipt{}, ErrParticipantRequired
	}
	if decision.Status == DecisionPending {
		return DecisionReceipt{}, ErrNotFound
	}
	if decision.Status == DecisionDenied {
		return DecisionReceipt{}, ErrDecisionDenied
	}
	now := s.now().UTC()
	if decision.ExpiresAt.IsZero() || !now.Before(decision.ExpiresAt) {
		return DecisionReceipt{}, ErrConflict
	}
	receipts, err := s.repository.ListDecisionReceipts(ctx, decision.TenantID, decision.ID)
	if err != nil {
		return DecisionReceipt{}, err
	}
	for index := len(receipts) - 1; index >= 0; index-- {
		receipt := receipts[index]
		if receipt.Effect == EffectAuthorize && receipt.SingleUse && receipt.UsedAt.IsZero() &&
			!receipt.ExpiresAt.IsZero() && now.Before(receipt.ExpiresAt) && receipt.ArgsHash == decision.ArgsHash {
			return receipt, nil
		}
	}
	return DecisionReceipt{}, ErrNotFound
}

func (s *Service) expireDecision(ctx context.Context, decision DecisionRequest, now time.Time) (DecisionResolution, error) {
	if decision.Status != DecisionPending || (decision.Route.Mode != ModeHIL && decision.Route.Mode != ModeApproval) {
		return DecisionResolution{}, ErrConflict
	}
	receiptID, err := s.ids.NewIDContext()
	if err != nil {
		return DecisionResolution{}, err
	}
	decision.Status = DecisionDenied
	receipt := DecisionReceipt{ID: receiptID, TenantID: decision.TenantID, RequestID: decision.ID, ArgsHash: decision.ArgsHash, Effect: EffectDeny, CreatedAt: now}
	if err := s.repository.ResolveDecision(ctx, decision, receipt); err != nil {
		return DecisionResolution{}, err
	}
	persisted, err := s.repository.GetDecision(ctx, decision.TenantID, decision.ID)
	if err != nil {
		return DecisionResolution{}, err
	}
	resolution := DecisionResolution{Decision: persisted, Receipt: receipt, Actor: ActorRef{Kind: ActorService, ID: "hil:expiry"}, Reason: "decision expired"}
	if s.observer != nil {
		s.observer.DecisionResolved(ctx, resolution)
	}
	return resolution, nil
}

func (s *Service) expireCouncil(ctx context.Context, decision DecisionRequest, now time.Time) error {
	if decision.Route.Mode != ModeCouncil || decision.Status != DecisionPending {
		return ErrConflict
	}
	session, err := s.repository.GetCouncilByRequestID(ctx, decision.TenantID, decision.ID)
	if err != nil {
		return err
	}
	if session.Status != CouncilOpen {
		return ErrConflict
	}
	session.Status = CouncilExpired
	session.Effect = EffectDeny
	session.ResolvedAt = now
	session.UpdatedAt = now
	receiptID, err := s.ids.NewIDContext()
	if err != nil {
		return err
	}
	receipt := DecisionReceipt{
		ID: receiptID, TenantID: decision.TenantID, RequestID: decision.ID,
		CouncilID: session.ID, ArgsHash: decision.ArgsHash, Effect: EffectDeny, CreatedAt: now,
	}
	if err := s.repository.ResolveCouncil(ctx, session, receipt); err != nil {
		return err
	}
	persisted, err := s.repository.GetDecision(ctx, decision.TenantID, decision.ID)
	if err != nil {
		return err
	}
	if s.observer != nil {
		s.observer.DecisionResolved(ctx, DecisionResolution{
			Decision: persisted, Receipt: receipt,
			Actor: ActorRef{Kind: ActorService, ID: "hil:expiry"}, Reason: "decision expired",
		})
	}
	return nil
}

// DecideDecision is kept as a discoverable alias for integrations that use
// "decide" terminology.
func (s *Service) DecideDecision(ctx context.Context, tenantID, decisionID string, actor ActorRef, input ResolveDecisionInput) (DecisionResolution, error) {
	return s.ResolveDecision(ctx, tenantID, decisionID, actor, input)
}

func (s *Service) resolutionForExisting(ctx context.Context, decision DecisionRequest, actor ActorRef, reason string) (DecisionResolution, error) {
	receipts, err := s.repository.ListDecisionReceipts(ctx, decision.TenantID, decision.ID)
	if err != nil {
		return DecisionResolution{}, err
	}
	if len(receipts) == 0 {
		return DecisionResolution{}, ErrConflict
	}
	receipt := receipts[len(receipts)-1]
	return DecisionResolution{Decision: decision, Receipt: receipt, Actor: normalizeActor(actor), Reason: strings.TrimSpace(reason)}, nil
}

func normalizeResolution(value string) (DecisionStatus, DecisionEffect, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "approve", "approved", "allow", "authorize", "authorized", "resolve", "resolved":
		return DecisionResolved, EffectAuthorize, true
	case "reject", "rejected", "deny", "denied":
		return DecisionDenied, EffectDeny, true
	default:
		return "", "", false
	}
}

func (s *Service) GetCouncil(ctx context.Context, tenantID, councilID string) (CouncilSession, error) {
	if s == nil || s.repository == nil {
		return CouncilSession{}, ErrInvalidRequest
	}
	return s.repository.GetCouncil(ctx, tenantID, councilID)
}

func (s *Service) SubmitContribution(ctx context.Context, tenantID, councilID string, actor ActorRef, input SubmitContributionInput) error {
	if s == nil || s.repository == nil || strings.TrimSpace(input.IdempotencyKey) == "" || strings.TrimSpace(input.Position) == "" || strings.TrimSpace(input.Reasoning) == "" {
		return ErrInvalidRequest
	}
	if s.workflow != nil {
		if _, err := s.workflow.repository.GetWorkflowInstanceByCouncilID(ctx, tenantID, councilID); err == nil {
			_, err = s.workflow.SubmitCouncilContribution(ctx, tenantID, councilID, actor, input)
			return err
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	session, err := s.repository.GetCouncil(ctx, tenantID, councilID)
	if err != nil {
		return err
	}
	actor = normalizeActor(actor)
	if !councilHasParticipant(session, actor) {
		return ErrParticipantRequired
	}
	now := s.now().UTC()
	contribution := CouncilContribution{
		Actor:        actor,
		Position:     strings.TrimSpace(input.Position),
		Reasoning:    strings.TrimSpace(input.Reasoning),
		EvidenceRefs: normalizeStrings(input.EvidenceRefs),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	fingerprint := stableFingerprint(struct {
		CouncilID   string
		Actor       ActorRef
		Position    string
		Reasoning   string
		EvidenceRef []string
	}{CouncilID: councilID, Actor: contribution.Actor, Position: contribution.Position, Reasoning: contribution.Reasoning, EvidenceRef: contribution.EvidenceRefs})
	return s.repository.SaveContribution(ctx, tenantID, councilID, contribution, strings.TrimSpace(input.IdempotencyKey), fingerprint)
}

func (s *Service) CastVote(ctx context.Context, tenantID, councilID string, actor ActorRef, input CastVoteInput) (CouncilSession, error) {
	if s == nil || s.repository == nil || s.ids == nil || strings.TrimSpace(input.IdempotencyKey) == "" || !validVoteChoice(input.Choice) {
		return CouncilSession{}, ErrInvalidRequest
	}
	if s.workflow != nil {
		if _, err := s.workflow.repository.GetWorkflowInstanceByCouncilID(ctx, tenantID, councilID); err == nil {
			if _, err = s.workflow.CastCouncilVote(ctx, tenantID, councilID, actor, input); err != nil {
				return CouncilSession{}, err
			}
			return s.repository.GetCouncil(ctx, tenantID, councilID)
		} else if !errors.Is(err, ErrNotFound) {
			return CouncilSession{}, err
		}
	}
	session, err := s.repository.GetCouncil(ctx, tenantID, councilID)
	if err != nil {
		return CouncilSession{}, err
	}
	actor = normalizeActor(actor)
	if !councilHasParticipant(session, actor) {
		return CouncilSession{}, ErrParticipantRequired
	}
	if !councilHasContribution(session, actor) {
		return CouncilSession{}, ErrInvalidRequest
	}
	vote := CouncilVote{Actor: actor, Choice: input.Choice, Reason: strings.TrimSpace(input.Reason), CreatedAt: s.now().UTC()}
	fingerprint := stableFingerprint(struct {
		CouncilID string
		Actor     ActorRef
		Choice    VoteChoice
		Reason    string
	}{CouncilID: councilID, Actor: vote.Actor, Choice: vote.Choice, Reason: vote.Reason})
	if err := s.repository.SaveVote(ctx, tenantID, councilID, vote, strings.TrimSpace(input.IdempotencyKey), fingerprint); err != nil {
		return CouncilSession{}, err
	}
	session, err = s.repository.GetCouncil(ctx, tenantID, councilID)
	if err != nil {
		return CouncilSession{}, err
	}
	if session.Status != CouncilOpen {
		return session, nil
	}
	resolution, err := ResolveCouncil(session, s.now().UTC())
	if err != nil {
		return CouncilSession{}, err
	}
	if resolution.Status == CouncilOpen {
		return session, nil
	}
	receiptID, err := s.ids.NewIDContext()
	if err != nil {
		return CouncilSession{}, err
	}
	session.Status = resolution.Status
	session.Effect = resolution.Effect
	session.Dissent = append([]ActorRef(nil), resolution.Dissent...)
	session.ResolvedAt = resolution.ResolvedAt
	session.UpdatedAt = resolution.ResolvedAt
	receipt := DecisionReceipt{
		ID:        receiptID,
		TenantID:  tenantID,
		RequestID: session.RequestID,
		CouncilID: session.ID,
		ArgsHash:  session.ArgsHash,
		Effect:    session.Effect,
		SingleUse: session.Effect == EffectAuthorize,
		CreatedAt: session.ResolvedAt,
	}
	if receipt.SingleUse {
		receipt.ExpiresAt = receipt.CreatedAt.Add(15 * time.Minute)
	}
	if err := s.repository.ResolveCouncil(ctx, session, receipt); err != nil {
		return CouncilSession{}, err
	}
	// Medium-risk council authorization and all denials are terminal decisions
	// that waiting runtimes may consume. A high-risk passed council remains
	// pending after it is atomically handed to the fixed-approval stage.
	decision, err := s.repository.GetDecision(ctx, tenantID, session.RequestID)
	if err != nil {
		return CouncilSession{}, err
	}
	if decision.Status != DecisionPending && s.observer != nil {
		s.observer.DecisionResolved(ctx, DecisionResolution{
			Decision: decision,
			Receipt:  receipt,
			Actor:    actor,
			Reason:   strings.TrimSpace(input.Reason),
		})
	}
	return session, nil
}

func (s *Service) ListWorkflowTemplates(ctx context.Context, query WorkflowTemplateListQuery) ([]WorkflowTemplateSummary, error) {
	if s == nil || s.workflow == nil {
		return nil, ErrInvalidRequest
	}
	return s.workflow.ListTemplates(ctx, query)
}

func (s *Service) GetWorkflowTemplate(ctx context.Context, tenantID, templateID string) (WorkflowTemplate, error) {
	if s == nil || s.workflow == nil {
		return WorkflowTemplate{}, ErrInvalidRequest
	}
	return s.workflow.GetTemplate(ctx, tenantID, templateID)
}

func (s *Service) SaveWorkflowTemplateDraft(ctx context.Context, actor ActorRef, input SaveTemplateDraftInput) (WorkflowTemplate, error) {
	if s == nil || s.workflow == nil {
		return WorkflowTemplate{}, ErrInvalidRequest
	}
	return s.workflow.SaveTemplateDraft(ctx, actor, input)
}

func (s *Service) PublishWorkflowTemplate(ctx context.Context, actor ActorRef, tenantID, templateID string, bindings []WorkflowTemplateBinding, idempotencyKey string) (WorkflowTemplateVersion, error) {
	if s == nil || s.workflow == nil {
		return WorkflowTemplateVersion{}, ErrInvalidRequest
	}
	return s.workflow.PublishTemplateByID(ctx, actor, tenantID, templateID, bindings, idempotencyKey)
}

func (s *Service) DisableWorkflowTemplate(ctx context.Context, actor ActorRef, input DisableTemplateInput) error {
	if s == nil || s.workflow == nil {
		return ErrInvalidRequest
	}
	return s.workflow.DisableTemplate(ctx, actor, input)
}

func (s *Service) PutWorkflowTemplateBinding(ctx context.Context, actor ActorRef, input PutTemplateBindingInput) (WorkflowTemplateBinding, error) {
	if s == nil || s.workflow == nil {
		return WorkflowTemplateBinding{}, ErrInvalidRequest
	}
	return s.workflow.PutTemplateBinding(ctx, actor, input)
}

func (s *Service) ListWorkflowInstances(ctx context.Context, query WorkflowListQuery) (WorkflowInstancePage, error) {
	if s == nil || s.workflow == nil {
		return WorkflowInstancePage{}, ErrInvalidRequest
	}
	return s.workflow.repository.ListWorkflowInstances(ctx, query)
}

func (s *Service) GetWorkflowInstance(ctx context.Context, tenantID, instanceID string) (WorkflowInstanceDetail, error) {
	if s == nil || s.workflow == nil {
		return WorkflowInstanceDetail{}, ErrInvalidRequest
	}
	return s.workflow.repository.GetWorkflowInstance(ctx, tenantID, instanceID)
}

func (s *Service) RecordWorkflowViewed(ctx context.Context, tenantID, instanceID string, actor ActorRef, idempotencyKey string) error {
	if s == nil || s.workflow == nil {
		return ErrInvalidRequest
	}
	return s.workflow.RecordViewed(ctx, tenantID, instanceID, actor, idempotencyKey)
}

func (s *Service) DecideWorkflowTask(ctx context.Context, tenantID, taskID string, actor ActorRef, input DecideWorkflowTaskInput) (WorkflowInstanceDetail, error) {
	if s == nil || s.workflow == nil {
		return WorkflowInstanceDetail{}, ErrInvalidRequest
	}
	return s.workflow.DecideTask(ctx, tenantID, taskID, actor, input)
}

func (s *Service) TransferWorkflowTask(ctx context.Context, tenantID, taskID string, actor ActorRef, input TransferWorkflowTaskInput) (WorkflowInstanceDetail, error) {
	if s == nil || s.workflow == nil {
		return WorkflowInstanceDetail{}, ErrInvalidRequest
	}
	return s.workflow.TransferTask(ctx, tenantID, taskID, actor, input)
}

func (s *Service) ReassignWorkflowTask(ctx context.Context, tenantID, taskID string, actor ActorRef, input ReassignWorkflowTaskInput) (WorkflowInstanceDetail, error) {
	if s == nil || s.workflow == nil {
		return WorkflowInstanceDetail{}, ErrInvalidRequest
	}
	return s.workflow.ReassignTask(ctx, tenantID, taskID, actor, input)
}

func (s *Service) ResubmitWorkflow(ctx context.Context, tenantID, instanceID string, actor ActorRef, input ResubmitWorkflowInput) (WorkflowInstanceDetail, error) {
	if s == nil || s.workflow == nil {
		return WorkflowInstanceDetail{}, ErrInvalidRequest
	}
	return s.workflow.Resubmit(ctx, tenantID, instanceID, actor, input)
}

func validCreateDecisionInput(input CreateDecisionInput) bool {
	return strings.TrimSpace(input.TenantID) != "" && strings.TrimSpace(input.Requester.ID) != "" && input.Requester.Kind != "" &&
		strings.TrimSpace(input.Action) != "" && strings.TrimSpace(input.Resource.Kind) != "" && strings.TrimSpace(input.Resource.ID) != "" &&
		strings.TrimSpace(input.ArgsHash) != "" && strings.TrimSpace(input.IdempotencyKey) != "" && len(input.SubjectRefs) > 0
}

func normalizeCouncilParticipants(participants []CouncilParticipant) ([]CouncilParticipant, error) {
	byKey := make(map[string]CouncilParticipant, len(participants))
	hasRequiredHuman := false
	for _, participant := range participants {
		participant.Actor = normalizeActor(participant.Actor)
		key := actorKey(participant.Actor)
		if key == ":" {
			continue
		}
		if participant.Required && participant.Actor.Kind == ActorHuman {
			hasRequiredHuman = true
		}
		byKey[key] = participant
	}
	if len(byKey) < 2 || !hasRequiredHuman {
		return nil, ErrParticipantRequired
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]CouncilParticipant, 0, len(keys))
	for _, key := range keys {
		out = append(out, byKey[key])
	}
	return out, nil
}

func councilHasParticipant(session CouncilSession, actor ActorRef) bool {
	for _, participant := range session.Participants {
		if actorKey(participant.Actor) == actorKey(actor) {
			return true
		}
	}
	return false
}

func councilHasContribution(session CouncilSession, actor ActorRef) bool {
	for _, contribution := range session.Contributions {
		if actorKey(contribution.Actor) == actorKey(actor) {
			return true
		}
	}
	return false
}

func majorityThreshold(count int) int {
	return count/2 + 1
}

func normalizeActor(actor ActorRef) ActorRef {
	return ActorRef{Kind: ActorKind(strings.ToLower(strings.TrimSpace(string(actor.Kind)))), ID: strings.TrimSpace(actor.ID)}
}

func normalizeObjectRef(ref ObjectRef) ObjectRef {
	return ObjectRef{Kind: strings.ToLower(strings.TrimSpace(ref.Kind)), ID: strings.TrimSpace(ref.ID)}
}

func normalizeObjectRefs(refs []ObjectRef) []ObjectRef {
	out := make([]ObjectRef, 0, len(refs))
	for _, ref := range refs {
		ref = normalizeObjectRef(ref)
		if ref.Kind != "" && ref.ID != "" {
			out = append(out, ref)
		}
	}
	return out
}

func canonicalActionObjectRefs(refs []ObjectRef) ([]ObjectRef, bool) {
	result := normalizeObjectRefs(refs)
	if len(result) != len(refs) {
		return nil, false
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Kind == result[j].Kind {
			return result[i].ID < result[j].ID
		}
		return result[i].Kind < result[j].Kind
	})
	for index := 1; index < len(result); index++ {
		if result[index] == result[index-1] {
			return nil, false
		}
	}
	return result, len(result) > 0
}

func sameActionObjectRefs(left, right []ObjectRef) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func actionObjectRefsContain(refs []ObjectRef, target ObjectRef) bool {
	target = normalizeObjectRef(target)
	for _, ref := range refs {
		if ref == target {
			return true
		}
	}
	return false
}

func normalizeStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func createDecisionFingerprintInput(input CreateDecisionInput) any {
	if businessKey := strings.TrimSpace(input.BusinessKey); businessKey != "" {
		return struct {
			TenantID    string
			Action      string
			BusinessKey string
		}{
			TenantID: strings.TrimSpace(input.TenantID), Action: strings.TrimSpace(input.Action), BusinessKey: businessKey,
		}
	}
	payloadPreview := cloneMap(input.PayloadPreview)
	delete(payloadPreview, ApprovalDisplayPayloadKey)
	return struct {
		TenantID         string
		Requester        ActorRef
		Action           string
		ActionTypeID     string
		Resource         ObjectRef
		ObjectRefs       []ObjectRef
		ArgsHash         string
		Purpose          string
		RiskLevel        RiskLevel
		PreferredMode    DecisionMode
		ExplicitDeny     bool
		HILRequired      bool
		ApprovalRequired bool
		CouncilRequired  bool
		SubjectRefs      []ObjectRef
		OntologyQuery    string
		PayloadPreview   map[string]any
		ExpiresAt        time.Time
	}{
		TenantID: strings.TrimSpace(input.TenantID), Requester: normalizeActor(input.Requester), Action: strings.TrimSpace(input.Action),
		ActionTypeID: strings.TrimSpace(input.ActionTypeID), Resource: normalizeObjectRef(input.Resource), ObjectRefs: normalizeObjectRefs(input.ObjectRefs),
		ArgsHash: strings.TrimSpace(input.ArgsHash), Purpose: strings.TrimSpace(input.Purpose), RiskLevel: normalizeRisk(input.RiskLevel), PreferredMode: input.PreferredMode,
		ExplicitDeny: input.ExplicitDeny, HILRequired: input.HILRequired, ApprovalRequired: input.ApprovalRequired,
		CouncilRequired: input.CouncilRequired, SubjectRefs: normalizeObjectRefs(input.SubjectRefs), OntologyQuery: strings.TrimSpace(input.OntologyQuery),
		PayloadPreview: payloadPreview, ExpiresAt: input.ExpiresAt.UTC(),
	}
}

// Runtime approval events created before assignee persistence can be replayed
// with the newly derived assignee after a restart. Idempotency-key replays must
// otherwise match exactly; business-key replays retain their established
// cross-attempt identity semantics.
func legacyRuntimeApprovalAssigneeReplayMatches(existing DecisionRequest, input CreateDecisionInput, businessKeyMatch bool) bool {
	if strings.TrimSpace(existing.Action) != "runtime.approval" ||
		normalizeObjectRef(existing.Resource).Kind != "runtime_approval" ||
		normalizeObjectRef(input.Resource).Kind != "runtime_approval" ||
		decisionHasApprovalAssignee(existing.PayloadPreview) {
		return false
	}
	inputAction := strings.TrimSpace(input.Action)
	if inputAction == "" || (inputAction != "runtime.approval" && inputAction != strings.TrimSpace(input.ActionTypeID)) {
		return false
	}
	if _, valid := canonicalApprovalAssignee(input.PayloadPreview); !valid {
		return false
	}
	if businessKeyMatch {
		businessKey := strings.TrimSpace(input.BusinessKey)
		return businessKey != "" && decisionPayloadString(existing, approvalBusinessKeyPayloadKey) == businessKey
	}
	legacyInput := input
	legacyInput.PayloadPreview = cloneMap(input.PayloadPreview)
	delete(legacyInput.PayloadPreview, approvalAssigneePayloadKey)
	if inputAction != "runtime.approval" {
		legacyInput.Action = existing.Action
		legacyInput.ActionTypeID = existing.ActionTypeID
		legacyInput.RiskLevel = existing.RiskLevel
		materialHash, _ := legacyInput.PayloadPreview["material_hash"].(string)
		if _, existed := existing.PayloadPreview["material_hash"]; !existed && strings.TrimSpace(materialHash) == strings.TrimSpace(input.ArgsHash) {
			delete(legacyInput.PayloadPreview, "material_hash")
		}
	}
	return existing.RequestFingerprint == stableFingerprint(createDecisionFingerprintInput(legacyInput))
}

func (s *Service) reconcileLegacyRuntimeApprovalAssignee(ctx context.Context, existing DecisionRequest, input CreateDecisionInput, requestFingerprint string) (DecisionRequest, error) {
	repository, ok := s.repository.(legacyRuntimeApprovalAssigneeRepairer)
	if !ok {
		return DecisionRequest{}, ErrIdempotencyConflict
	}
	assignee, valid := canonicalApprovalAssignee(input.PayloadPreview)
	if !valid {
		return DecisionRequest{}, ErrIdempotencyConflict
	}
	repaired, err := repository.repairLegacyRuntimeApprovalAssignee(ctx, legacyRuntimeApprovalAssigneeRepairInput{
		TenantID:               existing.TenantID,
		DecisionID:             existing.ID,
		ExpectedFingerprint:    existing.RequestFingerprint,
		ReplacementFingerprint: requestFingerprint,
		Assignee:               assignee,
	})
	if errors.Is(err, ErrConflict) {
		return DecisionRequest{}, ErrIdempotencyConflict
	}
	return repaired, err
}

func stableFingerprint(value any) string {
	payload, _ := json.Marshal(value)
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func cloneMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	payload, _ := json.Marshal(source)
	var out map[string]any
	_ = json.Unmarshal(payload, &out)
	return out
}
