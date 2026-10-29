package core

import (
	"context"
	"testing"
)

func TestStaticInstanceProvisionerIgnoresScopedWarmAgentIdentities(t *testing.T) {
	profile := directoryProfile("profile-ada", "instance-ada", AgentKindAI)
	provisioner := NewStaticInstanceProvisioner(map[AgentProfileID]AgentInstanceRef{
		profile.ID: {ID: "warm-instance"},
	})
	ref, err := provisioner.GetOrCreateInstance(context.Background(), profile, AgentInstanceScope{MissionID: "mission-001", ReuseWarm: true, IsolationPolicy: "shared"})
	if err != nil {
		t.Fatalf("get warm instance: %v", err)
	}
	if ref.ID != AgentInstanceID(profile.ID) {
		t.Fatalf("warm instance id = %q, want stable profile identity %q", ref.ID, profile.ID)
	}

	fresh, err := provisioner.GetOrCreateInstance(context.Background(), profile, AgentInstanceScope{MissionID: "mission-002", ReuseWarm: false, IsolationPolicy: "mission"})
	if err != nil {
		t.Fatalf("get fresh instance: %v", err)
	}
	if fresh.ID != AgentInstanceID(profile.ID) {
		t.Fatalf("fresh instance id = %q, want stable profile identity %q", fresh.ID, profile.ID)
	}
}

func TestStaticInstanceProvisionerDoesNotCreateMissionScopedAgentIdentity(t *testing.T) {
	profile := directoryProfile("profile-ada", "instance-ada", AgentKindAI)
	provisioner := NewStaticInstanceProvisioner(nil)

	ref, err := provisioner.GetOrCreateInstance(context.Background(), profile, AgentInstanceScope{
		MissionID:       "mission-001",
		ReuseWarm:       true,
		IsolationPolicy: "mission",
	})
	if err != nil {
		t.Fatalf("get mission instance: %v", err)
	}
	if ref.ID != AgentInstanceID(profile.ID) {
		t.Fatalf("mission scoped instance id = %q, want stable profile identity %q", ref.ID, profile.ID)
	}
}
