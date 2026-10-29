package responsibility

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/testkit"
)

func TestOperationsServiceBuildsPeopleAgentAndOrganizationProfiles(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	now := env.Clock.Now().UTC()
	seedPeoplePortfolio(t, repository, now, "tenant-1", "people-1", "subject-1", "agent-1", "digest-1")
	profiles := staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"agent-1": {ID: "agent-1", DisplayName: "Agent One", OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef: agentcore.OrganizationRef{ID: "org-1"}, DepartmentRef: agentcore.DepartmentRef{ID: "dept-1"}, PositionRef: agentcore.PositionRef{ID: "pos-1"},
		}},
	}}
	service := NewOperationsService(OperationsServiceOptions{Repository: repository, Profiles: profiles, Clock: env.Clock})

	agentProfile, err := service.GetPeopleAgentResponsibilityProfile(context.Background(), "tenant-1", "agent-1", 3)
	if err != nil {
		t.Fatalf("agent profile: %v", err)
	}
	if agentProfile.ScopeType != "agent" || agentProfile.ScopeID != "agent-1" || agentProfile.Source != "people" || agentProfile.ResponsibilityCount != 1 || agentProfile.VersionCount != 1 || len(agentProfile.Portfolios) != 1 {
		t.Fatalf("agent profile = %+v", agentProfile)
	}
	portfolio := agentProfile.Portfolios[0]
	if portfolio.AgentDisplayName != "Agent One" || portfolio.OrreryOrganizationID != "org-1" || portfolio.CurrentVersionID == "" {
		t.Fatalf("portfolio = %+v", portfolio)
	}
	currentOnlyProfile, err := service.GetPeopleAgentResponsibilityProfile(context.Background(), "tenant-1", "agent-1", 0)
	if err != nil {
		t.Fatalf("current-only agent profile: %v", err)
	}
	if currentOnlyProfile.ResponsibilityCount != 1 || currentOnlyProfile.VersionCount != 0 || len(currentOnlyProfile.Portfolios) != 1 {
		t.Fatalf("current-only agent profile = %+v", currentOnlyProfile)
	}
	currentOnlyPortfolio := currentOnlyProfile.Portfolios[0]
	if len(currentOnlyPortfolio.Responsibilities) != 1 || len(currentOnlyPortfolio.Versions) != 0 || len(currentOnlyPortfolio.Audit) != 0 {
		t.Fatalf("current-only portfolio = %+v", currentOnlyPortfolio)
	}

	organizationProfile, err := service.GetPeopleOrganizationResponsibilityProfile(context.Background(), "tenant-1", "org-1", 3)
	if err != nil || len(organizationProfile.Portfolios) != 1 || organizationProfile.ScopeType != "organization" {
		t.Fatalf("organization profile = %+v err=%v", organizationProfile, err)
	}
	departmentProfile, err := service.GetPeopleOrganizationResponsibilityProfile(context.Background(), "tenant-1", "dept-1", 3)
	if err != nil || len(departmentProfile.Portfolios) != 1 || departmentProfile.ScopeID != "dept-1" {
		t.Fatalf("department profile = %+v err=%v", departmentProfile, err)
	}
	emptyProfile, err := service.GetPeopleOrganizationResponsibilityProfile(context.Background(), "tenant-1", "org-other", 3)
	if err != nil || len(emptyProfile.Portfolios) != 0 {
		t.Fatalf("empty organization profile = %+v err=%v", emptyProfile, err)
	}
}

