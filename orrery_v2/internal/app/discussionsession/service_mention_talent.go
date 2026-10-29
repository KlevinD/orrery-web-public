package discussionsession

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	missionteam "orrery_v2/internal/mission/team"
)

type discussionTalentListResult struct {
	Total      int
	Returned   int
	Eligible   int
	Candidates []discussionTalentCandidate `json:"candidates"`
}

type discussionTalentCandidate struct {
	AgentID          string `json:"agent_id"`
	DisplayName      string `json:"display_name"`
	RoleName         string `json:"role_name,omitempty"`
	AgentKind        string `json:"agent_kind,omitempty"`
	Placement        string `json:"placement,omitempty"`
	Responsibility   string `json:"responsibility,omitempty"`
	AuthorityScope   string `json:"authority_scope,omitempty"`
	BehaviorBoundary string `json:"behavior_boundary,omitempty"`
	Availability     string `json:"availability,omitempty"`
	Capacity         int    `json:"capacity,omitempty"`
	CurrentLoad      int    `json:"current_load,omitempty"`
}

func (s *Service) listDiscussionTalentFromToolCall(ctx context.Context, conversationID string, call ToolCallDTO) (discussionTalentListResult, error) {
	if s == nil || s.repo == nil || s.agents == nil {
		return discussionTalentListResult{}, fmt.Errorf("%w: personnel directory is required to list discussion talent", ErrConversationMember)
	}
	view, err := s.repo.GetConversation(ctx, strings.TrimSpace(conversationID))
	if err != nil {
		return discussionTalentListResult{}, err
	}
	existing := map[string]bool{}
	for _, member := range view.Members {
		if strings.TrimSpace(member.MemberKind) != "agent" {
			continue
		}
		if id := strings.TrimSpace(member.MemberID); id != "" {
			existing[id] = true
		}
	}
	limit := discussionTalentListLimit(call)
	query := agentcore.AgentDirectoryQuery{
		EnabledOnly:   true,
		IncludeAI:     true,
		IncludeHumans: true,
		Placement:     discussionTalentListPlacement(call),
		Page:          agentcore.Page{Limit: discussionTalentListQueryLimit(limit, len(existing))},
	}
	page, err := s.agents.QueryDirectory(ctx, query)
	if err != nil {
		return discussionTalentListResult{}, fmt.Errorf("%w: query personnel directory: %v", ErrConversationMember, err)
	}
	result := discussionTalentListResult{Total: page.Total}
	for _, entry := range page.Entries {
		id := conversationDirectoryEntryMemberID(entry)
		if id == "" || !conversationMemberAddEntryAvailable(entry) {
			continue
		}
		if missionteam.IsWorkerInternalQAAgent(entry.AgentProfileID, entry.OrgIdentity.RoleRefs) {
			continue
		}
		if existing[id] {
			continue
		}
		if len(result.Candidates) >= limit {
			break
		}
		name, err := strictConversationDisplayName(id, entry.DisplayName, entry.RealName)
		if err != nil {
			continue
		}
		candidate := discussionTalentCandidate{
			AgentID:          id,
			DisplayName:      name,
			RoleName:         strings.TrimSpace(entry.RoleName),
			AgentKind:        strings.TrimSpace(string(entry.AgentKind)),
			Placement:        string(entry.Placement.NormalizedStatus(entry.OrgIdentity)),
			Responsibility:   truncateDiscussionTalentText(firstNonBlank(entry.PersonaSummary, entry.RoleName, entry.OrgIdentity.AuthorityScope), 240),
			AuthorityScope:   strings.TrimSpace(entry.OrgIdentity.AuthorityScope),
			BehaviorBoundary: truncateDiscussionTalentText(entry.BehaviorBoundary, 240),
			Availability:     firstNonBlank(string(entry.Availability.Status), string(agentcore.AvailabilityUnknown)),
			Capacity:         entry.Availability.Capacity,
			CurrentLoad:      entry.Availability.CurrentLoad,
		}
		result.Eligible++
		result.Candidates = append(result.Candidates, candidate)
	}
	result.Returned = len(result.Candidates)
	return result, nil
}

func discussionTalentListPlacement(call ToolCallDTO) agentcore.TalentPlacementFilter {
	switch strings.ToLower(strings.TrimSpace(firstNonBlank(call.Arguments["placement"], call.Arguments["scope"], call.Result["placement"], call.Result["scope"]))) {
	case "all", "any", "全部":
		return agentcore.TalentPlacementFilterAll
	case "market", "talent_market", "人才市场":
		return agentcore.TalentPlacementFilterMarket
	default:
		return agentcore.TalentPlacementFilterInternal
	}
}

func discussionTalentListLimit(call ToolCallDTO) int {
	raw := firstNonBlank(call.Arguments["limit"], call.Arguments["page_size"], call.Result["limit"], call.Result["page_size"])
	limit, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || limit <= 0 {
		return 30
	}
	if limit > 80 {
		return 80
	}
	return limit
}

func discussionTalentListQueryLimit(returnLimit int, existingCount int) int {
	limit := returnLimit + existingCount
	if limit < returnLimit {
		return returnLimit
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func truncateDiscussionTalentText(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if maxRunes <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return strings.TrimSpace(string(runes[:maxRunes])) + "..."
}

func writeDiscussionTalentListResult(result map[string]string, listed discussionTalentListResult) {
	result["total_count"] = strconv.Itoa(listed.Total)
	result["returned_count"] = strconv.Itoa(listed.Returned)
	result["eligible_count"] = strconv.Itoa(listed.Eligible)
	if summary := discussionTalentCandidatesSummary(listed.Candidates); summary != "" {
		result["candidates_summary"] = summary
	}
	if ids := eligibleDiscussionTalentAgentIDs(listed.Candidates); ids != "" {
		result["eligible_agent_ids"] = ids
	}
	if raw, err := json.Marshal(listed.Candidates); err == nil && len(raw) > 0 {
		result["candidates_json"] = string(raw)
	}
}

func eligibleDiscussionTalentAgentIDs(candidates []discussionTalentCandidate) string {
	var ids []string
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.AgentID) == "" {
			continue
		}
		ids = append(ids, candidate.AgentID)
	}
	return strings.Join(ids, ",")
}

func discussionTalentCandidatesSummary(candidates []discussionTalentCandidate) string {
	lines := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		id := strings.TrimSpace(candidate.AgentID)
		name := strings.TrimSpace(candidate.DisplayName)
		if id == "" || name == "" {
			continue
		}
		role := firstNonBlank(candidate.RoleName, "未标注角色")
		direction := firstNonBlank(candidate.Responsibility, candidate.AuthorityScope, "未标注负责方向")
		lines = append(lines, fmt.Sprintf("- %s (%s): %s；负责方向：%s", name, id, role, direction))
	}
	return strings.Join(lines, "\n")
}
