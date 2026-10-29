package config

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	platformdb "orrery_v2/internal/platform/db"
)

func TestLoadOrDefaultCreatesGatewayDefaults(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "config", "gateway.yaml"))

	record, err := store.LoadOrDefault()
	if err != nil {
		t.Fatalf("LoadOrDefault failed: %v", err)
	}

	if record.Config.Gateway.Port != 18789 {
		t.Fatalf("port = %d, want 18789", record.Config.Gateway.Port)
	}
	if record.Config.Gateway.BindAddress != "127.0.0.1" {
		t.Fatalf("bind address = %q", record.Config.Gateway.BindAddress)
	}
	if record.Config.Adapters.Feishu.DefaultSessionLabel != "general" {
		t.Fatalf("feishu default label = %q", record.Config.Adapters.Feishu.DefaultSessionLabel)
	}
	if _, err := os.Stat(filepath.Join(dir, "config", "gateway.yaml")); err != nil {
		t.Fatalf("expected yaml file to be created: %v", err)
	}
}

func TestSaveRejectsUnsupportedBindAddress(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "gateway.yaml"))
	cfg := DefaultConfig()
	cfg.Gateway.BindAddress = "0.0.0.0"

	if _, err := store.Save(cfg); err == nil {
		t.Fatal("Save should reject non-local bind addresses")
	}
}

func TestSaveMigratesLegacyFeishuWebhookModeToStream(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "gateway.yaml"))
	cfg := DefaultConfig()
	cfg.Adapters.Feishu.ConnectionMode = "webhook"

	record, err := store.Save(cfg)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	if record.Config.Adapters.Feishu.ConnectionMode != "stream" {
		t.Fatalf("connection mode = %q, want stream", record.Config.Adapters.Feishu.ConnectionMode)
	}
	if strings.Contains(record.RawYAML, "connectionMode: webhook") {
		t.Fatalf("legacy webhook mode persisted:\n%s", record.RawYAML)
	}
}

func TestSaveRestrictsGatewayConfigFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.yaml")
	store := NewStore(path)

	if _, err := store.Save(DefaultConfig()); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose ACLs through os.FileMode permission bits")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("config file mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestSaveRawRejectsUnknownFieldsAndMultipleDocuments(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "gateway.yaml"))
	for _, raw := range []string{
		"gateway:\n  port: 18789\n  bindAddress: 127.0.0.1\n  typoField: true\n",
		"gateway:\n  port: 18789\n  bindAddress: 127.0.0.1\n---\nadapters: {}\n",
	} {
		if _, err := store.SaveRaw(raw); err == nil {
			t.Fatalf("SaveRaw accepted ambiguous config:\n%s", raw)
		}
	}
}

func TestLoadOrDefaultRejectsUnknownFieldsAndMultipleDocuments(t *testing.T) {
	for _, raw := range []string{
		"gateway:\n  port: 18789\n  bindAddress: 127.0.0.1\n  typoField: true\n",
		"gateway:\n  port: 18789\n  bindAddress: 127.0.0.1\n---\nadapters: {}\n",
	} {
		path := filepath.Join(t.TempDir(), "gateway.yaml")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatalf("write config: %v", err)
		}
		if _, err := NewStore(path).LoadOrDefault(); err == nil {
			t.Fatalf("LoadOrDefault accepted ambiguous persisted config:\n%s", raw)
		}
	}
}

func TestLegacyDatabaseImportRejectsUnknownFieldsAndMultipleDocuments(t *testing.T) {
	for _, raw := range []string{
		"gateway:\n  port: 18789\n  bindAddress: 127.0.0.1\n  typoField: true\n",
		"gateway:\n  port: 18789\n  bindAddress: 127.0.0.1\n---\nadapters: {}\n",
	} {
		path := filepath.Join(t.TempDir(), "gateway.yaml")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatalf("write legacy config: %v", err)
		}
		if _, found, err := loadLegacyConfigIfExists(path); err == nil || found {
			t.Fatalf("legacy database import accepted ambiguous config: found=%v err=%v\n%s", found, err, raw)
		}
	}
}

func TestSaveEncryptsGatewaySecretsAtRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.yaml")
	store := NewStore(path)
	cfg := DefaultConfig()
	cfg.Adapters.Weixin.Extra = map[string]any{
		"weixinAccounts": map[string]any{
			"wx-user": map[string]any{"token": "account-token"},
		},
	}

	if _, err := store.Save(cfg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(raw) == "" || strings.Contains(string(raw), "account-token") {
		t.Fatalf("raw config leaked plaintext secret: %s", string(raw))
	}
	if _, err := os.Stat(path + ".key"); err != nil {
		t.Fatalf("expected gateway secret key file: %v", err)
	}
	loaded, err := store.LoadOrDefault()
	if err != nil {
		t.Fatalf("LoadOrDefault failed: %v", err)
	}
	accounts := loaded.Config.Adapters.Weixin.Extra["weixinAccounts"].(map[string]any)
	account := accounts["wx-user"].(map[string]any)
	if account["token"] != "account-token" {
		t.Fatalf("decrypted account = %+v", account)
	}
}

func TestLoadMigratesPlaintextSecretsAtRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.yaml")
	if err := os.WriteFile(path, []byte(`
gateway:
    port: 18789
    bindAddress: 127.0.0.1
adapters:
    weixin:
        enabled: true
        token: plaintext-weixin-token
        extra:
            weixinAccounts:
                wx-user:
                    token: plaintext-account-token
`), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	store := NewStore(path)
	record, err := store.LoadOrDefault()
	if err != nil {
		t.Fatalf("LoadOrDefault failed: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migrated config: %v", err)
	}
	text := string(raw)
	if strings.Contains(text, "plaintext-weixin-token") || strings.Contains(text, "plaintext-account-token") {
		t.Fatalf("migrated config leaked plaintext secret: %s", text)
	}
	if !strings.Contains(text, encryptedSecretPrefix) {
		t.Fatalf("migrated config did not encrypt secrets: %s", text)
	}
	var persisted struct {
		Adapters struct {
			Weixin map[string]any `yaml:"weixin"`
		} `yaml:"adapters"`
	}
	if err := yaml.Unmarshal(raw, &persisted); err != nil {
		t.Fatalf("decode migrated config: %v", err)
	}
	for _, key := range []string{"token", "apiKey", "webhookUrl"} {
		if _, exists := persisted.Adapters.Weixin[key]; exists {
			t.Fatalf("legacy global Weixin field %q was retained: %s", key, text)
		}
	}
	accounts := record.Config.Adapters.Weixin.Extra["weixinAccounts"].(map[string]any)
	if account := accounts["wx-user"].(map[string]any); account["token"] != "plaintext-account-token" {
		t.Fatalf("personal account token was not retained: %+v", account)
	}
}

func TestLoadDeletesLegacyWeixinBridgeToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.yaml")
	if err := os.WriteFile(path, []byte(`
gateway:
    port: 18789
    bindAddress: 127.0.0.1
adapters:
    weixin:
        enabled: true
        extra:
            bridgeToken: legacy-bridge-secret
`), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	store := NewStore(path)
	record, err := store.LoadOrDefault()
	if err != nil {
		t.Fatalf("LoadOrDefault failed: %v", err)
	}
	if _, exists := record.Config.Adapters.Weixin.Extra["bridgeToken"]; exists {
		t.Fatalf("legacy bridgeToken remains in normalized config: %+v", record.Config.Adapters.Weixin.Extra)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migrated config: %v", err)
	}
	if strings.Contains(string(raw), "legacy-bridge-secret") || strings.Contains(string(raw), "bridgeToken") {
		t.Fatalf("legacy global secret was not deleted: %s", raw)
	}
}

