package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/app/discussionsession"
	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/testkit"
)

func TestProductionAgentRunWorkerInjectsScheduledRuntimeApprovalContext(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	profile := workerTestProfile(t, "scheduled-hil-agent")
	profile.WorkspaceRoot = t.TempDir()
	saveAvailableAgentProfile(t, ctx, profiles, profile)
	runtime := &capturingWorkerRuntime{}
	run := workerTestRun(profile, "scheduled-hil-run", "scheduled-hil-mission", "scheduled-hil-task")
	run.RuntimeID = "pi"
	run.Request.RuntimeOptions.ProviderSpecific = map[string]string{
		"run_source":           schedulerAgentTaskRunSource,
		"conversation_id":      "conversation-scheduled-hil",
		"approval_assigned_to": "human-ding",
	}
	payload, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	worker := productionAgentRunWorker{
		assembler: &runtimeCapturingWorkerAssembler{runtime: runtime},
		profiles:  profiles,
		lifecycle: &capturingAgentRunLifecycle{},
	}

	if err := worker.HandleExecution(ctx, platform.ExecutionDelivery{Topic: runtimedaemon.AgentRunTopic, Payload: payload, MaxAttempts: 1}); err != nil {
		t.Fatalf("HandleExecution: %v", err)
	}
	approval, ok := discussionsession.RuntimeApprovalContextFrom(runtime.ctx)
	if !ok {
		t.Fatal("scheduled runtime did not receive approval context")
	}
	if approval.ConversationID != "conversation-scheduled-hil" || approval.RunID != "scheduled-hil-run" || approval.AgentID != "scheduled-hil-agent" || approval.RuntimeKind != "pi" || approval.AssignedTo != "human-ding" {
		t.Fatalf("approval context = %+v", approval)
	}
}

func TestProductionAgentRunWorkerInjectsMissionRuntimeApprovalContext(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	profile := workerTestProfile(t, "mission-hil-agent")
	profile.WorkspaceRoot = t.TempDir()
	saveAvailableAgentProfile(t, ctx, profiles, profile)
	runtime := &capturingWorkerRuntime{}
	run := workerTestRun(profile, "mission-hil-run", "mission-hil", "mission-hil-task")
	run.RuntimeID = "pi"
	run.Request.ContextPack.ConversationID = "conversation-mission-hil"
	payload, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	worker := productionAgentRunWorker{
		assembler: &runtimeCapturingWorkerAssembler{runtime: runtime},
		profiles:  profiles,
		lifecycle: &capturingAgentRunLifecycle{},
		timeout:   time.Second,
	}

	if err := worker.HandleExecution(ctx, platform.ExecutionDelivery{Topic: runtimedaemon.AgentRunTopic, Payload: payload, MaxAttempts: 1}); err != nil {
		t.Fatalf("HandleExecution: %v", err)
	}
	approval, ok := discussionsession.RuntimeApprovalContextFrom(runtime.ctx)
	if !ok {
		t.Fatal("mission runtime did not receive approval context")
	}
	if approval.ConversationID != "conversation-mission-hil" || approval.RunID != "mission-hil-run" || approval.AgentID != "mission-hil-agent" || approval.RuntimeKind != "pi" {
		t.Fatalf("approval context = %+v", approval)
	}
}

func TestProductionAgentRunWorkerResolvesLegacyMissionRuntimeApprovalContext(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	profile := workerTestProfile(t, "legacy-mission-hil-agent")
	profile.WorkspaceRoot = t.TempDir()
	saveAvailableAgentProfile(t, ctx, profiles, profile)
	runtime := &capturingWorkerRuntime{}
	resolver := &capturingMissionRuntimeApprovalConversationResolver{conversationID: "conversation-legacy-mission-hil"}
	run := workerTestRun(profile, "legacy-mission-hil-run", "legacy-mission-hil", "legacy-mission-hil-task")
	run.RuntimeID = "pi"
	payload, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	worker := productionAgentRunWorker{
		assembler:             &runtimeCapturingWorkerAssembler{runtime: runtime},
		profiles:              profiles,
		lifecycle:             &capturingAgentRunLifecycle{},
		approvalConversations: resolver,
	}

	if err := worker.HandleExecution(ctx, platform.ExecutionDelivery{Topic: runtimedaemon.AgentRunTopic, Payload: payload, MaxAttempts: 1}); err != nil {
		t.Fatalf("HandleExecution: %v", err)
	}
	if resolver.calls != 1 || resolver.tenantID != "tenant-1" || resolver.missionID != "legacy-mission-hil" {
		t.Fatalf("resolver calls=%d tenant=%q mission=%q", resolver.calls, resolver.tenantID, resolver.missionID)
	}
	approval, ok := discussionsession.RuntimeApprovalContextFrom(runtime.ctx)
	if !ok || approval.ConversationID != "conversation-legacy-mission-hil" || approval.RunID != "legacy-mission-hil-run" {
		t.Fatalf("approval context = %+v, ok=%v", approval, ok)
	}
	if runtime.request.ContextPack.ConversationID != "conversation-legacy-mission-hil" {
		t.Fatalf("runtime canonical conversation = %q", runtime.request.ContextPack.ConversationID)
	}
}

