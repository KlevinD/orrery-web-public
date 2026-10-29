package notification

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	core "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/gateway/outbound"
	"orrery_v2/internal/platform/db"
)

type testTasks struct {
	task    scheduler.ScheduledTask
	runs    []scheduler.ScheduledRun
	readErr error
}

func (f *testTasks) GetTask(context.Context, string) (scheduler.ScheduledTask, error) {
	return f.task, nil
}
func (f *testTasks) GetRun(_ context.Context, id string) (scheduler.ScheduledRun, error) {
	if f.readErr != nil {
		return scheduler.ScheduledRun{}, f.readErr
	}
	for _, run := range f.runs {
		if run.ScheduledRunID == id {
			return run, nil
		}
	}
	return scheduler.ScheduledRun{}, scheduler.ErrNotFound
}
func (f *testTasks) ListTasks(context.Context, bool) ([]scheduler.ScheduledTask, error) {
	return []scheduler.ScheduledTask{f.task}, nil
}
func (f *testTasks) ListNotificationRuns(context.Context, string, string, time.Time) ([]scheduler.ScheduledRun, error) {
	return f.runs, nil
}

type testProfiles map[string]core.AgentProfile

func (f testProfiles) GetProfile(_ context.Context, id core.AgentProfileID) (core.AgentProfile, error) {
	p, ok := f[string(id)]
	if !ok {
		return p, core.ErrNotFound
	}
	return p, nil
}
func (f testProfiles) QueryDirectory(context.Context, core.AgentDirectoryQuery) (core.AgentDirectoryPage, error) {
	// Deliberately return unrelated/inactive entries: the publisher must verify them.
	p := core.AgentDirectoryPage{}
	for _, v := range f {
		p.Entries = append(p.Entries, core.AgentDirectoryEntry{AgentProfileID: v.ID, DisplayName: v.DisplayName, Enabled: v.Enabled, AgentKind: v.AgentKind, OrgIdentity: v.OrgIdentity})
	}
	p.Total = len(p.Entries)
	return p, nil
}

type testGateway struct {
	requests   []outbound.EnqueueCommand
	deliveries map[string]outbound.Delivery
	unbound    bool
}

func (f *testGateway) Enqueue(_ context.Context, c outbound.EnqueueCommand) ([]outbound.Delivery, error) {
	if f.unbound && c.ChannelKeys[0] == "weixin" {
		return nil, outbound.ErrBindingMissing
	}
	for _, d := range f.deliveries {
		if d.IdempotencyKey == c.IdempotencyKey {
			return []outbound.Delivery{d}, nil
		}
	}
	f.requests = append(f.requests, c)
	d := outbound.Delivery{ID: key(c.IdempotencyKey), TenantID: c.TenantID, HumanProfileID: c.HumanProfileID, ChannelKey: c.ChannelKeys[0], IdempotencyKey: c.IdempotencyKey, Status: "pending", Body: c.Body, Title: c.Title}
	f.deliveries[d.ID] = d
	return []outbound.Delivery{d}, nil
}
func (f *testGateway) Get(_ context.Context, _ string, id string) (outbound.Delivery, error) {
	d, ok := f.deliveries[id]
	if !ok {
		return d, outbound.ErrNotFound
	}
	return d, nil
}

type testArtifacts struct {
	queries []scheduler.OpenRunArtifactQuery
}

