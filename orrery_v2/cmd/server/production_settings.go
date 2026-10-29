package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	llmprofile "orrery_v2/internal/agent/runtime/llmprofile"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/secret"
	httptransport "orrery_v2/internal/transport/http"
)

const productionSettingsTable = "core.app_settings"

type productionSettingsStore struct {
	router *db.Router
	config config.Config
}

func (s productionSettingsStore) GetSettings(ctx context.Context) (httptransport.SettingsDTO, error) {
	if err := ctx.Err(); err != nil {
		return httptransport.SettingsDTO{}, err
	}
	if s.router == nil {
		return httptransport.SettingsDTO{}, missioncontrol.ErrServiceDependency
	}
	record, ok, err := s.router.GetRecord(ctx, productionSettingsTable, "default")
	if err != nil {
		return httptransport.SettingsDTO{}, err
	}
	if !ok {
		settings := s.productionDefaultSettings()
		ensureProductionAgentCoreProfile(&settings)
		if err := s.applyImageGenerationBootstrap(&settings, nil); err != nil {
			return httptransport.SettingsDTO{}, err
		}
		return settings, nil
	}
	payload, ok := record["payload"].(string)
	if !ok || strings.TrimSpace(payload) == "" {
		return httptransport.SettingsDTO{}, fmt.Errorf("production settings payload is missing")
	}
	var settings httptransport.SettingsDTO
	if err := json.Unmarshal([]byte(payload), &settings); err != nil {
		return httptransport.SettingsDTO{}, fmt.Errorf("decode production settings: %w", err)
	}
	if err := s.applyImageGenerationBootstrap(&settings, []byte(payload)); err != nil {
		return httptransport.SettingsDTO{}, err
	}
	hasKnowledgeSettings := hasConfiguredKnowledgeSettings(settings.Knowledge)
	if settings.DiscussionAgentPairLoopLimit == 0 {
		settings.DiscussionAgentPairLoopLimit = httptransport.DefaultSettings().DiscussionAgentPairLoopLimit
	}
	ensureProductionAgentCoreProfile(&settings)
	httptransport.NormalizeSettings(&settings)
	if !hasKnowledgeSettings {
		if s.productionPageIndexConfigured() {
			settings.Knowledge = s.productionDefaultSettings().Knowledge
		} else {
			settings.Knowledge = httptransport.SettingsKnowledgeDTO{}
		}
	}
	return settings, nil
}

func (s productionSettingsStore) HumanReviewEnabled(ctx context.Context) (bool, error) {
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return false, err
	}
	return settings.HILDefaultEnabled, nil
}

func (s productionSettingsStore) productionDefaultSettings() httptransport.SettingsDTO {
	settings := httptransport.DefaultSettings()
	if !s.config.Bootstrap.PageIndexEnabled {
		settings.Knowledge = httptransport.SettingsKnowledgeDTO{}
		return settings
	}
	if apiURL := strings.TrimSpace(s.config.Bootstrap.PageIndexAPIURL); apiURL != "" {
		settings.Knowledge.PageIndex.APIURL = apiURL
	}
	if apiKeyRef := strings.TrimSpace(s.config.Bootstrap.PageIndexAPIKeyRef); apiKeyRef != "" {
		settings.Knowledge.PageIndex.APIKey = ""
		settings.Knowledge.PageIndex.APIKeyRef = apiKeyRef
	}
	return settings
}

func (s productionSettingsStore) productionPageIndexConfigured() bool {
	return s.config.Bootstrap.PageIndexEnabled && (strings.TrimSpace(s.config.Bootstrap.PageIndexAPIURL) != "" ||
		strings.TrimSpace(s.config.Bootstrap.PageIndexAPIKeyRef) != "")
}

func hasConfiguredKnowledgeSettings(settings httptransport.SettingsKnowledgeDTO) bool {
	return settings.Enabled ||
		strings.TrimSpace(settings.DefaultProvider) != "" ||
		strings.TrimSpace(settings.PageIndex.APIURL) != "" ||
		strings.TrimSpace(settings.PageIndex.APIKey) != "" ||
		strings.TrimSpace(settings.PageIndex.APIKeyRef) != "" ||
		strings.TrimSpace(settings.PageIndex.DefaultFolderID) != "" ||
		settings.PageIndex.TimeoutSeconds != 0 ||
		settings.Local.Enabled
}

