package config

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	platformdb "orrery_v2/internal/platform/db"
)

const (
	databaseGatewaySettingsID = "default"
	databaseRecordPath        = "database:core.gateway_settings"
)

var (
	platformKeys = []string{
		"dingtalk",
		"email",
	}
)

type DatabaseStoreOptions struct {
	LegacyPath               string
	SecretKeyPath            string
	SecretKey                string
	RequireExistingSecretKey bool
}

func NewDatabaseStore(store *platformdb.Store, opts DatabaseStoreOptions) *Store {
	legacyPath := strings.TrimSpace(opts.LegacyPath)
	secretKeyPath := strings.TrimSpace(opts.SecretKeyPath)
	if secretKeyPath == "" && legacyPath != "" {
		secretKeyPath = legacyPath + ".key"
	}
	return &Store{
		path:                     databaseRecordPath,
		database:                 store,
		legacyPath:               legacyPath,
		secretKeyFile:            secretKeyPath,
		secretKeyValue:           strings.TrimSpace(opts.SecretKey),
		requireExistingSecretKey: opts.RequireExistingSecretKey,
	}
}

func (s *Store) ensureDatabaseSchema(ctx context.Context) error {
	if s == nil || s.database == nil {
		return fmt.Errorf("gateway database store is not configured")
	}
	timeType := "INTEGER"
	if s.database.Dialect() == platformdb.DialectPostgres {
		timeType = "BIGINT"
	}
	for _, statement := range gatewayConfigMigrationStatements(timeType) {
		if _, err := s.database.Exec(ctx, statement); err != nil {
			return fmt.Errorf("ensure gateway config schema: %w", err)
		}
	}
	if err := s.database.QueryRow(ctx, `SELECT tenant_id FROM gateway_weixin_accounts LIMIT 1`).Scan(new(string)); err != nil && !errors.Is(err, sql.ErrNoRows) {
		if !isMissingGatewayTenantColumn(err) {
			return fmt.Errorf("inspect tenant-scoped gateway config schema: %w", err)
		}
		if err := s.database.WithTx(ctx, func(tx platformdb.Tx) error {
			for _, statement := range gatewayConfigTenantMigrationStatements(timeType) {
				if _, migrationErr := tx.ExecContext(ctx, statement); migrationErr != nil {
					return migrationErr
				}
			}
			return nil
		}); err != nil {
			return fmt.Errorf("ensure tenant-scoped gateway config schema: %w", err)
		}
	}
	for _, statement := range gatewayWeixinPersonalAssistantMigrationStatements(timeType) {
		if _, err := s.database.Exec(ctx, statement); err != nil {
			return fmt.Errorf("ensure gateway personal assistant bindings schema: %w", err)
		}
	}
	return nil
}

func isMissingGatewayTenantColumn(err error) bool {
	message := strings.ToLower(strings.TrimSpace(fmt.Sprint(err)))
	return strings.Contains(message, "no such column: tenant_id") ||
		(strings.Contains(message, "column") && strings.Contains(message, "tenant_id") && strings.Contains(message, "does not exist"))
}

func (s *Store) loadDatabaseOrDefault() (Record, error) {
	ctx := context.Background()
	if err := s.ensureDatabaseSchema(ctx); err != nil {
		return Record{}, err
	}
	configured, err := s.databaseConfigured(ctx)
	if err != nil {
		return Record{}, err
	}
	if !configured {
		cfg := DefaultConfig()
		if legacy, ok, err := loadLegacyConfigIfExists(strings.TrimSpace(s.legacyPath)); err != nil {
			return Record{}, err
		} else if ok {
			cfg = legacy
		}
		return s.saveDatabase(cfg)
	}
	cfg, obsoleteWeixinAdapterCredentials, err := s.readDatabaseConfig(ctx)
	if err != nil {
		return Record{}, err
	}
	plaintextSecrets := hasPlaintextSecrets(cfg)
	obsoleteWeixinGlobalConfig := obsoleteWeixinAdapterCredentials || hasObsoleteWeixinGlobalConfig(cfg)
	legacyDingTalkCredentials := hasLegacyDingTalkCredentials(cfg)
	if err := s.decryptSecrets(&cfg); err != nil {
		return Record{}, err
	}
	normalize(&cfg)
	if err := validate(cfg); err != nil {
		return Record{}, err
	}
	if plaintextSecrets || obsoleteWeixinGlobalConfig || legacyDingTalkCredentials {
		return s.saveDatabase(cfg)
	}
	return databaseRecord(cfg), nil
}

