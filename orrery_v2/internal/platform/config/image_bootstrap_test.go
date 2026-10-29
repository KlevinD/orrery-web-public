package config_test

import (
	"strings"
	"testing"

	"orrery_v2/internal/platform/config"
)

func TestImageGenerationBootstrapYAML(t *testing.T) {
	cfg, err := config.LoadYAML(writeConfigBody(t, `database:
  default_url: file://./test.db
bootstrap:
  image_generation:
    enabled: true
    base_url: " https://gateway.example/v1 "
    model: " image2 "
    api_key_ref: " secret://image/default "
`))
	if err != nil {
		t.Fatal(err)
	}
	want := config.ImageGenerationBootstrapConfig{Enabled: true, BaseURL: "https://gateway.example/v1", Model: "image2", APIKeyRef: "secret://image/default"}
	if cfg.Bootstrap.ImageGeneration != want {
		t.Fatalf("bootstrap = %+v", cfg.Bootstrap.ImageGeneration)
	}
	if model, _ := cfg.String("bootstrap.image_generation.model"); model != want.Model {
		t.Fatalf("raw model = %q", model)
	}
	if enabled, _ := cfg.Bool("bootstrap.image_generation.enabled"); !enabled {
		t.Fatal("raw enabled state lost")
	}
}

func TestImageGenerationBootstrapRejectsPlaintextKey(t *testing.T) {
	_, err := config.LoadYAML(writeConfigBody(t, `bootstrap:
  image_generation:
    api_key: do-not-store-this
`))
	if err == nil || !strings.Contains(err.Error(), "api_key") {
		t.Fatalf("plaintext key field accepted: %v", err)
	}
	if strings.Contains(err.Error(), "do-not-store-this") {
		t.Fatal("plaintext key leaked")
	}
}
