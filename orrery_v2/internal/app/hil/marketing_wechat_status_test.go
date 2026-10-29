package hil

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMarketingWechatStatusReadsExactCommittedReceiptAndOwner(t *testing.T) {
	at := time.Date(2026, 9, 9, 15, 47, 0, 0, time.UTC)
	decision := DecisionRequest{ID: "decision-1", TenantID: "system", Requester: ActorRef{Kind: ActorService, ID: "marketing"}, ActionTypeID: MarketingWechatDraftApprovalActionType, Resource: ObjectRef{Kind: MarketingWechatGenerationResourceKind, ID: "run-1"}, ArgsHash: "sha256:args", Status: DecisionResolved, PayloadPreview: map[string]any{"generation_id": "run-1", "account_id": "oa-1", "content_hash": "sha256:content", "asset_hashes": []string{"sha256:cover"}, "submission_id": "submission-1", "project_id": "project-1", "content_revision": 2, "bundle_hash": "sha256:bundle"}}
	receipt := DecisionReceipt{ID: "receipt-1", RequestID: "decision-1", WorkflowInstanceID: "workflow-1", ArgsHash: "sha256:args", MaterialHash: "sha256:material", Effect: EffectAuthorize, SingleUse: true, ResolverActorKind: ActorHuman, ResolverActorID: "reviewer-1", ResolverReason: "approved", CreatedAt: at}
	reader := &recordingMarketingWechatApprovalWorkflowReader{detail: WorkflowInstanceDetail{Decision: decision, Instance: WorkflowInstance{ID: "workflow-1", Status: WorkflowApproved}, Receipts: []DecisionReceipt{receipt}}}
	service := NewMarketingWechatApprovalService(nil, reader, nil)
	got, err := service.GetMarketingWechatApprovalStatus(context.Background(), "system", "marketing", "decision-1")
	if err != nil || got.Status != "approved" || got.Resolution == nil || got.Resolution.ReceiptID != "receipt-1" || got.Resolution.ContentRevision != 2 || got.Resolution.Actor.ID != "reviewer-1" || got.DecidedAt == nil || !got.DecidedAt.Equal(at) {
		t.Fatalf("status=%+v err=%v", got, err)
	}
	expected, err := BuildMarketingWechatApprovalResolution(DecisionResolution{Decision: decision, Receipt: receipt, Actor: ActorRef{Kind: ActorHuman, ID: "reviewer-1"}, Reason: "approved"})
	if err != nil || got.Resolution.ReceiptID != expected.ReceiptID || got.Resolution.Outcome != expected.Outcome {
		t.Fatal("status and callback disagree")
	}
	for _, ids := range [][2]string{{"other", "marketing"}, {"system", "other"}} {
		if _, err := service.GetMarketingWechatApprovalStatus(context.Background(), ids[0], ids[1], "decision-1"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("ownership error=%v", err)
		}
	}
	reader.detail.Receipts = nil
	if _, err := service.GetMarketingWechatApprovalStatus(context.Background(), "system", "marketing", "decision-1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("approved without a receipt: %v", err)
	}
	reader.detail.Decision.Status = DecisionPending
	reader.detail.Instance.Status = WorkflowRunning
	got, err = service.GetMarketingWechatApprovalStatus(context.Background(), "system", "marketing", "decision-1")
	if err != nil || got.Status != "pending" || got.Resolution != nil {
		t.Fatalf("pending=%+v %v", got, err)
	}
	for _, outcome := range []WorkflowInstanceStatus{WorkflowRejected, WorkflowChangesRequested, WorkflowExpired, WorkflowSuperseded} {
		reader.detail.Decision = decision
		reader.detail.Decision.Status = DecisionDenied
		reader.detail.Instance.Status = outcome
		candidate := receipt
		candidate.Effect, candidate.SingleUse = EffectDeny, false
		if outcome == WorkflowChangesRequested {
			reader.detail.Decision.Status = DecisionPending
			candidate.Effect = EffectRequiresApproval
		}
		if outcome == WorkflowExpired {
			candidate.ResolverActorKind, candidate.ResolverActorID, candidate.ResolverReason = ActorService, "workflow-sweeper", "approval deadline expired"
		}
		if outcome == WorkflowSuperseded {
			candidate.ResolverSummary = map[string]any{"lifecycle_outcome": "superseded"}
		}
		reader.detail.Receipts = []DecisionReceipt{candidate}
		got, err = service.GetMarketingWechatApprovalStatus(context.Background(), "system", "marketing", "decision-1")
		if err != nil || got.Resolution == nil || got.Resolution.Outcome != string(outcome) {
			t.Fatalf("outcome %s: %+v %v", outcome, got, err)
		}
	}
}
