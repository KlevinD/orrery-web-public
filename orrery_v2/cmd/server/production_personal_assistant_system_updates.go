package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"orrery_v2/internal/app/identity"
	gwoutbound "orrery_v2/internal/gateway/outbound"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/graphprotocol"
)

// The old attention guard rejects this unknown version on binary rollback.
const personalAssistantSystemUpdatePrefix = "personal-assistant:attention:system-update:v1:"
const personalAssistantSystemPhaseTable = "personal_assistant_system_update_phases"
const personalAssistantSystemCursorTable = "personal_assistant_system_update_cursors"

// Destination and source are explicit; subscribing to one's organization does
// not silently authorize platform-wide or another organization's incidents.
type personalAssistantSystemRoute struct {
	TenantID       string `json:"tenant_id"`
	HumanProfileID string `json:"human_profile_id"`
	SourceTenantID string `json:"source_tenant_id"`
}

func parsePersonalAssistantSystemRoutes(raw string) ([]personalAssistantSystemRoute, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var routes []personalAssistantSystemRoute
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&routes) != nil || d.Decode(new(any)) != io.EOF || len(routes) > 100 {
		return nil, errors.New("invalid personal assistant system update routes")
	}
	seen := map[personalAssistantSystemRoute]bool{}
	for _, r := range routes {
		for _, v := range []string{r.TenantID, r.HumanProfileID, r.SourceTenantID} {
			if v == "" || strings.TrimSpace(v) != v || len(v) > 128 {
				return nil, errors.New("invalid personal assistant system update identity")
			}
		}
		if seen[r] {
			return nil, errors.New("duplicate personal assistant system update route")
		}
		seen[r] = true
	}
	return routes, nil
}

type personalAssistantSystemPhase struct {
	Route             personalAssistantSystemRoute `json:"route"`
	Kind              string                       `json:"kind"`
	ID                string                       `json:"id"`
	State             string                       `json:"state"`
	Revision          int64                        `json:"revision"`
	Category          string                       `json:"category"`
	FailureStatus     string                       `json:"failure_status,omitempty"`
	SourceAt          time.Time                    `json:"source_at"`
	Phase             string                       `json:"phase"`
	Notified          bool                         `json:"notified"`
	Queued            bool                         `json:"queued,omitempty"`
	DeliveryIDs       []string                     `json:"delivery_ids,omitempty"`
	DeliveryStatus    string                       `json:"delivery_status,omitempty"`
	DeliveredAt       time.Time                    `json:"delivered_at,omitempty"`
	LeaseUntil        time.Time                    `json:"lease_until"`
	DiagnosticsHidden bool                         `json:"diagnostics_hidden,omitempty"`
}

type personalAssistantSystemAccess func(context.Context, identity.Principal, string, graphprotocol.AccessRef, bool) (bool, error)

type productionPersonalAssistantSystemUpdates struct {
	routes     []personalAssistantSystemRoute
	jobs       *db.Store
	source     personalAssistantSystemSource
	attention  productionPersonalAssistantAttentionSweeper
	access     personalAssistantSystemAccess
	replies    productionAgentNotificationReplyWriter
	deliveries productionPersonalAssistantAttentionDeliveries
	recovery   *productionPersonalAssistantNotificationRecovery
	now        func() time.Time
}

