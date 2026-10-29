package websockettransport

import (
	"encoding/json"
	"time"
)

func (h *Hub) CheckHeartbeat(clientID string) ([]byte, bool) {
	h.mu.RLock()
	client := h.clients[clientID]
	h.mu.RUnlock()
	if client == nil || !client.heartbeatExpired(h.options.HeartbeatTimeout, time.Now().UTC()) {
		return nil, false
	}
	frame, _ := json.Marshal(ServerEnvelope{
		Type:       MessageComplete,
		Payload:    map[string]string{"reason": string(CloseReasonHeartbeatTimeout)},
		ServerTime: time.Now().UTC().Format(time.RFC3339),
	})
	h.closeClient(clientID, CloseReasonHeartbeatTimeout)
	return frame, true
}
