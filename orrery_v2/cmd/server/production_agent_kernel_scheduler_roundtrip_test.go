package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/agentkernel"
	"orrery_v2/internal/agent/runtime/llmprofile"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/testkit"
	toolres "orrery_v2/internal/resource/tool"
	toolstd "orrery_v2/internal/resource/tool/std"
	mcptransport "orrery_v2/internal/transport/mcp"
)

type schedulerOwnerProfiles struct {
	principalRegressionProfileStore
}

func (p schedulerOwnerProfiles) GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	profile, err := p.principalRegressionProfileStore.GetProfile(ctx, id)
	if id == "A000017" {
		for _, toolID := range []agentcore.ToolID{toolstd.ScheduledTaskCreate, toolstd.ScheduledTaskUpdate, toolstd.ScheduledTaskCancel, toolstd.ScheduledTaskRunNow} {
			permission := toolstd.PermissionSchedulerWrite
			if toolID == toolstd.ScheduledTaskRunNow {
				permission = toolstd.PermissionSchedulerRun
			}
			profile.ToolRefs = append(profile.ToolRefs, agentcore.ToolRef{ToolID: toolID, PermissionScope: permission})
		}
	}
	return profile, err
}

type schedulerFixtureDispatch struct{ calls int }

func (d *schedulerFixtureDispatch) DispatchRun(_ context.Context, _ schedulerapp.ScheduledTask, run schedulerapp.ScheduledRun) (schedulerapp.ScheduledRun, error) {
	d.calls++
	return run, nil
}

