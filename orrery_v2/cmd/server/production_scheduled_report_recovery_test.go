package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/agentkernel"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/agent/runtime/llmprofile"
	"orrery_v2/internal/app/improvement"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/execution"
)

type scheduledReportJobFixture struct{ job execution.Job }

func (f scheduledReportJobFixture) Get(context.Context, string) (execution.Job, bool, error) {
	return f.job, true, nil
}

type scheduledReportObjects struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func (f *scheduledReportObjects) PutTrace(_ context.Context, key string, data []byte) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = append([]byte(nil), data...)
	return key, nil
}
func (f *scheduledReportObjects) GetReader(_ context.Context, key string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, ok := f.objects[key]
	if !ok {
		return nil, errors.New("missing fixture object")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func scheduledReportSourceFixture(t *testing.T) (*productionScheduledReportRecovery, schedulerapp.ScheduledRun, *scheduledReportObjects) {
	t.Helper()
	stage, repo, _ := scheduledReportsFixture(t)
	run := repo.runs[0]
	run.TaskID, run.MissionID = "mission-task-a", "mission-a"
	request := agentcore.RuntimeRequest{TenantID: "tenant-a", RunID: "runtime-a", AgentID: "agent-a", TaskID: run.TaskID, MissionID: run.MissionID,
		ContextPack: agentcore.ContextPack{AgentID: "agent-a"}, RuntimeOptions: agentcore.RuntimeOptions{SystemPrompt: "ORIGINAL BUSINESS INSTRUCTIONS MUST NOT BE REUSED",
			WorkingDirectory: "private-original-workspace", ResumeSessionID: "original-session", Environment: map[string]string{"BUSINESS_SECRET": "must-not-copy"},
			ProviderSpecific: map[string]string{"run_source": schedulerAgentTaskRunSource, "scheduled_task_id": run.ScheduledTaskID, "llm_profile_id": "report-fixture", "runtime_kind": "managed"}},
		ToolBindings:  []agentcore.RuntimeToolBinding{{ToolID: "dangerous-business-tool", SecretEnv: map[string]string{"secret": "must-not-copy"}}},
		SkillBindings: []agentcore.RuntimeSkillBinding{{SkillID: "business-skill"}}}
	payload, _ := json.Marshal(runtimedaemon.AgentRun{RunID: "runtime-a", AgentID: "agent-a", TaskID: run.TaskID, MissionID: run.MissionID, Request: request})
	objects := &scheduledReportObjects{objects: map[string][]byte{}}
	start, _ := json.Marshal(map[string]any{"tenant_id": "tenant-a", "run_id": "runtime-a", "agent_id": "agent-a", "task_id": run.TaskID, "mission_id": run.MissionID})
	objects.objects["agent-runs/runtime-a/trace-v2/start.json"] = start
	trace, _ := json.Marshal(scheduledReportTrace{RunID: "runtime-a", AgentID: "agent-a", TaskID: run.TaskID, MissionID: run.MissionID,
		Status: agentcore.RuntimeStatusCompleted, CompletedAt: stage.owner.now(), Messages: []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Content: "unfinished internal draft"}}})
	objects.objects["agent-runs/runtime-a/trace-v2/runtime-trace.json"] = trace
	var manifest map[string]any
	_ = json.Unmarshal(trace, &manifest)
	manifest["input"] = map[string]any{"request": map[string]any{"RuntimeOptions": map[string]any{"ProviderSpecific": map[string]string{"runtime_kind": "managed"}}}}
	objects.objects["agent-runs/runtime-a/trace-v2/runtime-trace.json"], _ = json.Marshal(manifest)
	return &productionScheduledReportRecovery{jobs: scheduledReportJobFixture{execution.Job{ID: "runtime-a", Topic: runtimedaemon.AgentRunTopic, Status: execution.StatusCompleted, Payload: payload}}, files: objects,
		traces: agentcore.StoreTraceRecorder{Store: objects}, workspaceRoot: t.TempDir()}, run, objects
}

type scheduledReportRuntimeSpy struct {
	agentcore.SessionRuntime
	request agentcore.RuntimeRequest
	calls   int
}

func (s *scheduledReportRuntimeSpy) ReleaseRuntimeWorkspace(context.Context, string) error {
	return nil
}

func (s *scheduledReportRuntimeSpy) RunSession(_ context.Context, request agentcore.RuntimeRequest, _ agentcore.SessionEventObserver) (agentcore.RuntimeResponse, error) {
	s.calls++
	s.request = request
	return agentcore.RuntimeResponse{RunID: request.RunID, Status: agentcore.RuntimeStatusCompleted, Messages: []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Status: agentcore.RuntimeMessageStatusFinal, Content: "已核对保存的执行结果，该任务的报告已补全。"}}}, nil
}

