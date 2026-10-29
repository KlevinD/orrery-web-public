package claude

import (
	"context"
	"sort"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func (s *NativeSession) emit(generation uint64, event agentcore.SessionEvent) {
	if err := event.Validate(); err != nil {
		event = agentcore.SessionEvent{Kind: agentcore.EventAdapterSpecific, AdapterSpecific: &agentcore.AdapterSpecificEvent{Tag: "claude_invalid_mapping", Summary: "provider event was rejected by canonical validation"}}
	}
	envelope := s.nativeEnvelope(generation, event)
	s.eventMu.RLock()
	defer s.eventMu.RUnlock()
	if s.eventsClosed {
		return
	}
	select {
	case s.eventIn <- envelope:
	case <-s.eventAbort:
	case <-s.ctx.Done():
	}
}

func (s *NativeSession) nativeEnvelope(generation uint64, event agentcore.SessionEvent) agentcore.SessionEnvelope {
	projection := agentcore.EventProjectionCanonical
	if event.Kind == agentcore.EventAdapterSpecific {
		projection = agentcore.EventProjectionDiagnostic
	}
	return agentcore.SessionEnvelope{
		SessionID: s.sessionID, TurnGeneration: generation, Timestamp: s.now().UTC(),
		Source: agentcore.EventSourceProvider, Projection: projection, Event: event,
	}
}

func (s *NativeSession) closeEvents() {
	s.eventsCloseOnce.Do(func() {
		s.eventMu.Lock()
		s.eventsClosed = true
		close(s.eventIn)
		s.eventMu.Unlock()
	})
}

func (s *NativeSession) runEventPump() {
	defer close(s.eventPumpDone)
	defer close(s.events)
	capacity := s.native.EventCapacity
	if capacity < 1 {
		capacity = cap(s.eventIn)
	}
	if capacity < 1 {
		capacity = defaultClaudeEventCapacity
	}
	backpressureWait := s.native.BackpressureWait
	if backpressureWait <= 0 {
		backpressureWait = defaultClaudeBackpressureWait
	}
	queue := make([]agentcore.SessionEnvelope, 0, capacity+2)
	input := s.eventIn
	var pending *agentcore.SessionEnvelope
	var blockedSince time.Time
	for input != nil || pending != nil || len(queue) != 0 {
		if pending != nil && len(queue) < capacity {
			queue = append(queue, *pending)
			pending = nil
			blockedSince = time.Time{}
		}
		var output chan agentcore.SessionEnvelope
		var next agentcore.SessionEnvelope
		if len(queue) != 0 {
			output = s.events
			next = queue[0]
		}
		var receive <-chan agentcore.SessionEnvelope
		if pending == nil {
			receive = input
		}
		var timeout <-chan time.Time
		var timer *time.Timer
		if pending != nil {
			remaining := backpressureWait - time.Since(blockedSince)
			if remaining <= 0 {
				remaining = time.Nanosecond
			}
			timer = time.NewTimer(remaining)
			timeout = timer.C
		}
		select {
		case envelope, ok := <-receive:
			if !ok {
				input = nil
				break
			}
			if len(queue) < capacity {
				queue = append(queue, envelope)
			} else if !coalesceClaudeDelta(queue, envelope) {
				pending = &envelope
				blockedSince = time.Now()
			}
		case output <- next:
			queue[0] = agentcore.SessionEnvelope{}
			queue = queue[1:]
		case <-timeout:
			queue = append(queue, s.claudeBackpressureEvents()...)
			pending = nil
			input = nil
			s.stopAfterBackpressure()
		case <-s.ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

func coalesceClaudeDelta(queue []agentcore.SessionEnvelope, incoming agentcore.SessionEnvelope) bool {
	for index := len(queue) - 1; index >= 0; index-- {
		candidate := &queue[index]
		if candidate.TurnGeneration != incoming.TurnGeneration || candidate.Event.Kind != incoming.Event.Kind {
			continue
		}
		switch incoming.Event.Kind {
		case agentcore.EventMessageDelta:
			if candidate.Event.MessageDelta.ItemID == incoming.Event.MessageDelta.ItemID && len(candidate.Event.MessageDelta.Text)+len(incoming.Event.MessageDelta.Text) <= 16<<20 {
				candidate.Event.MessageDelta.Text += incoming.Event.MessageDelta.Text
				return true
			}
		case agentcore.EventThoughtDelta:
			if candidate.Event.ThoughtDelta.ItemID == incoming.Event.ThoughtDelta.ItemID && len(candidate.Event.ThoughtDelta.Text)+len(incoming.Event.ThoughtDelta.Text) <= 16<<20 {
				candidate.Event.ThoughtDelta.Text += incoming.Event.ThoughtDelta.Text
				return true
			}
		case agentcore.EventToolOutputDelta:
			if candidate.Event.ToolOutputDelta.ItemID == incoming.Event.ToolOutputDelta.ItemID && len(candidate.Event.ToolOutputDelta.Text)+len(incoming.Event.ToolOutputDelta.Text) <= 16<<20 {
				candidate.Event.ToolOutputDelta.Text += incoming.Event.ToolOutputDelta.Text
				return true
			}
		}
		return false
	}
	return false
}

func (s *NativeSession) claudeBackpressureEvents() []agentcore.SessionEnvelope {
	s.dead.Store(true)
	s.mu.Lock()
	generations := make([]uint64, 0, len(s.active))
	for generation := range s.active {
		if !s.terminated[generation] {
			s.terminated[generation] = true
			generations = append(generations, generation)
		}
	}
	s.active = make(map[uint64]bool)
	current := s.turnGeneration
	s.mu.Unlock()
	sort.Slice(generations, func(i, j int) bool { return generations[i] < generations[j] })
	events := make([]agentcore.SessionEnvelope, 0, len(generations)+1)
	for _, generation := range generations {
		events = append(events, s.nativeEnvelope(generation, claudeFailedTurn(generation, "claude event backpressure limit exceeded")))
	}
	events = append(events, s.nativeEnvelope(current, agentcore.SessionEvent{Kind: agentcore.EventDetached, Detached: &agentcore.DetachedEvent{RedactedSummary: "claude event backpressure limit exceeded"}}))
	return events
}

func (s *NativeSession) stopAfterBackpressure() {
	s.backpressureOnce.Do(func() {
		if s.eventAbort != nil {
			close(s.eventAbort)
		}
		s.failPending(agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeQueueFull, "claude event queue is full", true))
		if binding := s.currentBinding(); binding != nil {
			go func() { _ = binding.Stop(context.Background()) }()
		}
	})
}

func (s *NativeSession) emitDiagnostic(tag, summary string) {
	s.emit(s.currentGeneration(), agentcore.SessionEvent{Kind: agentcore.EventAdapterSpecific, AdapterSpecific: &agentcore.AdapterSpecificEvent{Tag: claudeTag(tag), Summary: claudeDiagnosticText(summary, "Claude adapter diagnostic")}})
}

func (s *NativeSession) emitNotice(message string) {
	s.emit(s.currentGeneration(), agentcore.SessionEvent{Kind: agentcore.EventNotice, Notice: &agentcore.NoticeEvent{Level: agentcore.NoticeWarning, Message: claudeDiagnosticText(message, "Claude reported a warning")}})
}
