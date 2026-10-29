package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/ontologyscope"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/projectadmin"
	missioncore "orrery_v2/internal/mission/core"
	missionprojection "orrery_v2/internal/mission/projection"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/testkit"
	toolres "orrery_v2/internal/resource/tool"
)

func TestProductionOntologySourceDeletionDisablesCanonicalAndCustomBindings(t *testing.T) {
	ctx := context.Background()
	runtime := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := runtime.Databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	missions := missioncontrol.NewDBProjectionRepository(runtime.DBRouter)
	missionID := missioncore.MissionID("mission-delete-binding")
	now := time.Unix(100, 0).UTC()
	if err := missions.Save(ctx, missionprojection.ReadModel{
		Mission:    missionprojection.MissionView{MissionID: missionID, Title: "Delete binding", FinalGoal: "Verify binding lifecycle", Status: missionprojection.MissionStatusActive, CreatedByID: "owner", CreatedByKind: "user", CreatedAt: now},
		Checkpoint: missionprojection.Checkpoint{MissionID: missionID, Version: 1, UpdatedAt: now}, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("save mission: %v", err)
	}
	organizations := operations.NewOrganizationCatalogStore(runtime.DBRouter)
	authorizer := authorization.NewService(fixedProductionAuthorizationAdapter{decision: authorization.Decision{Allowed: true, Source: authorization.DecisionSourceOpenFGA}})
	state, err := newProductionOntologyScopeState(
		core, organizations, projectadmin.NewMemoryRepository(), missions, toolres.NewMemoryRegistry(), authorizer,
		authorization.NewSnapshotService(nil, nil), &fixedProductionRoleAuthorizer{allowed: true},
	)
	if err != nil {
		t.Fatalf("new scope state: %v", err)
	}
	custom := ontologyscope.ScopeBinding{
		TenantID: "historical-mission-delete-binding", Kind: ontologyscope.KindCustom,
		SourceObject: identity.ObjectRef{Type: "mission", ID: missionID.String()}, AuthorizationTenantID: "global",
		ReadAction: "mission.view", ManageAction: "mission.manage", BindingVersion: 1, State: ontologyscope.BindingStateActive,
	}
	if _, err := state.Store.SaveBinding(ctx, custom, 0, "test:seed"); err != nil {
		t.Fatalf("save custom binding: %v", err)
	}

	canonicalTenantID := missionScopeTenantID("global", missionID.String())
	rollback, err := prepareOntologySourceDeletion(ctx, state, identity.ObjectRef{Type: "mission", ID: missionID.String()}, []string{canonicalTenantID}, "test:delete")
	if err != nil {
		t.Fatalf("prepare deletion: %v", err)
	}
	if _, err := state.Resolver.Resolve(ctx, canonicalTenantID); !errors.Is(err, ontologyscope.ErrBindingConflict) {
		t.Fatalf("canonical binding during deletion = %v, want conflict", err)
	}
	if _, err := state.Resolver.Resolve(ctx, custom.TenantID); !errors.Is(err, ontologyscope.ErrBindingNotFound) {
		t.Fatalf("custom binding during deletion = %v, want not found", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := rollback(canceled); err != nil {
		t.Fatalf("rollback live source deletion: %v", err)
	}
	if _, err := state.Resolver.Resolve(ctx, canonicalTenantID); err != nil {
		t.Fatalf("canonical binding after rollback: %v", err)
	}
	if _, err := state.Resolver.Resolve(ctx, custom.TenantID); err != nil {
		t.Fatalf("custom binding after rollback: %v", err)
	}

	_, err = prepareOntologySourceDeletion(ctx, state, identity.ObjectRef{Type: "mission", ID: missionID.String()}, []string{canonicalTenantID}, "test:delete-recovery")
	if err != nil {
		t.Fatalf("prepare recoverable deletion: %v", err)
	}
	if err := (productionOntologySourceDeletionLifecycle{state: state, now: func() time.Time { return time.Now().UTC().Add(10 * time.Minute) }}).Sweep(ctx); err != nil {
		t.Fatalf("recover live source deletion: %v", err)
	}
	if _, err := state.Resolver.Resolve(ctx, canonicalTenantID); err != nil {
		t.Fatalf("canonical binding after recovery: %v", err)
	}

	rollback, err = prepareOntologySourceDeletion(ctx, state, identity.ObjectRef{Type: "mission", ID: missionID.String()}, []string{canonicalTenantID}, "test:delete-success")
	if err != nil {
		t.Fatalf("prepare successful deletion: %v", err)
	}
	if err := missions.Delete(ctx, missionID); err != nil {
		t.Fatalf("delete mission: %v", err)
	}
	if err := rollback(ctx); err != nil {
		t.Fatalf("rollback after committed source deletion: %v", err)
	}
	for _, tenantID := range []string{canonicalTenantID, custom.TenantID} {
		if _, err := state.Resolver.Resolve(ctx, tenantID); !errors.Is(err, ontologyscope.ErrBindingNotFound) {
			t.Fatalf("resolved deleted source binding %s: %v", tenantID, err)
		}
	}
	persisted, err := state.Store.GetBinding(ctx, custom.TenantID)
	if err != nil || persisted.State != ontologyscope.BindingStateDisabled {
		t.Fatalf("persisted custom binding = %+v err=%v", persisted, err)
	}
}
