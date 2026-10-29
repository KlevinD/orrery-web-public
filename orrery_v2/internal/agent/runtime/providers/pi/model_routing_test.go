package pi

import (
	"context"
	"testing"

	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/llmprofile"
)

func TestExplicitProfileReplacesStaleAgentCredential(t *testing.T) {
	opts, config, err := applyLLMProfileRuntimeEnv(context.Background(), runtimecore.ExecOptions{ProviderSpecific: map[string]string{
		"llm_profile_id": "new", "llm_provider": "openai", "llm_api_key_ref": "secret://old/key",
	}}, Config{LLMProfiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{ProfileID: "new", Provider: "openai", Model: "new-model", APIKeyRef: "secret://new/key", Enabled: true}}}, SecretResolver: staticPiSecretResolver{"secret://old/key": "old-token", "secret://new/key": "new-token"}})
	if err != nil {
		t.Fatal(err)
	}
	if config.Env["OPENAI_API_KEY"] != "new-token" || opts.ProviderSpecific["llm_api_key_ref"] != "secret://new/key" {
		t.Fatal("profile changed without changing its credential")
	}
}
