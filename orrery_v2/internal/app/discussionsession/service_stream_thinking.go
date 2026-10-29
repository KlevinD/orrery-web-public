package discussionsession

import (
	"strings"
	"time"
)

const (
	discussionThinkingFlushInterval = 32 * time.Millisecond
	discussionThinkingFlushBytes    = 64
)

type discussionStreamThinkingBuffer struct {
	pending strings.Builder
	timer   *time.Timer
	timerC  <-chan time.Time
}

func (b *discussionStreamThinkingBuffer) append(delta string) bool {
	if b == nil || delta == "" {
		return false
	}
	b.pending.WriteString(delta)
	if b.pending.Len() >= discussionThinkingFlushBytes {
		return true
	}
	if b.timer == nil {
		b.timer = time.NewTimer(discussionThinkingFlushInterval)
		b.timerC = b.timer.C
	}
	return false
}

func (b *discussionStreamThinkingBuffer) timerChannel() <-chan time.Time {
	if b == nil {
		return nil
	}
	return b.timerC
}

func (b *discussionStreamThinkingBuffer) flush() string {
	if b == nil {
		return ""
	}
	b.stopTimer()
	delta := b.pending.String()
	b.pending.Reset()
	return delta
}

func (b *discussionStreamThinkingBuffer) close() {
	if b == nil {
		return
	}
	b.stopTimer()
}

func (b *discussionStreamThinkingBuffer) stopTimer() {
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
