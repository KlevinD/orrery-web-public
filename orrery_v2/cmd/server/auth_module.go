package main

import (
	"context"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/peopleaccount"
	"orrery_v2/internal/app/peopledirectory"
	"orrery_v2/internal/app/userauth"
	websitechannel "orrery_v2/internal/gateway/channel/website"
	"orrery_v2/internal/platform"
)

func AuthModule() platform.Module {
	return platform.ModuleFunc(func(ctx context.Context, registrar *platform.ModuleRegistrar) error {
		_ = ctx
		if err := identity.RegisterMigrations(registrar.Migrations()); err != nil {
			return err
		}
		if err := websitechannel.RegisterMigrations(registrar.Migrations()); err != nil {
			return err
		}
		if err := peopleaccount.RegisterMigrations(registrar.Migrations()); err != nil {
			return err
		}
		if err := peopledirectory.RegisterMigrations(registrar.Migrations()); err != nil {
			return err
		}
		return userauth.RegisterMigrations(registrar.Migrations())
	})
}
