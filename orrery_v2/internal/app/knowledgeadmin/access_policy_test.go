package knowledgeadmin_test

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/testkit"
	doccore "orrery_v2/internal/platform/document/core"
)

func TestAccessPolicyAllowsPrivateOwnerAndExplicitBinding(t *testing.T) {
	repo := testkit.NewFakeRepository()
	base := accessPolicyBase("kb-private", core.OwnerAgent, "agent-owner", core.VisibilityPrivate)
	saveAccessBase(t, repo, base)
	saveAccessBinding(t, repo, "bind-reader", base.ID, core.BindingSubjectHuman, "human-reader", core.AccessRead)
	policy := knowledgeadmin.NewAccessPolicy(repo)

	if !policy.CanRead(context.Background(), knowledgeadmin.AccessSubject{AgentIDs: []string{"agent-owner"}}, base) {
		t.Fatal("owner agent should read private knowledge base")
	}
	if !policy.CanRead(context.Background(), knowledgeadmin.AccessSubject{HumanIDs: []string{"human-reader"}}, base) {
		t.Fatal("explicit read binding should read private knowledge base")
	}
	if policy.CanRead(context.Background(), knowledgeadmin.AccessSubject{HumanIDs: []string{"human-other"}}, base) {
		t.Fatal("unbound human should not read private knowledge base")
	}
}

func TestAccessPolicyCanReadBasesMatchesIndividualDecisions(t *testing.T) {
	repo := testkit.NewFakeRepository()
	bases := []core.KnowledgeBase{
		accessPolicyBase("kb-owner", core.OwnerAgent, "agent-owner", core.VisibilityPrivate),
		accessPolicyBase("kb-bound", core.OwnerOrganization, "org-bound", core.VisibilityPrivate),
		accessPolicyBase("kb-public", core.OwnerOrganization, "org-public", core.VisibilityPublic),
		accessPolicyBase("kb-scoped", core.OwnerOrganization, "org-scoped", core.VisibilityScoped),
		accessPolicyBase("kb-disabled", core.OwnerOrganization, "org-scoped", core.VisibilityScoped),
	}
	bases[4].Status = core.KnowledgeDisabled
	for _, base := range bases {
		saveAccessBase(t, repo, base)
	}
	saveAccessBinding(t, repo, "bind-batch-reader", "kb-bound", core.BindingSubjectHuman, "human-reader", core.AccessRead)
	policy := knowledgeadmin.NewAccessPolicy(repo)
	subject := knowledgeadmin.AccessSubject{
		ViewerID: "human-reader", AgentIDs: []string{"agent-owner"}, HumanIDs: []string{"human-reader"}, OrganizationIDs: []string{"org-scoped"},
	}

	batch := policy.CanReadBases(context.Background(), subject, bases)
	for _, base := range bases {
		if got, want := batch[base.ID], policy.CanRead(context.Background(), subject, base); got != want {
			t.Fatalf("CanReadBases(%s) = %v, want %v", base.ID, got, want)
		}
	}
}

func TestAccessPolicyDoesNotInferTypedOwnerFromViewerID(t *testing.T) {
	repo := testkit.NewFakeRepository()
	agentBase := accessPolicyBase("kb-agent", core.OwnerAgent, "shared-name", core.VisibilityPrivate)
	humanBase := accessPolicyBase("kb-human", core.OwnerHuman, "shared-name", core.VisibilityPrivate)
	saveAccessBase(t, repo, agentBase)
	saveAccessBase(t, repo, humanBase)
	policy := knowledgeadmin.NewAccessPolicy(repo)
	subject := (knowledgeadmin.ViewerRef{ViewerID: "shared-name"}).AccessSubject()

	if policy.CanRead(context.Background(), subject, agentBase) {
		t.Fatal("untyped viewer ID must not grant agent ownership")
	}
	if policy.CanRead(context.Background(), subject, humanBase) {
		t.Fatal("untyped viewer ID must not grant human ownership")
	}
}

