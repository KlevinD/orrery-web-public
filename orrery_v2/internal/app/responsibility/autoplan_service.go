package responsibility

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"orrery_v2/internal/app/hil"
)

const (
	AutoplanHILAction        = "responsibility.plan.execute"
	autoplanHILRequesterID   = "responsibility-autoplan"
	autoplanHILResourceKind  = "responsibility_autoplan"
	autoplanHILSource        = "responsibility_autoplan"
	autoplanHILExpiry        = 72 * time.Hour
	autoplanPlanningLease    = 2 * time.Minute
	autoplanPlanningRetryMin = time.Minute
	autoplanPlanningRetryMax = 30 * time.Minute
)

// AutoplanRepositoryPort is deliberately small so the planning lifecycle can
// be tested without depending on the rest of the responsibility coordinator.
type AutoplanRepositoryPort interface {
	ClaimAutoplan(context.Context, AutoplanRecord, int, time.Time) (AutoplanRecord, bool, error)
	RetryAutoplanPlanning(context.Context, AutoplanRecord, int, time.Time) (AutoplanRecord, bool, error)
	AcquireAutoplanPlanningLease(context.Context, AutoplanRecord, string, time.Duration, time.Time) (AutoplanRecord, bool, error)
	GetAutoplan(context.Context, string) (AutoplanRecord, error)
	GetLatestAutoplanForSnapshot(context.Context, AutoplanRequest) (AutoplanRecord, bool, error)
	ListAutoplans(context.Context, string, []AutoplanStatus, int) ([]AutoplanRecord, error)
	ListAutoplansForReconcile(context.Context, []AutoplanStatus, int) ([]AutoplanRecord, error)
	CompareAndSaveAutoplan(context.Context, AutoplanRecord, int64, time.Time) (bool, AutoplanRecord, error)
	LinkAutoplanHILDecision(context.Context, string, string, int64, time.Time) (AutoplanRecord, error)
	GetPolicy(context.Context, string, string, WorkKind) (ResponsibilityPolicyRecord, error)
	GetPeopleResponsibilityPortfolioByBindingID(context.Context, string) (PeopleResponsibilityPortfolio, bool, error)
	ListAssignments(context.Context, AssignmentListQuery) ([]AssignmentRecord, error)
}

// AutoplanPlanner is side-effect free. It may be backed by a model later, but
// the service accepts only a validated structured proposal and owns all
// durable state changes and task materialization.
type AutoplanPlanner interface {
	PlanAutoplan(context.Context, AutoplanPlanningRequest) (AutoplanProposal, error)
}

type AutoplanStepMaterializer interface {
	MaterializeAutoplanStep(context.Context, AutoplanRecord, string) (AutoplanMaterialization, error)
}

// AutoplanExecutionGrantWriter persists the narrow handoff from a consumed HIL
// receipt to the existing responsibility execution policy. The materializer
// has already created and assigned the task when this is called; a retry can
// therefore recover a crash between assignment and grant creation.
type AutoplanExecutionGrantWriter interface {
	GrantAutoplanExecution(context.Context, AutoplanRecord, AutoplanMaterialization, time.Time) (AutoplanExecutionGrant, error)
}

// AutoplanHILDecisionService exposes exactly the three durable HIL operations
// needed by a plan: create, recover a terminal result, and consume one
// authorization receipt before materializing a Mission Task.
type AutoplanHILDecisionService interface {
	CreateDecision(context.Context, hil.CreateDecisionInput) (hil.CreateDecisionResult, error)
	GetDecisionResolution(context.Context, string, string) (hil.DecisionResolution, error)
	ConsumeGatewayDecision(context.Context, hil.GatewayReceiptConsumeInput) (hil.DecisionReceipt, error)
}

// AutoplanHILDecisionSuperseder is intentionally separate from the normal
// creation/receipt port so older read-only HIL adapters remain usable until a
// plan actually needs to invalidate a pending approval. A linked HIL decision
// without this capability fails closed in the superseding state.
type AutoplanHILDecisionSuperseder interface {
	SupersedeDecision(context.Context, string, string, string) (hil.DecisionResolution, error)
}

type AutoplanServiceOptions struct {
	Repository         AutoplanRepositoryPort
	Planner            AutoplanPlanner
	GoalContext        AutoplanGoalContextProvider
	Materializer       AutoplanStepMaterializer
	ExecutionGrants    AutoplanExecutionGrantWriter
	HIL                AutoplanHILDecisionService
	Now                func() time.Time
	PlanningLeaseFor   time.Duration
	HumanReviewEnabled func(context.Context) (bool, error)
}

type AutoplanService struct {
	repository         AutoplanRepositoryPort
	planner            AutoplanPlanner
	goalContext        AutoplanGoalContextProvider
	materializer       AutoplanStepMaterializer
	executionGrants    AutoplanExecutionGrantWriter
	hil                AutoplanHILDecisionService
	now                func() time.Time
	planningLeaseFor   time.Duration
	humanReviewEnabled func(context.Context) (bool, error)
}

