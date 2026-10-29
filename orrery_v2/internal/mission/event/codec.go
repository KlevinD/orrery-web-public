package event

import (
	"encoding/json"
	"fmt"
	"reflect"
)

var payloadFactories = map[EventType]func() Payload{
	EventMissionCreated:            func() Payload { return &MissionCreatedPayload{} },
	EventMissionGoalUpdated:        func() Payload { return &MissionGoalUpdatedPayload{} },
	EventMissionPaused:             func() Payload { return &MissionPausedPayload{} },
	EventMissionCompleted:          func() Payload { return &MissionClosedPayload{} },
	EventMissionCancelled:          func() Payload { return &MissionClosedPayload{} },
	EventStaffingRequested:         func() Payload { return &StaffingRequestedPayload{} },
	EventStaffingDecisionRecorded:  func() Payload { return &StaffingFactPayload{} },
	EventAgentInvited:              func() Payload { return &AgentPayload{} },
	EventAgentJoined:               func() Payload { return &AgentPayload{} },
	EventAgentReleased:             func() Payload { return &AgentPayload{} },
	EventAgentReplaced:             func() Payload { return &StaffingFactPayload{} },
	EventMissionAssignmentCreated:  func() Payload { return &MissionAssignmentPayload{} },
	EventMissionAssignmentReleased: func() Payload { return &StaffingFactPayload{} },
	EventMissionAssignmentChanged:  func() Payload { return &MissionAssignmentPayload{} },
	EventCandidateRejected:         func() Payload { return &StaffingFactPayload{} },
	EventTaskContractCreated:       func() Payload { return &TaskContractCreatedPayload{} },
	EventTaskAssigned:              func() Payload { return &TaskAssignedPayload{} },
	EventTaskAccepted:              func() Payload { return &TaskAcceptedPayload{} },
	EventTaskStarted:               func() Payload { return &TaskStatusPayload{} },
	EventTaskBlocked:               func() Payload { return &TaskStatusPayload{} },
	EventTaskSubmitted:             func() Payload { return &TaskStatusPayload{} },
	EventReworkRequested:           func() Payload { return &ReworkRequestedPayload{} },
	EventReworkAssigned:            func() Payload { return &ReworkAssignedPayload{} },
	EventReworkResolved:            func() Payload { return &ReworkResolvedPayload{} },
	EventTaskApproved:              func() Payload { return &TaskStatusPayload{} },
	EventTaskRejected:              func() Payload { return &TaskStatusPayload{} },
	EventTaskCancelled:             func() Payload { return &TaskStatusPayload{} },
	EventAgentResultSubmitted:      func() Payload { return &AgentResultSubmittedPayload{} },
	EventSummarySubmitted:          func() Payload { return &SummarySubmittedPayload{} },
	EventDeliverableSubmitted:      func() Payload { return &DeliverableSubmittedPayload{} },
	EventEvidenceSubmitted:         func() Payload { return &EvidenceSubmittedPayload{} },
	EventQAPlanCompleted:           func() Payload { return &QAPayload{} },
	EventQARequested:               func() Payload { return &QAPayload{} },
	EventQAApproved:                func() Payload { return &QAPayload{} },
	EventQARejected:                func() Payload { return &QAPayload{} },
	EventQASkipped:                 func() Payload { return &QAPayload{} },
	EventAcceptanceReportCreated:   func() Payload { return &QAPayload{} },
	EventDiscussionGroupCreated:    func() Payload { return &DiscussionGroupCreatedPayload{} },
	EventMessagePosted:             func() Payload { return &MessagePostedPayload{} },
	EventAgentMentioned:            func() Payload { return &InboxItemPayload{} },
	EventAgentReplied:              func() Payload { return &MessagePostedPayload{} },
	EventInboxItemCreated:          func() Payload { return &InboxItemPayload{} },
	EventInboxItemClosed:           func() Payload { return &InboxItemPayload{} },
	EventDecisionRecorded:          func() Payload { return &DecisionRecordedPayload{} },
	EventApprovalDecided:           func() Payload { return &ApprovalDecidedPayload{} },
	EventBlockerRaised:             func() Payload { return &BlockerRaisedPayload{} },
	EventClarificationRequested:    func() Payload { return &InboxItemPayload{} },
	EventMemoryCandidateCreated:    func() Payload { return &MemoryCandidatePayload{} },
	EventMemoryCandidateAccepted:   func() Payload { return &MemoryCandidatePayload{} },
	EventMemoryRecordCreated:       func() Payload { return &MemoryRecordPayload{} },
	EventMemoryRecordUpdated:       func() Payload { return &MemoryRecordPayload{} },
	EventMemoryRecordDisabled:      func() Payload { return &MemoryRecordPayload{} },
	EventMemoryDigestGenerated:     func() Payload { return &MemoryDigestPayload{} },
	EventMemoryAccessDenied:        func() Payload { return &MemoryAccessDeniedPayload{} },
	EventAgentRunQueued:            func() Payload { return &RuntimeLifecyclePayload{} },
	EventAgentRunStarted:           func() Payload { return &RuntimeLifecyclePayload{} },
	EventAgentRunCompleted:         func() Payload { return &RuntimeLifecyclePayload{} },
	EventAgentRunFailed:            func() Payload { return &RuntimeFailurePayload{} },
	EventAgentRunTimedOut:          func() Payload { return &RuntimeFailurePayload{} },
	EventAgentRunCancelled:         func() Payload { return &RuntimeFailurePayload{} },
	EventAgentRunBlocked:           func() Payload { return &RuntimeFailurePayload{} },
}

