package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/app/reliability"
	gwoutbound "orrery_v2/internal/gateway/outbound"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
)

const productionHILFeishuAccountID = "orrery"

var productionHILMarkdownImagePattern = regexp.MustCompile(`!\[([^\]\r\n]*)\]\(([^)\r\n]+)\)`)

const (
	productionHILNotificationPollDelay   = 30 * time.Second
	productionHILNotificationRetryDelay  = time.Minute
	productionHILNotificationBodyRunes   = 8000
	productionHILNotificationMaxAttempts = 8
)

type productionHILWorkflowNotificationStore interface {
	GetWorkflowInstance(context.Context, string, string) (hil.WorkflowInstanceDetail, error)
	CompleteWorkflowNotification(context.Context, hil.WorkflowNotificationResult) error
}

type productionHILGatewayDeliveries interface {
	Enqueue(context.Context, gwoutbound.EnqueueCommand) ([]gwoutbound.Delivery, error)
	Get(context.Context, string, string) (gwoutbound.Delivery, error)
}

type productionHILWorkflowTenantLister interface {
	ListWorkflowTenants(context.Context, time.Time) ([]string, error)
}

type productionHILWorkflowLifecycle interface {
	SweepDue(context.Context, string, time.Time, int) (hil.WorkflowSweepResult, error)
}

type productionHILWorkflowOutbox interface {
	ClaimWorkflowNotifications(context.Context, string, time.Time, int) ([]hil.WorkflowNotification, error)
}

type productionHILWorkflowDispatchPort interface {
	Dispatch(context.Context, hil.WorkflowNotification) error
}

type productionHILWorkflowTenantStore struct {
	store *db.Store
}

func (s productionHILWorkflowTenantStore) ListWorkflowTenants(ctx context.Context, now time.Time) ([]string, error) {
	if s.store == nil || now.IsZero() {
		return nil, fmt.Errorf("production HIL workflow tenant store is invalid")
	}
	rows, err := s.store.Query(ctx, `
SELECT tenant_id FROM hil_workflow_instances WHERE status = ?
UNION
SELECT tenant_id FROM hil_workflow_notification_outbox
WHERE status IN (?, ?) AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
ORDER BY tenant_id`, hil.WorkflowRunning, hil.WorkflowNotificationPending, hil.WorkflowNotificationFailed, now.UTC().UnixNano())
	if err != nil {
		return nil, fmt.Errorf("list production HIL workflow tenants: %w", err)
	}
	defer rows.Close()
	tenants := []string{}
	for rows.Next() {
		var tenantID string
		if err := rows.Scan(&tenantID); err != nil {
			return nil, fmt.Errorf("scan production HIL workflow tenant: %w", err)
		}
		if tenantID = strings.TrimSpace(tenantID); tenantID != "" {
			tenants = append(tenants, tenantID)
		}
	}
	return tenants, rows.Err()
}

type productionHILWorkflowSweeper struct {
	tenants    productionHILWorkflowTenantLister
	lifecycle  productionHILWorkflowLifecycle
	outbox     productionHILWorkflowOutbox
	dispatcher productionHILWorkflowDispatchPort
	now        func() time.Time
	batchSize  int
}

func (s productionHILWorkflowSweeper) Sweep(ctx context.Context) error {
	if s.tenants == nil || s.lifecycle == nil || s.outbox == nil || s.dispatcher == nil {
		return fmt.Errorf("production HIL workflow sweeper dependencies are required")
	}
	now := time.Now().UTC()
	if s.now != nil {
		now = s.now().UTC()
	}
	limit := s.batchSize
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	tenants, err := s.tenants.ListWorkflowTenants(ctx, now)
	if err != nil {
		return err
	}
	var sweepErrors []error
	for _, tenantID := range tenants {
		if _, err := s.lifecycle.SweepDue(ctx, tenantID, now, limit); err != nil {
			sweepErrors = append(sweepErrors, fmt.Errorf("sweep HIL workflow deadlines for tenant %s: %w", tenantID, err))
			continue
		}
		notifications, err := s.outbox.ClaimWorkflowNotifications(ctx, tenantID, now, limit)
		if err != nil {
			sweepErrors = append(sweepErrors, fmt.Errorf("claim HIL workflow notifications for tenant %s: %w", tenantID, err))
			continue
		}
		for _, notification := range notifications {
			if err := s.dispatcher.Dispatch(ctx, notification); err != nil {
				sweepErrors = append(sweepErrors, fmt.Errorf("dispatch HIL workflow notification %s: %w", notification.ID, err))
			}
		}
	}
	return errors.Join(sweepErrors...)
}

