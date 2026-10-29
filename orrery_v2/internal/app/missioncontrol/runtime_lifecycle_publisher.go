package missioncontrol

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/outputpolicy"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/mission/projection"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/eventstore"
	"orrery_v2/internal/platform/idgen"
)

type RuntimeLifecyclePublisherOptions struct {
	Events      MissionEventSinkPort
	EventReader MissionEventReaderPort
	Repo        interface {
		ProjectionRepository
		Save(context.Context, projection.ReadModel) error
	}
	IDGenerator idgen.ErrorGenerator
	Clock       clock.Clock
	Projector   projection.Projector
}

type RuntimeLifecyclePublisher struct {
	events MissionEventSinkPort
	reader MissionEventReaderPort
	repo   interface {
		ProjectionRepository
		Save(context.Context, projection.ReadModel) error
	}
	ids       idgen.ErrorGenerator
	clock     clock.Clock
	projector projection.Projector
}

type runtimeProjectionRepairRepository interface {
	repairRuntimeProjection(context.Context, string, projection.ReadModel, projection.ReadModel) (bool, error)
}

func NewRuntimeLifecyclePublisher(opts RuntimeLifecyclePublisherOptions) *RuntimeLifecyclePublisher {
	projector := opts.Projector
	if projector == nil {
		projector = projection.NewProjector()
	}
	return &RuntimeLifecyclePublisher{
		events:    opts.Events,
		reader:    opts.EventReader,
		repo:      opts.Repo,
		ids:       opts.IDGenerator,
		clock:     opts.Clock,
		projector: projector,
	}
}

func (p *RuntimeLifecyclePublisher) PublishRuntimeLifecycle(ctx context.Context, event runtimedaemon.RuntimeLifecycleEvent) error {
	if err := p.requireDependencies(); err != nil {
		return err
	}
	eventType, ok := runtimeLifecycleMissionEventType(event.Kind)
	if !ok {
		return fmt.Errorf("%w: unsupported runtime lifecycle kind %q", ErrInvalidRequest, event.Kind)
	}
	tenantID := missionTenantID(event.TenantID)
	model, err := loadMissionProjection(ctx, p.repo, tenantID, event.MissionID)
	if err != nil {
		return err
	}
	eventTypeKey := runtimeLifecycleIdempotencyKey(event.MissionID, event.RunID, eventType)
	streamVersion, _, err := p.nextRuntimeLifecycleStreamVersion(ctx, tenantID, event.MissionID, event.RunID, eventType, eventTypeKey, model)
	if err != nil {
		return err
	}
	envelope, err := p.runtimeLifecycleEnvelope(tenantID, event.MissionID, event.AgentID, event.RunID, streamVersion, eventType, missionevent.RuntimeLifecyclePayload{
		TaskID:                event.TaskID,
		AgentID:               event.AgentID,
		RunID:                 event.RunID,
		RuntimeKind:           strings.TrimSpace(event.RuntimeKind),
		QueueID:               strings.TrimSpace(event.QueueID),
		Status:                strings.TrimSpace(event.Status),
		WorkerID:              strings.TrimSpace(event.WorkerID),
		Message:               outputpolicy.PublicRuntimeText(event.Message, string(event.Kind)),
		LastHeartbeatAt:       event.LastHeartbeatAt,
		LeaseExpiresAt:        event.LeaseExpiresAt,
		Usage:                 cloneRuntimeUsage(event.Usage),
		ResponsibilityContext: runtimeResponsibilityContext(model, event.RunID),
	})
	if err != nil {
		return err
	}
	if isTerminalRuntimeMissionEvent(eventType) {
		return p.appendTerminalAndProject(ctx, tenantID, model, envelope)
	}
	return p.appendAndProject(ctx, tenantID, model, envelope)
}

