package discussionsession

import (
	"encoding/json"
	"testing"
)

func TestMapMessagesFiltersPersistedAssistantReasoning(t *testing.T) {
	records := []MessageRecord{{
		MessageID: "assistant-message",
		Role:      "assistant",
		Content:   "<think>private reasoning</think>Visible answer",
		Metadata: map[string]string{
			discussionDisplayTimelineMetadataKey: `[{"kind":"text","text":"<think>private timeline</think>Visible timeline"}]`,
		},
	}, {
		MessageID: "user-message",
		Role:      "user",
		Content:   "Show me how <think> tags work",
	}}

	messages := mapMessages(records)
	if got := messages[0].Content; got != "Visible answer" {
		t.Fatalf("assistant content = %q, want visible answer", got)
	}
	if got := messages[1].Content; got != records[1].Content {
		t.Fatalf("user content = %q, want original content", got)
	}

	var timeline []DiscussionDisplayTimelineSegment
	if err := json.Unmarshal([]byte(messages[0].Metadata[discussionDisplayTimelineMetadataKey]), &timeline); err != nil {
		t.Fatal(err)
	}
	if len(timeline) != 1 || timeline[0].Text != "Visible timeline" {
		t.Fatalf("assistant timeline = %+v, want visible timeline", timeline)
	}
}

func TestMapMessagesDropsMalformedPersistedAssistantTimeline(t *testing.T) {
	messages := mapMessages([]MessageRecord{{
		Role:    "assistant",
		Content: "Visible answer",
		Metadata: map[string]string{
			discussionDisplayTimelineMetadataKey: `<think>private malformed timeline`,
		},
	}})
	if _, exists := messages[0].Metadata[discussionDisplayTimelineMetadataKey]; exists {
		t.Fatalf("malformed assistant timeline must not cross the public output boundary: %+v", messages[0].Metadata)
	}
}

func TestMapConversationSummaryFiltersPersistedAssistantReasoning(t *testing.T) {
	summary := mapConversationSummaryDTO(ConversationRecordView{
		Messages: []MessageRecord{{Role: "assistant", Content: "<think>private summary</think>Visible summary"}},
	})
	if summary.LastMessage != "Visible summary" {
		t.Fatalf("last message = %q, want visible summary", summary.LastMessage)
	}
}

func TestMapMessagesSanitizesHistoricalAgentResultNotice(t *testing.T) {
	messages := mapMessages([]MessageRecord{{
		Role:     "system",
		Content:  "<think>private report assembly</think>\n```orrery-agent-result\n{\"job_id\":\"secret\"}\n```",
		Metadata: map[string]string{"kind": "agent_result"},
	}})
	if got := messages[0].Content; got != "任务结果已记录到 Orrery。" {
		t.Fatalf("historical agent result notice = %q", got)
	}
}

func TestMapMessagesHidesPersistedPartialExecutionDiary(t *testing.T) {
	messages := mapMessages([]MessageRecord{{
		Role:    "assistant",
		Content: "我先读取当前发布清单，然后检查工作区和数据库状态。",
		Metadata: map[string]string{
			discussionPartialMessageMetadataKey:  "true",
			discussionDisplayTimelineMetadataKey: `[ {"kind":"text","text":"读取工具输出并整理上下文"} ]`,
		},
	}})
	if len(messages) != 1 {
		t.Fatalf("messages = %+v", messages)
	}
	if messages[0].Content != discussionPartialMessageFallback {
		t.Fatalf("partial content = %q, want %q", messages[0].Content, discussionPartialMessageFallback)
	}
	if _, ok := messages[0].Metadata[discussionDisplayTimelineMetadataKey]; ok {
		t.Fatalf("partial execution timeline must not cross the public boundary: %+v", messages[0].Metadata)
	}
	if messages[0].Metadata[discussionPartialMessageMetadataKey] != "true" {
		t.Fatalf("partial marker should remain available to the client: %+v", messages[0].Metadata)
	}
}

func TestPublicRunDebugSanitizesHistoricalMachineOutput(t *testing.T) {
	debug := publicRunDebug(RunDebugDTO{
		OutboundPrompt:  "role: system\ntool_call_id=secret",
		RuntimeResponse: "```orrery-agent-result\n{\"run_id\":\"secret\"}\n```",
	})
	if debug.OutboundPrompt != "任务请求已记录到 Orrery。" || debug.RuntimeResponse != "运行结果已记录到 Orrery。" {
		t.Fatalf("public debug = %+v", debug)
	}
}

func TestMapTurnsAndEventsSanitizeHistoricalMachineText(t *testing.T) {
	turns := mapTurns([]TurnRecord{{ErrorMessage: "<think>private</think>\ndocument_id=secret"}})
	if len(turns) != 1 || turns[0].ErrorMessage != "任务执行失败，详细原因已记录到 Orrery。" {
		t.Fatalf("public turns = %+v", turns)
	}
	events := mapEvents([]EventRecord{{Summary: "```orrery-agent-result\n{\"job_id\":\"secret\"}\n```"}})
	if len(events) != 1 || events[0].Summary != "事件已记录到 Orrery。" {
		t.Fatalf("public events = %+v", events)
	}
}
