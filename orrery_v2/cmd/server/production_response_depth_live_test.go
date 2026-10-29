package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/agentkernel"
	"orrery_v2/internal/agent/runtime/llmprofile"
	"orrery_v2/internal/platform/db"
)

// Explicitly enabled evaluation: real model, isolated runtime, no tools,
// production conversation writes, or channel delivery.
func TestProductionResponseDepthLiveComparison(t *testing.T) {
	raw, directory := os.Getenv("ORRERY_AGENTCORE_LIVE_MODEL_PROFILE"), os.Getenv("ORRERY_RESPONSE_DEPTH_EVAL_DIR")
	if raw == "" || directory == "" {
		t.Skip("live response comparison not configured")
	}
	var profile llmprofile.LLMProfile
	if json.Unmarshal([]byte(raw), &profile) != nil {
		t.Fatal("invalid live profile")
	}
	store, err := db.Open(context.Background(), db.Config{Driver: db.DriverFile, URL: "file://" + filepath.Join(t.TempDir(), "journal.db")})
	if err != nil {
		t.Fatal("isolated journal unavailable")
	}
	defer store.Close()
	journal, err := agentkernel.NewSQLToolJournal(store)
	if err != nil {
		t.Fatal("isolated journal unavailable")
	}
	registry := newProductionRuntimeRegistry(productionDiscussionAgentDependencies{KernelJournal: journal, ToolDispatcher: productionRuntimeToolDispatcher{}, LLMProfiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{profile}}})
	defer registry.Close()
	runtime, err := registry.ResolveRuntime(context.Background(), agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged})
	if err != nil {
		t.Fatal("isolated runtime unavailable")
	}
	cases := []struct{ Name, Input string }{
		{"greeting", "你好"},
		{"diagnosis", "请解释这次索引任务有没有完成、依据是什么、影响和下一步。已知事实：扫描了12份资料，3份解析失败，9份解析成功；索引写入因权限拒绝而失败，新增可检索文档为0；任务调度状态为completed。只依据这些事实回答，不调用工具，不声称已修复。"},
		{"tradeoffs", "请评估知识库方案并给出建议和取舍。方案A每晚批量更新，维护成本低，但资料最多滞后24小时；方案B按资料变更即时更新，通常5分钟内可检索，但需要队列、重试和幂等处理。团队只有2名维护人员，合同生效状态需要15分钟内更新。只根据这些条件分析，不调用工具，不虚构额外数据。"},
		{"diagnosis_default", "这次索引任务完成了吗？已知扫描了12份资料，3份解析失败，9份解析成功；索引写入因权限拒绝失败，新增可检索文档为0；调度状态是completed。只依据给定事实，不调用工具。"},
		{"tradeoffs_default", "这两个知识库更新方案选哪个好？A每晚批量更新，维护成本低，最多滞后24小时；B通常5分钟内可检索，需要队列、重试和幂等。我们有2名维护人员，合同生效状态必须15分钟内更新。只依据给定条件，不调用工具。"},
	}
	var answers []map[string]string
	for index, sample := range cases {
		for _, variant := range []string{"before", "after"} {
			blocks := agentcore.DefaultRuntimePromptBlocks()
			if variant == "before" {
				var legacy []agentcore.RuntimePromptBlock
				for _, block := range blocks {
					if block.BlockID != agentcore.PromptBlockAgentWorkingInstructions && block.BlockID != agentcore.PromptBlockResponseGuidance {
						legacy = append(legacy, block)
					}
				}
				// Explicitly disable the new blocks to compare only the prompt change.
				blocks = append(legacy, agentcore.RuntimePromptBlock{BlockID: agentcore.PromptBlockAgentWorkingInstructions}, agentcore.RuntimePromptBlock{BlockID: agentcore.PromptBlockResponseGuidance})
			}
			messages := agentcore.BuildAgentPromptMessages(agentcore.AgentPromptMessagesRequest{
				Profile: agentcore.AgentProfile{ID: "response-evaluation", DisplayName: "评估助理", BaseAgent: agentcore.BaseAgentSpec{SystemPrompt: "默认使用简洁、直接的中文。验证后再宣称完成，区分事实、推断和建议。"}},
				Blocks:  blocks, InputMessages: []agentcore.RuntimeChatMessage{{Role: "user", Content: sample.Input}},
			})
			ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
			response, runErr := runtime.RunSession(ctx, agentcore.RuntimeRequest{TenantID: "response-depth-validation", AgentID: "response-evaluation", RunID: agentcore.AgentRunID(fmt.Sprintf("depth-%d-%s", index, variant)), InputMessages: messages, ContextPack: agentcore.ContextPack{AgentID: "response-evaluation"}, RuntimeOptions: agentcore.RuntimeOptions{WorkingDirectory: t.TempDir(), ProviderSpecific: map[string]string{"llm_profile_id": llmprofile.PlatformDefaultProfileID}}}, nil)
			cancel()
			if runErr != nil || response.Status != agentcore.RuntimeStatusCompleted {
				t.Fatalf("%s/%s live model execution failed", sample.Name, variant)
			}
			text := ""
			for _, message := range response.Messages {
				if message.Type == agentcore.RuntimeMessageText && strings.TrimSpace(message.Content) != "" {
					text = message.Content
				}
			}
			if text == "" {
				t.Fatal("live model returned no reply")
			}
			answers = append(answers, map[string]string{"case": sample.Name, "variant": variant, "model": profile.Model, "input": sample.Input, "answer": text})
		}
	}
	data, err := json.MarshalIndent(answers, "", "  ")
	if err != nil || os.MkdirAll(directory, 0o700) != nil || os.WriteFile(filepath.Join(directory, "model-comparison.json"), data, 0o600) != nil {
		t.Fatal("could not save model comparison")
	}
}
