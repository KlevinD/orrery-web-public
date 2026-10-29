package main

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/peopleaccount"
)

func TestProductionPeopleAccountProjectionMapsAuthorizedResult(t *testing.T) {
	adapter := productionPeopleAccountProjector{next: &fakePeopleAccountProjector{
		result: peopleaccount.ProjectionResult{
			PersonID: "human-quzheng", Revision: 7, Status: peopleaccount.ProjectionStatusApplied,
			KratosIdentityID: "kratos-quzheng", InitialPassword: "initial-password-123",
		},
	}}

	result, err := adapter.ProjectLatest(context.Background(), "human-quzheng")
	if err != nil {
		t.Fatalf("ProjectLatest: %v", err)
	}
	if result.Revision != 7 || result.KratosIdentityID != "kratos-quzheng" || result.InitialPassword != "initial-password-123" {
		t.Fatalf("result = %+v", result)
	}
}

func TestProductionPeopleAccountProjectionLoadsLegacyHashInProcess(t *testing.T) {
	source := productionPeopleLegacyCredentialSource{profiles: fakePeopleProjectionProfiles{profile: agentcore.AgentProfile{
		ID: "human-quzheng", UserAccount: agentcore.UserAccountSpec{PasswordVerifier: "$argon2id$legacy-hash"},
	}}}

	hash, err := source.LegacyPasswordHash(context.Background(), "human-quzheng")
	if err != nil {
		t.Fatalf("LegacyPasswordHash: %v", err)
	}
	if hash != "$argon2id$legacy-hash" {
		t.Fatalf("hash = %q", hash)
	}
}

func TestProductionPeopleAccountProjectionSweeperUsesConfiguredBatch(t *testing.T) {
	next := &fakePeopleAccountProjector{}
	sweeper := productionPeopleAccountProjectionSweeper{next: next, batchSize: 37}

	if err := sweeper.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if next.sweepLimit != 37 {
		t.Fatalf("sweep limit = %d", next.sweepLimit)
	}
}

type fakePeopleAccountProjector struct {
	result     peopleaccount.ProjectionResult
	err        error
	sweepLimit int
}

func (p *fakePeopleAccountProjector) ProjectLatest(context.Context, string) (peopleaccount.ProjectionResult, error) {
	return p.result, p.err
}

func (p *fakePeopleAccountProjector) Sweep(_ context.Context, limit int) error {
	p.sweepLimit = limit
	return p.err
}

type fakePeopleProjectionProfiles struct {
	profile agentcore.AgentProfile
}

func (p fakePeopleProjectionProfiles) GetProfile(context.Context, agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	return p.profile, nil
}
