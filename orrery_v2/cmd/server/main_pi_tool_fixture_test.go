package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func writePiDiscussionMemberRuntimeFixture(t *testing.T, targetAgentIDs string) string {
	t.Helper()
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "pi-discussion-member-fixture.go")
	executablePath := filepath.Join(dir, "pi-discussion-member-fixture")
	if runtime.GOOS == "windows" {
		executablePath += ".exe"
	}
	source := `package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

type request struct {
	ID   string ` + "`json:\"id\"`" + `
	Type string ` + "`json:\"type\"`" + `
}

type bridgeConfig struct {
	BaseURL   string ` + "`json:\"base_url\"`" + `
	AuthToken string ` + "`json:\"auth_token\"`" + `
}

func emit(writer *bufio.Writer, value any) {
	_ = json.NewEncoder(writer).Encode(value)
	_ = writer.Flush()
}

func bridgeToolCall(cfg bridgeConfig, callID, tool string, input map[string]any) (map[string]any, error) {
	payload, err := json.Marshal(map[string]any{"tool_call_id": callID, "tool": tool, "input": input})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, cfg.BaseURL+"/tool-call", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("bridge tool %s failed with status %d: %s", tool, resp.StatusCode, string(body))
	}
	var decoded struct {
		Output map[string]any ` + "`json:\"output\"`" + `
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, err
	}
	return decoded.Output, nil
}

func executeTool(writer *bufio.Writer, cfg bridgeConfig, callID, tool string, input map[string]any) error {
	emit(writer, map[string]any{"type": "tool_execution_start", "toolCallId": callID, "toolName": tool, "args": input})
	output, err := bridgeToolCall(cfg, callID, tool, input)
	if err != nil {
		emit(writer, map[string]any{"type": "tool_execution_end", "toolCallId": callID, "toolName": tool, "args": input, "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": err.Error()}}}, "isError": true})
		return err
	}
	encoded, _ := json.Marshal(output)
	emit(writer, map[string]any{"type": "tool_execution_end", "toolCallId": callID, "toolName": tool, "args": input, "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": string(encoded)}}}, "isError": false})
	return nil
}

func main() {
	for _, argument := range os.Args[1:] {
		if argument == "--version" || argument == "-v" {
			fmt.Println("1.3.0")
			return
		}
	}
	configPath := os.Getenv("ORRERY_PI_BRIDGE_CONFIG")
	rawConfig, err := os.ReadFile(configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	var cfg bridgeConfig
	if err := json.Unmarshal(rawConfig, &cfg); err != nil || cfg.BaseURL == "" || cfg.AuthToken == "" {
		fmt.Fprintln(os.Stderr, "invalid PI bridge config")
		os.Exit(3)
	}
	decoder := json.NewDecoder(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	for {
		var message request
		if decoder.Decode(&message) != nil {
			return
		}
		switch message.Type {
		case "prompt":
			emit(writer, map[string]any{"id": message.ID, "type": "response", "command": "prompt", "success": true})
			if err := executeTool(writer, cfg, "tool-list-talent", "discussion_talent_list", map[string]any{"placement": "internal", "limit": 10}); err != nil {
				emit(writer, map[string]any{"type": "extension_error", "error": err.Error()})
				emit(writer, map[string]any{"type": "agent_end", "messages": []any{}})
				emit(writer, map[string]any{"type": "agent_settled", "aborted": false})
				continue
			}
			if err := executeTool(writer, cfg, "tool-add-members", "discussion_member_add", map[string]any{"agent_ids": "` + targetAgentIDs + `"}); err != nil {
				emit(writer, map[string]any{"type": "extension_error", "error": err.Error()})
				emit(writer, map[string]any{"type": "agent_end", "messages": []any{}})
				emit(writer, map[string]any{"type": "agent_settled", "aborted": false})
				continue
			}
			text := "Members added through native PI tools."
			emit(writer, map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant", "provider": "pi", "model": "pi-test", "content": []any{map[string]any{"type": "text", "text": text}}, "usage": map[string]any{"input": 3, "output": 4}}, "stopReason": "stop"})
			emit(writer, map[string]any{"type": "agent_end", "messages": []any{}})
			emit(writer, map[string]any{"type": "agent_settled", "aborted": false})
		case "get_state":
			emit(writer, map[string]any{"id": message.ID, "type": "response", "command": "get_state", "success": true, "data": map[string]any{"sessionId": "pi-member-session", "model": map[string]any{"id": "pi-test", "name": "PI Test", "provider": "fixture"}}})
		case "get_available_models":
			emit(writer, map[string]any{"id": message.ID, "type": "response", "command": "get_available_models", "success": true, "data": map[string]any{"models": []any{}}})
		case "get_commands":
			emit(writer, map[string]any{"id": message.ID, "type": "response", "command": "get_commands", "success": true, "data": map[string]any{"commands": []any{}}})
		}
	}
}
`
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		t.Fatalf("write PI discussion member fixture source: %v", err)
	}
	cmd := exec.Command("go", "build", "-o", executablePath, sourcePath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build PI discussion member fixture: %v\n%s", err, output)
	}
	removeWindowsRuntimeFixtureBeforeTempDirCleanup(t, executablePath)
	return executablePath
}
