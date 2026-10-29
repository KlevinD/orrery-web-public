package main

import (
	"context"
	"fmt"
	"path/filepath"

	"orrery_v2/internal/platform/db"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

func newProductionResourceRegistries(ctx context.Context, core *db.Store, baseDir string) (*toolres.PlatformRegistry, *skillres.PlatformRegistry, *skillres.PlatformBundleRegistry, *skillres.PackageStore, error) {
	packages := skillres.NewPackageStore(filepath.Join(baseDir, "data", "skills"))
	registry := skillres.NewPlatformRegistry(core)
	registry.SetPackageStore(packages)
	if err := registry.FreezeLegacyPackages(ctx); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("freeze registered skill packages: %w", err)
	}
	return toolres.NewPlatformRegistry(core), registry, skillres.NewPlatformBundleRegistry(core), packages, nil
}
