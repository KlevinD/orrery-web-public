package discussionsession

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func (s *Service) initialConversationMembers(ctx context.Context, conversation ConversationRecord, viewer ViewerRef, now time.Time) ([]ConversationMemberRecord, error) {
	if s == nil || s.agents == nil || s.humanProfiles == nil {
		return nil, fmt.Errorf("%w: personnel directory is required to create a discussion group", ErrConversationMember)
	}
	conversationID := strings.TrimSpace(conversation.ConversationID)
	if conversationID == "" || strings.TrimSpace(viewer.ViewerID) == "" {
		return nil, ErrInvalidRequest
	}

	members := make([]ConversationMemberRecord, 0, len(s.initialAgentMembers)+1)
	for i, seed := range s.initialAgentMembers {
		agentID := strings.TrimSpace(seed.AgentID)
		role := strings.TrimSpace(seed.Role)
		if agentID == "" || role == "" {
			return nil, ErrInvalidRequest
		}
		member, err := s.resolveAgentConversationMember(ctx, conversationID, agentID, role, now.Add(time.Duration(i)*time.Nanosecond))
		if err != nil {
			return nil, conversationMemberRoleError(err, role)
		}
		members = append(members, member)
	}
	if viewer.ViewerKind == ViewerKindSystem {
		return members, nil
	}

	creator, err := s.currentViewerConversationMember(ctx, conversationID, viewer, "话题发起人", now.Add(time.Duration(len(members))*time.Nanosecond))
	if err != nil {
		return nil, fmt.Errorf("%w: current viewer: %v", ErrConversationMember, err)
	}
	if conversationMemberName(members, creator.MemberKind, creator.MemberID) == "" {
		members = append(members, creator)
	}
	return members, nil
}

func (s *Service) initialConversationMembersFromCommand(ctx context.Context, conversation ConversationRecord, viewer ViewerRef, cmd SendMessageCommand, now time.Time) ([]ConversationMemberRecord, error) {
	return s.initialConversationMembersFromCommandWithPolicy(ctx, conversation, viewer, cmd, now, false)
}

func (s *Service) initialConversationMembersFromCommandWithPolicy(ctx context.Context, conversation ConversationRecord, viewer ViewerRef, cmd SendMessageCommand, now time.Time, allowWorkerInternalQA bool) ([]ConversationMemberRecord, error) {
	if len(nonEmptyStrings(cmd.InitialMemberIDs)) == 0 {
		return s.initialConversationMembers(ctx, conversation, viewer, now)
	}
	if s == nil || s.agents == nil || s.humanProfiles == nil {
		return nil, fmt.Errorf("%w: personnel directory is required to create a discussion group", ErrConversationMember)
	}
	conversationID := strings.TrimSpace(conversation.ConversationID)
	if conversationID == "" || strings.TrimSpace(viewer.ViewerID) == "" {
		return nil, ErrInvalidRequest
	}
	ids := nonEmptyStrings(cmd.InitialMemberIDs)
	members := make([]ConversationMemberRecord, 0, len(ids)+1)
	seen := map[string]bool{}
	for _, id := range ids {
		entry, err := s.findDirectoryEntry(ctx, id)
		if err != nil {
			return nil, err
		}
		memberKind := conversationMemberKindForDirectoryEntry(entry)
		if memberKind == "" {
			return nil, ErrInvalidRequest
		}
		memberID := conversationDirectoryEntryMemberID(entry)
		if memberID == "" {
			return nil, fmt.Errorf("%w: target %s has no stable profile id", ErrConversationMember, id)
		}
		key := memberKind + ":" + memberID
		if seen[key] {
			continue
		}
		if memberKind == "agent" && conversationDirectoryEntryWorkerInternalQA(entry) && !allowWorkerInternalQA {
			return nil, fmt.Errorf("%w: %w: agent %s cannot be directly invited into a discussion group", ErrConversationMember, ErrWorkerInternalQAMember, memberID)
		}
		name, err := strictConversationDisplayName(memberID, entry.DisplayName, entry.RealName)
		if err != nil {
			return nil, fmt.Errorf("%w: target %s has no real display name", ErrConversationMember, memberID)
		}
		members = append(members, ConversationMemberRecord{
			ConversationID: conversationID,
			MemberKind:     memberKind,
			MemberID:       memberID,
			DisplayName:    name,
			Role:           firstNonBlank(entry.RoleName, "参与者"),
			CreatedAt:      now.Add(time.Duration(len(members)) * time.Nanosecond),
		})
		seen[key] = true
	}
	if viewer.ViewerKind == ViewerKindSystem {
		return members, nil
	}

	creator, err := s.currentViewerConversationMember(ctx, conversationID, viewer, "话题发起人", now.Add(time.Duration(len(members))*time.Nanosecond))
	if err != nil {
		return nil, fmt.Errorf("%w: current viewer: %v", ErrConversationMember, err)
	}
	if !seen[creator.MemberKind+":"+creator.MemberID] {
		members = append(members, creator)
	}
	return members, nil
}

