package agentkernel

import (
	"context"
	"encoding/json"
	"errors"
	"orrery_v2/internal/agent/llm"
	"os"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/llmprofile"
	"orrery_v2/internal/agent/runtime/providers/profilemodel"
)

// This opt-in smoke test sends only synthetic facts. Credentials stay in the
// verifier's environment and neither real tools nor customer messages are used.
func TestAgentCoreLiveCompaction(t *testing.T) {
	for _, chunked := range []bool{false, true} {
		name := "single-pass"
		if chunked {
			name = "multiple-passes"
		}
		t.Run(name, func(t *testing.T) { runLiveCompaction(t, chunked, false) })
	}
	t.Run("interrupted-resume", func(t *testing.T) { runLiveCompaction(t, true, true) })
}

type interruptedLiveModel struct {
	llm.ChatModel
	calls int
}

func (m *interruptedLiveModel) Stream(ctx context.Context, messages []*llm.Message, opts ...llm.Option) (*llm.MessageStream, error) {
	m.calls++
	if m.calls == 2 {
		return nil, errors.New("injected interruption before second summary request")
	}
	return m.ChatModel.Stream(ctx, messages, opts...)
}

func (m *interruptedLiveModel) ModelIdentity() string {
	return m.ChatModel.(interface{ ModelIdentity() string }).ModelIdentity()
}

func (m *interruptedLiveModel) ContextUsage(messages []*llm.Message, tools []*llm.Tool) (profilemodel.ModelContextUsage, error) {
	return m.ChatModel.(profilemodel.ContextBudgetModel).ContextUsage(messages, tools)
}

func runLiveCompaction(t *testing.T, chunked, interrupted bool) {
	raw := os.Getenv("ORRERY_AGENTCORE_LIVE_MODEL_PROFILE")
	if raw == "" {
		t.Skip("live model profile is not configured")
	}
	var profile llmprofile.LLMProfile
	if json.Unmarshal([]byte(raw), &profile) != nil {
		t.Fatal("invalid live model profile")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	factory := profilemodel.NewProfileModelFactory(profilemodel.ProfileModelFactoryConfig{Profiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{profile}}})
	chat, err := factory.ModelForRequest(ctx, profilemodel.ModelRequest{LLMProfileID: profile.ProfileID, PromptCacheKey: "orrery-compaction-contract", ManagedContext: true, StreamResponses: true})
	if err != nil {
		t.Fatal("live model could not be initialized")
	}
	originalChat := chat
	if interrupted {
		chat = &interruptedLiveModel{ChatModel: chat}
	}
	r, input := openRunner(t, chat, func(c *Config, req *agentcore.RuntimeRequest) {
		checkpointConfig(t, c)
		c.AutoCompact = true
		c.CompactKeepMessages = 2
		c.CompactSummaryBytes = 2048
		req.InputMessages = memoryHistory()
		req.InputMessages[0].Content = "Use only the supplied synthetic evidence. Answer the latest question with a JSON object only, without Markdown or commentary."
		req.InputMessages[1].Content = "Identify the project code and accepted unit count in the evidence. Preserve these facts and source identifiers for the final answer."
		req.InputMessages[3].Content = "Synthetic acceptance record source:doc-alpha. The project_code is ECLIPSE-73 and the accepted units count is 7419."
		req.InputMessages = append(req.InputMessages, agentcore.RuntimeChatMessage{Role: "user", Content: "Return the accepted project_code and units from source:doc-alpha as JSON with exactly these two fields."})
		if interrupted {
			req.ToolBindings = nil
			req.TaskID = "synthetic-live-memory-task"
			req.RuntimeOptions.ProviderSpecific = map[string]string{"agentcore_task_mode": "long"}
		}
		messages, _ := initialMessages(*req)
		body, _ := json.Marshal(messages)
		c.MaxContextBytes = len(body)*2 + 4096
		if chunked {
			c.MaxContextBytes = len(body) * 3 / 4
			c.CompactSummaryBytes = 1024
		}
		c.CompactAtBytes = len(body) - 1
		if chunked {
			c.CompactAtBytes = c.MaxContextBytes * 3 / 4
		}
	})
	var compacted []*llm.Message
	var answer *llm.Message
	if interrupted {
		if _, err := r.Run(ctx, input, discard); !errors.Is(err, ErrModel) {
			t.Fatal("live interruption was not exercised")
		}
		progress := r.checkpoint.CompactionProgress
		if progress == nil || progress.Offset == 0 || progress.Offset == progress.PayloadBytes || r.checkpoint.ModelCalls != 2 {
			t.Fatal("live first fragment was not durable")
		}
		deadline := r.checkpoint.Deadline
		resumed, err := resumeTestRunner(t, r, r.request, originalChat)
		if err != nil || !resumed.deadline.Equal(deadline) || resumed.compactionProgress.Offset != progress.Offset {
			t.Fatal("live resume lost progress or deadline")
		}
		result, err := resumed.Run(ctx, input, discard)
		if err != nil || resumed.checkpoint.CompactionProgress != nil || resumed.checkpoint.Phase != CheckpointCompleted {
			t.Fatal("live continuation did not commit")
		}
		r, compacted, answer = resumed, resumed.messages, reply(result.ResultText, "stop")
	} else {
		compacted, err = r.compactContext(ctx, r.messages, 1, 8, discard)
		if err != nil {
			t.Fatal("live model compaction failed")
		}
		answer, err = r.modelStepWithTools(ctx, compacted, "live-memory-answer", discard, nil)
		if err != nil {
			t.Fatal("live model continuation failed")
		}
	}
	if r.compactions != 1 {
		t.Fatal("compaction was not exercised")
	}
	if chunked && r.modelCalls < 2 {
		t.Fatal("multiple summary passes were not exercised")
	}
	var result struct {
		ProjectCode string `json:"project_code"`
		Units       int    `json:"units"`
	}
	if answer.ResponseMeta == nil || answer.ResponseMeta.FinishReason != "stop" || json.Unmarshal([]byte(strings.TrimSpace(answer.Content)), &result) != nil || result.ProjectCode != "ECLIPSE-73" || result.Units != 7419 {
		t.Fatal("live model did not preserve synthetic evidence across compaction")
	}
	if _, err := contextBlocks(compacted); err != nil {
		t.Fatal("live compaction split tool pairs")
	}
	t.Logf("summarization_calls=%d input_tokens=%d output_tokens=%d cached_input_tokens=%d", r.modelCalls, r.usage.InputTokens, r.usage.OutputTokens, r.usage.CachedInputTokens)
}
