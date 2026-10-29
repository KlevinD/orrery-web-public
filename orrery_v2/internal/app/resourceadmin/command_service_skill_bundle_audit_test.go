package resourceadmin

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

func TestCommandServiceRemovesCreatedBundleWhenCreateAuditFails(t *testing.T) {
	ctx := context.Background()
	skills := skillres.NewMemoryRegistry()
	bundles := skillres.NewMemoryBundleRegistry()
	command := NewCommandService(toolres.NewMemoryRegistry(), skills)
	command.SetSkillBundleRegistry(bundles)
	command.SetIDGenerator(&sequenceIDGenerator{values: []string{"skill-bundle-quality"}})
	audit := &failingActionAuditRecorder{
		failAction: "resource.skill_bundle.create",
		failErr:    errors.New("injected audit failure"),
	}
	command.SetAuditRecorder(audit)
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}

	_, err := command.CreateSkillBundle(ctx, viewer, CreateSkillBundleRequest{Name: "Quality"})
	if !errors.Is(err, audit.failErr) {
		t.Fatalf("CreateSkillBundle error = %v, want injected failure", err)
	}
	if _, err := bundles.Get(ctx, "skill-bundle-quality"); !errors.Is(err, skillres.ErrNotFound) {
		t.Fatalf("failed create retained bundle: %v", err)
	}
}

func TestCommandServiceRestoresBundleWhenAddAuditFails(t *testing.T) {
	ctx := context.Background()
	skills := skillres.NewMemoryRegistry()
	registerBundleTestSkill(t, skills, "skill.review", "Review")
	bundles := skillres.NewMemoryBundleRegistry()
	command := NewCommandService(toolres.NewMemoryRegistry(), skills)
	command.SetSkillBundleRegistry(bundles)
	command.SetIDGenerator(&sequenceIDGenerator{values: []string{"skill-bundle-quality"}})
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}
	created, err := command.CreateSkillBundle(ctx, viewer, CreateSkillBundleRequest{Name: "Quality"})
	if err != nil {
		t.Fatalf("CreateSkillBundle: %v", err)
	}
	audit := &failingActionAuditRecorder{
		failAction: "resource.skill_bundle.skills.add",
		failErr:    errors.New("injected audit failure"),
	}
	command.SetAuditRecorder(audit)

	_, err = command.AddSkillsToBundle(ctx, viewer, created.BundleID, AddSkillsToBundleRequest{SkillIDs: []string{"skill.review"}})
	if !errors.Is(err, audit.failErr) {
		t.Fatalf("AddSkillsToBundle error = %v, want injected failure", err)
	}
	bundle, err := bundles.Get(ctx, created.BundleID)
	if err != nil {
		t.Fatalf("Get bundle: %v", err)
	}
	if len(bundle.SkillIDs) != 0 {
		t.Fatalf("failed add retained members: %+v", bundle.SkillIDs)
	}
}

func TestCommandServiceRestoresBundleWhenRemoveAuditFails(t *testing.T) {
	ctx := context.Background()
	skills := skillres.NewMemoryRegistry()
	registerBundleTestSkill(t, skills, "skill.review", "Review")
	bundles := skillres.NewMemoryBundleRegistry()
	command := NewCommandService(toolres.NewMemoryRegistry(), skills)
	command.SetSkillBundleRegistry(bundles)
	command.SetIDGenerator(&sequenceIDGenerator{values: []string{"skill-bundle-quality"}})
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}
	created, err := command.CreateSkillBundle(ctx, viewer, CreateSkillBundleRequest{
		Name:     "Quality",
		SkillIDs: []string{"skill.review"},
	})
	if err != nil {
		t.Fatalf("CreateSkillBundle: %v", err)
	}
	audit := &failingActionAuditRecorder{
		failAction: "resource.skill_bundle.skills.remove",
		failErr:    errors.New("injected audit failure"),
	}
	command.SetAuditRecorder(audit)

	_, err = command.RemoveSkillFromBundle(ctx, viewer, created.BundleID, "skill.review")
	if !errors.Is(err, audit.failErr) {
		t.Fatalf("RemoveSkillFromBundle error = %v, want injected failure", err)
	}
	bundle, err := bundles.Get(ctx, created.BundleID)
	if err != nil {
		t.Fatalf("Get bundle: %v", err)
	}
	if len(bundle.SkillIDs) != 1 || bundle.SkillIDs[0] != "skill.review" {
		t.Fatalf("failed remove changed members: %+v", bundle.SkillIDs)
	}
}

func TestCommandServiceRollsBackDirectSkillDeleteWhenAuditFails(t *testing.T) {
	ctx := context.Background()
	skills := skillres.NewMemoryRegistry()
	bundles := skillres.NewMemoryBundleRegistry()
	store := skillres.NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	command := NewCommandService(toolres.NewMemoryRegistry(), skills)
	command.SetSkillBundleRegistry(bundles)
	command.SetSkillPackageStore(store)
	command.SetAgentProfileBindingStore(&fakeAgentProfileBindingStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}})
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}
	pkg := buildSkillPackage(t, "skill.review", "Review", "# Review\n")
	if _, err := command.ImportSkillPackage(ctx, viewer, bytes.NewReader(pkg), int64(len(pkg))); err != nil {
		t.Fatalf("ImportSkillPackage: %v", err)
	}
	audit := &failingActionAuditRecorder{
		failAction: "resource.skill.delete",
		failErr:    errors.New("injected audit failure"),
	}
	command.SetAuditRecorder(audit)

	err := command.DeleteSkill(ctx, viewer, "skill.review")
	if !errors.Is(err, audit.failErr) {
		t.Fatalf("DeleteSkill error = %v, want injected failure", err)
	}
	if _, err := skills.Get(ctx, "skill.review"); err != nil {
		t.Fatalf("failed delete removed registry definition: %v", err)
	}
	if _, err := store.BuildFileTree(ctx, "skill.review"); err != nil {
		t.Fatalf("failed delete removed package files: %v", err)
	}
}
