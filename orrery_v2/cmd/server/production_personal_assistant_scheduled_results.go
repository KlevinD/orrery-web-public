package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	schedulerapp "orrery_v2/internal/app/scheduler"
	todoapp "orrery_v2/internal/app/todo"
	gwoutbound "orrery_v2/internal/gateway/outbound"
	"orrery_v2/internal/platform/db"
)

const (
	personalAssistantScheduledResultPrefix       = "personal-assistant:scheduled-result:v2:"
	personalAssistantScheduledResultLegacyPrefix = "personal-assistant:scheduled-result:v1:"
)

type personalAssistantScheduledResultConfig struct {
	recipients []productionPersonalAssistantAttentionRecipient
	since      time.Time
	replay     map[string]bool
}

func parsePersonalAssistantScheduledResultConfig(recipients, since, replay string) (personalAssistantScheduledResultConfig, error) {
	c := personalAssistantScheduledResultConfig{replay: map[string]bool{}}
	var err error
	c.recipients, err = productionPersonalAssistantAttentionRecipients(recipients)
	if err != nil {
		return c, err
	}
	if len(c.recipients) == 0 {
		return c, nil
	}
	c.since, err = time.Parse(time.RFC3339, strings.TrimSpace(since))
	if err != nil || c.since.IsZero() {
		return c, fmt.Errorf("scheduled result notifications require a persistent RFC3339 start time")
	}
	if strings.TrimSpace(replay) != "" {
		var ids []string
		if err := json.Unmarshal([]byte(replay), &ids); err != nil || len(ids) > 100 {
			return c, fmt.Errorf("invalid scheduled result replay IDs")
		}
		for _, id := range ids {
			if strings.TrimSpace(id) != id || id == "" || len(id) > 256 {
				return c, fmt.Errorf("invalid scheduled result replay ID")
			}
			c.replay[id] = true
		}
	}
	return c, nil
}

type personalAssistantScheduledResultRepository interface {
	ListResultNotificationRuns(context.Context, time.Time, map[string]bool) ([]schedulerapp.ScheduledRun, error)
	GetRun(context.Context, string) (schedulerapp.ScheduledRun, error)
	GetTask(context.Context, string) (schedulerapp.ScheduledTask, error)
}

type personalAssistantScheduledResultTargetStore struct{ core *db.Store }

func (s personalAssistantScheduledResultTargetStore) WeixinTarget(ctx context.Context, tenant, human string) (productionPersonalAssistantWeixinTarget, bool, error) {
	return (productionPersonalAssistantApprovalNotificationStore{store: s.core}).weixinTarget(ctx, tenant, human, true)
}

type productionPersonalAssistantScheduledResultSweeper struct {
	config     personalAssistantScheduledResultConfig
	repository personalAssistantScheduledResultRepository
	resolve    func(context.Context, productionPersonalAssistantAttentionRecipient) (todoapp.ViewerRef, productionPersonalAssistantWeixinTarget, bool, error)
	deliveries productionPersonalAssistantAttentionDeliveries
	jobs       *db.Store
	recovery   *productionPersonalAssistantNotificationRecovery
	replies    *productionScheduledResultReplies
	now        func() time.Time
}

type personalAssistantScheduledResultReference struct {
	recipient                 productionPersonalAssistantAttentionRecipient
	kind, id, status, version string
	legacy                    bool
}

func personalAssistantScheduledResultKey(ref personalAssistantScheduledResultReference) string {
	prefix := personalAssistantScheduledResultPrefix
	if ref.legacy {
		prefix = personalAssistantScheduledResultLegacyPrefix
	}
	return prefix + url.QueryEscape(ref.recipient.TenantID) + ":" + ref.kind + ":" + url.QueryEscape(ref.id) + ":" + ref.status + ":" + ref.version + ":recipient:" + url.QueryEscape(ref.recipient.HumanProfileID)
}

func parsePersonalAssistantScheduledResultKey(key string) (personalAssistantScheduledResultReference, bool) {
	var ref personalAssistantScheduledResultReference
	prefix := personalAssistantScheduledResultPrefix
	if strings.HasPrefix(key, personalAssistantScheduledResultLegacyPrefix) {
		prefix, ref.legacy = personalAssistantScheduledResultLegacyPrefix, true
	}
	if !strings.HasPrefix(key, prefix) {
		return ref, false
	}
	p := strings.Split(strings.TrimPrefix(key, prefix), ":")
	if len(p) != 7 || p[5] != "recipient" || (p[1] != "run" && p[1] != "digest") {
		return ref, false
	}
	var err error
	ref.recipient.TenantID, err = url.QueryUnescape(p[0])
	if err != nil {
		return ref, false
	}
	ref.id, err = url.QueryUnescape(p[2])
	if err != nil {
		return ref, false
	}
	ref.recipient.HumanProfileID, err = url.QueryUnescape(p[6])
	if err != nil {
		return ref, false
	}
	ref.kind, ref.status, ref.version = p[1], p[3], p[4]
	n, err := strconv.ParseInt(ref.version, 10, 64)
	return ref, err == nil && n > 0 && ref.id != "" && ref.recipient.TenantID != "" && ref.recipient.HumanProfileID != "" && personalAssistantScheduledResultKey(ref) == key
}