func (s *Service) newConversationWithMembers(ctx context.Context, conversationID string, viewer ViewerRef, cmd SendMessageCommand, now time.Time) (ConversationRecord, []ConversationMemberRecord, error) {
	conversationID = strings.TrimSpace(conversationID)
	conversationKind, ok := normalizeCreatableConversationKind(cmd.ConversationKind)
	if !ok {
		return ConversationRecord{}, nil, ErrInvalidRequest
	}
	target := directTargetFromCommand(cmd)
	if target.ID != "" {
		if conversationKind == ConversationKindPersonalAssistant {
			return ConversationRecord{}, nil, ErrInvalidRequest
		}
		return s.newDirectConversationWithMembers(ctx, conversationID, viewer, target, now)
	}
	title := trimmedDiscussionTitle(cmd.Content)
	hilMode := DiscussionHILModeManual
	if conversationKind == ConversationKindPersonalAssistant {
		allowMemberAdditions := false
		cmd.AllowMemberAdditions = &allowMemberAdditions
		title = "Personal Assistant"
		hilMode = DiscussionHILModeAuto
	}
	conversation := ConversationRecord{ConversationID: conversationID, TenantID: strings.TrimSpace(viewer.TenantID), ConversationKind: conversationKind, MemberAdditionsAllowed: cmd.AllowMemberAdditions, HILMode: hilMode, Title: title, Status: "running", CreatedBy: viewer.ViewerID, OwnerID: firstNonBlank(viewer.OwnerID, viewer.ViewerID), CreatedAt: now, UpdatedAt: now}
	if conversationKind == ConversationKindPersonalAssistant {
		displayName, err := s.configuredRunAgentDisplayNameForConversation(ctx, ConversationRecordView{ConversationRecord: conversation})
		if err != nil {
			return ConversationRecord{}, nil, err
		}
		conversation.Title = firstNonBlank(displayName, conversation.Title)
		members, err := s.personalAssistantConversationMembers(ctx, conversation, viewer, now)
		return conversation, members, err
	}
	members, err := s.initialConversationMembersFromCommand(ctx, conversation, viewer, cmd, now)
	return conversation, members, err
}

func (s *Service) personalAssistantConversationMembers(ctx context.Context, conversation ConversationRecord, viewer ViewerRef, now time.Time) ([]ConversationMemberRecord, error) {
	if s == nil || s.humanProfiles == nil || strings.TrimSpace(conversation.ConversationID) == "" || strings.TrimSpace(viewer.ViewerID) == "" {
		return nil, fmt.Errorf("%w: current user profile is required for the system assistant", ErrConversationMember)
	}
	if viewer.ViewerKind == ViewerKindSystem {
		return nil, nil
	}
	owner, err := s.currentViewerConversationMember(ctx, conversation.ConversationID, viewer, "Owner", now)
	if err != nil {
		return nil, fmt.Errorf("%w: current viewer: %v", ErrConversationMember, err)
	}
	return []ConversationMemberRecord{owner}, nil
}