func TestOperationsServiceBatchesOrganizationResponsibilityProfileReads(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	now := env.Clock.Now().UTC()
	seedPeoplePortfolio(t, repository, now, "tenant-1", "people-1", "subject-1", "agent-1", "digest-1")
	seedPeoplePortfolio(t, repository, now, "tenant-1", "people-1", "subject-2", "agent-2", "digest-2")
	getProfileCalls, getProfilesCalls := 0, 0
	profiles := staticResponsibilityProfiles{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"agent-1": {ID: "agent-1", DisplayName: "Agent One", OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "org-1"}}},
			"agent-2": {ID: "agent-2", DisplayName: "Agent Two", OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "org-1"}}},
		},
		getProfileCalls:  &getProfileCalls,
		getProfilesCalls: &getProfilesCalls,
	}
	service := NewOperationsService(OperationsServiceOptions{Repository: repository, Profiles: profiles, Clock: env.Clock})

	profile, err := service.GetPeopleOrganizationResponsibilityProfile(context.Background(), "tenant-1", "org-1", 3)
	if err != nil {
		t.Fatalf("organization profile: %v", err)
	}
	if len(profile.Portfolios) != 2 || profile.VersionCount != 2 {
		t.Fatalf("organization profile = %+v", profile)
	}
	for _, portfolio := range profile.Portfolios {
		if len(portfolio.Audit) != 3 {
			t.Fatalf("portfolio %s audits = %d, want 3", portfolio.BindingID, len(portfolio.Audit))
		}
	}
	if getProfilesCalls != 1 || getProfileCalls != 0 {
		t.Fatalf("profile reads: batch=%d single=%d, want one batch and no single reads", getProfilesCalls, getProfileCalls)
	}
}

func TestOperationsServiceReadsOrganizationBoundLegacySystemPortfolios(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	now := env.Clock.Now().UTC()
	seedPeoplePortfolio(t, repository, now, "system", "people-1", "subject-org-1", "agent-org-1", "digest-org-1")
	seedPeoplePortfolio(t, repository, now, "system", "people-1", "subject-org-2", "agent-org-2", "digest-org-2")
	profiles := staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"agent-org-1": {ID: "agent-org-1", DisplayName: "Org One", OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef: agentcore.OrganizationRef{ID: "org-1"}, DepartmentRef: agentcore.DepartmentRef{ID: "dept-1"},
		}},
		"agent-org-2": {ID: "agent-org-2", DisplayName: "Org Two", OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef: agentcore.OrganizationRef{ID: "org-2"}, DepartmentRef: agentcore.DepartmentRef{ID: "dept-2"},
		}},
	}}
	service := NewOperationsService(OperationsServiceOptions{Repository: repository, Profiles: profiles, Clock: env.Clock})

	organizationProfile, err := service.GetPeopleOrganizationResponsibilityProfile(context.Background(), "org-1", "org-1", 3)
	if err != nil || len(organizationProfile.Portfolios) != 1 || organizationProfile.Portfolios[0].AgentProfileID != "agent-org-1" {
		t.Fatalf("legacy organization profile = %+v err=%v", organizationProfile, err)
	}
	departmentProfile, err := service.GetPeopleOrganizationResponsibilityProfile(context.Background(), "org-1", "dept-1", 3)
	if err != nil || len(departmentProfile.Portfolios) != 1 || departmentProfile.Portfolios[0].AgentProfileID != "agent-org-1" {
		t.Fatalf("legacy department profile = %+v err=%v", departmentProfile, err)
	}
	agentProfile, err := service.GetPeopleAgentResponsibilityProfile(context.Background(), "org-1", "agent-org-1", 3)
	if err != nil || len(agentProfile.Portfolios) != 1 || agentProfile.Portfolios[0].AgentProfileID != "agent-org-1" {
		t.Fatalf("legacy agent profile = %+v err=%v", agentProfile, err)
	}
	crossTenantAgentProfile, err := service.GetPeopleAgentResponsibilityProfile(context.Background(), "org-2", "agent-org-1", 3)
	if err != nil || len(crossTenantAgentProfile.Portfolios) != 0 {
		t.Fatalf("cross-tenant legacy agent profile = %+v err=%v", crossTenantAgentProfile, err)
	}
}

