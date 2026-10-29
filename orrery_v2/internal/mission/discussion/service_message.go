package discussion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/platform/uow"
)

func (s *Service) PostMessage(ctx context.Context, cmd PostMessageCommand) (Message, error) {
	start := s.now()
	var out Message
	err := s.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		msg, inboxItems, replay, err := s.buildPostMessage(ctx, tx, cmd)
		if err != nil {
			return err
		}
		if replay {
			out = msg
			return nil
		}
		if err := s.repo.SaveMessage(ctx, tx, msg); err != nil {
			return err
		}
		if msg.IdempotencyKey != "" {
			if err := s.repo.SaveMessageIdempotency(ctx, tx, msg.TenantID, msg.IdempotencyKey, msg.ID); err != nil {
				return err
			}
		}
		if err := s.events.Emit(ctx, tx, EventCandidate{Type: missionevent.EventMessagePosted, MissionID: msg.MissionID, DiscussionGroupID: msg.DiscussionGroupID, Message: msg, Actor: cmd.Author, IdempotencyKey: scopedKey(cmd.IdempotencyKey, string(msg.ID), "message_posted")}); err != nil {
			return err
		}
		if err := s.emitDerivedMessageEvents(ctx, tx, msg, cmd.Author, cmd.IdempotencyKey); err != nil {
			return err
		}
		for _, item := range inboxItems {
			if err := s.repo.SaveInboxItem(ctx, tx, item); err != nil {
				return err
			}
			if err := s.events.Emit(ctx, tx, EventCandidate{Type: missionevent.EventAgentMentioned, MissionID: msg.MissionID, DiscussionGroupID: msg.DiscussionGroupID, Message: msg, InboxItem: item, Reason: "mention", Actor: cmd.Author, IdempotencyKey: scopedKey(cmd.IdempotencyKey, string(item.ID), "agent_mentioned")}); err != nil {
				return err
			}
			if err := s.events.Emit(ctx, tx, EventCandidate{Type: missionevent.EventInboxItemCreated, MissionID: msg.MissionID, DiscussionGroupID: msg.DiscussionGroupID, Message: msg, InboxItem: item, Reason: "mention", Actor: cmd.Author, IdempotencyKey: scopedKey(cmd.IdempotencyKey, string(item.ID), "inbox_created")}); err != nil {
				return err
			}
			if err := s.dispatchMentionAfterCommit(tx, item); err != nil {
				return err
			}
		}
		out = msg
		return nil
	})
	s.observeMutation(ctx, "post_message", firstMissionID(out.MissionID, cmd.MissionID), firstGroupID(out.DiscussionGroupID, cmd.DiscussionGroupID), cmd.IdempotencyKey, start, err)
	return out, err
}

func (s *Service) dispatchMentionAfterCommit(tx uow.Tx, item InboxItem) error {
	if s == nil || s.dispatcher == nil {
		return nil
	}
	if item.Recipient.Kind != ViewerAgent || !item.RequiredResponse || item.Status != InboxPending {
		return nil
	}
	if tx == nil {
		return fmt.Errorf("%w: transaction is required for mention dispatch", ErrInvalidArgument)
	}
	tx.AfterCommit(func(ctx context.Context) error {
		// The inbox item is already durable. Allow enqueue after the HTTP request
		// ends, but never leave dispatch without its own deadline.
		dispatchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return s.dispatcher.DispatchMention(dispatchCtx, item)
	})
	return nil
}

