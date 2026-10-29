package feishu

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkchannel "github.com/larksuite/oapi-sdk-go/v3/channel"
	larktypes "github.com/larksuite/oapi-sdk-go/v3/channel/types"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"

	"orrery_v2/internal/gateway/channel"
	"orrery_v2/internal/platform/logging"
)

type SDKClient struct {
	logger channel.Logger

	mu       sync.Mutex
	channel  larktypes.Channel
	options  Options
	started  bool
	healthy  bool
	outbound bool
	cancel   context.CancelFunc
	done     <-chan struct{}
}

const feishuStartupTimeout = 30 * time.Second
const feishuBotIdentityTimeout = 10 * time.Second
const feishuAPIRequestTimeout = 15 * time.Second
const feishuMaxAPIResponseBytes = 1 << 20

func NewSDKClient(logger channel.Logger) *SDKClient {
	return &SDKClient{logger: logger}
}

func (c *SDKClient) SetLogger(logger channel.Logger) {
	c.mu.Lock()
	c.logger = logger
	c.mu.Unlock()
}

func (c *SDKClient) Start(ctx context.Context, options Options, inbound func(context.Context, InboundEvent) error) error {
	if strings.TrimSpace(options.AppID) == "" || strings.TrimSpace(options.AppSecret) == "" {
		return errors.New("feishu app id and app secret are required")
	}
	mode := strings.ToLower(strings.TrimSpace(options.ConnectionMode))
	if mode != ModeStream && mode != ModeOutbound {
		return fmt.Errorf("feishu connection mode %q is not supported by the SDK stream client", options.ConnectionMode)
	}
	if !validFeishuDomain(options.DomainName) {
		return fmt.Errorf("feishu domain %q is not supported", options.DomainName)
	}
	if mode == ModeStream && inbound == nil {
		return errors.New("feishu inbound handler is required")
	}
	domain := lark.FeishuBaseUrl
	if strings.EqualFold(strings.TrimSpace(options.DomainName), "lark") || strings.EqualFold(strings.TrimSpace(options.DomainName), "larksuite") {
		domain = lark.LarkBaseUrl
	}
	runCtx, cancel := context.WithCancel(ctx)
	started := false
	defer func() {
		if !started {
			cancel()
		}
	}()
	logger := feishuSDKLogger{logger: c.logger, lifecycle: runCtx}
	apiClient := lark.NewClient(
		options.AppID,
		options.AppSecret,
		lark.WithOpenBaseUrl(domain),
		lark.WithLogger(logger),
		lark.WithHttpClient(newFeishuHTTPClient()),
	)
	if mode == ModeOutbound {
		ch := larkchannel.NewChannel(apiClient, nil)
		identityCtx, cancel := context.WithTimeout(ctx, feishuBotIdentityTimeout)
		identity := ch.GetBotIdentity(identityCtx)
		cancel()
		if identity == nil || strings.TrimSpace(identity.OpenID) == "" {
			return errors.New("feishu bot identity validation failed")
		}
		c.mu.Lock()
		if c.started {
			c.mu.Unlock()
			return errors.New("feishu SDK client is already started")
		}
		c.channel = ch
		c.options = options
		c.started = true
		c.healthy = true
		c.outbound = true
		c.cancel = cancel
		c.mu.Unlock()
		started = true
		return nil
	}
	eventDispatcher := dispatcher.NewEventDispatcher(options.VerifyToken, options.EncryptKey)
	eventDispatcher.InitConfig(larkevent.WithLogger(logger))
	wsClient := larkws.NewClient(
		options.AppID,
		options.AppSecret,
		larkws.WithDomain(domain),
		larkws.WithLogger(logger),
		larkws.WithEndpointValidator(feishuEndpointValidator(options.DomainName)),
		larkws.WithEventHandler(eventDispatcher),
	)
	ch := larkchannel.NewChannel(apiClient, wsClient)
	if options.MeetingEventsEnabled {
		registerMeetingEventHandlers(eventDispatcher, options, inbound)
	}
	ch.OnMessage(func(messageCtx context.Context, message *larktypes.NormalizedMessage) error {
		if message == nil {
			return errors.New("feishu normalized message is empty")
		}
		event := InboundEvent{
			Kind:           InboundEventMessage,
			EventID:        message.EventID,
			MessageID:      message.MessageID,
			AccountID:      options.AccountID(),
			ChatID:         message.ChatID,
			ChatType:       normalizeFeishuChatType(message.ChatType),
			SenderID:       message.UserID,
			Text:           message.Content,
			SenderVerified: true,
		}
		if raw, ok := message.RawEvent.(*larkim.P2MessageReceiveV1); ok && raw != nil && raw.EventV2Base != nil && raw.EventV2Base.Header != nil {
			event.TenantID = strings.TrimSpace(raw.EventV2Base.Header.TenantKey)
			if appID := strings.TrimSpace(raw.EventV2Base.Header.AppID); appID != "" && appID != options.AppID {
				return fmt.Errorf("feishu event app id does not match configured account")
			}
		}
		return inbound(messageCtx, event)
	})
	ch.OnError(func(err error) {
		if err != nil && c.logger != nil {
			c.logger.Error("feishu channel error", map[string]any{"error_code": "channel_error", "account_id": options.AccountID()})
		}
	})
	ch.OnDisconnected(func() {
		c.mu.Lock()
		if c.channel == ch {
			c.healthy = false
		}
		c.mu.Unlock()
	})
	ch.OnReconnected(func() {
		c.mu.Lock()
		if c.channel == ch {
			c.healthy = true
		}
		c.mu.Unlock()
	})
	done := make(chan struct{})
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		cancel()
		return errors.New("feishu SDK client is already started")
	}
	c.channel = ch
	c.options = options
	c.started = true
	c.cancel = cancel
	c.done = done
	c.mu.Unlock()

	var terminalErr error
	var terminalMu sync.Mutex
	ready := make(chan error, 1)
	var readyOnce sync.Once
	ch.OnReady(func() {
		identityCtx, identityCancel := context.WithTimeout(runCtx, feishuBotIdentityTimeout)
		identity := ch.GetBotIdentity(identityCtx)
		identityCancel()
		if identity == nil || strings.TrimSpace(identity.OpenID) == "" {
			c.mu.Lock()
			if c.channel == ch {
				c.healthy = false
			}
			c.mu.Unlock()
			readyOnce.Do(func() { ready <- errors.New("feishu bot identity validation failed") })
			return
		}
		c.mu.Lock()
		current := c.channel == ch && c.started
		if current {
			c.healthy = true
		}
		c.mu.Unlock()
		if !current {
			return
		}
		readyOnce.Do(func() { ready <- nil })
	})
	go func() {
		defer cancel()
		err := ch.Start(runCtx)
		terminalMu.Lock()
		terminalErr = err
		terminalMu.Unlock()
		c.clearChannel(ch)
		close(done)
	}()
	startupTimer := time.NewTimer(feishuStartupTimeout)
	defer startupTimer.Stop()
	select {
	case err := <-ready:
		if err == nil {
			started = true
			return nil
		}
		cancel()
		_ = ch.Stop(context.Background())
		<-done
		return err
	case <-done:
		terminalMu.Lock()
		err := terminalErr
		terminalMu.Unlock()
		if err == nil {
			return errors.New("feishu channel stopped before becoming ready")
		}
		return err
	case <-ctx.Done():
		cancel()
		_ = ch.Stop(context.Background())
		<-done
		return ctx.Err()
	case <-startupTimer.C:
		cancel()
		_ = ch.Stop(context.Background())
		<-done
		return errors.New("feishu channel did not become ready before the startup timeout")
	}
}

