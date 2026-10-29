package channel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	gwconfig "orrery_v2/internal/gateway/config"
	gwcore "orrery_v2/internal/gateway/core"
	"orrery_v2/internal/gateway/protocol"
)

// deliveryError marks a send failure whose retry semantics are known by the
// channel adapter. Unclassified transport failures remain retryable.
type deliveryError struct {
	err       error
	permanent bool
}

func (e *deliveryError) Error() string { return e.err.Error() }
func (e *deliveryError) Unwrap() error { return e.err }

func PermanentDeliveryError(err error) error {
	if err == nil || IsPermanentDeliveryError(err) {
		return err
	}
	return &deliveryError{err: err, permanent: true}
}

func IsPermanentDeliveryError(err error) bool {
	var classified *deliveryError
	return errors.As(err, &classified) && classified.permanent
}

type Logger interface {
	Info(message string, metadata map[string]any)
	Warn(message string, metadata map[string]any)
	Error(message string, metadata map[string]any)
}

type StartDeps struct {
	Gateway       *gwcore.Gateway
	Config        gwconfig.Config
	Logger        Logger
	StateDir      string
	InboundEvents InboundEventStore
}

type InboundEventClaimState string

const (
	InboundEventAcquired  InboundEventClaimState = "acquired"
	InboundEventCompleted InboundEventClaimState = "completed"
	InboundEventBusy      InboundEventClaimState = "busy"
)

type InboundEventClaim struct {
	ChannelKey string
	AccountID  string
	EventID    string
	Token      string
	State      InboundEventClaimState
}

// InboundRunID provides a stable discussion idempotency key for a provider
// event. Provider retries must not create another user message or agent run
// when the first attempt reached Orrery but its platform reply failed.
func InboundRunID(channelKey, accountID, eventID string) string {
	channelKey = strings.ToLower(strings.TrimSpace(channelKey))
	accountID = strings.TrimSpace(accountID)
	eventID = strings.TrimSpace(eventID)
	if channelKey == "" || accountID == "" || eventID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(channelKey + "\x00" + accountID + "\x00" + eventID))
	return "gwin_" + hex.EncodeToString(sum[:16])
}

// InboundEventStore provides durable, lease-based deduplication for platform
// callbacks. Implementations must use the claim token for completion/release
// so an expired worker cannot overwrite a newer claim.
type InboundEventStore interface {
	ClaimInboundEvent(ctx context.Context, channelKey, accountID, eventID string) (InboundEventClaim, error)
	RenewInboundEvent(ctx context.Context, claim InboundEventClaim) error
	CompleteInboundEvent(ctx context.Context, claim InboundEventClaim) error
	ReleaseInboundEvent(ctx context.Context, claim InboundEventClaim) error
}

type InboundJob struct {
	ChannelKey  string
	AccountID   string
	EventID     string
	Payload     []byte
	ClaimToken  string
	Attempts    int
	MaxAttempts int
}

// InboundJobQueue durably accepts provider callbacks before they are
// acknowledged. Workers claim jobs with a fencing token so multiple Gateway
// instances can safely consume the same channel account.
type InboundJobQueue interface {
	EnqueueInboundJob(ctx context.Context, job InboundJob) (bool, error)
	ClaimInboundJob(ctx context.Context, channelKey, accountID string) (InboundJob, bool, error)
	RenewInboundJob(ctx context.Context, job InboundJob) error
	CompleteInboundJob(ctx context.Context, job InboundJob) error
	ReleaseInboundJob(ctx context.Context, job InboundJob) error
	// A zero nextAttemptAt terminates a non-retryable failure immediately.
	FailInboundJob(ctx context.Context, job InboundJob, reason string, nextAttemptAt time.Time) error
}

const (
	inboundEventRenewInterval = time.Minute
	inboundEventRenewTimeout  = 10 * time.Second
)

// MaintainInboundEventClaim renews a durable inbound claim while a discussion
// turn and its platform reply are in flight. A renewal failure cancels the
// returned context so a stale worker cannot keep processing after losing its
// fencing token.
func MaintainInboundEventClaim(ctx context.Context, store InboundEventStore, claim InboundEventClaim) (context.Context, func() error) {
	leaseCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	var stopOnce sync.Once
	var errMu sync.Mutex
	var renewalErr error
	go func() {
		defer close(done)
		ticker := time.NewTicker(inboundEventRenewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-leaseCtx.Done():
				return
			case <-ticker.C:
				renewCtx, renewCancel := context.WithTimeout(leaseCtx, inboundEventRenewTimeout)
				err := store.RenewInboundEvent(renewCtx, claim)
				renewCancel()
				if err == nil {
					continue
				}
				if leaseCtx.Err() != nil {
					return
				}
				errMu.Lock()
				renewalErr = err
				errMu.Unlock()
				cancel()
				return
			}
		}
	}()
	stop := func() error {
		stopOnce.Do(func() {
			cancel()
			<-done
		})
		errMu.Lock()
		defer errMu.Unlock()
		return renewalErr
	}
	return leaseCtx, stop
}

// InboundProcessingError keeps runtime errors in the processing lifecycle.
// Error events must never be acknowledged as successful synthetic chat replies.
func InboundProcessingError(events []protocol.Event) error {
	var retryable error
	for _, event := range events {
		if event.Type != protocol.EventError {
			continue
		}
		code := "gateway_processing_failed"
		switch event.Code {
		case protocol.ErrorCodeGatewayRouteUnavailable, protocol.ErrorCodeGatewayRuntimeUnavailable,
			protocol.ErrorCodeGatewayPermissionDenied, protocol.ErrorCodeGatewayRequestInvalid:
			code = event.Code
		}
		err := &inboundProcessingError{code: code, retryable: event.Recoverable}
		if !err.retryable {
			return err
		}
		retryable = err
	}
	return retryable
}

type inboundProcessingError struct {
	code      string
	retryable bool
}

func (e *inboundProcessingError) Error() string {
	return "gateway inbound processing failed: " + e.code
}

func IsPermanentInboundError(err error) bool {
	var classified *inboundProcessingError
	return errors.As(err, &classified) && !classified.retryable
}

type Handle interface {
	Stop(ctx context.Context, reason string) error
}

// Delivery is a platform-initiated message to a verified channel target.
type Delivery struct {
	ID             string
	ChannelKey     string
	AccountID      string
	RecipientID    string
	ChatID         string
	Title          string
	Text           string
	Format         string
	IdempotencyKey string
}

type DeliveryResult struct {
	ExternalMessageID string
}

type AccountStatus struct {
	ChannelKey string `json:"channel_key"`
	AccountID  string `json:"account_id"`
	Enabled    bool   `json:"enabled"`
	Status     string `json:"status"`
	LastError  string `json:"last_error,omitempty"`
}

type AccountStatusProvider interface {
	GatewayAccountStatuses() []AccountStatus
}

type ConnectionHealthProvider interface {
	GatewayConnectionHealthy() bool
}

type AccountConnectionHealthProvider interface {
	GatewayAccountConnectionHealthy(accountID string) bool
}

type OutboundSender interface {
	Send(ctx context.Context, delivery Delivery) (DeliveryResult, error)
}

type OutboundHandle interface {
	Handle
	OutboundSender
}

type Adapter interface {
	Key() string
	Start(ctx context.Context, deps StartDeps) (Handle, error)
}

type Factory func(gwconfig.PlatformAdapterConfig) Adapter
type FeishuFactory func(gwconfig.FeishuAdapterConfig) Adapter
type WeixinFactory func(gwconfig.WeixinAdapterConfig) Adapter
