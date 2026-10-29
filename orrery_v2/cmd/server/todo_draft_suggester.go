package main

import (
	"context"
	"orrery_v2/internal/agent/llm"
	profilemodel "orrery_v2/internal/agent/runtime/providers/profilemodel"
	todoapp "orrery_v2/internal/app/todo"
)

type todoDraftModelFactory struct {
	inner profilemodel.ModelFactory
}

func newProductionTodoDraftSuggester(settings productionSettingsStore, secrets platformSecretResolver) todoapp.DraftSuggester {
	models := profilemodel.NewProfileModelFactory(profilemodel.ProfileModelFactoryConfig{
		Profiles: settings,
		Secrets:  secrets,
	})
	return todoapp.NewChatDraftSuggester(todoDraftModelFactory{inner: models})
}

func (f todoDraftModelFactory) ModelForRequest(ctx context.Context, req todoapp.ChatModelRequest) (llm.ChatModel, error) {
	return f.inner.ModelForRequest(ctx, profilemodel.ModelRequest{
		Model:        req.Model,
		ConfigRef:    req.ConfigRef,
		LLMProfileID: req.LLMProfileID,
	})
}
