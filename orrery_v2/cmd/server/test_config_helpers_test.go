package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"orrery_v2/internal/platform/config"
)

const testConfigOverridesEnv = "ORRERY_TEST_CONFIG_OVERRIDES"

func productionTestDatabaseURLs(root string) string {
	namespace := fmt.Sprintf("%x", sha256.Sum256([]byte(root)))[:16]
	return "core=memory://" + namespace + "/core,audit=memory://" + namespace + "/audit,events=memory://" + namespace + "/events,jobs=memory://" + namespace + "/jobs,runtime=memory://" + namespace + "/runtime,observability=memory://" + namespace + "/observability"
}

func persistentProductionTestDatabaseURLs(root string) string {
	return "core=file://" + root + "/core.db,audit=file://" + root + "/audit.db,events=file://" + root + "/events.db,jobs=file://" + root + "/jobs.db,runtime=file://" + root + "/runtime.db,observability=file://" + root + "/observability.db"
}

func loadTestConfig(t *testing.T, overrideSets ...map[string]string) config.Config {
	t.Helper()
	cfg, err := config.LoadYAMLWithSecrets(testConfigSource(t, overrideSets...), testConfigSecrets(overrideSets...))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg
}

func testConfigSecrets(overrideSets ...map[string]string) config.SecretValues {
	var secrets config.SecretValues
	for _, set := range overrideSets {
		for key, value := range set {
			switch key {
			case "ORRERY_DATABASE_PASSWORD":
				secrets.DatabasePassword = value
			case "ORRERY_AUTH_LOCAL_MFA_TOTP_SECRET":
				secrets.LocalMFATOTP = value
			case "ORRERY_FEISHU_APP_ID":
				secrets.FeishuAppID = value
			case "ORRERY_FEISHU_APP_SECRET":
				secrets.FeishuAppSecret = value
			case "ORRERY_FEISHU_STATE_SECRET":
				secrets.FeishuState = value
			case "ORRERY_OIDC_CLIENT_SECRET":
				secrets.OIDCClient = value
			case "ORRERY_KRATOS_ADMIN_TOKEN":
				secrets.KratosAdmin = value
			case "ORRERY_HYDRA_ADMIN_TOKEN":
				secrets.HydraAdmin = value
			case "ORRERY_AUTHZ_OPENFGA_TOKEN":
				secrets.OpenFGAToken = value
			}
		}
	}
	return secrets
}

func selectTestServerConfig(t *testing.T) string {
	t.Helper()
	path := testConfigSource(t, testConfigOverridesFromProcess())
	t.Setenv("ORRERY_CONFIG", path)
	t.Setenv("ORRERY_SKIP_DEFAULT_PROFILE_BOOTSTRAP", "false")
	return path
}

func testConfigOverridesFromProcess() map[string]string {
	overrides := map[string]string{}
	raw := strings.TrimSpace(os.Getenv(testConfigOverridesEnv))
	if raw == "" {
		return overrides
	}
	if err := json.Unmarshal([]byte(raw), &overrides); err != nil {
		panic(fmt.Sprintf("parse %s: %v", testConfigOverridesEnv, err))
	}
	return overrides
}

func setTestConfigOverride(t *testing.T, path, value string) {
	t.Helper()
	overrides := testConfigOverridesFromProcess()
	overrides[path] = value
	raw, err := json.Marshal(overrides)
	if err != nil {
		t.Fatalf("marshal test config overrides: %v", err)
	}
	t.Setenv(testConfigOverridesEnv, string(raw))
}

func testConfigSource(t *testing.T, overrideSets ...map[string]string) string {
	t.Helper()
	templatePath := filepath.Join("..", "..", "config", "orrery.development.yaml")
	body, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("read test config template: %v", err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(body, &document); err != nil {
		t.Fatalf("parse test config template: %v", err)
	}
	setTestConfigPath(t, document, "paths.base_dir", ".")
	setTestConfigPath(t, document, "server.trusted_viewer_headers_enabled", true)
	setTestConfigPath(t, document, "database.password_env", "")
	setTestDatabaseURL(t, document, "memory://"+t.Name())
	setTestConfigPath(t, document, "bootstrap.pageindex.api_url", "")
	setTestConfigPath(t, document, "bootstrap.pageindex.api_key_ref", "")
	setTestConfigPath(t, document, "auth.local.enabled", false)
	setTestConfigPath(t, document, "auth.oidc.audience", "")
	for _, set := range overrideSets {
		for path, value := range set {
			applyTestConfigOverride(t, document, path, value)
		}
	}
	encoded, err := yaml.Marshal(document)
	if err != nil {
		t.Fatalf("marshal test config: %v", err)
	}
	path := filepath.Join(t.TempDir(), "orrery.test.yaml")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	return path
}

func applyTestConfigOverride(t *testing.T, document map[string]any, path, value string) {
	t.Helper()
	switch path {
	case "database.default_url":
		setTestDatabaseURL(t, document, value)
		return
	case "database.urls":
		for _, entry := range strings.Split(value, ",") {
			role, databaseURL, ok := strings.Cut(strings.TrimSpace(entry), "=")
			if !ok {
				t.Fatalf("invalid test database URL entry %q", entry)
			}
			setTestConfigPath(t, document, "database.urls."+strings.TrimSpace(role), strings.TrimSpace(databaseURL))
		}
		return
	case "discussion.agent.runtime_config":
		var decoded any
		if err := json.Unmarshal([]byte(value), &decoded); err != nil {
			decoded = value
		}
		setTestConfigPath(t, document, path, decoded)
		return
	case "auth.identity.mode":
		setTestConfigPath(t, document, path, value)
		return
	case "ORRERY_DATABASE_PASSWORD", "ORRERY_AUTH_LOCAL_MFA_TOTP_SECRET", "ORRERY_FEISHU_APP_SECRET", "ORRERY_FEISHU_STATE_SECRET", "ORRERY_OIDC_CLIENT_SECRET", "ORRERY_KRATOS_ADMIN_TOKEN", "ORRERY_HYDRA_ADMIN_TOKEN", "ORRERY_AUTHZ_OPENFGA_TOKEN":
		return
	}
	current, ok := lookupTestConfigPath(document, path)
	if !ok {
		t.Fatalf("unsupported test YAML override %s", path)
	}
	switch current.(type) {
	case bool:
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		setTestConfigPath(t, document, path, parsed)
	case int:
		parsed, err := strconv.Atoi(value)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		setTestConfigPath(t, document, path, parsed)
	default:
		setTestConfigPath(t, document, path, value)
	}
}

func setTestDatabaseURL(t *testing.T, document map[string]any, value string) {
	t.Helper()
	setTestConfigPath(t, document, "database.default_url", value)
	for _, role := range []string{"core", "audit", "events", "jobs", "runtime", "observability"} {
		setTestConfigPath(t, document, "database.urls."+role, value)
	}
}

func setTestConfigPath(t *testing.T, document map[string]any, path string, value any) {
	t.Helper()
	parts := strings.Split(path, ".")
	node := document
	for _, part := range parts[:len(parts)-1] {
		child, ok := node[part].(map[string]any)
		if !ok {
			t.Fatalf("test config path %s does not exist", path)
		}
		node = child
	}
	node[parts[len(parts)-1]] = value
}

func lookupTestConfigPath(document map[string]any, path string) (any, bool) {
	var current any = document
	for _, part := range strings.Split(path, ".") {
		node, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = node[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}
