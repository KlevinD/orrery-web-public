package device

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type WorkOrderState string

const (
	WorkOrderOffered              WorkOrderState = "offered"
	WorkOrderAccepted             WorkOrderState = "accepted"
	WorkOrderRunning              WorkOrderState = "running"
	WorkOrderWaitingLocalApproval WorkOrderState = "waiting_local_approval"
	WorkOrderDetached             WorkOrderState = "detached"
	WorkOrderCancelling           WorkOrderState = "cancelling"
	WorkOrderSucceeded            WorkOrderState = "succeeded"
	WorkOrderFailed               WorkOrderState = "failed"
	WorkOrderCancelled            WorkOrderState = "cancelled"
	WorkOrderRejected             WorkOrderState = "rejected"
)

func (s WorkOrderState) Terminal() bool {
	switch s {
	case WorkOrderSucceeded, WorkOrderFailed, WorkOrderCancelled, WorkOrderRejected:
		return true
	default:
		return false
	}
}

type WorkOrderRecord struct {
	TenantID     string
	DeviceID     string
	SessionID    string
	Order        WorkOrder
	Fingerprint  string
	State        WorkOrderState
	LastSequence int64
	Terminal     *WorkOrderTerminalPayload
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type BrokerEventKind string

const (
	BrokerAccepted BrokerEventKind = "accepted"
	BrokerRejected BrokerEventKind = "rejected"
	BrokerFrame    BrokerEventKind = "frame"
	BrokerTerminal BrokerEventKind = "terminal"
	BrokerArtifact BrokerEventKind = "artifact"
)

type BrokerEvent struct {
	Cursor     int64
	MessageID  string
	Kind       BrokerEventKind
	Accepted   *WorkOrderAcceptedPayload
	Rejected   *WorkOrderRejectedPayload
	Frame      *WorkOrderFramePayload
	Terminal   *WorkOrderTerminalPayload
	Artifact   *ArtifactManifestPayload
	OccurredAt time.Time
}

type OfferRequest struct {
	TenantID  string
	DeviceID  string
	SessionID string
	Order     WorkOrder
}

type WorkOrderSubscription interface {
	Events() <-chan BrokerEvent
	Close()
}

// DeviceWorkOrderBroker is the transport-free port consumed by the Device
// SessionBackend. It exposes only WorkOrder semantics, never WebSocket state.
type DeviceWorkOrderBroker interface {
	Offer(context.Context, OfferRequest) (WorkOrderRecord, WorkOrderSubscription, error)
	Cancel(context.Context, string, string, string, string) error
	Subscribe(context.Context, string, string, string, int64) (WorkOrderRecord, WorkOrderSubscription, error)
}

type Repository interface {
	CreateOffer(context.Context, WorkOrderRecord, queuedOutbound) (WorkOrderRecord, bool, error)
	GetWorkOrder(context.Context, string, string, string) (WorkOrderRecord, bool, error)
	ApplyInbound(context.Context, string, string, Envelope) (applyInboundResult, error)
	QueueCancel(context.Context, string, string, string, queuedOutbound) (WorkOrderRecord, error)
	ListEvents(context.Context, string, string, string, int64) ([]BrokerEvent, error)
	ListOutbound(context.Context, string, string) ([]queuedOutbound, error)
	LastCursor(context.Context, string, string) (int64, error)
	Close() error
}

type applyInboundResult struct {
	Duplicate bool
	Record    *WorkOrderRecord
	Event     *BrokerEvent
}

type queuedOutbound struct {
	MessageID   string
	Type        MessageType
	WorkOrderID string
	SentAt      time.Time
	Payload     any
	Ordinal     int64
}

type BrokerOptions struct {
	Repository        Repository
	Now               func() time.Time
	HeartbeatInterval time.Duration
	LeaseTimeout      time.Duration
	RemoteExecution   func(string, string) bool
	SubscriberBuffer  int
	ConnectionBuffer  int
}

var ErrRemoteExecutionDisabled = errors.New("device remote execution is disabled")

type DurableBroker struct {
	repository       Repository
	now              func() time.Time
	heartbeat        time.Duration
	lease            time.Duration
	remoteExecution  func(string, string) bool
	subscriberBuffer int
	connectionBuffer int

	mu             sync.Mutex
	subscribers    map[string]map[uint64]*brokerSubscription
	nextSubID      uint64
	links          map[string]*DeviceLink
	browserPending map[string]*browserPendingCall
	closed         bool
}

type brokerSubscription struct {
	broker *DurableBroker
	key    string
	id     uint64
	events chan BrokerEvent
	once   sync.Once
}

func (s *brokerSubscription) Events() <-chan BrokerEvent { return s.events }
func (s *brokerSubscription) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		s.broker.mu.Lock()
		s.broker.removeSubscriptionLocked(s)
		s.broker.mu.Unlock()
	})
}

