package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestStoreRestartKeepsMessagesAndDoesNotRestoreApprovals(t *testing.T) {
	directory := t.TempDir()
	c, _ := testCore(t)
	if err := c.openStore(directory); err != nil {
		t.Fatal(err)
	}
	conv := createTestConversation(t, c, "saved", t.TempDir())
	c.mu.Lock()
	conv.state, conv.turnID = "running", "turn-1"
	conv.messages = []storedMessage{{ID: "message-1", TurnID: "turn-1", Role: "user", Content: "saved request"}}
	conv.pending["permission-1"] = agentcore.PermissionEvent{RequestID: "permission-1"}
	if err := c.saveLocked(conv); err != nil {
		t.Fatal(err)
	}
	c.mu.Unlock()
	if err := c.db.Close(); err != nil {
		t.Fatal(err)
	}
	c.db = nil
	restored, _ := testCore(t)
	if err := restored.openStore(directory); err != nil {
		t.Fatal(err)
	}
	snapshot := requestCore(t, restored, "GET", "/api/conversations/saved", "")
	if snapshot.Code != 200 || !strings.Contains(snapshot.Body.String(), `"state":"interrupted"`) {
		t.Fatal(snapshot.Body.String())
	}
	history := requestCore(t, restored, "GET", "/api/conversations/saved/messages", "")
	var messages []storedMessage
	if err := json.Unmarshal(history.Body.Bytes(), &messages); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Content != "saved request" {
		t.Fatal(messages)
	}
	if len(restored.conversations["saved"].pending) != 0 {
		t.Fatal("approval restored across restart")
	}
	send := requestCore(t, restored, "POST", "/api/conversations/saved/messages", `{"content":"continue"}`)
	if send.Code != http.StatusConflict {
		t.Fatal("silently started a new provider context")
	}
}

func TestStoreFailurePreventsProviderDispatch(t *testing.T) {
	c, connection := testCore(t)
	if err := c.openStore(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	createTestConversation(t, c, "saved", t.TempDir())
	_ = c.db.Close()
	w := requestCore(t, c, "POST", "/api/conversations/saved/messages", `{"content":"do work"}`)
	if w.Code != 500 {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(connection.backends) != 1 || len(connection.backends[0].commands) != 0 {
		t.Fatal("sent an unsaved message")
	}
}

func TestStoreCorruptionIsNotSilentlyReplaced(t *testing.T) {
	c, _ := testCore(t)
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "orrery-core.sqlite3"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.openStore(directory); err == nil {
		t.Fatal("accepted corrupt database")
	}
}

func TestCrossOriginCannotCreateConversation(t *testing.T) {
	c, _ := testCore(t)
	r := httptest.NewRequest("POST", "http://127.0.0.1:1234/api/conversations", strings.NewReader(`{}`))
	r.Header.Set("Origin", "https://untrusted.example")
	w := httptest.NewRecorder()
	c.ServeHTTP(w, r)
	if w.Code != 403 || len(c.conversations) != 0 {
		t.Fatal("cross-origin mutation accepted")
	}
}

func TestRebindingHostCannotCreateConversation(t *testing.T) {
	c, _ := testCore(t)
	r := httptest.NewRequest("POST", "http://rebinding.example/api/conversations", strings.NewReader(`{}`))
	r.Header.Set("Origin", "http://rebinding.example")
	w := httptest.NewRecorder()
	c.ServeHTTP(w, r)
	if w.Code != 403 || len(c.conversations) != 0 {
		t.Fatal("rebinding host accepted")
	}
}