func TestMaskedRecordDropsLegacyWeixinSecretAliases(t *testing.T) {
	record := Record{Config: DefaultConfig()}
	record.Config.Adapters.Weixin.Extra = map[string]any{
		"bridgeToken":         "legacy-secret",
		"openclawBridgeToken": "other-secret",
	}

	masked := record.Masked()
	if _, exists := masked.Config.Adapters.Weixin.Extra["bridgeToken"]; exists {
		t.Fatalf("masked config exposed bridgeToken: %+v", masked.Config.Adapters.Weixin.Extra)
	}
	if _, exists := masked.Config.Adapters.Weixin.Extra["openclawBridgeToken"]; exists {
		t.Fatalf("masked config exposed openclawBridgeToken: %+v", masked.Config.Adapters.Weixin.Extra)
	}
	if strings.Contains(masked.RawYAML, "legacy-secret") || strings.Contains(masked.RawYAML, "other-secret") {
		t.Fatalf("masked YAML exposed legacy secret aliases: %s", masked.RawYAML)
	}
}

func TestMaskedRecordHidesSecrets(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "gateway.yaml"))
	cfg := DefaultConfig()
	cfg.Adapters.Email.Enabled = true
	cfg.Adapters.Email.Token = "secret-token"

	record, err := store.Save(cfg)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	masked := record.Masked()
	if masked.Config.Adapters.Email.Token == "secret-token" {
		t.Fatal("masked token leaked secret")
	}
	if masked.Config.Adapters.Email.Token == "" {
		t.Fatal("masked token should preserve that a value exists")
	}
}

func TestMergeMaskedConfigPreservesExistingSecrets(t *testing.T) {
	existing := DefaultConfig()
	existing.Adapters.Feishu.AppSecret = "feishu-secret"
	existing.Adapters.Weixin.Extra = map[string]any{
		"weixinAccounts": map[string]any{
			"user-1@im.wechat": map[string]any{
				"userId": "user-1@im.wechat",
				"token":  "account-token",
			},
		},
	}

	next := existing
	next.Adapters.Feishu.AppSecret = MaskValue
	next.Adapters.Weixin.Extra = map[string]any{
		"pollIntervalSeconds": 5,
		"agentBindings":       map[string]any{"wx-user": "staffing-agent"},
	}

	merged := MergeMaskedConfig(next, existing)
	if merged.Adapters.Feishu.AppSecret != "feishu-secret" {
		t.Fatalf("feishu secret = %q", merged.Adapters.Feishu.AppSecret)
	}
	if _, ok := merged.Adapters.Weixin.Extra["pollIntervalSeconds"]; !ok {
		t.Fatalf("non-secret changes were not preserved: %+v", merged.Adapters.Weixin.Extra)
	}
	if _, ok := merged.Adapters.Weixin.Extra["agentBindings"]; ok {
		t.Fatalf("private bindings should not be accepted from masked admin updates: %+v", merged.Adapters.Weixin.Extra)
	}

	masked := MaskConfig(existing)
	accounts := masked.Adapters.Weixin.Extra["weixinAccounts"].(map[string]any)
	account := accounts["user-1@im.wechat"].(map[string]any)
	if account["token"] != MaskValue {
		t.Fatalf("nested weixin token not masked: %+v", account)
	}
	next = masked
	merged = MergeMaskedConfig(next, existing)
	accounts = merged.Adapters.Weixin.Extra["weixinAccounts"].(map[string]any)
	account = accounts["user-1@im.wechat"].(map[string]any)
	if account["token"] != "account-token" {
		t.Fatalf("nested weixin token not merged: %+v", account)
	}
}

func TestNormalizeMigratesDingTalkClientCredentialsOutOfExtra(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Adapters.DingTalk.Extra = map[string]any{
		"clientId": "client-id", "clientSecret": "client-secret", "robotCode": "robot-code",
	}
	normalize(&cfg)
	if cfg.Adapters.DingTalk.APIKey != "client-id" || cfg.Adapters.DingTalk.Token != "client-secret" {
		t.Fatalf("dingtalk = %+v", cfg.Adapters.DingTalk)
	}
	if _, ok := cfg.Adapters.DingTalk.Extra["clientSecret"]; ok {
		t.Fatal("legacy clientSecret remained in extra")
	}
	if cfg.Adapters.DingTalk.Extra["robotCode"] != "robot-code" {
		t.Fatalf("robotCode = %#v", cfg.Adapters.DingTalk.Extra["robotCode"])
	}
}

