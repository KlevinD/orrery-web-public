package weixin

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/gateway/channel"
)

type IlinkBotClient struct {
	httpClient            *http.Client
	allowInsecureLoopback bool

	mu              sync.Mutex
	stateMu         sync.Mutex
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	options         Options
	logger          Logger
	inbound         InboundProcessor
	queue           channel.InboundJobQueue
	state           IlinkBotStateStore
	getUpdatesBuf   string
	pendingUpdates  *ilinkGetUpdatesResponse
	seen            map[string]struct{}
	seenOrder       []string
	messageFailures map[string]IlinkMessageFailure
	deadLetters     []IlinkDeadLetter
	xWechatUIN      string
	healthy         bool
	generation      uint64
}

func NewIlinkBotClient() *IlinkBotClient {
	return &IlinkBotClient{}
}

func (c *IlinkBotClient) Start(ctx context.Context, opts Options, logger Logger, inbound InboundProcessor) error {
	opts = opts.WithDefaults()
	if strings.TrimSpace(opts.BotToken) == "" {
		return errors.New("weixin ilink bot token is required")
	}
	if strings.TrimSpace(opts.AccountID) == "" {
		return errors.New("weixin ilink account id is required")
	}
	if strings.TrimSpace(opts.StatePath) == "" {
		return errors.New("weixin durable cursor state path is required")
	}
	if opts.InboundEvents == nil {
		return errors.New("weixin durable inbound event store is required")
	}
	queue, ok := opts.InboundEvents.(channel.InboundJobQueue)
	if !ok || queue == nil {
		return errors.New("weixin durable inbound job queue is required")
	}
	if err := validateIlinkBotTokenScope(opts, c.allowInsecureLoopback); err != nil {
		return err
	}
	if inbound == nil {
		return errors.New("weixin inbound processor is required")
	}

	runCtx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	if c.cancel != nil {
		c.mu.Unlock()
		cancel()
		return errors.New("weixin ilink bot client already started")
	}
	c.generation++
	generation := c.generation
	stateStore := newFileIlinkBotStateStore(opts.StatePath)
	state, err := loadIlinkBotState(ctx, stateStore, opts)
	if err != nil {
		c.mu.Unlock()
		cancel()
		return err
	}
	c.options = opts
	c.logger = logger
	c.inbound = inbound
	c.queue = queue
	c.state = stateStore
	c.getUpdatesBuf = state.GetUpdatesBuf
	c.pendingUpdates = nil
	c.seen, c.seenOrder = ilinkSeenIndex(state.SeenMessageIDs, opts.SeenMessageLimit)
	c.messageFailures = ilinkFailureIndex(state.MessageFailures)
	c.deadLetters = append([]IlinkDeadLetter(nil), state.DeadLetters...)
	c.xWechatUIN = strings.TrimSpace(opts.XWechatUIN)
	if c.xWechatUIN == "" {
		c.xWechatUIN, err = newRandomWechatUIN()
		if err != nil {
			c.mu.Unlock()
			cancel()
			return err
		}
	}
	c.httpClient = secureIlinkHTTPClient(time.Duration(opts.RequestTimeoutSeconds) * time.Second)
	c.cancel = cancel
	c.wg.Add(1)
	c.mu.Unlock()
	defer c.wg.Done()

	validationTimeout := time.Duration(opts.RequestTimeoutSeconds) * time.Second
	if validationTimeout > time.Minute {
		validationTimeout = time.Minute
	}
	validationCtx, validationCancel := context.WithTimeout(runCtx, validationTimeout)
	validationErr := c.validateConnection(validationCtx)
	validationCancel()
	if validationErr != nil {
		cancel()
		c.clearGeneration(generation)
		return fmt.Errorf("validate Weixin iLink connection: %w", validationErr)
	}
	c.mu.Lock()
	if c.cancel == nil || c.generation != generation || runCtx.Err() != nil {
		c.mu.Unlock()
		cancel()
		return context.Canceled
	}
	c.wg.Add(2)
	c.mu.Unlock()
	go c.pollLoop(runCtx)
	go func() {
		defer c.wg.Done()
		channel.RunInboundJobWorker(runCtx, queue, "weixin", opts.AccountID, c.processQueuedInbound, logger)
	}()
	return nil
}

func (c *IlinkBotClient) SendReply(ctx context.Context, message OutboundMessage) error {
	_, err := c.SendWithReceipt(ctx, message)
	return err
}

