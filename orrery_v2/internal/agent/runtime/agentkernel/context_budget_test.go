package agentkernel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"orrery_v2/internal/agent/llm"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/providers/profilemodel"
)

type budgetMemoryModel struct {
	*modelFunc
	limit      int
	failBudget bool
}

func (m budgetMemoryModel) ContextUsage(messages []*llm.Message, tools []*llm.Tool) (profilemodel.ModelContextUsage, error) {
	if m.failBudget {
		return profilemodel.ModelContextUsage{}, errors.New("budget unavailable")
	}
	raw, _ := json.Marshal(messages)
	defs, _ := json.Marshal(tools)
	return profilemodel.ModelContextUsage{EstimatedInputTokens: agentcore.EstimateRuntimeTextTokens(string(raw)) + agentcore.EstimateRuntimeTextTokens(string(defs)), InputTokenLimit: m.limit, ContextWindowTokens: m.limit + 100, OutputReserveTokens: 100}, nil
}

func TestModelTokenWindowTriggersCompactionBeforeByteThreshold(t *testing.T) {
	var summaries, answers int
	base := &modelFunc{stream: func(_ context.Context, messages []*llm.Message, _ ...llm.Option) (*llm.MessageStream, error) {
		text := "answer"
		if strings.HasPrefix(messages[0].Content, compactionInstruction) {
			summaries++
			text = "Historical citations reviewed; continue with the latest evidence."
		} else {
			answers++
		}
		return llm.NewMessageStream([]*llm.Message{reply(text, "stop")}), nil
	}}
	r, input := openRunner(t, budgetMemoryModel{modelFunc: base, limit: 2300}, func(c *Config, req *agentcore.RuntimeRequest) {
		checkpointConfig(t, c)
		c.AutoCompact = true
		c.CompactKeepMessages = 2
		req.InputMessages = memoryHistory()
	})
	result, err := r.Run(context.Background(), input, discard)
	if err != nil || result.ResultText != "answer" || summaries < 1 || answers != 1 || r.compactions != 1 {
		t.Fatalf("token compaction failed: summaries=%d answers=%d result=%+v err=%v", summaries, answers, result, err)
	}
}

