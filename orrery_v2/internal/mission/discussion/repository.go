package discussion

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/mission/storage"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/uow"
)

const discussionInboxTable = storage.DiscussionInboxItems

type Repository interface {
	GetGroup(ctx context.Context, tx uow.Tx, id DiscussionGroupID) (DiscussionGroup, error)
	FindGroupByMission(ctx context.Context, tx uow.Tx, missionID missioncore.MissionID) (DiscussionGroup, bool, error)
	SaveGroup(ctx context.Context, tx uow.Tx, group DiscussionGroup) error
	SaveMember(ctx context.Context, tx uow.Tx, member DiscussionMember) error
	ListMembers(ctx context.Context, tx uow.Tx, groupID DiscussionGroupID) ([]DiscussionMember, error)
	GetMessage(ctx context.Context, tx uow.Tx, id MessageID) (Message, error)
	SaveMessage(ctx context.Context, tx uow.Tx, msg Message) error
	GetMessageIdempotency(ctx context.Context, tx uow.Tx, tenantID, idempotencyKey string) (MessageID, bool, error)
	SaveMessageIdempotency(ctx context.Context, tx uow.Tx, tenantID, idempotencyKey string, messageID MessageID) error
	ListMessages(ctx context.Context, tx uow.Tx, groupID DiscussionGroupID, limit int) ([]Message, error)
	SaveInboxItem(ctx context.Context, tx uow.Tx, item InboxItem) error
	GetInboxItem(ctx context.Context, tx uow.Tx, id InboxItemID) (InboxItem, error)
	ListInbox(ctx context.Context, tx uow.Tx, recipient ViewerRef, query InboxQuery) ([]InboxItem, error)
}

type PlatformRepository struct{}

func NewPlatformRepository() *PlatformRepository {
	return &PlatformRepository{}
}

func (r *PlatformRepository) GetGroup(ctx context.Context, tx uow.Tx, id DiscussionGroupID) (DiscussionGroup, error) {
	record, ok, err := storage.Get(ctx, tx, storage.TableDiscussionGroups, string(id))
	if err != nil {
		return DiscussionGroup{}, err
	}
	if !ok {
		return DiscussionGroup{}, fmt.Errorf("%w: group %s", ErrNotFound, id)
	}
	var out DiscussionGroup
	if err := decodeDiscussionPayload(record, &out); err != nil {
		return DiscussionGroup{}, err
	}
	return out, nil
}

func (r *PlatformRepository) FindGroupByMission(ctx context.Context, tx uow.Tx, missionID missioncore.MissionID) (DiscussionGroup, bool, error) {
	return r.FindGroupByMissionForTenant(ctx, tx, "global", missionID)
}

func (r *PlatformRepository) FindGroupByMissionForTenant(ctx context.Context, tx uow.Tx, tenantID string, missionID missioncore.MissionID) (DiscussionGroup, bool, error) {
	records, err := storage.Snapshot(ctx, tx, storage.TableDiscussionGroups)
	if err != nil {
		return DiscussionGroup{}, false, err
	}
	for _, record := range records {
		if got, _ := record["mission_id"].(string); got != missionID.String() {
			continue
		}
		gotTenant, _ := record["tenant_id"].(string)
		if normalizeDiscussionTenantID(gotTenant) != normalizeDiscussionTenantID(tenantID) {
			continue
		}
		var group DiscussionGroup
		if err := decodeDiscussionPayload(record, &group); err != nil {
			return DiscussionGroup{}, false, err
		}
		return group, true, nil
	}
	return DiscussionGroup{}, false, nil
}

func (r *PlatformRepository) SaveGroup(ctx context.Context, tx uow.Tx, group DiscussionGroup) error {
	payload, err := encodeDiscussionPayload(group)
	if err != nil {
		return err
	}
	return storage.Put(ctx, tx, storage.TableDiscussionGroups, string(group.ID), db.Record{
		"group_id":   string(group.ID),
		"tenant_id":  normalizeDiscussionTenantID(group.TenantID),
		"mission_id": group.MissionID.String(),
		"status":     string(group.Status),
		"payload":    payload,
	})
}

