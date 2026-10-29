package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/identity"
	gwoutbound "orrery_v2/internal/gateway/outbound"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/graphprotocol"
)

type systemUpdateSourceStub struct {
	updates map[string]personalAssistantSystemUpdate
}

func (s *systemUpdateSourceStub) Current(_ context.Context, tenant, kind, id string, _ bool) (personalAssistantSystemUpdate, bool, error) {
	u, ok := s.updates[id]
	return u, ok && u.TenantID == tenant && u.Kind == kind, nil
}
func (s *systemUpdateSourceStub) List(_ context.Context, tenant, kind string, after personalAssistantSystemCursor) ([]personalAssistantSystemSourceRow, error) {
	var out []personalAssistantSystemSourceRow
	for _, u := range s.updates {
		if u.TenantID == tenant && u.Kind == kind && (u.At.UnixNano() > after.At || u.At.UnixNano() == after.At && u.ID > after.ID) {
			out = append(out, personalAssistantSystemSourceRow{ID: u.ID, Position: personalAssistantSystemCursor{At: u.At.UnixNano(), ID: u.ID}})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Position.At == out[j].Position.At {
			return out[i].ID < out[j].ID
		}
		return out[i].Position.At < out[j].Position.At
	})
	if len(out) > 100 {
		out = out[:100]
	}
	return out, nil
}

