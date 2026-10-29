package agentkernel

import (
	"context"
	"errors"
	"orrery_v2/internal/agent/llm"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func TestRuntimeDependencyContractsTrackSchemasWithoutSecrets(t *testing.T) {
	binding := agentcore.RuntimeToolBinding{ToolID: "lookup", Version: "v1", SecretEnv: map[string]string{"API_KEY": "private credential"}, InputSchema: map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}}}
	_, first, err := prepareTools([]agentcore.RuntimeToolBinding{binding})
	if err != nil {
		t.Fatal(err)
	}
	before := dependencyToolsDigest(first)
	binding.SecretEnv["API_KEY"] = "rotated private credential"
	_, rotated, err := prepareTools([]agentcore.RuntimeToolBinding{binding})
	if err != nil || before == "" || dependencyToolsDigest(rotated) != before {
		t.Fatal("credentials changed exposed schema digest", err)
	}
	binding.InputSchema["properties"] = map[string]any{"query": map[string]any{"type": "integer"}}
	_, changed, err := prepareTools([]agentcore.RuntimeToolBinding{binding})
	if err != nil || dependencyToolsDigest(changed) == before {
		t.Fatal("effective schema change hidden", err)
	}
}

func TestRuntimeExecutionSnapshotResolvesBudgetsAndRestoredState(t *testing.T) {
	chat := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
		return nil, errors.New("interrupted")
	}}
	r, input := openRunner(t, chat, func(c *Config, req *agentcore.RuntimeRequest) {
		automaticTaskConfig(t, c, req)
		c.AutoCompact = true
		c.PromptCache = true
		c.LongTaskMaxModelCalls = 100
		c.LongTaskMaxToolCalls = 200
		req.RuntimeOptions.MaxTurns = 90
		c.ApprovalToolIDs = []string{"knowledge_search", "knowledge_search"}
	})
	s := r.RuntimeExecutionSnapshot()
	if chat.calls.Load() != 0 || s.Policy.MaxModelCalls != 90 || s.Policy.MaxToolCalls != 200 || !s.Policy.AutoCompact || !s.Policy.Checkpoints || s.ModelCalls != 0 || !s.AutomaticRecovery || s.Policy.ModelTimeoutNS != int64(45*time.Second) || len(s.Policy.ApprovalToolIDs) != 1 {
		t.Fatalf("unresolved snapshot %+v", s)
	}
	s.Policy.ApprovalToolIDs[0] = "mutated"
	if r.config.ApprovalToolIDs[0] != "knowledge_search" {
		t.Fatal("snapshot aliases engine")
	}
	if _, err := r.Run(context.Background(), input, discard); !errors.Is(err, ErrModel) {
		t.Fatal(err)
	}
	_ = r.Close(context.Background())
	next, err := reopenTaskRunner(t, r, r.request, chat)
	if err != nil {
		t.Fatal(err)
	}
	restored := next.RuntimeExecutionSnapshot()
	if !restored.Continuing || restored.ModelCalls != 1 || restored.ToolCalls != 0 || restored.BackendSessionID != s.BackendSessionID || !restored.Deadline.Equal(r.deadline) {
		t.Fatalf("restored state lost %+v", restored)
	}
}
