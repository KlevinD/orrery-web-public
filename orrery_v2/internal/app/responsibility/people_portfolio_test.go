package responsibility

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/platform/testkit"
)

func TestPeopleResponsibilityIDsMatchProductionContract(t *testing.T) {
	bindingID := PeopleAgentBindingID("system", "tenant-preview", "member-openclaw-main")
	if bindingID != "people-agent-binding-b0c29373998e45dca2a64c89" {
		t.Fatalf("binding id = %q", bindingID)
	}
	if got := PeopleAgentBindingID(" system ", " tenant-preview ", " member-openclaw-main "); got != bindingID {
		t.Fatalf("trimmed binding id = %q", got)
	}
	if got := PeopleResponsibilityPortfolioID(bindingID); got != "people-responsibility-portfolio-"+stableHex(bindingID)[:24] {
		t.Fatalf("portfolio id = %q", got)
	}
	if got := PeopleResponsibilityPortfolioID(" " + bindingID + " "); got != PeopleResponsibilityPortfolioID(bindingID) {
		t.Fatalf("trimmed portfolio id = %q", got)
	}
	if got := PeopleResponsibilityPortfolioVersionID(bindingID, "digest-1"); got != "people-responsibility-portfolio-version-"+stableHex(bindingID, "digest-1")[:24] {
		t.Fatalf("version id = %q", got)
	}
}

func TestPeopleResponsibilityRepositoryPersistsProductionRecords(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	ctx := context.Background()
	now := env.Clock.Now().UTC()

	binding := PeopleAgentBindingRecord{
		ID: PeopleAgentBindingID("system", "tenant-preview", "subject-1"), TenantID: "system",
		PeopleTenantID: "tenant-preview", SubjectID: "subject-1", AgentProfileID: "agent-1", Source: "people", Status: "active",
	}
	storedBinding, changed, err := repository.UpsertPeopleAgentBinding(ctx, binding, now)
	if err != nil || !changed || storedBinding.Version != 1 {
		t.Fatalf("upsert binding = %+v changed=%v err=%v", storedBinding, changed, err)
	}
	storedBinding, changed, err = repository.UpsertPeopleAgentBinding(ctx, binding, now.Add(time.Minute))
	if err != nil || changed || storedBinding.Version != 1 {
		t.Fatalf("idempotent upsert = %+v changed=%v err=%v", storedBinding, changed, err)
	}

	portfolio := PeopleResponsibilityPortfolio{
		ID: PeopleResponsibilityPortfolioID(binding.ID), TenantID: "system", BindingID: binding.ID,
		PeopleTenantID: "tenant-preview", SubjectID: "subject-1", AgentProfileID: "agent-1",
		SourceRevision: "5:2", SourceDigest: "digest-1", Status: "active", SourceAsOf: now,
		Responsibilities: []PeopleResolvedResponsibility{{ID: "responsibility-1", Code: "DELIVERY", Title: "Deliver", ValidFrom: now, Revision: 1}},
	}
	storedPortfolio, changed, err := repository.SavePeopleResponsibilityPortfolio(ctx, portfolio, now)
	if err != nil || !changed || storedPortfolio.Version != 1 {
		t.Fatalf("save portfolio = %+v changed=%v err=%v", storedPortfolio, changed, err)
	}
	versions, err := repository.ListPeopleResponsibilityPortfolioVersions(ctx, portfolio.ID, 10)
	if err != nil || len(versions) != 1 || versions[0].ID != PeopleResponsibilityPortfolioVersionID(binding.ID, "digest-1") {
		t.Fatalf("versions = %+v err=%v", versions, err)
	}
	version, found, err := repository.GetPeopleResponsibilityPortfolioVersion(ctx, versions[0].ID)
	if err != nil || !found || version.ID != versions[0].ID || version.SourceDigest != portfolio.SourceDigest || version.SourceRevision != portfolio.SourceRevision {
		t.Fatalf("version = %+v found=%v err=%v", version, found, err)
	}
	storedPortfolio, changed, err = repository.SavePeopleResponsibilityPortfolio(ctx, portfolio, now.Add(time.Minute))
	if err != nil || changed || storedPortfolio.Version != 1 {
		t.Fatalf("idempotent save = %+v changed=%v err=%v", storedPortfolio, changed, err)
	}
	audits, err := repository.ListAudits(ctx, "system", binding.ID, 10)
	if err != nil || len(audits) != 3 {
		t.Fatalf("People responsibility audits = %+v err=%v", audits, err)
	}
}

func TestPeopleResponsibilityRepositoryClaimsReconcileAndResolvesGap(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	ctx := context.Background()
	now := env.Clock.Now().UTC()

	claimed, err := repository.ClaimPeopleResponsibilityReconcile(ctx, "system", "tenant-preview", now, 15*time.Minute)
	if err != nil || !claimed {
		t.Fatalf("first claim = %v err=%v", claimed, err)
	}
	claimed, err = repository.ClaimPeopleResponsibilityReconcile(ctx, "system", "tenant-preview", now.Add(time.Minute), 15*time.Minute)
	if err != nil || claimed {
		t.Fatalf("throttled claim = %v err=%v", claimed, err)
	}
	if err := repository.CompletePeopleResponsibilityReconcile(ctx, "system", "tenant-preview", now.Add(2*time.Minute), nil); err != nil {
		t.Fatalf("complete reconcile: %v", err)
	}

	gap := PeopleResponsibilityGap{
		ID: PeopleResponsibilityGapID("binding-1"), TenantID: "system", BindingID: "binding-1",
		SubjectID: "subject-1", AgentProfileID: "agent-1", Code: "people_source_unavailable", Message: "temporarily unavailable",
	}
	changed, err := repository.OpenPeopleResponsibilityGap(ctx, gap, now)
	if err != nil || !changed {
		t.Fatalf("open gap changed=%v err=%v", changed, err)
	}
	storedGap, found, err := repository.GetPeopleResponsibilityGap(ctx, gap.ID)
	if err != nil || !found || !storedGap.Open {
		t.Fatalf("get gap = %+v found=%v err=%v", storedGap, found, err)
	}
	changed, err = repository.ResolvePeopleResponsibilityGap(ctx, gap.ID, now.Add(time.Minute))
	if err != nil || !changed {
		t.Fatalf("resolve gap changed=%v err=%v", changed, err)
	}
}
