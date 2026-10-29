package weixin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/gateway/channel"
	gwconfig "orrery_v2/internal/gateway/config"
	gwcore "orrery_v2/internal/gateway/core"
	gwinbound "orrery_v2/internal/gateway/inbound"
	"orrery_v2/internal/gateway/protocol"
	platformdb "orrery_v2/internal/platform/db"
)

type fakeRunner struct {
	inputs []protocol.SubmitTurnInput
}

type blockingApprovalRunner struct {
	release chan struct{}
}

func (r *blockingApprovalRunner) RunTurn(context.Context, protocol.SubmitTurnInput) (<-chan protocol.Event, error) {
	events := make(chan protocol.Event, 3)
	go func() {
		defer close(events)
		events <- protocol.Event{Type: protocol.EventPermissionRequest, RunID: "run-live", RequestID: "approval-live", ToolName: "bash"}
		<-r.release
		events <- protocol.Event{Type: protocol.EventAssistantTextDelta, Text: "finished"}
		events <- protocol.Event{Type: protocol.EventTurnCompleted, FinishReason: "completed"}
	}()
	return events, nil
}

func writeIlinkJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (f *fakeRunner) RunTurn(ctx context.Context, input protocol.SubmitTurnInput) (<-chan protocol.Event, error) {
	f.inputs = append(f.inputs, input)
	ch := make(chan protocol.Event, 3)
	go func() {
		defer close(ch)
		ch <- protocol.Event{Type: protocol.EventAssistantTextDelta, Text: "hello "}
		ch <- protocol.Event{Type: protocol.EventAssistantTextDelta, Text: "world"}
		ch <- protocol.Event{Type: protocol.EventTurnCompleted, FinishReason: "completed"}
	}()
	return ch, nil
}

type fakeClient struct {
	started bool
	replies []OutboundMessage
	sent    chan OutboundMessage
}

type fakeReceiptClient struct {
	fakeClient
	receipt string
}

func (f *fakeReceiptClient) SendWithReceipt(_ context.Context, message OutboundMessage) (string, error) {
	f.replies = append(f.replies, message)
	return f.receipt, nil
}

type capturedLogEntry struct {
	message  string
	metadata map[string]any
}

type captureLogger struct {
	warnings []capturedLogEntry
}

func (*captureLogger) Info(string, map[string]any)  {}
func (*captureLogger) Error(string, map[string]any) {}

func (l *captureLogger) Warn(message string, metadata map[string]any) {
	l.warnings = append(l.warnings, capturedLogEntry{message: message, metadata: metadata})
}

func (f *fakeClient) Start(ctx context.Context, opts Options, logger Logger, inbound InboundProcessor) error {
	f.started = true
	return nil
}

func (f *fakeClient) SendReply(ctx context.Context, message OutboundMessage) error {
	f.replies = append(f.replies, message)
	if f.sent != nil {
		f.sent <- message
	}
	return nil
}

func (f *fakeClient) Stop(ctx context.Context) error {
	return nil
}

func TestOptionsFromConfigIgnoresObsoleteGlobalWeixinExtras(t *testing.T) {
	opts := OptionsFromConfig(gwconfig.WeixinAdapterConfig{
		Extra: map[string]any{
			"mode":                "openclaw_bridge",
			"bridgeToken":         "legacy-bridge-token",
			"openclawBridgeToken": "legacy-openclaw-token",
		},
	})

	if opts.BotToken != "" || len(opts.Accounts) != 0 {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestOptionsFromConfigMapsIlinkBotSettings(t *testing.T) {
	opts := OptionsFromConfig(gwconfig.WeixinAdapterConfig{
		Extra: map[string]any{
			"ilinkBaseUrl":          "https://example.test/ilink/bot",
			"getUpdatesPath":        "updates",
			"sendMessagePath":       "send",
			"loginBaseUrl":          "https://login.example.test",
			"botType":               "4",
			"xWechatUin":            12345,
			"skRouteTag":            "route-a",
			"channelVersion":        "2.0.0",
			"ilinkAppId":            "bot-custom",
			"ilinkAppClientVersion": 70000,
			"getUpdatesBuf":         "cursor-1",
			"pollIntervalSeconds":   3,
			"requestTimeoutSeconds": 20,
			"inboundMaxAttempts":    4,
		},
	})

	if opts.BotToken != "" {
		t.Fatalf("opts = %+v", opts)
	}
	if opts.IlinkBaseURL != "https://example.test/ilink/bot" || opts.GetUpdatesPath != "updates" || opts.SendMessagePath != "send" {
		t.Fatalf("opts = %+v", opts)
	}
	if opts.IlinkLoginBaseURL != "https://login.example.test" || opts.IlinkBotType != "4" {
		t.Fatalf("opts = %+v", opts)
	}
	if opts.XWechatUIN != "12345" || opts.SKRouteTag != "route-a" || opts.IlinkChannelVersion != "2.0.0" || opts.IlinkAppID != "bot-custom" || opts.IlinkAppClientVersion != "70000" || opts.GetUpdatesBuf != "cursor-1" {
		t.Fatalf("opts = %+v", opts)
	}
	if opts.PollIntervalSeconds != 3 || opts.RequestTimeoutSeconds != 20 || opts.InboundMaxAttempts != 4 {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestTenantScopedAccountsUseDistinctStateFiles(t *testing.T) {
	tenantAKey := gwconfig.WeixinBindingKey("tenant-a", "shared-user")
	tenantBKey := gwconfig.WeixinBindingKey("tenant-b", "shared-user")
	opts := OptionsFromConfig(gwconfig.WeixinAdapterConfig{Extra: map[string]any{
		"weixinAccounts": map[string]any{
			tenantAKey: map[string]any{"userId": "shared-user", "botId": "bot-a", "token": "token-a"},
			tenantBKey: map[string]any{"userId": "shared-user", "botId": "bot-b", "token": "token-b"},
		},
	}})
	if len(opts.Accounts) != 2 {
		t.Fatalf("accounts = %+v", opts.Accounts)
	}
	byTenant := map[string]AccountOptions{}
	for _, account := range opts.Accounts {
		byTenant[account.TenantID] = account
	}
	if byTenant["tenant-a"].UserID != "shared-user" || byTenant["tenant-b"].UserID != "shared-user" {
		t.Fatalf("tenant account parsing = %+v", opts.Accounts)
	}
	base := filepath.Join(t.TempDir(), "weixin-state.json")
	pathA := accountStatePath(base, "tenant-a", "shared-user", "bot-a")
	pathB := accountStatePath(base, "tenant-b", "shared-user", "bot-b")
	if pathA == pathB || pathA == base || pathB == base {
		t.Fatalf("tenant state paths collide: a=%q b=%q", pathA, pathB)
	}
	oldBotPath := accountStatePath(base, "system", "shared-user", "bot-old")
	newBotPath := accountStatePath(base, "system", "shared-user", "bot-new")
	if oldBotPath == newBotPath {
		t.Fatalf("rotated bot state paths collide: old=%q new=%q", oldBotPath, newBotPath)
	}
}

func TestIlinkHTTPErrorDoesNotExposeProviderResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`upstream echoed Authorization: Bearer secret-token`))
	}))
	defer server.Close()
	client := &IlinkBotClient{allowInsecureLoopback: true}
	opts := Options{BotToken: "secret-token", IlinkBaseURL: server.URL, SendMessagePath: "sendmessage"}.WithDefaults()
	err := client.postJSON(context.Background(), server.Client(), opts, "uin", opts.SendMessagePath, map[string]string{"text": "hello"}, &ilinkSendMessageResponse{})
	if err == nil || strings.Contains(err.Error(), "secret-token") || strings.Contains(err.Error(), "Authorization") {
		t.Fatalf("postJSON error = %v", err)
	}
}

