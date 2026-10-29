package responsibility

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/uow"
)

const (
	autoplanExecutionGrantTable      = "responsibility_autoplan_execution_grants"
	autoplanExecutionGrantSourceKind = "responsibility_autoplan"
)

// AutoplanExecutionGrant is the server-issued projection of a consumed HIL
// receipt onto one already-created responsibility assignment. It is distinct
// from a Task source reference: the repository writes it only after checking
// the task assignment, current policy, approved proposal, and consumed HIL
// receipt in one transaction.
type AutoplanExecutionGrant struct {
	ID                   string    `json:"id"`
	TenantID             string    `json:"tenant_id"`
	ResponsibilityID     string    `json:"responsibility_id"`
	MissionID            string    `json:"mission_id"`
	TaskID               string    `json:"task_id"`
	AutoplanID           string    `json:"autoplan_id"`
	StepID               string    `json:"step_id"`
	ProposalDigest       string    `json:"proposal_digest"`
	HILDecisionID        string    `json:"hil_decision_id"`
	HILReceiptID         string    `json:"hil_receipt_id"`
	HILReceiptConsumedAt time.Time `json:"hil_receipt_consumed_at"`
	AgentProfileID       string    `json:"agent_profile_id"`
	PolicyID             string    `json:"policy_id"`
	PolicyVersion        int64     `json:"policy_version"`
	ScopeDigest          string    `json:"scope_digest"`
	CreatedAt            time.Time `json:"created_at"`
}

func AutoplanExecutionGrantID(tenantID, responsibilityID, planID, stepID, receiptID string) string {
	return "responsibility-autoplan-grant-" + stableHex(
		strings.TrimSpace(tenantID),
		strings.TrimSpace(responsibilityID),
		strings.TrimSpace(planID),
		strings.TrimSpace(stepID),
		strings.TrimSpace(receiptID),
	)[:24]
}

func (grant AutoplanExecutionGrant) Validate() error {
	if strings.TrimSpace(grant.ID) == "" || strings.TrimSpace(grant.TenantID) == "" || strings.TrimSpace(grant.ResponsibilityID) == "" ||
		strings.TrimSpace(grant.MissionID) == "" || strings.TrimSpace(grant.TaskID) == "" || strings.TrimSpace(grant.AutoplanID) == "" ||
		strings.TrimSpace(grant.StepID) == "" || strings.TrimSpace(grant.ProposalDigest) == "" || strings.TrimSpace(grant.HILDecisionID) == "" ||
		strings.TrimSpace(grant.HILReceiptID) == "" || strings.TrimSpace(grant.AgentProfileID) == "" || strings.TrimSpace(grant.PolicyID) == "" ||
		strings.TrimSpace(grant.ScopeDigest) == "" || grant.PolicyVersion < 1 || grant.HILReceiptConsumedAt.IsZero() || grant.CreatedAt.IsZero() {
		return fmt.Errorf("%w: autonomous-plan execution grant is incomplete", ErrInvalidContract)
	}
	wantID := AutoplanExecutionGrantID(grant.TenantID, grant.ResponsibilityID, grant.AutoplanID, grant.StepID, grant.HILReceiptID)
	if grant.ID != wantID {
		return fmt.Errorf("%w: autonomous-plan execution grant ID does not match its immutable binding", ErrInvalidContract)
	}
	return nil
}

