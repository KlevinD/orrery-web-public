package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	agentassembly "orrery_v2/internal/agent/assembly"
	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	runtimesession "orrery_v2/internal/agent/runtime/session"
	"orrery_v2/internal/app/developmentproject"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/testkit"
)

func TestProductionAgentRunWorkerRejectsVersionedOccurrenceTenantMismatch(t *testing.T) {
	snapshotTask := schedulerapp.ScheduledTask{
		TenantID: "tenant-snapshot", ScheduledTaskID: "scheduled-task-tenant", Title: "Snapshot title",
		Target: schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask, AgentID: "agent-tenant", TaskGoal: "Run tenant task"},
	}
	base := scheduledRunConfigurationSourceStub{
		task: schedulerapp.ScheduledTask{
			TenantID: "tenant-snapshot", ScheduledTaskID: "scheduled-task-tenant", Title: "Task title",
			Target: schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask},
		},
		run: schedulerapp.ScheduledRun{
			ScheduledRunID: "scheduled-run-tenant", ScheduledTaskID: "scheduled-task-tenant", TargetMode: schedulerapp.TargetModeAgentTask,
			ConfigurationSnapshotVersion: 1, ConfigurationSnapshot: &snapshotTask, TenantID: "tenant-snapshot", ScheduledTaskTitle: "Snapshot title", AgentID: "agent-tenant",
		},
	}
	tests := []struct {
		name   string
		mutate func(*scheduledRunConfigurationSourceStub, *runtimedaemon.AgentRun)
	}{
		{name: "missing request tenant", mutate: func(_ *scheduledRunConfigurationSourceStub, run *runtimedaemon.AgentRun) {
			run.Request.TenantID = ""
		}},
		{name: "task tenant differs", mutate: func(source *scheduledRunConfigurationSourceStub, _ *runtimedaemon.AgentRun) {
			source.task.TenantID = "tenant-other"
		}},
		{name: "occurrence tenant differs", mutate: func(source *scheduledRunConfigurationSourceStub, _ *runtimedaemon.AgentRun) {
			source.run.TenantID = "tenant-other"
		}},
		{name: "request tenant differs", mutate: func(_ *scheduledRunConfigurationSourceStub, run *runtimedaemon.AgentRun) {
			run.Request.TenantID = "tenant-other"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := base
			run := runtimedaemon.AgentRun{Request: agentcore.RuntimeRequest{
				TenantID: "tenant-snapshot", RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{
					"scheduled_task_id": "scheduled-task-tenant", "scheduled_run_id": "scheduled-run-tenant",
				}},
			}}
			test.mutate(&source, &run)
			worker := productionAgentRunWorker{schedulerTasks: source}
			if err := worker.loadScheduledResultContract(context.Background(), &run); err == nil || !strings.Contains(err.Error(), "tenant") {
				t.Fatalf("tenant mismatch error = %v", err)
			}
		})
	}
}

func TestProductionAgentRunWorkerRejectsVersionedOccurrenceAgentMismatch(t *testing.T) {
	snapshotTask := schedulerapp.ScheduledTask{
		TenantID: "tenant-agent", ScheduledTaskID: "scheduled-task-agent", Title: "Snapshot title",
		Target: schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask, AgentID: "agent-snapshot", TaskGoal: "Run Agent task"},
	}
	source := scheduledRunConfigurationSourceStub{
		task: snapshotTask,
		run: schedulerapp.ScheduledRun{
			ScheduledRunID: "scheduled-run-agent", ScheduledTaskID: snapshotTask.ScheduledTaskID, TargetMode: schedulerapp.TargetModeAgentTask,
			ConfigurationSnapshotVersion: 1, ConfigurationSnapshot: &snapshotTask, TenantID: snapshotTask.TenantID,
			ScheduledTaskTitle: snapshotTask.Title, AgentID: snapshotTask.Target.AgentID,
		},
	}
	run := runtimedaemon.AgentRun{
		AgentID: "agent-other",
		Request: agentcore.RuntimeRequest{
			TenantID: snapshotTask.TenantID,
			RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{
				"scheduled_task_id": snapshotTask.ScheduledTaskID, "scheduled_run_id": source.run.ScheduledRunID,
			}},
		},
	}
	worker := productionAgentRunWorker{schedulerTasks: source}
	if err := worker.loadScheduledResultContract(context.Background(), &run); err == nil || !strings.Contains(err.Error(), "Agent") {
		t.Fatalf("Agent mismatch error = %v", err)
	}
}