func (s productionSettingsStore) SaveSettings(ctx context.Context, settings httptransport.SettingsDTO) (httptransport.SettingsDTO, error) {
	if err := ctx.Err(); err != nil {
		return httptransport.SettingsDTO{}, err
	}
	if s.router == nil {
		return httptransport.SettingsDTO{}, missioncontrol.ErrServiceDependency
	}
	if field := httptransport.PlaintextSettingsSecretField(settings); field != "" {
		return httptransport.SettingsDTO{}, fmt.Errorf("refuse to persist plaintext settings secret in %s", field)
	}
	payload, err := json.Marshal(settings)
	if err != nil {
		return httptransport.SettingsDTO{}, fmt.Errorf("encode production settings: %w", err)
	}
	if err := s.router.PutRecord(ctx, productionSettingsTable, "default", db.Record{"payload": string(payload)}); err != nil {
		return httptransport.SettingsDTO{}, err
	}
	return settings, nil
}

func (s productionSettingsStore) LLMProfiles(ctx context.Context) ([]llmprofile.LLMProfile, error) {
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]llmprofile.LLMProfile, 0, len(settings.LLMProfiles))
	for _, profile := range settings.LLMProfiles {
		out = append(out, llmprofile.LLMProfile{
			ProfileID:     profile.ProfileID,
			DisplayName:   profile.DisplayName,
			Provider:      profile.Provider,
			BaseURL:       profile.BaseURL,
			APIKey:        profile.APIKey,
			APIKeyRef:     profile.APIKeyRef,
			Model:         profile.Model,
			Temperature:   profile.Temperature,
			ContextLength: profile.ContextLength,
			Enabled:       profile.Enabled,
			GlobalDefault: profile.GlobalDefault,
		})
	}
	return out, nil
}

func (s productionSettingsStore) WorkspaceRoot(ctx context.Context) (string, error) {
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(settings.WorkspaceRoot), nil
}

func (s productionSettingsStore) DiscussionWorkspaceAllowlist(ctx context.Context) ([]string, error) {
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	return append([]string(nil), settings.WorkspaceAllowlist...), nil
}

func (s productionSettingsStore) PromptBlocks(ctx context.Context) ([]agentcore.RuntimePromptBlock, error) {
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	return httptransport.RuntimePromptBlocksFromSettings(settings.PromptBlocks), nil
}

func (s productionSettingsStore) DiscussionAgentPairLoopLimit(ctx context.Context) (int, error) {
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return 0, err
	}
	return settings.DiscussionAgentPairLoopLimit, nil
}

func (s productionSettingsStore) MemoryEnabled(ctx context.Context) (bool, error) {
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return false, err
	}
	return settings.Memory.Enabled, nil
}

func (s productionSettingsStore) ResolveRuntimeSpec(ctx context.Context, spec agentcore.RuntimeSpec) (agentcore.RuntimeSpec, error) {
	if !spec.HasKind() || spec.HasConfig() {
		return spec, nil
	}
	configRef := strings.TrimSpace(spec.ConfigRef)
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return agentcore.RuntimeSpec{}, err
	}
	if configRef == "" {
		return resolveRuntimeSpecFromOnlyEnabledProfileForKind(spec, settings.RuntimeProfiles)
	}
	for _, profile := range settings.RuntimeProfiles {
		if !runtimeProfileMatchesSpec(profile, spec, configRef) {
			continue
		}
		return runtimeSpecFromSettingsProfile(spec, profile)
	}
	return spec, nil
}

func resolveRuntimeSpecFromOnlyEnabledProfileForKind(spec agentcore.RuntimeSpec, profiles []httptransport.SettingsRuntimeProfileDTO) (agentcore.RuntimeSpec, error) {
	var matched *httptransport.SettingsRuntimeProfileDTO
	for i := range profiles {
		profile := profiles[i]
		if !runtimeProfileEligibleForImplicitSelection(profile) {
			continue
		}
		if strings.TrimSpace(profile.RuntimeKind) != "" && agentcore.RuntimeKind(strings.TrimSpace(profile.RuntimeKind)) != spec.Kind {
			continue
		}
		if matched != nil {
			return spec, nil
		}
		matched = &profiles[i]
	}
	if matched == nil {
		return spec, nil
	}
	return runtimeSpecFromSettingsProfile(spec, *matched)
}

