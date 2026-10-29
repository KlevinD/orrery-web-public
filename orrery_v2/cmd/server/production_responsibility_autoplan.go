package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/hil"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/responsibility"
	schedulerapp "orrery_v2/internal/app/scheduler"
	missioncore "orrery_v2/internal/mission/core"
)

const (
	responsibilityAutoplanHILAction          = "responsibility.plan.execute"
	responsibilityAutoplanHILPlanIDKey       = "autoplan_id"
	responsibilityAutoplanHILProposalDigest  = "proposal_digest"
	responsibilityAutoplanSourceKind         = "responsibility_autoplan"
	responsibilityAutoplanMaterializerViewer = "responsibility-autoplan"
)

// responsibilityAutoplanServicePort is the complete lifecycle boundary used
// by the production adapters. The domain service owns durable plan state,
// HIL receipt consumption, and retryable Mission materialization; server
// wiring only translates scheduler and HIL transport contracts.
type responsibilityAutoplanServicePort interface {
	EvaluatePortfolio(context.Context, responsibility.AutoplanPortfolioRequest) (responsibility.AutoplanEvaluationResult, error)
	Reconcile(context.Context, int) (responsibility.AutoplanReconcileResult, error)
	ObserveHILResolution(context.Context, responsibility.AutoplanHILResolution) error
}

// productionResponsibilityAutoplanAdapter satisfies both the scheduler's
// narrow Autoplan port and the HIL observer consumer. It intentionally never
// returns a scheduler action: evaluation records a plan only, while reconcile
// later materializes approved steps.
type productionResponsibilityAutoplanAdapter struct {
	service responsibilityAutoplanServicePort
}

func newProductionResponsibilityAutoplanAdapter(service responsibilityAutoplanServicePort) *productionResponsibilityAutoplanAdapter {
	return &productionResponsibilityAutoplanAdapter{service: service}
}

var _ responsibilityAutoplanSchedulerPort = (*productionResponsibilityAutoplanAdapter)(nil)
var _ responsibilityAutoplanHILResolutionConsumer = (*productionResponsibilityAutoplanAdapter)(nil)

func (a *productionResponsibilityAutoplanAdapter) EvaluateAutoplan(ctx context.Context, request schedulerapp.ResponsibilityEvaluationRequest, portfolio responsibility.PeopleResponsibilityPortfolio) (schedulerapp.ResponsibilityEvaluationResult, error) {
	if a == nil || a.service == nil {
		return schedulerapp.ResponsibilityEvaluationResult{}, fmt.Errorf("responsibility autonomous-plan service is not configured")
	}
	evaluation, err := a.service.EvaluatePortfolio(ctx, responsibility.AutoplanPortfolioRequest{
		TenantID:           strings.TrimSpace(request.TenantID),
		ScheduledTaskID:    strings.TrimSpace(request.ScheduledTaskID),
		ScheduledRunID:     strings.TrimSpace(request.ScheduledRunID),
		ApprovalAssigneeID: strings.TrimSpace(request.ApprovalAssigneeID),
		TriggeredAt:        request.TriggeredAt,
		Portfolio:          portfolio,
	})
	if err != nil {
		return schedulerapp.ResponsibilityEvaluationResult{}, fmt.Errorf("evaluate responsibility autonomous plan: %w", err)
	}
	return responsibilityAutoplanSchedulerResult(portfolio, evaluation), nil
}

func (a *productionResponsibilityAutoplanAdapter) ReconcileAutoplans(ctx context.Context, limit int) error {
	if a == nil || a.service == nil {
		return fmt.Errorf("responsibility autonomous-plan service is not configured")
	}
	if _, err := a.service.Reconcile(ctx, limit); err != nil {
		return fmt.Errorf("reconcile responsibility autonomous plans: %w", err)
	}
	return nil
}

