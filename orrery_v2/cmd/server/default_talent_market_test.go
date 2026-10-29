package main

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/testkit"
)

func TestDefaultTalentMarketProfilesAreOpenSourceSafe(t *testing.T) {
	profiles := defaultTalentMarketProfiles()
	if len(profiles) != 5 {
		t.Fatalf("default talent market profiles = %d, want 5", len(profiles))
	}
	wantIDs := map[agentcore.AgentProfileID]bool{
		"talent-market-backoffice-finance-planning":       true,
		"talent-market-backoffice-finance-control":        true,
		"talent-market-backoffice-legal-contract":         true,
		"talent-market-backoffice-hr-operations":          true,
		"talent-market-backoffice-procurement-operations": true,
	}
	seen := map[agentcore.AgentProfileID]bool{}
	for _, profile := range profiles {
		if seen[profile.ID] {
			t.Fatalf("duplicate talent market profile %s", profile.ID)
		}
		seen[profile.ID] = true
		if !wantIDs[profile.ID] {
			t.Fatalf("unexpected non-backoffice talent market profile %s", profile.ID)
		}
		if errs := agentcore.ValidateAgentProfile(profile); len(errs) > 0 {
			t.Fatalf("profile %s should validate: %+v", profile.ID, errs)
		}
		if !profile.Placement.IsMarket(profile.OrgIdentity) {
			t.Fatalf("profile %s placement = %+v", profile.ID, profile.Placement)
		}
		if profile.OrgIdentity.OrganizationRef.ID != "" || profile.OrgIdentity.DepartmentRef.ID != "" || profile.OrgIdentity.PositionRef.ID != "" {
			t.Fatalf("profile %s should not belong to an organization: %+v", profile.ID, profile.OrgIdentity)
		}
		if profile.RuntimeSpec.Kind != agentcore.RuntimeKindManaged {
			t.Fatalf("profile %s runtime = %+v", profile.ID, profile.RuntimeSpec)
		}
		if len(profile.ToolRefs) != 0 || len(profile.SkillRefs) != 0 {
			t.Fatalf("profile %s should not carry external bindings: tools=%+v skills=%+v", profile.ID, profile.ToolRefs, profile.SkillRefs)
		}
	}
}

func TestEnsureDefaultTalentMarketProfilesIsIdempotent(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)

	if err := ensureDefaultTalentMarketProfiles(ctx, profiles); err != nil {
		t.Fatalf("first bootstrap: %v", err)
	}
	if err := ensureDefaultTalentMarketProfiles(ctx, profiles); err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
	page, err := profiles.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{
		Placement:     agentcore.TalentPlacementFilterMarket,
		IncludeAI:     true,
		IncludeHumans: true,
	})
	if err != nil {
		t.Fatalf("query talent market: %v", err)
	}
	if page.Total != 5 {
		t.Fatalf("talent market total = %d, want 5", page.Total)
	}
	for _, entry := range page.Entries {
		if entry.Availability.Status != agentcore.AvailabilityAvailable {
			t.Fatalf("profile %s availability = %+v", entry.AgentProfileID, entry.Availability)
		}
	}
}
