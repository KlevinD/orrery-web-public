package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"

	agentcore "orrery_v2/internal/agent/core"
	agentidentity "orrery_v2/internal/agent/identity"
	"orrery_v2/internal/platform/config"
	toolres "orrery_v2/internal/resource/tool"
)

const (
	defaultServerBrowserToolAlias   = "orrery_server_browser"
	defaultServerBrowserAgentIDsEnv = "ORRERY_SERVER_BROWSER_AGENT_IDS"
)

type agentProfileBindingStore interface {
	GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error)
	SaveProfile(ctx context.Context, profile agentcore.AgentProfile) error
}

func configuredServerBrowserAgentIDs(cfg config.Config) []string {
	candidates := []string{configuredDiscussionAgentID(cfg)}
	candidates = append(candidates, strings.FieldsFunc(os.Getenv(defaultServerBrowserAgentIDsEnv), func(r rune) bool {
		return r == ',' || r == ';' || unicode.IsSpace(r)
	})...)

	seen := make(map[string]struct{}, len(candidates))
	agentIDs := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		agentID := agentidentity.CanonicalProfileID(candidate)
		key := strings.ToLower(agentID)
		if agentID == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		agentIDs = append(agentIDs, agentID)
	}
	return agentIDs
}

func ensureAgentServerBrowser(
	ctx context.Context,
	profiles agentProfileBindingStore,
	tools toolres.Registry,
	agentID string,
) error {
	agentID = strings.TrimSpace(agentID)
	if profiles == nil || tools == nil || agentID == "" {
		return nil
	}

	definition, err := tools.Get(ctx, defaultServerBrowserMCPServerToolID)
	if errors.Is(err, toolres.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load server browser resource: %w", err)
	}
	if !definition.Enabled {
		return nil
	}
	if definition.Kind != toolres.KindMCPServer || definition.MCP == nil {
		return fmt.Errorf("server browser resource %s is not an MCP server", defaultServerBrowserMCPServerToolID)
	}

	profile, err := profiles.GetProfile(ctx, agentcore.AgentProfileID(agentID))
	if errors.Is(err, agentcore.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load configured server browser agent %s: %w", agentID, err)
	}
	if profile.ID.IsZero() || !profile.Enabled || profile.AgentKind == agentcore.AgentKindHuman {
		return nil
	}

	refs, changed := ensureServerBrowserToolRef(profile.ToolRefs)
	if !changed {
		return nil
	}
	profile.ToolRefs = refs
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		return fmt.Errorf("save configured server browser agent %s: %w", agentID, err)
	}
	return nil
}

func ensureServerBrowserToolRef(refs []agentcore.ToolRef) ([]agentcore.ToolRef, bool) {
	desired := agentcore.ToolRef{
		ToolID:          defaultServerBrowserMCPServerToolID,
		Alias:           defaultServerBrowserToolAlias,
		PermissionScope: defaultServerBrowserPermission,
		BindingPolicy:   "required",
	}
	out := append([]agentcore.ToolRef(nil), refs...)
	for i := range out {
		if !strings.EqualFold(strings.TrimSpace(string(out[i].ToolID)), string(defaultServerBrowserMCPServerToolID)) {
			continue
		}
		updated := out[i]
		updated.ToolID = desired.ToolID
		updated.Alias = desired.Alias
		updated.PermissionScope = desired.PermissionScope
		updated.BindingPolicy = desired.BindingPolicy
		if updated == out[i] {
			return out, false
		}
		out[i] = updated
		return out, true
	}
	return append(out, desired), true
}
