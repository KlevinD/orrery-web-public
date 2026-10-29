package main

import (
	"context"
	"strings"
	"testing"

	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/testkit"
	httptransport "orrery_v2/internal/transport/http"
)

func TestProductionSettingsUsePageIndexBootstrapDefaults(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()

	settings, err := (productionSettingsStore{router: env.DBRouter, config: pageIndexBootstrapConfig()}).GetSettings(context.Background())
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if settings.Knowledge.PageIndex.APIURL != "http://pageindex:8091" ||
		settings.Knowledge.PageIndex.APIKeyRef != "secret://knowledge/pageindex" ||
		settings.Knowledge.PageIndex.APIKey != "" {
		t.Fatalf("PageIndex settings = %+v", settings.Knowledge.PageIndex)
	}
}

func TestProductionSettingsRejectPlaintextIntegrationSecrets(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	store := productionSettingsStore{router: env.DBRouter}

	tests := []struct {
		name     string
		settings httptransport.SettingsDTO
		field    string
	}{
		{
			name: "pageindex",
			settings: httptransport.SettingsDTO{Knowledge: httptransport.SettingsKnowledgeDTO{
				PageIndex: httptransport.SettingsPageIndexKnowledgeDTO{APIKey: "plaintext"},
			}},
			field: "knowledge.pageindex.api_key",
		},
		{
			name: "llm",
			settings: httptransport.SettingsDTO{LLMProfiles: []httptransport.SettingsLLMProfileDTO{
				{ProfileID: "llm", APIKey: "plaintext"},
			}},
			field: "llm_profiles[0].api_key",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := store.SaveSettings(context.Background(), test.settings)
			if err == nil || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("SaveSettings error = %v, want field %s", err, test.field)
			}
		})
	}
}

func TestProductionSettingsOverlayPageIndexBootstrapOnLegacyPartialRecord(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	store := productionSettingsStore{router: env.DBRouter, config: pageIndexBootstrapConfig()}

	if _, err := store.SaveSettings(context.Background(), httptransport.SettingsDTO{Language: "zh-CN"}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	settings, err := store.GetSettings(context.Background())
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if settings.Knowledge.PageIndex.APIURL != "http://pageindex:8091" ||
		settings.Knowledge.PageIndex.APIKeyRef != "secret://knowledge/pageindex" {
		t.Fatalf("PageIndex settings = %+v", settings.Knowledge.PageIndex)
	}
}

func TestProductionSettingsCanDisablePageIndexRESTBootstrap(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	cfg := pageIndexBootstrapConfig()
	cfg.Bootstrap.PageIndexEnabled = false

	settings, err := (productionSettingsStore{router: env.DBRouter, config: cfg}).GetSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if hasConfiguredKnowledgeSettings(settings.Knowledge) {
		t.Fatalf("knowledge settings = %+v, want disabled bootstrap", settings.Knowledge)
	}
}

func pageIndexBootstrapConfig() config.Config {
	return config.Config{Bootstrap: config.BootstrapConfig{
		PageIndexEnabled:   true,
		PageIndexAPIURL:    "http://pageindex:8091",
		PageIndexAPIKeyRef: "secret://knowledge/pageindex",
	}}
}
