package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentassembly "orrery_v2/internal/agent/assembly"
	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/agentkernel"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/agent/runtime/llmprofile"
	"orrery_v2/internal/app/developmentproject"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/execution"
	"orrery_v2/internal/platform/testkit"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestQueuedAgentCoreWorkerRetriesOnlySafeTaskFailures(t *testing.T) {
	for _, kind := range []agentcore.RuntimeKind{agentcore.RuntimeKindManaged, agentcore.RuntimeKindPi} {
		for _, last := range []bool{false, true} {
			t.Run(string(kind)+"/last="+map[bool]string{true: "yes", false: "no"}[last], func(t *testing.T) {
				ctx := context.Background()
				env := testkit.New(t)
				defer env.Close()
				profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
				profile := workerTestProfile(t, "queued-retry-agent")
				profile.RuntimeSpec = agentcore.RuntimeSpec{Kind: kind}
				profile.WorkspaceRoot = t.TempDir()
				saveAvailableAgentProfile(t, ctx, profiles, profile)
				runtime := &capturingWorkerRuntime{response: agentcore.RuntimeResponse{Status: agentcore.RuntimeStatusFailed, Error: agentcore.RuntimeFailure{Message: "temporary model failure"}, ProviderMetadata: map[string]string{"session_id": "agentcore-task-scoped", "task_resume_ready": "true"}}}
				lifecycle := &capturingAgentRunLifecycle{}
				development := &capturingDevelopmentAgentRuns{managed: true, prepared: developmentproject.PreparedAgentRun{RootDir: profile.WorkspaceRoot, WorkDir: profile.WorkspaceRoot}}
				worker := productionAgentRunWorker{assembler: &runtimeCapturingWorkerAssembler{runtime: runtime}, profiles: profiles, lifecycle: lifecycle, development: development}
				run := workerTestRun(profile, "queued-retry-run", "mission-retry", "task-retry")
				run.RuntimeID = string(kind)
				run.Request.RuntimeOptions.Model = "primary"
				run.Request.RuntimeOptions.ProviderSpecific = map[string]string{
					"run_source":   schedulerAgentTaskRunSource,
					"source_model": "openai/primary", "fallback_models": "openai/backup",
				}
				payload, _ := json.Marshal(run)
				delivery := platform.ExecutionDelivery{Topic: runtimedaemon.AgentRunTopic, Payload: payload, MaxAttempts: 3}
				if last {
					delivery.Attempts = 2
				}
				err := worker.HandleExecution(ctx, delivery)
				retry := kind == agentcore.RuntimeKindManaged && !last
				var delayed execution.RetryAfterError
				if errors.As(err, &delayed) != retry {
					t.Fatalf("retry=%v err=%v", retry, err)
				}
				if retry && (delayed.Delay != 2*time.Second || len(lifecycle.failures) != 0 || development.failCalls != 0) {
					t.Fatal("retry prematurely finalized task or released workspace")
				}
				if !retry && (err != nil || len(lifecycle.failures) != 1 || development.failCalls != 1) {
					t.Fatalf("terminal/native runtime behavior changed: %v", err)
				}
				options := runtime.request.RuntimeOptions.ProviderSpecific
				if (options["agentcore_task_recovery"] == "automatic") != (kind == agentcore.RuntimeKindManaged) {
					t.Fatal("task policy applied to wrong runtime")
				}
				wantModel := "primary"
				if kind == agentcore.RuntimeKindPi && last {
					wantModel = "backup"
				}
				if runtime.request.RuntimeOptions.Model != wantModel {
					t.Fatal("AgentCore recovery switched model or native fallback stopped working")
				}
			})
		}
	}
}

