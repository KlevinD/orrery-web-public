package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"orrery_v2/internal/app/problemhub"
	"orrery_v2/internal/app/reliability"
	"orrery_v2/internal/platform/graphprotocol"
)

const (
	productionProblemCaseUpsertedEventType     = "problem.case.upserted"
	productionLessonCandidateProposedEventType = "problem.lesson_candidate.proposed"
)

func (s *productionAgentExecutionOntologySink) mirrorReliabilityProblemEvent(ctx context.Context, event reliability.Event) error {
	if s == nil || s.problems == nil {
		return nil
	}
	switch event.Type {
	case reliability.EventFailureObserved:
		incidentID := firstNonBlank(payloadString(event.Payload, "id"), event.StreamID)
		incidentVersion := payloadInt64(event.Payload, "incident_version")
		evidenceID := payloadString(event.Payload, "evidence_id")
		if evidenceID == "" {
			evidenceID = fmt.Sprintf("failure-observation-evidence:%s:%d", incidentID, incidentVersion)
		}
		_, err := s.problems.Observe(ctx, problemhub.Observation{
			ID:       firstNonBlank(payloadString(event.Payload, "observation_id"), event.ID),
			TenantID: event.TenantID, Fingerprint: payloadString(event.Payload, "fingerprint"),
			SourceDomain: "reliability", Source: problemhub.ObjectRef{Kind: "failure_incident", ID: incidentID},
			Category: payloadString(event.Payload, "category"), Severity: payloadString(event.Payload, "severity"),
			Title:           strings.TrimSpace(payloadString(event.Payload, "component") + " " + payloadString(event.Payload, "error_code")),
			RedactedSummary: payloadString(event.Payload, "redacted_summary"), EvidenceID: evidenceID,
			Actor: problemActor(event.Actor), CorrelationID: event.CorrelationID, CausationID: event.CausationID,
			OccurredAt: event.OccurredAt,
		})
		return err
	case reliability.EventFailureStatusChanged:
		status, ok := reliabilityProblemStatus(payloadString(event.Payload, "status"))
		if !ok {
			return fmt.Errorf("unsupported reliability problem status %q", payloadString(event.Payload, "status"))
		}
		_, err := s.problems.SyncSourceStatus(ctx, event.TenantID,
			problemhub.ObjectRef{Kind: "failure_incident", ID: firstNonBlank(payloadString(event.Payload, "id"), event.StreamID)},
			status, problemActor(event.Actor), event.OccurredAt)
		return err
	case reliability.EventRepairRuleUpserted:
		outcomeID := payloadString(event.Payload, "outcome_id")
		if outcomeID == "" {
			return nil
		}
		_, err := s.problems.RecordOutcome(ctx, problemhub.Outcome{
			ID: outcomeID, TenantID: event.TenantID,
			Source:  problemhub.ObjectRef{Kind: "failure_incident", ID: payloadString(event.Payload, "outcome_incident_id")},
			Success: payloadBool(event.Payload, "outcome_success"),
			Resolution: problemhub.ObjectRef{
				Kind: payloadString(event.Payload, "outcome_resolution_kind"),
				ID:   payloadString(event.Payload, "outcome_resolution_id"),
			},
			RedactedSummary: payloadString(event.Payload, "outcome_summary"),
			EvidenceID:      payloadString(event.Payload, "outcome_evidence_id"), Actor: problemActor(event.Actor),
			CorrelationID: event.CorrelationID, CausationID: event.CausationID, OccurredAt: event.OccurredAt,
		})
		return err
	default:
		return nil
	}
}

func (s *productionAgentExecutionOntologySink) PublishProblemHubEvent(ctx context.Context, event problemhub.Event) error {
	domainEvent, err := productionProblemHubDomainEvent(event)
	if err != nil {
		return err
	}
	return s.apply(ctx, domainEvent)
}

