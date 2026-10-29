package binding

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	platformdb "orrery_v2/internal/platform/db"
)

type AgentChannelBinding struct {
	TenantID       string
	ChannelKey     string
	AccountID      string
	AgentProfileID string
	InboundEnabled bool
	Revision       int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type PutAgentChannelBindingInput struct {
	TenantID               string
	ChannelKey             string
	AccountID              string
	AgentProfileID         string
	InboundEnabled         bool
	ExpectedRevision       int64
	ExpectedAgentProfileID string
}

func (s *Store) PutAgentChannelBinding(ctx context.Context, input PutAgentChannelBindingInput) (AgentChannelBinding, error) {
	input = normalizeAgentChannelBindingInput(input)
	if !validAgentChannelBindingInput(input) {
		return AgentChannelBinding{}, ErrInvalidInput
	}
	if err := s.EnsureSchema(ctx); err != nil {
		return AgentChannelBinding{}, err
	}
	now := s.now().UTC()
	var result AgentChannelBinding
	err := s.db.WithTx(ctx, func(tx platformdb.Tx) error {
		var tenantID, agentProfileID string
		var revision, createdAt int64
		lookupErr := tx.QueryRowContext(ctx, `SELECT tenant_id, agent_profile_id, revision, created_at
			FROM gateway_agent_channel_bindings WHERE channel_key = ? AND account_id = ?`,
			input.ChannelKey, input.AccountID).Scan(&tenantID, &agentProfileID, &revision, &createdAt)
		switch {
		case errors.Is(lookupErr, sql.ErrNoRows):
			if input.ExpectedRevision != 0 || input.ExpectedAgentProfileID != "" {
				return ErrConflict
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO gateway_agent_channel_bindings (
				tenant_id, channel_key, account_id, agent_profile_id, inbound_enabled, revision, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, 1, ?, ?)`, input.TenantID, input.ChannelKey, input.AccountID,
				input.AgentProfileID, agentChannelBoolInt(input.InboundEnabled), now.UnixNano(), now.UnixNano())
			if err != nil {
				return classifyConflict("create gateway agent channel binding", err)
			}
		case lookupErr != nil:
			return fmt.Errorf("load gateway agent channel binding revision: %w", lookupErr)
		case tenantID != input.TenantID || revision != input.ExpectedRevision:
			return ErrConflict
		case input.ExpectedAgentProfileID != "" && agentProfileID != input.ExpectedAgentProfileID:
			return ErrConflict
		default:
			updated, err := tx.ExecContext(ctx, `UPDATE gateway_agent_channel_bindings SET
				agent_profile_id = ?, inbound_enabled = ?, revision = revision + 1, updated_at = ?
				WHERE tenant_id = ? AND channel_key = ? AND account_id = ? AND agent_profile_id = ? AND revision = ?`,
				input.AgentProfileID, agentChannelBoolInt(input.InboundEnabled), now.UnixNano(), input.TenantID, input.ChannelKey,
				input.AccountID, agentProfileID, input.ExpectedRevision)
			if err != nil {
				return fmt.Errorf("update gateway agent channel binding: %w", err)
			}
			if err := requireSingleAffected(updated, "update gateway agent channel binding"); err != nil {
				return ErrConflict
			}
		}
		var scanErr error
		result, scanErr = scanAgentChannelBinding(tx.QueryRowContext(ctx, agentChannelBindingSelect+`
			WHERE tenant_id = ? AND channel_key = ? AND account_id = ?`, input.TenantID, input.ChannelKey, input.AccountID))
		return scanErr
	})
	if err != nil {
		return AgentChannelBinding{}, err
	}
	return result, nil
}

func (s *Store) ResolveAgentChannelBinding(ctx context.Context, channelKey, accountID string) (AgentChannelBinding, error) {
	channelKey = strings.ToLower(strings.TrimSpace(channelKey))
	accountID = strings.TrimSpace(accountID)
	if channelKey != "feishu" || !validText(accountID, 512) {
		return AgentChannelBinding{}, ErrInvalidInput
	}
	if err := s.EnsureSchema(ctx); err != nil {
		return AgentChannelBinding{}, err
	}
	binding, err := scanAgentChannelBinding(s.db.QueryRow(ctx, agentChannelBindingSelect+`
		WHERE channel_key = ? AND account_id = ?`, channelKey, accountID))
	if errors.Is(err, sql.ErrNoRows) {
		return AgentChannelBinding{}, ErrNotFound
	}
	return binding, err
}

func (s *Store) ListAgentChannelBindings(ctx context.Context, tenantID, agentProfileID string) ([]AgentChannelBinding, error) {
	tenantID = strings.TrimSpace(tenantID)
	agentProfileID = strings.TrimSpace(agentProfileID)
	if !validText(tenantID, 256) || !validText(agentProfileID, 256) {
		return nil, ErrInvalidInput
	}
	return s.listAgentChannelBindings(ctx, `WHERE tenant_id = ? AND agent_profile_id = ? ORDER BY channel_key, account_id`, tenantID, agentProfileID)
}

func (s *Store) ListTenantAgentChannelBindings(ctx context.Context, tenantID string) ([]AgentChannelBinding, error) {
	tenantID = strings.TrimSpace(tenantID)
	if !validText(tenantID, 256) {
		return nil, ErrInvalidInput
	}
	return s.listAgentChannelBindings(ctx, `WHERE tenant_id = ? ORDER BY channel_key, account_id`, tenantID)
}

func (s *Store) listAgentChannelBindings(ctx context.Context, suffix string, args ...any) ([]AgentChannelBinding, error) {
	if err := s.EnsureSchema(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, agentChannelBindingSelect+suffix, args...)
	if err != nil {
		return nil, fmt.Errorf("list gateway agent channel bindings: %w", err)
	}
	defer rows.Close()
	bindings := []AgentChannelBinding{}
	for rows.Next() {
		binding, err := scanAgentChannelBinding(rows)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	return bindings, rows.Err()
}

func (s *Store) DeleteOwnedAgentChannelBinding(ctx context.Context, tenantID, channelKey, accountID, agentProfileID string, revision int64) error {
	tenantID = strings.TrimSpace(tenantID)
	channelKey = strings.ToLower(strings.TrimSpace(channelKey))
	accountID = strings.TrimSpace(accountID)
	agentProfileID = strings.TrimSpace(agentProfileID)
	if !validText(tenantID, 256) || channelKey != "feishu" || !validText(accountID, 512) ||
		!validText(agentProfileID, 256) || revision <= 0 {
		return ErrInvalidInput
	}
	if err := s.EnsureSchema(ctx); err != nil {
		return err
	}
	deleted, err := s.db.Exec(ctx, `DELETE FROM gateway_agent_channel_bindings
		WHERE tenant_id = ? AND channel_key = ? AND account_id = ? AND agent_profile_id = ? AND revision = ?`,
		tenantID, channelKey, accountID, agentProfileID, revision)
	if err != nil {
		return fmt.Errorf("delete gateway agent channel binding: %w", err)
	}
	rows, err := deleted.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete gateway agent channel binding rows affected: %w", err)
	}
	if rows == 1 {
		return nil
	}
	var found int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM gateway_agent_channel_bindings
		WHERE channel_key = ? AND account_id = ?`, channelKey, accountID).Scan(&found); err != nil {
		return fmt.Errorf("check gateway agent channel binding after delete: %w", err)
	}
	if found == 0 {
		return ErrNotFound
	}
	return ErrConflict
}

func normalizeAgentChannelBindingInput(input PutAgentChannelBindingInput) PutAgentChannelBindingInput {
	input.TenantID = strings.TrimSpace(input.TenantID)
	input.ChannelKey = strings.ToLower(strings.TrimSpace(input.ChannelKey))
	input.AccountID = strings.TrimSpace(input.AccountID)
	input.AgentProfileID = strings.TrimSpace(input.AgentProfileID)
	input.ExpectedAgentProfileID = strings.TrimSpace(input.ExpectedAgentProfileID)
	return input
}

func validAgentChannelBindingInput(input PutAgentChannelBindingInput) bool {
	return validText(input.TenantID, 256) && input.ChannelKey == "feishu" && validText(input.AccountID, 512) &&
		validText(input.AgentProfileID, 256) && input.ExpectedRevision >= 0 &&
		(input.ExpectedAgentProfileID == "" || validText(input.ExpectedAgentProfileID, 256))
}

const agentChannelBindingSelect = `SELECT tenant_id, channel_key, account_id, agent_profile_id,
	inbound_enabled, revision, created_at, updated_at FROM gateway_agent_channel_bindings `

func scanAgentChannelBinding(row rowScanner) (AgentChannelBinding, error) {
	var binding AgentChannelBinding
	var inboundEnabled, createdAt, updatedAt int64
	err := row.Scan(&binding.TenantID, &binding.ChannelKey, &binding.AccountID, &binding.AgentProfileID,
		&inboundEnabled, &binding.Revision, &createdAt, &updatedAt)
	if err != nil {
		return AgentChannelBinding{}, err
	}
	binding.InboundEnabled = inboundEnabled != 0
	binding.CreatedAt = time.Unix(0, createdAt).UTC()
	binding.UpdatedAt = time.Unix(0, updatedAt).UTC()
	return binding, nil
}

func agentChannelBoolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
