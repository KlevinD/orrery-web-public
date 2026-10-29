package discussionsession

import (
	"strings"
	"testing"
)

func TestResumedRuntimeAlwaysReceivesCurrentStablePreferences(t *testing.T) {
	request := DiscussionAgentRequest{
		ConversationKind: ConversationKindPersonalAssistant, ConversationID: "private-conversation",
		LogicalAgentID: PersonalAssistantAgentID("tenant", "owner"), Content: "继续",
		Memory: DiscussionAgentMemoryContext{PersonalDigest: DiscussionAgentMemoryDigest{
			DigestID: "preferences", RetrievalMode: "stable_preferences", MemoryKind: "personal", Summary: "默认用中文回答",
		}},
	}
	session := discussionExternalCLISession{SessionID: "ongoing", RunID: "previous", ContextFingerprint: discussionExternalCLIContextFingerprint(request)}
	parts := discussionExternalCLIContextParts(request, session)
	matches := 0
	for _, part := range parts {
		if strings.Contains(part.Content, request.Memory.PersonalDigest.Summary) {
			matches++
			if !part.Required {
				t.Fatal("stable preferences can be dropped by context truncation")
			}
		}
		if strings.Contains(part.Content, "<discussion_context>") {
			t.Fatal("resuming preferences unnecessarily repeated the entire context")
		}
	}
	if matches != 1 {
		t.Fatalf("resumed runtime received %d preference blocks", matches)
	}
	request.Memory.PersonalDigest.Summary = "默认用英文回答"
	prompt := discussionExternalCLIPrompt(request, session)
	if strings.Count(prompt, "默认用英文回答") != 1 || strings.Contains(prompt, "默认用中文回答") {
		t.Fatalf("updated preference not refreshed: %s", prompt)
	}
}

func TestResumedRuntimeDoesNotReinjectOrdinaryMemoryDigest(t *testing.T) {
	request := DiscussionAgentRequest{Memory: DiscussionAgentMemoryContext{PersonalDigest: DiscussionAgentMemoryDigest{
		DigestID: "history", RetrievalMode: "profile_digest", Summary: "Detailed prior task experience",
	}}}
	session := discussionExternalCLISession{SessionID: "ongoing", ContextFingerprint: discussionExternalCLIContextFingerprint(request)}
	if prompt := discussionExternalCLIPrompt(request, session); strings.Contains(prompt, request.Memory.PersonalDigest.Summary) {
		t.Fatal("ordinary recall was automatically repeated")
	}
}
