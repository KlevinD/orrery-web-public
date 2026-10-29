package binding

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	database, err := db.Open(context.Background(), db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	store := NewStore(database)
	store.now = func() time.Time { return time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC) }
	return store
}

func TestBindingCodeCreatesTenantScopedBinding(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	code, err := store.CreateCode(ctx, CreateCodeInput{
		TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "feishu:app-1",
		HumanProfileID: "human-a", DefaultAgentID: "agent-a",
	})
	if err != nil {
		t.Fatalf("CreateCode: %v", err)
	}
	binding, err := store.ConsumeCode(ctx, code.Value, ExternalIdentity{
		ChannelKey: "feishu", AccountID: "feishu:app-1", ExternalTenantID: "fs-tenant-a",
		ExternalUserID: "open-a", DisplayName: "Alice",
	})
	if err != nil {
		t.Fatalf("ConsumeCode: %v", err)
	}
	if binding.TenantID != "tenant-a" || binding.HumanProfileID != "human-a" || binding.CurrentAgentID() != "agent-a" {
		t.Fatalf("binding = %+v", binding)
	}
	resolved, err := store.Resolve(ctx, ExternalIdentity{
		ChannelKey: "feishu", AccountID: "feishu:app-1", ExternalTenantID: "fs-tenant-a", ExternalUserID: "open-a",
	})
	if err != nil || resolved.ID != binding.ID {
		t.Fatalf("Resolve = %+v, %v", resolved, err)
	}
	replayed, err := store.ConsumeCode(ctx, code.Value, ExternalIdentity{
		ChannelKey: "feishu", AccountID: "feishu:app-1", ExternalTenantID: "fs-tenant-a", ExternalUserID: "open-a",
	})
	if err != nil || replayed.ID != binding.ID {
		t.Fatalf("idempotent code replay = %+v, %v", replayed, err)
	}
	if _, err := store.ConsumeCode(ctx, code.Value, ExternalIdentity{
		ChannelKey: "feishu", AccountID: "feishu:app-1", ExternalTenantID: "fs-tenant-a", ExternalUserID: "open-b",
	}); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("cross-identity reused code error = %v", err)
	}
}

