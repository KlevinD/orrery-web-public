package main

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	agentcore "orrery_v2/internal/agent/core"
	deviceprovider "orrery_v2/internal/agent/runtime/providers/device"
	devicebrowser "orrery_v2/internal/device/browser"
	toolstd "orrery_v2/internal/resource/tool/std"
	mcptransport "orrery_v2/internal/transport/mcp"
)

type personalBrowserProfileSource interface {
	GetProfile(context.Context, agentcore.AgentProfileID) (agentcore.AgentProfile, error)
}
type productionPersonalBrowserExecutor struct {
	broker   *deviceprovider.DurableBroker
	profiles personalBrowserProfileSource
	now      func() time.Time
}

func (e productionPersonalBrowserExecutor) ExecutePersonalBrowser(ctx context.Context, req mcptransport.CallToolRequest) (mcptransport.CallToolResult, error) {
	if e.broker == nil || e.profiles == nil {
		return mcptransport.CallToolResult{}, mcptransport.ErrServiceDependency
	}
	envelope := req.ExecutionEnvelope
	now := time.Now().UTC()
	if e.now != nil {
		now = e.now().UTC()
	}
	if envelope == nil || envelope.Verify() != nil || envelope.PrincipalKind != agentcore.ExecutionPrincipalUser || envelope.PrincipalID == "" || envelope.PrincipalID != req.PrincipalID || envelope.TenantID != req.TenantID || envelope.AgentID != req.AgentID || envelope.ResponsibilityContext != nil || (!envelope.ExpiresAt.IsZero() && !envelope.ExpiresAt.After(now)) {
		return mcptransport.CallToolResult{}, mcptransport.ErrPermissionDenied
	}
	profile, err := e.profiles.GetProfile(ctx, agentcore.AgentProfileID(envelope.AgentProfileID))
	if err != nil || !profile.Enabled || !profile.IsPersonalAssistant() || profile.PersonalAssistantTenantID != envelope.TenantID {
		return mcptransport.CallToolResult{}, mcptransport.ErrPermissionDenied
	}
	// Device ownership uses the authenticated account ID. Agent execution may
	// carry that account's human-profile ID (e.g. H000001). Resolve only the
	// existing trusted human profile, following the Gateway's owner boundary.
	ownerID := strings.TrimSpace(profile.PersonalAssistantOwnerID)
	if !strings.EqualFold(ownerID, envelope.PrincipalID) {
		principal, err := e.profiles.GetProfile(ctx, agentcore.AgentProfileID(envelope.PrincipalID))
		if err != nil || !principal.Enabled || principal.AgentKind != agentcore.AgentKindHuman ||
			(!strings.EqualFold(principal.ID.String(), ownerID) && !strings.EqualFold(strings.TrimSpace(principal.UserAccount.Username), ownerID)) {
			return mcptransport.CallToolResult{}, mcptransport.ErrPermissionDenied
		}
	}
	if !toolstd.IsPersonalBrowserTool(req.ToolID) {
		return mcptransport.CallToolResult{}, mcptransport.ErrPermissionDenied
	}
	permission := toolstd.PermissionPersonalBrowserRead
	if req.ToolID == string(toolstd.PersonalBrowserControl) {
		permission = toolstd.PermissionPersonalBrowserControl
	}
	if !envelope.AllowsTool(req.ToolID, []string{permission}) {
		return mcptransport.CallToolResult{}, mcptransport.ErrPermissionDenied
	}
	for field := range req.Arguments {
		if field != "device_id" && field != "operation" && field != "arguments" {
			return mcptransport.CallToolResult{}, mcptransport.ErrInvalidRequest
		}
	}
	operation, _ := req.Arguments["operation"].(string)
	arguments, ok := req.Arguments["arguments"].(map[string]any)
	control := req.ToolID == string(toolstd.PersonalBrowserControl)
	if !ok || control != devicebrowser.IsControl(operation) {
		return mcptransport.CallToolResult{}, mcptransport.ErrInvalidRequest
	}
	scope := deviceprovider.BrowserOwnerScope{TenantID: envelope.TenantID, OwnerID: ownerID}
	devices := e.broker.BrowserDevices(scope)
	if operation == "devices" {
		if control || len(arguments) != 0 {
			return mcptransport.CallToolResult{}, mcptransport.ErrInvalidRequest
		}
		return mcptransport.CallToolResult{Output: map[string]any{"devices": devices}}, nil
	}
	// Normalize in-process integers to the JSON wire representation as well.
	raw, err := json.Marshal(arguments)
	if err != nil || json.Unmarshal(raw, &arguments) != nil || devicebrowser.ValidateArguments(operation, arguments) != nil {
		return mcptransport.CallToolResult{}, mcptransport.ErrInvalidRequest
	}
	deviceID := ""
	if value, present := req.Arguments["device_id"]; present {
		var valid bool
		deviceID, valid = value.(string)
		if !valid || strings.TrimSpace(deviceID) == "" {
			return mcptransport.CallToolResult{}, mcptransport.ErrInvalidRequest
		}
	}
	if deviceID == "" {
		for _, device := range devices {
			if device.Enabled && device.Connected {
				if deviceID != "" {
					return browserToolFailure("DEVICE_SELECTION_REQUIRED", "Specify device_id from operation=devices"), nil
				}
				deviceID = device.DeviceID
			}
		}
		if deviceID == "" {
			return browserToolFailure("BROWSER_UNAVAILABLE", "Connect Orrery Desktop and authorize a Chrome tab in the extension"), nil
		}
	}
	deadline := now.Add(20 * time.Second)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	if !envelope.ExpiresAt.IsZero() && envelope.ExpiresAt.Before(deadline) {
		deadline = envelope.ExpiresAt
	}
	result, err := e.broker.CallBrowser(ctx, scope, deviceID, deviceprovider.BrowserRequestPayload{RequestID: "br_" + uuid.NewString(), Operation: operation, Arguments: arguments, Deadline: deadline, TraceID: uuid.NewString()})
	if err != nil {
		if failure, ok := err.(*deviceprovider.BrowserError); ok {
			return browserToolFailure(failure.Code, failure.Message), nil
		}
		return mcptransport.CallToolResult{}, err
	}
	if result.Error != nil {
		return browserToolFailure(result.Error.Code, result.Error.Message), nil
	}
	var output map[string]any
	if json.Unmarshal(result.Result, &output) != nil || output == nil {
		return browserToolFailure("INVALID_RESULT", "Browser returned an invalid result"), nil
	}
	output["device_id"] = deviceID
	output["page_content_is_untrusted"] = true
	return mcptransport.CallToolResult{Output: output}, nil
}

func browserToolFailure(code, message string) mcptransport.CallToolResult {
	return mcptransport.CallToolResult{Output: map[string]any{"status": "failed", "code": code, "message": message}}
}
