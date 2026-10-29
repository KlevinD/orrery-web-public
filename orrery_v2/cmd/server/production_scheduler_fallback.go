package main

import (
	"strings"

	runtimecore "orrery_v2/internal/agent/runtime/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
)

func applyScheduledFallbackModel(run *runtimedaemon.AgentRun, attempt int) bool {
	if run == nil || attempt < 2 || run.Request.RuntimeOptions.ProviderSpecific == nil {
		return false
	}
	providerSpecific := run.Request.RuntimeOptions.ProviderSpecific
	fallbacks := splitCommaSeparatedValues(providerSpecific["fallback_models"])
	if len(fallbacks) == 0 {
		return false
	}
	index := attempt - 2
	if index >= len(fallbacks) {
		index = len(fallbacks) - 1
	}
	primaryProvider, _ := splitScheduledModelReference(providerSpecific["source_model"])
	fallbackProvider, fallbackModel := splitScheduledModelReference(fallbacks[index])
	if fallbackModel == "" || (primaryProvider != "" && fallbackProvider != "" && !strings.EqualFold(primaryProvider, fallbackProvider)) {
		return false
	}
	run.Request.RuntimeOptions.Model = fallbackModel
	providerSpecific[runtimecore.LLMModelOverrideProviderKey] = fallbackModel
	providerSpecific["active_source_model"] = fallbacks[index]
	return true
}

func splitCommaSeparatedValues(raw string) []string {
	values := []string{}
	for _, part := range strings.Split(raw, ",") {
		if value := strings.TrimSpace(part); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func splitScheduledModelReference(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	provider, model, ok := strings.Cut(raw, "/")
	if !ok {
		return "", raw
	}
	return strings.TrimSpace(provider), strings.TrimSpace(model)
}