func NewAutoplanService(options AutoplanServiceOptions) (*AutoplanService, error) {
	if options.Repository == nil || options.Materializer == nil {
		return nil, ErrInvalidContract
	}
	if options.Planner == nil {
		options.Planner = BaselineAutoplanPlanner{}
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.PlanningLeaseFor <= 0 {
		options.PlanningLeaseFor = autoplanPlanningLease
	}
	return &AutoplanService{
		repository: options.Repository, planner: options.Planner, goalContext: options.GoalContext, materializer: options.Materializer,
		executionGrants: options.ExecutionGrants, hil: options.HIL, now: options.Now, planningLeaseFor: options.PlanningLeaseFor,
		humanReviewEnabled: options.HumanReviewEnabled,
	}, nil
}

func (s *AutoplanService) requiresHumanReview(ctx context.Context) (bool, error) {
	if s == nil || s.humanReviewEnabled == nil {
		return true, nil
	}
	return s.humanReviewEnabled(ctx)
}

// AutoplanPortfolioRequest is supplied by the responsibility pulse after it
// has established that no existing concrete Mission Task should take priority.
type AutoplanPortfolioRequest struct {
	TenantID           string
	ScheduledTaskID    string
	ScheduledRunID     string
	ApprovalAssigneeID string
	TriggeredAt        time.Time
	Portfolio          PeopleResponsibilityPortfolio
}

type AutoplanPlanningRequest struct {
	PlanRequest    AutoplanRequest
	Portfolio      PeopleResponsibilityPortfolio
	Responsibility PeopleResolvedResponsibility
	GoalContext    AutoplanGoalContext
	Policy         AutoplanPolicy
}

type AutoplanEvaluationResult struct {
	Evaluated        bool
	Reason           string
	DecisionSource   string
	Proposed         int
	PendingApproval  int
	Approved         int
	NotActionable    int
	Rejected         int
	Reused           int
	WaitingForSignal int
	CapacityBlocked  int
	PlanIDs          []string
}

type AutoplanReconcileResult struct {
	Candidates    int
	HILCreated    int
	Approved      int
	Rejected      int
	Superseded    int
	Materializing int
	Materialized  int
	Deferred      int
	Failed        int
	PlanIDs       []string
}

// AutoplanHILResolution is the validated handoff from the HIL observer or a
// replay scan. It intentionally carries only immutable plan bindings.
type AutoplanHILResolution struct {
	TenantID       string
	AutoplanID     string
	ProposalDigest string
	DecisionID     string
	ReceiptID      string
	Approved       bool
	Reason         string
	ResolvedAt     time.Time
}

// EvaluatePortfolio claims one plan per responsibility source revision. It
// never materializes a Mission Task directly; the reconcile worker consumes
// any HIL receipt and performs the retryable materialization phase later.
func (s *AutoplanService) EvaluatePortfolio(ctx context.Context, input AutoplanPortfolioRequest) (AutoplanEvaluationResult, error) {
	result := AutoplanEvaluationResult{}
	if s == nil || s.repository == nil || s.planner == nil {
		return result, ErrInvalidContract
	}
	input = normalizeAutoplanPortfolioRequest(input)
	if err := validateAutoplanPortfolioRequest(input); err != nil {
		return result, err
	}
	result.Evaluated = true
	policy, err := s.repository.GetPolicy(ctx, input.TenantID, input.Portfolio.AgentProfileID, WorkKindMissionTask)
	if errors.Is(err, ErrNotFound) {
		result.Reason = "autoplan_policy_not_configured"
		return result, nil
	}
	if err != nil {
		return result, err
	}
	planningPolicy := AutoplanPolicyFromResponsibility(policy)
	if !planningPolicy.Enabled {
		result.Reason = "autoplan_policy_not_continuous"
		return result, nil
	}

	responsibilities := append([]PeopleResolvedResponsibility(nil), input.Portfolio.Responsibilities...)
	sort.Slice(responsibilities, func(i, j int) bool {
		if responsibilities[i].Revision != responsibilities[j].Revision {
			return responsibilities[i].Revision > responsibilities[j].Revision
		}
		return responsibilities[i].ID < responsibilities[j].ID
	})
	if len(responsibilities) == 0 {
		result.Reason = "autoplan_no_responsibilities"
		return result, nil
	}

	for _, item := range responsibilities {
		goalContext, goalContextErr := s.resolveAutoplanGoalContext(ctx, input.Portfolio, item)
		if goalContextErr != nil {
			return result, goalContextErr
		}
		baseRequest := autoplanRequestForResponsibility(input, policy, item, goalContext, 1)
		latest, found, latestErr := s.repository.GetLatestAutoplanForSnapshot(ctx, baseRequest)
		if latestErr != nil {
			return result, latestErr
		}
		cycle := 1
		var existing *AutoplanRecord
		if found {
			copy := latest
			existing = &copy
			if latest.Status == AutoplanStatusMaterialized {
				// A completed plan is evidence that this exact responsibility
				// snapshot was already acted on, not evidence of a new gap. Wait
				// for a changed source or policy snapshot instead of manufacturing
				// the same task again on every scheduled pulse.
				result.PlanIDs = appendAutoplanID(result.PlanIDs, latest.ID)
				result.Reused++
				result.WaitingForSignal++
				continue
			}
		}
		if existing != nil && existing.ActorMemberID != baseRequest.ActorMemberID {
			switch existing.Status {
			case AutoplanStatusPlanning, AutoplanStatusProposed, AutoplanStatusFailed:
				updated, _, updateErr := s.reassignAutoplanAssignee(ctx, *existing, baseRequest.ActorMemberID)
				if updateErr != nil {
					return result, updateErr
				}
				existing = &updated
			case AutoplanStatusPendingApproval, AutoplanStatusApproved:
				superseded, _, supersedeErr := s.supersedeAutoplan(ctx, *existing, "approval_assignee_changed")
				if supersedeErr != nil {
					return result, supersedeErr
				}
				if superseded.Status != AutoplanStatusSuperseded {
					result.PlanIDs = appendAutoplanID(result.PlanIDs, superseded.ID)
					result.Reused++
					continue
				}
				cycle = existing.PlanningCycle + 1
				existing = nil
			case AutoplanStatusSuperseding:
				superseded, _, supersedeErr := s.completeAutoplanSupersede(ctx, *existing)
				if supersedeErr != nil {
					return result, supersedeErr
				}
				if superseded.Status != AutoplanStatusSuperseded {
					result.PlanIDs = appendAutoplanID(result.PlanIDs, superseded.ID)
					result.Reused++
					continue
				}
				cycle = existing.PlanningCycle + 1
				existing = nil
			case AutoplanStatusMaterializing:
				// A consumed receipt may already be creating a task. Do not replace
				// its authorization mid-materialization; the next cycle picks up the
				// current assignee after this immutable plan becomes terminal.
				result.PlanIDs = appendAutoplanID(result.PlanIDs, existing.ID)
				result.Reused++
				continue
			default:
				cycle = existing.PlanningCycle + 1
				existing = nil
			}
		}
		if existing != nil && existing.Status != AutoplanStatusMaterialized {
			result.PlanIDs = appendAutoplanID(result.PlanIDs, existing.ID)
			switch existing.Status {
			case AutoplanStatusPlanning:
				stored, outcome, proposeErr := s.proposeAutoplan(ctx, *existing, input.Portfolio, item, goalContext, planningPolicy)
				if proposeErr != nil {
					return result, proposeErr
				}
				recordAutoplanEvaluationOutcome(&result, stored, outcome)
				continue
			case AutoplanStatusFailed:
				stored, retryErr := s.retryPlanning(ctx, *existing)
				if retryErr != nil {
					if errors.Is(retryErr, ErrAutoplanCapacity) {
						result.CapacityBlocked++
						continue
					}
					return result, retryErr
				}
				if stored.Status != AutoplanStatusPlanning {
					result.Reused++
					continue
				}
				stored, outcome, proposeErr := s.proposeAutoplan(ctx, stored, input.Portfolio, item, goalContext, planningPolicy)
				if proposeErr != nil {
					return result, proposeErr
				}
				recordAutoplanEvaluationOutcome(&result, stored, outcome)
				continue
			case AutoplanStatusProposed:
				stored, outcome, advanceErr := s.advanceProposedAutoplan(ctx, *existing, planningPolicy)
				if advanceErr != nil {
					return result, advanceErr
				}
				recordAutoplanEvaluationOutcome(&result, stored, outcome)
				continue
			default:
				result.Reused++
				continue
			}
		}
		request := autoplanRequestForResponsibility(input, policy, item, goalContext, cycle)
		candidate, err := NewAutoplanRecord(request, s.nowUTC())
		if err != nil {
			return result, err
		}
		stored, claimed, err := s.repository.ClaimAutoplan(ctx, candidate, planningPolicy.MaxOpenPlans, s.nowUTC())
		if err != nil {
			if errors.Is(err, ErrAutoplanCapacity) {
				result.CapacityBlocked++
				continue
			}
			return result, err
		}
		result.PlanIDs = appendAutoplanID(result.PlanIDs, stored.ID)
		if !claimed {
			if stored.Status == AutoplanStatusPlanning {
				claimed = true
			}
			if stored.Status == AutoplanStatusFailed {
				stored, err = s.retryPlanning(ctx, stored)
				if err != nil {
					if errors.Is(err, ErrAutoplanCapacity) {
						result.CapacityBlocked++
						continue
					}
					return result, err
				}
				claimed = stored.Status == AutoplanStatusPlanning
			}
			if !claimed {
				result.Reused++
				continue
			}
		}
		stored, outcome, err := s.proposeAutoplan(ctx, stored, input.Portfolio, item, goalContext, planningPolicy)
		if err != nil {
			return result, err
		}
		recordAutoplanEvaluationOutcome(&result, stored, outcome)
	}
	result.Reason = autoplanEvaluationReason(result)
	result.DecisionSource = firstAutoplanID(result.PlanIDs)
	return result, nil
}

// Reconcile advances persisted plans after a restart or missed observer
// delivery. It is safe to run from the global responsibility reconciler.
func (s *AutoplanService) Reconcile(ctx context.Context, limit int) (AutoplanReconcileResult, error) {
	result := AutoplanReconcileResult{}
	if s == nil || s.repository == nil || s.materializer == nil {
		return result, ErrInvalidContract
	}
	plans, err := s.repository.ListAutoplansForReconcile(ctx, []AutoplanStatus{
		AutoplanStatusPlanning, AutoplanStatusProposed, AutoplanStatusPendingApproval, AutoplanStatusApproved, AutoplanStatusMaterializing, AutoplanStatusFailed, AutoplanStatusSuperseding,
	}, limit)
	if err != nil {
		return result, err
	}
	var reconcileErrors []error
	for _, plan := range plans {
		result.Candidates++
		result.PlanIDs = appendAutoplanID(result.PlanIDs, plan.ID)
		var current AutoplanRecord
		var changed bool
		if plan.Status == AutoplanStatusSuperseding {
			current, changed, err = s.completeAutoplanSupersede(ctx, plan)
			if err != nil {
				result.Failed++
				reconcileErrors = append(reconcileErrors, fmt.Errorf("reconcile autonomous plan %s: %w", plan.ID, err))
				continue
			}
			if changed && current.Status == AutoplanStatusSuperseded {
				result.Superseded++
			} else {
				result.Deferred++
			}
			continue
		}
		if plan.Status == AutoplanStatusPlanning || plan.Status == AutoplanStatusProposed || plan.Status == AutoplanStatusPendingApproval || plan.Status == AutoplanStatusApproved || plan.Status == AutoplanStatusMaterializing || plan.Status == AutoplanStatusFailed {
			current, changed, err = s.supersedeStaleAutoplan(ctx, plan)
			if err != nil {
				result.Failed++
				reconcileErrors = append(reconcileErrors, fmt.Errorf("reconcile autonomous plan %s: %w", plan.ID, err))
				continue
			}
			if changed {
				result.Superseded++
				continue
			}
			plan = current
		}
		switch plan.Status {
		case AutoplanStatusPlanning, AutoplanStatusProposed:
			current, changed, err = s.recoverUnfinishedAutoplan(ctx, plan)
			if err == nil && changed {
				if current.Status == AutoplanStatusPendingApproval {
					result.HILCreated++
				} else if current.Status == AutoplanStatusApproved {
					result.Approved++
				} else if current.Status == AutoplanStatusNotActionable {
					result.Deferred++
				}
			}
		case AutoplanStatusFailed:
			current, err = s.retryPlanning(ctx, plan)
			changed = err == nil && current.Version != plan.Version
			if errors.Is(err, ErrAutoplanCapacity) {
				err = nil
				changed = false
				current = plan
			}
			if err == nil && changed && current.Status == AutoplanStatusPlanning {
				current, changed, err = s.recoverUnfinishedAutoplan(ctx, current)
			}
			if err == nil && changed {
				if current.Status == AutoplanStatusPendingApproval {
					result.HILCreated++
				} else if current.Status == AutoplanStatusApproved {
					result.Approved++
				} else if current.Status == AutoplanStatusNotActionable {
					result.Deferred++
				}
			}
		case AutoplanStatusPendingApproval:
			current, changed, err = s.reconcilePendingApproval(ctx, plan)
			if err == nil && changed {
				if current.Status == AutoplanStatusApproved {
					result.Approved++
				} else if current.Status == AutoplanStatusRejected {
					result.Rejected++
				} else if current.HILDecisionID != "" {
					result.HILCreated++
				}
			}
		case AutoplanStatusApproved, AutoplanStatusMaterializing:
			current, changed, err = s.materializeAutoplan(ctx, plan)
			if err == nil && changed {
				if current.Status == AutoplanStatusMaterialized {
					result.Materialized++
				} else if current.Status == AutoplanStatusMaterializing {
					result.Materializing++
				}
			}
		}
		if err != nil {
			result.Failed++
			reconcileErrors = append(reconcileErrors, fmt.Errorf("reconcile autonomous plan %s: %w", plan.ID, err))
		} else if !changed {
			result.Deferred++
		}
	}
	return result, errors.Join(reconcileErrors...)
}

// ObserveHILResolution persists a terminal HIL decision. The observer does
// not call the Mission materializer; the next reconciliation owns that side
// effect and can recover after process restart.
func (s *AutoplanService) ObserveHILResolution(ctx context.Context, resolution AutoplanHILResolution) error {
	if s == nil || s.repository == nil {
		return ErrInvalidContract
	}
	resolution = normalizeAutoplanHILResolution(resolution)
	if err := resolution.validate(); err != nil {
		return err
	}
	for attempt := 0; attempt < 3; attempt++ {
		plan, err := s.repository.GetAutoplan(ctx, resolution.AutoplanID)
		if err != nil {
			return err
		}
		if plan.TenantID != resolution.TenantID || plan.HILDecisionID != resolution.DecisionID || plan.ProposalDigest != resolution.ProposalDigest {
			return ErrConflict
		}
		if plan.Status == AutoplanStatusSuperseding || plan.Status == AutoplanStatusSuperseded {
			// The plan fence wins over a late HIL observer callback. The terminal
			// HIL receipt remains auditable, but it must not revive invalidated work.
			return nil
		}
		if plan.Status == AutoplanStatusMaterialized || plan.Status == AutoplanStatusMaterializing || plan.Status == AutoplanStatusApproved {
			if resolution.Approved && plan.HILReceiptID == resolution.ReceiptID {
				return nil
			}
			return ErrConflict
		}
		if plan.Status == AutoplanStatusRejected {
			if !resolution.Approved {
				return nil
			}
			return ErrConflict
		}
		if plan.Status != AutoplanStatusPendingApproval {
			return ErrConflict
		}
		next := plan
		if resolution.Approved {
			next.Status = AutoplanStatusApproved
			next.ApprovedProposalDigest = plan.ProposalDigest
			next.HILReceiptID = resolution.ReceiptID
			next.RejectionReason = ""
		} else {
			next.Status = AutoplanStatusRejected
			next.RejectionReason = firstNonBlankAutoplan(resolution.Reason, "HIL decision denied")
		}
		updated, current, err := s.repository.CompareAndSaveAutoplan(ctx, next, plan.Version, s.nowUTC())
		if err != nil {
			return err
		}
		if updated || current.Status == next.Status {
			return nil
		}
	}
	return ErrConflict
}

func (s *AutoplanService) proposeAutoplan(ctx context.Context, record AutoplanRecord, portfolio PeopleResponsibilityPortfolio, item PeopleResolvedResponsibility, goalContext AutoplanGoalContext, policy AutoplanPolicy) (AutoplanRecord, AutoplanStatus, error) {
	if record.Status != AutoplanStatusPlanning {
		return record, record.Status, ErrConflict
	}
	leased, acquired, err := s.repository.AcquireAutoplanPlanningLease(ctx, record, "autoplan-planner-"+uuid.NewString(), s.planningLeaseFor, s.nowUTC())
	if err != nil {
		return record, record.Status, err
	}
	if !acquired {
		return leased, leased.Status, nil
	}
	record = leased
	goalDigest, err := AutoplanGoalContextDigest(goalContext)
	if err != nil || (record.GoalContextDigest != "" && record.GoalContextDigest != goalDigest) {
		if err == nil {
			err = ErrConflict
		}
		_, _ = s.failPlanning(ctx, record, err)
		return record, AutoplanStatusFailed, err
	}
	planningCtx, cancel := context.WithTimeout(ctx, s.planningLeaseFor)
	defer cancel()
	proposal, err := s.planner.PlanAutoplan(planningCtx, AutoplanPlanningRequest{
		PlanRequest: record.Request(), Portfolio: portfolio, Responsibility: item, GoalContext: goalContext, Policy: policy,
	})
	if err != nil {
		_, _ = s.failPlanning(ctx, record, err)
		return record, AutoplanStatusFailed, err
	}
	proposal = proposal.Normalized()
	if err := proposal.Validate(); err != nil {
		_, _ = s.failPlanning(ctx, record, err)
		return record, AutoplanStatusFailed, err
	}
	if err := validateAutoplanGoalDerivation(goalContext, proposal); err != nil {
		_, _ = s.failPlanning(ctx, record, err)
		return record, AutoplanStatusFailed, err
	}
	digest, err := AutoplanProposalDigest(proposal)
	if err != nil {
		return record, AutoplanStatusFailed, err
	}
	next := record
	next.Status = AutoplanStatusProposed
	if len(proposal.Steps) == 0 {
		next.Status = AutoplanStatusNotActionable
	}
	next.Proposal, next.ProposalDigest = proposal, digest
	next.PlanningLeaseOwner = ""
	next.PlanningLeaseExpiresAt = time.Time{}
	updated, current, err := s.repository.CompareAndSaveAutoplan(ctx, next, record.Version, s.nowUTC())
	if err != nil {
		return record, AutoplanStatusFailed, err
	}
	if !updated {
		return current, current.Status, nil
	}
	if current.Status == AutoplanStatusNotActionable {
		return current, current.Status, nil
	}
	return s.advanceProposedAutoplan(ctx, current, policy)
}

// advanceProposedAutoplan applies deterministic policy after a proposal is
// durably stored. Keeping this separate from model invocation means a crash
// between proposal persistence and approval routing is recoverable.
func (s *AutoplanService) advanceProposedAutoplan(ctx context.Context, record AutoplanRecord, policy AutoplanPolicy) (AutoplanRecord, AutoplanStatus, error) {
	if record.Status != AutoplanStatusProposed {
		return record, record.Status, ErrConflict
	}
	proposal := record.Proposal.Normalized()
	if err := proposal.Validate(); err != nil {
		_, _ = s.failProposedAutoplan(ctx, record, err)
		return record, AutoplanStatusFailed, err
	}
	digest, err := AutoplanProposalDigest(proposal)
	if err != nil || digest != record.ProposalDigest {
		if err == nil {
			err = ErrConflict
		}
		_, _ = s.failProposedAutoplan(ctx, record, err)
		return record, AutoplanStatusFailed, err
	}
	if len(proposal.Steps) == 0 {
		next := record
		next.Status = AutoplanStatusNotActionable
		updated, current, err := s.repository.CompareAndSaveAutoplan(ctx, next, record.Version, s.nowUTC())
		if err != nil || !updated {
			return current, current.Status, err
		}
		return current, current.Status, nil
	}
	decision, err := EvaluateAutoplanPolicy(policy, proposal)
	if err != nil {
		return record, record.Status, err
	}
	if decision.ApprovalRequired {
		reviewEnabled, reviewErr := s.requiresHumanReview(ctx)
		if reviewErr != nil {
			return record, record.Status, reviewErr
		}
		if !reviewEnabled {
			decision.ApprovalRequired = false
			decision.Reason = "human review is disabled"
		}
	}
	if !decision.Allowed {
		next := record
		next.Status = AutoplanStatusRejected
		next.RejectionReason = firstNonBlankAutoplan(decision.Reason, "autonomous plan is outside the responsibility policy")
		updated, current, err := s.repository.CompareAndSaveAutoplan(ctx, next, record.Version, s.nowUTC())
		if err != nil || !updated {
			return current, current.Status, err
		}
		return current, current.Status, nil
	}
	if decision.ApprovalRequired {
		if strings.TrimSpace(record.ActorMemberID) == "" {
			next := record
			next.Status = AutoplanStatusRejected
			next.RejectionReason = "autonomous plan requires a configured human approval assignee"
			updated, current, err := s.repository.CompareAndSaveAutoplan(ctx, next, record.Version, s.nowUTC())
			if err != nil || !updated {
				return current, current.Status, err
			}
			return current, current.Status, nil
		}
		next := record
		next.Status = AutoplanStatusPendingApproval
		next.ApprovalAttempt = 1
		next.ApprovalRequestedAt = s.nowUTC()
		next.ApprovalID = autoplanApprovalID(next)
		updated, current, err := s.repository.CompareAndSaveAutoplan(ctx, next, record.Version, next.ApprovalRequestedAt)
		if err != nil || !updated {
			return current, current.Status, err
		}
		linked, linkErr := s.ensureAutoplanHILDecision(ctx, current)
		if linkErr != nil {
			return current, current.Status, linkErr
		}
		return linked, linked.Status, nil
	}
	next := record
	next.Status = AutoplanStatusApproved
	next.ApprovedProposalDigest = record.ProposalDigest
	updated, current, err := s.repository.CompareAndSaveAutoplan(ctx, next, record.Version, s.nowUTC())
	if err != nil || !updated {
		return current, current.Status, err
	}
	return current, current.Status, nil
}

func recordAutoplanEvaluationOutcome(result *AutoplanEvaluationResult, plan AutoplanRecord, outcome AutoplanStatus) {
	if result == nil {
		return
	}
	result.PlanIDs = appendAutoplanID(result.PlanIDs, plan.ID)
	switch outcome {
	case AutoplanStatusProposed:
		result.Proposed++
	case AutoplanStatusPendingApproval:
		result.PendingApproval++
	case AutoplanStatusApproved:
		result.Approved++
	case AutoplanStatusNotActionable:
		result.NotActionable++
	case AutoplanStatusRejected:
		result.Rejected++
	}
}

// recoverUnfinishedAutoplan resumes the two states that can survive a process
// exit between independent durable writes: before proposal generation and
// after proposal persistence but before policy/approval routing.
func (s *AutoplanService) recoverUnfinishedAutoplan(ctx context.Context, plan AutoplanRecord) (AutoplanRecord, bool, error) {
	if plan.Status != AutoplanStatusPlanning && plan.Status != AutoplanStatusProposed {
		return plan, false, ErrConflict
	}
	portfolio, found, err := s.repository.GetPeopleResponsibilityPortfolioByBindingID(ctx, plan.PortfolioBindingID)
	if err != nil {
		return plan, false, err
	}
	if !found || portfolio.TenantID != plan.TenantID || portfolio.ID != plan.PortfolioID ||
		portfolio.BindingID != plan.PortfolioBindingID || portfolio.AgentProfileID != plan.AgentProfileID ||
		portfolio.Status != PeoplePortfolioStatusActive || portfolio.SourceRevision != plan.SourceRevision ||
		portfolio.SourceDigest != plan.SourceDigest {
		return s.supersedeAutoplan(ctx, plan, "portfolio_version_changed")
	}
	policyRecord, err := s.repository.GetPolicy(ctx, plan.TenantID, plan.AgentProfileID, WorkKindMissionTask)
	if errors.Is(err, ErrNotFound) {
		return s.supersedeAutoplan(ctx, plan, "responsibility_policy_removed")
	}
	if err != nil {
		return plan, false, err
	}
	policy := AutoplanPolicyFromResponsibility(policyRecord)
	if !policy.Enabled || policyRecord.ID != plan.PolicyID || policyRecord.Version != plan.PolicyVersion {
		return s.supersedeAutoplan(ctx, plan, "responsibility_policy_changed")
	}
	var item *PeopleResolvedResponsibility
	for index := range portfolio.Responsibilities {
		candidate := &portfolio.Responsibilities[index]
		if responsibilityAutoplanTriggerRef(*candidate) == plan.TriggerRef {
			item = candidate
			break
		}
	}
	if item == nil {
		return s.supersedeAutoplan(ctx, plan, "responsibility_removed")
	}
	goalContext, err := s.resolveAutoplanGoalContext(ctx, portfolio, *item)
	if err != nil {
		return plan, false, err
	}
	goalDigest, err := AutoplanGoalContextDigest(goalContext)
	if err != nil {
		return plan, false, err
	}
	if plan.GoalContextDigest != "" && plan.GoalContextDigest != goalDigest {
		return s.supersedeAutoplan(ctx, plan, "goal_context_changed")
	}
	if plan.Status == AutoplanStatusPlanning {
		next, _, err := s.proposeAutoplan(ctx, plan, portfolio, *item, goalContext, policy)
		return next, err == nil && next.Version != plan.Version, err
	}
	next, _, err := s.advanceProposedAutoplan(ctx, plan, policy)
	return next, err == nil && next.Version != plan.Version, err
}

func (s *AutoplanService) reconcilePendingApproval(ctx context.Context, plan AutoplanRecord) (AutoplanRecord, bool, error) {
	if plan.HILDecisionID == "" {
		if expiresAt := autoplanHILExpiresAt(plan); !expiresAt.IsZero() && !s.nowUTC().Before(expiresAt) {
			return s.rejectAutoplan(ctx, plan, "HIL approval request expired")
		}
		linked, err := s.ensureAutoplanHILDecision(ctx, plan)
		return linked, linked.ID != plan.ID || linked.HILDecisionID != plan.HILDecisionID, err
	}
	if s.hil == nil {
		return plan, false, ErrInvalidContract
	}
	resolution, err := s.hil.GetDecisionResolution(ctx, plan.TenantID, plan.HILDecisionID)
	if errors.Is(err, hil.ErrConflict) {
		if expiresAt := autoplanHILExpiresAt(plan); !expiresAt.IsZero() && !s.nowUTC().Before(expiresAt) {
			return s.rejectAutoplan(ctx, plan, "HIL approval request expired")
		}
		return plan, false, nil
	}
	if err != nil {
		return plan, false, err
	}
	handoff, err := autoplanHILResolutionFromDecision(resolution)
	if err != nil {
		return plan, false, err
	}
	if err := s.ObserveHILResolution(ctx, handoff); err != nil {
		return plan, false, err
	}
	current, err := s.repository.GetAutoplan(ctx, plan.ID)
	return current, err == nil && current.Version != plan.Version, err
}

func (s *AutoplanService) ensureAutoplanHILDecision(ctx context.Context, plan AutoplanRecord) (AutoplanRecord, error) {
	if plan.Status != AutoplanStatusPendingApproval || plan.ProposalDigest == "" || plan.ApprovalID == "" {
		return plan, ErrInvalidContract
	}
	if plan.ApprovalAttempt < 1 || plan.ApprovalRequestedAt.IsZero() {
		return plan, ErrInvalidContract
	}
	if plan.HILDecisionID != "" {
		return plan, nil
	}
	if s.hil == nil {
		return plan, ErrInvalidContract
	}
	input := hil.CreateDecisionInput{
		TenantID:         plan.TenantID,
		Requester:        hil.ActorRef{Kind: hil.ActorService, ID: autoplanHILRequesterID},
		Action:           AutoplanHILAction,
		ActionTypeID:     AutoplanHILAction,
		Resource:         autoplanHILResource(plan),
		ObjectRefs:       []hil.ObjectRef{{Kind: "responsibility_portfolio", ID: plan.PortfolioID}, {Kind: autoplanHILResourceKind, ID: plan.ID}},
		ArgsHash:         plan.ProposalDigest,
		Purpose:          "Approve autonomous responsibility plan materialization and resulting scoped runtime execution",
		RiskLevel:        autoplanProposalRisk(plan.Proposal),
		PreferredMode:    hil.ModeApproval,
		ApprovalRequired: true,
		SubjectRefs:      []hil.ObjectRef{{Kind: "responsibility_portfolio", ID: plan.PortfolioID}},
		PayloadPreview:   autoplanHILPayload(plan),
		IdempotencyKey:   autoplanHILIdempotencyKey(plan),
		ExpiresAt:        autoplanHILExpiresAt(plan),
	}
	created, err := s.hil.CreateDecision(ctx, input)
	if err != nil {
		return plan, err
	}
	if created.Decision.Route.Mode != hil.ModeApproval || strings.TrimSpace(created.Decision.ID) == "" {
		return plan, fmt.Errorf("%w: autonomous plan must use a fixed HIL approval", ErrConflict)
	}
	linked, err := s.repository.LinkAutoplanHILDecision(ctx, plan.ID, created.Decision.ID, plan.Version, s.nowUTC())
	if err != nil {
		return plan, err
	}
	if created.Decision.Status != hil.DecisionPending {
		resolution, resolveErr := s.hil.GetDecisionResolution(ctx, plan.TenantID, created.Decision.ID)
		if resolveErr == nil {
			if handoff, handoffErr := autoplanHILResolutionFromDecision(resolution); handoffErr == nil {
				resolveErr = s.ObserveHILResolution(ctx, handoff)
			}
		}
		if resolveErr != nil && !errors.Is(resolveErr, hil.ErrConflict) {
			return linked, resolveErr
		}
		if current, getErr := s.repository.GetAutoplan(ctx, plan.ID); getErr == nil {
			linked = current
		}
	}
	return linked, nil
}

func (s *AutoplanService) materializeAutoplan(ctx context.Context, plan AutoplanRecord) (AutoplanRecord, bool, error) {
	if plan.Status != AutoplanStatusApproved && plan.Status != AutoplanStatusMaterializing {
		return plan, false, ErrConflict
	}
	if plan.Status == AutoplanStatusApproved {
		hasObligation, err := s.hasConcreteAutoplanObligation(ctx, plan)
		if err != nil {
			return plan, false, err
		}
		if hasObligation {
			return plan, false, nil
		}
	}
	var err error
	if plan.HILDecisionID != "" {
		plan, err = s.ensureAutoplanHILReceiptConsumed(ctx, plan)
		if err != nil {
			return plan, false, err
		}
		if plan.Status != AutoplanStatusApproved && plan.Status != AutoplanStatusMaterializing {
			return plan, true, nil
		}
	}
	if plan.Status == AutoplanStatusApproved {
		next := plan
		next.Status = AutoplanStatusMaterializing
		updated, current, err := s.repository.CompareAndSaveAutoplan(ctx, next, plan.Version, s.nowUTC())
		if err != nil {
			return plan, false, err
		}
		if !updated {
			return current, false, nil
		}
		plan = current
	}
	completed := make(map[string]struct{}, len(plan.Materializations))
	for _, receipt := range plan.Materializations {
		completed[receipt.StepID] = struct{}{}
	}
	for _, step := range plan.Proposal.Steps {
		if _, ok := completed[step.StepID]; ok {
			continue
		}
		fenced, superseded, fenceErr := s.supersedeStaleAutoplan(ctx, plan)
		if fenceErr != nil {
			return plan, false, fenceErr
		}
		if superseded {
			return fenced, true, nil
		}
		plan = fenced
		receipt, err := s.materializer.MaterializeAutoplanStep(ctx, plan, step.StepID)
		if err != nil {
			next := plan
			next.FailureReason = strings.TrimSpace(err.Error())
			_, current, saveErr := s.repository.CompareAndSaveAutoplan(ctx, next, plan.Version, s.nowUTC())
			if saveErr == nil {
				plan = current
			}
			return plan, false, err
		}
		if err := receipt.Validate(); err != nil || receipt.StepID != step.StepID {
			return plan, false, fmt.Errorf("%w: autonomous plan materializer returned an invalid receipt", ErrInvalidContract)
		}
		if plan.HILDecisionID != "" {
			if s.executionGrants == nil {
				return plan, false, fmt.Errorf("%w: HIL-authorized autonomous plan requires an execution grant writer", ErrInvalidContract)
			}
			if _, err := s.executionGrants.GrantAutoplanExecution(ctx, plan, receipt, s.nowUTC()); err != nil {
				return plan, false, fmt.Errorf("grant HIL-authorized autonomous-plan execution: %w", err)
			}
		}
		next := plan
		next.Materializations = append(next.Materializations, receipt)
		next.FailureReason = ""
		updated, current, err := s.repository.CompareAndSaveAutoplan(ctx, next, plan.Version, s.nowUTC())
		if err != nil {
			return plan, false, err
		}
		if !updated {
			return current, false, nil
		}
		plan = current
	}
	next := plan
	next.Status = AutoplanStatusMaterialized
	next.FailureReason = ""
	updated, current, err := s.repository.CompareAndSaveAutoplan(ctx, next, plan.Version, s.nowUTC())
	if err != nil {
		return plan, false, err
	}
	return current, updated, nil
}

// supersedeStaleAutoplan fences a plan before consuming a HIL receipt or
// creating work. Portfolio and policy revisions are part of the authorization
// boundary: a decision for an older snapshot must never create a new task.
func (s *AutoplanService) supersedeStaleAutoplan(ctx context.Context, plan AutoplanRecord) (AutoplanRecord, bool, error) {
	if plan.Status != AutoplanStatusPlanning && plan.Status != AutoplanStatusProposed && plan.Status != AutoplanStatusPendingApproval &&
		plan.Status != AutoplanStatusApproved && plan.Status != AutoplanStatusMaterializing && plan.Status != AutoplanStatusFailed {
		return plan, false, nil
	}
	portfolio, found, err := s.repository.GetPeopleResponsibilityPortfolioByBindingID(ctx, plan.PortfolioBindingID)
	if err != nil {
		return plan, false, err
	}
	reason := ""
	if !found {
		reason = "portfolio_removed"
	} else if portfolio.TenantID != plan.TenantID || portfolio.ID != plan.PortfolioID || portfolio.BindingID != plan.PortfolioBindingID ||
		portfolio.AgentProfileID != plan.AgentProfileID || portfolio.Status != PeoplePortfolioStatusActive ||
		portfolio.SourceRevision != plan.SourceRevision || portfolio.SourceDigest != plan.SourceDigest ||
		PeopleResponsibilityPortfolioVersionID(portfolio.BindingID, portfolio.SourceDigest) != plan.PortfolioVersionID {
		reason = "portfolio_version_changed"
	}
	if reason == "" {
		policy, policyErr := s.repository.GetPolicy(ctx, plan.TenantID, plan.AgentProfileID, WorkKindMissionTask)
		if errors.Is(policyErr, ErrNotFound) {
			reason = "responsibility_policy_removed"
		} else if policyErr != nil {
			return plan, false, policyErr
		} else if policy.ID != plan.PolicyID || policy.Version != plan.PolicyVersion || !policy.Enabled || policy.AutonomyLevel != AutonomyContinuous {
			reason = "responsibility_policy_changed"
		}
	}
	if reason == "" && plan.GoalContextDigest != "" {
		var item *PeopleResolvedResponsibility
		for index := range portfolio.Responsibilities {
			candidate := &portfolio.Responsibilities[index]
			if responsibilityAutoplanTriggerRef(*candidate) == plan.TriggerRef {
				item = candidate
				break
			}
		}
		if item == nil {
			reason = "responsibility_removed"
		} else {
			goalContext, goalErr := s.resolveAutoplanGoalContext(ctx, portfolio, *item)
			if goalErr != nil {
				return plan, false, goalErr
			}
			goalDigest, goalErr := AutoplanGoalContextDigest(goalContext)
			if goalErr != nil {
				return plan, false, goalErr
			}
			if goalDigest != plan.GoalContextDigest {
				reason = "goal_context_changed"
			}
		}
	}
	if reason == "" {
		return plan, false, nil
	}
	return s.supersedeAutoplan(ctx, plan, reason)
}

func (s *AutoplanService) supersedeAutoplan(ctx context.Context, plan AutoplanRecord, reason string) (AutoplanRecord, bool, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return plan, false, ErrInvalidContract
	}
	if plan.Status == AutoplanStatusSuperseded {
		return plan, false, nil
	}
	if plan.Status == AutoplanStatusSuperseding {
		return s.completeAutoplanSupersede(ctx, plan)
	}
	if plan.Status == AutoplanStatusPendingApproval && strings.TrimSpace(plan.HILDecisionID) != "" {
		next := plan
		next.Status = AutoplanStatusSuperseding
		next.SupersededBy = reason
		next.FailureReason = ""
		updated, current, err := s.repository.CompareAndSaveAutoplan(ctx, next, plan.Version, s.nowUTC())
		if err != nil || !updated {
			return current, updated, err
		}
		return s.completeAutoplanSupersede(ctx, current)
	}
	next := plan
	next.Status = AutoplanStatusSuperseded
	next.SupersededBy = reason
	next.FailureReason = ""
	updated, current, err := s.repository.CompareAndSaveAutoplan(ctx, next, plan.Version, s.nowUTC())
	if err != nil {
		return plan, false, err
	}
	return current, updated, nil
}

