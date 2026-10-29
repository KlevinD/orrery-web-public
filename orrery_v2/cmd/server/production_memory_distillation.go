package main

import (
	"context"
	"net/http"
	"orrery_v2/internal/agent/llm"
	profilemodel "orrery_v2/internal/agent/runtime/providers/profilemodel"
	memorydistill "orrery_v2/internal/app/memorydistill"
	"strings"
	"time"
)

const (
	memoryNaturalLanguageReasoningEffort = "max"
)

func newProductionMemoryNaturalLanguageSynthesizer(settings productionSettingsStore, secrets platformSecretResolver) memorydistill.NaturalLanguageSynthesizer {
	models := profilemodel.NewProfileModelFactory(profilemodel.ProfileModelFactoryConfig{
		Profiles: settings,
		Secrets:  secrets,
		Client:   memoryNaturalLanguageHTTPClient(),
	})
	inner := memorydistill.NewChatNaturalLanguageSynthesizer(memorydistill.ChatModelProviderFunc(func(ctx context.Context) (llm.ChatModel, error) {
		return models.ModelForRequest(ctx, memoryNaturalLanguageModelRequest())
	}))
	return &productionMemoryNaturalLanguageSynthesizer{settings: settings, inner: inner}
}

type productionMemoryNaturalLanguageSynthesizer struct {
	settings productionSettingsStore
	inner    memorydistill.NaturalLanguageSynthesizer
}

func (s *productionMemoryNaturalLanguageSynthesizer) Synthesize(ctx context.Context, request memorydistill.NaturalLanguageSynthesisRequest) (memorydistill.NaturalLanguageSynthesis, error) {
	if strings.TrimSpace(request.Language) == "" {
		settings, err := s.settings.GetSettings(ctx)
		if err != nil {
			return memorydistill.NaturalLanguageSynthesis{}, err
		}
		request.Language = memoryNaturalLanguageLocale(request.Language, settings.Language)
	}
	result, err := s.inner.Synthesize(ctx, request)
	if err != nil {
		return memorydistill.NaturalLanguageSynthesis{}, err
	}
	result.Language = request.Language
	return result, nil
}

func memoryNaturalLanguageLocale(requested, fallback string) string {
	if requested = strings.TrimSpace(requested); requested != "" {
		return requested
	}
	if fallback = strings.TrimSpace(fallback); fallback != "" {
		return fallback
	}
	return "zh-CN"
}

func memoryNaturalLanguageModelRequest() profilemodel.ModelRequest {
	return profilemodel.ModelRequest{
		ReasoningEffort: memoryNaturalLanguageReasoningEffort,
	}
}

func memoryNaturalLanguageHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyFromEnvironment
	return &http.Client{Transport: transport, Timeout: 4 * time.Minute}
}
