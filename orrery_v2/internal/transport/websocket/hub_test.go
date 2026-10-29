package websockettransport

import (
	"encoding/json"
	"testing"
	"time"
)

func TestHubRegistersClientAndEvictsSlowQueue(t *testing.T) {
	hub := NewHub(HubOptions{SendBuffer: 1, HeartbeatTimeout: time.Minute})
	client := NewClient("client-1", "agent-1")

	if err := hub.Subscribe(client, "sub-1", "mission-1"); err != nil {
		t.Fatalf("Subscribe returned error: %v", err)
	}
	if hub.ConnectionCount("mission-1") != 1 {
		t.Fatalf("connection count = %d", hub.ConnectionCount("mission-1"))
	}
	hub.Publish("mission-1", []byte(`{"type":"patch","version":1}`))
	hub.Publish("mission-1", []byte(`{"type":"patch","version":2}`))

	if hub.ConnectionCount("mission-1") != 0 {
		t.Fatalf("slow client should be evicted, count = %d", hub.ConnectionCount("mission-1"))
	}
}

func TestHubAckUpdatesClientState(t *testing.T) {
	hub := NewHub(HubOptions{SendBuffer: 4, HeartbeatTimeout: time.Minute})
	client := NewClient("client-1", "agent-1")
	if err := hub.Subscribe(client, "sub-1", "mission-1"); err != nil {
		t.Fatalf("Subscribe returned error: %v", err)
	}
	client.RecordSent(5)

	if err := hub.Ack("client-1", "sub-1", 5); err != nil {
		t.Fatalf("Ack returned error: %v", err)
	}
	if client.State().LastAckedVersion != 5 {
		t.Fatalf("client state = %+v", client.State())
	}
}

func TestHubHeartbeatTimeoutReturnsCompleteFrame(t *testing.T) {
	hub := NewHub(HubOptions{SendBuffer: 4, HeartbeatTimeout: time.Nanosecond})
	client := NewClient("client-1", "agent-1")
	if err := hub.Subscribe(client, "sub-1", "mission-1"); err != nil {
		t.Fatalf("Subscribe returned error: %v", err)
	}
	time.Sleep(time.Millisecond)

	frame, expired := hub.CheckHeartbeat("client-1")

	if !expired {
		t.Fatal("client should be expired")
	}
	var envelope ServerEnvelope
	if err := json.Unmarshal(frame, &envelope); err != nil {
		t.Fatalf("unmarshal complete frame: %v", err)
	}
	if envelope.Type != MessageComplete {
		t.Fatalf("envelope = %+v, want complete", envelope)
	}
}
