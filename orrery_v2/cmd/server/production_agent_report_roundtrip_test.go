package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/agentkernel"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/agent/runtime/llmprofile"
	"orrery_v2/internal/platform/db"
)

// Use the production registry and real streamed model adapter, then persist and
// recover the result before the private assistant queues the unchanged report.
// The only model is an HTTP fixture; the outbound sender panics if invoked.
func TestProductionAgentCoreStreamedReportReachesPersonalAssistantQueue(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sweeper, repo, _ := scheduledResultFixture(t)
	const body = "已核对今天的资料，发现一条需要更新的信息。请查看本次结果。"
	const receipt = "internal receipt is not the user report"
	output := "```orrery-public-summary\n" + body + "\n```\n```orrery-agent-result\n" +
		`{"version":1,"outcome":"completed","summary":"` + receipt + `","deliverables":[],"evidence":[]}` + "\n```"
	var modelCalls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls.Add(1)
		var request struct {
			Stream bool `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || !request.Stream {
			t.Errorf("expected a streamed model request: %v", err)
			http.Error(w, "invalid fixture request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for index, text := range []string{output[:24], output[24:]} {
			finish := ""
			if index == 1 {
				finish = "stop"
			}
			chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
				"index": 0, "delta": map[string]any{"role": "assistant", "content": text}, "finish_reason": finish,
			}}})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer model.Close()
	journalStore, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + filepath.Join(t.TempDir(), "journal.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer journalStore.Close()
	journal, err := agentkernel.NewSQLToolJournal(journalStore)
	if err != nil {
		t.Fatal(err)
	}
	registry := newProductionRuntimeRegistry(productionDiscussionAgentDependencies{
		KernelJournal: journal,
		ToolDispatcher: runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
			return runtimecore.RuntimeToolCallResult{}, errors.New("this fixture must not execute tools")
		}),
		LLMProfiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{
			ProfileID: "report-fixture", Provider: "openai", BaseURL: model.URL + "/v1", Model: "report-fixture", APIKey: "fixture", Enabled: true,
		}}},
	})
	defer registry.Close()
	runtime, err := registry.ResolveRuntime(ctx, agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged})
	if err != nil {
		t.Fatal(err)
	}
	options := agentcore.RuntimeOptions{WorkingDirectory: t.TempDir(), ProviderSpecific: map[string]string{
		"llm_profile_id": "report-fixture", "run_source": schedulerAgentTaskRunSource,
	}}
	response, err := runtime.RunSession(ctx, agentcore.RuntimeRequest{
		TenantID: "tenant-a", AgentID: "agent-a", RunID: "runtime-a",
		ExecutionPrincipal: agentcore.ExecutionPrincipal{Kind: agentcore.ExecutionPrincipalUser, ID: "human-a", TenantID: "tenant-a"},
		ContextPack:        agentcore.ContextPack{AgentID: "agent-a"},
		InputMessages:      []agentcore.RuntimeChatMessage{{Role: "user", Content: "Produce the fixture report."}}, RuntimeOptions: options,
	}, nil)
	if err != nil || response.Status != agentcore.RuntimeStatusCompleted {
		t.Fatalf("runtime failed: %+v %v", response, err)
	}
	input := agentcore.AgentInput{RunID: "runtime-a", ContextPack: agentcore.ContextPack{AgentID: "agent-a"},
		Task: agentcore.TaskInput{ID: "task-a", MissionID: "mission-a"}, RuntimeOptions: options}
	result, err := (missionTaskAgentResultNormalizer{}).NormalizeContext(ctx, input, response, time.Now())
	if err != nil || result.UserReport != body || result.Summary.Text != receipt {
		t.Fatalf("Agent report lost or replaced: %+v %v", result, err)
	}
	stored, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var recovered agentcore.AgentResult
	if err := json.Unmarshal(stored, &recovered); err != nil {
		t.Fatal(err)
	}
	runResult, err := scheduledRunResultFromCommittedAgentResult(runtimedaemon.AgentRun{
		RunID: "runtime-a", AgentID: "agent-a", TaskID: "task-a", MissionID: "mission-a",
	}, recovered)
	if err != nil || runResult.AgentReport == nil || runResult.AgentReport.Body != body {
		t.Fatalf("recovery lost the report: %+v %v", runResult, err)
	}
	run := scheduledAuthoredRun(sweeper)
	run.Result = runResult
	repo.runs = append(repo.runs, run)
	for retry := 0; retry < 2; retry++ {
		restarted := *sweeper
		restarted.replies = newProductionScheduledResultReplies(sweeper.jobs)
		if err := restarted.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	var queuedBody, title string
	if err := sweeper.jobs.QueryRow(ctx, "SELECT COUNT(*) FROM gateway_message_deliveries").Scan(&count); err != nil || count != 1 {
		t.Fatalf("expected exactly one queued delivery: %d %v", count, err)
	}
	if err := sweeper.jobs.QueryRow(ctx, "SELECT title,body FROM gateway_message_deliveries").Scan(&title, &queuedBody); err != nil || title != "" || queuedBody != body {
		t.Fatalf("queued report changed: %q %q %v", title, queuedBody, err)
	}
	if modelCalls.Load() != 1 {
		t.Fatalf("report was regenerated: model calls=%d", modelCalls.Load())
	}
}
