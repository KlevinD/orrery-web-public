package feishu

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

func (f *fakeRunner) RunTurn(ctx context.Context, input protocol.SubmitTurnInput) (<-chan protocol.Event, error) {
	f.inputs = append(f.inputs, input)
	ch := make(chan protocol.Event, 2)
	go func() {
		defer close(ch)
		ch <- protocol.Event{Type: protocol.EventAssistantTextDelta, Text: "reply"}
		ch <- protocol.Event{Type: protocol.EventTurnCompleted, FinishReason: "completed"}
	}()
	return ch, nil
}

type fakeClient struct {
	replies []Reply
}

func (f *fakeClient) Start(context.Context, Options, func(context.Context, InboundEvent) error) error {
	return nil
}

func (f *fakeClient) Stop(context.Context) error { return nil }

func (f *fakeClient) SendReply(ctx context.Context, reply Reply) error {
	f.replies = append(f.replies, reply)
	return nil
}

type capturingDirectClient struct {
	fakeClient
	recipientID    string
	title          string
	text           string
	format         string
	idempotencyKey string
	messageID      string
}

func (c *capturingDirectClient) SendDirect(_ context.Context, recipientID, title, text, format, idempotencyKey string) (string, error) {
	c.recipientID = recipientID
	c.title = title
	c.text = text
	c.format = format
	c.idempotencyKey = idempotencyKey
	return c.messageID, nil
}

