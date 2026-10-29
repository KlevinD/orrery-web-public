package core

import "testing"

func TestThoughtDeltaReasoningKindValidation(t *testing.T) {
	for _, kind := range []ReasoningContentKind{"", ReasoningContentRaw, ReasoningContentSummary} {
		event := SessionEvent{Kind: EventThoughtDelta, ThoughtDelta: &ThoughtDeltaEvent{ItemID: "thought-1", Text: "reasoning", ReasoningKind: kind}}
		if err := event.Validate(); err != nil {
			t.Fatalf("reasoning kind %q rejected: %v", kind, err)
		}
		if cloned := event.Clone(); cloned.ThoughtDelta == nil || cloned.ThoughtDelta.ReasoningKind != kind {
			t.Fatalf("reasoning kind %q was not cloned: %#v", kind, cloned)
		}
	}

	invalid := SessionEvent{Kind: EventThoughtDelta, ThoughtDelta: &ThoughtDeltaEvent{ItemID: "thought-1", ReasoningKind: "public"}}
	if err := invalid.Validate(); err == nil {
		t.Fatal("invalid reasoning kind was accepted")
	}
}

func TestFinalizedReasoningKindOnlyBelongsToThinking(t *testing.T) {
	event := SessionEvent{Kind: EventMessageFinalized, MessageFinalized: &MessageFinalizedEvent{Message: FinalizedMessage{
		ItemID:        "message-1",
		Kind:          ItemText,
		Content:       "answer",
		ReasoningKind: ReasoningContentSummary,
	}}}
	if err := event.Validate(); err == nil {
		t.Fatal("text item accepted a reasoning kind")
	}
}