func registerProductionHILWorkflowSweeper(rt *platform.Runtime, core *db.Store, wiring productionHILAutoplanWiring, profiles productionHILProfileDirectory, deliveries productionHILGatewayDeliveries, failures productionNotificationFailureCapture) error {
	if rt == nil || core == nil || wiring.workflowService == nil || wiring.workflowRepository == nil || profiles == nil || deliveries == nil {
		return fmt.Errorf("production HIL workflow sweeper dependencies are required")
	}
	dispatcher := productionHILWorkflowNotificationDispatcher{
		workflows: wiring.workflowRepository, profiles: profiles, deliveries: deliveries,
		accountID: productionHILFeishuAccountID, publicOrigin: rt.Config.DeviceDelivery.PublicOrigin, now: rt.Clock.Now,
		failures: failures,
	}
	return rt.RegisterSweeper("hil.approval_workflow", productionHILWorkflowSweeper{
		tenants: productionHILWorkflowTenantStore{store: core}, lifecycle: wiring.workflowService,
		outbox: wiring.workflowRepository, dispatcher: dispatcher, now: rt.Clock.Now, batchSize: rt.Config.Sweeper.BatchSize,
	})
}

type productionHILWorkflowNotificationDispatcher struct {
	workflows    productionHILWorkflowNotificationStore
	profiles     productionHILProfileDirectory
	deliveries   productionHILGatewayDeliveries
	accountID    string
	publicOrigin string
	now          func() time.Time
	failures     productionNotificationFailureCapture
}

func (d productionHILWorkflowNotificationDispatcher) Dispatch(ctx context.Context, notification hil.WorkflowNotification) error {
	now := time.Now().UTC()
	if d.now != nil {
		now = d.now().UTC()
	}
	if d.workflows == nil || d.profiles == nil || d.deliveries == nil || notification.Channel != "feishu" ||
		notification.Recipient.Kind != hil.ActorHuman || strings.TrimSpace(notification.Recipient.ID) == "" {
		return fmt.Errorf("production HIL notification dependencies or target are invalid")
	}
	detail, err := d.workflows.GetWorkflowInstance(ctx, notification.TenantID, notification.WorkflowInstanceID)
	if err != nil {
		return d.completeFailure(ctx, notification, now, "approval workflow material is unavailable")
	}
	if !productionHILNotificationIsBusinessDecision(detail.Decision.Action, detail.Decision.ActionTypeID, detail.Decision.PayloadPreview) {
		return d.completeSuppressed(ctx, notification, now)
	}
	if !productionHILNotificationActionable(detail, notification, now) {
		return d.workflows.CompleteWorkflowNotification(ctx, hil.WorkflowNotificationResult{
			TenantID: notification.TenantID, NotificationID: notification.ID, Cancelled: true,
			Failure: "approval notification is no longer actionable", ProviderReceipt: notification.ProviderReceipt, CompletedAt: now,
		})
	}
	if deliveryID := strings.TrimSpace(notification.ProviderReceipt); deliveryID != "" {
		delivery, err := d.deliveries.Get(ctx, "", deliveryID)
		if err != nil {
			return d.completePending(ctx, notification, deliveryID, now, "gateway delivery status is temporarily unavailable")
		}
		return d.completeGatewayDelivery(ctx, notification, delivery, now)
	}
	profile, err := d.profiles.GetProfile(ctx, agentcore.AgentProfileID(notification.Recipient.ID))
	if err != nil || !profile.Enabled || profile.AgentKind != agentcore.AgentKindHuman || !productionHILTenantAllowsProfile(notification.TenantID, profile.OrgIdentity) {
		return d.completeFailure(ctx, notification, now, "approval recipient profile is unavailable")
	}
	accountID := strings.TrimSpace(d.accountID)
	if accountID == "" {
		accountID = productionHILFeishuAccountID
	}
	title, body := productionHILApprovalNotificationContent(detail, notification, d.publicOrigin)
	command := gwoutbound.EnqueueCommand{
		HumanProfileID: notification.Recipient.ID,
		ChannelKeys:    []string{"feishu"},
		AccountID:      accountID,
		EventType:      "approval",
		Title:          title,
		Body:           body,
		Format:         "markdown",
		IdempotencyKey: productionHILNotificationDeliveryKey(notification),
	}
	bindingTenants := productionHILNotificationBindingTenants(notification.TenantID, profile)
	var deliveries []gwoutbound.Delivery
	for _, tenantID := range bindingTenants {
		command.TenantID = tenantID
		deliveries, err = d.deliveries.Enqueue(ctx, command)
		if err == nil {
			break
		}
		if !errors.Is(err, gwoutbound.ErrBindingMissing) {
			break
		}
	}
	if err != nil || len(deliveries) != 1 || strings.TrimSpace(deliveries[0].ID) == "" {
		if errors.Is(err, gwoutbound.ErrBindingMissing) {
			return d.completeFailure(ctx, notification, now, "approval notification binding is missing")
		}
		return d.completeFailure(ctx, notification, now, "approval notification could not be queued")
	}
	return d.completeGatewayDelivery(ctx, notification, deliveries[0], now)
}