func feishuEndpointValidator(domainName string) func(string) error {
	allowedSuffix := ".feishu.cn"
	if strings.EqualFold(strings.TrimSpace(domainName), "lark") || strings.EqualFold(strings.TrimSpace(domainName), "larksuite") {
		allowedSuffix = ".larksuite.com"
	}
	return func(rawURL string) error {
		endpoint, err := url.Parse(rawURL)
		if err != nil {
			return fmt.Errorf("parse endpoint: %w", err)
		}
		if !strings.EqualFold(endpoint.Scheme, "wss") {
			return errors.New("endpoint must use wss")
		}
		if endpoint.User != nil {
			return errors.New("endpoint must not contain user information")
		}
		if endpoint.Fragment != "" {
			return errors.New("endpoint must not contain a fragment")
		}
		hostname := strings.ToLower(endpoint.Hostname())
		if hostname == "" || strings.HasSuffix(hostname, ".") || !strings.HasSuffix(hostname, allowedSuffix) {
			return fmt.Errorf("endpoint host is outside the official %s domain", strings.TrimPrefix(allowedSuffix, "."))
		}
		if port := endpoint.Port(); port != "" && port != "443" {
			return errors.New("endpoint must use port 443")
		}
		return nil
	}
}

type feishuHTTPClient struct {
	client *http.Client
}