func (a *productionResponsibilityAutoplanAdapter) ObserveAutoplanHILResolution(ctx context.Context, resolution responsibilityAutoplanHILResolution) error {
	if a == nil || a.service == nil {
		return fmt.Errorf("responsibility autonomous-plan service is not configured")
	}
	return a.service.ObserveHILResolution(ctx, responsibility.AutoplanHILResolution{
		TenantID:       strings.TrimSpace(resolution.TenantID),
		AutoplanID:     strings.TrimSpace(resolution.AutoplanID),
		ProposalDigest: strings.TrimSpace(resolution.ProposalDigest),
		DecisionID:     strings.TrimSpace(resolution.DecisionID),
		ReceiptID:      strings.TrimSpace(resolution.ReceiptID),
		Approved:       resolution.Approved,
		Reason:         strings.TrimSpace(resolution.Reason),
		ResolvedAt:     resolution.ResolvedAt,
	})
}

func responsibilityAutoplanSchedulerResult(portfolio responsibility.PeopleResponsibilityPortfolio, evaluation responsibility.AutoplanEvaluationResult) schedulerapp.ResponsibilityEvaluationResult {
	decisionSource := strings.TrimSpace(evaluation.DecisionSource)
	if decisionSource == "" {
		decisionSource = responsibility.PeopleResponsibilityPortfolioVersionID(portfolio.BindingID, portfolio.SourceDigest)
	}
	reason := strings.TrimSpace(evaluation.Reason)
	if reason == "" {
		reason = "autoplan_evaluated"
	}
	return schedulerapp.ResponsibilityEvaluationResult{
		PortfolioID:        strings.TrimSpace(portfolio.ID),
		PortfolioBindingID: strings.TrimSpace(portfolio.BindingID),
		PortfolioVersionID: responsibility.PeopleResponsibilityPortfolioVersionID(portfolio.BindingID, portfolio.SourceDigest),
		SourceRevision:     strings.TrimSpace(portfolio.SourceRevision),
		SourceDigest:       strings.TrimSpace(portfolio.SourceDigest),
		AgentProfileID:     strings.TrimSpace(portfolio.AgentProfileID),
		Evaluated:          true,
		Actionable:         false,
		DecisionKind:       schedulerapp.ResponsibilityEvaluationNoAction,
		DecisionSourceRef:  decisionSource,
		Reason:             reason,
	}
}

// responsibilityAutoplanHILResolution is the narrow, validated handoff from
// the shared HIL fanout to the autonomous-planning domain. The observer never
// materializes a Mission itself: a durable plan worker owns that retryable
// side effect after it has recorded this resolution.
type responsibilityAutoplanHILResolution struct {
	TenantID       string
	AutoplanID     string
	ProposalDigest string
	DecisionID     string
	ReceiptID      string
	Approved       bool
	Actor          hil.ActorRef
	Reason         string
	ResolvedAt     time.Time
}

// responsibilityAutoplanHILResolutionConsumer is intentionally independent
// from the responsibility domain service while that service is being wired.
// Implementations must durably deduplicate by DecisionID before enqueueing a
// materialization attempt.
type responsibilityAutoplanHILResolutionConsumer interface {
	ObserveAutoplanHILResolution(context.Context, responsibilityAutoplanHILResolution) error
}

// productionResponsibilityAutoplanHILObserver is added to the existing HIL
// observer fanout at composition time. It accepts only the exact plan action
// and only terminal receipts whose immutable args hash matches the proposal.
type productionResponsibilityAutoplanHILObserver struct {
	consumer responsibilityAutoplanHILResolutionConsumer
	report   func(error)
}

func newProductionResponsibilityAutoplanHILObserver(consumer responsibilityAutoplanHILResolutionConsumer, report func(error)) *productionResponsibilityAutoplanHILObserver {
	return &productionResponsibilityAutoplanHILObserver{consumer: consumer, report: report}
}

func (o *productionResponsibilityAutoplanHILObserver) DecisionResolved(ctx context.Context, resolution hil.DecisionResolution) {
	if o == nil || o.consumer == nil {
		return
	}
	handoff, matched, err := responsibilityAutoplanHILHandoff(resolution)
	if err != nil {
		o.reportError(err)
		return
	}
	if !matched {
		return
	}
	if err := o.consumer.ObserveAutoplanHILResolution(ctx, handoff); err != nil {
		o.reportError(fmt.Errorf("record responsibility autonomous-plan HIL resolution: %w", err))
	}
}

