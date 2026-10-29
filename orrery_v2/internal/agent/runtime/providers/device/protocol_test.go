package device

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProtocolMatchesDesktopSharedFixtures(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	root := filepath.Join("testdata", "orrery-device-v1")
	desktopRoot := filepath.Join("..", "..", "..", "..", "..", "..", "orrery-desktop", "tests", "fixtures", "orrery-device-v1")
	for _, fixture := range []struct {
		name      string
		direction Direction
	}{
		{name: "valid-outbound.json", direction: DeviceToCloud},
		{name: "valid-inbound.json", direction: CloudToDevice},
	} {
		body, err := os.ReadFile(filepath.Join(root, fixture.name))
		if err != nil {
			t.Fatal(err)
		}
		if desktopBody, err := os.ReadFile(filepath.Join(desktopRoot, fixture.name)); err == nil {
			if !bytes.Equal(bytes.ReplaceAll(body, []byte("\r\n"), []byte("\n")), bytes.ReplaceAll(desktopBody, []byte("\r\n"), []byte("\n"))) {
				t.Fatalf("%s differs from the desktop shared fixture", fixture.name)
			}
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		var frames []json.RawMessage
		if err := json.Unmarshal(body, &frames); err != nil {
			t.Fatal(err)
		}
		for index, frame := range frames {
			envelope, err := ParseEnvelope(frame, fixture.direction, now)
			if err != nil {
				t.Fatalf("%s[%d]: %v", fixture.name, index, err)
			}
			encoded, err := MarshalEnvelope(envelope, fixture.direction, now)
			if err != nil {
				t.Fatalf("marshal %s[%d]: %v", fixture.name, index, err)
			}
			if _, err := ParseEnvelope(encoded, fixture.direction, now); err != nil {
				t.Fatalf("round trip %s[%d]: %v", fixture.name, index, err)
			}
		}
	}
}

func TestProtocolRejectsUnknownOversizedProhibitedAndWrongDirection(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	base := `{"protocol_version":"1.0","message_id":"msg_test_001","connection_epoch":"conn_test_001","type":"%s","sent_at":"2026-07-26T12:00:00Z","payload":%s}`
	tests := []struct {
		name      string
		raw       string
		direction Direction
		code      ProtocolErrorCode
	}{
		{name: "unknown", raw: fmtJSON(base, "future.message", `{}`), direction: DeviceToCloud, code: ProtocolUnknownType},
		{name: "wrong direction", raw: fmtJSON(base, "device.welcome", `{"selected_protocol_version":"1.0","server_time":"2026-07-26T12:00:00Z","replay_cursor":"cursor_1","heartbeat_interval_ms":20000,"lease_timeout_ms":60000}`), direction: DeviceToCloud, code: ProtocolWrongDirection},
		{name: "prohibited nested key", raw: fmtJSON(base, "device.heartbeat", `{"active_work_orders":[],"last_replay_cursor":"cursor_1","nested":{"argv":["secret"]}}`), direction: DeviceToCloud, code: ProtocolProhibitedField},
		{name: "unknown field", raw: fmtJSON(base, "device.heartbeat", `{"active_work_orders":[],"last_replay_cursor":"cursor_1","extra":true}`), direction: DeviceToCloud, code: ProtocolInvalidSchema},
		{name: "future timestamp", raw: strings.Replace(fmtJSON(base, "device.heartbeat", `{"active_work_orders":[],"last_replay_cursor":"cursor_1"}`), "2026-07-26T12:00:00Z", "2026-07-26T12:05:01Z", 1), direction: DeviceToCloud, code: ProtocolFutureTimestamp},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseEnvelope([]byte(test.raw), test.direction, now)
			if !IsProtocolError(err, test.code) {
				t.Fatalf("error = %v, want %s", err, test.code)
			}
		})
	}
	oversized := make([]byte, ProtocolMaxFrameBytes+1)
	if _, err := ParseEnvelope(oversized, DeviceToCloud, now); !IsProtocolError(err, ProtocolFrameTooLarge) {
		t.Fatalf("oversized error = %v", err)
	}
}

func TestProtocolRejectsNonUTCPayloadTimestamps(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	base := `{"protocol_version":"1.0","message_id":"msg_test_001","connection_epoch":"conn_test_001","type":"%s","sent_at":"2026-07-26T12:00:00Z","payload":%s}`
	tests := []struct {
		name      string
		kind      string
		payload   string
		direction Direction
	}{
		{
			name:      "welcome server time",
			kind:      "device.welcome",
			payload:   `{"selected_protocol_version":"1.0","server_time":"2026-07-26T20:00:00+08:00","replay_cursor":"cursor_1","heartbeat_interval_ms":20000,"lease_timeout_ms":60000}`,
			direction: CloudToDevice,
		},
		{
			name:      "offer deadline",
			kind:      "work_order.offer",
			payload:   `{"work_order_id":"wo_test_001","idempotency_key":"idem-test-001","project_id":"project_test_001","origin_actor_id":"actor_test_001","task":{"kind":"agent_turn","instruction":"Implement the change"},"workspace_grant_id":"grant_test_001","required_capabilities":[],"requested_artifact_classes":[],"deadline":"2026-07-26T20:30:00+08:00"}`,
			direction: CloudToDevice,
		},
		{
			name:      "accepted at",
			kind:      "work_order.accepted",
			payload:   `{"work_order_id":"wo_test_001","accepted_at":"2026-07-26T20:00:00+08:00"}`,
			direction: DeviceToCloud,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseEnvelope([]byte(fmtJSON(base, test.kind, test.payload)), test.direction, now)
			if !IsProtocolError(err, ProtocolInvalidSchema) {
				t.Fatalf("error = %v, want %s", err, ProtocolInvalidSchema)
			}
		})
	}
}

func fmtJSON(format, kind, payload string) string {
	return strings.Replace(strings.Replace(format, "%s", kind, 1), "%s", payload, 1)
}
