package discussionsession

import (
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/retrievalevidence"
)

func TestDiscussionRetrievalEvidenceSurvivesContextAndKnowledgePrompt(t *testing.T) {
	item := retrievalevidence.Seal(retrievalevidence.Item{Source: retrievalevidence.Source{Kind: "memory_record", ID: "record", Version: "v1"}, Purpose: retrievalevidence.PurposeMemory, TruthState: retrievalevidence.TruthUnknown}, time.Now())
	ref := agentcore.MemoryDigestRef{ID: "digest", SummaryRef: "authorized memory", Evidence: []retrievalevidence.Item{item}}
	memory := DiscussionAgentMemoryContext{PersonalDigest: discussionAgentMemoryDigestFromRef(ref, "personal")}
	var pack agentcore.ContextPack
	applyDiscussionAgentMemoryToContextPack(&pack, memory)
	if len(pack.PersonalMemoryDigestRef.Evidence) != 1 || pack.PersonalMemoryDigestRef.Evidence[0].InputHash != item.InputHash {
		t.Fatalf("lost evidence: %+v", pack.PersonalMemoryDigestRef)
	}
	prompt := discussionKnowledgeContextPrompt(DiscussionAgentRequest{Knowledge: DiscussionAgentKnowledgeContext{Results: []DiscussionAgentKnowledgeResult{{Snippet: "raw expired contract", Evidence: &item}}}})
	if !strings.Contains(prompt, "raw expired contract") || !strings.Contains(prompt, item.InputHash) || !strings.Contains(prompt, "not verified current facts") {
		t.Fatalf("knowledge prompt=%s", prompt)
	}
}