func TestAccessPolicyAllowsScopedOrganizationMember(t *testing.T) {
	repo := testkit.NewFakeRepository()
	base := accessPolicyBase("kb-scoped", core.OwnerOrganization, "org-1", core.VisibilityScoped)
	saveAccessBase(t, repo, base)
	policy := knowledgeadmin.NewAccessPolicy(repo)

	if !policy.CanRead(context.Background(), knowledgeadmin.AccessSubject{OrganizationIDs: []string{"org-1"}}, base) {
		t.Fatal("organization member should read scoped organization knowledge base")
	}
	if policy.CanRead(context.Background(), knowledgeadmin.AccessSubject{OrganizationIDs: []string{"org-2"}}, base) {
		t.Fatal("other organization member should not read scoped organization knowledge base")
	}
}

func TestAccessPolicyAllowsPublicReadOnly(t *testing.T) {
	repo := testkit.NewFakeRepository()
	base := accessPolicyBase("kb-public", core.OwnerOrganization, "org-1", core.VisibilityPublic)
	saveAccessBase(t, repo, base)
	policy := knowledgeadmin.NewAccessPolicy(repo)
	subject := knowledgeadmin.AccessSubject{ViewerID: "reader"}

	if !policy.CanRead(context.Background(), subject, base) {
		t.Fatal("authenticated viewer should read public knowledge base")
	}
	if policy.CanWrite(context.Background(), subject, base) || policy.CanManage(context.Background(), subject, base) {
		t.Fatal("public visibility must not grant write or manage")
	}
}

func TestAccessPolicyTreatsGlobalAsPublicWritableButNotGloballyManageable(t *testing.T) {
	repo := testkit.NewFakeRepository()
	base := accessPolicyBase("kb-global", core.OwnerGlobal, "", core.VisibilityPublic)
	saveAccessBase(t, repo, base)
	saveAccessBinding(t, repo, "bind-manager", base.ID, core.BindingSubjectHuman, "human-manager", core.AccessManage)
	policy := knowledgeadmin.NewAccessPolicy(repo)

	reader := knowledgeadmin.AccessSubject{ViewerID: "reader"}
	writer := knowledgeadmin.AccessSubject{ViewerID: "writer"}
	manager := knowledgeadmin.AccessSubject{ViewerID: "human-manager", HumanIDs: []string{"human-manager"}}
	superadmin := knowledgeadmin.AccessSubject{ViewerID: "human-superadmin", RoleIDs: []string{"superadmin"}}

	if !policy.CanRead(context.Background(), reader, base) {
		t.Fatal("authenticated viewer should read global public knowledge base")
	}
	if !policy.CanWrite(context.Background(), writer, base) {
		t.Fatal("authenticated writer should write global public knowledge base when route scope allows write")
	}
	if policy.CanManage(context.Background(), writer, base) {
		t.Fatal("global public knowledge base must not be manageable by every authenticated viewer")
	}
	if !policy.CanManage(context.Background(), manager, base) {
		t.Fatal("explicit manage binding should manage global public knowledge base")
	}
	if !policy.CanManage(context.Background(), superadmin, base) {
		t.Fatal("superadmin role should manage global public knowledge base")
	}
}

func TestAccessPolicyMatchesPositionAndRoleBindings(t *testing.T) {
	repo := testkit.NewFakeRepository()
	base := accessPolicyBase("kb-position-role", core.OwnerOrganization, "org-1", core.VisibilityPrivate)
	saveAccessBase(t, repo, base)
	saveAccessBinding(t, repo, "bind-position", base.ID, core.BindingSubjectPosition, "position-pm", core.AccessRead)
	saveAccessBinding(t, repo, "bind-role", base.ID, core.BindingSubjectRole, "role-reviewer", core.AccessWrite)
	policy := knowledgeadmin.NewAccessPolicy(repo)

	if !policy.CanRead(context.Background(), knowledgeadmin.AccessSubject{PositionIDs: []string{"position-pm"}}, base) {
		t.Fatal("position binding should grant read")
	}
	if !policy.CanWrite(context.Background(), knowledgeadmin.AccessSubject{RoleIDs: []string{"role-reviewer"}}, base) {
		t.Fatal("role binding should grant write")
	}
	if policy.CanRead(context.Background(), knowledgeadmin.AccessSubject{PositionIDs: []string{"position-other"}, RoleIDs: []string{"role-other"}}, base) {
		t.Fatal("unmatched position and role should not grant access")
	}
}