func TestNewClientDefaultsToIlinkBotAPI(t *testing.T) {
	client := NewClient()
	if _, ok := client.(*IlinkBotMultiClient); !ok {
		t.Fatalf("client = %T, want *IlinkBotMultiClient", client)
	}
}

func TestValidateConfiguredAccountsRejectsMalformedProvisioningData(t *testing.T) {
	_, err := ValidateConfiguredAccounts(gwconfig.WeixinAdapterConfig{Extra: map[string]any{
		"weixinAccounts": map[string]any{
			"user-a": map[string]any{"botId": "bot-a"},
		},
	}})
	if err == nil || !strings.Contains(err.Error(), "userId, botId, and token") {
		t.Fatalf("ValidateConfiguredAccounts error = %v", err)
	}
}

func TestValidateConfiguredAccountsRejectsUnboundedRuntimeFanout(t *testing.T) {
	accounts := make(map[string]any, maxConfiguredAccounts+1)
	for index := 0; index <= maxConfiguredAccounts; index++ {
		key := fmt.Sprintf("tenant-a:user-%d", index)
		accounts[key] = map[string]any{"botId": fmt.Sprintf("bot-%d", index), "token": "token"}
	}
	_, err := ValidateConfiguredAccounts(gwconfig.WeixinAdapterConfig{Extra: map[string]any{"weixinAccounts": accounts}})
	if err == nil || !strings.Contains(err.Error(), "production limit") {
		t.Fatalf("ValidateConfiguredAccounts error = %v", err)
	}
}

func TestIlinkBotClientValidatesConnectionBeforeReportingStarted(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests++
		if req.URL.Path != "/getupdates" {
			http.NotFound(w, req)
			return
		}
		if req.Header.Get("Authorization") != "Bearer token-a" || req.Header.Get("X-WECHAT-UIN") == "" {
			t.Errorf("headers = %+v", req.Header)
		}
		writeIlinkJSON(w, http.StatusOK, map[string]any{"ret": 0, "msgs": []any{}, "get_updates_buf": "cursor-a"})
	}))
	defer server.Close()
	client := NewIlinkBotClient()
	client.allowInsecureLoopback = true
	err := client.Start(context.Background(), Options{
		BotToken: "token-a", AccountID: "bot-a", IlinkBaseURL: server.URL, GetUpdatesPath: "getupdates", SendMessagePath: "sendmessage",
		StatePath: filepath.Join(t.TempDir(), "state.json"), PollIntervalSeconds: 3600, RequestTimeoutSeconds: 2,
		InboundEvents: newWeixinInboundStore(t),
	}, nil, func(context.Context, InboundMessage) ([]OutboundMessage, error) { return nil, nil })
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = client.Stop(context.Background()) }()
	if requests != 1 {
		t.Fatalf("startup validation requests = %d, want 1", requests)
	}
}

func TestIlinkBotClientQueuesValidatedMessagesAndAdvancesCursorBeforeRuntimeProcessing(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/getupdates" {
			http.NotFound(w, req)
			return
		}
		requests++
		if requests > 1 {
			writeIlinkJSON(w, http.StatusOK, map[string]any{
				"ret":             0,
				"get_updates_buf": "cursor-next",
				"msgs": []map[string]any{{
					"message_id":   "next-message",
					"from_user_id": "user-1@im.wechat",
					"to_user_id":   "bot-a",
					"item_list": []map[string]any{{
						"type":      1,
						"text_item": map[string]any{"text": "next"},
					}},
				}},
			})
			return
		}
		writeIlinkJSON(w, http.StatusOK, map[string]any{
			"ret":             0,
			"get_updates_buf": "cursor-after",
			"msgs": []map[string]any{{
				"message_id":   "pending-message",
				"from_user_id": "user-1@im.wechat",
				"to_user_id":   "bot-a",
				"item_list": []map[string]any{{
					"type":      1,
					"text_item": map[string]any{"text": "pending"},
				}},
			}},
		})
	}))
	defer server.Close()

	inboundAttempt := make(chan struct{}, 1)
	nextProcessed := make(chan struct{}, 1)
	statePath := filepath.Join(t.TempDir(), "state.json")
	client := NewIlinkBotClient()
	client.allowInsecureLoopback = true
	err := client.Start(context.Background(), Options{
		BotToken: "token-a", AccountID: "bot-a", IlinkBaseURL: server.URL, GetUpdatesPath: "getupdates", SendMessagePath: "sendmessage",
		GetUpdatesBuf: "cursor-before", StatePath: statePath, PollIntervalSeconds: 3600, RequestTimeoutSeconds: 2,
		InboundEvents: newWeixinInboundStore(t),
	}, nil, func(_ context.Context, message InboundMessage) ([]OutboundMessage, error) {
		if message.Text == "next" {
			select {
			case nextProcessed <- struct{}{}:
			default:
			}
			return nil, nil
		}
		select {
		case inboundAttempt <- struct{}{}:
		default:
		}
		return nil, fmt.Errorf("runtime temporarily unavailable")
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = client.Stop(context.Background()) }()
	select {
	case <-inboundAttempt:
		t.Fatal("startup validation processed an inbound message")
	default:
	}
	if got := client.currentGetUpdatesBuf(); got != "cursor-before" {
		t.Fatalf("startup validation advanced cursor to %q", got)
	}
	if err := client.pollOnce(context.Background()); err != nil {
		t.Fatalf("queue pending update: %v", err)
	}
	if requests != 1 {
		t.Fatalf("queued pending update made %d requests, want 1", requests)
	}
	if got := client.currentGetUpdatesBuf(); got != "cursor-after" {
		t.Fatalf("queued pending update cursor = %q", got)
	}
	state, err := newFileIlinkBotStateStore(statePath).Load(context.Background())
	if err != nil {
		t.Fatalf("load persisted state: %v", err)
	}
	if state.GetUpdatesBuf != "cursor-after" {
		t.Fatalf("persisted pending update cursor = %q", state.GetUpdatesBuf)
	}
	select {
	case <-inboundAttempt:
	case <-time.After(time.Second):
		t.Fatal("durable worker did not process queued message")
	}

	if err := client.pollOnce(context.Background()); err != nil {
		t.Fatalf("poll after failed queued runtime: %v", err)
	}
	if requests != 2 {
		t.Fatalf("poll after pending update made %d requests, want 2", requests)
	}
	if got := client.currentGetUpdatesBuf(); got != "cursor-next" {
		t.Fatalf("next network update cursor = %q", got)
	}
	select {
	case <-nextProcessed:
	case <-time.After(time.Second):
		t.Fatal("later Weixin message was blocked by the failed queued message")
	}
}

func TestIlinkPollFailureStageDoesNotExposeMessageDetails(t *testing.T) {
	tests := []struct {
		err   error
		stage string
	}{
		{err: errors.New("weixin getupdates failed: session timeout"), stage: "get_updates"},
		{err: errors.New("weixin ilink inbound failed: gateway_route_unavailable"), stage: "gateway_route"},
		{err: errors.New("weixin ilink inbound failed: gateway_runtime_unavailable"), stage: "gateway_runtime"},
		{err: errors.New("Weixin inbound message has no verified sender"), stage: "sender_validation"},
		{err: errors.New("weixin ilink inbound failed for private-chat: runtime temporarily unavailable"), stage: "inbound_runtime"},
		{err: errors.New("persist weixin ilink cursor: disk full"), stage: "state_persistence"},
		{err: errors.New("unexpected private detail"), stage: "unknown"},
	}
	for _, tt := range tests {
		if got := ilinkPollFailureStage(tt.err); got != tt.stage {
			t.Errorf("ilinkPollFailureStage(%q) = %q, want %q", tt.err, got, tt.stage)
		}
	}
}

