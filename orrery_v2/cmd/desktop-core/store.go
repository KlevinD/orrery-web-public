package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
	agentcore "orrery_v2/internal/agent/core"
)

type storedMessage struct {
	ID      string `json:"id"`
	TurnID  string `json:"turn_id"`
	Role    string `json:"role"`
	Content string `json:"content"`
}

func conversationWire(conv *conversation) map[string]any {
	return map[string]any{"id": conv.id, "name": conv.name, "type": "acp", "createTime": conv.created, "modifyTime": conv.updated,
		"assistant": map[string]any{"id": conv.assistantID}, "extra": map[string]any{"workspace": conv.workspace},
		"runtime": map[string]any{"state": conv.state, "turn_id": conv.turnID, "pending_confirmations": len(conv.pending), "resume_supported": false}}
}

// Orrery Core owns a separate database; it never opens or rewrites vendor data.
func (c *localCore) openStore(directory string) error {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", filepath.Join(directory, "orrery-core.sqlite3"))
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	ok := false
	defer func() {
		if !ok {
			_ = db.Close()
		}
	}()
	if _, err = db.Exec(`PRAGMA busy_timeout=5000; CREATE TABLE IF NOT EXISTS core_conversations (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, assistant TEXT NOT NULL, workspace TEXT NOT NULL, ref TEXT NOT NULL,
		state TEXT NOT NULL, turn_id TEXT NOT NULL, created INTEGER NOT NULL, updated INTEGER NOT NULL, messages TEXT NOT NULL)`); err != nil {
		return err
	}
	if err = os.Chmod(filepath.Join(directory, "orrery-core.sqlite3"), 0600); err != nil {
		return err
	}
	var integrity string
	if err = db.QueryRow("PRAGMA quick_check").Scan(&integrity); err != nil || integrity != "ok" {
		return errors.New("core database integrity check failed")
	}
	rows, err := db.Query("SELECT id,name,assistant,workspace,ref,state,turn_id,created,updated,messages FROM core_conversations ORDER BY created,id")
	if err != nil {
		return err
	}
	for rows.Next() {
		conv := &conversation{restored: true, pending: make(map[string]agentcore.PermissionEvent)}
		var messages string
		if err = rows.Scan(&conv.id, &conv.name, &conv.assistantID, &conv.workspace, &conv.workspaceRef, &conv.state, &conv.turnID, &conv.created, &conv.updated, &messages); err != nil {
			_ = rows.Close()
			return err
		}
		if err = json.Unmarshal([]byte(messages), &conv.messages); err != nil {
			_ = rows.Close()
			return err
		}
		if activeState(conv.state) {
			conv.state = "interrupted"
		}
		c.conversations[conv.id] = conv
		c.workspaceRefs[conv.workspaceRef] = conv.workspace
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	if _, err = db.Exec("UPDATE core_conversations SET state='interrupted' WHERE state IN ('starting','running','cancelling')"); err != nil {
		return err
	}
	c.db, ok = db, true
	return nil
}

// Call with c.mu held. The accepted user message is durable before dispatch.
func (c *localCore) saveLocked(conv *conversation) error {
	if c.db == nil {
		return nil
	}
	messages, err := json.Marshal(conv.messages)
	if err != nil {
		return err
	}
	conv.updated = time.Now().UnixMilli()
	_, err = c.db.Exec(`INSERT INTO core_conversations VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name,state=excluded.state,turn_id=excluded.turn_id,updated=excluded.updated,messages=excluded.messages`,
		conv.id, conv.name, conv.assistantID, conv.workspace, conv.workspaceRef, conv.state, conv.turnID, conv.created, conv.updated, string(messages))
	return err
}
