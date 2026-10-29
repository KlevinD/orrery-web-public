package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	llmprofile "orrery_v2/internal/agent/runtime/llmprofile"
	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/ingest"
	"orrery_v2/internal/knowledge/observability"
)

type knowledgeEmbeddingProvider struct {
	profiles llmprofile.ProfileProvider
	secrets  llmprofile.SecretResolver
	client   *http.Client
	metrics  *observability.Metrics
}

func newKnowledgeEmbeddingProvider(profiles llmprofile.ProfileProvider, secrets llmprofile.SecretResolver, client *http.Client) *knowledgeEmbeddingProvider {
	if client == nil {
		client = http.DefaultClient
	}
	return &knowledgeEmbeddingProvider{profiles: profiles, secrets: secrets, client: client}
}

func (p *knowledgeEmbeddingProvider) WithMetrics(metrics *observability.Metrics) *knowledgeEmbeddingProvider {
	p.metrics = metrics
	return p
}

func (p *knowledgeEmbeddingProvider) Embed(ctx context.Context, req ingest.EmbedRequest) (ingest.EmbedResponse, error) {
	profile, err := p.selectProfile(ctx, req.ProfileRef)
	if err != nil {
		return ingest.EmbedResponse{}, err
	}
	apiKey, err := p.resolveAPIKey(ctx, profile)
	if err != nil {
		return ingest.EmbedResponse{}, err
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = strings.TrimSpace(profile.Model)
	}
	if model == "" {
		return ingest.EmbedResponse{}, fmt.Errorf("%w: embedding model is not configured", core.ErrEmbeddingFailed)
	}
	resp, err := ingest.NewOpenAIEmbeddingProvider(p.client, profile.BaseURL, apiKey).Embed(ctx, ingest.EmbedRequest{
		ProfileRef: strings.TrimSpace(profile.ProfileID),
		Model:      model,
		Inputs:     append([]string(nil), req.Inputs...),
	})
	if err == nil && p.metrics != nil {
		p.metrics.RecordEmbeddingBatch(len(req.Inputs))
	}
	return resp, err
}

func (p *knowledgeEmbeddingProvider) selectProfile(ctx context.Context, profileRef string) (llmprofile.LLMProfile, error) {
	if p == nil || p.profiles == nil {
		return llmprofile.LLMProfile{}, fmt.Errorf("%w: embedding profile provider is not configured", core.ErrEmbeddingFailed)
	}
	profiles, err := p.profiles.LLMProfiles(ctx)
	if err != nil {
		return llmprofile.LLMProfile{}, err
	}
	profileRef = strings.TrimSpace(profileRef)
	var firstEnabled *llmprofile.LLMProfile
	for i := range profiles {
		profile := normalizeKnowledgeEmbeddingProfile(profiles[i])
		if !profile.Enabled {
			continue
		}
		if firstEnabled == nil {
			copy := profile
			firstEnabled = &copy
		}
		if profileRef != "" && profile.ProfileID == profileRef {
			return profile, nil
		}
		if profileRef == "" && profile.GlobalDefault {
			return profile, nil
		}
	}
	if profileRef == "" && firstEnabled != nil {
		return *firstEnabled, nil
	}
	return llmprofile.LLMProfile{}, fmt.Errorf("%w: embedding profile %q is not configured", core.ErrEmbeddingFailed, profileRef)
}

func (p *knowledgeEmbeddingProvider) resolveAPIKey(ctx context.Context, profile llmprofile.LLMProfile) (string, error) {
	if strings.TrimSpace(profile.APIKey) != "" {
		return strings.TrimSpace(profile.APIKey), nil
	}
	ref := strings.TrimSpace(profile.APIKeyRef)
	if ref == "" {
		return "", fmt.Errorf("%w: embedding profile %q has no api key", core.ErrEmbeddingFailed, profile.ProfileID)
	}
	if p == nil || p.secrets == nil {
		return "", fmt.Errorf("%w: embedding secret resolver is not configured", core.ErrEmbeddingFailed)
	}
	value, err := p.secrets.ResolveSecret(ctx, ref)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

func normalizeKnowledgeEmbeddingProfile(profile llmprofile.LLMProfile) llmprofile.LLMProfile {
	profile.ProfileID = strings.TrimSpace(profile.ProfileID)
	profile.BaseURL = strings.TrimSpace(profile.BaseURL)
	profile.APIKey = strings.TrimSpace(profile.APIKey)
	profile.APIKeyRef = strings.TrimSpace(profile.APIKeyRef)
	profile.Model = strings.TrimSpace(profile.Model)
	return profile
}
