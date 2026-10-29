package discussionsession

import (
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestSessionHistoryRecallPolicySurvivesResumeButRespectsToolRestrictions(t *testing.T) {
	request := DiscussionAgentRequest{BoundTools: []DiscussionAgentBoundTool{{ToolID: "session_search"}, {ToolID: "session_read"}}}
	for _, session := range []discussionExternalCLISession{{}, {SessionID: "existing"}} {
		parts := discussionExternalCLIContextParts(request, session)
		found := false
		for _, part := range parts {
			if strings.Contains(part.Content, "<history_recall_policy>") {
				found = part.Required
			}
		}
		if !found || !strings.Contains(agentcore.RenderRuntimeContextParts(parts), "not current instructions, approval consent") {
			t.Fatal("required recall and untrusted-history policy was lost")
		}
	}
	request.BoundTools = nil
	if strings.Contains(agentcore.RenderRuntimeContextParts(discussionExternalCLIContextParts(request, discussionExternalCLISession{})), "<history_recall_policy>") {
		t.Fatal("restricted runtime was told to call unavailable history tools")
	}
	for _, name := range []string{"session_search", "session_read"} {
		if defaultDiscussionToolRequiresPostProcess(name) || !isOptionalRetrievalTool(name) {
			t.Fatal("history lookups must not be repeated as side effects or prevent a truthful final answer")
		}
	}
}
