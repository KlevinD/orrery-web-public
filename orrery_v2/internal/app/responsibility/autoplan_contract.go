package responsibility

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// AutoplanProposalVersion is the schema emitted by new planners. Version 1 is
// still accepted so durable plans created before goal anchoring can finish.
const (
	AutoplanLegacyProposalVersion = 1
	AutoplanProposalVersion       = 2
)

type AutoplanStatus string

const (
	AutoplanStatusPlanning        AutoplanStatus = "planning"
	AutoplanStatusProposed        AutoplanStatus = "proposed"
	AutoplanStatusPendingApproval AutoplanStatus = "pending_approval"
	AutoplanStatusApproved        AutoplanStatus = "approved"
	AutoplanStatusMaterializing   AutoplanStatus = "materializing"
	AutoplanStatusMaterialized    AutoplanStatus = "materialized"
	AutoplanStatusNotActionable   AutoplanStatus = "not_actionable"
	AutoplanStatusRejected        AutoplanStatus = "rejected"
	AutoplanStatusSuperseding     AutoplanStatus = "superseding"
	AutoplanStatusSuperseded      AutoplanStatus = "superseded"
	AutoplanStatusFailed          AutoplanStatus = "failed"
)

type AutoplanRiskLevel string

const (
	AutoplanRiskLow      AutoplanRiskLevel = "low"
	AutoplanRiskMedium   AutoplanRiskLevel = "medium"
	AutoplanRiskHigh     AutoplanRiskLevel = "high"
	AutoplanRiskCritical AutoplanRiskLevel = "critical"
)

// AutoplanRequest identifies a responsibility snapshot being considered for
// planning. ScheduledRunID is trace metadata only: it deliberately does not
// participate in the dedupe key, so repeated pulses do not create duplicate
// plans for unchanged source facts.
type AutoplanRequest struct {
	TenantID           string
	PortfolioID        string
	PortfolioBindingID string
	PortfolioVersionID string
	SourceRevision     string
	SourceDigest       string
	GoalContextID      string
	GoalContextDigest  string
	AgentProfileID     string
	ActorMemberID      string
	PolicyID           string
	PolicyVersion      int64
	PlanningCycle      int
	TriggerKind        string
	TriggerRef         string
	ScheduledTaskID    string
	ScheduledRunID     string
	TriggeredAt        time.Time
}

// AutoplanProposal is the planner's structured, side-effect-free answer.
// A proposal with no steps must explicitly explain why there is no actionable
// work; a proposal with steps must be fully traceable and verifiable.
type AutoplanProposal struct {
	Version           int
	Summary           string
	Rationale         string
	SourceRefs        []string
	RiskSummary       []string
	RequiredApprovals []string
	NoActionReason    string
	Steps             []AutoplanStep
}

type AutoplanStep struct {
	StepID               string
	Title                string
	Description          string
	ParentGoalRef        string
	ParentGoal           string
	Goal                 string
	AssigneeAgentID      string
	Priority             int
	DueAt                time.Time
	DependsOn            []string
	AcceptanceCriteria   []string
	EvidenceRequirements []AutoplanEvidenceRequirement
	ExecutionScopes      []string
	RiskLevel            AutoplanRiskLevel
	HasSideEffect        bool
}

type AutoplanEvidenceRequirement struct {
	EvidenceRequirementID string
	AcceptanceCriterion   string
	Kind                  string
	Required              bool
}

// AutoplanMaterialization stores the durable outcome for one planned step.
// The eventual adapter may create one Mission for many steps; therefore each
// receipt carries its own Mission ID rather than assuming a single Mission.
type AutoplanMaterialization struct {
	StepID           string
	MissionID        string
	TaskID           string
	ResponsibilityID string
	MaterializedAt   time.Time
}