func systemUpdateDigest(v any) string {
	raw, _ := json.Marshal(v)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
func systemUpdateRecord(v any) db.Record {
	raw, _ := json.Marshal(v)
	return db.Record{"payload": string(raw)}
}
func systemUpdateDecode[T any](record db.Record) (T, error) {
	var v T
	raw, _ := record["payload"].(string)
	err := json.Unmarshal([]byte(raw), &v)
	return v, err
}
func systemRouteKey(r personalAssistantSystemRoute) string { return systemUpdateDigest(r) }
func systemPhaseKey(r personalAssistantSystemRoute, kind, id string) string {
	return systemRouteKey(r) + "/" + systemUpdateDigest([]string{kind, id})
}
func systemNoticeKey(p personalAssistantSystemPhase) string {
	return personalAssistantSystemUpdatePrefix + url.QueryEscape(p.Route.TenantID) + ":" + url.QueryEscape(p.Route.SourceTenantID) + ":" + p.Kind + ":" + url.QueryEscape(p.ID) + ":" + p.Phase + ":recipient:" + url.QueryEscape(p.Route.HumanProfileID)
}

func (s *productionPersonalAssistantSystemUpdates) fence(ctx context.Context, r personalAssistantSystemRoute) (time.Time, error) {
	key := systemRouteKey(r)
	err := s.jobs.WithTx(ctx, func(tx db.Tx) error {
		_, err := db.InsertRecordTx(ctx, tx, personalAssistantSystemCursorTable, key, systemUpdateRecord(s.now().UTC()))
		return err
	})
	if err != nil {
		return time.Time{}, err
	}
	record, found, err := s.jobs.GetRecord(ctx, personalAssistantSystemCursorTable, key)
	if err != nil || !found {
		return time.Time{}, errors.Join(err, errors.New("system update activation fence missing"))
	}
	return systemUpdateDecode[time.Time](record)
}

func (s *productionPersonalAssistantSystemUpdates) recipient(ctx context.Context, r personalAssistantSystemRoute) (productionPersonalAssistantWeixinTarget, identity.Principal, bool, error) {
	viewer, target, found, err := s.attention.recipient(ctx, productionPersonalAssistantAttentionRecipient{TenantID: r.TenantID, HumanProfileID: r.HumanProfileID})
	if err != nil || !found {
		return target, identity.Principal{}, false, err
	}
	if !attentionHasScope(viewer, "operations.read") || (r.SourceTenantID != r.TenantID && !attentionHasScope(viewer, "operations.manage")) {
		return target, identity.Principal{}, false, nil
	}
	p := identity.NormalizePrincipal(identity.Principal{SubjectProvider: identity.ProviderLocal, Subject: r.HumanProfileID, UserID: target.OwnerID, Username: viewer.ViewerID, AgentProfileID: r.HumanProfileID, ViewerKind: identity.ViewerKindUser, TenantID: r.TenantID, CoarseScopes: viewer.PermissionScope})
	if s.access == nil {
		return target, p, false, errors.New("system update source authorizer missing")
	}
	allowed, err := s.access(ctx, p, r.SourceTenantID, graphprotocol.AccessRef{}, false)
	return target, p, allowed, err
}

// Project only the material the current owner can read. Never load host repair
// receipts for a read-only viewer, including during pending-phase refreshes.
func (s *productionPersonalAssistantSystemUpdates) readCurrent(ctx context.Context, r personalAssistantSystemRoute, kind, id string) (personalAssistantSystemUpdate, productionPersonalAssistantWeixinTarget, bool, error) {
	target, principal, allowed, err := s.recipient(ctx, r)
	if err != nil || !allowed {
		return personalAssistantSystemUpdate{}, target, false, err
	}
	manage := false
	if kind == "failure" {
		manage, err = s.access(ctx, principal, r.SourceTenantID, graphprotocol.AccessRef{}, true)
		if err != nil {
			return personalAssistantSystemUpdate{}, target, false, err
		}
	}
	u, visible, err := s.source.Current(ctx, r.SourceTenantID, kind, id, manage)
	if err != nil || !visible {
		return u, target, false, err
	}
	if u.Access.Kind != "" {
		allowed, err = s.access(ctx, principal, r.SourceTenantID, u.Access, false)
		if err != nil || !allowed {
			return u, target, false, err
		}
	}
	return u, target, true, nil
}

func (s *productionPersonalAssistantSystemUpdates) stage(ctx context.Context, r personalAssistantSystemRoute, u personalAssistantSystemUpdate) error {
	return s.jobs.WithTx(ctx, func(tx db.Tx) error {
		key := systemPhaseKey(r, u.Kind, u.ID)
		if err := db.LockRecordKeyTx(ctx, tx, personalAssistantSystemPhaseTable, key); err != nil {
			return err
		}
		record, found, err := db.GetRecordTx(ctx, tx, personalAssistantSystemPhaseTable, key)
		if err != nil {
			return err
		}
		var p personalAssistantSystemPhase
		if found {
			p, err = systemUpdateDecode[personalAssistantSystemPhase](record)
			if err != nil {
				return err
			}
		}
		viewChanged := found && p.DiagnosticsHidden != u.DiagnosticsHidden
		// A read-only projection uses the incident clock, which can precede
		// the host receipt clock. A permission change must not strand it.
		if found && (u.Revision < p.Revision || (u.Revision == p.Revision && !viewChanged && u.At.Before(p.SourceAt))) {
			return nil
		}
		// A new occurrence can briefly invalidate a host receipt while the
		// worker refreshes it. Do not reannounce the same operational phase.
		failureStatus := strings.SplitN(u.State, ":", 2)[0]
		if status, ok := u.Facts["status"]; ok {
			failureStatus = fmt.Sprint(status)
		}
		receiptGap := found && !u.DiagnosticsHidden && u.Kind == "failure" && u.Category == "system_error" && p.Category == "automatic_repair" && p.FailureStatus == failureStatus
		if !receiptGap && (!found || p.State != u.State) {
			p = personalAssistantSystemPhase{Route: r, Kind: u.Kind, ID: u.ID, State: u.State, Revision: u.Revision, Phase: systemUpdateDigest([]any{p.Phase, u.State, u.Revision, u.At.UTC()})}
		} else {
			p.Revision = u.Revision
		}
		p.SourceAt = u.At
		p.DiagnosticsHidden = u.DiagnosticsHidden
		p.FailureStatus = failureStatus
		if !receiptGap {
			p.Category = u.Category
		}
		return db.PutRecordTx(ctx, tx, personalAssistantSystemPhaseTable, key, systemUpdateRecord(p))
	})
}

func (s *productionPersonalAssistantSystemUpdates) collect(ctx context.Context, r personalAssistantSystemRoute) error {
	_, _, allowed, err := s.recipient(ctx, r)
	if err != nil || !allowed {
		return err
	}
	fence, err := s.fence(ctx, r)
	if err != nil {
		return err
	}
	for _, kind := range []string{"failure", "rsi", "execution"} {
		key := systemRouteKey(r) + "/" + kind
		cursor := personalAssistantSystemCursor{At: fence.UnixNano()}
		// SQLite's JSON date conversion has millisecond precision. Read a
		// small overlap and apply the exact activation fence to the real case.
		if kind == "rsi" {
			cursor.At = fence.Add(-time.Millisecond).UnixNano()
		}
		if record, found, err := s.jobs.GetRecord(ctx, personalAssistantSystemCursorTable, key); err != nil {
			return err
		} else if found {
			cursor, err = systemUpdateDecode[personalAssistantSystemCursor](record)
			if err != nil {
				return err
			}
		}
		rows, err := s.source.List(ctx, r.SourceTenantID, kind, cursor)
		if err != nil {
			return err
		}
		for _, row := range rows {
			u, _, visible, err := s.readCurrent(ctx, r, kind, row.ID)
			if err != nil {
				return err
			}
			if visible && !u.At.Before(fence) {
				if err := s.stage(ctx, r, u); err != nil {
					return err
				}
			}
			cursor = row.Position
		}
		if err := s.jobs.PutRecord(ctx, personalAssistantSystemCursorTable, key, systemUpdateRecord(cursor)); err != nil {
			return err
		}
	}
	return nil
}

// A single heartbeat can begin at most one explanation. The existing Agent
// writer persists five-minute backoff and the exact authored words.
func (s *productionPersonalAssistantSystemUpdates) Sweep(ctx context.Context) error {
	if s == nil || len(s.routes) == 0 {
		return nil
	}
	var failures []error
	for _, r := range s.routes {
		if err := s.collect(ctx, r); err != nil {
			failures = append(failures, err)
		}
	}
	record, _, err := s.jobs.GetRecord(ctx, personalAssistantSystemCursorTable, "pending")
	if err != nil {
		return errors.Join(append(failures, err)...)
	}
	after, _ := record["key"].(string)
	page, err := s.jobs.ListRecords(ctx, personalAssistantSystemPhaseTable, after, 100)
	if err != nil {
		return errors.Join(append(failures, err)...)
	}
	consumed := 0
	for _, entry := range page.Items {
		consumed++
		after = entry.Key
		p, err := systemUpdateDecode[personalAssistantSystemPhase](entry.Record)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if !s.routeEnabled(p.Route) {
			continue
		}
		// Host repair receipts can advance without updating the incident row.
		// Refresh known sources, including phases already queued, without a
		// second model call for an unchanged phase.
		u, _, visible, err := s.readCurrent(ctx, p.Route, p.Kind, p.ID)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if !visible {
			continue
		}
		if err := s.stage(ctx, p.Route, u); err != nil {
			failures = append(failures, err)
			continue
		}
		fresh, found, err := s.jobs.GetRecord(ctx, personalAssistantSystemPhaseTable, entry.Key)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if !found {
			continue
		}
		p, err = systemUpdateDecode[personalAssistantSystemPhase](fresh)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if p.Queued || p.Notified {
			if err := s.maintain(ctx, p); err != nil {
				failures = append(failures, err)
			}
			continue
		}
		attempted, err := s.send(ctx, p)
		if err != nil {
			failures = append(failures, err)
		}
		if attempted {
			break
		}
	}
	if consumed == len(page.Items) && len(page.Items) < 100 {
		after = ""
	}
	if err := s.jobs.PutRecord(ctx, personalAssistantSystemCursorTable, "pending", db.Record{"key": after}); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

func (s *productionPersonalAssistantSystemUpdates) routeEnabled(r personalAssistantSystemRoute) bool {
	for _, allowed := range s.routes {
		if allowed == r {
			return true
		}
	}
	return false
}

func (s *productionPersonalAssistantSystemUpdates) current(ctx context.Context, p personalAssistantSystemPhase) (personalAssistantSystemUpdate, productionPersonalAssistantWeixinTarget, bool, error) {
	if !s.routeEnabled(p.Route) {
		return personalAssistantSystemUpdate{}, productionPersonalAssistantWeixinTarget{}, false, nil
	}
	record, exists, err := s.jobs.GetRecord(ctx, personalAssistantSystemPhaseTable, systemPhaseKey(p.Route, p.Kind, p.ID))
	if err != nil || !exists {
		return personalAssistantSystemUpdate{}, productionPersonalAssistantWeixinTarget{}, false, err
	}
	fresh, err := systemUpdateDecode[personalAssistantSystemPhase](record)
	if err != nil || fresh.Phase != p.Phase {
		return personalAssistantSystemUpdate{}, productionPersonalAssistantWeixinTarget{}, false, err
	}
	u, target, found, err := s.readCurrent(ctx, p.Route, p.Kind, p.ID)
	if err != nil || !found {
		return u, target, false, err
	}
	fence, err := s.fence(ctx, p.Route)
	if err != nil || u.At.Before(fence) || u.State != p.State || u.TenantID != p.Route.SourceTenantID || u.ID != p.ID || u.Kind != p.Kind {
		return u, target, false, err
	}
	return u, target, true, nil
}

func (s *productionPersonalAssistantSystemUpdates) send(ctx context.Context, p personalAssistantSystemPhase) (bool, error) {
	if s.replies == nil || s.deliveries == nil {
		return false, errors.New("system update notification dependencies missing")
	}
	u, target, valid, err := s.current(ctx, p)
	if err != nil || !valid {
		return false, err
	}
	claimed := false
	key := systemPhaseKey(p.Route, p.Kind, p.ID)
	err = s.jobs.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, personalAssistantSystemPhaseTable, key); err != nil {
			return err
		}
		record, found, err := db.GetRecordTx(ctx, tx, personalAssistantSystemPhaseTable, key)
		if err != nil || !found {
			return err
		}
		fresh, err := systemUpdateDecode[personalAssistantSystemPhase](record)
		if err != nil {
			return err
		}
		if fresh.Phase != p.Phase || fresh.Queued || fresh.Notified || fresh.LeaseUntil.After(s.now()) {
			return nil
		}
		fresh.LeaseUntil = s.now().Add(time.Minute)
		claimed = true
		return db.PutRecordTx(ctx, tx, personalAssistantSystemPhaseTable, key, systemUpdateRecord(fresh))
	})
	if err != nil || !claimed {
		return false, err
	}
	material, _ := json.Marshal(map[string]any{"category": u.Category, "source_status": u.State, "observed_at": u.At.UTC(), "facts": u.Facts})
	noticeKey := systemNoticeKey(p)
	body, err := s.replies.Reply(ctx, target, p.Route.HumanProfileID, noticeKey, string(material))
	if err != nil {
		return true, err
	}
	_, freshTarget, valid, err := s.current(ctx, p)
	if err != nil || !valid || freshTarget != target {
		return true, err
	}
	if err := s.recovery.Maintain(ctx, productionPersonalAssistantAttentionRecipient{TenantID: p.Route.TenantID, HumanProfileID: p.Route.HumanProfileID}, target, noticeKey, "operations.read", body); err != nil {
		return true, err
	}
	deliveries, err := s.deliveries.Enqueue(ctx, gwoutbound.EnqueueCommand{TenantID: target.TenantID, HumanProfileID: p.Route.HumanProfileID, AccountID: target.AccountID, ChannelKeys: []string{"weixin"}, EventType: "mention", Body: body, Format: "text", IdempotencyKey: noticeKey})
	if err != nil {
		return true, err
	}
	var accepted []gwoutbound.Delivery
	for _, d := range deliveries {
		if d.ChannelKey == "weixin" && notificationTargetMatchesDelivery(target, d) && d.IdempotencyKey == noticeKey {
			accepted = append(accepted, d)
		}
	}
	if len(accepted) == 0 {
		return true, errors.New("system update has no Weixin delivery receipt")
	}
	err = s.jobs.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, personalAssistantSystemPhaseTable, key); err != nil {
			return err
		}
		record, found, err := db.GetRecordTx(ctx, tx, personalAssistantSystemPhaseTable, key)
		if err != nil || !found {
			return err
		}
		fresh, err := systemUpdateDecode[personalAssistantSystemPhase](record)
		if err != nil {
			return err
		}
		if fresh.Phase != p.Phase {
			return nil
		}
		fresh.Queued = true
		fresh.Notified = false
		fresh.DeliveryIDs = nil
		for _, d := range accepted {
			fresh.DeliveryIDs = append(fresh.DeliveryIDs, d.ID)
			fresh.DeliveryStatus = d.Status
			if d.Status == "delivered" {
				fresh.Notified, fresh.DeliveredAt = true, d.DeliveredAt
			}
		}
		return db.PutRecordTx(ctx, tx, personalAssistantSystemPhaseTable, key, systemUpdateRecord(fresh))
	})
	return true, err
}