func systemUpdatesFixture(t *testing.T) (*productionPersonalAssistantSystemUpdates, *systemUpdateSourceStub, *recordingGatewayViewerAuthorization) {
	t.Helper()
	scheduled, _, deliveries := scheduledResultFixture(t)
	attention, _, _, _, auth := attentionFixture()
	auth.resolved = append(auth.resolved, "operations.read", "operations.manage")
	attention.targets = &recordingPersonalAssistantApprovalNotificationSource{targetsByTenant: map[string]productionPersonalAssistantWeixinTarget{"tenant-a": {TenantID: "tenant-a", OwnerID: "owner-a", AccountID: "bot-a"}}}
	source := &systemUpdateSourceStub{updates: map[string]personalAssistantSystemUpdate{}}
	s := &productionPersonalAssistantSystemUpdates{routes: []personalAssistantSystemRoute{{TenantID: "tenant-a", HumanProfileID: "human-a", SourceTenantID: "tenant-a"}}, jobs: scheduled.jobs, source: source, attention: attention, replies: &agentNoticeWriterStub{body: "数据访问出现故障，目前正在排查，尚未确认原任务完成。"}, deliveries: deliveries, now: scheduled.now,
		access: func(_ context.Context, p identity.Principal, tenant string, _ graphprotocol.AccessRef, _ bool) (bool, error) {
			return p.UserID == "owner-a" && p.AgentProfileID == "human-a" && tenant != "forbidden", nil
		}}
	if _, err := s.fence(context.Background(), s.routes[0]); err != nil {
		t.Fatal(err)
	}
	return s, source, auth
}
func newSystemTestUpdate(s *productionPersonalAssistantSystemUpdates, id string) personalAssistantSystemUpdate {
	return personalAssistantSystemUpdate{ID: id, TenantID: "tenant-a", Kind: "failure", State: "open:error", Category: "system_error", Revision: 1, At: s.now().Add(time.Second), Facts: map[string]any{"summary": "data unavailable"}}
}
func systemDeliveryCount(t *testing.T, s *productionPersonalAssistantSystemUpdates) int {
	t.Helper()
	var count int
	if err := s.jobs.QueryRow(context.Background(), "SELECT COUNT(*) FROM gateway_message_deliveries").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
func sweepSystem(t *testing.T, s *productionPersonalAssistantSystemUpdates, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := s.Sweep(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSystemUpdatesActivationRestartDedupAndRecurrence(t *testing.T) {
	s, source, _ := systemUpdatesFixture(t)
	old := newSystemTestUpdate(s, "old")
	old.At = s.now().Add(-time.Hour)
	source.updates[old.ID] = old
	u := newSystemTestUpdate(s, "current")
	source.updates[u.ID] = u
	sweepSystem(t, s, 1)
	u.Revision++
	u.At = u.At.Add(time.Minute)
	source.updates[u.ID] = u
	restarted := *s
	sweepSystem(t, &restarted, 3)
	if got := systemDeliveryCount(t, s); got != 1 {
		t.Fatalf("historical/repeated incident replayed: %d", got)
	}
	u.State = "technical_recovered"
	u.Revision++
	u.At = u.At.Add(time.Minute)
	source.updates[u.ID] = u
	sweepSystem(t, s, 2)
	u.State = "open:error"
	u.Revision++
	u.At = u.At.Add(time.Minute)
	source.updates[u.ID] = u
	sweepSystem(t, s, 2)
	if got := systemDeliveryCount(t, s); got != 3 {
		t.Fatalf("new repair phase or recurrence lost: %d", got)
	}
	rows, err := s.jobs.Query(context.Background(), "SELECT title,body FROM gateway_message_deliveries")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var title, body string
		if err := rows.Scan(&title, &body); err != nil {
			t.Fatal(err)
		}
		if title != "" || body != s.replies.(*agentNoticeWriterStub).body {
			t.Fatal("Agent words changed")
		}
	}
}

func TestSystemUpdatesQueueReceiptIsNotDeliveryAndSurvivesRestart(t *testing.T) {
	s, source, _ := systemUpdatesFixture(t)
	u := newSystemTestUpdate(s, "needs-maintenance")
	u.State, u.Category = "needs_attention", "automatic_repair"
	source.updates[u.ID] = u
	sweepSystem(t, s, 1)
	read := func() personalAssistantSystemPhase {
		r, found, err := s.jobs.GetRecord(context.Background(), personalAssistantSystemPhaseTable, systemPhaseKey(s.routes[0], u.Kind, u.ID))
		if err != nil || !found {
			t.Fatalf("phase missing: %v", err)
		}
		p, err := systemUpdateDecode[personalAssistantSystemPhase](r)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	p := read()
	if !p.Queued || p.Notified || p.DeliveryStatus != "pending" || len(p.DeliveryIDs) != 1 {
		t.Fatalf("enqueue was treated as delivery: %+v", p)
	}
	// Simulate the old binary's premature flag and verify reconciliation without
	// a second model reply or a second channel message.
	p.Notified, p.Queued, p.DeliveryIDs = true, false, nil
	if err := s.jobs.PutRecord(context.Background(), personalAssistantSystemPhaseTable, systemPhaseKey(p.Route, p.Kind, p.ID), systemUpdateRecord(p)); err != nil {
		t.Fatal(err)
	}
	restarted := *s
	sweepSystem(t, &restarted, 2)
	if p = read(); p.Notified || !p.Queued {
		t.Fatalf("legacy pending receipt not reconciled: %+v", p)
	}
	if err := s.jobs.WithTx(context.Background(), func(tx db.Tx) error {
		_, err := tx.ExecContext(context.Background(), "UPDATE gateway_message_deliveries SET status='delivered',delivered_at=? WHERE id=?", s.now().UnixNano(), p.DeliveryIDs[0])
		return err
	}); err != nil {
		t.Fatal(err)
	}
	sweepSystem(t, &restarted, 2)
	p = read()
	if !p.Notified || p.DeliveryStatus != "delivered" || p.DeliveredAt.IsZero() || systemDeliveryCount(t, s) != 1 {
		t.Fatalf("canonical delivery not reconciled or duplicated: %+v", p)
	}
}

func TestSystemUpdatesRefreshReceiptWithoutIncidentTimestampChange(t *testing.T) {
	s, source, _ := systemUpdatesFixture(t)
	u := newSystemTestUpdate(s, "repair")
	source.updates[u.ID] = u
	sweepSystem(t, s, 1)
	// List would no longer return this ID. A host receipt changed independently
	// of incident revision and must still produce exactly one new phase.
	u.State = "retry_wait"
	source.updates[u.ID] = u
	sweepSystem(t, s, 4)
	if got := systemDeliveryCount(t, s); got != 2 {
		t.Fatalf("receipt progress missing or duplicated: %d", got)
	}
}

func TestSystemUpdatesReceiptRefreshGapDoesNotReannounceSameFault(t *testing.T) {
	s, source, _ := systemUpdatesFixture(t)
	u := newSystemTestUpdate(s, "repair")
	u.State = "running"
	u.Category = "automatic_repair"
	u.Facts["status"] = "repairing"
	source.updates[u.ID] = u
	sweepSystem(t, s, 1)
	u.Revision++
	u.At = u.At.Add(time.Second)
	u.State = "repairing:error"
	u.Category = "system_error"
	source.updates[u.ID] = u
	sweepSystem(t, s, 2)
	u.State = "running"
	u.Category = "automatic_repair"
	source.updates[u.ID] = u
	sweepSystem(t, s, 3)
	if got := systemDeliveryCount(t, s); got != 1 {
		t.Fatalf("temporary receipt invalidation repeated fault: %d", got)
	}
	u.Revision++
	u.At = u.At.Add(time.Second)
	u.Facts["status"] = "monitoring"
	source.updates[u.ID] = u
	sweepSystem(t, s, 2)
	if got := systemDeliveryCount(t, s); got != 1 {
		t.Fatalf("incident bookkeeping repeated the same repair phase: %d", got)
	}
}

func TestSystemUpdatesChecksOriginalRSIAccessBeforeModelGeneration(t *testing.T) {
	s, source, _ := systemUpdatesFixture(t)
	u := newSystemTestUpdate(s, "private-rsi")
	u.Kind = "rsi"
	u.Category = "rsi_progress"
	u.State = "diagnosed"
	u.Access = graphprotocol.AccessRef{Kind: "memory", ID: "private-memory", TenantID: "tenant-a"}
	source.updates[u.ID] = u
	s.access = func(_ context.Context, _ identity.Principal, _ string, ref graphprotocol.AccessRef, _ bool) (bool, error) {
		return ref.Kind == "", nil
	}
	sweepSystem(t, s, 3)
	if s.replies.(*agentNoticeWriterStub).calls != 0 || systemDeliveryCount(t, s) != 0 {
		t.Fatal("private RSI source disclosed before original ACL check")
	}
}

func TestSystemUpdatesPaginationDoesNotStarveBehindFailedAgent(t *testing.T) {
	s, source, _ := systemUpdatesFixture(t)
	for i := 0; i < 105; i++ {
		u := newSystemTestUpdate(s, fmt.Sprintf("incident-%03d", i))
		source.updates[u.ID] = u
	}
	writer := s.replies.(*agentNoticeWriterStub)
	writer.err = errors.New("model unavailable")
	if err := s.Sweep(context.Background()); err == nil {
		t.Fatal("model failure lost")
	}
	if systemDeliveryCount(t, s) != 0 {
		t.Fatal("platform supplied fallback")
	}
	writer.err = nil
	later := s.now().Add(2 * time.Minute)
	s.now = func() time.Time { return later }
	sweepSystem(t, s, 110)
	if got := systemDeliveryCount(t, s); got != 105 {
		t.Fatalf("page or pending cursor lost events: %d", got)
	}
}

type systemUpdateMutatingWriter struct {
	*agentNoticeWriterStub
	mutate func()
}

func (w systemUpdateMutatingWriter) Reply(ctx context.Context, target productionPersonalAssistantWeixinTarget, human, key, material string) (string, error) {
	body, err := w.agentNoticeWriterStub.Reply(ctx, target, human, key, material)
	w.mutate()
	return body, err
}

func TestSystemUpdatesFenceStatusAndPermissionAtSendAndContext(t *testing.T) {
	for _, scenario := range []string{"phase_changed", "permission_revoked", "source_denied", "recipient_disabled"} {
		t.Run(scenario, func(t *testing.T) {
			s, source, auth := systemUpdatesFixture(t)
			u := newSystemTestUpdate(s, "incident")
			source.updates[u.ID] = u
			s.replies = systemUpdateMutatingWriter{agentNoticeWriterStub: s.replies.(*agentNoticeWriterStub), mutate: func() {
				switch scenario {
				case "phase_changed":
					u.State = "resolved"
					source.updates[u.ID] = u
				case "permission_revoked":
					auth.resolved = nil
				case "source_denied":
					s.access = func(context.Context, identity.Principal, string, graphprotocol.AccessRef, bool) (bool, error) {
						return false, nil
					}
				case "recipient_disabled":
					auth.account.Enabled = false
				}
			}}
			sweepSystem(t, s, 1)
			if systemDeliveryCount(t, s) != 0 {
				t.Fatal("stale or unauthorized notification queued")
			}
		})
	}
	s, source, auth := systemUpdatesFixture(t)
	u := newSystemTestUpdate(s, "incident")
	source.updates[u.ID] = u
	sweepSystem(t, s, 1)
	var key, body string
	if err := s.jobs.QueryRow(context.Background(), "SELECT idempotency_key,body FROM gateway_message_deliveries").Scan(&key, &body); err != nil {
		t.Fatal(err)
	}
	d := gwoutbound.Delivery{TenantID: "tenant-a", HumanProfileID: "human-a", AccountID: "bot-a", ChannelKey: "weixin", IdempotencyKey: key, Body: body}
	if ok, err := s.Validate(context.Background(), d); err != nil || !ok {
		t.Fatal("current real source rejected", err)
	}
	if _, ok, err := s.ContextNotice(context.Background(), d); err != nil || !ok {
		t.Fatal("context missing", err)
	}
	auth.resolved = nil
	if ok, _ := s.Validate(context.Background(), d); ok {
		t.Fatal("queued send survived permission revocation")
	}
	if _, ok, _ := s.ContextNotice(context.Background(), d); ok {
		t.Fatal("context survived permission revocation")
	}
	if ok, _ := (productionPersonalAssistantNotificationGuard{}).Validate(context.Background(), d); ok {
		t.Fatal("disabled system stream accepted queued notification")
	}
}

func TestSystemUpdatesAgentContextAndNoSecondModelOnRestart(t *testing.T) {
	s, source, _ := systemUpdatesFixture(t)
	u := newSystemTestUpdate(s, "incident")
	source.updates[u.ID] = u
	body := "资料访问出现故障，目前只确认了技术恢复，原任务是否完成还需要核验。"
	runner := &recordingPersonalAssistantApprovalReviewRunner{response: discussionsession.DiscussionAgentResponse{AssistantMessage: body, Status: "completed"}}
	s.replies = &productionAgentNotificationReplies{store: s.jobs, runner: runner}
	sweepSystem(t, s, 1)
	runner.err = errors.New("must reuse original report")
	restarted := *s
	restarted.replies = &productionAgentNotificationReplies{store: s.jobs, runner: runner}
	sweepSystem(t, &restarted, 2)
	contextProvider := &productionPersonalAssistantNotificationContext{jobs: s.jobs, attention: s.attention, systemUpdates: s}
	request := discussionsession.DiscussionAgentRequest{ConversationKind: discussionsession.ConversationKindPersonalAssistant, ConversationTenantID: "tenant-a", ConversationOwnerID: "owner-a", Viewer: discussionsession.ViewerRef{AgentProfileID: "human-a"}}
	material, err := contextProvider.NotificationContext(context.Background(), request)
	if err != nil || !strings.Contains(material, strings.TrimSuffix(body, "。")) || !strings.Contains(material, "system_error") {
		t.Fatalf("push context lost: %q %v", material, err)
	}
	var key string
	if err := s.jobs.QueryRow(context.Background(), "SELECT idempotency_key FROM gateway_message_deliveries").Scan(&key); err != nil {
		t.Fatal(err)
	}
	d := gwoutbound.Delivery{TenantID: "tenant-a", HumanProfileID: "human-a", AccountID: "bot-a", ChannelKey: "weixin", IdempotencyKey: key, Body: body + "平台追加文本"}
	if ok, _ := s.Validate(context.Background(), d); ok {
		t.Fatal("rewritten Agent report accepted")
	}
}

func TestSystemUpdateRoutesRequireExplicitSourceAndStrictOptIn(t *testing.T) {
	valid := `[{"tenant_id":"tenant-a","human_profile_id":"human-a","source_tenant_id":"system"}]`
	if routes, err := parsePersonalAssistantSystemRoutes(valid); err != nil || len(routes) != 1 {
		t.Fatal(err)
	}
	for _, raw := range []string{`[{"tenant_id":"tenant-a","human_profile_id":"human-a"}]`, valid + `[]`, `[{"tenant_id":"t","human_profile_id":"h","source_tenant_id":"s","broadcast":true}]`} {
		if _, err := parsePersonalAssistantSystemRoutes(raw); err == nil {
			t.Fatal("invalid route accepted")
		}
	}
	s, source, auth := systemUpdatesFixture(t)
	s.routes[0].SourceTenantID = "system"
	u := newSystemTestUpdate(s, "cross-tenant")
	u.TenantID = "system"
	source.updates[u.ID] = u
	auth.resolved = []string{"operations.read"}
	sweepSystem(t, s, 2)
	if systemDeliveryCount(t, s) != 0 {
		t.Fatal("cross-source stream bypassed operational authority")
	}
}
