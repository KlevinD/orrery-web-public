package skill

import (
	"context"
	"fmt"
	"strings"
)

// ManagedUpdateSource identifies the non-RSI compatibility path that is
// allowed to replace an active catalog definition in place.
type ManagedUpdateSource string

const (
	ManagedUpdateSourceResourceAdmin            ManagedUpdateSource = "resource_admin_compatibility"
	ManagedUpdateSourceDefaultCatalog           ManagedUpdateSource = "default_catalog_sync"
	ManagedUpdateSourceTalentMarket             ManagedUpdateSource = "talent_market_import"
	ManagedUpdateSourceShadowMigration          ManagedUpdateSource = "openclaw_shadow_migration"
	ManagedUpdateSourceNativeScheduledMigration ManagedUpdateSource = "native_scheduled_migration"
)

// ApplyLegacyManagedDefinition centralizes the remaining in-place update
// compatibility. VersionedRegistry intentionally does not expose this method,
// so RSI callers cannot reach it through the controlled interface.
func ApplyLegacyManagedDefinition(ctx context.Context, registry Registry, definition SkillDefinition, source ManagedUpdateSource) error {
	if registry == nil {
		return fmt.Errorf("%w: skill registry is required", ErrInvalidDefinition)
	}
	if !validManagedUpdateSource(source) {
		return fmt.Errorf("%w: unsupported in-place skill update source %q", ErrInvalidDefinition, strings.TrimSpace(string(source)))
	}
	mutable, ok := registry.(MutableRegistry)
	if !ok {
		return fmt.Errorf("%w: registry does not support compatibility updates", ErrInvalidDefinition)
	}
	return mutable.UpdateDefinition(ctx, definition)
}

func validManagedUpdateSource(source ManagedUpdateSource) bool {
	switch source {
	case ManagedUpdateSourceResourceAdmin, ManagedUpdateSourceDefaultCatalog, ManagedUpdateSourceTalentMarket, ManagedUpdateSourceShadowMigration, ManagedUpdateSourceNativeScheduledMigration:
		return true
	default:
		return false
	}
}
