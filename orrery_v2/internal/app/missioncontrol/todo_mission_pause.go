package missioncontrol

import (
	"context"
	"strings"

	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/mission/projection"
)

type PauseTodoMissionCommand struct {
	IdempotencyKey string
	MissionID      missioncore.MissionID
	TodoID         string
	Reason         string
}

// PauseTodoMission is the internal, authorized boundary used when a Todo
// version invalidates the scope of its active Mission.
func (s *CommandService) PauseTodoMission(ctx context.Context, viewer ViewerRef, cmd PauseTodoMissionCommand) error {
	if s == nil || s.repo == nil || s.runStore == nil || s.events == nil || s.ids == nil {
		return ErrServiceDependency
	}
	if err := viewer.Validate(); err != nil {
		return err
	}
	cmd.IdempotencyKey = strings.TrimSpace(cmd.IdempotencyKey)
	cmd.TodoID = strings.TrimSpace(cmd.TodoID)
	cmd.Reason = strings.TrimSpace(cmd.Reason)
	if cmd.IdempotencyKey == "" || cmd.MissionID.IsZero() || cmd.TodoID == "" || cmd.Reason == "" {
		return ErrInvalidRequest
	}
	if err := s.authorizeMissionAction(ctx, viewer, cmd.MissionID, "mission.edit"); err != nil {
		return err
	}
	model, err := loadMissionProjection(ctx, s.repo, viewer.Tenant(), cmd.MissionID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(model.Mission.OriginKind) != "todo" || strings.TrimSpace(model.Mission.OriginTodoID) != cmd.TodoID {
		return ErrProjectionNotFound
	}
	transitioned := false
	if model.Mission.Status == projection.MissionStatusActive {
		model, err = s.appendTodoMissionPaused(ctx, viewer, cmd, model)
		if err != nil {
			return err
		}
		transitioned = true
	} else if model.Mission.Status != projection.MissionStatusPaused && model.Mission.Status != projection.MissionStatusCompleted && model.Mission.Status != projection.MissionStatusCancelled {
		return ErrInvalidRequest
	}

	for sessionIndex, session := range model.RuntimeSessions {
		if !runtimeSessionIsActive(model.RuntimeSessions, sessionIndex) {
			continue
		}
		if _, err := s.CancelAgentRun(ctx, viewer, CancelAgentRunCommand{
			RequestID: cmd.IdempotencyKey, IdempotencyKey: cmd.IdempotencyKey + ":run:" + session.RunID.String(),
			MissionID: cmd.MissionID, RunID: session.RunID, Reason: cmd.Reason,
		}); err != nil {
			return err
		}
	}
	if !transitioned {
		return nil
	}
	return s.recordViewerAudit(ctx, viewer, AuditEvent{
		ActorID: viewer.ViewerID, ViewerID: viewer.ViewerID, MissionID: cmd.MissionID.String(),
		EntityType: "mission", EntityID: cmd.MissionID.String(), Action: "mission.pause_todo_scope_change",
		Result: "succeeded", Severity: "warning", CorrelationID: cmd.IdempotencyKey,
		Summary: "paused Mission after Todo scope changed", Metadata: map[string]string{"todo_id": cmd.TodoID, "reason": cmd.Reason},
		OccurredAt: s.nowUTC(),
	})
}

func (s *CommandService) appendTodoMissionPaused(ctx context.Context, viewer ViewerRef, cmd PauseTodoMissionCommand, model projection.ReadModel) (projection.ReadModel, error) {
	eventID, err := s.ids.NewIDContext()
	if err != nil {
		return projection.ReadModel{}, err
	}
	streamVersion, err := s.nextRuntimeMissionEventVersion(ctx, viewer.Tenant(), cmd.MissionID, model)
	if err != nil {
		return projection.ReadModel{}, err
	}
	payload := missionevent.MissionPausedPayload{TodoID: cmd.TodoID, Reason: cmd.Reason}
	event := missionevent.Envelope{
		EventID: eventID, TenantID: viewer.Tenant(), MissionID: cmd.MissionID,
		StreamID: missionevent.MissionStreamID(viewer.Tenant(), cmd.MissionID), StreamVersion: streamVersion,
		EventType: missionevent.EventMissionPaused, EventVersion: payload.EventPayloadVersion(), Actor: actorFromViewer(viewer),
		CorrelationID: cmd.IdempotencyKey, IdempotencyKey: cmd.IdempotencyKey, OccurredAt: s.nowUTC(), Payload: payload,
		Metadata: map[string]string{"command": "mission.pause_todo_scope_change", "todo_id": cmd.TodoID},
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
			todoID, reason, ok := missionPauseDetails(item.Payload)
			if item.EventType != missionevent.EventMissionPaused || !ok || todoID != cmd.TodoID || reason != cmd.Reason {
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
	if model.Mission.Status != projection.MissionStatusPaused {
		return projection.ReadModel{}, ErrServiceDependency
	}
	if err := saveMissionProjection(ctx, s.repo, viewer.Tenant(), model); err != nil {
		return projection.ReadModel{}, err
	}
	return model, nil
}

func missionPauseDetails(payload missionevent.Payload) (string, string, bool) {
	switch value := payload.(type) {
	case missionevent.MissionPausedPayload:
		return strings.TrimSpace(value.TodoID), strings.TrimSpace(value.Reason), true
	case *missionevent.MissionPausedPayload:
		if value != nil {
			return strings.TrimSpace(value.TodoID), strings.TrimSpace(value.Reason), true
		}
	}
	return "", "", false
}
