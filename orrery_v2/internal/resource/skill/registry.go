package skill

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	agentcore "orrery_v2/internal/agent/core"
)

type Registry interface {
	Register(ctx context.Context, definition SkillDefinition) error
	Disable(ctx context.Context, id agentcore.SkillID) error
	Delete(ctx context.Context, id agentcore.SkillID) error
	IsDeleted(ctx context.Context, id agentcore.SkillID) (bool, error)
	Get(ctx context.Context, id agentcore.SkillID) (SkillDefinition, error)
	List(ctx context.Context) ([]SkillDefinition, error)
	ListByTag(ctx context.Context, tag string) ([]SkillDefinition, error)
}

// MutableRegistry is the compatibility-only write surface for managed
// catalog synchronization. RSI code must use VersionedRegistry instead.
type MutableRegistry interface {
	UpdateDefinition(ctx context.Context, definition SkillDefinition) error
}

// VersionedRegistry is the controlled update surface used by improvement
// workflows. RegisterVersion stores an immutable candidate without changing
// the active definition. Evaluated application writes additionally require
// AtomicVersionedRegistry so their content checks and audit commit are atomic.
type VersionedRegistry interface {
	Registry
	RegisterVersion(ctx context.Context, definition SkillDefinition) error
	GetVersion(ctx context.Context, id agentcore.SkillID, version string) (SkillDefinition, error)
	ListVersions(ctx context.Context, id agentcore.SkillID) ([]SkillDefinition, error)
	WasActiveVersion(ctx context.Context, id agentcore.SkillID, version string) (bool, error)
	PromoteVersion(ctx context.Context, id agentcore.SkillID, version string) error
	RollbackVersion(ctx context.Context, id agentcore.SkillID, version string) error
}

var _ VersionedRegistry = (*MemoryRegistry)(nil)
var _ AtomicVersionedRegistry = (*MemoryRegistry)(nil)

type MemoryRegistry struct {
	mu             sync.RWMutex
	skills         map[agentcore.SkillID]SkillDefinition
	versions       map[agentcore.SkillID]map[string]SkillDefinition
	activeVersions map[agentcore.SkillID]map[string]bool
	deleted        map[agentcore.SkillID]bool
}

func NewMemoryRegistry() *MemoryRegistry {
	return &MemoryRegistry{
		skills:         map[agentcore.SkillID]SkillDefinition{},
		versions:       map[agentcore.SkillID]map[string]SkillDefinition{},
		activeVersions: map[agentcore.SkillID]map[string]bool{},
		deleted:        map[agentcore.SkillID]bool{},
	}
}

func (r *MemoryRegistry) Register(ctx context.Context, definition SkillDefinition) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateDefinition(definition); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.skills[definition.ID]; exists {
		return fmt.Errorf("%w: %s", ErrAlreadyExists, definition.ID)
	}
	delete(r.deleted, definition.ID)
	r.skills[definition.ID] = definition.Clone()
	r.saveVersionLocked(definition)
	r.markActiveVersionLocked(definition.ID, definition.Version)
	return nil
}

// UpdateDefinition is retained for non-RSI compatibility synchronization.
// Callers should use ApplyLegacyManagedDefinition with an explicit source.
func (r *MemoryRegistry) UpdateDefinition(ctx context.Context, definition SkillDefinition) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateDefinition(definition); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.skills[definition.ID]; !exists {
		return fmt.Errorf("%w: %s", ErrNotFound, definition.ID)
	}
	r.skills[definition.ID] = definition.Clone()
	r.saveVersionLocked(definition)
	r.markActiveVersionLocked(definition.ID, definition.Version)
	return nil
}

func (r *MemoryRegistry) RegisterVersion(ctx context.Context, definition SkillDefinition) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateDefinition(definition); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.skills[definition.ID]; !exists {
		return fmt.Errorf("%w: %s", ErrNotFound, definition.ID)
	}
	if versions := r.versions[definition.ID]; versions != nil {
		if existing, exists := versions[definition.Version]; exists {
			if sameSkillDefinition(existing, definition) {
				return nil
			}
			return fmt.Errorf("%w: %s@%s", ErrAlreadyExists, definition.ID, definition.Version)
		}
	}
	r.saveVersionLocked(definition)
	return nil
}

