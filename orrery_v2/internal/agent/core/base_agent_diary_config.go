package core

import (
	"encoding/json"
	"strings"
	"time"
)

const BaseAgentDiaryConfigExtensionKey = "diary"

type BaseAgentDiaryConfig struct {
	Timezone string `json:"timezone"`
}

func fillDiaryConfigProviderSpecificDefaults(providerSpecific map[string]string, extensions map[string]json.RawMessage) {
	if providerSpecific == nil || len(extensions) == 0 || strings.TrimSpace(providerSpecific["diary_timezone"]) != "" {
		return
	}
	var config BaseAgentDiaryConfig
	if err := json.Unmarshal(extensions[BaseAgentDiaryConfigExtensionKey], &config); err != nil {
		return
	}
	config.Timezone = strings.TrimSpace(config.Timezone)
	if config.Timezone == "" {
		return
	}
	if _, err := time.LoadLocation(config.Timezone); err != nil {
		return
	}
	providerSpecific["diary_timezone"] = config.Timezone
}
