package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/hil"
)

type recordingProductionActionDecisionConsumer struct {
	input hil.ActionReceiptConsumeInput
	calls int
	err   error
}

func (c *recordingProductionActionDecisionConsumer) ConsumeActionDecision(_ context.Context, input hil.ActionReceiptConsumeInput) (hil.DecisionReceipt, error) {
	c.calls++
	c.input = input
	if c.err != nil {
		return hil.DecisionReceipt{}, c.err
	}
	return hil.DecisionReceipt{ID: input.ReceiptID, RequestID: input.DecisionID, TenantID: input.TenantID}, nil
}

func TestProductionActionHILAuthorizerBindsCompleteActionMaterial(t *testing.T) {
	consumer := &recordingProductionActionDecisionConsumer{}
	authorizer := productionActionHILAuthorizer{decisions: consumer}
	request := actioncontract.StartRunRequest{
		TenantID: "tenant-a", ActionTypeID: productionRepairActionTypeID,
		Actor:            actioncontract.ActionSubject{Kind: actioncontract.SubjectHuman, ID: "operator-1"},
		Approval:         actioncontract.Ref{Kind: "approval", ID: "decision-1"},
		PolicyDecisionID: "decision-1", HILTokenID: "receipt-1", ExecutionMode: actioncontract.ExecutionModeDryRun,
		ObjectRefs: []actioncontract.ObjectRef{{Kind: "repair_rule", ID: "rule-1"}, {Kind: "failure_incident", ID: "incident-1"}},
		ArgsHash:   "sha256:repair", Purpose: "validate service recovery",
	}
	actionType := actioncontract.ActionType{ID: productionRepairActionTypeID, RiskLevel: actioncontract.RiskHigh}

	if err := authorizer.VerifyActionAuthorization(context.Background(), request, actionType); err != nil {
		t.Fatalf("VerifyActionAuthorization: %v", err)
	}
	want := hil.ActionReceiptConsumeInput{
		TenantID: "tenant-a", DecisionID: "decision-1", ReceiptID: "receipt-1",
		Actor: hil.ActorRef{Kind: hil.ActorHuman, ID: "operator-1"}, Action: "ontology.action.dry_run",
		ActionTypeID: "reliability.repair.execute",
		ObjectRefs:   []hil.ObjectRef{{Kind: "repair_rule", ID: "rule-1"}, {Kind: "failure_incident", ID: "incident-1"}},
		ArgsHash:     "sha256:repair", Purpose: "validate service recovery", RiskLevel: hil.RiskHigh,
	}
	if consumer.calls != 1 || !reflect.DeepEqual(consumer.input, want) {
		t.Fatalf("consumed action = %+v calls=%d, want %+v", consumer.input, consumer.calls, want)
	}
}

func TestProductionActionHILAuthorizerRejectsUnboundOrReplayedReceipt(t *testing.T) {
	consumer := &recordingProductionActionDecisionConsumer{}
	authorizer := productionActionHILAuthorizer{decisions: consumer}
	request := actioncontract.StartRunRequest{
		TenantID: "tenant-a", Actor: actioncontract.ActionSubject{Kind: actioncontract.SubjectHuman, ID: "operator-1"},
		Approval: actioncontract.Ref{Kind: "approval", ID: "another-decision"}, PolicyDecisionID: "decision-1",
		HILTokenID: "receipt-1", ExecutionMode: actioncontract.ExecutionModeDryRun,
		ObjectRefs: []actioncontract.ObjectRef{{Kind: "failure_incident", ID: "incident-1"}}, ArgsHash: "sha256:repair",
	}
	actionType := actioncontract.ActionType{ID: productionRepairActionTypeID, RiskLevel: actioncontract.RiskHigh}
	if err := authorizer.VerifyActionAuthorization(context.Background(), request, actionType); err == nil {
		t.Fatal("mismatched approval was accepted")
	}
	if consumer.calls != 0 {
		t.Fatalf("receipt consumer calls = %d, want 0", consumer.calls)
	}

	request.Approval.ID = request.PolicyDecisionID
	consumer.err = hil.ErrConflict
	if err := authorizer.VerifyActionAuthorization(context.Background(), request, actionType); !errors.Is(err, hil.ErrConflict) {
		t.Fatalf("replayed receipt error = %v, want HIL conflict", err)
	}
}