func TestProductionAgentRunWorkerRejectsIncompleteVersionedOccurrenceSnapshot(t *testing.T) {
	source := scheduledRunConfigurationSourceStub{
		task: schedulerapp.ScheduledTask{
			TenantID: "tenant-title", ScheduledTaskID: "scheduled-task-title", Title: "Mutable task title",
			Target: schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask},
		},
		run: schedulerapp.ScheduledRun{
			ScheduledRunID: "scheduled-run-title", ScheduledTaskID: "scheduled-task-title", TargetMode: schedulerapp.TargetModeAgentTask,
			ConfigurationSnapshotVersion: 1, TenantID: "tenant-title", ScheduledTaskTitle: "",
		},
	}
	run := runtimedaemon.AgentRun{Request: agentcore.RuntimeRequest{
		TenantID: "tenant-title", RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{
			"scheduled_task_id": "scheduled-task-title", "scheduled_run_id": "scheduled-run-title",
			"scheduled_task_name": "Stale mutable title",
		}},
	}}
	worker := productionAgentRunWorker{schedulerTasks: source}
	if err := worker.loadScheduledResultContract(context.Background(), &run); err == nil || !strings.Contains(err.Error(), "snapshot") {
		t.Fatalf("incomplete occurrence snapshot error = %v", err)
	}
}

func TestApplyScheduledFallbackModelUsesFirstSameProviderFallbackOnSecondAttempt(t *testing.T) {
	run := runtimedaemon.AgentRun{Request: agentcore.RuntimeRequest{RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{
		"source_model":    "openai-codex-api/gpt-5.5",
		"fallback_models": "openai-codex-api/gpt-5.6-luna",
	}}}}

	if !applyScheduledFallbackModel(&run, 2) {
		t.Fatal("second attempt should select the first same-provider fallback")
	}
	if run.Request.RuntimeOptions.Model != "gpt-5.6-luna" || run.Request.RuntimeOptions.ProviderSpecific["llm_model_override"] != "gpt-5.6-luna" {
		t.Fatalf("runtime options = %+v", run.Request.RuntimeOptions)
	}
	if run.Request.RuntimeOptions.ProviderSpecific["active_source_model"] != "openai-codex-api/gpt-5.6-luna" {
		t.Fatalf("provider metadata = %+v", run.Request.RuntimeOptions.ProviderSpecific)
	}
}

func TestApplyScheduledFallbackModelDoesNotReuseCredentialsAcrossProviders(t *testing.T) {
	run := runtimedaemon.AgentRun{Request: agentcore.RuntimeRequest{RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{
		"source_model":    "openai-codex-api/gpt-5.5",
		"fallback_models": "minimax/MiniMax-M3",
	}}}}

	if applyScheduledFallbackModel(&run, 2) {
		t.Fatal("cross-provider fallback requires a separately configured LLM profile")
	}
	if run.Request.RuntimeOptions.Model != "" || run.Request.RuntimeOptions.ProviderSpecific["llm_model_override"] != "" {
		t.Fatalf("runtime options were mutated: %+v", run.Request.RuntimeOptions)
	}
}

func TestProductionAgentRunWorkerRejectsNativeRuntimeForOtherScheduledTasks(t *testing.T) {
	worker := productionAgentRunWorker{schedulerTasks: scheduledTaskContractSourceFunc(func(_ context.Context, id string) (schedulerapp.ScheduledTask, error) {
		return schedulerapp.ScheduledTask{ScheduledTaskID: id, CreatedBy: "operator"}, nil
	})}
	run := runtimedaemon.AgentRun{Request: agentcore.RuntimeRequest{RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{
		"scheduled_task_id":                           "scheduled-task-other",
		schedulerRuntimeConfigRefProviderKey:          nativeScheduledRuntimeProfileID,
		runtimecore.ToolCallTimeoutSecondsProviderKey: "1800",
	}}}}

	if err := worker.loadScheduledResultContract(context.Background(), &run); err != nil {
		t.Fatal(err)
	}
	if value := run.Request.RuntimeOptions.ProviderSpecific[schedulerRuntimeConfigRefProviderKey]; value != "" {
		t.Fatalf("untrusted scheduler runtime config ref was retained: %q", value)
	}
	if value := run.Request.RuntimeOptions.ProviderSpecific[runtimecore.ToolCallTimeoutSecondsProviderKey]; value != "" {
		t.Fatalf("untrusted runtime tool timeout was retained: %q", value)
	}
}

type scheduledTaskContractSourceFunc func(context.Context, string) (schedulerapp.ScheduledTask, error)

func (f scheduledTaskContractSourceFunc) GetTask(ctx context.Context, id string) (schedulerapp.ScheduledTask, error) {
	return f(ctx, id)
}

type scheduledRunConfigurationSourceStub struct {
	task schedulerapp.ScheduledTask
	run  schedulerapp.ScheduledRun
}

func (s scheduledRunConfigurationSourceStub) GetTask(context.Context, string) (schedulerapp.ScheduledTask, error) {
	return s.task, nil
}

func (s scheduledRunConfigurationSourceStub) GetRun(context.Context, string) (schedulerapp.ScheduledRun, error) {
	return s.run, nil
}