func (s *Service) newDirectConversationWithMembers(ctx context.Context, conversationID string, viewer ViewerRef, targetRef directConversationTargetRef, now time.Time) (ConversationRecord, []ConversationMemberRecord, error) {
	if s == nil || s.agents == nil || s.humanProfiles == nil {
		return ConversationRecord{}, nil, fmt.Errorf("%w: personnel directory is required to create a direct discussion", ErrConversationMember)
	}
	conversationID = strings.TrimSpace(conversationID)
	targetRef.Kind = normalizeDirectTargetKind(targetRef.Kind)
	targetRef.ID = strings.TrimSpace(targetRef.ID)
	if conversationID == "" || targetRef.ID == "" || strings.TrimSpace(viewer.ViewerID) == "" {
		return ConversationRecord{}, nil, ErrInvalidRequest
	}
	if targetRef.Kind == "" {
		resolved, err := s.resolveDirectConversationTargetRef(ctx, targetRef.ID)
		if err != nil {
			return ConversationRecord{}, nil, err
		}
		targetRef = resolved
	}
	if directTargetMatchesCurrentViewer(targetRef, viewer) {
		return ConversationRecord{}, nil, fmt.Errorf("%w: direct chat target cannot be the current user", ErrInvalidRequest)
	}
	target, err := s.resolveDirectConversationTargetMember(ctx, conversationID, targetRef, "私聊对象", now)
	if err != nil {
		return ConversationRecord{}, nil, err
	}
	conversation := ConversationRecord{
		ConversationID:   conversationID,
		TenantID:         strings.TrimSpace(viewer.TenantID),
		ConversationKind: "direct",
		DirectAgentID:    directAgentIDForTarget(targetRef.Kind, target.MemberID),
		DirectTargetKind: targetRef.Kind,
		DirectTargetID:   target.MemberID,
		HILMode:          DiscussionHILModeManual,
		Title:            target.DisplayName,
		Status:           "running",
		CreatedBy:        viewer.ViewerID,
		OwnerID:          firstNonBlank(viewer.OwnerID, viewer.ViewerID),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if viewer.ViewerKind == ViewerKindSystem {
		conversation.CreatedByName = firstNonBlank(strings.TrimSpace(viewer.DisplayName), strings.TrimSpace(viewer.ViewerID))
		return conversation, []ConversationMemberRecord{target}, nil
	}
	creator, err := s.currentViewerConversationMember(ctx, conversationID, viewer, "话题发起人", now.Add(time.Nanosecond))
	if err != nil {
		return ConversationRecord{}, nil, fmt.Errorf("%w: current viewer: %v", ErrConversationMember, err)
	}
	conversation.CreatedByName = creator.DisplayName
	return conversation, []ConversationMemberRecord{target, creator}, nil
}

func (s *Service) resolveDirectConversationTargetMember(ctx context.Context, conversationID string, target directConversationTargetRef, role string, createdAt time.Time) (ConversationMemberRecord, error) {
	switch normalizeDirectTargetKind(target.Kind) {
	case directTargetKindAgent:
		return s.resolveAgentConversationMember(ctx, conversationID, target.ID, role, createdAt)
	case directTargetKindUser:
		return s.resolveHumanConversationMember(ctx, conversationID, target.ID, role, createdAt)
	default:
		return ConversationMemberRecord{}, ErrInvalidRequest
	}
}

func (s *Service) resolveDirectConversationTargetRef(ctx context.Context, targetID string) (directConversationTargetRef, error) {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return directConversationTargetRef{}, ErrInvalidRequest
	}
	entry, err := s.findDirectoryEntry(ctx, targetID)
	if err != nil {
		return directConversationTargetRef{}, err
	}
	id := conversationDirectoryEntryMemberID(entry)
	if id == "" {
		return directConversationTargetRef{}, fmt.Errorf("%w: target %s has no stable profile id", ErrConversationMember, targetID)
	}
	switch entry.AgentKind {
	case agentcore.AgentKindAI:
		return directConversationTargetRef{Kind: directTargetKindAgent, ID: id}, nil
	case agentcore.AgentKindHuman:
		return directConversationTargetRef{Kind: directTargetKindUser, ID: id}, nil
	default:
		return directConversationTargetRef{}, ErrInvalidRequest
	}
}

func (s *Service) rejectCurrentViewerDirectTarget(_ context.Context, viewer ViewerRef, target directConversationTargetRef) error {
	if normalizeDirectTargetKind(target.Kind) != conversationViewerMemberKind(viewer.ViewerKind) {
		return nil
	}
	if strings.TrimSpace(target.ID) == "" || strings.TrimSpace(viewer.ViewerID) == "" {
		return ErrInvalidRequest
	}
	if directTargetMatchesCurrentViewer(target, viewer) {
		return fmt.Errorf("%w: direct chat target cannot be the current user", ErrInvalidRequest)
	}
	return nil
}

