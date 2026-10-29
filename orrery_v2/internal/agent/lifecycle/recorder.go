package lifecycle

import (
	"context"
	"sync"
)

type SignalRecorder struct {
	mu      sync.Mutex
	name    string
	signals []SignalEnvelope
}

func NewSignalRecorder() *SignalRecorder {
	return &SignalRecorder{name: "signal-recorder"}
}

func (r *SignalRecorder) Name() string {
	return r.name
}

func (r *SignalRecorder) HandleSignal(_ context.Context, signal SignalEnvelope) error {
	r.Record(signal)
	return nil
}

func (r *SignalRecorder) Record(signal SignalEnvelope) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.signals = append(r.signals, signal)
}

func (r *SignalRecorder) Signals() []SignalEnvelope {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]SignalEnvelope, len(r.signals))
	copy(out, r.signals)
	return out
}

func (r *SignalRecorder) Names() []SignalName {
	signals := r.Signals()
	out := make([]SignalName, 0, len(signals))
	for _, signal := range signals {
		out = append(out, signal.Name)
	}
	return out
}
