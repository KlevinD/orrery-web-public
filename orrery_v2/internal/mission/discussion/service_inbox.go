package discussion

import (
	"context"
	"fmt"
	"time"

	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/mission/storage"
	"orrery_v2/internal/platform/uow"
)

func (s *Service) ListInbox(ctx context.Context, recipient ViewerRef, q InboxQuery) (InboxPage, error) {
	var out InboxPage
	err := s.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		items, err := s.repo.ListInbox(ctx, tx, recipient, q)
		if err != nil {
			return err
		}
		out.Items = items
		return nil
	})
	return out, err
}

func (s *Service) MarkRead(ctx context.Context, itemID InboxItemID, viewer ViewerRef) error {
	start := s.now()
	var item InboxItem
	err := s.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		item, err := s.repo.GetInboxItem(ctx, tx, itemID)
		if err != nil {
			return err
		}
		if item.Recipient != viewer {
			return fmt.Errorf("%w: inbox item belongs to another recipient", ErrPermissionDenied)
		}
		if item.Status == InboxPending {
			item.Status = InboxRead
		}
		item.NotificationState.Read = true
		return s.repo.SaveInboxItem(ctx, tx, item)
	})
	s.observeMutation(ctx, "mark_read", item.MissionID, item.DiscussionGroupID, string(itemID), start, err)
	return err
}

func (s *Service) CloseWithReply(ctx context.Context, itemID InboxItemID, viewer ViewerRef, reply MessageID) (InboxItem, error) {
	start := s.now()
	var item InboxItem
	err := s.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		var err error
		item, err = s.closeInboxWithReply(ctx, tx, itemID, viewer, reply)
		if err != nil {
			return err
		}
		return s.events.Emit(ctx, tx, EventCandidate{
			Type:              missionevent.EventInboxItemClosed,
			MissionID:         item.MissionID,
			DiscussionGroupID: item.DiscussionGroupID,
			InboxItem:         item,
			Reason:            "responded",
			Actor:             viewer,
			IdempotencyKey:    scopedKey(string(item.ID), string(reply), "inbox_closed"),
		})
	})
	s.observeMutation(ctx, "close_with_reply", item.MissionID, item.DiscussionGroupID, string(reply), start, err)
	return item, err
}

func (s *Service) Expire(ctx context.Context, now time.Time) error {
	return s.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		records, err := storage.Snapshot(ctx, tx, storage.TableDiscussionInboxItems)
		if err != nil {
			return err
		}
		for _, record := range records {
			var item InboxItem
			if err := decodeDiscussionPayload(record, &item); err != nil {
				return err
			}
			if item.Status.Closed() || item.DueAt.IsZero() || item.DueAt.After(now) {
				continue
			}
			item.Status = InboxExpired
			item.ClosedAt = now
			if err := s.repo.SaveInboxItem(ctx, tx, item); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Service) closeInboxWithReply(ctx context.Context, tx uow.Tx, id InboxItemID, viewer ViewerRef, reply MessageID) (InboxItem, error) {
	item, err := s.repo.GetInboxItem(ctx, tx, id)
	if err != nil {
		return InboxItem{}, err
	}
	if item.Recipient != viewer {
		return InboxItem{}, fmt.Errorf("%w: inbox item belongs to another recipient", ErrPermissionDenied)
	}
	item.Status = InboxResponded
	item.RespondedByMessageID = reply
	item.NotificationState.Read = true
	item.ClosedAt = s.now()
	return item, s.repo.SaveInboxItem(ctx, tx, item)
}
