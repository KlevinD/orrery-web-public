package responsibility

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/testkit"
)

func TestPeopleResponsibilityReconcilerCreatesPortfolioForBatchEvaluation(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	now := env.Clock.Now().UTC()
	source := &staticPeopleResponsibilitySource{resolution: PeopleResponsibilityResolution{
		SubjectID: "subject-1", SourceRevision: "5:2", AsOf: now,
		Items: []PeopleResolvedResponsibility{{ID: "responsibility-1", Code: "DELIVERY", Title: "Deliver", ValidFrom: now, Revision: 1}},
	}}
	profiles := staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"agent-1": {ID: "agent-1", DisplayName: "Agent One", Enabled: true},
	}}
	roleSync := &capturingPeopleResponsibilityRoleSync{changed: true}
	reconciler, err := NewPeopleResponsibilityReconciler(PeopleResponsibilityReconcilerOptions{
		Repository: repository, Source: source, Profiles: profiles, RoleSync: roleSync, Clock: env.Clock,
		PeopleTenantID: "people-1", OrreryTenantID: "tenant-1", AgentBindings: map[string]string{"subject-1": "agent-1"},
		HumanRoleMappings: map[string]string{"DELIVERY": "approval.reviewer"}, ReconcileInterval: time.Minute,
	})
	if err != nil {
		t.Fatalf("new reconciler: %v", err)
	}
	stats, err := reconciler.Reconcile(context.Background())
	if err != nil || stats.PortfoliosChanged != 1 || stats.RoleProfilesChanged != 1 || stats.PulsesEnsured != 0 || stats.GapsOpen != 0 {
		t.Fatalf("reconcile stats=%+v err=%v", stats, err)
	}
	if len(roleSync.inputs) != 1 || len(roleSync.inputs[0].ManagedRoleIDs) != 1 || roleSync.inputs[0].ManagedRoleIDs[0] != "approval.reviewer" ||
		len(roleSync.inputs[0].DesiredRoleIDs) != 1 || roleSync.inputs[0].DesiredRoleIDs[0] != "approval.reviewer" || roleSync.inputs[0].SourceRevision != "5:2" {
		t.Fatalf("role sync inputs = %+v", roleSync.inputs)
	}
	portfolio, found, err := repository.GetPeopleResponsibilityPortfolio(context.Background(), PeopleResponsibilityPortfolioID(PeopleAgentBindingID("tenant-1", "people-1", "subject-1")))
	if err != nil || !found || portfolio.PulseScheduledTaskID != "" || portfolio.SourceDigest == "" {
		t.Fatalf("portfolio=%+v found=%v err=%v", portfolio, found, err)
	}
}

func TestPeopleResponsibilityReconcilerOpensGapAndPausesPortfolio(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	now := env.Clock.Now().UTC()
	portfolio := seedPeoplePortfolio(t, repository, now, "tenant-1", "people-1", "subject-1", "agent-1", "digest-1")
	portfolio.PulseScheduledTaskID = "scheduled-task-1"
	if _, _, err := repository.SavePeopleResponsibilityPortfolio(context.Background(), portfolio, now.Add(time.Second)); err != nil {
		t.Fatalf("attach pulse: %v", err)
	}
	source := &staticPeopleResponsibilitySource{err: errors.New("upstream unavailable")}
	roleSync := &capturingPeopleResponsibilityRoleSync{}
	reconciler, err := NewPeopleResponsibilityReconciler(PeopleResponsibilityReconcilerOptions{
		Repository: repository, Source: source, Profiles: staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{"agent-1": {ID: "agent-1", Enabled: true}}}, RoleSync: roleSync, Clock: env.Clock,
		PeopleTenantID: "people-1", OrreryTenantID: "tenant-1", AgentBindings: map[string]string{"subject-1": "agent-1"},
		HumanRoleMappings: map[string]string{"DELIVERY": "approval.reviewer"}, ReconcileInterval: time.Minute,
	})
	if err != nil {
		t.Fatalf("new reconciler: %v", err)
	}
	stats, err := reconciler.Reconcile(context.Background())
	if err == nil || stats.GapsOpen != 1 || stats.PulsesDisabled != 0 {
		t.Fatalf("reconcile stats=%+v err=%v", stats, err)
	}
	if len(roleSync.inputs) != 0 {
		t.Fatalf("source failure must retain last known roles, inputs=%+v", roleSync.inputs)
	}
	stored, found, getErr := repository.GetPeopleResponsibilityPortfolio(context.Background(), portfolio.ID)
	if getErr != nil || !found || stored.Status != peoplePortfolioStatusPaused {
		t.Fatalf("stored portfolio=%+v found=%v err=%v", stored, found, getErr)
	}
	gap, found, getErr := repository.GetPeopleResponsibilityGap(context.Background(), PeopleResponsibilityGapID(portfolio.BindingID))
	if getErr != nil || !found || !gap.Open || gap.Code != "people_source_unavailable" {
		t.Fatalf("gap=%+v found=%v err=%v", gap, found, getErr)
	}
}

