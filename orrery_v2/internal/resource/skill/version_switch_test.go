package skill

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"orrery_v2/internal/platform/db"
)

func TestVersionSwitchRejectsChangedDefinitionsBeforeCommitHook(t *testing.T) {
	for _, kind := range []string{"memory", "platform"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			registry := atomicTestRegistry(t, kind)
			base, candidate := registerSwitchDefinitions(t, registry)
			request := switchRequest(base, candidate, false)
			changed := base.Clone()
			changed.Description = "changed without changing the version label"
			if err := registry.(MutableRegistry).UpdateDefinition(ctx, changed); err != nil {
				t.Fatal(err)
			}
			called := false
			err := registry.SwitchVersion(ctx, request, func(context.Context, db.Tx) error { called = true; return nil })
			if !errors.Is(err, ErrConflict) || called {
				t.Fatalf("stale switch = %v, commit hook called = %v", err, called)
			}
			active, _ := registry.Get(ctx, base.ID)
			if DefinitionHash(active) != DefinitionHash(changed) {
				t.Fatal("stale switch overwrote a compatibility update")
			}
		})
	}
}

func TestVersionSwitchFailedCommitKeepsCandidateRollbackIneligible(t *testing.T) {
	for _, kind := range []string{"memory", "platform"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			registry := atomicTestRegistry(t, kind)
			base, candidate := registerSwitchDefinitions(t, registry)
			failure := errors.New("audit persistence failed")
			err := registry.SwitchVersion(ctx, switchRequest(base, candidate, false), func(ctx context.Context, tx db.Tx) error {
				if tx != nil {
					if err := db.PutRecordTx(ctx, tx, "switch_test_audit", "failed", db.Record{"event": "must roll back"}); err != nil {
						return err
					}
				}
				return failure
			})
			if !errors.Is(err, failure) {
				t.Fatalf("failed switch = %v", err)
			}
			active, _ := registry.Get(ctx, base.ID)
			wasActive, err := registry.WasActiveVersion(ctx, base.ID, candidate.Version)
			if DefinitionHash(active) != DefinitionHash(base) || err != nil || wasActive {
				t.Fatalf("failed commit changed state: active=%+v, history=%v, error=%v", active, wasActive, err)
			}
			if err := registry.RollbackVersion(ctx, base.ID, candidate.Version); !errors.Is(err, ErrVersionMismatch) {
				t.Fatalf("failed candidate became a rollback target: %v", err)
			}
			if platform, ok := registry.(*PlatformRegistry); ok {
				if _, exists, err := platform.store.GetRecord(ctx, "switch_test_audit", "failed"); err != nil || exists {
					t.Fatalf("failed transaction left audit state: exists=%v, error=%v", exists, err)
				}
			}
		})
	}
}

func TestPlatformVersionSwitchPreservesFirstLegacyBaseline(t *testing.T) {
	ctx := context.Background()
	registry := atomicTestRegistry(t, "platform").(*PlatformRegistry)
	base := SkillDefinition{ID: "skill.legacy-switch", Name: "Legacy", Version: "1", Description: "legacy baseline", Enabled: true}
	if err := registry.put(ctx, base); err != nil {
		t.Fatal(err)
	}
	candidate := base.Clone()
	candidate.Version, candidate.Description = "2", "candidate"
	if err := registry.RegisterVersion(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	if err := registry.PromoteVersion(ctx, base.ID, candidate.Version); err != nil {
		t.Fatal(err)
	}
	old, err := registry.GetVersion(ctx, base.ID, base.Version)
	if err != nil || DefinitionHash(old) != DefinitionHash(base) {
		t.Fatalf("first promotion lost baseline: %+v, %v", old, err)
	}
	if err := registry.RollbackVersion(ctx, base.ID, base.Version); err != nil {
		t.Fatal(err)
	}
	active, _ := registry.Get(ctx, base.ID)
	if DefinitionHash(active) != DefinitionHash(base) {
		t.Fatal("rollback did not restore the actual legacy definition")
	}
}

func TestPlatformVersionSwitchRejectsChangedCandidateAndDeletedSkill(t *testing.T) {
	ctx := context.Background()
	registry := atomicTestRegistry(t, "platform").(*PlatformRegistry)
	base, candidate := registerSwitchDefinitions(t, registry)
	request := switchRequest(base, candidate, false)
	changed := candidate.Clone()
	changed.Description = "changed after evaluation"
	if err := registry.putVersion(ctx, changed); err != nil {
		t.Fatal(err)
	}
	if err := registry.SwitchVersion(ctx, request, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed candidate switch = %v", err)
	}
	if err := registry.Delete(ctx, base.ID); err != nil {
		t.Fatal(err)
	}
	if err := registry.SwitchVersion(ctx, request, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted skill switch = %v", err)
	}
	deleted, _ := registry.IsDeleted(ctx, base.ID)
	if !deleted {
		t.Fatal("stale switch resurrected a deleted skill")
	}
}

func TestPlatformVersionSwitchCommitFailureAndCancellationAreAtomic(t *testing.T) {
	for _, kind := range []string{"persist", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			registry := atomicTestRegistry(t, "platform").(*PlatformRegistry)
			base, candidate := registerSwitchDefinitions(t, registry)
			attemptCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			if kind == "persist" {
				registry.store.InjectReplaceError(errors.New("commit unavailable"))
			}
			err := registry.SwitchVersion(attemptCtx, switchRequest(base, candidate, false), func(ctx context.Context, tx db.Tx) error {
				if err := db.PutRecordTx(ctx, tx, "switch_test_audit", "commit", db.Record{"committed": true}); err != nil {
					return err
				}
				if kind == "cancel" {
					cancel()
				}
				return nil
			})
			registry.store.InjectReplaceError(nil)
			if err == nil || (kind == "cancel" && !errors.Is(err, context.Canceled)) || (kind == "persist" && !errors.Is(err, db.ErrPersist)) {
				t.Fatalf("failed commit = %v", err)
			}
			active, _ := registry.Get(ctx, base.ID)
			history, _ := registry.WasActiveVersion(ctx, base.ID, candidate.Version)
			_, auditExists, auditErr := registry.store.GetRecord(ctx, "switch_test_audit", "commit")
			if DefinitionHash(active) != DefinitionHash(base) || history || auditExists || auditErr != nil {
				t.Fatalf("failed commit left state: active=%+v, history=%v, audit=%v, error=%v", active, history, auditExists, auditErr)
			}
		})
	}
}