func (b *DurableBroker) removeSubscriptionLocked(subscription *brokerSubscription) {
	if subscription == nil {
		return
	}
	subscribers := b.subscribers[subscription.key]
	if subscribers == nil || subscribers[subscription.id] != subscription {
		return
	}
	delete(subscribers, subscription.id)
	close(subscription.events)
	if len(subscribers) == 0 {
		delete(b.subscribers, subscription.key)
	}
}

func NewDurableBroker(options BrokerOptions) (*DurableBroker, error) {
	if options.Repository == nil {
		return nil, errors.New("device work order broker requires a durable repository")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.HeartbeatInterval == 0 {
		options.HeartbeatInterval = 20 * time.Second
	}
	if options.LeaseTimeout == 0 {
		options.LeaseTimeout = 60 * time.Second
	}
	if options.HeartbeatInterval < 5*time.Second || options.HeartbeatInterval > 60*time.Second || options.LeaseTimeout < 15*time.Second || options.LeaseTimeout > 180*time.Second {
		return nil, errors.New("device heartbeat or lease duration is outside protocol limits")
	}
	if options.SubscriberBuffer <= 0 {
		options.SubscriberBuffer = 512
	}
	if options.ConnectionBuffer <= 0 {
		options.ConnectionBuffer = 256
	}
	if options.RemoteExecution == nil {
		options.RemoteExecution = func(string, string) bool { return true }
	}
	return &DurableBroker{
		repository: options.Repository, now: options.Now, heartbeat: options.HeartbeatInterval, lease: options.LeaseTimeout,
		remoteExecution: options.RemoteExecution, subscriberBuffer: options.SubscriberBuffer, connectionBuffer: options.ConnectionBuffer,
		subscribers: map[string]map[uint64]*brokerSubscription{}, links: map[string]*DeviceLink{}, browserPending: map[string]*browserPendingCall{},
	}, nil
}

func (b *DurableBroker) Offer(ctx context.Context, request OfferRequest) (WorkOrderRecord, WorkOrderSubscription, error) {
	if err := validateOfferRequest(request); err != nil {
		return WorkOrderRecord{}, nil, err
	}
	now := b.now().UTC()
	if !request.Order.Deadline.After(now) {
		return WorkOrderRecord{}, nil, &ProtocolError{Code: ProtocolInvalidSchema, Message: "work order deadline has expired"}
	}
	fingerprint, err := workOrderFingerprint(request.Order)
	if err != nil {
		return WorkOrderRecord{}, nil, err
	}
	record := WorkOrderRecord{TenantID: strings.TrimSpace(request.TenantID), DeviceID: request.DeviceID, SessionID: request.SessionID, Order: request.Order, Fingerprint: fingerprint, State: WorkOrderOffered, CreatedAt: now, UpdatedAt: now}
	outbound := queuedOutbound{MessageID: newProtocolID("msg"), Type: MessageWorkOrderOffer, WorkOrderID: request.Order.WorkOrderID, SentAt: now, Payload: request.Order}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return WorkOrderRecord{}, nil, errors.New("device broker is closed")
	}
	if !b.remoteExecution(record.TenantID, record.DeviceID) {
		b.mu.Unlock()
		return WorkOrderRecord{}, nil, ErrRemoteExecutionDisabled
	}
	if record.Order.Task.Kind == "workspace_tool" {
		link := b.links[deviceKey(record.TenantID, record.DeviceID)]
		if link == nil {
			b.mu.Unlock()
			return WorkOrderRecord{}, nil, errors.New("workspace tools require a connected v1 executor")
		}
		link.mu.Lock()
		supported := link.workspaceToolsVersion == 1 && link.welcome && !link.closed
		link.mu.Unlock()
		if !supported {
			b.mu.Unlock()
			return WorkOrderRecord{}, nil, errors.New("workspace tools are unsupported by this client")
		}
	}
	stored, created, err := b.repository.CreateOffer(ctx, record, outbound)
	if err != nil {
		b.mu.Unlock()
		return WorkOrderRecord{}, nil, err
	}
	subscription, err := b.subscribeLocked(ctx, stored, 0)
	if err == nil {
		if created {
			b.deliverOutboundLocked(stored.TenantID, stored.DeviceID, outbound)
		} else if pending, listErr := b.repository.ListOutbound(ctx, stored.TenantID, stored.DeviceID); listErr != nil {
			subscription.once.Do(func() { b.removeSubscriptionLocked(subscription) })
			err = listErr
		} else {
			for _, message := range pending {
				if message.WorkOrderID == stored.Order.WorkOrderID && message.Type == MessageWorkOrderOffer {
					b.deliverOutboundLocked(stored.TenantID, stored.DeviceID, message)
					break
				}
			}
		}
	}
	b.mu.Unlock()
	if err != nil {
		return WorkOrderRecord{}, nil, err
	}
	return stored, subscription, nil
}

