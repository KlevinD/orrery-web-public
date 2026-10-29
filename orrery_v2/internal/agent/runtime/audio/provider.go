package audio

import "context"

// These are Orrery's speech contracts. Backend implementations do not own users,
// conversations, approval decisions, preferences or channel delivery.
type Transcriber interface {
	Transcribe(context.Context, TranscriptionRequest) (Transcript, error)
}

type Synthesizer interface {
	Synthesize(context.Context, SpeechRequest) (Speech, error)
}

type Provider interface {
	Transcriber
	Synthesizer
}

var _ Provider = (*Gateway)(nil)
var _ Provider = (*OpenClaw)(nil)