func (o *productionResponsibilityAutoplanHILObserver) reportError(err error) {
	if err != nil && o != nil && o.report != nil {
		o.report(err)
	}
}

func responsibilityAutoplanHILHandoff(resolution hil.DecisionResolution) (responsibilityAutoplanHILResolution, bool, error) {
	decision := resolution.Decision
	if !strings.EqualFold(strings.TrimSpace(decision.Action), responsibilityAutoplanHILAction) {
		return responsibilityAutoplanHILResolution{}, false, nil
	}
	approved, terminal := responsibilityAutoplanHILTerminal(resolution)
	if !terminal {
		return responsibilityAutoplanHILResolution{}, false, nil
	}
	planID := responsibilityAutoplanHILPayloadString(decision.PayloadPreview, responsibilityAutoplanHILPlanIDKey)
	proposalDigest := responsibilityAutoplanHILPayloadString(decision.PayloadPreview, responsibilityAutoplanHILProposalDigest)
	if strings.TrimSpace(decision.TenantID) == "" || strings.TrimSpace(decision.ID) == "" || strings.TrimSpace(resolution.Receipt.ID) == "" ||
		(strings.TrimSpace(resolution.Receipt.TenantID) != "" && strings.TrimSpace(resolution.Receipt.TenantID) != strings.TrimSpace(decision.TenantID)) ||
		(strings.TrimSpace(resolution.Receipt.RequestID) != "" && strings.TrimSpace(resolution.Receipt.RequestID) != strings.TrimSpace(decision.ID)) ||
		planID == "" || proposalDigest == "" || strings.TrimSpace(decision.ArgsHash) != proposalDigest || strings.TrimSpace(resolution.Receipt.ArgsHash) != proposalDigest {
		return responsibilityAutoplanHILResolution{}, false, fmt.Errorf("responsibility autonomous-plan HIL decision %q has an invalid immutable binding", strings.TrimSpace(decision.ID))
	}
	return responsibilityAutoplanHILResolution{
		TenantID:       strings.TrimSpace(decision.TenantID),
		AutoplanID:     planID,
		ProposalDigest: proposalDigest,
		DecisionID:     strings.TrimSpace(decision.ID),
		ReceiptID:      strings.TrimSpace(resolution.Receipt.ID),
		Approved:       approved,
		Actor:          resolution.Actor,
		Reason:         strings.TrimSpace(resolution.Reason),
		ResolvedAt:     responsibilityAutoplanFirstNonZeroTime(resolution.Receipt.CreatedAt, decision.CreatedAt),
	}, true, nil
}

func responsibilityAutoplanHILTerminal(resolution hil.DecisionResolution) (bool, bool) {
	switch resolution.Decision.Status {
	case hil.DecisionResolved:
		return true, resolution.Receipt.Effect == hil.EffectAuthorize
	case hil.DecisionDenied:
		return false, resolution.Receipt.Effect == hil.EffectDeny
	default:
		return false, false
	}
}

func responsibilityAutoplanHILPayloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	value, ok := payload[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func responsibilityAutoplanFirstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value.UTC()
		}
	}
	return time.Time{}
}

// responsibilityAutoplanMissionMaterializer is the domain-facing port for a
// single already-authorized plan step. HIL receipt consumption belongs before
// this call, in the plan worker; this adapter verifies the approved immutable
// proposal digest and converts the step to existing Mission commands.
type responsibilityAutoplanMissionMaterializer interface {
	MaterializeAutoplanStep(context.Context, responsibility.AutoplanRecord, string) (responsibility.AutoplanMaterialization, error)
}

type responsibilityAutoplanMissionCommandPort interface {
	CreateMission(context.Context, missioncontrol.ViewerRef, missioncontrol.CreateMissionCommand) (missioncontrol.MissionDTO, error)
	CreateTask(context.Context, missioncontrol.ViewerRef, missioncontrol.CreateTaskCommand) (missioncontrol.TaskCardDTO, error)
	AssignTask(context.Context, missioncontrol.ViewerRef, missioncontrol.AssignTaskCommand) (missioncontrol.CommandReceiptDTO, error)
}

