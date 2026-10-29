package lifecycle

import (
	"context"

	"orrery_v2/internal/platform/clock"
)

func NewTestSignalBus() (*InMemorySignalBus, *SignalRecorder) {
	recorder := NewSignalRecorder()
	bus := NewInMemorySignalBus(SignalBusOptions{Clock: clock.Real{}})
	return bus, recorder
}

type recordingHandler struct {
	name string
	fn   func(SignalEnvelope)
}

func NewRecordingHandler(name string, fn func(SignalEnvelope)) SignalHandler {
	return recordingHandler{name: name, fn: fn}
}

func (h recordingHandler) Name() string {
	return h.name
}

func (h recordingHandler) HandleSignal(_ context.Context, signal SignalEnvelope) error {
	if h.fn != nil {
		h.fn(signal)
	}
	return nil
}

type failingHandler struct {
	name string
	err  error
}

func NewFailingHandler(name string, err error) SignalHandler {
	return failingHandler{name: name, err: err}
}

func (h failingHandler) Name() string {
	return h.name
}

func (h failingHandler) HandleSignal(context.Context, SignalEnvelope) error {
	return h.err
}