func (c *IlinkBotClient) SendWithReceipt(ctx context.Context, message OutboundMessage) (string, error) {
	opts, client, xWechatUIN := c.snapshot()
	if client == nil {
		return "", errors.New("weixin ilink bot client is not connected")
	}
	to := strings.TrimSpace(message.ChatID)
	if to == "" {
		return "", channel.PermanentDeliveryError(errors.New("weixin recipient is required"))
	}
	text := strings.TrimSpace(message.Text)
	if text == "" && len(message.Image) == 0 {
		return "", channel.PermanentDeliveryError(errors.New("weixin message text is required"))
	}
	clientID := strings.TrimSpace(message.ClientID)
	if clientID == "" {
		var err error
		clientID, err = newRandomIlinkMessageID()
		if err != nil {
			return "", err
		}
	}
	items := []map[string]any{{"type": 1, "text_item": map[string]any{"text": text}}}
	if len(message.Image) > 0 {
		item, err := c.uploadOutboundImage(ctx, client, opts, xWechatUIN, to, message.Image)
		if err != nil {
			return "", err
		}
		items = []map[string]any{item}
	}
	body := map[string]any{
		"msg": map[string]any{
			"from_user_id":  "",
			"to_user_id":    to,
			"client_id":     clientID,
			"message_type":  2,
			"message_state": 2,
			"context_token": strings.TrimSpace(message.ContextToken),
			"item_list":     items,
		},
		"base_info": c.baseInfo(opts),
	}
	var out ilinkSendMessageResponse
	if err := c.postJSON(ctx, client, opts, xWechatUIN, opts.SendMessagePath, body, &out); err != nil {
		logWarn(c.logger, "weixin ilink outbound send failed", map[string]any{
			"account_id":    strings.TrimSpace(opts.AccountID),
			"error_code":    "send_request_failed",
			"failure_stage": "request",
			"permanent":     channel.IsPermanentDeliveryError(err),
		})
		return "", err
	}
	if err := out.Err(); err != nil {
		metadata := map[string]any{
			"account_id":    strings.TrimSpace(opts.AccountID),
			"error_code":    "send_response_rejected",
			"failure_stage": "provider_response",
		}
		if out.ReturnCode != nil {
			metadata["return_code"] = *out.ReturnCode
		}
		if out.ErrCode != nil {
			metadata["provider_error_code"] = *out.ErrCode
		}
		logWarn(c.logger, "weixin ilink outbound send failed", metadata)
		return "", classifyIlinkDeliveryError(fmt.Errorf("weixin sendmessage failed: %w", err))
	}
	return clientID, nil
}

func classifyIlinkDeliveryError(err error) error {
	if err == nil || channel.IsPermanentDeliveryError(err) {
		return err
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"invalid token", "token invalid", "unauthorized", "forbidden", "permission", "recipient", "not found",
		"无权限", "未授权", "用户不存在", "账号不存在", "已封禁", "被封禁",
	} {
		if strings.Contains(message, marker) {
			return channel.PermanentDeliveryError(err)
		}
	}
	return err
}

func (c *IlinkBotClient) Stop(ctx context.Context) error {
	c.mu.Lock()
	cancel := c.cancel
	if cancel == nil {
		c.mu.Unlock()
		return nil
	}
	c.generation++
	stopGeneration := c.generation
	c.healthy = false
	c.httpClient = nil
	c.mu.Unlock()
	cancel()
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		c.mu.Lock()
		if c.generation == stopGeneration {
			c.cancel = nil
			c.inbound = nil
			c.queue = nil
			c.state = nil
			c.getUpdatesBuf = ""
			c.pendingUpdates = nil
			c.seen = nil
			c.seenOrder = nil
			c.xWechatUIN = ""
		}
		c.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *IlinkBotClient) clearGeneration(generation uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation != generation {
		return
	}
	c.cancel = nil
	c.httpClient = nil
	c.inbound = nil
	c.queue = nil
	c.state = nil
	c.pendingUpdates = nil
	c.healthy = false
}

func (c *IlinkBotClient) pollLoop(ctx context.Context) {
	defer c.wg.Done()
	logInfo(c.logger, "weixin ilink bot polling started", map[string]any{"base_url": c.options.IlinkBaseURL})
	baseDelay := time.Duration(c.options.PollIntervalSeconds) * time.Second
	delay := baseDelay
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if err := c.pollOnce(ctx); err != nil && ctx.Err() == nil {
			c.setConnectionHealthy(false)
			logWarn(c.logger, "weixin ilink bot poll failed", map[string]any{
				"error_code":    "poll_failed",
				"failure_stage": ilinkPollFailureStage(err),
				"account_id":    c.options.AccountID,
			})
			delay *= 2
			if delay > time.Minute {
				delay = time.Minute
			}
			continue
		}
		c.setConnectionHealthy(true)
		delay = baseDelay
	}
}

func ilinkPollFailureStage(err error) string {
	if err == nil {
		return ""
	}
	message := strings.ToLower(err.Error())
	for _, candidate := range []struct {
		stage   string
		markers []string
	}{
		{stage: "get_updates", markers: []string{"weixin getupdates failed", "weixin ilink http"}},
		{stage: "gateway_route", markers: []string{"gateway_route_unavailable"}},
		{stage: "gateway_runtime", markers: []string{"gateway_runtime_unavailable"}},
		{stage: "account_validation", markers: []string{"does not match the configured account"}},
		{stage: "sender_validation", markers: []string{"has no verified sender"}},
		{stage: "message_validation", markers: []string{"weixin chat id is required", "weixin message text is required", "weixin message text or image is required"}},
		{stage: "media_download", markers: []string{"weixin ilink media download failed", "download weixin image"}},
		{stage: "inbound_concurrency", markers: []string{"already being processed", "already has a message in progress"}},
		{stage: "inbound_claim", markers: []string{"claim weixin inbound message", "claim gateway inbound event"}},
		{stage: "inbound_runtime", markers: []string{"weixin ilink inbound failed"}},
		{stage: "outbound_reply", markers: []string{"weixin ilink reply failed"}},
		{stage: "inbound_completion", markers: []string{"complete weixin inbound message", "maintain weixin inbound event claim"}},
		{stage: "state_persistence", markers: []string{"persist weixin ilink delivered message", "persist weixin ilink cursor"}},
	} {
		for _, marker := range candidate.markers {
			if strings.Contains(message, marker) {
				return candidate.stage
			}
		}
	}
	return "unknown"
}

