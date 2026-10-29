package lifecycle

import (
	"context"
	"fmt"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
)

type CoreSignalSink struct {
	Bus SignalBus
}

func NewCoreSignalSink(bus SignalBus) CoreSignalSink {
	return CoreSignalSink{Bus: bus}
}

func (s CoreSignalSink) EmitSignal(ctx context.Context, signal agentcore.Signal) error {
	if s.Bus == nil {
		return nil
	}
	name, err := toLifecycleSignalName(signal.Name)
	if err != nil {
		return err
	}
	_, err = s.Bus.Emit(ctx, SignalEnvelope{
		SignalID:       SignalID(fmt.Sprintf("%s-%s-%d", signal.RunID, signal.Name, signal.At.UnixNano())),
		Name:           name,
		TenantID:       signal.TenantID,
		MissionID:      missioncore.MissionID(signal.MissionID),
		AgentID:        signal.AgentID,
		AgentProfileID: signal.AgentProfileID,
		TaskID:         missioncore.TaskID(signal.TaskID),
		RunID:          signal.RunID,
		CorrelationID:  CorrelationID(signal.RunID),
		Attempt:        1,
		EmittedAt:      nonZeroTime(signal.At),
		Payload:        coreSignalPayload(signal),
		Metadata:       cloneCoreSignalMetadata(signal.Payload),
	})
	return err
}

func toLifecycleSignalName(name agentcore.SignalName) (SignalName, error) {
	candidate := SignalName(name)
	if !IsKnownSignalName(candidate) {
		return "", fmt.Errorf("unknown lifecycle signal %q", name)
	}
	return candidate, nil
}

func nonZeroTime(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now().UTC()
	}
	return t.UTC()
}

func cloneCoreSignalMetadata(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func coreSignalPayload(signal agentcore.Signal) SignalPayload {
	switch signal.Name {
	case agentcore.SignalAfterTaskAccepted:
		return TaskAcceptedPayload{AcceptedBy: signal.AgentID}
	case agentcore.SignalAgentBlocked, agentcore.SignalAgentFailed, agentcore.SignalAgentCancelled, agentcore.SignalAgentTimeout:
		return FailurePayload{
			Code:    firstCoreSignalPayloadValue(signal.Payload["code"], string(signal.Name)),
			Message: firstCoreSignalPayloadValue(signal.Payload["error"], signal.Payload["message"], string(signal.Name)),
		}
	default:
		return EmptyPayload{}
	}
}

func firstCoreSignalPayloadValue(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
