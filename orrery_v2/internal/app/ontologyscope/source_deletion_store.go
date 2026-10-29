package ontologyscope

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

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/platform/db"
)

const sourceDeletionRecoveryDelay = 5 * time.Minute

type SourceDeletionBinding struct {
	Binding         ScopeBinding
	ExpectedVersion int64
}

type SourceDeletionBindingSnapshot struct {
	Original        ScopeBinding `json:"original"`
	DisabledVersion int64        `json:"disabled_version"`
}

type SourceDeletionIntent struct {
	IntentID           string
	SourceObject       identity.ObjectRef
	CanonicalTenantIDs []string
	Bindings           []SourceDeletionBindingSnapshot
	ChangedBy          string
	RecoverAfter       time.Time
	CreatedAt          time.Time
}

func (s *PlatformBindingStore) PrepareSourceDeletion(
	ctx context.Context,
	source identity.ObjectRef,
	canonicalTenantIDs []string,
	bindings []SourceDeletionBinding,
	changedBy string,
) (SourceDeletionIntent, error) {
	if s == nil || s.store == nil {
		return SourceDeletionIntent{}, ErrDependencyUnavailable
	}
	source.Type = strings.ToLower(strings.TrimSpace(source.Type))
	source.ID = strings.TrimSpace(source.ID)
	changedBy = strings.TrimSpace(changedBy)
	if source.Type == "" || source.ID == "" || changedBy == "" || len(canonicalTenantIDs) == 0 || len(bindings) == 0 {
		return SourceDeletionIntent{}, ErrInvalidBinding
	}
	canonical := make([]string, len(canonicalTenantIDs))
	for index, raw := range canonicalTenantIDs {
		canonical[index] = strings.TrimSpace(raw)
		if canonical[index] == "" {
			return SourceDeletionIntent{}, ErrInvalidBinding
		}
	}
	prepared := make([]SourceDeletionBinding, len(bindings))
	for index, candidate := range bindings {
		candidate.Binding = NormalizeBinding(candidate.Binding)
		if err := ValidateBinding(candidate.Binding); err != nil {
			return SourceDeletionIntent{}, err
		}
		if candidate.ExpectedVersion < 0 || candidate.Binding.SourceObject.Type != source.Type || candidate.Binding.SourceObject.ID != source.ID {
			return SourceDeletionIntent{}, ErrBindingConflict
		}
		prepared[index] = candidate
	}
	now := time.Now().UTC()
	if s.now != nil {
		now = s.now().UTC()
	}
	intent := SourceDeletionIntent{
		IntentID: sourceDeletionIntentID(source), SourceObject: source, CanonicalTenantIDs: canonical,
		ChangedBy: changedBy, RecoverAfter: now.Add(sourceDeletionRecoveryDelay), CreatedAt: now,
	}
	err := s.store.WithTx(ctx, func(tx db.Tx) error {
		intent.Bindings = make([]SourceDeletionBindingSnapshot, 0, len(prepared))
		for _, candidate := range prepared {
			disabled := candidate.Binding
			disabled.State = BindingStateDisabled
			saved, err := saveBindingTx(ctx, tx, disabled, candidate.ExpectedVersion, changedBy, now)
			if err != nil {
				return err
			}
			intent.Bindings = append(intent.Bindings, SourceDeletionBindingSnapshot{
				Original: candidate.Binding, DisabledVersion: saved.BindingVersion,
			})
		}
		canonicalJSON, err := json.Marshal(intent.CanonicalTenantIDs)
		if err != nil {
			return err
		}
		bindingsJSON, err := json.Marshal(intent.Bindings)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO ontology_scope_binding_source_deletions (
	intent_id, source_object_type, source_object_id, canonical_tenant_ids_json,
	bindings_json, changed_by, recover_after, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, intent.IntentID, source.Type, source.ID, string(canonicalJSON), string(bindingsJSON), changedBy, intent.RecoverAfter.UnixMilli(), now.UnixMilli()); err != nil {
			return fmt.Errorf("persist scope binding source deletion: %w", err)
		}
		return nil
	})
	if err != nil {
		return SourceDeletionIntent{}, err
	}
	return intent, nil
}