// GrantAutoplanExecution projects an already-consumed HIL receipt into the
// existing responsibility approval boundary. It never trusts Task source
// metadata alone: the plan, materialization receipt, responsibility assignment
// and current policy must all agree before the grant is stored.
func (r *Repository) GrantAutoplanExecution(ctx context.Context, plan AutoplanRecord, materialization AutoplanMaterialization, now time.Time) (AutoplanExecutionGrant, error) {
	if r == nil || r.unit == nil || now.IsZero() {
		return AutoplanExecutionGrant{}, ErrInvalidContract
	}
	plan = normalizeAutoplanRecord(plan)
	if strings.TrimSpace(plan.ID) == "" {
		return AutoplanExecutionGrant{}, ErrInvalidContract
	}
	var out AutoplanExecutionGrant
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, autoplanTable, plan.ID); err != nil {
			return err
		}
		storedPlan, found, err := getAutoplanExecutionGrantPlanTx(tx, plan.ID)
		if err != nil {
			return err
		}
		if !found {
			return ErrNotFound
		}
		if !autoplanExecutionGrantPlanMatches(storedPlan, plan) {
			return ErrConflict
		}
		grant, err := newAutoplanExecutionGrant(storedPlan, materialization, now.UTC())
		if err != nil {
			return err
		}
		if err := lockResponsibilityAggregateTx(ctx, tx, grant.ResponsibilityID); err != nil {
			return err
		}
		assignment, found, err := getAssignmentTx(ctx, tx, grant.ResponsibilityID)
		if err != nil {
			return err
		}
		if !found || assignmentTerminal(assignment.State) || assignment.TenantID != grant.TenantID || assignment.MissionID != grant.MissionID ||
			assignment.WorkID != grant.TaskID || assignment.AccountableAgent != grant.AgentProfileID || assignment.SourceKind != autoplanExecutionGrantSourceKind ||
			assignment.SourceID != grant.AutoplanID || assignment.SourceStepID != grant.StepID {
			return ErrConflict
		}
		policy, err := resolvePolicyTx(tx, assignment)
		if err != nil {
			return err
		}
		if policy.ID != grant.PolicyID || policy.Version != grant.PolicyVersion {
			return ErrConflict
		}
		if stored, found, err := getAutoplanExecutionGrantTx(tx, grant.ID); err != nil {
			return err
		} else if found {
			if !autoplanExecutionGrantEqual(stored, grant) {
				return ErrConflict
			}
			out = stored
		} else {
			if err := putRecordTx(ctx, tx, autoplanExecutionGrantTable, grant.ID, grant); err != nil {
				return err
			}
			if err := putAuditTx(ctx, tx, AuditRecord{
				TenantID: grant.TenantID, ResponsibilityID: grant.ResponsibilityID,
				EntityType: "autoplan_execution_grant", EntityID: grant.ID, Action: AuditAutoplanExecutionGranted,
				ActorID: "hil:" + grant.HILDecisionID, ToState: "granted",
				Details: "HIL receipt " + grant.HILReceiptID + " authorized autonomous-plan execution", OccurredAt: grant.CreatedAt,
			}); err != nil {
				return err
			}
			out = grant
		}
		return resolveAutoplanExecutionApprovalBlockerTx(ctx, tx, assignment, grant, now.UTC())
	})
	return out, err
}

func getAutoplanExecutionGrantPlanTx(tx uow.Tx, planID string) (AutoplanRecord, bool, error) {
	record, found := tx.Get(autoplanTable, strings.TrimSpace(planID))
	if !found {
		return AutoplanRecord{}, false, nil
	}
	var plan AutoplanRecord
	if err := decodeRecord(record, &plan); err != nil {
		return AutoplanRecord{}, false, err
	}
	plan = normalizeAutoplanRecord(plan)
	if err := plan.Validate(); err != nil {
		return AutoplanRecord{}, false, err
	}
	return plan, true, nil
}

func autoplanExecutionGrantPlanMatches(stored, supplied AutoplanRecord) bool {
	stored, supplied = normalizeAutoplanRecord(stored), normalizeAutoplanRecord(supplied)
	return stored.ID == supplied.ID && stored.TenantID == supplied.TenantID && stored.Status == supplied.Status && stored.Version == supplied.Version &&
		stored.ProposalDigest == supplied.ProposalDigest && stored.ApprovedProposalDigest == supplied.ApprovedProposalDigest &&
		stored.HILDecisionID == supplied.HILDecisionID && stored.HILReceiptID == supplied.HILReceiptID &&
		stored.HILReceiptConsumedAt.Equal(supplied.HILReceiptConsumedAt) && stored.PolicyID == supplied.PolicyID &&
		stored.PolicyVersion == supplied.PolicyVersion && stored.AgentProfileID == supplied.AgentProfileID
}

