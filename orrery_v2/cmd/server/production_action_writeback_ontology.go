package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/platform/graphprotocol"
)

func (s *productionAgentExecutionOntologySink) ProjectWritebackReceipt(ctx context.Context, run actioncontract.ActionRun, receipt actioncontract.WritebackReceipt) error {
	if s == nil || s.runner == nil {
		return fmt.Errorf("agent execution ontology projection runner is required")
	}
	event, err := actionWritebackOntologyEvent(run, receipt)
	if err != nil {
		return err
	}
	return projectActionOntologyEvents(ctx, []graphprotocol.DomainEvent{event}, productionActionOntologyProjectionRetryDelays, s.apply)
}

func actionWritebackOntologyEvent(run actioncontract.ActionRun, receipt actioncontract.WritebackReceipt) (graphprotocol.DomainEvent, error) {
	eventType := ""
	switch receipt.Status {
	case actioncontract.WritebackStatusSucceeded:
		eventType = "action.writeback.succeeded"
	case actioncontract.WritebackStatusFailed:
		eventType = "action.writeback.failed"
	default:
		return graphprotocol.DomainEvent{}, fmt.Errorf("action writeback status %q is not supported by ontology projection", receipt.Status)
	}
	if strings.TrimSpace(string(receipt.ID)) == "" || strings.TrimSpace(string(receipt.ActionRunID)) == "" {
		return graphprotocol.DomainEvent{}, fmt.Errorf("action writeback receipt and action run ids are required for ontology projection")
	}
	revision, occurredAt := timeProjectionRevision(receipt.CreatedAt)
	return graphprotocol.DomainEvent{
		ID:       "action-writeback-" + string(receipt.Status) + ":" + string(receipt.ID) + ":" + strconv.FormatInt(revision, 10),
		TenantID: receipt.TenantID, Type: eventType, StreamID: string(receipt.ActionRunID), Version: revision, OccurredAt: occurredAt,
		Payload: map[string]any{
			"action_run_id": string(receipt.ActionRunID), "receipt_id": string(receipt.ID),
			"status": string(receipt.Status), "target_system": receipt.TargetSystem,
		},
		PublishedBy:     graphprotocol.ActorRef{Kind: string(run.Actor.Kind), ID: run.Actor.ID},
		SourceAccessRef: trustedOntologySourceRef("action_writeback", string(receipt.ID), receipt.TenantID),
	}, nil
}
