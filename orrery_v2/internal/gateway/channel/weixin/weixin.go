package weixin

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/gateway/channel"
	gwconfig "orrery_v2/internal/gateway/config"
	"orrery_v2/internal/gateway/protocol"
)

type InboundProcessor func(context.Context, InboundMessage) ([]OutboundMessage, error)

type Client interface {
	Start(ctx context.Context, opts Options, logger Logger, inbound InboundProcessor) error
	SendReply(ctx context.Context, message OutboundMessage) error
	Stop(ctx context.Context) error
}

type ReceiptClient interface {
	SendWithReceipt(ctx context.Context, message OutboundMessage) (string, error)
}

type Logger interface {
	Info(message string, metadata map[string]any)
	Warn(message string, metadata map[string]any)
	Error(message string, metadata map[string]any)
}

type Adapter struct {
	config gwconfig.WeixinAdapterConfig
	client Client
}

type Handle struct {
	gateway interface {
		SubmitTurn(context.Context, protocol.SubmitTurnInput) <-chan protocol.Event
	}
	client  Client
	options Options
	logger  Logger
	cancel  context.CancelFunc
	inbound channel.InboundEventStore
	active  map[string]struct{}
	mu      sync.Mutex
}

func NewAdapter(config gwconfig.WeixinAdapterConfig, client Client) *Adapter {
	return &Adapter{config: config, client: client}
}

func NewClient() Client {
	return NewIlinkBotMultiClient()
}

func (a *Adapter) Key() string {
	return string(protocol.ChannelWeixin)
}

func (a *Adapter) Start(ctx context.Context, deps channel.StartDeps) (channel.Handle, error) {
	if deps.Gateway == nil {
		return nil, errors.New("gateway is required")
	}
	if _, err := ValidateConfiguredAccounts(a.config); err != nil {
		return nil, err
	}
	if deps.InboundEvents == nil {
		return nil, errors.New("weixin durable inbound event store is required")
	}
	startCtx, cancel := context.WithCancel(ctx)
	handle := &Handle{
		gateway: deps.Gateway,
		client:  a.client,
		options: OptionsFromConfig(a.config),
		logger:  deps.Logger,
		cancel:  cancel,
		inbound: deps.InboundEvents,
		active:  map[string]struct{}{},
	}
	handle.options.StatePath = firstNonBlank(handle.options.StatePath, defaultWeixinStatePath(deps.StateDir))
	handle.options.InboundEvents = deps.InboundEvents
	if len(handle.options.Accounts) == 0 {
		cancel()
		return nil, errors.New("weixin channel is enabled but has no verified account bindings")
	}
	if a.client == nil {
		cancel()
		return nil, errors.New("weixin production client is required")
	}
	if err := a.client.Start(startCtx, handle.options, handle.logger, handle.ProcessInbound); err != nil {
		cancel()
		return nil, err
	}
	return handle, nil
}

func (h *Handle) ProcessInbound(ctx context.Context, message InboundMessage) ([]OutboundMessage, error) {
	message.ChatID = strings.TrimSpace(message.ChatID)
	if message.ChatID == "" {
		return nil, errors.New("weixin chat id is required")
	}
	message.Text = strings.TrimSpace(message.Text)
	if message.Text == "" && len(protocolAttachments(message.Attachments)) == 0 && message.InputModality != "voice" {
		return nil, errors.New("weixin message text or image is required")
	}
	if !h.markActive(message.ChatID) {
		return nil, errors.New("weixin chat already has a message in progress")
	}
	defer h.clearActive(message.ChatID)
	input := BuildSubmitInput(message)
	var events []protocol.Event
	permissionSequence := 0
	for event := range h.gateway.SubmitTurn(ctx, input) {
		if event.Type == protocol.EventPermissionRequest {
			if err := h.sendPermissionRequest(ctx, message, input.RunID, event, permissionSequence); err != nil {
				return nil, err
			}
			permissionSequence++
			continue
		}
		events = append(events, event)
	}
	if err := channel.InboundProcessingError(events); err != nil {
		return nil, err
	}
	replies := RenderReplies(message.ChatID, events, h.options)
	for i := range replies {
		replies[i].AccountID = message.AccountID
		replies[i].ContextToken = message.ContextToken
		replies[i].ClientID = weixinMessageClientID(
			channel.InboundRunID(string(protocol.ChannelWeixin), message.AccountID, message.MessageID),
			i,
		)
	}
	return replies, nil
}

func (h *Handle) sendPermissionRequest(ctx context.Context, message InboundMessage, runID string, event protocol.Event, sequence int) error {
	if h == nil || h.client == nil {
		return errors.New("weixin outbound client is unavailable")
	}
	replies := RenderReplies(message.ChatID, []protocol.Event{event}, h.options)
	source := fmt.Sprintf("%s:permission:%s:%d", strings.TrimSpace(runID), firstNonBlank(event.RequestID, event.RunID), sequence)
	for index := range replies {
		replies[index].AccountID = message.AccountID
		replies[index].ContextToken = message.ContextToken
		replies[index].ClientID = weixinMessageClientID(source, index)
		if err := h.client.SendReply(ctx, replies[index]); err != nil {
			return fmt.Errorf("send Weixin approval prompt: %w", err)
		}
	}
	return nil
}