type blockingRunner struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *blockingRunner) RunTurn(ctx context.Context, input protocol.SubmitTurnInput) (<-chan protocol.Event, error) {
	r.once.Do(func() { close(r.started) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-r.release:
	}
	events := make(chan protocol.Event, 2)
	events <- protocol.Event{Type: protocol.EventAssistantTextDelta, Text: "durable reply"}
	events <- protocol.Event{Type: protocol.EventTurnCompleted, FinishReason: "completed"}
	close(events)
	return events, nil
}

type queuedClient struct {
	inbound func(context.Context, InboundEvent) error
	replies chan Reply
}

func (c *queuedClient) Start(_ context.Context, _ Options, inbound func(context.Context, InboundEvent) error) error {
	c.inbound = inbound
	return nil
}

func (c *queuedClient) Stop(context.Context) error { return nil }

func (c *queuedClient) SendReply(_ context.Context, reply Reply) error {
	c.replies <- reply
	return nil
}

func TestOptionsFromConfigMapsFeishuFields(t *testing.T) {
	opts := OptionsFromConfig(gwconfig.FeishuAdapterConfig{
		Enabled:             true,
		AppID:               "app",
		AppSecret:           "secret",
		EncryptKey:          "encrypt",
		VerifyToken:         "verify",
		DefaultSessionLabel: "general",
		ConnectionMode:      "webhook",
		DomainName:          "feishu",
	})

	if !opts.Enabled || opts.AppID != "app" || opts.ConnectionMode != ModeWebhook || opts.DefaultSessionLabel != "general" {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestAdapterStartsOutboundWithoutInboundStore(t *testing.T) {
	adapter := NewAccountAdapter("main", gwconfig.FeishuAccountConfig{
		Enabled: true, AppID: "app", AppSecret: "secret", ConnectionMode: ModeOutbound, DomainName: "feishu",
	}, &fakeClient{})
	handle, err := adapter.Start(context.Background(), channel.StartDeps{Gateway: gwcore.NewGateway(&fakeRunner{})})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if handle == nil {
		t.Fatal("outbound adapter returned nil handle")
	}
	if err := handle.Stop(context.Background(), "test"); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
}

func TestHandleSendForwardsMarkdownDeliveryToDirectClient(t *testing.T) {
	client := &capturingDirectClient{messageID: "om_message_1"}
	adapter := NewAccountAdapter("main", gwconfig.FeishuAccountConfig{
		Enabled: true, AppID: "app", AppSecret: "secret", ConnectionMode: ModeOutbound, DomainName: "feishu",
	}, client)
	handle, err := adapter.Start(context.Background(), channel.StartDeps{Gateway: gwcore.NewGateway(&fakeRunner{})})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() {
		if err := handle.Stop(context.Background(), "test"); err != nil {
			t.Fatalf("Stop failed: %v", err)
		}
	}()

	result, err := handle.(channel.OutboundHandle).Send(context.Background(), channel.Delivery{
		ID:          "delivery-1",
		AccountID:   "main",
		RecipientID: "oc_chat_1",
		Title:       "每日新闻简报",
		Text:        "# 每日新闻简报\n\n1. 第一条新闻",
		Format:      "markdown",
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if result.ExternalMessageID != "om_message_1" {
		t.Fatalf("external message id = %q", result.ExternalMessageID)
	}
	if client.recipientID != "oc_chat_1" || client.title != "每日新闻简报" || client.format != "markdown" {
		t.Fatalf("direct delivery = recipient %q title %q format %q", client.recipientID, client.title, client.format)
	}
	if client.text != "# 每日新闻简报\n\n1. 第一条新闻" || client.idempotencyKey != "delivery-1" {
		t.Fatalf("direct delivery = text %q idempotency key %q", client.text, client.idempotencyKey)
	}
}

func TestURLVerificationReturnsChallenge(t *testing.T) {
	handler := NewWebhookHandler(nil, Options{VerifyToken: "verify"})
	challenge, ok := handler.VerifyURL(URLVerification{Token: "verify", Challenge: "challenge-1"})

	if !ok || challenge != "challenge-1" {
		t.Fatalf("challenge = %q ok=%v", challenge, ok)
	}
}

func TestEventMapperHandlesCommandsAndMessages(t *testing.T) {
	input := BuildSubmitInput(InboundEvent{
		EventID: "evt-1",
		ChatID:  "chat-1",
		Text:    "/switch-project proj-1 build",
	})

	if input.ChannelKey != protocol.ChannelFeishu {
		t.Fatalf("channel = %s", input.ChannelKey)
	}
	if input.SessionKey != "feishu:chat-1" || input.ProjectKey != "proj-1" || input.Message != "build" {
		t.Fatalf("input = %+v", input)
	}
	stable := BuildSubmitInput(InboundEvent{EventID: "evt-1", AccountID: "feishu:app", ChatID: "chat-1", Text: "build"})
	if stable.RunID == "" || stable.RunID != BuildSubmitInput(InboundEvent{EventID: "evt-1", AccountID: "feishu:app", ChatID: "chat-1", Text: "build"}).RunID {
		t.Fatalf("stable inbound run id = %q", stable.RunID)
	}
}

func TestRenderReplyDoesNotExposeInternalError(t *testing.T) {
	reply, ok := RenderReply("chat-1", []protocol.Event{{Type: protocol.EventError, Message: "database password leaked"}})
	if ok || reply.Text != "" {
		t.Fatalf("reply = %+v ok=%v", reply, ok)
	}
}

func TestFeishuHTTPClientRejectsOversizedResponseAndCrossOriginRedirect(t *testing.T) {
	t.Run("oversized response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"code":0}` + strings.Repeat(" ", feishuMaxAPIResponseBytes)))
		}))
		defer server.Close()
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		if _, err := newFeishuHTTPClient().Do(req); err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("oversized response error = %v", err)
		}
	})

	t.Run("cross-origin redirect", func(t *testing.T) {
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("cross-origin redirect target should not be called")
		}))
		defer target.Close()
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL, http.StatusFound)
		}))
		defer origin.Close()
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, origin.URL, nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		if _, err := newFeishuHTTPClient().Do(req); err == nil || !strings.Contains(err.Error(), "cross-origin") {
			t.Fatalf("redirect error = %v", err)
		}
	})
}

func TestFeishuEndpointValidatorOnlyAllowsOfficialSecureEndpoints(t *testing.T) {
	tests := []struct {
		name       string
		domainName string
		endpoint   string
		wantError  bool
	}{
		{name: "feishu official subdomain", domainName: "feishu", endpoint: "wss://msg-frontier.feishu.cn/ws/v2?ticket=secret"},
		{name: "lark official subdomain", domainName: "larksuite", endpoint: "wss://msg-frontier.larksuite.com/ws/v2"},
		{name: "explicit tls port", domainName: "feishu", endpoint: "wss://msg-frontier.feishu.cn:443/ws/v2"},
		{name: "insecure scheme", domainName: "feishu", endpoint: "ws://msg-frontier.feishu.cn/ws/v2", wantError: true},
		{name: "loopback ip", domainName: "feishu", endpoint: "wss://127.0.0.1/ws", wantError: true},
		{name: "non official domain", domainName: "feishu", endpoint: "wss://feishu.cn.attacker.example/ws", wantError: true},
		{name: "wrong official environment", domainName: "larksuite", endpoint: "wss://msg-frontier.feishu.cn/ws/v2", wantError: true},
		{name: "non tls port", domainName: "feishu", endpoint: "wss://msg-frontier.feishu.cn:8443/ws", wantError: true},
		{name: "userinfo", domainName: "feishu", endpoint: "wss://user@msg-frontier.feishu.cn/ws", wantError: true},
		{name: "fragment", domainName: "feishu", endpoint: "wss://msg-frontier.feishu.cn/ws#fragment", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := feishuEndpointValidator(test.domainName)(test.endpoint)
			if test.wantError && err == nil {
				t.Fatalf("endpoint %q was accepted", test.endpoint)
			}
			if !test.wantError && err != nil {
				t.Fatalf("endpoint %q was rejected: %v", test.endpoint, err)
			}
		})
	}
}

