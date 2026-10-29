package core

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestDirectoryQueryFiltersByOrganizationSkillsRuntimeAvailabilityCostQualityAndRisk(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := NewPlatformProfileRepository(env.UnitOfWork)
	ada := directoryProfile("profile-ada", "instance-ada", AgentKindAI)
	busy := directoryProfile("profile-busy", "instance-busy", AgentKindAI)
	busy.OrgIdentity.DepartmentRef = DepartmentRef{ID: "support"}
	disabled := directoryProfile("profile-disabled", "instance-disabled", AgentKindAI)
	disabled.Enabled = false

	saveDirectoryFixture(t, ctx, repo, ada, AvailabilityAvailable, 10, 0.95, RiskLevelLow)
	saveDirectoryFixture(t, ctx, repo, busy, AvailabilityBusy, 3, 0.75, RiskLevelMedium)
	saveDirectoryFixture(t, ctx, repo, disabled, AvailabilityAvailable, 1, 0.99, RiskLevelLow)

	page, err := repo.QueryDirectory(ctx, AgentDirectoryQuery{
		Organization:      "orrery",
		Department:        "engineering",
		Roles:             []RoleID{"worker"},
		SkillRefs:         []SkillRef{{SkillID: "go"}},
		ToolRefs:          []ToolRef{{ToolID: "git"}},
		RuntimeKind:       RuntimeKindCodex,
		AuthorityScope:    "repo:write",
		MaxCost:           15,
		MinQualityScore:   0.9,
		AllowedRiskLevels: []RiskLevel{RiskLevelLow},
		EnabledOnly:       true,
		IncludeAI:         true,
	})
	if err != nil {
		t.Fatalf("query directory: %v", err)
	}

	if len(page.Entries) != 1 || page.Entries[0].AgentProfileID != ada.ID {
		t.Fatalf("entries = %#v, want only Ada", page.Entries)
	}
	if len(page.Rejected) < 2 {
		t.Fatalf("expected rejected context for filtered candidates: %#v", page.Rejected)
	}
}

func TestDirectoryQueryNeverReturnsNonAgentCandidates(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := NewPlatformProfileRepository(env.UnitOfWork)
	profile := directoryProfile("profile-agent", "instance-agent", AgentKindAI)
	notAgent := directoryProfile("profile-system", "instance-system", AgentKindComposite)
	notAgent.AgentKind = AgentKind("tool")

	saveDirectoryFixture(t, ctx, repo, profile, AvailabilityAvailable, 1, 0.9, RiskLevelLow)
	saveRawDirectoryFixture(t, ctx, repo, notAgent, AvailabilityAvailable, 1, 0.9, RiskLevelLow)

	page, err := repo.QueryDirectory(ctx, AgentDirectoryQuery{EnabledOnly: true, IncludeAI: true, IncludeHumans: true})
	if err != nil {
		t.Fatalf("query directory: %v", err)
	}
	if len(page.Entries) != 1 || page.Entries[0].AgentProfileID != profile.ID {
		t.Fatalf("non-agent candidate leaked into directory: %#v", page)
	}
	if len(page.Rejected) == 0 || page.Rejected[0].Code != "candidate_not_agent" {
		t.Fatalf("expected candidate_not_agent rejection, got %#v", page.Rejected)
	}
}

func TestDirectoryQueryExcludesStableAgentProfiles(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := NewPlatformProfileRepository(env.UnitOfWork)
	selected := directoryProfile("profile-selected", "profile-selected", AgentKindAI)
	available := directoryProfile("profile-available", "profile-available", AgentKindAI)
	saveDirectoryFixture(t, ctx, repo, selected, AvailabilityAvailable, 1, 0.9, RiskLevelLow)
	saveDirectoryFixture(t, ctx, repo, available, AvailabilityAvailable, 1, 0.9, RiskLevelLow)

	page, err := repo.QueryDirectory(ctx, AgentDirectoryQuery{
		EnabledOnly:          true,
		IncludeAI:            true,
		ExcludeAgentProfiles: []AgentProfileID{selected.ID},
	})
	if err != nil {
		t.Fatalf("query directory: %v", err)
	}

	if len(page.Entries) != 1 || page.Entries[0].AgentProfileID != available.ID {
		t.Fatalf("entries = %#v, want only non-excluded profile", page.Entries)
	}
	if len(page.Rejected) == 0 || page.Rejected[0].AgentProfileID != selected.ID || page.Rejected[0].Code != "profile_excluded" {
		t.Fatalf("rejected = %#v, want profile_excluded rejection", page.Rejected)
	}
}

