package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
	"time"

	"github.com/centrifugal/centrifuge"
)

func TestNodeUsesMemoryBrokerWhenFactoryOmitted(t *testing.T) {
	node, err := NewNode(NodeOptions{
		Name: "test-memory-broker",
	})
	if err != nil {
		t.Fatalf("NewNode returned error: %v", err)
	}
	defer shutdownRealtimeNode(t, node)

	if node.Handler() == nil {
		t.Fatalf("Handler returned nil")
	}
}

func TestMemoryBrokerFactoryCreatesMemoryBroker(t *testing.T) {
	node, err := centrifuge.New(centrifuge.Config{Name: "test-memory-broker-factory"})
	if err != nil {
		t.Fatalf("centrifuge.New returned error: %v", err)
	}

	broker, err := MemoryBrokerFactory{}.Broker(node, BrokerConfig{Kind: BrokerKindMemory})
	if err != nil {
		t.Fatalf("Broker returned error: %v", err)
	}
	if _, ok := broker.(*centrifuge.MemoryBroker); !ok {
		t.Fatalf("Broker type = %T, want *centrifuge.MemoryBroker", broker)
	}
}

func TestNodeDoesNotExposeRawCentrifugePublishBypass(t *testing.T) {
	fileset := token.NewFileSet()
	file, err := parser.ParseFile(fileset, "node.go", nil, 0)
	if err != nil {
		t.Fatalf("parse node.go: %v", err)
	}

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || fn.Name == nil || !fn.Name.IsExported() {
			continue
		}
		if !isNodeReceiver(fn.Recv.List[0].Type) {
			continue
		}
		switch fn.Name.Name {
		case "Broker", "Centrifuge":
			t.Fatalf("Node exposes %s, which can bypass durable realtime outbox publishing", fn.Name.Name)
		}
	}
}

func isNodeReceiver(expr ast.Expr) bool {
	switch typ := expr.(type) {
	case *ast.StarExpr:
		ident, ok := typ.X.(*ast.Ident)
		return ok && ident.Name == "Node"
	case *ast.Ident:
		return typ.Name == "Node"
	default:
		return false
	}
}

func TestNodeAcceptsCustomBrokerFactoryWithoutRedisDependency(t *testing.T) {
	factory := &recordingBrokerFactory{}
	node, err := NewNode(NodeOptions{
		Name:          "test-custom-broker",
		BrokerFactory: factory,
	})
	if err != nil {
		t.Fatalf("NewNode returned error: %v", err)
	}
	defer shutdownRealtimeNode(t, node)

	if factory.calls != 1 {
		t.Fatalf("factory calls = %d, want 1", factory.calls)
	}
	if factory.node == nil {
		t.Fatalf("factory did not receive centrifuge node")
	}
	if factory.config.Kind != BrokerKindMemory {
		t.Fatalf("factory kind = %q, want %q", factory.config.Kind, BrokerKindMemory)
	}
}

func TestNodeRejectsClientSidePublish(t *testing.T) {
	node, err := NewNode(NodeOptions{
		Name: "test-publish-reject",
	})
	if err != nil {
		t.Fatalf("NewNode returned error: %v", err)
	}
	defer shutdownRealtimeNode(t, node)

	var callbackCalled bool
	node.ClientPublishHandler()(centrifuge.PublishEvent{Channel: "conversation:conv-1"}, func(reply centrifuge.PublishReply, err error) {
		callbackCalled = true
		if !errors.Is(err, centrifuge.ErrorPermissionDenied) {
			t.Fatalf("publish error = %v, want permission denied", err)
		}
	})

	if !callbackCalled {
		t.Fatalf("publish callback was not called")
	}
}

func TestNodeRejectsUnauthorizedSubscriptions(t *testing.T) {
	authorizer := recordingSubscriptionAuthorizer{err: ErrSubscriptionUnauthorized}
	node, err := NewNode(NodeOptions{
		Name:                   "test-subscribe-reject",
		SubscriptionAuthorizer: &authorizer,
	})
	if err != nil {
		t.Fatalf("NewNode returned error: %v", err)
	}
	defer shutdownRealtimeNode(t, node)

	var callbackCalled bool
	node.ClientSubscribeHandler(context.Background(), "viewer-1", []byte(`{"viewer_id":"viewer-1"}`))(centrifuge.SubscribeEvent{Channel: "conversation:conv-1"}, func(reply centrifuge.SubscribeReply, err error) {
		callbackCalled = true
		if !errors.Is(err, centrifuge.ErrorPermissionDenied) {
			t.Fatalf("subscribe error = %v, want permission denied", err)
		}
	})

	if !callbackCalled {
		t.Fatalf("subscribe callback was not called")
	}
	if authorizer.request.UserID != "viewer-1" || authorizer.request.Channel != "conversation:conv-1" {
		t.Fatalf("authorizer request = %+v", authorizer.request)
	}
}

