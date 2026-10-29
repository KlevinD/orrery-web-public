package main

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	deviceprovider "orrery_v2/internal/agent/runtime/providers/device"
	"orrery_v2/internal/platform/testkit"
	toolres "orrery_v2/internal/resource/tool"
	toolstd "orrery_v2/internal/resource/tool/std"
	mcptransport "orrery_v2/internal/transport/mcp"
)

type browserTestProfiles struct{ profile agentcore.AgentProfile }

type browserTestHumanProfiles struct{ browserTestProfiles }

func (s browserTestHumanProfiles) GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	if id == "H_BROWSER_001" {
		return agentcore.AgentProfile{ID: id, Enabled: true, AgentKind: agentcore.AgentKindHuman, UserAccount: agentcore.UserAccountSpec{Username: "owner-a"}}, nil
	}
	return s.browserTestProfiles.GetProfile(ctx, id)
}

func browserTestHumanEnvelope(t *testing.T) *agentcore.ExecutionEnvelope {
	e := browserTestEnvelope(t)
	e.PrincipalID = "H_BROWSER_001"
	if err := e.Seal(); err != nil {
		t.Fatal(err)
	}
	return e
}

func (s browserTestProfiles) QueryDirectory(context.Context, agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	return agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{AgentProfileID: s.profile.ID, AgentInstanceID: agentcore.AgentInstanceID(s.profile.ID)}}}, nil
}
func (s browserTestProfiles) GetProfile(_ context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	if id != s.profile.ID {
		return agentcore.AgentProfile{}, errors.New("missing profile")
	}
	return s.profile, nil
}
func browserTestProfile() agentcore.AgentProfile {
	return agentcore.AgentProfile{ID: "assistant-browser", Enabled: true, PersonalAssistantOwnerID: "owner-a", PersonalAssistantTenantID: "tenant-a", ToolRefs: toolstd.EnsurePersonalBrowserToolRefs(nil)}
}
func browserTestEnvelope(t *testing.T) *agentcore.ExecutionEnvelope {
	t.Helper()
	e := &agentcore.ExecutionEnvelope{Version: 1, EnvelopeID: "browser-envelope", IssuedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Minute), TenantID: "tenant-a", RunID: "run-browser", AgentID: "assistant-browser", AgentProfileID: "assistant-browser", PrincipalID: "owner-a", PrincipalKind: agentcore.ExecutionPrincipalUser, PermissionScopes: []string{"tool.call"}, BoundToolPermissionScopes: map[string][]string{"personal_browser_read": {toolstd.PermissionPersonalBrowserRead}, "personal_browser_control": {toolstd.PermissionPersonalBrowserControl}}}
	if err := e.Seal(); err != nil {
		t.Fatal(err)
	}
	return e
}
func browserTestBroker(t *testing.T) *deviceprovider.DurableBroker {
	t.Helper()
	repo, err := deviceprovider.NewSQLRepository(context.Background(), testkit.NewTestDB(t))
	if err != nil {
		t.Fatal(err)
	}
	b, err := deviceprovider.NewDurableBroker(deviceprovider.BrokerOptions{Repository: repo})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return b
}

func TestPersonalBrowserExecutorRequiresTrustedOwnerAndExactToolScope(t *testing.T) {
	b := browserTestBroker(t)
	for _, mode := range []string{"owner", "no-envelope", "tampered", "expired", "agent-principal", "foreign-owner", "foreign-tenant", "organization-agent", "disabled-profile", "wrong-scope", "extra-argument", "control-via-read"} {
		t.Run(mode, func(t *testing.T) {
			profile := browserTestProfile()
			envelope := browserTestEnvelope(t)
			req := mcptransport.CallToolRequest{TenantID: "tenant-a", PrincipalID: "owner-a", AgentID: "assistant-browser", ToolID: "personal_browser_read", Arguments: map[string]any{"operation": "devices", "arguments": map[string]any{}}, ExecutionEnvelope: envelope}
			switch mode {
			case "no-envelope":
				req.ExecutionEnvelope = nil
			case "tampered":
				envelope.PrincipalID = "attacker"
			case "expired":
				envelope.ExpiresAt = time.Now().UTC().Add(-time.Second)
				_ = envelope.Seal()
			case "agent-principal":
				envelope.PrincipalKind = agentcore.ExecutionPrincipalAgent
				_ = envelope.Seal()
			case "foreign-owner":
				profile.PersonalAssistantOwnerID = "someone-else"
			case "foreign-tenant":
				profile.PersonalAssistantTenantID = "tenant-b"
			case "organization-agent":
				profile.PersonalAssistantOwnerID = ""
			case "disabled-profile":
				profile.Enabled = false
			case "wrong-scope":
				envelope.BoundToolPermissionScopes = nil
				_ = envelope.Seal()
			case "extra-argument":
				req.Arguments["owner_id"] = "someone-else"
			case "control-via-read":
				req.Arguments = map[string]any{"operation": "fill", "arguments": map[string]any{}}
			}
			result, err := (productionPersonalBrowserExecutor{broker: b, profiles: browserTestProfiles{profile}}).ExecutePersonalBrowser(context.Background(), req)
			if mode == "owner" {
				if err != nil || result.Output["devices"] == nil {
					t.Fatalf("owner discovery: %+v %v", result, err)
				}
			} else if err == nil {
				t.Fatalf("invalid authority accepted: %s %+v", mode, result)
			}
		})
	}
}

func TestPersonalBrowserProductionDispatcherAndGatewayPreserveOwnerEnvelope(t *testing.T) {
	ctx := context.Background()
	b := browserTestBroker(t)
	profiles := browserTestProfiles{browserTestProfile()}
	registry := toolres.NewMemoryRegistry()
	for _, definition := range toolstd.Definitions(time.Now().UTC()) {
		if err := registry.Register(ctx, definition); err != nil {
			t.Fatal(err)
		}
	}
	gateway := newProductionMCPGateway(productionMCPGatewayOptions{Registry: registry, Profiles: profiles, PersonalBrowserExecutor: productionPersonalBrowserExecutor{broker: b, profiles: profiles}})
	dispatcher := productionRuntimeToolDispatcher{mcpGateway: gateway}
	call := runtimecore.RuntimeToolCallRequest{TenantID: "tenant-a", RunID: "run-browser", AgentID: "assistant-browser", Name: "personal_browser_read", ExecutionPrincipal: agentcore.ExecutionPrincipal{ID: "owner-a", Kind: agentcore.ExecutionPrincipalUser, TenantID: "tenant-a", PermissionScope: []string{"tool.call"}}, ExecutionEnvelope: browserTestEnvelope(t), ToolBindings: []agentcore.RuntimeToolBinding{{ToolID: toolstd.PersonalBrowserRead, Alias: "personal_browser_read", GrantedPermissions: []string{toolstd.PermissionPersonalBrowserRead}}}, Input: map[string]any{"operation": "devices", "arguments": map[string]any{}}}
	result, err := dispatcher.CallRuntimeTool(ctx, call)
	if err != nil || result.Output["devices"] == nil {
		t.Fatalf("real gateway lost owner/tool authority: %+v %v", result, err)
	}
	call.ExecutionEnvelope = nil
	result, err = dispatcher.CallRuntimeTool(ctx, call)
	if err == nil && result.Output["status"] != "failed" {
		t.Fatal("unsealed browser tool call accepted")
	}
}
