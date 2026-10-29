package dingtalk

import (
	"bytes"
	"context"
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
		ch <- protocol.Event{Type: protocol.EventAssistantTextDelta, Text: "dingtalk reply"}
		ch <- protocol.Event{Type: protocol.EventTurnCompleted, FinishReason: "completed"}
	}()
	return ch, nil
}

type fakeClient struct {
	started bool
	replies []OutboundMessage
	direct  []string
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
	events <- protocol.Event{Type: protocol.EventAssistantTextDelta, Text: "durable dingtalk reply"}
	events <- protocol.Event{Type: protocol.EventTurnCompleted, FinishReason: "completed"}
	close(events)
	return events, nil
}

type queuedClient struct {
	inbound        func(context.Context, InboundMessage) error
	directReplies  chan string
	webhookReplies chan OutboundMessage
}

func (c *queuedClient) StartStream(_ context.Context, _ Options, inbound func(context.Context, InboundMessage) error) error {
	c.inbound = inbound
	return nil
}

func (c *queuedClient) Stop(context.Context) error { return nil }

func (c *queuedClient) SendReply(_ context.Context, message OutboundMessage) error {
	c.webhookReplies <- message
	return nil
}

func (c *queuedClient) SendDirect(_ context.Context, recipientID string, _ string, text string, _ string) (string, error) {
	c.directReplies <- recipientID + ":" + text
	return "durable-receipt", nil
}

func (f *fakeClient) StartStream(ctx context.Context, opts Options, inbound func(context.Context, InboundMessage) error) error {
	f.started = true
	return nil
}

func (f *fakeClient) Stop(context.Context) error { return nil }

func (f *fakeClient) SendReply(ctx context.Context, message OutboundMessage) error {
	f.replies = append(f.replies, message)
	return nil
}

func (f *fakeClient) SendDirect(ctx context.Context, recipientID string, title string, text string, format string) (string, error) {
	f.direct = append(f.direct, recipientID+":"+text)
	return "receipt-1", nil
}

