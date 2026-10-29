package feishu

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
	SendReply(ctx context.Context, reply Reply) error
}

type LifecycleClient interface {
	Client
	Start(ctx context.Context, options Options, inbound func(context.Context, InboundEvent) error) error
	Stop(ctx context.Context) error
}

type DirectClient interface {
	SendDirect(ctx context.Context, recipientID string, title string, text string, format string, idempotencyKey string) (string, error)
}

type LoggerClient interface {
	SetLogger(channel.Logger)
}

type Adapter struct {
	config   gwconfig.FeishuAdapterConfig
	client   Client
	options  Options
	meetings MeetingAutomation
}

type Handle struct {
	gateway interface {
		SubmitTurn(context.Context, protocol.SubmitTurnInput) <-chan protocol.Event
	}
	client   Client
	options  Options
	inbound  channel.InboundEventStore
	queue    channel.InboundJobQueue
	logger   channel.Logger
	meetings MeetingAutomation
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	active   map[string]struct{}
	mu       sync.Mutex
}

func NewAdapter(config gwconfig.FeishuAdapterConfig, client Client) *Adapter {
	return &Adapter{config: config, client: client}
}

func NewAccountAdapter(accountID string, config gwconfig.FeishuAccountConfig, client Client) *Adapter {
	return &Adapter{client: client, options: OptionsFromAccountConfig(accountID, config)}
}

func NewAdapterWithMeetingAutomation(config gwconfig.FeishuAdapterConfig, client Client, meetings MeetingAutomation) *Adapter {
	return &Adapter{config: config, client: client, meetings: meetings}
}

func (a *Adapter) Key() string {
	return string(protocol.ChannelFeishu)
}

