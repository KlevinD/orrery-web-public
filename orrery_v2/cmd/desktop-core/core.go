package main

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/providers/claude"
)

type localConnection interface {
	agentcore.BackendConnection
	Close() error
}

type localCore struct {
	mu            sync.RWMutex
	conversations map[string]*conversation
	workspaceRefs map[string]string
	clients       map[*websocketClient]struct{}
	claude        localConnection
	executable    string
	db            *sql.DB
}

type conversation struct {
	operationMu  sync.Mutex
	eventMu      sync.Mutex
	id           string
	name         string
	assistantID  string
	workspace    string
	workspaceRef string
	sessionID    agentcore.SessionID
	backend      agentcore.SessionBackend
	turnID       string
	generation   uint64
	lastSequence uint64
	state        string
	pending      map[string]agentcore.PermissionEvent
	created      int64
	updated      int64
	messages     []storedMessage
	restored     bool
	response     string
}

type websocketClient struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

func newLocalCore(executable string) (*localCore, error) {
	c := &localCore{
		conversations: make(map[string]*conversation),
		workspaceRefs: make(map[string]string),
		clients:       make(map[*websocketClient]struct{}),
		executable:    strings.TrimSpace(executable),
	}
	if c.executable == "" {
		c.executable = "claude"
	}
	connection, err := claude.NewNativeConnection(claude.NativeConfig{
		Executable: c.executable,
		ResolveWorkspace: claude.NativeWorkspaceResolverFunc(func(_ context.Context, ref string) (claude.NativeWorkspace, error) {
			c.mu.RLock()
			workspace := c.workspaceRefs[strings.TrimSpace(ref)]
			c.mu.RUnlock()
			if workspace == "" {
				return claude.NativeWorkspace{}, errors.New("workspace reference is not registered")
			}
			return claude.NativeWorkspace{
				Directory: workspace, Launch: claude.NativeLaunch{Executable: c.executable},
				MCPServers: map[string]claude.NativeMCPServer{},
			}, nil
		}),
	})
	if err != nil {
		return nil, err
	}
	c.claude = connection
	return c, nil
}

func (c *localCore) close() error {
	c.mu.RLock()
	clients := make([]*websocketClient, 0, len(c.clients))
	for client := range c.clients {
		clients = append(clients, client)
	}
	c.mu.RUnlock()
	for _, client := range clients {
		_ = client.conn.Close()
	}
	var providerErr, storeErr error
	if c.claude != nil {
		providerErr = c.claude.Close()
	}
	if c.db != nil {
		storeErr = c.db.Close()
	}
	return errors.Join(providerErr, storeErr)
}

func (c *localCore) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		writeError(w, http.StatusForbidden, errors.New("only loopback hosts are allowed"))
		return
	}
	// Loopback is not authorization by itself: reject browser-origin requests
	// from other sites, including simple cross-origin form submissions.
	if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
		writeError(w, http.StatusForbidden, errors.New("cross-origin requests are not allowed"))
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/health":
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": coreVersion})
	case r.Method == http.MethodGet && r.URL.Path == "/api/assistants":
		status := "available"
		if _, err := exec.LookPath(c.executable); err != nil {
			status = "missing"
		}
		writeJSON(w, http.StatusOK, []map[string]any{{
			"id": "orrery-local-agent", "name": "Orrery Local Agent", "enabled": true, "agent_status": status,
		}})
	case r.Method == http.MethodGet && r.URL.Path == "/ws":
		c.handleWebSocket(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/conversations":
		c.createConversation(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/conversations":
		c.mu.RLock()
		items := make([]map[string]any, 0, len(c.conversations))
		for _, conv := range c.conversations {
			items = append(items, conversationWire(conv))
		}
		c.mu.RUnlock()
		writeJSON(w, http.StatusOK, items)
	case strings.HasPrefix(r.URL.Path, "/api/conversations/"):
		c.handleConversation(w, r)
	default:
		writeError(w, http.StatusNotFound, errors.New("route is not implemented by Orrery Core"))
	}
}