func TestStoreReassignTenantMovesRoutesAndDeduplicatesCanonicalRows(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	legacyTenant := "org-system-runtime-support"
	canonicalTenant := "minvia"
	for _, statement := range []string{
		`INSERT INTO gateway_channel_accounts (tenant_id, channel_key, account_id, external_tenant_id, display_name, status)
		 VALUES ('org-system-runtime-support', 'feishu', 'main', 'fs-a', 'Main', 'connected')`,
		`INSERT INTO gateway_channel_accounts (tenant_id, channel_key, account_id, external_tenant_id, display_name, status)
		 VALUES ('org-system-runtime-support', 'weixin', 'bot-a', 'org-system-runtime-support', 'Old bot', 'disconnected')`,
		`INSERT INTO gateway_channel_accounts (tenant_id, channel_key, account_id, external_tenant_id, display_name, status)
		 VALUES ('minvia', 'weixin', 'bot-a', 'minvia', 'Current bot', 'connected')`,
		`INSERT INTO gateway_channel_bindings (
		 id, tenant_id, channel_key, account_id, external_tenant_id, external_user_id, human_profile_id, status
		 ) VALUES ('legacy-feishu', 'org-system-runtime-support', 'feishu', 'main', 'fs-a', 'open-a', 'human-a', 'active')`,
		`INSERT INTO gateway_channel_bindings (
		 id, tenant_id, channel_key, account_id, external_tenant_id, external_user_id, human_profile_id, status
		 ) VALUES ('legacy-weixin', 'org-system-runtime-support', 'weixin', 'bot-a', 'org-system-runtime-support', 'wx-a', 'human-a', 'revoked')`,
		`INSERT INTO gateway_channel_bindings (
		 id, tenant_id, channel_key, account_id, external_tenant_id, external_user_id, human_profile_id, status
		 ) VALUES ('current-weixin', 'minvia', 'weixin', 'bot-a', 'minvia', 'wx-a', 'human-a', 'active')`,
		`INSERT INTO gateway_agent_channel_bindings (tenant_id, channel_key, account_id, agent_profile_id)
		 VALUES ('org-system-runtime-support', 'feishu', 'main', 'agent-a')`,
		`INSERT INTO gateway_channel_binding_codes (
		 code_hash, tenant_id, channel_key, account_id, human_profile_id, default_agent_id, expires_at, consumed_at, created_at
		 ) VALUES ('old-code', 'org-system-runtime-support', 'feishu', 'main', 'human-a', 'agent-a', 20, 10, 1)`,
	} {
		if _, err := store.db.Exec(ctx, statement); err != nil {
			t.Fatalf("seed gateway tenant data: %v\n%s", err, statement)
		}
	}

	changed, err := store.ReassignTenant(ctx, legacyTenant, canonicalTenant)
	if err != nil {
		t.Fatalf("ReassignTenant: %v", err)
	}
	if changed != 6 {
		t.Fatalf("changed = %d, want 6", changed)
	}
	if changed, err = store.ReassignTenant(ctx, legacyTenant, canonicalTenant); err != nil || changed != 0 {
		t.Fatalf("idempotent ReassignTenant = %d, %v", changed, err)
	}

	for _, table := range []string{
		"gateway_channel_accounts", "gateway_channel_bindings", "gateway_channel_binding_codes", "gateway_agent_channel_bindings",
	} {
		var count int
		if err := store.db.QueryRow(ctx, `SELECT COUNT(*) FROM `+table+` WHERE tenant_id = ?`, legacyTenant).Scan(&count); err != nil {
			t.Fatalf("count legacy %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("legacy %s rows = %d", table, count)
		}
	}
	var feishuBindings, weixinBindings, agentBindings int
	if err := store.db.QueryRow(ctx, `SELECT COUNT(*) FROM gateway_channel_bindings WHERE tenant_id = ? AND channel_key = 'feishu'`, canonicalTenant).Scan(&feishuBindings); err != nil {
		t.Fatalf("count canonical Feishu bindings: %v", err)
	}
	if err := store.db.QueryRow(ctx, `SELECT COUNT(*) FROM gateway_channel_bindings WHERE tenant_id = ? AND channel_key = 'weixin'`, canonicalTenant).Scan(&weixinBindings); err != nil {
		t.Fatalf("count canonical Weixin bindings: %v", err)
	}
	if err := store.db.QueryRow(ctx, `SELECT COUNT(*) FROM gateway_agent_channel_bindings WHERE tenant_id = ?`, canonicalTenant).Scan(&agentBindings); err != nil {
		t.Fatalf("count canonical agent bindings: %v", err)
	}
	if feishuBindings != 1 || weixinBindings != 1 || agentBindings != 1 {
		t.Fatalf("canonical bindings Feishu=%d Weixin=%d agent=%d", feishuBindings, weixinBindings, agentBindings)
	}
	var externalTenantID string
	if err := store.db.QueryRow(ctx, `SELECT external_tenant_id FROM gateway_channel_accounts WHERE tenant_id = ? AND channel_key = 'weixin'`, canonicalTenant).Scan(&externalTenantID); err != nil {
		t.Fatalf("load canonical Weixin account: %v", err)
	}
	if externalTenantID != canonicalTenant {
		t.Fatalf("canonical Weixin account external tenant = %q", externalTenantID)
	}
}

func TestCreatingBindingCodeInvalidatesPreviousOutstandingCode(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	input := CreateCodeInput{
		TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "feishu:app-1",
		HumanProfileID: "human-a", DefaultAgentID: "agent-a",
	}
	first, err := store.CreateCode(ctx, input)
	if err != nil {
		t.Fatalf("first CreateCode: %v", err)
	}
	second, err := store.CreateCode(ctx, input)
	if err != nil {
		t.Fatalf("second CreateCode: %v", err)
	}
	identity := ExternalIdentity{
		ChannelKey: "feishu", AccountID: "feishu:app-1", ExternalTenantID: "fs-tenant-a", ExternalUserID: "open-a",
	}
	if _, err := store.ConsumeCode(ctx, first.Value, identity); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("superseded code error = %v", err)
	}
	if _, err := store.ConsumeCode(ctx, second.Value, identity); err != nil {
		t.Fatalf("latest ConsumeCode: %v", err)
	}
	if _, err := store.ConsumeCode(ctx, first.Value, identity); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("superseded code became valid after binding: %v", err)
	}
}

