package pi

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

// TestNativePiRealRPCHandshakeSmoke is opt-in because it starts the pinned PI
// CLI. It verifies the real JSONL handshake and discovery path without a
// process fixture or model credentials.
func TestNativePiRealRPCHandshakeSmoke(t *testing.T) {
	if os.Getenv("ORRERY_PI_REAL_RPC_SMOKE") != "1" {
		t.Skip("set ORRERY_PI_REAL_RPC_SMOKE=1 to exercise the installed PI CLI")
	}
	executable := strings.TrimSpace(os.Getenv("ORRERY_PI_REAL_EXECUTABLE"))
	if executable == "" {
		executable = "pi"
	}
	providerConfig, err := json.Marshal(map[string]any{"executable": executable})
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	request := agentcore.RuntimeRequest{
		TenantID: "tenant-pi-real-smoke", RunID: "run-pi-real-smoke", AgentID: "agent-pi-real-smoke",
		RuntimeOptions: agentcore.RuntimeOptions{
			WorkingDirectory: workspace,
			ProviderConfig:   providerConfig,
			WorkspacePolicy: agentcore.RuntimeWorkspacePolicy{
				Root: workspace, WorkDir: workspace, EnforceBoundary: true,
			},
		},
	}
	connection, err := NewNativeConnection(NativeConfig{ResolveWorkspace: NativeWorkspaceResolverFunc(func(context.Context, string) (NativeWorkspace, error) {
		return NativeWorkspace{Request: request}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	backend, err := connection.OpenSession(context.Background(), agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "pi-real-rpc-smoke"}, agentcore.SessionConfig{WorkspaceRef: "pi-real-rpc-workspace"})
	if err != nil {
		t.Fatal(err)
	}
	session := backend.(*NativeSession)
	var bound, catalog bool
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for !bound || !catalog {
		select {
		case envelope, ok := <-session.Events():
			if !ok {
				t.Fatal("PI event stream closed during the real RPC handshake")
			}
			switch envelope.Event.Kind {
			case agentcore.EventBackendBound:
				bound = strings.TrimSpace(envelope.Event.BackendBound.BackendSessionID) != ""
			case agentcore.EventCatalogUpdated:
				catalog = true
			case agentcore.EventDetached:
				t.Fatalf("PI detached during the real RPC handshake: %s", envelope.Event.Detached.RedactedSummary)
			}
		case <-timer.C:
			t.Fatal("timed out waiting for the real PI RPC handshake")
		}
	}
	capabilities := session.Capabilities()
	if capabilities.Transport.Version != supportedNativePiVersion || capabilities.Transport.Protocol != "rpc-jsonl-stdio" {
		t.Fatalf("real PI capabilities = %#v", capabilities)
	}
}