func TestPeopleResponsibilityReconcilerDoesNotScheduleUnassignedAgent(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	roleSync := &capturingPeopleResponsibilityRoleSync{changed: true}
	reconciler, err := NewPeopleResponsibilityReconciler(PeopleResponsibilityReconcilerOptions{
		Repository: repository,
		Source:     &staticPeopleResponsibilitySource{resolution: PeopleResponsibilityResolution{SubjectID: "subject-1", SourceRevision: "5:2", AsOf: env.Clock.Now().UTC(), Items: []PeopleResolvedResponsibility{}}},
		Profiles:   staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{"agent-1": {ID: "agent-1", Enabled: true}}},
		RoleSync:   roleSync, Clock: env.Clock, PeopleTenantID: "people-1", OrreryTenantID: "tenant-1",
		AgentBindings: map[string]string{"subject-1": "agent-1"}, HumanRoleMappings: map[string]string{"DELIVERY": "approval.reviewer"}, ReconcileInterval: time.Minute,
	})
	if err != nil {
		t.Fatalf("new reconciler: %v", err)
	}
	stats, err := reconciler.Reconcile(context.Background())
	if err == nil || stats.GapsOpen != 1 || stats.RoleProfilesChanged != 1 || stats.PulsesEnsured != 0 {
		t.Fatalf("reconcile stats=%+v err=%v", stats, err)
	}
	if len(roleSync.inputs) != 1 || len(roleSync.inputs[0].DesiredRoleIDs) != 0 {
		t.Fatalf("unassigned role sync inputs = %+v", roleSync.inputs)
	}
	gap, found, getErr := repository.GetPeopleResponsibilityGap(context.Background(), PeopleResponsibilityGapID(PeopleAgentBindingID("tenant-1", "people-1", "subject-1")))
	if getErr != nil || !found || gap.Code != "responsibility_unassigned" {
		t.Fatalf("gap=%+v found=%v err=%v", gap, found, getErr)
	}
}

func TestPeopleResponsibilityReconcilerOpensGapWhenHumanRoleSyncFails(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	roleSync := &capturingPeopleResponsibilityRoleSync{err: errors.New("profile update unavailable")}
	reconciler, err := NewPeopleResponsibilityReconciler(PeopleResponsibilityReconcilerOptions{
		Repository: repository,
		Source: &staticPeopleResponsibilitySource{resolution: PeopleResponsibilityResolution{
			SubjectID: "subject-1", SourceRevision: "5:2", AsOf: env.Clock.Now().UTC(),
			Items: []PeopleResolvedResponsibility{{ID: "responsibility-1", Code: "DELIVERY"}},
		}},
		Profiles: staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"agent-1": {ID: "agent-1", AgentKind: agentcore.AgentKindHuman, Enabled: true},
		}},
		RoleSync: roleSync, Clock: env.Clock, PeopleTenantID: "people-1", OrreryTenantID: "tenant-1",
		AgentBindings: map[string]string{"subject-1": "agent-1"}, HumanRoleMappings: map[string]string{"DELIVERY": "approval.reviewer"}, ReconcileInterval: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	stats, err := reconciler.Reconcile(context.Background())
	if err == nil || stats.Failed != 1 || stats.GapsOpen != 1 || stats.PulsesEnsured != 0 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
	gap, found, getErr := repository.GetPeopleResponsibilityGap(context.Background(), PeopleResponsibilityGapID(PeopleAgentBindingID("tenant-1", "people-1", "subject-1")))
	if getErr != nil || !found || gap.Code != "responsibility_role_sync_failed" {
		t.Fatalf("gap=%+v found=%v err=%v", gap, found, getErr)
	}
}