func (a *Adapter) Start(ctx context.Context, deps channel.StartDeps) (channel.Handle, error) {
	if deps.Gateway == nil {
		return nil, errors.New("gateway is required")
	}
	options := a.options
	if strings.TrimSpace(options.AppID) == "" {
		options = OptionsFromConfig(a.config)
	}
	options.MeetingEventsEnabled = a.meetings != nil
	mode := strings.ToLower(strings.TrimSpace(options.ConnectionMode))
	if mode != ModeStream && mode != ModeOutbound {
		return nil, fmt.Errorf("feishu connection mode %q is not supported by the production gateway", options.ConnectionMode)
	}
	if !validFeishuDomain(options.DomainName) {
		return nil, fmt.Errorf("feishu domain %q is not supported", options.DomainName)
	}
	client, ok := a.client.(LifecycleClient)
	if !ok || client == nil {
		return nil, errors.New("feishu production lifecycle client is required")
	}
	if mode == ModeStream && deps.InboundEvents == nil {
		return nil, errors.New("feishu durable inbound event store is required")
	}
	var queue channel.InboundJobQueue
	if mode == ModeStream {
		var ok bool
		queue, ok = deps.InboundEvents.(channel.InboundJobQueue)
		if !ok || queue == nil {
			return nil, errors.New("feishu durable inbound job queue is required")
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	handle := &Handle{
		gateway: deps.Gateway, client: a.client, options: options, inbound: deps.InboundEvents,
		queue: queue, logger: deps.Logger, meetings: a.meetings, cancel: cancel, active: map[string]struct{}{},
	}
	if client, ok := a.client.(LoggerClient); ok {
		client.SetLogger(deps.Logger)
	}
	if err := client.Start(runCtx, options, handle.EnqueueEvent); err != nil {
		cancel()
		return nil, err
	}
	if mode == ModeStream {
		handle.wg.Add(1)
		go func() {
			defer handle.wg.Done()
			channel.RunInboundJobWorker(runCtx, queue, string(protocol.ChannelFeishu), options.AccountID(), handle.processQueuedEvent, deps.Logger)
		}()
	}
	return handle, nil
}

func validFeishuDomain(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "feishu", "lark", "larksuite":
		return true
	default:
		return false
	}
}

func (h *Handle) HandleEvent(ctx context.Context, event InboundEvent) (handleErr error) {
	eventID := firstNonEmpty(strings.TrimSpace(event.EventID), strings.TrimSpace(event.MessageID))
	accountID := firstNonEmpty(strings.TrimSpace(event.AccountID), h.options.AccountID())
	claim, err := h.inbound.ClaimInboundEvent(ctx, h.optionsChannelKey(), accountID, eventID)
	if err != nil {
		return err
	}
	if claim.State == channel.InboundEventCompleted {
		return nil
	}
	if claim.State != channel.InboundEventAcquired {
		return errors.New("feishu event is already being processed")
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
	if err := h.processEvent(processingCtx, event); err != nil {
		return err
	}
	if err := stopRenewal(); err != nil {
		return fmt.Errorf("maintain feishu inbound event claim: %w", err)
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

func (h *Handle) EnqueueEvent(ctx context.Context, event InboundEvent) error {
	event.EventID = firstNonEmpty(strings.TrimSpace(event.EventID), strings.TrimSpace(event.MessageID))
	event.AccountID = firstNonEmpty(strings.TrimSpace(event.AccountID), h.options.AccountID())
	if event.EventID == "" || event.AccountID == "" {
		return errors.New("feishu inbound account id and event id are required")
	}
	if err := validateMeetingInboundIdentity(event); err != nil {
		return err
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode feishu inbound event: %w", err)
	}
	_, err = h.queue.EnqueueInboundJob(ctx, channel.InboundJob{
		ChannelKey: string(protocol.ChannelFeishu), AccountID: event.AccountID, EventID: event.EventID, Payload: payload,
	})
	return err
}

func (h *Handle) processQueuedEvent(ctx context.Context, job channel.InboundJob) error {
	var event InboundEvent
	if err := json.Unmarshal(job.Payload, &event); err != nil {
		return errors.New("decode durable feishu inbound event")
	}
	if event.AccountID != job.AccountID || firstNonEmpty(event.EventID, event.MessageID) != job.EventID {
		return errors.New("durable feishu inbound event identity mismatch")
	}
	return h.processEvent(ctx, event)
}

func (h *Handle) processEvent(ctx context.Context, event InboundEvent) error {
	if event.Kind == InboundEventMeeting {
		if h.meetings == nil || event.Meeting == nil {
			return errors.New("feishu meeting event handler is unavailable")
		}
		if err := validateMeetingInboundIdentity(event); err != nil {
			return err
		}
		_, _, err := h.meetings.HandleProviderEvent(ctx, *event.Meeting)
		return err
	}
	eventID := firstNonEmpty(strings.TrimSpace(event.EventID), strings.TrimSpace(event.MessageID))
	accountID := firstNonEmpty(strings.TrimSpace(event.AccountID), h.options.AccountID())
	activeKey := "feishu:" + strings.TrimSpace(event.ChatID)
	if !h.markActive(activeKey) {
		return errors.New("feishu conversation already has a message in progress")
	}
	defer h.clearActive(activeKey)
	var events []protocol.Event
	for gatewayEvent := range h.gateway.SubmitTurn(ctx, BuildSubmitInput(event)) {
		events = append(events, gatewayEvent)
	}
	if err := channel.InboundProcessingError(events); err != nil {
		return err
	}
	reply, ok := RenderReply(event.ChatID, events)
	if ok && h.client != nil {
		reply.ReplyMessageID = event.MessageID
		reply.IdempotencyKey = channel.InboundRunID(h.optionsChannelKey(), accountID, eventID)
		if err := h.client.SendReply(ctx, reply); err != nil {
			return err
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
	if strings.TrimSpace(delivery.AccountID) != "" && delivery.AccountID != h.options.AccountID() {
		return channel.DeliveryResult{}, channel.PermanentDeliveryError(errors.New("feishu delivery account does not match active client"))
	}
	if strings.TrimSpace(delivery.RecipientID) == "" {
		return channel.DeliveryResult{}, channel.PermanentDeliveryError(errors.New("feishu delivery recipient is required"))
	}
	client, ok := h.client.(DirectClient)
	if !ok {
		return channel.DeliveryResult{}, errors.New("feishu outbound client is unavailable")
	}
	messageID, err := client.SendDirect(
		ctx, delivery.RecipientID, delivery.Title, delivery.Text, delivery.Format,
		firstNonEmpty(strings.TrimSpace(delivery.ID), strings.TrimSpace(delivery.IdempotencyKey)),
	)
	if err != nil {
		return channel.DeliveryResult{}, err
	}
	return channel.DeliveryResult{ExternalMessageID: messageID}, nil
}

func (h *Handle) GatewayConnectionHealthy() bool {
	provider, ok := h.client.(channel.ConnectionHealthProvider)
	return ok && provider.GatewayConnectionHealthy()
}

func (h *Handle) optionsChannelKey() string {
	return string(protocol.ChannelFeishu)
}

func (h *Handle) markActive(eventID string) bool {
	if eventID == "" {
		return true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.active[eventID]; exists {
		return false
	}
	h.active[eventID] = struct{}{}
	return true
}

func (h *Handle) clearActive(eventID string) {
	if eventID == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.active, eventID)
}
