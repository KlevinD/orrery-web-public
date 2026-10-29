package main

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/platform/testkit"
	toolres "orrery_v2/internal/resource/tool"
)

func TestProductionOperationsReadsDoNotCreateViewerProfile(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	hook := &countingProfileSaveHook{}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork).WithProfileSaveHook(hook)
	query := operations.NewQueryService(profiles, toolres.NewMemoryRegistry())
	api := productionAPI{
		operations:    operations.NewService(query, nil),
		humanProfiles: newProductionHumanProfileBootstrap(profiles),
	}
	viewer := operations.ViewerRef{
		ViewerID: "orrery-browser-e2e-1786379643169", ViewerKind: "user", DisplayName: "Browser E2E",
		PermissionScope: []string{operations.PermissionWorkforceRead, operations.PermissionOrgRead},
	}

	if _, err := api.ListWorkforceMembers(ctx, viewer, operations.WorkforceQuery{}); err != nil {
		t.Fatalf("ListWorkforceMembers: %v", err)
	}
	if _, err := api.GetOrgGraph(ctx, viewer, operations.OrgGraphQuery{}); err != nil {
		t.Fatalf("GetOrgGraph: %v", err)
	}
	if hook.calls != 0 {
		t.Fatalf("read endpoints wrote viewer profile %d times", hook.calls)
	}
	if _, err := profiles.GetProfile(ctx, humanAgentProfileID(viewer.ViewerID)); err == nil {
		t.Fatal("read endpoints created a derived viewer profile")
	}
}
