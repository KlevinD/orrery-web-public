package agentkernel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"orrery_v2/internal/agent/llm"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func memoryHistory() []agentcore.RuntimeChatMessage {
	messages := []agentcore.RuntimeChatMessage{{Role: "system", Content: "Only public knowledge is authorized."}, {Role: "user", Content: "Find the original source; preserve citation IDs."}}
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("history-%d", i)
		messages = append(messages, agentcore.RuntimeChatMessage{Role: "assistant", ToolCalls: []agentcore.RuntimeToolCall{{ID: id, Name: "knowledge_search", Input: map[string]any{"kb": "public"}}}}, agentcore.RuntimeChatMessage{Role: "tool", ToolCallID: id, ToolName: "knowledge_search", Content: strings.Repeat("historical evidence ", 40)})
	}
	return messages
}

func TestCompactionArchivesHistoryPreservesAnchorsAndToolPairs(t *testing.T) {
	var summaryInput []*llm.Message
	m := &modelFunc{stream: func(_ context.Context, messages []*llm.Message, opts ...llm.Option) (*llm.MessageStream, error) {
		summaryInput = messages
		if len(llm.Apply(opts...).Tools) != 0 {
			t.Fatal("compaction received executable tools")
		}
		answer := reply("Evidence examined: history-0 through history-4. Next: verify the newest citation.", "stop")
		answer.ResponseMeta.Usage = &llm.TokenUsage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120}
		return llm.NewMessageStream([]*llm.Message{answer}), nil
	}}
	r, _ := openRunner(t, m, func(c *Config, req *agentcore.RuntimeRequest) {
		checkpointConfig(t, c)
		c.AutoCompact = true
		c.CompactKeepMessages = 2
		c.CompactSummaryBytes = 512
		req.InputMessages = memoryHistory()
		messages, _ := initialMessages(*req)
		raw, _ := json.Marshal(messages)
		c.MaxContextBytes = len(raw)*2 + 4096
		c.CompactAtBytes = len(raw) - 1
	})
	var deltas, usage int
	compacted, err := r.compactContext(context.Background(), r.messages, 1, 8, func(e agentcore.SessionEvent) error {
		if e.Kind == agentcore.EventMessageDelta {
			deltas++
		}
		if e.Kind == agentcore.EventUsageDelta {
			usage++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(summaryInput) != 2 || !strings.Contains(summaryInput[1].Content, "history-0") {
		t.Fatal("compaction lost source history")
	}
	if deltas != 0 || usage != 1 {
		t.Fatalf("internal summary emitted or usage lost: deltas=%d usage=%d", deltas, usage)
	}
	if compacted[0].Role != llm.System || compacted[0].Content != r.messages[0].Content || compacted[1].Content != r.messages[1].Content {
		t.Fatal("system or original goal changed")
	}
	if _, err := contextBlocks(compacted); err != nil {
		t.Fatalf("tool pairs split: %v", err)
	}
	if compacted[len(compacted)-1].ToolCallID != "history-5" || !strings.Contains(compacted[2].Content, "Working memory") {
		t.Fatal("recent evidence or working memory lost")
	}
	store := r.config.Checkpoints.(*SQLCheckpointStore)
	archives, err := store.store.ListRecordTable(context.Background(), compactionArchiveTable)
	if err != nil || len(archives) != 1 {
		t.Fatalf("raw history not archived atomically: %d %v", len(archives), err)
	}
	raw, _ := json.Marshal(archives)
	if !strings.Contains(string(raw), "history-0") || !strings.Contains(string(raw), "history-5") || strings.Contains(string(raw), "never-send-to-model") {
		t.Fatal("archive incomplete or credential leaked")
	}
}

func TestCompactionFailurePreservesOriginalCheckpoint(t *testing.T) {
	for _, failure := range []string{"stream", "oversized", "tool-output"} {
		t.Run(failure, func(t *testing.T) {
			m := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
				if failure == "stream" {
					return nil, errors.New("unavailable")
				}
				message := reply(strings.Repeat("summary ", 200), "stop")
				if failure == "tool-output" {
					message = reply("", "tool_calls", toolCall("forbidden", `{"kb":"public"}`))
				}
				return llm.NewMessageStream([]*llm.Message{message}), nil
			}}
			r, _ := openRunner(t, m, func(c *Config, req *agentcore.RuntimeRequest) {
				checkpointConfig(t, c)
				c.AutoCompact = true
				c.CompactKeepMessages = 2
				c.CompactSummaryBytes = 64
				req.InputMessages = memoryHistory()
				messages, _ := initialMessages(*req)
				raw, _ := json.Marshal(messages)
				c.MaxContextBytes = len(raw)*2 + 4096
				c.CompactAtBytes = len(raw) - 1
			})
			before, _ := json.Marshal(r.messages)
			if _, err := r.compactContext(context.Background(), r.messages, 1, 8, discard); err == nil {
				t.Fatal("invalid summary accepted")
			}
			after, _ := json.Marshal(r.checkpoint.Messages)
			if string(before) != string(after) || r.checkpoint.Compactions != 0 {
				t.Fatal("failed summary replaced durable history")
			}
		})
	}
}

func TestLongTaskBudgetsAreExplicitAndSurviveResume(t *testing.T) {
	for _, long := range []bool{false, true} {
		t.Run(fmt.Sprint("long-", long), func(t *testing.T) {
			steps := 0
			m := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
				steps++
				if steps == 10 {
					return llm.NewMessageStream([]*llm.Message{reply("long task complete", "stop")}), nil
				}
				return llm.NewMessageStream([]*llm.Message{reply("", "tool_calls", toolCall(fmt.Sprintf("step-%d", steps), `{"kb":"public"}`))}), nil
			}}
			r, input := openRunner(t, m, func(c *Config, req *agentcore.RuntimeRequest) {
				checkpointConfig(t, c)
				if long {
					req.TaskID = "host-created-task"
					req.RuntimeOptions.ProviderSpecific = map[string]string{"agentcore_task_mode": "long"}
				}
			})
			result, err := r.Run(context.Background(), input, discard)
			if long {
				if err != nil || result.ResultText != "long task complete" || steps != 10 {
					t.Fatalf("long budget failed: %d %+v %v", steps, result, err)
				}
			} else if !errors.Is(err, ErrBudget) || steps != 8 {
				t.Fatalf("normal budget expanded: steps=%d %v", steps, err)
			}
		})
	}
	// A model failure leaves the original deadline in the durable checkpoint.
	m := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
		return nil, errors.New("interrupted")
	}}
	r, input := openRunner(t, m, func(c *Config, req *agentcore.RuntimeRequest) {
		checkpointConfig(t, c)
		c.LongTaskTimeout = time.Minute
		req.TaskID = "task"
		req.RuntimeOptions.ProviderSpecific = map[string]string{"agentcore_task_mode": "long"}
	})
	_, _ = r.Run(context.Background(), input, discard)
	deadline := r.checkpoint.Deadline
	resumed, err := resumeTestRunner(t, r, r.request, m)
	if err != nil || deadline.IsZero() || !resumed.deadline.Equal(deadline) {
		t.Fatalf("resume reset time budget: %v %v", deadline, err)
	}
}