func TestBindingReplayReceiptMigrationUpgradesExistingCodeTable(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	legacy := strings.Replace(schemaStatements("INTEGER")[5], "\n\t\t\tconsumed_binding_id TEXT NOT NULL DEFAULT '',", "", 1)
	if _, err := database.Exec(ctx, legacy); err != nil {
		t.Fatalf("create legacy code table: %v", err)
	}
	if _, err := database.Exec(ctx, `ALTER TABLE gateway_channel_binding_codes ADD COLUMN consumed_binding_id TEXT NOT NULL DEFAULT ''`); err != nil {
		t.Fatalf("apply replay receipt migration: %v", err)
	}
	if _, err := database.Exec(ctx, `INSERT INTO gateway_channel_binding_codes (
		code_hash, tenant_id, channel_key, account_id, human_profile_id, default_agent_id, expires_at, consumed_at, created_at
	) VALUES ('hash', 'tenant-a', 'feishu', 'feishu:app', 'human-a', 'agent-a', 10, 0, 1)`); err != nil {
		t.Fatalf("insert migrated row: %v", err)
	}
	var receipt string
	if err := database.QueryRow(ctx, `SELECT consumed_binding_id FROM gateway_channel_binding_codes WHERE code_hash = 'hash'`).Scan(&receipt); err != nil || receipt != "" {
		t.Fatalf("replay receipt = %q, %v", receipt, err)
	}
}

func TestAccountScopedBindingMigrationPreservesExistingRows(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	for _, statement := range []string{
		`CREATE TABLE gateway_channel_bindings (
			id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, channel_key TEXT NOT NULL, account_id TEXT NOT NULL,
			external_tenant_id TEXT NOT NULL DEFAULT '', external_user_id TEXT NOT NULL, human_profile_id TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '', default_agent_id TEXT NOT NULL DEFAULT '', active_agent_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active', verified_at INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0, UNIQUE (channel_key, account_id, external_tenant_id, external_user_id),
			UNIQUE (tenant_id, channel_key, human_profile_id))`,
		`CREATE INDEX idx_gateway_channel_bindings_external ON gateway_channel_bindings
			(channel_key, account_id, external_tenant_id, external_user_id, status)`,
		`CREATE INDEX idx_gateway_channel_bindings_human ON gateway_channel_bindings
			(tenant_id, human_profile_id, status)`,
		`CREATE TABLE gateway_channel_binding_codes (
			code_hash TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, channel_key TEXT NOT NULL, account_id TEXT NOT NULL,
			human_profile_id TEXT NOT NULL, default_agent_id TEXT NOT NULL DEFAULT '', expires_at INTEGER NOT NULL,
			consumed_at INTEGER NOT NULL DEFAULT 0, consumed_binding_id TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL)`,
		`CREATE UNIQUE INDEX idx_gateway_channel_binding_codes_active ON gateway_channel_binding_codes
			(tenant_id, human_profile_id, channel_key) WHERE consumed_at = 0`,
	} {
		if _, err := database.Exec(ctx, statement); err != nil {
			t.Fatalf("prepare legacy schema: %v", err)
		}
	}
	if _, err := database.Exec(ctx, `INSERT INTO gateway_channel_bindings (
		id, tenant_id, channel_key, account_id, external_tenant_id, external_user_id, human_profile_id,
		default_agent_id, active_agent_id, verified_at, created_at, updated_at
	) VALUES ('gwb-existing', 'tenant-a', 'feishu', 'main', 'fs-a', 'open-main', 'human-a', 'gangbeng', 'gangbeng', 1, 1, 1)`); err != nil {
		t.Fatalf("insert legacy binding: %v", err)
	}
	for _, statement := range accountScopedBindingStatements() {
		if _, err := database.Exec(ctx, statement); err != nil {
			t.Fatalf("apply account-scope migration: %v", err)
		}
	}
	if _, err := database.Exec(ctx, `INSERT INTO gateway_channel_bindings (
		id, tenant_id, channel_key, account_id, external_tenant_id, external_user_id, human_profile_id,
		default_agent_id, active_agent_id, verified_at, created_at, updated_at
	) VALUES ('gwb-jordan', 'tenant-a', 'feishu', 'jordan-belfort', 'fs-a', 'open-jordan', 'human-a', 'jordan-belfort', 'jordan-belfort', 2, 2, 2)`); err != nil {
		t.Fatalf("insert second account binding: %v", err)
	}
	var count int
	if err := database.QueryRow(ctx, `SELECT COUNT(*) FROM gateway_channel_bindings
		WHERE tenant_id = 'tenant-a' AND channel_key = 'feishu' AND human_profile_id = 'human-a'`).Scan(&count); err != nil {
		t.Fatalf("count migrated bindings: %v", err)
	}
	if count != 2 {
		t.Fatalf("migrated bindings = %d, want 2", count)
	}
}