func (r *MemoryRegistry) GetVersion(ctx context.Context, id agentcore.SkillID, version string) (SkillDefinition, error) {
	if err := ctx.Err(); err != nil {
		return SkillDefinition{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, exists := r.skills[id]; !exists {
		return SkillDefinition{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	definition, exists := r.versions[id][strings.TrimSpace(version)]
	if !exists {
		return SkillDefinition{}, fmt.Errorf("%w: %s@%s", ErrNotFound, id, version)
	}
	return definition.Clone(), nil
}

func (r *MemoryRegistry) ListVersions(ctx context.Context, id agentcore.SkillID) ([]SkillDefinition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, exists := r.skills[id]; !exists {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	definitions := make([]SkillDefinition, 0, len(r.versions[id]))
	for _, definition := range r.versions[id] {
		definitions = append(definitions, definition.Clone())
	}
	sortSkillDefinitionsByVersion(definitions)
	return definitions, nil
}

func (r *MemoryRegistry) WasActiveVersion(ctx context.Context, id agentcore.SkillID, version string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, exists := r.skills[id]; !exists {
		return false, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return r.activeVersions[id][strings.TrimSpace(version)], nil
}

func (r *MemoryRegistry) PromoteVersion(ctx context.Context, id agentcore.SkillID, version string) error {
	return switchCurrentVersion(ctx, r, id, version, false)
}

func (r *MemoryRegistry) RollbackVersion(ctx context.Context, id agentcore.SkillID, version string) error {
	return switchCurrentVersion(ctx, r, id, version, true)
}

func (r *MemoryRegistry) Disable(ctx context.Context, id agentcore.SkillID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	definition, exists := r.skills[id]
	if !exists {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	definition.Enabled = false
	r.skills[id] = definition
	return nil
}

func (r *MemoryRegistry) Delete(ctx context.Context, id agentcore.SkillID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.skills[id]; !exists {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	delete(r.skills, id)
	r.deleted[id] = true
	return nil
}

func (r *MemoryRegistry) IsDeleted(ctx context.Context, id agentcore.SkillID) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.deleted[id], nil
}

func (r *MemoryRegistry) Get(ctx context.Context, id agentcore.SkillID) (SkillDefinition, error) {
	if err := ctx.Err(); err != nil {
		return SkillDefinition{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	definition, exists := r.skills[id]
	if !exists {
		return SkillDefinition{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return definition.Clone(), nil
}

func (r *MemoryRegistry) List(ctx context.Context) ([]SkillDefinition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]SkillDefinition, 0, len(r.skills))
	for _, definition := range r.skills {
		out = append(out, definition.Clone())
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (r *MemoryRegistry) ListByTag(ctx context.Context, tag string) ([]SkillDefinition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []SkillDefinition
	for _, definition := range r.skills {
		if hasString(definition.DomainTags, tag) || hasString(definition.TaskTypeTags, tag) {
			out = append(out, definition.Clone())
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func validateDefinition(definition SkillDefinition) error {
	if definition.ID == "" {
		return fmt.Errorf("%w: skill_id is required", ErrInvalidDefinition)
	}
	if strings.TrimSpace(definition.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidDefinition)
	}
	if strings.TrimSpace(definition.Version) == "" {
		return fmt.Errorf("%w: version is required", ErrInvalidDefinition)
	}
	return nil
}

func hasString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (r *MemoryRegistry) saveVersionLocked(definition SkillDefinition) {
	if r.versions[definition.ID] == nil {
		r.versions[definition.ID] = map[string]SkillDefinition{}
	}
	r.versions[definition.ID][definition.Version] = definition.Clone()
}

func (r *MemoryRegistry) markActiveVersionLocked(id agentcore.SkillID, version string) {
	if r.activeVersions[id] == nil {
		r.activeVersions[id] = map[string]bool{}
	}
	r.activeVersions[id][strings.TrimSpace(version)] = true
}

func sameSkillDefinition(left, right SkillDefinition) bool {
	return reflect.DeepEqual(left.Clone(), right.Clone())
}

func sortSkillDefinitionsByVersion(definitions []SkillDefinition) {
	sort.Slice(definitions, func(i, j int) bool {
		comparison := compareVersion(definitions[i].Version, definitions[j].Version)
		if comparison == 0 {
			return definitions[i].Version < definitions[j].Version
		}
		return comparison < 0
	})
}
