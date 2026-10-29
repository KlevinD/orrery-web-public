package event

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/uow"
)

type TaskEventSink struct{}

func NewTaskEventSink() *TaskEventSink {
	return &TaskEventSink{}
}

func (s *TaskEventSink) AppendTaskEvent(ctx context.Context, tx uow.Tx, event missiontask.TaskEvent) error {
	envelope, err := taskEventEnvelope(event)
	if err != nil {
		return err
	}
	payload, err := encodePayload(envelope.Payload)
	if err != nil {
		return err
	}
	metadata, err := encodeMetadata(envelope)
	if err != nil {
		return err
	}
	streamVersion, err := nextTaskEventStreamVersion(ctx, tx, envelope.StreamIDOrDefault())
	if err != nil {
		return err
	}
	_, err = tx.RecordEvent(ctx, uow.EventMessage{
		StreamID:       envelope.StreamIDOrDefault(),
		StreamVersion:  streamVersion,
		EventType:      string(envelope.EventType),
		EventVersion:   envelope.EventVersion,
		Payload:        payload,
		Metadata:       metadata,
		IdempotencyKey: envelope.IdempotencyKey,
		CorrelationID:  envelope.CorrelationID,
		CausationID:    envelope.CausationID,
	})
	return err
}

func nextTaskEventStreamVersion(ctx context.Context, tx uow.Tx, streamID string) (int, error) {
	events, err := tx.EventStore().Load(ctx, streamID)
	if err != nil {
		return 0, err
	}
	return len(events) + 1, nil
}

func taskEventEnvelope(event missiontask.TaskEvent) (Envelope, error) {
	if strings.TrimSpace(event.MissionID) == "" {
		return Envelope{}, fmt.Errorf("%w: task event mission_id is required", ErrInvalidEnvelope)
	}
	actorProfileID, err := requireTaskEventActorProfileID(event)
	if err != nil {
		return Envelope{}, err
	}
	payload, eventType, err := taskEventPayload(event)
	if err != nil {
		return Envelope{}, err
	}
	occurredAt := event.OccurredAt
	if occurredAt.IsZero() {
		return Envelope{}, fmt.Errorf("%w: task event occurred_at is required", ErrInvalidEnvelope)
	}
	envelope := Envelope{
		MissionID:      missioncore.MissionID(event.MissionID),
		StreamID:       event.MissionID,
		StreamVersion:  1,
		EventType:      eventType,
		EventVersion:   1,
		Actor:          ActorRef{Kind: ActorKindAgent, ID: actorProfileID.String()},
		IdempotencyKey: taskEventIdempotencyKey(event),
		OccurredAt:     occurredAt,
		Payload:        payload,
		Metadata: map[string]string{
			"task_id": string(event.TaskID),
			"source":  "mission_task",
		},
	}
	return envelope, nil
}

func requireTaskEventActorProfileID(event missiontask.TaskEvent) (agentcore.AgentProfileID, error) {
	if event.ActorProfileID.IsZero() {
		return "", fmt.Errorf("%w: task event actor_profile_id is required", ErrInvalidEnvelope)
	}
	return event.ActorProfileID, nil
}

