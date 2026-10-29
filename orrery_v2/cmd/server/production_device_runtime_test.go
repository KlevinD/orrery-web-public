package main

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	deviceprovider "orrery_v2/internal/agent/runtime/providers/device"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/testkit"
)

func TestProductionRuntimeRegistryUsesDurableDeviceConnectionWithoutLegacyFallback(t *testing.T) {
	repository, err := deviceprovider.NewSQLRepository(context.Background(), testkit.NewTestDB(t))
	if err != nil {
		t.Fatal(err)
	}
	broker, err := deviceprovider.NewDurableBroker(deviceprovider.BrokerOptions{Repository: repository})
	if err != nil {
		t.Fatal(err)
	}
	registry := newProductionRuntimeRegistry(productionDiscussionAgentDependencies{DeviceBroker: broker})
	t.Cleanup(func() { _ = registry.Close() })
	connection, err := registry.Resolve(agentcore.RuntimeKindDevice)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := connection.(*deviceprovider.Connection); !ok {
		t.Fatalf("Device production connection = %T", connection)
	}
	if _, ok := registry.Capabilities(context.Background(), agentcore.RuntimeKindDevice); !ok {
		t.Fatal("Device production capabilities unavailable")
	}
}

func TestProductionModulesRegisterDeviceRuntimeMigration(t *testing.T) {
	ctx := context.Background()
	cfg := loadTestConfig(t, testConfigOverridesFromProcess())
	rt, err := platform.OpenWithModules(ctx, cfg, productionModules()...)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	if err := rt.Migrations.UpRole(ctx, db.RoleRuntime); err != nil {
		t.Fatal(err)
	}
	store, err := rt.Databases.Store(db.RoleRuntime)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"runtime_device_work_orders", "runtime_device_work_order_events", "runtime_device_inbound_messages", "runtime_device_outbox", "runtime_device_leases"} {
		exists, err := store.TableExists(ctx, table)
		if err != nil || !exists {
			t.Fatalf("production Device table %s exists = %v, error = %v", table, exists, err)
		}
	}
}