func TestQueuedAgentCorePolicyRequiresTaskAndTrustedRecoveryMetadata(t *testing.T) {
	for _, missing := range []string{"task", "mission", "run"} {
		r := runtimedaemon.AgentRun{RunID: "run", TaskID: "task", MissionID: "mission"}
		switch missing {
		case "task":
			r.TaskID = ""
		case "mission":
			r.MissionID = ""
		case "run":
			r.RunID = ""
		}
		configureQueuedAgentCoreTask(&r, agentcore.RuntimeKindManaged)
		if r.Request.RuntimeOptions.ProviderSpecific["agentcore_task_recovery"] != "" {
			t.Fatal("chat acquired task recovery")
		}
	}
	r := runtimedaemon.AgentRun{RunID: "run", TaskID: "task", MissionID: "mission"}
	configureQueuedAgentCoreTask(&r, agentcore.RuntimeKindManaged)
	for _, status := range []agentcore.RuntimeStatus{agentcore.RuntimeStatusBlocked, agentcore.RuntimeStatusCancelled, agentcore.RuntimeStatusFailed} {
		err := agentcore.RuntimePipelineError{Status: status, ProviderMetadata: map[string]string{"session_id": "agentcore-task-id"}}
		if queuedAgentCoreTaskCanRetry(r, agentcore.RuntimeKindManaged, err) {
			t.Fatal("missing safe recovery proof accepted")
		}
		err.ProviderMetadata["task_resume_ready"] = "true"
		if queuedAgentCoreTaskCanRetry(r, agentcore.RuntimeKindManaged, err) != (status == agentcore.RuntimeStatusFailed) {
			t.Fatal("blocked or cancelled task retried")
		}
	}
	if !queuedAgentCoreTaskMustStop(r, agentcore.RuntimeKindManaged, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "unsafe checkpoint", false)) {
		t.Fatal("unsafe checkpoint open would retry")
	}
}

type queuedRecoveryAssembler struct {
	runtime agentcore.SessionRuntime
	commits *countingWorkerResultCommitter
}

func (a queuedRecoveryAssembler) Build(_ context.Context, p agentcore.AgentProfile, o agentassembly.BuildOptions) (agentcore.AgentInstance, error) {
	return agentcore.NewBaseAgent(agentcore.BaseAgentConfig{
		Identity: agentcore.AgentIdentity{AgentID: o.AgentID, AgentProfileID: p.ID, InstanceRef: agentcore.AgentInstanceRef{ID: o.InstanceID}}, Profile: p, Runtime: a.runtime, Delivery: a.commits,
		Bindings: agentcore.AgentBindings{Tools: []agentcore.ToolBinding{{ToolID: toolstd.KnowledgeSearch, Alias: "knowledge_search", GrantedPermissions: []string{toolstd.PermissionKnowledgeRead}, InputSchema: map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}}}}},
		Trace:    agentcore.NoopTraceRecorder{}, Notifier: agentcore.NoopNotifier{}, Validator: agentcore.ResultValidatorFunc(func(agentcore.AgentInput, agentcore.AgentResult) error { return nil }),
	}), nil
}

