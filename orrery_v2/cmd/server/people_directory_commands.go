package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/peopleaccount"
	"orrery_v2/internal/app/peopledirectory"
	"orrery_v2/internal/app/userauth"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/db"
)

func runProductionPeopleDirectorySync(ctx context.Context, cfg config.Config, out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	runtime, core, profiles, auth, err := openProductionPeopleDirectoryRuntime(ctx, cfg)
	if err != nil {
		return err
	}
	defer runtime.Close()
	syncer, err := newProductionPeopleDirectorySync(runtime, core, profiles, auth)
	if err != nil {
		return err
	}
	report, err := syncer.ScanAndMatch(ctx)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(report)
}

func runProductionPeopleDirectoryApply(ctx context.Context, cfg config.Config, snapshotID string, out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	snapshotID = strings.TrimSpace(snapshotID)
	if snapshotID == "" {
		return fmt.Errorf("People directory snapshot id is required")
	}
	runtime, core, profiles, auth, err := openProductionPeopleDirectoryRuntime(ctx, cfg)
	if err != nil {
		return err
	}
	defer runtime.Close()
	profiles.WithProfileSaveHook(peopleaccount.NewProfileProjectionHook())
	projectionStore := peopleaccount.NewStore(core)
	projection := &productionPeopleAccountProjector{next: peopleaccount.NewProjector(projectionStore, auth.KratosAdmin).
		WithLegacyCredentialSource(productionPeopleLegacyCredentialSource{profiles: profiles})}
	audit := operations.NewAuditStore(runtime.DBRouter, runtime.Clock, runtime.IDGenerator)
	roles := operations.NewRoleStore(runtime.DBRouter)
	organizations := operations.NewOrganizationCatalogStore(runtime.DBRouter)
	authState := userauth.NewPlatformAuthStateStore(core)
	authStore := userauth.NewSplitAccountStore(userauth.NewProfileAccountStore(profiles), audit).WithAuthStateStore(authState)
	roleScopeMode, err := authorizationRoleScopeModeFromPlatform(runtime.Config)
	if err != nil {
		return err
	}
	authService := userauth.NewService(authStore).
		WithIdentitySubjectStore(identity.NewPlatformSubjectStore(core)).
		WithPermissionScopeResolver(operations.NewRolePermissionScopeResolver(roles).WithMode(roleScopeMode))
	workforce := operations.NewCommandService(profiles)
	workforce.SetAuditRecorder(audit)
	workforce.SetOrganizationCatalogStore(organizations)
	workforce.SetRoleStore(roles)
	workforce.SetWorkforceSessionRevoker(authService)
	workforce.SetPeopleSSOEnabled(true)
	workforce.SetWorkforceAccountProjector(projection)
	report, err := newPeopleDirectorySnapshotApplier(peopledirectory.NewStore(core), workforce, peopleDirectoryPlacement{
		OrganizationID: defaultHumanOrganizationID,
		DepartmentID:   defaultHumanDepartmentID,
		PositionID:     defaultHumanPositionID,
	}).ApplySnapshot(ctx, snapshotID)
	return writePeopleDirectoryApplyReport(out, report, err)
}

func writePeopleDirectoryApplyReport(out io.Writer, report peopleDirectoryApplyReport, applyErr error) error {
	encodeErr := json.NewEncoder(out).Encode(report)
	if applyErr != nil && encodeErr != nil {
		return errors.Join(applyErr, encodeErr)
	}
	if applyErr != nil {
		return applyErr
	}
	return encodeErr
}

func openProductionPeopleDirectoryRuntime(ctx context.Context, cfg config.Config) (*platform.Runtime, *db.Store, *agentcore.PlatformProfileRepository, identityAuthConfig, error) {
	auth, err := identityAuthConfigFromPlatform(cfg)
	if err != nil {
		return nil, nil, nil, identityAuthConfig{}, err
	}
	if auth.IdentityMode != identityModePeopleSSO || auth.KratosAdmin == nil {
		return nil, nil, nil, identityAuthConfig{}, fmt.Errorf("People directory commands require people_sso mode and Kratos Admin")
	}
	runtime, err := platform.OpenWithModules(ctx, cfg, productionModules()...)
	if err != nil {
		return nil, nil, nil, identityAuthConfig{}, err
	}
	if err := runtime.Initialize(ctx); err != nil {
		runtime.Close()
		return nil, nil, nil, identityAuthConfig{}, err
	}
	core, err := runtime.Databases.Store(db.RoleCore)
	if err != nil {
		runtime.Close()
		return nil, nil, nil, identityAuthConfig{}, err
	}
	return runtime, core, agentcore.NewPlatformProfileRepository(runtime.UnitOfWork), auth, nil
}