func newAutoplanExecutionGrant(plan AutoplanRecord, materialization AutoplanMaterialization, now time.Time) (AutoplanExecutionGrant, error) {
	plan = normalizeAutoplanRecord(plan)
	if err := plan.Validate(); err != nil {
		return AutoplanExecutionGrant{}, err
	}
	if plan.Status != AutoplanStatusMaterializing && plan.Status != AutoplanStatusMaterialized {
		return AutoplanExecutionGrant{}, ErrConflict
	}
	if plan.ApprovedProposalDigest != plan.ProposalDigest || strings.TrimSpace(plan.HILDecisionID) == "" || strings.TrimSpace(plan.HILReceiptID) == "" || plan.HILReceiptConsumedAt.IsZero() {
		return AutoplanExecutionGrant{}, ErrConflict
	}
	if err := materialization.Validate(); err != nil || materialization.ResponsibilityID != AssignmentID(plan.TenantID, WorkKindMissionTask, materialization.TaskID) {
		return AutoplanExecutionGrant{}, ErrInvalidContract
	}
	proposal := plan.Proposal.Normalized()
	digest, err := AutoplanProposalDigest(proposal)
	if err != nil || digest != plan.ProposalDigest {
		return AutoplanExecutionGrant{}, ErrConflict
	}
	var step *AutoplanStep
	for index := range proposal.Steps {
		if proposal.Steps[index].StepID == materialization.StepID {
			step = &proposal.Steps[index]
			break
		}
	}
	if step == nil || step.AssigneeAgentID != plan.AgentProfileID {
		return AutoplanExecutionGrant{}, ErrConflict
	}
	grant := AutoplanExecutionGrant{
		TenantID: plan.TenantID, ResponsibilityID: materialization.ResponsibilityID,
		MissionID: materialization.MissionID, TaskID: materialization.TaskID,
		AutoplanID: plan.ID, StepID: step.StepID, ProposalDigest: plan.ProposalDigest,
		HILDecisionID: plan.HILDecisionID, HILReceiptID: plan.HILReceiptID, HILReceiptConsumedAt: plan.HILReceiptConsumedAt.UTC(),
		AgentProfileID: step.AssigneeAgentID, PolicyID: plan.PolicyID, PolicyVersion: plan.PolicyVersion,
		ScopeDigest: approvalScopeDigest(step.ExecutionScopes), CreatedAt: now.UTC(),
	}
	grant.ID = AutoplanExecutionGrantID(grant.TenantID, grant.ResponsibilityID, grant.AutoplanID, grant.StepID, grant.HILReceiptID)
	if err := grant.Validate(); err != nil {
		return AutoplanExecutionGrant{}, err
	}
	return grant, nil
}

func getAutoplanExecutionGrantTx(tx uow.Tx, id string) (AutoplanExecutionGrant, bool, error) {
	record, found := tx.Get(autoplanExecutionGrantTable, id)
	if !found {
		return AutoplanExecutionGrant{}, false, nil
	}
	var grant AutoplanExecutionGrant
	if err := decodeRecord(record, &grant); err != nil {
		return AutoplanExecutionGrant{}, false, err
	}
	if err := grant.Validate(); err != nil {
		return AutoplanExecutionGrant{}, false, err
	}
	return grant, true, nil
}

func autoplanExecutionGrantEqual(left, right AutoplanExecutionGrant) bool {
	return left.ID == right.ID && left.TenantID == right.TenantID && left.ResponsibilityID == right.ResponsibilityID &&
		left.MissionID == right.MissionID && left.TaskID == right.TaskID && left.AutoplanID == right.AutoplanID &&
		left.StepID == right.StepID && left.ProposalDigest == right.ProposalDigest && left.HILDecisionID == right.HILDecisionID &&
		left.HILReceiptID == right.HILReceiptID && left.HILReceiptConsumedAt.Equal(right.HILReceiptConsumedAt) &&
		left.AgentProfileID == right.AgentProfileID && left.PolicyID == right.PolicyID && left.PolicyVersion == right.PolicyVersion &&
		left.ScopeDigest == right.ScopeDigest
}

