package device

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"orrery_v2/internal/platform/testkit"
)

func newBrowserBroker(t *testing.T) *DurableBroker {
	t.Helper()
	repo, err := NewSQLRepository(context.Background(), testkit.NewTestDB(t))
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewDurableBroker(BrokerOptions{Repository: repo})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return b
}

func attachBrowser(t *testing.T, b *DurableBroker, tenant, owner, deviceID, epoch string) *DeviceLink {
	t.Helper()
	l, err := b.AttachOwnedDevice(tenant, deviceID, owner, epoch)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	sendDeviceEnvelope(t, l, now, "msg_hello_"+epoch, MessageDeviceHello, DeviceHelloPayload{DeviceID: deviceID, ProtocolMin: ProtocolVersion, ProtocolMax: ProtocolVersion, ClientVersion: "0.1.0", AionCoreVersion: "0.1.52", CapabilityDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ActiveWorkOrders: []ActiveWorkOrder{}, BrowserProtocol: "1"})
	if w := receiveOutbound(t, l); w.Payload.(DeviceWelcomePayload).BrowserProtocol != "1" {
		t.Fatal("browser negotiation missing")
	}
	ready := true
	sendDeviceEnvelope(t, l, now, "msg_state_"+epoch, MessageBrowserState, BrowserStatePayload{Enabled: &ready, Connected: &ready})
	return l
}

func browserRequest(id, operation string) BrowserRequestPayload {
	args := map[string]any{}
	if operation == "fill" {
		args = map[string]any{"tabId": 7, "snapshotId": "snapshot-123", "ref": "e1", "operationId": "operation-123", "text": "backend engineer"}
	}
	return BrowserRequestPayload{RequestID: id, Operation: operation, Arguments: args, Deadline: time.Now().UTC().Add(5 * time.Second), TraceID: "trace-123"}
}

