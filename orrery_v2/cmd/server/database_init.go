package main

import (
	"fmt"
	"strings"

	"orrery_v2/internal/platform/config"
)

type productionDatabaseInitGroup struct {
	databaseURL string
	roles       []string
}

func productionDatabaseInitGroups(cfg config.Config) []productionDatabaseInitGroup {
	groups := make([]productionDatabaseInitGroup, 0, len(productionDatabaseRoles()))
	groupByURL := map[string]int{}
	for _, role := range productionDatabaseRoles() {
		databaseURL := strings.TrimSpace(cfg.DatabaseURLs[role])
		if existing, ok := groupByURL[databaseURL]; ok {
			groups[existing].roles = append(groups[existing].roles, role)
			continue
		}
		groupByURL[databaseURL] = len(groups)
		groups = append(groups, productionDatabaseInitGroup{
			databaseURL: databaseURL,
			roles:       []string{role},
		})
	}
	return groups
}

func (group productionDatabaseInitGroup) initializedMessage(cfg config.Config) string {
	if len(group.roles) == 1 {
		return fmt.Sprintf("database %s initialized", group.roles[0])
	}
	label := "shared"
	if strings.TrimSpace(cfg.DatabaseURL) == group.databaseURL {
		label = "default"
	}
	return fmt.Sprintf("database %s initialized (roles: %s)", label, strings.Join(group.roles, ", "))
}