type productionResponsibilityAutoplanMissionMaterializer struct {
	missions responsibilityAutoplanMissionCommandPort
	now      func() time.Time
}

func newProductionResponsibilityAutoplanMissionMaterializer(missions responsibilityAutoplanMissionCommandPort, now func() time.Time) *productionResponsibilityAutoplanMissionMaterializer {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &productionResponsibilityAutoplanMissionMaterializer{missions: missions, now: now}
}

func (m *productionResponsibilityAutoplanMissionMaterializer) MaterializeAutoplanStep(ctx context.Context, plan responsibility.AutoplanRecord, stepID string) (responsibility.AutoplanMaterialization, error) {
	if m == nil || m.missions == nil {
		return responsibility.AutoplanMaterialization{}, fmt.Errorf("responsibility autonomous-plan Mission commands are not configured")
	}
	plan, step, err := responsibilityAutoplanMaterializationInput(plan, stepID)
	if err != nil {
		return responsibility.AutoplanMaterialization{}, err
	}
	key := responsibilityAutoplanMaterializationKey(plan, step.StepID)
	viewer := responsibilityAutoplanMissionViewer(plan.TenantID)
	missionGoal := strings.TrimSpace(step.ParentGoal)
	if missionGoal == "" {
		missionGoal = step.Goal
	}
	mission, err := m.missions.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{
		RequestID:         key + ":mission",
		IdempotencyKey:    key + ":mission",
		Title:             "Responsibility: " + step.Title,
		FinalGoal:         missionGoal,
		BusinessDirection: strings.TrimSpace(plan.Proposal.Summary),
		RequesterID:       strings.TrimSpace(plan.ActorMemberID),
	})
	if err != nil {
		return responsibility.AutoplanMaterialization{}, fmt.Errorf("materialize responsibility autonomous-plan Mission: %w", err)
	}
	missionID := strings.TrimSpace(mission.MissionID)
	if missionID == "" {
		return responsibility.AutoplanMaterialization{}, fmt.Errorf("materialize responsibility autonomous-plan Mission: Mission ID is empty")
	}
	taskCommand, err := responsibilityAutoplanTaskCommand(plan, step, missionID, key)
	if err != nil {
		return responsibility.AutoplanMaterialization{}, fmt.Errorf("build responsibility autonomous-plan task contract: %w", err)
	}
	task, err := m.missions.CreateTask(ctx, viewer, taskCommand)
	if err != nil {
		return responsibility.AutoplanMaterialization{}, fmt.Errorf("materialize responsibility autonomous-plan task: %w", err)
	}
	taskID := strings.TrimSpace(task.TaskID)
	if taskID == "" {
		return responsibility.AutoplanMaterialization{}, fmt.Errorf("materialize responsibility autonomous-plan task: task ID is empty")
	}
	if _, err := m.missions.AssignTask(ctx, viewer, missioncontrol.AssignTaskCommand{
		RequestID: key + ":assign", IdempotencyKey: key + ":assign",
		MissionID: missioncore.MissionID(missionID), TaskID: missioncore.TaskID(taskID), AssigneeID: step.AssigneeAgentID,
		Reason: "materialize approved responsibility autonomous plan " + plan.ID,
	}); err != nil {
		return responsibility.AutoplanMaterialization{}, fmt.Errorf("assign responsibility autonomous-plan task: %w", err)
	}
	return responsibility.AutoplanMaterialization{
		StepID: step.StepID, MissionID: missionID, TaskID: taskID,
		ResponsibilityID: responsibility.AssignmentID(plan.TenantID, responsibility.WorkKindMissionTask, taskID),
		MaterializedAt:   m.now().UTC(),
	}, nil
}