func newFeishuHTTPClient() *feishuHTTPClient {
	return &feishuHTTPClient{client: &http.Client{
		Timeout: feishuAPIRequestTimeout,
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if len(via) == 0 {
				return errors.New("feishu redirect has no original request")
			}
			origin := via[0].URL
			if next.URL.Scheme != origin.Scheme || !strings.EqualFold(next.URL.Host, origin.Host) {
				return errors.New("feishu cross-origin redirect is not allowed")
			}
			if len(via) >= 5 {
				return errors.New("too many feishu HTTP redirects")
			}
			return nil
		},
	}}
}

func (c *feishuHTTPClient) Do(req *http.Request) (*http.Response, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("feishu HTTP client is not configured")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.Body == nil {
		return resp, nil
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, feishuMaxAPIResponseBytes+1))
	closeErr := resp.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(raw) > feishuMaxAPIResponseBytes {
		return nil, errors.New("feishu API response exceeds 1 MiB")
	}
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	resp.ContentLength = int64(len(raw))
	return resp, nil
}

func (c *SDKClient) SendReply(ctx context.Context, reply Reply) error {
	ch, err := c.activeChannel()
	if err != nil {
		return err
	}
	input := &larktypes.SendInput{
		ChatID: strings.TrimSpace(reply.ChatID), ReplyMessageID: strings.TrimSpace(reply.ReplyMessageID),
		IdempotencyKey: strings.TrimSpace(reply.IdempotencyKey), Markdown: reply.Text,
	}
	result, err := ch.Send(ctx, input)
	if err != nil {
		return err
	}
	if result == nil || (strings.TrimSpace(result.MessageID) == "" && len(result.ChunkIDs) == 0) {
		return errors.New("feishu reply returned no receipt")
	}
	return nil
}

func (c *SDKClient) SendDirect(ctx context.Context, recipientID string, title string, text string, format string, idempotencyKey string) (string, error) {
	if strings.TrimSpace(recipientID) == "" {
		return "", channel.PermanentDeliveryError(errors.New("feishu recipient is required"))
	}
	ch, err := c.activeChannel()
	if err != nil {
		return "", err
	}
	input := &larktypes.SendInput{
		ReceiveID: strings.TrimSpace(recipientID), Title: strings.TrimSpace(title),
		IdempotencyKey: strings.TrimSpace(idempotencyKey),
	}
	if strings.EqualFold(strings.TrimSpace(format), "text") {
		input.Text = text
	} else {
		input.MsgType = "interactive"
		input.Markdown = text
	}
	result, err := ch.Send(ctx, input)
	if err != nil {
		classified := larktypes.ClassifyError(err)
		if !larktypes.IsRetryable(classified) {
			return "", channel.PermanentDeliveryError(classified)
		}
		return "", classified
	}
	if result == nil {
		return "", errors.New("feishu send returned no receipt")
	}
	receipts := append([]string(nil), result.ChunkIDs...)
	if len(receipts) == 0 && strings.TrimSpace(result.MessageID) != "" {
		receipts = append(receipts, result.MessageID)
	}
	if len(receipts) == 0 {
		return "", errors.New("feishu send returned an empty receipt")
	}
	return strings.Join(receipts, ","), nil
}

func (c *SDKClient) Stop(ctx context.Context) error {
	c.mu.Lock()
	ch := c.channel
	cancel := c.cancel
	done := c.done
	outbound := c.outbound
	c.healthy = false
	if outbound {
		c.channel = nil
		c.options = Options{}
		c.started = false
		c.outbound = false
	}
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if ch == nil || outbound {
		return nil
	}
	stopErr := ch.Stop(ctx)
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return errors.Join(stopErr, ctx.Err())
		}
	}
	return stopErr
}

func (c *SDKClient) GatewayConnectionHealthy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.started && c.healthy && c.channel != nil
}

