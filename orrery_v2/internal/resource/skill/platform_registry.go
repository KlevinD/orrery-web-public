package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/db"
)

const (
	platformRegistryTable        = "resource_skill_definitions"
	platformVersionRegistryTable = "resource_skill_definition_versions"
	platformActivationTable      = "resource_skill_definition_activations"
)

type PlatformRegistry struct {
	store    *db.Store
	packages *PackageStore
}

var _ VersionedRegistry = (*PlatformRegistry)(nil)
var _ AtomicVersionedRegistry = (*PlatformRegistry)(nil)

func NewPlatformRegistry(store *db.Store) *PlatformRegistry {
	return &PlatformRegistry{store: store}
}

// SetPackageStore enables content snapshots on catalog and candidate writes.
// Configure it before serving requests; existing definitions are migrated by
// FreezeLegacyPackages rather than by a read-side mutation.
func (r *PlatformRegistry) SetPackageStore(packages *PackageStore) { r.packages = packages }

func (r *PlatformRegistry) FreezeLegacyPackages(ctx context.Context) error {
	definitions, err := r.List(ctx)
	if err != nil {
		return err
	}
	for _, definition := range definitions {
		if definition.PackageSnapshot != nil {
			continue
		}
		frozen, err := r.freeze(ctx, definition)
		if err != nil {
			return err
		}
		if err := r.replaceDefinition(ctx, frozen, DefinitionHash(definition)); err != nil {
			return err
		}
	}
	return nil
}

func (r *PlatformRegistry) freeze(ctx context.Context, definition SkillDefinition) (SkillDefinition, error) {
	if r.packages == nil {
		return definition, nil
	}
	return r.packages.FreezeDefinition(ctx, definition)
}

func (r *PlatformRegistry) Register(ctx context.Context, definition SkillDefinition) error {
	if err := validateDefinition(definition); err != nil {
		return err
	}
	var err error
	definition, err = r.freeze(ctx, definition)
	if err != nil {
		return err
	}
	return r.store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, platformRegistryTable, string(definition.ID)); err != nil {
			return err
		}
		if _, err := activeDefinitionTx(ctx, tx, definition.ID); err == nil {
			return fmt.Errorf("%w: %s", ErrAlreadyExists, definition.ID)
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		return writeActiveDefinitionTx(ctx, tx, definition)
	})
}

// UpdateDefinition is retained for non-RSI compatibility synchronization.
// Callers should use ApplyLegacyManagedDefinition with an explicit source.
func (r *PlatformRegistry) UpdateDefinition(ctx context.Context, definition SkillDefinition) error {
	if err := validateDefinition(definition); err != nil {
		return err
	}
	var err error
	definition, err = r.freeze(ctx, definition)
	if err != nil {
		return err
	}
	return r.replaceDefinition(ctx, definition, "")
}

func (r *PlatformRegistry) Disable(ctx context.Context, id agentcore.SkillID) error {
	return r.mutateActive(ctx, id, func(definition SkillDefinition) (db.Record, error) {
		definition.Enabled = false
		return definitionRecord(definition)
	})
}

func (r *PlatformRegistry) Delete(ctx context.Context, id agentcore.SkillID) error {
	return r.mutateActive(ctx, id, func(SkillDefinition) (db.Record, error) {
		return db.Record{"deleted": true}, nil
	})
}

func (r *PlatformRegistry) IsDeleted(ctx context.Context, id agentcore.SkillID) (bool, error) {
	record, ok, err := r.store.GetRecord(ctx, platformRegistryTable, string(id))
	if err != nil || !ok {
		return false, err
	}
	deleted, _ := record["deleted"].(bool)
	return deleted, nil
}

