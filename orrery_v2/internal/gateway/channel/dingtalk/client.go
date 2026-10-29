package dingtalk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/gateway/channel"

	dingchatbot "github.com/open-dingtalk/dingtalk-stream-sdk-go/chatbot"
	dingclient "github.com/open-dingtalk/dingtalk-stream-sdk-go/client"
	dinglogger "github.com/open-dingtalk/dingtalk-stream-sdk-go/logger"
	dingpayload "github.com/open-dingtalk/dingtalk-stream-sdk-go/payload"
	dingutils "github.com/open-dingtalk/dingtalk-stream-sdk-go/utils"
)

const (
	dingTalkTokenPath      = "/v1.0/oauth2/accessToken"
	dingTalkDirectSendPath = "/v1.0/robot/oToMessages/batchSend"
	dingTalkTextLimit      = 2048
	dingTalkMarkdownLimit  = 20000
	dingTalkStartupTimeout = 45 * time.Second
	dingTalkMaxTokenTTL    = 24 * time.Hour
	dingTalkMaxResponse    = 1 << 20
)

type SDKClient struct {
	logger channel.Logger
	http   *http.Client

	mu            sync.Mutex
	stream        *dingclient.StreamClient
	options       Options
	accessToken   string
	tokenExpires  time.Time
	refreshing    bool
	refreshWaiter chan struct{}
	allowLoopback bool
	healthy       bool
	generation    uint64
}

func NewSDKClient(logger channel.Logger) *SDKClient {
	return &SDKClient{logger: logger, http: &http.Client{Timeout: 15 * time.Second}}
}

func NewSDKClientWithHTTP(logger channel.Logger, client *http.Client, allowLoopback bool) *SDKClient {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &SDKClient{logger: logger, http: client, allowLoopback: allowLoopback}
}

func (c *SDKClient) SetLogger(logger channel.Logger) {
	c.mu.Lock()
	c.logger = logger
	c.mu.Unlock()
	if logger != nil {
		dinglogger.SetLogger(dingTalkSDKLogger{logger: logger})
	}
}

func (c *SDKClient) StartStream(ctx context.Context, opts Options, inbound func(context.Context, InboundMessage) error) error {
	if strings.TrimSpace(opts.ClientID) == "" || strings.TrimSpace(opts.ClientSecret) == "" {
		return errors.New("dingtalk client id and client secret are required")
	}
	if inbound == nil {
		return errors.New("dingtalk inbound handler is required")
	}
	streamOptions := []dingclient.ClientOption{
		dingclient.WithAppCredential(dingclient.NewAppCredentialConfig(opts.ClientID, opts.ClientSecret)),
		dingclient.WithEndpointValidator(func(endpoint string) error {
			return validateDingTalkStreamEndpoint(endpoint, c.allowLoopback)
		}),
	}
	if opts.APIBaseURL != "" && opts.APIBaseURL != "https://api.dingtalk.com" {
		if err := validateDingTalkBaseURL(opts.APIBaseURL, c.allowLoopback); err != nil {
			return err
		}
		streamOptions = append(streamOptions, dingclient.WithOpenApiHost(opts.APIBaseURL))
	}
	stream := dingclient.NewStreamClient(streamOptions...)
	stream.SetConnectionStateHandler(c.setHealthy)
	stream.RegisterRouter(dingutils.SubscriptionTypeKSystem, "disconnect", func(frameCtx context.Context, frame *dingpayload.DataFrame) (*dingpayload.DataFrameResponse, error) {
		c.setHealthy(false)
		return stream.OnDisconnect(frameCtx, frame)
	})
	stream.RegisterRouter(dingutils.SubscriptionTypeKSystem, "ping", func(frameCtx context.Context, frame *dingpayload.DataFrame) (*dingpayload.DataFrameResponse, error) {
		c.setHealthy(true)
		return stream.OnPing(frameCtx, frame)
	})
	stream.RegisterChatBotCallbackRouter(func(messageCtx context.Context, data *dingchatbot.BotCallbackDataModel) ([]byte, error) {
		if data == nil {
			return nil, errors.New("dingtalk callback payload is empty")
		}
		c.setHealthy(true)
		// oToMessages/batchSend accepts DingTalk staff IDs. The encrypted
		// senderId is not a valid proactive-delivery recipient.
		senderID := dingTalkProactiveRecipientID(data)
		message := InboundMessage{
			MessageID:        data.MsgId,
			AccountID:        opts.AccountID(),
			TenantID:         firstNonBlank(data.SenderCorpId, data.ChatbotCorpId),
			ConversationID:   data.ConversationId,
			ConversationType: data.ConversationType,
			SenderID:         senderID,
			SenderName:       data.SenderNick,
			SessionWebhook:   data.SessionWebhook,
			Text:             data.Text.Content,
			SenderVerified:   senderID != "",
		}
		if err := inbound(messageCtx, message); err != nil {
			return nil, err
		}
		return []byte(""), nil
	})
	c.mu.Lock()
	if c.stream != nil {
		c.mu.Unlock()
		return errors.New("dingtalk stream client is already started")
	}
	c.stream = stream
	c.options = opts
	c.healthy = true
	c.generation++
	c.mu.Unlock()
	startCtx, cancel := context.WithTimeout(ctx, dingTalkStartupTimeout)
	err := stream.Start(startCtx)
	cancel()
	if err != nil {
		c.mu.Lock()
		if c.stream == stream {
			c.stream = nil
			c.options = Options{}
			c.healthy = false
		}
		c.mu.Unlock()
		return err
	}
	return nil
}

