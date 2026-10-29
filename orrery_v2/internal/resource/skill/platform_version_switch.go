package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/db"
)

func (r *PlatformRegistry) SwitchVersion(ctx context.Context, request VersionSwitch, commit VersionCommitHook) error {
	return r.store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, platformRegistryTable, string(request.SkillID)); err != nil {
			return err
		}
		expectedRecord, active, err := activeDefinitionRecordTx(ctx, tx, request.SkillID)
		if err != nil {
			return err
		}
		target, err := versionDefinitionTx(ctx, tx, active, request.TargetVersion)
		if err != nil {
			return err
		}
		if err := validateVersionSwitch(request, active, target); err != nil {
			return err
		}
		if request.Rollback {
			record, exists, err := db.GetRecordTx(ctx, tx, platformActivationTable, skillVersionKey(request.SkillID, target.Version))
			if err != nil {
				return err
			}
			wasActive, _ := record["active"].(bool)
			if !wasActive && (exists || active.Version != target.Version) {
				return fmt.Errorf("%w: %s@%s was never active", ErrVersionMismatch, request.SkillID, target.Version)
			}
		}
		// Preserve the first legacy baseline before changing its active record.
		// Existing history is never overwritten by a version switch.
		if err := ensureBaselineVersionTx(ctx, tx, active); err != nil {
			return err
		}
		if err := ensureVersionTx(ctx, tx, target); err != nil {
			return err
		}
		if err := markActiveTx(ctx, tx, active.ID, active.Version); err != nil {
			return err
		}
		record, err := definitionRecord(target)
		if err != nil {
			return err
		}
		if err := compareActiveRecordTx(ctx, tx, target.ID, expectedRecord, record); err != nil {
			return err
		}
		if err := markActiveTx(ctx, tx, target.ID, target.Version); err != nil {
			return err
		}
		if commit != nil {
			return commit(ctx, tx)
		}
		return nil
	})
}

func (r *PlatformRegistry) replaceDefinition(ctx context.Context, definition SkillDefinition, expectedHash string) error {
	return r.store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, platformRegistryTable, string(definition.ID)); err != nil {
			return err
		}
		active, err := activeDefinitionTx(ctx, tx, definition.ID)
		if err != nil {
			return err
		}
		if expectedHash != "" && DefinitionHash(active) != expectedHash {
			return fmt.Errorf("%w: migration baseline changed", ErrConflict)
		}
		if active.Version != definition.Version {
			if err := ensureBaselineVersionTx(ctx, tx, active); err != nil {
				return err
			}
			if err := markActiveTx(ctx, tx, active.ID, active.Version); err != nil {
				return err
			}
		}
		return writeActiveDefinitionTx(ctx, tx, definition)
	})
}

func (r *PlatformRegistry) mutateActive(ctx context.Context, id agentcore.SkillID, mutate func(SkillDefinition) (db.Record, error)) error {
	return r.store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, platformRegistryTable, string(id)); err != nil {
			return err
		}
		active, err := activeDefinitionTx(ctx, tx, id)
		if err != nil {
			return err
		}
		record, err := mutate(active)
		if err != nil {
			return err
		}
		return db.PutRecordTx(ctx, tx, platformRegistryTable, string(id), record)
	})
}

func activeDefinitionTx(ctx context.Context, tx db.Tx, id agentcore.SkillID) (SkillDefinition, error) {
	_, definition, err := activeDefinitionRecordTx(ctx, tx, id)
	return definition, err
}

func activeDefinitionRecordTx(ctx context.Context, tx db.Tx, id agentcore.SkillID) (db.Record, SkillDefinition, error) {
	record, exists, err := db.GetRecordTx(ctx, tx, platformRegistryTable, string(id))
	if err != nil {
		return nil, SkillDefinition{}, err
	}
	if deleted, _ := record["deleted"].(bool); !exists || deleted {
		return nil, SkillDefinition{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	definition, err := decodeDefinition(record)
	return record, definition, err
}

func compareActiveRecordTx(ctx context.Context, tx db.Tx, id agentcore.SkillID, expected, replacement db.Record) error {
	expectedJSON, err := json.Marshal(db.SanitizeRecordText(expected))
	if err != nil {
		return err
	}
	replacementJSON, err := json.Marshal(db.SanitizeRecordText(replacement))
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE platform_records SET payload_json = ? WHERE table_name = ? AND record_key = ? AND payload_json = ?`, string(replacementJSON), platformRegistryTable, string(id), string(expectedJSON))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("%w: active skill record changed", ErrConflict)
	}
	return nil
}

func versionDefinitionTx(ctx context.Context, tx db.Tx, active SkillDefinition, version string) (SkillDefinition, error) {
	version = strings.TrimSpace(version)
	record, exists, err := db.GetRecordTx(ctx, tx, platformVersionRegistryTable, skillVersionKey(active.ID, version))
	if err != nil {
		return SkillDefinition{}, err
	}
	if exists {
		return decodeDefinition(record)
	}
	if active.Version == version {
		return active.Clone(), nil
	}
	return SkillDefinition{}, fmt.Errorf("%w: %s@%s", ErrNotFound, active.ID, version)
}

func definitionRecord(definition SkillDefinition) (db.Record, error) {
	payload, err := json.Marshal(definition.Clone())
	if err != nil {
		return nil, fmt.Errorf("encode skill definition: %w", err)
	}
	return db.Record{"payload": string(payload)}, nil
}

func ensureVersionTx(ctx context.Context, tx db.Tx, definition SkillDefinition) error {
	return ensureVersionRecordTx(ctx, tx, definition, false)
}

func ensureBaselineVersionTx(ctx context.Context, tx db.Tx, definition SkillDefinition) error {
	return ensureVersionRecordTx(ctx, tx, definition, true)
}

func ensureVersionRecordTx(ctx context.Context, tx db.Tx, definition SkillDefinition, baseline bool) error {
	record, err := definitionRecord(definition)
	if err != nil {
		return err
	}
	key := skillVersionKey(definition.ID, definition.Version)
	created, err := db.InsertRecordTx(ctx, tx, platformVersionRegistryTable, key, record)
	if err != nil || created {
		return err
	}
	existing, _, err := db.GetRecordTx(ctx, tx, platformVersionRegistryTable, key)
	if err != nil {
		return err
	}
	stored, err := decodeDefinition(existing)
	if err != nil {
		return err
	}
	// Disable changes the active control flag without changing archived
	// executable content. Preserve the version's original enablement value.
	if baseline {
		definition.Enabled = stored.Enabled
	}
	if !sameSkillDefinition(stored, definition) {
		return fmt.Errorf("%w: %s@%s", ErrAlreadyExists, definition.ID, definition.Version)
	}
	return nil
}

func writeActiveDefinitionTx(ctx context.Context, tx db.Tx, definition SkillDefinition) error {
	record, err := definitionRecord(definition)
	if err != nil {
		return err
	}
	if err := db.PutRecordTx(ctx, tx, platformRegistryTable, string(definition.ID), record); err != nil {
		return err
	}
	// This overwrite is intentionally confined to compatibility catalog writes.
	if err := db.PutRecordTx(ctx, tx, platformVersionRegistryTable, skillVersionKey(definition.ID, definition.Version), record); err != nil {
		return err
	}
	return markActiveTx(ctx, tx, definition.ID, definition.Version)
}

func markActiveTx(ctx context.Context, tx db.Tx, id agentcore.SkillID, version string) error {
	return db.PutRecordTx(ctx, tx, platformActivationTable, skillVersionKey(id, version), db.Record{"active": true})
}