type metadataEnvelope struct {
	MissionID string            `json:"mission_id"`
	TenantID  string            `json:"tenant_id,omitempty"`
	Actor     ActorRef          `json:"actor_ref"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

func encodePayload(payload Payload) ([]byte, error) {
	return json.Marshal(payload)
}

func decodePayload(eventType EventType, data []byte) (Payload, error) {
	if payload, ok, err := decodeStaffingFactPayload(eventType, data); ok || err != nil {
		return payload, err
	}
	factory, ok := payloadFactories[eventType]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownEventType, eventType)
	}
	payload := factory()
	if err := json.Unmarshal(data, payload); err != nil {
		return nil, fmt.Errorf("decode payload for %s: %w", eventType, err)
	}
	value := reflect.ValueOf(payload)
	if value.Kind() == reflect.Pointer && !value.IsNil() {
		if decoded, ok := value.Elem().Interface().(Payload); ok {
			return decoded, nil
		}
	}
	return payload, nil
}

func decodeStaffingFactPayload(eventType EventType, data []byte) (Payload, bool, error) {
	switch eventType {
	case EventStaffingDecisionRecorded, EventAgentJoined, EventAgentReleased, EventAgentReplaced, EventMissionAssignmentCreated, EventMissionAssignmentReleased, EventCandidateRejected:
	default:
		return nil, false, nil
	}
	var fact StaffingFactPayload
	if err := json.Unmarshal(data, &fact); err != nil {
		return nil, true, fmt.Errorf("decode payload for %s: %w", eventType, err)
	}
	if fact.TeamID != "" ||
		fact.RequestID != "" ||
		fact.DecisionID != "" ||
		fact.MemberID != "" ||
		fact.AgentProfileID != "" ||
		fact.AgentInstanceID != "" ||
		fact.AuditRef != "" ||
		fact.ReasonCode != "" ||
		fact.Reason != "" {
		return fact, true, nil
	}
	return nil, false, nil
}

func encodeMetadata(event Envelope) ([]byte, error) {
	return json.Marshal(metadataEnvelope{
		MissionID: event.MissionID.String(),
		TenantID:  event.Tenant(),
		Actor:     event.Actor,
		Metadata:  event.Metadata,
	})
}

func decodeMetadata(data []byte) (metadataEnvelope, error) {
	if len(data) == 0 {
		return metadataEnvelope{}, nil
	}
	var decoded metadataEnvelope
	if err := json.Unmarshal(data, &decoded); err != nil {
		return metadataEnvelope{}, fmt.Errorf("decode metadata: %w", err)
	}
	return decoded, nil
}
