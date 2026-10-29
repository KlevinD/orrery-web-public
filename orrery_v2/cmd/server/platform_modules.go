package main

import (
	"context"

	deviceprovider "orrery_v2/internal/agent/runtime/providers/device"
	"orrery_v2/internal/app/agenttrace"
	devicedirectory "orrery_v2/internal/device/directory"
	deviceidentity "orrery_v2/internal/device/identity"
	"orrery_v2/internal/gateway/binding"
	"orrery_v2/internal/gateway/channel/feishu"
	gwconfig "orrery_v2/internal/gateway/config"
	gwinbound "orrery_v2/internal/gateway/inbound"
	gwoutbound "orrery_v2/internal/gateway/outbound"
	"orrery_v2/internal/platform"
	platformrealtime "orrery_v2/internal/platform/realtime"
)

func registerAgentTracePlatform(_ context.Context, registrar *platform.ModuleRegistrar) error {
	return agenttrace.RegisterMigrations(registrar.Migrations())
}

func registerRealtimePlatform(_ context.Context, registrar *platform.ModuleRegistrar) error {
	return platformrealtime.RegisterMigrations(registrar)
}

func registerDeviceRuntimePlatform(_ context.Context, registrar *platform.ModuleRegistrar) error {
	if err := deviceprovider.RegisterMigrations(registrar.Migrations()); err != nil {
		return err
	}
	if err := deviceidentity.RegisterMigrations(registrar.Migrations()); err != nil {
		return err
	}
	return devicedirectory.RegisterMigrations(registrar.Migrations())
}

func registerGatewayConfigPlatform(_ context.Context, registrar *platform.ModuleRegistrar) error {
	if err := feishu.RegisterMeetingMigrations(registrar.Migrations()); err != nil {
		return err
	}
	if err := gwconfig.RegisterMigrations(registrar.Migrations()); err != nil {
		return err
	}
	if err := binding.RegisterMigrations(registrar.Migrations()); err != nil {
		return err
	}
	if err := gwoutbound.RegisterMigrations(registrar.Migrations()); err != nil {
		return err
	}
	return gwinbound.RegisterMigrations(registrar.Migrations())
}