// AutoplanRecord is the durable aggregate shape used by a future repository.
// This file intentionally contains no persistence or transport code.
type AutoplanRecord struct {
	ID                     string
	TenantID               string
	PortfolioID            string
	PortfolioBindingID     string
	PortfolioVersionID     string
	SourceRevision         string
	SourceDigest           string
	GoalContextID          string
	GoalContextDigest      string
	AgentProfileID         string
	ActorMemberID          string
	PolicyID               string
	PolicyVersion          int64
	PlanningCycle          int
	TriggerKind            string
	TriggerRef             string
	ScheduledTaskID        string
	ScheduledRunID         string
	DedupeKey              string
	Proposal               AutoplanProposal
	ProposalDigest         string
	ApprovedProposalDigest string
	Status                 AutoplanStatus
	ApprovalID             string
	ApprovalAttempt        int
	ApprovalRequestedAt    time.Time
	HILDecisionID          string
	HILReceiptID           string
	HILReceiptConsumedAt   time.Time
	PlanningAttempt        int
	PlanningLeaseOwner     string
	PlanningLeaseExpiresAt time.Time
	NextPlanningAttemptAt  time.Time
	Materializations       []AutoplanMaterialization
	RejectionReason        string
	FailureReason          string
	SupersededBy           string
	Version                int64
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

func (request AutoplanRequest) Normalized() AutoplanRequest {
	request.TenantID = strings.TrimSpace(request.TenantID)
	request.PortfolioID = strings.TrimSpace(request.PortfolioID)
	request.PortfolioBindingID = strings.TrimSpace(request.PortfolioBindingID)
	request.PortfolioVersionID = strings.TrimSpace(request.PortfolioVersionID)
	request.SourceRevision = strings.TrimSpace(request.SourceRevision)
	request.SourceDigest = strings.TrimSpace(request.SourceDigest)
	request.GoalContextID = strings.TrimSpace(request.GoalContextID)
	request.GoalContextDigest = strings.TrimSpace(request.GoalContextDigest)
	request.AgentProfileID = strings.TrimSpace(request.AgentProfileID)
	request.ActorMemberID = strings.TrimSpace(request.ActorMemberID)
	request.PolicyID = strings.TrimSpace(request.PolicyID)
	if request.PlanningCycle == 0 {
		request.PlanningCycle = 1
	}
	request.TriggerKind = strings.TrimSpace(request.TriggerKind)
	request.TriggerRef = strings.TrimSpace(request.TriggerRef)
	request.ScheduledTaskID = strings.TrimSpace(request.ScheduledTaskID)
	request.ScheduledRunID = strings.TrimSpace(request.ScheduledRunID)
	if request.TriggerKind == "" {
		request.TriggerKind = "portfolio_pulse"
	}
	if request.TriggerRef == "" {
		request.TriggerRef = request.PortfolioVersionID
	}
	if !request.TriggeredAt.IsZero() {
		request.TriggeredAt = request.TriggeredAt.UTC()
	}
	return request
}

func (request AutoplanRequest) Validate() error {
	request = request.Normalized()
	if request.TenantID == "" || request.PortfolioBindingID == "" || request.PortfolioVersionID == "" ||
		request.SourceDigest == "" || request.AgentProfileID == "" || request.TriggerKind == "" || request.TriggerRef == "" {
		return fmt.Errorf("%w: plan request tenant, portfolio binding/version, source digest, Agent, and trigger are required", ErrInvalidContract)
	}
	if request.PolicyVersion < 0 || request.PlanningCycle < 1 {
		return fmt.Errorf("%w: plan request policy version and cycle are invalid", ErrInvalidContract)
	}
	if (request.GoalContextID == "") != (request.GoalContextDigest == "") {
		return fmt.Errorf("%w: plan request goal context identity and digest must be set together", ErrInvalidContract)
	}
	return nil
}

func AutoplanDedupeKey(request AutoplanRequest) (string, error) {
	request = request.Normalized()
	if err := request.Validate(); err != nil {
		return "", err
	}
	parts := []string{
		request.TenantID,
		request.PortfolioBindingID,
		request.PortfolioVersionID,
		request.SourceRevision,
		request.SourceDigest,
		request.AgentProfileID,
		request.PolicyID,
		fmt.Sprint(request.PolicyVersion),
		fmt.Sprint(request.PlanningCycle),
		request.TriggerKind,
		request.TriggerRef,
	}
	// Preserve IDs of durable pre-goal-anchor plans while making a changed goal
	// snapshot a distinct planning identity for all new plans.
	if request.GoalContextDigest != "" {
		parts = append(parts, "goal-context", request.GoalContextDigest)
	}
	return "responsibility-autoplan:" + autoplanHash(parts...), nil
}

func AutoplanID(request AutoplanRequest) (string, error) {
	dedupeKey, err := AutoplanDedupeKey(request)
	if err != nil {
		return "", err
	}
	return "autoplan-" + autoplanHash(dedupeKey)[:24], nil
}

func NewAutoplanRecord(request AutoplanRequest, now time.Time) (AutoplanRecord, error) {
	request = request.Normalized()
	if err := request.Validate(); err != nil {
		return AutoplanRecord{}, err
	}
	id, err := AutoplanID(request)
	if err != nil {
		return AutoplanRecord{}, err
	}
	dedupeKey, err := AutoplanDedupeKey(request)
	if err != nil {
		return AutoplanRecord{}, err
	}
	if now.IsZero() {
		return AutoplanRecord{}, fmt.Errorf("%w: plan creation time is required", ErrInvalidContract)
	}
	now = now.UTC()
	return AutoplanRecord{
		ID: id, TenantID: request.TenantID, PortfolioID: request.PortfolioID, PortfolioBindingID: request.PortfolioBindingID,
		PortfolioVersionID: request.PortfolioVersionID, SourceRevision: request.SourceRevision, SourceDigest: request.SourceDigest,
		GoalContextID: request.GoalContextID, GoalContextDigest: request.GoalContextDigest,
		AgentProfileID: request.AgentProfileID, ActorMemberID: request.ActorMemberID, PolicyID: request.PolicyID,
		PolicyVersion: request.PolicyVersion, PlanningCycle: request.PlanningCycle, TriggerKind: request.TriggerKind, TriggerRef: request.TriggerRef,
		ScheduledTaskID: request.ScheduledTaskID, ScheduledRunID: request.ScheduledRunID, DedupeKey: dedupeKey,
		Status: AutoplanStatusPlanning, Version: 1, CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (proposal AutoplanProposal) Normalized() AutoplanProposal {
	proposal.Summary = strings.TrimSpace(proposal.Summary)
	proposal.Rationale = strings.TrimSpace(proposal.Rationale)
	proposal.NoActionReason = strings.TrimSpace(proposal.NoActionReason)
	proposal.SourceRefs = cleanAutoplanStrings(proposal.SourceRefs)
	proposal.RiskSummary = cleanAutoplanStrings(proposal.RiskSummary)
	proposal.RequiredApprovals = cleanAutoplanStrings(proposal.RequiredApprovals)
	proposal.Steps = append([]AutoplanStep(nil), proposal.Steps...)
	for index := range proposal.Steps {
		proposal.Steps[index] = proposal.Steps[index].normalized()
	}
	sort.Slice(proposal.Steps, func(i, j int) bool { return proposal.Steps[i].StepID < proposal.Steps[j].StepID })
	return proposal
}

func (proposal AutoplanProposal) Validate() error {
	proposal = proposal.Normalized()
	if proposal.Version != AutoplanLegacyProposalVersion && proposal.Version != AutoplanProposalVersion {
		return fmt.Errorf("%w: plan proposal version must be %d or %d", ErrInvalidContract, AutoplanLegacyProposalVersion, AutoplanProposalVersion)
	}
	if len(proposal.Steps) == 0 {
		if proposal.NoActionReason == "" {
			return fmt.Errorf("%w: a plan without steps requires a no-action reason", ErrInvalidContract)
		}
		if len(proposal.RequiredApprovals) > 0 {
			return fmt.Errorf("%w: a no-action plan cannot require approval", ErrInvalidContract)
		}
		return nil
	}
	if proposal.Summary == "" || proposal.NoActionReason != "" || len(proposal.SourceRefs) == 0 {
		return fmt.Errorf("%w: an actionable plan requires summary, source references, and no no-action reason", ErrInvalidContract)
	}
	steps := make(map[string]AutoplanStep, len(proposal.Steps))
	for _, step := range proposal.Steps {
		if err := step.Validate(); err != nil {
			return err
		}
		if proposal.Version == AutoplanLegacyProposalVersion && (step.ParentGoalRef != "" || step.ParentGoal != "") {
			return fmt.Errorf("%w: legacy plan step %s cannot contain unbound parent-goal fields", ErrInvalidContract, step.StepID)
		}
		if proposal.Version == AutoplanProposalVersion && (step.ParentGoalRef == "" || step.ParentGoal == "") {
			return fmt.Errorf("%w: plan step %s must be derived from an existing parent goal", ErrInvalidContract, step.StepID)
		}
		if _, exists := steps[step.StepID]; exists {
			return fmt.Errorf("%w: plan step IDs must be unique", ErrInvalidContract)
		}
		steps[step.StepID] = step
	}
	for _, step := range proposal.Steps {
		for _, dependency := range step.DependsOn {
			if dependency == step.StepID {
				return fmt.Errorf("%w: a plan step cannot depend on itself", ErrInvalidContract)
			}
			if _, exists := steps[dependency]; !exists {
				return fmt.Errorf("%w: plan step %s depends on unknown step %s", ErrInvalidContract, step.StepID, dependency)
			}
		}
	}
	if autoplanHasDependencyCycle(steps) {
		return fmt.Errorf("%w: plan step dependencies contain a cycle", ErrInvalidContract)
	}
	return nil
}

func (step AutoplanStep) normalized() AutoplanStep {
	step.StepID = strings.TrimSpace(step.StepID)
	step.Title = strings.TrimSpace(step.Title)
	step.Description = strings.TrimSpace(step.Description)
	step.ParentGoalRef = strings.TrimSpace(step.ParentGoalRef)
	step.ParentGoal = strings.TrimSpace(step.ParentGoal)
	step.Goal = strings.TrimSpace(step.Goal)
	step.AssigneeAgentID = strings.TrimSpace(step.AssigneeAgentID)
	step.DependsOn = cleanAutoplanStrings(step.DependsOn)
	step.AcceptanceCriteria = cleanAutoplanStrings(step.AcceptanceCriteria)
	step.ExecutionScopes = cleanAutoplanStrings(step.ExecutionScopes)
	if !step.DueAt.IsZero() {
		step.DueAt = step.DueAt.UTC()
	}
	step.EvidenceRequirements = append([]AutoplanEvidenceRequirement(nil), step.EvidenceRequirements...)
	for index := range step.EvidenceRequirements {
		step.EvidenceRequirements[index] = step.EvidenceRequirements[index].normalized()
	}
	sort.Slice(step.EvidenceRequirements, func(i, j int) bool {
		return step.EvidenceRequirements[i].EvidenceRequirementID < step.EvidenceRequirements[j].EvidenceRequirementID
	})
	return step
}

func (step AutoplanStep) Validate() error {
	step = step.normalized()
	if step.StepID == "" || step.Title == "" || step.Goal == "" || step.AssigneeAgentID == "" {
		return fmt.Errorf("%w: plan step ID, title, goal, and assignee are required", ErrInvalidContract)
	}
	if step.Priority < 0 || step.Priority > 100 || !step.RiskLevel.valid() {
		return fmt.Errorf("%w: plan step priority or risk level is invalid", ErrInvalidContract)
	}
	if len(step.AcceptanceCriteria) == 0 || len(step.EvidenceRequirements) == 0 {
		return fmt.Errorf("%w: every plan step requires acceptance criteria and evidence", ErrInvalidContract)
	}
	criteria := make(map[string]struct{}, len(step.AcceptanceCriteria))
	for _, criterion := range step.AcceptanceCriteria {
		criteria[criterion] = struct{}{}
	}
	covered := make(map[string]bool, len(criteria))
	evidenceIDs := make(map[string]struct{}, len(step.EvidenceRequirements))
	for _, evidence := range step.EvidenceRequirements {
		if err := evidence.Validate(); err != nil {
			return err
		}
		if _, exists := evidenceIDs[evidence.EvidenceRequirementID]; exists {
			return fmt.Errorf("%w: plan evidence requirement IDs must be unique", ErrInvalidContract)
		}
		evidenceIDs[evidence.EvidenceRequirementID] = struct{}{}
		if _, exists := criteria[evidence.AcceptanceCriterion]; !exists {
			return fmt.Errorf("%w: evidence requirement references an unknown acceptance criterion", ErrInvalidContract)
		}
		if evidence.Required {
			covered[evidence.AcceptanceCriterion] = true
		}
	}
	for criterion := range criteria {
		if !covered[criterion] {
			return fmt.Errorf("%w: every acceptance criterion requires evidence", ErrInvalidContract)
		}
	}
	return nil
}

func (requirement AutoplanEvidenceRequirement) normalized() AutoplanEvidenceRequirement {
	requirement.EvidenceRequirementID = strings.TrimSpace(requirement.EvidenceRequirementID)
	requirement.AcceptanceCriterion = strings.TrimSpace(requirement.AcceptanceCriterion)
	requirement.Kind = strings.TrimSpace(strings.ToLower(requirement.Kind))
	return requirement
}

func (requirement AutoplanEvidenceRequirement) Validate() error {
	requirement = requirement.normalized()
	if requirement.EvidenceRequirementID == "" || requirement.AcceptanceCriterion == "" || !requirement.validKind() {
		return fmt.Errorf("%w: plan evidence ID, criterion, and kind are required", ErrInvalidContract)
	}
	return nil
}

func (requirement AutoplanEvidenceRequirement) validKind() bool {
	switch requirement.Kind {
	case "artifact", "tool_log", "test_log", "external_url", "human_confirmation":
		return true
	default:
		return false
	}
}

func AutoplanProposalDigest(proposal AutoplanProposal) (string, error) {
	proposal = proposal.Normalized()
	if err := proposal.Validate(); err != nil {
		return "", err
	}
	payloadValue := any(proposal)
	if proposal.Version == AutoplanLegacyProposalVersion {
		payloadValue = legacyAutoplanProposalDigestValue(proposal)
	}
	payload, err := json.Marshal(payloadValue)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// Version 1 digests were created before parent-goal fields existed. Preserve
// that exact JSON field layout so durable pre-goal-anchor plans remain valid.
func legacyAutoplanProposalDigestValue(proposal AutoplanProposal) legacyAutoplanProposal {
	var steps []legacyAutoplanStep
	if proposal.Steps != nil {
		steps = make([]legacyAutoplanStep, len(proposal.Steps))
		for index, step := range proposal.Steps {
			var evidenceRequirements []legacyAutoplanEvidenceRequirement
			if step.EvidenceRequirements != nil {
				evidenceRequirements = make([]legacyAutoplanEvidenceRequirement, len(step.EvidenceRequirements))
				for evidenceIndex, requirement := range step.EvidenceRequirements {
					evidenceRequirements[evidenceIndex] = legacyAutoplanEvidenceRequirement{
						EvidenceRequirementID: requirement.EvidenceRequirementID,
						AcceptanceCriterion:   requirement.AcceptanceCriterion,
						Kind:                  requirement.Kind,
						Required:              requirement.Required,
					}
				}
			}
			steps[index] = legacyAutoplanStep{
				StepID: step.StepID, Title: step.Title, Description: step.Description, Goal: step.Goal,
				AssigneeAgentID: step.AssigneeAgentID, Priority: step.Priority, DueAt: step.DueAt,
				DependsOn: step.DependsOn, AcceptanceCriteria: step.AcceptanceCriteria,
				EvidenceRequirements: evidenceRequirements, ExecutionScopes: step.ExecutionScopes,
				RiskLevel: step.RiskLevel, HasSideEffect: step.HasSideEffect,
			}
		}
	}
	return legacyAutoplanProposal{
		Version: proposal.Version, Summary: proposal.Summary, Rationale: proposal.Rationale,
		SourceRefs: proposal.SourceRefs, RiskSummary: proposal.RiskSummary,
		RequiredApprovals: proposal.RequiredApprovals, NoActionReason: proposal.NoActionReason, Steps: steps,
	}
}

type legacyAutoplanProposal struct {
	Version           int
	Summary           string
	Rationale         string
	SourceRefs        []string
	RiskSummary       []string
	RequiredApprovals []string
	NoActionReason    string
	Steps             []legacyAutoplanStep
}

type legacyAutoplanStep struct {
	StepID               string
	Title                string
	Description          string
	Goal                 string
	AssigneeAgentID      string
	Priority             int
	DueAt                time.Time
	DependsOn            []string
	AcceptanceCriteria   []string
	EvidenceRequirements []legacyAutoplanEvidenceRequirement
	ExecutionScopes      []string
	RiskLevel            AutoplanRiskLevel
	HasSideEffect        bool
}

type legacyAutoplanEvidenceRequirement struct {
	EvidenceRequirementID string
	AcceptanceCriterion   string
	Kind                  string
	Required              bool
}

func (record AutoplanRecord) Request() AutoplanRequest {
	return AutoplanRequest{
		TenantID: record.TenantID, PortfolioID: record.PortfolioID, PortfolioBindingID: record.PortfolioBindingID,
		PortfolioVersionID: record.PortfolioVersionID, SourceRevision: record.SourceRevision, SourceDigest: record.SourceDigest,
		GoalContextID: record.GoalContextID, GoalContextDigest: record.GoalContextDigest,
		AgentProfileID: record.AgentProfileID, ActorMemberID: record.ActorMemberID, PolicyID: record.PolicyID,
		PolicyVersion: record.PolicyVersion, PlanningCycle: record.PlanningCycle, TriggerKind: record.TriggerKind, TriggerRef: record.TriggerRef,
		ScheduledTaskID: record.ScheduledTaskID, ScheduledRunID: record.ScheduledRunID,
	}
}

func (record AutoplanRecord) Validate() error {
	if err := record.Request().Validate(); err != nil {
		return err
	}
	id, err := AutoplanID(record.Request())
	if err != nil || record.ID != id {
		return fmt.Errorf("%w: plan ID does not match its planning identity", ErrInvalidContract)
	}
	dedupeKey, err := AutoplanDedupeKey(record.Request())
	if err != nil || record.DedupeKey != dedupeKey {
		return fmt.Errorf("%w: plan dedupe key does not match its planning identity", ErrInvalidContract)
	}
	if !record.Status.valid() || record.Version < 1 || record.ApprovalAttempt < 0 || record.PlanningAttempt < 0 || record.CreatedAt.IsZero() || record.UpdatedAt.IsZero() || record.UpdatedAt.Before(record.CreatedAt) {
		return fmt.Errorf("%w: plan state, version, or timestamps are invalid", ErrInvalidContract)
	}
	if (record.PlanningLeaseOwner == "") != record.PlanningLeaseExpiresAt.IsZero() {
		return fmt.Errorf("%w: planning lease owner and expiry must be set together", ErrInvalidContract)
	}
	if record.Status != AutoplanStatusPlanning && (record.PlanningLeaseOwner != "" || !record.PlanningLeaseExpiresAt.IsZero()) {
		return fmt.Errorf("%w: only a planning record may hold a planning lease", ErrInvalidContract)
	}
	if record.Status != AutoplanStatusFailed && !record.NextPlanningAttemptAt.IsZero() {
		return fmt.Errorf("%w: only a failed plan may defer its next planning attempt", ErrInvalidContract)
	}
	hasProposal := record.Proposal.Version != 0
	switch record.Status {
	case AutoplanStatusPlanning:
		if hasProposal || record.ProposalDigest != "" {
			return fmt.Errorf("%w: planning record cannot contain a proposal", ErrInvalidContract)
		}
		return nil
	case AutoplanStatusFailed:
		if record.FailureReason == "" {
			return fmt.Errorf("%w: failed plan requires a failure reason", ErrInvalidContract)
		}
		if !hasProposal {
			return nil
		}
	case AutoplanStatusNotActionable:
		if !hasProposal || len(record.Proposal.Steps) != 0 {
			return fmt.Errorf("%w: not-actionable plan requires a no-action proposal", ErrInvalidContract)
		}
	case AutoplanStatusPendingApproval:
		if record.ApprovalID == "" || record.ApprovalAttempt < 1 || record.ApprovalRequestedAt.IsZero() {
			return fmt.Errorf("%w: pending approval plan requires an approval identity and request time", ErrInvalidContract)
		}
	case AutoplanStatusApproved, AutoplanStatusMaterializing, AutoplanStatusMaterialized:
		if record.ApprovedProposalDigest == "" || record.ApprovedProposalDigest != record.ProposalDigest {
			return fmt.Errorf("%w: executable plan must bind approval to the current proposal digest", ErrInvalidContract)
		}
		if record.HILDecisionID != "" && record.HILReceiptID == "" {
			return fmt.Errorf("%w: HIL-authorized plan requires its receipt identity", ErrInvalidContract)
		}
		if (record.Status == AutoplanStatusMaterializing || record.Status == AutoplanStatusMaterialized) &&
			record.HILDecisionID != "" && record.HILReceiptConsumedAt.IsZero() {
			return fmt.Errorf("%w: HIL plan must consume its receipt before materialization", ErrInvalidContract)
		}
	case AutoplanStatusRejected:
		if record.RejectionReason == "" {
			return fmt.Errorf("%w: rejected plan requires a rejection reason", ErrInvalidContract)
		}
	case AutoplanStatusSuperseding, AutoplanStatusSuperseded:
		if record.SupersededBy == "" {
			return fmt.Errorf("%w: superseding plan requires its replacement or source reference", ErrInvalidContract)
		}
		if !hasProposal {
			return nil
		}
	}
	if !hasProposal {
		return fmt.Errorf("%w: plan state requires a proposal", ErrInvalidContract)
	}
	if !record.HILReceiptConsumedAt.IsZero() && record.HILReceiptID == "" {
		return fmt.Errorf("%w: consumed HIL receipt requires a receipt identity", ErrInvalidContract)
	}
	if err := record.Proposal.Validate(); err != nil {
		return err
	}
	digest, err := AutoplanProposalDigest(record.Proposal)
	if err != nil || record.ProposalDigest != digest {
		return fmt.Errorf("%w: plan proposal digest is inconsistent", ErrInvalidContract)
	}
	if record.Status != AutoplanStatusNotActionable && len(record.Proposal.Steps) == 0 {
		return fmt.Errorf("%w: actionable plan state requires at least one step", ErrInvalidContract)
	}
	if record.Status == AutoplanStatusMaterialized {
		if len(record.Materializations) != len(record.Proposal.Steps) {
			return fmt.Errorf("%w: materialized plan requires one receipt per step", ErrInvalidContract)
		}
		seen := make(map[string]struct{}, len(record.Materializations))
		for _, receipt := range record.Materializations {
			if err := receipt.Validate(); err != nil {
				return err
			}
			if _, exists := seen[receipt.StepID]; exists {
				return fmt.Errorf("%w: materialization step IDs must be unique", ErrInvalidContract)
			}
			seen[receipt.StepID] = struct{}{}
		}
		for _, step := range record.Proposal.Steps {
			if _, exists := seen[step.StepID]; !exists {
				return fmt.Errorf("%w: missing materialization receipt for step %s", ErrInvalidContract, step.StepID)
			}
		}
	}
	return nil
}

func (receipt AutoplanMaterialization) Validate() error {
	if strings.TrimSpace(receipt.StepID) == "" || strings.TrimSpace(receipt.MissionID) == "" || strings.TrimSpace(receipt.TaskID) == "" ||
		strings.TrimSpace(receipt.ResponsibilityID) == "" || receipt.MaterializedAt.IsZero() {
		return fmt.Errorf("%w: materialization receipt identity and time are required", ErrInvalidContract)
	}
	return nil
}

func ValidateAutoplanTransition(from, to AutoplanStatus) error {
	if !from.valid() || !to.valid() || !autoplanTransitions[from][to] {
		return fmt.Errorf("%w: autonomous plan %s -> %s", ErrInvalidTransition, from, to)
	}
	return nil
}

func (status AutoplanStatus) valid() bool {
	switch status {
	case AutoplanStatusPlanning, AutoplanStatusProposed, AutoplanStatusPendingApproval, AutoplanStatusApproved,
		AutoplanStatusMaterializing, AutoplanStatusMaterialized, AutoplanStatusNotActionable, AutoplanStatusRejected,
		AutoplanStatusSuperseding, AutoplanStatusSuperseded, AutoplanStatusFailed:
		return true
	default:
		return false
	}
}

func (level AutoplanRiskLevel) valid() bool {
	switch level {
	case AutoplanRiskLow, AutoplanRiskMedium, AutoplanRiskHigh, AutoplanRiskCritical:
		return true
	default:
		return false
	}
}

var autoplanTransitions = map[AutoplanStatus]map[AutoplanStatus]bool{
	AutoplanStatusPlanning: {
		AutoplanStatusProposed: true, AutoplanStatusNotActionable: true, AutoplanStatusFailed: true, AutoplanStatusSuperseded: true,
	},
	AutoplanStatusProposed: {
		AutoplanStatusPendingApproval: true, AutoplanStatusApproved: true, AutoplanStatusRejected: true, AutoplanStatusFailed: true, AutoplanStatusSuperseded: true,
	},
	AutoplanStatusPendingApproval: {
		AutoplanStatusApproved: true, AutoplanStatusRejected: true, AutoplanStatusFailed: true, AutoplanStatusSuperseding: true, AutoplanStatusSuperseded: true,
	},
	AutoplanStatusApproved: {
		AutoplanStatusPendingApproval: true, AutoplanStatusMaterializing: true, AutoplanStatusFailed: true, AutoplanStatusSuperseded: true,
	},
	AutoplanStatusMaterializing: {
		AutoplanStatusMaterialized: true, AutoplanStatusFailed: true, AutoplanStatusSuperseded: true,
	},
	AutoplanStatusFailed: {
		AutoplanStatusPlanning: true, AutoplanStatusMaterializing: true, AutoplanStatusSuperseded: true,
	},
	AutoplanStatusSuperseding: {
		AutoplanStatusSuperseded: true,
	},
}

func cleanAutoplanStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func autoplanHasDependencyCycle(steps map[string]AutoplanStep) bool {
	const (
		unseen = iota
		visiting
		visited
	)
	states := make(map[string]int, len(steps))
	var visit func(string) bool
	visit = func(stepID string) bool {
		switch states[stepID] {
		case visiting:
			return true
		case visited:
			return false
		}
		states[stepID] = visiting
		for _, dependency := range steps[stepID].DependsOn {
			if visit(dependency) {
				return true
			}
		}
		states[stepID] = visited
		return false
	}
	for stepID := range steps {
		if visit(stepID) {
			return true
		}
	}
	return false
}

func autoplanHash(parts ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(digest[:])
}
