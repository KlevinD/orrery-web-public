package discussionsession

import (
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/agent/outputpolicy"
)

const (
	discussionTokenFlushInterval = 32 * time.Millisecond
	discussionTokenFlushBytes    = 64
)

// discussionStreamTokenBuffer applies the public-output policy before batching
// delivery updates. It never writes to the repository.
type discussionStreamTokenBuffer struct {
	mu       sync.Mutex
	all      strings.Builder
	pending  strings.Builder
	privacy  outputpolicy.PrivateReasoningFilter
	finished bool
	timer    *time.Timer
	timerC   <-chan time.Time
}

func (b *discussionStreamTokenBuffer) append(delta string) bool {
	if b == nil || delta == "" {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.finished {
		return false
	}
	visible := b.privacy.Write(delta)
	if visible == "" {
		return false
	}
	b.all.WriteString(visible)
	b.pending.WriteString(visible)
	if b.pending.Len() >= discussionTokenFlushBytes {
		return true
	}
	if b.timer == nil {
		b.timer = time.NewTimer(discussionTokenFlushInterval)
		b.timerC = b.timer.C
	}
	return false
}

func (b *discussionStreamTokenBuffer) finish() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.finished {
		return
	}
	b.finished = true
	if tail := b.privacy.Finish(); tail != "" {
		b.all.WriteString(tail)
		b.pending.WriteString(tail)
	}
}

func (b *discussionStreamTokenBuffer) privacyResult() outputpolicy.FilterResult {
	if b == nil {
		return outputpolicy.FilterResult{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.privacy.Result("")
}

func (b *discussionStreamTokenBuffer) timerChannel() <-chan time.Time {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.timerC
}

func (b *discussionStreamTokenBuffer) flush() string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopTimerLocked()
	delta := b.pending.String()
	b.pending.Reset()
	return delta
}

func (b *discussionStreamTokenBuffer) cached() string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.all.String()
}

func (b *discussionStreamTokenBuffer) close() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopTimerLocked()
}

func (b *discussionStreamTokenBuffer) stopTimerLocked() {
	if b.timer == nil {
		return
	}
	if !b.timer.Stop() {
		select {
		case <-b.timer.C:
		default:
		}
	}
	b.timer = nil
	b.timerC = nil
}

func partialDiscussionStreamResponse(response DiscussionAgentResponse, tokens *discussionStreamTokenBuffer, displayEvents []DiscussionAgentStreamEvent, runID string) DiscussionAgentResponse {
	tokens.finish()
	response.AssistantMessage = tokens.cached()
	response.DisplayTimeline = completedStreamDisplayTimeline(displayEvents, runID, response.DisplayTimeline)
	applyStreamOutputPrivacyMetadata(&response, tokens)
	applyDiscussionResponseOutputPolicy(&response)
	return response
}

func (s *Service) registerDiscussionStreamTokenBuffer(runID string, tokens *discussionStreamTokenBuffer) {
	if s == nil || tokens == nil || strings.TrimSpace(runID) == "" {
		return
	}
	s.activeRunsMu.Lock()
	defer s.activeRunsMu.Unlock()
	if s.streamTokenBuffers == nil {
		s.streamTokenBuffers = map[string]*discussionStreamTokenBuffer{}
	}
	s.streamTokenBuffers[runID] = tokens
}

func (s *Service) unregisterDiscussionStreamTokenBuffer(runID string, tokens *discussionStreamTokenBuffer) {
	if s == nil || tokens == nil || strings.TrimSpace(runID) == "" {
		return
	}
	s.activeRunsMu.Lock()
	defer s.activeRunsMu.Unlock()
	if s.streamTokenBuffers[runID] == tokens {
		delete(s.streamTokenBuffers, runID)
	}
}

func (s *Service) cachedDiscussionStreamResponse(runID string) (DiscussionAgentResponse, bool) {
	if s == nil || strings.TrimSpace(runID) == "" {
		return DiscussionAgentResponse{}, false
	}
	s.activeRunsMu.Lock()
	tokens := s.streamTokenBuffers[runID]
	s.activeRunsMu.Unlock()
	if tokens == nil {
		return DiscussionAgentResponse{}, false
	}
	content := tokens.cached()
	if strings.TrimSpace(content) == "" {
		return DiscussionAgentResponse{}, false
	}
	return DiscussionAgentResponse{AssistantMessage: content}, true
}
