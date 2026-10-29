package llmprofile

import (
	"context"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

// PlatformDefaultProfileID is a live reference, never a copy of today's default.
const PlatformDefaultProfileID = agentcore.PlatformDefaultLLMProfileID

type SelectionRequest struct {
	ProfileID string
	// ConfigRef is a legacy hint; it may name a runtime rather than an LLM profile.
	ConfigRef string
	Model     string
}

type Selection struct {
	Profile LLMProfile
	Source  string
}

// Resolve selects a fresh profile for each invocation. Explicit missing or
// disabled profiles fail instead of silently using another provider's model.
func Resolve(ctx context.Context, provider ProfileProvider, req SelectionRequest) (Selection, error) {
	if err := ctx.Err(); err != nil {
		return Selection{}, err
	}
	if provider == nil {
		return Selection{}, fmt.Errorf("llm profile provider is not configured")
	}
	profiles, err := provider.LLMProfiles(ctx)
	if err != nil {
		return Selection{}, err
	}
	return Select(profiles, req)
}

func Select(profiles []LLMProfile, req SelectionRequest) (Selection, error) {
	preferred := strings.TrimSpace(req.ProfileID)
	configRef := strings.TrimSpace(req.ConfigRef)
	if preferred == PlatformDefaultProfileID {
		preferred, configRef = "", ""
	}
	var first, global, explicit, legacy *LLMProfile
	for _, item := range profiles {
		profile := Normalize(item)
		if !profile.Enabled {
			continue
		}
		if first == nil {
			copy := profile
			first = &copy
		}
		if profile.GlobalDefault {
			if global != nil {
				return Selection{}, fmt.Errorf("multiple enabled global llm defaults are configured")
			}
			copy := profile
			global = &copy
		}
		if preferred != "" && profile.ProfileID == preferred {
			if explicit != nil {
				return Selection{}, fmt.Errorf("llm profile %q is ambiguous", preferred)
			}
			copy := profile
			explicit = &copy
		}
		if preferred == "" && configRef != "" && profile.ProfileID == configRef {
			copy := profile
			legacy = &copy
		}
	}
	var selected *LLMProfile
	source := "global_default"
	switch {
	case preferred != "":
		if explicit == nil {
			return Selection{}, fmt.Errorf("llm profile %q is missing or disabled", preferred)
		}
		selected, source = explicit, "explicit_profile"
	case legacy != nil:
		selected, source = legacy, "config_ref"
	case global != nil:
		selected = global
	case first != nil:
		selected, source = first, "first_enabled"
	default:
		return Selection{}, fmt.Errorf("no enabled llm profile is configured")
	}
	profile := *selected
	if model := strings.TrimSpace(req.Model); model != "" {
		profile.Model = model
	}
	return Selection{Profile: profile, Source: source}, nil
}

func Normalize(profile LLMProfile) LLMProfile {
	profile.ProfileID = strings.TrimSpace(profile.ProfileID)
	profile.DisplayName = strings.TrimSpace(profile.DisplayName)
	profile.Provider = strings.TrimSpace(profile.Provider)
	profile.BaseURL = strings.TrimSpace(profile.BaseURL)
	profile.APIKey = strings.TrimSpace(profile.APIKey)
	profile.APIKeyRef = strings.TrimSpace(profile.APIKeyRef)
	profile.Model = strings.TrimSpace(profile.Model)
	return profile
}
