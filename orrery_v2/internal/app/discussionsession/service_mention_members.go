package discussionsession

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missionteam "orrery_v2/internal/mission/team"
)

type discussionMemberAddResult struct {
	Added int
	IDs   []string
	Names []string
	Roles []string
}

func (s *Service) addDiscussionMembersFromToolCall(ctx context.Context, conversationID string, call ToolCallDTO, at time.Time) (discussionMemberAddResult, error) {
	if s == nil || s.repo == nil || s.agents == nil {
		return discussionMemberAddResult{}, fmt.Errorf("%w: personnel directory is required to add conversation members", ErrConversationMember)
	}
	view, err := s.repo.GetConversation(ctx, strings.TrimSpace(conversationID))
	if err != nil {
		return discussionMemberAddResult{}, err
	}
	if err := ensureConversationMemberAdditionsAllowed(view.ConversationRecord); err != nil {
		return discussionMemberAddResult{}, err
	}
	page, err := s.agents.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{EnabledOnly: true, IncludeAI: true, IncludeHumans: true})
	if err != nil {
		return discussionMemberAddResult{}, fmt.Errorf("%w: query personnel directory: %v", ErrConversationMember, err)
	}
	ids := conversationMemberAddAgentIDs(call)
	entries := make([]agentcore.AgentDirectoryEntry, 0, len(ids))
	if len(ids) > 0 {
		for _, id := range ids {
			entry, ok := findConversationMemberAddEntry(page.Entries, id)
			if !ok {
				return discussionMemberAddResult{}, fmt.Errorf("%w: target %s missing from enabled personnel directory", ErrConversationMember, id)
			}
			entries = append(entries, entry)
		}
	} else if count := conversationMemberAddCount(call); count > 0 {
		return discussionMemberAddResult{}, fmt.Errorf("%w: discussion_member_add no longer accepts count=%d; call discussion_talent_list first and pass explicit agent_ids chosen from the returned personnel directory", ErrInvalidRequest, count)
	} else {
		return discussionMemberAddResult{}, fmt.Errorf("%w: discussion_member_add requires explicit agent_ids chosen from discussion_talent_list", ErrInvalidRequest)
	}
	return s.addDiscussionMemberEntries(ctx, view, entries, call.Arguments["member_role"], at)
}

func (s *Service) addDiscussionMemberEntries(ctx context.Context, view ConversationRecordView, entries []agentcore.AgentDirectoryEntry, requestedRole string, at time.Time) (discussionMemberAddResult, error) {
	return s.addDiscussionMemberEntriesWithPolicy(ctx, view, entries, requestedRole, at, false)
}

func (s *Service) addDiscussionMemberEntriesWithPolicy(ctx context.Context, view ConversationRecordView, entries []agentcore.AgentDirectoryEntry, requestedRole string, at time.Time, allowWorkerInternalQA bool) (discussionMemberAddResult, error) {
	existing := map[string]bool{}
	for _, member := range view.Members {
		id := strings.TrimSpace(member.MemberID)
		if id == "" {
			continue
		}
		existing[strings.TrimSpace(member.MemberKind)+":"+id] = true
	}
	result := discussionMemberAddResult{}
	latest := latestTime(at, latestConversationMemberCreatedAt(view.Members), s.now())
	for _, entry := range entries {
		if !conversationMemberAddEntryAvailable(entry) {
			return result, fmt.Errorf("%w: target %s missing from enabled personnel directory", ErrConversationMember, conversationMemberAddEntryID(entry))
		}
		if conversationDirectoryEntryWorkerInternalQA(entry) && !allowWorkerInternalQA {
			return result, fmt.Errorf("%w: %w: member %s cannot be directly invited into a discussion group", ErrConversationMember, ErrWorkerInternalQAMember, conversationMemberAddEntryID(entry))
		}
		id := conversationMemberAddEntryID(entry)
		if id == "" {
			return result, fmt.Errorf("%w: target %s has no stable profile id", ErrConversationMember, firstNonBlank(entry.AgentInstanceID.String(), entry.DisplayName))
		}
		memberKind := conversationMemberKindForDirectoryEntry(entry)
		if memberKind == "" {
			return result, ErrInvalidRequest
		}
		if existing[memberKind+":"+id] {
			continue
		}
		name, err := strictConversationDisplayName(id, entry.DisplayName, entry.RealName)
		if err != nil {
			return result, fmt.Errorf("%w: target %s has no real display name", ErrConversationMember, id)
		}
		latest = firstTimeAfter(s.now(), latest)
		role := conversationMemberAddRole(requestedRole, entry)
		if err := s.repo.SaveMember(ctx, ConversationMemberRecord{
			ConversationID: strings.TrimSpace(view.ConversationID),
			MemberKind:     memberKind,
			MemberID:       id,
			DisplayName:    name,
			Role:           role,
			CreatedAt:      latest,
		}); err != nil {
			return result, err
		}
		existing[memberKind+":"+id] = true
		result.Added++
		result.IDs = append(result.IDs, id)
		result.Names = append(result.Names, name)
		result.Roles = append(result.Roles, role)
	}
	if result.Added > 0 {
		if err := s.convertDirectConversationToGroupIfNeeded(ctx, strings.TrimSpace(view.ConversationID), latest); err != nil {
			return result, err
		}
		s.writeConversationAuthorization(ctx, view.ConversationID)
	}
	return result, nil
}

func latestConversationMemberCreatedAt(members []ConversationMemberRecord) time.Time {
	var latest time.Time
	for _, member := range members {
		if member.CreatedAt.After(latest) {
			latest = member.CreatedAt
		}
	}
	return latest
}

func latestTime(values ...time.Time) time.Time {
	var latest time.Time
	for _, value := range values {
		if value.After(latest) {
			latest = value
		}
	}
	return latest
}