func (c *IlinkBotClient) validateConnection(ctx context.Context) error {
	out, err := c.getUpdates(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.pendingUpdates = &out
	c.healthy = true
	c.mu.Unlock()
	return nil
}

func (c *IlinkBotClient) getUpdates(ctx context.Context) (ilinkGetUpdatesResponse, error) {
	opts, client, xWechatUIN := c.snapshot()
	body := map[string]any{
		"get_updates_buf": c.currentGetUpdatesBuf(),
		"base_info":       c.baseInfo(opts),
	}
	var out ilinkGetUpdatesResponse
	if err := c.postJSON(ctx, client, opts, xWechatUIN, opts.GetUpdatesPath, body, &out); err != nil {
		return ilinkGetUpdatesResponse{}, err
	}
	if err := out.Err(); err != nil {
		return ilinkGetUpdatesResponse{}, fmt.Errorf("weixin getupdates failed: %w", err)
	}
	return out, nil
}

func (c *IlinkBotClient) pollOnce(ctx context.Context) error {
	opts, _, _ := c.snapshot()
	pending := c.pendingConnectionUpdates()
	var out ilinkGetUpdatesResponse
	if pending != nil {
		out = *pending
	} else {
		var err error
		out, err = c.getUpdates(ctx)
		if err != nil {
			return err
		}
	}
	for _, msg := range out.Messages {
		messageKey := ilinkMessageDedupeKey(msg)
		if c.isMessageDelivered(messageKey) {
			continue
		}
		var err error
		if c.queue != nil {
			err = c.enqueueInboundMessage(ctx, opts, msg, messageKey)
		} else {
			err = c.processInboundMessage(ctx, opts, msg, messageKey)
		}
		if err != nil {
			return err
		}
	}
	if err := c.setGetUpdatesBuf(ctx, out.GetUpdatesBuf); err != nil {
		return fmt.Errorf("persist weixin ilink cursor: %w", err)
	}
	if pending != nil {
		c.clearPendingConnectionUpdates(pending)
	}
	c.setConnectionHealthy(true)
	return nil
}

type ilinkInboundJobPayload struct {
	EventID string         `json:"event_id"`
	Message InboundMessage `json:"message"`
}

func (c *IlinkBotClient) enqueueInboundMessage(ctx context.Context, opts Options, msg ilinkWeixinMessage, messageKey string) error {
	inbound, err := normalizedIlinkInboundMessage(opts, msg, messageKey)
	if err != nil {
		return c.handleUnqueueableMessage(ctx, opts, msg, messageKey, err)
	}
	payload, err := json.Marshal(ilinkInboundJobPayload{EventID: messageKey, Message: inbound})
	if err != nil {
		return fmt.Errorf("encode durable Weixin inbound message: %w", err)
	}
	if _, err := c.queue.EnqueueInboundJob(ctx, channel.InboundJob{
		ChannelKey:  "weixin",
		AccountID:   inbound.AccountID,
		EventID:     messageKey,
		Payload:     payload,
		MaxAttempts: effectiveInboundMaxAttempts(opts.InboundMaxAttempts),
	}); err != nil {
		return fmt.Errorf("enqueue durable Weixin inbound message: %w", err)
	}
	if err := c.markMessageDelivered(ctx, messageKey); err != nil {
		return fmt.Errorf("persist queued Weixin message: %w", err)
	}
	return nil
}

func (c *IlinkBotClient) processQueuedInbound(ctx context.Context, job channel.InboundJob) error {
	var payload ilinkInboundJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return errors.New("decode durable Weixin inbound message")
	}
	if payload.EventID != job.EventID || payload.Message.AccountID != job.AccountID || !payload.Message.SenderVerified {
		return errors.New("durable Weixin inbound message identity mismatch")
	}
	resolved, err := c.resolveInboundAttachments(ctx, payload.Message)
	if err != nil {
		cause := fmt.Errorf("weixin ilink media download failed for %s: %w", payload.Message.ChatID, err)
		if job.MaxAttempts > 0 && job.Attempts >= job.MaxAttempts {
			c.logQueuedDeadLetter(job, "media_download")
		}
		return cause
	}
	replies, err := c.inbound(ctx, resolved)
	if err != nil {
		cause := fmt.Errorf("weixin ilink inbound failed for %s: %w", payload.Message.ChatID, err)
		if job.MaxAttempts > 0 && job.Attempts >= job.MaxAttempts {
			c.logQueuedDeadLetter(job, ilinkPollFailureStage(cause))
		}
		return cause
	}
	for _, reply := range replies {
		if err := c.SendReply(ctx, reply); err != nil {
			return fmt.Errorf("weixin ilink reply failed for %s: %w", reply.ChatID, err)
		}
	}
	return nil
}

