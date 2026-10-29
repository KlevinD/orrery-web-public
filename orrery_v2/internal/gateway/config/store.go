package config

import (
	"crypto/aes"
	"crypto/cipher"
	crand "crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
	platformdb "orrery_v2/internal/platform/db"
)

const MaskValue = "********"
const encryptedSecretPrefix = "enc:v1:"

var protectedWeixinExtraKeys = map[string]struct{}{
	"agentBindings":             {},
	"weixinUserBindings":        {},
	"weixinAccounts":            {},
	"accounts":                  {},
	"activeAgentBindings":       {},
	"personalAssistantBindings": {},
}

var errWeixinTenantUnchanged = errors.New("weixin tenant state is already current")

var obsoleteWeixinGlobalExtraKeys = []string{
	"mode",
	"bridgeToken",
	"openclawBridgeToken",
	"bindAddress",
	"host",
	"port",
	"path",
	"inboundPath",
	"maxBodyBytes",
}

type Store struct {
	path                     string
	database                 *platformdb.Store
	legacyPath               string
	secretKeyFile            string
	secretKeyValue           string
	requireExistingSecretKey bool
	updateMu                 sync.Mutex
}

type ConfigMutation func(*Config) error
type ConfigApply func(Config) error

type ApplyError struct {
	Cause           error
	PersistRollback error
	RuntimeRestore  error
}

func (e *ApplyError) Error() string {
	if e == nil {
		return "apply gateway config failed"
	}
	message := fmt.Sprintf("apply gateway config: %v", e.Cause)
	if e.PersistRollback != nil {
		message += fmt.Sprintf("; persist rollback: %v", e.PersistRollback)
	}
	if e.RuntimeRestore != nil {
		message += fmt.Sprintf("; restore runtime: %v", e.RuntimeRestore)
	}
	return message
}