func (s *Store) saveDatabase(cfg Config) (Record, error) {
	ctx := context.Background()
	if err := s.ensureDatabaseSchema(ctx); err != nil {
		return Record{}, err
	}
	normalize(&cfg)
	if err := validate(cfg); err != nil {
		return Record{}, err
	}
	stored, err := s.encryptSecrets(cfg)
	if err != nil {
		return Record{}, err
	}
	now := time.Now().UTC().UnixMilli()
	if err := s.database.WithTx(ctx, func(tx platformdb.Tx) error {
		if err := saveGatewaySettingsRow(ctx, tx, stored, now); err != nil {
			return err
		}
		if err := clearGatewayConfigRows(ctx, tx); err != nil {
			return err
		}
		if err := saveFeishuAdapter(ctx, tx, stored.Adapters.Feishu, now); err != nil {
			return err
		}
		if err := saveFeishuAccounts(ctx, tx, stored.Adapters.Feishu.Accounts, now); err != nil {
			return err
		}
		if err := saveWeixinAdapter(ctx, tx, stored.Adapters.Weixin, now); err != nil {
			return err
		}
		if err := savePlatformAdapters(ctx, tx, stored, now); err != nil {
			return err
		}
		return saveWeixinPrivateState(ctx, tx, stored.Adapters.Weixin.Extra, now)
	}); err != nil {
		return Record{}, err
	}
	return databaseRecord(cfg), nil
}

func (s *Store) databaseConfigured(ctx context.Context) (bool, error) {
	var id string
	err := s.database.QueryRow(ctx, `SELECT id FROM gateway_settings WHERE id = ?`, databaseGatewaySettingsID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query gateway settings: %w", err)
	}
	return strings.TrimSpace(id) != "", nil
}

func (s *Store) readDatabaseConfig(ctx context.Context) (Config, bool, error) {
	cfg := DefaultConfig()
	if err := s.database.QueryRow(ctx, `
SELECT port, bind_address, idle_session_timeout_minutes, static_assets_path, max_per_session_mcp_instances
FROM gateway_settings
WHERE id = ?`, databaseGatewaySettingsID).Scan(
		&cfg.Gateway.Port,
		&cfg.Gateway.BindAddress,
		&cfg.Gateway.IdleSessionTimeoutMinutes,
		&cfg.Gateway.StaticAssetsPath,
		&cfg.Gateway.MaxPerSessionMCPInstances,
	); err != nil {
		return Config{}, false, fmt.Errorf("load gateway settings: %w", err)
	}
	obsoleteWeixinAdapterCredentials, err := s.readAdapterRows(ctx, &cfg)
	if err != nil {
		return Config{}, false, err
	}
	if err := s.readAdapterExtraRows(ctx, &cfg); err != nil {
		return Config{}, false, err
	}
	if err := s.readWeixinPrivateRows(ctx, &cfg); err != nil {
		return Config{}, false, err
	}
	return cfg, obsoleteWeixinAdapterCredentials, nil
}