func taskEventPayload(event missiontask.TaskEvent) (Payload, EventType, error) {
	switch event.Type {
	case missiontask.TaskEventTaskAccepted:
		return TaskAcceptedPayload{TaskID: event.TaskID, AcceptedBy: agentcore.AgentID(event.ActorProfileID)}, EventTaskAccepted, nil
	case missiontask.TaskEventTaskStarted:
		return TaskStatusPayload{TaskID: event.TaskID, Reason: payloadReason(event.Payload)}, EventTaskStarted, nil
	case missiontask.TaskEventTaskBlocked:
		return TaskStatusPayload{TaskID: event.TaskID, Reason: payloadReason(event.Payload)}, EventTaskBlocked, nil
	case missiontask.TaskEventTaskSubmitted:
		return TaskStatusPayload{TaskID: event.TaskID, Reason: payloadReason(event.Payload)}, EventTaskSubmitted, nil
	case missiontask.TaskEventTaskCancelled:
		return TaskStatusPayload{TaskID: event.TaskID, Reason: payloadReason(event.Payload)}, EventTaskCancelled, nil
	case missiontask.TaskEventAgentResultSubmitted:
		submission, ok := event.Payload.(missiontask.TaskSubmission)
		if !ok {
			return nil, "", unstableTaskPayload(event.Type, event.Payload)
		}
		return AgentResultSubmittedPayload{
			TaskID:    event.TaskID,
			AgentID:   submission.AgentID,
			ResultRef: string(submission.AgentResultRef),
			Summary:   submission.Summary.Text,
		}, EventAgentResultSubmitted, nil
	case missiontask.TaskEventDeliverableSubmitted:
		submission, ok := event.Payload.(missiontask.DeliverableSubmission)
		if !ok {
			return nil, "", unstableTaskPayload(event.Type, event.Payload)
		}
		return DeliverableSubmittedPayload{
			TaskID:                event.TaskID,
			AgentID:               submission.AgentID,
			DeliverableID:         submission.Deliverable.ID,
			ExpectedDeliverableID: submission.Deliverable.ExpectedDeliverableID,
			Title:                 submission.Deliverable.Title,
			ArtifactRef:           submission.Deliverable.ArtifactRef,
		}, EventDeliverableSubmitted, nil
	case missiontask.TaskEventEvidenceSubmitted:
		submission, ok := event.Payload.(missiontask.EvidenceSubmission)
		if !ok {
			return nil, "", unstableTaskPayload(event.Type, event.Payload)
		}
		return EvidenceSubmittedPayload{
			TaskID:                event.TaskID,
			AgentID:               submission.AgentID,
			EvidenceID:            submission.Evidence.ID,
			EvidenceRequirementID: submission.Evidence.EvidenceRequirementID,
			AcceptanceCriterionID: submission.Evidence.AcceptanceCriterionID,
			Kind:                  submission.Evidence.Kind,
			ArtifactRef:           submission.Evidence.ArtifactRef,
			ToolLogRef:            submission.Evidence.ToolLogRef,
			ExternalURL:           submission.Evidence.ExternalURL,
		}, EventEvidenceSubmitted, nil
	case missiontask.TaskEventQARequested:
		qaRef, message := taskQARequestIdentity(event.TaskID, event.Payload)
		return QAPayload{TaskID: event.TaskID, QARef: qaRef, Status: "requested", Reason: payloadReason(event.Payload), Message: message}, EventQARequested, nil
	case missiontask.TaskEventQAPlanCompleted:
		plan, ok := event.Payload.(missiontask.QAPlanRecord)
		if !ok {
			return nil, "", unstableTaskPayload(event.Type, event.Payload)
		}
		return QAPayload{TaskID: event.TaskID, QARef: string(plan.ID), Status: "plan_completed", Reason: plan.Summary, ArtifactRef: plan.ArtifactRef}, EventQAPlanCompleted, nil
	case missiontask.TaskEventQAApproved:
		result, ok := event.Payload.(missiontask.QAResultRecord)
		if !ok {
			return nil, "", unstableTaskPayload(event.Type, event.Payload)
		}
		return QAPayload{TaskID: event.TaskID, QARef: missiontask.QARequestRef(event.TaskID), Status: string(result.Status), Reason: strings.Join(result.RejectionReasons, "; ")}, EventQAApproved, nil
	case missiontask.TaskEventQARejected:
		result, ok := event.Payload.(missiontask.QAResultRecord)
		if !ok {
			return nil, "", unstableTaskPayload(event.Type, event.Payload)
		}
		return QAPayload{TaskID: event.TaskID, QARef: missiontask.QARequestRef(event.TaskID), Status: string(result.Status), Reason: strings.Join(result.RejectionReasons, "; ")}, EventQARejected, nil
	case missiontask.TaskEventQASkipped:
		skip, ok := event.Payload.(missiontask.QASkipRecord)
		if !ok {
			return nil, "", unstableTaskPayload(event.Type, event.Payload)
		}
		return QAPayload{TaskID: event.TaskID, QARef: string(skip.ID), Status: string(missiontask.QAStatusApproved), Reason: skip.Reason}, EventQASkipped, nil
	case missiontask.TaskEventReworkRequested:
		request, ok := event.Payload.(missiontask.ReworkRequest)
		if !ok {
			return nil, "", unstableTaskPayload(event.Type, event.Payload)
		}
		return ReworkRequestedPayload{TaskID: event.TaskID, QARef: string(request.ID), Reason: request.Reason}, EventReworkRequested, nil
	case missiontask.TaskEventReworkAssigned:
		request, ok := event.Payload.(missiontask.ReworkRequest)
		if !ok {
			return nil, "", unstableTaskPayload(event.Type, event.Payload)
		}
		return ReworkAssignedPayload{TaskID: event.TaskID, ReworkRef: string(request.ID), Reason: request.Reason}, EventReworkAssigned, nil
	case missiontask.TaskEventReworkResolved:
		request, ok := event.Payload.(missiontask.ReworkRequest)
		if !ok {
			return nil, "", unstableTaskPayload(event.Type, event.Payload)
		}
		return ReworkResolvedPayload{TaskID: event.TaskID, ReworkRef: string(request.ID), Reason: request.Reason}, EventReworkResolved, nil
	case missiontask.TaskEventAcceptanceReportCreated:
		report, ok := event.Payload.(missiontask.AcceptanceReport)
		if !ok {
			return nil, "", unstableTaskPayload(event.Type, event.Payload)
		}
		return QAPayload{TaskID: event.TaskID, QARef: string(report.QAResultID), Status: "acceptance_report_created", Reason: report.Summary}, EventAcceptanceReportCreated, nil
	default:
		return nil, "", fmt.Errorf("%w: unknown task event type %s", ErrUnknownEventType, event.Type)
	}
}