func (s *AutoplanService) completeAutoplanSupersede(ctx context.Context, plan AutoplanRecord) (AutoplanRecord, bool, error) {
	if plan.Status != AutoplanStatusSuperseding || strings.TrimSpace(plan.SupersededBy) == "" {
		return plan, false, ErrConflict
	}
	if strings.TrimSpace(plan.HILDecisionID) != "" {
		if s.hil == nil {
			return plan, false, ErrInvalidContract
		}
		superseder, ok := s.hil.(AutoplanHILDecisionSuperseder)
		if !ok {
			return plan, false, ErrInvalidContract
		}
		if _, err := superseder.SupersedeDecision(ctx, plan.TenantID, plan.HILDecisionID, "autonomous plan superseded: "+plan.SupersededBy); err != nil {
			return plan, false, err
		}
	}
	next := plan
	next.Status = AutoplanStatusSuperseded
	updated, current, err := s.repository.CompareAndSaveAutoplan(ctx, next, plan.Version, s.nowUTC())
	if err != nil {
		return plan, false, err
	}
	return current, updated, nil
}

// hasConcreteAutoplanObligation gives existing work priority over autonomous
// creation. It deliberately defers instead of invalidating the plan: once the
// existing task closes, the same immutable plan can still be materialized if
// its portfolio, policy, and HIL receipt remain current.
func (s *AutoplanService) hasConcreteAutoplanObligation(ctx context.Context, plan AutoplanRecord) (bool, error) {
	assignments, err := s.repository.ListAssignments(ctx, AssignmentListQuery{
		TenantID: plan.TenantID,
		AgentID:  plan.AgentProfileID,
		States: []AssignmentState{
			AssignmentStateOffered, AssignmentStateAccepted, AssignmentStateActive,
			AssignmentStateWaiting, AssignmentStateSubmitted, AssignmentStateEscalated,
		},
		Limit: 100,
	})
	if err != nil {
		return false, err
	}
	for _, assignment := range assignments {
		if assignment.WorkKind == WorkKindMissionTask && strings.TrimSpace(assignment.MissionID) != "" && strings.TrimSpace(assignment.WorkID) != "" {
			return true, nil
		}
	}
	return false, nil
}

