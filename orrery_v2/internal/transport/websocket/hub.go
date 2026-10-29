package websockettransport

import (
	"encoding/json"
	"errors"
	"sync"
	"time"
)

type HubOptions struct {
	SendBuffer       int
	HeartbeatTimeout time.Duration
}

type Hub struct {
	mu        sync.RWMutex
	clients   map[string]*Client
	byMission map[string]map[string]*Client
	options   HubOptions
}

func NewHub(options HubOptions) *Hub {
	if options.SendBuffer <= 0 {
		options.SendBuffer = 16
	}
	if options.HeartbeatTimeout <= 0 {
		options.HeartbeatTimeout = time.Minute
	}
	return &Hub{
		clients:   map[string]*Client{},
		byMission: map[string]map[string]*Client{},
		options:   options,
	}
}

func (h *Hub) Subscribe(client *Client, subscriptionID, missionID string) error {
	if client == nil || client.id == "" || subscriptionID == "" || missionID == "" {
		return errors.New("client, subscription_id, and mission_id are required")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if client.send == nil {
		client.send = make(chan []byte, h.options.SendBuffer)
	}
	client.subscriptions[subscriptionID] = missionID
	h.clients[client.id] = client
	if h.byMission[missionID] == nil {
		h.byMission[missionID] = map[string]*Client{}
	}
	h.byMission[missionID][client.id] = client
	return nil
}

func (h *Hub) Publish(missionID string, frame []byte) {
	h.mu.RLock()
	clients := make([]*Client, 0, len(h.byMission[missionID]))
	for _, client := range h.byMission[missionID] {
		clients = append(clients, client)
	}
	h.mu.RUnlock()
	for _, client := range clients {
		select {
		case client.send <- append([]byte(nil), frame...):
			client.RecordSent(versionFromFrame(frame))
		default:
			h.closeClient(client.id, CloseReasonSlowClient)
		}
	}
}

func (h *Hub) Ack(clientID, subscriptionID string, version int) error {
	h.mu.RLock()
	client := h.clients[clientID]
	h.mu.RUnlock()
	if client == nil {
		return errors.New("client not found")
	}
	client.mu.RLock()
	_, ok := client.subscriptions[subscriptionID]
	client.mu.RUnlock()
	if !ok {
		return errors.New("subscription not found")
	}
	client.Ack(version)
	return nil
}

func (h *Hub) ConnectionCount(missionID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.byMission[missionID])
}

func (h *Hub) closeClient(clientID string, reason CloseReason) {
	h.mu.Lock()
	client := h.clients[clientID]
	if client == nil {
		h.mu.Unlock()
		return
	}
	delete(h.clients, clientID)
	for _, clients := range h.byMission {
		delete(clients, clientID)
	}
	if client.send != nil {
		close(client.send)
	}
	h.mu.Unlock()
	_ = reason
}

func versionFromFrame(frame []byte) int {
	var envelope ServerEnvelope
	if err := json.Unmarshal(frame, &envelope); err != nil {
		return 0
	}
	return envelope.Version
}