type scheduledReportRegistrySpy struct {
	agentcore.SessionRuntimeRegistry
	runtime *scheduledReportRuntimeSpy
}

type scheduledReportProfileFixture struct{ profile agentcore.AgentProfile }

func (s scheduledReportProfileFixture) GetProfile(context.Context, agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	return s.profile, nil
}

func (s scheduledReportRegistrySpy) ResolveRuntime(_ context.Context, spec agentcore.RuntimeSpec) (agentcore.SessionRuntime, error) {
	if spec.Kind != agentcore.RuntimeKindManaged {
		return nil, errors.New("unexpected native runtime")
	}
	return s.runtime, nil
}

func TestScheduledReportRecoveryUsesOriginalAgentWithNoBusinessCapabilities(t *testing.T) {
	recovery, run, objects := scheduledReportSourceFixture(t)
	spy := &scheduledReportRuntimeSpy{}
	recovery.registry = scheduledReportRegistrySpy{runtime: spy}
	author, failure := recovery.Recover(context.Background(), run, "scheduled-report-fixture")
	if failure != "" || author.Body == "" || author.RuntimeRunID != "scheduled-report-fixture" || spy.calls != 1 {
		t.Fatalf("recovery failed: %+v %s", author, failure)
	}
	request := spy.request
	if request.AgentID.String() != run.AgentID || request.TenantID != run.TenantID || request.MissionID != "" || request.TaskID != "" || request.ExecutionEnvelope != nil ||
		len(request.ToolBindings) != 0 || len(request.SkillBindings) != 0 || request.RuntimeOptions.ResumeSessionID != "" || len(request.RuntimeOptions.Environment) != 0 || len(request.RuntimeOptions.MCPConfig) != 0 ||
		request.RuntimeOptions.MaxTurns != 1 || request.RuntimeOptions.ProviderSpecific["llm_profile_id"] != "report-fixture" || request.RuntimeOptions.WorkingDirectory == "private-original-workspace" || len(request.ExecutionPrincipal.PermissionScope) != 0 {
		t.Fatalf("report run inherited business capabilities: %+v", request)
	}
	encoded, _ := json.Marshal(request)
	if strings.Contains(string(encoded), "ORIGINAL BUSINESS INSTRUCTIONS") || strings.Contains(string(encoded), "must-not-copy") {
		t.Fatal("original business prompt or credentials copied")
	}
	if len(objects.objects["agent-runs/scheduled-report-fixture/trace-v2/runtime-trace.json"]) == 0 {
		t.Fatal("supplement has no durable trace")
	}
}

func TestScheduledReportRecoveryRestoresFinalWithoutModelAndRejectsForeignSources(t *testing.T) {
	for _, mode := range []string{"restore", "tenant", "agent", "not-terminal", "native"} {
		t.Run(mode, func(t *testing.T) {
			recovery, run, objects := scheduledReportSourceFixture(t)
			spy := &scheduledReportRuntimeSpy{}
			recovery.registry = scheduledReportRegistrySpy{runtime: spy}
			switch mode {
			case "restore":
				var trace scheduledReportTrace
				_ = json.Unmarshal(objects.objects["agent-runs/runtime-a/trace-v2/runtime-trace.json"], &trace)
				trace.Messages = []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Status: agentcore.RuntimeMessageStatusFinal, Content: "原 Agent 已写好的公开报告。"}}
				objects.objects["agent-runs/runtime-a/trace-v2/runtime-trace.json"], _ = json.Marshal(trace)
			case "tenant":
				run.TenantID = "tenant-b"
			case "agent":
				run.AgentID = "agent-b"
			case "not-terminal":
				job := recovery.jobs.(scheduledReportJobFixture)
				job.job.Status = execution.StatusRunning
				recovery.jobs = job
			case "native":
				job := recovery.jobs.(scheduledReportJobFixture)
				original, _ := runtimedaemon.DecodeAgentRunPayload(job.job.Payload)
				original.Request.RuntimeOptions.ProviderSpecific["runtime_kind"] = "codex"
				job.job.Payload, _ = json.Marshal(original)
				recovery.jobs = job
			}
			author, failure := recovery.Recover(context.Background(), run, "scheduled-report-fixture")
			if spy.calls != 0 {
				t.Fatal("source check or trace restoration invoked a model")
			}
			if mode == "restore" {
				if failure != "" || author.Source != "trace_final" || author.RuntimeRunID != run.RuntimeRunID || author.Body != "原 Agent 已写好的公开报告。" {
					t.Fatalf("restoration changed authorship: %+v %s", author, failure)
				}
			} else if failure == "" || author.Body != "" {
				t.Fatalf("foreign or active source accepted: %+v", author)
			}
		})
	}
}