func (p *RuntimeLifecyclePublisher) PublishRuntimeFailure(ctx context.Context, event runtimedaemon.RuntimeFailureEvent) error {
	if err := p.requireDependencies(); err != nil {
		return err
	}
	eventType, ok := runtimeFailureMissionEventType(event.Kind)
	if !ok {
		return fmt.Errorf("%w: unsupported runtime failure kind %q", ErrInvalidRequest, event.Kind)
	}
	tenantID := missionTenantID(event.TenantID)
	model, err := loadMissionProjection(ctx, p.repo, tenantID, event.MissionID)
	if err != nil {
		return err
	}
	eventTypeKey := runtimeLifecycleIdempotencyKey(event.MissionID, event.RunID, eventType)
	streamVersion, _, err := p.nextRuntimeLifecycleStreamVersion(ctx, tenantID, event.MissionID, event.RunID, eventType, eventTypeKey, model)
	if err != nil {
		return err
	}
	envelope, err := p.runtimeLifecycleEnvelope(tenantID, event.MissionID, event.AgentID, event.RunID, streamVersion, eventType, missionevent.RuntimeFailurePayload{
		TaskID:                event.TaskID,
		AgentID:               event.AgentID,
		RunID:                 event.RunID,
		Code:                  firstString(strings.TrimSpace(event.Code), "agent_runtime_failed"),
		Message:               outputpolicy.PublicRuntimeText(event.Message, string(event.Kind)),
		Metadata:              cloneStringMap(event.Metadata),
		ResponsibilityContext: runtimeResponsibilityContext(model, event.RunID),
	})
	if err != nil {
		return err
	}
	return p.appendTerminalAndProject(ctx, tenantID, model, envelope)
}

func runtimeResponsibilityContext(model projection.ReadModel, runID agentcore.AgentRunID) *agentcore.ResponsibilityContext {
	for _, session := range model.RuntimeSessions {
		if session.RunID == runID {
			return agentcore.CloneResponsibilityContext(session.ResponsibilityContext)
		}
	}
	return nil
}

func (p *RuntimeLifecyclePublisher) runtimeLifecycleEnvelope(tenantID string, missionID missioncore.MissionID, agentID agentcore.AgentID, runID agentcore.AgentRunID, streamVersion int, eventType missionevent.EventType, payload missionevent.Payload) (missionevent.Envelope, error) {
	if missionID.IsZero() || agentID.IsZero() || runID == "" || streamVersion <= 0 || payload == nil {
		return missionevent.Envelope{}, ErrInvalidRequest
	}
	eventID, err := p.ids.NewIDContext()
	if err != nil {
		return missionevent.Envelope{}, err
	}
	metadata := map[string]string{
		"source": "agent_runtime_daemon",
		"run_id": runID.String(),
	}
	var responsibility *agentcore.ResponsibilityContext
	switch value := payload.(type) {
	case missionevent.RuntimeLifecyclePayload:
		responsibility = value.ResponsibilityContext
	case *missionevent.RuntimeLifecyclePayload:
		if value != nil {
			responsibility = value.ResponsibilityContext
		}
	case missionevent.RuntimeFailurePayload:
		responsibility = value.ResponsibilityContext
	case *missionevent.RuntimeFailurePayload:
		if value != nil {
			responsibility = value.ResponsibilityContext
		}
	}
	for key, value := range responsibilityContextMetadata(responsibility) {
		metadata[key] = value
	}
	return missionevent.Envelope{
		EventID:        eventID,
		TenantID:       missionTenantID(tenantID),
		MissionID:      missionID,
		StreamID:       missionevent.MissionStreamID(tenantID, missionID),
		StreamVersion:  streamVersion,
		EventType:      eventType,
		EventVersion:   payload.EventPayloadVersion(),
		Actor:          missionevent.ActorRef{Kind: missionevent.ActorKindAgent, ID: agentID.String()},
		CorrelationID:  runID.String(),
		IdempotencyKey: runtimeLifecycleIdempotencyKey(missionID, runID, eventType),
		OccurredAt:     p.now(),
		Payload:        payload,
		Metadata:       metadata,
	}, nil
}

func (p *RuntimeLifecyclePublisher) appendAndProject(ctx context.Context, tenantID string, model projection.ReadModel, event missionevent.Envelope) error {
	if err := p.events.Append(ctx, event); err != nil {
		return err
	}
	return p.projectAppendedRuntimeEvent(ctx, tenantID, model, event)
}

func (p *RuntimeLifecyclePublisher) appendTerminalAndProject(ctx context.Context, tenantID string, model projection.ReadModel, event missionevent.Envelope) error {
	const maxAttempts = 8
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := p.events.Append(ctx, event); err == nil {
			return p.projectAppendedRuntimeEvent(ctx, tenantID, model, event)
		} else if p.reader == nil {
			return err
		} else {
			streamVersion, replay, stateErr := p.nextRuntimeLifecycleStreamVersion(
				ctx, tenantID, event.MissionID, runtimeLifecycleMissionEventRunID(event.Payload), event.EventType, event.IdempotencyKey, model,
			)
			if stateErr != nil {
				return stateErr
			}
			if replay {
				return p.projectAppendedRuntimeEvent(ctx, tenantID, model, event)
			}
			if !errors.Is(err, eventstore.ErrConflict) && !errors.Is(err, ErrConflict) {
				return err
			}
			event.StreamVersion = streamVersion
		}
	}
	return ErrConflict
}

