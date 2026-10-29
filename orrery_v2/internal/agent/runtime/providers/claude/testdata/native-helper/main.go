package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		if count := os.Getenv("ORRERY_CLAUDE_VERSION_COUNT"); count != "" {
			file, err := os.OpenFile(count, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
			if err == nil {
				_, _ = file.WriteString("1\n")
				_ = file.Close()
			}
		}
		version := os.Getenv("ORRERY_CLAUDE_HELPER_VERSION")
		if version == "" {
			version = "2.1.220 (Claude Code)"
		}
		_, _ = os.Stdout.WriteString(version + "\n")
		return
	}
	mode := os.Getenv("ORRERY_CLAUDE_HELPER_MODE")
	capture := os.Getenv("ORRERY_CLAUDE_HELPER_CAPTURE")
	argsCapture := os.Getenv("ORRERY_CLAUDE_HELPER_ARGS")
	if argsCapture != "" {
		encoded, _ := json.Marshal(os.Args[1:])
		_ = os.WriteFile(argsCapture, encoded, 0o600)
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	turn := 0
	pendingPermission := false
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		captureLine(capture, line)
		var frame map[string]any
		if json.Unmarshal(line, &frame) != nil {
			continue
		}
		switch stringValue(frame["type"]) {
		case "control_request":
			request, _ := frame["request"].(map[string]any)
			id := stringValue(frame["request_id"])
			switch stringValue(request["subtype"]) {
			case "initialize":
				if mode == "startup-timeout" {
					continue
				}
				controlSuccess(id, map[string]any{
					"models": []any{
						map[string]any{"value": "claude-test", "displayName": "Claude Test", "description": "captured model", "supportedEffortLevels": []any{"low", "high", "xhigh"}},
						map[string]any{"value": "claude-fast", "displayName": "Claude Fast", "supportedEffortLevels": []any{"low"}},
					},
					"commands": []any{map[string]any{"name": "compact", "description": "Compact context"}, map[string]any{"name": "review", "description": "Review changes"}},
				})
			case "interrupt":
				controlSuccess(id, nil)
				result(true, "cancelled", "cancelled", nil, true)
			case "set_permission_mode":
				modeValue := stringValue(request["mode"])
				if modeValue == "dontAsk" && mode == "reject-setting" {
					controlError(id, "permission mode rejected")
				} else {
					controlSuccess(id, nil)
					write(map[string]any{"type": "system", "subtype": "status", "permissionMode": modeValue})
				}
			case "set_model", "apply_flag_settings":
				controlSuccess(id, nil)
			case "get_context_usage":
				controlSuccess(id, map[string]any{"totalTokens": 3000, "maxTokens": 200000, "categories": []any{map[string]any{"name": "System prompt", "tokens": 1400}}})
			case "get_session_cost":
				controlSuccess(id, map[string]any{"text": "Total cost: $0.12"})
			default:
				controlError(id, "unsupported helper control")
			}
		case "control_response":
			if pendingPermission {
				pendingPermission = false
				result(false, "success", "end_turn", map[string]any{"input_tokens": 2, "output_tokens": 1}, true)
			}
		case "user":
			turn++
			if mode == "crash" {
				os.Exit(19)
			}
			sessionID := sessionArg()
			write(map[string]any{"type": "system", "subtype": "init", "session_id": sessionID, "model": "claude-test", "permissionMode": "default", "mcp_servers": []any{map[string]any{"name": "docs", "status": "connected"}}})
			frame["session_id"] = sessionID
			frame["isReplay"] = true
			write(frame)
			switch mode {
			case "hold", "manual":
				continue
			case "permission":
				pendingPermission = true
				write(map[string]any{"type": "control_request", "request_id": "perm-1", "request": map[string]any{"subtype": "can_use_tool", "tool_name": "Bash", "tool_use_id": "tool-1", "input": map[string]any{"command": "echo ok"}}})
			case "questions":
				pendingPermission = true
				write(map[string]any{"type": "control_request", "request_id": "question-1", "request": map[string]any{"subtype": "can_use_tool", "tool_name": "AskUserQuestion", "tool_use_id": "tool-q", "input": map[string]any{"questions": []any{
					map[string]any{"question": "Pick one", "header": "One", "options": []any{map[string]any{"label": "A", "description": "first"}, map[string]any{"label": "B"}}, "multiSelect": false},
					map[string]any{"question": "Pick many", "header": "Many", "options": []any{map[string]any{"label": "X"}, map[string]any{"label": "Y"}}, "multiSelect": true},
				}}}})
			case "tail-valid":
				writeNoNewline(map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": "tail", "stop_reason": "end_turn", "usage": map[string]any{"input_tokens": 1, "output_tokens": 1}})
				return
			case "tail-invalid":
				_, _ = os.Stdout.WriteString(`{"type":"result"`)
				return
			default:
				emitNormalTurn(turn)
			}
		}
	}
}

func emitNormalTurn(turn int) {
	messageID := fmt.Sprintf("message-%d", turn)
	write(map[string]any{"type": "stream_event", "event": map[string]any{"type": "message_start", "message": map[string]any{"id": messageID}}})
	write(map[string]any{"type": "stream_event", "event": map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text"}}})
	write(map[string]any{"type": "stream_event", "event": map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": fmt.Sprintf("reply-%d", turn)}}})
	write(map[string]any{"type": "assistant", "message": map[string]any{"id": messageID, "content": []any{map[string]any{"type": "text", "text": fmt.Sprintf("reply-%d", turn)}}}})
	write(map[string]any{"type": "stream_event", "event": map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn"}}})
	result(false, "success", "end_turn", map[string]any{"input_tokens": 3, "output_tokens": 2, "cache_read_input_tokens": 4, "cache_creation_input_tokens": 5}, true)
}

func result(isError bool, subtype, reason string, usage map[string]any, newline bool) {
	frame := map[string]any{"type": "result", "subtype": subtype, "is_error": isError, "result": "", "stop_reason": reason, "total_cost_usd": 0.01}
	if usage != nil {
		frame["usage"] = usage
	}
	if newline {
		write(frame)
	} else {
		writeNoNewline(frame)
	}
}

func controlSuccess(id string, response any) {
	inner := map[string]any{"subtype": "success", "request_id": id}
	if response != nil {
		inner["response"] = response
	}
	write(map[string]any{"type": "control_response", "response": inner})
}

func controlError(id, message string) {
	write(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "error", "request_id": id, "error": message}})
}

func sessionArg() string {
	for index, arg := range os.Args[1:] {
		if (arg == "--session-id" || arg == "--resume") && index+2 <= len(os.Args[1:]) {
			return os.Args[1:][index+1]
		}
	}
	return "00000000-0000-0000-0000-000000000001"
}

func captureLine(path string, line []byte) {
	if path == "" {
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, _ = file.Write(append(line, '\n'))
	_ = file.Close()
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func write(value any) {
	encoded, _ := json.Marshal(value)
	_, _ = os.Stdout.Write(append(encoded, '\n'))
}

func writeNoNewline(value any) {
	encoded, _ := json.Marshal(value)
	_, _ = os.Stdout.Write(encoded)
}