func (c *localCore) createConversation(w http.ResponseWriter, r *http.Request) {
	var request map[string]any
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	id, _ := request["id"].(string)
	id = strings.TrimSpace(id)
	if id == "" {
		id = uuid.NewString()
	}
	if len(id) > 128 || strings.ContainsAny(id, "/\\\r\n\x00") {
		writeError(w, http.StatusBadRequest, errors.New("conversation ID is invalid"))
		return
	}
	name, _ := request["name"].(string)
	assistantID := "orrery-local-agent"
	if assistant, ok := request["assistant"].(map[string]any); ok {
		if id, ok := assistant["id"].(string); ok && id != "" {
			assistantID = id
		}
	}
	if assistantID != "orrery-local-agent" {
		writeError(w, http.StatusBadRequest, errors.New("assistant is not available in the local core"))
		return
	}
	workspace := ""
	if extra, ok := request["extra"].(map[string]any); ok {
		workspace, _ = extra["workspace"].(string)
	}
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		workspace, _ = os.Getwd()
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("workspace is invalid"))
		return
	}
	info, err := os.Stat(workspace)
	if err != nil || !info.IsDir() {
		writeError(w, http.StatusBadRequest, errors.New("workspace must be an existing directory"))
		return
	}
	c.mu.Lock()
	if existing := c.conversations[id]; existing != nil {
		matches := existing.workspace == workspace && existing.assistantID == assistantID
		c.mu.Unlock()
		if !matches {
			writeError(w, http.StatusConflict, errors.New("conversation already has a different workspace or assistant"))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id})
		return
	}
	ref := "workspace-" + uuid.NewString()
	c.conversations[id] = &conversation{
		id: id, name: name, assistantID: assistantID, workspace: workspace, workspaceRef: ref,
		state: "idle", pending: make(map[string]agentcore.PermissionEvent),
		created: time.Now().UnixMilli(), updated: time.Now().UnixMilli(),
	}
	if err := c.saveLocked(c.conversations[id]); err != nil {
		delete(c.conversations, id)
		c.mu.Unlock()
		writeError(w, http.StatusInternalServerError, errors.New("conversation could not be saved"))
		return
	}
	c.workspaceRefs[ref] = workspace
	c.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (c *localCore) handleConversation(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/conversations/"), "/")
	c.mu.RLock()
	conv := c.conversations[parts[0]]
	c.mu.RUnlock()
	if conv == nil {
		writeError(w, http.StatusNotFound, errors.New("conversation not found"))
		return
	}
	switch {
	case r.Method == http.MethodGet && len(parts) == 1:
		c.mu.RLock()
		wire := conversationWire(conv)
		c.mu.RUnlock()
		writeJSON(w, http.StatusOK, wire)
	case r.Method == http.MethodGet && len(parts) == 2 && parts[1] == "messages":
		c.mu.RLock()
		messages := append([]storedMessage{}, conv.messages...)
		c.mu.RUnlock()
		writeJSON(w, http.StatusOK, messages)
	case r.Method == http.MethodGet && len(parts) == 2 && parts[1] == "confirmations":
		c.mu.RLock()
		confirmations := make([]map[string]any, 0, len(conv.pending))
		for id, permission := range conv.pending {
			confirmations = append(confirmations, permissionWire(conv.id, id, permission))
		}
		c.mu.RUnlock()
		writeJSON(w, http.StatusOK, confirmations)
	case r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "messages":
		c.sendMessage(w, r, conv)
	case r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "cancel":
		c.cancelConversation(w, r, conv)
	case r.Method == http.MethodPost && len(parts) == 4 && parts[1] == "confirmations" && parts[3] == "confirm":
		c.confirmPermission(w, r, conv, parts[2])
	default:
		writeError(w, http.StatusNotFound, errors.New("route is not implemented by Orrery Core"))
	}
}

func activeState(state string) bool {
	return state == "starting" || state == "running" || state == "cancelling"
}