func TestDirectoryQueryFiltersByStableManagerProfile(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := NewPlatformProfileRepository(env.UnitOfWork)
	selected := directoryProfile("profile-selected", "profile-selected", AgentKindAI)
	selected.OrgIdentity.ManagerAgentRef = AgentProfileRef{ID: "profile-manager"}
	other := directoryProfile("profile-other", "profile-other", AgentKindAI)
	other.OrgIdentity.ManagerAgentRef = AgentProfileRef{ID: "profile-other-manager"}
	saveDirectoryFixture(t, ctx, repo, selected, AvailabilityAvailable, 1, 0.9, RiskLevelLow)
	saveDirectoryFixture(t, ctx, repo, other, AvailabilityAvailable, 1, 0.9, RiskLevelLow)

	page, err := repo.QueryDirectory(ctx, AgentDirectoryQuery{
		Manager:   AgentProfileID("profile-manager"),
		IncludeAI: true,
	})
	if err != nil {
		t.Fatalf("query directory: %v", err)
	}

	if len(page.Entries) != 1 || page.Entries[0].AgentProfileID != selected.ID {
		t.Fatalf("entries = %#v, want only selected manager profile", page.Entries)
	}
	if len(page.Rejected) == 0 || page.Rejected[0].AgentProfileID != other.ID || page.Rejected[0].Code != "manager_mismatch" {
		t.Fatalf("rejected = %#v, want manager_mismatch for other profile", page.Rejected)
	}
}

func TestDirectoryQueryDefaultsToInternalTalentAndCanListMarketTalent(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := NewPlatformProfileRepository(env.UnitOfWork)
	internal := directoryProfile("profile-internal", "instance-internal", AgentKindAI)
	market := directoryProfile("profile-market", "instance-market", AgentKindAI)
	market.Placement = TalentPlacement{Status: TalentPlacementMarket}
	market.OrgIdentity = AgentOrgIdentity{EmploymentType: EmploymentTypeAIAgent, SupervisionPolicy: SupervisionPolicyRoot}

	saveDirectoryFixture(t, ctx, repo, internal, AvailabilityAvailable, 1, 0.9, RiskLevelLow)
	saveDirectoryFixture(t, ctx, repo, market, AvailabilityAvailable, 1, 0.9, RiskLevelLow)

	internalPage, err := repo.QueryDirectory(ctx, AgentDirectoryQuery{IncludeAI: true, IncludeHumans: true})
	if err != nil {
		t.Fatalf("query internal directory: %v", err)
	}
	if len(internalPage.Entries) != 1 || internalPage.Entries[0].AgentProfileID != internal.ID {
		t.Fatalf("default query should return only internal talent: %#v", internalPage)
	}

	marketPage, err := repo.QueryDirectory(ctx, AgentDirectoryQuery{Placement: TalentPlacementFilterMarket, IncludeAI: true, IncludeHumans: true})
	if err != nil {
		t.Fatalf("query market directory: %v", err)
	}
	if len(marketPage.Entries) != 1 || marketPage.Entries[0].AgentProfileID != market.ID {
		t.Fatalf("market query should return only market talent: %#v", marketPage)
	}
}

