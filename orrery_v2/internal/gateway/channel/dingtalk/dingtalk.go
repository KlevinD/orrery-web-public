package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/gateway/channel"
	gwconfig "orrery_v2/internal/gateway/config"
	"orrery_v2/internal/gateway/protocol"
)

type Client interface {
	SendReply(ctx context.Context, message OutboundMessage) error
}

type LifecycleClient interface {
	Client
	StartStream(ctx context.Context, opts Options, inbound func(context.Context, InboundMessage) error) error
	Stop(ctx context.Context) error
}

type DirectClient interface {
	SendDirect(ctx context.Context, recipientID string, title string, text string, format string) (string, error)
}

type LoggerClient interface {
	SetLogger(channel.Logger)
}

type Adapter struct {
	config  gwconfig.PlatformAdapterConfig
	client  Client
	options Options
}

type Handle struct {
	gateway interface {
		SubmitTurn(context.Context, protocol.SubmitTurnInput) <-chan protocol.Event
	}
	client  Client
	options Options
	inbound channel.InboundEventStore
	queue   channel.InboundJobQueue
	logger  channel.Logger
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	active  map[string]struct{}
	mu      sync.Mutex
}

func NewAdapter(config gwconfig.PlatformAdapterConfig, client Client) *Adapter {
	return &Adapter{config: config, client: client}
}

func (a *Adapter) Key() string {
	return string(protocol.ChannelDingTalk)
}

func (a *Adapter) Start(ctx context.Context, deps channel.StartDeps) (channel.Handle, error) {
	if deps.Gateway == nil {
		return nil, errors.New("gateway is required")
	}
	opts := OptionsFromConfig(a.config)
	client, ok := a.client.(LifecycleClient)
	if !ok || client == nil {
		return nil, errors.New("dingtalk production lifecycle client is required")
	}
	if deps.InboundEvents == nil {
		return nil, errors.New("dingtalk durable inbound event store is required")
	}
	if _, ok := a.client.(DirectClient); !ok {
		return nil, errors.New("dingtalk durable inbound delivery client is required")
	}
	queue, ok := deps.InboundEvents.(channel.InboundJobQueue)
	if !ok || queue == nil {
		return nil, errors.New("dingtalk durable inbound job queue is required")
	}
	runCtx, cancel := context.WithCancel(ctx)
	handle := &Handle{
		gateway: deps.Gateway, client: a.client, options: opts, inbound: deps.InboundEvents,
		queue: queue, logger: deps.Logger, cancel: cancel, active: map[string]struct{}{},
	}
	if client, ok := a.client.(LoggerClient); ok {
		client.SetLogger(deps.Logger)
	}
	if err := client.StartStream(runCtx, opts, handle.EnqueueInbound); err != nil {
		cancel()
		return nil, err
	}
	handle.wg.Add(1)
	go func() {
		defer handle.wg.Done()
		channel.RunInboundJobWorker(runCtx, queue, string(protocol.ChannelDingTalk), opts.AccountID(), handle.processQueuedInbound, deps.Logger)
	}()
	return handle, nil
}

func (h *Handle) HandleInbound(ctx context.Context, message InboundMessage) (handleErr error) {
	accountID := firstNonBlank(strings.TrimSpace(message.AccountID), h.options.AccountID())
	claim, err := h.inbound.ClaimInboundEvent(ctx, string(protocol.ChannelDingTalk), accountID, strings.TrimSpace(message.MessageID))
	if err != nil {
		return err
	}
	if claim.State == channel.InboundEventCompleted {
		return nil
	}
	if claim.State != channel.InboundEventAcquired {
		return errors.New("dingtalk event is already being processed")
	}
	processingCtx, stopRenewal := channel.MaintainInboundEventClaim(ctx, h.inbound, claim)
	completed := false
	defer func() {
		handleErr = errors.Join(handleErr, stopRenewal())
		if completed {
			return
		}
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if releaseErr := h.inbound.ReleaseInboundEvent(releaseCtx, claim); releaseErr != nil {
			handleErr = errors.Join(handleErr, releaseErr)
		}
	}()
	if err := h.processInbound(processingCtx, message); err != nil {
		return err
	}
	if err := stopRenewal(); err != nil {
		return fmt.Errorf("maintain dingtalk inbound event claim: %w", err)
	}
	completeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = h.inbound.CompleteInboundEvent(completeCtx, claim)
	cancel()
	if err != nil {
		return err
	}
	completed = true
	return nil
}