func (r *PlatformRepository) SaveMember(ctx context.Context, tx uow.Tx, member DiscussionMember) error {
	payload, err := encodeDiscussionPayload(member)
	if err != nil {
		return err
	}
	return storage.Put(ctx, tx, storage.TableDiscussionMembers, memberKey(member), db.Record{
		"group_id":          string(member.DiscussionGroupID),
		"agent_profile_id":  member.AgentProfileID.String(),
		"agent_instance_id": member.AgentInstanceID.String(),
		"user_id":           member.UserID,
		"member_ref":        memberRef(member),
		"payload":           payload,
	})
}

func (r *PlatformRepository) ListMembers(ctx context.Context, tx uow.Tx, groupID DiscussionGroupID) ([]DiscussionMember, error) {
	var out []DiscussionMember
	records, err := storage.Snapshot(ctx, tx, storage.TableDiscussionMembers)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if got, _ := record["group_id"].(string); got != string(groupID) {
			continue
		}
		var member DiscussionMember
		if err := decodeDiscussionPayload(record, &member); err != nil {
			return nil, err
		}
		out = append(out, member)
	}
	sort.Slice(out, func(i, j int) bool {
		return memberRef(out[i]) < memberRef(out[j])
	})
	return out, nil
}

func (r *PlatformRepository) GetMessage(ctx context.Context, tx uow.Tx, id MessageID) (Message, error) {
	record, ok, err := storage.Get(ctx, tx, storage.TableDiscussionMessages, string(id))
	if err != nil {
		return Message{}, err
	}
	if !ok {
		return Message{}, fmt.Errorf("%w: message %s", ErrNotFound, id)
	}
	var out Message
	if err := decodeDiscussionPayload(record, &out); err != nil {
		return Message{}, err
	}
	return out, nil
}

func (r *PlatformRepository) SaveMessage(ctx context.Context, tx uow.Tx, msg Message) error {
	payload, err := encodeDiscussionPayload(msg)
	if err != nil {
		return err
	}
	return storage.Put(ctx, tx, storage.TableDiscussionMessages, string(msg.ID), db.Record{
		"message_id": string(msg.ID),
		"group_id":   string(msg.DiscussionGroupID),
		"thread_id":  string(msg.ThreadID),
		"created_at": msg.CreatedAt.Format(timeFormatRFC3339Nano),
		"payload":    payload,
	})
}

func (r *PlatformRepository) GetMessageIdempotency(ctx context.Context, tx uow.Tx, tenantID, idempotencyKey string) (MessageID, bool, error) {
	tenantID = normalizeDiscussionTenantID(tenantID)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return "", false, nil
	}
	record, ok, err := storage.Get(ctx, tx, storage.TableDiscussionMessageIdempotency, discussionMessageIdempotencyRecordKey(tenantID, idempotencyKey))
	if err != nil || !ok {
		return "", false, err
	}
	storedTenant, _ := record["tenant_id"].(string)
	storedKey, _ := record["idempotency_key"].(string)
	messageID, _ := record["message_id"].(string)
	if normalizeDiscussionTenantID(storedTenant) != tenantID || storedKey != idempotencyKey || strings.TrimSpace(messageID) == "" {
		return "", false, fmt.Errorf("%w: discussion message idempotency record is invalid", ErrConflict)
	}
	return MessageID(messageID), true, nil
}

func (r *PlatformRepository) SaveMessageIdempotency(ctx context.Context, tx uow.Tx, tenantID, idempotencyKey string, messageID MessageID) error {
	tenantID = normalizeDiscussionTenantID(tenantID)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" || messageID == "" {
		return fmt.Errorf("%w: idempotency key and message id are required", ErrInvalidArgument)
	}
	recordKey := discussionMessageIdempotencyRecordKey(tenantID, idempotencyKey)
	record := db.Record{
		"tenant_id":       tenantID,
		"idempotency_key": idempotencyKey,
		"message_id":      string(messageID),
	}
	inserted, err := db.InsertRecordTx(ctx, tx, storage.TableDiscussionMessageIdempotency.LogicalName, recordKey, record)
	if err != nil {
		return err
	}
	if inserted {
		return nil
	}
	existing, found, err := db.GetRecordTx(ctx, tx, storage.TableDiscussionMessageIdempotency.LogicalName, recordKey)
	if err != nil {
		return err
	}
	storedTenant, _ := existing["tenant_id"].(string)
	storedKey, _ := existing["idempotency_key"].(string)
	storedMessageID, _ := existing["message_id"].(string)
	if !found || normalizeDiscussionTenantID(storedTenant) != tenantID || storedKey != idempotencyKey || storedMessageID != string(messageID) {
		return fmt.Errorf("%w: idempotency key was already bound to a different discussion message", ErrConflict)
	}
	return nil
}