func (p *RuntimeLifecyclePublisher) projectAppendedRuntimeEvent(ctx context.Context, tenantID string, model projection.ReadModel, event missionevent.Envelope) error {
	if p.reader == nil {
		projectRuntimeEvent(&model, event, p.now())
		return saveMissionProjection(ctx, p.repo, tenantID, model)
	}
	events, err := loadMissionEvents(ctx, p.reader, tenantID, event.MissionID)
	if err != nil {
		return err
	}
	for _, persisted := range events {
		matchesEvent := persisted.EventID == event.EventID || (event.IdempotencyKey != "" && persisted.IdempotencyKey == event.IdempotencyKey)
		if matchesEvent && persisted.StreamVersion <= model.Checkpoint.Version && runtimeProjectionNeedsPersistedEventRepair(model, persisted) {
			repairRepo, ok := p.repo.(runtimeProjectionRepairRepository)
			if !ok {
				return ErrServiceDependency
			}
			repaired, err := repairRuntimeProjectionFromPersistedEvent(model, persisted, p.now())
			if err != nil {
				return err
			}
			swapped, err := repairRepo.repairRuntimeProjection(ctx, tenantID, model, repaired)
			if err != nil {
				return err
			}
			if !swapped {
				return ErrConflict
			}
			model = repaired
			break
		}
	}
	appendedEventLoaded := false
	for _, persisted := range events {
		if persisted.EventID == event.EventID || (event.IdempotencyKey != "" && persisted.IdempotencyKey == event.IdempotencyKey) {
			appendedEventLoaded = true
		}
		if persisted.StreamVersion <= model.Checkpoint.Version {
			continue
		}
		if err := p.projector.Apply(ctx, &model, persisted); err != nil {
			return err
		}
	}
	if !appendedEventLoaded {
		projectRuntimeEvent(&model, event, p.now())
	}
	return saveMissionProjection(ctx, p.repo, tenantID, model)
}

func (p *RuntimeLifecyclePublisher) nextRuntimeLifecycleStreamVersion(ctx context.Context, tenantID string, missionID missioncore.MissionID, runID agentcore.AgentRunID, eventType missionevent.EventType, idempotencyKey string, model projection.ReadModel) (int, bool, error) {
	if !isTerminalRuntimeMissionEvent(eventType) || p == nil || p.reader == nil {
		streamVersion, err := p.nextStreamVersion(ctx, tenantID, missionID, model)
		return streamVersion, false, err
	}
	events, err := loadMissionEvents(ctx, p.reader, tenantID, missionID)
	if err != nil {
		return 0, false, err
	}
	matchingVersion := 0
	for _, event := range events {
		if !isTerminalRuntimeMissionEvent(event.EventType) || runtimeLifecycleMissionEventRunID(event.Payload) != runID {
			continue
		}
		if event.EventType == eventType && idempotencyKey != "" && event.IdempotencyKey == idempotencyKey {
			matchingVersion = event.StreamVersion
			continue
		}
		return 0, false, ErrAgentRunTerminal
	}
	if matchingVersion > 0 {
		return matchingVersion, true, nil
	}
	return len(events) + 1, false, nil
}

