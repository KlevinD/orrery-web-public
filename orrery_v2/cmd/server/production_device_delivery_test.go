package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	deviceprovider "orrery_v2/internal/agent/runtime/providers/device"
	deviceidentity "orrery_v2/internal/device/identity"
	"orrery_v2/internal/platform/db"
)

func TestProductionDeviceSessionRunnerBridgesWebSocketToDurableBroker(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	repository, err := deviceprovider.NewSQLRepository(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 26, 13, 0, 0, 0, time.UTC)
	broker, err := deviceprovider.NewDurableBroker(deviceprovider.BrokerOptions{Repository: repository, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	runner := productionDeviceSessionRunner{broker: broker, now: func() time.Time { return now }}
	runnerResult := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		connection, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, request, nil)
		if err != nil {
			runnerResult <- err
			return
		}
		defer connection.Close()
		runnerResult <- runner.RunDeviceSession(request.Context(), connection, deviceidentity.ConnectionIdentity{DeviceID: "device_test_001", TenantID: "global", OwnerID: "user-1", CredentialRevision: 1}, "conn_test_001")
	}))
	defer server.Close()

	connection, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("capabilities"))
	hello, err := deviceprovider.MarshalEnvelope(deviceprovider.Envelope{
		ProtocolVersion: deviceprovider.ProtocolVersion, MessageID: "msg_hello_001", ConnectionEpoch: "conn_test_001",
		Type: deviceprovider.MessageDeviceHello, SentAt: now,
		Payload: deviceprovider.DeviceHelloPayload{
			DeviceID: "device_test_001", ProtocolMin: deviceprovider.ProtocolVersion, ProtocolMax: deviceprovider.ProtocolVersion,
			ClientVersion: "1.0.0", AionCoreVersion: "1.0.0", CapabilityDigest: hex.EncodeToString(digest[:]), ActiveWorkOrders: []deviceprovider.ActiveWorkOrder{},
		},
	}, deviceprovider.DeviceToCloud, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.WriteMessage(websocket.TextMessage, hello); err != nil {
		t.Fatal(err)
	}
	messageType, payload, err := connection.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if messageType != websocket.TextMessage {
		t.Fatalf("message type = %d", messageType)
	}
	welcome, err := deviceprovider.ParseEnvelope(payload, deviceprovider.CloudToDevice, now)
	if err != nil {
		t.Fatal(err)
	}
	if welcome.Type != deviceprovider.MessageDeviceWelcome || welcome.ConnectionEpoch != "conn_test_001" {
		t.Fatalf("welcome = %+v", welcome)
	}
	_ = connection.Close()
	select {
	case err := <-runnerResult:
		if err == nil {
			t.Fatal("session runner returned nil after peer closed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("session runner did not stop after peer closed")
	}
}
