package binding

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	platformdb "orrery_v2/internal/platform/db"
)

var (
	ErrNotFound      = errors.New("gateway channel binding not found")
	ErrConflict      = errors.New("gateway channel binding conflicts with another user or tenant")
	ErrInvalidCode   = errors.New("gateway channel binding code is invalid or expired")
	ErrInvalidInput  = errors.New("gateway channel binding input is invalid")
	ErrAgentMismatch = errors.New("gateway channel binding code does not match the account owner")
)

const defaultCodeTTL = 10 * time.Minute

type Binding struct {
	ID               string    `json:"id"`
	TenantID         string    `json:"tenant_id"`
	ChannelKey       string    `json:"channel_key"`
	AccountID        string    `json:"account_id"`
	ExternalTenantID string    `json:"external_tenant_id,omitempty"`
	ExternalUserID   string    `json:"external_user_id"`
	HumanProfileID   string    `json:"human_profile_id"`
	DisplayName      string    `json:"display_name,omitempty"`
	DefaultAgentID   string    `json:"default_agent_id,omitempty"`
	ActiveAgentID    string    `json:"active_agent_id,omitempty"`
	Status           string    `json:"status"`
	VerifiedAt       time.Time `json:"verified_at"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (b Binding) CurrentAgentID() string {
	if value := strings.TrimSpace(b.ActiveAgentID); value != "" {
		return value
	}
	return strings.TrimSpace(b.DefaultAgentID)
}

type ExternalIdentity struct {
	ChannelKey       string
	AccountID        string
	ExternalTenantID string
	ExternalUserID   string
	DisplayName      string
}

type Code struct {
	Value     string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

type CreateCodeInput struct {
	TenantID       string
	ChannelKey     string
	AccountID      string
	HumanProfileID string
	DefaultAgentID string
	TTL            time.Duration
}

type UpsertInput struct {
	TenantID         string
	ChannelKey       string
	AccountID        string
	ExternalTenantID string
	ExternalUserID   string
	HumanProfileID   string
	DisplayName      string
	DefaultAgentID   string
	ActiveAgentID    string
}

type Store struct {
	db  *platformdb.Store
	now func() time.Time
}

func NewStore(db *platformdb.Store) *Store {
	return &Store{db: db, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Store) EnsureSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("gateway channel binding store is not configured")
	}
	timeType := "INTEGER"
	if s.db.Dialect() == platformdb.DialectPostgres {
		timeType = "BIGINT"
	}
	for _, statement := range schemaStatements(timeType) {
		if _, err := s.db.Exec(ctx, statement); err != nil {
			return fmt.Errorf("ensure gateway channel binding schema: %w", err)
		}
	}
	return nil
}

// ReassignTenant moves Gateway account ownership and personal routes to a
// canonical tenant. Equivalent destination rows win, which makes this safe to
// repeat after partial tenant migrations.
func (s *Store) ReassignTenant(ctx context.Context, fromTenantID, toTenantID string) (int, error) {
	fromTenantID = strings.TrimSpace(fromTenantID)
	toTenantID = strings.TrimSpace(toTenantID)
	if fromTenantID == "" || toTenantID == "" {
		return 0, ErrInvalidInput
	}
	if fromTenantID == toTenantID {
		return 0, nil
	}
	if err := s.EnsureSchema(ctx); err != nil {
		return 0, err
	}
	changed := 0
	err := s.db.WithTx(ctx, func(tx platformdb.Tx) error {
		for _, table := range []string{
			"gateway_channel_accounts", "gateway_channel_bindings", "gateway_channel_binding_codes", "gateway_agent_channel_bindings",
		} {
			var count int
			if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE tenant_id = ?", fromTenantID).Scan(&count); err != nil {
				return fmt.Errorf("count %s legacy tenant rows: %w", table, err)
			}
			changed += count
		}
		if changed == 0 {
			return nil
		}

		if _, err := tx.ExecContext(ctx, `
DELETE FROM gateway_channel_bindings
WHERE tenant_id = ? AND EXISTS (
	SELECT 1 FROM gateway_channel_bindings AS canonical
	WHERE canonical.tenant_id = ? AND canonical.id <> gateway_channel_bindings.id AND (
		(canonical.channel_key = gateway_channel_bindings.channel_key AND
		 canonical.account_id = gateway_channel_bindings.account_id AND
		 canonical.external_tenant_id = gateway_channel_bindings.external_tenant_id AND
		 canonical.external_user_id = gateway_channel_bindings.external_user_id) OR
		(canonical.channel_key = gateway_channel_bindings.channel_key AND
		 canonical.human_profile_id = gateway_channel_bindings.human_profile_id AND
		 (canonical.channel_key <> 'feishu' OR canonical.account_id = gateway_channel_bindings.account_id))
	)
)`, fromTenantID, toTenantID); err != nil {
			return fmt.Errorf("deduplicate gateway channel bindings: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE gateway_channel_bindings SET
			tenant_id = ?, external_tenant_id = CASE WHEN external_tenant_id = ? THEN ? ELSE external_tenant_id END
			WHERE tenant_id = ?`, toTenantID, fromTenantID, toTenantID, fromTenantID); err != nil {
			return fmt.Errorf("reassign gateway channel bindings: %w", err)
		}

		if _, err := tx.ExecContext(ctx, `
DELETE FROM gateway_channel_binding_codes
WHERE tenant_id = ? AND consumed_at = 0 AND EXISTS (
	SELECT 1 FROM gateway_channel_binding_codes AS canonical
	WHERE canonical.tenant_id = ? AND canonical.consumed_at = 0 AND
		canonical.human_profile_id = gateway_channel_binding_codes.human_profile_id AND
		canonical.channel_key = gateway_channel_binding_codes.channel_key AND
		(canonical.channel_key <> 'feishu' OR canonical.account_id = gateway_channel_binding_codes.account_id)
)`, fromTenantID, toTenantID); err != nil {
			return fmt.Errorf("deduplicate gateway channel binding codes: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE gateway_channel_binding_codes SET tenant_id = ? WHERE tenant_id = ?`, toTenantID, fromTenantID); err != nil {
			return fmt.Errorf("reassign gateway channel binding codes: %w", err)
		}

		if _, err := tx.ExecContext(ctx, `
DELETE FROM gateway_agent_channel_bindings
WHERE tenant_id = ? AND EXISTS (
	SELECT 1 FROM gateway_agent_channel_bindings AS canonical
	WHERE canonical.tenant_id = ? AND canonical.channel_key = gateway_agent_channel_bindings.channel_key AND
		canonical.account_id = gateway_agent_channel_bindings.account_id
)`, fromTenantID, toTenantID); err != nil {
			return fmt.Errorf("deduplicate gateway agent channel bindings: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE gateway_agent_channel_bindings SET tenant_id = ? WHERE tenant_id = ?`, toTenantID, fromTenantID); err != nil {
			return fmt.Errorf("reassign gateway agent channel bindings: %w", err)
		}

		if _, err := tx.ExecContext(ctx, `
INSERT INTO gateway_channel_accounts (
	tenant_id, channel_key, account_id, external_tenant_id, display_name, status, created_at, updated_at
)
SELECT ?, channel_key, account_id,
	CASE WHEN external_tenant_id = ? THEN ? ELSE external_tenant_id END,
	display_name, status, created_at, updated_at
FROM gateway_channel_accounts WHERE tenant_id = ?
ON CONFLICT DO NOTHING`, toTenantID, fromTenantID, toTenantID, fromTenantID); err != nil {
			return fmt.Errorf("copy gateway channel accounts: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM gateway_channel_accounts WHERE tenant_id = ?`, fromTenantID); err != nil {
			return fmt.Errorf("delete legacy gateway channel accounts: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("reassign gateway tenant %s to %s: %w", fromTenantID, toTenantID, err)
	}
	return changed, nil
}

func (s *Store) CreateCode(ctx context.Context, input CreateCodeInput) (Code, error) {
	input = normalizeCodeInput(input)
	if !validCodeInput(input) {
		return Code{}, ErrInvalidInput
	}
	if input.TTL <= 0 {
		input.TTL = defaultCodeTTL
	}
	if input.TTL > 30*time.Minute {
		input.TTL = 30 * time.Minute
	}
	if err := s.EnsureSchema(ctx); err != nil {
		return Code{}, err
	}
	value, err := randomCode()
	if err != nil {
		return Code{}, err
	}
	now := s.now().UTC()
	expiresAt := now.Add(input.TTL)
	err = s.db.WithTx(ctx, func(tx platformdb.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM gateway_channel_binding_codes
			WHERE (consumed_at <> 0 AND consumed_at < ?) OR expires_at < ?`,
			now.Add(-30*24*time.Hour).UnixNano(), now.Add(-30*24*time.Hour).UnixNano()); err != nil {
			return fmt.Errorf("prune gateway channel binding codes: %w", err)
		}
		invalidateQuery := `UPDATE gateway_channel_binding_codes SET consumed_at = ?
			WHERE tenant_id = ? AND human_profile_id = ? AND channel_key = ? AND consumed_at = 0`
		invalidateArgs := []any{now.UnixNano(), input.TenantID, input.HumanProfileID, input.ChannelKey}
		if input.ChannelKey == "feishu" {
			invalidateQuery += ` AND account_id = ?`
			invalidateArgs = append(invalidateArgs, input.AccountID)
		}
		if _, err := tx.ExecContext(ctx, invalidateQuery, invalidateArgs...); err != nil {
			return fmt.Errorf("invalidate previous gateway channel binding code: %w", err)
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO gateway_channel_binding_codes (
			code_hash, tenant_id, channel_key, account_id, human_profile_id, default_agent_id, expires_at, consumed_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?)`, codeHash(value), input.TenantID, input.ChannelKey, input.AccountID,
			input.HumanProfileID, input.DefaultAgentID, expiresAt.UnixNano(), now.UnixNano())
		return err
	})
	if err != nil {
		return Code{}, fmt.Errorf("create gateway channel binding code: %w", err)
	}
	return Code{Value: value, ExpiresAt: expiresAt}, nil
}

func (s *Store) ConsumeCode(ctx context.Context, code string, identity ExternalIdentity) (Binding, error) {
	return s.consumeCode(ctx, code, identity, "")
}

func (s *Store) ConsumeCodeForAgent(ctx context.Context, code string, identity ExternalIdentity, agentProfileID string) (Binding, error) {
	agentProfileID = strings.TrimSpace(agentProfileID)
	if !validText(agentProfileID, 256) {
		return Binding{}, ErrInvalidInput
	}
	return s.consumeCode(ctx, code, identity, agentProfileID)
}

func (s *Store) consumeCode(ctx context.Context, code string, identity ExternalIdentity, requiredAgentID string) (Binding, error) {
	identity = normalizeIdentity(identity)
	if !validBindingCode(code) || !validExternalIdentity(identity) {
		return Binding{}, ErrInvalidInput
	}
	if err := s.EnsureSchema(ctx); err != nil {
		return Binding{}, err
	}
	now := s.now().UTC()
	var result Binding
	err := s.db.WithTx(ctx, func(tx platformdb.Tx) error {
		var tenantID, channelKey, accountID, humanProfileID, defaultAgentID, consumedBindingID string
		var expiresAt, consumedAt int64
		err := tx.QueryRowContext(ctx, `SELECT tenant_id, channel_key, account_id, human_profile_id, default_agent_id, expires_at, consumed_at,
				consumed_binding_id
				FROM gateway_channel_binding_codes WHERE code_hash = ?`, codeHash(code)).Scan(
			&tenantID, &channelKey, &accountID, &humanProfileID, &defaultAgentID, &expiresAt, &consumedAt, &consumedBindingID,
		)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidCode
		}
		if err != nil {
			return fmt.Errorf("load gateway channel binding code: %w", err)
		}
		if expiresAt <= now.UnixNano() {
			return ErrInvalidCode
		}
		if channelKey != identity.ChannelKey || accountID != identity.AccountID {
			return ErrInvalidCode
		}
		if requiredAgentID != "" && defaultAgentID != requiredAgentID {
			return ErrAgentMismatch
		}
		if consumedAt != 0 {
			existing, found, lookupErr := bindingByExternalTx(ctx, tx, identity)
			if lookupErr != nil {
				return lookupErr
			}
			if strings.TrimSpace(consumedBindingID) == "" || !found || existing.ID != consumedBindingID || existing.Status != "active" ||
				existing.TenantID != tenantID || existing.HumanProfileID != humanProfileID {
				return ErrInvalidCode
			}
			result = existing
			return nil
		}
		if err := ensureAccountTx(ctx, tx, tenantID, identity, now); err != nil {
			return err
		}
		existing, found, err := bindingByExternalTx(ctx, tx, identity)
		if err != nil {
			return err
		}
		if found && (existing.TenantID != tenantID || existing.HumanProfileID != humanProfileID) {
			return ErrConflict
		}
		existingByHuman, humanFound, err := bindingByHumanTx(ctx, tx, tenantID, identity.ChannelKey, identity.AccountID, humanProfileID)
		if err != nil {
			return err
		}
		reuseHumanBinding := !found && humanFound
		if !found {
			bindingID, err := newBindingID()
			if err != nil {
				return err
			}
			createdAt := now
			if reuseHumanBinding {
				bindingID = existingByHuman.ID
				createdAt = existingByHuman.CreatedAt
			}
			result = Binding{
				ID:               bindingID,
				TenantID:         tenantID,
				ChannelKey:       identity.ChannelKey,
				AccountID:        identity.AccountID,
				ExternalTenantID: identity.ExternalTenantID,
				ExternalUserID:   identity.ExternalUserID,
				HumanProfileID:   humanProfileID,
				DisplayName:      identity.DisplayName,
				DefaultAgentID:   defaultAgentID,
				ActiveAgentID:    defaultAgentID,
				Status:           "active",
				VerifiedAt:       now,
				CreatedAt:        createdAt,
				UpdatedAt:        now,
			}
			if reuseHumanBinding {
				var update sql.Result
				update, err = tx.ExecContext(ctx, `UPDATE gateway_channel_bindings SET account_id = ?, external_tenant_id = ?,
					external_user_id = ?, display_name = ?, default_agent_id = ?, active_agent_id = ?, status = 'active',
					verified_at = ?, updated_at = ? WHERE id = ? AND tenant_id = ? AND human_profile_id = ?`, result.AccountID,
					result.ExternalTenantID, result.ExternalUserID, result.DisplayName, result.DefaultAgentID,
					result.ActiveAgentID, now.UnixNano(), now.UnixNano(), result.ID, result.TenantID, result.HumanProfileID)
				if err == nil {
					err = requireSingleAffected(update, "replace gateway channel binding")
				}
			} else {
				_, err = tx.ExecContext(ctx, `INSERT INTO gateway_channel_bindings (
					id, tenant_id, channel_key, account_id, external_tenant_id, external_user_id, human_profile_id,
					display_name, default_agent_id, active_agent_id, status, verified_at, created_at, updated_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', ?, ?, ?)`, result.ID, result.TenantID, result.ChannelKey,
					result.AccountID, result.ExternalTenantID, result.ExternalUserID, result.HumanProfileID, result.DisplayName,
					result.DefaultAgentID, result.ActiveAgentID, now.UnixNano(), now.UnixNano(), now.UnixNano())
			}
			if err != nil {
				return classifyConflict("create gateway channel binding", err)
			}
		} else {
			result = existing
			result.DisplayName = identity.DisplayName
			result.ExternalTenantID = identity.ExternalTenantID
			if defaultAgentID != "" {
				result.DefaultAgentID = defaultAgentID
				result.ActiveAgentID = defaultAgentID
			} else {
				result.ActiveAgentID = firstNonBlank(result.ActiveAgentID, result.DefaultAgentID)
			}
			result.Status = "active"
			result.VerifiedAt = now
			result.UpdatedAt = now
			var update sql.Result
			update, err = tx.ExecContext(ctx, `UPDATE gateway_channel_bindings SET external_tenant_id = ?, display_name = ?,
				default_agent_id = ?, active_agent_id = ?, status = 'active', verified_at = ?, updated_at = ? WHERE id = ?`,
				result.ExternalTenantID, result.DisplayName, result.DefaultAgentID, result.ActiveAgentID, now.UnixNano(), now.UnixNano(), result.ID)
			if err == nil {
				err = requireSingleAffected(update, "update gateway channel binding")
			}
			if err != nil {
				return classifyConflict("update gateway channel binding", err)
			}
		}
		updated, err := tx.ExecContext(ctx, `UPDATE gateway_channel_binding_codes SET consumed_at = ?, consumed_binding_id = ?
			WHERE code_hash = ? AND consumed_at = 0`, now.UnixNano(), result.ID, codeHash(code))
		if err != nil {
			return fmt.Errorf("consume gateway channel binding code: %w", err)
		}
		rows, err := updated.RowsAffected()
		if err != nil {
			return fmt.Errorf("consume gateway channel binding code rows affected: %w", err)
		}
		if rows != 1 {
			return ErrInvalidCode
		}
		return nil
	})
	if err != nil {
		return Binding{}, err
	}
	return result, nil
}

func (s *Store) Resolve(ctx context.Context, identity ExternalIdentity) (Binding, error) {
	identity = normalizeIdentity(identity)
	if !validExternalIdentity(identity) {
		return Binding{}, ErrInvalidInput
	}
	if err := s.EnsureSchema(ctx); err != nil {
		return Binding{}, err
	}
	row := s.db.QueryRow(ctx, `SELECT id, tenant_id, channel_key, account_id, external_tenant_id, external_user_id,
		human_profile_id, display_name, default_agent_id, active_agent_id, status, verified_at, created_at, updated_at
		FROM gateway_channel_bindings
		WHERE channel_key = ? AND account_id = ? AND external_tenant_id = ? AND external_user_id = ? AND status = 'active'`,
		identity.ChannelKey, identity.AccountID, identity.ExternalTenantID, identity.ExternalUserID)
	binding, err := scanBinding(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Binding{}, ErrNotFound
	}
	return binding, err
}

// UpsertVerified imports a binding that has already been verified by a
// channel-specific trusted flow, such as the existing Weixin QR login.
func (s *Store) UpsertVerified(ctx context.Context, input UpsertInput) (Binding, error) {
	input, identity, err := normalizeVerifiedInput(input)
	if err != nil {
		return Binding{}, ErrInvalidInput
	}
	if err := s.EnsureSchema(ctx); err != nil {
		return Binding{}, err
	}
	now := s.now().UTC()
	var result Binding
	err = s.db.WithTx(ctx, func(tx platformdb.Tx) error {
		var upsertErr error
		result, upsertErr = upsertVerifiedTx(ctx, tx, input, identity, now)
		return upsertErr
	})
	return result, err
}

// UpsertVerifiedBatch applies a trusted binding import atomically. It is used
// when a Gateway configuration reload may add several account bindings.
func (s *Store) UpsertVerifiedBatch(ctx context.Context, inputs []UpsertInput) ([]Binding, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	normalized := make([]UpsertInput, len(inputs))
	identities := make([]ExternalIdentity, len(inputs))
	for index, input := range inputs {
		var err error
		normalized[index], identities[index], err = normalizeVerifiedInput(input)
		if err != nil {
			return nil, ErrInvalidInput
		}
	}
	if err := s.EnsureSchema(ctx); err != nil {
		return nil, err
	}
	results := make([]Binding, len(inputs))
	now := s.now().UTC()
	err := s.db.WithTx(ctx, func(tx platformdb.Tx) error {
		for index := range normalized {
			binding, err := upsertVerifiedTx(ctx, tx, normalized[index], identities[index], now)
			if err != nil {
				return err
			}
			results[index] = binding
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

// ReconcileVerifiedChannelBindings atomically replaces the trusted imported
// bindings for one channel. Bindings removed from the source configuration are
// revoked so they cannot remain valid outbound targets.
func (s *Store) ReconcileVerifiedChannelBindings(ctx context.Context, channelKey string, inputs []UpsertInput) ([]Binding, error) {
	channelKey = strings.ToLower(strings.TrimSpace(channelKey))
	if !validChannelKey(channelKey) {
		return nil, ErrInvalidInput
	}
	normalized := make([]UpsertInput, len(inputs))
	identities := make([]ExternalIdentity, len(inputs))
	for index, input := range inputs {
		var err error
		normalized[index], identities[index], err = normalizeVerifiedInput(input)
		if err != nil || identities[index].ChannelKey != channelKey {
			return nil, ErrInvalidInput
		}
	}
	if err := s.EnsureSchema(ctx); err != nil {
		return nil, err
	}
	results := make([]Binding, len(inputs))
	now := s.now().UTC()
	err := s.db.WithTx(ctx, func(tx platformdb.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE gateway_channel_accounts SET status = 'disconnected', updated_at = ?
			WHERE channel_key = ? AND status = 'connected'`, now.UnixNano(), channelKey); err != nil {
			return fmt.Errorf("disconnect stale gateway channel accounts: %w", err)
		}
		for index := range normalized {
			binding, err := upsertVerifiedTx(ctx, tx, normalized[index], identities[index], now)
			if err != nil {
				return err
			}
			results[index] = binding
		}
		query := `UPDATE gateway_channel_bindings SET status = 'revoked', updated_at = ? WHERE channel_key = ? AND status = 'active'`
		args := []any{now.UnixNano(), channelKey}
		if len(results) > 0 {
			placeholders := make([]string, len(results))
			for index, binding := range results {
				placeholders[index] = "?"
				args = append(args, binding.ID)
			}
			query += ` AND id NOT IN (` + strings.Join(placeholders, ",") + `)`
		}
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("revoke stale gateway channel bindings: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

func normalizeVerifiedInput(input UpsertInput) (UpsertInput, ExternalIdentity, error) {
	input.TenantID = strings.TrimSpace(input.TenantID)
	identity := normalizeIdentity(ExternalIdentity{
		ChannelKey: input.ChannelKey, AccountID: input.AccountID, ExternalTenantID: input.ExternalTenantID,
		ExternalUserID: input.ExternalUserID, DisplayName: input.DisplayName,
	})
	input.HumanProfileID = strings.TrimSpace(input.HumanProfileID)
	input.DefaultAgentID = strings.TrimSpace(input.DefaultAgentID)
	input.ActiveAgentID = strings.TrimSpace(input.ActiveAgentID)
	if !validText(input.TenantID, 256) || !validExternalIdentity(identity) || !validText(input.HumanProfileID, 256) ||
		!validOptionalText(input.DefaultAgentID, 256) || !validOptionalText(input.ActiveAgentID, 256) {
		return UpsertInput{}, ExternalIdentity{}, ErrInvalidInput
	}
	return input, identity, nil
}

func upsertVerifiedTx(ctx context.Context, tx platformdb.Tx, input UpsertInput, identity ExternalIdentity, now time.Time) (Binding, error) {
	if err := ensureAccountTx(ctx, tx, input.TenantID, identity, now); err != nil {
		return Binding{}, err
	}
	existing, found, err := bindingByExternalTx(ctx, tx, identity)
	if err != nil {
		return Binding{}, err
	}
	if found && (existing.TenantID != input.TenantID || existing.HumanProfileID != input.HumanProfileID) {
		return Binding{}, ErrConflict
	}
	byHuman, humanFound, err := bindingByHumanTx(ctx, tx, input.TenantID, identity.ChannelKey, identity.AccountID, input.HumanProfileID)
	if err != nil {
		return Binding{}, err
	}
	reuseHumanBinding := !found && humanFound
	if !found {
		bindingID, err := newBindingID()
		if err != nil {
			return Binding{}, err
		}
		createdAt := now
		if reuseHumanBinding {
			bindingID = byHuman.ID
			createdAt = byHuman.CreatedAt
		}
		result := Binding{
			ID: bindingID, TenantID: input.TenantID, ChannelKey: identity.ChannelKey, AccountID: identity.AccountID,
			ExternalTenantID: identity.ExternalTenantID, ExternalUserID: identity.ExternalUserID, HumanProfileID: input.HumanProfileID,
			DisplayName: identity.DisplayName, DefaultAgentID: input.DefaultAgentID,
			ActiveAgentID: firstNonBlank(input.ActiveAgentID, input.DefaultAgentID), Status: "active",
			VerifiedAt: now, CreatedAt: createdAt, UpdatedAt: now,
		}
		if reuseHumanBinding {
			var update sql.Result
			update, err = tx.ExecContext(ctx, `UPDATE gateway_channel_bindings SET account_id = ?, external_tenant_id = ?,
				external_user_id = ?, display_name = ?, default_agent_id = ?, active_agent_id = ?, status = 'active',
				verified_at = ?, updated_at = ? WHERE id = ? AND tenant_id = ? AND human_profile_id = ?`, result.AccountID,
				result.ExternalTenantID, result.ExternalUserID, result.DisplayName, result.DefaultAgentID,
				result.ActiveAgentID, now.UnixNano(), now.UnixNano(), result.ID, result.TenantID, result.HumanProfileID)
			if err == nil {
				err = requireSingleAffected(update, "replace verified gateway binding")
			}
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO gateway_channel_bindings (
				id, tenant_id, channel_key, account_id, external_tenant_id, external_user_id, human_profile_id,
				display_name, default_agent_id, active_agent_id, status, verified_at, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', ?, ?, ?)`, result.ID, result.TenantID, result.ChannelKey,
				result.AccountID, result.ExternalTenantID, result.ExternalUserID, result.HumanProfileID, result.DisplayName,
				result.DefaultAgentID, result.ActiveAgentID, now.UnixNano(), now.UnixNano(), now.UnixNano())
		}
		return result, classifyConflict("create verified gateway binding", err)
	}
	result := existing
	result.ExternalTenantID = identity.ExternalTenantID
	result.DisplayName = firstNonBlank(identity.DisplayName, result.DisplayName)
	result.DefaultAgentID = firstNonBlank(input.DefaultAgentID, result.DefaultAgentID)
	result.ActiveAgentID = firstNonBlank(input.ActiveAgentID, result.ActiveAgentID, result.DefaultAgentID)
	result.Status = "active"
	result.VerifiedAt = now
	result.UpdatedAt = now
	var update sql.Result
	update, err = tx.ExecContext(ctx, `UPDATE gateway_channel_bindings SET external_tenant_id = ?, display_name = ?,
		default_agent_id = ?, active_agent_id = ?, status = 'active', verified_at = ?, updated_at = ? WHERE id = ?`,
		result.ExternalTenantID, result.DisplayName, result.DefaultAgentID, result.ActiveAgentID, now.UnixNano(), now.UnixNano(), result.ID)
	if err == nil {
		err = requireSingleAffected(update, "update verified gateway binding")
	}
	return result, classifyConflict("update verified gateway binding", err)
}

func (s *Store) ListByHuman(ctx context.Context, tenantID, humanProfileID string) ([]Binding, error) {
	tenantID = strings.TrimSpace(tenantID)
	humanProfileID = strings.TrimSpace(humanProfileID)
	if !validText(tenantID, 256) || !validText(humanProfileID, 256) {
		return nil, ErrInvalidInput
	}
	if err := s.EnsureSchema(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `SELECT id, tenant_id, channel_key, account_id, external_tenant_id, external_user_id,
		human_profile_id, display_name, default_agent_id, active_agent_id, status, verified_at, created_at, updated_at
		FROM gateway_channel_bindings WHERE tenant_id = ? AND human_profile_id = ? AND status = 'active'
		ORDER BY channel_key, created_at`, tenantID, humanProfileID)
	if err != nil {
		return nil, fmt.Errorf("list gateway channel bindings: %w", err)
	}
	defer rows.Close()
	var result []Binding
	for rows.Next() {
		binding, err := scanBinding(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, binding)
	}
	return result, rows.Err()
}

func (s *Store) Revoke(ctx context.Context, tenantID, humanProfileID, bindingID string) error {
	tenantID = strings.TrimSpace(tenantID)
	humanProfileID = strings.TrimSpace(humanProfileID)
	bindingID = strings.TrimSpace(bindingID)
	if !validText(tenantID, 256) || !validText(humanProfileID, 256) || !validText(bindingID, 256) {
		return ErrInvalidInput
	}
	if err := s.EnsureSchema(ctx); err != nil {
		return err
	}
	result, err := s.db.Exec(ctx, `UPDATE gateway_channel_bindings SET status = 'revoked', updated_at = ?
		WHERE id = ? AND tenant_id = ? AND human_profile_id = ? AND status = 'active'`,
		s.now().UTC().UnixNano(), bindingID, tenantID, humanProfileID)
	if err != nil {
		return fmt.Errorf("revoke gateway channel binding: %w", err)
	}
	rows, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return fmt.Errorf("revoke gateway channel binding rows affected: %w", rowsErr)
	}
	if rows != 1 {
		return ErrNotFound
	}
	return nil
}

func normalizeCodeInput(input CreateCodeInput) CreateCodeInput {
	input.TenantID = strings.TrimSpace(input.TenantID)
	input.ChannelKey = strings.ToLower(strings.TrimSpace(input.ChannelKey))
	input.AccountID = strings.TrimSpace(input.AccountID)
	input.HumanProfileID = strings.TrimSpace(input.HumanProfileID)
	input.DefaultAgentID = strings.TrimSpace(input.DefaultAgentID)
	return input
}

func normalizeIdentity(identity ExternalIdentity) ExternalIdentity {
	identity.ChannelKey = strings.ToLower(strings.TrimSpace(identity.ChannelKey))
	identity.AccountID = strings.TrimSpace(identity.AccountID)
	identity.ExternalTenantID = strings.TrimSpace(identity.ExternalTenantID)
	identity.ExternalUserID = strings.TrimSpace(identity.ExternalUserID)
	identity.DisplayName = strings.TrimSpace(identity.DisplayName)
	return identity
}

func validCodeInput(input CreateCodeInput) bool {
	return validText(input.TenantID, 256) && validBindingCodeChannel(input.ChannelKey) && validText(input.AccountID, 512) &&
		validText(input.HumanProfileID, 256) && validText(input.DefaultAgentID, 256)
}

func validBindingCodeChannel(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "feishu", "dingtalk":
		return true
	default:
		return false
	}
}

func validBindingCode(code string) bool {
	code = strings.TrimSpace(code)
	return code != "" && len(code) <= 32
}

func validExternalIdentity(identity ExternalIdentity) bool {
	return validChannelKey(identity.ChannelKey) && validText(identity.AccountID, 512) &&
		validOptionalText(identity.ExternalTenantID, 512) && validText(identity.ExternalUserID, 512) &&
		validOptionalText(identity.DisplayName, 1000)
}

func validChannelKey(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "weixin", "feishu", "dingtalk":
		return true
	default:
		return false
	}
}

func validText(value string, limit int) bool {
	value = strings.TrimSpace(value)
	return value != "" && len([]rune(value)) <= limit
}

func validOptionalText(value string, limit int) bool {
	return len([]rune(strings.TrimSpace(value))) <= limit
}

func randomCode() (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate gateway binding code: %w", err)
	}
	for i := range raw {
		raw[i] = alphabet[int(raw[i])%len(alphabet)]
	}
	return string(raw[:4]) + "-" + string(raw[4:8]) + "-" + string(raw[8:]), nil
}