func TestIlinkBotClientRejectsInvalidCredentialsDuringStart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeIlinkJSON(w, http.StatusUnauthorized, map[string]any{"ret": -1, "errmsg": "invalid token"})
	}))
	defer server.Close()
	client := NewIlinkBotClient()
	client.allowInsecureLoopback = true
	err := client.Start(context.Background(), Options{
		BotToken: "invalid", AccountID: "bot-a", IlinkBaseURL: server.URL, GetUpdatesPath: "getupdates", SendMessagePath: "sendmessage",
		RequestTimeoutSeconds: 2, StatePath: filepath.Join(t.TempDir(), "state.json"),
		InboundEvents: newWeixinInboundStore(t),
	}, nil, func(context.Context, InboundMessage) ([]OutboundMessage, error) { return nil, nil })
	if err == nil || !channel.IsPermanentDeliveryError(err) {
		t.Fatalf("Start error = %v, want permanent credential failure", err)
	}
	if _, sendErr := client.SendWithReceipt(context.Background(), OutboundMessage{ChatID: "user-a", Text: "hello"}); sendErr == nil {
		t.Fatal("failed client accepted an outbound send")
	}
}

func TestIlinkBotClientLogsSafeProviderSendCodes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeIlinkJSON(w, http.StatusOK, map[string]any{
			"ret": -8, "errcode": 40003, "errmsg": "private recipient detail",
		})
	}))
	defer server.Close()
	logger := &captureLogger{}
	client := &IlinkBotClient{
		httpClient:            server.Client(),
		allowInsecureLoopback: true,
		options: Options{
			BotToken: "private-token", AccountID: "bot-a", IlinkBaseURL: server.URL, SendMessagePath: "sendmessage",
		}.WithDefaults(),
		xWechatUIN: "private-uin",
		logger:     logger,
	}
	_, err := client.SendWithReceipt(context.Background(), OutboundMessage{
		ChatID: "private-recipient", Text: "private body", ContextToken: "private-context",
	})
	if err == nil {
		t.Fatal("SendWithReceipt accepted a rejected provider response")
	}
	if len(logger.warnings) != 1 {
		t.Fatalf("warnings = %#v", logger.warnings)
	}
	entry := logger.warnings[0]
	if entry.message != "weixin ilink outbound send failed" || entry.metadata["error_code"] != "send_response_rejected" ||
		entry.metadata["failure_stage"] != "provider_response" || entry.metadata["return_code"] != -8 || entry.metadata["provider_error_code"] != 40003 {
		t.Fatalf("warning = %#v", entry)
	}
	raw, marshalErr := json.Marshal(entry.metadata)
	if marshalErr != nil {
		t.Fatalf("marshal warning: %v", marshalErr)
	}
	for _, privateValue := range []string{"private recipient detail", "private-token", "private-recipient", "private body", "private-context", "private-uin"} {
		if strings.Contains(string(raw), privateValue) {
			t.Fatalf("warning exposed private value %q: %s", privateValue, raw)
		}
	}
}