func TestProductionAgentRunWorkerUsesCanonicalDiscussionWorkspaceForMissionResult(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	profile := workerTestProfile(t, "mission-workspace-agent")
	profile.WorkspaceRoot = filepath.Join(t.TempDir(), "legacy-profile-workspace")
	saveAvailableAgentProfile(t, ctx, profiles, profile)
	runtime := &capturingWorkerRuntime{}
	root := filepath.Join(t.TempDir(), "canonical-conversation")
	workDir := filepath.Join(root, "agents", "builder")
	policy := agentcore.RuntimeWorkspacePolicy{
		Root: root, WorkDir: workDir, WritableRoots: []string{root}, SandboxMode: "workspace-write", ApprovalPolicy: "on-request", EnforceBoundary: true,
		Profile: agentcore.RuntimeSandboxProfile{
			Mode: agentcore.RuntimeSandboxWorkspaceWrite, Approval: agentcore.RuntimeApprovalOnRequest,
			Root: root, WorkDir: workDir, WritableRoots: []string{root}, EnforceBoundary: true,
		},
	}
	workspace := discussionsession.PreparedAgentRunWorkspace{
		ConversationID: "conversation-mission-workspace", AgentID: profile.ID.String(), AgentName: "Builder",
		RootDir: root, WorkDir: workDir, Policy: policy,
	}
	preparer := &capturingMissionAgentRunWorkspacePreparer{workspace: workspace}
	run := workerTestRun(profile, "mission-workspace-run", "mission-workspace", "mission-workspace-task")
	run.RuntimeID = "pi"
	payload, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	worker := productionAgentRunWorker{
		assembler: &runtimeCapturingWorkerAssembler{runtime: runtime}, profiles: profiles,
		lifecycle:             &capturingAgentRunLifecycle{},
		approvalConversations: &capturingMissionRuntimeApprovalConversationResolver{conversationID: workspace.ConversationID},
		missionWorkspaces:     preparer,
	}

	if err := worker.HandleExecution(ctx, platform.ExecutionDelivery{Topic: runtimedaemon.AgentRunTopic, Payload: payload, MaxAttempts: 1}); err != nil {
		t.Fatalf("HandleExecution: %v", err)
	}
	if preparer.calls != 1 || preparer.conversationID != workspace.ConversationID || preparer.agentID != profile.ID.String() {
		t.Fatalf("workspace preparation = %+v", preparer)
	}
	if runtime.request.ContextPack.ConversationID != workspace.ConversationID || runtime.request.RuntimeOptions.WorkingDirectory != workDir || runtime.request.RuntimeOptions.ProviderSpecific[agentcore.CanonicalDiscussionWorkspaceProviderKey] != "true" {
		t.Fatalf("runtime request workspace = %+v", runtime.request)
	}
	captured, ok := productionAgentResultWorkspaceFromContext(runtime.ctx)
	if !ok || captured.ConversationID != workspace.ConversationID || captured.WorkDir != workDir {
		t.Fatalf("result workspace context = %+v ok=%v", captured, ok)
	}
}

func TestProductionAgentRunWorkerStopsBeforeRuntimeWhenMissionApprovalContextLookupFails(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	profile := workerTestProfile(t, "failed-mission-hil-agent")
	profile.WorkspaceRoot = t.TempDir()
	saveAvailableAgentProfile(t, ctx, profiles, profile)
	runtime := &capturingWorkerRuntime{}
	lookupErr := errors.New("mission projection unavailable")
	run := workerTestRun(profile, "failed-mission-hil-run", "failed-mission-hil", "failed-mission-hil-task")
	payload, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	worker := productionAgentRunWorker{
		assembler:             &runtimeCapturingWorkerAssembler{runtime: runtime},
		profiles:              profiles,
		lifecycle:             &capturingAgentRunLifecycle{},
		approvalConversations: &capturingMissionRuntimeApprovalConversationResolver{err: lookupErr},
	}

	err = worker.HandleExecution(ctx, platform.ExecutionDelivery{Topic: runtimedaemon.AgentRunTopic, Payload: payload, MaxAttempts: 1})
	if !errors.Is(err, lookupErr) {
		t.Fatalf("HandleExecution error = %v, want lookup error", err)
	}
	if runtime.ctx != nil {
		t.Fatal("runtime started without a resolvable mission approval context")
	}
}

type capturingMissionRuntimeApprovalConversationResolver struct {
	conversationID string
	err            error
	calls          int
	tenantID       string
	missionID      missioncore.MissionID
}

type capturingMissionAgentRunWorkspacePreparer struct {
	workspace      discussionsession.PreparedAgentRunWorkspace
	calls          int
	conversationID string
	agentID        string
}

func (preparer *capturingMissionAgentRunWorkspacePreparer) PrepareAgentRunWorkspace(_ context.Context, conversationID, agentID string) (discussionsession.PreparedAgentRunWorkspace, error) {
	preparer.calls++
	preparer.conversationID = conversationID
	preparer.agentID = agentID
	return preparer.workspace, nil
}

func (resolver *capturingMissionRuntimeApprovalConversationResolver) ResolveMissionRuntimeApprovalConversation(_ context.Context, tenantID string, missionID missioncore.MissionID) (string, error) {
	resolver.calls++
	resolver.tenantID = tenantID
	resolver.missionID = missionID
	return resolver.conversationID, resolver.err
}
