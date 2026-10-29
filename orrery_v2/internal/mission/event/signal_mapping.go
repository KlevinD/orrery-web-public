package event

import (
	"fmt"
	"strings"

	"orrery_v2/internal/agent/lifecycle"
	missioncore "orrery_v2/internal/mission/core"
)

type SignalMapper struct {
	ignored map[lifecycle.SignalName]struct{}
}

func NewSignalMapper() SignalMapper {
	return SignalMapper{ignored: map[lifecycle.SignalName]struct{}{
		lifecycle.BeforeTaskReceived:    {},
		lifecycle.AfterTaskReceived:     {},
		lifecycle.BeforeTaskAccepted:    {},
		lifecycle.BeforeContextBuild:    {},
		lifecycle.AfterContextBuild:     {},
		lifecycle.BeforeRuntimeStart:    {},
		lifecycle.RuntimeStarted:        {},
		lifecycle.RuntimeProgress:       {},
		lifecycle.BeforeRuntimeFinish:   {},
		lifecycle.AfterRuntimeFinish:    {},
		lifecycle.BeforeResultNormalize: {},
		lifecycle.AfterResultNormalize:  {},
		lifecycle.BeforeResultValidate:  {},
		lifecycle.AfterResultValidate:   {},
		lifecycle.BeforeResultCommit:    {},
		lifecycle.BeforeAgentReply:      {},
	}}
}

func (m SignalMapper) Map(signal lifecycle.SignalEnvelope) ([]Envelope, error) {
	if err := signal.Validate(); err != nil {
		return nil, err
	}
	tenantID := strings.TrimSpace(signal.TenantID)
	if tenantID == "" {
		tenantID = "global"
	}
	base := Envelope{
		TenantID:      tenantID,
		MissionID:     signal.MissionID,
		StreamID:      MissionStreamID(tenantID, signal.MissionID),
		Actor:         ActorRef{Kind: ActorKindAgent, ID: signal.AgentProfileID.String()},
		CausationID:   string(signal.CausationID),
		CorrelationID: string(signal.CorrelationID),
		OccurredAt:    signal.EmittedAt,
		Metadata: map[string]string{
			"source_signal_id": string(signal.SignalID),
			"source_signal":    string(signal.Name),
		},
	}
	switch signal.Name {
	case lifecycle.AfterTaskAccepted:
		payload, ok := signal.Payload.(lifecycle.TaskAcceptedPayload)
		if !ok {
			return nil, fmt.Errorf("%w: AfterTaskAccepted requires TaskAcceptedPayload", ErrInvalidEnvelope)
		}
		base.EventID = eventID(signal, EventTaskAccepted, signal.TaskID.String())
		base.EventType = EventTaskAccepted
		base.EventVersion = 1
		base.IdempotencyKey = idempotency(signal, EventTaskAccepted, signal.TaskID.String())
		base.Payload = TaskAcceptedPayload{TaskID: signal.TaskID, AcceptedBy: payload.AcceptedBy}
		return assignCandidateVersions([]Envelope{base}), nil
	case lifecycle.AfterResultCommit:
		return mapResultCommitted(signal, base)
	case lifecycle.AgentBlocked:
		payload, ok := signal.Payload.(lifecycle.FailurePayload)
		if !ok {
			return nil, fmt.Errorf("%w: AgentBlocked requires FailurePayload", ErrInvalidEnvelope)
		}
		base.EventID = eventID(signal, EventTaskBlocked, signal.TaskID.String())
		base.EventType = EventTaskBlocked
		base.EventVersion = 1
		base.IdempotencyKey = idempotency(signal, EventTaskBlocked, signal.TaskID.String())
		base.Payload = TaskStatusPayload{TaskID: signal.TaskID, Reason: payload.Code, Message: payload.Message}
		return assignCandidateVersions([]Envelope{base}), nil
	case lifecycle.AgentFailed:
		event, err := runtimeEvent(signal, base, EventAgentRunFailed)
		if err != nil {
			return nil, err
		}
		return assignCandidateVersions([]Envelope{event}), nil
	case lifecycle.AgentTimeout:
		event, err := runtimeEvent(signal, base, EventAgentRunTimedOut)
		if err != nil {
			return nil, err
		}
		return assignCandidateVersions([]Envelope{event}), nil
	case lifecycle.AgentCancelled:
		event, err := runtimeEvent(signal, base, EventAgentRunCancelled)
		if err != nil {
			return nil, err
		}
		return assignCandidateVersions([]Envelope{event}), nil
	case lifecycle.AfterAgentReply:
		payload, ok := signal.Payload.(lifecycle.AgentReplyPayload)
		if !ok {
			return []Envelope{}, nil
		}
		if payload.MessageRef == "" {
			return []Envelope{}, nil
		}
		base.EventID = eventID(signal, EventAgentReplied, payload.MessageRef)
		base.EventType = EventAgentReplied
		base.EventVersion = 1
		base.IdempotencyKey = idempotency(signal, EventAgentReplied, payload.MessageRef)
		base.Payload = MessagePostedPayload{MessageID: payload.MessageRef, AuthorID: signal.AgentID}
		return assignCandidateVersions([]Envelope{base}), nil
	default:
		if _, ok := m.ignoredSignals()[signal.Name]; ok {
			return []Envelope{}, nil
		}
		return nil, fmt.Errorf("%w: signal %s is not mapped to mission events", ErrInvalidEnvelope, signal.Name)
	}
}

