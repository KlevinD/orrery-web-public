package missioncontrol

import (
	"context"
	"strings"

	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/mission/projection"
)

type CancelMissionCommand struct {
	RequestID      string
	IdempotencyKey string
	MissionID      missioncore.MissionID
	Reason         string
}

// CancelMission terminally closes a Mission while retaining its event and
// projection history. It also drains any non-terminal runtime sessions.
func (s *CommandService) CancelMission(ctx context.Context, viewer ViewerRef, cmd CancelMissionCommand) (CommandReceiptDTO, error) {
	if s == nil || s.repo == nil || s.runStore == nil || s.events == nil || s.ids == nil {
		return CommandReceiptDTO{}, ErrServiceDependency
	}
	if err := viewer.Validate(); err != nil {
		return CommandReceiptDTO{}, err
	}
	cmd.RequestID = strings.TrimSpace(cmd.RequestID)
	cmd.IdempotencyKey = strings.TrimSpace(cmd.IdempotencyKey)
	cmd.Reason = strings.TrimSpace(cmd.Reason)
	if cmd.IdempotencyKey == "" || cmd.MissionID.IsZero() || cmd.Reason == "" {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	if err := s.authorizeMissionAction(ctx, viewer, cmd.MissionID, "mission.edit"); err != nil {
		return CommandReceiptDTO{}, err
	}

	model, err := loadMissionProjection(ctx, s.repo, viewer.Tenant(), cmd.MissionID)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	if model.Mission.Status == projection.MissionStatusCompleted {
		return CommandReceiptDTO{}, ErrConflict
	}
	if model.Mission.Status == projection.MissionStatusCancelled {
		if err := s.validateMissionCancellationReplay(ctx, viewer.Tenant(), cmd); err != nil {
			return CommandReceiptDTO{}, err
		}
	}
	transitioned := model.Mission.Status != projection.MissionStatusCancelled
	if transitioned {
		model, err = s.appendMissionCancelled(ctx, viewer, cmd, model)
		if err != nil {
			return CommandReceiptDTO{}, err
		}
	}

	for sessionIndex, session := range model.RuntimeSessions {
		if !runtimeSessionIsActive(model.RuntimeSessions, sessionIndex) {
			continue
		}
		if _, err := s.CancelAgentRun(ctx, viewer, CancelAgentRunCommand{
			RequestID:      firstString(cmd.RequestID, cmd.IdempotencyKey),
			IdempotencyKey: cmd.IdempotencyKey + ":run:" + session.RunID.String(),
			MissionID:      cmd.MissionID,
			RunID:          session.RunID,
			Reason:         cmd.Reason,
		}); err != nil {
			return CommandReceiptDTO{}, err
		}
	}

	model, err = loadMissionProjection(ctx, s.repo, viewer.Tenant(), cmd.MissionID)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	if model.Mission.Status != projection.MissionStatusCancelled {
		return CommandReceiptDTO{}, ErrServiceDependency
	}
	if transitioned {
		if err := s.recordViewerAudit(ctx, viewer, AuditEvent{
			ActorID: viewer.ViewerID, ViewerID: viewer.ViewerID, MissionID: cmd.MissionID.String(),
			EntityType: "mission", EntityID: cmd.MissionID.String(), Action: "mission.cancel",
			Result: "succeeded", Severity: "warning", CorrelationID: firstString(cmd.RequestID, cmd.IdempotencyKey),
			Summary: "cancelled Mission", Metadata: map[string]string{"reason": cmd.Reason}, OccurredAt: model.UpdatedAt,
		}); err != nil {
			return CommandReceiptDTO{}, err
		}
	}
	return CommandReceiptDTO{
		CommandID: cmd.IdempotencyKey, Status: "accepted", MissionID: cmd.MissionID.String(),
		Version: model.Checkpoint.Version, AcceptedAt: model.UpdatedAt,
	}, nil
}

func (s *CommandService) validateMissionCancellationReplay(ctx context.Context, tenantID string, cmd CancelMissionCommand) error {
	if s == nil || s.eventReader == nil {
		return nil
	}
	events, err := loadMissionEvents(ctx, s.eventReader, tenantID, cmd.MissionID)
	if err != nil {
		return err
	}
	for _, event := range events {
		if event.IdempotencyKey != cmd.IdempotencyKey {
			continue
		}
		reason, ok := missionCancellationReason(event.Payload)
		if event.EventType != missionevent.EventMissionCancelled || !ok || reason != cmd.Reason {
			return ErrConflict
		}
		return nil
	}
	return nil
}

func (s *CommandService) appendMissionCancelled(ctx context.Context, viewer ViewerRef, cmd CancelMissionCommand, model projection.ReadModel) (projection.ReadModel, error) {
	eventID, err := s.ids.NewIDContext()
	if err != nil {
		return projection.ReadModel{}, err
	}
	streamVersion, err := s.nextRuntimeMissionEventVersion(ctx, viewer.Tenant(), cmd.MissionID, model)
	if err != nil {
		return projection.ReadModel{}, err
	}
	payload := missionevent.MissionClosedPayload{Reason: cmd.Reason}
	event := missionevent.Envelope{
		EventID: eventID, TenantID: viewer.Tenant(), MissionID: cmd.MissionID,
		StreamID: missionevent.MissionStreamID(viewer.Tenant(), cmd.MissionID), StreamVersion: streamVersion,
		EventType: missionevent.EventMissionCancelled, EventVersion: payload.EventPayloadVersion(), Actor: actorFromViewer(viewer),
		CorrelationID: firstString(cmd.RequestID, cmd.IdempotencyKey), IdempotencyKey: cmd.IdempotencyKey,
		OccurredAt: s.nowUTC(), Payload: payload, Metadata: map[string]string{"command": "mission.cancel"},
	}
	if err := s.events.Append(ctx, event); err != nil {
		return projection.ReadModel{}, err
	}

	projector := s.projector
	if projector == nil {
		projector = projection.NewProjector()
	}
	if s.eventReader == nil {
		if err := projector.Apply(ctx, &model, event); err != nil {
			return projection.ReadModel{}, err
		}
	} else {
		events, err := loadMissionEvents(ctx, s.eventReader, viewer.Tenant(), cmd.MissionID)
		if err != nil {
			return projection.ReadModel{}, err
		}
		persisted, err := projectPersistedMissionEvents(ctx, projector, &model, events, func(item missionevent.Envelope) (bool, error) {
			if item.EventID != event.EventID && item.IdempotencyKey != event.IdempotencyKey {
				return false, nil
			}
			reason, ok := missionCancellationReason(item.Payload)
			if item.EventType != missionevent.EventMissionCancelled || !ok || reason != cmd.Reason {
				return false, ErrConflict
			}
			return true, nil
		})
		if err != nil {
			return projection.ReadModel{}, err
		}
		if !persisted {
			return projection.ReadModel{}, ErrServiceDependency
		}
	}
	if model.Mission.Status != projection.MissionStatusCancelled {
		return projection.ReadModel{}, ErrServiceDependency
	}
	if err := saveMissionProjection(ctx, s.repo, viewer.Tenant(), model); err != nil {
		return projection.ReadModel{}, err
	}
	return model, nil
}

func missionCancellationReason(payload missionevent.Payload) (string, bool) {
	switch value := payload.(type) {
	case missionevent.MissionClosedPayload:
		return strings.TrimSpace(value.Reason), true
	case *missionevent.MissionClosedPayload:
		if value != nil {
			return strings.TrimSpace(value.Reason), true
		}
	}
	return "", false
}
