package discussionsession

import (
	"context"
	"sync"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func TestCanonicalRuntimeProgressWaitsForFirstEventBeforeReadingRequest(t *testing.T) {
	reader := &delayedRuntimeRecordReader{events: make(chan agentcore.SessionEnvelope, 1)}
	progress, unsubscribe := canonicalRuntimeProgress(reader, "run-a")
	defer unsubscribe()
	reader.mu.Lock()
	if reader.requestReads != 0 {
		reader.mu.Unlock()
		t.Fatalf("request was read before the runtime record existed: %d", reader.requestReads)
	}
	reader.request = agentcore.RuntimeRequest{RunID: "run-a", RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{"runtime_kind": "codex"}}}
	reader.hasRequest = true
	reader.mu.Unlock()
	reader.events <- agentcore.SessionEnvelope{
		SessionID: "session-a", TurnGeneration: 1, Sequence: 1, Timestamp: time.Now().UTC(),
		Source: agentcore.EventSourceProvider, Projection: agentcore.EventProjectionCanonical,
		Event: agentcore.SessionEvent{Kind: agentcore.EventMessageDelta, MessageDelta: &agentcore.MessageDeltaEvent{ItemID: "message-a", Text: "streamed"}},
	}
	select {
	case message := <-progress:
		if message.Content != "streamed" || message.SessionID != "session-a" {
			t.Fatalf("progress = %#v", message)
		}
	case <-time.After(time.Second):
		t.Fatal("canonical progress was not projected")
	}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.requestReads != 1 {
		t.Fatalf("request reads = %d, want 1 after first event", reader.requestReads)
	}
}

type delayedRuntimeRecordReader struct {
	mu           sync.Mutex
	request      agentcore.RuntimeRequest
	hasRequest   bool
	requestReads int
	events       chan agentcore.SessionEnvelope
	closeOnce    sync.Once
}

func (r *delayedRuntimeRecordReader) RuntimeRequest(agentcore.AgentRunID) (agentcore.RuntimeRequest, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requestReads++
	return r.request, r.hasRequest
}

func (*delayedRuntimeRecordReader) RuntimeResponse(agentcore.AgentRunID) (agentcore.RuntimeResponse, bool) {
	return agentcore.RuntimeResponse{}, false
}

func (*delayedRuntimeRecordReader) RuntimeEvents(agentcore.AgentRunID) []agentcore.SessionEnvelope {
	return nil
}

func (r *delayedRuntimeRecordReader) SubscribeRuntimeEvents(agentcore.AgentRunID) (<-chan agentcore.SessionEnvelope, func()) {
	return r.events, func() { r.closeOnce.Do(func() { close(r.events) }) }
}

func (*delayedRuntimeRecordReader) DeleteRuntimeRecord(context.Context, agentcore.AgentRunID) error {
	return nil
}
