package llmprofile

import (
	"context"
	"strings"
	"testing"
)

func TestStaticProfileProviderReturnsCopy(t *testing.T) {
	provider := StaticProfileProvider{Profiles: []LLMProfile{{
		ProfileID:     "llm-primary",
		Enabled:       true,
		GlobalDefault: true,
	}}}

	profiles, err := provider.LLMProfiles(context.Background())
	if err != nil {
		t.Fatalf("LLMProfiles: %v", err)
	}
	profiles[0].ProfileID = "mutated"

	again, err := provider.LLMProfiles(context.Background())
	if err != nil {
		t.Fatalf("LLMProfiles again: %v", err)
	}
	if again[0].ProfileID != "llm-primary" {
		t.Fatalf("provider leaked internal profile slice: %+v", again)
	}
}

func TestStaticSecretResolverTrimsAndReportsMissingSecrets(t *testing.T) {
	resolver := StaticSecretResolver{Values: map[string]string{"secret://llm": "  sk-test  "}}

	value, err := resolver.ResolveSecret(context.Background(), " secret://llm ")
	if err != nil {
		t.Fatalf("ResolveSecret: %v", err)
	}
	if value != "sk-test" {
		t.Fatalf("secret value = %q", value)
	}

	_, err = resolver.ResolveSecret(context.Background(), "secret://missing")
	if err == nil || !strings.Contains(err.Error(), "secret://missing") {
		t.Fatalf("missing secret error = %v", err)
	}
}