func (c *IlinkBotClient) handleUnqueueableMessage(ctx context.Context, opts Options, msg ilinkWeixinMessage, messageKey string, cause error) error {
	failure, err := c.recordMessageFailure(ctx, messageKey, ilinkPollFailureStage(cause))
	if err != nil {
		return errors.Join(cause, fmt.Errorf("persist Weixin validation failure: %w", err))
	}
	if failure.Attempts < effectiveInboundMaxAttempts(opts.InboundMaxAttempts) {
		return cause
	}
	if err := c.markMessageDeadLettered(ctx, failure); err != nil {
		return errors.Join(cause, fmt.Errorf("persist invalid Weixin dead letter: %w", err))
	}
	logWarn(c.logger, "weixin ilink invalid inbound message dead-lettered", map[string]any{
		"error_code": "inbound_dead_letter", "failure_stage": failure.FailureStage,
		"account_id": opts.AccountID, "attempts": failure.Attempts,
	})
	return nil
}

func (c *IlinkBotClient) logQueuedDeadLetter(job channel.InboundJob, failureStage string) {
	// Keep channel failures observable without synthesizing an assistant reply.
	logWarn(c.logger, "weixin ilink inbound message exhausted retries", map[string]any{
		"error_code": "inbound_dead_letter", "failure_stage": failureStage,
		"account_id": job.AccountID, "attempts": job.Attempts,
	})
}

func normalizedIlinkInboundMessage(opts Options, msg ilinkWeixinMessage, messageKey string) (InboundMessage, error) {
	inbound := inboundFromWeixinMessage(msg)
	configuredAccountID := strings.TrimSpace(opts.AccountID)
	providerAccountID := strings.TrimSpace(inbound.AccountID)
	if providerAccountID != "" && providerAccountID != configuredAccountID {
		return InboundMessage{}, errors.New("Weixin inbound message does not match the configured account")
	}
	if strings.TrimSpace(inbound.SenderID) == "" {
		return InboundMessage{}, errors.New("Weixin inbound message has no verified sender")
	}
	inbound.SenderVerified = true
	inbound.AccountID = configuredAccountID
	inbound.MessageID = firstNonBlank(inbound.MessageID, messageKey)
	return inbound, nil
}

func (c *IlinkBotClient) pendingConnectionUpdates() *ilinkGetUpdatesResponse {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pendingUpdates
}

func (c *IlinkBotClient) clearPendingConnectionUpdates(processed *ilinkGetUpdatesResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pendingUpdates == processed {
		c.pendingUpdates = nil
	}
}

func (c *IlinkBotClient) processInboundMessage(ctx context.Context, opts Options, msg ilinkWeixinMessage, messageKey string) (processErr error) {
	var claim channel.InboundEventClaim
	claimCompleted := false
	if opts.InboundEvents != nil {
		claimed, err := opts.InboundEvents.ClaimInboundEvent(ctx, "weixin", opts.AccountID, messageKey)
		if err != nil {
			return fmt.Errorf("claim Weixin inbound message: %w", err)
		}
		if claimed.State == channel.InboundEventCompleted {
			if err := c.markMessageDelivered(ctx, messageKey); err != nil {
				return fmt.Errorf("persist deduplicated Weixin message: %w", err)
			}
			return nil
		}
		if claimed.State != channel.InboundEventAcquired {
			return errors.New("Weixin inbound message is already being processed")
		}
		claim = claimed
		defer func() {
			if claimCompleted {
				return
			}
			releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if releaseErr := opts.InboundEvents.ReleaseInboundEvent(releaseCtx, claim); releaseErr != nil {
				processErr = errors.Join(processErr, releaseErr)
			}
		}()
	}
	processingCtx := ctx
	stopRenewal := func() error { return nil }
	if opts.InboundEvents != nil {
		processingCtx, stopRenewal = channel.MaintainInboundEventClaim(ctx, opts.InboundEvents, claim)
		defer func() { processErr = errors.Join(processErr, stopRenewal()) }()
	}

	inbound := inboundFromWeixinMessage(msg)
	configuredAccountID := strings.TrimSpace(opts.AccountID)
	providerAccountID := strings.TrimSpace(inbound.AccountID)
	if providerAccountID != "" && providerAccountID != configuredAccountID {
		cause := errors.New("Weixin inbound message does not match the configured account")
		handled, err := c.handleMessageFailure(processingCtx, opts, messageKey, claim, stopRenewal, "account_validation")
		claimCompleted = handled
		if err != nil {
			return errors.Join(cause, err)
		}
		if handled {
			return nil
		}
		return cause
	}
	if strings.TrimSpace(inbound.SenderID) == "" {
		cause := errors.New("Weixin inbound message has no verified sender")
		handled, err := c.handleMessageFailure(processingCtx, opts, messageKey, claim, stopRenewal, "sender_validation")
		claimCompleted = handled
		if err != nil {
			return errors.Join(cause, err)
		}
		if handled {
			return nil
		}
		return cause
	}
	inbound.SenderVerified = true
	inbound.AccountID = configuredAccountID
	inbound.MessageID = firstNonBlank(inbound.MessageID, messageKey)
	if failure, exists := c.messageFailure(messageKey); exists && failure.Attempts >= effectiveInboundMaxAttempts(opts.InboundMaxAttempts) {
		handled, handleErr := c.finalizeMessageFailure(opts, failure, claim, stopRenewal)
		claimCompleted = handled
		if handleErr != nil {
			return handleErr
		}
		return nil
	}
	resolved, err := c.resolveInboundAttachments(processingCtx, inbound)
	if err != nil {
		cause := fmt.Errorf("weixin ilink media download failed for %s: %w", inbound.ChatID, err)
		handled, handleErr := c.handleMessageFailure(processingCtx, opts, messageKey, claim, stopRenewal, "media_download")
		claimCompleted = handled
		if handleErr != nil {
			return errors.Join(cause, handleErr)
		}
		if handled {
			return nil
		}
		return cause
	}
	inbound = resolved
	replies, err := c.inbound(processingCtx, inbound)
	if err != nil {
		cause := fmt.Errorf("weixin ilink inbound failed for %s: %w", inbound.ChatID, err)
		handled, handleErr := c.handleMessageFailure(processingCtx, opts, messageKey, claim, stopRenewal, ilinkPollFailureStage(cause))
		claimCompleted = handled
		if handleErr != nil {
			return errors.Join(cause, handleErr)
		}
		if handled {
			return nil
		}
		return cause
	}
	for _, reply := range replies {
		if err := c.SendReply(processingCtx, reply); err != nil {
			cause := fmt.Errorf("weixin ilink reply failed for %s: %w", reply.ChatID, err)
			handled, handleErr := c.handleMessageFailure(processingCtx, opts, messageKey, claim, stopRenewal, "outbound_reply")
			claimCompleted = handled
			if handleErr != nil {
				return errors.Join(cause, handleErr)
			}
			if handled {
				return nil
			}
			return cause
		}
	}
	if opts.InboundEvents != nil {
		if err := stopRenewal(); err != nil {
			return fmt.Errorf("maintain Weixin inbound event claim: %w", err)
		}
		completeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = opts.InboundEvents.CompleteInboundEvent(completeCtx, claim)
		cancel()
		if err != nil {
			return fmt.Errorf("complete Weixin inbound message: %w", err)
		}
		claimCompleted = true
	}
	if err := c.markMessageDelivered(ctx, messageKey); err != nil {
		return fmt.Errorf("persist weixin ilink delivered message: %w", err)
	}
	return nil
}

