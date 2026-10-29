package llmprofile

import (
	"context"
	"fmt"
	"strings"
)

type LLMProfile struct {
	ProfileID     string
	DisplayName   string
	Provider      string
	BaseURL       string
	APIKey        string
	APIKeyRef     string
	Model         string
	Temperature   float64
	ContextLength int
	Enabled       bool
	GlobalDefault bool
}

type ProfileProvider interface {
	LLMProfiles(ctx context.Context) ([]LLMProfile, error)
}

type SecretResolver interface {
	ResolveSecret(ctx context.Context, ref string) (string, error)
}

type StaticProfileProvider struct {
	Profiles []LLMProfile
}

func (p StaticProfileProvider) LLMProfiles(ctx context.Context) ([]LLMProfile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append([]LLMProfile(nil), p.Profiles...), nil
}

type StaticSecretResolver struct {
	Values map[string]string
}

func (r StaticSecretResolver) ResolveSecret(ctx context.Context, ref string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", nil
	}
	if r.Values == nil {
		return "", fmt.Errorf("llm secret %q is not configured", ref)
	}
	value := strings.TrimSpace(r.Values[ref])
	if value == "" {
		return "", fmt.Errorf("llm secret %q is not configured", ref)
	}
	return value, nil
}