func resolveAutoplanExecutionApprovalBlockerTx(ctx context.Context, tx uow.Tx, assignment AssignmentRecord, grant AutoplanExecutionGrant, now time.Time) error {
	blockerID := BlockerID(grant.TenantID, grant.ResponsibilityID, "dispatch-policy-approval")
	blocker, found, err := getBlockerTx(tx, blockerID)
	if err != nil || !found || blocker.Status != BlockerStatusOpen {
		return err
	}
	if blocker.Kind != BlockerKindApproval || blocker.TenantID != grant.TenantID || blocker.ResponsibilityID != grant.ResponsibilityID || blocker.DedupeKey != "dispatch-policy-approval" {
		return ErrConflict
	}
	blocker.Status = BlockerStatusResolved
	blocker.Resolution = "HIL decision " + grant.HILDecisionID + " receipt " + grant.HILReceiptID + " authorized autonomous-plan execution"
	blocker.ResolvedBy = "hil:" + grant.HILDecisionID
	blocker.ResolvedAt = now
	blocker.UpdatedAt = now
	blocker.Version++
	blocker.ApprovalAgentProfileID = grant.AgentProfileID
	blocker.ApprovalPolicyID = grant.PolicyID
	blocker.ApprovalPolicyVersion = grant.PolicyVersion
	blocker.ApprovalScopeDigest = grant.ScopeDigest
	if err := putRecordTx(ctx, tx, blockerTable, blocker.ID, blocker); err != nil {
		return err
	}
	hasOpenBlocker, err := hasOpenBlockerTx(ctx, tx, assignment.ID)
	if err != nil {
		return err
	}
	if hasOpenBlocker {
		if err := putAssignmentTx(ctx, tx, assignment); err != nil {
			return err
		}
	} else if assignment.State == AssignmentStateOffered {
		if _, found, err := findActivationForVersionTx(ctx, tx, assignment.ID, assignment.Version); err != nil || !found {
			if err != nil {
				return err
			}
			return ErrConflict
		}
	} else {
		assignment.Paused = false
		assignment.PauseReason = ""
		if assignment.State == AssignmentStateWaiting || assignment.State == AssignmentStateEscalated {
			assignment.State = AssignmentStateActive
		}
		if _, err := createActivationTx(ctx, tx, &assignment, ActivationCauseBlockerResolved, now, now); err != nil {
			return err
		}
		if err := putAssignmentTx(ctx, tx, assignment); err != nil {
			return err
		}
	}
	return putAuditTx(ctx, tx, AuditRecord{
		TenantID: grant.TenantID, ResponsibilityID: grant.ResponsibilityID,
		EntityType: "blocker", EntityID: blocker.ID, Action: AuditBlockerResolved,
		ActorID: blocker.ResolvedBy, FromState: string(BlockerStatusOpen), ToState: string(BlockerStatusResolved), Details: blocker.Resolution, OccurredAt: now,
	})
}

func autoplanExecutionGrantApprovedTx(ctx context.Context, tx uow.Tx, assignment AssignmentRecord, policy ResponsibilityPolicyRecord, executionScopes []string) (bool, error) {
	if assignment.SourceKind != autoplanExecutionGrantSourceKind || strings.TrimSpace(assignment.SourceID) == "" || strings.TrimSpace(assignment.SourceStepID) == "" {
		return false, nil
	}
	_, granted, err := autoplanExecutionGrantForScopeDigestTx(ctx, tx, assignment, policy, approvalScopeDigest(executionScopes))
	return granted, err
}

