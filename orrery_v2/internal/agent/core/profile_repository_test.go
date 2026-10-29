package core

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestPlatformProfileRepositoryPreservesAbsoluteWorkspaceRoot(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := NewPlatformProfileRepository(env.UnitOfWork)
	profile := directoryProfile("profile-workspace", "instance-workspace", AgentKindAI)
	profile.WorkspaceRoot = filepath.Join(t.TempDir(), "profile-workspace")

	if err := repo.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save profile: %v", err)
	}
	got, err := repo.GetProfile(ctx, profile.ID)
	if err != nil {
		t.Fatalf("get profile: %v", err)
	}
	if got.WorkspaceRoot != profile.WorkspaceRoot {
		t.Fatalf("workspace root = %q, want %q", got.WorkspaceRoot, profile.WorkspaceRoot)
	}
}

func TestPlatformProfileRepositoryRejectsRelativeWorkspaceRoot(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := NewPlatformProfileRepository(env.UnitOfWork)
	profile := directoryProfile("profile-workspace", "instance-workspace", AgentKindAI)
	profile.WorkspaceRoot = "relative/workspace"

	err := repo.SaveProfile(ctx, profile)
	var validationErr AgentProfileValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error = %T %[1]v, want AgentProfileValidationError", err)
	}
	assertValidationError(t, validationErr.Errors, CodeInvalidRef, "workspace_root")
}

func TestPlatformProfileRepositoryRollsBackWhenProfileSaveHookFails(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := NewPlatformProfileRepository(env.UnitOfWork).WithProfileSaveHook(profileSaveHookFunc(
		func(context.Context, uow.Tx, *AgentProfile, AgentProfile) error {
			return errors.New("projection unavailable")
		},
	))
	profile := directoryProfile("profile-hook-rollback", "instance-hook-rollback", AgentKindHuman)

	if err := repo.SaveProfile(ctx, profile); err == nil {
		t.Fatal("SaveProfile error = nil, want hook failure")
	}
	if _, err := repo.GetProfile(ctx, profile.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetProfile error = %v, want ErrNotFound", err)
	}
}

func TestPlatformProfileRepositoryMutateProfileUpdatesAtomicallyAndRunsHooks(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := NewPlatformProfileRepository(env.UnitOfWork)
	profile := directoryProfile("profile-role-sync", "instance-role-sync", AgentKindHuman)
	profile.OrgIdentity.RoleRefs = []RoleRef{{ID: "member"}}
	profile.WorkspaceRoot = filepath.Join(t.TempDir(), "role-sync")
	if err := repo.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	hookCalls := 0
	repo.WithProfileSaveHook(profileSaveHookFunc(func(_ context.Context, _ uow.Tx, previous *AgentProfile, current AgentProfile) error {
		hookCalls++
		if previous == nil || previous.ID != profile.ID || current.ID != profile.ID {
			t.Fatalf("hook previous=%+v current=%+v", previous, current)
		}
		return nil
	}))
	updated, changed, err := repo.MutateProfile(ctx, profile.ID, func(current AgentProfile) (AgentProfile, bool, error) {
		current.OrgIdentity.RoleRefs = append(current.OrgIdentity.RoleRefs, RoleRef{ID: "approval.reviewer"})
		return current, true, nil
	})
	if err != nil || !changed || hookCalls != 1 {
		t.Fatalf("updated=%+v changed=%v hook_calls=%d err=%v", updated, changed, hookCalls, err)
	}
	if updated.WorkspaceRoot != profile.WorkspaceRoot || len(updated.OrgIdentity.RoleRefs) != 2 || updated.OrgIdentity.RoleRefs[1].ID != "approval.reviewer" {
		t.Fatalf("updated profile = %+v", updated)
	}
	stored, err := repo.GetProfile(ctx, profile.ID)
	if err != nil || stored.WorkspaceRoot != profile.WorkspaceRoot || len(stored.OrgIdentity.RoleRefs) != 2 {
		t.Fatalf("stored profile=%+v err=%v", stored, err)
	}

	unchanged, changed, err := repo.MutateProfile(ctx, profile.ID, func(current AgentProfile) (AgentProfile, bool, error) {
		return current, false, nil
	})
	if err != nil || changed || hookCalls != 1 || unchanged.ID != profile.ID {
		t.Fatalf("unchanged=%+v changed=%v hook_calls=%d err=%v", unchanged, changed, hookCalls, err)
	}
}