func (s *productionPersonalAssistantSystemUpdates) maintain(ctx context.Context, p personalAssistantSystemPhase) error {
	_, target, valid, err := s.current(ctx, p)
	if err != nil || !valid {
		return err
	}
	key := systemNoticeKey(p)
	var deliveries []gwoutbound.Delivery
	for _, id := range p.DeliveryIDs {
		d, err := s.deliveries.Get(ctx, target.TenantID, id)
		if err != nil {
			return err
		}
		deliveries = append(deliveries, d)
	}
	// Reconcile pre-upgrade records against canonical channel receipts too.
	if len(deliveries) == 0 {
		if history, ok := s.deliveries.(interface {
			ListByIdempotencyKey(context.Context, string, string, string) ([]gwoutbound.Delivery, error)
		}); ok {
			deliveries, err = history.ListByIdempotencyKey(ctx, target.TenantID, p.Route.HumanProfileID, key)
			if err != nil {
				return err
			}
		}
	}
	for _, d := range deliveries {
		if d.ChannelKey != "weixin" || !notificationTargetMatchesDelivery(target, d) || d.IdempotencyKey != key {
			continue
		}
		valid, err := s.Validate(ctx, d)
		if err != nil {
			return err
		}
		if !valid {
			continue
		}
		if err := s.recordDelivery(ctx, p, d); err != nil {
			return err
		}
		return s.recovery.Maintain(ctx, productionPersonalAssistantAttentionRecipient{TenantID: p.Route.TenantID, HumanProfileID: p.Route.HumanProfileID}, target, key, "operations.read", d.Body)
	}
	return nil
}