func (s *AutoplanService) ensureAutoplanHILReceiptConsumed(ctx context.Context, plan AutoplanRecord) (AutoplanRecord, error) {
	if s.hil == nil || plan.HILDecisionID == "" || plan.HILReceiptID == "" {
		return plan, ErrInvalidContract
	}
	resolution, err := s.hil.GetDecisionResolution(ctx, plan.TenantID, plan.HILDecisionID)
	if err != nil {
		return plan, err
	}
	if resolution.Decision.Status != hil.DecisionResolved || resolution.Receipt.Effect != hil.EffectAuthorize ||
		resolution.Receipt.ID != plan.HILReceiptID || resolution.Receipt.ArgsHash != plan.ProposalDigest {
		return plan, ErrConflict
	}
	receipt := resolution.Receipt
	if receipt.UsedAt.IsZero() && !receipt.ExpiresAt.IsZero() && !s.nowUTC().Before(receipt.ExpiresAt) {
		return s.reissueAutoplanHILApproval(ctx, plan)
	}
	if receipt.UsedAt.IsZero() {
		receipt, err = s.hil.ConsumeGatewayDecision(ctx, hil.GatewayReceiptConsumeInput{
			TenantID: plan.TenantID, IdempotencyKey: autoplanHILIdempotencyKey(plan),
			Actor: hil.ActorRef{Kind: hil.ActorService, ID: autoplanHILRequesterID}, Resource: autoplanHILResource(plan),
			ArgsHash: plan.ProposalDigest,
		})
		if err != nil {
			return plan, err
		}
	}
	if receipt.ID != plan.HILReceiptID || receipt.UsedAt.IsZero() {
		return plan, ErrConflict
	}
	if !plan.HILReceiptConsumedAt.IsZero() {
		return plan, nil
	}
	next := plan
	next.HILReceiptConsumedAt = receipt.UsedAt.UTC()
	updated, current, err := s.repository.CompareAndSaveAutoplan(ctx, next, plan.Version, s.nowUTC())
	if err != nil {
		return plan, err
	}
	if !updated {
		return current, nil
	}
	return current, nil
}