func (h *Handle) EnqueueInbound(ctx context.Context, message InboundMessage) error {
	message.AccountID = firstNonBlank(strings.TrimSpace(message.AccountID), h.options.AccountID())
	message.MessageID = strings.TrimSpace(message.MessageID)
	if message.AccountID == "" || message.MessageID == "" {
		return errors.New("dingtalk inbound account id and message id are required")
	}
	// Session webhooks are bearer credentials. Durable workers use the verified
	// staff ID and the app's proactive-send API instead of persisting the URL.
	message.SessionWebhook = ""
	payload, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("encode dingtalk inbound message: %w", err)
	}
	_, err = h.queue.EnqueueInboundJob(ctx, channel.InboundJob{
		ChannelKey: string(protocol.ChannelDingTalk), AccountID: message.AccountID, EventID: message.MessageID, Payload: payload,
	})
	return err
}

func (h *Handle) processQueuedInbound(ctx context.Context, job channel.InboundJob) error {
	var message InboundMessage
	if err := json.Unmarshal(job.Payload, &message); err != nil {
		return errors.New("decode durable dingtalk inbound message")
	}
	if message.AccountID != job.AccountID || message.MessageID != job.EventID || strings.TrimSpace(message.SessionWebhook) != "" {
		return errors.New("durable dingtalk inbound message identity mismatch")
	}
	return h.processInbound(ctx, message)
}

func (h *Handle) processInbound(ctx context.Context, message InboundMessage) error {
	key := "dingtalk:" + message.ConversationID
	if !h.markActive(key) {
		return errors.New("dingtalk conversation already has a message in progress")
	}
	defer h.clearActive(key)
	var events []protocol.Event
	for event := range h.gateway.SubmitTurn(ctx, BuildSubmitInput(message)) {
		events = append(events, event)
	}
	if err := channel.InboundProcessingError(events); err != nil {
		return err
	}
	reply, ok := RenderReply(message.ConversationID, events)
	if ok && h.client != nil {
		if strings.TrimSpace(message.SessionWebhook) != "" {
			reply.SessionWebhook = message.SessionWebhook
			if err := h.client.SendReply(ctx, reply); err != nil {
				return err
			}
		} else {
			direct, ok := h.client.(DirectClient)
			if !ok {
				return errors.New("dingtalk durable inbound delivery client is unavailable")
			}
			receipt, err := direct.SendDirect(ctx, message.SenderID, "Orrery", reply.Text, "markdown")
			if err != nil {
				return err
			}
			if strings.TrimSpace(receipt) == "" {
				return errors.New("dingtalk durable inbound reply returned no receipt")
			}
		}
	}
	return nil
}

func (h *Handle) Stop(ctx context.Context, reason string) error {
	if h.cancel != nil {
		h.cancel()
	}
	var stopErr error
	if client, ok := h.client.(LifecycleClient); ok {
		stopErr = client.Stop(ctx)
	}
	done := make(chan struct{})
	go func() {
		h.wg.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return errors.Join(stopErr, ctx.Err())
	case <-done:
		return stopErr
	}
}

func (h *Handle) Send(ctx context.Context, delivery channel.Delivery) (channel.DeliveryResult, error) {
	if delivery.AccountID != "" && delivery.AccountID != h.options.AccountID() {
		return channel.DeliveryResult{}, channel.PermanentDeliveryError(errors.New("dingtalk delivery account does not match active client"))
	}
	if strings.TrimSpace(delivery.RecipientID) == "" {
		return channel.DeliveryResult{}, channel.PermanentDeliveryError(errors.New("dingtalk delivery recipient is required"))
	}
	client, ok := h.client.(DirectClient)
	if !ok {
		return channel.DeliveryResult{}, errors.New("dingtalk outbound client is unavailable")
	}
	messageID, err := client.SendDirect(ctx, delivery.RecipientID, delivery.Title, delivery.Text, delivery.Format)
	if err != nil {
		return channel.DeliveryResult{}, err
	}
	return channel.DeliveryResult{ExternalMessageID: messageID}, nil
}

func (h *Handle) GatewayConnectionHealthy() bool {
	provider, ok := h.client.(channel.ConnectionHealthProvider)
	return ok && provider.GatewayConnectionHealthy()
}

func (h *Handle) markActive(key string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.active[key]; exists {
		return false
	}
	h.active[key] = struct{}{}
	return true
}

func (h *Handle) clearActive(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.active, key)
}
