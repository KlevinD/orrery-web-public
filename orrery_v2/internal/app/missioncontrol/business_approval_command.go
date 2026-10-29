package missioncontrol

import (
	"context"
	"strings"
	"time"

	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/mission/projection"
)

func (s *CommandService) DecideBusinessApproval(ctx context.Context, viewer ViewerRef, cmd BusinessApprovalDecisionCommand) (CommandReceiptDTO, error) {
	if err := viewer.Validate(); err != nil {
		return CommandReceiptDTO{}, err
	}
	if cmd.IdempotencyKey == "" || cmd.ApprovalType == "" || cmd.ApprovalRef == "" || cmd.Decision == "" {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	if err := s.authorizeMissionAction(ctx, viewer, cmd.MissionID, "mission.qa_approve"); err != nil {
		return CommandReceiptDTO{}, err
	}
	now := time.Now().UTC()
	if s != nil && s.clock != nil {
		now = s.clock.Now().UTC()
	}
	if err := s.recordApprovalDecisionEvent(ctx, viewer, cmd, now); err != nil {
		return CommandReceiptDTO{}, err
	}
	if shouldResumeApprovalDecision(cmd.Decision) {
		if _, err := s.ResumeRunAfterApproval(ctx, viewer, ResumeApprovalRunCommand{
			RequestID:      firstString(cmd.RequestID, cmd.IdempotencyKey),
			IdempotencyKey: cmd.IdempotencyKey + "|resume",
			MissionID:      cmd.MissionID,
			ApprovalRef:    cmd.ApprovalRef,
			ApprovalType:   cmd.ApprovalType,
			Decision:       cmd.Decision,
			ActionID:       cmd.ActionID,
			Message:        cmd.Message,
			ModifiedInput:  cmd.ModifiedInput,
			ChoiceID:       cmd.ChoiceID,
			Remember:       cmd.Remember,
			Metadata:       cloneStringMap(cmd.Metadata),
		}); err != nil {
			return CommandReceiptDTO{}, err
		}
	}
	receipt := CommandReceiptDTO{CommandID: cmd.ApprovalRef, Status: "accepted", MissionID: cmd.MissionID.String(), AcceptedAt: now}
	summary := strings.TrimSpace("decided business approval " + cmd.Message)
	if err := s.recordViewerAudit(ctx, viewer, AuditEvent{ActorID: viewer.ViewerID, ViewerID: viewer.ViewerID, MissionID: cmd.MissionID.String(), EntityType: "approval", EntityID: cmd.ApprovalRef, Action: "approval." + cmd.ApprovalType + "." + cmd.Decision, Result: "succeeded", Severity: "info", CorrelationID: firstString(cmd.RequestID, cmd.IdempotencyKey), Summary: summary, Metadata: cloneStringMap(cmd.Metadata), OccurredAt: now}); err != nil {
		return CommandReceiptDTO{}, err
	}
	return receipt, nil
}

func (s *CommandService) recordApprovalDecisionEvent(ctx context.Context, viewer ViewerRef, cmd BusinessApprovalDecisionCommand, now time.Time) error {
	if s == nil || s.repo == nil || s.events == nil || s.ids == nil || cmd.MissionID.IsZero() {
		return nil
	}
	model, err := loadMissionProjection(ctx, s.repo, viewer.Tenant(), cmd.MissionID)
	if err != nil {
		return err
	}
	eventID, err := s.ids.NewIDContext()
	if err != nil {
		return err
	}
	streamVersion, err := s.nextRuntimeMissionEventVersion(ctx, viewer.Tenant(), cmd.MissionID, model)
	if err != nil {
		return err
	}
	event := missionevent.Envelope{
		EventID:        eventID,
		TenantID:       viewer.Tenant(),
		MissionID:      cmd.MissionID,
		StreamID:       missionevent.MissionStreamID(viewer.Tenant(), cmd.MissionID),
		StreamVersion:  streamVersion,
		EventType:      missionevent.EventApprovalDecided,
		EventVersion:   1,
		Actor:          actorFromViewer(viewer),
		CorrelationID:  firstString(cmd.RequestID, cmd.IdempotencyKey),
		IdempotencyKey: cmd.IdempotencyKey,
		OccurredAt:     now,
		Payload: missionevent.ApprovalDecidedPayload{
			ApprovalRef:  cmd.ApprovalRef,
			ApprovalType: cmd.ApprovalType,
			Decision:     cmd.Decision,
			ActionID:     cmd.ActionID,
			Message:      cmd.Message,
			Metadata:     cloneStringMap(cmd.Metadata),
		},
		Metadata: map[string]string{"command": "mission.approval.decide"},
	}
	if err := s.events.Append(ctx, event); err != nil {
		return err
	}
	model.ApprovalRequests = decideApprovalRequestProjection(model.ApprovalRequests, event.Payload.(missionevent.ApprovalDecidedPayload), now)
	model.Checkpoint.Version++
	model.Checkpoint.UpdatedAt = now
	model.UpdatedAt = now
	return saveMissionProjection(ctx, s.repo, viewer.Tenant(), model)
}

func decideApprovalRequestProjection(requests []projection.ApprovalRequestView, decision missionevent.ApprovalDecidedPayload, decidedAt time.Time) []projection.ApprovalRequestView {
	for i := range requests {
		if requests[i].RequestID != decision.ApprovalRef || requests[i].ApprovalType != decision.ApprovalType {
			continue
		}
		status := strings.TrimSpace(decision.Decision)
		if status == "" {
			status = "closed"
		}
		requests[i].Status = status
		requests[i].DecidedAt = decidedAt
		if len(decision.Metadata) > 0 {
			requests[i].Metadata = mergeApprovalMetadataProjection(requests[i].Metadata, decision.Metadata)
		}
		return requests
	}
	return requests
}

func mergeApprovalMetadataProjection(existing, next map[string]string) map[string]string {
	merged := cloneStringMap(existing)
	if merged == nil {
		merged = map[string]string{}
	}
	for key, value := range cloneStringMap(next) {
		merged[key] = value
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

func shouldResumeApprovalDecision(decision string) bool {
	switch strings.TrimSpace(decision) {
	case "approved", "answered":
		return true
	default:
		return false
	}
}