func dingTalkProactiveRecipientID(data *dingchatbot.BotCallbackDataModel) string {
	if data == nil {
		return ""
	}
	return strings.TrimSpace(data.SenderStaffId)
}

func (c *SDKClient) SendReply(ctx context.Context, message OutboundMessage) error {
	if !c.GatewayConnectionHealthy() {
		return errors.New("dingtalk stream client is not connected")
	}
	webhook := strings.TrimSpace(message.SessionWebhook)
	if webhook == "" {
		return errors.New("dingtalk reply session webhook is required")
	}
	if err := validateDingTalkSessionWebhook(webhook, c.allowLoopback); err != nil {
		return err
	}
	chunks := splitDingTalkMessage(message.Text, dingTalkMarkdownLimit)
	if len(chunks) == 0 {
		return errors.New("dingtalk reply text is required")
	}
	for _, chunk := range chunks {
		body := map[string]any{
			"msgtype": "markdown",
			"markdown": map[string]string{
				"title": "Orrery",
				"text":  chunk,
			},
		}
		if err := c.postWebhook(ctx, webhook, body); err != nil {
			return err
		}
	}
	return nil
}

func (c *SDKClient) SendDirect(ctx context.Context, recipientID string, title string, text string, format string) (string, error) {
	if !c.GatewayConnectionHealthy() {
		return "", errors.New("dingtalk stream client is not connected")
	}
	recipientID = strings.TrimSpace(recipientID)
	if recipientID == "" {
		return "", channel.PermanentDeliveryError(errors.New("dingtalk recipient is required"))
	}
	opts := c.snapshotOptions()
	if err := validateDingTalkBaseURL(opts.APIBaseURL, c.allowLoopback); err != nil {
		return "", err
	}
	messageKey := "sampleMarkdown"
	limit := dingTalkMarkdownLimit
	if strings.EqualFold(strings.TrimSpace(format), "text") {
		messageKey = "sampleText"
		limit = dingTalkTextLimit
	}
	chunks := splitDingTalkMessage(text, limit)
	if len(chunks) == 0 {
		return "", channel.PermanentDeliveryError(errors.New("dingtalk message text is required"))
	}
	token, err := c.token(ctx, false)
	if err != nil {
		return "", err
	}
	receiptIDs := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		messageParam := map[string]any{"title": firstNonBlank(title, "Orrery"), "text": chunk}
		if messageKey == "sampleText" {
			messageParam = map[string]any{"content": chunk}
		}
		paramJSON, marshalErr := json.Marshal(messageParam)
		if marshalErr != nil {
			return "", marshalErr
		}
		body := map[string]any{
			"robotCode": opts.RobotCode,
			"userIds":   []string{recipientID},
			"msgKey":    messageKey,
			"msgParam":  string(paramJSON),
		}
		receipt, status, sendErr := c.postAPI(ctx, opts.APIBaseURL+dingTalkDirectSendPath, token, body)
		if sendErr != nil && isDingTalkAuthenticationError(status, sendErr) {
			var tokenErr error
			token, tokenErr = c.token(ctx, true)
			if tokenErr != nil {
				return "", tokenErr
			}
			receipt, _, sendErr = c.postAPI(ctx, opts.APIBaseURL+dingTalkDirectSendPath, token, body)
		}
		if sendErr != nil {
			return "", sendErr
		}
		receiptID := strings.TrimSpace(receipt.ProcessQueryKey)
		if receiptID == "" {
			return "", errors.New("dingtalk proactive send returned no process receipt")
		}
		receiptIDs = append(receiptIDs, receiptID)
	}
	return strings.Join(receiptIDs, ","), nil
}

