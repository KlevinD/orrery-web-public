package userauth

import (
	"context"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestAccountFromProfileDisablesMarketTalentLogin(t *testing.T) {
	profile := agentcore.AgentProfile{
		ID:        "human-market",
		AgentKind: agentcore.AgentKindHuman,
		Enabled:   true,
		Placement: agentcore.TalentPlacement{Status: agentcore.TalentPlacementMarket},
		UserAccount: agentcore.UserAccountSpec{
			Username:       "market-user",
			AccountEnabled: true,
		},
	}

	account := accountFromProfile(profile)
	if account.Enabled {
		t.Fatalf("market talent account should not be login-enabled: %+v", account)
	}

	profile.Placement = agentcore.TalentPlacement{Status: agentcore.TalentPlacementInternal}
	account = accountFromProfile(profile)
	if !account.Enabled {
		t.Fatalf("internal enabled account should be login-enabled: %+v", account)
	}
}

func TestProfileAccountStoreRejectsDuplicateNormalizedUsernames(t *testing.T) {
	ctx := context.Background()
	repo := &profileAccountTestRepository{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"human-1": {
			ID:        "human-1",
			AgentKind: agentcore.AgentKindHuman,
			UserAccount: agentcore.UserAccountSpec{
				Username: "Operator",
			},
		},
		"human-2": {
			ID:        "human-2",
			AgentKind: agentcore.AgentKindHuman,
			UserAccount: agentcore.UserAccountSpec{
				Username: " operator ",
			},
		},
	}}
	store := NewProfileAccountStore(repo)

	if _, err := store.GetAccountByUsername(ctx, "OPERATOR"); !errors.Is(err, ErrAccountConflict) {
		t.Fatalf("GetAccountByUsername error = %v, want ErrAccountConflict", err)
	}
	if err := store.SaveAccount(ctx, Account{
		Username:       "operator",
		AgentProfileID: "human-1",
		Enabled:        true,
	}); !errors.Is(err, ErrAccountConflict) {
		t.Fatalf("SaveAccount error = %v, want ErrAccountConflict", err)
	}
}

func TestProfileAccountStoreUsesBulkProfileLookup(t *testing.T) {
	ctx := context.Background()
	repo := &bulkProfileAccountTestRepository{profileAccountTestRepository: profileAccountTestRepository{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"human-1": {
				ID:        "human-1",
				AgentKind: agentcore.AgentKindHuman,
				Enabled:   true,
				UserAccount: agentcore.UserAccountSpec{
					Username:       "operator",
					AccountEnabled: true,
				},
			},
			"human-2": {
				ID:        "human-2",
				AgentKind: agentcore.AgentKindHuman,
				UserAccount: agentcore.UserAccountSpec{
					Username: "other-user",
				},
			},
		},
	}}
	store := NewProfileAccountStore(repo)

	account, err := store.GetAccountByUsername(ctx, "OPERATOR")
	if err != nil {
		t.Fatalf("GetAccountByUsername: %v", err)
	}
	if account.Username != "operator" {
		t.Fatalf("account = %+v, want operator", account)
	}
	if repo.getProfilesCalls != 1 || repo.getProfileCalls != 0 {
		t.Fatalf("profile reads: bulk=%d individual=%d, want bulk=1 individual=0", repo.getProfilesCalls, repo.getProfileCalls)
	}

	other := repo.profiles["human-2"]
	other.UserAccount.Username = " operator "
	repo.profiles["human-2"] = other
	repo.getProfilesCalls = 0
	repo.getProfileCalls = 0
	if err := store.SaveAccount(ctx, Account{
		Username:       "operator",
		AgentProfileID: "human-1",
		Enabled:        true,
	}); !errors.Is(err, ErrAccountConflict) {
		t.Fatalf("SaveAccount error = %v, want ErrAccountConflict", err)
	}
	if repo.getProfilesCalls != 1 || repo.getProfileCalls != 0 {
		t.Fatalf("conflict profile reads: bulk=%d individual=%d, want bulk=1 individual=0", repo.getProfilesCalls, repo.getProfileCalls)
	}
}

func TestProfileAccountStoreKeepsBootstrapSuperadminOutOfWorkforceProfiles(t *testing.T) {
	ctx := context.Background()
	repo := &profileAccountTestRepository{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	store := NewProfileAccountStore(repo)
	if err := EnsureBootstrapSuperadmin(ctx, store, "bootstrap-secret"); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	account, err := store.GetAccountByUsername(ctx, SuperadminUsername)
	if err != nil {
		t.Fatalf("GetAccountByUsername: %v", err)
	}
	if account.AgentProfileID != SuperadminAgentProfileID || len(repo.profiles) != 0 {
		t.Fatalf("bootstrap superadmin leaked into workforce: account=%+v profiles=%+v", account, repo.profiles)
	}
	for _, id := range []string{LegacySuperadminAgentProfileID, LegacyLocalAdminProfileID, LegacyLocalUserProfileID} {
		if err := store.SaveAccount(ctx, Account{Username: "reserved", AgentProfileID: id, Enabled: true}); !errors.Is(err, ErrAccountConflict) {
			t.Fatalf("SaveAccount(%s) error = %v, want ErrAccountConflict", id, err)
		}
	}
}

type profileAccountTestRepository struct {
	profiles map[agentcore.AgentProfileID]agentcore.AgentProfile
}

type bulkProfileAccountTestRepository struct {
	profileAccountTestRepository
	getProfileCalls  int
	getProfilesCalls int
}

func (r *bulkProfileAccountTestRepository) GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	r.getProfileCalls++
	return r.profileAccountTestRepository.GetProfile(ctx, id)
}

func (r *bulkProfileAccountTestRepository) GetProfiles(_ context.Context, ids []agentcore.AgentProfileID) (map[agentcore.AgentProfileID]agentcore.AgentProfile, error) {
	r.getProfilesCalls++
	profiles := make(map[agentcore.AgentProfileID]agentcore.AgentProfile, len(ids))
	for _, id := range ids {
		if profile, ok := r.profiles[id]; ok {
			profiles[id] = profile
		}
	}
	return profiles, nil
}

func (r *profileAccountTestRepository) GetProfile(_ context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	profile, ok := r.profiles[id]
	if !ok {
		return agentcore.AgentProfile{}, ErrAccountNotFound
	}
	return profile, nil
}

func (r *profileAccountTestRepository) QueryDirectory(_ context.Context, _ agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	entries := make([]agentcore.AgentDirectoryEntry, 0, len(r.profiles))
	for id, profile := range r.profiles {
		entries = append(entries, agentcore.AgentDirectoryEntry{
			AgentProfileID: id,
			AgentKind:      profile.AgentKind,
		})
	}
	return agentcore.AgentDirectoryPage{Entries: entries, Total: len(entries)}, nil
}

func (r *profileAccountTestRepository) SaveProfile(_ context.Context, profile agentcore.AgentProfile) error {
	if r.profiles == nil {
		r.profiles = map[agentcore.AgentProfileID]agentcore.AgentProfile{}
	}
	r.profiles[profile.ID] = profile
	return nil
}