func codeHash(value string) string {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(value), "-", ""))
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

func newBindingID() (string, error) {
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate gateway channel binding id: %w", err)
	}
	return "gwb_" + hex.EncodeToString(random), nil
}

func ensureAccountTx(ctx context.Context, tx platformdb.Tx, tenantID string, identity ExternalIdentity, now time.Time) error {
	var existingTenant string
	err := tx.QueryRowContext(ctx, `SELECT tenant_id FROM gateway_channel_accounts
		WHERE channel_key = ? AND account_id = ? AND external_tenant_id = ?`, identity.ChannelKey, identity.AccountID, identity.ExternalTenantID).Scan(&existingTenant)
	if err == nil && existingTenant != tenantID {
		return ErrConflict
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("load gateway channel account: %w", err)
	}
	if err == nil {
		result, updateErr := tx.ExecContext(ctx, `UPDATE gateway_channel_accounts SET status = 'connected', updated_at = ?
			WHERE tenant_id = ? AND channel_key = ? AND account_id = ?`, now.UnixNano(), tenantID, identity.ChannelKey, identity.AccountID)
		if updateErr != nil {
			return updateErr
		}
		return requireSingleAffected(result, "reconnect gateway channel account")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO gateway_channel_accounts (
		tenant_id, channel_key, account_id, external_tenant_id, display_name, status, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, 'connected', ?, ?)`, tenantID, identity.ChannelKey, identity.AccountID,
		identity.ExternalTenantID, identity.AccountID, now.UnixNano(), now.UnixNano())
	if err != nil {
		return classifyConflict("create gateway channel account", err)
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanBinding(row rowScanner) (Binding, error) {
	var binding Binding
	var verifiedAt, createdAt, updatedAt int64
	err := row.Scan(&binding.ID, &binding.TenantID, &binding.ChannelKey, &binding.AccountID, &binding.ExternalTenantID,
		&binding.ExternalUserID, &binding.HumanProfileID, &binding.DisplayName, &binding.DefaultAgentID, &binding.ActiveAgentID,
		&binding.Status, &verifiedAt, &createdAt, &updatedAt)
	if err != nil {
		return Binding{}, err
	}
	binding.VerifiedAt = time.Unix(0, verifiedAt).UTC()
	binding.CreatedAt = time.Unix(0, createdAt).UTC()
	binding.UpdatedAt = time.Unix(0, updatedAt).UTC()
	return binding, nil
}

func bindingByExternalTx(ctx context.Context, tx platformdb.Tx, identity ExternalIdentity) (Binding, bool, error) {
	binding, err := scanBinding(tx.QueryRowContext(ctx, `SELECT id, tenant_id, channel_key, account_id, external_tenant_id, external_user_id,
		human_profile_id, display_name, default_agent_id, active_agent_id, status, verified_at, created_at, updated_at
		FROM gateway_channel_bindings WHERE channel_key = ? AND account_id = ? AND external_tenant_id = ? AND external_user_id = ?`,
		identity.ChannelKey, identity.AccountID, identity.ExternalTenantID, identity.ExternalUserID))
	if errors.Is(err, sql.ErrNoRows) {
		return Binding{}, false, nil
	}
	return binding, err == nil, err
}

func bindingByHumanTx(ctx context.Context, tx platformdb.Tx, tenantID, channelKey, accountID, humanID string) (Binding, bool, error) {
	query := `SELECT id, tenant_id, channel_key, account_id, external_tenant_id, external_user_id,
		human_profile_id, display_name, default_agent_id, active_agent_id, status, verified_at, created_at, updated_at
		FROM gateway_channel_bindings WHERE tenant_id = ? AND channel_key = ? AND human_profile_id = ?`
	args := []any{tenantID, channelKey, humanID}
	if channelKey == "feishu" {
		query += ` AND account_id = ?`
		args = append(args, accountID)
	}
	binding, err := scanBinding(tx.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return Binding{}, false, nil
	}
	return binding, err == nil, err
}

func classifyConflict(operation string, err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "unique") || strings.Contains(message, "duplicate") {
		return fmt.Errorf("%s: %w", operation, ErrConflict)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func requireSingleAffected(result sql.Result, operation string) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s rows affected: %w", operation, err)
	}
	if rows != 1 {
		return fmt.Errorf("%s: expected one row, updated %d", operation, rows)
	}
	return nil
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