func (b *DurableBroker) Subscribe(ctx context.Context, tenantID, deviceID, workOrderID string, afterCursor int64) (WorkOrderRecord, WorkOrderSubscription, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return WorkOrderRecord{}, nil, errors.New("device broker is closed")
	}
	record, found, err := b.repository.GetWorkOrder(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(deviceID), strings.TrimSpace(workOrderID))
	if err != nil {
		return WorkOrderRecord{}, nil, err
	}
	if !found {
		return WorkOrderRecord{}, nil, &ProtocolError{Code: ProtocolDeviceMismatch, Message: "work order is unavailable for device"}
	}
	subscription, err := b.subscribeLocked(ctx, record, afterCursor)
	return record, subscription, err
}

func (b *DurableBroker) subscribeLocked(ctx context.Context, record WorkOrderRecord, afterCursor int64) (*brokerSubscription, error) {
	events, err := b.repository.ListEvents(ctx, record.TenantID, record.DeviceID, record.Order.WorkOrderID, afterCursor)
	if err != nil {
		return nil, err
	}
	capacity := b.subscriberBuffer
	if len(events)+16 > capacity {
		capacity = len(events) + 16
	}
	b.nextSubID++
	key := workOrderKey(record.TenantID, record.DeviceID, record.Order.WorkOrderID)
	subscription := &brokerSubscription{broker: b, key: key, id: b.nextSubID, events: make(chan BrokerEvent, capacity)}
	for _, event := range events {
		subscription.events <- cloneBrokerEvent(event)
	}
	if b.subscribers[key] == nil {
		b.subscribers[key] = map[uint64]*brokerSubscription{}
	}
	b.subscribers[key][subscription.id] = subscription
	return subscription, nil
}

func (b *DurableBroker) Cancel(ctx context.Context, tenantID, deviceID, workOrderID, reason string) error {
	tenantID, deviceID, workOrderID, reason = strings.TrimSpace(tenantID), strings.TrimSpace(deviceID), strings.TrimSpace(workOrderID), sanitizeReason(reason, 512)
	if tenantID == "" || !validIdentifier(deviceID) || !validIdentifier(workOrderID) || reason == "" {
		return &ProtocolError{Code: ProtocolInvalidSchema, Message: "work order cancel identity and reason are required"}
	}
	outbound := queuedOutbound{MessageID: newProtocolID("msg"), Type: MessageWorkOrderCancel, WorkOrderID: workOrderID, SentAt: b.now().UTC(), Payload: WorkOrderCancelPayload{WorkOrderID: workOrderID, Reason: reason}}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return errors.New("device broker is closed")
	}
	record, err := b.repository.QueueCancel(ctx, tenantID, deviceID, workOrderID, outbound)
	if err != nil {
		return err
	}
	if !record.State.Terminal() {
		b.deliverOutboundLocked(tenantID, deviceID, outbound)
	}
	return nil
}

