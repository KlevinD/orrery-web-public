package platform

import (
	"fmt"
	"sort"
	"strings"

	"orrery_v2/internal/platform/config"
)

type staticRuntimeConfig struct {
	databaseURL  string
	databaseURLs map[string]string
}

func staticFromConfig(cfg config.Config) staticRuntimeConfig {
	urls := make(map[string]string, len(cfg.DatabaseURLs))
	for role, rawURL := range cfg.DatabaseURLs {
		urls[role] = rawURL
	}
	return staticRuntimeConfig{databaseURL: cfg.DatabaseURL, databaseURLs: urls}
}

func (s staticRuntimeConfig) diff(cfg config.Config) error {
	var changed []string
	if cfg.DatabaseURL != s.databaseURL {
		changed = append(changed, "database.default_url")
	}
	roles := make([]string, 0, len(cfg.DatabaseURLs))
	for role := range cfg.DatabaseURLs {
		roles = append(roles, role)
	}
	for role := range s.databaseURLs {
		if _, ok := cfg.DatabaseURLs[role]; !ok {
			roles = append(roles, role)
		}
	}
	sort.Strings(roles)
	last := ""
	for _, role := range roles {
		if role == last {
			continue
		}
		last = role
		if cfg.DatabaseURLs[role] != s.databaseURLs[role] {
			changed = append(changed, "database.urls."+role)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	return fmt.Errorf("restart required: static runtime config changed: %s", strings.Join(changed, ", "))
}

func isRestartRequired(err error) bool {
	return err != nil && strings.Contains(err.Error(), "restart required")
}