// Exercises the production model loop, policy, dispatcher, Gateway and scheduler
// store together. Dispatch stays inside this test; no worker or delivery runs.
func TestProductionAgentCoreSchedulerOwnerRoundTrip(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	repo := schedulerapp.NewRepository(env.DBRouter)
	service := schedulerapp.NewService(schedulerapp.ServiceOptions{Repository: repo, Clock: env.Clock, IDGenerator: env.IDGenerator})
	dispatch := &schedulerFixtureDispatch{}
	executor := schedulerapp.NewToolExecutor(schedulerapp.ToolExecutorOptions{Service: service, Dispatcher: dispatch})
	tools := toolres.NewMemoryRegistry()
	bindings := []agentcore.RuntimeToolBinding{}
	for _, definition := range toolstd.Definitions(time.Now().UTC()) {
		if err := tools.Register(ctx, definition); err != nil {
			t.Fatal(err)
		}
		switch definition.ID {
		case toolstd.ScheduledTaskCreate, toolstd.ScheduledTaskUpdate, toolstd.ScheduledTaskCancel, toolstd.ScheduledTaskRunNow:
			bindings = append(bindings, agentcore.RuntimeToolBinding{ToolID: definition.ID, InputSchema: definition.InputSchema, GrantedPermissions: []string{definition.Permissions[0].Scope}})
		}
	}
	envelope := &agentcore.ExecutionEnvelope{TenantID: "minvia", RunID: "scheduler-owner-roundtrip", AgentID: "A000017", AgentProfileID: "A000017", PrincipalID: "H000001", PrincipalKind: agentcore.ExecutionPrincipalUser, PermissionScopes: []string{toolstd.PermissionSchedulerWrite, toolstd.PermissionSchedulerRun}}
	if err := envelope.Seal(); err != nil {
		t.Fatal(err)
	}
	gateway := mcptransport.NewGateway(mcptransport.GatewayOptions{Registry: tools, Profiles: schedulerOwnerProfiles{}, ResponsibilityAuthority: personalLiveAuthority{envelope}, Router: mcptransport.NewToolRouter(mcptransport.ToolRouterOptions{Registry: tools, SchedulerExecutor: executor})})
	requests := 0
	taskID := ""
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		name := ""
		args := map[string]any{"scheduled_task_id": taskID}
		switch requests {
		case 1:
			name = string(toolstd.ScheduledTaskCreate)
			args = map[string]any{"title": "Fixture schedule", "enabled": false, "idempotency_key": "fixture-create", "schedule": map[string]any{"type": "once", "run_at": env.Clock.Now().Add(time.Hour).Format(time.RFC3339)}, "target": map[string]any{"mode": "discussion_workflow", "message": "Fixture only"}}
		case 2:
			tasks, err := repo.ListTasks(ctx, true)
			if err != nil || len(tasks) != 1 {
				t.Errorf("create did not persist a single task: %v %v", tasks, err)
			} else {
				taskID, args["scheduled_task_id"] = tasks[0].ScheduledTaskID, tasks[0].ScheduledTaskID
			}
			name, args["title"], args["enabled"] = string(toolstd.ScheduledTaskUpdate), "Updated fixture", true
		case 3:
			name = string(toolstd.ScheduledTaskRunNow)
		case 4:
			name = string(toolstd.ScheduledTaskCancel)
		}
		message := map[string]any{"role": "assistant", "content": "Fixture archived."}
		finish := "stop"
		if name != "" {
			raw, _ := json.Marshal(args)
			message = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"id": fmt.Sprintf("manage-%d", requests), "type": "function", "function": map[string]any{"name": name, "arguments": string(raw)}}}}
			finish = "tool_calls"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message, "finish_reason": finish}}})
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
	registry := newProductionRuntimeRegistry(productionDiscussionAgentDependencies{KernelJournal: journal, ToolDispatcher: productionRuntimeToolDispatcher{mcpGateway: gateway}, LLMProfiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{ProfileID: "fixture", Provider: "openai", BaseURL: model.URL + "/v1", Model: "fixture", APIKey: "fixture", Enabled: true}}}})
	defer registry.Close()
	runtime, err := registry.ResolveRuntime(ctx, agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged})
	if err != nil {
		t.Fatal(err)
	}
	response, err := runtime.RunSession(ctx, agentcore.RuntimeRequest{TenantID: "minvia", AgentID: "A000017", RunID: agentcore.AgentRunID(envelope.RunID), ExecutionEnvelope: envelope, ExecutionPrincipal: agentcore.ExecutionPrincipal{Kind: agentcore.ExecutionPrincipalUser, ID: "H000001", TenantID: "minvia", PermissionScope: envelope.PermissionScopes}, ContextPack: agentcore.ContextPack{AgentID: "A000017"}, ToolBindings: bindings, InputMessages: []agentcore.RuntimeChatMessage{{Role: "user", Content: "Manage this isolated test schedule."}}, RuntimeOptions: agentcore.RuntimeOptions{WorkingDirectory: t.TempDir(), ProviderSpecific: map[string]string{"llm_profile_id": "fixture"}}}, nil)
	if err != nil || response.Status != agentcore.RuntimeStatusCompleted || requests != 5 || dispatch.calls != 1 {
		t.Fatalf("runtime=%+v err=%v requests=%d dispatch=%d", response, err, requests, dispatch.calls)
	}
	task, err := repo.GetTask(ctx, taskID)
	if err != nil || task.CreatedBy != "H000001" || task.CreatedByKind != schedulerapp.ViewerKindUser || task.CreatorProfileID != "H000001" || task.Title != "Updated fixture" || task.Enabled || task.Status != schedulerapp.TaskStatusArchived {
		t.Fatalf("persisted owner/schedule contract lost: task=%+v err=%v", task, err)
	}
	for i := 1; i <= 4; i++ {
		record, found, err := journal.Get(ctx, agentkernel.ToolExecutionKey{TenantID: "minvia", RunID: agentcore.AgentRunID(envelope.RunID), CallID: fmt.Sprintf("manage-%d", i)})
		if err != nil || !found || record.State != agentkernel.ToolExecutionCompleted {
			t.Fatalf("tool journal %d: found=%v record=%+v err=%v", i, found, record, err)
		}
	}
}