func TestPersonalBrowserBrokerOwnerIsolationAndDirectResults(t *testing.T) {
	b := newBrowserBroker(t)
	l := attachBrowser(t, b, "tenant-a", "owner-a", "device_browser_a", "epoch_browser_a")
	attachBrowser(t, b, "tenant-a", "owner-b", "device_browser_b", "epoch_browser_b")
	attachBrowser(t, b, "tenant-b", "owner-a", "device_browser_c", "epoch_browser_c")
	scope := BrowserOwnerScope{TenantID: "tenant-a", OwnerID: "owner-a"}
	if devices := b.BrowserDevices(scope); len(devices) != 1 || devices[0].DeviceID != "device_browser_a" {
		t.Fatalf("ownership leak: %+v", devices)
	}
	for _, id := range []string{"device_browser_b", "device_browser_c"} {
		if _, err := b.CallBrowser(context.Background(), scope, id, browserRequest("br_isolation_123", "tabs")); err == nil {
			t.Fatalf("foreign device %s accepted", id)
		}
	}
	done := make(chan BrowserResultPayload, 1)
	go func() {
		result, err := b.CallBrowser(context.Background(), scope, "device_browser_a", browserRequest("br_result_123", "tabs"))
		if err != nil {
			done <- BrowserResultPayload{Error: &BrowserError{Code: "TEST_ERROR", Message: err.Error()}}
		} else {
			done <- result
		}
	}()
	request := receiveOutbound(t, l)
	if request.Type != MessageBrowserRequest {
		t.Fatalf("not fixed request: %+v", request)
	}
	// DOM data may contain protocol-like keys; they cannot become an executable command.
	sendDeviceEnvelope(t, l, time.Now().UTC(), "msg_browser_result_123", MessageBrowserResult, BrowserResultPayload{RequestID: "br_result_123", Result: json.RawMessage(`{"tabs":[{"title":"jobs","method":"POST","path":"/jobs"}]}`)})
	select {
	case result := <-done:
		if result.Error != nil || len(result.Result) == 0 {
			t.Fatalf("result: %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("result not delivered")
	}
	if len(b.browserPending) != 0 {
		t.Fatal("pending call retained")
	}
}

func TestPersonalBrowserBrokerCancellationDisconnectAndNoReplay(t *testing.T) {
	for _, mode := range []string{"cancel", "replace", "revoke", "disable", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			b := newBrowserBroker(t)
			l := attachBrowser(t, b, "tenant-a", "owner-a", "device_browser_a", "epoch_browser_a")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan BrowserResultPayload, 1)
			request := browserRequest("br_mutation_123", "fill")
			if mode == "timeout" {
				request.Deadline = time.Now().UTC().Add(100 * time.Millisecond)
			}
			go func() {
				r, err := b.CallBrowser(ctx, BrowserOwnerScope{"tenant-a", "owner-a"}, "device_browser_a", request)
				if err != nil {
					r.Error = &BrowserError{Code: "TEST_ERROR", Message: err.Error()}
				}
				done <- r
			}()
			if receiveOutbound(t, l).Type != MessageBrowserRequest {
				t.Fatal("missing request")
			}
			var replacement *DeviceLink
			switch mode {
			case "cancel":
				cancel()
			case "replace":
				replacement = attachBrowser(t, b, "tenant-a", "owner-a", "device_browser_a", "epoch_browser_new")
			case "revoke":
				b.RevokeDeviceConnection("device_browser_a")
			case "disable":
				off := false
				sendDeviceEnvelope(t, l, time.Now().UTC(), "msg_browser_disabled", MessageBrowserState, BrowserStatePayload{Enabled: &off, Connected: &off})
			}
			select {
			case result := <-done:
				if result.Error == nil || result.Error.Code != "OUTCOME_UNKNOWN" {
					t.Fatalf("mutation uncertainty lost: %+v", result)
				}
			case <-time.After(time.Second):
				t.Fatal("pending mutation not fenced")
			}
			if mode == "cancel" || mode == "timeout" {
				if e := receiveOutbound(t, l); e.Type != MessageBrowserCancel {
					t.Fatalf("no cancellation: %+v", e)
				}
			}
			if replacement != nil {
				select {
				case e := <-replacement.Outbound():
					t.Fatalf("browser action replayed: %+v", e)
				default:
				}
			}
			b.mu.Lock()
			remaining := len(b.browserPending)
			b.mu.Unlock()
			if remaining != 0 {
				t.Fatal("pending call leaked")
			}
		})
	}
}

func TestPersonalBrowserProtocolStrictDirectionsAndState(t *testing.T) {
	for _, raw := range []string{
		`{"enabled":false,"connected":true}`, `{"enabled":true}`, `{"enabled":true,"connected":true,"owner":"spoofed"}`,
	} {
		if _, err := decodeBrowserPayload(MessageBrowserState, json.RawMessage(raw)); err == nil {
			t.Fatalf("invalid state accepted: %s", raw)
		}
	}
	for _, raw := range []string{`{"request_id":"br_result_123"}`, `{"request_id":"br_result_123","result":{},"error":{"code":"ERROR","message":"x"}}`, `{"request_id":"br_result_123","result":null}`} {
		if _, err := decodeBrowserPayload(MessageBrowserResult, json.RawMessage(raw)); err == nil {
			t.Fatalf("invalid result accepted: %s", raw)
		}
	}
	b := newBrowserBroker(t)
	old := attachWelcomedDevice(t, b, "tenant-a", "device_old_client", "epoch_old_client", time.Now().UTC())
	on := true
	if err := receiveDeviceEnvelope(old, time.Now().UTC(), "msg_unnegotiated", MessageBrowserState, BrowserStatePayload{Enabled: &on, Connected: &on}); err == nil {
		t.Fatal("old client gained browser channel")
	}
	if len(b.BrowserDevices(BrowserOwnerScope{"tenant-a", "owner-a"})) != 0 {
		t.Fatal("old unowned device exposed")
	}
}
