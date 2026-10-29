package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	agentcore "orrery_v2/internal/agent/core"
)

func (c *localCore) consumeEvents(conv *conversation, sessionID agentcore.SessionID, backend agentcore.SessionBackend) {
	defer func() {
		conv.eventMu.Lock()
		c.mu.Lock()
		var wire []map[string]any
		if conv.sessionID == sessionID {
			conv.backend = nil
			if activeState(conv.state) {
				wire = c.finishTurnLocked(conv, "failed", "Local provider event stream ended before a result")
			}
		}
		c.mu.Unlock()
		for _, event := range wire {
			c.broadcast(event)
		}
		conv.eventMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = c.claude.CloseSession(ctx, sessionID)
	}()
	for envelope := range backend.Events() {
		if envelope.SessionID != sessionID || envelope.Projection != agentcore.EventProjectionCanonical || envelope.Validate() != nil {
			continue
		}
		conv.eventMu.Lock()
		c.mu.Lock()
		if conv.sessionID != sessionID || (envelope.Sequence > 0 && envelope.Sequence <= conv.lastSequence) {
			c.mu.Unlock()
			conv.eventMu.Unlock()
			continue
		}
		conv.lastSequence = envelope.Sequence
		// Detached describes the process lifetime. Other turn events must not be
		// projected onto a later turn that happens to reuse the same session.
		if envelope.Event.Kind != agentcore.EventDetached && envelope.TurnGeneration > 0 && envelope.TurnGeneration != conv.generation {
			c.mu.Unlock()
			conv.eventMu.Unlock()
			continue
		}
		wire := c.eventWireLocked(conv, envelope.Event)
		c.mu.Unlock()
		for _, event := range wire {
			c.broadcast(event)
		}
		conv.eventMu.Unlock()
		if envelope.Event.Kind == agentcore.EventDetached {
			return
		}
	}
}

func (c *localCore) eventWireLocked(conv *conversation, event agentcore.SessionEvent) []map[string]any {
	if event.Validate() != nil {
		return nil
	}
	data := map[string]any{"conversation_id": conv.id, "turn_id": conv.turnID}
	name := "message.stream"
	switch event.Kind {
	case agentcore.EventMessageDelta:
		conv.response += event.MessageDelta.Text
		data["type"], data["data"], data["msg_id"] = "text", event.MessageDelta.Text, event.MessageDelta.ItemID
	case agentcore.EventThoughtDelta:
		data["type"], data["data"], data["msg_id"] = "thought", event.ThoughtDelta.Text, event.ThoughtDelta.ItemID
	case agentcore.EventToolCall:
		data["type"], data["data"] = "tool_call", map[string]any{
			"call_id": event.ToolCall.ToolUseID, "name": event.ToolCall.Name, "status": "started",
		}
	case agentcore.EventToolResult:
		status := "completed"
		if event.ToolResult.IsError {
			status = "failed"
		}
		data["type"], data["data"] = "tool_call", map[string]any{
			"call_id": event.ToolResult.ToolUseID, "status": status,
		}
	case agentcore.EventPermission:
		if !activeState(conv.state) {
			return nil
		}
		permission := *event.Permission
		conv.pending[permission.RequestID] = permission
		data = permissionWire(conv.id, permission.RequestID, permission)
		data["turn_id"] = conv.turnID
		name = "confirmation.add"
	case agentcore.EventPermissionResolved:
		id := event.PermissionResolved.RequestID
		if _, exists := conv.pending[id]; !exists {
			return nil
		}
		delete(conv.pending, id)
		return []map[string]any{confirmationRemoved(conv.id, conv.turnID, id)}
	case agentcore.EventTurnResult:
		if !activeState(conv.state) {
			return nil
		}
		state := "completed"
		if event.TurnResult.Outcome.Kind == agentcore.TurnOutcomeCancelled {
			state = "cancelled"
		} else if event.TurnResult.IsError || event.TurnResult.Outcome.Kind == agentcore.TurnOutcomeFailed {
			state = "failed"
		}
		return c.finishTurnLocked(conv, state, event.TurnResult.ResultText)
	case agentcore.EventDetached:
		if !activeState(conv.state) {
			return nil
		}
		return c.finishTurnLocked(conv, "failed", event.Detached.RedactedSummary)
	default:
		return nil
	}
	return []map[string]any{{"name": name, "data": data}}
}

func (c *localCore) finishTurnLocked(conv *conversation, state, summary string) []map[string]any {
	wire := make([]map[string]any, 0, len(conv.pending)+1)
	for id := range conv.pending {
		wire = append(wire, confirmationRemoved(conv.id, conv.turnID, id))
		delete(conv.pending, id)
	}
	conv.state = state
	text := conv.response
	if text == "" {
		text = summary
	}
	if text != "" {
		conv.messages = append(conv.messages, storedMessage{ID: conv.turnID + "-assistant", TurnID: conv.turnID, Role: "assistant", Content: text})
	}
	if err := c.saveLocked(conv); err != nil {
		conv.state, state, summary = "failed", "failed", "local conversation state could not be saved"
		slog.Error("Core conversation persistence failed")
	}
	return append(wire, map[string]any{"name": "turn.completed", "data": map[string]any{
		"conversation_id": conv.id, "turn_id": conv.turnID, "state": state, "last_message": summary,
	}})
}

func (c *localCore) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// The native main-process client sends no Origin. Browser requests use the
	// library's same-host check rather than accepting arbitrary web origins.
	upgrader := websocket.Upgrader{}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(4 << 20)
	client := &websocketClient{conn: conn}
	c.mu.Lock()
	c.clients[client] = struct{}{}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.clients, client)
		c.mu.Unlock()
		_ = conn.Close()
	}()
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (c *localCore) broadcast(event map[string]any) {
	c.mu.RLock()
	clients := make([]*websocketClient, 0, len(c.clients))
	for client := range c.clients {
		clients = append(clients, client)
	}
	c.mu.RUnlock()
	for _, client := range clients {
		client.mu.Lock()
		_ = client.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		err := client.conn.WriteJSON(event)
		client.mu.Unlock()
		if err != nil {
			c.mu.Lock()
			delete(c.clients, client)
			c.mu.Unlock()
			_ = client.conn.Close()
		}
	}
}
