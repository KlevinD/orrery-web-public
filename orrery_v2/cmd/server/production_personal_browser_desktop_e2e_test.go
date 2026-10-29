package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	deviceprovider "orrery_v2/internal/agent/runtime/providers/device"
	"orrery_v2/internal/device/delivery"
	devicedirectory "orrery_v2/internal/device/directory"
	deviceidentity "orrery_v2/internal/device/identity"
	"orrery_v2/internal/platform/testkit"
	toolres "orrery_v2/internal/resource/tool"
	toolstd "orrery_v2/internal/resource/tool/std"
)

type browserE2EAudit struct{}

func (browserE2EAudit) RecordDeviceAudit(context.Context, delivery.AuditEvent) error { return nil }

// Opt-in fixture used by the isolated Chromium test. All routes and the test
// browser identity exist only in a Go test binary, bound to loopback. Device
// PKCE registration, rotation, DPoP, WSS protocol and runtime routing are real.
func TestPersonalBrowserDesktopE2EFixture(t *testing.T) {
	config := os.Getenv("ORRERY_BROWSER_E2E_CONFIG")
	secret := os.Getenv("ORRERY_BROWSER_E2E_SECRET")
	if config == "" || len(secret) < 32 {
		t.Skip("run via desktop-browser.test.mjs")
	}
	ctx := context.Background()
	database := testkit.NewTestDB(t)
	repository, err := deviceprovider.NewSQLRepository(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	b, err := deviceprovider.NewDurableBroker(deviceprovider.BrokerOptions{Repository: repository})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	identityStore, err := deviceidentity.NewSQLStore(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	directoryStore, err := devicedirectory.NewSQLStore(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := devicedirectory.NewService(devicedirectory.Options{
		Store: directoryStore, DeviceConnected: b.DeviceConnected, DefaultTenantID: "tenant-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := deviceidentity.NewService(deviceidentity.Options{Store: identityStore})
	if err != nil {
		t.Fatal(err)
	}
	profiles := browserTestHumanProfiles{browserTestProfiles{browserTestProfile()}}
	registry := toolres.NewMemoryRegistry()
	for _, definition := range toolstd.Definitions(time.Now().UTC()) {
		if err := registry.Register(ctx, definition); err != nil {
			t.Fatal(err)
		}
	}
	gateway := newProductionMCPGateway(productionMCPGatewayOptions{Registry: registry, Profiles: profiles, PersonalBrowserExecutor: productionPersonalBrowserExecutor{broker: b, profiles: profiles}})
	dispatcher := productionRuntimeToolDispatcher{mcpGateway: gateway}
	done := make(chan struct{}, 1)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			http.Error(w, "fixture authorization required", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/__test/shutdown" {
			select {
			case done <- struct{}{}:
			default:
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/__test/desktop" && r.Method == http.MethodGet {
			var clientVersion, runtimeVersion string
			err := database.QueryRow(r.Context(), `SELECT l.client_version, l.aioncore_version
FROM runtime_device_leases l JOIN devices d ON d.device_id = l.device_id AND d.tenant_id = l.tenant_id
WHERE d.tenant_id = ? AND d.owner_id = ? AND d.revoked_at IS NULL`, "tenant-a", "owner-a").Scan(&clientVersion, &runtimeVersion)
			if err != nil {
				http.Error(w, "device hello not recorded", http.StatusServiceUnavailable)
				return
			}
			agents, err := directoryStore.ListOwnedAgents(r.Context(), devicedirectory.OwnerScope{TenantID: "tenant-a", OwnerID: "owner-a"})
			if err != nil {
				http.Error(w, "device catalog unavailable", http.StatusServiceUnavailable)
				return
			}
			profileIDs := make([]string, 0, len(agents))
			for _, agent := range agents {
				profileIDs = append(profileIDs, agent.LocalProfileID)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"client_version": clientVersion, "runtime_version": runtimeVersion, "profile_ids": profileIDs})
			return
		}
		if r.URL.Path != "/__test/tool" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var input struct {
			ToolID    string         `json:"tool_id"`
			Arguments map[string]any `json:"arguments"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || !toolstd.IsPersonalBrowserTool(input.ToolID) {
			http.Error(w, "invalid fixture tool", http.StatusBadRequest)
			return
		}
		permission := toolstd.PermissionPersonalBrowserRead
		if input.ToolID == string(toolstd.PersonalBrowserControl) {
			permission = toolstd.PermissionPersonalBrowserControl
		}
		result, err := dispatcher.CallRuntimeTool(r.Context(), runtimecore.RuntimeToolCallRequest{TenantID: "tenant-a", RunID: "run-browser", AgentID: "assistant-browser", Name: input.ToolID,
			ExecutionPrincipal: agentcore.ExecutionPrincipal{ID: "H_BROWSER_001", Kind: agentcore.ExecutionPrincipalUser, TenantID: "tenant-a", PermissionScope: []string{"tool.call"}}, ExecutionEnvelope: browserTestHumanEnvelope(t),
			ToolBindings: []agentcore.RuntimeToolBinding{{ToolID: agentcore.ToolID(input.ToolID), Alias: input.ToolID, GrantedPermissions: []string{permission}}}, Input: input.Arguments})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result.Output)
	})
	handler, err := delivery.NewHandler(delivery.Options{Identity: identity,
		AuthenticateBrowser: func(_ context.Context, r *http.Request) (deviceidentity.BrowserPrincipal, error) {
			cookie, err := r.Cookie("orrery_access_token")
			if err != nil || cookie.Value != secret {
				return deviceidentity.BrowserPrincipal{}, deviceidentity.ErrUnauthorized
			}
			return deviceidentity.BrowserPrincipal{UserID: "owner-a", TenantID: "tenant-a", DisplayName: "Browser test owner"}, nil
		},
		AuthorizeTenant: func(_ context.Context, p deviceidentity.BrowserPrincipal, tenant string) error {
			if p.UserID != "owner-a" || tenant != "tenant-a" {
				return deviceidentity.ErrUnauthorized
			}
			return nil
		},
		Audit: browserE2EAudit{}, Sessions: productionDeviceSessionRunner{broker: b, now: time.Now}, AgentDirectory: directory, Next: next, DefaultTenantID: "tenant-a", AllowRequestOrigin: true})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	data, _ := json.Marshal(map[string]string{"url": server.URL})
	if err := os.WriteFile(config, data, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(4 * time.Minute):
		t.Fatal("desktop browser E2E fixture timed out")
	}
}
