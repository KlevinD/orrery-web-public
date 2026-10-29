package missioncontrol

import (
	"sort"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/mission/projection"
)

func canonicalTaskAssigneeIDs(tasks []projection.TaskCardView, members []projection.TeamMemberView) []string {
	humanIDs := make(map[string]struct{})
	for _, member := range members {
		if member.AgentKind != agentcore.AgentKindHuman {
			continue
		}
		for _, id := range []string{
			strings.TrimSpace(member.AgentID.String()),
			strings.TrimSpace(member.AgentProfileID.String()),
			strings.TrimSpace(member.AgentInstanceID.String()),
		} {
			if id != "" {
				humanIDs[id] = struct{}{}
			}
		}
	}
	seen := make(map[string]struct{}, len(tasks))
	out := make([]string, 0, len(tasks))
	for _, task := range tasks {
		agentID := strings.TrimSpace(task.Assignee.String())
		if agentID == "" {
			continue
		}
		if _, human := humanIDs[agentID]; human {
			continue
		}
		if _, ok := seen[agentID]; ok {
			continue
		}
		seen[agentID] = struct{}{}
		out = append(out, agentID)
	}
	sort.Strings(out)
	return out
}

func canonicalHumanTaskAssigneeIDs(tasks []projection.TaskCardView, members []projection.TeamMemberView) []string {
	profiles := map[string]string{}
	for _, member := range members {
		if member.AgentKind != agentcore.AgentKindHuman || member.Status == projection.TeamMemberReleased || member.AgentProfileID.IsZero() {
			continue
		}
		for _, id := range []string{member.AgentID.String(), member.AgentProfileID.String(), member.AgentInstanceID.String()} {
			if id != "" {
				profiles[id] = member.AgentProfileID.String()
			}
		}
	}
	seen := map[string]bool{}
	var ids []string
	for _, task := range tasks {
		if id := profiles[task.Assignee.String()]; id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}