func TestRandomWechatUINMatchesIlinkHeaderFormat(t *testing.T) {
	uin, err := newRandomWechatUIN()
	if err != nil {
		t.Fatalf("newRandomWechatUIN: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(uin)
	if err != nil {
		t.Fatalf("decode X-WECHAT-UIN: %v", err)
	}
	if _, err := strconv.ParseUint(string(raw), 10, 32); err != nil {
		t.Fatalf("X-WECHAT-UIN should be base64 decimal uint32, got %q: %v", string(raw), err)
	}
}

func TestBuildSubmitInputUsesWeixinSessionAndNewCommand(t *testing.T) {
	input := BuildSubmitInput(InboundMessage{ChatID: "chat-1", Text: "/new hello"})

	if input.ChannelKey != protocol.ChannelWeixin {
		t.Fatalf("channel = %s", input.ChannelKey)
	}
	if input.SessionKey == "weixin:chat-1" || input.Message != "hello" {
		t.Fatalf("input = %+v", input)
	}
	if input.Telemetry["weixin_new_session"] != "true" {
		t.Fatalf("new session telemetry = %+v", input.Telemetry)
	}
}

func TestRenderReplyChunksAssistantText(t *testing.T) {
	replies := RenderReplies("chat-1", []protocol.Event{
		{Type: protocol.EventAssistantTextDelta, Text: "hello"},
		{Type: protocol.EventAssistantTextDelta, Text: "world"},
	}, Options{ReplyChunkSize: 5})

	if len(replies) != 2 || replies[0].Text != "hello" || replies[1].Text != "world" {
		t.Fatalf("replies = %+v", replies)
	}
}

func TestRenderReplyChunksUnicodeWithoutCorruption(t *testing.T) {
	replies := RenderReplies("chat-1", []protocol.Event{
		{Type: protocol.EventAssistantTextDelta, Text: "你好世界"},
	}, Options{ReplyChunkSize: 3})
	if len(replies) != 2 || replies[0].Text != "你好世" || replies[1].Text != "界" {
		t.Fatalf("replies = %+v", replies)
	}
}

func TestRenderReplyDoesNotExposeInternalError(t *testing.T) {
	replies := RenderReplies("chat-1", []protocol.Event{{Type: protocol.EventError, Message: "database password leaked"}}, Options{})
	if len(replies) != 0 {
		t.Fatalf("replies = %+v", replies)
	}
}

func TestRenderReplyExplainsPermissionDeniedWithoutRetryAdvice(t *testing.T) {
	replies := RenderReplies("chat-1", []protocol.Event{{
		Type: protocol.EventError, Code: protocol.ErrorCodeGatewayPermissionDenied, Message: "discussion forbidden: secret identity",
	}}, Options{})
	if len(replies) != 0 {
		t.Fatalf("replies = %+v", replies)
	}
}

func TestRenderReplyExplainsInvalidMissionRequestWithoutRetryAdvice(t *testing.T) {
	replies := RenderReplies("chat-1", []protocol.Event{{
		Type: protocol.EventError, Code: protocol.ErrorCodeGatewayRequestInvalid, Message: "secret task conflict details",
	}}, Options{})
	if len(replies) != 0 {
		t.Fatalf("replies = %+v", replies)
	}
}

func TestRenderReplyShowsApprovalWithoutInternalPayload(t *testing.T) {
	replies := RenderReplies("chat-1", []protocol.Event{{
		Type: protocol.EventPermissionRequest, ToolName: "bash", Payload: map[string]string{"command": "secret command"},
	}}, Options{})
	if len(replies) != 1 || !strings.Contains(replies[0].Text, "bash") || !strings.Contains(replies[0].Text, "Orrery") || strings.Contains(replies[0].Text, "secret command") {
		t.Fatalf("approval replies = %+v", replies)
	}
}

func TestRenderReplyDirectsPersonalAssistantApprovalToNaturalReply(t *testing.T) {
	replies := RenderReplies("chat-1", []protocol.Event{{
		Type: protocol.EventPermissionRequest, ToolName: "internal_tool_name",
		Payload: map[string]string{"personal_assistant_reply": "notification", "approval_id": "internal-approval-id"},
	}}, Options{})
	if len(replies) != 1 || !strings.Contains(replies[0].Text, "直接回复“同意”或“拒绝”") ||
		strings.Contains(replies[0].Text, "internal_tool_name") || strings.Contains(replies[0].Text, "internal-approval-id") {
		t.Fatalf("personal assistant approval reply = %+v", replies)
	}
}

func TestProcessInboundSendsApprovalBeforeTurnCompletes(t *testing.T) {
	runner := &blockingApprovalRunner{release: make(chan struct{})}
	client := &fakeClient{sent: make(chan OutboundMessage, 1)}
	handle := &Handle{
		gateway: gwcore.NewGateway(runner),
		client:  client,
		options: Options{},
		active:  map[string]struct{}{},
	}
	type result struct {
		replies []OutboundMessage
		err     error
	}
	done := make(chan result, 1)
	go func() {
		replies, err := handle.ProcessInbound(context.Background(), InboundMessage{
			AccountID: "bot-a", MessageID: "message-a", ChatID: "chat-1", ContextToken: "context-a", Text: "inspect",
		})
		done <- result{replies: replies, err: err}
	}()

	select {
	case prompt := <-client.sent:
		if prompt.AccountID != "bot-a" || prompt.ChatID != "chat-1" || prompt.ContextToken != "context-a" || prompt.ClientID == "" || !strings.Contains(prompt.Text, "bash") {
			t.Fatalf("approval prompt = %+v", prompt)
		}
	case <-time.After(time.Second):
		t.Fatal("approval prompt was not sent while the turn was waiting")
	}
	select {
	case premature := <-done:
		t.Fatalf("turn completed before approval wait was released: %+v", premature)
	default:
	}
	close(runner.release)
	select {
	case completed := <-done:
		if completed.err != nil || len(completed.replies) != 1 || completed.replies[0].Text != "finished" {
			t.Fatalf("completed turn = %+v", completed)
		}
	case <-time.After(time.Second):
		t.Fatal("turn did not complete after approval wait was released")
	}
}

func TestHandleInboundSendsReplies(t *testing.T) {
	runner := &fakeRunner{}
	client := &fakeClient{}
	adapter := NewAdapter(gwconfig.WeixinAdapterConfig{Extra: map[string]any{
		"weixinAccounts": map[string]any{
			gwconfig.WeixinBindingKey("tenant-a", "user-a"): map[string]any{"userId": "user-a", "botId": "bot-a", "token": "secret"},
		},
	}}, client)
	handle, err := adapter.Start(context.Background(), channel.StartDeps{
		Gateway: gwcore.NewGateway(runner), StateDir: t.TempDir(), InboundEvents: newWeixinInboundStore(t),
	})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if !client.started {
		t.Fatal("client should be started")
	}
	weixinHandle := handle.(*Handle)

	if err := weixinHandle.HandleInbound(context.Background(), InboundMessage{AccountID: "bot-a", MessageID: "message-a", ChatID: "chat-1", Text: "ping"}); err != nil {
		t.Fatalf("HandleInbound failed: %v", err)
	}
	if len(runner.inputs) != 1 || runner.inputs[0].ChannelKey != protocol.ChannelWeixin {
		t.Fatalf("inputs = %+v", runner.inputs)
	}
	if len(client.replies) != 1 || client.replies[0].Text != "hello world" || client.replies[0].ClientID == "" {
		t.Fatalf("replies = %+v", client.replies)
	}
	expectedClientID := weixinMessageClientID(
		channel.InboundRunID(string(protocol.ChannelWeixin), "bot-a", "message-a"),
		0,
	)
	if client.replies[0].ClientID != expectedClientID {
		t.Fatalf("reply client id = %q, want %q", client.replies[0].ClientID, expectedClientID)
	}
}

func TestHandleProactiveSendUsesLatestDurableContextToken(t *testing.T) {
	store := newWeixinInboundStore(t)
	payload, err := json.Marshal(ilinkInboundJobPayload{
		EventID: "message_id:context",
		Message: InboundMessage{
			AccountID: "bot-a", ChatID: "user-a", SenderID: "user-a", SenderVerified: true,
			ContextToken: "durable-context-token", Text: "private inbound text",
		},
	})
	if err != nil {
		t.Fatalf("marshal inbound payload: %v", err)
	}
	if inserted, err := store.EnqueueInboundJob(context.Background(), channel.InboundJob{
		ChannelKey: "weixin", AccountID: "bot-a", EventID: "message_id:context", Payload: payload,
	}); err != nil || !inserted {
		t.Fatalf("enqueue inbound payload: inserted=%v err=%v", inserted, err)
	}
	client := &fakeReceiptClient{receipt: "receipt-a"}
	handle := &Handle{client: client, inbound: store, options: Options{}.WithDefaults()}
	result, err := handle.Send(context.Background(), channel.Delivery{
		ID: "delivery-a", AccountID: "bot-a", RecipientID: "user-a", Text: "approval reminder",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result.ExternalMessageID != "receipt-a" || len(client.replies) != 1 || client.replies[0].ContextToken != "durable-context-token" {
		t.Fatalf("result=%+v replies=%+v", result, client.replies)
	}
}

func TestHandleProactiveSendDoesNotReuseAnotherRecipientsContext(t *testing.T) {
	store := newWeixinInboundStore(t)
	payload, err := json.Marshal(ilinkInboundJobPayload{
		EventID: "message_id:other-user",
		Message: InboundMessage{
			AccountID: "bot-a", ChatID: "user-b", SenderID: "user-b", SenderVerified: true,
			ContextToken: "other-users-context",
		},
	})
	if err != nil {
		t.Fatalf("marshal inbound payload: %v", err)
	}
	if inserted, err := store.EnqueueInboundJob(context.Background(), channel.InboundJob{
		ChannelKey: "weixin", AccountID: "bot-a", EventID: "message_id:other-user", Payload: payload,
	}); err != nil || !inserted {
		t.Fatalf("enqueue inbound payload: inserted=%v err=%v", inserted, err)
	}
	client := &fakeReceiptClient{receipt: "unexpected-receipt"}
	handle := &Handle{client: client, inbound: store, options: Options{}.WithDefaults()}
	_, err = handle.Send(context.Background(), channel.Delivery{
		ID: "delivery-a", AccountID: "bot-a", RecipientID: "user-a", Text: "approval reminder",
	})
	if err == nil || !strings.Contains(err.Error(), "context token is unavailable") {
		t.Fatalf("Send error = %v", err)
	}
	if len(client.replies) != 0 {
		t.Fatalf("unexpected replies = %+v", client.replies)
	}
}

func TestInboundReplyClientIDsAreStableAndChunkScoped(t *testing.T) {
	source := channel.InboundRunID(string(protocol.ChannelWeixin), "bot-a", "message-a")
	first := weixinMessageClientID(source, 0)
	if first == "" || first != weixinMessageClientID(source, 0) {
		t.Fatalf("first client id is not stable: %q", first)
	}
	if second := weixinMessageClientID(source, 1); second == "" || second == first {
		t.Fatalf("chunk client ids collide: first=%q second=%q", first, second)
	}
	if empty := weixinMessageClientID("", 0); empty != "" {
		t.Fatalf("empty source client id = %q", empty)
	}
}

func TestAdapterRejectsEnabledGatewayWithoutPersonalAccount(t *testing.T) {
	runner := &fakeRunner{}
	client := &fakeClient{}
	adapter := NewAdapter(gwconfig.WeixinAdapterConfig{}, client)

	handle, err := adapter.Start(context.Background(), channel.StartDeps{
		Gateway: gwcore.NewGateway(runner), StateDir: t.TempDir(), InboundEvents: newWeixinInboundStore(t),
	})
	if err == nil {
		t.Fatal("Start should reject an enabled channel without a verified account")
	}
	if client.started {
		t.Fatal("client should not start without a personal account")
	}
	if handle != nil {
		t.Fatalf("handle = %#v, want nil", handle)
	}
}

func newWeixinInboundStore(t *testing.T) *gwinbound.Store {
	t.Helper()
	database, err := platformdb.Open(context.Background(), platformdb.Config{Driver: platformdb.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open inbound database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return gwinbound.NewStore(database)
}

func TestIlinkBotClientsShareDurableInboundDeduplication(t *testing.T) {
	var inboundCalls int
	var sendCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/sendmessage" {
			http.NotFound(w, req)
			return
		}
		sendCalls++
		writeIlinkJSON(w, http.StatusOK, map[string]any{"ret": 0})
	}))
	defer server.Close()
	store := newWeixinInboundStore(t)
	opts := Options{
		AccountID: "bot-a", BotToken: "token", IlinkBaseURL: server.URL, SendMessagePath: "sendmessage",
		RequestTimeoutSeconds: 2, InboundEvents: store,
	}.WithDefaults()
	processor := func(context.Context, InboundMessage) ([]OutboundMessage, error) {
		inboundCalls++
		return []OutboundMessage{{AccountID: "bot-a", ChatID: "user-a", Text: "reply"}}, nil
	}
	newClient := func(statePath string) *IlinkBotClient {
		return &IlinkBotClient{
			httpClient: server.Client(), allowInsecureLoopback: true, options: opts, inbound: processor,
			state: newFileIlinkBotStateStore(statePath), seen: map[string]struct{}{}, xWechatUIN: "uin",
		}
	}
	message := ilinkWeixinMessage{MessageID: "message-1", FromUserID: "user-a", ToUserID: "bot-a"}
	first := newClient(filepath.Join(t.TempDir(), "first.json"))
	second := newClient(filepath.Join(t.TempDir(), "second.json"))
	if err := first.processInboundMessage(context.Background(), opts, message, "message_id:message-1"); err != nil {
		t.Fatalf("first process: %v", err)
	}
	if err := second.processInboundMessage(context.Background(), opts, message, "message_id:message-1"); err != nil {
		t.Fatalf("second process: %v", err)
	}
	if inboundCalls != 1 || sendCalls != 1 {
		t.Fatalf("inbound calls=%d send calls=%d", inboundCalls, sendCalls)
	}
}

func TestIlinkBotClientRejectsMismatchedInboundAccount(t *testing.T) {
	store := newWeixinInboundStore(t)
	opts := Options{AccountID: "bot-a", InboundEvents: store}.WithDefaults()
	var inboundCalls int
	client := &IlinkBotClient{inbound: func(context.Context, InboundMessage) ([]OutboundMessage, error) {
		inboundCalls++
		return nil, nil
	}}
	message := ilinkWeixinMessage{MessageID: "message-mismatch", FromUserID: "user-a", ToUserID: "bot-b"}
	messageKey := "message_id:message-mismatch"
	if err := client.processInboundMessage(context.Background(), opts, message, messageKey); err == nil || !strings.Contains(err.Error(), "configured account") {
		t.Fatalf("mismatched account error = %v", err)
	}
	if inboundCalls != 0 {
		t.Fatalf("mismatched account reached inbound processor %d times", inboundCalls)
	}
	claim, err := store.ClaimInboundEvent(context.Background(), "weixin", "bot-a", messageKey)
	if err != nil || claim.State != channel.InboundEventAcquired {
		t.Fatalf("failed account validation retained the durable claim: claim=%+v err=%v", claim, err)
	}
}

func TestIlinkBotClientPollsAndSendsReplies(t *testing.T) {
	var gotInbound InboundMessage
	var gotSend map[string]any
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.Header.Get("AuthorizationType") != "ilink_bot_token" {
			t.Fatalf("AuthorizationType = %q", r.Header.Get("AuthorizationType"))
		}
		switch r.URL.Path {
		case "/getupdates":
			writeIlinkJSON(w, http.StatusOK, map[string]any{
				"get_updates_buf": "cursor-2",
				"msgs": []map[string]any{{
					"message_id":    "msg-1",
					"from_user_id":  "user-1@im.wechat",
					"to_user_id":    "bot-1",
					"context_token": "ctx-1",
					"item_list": []map[string]any{{
						"type":      1,
						"text_item": map[string]any{"text": "ping"},
					}},
				}},
			})
		case "/sendmessage":
			if err := json.NewDecoder(r.Body).Decode(&gotSend); err != nil {
				t.Fatalf("decode sendmessage: %v", err)
			}
			writeIlinkJSON(w, http.StatusOK, map[string]any{"ret": 0})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &IlinkBotClient{
		httpClient:            server.Client(),
		allowInsecureLoopback: true,
		options:               Options{BotToken: "secret", AccountID: "bot-1", IlinkBaseURL: server.URL, GetUpdatesPath: "getupdates", SendMessagePath: "sendmessage", RequestTimeoutSeconds: 2}.WithDefaults(),
		xWechatUIN:            "12345",
		getUpdatesBuf:         "cursor-1",
		inbound: func(ctx context.Context, message InboundMessage) ([]OutboundMessage, error) {
			gotInbound = message
			return []OutboundMessage{{ChatID: message.ChatID, ContextToken: message.ContextToken, Text: "pong"}}, nil
		},
	}

	if err := client.pollOnce(context.Background()); err != nil {
		t.Fatalf("pollOnce: %v", err)
	}
	if gotAuth != "Bearer secret" {
		t.Fatalf("authorization = %q", gotAuth)
	}
	if gotInbound.ChatID != "user-1@im.wechat" || gotInbound.SenderID != "user-1@im.wechat" || gotInbound.AccountID != "bot-1" || gotInbound.ContextToken != "ctx-1" || gotInbound.Text != "ping" {
		t.Fatalf("inbound = %+v", gotInbound)
	}
	msg, _ := gotSend["msg"].(map[string]any)
	if msg["to_user_id"] != "user-1@im.wechat" || msg["context_token"] != "ctx-1" {
		t.Fatalf("sendmessage msg = %#v", msg)
	}
	items, _ := msg["item_list"].([]any)
	first, _ := items[0].(map[string]any)
	textItem, _ := first["text_item"].(map[string]any)
	if textItem["text"] != "pong" {
		t.Fatalf("sendmessage item = %#v", first)
	}
	if client.currentGetUpdatesBuf() != "cursor-2" {
		t.Fatalf("cursor = %q", client.currentGetUpdatesBuf())
	}
}

func TestIlinkBotClientDoesNotAdvanceCursorWhenSendFails(t *testing.T) {
	var sendAttempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/getupdates":
			writeIlinkJSON(w, http.StatusOK, map[string]any{
				"ret":             0,
				"get_updates_buf": "cursor-2",
				"msgs": []map[string]any{{
					"message_id":   "msg-1",
					"from_user_id": "user-1@im.wechat",
					"to_user_id":   "bot-1",
					"item_list": []map[string]any{{
						"type":      1,
						"text_item": map[string]any{"text": "ping"},
					}},
				}},
			})
		case "/sendmessage":
			sendAttempts++
			writeIlinkJSON(w, http.StatusOK, map[string]any{"ret": 500, "errmsg": "send failed"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &IlinkBotClient{
		httpClient:            server.Client(),
		allowInsecureLoopback: true,
		options:               Options{BotToken: "secret", AccountID: "bot-1", IlinkBaseURL: server.URL, GetUpdatesPath: "getupdates", SendMessagePath: "sendmessage", RequestTimeoutSeconds: 2}.WithDefaults(),
		getUpdatesBuf:         "cursor-1",
		inbound: func(ctx context.Context, message InboundMessage) ([]OutboundMessage, error) {
			return []OutboundMessage{{ChatID: message.ChatID, Text: "pong"}}, nil
		},
	}

	err := client.pollOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "send failed") {
		t.Fatalf("pollOnce error = %v", err)
	}
	if sendAttempts != 1 {
		t.Fatalf("send attempts = %d", sendAttempts)
	}
	if client.currentGetUpdatesBuf() != "cursor-1" {
		t.Fatalf("cursor advanced on failed send: %q", client.currentGetUpdatesBuf())
	}
}

func TestIlinkBotClientDeadLettersPoisonMessageAndContinues(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	var sentTexts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/getupdates":
			writeIlinkJSON(w, http.StatusOK, map[string]any{
				"ret": 0, "get_updates_buf": "cursor-2",
				"msgs": []map[string]any{
					{"message_id": "poison", "from_user_id": "user-1", "to_user_id": "bot-1", "context_token": "ctx-1", "item_list": []map[string]any{{"type": 1, "text_item": map[string]any{"text": "poison"}}}},
					{"message_id": "healthy", "from_user_id": "user-1", "to_user_id": "bot-1", "context_token": "ctx-2", "item_list": []map[string]any{{"type": 1, "text_item": map[string]any{"text": "healthy"}}}},
				},
			})
		case "/sendmessage":
			var body struct {
				Message struct {
					Items []struct {
						TextItem struct {
							Text string `json:"text"`
						} `json:"text_item"`
					} `json:"item_list"`
				} `json:"msg"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode send message: %v", err)
			}
			if len(body.Message.Items) != 1 {
				t.Fatalf("send items = %+v", body.Message.Items)
			}
			sentTexts = append(sentTexts, body.Message.Items[0].TextItem.Text)
			writeIlinkJSON(w, http.StatusOK, map[string]any{"ret": 0})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	store := newFileIlinkBotStateStore(statePath)
	opts := Options{
		BotToken: "secret", AccountID: "bot-1", IlinkBaseURL: server.URL,
		GetUpdatesPath: "getupdates", SendMessagePath: "sendmessage", StatePath: statePath,
		RequestTimeoutSeconds: 2, InboundMaxAttempts: 3, InboundEvents: newWeixinInboundStore(t),
	}.WithDefaults()
	inboundCalls := map[string]int{}
	client := &IlinkBotClient{
		httpClient: server.Client(), allowInsecureLoopback: true, options: opts, state: store,
		getUpdatesBuf: "cursor-1", xWechatUIN: "uin",
		inbound: func(_ context.Context, message InboundMessage) ([]OutboundMessage, error) {
			inboundCalls[message.MessageID]++
			if message.MessageID == "poison" {
				return nil, errors.New("knowledge dependency unavailable")
			}
			return []OutboundMessage{{AccountID: message.AccountID, ChatID: message.ChatID, ContextToken: message.ContextToken, Text: "healthy reply"}}, nil
		},
	}

	for attempt := 1; attempt <= 2; attempt++ {
		err := client.pollOnce(context.Background())
		if err == nil || !strings.Contains(err.Error(), "knowledge dependency unavailable") {
			t.Fatalf("attempt %d error = %v", attempt, err)
		}
		if client.currentGetUpdatesBuf() != "cursor-1" {
			t.Fatalf("attempt %d advanced cursor", attempt)
		}
		if attempt == 1 {
			loaded, err := loadIlinkBotState(context.Background(), store, opts)
			if err != nil || len(loaded.MessageFailures) != 1 || loaded.MessageFailures[0].Attempts != 1 {
				t.Fatalf("load failure state after restart = %+v err=%v", loaded, err)
			}
			seen, order := ilinkSeenIndex(loaded.SeenMessageIDs, opts.SeenMessageLimit)
			client = &IlinkBotClient{
				httpClient: server.Client(), allowInsecureLoopback: true, options: opts, state: store,
				getUpdatesBuf: loaded.GetUpdatesBuf, seen: seen, seenOrder: order,
				messageFailures: ilinkFailureIndex(loaded.MessageFailures), deadLetters: loaded.DeadLetters,
				xWechatUIN: "uin", inbound: client.inbound,
			}
		}
	}
	if err := client.pollOnce(context.Background()); err != nil {
		t.Fatalf("dead-letter poll: %v", err)
	}
	if client.currentGetUpdatesBuf() != "cursor-2" || inboundCalls["poison"] != 3 || inboundCalls["healthy"] != 1 {
		t.Fatalf("cursor=%q calls=%+v", client.currentGetUpdatesBuf(), inboundCalls)
	}
	if len(sentTexts) != 1 || sentTexts[0] != "healthy reply" {
		t.Fatalf("sent texts = %+v", sentTexts)
	}
	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.MessageFailures) != 0 || len(state.DeadLetters) != 1 || state.DeadLetters[0].MessageKey != "message_id:poison" || state.DeadLetters[0].Attempts != 3 || len(state.SeenMessageIDs) != 2 {
		t.Fatalf("persisted state = %+v", state)
	}
	if err := client.pollOnce(context.Background()); err != nil {
		t.Fatalf("deduplicated poll: %v", err)
	}
	if inboundCalls["poison"] != 3 || inboundCalls["healthy"] != 1 || len(sentTexts) != 1 {
		t.Fatalf("deduplicated calls=%+v sent=%+v", inboundCalls, sentTexts)
	}
}

func TestIlinkBotClientQueuedPoisonMessageNeverSendsSubstituteReply(t *testing.T) {
	var sentTexts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sendmessage" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Message struct {
				Items []struct {
					TextItem struct {
						Text string `json:"text"`
					} `json:"text_item"`
				} `json:"item_list"`
			} `json:"msg"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode send message: %v", err)
		}
		if len(body.Message.Items) != 1 {
			t.Fatalf("send items = %+v", body.Message.Items)
		}
		sentTexts = append(sentTexts, body.Message.Items[0].TextItem.Text)
		writeIlinkJSON(w, http.StatusOK, map[string]any{"ret": 0})
	}))
	defer server.Close()

	client := &IlinkBotClient{
		httpClient: server.Client(), allowInsecureLoopback: true,
		options:    Options{BotToken: "secret", AccountID: "bot-1", IlinkBaseURL: server.URL, SendMessagePath: "sendmessage", RequestTimeoutSeconds: 2}.WithDefaults(),
		xWechatUIN: "uin",
		inbound: func(context.Context, InboundMessage) ([]OutboundMessage, error) {
			return nil, errors.New("knowledge dependency unavailable")
		},
	}
	payload, err := json.Marshal(ilinkInboundJobPayload{
		EventID: "message_id:poison",
		Message: InboundMessage{AccountID: "bot-1", ChatID: "user-1", MessageID: "poison", SenderID: "user-1", SenderVerified: true, ContextToken: "ctx-1", Text: "poison"},
	})
	if err != nil {
		t.Fatal(err)
	}
	job := channel.InboundJob{ChannelKey: "weixin", AccountID: "bot-1", EventID: "message_id:poison", Payload: payload, Attempts: 2, MaxAttempts: 3}
	if err := client.processQueuedInbound(context.Background(), job); err == nil {
		t.Fatal("non-final poison attempt returned nil")
	}
	if len(sentTexts) != 0 {
		t.Fatalf("non-final attempt sent notification: %+v", sentTexts)
	}
	job.Attempts = 3
	if err := client.processQueuedInbound(context.Background(), job); err == nil {
		t.Fatal("final poison attempt returned nil")
	}
	if len(sentTexts) != 0 {
		t.Fatalf("final failure sent a substitute reply: %+v", sentTexts)
	}
	if err := client.processQueuedInbound(context.Background(), job); err == nil {
		t.Fatal("replayed final attempt returned nil")
	}
	if len(sentTexts) != 0 {
		t.Fatalf("replayed final failure sent a substitute reply: %+v", sentTexts)
	}
	const answer = "网页访问超时，我无法确认页面内容。"
	client.inbound = func(_ context.Context, message InboundMessage) ([]OutboundMessage, error) {
		return []OutboundMessage{{AccountID: message.AccountID, ChatID: message.ChatID, ContextToken: message.ContextToken, Text: answer}}, nil
	}
	if err := client.processQueuedInbound(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if len(sentTexts) != 1 || sentTexts[0] != answer {
		t.Fatalf("Agent explanation was not delivered unchanged: %+v", sentTexts)
	}
}

func TestIlinkBotClientDeadLettersInvalidMessageAndAdvancesCursor(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/getupdates" {
			http.NotFound(w, r)
			return
		}
		writeIlinkJSON(w, http.StatusOK, map[string]any{
			"ret": 0, "get_updates_buf": "cursor-after-invalid",
			"msgs": []map[string]any{{"message_id": "invalid", "to_user_id": "bot-1", "item_list": []map[string]any{{"type": 1, "text_item": map[string]any{"text": "invalid"}}}}},
		})
	}))
	defer server.Close()

	store := newFileIlinkBotStateStore(statePath)
	opts := Options{
		BotToken: "secret", AccountID: "bot-1", IlinkBaseURL: server.URL, GetUpdatesPath: "getupdates",
		StatePath: statePath, RequestTimeoutSeconds: 2, InboundMaxAttempts: 3, InboundEvents: newWeixinInboundStore(t),
	}.WithDefaults()
	client := &IlinkBotClient{
		httpClient: server.Client(), allowInsecureLoopback: true, options: opts,
		queue: opts.InboundEvents.(channel.InboundJobQueue), state: store, getUpdatesBuf: "cursor-before", xWechatUIN: "uin",
		inbound: func(context.Context, InboundMessage) ([]OutboundMessage, error) {
			t.Fatal("invalid message reached inbound processor")
			return nil, nil
		},
	}
	for attempt := 1; attempt < opts.InboundMaxAttempts; attempt++ {
		err := client.pollOnce(context.Background())
		if err == nil || !strings.Contains(err.Error(), "no verified sender") {
			t.Fatalf("attempt %d error = %v", attempt, err)
		}
		if client.currentGetUpdatesBuf() != "cursor-before" {
			t.Fatalf("attempt %d advanced cursor", attempt)
		}
	}
	if err := client.pollOnce(context.Background()); err != nil {
		t.Fatalf("final invalid attempt: %v", err)
	}
	if client.currentGetUpdatesBuf() != "cursor-after-invalid" {
		t.Fatalf("cursor = %q", client.currentGetUpdatesBuf())
	}
	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.DeadLetters) != 1 || state.DeadLetters[0].MessageKey != "message_id:invalid" || state.DeadLetters[0].Attempts != 3 {
		t.Fatalf("dead-letter state = %+v", state)
	}
}