func TestScheduledReportRecoveryWithholdsUnsafePublicTextWithoutRewriting(t *testing.T) {
	recovery, run, objects := scheduledReportSourceFixture(t)
	spy := &scheduledReportRuntimeSpy{}
	recovery.registry = scheduledReportRegistrySpy{runtime: spy}
	var trace scheduledReportTrace
	_ = json.Unmarshal(objects.objects["agent-runs/runtime-a/trace-v2/runtime-trace.json"], &trace)
	trace.Messages = []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Status: agentcore.RuntimeMessageStatusFinal, Content: "核查结果如下。\nsource_ref: this must not be sent"}}
	objects.objects["agent-runs/runtime-a/trace-v2/runtime-trace.json"], _ = json.Marshal(trace)
	author, failure := recovery.Recover(context.Background(), run, "scheduled-report-fixture")
	if failure != "source_report_not_public" || author.Body != "" || spy.calls != 0 {
		t.Fatalf("unsafe source silently rewritten: %+v %s", author, failure)
	}
}

func TestScheduledReportRecoveryRetainsCapturedAgentInstanceProfileBinding(t *testing.T) {
	recovery, run, _ := scheduledReportSourceFixture(t)
	job := recovery.jobs.(scheduledReportJobFixture)
	original, _ := runtimedaemon.DecodeAgentRunPayload(job.job.Payload)
	original.AgentProfileID = "profile-a"
	original.Request.ExecutionEnvelope = &agentcore.ExecutionEnvelope{AgentID: run.AgentID, AgentProfileID: "profile-a", TenantID: run.TenantID, RunID: run.RuntimeRunID}
	delete(original.Request.RuntimeOptions.ProviderSpecific, "llm_profile_id")
	job.job.Payload, _ = json.Marshal(original)
	recovery.jobs = job
	binding, _ := json.Marshal(agentcore.BaseAgentLLMProfileSpec{ProfileID: "instance-configured-model"})
	profile := agentcore.AgentProfile{ID: "profile-a", Enabled: true}
	profile.BaseAgent.Extensions = map[string]json.RawMessage{agentcore.BaseAgentLLMProfileExtensionKey: binding}
	recovery.profiles = scheduledReportProfileFixture{profile}
	spy := &scheduledReportRuntimeSpy{}
	recovery.registry = scheduledReportRegistrySpy{runtime: spy}
	author, failure := recovery.Recover(context.Background(), run, "scheduled-report-bound-profile")
	if failure != "" || author.Body == "" || spy.calls != 1 || spy.request.AgentID.String() != run.AgentID || spy.request.RuntimeOptions.ProviderSpecific["llm_profile_id"] != "instance-configured-model" || spy.request.ExecutionEnvelope != nil {
		t.Fatalf("captured instance/profile binding was not preserved: %+v %s", author, failure)
	}
}

func TestScheduledReportRecoveryUsesConversationTraceAndAgentModelInheritance(t *testing.T) {
	recovery, run, objects := scheduledReportSourceFixture(t)
	job := recovery.jobs.(scheduledReportJobFixture)
	original, _ := runtimedaemon.DecodeAgentRunPayload(job.job.Payload)
	original.RuntimeID = "managed"
	original.Request.ContextPack.ConversationID = "conversation-a"
	delete(original.Request.RuntimeOptions.ProviderSpecific, "llm_profile_id")
	job.job.Payload, _ = json.Marshal(original)
	recovery.jobs = job
	for _, name := range []string{"start.json", "runtime-trace.json"} {
		from := "agent-runs/runtime-a/trace-v2/" + name
		objects.objects["discussion-conversations/conversation-a/runs/runtime-a/trace-v2/"+name] = objects.objects[from]
		delete(objects.objects, from)
	}
	// The pre-dispatch trace may not yet contain the runtime adapter's kind.
	var manifest map[string]any
	key := "discussion-conversations/conversation-a/runs/runtime-a/trace-v2/runtime-trace.json"
	_ = json.Unmarshal(objects.objects[key], &manifest)
	delete(manifest, "input")
	objects.objects[key], _ = json.Marshal(manifest)
	binding, _ := json.Marshal(agentcore.BaseAgentLLMProfileSpec{ProfileID: "agent-configured-model"})
	profile := agentcore.AgentProfile{ID: "agent-a", Enabled: true}
	profile.BaseAgent.Extensions = map[string]json.RawMessage{agentcore.BaseAgentLLMProfileExtensionKey: binding}
	recovery.profiles = scheduledReportProfileFixture{profile}
	spy := &scheduledReportRuntimeSpy{}
	recovery.registry = scheduledReportRegistrySpy{runtime: spy}
	author, failure := recovery.Recover(context.Background(), run, "scheduled-report-conversation-fixture")
	if failure != "" || author.Body == "" || spy.request.RuntimeOptions.ProviderSpecific["llm_profile_id"] != "agent-configured-model" {
		t.Fatalf("actual production namespace/model inheritance failed: %+v %s", author, failure)
	}
	profile.Enabled = false
	recovery.profiles = scheduledReportProfileFixture{profile}
	if _, failure := recovery.Recover(context.Background(), run, "scheduled-report-disabled-fixture"); failure != "report_agent_unavailable" || spy.calls != 1 {
		t.Fatalf("disabled Agent was run: %s calls=%d", failure, spy.calls)
	}
}

