package discussionsession

import (
	"strings"
	"testing"
)

func TestDiscussionStreamThinkingBufferBatchesSmallDeltas(t *testing.T) {
	buffer := &discussionStreamThinkingBuffer{}
	defer buffer.close()

	if buffer.append("Checking ") {
		t.Fatal("small delta requested an early flush")
	}
	if buffer.append("the runtime.") {
		t.Fatal("small combined delta requested an early flush")
	}
	if buffer.timerChannel() == nil {
		t.Fatal("small delta did not schedule a flush")
	}
	if got := buffer.flush(); got != "Checking the runtime." {
		t.Fatalf("flushed thinking = %q", got)
	}
	if buffer.timerChannel() != nil {
		t.Fatal("flush left the timer active")
	}
}

func TestDiscussionStreamThinkingBufferFlushesAtByteLimit(t *testing.T) {
	buffer := &discussionStreamThinkingBuffer{}
	defer buffer.close()

	delta := strings.Repeat("x", discussionThinkingFlushBytes)
	if !buffer.append(delta) {
		t.Fatal("large delta did not request a flush")
	}
	if got := buffer.flush(); got != delta {
		t.Fatalf("flushed thinking length = %d", len(got))
	}
}