func TestNormalizeFeishuChatTypeFailsClosedForUnknownValues(t *testing.T) {
	for input, want := range map[string]string{
		"p2p": "direct", "direct": "direct", "group": "group", "": "unknown", "new-provider-kind": "unknown",
	} {
		if got := normalizeFeishuChatType(input); got != want {
			t.Fatalf("normalizeFeishuChatType(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestAdapterDedupesEventsAndSendsReplies(t *testing.T) {
	runner := &fakeRunner{}
	client := &fakeClient{}
	adapter := NewAdapter(gwconfig.FeishuAdapterConfig{Enabled: true, AppID: "app", DefaultSessionLabel: "general"}, client)
	handle, err := adapter.Start(context.Background(), channel.StartDeps{
		Gateway: gwcore.NewGateway(runner), InboundEvents: newFeishuInboundStore(t),
	})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	feishuHandle := handle.(*Handle)
	event := InboundEvent{EventID: "evt-1", ChatID: "chat-1", Text: "hello"}

	if err := feishuHandle.HandleEvent(context.Background(), event); err != nil {
		t.Fatalf("HandleEvent failed: %v", err)
	}
	if err := feishuHandle.HandleEvent(context.Background(), event); err != nil {
		t.Fatalf("HandleEvent duplicate failed: %v", err)
	}

	if len(runner.inputs) != 1 || runner.inputs[0].ChannelKey != protocol.ChannelFeishu {
		t.Fatalf("inputs = %+v", runner.inputs)
	}
	if len(client.replies) != 1 || client.replies[0].ChatID != "chat-1" || client.replies[0].Text != "reply" {
		t.Fatalf("replies = %+v", client.replies)
	}
	expectedKey := channel.InboundRunID(string(protocol.ChannelFeishu), "feishu:app", "evt-1")
	if client.replies[0].IdempotencyKey != expectedKey {
		t.Fatalf("reply idempotency key = %q, want %q", client.replies[0].IdempotencyKey, expectedKey)
	}
}

func TestStreamCallbackAcknowledgesBeforeAgentTurnCompletes(t *testing.T) {
	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	client := &queuedClient{replies: make(chan Reply, 1)}
	adapter := NewAdapter(gwconfig.FeishuAdapterConfig{Enabled: true, AppID: "app", DefaultSessionLabel: "general"}, client)
	handle, err := adapter.Start(context.Background(), channel.StartDeps{
		Gateway: gwcore.NewGateway(runner), InboundEvents: newFeishuInboundStore(t),
	})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = handle.Stop(ctx, "test cleanup")
	})

	callbackDone := make(chan error, 1)
	go func() {
		callbackDone <- client.inbound(context.Background(), InboundEvent{
			EventID: "queued-event", MessageID: "queued-message", ChatID: "chat-queued", Text: "hello",
		})
	}()
	select {
	case err := <-callbackDone:
		if err != nil {
			t.Fatalf("callback enqueue failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("provider callback waited for the agent turn")
	}
	select {
	case <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("durable worker did not start the agent turn")
	}
	select {
	case reply := <-client.replies:
		t.Fatalf("reply arrived before the blocked turn completed: %+v", reply)
	default:
	}
	close(runner.release)
	select {
	case reply := <-client.replies:
		if reply.ChatID != "chat-queued" || reply.Text != "durable reply" {
			t.Fatalf("reply = %+v", reply)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("durable worker did not send the reply")
	}
}

func newFeishuInboundStore(t *testing.T) *gwinbound.Store {
	t.Helper()
	database, err := platformdb.Open(context.Background(), platformdb.Config{Driver: platformdb.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open inbound database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return gwinbound.NewStore(database)
}