func (c *IlinkBotClient) handleMessageFailure(ctx context.Context, opts Options, messageKey string, claim channel.InboundEventClaim, stopRenewal func() error, failureStage string) (bool, error) {
	failure, err := c.recordMessageFailure(ctx, messageKey, failureStage)
	if err != nil {
		return false, fmt.Errorf("persist Weixin inbound failure: %w", err)
	}
	if failure.Attempts < effectiveInboundMaxAttempts(opts.InboundMaxAttempts) {
		return false, nil
	}
	return c.finalizeMessageFailure(opts, failure, claim, stopRenewal)
}

func (c *IlinkBotClient) finalizeMessageFailure(opts Options, failure IlinkMessageFailure, claim channel.InboundEventClaim, stopRenewal func() error) (bool, error) {
	if err := stopRenewal(); err != nil {
		return false, fmt.Errorf("maintain Weixin inbound event claim before dead-lettering: %w", err)
	}
	if opts.InboundEvents != nil {
		completeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := opts.InboundEvents.CompleteInboundEvent(completeCtx, claim)
		cancel()
		if err != nil {
			return false, fmt.Errorf("complete dead-lettered Weixin inbound message: %w", err)
		}
	}
	stateCtx, stateCancel := context.WithTimeout(context.Background(), 5*time.Second)
	err := c.markMessageDeadLettered(stateCtx, failure)
	stateCancel()
	if err != nil {
		return true, fmt.Errorf("persist Weixin inbound dead letter: %w", err)
	}
	logWarn(c.logger, "weixin ilink inbound message dead-lettered", map[string]any{
		"error_code": "inbound_dead_letter", "failure_stage": failure.FailureStage, "account_id": opts.AccountID,
		"attempts": failure.Attempts,
	})
	return true, nil
}

func (c *IlinkBotClient) messageFailure(messageKey string) (IlinkMessageFailure, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	failure, exists := c.messageFailures[strings.TrimSpace(messageKey)]
	return failure, exists
}

func (c *IlinkBotClient) GatewayConnectionHealthy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cancel != nil && c.healthy && c.httpClient != nil
}

func (c *IlinkBotClient) setConnectionHealthy(healthy bool) {
	c.mu.Lock()
	c.healthy = healthy
	c.mu.Unlock()
}

func (c *IlinkBotClient) postJSON(ctx context.Context, client *http.Client, opts Options, xWechatUIN string, path string, body any, out any) error {
	if client == nil {
		return errors.New("weixin ilink bot client is not connected")
	}
	if err := validateIlinkEndpointOrigin(opts.IlinkBaseURL, c.allowInsecureLoopback); err != nil {
		return err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ilinkEndpoint(opts.IlinkBaseURL, path), bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(opts.BotToken))
	req.Header.Set("AuthorizationType", "ilink_bot_token")
	req.Header.Set("iLink-App-Id", opts.IlinkAppID)
	req.Header.Set("iLink-App-ClientVersion", opts.IlinkAppClientVersion)
	if xWechatUIN != "" {
		req.Header.Set("X-WECHAT-UIN", xWechatUIN)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		httpErr := fmt.Errorf("Weixin iLink HTTP %d", resp.StatusCode)
		if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusRequestTimeout && resp.StatusCode != http.StatusTooManyRequests {
			return channel.PermanentDeliveryError(httpErr)
		}
		return httpErr
	}
	return decodeIlinkJSON(resp.Body, out)
}