type DeviceLink struct {
	broker           *DurableBroker
	tenantID         string
	deviceID         string
	epoch            string
	ownerID          string
	browserProtocol  string
	browserEnabled   bool
	browserConnected bool
	outbound         chan Envelope

	mu                    sync.Mutex
	welcome               bool
	workspaceToolsVersion int
	closed                bool
	once                  sync.Once

	leaseTimer      *time.Timer
	leaseGeneration uint64
}

func (b *DurableBroker) AttachDevice(tenantID, deviceID, connectionEpoch string) (*DeviceLink, error) {
	return b.AttachOwnedDevice(tenantID, deviceID, "", connectionEpoch)
}

// AttachOwnedDevice accepts identity only from the authenticated gateway, never
// from device.hello or an Agent-supplied argument.
func (b *DurableBroker) AttachOwnedDevice(tenantID, deviceID, ownerID, connectionEpoch string) (*DeviceLink, error) {
	tenantID, deviceID, connectionEpoch = strings.TrimSpace(tenantID), strings.TrimSpace(deviceID), strings.TrimSpace(connectionEpoch)
	if tenantID == "" || !validIdentifier(deviceID) || !validIdentifier(connectionEpoch) {
		return nil, &ProtocolError{Code: ProtocolInvalidSchema, Message: "device link identity is invalid"}
	}
	link := &DeviceLink{broker: b, tenantID: tenantID, deviceID: deviceID, ownerID: strings.TrimSpace(ownerID), epoch: connectionEpoch, outbound: make(chan Envelope, b.connectionBuffer)}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, errors.New("device broker is closed")
	}
	key := deviceKey(tenantID, deviceID)
	if existing := b.links[key]; existing != nil {
		existing.closeLocked()
	}
	b.links[key] = link
	link.armLeaseLocked()
	return link, nil
}

func (b *DurableBroker) DeviceConnected(tenantID, deviceID string) bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	link := b.links[deviceKey(strings.TrimSpace(tenantID), strings.TrimSpace(deviceID))]
	if link == nil {
		return false
	}
	link.mu.Lock()
	defer link.mu.Unlock()
	return link.welcome && !link.closed
}

func (l *DeviceLink) Outbound() <-chan Envelope { return l.outbound }

func (l *DeviceLink) Receive(ctx context.Context, raw []byte) error {
	if l == nil {
		return errors.New("device link is unavailable")
	}
	envelope, err := ParseEnvelope(raw, DeviceToCloud, l.broker.now().UTC())
	if err != nil {
		return err
	}
	if envelope.ConnectionEpoch != l.epoch {
		return &ProtocolError{Code: ProtocolStaleEpoch, Message: "device message belongs to a stale connection epoch"}
	}
	l.mu.Lock()
	closed, welcomed := l.closed, l.welcome
	l.mu.Unlock()
	if closed {
		return errors.New("device link is closed")
	}
	if !welcomed && envelope.Type != MessageDeviceHello {
		return &ProtocolError{Code: ProtocolInvalidSchema, Message: "device.hello is required before other messages"}
	}
	if welcomed && envelope.Type == MessageDeviceHello {
		return &ProtocolError{Code: ProtocolDuplicateConflict, Message: "device.hello was already accepted for this epoch"}
	}

	l.broker.mu.Lock()
	defer l.broker.mu.Unlock()
	if current := l.broker.links[deviceKey(l.tenantID, l.deviceID)]; current != l {
		return &ProtocolError{Code: ProtocolStaleEpoch, Message: "device link was replaced by a newer epoch"}
	}
	if envelope.Type == MessageDeviceHello && envelope.Payload.(DeviceHelloPayload).DeviceID != l.deviceID {
		return &ProtocolError{Code: ProtocolDeviceMismatch, Message: "device.hello identity does not match authenticated device"}
	}
	if isBrowserMessage(envelope.Type) {
		return l.receiveBrowserLocked(envelope)
	}
	result, err := l.broker.repository.ApplyInbound(ctx, l.tenantID, l.deviceID, envelope)
	if err != nil {
		return err
	}
	if result.Duplicate {
		return nil
	}
	if envelope.Type == MessageDeviceHello {
		l.browserProtocol = envelope.Payload.(DeviceHelloPayload).BrowserProtocol
		l.mu.Lock()
		l.welcome = true
		l.workspaceToolsVersion = envelope.Payload.(DeviceHelloPayload).WorkspaceToolsVersion
		l.mu.Unlock()
		l.armLeaseLocked()
		enabled := l.broker.remoteExecution(l.tenantID, l.deviceID)
		if err := l.broker.sendWelcomeLocked(ctx, l, enabled); err != nil {
			return err
		}
		if !enabled {
			return nil
		}
		outbound, err := l.broker.repository.ListOutbound(ctx, l.tenantID, l.deviceID)
		if err != nil {
			return err
		}
		for _, message := range outbound {
			if err := l.broker.sendOutboundLocked(l, message); err != nil {
				return err
			}
		}
		return nil
	}
	if envelope.Type == MessageDeviceHeartbeat {
		l.armLeaseLocked()
	}
	if result.Event != nil && result.Record != nil {
		l.broker.publishLocked(*result.Record, *result.Event)
	}
	return nil
}