func runtimeLifecycleMissionEventRunID(payload missionevent.Payload) agentcore.AgentRunID {
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

func isTerminalRuntimeMissionEvent(eventType missionevent.EventType) bool {
	switch eventType {
	case missionevent.EventAgentRunCompleted, missionevent.EventAgentRunFailed, missionevent.EventAgentRunTimedOut,
		missionevent.EventAgentRunCancelled, missionevent.EventAgentRunBlocked:
		return true
	default:
		return false
	}
}

func (p *RuntimeLifecyclePublisher) nextStreamVersion(ctx context.Context, tenantID string, missionID missioncore.MissionID, model projection.ReadModel) (int, error) {
	if p != nil && p.reader != nil {
		events, err := loadMissionEvents(ctx, p.reader, tenantID, missionID)
		if err != nil {
			return 0, err
		}
		return len(events) + 1, nil
	}
	return model.Checkpoint.Version + 1, nil
}

func (p *RuntimeLifecyclePublisher) requireDependencies() error {
	if p == nil || p.events == nil || p.repo == nil || p.ids == nil || p.projector == nil {
		return ErrServiceDependency
	}
	return nil
}

func (p *RuntimeLifecyclePublisher) now() time.Time {
	if p != nil && p.clock != nil {
		return p.clock.Now().UTC()
	}
	return time.Now().UTC()
}

func runtimeLifecycleMissionEventType(kind runtimedaemon.RuntimeLifecycleKind) (missionevent.EventType, bool) {
	switch kind {
	case runtimedaemon.RuntimeLifecycleStarted:
		return missionevent.EventAgentRunStarted, true
	case runtimedaemon.RuntimeLifecycleCompleted:
		return missionevent.EventAgentRunCompleted, true
	default:
		return "", false
	}
}

func runtimeFailureMissionEventType(kind runtimedaemon.RuntimeLifecycleKind) (missionevent.EventType, bool) {
	switch kind {
	case runtimedaemon.RuntimeLifecycleFailed:
		return missionevent.EventAgentRunFailed, true
	case runtimedaemon.RuntimeLifecycleTimedOut:
		return missionevent.EventAgentRunTimedOut, true
	case runtimedaemon.RuntimeLifecycleCancelled:
		return missionevent.EventAgentRunCancelled, true
	case runtimedaemon.RuntimeLifecycleBlocked:
		return missionevent.EventAgentRunBlocked, true
	default:
		return "", false
	}
}

func runtimeLifecycleIdempotencyKey(missionID missioncore.MissionID, runID agentcore.AgentRunID, eventType missionevent.EventType) string {
	return strings.Join([]string{"agent-runtime", missionID.String(), runID.String(), string(eventType)}, "|")
}

func runtimeProjectionNeedsPersistedEventRepair(model projection.ReadModel, event missionevent.Envelope) bool {
	var runID agentcore.AgentRunID
	var expected projection.RuntimeSessionStatus
	switch payload := event.Payload.(type) {
	case missionevent.RuntimeLifecyclePayload:
		runID = payload.RunID
		expected = runtimeProjectionStatus(event.EventType, payload.Status)
	case *missionevent.RuntimeLifecyclePayload:
		if payload != nil {
			runID = payload.RunID
			expected = runtimeProjectionStatus(event.EventType, payload.Status)
		}
	case missionevent.RuntimeFailurePayload:
		runID = payload.RunID
		expected = runtimeFailureProjectionStatus(event.EventType)
	case *missionevent.RuntimeFailurePayload:
		if payload != nil {
			runID = payload.RunID
			expected = runtimeFailureProjectionStatus(event.EventType)
		}
	}
	if runID == "" || expected == "" {
		return false
	}
	for _, session := range model.RuntimeSessions {
		if session.RunID != runID {
			continue
		}
		return !isTerminalRuntimeSession(session.Status) && session.Status != expected
	}
	return true
}

func repairRuntimeProjectionFromPersistedEvent(model projection.ReadModel, event missionevent.Envelope, fallbackAt time.Time) (projection.ReadModel, error) {
	repaired, err := cloneMemoryProjection(model)
	if err != nil {
		return projection.ReadModel{}, err
	}
	checkpoint := repaired.Checkpoint
	progress := repaired.ProgressGraph
	updatedAt := repaired.UpdatedAt
	projectRuntimeEvent(&repaired, event, fallbackAt)
	repaired.Checkpoint = checkpoint
	repaired.ProgressGraph = progress
	repaired.UpdatedAt = updatedAt
	return repaired, nil
}

func cloneRuntimeUsage(src map[string]agentcore.TokenUsage) map[string]agentcore.TokenUsage {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]agentcore.TokenUsage, len(src))
	for model, usage := range src {
		model = strings.TrimSpace(model)
		if model == "" || isZeroRuntimeUsage(usage) {
			continue
		}
		out[model] = usage
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func isZeroRuntimeUsage(usage agentcore.TokenUsage) bool {
	return usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.CacheReadTokens == 0 && usage.CacheWriteTokens == 0 && usage.CachedInputTokens == 0 && usage.ReasoningTokens == 0 && !usage.CachedInputTokensKnown && !usage.ReasoningTokensKnown
}