func mustEncodeSchedulerResultContract(t *testing.T, contract schedulerapp.AgentResultContract) string {
	t.Helper()
	encoded, err := encodeSchedulerResultContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestPrepareProductionAgentRunWorkspaceCreatesCanonicalPerRunPolicy(t *testing.T) {
	root := filepath.Join(t.TempDir(), "execenv")
	run := runtimedaemon.AgentRun{
		RunID:     "run-workspace",
		MissionID: "mission-workspace",
		TaskID:    "task-workspace",
	}
	cfg := loadTestConfig(t, map[string]string{
		"paths.base_dir":       t.TempDir(),
		"runtime.execenv.root": root,
	})

	if err := prepareProductionAgentRunWorkspace(&run, cfg, ""); err != nil {
		t.Fatalf("prepareProductionAgentRunWorkspace: %v", err)
	}
	options := run.Request.RuntimeOptions
	policy := options.WorkspacePolicy
	if options.WorkingDirectory == "" || options.WorkingDirectory != policy.WorkDir || !filepath.IsAbs(policy.WorkDir) {
		t.Fatalf("runtime workspace = %+v", options)
	}
	if policy.Root != filepath.Clean(root) || policy.SandboxMode != "workspace-write" || policy.ApprovalPolicy != "on-request" || !policy.EnforceBoundary {
		t.Fatalf("runtime workspace policy = %+v", policy)
	}
	if !reflect.DeepEqual(options.SandboxProfile, policy.Profile) || options.SandboxProfile.WorkDir != policy.WorkDir {
		t.Fatalf("sandbox profile does not match workspace policy: profile=%+v policy=%+v", options.SandboxProfile, policy)
	}
	if info, err := os.Stat(policy.WorkDir); err != nil || !info.IsDir() {
		t.Fatalf("runtime workspace directory was not created: info=%v err=%v", info, err)
	}
}

func TestPrepareProductionAgentRunWorkspacePreservesAuthorizedWorkspace(t *testing.T) {
	existing := filepath.Join(t.TempDir(), "authorized")
	policy := agentcore.RuntimeWorkspacePolicy{
		Root: existing, WorkDir: existing, WritableRoots: []string{existing},
		SandboxMode: "read-only", ApprovalPolicy: "never", EnforceBoundary: true,
	}
	run := runtimedaemon.AgentRun{RunID: "run-existing", Request: agentcore.RuntimeRequest{RuntimeOptions: agentcore.RuntimeOptions{
		WorkingDirectory: existing,
		WorkspacePolicy:  policy,
		SandboxProfile:   agentcore.SandboxProfileFromWorkspacePolicy(policy),
	}}}

	if err := prepareProductionAgentRunWorkspace(&run, loadTestConfig(t), ""); err != nil {
		t.Fatalf("prepareProductionAgentRunWorkspace: %v", err)
	}
	options := run.Request.RuntimeOptions
	if options.WorkingDirectory != existing || options.WorkspacePolicy.SandboxMode != "read-only" || options.WorkspacePolicy.ApprovalPolicy != "never" {
		t.Fatalf("authorized workspace was overwritten: %+v", options)
	}
}

func TestPrepareProductionAgentRunWorkspaceUsesProfilePersistentWorkspace(t *testing.T) {
	persistent := filepath.Join(t.TempDir(), "agents", "jordan-belfort")
	run := runtimedaemon.AgentRun{
		RunID:     "run-profile-workspace",
		MissionID: "mission-profile-workspace",
		TaskID:    "task-profile-workspace",
	}

	if err := prepareProductionAgentRunWorkspace(&run, loadTestConfig(t), persistent); err != nil {
		t.Fatalf("prepareProductionAgentRunWorkspace: %v", err)
	}
	options := run.Request.RuntimeOptions
	policy := options.WorkspacePolicy
	if options.WorkingDirectory != filepath.Clean(persistent) || policy.Root != filepath.Clean(persistent) || policy.WorkDir != filepath.Clean(persistent) {
		t.Fatalf("profile workspace was not used: %+v", options)
	}
	if !reflect.DeepEqual(options.SandboxProfile, policy.Profile) || !policy.EnforceBoundary {
		t.Fatalf("profile workspace policy is incomplete: %+v", options)
	}
	if info, err := os.Stat(persistent); err != nil || !info.IsDir() {
		t.Fatalf("profile workspace directory was not created: info=%v err=%v", info, err)
	}
}

func TestProductionAgentRunWorkerDependenciesCarrySharedRuntime(t *testing.T) {
	registry := runtimesession.NewConnectionRegistry()
	dispatcher := runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
		return runtimecore.RuntimeToolCallResult{}, nil
	})
	broker := runtimecore.ApprovalBrokerFunc(func(context.Context, runtimecore.ApprovalRequest, runtimecore.ApprovalResponder) error {
		return nil
	})

	deps := productionAgentRunWorkerDependencies(productionAgentRunWorkerRegistration{
		RuntimeRegistry: registry,
		ToolDispatcher:  dispatcher,
		ApprovalBroker:  broker,
	})
	if deps.RuntimeRegistry != registry || deps.ToolDispatcher == nil || deps.ApprovalBroker == nil {
		t.Fatalf("queued worker runtime dependencies = %+v", deps)
	}
}