func (l *DeviceLink) Close() {
	if l == nil {
		return
	}
	l.once.Do(func() { l.broker.mu.Lock(); defer l.broker.mu.Unlock(); l.closeLocked() })
}
func (l *DeviceLink) closeLocked() {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.closed = true
	l.leaseGeneration++
	if l.leaseTimer != nil {
		l.leaseTimer.Stop()
		l.leaseTimer = nil
	}
	close(l.outbound)
	l.mu.Unlock()
	if l.broker.links[deviceKey(l.tenantID, l.deviceID)] == l {
		delete(l.broker.links, deviceKey(l.tenantID, l.deviceID))
	}
	l.failBrowserCallsLocked("DEVICE_DISCONNECTED")
}

func (l *DeviceLink) armLeaseLocked() {
	if l.closed {
		return
	}
	l.leaseGeneration++
	generation := l.leaseGeneration
	if l.leaseTimer != nil {
		l.leaseTimer.Stop()
	}
	l.leaseTimer = time.AfterFunc(l.broker.lease, func() { l.expireLease(generation) })
}

func (l *DeviceLink) expireLease(generation uint64) {
	l.broker.mu.Lock()
	defer l.broker.mu.Unlock()
	if l.leaseGeneration != generation || l.broker.links[deviceKey(l.tenantID, l.deviceID)] != l {
		return
	}
	l.closeLocked()
}

func (b *DurableBroker) sendWelcomeLocked(ctx context.Context, link *DeviceLink, enabled bool) error {
	cursor, err := b.repository.LastCursor(ctx, link.tenantID, link.deviceID)
	if err != nil {
		return err
	}
	payload := DeviceWelcomePayload{SelectedProtocolVersion: ProtocolVersion, ServerTime: b.now().UTC(), ReplayCursor: fmt.Sprintf("cursor_%d", cursor), HeartbeatIntervalMS: b.heartbeat.Milliseconds(), LeaseTimeoutMS: b.lease.Milliseconds(), RemoteExecutionEnabled: &enabled}
	payload.BrowserProtocol = link.browserProtocol
	return b.sendEnvelopeLocked(link, Envelope{ProtocolVersion: ProtocolVersion, MessageID: newProtocolID("msg"), ConnectionEpoch: link.epoch, Type: MessageDeviceWelcome, SentAt: b.now().UTC(), Payload: payload})
}

func (b *DurableBroker) deliverOutboundLocked(tenantID, deviceID string, message queuedOutbound) {
	if message.Type == MessageWorkOrderOffer && !b.remoteExecution(tenantID, deviceID) {
		return
	}
	link := b.links[deviceKey(tenantID, deviceID)]
	if link == nil {
		return
	}
	link.mu.Lock()
	welcomed, closed := link.welcome, link.closed
	link.mu.Unlock()
	if !welcomed || closed {
		return
	}
	_ = b.sendOutboundLocked(link, message)
}