func TestPlatformProfileRepositoryMutateProfileRollsBackRejectedMutation(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := NewPlatformProfileRepository(env.UnitOfWork)
	profile := directoryProfile("profile-role-sync-reject", "instance-role-sync-reject", AgentKindHuman)
	if err := repo.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	_, changed, err := repo.MutateProfile(ctx, profile.ID, func(current AgentProfile) (AgentProfile, bool, error) {
		current.ID = "different-profile"
		return current, true, nil
	})
	if err == nil || changed {
		t.Fatalf("changed=%v err=%v, want rejected mutation", changed, err)
	}
	stored, getErr := repo.GetProfile(ctx, profile.ID)
	if getErr != nil || stored.ID != profile.ID {
		t.Fatalf("stored=%+v err=%v", stored, getErr)
	}
}

type profileSaveHookFunc func(context.Context, uow.Tx, *AgentProfile, AgentProfile) error

func (f profileSaveHookFunc) BeforeProfileSave(ctx context.Context, tx uow.Tx, previous *AgentProfile, current AgentProfile) error {
	return f(ctx, tx, previous, current)
}

func TestPlatformProfileRepositoryRejectsManagedProfileWithoutBaseAgentContract(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := NewPlatformProfileRepository(env.UnitOfWork)
	profile := directoryProfile("profile-unmanaged", "instance-unmanaged", AgentKindAI)
	profile.BaseAgent = BaseAgentSpec{}

	err := repo.SaveProfile(ctx, profile)

	if err == nil {
		t.Fatalf("expected invalid managed profile to be rejected")
	}
	var validationErr AgentProfileValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error = %T %[1]v, want AgentProfileValidationError", err)
	}
	assertValidationError(t, validationErr.Errors, CodeMissingRequiredField, "base_agent.inherits_base_agent")
}

func TestPlatformProfileRepositoryLoadsCompleteProfiles(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := NewPlatformProfileRepository(env.UnitOfWork)
	profile := directoryProfile("profile-ada", "instance-ada", AgentKindAI)
	profile.QAEnabled = true

	if err := repo.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save profile: %v", err)
	}
	got, err := repo.GetProfile(ctx, profile.ID)
	if err != nil {
		t.Fatalf("get profile: %v", err)
	}

	if got.RealName != profile.RealName || got.OrgIdentity.OrganizationRef.ID != "orrery" || got.RuntimeSpec.Kind != RuntimeKindCodex {
		t.Fatalf("loaded incomplete profile: %#v", got)
	}
	if !got.QAEnabled {
		t.Fatalf("profile qa_enabled was not preserved: %#v", got)
	}
	if len(got.SkillRefs) != 2 || len(got.ToolRefs) != 1 || got.PersonalMemoryProfileRef.MemoryKind != MemoryKindPersonal {
		t.Fatalf("profile refs not preserved: %#v", got)
	}
}

func TestPlatformProfileRepositoryLoadsRequestedProfilesOnly(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := NewPlatformProfileRepository(env.UnitOfWork)
	wanted := directoryProfile("profile-wanted", "instance-wanted", AgentKindAI)
	unrelated := directoryProfile("profile-unrelated", "instance-unrelated", AgentKindAI)
	if err := repo.SaveProfile(ctx, wanted); err != nil {
		t.Fatalf("save wanted profile: %v", err)
	}
	if err := repo.SaveProfile(ctx, unrelated); err != nil {
		t.Fatalf("save unrelated profile: %v", err)
	}

	profiles, err := repo.GetProfiles(ctx, []AgentProfileID{wanted.ID, "profile-missing", wanted.ID})
	if err != nil {
		t.Fatalf("get profiles: %v", err)
	}
	if len(profiles) != 1 || profiles[wanted.ID].ID != wanted.ID {
		t.Fatalf("profiles = %#v, want only %s", profiles, wanted.ID)
	}
}

