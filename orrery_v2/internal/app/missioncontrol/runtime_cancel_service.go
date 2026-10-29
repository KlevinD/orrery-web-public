package missioncontrol

import (
	"context"
	"errors"
	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/mission/projection"
	"strings"
)

func (s *CommandService) CancelAgentRun(ctx context.Context, viewer ViewerRef, cmd CancelAgentRunCommand) (CommandReceiptDTO, error) {
	if s == nil || s.runStore == nil || s.repo == nil || s.events == nil || s.ids == nil {
		return CommandReceiptDTO{}, ErrServiceDependency
	}
	if err := viewer.Validate(); err != nil {
		return CommandReceiptDTO{}, err
	}
	if cmd.IdempotencyKey == "" || cmd.MissionID.IsZero() || cmd.RunID == "" {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	if err := s.authorizeMissionAction(ctx, viewer, cmd.MissionID, "mission.edit"); err != nil {
		return CommandReceiptDTO{}, err
	}
	model, err := loadMissionProjection(ctx, s.repo, viewer.Tenant(), cmd.MissionID)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	session, ok := findRuntimeSession(model, cmd.RunID)
	if ok && isTerminalRuntimeSession(session.Status) {
		return CommandReceiptDTO{}, ErrAgentRunTerminal
	}
	taskID := cmd.TaskID
	agentID := cmd.AgentID
	if ok {
		taskID = session.TaskID
		agentID = session.AgentID
	}
	reason := strings.TrimSpace(cmd.Reason)
	if reason == "" {
		reason = "agent run cancelled"
	}
	if conditional, supportsConditional := s.runStore.(RuntimeConditionalRunStorePort); supportsConditional {
		result, err := conditional.CancelWithResult(ctx, cmd.RunID, reason)
		if err != nil {
			return CommandReceiptDTO{}, err
		}
		switch result.Disposition {
		case RuntimeRunCancelApplied:
		case RuntimeRunCancelAlreadyCancelled, RuntimeRunCancelAlreadyTerminal:
			return CommandReceiptDTO{}, ErrAgentRunTerminal
		case RuntimeRunCancelMissing:
			return CommandReceiptDTO{}, ErrAgentRunMissing
		default:
			return CommandReceiptDTO{}, ErrServiceDependency
		}
	} else {
		if !ok {
			return CommandReceiptDTO{}, ErrInvalidRequest
		}
		if err := s.runStore.Cancel(ctx, cmd.RunID, reason); err != nil {
			return CommandReceiptDTO{}, err
		}
	}
	if taskID.IsZero() || agentID.IsZero() {
		terminal, terminalErr := s.hasTerminalRuntimeEvent(ctx, viewer.Tenant(), cmd.MissionID, cmd.RunID)
		if terminalErr != nil {
			return CommandReceiptDTO{}, terminalErr
		}
		if terminal {
			return CommandReceiptDTO{}, ErrAgentRunTerminal
		}
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	streamVersion, terminal, err := s.nextCancelableRuntimeEventVersion(ctx, viewer.Tenant(), cmd.MissionID, cmd.RunID, model)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	if terminal {
		return CommandReceiptDTO{}, ErrAgentRunTerminal
	}
	event, err := s.runtimeFailureCommandEvent(ctx, viewer, runtimeFailureCommandEventInput{
		MissionID:      cmd.MissionID,
		TaskID:         taskID,
		AgentID:        agentID,
		RunID:          cmd.RunID,
		RequestID:      cmd.RequestID,
		IdempotencyKey: cmd.IdempotencyKey,
		EventType:      missionevent.EventAgentRunCancelled,
		Code:           "agent_runtime_cancelled",
		Message:        reason,
		CommandName:    "mission.ops.cancel_agent_run",
		StreamVersion:  streamVersion,
	}, model)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	if err := s.events.Append(ctx, event); err != nil {
		terminal, readErr := s.hasTerminalRuntimeEvent(ctx, viewer.Tenant(), cmd.MissionID, cmd.RunID)
		if readErr != nil {
			return CommandReceiptDTO{}, errors.Join(err, readErr)
		}
		if terminal {
			return CommandReceiptDTO{}, ErrAgentRunTerminal
		}
		return CommandReceiptDTO{}, err
	}
	projectRuntimeEvent(&model, event, s.nowUTC())
	if err := saveMissionProjection(ctx, s.repo, viewer.Tenant(), model); err != nil {
		return CommandReceiptDTO{}, err
	}
	receipt := CommandReceiptDTO{CommandID: cmd.RunID.String(), Status: "accepted", MissionID: cmd.MissionID.String(), TaskID: taskID.String(), Version: model.Checkpoint.Version, AcceptedAt: event.OccurredAt}
	if err := s.recordViewerAudit(ctx, viewer, AuditEvent{
		ActorID:       viewer.ViewerID,
		ViewerID:      viewer.ViewerID,
		MissionID:     cmd.MissionID.String(),
		EntityType:    "agent_run",
		EntityID:      cmd.RunID.String(),
		Action:        "agent_run.cancel",
		Result:        "succeeded",
		Severity:      "info",
		CorrelationID: firstString(cmd.RequestID, cmd.IdempotencyKey),
		Summary:       "cancelled agent run",
		Metadata:      map[string]string{"reason": reason},
		OccurredAt:    event.OccurredAt,
	}); err != nil {
		return CommandReceiptDTO{}, err
	}
	return receipt, nil
}

func (s *CommandService) nextCancelableRuntimeEventVersion(ctx context.Context, tenantID string, missionID missioncore.MissionID, runID agentcore.AgentRunID, model projection.ReadModel) (int, bool, error) {
	if s == nil || s.eventReader == nil {
		return model.Checkpoint.Version + 1, false, nil
	}
	events, err := loadMissionEvents(ctx, s.eventReader, tenantID, missionID)
	if err != nil {
		return 0, false, err
	}
	for _, event := range events {
		switch event.EventType {
		case missionevent.EventAgentRunCompleted, missionevent.EventAgentRunFailed, missionevent.EventAgentRunTimedOut,
			missionevent.EventAgentRunCancelled, missionevent.EventAgentRunBlocked:
		default:
			continue
		}
		if runtimeEventRunID(event.Payload) == runID {
			return 0, true, nil
		}
	}
	return len(events) + 1, false, nil
}

func (s *CommandService) hasTerminalRuntimeEvent(ctx context.Context, tenantID string, missionID missioncore.MissionID, runID agentcore.AgentRunID) (bool, error) {
	if s == nil || s.eventReader == nil {
		return false, nil
	}
	events, err := loadMissionEvents(ctx, s.eventReader, tenantID, missionID)
	if err != nil {
		return false, err
	}
	for _, event := range events {
		switch event.EventType {
		case missionevent.EventAgentRunCompleted, missionevent.EventAgentRunFailed, missionevent.EventAgentRunTimedOut,
			missionevent.EventAgentRunCancelled, missionevent.EventAgentRunBlocked:
		default:
			continue
		}
		if runtimeEventRunID(event.Payload) == runID {
			return true, nil
		}
	}
	return false, nil
}

func runtimeEventRunID(payload missionevent.Payload) agentcore.AgentRunID {
	switch value := payload.(type) {
	case missionevent.RuntimeLifecyclePayload:
		return value.RunID
	case *missionevent.RuntimeLifecyclePayload:
		if value != nil {
			return value.RunID
		}
	case missionevent.RuntimeFailurePayload:
		return value.RunID
	case *missionevent.RuntimeFailurePayload:
		if value != nil {
			return value.RunID
		}
	}
	return ""
}
