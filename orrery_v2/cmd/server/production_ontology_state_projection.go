package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform/graphprotocol"
)

type productionOntologyStateProjector struct {
	runner  *ontology.MappingRuleProjectionRunner
	store   *ontology.PlatformStateProjectionStore
	now     func() time.Time
	metrics ontologyMetricRecorder
}

type ontologyMetricRecorder interface {
	CaptureContext(context.Context, string, map[string]any) bool
}

func isProductionOntologyStateEvent(eventType string) bool {
	switch eventType {
	case "agent.task.upserted",
		"scheduler.task.created",
		"scheduler.task.updated",
		"scheduler.task.cancelled",
		"people.agent.updated",
		"scheduler.task.agent_targeted",
		"agent.version.observed",
		"improvement.case.upserted",
		"improvement.change_set.upserted",
		"improvement.resource_version.upserted",
		"improvement.experiment.upserted",
		"improvement.evaluation.upserted",
		"improvement.release.upserted",
		"improvement.outcome.upserted":
		return true
	default:
		return false
	}
}

func (p productionOntologyStateProjector) Apply(ctx context.Context, event graphprotocol.DomainEvent) (ontology.StateProjectionReport, error) {
	return p.apply(ctx, event, false)
}

func (p productionOntologyStateProjector) ApplyAuthoritativeCurrentState(ctx context.Context, event graphprotocol.DomainEvent) (ontology.StateProjectionReport, error) {
	return p.apply(ctx, event, true)
}

func (p productionOntologyStateProjector) apply(ctx context.Context, event graphprotocol.DomainEvent, authoritativeCurrentState bool) (ontology.StateProjectionReport, error) {
	if p.runner == nil || p.store == nil || p.now == nil {
		return ontology.StateProjectionReport{}, fmt.Errorf("production ontology state projector is not configured")
	}
	if !isProductionOntologyStateEvent(event.Type) {
		return ontology.StateProjectionReport{}, fmt.Errorf("ontology event %s is not a production state event", event.Type)
	}
	entityID := strings.TrimSpace(event.StreamID)
	if entityID == "" {
		return ontology.StateProjectionReport{}, fmt.Errorf("ontology state event %s requires a stream id", event.Type)
	}
	report, err := p.runner.ApplyStateEvent(ctx, p.store, ontology.StateProjectionEvent{
		EntityID:                  entityID,
		SnapshotAt:                p.now().UTC(),
		AuthoritativeCurrentState: authoritativeCurrentState,
		Event:                     event,
	})
	if err != nil {
		return ontology.StateProjectionReport{}, fmt.Errorf("project ontology state %s: %w", event.Type, err)
	}
	if p.metrics != nil {
		p.metrics.CaptureContext(ctx, "ontology.state_projection", map[string]any{
			"event_type": event.Type,
			"outcome":    string(report.Outcome),
		})
	}
	return report, nil
}