func (s *Store) readAdapterRows(ctx context.Context, cfg *Config) (bool, error) {
	rows, err := s.database.Query(ctx, `
SELECT adapter_key, adapter_kind, enabled, token, api_key, webhook_url,
       app_id, app_secret, encrypt_key, verify_token, default_session_label, connection_mode, domain_name
FROM gateway_adapters
ORDER BY adapter_key`)
	if err != nil {
		return false, fmt.Errorf("load gateway adapters: %w", err)
	}
	defer rows.Close()
	obsoleteWeixinAdapterCredentials := false
	for rows.Next() {
		var row gatewayAdapterRow
		if err := rows.Scan(
			&row.AdapterKey,
			&row.AdapterKind,
			&row.Enabled,
			&row.Token,
			&row.APIKey,
			&row.WebhookURL,
			&row.AppID,
			&row.AppSecret,
			&row.EncryptKey,
			&row.VerifyToken,
			&row.DefaultSessionLabel,
			&row.ConnectionMode,
			&row.DomainName,
		); err != nil {
			return false, fmt.Errorf("scan gateway adapter: %w", err)
		}
		if applyGatewayAdapterRow(cfg, row) {
			obsoleteWeixinAdapterCredentials = true
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate gateway adapters: %w", err)
	}
	return obsoleteWeixinAdapterCredentials, nil
}

func (s *Store) readAdapterExtraRows(ctx context.Context, cfg *Config) error {
	rows, err := s.database.Query(ctx, `
SELECT adapter_key, extra_key, value_kind, string_value, int_value, real_value, bool_value, json_value
FROM gateway_adapter_extra
ORDER BY adapter_key, extra_key`)
	if err != nil {
		return fmt.Errorf("load gateway adapter extras: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var adapterKey, extraKey, kind, stringValue, jsonValue string
		var intValue sql.NullInt64
		var realValue sql.NullFloat64
		var boolValue sql.NullInt64
		if err := rows.Scan(&adapterKey, &extraKey, &kind, &stringValue, &intValue, &realValue, &boolValue, &jsonValue); err != nil {
			return fmt.Errorf("scan gateway adapter extra: %w", err)
		}
		extra := adapterExtraByKey(cfg, adapterKey)
		if extra == nil {
			continue
		}
		value, err := decodeDatabaseExtraValue(kind, stringValue, intValue, realValue, boolValue, jsonValue)
		if err != nil {
			return fmt.Errorf("decode gateway adapter extra %s.%s: %w", adapterKey, extraKey, err)
		}
		(*extra)[extraKey] = value
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate gateway adapter extras: %w", err)
	}
	return nil
}

func (s *Store) readWeixinPrivateRows(ctx context.Context, cfg *Config) error {
	extra := ensureDatabaseWeixinExtra(cfg)
	userBindings, err := readScopedStringBindingRows(ctx, s.database, `SELECT tenant_id, weixin_user_id, human_profile_id FROM gateway_weixin_user_bindings ORDER BY tenant_id, weixin_user_id`)
	if err != nil {
		return err
	}
	if len(userBindings) > 0 {
		extra["weixinUserBindings"] = userBindings
	}
	agentBindings := map[string]any{}
	activeBindings := map[string]any{}
	rows, err := s.database.Query(ctx, `SELECT tenant_id, weixin_user_id, default_agent_id, active_agent_id FROM gateway_weixin_agent_bindings ORDER BY tenant_id, weixin_user_id`)
	if err != nil {
		return fmt.Errorf("load weixin agent bindings: %w", err)
	}
	for rows.Next() {
		var tenantID, userID, defaultAgentID, activeAgentID string
		if err := rows.Scan(&tenantID, &userID, &defaultAgentID, &activeAgentID); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan weixin agent binding: %w", err)
		}
		userID = strings.TrimSpace(userID)
		if userID == "" {
			continue
		}
		bindingKey := WeixinBindingKey(tenantID, userID)
		if strings.TrimSpace(defaultAgentID) != "" {
			agentBindings[bindingKey] = strings.TrimSpace(defaultAgentID)
		}
		if strings.TrimSpace(activeAgentID) != "" {
			activeBindings[bindingKey] = strings.TrimSpace(activeAgentID)
		}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close weixin agent binding rows: %w", err)
	}
	if len(agentBindings) > 0 {
		extra["agentBindings"] = agentBindings
	}
	if len(activeBindings) > 0 {
		extra["activeAgentBindings"] = activeBindings
	}
	personalAssistantBindings, err := readScopedStringBindingRows(ctx, s.database, `SELECT tenant_id, weixin_user_id, owner_id FROM gateway_weixin_personal_assistant_bindings ORDER BY tenant_id, weixin_user_id`)
	if err != nil {
		return err
	}
	if len(personalAssistantBindings) > 0 {
		extra["personalAssistantBindings"] = personalAssistantBindings
	}
	accounts, err := readWeixinAccountRows(ctx, s.database)
	if err != nil {
		return err
	}
	if len(accounts) > 0 {
		extra["weixinAccounts"] = accounts
	}
	return nil
}

func saveGatewaySettingsRow(ctx context.Context, tx platformdb.Tx, cfg Config, now int64) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO gateway_settings (
	id, port, bind_address, idle_session_timeout_minutes, static_assets_path, max_per_session_mcp_instances, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	port = excluded.port,
	bind_address = excluded.bind_address,
	idle_session_timeout_minutes = excluded.idle_session_timeout_minutes,
	static_assets_path = excluded.static_assets_path,
	max_per_session_mcp_instances = excluded.max_per_session_mcp_instances,
	updated_at = excluded.updated_at`,
		databaseGatewaySettingsID,
		cfg.Gateway.Port,
		cfg.Gateway.BindAddress,
		cfg.Gateway.IdleSessionTimeoutMinutes,
		cfg.Gateway.StaticAssetsPath,
		cfg.Gateway.MaxPerSessionMCPInstances,
		now,
	)
	if err != nil {
		return fmt.Errorf("save gateway settings: %w", err)
	}
	return nil
}

func clearGatewayConfigRows(ctx context.Context, tx platformdb.Tx) error {
	for _, statement := range []string{
		`DELETE FROM gateway_adapters`,
		`DELETE FROM gateway_adapter_extra`,
		`DELETE FROM gateway_weixin_accounts`,
		`DELETE FROM gateway_weixin_user_bindings`,
		`DELETE FROM gateway_weixin_agent_bindings`,
		`DELETE FROM gateway_weixin_personal_assistant_bindings`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("clear gateway config rows: %w", err)
		}
	}
	return nil
}

func saveFeishuAdapter(ctx context.Context, tx platformdb.Tx, cfg FeishuAdapterConfig, now int64) error {
	return saveGatewayAdapterRow(ctx, tx, gatewayAdapterRow{
		AdapterKey:          "feishu",
		AdapterKind:         "feishu",
		Enabled:             boolInt(cfg.Enabled),
		AppID:               cfg.AppID,
		AppSecret:           cfg.AppSecret,
		EncryptKey:          cfg.EncryptKey,
		VerifyToken:         cfg.VerifyToken,
		DefaultSessionLabel: cfg.DefaultSessionLabel,
		ConnectionMode:      cfg.ConnectionMode,
		DomainName:          cfg.DomainName,
	}, now)
}

func saveFeishuAccounts(ctx context.Context, tx platformdb.Tx, accounts map[string]FeishuAccountConfig, now int64) error {
	keys := make([]string, 0, len(accounts))
	for accountID := range accounts {
		keys = append(keys, accountID)
	}
	sort.Strings(keys)
	for _, accountID := range keys {
		cfg := accounts[accountID]
		if err := saveGatewayAdapterRow(ctx, tx, gatewayAdapterRow{
			AdapterKey:          "feishu:" + accountID,
			AdapterKind:         "feishu_account",
			Enabled:             boolInt(cfg.Enabled),
			AppID:               cfg.AppID,
			AppSecret:           cfg.AppSecret,
			EncryptKey:          cfg.EncryptKey,
			VerifyToken:         cfg.VerifyToken,
			DefaultSessionLabel: cfg.DefaultSessionLabel,
			ConnectionMode:      cfg.ConnectionMode,
			DomainName:          cfg.DomainName,
		}, now); err != nil {
			return err
		}
	}
	return nil
}

func saveWeixinAdapter(ctx context.Context, tx platformdb.Tx, cfg WeixinAdapterConfig, now int64) error {
	if err := saveGatewayAdapterRow(ctx, tx, gatewayAdapterRow{
		AdapterKey:  "weixin",
		AdapterKind: "weixin",
		Enabled:     boolInt(cfg.Enabled),
	}, now); err != nil {
		return err
	}
	return saveAdapterExtra(ctx, tx, "weixin", cfg.Extra)
}

func savePlatformAdapters(ctx context.Context, tx platformdb.Tx, cfg Config, now int64) error {
	for _, key := range platformKeys {
		adapter := platformAdapterValueByKey(cfg, key)
		if err := saveGatewayAdapterRow(ctx, tx, gatewayAdapterRow{
			AdapterKey:  key,
			AdapterKind: "platform",
			Enabled:     boolInt(adapter.Enabled),
			Token:       adapter.Token,
			APIKey:      adapter.APIKey,
			WebhookURL:  adapter.WebhookURL,
		}, now); err != nil {
			return err
		}
		if err := saveAdapterExtra(ctx, tx, key, adapter.Extra); err != nil {
			return err
		}
	}
	return nil
}

func saveGatewayAdapterRow(ctx context.Context, tx platformdb.Tx, row gatewayAdapterRow, now int64) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO gateway_adapters (
	adapter_key, adapter_kind, enabled, token, api_key, webhook_url,
	app_id, app_secret, encrypt_key, verify_token, default_session_label, connection_mode, domain_name, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(adapter_key) DO UPDATE SET
	adapter_kind = excluded.adapter_kind,
	enabled = excluded.enabled,
	token = excluded.token,
	api_key = excluded.api_key,
	webhook_url = excluded.webhook_url,
	app_id = excluded.app_id,
	app_secret = excluded.app_secret,
	encrypt_key = excluded.encrypt_key,
	verify_token = excluded.verify_token,
	default_session_label = excluded.default_session_label,
	connection_mode = excluded.connection_mode,
	domain_name = excluded.domain_name,
	updated_at = excluded.updated_at`,
		row.AdapterKey,
		row.AdapterKind,
		row.Enabled,
		row.Token,
		row.APIKey,
		row.WebhookURL,
		row.AppID,
		row.AppSecret,
		row.EncryptKey,
		row.VerifyToken,
		row.DefaultSessionLabel,
		row.ConnectionMode,
		row.DomainName,
		now,
	)
	if err != nil {
		return fmt.Errorf("save gateway adapter %s: %w", row.AdapterKey, err)
	}
	return nil
}