func (c *IlinkBotClient) snapshot() (Options, *http.Client, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.options, c.httpClient, c.xWechatUIN
}

func (c *IlinkBotClient) currentGetUpdatesBuf() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.getUpdatesBuf
}

func (c *IlinkBotClient) setGetUpdatesBuf(ctx context.Context, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return c.updateState(ctx, func(state *IlinkBotState) {
		state.GetUpdatesBuf = value
	})
}

func (c *IlinkBotClient) isMessageDelivered(messageKey string) bool {
	messageKey = strings.TrimSpace(messageKey)
	if messageKey == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, exists := c.seen[messageKey]
	return exists
}

func (c *IlinkBotClient) markMessageDelivered(ctx context.Context, messageKey string) error {
	messageKey = strings.TrimSpace(messageKey)
	if messageKey == "" {
		return nil
	}
	return c.updateState(ctx, func(state *IlinkBotState) {
		if !containsString(state.SeenMessageIDs, messageKey) {
			state.SeenMessageIDs = append(state.SeenMessageIDs, messageKey)
		}
		failure, found := removeIlinkMessageFailure(&state.MessageFailures, messageKey)
		if found && failure.Attempts >= effectiveInboundMaxAttempts(c.options.InboundMaxAttempts) {
			appendIlinkDeadLetter(&state.DeadLetters, failure, time.Now().UTC())
		}
	})
}

func (c *IlinkBotClient) currentStateLocked() IlinkBotState {
	failures := make([]IlinkMessageFailure, 0, len(c.messageFailures))
	keys := make([]string, 0, len(c.messageFailures))
	for key := range c.messageFailures {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		failures = append(failures, c.messageFailures[key])
	}
	return IlinkBotState{
		GetUpdatesBuf:   strings.TrimSpace(c.getUpdatesBuf),
		SeenMessageIDs:  append([]string(nil), c.seenOrder...),
		MessageFailures: failures,
		DeadLetters:     append([]IlinkDeadLetter(nil), c.deadLetters...),
	}
}