func TestChunkedCompactionCarriesMemoryAndArchivesEveryOriginalByte(t *testing.T) {
	var fragments []string
	var lastSummary string
	base := &modelFunc{stream: func(_ context.Context, messages []*llm.Message, opts ...llm.Option) (*llm.MessageStream, error) {
		if len(llm.Apply(opts...).Tools) != 0 {
			t.Fatal("summary received executable tools")
		}
		var payload struct {
			Previous string `json:"previous_working_memory"`
			Fragment string `json:"historical_fragment"`
		}
		if json.Unmarshal([]byte(messages[1].Content), &payload) != nil || !utf8.ValidString(payload.Fragment) {
			t.Fatal("invalid history fragment")
		}
		if payload.Previous != lastSummary {
			t.Fatal("earlier memory omitted from later pass")
		}
		fragments = append(fragments, payload.Fragment)
		lastSummary = fmt.Sprintf("Preserved evidence from %d fragments.", len(fragments))
		return llm.NewMessageStream([]*llm.Message{reply(lastSummary, "stop")}), nil
	}}
	r, _ := openRunner(t, base, func(c *Config, req *agentcore.RuntimeRequest) {
		checkpointConfig(t, c)
		c.AutoCompact = true
		c.CompactKeepMessages = 2
		c.CompactSummaryBytes = 256
		req.InputMessages = memoryHistory()
		for i := range req.InputMessages {
			if req.InputMessages[i].Role == "tool" {
				req.InputMessages[i].Content = strings.Repeat("证据引用 payload-α ", 80)
			}
		}
		messages, _ := initialMessages(*req)
		raw, _ := json.Marshal(messages)
		c.MaxContextBytes = len(raw) * 3 / 4
		c.CompactAtBytes = c.MaxContextBytes * 3 / 4
	})
	prefix := 0
	for r.messages[prefix].Role == llm.System {
		prefix++
	}
	expected, _ := json.Marshal(r.messages[prefix : len(r.messages)-2])
	compacted, err := r.compactContext(context.Background(), r.messages, 1, 16, discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(fragments) < 2 || strings.Join(fragments, "") != string(expected) {
		t.Fatal("segmented summary dropped or duplicated original data")
	}
	if _, err := contextBlocks(compacted); err != nil {
		t.Fatalf("live tool pair split: %v", err)
	}
	store := r.config.Checkpoints.(*SQLCheckpointStore)
	archives, err := store.store.ListRecordTable(context.Background(), compactionArchiveTable)
	if err != nil || len(archives) != 1 {
		t.Fatalf("archive not atomic: %d %v", len(archives), err)
	}
	raw, _ := json.Marshal(archives)
	if !strings.Contains(string(raw), "history-0") || !strings.Contains(string(raw), "history-5") {
		t.Fatal("original paired history missing from archive")
	}
}

func TestLaterSummaryFailureNeverCommitsPartialMemory(t *testing.T) {
	steps := 0
	base := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
		steps++
		if steps == 2 {
			return nil, errors.New("second fragment interrupted")
		}
		return llm.NewMessageStream([]*llm.Message{reply("first fragment memory", "stop")}), nil
	}}
	r, _ := openRunner(t, base, func(c *Config, req *agentcore.RuntimeRequest) {
		checkpointConfig(t, c)
		c.AutoCompact = true
		c.CompactKeepMessages = 2
		c.CompactSummaryBytes = 128
		req.InputMessages = memoryHistory()
		messages, _ := initialMessages(*req)
		raw, _ := json.Marshal(messages)
		c.MaxContextBytes = len(raw) * 3 / 4
		c.CompactAtBytes = c.MaxContextBytes / 2
	})
	before, _ := json.Marshal(r.messages)
	if _, err := r.compactContext(context.Background(), r.messages, 1, 8, discard); !errors.Is(err, ErrModel) {
		t.Fatalf("unexpected error: %v", err)
	}
	after, _ := json.Marshal(r.checkpoint.Messages)
	if steps != 2 || string(before) != string(after) || r.checkpoint.Compactions != 0 || r.checkpoint.ModelCalls != 2 {
		t.Fatal("failed compaction committed partial history or reset budget")
	}
	store := r.config.Checkpoints.(*SQLCheckpointStore)
	archives, _ := store.store.ListRecordTable(context.Background(), compactionArchiveTable)
	if len(archives) != 0 {
		t.Fatal("failed compaction created an archive")
	}
}

func TestProviderWindowRejectionCompactsOnceWithoutExecutingTools(t *testing.T) {
	for _, rejectAgain := range []bool{false, true} {
		t.Run(fmt.Sprint(rejectAgain), func(t *testing.T) {
			attempts, summaries, tools := 0, 0, 0
			base := &modelFunc{stream: func(_ context.Context, messages []*llm.Message, _ ...llm.Option) (*llm.MessageStream, error) {
				if strings.HasPrefix(messages[0].Content, compactionInstruction) {
					summaries++
					return llm.NewMessageStream([]*llm.Message{reply("Previous evidence retained.", "stop")}), nil
				}
				attempts++
				if attempts == 1 || rejectAgain {
					return nil, profilemodel.ErrContextWindowExceeded
				}
				return llm.NewMessageStream([]*llm.Message{reply("recovered answer", "stop")}), nil
			}}
			r, input := openRunner(t, base, func(c *Config, req *agentcore.RuntimeRequest) {
				checkpointConfig(t, c)
				c.AutoCompact = true
				c.CompactKeepMessages = 2
				c.CompactSummaryBytes = 256
				req.InputMessages = memoryHistory()
				c.AuthorizeTool = func(context.Context, runtimecore.RuntimeToolCallRequest) error { tools++; return nil }
			})
			result, err := r.Run(context.Background(), input, discard)
			if attempts != 2 || summaries != 1 || tools != 0 {
				t.Fatalf("unbounded recovery or replay: attempts=%d summaries=%d tools=%d err=%v", attempts, summaries, tools, err)
			}
			if rejectAgain && !errors.Is(err, profilemodel.ErrContextWindowExceeded) {
				t.Fatalf("second window rejection not propagated: %v", err)
			}
			if !rejectAgain && (err != nil || result.ResultText != "recovered answer") {
				t.Fatalf("failed recovery: %+v %v", result, err)
			}
		})
	}
}

