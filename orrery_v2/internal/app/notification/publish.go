package notification

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"orrery_v2/internal/agent/outputpolicy"
	"orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/gateway/outbound"
	"orrery_v2/internal/platform/db"
)

// Sweep reconciles durable terminal results. A process crash is recovered by
// rereading the source; publication and every recipient are committed together.
func (s *Service) Sweep(ctx context.Context) error {
	sources, err := s.sourceRows(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, p := range sources {
		if !p.Enabled {
			continue
		}
		runs, e := s.scheduler.ListNotificationRuns(ctx, p.OriginTenant, p.TaskID, p.ActivatedAt)
		if e != nil {
			failures = append(failures, e)
			continue
		}
		for _, r := range runs {
			if r.ScheduledTaskID != p.TaskID || r.TenantID != p.OriginTenant || r.CreatedAt.Before(p.ActivatedAt) || r.CompletedAt.IsZero() || r.CompletedAt.After(s.now().Add(-10*time.Second)) {
				continue
			}
			if r.Status != scheduler.RunStatusCompleted && r.Status != scheduler.RunStatusFailed {
				continue
			}
			if e = s.publish(ctx, p, r); e != nil {
				failures = append(failures, fmt.Errorf("publish %s: %w", p.ID, e))
			}
		}
	}
	if e := s.reconcilePushes(ctx); e != nil {
		failures = append(failures, e)
	}
	return errors.Join(failures...)
}
func (s *Service) publish(ctx context.Context, p Source, r scheduler.ScheduledRun) error {
	eventID := key(p.ID, r.ScheduledRunID)
	var count int
	if err := s.store.QueryRow(ctx, "SELECT COUNT(*) FROM notification_events WHERE id=?", eventID).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	if !validReportRun(r) {
		return s.recordPublicationCheck(ctx, p.ID, r.ScheduledRunID, "invalid_report_identity")
	}
	report, authored, err := s.report(ctx, r)
	if err != nil {
		return err
	}
	if !authored {
		reason := "invalid_report_identity"
		if r.Result.AgentReport == nil {
			reason = "missing_agent_report"
		}
		return s.recordPublicationCheck(ctx, p.ID, r.ScheduledRunID, reason)
	}
	summary := report.Body
	if summary == "" || outputpolicy.PublicReportText(summary) != summary {
		return s.recordPublicationCheck(ctx, p.ID, r.ScheduledRunID, "unsafe_agent_report")
	}
	origin, err := reportOriginFingerprint(r)
	if err != nil {
		return err
	}
	e := Event{ID: eventID, SourceID: p.ID, TaskID: p.TaskID, RunID: r.ScheduledRunID, TenantID: p.TenantID, OriginTenant: p.OriginTenant, Title: p.Title, AgentID: report.AgentID, Status: "completed", Summary: summary, AgentReport: &report, Artifacts: []Artifact{}, CreatedAt: r.CompletedAt}
	for index, a := range r.Result.Artifacts {
		name := path.Base(strings.ReplaceAll(a.FileName, "\\", "/"))
		if name == "." || name == "" {
			name = path.Base(a.URI)
		}
		if a.Kind != "report" || name == "blocked-report.md" || s.artifacts == nil {
			continue
		}
		id := scheduler.PublicRunArtifactID(r.ScheduledRunID, index, a)
		content, err := s.artifacts.OpenRunArtifact(ctx, scheduler.OpenRunArtifactQuery{TenantID: p.OriginTenant, ScheduledTaskID: p.TaskID, ScheduledRunID: r.ScheduledRunID, ArtifactID: id})
		if err != nil {
			return err
		}
		// Opening the finalized artifact verifies its durable snapshot reference.
		content.Reader.Close()
		if content.SizeBytes > 0 {
			e.Artifacts = append(e.Artifacts, Artifact{ID: id, Name: content.FileName, Size: content.SizeBytes})
		}
	}
	if r.Status == scheduler.RunStatusFailed {
		e.Status = "failed"
		if len(e.Artifacts) > 0 {
			e.Status = "partial"
		}
	}
	members, err := s.Members(ctx, Viewer{TenantID: p.TenantID, Manage: true})
	if err != nil {
		return err
	}
	type recipient struct {
		human, reason string
		channels      []string
	}
	people := []recipient{}
	for _, m := range members {
		if !eligible(p, m.ID) {
			continue
		}
		v := Viewer{TenantID: p.TenantID, HumanID: m.ID}
		sub, err := s.subscription(ctx, v, p.ID)
		if err != nil {
			return err
		}
		opted := sub.Enabled && !r.CreatedAt.Before(sub.Since)
		automatic := p.Audience == "all" || p.Audience == "people"
		if !automatic && !opted {
			continue
		}
		if e.Status == "failed" && !p.IncludeFailures && !(opted && sub.IncludeFailures) {
			continue
		}
		pref, err := s.Preferences(ctx, v)
		if err != nil {
			return err
		}
		ch := pref.Channels
		reason := "organization"
		if opted {
			ch = sub.Channels
			if !automatic {
				reason = "subscription"
			} else {
				reason = "organization_and_subscription"
			}
		}
		people = append(people, recipient{m.ID, reason, ch})
	}
	return s.store.WithTx(ctx, func(tx db.Tx) error {
		// Locks the rule against concurrent edits for this short fan-out transaction.
		locked, err := tx.ExecContext(ctx, "UPDATE notification_sources SET revision=revision WHERE id=? AND revision=?", p.ID, p.Revision)
		if err != nil {
			return err
		}
		n, err := locked.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrConflict
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM notification_publication_checks WHERE source_id=? AND run_id=?", p.ID, r.ScheduledRunID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, "INSERT INTO notification_events(id,source_id,tenant_id,created_at,payload) VALUES(?,?,?,?,?) ON CONFLICT DO NOTHING", e.ID, p.ID, p.TenantID, e.CreatedAt.UnixNano(), encode(e))
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		if err != nil || n == 0 {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO notification_report_origins(event_id,origin_sha256,event_sha256) VALUES(?,?,?)", e.ID, origin, reportHash([]byte(encode(e)))); err != nil {
			return err
		}
		for _, person := range people {
			inbox := key(e.ID, p.TenantID, person.human)
			_, err = tx.ExecContext(ctx, "INSERT INTO notification_inbox(id,event_id,source_id,tenant_id,human_id,reason,created_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT DO NOTHING", inbox, e.ID, p.ID, p.TenantID, person.human, person.reason, e.CreatedAt.UnixNano())
			if err != nil {
				return err
			}
			for _, ch := range person.channels {
				if s.externalReportRelay != nil && s.externalReportRelay(e, person.human, ch) {
					continue
				}
				_, err = tx.ExecContext(ctx, "INSERT INTO notification_pushes(id,inbox_id,tenant_id,human_id,channel,status,updated_at) VALUES(?,?,?,?,?,'pending',?) ON CONFLICT DO NOTHING", key(inbox, ch), inbox, p.TenantID, person.human, ch, s.now().UnixNano())
				if err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// Retain suppression reasons without storing unsafe report bodies or secrets.
func (s *Service) recordPublicationCheck(ctx context.Context, sourceID, runID, reason string) error {
	_, err := s.store.Exec(ctx, "INSERT INTO notification_publication_checks(source_id,run_id,reason,updated_at) VALUES(?,?,?,?) ON CONFLICT(source_id,run_id) DO UPDATE SET reason=excluded.reason,updated_at=excluded.updated_at", sourceID, runID, reason, s.now().UnixNano())
	return err
}

type pendingPush struct{ id, inbox, tenant, human, channel, status, gatewayID string }

func (s *Service) reconcilePushes(ctx context.Context) error {
	if s.gateway == nil {
		return nil
	}
	rows, err := s.store.Query(ctx, "SELECT id,inbox_id,tenant_id,human_id,channel,status,gateway_id FROM notification_pushes WHERE status IN ('pending','queued','retrying','unbound') ORDER BY updated_at LIMIT 100")
	if err != nil {
		return err
	}
	pending := []pendingPush{}
	for rows.Next() {
		var p pendingPush
		if err = rows.Scan(&p.id, &p.inbox, &p.tenant, &p.human, &p.channel, &p.status, &p.gatewayID); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var failures []error
	for _, p := range pending {
		if err = s.processPush(ctx, p); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
func (s *Service) pushState(ctx context.Context, p pendingPush, status, message, gatewayID string) error {
	_, err := s.store.Exec(ctx, "UPDATE notification_pushes SET status=?,last_error=?,gateway_id=?,updated_at=? WHERE id=?", status, message, gatewayID, s.now().UnixNano(), p.id)
	return err
}
func (s *Service) processPush(ctx context.Context, p pendingPush) error {
	v := Viewer{TenantID: p.tenant, HumanID: p.human}
	item, err := s.Item(ctx, v, p.inbox)
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden) {
		return s.pushState(ctx, p, "cancelled", "接收权限已变化", p.gatewayID)
	}
	if err != nil {
		return err
	}
	allowed, err := s.eligiblePush(ctx, item, v, p.channel)
	if err != nil {
		return err
	}
	if !allowed {
		return s.pushState(ctx, p, "cancelled", "通知规则或接收偏好已变化", p.gatewayID)
	}
	if p.gatewayID != "" {
		delivery, err := s.gateway.Get(ctx, p.tenant, p.gatewayID)
		if err != nil {
			return err
		}
		status := delivery.Status
		switch status {
		case "pending":
			status = "queued"
		case "sending":
			status = "queued"
		case "delivered":
			status = "delivered"
		case "failed", "dead_letter":
			status = "failed"
		case "cancelled":
			status = "cancelled"
		default:
			status = "retrying"
		}
		message := ""
		if delivery.LastError != "" {
			message = "渠道发送暂未完成，请查看绑定状态或由管理员检查投递记录"
		}
		return s.pushState(ctx, p, status, message, p.gatewayID)
	}
	body := item.Event.Summary
	// Report notices use the gateway's informational event; the task-specific
	// identity and permission guard remain attached to the idempotency key.
	deliveries, err := s.gateway.Enqueue(ctx, outbound.EnqueueCommand{TenantID: p.tenant, HumanProfileID: p.human, ChannelKeys: []string{p.channel}, EventType: "mention", Body: body, Format: "text", IdempotencyKey: "task-notification:" + p.id})
	if errors.Is(err, outbound.ErrBindingMissing) {
		return s.pushState(ctx, p, "unbound", "该渠道尚未绑定", "")
	}
	if err != nil {
		return err
	}
	if len(deliveries) != 1 {
		return errors.New("notification delivery receipt missing")
	}
	return s.pushState(ctx, p, "queued", "", deliveries[0].ID)
}
func (s *Service) eligiblePush(ctx context.Context, item Item, v Viewer, ch string) (bool, error) {
	if s.externalReportRelay != nil && s.externalReportRelay(item.Event, v.HumanID, ch) {
		return false, nil
	}
	if report := item.Event.AgentReport; report == nil || report.Body == "" ||
		report.Body != item.Event.Summary || outputpolicy.PublicReportText(report.Body) != report.Body || report.AgentID != item.Event.AgentID || report.RuntimeRunID == "" {
		return false, nil
	}
	p, err := s.loadSource(ctx, item.Event.SourceID)
	if err != nil {
		return false, err
	}
	if !p.Enabled || p.TenantID != v.TenantID || p.TaskID != item.Event.TaskID || p.OriginTenant != item.Event.OriginTenant || !eligible(p, v.HumanID) {
		return false, nil
	}
	if current, err := s.reportStillCurrent(ctx, p, item.Event); err != nil || !current {
		return false, err
	}
	sub, err := s.subscription(ctx, v, p.ID)
	if err != nil {
		return false, err
	}
	opted := sub.Enabled && !item.Event.CreatedAt.Before(sub.Since)
	if item.Event.Status == "failed" && !p.IncludeFailures && !(opted && sub.IncludeFailures) {
		return false, nil
	}
	if opted {
		return contains(sub.Channels, ch), nil
	}
	if item.Reason == "subscription" || p.Audience == "subscribers" {
		return false, nil
	}
	pref, err := s.Preferences(ctx, v)
	return contains(pref.Channels, ch), err
}

// SetExternalReportRelay reserves channels already covered by the personal
// assistant's durable result relay, while retaining the in-app result inbox.
func (s *Service) SetExternalReportRelay(relay func(Event, string, string) bool) {
	s.externalReportRelay = relay
}

// ValidateDelivery composes with the existing approval/attention send guard.
func (s *Service) ValidateDelivery(ctx context.Context, d outbound.Delivery) (bool, error) {
	if !strings.HasPrefix(d.IdempotencyKey, "task-notification:") {
		return true, nil
	}
	id := strings.TrimPrefix(d.IdempotencyKey, "task-notification:")
	var inbox, tenant, human, ch string
	err := s.store.QueryRow(ctx, "SELECT inbox_id,tenant_id,human_id,channel FROM notification_pushes WHERE id=?", id).Scan(&inbox, &tenant, &human, &ch)
	if err != nil {
		return false, err
	}
	if d.HumanProfileID != human || d.TenantID != tenant || d.ChannelKey != ch {
		return false, nil
	}
	v := Viewer{TenantID: tenant, HumanID: human}
	item, err := s.Item(ctx, v, inbox)
	if errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if d.Body != item.Event.Summary || d.Title != "" {
		return false, nil
	}
	return s.eligiblePush(ctx, item, v, ch)
}
func (s *Service) OpenArtifact(ctx context.Context, v Viewer, itemID, artifactID string) (Content, error) {
	item, err := s.Item(ctx, v, itemID)
	if err != nil {
		return Content{}, err
	}
	found := false
	for _, a := range item.Event.Artifacts {
		if a.ID == artifactID {
			found = true
		}
	}
	if !found || s.artifacts == nil {
		return Content{}, ErrNotFound
	}
	source, err := s.loadSource(ctx, item.Event.SourceID)
	if err != nil {
		return Content{}, err
	}
	c, err := s.artifacts.OpenRunArtifact(ctx, scheduler.OpenRunArtifactQuery{TenantID: source.OriginTenant, ScheduledTaskID: source.TaskID, ScheduledRunID: item.Event.RunID, ArtifactID: artifactID})
	if err != nil {
		return Content{}, err
	}
	return Content{Reader: c.Reader, Name: c.FileName, ContentType: c.ContentType, Size: c.SizeBytes}, nil
}
