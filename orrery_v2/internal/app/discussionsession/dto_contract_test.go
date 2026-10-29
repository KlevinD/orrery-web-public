package discussionsession

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDiscussionSessionDTOContractIncludesRuntimeAndMissionLinkage(t *testing.T) {
	at := time.Date(2026, 6, 2, 1, 2, 3, 0, time.UTC)
	dto := ConversationDTO{
		ConversationID: "conv-1",
		Title:          "New product launch",
		Status:         "running",
		CreatedBy:      "user-1",
		CreatedAt:      at,
		UpdatedAt:      at,
		Messages: []MessageDTO{{
			MessageID:      "msg-1",
			ConversationID: "conv-1",
			Role:           "user",
			AuthorID:       "user-1",
			Content:        "Plan the launch",
			CreatedAt:      at,
		}},
		Runs: []RunDTO{{
			RunID:          "run-1",
			ConversationID: "conv-1",
			AgentID:        "coordinator-agent",
			Status:         "running",
			RuntimeKind:    "openclaw",
			Usage:          ModelUsageMap{"gpt-test": {InputTokens: 12, OutputTokens: 7, CacheReadTokens: 3, TotalTokens: 19}},
			DurationMs:     345,
			CreatedAt:      at,
		}},
		LinkedMissions: []LinkedMissionDTO{{
			MissionID: "mission-1",
			Title:     "Launch plan",
			Status:    "active",
			CreatedAt: at,
		}},
	}

	payload, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshal dto: %v", err)
	}
	for _, want := range []string{
		`"conversation_id":"conv-1"`,
		`"messages"`,
		`"runs"`,
		`"runtime_kind":"openclaw"`,
		`"usage":{"gpt-test":{"input_tokens":12,"output_tokens":7,"cache_read_tokens":3,"total_tokens":19}}`,
		`"duration_ms":345`,
		`"linked_missions"`,
	} {
		if !strings.Contains(string(payload), want) {
			t.Fatalf("payload missing %s: %s", want, payload)
		}
	}
}
