package pi

import (
	"context"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestPiHandshakeRequestErrorPreservesRuntimeBridgeError(t *testing.T) {
	want := agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "pi RPC process exited", true)
	if got := piHandshakeRequestError(want, "pi RPC state handshake failed"); got != want {
		t.Fatalf("handshake error = %v, want original transport error %v", got, want)
	}
}

func TestPiHandshakeRequestErrorClassifiesDeadline(t *testing.T) {
	err := piHandshakeRequestError(context.DeadlineExceeded, "pi RPC state handshake failed")
	if !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeHandshakeTimeout) {
		t.Fatalf("handshake error = %v, want handshake timeout", err)
	}
}

func TestPiProcessEnvironmentExcludesOpenClawVariables(t *testing.T) {
	t.Setenv("OPENCLAW_GATEWAY_TOKEN", "legacy-token")
	t.Setenv("ORRERY_OPENCLAW_BASE_CONFIG_PATH", "/legacy/openclaw.json")
	t.Setenv("ORRERY_RUNTIME_MCP_URL", "http://127.0.0.1:18080/mcp")

	environment := piProcessEnvironment(map[string]string{
		"OPENCLAW_STATE_DIR":     "/legacy/state",
		"ORRERY_OPENCLAW_CONFIG": "/legacy/config.json",
		"ORRERY_PI_SKILL_PATHS":  "/orrery/skills",
	})

	for _, pair := range environment {
		key, _, _ := strings.Cut(pair, "=")
		if strings.HasPrefix(key, "OPENCLAW_") || strings.HasPrefix(key, "ORRERY_OPENCLAW_") {
			t.Fatalf("PI environment contains legacy runtime variable %q", key)
		}
	}
	assertPiEnvironmentValue(t, environment, "ORRERY_RUNTIME_MCP_URL", "http://127.0.0.1:18080/mcp")
	assertPiEnvironmentValue(t, environment, "ORRERY_PI_SKILL_PATHS", "/orrery/skills")
}

func TestPiProcessEnvironmentFiltersUnsupportedProxySchemes(t *testing.T) {
	t.Setenv("HTTP_PROXY", "socks5://127.0.0.1:1080")
	t.Setenv("HTTPS_PROXY", "socks5://127.0.0.1:1080")
	t.Setenv("NO_PROXY", "127.0.0.1")

	environment := piProcessEnvironment(map[string]string{
		"ALL_PROXY":   "unix:///tmp/proxy.sock",
		"https_proxy": "http://127.0.0.1:8080",
	})
	assertPiEnvironmentMissing(t, environment, "HTTP_PROXY")
	assertPiEnvironmentMissing(t, environment, "HTTPS_PROXY")
	assertPiEnvironmentMissing(t, environment, "ALL_PROXY")
	assertPiEnvironmentValue(t, environment, "https_proxy", "http://127.0.0.1:8080")
	assertPiEnvironmentValue(t, environment, "NO_PROXY", "127.0.0.1")
}

func TestPiVersionEnvironmentNeutralizesInheritedUnsupportedProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "socks5://127.0.0.1:1080")
	t.Setenv("HTTPS_PROXY", "https://proxy.example.test:8443")

	environment := piVersionEnvironment(nil)
	if environment["HTTP_PROXY"] != "" {
		t.Fatalf("HTTP_PROXY = %q, want neutralized value", environment["HTTP_PROXY"])
	}
	if _, ok := environment["HTTPS_PROXY"]; ok {
		t.Fatalf("supported HTTPS_PROXY should remain inherited instead of being overridden: %#v", environment)
	}
}

func TestPiProcessEnvironmentUsesHTTPAllProxyWhenProtocolProxiesAreEmpty(t *testing.T) {
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(key, "")
	}
	const proxy = "http://127.0.0.1:21445"
	environment := piProcessEnvironment(map[string]string{"ALL_PROXY": proxy})
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		assertPiEnvironmentValue(t, environment, key, proxy)
	}

	environment = piProcessEnvironment(map[string]string{
		"ALL_PROXY": proxy, "HTTPS_PROXY": "http://127.0.0.1:8080",
	})
	assertPiEnvironmentValue(t, environment, "HTTPS_PROXY", "http://127.0.0.1:8080")
	assertPiEnvironmentMissing(t, environment, "https_proxy")

	environment = piProcessEnvironment(map[string]string{"ALL_PROXY": "socks5://127.0.0.1:1080"})
	assertPiEnvironmentMissing(t, environment, "HTTP_PROXY")
	assertPiEnvironmentMissing(t, environment, "HTTPS_PROXY")
}

func assertPiEnvironmentValue(t *testing.T, environment []string, key, want string) {
	t.Helper()
	for _, pair := range environment {
		name, value, ok := strings.Cut(pair, "=")
		if ok && name == key {
			if value != want {
				t.Fatalf("%s = %q, want %q", key, value, want)
			}
			return
		}
	}
	t.Fatalf("%s is missing from PI environment", key)
}

func assertPiEnvironmentMissing(t *testing.T, environment []string, key string) {
	t.Helper()
	for _, pair := range environment {
		name, _, _ := strings.Cut(pair, "=")
		if name == key {
			t.Fatalf("%s should be absent from PI environment", key)
		}
	}
}
