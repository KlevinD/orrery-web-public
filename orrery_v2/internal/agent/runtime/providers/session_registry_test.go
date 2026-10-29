package providers

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/providers/acp"
	"orrery_v2/internal/agent/runtime/providers/claude"
	"orrery_v2/internal/agent/runtime/providers/codex"
	deviceprovider "orrery_v2/internal/agent/runtime/providers/device"
	"orrery_v2/internal/agent/runtime/providers/openclaw"
	"orrery_v2/internal/agent/runtime/providers/pi"
	"orrery_v2/internal/platform/testkit"
)

func TestSessionRegistryAlwaysSelectsNativeCodex(t *testing.T) {
	enabled, err := NewSessionRegistry(SessionRegistryConfig{NativeCodex: codex.NativeConfig{Version: "0.145.0"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = enabled.Close() })
	connection, err := enabled.Resolve(agentcore.RuntimeKindCodex)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := connection.(*codex.NativeConnection); !ok {
		t.Fatalf("Codex connection type = %T", connection)
	}
	if _, ok := enabled.Capabilities(nil, agentcore.RuntimeKindCodex); !ok {
		t.Fatal("native Codex capabilities unavailable")
	}
}

func TestSessionRegistrySelectsDurableDeviceConnectionOnlyWhenConfigured(t *testing.T) {
	disabled, err := NewSessionRegistry(SessionRegistryConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := disabled.Resolve(agentcore.RuntimeKindDevice); err == nil {
		t.Fatal("unconfigured Device Runtime silently resolved")
	}

	repository, err := deviceprovider.NewSQLRepository(context.Background(), testkit.NewTestDB(t))
	if err != nil {
		t.Fatal(err)
	}
	broker, err := deviceprovider.NewDurableBroker(deviceprovider.BrokerOptions{Repository: repository})
	if err != nil {
		t.Fatal(err)
	}
	enabled, err := NewSessionRegistry(SessionRegistryConfig{Device: deviceprovider.ConnectionConfig{Broker: broker, CloseBroker: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = enabled.Close() })
	connection, err := enabled.Resolve(agentcore.RuntimeKindDevice)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := connection.(*deviceprovider.Connection); !ok {
		t.Fatalf("Device feature selected %T", connection)
	}
	capabilities, ok := enabled.Capabilities(context.Background(), agentcore.RuntimeKindDevice)
	if !ok || capabilities.SupportedCommands.AnswerPermission || capabilities.SupportedCommands.AnswerAuth || !capabilities.SupportsResume {
		t.Fatalf("Device capabilities = %#v/%v", capabilities, ok)
	}
}

func TestSessionRegistryAlwaysSelectsNativeClaude(t *testing.T) {
	enabled, err := NewSessionRegistry(SessionRegistryConfig{NativeClaude: claude.NativeConfig{Version: "2.1.220"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = enabled.Close() })
	connection, err := enabled.Resolve(agentcore.RuntimeKindClaude)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := connection.(*claude.NativeConnection); !ok {
		t.Fatalf("Claude connection type = %T", connection)
	}
	if _, ok := enabled.Capabilities(nil, agentcore.RuntimeKindClaude); !ok {
		t.Fatal("native Claude capabilities unavailable")
	}
}

func TestSessionRegistryAlwaysSelectsNativePi(t *testing.T) {
	registry, err := NewSessionRegistry(SessionRegistryConfig{NativePi: pi.NativeConfig{Version: "1.3.0"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	connection, err := registry.Resolve(agentcore.RuntimeKindPi)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := connection.(*pi.NativeConnection); !ok {
		t.Fatalf("PI connection type = %T", connection)
	}
	capabilities, ok := registry.Capabilities(context.Background(), agentcore.RuntimeKindPi)
	if !ok || capabilities.Transport.Protocol != "rpc-jsonl-stdio" || !capabilities.SupportsMultiTurn {
		t.Fatalf("PI capabilities = %#v/%v", capabilities, ok)
	}
}

func TestSessionRegistryAlwaysSelectsGenericACP(t *testing.T) {
	registry, err := NewSessionRegistry(SessionRegistryConfig{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	connection, err := registry.Resolve(agentcore.RuntimeKindACP)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := connection.(*acp.Connection); !ok {
		t.Fatalf("ACP connection type = %T", connection)
	}
	capabilities, ok := registry.Capabilities(context.Background(), agentcore.RuntimeKindACP)
	if !ok || capabilities.Transport.Protocol != "agent-client-protocol-jsonrpc-stdio" || !capabilities.SupportsMultiTurn {
		t.Fatalf("ACP capabilities = %#v/%v", capabilities, ok)
	}
}

func TestSessionRegistrySelectsOpenClawConnection(t *testing.T) {
	registry, err := NewSessionRegistry(SessionRegistryConfig{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	connection, err := registry.Resolve(agentcore.RuntimeKindOpenClaw)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := connection.(*openclaw.Connection); !ok {
		t.Fatalf("OpenClaw connection type = %T", connection)
	}
	capabilities, ok := registry.Capabilities(context.Background(), agentcore.RuntimeKindOpenClaw)
	if !ok || !capabilities.SupportedCommands.Send || !capabilities.SupportsResume {
		t.Fatalf("OpenClaw capabilities = %#v/%v", capabilities, ok)
	}
}

func TestSessionRegistryMarksUnmigratedProvidersUnavailable(t *testing.T) {
	registry, err := NewSessionRegistry(SessionRegistryConfig{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	for _, kind := range []agentcore.RuntimeKind{agentcore.RuntimeKindGemini, agentcore.RuntimeKindCursor, agentcore.RuntimeKindManaged} {
		if _, err := registry.Resolve(kind); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeWorkspaceUnavailable) {
			t.Fatalf("Resolve(%s) = %v", kind, err)
		}
	}
}
