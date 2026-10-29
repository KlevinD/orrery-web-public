package main

import (
	"context"
	"fmt"
	"slices"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	deviceprovider "orrery_v2/internal/agent/runtime/providers/device"
	devicedirectory "orrery_v2/internal/device/directory"
)

func productionDesktopToolResolver(directory productionPersonalAssistantDirectory) deviceprovider.WorkspaceToolResolver {
	return func(ctx context.Context, call runtimecore.RuntimeToolCallRequest, deviceID, grantID string) (deviceprovider.WorkspaceToolTarget, error) {
		principal := runtimeExecutionPrincipal(call)
		envelope := call.ExecutionEnvelope
		binding, bound := runtimeToolBindingForCall(call.Name, call.ToolBindings)
		if envelope == nil || !slices.Contains(envelope.AllowedToolIDs, string(binding.ToolID)) {
			return deviceprovider.WorkspaceToolTarget{}, fmt.Errorf("desktop tool is not explicitly granted")
		}
		if directory == nil || principal.Kind != agentcore.ExecutionPrincipalUser || principal.ID == "" || principal.TenantID != call.TenantID || !bound || envelope == nil || validateRuntimeExecutionEnvelope(call, binding) != nil || envelope.PrincipalID != principal.ID || envelope.PrincipalKind != principal.Kind || !envelope.AllowsTool(string(binding.ToolID), []string{desktopToolPermission(string(binding.ToolID))}) {
			return deviceprovider.WorkspaceToolTarget{}, fmt.Errorf("desktop permission denied")
		}
		view, err := directory.GetPersonalAssistant(ctx, devicedirectory.OwnerScope{TenantID: call.TenantID, OwnerID: principal.ID})
		if err != nil {
			return deviceprovider.WorkspaceToolTarget{}, err
		}
		// Only a catalog belonging to this actual user can select a device. Local
		// grant policy remains authoritative for the actual executing Agent ID.
		for _, owned := range view.OwnedAgents {
			if owned.DeviceID != deviceID || owned.Status != devicedirectory.AgentStatusOnline {
				continue
			}
			for _, grant := range owned.WorkspaceGrants {
				if grant.GrantID == grantID && grant.Available {
					project := envelope.MissionID
					if project == "" {
						project = envelope.TaskID
					}
					if project == "" {
						project = call.ProviderSpecific["discussion_id"]
					}
					if project == "" {
						project = call.RunID.String()
					}
					return deviceprovider.WorkspaceToolTarget{DeviceID: deviceID, GrantID: grantID, ProjectID: deviceprovider.WorkspaceToolProjectID(project), ActorID: call.AgentID.String()}, nil
				}
			}
		}
		return deviceprovider.WorkspaceToolTarget{}, fmt.Errorf("desktop device or grant unavailable")
	}
}

func desktopToolPermission(id string) string {
	if id == "desktop_write_excel" {
		return "desktop.workspace.write"
	}
	return "desktop.workspace.read"
}