func TestPeopleResponsibilityReconcilerDetachesPersonalAssistantBinding(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	now := env.Clock.Now().UTC()
	portfolio := seedPeoplePortfolio(t, repository, now, "tenant-1", "people-1", "subject-assistant", "agent-assistant", "digest-assistant")
	portfolio.PulseScheduledTaskID = "scheduled-task-assistant"
	if _, _, err := repository.SavePeopleResponsibilityPortfolio(context.Background(), portfolio, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	roleSync := &capturingPeopleResponsibilityRoleSync{changed: true}
	reconciler, err := NewPeopleResponsibilityReconciler(PeopleResponsibilityReconcilerOptions{
		Repository: repository,
		Source:     &staticPeopleResponsibilitySource{},
		Profiles: staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"agent-assistant": {
				ID: "agent-assistant", Enabled: true, PersonalAssistantOwnerID: "owner-1", PersonalAssistantTenantID: "tenant-1",
			},
		}},
		RoleSync: roleSync, Clock: env.Clock, PeopleTenantID: "people-1", OrreryTenantID: "tenant-1",
		AgentBindings: map[string]string{"subject-assistant": "agent-assistant"}, HumanRoleMappings: map[string]string{"DELIVERY": "approval.reviewer"}, ReconcileInterval: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	stats, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.PulsesEnsured != 0 || stats.PulsesDisabled != 0 {
		t.Fatalf("reconcile stats=%+v", stats)
	}
	if stats.RoleProfilesChanged != 1 || len(roleSync.inputs) != 1 || len(roleSync.inputs[0].DesiredRoleIDs) != 0 {
		t.Fatalf("detach role sync stats=%+v inputs=%+v", stats, roleSync.inputs)
	}
	bindings, err := repository.ListPeopleAgentBindings(context.Background())
	if err != nil || len(bindings) != 1 || bindings[0].Status != peopleBindingStatusDetached {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	stored, found, err := repository.GetPeopleResponsibilityPortfolio(context.Background(), portfolio.ID)
	if err != nil || !found || stored.Status != peoplePortfolioStatusPaused {
		t.Fatalf("portfolio=%+v found=%v err=%v", stored, found, err)
	}
}

func TestPeopleResponsibilityReconcilerRequiresRoleSyncForConfiguredMappings(t *testing.T) {
	_, err := NewPeopleResponsibilityReconciler(PeopleResponsibilityReconcilerOptions{
		Repository: &Repository{}, Source: &staticPeopleResponsibilitySource{}, Profiles: staticResponsibilityProfiles{},
		PeopleTenantID: "people-1", OrreryTenantID: "tenant-1", AgentBindings: map[string]string{"subject-1": "agent-1"},
		HumanRoleMappings: map[string]string{"DELIVERY": "approval.reviewer"}, ReconcileInterval: time.Minute,
	})
	if !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("error = %v, want ErrInvalidContract", err)
	}
}

func TestPeopleResponsibilitiesDigestMatchesProductionCanonicalPayload(t *testing.T) {
	validFrom := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	resolution := PeopleResponsibilityResolution{
		SubjectID: "subject-1", SourceRevision: "5:2",
		Items: []PeopleResolvedResponsibility{{ID: "r1", Code: "B", Title: "Title", ValidFrom: validFrom, Revision: 1, Sources: []PeopleResponsibilityBinding{}}},
	}
	digest, err := peopleResponsibilitiesDigest(resolution)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	if digest != "f2d0e939a625fa8d3d44673e470b99656c372d17eed30b4ca4b4006b9c23333d" {
		t.Fatalf("digest = %s", digest)
	}
}

type staticPeopleResponsibilitySource struct {
	resolution PeopleResponsibilityResolution
	err        error
}

func (s *staticPeopleResponsibilitySource) ResolveResponsibilities(_ context.Context, _ string) (PeopleResponsibilityResolution, error) {
	return s.resolution, s.err
}

type capturingPeopleResponsibilityRoleSync struct {
	inputs  []PeopleResponsibilityRoleSyncInput
	changed bool
	err     error
}

func (s *capturingPeopleResponsibilityRoleSync) SyncPeopleResponsibilityRoles(_ context.Context, input PeopleResponsibilityRoleSyncInput) (bool, error) {
	s.inputs = append(s.inputs, input)
	return s.changed, s.err
}