func responsibilityAutoplanMaterializationInput(plan responsibility.AutoplanRecord, stepID string) (responsibility.AutoplanRecord, responsibility.AutoplanStep, error) {
	planID := strings.TrimSpace(plan.ID)
	stepID = strings.TrimSpace(stepID)
	if planID == "" || strings.TrimSpace(plan.TenantID) == "" || stepID == "" ||
		(plan.Status != responsibility.AutoplanStatusApproved && plan.Status != responsibility.AutoplanStatusMaterializing) ||
		strings.TrimSpace(plan.ProposalDigest) == "" || plan.ApprovedProposalDigest != plan.ProposalDigest {
		return responsibility.AutoplanRecord{}, responsibility.AutoplanStep{}, fmt.Errorf("responsibility autonomous-plan materialization input is not approved")
	}
	proposal := plan.Proposal.Normalized()
	digest, err := responsibility.AutoplanProposalDigest(proposal)
	if err != nil {
		return responsibility.AutoplanRecord{}, responsibility.AutoplanStep{}, fmt.Errorf("validate responsibility autonomous-plan proposal: %w", err)
	}
	if digest != plan.ProposalDigest {
		return responsibility.AutoplanRecord{}, responsibility.AutoplanStep{}, fmt.Errorf("responsibility autonomous-plan proposal digest no longer matches the approved plan")
	}
	for _, step := range proposal.Steps {
		if step.StepID == stepID {
			plan.Proposal = proposal
			return plan, step, nil
		}
	}
	return responsibility.AutoplanRecord{}, responsibility.AutoplanStep{}, fmt.Errorf("responsibility autonomous-plan step %q is not in the approved proposal", stepID)
}

func responsibilityAutoplanMissionViewer(tenantID string) missioncontrol.ViewerRef {
	return missioncontrol.ViewerRef{
		ViewerID: responsibilityAutoplanMaterializerViewer, ViewerKind: missioncontrol.ViewerKindSystem,
		TenantID: strings.TrimSpace(tenantID), AuthorizationSubjectID: responsibilityAutoplanMaterializerViewer,
		PermissionScope: []string{"mission.write"},
	}
}

func responsibilityAutoplanTaskCommand(plan responsibility.AutoplanRecord, step responsibility.AutoplanStep, missionID, key string) (missioncontrol.CreateTaskCommand, error) {
	workerGoal := strings.TrimSpace(step.Description)
	if workerGoal == "" {
		workerGoal = step.Goal
	}
	taskID := responsibilityAutoplanTaskID(plan, step.StepID)
	executionEnvelope, err := responsibilityAutoplanExecutionEnvelope(plan, step, missionID, taskID)
	if err != nil {
		return missioncontrol.CreateTaskCommand{}, err
	}
	return missioncontrol.CreateTaskCommand{
		RequestID: key + ":task", IdempotencyKey: key + ":task", MissionID: missioncore.MissionID(missionID),
		TaskID: missioncore.TaskID(taskID), AssigneeID: step.AssigneeAgentID,
		Goal: step.Goal, WorkerPersonalGoal: workerGoal, Scope: responsibilityAutoplanTaskScope(plan, step),
		Priority: responsibilityAutoplanTaskPriority(step.Priority), DueAt: step.DueAt,
		ExpectedDeliverables: []missioncontrol.ExpectedDeliverableCommand{{ID: "deliverable-" + step.StepID, Title: step.Title}},
		EvidenceRequirements: responsibilityAutoplanEvidenceRequirements(step), AcceptanceCriteria: append([]string(nil), step.AcceptanceCriteria...),
		SourceKind: responsibilityAutoplanSourceKind, SourceID: plan.ID, SourceStepID: step.StepID,
		ExecutionPermissionScopes: append([]string(nil), step.ExecutionScopes...),
		ExecutionEnvelope:         executionEnvelope,
		RequireExplicitToolScopes: true,
	}, nil
}