func personalAssistantScheduledRunTime(run schedulerapp.ScheduledRun) time.Time {
	if !run.CompletedAt.IsZero() {
		return run.CompletedAt
	}
	return run.CreatedAt
}

func personalAssistantScheduledRunReference(recipient productionPersonalAssistantAttentionRecipient, run schedulerapp.ScheduledRun) personalAssistantScheduledResultReference {
	return personalAssistantScheduledResultReference{recipient: recipient, kind: "run", id: run.ScheduledRunID, status: string(run.Status), version: strconv.FormatInt(personalAssistantScheduledRunTime(run).UnixNano(), 10)}
}

func personalAssistantScheduledTenant(tenant string) string {
	if tenant = strings.TrimSpace(tenant); tenant != "" {
		return tenant
	}
	return "system"
}

func (s *productionPersonalAssistantScheduledResultSweeper) eligible(run schedulerapp.ScheduledRun) bool {
	return run.TargetMode == schedulerapp.TargetModeAgentTask && schedulerapp.ResultNotificationTerminal(run) && (!personalAssistantScheduledRunTime(run).Before(s.config.since) || s.config.replay[run.ScheduledRunID])
}

func (s *productionPersonalAssistantScheduledResultSweeper) recipient(ctx context.Context, ref personalAssistantScheduledResultReference) (productionPersonalAssistantWeixinTarget, bool, error) {
	if s == nil || s.resolve == nil {
		return productionPersonalAssistantWeixinTarget{}, false, nil
	}
	allowed := false
	for _, recipient := range s.config.recipients {
		if recipient == ref.recipient {
			allowed = true
			break
		}
	}
	if !allowed {
		return productionPersonalAssistantWeixinTarget{}, false, nil
	}
	viewer, target, found, err := s.resolve(ctx, ref.recipient)
	canRead := attentionHasScope(viewer, "scheduler.read")
	if ref.recipient.TenantID == "system" && target.TenantID != "system" {
		canRead = canRead && attentionHasScope(viewer, "scheduler.write") && attentionHasScope(viewer, "operations.manage")
	}
	return target, found && canRead, err
}

