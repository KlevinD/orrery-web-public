package email

import (
	"context"
	"errors"
	"sync"

	"orrery_v2/internal/gateway/channel"
	gwconfig "orrery_v2/internal/gateway/config"
	"orrery_v2/internal/gateway/protocol"
)

type Client interface {
	SendReply(ctx context.Context, message OutboundEmail) error
}

type Adapter struct {
	config gwconfig.PlatformAdapterConfig
	client Client
}

type Handle struct {
	gateway interface {
		SubmitTurn(context.Context, protocol.SubmitTurnInput) <-chan protocol.Event
	}
	client    Client
	processed map[string]struct{}
	active    map[string]struct{}
	mu        sync.Mutex
}

func NewAdapter(config gwconfig.PlatformAdapterConfig, client Client) *Adapter {
	return &Adapter{config: config, client: client}
}

func (a *Adapter) Key() string {
	return string(protocol.ChannelEmail)
}

func (a *Adapter) Start(ctx context.Context, deps channel.StartDeps) (channel.Handle, error) {
	if deps.Gateway == nil {
		return nil, errors.New("gateway is required")
	}
	return &Handle{gateway: deps.Gateway, client: a.client, processed: map[string]struct{}{}, active: map[string]struct{}{}}, nil
}

func (h *Handle) HandleInbound(ctx context.Context, message InboundEmail) error {
	key := "email:" + firstNonEmpty(message.ThreadID, message.MessageID)
	if !h.markActive(key) {
		return nil
	}
	defer h.clearActive(key)
	if !h.markProcessed(message.MessageID) {
		return nil
	}
	completed := false
	defer func() {
		if !completed {
			h.mu.Lock()
			delete(h.processed, message.MessageID)
			h.mu.Unlock()
		}
	}()
	var events []protocol.Event
	for event := range h.gateway.SubmitTurn(ctx, BuildSubmitInput(message)) {
		events = append(events, event)
	}
	if err := channel.InboundProcessingError(events); err != nil {
		return err
	}
	reply, ok := RenderReply(message, events)
	if ok && h.client != nil {
		if err := h.client.SendReply(ctx, reply); err != nil {
			return err
		}
	}
	completed = true
	return nil
}

func (h *Handle) Stop(ctx context.Context, reason string) error {
	return nil
}

func (h *Handle) markProcessed(id string) bool {
	if id == "" {
		return true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.processed[id]; exists {
		return false
	}
	h.processed[id] = struct{}{}
	return true
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
