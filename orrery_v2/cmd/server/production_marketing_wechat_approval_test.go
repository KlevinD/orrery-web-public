package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/testkit"
)

func TestRegisterProductionMarketingWechatApprovalResolutionRejectsPartialConfiguration(t *testing.T) {
	if err := registerProductionMarketingWechatApprovalResolution(nil, nil, "", "", ""); err != nil {
		t.Fatalf("disabled registration: %v", err)
	}
	if err := registerProductionMarketingWechatApprovalResolution(nil, nil, "Bearer submit", "", ""); err == nil ||
		!strings.Contains(err.Error(), "must be complete") {
		t.Fatalf("partial registration error = %v", err)
	}
}

func TestProductionMarketingWechatApprovalObserverEnqueuesOneBoundResolution(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	observer := productionMarketingWechatApprovalObserver{queue: env.ExecutionQueue}
	resolution := marketingWechatApprovalResolutionForTest()

	observer.DecisionResolved(context.Background(), hil.DecisionResolution{Decision: hil.DecisionRequest{ActionTypeID: "unrelated"}})
	observer.DecisionResolved(context.Background(), resolution)
	observer.DecisionResolved(context.Background(), resolution)

	jobID := productionMarketingWechatApprovalResolutionJobPrefix + resolution.Receipt.ID
	job, ok, err := env.ExecutionQueue.Get(context.Background(), jobID)
	if err != nil || !ok {
		t.Fatalf("job=%+v found=%t err=%v", job, ok, err)
	}
	if job.Topic != productionMarketingWechatApprovalResolutionTopic || job.QueueKey != "marketing-wechat-generation:scheduled-run-1" || job.MaxAttempts != 12 {
		t.Fatalf("job = %+v", job)
	}
	var payload productionMarketingWechatApprovalResolutionPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.GenerationID != "scheduled-run-1" || payload.DecisionID != "decision-1" || payload.WorkflowInstanceID != "workflow-1" ||
		payload.ReceiptID != "receipt-1" || payload.Outcome != "approved" || payload.ContentHash != "sha256:content" || payload.MaterialHash != "sha256:material" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestProductionMarketingWechatApprovalObserverEnqueuesChangesRequested(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	observer := productionMarketingWechatApprovalObserver{queue: env.ExecutionQueue}
	resolution := marketingWechatApprovalResolutionForTest()
	resolution.Decision.Status = hil.DecisionPending
	resolution.Receipt.ID = "receipt-changes-1"
	resolution.Receipt.Effect = hil.EffectRequiresApproval
	resolution.Receipt.SingleUse = false
	resolution.Reason = "add missing evidence"

	observer.DecisionResolved(context.Background(), resolution)
	job, ok, err := env.ExecutionQueue.Get(context.Background(), productionMarketingWechatApprovalResolutionJobPrefix+resolution.Receipt.ID)
	if err != nil || !ok {
		t.Fatalf("job=%+v found=%t err=%v", job, ok, err)
	}
	var payload productionMarketingWechatApprovalResolutionPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Outcome != "changes_requested" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestProductionMarketingWechatApprovalDeliveryPostsBoundResolution(t *testing.T) {
	var authorization string
	var received productionMarketingWechatApprovalResolutionPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		authorization = req.Header.Get("Authorization")
		if err := json.NewDecoder(req.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	payload := productionMarketingWechatApprovalResolutionPayload{
		GenerationID: "scheduled-run-1", DecisionID: "decision-1", WorkflowInstanceID: "workflow-1", ReceiptID: "receipt-1", Outcome: "approved",
	}
	encoded, _ := json.Marshal(payload)
	handler := productionMarketingWechatApprovalResolutionDelivery(server.Client(), server.URL, "Bearer callback-secret")
	if err := handler(context.Background(), platform.ExecutionDelivery{Payload: encoded}); err != nil {
		t.Fatalf("delivery: %v", err)
	}
	if authorization != "Bearer callback-secret" || received.ReceiptID != payload.ReceiptID || received.GenerationID != payload.GenerationID {
		t.Fatalf("authorization=%q received=%+v", authorization, received)
	}
}

func marketingWechatApprovalResolutionForTest() hil.DecisionResolution {
	return hil.DecisionResolution{
		Decision: hil.DecisionRequest{
			ID: "decision-1", TenantID: "system", Action: hil.MarketingWechatDraftApprovalActionType, ActionTypeID: hil.MarketingWechatDraftApprovalActionType,
			Resource: hil.ObjectRef{Kind: hil.MarketingWechatGenerationResourceKind, ID: "scheduled-run-1"}, ArgsHash: "sha256:args", Status: hil.DecisionResolved,
			PayloadPreview: map[string]any{"generation_id": "scheduled-run-1", "account_id": "wechat-main", "content_hash": "sha256:content", "asset_hashes": []string{"sha256:cover"}},
		},
		Receipt: hil.DecisionReceipt{ID: "receipt-1", WorkflowInstanceID: "workflow-1", ArgsHash: "sha256:args", MaterialHash: "sha256:material", Effect: hil.EffectAuthorize, SingleUse: true},
		Actor:   hil.ActorRef{Kind: hil.ActorHuman, ID: "H000001"}, Reason: "approved for draft creation",
	}
}

func TestProductionMarketingWechatApprovalReplacementCallbackPreservesContentBinding(t *testing.T) {
	resolution := marketingWechatApprovalResolutionForTest()
	resolution.Decision.Status = hil.DecisionDenied
	resolution.Decision.PayloadPreview["submission_id"] = "submission-2"
	resolution.Decision.PayloadPreview["project_id"] = "project-1"
	resolution.Decision.PayloadPreview["content_revision"] = float64(2)
	resolution.Decision.PayloadPreview["bundle_hash"] = "sha256:bundle2"
	resolution.Receipt.Effect = hil.EffectDeny
	resolution.Receipt.SingleUse = false
	resolution.Receipt.ResolverSummary = map[string]any{"lifecycle_outcome": "superseded", "replacement_submission_id": "submission-3"}
	resolution.Actor = hil.ActorRef{Kind: hil.ActorService, ID: "marketing"}
	payload, err := productionMarketingWechatApprovalPayload(resolution)
	if err != nil {
		t.Fatal(err)
	}
	if payload.Outcome != "superseded" || payload.ContentRevision != 2 || payload.SubmissionID != "submission-2" || payload.BundleHash != "sha256:bundle2" || payload.Actor.Kind != hil.ActorService {
		t.Fatalf("replacement callback: %+v", payload)
	}
}