func TestPlatformProfileRepositoryLoadsAvailabilityCostQualityAndRisk(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := NewPlatformProfileRepository(env.UnitOfWork)
	profile := directoryProfile("profile-ada", "instance-ada", AgentKindAI)
	availability := AgentAvailability{AgentProfileID: profile.ID, AgentInstanceID: "instance-ada", Status: AvailabilityAvailable, Capacity: 4, CurrentLoad: 1, Source: "runtime", UpdatedAt: time.Now().UTC()}
	cost := AgentCostProfile{AgentProfileID: profile.ID, EstimatedCostPerHour: 12, EstimatedCostPerTask: 20, BudgetClass: "standard", Source: "catalog"}
	quality := AgentQualitySignal{AgentProfileID: profile.ID, Score: 0.93, SampleSize: 20}
	risk := AgentRiskProfile{AgentProfileID: profile.ID, RiskLevel: RiskLevelLow}

	if err := repo.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save profile: %v", err)
	}
	if err := repo.SaveAvailability(ctx, availability); err != nil {
		t.Fatalf("save availability: %v", err)
	}
	if err := repo.SaveCostProfile(ctx, cost); err != nil {
		t.Fatalf("save cost: %v", err)
	}
	if err := repo.SaveQualitySignal(ctx, quality); err != nil {
		t.Fatalf("save quality: %v", err)
	}
	if err := repo.SaveRiskProfile(ctx, risk); err != nil {
		t.Fatalf("save risk: %v", err)
	}

	got, err := repo.GetAvailability(ctx, profile.ID)
	if err != nil {
		t.Fatalf("get availability: %v", err)
	}
	if got.Status != AvailabilityAvailable || got.CurrentLoad != 1 {
		t.Fatalf("availability = %#v", got)
	}
	page, err := repo.QueryDirectory(ctx, AgentDirectoryQuery{EnabledOnly: true})
	if err != nil {
		t.Fatalf("query directory: %v", err)
	}
	if len(page.Entries) != 1 || page.Entries[0].CostProfile.EstimatedCostPerHour != 12 || page.Entries[0].QualitySignal.Score != 0.93 || page.Entries[0].RiskProfile.RiskLevel != RiskLevelLow {
		t.Fatalf("directory did not join operational signals: %#v", page)
	}
	entry, err := repo.GetDirectoryEntry(ctx, profile.ID)
	if err != nil {
		t.Fatalf("get directory entry: %v", err)
	}
	if entry.Availability.Status != AvailabilityAvailable || entry.CostProfile.EstimatedCostPerHour != 12 || entry.QualitySignal.Score != 0.93 || entry.RiskProfile.RiskLevel != RiskLevelLow {
		t.Fatalf("directory entry did not join operational signals: %#v", entry)
	}
}