func splitDingTalkMessage(text string, limit int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if limit <= 0 {
		return []string{text}
	}
	runes := []rune(text)
	chunks := make([]string, 0, (len(runes)+limit-1)/limit)
	for len(runes) > limit {
		chunks = append(chunks, string(runes[:limit]))
		runes = runes[limit:]
	}
	chunks = append(chunks, string(runes))
	return chunks
}

func isDingTalkAuthenticationError(status int, err error) bool {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return true
	}
	var apiErr *dingTalkAPIError
	if !errors.As(err, &apiErr) {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(apiErr.Code + " " + apiErr.Message))
	return strings.Contains(message, "invalidauthentication") || strings.Contains(message, "access token") ||
		strings.Contains(message, "accesstoken") || strings.Contains(message, "token expired") ||
		strings.Contains(message, "token.invalid")
}

func (c *SDKClient) Stop(ctx context.Context) error {
	c.mu.Lock()
	stream := c.stream
	c.stream = nil
	c.options = Options{}
	c.accessToken = ""
	c.tokenExpires = time.Time{}
	c.healthy = false
	c.generation++
	c.mu.Unlock()
	if stream != nil {
		return stream.Shutdown(ctx)
	}
	return nil
}

func (c *SDKClient) GatewayConnectionHealthy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stream != nil && c.healthy
}

func (c *SDKClient) setHealthy(healthy bool) {
	c.mu.Lock()
	c.healthy = healthy
	c.mu.Unlock()
}

func (c *SDKClient) snapshotOptions() Options {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.options
}

type dingTalkTokenResponse struct {
	AccessToken string `json:"accessToken"`
	ExpireIn    int64  `json:"expireIn"`
	Code        any    `json:"code"`
	Message     string `json:"message"`
	RequestID   string `json:"requestid"`
}

func (c *SDKClient) token(ctx context.Context, force bool) (string, error) {
	for {
		c.mu.Lock()
		if c.stream == nil || !c.healthy {
			c.mu.Unlock()
			return "", errors.New("dingtalk stream client is not connected")
		}
		if !force && c.accessToken != "" && time.Until(c.tokenExpires) > time.Minute {
			token := c.accessToken
			c.mu.Unlock()
			return token, nil
		}
		if c.refreshing {
			waiter := c.refreshWaiter
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-waiter:
			}
			force = false
			continue
		}
		c.refreshing = true
		c.refreshWaiter = make(chan struct{})
		waiter := c.refreshWaiter
		opts := c.options
		generation := c.generation
		c.mu.Unlock()

		token, expiry, err := c.fetchToken(ctx, opts)
		c.mu.Lock()
		if c.generation != generation || c.stream == nil || !c.healthy {
			token = ""
			expiry = time.Time{}
			err = errors.New("dingtalk stream client stopped while refreshing its access token")
		}
		if err == nil {
			c.accessToken = token
			c.tokenExpires = expiry
		} else if force {
			c.accessToken = ""
			c.tokenExpires = time.Time{}
		}
		c.refreshing = false
		close(waiter)
		c.mu.Unlock()
		return token, err
	}
}