func TestAccessPolicyAllowsPositionOwnerScopedKnowledge(t *testing.T) {
	repo := testkit.NewFakeRepository()
	base := accessPolicyBase("kb-position-owner", core.OwnerPosition, "position-pm", core.VisibilityScoped)
	saveAccessBase(t, repo, base)
	policy := knowledgeadmin.NewAccessPolicy(repo)

	owner := knowledgeadmin.AccessSubject{PositionIDs: []string{"position-pm"}}
	other := knowledgeadmin.AccessSubject{PositionIDs: []string{"position-other"}}

	if !policy.CanRead(context.Background(), owner, base) {
		t.Fatal("position owner should read scoped position knowledge base")
	}
	if !policy.CanWrite(context.Background(), owner, base) {
		t.Fatal("position owner should write scoped position knowledge base")
	}
	if !policy.CanManage(context.Background(), owner, base) {
		t.Fatal("position owner should manage scoped position knowledge base")
	}
	if policy.CanRead(context.Background(), other, base) {
		t.Fatal("other position should not read scoped position knowledge base")
	}
}

func TestAccessPolicyRequiresBindingForNonOwnerWriteAndManage(t *testing.T) {
	repo := testkit.NewFakeRepository()
	base := accessPolicyBase("kb-write", core.OwnerOrganization, "org-1", core.VisibilityScoped)
	saveAccessBase(t, repo, base)
	saveAccessBinding(t, repo, "bind-read", base.ID, core.BindingSubjectAgent, "agent-read", core.AccessRead)
	saveAccessBinding(t, repo, "bind-write", base.ID, core.BindingSubjectAgent, "agent-write", core.AccessWrite)
	saveAccessBinding(t, repo, "bind-manage", base.ID, core.BindingSubjectAgent, "agent-manage", core.AccessManage)
	policy := knowledgeadmin.NewAccessPolicy(repo)

	if policy.CanWrite(context.Background(), knowledgeadmin.AccessSubject{AgentIDs: []string{"agent-read"}}, base) {
		t.Fatal("read binding should not write")
	}
	if !policy.CanWrite(context.Background(), knowledgeadmin.AccessSubject{AgentIDs: []string{"agent-write"}}, base) {
		t.Fatal("write binding should write")
	}
	if policy.CanManage(context.Background(), knowledgeadmin.AccessSubject{AgentIDs: []string{"agent-write"}}, base) {
		t.Fatal("write binding should not manage")
	}
	if !policy.CanManage(context.Background(), knowledgeadmin.AccessSubject{AgentIDs: []string{"agent-manage"}}, base) {
		t.Fatal("manage binding should manage")
	}
	if !policy.CanWrite(context.Background(), knowledgeadmin.AccessSubject{AgentIDs: []string{"agent-manage"}}, base) {
		t.Fatal("manage binding should imply write")
	}
}

func accessPolicyBase(id core.KnowledgeBaseID, ownerType core.OwnerType, ownerID string, visibility core.Visibility) core.KnowledgeBase {
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	return core.KnowledgeBase{
		ID:            id,
		Name:          string(id),
		OwnerType:     ownerType,
		OwnerID:       ownerID,
		Visibility:    visibility,
		VectorBackend: core.VectorBackendLanceDB,
		ChunkPolicy:   doccore.ChunkPolicy{MaxRunes: 100, OverlapRunes: 10},
		Status:        core.KnowledgeActive,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

func saveAccessBase(t *testing.T, repo *testkit.FakeRepository, base core.KnowledgeBase) {
	t.Helper()
	if err := repo.SaveKnowledgeBase(context.Background(), base); err != nil {
		t.Fatalf("save base: %v", err)
	}
}

func saveAccessBinding(t *testing.T, repo *testkit.FakeRepository, id core.KnowledgeBindingID, baseID core.KnowledgeBaseID, subject core.BindingSubjectType, subjectID string, access core.AccessLevel) {
	t.Helper()
	if err := repo.SaveBinding(context.Background(), core.KnowledgeBinding{
		ID:              id,
		KnowledgeBaseID: baseID,
		BoundToType:     subject,
		BoundToID:       subjectID,
		Access:          access,
		CreatedBy:       "test",
		CreatedAt:       time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("save binding: %v", err)
	}
}