func directTargetMatchesCurrentViewer(target directConversationTargetRef, viewer ViewerRef) bool {
	targetKind := normalizeDirectTargetKind(target.Kind)
	viewerKind := conversationViewerMemberKind(viewer.ViewerKind)
	if targetKind != viewerKind {
		return false
	}
	targetID := strings.TrimSpace(target.ID)
	if targetID == "" {
		return false
	}
	if targetID == strings.TrimSpace(viewer.ViewerID) {
		return true
	}
	return targetKind == directTargetKindUser && targetID == strings.TrimSpace(viewer.AgentProfileID)
}

func directAgentIDForTarget(kind string, id string) string {
	if normalizeDirectTargetKind(kind) == directTargetKindAgent {
		return strings.TrimSpace(id)
	}
	return ""
}

func trimmedDiscussionTitle(content string) string {
	title := strings.TrimSpace(content)
	if len(title) > 80 {
		title = title[:80]
	}
	return title
}

func (s *Service) validateInitialAgentMembers(ctx context.Context, cmd SendMessageCommand) error {
	if strings.TrimSpace(cmd.ConversationID) != "" {
		return nil
	}
	conversationID := strings.TrimSpace(cmd.IdempotencyKey)
	if conversationID == "" {
		conversationID = "runtime-unavailable-preflight"
	}
	for i, seed := range s.initialAgentMembers {
		agentID := strings.TrimSpace(seed.AgentID)
		role := strings.TrimSpace(seed.Role)
		if agentID == "" || role == "" {
			return ErrInvalidRequest
		}
		if _, err := s.resolveAgentConversationMember(ctx, conversationID, agentID, role, s.now().Add(time.Duration(i)*time.Nanosecond)); err != nil {
			return conversationMemberRoleError(err, role)
		}
	}
	return nil
}

func conversationMemberRoleError(err error, role string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrWorkerInternalQAMember) {
		return err
	}
	role = strings.TrimSpace(role)
	if role == "" || !errors.Is(err, ErrConversationMember) {
		return err
	}
	return fmt.Errorf("%w: no available agent for discussion role %s in personnel directory", ErrConversationMember, role)
}

func (s *Service) resolveAgentConversationMember(ctx context.Context, conversationID string, agentID string, role string, createdAt time.Time) (ConversationMemberRecord, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return ConversationMemberRecord{}, ErrInvalidRequest
	}
	entry, err := s.findDirectoryAgent(ctx, agentID)
	if err != nil {
		return ConversationMemberRecord{}, err
	}
	if conversationDirectoryEntryWorkerInternalQA(entry) {
		return ConversationMemberRecord{}, fmt.Errorf("%w: %w: agent %s cannot be directly invited into a discussion group", ErrConversationMember, ErrWorkerInternalQAMember, conversationMemberAddEntryID(entry))
	}
	id := conversationDirectoryEntryMemberID(entry)
	if id == "" {
		return ConversationMemberRecord{}, fmt.Errorf("%w: agent %s has no stable profile id", ErrConversationMember, agentID)
	}
	name, err := strictConversationDisplayName(id, entry.DisplayName, entry.RealName)
	if err != nil {
		return ConversationMemberRecord{}, fmt.Errorf("%w: agent %s has no real display name", ErrConversationMember, id)
	}
	return ConversationMemberRecord{
		ConversationID: strings.TrimSpace(conversationID),
		MemberKind:     "agent",
		MemberID:       id,
		DisplayName:    name,
		Role:           strings.TrimSpace(role),
		CreatedAt:      createdAt,
	}, nil
}

func (s *Service) resolveHumanConversationMember(ctx context.Context, conversationID string, humanID string, role string, createdAt time.Time) (ConversationMemberRecord, error) {
	humanID = strings.TrimSpace(humanID)
	if humanID == "" {
		return ConversationMemberRecord{}, ErrInvalidRequest
	}
	entry, err := s.findDirectoryHuman(ctx, humanID)
	if err != nil {
		return ConversationMemberRecord{}, err
	}
	id := conversationDirectoryEntryMemberID(entry)
	if id == "" {
		return ConversationMemberRecord{}, fmt.Errorf("%w: human %s has no stable profile id", ErrConversationMember, humanID)
	}
	name, err := strictConversationDisplayName(id, entry.DisplayName, entry.RealName)
	if err != nil {
		return ConversationMemberRecord{}, fmt.Errorf("%w: human %s has no real display name", ErrConversationMember, id)
	}
	return ConversationMemberRecord{
		ConversationID: strings.TrimSpace(conversationID),
		MemberKind:     "user",
		MemberID:       id,
		DisplayName:    name,
		Role:           strings.TrimSpace(role),
		CreatedAt:      createdAt,
	}, nil
}

