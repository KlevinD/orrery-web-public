package main

import (
	"context"
	"fmt"
	"strings"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/hil"
)

const productionOntologyActionDecisionPrefix = "ontology.action."

type productionActionDecisionConsumer interface {
	ConsumeActionDecision(context.Context, hil.ActionReceiptConsumeInput) (hil.DecisionReceipt, error)
}

type productionActionHILAuthorizer struct {
	decisions productionActionDecisionConsumer
}

func (a productionActionHILAuthorizer) VerifyActionAuthorization(ctx context.Context, request actioncontract.StartRunRequest, actionType actioncontract.ActionType) error {
	if a.decisions == nil {
		return fmt.Errorf("HIL decision service is unavailable")
	}
	decisionID := strings.TrimSpace(request.PolicyDecisionID)
	if request.Approval.Kind != "approval" || strings.TrimSpace(request.Approval.ID) == "" || strings.TrimSpace(request.Approval.ID) != decisionID {
		return fmt.Errorf("approval must identify the bound HIL decision")
	}
	actor, err := productionHILActionActor(request.Actor)
	if err != nil {
		return err
	}
	risk, err := productionHILActionRisk(actionType.RiskLevel)
	if err != nil {
		return err
	}
	objectRefs := make([]hil.ObjectRef, 0, len(request.ObjectRefs))
	for _, ref := range request.ObjectRefs {
		objectRefs = append(objectRefs, hil.ObjectRef{Kind: ref.Kind, ID: ref.ID})
	}
	_, err = a.decisions.ConsumeActionDecision(ctx, hil.ActionReceiptConsumeInput{
		TenantID:     request.TenantID,
		DecisionID:   decisionID,
		ReceiptID:    request.HILTokenID,
		Actor:        actor,
		Action:       productionOntologyActionDecisionPrefix + string(request.ExecutionMode),
		ActionTypeID: string(actionType.ID),
		ObjectRefs:   objectRefs,
		ArgsHash:     request.ArgsHash,
		Purpose:      request.Purpose,
		RiskLevel:    risk,
	})
	if err != nil {
		return fmt.Errorf("consume bound HIL action receipt: %w", err)
	}
	return nil
}

func productionHILActionActor(subject actioncontract.ActionSubject) (hil.ActorRef, error) {
	actor := hil.ActorRef{ID: strings.TrimSpace(subject.ID)}
	switch subject.Kind {
	case actioncontract.SubjectHuman:
		actor.Kind = hil.ActorHuman
	case actioncontract.SubjectAgent:
		actor.Kind = hil.ActorAgent
	case actioncontract.SubjectService:
		actor.Kind = hil.ActorService
	default:
		return hil.ActorRef{}, fmt.Errorf("action actor kind %q cannot consume a HIL receipt", subject.Kind)
	}
	if actor.ID == "" {
		return hil.ActorRef{}, fmt.Errorf("action actor id is required")
	}
	return actor, nil
}

func productionHILActionRisk(risk actioncontract.RiskLevel) (hil.RiskLevel, error) {
	switch risk {
	case actioncontract.RiskHigh:
		return hil.RiskHigh, nil
	case actioncontract.RiskCritical:
		return hil.RiskCritical, nil
	default:
		return "", fmt.Errorf("risk level %q does not require HIL authorization", risk)
	}
}
