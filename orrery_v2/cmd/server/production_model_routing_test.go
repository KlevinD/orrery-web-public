package main

import (
	"context"
	"encoding/json"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/llmprofile"
)

func TestProductionModelRoutingInheritsHotDefaultAndPreservesTaskOverride(t *testing.T) {
	provider := &llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{
		{ProfileID: "luna", Model: "gpt-6-luna", Enabled: true, GlobalDefault: true},
		{ProfileID: "special", Model: "special-model", Enabled: true},
	}}
	router := productionModelRouter{profiles: provider}
	for _, kind := range []agentcore.RuntimeKind{agentcore.RuntimeKindPi, agentcore.RuntimeKindCodex} {
		for _, tc := range []struct {
			name           string
			values         map[string]string
			config         string
			model, profile string
		}{
			{"inherit", map[string]string{"llm_profile_id": "platform-default"}, `{"model":"stale-copy"}`, "gpt-6-luna", "luna"},
			{"empty", nil, `{}`, "gpt-6-luna", "luna"},
			{"agent", map[string]string{"llm_profile_id": "special"}, `{"model":"stale-copy"}`, "special-model", "special"},
			{"task model", map[string]string{"llm_profile_id": "special", "source_model": "openai/task-model"}, `{}`, "task-model", "special"},
			{"fallback", map[string]string{"llm_profile_id": "special", "source_model": "openai/task-model", runtimecore.LLMModelOverrideProviderKey: "fallback-model"}, `{}`, "fallback-model", "special"},
			{"runtime override", nil, `{"model":"runtime-model"}`, "runtime-model", "luna"},
		} {
			t.Run(string(kind)+"/"+tc.name, func(t *testing.T) {
				req := agentcore.RuntimeRequest{TenantID: "tenant", RunID: "run", AgentID: "agent", RuntimeOptions: agentcore.RuntimeOptions{ProviderConfig: json.RawMessage(tc.config), ProviderSpecific: tc.values}}
				got, err := router.ResolveRuntimeModel(context.Background(), kind, req)
				if err != nil {
					t.Fatal(err)
				}
				if got.RuntimeOptions.Model != tc.model || got.RuntimeOptions.ProviderSpecific["llm_profile_id"] != tc.profile {
					t.Fatalf("options=%+v", got.RuntimeOptions)
				}
				if got.TenantID != req.TenantID || got.AgentID != req.AgentID || got.RunID != req.RunID {
					t.Fatal("routing changed identity")
				}
				if _, changed := req.RuntimeOptions.ProviderSpecific["resolved_llm_model"]; changed {
					t.Fatal("routing mutated caller's map")
				}
			})
		}
	}
	req := agentcore.RuntimeRequest{RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{"llm_profile_id": "platform-default"}}}
	provider.Profiles[0].GlobalDefault, provider.Profiles[1].GlobalDefault = false, true
	got, err := router.ResolveRuntimeModel(context.Background(), agentcore.RuntimeKindCodex, req)
	if err != nil || got.RuntimeOptions.Model != "special-model" {
		t.Fatalf("hot default=%+v error=%v", got.RuntimeOptions, err)
	}
}

func TestProductionModelRoutingRejectsMissingBindingAndPreservesDedicatedBackends(t *testing.T) {
	router := productionModelRouter{profiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{ProfileID: "default", Model: "model", Enabled: true, GlobalDefault: true}}}}
	req := agentcore.RuntimeRequest{RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{"llm_profile_id": "missing"}}}
	if _, err := router.ResolveRuntimeModel(context.Background(), agentcore.RuntimeKindPi, req); err == nil {
		t.Fatal("missing explicit profile silently fell back")
	}
	for _, kind := range []agentcore.RuntimeKind{agentcore.RuntimeKindDevice, agentcore.RuntimeKindClaude, agentcore.RuntimeKindOpenClaw} {
		got, err := router.ResolveRuntimeModel(context.Background(), kind, req)
		if err != nil || got.RuntimeOptions.Model != "" {
			t.Fatalf("dedicated backend=%+v error=%v", got, err)
		}
	}
	req.RuntimeOptions.ProviderSpecific = map[string]string{"llm_provider": "custom", "llm_base_url": "https://private.example/v1", "llm_api_key_ref": "secret://private/key", "llm_model": "private-model"}
	got, err := router.ResolveRuntimeModel(context.Background(), agentcore.RuntimeKindPi, req)
	if err != nil || got.RuntimeOptions.Model != "private-model" || got.RuntimeOptions.ProviderSpecific["llm_profile_id"] != "" {
		t.Fatalf("legacy binding=%+v error=%v", got, err)
	}
}
