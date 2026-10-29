package hil

import (
	"context"
	"fmt"
)

func (s *Service) LookupMarketingWechatApproval(ctx context.Context, tenantID, businessKey string) (DecisionRequest, bool, error) {
	return s.findDecisionByBusinessKey(ctx, tenantID, businessKey)
}

// SupersedeMarketingApproval ends only the pending workflow owned by this
// material producer. The structured receipt distinguishes replacement from a
// human rejection. Retries must name the same replacement and material hash.
func (s *Service) SupersedeMarketingApproval(ctx context.Context, input MarketingWechatApprovalInput) error {
	repository, ok := s.repository.(WorkflowRepository)
	if !ok || input.SubmissionID == "" || input.SupersedesDecisionID == "" || input.BundleHash == "" {
		return ErrInvalidRequest
	}
	detail, err := repository.GetWorkflowInstanceByDecisionID(ctx, input.TenantID, input.SupersedesDecisionID)
	if err != nil {
		return err
	}
	decision := detail.Decision
	if decision.Requester != (ActorRef{Kind: ActorService, ID: input.RequesterID}) ||
		decision.ActionTypeID != MarketingWechatDraftApprovalActionType ||
		decision.Resource != (ObjectRef{Kind: MarketingWechatGenerationResourceKind, ID: input.GenerationID}) ||
		decision.PayloadPreview["account_id"] != input.AccountID {
		return ErrConflict
	}
	if project, exists := decision.PayloadPreview["project_id"]; exists && project != input.ProjectID {
		return ErrConflict
	}
	if detail.Instance.Status == WorkflowSuperseded {
		for _, receipt := range detail.Receipts {
			if receipt.ResolverSummary["lifecycle_outcome"] == "superseded" &&
				receipt.ResolverSummary["replacement_submission_id"] == input.SubmissionID &&
				receipt.ResolverSummary["replacement_bundle_hash"] == input.BundleHash {
				if s.observer != nil {
					s.observer.DecisionResolved(ctx, DecisionResolution{Decision: decision, Receipt: receipt, Actor: ActorRef{Kind: ActorService, ID: "marketing"}, Reason: receipt.ResolverReason})
				}
				return nil
			}
		}
		return ErrConflict
	}
	if decision.Status != DecisionPending || detail.Instance.Status != WorkflowRunning {
		return ErrConflict
	}
	now := s.now().UTC()
	receiptID, err := s.ids.NewIDContext()
	if err != nil {
		return err
	}
	eventID, err := s.ids.NewIDContext()
	if err != nil {
		return err
	}
	actor := ActorRef{Kind: ActorService, ID: input.RequesterID}
	reason := fmt.Sprintf("材料已由版本 %d 替换，请处理新版审批。", input.ContentRevision)
	summary := map[string]any{"lifecycle_outcome": "superseded", "replacement_submission_id": input.SubmissionID, "replacement_bundle_hash": input.BundleHash}
	receipt := DecisionReceipt{ID: receiptID, TenantID: input.TenantID, RequestID: decision.ID,
		WorkflowInstanceID: detail.Instance.ID, ArgsHash: decision.ArgsHash, MaterialHash: detail.Instance.MaterialHash,
		Effect: EffectDeny, CreatedAt: now, ResolverActorKind: actor.Kind, ResolverActorID: actor.ID, ResolverReason: reason, ResolverSummary: summary}
	tasks := []WorkflowTask{}
	for _, task := range detail.Tasks {
		if task.Status == TaskPending || task.Status == TaskScheduled {
			task.Status = TaskSuperseded
			task.UpdatedAt = now
			task.CompletedAt = now
			task.Reason = reason
			tasks = append(tasks, task)
		}
	}
	stages := []WorkflowStageInstance{}
	for _, stage := range detail.Stages {
		if stage.Status == WorkflowStageActive || stage.Status == WorkflowStageScheduled {
			stage.Status = WorkflowStageSuperseded
			stage.ResolvedAt = now
			stages = append(stages, stage)
		}
	}
	updated, err := repository.ApplyWorkflowCommand(ctx, WorkflowCommand{
		TenantID: input.TenantID, InstanceID: detail.Instance.ID, ExpectedInstanceVersion: detail.Instance.Version,
		Status: WorkflowSuperseded, UpdatedAt: now, DecisionStatus: DecisionDenied, Receipt: &receipt, Tasks: tasks, Stages: stages,
		IdempotencyKey:     "marketing-replace:" + input.SubmissionID,
		RequestFingerprint: stableFingerprint([]string{decision.ID, input.SubmissionID, input.BundleHash}),
		Audit: []WorkflowAuditEvent{{ID: eventID, TenantID: input.TenantID, CaseID: detail.Instance.CaseID, WorkflowInstanceID: detail.Instance.ID,
			Actor: actor, EventType: "workflow.superseded", Reason: reason, Metadata: summary, CreatedAt: now}},
	})
	if err != nil {
		return err
	}
	if s.observer != nil {
		s.observer.DecisionResolved(ctx, DecisionResolution{Decision: updated.Decision, Receipt: receipt, Actor: actor, Reason: reason})
	}
	return nil
}