func conversationMemberAddAgentIDs(call ToolCallDTO) []string {
	raw := firstNonBlank(call.Arguments["agent_ids"], call.Arguments["agent_id"], call.Arguments["members"], call.Result["agent_ids"], call.Result["agent_id"], call.Result["members"])
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.HasPrefix(raw, "[") {
		var ids []string
		if err := json.Unmarshal([]byte(raw), &ids); err == nil {
			return nonEmptyStrings(ids)
		}
		var items []struct {
			AgentID string `json:"agent_id"`
			ID      string `json:"id"`
		}
		if err := json.Unmarshal([]byte(raw), &items); err == nil {
			out := make([]string, 0, len(items))
			for _, item := range items {
				out = append(out, firstNonBlank(item.AgentID, item.ID))
			}
			return nonEmptyStrings(out)
		}
	}
	return nonEmptyStrings(strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', '，', ';', '；', '\n', '\t':
			return true
		default:
			return false
		}
	}))
}

func conversationMemberAddCount(call ToolCallDTO) int {
	raw := firstNonBlank(call.Arguments["count"], call.Arguments["member_count"], call.Arguments["number"], call.Result["count"], call.Result["member_count"], call.Result["number"])
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func conversationMemberAddRole(requestedRole string, entry agentcore.AgentDirectoryEntry) string {
	return firstNonBlank(requestedRole, entry.RoleName, "参与者")
}

func findConversationMemberAddEntry(entries []agentcore.AgentDirectoryEntry, id string) (agentcore.AgentDirectoryEntry, bool) {
	id = strings.TrimSpace(id)
	for _, entry := range entries {
		if id != "" && entry.AgentProfileID.String() == id {
			return entry, true
		}
	}
	return agentcore.AgentDirectoryEntry{}, false
}

func conversationMemberAddEntryID(entry agentcore.AgentDirectoryEntry) string {
	return conversationDirectoryEntryMemberID(entry)
}

func conversationMemberAddEntryAvailable(entry agentcore.AgentDirectoryEntry) bool {
	return entry.Enabled && entry.AgentKind.IsValid()
}

func conversationMemberKindForDirectoryEntry(entry agentcore.AgentDirectoryEntry) string {
	switch entry.AgentKind {
	case agentcore.AgentKindAI:
		return "agent"
	case agentcore.AgentKindHuman:
		return "user"
	default:
		return ""
	}
}

func conversationDirectoryEntryWorkerInternalQA(entry agentcore.AgentDirectoryEntry) bool {
	return missionteam.IsWorkerInternalQAAgent(entry.AgentProfileID, entry.OrgIdentity.RoleRefs)
}

func latestAgentMessageAt(messages []MessageRecord, agentID string, at time.Time) (MessageRecord, bool) {
	agentID = strings.TrimSpace(agentID)
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if message.Role != "assistant" || strings.TrimSpace(message.AuthorID) != agentID {
			continue
		}
		if !at.IsZero() && !message.CreatedAt.Equal(at) {
			continue
		}
		return message, true
	}
	return MessageRecord{}, false
}

// discussionRunMessageAuthorID returns the identity persisted on the host
// message. PA-origin Mission hosts execute as the physical Lead but publish
// their reply under the user's logical personal-assistant identity.
func discussionAgentMentionTargets(message MessageRecord, members []ConversationMemberRecord) []ConversationMemberRecord {
	byID := map[string]ConversationMemberRecord{}
	for _, member := range members {
		if strings.TrimSpace(member.MemberKind) != "agent" {
			continue
		}
		id := strings.TrimSpace(member.MemberID)
		if id != "" {
			if conversationMemberIsWorkerInternalQA(member) {
				continue
			}
			byID[id] = member
		}
	}
	var out []ConversationMemberRecord
	for _, id := range discussionAgentMentionCandidateIDs(message) {
		if target, ok := byID[id]; ok {
			out = append(out, target)
		}
	}
	return out
}

// Candidates use the same routing rules as dispatch, without consulting storage.
// Persisted membership is still checked immediately before preparing real runs.
func discussionAgentMentionCandidateIDs(message MessageRecord) []string {
	var out []string
	seen := map[string]bool{}
	for _, mention := range message.Mentions {
		if strings.TrimSpace(mention.TargetKind) != "agent" {
			continue
		}
		id := strings.TrimSpace(mention.TargetID)
		if id == "" || id == strings.TrimSpace(message.AuthorID) || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(message.Mentions) > 0 {
		return out
	}
	if message.Role == "user" && message.RoutingSchemaVersion > 0 {
		return nil
	}
	// Legacy and provider-originated messages may still carry mention links in
	// text. New user commands use MessageRecord.Mentions and never depend on
	// parsing display content for routing.
	for _, mentionedID := range discussionAgentMentionIDs(message.Content) {
		id := strings.TrimSpace(mentionedID)
		if id == "" || id == strings.TrimSpace(message.AuthorID) || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func conversationMemberIsWorkerInternalQA(member ConversationMemberRecord) bool {
	return missionteam.IsWorkerInternalQARoleIdentifier(member.MemberID) || missionteam.IsWorkerInternalQARoleIdentifier(member.Role)
}

func messageAuthorIDs(messages []MessageDTO) map[string]bool {
	if len(messages) == 0 {
		return nil
	}
	out := map[string]bool{}
	for _, message := range messages {
		id := strings.TrimSpace(message.AuthorID)
		if id != "" {
			out[id] = true
		}
	}
	return out
}

func firstLinkedMission(missions []LinkedMissionDTO) LinkedMissionDTO {
	for _, mission := range missions {
		if strings.TrimSpace(mission.MissionID) != "" {
			return mission
		}
	}
	return LinkedMissionDTO{}
}