// Real production registry, AgentKernel, streamed model adapter and tool journal.
// Even an unsolicited model tool call cannot reach the business dispatcher.
func TestScheduledReportRecoveryProductionKernelFencesTools(t *testing.T) {
	for _, malicious := range []bool{false, true} {
		t.Run(fmt.Sprint(malicious), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			recovery, run, objects := scheduledReportSourceFixture(t)
			var calls, dispatched atomic.Int32
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if tools, ok := request["tools"].([]any); ok && len(tools) != 0 {
					t.Error("business tools exposed to reporting model")
				}
				delta := map[string]any{"role": "assistant", "content": "已检查本次执行记录，原任务尚未完成，需要恢复资料源访问。"}
				finish := "stop"
				if malicious {
					delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "call-business", "type": "function", "function": map[string]any{"name": "dangerous_business_tool", "arguments": "{}"}}}}
					finish = "tool_calls"
				}
				chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
			}))
			defer model.Close()
			journalDB, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + filepath.Join(t.TempDir(), "journal.db")})
			if err != nil {
				t.Fatal(err)
			}
			defer journalDB.Close()
			recorder := productionRuntimeEvidenceRecorder(agentcore.StoreTraceRecorder{Store: objects}, productionTraceStore{evidenceCore: journalDB})
			recovery.traces = recorder.(agentcore.DurableTraceRecorder)
			journal, err := agentkernel.NewSQLToolJournal(journalDB)
			if err != nil {
				t.Fatal(err)
			}
			registry := newProductionRuntimeRegistry(productionDiscussionAgentDependencies{KernelJournal: journal,
				ToolDispatcher: runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
					dispatched.Add(1)
					return runtimecore.RuntimeToolCallResult{}, errors.New("business dispatcher must remain unused")
				}),
				LLMProfiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{ProfileID: "report-fixture", Provider: "openai", BaseURL: model.URL + "/v1", Model: "report-fixture", APIKey: "fixture", Enabled: true}}}})
			defer registry.Close()
			recovery.registry = registry
			author, failure := recovery.Recover(ctx, run, "scheduled-report-production-fixture")
			if dispatched.Load() != 0 || calls.Load() != 1 {
				t.Fatalf("unexpected side effects: tools=%d model=%d failure=%s trace=%s", dispatched.Load(), calls.Load(), failure, author.TraceRef)
			}
			if malicious {
				if failure == "" || author.Body != "" {
					t.Fatalf("unsolicited tool call accepted: %+v", author)
				}
			} else if failure != "" || author.Body == "" {
				t.Fatalf("production report failed: %+v %s", author, failure)
			}
			captured, err := journalDB.ListRecords(ctx, "ontology_improvement_runtime_evidence", "", 10)
			if err != nil || len(captured.Items) < 2 {
				t.Fatalf("report did not use production RSI evidence recorder: count=%d err=%v", len(captured.Items), err)
			}
			requests := 0
			for _, item := range captured.Items {
				var request improvement.RuntimeRequestEvidence
				payload, _ := item.Record["payload"].(string)
				if err := json.Unmarshal([]byte(payload), &request); err != nil {
					t.Fatal(err)
				}
				if request.Phase != "prepared_for_dispatch" {
					continue
				}
				requests++
				if request.RunID != "scheduled-report-production-fixture" || request.AgentID != run.AgentID || len(request.Bindings) != 0 {
					t.Fatalf("wrong report evidence: %+v", request)
				}
				terminal, err := improvement.NewRuntimeEvidenceStore(journalDB).GetTerminal(ctx, run.TenantID, request.AttemptID)
				if err != nil || terminal.TraceFinalization != "succeeded" {
					t.Fatalf("report terminal evidence missing: %+v %v", terminal, err)
				}
			}
			if requests != 1 {
				t.Fatalf("expected one request plus its terminal evidence, got %d", requests)
			}
		})
	}
}