func TestPlatformVersionSwitchChecksBaselineAcrossDatabaseConnections(t *testing.T) {
	ctx := context.Background()
	config := db.Config{Driver: db.DriverFile, URL: "file://" + filepath.Join(t.TempDir(), "registry.db")}
	first, err := db.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := db.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	one, two := NewPlatformRegistry(first), NewPlatformRegistry(second)
	base, candidate := registerSwitchDefinitions(t, one)
	other := candidate.Clone()
	other.Version = "3"
	if err := two.RegisterVersion(ctx, other); err != nil {
		t.Fatal(err)
	}
	staleRequest := switchRequest(base, other, false)
	if err := one.SwitchVersion(ctx, switchRequest(base, candidate, false), nil); err != nil {
		t.Fatal(err)
	}
	if err := two.SwitchVersion(ctx, staleRequest, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("second connection overwrote the winner: %v", err)
	}
	active, _ := two.Get(ctx, base.ID)
	if active.Version != candidate.Version {
		t.Fatalf("active = %s, want %s", active.Version, candidate.Version)
	}
}

func TestVersionRollbackFromDisabledActivePreservesArchivedContent(t *testing.T) {
	for _, kind := range []string{"memory", "platform"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			registry := atomicTestRegistry(t, kind)
			base, candidate := registerSwitchDefinitions(t, registry)
			if err := registry.PromoteVersion(ctx, base.ID, candidate.Version); err != nil {
				t.Fatal(err)
			}
			if err := registry.Disable(ctx, base.ID); err != nil {
				t.Fatal(err)
			}
			if err := registry.RollbackVersion(ctx, base.ID, base.Version); err != nil {
				t.Fatal(err)
			}
			archived, err := registry.GetVersion(ctx, base.ID, candidate.Version)
			if err != nil || DefinitionHash(archived) != DefinitionHash(candidate) {
				t.Fatalf("disable/rollback rewrote archived content: %+v, %v", archived, err)
			}
		})
	}
}

func atomicTestRegistry(t *testing.T, kind string) AtomicVersionedRegistry {
	t.Helper()
	if kind == "memory" {
		return NewMemoryRegistry()
	}
	store, err := db.Open(context.Background(), db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return NewPlatformRegistry(store)
}

func registerSwitchDefinitions(t *testing.T, registry VersionedRegistry) (SkillDefinition, SkillDefinition) {
	t.Helper()
	base := SkillDefinition{ID: "skill.atomic", Name: "Atomic", Version: "1", Description: "baseline", Enabled: true}
	candidate := base.Clone()
	candidate.Version, candidate.Description = "2", "candidate"
	if err := registry.Register(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterVersion(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	return base, candidate
}

func switchRequest(active, target SkillDefinition, rollback bool) VersionSwitch {
	return VersionSwitch{SkillID: active.ID, TargetVersion: target.Version, ExpectedActiveHash: DefinitionHash(active), ExpectedTargetHash: DefinitionHash(target), Rollback: rollback}
}