func (e *ApplyError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func IsApplyError(err error) bool {
	var target *ApplyError
	return errors.As(err, &target)
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

func DefaultConfig() Config {
	return Config{
		Gateway: GatewayConfig{
			Port:                      18789,
			BindAddress:               "127.0.0.1",
			IdleSessionTimeoutMinutes: 30,
			MaxPerSessionMCPInstances: 5,
		},
		Adapters: AdaptersConfig{
			Feishu: FeishuAdapterConfig{
				DefaultSessionLabel: "general",
				ConnectionMode:      "stream",
				DomainName:          "feishu",
			},
		},
	}
}

func (s *Store) LoadOrDefault() (Record, error) {
	if s != nil && s.database != nil {
		return s.loadDatabaseOrDefault()
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if !os.IsNotExist(err) {
			return Record{}, err
		}
		return s.Save(DefaultConfig())
	}

	cfg := DefaultConfig()
	plaintextSecrets := false
	obsoleteWeixinGlobalConfig := false
	legacyDingTalkCredentials := false
	if len(raw) > 0 {
		if err := DecodeConfigYAML(string(raw), &cfg); err != nil {
			return Record{}, err
		}
		plaintextSecrets = hasPlaintextSecrets(cfg)
		obsoleteWeixinGlobalConfig = hasObsoleteWeixinGlobalYAML(raw) || hasObsoleteWeixinGlobalConfig(cfg)
		legacyDingTalkCredentials = hasLegacyDingTalkCredentials(cfg)
	}
	if err := s.decryptSecrets(&cfg); err != nil {
		return Record{}, err
	}
	normalize(&cfg)
	if err := validate(cfg); err != nil {
		return Record{}, err
	}
	if plaintextSecrets || obsoleteWeixinGlobalConfig || legacyDingTalkCredentials {
		return s.Save(cfg)
	}
	return Record{Path: s.path, RawYAML: string(raw), Config: cfg}, nil
}

func (s *Store) Save(cfg Config) (Record, error) {
	if s != nil && s.database != nil {
		return s.saveDatabase(cfg)
	}
	normalize(&cfg)
	if err := validate(cfg); err != nil {
		return Record{}, err
	}
	stored, err := s.encryptSecrets(cfg)
	if err != nil {
		return Record{}, err
	}
	raw, err := yaml.Marshal(stored)
	if err != nil {
		return Record{}, err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return Record{}, err
	}
	if err := os.WriteFile(s.path, raw, 0600); err != nil {
		return Record{}, err
	}
	if err := os.Chmod(s.path, 0600); err != nil {
		return Record{}, err
	}
	return Record{Path: s.path, RawYAML: string(raw), Config: cfg}, nil
}

func (s *Store) SaveRaw(raw string) (Record, error) {
	if s != nil && s.database != nil {
		cfg := DefaultConfig()
		if err := DecodeConfigYAML(raw, &cfg); err != nil {
			return Record{}, err
		}
		if err := s.decryptSecrets(&cfg); err != nil {
			return Record{}, err
		}
		normalize(&cfg)
		if err := validate(cfg); err != nil {
			return Record{}, err
		}
		return s.Save(cfg)
	}
	cfg := DefaultConfig()
	if err := DecodeConfigYAML(raw, &cfg); err != nil {
		return Record{}, err
	}
	if err := s.decryptSecrets(&cfg); err != nil {
		return Record{}, err
	}
	normalize(&cfg)
	if err := validate(cfg); err != nil {
		return Record{}, err
	}
	return s.Save(cfg)
}

func DecodeConfigYAML(raw string, cfg *Config) error {
	if cfg == nil {
		return errors.New("gateway config target is required")
	}
	decoder := yaml.NewDecoder(strings.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(cfg); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("gateway config must contain exactly one YAML document")
		}
		return err
	}
	return nil
}

// Update serializes read-modify-write operations performed through one Store.
// Production wires a shared Store instance so independent Gateway surfaces do
// not overwrite each other's in-process updates with stale snapshots.
func (s *Store) Update(mutate ConfigMutation) (Record, error) {
	return s.UpdateAndApply(mutate, nil)
}

// ReassignWeixinTenant moves all private Weixin account and routing state from
// one tenant to another. It is intentionally idempotent so tenant catalog
// repairs can run on every production startup.
func (s *Store) ReassignWeixinTenant(fromTenantID, toTenantID string) (int, error) {
	fromTenantID = NormalizeTenantID(fromTenantID)
	toTenantID = NormalizeTenantID(toTenantID)
	if fromTenantID == toTenantID {
		return 0, nil
	}
	changed := 0
	_, err := s.Update(func(cfg *Config) error {
		extra := cfg.Adapters.Weixin.Extra
		if extra == nil {
			return errWeixinTenantUnchanged
		}
		for _, key := range []string{"agentBindings", "activeAgentBindings", "personalAssistantBindings", "weixinUserBindings"} {
			count, err := reassignWeixinStringBindingTenant(extra, key, fromTenantID, toTenantID)
			if err != nil {
				return err
			}
			changed += count
		}
		for _, key := range []string{"weixinAccounts", "accounts"} {
			count, err := reassignWeixinAccountTenant(extra, key, fromTenantID, toTenantID)
			if err != nil {
				return err
			}
			changed += count
		}
		if changed == 0 {
			return errWeixinTenantUnchanged
		}
		return nil
	})
	if errors.Is(err, errWeixinTenantUnchanged) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return changed, nil
}

func reassignWeixinStringBindingTenant(extra map[string]any, extraKey, fromTenantID, toTenantID string) (int, error) {
	raw, ok := extra[extraKey]
	if !ok {
		return 0, nil
	}
	changed := 0
	switch bindings := raw.(type) {
	case map[string]any:
		for bindingKey, value := range bindings {
			tenantID, userID, valid := ParseWeixinBindingKey(bindingKey)
			if !valid || tenantID != fromTenantID {
				continue
			}
			nextKey := WeixinBindingKey(toTenantID, userID)
			if existing, exists := bindings[nextKey]; exists && !reflect.DeepEqual(existing, value) {
				return 0, fmt.Errorf("reassign Weixin %s %s: destination binding conflicts", extraKey, userID)
			}
			bindings[nextKey] = value
			delete(bindings, bindingKey)
			changed++
		}
	case map[string]string:
		for bindingKey, value := range bindings {
			tenantID, userID, valid := ParseWeixinBindingKey(bindingKey)
			if !valid || tenantID != fromTenantID {
				continue
			}
			nextKey := WeixinBindingKey(toTenantID, userID)
			if existing, exists := bindings[nextKey]; exists && existing != value {
				return 0, fmt.Errorf("reassign Weixin %s %s: destination binding conflicts", extraKey, userID)
			}
			bindings[nextKey] = value
			delete(bindings, bindingKey)
			changed++
		}
	}
	return changed, nil
}

func reassignWeixinAccountTenant(extra map[string]any, extraKey, fromTenantID, toTenantID string) (int, error) {
	accounts, ok := extra[extraKey].(map[string]any)
	if !ok {
		return 0, nil
	}
	changed := 0
	for bindingKey, value := range accounts {
		tenantID, userID, valid := ParseWeixinBindingKey(bindingKey)
		fields, fieldsOK := value.(map[string]any)
		if fieldsOK {
			configuredTenantID := firstDatabaseString(databaseStringValue(fields["tenantId"]), databaseStringValue(fields["tenant_id"]))
			if configuredTenantID != "" {
				tenantID = NormalizeTenantID(configuredTenantID)
			}
			if !valid || userID == "" {
				userID = firstDatabaseString(databaseStringValue(fields["userId"]), databaseStringValue(fields["user_id"]), databaseStringValue(fields["weixinUserId"]), databaseStringValue(fields["ilinkUserId"]))
				valid = userID != ""
			}
		}
		if !valid || tenantID != fromTenantID {
			continue
		}
		nextValue := cloneExtraValue(value)
		if nextFields, ok := nextValue.(map[string]any); ok {
			nextFields["tenantId"] = toTenantID
			delete(nextFields, "tenant_id")
		}
		nextKey := WeixinBindingKey(toTenantID, userID)
		if existing, exists := accounts[nextKey]; exists && !reflect.DeepEqual(existing, nextValue) {
			return 0, fmt.Errorf("reassign Weixin account %s: destination account conflicts", userID)
		}
		accounts[nextKey] = nextValue
		delete(accounts, bindingKey)
		changed++
	}
	return changed, nil
}

// UpdateAndApply persists a mutation and applies it to the running Gateway as
// one serialized operation. If apply fails, both persistence and runtime are
// restored to the previous configuration before the error is returned.
func (s *Store) UpdateAndApply(mutate ConfigMutation, apply ConfigApply) (Record, error) {
	if s == nil {
		return Record{}, fmt.Errorf("gateway config store is not configured")
	}
	s.updateMu.Lock()
	defer s.updateMu.Unlock()

	previous, err := s.LoadOrDefault()
	if err != nil {
		return Record{}, err
	}
	next := previous.Config
	cloneConfigExtras(&next)
	if mutate != nil {
		if err := mutate(&next); err != nil {
			return Record{}, err
		}
	}
	saved, err := s.Save(next)
	if err != nil {
		return Record{}, err
	}
	if apply == nil {
		return saved, nil
	}
	if err := apply(saved.Config); err != nil {
		rollback, rollbackErr := s.Save(previous.Config)
		if rollbackErr != nil {
			return Record{}, &ApplyError{Cause: err, PersistRollback: rollbackErr}
		}
		if restoreErr := apply(rollback.Config); restoreErr != nil {
			return Record{}, &ApplyError{Cause: err, RuntimeRestore: restoreErr}
		}
		return Record{}, &ApplyError{Cause: err}
	}
	return saved, nil
}

func (r Record) Masked() Record {
	next := r
	next.Config = MaskConfig(next.Config)
	raw, _ := yaml.Marshal(next.Config)
	next.RawYAML = string(raw)
	return next
}

func MaskConfig(cfg Config) Config {
	if cfg.Adapters.Feishu.AppSecret != "" {
		cfg.Adapters.Feishu.AppSecret = MaskValue
	}
	if cfg.Adapters.Feishu.EncryptKey != "" {
		cfg.Adapters.Feishu.EncryptKey = MaskValue
	}
	if cfg.Adapters.Feishu.VerifyToken != "" {
		cfg.Adapters.Feishu.VerifyToken = MaskValue
	}
	maskedAccounts := make(map[string]FeishuAccountConfig, len(cfg.Adapters.Feishu.Accounts))
	for accountID, account := range cfg.Adapters.Feishu.Accounts {
		maskedAccounts[accountID] = maskFeishuAccount(account)
	}
	if len(maskedAccounts) > 0 {
		cfg.Adapters.Feishu.Accounts = maskedAccounts
	}

	cfg.Adapters.Weixin = maskWeixin(cfg.Adapters.Weixin)
	cfg.Adapters.DingTalk = maskPlatform(cfg.Adapters.DingTalk)
	cfg.Adapters.Email = maskPlatform(cfg.Adapters.Email)
	return cfg
}

func MergeMaskedConfig(next Config, existing Config) Config {
	if next.Adapters.Feishu.AppSecret == MaskValue {
		next.Adapters.Feishu.AppSecret = existing.Adapters.Feishu.AppSecret
	}
	if next.Adapters.Feishu.EncryptKey == MaskValue {
		next.Adapters.Feishu.EncryptKey = existing.Adapters.Feishu.EncryptKey
	}
	if next.Adapters.Feishu.VerifyToken == MaskValue {
		next.Adapters.Feishu.VerifyToken = existing.Adapters.Feishu.VerifyToken
	}
	for accountID, account := range next.Adapters.Feishu.Accounts {
		account = mergeMaskedFeishuAccount(account, existing.Adapters.Feishu.Accounts[accountID])
		next.Adapters.Feishu.Accounts[accountID] = account
	}

	mergeMaskedWeixin(&next.Adapters.Weixin, existing.Adapters.Weixin)
	mergeMaskedPlatform(&next.Adapters.DingTalk, existing.Adapters.DingTalk)
	mergeMaskedPlatform(&next.Adapters.Email, existing.Adapters.Email)
	return next
}

func maskFeishuAccount(cfg FeishuAccountConfig) FeishuAccountConfig {
	if cfg.AppSecret != "" {
		cfg.AppSecret = MaskValue
	}
	if cfg.EncryptKey != "" {
		cfg.EncryptKey = MaskValue
	}
	if cfg.VerifyToken != "" {
		cfg.VerifyToken = MaskValue
	}
	return cfg
}

func mergeMaskedFeishuAccount(next, existing FeishuAccountConfig) FeishuAccountConfig {
	if next.AppSecret == MaskValue {
		next.AppSecret = existing.AppSecret
	}
	if next.EncryptKey == MaskValue {
		next.EncryptKey = existing.EncryptKey
	}
	if next.VerifyToken == MaskValue {
		next.VerifyToken = existing.VerifyToken
	}
	return next
}

func RedactPrivateWeixinBindings(cfg Config) Config {
	if cfg.Adapters.Weixin.Extra == nil {
		return cfg
	}
	cfg.Adapters.Weixin.Extra = cloneExtra(cfg.Adapters.Weixin.Extra)
	for key := range protectedWeixinExtraKeys {
		delete(cfg.Adapters.Weixin.Extra, key)
	}
	return cfg
}

func MergeProtectedWeixinBindings(next Config, existing Config) Config {
	preserveProtectedWeixinExtra(&next.Adapters.Weixin, existing.Adapters.Weixin)
	return next
}

func maskPlatform(cfg PlatformAdapterConfig) PlatformAdapterConfig {
	if cfg.Token != "" {
		cfg.Token = MaskValue
	}
	if cfg.APIKey != "" {
		cfg.APIKey = MaskValue
	}
	return cfg
}

func maskWeixin(cfg WeixinAdapterConfig) WeixinAdapterConfig {
	if cfg.Extra == nil {
		return cfg
	}
	cfg.Extra = cloneExtra(cfg.Extra)
	for _, key := range obsoleteWeixinGlobalExtraKeys {
		delete(cfg.Extra, key)
	}
	maskWeixinAccounts(cfg.Extra["weixinAccounts"])
	return cfg
}

func mergeMaskedPlatform(next *PlatformAdapterConfig, existing PlatformAdapterConfig) {
	if next.Token == MaskValue {
		next.Token = existing.Token
	}
	if next.APIKey == MaskValue {
		next.APIKey = existing.APIKey
	}
}

func mergeMaskedWeixin(next *WeixinAdapterConfig, existing WeixinAdapterConfig) {
	if next.Extra != nil {
		for _, key := range obsoleteWeixinGlobalExtraKeys {
			delete(next.Extra, key)
		}
	}
	mergeMaskedWeixinAccounts(next.Extra, existing.Extra)
	preserveProtectedWeixinExtra(next, existing)
}

func cloneExtra(extra map[string]any) map[string]any {
	out := make(map[string]any, len(extra))
	for key, value := range extra {
		out[key] = cloneExtraValue(value)
	}
	return out
}

func cloneExtraValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneExtra(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = cloneExtraValue(item)
		}
		return out
	default:
		return value
	}
}