func (c *IlinkBotClient) recordMessageFailure(ctx context.Context, messageKey, failureStage string) (IlinkMessageFailure, error) {
	messageKey = strings.TrimSpace(messageKey)
	failureStage = strings.TrimSpace(failureStage)
	if failureStage == "" || failureStage == "unknown" {
		failureStage = "inbound_runtime"
	}
	if messageKey == "" {
		return IlinkMessageFailure{}, errors.New("Weixin message key is required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var recorded IlinkMessageFailure
	err := c.updateState(ctx, func(state *IlinkBotState) {
		for index := range state.MessageFailures {
			if state.MessageFailures[index].MessageKey != messageKey {
				continue
			}
			state.MessageFailures[index].Attempts++
			state.MessageFailures[index].FailureStage = failureStage
			state.MessageFailures[index].LastFailedAt = now
			recorded = state.MessageFailures[index]
			return
		}
		recorded = IlinkMessageFailure{
			MessageKey: messageKey, Attempts: 1, FailureStage: failureStage,
			FirstFailedAt: now, LastFailedAt: now,
		}
		state.MessageFailures = append(state.MessageFailures, recorded)
	})
	return recorded, err
}

func (c *IlinkBotClient) markMessageDeadLettered(ctx context.Context, failure IlinkMessageFailure) error {
	return c.updateState(ctx, func(state *IlinkBotState) {
		if !containsString(state.SeenMessageIDs, failure.MessageKey) {
			state.SeenMessageIDs = append(state.SeenMessageIDs, failure.MessageKey)
		}
		removeIlinkMessageFailure(&state.MessageFailures, failure.MessageKey)
		appendIlinkDeadLetter(&state.DeadLetters, failure, time.Now().UTC())
	})
}

func (c *IlinkBotClient) updateState(ctx context.Context, mutate func(*IlinkBotState)) error {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.mu.Lock()
	state := c.currentStateLocked()
	store := c.state
	seenLimit := c.options.SeenMessageLimit
	c.mu.Unlock()
	mutate(&state)
	state = normalizeIlinkBotState(state, seenLimit)
	if store != nil {
		if err := store.Save(ctx, state); err != nil {
			return err
		}
	}
	c.mu.Lock()
	c.getUpdatesBuf = state.GetUpdatesBuf
	c.seen, c.seenOrder = ilinkSeenIndex(state.SeenMessageIDs, seenLimit)
	c.messageFailures = ilinkFailureIndex(state.MessageFailures)
	c.deadLetters = append([]IlinkDeadLetter(nil), state.DeadLetters...)
	c.mu.Unlock()
	return nil
}

func ilinkFailureIndex(failures []IlinkMessageFailure) map[string]IlinkMessageFailure {
	index := make(map[string]IlinkMessageFailure, len(failures))
	for _, failure := range failures {
		if key := strings.TrimSpace(failure.MessageKey); key != "" {
			index[key] = failure
		}
	}
	return index
}

func removeIlinkMessageFailure(failures *[]IlinkMessageFailure, messageKey string) (IlinkMessageFailure, bool) {
	for index, failure := range *failures {
		if failure.MessageKey != messageKey {
			continue
		}
		*failures = append((*failures)[:index], (*failures)[index+1:]...)
		return failure, true
	}
	return IlinkMessageFailure{}, false
}

func appendIlinkDeadLetter(deadLetters *[]IlinkDeadLetter, failure IlinkMessageFailure, deadLetteredAt time.Time) {
	for _, existing := range *deadLetters {
		if existing.MessageKey == failure.MessageKey {
			return
		}
	}
	*deadLetters = append(*deadLetters, IlinkDeadLetter{
		MessageKey: failure.MessageKey, Attempts: failure.Attempts, FailureStage: failure.FailureStage,
		FirstFailedAt: failure.FirstFailedAt, DeadLetteredAt: deadLetteredAt.Format(time.RFC3339Nano),
	})
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func effectiveInboundMaxAttempts(configured int) int {
	if configured <= 0 {
		return defaultInboundMaxAttempts
	}
	return configured
}

func (c *IlinkBotClient) baseInfo(opts Options) map[string]any {
	info := map[string]any{"channel_version": opts.IlinkChannelVersion}
	if opts.SKRouteTag != "" {
		info["sk_route_tag"] = opts.SKRouteTag
	}
	return info
}

type ilinkAPIResponse struct {
	ReturnCode *int
	ErrCode    *int
	ErrMessage string
}

func (r *ilinkAPIResponse) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if value, ok := firstRawInt(raw, "ret", "Ret", "return_code", "returnCode"); ok {
		r.ReturnCode = &value
	}
	if value, ok := firstRawInt(raw, "errcode", "errCode", "error_code", "errorCode"); ok {
		r.ErrCode = &value
	}
	r.ErrMessage = firstRawString(raw, "errmsg", "errMsg", "error_message", "errorMessage", "message")
	return nil
}

func (r ilinkAPIResponse) Err() error {
	if r.ReturnCode == nil && r.ErrCode == nil {
		if message := strings.TrimSpace(r.ErrMessage); message != "" {
			return errors.New(message)
		}
		return errors.New("missing ret/errcode in response")
	}
	ret := 0
	if r.ReturnCode != nil {
		ret = *r.ReturnCode
	}
	errCode := 0
	if r.ErrCode != nil {
		errCode = *r.ErrCode
	}
	if ret == 0 && errCode == 0 {
		return nil
	}
	message := strings.TrimSpace(r.ErrMessage)
	if message == "" {
		message = "non-zero response"
	}
	return fmt.Errorf("%s (ret=%d errcode=%d)", message, ret, errCode)
}

type ilinkGetUpdatesResponse struct {
	ilinkAPIResponse
	Messages      []ilinkWeixinMessage `json:"msgs"`
	GetUpdatesBuf string               `json:"get_updates_buf"`
	TimeoutMillis int                  `json:"longpolling_timeout_ms"`
	messagesSet   bool
	cursorSet     bool
}

type ilinkSendMessageResponse struct {
	ilinkAPIResponse
}

func (r ilinkSendMessageResponse) Err() error {
	if r.ReturnCode == nil && r.ErrCode == nil && strings.TrimSpace(r.ErrMessage) == "" {
		return nil
	}
	return r.ilinkAPIResponse.Err()
}

func (r *ilinkGetUpdatesResponse) UnmarshalJSON(data []byte) error {
	var api ilinkAPIResponse
	if err := json.Unmarshal(data, &api); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*r = ilinkGetUpdatesResponse{ilinkAPIResponse: api}
	if messages, ok, err := firstRawMessages(raw, "msgs", "Msgs", "messages", "Messages"); err != nil {
		return err
	} else if ok {
		r.Messages = messages
		r.messagesSet = true
	}
	if value, ok := firstRawStringOK(raw, "get_updates_buf", "getUpdatesBuf", "GetUpdatesBuf", "sync_buf", "syncBuf", "SyncBuf"); ok {
		r.GetUpdatesBuf = value
		r.cursorSet = true
	}
	if value, ok := firstRawInt(raw, "longpolling_timeout_ms", "longPollingTimeoutMs", "timeout_ms", "timeoutMs"); ok {
		r.TimeoutMillis = value
	}
	return nil
}

func (r ilinkGetUpdatesResponse) Err() error {
	if r.ReturnCode != nil || r.ErrCode != nil {
		if err := r.ilinkAPIResponse.Err(); err != nil {
			return err
		}
	}
	if !r.messagesSet {
		return errors.New("missing msgs/messages in getupdates response")
	}
	if !r.cursorSet {
		return errors.New("missing get_updates_buf/getUpdatesBuf/sync_buf in getupdates response")
	}
	return nil
}

func loadIlinkBotState(ctx context.Context, store IlinkBotStateStore, opts Options) (IlinkBotState, error) {
	state := IlinkBotState{GetUpdatesBuf: strings.TrimSpace(opts.GetUpdatesBuf)}
	if store != nil {
		loaded, err := store.Load(ctx)
		if err != nil {
			return IlinkBotState{}, fmt.Errorf("load weixin ilink state: %w", err)
		}
		loaded = normalizeIlinkBotState(loaded, opts.SeenMessageLimit)
		if loaded.GetUpdatesBuf != "" {
			state.GetUpdatesBuf = loaded.GetUpdatesBuf
		}
		state.SeenMessageIDs = loaded.SeenMessageIDs
		state.MessageFailures = loaded.MessageFailures
		state.DeadLetters = loaded.DeadLetters
	}
	return normalizeIlinkBotState(state, opts.SeenMessageLimit), nil
}

func ilinkSeenIndex(ids []string, limit int) (map[string]struct{}, []string) {
	state := normalizeIlinkBotState(IlinkBotState{SeenMessageIDs: ids}, limit)
	seen := make(map[string]struct{}, len(state.SeenMessageIDs))
	for _, id := range state.SeenMessageIDs {
		seen[id] = struct{}{}
	}
	return seen, state.SeenMessageIDs
}

func ilinkMessageDedupeKey(message ilinkWeixinMessage) string {
	if id := messageIDString(message.MessageID); id != "" {
		return "message_id:" + id
	}
	if id := strings.TrimSpace(message.ClientID); id != "" {
		return "client_id:" + id
	}
	if seq := messageIDString(message.Seq); seq != "" && strings.TrimSpace(message.FromUserID) != "" {
		return "seq:" + strings.TrimSpace(message.FromUserID) + ":" + seq
	}
	raw, err := json.Marshal(message)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("content:%x", sum[:16])
}

func firstRawString(raw map[string]json.RawMessage, keys ...string) string {
	value, _ := firstRawStringOK(raw, keys...)
	return value
}

func firstRawStringOK(raw map[string]json.RawMessage, keys ...string) (string, bool) {
	for _, key := range keys {
		data, ok := raw[key]
		if !ok {
			continue
		}
		var text string
		if json.Unmarshal(data, &text) == nil {
			return strings.TrimSpace(text), true
		}
		var number json.Number
		if json.Unmarshal(data, &number) == nil {
			return number.String(), true
		}
	}
	return "", false
}

func firstRawInt(raw map[string]json.RawMessage, keys ...string) (int, bool) {
	for _, key := range keys {
		data, ok := raw[key]
		if !ok {
			continue
		}
		var number json.Number
		if json.Unmarshal(data, &number) == nil {
			if value, err := strconv.Atoi(number.String()); err == nil {
				return value, true
			}
		}
		var text string
		if json.Unmarshal(data, &text) == nil {
			if value, err := strconv.Atoi(strings.TrimSpace(text)); err == nil {
				return value, true
			}
		}
	}
	return 0, false
}

func firstRawMessages(raw map[string]json.RawMessage, keys ...string) ([]ilinkWeixinMessage, bool, error) {
	for _, key := range keys {
		data, ok := raw[key]
		if !ok {
			continue
		}
		var messages []ilinkWeixinMessage
		if err := json.Unmarshal(data, &messages); err != nil {
			return nil, true, fmt.Errorf("decode %s: %w", key, err)
		}
		return messages, true, nil
	}
	return nil, false, nil
}

func ilinkEndpoint(baseURL string, path string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	path = strings.TrimLeft(strings.TrimSpace(path), "/")
	if baseURL == "" {
		baseURL = defaultIlinkBaseURL
	}
	if path == "" {
		return baseURL
	}
	parsed, err := url.Parse(baseURL)
	if err == nil && parsed.Path != "" && strings.HasSuffix(strings.TrimRight(parsed.Path, "/"), "/"+path) {
		return baseURL
	}
	return baseURL + "/" + path
}

func validateIlinkBotTokenScope(opts Options, allowInsecureLoopback bool) error {
	opts = opts.WithDefaults()
	if strings.Trim(strings.TrimSpace(opts.GetUpdatesPath), "/") != defaultIlinkGetUpdatesPath {
		return fmt.Errorf("weixin ilink token may only be used with %s", defaultIlinkGetUpdatesPath)
	}
	if strings.Trim(strings.TrimSpace(opts.SendMessagePath), "/") != defaultIlinkSendMessagePath {
		return fmt.Errorf("weixin ilink token may only be used with %s", defaultIlinkSendMessagePath)
	}
	if err := validateIlinkEndpointOrigin(opts.IlinkBaseURL, allowInsecureLoopback); err != nil {
		return err
	}
	if err := validateIlinkMediaURL(opts.IlinkCDNBaseURL, allowInsecureLoopback); err != nil {
		return err
	}
	for _, account := range opts.Accounts {
		if err := validateIlinkEndpointOrigin(firstNonBlank(account.IlinkBaseURL, opts.IlinkBaseURL), allowInsecureLoopback); err != nil {
			return err
		}
		if err := validateIlinkMediaURL(firstNonBlank(account.IlinkCDNBaseURL, opts.IlinkCDNBaseURL), allowInsecureLoopback); err != nil {
			return err
		}
	}
	return nil
}

func validateIlinkHost(baseURL string) error {
	return validateIlinkEndpointOrigin(baseURL, false)
}

func secureIlinkHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: secureIlinkTransport(),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func newRandomWechatUIN() (string, error) {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate Weixin device id: %w", err)
	}
	value := strconv.FormatUint(uint64(binary.BigEndian.Uint32(buf[:])), 10)
	return base64.StdEncoding.EncodeToString([]byte(value)), nil
}

func newRandomIlinkMessageID() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate Weixin message id: %w", err)
	}
	return "orrery-" + strconv.FormatUint(binary.BigEndian.Uint64(buf[:]), 10), nil
}