// AutoplanDispatchAuthorized verifies the durable plan that created an
// autonomous Task before the responsibility coordinator can dispatch it. HIL
// plans require their exact execution grant; low-risk plans without HIL remain
// eligible after their persisted plan binding is verified.
func (r *Repository) AutoplanDispatchAuthorized(ctx context.Context, assignment AssignmentRecord, policy ResponsibilityPolicyRecord, executionScopes []string) (bool, error) {
	if r == nil || r.unit == nil || !isAutoplanExecutionAssignment(assignment) {
		return false, ErrInvalidContract
	}
	if err := validatePolicyForAssignment(policy, assignment); err != nil {
		return false, err
	}
	authorized := false
	err := r.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		plan, found, err := getAutoplanExecutionGrantPlanTx(tx, assignment.SourceID)
		if err != nil {
			return err
		}
		if !found || !autoplanDispatchPlanMatches(plan, assignment, policy, executionScopes) {
			return nil
		}
		if strings.TrimSpace(plan.HILDecisionID) == "" {
			authorized = true
			return nil
		}
		_, authorized, err = autoplanExecutionGrantForScopeDigestTx(ctx, tx, assignment, policy, approvalScopeDigest(executionScopes))
		return err
	})
	return authorized, err
}

func isAutoplanExecutionAssignment(assignment AssignmentRecord) bool {
	return strings.EqualFold(strings.TrimSpace(assignment.SourceKind), autoplanExecutionGrantSourceKind) &&
		strings.TrimSpace(assignment.SourceID) != "" && strings.TrimSpace(assignment.SourceStepID) != ""
}

func autoplanDispatchPlanMatches(plan AutoplanRecord, assignment AssignmentRecord, policy ResponsibilityPolicyRecord, executionScopes []string) bool {
	if plan.TenantID != assignment.TenantID || plan.PolicyID != policy.ID || plan.PolicyVersion != policy.Version ||
		plan.AgentProfileID != assignment.AccountableAgent || (plan.Status != AutoplanStatusMaterializing && plan.Status != AutoplanStatusMaterialized) {
		return false
	}
	wantedScopeDigest := approvalScopeDigest(executionScopes)
	for _, step := range plan.Proposal.Steps {
		if step.StepID == assignment.SourceStepID && step.AssigneeAgentID == assignment.AccountableAgent &&
			approvalScopeDigest(step.ExecutionScopes) == wantedScopeDigest {
			return true
		}
	}
	return false
}

func autoplanExecutionGrantForScopeDigestTx(ctx context.Context, tx uow.Tx, assignment AssignmentRecord, policy ResponsibilityPolicyRecord, wantedScopeDigest string) (AutoplanExecutionGrant, bool, error) {
	if assignment.SourceKind != autoplanExecutionGrantSourceKind || strings.TrimSpace(assignment.SourceID) == "" || strings.TrimSpace(assignment.SourceStepID) == "" || strings.TrimSpace(wantedScopeDigest) == "" {
		return AutoplanExecutionGrant{}, false, nil
	}
	records, err := uow.ListRecords(ctx, tx, autoplanExecutionGrantTable)
	if err != nil {
		return AutoplanExecutionGrant{}, false, err
	}
	for _, record := range records {
		var grant AutoplanExecutionGrant
		if err := decodeRecord(record, &grant); err != nil {
			return AutoplanExecutionGrant{}, false, err
		}
		if err := grant.Validate(); err != nil {
			return AutoplanExecutionGrant{}, false, err
		}
		if grant.TenantID == assignment.TenantID && grant.ResponsibilityID == assignment.ID && grant.MissionID == assignment.MissionID &&
			grant.TaskID == assignment.WorkID && grant.AutoplanID == assignment.SourceID && grant.StepID == assignment.SourceStepID &&
			grant.AgentProfileID == assignment.AccountableAgent && grant.PolicyID == policy.ID && grant.PolicyVersion == policy.Version &&
			grant.ScopeDigest == wantedScopeDigest {
			return grant, true, nil
		}
	}
	return AutoplanExecutionGrant{}, false, nil
}
