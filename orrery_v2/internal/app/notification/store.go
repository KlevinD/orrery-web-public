package notification

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/scheduler"
)

func key(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "nt-" + hex.EncodeToString(h[:16])
}
func encode(v any) string { b, _ := json.Marshal(v); return string(b) }
func contains(items []string, v string) bool {
	for _, i := range items {
		if i == v {
			return true
		}
	}
	return false
}
func channels(in []string) ([]string, error) {
	out := []string{}
	for _, v := range in {
		if v != "weixin" && v != "feishu" {
			return nil, ErrInvalid
		}
		if !contains(out, v) {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out, nil
}
func (s *Service) EnsureSchema(ctx context.Context) error {
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS notification_sources (id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, origin_tenant TEXT NOT NULL, task_id TEXT NOT NULL, revision BIGINT NOT NULL, payload TEXT NOT NULL, UNIQUE(tenant_id,origin_tenant,task_id))`,
		`CREATE TABLE IF NOT EXISTS notification_subscriptions (source_id TEXT NOT NULL, tenant_id TEXT NOT NULL, human_id TEXT NOT NULL, payload TEXT NOT NULL, PRIMARY KEY(source_id,tenant_id,human_id))`,
		`CREATE TABLE IF NOT EXISTS notification_preferences (tenant_id TEXT NOT NULL, human_id TEXT NOT NULL, payload TEXT NOT NULL, PRIMARY KEY(tenant_id,human_id))`,
		`CREATE TABLE IF NOT EXISTS notification_events (id TEXT PRIMARY KEY, source_id TEXT NOT NULL, tenant_id TEXT NOT NULL, created_at BIGINT NOT NULL, payload TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS notification_report_origins (event_id TEXT PRIMARY KEY, origin_sha256 TEXT NOT NULL, event_sha256 TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS notification_publication_checks (source_id TEXT NOT NULL, run_id TEXT NOT NULL, reason TEXT NOT NULL, updated_at BIGINT NOT NULL, PRIMARY KEY(source_id,run_id))`,
		`CREATE TABLE IF NOT EXISTS notification_inbox (id TEXT PRIMARY KEY, event_id TEXT NOT NULL, source_id TEXT NOT NULL, tenant_id TEXT NOT NULL, human_id TEXT NOT NULL, reason TEXT NOT NULL, read_at BIGINT NOT NULL DEFAULT 0, created_at BIGINT NOT NULL, UNIQUE(event_id,tenant_id,human_id))`,
		`CREATE TABLE IF NOT EXISTS notification_pushes (id TEXT PRIMARY KEY, inbox_id TEXT NOT NULL, tenant_id TEXT NOT NULL, human_id TEXT NOT NULL, channel TEXT NOT NULL, status TEXT NOT NULL, last_error TEXT NOT NULL DEFAULT '', gateway_id TEXT NOT NULL DEFAULT '', updated_at BIGINT NOT NULL, UNIQUE(inbox_id,channel))`,
		`CREATE INDEX IF NOT EXISTS idx_notification_inbox_person ON notification_inbox(tenant_id,human_id,created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_notification_events_source ON notification_events(source_id,created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_notification_pushes_pending ON notification_pushes(status,updated_at)`,
	} {
		if _, err := s.store.Exec(ctx, q); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) human(ctx context.Context, v Viewer) error {
	if v.TenantID == "" || v.HumanID == "" || agentcore.IsReservedNonWorkforceProfileID(v.HumanID) || s.profiles == nil {
		return ErrForbidden
	}
	p, err := s.profiles.GetProfile(ctx, agentcore.AgentProfileID(v.HumanID))
	if errors.Is(err, agentcore.ErrNotFound) {
		return ErrForbidden
	}
	if err != nil {
		return err
	}
	if !p.Enabled || p.AgentKind != agentcore.AgentKindHuman || string(p.OrgIdentity.OrganizationRef.ID) != v.TenantID {
		return ErrForbidden
	}
	return nil
}
func (s *Service) loadSource(ctx context.Context, id string) (Source, error) {
	var raw string
	err := s.store.QueryRow(ctx, "SELECT payload FROM notification_sources WHERE id=?", id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Source{}, ErrNotFound
	}
	if err != nil {
		return Source{}, err
	}
	var p Source
	err = json.Unmarshal([]byte(raw), &p)
	return p, err
}
func eligible(p Source, human string) bool {
	return p.Audience != "people" || contains(p.People, human)
}
func (s *Service) Source(ctx context.Context, v Viewer, id string) (Source, error) {
	p, err := s.loadSource(ctx, id)
	if err != nil {
		return p, err
	}
	if p.TenantID != v.TenantID && !v.System {
		return Source{}, ErrNotFound
	}
	if !v.Manage {
		if err = s.human(ctx, v); err != nil {
			return Source{}, err
		}
		if !eligible(p, v.HumanID) {
			return Source{}, ErrNotFound
		}
	}
	if v.HumanID != "" && p.TenantID == v.TenantID {
		sub, e := s.subscription(ctx, v, id)
		if e != nil {
			return Source{}, e
		}
		p.Subscription = &sub
	}
	return p, nil
}
func (s *Service) Sources(ctx context.Context, v Viewer) ([]Source, error) {
	if !v.Manage {
		if err := s.human(ctx, v); err != nil {
			return nil, err
		}
	}
	rows, err := s.store.Query(ctx, "SELECT payload FROM notification_sources WHERE tenant_id=? ORDER BY id", v.TenantID)
	if err != nil {
		return nil, err
	}
	all := []Source{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, err
		}
		var p Source
		if err = json.Unmarshal([]byte(raw), &p); err != nil {
			rows.Close()
			return nil, err
		}
		if v.Manage || (p.Enabled && eligible(p, v.HumanID)) {
			all = append(all, p)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range all {
		if v.HumanID != "" {
			sub, e := s.subscription(ctx, v, all[i].ID)
			if e != nil {
				return nil, e
			}
			all[i].Subscription = &sub
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Title < all[j].Title })
	return all, nil
}
func (s *Service) SaveSource(ctx context.Context, v Viewer, p Source) (Source, error) {
	if !v.Manage || v.TenantID == "" {
		return Source{}, ErrForbidden
	}
	if p.TenantID == "" {
		p.TenantID = v.TenantID
	}
	if p.OriginTenant == "" {
		p.OriginTenant = v.TenantID
	}
	if !v.System && p.TenantID != v.TenantID {
		return Source{}, ErrForbidden
	}
	if !v.System && p.OriginTenant != v.TenantID {
		// Only the system can introduce a cross-tenant result grant. Once
		// published, the receiving organization can manage its audience.
		grant, err := s.loadSource(ctx, key(p.TenantID, p.OriginTenant, p.TaskID))
		if errors.Is(err, ErrNotFound) {
			return Source{}, ErrForbidden
		}
		if err != nil {
			return Source{}, err
		}
		if grant.TenantID != v.TenantID {
			return Source{}, ErrForbidden
		}
	}
	if p.TaskID == "" || len(p.People) > 500 || !contains([]string{"subscribers", "all", "people"}, p.Audience) {
		return Source{}, ErrInvalid
	}
	task, err := s.scheduler.GetTask(ctx, p.TaskID)
	if err != nil {
		return Source{}, err
	}
	if task.TenantID != p.OriginTenant || task.Target.Mode != scheduler.TargetModeAgentTask {
		return Source{}, ErrNotFound
	}
	members, err := s.Members(ctx, Viewer{TenantID: p.TenantID, Manage: true, System: v.System})
	if err != nil {
		return Source{}, err
	}
	known := map[string]bool{}
	for _, m := range members {
		known[m.ID] = true
	}
	clean := []string{}
	for _, id := range p.People {
		if !known[id] {
			return Source{}, fmt.Errorf("%w: unknown recipient", ErrInvalid)
		}
		if !contains(clean, id) {
			clean = append(clean, id)
		}
	}
	if p.Audience == "people" && len(clean) == 0 {
		return Source{}, ErrInvalid
	}
	p.People = clean
	p.ID = key(p.TenantID, p.OriginTenant, p.TaskID)
	p.Title = task.Title
	p.AgentID = task.Target.AgentID
	p.Subscription = nil
	old, err := s.loadSource(ctx, p.ID)
	exists := err == nil
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Source{}, err
	}
	if exists && old.Revision != p.Revision || !exists && p.Revision != 0 {
		return Source{}, ErrConflict
	}
	p.UpdatedAt = s.now()
	p.ActivatedAt = old.ActivatedAt
	if p.ActivatedAt.IsZero() || (!old.Enabled && p.Enabled) {
		p.ActivatedAt = p.UpdatedAt
	}
	p.Revision++
	if exists {
		res, e := s.store.Exec(ctx, "UPDATE notification_sources SET payload=?,revision=? WHERE id=? AND revision=?", encode(p), p.Revision, p.ID, old.Revision)
		if e != nil {
			return Source{}, e
		}
		n, e := res.RowsAffected()
		if e != nil {
			return Source{}, e
		}
		if n != 1 {
			return Source{}, ErrConflict
		}
	} else {
		res, e := s.store.Exec(ctx, "INSERT INTO notification_sources(id,tenant_id,origin_tenant,task_id,revision,payload) VALUES(?,?,?,?,?,?) ON CONFLICT DO NOTHING", p.ID, p.TenantID, p.OriginTenant, p.TaskID, p.Revision, encode(p))
		if e != nil {
			return Source{}, e
		}
		n, e := res.RowsAffected()
		if e != nil {
			return Source{}, e
		}
		if n != 1 {
			return Source{}, ErrConflict
		}
	}
	return p, nil
}
func (s *Service) subscription(ctx context.Context, v Viewer, id string) (Subscription, error) {
	var raw string
	sub := Subscription{Channels: []string{}}
	err := s.store.QueryRow(ctx, "SELECT payload FROM notification_subscriptions WHERE tenant_id=? AND human_id=? AND source_id=?", v.TenantID, v.HumanID, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return sub, nil
	}
	if err != nil {
		return sub, err
	}
	err = json.Unmarshal([]byte(raw), &sub)
	return sub, err
}
func (s *Service) Subscribe(ctx context.Context, v Viewer, id string, sub Subscription) (Subscription, error) {
	if err := s.human(ctx, v); err != nil {
		return sub, err
	}
	p, err := s.Source(ctx, Viewer{TenantID: v.TenantID, HumanID: v.HumanID}, id)
	if err != nil {
		return sub, err
	}
	if !p.Enabled && sub.Enabled {
		return sub, ErrInvalid
	}
	sub.Channels, err = channels(sub.Channels)
	if err != nil {
		return sub, err
	}
	old, err := s.subscription(ctx, v, id)
	if err != nil {
		return sub, err
	}
	sub.Since = old.Since
	if sub.Since.IsZero() || (!old.Enabled && sub.Enabled) {
		sub.Since = s.now()
	}
	_, err = s.store.Exec(ctx, "INSERT INTO notification_subscriptions(source_id,tenant_id,human_id,payload) VALUES(?,?,?,?) ON CONFLICT(source_id,tenant_id,human_id) DO UPDATE SET payload=excluded.payload", id, v.TenantID, v.HumanID, encode(sub))
	return sub, err
}
func (s *Service) Preferences(ctx context.Context, v Viewer) (Preferences, error) {
	if err := s.human(ctx, v); err != nil {
		return Preferences{}, err
	}
	var raw string
	p := Preferences{Channels: []string{}}
	err := s.store.QueryRow(ctx, "SELECT payload FROM notification_preferences WHERE tenant_id=? AND human_id=?", v.TenantID, v.HumanID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	err = json.Unmarshal([]byte(raw), &p)
	return p, err
}

func (s *Service) Channels(ctx context.Context, v Viewer) ([]string, error) {
	if err := s.human(ctx, v); err != nil {
		return nil, err
	}
	if s.bindings == nil {
		return []string{}, nil
	}
	return s.bindings.Channels(ctx, v.TenantID, v.HumanID)
}
func (s *Service) SavePreferences(ctx context.Context, v Viewer, p Preferences) (Preferences, error) {
	if err := s.human(ctx, v); err != nil {
		return p, err
	}
	c, err := channels(p.Channels)
	if err != nil {
		return p, err
	}
	p.Channels = c
	_, err = s.store.Exec(ctx, "INSERT INTO notification_preferences(tenant_id,human_id,payload) VALUES(?,?,?) ON CONFLICT(tenant_id,human_id) DO UPDATE SET payload=excluded.payload", v.TenantID, v.HumanID, encode(p))
	return p, err
}
func (s *Service) Members(ctx context.Context, v Viewer) ([]Person, error) {
	if !v.Manage || v.TenantID == "" {
		return nil, ErrForbidden
	}
	people := []Person{}
	for offset := 0; ; offset += 200 {
		page, err := s.profiles.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{Organization: agentcore.OrganizationID(v.TenantID), AgentKind: agentcore.AgentKindHuman, EnabledOnly: true, IncludeHumans: true, Page: agentcore.Page{Limit: 200, Offset: offset}})
		if err != nil {
			return nil, err
		}
		for _, p := range page.Entries {
			if p.Enabled && p.AgentKind == agentcore.AgentKindHuman && !agentcore.IsReservedNonWorkforceProfileID(p.AgentProfileID.String()) && string(p.OrgIdentity.OrganizationRef.ID) == v.TenantID {
				people = append(people, Person{ID: p.AgentProfileID.String(), Name: p.DisplayName, Channels: []string{}})
			}
		}
		if offset+len(page.Entries) >= page.Total {
			break
		}
		if len(page.Entries) == 0 {
			return nil, errors.New("incomplete notification member directory")
		}
	}
	return people, nil
}
func (s *Service) Preview(ctx context.Context, v Viewer, id string) (Preview, error) {
	if !v.Manage {
		return Preview{}, ErrForbidden
	}
	p, err := s.Source(ctx, v, id)
	if err != nil {
		return Preview{}, err
	}
	if !p.Enabled {
		return Preview{People: []Person{}}, nil
	}
	members, err := s.Members(ctx, Viewer{TenantID: p.TenantID, Manage: true})
	if err != nil {
		return Preview{}, err
	}
	out := Preview{People: []Person{}}
	for _, m := range members {
		sub, e := s.subscription(ctx, Viewer{TenantID: p.TenantID, HumanID: m.ID}, id)
		if e != nil {
			return out, e
		}
		if !eligible(p, m.ID) || p.Audience == "subscribers" && !sub.Enabled {
			continue
		}
		if s.bindings != nil {
			m.Channels, e = s.bindings.Channels(ctx, p.TenantID, m.ID)
			if e != nil {
				return out, e
			}
		}
		out.People = append(out.People, m)
	}
	out.Total = len(out.People)
	return out, nil
}

// Task candidates are restricted to the managing user's organization.
func (s *Service) Candidates(ctx context.Context, v Viewer) ([]Source, error) {
	if !v.Manage {
		return nil, ErrForbidden
	}
	tasks, err := s.scheduler.ListTasks(ctx, false)
	if err != nil {
		return nil, err
	}
	out := []Source{}
	for _, t := range tasks {
		if t.TenantID == v.TenantID && t.Target.Mode == scheduler.TargetModeAgentTask {
			out = append(out, Source{ID: key(v.TenantID, v.TenantID, t.ScheduledTaskID), TenantID: v.TenantID, OriginTenant: v.TenantID, TaskID: t.ScheduledTaskID, Title: t.Title, AgentID: t.Target.AgentID, Audience: "subscribers", People: []string{}})
		}
	}
	return out, nil
}
func (s *Service) sourceRows(ctx context.Context) ([]Source, error) {
	rows, err := s.store.Query(ctx, "SELECT payload FROM notification_sources ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Source{}
	for rows.Next() {
		var raw string
		var p Source
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
