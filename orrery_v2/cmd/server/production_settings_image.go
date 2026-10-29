package main

import (
	"encoding/json"
	"fmt"
	"strings"

	httptransport "orrery_v2/internal/transport/http"
)

func (s productionSettingsStore) applyImageGenerationBootstrap(settings *httptransport.SettingsDTO, payload []byte) error {
	if len(payload) > 0 {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(payload, &fields); err != nil {
			return fmt.Errorf("decode image generation settings presence: %w", err)
		}
		// Presence is intentional, including an empty object or explicit disabled state.
		// Never re-enable an administrator's saved configuration on restart.
		if field, exists := fields["image_generation"]; exists && string(field) != "null" {
			return nil
		}
	}
	bootstrap := s.config.Bootstrap.ImageGeneration
	image := httptransport.SettingsImageGenerationDTO{
		Enabled:   bootstrap.Enabled,
		Protocol:  "openai-compatible",
		BaseURL:   strings.TrimRight(strings.TrimSpace(bootstrap.BaseURL), "/"),
		Model:     strings.TrimSpace(bootstrap.Model),
		APIKeyRef: strings.TrimSpace(bootstrap.APIKeyRef),
	}
	if image.Model == "" {
		image.Model = "gpt-image-2"
	}
	if image.APIKeyRef == "" {
		image.APIKeyRef = "secret://image/default"
	}
	if field, message, valid := httptransport.ValidateImageGenerationSettings(image); !valid {
		return fmt.Errorf("invalid bootstrap.%s: %s", field, message)
	}
	settings.ImageGeneration = image
	return nil
}
