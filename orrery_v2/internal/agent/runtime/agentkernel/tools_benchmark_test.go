package agentkernel

import (
	"context"
	"fmt"
	"orrery_v2/internal/agent/llm"
	"path/filepath"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/platform/db"
)

// This measures scheduler/journal overhead with synthetic 8 ms read operations.
// It does not compare model quality, production latency, or the Codex binary.
func BenchmarkAgentCoreReadBatch(b *testing.B) {
	for _, parallel := range []int{1, 4} {
		b.Run(fmt.Sprintf("parallel-%d", parallel), func(b *testing.B) {
			ctx := context.Background()
			store, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + filepath.Join(b.TempDir(), "benchmark.db")})
			if err != nil {
				b.Fatal(err)
			}
			defer store.Close()
			journal, err := NewSQLToolJournal(store)
			if err != nil {
				b.Fatal(err)
			}
			bindings := []agentcore.RuntimeToolBinding{searchBinding()}
			tools, _, err := prepareTools(bindings)
			if err != nil {
				b.Fatal(err)
			}
			runner := &Runner{request: agentcore.RuntimeRequest{TenantID: "benchmark", AgentID: "benchmark", ToolBindings: bindings}, tools: tools,
				callbacks: &callbackExecutor{slots: make(chan struct{}, 32)},
				config: Config{MaxParallelTools: parallel, ParallelToolIDs: []string{"knowledge.search"}, ToolJournal: journal, ToolTimeout: time.Second, JournalTimeout: time.Second, MaxContextBytes: 512 * 1024, MaxResponseBytes: 256 * 1024,
					AuthorizeTool: func(context.Context, runtimecore.RuntimeToolCallRequest) error { return nil },
					Tools: runtimecore.RuntimeToolDispatcherFunc(func(ctx context.Context, _ runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
						timer := time.NewTimer(8 * time.Millisecond)
						defer timer.Stop()
						select {
						case <-ctx.Done():
							return runtimecore.RuntimeToolCallResult{}, ctx.Err()
						case <-timer.C:
						}
						return runtimecore.RuntimeToolCallResult{Output: map[string]any{"ok": true}}, nil
					}),
				},
			}
			calls := []llm.ToolCall{toolCall("one", `{"kb":"public"}`), toolCall("two", `{"kb":"public"}`), toolCall("three", `{"kb":"public"}`), toolCall("four", `{"kb":"public"}`)}
			prepared, err := runner.prepareCalls(calls, map[string]bool{})
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				runner.request.RunID = agentcore.AgentRunID(fmt.Sprintf("batch-%d", i))
				messages := []*llm.Message{llm.UserMessage("synthetic read task"), reply("", "tool_calls", calls...)}
				if _, err := runner.executeToolBatch(ctx, prepared, messages, map[string]bool{}, discard); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