func (s *Service) Reply(ctx context.Context, cmd ReplyCommand) (Message, error) {
	start := s.now()
	var out Message
	err := s.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		parent, err := s.repo.GetMessage(ctx, tx, cmd.ReplyToMessageID)
		if err != nil {
			return err
		}
		msg, inboxItems, replay, err := s.buildPostMessage(ctx, tx, PostMessageCommand{
			TenantID:          cmd.TenantID,
			MissionID:         cmd.MissionID,
			DiscussionGroupID: cmd.DiscussionGroupID,
			ReplyToMessageID:  cmd.ReplyToMessageID,
			Author:            cmd.Author,
			Content:           cmd.Content,
			Summary:           cmd.Summary,
			MessageKind:       MessageKindReply,
			Visibility:        MessageVisibility{Scope: VisibilityMission},
			IdempotencyKey:    cmd.IdempotencyKey,
		})
		if err != nil {
			return err
		}
		if replay {
			out = msg
			return nil
		}
		msg, err = BuildReplyMessage(parent, msg)
		if err != nil {
			return err
		}
		if err := ValidateMessage(msg); err != nil {
			return err
		}
		if err := s.repo.SaveMessage(ctx, tx, msg); err != nil {
			return err
		}
		if msg.IdempotencyKey != "" {
			if err := s.repo.SaveMessageIdempotency(ctx, tx, msg.TenantID, msg.IdempotencyKey, msg.ID); err != nil {
				return err
			}
		}
		if err := s.events.Emit(ctx, tx, EventCandidate{Type: missionevent.EventAgentReplied, MissionID: msg.MissionID, DiscussionGroupID: msg.DiscussionGroupID, Message: msg, Actor: cmd.Author, IdempotencyKey: scopedKey(cmd.IdempotencyKey, string(msg.ID), "agent_replied")}); err != nil {
			return err
		}
		for _, item := range inboxItems {
			if err := s.repo.SaveInboxItem(ctx, tx, item); err != nil {
				return err
			}
			if err := s.events.Emit(ctx, tx, EventCandidate{Type: missionevent.EventAgentMentioned, MissionID: msg.MissionID, DiscussionGroupID: msg.DiscussionGroupID, Message: msg, InboxItem: item, Reason: "mention", Actor: cmd.Author, IdempotencyKey: scopedKey(cmd.IdempotencyKey, string(item.ID), "agent_mentioned")}); err != nil {
				return err
			}
			if err := s.events.Emit(ctx, tx, EventCandidate{Type: missionevent.EventInboxItemCreated, MissionID: msg.MissionID, DiscussionGroupID: msg.DiscussionGroupID, Message: msg, InboxItem: item, Reason: "mention", Actor: cmd.Author, IdempotencyKey: scopedKey(cmd.IdempotencyKey, string(item.ID), "inbox_created")}); err != nil {
				return err
			}
			if err := s.dispatchMentionAfterCommit(tx, item); err != nil {
				return err
			}
		}
		if cmd.InboxItemID != "" {
			item, err := s.closeInboxWithReply(ctx, tx, cmd.InboxItemID, cmd.Author, msg.ID)
			if err != nil {
				return err
			}
			if err := s.events.Emit(ctx, tx, EventCandidate{Type: missionevent.EventInboxItemClosed, MissionID: msg.MissionID, DiscussionGroupID: msg.DiscussionGroupID, Message: msg, InboxItem: item, Reason: "responded", Actor: cmd.Author, IdempotencyKey: scopedKey(cmd.IdempotencyKey, string(item.ID), "inbox_closed")}); err != nil {
				return err
			}
		}
		out = msg
		return nil
	})
	s.observeMutation(ctx, "reply", firstMissionID(out.MissionID, cmd.MissionID), firstGroupID(out.DiscussionGroupID, cmd.DiscussionGroupID), cmd.IdempotencyKey, start, err)
	return out, err
}

func (s *Service) buildPostMessage(ctx context.Context, tx uow.Tx, cmd PostMessageCommand) (Message, []InboxItem, bool, error) {
	group, err := s.repo.GetGroup(ctx, tx, cmd.DiscussionGroupID)
	if err != nil {
		return Message{}, nil, false, err
	}
	if cmd.MissionID.IsZero() {
		cmd.MissionID = group.MissionID
	}
	if !group.MissionID.IsZero() && cmd.MissionID != group.MissionID {
		return Message{}, nil, false, fmt.Errorf("%w: command mission_id does not match discussion group", ErrInvalidArgument)
	}
	members, err := s.repo.ListMembers(ctx, tx, cmd.DiscussionGroupID)
	if err != nil {
		return Message{}, nil, false, err
	}
	if err := s.policy.CanPost(group, members, cmd.Author); err != nil {
		return Message{}, nil, false, err
	}
	parsed := s.parser.Parse(cmd.Content)
	if len(parsed.Targets) > 0 {
		if err := s.policy.CanMention(members, cmd.Author); err != nil {
			return Message{}, nil, false, err
		}
	}
	now := s.now()
	visibility := cmd.Visibility
	if visibility.Scope == "" {
		visibility.Scope = VisibilityMission
	}
	kind := cmd.MessageKind
	if kind == "" {
		kind = MessageKindRequest
	}
	tenantID := normalizeDiscussionTenantID(cmd.TenantID)
	idempotencyKey := strings.TrimSpace(cmd.IdempotencyKey)
	messageID := MessageID("")
	if idempotencyKey != "" {
		messageID = deterministicDiscussionMessageID(tenantID, cmd.MissionID, cmd.DiscussionGroupID, idempotencyKey)
	} else {
		id, err := s.newID("message")
		if err != nil {
			return Message{}, nil, false, err
		}
		messageID = MessageID(id)
	}
	msg := Message{
		ID:                messageID,
		TenantID:          tenantID,
		MissionID:         cmd.MissionID,
		DiscussionGroupID: cmd.DiscussionGroupID,
		Author:            authorRefFromViewer(members, cmd.Author),
		AuthorMissionRole: authorRole(members, cmd.Author),
		Content:           cmd.Content,
		Summary:           firstNonEmpty(cmd.Summary, cmd.Content),
		MessageKind:       kind,
		RelatedTaskID:     cmd.RelatedTaskID,
		RelatedEvidenceID: cmd.RelatedEvidenceID,
		Visibility:        visibility,
		SourceRefs:        append([]string(nil), cmd.SourceRefs...),
		IdempotencyKey:    idempotencyKey,
		CreatedAt:         now,
	}
	msg.ThreadID = ReplyThreadID(msg.ID)
	if cmd.ReplyToMessageID != "" {
		msg.ReplyToMessageID = cmd.ReplyToMessageID
	}
	if len(parsed.Targets) > 0 {
		cmd.RequiredResponse = true
	}
	msg.IdempotencyFingerprint = discussionMessageCommandFingerprint(msg, cmd.RequiredResponse, cmd.DueAt)
	if idempotencyKey != "" {
		boundID, found, err := s.repo.GetMessageIdempotency(ctx, tx, tenantID, idempotencyKey)
		if err != nil {
			return Message{}, nil, false, err
		}
		if found {
			existing, err := s.repo.GetMessage(ctx, tx, boundID)
			if err != nil {
				return Message{}, nil, false, fmt.Errorf("%w: idempotency record references unavailable message", ErrConflict)
			}
			if boundID != messageID || existing.IdempotencyFingerprint == "" || existing.IdempotencyFingerprint != msg.IdempotencyFingerprint {
				return Message{}, nil, false, fmt.Errorf("%w: idempotency key was already used for a different discussion message", ErrConflict)
			}
			return existing, nil, true, nil
		}
		existing, err := s.repo.GetMessage(ctx, tx, messageID)
		if err == nil {
			if existing.IdempotencyFingerprint == "" || existing.IdempotencyFingerprint != msg.IdempotencyFingerprint {
				return Message{}, nil, false, fmt.Errorf("%w: deterministic message id is already bound to different content", ErrConflict)
			}
			return existing, nil, true, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return Message{}, nil, false, err
		}
	}
	mentions, inboxItems, err := s.materializeMentions(parsed.Targets, msg, members, cmd, now)
	if err != nil {
		return Message{}, nil, false, err
	}
	msg.Mentions = mentions
	if err := ValidateMessage(msg); err != nil {
		return Message{}, nil, false, err
	}
	return msg, inboxItems, false, nil
}

