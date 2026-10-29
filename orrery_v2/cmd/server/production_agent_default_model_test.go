package main

import (
	"context"
	"testing"

	"orrery_v2/internal/agent/runtime/llmprofile"
	"orrery_v2/internal/agent/runtime/providers/profilemodel"
)

func TestProductionAgentCorePlatformDefaultModel(t *testing.T) {
	profiles := llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{
		{ProfileID: "current-default", Provider: "openai", BaseURL: "http://127.0.0.1:1/v1", APIKey: "test", Model: "selected", Enabled: true, GlobalDefault: true},
	}}
	models := productionAgentCoreModels{
		factory: profilemodel.NewProfileModelFactory(profilemodel.ProfileModelFactoryConfig{Profiles: profiles}),
		deps:    productionDiscussionAgentDependencies{LLMProfiles: profiles},
	}
	chat, err := models.ModelForRequest(context.Background(), profilemodel.ModelRequest{LLMProfileID: llmprofile.PlatformDefaultProfileID})
	if err != nil || chat == nil {
		t.Fatalf("platform default did not resolve: %v", err)
	}
	if _, err := models.ModelForRequest(context.Background(), profilemodel.ModelRequest{LLMProfileID: "missing"}); err == nil {
		t.Fatal("missing explicit profile fell back")
	}
}
