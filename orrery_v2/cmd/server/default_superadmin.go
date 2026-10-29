package main

import (
	"context"
	"errors"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/userauth"
)

const (
	retiredDefaultPMUserProfileID = "human-pm"
)

func ensureDefaultSystemUserProfiles(ctx context.Context, profiles *agentcore.PlatformProfileRepository) error {
	return ensureDefaultSystemUserProfilesWithPasswords(ctx, profiles, defaultSystemUserBootstrapPasswords())
}

func ensureDefaultSystemUserProfilesWithPasswords(ctx context.Context, profiles *agentcore.PlatformProfileRepository, passwords systemUserBootstrapPasswords) error {
	_ = passwords
	if err := removeReservedNonWorkforceProfiles(ctx, profiles); err != nil {
		return err
	}
	return removeRetiredDefaultPMUserProfile(ctx, profiles)
}

func removeReservedNonWorkforceProfiles(ctx context.Context, profiles *agentcore.PlatformProfileRepository) error {
	if profiles == nil {
		return nil
	}
	ids := []agentcore.AgentProfileID{
		userauth.SuperadminAgentProfileID,
		userauth.LegacySuperadminAgentProfileID,
		userauth.LegacyLocalAdminProfileID,
		userauth.LegacyLocalUserProfileID,
	}
	found, err := profiles.GetProfiles(ctx, ids)
	if err != nil {
		return err
	}
	for _, profileID := range ids {
		if _, exists := found[profileID]; !exists {
			continue
		}
		if err := profiles.DeleteProfile(ctx, profileID, "reserved non-workforce principal"); err != nil {
			return err
		}
	}
	return nil
}

func removeRetiredDefaultPMUserProfile(ctx context.Context, profiles *agentcore.PlatformProfileRepository) error {
	if profiles == nil {
		return nil
	}
	profileID := agentcore.AgentProfileID(retiredDefaultPMUserProfileID)
	if _, err := profiles.GetProfile(ctx, profileID); errors.Is(err, agentcore.ErrNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	return profiles.DeleteProfile(ctx, profileID, "retired built-in PM user")
}

func mergeRoleRefs(existing []agentcore.RoleRef, seeded []agentcore.RoleRef) []agentcore.RoleRef {
	seen := map[agentcore.RoleID]struct{}{}
	out := make([]agentcore.RoleRef, 0, len(existing)+len(seeded))
	for _, refs := range [][]agentcore.RoleRef{existing, seeded} {
		for _, ref := range refs {
			if strings.TrimSpace(string(ref.ID)) == "" {
				continue
			}
			if _, ok := seen[ref.ID]; ok {
				continue
			}
			seen[ref.ID] = struct{}{}
			out = append(out, ref)
		}
	}
	return out
}