func (c *SDKClient) fetchToken(ctx context.Context, opts Options) (string, time.Time, error) {
	if strings.TrimSpace(opts.ClientID) == "" || strings.TrimSpace(opts.ClientSecret) == "" {
		return "", time.Time{}, errors.New("dingtalk credentials are required")
	}
	if err := validateDingTalkBaseURL(opts.APIBaseURL, c.allowLoopback); err != nil {
		return "", time.Time{}, err
	}
	body, _ := json.Marshal(map[string]string{"appKey": opts.ClientID, "appSecret": opts.ClientSecret})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, opts.APIBaseURL+dingTalkTokenPath, bytes.NewReader(body))
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.do(req, func(target string) error { return validateDingTalkAPIEndpoint(target, c.allowLoopback) })
	if err != nil {
		return "", time.Time{}, fmt.Errorf("request dingtalk access token: %w", err)
	}
	defer resp.Body.Close()
	raw, err := readDingTalkResponse(resp.Body)
	if err != nil {
		return "", time.Time{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", time.Time{}, classifyDingTalkDeliveryError(parseDingTalkAPIError(resp.StatusCode, raw))
	}
	var result dingTalkTokenResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", time.Time{}, fmt.Errorf("decode dingtalk access token: %w", err)
	}
	if code := normalizeDingTalkCode(result.Code); code != "" && code != "0" {
		return "", time.Time{}, classifyDingTalkDeliveryError(&dingTalkAPIError{StatusCode: resp.StatusCode, Code: code, Message: result.Message, RequestID: result.RequestID})
	}
	if strings.TrimSpace(result.AccessToken) == "" {
		return "", time.Time{}, errors.New("dingtalk access token response is empty")
	}
	maxTTLSeconds := int64(dingTalkMaxTokenTTL / time.Second)
	if result.ExpireIn <= 0 || result.ExpireIn > maxTTLSeconds {
		return "", time.Time{}, errors.New("dingtalk access token response has an invalid expiry")
	}
	return result.AccessToken, time.Now().UTC().Add(time.Duration(result.ExpireIn) * time.Second), nil
}

type dingTalkSendReceipt struct {
	ProcessQueryKey string `json:"processQueryKey"`
	RequestID       string `json:"requestId"`
	Code            any    `json:"code"`
	ErrCode         any    `json:"errcode"`
	Message         string `json:"message"`
	ErrMsg          string `json:"errmsg"`
}

func (c *SDKClient) postAPI(ctx context.Context, endpoint string, token string, body any) (dingTalkSendReceipt, int, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return dingTalkSendReceipt{}, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return dingTalkSendReceipt{}, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-acs-dingtalk-access-token", token)
	resp, err := c.do(req, func(target string) error { return validateDingTalkAPIEndpoint(target, c.allowLoopback) })
	if err != nil {
		return dingTalkSendReceipt{}, 0, fmt.Errorf("send dingtalk message: %w", err)
	}
	defer resp.Body.Close()
	responseBody, readErr := readDingTalkResponse(resp.Body)
	if readErr != nil {
		return dingTalkSendReceipt{}, resp.StatusCode, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return dingTalkSendReceipt{}, resp.StatusCode, classifyDingTalkDeliveryError(parseDingTalkAPIError(resp.StatusCode, responseBody))
	}
	var receipt dingTalkSendReceipt
	if len(responseBody) > 0 {
		if err := json.Unmarshal(responseBody, &receipt); err != nil {
			return dingTalkSendReceipt{}, resp.StatusCode, fmt.Errorf("decode dingtalk message receipt: %w", err)
		}
	}
	code := normalizeDingTalkCode(receipt.Code)
	if code == "" {
		code = normalizeDingTalkCode(receipt.ErrCode)
	}
	if code != "" && code != "0" {
		return dingTalkSendReceipt{}, resp.StatusCode, classifyDingTalkDeliveryError(&dingTalkAPIError{
			StatusCode: resp.StatusCode, Code: code, Message: firstNonBlank(receipt.Message, receipt.ErrMsg), RequestID: receipt.RequestID,
		})
	}
	return receipt, resp.StatusCode, nil
}

func (c *SDKClient) postWebhook(ctx context.Context, endpoint string, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.do(req, func(target string) error { return validateDingTalkSessionWebhook(target, c.allowLoopback) })
	if err != nil {
		return fmt.Errorf("send dingtalk reply: %w", err)
	}
	defer resp.Body.Close()
	responseBody, readErr := readDingTalkResponse(resp.Body)
	if readErr != nil {
		return readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return parseDingTalkAPIError(resp.StatusCode, responseBody)
	}
	if apiErr := parseDingTalkSuccessError(resp.StatusCode, responseBody); apiErr != nil {
		return apiErr
	}
	return nil
}