// Exercise the real production worker, canonical runtime and persistent queue.
// Only the remote inference service and business result delivery are fixtures.
func TestQueuedAgentCoreExecutionQueueRecoversAcrossRuntimeRestart(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	var requests, tools atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		step := requests.Add(1)
		var payload map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&payload) != nil {
			t.Error("invalid model payload")
		}
		if step == 2 {
			http.Error(w, "temporary upstream failure", http.StatusServiceUnavailable)
			return
		}
		message := map[string]any{"role": "assistant", "content": "completed from saved evidence"}
		finish := "stop"
		if step == 1 {
			message["content"] = ""
			message["tool_calls"] = []any{map[string]any{"id": "durable-public-search", "type": "function", "function": map[string]any{"name": "knowledge_search", "arguments": `{"query":"public"}`}}}
			finish = "tool_calls"
		}
		if step == 3 && !strings.Contains(string(payload["messages"]), "saved-public-source") {
			t.Error("retry omitted completed tool result")
		}
		if step > 3 {
			t.Error("completed task inferred again")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message, "finish_reason": finish}}})
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "runtime.db")
	store, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	newRuntime := func() agentcore.SessionRuntime {
		journal, _ := agentkernel.NewSQLToolJournal(store)
		checkpoints, _ := agentkernel.NewSQLCheckpointStore(store)
		registry := newProductionRuntimeRegistry(productionDiscussionAgentDependencies{KernelJournal: journal, KernelCheckpoints: checkpoints,
			LLMProfiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{ProfileID: "test", Model: "test-model", Provider: "openai", BaseURL: server.URL + "/v1", APIKey: "test", Enabled: true, GlobalDefault: true}}},
			ToolDispatcher: runtimecore.RuntimeToolDispatcherFunc(func(_ context.Context, r runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
				tools.Add(1)
				if r.TenantID != "tenant-1" || r.Input["query"] != "public" {
					t.Error("tool authorization scope lost")
				}
				return runtimecore.RuntimeToolCallResult{Output: map[string]any{"source": "saved-public-source"}}, nil
			}),
		})
		t.Cleanup(func() { _ = registry.Close() })
		runtime, err := registry.ResolveRuntime(ctx, agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged})
		if err != nil {
			t.Fatal(err)
		}
		return runtime
	}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	profile := workerTestProfile(t, "queued-real-agent")
	profile.RuntimeSpec = agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged}
	profile.WorkspaceRoot = t.TempDir()
	saveAvailableAgentProfile(t, ctx, profiles, profile)
	commits := &countingWorkerResultCommitter{}
	life := &capturingAgentRunLifecycle{}
	worker := productionAgentRunWorker{assembler: queuedRecoveryAssembler{runtime: newRuntime(), commits: commits}, profiles: profiles, lifecycle: life}
	run := workerTestRun(profile, "queued-real-run", "queued-mission", "queued-task")
	run.RuntimeID = string(agentcore.RuntimeKindManaged)
	job, err := runtimedaemon.ExecutionJobFromAgentRun(run)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := execution.NewQueue(ctx, env.DB, execution.QueueConfig{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := queue.Enqueue(ctx, job, now); err != nil {
		t.Fatal(err)
	}
	runner := execution.NewQueueRunner(queue, "worker-a", func(ctx context.Context, j execution.Job) error {
		return worker.HandleExecution(ctx, platform.ExecutionDelivery{Topic: j.Topic, Payload: j.Payload, Attempts: j.Attempts, MaxAttempts: j.MaxAttempts})
	}, func() time.Time { return now })
	if processed, err := runner.RunOnce(ctx); !processed || err == nil {
		t.Fatalf("first failure not requeued: %v", err)
	}
	state, _, _ := queue.Get(ctx, job.ID)
	if state.Status != execution.StatusQueued || state.Attempts != 1 || tools.Load() != 1 || requests.Load() != 2 || commits.calls != 0 || len(life.failures) != 0 {
		t.Fatalf("first attempt finalized task: job=%+v requests=%d tools=%d", state, requests.Load(), tools.Load())
	}
	if state.NextAttemptAt.Before(now.Add(2 * time.Second)) {
		t.Fatal("retry delay missing")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + path})
	if err != nil {
		t.Fatal(err)
	}
	worker.assembler = queuedRecoveryAssembler{runtime: newRuntime(), commits: commits}
	now = now.Add(3 * time.Second)
	if processed, err := runner.RunOnce(ctx); !processed || err != nil {
		t.Fatalf("second delivery failed: %v", err)
	}
	state, _, _ = queue.Get(ctx, job.ID)
	if state.Status != execution.StatusCompleted || tools.Load() != 1 || requests.Load() != 3 || commits.calls != 1 || len(life.failures) != 0 {
		t.Fatalf("recovered job replayed or lost work: %+v tools=%d requests=%d commits=%d", state, tools.Load(), requests.Load(), commits.calls)
	}
	if err := worker.HandleExecution(ctx, platform.ExecutionDelivery{Topic: job.Topic, Payload: job.Payload, Attempts: 2, MaxAttempts: 3}); err != nil {
		t.Fatalf("completed redelivery failed: %v", err)
	}
	if requests.Load() != 3 || tools.Load() != 1 {
		t.Fatal("completed checkpoint performed new inference/tool execution")
	}
}