func TestProductionAgentRunWorkerUsesConfiguredRuntimeOverProfileRuntime(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	cfg := loadTestConfig(t, map[string]string{
		"discussion.agent.runtime_kind": "pi",
	})
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	template, err := readDefaultAgentTemplate("lead-agent.json")
	if err != nil {
		t.Fatalf("read lead template: %v", err)
	}
	profile := defaultCoordinatorAgentProfile(defaultLeadAgentID, "lead", agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}, template)
	saveAvailableAgentProfile(t, ctx, profiles, profile)
	assembler := &capturingAgentRunWorkerAssembler{}
	worker := productionAgentRunWorker{
		assembler: assembler,
		profiles:  profiles,
		config:    cfg,
	}

	_, err = worker.buildAgent(ctx, runtimedaemon.AgentRun{
		RunID:          "run-configured-worker-runtime",
		AgentID:        agentcore.AgentID(defaultLeadAgentID),
		AgentProfileID: profile.ID,
	})
	if err != nil {
		t.Fatalf("buildAgent: %v", err)
	}
	if assembler.profile.RuntimeSpec.Kind != agentcore.RuntimeKindPi {
		t.Fatalf("runtime kind = %q, want configured %q", assembler.profile.RuntimeSpec.Kind, agentcore.RuntimeKindPi)
	}
}