func (s *productionAgentExecutionOntologySink) ReplayProblemHubEvent(ctx context.Context, event problemhub.Event) error {
	domainEvent, err := productionProblemHubDomainEvent(event)
	if err != nil {
		return err
	}
	return s.replayRetainedOntologyEvent(ctx, domainEvent)
}

func productionProblemHubDomainEvent(event problemhub.Event) (graphprotocol.DomainEvent, error) {
	if event.Type != productionProblemCaseUpsertedEventType && event.Type != productionLessonCandidateProposedEventType {
		return graphprotocol.DomainEvent{}, fmt.Errorf("unsupported problem hub ontology event type %q", event.Type)
	}
	actor := graphprotocol.ActorRef{Kind: event.Actor.Kind, ID: event.Actor.ID}
	if strings.TrimSpace(actor.Kind) == "" || strings.TrimSpace(actor.ID) == "" {
		actor = graphprotocol.ActorRef{Kind: "service", ID: "problem-hub"}
	}
	sourceKind := "problem_case"
	if event.Type == productionLessonCandidateProposedEventType {
		sourceKind = "lesson_candidate"
	}
	return graphprotocol.DomainEvent{
		ID: event.ID, SourceEventID: event.ID, TenantID: event.TenantID, Type: event.Type,
		SchemaVersion: "v1", StreamID: event.StreamID, Version: event.Version,
		CorrelationID: event.CorrelationID, CausationID: event.CausationID,
		OccurredAt: event.OccurredAt, Payload: event.Payload, PublishedBy: actor,
		SourceAccessRef: trustedOntologySourceRef(sourceKind, event.StreamID, event.TenantID),
	}, nil
}

func (s *productionAgentExecutionOntologySink) replayRetainedOntologyEvent(ctx context.Context, event graphprotocol.DomainEvent) error {
	if s.events.store == nil {
		_, err := s.runner.ApplyEvent(ctx, event)
		return err
	}
	retained, err := s.events.Get(ctx, event.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return s.apply(ctx, event)
	}
	if err != nil {
		return fmt.Errorf("load retained ontology event %s: %w", event.ID, err)
	}
	if _, err := s.runner.ApplyEvent(ctx, retained); err != nil {
		return fmt.Errorf("replay ontology event %s: %w", retained.Type, err)
	}
	return nil
}

func reliabilityProblemStatus(value string) (problemhub.Status, bool) {
	switch strings.TrimSpace(value) {
	case string(reliability.IncidentStatusOpen):
		return problemhub.StatusOpen, true
	case string(reliability.IncidentStatusContained):
		return problemhub.StatusContained, true
	case string(reliability.IncidentStatusDiagnosed):
		return problemhub.StatusDiagnosed, true
	case string(reliability.IncidentStatusRepairing):
		return problemhub.StatusInProgress, true
	case string(reliability.IncidentStatusMonitoring):
		return problemhub.StatusMonitoring, true
	case string(reliability.IncidentStatusResolved):
		return problemhub.StatusResolved, true
	case string(reliability.IncidentStatusEscalated):
		return problemhub.StatusEscalated, true
	case string(reliability.IncidentStatusQuarantined):
		return problemhub.StatusQuarantined, true
	default:
		return "", false
	}
}

func problemActor(actor reliability.ActorRef) problemhub.ActorRef {
	return problemhub.ActorRef{Kind: actor.Kind, ID: actor.ID}
}

func payloadString(payload map[string]any, key string) string {
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func payloadInt64(payload map[string]any, key string) int64 {
	value, ok := payload[key]
	if !ok || value == nil {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed
	default:
		return 0
	}
}

func payloadBool(payload map[string]any, key string) bool {
	value, ok := payload[key]
	if !ok || value == nil {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		parsed, _ := strconv.ParseBool(strings.TrimSpace(typed))
		return parsed
	default:
		return false
	}
}

var _ problemhub.EventSink = (*productionAgentExecutionOntologySink)(nil)
var _ problemhub.EventReplaySink = (*productionAgentExecutionOntologySink)(nil)