// reissueAutoplanHILApproval starts a new, durably versioned approval attempt
// only after a previously approved receipt expired before it could authorize
// task creation. The old HIL decision remains auditable, but cannot be reused
// as authority for the new attempt.
func (s *AutoplanService) reissueAutoplanHILApproval(ctx context.Context, plan AutoplanRecord) (AutoplanRecord, error) {
	if plan.Status != AutoplanStatusApproved || plan.HILDecisionID == "" || !plan.HILReceiptConsumedAt.IsZero() {
		return plan, ErrConflict
	}
	now := s.nowUTC()
	next := plan
	next.Status = AutoplanStatusPendingApproval
	next.ApprovedProposalDigest = ""
	next.ApprovalAttempt++
	next.ApprovalRequestedAt = now
	next.ApprovalID = autoplanApprovalID(next)
	next.HILDecisionID = ""
	next.HILReceiptID = ""
	next.HILReceiptConsumedAt = time.Time{}
	updated, current, err := s.repository.CompareAndSaveAutoplan(ctx, next, plan.Version, now)
	if err != nil {
		return plan, err
	}
	if !updated {
		return current, nil
	}
	return s.ensureAutoplanHILDecision(ctx, current)
}

func (s *AutoplanService) rejectAutoplan(ctx context.Context, plan AutoplanRecord, reason string) (AutoplanRecord, bool, error) {
	if plan.Status != AutoplanStatusPendingApproval && plan.Status != AutoplanStatusApproved {
		return plan, false, ErrConflict
	}
	next := plan
	next.Status = AutoplanStatusRejected
	next.RejectionReason = firstNonBlankAutoplan(reason, "HIL approval was not available")
	next.FailureReason = ""
	updated, current, err := s.repository.CompareAndSaveAutoplan(ctx, next, plan.Version, s.nowUTC())
	if err != nil {
		return plan, false, err
	}
	return current, updated, nil
}