func TestIlinkBotClientRejectsMalformedResponses(t *testing.T) {
	t.Run("getupdates", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeIlinkJSON(w, http.StatusOK, map[string]any{"ok": true})
		}))
		defer server.Close()

		client := &IlinkBotClient{
			httpClient:            server.Client(),
			allowInsecureLoopback: true,
			options:               Options{BotToken: "secret", AccountID: "bot-1", IlinkBaseURL: server.URL, GetUpdatesPath: "getupdates", RequestTimeoutSeconds: 2}.WithDefaults(),
			getUpdatesBuf:         "cursor-1",
			inbound: func(ctx context.Context, message InboundMessage) ([]OutboundMessage, error) {
				t.Fatal("inbound should not be called for malformed getupdates")
				return nil, nil
			},
		}

		err := client.pollOnce(context.Background())
		if err == nil || !strings.Contains(err.Error(), "missing msgs/messages") {
			t.Fatalf("pollOnce error = %v", err)
		}
		if client.currentGetUpdatesBuf() != "cursor-1" {
			t.Fatalf("cursor advanced on malformed getupdates: %q", client.currentGetUpdatesBuf())
		}
	})

	t.Run("sendmessage error message without code", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/getupdates":
				writeIlinkJSON(w, http.StatusOK, map[string]any{
					"ret":             0,
					"get_updates_buf": "cursor-2",
					"msgs": []map[string]any{{
						"message_id":   "msg-1",
						"from_user_id": "user-1@im.wechat",
						"to_user_id":   "bot-1",
						"item_list": []map[string]any{{
							"type":      1,
							"text_item": map[string]any{"text": "ping"},
						}},
					}},
				})
			case "/sendmessage":
				writeIlinkJSON(w, http.StatusOK, map[string]any{"errmsg": "send failed"})
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		client := &IlinkBotClient{
			httpClient:            server.Client(),
			allowInsecureLoopback: true,
			options:               Options{BotToken: "secret", AccountID: "bot-1", IlinkBaseURL: server.URL, GetUpdatesPath: "getupdates", SendMessagePath: "sendmessage", RequestTimeoutSeconds: 2}.WithDefaults(),
			getUpdatesBuf:         "cursor-1",
			inbound: func(ctx context.Context, message InboundMessage) ([]OutboundMessage, error) {
				return []OutboundMessage{{ChatID: message.ChatID, Text: "pong"}}, nil
			},
		}

		err := client.pollOnce(context.Background())
		if err == nil || !strings.Contains(err.Error(), "send failed") {
			t.Fatalf("pollOnce error = %v", err)
		}
		if client.currentGetUpdatesBuf() != "cursor-1" {
			t.Fatalf("cursor advanced on malformed sendmessage: %q", client.currentGetUpdatesBuf())
		}
	})
}