func saveAdapterExtra(ctx context.Context, tx platformdb.Tx, adapterKey string, extra map[string]any) error {
	if len(extra) == 0 {
		return nil
	}
	keys := make([]string, 0, len(extra))
	for key := range extra {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if isPrivateWeixinExtraKey(key) || strings.HasPrefix(strings.TrimSpace(key), "_") {
			continue
		}
		value, err := encodeDatabaseExtraValue(extra[key])
		if err != nil {
			return fmt.Errorf("encode gateway adapter extra %s.%s: %w", adapterKey, key, err)
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO gateway_adapter_extra (
	adapter_key, extra_key, value_kind, string_value, int_value, real_value, bool_value, json_value
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			adapterKey,
			key,
			value.Kind,
			value.String,
			value.Int,
			value.Real,
			value.Bool,
			value.JSON,
		)
		if err != nil {
			return fmt.Errorf("save gateway adapter extra %s.%s: %w", adapterKey, key, err)
		}
	}
	return nil
}

func saveWeixinPrivateState(ctx context.Context, tx platformdb.Tx, extra map[string]any, now int64) error {
	if extra == nil {
		return nil
	}
	defaultAgents := stringBindingMap(extra["agentBindings"])
	activeAgents := stringBindingMap(extra["activeAgentBindings"])
	userIDs := make(map[string]struct{}, len(defaultAgents)+len(activeAgents))
	for userID := range defaultAgents {
		userIDs[userID] = struct{}{}
	}
	for userID := range activeAgents {
		userIDs[userID] = struct{}{}
	}
	sortedUserIDs := sortedStringKeys(userIDs)
	for _, userID := range sortedUserIDs {
		tenantID, scopedUserID, ok := ParseWeixinBindingKey(userID)
		if !ok {
			continue
		}
		defaultAgentID := strings.TrimSpace(defaultAgents[userID])
		activeAgentID := strings.TrimSpace(activeAgents[userID])
		if defaultAgentID == "" && activeAgentID == "" {
			continue
		}
		if defaultAgentID == "" {
			defaultAgentID = activeAgentID
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO gateway_weixin_agent_bindings (
	tenant_id, weixin_user_id, default_agent_id, active_agent_id, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, weixin_user_id) DO UPDATE SET
	default_agent_id = excluded.default_agent_id,
	active_agent_id = excluded.active_agent_id,
	updated_at = excluded.updated_at`,
			tenantID,
			scopedUserID,
			defaultAgentID,
			activeAgentID,
			now,
			now,
		); err != nil {
			return fmt.Errorf("save weixin agent binding %s: %w", userID, err)
		}
	}
	userBindings := stringBindingMap(extra["weixinUserBindings"])
	for _, userID := range sortedMapKeys(userBindings) {
		tenantID, scopedUserID, ok := ParseWeixinBindingKey(userID)
		if !ok {
			continue
		}
		humanID := strings.TrimSpace(userBindings[userID])
		if scopedUserID == "" || humanID == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO gateway_weixin_user_bindings (
	tenant_id, weixin_user_id, human_profile_id, created_at, updated_at
) VALUES (?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, weixin_user_id) DO UPDATE SET
	human_profile_id = excluded.human_profile_id,
	updated_at = excluded.updated_at`,
			tenantID,
			scopedUserID,
			humanID,
			now,
			now,
		); err != nil {
			return fmt.Errorf("save weixin user binding %s: %w", userID, err)
		}
	}
	personalAssistantBindings := stringBindingMap(extra["personalAssistantBindings"])
	for _, userID := range sortedMapKeys(personalAssistantBindings) {
		tenantID, scopedUserID, ok := ParseWeixinBindingKey(userID)
		if !ok {
			continue
		}
		ownerID := strings.TrimSpace(personalAssistantBindings[userID])
		if scopedUserID == "" || ownerID == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO gateway_weixin_personal_assistant_bindings (
	tenant_id, weixin_user_id, owner_id, created_at, updated_at
) VALUES (?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, weixin_user_id) DO UPDATE SET
	owner_id = excluded.owner_id,
	updated_at = excluded.updated_at`, tenantID, scopedUserID, ownerID, now, now); err != nil {
			return fmt.Errorf("save weixin personal assistant binding %s: %w", userID, err)
		}
	}
	for _, account := range weixinAccountsFromExtra(extra) {
		if account.UserID == "" || account.Token == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO gateway_weixin_accounts (
	tenant_id, weixin_user_id, bot_id, token, ilink_base_url, state_path, x_wechat_uin, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, weixin_user_id) DO UPDATE SET
	bot_id = excluded.bot_id,
	token = excluded.token,
	ilink_base_url = excluded.ilink_base_url,
	state_path = excluded.state_path,
	x_wechat_uin = excluded.x_wechat_uin,
	updated_at = excluded.updated_at`,
			account.TenantID,
			account.UserID,
			account.BotID,
			account.Token,
			account.IlinkBaseURL,
			account.StatePath,
			account.XWechatUIN,
			now,
			now,
		); err != nil {
			return fmt.Errorf("save weixin account %s: %w", account.UserID, err)
		}
	}
	return nil
}