func (s *AutoplanService) retryPlanning(ctx context.Context, plan AutoplanRecord) (AutoplanRecord, error) {
	if plan.Status != AutoplanStatusFailed {
		return plan, nil
	}
	policy, err := s.repository.GetPolicy(ctx, plan.TenantID, plan.AgentProfileID, WorkKindMissionTask)
	if err != nil {
		return plan, err
	}
	current, updated, err := s.repository.RetryAutoplanPlanning(ctx, plan, AutoplanPolicyFromResponsibility(policy).MaxOpenPlans, s.nowUTC())
	if err != nil || !updated {
		return current, err
	}
	return current, nil
}

func (s *AutoplanService) failPlanning(ctx context.Context, plan AutoplanRecord, cause error) (AutoplanRecord, error) {
	if plan.Status != AutoplanStatusPlanning {
		return plan, nil
	}
	next := plan
	next.Status = AutoplanStatusFailed
	next.FailureReason = strings.TrimSpace(cause.Error())
	next.PlanningLeaseOwner = ""
	next.PlanningLeaseExpiresAt = time.Time{}
	next.NextPlanningAttemptAt = s.nowUTC().Add(autoplanPlanningRetryDelay(plan.PlanningAttempt))
	updated, current, err := s.repository.CompareAndSaveAutoplan(ctx, next, plan.Version, s.nowUTC())
	if err != nil || !updated {
		return current, err
	}
	return current, nil
}

func autoplanPlanningRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := autoplanPlanningRetryMin
	for retry := 1; retry < attempt && delay < autoplanPlanningRetryMax; retry++ {
		delay *= 2
	}
	if delay > autoplanPlanningRetryMax {
		return autoplanPlanningRetryMax
	}
	return delay
}

func (s *AutoplanService) failProposedAutoplan(ctx context.Context, plan AutoplanRecord, cause error) (AutoplanRecord, error) {
	if plan.Status != AutoplanStatusProposed {
		return plan, nil
	}
	next := plan
	next.Status = AutoplanStatusFailed
	next.FailureReason = strings.TrimSpace(cause.Error())
	next.NextPlanningAttemptAt = s.nowUTC().Add(autoplanPlanningRetryDelay(plan.PlanningAttempt))
	updated, current, err := s.repository.CompareAndSaveAutoplan(ctx, next, plan.Version, s.nowUTC())
	if err != nil || !updated {
		return current, err
	}
	return current, nil
}

func autoplanHILResolutionFromDecision(resolution hil.DecisionResolution) (AutoplanHILResolution, error) {
	decision := resolution.Decision
	if decision.Action != AutoplanHILAction || (decision.Status != hil.DecisionResolved && decision.Status != hil.DecisionDenied) {
		return AutoplanHILResolution{}, ErrConflict
	}
	payload := decision.PayloadPreview
	planID, _ := payload["autoplan_id"].(string)
	digest, _ := payload["proposal_digest"].(string)
	approved := decision.Status == hil.DecisionResolved && resolution.Receipt.Effect == hil.EffectAuthorize
	denied := decision.Status == hil.DecisionDenied && resolution.Receipt.Effect == hil.EffectDeny
	if !approved && !denied || strings.TrimSpace(planID) == "" || strings.TrimSpace(digest) == "" ||
		strings.TrimSpace(decision.ArgsHash) != strings.TrimSpace(digest) || strings.TrimSpace(resolution.Receipt.ArgsHash) != strings.TrimSpace(digest) {
		return AutoplanHILResolution{}, ErrConflict
	}
	return AutoplanHILResolution{
		TenantID: decision.TenantID, AutoplanID: planID, ProposalDigest: digest, DecisionID: decision.ID,
		ReceiptID: resolution.Receipt.ID, Approved: approved, Reason: resolution.Reason, ResolvedAt: resolution.Receipt.CreatedAt,
	}, nil
}