func productionHILNotificationActionable(detail hil.WorkflowInstanceDetail, notification hil.WorkflowNotification, now time.Time) bool {
	if detail.Instance.Status != hil.WorkflowRunning || detail.Decision.Status != hil.DecisionPending ||
		(!detail.Instance.ExpiresAt.IsZero() && !now.Before(detail.Instance.ExpiresAt)) {
		return false
	}
	for _, task := range detail.Tasks {
		if task.ID == notification.TaskID {
			return task.Status == hil.TaskPending && task.CurrentAssignee == notification.Recipient &&
				(task.DeadlineAt.IsZero() || now.Before(task.DeadlineAt))
		}
	}
	return false
}

func (d productionHILWorkflowNotificationDispatcher) completeSuppressed(ctx context.Context, notification hil.WorkflowNotification, now time.Time) error {
	return d.workflows.CompleteWorkflowNotification(ctx, hil.WorkflowNotificationResult{
		TenantID: notification.TenantID, NotificationID: notification.ID, Cancelled: true,
		Failure: "non-business decision notification suppressed", CompletedAt: now,
	})
}

func productionHILNotificationIsBusinessDecision(action, actionTypeID string, payload map[string]any) bool {
	for _, value := range []string{actionTypeID, action} {
		value = strings.ToLower(strings.TrimSpace(value))
		if actioncontract.IsHumanBusinessDecisionAction(actioncontract.ActionTypeID(value)) {
			return true
		}
		switch value {
		case "business_approval", "todo.execution_plan.approve", responsibilityAutoplanHILAction:
			return true
		}
	}
	if payload["business_approval_material"] != nil {
		return true
	}
	toolName := firstNonBlank(
		productionHILPreviewString(payload, "approval_tool_name"),
		productionHILPreviewString(payload, "approval_native_tool_name"),
		productionHILPreviewString(payload, "tool_name"),
	)
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case hil.BusinessApprovalToolName, "email_send", marketingWechatDraftApprovalTool:
		return true
	default:
		return false
	}
}

func (d productionHILWorkflowNotificationDispatcher) completeGatewayDelivery(ctx context.Context, notification hil.WorkflowNotification, delivery gwoutbound.Delivery, now time.Time) error {
	notification.ProviderReceipt = delivery.ID
	switch delivery.Status {
	case "delivered":
		receipt := strings.TrimSpace(delivery.ExternalMessageID)
		if receipt == "" {
			return d.completeFailure(ctx, notification, now, "Feishu returned no external message receipt")
		}
		return d.workflows.CompleteWorkflowNotification(ctx, hil.WorkflowNotificationResult{
			TenantID: notification.TenantID, NotificationID: notification.ID, Delivered: true,
			ProviderReceipt: receipt, CompletedAt: now,
		})
	case "pending", "sending", "retrying", "":
		return d.completePending(ctx, notification, delivery.ID, now, "")
	case "failed", "dead_letter":
		return d.completeFailure(ctx, notification, now, "approval notification delivery did not complete")
	default:
		return d.completePending(ctx, notification, delivery.ID, now, "gateway delivery status is not terminal")
	}
}