func (s *productionPersonalAssistantSystemUpdates) recordDelivery(ctx context.Context, p personalAssistantSystemPhase, d gwoutbound.Delivery) error {
	return s.jobs.WithTx(ctx, func(tx db.Tx) error {
		key := systemPhaseKey(p.Route, p.Kind, p.ID)
		if err := db.LockRecordKeyTx(ctx, tx, personalAssistantSystemPhaseTable, key); err != nil {
			return err
		}
		record, found, err := db.GetRecordTx(ctx, tx, personalAssistantSystemPhaseTable, key)
		if err != nil || !found {
			return err
		}
		fresh, err := systemUpdateDecode[personalAssistantSystemPhase](record)
		if err != nil || fresh.Phase != p.Phase {
			return err
		}
		fresh.Queued, fresh.DeliveryIDs, fresh.DeliveryStatus = true, []string{d.ID}, d.Status
		fresh.Notified, fresh.DeliveredAt = d.Status == "delivered", d.DeliveredAt
		return db.PutRecordTx(ctx, tx, personalAssistantSystemPhaseTable, key, systemUpdateRecord(fresh))
	})
}

func (s *productionPersonalAssistantSystemUpdates) lookup(ctx context.Context, key string) (personalAssistantSystemPhase, bool, error) {
	var empty personalAssistantSystemPhase
	parts := strings.Split(strings.TrimPrefix(key, personalAssistantSystemUpdatePrefix), ":")
	if !strings.HasPrefix(key, personalAssistantSystemUpdatePrefix) || len(parts) != 7 || parts[5] != "recipient" {
		return empty, false, nil
	}
	values := make([]string, 4)
	for i, pos := range []int{0, 1, 3, 6} {
		v, err := url.QueryUnescape(parts[pos])
		if err != nil {
			return empty, false, nil
		}
		values[i] = v
	}
	r := personalAssistantSystemRoute{TenantID: values[0], SourceTenantID: values[1], HumanProfileID: values[3]}
	if !s.routeEnabled(r) {
		return empty, false, nil
	}
	record, found, err := s.jobs.GetRecord(ctx, personalAssistantSystemPhaseTable, systemPhaseKey(r, parts[2], values[2]))
	if err != nil || !found {
		return empty, false, err
	}
	p, err := systemUpdateDecode[personalAssistantSystemPhase](record)
	return p, err == nil && systemNoticeKey(p) == key, err
}

