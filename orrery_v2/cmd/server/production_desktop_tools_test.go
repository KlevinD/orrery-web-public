package main

import (
	"context"
	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	devicedirectory "orrery_v2/internal/device/directory"
	"testing"
	"time"
)

func TestDesktopToolsRequireActualOwnerPrincipalAndExplicitToolScope(t *testing.T) {
	directory := &recordingPersonalAssistantDirectory{view: devicedirectory.PersonalAssistantView{OwnedAgents: []devicedirectory.OwnedAgentView{{DeviceID: "device_test", Status: devicedirectory.AgentStatusOnline, WorkspaceGrants: []devicedirectory.WorkspaceGrantDescriptor{{GrantID: "grant_test", Available: true}}}}}}
	request := runtimecore.RuntimeToolCallRequest{TenantID: "tenant_test", RunID: "run_test", AgentID: "agent_test", Name: "desktop_read_excel", CallID: "call_test", ExecutionPrincipal: agentcore.ExecutionPrincipal{Kind: agentcore.ExecutionPrincipalUser, ID: "user_test", TenantID: "tenant_test", PermissionScope: []string{"desktop.workspace.read"}}, ToolBindings: []agentcore.RuntimeToolBinding{{ToolID: "desktop_read_excel", GrantedPermissions: []string{"desktop.workspace.read"}}}}
	request.ExecutionEnvelope = &agentcore.ExecutionEnvelope{TenantID: "tenant_test", RunID: "run_test", AgentID: "agent_test", AgentProfileID: "agent_test", PrincipalID: "user_test", PrincipalKind: agentcore.ExecutionPrincipalUser, PermissionScopes: []string{"desktop.workspace.read"}, AllowedToolIDs: []string{"desktop_read_excel"}, MissionID: "project_test", ExpiresAt: time.Now().Add(time.Hour).UTC()}
	if err := request.ExecutionEnvelope.Seal(); err != nil {
		t.Fatal(err)
	}
	resolve := productionDesktopToolResolver(directory)
	target, err := resolve(context.Background(), request, "device_test", "grant_test")
	if err != nil || target.ActorID != "agent_test" || target.ProjectID != "project_test" || directory.scope.OwnerID != "user_test" {
		t.Fatalf("valid owner denied: %#v %v", target, err)
	}
	if err := productionAgentCoreToolPolicy(context.Background(), request); err != nil {
		t.Fatal("managed kernel denied valid desktop tool", err)
	}
	for _, change := range []func(*runtimecore.RuntimeToolCallRequest){
		func(c *runtimecore.RuntimeToolCallRequest) { c.ExecutionPrincipal.ID = "other_user" },
		func(c *runtimecore.RuntimeToolCallRequest) { c.TenantID = "other_tenant" },
		func(c *runtimecore.RuntimeToolCallRequest) { c.AgentID = "other_agent" },
		func(c *runtimecore.RuntimeToolCallRequest) { c.ExecutionEnvelope = nil },
		func(c *runtimecore.RuntimeToolCallRequest) {
			c.ExecutionEnvelope = agentcore.CloneExecutionEnvelope(c.ExecutionEnvelope)
			c.ExecutionEnvelope.AllowedToolIDs = nil
			_ = c.ExecutionEnvelope.Seal()
		},
		func(c *runtimecore.RuntimeToolCallRequest) { c.ToolBindings = nil },
	} {
		mutated := request
		change(&mutated)
		if _, err := resolve(context.Background(), mutated, "device_test", "grant_test"); err == nil {
			t.Fatal("accepted changed principal/envelope/binding")
		}
	}
	if _, err := resolve(context.Background(), request, "other_device", "grant_test"); err == nil {
		t.Fatal("accepted foreign device")
	}
	if _, err := resolve(context.Background(), request, "device_test", "other_grant"); err == nil {
		t.Fatal("accepted foreign grant")
	}
}