func (resolution AutoplanHILResolution) validate() error {
	if resolution.TenantID == "" || resolution.AutoplanID == "" || resolution.ProposalDigest == "" || resolution.DecisionID == "" || resolution.ReceiptID == "" {
		return ErrInvalidContract
	}
	return nil
}

func normalizeAutoplanHILResolution(value AutoplanHILResolution) AutoplanHILResolution {
	value.TenantID = strings.TrimSpace(value.TenantID)
	value.AutoplanID = strings.TrimSpace(value.AutoplanID)
	value.ProposalDigest = strings.TrimSpace(value.ProposalDigest)
	value.DecisionID = strings.TrimSpace(value.DecisionID)
	value.ReceiptID = strings.TrimSpace(value.ReceiptID)
	value.Reason = strings.TrimSpace(value.Reason)
	if !value.ResolvedAt.IsZero() {
		value.ResolvedAt = value.ResolvedAt.UTC()
	}
	return value
}

func normalizeAutoplanPortfolioRequest(value AutoplanPortfolioRequest) AutoplanPortfolioRequest {
	value.TenantID = strings.TrimSpace(value.TenantID)
	value.ScheduledTaskID = strings.TrimSpace(value.ScheduledTaskID)
	value.ScheduledRunID = strings.TrimSpace(value.ScheduledRunID)
	value.ApprovalAssigneeID = strings.TrimSpace(value.ApprovalAssigneeID)
	if !value.TriggeredAt.IsZero() {
		value.TriggeredAt = value.TriggeredAt.UTC()
	}
	return value
}

func validateAutoplanPortfolioRequest(value AutoplanPortfolioRequest) error {
	portfolio := value.Portfolio
	if value.TenantID == "" || portfolio.ID == "" || portfolio.TenantID != value.TenantID || portfolio.BindingID == "" ||
		portfolio.AgentProfileID == "" || portfolio.SourceDigest == "" || portfolio.Status != PeoplePortfolioStatusActive {
		return ErrInvalidContract
	}
	return nil
}

func autoplanRequestForResponsibility(input AutoplanPortfolioRequest, policy ResponsibilityPolicyRecord, item PeopleResolvedResponsibility, goalContext AutoplanGoalContext, cycle int) AutoplanRequest {
	goalDigest, _ := AutoplanGoalContextDigest(goalContext)
	return AutoplanRequest{
		TenantID: input.TenantID, PortfolioID: input.Portfolio.ID, PortfolioBindingID: input.Portfolio.BindingID,
		PortfolioVersionID: PeopleResponsibilityPortfolioVersionID(input.Portfolio.BindingID, input.Portfolio.SourceDigest),
		SourceRevision:     input.Portfolio.SourceRevision, SourceDigest: input.Portfolio.SourceDigest,
		GoalContextID: goalContext.ContextID, GoalContextDigest: goalDigest,
		AgentProfileID: input.Portfolio.AgentProfileID, ActorMemberID: input.ApprovalAssigneeID,
		PolicyID: policy.ID, PolicyVersion: policy.Version, PlanningCycle: cycle, TriggerKind: "responsibility_pulse",
		TriggerRef: responsibilityAutoplanTriggerRef(item), ScheduledTaskID: input.ScheduledTaskID,
		ScheduledRunID: input.ScheduledRunID, TriggeredAt: input.TriggeredAt,
	}
}

// autoplanPlanningCycle reuses the latest plan for the same responsibility
// snapshot. A materialized plan keeps its cycle until a changed source or
// policy snapshot provides a new planning input.
func autoplanPlanningCycle(plans []AutoplanRecord, base AutoplanRequest) (int, *AutoplanRecord) {
	base = base.Normalized()
	var latest *AutoplanRecord
	for index := range plans {
		plan := plans[index]
		if !autoplanSnapshotMatches(plan, base) {
			continue
		}
		if latest == nil || plan.PlanningCycle > latest.PlanningCycle || (plan.PlanningCycle == latest.PlanningCycle && plan.UpdatedAt.After(latest.UpdatedAt)) {
			copy := plan
			latest = &copy
		}
	}
	if latest == nil {
		return 1, nil
	}
	return latest.PlanningCycle, latest
}

func autoplanSnapshotMatches(plan AutoplanRecord, request AutoplanRequest) bool {
	request = request.Normalized()
	return plan.TenantID == request.TenantID && plan.PortfolioID == request.PortfolioID &&
		plan.PortfolioBindingID == request.PortfolioBindingID && plan.PortfolioVersionID == request.PortfolioVersionID &&
		plan.SourceRevision == request.SourceRevision && plan.SourceDigest == request.SourceDigest &&
		plan.GoalContextDigest == request.GoalContextDigest &&
		plan.AgentProfileID == request.AgentProfileID &&
		plan.PolicyID == request.PolicyID && plan.PolicyVersion == request.PolicyVersion &&
		plan.TriggerKind == request.TriggerKind && plan.TriggerRef == request.TriggerRef
}

// reassignAutoplanAssignee is only permitted before a HIL decision exists.
// Once a fixed approval is pending or approved, EvaluatePortfolio supersedes
// that immutable authorization and creates a successor cycle instead.
func (s *AutoplanService) reassignAutoplanAssignee(ctx context.Context, plan AutoplanRecord, assigneeID string) (AutoplanRecord, bool, error) {
	if plan.Status != AutoplanStatusPlanning && plan.Status != AutoplanStatusProposed && plan.Status != AutoplanStatusFailed {
		return plan, false, ErrConflict
	}
	next := plan
	next.ActorMemberID = strings.TrimSpace(assigneeID)
	updated, current, err := s.repository.CompareAndSaveAutoplan(ctx, next, plan.Version, s.nowUTC())
	if err != nil {
		return plan, false, err
	}
	return current, updated, nil
}

func autoplanStatusOpen(status AutoplanStatus) bool {
	switch status {
	case AutoplanStatusPlanning, AutoplanStatusProposed, AutoplanStatusPendingApproval, AutoplanStatusApproved, AutoplanStatusMaterializing, AutoplanStatusSuperseding:
		return true
	default:
		return false
	}
}

func responsibilityAutoplanTriggerRef(item PeopleResolvedResponsibility) string {
	return "people:responsibility:" + strings.TrimSpace(item.ID) + ":v" + fmt.Sprint(item.Revision)
}

func (s *AutoplanService) resolveAutoplanGoalContext(ctx context.Context, portfolio PeopleResponsibilityPortfolio, item PeopleResolvedResponsibility) (AutoplanGoalContext, error) {
	if s == nil || s.goalContext == nil {
		return AutoplanGoalContext{}, fmt.Errorf("%w: autonomous planning goal context provider is not configured", ErrInvalidContract)
	}
	request := ContextRequest{
		TenantID: portfolio.TenantID, ActorMemberID: portfolio.SubjectID, AgentProfileID: portfolio.AgentProfileID,
		ResponsibilityID: item.ID, AssignmentVersion: item.Revision,
	}
	goalContext, err := s.goalContext.ResolveAutoplanGoalContext(ctx, request)
	if err != nil {
		return AutoplanGoalContext{}, fmt.Errorf("resolve autonomous planning goals: %w", err)
	}
	goalContext = goalContext.Normalized()
	if err := goalContext.Validate(request); err != nil {
		return AutoplanGoalContext{}, err
	}
	return goalContext, nil
}

func autoplanOpenStatuses() []AutoplanStatus {
	return []AutoplanStatus{AutoplanStatusPlanning, AutoplanStatusProposed, AutoplanStatusPendingApproval, AutoplanStatusApproved, AutoplanStatusMaterializing, AutoplanStatusSuperseding}
}

func autoplanApprovalID(plan AutoplanRecord) string {
	return "responsibility-autoplan-approval-" + autoplanHash(plan.ID, plan.ProposalDigest, fmt.Sprint(plan.ApprovalAttempt))[:24]
}

func autoplanHILIdempotencyKey(plan AutoplanRecord) string {
	return "responsibility-autoplan:" + plan.TenantID + ":" + plan.ID + ":" + plan.ProposalDigest + ":" + fmt.Sprint(plan.ApprovalAttempt)
}

func autoplanHILExpiresAt(plan AutoplanRecord) time.Time {
	requestedAt := plan.ApprovalRequestedAt
	if requestedAt.IsZero() {
		requestedAt = plan.CreatedAt
	}
	if requestedAt.IsZero() {
		return time.Time{}
	}
	return requestedAt.UTC().Add(autoplanHILExpiry)
}

func autoplanHILResource(plan AutoplanRecord) hil.ObjectRef {
	return hil.ObjectRef{Kind: autoplanHILResourceKind, ID: plan.ID}
}