func (f *testArtifacts) OpenRunArtifact(_ context.Context, q scheduler.OpenRunArtifactQuery) (scheduler.RunArtifactContent, error) {
	f.queries = append(f.queries, q)
	return scheduler.RunArtifactContent{Reader: io.NopCloser(strings.NewReader("verified report")), FileName: "report.md", SizeBytes: 15}, nil
}
func fixture(t *testing.T) (*Service, *testTasks, testProfiles, *testGateway, *testArtifacts, Viewer) {
	t.Helper()
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	profiles := testProfiles{}
	for _, id := range []string{"h1", "h2", "foreign", "disabled", "agent", "human-superadmin"} {
		p := core.AgentProfile{ID: core.AgentProfileID(id), DisplayName: id, Enabled: id != "disabled", AgentKind: core.AgentKindHuman, OrgIdentity: core.AgentOrgIdentity{OrganizationRef: core.OrganizationRef{ID: "org"}}}
		if id == "foreign" {
			p.OrgIdentity.OrganizationRef.ID = "other"
		}
		if id == "agent" {
			p.AgentKind = "ai"
		}
		profiles[id] = p
	}
	tasks := &testTasks{task: scheduler.ScheduledTask{ScheduledTaskID: "task", TenantID: "org", Title: "Daily report", Target: scheduler.Target{Mode: scheduler.TargetModeAgentTask, AgentID: "agent"}}}
	gateway := &testGateway{deliveries: map[string]outbound.Delivery{}}
	artifacts := &testArtifacts{}
	s := New(store, tasks, profiles, gateway, artifacts, nil, "https://orrery.example")
	s.now = func() time.Time { return time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC) }
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	return s, tasks, profiles, gateway, artifacts, Viewer{TenantID: "org", HumanID: "h1", Manage: true}
}
func publishRule(t *testing.T, s *Service, v Viewer, audience string, people ...string) Source {
	t.Helper()
	p, e := s.SaveSource(context.Background(), v, Source{TaskID: "task", Enabled: true, Audience: audience, People: people})
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func addRun(s *Service, f *testTasks, id string, status scheduler.RunStatus, artifact bool) {
	r := scheduler.ScheduledRun{ScheduledRunID: id, ScheduledTaskID: "task", TenantID: f.task.TenantID, CreatedAt: s.now().Add(time.Minute), CompletedAt: s.now().Add(2 * time.Minute), Status: status, Result: scheduler.RunResult{Summary: "The report is ready."}}
	r.TargetMode, r.AgentID, r.RuntimeRunID = scheduler.TargetModeAgentTask, "agent", "runtime-"+id
	r.Result.AgentReport = &scheduler.AgentReport{AgentID: r.AgentID, RuntimeRunID: r.RuntimeRunID, Body: r.Result.Summary}
	if artifact {
		r.Result.Artifacts = []scheduler.RunArtifact{{Kind: "report", FileName: "report.md", URI: "artifact://verified"}, {Kind: "report", FileName: "blocked-report.md", URI: "artifact://blocker"}}
	}
	f.runs = append(f.runs, r)
}
func later(s *Service) { now := s.now().Add(time.Hour); s.now = func() time.Time { return now } }
func mustSweep(t *testing.T, s *Service) {
	t.Helper()
	if e := s.Sweep(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func mustInbox(t *testing.T, s *Service, v Viewer) Page {
	t.Helper()
	p, e := s.Inbox(context.Background(), v, false, 25, 0)
	if e != nil {
		t.Fatal(e)
	}
	return p
}

func TestPublicationDeduplicatesAndIsolatesPeopleAndReadState(t *testing.T) {
	s, tasks, _, gateway, artifacts, v := fixture(t)
	ctx := context.Background()
	source := publishRule(t, s, v, "all")
	if _, e := s.Subscribe(ctx, v, source.ID, Subscription{Enabled: true, Channels: []string{"weixin", "feishu", "weixin"}}); e != nil {
		t.Fatal(e)
	}
	addRun(s, tasks, "r1", scheduler.RunStatusCompleted, true)
	later(s)
	mustSweep(t, s)
	restarted := New(s.store, tasks, s.profiles, gateway, artifacts, nil, s.publicOrigin)
	restarted.now = s.now
	mustSweep(t, restarted)
	first := mustInbox(t, s, v)
	if first.Total != 1 || first.Unread != 1 || first.Items[0].Reason != "organization_and_subscription" || len(first.Items[0].Event.Artifacts) != 1 || len(gateway.requests) != 2 {
		t.Fatalf("unexpected fanout: %+v requests=%d", first, len(gateway.requests))
	}
	other := Viewer{TenantID: "org", HumanID: "h2"}
	second := mustInbox(t, s, other)
	if second.Total != 1 || len(second.Items[0].Pushes) != 0 {
		t.Fatalf("default external opt-in: %+v", second)
	}
	if _, e := s.Item(ctx, other, first.Items[0].ID); !errors.Is(e, ErrNotFound) {
		t.Fatalf("other person's item: %v", e)
	}
	if _, e := s.OpenArtifact(ctx, other, first.Items[0].ID, first.Items[0].Event.Artifacts[0].ID); !errors.Is(e, ErrNotFound) {
		t.Fatalf("artifact leak: %v", e)
	}
	if _, e := s.OpenArtifact(ctx, v, first.Items[0].ID, "internal-log"); !errors.Is(e, ErrNotFound) {
		t.Fatalf("unpublished artifact: %v", e)
	}
	c, e := s.OpenArtifact(ctx, v, first.Items[0].ID, first.Items[0].Event.Artifacts[0].ID)
	if e != nil {
		t.Fatal(e)
	}
	c.Reader.Close()
	if e := s.MarkRead(ctx, v, first.Items[0].ID, true); e != nil {
		t.Fatal(e)
	}
	if mustInbox(t, s, v).Unread != 0 || mustInbox(t, s, other).Unread != 1 {
		t.Fatal("read states not independent")
	}
	for _, id := range []string{"foreign", "disabled", "agent", "human-superadmin"} {
		if _, e := s.Inbox(ctx, Viewer{TenantID: "org", HumanID: id}, false, 25, 0); !errors.Is(e, ErrForbidden) {
			t.Fatalf("invalid member %s: %v", id, e)
		}
	}
	receipts, e := s.Receipts(ctx, v, first.Items[0].Event.ID)
	if e != nil || len(receipts) != 2 {
		t.Fatalf("receipts=%+v err=%v", receipts, e)
	}
	history, e := s.Events(ctx, v, source.ID)
	if e != nil || len(history) != 1 || history[0].ID != first.Items[0].Event.ID {
		t.Fatalf("publication history=%+v err=%v", history, e)
	}
	if _, e := s.Events(ctx, other, source.ID); !errors.Is(e, ErrForbidden) {
		t.Fatalf("ordinary recipient read audience history: %v", e)
	}
}

func TestRulesRequireAuthorityAndCompareRevision(t *testing.T) {
	s, _, _, _, _, v := fixture(t)
	ctx := context.Background()
	plain := v
	plain.Manage = false
	if _, e := s.SaveSource(ctx, plain, Source{TaskID: "task", Audience: "all"}); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	if _, e := s.SaveSource(ctx, v, Source{TaskID: "task", OriginTenant: "other", Audience: "all"}); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	if _, e := s.SaveSource(ctx, v, Source{TaskID: "task", Audience: "people", People: []string{"foreign"}}); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	p := publishRule(t, s, v, "people", "h1")
	if _, e := s.Source(ctx, Viewer{TenantID: "org", HumanID: "h2"}, p.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if _, e := s.Source(ctx, Viewer{TenantID: "other", HumanID: "foreign", Manage: true}, p.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if _, e := s.SaveSource(ctx, v, p); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SaveSource(ctx, v, p); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
}

func TestOnlyFutureRunsAndVerifiedPartialReportsArePublished(t *testing.T) {
	s, tasks, _, _, _, v := fixture(t)
	ctx := context.Background()
	addRun(s, tasks, "historical", scheduler.RunStatusCompleted, true)
	later(s)
	p := publishRule(t, s, v, "subscribers")
	if _, e := s.Subscribe(ctx, v, p.ID, Subscription{Enabled: true}); e != nil {
		t.Fatal(e)
	}
	addRun(s, tasks, "partial", scheduler.RunStatusFailed, true)
	addRun(s, tasks, "failed", scheduler.RunStatusFailed, false)
	later(s)
	mustSweep(t, s)
	inbox := mustInbox(t, s, v)
	if inbox.Total != 1 || inbox.Items[0].Event.Status != "partial" {
		t.Fatalf("history/failure suppression: %+v", inbox)
	}
	if _, e := s.Subscribe(ctx, Viewer{TenantID: "org", HumanID: "h2"}, p.ID, Subscription{Enabled: true}); e != nil {
		t.Fatal(e)
	}
	mustSweep(t, s)
	if mustInbox(t, s, Viewer{TenantID: "org", HumanID: "h2"}).Total != 0 {
		t.Fatal("late subscription replayed history")
	}
}

func TestChannelRecoveryIsIndependentAndRechecksConsent(t *testing.T) {
	s, tasks, profiles, gateway, _, v := fixture(t)
	ctx := context.Background()
	p := publishRule(t, s, v, "all")
	gateway.unbound = true
	if _, e := s.SavePreferences(ctx, v, Preferences{Channels: []string{"weixin", "feishu"}}); e != nil {
		t.Fatal(e)
	}
	addRun(s, tasks, "r1", scheduler.RunStatusCompleted, false)
	later(s)
	mustSweep(t, s)
	item := mustInbox(t, s, v).Items[0]
	if len(item.Pushes) != 2 || len(gateway.requests) != 1 {
		t.Fatalf("one channel blocked another: %+v", item)
	}
	gateway.unbound = false
	mustSweep(t, s)
	if len(gateway.requests) != 2 {
		t.Fatal("binding recovery missing")
	}
	for _, d := range gateway.deliveries {
		if allowed, e := s.ValidateDelivery(ctx, d); e != nil || !allowed {
			t.Fatalf("valid delivery: %v %v", allowed, e)
		}
	}
	p.Audience = "subscribers"
	if _, e := s.SaveSource(ctx, v, p); e != nil {
		t.Fatal(e)
	}
	for _, d := range gateway.deliveries {
		if allowed, e := s.ValidateDelivery(ctx, d); e != nil || allowed {
			t.Fatalf("revoked audience: %v %v", allowed, e)
		}
	}
	mustSweep(t, s)
	for _, push := range mustInbox(t, s, v).Items[0].Pushes {
		if push.Status != "cancelled" {
			t.Fatalf("queued delivery not cancelled: %+v", push)
		}
	}
	human := profiles["h1"]
	human.Enabled = false
	profiles["h1"] = human
	for _, d := range gateway.deliveries {
		if allowed, e := s.ValidateDelivery(ctx, d); e != nil || allowed {
			t.Fatalf("disabled person: %v %v", allowed, e)
		}
	}
}

func TestCrossTenantPublicationNeedsSystemGrantAndScopesArtifacts(t *testing.T) {
	s, tasks, _, _, artifacts, v := fixture(t)
	ctx := context.Background()
	tasks.task.TenantID = "system"
	p := Source{TaskID: "task", TenantID: "org", OriginTenant: "system", Audience: "all", Enabled: true}
	if _, e := s.SaveSource(ctx, v, p); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	admin := v
	admin.System = true
	if _, e := s.SaveSource(ctx, admin, p); e != nil {
		t.Fatal(e)
	}
	addRun(s, tasks, "system-report", scheduler.RunStatusCompleted, true)
	later(s)
	mustSweep(t, s)
	item := mustInbox(t, s, v).Items[0]
	if item.Event.TenantID != "org" {
		t.Fatal("incorrect audience tenant")
	}
	c, e := s.OpenArtifact(ctx, v, item.ID, item.Event.Artifacts[0].ID)
	if e != nil {
		t.Fatal(e)
	}
	c.Reader.Close()
	for _, q := range artifacts.queries {
		if q.TenantID != "system" || q.ScheduledRunID != "system-report" {
			t.Fatalf("incorrect artifact scope: %+v", q)
		}
	}
}

func TestPaginationCountsUnreadAndRevokesRemovedAudience(t *testing.T) {
	s, tasks, _, _, _, v := fixture(t)
	ctx := context.Background()
	p := publishRule(t, s, v, "all")
	for _, id := range []string{"r1", "r2", "r3"} {
		addRun(s, tasks, id, scheduler.RunStatusCompleted, false)
	}
	later(s)
	mustSweep(t, s)
	all := mustInbox(t, s, v)
	if e := s.MarkRead(ctx, v, all.Items[0].ID, true); e != nil {
		t.Fatal(e)
	}
	page, e := s.Inbox(ctx, v, false, 1, 1)
	if e != nil || page.Total != 3 || page.Unread != 2 || len(page.Items) != 1 || page.Items[0].ID != all.Items[1].ID {
		t.Fatalf("pagination: %+v %v", page, e)
	}
	unread, e := s.Inbox(ctx, v, true, 1, 1)
	if e != nil || unread.Total != 2 || len(unread.Items) != 1 {
		t.Fatalf("unread: %+v %v", unread, e)
	}
	p.Audience = "people"
	p.People = []string{"h2"}
	if _, e := s.SaveSource(ctx, v, p); e != nil {
		t.Fatal(e)
	}
	if mustInbox(t, s, v).Total != 0 {
		t.Fatal("private audience removal did not revoke access")
	}
}