func TestPostgresAccountScopedBindingConstraintRepairDropsTruncatedLegacyConstraint(t *testing.T) {
	statements := strings.Join(postgresAccountScopedBindingConstraintRepairStatements(), "\n")
	if !strings.Contains(statements, "gateway_channel_bindings_tenant_id_channel_key_human_profil_key") {
		t.Fatalf("PostgreSQL repair does not drop the truncated legacy constraint: %s", statements)
	}
}

func TestRegisteredBindingMigrationsCreateCurrentFreshSchema(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: t.Name()},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	defer databases.Close()
	coordinator := migration.NewCoordinator(databases)
	if err := RegisterMigrations(coordinator); err != nil {
		t.Fatalf("RegisterMigrations: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatalf("apply registered binding migrations: %v", err)
	}
	core, err := databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("load core store: %v", err)
	}
	if _, err := core.Exec(ctx, `INSERT INTO gateway_channel_binding_codes (
		code_hash, tenant_id, channel_key, account_id, human_profile_id, default_agent_id, expires_at, consumed_at, created_at
	) VALUES ('hash-fresh', 'tenant-a', 'feishu', 'feishu:app', 'human-a', 'agent-a', 10, 0, 1)`); err != nil {
		t.Fatalf("insert binding code into migrated schema: %v", err)
	}
	var receipt string
	if err := core.QueryRow(ctx, `SELECT consumed_binding_id FROM gateway_channel_binding_codes WHERE code_hash = 'hash-fresh'`).Scan(&receipt); err != nil || receipt != "" {
		t.Fatalf("fresh replay receipt = %q, %v", receipt, err)
	}
	var applied int
	if err := core.QueryRow(ctx, `SELECT COUNT(*) FROM schema_migrations
		WHERE version IN ('0177_gateway_channel_bindings', '0180_gateway_binding_code_channel_scope',
			'0181_gateway_binding_code_replay_receipt', '0193_gateway_binding_account_scope',
			'0194_feishu_agent_channel_bindings', '0200_gateway_binding_account_scope_constraint_repair')`).Scan(&applied); err != nil {
		t.Fatalf("count applied binding migrations: %v", err)
	}
	if applied != 6 {
		t.Fatalf("applied binding migrations = %d, want 6", applied)
	}
}

func TestChangingFeishuAccountKeepsEachAccountCodeActive(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	first, err := store.CreateCode(ctx, CreateCodeInput{
		TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "feishu:app-1", HumanProfileID: "human-a", DefaultAgentID: "agent-a",
	})
	if err != nil {
		t.Fatalf("first CreateCode: %v", err)
	}
	second, err := store.CreateCode(ctx, CreateCodeInput{
		TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "feishu:app-2", HumanProfileID: "human-a", DefaultAgentID: "agent-a",
	})
	if err != nil {
		t.Fatalf("second CreateCode: %v", err)
	}
	firstBinding, err := store.ConsumeCode(ctx, first.Value, ExternalIdentity{
		ChannelKey: "feishu", AccountID: "feishu:app-1", ExternalTenantID: "fs-a", ExternalUserID: "open-a",
	})
	if err != nil {
		t.Fatalf("first account ConsumeCode: %v", err)
	}
	secondBinding, err := store.ConsumeCode(ctx, second.Value, ExternalIdentity{
		ChannelKey: "feishu", AccountID: "feishu:app-2", ExternalTenantID: "fs-a", ExternalUserID: "open-a",
	})
	if err != nil {
		t.Fatalf("latest account ConsumeCode: %v", err)
	}
	if firstBinding.ID == secondBinding.ID {
		t.Fatalf("account bindings reused one id: first=%+v second=%+v", firstBinding, secondBinding)
	}
	bindings, err := store.ListByHuman(ctx, "tenant-a", "human-a")
	if err != nil {
		t.Fatalf("ListByHuman: %v", err)
	}
	if len(bindings) != 2 {
		t.Fatalf("bindings = %+v, want two account-scoped bindings", bindings)
	}
}