func TestDatabaseStorePersistsStructuredGatewayConfig(t *testing.T) {
	core := openGatewayConfigTestDB(t)
	store := NewDatabaseStore(core, DatabaseStoreOptions{
		SecretKeyPath: filepath.Join(t.TempDir(), "gateway-config-key"),
	})
	cfg := DefaultConfig()
	cfg.Gateway.Port = 18791
	cfg.Adapters.Email.Enabled = true
	cfg.Adapters.Email.Token = "email-secret"
	cfg.Adapters.Weixin.Enabled = true
	cfg.Adapters.Weixin.Extra = map[string]any{
		"pollIntervalSeconds":       3,
		"agentBindings":             map[string]any{"user-1@im.wechat": "staffing-agent"},
		"activeAgentBindings":       map[string]any{"user-1@im.wechat": "qa-agent"},
		"personalAssistantBindings": map[string]any{"user-1@im.wechat": "account-alice"},
		"weixinUserBindings":        map[string]any{"user-1@im.wechat": "human-alice"},
		"weixinAccounts": map[string]any{
			"user-1@im.wechat": map[string]any{
				"userId":       "user-1@im.wechat",
				"botId":        "bot-1@im.bot",
				"token":        "account-token",
				"ilinkBaseUrl": "https://ilink.example.test/ilink/bot",
			},
		},
	}

	if _, err := store.Save(cfg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := store.LoadOrDefault()
	if err != nil {
		t.Fatalf("LoadOrDefault failed: %v", err)
	}
	if loaded.Path != databaseRecordPath {
		t.Fatalf("record path = %q", loaded.Path)
	}
	if loaded.Config.Gateway.Port != 18791 || !loaded.Config.Adapters.Email.Enabled {
		t.Fatalf("loaded config = %+v", loaded.Config)
	}
	if loaded.Config.Adapters.Email.Token != "email-secret" {
		t.Fatalf("loaded email credential is invalid: %q", loaded.Config.Adapters.Email.Token)
	}
	extra := loaded.Config.Adapters.Weixin.Extra
	if extra["pollIntervalSeconds"] != 3 {
		t.Fatalf("typed extra not loaded: %+v", extra)
	}
	agentBindings := extra["agentBindings"].(map[string]any)
	activeBindings := extra["activeAgentBindings"].(map[string]any)
	personalAssistantBindings := extra["personalAssistantBindings"].(map[string]any)
	userBindings := extra["weixinUserBindings"].(map[string]any)
	bindingKey := WeixinBindingKey(DefaultTenantID, "user-1@im.wechat")
	if agentBindings[bindingKey] != "staffing-agent" || activeBindings[bindingKey] != "qa-agent" || personalAssistantBindings[bindingKey] != "account-alice" || userBindings[bindingKey] != "human-alice" {
		t.Fatalf("private bindings not loaded: %+v", extra)
	}
	accounts := extra["weixinAccounts"].(map[string]any)
	account := accounts[bindingKey].(map[string]any)
	if account["token"] != "account-token" || account["botId"] != "bot-1@im.bot" {
		t.Fatalf("weixin account not loaded: %+v", account)
	}

	var storedToken string
	if err := core.QueryRow(context.Background(), `SELECT token FROM gateway_adapters WHERE adapter_key = ?`, "weixin").Scan(&storedToken); err != nil {
		t.Fatalf("query stored weixin token: %v", err)
	}
	if storedToken != "" {
		t.Fatalf("database retained global Weixin token: %q", storedToken)
	}
	if err := core.QueryRow(context.Background(), `SELECT token FROM gateway_weixin_accounts WHERE weixin_user_id = ?`, "user-1@im.wechat").Scan(&storedToken); err != nil {
		t.Fatalf("query stored account token: %v", err)
	}
	if strings.Contains(storedToken, "account-token") || !strings.HasPrefix(storedToken, encryptedSecretPrefix) {
		t.Fatalf("stored weixin account token is not encrypted: %q", storedToken)
	}
	var protectedExtraRows int
	if err := core.QueryRow(context.Background(), `SELECT COUNT(*) FROM gateway_adapter_extra WHERE extra_key IN ('agentBindings', 'activeAgentBindings', 'personalAssistantBindings', 'weixinUserBindings', 'weixinAccounts')`).Scan(&protectedExtraRows); err != nil {
		t.Fatalf("query protected extras: %v", err)
	}
	if protectedExtraRows != 0 {
		t.Fatalf("private weixin state should not be stored as adapter extra rows: %d", protectedExtraRows)
	}
	var platformRecords int
	if err := core.QueryRow(context.Background(), `SELECT COUNT(*) FROM platform_records WHERE table_name LIKE '%gateway%'`).Scan(&platformRecords); err != nil {
		t.Fatalf("query platform records: %v", err)
	}
	if platformRecords != 0 {
		t.Fatalf("gateway config should not be stored as opaque platform_records JSON rows: %d", platformRecords)
	}
}

func TestDatabaseStorePersistsMultipleFeishuAccounts(t *testing.T) {
	core := openGatewayConfigTestDB(t)
	store := NewDatabaseStore(core, DatabaseStoreOptions{
		SecretKeyPath: filepath.Join(t.TempDir(), "gateway-config-key"),
	})
	cfg := DefaultConfig()
	cfg.Adapters.Feishu.Enabled = true
	cfg.Adapters.Feishu.Accounts = map[string]FeishuAccountConfig{
		"main": {
			Enabled: true, AppID: "main-app", AppSecret: "main-secret",
			ConnectionMode: "outbound", DomainName: "feishu",
		},
		"jordan-belfort": {
			Enabled: true, AppID: "jordan-app", AppSecret: "jordan-secret",
			ConnectionMode: "outbound", DomainName: "feishu",
		},
	}

	saved, err := store.Save(cfg)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	if got := saved.Config.Adapters.Feishu.Accounts["main"].AppSecret; got != "main-secret" {
		t.Fatalf("saved runtime account secret = %q, want plaintext", got)
	}
	if got := cfg.Adapters.Feishu.Accounts["main"].AppSecret; got != "main-secret" {
		t.Fatalf("Save mutated input account secret = %q", got)
	}
	loaded, err := store.LoadOrDefault()
	if err != nil {
		t.Fatalf("LoadOrDefault failed: %v", err)
	}
	if got := loaded.Config.Adapters.Feishu.Accounts["main"]; got.AppID != "main-app" || got.AppSecret != "main-secret" || got.ConnectionMode != "outbound" {
		t.Fatalf("main account = %+v", got)
	}
	if got := loaded.Config.Adapters.Feishu.Accounts["jordan-belfort"]; got.AppID != "jordan-app" || got.AppSecret != "jordan-secret" {
		t.Fatalf("jordan account = %+v", got)
	}
	var storedSecret string
	if err := core.QueryRow(context.Background(), `SELECT app_secret FROM gateway_adapters WHERE adapter_key = ?`, "feishu:main").Scan(&storedSecret); err != nil {
		t.Fatalf("query stored Feishu account secret: %v", err)
	}
	if strings.Contains(storedSecret, "main-secret") || !strings.HasPrefix(storedSecret, encryptedSecretPrefix) {
		t.Fatalf("stored Feishu account secret is not encrypted: %q", storedSecret)
	}
}

func TestNormalizePreservesFeishuOutboundMode(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Adapters.Feishu.Enabled = true
	cfg.Adapters.Feishu.Accounts = map[string]FeishuAccountConfig{
		"main": {Enabled: true, AppID: "app", AppSecret: "secret", ConnectionMode: "outbound"},
	}
	normalize(&cfg)
	if got := cfg.Adapters.Feishu.Accounts["main"].ConnectionMode; got != "outbound" {
		t.Fatalf("connection mode = %q, want outbound", got)
	}
}

func TestDatabaseStoreDeletesLegacyGlobalWeixinCredentials(t *testing.T) {
	core := openGatewayConfigTestDB(t)
	store := NewDatabaseStore(core, DatabaseStoreOptions{SecretKeyPath: filepath.Join(t.TempDir(), "gateway-config-key")})
	if _, err := store.Save(DefaultConfig()); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	if _, err := core.Exec(context.Background(), `
UPDATE gateway_adapters
SET token = ?, api_key = ?, webhook_url = ?
WHERE adapter_key = ?`, "legacy-token", "legacy-api-key", "https://example.test/legacy", "weixin"); err != nil {
		t.Fatalf("seed legacy Weixin credentials: %v", err)
	}

	if _, err := store.LoadOrDefault(); err != nil {
		t.Fatalf("LoadOrDefault failed: %v", err)
	}
	var token, apiKey, webhookURL string
	if err := core.QueryRow(context.Background(), `
SELECT token, api_key, webhook_url
FROM gateway_adapters
WHERE adapter_key = ?`, "weixin").Scan(&token, &apiKey, &webhookURL); err != nil {
		t.Fatalf("query migrated Weixin adapter: %v", err)
	}
	if token != "" || apiKey != "" || webhookURL != "" {
		t.Fatalf("legacy global Weixin credentials remain: token=%q api_key=%q webhook_url=%q", token, apiKey, webhookURL)
	}
}

func TestDatabaseStoreCanRequireProvisionedEncryptionKey(t *testing.T) {
	core := openGatewayConfigTestDB(t)
	keyPath := filepath.Join(t.TempDir(), "gateway-config-key")
	store := NewDatabaseStore(core, DatabaseStoreOptions{
		SecretKeyPath:            keyPath,
		RequireExistingSecretKey: true,
	})

	if err := store.ValidateEncryptionKey(); err == nil {
		t.Fatal("ValidateEncryptionKey accepted a missing production key")
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatalf("production key validation created a local key: %v", err)
	}

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	if err := os.WriteFile(keyPath, []byte(base64.StdEncoding.EncodeToString(key)+"\n"), 0o600); err != nil {
		t.Fatalf("write provisioned key: %v", err)
	}
	if err := store.ValidateEncryptionKey(); err != nil {
		t.Fatalf("ValidateEncryptionKey rejected provisioned key: %v", err)
	}
}

func TestDatabaseStoreAcceptsInjectedEncryptionKeyValue(t *testing.T) {
	core := openGatewayConfigTestDB(t)
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(32 - i)
	}
	store := NewDatabaseStore(core, DatabaseStoreOptions{
		SecretKey:                base64.StdEncoding.EncodeToString(key),
		SecretKeyPath:            filepath.Join(t.TempDir(), "must-not-be-created"),
		RequireExistingSecretKey: true,
	})

	if err := store.ValidateEncryptionKey(); err != nil {
		t.Fatalf("ValidateEncryptionKey rejected injected key: %v", err)
	}
	if _, err := store.Save(DefaultConfig()); err != nil {
		t.Fatalf("Save with injected key: %v", err)
	}
}

