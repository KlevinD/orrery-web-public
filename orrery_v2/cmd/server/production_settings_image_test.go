package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/testkit"
	httptransport "orrery_v2/internal/transport/http"
)

func imageBootstrapConfig() config.Config {
	return config.Config{Bootstrap: config.BootstrapConfig{ImageGeneration: config.ImageGenerationBootstrapConfig{
		Enabled: true, BaseURL: " https://gateway.example/v1/ ",
	}}}
}

func TestProductionImageBootstrapForNewAndLegacyInstallations(t *testing.T) {
	for _, payload := range []string{"", `{"language":"zh-CN"}`, `{"image_generation":null}`} {
		t.Run(payload, func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			store := productionSettingsStore{router: env.DBRouter, config: imageBootstrapConfig()}
			if payload != "" {
				if err := env.DBRouter.PutRecord(context.Background(), productionSettingsTable, "default", db.Record{"payload": payload}); err != nil {
					t.Fatal(err)
				}
			}
			settings, err := store.GetSettings(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			want := httptransport.SettingsImageGenerationDTO{Enabled: true, Protocol: "openai-compatible", BaseURL: "https://gateway.example/v1", Model: "gpt-image-2", APIKeyRef: "secret://image/default"}
			if settings.ImageGeneration != want {
				t.Fatalf("default = %+v", settings.ImageGeneration)
			}
			// Reading defaults must not write or migrate the database.
			record, exists, err := env.DBRouter.GetRecord(context.Background(), productionSettingsTable, "default")
			if err != nil || exists != (payload != "") || exists && record["payload"] != payload {
				t.Fatalf("read modified persisted settings: %v %v", record, err)
			}
			if _, err := store.SaveSettings(context.Background(), settings); err != nil {
				t.Fatal(err)
			}
			reloaded, err := store.GetSettings(context.Background())
			if err != nil || reloaded.ImageGeneration != want {
				t.Fatalf("save/load lost defaults: %+v %v", reloaded.ImageGeneration, err)
			}
		})
	}
}

func TestProductionImageBootstrapPreservesOverridesAndDisabledState(t *testing.T) {
	for _, image := range []httptransport.SettingsImageGenerationDTO{
		{},
		{Enabled: false, Protocol: "openai-compatible"},
		{Enabled: false, Protocol: "openai-compatible", BaseURL: "https://custom.example/v1", Model: "custom-image", APIKeyRef: "secret://custom/image"},
		{Enabled: true, Protocol: "openai-compatible", BaseURL: "https://custom.example/v1", Model: "custom-image", APIKeyRef: "secret://custom/image"},
	} {
		env := testkit.New(t)
		store := productionSettingsStore{router: env.DBRouter, config: imageBootstrapConfig()}
		settings := httptransport.DefaultSettings()
		settings.ImageGeneration = image
		if _, err := store.SaveSettings(context.Background(), settings); err != nil {
			t.Fatal(err)
		}
		got, err := store.GetSettings(context.Background())
		env.Close()
		if image.Protocol == "" {
			image.Protocol = "openai-compatible"
		}
		if err != nil || got.ImageGeneration != image {
			t.Fatalf("overrode saved image config: %+v %v", got.ImageGeneration, err)
		}
	}
}

func TestProductionImageBootstrapSafetyAndCustomModel(t *testing.T) {
	store := productionSettingsStore{config: imageBootstrapConfig()}
	settings := httptransport.DefaultSettings()
	store.config.Bootstrap.ImageGeneration.Model = " another-image-model "
	store.config.Bootstrap.ImageGeneration.APIKeyRef = " secret://platform/image "
	if err := store.applyImageGenerationBootstrap(&settings, nil); err != nil {
		t.Fatal(err)
	}
	if settings.ImageGeneration.Model != "another-image-model" || settings.ImageGeneration.APIKeyRef != "secret://platform/image" {
		t.Fatalf("custom platform defaults lost: %+v", settings.ImageGeneration)
	}
	for _, raw := range []string{"", "http://gateway.example/v1", "https://127.0.0.1", "https://user:sensitive@gateway.example/v1"} {
		store.config.Bootstrap.ImageGeneration.BaseURL = raw
		err := store.applyImageGenerationBootstrap(&settings, nil)
		if err == nil || strings.Contains(err.Error(), "sensitive") {
			t.Fatalf("unsafe default accepted or leaked: %v", err)
		}
	}
	store.config.Bootstrap.ImageGeneration = config.ImageGenerationBootstrapConfig{}
	if err := store.applyImageGenerationBootstrap(&settings, nil); err != nil {
		t.Fatal(err)
	}
	if settings.ImageGeneration.Enabled {
		t.Fatal("unprovisioned deployment enabled generation")
	}
	encoded, _ := json.Marshal(settings.ImageGeneration)
	if strings.Contains(string(encoded), `"api_key":`) {
		t.Fatal("raw credential included")
	}
}
