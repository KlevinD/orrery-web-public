package main

import (
	"context"
	"errors"
	"fmt"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/peopleaccount"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
)

func configureProductionPeopleAccountProjection(rt *platform.Runtime, core *db.Store, profiles *agentcore.PlatformProfileRepository, auth identityAuthConfig) (*peopleaccount.Store, *productionPeopleAccountProjector, error) {
	if auth.IdentityMode != identityModePeopleSSO {
		return nil, nil, nil
	}
	if rt == nil || core == nil || profiles == nil || auth.KratosAdmin == nil {
		return nil, nil, fmt.Errorf("People account projection is not configured")
	}
	store := peopleaccount.NewStore(core)
	projector := peopleaccount.NewProjector(store, auth.KratosAdmin).
		WithOAuth2SessionRevoker(auth.HydraAdmin).
		WithLegacyCredentialSource(productionPeopleLegacyCredentialSource{profiles: profiles})
	profiles.WithProfileSaveHook(peopleaccount.NewProfileProjectionHook())
	if err := rt.RegisterSweeper("people.account_projection", productionPeopleAccountProjectionSweeper{
		next: projector, batchSize: rt.Config.Sweeper.BatchSize,
	}); err != nil {
		return nil, nil, err
	}
	return store, &productionPeopleAccountProjector{next: projector}, nil
}

type peopleAccountProjectionPort interface {
	ProjectLatest(ctx context.Context, personID string) (peopleaccount.ProjectionResult, error)
}

type productionPeopleAccountProjector struct {
	next peopleAccountProjectionPort
}

type productionPeopleAccountProjectionStateReader struct {
	store *peopleaccount.Store
}

func (r productionPeopleAccountProjectionStateReader) AccountProjectionState(ctx context.Context, personID string) (operations.WorkforceAccountProjectionDTO, bool, error) {
	if r.store == nil {
		return operations.WorkforceAccountProjectionDTO{}, false, nil
	}
	state, err := r.store.ProjectionState(ctx, personID)
	if errors.Is(err, peopleaccount.ErrProjectionNotFound) {
		return operations.WorkforceAccountProjectionDTO{}, false, nil
	}
	if err != nil {
		return operations.WorkforceAccountProjectionDTO{}, false, err
	}
	return operations.WorkforceAccountProjectionDTO{
		Status: state.Status, Revision: state.Projection.Revision, KratosIdentityID: state.KratosIdentityID,
	}, true, nil
}

func configureProductionPeopleAccountOperations(query *operations.QueryService, command *operations.CommandService, store *peopleaccount.Store, projector *productionPeopleAccountProjector) {
	if store == nil || projector == nil {
		return
	}
	command.SetPeopleSSOEnabled(true)
	command.SetWorkforceAccountProjector(projector)
	query.SetWorkforceAccountProjectionStateReader(productionPeopleAccountProjectionStateReader{store: store})
}

func (p productionPeopleAccountProjector) ProjectLatest(ctx context.Context, personID string) (operations.WorkforceAccountProjectionResult, error) {
	if p.next == nil {
		return operations.WorkforceAccountProjectionResult{}, fmt.Errorf("People account projector is not configured")
	}
	result, err := p.next.ProjectLatest(ctx, personID)
	if err != nil {
		return operations.WorkforceAccountProjectionResult{}, err
	}
	return operations.WorkforceAccountProjectionResult{
		Status:           result.Status,
		Revision:         result.Revision,
		KratosIdentityID: result.KratosIdentityID,
		InitialPassword:  result.InitialPassword,
	}, nil
}

type peopleProjectionProfileReader interface {
	GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error)
}

type productionPeopleLegacyCredentialSource struct {
	profiles peopleProjectionProfileReader
}

func (s productionPeopleLegacyCredentialSource) LegacyPasswordHash(ctx context.Context, personID string) (string, error) {
	if s.profiles == nil {
		return "", fmt.Errorf("People profile repository is not configured")
	}
	profile, err := s.profiles.GetProfile(ctx, agentcore.AgentProfileID(personID))
	if err != nil {
		return "", err
	}
	return profile.UserAccount.PasswordVerifier, nil
}

type peopleAccountProjectionSweepPort interface {
	Sweep(ctx context.Context, limit int) error
}

type productionPeopleAccountProjectionSweeper struct {
	next      peopleAccountProjectionSweepPort
	batchSize int
}

func (s productionPeopleAccountProjectionSweeper) Sweep(ctx context.Context) error {
	if s.next == nil {
		return fmt.Errorf("People account projection sweeper is not configured")
	}
	batchSize := s.batchSize
	if batchSize <= 0 {
		batchSize = 100
	}
	return s.next.Sweep(ctx, batchSize)
}