func TestDirectoryQueryHidesPersonalAssistantsUnlessExplicitlyRequested(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := NewPlatformProfileRepository(env.UnitOfWork)
	workforce := directoryProfile("profile-workforce", "instance-workforce", AgentKindAI)
	personalAssistant := directoryProfile("profile-personal-assistant", "instance-personal-assistant", AgentKindAI)
	personalAssistant.PersonalAssistantOwnerID = "owner-1"
	personalAssistant.PersonalAssistantTenantID = "orrery"

	saveDirectoryFixture(t, ctx, repo, workforce, AvailabilityAvailable, 1, 0.9, RiskLevelLow)
	saveDirectoryFixture(t, ctx, repo, personalAssistant, AvailabilityAvailable, 1, 0.9, RiskLevelLow)

	privatePage, err := repo.QueryDirectory(ctx, AgentDirectoryQuery{IncludeAI: true, IncludeHumans: true})
	if err != nil {
		t.Fatalf("query private directory: %v", err)
	}
	if privatePage.Total != 1 || len(privatePage.Entries) != 1 || privatePage.Entries[0].AgentProfileID != workforce.ID {
		t.Fatalf("private directory = %#v, want only workforce profile", privatePage)
	}
	for _, rejected := range privatePage.Rejected {
		if rejected.AgentProfileID == personalAssistant.ID {
			t.Fatalf("personal assistant leaked through rejected entries: %#v", privatePage.Rejected)
		}
	}

	internalPage, err := repo.QueryDirectory(ctx, AgentDirectoryQuery{
		IncludeAI: true, IncludeHumans: true, IncludePersonalAssistants: true,
	})
	if err != nil {
		t.Fatalf("query internal directory: %v", err)
	}
	if internalPage.Total != 2 || len(internalPage.Entries) != 2 {
		t.Fatalf("internal directory = %#v, want both profiles", internalPage)
	}
}

func TestDirectorySortUsesStableTieBreakerForEqualScores(t *testing.T) {
	entries := []AgentDirectoryEntry{
		{AgentProfileID: "profile-zed", RealName: "Zed", DisplayName: "Zed", QualitySignal: AgentQualitySignal{Score: 0.9}},
		{AgentProfileID: "profile-ada", RealName: "Ada", DisplayName: "Ada", QualitySignal: AgentQualitySignal{Score: 0.9}},
		{AgentProfileID: "profile-mia", RealName: "Mia", DisplayName: "Mia", QualitySignal: AgentQualitySignal{Score: 0.9}},
	}

	sortDirectoryEntries(entries, DirectorySortScoreDesc)

	want := []AgentProfileID{"profile-ada", "profile-mia", "profile-zed"}
	for i, id := range want {
		if entries[i].AgentProfileID != id {
			t.Fatalf("entries[%d] = %s, want %s; sorted entries = %#v", i, entries[i].AgentProfileID, id, entries)
		}
	}
}

func directoryProfile(profileID AgentProfileID, instanceID AgentInstanceID, kind AgentKind) AgentProfile {
	return AgentProfile{
		ID:          profileID,
		RealName:    "艾达",
		DisplayName: "艾达",
		RoleName:    "Worker",
		AgentKind:   kind,
		OrgIdentity: AgentOrgIdentity{
			OrganizationRef:   OrganizationRef{ID: "orrery"},
			DepartmentRef:     DepartmentRef{ID: "engineering"},
			PositionRef:       PositionRef{ID: "backend"},
			RoleRefs:          []RoleRef{{ID: "worker"}},
			ManagerAgentRef:   AgentProfileRef{ID: "profile-manager-001"},
			ReportingLineRef:  ReportingLineRef{ID: "engineering-line"},
			AuthorityScope:    "repo:write",
			EmploymentType:    EmploymentTypeAIAgent,
			SupervisionPolicy: SupervisionPolicyManaged,
		},
		Persona:                  AgentPersona{ResponsibilityBoundary: "负责可靠地执行工程任务并提交交付物和证据。"},
		BaseAgent:                directoryBaseAgentSpec(kind),
		PersonalMemoryProfileRef: MemoryProfileRef{MemoryProfileID: "memory-ada", OwnerAgentID: AgentID(instanceID), MemoryKind: MemoryKindPersonal},
		SkillRefs:                []SkillRef{{SkillID: "go"}, {SkillID: "testing"}},
		ToolRefs:                 []ToolRef{{ToolID: "git"}},
		RuntimeSpec:              RuntimeSpec{Kind: RuntimeKindCodex, ConfigRef: "runtime-config"},
		Enabled:                  true,
	}
}

