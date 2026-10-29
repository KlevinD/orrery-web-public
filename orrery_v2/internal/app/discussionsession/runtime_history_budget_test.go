package discussionsession

import (
	"fmt"
	"strings"
	"testing"
)

func TestPromptHistoryBoundsPassiveNoticesWithoutLosingConversation(t *testing.T) {
	request := DiscussionAgentRequest{History: []MessageDTO{{Role: "user", Content: "Please compare the designs."}, {Role: "assistant", Content: "Here is the earlier analysis."}}}
	for index := 0; index < 30; index++ {
		request.History = append(request.History, MessageDTO{Role: "assistant", Content: fmt.Sprintf("Notice %d", index), Metadata: map[string]string{"kind": "scheduled_task_result"}})
	}
	request.History = append(request.History, MessageDTO{Role: "user", Content: "Explain the tradeoffs."})
	got := discussionPromptHistory(request)
	if len(got) != 6 || got[0].Content != request.History[0].Content || got[1].Content != request.History[1].Content || got[2].Content != "Notice 27" || got[5].Content != "Explain the tradeoffs." {
		t.Fatalf("conversation displaced by background notices: %+v", got)
	}
	if len(request.History) != 33 {
		t.Fatal("durable source history mutated")
	}
}

func TestPromptHistoryBoundsLegacySystemNoticesAndKeepsUserText(t *testing.T) {
	request := DiscussionAgentRequest{History: []MessageDTO{{Role: "user", Content: "User request", Metadata: map[string]string{"delivery": "passive_notice"}}}}
	for index := 0; index < 9; index++ {
		request.History = append(request.History, MessageDTO{Role: "system", Content: fmt.Sprintf("Legacy notice %d", index)})
	}
	request.History = append(request.History, MessageDTO{Role: "system", Content: strings.Repeat("oversized notice ", 1000)})
	got := discussionPromptHistory(request)
	if len(got) != 4 || got[0].Content != "User request" || got[1].Content != "Legacy notice 6" {
		t.Fatalf("legacy notices displaced conversation: %+v", got)
	}
}
