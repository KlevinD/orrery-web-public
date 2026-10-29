package main

import (
	"context"

	deviceprovider "orrery_v2/internal/agent/runtime/providers/device"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
)

func newProductionDeviceBroker(ctx context.Context, rt *platform.Runtime) (*deviceprovider.DurableBroker, error) {
	runtimeDatabase, err := rt.Databases.Store(db.RoleRuntime)
	if err != nil {
		return nil, err
	}
	repository, err := deviceprovider.NewSQLRepository(ctx, runtimeDatabase)
	if err != nil {
		return nil, err
	}
	return deviceprovider.NewDurableBroker(deviceprovider.BrokerOptions{Repository: repository, Now: rt.Clock.Now})
}