func (b *DurableBroker) sendOutboundLocked(link *DeviceLink, message queuedOutbound) error {
	if message.Type == MessageWorkOrderOffer {
		raw, _ := json.Marshal(message.Payload)
		var order WorkOrder
		if json.Unmarshal(raw, &order) == nil && order.Task.Kind == "workspace_tool" {
			link.mu.Lock()
			supported := link.workspaceToolsVersion == 1
			link.mu.Unlock()
			if !supported {
				return errors.New("workspace tools are unsupported by this client")
			}
		}
	}
	return b.sendEnvelopeLocked(link, Envelope{ProtocolVersion: ProtocolVersion, MessageID: message.MessageID, ConnectionEpoch: link.epoch, Type: message.Type, SentAt: message.SentAt, Payload: message.Payload})
}

func (b *DurableBroker) sendEnvelopeLocked(link *DeviceLink, envelope Envelope) error {
	if _, err := MarshalEnvelope(envelope, CloudToDevice, b.now().UTC()); err != nil {
		return err
	}
	select {
	case link.outbound <- envelope:
		return nil
	default:
		link.closeLocked()
		return errors.New("device connection outbound buffer is full")
	}
}

func (b *DurableBroker) publishLocked(record WorkOrderRecord, event BrokerEvent) {
	key := workOrderKey(record.TenantID, record.DeviceID, record.Order.WorkOrderID)
	for _, subscriber := range b.subscribers[key] {
		select {
		case subscriber.events <- cloneBrokerEvent(event):
		default:
			delete(b.subscribers[key], subscriber.id)
			close(subscriber.events)
		}
	}
	if len(b.subscribers[key]) == 0 {
		delete(b.subscribers, key)
	}
}

func (b *DurableBroker) Close() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	for _, link := range b.links {
		link.closeLocked()
	}
	for key, subscribers := range b.subscribers {
		for _, subscriber := range subscribers {
			close(subscriber.events)
		}
		delete(b.subscribers, key)
	}
	b.mu.Unlock()
	return b.repository.Close()
}

func validateOfferRequest(request OfferRequest) error {
	if strings.TrimSpace(request.TenantID) == "" || !validIdentifier(request.DeviceID) || strings.TrimSpace(request.SessionID) == "" {
		return &ProtocolError{Code: ProtocolInvalidSchema, Message: "work order tenant, device, and session identity are required"}
	}
	return validateWorkOrder(request.Order)
}

func workOrderFingerprint(order WorkOrder) (string, error) {
	normalized := order
	normalized.RequiredCapabilities = append([]Capability(nil), order.RequiredCapabilities...)
	normalized.RequestedArtifactClasses = append([]ArtifactClass(nil), order.RequestedArtifactClasses...)
	// Protocol order is already significant on the wire; stable sorting here makes
	// retries with equivalent set order idempotent.
	sortCapabilities(normalized.RequiredCapabilities)
	sortArtifactClasses(normalized.RequestedArtifactClasses)
	data, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func sortCapabilities(values []Capability) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
func sortArtifactClasses(values []ArtifactClass) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func newProtocolID(prefix string) string {
	return prefix + "_" + strings.ReplaceAll(uuid.NewString(), "-", "")
}
func workOrderKey(tenantID, deviceID, workOrderID string) string {
	return tenantID + "\x00" + deviceID + "\x00" + workOrderID
}
func deviceKey(tenantID, deviceID string) string { return tenantID + "\x00" + deviceID }
func sanitizeReason(value string, limit int) string {
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "\r", " "), "\n", " "))
	if len(value) > limit {
		value = value[:limit]
	}
	return value
}

func cloneBrokerEvent(event BrokerEvent) BrokerEvent {
	out := event
	if event.Accepted != nil {
		value := *event.Accepted
		out.Accepted = &value
	}
	if event.Rejected != nil {
		value := *event.Rejected
		out.Rejected = &value
	}
	if event.Frame != nil {
		value := *event.Frame
		out.Frame = &value
	}
	if event.Terminal != nil {
		value := *event.Terminal
		out.Terminal = &value
	}
	if event.Artifact != nil {
		value := *event.Artifact
		out.Artifact = &value
	}
	return out
}

var _ DeviceWorkOrderBroker = (*DurableBroker)(nil)