func taskEventIdempotencyKey(event missiontask.TaskEvent) string {
	return strings.Join([]string{"task", event.MissionID, string(event.TaskID), string(event.Type), payloadStableID(event.Payload)}, "|")
}

func payloadStableID(payload any) string {
	switch item := payload.(type) {
	case missiontask.TaskSubmission:
		return string(item.ID)
	case missiontask.DeliverableSubmission:
		return string(item.ID)
	case missiontask.EvidenceSubmission:
		return string(item.ID)
	case missiontask.QAResultRecord:
		return string(item.ID)
	case missiontask.QAPlanRecord:
		return string(item.ID)
	case missiontask.QASkipRecord:
		return string(item.ID)
	case missiontask.ReworkRequest:
		return string(item.ID)
	case missiontask.AcceptanceReport:
		return string(item.ID)
	default:
		raw, _ := json.Marshal(item)
		return string(raw)
	}
}

func payloadReason(payload any) string {
	if payload == nil {
		return ""
	}
	if values, ok := payload.(map[string]any); ok {
		if reason, ok := values["reason"].(string); ok {
			return reason
		}
		if status, ok := values["status"].(string); ok {
			return status
		}
	}
	return ""
}

func taskQARequestIdentity(taskID missiontask.TaskID, payload any) (string, string) {
	qaRef := missiontask.QARequestRef(taskID)
	message := "QA review requested"
	values, ok := payload.(map[string]any)
	if !ok {
		return qaRef, message
	}
	if value, ok := values["qa_ref"].(string); ok && strings.TrimSpace(value) != "" {
		qaRef = strings.TrimSpace(value)
	}
	if value, ok := values["message"].(string); ok && strings.TrimSpace(value) != "" {
		message = strings.TrimSpace(value)
	}
	return qaRef, message
}

func unstableTaskPayload(eventType missiontask.TaskEventType, payload any) error {
	return fmt.Errorf("%w: task event %s payload has unstable type %T", ErrInvalidEnvelope, eventType, payload)
}
