package lifecycle

import (
	"fmt"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/idgen"
)

type SignalBuilderOptions struct {
	Clock       clock.Clock
	IDGenerator idgen.Generator
}

type SignalBuilder struct {
	clock       clock.Clock
	idGenerator idgen.Generator
}

type SignalBuildOptions struct {
	Parent        SignalEnvelope
	CorrelationID CorrelationID
	CausationID   CausationID
	Metadata      map[string]string
}

func NewSignalBuilder(opts SignalBuilderOptions) *SignalBuilder {
	if opts.Clock == nil {
		opts.Clock = clock.Real{}
	}
	if opts.IDGenerator == nil {
		opts.IDGenerator = randomSignalIDGenerator{generator: idgen.NewRandom("signal")}
	}
	return &SignalBuilder{clock: opts.Clock, idGenerator: opts.IDGenerator}
}

func (b *SignalBuilder) FromState(state AgentRunState, name SignalName, payload SignalPayload, opts SignalBuildOptions) (SignalEnvelope, error) {
	if payload == nil {
		payload = EmptyPayload{}
	}
	correlationID := opts.CorrelationID
	if correlationID == "" && opts.Parent.CorrelationID != "" {
		correlationID = opts.Parent.CorrelationID
	}
	if correlationID == "" {
		correlationID = CorrelationID(state.RunID.String())
	}
	causationID := opts.CausationID
	if causationID == "" && opts.Parent.SignalID != "" {
		causationID = CausationID(opts.Parent.SignalID)
	}

	signal := SignalEnvelope{
		SignalID:       SignalID(b.idGenerator.NewID()),
		Name:           name,
		TenantID:       state.TenantID,
		MissionID:      state.MissionID,
		AgentID:        state.AgentID,
		AgentProfileID: state.AgentProfileID,
		TaskID:         state.TaskID,
		RunID:          state.RunID,
		CorrelationID:  correlationID,
		CausationID:    causationID,
		Attempt:        state.Attempt,
		EmittedAt:      b.clock.Now(),
		Payload:        payload,
		Metadata:       cloneMetadata(opts.Metadata),
	}
	if len(signal.Metadata) == 0 {
		signal.Metadata = cloneMetadata(state.Metadata)
	}
	if err := signal.Validate(); err != nil {
		return SignalEnvelope{}, fmt.Errorf("build signal %s: %w", name, err)
	}
	return signal, nil
}

func cloneMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	out := make(map[string]string, len(metadata))
	for key, value := range metadata {
		out[key] = value
	}
	return out
}

type randomSignalIDGenerator struct {
	generator idgen.ErrorGenerator
}

func (g randomSignalIDGenerator) NewID() string {
	id, err := g.generator.NewIDContext()
	if err != nil || id == "" {
		return idgen.NewDeterministic("signal-fallback").NewID()
	}
	return id
}