func (h *Handle) HandleInbound(ctx context.Context, message InboundMessage) (handleErr error) {
	messageID := strings.TrimSpace(message.MessageID)
	accountID := strings.TrimSpace(message.AccountID)
	if messageID == "" || accountID == "" {
		return errors.New("weixin inbound account id and message id are required")
	}
	claim, err := h.inbound.ClaimInboundEvent(ctx, string(protocol.ChannelWeixin), accountID, messageID)
	if err != nil {
		return err
	}
	if claim.State == channel.InboundEventCompleted {
		return nil
	}
	if claim.State != channel.InboundEventAcquired {
		return errors.New("weixin inbound message is already being processed")
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
	replies, err := h.ProcessInbound(processingCtx, message)
	if err != nil {
		return err
	}
	for _, reply := range replies {
		if h.client != nil {
			if err := h.client.SendReply(processingCtx, reply); err != nil {
				return fmt.Errorf("send weixin reply: %w", err)
			}
		}
	}
	if err := stopRenewal(); err != nil {
		return fmt.Errorf("maintain weixin inbound event claim: %w", err)
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

func (h *Handle) Stop(ctx context.Context, reason string) error {
	if h.cancel != nil {
		h.cancel()
	}
	if h.client != nil {
		return h.client.Stop(ctx)
	}
	return nil
}

func (h *Handle) Send(ctx context.Context, delivery channel.Delivery) (channel.DeliveryResult, error) {
	if h.client == nil {
		return channel.DeliveryResult{}, errors.New("weixin outbound client is unavailable")
	}
	accountID := strings.TrimSpace(delivery.AccountID)
	recipientID := strings.TrimSpace(delivery.RecipientID)
	if accountID == "" || recipientID == "" {
		return channel.DeliveryResult{}, channel.PermanentDeliveryError(errors.New("weixin outbound account and recipient are required"))
	}
	contextToken, err := h.proactiveContextToken(ctx, accountID, recipientID)
	if err != nil {
		return channel.DeliveryResult{}, err
	}
	replies := RenderReplies(recipientID, []protocol.Event{{Type: protocol.EventAssistantTextDelta, Text: delivery.Text}}, h.options)
	receiptClient, ok := h.client.(ReceiptClient)
	if !ok {
		return channel.DeliveryResult{}, errors.New("weixin outbound receipt client is unavailable")
	}
	receipts := make([]string, 0, len(replies))
	for index, reply := range replies {
		reply.AccountID = accountID
		reply.ContextToken = contextToken
		reply.ClientID = proactiveWeixinClientID(delivery, index)
		receipt, err := receiptClient.SendWithReceipt(ctx, reply)
		if err != nil {
			return channel.DeliveryResult{}, err
		}
		if strings.TrimSpace(receipt) == "" {
			return channel.DeliveryResult{}, errors.New("weixin proactive send returned no receipt")
		}
		receipts = append(receipts, receipt)
	}
	return channel.DeliveryResult{ExternalMessageID: strings.Join(receipts, ",")}, nil
}

type recentInboundJobPayloadReader interface {
	ListRecentInboundJobPayloads(context.Context, string, string, int) ([][]byte, error)
}

func (h *Handle) proactiveContextToken(ctx context.Context, accountID, recipientID string) (string, error) {
	reader, ok := h.inbound.(recentInboundJobPayloadReader)
	if !ok {
		return "", errors.New("weixin proactive context store is unavailable")
	}
	payloads, err := reader.ListRecentInboundJobPayloads(ctx, string(protocol.ChannelWeixin), accountID, 20)
	if err != nil {
		return "", fmt.Errorf("load weixin proactive context: %w", err)
	}
	for _, raw := range payloads {
		var payload ilinkInboundJobPayload
		if err := json.Unmarshal(raw, &payload); err != nil {
			continue
		}
		message := payload.Message
		if !message.SenderVerified || strings.TrimSpace(message.AccountID) != accountID || strings.TrimSpace(message.ChatID) != recipientID {
			continue
		}
		if token := strings.TrimSpace(message.ContextToken); token != "" {
			return token, nil
		}
	}
	return "", errors.New("weixin proactive context token is unavailable")
}

func (h *Handle) GatewayConnectionHealthy() bool {
	provider, ok := h.client.(channel.ConnectionHealthProvider)
	return ok && provider.GatewayConnectionHealthy()
}

func (h *Handle) GatewayAccountConnectionHealthy(accountID string) bool {
	provider, ok := h.client.(channel.AccountConnectionHealthProvider)
	return ok && provider.GatewayAccountConnectionHealthy(strings.TrimSpace(accountID))
}

func proactiveWeixinClientID(delivery channel.Delivery, chunkIndex int) string {
	return weixinMessageClientID(firstNonBlank(delivery.ID, delivery.IdempotencyKey), chunkIndex)
}

func weixinMessageClientID(source string, chunkIndex int) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return ""
	}
	source += fmt.Sprintf(":%d", chunkIndex)
	sum := sha256.Sum256([]byte(source))
	return fmt.Sprintf("orrery-%x", sum[:16])
}

func (h *Handle) markActive(key string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.active == nil {
		h.active = map[string]struct{}{}
	}
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

func defaultWeixinStatePath(stateDir string) string {
	stateDir = strings.TrimSpace(stateDir)
	if stateDir == "" {
		return ""
	}
	return filepath.Join(stateDir, "weixin-ilink-state.json")
}