func TestOptionsFromConfigMapsDingTalkFields(t *testing.T) {
	opts := OptionsFromConfig(gwconfig.PlatformAdapterConfig{Extra: map[string]any{"clientId": "cid", "clientSecret": "secret"}})

	if opts.ClientID != "cid" || opts.ClientSecret != "secret" {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestOptionsFromConfigPrefersEncryptedPrimaryFields(t *testing.T) {
	opts := OptionsFromConfig(gwconfig.PlatformAdapterConfig{
		APIKey: "client-id", Token: "client-secret", Extra: map[string]any{"robotCode": "robot-code"},
	})
	if opts.ClientID != "client-id" || opts.ClientSecret != "client-secret" || opts.RobotCode != "robot-code" || opts.AccountID() != "dingtalk:client-id" {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestBuildSubmitInputMapsConversationAndAnswer(t *testing.T) {
	input := BuildSubmitInput(InboundMessage{MessageID: "m1", ConversationID: "c1", SenderID: "u1", Text: "hi"})
	answer := BuildSubmitInput(InboundMessage{MessageID: "m2", ConversationID: "c1", SenderID: "u1", Text: "/answer req-1 yes"})

	if input.ChannelKey != protocol.ChannelDingTalk || input.SessionKey != "dingtalk:c1" || input.Telemetry["dingtalk_message_id"] != "m1" {
		t.Fatalf("input = %+v", input)
	}
	if answer.Telemetry["elicitation_request_id"] != "req-1" || answer.Message != "yes" {
		t.Fatalf("answer = %+v", answer)
	}
}

func TestNormalizeConversationTypeFailsClosedForUnknownValues(t *testing.T) {
	for input, want := range map[string]string{
		"1": "direct", "direct": "direct", "2": "group", "group": "group", "": "unknown", "3": "unknown",
	} {
		if got := normalizeConversationType(input); got != want {
			t.Fatalf("normalizeConversationType(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestRenderReplyDoesNotExposeInternalError(t *testing.T) {
	reply, ok := RenderReply("conversation-1", []protocol.Event{{Type: protocol.EventError, Message: "database password leaked"}})
	if ok || reply.Text != "" {
		t.Fatalf("reply = %+v ok=%v", reply, ok)
	}
}

func TestHandleInboundStartsStreamAndSendsReply(t *testing.T) {
	runner := &fakeRunner{}
	client := &fakeClient{}
	adapter := NewAdapter(gwconfig.PlatformAdapterConfig{APIKey: "client-id"}, client)
	handle, err := adapter.Start(context.Background(), channel.StartDeps{
		Gateway: gwcore.NewGateway(runner), InboundEvents: newDingTalkInboundStore(t),
	})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	dingHandle := handle.(*Handle)
	if !client.started {
		t.Fatal("stream should start")
	}

	if err := dingHandle.HandleInbound(context.Background(), InboundMessage{MessageID: "m1", ConversationID: "c1", SenderID: "u1", Text: "hi"}); err != nil {
		t.Fatalf("HandleInbound failed: %v", err)
	}
	if len(runner.inputs) != 1 || runner.inputs[0].ChannelKey != protocol.ChannelDingTalk {
		t.Fatalf("inputs = %+v", runner.inputs)
	}
	if len(client.direct) != 1 || client.direct[0] != "u1:dingtalk reply" {
		t.Fatalf("direct replies = %+v", client.direct)
	}
	if err := dingHandle.HandleInbound(context.Background(), InboundMessage{MessageID: "m1", ConversationID: "c1", SenderID: "u1", Text: "hi"}); err != nil {
		t.Fatalf("duplicate HandleInbound failed: %v", err)
	}
	if len(runner.inputs) != 1 || len(client.direct) != 1 {
		t.Fatalf("duplicate was processed: inputs=%d replies=%d", len(runner.inputs), len(client.direct))
	}
}

func TestStreamCallbackPersistsSanitizedJobAndRepliesProactively(t *testing.T) {
	database, err := platformdb.Open(context.Background(), platformdb.Config{Driver: platformdb.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open inbound database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	store := gwinbound.NewStore(database)
	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	client := &queuedClient{directReplies: make(chan string, 1), webhookReplies: make(chan OutboundMessage, 1)}
	adapter := NewAdapter(gwconfig.PlatformAdapterConfig{APIKey: "client-id", Token: "client-secret"}, client)
	handle, err := adapter.Start(context.Background(), channel.StartDeps{
		Gateway: gwcore.NewGateway(runner), InboundEvents: store,
	})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = handle.Stop(ctx, "test cleanup")
	})

	const secretWebhook = "https://oapi.dingtalk.com/robot/sendBySession?access_token=secret"
	callbackDone := make(chan error, 1)
	go func() {
		callbackDone <- client.inbound(context.Background(), InboundMessage{
			MessageID: "queued-message", ConversationID: "conversation-1", SenderID: "staff-1",
			SessionWebhook: secretWebhook, Text: "hello", SenderVerified: true,
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

	var payload []byte
	if err := database.QueryRow(context.Background(), `SELECT payload FROM gateway_inbound_jobs WHERE channel_key = ? AND account_id = ? AND event_id = ?`,
		"dingtalk", "dingtalk:client-id", "queued-message").Scan(&payload); err != nil {
		t.Fatalf("load durable payload: %v", err)
	}
	if bytes.Contains(payload, []byte(secretWebhook)) || bytes.Contains(payload, []byte("access_token")) {
		t.Fatalf("durable payload contains session webhook credential: %s", payload)
	}
	select {
	case <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("durable worker did not start the agent turn")
	}
	close(runner.release)
	select {
	case reply := <-client.directReplies:
		if reply != "staff-1:durable dingtalk reply" {
			t.Fatalf("direct reply = %q", reply)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("durable worker did not send a proactive reply")
	}
	select {
	case reply := <-client.webhookReplies:
		t.Fatalf("durable worker used the callback webhook: %+v", reply)
	default:
	}
}

func newDingTalkInboundStore(t *testing.T) *gwinbound.Store {
	t.Helper()
	database, err := platformdb.Open(context.Background(), platformdb.Config{Driver: platformdb.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open inbound database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return gwinbound.NewStore(database)
}