func TestBindingCodePreservesOtherFeishuAccountForSameHuman(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	firstCode, err := store.CreateCode(ctx, CreateCodeInput{
		TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "feishu:old", HumanProfileID: "human-a", DefaultAgentID: "agent-a",
	})
	if err != nil {
		t.Fatalf("first CreateCode: %v", err)
	}
	oldIdentity := ExternalIdentity{ChannelKey: "feishu", AccountID: "feishu:old", ExternalTenantID: "fs-old", ExternalUserID: "open-old"}
	first, err := store.ConsumeCode(ctx, firstCode.Value, oldIdentity)
	if err != nil {
		t.Fatalf("first ConsumeCode: %v", err)
	}
	secondCode, err := store.CreateCode(ctx, CreateCodeInput{
		TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "feishu:new", HumanProfileID: "human-a", DefaultAgentID: "agent-b",
	})
	if err != nil {
		t.Fatalf("second CreateCode: %v", err)
	}
	newIdentity := ExternalIdentity{ChannelKey: "feishu", AccountID: "feishu:new", ExternalTenantID: "fs-new", ExternalUserID: "open-new"}
	second, err := store.ConsumeCode(ctx, secondCode.Value, newIdentity)
	if err != nil {
		t.Fatalf("second ConsumeCode: %v", err)
	}
	if second.ID == first.ID || second.AccountID != "feishu:new" || second.CurrentAgentID() != "agent-b" {
		t.Fatalf("second binding = %+v; first = %+v", second, first)
	}
	if resolved, err := store.Resolve(ctx, oldIdentity); err != nil || resolved.ID != first.ID {
		t.Fatalf("old account Resolve = %+v, %v", resolved, err)
	}
	if resolved, err := store.Resolve(ctx, newIdentity); err != nil || resolved.ID != second.ID {
		t.Fatalf("new account Resolve = %+v, %v", resolved, err)
	}
}