func (c *SDKClient) activeChannel() (larktypes.Channel, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.channel == nil || !c.started {
		return nil, errors.New("feishu channel is not connected")
	}
	return c.channel, nil
}

func (c *SDKClient) clearChannel(ch larktypes.Channel) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.channel == ch {
		c.channel = nil
		c.options = Options{}
		c.started = false
		c.healthy = false
		c.outbound = false
		c.cancel = nil
		c.done = nil
	}
}

func normalizeFeishuChatType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "group":
		return "group"
	case "p2p", "direct", "private":
		return "direct"
	default:
		return "unknown"
	}
}

type feishuSDKLogger struct {
	logger    channel.Logger
	lifecycle context.Context
}

func (l feishuSDKLogger) Debug(context.Context, ...interface{}) {}

func (l feishuSDKLogger) Info(_ context.Context, args ...interface{}) {
	if l.logger != nil {
		l.logger.Info("feishu SDK event", map[string]any{"event_code": "sdk_info"})
	}
}

func (l feishuSDKLogger) Warn(_ context.Context, args ...interface{}) {
	if l.logger != nil {
		l.logger.Warn("feishu SDK warning", feishuSDKDiagnostic("sdk_warning", args))
	}
}

func (l feishuSDKLogger) Error(_ context.Context, args ...interface{}) {
	if l.logger != nil {
		fields := feishuSDKDiagnostic("sdk_error", args)
		if fields["error_kind"] == "connection_closed" && l.lifecycle != nil && errors.Is(context.Cause(l.lifecycle), context.Canceled) && !strings.Contains(strings.ToLower(fmt.Sprint(args...)), "panic") {
			l.logger.Info("feishu SDK connection closed during shutdown", map[string]any{"event_code": "sdk_shutdown", "connection_state": "closed"})
			return
		}
		l.logger.Error("feishu SDK error", fields)
	}
}

var _ larkcore.Logger = feishuSDKLogger{}

var feishuSDKCode = regexp.MustCompile(`(?i)(?:error_code|status_code|code|status)["' ]*[:=]["' ]*([0-9]{3,9})`)
var feishuSDKURL = regexp.MustCompile(`(?i)\b(?:https?|wss?)://[^\s]+`)
var feishuSDKPayload = regexp.MustCompile(`(?i)\b(?:body|payload|headers?|request_body|response_body)\s*[:=]`)

func feishuSDKDiagnostic(code string, args []interface{}) map[string]any {
	message := strings.ToLower(fmt.Sprint(args...))
	kind := "sdk"
	switch {
	case strings.Contains(message, "timeout"), strings.Contains(message, "deadline exceeded"):
		kind = "timeout"
	case strings.Contains(message, "no route"), strings.Contains(message, "dial tcp"), strings.Contains(message, "connection refused"):
		kind = "network"
	case strings.Contains(message, "unauthorized"), strings.Contains(message, "authentication"), strings.Contains(message, "invalid credential"):
		kind = "authentication"
	case strings.Contains(message, "rate limit"), strings.Contains(message, "too many requests"):
		kind = "rate_limit"
	case strings.Contains(message, "websocket"), strings.Contains(message, "handshake"):
		kind = "websocket"
	case strings.Contains(message, "closed"), strings.Contains(message, "eof"):
		kind = "connection_closed"
	}
	fields := map[string]any{"error_code": code + "_" + kind, "error_kind": kind}
	// Only transport diagnoses enter the diagnostic store. Never persist SDK
	// request/response bodies or event payloads, including unclassified errors.
	if kind != "sdk" {
		cause := fmt.Sprint(args...)
		if location := feishuSDKPayload.FindStringIndex(cause); location != nil {
			cause = cause[:location[0]]
		}
		cause, _, _ = strings.Cut(cause, "\n")
		cause = feishuSDKURL.ReplaceAllStringFunc(cause, func(raw string) string {
			u, err := url.Parse(raw)
			if err != nil {
				return "[redacted endpoint]"
			}
			return u.Scheme + "://" + u.Hostname()
		})
		fields["error_cause"] = logging.RedactDiagnostic(cause, 768)
	}
	if match := feishuSDKCode.FindStringSubmatch(message); len(match) == 2 {
		fields["sdk_status_code"] = match[1]
	}
	return fields
}
