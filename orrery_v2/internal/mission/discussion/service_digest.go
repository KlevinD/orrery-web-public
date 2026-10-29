package discussion

import (
	"context"
	"fmt"
	"sort"
	"time"

	"orrery_v2/internal/platform/uow"
)

func (s *Service) BuildDigest(ctx context.Context, groupID DiscussionGroupID, viewer ViewerRef) (DiscussionDigest, error) {
	var out DiscussionDigest
	err := s.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		group, err := s.repo.GetGroup(ctx, tx, groupID)
		if err != nil {
			return err
		}
		members, err := s.repo.ListMembers(ctx, tx, groupID)
		if err != nil {
			return err
		}
		if _, ok := findPostingMember(members, viewer); !ok && viewer.Kind != ViewerSystem {
			return fmt.Errorf("%w: viewer is not a discussion member", ErrPermissionDenied)
		}
		messages, err := s.repo.ListMessages(ctx, tx, groupID, 50)
		if err != nil {
			return err
		}
		inbox, err := s.repo.ListInbox(ctx, tx, viewer, InboxQuery{MissionID: group.MissionID})
		if err != nil {
			return err
		}
		id, err := s.newID("discussion-digest")
		if err != nil {
			return err
		}
		out = s.buildDigestFromRecords(DiscussionDigestID(id), group, members, messages, inbox)
		return nil
	})
	return out, err
}

func (s *Service) buildDigestFromRecords(id DiscussionDigestID, group DiscussionGroup, members []DiscussionMember, messages []Message, inbox []InboxItem) DiscussionDigest {
	var decisions []DigestDecision
	var blockers []DigestBlocker
	var questions []DigestQuestion
	var evidence []EvidenceIndexEntry
	sourceMessages := make([]string, 0, len(messages))
	sourceEvents := map[string]bool{}
	for _, msg := range messages {
		sourceMessages = append(sourceMessages, string(msg.ID))
		for _, ref := range msg.SourceRefs {
			if ref != "" {
				sourceEvents[ref] = true
			}
		}
		summary := sanitizedSummary(msg)
		switch msg.MessageKind {
		case MessageKindDecision:
			decisions = append(decisions, DigestDecision{MessageID: string(msg.ID), Summary: summary, SourceRefs: append([]string(nil), msg.SourceRefs...)})
		case MessageKindBlocker:
			blockers = append(blockers, DigestBlocker{MessageID: string(msg.ID), TaskID: msg.RelatedTaskID, Summary: summary})
		case MessageKindRequest, MessageKindClarification:
			questions = append(questions, DigestQuestion{MessageID: string(msg.ID), Summary: summary})
		}
		if msg.RelatedEvidenceID != "" {
			evidence = append(evidence, EvidenceIndexEntry{EvidenceID: msg.RelatedEvidenceID, MessageID: string(msg.ID), Summary: summary})
		}
	}
	responsibility, workerRefs := digestResponsibilities(members)
	pending := make([]PendingMentionDigest, 0, len(inbox))
	for _, item := range inbox {
		if item.Status.Closed() {
			continue
		}
		pending = append(pending, PendingMentionDigest{InboxItemID: item.ID, MessageID: item.MessageID, MentionID: item.MentionID, Recipient: item.Recipient, RequiredResponse: item.RequiredResponse, CreatedAt: item.CreatedAt})
	}
	now := s.now()
	return DiscussionDigest{
		ID:                  id,
		MissionID:           group.MissionID,
		DiscussionGroupID:   group.ID,
		MissionFinalGoal:    group.Goal,
		DiscussionGroupGoal: group.Goal,
		CurrentPhase:        "discussion",
		ActiveDecisions:     decisions,
		ResponsibilityMap:   responsibility,
		WorkerGoalRefs:      workerRefs,
		OpenQuestions:       questions,
		Blockers:            blockers,
		EvidenceIndex:       evidence,
		PendingMentions:     pending,
		RecentSummary:       buildRecentSummary(messages, decisions, blockers, pending),
		SourceMessageRefs:   sourceMessages,
		SourceEventRefs:     sortedStringSet(sourceEvents),
		Visibility:          MessageVisibility{Scope: VisibilityMission},
		CreatedAt:           now,
		ExpiresAt:           now.Add(30 * time.Minute),
	}
}

func digestResponsibilities(members []DiscussionMember) ([]ResponsibilityEntry, []WorkerGoalRef) {
	responsibility := make([]ResponsibilityEntry, 0, len(members))
	workerRefs := make([]WorkerGoalRef, 0, len(members))
	for _, member := range members {
		if !member.Active() {
			continue
		}
		role := string(member.MissionRole)
		responsibility = append(responsibility, ResponsibilityEntry{AgentInstanceID: member.AgentInstanceID, MissionAssignmentRef: string(member.MissionAssignmentRef), MissionRole: role})
		workerRefs = append(workerRefs, WorkerGoalRef{AgentInstanceID: member.AgentInstanceID, MissionAssignmentRef: string(member.MissionAssignmentRef), MissionRole: role, GoalRef: "mission_assignment:" + string(member.MissionAssignmentRef)})
	}
	return responsibility, workerRefs
}

func sortedStringSet(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