func (s *PlatformBindingStore) RestoreSourceDeletion(ctx context.Context, intent SourceDeletionIntent, changedBy string) error {
	if s == nil || s.store == nil {
		return ErrDependencyUnavailable
	}
	changedBy = strings.TrimSpace(changedBy)
	if strings.TrimSpace(intent.IntentID) == "" || changedBy == "" {
		return ErrInvalidBinding
	}
	now := time.Now().UTC()
	if s.now != nil {
		now = s.now().UTC()
	}
	return s.store.WithTx(ctx, func(tx db.Tx) error {
		persisted, err := loadSourceDeletionIntentTx(ctx, tx, intent.IntentID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		} else if err != nil {
			return err
		}
		for index := len(persisted.Bindings) - 1; index >= 0; index-- {
			snapshot := persisted.Bindings[index]
			original := snapshot.Original
			original.State = BindingStateActive
			if _, err := saveBindingTx(ctx, tx, original, snapshot.DisabledVersion, changedBy, now); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM ontology_scope_binding_source_deletions WHERE intent_id = ?`, intent.IntentID)
		return err
	})
}

func (s *PlatformBindingStore) CompleteSourceDeletion(ctx context.Context, intentID string) error {
	if s == nil || s.store == nil {
		return ErrDependencyUnavailable
	}
	intentID = strings.TrimSpace(intentID)
	if intentID == "" {
		return ErrInvalidBinding
	}
	_, err := s.store.Exec(ctx, `DELETE FROM ontology_scope_binding_source_deletions WHERE intent_id = ?`, intentID)
	return err
}

func (s *PlatformBindingStore) ListRecoverableSourceDeletions(ctx context.Context, before time.Time, limit int) ([]SourceDeletionIntent, error) {
	if s == nil || s.store == nil {
		return nil, ErrDependencyUnavailable
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.store.Query(ctx, sourceDeletionIntentSelect+` WHERE recover_after <= ? ORDER BY recover_after, intent_id LIMIT ?`, before.UTC().UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	intents := make([]SourceDeletionIntent, 0, limit)
	for rows.Next() {
		intent, err := scanSourceDeletionIntent(rows)
		if err != nil {
			return nil, err
		}
		intents = append(intents, intent)
	}
	return intents, rows.Err()
}

func sourceDeletionIntentID(source identity.ObjectRef) string {
	digest := sha256.Sum256([]byte(source.Type + "\x00" + source.ID))
	return "scope-delete-" + hex.EncodeToString(digest[:])
}

const sourceDeletionIntentSelect = `SELECT intent_id, source_object_type, source_object_id,
	canonical_tenant_ids_json, bindings_json, changed_by, recover_after, created_at
	FROM ontology_scope_binding_source_deletions`

func loadSourceDeletionIntentTx(ctx context.Context, tx db.Tx, intentID string) (SourceDeletionIntent, error) {
	return scanSourceDeletionIntent(tx.QueryRowContext(ctx, sourceDeletionIntentSelect+` WHERE intent_id = ?`, intentID))
}

func scanSourceDeletionIntent(row rowScanner) (SourceDeletionIntent, error) {
	var intent SourceDeletionIntent
	var canonicalJSON, bindingsJSON string
	var recoverAfter, createdAt int64
	if err := row.Scan(
		&intent.IntentID, &intent.SourceObject.Type, &intent.SourceObject.ID,
		&canonicalJSON, &bindingsJSON, &intent.ChangedBy, &recoverAfter, &createdAt,
	); err != nil {
		return SourceDeletionIntent{}, err
	}
	if err := json.Unmarshal([]byte(canonicalJSON), &intent.CanonicalTenantIDs); err != nil {
		return SourceDeletionIntent{}, fmt.Errorf("decode source deletion canonical tenants: %w", err)
	}
	if err := json.Unmarshal([]byte(bindingsJSON), &intent.Bindings); err != nil {
		return SourceDeletionIntent{}, fmt.Errorf("decode source deletion bindings: %w", err)
	}
	intent.RecoverAfter = time.UnixMilli(recoverAfter).UTC()
	intent.CreatedAt = time.UnixMilli(createdAt).UTC()
	return intent, nil
}