func (c *localCore) sendMessage(w http.ResponseWriter, r *http.Request, conv *conversation) {
	var request struct {
		Content string `json:"content"`
	}
	if err := decodeJSON(w, r, &request); err != nil || strings.TrimSpace(request.Content) == "" {
		writeError(w, http.StatusBadRequest, errors.New("message content is required"))
		return
	}
	conv.operationMu.Lock()
	defer conv.operationMu.Unlock()
	conv.eventMu.Lock()
	defer conv.eventMu.Unlock()
	c.mu.Lock()
	if conv.restored && len(conv.messages) > 0 {
		c.mu.Unlock()
		writeError(w, http.StatusConflict, errors.New("provider resume is not implemented; saved history is read-only after restart, create a new conversation"))
		return
	}
	if activeState(conv.state) {
		c.mu.Unlock()
		writeError(w, http.StatusConflict, errors.New("conversation already has an active turn"))
		return
	}
	conv.turnID = uuid.NewString()
	conv.state = "starting"
	conv.generation = 0
	conv.response = ""
	backend := conv.backend
	c.mu.Unlock()
	if backend == nil {
		sessionID := agentcore.SessionID(uuid.NewString())
		opened, err := c.claude.OpenSession(r.Context(), agentcore.SessionSpec{
			Mode: agentcore.SessionOpenFresh, SessionID: sessionID,
		}, agentcore.SessionConfig{WorkspaceRef: conv.workspaceRef, Mode: "default"})
		if err != nil {
			c.mu.Lock()
			conv.state = "failed"
			c.mu.Unlock()
			writeError(w, http.StatusBadGateway, errors.New("local provider could not open a session"))
			return
		}
		c.mu.Lock()
		conv.sessionID, conv.backend = sessionID, opened
		conv.lastSequence = 0
		backend = opened
		c.mu.Unlock()
		go c.consumeEvents(conv, sessionID, backend)
	}
	c.mu.Lock()
	turnID := conv.turnID
	conv.state = "running"
	conv.messages = append(conv.messages, storedMessage{ID: uuid.NewString(), TurnID: turnID, Role: "user", Content: request.Content})
	if err := c.saveLocked(conv); err != nil {
		conv.messages = conv.messages[:len(conv.messages)-1]
		conv.state = "failed"
		c.mu.Unlock()
		writeError(w, http.StatusInternalServerError, errors.New("message could not be saved; it was not sent"))
		return
	}
	c.mu.Unlock()
	receipt, err := backend.Dispatch(r.Context(), agentcore.RuntimeCommand{
		CommandID: "command-" + uuid.NewString(), Kind: agentcore.RuntimeCommandSend,
		Send: &agentcore.SendCommand{
			Content:  []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: request.Content}},
			Metadata: agentcore.CommandMetadata{ClientMsgID: turnID},
		},
	})
	if err != nil || !receipt.Accepted {
		c.mu.Lock()
		conv.state = "failed"
		_ = c.saveLocked(conv)
		c.mu.Unlock()
		writeError(w, http.StatusBadGateway, errors.New("local provider rejected the message"))
		return
	}
	c.mu.Lock()
	conv.generation = receipt.TurnGeneration
	c.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"turn_id": turnID})
}

func (c *localCore) cancelConversation(w http.ResponseWriter, r *http.Request, conv *conversation) {
	var request struct {
		TurnID string `json:"turn_id"`
	}
	if err := decodeJSON(w, r, &request); err != nil || request.TurnID == "" {
		writeError(w, http.StatusBadRequest, errors.New("turn ID is required"))
		return
	}
	conv.operationMu.Lock()
	defer conv.operationMu.Unlock()
	c.mu.RLock()
	backend, turnID, state := conv.backend, conv.turnID, conv.state
	c.mu.RUnlock()
	if request.TurnID != turnID {
		writeError(w, http.StatusConflict, errors.New("cancel targets a different turn"))
		return
	}
	if backend == nil || !activeState(state) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	receipt, err := backend.Dispatch(r.Context(), agentcore.RuntimeCommand{
		CommandID: "cancel-" + uuid.NewString(), Kind: agentcore.RuntimeCommandCancel,
		Cancel: &agentcore.CancelCommand{Target: agentcore.CancelTargetTurn, Reason: "desktop request"},
	})
	if err != nil || !receipt.Accepted {
		writeError(w, http.StatusBadGateway, errors.New("local provider did not accept cancellation"))
		return
	}
	c.mu.Lock()
	if activeState(conv.state) {
		conv.state = "cancelling"
	}
	c.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