// responsibilityAutoplanExecutionEnvelope freezes the identity and scope that
// were approved for a plan step. The responsibility coordinator will rekey it
// for each runtime attempt and intersect it with the live context.
func responsibilityAutoplanExecutionEnvelope(plan responsibility.AutoplanRecord, step responsibility.AutoplanStep, missionID, taskID string) (*agentcore.ExecutionEnvelope, error) {
	tenantID := strings.TrimSpace(plan.TenantID)
	missionID = strings.TrimSpace(missionID)
	taskID = strings.TrimSpace(taskID)
	agentID := strings.TrimSpace(step.AssigneeAgentID)
	if tenantID == "" || missionID == "" || taskID == "" || agentID == "" {
		return nil, fmt.Errorf("autonomous-plan task execution envelope identity is incomplete")
	}
	if declaredAgent := strings.TrimSpace(plan.AgentProfileID); declaredAgent != "" && declaredAgent != agentID {
		return nil, fmt.Errorf("autonomous-plan task Agent does not match the approved portfolio")
	}
	issuedAt := plan.UpdatedAt
	if issuedAt.IsZero() {
		issuedAt = plan.CreatedAt
	}
	if issuedAt.IsZero() {
		// Older plan fixtures do not carry timestamps. A fixed fallback keeps
		// idempotent re-materialization's contract fingerprint stable.
		issuedAt = time.Unix(0, 0).UTC()
	}
	envelope := &agentcore.ExecutionEnvelope{
		TenantID:           tenantID,
		RunID:              "autoplan-task:" + taskID,
		MissionID:          missionID,
		TaskID:             taskID,
		AgentID:            agentID,
		AgentProfileID:     agentID,
		PrincipalID:        agentID,
		PrincipalKind:      agentcore.ExecutionPrincipalAgent,
		PortfolioID:        strings.TrimSpace(plan.PortfolioID),
		PortfolioBindingID: strings.TrimSpace(plan.PortfolioBindingID),
		PortfolioVersionID: strings.TrimSpace(plan.PortfolioVersionID),
		ResponsibilityID:   responsibility.AssignmentID(tenantID, responsibility.WorkKindMissionTask, taskID),
		PolicyID:           strings.TrimSpace(plan.PolicyID),
		PolicyVersion:      plan.PolicyVersion,
		PermissionScopes:   append([]string(nil), step.ExecutionScopes...),
	}
	if err := envelope.SealAt(issuedAt); err != nil {
		return nil, fmt.Errorf("seal autonomous-plan task execution envelope: %w", err)
	}
	return envelope, nil
}

func responsibilityAutoplanEvidenceRequirements(step responsibility.AutoplanStep) []missioncontrol.EvidenceRequirementCommand {
	result := make([]missioncontrol.EvidenceRequirementCommand, 0, len(step.EvidenceRequirements))
	for _, requirement := range step.EvidenceRequirements {
		result = append(result, missioncontrol.EvidenceRequirementCommand{
			ID: requirement.EvidenceRequirementID, Description: requirement.AcceptanceCriterion,
			Kind: requirement.Kind, Required: requirement.Required,
		})
	}
	return result
}

func responsibilityAutoplanMaterializationKey(plan responsibility.AutoplanRecord, stepID string) string {
	payload := strings.Join([]string{
		strings.TrimSpace(plan.TenantID), strings.TrimSpace(plan.ID), strings.TrimSpace(plan.ProposalDigest), strings.TrimSpace(stepID),
	}, "\x00")
	digest := sha256.Sum256([]byte(payload))
	return "responsibility-autoplan:" + hex.EncodeToString(digest[:12])
}

func responsibilityAutoplanTaskID(plan responsibility.AutoplanRecord, stepID string) string {
	return "autoplan-task-" + strings.TrimPrefix(responsibilityAutoplanMaterializationKey(plan, stepID), "responsibility-autoplan:")
}

func responsibilityAutoplanTaskScope(plan responsibility.AutoplanRecord, step responsibility.AutoplanStep) string {
	scope := "Responsibility autonomous plan " + plan.ID + " step " + step.StepID
	if parentGoalRef := strings.TrimSpace(step.ParentGoalRef); parentGoalRef != "" {
		scope += " derived from " + parentGoalRef
	}
	if description := strings.TrimSpace(step.Description); description != "" {
		scope += ": " + description
	}
	return scope
}

func responsibilityAutoplanTaskPriority(priority int) string {
	switch {
	case priority >= 90:
		return "critical"
	case priority >= 75:
		return "high"
	case priority <= 25:
		return "low"
	default:
		return "normal"
	}
}
