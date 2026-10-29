package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/centrifugal/centrifuge"
)

type BrokerKind string

const BrokerKindMemory BrokerKind = "memory"

type BrokerConfig struct {
	Kind BrokerKind
}

type BrokerFactory interface {
	Broker(*centrifuge.Node, BrokerConfig) (centrifuge.Broker, error)
}

type NodeOptions struct {
	Name                   string
	BrokerFactory          BrokerFactory
	SubscriptionAuthorizer SubscriptionAuthorizer
	Config                 centrifuge.Config
	Websocket              centrifuge.WebsocketConfig
}

type MemoryBrokerFactory struct {
}

type Node struct {
	node        *centrifuge.Node
	broker      centrifuge.Broker
	handler     http.Handler
	onPublish   centrifuge.PublishHandler
	onSubscribe SubscriptionAuthorizer
}

var ErrSubscriptionUnauthorized = errors.New("realtime subscription unauthorized")

type SubscriptionRequest struct {
	UserID  string
	Info    []byte
	Channel string
	Data    []byte
}

type SubscriptionAuthorizer interface {
	AuthorizeSubscribe(context.Context, SubscriptionRequest) error
}

type SubscriptionAuthorizerFunc func(context.Context, SubscriptionRequest) error

func (fn SubscriptionAuthorizerFunc) AuthorizeSubscribe(ctx context.Context, req SubscriptionRequest) error {
	if fn == nil {
		return nil
	}
	return fn(ctx, req)
}

func NewNode(opts NodeOptions) (*Node, error) {
	config := opts.Config
	if opts.Name != "" {
		config.Name = opts.Name
	}
	inner, err := centrifuge.New(config)
	if err != nil {
		return nil, fmt.Errorf("create centrifuge node: %w", err)
	}

	factory := opts.BrokerFactory
	if factory == nil {
		factory = MemoryBrokerFactory{}
	}
	broker, err := factory.Broker(inner, BrokerConfig{Kind: BrokerKindMemory})
	if err != nil {
		return nil, fmt.Errorf("create realtime broker: %w", err)
	}
	if broker == nil {
		return nil, fmt.Errorf("realtime broker factory returned nil broker")
	}
	inner.SetBroker(broker)

	n := &Node{
		node:        inner,
		broker:      broker,
		onPublish:   rejectClientPublish,
		onSubscribe: opts.SubscriptionAuthorizer,
	}
	n.node.OnConnect(func(client *centrifuge.Client) {
		client.OnPublish(n.onPublish)
		client.OnSubscribe(n.ClientSubscribeHandler(client.Context(), client.UserID(), client.Info()))
	})
	n.handler = centrifuge.NewWebsocketHandler(inner, opts.Websocket)
	return n, nil
}

func (f MemoryBrokerFactory) Broker(node *centrifuge.Node, config BrokerConfig) (centrifuge.Broker, error) {
	if node == nil {
		return nil, fmt.Errorf("centrifuge node is required for memory broker")
	}
	if config.Kind != "" && config.Kind != BrokerKindMemory {
		return nil, fmt.Errorf("unsupported realtime broker kind %q", config.Kind)
	}
	return centrifuge.NewMemoryBroker(node, centrifuge.MemoryBrokerConfig{})
}

func (n *Node) Run(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := n.node.Run(); err != nil {
		return fmt.Errorf("run centrifuge node: %w", err)
	}
	go func() {
		<-ctx.Done()
		_ = n.node.Shutdown(context.Background())
	}()
	return nil
}

func (n *Node) Shutdown(ctx context.Context) error {
	if n == nil || n.node == nil {
		return nil
	}
	return n.node.Shutdown(ctx)
}

func (n *Node) Publish(ctx context.Context, env Envelope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := env.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal realtime publish envelope: %w", err)
	}
	if _, err := n.node.Publish(env.Channel, payload, centrifuge.WithIdempotencyKey(env.EventID)); err != nil {
		return fmt.Errorf("publish realtime envelope: %w", err)
	}
	return nil
}

func (n *Node) Handler() http.Handler {
	return n.handler
}

func (n *Node) ClientPublishHandler() centrifuge.PublishHandler {
	return n.onPublish
}

func (n *Node) ClientSubscribeHandler(ctx context.Context, userID string, info []byte) centrifuge.SubscribeHandler {
	if ctx == nil {
		ctx = context.Background()
	}
	return func(event centrifuge.SubscribeEvent, cb centrifuge.SubscribeCallback) {
		if n != nil && n.onSubscribe != nil {
			err := n.onSubscribe.AuthorizeSubscribe(ctx, SubscriptionRequest{
				UserID:  userID,
				Info:    append([]byte(nil), info...),
				Channel: event.Channel,
				Data:    append([]byte(nil), event.Data...),
			})
			if err != nil {
				cb(centrifuge.SubscribeReply{}, centrifuge.ErrorPermissionDenied)
				return
			}
		}
		cb(centrifuge.SubscribeReply{}, nil)
	}
}

func rejectClientPublish(_ centrifuge.PublishEvent, cb centrifuge.PublishCallback) {
	cb(centrifuge.PublishReply{}, centrifuge.ErrorPermissionDenied)
}