func TestPlatformProfileRepositoryDirectoryIgnoresUnrelatedRecords(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := NewPlatformProfileRepository(env.UnitOfWork)
	profile := directoryProfile("profile-bounded", "instance-bounded", AgentKindAI)
	saveDirectoryFixture(t, ctx, repo, profile, AvailabilityAvailable, 12, 0.93, RiskLevelLow)
	if err := repo.withTx(ctx, func(ctx context.Context, tx uow.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO platform_records (table_name, record_key, payload_json) VALUES (?, ?, ?)`, "aaa_unrelated", "broken", "{")
		return err
	}); err != nil {
		t.Fatalf("insert invalid unrelated record: %v", err)
	}

	page, err := repo.QueryDirectory(ctx, AgentDirectoryQuery{EnabledOnly: true, IncludeAI: true})
	if err != nil {
		t.Fatalf("QueryDirectory: %v", err)
	}
	if len(page.Entries) != 1 || page.Entries[0].AgentProfileID != profile.ID {
		t.Fatalf("entries = %+v", page.Entries)
	}
	entry := page.Entries[0]
	if entry.Availability.Status != AvailabilityAvailable || entry.CostProfile.EstimatedCostPerHour != 12 || entry.QualitySignal.Score != 0.93 || entry.RiskProfile.RiskLevel != RiskLevelLow {
		t.Fatalf("signals = %+v", entry)
	}
}

func TestPlatformProfileRepositoryDirectoryEntryUsesStableProfileIdentityWithRuntimeAvailability(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := NewPlatformProfileRepository(env.UnitOfWork)
	profile := directoryProfile("profile-ada", "profile-ada", AgentKindAI)

	if err := repo.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save profile: %v", err)
	}
	if err := repo.SaveAvailability(ctx, AgentAvailability{
		AgentProfileID:  profile.ID,
		AgentInstanceID: "profile-ada:runtime",
		Status:          AvailabilityAvailable,
		Capacity:        4,
		UpdatedAt:       time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save availability: %v", err)
	}

	page, err := repo.QueryDirectory(ctx, AgentDirectoryQuery{EnabledOnly: true, IncludeAI: true})
	if err != nil {
		t.Fatalf("query directory: %v", err)
	}
	if len(page.Entries) != 1 {
		t.Fatalf("entries = %#v, want one entry", page.Entries)
	}
	entry := page.Entries[0]
	if entry.AgentInstanceID != AgentInstanceID(profile.ID) {
		t.Fatalf("directory entry instance id = %q, want stable profile identity %q", entry.AgentInstanceID, profile.ID)
	}
	if entry.Availability.AgentInstanceID != "profile-ada:runtime" || entry.Availability.Status != AvailabilityAvailable {
		t.Fatalf("availability should retain runtime signal identity: %#v", entry.Availability)
	}
}

func TestPlatformProfileRepositoryDoesNotInventDirectoryInstanceWithoutAvailability(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := NewPlatformProfileRepository(env.UnitOfWork)
	profile := directoryProfile("profile-no-runtime", "instance-unused", AgentKindAI)

	if err := repo.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save profile: %v", err)
	}

	page, err := repo.QueryDirectory(ctx, AgentDirectoryQuery{IncludeAI: true})
	if err != nil {
		t.Fatalf("query directory: %v", err)
	}
	if len(page.Entries) != 1 {
		t.Fatalf("entries = %#v", page.Entries)
	}
	entry := page.Entries[0]
	if entry.AgentProfileID != profile.ID {
		t.Fatalf("entry profile id = %s, want %s", entry.AgentProfileID, profile.ID)
	}
	if entry.AgentInstanceID != "" {
		t.Fatalf("directory must not invent runtime instance id from profile id: %#v", entry)
	}
	if entry.Availability.AgentInstanceID != "" || entry.Availability.Status != AvailabilityUnknown {
		t.Fatalf("availability = %#v, want unknown without runtime instance", entry.Availability)
	}
}

func TestPlatformProfileRepositoryDeleteProfileRemovesDirectoryEntryAndKeepsTombstone(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := NewPlatformProfileRepository(env.UnitOfWork)
	profile := directoryProfile("profile-ada", "instance-ada", AgentKindAI)

	saveDirectoryFixture(t, ctx, repo, profile, AvailabilityAvailable, 12, 0.93, RiskLevelLow)

	if err := repo.DeleteProfile(ctx, profile.ID, "workforce.member.delete"); err != nil {
		t.Fatalf("delete profile: %v", err)
	}

	if _, err := repo.GetProfile(ctx, profile.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted profile should not be loadable")
	}
	page, err := repo.QueryDirectory(ctx, AgentDirectoryQuery{})
	if err != nil {
		t.Fatalf("query directory: %v", err)
	}
	if page.Total != 0 || len(page.Entries) != 0 {
		t.Fatalf("deleted profile should not remain in directory: %#v", page)
	}
	deleted, err := repo.IsProfileDeleted(ctx, profile.ID)
	if err != nil {
		t.Fatalf("is profile deleted: %v", err)
	}
	if !deleted {
		t.Fatalf("deleted profile tombstone was not recorded")
	}
	deletedIDs, err := repo.ListDeletedProfileIDs(ctx)
	if err != nil {
		t.Fatalf("list deleted profiles: %v", err)
	}
	if len(deletedIDs) != 1 || deletedIDs[0] != profile.ID {
		t.Fatalf("deleted profile ids = %#v", deletedIDs)
	}
}

func TestPlatformProfileRepositoryDoesNotRecoverSoftDeletedProfileAsMarketTalent(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := NewPlatformProfileRepository(env.UnitOfWork)
	profile := directoryProfile("profile-soft-deleted", "instance-soft-deleted", AgentKindAI)
	profile.Enabled = false

	if err := repo.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save profile: %v", err)
	}
	if err := repo.SaveAvailability(ctx, AgentAvailability{
		AgentProfileID:  profile.ID,
		AgentInstanceID: "instance-soft-deleted",
		Status:          AvailabilityDisabled,
		Reason:          "workforce member deleted",
		Source:          "operations.workforce",
		UpdatedAt:       time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save availability: %v", err)
	}

	page, err := repo.QueryDirectory(ctx, AgentDirectoryQuery{Placement: TalentPlacementFilterMarket, IncludeAI: true, IncludeHumans: true})
	if err != nil {
		t.Fatalf("query directory: %v", err)
	}
	if page.Total != 0 || len(page.Entries) != 0 {
		t.Fatalf("soft-deleted profile should not surface as market talent: %#v", page)
	}
	deleted, err := repo.IsProfileDeleted(ctx, profile.ID)
	if err != nil {
		t.Fatalf("is profile deleted: %v", err)
	}
	if deleted {
		t.Fatalf("soft-deleted profile without tombstone should not count as hard-deleted")
	}
}

func TestPlatformProfileRepositoryDirectoryEntryCarriesBaseAgentSummary(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := NewPlatformProfileRepository(env.UnitOfWork)
	profile := directoryProfile("profile-ada", "instance-ada", AgentKindAI)
	profile.BaseAgent = BaseAgentSpec{
		InheritsBaseAgent: true,
		AuditRequired:     true,
		SystemPrompt:      "你是目录测试托管 Agent。",
		LLMAPIKeyRef:      "secret://system/llm-api-key",
		SkillScope:        "skill-scope",
		ToolScope:         "tool-scope",
		MCPScope:          "mcp-scope",
	}
	profile.QAEnabled = true

	if err := repo.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save profile: %v", err)
	}
	page, err := repo.QueryDirectory(ctx, AgentDirectoryQuery{EnabledOnly: true, IncludeAI: true})
	if err != nil {
		t.Fatalf("query directory: %v", err)
	}
	if len(page.Entries) != 1 {
		t.Fatalf("entries = %#v", page.Entries)
	}

	summary := page.Entries[0].BaseAgentSummary
	if !summary.InheritsBaseAgent || !summary.AuditRequired || !summary.HasSystemPrompt || !summary.HasLLMAPIKeyRef {
		t.Fatalf("base agent summary missing managed flags: %#v", summary)
	}
	if summary.SkillScope != "skill-scope" || summary.ToolScope != "tool-scope" || summary.MCPScope != "mcp-scope" {
		t.Fatalf("base agent scopes = %#v", summary)
	}
	if !page.Entries[0].QAEnabled {
		t.Fatalf("directory entry should expose qa_enabled: %#v", page.Entries[0])
	}
}
