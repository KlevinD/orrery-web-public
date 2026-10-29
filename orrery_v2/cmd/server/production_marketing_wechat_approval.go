package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/execution"
	"orrery_v2/internal/platform/logging"
)

const (
	productionMarketingWechatApprovalResolutionTopic     = "marketing.wechat.approval.resolution"
	productionMarketingWechatApprovalResolutionJobPrefix = "marketing-wechat-approval-resolution:"
)

type productionMarketingWechatApprovalResolutionPayload = hil.MarketingWechatApprovalResolution

type productionMarketingWechatApprovalObserver struct {
	queue  *execution.QueueService
	report func(error)
}

func (o productionMarketingWechatApprovalObserver) DecisionResolved(ctx context.Context, resolution hil.DecisionResolution) {
	if o.queue == nil || strings.TrimSpace(resolution.Decision.ActionTypeID) != hil.MarketingWechatDraftApprovalActionType ||
		resolution.Decision.Resource.Kind != hil.MarketingWechatGenerationResourceKind || strings.TrimSpace(resolution.Receipt.ID) == "" {
		return
	}
	payload, err := productionMarketingWechatApprovalPayload(resolution)
	if err != nil {
		o.reportError(err)
		return
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		o.reportError(err)
		return
	}
	job := execution.Job{
		ID:    productionMarketingWechatApprovalResolutionJobPrefix + payload.ReceiptID,
		Topic: productionMarketingWechatApprovalResolutionTopic, Payload: encoded,
		QueueKey: "marketing-wechat-generation:" + payload.GenerationID, MaxAttempts: 12,
	}
	if err := o.queue.Enqueue(context.WithoutCancel(ctx), job); err != nil {
		o.reportError(fmt.Errorf("enqueue Marketing WeChat approval resolution: %w", err))
	}
}

func (o productionMarketingWechatApprovalObserver) reportError(err error) {
	if err != nil && o.report != nil {
		o.report(err)
	}
}

func productionMarketingWechatApprovalPayload(resolution hil.DecisionResolution) (productionMarketingWechatApprovalResolutionPayload, error) {
	return hil.BuildMarketingWechatApprovalResolution(resolution)
}

func marketingPreviewText(preview map[string]any, key string) string {
	value, _ := preview[key].(string)
	return value
}
func marketingPreviewInt(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case float64:
		return int(v)
	default:
		return 0
	}
}

func productionMarketingWechatApprovalStrings(value any) []string {
	switch values := value.(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, strings.TrimSpace(text))
			}
		}
		return out
	default:
		return nil
	}
}

func productionMarketingWechatApprovalResolutionDelivery(client *http.Client, callbackURL, authorization string) platform.ExecutionDeliveryHandler {
	if client == nil {
		client = http.DefaultClient
	}
	callbackURL = strings.TrimSpace(callbackURL)
	authorization = strings.TrimSpace(authorization)
	return func(ctx context.Context, delivery platform.ExecutionDelivery) error {
		if callbackURL == "" || authorization == "" {
			return fmt.Errorf("Marketing approval callback is not configured")
		}
		var payload productionMarketingWechatApprovalResolutionPayload
		if err := json.Unmarshal(delivery.Payload, &payload); err != nil {
			return fmt.Errorf("decode Marketing approval resolution: %w", err)
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, callbackURL, bytes.NewReader(encoded))
		if err != nil {
			return err
		}
		request.Header.Set("Authorization", authorization)
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			return fmt.Errorf("Marketing approval callback returned HTTP %d", response.StatusCode)
		}
		return nil
	}
}

func registerProductionMarketingWechatApprovalResolution(rt *platform.Runtime, observers *productionHILResolutionObservers, approvalAuthorization, callbackURL, callbackAuthorization string) error {
	approvalAuthorization = strings.TrimSpace(approvalAuthorization)
	callbackURL = strings.TrimSpace(callbackURL)
	callbackAuthorization = strings.TrimSpace(callbackAuthorization)
	if approvalAuthorization == "" && callbackURL == "" && callbackAuthorization == "" {
		return nil
	}
	if rt == nil || observers == nil || approvalAuthorization == "" || callbackURL == "" || callbackAuthorization == "" {
		return fmt.Errorf("Marketing WeChat approval submission and callback configuration must be complete")
	}
	if err := rt.RegisterExecutionDeliveryHandler(
		productionMarketingWechatApprovalResolutionTopic,
		productionMarketingWechatApprovalResolutionDelivery(&http.Client{Timeout: 30 * time.Second}, callbackURL, callbackAuthorization),
	); err != nil {
		return err
	}
	observers.Add(productionMarketingWechatApprovalObserver{queue: rt.ExecutionQueue, report: func(err error) {
		rt.Logger.Warn(context.Background(), "Marketing WeChat approval resolution enqueue failed", logging.Any("error", err))
	}})
	return nil
}