func runtimeProfileEligibleForImplicitSelection(profile httptransport.SettingsRuntimeProfileDTO) bool {
	if !profile.Enabled {
		return false
	}
	return strings.TrimSpace(profile.ProfileID) != nativeScheduledRuntimeProfileID &&
		strings.TrimSpace(profile.RuntimeConfigRef) != nativeScheduledRuntimeProfileID
}

func runtimeSpecFromSettingsProfile(spec agentcore.RuntimeSpec, profile httptransport.SettingsRuntimeProfileDTO) (agentcore.RuntimeSpec, error) {
	resolved := spec
	if runtimeKind := strings.TrimSpace(profile.RuntimeKind); runtimeKind != "" {
		resolved.Kind = agentcore.RuntimeKind(runtimeKind)
	}
	if strings.TrimSpace(resolved.ConfigRef) == "" {
		resolved.ConfigRef = firstNonBlank(profile.RuntimeConfigRef, profile.ProfileID)
	}
	if len(profile.RuntimeConfig) > 0 {
		raw, err := json.Marshal(profile.RuntimeConfig)
		if err != nil {
			return agentcore.RuntimeSpec{}, err
		}
		resolved.Config = raw
	}
	return resolved, nil
}

func runtimeApprovalHoldTimeoutFromSettings(ctx context.Context, settings productionSettingsStore) time.Duration {
	dto, err := settings.GetSettings(ctx)
	if err != nil {
		return 0
	}
	var timeout time.Duration
	for _, profile := range dto.RuntimeProfiles {
		if !profile.Enabled {
			continue
		}
		kind := strings.ToLower(strings.TrimSpace(profile.RuntimeKind))
		if kind != "claude" && kind != "codex" && kind != "pi" {
			continue
		}
		ms := runtimeProfileConfigInt64(profile.RuntimeConfig, "approval_timeout_ms")
		if ms <= 0 {
			continue
		}
		if value := time.Duration(ms) * time.Millisecond; value > timeout {
			timeout = value
		}
	}
	return timeout
}

func runtimeProfileConfigInt64(config map[string]any, key string) int64 {
	if config == nil {
		return 0
	}
	switch value := config[key].(type) {
	case int:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	case json.Number:
		parsed, _ := value.Int64()
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		return parsed
	default:
		return 0
	}
}

func runtimeProfileMatchesSpec(profile httptransport.SettingsRuntimeProfileDTO, spec agentcore.RuntimeSpec, configRef string) bool {
	if !profile.Enabled {
		return false
	}
	if strings.TrimSpace(profile.RuntimeKind) != "" && agentcore.RuntimeKind(strings.TrimSpace(profile.RuntimeKind)) != spec.Kind {
		return false
	}
	return strings.TrimSpace(profile.ProfileID) == configRef || strings.TrimSpace(profile.RuntimeConfigRef) == configRef
}

type platformSecretResolver struct {
	provider secret.Provider
}

func (r platformSecretResolver) ResolveSecret(ctx context.Context, ref string) (string, error) {
	name := managedSecretName(ref)
	if name == "" {
		return "", fmt.Errorf("llm secret ref is required")
	}
	if r.provider == nil {
		return "", fmt.Errorf("llm secret provider is not configured")
	}
	value, err := r.provider.Get(ctx, name)
	if err != nil {
		return "", err
	}
	return value.Plaintext(), nil
}

func (r platformSecretResolver) ValidateSecretReference(ctx context.Context, ref string) error {
	name := managedSecretName(ref)
	if name == "" {
		return fmt.Errorf("secret ref is required")
	}
	if r.provider == nil {
		return fmt.Errorf("secret provider is not configured")
	}
	_, err := r.provider.Get(ctx, name)
	return err
}

func managedSecretName(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	for _, prefix := range []string{"secret://", "settings://"} {
		if strings.HasPrefix(ref, prefix) {
			ref = strings.TrimPrefix(ref, prefix)
			break
		}
	}
	ref = strings.Trim(ref, "/")
	ref = strings.ReplaceAll(ref, "/", "_")
	return strings.TrimSpace(ref)
}