func (m SignalMapper) ignoredSignals() map[lifecycle.SignalName]struct{} {
	if len(m.ignored) > 0 {
		return m.ignored
	}
	return NewSignalMapper().ignored
}

func mapResultCommitted(signal lifecycle.SignalEnvelope, base Envelope) ([]Envelope, error) {
	payload, ok := signal.Payload.(ResultCommittedPayload)
	if !ok {
		return nil, fmt.Errorf("%w: AfterResultCommit requires ResultCommittedPayload", ErrInvalidEnvelope)
	}
	result := payload.Result
	if result.MissionID.IsZero() {
		result.MissionID = signal.MissionID
	}
	if result.TaskID.IsZero() {
		result.TaskID = signal.TaskID
	}
	if result.AgentID.IsZero() {
		result.AgentID = signal.AgentID
	}
	events := []Envelope{withBase(base, eventID(signal, EventAgentResultSubmitted, result.ID.String()), EventAgentResultSubmitted, idempotency(signal, EventAgentResultSubmitted, result.ID.String()), AgentResultSubmittedPayload{
		TaskID:    result.TaskID,
		AgentID:   result.AgentID,
		ResultRef: result.ID.String(),
		Summary:   result.Summary.Text,
	})}
	for _, deliverable := range result.Deliverables {
		deliverableID := string(deliverable.ID)
		events = append(events, withBase(base, eventID(signal, EventDeliverableSubmitted, deliverableID), EventDeliverableSubmitted, idempotency(signal, EventDeliverableSubmitted, deliverableID), DeliverableSubmittedPayload{
			TaskID:                result.TaskID,
			AgentID:               result.AgentID,
			DeliverableID:         deliverable.ID,
			ExpectedDeliverableID: deliverable.ExpectedDeliverableID,
			Title:                 deliverable.Title,
			ArtifactRef:           deliverable.ArtifactRef,
		}))
	}
	for _, evidence := range result.Evidence {
		evidenceID := string(evidence.ID)
		events = append(events, withBase(base, eventID(signal, EventEvidenceSubmitted, evidenceID), EventEvidenceSubmitted, idempotency(signal, EventEvidenceSubmitted, evidenceID), EvidenceSubmittedPayload{
			TaskID:                result.TaskID,
			AgentID:               result.AgentID,
			EvidenceID:            evidence.ID,
			EvidenceRequirementID: evidence.EvidenceRequirementID,
			AcceptanceCriterionID: evidence.AcceptanceCriterionID,
			Kind:                  evidence.Kind,
			ArtifactRef:           evidence.ArtifactRef,
			ToolLogRef:            evidence.ToolLogRef,
			ExternalURL:           evidence.ExternalURL,
		}))
	}
	return assignCandidateVersions(events), nil
}

func runtimeEvent(signal lifecycle.SignalEnvelope, base Envelope, eventType EventType) (Envelope, error) {
	payload, ok := signal.Payload.(lifecycle.FailurePayload)
	if !ok {
		return Envelope{}, fmt.Errorf("%w: %s requires FailurePayload", ErrInvalidEnvelope, signal.Name)
	}
	return withBase(base, eventID(signal, eventType, signal.RunID.String()), eventType, idempotency(signal, eventType, signal.RunID.String()), RuntimeFailurePayload{
		TaskID:  signal.TaskID,
		AgentID: signal.AgentID,
		RunID:   signal.RunID,
		Code:    payload.Code,
		Message: payload.Message,
	}), nil
}

func withBase(base Envelope, eventID string, eventType EventType, key string, payload Payload) Envelope {
	base.EventID = eventID
	base.EventType = eventType
	base.EventVersion = payload.EventPayloadVersion()
	base.IdempotencyKey = key
	base.Payload = payload
	return base
}

func eventID(signal lifecycle.SignalEnvelope, eventType EventType, suffix string) string {
	return fmt.Sprintf("%s:%s:%s", signal.SignalID, eventType, suffix)
}

func idempotency(signal lifecycle.SignalEnvelope, eventType EventType, suffix string) string {
	if suffix == "" {
		suffix = missioncore.TaskID(signal.TaskID).String()
	}
	return fmt.Sprintf("%s:%s:%s", signal.MissionID, eventType, suffix)
}

func assignCandidateVersions(events []Envelope) []Envelope {
	for i := range events {
		events[i].StreamVersion = i + 1
	}
	return events
}