func maskWeixinAccounts(value any) {
	accounts, ok := value.(map[string]any)
	if !ok {
		return
	}
	for key, raw := range accounts {
		account, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if token, ok := account["token"].(string); ok && token != "" {
			account["token"] = MaskValue
		}
		accounts[key] = account
	}
}

func mergeMaskedWeixinAccounts(nextExtra map[string]any, existingExtra map[string]any) {
	if nextExtra == nil || existingExtra == nil {
		return
	}
	nextAccounts, ok := nextExtra["weixinAccounts"].(map[string]any)
	if !ok {
		return
	}
	existingAccounts, ok := existingExtra["weixinAccounts"].(map[string]any)
	if !ok {
		return
	}
	for key, raw := range nextAccounts {
		nextAccount, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if nextAccount["token"] != MaskValue {
			continue
		}
		existingAccount, ok := existingAccounts[key].(map[string]any)
		if !ok {
			continue
		}
		if token, ok := existingAccount["token"].(string); ok {
			nextAccount["token"] = token
		}
	}
}

func preserveProtectedWeixinExtra(next *WeixinAdapterConfig, existing WeixinAdapterConfig) {
	if next.Extra == nil {
		next.Extra = map[string]any{}
	}
	for key := range protectedWeixinExtraKeys {
		delete(next.Extra, key)
	}
	if existing.Extra == nil {
		return
	}
	for key := range protectedWeixinExtraKeys {
		if value, ok := existing.Extra[key]; ok {
			next.Extra[key] = cloneExtraValue(value)
		}
	}
}

