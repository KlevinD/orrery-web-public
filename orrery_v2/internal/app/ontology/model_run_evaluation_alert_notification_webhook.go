package ontology

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultModelRunEvaluationTrendAlertNotificationWebhookChannel = "webhook"

type ModelRunEvaluationTrendAlertNotificationWebhookSenderOptions struct {
	Endpoint              string
	Channel               string
	Client                *http.Client
	Headers               map[string]string
	Now                   func() time.Time
	AllowPrivateEndpoints bool
	Resolver              ModelRunEvaluationTrendAlertNotificationEndpointResolver
	Dialer                ModelRunEvaluationTrendAlertNotificationDialer
}

type ModelRunEvaluationTrendAlertNotificationWebhookSender struct {
	endpoint              string
	channel               string
	client                *http.Client
	headers               map[string]string
	now                   func() time.Time
	allowPrivateEndpoints bool
	resolver              ModelRunEvaluationTrendAlertNotificationEndpointResolver
}

type ModelRunEvaluationTrendAlertNotificationDialer interface {
	DialContext(ctx context.Context, network string, address string) (net.Conn, error)
}

type ModelRunEvaluationTrendAlertNotificationEndpointResolver interface {
	LookupNetIP(ctx context.Context, host string) ([]netip.Addr, error)
}

type defaultModelRunEvaluationTrendAlertNotificationEndpointResolver struct{}