func autoplanHILPayload(plan AutoplanRecord) map[string]any {
	proposal := plan.Proposal.Normalized()
	display := map[string]any{
		"summary":            proposal.Summary,
		"business_context":   proposal.Rationale,
		"approval_summary":   "是否同意按这份职责计划继续执行？",
		"impact_scope":       fmt.Sprintf("同意后系统将创建并推进 %d 项工作；拒绝后不会执行。", len(proposal.Steps)),
		"risk_summary":       append([]string(nil), proposal.RiskSummary...),
		"required_approvals": append([]string(nil), proposal.RequiredApprovals...),
		"steps":              autoplanHILStepPreviews(proposal),
	}
	payload := map[string]any{
		"source": "responsibility_autoplan", "autoplan_id": plan.ID, "proposal_digest": plan.ProposalDigest,
		"portfolio_id": plan.PortfolioID, "portfolio_binding_id": plan.PortfolioBindingID,
		"portfolio_version_id": plan.PortfolioVersionID, "agent_profile_id": plan.AgentProfileID,
		"goal_context_id": plan.GoalContextID, "goal_context_digest": plan.GoalContextDigest,
		"policy_id": plan.PolicyID, "policy_version": plan.PolicyVersion,
		"summary": proposal.Summary, "source_refs": append([]string(nil), proposal.SourceRefs...),
		"steps":                       autoplanHILStepPreviews(proposal),
		hil.ApprovalDisplayPayloadKey: display,
	}
	if actor := strings.TrimSpace(plan.ActorMemberID); actor != "" {
		payload["approval_assigned_to"] = actor
	}
	return payload
}

func autoplanHILStepPreviews(proposal AutoplanProposal) []map[string]any {
	previews := make([]map[string]any, 0, len(proposal.Steps))
	for _, step := range proposal.Steps {
		preview := map[string]any{
			"step_id": step.StepID, "title": step.Title, "parent_goal_ref": step.ParentGoalRef,
			"parent_goal": step.ParentGoal, "goal": step.Goal,
			"assignee_agent_id": step.AssigneeAgentID, "risk_level": step.RiskLevel,
			"has_side_effect": step.HasSideEffect, "execution_scopes": append([]string(nil), step.ExecutionScopes...),
			"depends_on": append([]string(nil), step.DependsOn...),
		}
		if !step.DueAt.IsZero() {
			preview["due_at"] = step.DueAt.UTC().Format(time.RFC3339)
		}
		previews = append(previews, preview)
	}
	return previews
}

func autoplanProposalRisk(proposal AutoplanProposal) hil.RiskLevel {
	risk := hil.RiskLow
	for _, step := range proposal.Steps {
		switch step.RiskLevel {
		case AutoplanRiskCritical:
			return hil.RiskCritical
		case AutoplanRiskHigh:
			risk = hil.RiskHigh
		case AutoplanRiskMedium:
			if risk == hil.RiskLow {
				risk = hil.RiskMedium
			}
		}
	}
	return risk
}

func appendAutoplanID(values []string, id string) []string {
	id = strings.TrimSpace(id)
	if id == "" {
		return values
	}
	for _, existing := range values {
		if existing == id {
			return values
		}
	}
	return append(values, id)
}

func firstAutoplanID(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func firstNonBlankAutoplan(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func autoplanEvaluationReason(result AutoplanEvaluationResult) string {
	switch {
	case result.PendingApproval > 0:
		return "autoplan_pending_approval"
	case result.Approved > 0:
		return "autoplan_approved_for_materialization"
	case result.Proposed > 0:
		return "autoplan_proposed"
	case result.NotActionable > 0:
		return "autoplan_not_actionable"
	case result.Rejected > 0:
		return "autoplan_rejected_by_policy"
	case result.CapacityBlocked > 0:
		return "autoplan_capacity_limited"
	case result.WaitingForSignal > 0:
		return "autoplan_waiting_for_new_signal"
	case result.Reused > 0:
		return "autoplan_existing_plan_reused"
	default:
		return "autoplan_no_action"
	}
}

func (s *AutoplanService) nowUTC() time.Time {
	if s == nil || s.now == nil {
		return time.Now().UTC()
	}
	return s.now().UTC()
}

// BaselineAutoplanPlanner is intentionally deterministic and read-only. It
// creates the smallest evidence-backed assessment from one responsibility
// source and derives only the evidence-read scopes authorized by that
// responsibility policy.
type BaselineAutoplanPlanner struct{}

func (BaselineAutoplanPlanner) PlanAutoplan(_ context.Context, request AutoplanPlanningRequest) (AutoplanProposal, error) {
	item := request.Responsibility
	if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Title) == "" || strings.TrimSpace(request.PlanRequest.AgentProfileID) == "" {
		return AutoplanProposal{Version: AutoplanProposalVersion, NoActionReason: "responsibility lacks a concrete title or accountable Agent"}, nil
	}
	goalContext := request.GoalContext.Normalized()
	if len(goalContext.Goals) == 0 {
		return AutoplanProposal{
			Version: AutoplanProposalVersion, SourceRefs: responsibilityAutoplanAuthorizedSourceRefs(request.Portfolio, item, goalContext),
			NoActionReason: "responsibility has no explicit existing goal from which work can be decomposed",
		}, nil
	}
	executionScopes := baselineAutoplanEvidenceReadScopes(request.Policy)
	if len(executionScopes) == 0 {
		return AutoplanProposal{
			Version:        AutoplanProposalVersion,
			SourceRefs:     responsibilityAutoplanSourceRefs(request.Portfolio, item),
			NoActionReason: "responsibility policy has no authorized evidence-read scope for a traceable assessment",
		}, nil
	}
	parentGoal := goalContext.Goals[0]
	title := "Assess goal progress: " + strings.TrimSpace(item.Title)
	goal := "Assess current evidence against the existing goal: " + parentGoal.Goal + ". Record the smallest responsibility-scoped improvement. Do not perform external changes or operational actions."
	criterion := "A traceable read-only gap assessment is recorded for existing goal " + parentGoal.Ref
	stepID := "step-" + autoplanHash(item.ID, fmt.Sprint(item.Revision))[:16]
	evidenceID := "evidence-" + autoplanHash(stepID, criterion)[:16]
	description := strings.TrimSpace(item.Description)
	if description == "" {
		description = goal
	}
	description = strings.TrimSpace(truncateRunes(description, 1200))
	return AutoplanProposal{
		Version: AutoplanProposalVersion, Summary: title,
		Rationale:   "The active People responsibility has no concrete Mission Task and is explicitly enabled for continuous autonomy. The baseline task uses only responsibility-authorized evidence reads.",
		RiskSummary: []string{"read_only", "responsibility_scoped"},
		SourceRefs:  responsibilityAutoplanAuthorizedSourceRefs(request.Portfolio, item, goalContext),
		Steps: []AutoplanStep{{
			StepID: stepID, Title: title, Description: description, Goal: goal,
			ParentGoalRef: parentGoal.Ref, ParentGoal: parentGoal.Goal,
			AssigneeAgentID: request.PlanRequest.AgentProfileID, Priority: 50, DueAt: responsibilityAutoplanDueAt(item),
			AcceptanceCriteria: []string{criterion},
			EvidenceRequirements: []AutoplanEvidenceRequirement{{
				EvidenceRequirementID: evidenceID, AcceptanceCriterion: criterion, Kind: "tool_log", Required: true,
			}},
			ExecutionScopes: executionScopes,
			RiskLevel:       AutoplanRiskLow,
		}},
	}, nil
}

func baselineAutoplanEvidenceReadScopes(policy AutoplanPolicy) []string {
	allowed := policy.Normalized().AllowedExecutionScopes
	candidates := []string{"enterprise_graph.read", "knowledge.read", "memory.read", "scheduler.read"}
	derived := make([]string, 0, len(candidates))
	for _, scope := range candidates {
		if policyScopeAllows(allowed, scope) {
			derived = append(derived, scope)
		}
	}
	return cleanAutoplanStrings(derived)
}

func responsibilityAutoplanSourceRefs(portfolio PeopleResponsibilityPortfolio, item PeopleResolvedResponsibility) []string {
	refs := []string{
		"people:subject:" + strings.TrimSpace(portfolio.SubjectID),
		"people:source-revision:" + strings.TrimSpace(portfolio.SourceRevision),
		responsibilityAutoplanTriggerRef(item),
	}
	for _, source := range item.Sources {
		bindingID := firstNonBlankAutoplan(source.BindingID, source.ID)
		if bindingID != "" {
			refs = append(refs, "people:responsibility-binding:"+bindingID+":v"+fmt.Sprint(source.Revision))
		}
	}
	return cleanAutoplanStrings(refs)
}

func responsibilityAutoplanAuthorizedSourceRefs(portfolio PeopleResponsibilityPortfolio, item PeopleResolvedResponsibility, goalContext AutoplanGoalContext) []string {
	return cleanAutoplanStrings(append(responsibilityAutoplanSourceRefs(portfolio, item), goalContext.Normalized().SourceRefs...))
}

func responsibilityAutoplanDueAt(item PeopleResolvedResponsibility) time.Time {
	if item.ValidTo == nil || item.ValidTo.IsZero() {
		return time.Time{}
	}
	return item.ValidTo.UTC()
}