func (s *Store) encryptSecrets(cfg Config) (Config, error) {
	cloneConfigExtras(&cfg)
	var key []byte
	var err error
	ensureKey := func() ([]byte, error) {
		if key != nil {
			return key, nil
		}
		key, err = s.loadOrCreateSecretKey()
		return key, err
	}
	encrypt := func(value string) (string, error) {
		value = strings.TrimSpace(value)
		if value == "" || value == MaskValue || strings.HasPrefix(value, encryptedSecretPrefix) {
			return value, nil
		}
		secretKey, err := ensureKey()
		if err != nil {
			return "", err
		}
		return sealSecret(secretKey, value)
	}
	if err := encryptConfigSecrets(&cfg, encrypt); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (s *Store) decryptSecrets(cfg *Config) error {
	decrypt := func(value string) (string, error) {
		value = strings.TrimSpace(value)
		if !strings.HasPrefix(value, encryptedSecretPrefix) {
			return value, nil
		}
		key, err := s.loadSecretKey()
		if err != nil {
			return "", err
		}
		return openSecret(key, value)
	}
	return encryptConfigSecrets(cfg, decrypt)
}

func hasPlaintextSecrets(cfg Config) bool {
	found := false
	inspect := func(value string) (string, error) {
		value = strings.TrimSpace(value)
		if value != "" && value != MaskValue && !strings.HasPrefix(value, encryptedSecretPrefix) {
			found = true
		}
		return value, nil
	}
	_ = encryptConfigSecrets(&cfg, inspect)
	return found
}

func encryptConfigSecrets(cfg *Config, transform func(string) (string, error)) error {
	var err error
	apply := func(value *string) {
		if err != nil || value == nil {
			return
		}
		*value, err = transform(*value)
	}
	apply(&cfg.Adapters.Feishu.AppSecret)
	apply(&cfg.Adapters.Feishu.EncryptKey)
	apply(&cfg.Adapters.Feishu.VerifyToken)
	for accountID, account := range cfg.Adapters.Feishu.Accounts {
		apply(&account.AppSecret)
		apply(&account.EncryptKey)
		apply(&account.VerifyToken)
		cfg.Adapters.Feishu.Accounts[accountID] = account
	}
	transformPlatformSecrets(&cfg.Adapters.DingTalk, apply)
	transformPlatformSecrets(&cfg.Adapters.Email, apply)
	if err != nil {
		return err
	}
	if err := transformWeixinAccountSecrets(cfg.Adapters.Weixin.Extra, transform); err != nil {
		return err
	}
	return err
}

func transformPlatformSecrets(cfg *PlatformAdapterConfig, apply func(*string)) {
	if cfg == nil {
		return
	}
	apply(&cfg.Token)
	apply(&cfg.APIKey)
}

func cloneConfigExtras(cfg *Config) {
	if cfg.Adapters.Feishu.Accounts != nil {
		accounts := make(map[string]FeishuAccountConfig, len(cfg.Adapters.Feishu.Accounts))
		for accountID, account := range cfg.Adapters.Feishu.Accounts {
			accounts[accountID] = account
		}
		cfg.Adapters.Feishu.Accounts = accounts
	}
	clonePlatformExtra := func(adapter *PlatformAdapterConfig) {
		if adapter != nil && adapter.Extra != nil {
			adapter.Extra = cloneExtra(adapter.Extra)
		}
	}
	if cfg.Adapters.Weixin.Extra != nil {
		cfg.Adapters.Weixin.Extra = cloneExtra(cfg.Adapters.Weixin.Extra)
	}
	clonePlatformExtra(&cfg.Adapters.DingTalk)
	clonePlatformExtra(&cfg.Adapters.Email)
}

func transformWeixinAccountSecrets(extra map[string]any, transform func(string) (string, error)) error {
	if extra == nil {
		return nil
	}
	for _, key := range []string{"weixinAccounts", "accounts"} {
		raw, ok := extra[key].(map[string]any)
		if !ok {
			continue
		}
		for accountKey, accountValue := range raw {
			account, ok := accountValue.(map[string]any)
			if !ok {
				continue
			}
			for _, secretKey := range []string{"token"} {
				text, ok := account[secretKey].(string)
				if !ok {
					continue
				}
				next, err := transform(text)
				if err != nil {
					return err
				}
				account[secretKey] = next
			}
			raw[accountKey] = account
		}
	}
	return nil
}

func (s *Store) loadSecretKey() ([]byte, error) {
	if value := strings.TrimSpace(s.secretKeyValue); value != "" {
		return parseSecretKey([]byte(value))
	}
	raw, err := os.ReadFile(s.secretKeyPath())
	if err != nil {
		return nil, fmt.Errorf("load gateway secret key: %w", err)
	}
	return parseSecretKey(raw)
}

func (s *Store) loadOrCreateSecretKey() ([]byte, error) {
	if value := strings.TrimSpace(s.secretKeyValue); value != "" {
		return parseSecretKey([]byte(value))
	}
	if s.requireExistingSecretKey {
		return s.loadSecretKey()
	}
	if raw, err := os.ReadFile(s.secretKeyPath()); err == nil {
		return parseSecretKey(raw)
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("load gateway secret key: %w", err)
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(crand.Reader, key); err != nil {
		return nil, fmt.Errorf("create gateway secret key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.secretKeyPath()), 0755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(s.secretKeyPath(), []byte(base64.StdEncoding.EncodeToString(key)+"\n"), 0600); err != nil {
		return nil, err
	}
	if err := os.Chmod(s.secretKeyPath(), 0600); err != nil {
		return nil, err
	}
	return key, nil
}

func (s *Store) ValidateEncryptionKey() error {
	if s == nil || !s.requireExistingSecretKey {
		return nil
	}
	_, err := s.loadSecretKey()
	return err
}

func (s *Store) secretKeyPath() string {
	if strings.TrimSpace(s.secretKeyFile) != "" {
		return strings.TrimSpace(s.secretKeyFile)
	}
	return s.path + ".key"
}

func parseSecretKey(raw []byte) ([]byte, error) {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return nil, fmt.Errorf("gateway secret key is empty")
	}
	key, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		if len(raw) == 32 {
			key = append([]byte(nil), raw...)
		} else {
			return nil, fmt.Errorf("decode gateway secret key: %w", err)
		}
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("gateway secret key must be 32 bytes")
	}
	return key, nil
}

func sealSecret(key []byte, value string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(crand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nil, nonce, []byte(value), nil)
	payload := append(nonce, ciphertext...)
	return encryptedSecretPrefix + base64.StdEncoding.EncodeToString(payload), nil
}

func openSecret(key []byte, value string) (string, error) {
	payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, encryptedSecretPrefix))
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(payload) < gcm.NonceSize() {
		return "", fmt.Errorf("encrypted gateway secret is truncated")
	}
	nonce := payload[:gcm.NonceSize()]
	ciphertext := payload[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func normalize(cfg *Config) {
	normalizeWeixinConfig(cfg)
	normalizeDingTalkConfig(cfg)
	if cfg.Gateway.Port == 0 {
		cfg.Gateway.Port = 18789
	}
	if cfg.Gateway.BindAddress == "" {
		cfg.Gateway.BindAddress = "127.0.0.1"
	}
	if cfg.Gateway.IdleSessionTimeoutMinutes == 0 {
		cfg.Gateway.IdleSessionTimeoutMinutes = 30
	}
	if cfg.Gateway.MaxPerSessionMCPInstances == 0 {
		cfg.Gateway.MaxPerSessionMCPInstances = 5
	}
	if cfg.Adapters.Feishu.DefaultSessionLabel == "" {
		cfg.Adapters.Feishu.DefaultSessionLabel = "general"
	}
	if cfg.Adapters.Feishu.ConnectionMode == "" || cfg.Adapters.Feishu.ConnectionMode == "webhook" {
		cfg.Adapters.Feishu.ConnectionMode = "stream"
	}
	if cfg.Adapters.Feishu.DomainName == "" {
		cfg.Adapters.Feishu.DomainName = "feishu"
	}
	normalizedAccounts := make(map[string]FeishuAccountConfig, len(cfg.Adapters.Feishu.Accounts))
	for accountID, account := range cfg.Adapters.Feishu.Accounts {
		accountID = strings.TrimSpace(accountID)
		if account.DefaultSessionLabel == "" {
			account.DefaultSessionLabel = "general"
		}
		if account.ConnectionMode == "" || account.ConnectionMode == "webhook" {
			account.ConnectionMode = "stream"
		}
		if account.DomainName == "" {
			account.DomainName = "feishu"
		}
		normalizedAccounts[accountID] = account
	}
	if len(normalizedAccounts) > 0 {
		cfg.Adapters.Feishu.Accounts = normalizedAccounts
	}
}

func normalizeDingTalkConfig(cfg *Config) {
	if cfg == nil || cfg.Adapters.DingTalk.Extra == nil {
		return
	}
	adapter := &cfg.Adapters.DingTalk
	if adapter.APIKey == "" {
		adapter.APIKey, _ = adapter.Extra["clientId"].(string)
	}
	if adapter.Token == "" {
		adapter.Token, _ = adapter.Extra["clientSecret"].(string)
	}
	delete(adapter.Extra, "clientId")
	delete(adapter.Extra, "clientSecret")
}

func hasLegacyDingTalkCredentials(cfg Config) bool {
	if cfg.Adapters.DingTalk.Extra == nil {
		return false
	}
	_, hasClientID := cfg.Adapters.DingTalk.Extra["clientId"]
	_, hasClientSecret := cfg.Adapters.DingTalk.Extra["clientSecret"]
	return hasClientID || hasClientSecret
}

func normalizeWeixinConfig(cfg *Config) {
	if cfg == nil {
		return
	}
	cfg.Adapters.Weixin.LegacyToken = ""
	cfg.Adapters.Weixin.LegacyAPIKey = ""
	cfg.Adapters.Weixin.LegacyWebhookURL = ""
	if cfg.Adapters.Weixin.Extra == nil {
		return
	}
	for _, key := range obsoleteWeixinGlobalExtraKeys {
		delete(cfg.Adapters.Weixin.Extra, key)
	}
}

func hasObsoleteWeixinGlobalConfig(cfg Config) bool {
	adapter := cfg.Adapters.Weixin
	if adapter.LegacyToken != "" || adapter.LegacyAPIKey != "" || adapter.LegacyWebhookURL != "" {
		return true
	}
	for _, key := range obsoleteWeixinGlobalExtraKeys {
		if _, ok := adapter.Extra[key]; ok {
			return true
		}
	}
	return false
}

func hasObsoleteWeixinGlobalYAML(raw []byte) bool {
	var document struct {
		Adapters struct {
			Weixin map[string]any `yaml:"weixin"`
		} `yaml:"adapters"`
	}
	if len(raw) == 0 || yaml.Unmarshal(raw, &document) != nil {
		return false
	}
	for _, key := range []string{"token", "apiKey", "api_key", "webhookUrl", "webhook_url"} {
		if _, ok := document.Adapters.Weixin[key]; ok {
			return true
		}
	}
	extra, _ := document.Adapters.Weixin["extra"].(map[string]any)
	for _, key := range obsoleteWeixinGlobalExtraKeys {
		if _, ok := extra[key]; ok {
			return true
		}
	}
	return false
}

func validate(cfg Config) error {
	if cfg.Gateway.BindAddress != "127.0.0.1" && cfg.Gateway.BindAddress != "localhost" {
		return fmt.Errorf("gateway.bindAddress must be 127.0.0.1 or localhost")
	}
	if cfg.Gateway.Port < 1 || cfg.Gateway.Port > 65535 {
		return fmt.Errorf("gateway.port must be between 1 and 65535")
	}
	if cfg.Gateway.MaxPerSessionMCPInstances < 1 {
		return fmt.Errorf("gateway.maxPerSessionMcpInstances must be at least 1")
	}
	if err := validateFeishuConfig(cfg.Adapters.Feishu); err != nil {
		return err
	}
	if err := validateWeixinConfig(cfg.Adapters.Weixin); err != nil {
		return err
	}
	return nil
}

func validateFeishuConfig(adapter FeishuAdapterConfig) error {
	validateAccount := func(path string, enabled bool, appID, appSecret, mode, domain string) error {
		if !enabled {
			return nil
		}
		if strings.TrimSpace(appID) == "" || strings.TrimSpace(appSecret) == "" {
			return fmt.Errorf("%s appId and appSecret are required when enabled", path)
		}
		switch strings.ToLower(strings.TrimSpace(mode)) {
		case "stream", "outbound":
		default:
			return fmt.Errorf("%s connectionMode must be stream or outbound", path)
		}
		switch strings.ToLower(strings.TrimSpace(domain)) {
		case "feishu", "lark", "larksuite":
		default:
			return fmt.Errorf("%s domainName must be feishu, lark, or larksuite", path)
		}
		return nil
	}
	if len(adapter.Accounts) == 0 {
		return validateAccount("adapters.feishu", adapter.Enabled, adapter.AppID, adapter.AppSecret, adapter.ConnectionMode, adapter.DomainName)
	}
	enabledAccounts := 0
	for accountID, account := range adapter.Accounts {
		if strings.TrimSpace(accountID) == "" || strings.Contains(accountID, ":") {
			return fmt.Errorf("adapters.feishu.accounts account id must be non-empty and must not contain ':'")
		}
		if err := validateAccount("adapters.feishu.accounts."+accountID, adapter.Enabled && account.Enabled, account.AppID, account.AppSecret, account.ConnectionMode, account.DomainName); err != nil {
			return err
		}
		if account.Enabled {
			enabledAccounts++
		}
	}
	if adapter.Enabled && enabledAccounts == 0 {
		return fmt.Errorf("adapters.feishu must have at least one enabled account")
	}
	return nil
}

func validateWeixinConfig(adapter WeixinAdapterConfig) error {
	extra := adapter.Extra
	if extra == nil {
		return nil
	}
	for _, key := range []string{"loginBaseUrl", "ilinkLoginBaseUrl", "ilinkBaseUrl", "baseUrl", "ilinkCdnBaseUrl", "cdnBaseUrl"} {
		if value := strings.TrimSpace(configString(extra[key])); value != "" {
			parsed, err := url.Parse(value)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
				return fmt.Errorf("adapters.weixin.extra.%s must be an HTTP(S) origin/path without credentials, query, or fragment", key)
			}
		}
	}
	for key, want := range map[string]string{"getUpdatesPath": "getupdates", "sendMessagePath": "sendmessage"} {
		if value := strings.Trim(strings.TrimSpace(configString(extra[key])), "/"); value != "" && value != want {
			return fmt.Errorf("adapters.weixin.extra.%s must be %s", key, want)
		}
	}
	for _, bound := range []struct {
		key     string
		minimum int
		maximum int
	}{
		{key: "pollIntervalSeconds", minimum: 1, maximum: 60},
		{key: "requestTimeoutSeconds", minimum: 5, maximum: 120},
		{key: "seenMessageLimit", minimum: 100, maximum: 100000},
		{key: "contextTokens", minimum: 256, maximum: 1000000},
		{key: "replyChunkSize", minimum: 100, maximum: 10000},
	} {
		value, present, err := configInt(extra[bound.key])
		if err != nil {
			return fmt.Errorf("adapters.weixin.extra.%s must be an integer", bound.key)
		}
		if present && (value < bound.minimum || value > bound.maximum) {
			return fmt.Errorf("adapters.weixin.extra.%s must be between %d and %d", bound.key, bound.minimum, bound.maximum)
		}
	}
	return nil
}

func configString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func configInt(value any) (int, bool, error) {
	if value == nil || strings.TrimSpace(configString(value)) == "" {
		return 0, false, nil
	}
	switch typed := value.(type) {
	case int:
		return typed, true, nil
	case int64:
		return int(typed), true, nil
	case float64:
		if typed != float64(int(typed)) {
			return 0, true, fmt.Errorf("not an integer")
		}
		return int(typed), true, nil
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		return parsed, true, err
	default:
		return 0, true, fmt.Errorf("unsupported integer type")
	}
}
