package knowledgeprovider

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

var (
	ErrNotFound      = errors.New("knowledge provider record not found")
	ErrInvalid       = errors.New("knowledge provider request invalid")
	ErrAlreadyExists = errors.New("knowledge provider binding already exists")
)

type Store struct {
	database *db.Store
}

func NewStore(database *db.Store) *Store {
	return &Store{database: database}
}

func (s *Store) SaveBinding(ctx context.Context, binding Binding) error {
	if s == nil || s.database == nil {
		return fmt.Errorf("knowledge provider store is required")
	}
	if strings.TrimSpace(binding.ID) == "" || strings.TrimSpace(binding.MCPServerID) == "" {
		return fmt.Errorf("binding id and MCP server id are required")
	}
	payload, err := json.Marshal(binding)
	if err != nil {
		return fmt.Errorf("encode knowledge provider binding: %w", err)
	}
	_, err = s.database.Exec(ctx, `
		INSERT INTO knowledge_provider_bindings (id, mcp_server_id, status, payload)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			mcp_server_id=excluded.mcp_server_id,
			status=excluded.status,
			payload=excluded.payload`, binding.ID, binding.MCPServerID, string(binding.Status), string(payload))
	if err != nil {
		return fmt.Errorf("save knowledge provider binding: %w", err)
	}
	return nil
}

func (s *Store) GetBinding(ctx context.Context, id string) (Binding, error) {
	if s == nil || s.database == nil {
		return Binding{}, fmt.Errorf("knowledge provider store is required")
	}
	var payload string
	if err := s.database.QueryRow(ctx, `SELECT payload FROM knowledge_provider_bindings WHERE id = ?`, strings.TrimSpace(id)).Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Binding{}, ErrNotFound
		}
		return Binding{}, fmt.Errorf("get knowledge provider binding: %w", err)
	}
	var binding Binding
	if err := json.Unmarshal([]byte(payload), &binding); err != nil {
		return Binding{}, fmt.Errorf("decode knowledge provider binding: %w", err)
	}
	return binding, nil
}

func (s *Store) ListBindings(ctx context.Context) ([]Binding, error) {
	if s == nil || s.database == nil {
		return nil, fmt.Errorf("knowledge provider store is required")
	}
	rows, err := s.database.Query(ctx, `SELECT payload FROM knowledge_provider_bindings ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list knowledge provider bindings: %w", err)
	}
	defer rows.Close()
	bindings := []Binding{}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("scan knowledge provider binding: %w", err)
		}
		var binding Binding
		if err := json.Unmarshal([]byte(payload), &binding); err != nil {
			return nil, fmt.Errorf("decode knowledge provider binding: %w", err)
		}
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate knowledge provider bindings: %w", err)
	}
	return bindings, nil
}

func (s *Store) SaveMapping(ctx context.Context, mapping ExternalMapping) error {
	if s == nil || s.database == nil {
		return fmt.Errorf("knowledge provider store is required")
	}
	if strings.TrimSpace(mapping.BindingID) == "" || strings.TrimSpace(mapping.ExternalID) == "" || strings.TrimSpace(mapping.LocalID) == "" || mapping.Kind == "" {
		return fmt.Errorf("mapping binding, kind, external id, and local id are required")
	}
	if mapping.UpdatedAt.IsZero() {
		mapping.UpdatedAt = time.Now().UTC()
	}
	_, err := s.database.Exec(ctx, `
		INSERT INTO knowledge_external_mappings (id, binding_id, kind, external_id, local_id, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(binding_id, kind, external_id) DO UPDATE SET
			local_id=excluded.local_id,
			updated_at=excluded.updated_at`, mappingKey(mapping.BindingID, mapping.Kind, mapping.ExternalID), mapping.BindingID, string(mapping.Kind), mapping.ExternalID, mapping.LocalID, mapping.UpdatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("save knowledge external mapping: %w", err)
	}
	return nil
}

func (s *Store) FindMapping(ctx context.Context, bindingID string, kind ResourceKind, externalID string) (ExternalMapping, error) {
	if s == nil || s.database == nil {
		return ExternalMapping{}, fmt.Errorf("knowledge provider store is required")
	}
	var mapping ExternalMapping
	var kindValue, updatedAt string
	err := s.database.QueryRow(ctx, `
		SELECT binding_id, kind, external_id, local_id, updated_at
		FROM knowledge_external_mappings
		WHERE binding_id = ? AND kind = ? AND external_id = ?`, bindingID, string(kind), externalID).
		Scan(&mapping.BindingID, &kindValue, &mapping.ExternalID, &mapping.LocalID, &updatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ExternalMapping{}, ErrNotFound
		}
		return ExternalMapping{}, fmt.Errorf("find knowledge external mapping: %w", err)
	}
	mapping.Kind = ResourceKind(kindValue)
	mapping.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return mapping, nil
}

func mappingKey(bindingID string, kind ResourceKind, externalID string) string {
	sum := sha256.Sum256([]byte(bindingID + "\x00" + string(kind) + "\x00" + externalID))
	return hex.EncodeToString(sum[:])
}
