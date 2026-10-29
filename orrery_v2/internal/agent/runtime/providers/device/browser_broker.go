package device

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	devicebrowser "orrery_v2/internal/device/browser"
)

type BrowserOwnerScope struct{ TenantID, OwnerID string }
type BrowserDeviceView struct {
	DeviceID  string `json:"device_id"`
	Enabled   bool   `json:"enabled"`
	Connected bool   `json:"connected"`
}
type browserPendingCall struct {
	link      *DeviceLink
	operation string
	result    chan BrowserResultPayload
}

// BrowserDevices reveals only browser-capable sockets owned by this principal.
// This is availability, not a grant or proof of business completion.
func (b *DurableBroker) BrowserDevices(scope BrowserOwnerScope) []BrowserDeviceView {
	views := []BrowserDeviceView{}
	if b == nil || strings.TrimSpace(scope.TenantID) == "" || strings.TrimSpace(scope.OwnerID) == "" {
		return views
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, link := range b.links {
		if link.tenantID == scope.TenantID && link.ownerID == scope.OwnerID && link.welcome && !link.closed && link.browserProtocol == devicebrowser.Version {
			views = append(views, BrowserDeviceView{DeviceID: link.deviceID, Enabled: link.browserEnabled, Connected: link.browserConnected && b.remoteExecution(link.tenantID, link.deviceID)})
		}
	}
	sort.Slice(views, func(i, j int) bool { return views[i].DeviceID < views[j].DeviceID })
	return views
}

// CallBrowser is an online-only, bounded operation. It never persists or
// replays a browser action after disconnect, restart, or deadline expiry.
func (b *DurableBroker) CallBrowser(ctx context.Context, scope BrowserOwnerScope, deviceID string, request BrowserRequestPayload) (BrowserResultPayload, error) {
	if err := ctx.Err(); err != nil {
		return BrowserResultPayload{}, err
	}
	if b == nil || strings.TrimSpace(scope.TenantID) == "" || strings.TrimSpace(scope.OwnerID) == "" {
		return BrowserResultPayload{}, &BrowserError{Code: "PERMISSION_DENIED", Message: "Browser owner authority is required"}
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return BrowserResultPayload{}, err
	}
	validated, err := decodeBrowserPayload(MessageBrowserRequest, raw)
	if err != nil {
		return BrowserResultPayload{}, err
	}
	request = validated.(BrowserRequestPayload)
	now := b.now().UTC()
	if !request.Deadline.After(now) || request.Deadline.After(now.Add(45*time.Second)) {
		return BrowserResultPayload{}, &BrowserError{Code: "DEADLINE_EXCEEDED", Message: "Browser deadline must be within 45 seconds"}
	}
	b.mu.Lock()
	link := b.links[deviceKey(scope.TenantID, deviceID)]
	if b.closed || link == nil || link.ownerID != scope.OwnerID || !link.welcome || link.closed || link.browserProtocol != devicebrowser.Version || !link.browserEnabled || !link.browserConnected || !b.remoteExecution(scope.TenantID, deviceID) {
		b.mu.Unlock()
		return BrowserResultPayload{}, &BrowserError{Code: "BROWSER_UNAVAILABLE", Message: "Connect your desktop and grant a tab in Orrery Browser"}
	}
	count := 0
	for _, pending := range b.browserPending {
		if pending.link == link {
			count++
		}
	}
	if count >= 12 || len(b.browserPending) >= 512 {
		b.mu.Unlock()
		return BrowserResultPayload{}, &BrowserError{Code: "BUSY", Message: "Browser operation queue is full"}
	}
	if _, exists := b.browserPending[request.RequestID]; exists {
		b.mu.Unlock()
		return BrowserResultPayload{}, &BrowserError{Code: "DUPLICATE_CONFLICT", Message: "Browser request ID is already in flight"}
	}
	pending := &browserPendingCall{link: link, operation: request.Operation, result: make(chan BrowserResultPayload, 1)}
	b.browserPending[request.RequestID] = pending
	err = b.sendEnvelopeLocked(link, Envelope{ProtocolVersion: ProtocolVersion, MessageID: newProtocolID("msg"), ConnectionEpoch: link.epoch, Type: MessageBrowserRequest, SentAt: now, Payload: request})
	if err != nil {
		delete(b.browserPending, request.RequestID)
		b.mu.Unlock()
		return BrowserResultPayload{}, &BrowserError{Code: "BROWSER_UNAVAILABLE", Message: "Browser request could not be queued"}
	}
	b.mu.Unlock()
	timer := time.NewTimer(request.Deadline.Sub(now))
	defer timer.Stop()
	select {
	case result := <-pending.result:
		return result, nil
	case <-ctx.Done():
	case <-timer.C:
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if current := b.browserPending[request.RequestID]; current == pending {
		delete(b.browserPending, request.RequestID)
		if b.links[deviceKey(link.tenantID, link.deviceID)] == link {
			_ = b.sendEnvelopeLocked(link, Envelope{ProtocolVersion: ProtocolVersion, MessageID: newProtocolID("msg"), ConnectionEpoch: link.epoch, Type: MessageBrowserCancel, SentAt: b.now().UTC(), Payload: BrowserCancelPayload{RequestID: request.RequestID}})
		}
	} else {
		select {
		case result := <-pending.result:
			return result, nil
		default:
		}
	}
	code := "CANCELLED"
	if ctx.Err() == nil {
		code = "DEADLINE_EXCEEDED"
	}
	if devicebrowser.IsMutation(request.Operation) {
		code = "OUTCOME_UNKNOWN"
	}
	return BrowserResultPayload{RequestID: request.RequestID, Error: &BrowserError{Code: code, Message: "Browser request ended; verify the page before repeating an action"}}, nil
}

func (l *DeviceLink) receiveBrowserLocked(envelope Envelope) error {
	if l.browserProtocol != devicebrowser.Version || l.ownerID == "" {
		return &ProtocolError{Code: ProtocolInvalidSchema, Message: "browser protocol was not negotiated"}
	}
	switch envelope.Type {
	case MessageBrowserState:
		state := envelope.Payload.(BrowserStatePayload)
		l.browserEnabled, l.browserConnected = *state.Enabled, *state.Connected
		if !l.browserEnabled || !l.browserConnected {
			l.failBrowserCallsLocked("BROWSER_DISCONNECTED")
		}
	case MessageBrowserResult:
		result := envelope.Payload.(BrowserResultPayload)
		pending := l.broker.browserPending[result.RequestID]
		if pending != nil && pending.link == l {
			delete(l.broker.browserPending, result.RequestID)
			pending.result <- result
		}
	default:
		return &ProtocolError{Code: ProtocolWrongDirection, Message: "unexpected browser message"}
	}
	return nil
}

func (l *DeviceLink) failBrowserCallsLocked(code string) {
	for id, pending := range l.broker.browserPending {
		if pending.link != l {
			continue
		}
		failureCode := code
		if devicebrowser.IsMutation(pending.operation) {
			failureCode = "OUTCOME_UNKNOWN"
		}
		delete(l.broker.browserPending, id)
		pending.result <- BrowserResultPayload{RequestID: id, Error: &BrowserError{Code: failureCode, Message: "Browser connection ended; no automatic replay was performed"}}
	}
}

// RevokeDeviceConnection immediately fences all in-flight browser operations.
// Device IDs are globally unique in the identity store.
func (b *DurableBroker) RevokeDeviceConnection(deviceID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, link := range b.links {
		if link.deviceID == deviceID {
			link.closeLocked()
		}
	}
}