func directoryBaseAgentSpec(kind AgentKind) BaseAgentSpec {
	return BaseAgentSpec{
		InheritsBaseAgent: true,
		AuditRequired:     true,
		SystemPrompt:      "你是测试用托管 Agent，必须经 BaseAgent 审计壳执行。",
		LLMAPIKeyRef:      "secret://system/llm-api-key",
		SkillScope:        "profile_bound",
		ToolScope:         "profile_bound",
		MCPScope:          "profile_bound",
	}
}

func saveDirectoryFixture(t *testing.T, ctx context.Context, repo *PlatformProfileRepository, profile AgentProfile, status AvailabilityStatus, cost float64, quality float64, risk RiskLevel) {
	t.Helper()
	if err := repo.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save profile: %v", err)
	}
	if err := repo.SaveAvailability(ctx, AgentAvailability{AgentProfileID: profile.ID, AgentInstanceID: AgentInstanceID(profile.PersonalMemoryProfileRef.OwnerAgentID), Status: status, Capacity: 4, AvailableFrom: time.Now().Add(-time.Hour), AvailableUntil: time.Now().Add(time.Hour), UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("save availability: %v", err)
	}
	if err := repo.SaveCostProfile(ctx, AgentCostProfile{AgentProfileID: profile.ID, EstimatedCostPerHour: cost}); err != nil {
		t.Fatalf("save cost: %v", err)
	}
	if err := repo.SaveQualitySignal(ctx, AgentQualitySignal{AgentProfileID: profile.ID, Score: quality, SampleSize: 10}); err != nil {
		t.Fatalf("save quality: %v", err)
	}
	if err := repo.SaveRiskProfile(ctx, AgentRiskProfile{AgentProfileID: profile.ID, RiskLevel: risk}); err != nil {
		t.Fatalf("save risk: %v", err)
	}
}

func saveRawDirectoryFixture(t *testing.T, ctx context.Context, repo *PlatformProfileRepository, profile AgentProfile, status AvailabilityStatus, cost float64, quality float64, risk RiskLevel) {
	t.Helper()
	payload, err := encodeProfilePayload(profile)
	if err != nil {
		t.Fatalf("encode raw profile: %v", err)
	}
	if err := repo.withTx(ctx, func(ctx context.Context, tx uow.Tx) error {
		tx.Put(profileRecordTable, profile.ID.String(), map[string]any{
			"profile_id": profile.ID.String(),
			"enabled":    profile.Enabled,
			"kind":       string(profile.AgentKind),
			"payload":    payload,
		})
		return nil
	}); err != nil {
		t.Fatalf("save raw profile: %v", err)
	}
	if err := repo.SaveAvailability(ctx, AgentAvailability{AgentProfileID: profile.ID, AgentInstanceID: AgentInstanceID(profile.PersonalMemoryProfileRef.OwnerAgentID), Status: status, Capacity: 4, AvailableFrom: time.Now().Add(-time.Hour), AvailableUntil: time.Now().Add(time.Hour), UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("save availability: %v", err)
	}
	if err := repo.SaveCostProfile(ctx, AgentCostProfile{AgentProfileID: profile.ID, EstimatedCostPerHour: cost}); err != nil {
		t.Fatalf("save cost: %v", err)
	}
	if err := repo.SaveQualitySignal(ctx, AgentQualitySignal{AgentProfileID: profile.ID, Score: quality, SampleSize: 10}); err != nil {
		t.Fatalf("save quality: %v", err)
	}
	if err := repo.SaveRiskProfile(ctx, AgentRiskProfile{AgentProfileID: profile.ID, RiskLevel: risk}); err != nil {
		t.Fatalf("save risk: %v", err)
	}
}
