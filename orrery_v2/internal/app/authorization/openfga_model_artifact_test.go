package authorization

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenFGAModelArtifactCoversRuntimeRelationDomains(t *testing.T) {
	path := filepath.Join("..", "..", "..", "docs", "agent-company-mission-control", "openfga", "authorization-model.fga")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read OpenFGA model artifact: %v", err)
	}
	model := string(raw)

	for _, required := range []string{
		"schema 1.1",
		"type user",
		"type agent",
		"type platform",
		"define assigned_role: [role]",
		"define member: [user, agent] or assignee from assigned_role",
		"type organization",
		"type department",
		"type role",
		"type mission",
		"type discussion",
		"type project",
		"type resource",
		"define owner: [user, agent]",
		"define participant: [user, agent]",
		"define development_monitor: monitor from origin_development_project",
		"define member: [user, agent]",
		"define viewer: owner or member",
		"define poster: owner or member",
		"define viewer: admin or member",
		"define viewer: member or manager or admin",
		"define editor: manager or admin",
		"define assignee: [user, agent]",
		"define viewer: assignee or admin",
		"define operator: [user, agent] or owner or editor from bound_mission",
		"define user: [user, agent, development_project] or admin from org",
		"define manager: [user] or admin from org",
	} {
		if !strings.Contains(model, required) {
			t.Fatalf("OpenFGA model artifact missing %q:\n%s", required, model)
		}
	}
}