func (s *productionPersonalAssistantSystemUpdates) Validate(ctx context.Context, d gwoutbound.Delivery) (bool, error) {
	if s == nil || d.ChannelKey != "weixin" {
		return false, nil
	}
	p, found, err := s.lookup(ctx, d.IdempotencyKey)
	if err != nil || !found || p.Route.HumanProfileID != d.HumanProfileID {
		return false, err
	}
	_, target, valid, err := s.current(ctx, p)
	if err != nil || !valid || !notificationTargetMatchesDelivery(target, d) {
		return false, err
	}
	if s.replies == nil {
		return false, nil
	}
	return s.replies.Validate(ctx, d)
}

func (s *productionPersonalAssistantSystemUpdates) ContextNotice(ctx context.Context, d gwoutbound.Delivery) (personalAssistantContextNotice, bool, error) {
	p, found, err := s.lookup(ctx, d.IdempotencyKey)
	if err != nil || !found {
		return personalAssistantContextNotice{}, false, err
	}
	u, target, valid, err := s.current(ctx, p)
	if err != nil || !valid || d.HumanProfileID != p.Route.HumanProfileID || !notificationTargetMatchesDelivery(target, d) {
		return personalAssistantContextNotice{}, false, err
	}
	if valid, err := s.Validate(ctx, d); err != nil || !valid {
		return personalAssistantContextNotice{}, false, err
	}
	return personalAssistantContextNotice{ID: u.ID, Kind: u.Category, CurrentStatus: u.State, DeliveryStatus: d.Status, Content: productionPersonalAssistantApprovalDetailText(d.Body, 1600)}, true, nil
}