func TestOperationsServicePrefersTenantPortfolioOverLegacyDuplicate(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	now := env.Clock.Now().UTC()
	seedPeoplePortfolio(t, repository, now, "org-1", "people-1", "subject-1", "agent-1", "digest-current")
	seedPeoplePortfolio(t, repository, now, "system", "people-1", "subject-1", "agent-1", "digest-legacy")
	profiles := staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"agent-1": {ID: "agent-1", DisplayName: "Agent One", OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef: agentcore.OrganizationRef{ID: "org-1"}, DepartmentRef: agentcore.DepartmentRef{ID: "dept-1"},
		}},
	}}
	service := NewOperationsService(OperationsServiceOptions{Repository: repository, Profiles: profiles, Clock: env.Clock})

	profile, err := service.GetPeopleOrganizationResponsibilityProfile(context.Background(), "org-1", "org-1", 3)
	if err != nil || len(profile.Portfolios) != 1 || profile.Portfolios[0].CurrentSourceDigest != "digest-current" {
		t.Fatalf("deduplicated profile = %+v err=%v", profile, err)
	}
}

func TestOperationsServiceExcludesPersonalAssistantsFromResponsibilityProfiles(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	now := env.Clock.Now().UTC()
	seedPeoplePortfolio(t, repository, now, "tenant-1", "people-1", "subject-workforce", "agent-workforce", "digest-workforce")
	seedPeoplePortfolio(t, repository, now, "tenant-1", "people-1", "subject-assistant", "agent-assistant", "digest-assistant")
	profiles := staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"agent-workforce": {
			ID: "agent-workforce", DisplayName: "Workforce Agent",
			OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "org-1"}},
		},
		"agent-assistant": {
			ID: "agent-assistant", DisplayName: "Private Assistant", PersonalAssistantOwnerID: "owner-1", PersonalAssistantTenantID: "tenant-1",
			OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "org-1"}},
		},
	}}
	service := NewOperationsService(OperationsServiceOptions{Repository: repository, Profiles: profiles, Clock: env.Clock})

	organizationProfile, err := service.GetPeopleOrganizationResponsibilityProfile(context.Background(), "tenant-1", "org-1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(organizationProfile.Portfolios) != 1 || organizationProfile.Portfolios[0].AgentProfileID != "agent-workforce" || organizationProfile.ResponsibilityCount != 1 {
		t.Fatalf("organization profile = %+v", organizationProfile)
	}
	assistantProfile, err := service.GetPeopleAgentResponsibilityProfile(context.Background(), "tenant-1", "agent-assistant", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(assistantProfile.Portfolios) != 0 || assistantProfile.ResponsibilityCount != 0 || assistantProfile.VersionCount != 0 {
		t.Fatalf("personal assistant responsibility profile = %+v", assistantProfile)
	}
}

func seedPeoplePortfolio(t *testing.T, repository *Repository, now time.Time, tenantID, peopleTenantID, subjectID, agentID, digest string) PeopleResponsibilityPortfolio {
	t.Helper()
	bindingID := PeopleAgentBindingID(tenantID, peopleTenantID, subjectID)
	if _, _, err := repository.UpsertPeopleAgentBinding(context.Background(), PeopleAgentBindingRecord{
		ID: bindingID, TenantID: tenantID, PeopleTenantID: peopleTenantID, SubjectID: subjectID, AgentProfileID: agentID,
	}, now); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	portfolio, _, err := repository.SavePeopleResponsibilityPortfolio(context.Background(), PeopleResponsibilityPortfolio{
		ID: PeopleResponsibilityPortfolioID(bindingID), TenantID: tenantID, BindingID: bindingID, PeopleTenantID: peopleTenantID,
		SubjectID: subjectID, AgentProfileID: agentID, SourceRevision: "5:2", SourceDigest: digest, SourceAsOf: now,
		Responsibilities: []PeopleResolvedResponsibility{{ID: "responsibility-1", Code: "DELIVERY", Title: "Deliver", ValidFrom: now, Revision: 1}},
	}, now)
	if err != nil {
		t.Fatalf("seed portfolio: %v", err)
	}
	return portfolio
}
