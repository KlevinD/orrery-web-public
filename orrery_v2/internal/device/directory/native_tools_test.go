package directory

import (
	"context"
	"errors"
	deviceidentity "orrery_v2/internal/device/identity"
	"orrery_v2/internal/platform/db"
	"strings"
	"testing"
	"time"
)

func TestNativeToolExecutorIsDiscoverableButCannotHostAnAgent(t *testing.T) {
	now := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	database, err := db.Open(context.Background(), db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := deviceidentity.NewSQLStore(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	store, err := NewSQLStore(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	insertDirectoryTestDevice(t, context.Background(), database, now)
	service, err := NewService(Options{Store: store, Now: func() time.Time { return now }, DeviceConnected: func(string, string) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	scope := OwnerScope{TenantID: "global", OwnerID: "user-1"}
	err = service.PublishCatalog(context.Background(), scope, "device_test_001", PublishCatalogInput{
		CatalogDigest: strings.Repeat("a", 64), ObservedAt: now,
		Profiles: []AgentProfileDescriptor{{ProfileID: "profile_orrery_workspace_tools_v1", DisplayName: "Orrery local tools", RuntimeKind: RuntimeKindOrreryTools, Available: true,
			Capabilities: []string{"workspace.read", "workspace.write"}, WorkspaceGrants: []WorkspaceGrantDescriptor{{GrantID: "grant_native_001", DisplayName: "Orders", Capabilities: []string{"workspace.read"}, Available: true}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := service.GetPersonalAssistant(context.Background(), scope)
	if err != nil || len(view.OwnedAgents) != 1 || view.OwnedAgents[0].Status != AgentStatusOnline {
		t.Fatalf("native tool discovery: %+v %v", view, err)
	}
	_, err = service.UpdatePersonalAssistant(context.Background(), scope, UpdatePersonalAssistantInput{HostingMode: HostingModeOwnedAgent, OwnedAgentID: view.OwnedAgents[0].OwnedAgentID, WorkspaceGrantID: "grant_native_001"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("tool executor used as Agent host: %v", err)
	}
}