func (r *PlatformRegistry) Get(ctx context.Context, id agentcore.SkillID) (SkillDefinition, error) {
	record, ok, err := r.store.GetRecord(ctx, platformRegistryTable, string(id))
	if err != nil {
		return SkillDefinition{}, err
	}
	if !ok {
		return SkillDefinition{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if deleted, _ := record["deleted"].(bool); deleted {
		return SkillDefinition{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return decodeDefinition(record)
}

func (r *PlatformRegistry) List(ctx context.Context) ([]SkillDefinition, error) {
	definitions, err := r.all(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(definitions, func(i, j int) bool {
		return definitions[i].ID < definitions[j].ID
	})
	return definitions, nil
}

func (r *PlatformRegistry) ListByTag(ctx context.Context, tag string) ([]SkillDefinition, error) {
	definitions, err := r.all(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SkillDefinition, 0)
	for _, definition := range definitions {
		if hasString(definition.DomainTags, tag) || hasString(definition.TaskTypeTags, tag) {
			out = append(out, definition.Clone())
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (r *PlatformRegistry) RegisterVersion(ctx context.Context, definition SkillDefinition) error {
	if err := validateDefinition(definition); err != nil {
		return err
	}
	active, err := r.Get(ctx, definition.ID)
	if err != nil {
		return err
	}
	// Candidates inherit the registered baseline, never unreviewed catalog
	// edits that happen to be present in the editable package directory.
	if definition.PackageSnapshot == nil && active.PackageSnapshot != nil {
		definition.PackageSnapshot = active.Clone().PackageSnapshot
	}
	var freezeErr error
	definition, freezeErr = r.freeze(ctx, definition)
	if freezeErr != nil {
		return freezeErr
	}
	return r.store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, platformRegistryTable, string(definition.ID)); err != nil {
			return err
		}
		current, err := activeDefinitionTx(ctx, tx, definition.ID)
		if err != nil {
			return err
		}
		if DefinitionHash(current) != DefinitionHash(active) {
			return fmt.Errorf("%w: candidate baseline changed", ErrConflict)
		}
		return ensureVersionTx(ctx, tx, definition)
	})
}

func (r *PlatformRegistry) GetVersion(ctx context.Context, id agentcore.SkillID, version string) (SkillDefinition, error) {
	version = strings.TrimSpace(version)
	if version == "" {
		return SkillDefinition{}, fmt.Errorf("%w: skill version is required", ErrInvalidDefinition)
	}
	active, err := r.Get(ctx, id)
	if err != nil {
		return SkillDefinition{}, err
	}
	record, ok, err := r.store.GetRecord(ctx, platformVersionRegistryTable, skillVersionKey(id, version))
	if err != nil {
		return SkillDefinition{}, err
	}
	if ok {
		return decodeDefinition(record)
	}
	// Existing installations predate the version table. Treat the current
	// active record as its first version until it is next updated.
	if active.Version == version {
		return active, nil
	}
	return SkillDefinition{}, fmt.Errorf("%w: %s@%s", ErrNotFound, id, version)
}

func (r *PlatformRegistry) ListVersions(ctx context.Context, id agentcore.SkillID) ([]SkillDefinition, error) {
	active, err := r.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	snapshot := r.store.Snapshot()
	table := snapshot[platformVersionRegistryTable]
	prefix := skillVersionKeyPrefix(id)
	definitions := make([]SkillDefinition, 0)
	for key, record := range table {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		definition, err := decodeDefinition(record)
		if err != nil {
			return nil, err
		}
		if definition.ID != id {
			continue
		}
		definitions = append(definitions, definition)
	}
	foundActive := false
	for _, definition := range definitions {
		if definition.Version == active.Version {
			foundActive = true
			break
		}
	}
	if !foundActive {
		definitions = append(definitions, active.Clone())
	}
	sortSkillDefinitionsByVersion(definitions)
	return definitions, nil
}

func (r *PlatformRegistry) WasActiveVersion(ctx context.Context, id agentcore.SkillID, version string) (bool, error) {
	active, err := r.Get(ctx, id)
	if err != nil {
		return false, err
	}
	version = strings.TrimSpace(version)
	record, ok, err := r.store.GetRecord(ctx, platformActivationTable, skillVersionKey(id, strings.TrimSpace(version)))
	if err != nil || !ok {
		// Older installations have no activation ledger. The current active
		// definition is still a valid rollback target in that case.
		return active.Version == version, err
	}
	isActive, _ := record["active"].(bool)
	return isActive, nil
}

func (r *PlatformRegistry) PromoteVersion(ctx context.Context, id agentcore.SkillID, version string) error {
	return switchCurrentVersion(ctx, r, id, version, false)
}

func (r *PlatformRegistry) RollbackVersion(ctx context.Context, id agentcore.SkillID, version string) error {
	return switchCurrentVersion(ctx, r, id, version, true)
}

func (r *PlatformRegistry) put(ctx context.Context, definition SkillDefinition) error {
	payload, err := json.Marshal(definition.Clone())
	if err != nil {
		return fmt.Errorf("encode skill definition: %w", err)
	}
	return r.store.PutRecord(ctx, platformRegistryTable, string(definition.ID), db.Record{
		"payload": string(payload),
	})
}

func (r *PlatformRegistry) putVersion(ctx context.Context, definition SkillDefinition) error {
	payload, err := json.Marshal(definition.Clone())
	if err != nil {
		return fmt.Errorf("encode skill definition version: %w", err)
	}
	return r.store.PutRecord(ctx, platformVersionRegistryTable, skillVersionKey(definition.ID, definition.Version), db.Record{
		"payload": string(payload),
	})
}

func skillVersionKey(id agentcore.SkillID, version string) string {
	return skillVersionKeyPrefix(id) + strings.TrimSpace(version)
}

func skillVersionKeyPrefix(id agentcore.SkillID) string {
	return string(id) + "::"
}

func (r *PlatformRegistry) all(ctx context.Context) ([]SkillDefinition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	definitions := make([]SkillDefinition, 0)
	afterID := ""
	for {
		page, err := r.store.ListRecords(ctx, platformRegistryTable, afterID, db.MaxRecordPageLimit)
		if err != nil {
			return nil, err
		}
		for _, item := range page.Items {
			afterID = item.Key
			if deleted, _ := item.Record["deleted"].(bool); deleted {
				continue
			}
			definition, err := decodeDefinition(item.Record)
			if err != nil {
				return nil, err
			}
			definitions = append(definitions, definition)
		}
		if !page.HasMore {
			return definitions, nil
		}
	}
}

func decodeDefinition(record db.Record) (SkillDefinition, error) {
	raw, ok := record["payload"].(string)
	if !ok || raw == "" {
		return SkillDefinition{}, fmt.Errorf("skill definition payload missing")
	}
	var definition SkillDefinition
	if err := json.Unmarshal([]byte(raw), &definition); err != nil {
		return SkillDefinition{}, fmt.Errorf("decode skill definition: %w", err)
	}
	return definition.Clone(), nil
}
