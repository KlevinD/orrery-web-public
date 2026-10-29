package skill

import (
	"context"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/db"
)

// VersionSwitch compares complete definitions, including package digests and
// enablement, at the commit boundary rather than trusting version labels.
type VersionSwitch struct {
	SkillID            agentcore.SkillID
	TargetVersion      string
	ExpectedActiveHash string
	ExpectedTargetHash string
	Rollback           bool
}

// VersionCommitHook must only use the supplied transaction for persistent
// writes. A nil transaction denotes the in-memory registry: its hook runs
// before an infallible state change while the registry lock is held.
type VersionCommitHook func(context.Context, db.Tx) error

type AtomicVersionedRegistry interface {
	VersionedRegistry
	SwitchVersion(context.Context, VersionSwitch, VersionCommitHook) error
}

func validateVersionSwitch(request VersionSwitch, active, target SkillDefinition) error {
	if request.SkillID == "" || strings.TrimSpace(request.TargetVersion) == "" || request.ExpectedActiveHash == "" || request.ExpectedTargetHash == "" {
		return fmt.Errorf("%w: skill, target version, and expected content hashes are required", ErrInvalidDefinition)
	}
	if active.ID != request.SkillID || target.ID != request.SkillID || target.Version != strings.TrimSpace(request.TargetVersion) || DefinitionHash(active) != request.ExpectedActiveHash || DefinitionHash(target) != request.ExpectedTargetHash {
		return fmt.Errorf("%w: active or target skill definition changed", ErrConflict)
	}
	return nil
}

func (r *MemoryRegistry) SwitchVersion(ctx context.Context, request VersionSwitch, commit VersionCommitHook) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	active, exists := r.skills[request.SkillID]
	if !exists {
		return fmt.Errorf("%w: %s", ErrNotFound, request.SkillID)
	}
	target, exists := r.versions[request.SkillID][strings.TrimSpace(request.TargetVersion)]
	if !exists {
		return fmt.Errorf("%w: %s@%s", ErrNotFound, request.SkillID, request.TargetVersion)
	}
	if err := validateVersionSwitch(request, active, target); err != nil {
		return err
	}
	if request.Rollback && !r.activeVersions[request.SkillID][target.Version] {
		return fmt.Errorf("%w: %s@%s was never active", ErrVersionMismatch, request.SkillID, target.Version)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if commit != nil {
		if err := commit(ctx, nil); err != nil {
			return err
		}
	}
	r.skills[request.SkillID] = target.Clone()
	r.markActiveVersionLocked(request.SkillID, active.Version)
	r.markActiveVersionLocked(request.SkillID, target.Version)
	return nil
}

func switchCurrentVersion(ctx context.Context, registry AtomicVersionedRegistry, id agentcore.SkillID, version string, rollback bool) error {
	active, err := registry.Get(ctx, id)
	if err != nil {
		return err
	}
	target, err := registry.GetVersion(ctx, id, version)
	if err != nil {
		return err
	}
	return registry.SwitchVersion(ctx, VersionSwitch{SkillID: id, TargetVersion: target.Version, ExpectedActiveHash: DefinitionHash(active), ExpectedTargetHash: DefinitionHash(target), Rollback: rollback}, nil)
}
