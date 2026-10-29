package main

import (
	"os"
	"path/filepath"
	"testing"

	"orrery_v2/internal/platform/config"
)

func TestProductionAuthorizationFromEnvReadsTokenFile(t *testing.T) {
	t.Setenv("TEST_RUNTIME_AUTHORIZATION", "")
	t.Setenv("TEST_RUNTIME_AUTHORIZATION_FILE", "")
	t.Setenv("TEST_RUNTIME_TOKEN", "")
	tokenPath := filepath.Join(t.TempDir(), "runtime-mcp-token")
	if err := os.WriteFile(tokenPath, []byte("runtime-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_RUNTIME_TOKEN_FILE", tokenPath)

	if got := productionAuthorizationFromEnv("TEST_RUNTIME_AUTHORIZATION", "TEST_RUNTIME_TOKEN"); got != "Bearer runtime-token" {
		t.Fatalf("authorization = %q", got)
	}
}

func TestProductionAuthorizationFromEnvPrefersDirectAuthorization(t *testing.T) {
	authorizationPath := filepath.Join(t.TempDir(), "runtime-mcp-authorization")
	if err := os.WriteFile(authorizationPath, []byte("Bearer file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_RUNTIME_AUTHORIZATION", "Bearer direct-token")
	t.Setenv("TEST_RUNTIME_AUTHORIZATION_FILE", authorizationPath)
	t.Setenv("TEST_RUNTIME_TOKEN", "fallback-token")

	if got := productionAuthorizationFromEnv("TEST_RUNTIME_AUTHORIZATION", "TEST_RUNTIME_TOKEN"); got != "Bearer direct-token" {
		t.Fatalf("authorization = %q", got)
	}
}

func TestProductionRuntimeEnvironmentResolvesFileBackedMCPToken(t *testing.T) {
	t.Setenv("ORRERY_RUNTIME_MCP_AUTHORIZATION", "")
	t.Setenv("ORRERY_RUNTIME_MCP_TOKEN", "")
	tokenPath := filepath.Join(t.TempDir(), "runtime-mcp-token")
	if err := os.WriteFile(tokenPath, []byte("runtime-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ORRERY_RUNTIME_MCP_TOKEN_FILE", tokenPath)

	environment := productionRuntimeEnvironment(config.Config{})
	if got := environment["ORRERY_RUNTIME_MCP_AUTHORIZATION"]; got != "Bearer runtime-token" {
		t.Fatalf("runtime authorization = %q", got)
	}
	if _, exists := environment["ORRERY_RUNTIME_MCP_TOKEN"]; exists {
		t.Fatal("file-backed token must not be exposed under the token variable")
	}
}