func TestNodeSubscriptionAuthorizationUsesConnectionContext(t *testing.T) {
	authorizer := &recordingSubscriptionAuthorizer{}
	node, err := NewNode(NodeOptions{
		Name:                   "test-subscribe-context",
		SubscriptionAuthorizer: authorizer,
	})
	if err != nil {
		t.Fatalf("NewNode returned error: %v", err)
	}
	defer shutdownRealtimeNode(t, node)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	node.ClientSubscribeHandler(ctx, "viewer-1", nil)(centrifuge.SubscribeEvent{Channel: "user:viewer-1"}, func(reply centrifuge.SubscribeReply, err error) {})

	if authorizer.ctxErr == nil {
		t.Fatalf("authorizer did not receive canceled connection context")
	}
}

func TestNodePublishUsesEnvelopeEventIDAsIdempotencyKey(t *testing.T) {
	broker := &capturingBroker{}
	node, err := NewNode(NodeOptions{
		Name:          "test-publish-idempotency",
		BrokerFactory: fixedBrokerFactory{broker: broker},
	})
	if err != nil {
		t.Fatalf("NewNode returned error: %v", err)
	}
	defer shutdownRealtimeNode(t, node)

	occurredAt := time.Date(2026, 6, 10, 16, 30, 0, 0, time.UTC)
	envelope := Envelope{
		EventID:       "evt-idempotent",
		Type:          EventDiscussionMessageCreated,
		Channel:       "conversation:conv-1",
		AggregateType: AggregateConversation,
		AggregateID:   "conv-1",
		Version:       7,
		OccurredAt:    occurredAt,
		Payload:       json.RawMessage(`{"message_id":"msg-1"}`),
	}

	if err := node.Publish(context.Background(), envelope); err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}
	if broker.channel != envelope.Channel {
		t.Fatalf("broker channel = %q, want %q", broker.channel, envelope.Channel)
	}
	if broker.options.IdempotencyKey != envelope.EventID {
		t.Fatalf("idempotency key = %q, want %q", broker.options.IdempotencyKey, envelope.EventID)
	}
	var published Envelope
	if err := json.Unmarshal(broker.data, &published); err != nil {
		t.Fatalf("unmarshal published envelope: %v", err)
	}
	if published.EventID != envelope.EventID || published.Version != envelope.Version {
		t.Fatalf("published envelope = %+v, want event_id/version preserved", published)
	}
}

type recordingBrokerFactory struct {
	calls  int
	config BrokerConfig
	node   *centrifuge.Node
	broker centrifuge.Broker
}

func (f *recordingBrokerFactory) Broker(node *centrifuge.Node, config BrokerConfig) (centrifuge.Broker, error) {
	f.calls++
	f.config = config
	f.node = node
	broker, err := centrifuge.NewMemoryBroker(node, centrifuge.MemoryBrokerConfig{})
	if err != nil {
		return nil, err
	}
	f.broker = broker
	return broker, nil
}

type fixedBrokerFactory struct {
	broker centrifuge.Broker
}

func (f fixedBrokerFactory) Broker(_ *centrifuge.Node, _ BrokerConfig) (centrifuge.Broker, error) {
	return f.broker, nil
}

type recordingSubscriptionAuthorizer struct {
	request SubscriptionRequest
	ctxErr  error
	err     error
}

func (a *recordingSubscriptionAuthorizer) AuthorizeSubscribe(ctx context.Context, request SubscriptionRequest) error {
	a.request = request
	a.ctxErr = ctx.Err()
	return a.err
}

type capturingBroker struct {
	handler centrifuge.BrokerEventHandler
	channel string
	data    []byte
	options centrifuge.PublishOptions
}

func (b *capturingBroker) RegisterBrokerEventHandler(handler centrifuge.BrokerEventHandler) error {
	b.handler = handler
	return nil
}

func (b *capturingBroker) Subscribe(string) error {
	return nil
}

func (b *capturingBroker) Unsubscribe(string) error {
	return nil
}

func (b *capturingBroker) Publish(ch string, data []byte, opts centrifuge.PublishOptions) (centrifuge.StreamPosition, bool, error) {
	b.channel = ch
	b.data = append([]byte(nil), data...)
	b.options = opts
	return centrifuge.StreamPosition{Offset: 1, Epoch: "test"}, false, nil
}

func (b *capturingBroker) PublishJoin(string, *centrifuge.ClientInfo) error {
	return nil
}

func (b *capturingBroker) PublishLeave(string, *centrifuge.ClientInfo) error {
	return nil
}

func (b *capturingBroker) History(string, centrifuge.HistoryOptions) ([]*centrifuge.Publication, centrifuge.StreamPosition, error) {
	return nil, centrifuge.StreamPosition{}, nil
}

func (b *capturingBroker) RemoveHistory(string) error {
	return nil
}

func shutdownRealtimeNode(t *testing.T, node *Node) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := node.Shutdown(ctx); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Shutdown returned error: %v", err)
	}
}
