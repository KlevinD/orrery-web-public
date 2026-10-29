package resourceadmin

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

func TestQueryServiceKeepsBundleVisibleWhenMemberSkillIsMissing(t *testing.T) {
	ctx := context.Background()
	skills := skillres.NewMemoryRegistry()
	bundles := skillres.NewMemoryBundleRegistry()
	if err := bundles.Create(ctx, skillres.SkillBundle{
		ID:       "skill-bundle-unresolved",
		Name:     "Unresolved",
		SkillIDs: []agentcore.SkillID{"skill.missing"},
	}); err != nil {
		t.Fatalf("Create bundle: %v", err)
	}
	query := NewQueryService(toolres.NewMemoryRegistry(), skills)
	query.SetSkillBundleRegistry(bundles)
	viewer := ViewerRef{ViewerID: "reader", PermissionScope: []string{PermissionResourceRead}}

	page, err := query.ListSkillBundles(ctx, viewer)
	if err != nil {
		t.Fatalf("ListSkillBundles: %v", err)
	}
	if len(page.Bundles) != 1 {
		t.Fatalf("bundle count = %d, want 1", len(page.Bundles))
	}
	if got := page.Bundles[0].SkillIDs; len(got) != 1 || got[0] != "skill.missing" {
		t.Fatalf("unresolved member ids = %+v", got)
	}
	if len(page.Bundles[0].Skills) != 0 {
		t.Fatalf("hydrated missing skills = %+v, want none", page.Bundles[0].Skills)
	}
}