func TestDatabaseStoreImportsLegacyYAMLIntoStructuredTables(t *testing.T) {
	core := openGatewayConfigTestDB(t)
	path := filepath.Join(t.TempDir(), "gateway.yaml")
	if err := os.WriteFile(path, []byte(`
gateway:
    port: 18793
    bindAddress: 127.0.0.1
adapters:
    weixin:
        enabled: true
        token: plaintext-weixin-token
        extra:
            mode: ilink_bot_api
            agentBindings:
                user-1@im.wechat: staffing-agent
            weixinUserBindings:
                user-1@im.wechat: human-alice
            weixinAccounts:
                user-1@im.wechat:
                    userId: user-1@im.wechat
                    botId: bot-1@im.bot
                    token: plaintext-account-token
`), 0600); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}
	store := NewDatabaseStore(core, DatabaseStoreOptions{
		LegacyPath:    path,
		SecretKeyPath: filepath.Join(t.TempDir(), "gateway-config-key"),
	})

	record, err := store.LoadOrDefault()
	if err != nil {
		t.Fatalf("LoadOrDefault failed: %v", err)
	}
	if record.Config.Gateway.Port != 18793 {
		t.Fatalf("legacy config not imported: %+v", record.Config)
	}
	var defaultAgentID, humanID, storedToken string
	if err := core.QueryRow(context.Background(), `SELECT default_agent_id FROM gateway_weixin_agent_bindings WHERE weixin_user_id = ?`, "user-1@im.wechat").Scan(&defaultAgentID); err != nil {
		t.Fatalf("query imported agent binding: %v", err)
	}
	if err := core.QueryRow(context.Background(), `SELECT human_profile_id FROM gateway_weixin_user_bindings WHERE weixin_user_id = ?`, "user-1@im.wechat").Scan(&humanID); err != nil {
		t.Fatalf("query imported user binding: %v", err)
	}
	if err := core.QueryRow(context.Background(), `SELECT token FROM gateway_weixin_accounts WHERE weixin_user_id = ?`, "user-1@im.wechat").Scan(&storedToken); err != nil {
		t.Fatalf("query imported account: %v", err)
	}
	if defaultAgentID != "staffing-agent" || humanID != "human-alice" {
		t.Fatalf("imported bindings = agent:%q human:%q", defaultAgentID, humanID)
	}
	if strings.Contains(storedToken, "plaintext-account-token") || !strings.HasPrefix(storedToken, encryptedSecretPrefix) {
		t.Fatalf("imported account token was not encrypted in DB: %q", storedToken)
	}
}