func TestActualContextAndCacheAccountingSurviveResumeWithoutDoubleCounting(t *testing.T) {
	base := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
		message := reply("completed", "stop")
		message.ResponseMeta.Usage = &llm.TokenUsage{PromptTokens: 1000, CompletionTokens: 100, TotalTokens: 1100, PromptTokenDetails: llm.PromptTokenDetails{CachedTokens: 800}, CompletionTokensDetails: llm.CompletionTokensDetails{ReasoningTokens: 40}}
		return llm.NewMessageStream([]*llm.Message{message}), nil
	}}
	m := budgetMemoryModel{modelFunc: base, limit: 10000}
	r, input := openRunner(t, m, func(c *Config, _ *agentcore.RuntimeRequest) { checkpointConfig(t, c) })
	if _, err := r.Run(context.Background(), input, discard); err != nil {
		t.Fatal(err)
	}
	if r.checkpoint.Usage != (KernelUsage{InputTokens: 1000, OutputTokens: 100, CachedInputTokens: 800, ReasoningTokens: 40}) {
		t.Fatalf("cache counted incorrectly: %+v", r.checkpoint.Usage)
	}
	req := r.request
	req.RunID = "follow-up-run"
	req.InputMessages = append(req.InputMessages, agentcore.RuntimeChatMessage{Role: "user", Content: "continue"})
	resumed, err := resumeTestRunner(t, r, req, m)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resumed.usage, r.usage) || resumed.observation != r.observation {
		t.Fatal("resume reset actual usage baseline")
	}
	tokens, _ := resumed.activeContextTokens(resumed.messages)
	if tokens < 1100 {
		t.Fatal("optimistic estimate hid actual usage")
	}
	if _, err := resumed.Run(context.Background(), agentcore.SendCommand{Content: []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: agentcore.RuntimePromptMessagesFallbackPrompt(req.InputMessages)}}}, discard); err != nil {
		t.Fatal(err)
	}
	if resumed.checkpoint.Usage.InputTokens != 2000 || resumed.checkpoint.Usage.CachedInputTokens != 1600 {
		t.Fatal("resume lost or duplicated usage")
	}
}

func TestInvalidBudgetAndInvalidCachedUsageFailClosed(t *testing.T) {
	for _, bad := range []string{"budget", "negative-cache", "excess-cache", "excess-reasoning"} {
		t.Run(bad, func(t *testing.T) {
			base := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
				message := reply("answer", "stop")
				message.ResponseMeta.Usage = &llm.TokenUsage{PromptTokens: 10, CompletionTokens: 5}
				switch bad {
				case "negative-cache":
					message.ResponseMeta.Usage.PromptTokenDetails.CachedTokens = -1
				case "excess-cache":
					message.ResponseMeta.Usage.PromptTokenDetails.CachedTokens = 11
				case "excess-reasoning":
					message.ResponseMeta.Usage.CompletionTokensDetails.ReasoningTokens = 6
				}
				return llm.NewMessageStream([]*llm.Message{message}), nil
			}}
			r, input := openRunner(t, budgetMemoryModel{modelFunc: base, limit: 10000, failBudget: bad == "budget"}, nil)
			if _, err := r.Run(context.Background(), input, discard); err == nil {
				t.Fatal("invalid budget/usage accepted")
			}
			if bad == "budget" && base.calls.Load() != 0 {
				t.Fatal("invalid budget sent data upstream")
			}
		})
	}
}