func TestIlinkBotClientAcceptsSuccessfulSendMessageResponseWithoutReturnCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/getupdates":
			writeIlinkJSON(w, http.StatusOK, map[string]any{
				"ret":             0,
				"get_updates_buf": "cursor-2",
				"msgs": []map[string]any{{
					"message_id":   "msg-1",
					"from_user_id": "user-1@im.wechat",
					"to_user_id":   "bot-1",
					"item_list": []map[string]any{{
						"type":      1,
						"text_item": map[string]any{"text": "ping"},
					}},
				}},
			})
		case "/sendmessage":
			writeIlinkJSON(w, http.StatusOK, map[string]any{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &IlinkBotClient{
		httpClient:            server.Client(),
		allowInsecureLoopback: true,
		options:               Options{BotToken: "secret", AccountID: "bot-1", IlinkBaseURL: server.URL, GetUpdatesPath: "getupdates", SendMessagePath: "sendmessage", RequestTimeoutSeconds: 2}.WithDefaults(),
		getUpdatesBuf:         "cursor-1",
		inbound: func(ctx context.Context, message InboundMessage) ([]OutboundMessage, error) {
			return []OutboundMessage{{ChatID: message.ChatID, Text: "pong"}}, nil
		},
	}

	if err := client.pollOnce(context.Background()); err != nil {
		t.Fatalf("pollOnce: %v", err)
	}
	if client.currentGetUpdatesBuf() != "cursor-2" {
		t.Fatalf("cursor = %q", client.currentGetUpdatesBuf())
	}
}

func TestIlinkBotClientPersistsCursorAndSeenMessages(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "weixin-ilink-state.json")
	var sendAttempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/getupdates":
			writeIlinkJSON(w, http.StatusOK, map[string]any{
				"ret":           0,
				"getUpdatesBuf": "cursor-2",
				"messages": []map[string]any{{
					"message_id":   "msg-1",
					"from_user_id": "user-1@im.wechat",
					"to_user_id":   "bot-1",
					"item_list": []map[string]any{{
						"type":      1,
						"text_item": map[string]any{"text": "ping"},
					}},
				}},
			})
		case "/sendmessage":
			sendAttempts++
			writeIlinkJSON(w, http.StatusOK, map[string]any{"ret": "0"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	store := newFileIlinkBotStateStore(statePath)
	opts := Options{BotToken: "secret", AccountID: "bot-1", IlinkBaseURL: server.URL, GetUpdatesPath: "getupdates", SendMessagePath: "sendmessage", RequestTimeoutSeconds: 2, StatePath: statePath}.WithDefaults()
	client := &IlinkBotClient{
		httpClient:            server.Client(),
		allowInsecureLoopback: true,
		options:               opts,
		state:                 store,
		getUpdatesBuf:         "cursor-1",
		inbound: func(ctx context.Context, message InboundMessage) ([]OutboundMessage, error) {
			return []OutboundMessage{{ChatID: message.ChatID, Text: "pong"}}, nil
		},
	}

	if err := client.pollOnce(context.Background()); err != nil {
		t.Fatalf("pollOnce: %v", err)
	}
	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if state.GetUpdatesBuf != "cursor-2" || len(state.SeenMessageIDs) != 1 || state.SeenMessageIDs[0] != "message_id:msg-1" {
		t.Fatalf("state = %+v", state)
	}

	loaded, err := loadIlinkBotState(context.Background(), store, Options{GetUpdatesBuf: "older", SeenMessageLimit: 100}.WithDefaults())
	if err != nil {
		t.Fatalf("load state through client helper: %v", err)
	}
	seen, order := ilinkSeenIndex(loaded.SeenMessageIDs, 100)
	client = &IlinkBotClient{
		httpClient:            server.Client(),
		allowInsecureLoopback: true,
		options:               opts,
		state:                 store,
		getUpdatesBuf:         loaded.GetUpdatesBuf,
		seen:                  seen,
		seenOrder:             order,
		inbound: func(ctx context.Context, message InboundMessage) ([]OutboundMessage, error) {
			t.Fatal("persisted seen message should be skipped")
			return nil, nil
		},
	}
	if err := client.pollOnce(context.Background()); err != nil {
		t.Fatalf("second pollOnce: %v", err)
	}
	if sendAttempts != 1 {
		t.Fatalf("send attempts = %d, want 1", sendAttempts)
	}
}

func TestIlinkLoginClientFetchesAndPollsQRCode(t *testing.T) {
	var gotLocalTokens []string
	var gotPostAuthType string
	var gotBotType string
	var gotVerifyCode string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ilink/bot/get_bot_qrcode":
			gotPostAuthType = r.Header.Get("AuthorizationType")
			gotBotType = r.URL.Query().Get("bot_type")
			var body struct {
				LocalTokenList []string `json:"local_token_list"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode qrcode request: %v", err)
			}
			gotLocalTokens = body.LocalTokenList
			writeIlinkJSON(w, http.StatusOK, map[string]any{
				"qrcode":             "qr-token",
				"qrcode_img_content": "https://weixin.example.test/login/qr-token",
			})
		case "/ilink/bot/get_qrcode_status":
			gotVerifyCode = r.URL.Query().Get("verify_code")
			writeIlinkJSON(w, http.StatusOK, map[string]any{
				"status":        "confirmed",
				"bot_token":     "bot-token",
				"ilink_bot_id":  "bot-1@im.bot",
				"baseurl":       "https://ilink-idc.example.test",
				"ilink_user_id": "user-1@im.wechat",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewIlinkLoginClient(server.Client())
	opts := IlinkLoginOptions{LoginBaseURL: server.URL, BotType: "3", IlinkAppID: "bot", IlinkAppClientVersion: "132102", RequestTimeoutSeconds: 2, AllowInsecureLoopback: true}
	qr, err := client.FetchQRCode(context.Background(), opts, []string{"old-token", "********", "old-token"})
	if err != nil {
		t.Fatalf("FetchQRCode: %v", err)
	}
	if qr.Code != "qr-token" || qr.URL == "" {
		t.Fatalf("qr = %+v", qr)
	}
	if gotPostAuthType != "ilink_bot_token" || gotBotType != "3" {
		t.Fatalf("headers/query auth=%q botType=%q", gotPostAuthType, gotBotType)
	}
	if len(gotLocalTokens) != 1 || gotLocalTokens[0] != "old-token" {
		t.Fatalf("local tokens = %+v", gotLocalTokens)
	}

	status, err := client.PollQRCodeStatus(context.Background(), opts, qr.Code, "123456")
	if err != nil {
		t.Fatalf("PollQRCodeStatus: %v", err)
	}
	if status.Status != "confirmed" || status.BotToken != "bot-token" || status.IlinkBotID != "bot-1@im.bot" || status.IlinkUserID != "user-1@im.wechat" {
		t.Fatalf("status = %+v", status)
	}
	if gotVerifyCode != "123456" {
		t.Fatalf("verify code = %q", gotVerifyCode)
	}
}

func TestValidateIlinkBotBaseURLRestrictsProductionOrigins(t *testing.T) {
	for _, valid := range []string{
		"https://ilinkai.weixin.qq.com",
		"https://shard.weixin.qq.com/path",
	} {
		if err := ValidateIlinkBotBaseURL(valid, false); err != nil {
			t.Fatalf("official URL %q rejected: %v", valid, err)
		}
	}
	for _, invalid := range []string{
		"http://ilinkai.weixin.qq.com",
		"http://127.0.0.1:8080",
		"https://127.0.0.1",
		"https://10.0.0.1",
		"https://example.com",
		"https://ilinkai.weixin.qq.com:8443",
		"https://user:secret@ilinkai.weixin.qq.com",
		"https://ilinkai.weixin.qq.com?target=evil",
	} {
		if err := ValidateIlinkBotBaseURL(invalid, false); err == nil {
			t.Fatalf("production accepted untrusted URL %q", invalid)
		}
	}
	if err := ValidateIlinkBotBaseURL("http://127.0.0.1:8080", true); err != nil {
		t.Fatalf("explicit test loopback rejected: %v", err)
	}
}

func TestIlinkLoginClientRejectsRedirectsAndOversizedResponses(t *testing.T) {
	redirectTargetCalled := false
	redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirected" {
			redirectTargetCalled = true
			writeIlinkJSON(w, http.StatusOK, map[string]any{"qrcode": "qr", "qrcode_img_content": "image"})
			return
		}
		http.Redirect(w, r, "/redirected", http.StatusFound)
	}))
	defer redirectServer.Close()
	opts := IlinkLoginOptions{LoginBaseURL: redirectServer.URL, AllowInsecureLoopback: true}
	if _, err := NewIlinkLoginClient(redirectServer.Client()).FetchQRCode(context.Background(), opts, nil); err == nil || !strings.Contains(err.Error(), "HTTP 302") {
		t.Fatalf("redirect response error = %v", err)
	}
	if redirectTargetCalled {
		t.Fatal("login client followed an upstream redirect")
	}

	oversizedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"qrcode":"`))
		_, _ = w.Write([]byte(strings.Repeat("x", maxIlinkResponseBytes)))
		_, _ = w.Write([]byte(`","qrcode_img_content":"image"}`))
	}))
	defer oversizedServer.Close()
	opts.LoginBaseURL = oversizedServer.URL
	if _, err := NewIlinkLoginClient(oversizedServer.Client()).FetchQRCode(context.Background(), opts, nil); err == nil || !strings.Contains(err.Error(), "exceeds 1 MiB") {
		t.Fatalf("oversized response error = %v", err)
	}
}