func TestDatabaseStoreIsolatesSameWeixinUserAcrossTenants(t *testing.T) {
	core := openGatewayConfigTestDB(t)
	store := NewDatabaseStore(core, DatabaseStoreOptions{SecretKeyPath: filepath.Join(t.TempDir(), "gateway-config-key")})
	tenantAKey := WeixinBindingKey("tenant-a", "shared-user")
	tenantBKey := WeixinBindingKey("tenant-b", "shared-user")
	cfg := DefaultConfig()
	cfg.Adapters.Weixin.Extra = map[string]any{
		"agentBindings":      map[string]any{tenantAKey: "agent-a", tenantBKey: "agent-b"},
		"weixinUserBindings": map[string]any{tenantAKey: "human-a", tenantBKey: "human-b"},
		"weixinAccounts": map[string]any{
			tenantAKey: map[string]any{"tenantId": "tenant-a", "userId": "shared-user", "botId": "bot-a", "token": "token-a"},
			tenantBKey: map[string]any{"tenantId": "tenant-b", "userId": "shared-user", "botId": "bot-b", "token": "token-b"},
		},
	}
	if _, err := store.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.LoadOrDefault()
	if err != nil {
		t.Fatalf("LoadOrDefault: %v", err)
	}
	users := loaded.Config.Adapters.Weixin.Extra["weixinUserBindings"].(map[string]any)
	agents := loaded.Config.Adapters.Weixin.Extra["agentBindings"].(map[string]any)
	accounts := loaded.Config.Adapters.Weixin.Extra["weixinAccounts"].(map[string]any)
	if users[tenantAKey] != "human-a" || users[tenantBKey] != "human-b" || agents[tenantAKey] != "agent-a" || agents[tenantBKey] != "agent-b" {
		t.Fatalf("tenant bindings crossed: users=%+v agents=%+v", users, agents)
	}
	if accounts[tenantAKey].(map[string]any)["token"] != "token-a" || accounts[tenantBKey].(map[string]any)["token"] != "token-b" {
		t.Fatalf("tenant accounts crossed: %+v", accounts)
	}
	var accountRows, userRows, agentRows int
	if err := core.QueryRow(context.Background(), `SELECT COUNT(*) FROM gateway_weixin_accounts WHERE weixin_user_id = ?`, "shared-user").Scan(&accountRows); err != nil {
		t.Fatalf("count account rows: %v", err)
	}
	if err := core.QueryRow(context.Background(), `SELECT COUNT(*) FROM gateway_weixin_user_bindings WHERE weixin_user_id = ?`, "shared-user").Scan(&userRows); err != nil {
		t.Fatalf("count user rows: %v", err)
	}
	if err := core.QueryRow(context.Background(), `SELECT COUNT(*) FROM gateway_weixin_agent_bindings WHERE weixin_user_id = ?`, "shared-user").Scan(&agentRows); err != nil {
		t.Fatalf("count agent rows: %v", err)
	}
	if accountRows != 2 || userRows != 2 || agentRows != 2 {
		t.Fatalf("tenant rows accounts=%d users=%d agents=%d", accountRows, userRows, agentRows)
	}
}