func (s *Service) findDirectoryAgent(ctx context.Context, agentID string) (agentcore.AgentDirectoryEntry, error) {
	entry, err := s.findDirectoryEntry(ctx, agentID)
	if err != nil {
		return agentcore.AgentDirectoryEntry{}, err
	}
	if directoryEntryMatchesAgent(entry, agentID) {
		return entry, nil
	}
	return agentcore.AgentDirectoryEntry{}, fmt.Errorf("%w: agent %s missing from personnel directory", ErrConversationMember, agentID)
}

func (s *Service) findDirectoryHuman(ctx context.Context, humanID string) (agentcore.AgentDirectoryEntry, error) {
	entry, err := s.findDirectoryEntry(ctx, humanID)
	if err != nil {
		return agentcore.AgentDirectoryEntry{}, err
	}
	if directoryEntryMatchesHuman(entry, humanID) {
		return entry, nil
	}
	return agentcore.AgentDirectoryEntry{}, fmt.Errorf("%w: human %s missing from personnel directory", ErrConversationMember, humanID)
}

func (s *Service) findDirectoryEntry(ctx context.Context, targetID string) (agentcore.AgentDirectoryEntry, error) {
	if s == nil || s.agents == nil {
		return agentcore.AgentDirectoryEntry{}, fmt.Errorf("%w: personnel directory is unavailable", ErrConversationMember)
	}
	page, err := s.agents.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{EnabledOnly: true, IncludeAI: true, IncludeHumans: true})
	if err != nil {
		return agentcore.AgentDirectoryEntry{}, fmt.Errorf("%w: query personnel directory: %v", ErrConversationMember, err)
	}
	for _, entry := range page.Entries {
		if directoryEntryMatchesProfile(entry, targetID) {
			return entry, nil
		}
	}
	return agentcore.AgentDirectoryEntry{}, fmt.Errorf("%w: target %s missing from personnel directory", ErrConversationMember, targetID)
}

func directoryEntryMatchesAgent(entry agentcore.AgentDirectoryEntry, agentID string) bool {
	agentID = strings.TrimSpace(agentID)
	return agentID != "" && entry.AgentKind == agentcore.AgentKindAI && directoryEntryMatchesProfile(entry, agentID)
}

func directoryEntryMatchesHuman(entry agentcore.AgentDirectoryEntry, humanID string) bool {
	humanID = strings.TrimSpace(humanID)
	return humanID != "" && entry.AgentKind == agentcore.AgentKindHuman && directoryEntryMatchesProfile(entry, humanID)
}

func directoryEntryMatchesProfile(entry agentcore.AgentDirectoryEntry, targetID string) bool {
	targetID = strings.TrimSpace(targetID)
	return targetID != "" && entry.AgentProfileID.String() == targetID
}

func conversationDirectoryEntryMemberID(entry agentcore.AgentDirectoryEntry) string {
	return strings.TrimSpace(entry.AgentProfileID.String())
}

func strictConversationDisplayName(id string, values ...string) (string, error) {
	id = strings.TrimSpace(id)
	for _, value := range values {
		name := strings.TrimSpace(value)
		if name == "" {
			continue
		}
		return name, nil
	}
	return "", ErrConversationMember
}

func (s *Service) saveConversationMembers(ctx context.Context, members []ConversationMemberRecord) error {
	if s == nil || s.repo == nil {
		return ErrServiceDependency
	}
	conversationIDs := make([]string, 0, len(members))
	seenConversationIDs := make(map[string]bool, len(members))
	for _, member := range members {
		if err := s.repo.SaveMember(ctx, member); err != nil {
			return err
		}
		conversationID := strings.TrimSpace(member.ConversationID)
		if conversationID != "" && !seenConversationIDs[conversationID] {
			conversationIDs = append(conversationIDs, conversationID)
			seenConversationIDs[conversationID] = true
		}
	}
	for _, conversationID := range conversationIDs {
		s.writeConversationAuthorization(ctx, conversationID)
	}
	return nil
}

