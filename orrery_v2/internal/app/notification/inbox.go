package notification

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

func (s *Service) Item(ctx context.Context, v Viewer, id string) (Item, error) {
	if err := s.human(ctx, v); err != nil {
		return Item{}, err
	}
	var out Item
	var raw string
	var read int64
	err := s.store.QueryRow(ctx, `SELECT i.id,e.payload,i.read_at,i.reason FROM notification_inbox i JOIN notification_events e ON e.id=i.event_id WHERE i.id=? AND i.tenant_id=? AND i.human_id=?`, id, v.TenantID, v.HumanID).Scan(&out.ID, &raw, &read, &out.Reason)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal([]byte(raw), &out.Event); err != nil {
		return out, err
	}
	source, err := s.loadSource(ctx, out.Event.SourceID)
	if err != nil {
		return out, err
	}
	if source.TenantID != v.TenantID || !eligible(source, v.HumanID) {
		return Item{}, ErrNotFound
	}
	out.Read = read != 0
	out.Pushes, err = s.pushes(ctx, id)
	return out, err
}
func (s *Service) pushes(ctx context.Context, id string) ([]Push, error) {
	rows, err := s.store.Query(ctx, "SELECT channel,status,last_error FROM notification_pushes WHERE inbox_id=? ORDER BY channel", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Push{}
	for rows.Next() {
		var p Push
		if err = rows.Scan(&p.Channel, &p.Status, &p.Error); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Service) Inbox(ctx context.Context, v Viewer, unread bool, limit, offset int) (Page, error) {
	out := Page{Items: []Item{}, Limit: limit, Offset: offset}
	if err := s.human(ctx, v); err != nil {
		return out, err
	}
	if limit < 1 || limit > 100 || offset < 0 || offset > 100000 {
		return out, ErrInvalid
	}
	// Resolve current sharing rules once, then paginate in SQL. Old results remain
	// readable after unsubscribing, but removing someone from a private audience
	// revokes that result grant immediately.
	rows, err := s.store.Query(ctx, "SELECT payload FROM notification_sources WHERE tenant_id=?", v.TenantID)
	if err != nil {
		return out, err
	}
	marks := []string{}
	args := []any{v.TenantID, v.HumanID}
	for rows.Next() {
		var raw string
		var source Source
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return out, err
		}
		if err = json.Unmarshal([]byte(raw), &source); err != nil {
			rows.Close()
			return out, err
		}
		if eligible(source, v.HumanID) {
			marks = append(marks, "?")
			args = append(args, source.ID)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(marks) == 0 {
		return out, nil
	}
	where := "i.tenant_id=? AND i.human_id=? AND i.source_id IN (" + strings.Join(marks, ",") + ")"
	err = s.store.QueryRow(ctx, "SELECT COUNT(*),COALESCE(SUM(CASE WHEN i.read_at=0 THEN 1 ELSE 0 END),0) FROM notification_inbox i WHERE "+where, args...).Scan(&out.Total, &out.Unread)
	if err != nil {
		return out, err
	}
	if unread {
		where += " AND i.read_at=0"
		out.Total = out.Unread
	}
	pageArgs := append(append([]any{}, args...), limit, offset)
	rows, err = s.store.Query(ctx, "SELECT i.id,e.payload,i.read_at,i.reason FROM notification_inbox i JOIN notification_events e ON e.id=i.event_id WHERE "+where+" ORDER BY i.created_at DESC,i.id DESC LIMIT ? OFFSET ?", pageArgs...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var item Item
		var raw string
		var read int64
		if err = rows.Scan(&item.ID, &raw, &read, &item.Reason); err != nil {
			rows.Close()
			return out, err
		}
		if err = json.Unmarshal([]byte(raw), &item.Event); err != nil {
			rows.Close()
			return out, err
		}
		item.Read = read != 0
		item.Pushes = []Push{}
		out.Items = append(out.Items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(out.Items) == 0 {
		return out, err
	}
	marks = nil
	args = nil
	indices := map[string]int{}
	for i, item := range out.Items {
		marks = append(marks, "?")
		args = append(args, item.ID)
		indices[item.ID] = i
	}
	rows, err = s.store.Query(ctx, "SELECT inbox_id,channel,status,last_error FROM notification_pushes WHERE inbox_id IN ("+strings.Join(marks, ",")+") ORDER BY channel", args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var p Push
		if err = rows.Scan(&id, &p.Channel, &p.Status, &p.Error); err != nil {
			return out, err
		}
		i := indices[id]
		out.Items[i].Pushes = append(out.Items[i].Pushes, p)
	}
	return out, rows.Err()
}
func (s *Service) MarkRead(ctx context.Context, v Viewer, id string, read bool) error {
	if _, err := s.Item(ctx, v, id); err != nil {
		return err
	}
	var at int64
	if read {
		at = s.now().UnixNano()
	}
	_, err := s.store.Exec(ctx, "UPDATE notification_inbox SET read_at=? WHERE id=? AND tenant_id=? AND human_id=?", at, id, v.TenantID, v.HumanID)
	return err
}
func (s *Service) Receipts(ctx context.Context, v Viewer, eventID string) ([]Receipt, error) {
	if !v.Manage {
		return nil, ErrForbidden
	}
	var raw string
	err := s.store.QueryRow(ctx, "SELECT payload FROM notification_events WHERE id=? AND tenant_id=?", eventID, v.TenantID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var event Event
	if err = json.Unmarshal([]byte(raw), &event); err != nil {
		return nil, err
	}
	rows, err := s.store.Query(ctx, "SELECT id,human_id,read_at FROM notification_inbox WHERE event_id=? AND tenant_id=? ORDER BY human_id", eventID, v.TenantID)
	if err != nil {
		return nil, err
	}
	type row struct {
		id, human string
		read      int64
	}
	all := []row{}
	for rows.Next() {
		var r row
		if err = rows.Scan(&r.id, &r.human, &r.read); err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	members, err := s.Members(ctx, v)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, m := range members {
		names[m.ID] = m.Name
	}
	out := []Receipt{}
	for _, r := range all {
		push, e := s.pushes(ctx, r.id)
		if e != nil {
			return nil, e
		}
		out = append(out, Receipt{HumanID: r.human, Name: names[r.human], Read: r.read != 0, Pushes: push})
	}
	return out, nil
}

// Events gives a publication manager access to delivery history even when they
// are not a recipient. It does not create a personal inbox entry or read receipt.
func (s *Service) Events(ctx context.Context, v Viewer, sourceID string) ([]Event, error) {
	if !v.Manage {
		return nil, ErrForbidden
	}
	source, err := s.Source(ctx, v, sourceID)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.Query(ctx, "SELECT payload FROM notification_events WHERE source_id=? AND tenant_id=? ORDER BY created_at DESC,id DESC LIMIT 25", source.ID, source.TenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var raw string
		var event Event
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &event); err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}