func (c *SDKClient) do(req *http.Request, validateRedirect func(string) error) (*http.Response, error) {
	client := *c.http
	previous := client.CheckRedirect
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if validateRedirect != nil {
			if err := validateRedirect(next.URL.String()); err != nil {
				return err
			}
		}
		if previous != nil {
			return previous(next, via)
		}
		if len(via) >= 5 {
			return errors.New("too many dingtalk HTTP redirects")
		}
		return nil
	}
	return client.Do(req)
}

type dingTalkAPIError struct {
	StatusCode int
	Code       string
	Message    string
	RequestID  string
}

func (e *dingTalkAPIError) Error() string {
	parts := []string{fmt.Sprintf("dingtalk API HTTP %d", e.StatusCode)}
	if e.Code != "" {
		parts = append(parts, "code="+e.Code)
	}
	if message := strings.TrimSpace(e.Message); message != "" {
		if runes := []rune(message); len(runes) > 300 {
			message = string(runes[:300])
		}
		parts = append(parts, "message="+message)
	}
	if e.RequestID != "" {
		parts = append(parts, "request_id="+e.RequestID)
	}
	return strings.Join(parts, " ")
}

func parseDingTalkAPIError(status int, raw []byte) error {
	var envelope struct {
		Code       any    `json:"code"`
		ErrCode    any    `json:"errcode"`
		Message    string `json:"message"`
		Msg        string `json:"msg"`
		ErrMsg     string `json:"errmsg"`
		RequestID  string `json:"requestid"`
		RequestID2 string `json:"requestId"`
	}
	_ = json.Unmarshal(raw, &envelope)
	code := normalizeDingTalkCode(envelope.Code)
	if code == "" {
		code = normalizeDingTalkCode(envelope.ErrCode)
	}
	return &dingTalkAPIError{
		StatusCode: status,
		Code:       code,
		Message:    firstNonBlank(envelope.Message, envelope.Msg, envelope.ErrMsg, http.StatusText(status)),
		RequestID:  firstNonBlank(envelope.RequestID, envelope.RequestID2),
	}
}