func TestVerifiedImportReplacesChangedAccountForSameHuman(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	first, err := store.UpsertVerified(ctx, UpsertInput{
		TenantID: "tenant-a", ChannelKey: "weixin", AccountID: "bot-old", ExternalTenantID: "tenant-a",
		ExternalUserID: "wx-old", HumanProfileID: "human-a", DefaultAgentID: "agent-a",
	})
	if err != nil {
		t.Fatalf("first UpsertVerified: %v", err)
	}
	second, err := store.UpsertVerified(ctx, UpsertInput{
		TenantID: "tenant-a", ChannelKey: "weixin", AccountID: "bot-new", ExternalTenantID: "tenant-a",
		ExternalUserID: "wx-new", HumanProfileID: "human-a", DefaultAgentID: "agent-b",
	})
	if err != nil {
		t.Fatalf("second UpsertVerified: %v", err)
	}
	if second.ID != first.ID || second.AccountID != "bot-new" || second.ExternalUserID != "wx-new" || second.CurrentAgentID() != "agent-b" {
		t.Fatalf("replaced verified binding = %+v; first = %+v", second, first)
	}
	if _, err := store.Resolve(ctx, ExternalIdentity{
		ChannelKey: "weixin", AccountID: "bot-old", ExternalTenantID: "tenant-a", ExternalUserID: "wx-old",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old verified binding still resolved: %v", err)
	}
}

func TestRebindingUpdatesTheEffectiveDefaultAgent(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	identity := ExternalIdentity{
		ChannelKey: "feishu", AccountID: "feishu:app", ExternalTenantID: "fs-a", ExternalUserID: "open-a",
	}
	first, err := store.CreateCode(ctx, CreateCodeInput{
		TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "feishu:app", HumanProfileID: "human-a", DefaultAgentID: "agent-a",
	})
	if err != nil {
		t.Fatalf("first CreateCode: %v", err)
	}
	if _, err := store.ConsumeCode(ctx, first.Value, identity); err != nil {
		t.Fatalf("first ConsumeCode: %v", err)
	}
	second, err := store.CreateCode(ctx, CreateCodeInput{
		TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "feishu:app", HumanProfileID: "human-a", DefaultAgentID: "agent-b",
	})
	if err != nil {
		t.Fatalf("second CreateCode: %v", err)
	}
	rebound, err := store.ConsumeCode(ctx, second.Value, identity)
	if err != nil {
		t.Fatalf("second ConsumeCode: %v", err)
	}
	if rebound.DefaultAgentID != "agent-b" || rebound.ActiveAgentID != "agent-b" || rebound.CurrentAgentID() != "agent-b" {
		t.Fatalf("rebound agent selection = %+v", rebound)
	}
}

func TestBindingCodesRequireSupportedChannelAndDefaultAgent(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	for _, input := range []CreateCodeInput{
		{TenantID: "tenant-a", ChannelKey: "weixin", AccountID: "bot-a", HumanProfileID: "human-a", DefaultAgentID: "agent-a"},
		{TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "feishu:app", HumanProfileID: "human-a"},
	} {
		if _, err := store.CreateCode(ctx, input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("CreateCode(%+v) error = %v", input, err)
		}
	}
}

func TestConsumeBindingCodeDoesNotMaskStorageCorruptionAsInvalidCode(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	code := "ABCD-EFGH-JKLM"
	if _, err := store.db.Exec(ctx, `INSERT INTO gateway_channel_binding_codes (
		code_hash, tenant_id, channel_key, account_id, human_profile_id, default_agent_id, expires_at, consumed_at, created_at
	) VALUES (?, 'tenant-a', 'feishu', 'feishu:app', 'human-a', 'agent-a', 'invalid-expiry', 0, ?)`,
		codeHash(code), store.now().UnixNano()); err != nil {
		t.Fatalf("insert corrupted binding code: %v", err)
	}
	_, err := store.ConsumeCode(ctx, code, ExternalIdentity{
		ChannelKey: "feishu", AccountID: "feishu:app", ExternalTenantID: "fs-a", ExternalUserID: "open-a",
	})
	if err == nil || errors.Is(err, ErrInvalidCode) || !strings.Contains(err.Error(), "load gateway channel binding code") {
		t.Fatalf("ConsumeCode error = %v, want an explicit storage error", err)
	}
}

func TestBindingMigrationDeduplicatesOutstandingCodesBeforeUniqueIndex(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	schema := bindingSchemaStatements("INTEGER", true, false)
	for _, statement := range schema[:len(schema)-1] {
		if _, err := database.Exec(ctx, statement); err != nil {
			t.Fatalf("prepare legacy schema: %v", err)
		}
	}
	for index, hash := range []string{"hash-z-old", "hash-a-new"} {
		if _, err := database.Exec(ctx, `INSERT INTO gateway_channel_binding_codes (
			code_hash, tenant_id, channel_key, account_id, human_profile_id, expires_at, consumed_at, created_at
		) VALUES (?, 'tenant-a', 'feishu', 'feishu:app', 'human-a', 100, 0, ?)`, hash, index+1); err != nil {
			t.Fatalf("seed duplicate code: %v", err)
		}
	}
	for _, statement := range migrationStatements("INTEGER") {
		if _, err := database.Exec(ctx, statement); err != nil {
			t.Fatalf("apply binding migration: %v", err)
		}
	}
	var active int
	if err := database.QueryRow(ctx, `SELECT COUNT(*) FROM gateway_channel_binding_codes WHERE consumed_at = 0`).Scan(&active); err != nil {
		t.Fatalf("count active codes: %v", err)
	}
	if active != 1 {
		t.Fatalf("active codes = %d, want 1", active)
	}
	var activeHash string
	if err := database.QueryRow(ctx, `SELECT code_hash FROM gateway_channel_binding_codes WHERE consumed_at = 0`).Scan(&activeHash); err != nil {
		t.Fatalf("load active code: %v", err)
	}
	if activeHash != "hash-a-new" {
		t.Fatalf("active code = %q, want newest code", activeHash)
	}
	if _, err := database.Exec(ctx, `INSERT INTO gateway_channel_binding_codes (
		code_hash, tenant_id, channel_key, account_id, human_profile_id, expires_at, consumed_at, created_at
	) VALUES ('hash-c', 'tenant-a', 'feishu', 'feishu:app', 'human-a', 100, 0, 3)`); err == nil {
		t.Fatal("active binding code unique index was not created")
	}
}

func TestBindingCodeChannelScopeMigrationUpgradesOldAccountScopedIndex(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()

	statements := bindingSchemaStatements("INTEGER", true, false)
	for _, statement := range statements[:len(statements)-1] {
		if _, err := database.Exec(ctx, statement); err != nil {
			t.Fatalf("prepare old binding schema: %v", err)
		}
	}
	if _, err := database.Exec(ctx, `CREATE UNIQUE INDEX idx_gateway_channel_binding_codes_active
		ON gateway_channel_binding_codes (tenant_id, human_profile_id, channel_key, account_id)
		WHERE consumed_at = 0`); err != nil {
		t.Fatalf("create old active-code index: %v", err)
	}
	for index, accountID := range []string{"feishu:app-old", "feishu:app-new"} {
		if _, err := database.Exec(ctx, `INSERT INTO gateway_channel_binding_codes (
			code_hash, tenant_id, channel_key, account_id, human_profile_id, expires_at, consumed_at, created_at
		) VALUES (?, 'tenant-a', 'feishu', ?, 'human-a', 100, 0, ?)`, "hash-"+accountID, accountID, index+1); err != nil {
			t.Fatalf("seed account-scoped code: %v", err)
		}
	}
	for _, statement := range channelScopedBindingCodeStatements() {
		if _, err := database.Exec(ctx, statement); err != nil {
			t.Fatalf("apply channel-scope migration: %v", err)
		}
	}
	var activeAccount string
	if err := database.QueryRow(ctx, `SELECT account_id FROM gateway_channel_binding_codes WHERE consumed_at = 0`).Scan(&activeAccount); err != nil {
		t.Fatalf("load retained binding code: %v", err)
	}
	if activeAccount != "feishu:app-new" {
		t.Fatalf("retained account = %q", activeAccount)
	}
	if _, err := database.Exec(ctx, `INSERT INTO gateway_channel_binding_codes (
		code_hash, tenant_id, channel_key, account_id, human_profile_id, expires_at, consumed_at, created_at
	) VALUES ('hash-third', 'tenant-a', 'feishu', 'feishu:app-third', 'human-a', 100, 0, 3)`); err == nil {
		t.Fatal("channel-scoped active binding code index was not created")
	}
}

func TestBindingRejectsExternalIdentityCrossTenantReuse(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	first, _ := store.CreateCode(ctx, CreateCodeInput{TenantID: "tenant-a", ChannelKey: "dingtalk", AccountID: "ding:app", HumanProfileID: "human-a", DefaultAgentID: "agent-a"})
	identity := ExternalIdentity{ChannelKey: "dingtalk", AccountID: "ding:app", ExternalTenantID: "corp-a", ExternalUserID: "staff-a"}
	if _, err := store.ConsumeCode(ctx, first.Value, identity); err != nil {
		t.Fatalf("first ConsumeCode: %v", err)
	}
	second, _ := store.CreateCode(ctx, CreateCodeInput{TenantID: "tenant-b", ChannelKey: "dingtalk", AccountID: "ding:app", HumanProfileID: "human-b", DefaultAgentID: "agent-b"})
	if _, err := store.ConsumeCode(ctx, second.Value, identity); !errors.Is(err, ErrConflict) {
		t.Fatalf("cross-tenant ConsumeCode error = %v", err)
	}
}

func TestRevokedBindingCannotResolve(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	code, _ := store.CreateCode(ctx, CreateCodeInput{TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "feishu:app", HumanProfileID: "human-a", DefaultAgentID: "agent-a"})
	identity := ExternalIdentity{ChannelKey: "feishu", AccountID: "feishu:app", ExternalTenantID: "fs-a", ExternalUserID: "open-a"}
	binding, err := store.ConsumeCode(ctx, code.Value, identity)
	if err != nil {
		t.Fatalf("ConsumeCode: %v", err)
	}
	if err := store.Revoke(ctx, "tenant-a", "human-a", binding.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := store.Resolve(ctx, identity); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve after revoke error = %v", err)
	}
}

func TestUpsertVerifiedBatchRollsBackAllBindingsOnConflict(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	if _, err := store.UpsertVerified(ctx, UpsertInput{
		TenantID: "tenant-a", ChannelKey: "weixin", AccountID: "bot-existing", ExternalTenantID: "tenant-a",
		ExternalUserID: "user-existing", HumanProfileID: "human-existing",
	}); err != nil {
		t.Fatalf("seed UpsertVerified: %v", err)
	}
	_, err := store.UpsertVerifiedBatch(ctx, []UpsertInput{
		{
			TenantID: "tenant-a", ChannelKey: "weixin", AccountID: "bot-new", ExternalTenantID: "tenant-a",
			ExternalUserID: "user-new", HumanProfileID: "human-new",
		},
		{
			TenantID: "tenant-a", ChannelKey: "weixin", AccountID: "bot-existing", ExternalTenantID: "tenant-a",
			ExternalUserID: "user-existing", HumanProfileID: "human-other",
		},
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("UpsertVerifiedBatch error = %v", err)
	}
	if _, err := store.Resolve(ctx, ExternalIdentity{
		ChannelKey: "weixin", AccountID: "bot-new", ExternalTenantID: "tenant-a", ExternalUserID: "user-new",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partially committed binding Resolve error = %v", err)
	}
}

func TestReconcileVerifiedChannelBindingsRevokesRemovedTargets(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	first := UpsertInput{
		TenantID: "tenant-a", ChannelKey: "weixin", AccountID: "bot-a", ExternalTenantID: "tenant-a",
		ExternalUserID: "user-a", HumanProfileID: "human-a",
	}
	second := UpsertInput{
		TenantID: "tenant-a", ChannelKey: "weixin", AccountID: "bot-b", ExternalTenantID: "tenant-a",
		ExternalUserID: "user-b", HumanProfileID: "human-b",
	}
	if _, err := store.ReconcileVerifiedChannelBindings(ctx, "weixin", []UpsertInput{first, second}); err != nil {
		t.Fatalf("initial reconcile: %v", err)
	}
	if _, err := store.ReconcileVerifiedChannelBindings(ctx, "weixin", []UpsertInput{second}); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if _, err := store.Resolve(ctx, ExternalIdentity{
		ChannelKey: "weixin", AccountID: "bot-a", ExternalTenantID: "tenant-a", ExternalUserID: "user-a",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removed binding Resolve error = %v", err)
	}
	if _, err := store.Resolve(ctx, ExternalIdentity{
		ChannelKey: "weixin", AccountID: "bot-b", ExternalTenantID: "tenant-a", ExternalUserID: "user-b",
	}); err != nil {
		t.Fatalf("retained binding Resolve: %v", err)
	}
}

func TestRevokedHumanCanBindDifferentExternalIdentity(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	firstCode, err := store.CreateCode(ctx, CreateCodeInput{
		TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "feishu:app", HumanProfileID: "human-a", DefaultAgentID: "agent-a",
	})
	if err != nil {
		t.Fatalf("first CreateCode: %v", err)
	}
	first, err := store.ConsumeCode(ctx, firstCode.Value, ExternalIdentity{
		ChannelKey: "feishu", AccountID: "feishu:app", ExternalTenantID: "fs-a", ExternalUserID: "open-a",
	})
	if err != nil {
		t.Fatalf("first ConsumeCode: %v", err)
	}
	if err := store.Revoke(ctx, "tenant-a", "human-a", first.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	secondCode, err := store.CreateCode(ctx, CreateCodeInput{
		TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "feishu:app", HumanProfileID: "human-a", DefaultAgentID: "agent-a",
	})
	if err != nil {
		t.Fatalf("second CreateCode: %v", err)
	}
	second, err := store.ConsumeCode(ctx, secondCode.Value, ExternalIdentity{
		ChannelKey: "feishu", AccountID: "feishu:app", ExternalTenantID: "fs-a", ExternalUserID: "open-b",
	})
	if err != nil {
		t.Fatalf("second ConsumeCode: %v", err)
	}
	if second.ID != first.ID || second.ExternalUserID != "open-b" || second.Status != "active" {
		t.Fatalf("rebound binding = %+v, first = %+v", second, first)
	}
}