func (d productionHILWorkflowNotificationDispatcher) completePending(ctx context.Context, notification hil.WorkflowNotification, deliveryID string, now time.Time, failure string) error {
	if failure != "" {
		if err := captureProductionNotificationFailure(ctx, d.failures, productionNotificationFailure{
			ID: "hil-notification:" + notification.ID + ":delivery-status-unavailable", TenantID: notification.TenantID,
			Channel: notification.Channel, AccountID: firstNonBlank(d.accountID, productionHILFeishuAccountID), HumanProfileID: notification.Recipient.ID,
			Source: "workflow_notification", Code: "delivery_status_unavailable", Message: failure, Category: reliability.FailureCategoryTransient,
			CorrelationID: notification.WorkflowInstanceID, TaskID: notification.TaskID, OccurredAt: now,
		}); err != nil {
			return err
		}
	}
	return d.workflows.CompleteWorkflowNotification(ctx, hil.WorkflowNotificationResult{
		TenantID: notification.TenantID, NotificationID: notification.ID, Pending: true,
		ProviderReceipt: strings.TrimSpace(deliveryID), Failure: failure,
		NextAttemptAt: now.Add(productionHILNotificationPollDelay), CompletedAt: now,
	})
}

func (d productionHILWorkflowNotificationDispatcher) completeFailure(ctx context.Context, notification hil.WorkflowNotification, now time.Time, failure string) error {
	code := diagnosticComponent(failure)
	category := reliability.FailureCategoryTransient
	if failure == "approval notification binding is missing" || failure == "approval recipient profile is unavailable" {
		category = reliability.FailureCategoryConfiguration
	}
	if err := captureProductionNotificationFailure(ctx, d.failures, productionNotificationFailure{
		ID:       fmt.Sprintf("hil-notification:%s:%d:%s", notification.ID, notification.AttemptCount, code),
		TenantID: notification.TenantID, Channel: notification.Channel, AccountID: firstNonBlank(d.accountID, productionHILFeishuAccountID),
		HumanProfileID: notification.Recipient.ID, Source: "workflow_notification", Code: code,
		Message: failure, Category: category, CorrelationID: notification.WorkflowInstanceID, TaskID: notification.TaskID, OccurredAt: now,
	}); err != nil {
		return err
	}
	delay := productionHILNotificationRetryDelay
	for attempt := 1; attempt < notification.AttemptCount && delay < time.Hour; attempt++ {
		delay *= 2
	}
	if delay > time.Hour {
		delay = time.Hour
	}
	return d.workflows.CompleteWorkflowNotification(ctx, hil.WorkflowNotificationResult{
		TenantID: notification.TenantID, NotificationID: notification.ID, Failure: failure,
		ProviderReceipt: notification.ProviderReceipt, NeedsAttention: notification.AttemptCount >= productionHILNotificationMaxAttempts,
		NextAttemptAt: now.Add(delay), CompletedAt: now,
	})
}

func productionHILNotificationBindingTenants(workflowTenant string, profile agentcore.AgentProfile) []string {
	workflowTenant = strings.TrimSpace(workflowTenant)
	organizationID := strings.TrimSpace(string(profile.OrgIdentity.OrganizationRef.ID))
	if workflowTenant == productionHILTenantID && organizationID != "" && organizationID != workflowTenant {
		return []string{organizationID, workflowTenant}
	}
	return []string{workflowTenant}
}

func productionHILNotificationDeliveryKey(notification hil.WorkflowNotification) string {
	return strings.TrimSpace(notification.IdempotencyKey)
}