func TestProductionAgentRunWorkerUsesSchedulerAssemblerForScheduledRuns(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	template, err := readDefaultAgentTemplate("lead-agent.json")
	if err != nil {
		t.Fatal(err)
	}
	profile := defaultCoordinatorAgentProfile(defaultLeadAgentID, "lead", agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindPi}, template)
	saveAvailableAgentProfile(t, ctx, profiles, profile)
	missionAssembler := &capturingAgentRunWorkerAssembler{}
	schedulerAssembler := &capturingAgentRunWorkerAssembler{}
	worker := productionAgentRunWorker{assembler: missionAssembler, schedulerAssembler: schedulerAssembler, profiles: profiles}

	_, err = worker.buildAgent(ctx, runtimedaemon.AgentRun{
		RunID:          "scheduled-run",
		AgentID:        agentcore.AgentID(defaultLeadAgentID),
		AgentProfileID: profile.ID,
		Request: agentcore.RuntimeRequest{RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{
			"run_source": schedulerAgentTaskRunSource,
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if schedulerAssembler.profile.ID != profile.ID || !missionAssembler.profile.ID.IsZero() {
		t.Fatalf("scheduler profile=%q mission profile=%q", schedulerAssembler.profile.ID, missionAssembler.profile.ID)
	}
}

func TestProductionAgentRunWorkerPreservesConfiguredRuntimeForMigratedTask(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	template, err := readDefaultAgentTemplate("lead-agent.json")
	if err != nil {
		t.Fatal(err)
	}
	profile := defaultCoordinatorAgentProfile(defaultLeadAgentID, "lead", agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged, ConfigRef: agentCoreRuntimeProfileID}, template)
	saveAvailableAgentProfile(t, ctx, profiles, profile)
	schedulerAssembler := &capturingAgentRunWorkerAssembler{}
	worker := productionAgentRunWorker{assembler: schedulerAssembler, schedulerAssembler: schedulerAssembler, profiles: profiles}

	_, err = worker.buildAgent(ctx, runtimedaemon.AgentRun{
		RunID:          "native-scheduled-run",
		AgentID:        agentcore.AgentID(defaultLeadAgentID),
		AgentProfileID: profile.ID,
		Request: agentcore.RuntimeRequest{RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{
			"run_source":                         schedulerAgentTaskRunSource,
			schedulerRuntimeConfigRefProviderKey: nativeScheduledRuntimeProfileID,
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if schedulerAssembler.profile.RuntimeSpec.Kind != agentcore.RuntimeKindManaged || schedulerAssembler.profile.RuntimeSpec.ConfigRef != agentCoreRuntimeProfileID {
		t.Fatalf("runtime spec = %+v", schedulerAssembler.profile.RuntimeSpec)
	}
}

func TestProductionAgentRunWorkerReadsCanonicalUsageForApplicationCommit(t *testing.T) {
	runtime := &agentRunResponseReader{response: agentcore.RuntimeResponse{
		RunID: "run-usage", Status: agentcore.RuntimeStatusCompleted,
		Usage: map[string]agentcore.TokenUsage{"model-a": {InputTokens: 11, OutputTokens: 7, CacheReadTokens: 3}},
	}}
	agent := agentcore.NewBaseAgent(agentcore.BaseAgentConfig{
		Identity: agentcore.AgentIdentity{AgentID: "agent-a", AgentProfileID: "profile-a", InstanceRef: agentcore.AgentInstanceRef{ID: "instance-a"}},
		Profile:  agentcore.AgentProfile{ID: "profile-a"}, Runtime: runtime,
	})
	response, ok := productionAgentRunRuntimeResponse(agent, "run-usage")
	if !ok || response.Usage["model-a"].InputTokens != 11 || response.Usage["model-a"].OutputTokens != 7 {
		t.Fatalf("canonical runtime response = %#v, ok=%v", response, ok)
	}
	cloned := cloneProductionRuntimeUsage(response.Usage)
	cloned["model-a"] = agentcore.TokenUsage{InputTokens: 99}
	if runtime.response.Usage["model-a"].InputTokens != 11 {
		t.Fatal("application usage commit aliases canonical runtime record")
	}
}

func TestRuntimeLifecycleKnowledgeRefsExtractSearchResultLineage(t *testing.T) {
	refs := runtimeLifecycleKnowledgeRefs(agentcore.RuntimeResponse{ToolCalls: []agentcore.RuntimeToolCall{
		{Name: "knowledge_search", Output: `{"results":[{"document_id":"doc-1","chunk_id":"doc-1:0"},{"document_id":"doc-1","chunk_id":"doc-1:1"}]}`},
		{Name: "unrelated_tool", Output: `{"document_id":"must-not-appear"}`},
	}})
	want := []runtimedaemon.RuntimeKnowledgeRef{
		{Kind: "knowledge_document", ID: "doc-1"},
		{Kind: "knowledge_chunk", ID: "doc-1:0"},
		{Kind: "knowledge_chunk", ID: "doc-1:1"},
	}
	if !reflect.DeepEqual(refs, want) {
		t.Fatalf("knowledge refs = %+v, want %+v", refs, want)
	}
}

type agentRunResponseReader struct {
	response agentcore.RuntimeResponse
}

func (r *agentRunResponseReader) RuntimeResponse(runID agentcore.AgentRunID) (agentcore.RuntimeResponse, bool) {
	return r.response, r != nil && r.response.RunID == runID
}

func (r *agentRunResponseReader) RunSession(context.Context, agentcore.RuntimeRequest, agentcore.SessionEventObserver) (agentcore.RuntimeResponse, error) {
	return r.response, nil
}
func (*agentRunResponseReader) CancelSession(context.Context, agentcore.AgentRunID, string) error {
	return nil
}
func (*agentRunResponseReader) SteerSession(context.Context, agentcore.AgentRunID, string) error {
	return nil
}
func (*agentRunResponseReader) PrewarmSession(context.Context, agentcore.RuntimeRequest) error {
	return nil
}
func (*agentRunResponseReader) ReleaseRuntimeWorkspace(context.Context, string) error { return nil }
func (*agentRunResponseReader) Capabilities(context.Context) agentcore.Capabilities {
	return agentcore.Capabilities{}
}
func (*agentRunResponseReader) RuntimeRequest(agentcore.AgentRunID) (agentcore.RuntimeRequest, bool) {
	return agentcore.RuntimeRequest{}, false
}
func (*agentRunResponseReader) RuntimeEvents(agentcore.AgentRunID) []agentcore.SessionEnvelope {
	return nil
}
func (*agentRunResponseReader) SubscribeRuntimeEvents(agentcore.AgentRunID) (<-chan agentcore.SessionEnvelope, func()) {
	ch := make(chan agentcore.SessionEnvelope)
	close(ch)
	return ch, func() {}
}
func (*agentRunResponseReader) DeleteRuntimeRecord(context.Context, agentcore.AgentRunID) error {
	return nil
}

func TestProductionAgentRunWorkerAppliesManagedWorkspaceOnlyToBoundMissionTask(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	profile := workerTestProfile(t, "managed-workspace-agent")
	saveAvailableAgentProfile(t, ctx, profiles, profile)
	runtime := &capturingWorkerRuntime{}
	assembler := &runtimeCapturingWorkerAssembler{runtime: runtime}
	development := &capturingDevelopmentAgentRuns{prepared: developmentproject.PreparedAgentRun{
		ExecutionRunID: "development-run-1", WorkspaceID: "workspace-1", RootDir: `C:\managed\workspace`,
		WorkDir: `C:\managed\workspace\orrery_v2`, NetworkMode: "none", AllowedHosts: []string{"api.example.test"},
	}, managed: true}
	run := workerTestRun(profile, "managed-run", "mission-managed", "task-managed")
	run.Request.RuntimeOptions.WorkingDirectory = `C:\untrusted\request`
	payload, err := json.Marshal(run)
	if err != nil {
		t.Fatalf("marshal run: %v", err)
	}
	worker := productionAgentRunWorker{assembler: assembler, profiles: profiles, lifecycle: &capturingAgentRunLifecycle{}, development: development}
	if err := worker.HandleExecution(ctx, platform.ExecutionDelivery{Topic: runtimedaemon.AgentRunTopic, Payload: payload, MaxAttempts: 1}); err != nil {
		t.Fatalf("HandleExecution: %v", err)
	}
	request := runtime.request
	if request.RuntimeOptions.WorkingDirectory != development.prepared.WorkDir || request.RuntimeOptions.SandboxProfile.Root != development.prepared.RootDir ||
		request.RuntimeOptions.SandboxProfile.Mode != agentcore.RuntimeSandboxWorkspaceWrite || !request.RuntimeOptions.SandboxProfile.EnforceBoundary {
		t.Fatalf("managed runtime options = %+v", request.RuntimeOptions)
	}
	if request.RuntimeOptions.ProviderSpecific["development_execution_run_id"] != development.prepared.ExecutionRunID ||
		request.RuntimeOptions.ProviderSpecific["development_workspace_id"] != development.prepared.WorkspaceID ||
		request.RuntimeOptions.ProviderSpecific["development_allowed_hosts"] != "api.example.test" {
		t.Fatalf("managed provider metadata = %+v", request.RuntimeOptions.ProviderSpecific)
	}

	runtime.request = agentcore.RuntimeRequest{}
	development.managed = false
	unrelated := workerTestRun(profile, "unrelated-run", "mission-unrelated", "task-unrelated")
	unrelated.Request.RuntimeOptions.WorkingDirectory = `C:\ordinary\workspace`
	unrelated.Request.RuntimeOptions.SandboxProfile = agentcore.RuntimeSandboxProfile{Mode: agentcore.RuntimeSandboxReadOnly, Root: `C:\ordinary\workspace`}
	payload, err = json.Marshal(unrelated)
	if err != nil {
		t.Fatalf("marshal unrelated run: %v", err)
	}
	if err := worker.HandleExecution(ctx, platform.ExecutionDelivery{Topic: runtimedaemon.AgentRunTopic, Payload: payload, MaxAttempts: 1}); err != nil {
		t.Fatalf("HandleExecution unrelated: %v", err)
	}
	if runtime.request.RuntimeOptions.WorkingDirectory != unrelated.Request.RuntimeOptions.WorkingDirectory || runtime.request.RuntimeOptions.SandboxProfile.Mode != agentcore.RuntimeSandboxReadOnly {
		t.Fatalf("unrelated Mission workspace was replaced: %+v", runtime.request.RuntimeOptions)
	}
}

func TestProductionAgentRunWorkerAppliesProfilePersistentWorkspace(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	profile := workerTestProfile(t, "persistent-workspace-agent")
	profile.WorkspaceRoot = filepath.Join(t.TempDir(), "agents", "persistent-workspace-agent")
	saveAvailableAgentProfile(t, ctx, profiles, profile)
	runtime := &capturingWorkerRuntime{}
	run := workerTestRun(profile, "persistent-run", "mission-persistent", "task-persistent")
	payload, err := json.Marshal(run)
	if err != nil {
		t.Fatalf("marshal run: %v", err)
	}
	worker := productionAgentRunWorker{
		assembler: &runtimeCapturingWorkerAssembler{runtime: runtime},
		profiles:  profiles,
		lifecycle: &capturingAgentRunLifecycle{},
	}

	if err := worker.HandleExecution(ctx, platform.ExecutionDelivery{Topic: runtimedaemon.AgentRunTopic, Payload: payload, MaxAttempts: 1}); err != nil {
		t.Fatalf("HandleExecution: %v", err)
	}
	options := runtime.request.RuntimeOptions
	if options.WorkingDirectory != profile.WorkspaceRoot || options.WorkspacePolicy.Root != profile.WorkspaceRoot || options.WorkspacePolicy.WorkDir != profile.WorkspaceRoot {
		t.Fatalf("runtime did not receive profile workspace: %+v", options)
	}
}

func TestProductionAgentRunWorkerReleasesManagedLeaseAfterRuntimeFailure(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	profile := workerTestProfile(t, "failed-workspace-agent")
	saveAvailableAgentProfile(t, ctx, profiles, profile)
	runtimeFailure := errors.New("runtime unavailable")
	development := &capturingDevelopmentAgentRuns{prepared: developmentproject.PreparedAgentRun{
		ExecutionRunID: "development-run-failed", WorkspaceID: "workspace-failed", RootDir: `C:\managed\failed`, WorkDir: `C:\managed\failed`,
	}, managed: true}
	run := workerTestRun(profile, "failed-run", "mission-failed", "task-failed")
	payload, err := json.Marshal(run)
	if err != nil {
		t.Fatalf("marshal run: %v", err)
	}
	worker := productionAgentRunWorker{
		assembler: &runtimeCapturingWorkerAssembler{runtime: &capturingWorkerRuntime{err: runtimeFailure}},
		profiles:  profiles, lifecycle: &capturingAgentRunLifecycle{}, development: development,
	}
	if err := worker.HandleExecution(ctx, platform.ExecutionDelivery{Topic: runtimedaemon.AgentRunTopic, Payload: payload, MaxAttempts: 3}); !errors.Is(err, runtimeFailure) {
		t.Fatalf("HandleExecution error = %v, want runtime failure", err)
	}
	if development.failCalls != 1 || development.failedRunID != run.RunID.String() || !errors.Is(development.failCause, runtimeFailure) {
		t.Fatalf("development failure release = calls=%d run=%q cause=%v", development.failCalls, development.failedRunID, development.failCause)
	}
}

func TestDevelopmentAgentResultCommitterStopsMissionDeliveryWhenControlledCommitFails(t *testing.T) {
	commitFailure := errors.New("controlled commit rejected")
	development := &capturingDevelopmentAgentRuns{commitErr: commitFailure, managed: true}
	next := &countingWorkerResultCommitter{}
	committer := developmentAgentResultCommitter{projects: development, next: next}
	result := agentcore.AgentResult{MissionID: "mission-commit", TaskID: "task-commit", AgentID: "agent-commit", RunID: "run-commit", Summary: agentcore.Summary{Text: "done"}}

	if err := committer.SubmitAgentResult(context.Background(), result, agentcore.CommitOptions{TenantID: "tenant-1"}); !errors.Is(err, commitFailure) {
		t.Fatalf("SubmitAgentResult error = %v, want controlled commit failure", err)
	}
	if next.calls != 0 {
		t.Fatalf("Mission delivery calls = %d, want 0", next.calls)
	}
}

type capturingAgentRunWorkerAssembler struct {
	profile agentcore.AgentProfile
	err     error
}

type postCommitWorkerAssembler struct {
	runtime *countingPostCommitWorkerRuntime
	err     error
}

type countingPostCommitWorkerRuntime struct {
	calls atomic.Int32
}

type postCommitWorkerDelivery struct{ err error }

type capturingPostCommitRepairScheduler struct {
	calls  int
	input  agentcore.AgentInput
	result agentcore.AgentResult
}

type runtimeCapturingWorkerAssembler struct{ runtime *capturingWorkerRuntime }

type capturingWorkerRuntime struct {
	request  agentcore.RuntimeRequest
	response agentcore.RuntimeResponse
	ctx      context.Context
	err      error
}

type capturingDevelopmentAgentRuns struct {
	prepared    developmentproject.PreparedAgentRun
	managed     bool
	prepareErr  error
	commitErr   error
	failCalls   int
	failedRunID string
	failCause   error
}

type countingWorkerResultCommitter struct{ calls int }

func (a *runtimeCapturingWorkerAssembler) Build(_ context.Context, profile agentcore.AgentProfile, opts agentassembly.BuildOptions) (agentcore.AgentInstance, error) {
	return agentcore.NewBaseAgent(agentcore.BaseAgentConfig{
		Identity: agentcore.AgentIdentity{AgentID: opts.AgentID, AgentProfileID: profile.ID, InstanceRef: agentcore.AgentInstanceRef{ID: opts.InstanceID}},
		Profile:  profile, Runtime: a.runtime, Delivery: &countingWorkerResultCommitter{}, Trace: agentcore.NoopTraceRecorder{}, Notifier: agentcore.NoopNotifier{},
		Validator: agentcore.ResultValidatorFunc(func(agentcore.AgentInput, agentcore.AgentResult) error { return nil }), Summary: agentcore.DefaultSummaryGenerator{},
	}), nil
}

func (r *capturingWorkerRuntime) Run(ctx context.Context, request agentcore.RuntimeRequest) (agentcore.RuntimeResponse, error) {
	r.ctx = ctx
	r.request = request
	if r.err != nil {
		return agentcore.RuntimeResponse{}, r.err
	}
	response := r.response
	if response.Status == "" {
		response = agentcore.RuntimeResponse{Status: agentcore.RuntimeStatusCompleted, Messages: []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Status: agentcore.RuntimeMessageStatusFinal, Content: "done"}}}
	}
	if response.RunID == "" {
		response.RunID = request.RunID
	}
	r.response = response
	return response, nil
}

func (r *capturingWorkerRuntime) RunSession(ctx context.Context, request agentcore.RuntimeRequest, _ agentcore.SessionEventObserver) (agentcore.RuntimeResponse, error) {
	return r.Run(ctx, request)
}

func (*capturingWorkerRuntime) CancelSession(context.Context, agentcore.AgentRunID, string) error {
	return nil
}

func (*capturingWorkerRuntime) SteerSession(context.Context, agentcore.AgentRunID, string) error {
	return nil
}

func (*capturingWorkerRuntime) PrewarmSession(context.Context, agentcore.RuntimeRequest) error {
	return nil
}

func (*capturingWorkerRuntime) ReleaseRuntimeWorkspace(context.Context, string) error {
	return nil
}

func (*capturingWorkerRuntime) Capabilities(context.Context) agentcore.Capabilities {
	return agentcore.Capabilities{}
}

func (r *capturingWorkerRuntime) RuntimeRequest(runID agentcore.AgentRunID) (agentcore.RuntimeRequest, bool) {
	return r.request, r != nil && r.request.RunID == runID
}

func (r *capturingWorkerRuntime) RuntimeResponse(runID agentcore.AgentRunID) (agentcore.RuntimeResponse, bool) {
	return r.response, r != nil && r.response.RunID == runID
}

func (*capturingWorkerRuntime) RuntimeEvents(agentcore.AgentRunID) []agentcore.SessionEnvelope {
	return nil
}

func (*capturingWorkerRuntime) SubscribeRuntimeEvents(agentcore.AgentRunID) (<-chan agentcore.SessionEnvelope, func()) {
	ch := make(chan agentcore.SessionEnvelope)
	close(ch)
	return ch, func() {}
}

func (*capturingWorkerRuntime) DeleteRuntimeRecord(context.Context, agentcore.AgentRunID) error {
	return nil
}

func (d *capturingDevelopmentAgentRuns) PrepareAgentRun(context.Context, string, string, string, string, string) (developmentproject.PreparedAgentRun, bool, error) {
	return d.prepared, d.managed, d.prepareErr
}

func (d *capturingDevelopmentAgentRuns) CommitAgentRunResult(context.Context, string, string, string, string, string, string) (developmentproject.TodoCommitRecord, bool, error) {
	return developmentproject.TodoCommitRecord{}, d.managed, d.commitErr
}

func (d *capturingDevelopmentAgentRuns) FailAgentRun(_ context.Context, _, _, _, _, runID string, cause error) error {
	d.failCalls++
	d.failedRunID = runID
	d.failCause = cause
	return nil
}

func (c *countingWorkerResultCommitter) SubmitAgentResult(context.Context, agentcore.AgentResult, agentcore.CommitOptions) error {
	c.calls++
	return nil
}

func workerTestProfile(t *testing.T, id string) agentcore.AgentProfile {
	t.Helper()
	template, err := readDefaultAgentTemplate("lead-agent.json")
	if err != nil {
		t.Fatalf("read lead template: %v", err)
	}
	return defaultCoordinatorAgentProfile(id, "worker", agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}, template)
}

func workerTestRun(profile agentcore.AgentProfile, runID, missionID, taskID string) runtimedaemon.AgentRun {
	run := runtimedaemon.AgentRun{
		RunID: agentcore.AgentRunID(runID), AgentID: agentcore.AgentID(profile.ID), AgentProfileID: profile.ID,
		AgentInstanceID: agentcore.AgentInstanceID(profile.ID), RuntimeID: string(agentcore.RuntimeKindCodex), MissionID: missionID, TaskID: taskID,
	}
	run.Request.TenantID = "tenant-1"
	return run
}

func (s *capturingPostCommitRepairScheduler) Schedule(_ context.Context, input agentcore.AgentInput, result agentcore.AgentResult) error {
	s.calls++
	s.input = input
	s.result = result
	return nil
}

type failingResponsibilityRunLifecycle struct {
	err error
}

type capturingAgentRunLifecycle struct {
	started  []runtimedaemon.RuntimeLifecycleEvent
	failures []runtimedaemon.RuntimeFailureEvent
}

func (c *capturingAgentRunLifecycle) PublishRuntimeLifecycle(_ context.Context, event runtimedaemon.RuntimeLifecycleEvent) error {
	c.started = append(c.started, event)
	return nil
}

func (c *capturingAgentRunLifecycle) PublishRuntimeFailure(_ context.Context, event runtimedaemon.RuntimeFailureEvent) error {
	c.failures = append(c.failures, event)
	return nil
}

func (f failingResponsibilityRunLifecycle) Completed(context.Context, string, string, string) error {
	return f.err
}

func (f failingResponsibilityRunLifecycle) Failed(context.Context, string, string, string, runtimedaemon.RuntimeLifecycleKind, error) error {
	return f.err
}

func (a *capturingAgentRunWorkerAssembler) Build(_ context.Context, profile agentcore.AgentProfile, opts agentassembly.BuildOptions) (agentcore.AgentInstance, error) {
	a.profile = profile
	if a.err != nil {
		return nil, a.err
	}
	return agentcore.NewBaseAgent(agentcore.BaseAgentConfig{
		Identity: agentcore.AgentIdentity{
			AgentID:        opts.AgentID,
			AgentProfileID: profile.ID,
			InstanceRef:    agentcore.AgentInstanceRef{ID: opts.InstanceID},
		},
		Profile: profile,
	}), nil
}
