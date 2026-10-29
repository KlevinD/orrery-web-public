package directory

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	deviceidentity "orrery_v2/internal/device/identity"
	"orrery_v2/internal/platform/db"
)

func TestServicePersistsOwnedAgentBindingAcrossCatalogChanges(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := deviceidentity.NewSQLStore(ctx, database); err != nil {
		t.Fatal(err)
	}
	store, err := NewSQLStore(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 6, 4, 0, 0, 0, time.UTC)
	insertDirectoryTestDevice(t, ctx, database, now)
	service, err := NewService(Options{
		Store: store, Now: func() time.Time { return now }, DefaultTenantID: "global",
		DeviceConnected: func(tenantID, deviceID string) bool {
			return tenantID == "global" && deviceID == "device_test_001"
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	scope := OwnerScope{OwnerID: "user-1"}
	input := PublishCatalogInput{
		CatalogDigest: strings.Repeat("a", 64), ObservedAt: now,
		Profiles: []AgentProfileDescriptor{
			{
				ProfileID: "profile_openclaw", DisplayName: "My OpenClaw", RuntimeKind: RuntimeKindOpenClaw,
				Capabilities: []string{"network.access", "workspace.read"}, Available: true,
				WorkspaceGrants: []WorkspaceGrantDescriptor{{
					GrantID: "grant_openclaw_001", DisplayName: "Product workspace",
					Capabilities: []string{"workspace.read", "network.access"}, Available: true,
				}},
			},
			{ProfileID: "profile_hermes", DisplayName: "Hermes", RuntimeKind: RuntimeKindHermes, Capabilities: []string{"workspace.read"}, Available: false},
		},
	}
	if err := service.PublishCatalog(ctx, scope, "device_test_001", input); err != nil {
		t.Fatal(err)
	}
	view, err := service.GetPersonalAssistant(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if !view.AssistantEnabled || view.HostingMode != HostingModeManaged || view.Revision != 0 || len(view.OwnedAgents) != 2 {
		t.Fatalf("default personal assistant view = %+v", view)
	}
	var openClaw OwnedAgentView
	for _, agent := range view.OwnedAgents {
		if agent.RuntimeKind == RuntimeKindOpenClaw {
			openClaw = agent
		}
	}
	if openClaw.OwnedAgentID == "" || openClaw.Status != AgentStatusOnline || openClaw.DeviceName != "Alice Desktop" || len(openClaw.WorkspaceGrants) != 1 {
		t.Fatalf("OpenClaw view = %+v", openClaw)
	}
	updated, err := service.UpdatePersonalAssistant(ctx, scope, UpdatePersonalAssistantInput{
		AssistantEnabled: true, AssignedAgentID: "organization-agent", HostingMode: HostingModeOwnedAgent, OwnedAgentID: openClaw.OwnedAgentID,
		WorkspaceGrantID: "grant_openclaw_001", FallbackToManaged: true, Revision: view.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 1 || updated.AssignedAgentID != "organization-agent" || updated.OwnedAgentID != openClaw.OwnedAgentID || updated.WorkspaceGrantID != "grant_openclaw_001" || updated.FallbackToManaged {
		t.Fatalf("updated personal assistant view = %+v", updated)
	}
	if _, err := service.UpdatePersonalAssistant(ctx, scope, UpdatePersonalAssistantInput{
		AssistantEnabled: true, HostingMode: HostingModeManaged, Revision: 0,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision error = %v, want ErrConflict", err)
	}

	now = now.Add(time.Minute)
	input.ObservedAt = now
	input.CatalogDigest = strings.Repeat("b", 64)
	input.Profiles = input.Profiles[1:]
	if err := service.PublishCatalog(ctx, scope, "device_test_001", input); err != nil {
		t.Fatal(err)
	}
	view, err = service.GetPersonalAssistant(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if view.OwnedAgentID != openClaw.OwnedAgentID {
		t.Fatalf("binding changed after catalog update: %+v", view)
	}
	if view.AssignedAgentID != "organization-agent" {
		t.Fatalf("assigned organization Agent changed after reload: %+v", view)
	}
	for _, agent := range view.OwnedAgents {
		if agent.OwnedAgentID == openClaw.OwnedAgentID && (agent.Status != AgentStatusUnavailable || agent.WorkspaceGrants[0].Available) {
			t.Fatalf("missing bound agent = %+v, want unavailable", agent)
		}
	}
	other, err := service.GetPersonalAssistant(ctx, OwnerScope{TenantID: "global", OwnerID: "user-2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(other.OwnedAgents) != 0 {
		t.Fatalf("another owner can see agents: %+v", other.OwnedAgents)
	}
}

func TestServiceRejectsWorkspaceGrantFromAnotherAgent(t *testing.T) {
	service, err := NewService(Options{Store: &memoryDirectoryStore{agents: []OwnedAgent{
		{OwnedAgentID: "owned_agent_1", Available: true, WorkspaceGrants: []WorkspaceGrantDescriptor{{GrantID: "grant_one", Available: true}}},
		{OwnedAgentID: "owned_agent_2", Available: true, WorkspaceGrants: []WorkspaceGrantDescriptor{{GrantID: "grant_two", Available: true}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.UpdatePersonalAssistant(context.Background(), OwnerScope{TenantID: "global", OwnerID: "user-1"}, UpdatePersonalAssistantInput{
		AssistantEnabled: true, HostingMode: HostingModeOwnedAgent, OwnedAgentID: "owned_agent_1", WorkspaceGrantID: "grant_two",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdatePersonalAssistant error = %v, want ErrNotFound", err)
	}
}

func TestServiceKeepsPersonalAssistantAvatarsOwnerScoped(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := deviceidentity.NewSQLStore(ctx, database); err != nil {
		t.Fatal(err)
	}
	store, err := NewSQLStore(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 8, 6, 0, 0, 0, time.UTC)
	service, err := NewService(Options{Store: store, Now: func() time.Time { return now }, DefaultTenantID: "global"})
	if err != nil {
		t.Fatal(err)
	}
	owner := OwnerScope{TenantID: "global", OwnerID: "user-1"}
	otherOwner := OwnerScope{TenantID: "global", OwnerID: "user-2"}
	content := []byte("private-avatar")

	info, err := service.SavePersonalAssistantAvatar(ctx, owner, "image/png", content)
	if err != nil {
		t.Fatal(err)
	}
	view, err := service.GetPersonalAssistant(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if info.ETag == "" || view.AvatarETag != info.ETag || view.ExecutionIdentity != "user" || view.CloudFallback {
		t.Fatalf("owner view = %+v avatar = %+v", view, info)
	}
	avatar, err := service.GetPersonalAssistantAvatar(ctx, owner)
	if err != nil || !bytes.Equal(avatar.Content, content) {
		t.Fatalf("owner avatar = %+v error = %v", avatar.Info, err)
	}
	if _, err := service.GetPersonalAssistantAvatar(ctx, otherOwner); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other owner avatar error = %v, want ErrNotFound", err)
	}
	otherView, err := service.GetPersonalAssistant(ctx, otherOwner)
	if err != nil || otherView.AvatarETag != "" {
		t.Fatalf("other owner view = %+v error = %v", otherView, err)
	}
	if err := service.DeletePersonalAssistantAvatar(ctx, otherOwner); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetPersonalAssistantAvatar(ctx, owner); err != nil {
		t.Fatalf("other owner deleted owner avatar: %v", err)
	}
	if err := service.DeletePersonalAssistantAvatar(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetPersonalAssistantAvatar(ctx, owner); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted avatar error = %v, want ErrNotFound", err)
	}
}

func TestServiceKeepsPersonalAssistantNotificationsOwnerScoped(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	store, err := NewSQLStore(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)
	service, err := NewService(Options{Store: store, Now: func() time.Time { return now }, DefaultTenantID: "tenant-a"})
	if err != nil {
		t.Fatal(err)
	}
	owner := OwnerScope{TenantID: "tenant-a", OwnerID: "account-alice"}
	other := OwnerScope{TenantID: "tenant-a", OwnerID: "account-bob"}

	initial, err := service.GetPersonalAssistantNotifications(ctx, owner)
	if err != nil || !initial.ApprovalNotificationsEnabled || initial.Channel != "weixin" || initial.Revision != 0 {
		t.Fatalf("initial notifications = %+v error=%v", initial, err)
	}
	updated, err := service.UpdatePersonalAssistantNotifications(ctx, owner, UpdatePersonalAssistantNotificationsInput{
		ApprovalNotificationsEnabled: false, Revision: initial.Revision,
	})
	if err != nil || updated.ApprovalNotificationsEnabled || updated.Revision != 1 || updated.UpdatedAt == "" {
		t.Fatalf("updated notifications = %+v error=%v", updated, err)
	}
	if _, err := service.UpdatePersonalAssistantNotifications(ctx, owner, UpdatePersonalAssistantNotificationsInput{
		ApprovalNotificationsEnabled: true, Revision: 0,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale notification revision error = %v, want ErrConflict", err)
	}
	otherView, err := service.GetPersonalAssistantNotifications(ctx, other)
	if err != nil || !otherView.ApprovalNotificationsEnabled || otherView.Revision != 0 {
		t.Fatalf("other owner notifications = %+v error=%v", otherView, err)
	}
	stored, err := service.GetPersonalAssistantNotifications(ctx, owner)
	if err != nil || stored.ApprovalNotificationsEnabled || stored.Revision != 1 {
		t.Fatalf("owner notifications = %+v error=%v", stored, err)
	}
}

func TestServiceRejectsInvalidPublishedCatalog(t *testing.T) {
	service, err := NewService(Options{Store: &memoryDirectoryStore{}, Now: func() time.Time {
		return time.Date(2026, 8, 6, 4, 0, 0, 0, time.UTC)
	}})
	if err != nil {
		t.Fatal(err)
	}
	err = service.PublishCatalog(context.Background(), OwnerScope{TenantID: "global", OwnerID: "user-1"}, "device_test_001", PublishCatalogInput{
		CatalogDigest: strings.Repeat("a", 64), ObservedAt: time.Date(2026, 8, 8, 4, 0, 0, 0, time.UTC),
		Profiles: []AgentProfileDescriptor{{ProfileID: "profile_one", DisplayName: "Agent", RuntimeKind: "shell", Available: true}},
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("PublishCatalog error = %v, want ErrInvalidRequest", err)
	}
}

func insertDirectoryTestDevice(t *testing.T, ctx context.Context, store *db.Store, now time.Time) {
	t.Helper()
	_, err := store.Exec(ctx, `
INSERT INTO devices (
 device_id, pairing_id, tenant_id, owner_id, device_name, platform, architecture, client_version,
 protocol_min, protocol_max, public_key_json, credential_revision, allowed_capabilities_json,
 created_at, updated_at, revoked_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
		"device_test_001", "pairing_test_001", "global", "user-1", "Alice Desktop", "win32", "x64", "0.1.0",
		"1.0", "1.0", `{}`, 1, `[]`, now.UnixNano(), now.UnixNano())
	if err != nil {
		t.Fatal(err)
	}
}

type memoryDirectoryStore struct {
	agents []OwnedAgent
}

func (*memoryDirectoryStore) ReplaceOwnedAgents(context.Context, OwnerScope, string, string, time.Time, []OwnedAgent, time.Time) error {
	return nil
}
func (store *memoryDirectoryStore) ListOwnedAgents(context.Context, OwnerScope) ([]OwnedAgent, error) {
	return store.agents, nil
}
func (*memoryDirectoryStore) LoadHosting(context.Context, OwnerScope) (HostingRecord, bool, error) {
	return HostingRecord{}, false, nil
}
func (*memoryDirectoryStore) SaveHosting(context.Context, HostingRecord, int64) (HostingRecord, error) {
	return HostingRecord{}, nil
}
