package httptransport

import (
	"testing"

	"orrery_v2/internal/app/discussionsession"
)

func TestToDiscussionRunDTOIncludesAgentName(t *testing.T) {
	got := toDiscussionRunDTO(discussionsession.RunDTO{
		RunID:          "run-1",
		ConversationID: "conv-1",
		AgentID:        "profile-f54c4401bfb058eadd0e48a5a878b4e4",
		AgentName:      "Codex",
	})

	if got.AgentName != "Codex" {
		t.Fatalf("agent name = %q, want Codex", got.AgentName)
	}
}