func parseDingTalkSuccessError(status int, raw []byte) error {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return errors.New("dingtalk API response is empty")
	}
	var envelope struct {
		Code      any    `json:"code"`
		ErrCode   any    `json:"errcode"`
		Message   string `json:"message"`
		Msg       string `json:"msg"`
		ErrMsg    string `json:"errmsg"`
		RequestID string `json:"requestid"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("decode dingtalk API response: %w", err)
	}
	code := normalizeDingTalkCode(envelope.Code)
	if code == "" {
		code = normalizeDingTalkCode(envelope.ErrCode)
	}
	if code == "" {
		return errors.New("dingtalk API response is missing code")
	}
	if code == "0" {
		return nil
	}
	return &dingTalkAPIError{StatusCode: status, Code: code, Message: firstNonBlank(envelope.Message, envelope.Msg, envelope.ErrMsg), RequestID: envelope.RequestID}
}

func readDingTalkResponse(body io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(body, dingTalkMaxResponse+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > dingTalkMaxResponse {
		return nil, errors.New("dingtalk API response exceeds 1 MiB")
	}
	return raw, nil
}

func normalizeDingTalkCode(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return fmt.Sprintf("%.0f", typed)
	case json.Number:
		return typed.String()
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func classifyDingTalkDeliveryError(err error) error {
	var apiErr *dingTalkAPIError
	if !errors.As(err, &apiErr) {
		return err
	}
	message := strings.ToLower(apiErr.Code + " " + apiErr.Message)
	retryable := apiErr.StatusCode == http.StatusRequestTimeout || apiErr.StatusCode == http.StatusTooEarly ||
		apiErr.StatusCode == http.StatusTooManyRequests || apiErr.StatusCode >= http.StatusInternalServerError ||
		strings.Contains(message, "rate") || strings.Contains(message, "limit") || strings.Contains(message, "busy") ||
		strings.Contains(message, "timeout") || strings.Contains(message, "system.error")
	if retryable {
		return err
	}
	return channel.PermanentDeliveryError(err)
}

func validateDingTalkBaseURL(raw string, allowLoopback bool) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.EscapedPath() != "" && parsed.EscapedPath() != "/") {
		return errors.New("invalid dingtalk API base URL")
	}
	host := strings.ToLower(parsed.Hostname())
	if parsed.Scheme == "https" && host == "api.dingtalk.com" && (parsed.Port() == "" || parsed.Port() == "443") {
		return nil
	}
	if allowLoopback && (parsed.Scheme == "http" || parsed.Scheme == "https") {
		ip := net.ParseIP(host)
		if host == "localhost" || (ip != nil && ip.IsLoopback()) {
			return nil
		}
	}
	return errors.New("dingtalk API base URL must use official HTTPS origin")
}

func validateDingTalkAPIEndpoint(raw string, allowLoopback bool) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("invalid dingtalk API endpoint")
	}
	host := strings.ToLower(parsed.Hostname())
	if parsed.Scheme == "https" && host == "api.dingtalk.com" && (parsed.Port() == "" || parsed.Port() == "443") {
		return nil
	}
	if allowLoopback && (parsed.Scheme == "http" || parsed.Scheme == "https") {
		ip := net.ParseIP(host)
		if host == "localhost" || (ip != nil && ip.IsLoopback()) {
			return nil
		}
	}
	return errors.New("dingtalk API endpoint must use official HTTPS origin")
}

func validateDingTalkSessionWebhook(raw string, allowLoopback bool) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("invalid dingtalk session webhook URL")
	}
	host := strings.ToLower(parsed.Hostname())
	if parsed.Scheme == "https" && (parsed.Port() == "" || parsed.Port() == "443") {
		if host == "api.dingtalk.com" && strings.HasPrefix(parsed.EscapedPath(), "/v1.0/robot/") {
			return nil
		}
		if host == "oapi.dingtalk.com" && strings.HasPrefix(parsed.EscapedPath(), "/robot/") {
			return nil
		}
	}
	if allowLoopback && (parsed.Scheme == "http" || parsed.Scheme == "https") {
		ip := net.ParseIP(host)
		if host == "localhost" || (ip != nil && ip.IsLoopback()) {
			return nil
		}
	}
	return errors.New("dingtalk session webhook must use an official HTTPS endpoint")
}

func validateDingTalkStreamEndpoint(raw string, allowLoopback bool) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("invalid dingtalk stream endpoint")
	}
	host := strings.ToLower(parsed.Hostname())
	if parsed.Scheme == "wss" && strings.HasSuffix(host, ".dingtalk.com") && !strings.HasSuffix(host, ".") &&
		(parsed.Port() == "" || parsed.Port() == "443") {
		return nil
	}
	if allowLoopback && (parsed.Scheme == "ws" || parsed.Scheme == "wss") {
		ip := net.ParseIP(host)
		if host == "localhost" || (ip != nil && ip.IsLoopback()) {
			return nil
		}
	}
	return errors.New("dingtalk stream endpoint must use an official secure origin")
}

type dingTalkSDKLogger struct {
	logger channel.Logger
}

func (l dingTalkSDKLogger) Debugf(string, ...interface{}) {}
func (l dingTalkSDKLogger) Infof(format string, args ...interface{}) {
	if l.logger != nil {
		l.logger.Info("dingtalk SDK event", map[string]any{"event_code": "sdk_info"})
	}
}
func (l dingTalkSDKLogger) Warningf(format string, args ...interface{}) {
	if l.logger != nil {
		l.logger.Warn("dingtalk SDK warning", map[string]any{"error_code": "sdk_warning"})
	}
}
func (l dingTalkSDKLogger) Errorf(format string, args ...interface{}) {
	if l.logger != nil {
		l.logger.Error("dingtalk SDK error", map[string]any{"error_code": "sdk_error"})
	}
}
func (l dingTalkSDKLogger) Fatalf(format string, args ...interface{}) {
	l.Errorf(format, args...)
}

var _ dinglogger.ILogger = dingTalkSDKLogger{}
