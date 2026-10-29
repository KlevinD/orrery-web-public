package memorydistill

import (
	"context"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	mempageindex "orrery_v2/internal/memory/pageindex"
)

func (s *Service) recallSourceRecords(ctx context.Context, viewer ViewerRef, candidates []memcore.MemoryRecord, query string, limit int) ([]memcore.MemoryRecord, error) {
	if s == nil || s.recall == nil || len(candidates) == 0 {
		return candidates, nil
	}
	profileRefs := make([]agentcore.MemoryProfileRef, 0, len(candidates))
	seenProfiles := map[agentcore.MemoryProfileID]struct{}{}
	subject := memcore.AccessSubject{
		AgentID:         agentcore.AgentID(strings.TrimSpace(viewer.AgentID)),
		OrganizationIDs: append([]string(nil), viewer.OrganizationIDs...),
		DepartmentIDs:   append([]string(nil), viewer.DepartmentIDs...),
		ProjectIDs:      append([]string(nil), viewer.ProjectIDs...),
		MissionIDs:      append([]string(nil), viewer.MissionIDs...),
		DiscussionIDs:   append([]string(nil), viewer.DiscussionIDs...),
	}
	allowedIDs := make(map[string]struct{}, len(candidates))
	for _, record := range candidates {
		allowedIDs[record.ID] = struct{}{}
		if _, exists := seenProfiles[record.ProfileID]; !exists && record.ProfileID != "" {
			seenProfiles[record.ProfileID] = struct{}{}
			profileRefs = append(profileRefs, agentcore.MemoryProfileRef{MemoryProfileID: record.ProfileID})
		}
		addScopeToAccessSubject(&subject, record.Scope)
	}
	if len(profileRefs) == 0 {
		return nil, nil
	}
	recallLimit := limit * 3
	if recallLimit < 20 {
		recallLimit = 20
	}
	if recallLimit > 100 {
		recallLimit = 100
	}
	recalled, err := s.recall.Recall(ctx, mempageindex.RecallRequest{
		Query:       query,
		Subject:     subject,
		ProfileRefs: profileRefs,
		Limit:       recallLimit,
	})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		// Semantic ranking improves source selection, but a provider outage must not
		// block distillation of candidates that already passed access checks.
		return cloneMemoryRecords(candidates), nil
	}
	out := make([]memcore.MemoryRecord, 0, len(recalled))
	seen := map[string]struct{}{}
	for _, record := range recalled {
		if _, ok := allowedIDs[record.ID]; !ok {
			continue
		}
		if _, ok := seen[record.ID]; ok {
			continue
		}
		seen[record.ID] = struct{}{}
		out = append(out, record.Clone())
	}
	return out, nil
}

func addScopeToAccessSubject(subject *memcore.AccessSubject, scope memcore.MemoryScope) {
	if subject == nil || strings.TrimSpace(scope.ID) == "" {
		return
	}
	switch scope.Kind {
	case memcore.ScopeOrganization:
		subject.OrganizationIDs = appendUniqueText(subject.OrganizationIDs, scope.ID)
	case memcore.ScopeDepartment:
		subject.DepartmentIDs = appendUniqueText(subject.DepartmentIDs, scope.ID)
	case memcore.ScopeProject:
		subject.ProjectIDs = appendUniqueText(subject.ProjectIDs, scope.ID)
	case memcore.ScopeMission:
		subject.MissionIDs = appendUniqueText(subject.MissionIDs, scope.ID)
	case memcore.ScopeDiscussion:
		subject.DiscussionIDs = appendUniqueText(subject.DiscussionIDs, scope.ID)
	case memcore.ScopeAgent:
		subject.AgentID = agentcore.AgentID(scope.ID)
	}
}

func appendUniqueText(values []string, value string) []string {
	value = strings.TrimSpace(value)
	for _, existing := range values {
		if strings.TrimSpace(existing) == value {
			return values
		}
	}
	return append(values, value)
}

func experienceRecallQuery(topic string) string {
	return strings.TrimSpace(topic) + " reusable work outcomes, decisions, experience, and lessons"
}

func worldRecallQuery(topic string) string {
	return strings.TrimSpace(topic) + " durable organization facts, rules, constraints, and consensus"
}

func cloneMemoryRecords(records []memcore.MemoryRecord) []memcore.MemoryRecord {
	out := make([]memcore.MemoryRecord, 0, len(records))
	for _, record := range records {
		out = append(out, record.Clone())
	}
	return out
}

func recordsWithIDs(records []memcore.MemoryRecord, ids []string) []memcore.MemoryRecord {
	byID := make(map[string]memcore.MemoryRecord, len(records))
	for _, record := range records {
		byID[record.ID] = record
	}
	out := make([]memcore.MemoryRecord, 0, len(ids))
	for _, id := range ids {
		if record, ok := byID[id]; ok {
			out = append(out, record.Clone())
		}
	}
	return out
}