func readScopedStringBindingRows(ctx context.Context, store *platformdb.Store, query string) (map[string]any, error) {
	rows, err := store.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("load tenant-scoped string bindings: %w", err)
	}
	defer rows.Close()
	out := map[string]any{}
	for rows.Next() {
		var tenantID, userID, value string
		if err := rows.Scan(&tenantID, &userID, &value); err != nil {
			return nil, fmt.Errorf("scan tenant-scoped string binding: %w", err)
		}
		key := WeixinBindingKey(tenantID, userID)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			out[key] = value
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tenant-scoped string bindings: %w", err)
	}
	return out, nil
}

func readWeixinAccountRows(ctx context.Context, store *platformdb.Store) (map[string]any, error) {
	rows, err := store.Query(ctx, `
SELECT tenant_id, weixin_user_id, bot_id, token, ilink_base_url, state_path, x_wechat_uin
FROM gateway_weixin_accounts
ORDER BY tenant_id, weixin_user_id`)
	if err != nil {
		return nil, fmt.Errorf("load weixin accounts: %w", err)
	}
	defer rows.Close()
	out := map[string]any{}
	for rows.Next() {
		var tenantID, userID, botID, token, ilinkBaseURL, statePath, xWechatUIN string
		if err := rows.Scan(&tenantID, &userID, &botID, &token, &ilinkBaseURL, &statePath, &xWechatUIN); err != nil {
			return nil, fmt.Errorf("scan weixin account: %w", err)
		}
		userID = strings.TrimSpace(userID)
		if userID == "" {
			continue
		}
		account := map[string]any{
			"tenantId":     NormalizeTenantID(tenantID),
			"userId":       userID,
			"botId":        strings.TrimSpace(botID),
			"token":        strings.TrimSpace(token),
			"ilinkBaseUrl": strings.TrimSpace(ilinkBaseURL),
		}
		if strings.TrimSpace(statePath) != "" {
			account["statePath"] = strings.TrimSpace(statePath)
		}
		if strings.TrimSpace(xWechatUIN) != "" {
			account["xWechatUin"] = strings.TrimSpace(xWechatUIN)
		}
		out[WeixinBindingKey(tenantID, userID)] = account
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate weixin accounts: %w", err)
	}
	return out, nil
}