func TestDatabaseStoreReassignsWeixinTenant(t *testing.T) {
	core := openGatewayConfigTestDB(t)
	store := NewDatabaseStore(core, DatabaseStoreOptions{SecretKeyPath: filepath.Join(t.TempDir(), "gateway-config-key")})
	oldTenant := "org-system-runtime-support"
	newTenant := "minvia"
	userID := "user-1@im.wechat"
	oldKey := WeixinBindingKey(oldTenant, userID)
	newKey := WeixinBindingKey(newTenant, userID)
	cfg := DefaultConfig()
	cfg.Adapters.Weixin.Extra = map[string]any{
		"agentBindings":             map[string]any{oldKey: "agent-a"},
		"activeAgentBindings":       map[string]any{oldKey: "agent-b"},
		"personalAssistantBindings": map[string]any{oldKey: "owner-a"},
		"weixinUserBindings":        map[string]any{oldKey: "human-a"},
		"weixinAccounts": map[string]any{
			oldKey: map[string]any{"tenantId": oldTenant, "userId": userID, "botId": "bot-a", "token": "token-a"},
		},
	}
	if _, err := store.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	changed, err := store.ReassignWeixinTenant(oldTenant, newTenant)
	if err != nil {
		t.Fatalf("ReassignWeixinTenant: %v", err)
	}
	if changed != 5 {
		t.Fatalf("changed = %d, want 5", changed)
	}
	if changed, err = store.ReassignWeixinTenant(oldTenant, newTenant); err != nil || changed != 0 {
		t.Fatalf("idempotent ReassignWeixinTenant = %d, %v", changed, err)
	}

	loaded, err := store.LoadOrDefault()
	if err != nil {
		t.Fatalf("LoadOrDefault: %v", err)
	}
	extra := loaded.Config.Adapters.Weixin.Extra
	for _, extraKey := range []string{"agentBindings", "activeAgentBindings", "personalAssistantBindings", "weixinUserBindings"} {
		bindings := extra[extraKey].(map[string]any)
		if _, exists := bindings[oldKey]; exists || bindings[newKey] == nil {
			t.Fatalf("%s was not reassigned: %+v", extraKey, bindings)
		}
	}
	accounts := extra["weixinAccounts"].(map[string]any)
	account := accounts[newKey].(map[string]any)
	if _, exists := accounts[oldKey]; exists || account["tenantId"] != newTenant || account["token"] != "token-a" {
		t.Fatalf("account was not reassigned: %+v", accounts)
	}
	for _, table := range []string{"gateway_weixin_accounts", "gateway_weixin_user_bindings", "gateway_weixin_agent_bindings", "gateway_weixin_personal_assistant_bindings"} {
		var oldRows, newRows int
		if err := core.QueryRow(context.Background(), `SELECT COUNT(*) FROM `+table+` WHERE tenant_id = ?`, oldTenant).Scan(&oldRows); err != nil {
			t.Fatalf("count old %s rows: %v", table, err)
		}
		if err := core.QueryRow(context.Background(), `SELECT COUNT(*) FROM `+table+` WHERE tenant_id = ?`, newTenant).Scan(&newRows); err != nil {
			t.Fatalf("count new %s rows: %v", table, err)
		}
		if oldRows != 0 || newRows != 1 {
			t.Fatalf("%s rows old=%d new=%d", table, oldRows, newRows)
		}
	}
}

func openGatewayConfigTestDB(t *testing.T) *platformdb.Store {
	t.Helper()
	store, err := platformdb.Open(context.Background(), platformdb.Config{Driver: platformdb.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