func conversationViewerMemberKind(kind ViewerKind) string {
	if kind == ViewerKindAgent {
		return "agent"
	}
	return "user"
}

func conversationMemberName(records []ConversationMemberRecord, kind string, id string) string {
	kind = strings.TrimSpace(kind)
	id = strings.TrimSpace(id)
	for _, record := range records {
		if strings.TrimSpace(record.MemberKind) == kind && strings.TrimSpace(record.MemberID) == id {
			return strings.TrimSpace(record.DisplayName)
		}
	}
	return ""
}

func requiredConversationMemberName(records []ConversationMemberRecord, kind string, id string) (string, error) {
	if name := conversationMemberName(records, kind, id); name != "" {
		return name, nil
	}
	return "", fmt.Errorf("%w: %s %s is not a persisted discussion member", ErrConversationMember, kind, id)
}

func requiredConversationViewerName(records []ConversationMemberRecord, viewer ViewerRef) (string, error) {
	if viewer.ViewerKind == ViewerKindSystem {
		if name := firstNonBlank(strings.TrimSpace(viewer.DisplayName), strings.TrimSpace(viewer.ViewerID)); name != "" {
			return name, nil
		}
		return "", fmt.Errorf("%w: system viewer identity is required", ErrConversationMember)
	}
	return requiredConversationMemberName(records, conversationViewerMemberKind(viewer.ViewerKind), viewer.ViewerID)
}

func conversationRunAgentName(view ConversationRecordView, run RunRecord) string {
	if isPersonalAssistantConversationKind(view.ConversationKind) || isPersonalAssistantRuntimeRun(run) {
		if isPersonalAssistantConversationKind(view.ConversationKind) {
			return firstNonBlank(view.Title, "Personal Assistant")
		}
		return "Personal Assistant"
	}
	if name := conversationMemberName(view.Members, "agent", run.AgentID); name != "" {
		return name
	}
	return ""
}

func requiredConversationRunAgentName(view ConversationRecordView, run RunRecord) (string, error) {
	if name := conversationRunAgentName(view, run); name != "" {
		return name, nil
	}
	return requiredConversationMemberName(view.Members, "agent", run.AgentID)
}

func requiredConversationRunMessageAuthor(view ConversationRecordView, run RunRecord) (string, string, error) {
	if isPersonalAssistantConversationKind(view.ConversationKind) {
		authorID := PersonalAssistantAgentID(view.TenantID, firstNonBlank(view.OwnerID, view.CreatedBy))
		if authorID == "" {
			authorID = strings.TrimSpace(run.LogicalAgentID)
		}
		if !IsPersonalAssistantAgentID(authorID) {
			return "", "", fmt.Errorf("%w: personal assistant identity is required", ErrConversationMember)
		}
		return authorID, conversationRunAgentName(view, run), nil
	}
	if isPersonalAssistantRuntimeRun(run) {
		authorID := strings.TrimSpace(run.LogicalAgentID)
		if !IsPersonalAssistantAgentID(authorID) {
			return "", "", fmt.Errorf("%w: personal assistant identity is required", ErrConversationMember)
		}
		return authorID, conversationRunAgentName(view, run), nil
	}

	authorID := strings.TrimSpace(run.AgentID)
	if authorID == "" {
		return "", "", fmt.Errorf("%w: runtime agent identity is required", ErrConversationMember)
	}
	authorName, err := requiredConversationRunAgentName(view, run)
	if err != nil {
		return "", "", err
	}
	return authorID, authorName, nil
}

func conversationAssistantReplyContent(view ConversationRecordView, run RunRecord, content string) string {
	if isPersonalAssistantConversationKind(view.ConversationKind) || isPersonalAssistantRuntimeRun(run) {
		return content
	}
	return discussionReplyWithMentionFallback(content, runHumanViewer(view.Messages, run))
}

func conversationMemberAdditionsAllowed(record ConversationRecord) bool {
	return record.MemberAdditionsAllowed == nil || *record.MemberAdditionsAllowed
}

func ensureConversationMemberAdditionsAllowed(record ConversationRecord) error {
	if conversationMemberAdditionsAllowed(record) {
		return nil
	}
	return fmt.Errorf("%w: adding members is disabled for this discussion", ErrForbidden)
}