type gatewayAdapterRow struct {
	AdapterKey          string
	AdapterKind         string
	Enabled             int
	Token               string
	APIKey              string
	WebhookURL          string
	AppID               string
	AppSecret           string
	EncryptKey          string
	VerifyToken         string
	DefaultSessionLabel string
	ConnectionMode      string
	DomainName          string
}

type databaseExtraValue struct {
	Kind   string
	String string
	Int    any
	Real   any
	Bool   any
	JSON   string
}

type databaseWeixinAccount struct {
	TenantID     string
	UserID       string
	BotID        string
	Token        string
	IlinkBaseURL string
	StatePath    string
	XWechatUIN   string
}

func applyGatewayAdapterRow(cfg *Config, row gatewayAdapterRow) bool {
	if row.AdapterKind == "feishu_account" && strings.HasPrefix(row.AdapterKey, "feishu:") {
		accountID := strings.TrimPrefix(row.AdapterKey, "feishu:")
		if accountID == "" {
			return false
		}
		if cfg.Adapters.Feishu.Accounts == nil {
			cfg.Adapters.Feishu.Accounts = map[string]FeishuAccountConfig{}
		}
		cfg.Adapters.Feishu.Accounts[accountID] = FeishuAccountConfig{
			Enabled:             intBool(row.Enabled),
			AppID:               row.AppID,
			AppSecret:           row.AppSecret,
			EncryptKey:          row.EncryptKey,
			VerifyToken:         row.VerifyToken,
			DefaultSessionLabel: row.DefaultSessionLabel,
			ConnectionMode:      row.ConnectionMode,
			DomainName:          row.DomainName,
		}
		return false
	}
	switch row.AdapterKey {
	case "feishu":
		cfg.Adapters.Feishu = FeishuAdapterConfig{
			Enabled:             intBool(row.Enabled),
			AppID:               row.AppID,
			AppSecret:           row.AppSecret,
			EncryptKey:          row.EncryptKey,
			VerifyToken:         row.VerifyToken,
			DefaultSessionLabel: row.DefaultSessionLabel,
			ConnectionMode:      row.ConnectionMode,
			DomainName:          row.DomainName,
		}
	case "weixin":
		cfg.Adapters.Weixin.Enabled = intBool(row.Enabled)
		return strings.TrimSpace(row.Token) != "" || strings.TrimSpace(row.APIKey) != "" || strings.TrimSpace(row.WebhookURL) != ""
	default:
		adapter := platformAdapterByKey(cfg, row.AdapterKey)
		if adapter == nil {
			return false
		}
		adapter.Enabled = intBool(row.Enabled)
		adapter.Token = row.Token
		adapter.APIKey = row.APIKey
		adapter.WebhookURL = row.WebhookURL
	}
	return false
}