func productionHILApprovalNotificationContent(detail hil.WorkflowInstanceDetail, notification hil.WorkflowNotification, publicOrigin string) (string, string) {
	preview := detail.Decision.PayloadPreview
	if material, ok := productionHILBusinessApprovalMaterial(preview); ok {
		return productionHILBusinessApprovalNotificationContent(detail, notification, publicOrigin, material)
	}
	materialTitle := firstNonBlank(productionHILPreviewString(preview, "title"), detail.Decision.Purpose, productionMarketingWorkflowTemplateName)
	title := "待审批：" + materialTitle
	lines := []string{
		"**有一项审批需要你处理**",
		"",
		"- 标题：" + materialTitle,
		"- 申请人：" + detail.Case.Requester.ID,
		"- 风险级别：" + string(detail.Decision.RiskLevel),
		"- 材料哈希：" + firstNonBlank(productionHILPreviewString(preview, "material_hash"), detail.Instance.MaterialHash),
	}
	if deadline := productionHILNotificationDeadline(detail, notification.TaskID); !deadline.IsZero() {
		lines = append(lines, "- 截止时间："+deadline.UTC().Format(time.RFC3339))
	}
	if summary := productionHILPreviewString(preview, "summary"); summary != "" {
		lines = append(lines, "", "**摘要**", summary)
	}
	if artifacts := productionHILPreviewArtifacts(preview); len(artifacts) > 0 {
		lines = append(lines, "", "**审批附件**")
		lines = append(lines, artifacts...)
	}
	if content := productionHILPreviewString(preview, "content"); content != "" {
		lines = append(lines, "", "**审批材料正文**", truncateProductionHILRunes(productionHILFeishuSafeMarkdown(content), 4000))
	}
	if fields := productionHILPreviewFields(preview); len(fields) > 0 {
		lines = append(lines, "", "**关键字段**")
		keys := make([]string, 0, len(fields))
		for key := range fields {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			lines = append(lines, "- "+key+"："+fields[key])
		}
	}
	if origin := strings.TrimRight(strings.TrimSpace(publicOrigin), "/"); origin != "" {
		lines = append(lines, "", "[打开 Orrery 审批中心]("+origin+"/approvals?mode=hil&workflow_id="+url.QueryEscape(detail.Instance.ID)+")")
	}
	body := truncateProductionHILRunes(strings.Join(lines, "\n"), productionHILNotificationBodyRunes)
	return truncateProductionHILRunes(title, 1000), body
}

func productionHILBusinessApprovalMaterial(preview map[string]any) (hil.BusinessApprovalMaterial, bool) {
	encoded, err := json.Marshal(preview["business_approval_material"])
	if err != nil || string(encoded) == "null" {
		return hil.BusinessApprovalMaterial{}, false
	}
	var material hil.BusinessApprovalMaterial
	if err := json.Unmarshal(encoded, &material); err != nil {
		return hil.BusinessApprovalMaterial{}, false
	}
	material, err = hil.NormalizeBusinessApprovalMaterial(material)
	return material, err == nil
}

func productionHILBusinessApprovalNotificationContent(detail hil.WorkflowInstanceDetail, notification hil.WorkflowNotification, publicOrigin string, material hil.BusinessApprovalMaterial) (string, string) {
	lines := []string{
		"**有一项审批需要你处理**",
		"",
		"- 标题：" + material.Title,
		"- 申请人：" + detail.Case.Requester.ID,
	}
	if deadline := productionHILNotificationDeadline(detail, notification.TaskID); !deadline.IsZero() {
		lines = append(lines, "- 截止时间："+deadline.UTC().Format(time.RFC3339))
	}
	lines = append(lines,
		"", "**审批内容**",
		"- 需要你决定："+material.DecisionRequest,
		"- 业务背景："+material.BusinessContext,
		"- 拟执行动作："+material.ProposedAction,
		"- 预期结果："+material.ExpectedResult,
		"- 影响范围："+material.ImpactScope,
		"", "**风险与应对**",
	)
	for index, risk := range material.Risks {
		lines = append(lines, fmt.Sprintf("- 风险 %d：%s；应对措施：%s", index+1, risk.Description, risk.Mitigation))
	}
	if artifacts := productionHILBusinessApprovalArtifacts(detail.Decision.PayloadPreview["artifacts"]); len(artifacts) > 0 {
		lines = append(lines, "", "**审批附件**")
		lines = append(lines, artifacts...)
	}
	if origin := strings.TrimRight(strings.TrimSpace(publicOrigin), "/"); origin != "" {
		lines = append(lines, "", "[打开 Orrery 审批中心]("+origin+"/approvals?mode=approval&workflow_id="+url.QueryEscape(detail.Instance.ID)+")")
	}
	body := truncateProductionHILRunes(productionHILFeishuSafeMarkdown(strings.Join(lines, "\n")), productionHILNotificationBodyRunes)
	return truncateProductionHILRunes("待审批："+material.Title, 1000), body
}