func deterministicDiscussionMessageID(tenantID string, missionID missioncore.MissionID, groupID DiscussionGroupID, idempotencyKey string) MessageID {
	digest := sha256.Sum256([]byte(normalizeDiscussionTenantID(tenantID) + "\x00" + missionID.String() + "\x00" + string(groupID) + "\x00" + strings.TrimSpace(idempotencyKey)))
	return MessageID("message-idem-" + hex.EncodeToString(digest[:]))
}

func discussionMessageCommandFingerprint(msg Message, requiredResponse bool, dueAt time.Time) string {
	payload, _ := json.Marshal(struct {
		TenantID, MissionID, GroupID, ReplyTo, AuthorKind, AuthorID, Content, Summary, Kind, Visibility, RelatedTaskID, RelatedEvidenceID string
		VisibilityRefs, SourceRefs                                                                                                        []string
		RequiredResponse                                                                                                                  bool
		DueAt                                                                                                                             string
	}{
		TenantID: msg.TenantID, MissionID: msg.MissionID.String(), GroupID: string(msg.DiscussionGroupID), ReplyTo: string(msg.ReplyToMessageID),
		AuthorKind: string(msg.Author.Kind), AuthorID: msg.Author.StableID().String(), Content: msg.Content, Summary: msg.Summary,
		Kind: string(msg.MessageKind), Visibility: string(msg.Visibility.Scope), VisibilityRefs: append([]string(nil), msg.Visibility.Refs...),
		RelatedTaskID: msg.RelatedTaskID.String(), RelatedEvidenceID: msg.RelatedEvidenceID, SourceRefs: append([]string(nil), msg.SourceRefs...),
		RequiredResponse: requiredResponse, DueAt: dueAt.UTC().Format(time.RFC3339Nano),
	})
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func normalizeDiscussionTenantID(tenantID string) string {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return "global"
	}
	return tenantID
}

func (s *Service) emitDerivedMessageEvents(ctx context.Context, tx uow.Tx, msg Message, actor ViewerRef, key string) error {
	switch msg.MessageKind {
	case MessageKindDecision:
		if err := s.events.Emit(ctx, tx, EventCandidate{Type: missionevent.EventDecisionRecorded, MissionID: msg.MissionID, DiscussionGroupID: msg.DiscussionGroupID, Message: msg, Actor: actor, IdempotencyKey: scopedKey(key, string(msg.ID), "decision_recorded")}); err != nil {
			return err
		}
	case MessageKindBlocker:
		if err := s.events.Emit(ctx, tx, EventCandidate{Type: missionevent.EventBlockerRaised, MissionID: msg.MissionID, DiscussionGroupID: msg.DiscussionGroupID, Message: msg, Actor: actor, IdempotencyKey: scopedKey(key, string(msg.ID), "blocker_raised")}); err != nil {
			return err
		}
	}
	if _, ok := MemoryCandidateFromMessage(msg); ok {
		if err := s.events.Emit(ctx, tx, EventCandidate{Type: missionevent.EventMemoryCandidateCreated, MissionID: msg.MissionID, DiscussionGroupID: msg.DiscussionGroupID, Message: msg, Actor: actor, IdempotencyKey: scopedKey(key, string(msg.ID), "memory_candidate_created")}); err != nil {
			return err
		}
	}
	return nil
}
