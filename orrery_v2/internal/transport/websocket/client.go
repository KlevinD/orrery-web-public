package websockettransport

import (
	"sync"
	"time"
)

type Client struct {
	id       string
	viewerID string
	send     chan []byte

	mu            sync.RWMutex
	subscriptions map[string]string
	lastHeartbeat time.Time
	state         ClientState
}

func NewClient(id, viewerID string) *Client {
	return &Client{
		id:            id,
		viewerID:      viewerID,
		subscriptions: map[string]string{},
		lastHeartbeat: time.Now().UTC(),
	}
}

func (c *Client) ID() string {
	return c.id
}

func (c *Client) State() ClientState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

func (c *Client) RecordSent(version int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if version > c.state.LastSentVersion {
		c.state.LastSentVersion = version
	}
	c.state.QueueDepth++
}

func (c *Client) Ack(version int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if version > c.state.LastAckedVersion {
		c.state.LastAckedVersion = version
	}
	if c.state.QueueDepth > 0 {
		c.state.QueueDepth--
	}
	c.lastHeartbeat = time.Now().UTC()
}

func (c *Client) heartbeatExpired(timeout time.Duration, now time.Time) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return timeout > 0 && now.Sub(c.lastHeartbeat) > timeout
}
