package main

import (
	"context"
	"encoding/json"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/llmprofile"
)

type productionModelRouter struct {
	profiles llmprofile.ProfileProvider
}

func (r productionModelRouter) ResolveRuntimeModel(ctx context.Context, kind agentcore.RuntimeKind, request agentcore.RuntimeRequest) (agentcore.RuntimeRequest, error) {
	// Provider-owned Claude, device, ACP and OpenClaw models retain their own
	// configuration. Only the platform's shared Pi/Codex LLM paths inherit here.
	if r.profiles == nil || (kind != agentcore.RuntimeKindPi && kind != agentcore.RuntimeKindCodex) {
		return request, nil
	}
	opts := request.RuntimeOptions
	values := cloneStringMap(opts.ProviderSpecific)
	if values == nil {
		values = map[string]string{}
	}
	profileID := strings.TrimSpace(values["llm_profile_id"])
	_, scheduledModel := splitScheduledModelReference(values["source_model"])
	model := firstNonBlank(values[runtimecore.LLMModelOverrideProviderKey], scheduledModel, opts.Model)
	var config struct {
		Model string `json:"model"`
	}
	if len(opts.ProviderConfig) > 0 {
		if err := json.Unmarshal(opts.ProviderConfig, &config); err != nil {
			return request, err
		}
	}
	if profileID == "" && model == "" {
		model = firstNonBlank(config.Model, values["llm_model"])
	}
	// An inline provider with its own credential is an explicit legacy binding.
	// It must not be replaced with credentials from the platform default.
	if profileID == "" && kind == agentcore.RuntimeKindPi &&
		firstNonBlank(values["llm_provider"], values["llm_base_url"]) != "" &&
		firstNonBlank(values["llm_api_key_ref"], values["api_key_ref"]) != "" &&
		firstNonBlank(values["llm_api_key_ref"], values["api_key_ref"]) != agentcore.DefaultSystemLLMAPIKeyRef {
		if model != "" {
			opts.Model = model
			values[runtimecore.LLMModelOverrideProviderKey] = model
		}
		opts.ProviderSpecific = values
		request.RuntimeOptions = opts
		return request, nil
	}
	selection, err := llmprofile.Resolve(ctx, r.profiles, llmprofile.SelectionRequest{ProfileID: profileID, Model: model})
	if err != nil {
		return request, err
	}
	profile := selection.Profile
	opts.Model = profile.Model
	values["llm_profile_id"] = profile.ProfileID
	values["resolved_llm_profile_id"] = profile.ProfileID
	values["resolved_llm_model"] = profile.Model
	values["llm_selection_source"] = selection.Source
	if model != "" {
		values["llm_selection_source"] += ":model_override"
	}
	// Freeze the selected model for this execution; subsequent executions read
	// current settings again. Pi's runtime config cannot override this snapshot.
	values[runtimecore.LLMModelOverrideProviderKey] = profile.Model
	opts.ProviderSpecific = values
	request.RuntimeOptions = opts
	return request, nil
}