func productionHILBusinessApprovalArtifacts(value any) []string {
	artifacts, _ := value.([]any)
	lines := make([]string, 0, len(artifacts))
	for _, raw := range artifacts {
		artifact, _ := raw.(map[string]any)
		label := strings.TrimSpace(fmt.Sprint(artifact["label"]))
		artifactURL := strings.TrimSpace(fmt.Sprint(artifact["url"]))
		if label == "" || label == "<nil>" || artifactURL == "" || artifactURL == "<nil>" {
			continue
		}
		lines = append(lines, "- ["+strings.ReplaceAll(label, "]", "")+"]("+artifactURL+")")
	}
	return lines
}

func productionHILNotificationDeadline(detail hil.WorkflowInstanceDetail, taskID string) time.Time {
	for _, task := range detail.Tasks {
		if task.ID == taskID {
			return task.DeadlineAt
		}
	}
	return detail.Instance.ExpiresAt
}

func productionHILPreviewString(preview map[string]any, key string) string {
	value, _ := preview[key].(string)
	return strings.TrimSpace(value)
}

func productionHILPreviewFields(preview map[string]any) map[string]string {
	result := map[string]string{}
	switch fields := preview["fields"].(type) {
	case map[string]any:
		for key, value := range fields {
			if text := strings.TrimSpace(fmt.Sprint(value)); text != "" && text != "<nil>" {
				result[strings.TrimSpace(key)] = text
			}
		}
	case map[string]string:
		for key, value := range fields {
			if value = strings.TrimSpace(value); value != "" {
				result[strings.TrimSpace(key)] = value
			}
		}
	}
	return result
}

func productionHILPreviewArtifacts(preview map[string]any) []string {
	rawArtifacts, _ := preview["artifacts"].([]any)
	lines := make([]string, 0, len(rawArtifacts))
	for _, rawArtifact := range rawArtifacts {
		artifact, _ := rawArtifact.(map[string]any)
		path := strings.TrimSpace(fmt.Sprint(artifact["path"]))
		if path == "" || path == "<nil>" {
			continue
		}
		role := strings.TrimSpace(fmt.Sprint(artifact["role"]))
		if role == "" || role == "<nil>" {
			role = "artifact"
		}
		details := make([]string, 0, 3)
		if mediaType := strings.TrimSpace(fmt.Sprint(artifact["media_type"])); mediaType != "" && mediaType != "<nil>" {
			details = append(details, mediaType)
		}
		if size := productionHILPreviewArtifactSize(artifact["size_bytes"]); size != "" {
			details = append(details, size+" bytes")
		}
		if hash := strings.TrimSpace(fmt.Sprint(artifact["sha256"])); hash != "" && hash != "<nil>" {
			details = append(details, hash)
		}
		line := "- " + role + "：" + path
		if len(details) > 0 {
			line += "（" + strings.Join(details, "，") + "）"
		}
		lines = append(lines, line)
	}
	return lines
}

func productionHILPreviewArtifactSize(value any) string {
	switch typed := value.(type) {
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		if typed >= 0 && typed == float64(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10)
		}
	}
	return ""
}

func productionHILFeishuSafeMarkdown(value string) string {
	value = productionHILMarkdownImagePattern.ReplaceAllStringFunc(value, func(image string) string {
		parts := productionHILMarkdownImagePattern.FindStringSubmatch(image)
		if len(parts) != 3 {
			return strings.ReplaceAll(image, "![", "图片：[")
		}
		label := strings.TrimSpace(parts[1])
		if label == "" {
			label = "图片"
		}
		return "图片附件：" + label + "（" + strings.TrimSpace(parts[2]) + "）"
	})
	return strings.ReplaceAll(value, "![", "图片：[")
}

func truncateProductionHILRunes(value string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit-3]) + "..."
}