func (s *productionPersonalAssistantScheduledResultSweeper) Validate(ctx context.Context, delivery gwoutbound.Delivery) (bool, error) {
	ref, ok := parsePersonalAssistantScheduledResultKey(delivery.IdempotencyKey)
	if !ok || ref.legacy || ref.recipient.HumanProfileID != delivery.HumanProfileID || delivery.ChannelKey != "weixin" {
		return false, nil
	}
	target, found, err := s.recipient(ctx, ref)
	if err != nil || !found || !notificationTargetMatchesDelivery(target, delivery) {
		return false, err
	}
	// A queued status template or separate model explanation is not an Agent report.
	if allowed, err := s.replies.Validate(ctx, delivery); err != nil || !allowed {
		return false, err
	}
	if ref.kind == "digest" {
		return false, nil
	}
	run, err := s.repository.GetRun(ctx, ref.id)
	if errors.Is(err, schedulerapp.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	verified, err := s.replies.ValidateReport(ctx, delivery, run)
	return err == nil && verified && s.eligible(run) && personalAssistantScheduledTenant(run.TenantID) == ref.recipient.TenantID && personalAssistantScheduledResultKey(personalAssistantScheduledRunReference(ref.recipient, run)) == delivery.IdempotencyKey, err
}

func (s *productionPersonalAssistantScheduledResultSweeper) sentKeys(ctx context.Context, target productionPersonalAssistantWeixinTarget, human string) (map[string]bool, error) {
	rows, err := s.jobs.Query(ctx, `SELECT idempotency_key, status FROM gateway_message_deliveries WHERE tenant_id=? AND human_profile_id=? AND channel_key='weixin' AND account_id=? AND idempotency_key LIKE 'personal-assistant:scheduled-result:%' AND status NOT IN ('failed','dead_letter')`, target.TenantID, human, target.AccountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := map[string]bool{}
	for rows.Next() {
		var key, status string
		if err := rows.Scan(&key, &status); err != nil {
			return nil, err
		}
		if ref, ok := parsePersonalAssistantScheduledResultKey(key); ok {
			if ref.legacy && status != "delivered" {
				continue
			}
			ref.legacy = false
			keys[personalAssistantScheduledResultKey(ref)] = true
		}
	}
	return keys, rows.Err()
}

func (s *productionPersonalAssistantScheduledResultSweeper) Sweep(ctx context.Context) error {
	if s == nil || len(s.config.recipients) == 0 {
		return nil
	}
	runs, err := s.repository.ListResultNotificationRuns(ctx, s.config.since, s.config.replay)
	if err != nil {
		return err
	}
	var failures []error
	queued := 0
	for _, recipient := range s.config.recipients {
		target, found, err := s.recipient(ctx, personalAssistantScheduledResultReference{recipient: recipient})
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if !found {
			continue
		}
		existing, err := s.sentKeys(ctx, target, recipient.HumanProfileID)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		type notice struct {
			ref  personalAssistantScheduledResultReference
			runs []schedulerapp.ScheduledRun
		}
		var notices []notice
		for _, run := range runs {
			if !s.eligible(run) || personalAssistantScheduledTenant(run.TenantID) != recipient.TenantID {
				continue
			}
			if _, authored, err := s.replies.Report(ctx, run); err != nil {
				failures = append(failures, err)
				continue
			} else if !authored {
				continue
			}
			ref := personalAssistantScheduledRunReference(recipient, run)
			notices = append(notices, notice{ref, []schedulerapp.ScheduledRun{run}})
		}
		for _, notice := range notices {
			if ctx.Err() != nil {
				return errors.Join(append(failures, ctx.Err())...)
			}
			key := personalAssistantScheduledResultKey(notice.ref)
			if existing[key] {
				continue
			}
			body, err := s.replies.Reply(ctx, key, notice.runs)
			if err != nil {
				failures = append(failures, err)
				continue
			}
			delivery := gwoutbound.Delivery{IdempotencyKey: key, TenantID: target.TenantID, HumanProfileID: recipient.HumanProfileID, AccountID: target.AccountID, ChannelKey: "weixin", Body: body}
			valid, err := s.Validate(ctx, delivery)
			if err != nil {
				failures = append(failures, err)
				continue
			}
			if !valid {
				continue
			}
			if err := s.recovery.Maintain(ctx, recipient, target, key, "scheduler.read", body); err != nil {
				failures = append(failures, err)
				continue
			}
			_, err = s.deliveries.Enqueue(ctx, gwoutbound.EnqueueCommand{TenantID: target.TenantID, HumanProfileID: recipient.HumanProfileID, AccountID: target.AccountID, ChannelKeys: []string{"weixin"}, EventType: "mission_status", Body: body, Format: "text", IdempotencyKey: key})
			if err != nil {
				failures = append(failures, err)
				continue
			}
			queued++
			if queued >= 50 {
				return errors.Join(failures...)
			}
		}
	}
	return errors.Join(failures...)
}

func (s *productionPersonalAssistantScheduledResultSweeper) ContextNotice(ctx context.Context, key, status, body, human string, target productionPersonalAssistantWeixinTarget) (personalAssistantContextNotice, bool, error) {
	ref, ok := parsePersonalAssistantScheduledResultKey(key)
	if !ok || ref.recipient.HumanProfileID != human {
		return personalAssistantContextNotice{}, false, nil
	}
	currentTarget, found, err := s.recipient(ctx, ref)
	if err != nil || !found || currentTarget != target {
		return personalAssistantContextNotice{}, false, err
	}
	state := "completed"
	if ref.kind == "run" {
		run, err := s.repository.GetRun(ctx, ref.id)
		if errors.Is(err, schedulerapp.ErrNotFound) {
			return personalAssistantContextNotice{}, false, nil
		}
		if err != nil || personalAssistantScheduledTenant(run.TenantID) != ref.recipient.TenantID {
			return personalAssistantContextNotice{}, false, err
		}
		state = string(run.Status)
	} else {
		task, err := s.repository.GetTask(ctx, ref.id)
		if errors.Is(err, schedulerapp.ErrNotFound) {
			return personalAssistantContextNotice{}, false, nil
		}
		if err != nil || personalAssistantScheduledTenant(task.TenantID) != ref.recipient.TenantID {
			return personalAssistantContextNotice{}, false, err
		}
	}
	return personalAssistantContextNotice{ID: ref.id, Kind: "scheduled_task_result", DeliveryStatus: status, CurrentStatus: state, Content: productionPersonalAssistantApprovalDetailText(body, 2000)}, true, nil
}