func discussionMessageIdempotencyRecordKey(tenantID, idempotencyKey string) string {
	digest := sha256.Sum256([]byte(normalizeDiscussionTenantID(tenantID) + "\x00" + strings.TrimSpace(idempotencyKey)))
	return fmt.Sprintf("message-idempotency-%x", digest[:])
}

func (r *PlatformRepository) ListMessages(ctx context.Context, tx uow.Tx, groupID DiscussionGroupID, limit int) ([]Message, error) {
	var out []Message
	records, err := storage.Snapshot(ctx, tx, storage.TableDiscussionMessages)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if got, _ := record["group_id"].(string); got != string(groupID) {
			continue
		}
		var msg Message
		if err := decodeDiscussionPayload(record, &msg); err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func (r *PlatformRepository) SaveInboxItem(ctx context.Context, tx uow.Tx, item InboxItem) error {
	payload, err := encodeDiscussionPayload(item)
	if err != nil {
		return err
	}
	return storage.Put(ctx, tx, storage.TableDiscussionInboxItems, string(item.ID), db.Record{
		"inbox_item_id": string(item.ID),
		"mission_id":    item.MissionID.String(),
		"group_id":      string(item.DiscussionGroupID),
		"message_id":    string(item.MessageID),
		"recipient":     item.Recipient.key(),
		"status":        string(item.Status),
		"created_at":    item.CreatedAt.Format(timeFormatRFC3339Nano),
		"payload":       payload,
	})
}

func (r *PlatformRepository) GetInboxItem(ctx context.Context, tx uow.Tx, id InboxItemID) (InboxItem, error) {
	record, ok, err := storage.Get(ctx, tx, storage.TableDiscussionInboxItems, string(id))
	if err != nil {
		return InboxItem{}, err
	}
	if !ok {
		return InboxItem{}, fmt.Errorf("%w: inbox item %s", ErrNotFound, id)
	}
	var out InboxItem
	if err := decodeDiscussionPayload(record, &out); err != nil {
		return InboxItem{}, err
	}
	return out, nil
}

func (r *PlatformRepository) ListInbox(ctx context.Context, tx uow.Tx, recipient ViewerRef, query InboxQuery) ([]InboxItem, error) {
	var out []InboxItem
	records, err := storage.Snapshot(ctx, tx, storage.TableDiscussionInboxItems)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if got, _ := record["recipient"].(string); got != recipient.key() {
			continue
		}
		if query.MissionID != "" {
			if got, _ := record["mission_id"].(string); got != query.MissionID.String() {
				continue
			}
		}
		var item InboxItem
		if err := decodeDiscussionPayload(record, &item); err != nil {
			return nil, err
		}
		if item.Status.Closed() && !query.IncludeClosed {
			continue
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if query.Limit > 0 && len(out) > query.Limit {
		out = out[:query.Limit]
	}
	return out, nil
}

func memberKey(member DiscussionMember) string {
	return string(member.DiscussionGroupID) + "|" + memberRef(member)
}

func memberRef(member DiscussionMember) string {
	if member.UserID != "" && (member.MemberKind == DiscussionMemberHuman || member.AgentInstanceID == "") {
		return "user:" + member.UserID
	}
	if member.AgentProfileID != "" {
		return "agent:" + member.AgentProfileID.String()
	}
	return "unknown"
}

func (v ViewerRef) key() string {
	return string(v.Kind) + ":" + v.Ref
}

const timeFormatRFC3339Nano = "2006-01-02T15:04:05.999999999Z07:00"

func encodeDiscussionPayload(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode discussion payload: %w", err)
	}
	return string(payload), nil
}

func decodeDiscussionPayload(record db.Record, out any) error {
	raw, ok := record["payload"].(string)
	if !ok || raw == "" {
		return fmt.Errorf("discussion repository payload missing")
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return fmt.Errorf("decode discussion payload: %w", err)
	}
	return nil
}