func TestWindowRecoveryAllowanceAndUsageCannotResetOnResume(t *testing.T) {
	base := &modelFunc{stream: func(_ context.Context, messages []*llm.Message, _ ...llm.Option) (*llm.MessageStream, error) {
		if strings.HasPrefix(messages[0].Content, compactionInstruction) {
			return llm.NewMessageStream([]*llm.Message{reply("Saved history memory.", "stop")}), nil
		}
		return nil, profilemodel.ErrContextWindowExceeded
	}}
	r, input := openRunner(t, base, func(c *Config, req *agentcore.RuntimeRequest) {
		checkpointConfig(t, c)
		c.AutoCompact = true
		c.CompactKeepMessages = 2
		c.CompactSummaryBytes = 256
		req.InputMessages = memoryHistory()
	})
	if _, err := r.Run(context.Background(), input, discard); !errors.Is(err, profilemodel.ErrContextWindowExceeded) {
		t.Fatal(err)
	}
	if r.checkpoint.WindowRecoveries != 1 {
		t.Fatal("recovery allowance not persisted")
	}
	before := r.modelCalls
	resumed, err := resumeTestRunner(t, r, r.request, base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resumed.Run(context.Background(), input, discard); !errors.Is(err, profilemodel.ErrContextWindowExceeded) {
		t.Fatal(err)
	}
	if resumed.compactions != 1 || resumed.modelCalls != before+1 {
		t.Fatal("resume reset context recovery allowance")
	}
	stale := *resumed.checkpoint
	stale.WindowRecoveries = 0
	if _, err := resumed.config.Checkpoints.Save(context.Background(), stale); !errors.Is(err, ErrCheckpoint) {
		t.Fatal("checkpoint allowed recovery counter rollback")
	}
	stale = *resumed.checkpoint
	stale.Usage.InputTokens = 100
	stale.Usage.CachedInputTokens = 101
	if _, err := resumed.config.Checkpoints.Save(context.Background(), stale); !errors.Is(err, ErrCheckpoint) {
		t.Fatal("checkpoint accepted invalid cached usage")
	}
}

func TestSummaryWindowRejectionShrinksFragmentsWithoutLosingSource(t *testing.T) {
	var fragments []string
	attempts := 0
	base := &modelFunc{stream: func(_ context.Context, messages []*llm.Message, _ ...llm.Option) (*llm.MessageStream, error) {
		attempts++
		if attempts == 1 {
			return nil, profilemodel.ErrContextWindowExceeded
		}
		var payload struct {
			Fragment string `json:"historical_fragment"`
		}
		if err := json.Unmarshal([]byte(messages[1].Content), &payload); err != nil {
			t.Fatal(err)
		}
		fragments = append(fragments, payload.Fragment)
		return llm.NewMessageStream([]*llm.Message{reply("Earlier evidence preserved.", "stop")}), nil
	}}
	r, _ := openRunner(t, base, func(c *Config, req *agentcore.RuntimeRequest) {
		checkpointConfig(t, c)
		c.AutoCompact = true
		c.CompactKeepMessages = 2
		c.CompactSummaryBytes = 256
		req.InputMessages = memoryHistory()
		c.CompactAtBytes = 1024
	})
	prefix := 0
	for r.messages[prefix].Role == llm.System {
		prefix++
	}
	expected, _ := json.Marshal(r.messages[prefix : len(r.messages)-2])
	if _, err := r.compactContext(context.Background(), r.messages, 1, 8, discard); err != nil {
		t.Fatal(err)
	}
	if len(fragments) < 2 || strings.Join(fragments, "") != string(expected) || r.checkpoint.ModelCalls != attempts || attempts != len(fragments)+1 {
		t.Fatalf("fragment retry lost history or call budget: attempts=%d fragments=%d", attempts, len(fragments))
	}
}