func adapterExtraByKey(cfg *Config, key string) *map[string]any {
	if cfg == nil {
		return nil
	}
	if key == "weixin" {
		if cfg.Adapters.Weixin.Extra == nil {
			cfg.Adapters.Weixin.Extra = map[string]any{}
		}
		return &cfg.Adapters.Weixin.Extra
	}
	adapter := platformAdapterByKey(cfg, key)
	if adapter == nil {
		return nil
	}
	if adapter.Extra == nil {
		adapter.Extra = map[string]any{}
	}
	return &adapter.Extra
}

func platformAdapterValueByKey(cfg Config, key string) PlatformAdapterConfig {
	if adapter := platformAdapterByKey(&cfg, key); adapter != nil {
		return *adapter
	}
	return PlatformAdapterConfig{}
}

func platformAdapterByKey(cfg *Config, key string) *PlatformAdapterConfig {
	if cfg == nil {
		return nil
	}
	switch key {
	case "dingtalk":
		return &cfg.Adapters.DingTalk
	case "email":
		return &cfg.Adapters.Email
	default:
		return nil
	}
}

func encodeDatabaseExtraValue(value any) (databaseExtraValue, error) {
	switch typed := value.(type) {
	case nil:
		return databaseExtraValue{Kind: "string"}, nil
	case string:
		return databaseExtraValue{Kind: "string", String: typed}, nil
	case bool:
		return databaseExtraValue{Kind: "bool", Bool: boolInt(typed)}, nil
	case int:
		return databaseExtraValue{Kind: "int", Int: int64(typed)}, nil
	case int8:
		return databaseExtraValue{Kind: "int", Int: int64(typed)}, nil
	case int16:
		return databaseExtraValue{Kind: "int", Int: int64(typed)}, nil
	case int32:
		return databaseExtraValue{Kind: "int", Int: int64(typed)}, nil
	case int64:
		return databaseExtraValue{Kind: "int", Int: typed}, nil
	case uint:
		return databaseExtraValue{Kind: "int", Int: int64(typed)}, nil
	case uint8:
		return databaseExtraValue{Kind: "int", Int: int64(typed)}, nil
	case uint16:
		return databaseExtraValue{Kind: "int", Int: int64(typed)}, nil
	case uint32:
		return databaseExtraValue{Kind: "int", Int: int64(typed)}, nil
	case uint64:
		if typed > math.MaxInt64 {
			return databaseExtraValue{}, fmt.Errorf("integer value is too large")
		}
		return databaseExtraValue{Kind: "int", Int: int64(typed)}, nil
	case float32:
		return databaseExtraValue{Kind: "real", Real: float64(typed)}, nil
	case float64:
		return databaseExtraValue{Kind: "real", Real: typed}, nil
	case json.Number:
		if i, err := typed.Int64(); err == nil {
			return databaseExtraValue{Kind: "int", Int: i}, nil
		}
		f, err := typed.Float64()
		if err != nil {
			return databaseExtraValue{}, err
		}
		return databaseExtraValue{Kind: "real", Real: f}, nil
	default:
		raw, err := json.Marshal(typed)
		if err != nil {
			return databaseExtraValue{}, err
		}
		return databaseExtraValue{Kind: "json", JSON: string(raw)}, nil
	}
}

func decodeDatabaseExtraValue(kind string, stringValue string, intValue sql.NullInt64, realValue sql.NullFloat64, boolValue sql.NullInt64, jsonValue string) (any, error) {
	switch strings.TrimSpace(kind) {
	case "", "string":
		return stringValue, nil
	case "int":
		if !intValue.Valid {
			return 0, nil
		}
		return int(intValue.Int64), nil
	case "real":
		if !realValue.Valid {
			return float64(0), nil
		}
		return realValue.Float64, nil
	case "bool":
		return boolValue.Valid && boolValue.Int64 != 0, nil
	case "json":
		if strings.TrimSpace(jsonValue) == "" {
			return nil, nil
		}
		var value any
		dec := json.NewDecoder(strings.NewReader(jsonValue))
		dec.UseNumber()
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		return normalizeDecodedJSONNumbers(value), nil
	default:
		return nil, fmt.Errorf("unsupported value kind %q", kind)
	}
}