func (defaultModelRunEvaluationTrendAlertNotificationEndpointResolver) LookupNetIP(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

func NewModelRunEvaluationTrendAlertNotificationWebhookSender(opts ModelRunEvaluationTrendAlertNotificationWebhookSenderOptions) *ModelRunEvaluationTrendAlertNotificationWebhookSender {
	channel := strings.TrimSpace(opts.Channel)
	if channel == "" {
		channel = defaultModelRunEvaluationTrendAlertNotificationWebhookChannel
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	resolver := opts.Resolver
	if resolver == nil {
		resolver = defaultModelRunEvaluationTrendAlertNotificationEndpointResolver{}
	}
	client := opts.Client
	if client == nil {
		client = modelRunEvaluationTrendAlertNotificationWebhookHTTPClient(opts.AllowPrivateEndpoints, resolver, opts.Dialer)
	}
	return &ModelRunEvaluationTrendAlertNotificationWebhookSender{
		endpoint:              strings.TrimSpace(opts.Endpoint),
		channel:               channel,
		client:                client,
		headers:               cloneStringMetadata(opts.Headers),
		now:                   now,
		allowPrivateEndpoints: opts.AllowPrivateEndpoints,
		resolver:              resolver,
	}
}

func (s *ModelRunEvaluationTrendAlertNotificationWebhookSender) SendModelRunEvaluationTrendAlertNotification(ctx context.Context, event ModelRunEvaluationTrendAlertAuditEvent, plan ModelRunEvaluationTrendAlertNotificationPlan) (ModelRunEvaluationTrendAlertNotificationReceipt, error) {
	if s == nil || s.endpoint == "" {
		return ModelRunEvaluationTrendAlertNotificationReceipt{}, fmt.Errorf("%w: model run evaluation trend alert notification webhook endpoint is required", ErrInvalidDefinition)
	}
	if err := validateModelRunEvaluationTrendAlertNotificationWebhookEndpoint(ctx, s.endpoint, s.allowPrivateEndpoints, s.resolver); err != nil {
		return ModelRunEvaluationTrendAlertNotificationReceipt{}, err
	}
	body, err := json.Marshal(modelRunEvaluationTrendAlertNotificationWebhookPayload(event, plan))
	if err != nil {
		return ModelRunEvaluationTrendAlertNotificationReceipt{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return ModelRunEvaluationTrendAlertNotificationReceipt{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Orrery-Alert-Tenant", event.TenantID)
	req.Header.Set("X-Orrery-Alert-Rule", event.RuleID)
	for key, value := range s.headers {
		if strings.TrimSpace(key) != "" {
			req.Header.Set(strings.TrimSpace(key), value)
		}
	}
	res, err := s.client.Do(req)
	if err != nil {
		return ModelRunEvaluationTrendAlertNotificationReceipt{}, err
	}
	defer res.Body.Close()
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(readModelRunEvaluationTrendAlertNotificationWebhookError(res.Body))
		if message == "" {
			message = http.StatusText(res.StatusCode)
		}
		return ModelRunEvaluationTrendAlertNotificationReceipt{}, fmt.Errorf("send ontology alert notification webhook: status %d: %s", res.StatusCode, message)
	}
	messageID := strings.TrimSpace(res.Header.Get("X-Webhook-Message-ID"))
	if messageID == "" {
		messageID = strings.TrimSpace(res.Header.Get("X-Request-ID"))
	}
	if messageID == "" {
		messageID = "ontology-alert-webhook:" + event.TenantID + ":" + event.RuleID + ":" + s.now().UTC().Format(time.RFC3339Nano)
	}
	return ModelRunEvaluationTrendAlertNotificationReceipt{
		Channel:   s.channel,
		MessageID: messageID,
		Metadata: map[string]string{
			"http_status": strconv.Itoa(res.StatusCode),
		},
		SentAt: s.now().UTC(),
	}, nil
}

func modelRunEvaluationTrendAlertNotificationWebhookPayload(event ModelRunEvaluationTrendAlertAuditEvent, plan ModelRunEvaluationTrendAlertNotificationPlan) map[string]any {
	return map[string]any{
		"event_type":  event.EventType,
		"tenant_id":   event.TenantID,
		"rule_id":     event.RuleID,
		"severity":    event.Severity,
		"status":      event.Status,
		"reason":      plan.Decision.Reason,
		"labels":      cloneStringMetadata(event.Labels),
		"details":     cloneStringMetadata(event.Details),
		"observed_at": event.ObservedAt.UTC().Format(time.RFC3339Nano),
		"updated_at":  event.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func readModelRunEvaluationTrendAlertNotificationWebhookError(body io.Reader) string {
	if body == nil {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(body, 1024))
	if err != nil {
		return ""
	}
	return string(data)
}

func validateModelRunEvaluationTrendAlertNotificationWebhookEndpoint(ctx context.Context, endpoint string, allowPrivate bool, resolver ModelRunEvaluationTrendAlertNotificationEndpointResolver) error {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("%w: model run evaluation trend alert notification webhook endpoint must be an absolute http or https URL", ErrInvalidDefinition)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("%w: model run evaluation trend alert notification webhook endpoint scheme %q is not allowed", ErrInvalidDefinition, parsed.Scheme)
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return fmt.Errorf("%w: model run evaluation trend alert notification webhook endpoint host is required", ErrInvalidDefinition)
	}
	if allowPrivate {
		return nil
	}
	return validateModelRunEvaluationTrendAlertNotificationWebhookEndpointHost(ctx, host, resolver)
}

func validateModelRunEvaluationTrendAlertNotificationWebhookEndpointAddr(addr netip.Addr, host string) error {
	addr = addr.Unmap()
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsUnspecified() || addr.IsMulticast() {
		return fmt.Errorf("%w: model run evaluation trend alert notification webhook endpoint host %q resolved to address %q which is not allowed", ErrInvalidDefinition, host, addr)
	}
	return nil
}

func modelRunEvaluationTrendAlertNotificationWebhookHTTPClient(allowPrivate bool, resolver ModelRunEvaluationTrendAlertNotificationEndpointResolver, dialer ModelRunEvaluationTrendAlertNotificationDialer) *http.Client {
	if dialer == nil {
		dialer = &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network string, address string) (net.Conn, error) {
		if !allowPrivate {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				host = address
			}
			if err := validateModelRunEvaluationTrendAlertNotificationWebhookEndpointHost(ctx, host, resolver); err != nil {
				return nil, err
			}
		}
		return dialer.DialContext(ctx, network, address)
	}
	return &http.Client{Transport: transport}
}

func validateModelRunEvaluationTrendAlertNotificationWebhookEndpointHost(ctx context.Context, host string, resolver ModelRunEvaluationTrendAlertNotificationEndpointResolver) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("%w: model run evaluation trend alert notification webhook endpoint host is required", ErrInvalidDefinition)
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return validateModelRunEvaluationTrendAlertNotificationWebhookEndpointAddr(addr, host)
	}
	if resolver == nil {
		resolver = defaultModelRunEvaluationTrendAlertNotificationEndpointResolver{}
	}
	addrs, err := resolver.LookupNetIP(ctx, host)
	if err != nil {
		return fmt.Errorf("resolve ontology alert notification webhook endpoint host %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("%w: model run evaluation trend alert notification webhook endpoint host %q did not resolve to an address", ErrInvalidDefinition, host)
	}
	for _, addr := range addrs {
		if err := validateModelRunEvaluationTrendAlertNotificationWebhookEndpointAddr(addr, host); err != nil {
			return err
		}
	}
	return nil
}