func normalizeDecodedJSONNumbers(value any) any {
	switch typed := value.(type) {
	case json.Number:
		if i, err := typed.Int64(); err == nil {
			return int(i)
		}
		if f, err := typed.Float64(); err == nil {
			return f
		}
		return typed.String()
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = normalizeDecodedJSONNumbers(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = normalizeDecodedJSONNumbers(item)
		}
		return out
	default:
		return value
	}
}

func stringBindingMap(value any) map[string]string {
	switch typed := value.(type) {
	case map[string]any:
		out := map[string]string{}
		for key, raw := range typed {
			key = strings.TrimSpace(key)
			text := databaseStringValue(raw)
			if key != "" && text != "" {
				out[key] = text
			}
		}
		return out
	case map[string]string:
		out := map[string]string{}
		for key, value := range typed {
			key = strings.TrimSpace(key)
			value = strings.TrimSpace(value)
			if key != "" && value != "" {
				out[key] = value
			}
		}
		return out
	default:
		return nil
	}
}

func weixinAccountsFromExtra(extra map[string]any) []databaseWeixinAccount {
	if extra == nil {
		return nil
	}
	value, ok := extra["weixinAccounts"]
	if !ok {
		value = extra["accounts"]
	}
	raw, ok := value.(map[string]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	accounts := make([]databaseWeixinAccount, 0, len(raw))
	for key, value := range raw {
		fields, ok := value.(map[string]any)
		if !ok {
			continue
		}
		account := databaseWeixinAccount{
			TenantID:     firstDatabaseString(databaseStringValue(fields["tenantId"]), databaseStringValue(fields["tenant_id"])),
			UserID:       firstDatabaseString(databaseStringValue(fields["userId"]), databaseStringValue(fields["user_id"]), databaseStringValue(fields["weixinUserId"]), databaseStringValue(fields["ilinkUserId"]), key),
			BotID:        firstDatabaseString(databaseStringValue(fields["botId"]), databaseStringValue(fields["bot_id"]), databaseStringValue(fields["accountId"]), databaseStringValue(fields["ilinkBotId"])),
			Token:        databaseStringValue(fields["token"]),
			IlinkBaseURL: firstDatabaseString(databaseStringValue(fields["ilinkBaseUrl"]), databaseStringValue(fields["baseUrl"])),
			StatePath:    databaseStringValue(fields["statePath"]),
			XWechatUIN:   firstDatabaseString(databaseStringValue(fields["xWechatUin"]), databaseStringValue(fields["xWechatUIN"])),
		}
		if tenantID, userID, scoped := ParseWeixinBindingKey(key); scoped {
			if account.TenantID == "" {
				account.TenantID = tenantID
			}
			if databaseStringValue(fields["userId"]) == "" && databaseStringValue(fields["user_id"]) == "" && databaseStringValue(fields["weixinUserId"]) == "" && databaseStringValue(fields["ilinkUserId"]) == "" {
				account.UserID = userID
			}
		}
		account.TenantID = NormalizeTenantID(account.TenantID)
		if account.UserID == "" || account.Token == "" {
			continue
		}
		accounts = append(accounts, account)
	}
	sort.SliceStable(accounts, func(i, j int) bool {
		if accounts[i].TenantID == accounts[j].TenantID {
			return accounts[i].UserID < accounts[j].UserID
		}
		return accounts[i].TenantID < accounts[j].TenantID
	})
	return accounts
}

func databaseStringValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func firstDatabaseString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func ensureDatabaseWeixinExtra(cfg *Config) map[string]any {
	if cfg.Adapters.Weixin.Extra == nil {
		cfg.Adapters.Weixin.Extra = map[string]any{}
	}
	return cfg.Adapters.Weixin.Extra
}

func databaseRecord(cfg Config) Record {
	raw, _ := yaml.Marshal(cfg)
	return Record{Path: databaseRecordPath, RawYAML: string(raw), Config: cfg}
}

func loadLegacyConfigIfExists(path string) (Config, bool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return Config{}, false, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, false, nil
		}
		return Config{}, false, err
	}
	cfg := DefaultConfig()
	if len(raw) > 0 {
		if err := DecodeConfigYAML(string(raw), &cfg); err != nil {
			return Config{}, false, err
		}
	}
	fileStore := NewStore(path)
	if err := fileStore.decryptSecrets(&cfg); err != nil {
		return Config{}, false, err
	}
	normalize(&cfg)
	if err := validate(cfg); err != nil {
		return Config{}, false, err
	}
	return cfg, true, nil
}

func isPrivateWeixinExtraKey(key string) bool {
	_, ok := protectedWeixinExtraKeys[strings.TrimSpace(key)]
	return ok
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func intBool(value int) bool {
	return value != 0
}

func sortedMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedStringKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
