package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func emit(value any) { _ = json.NewEncoder(os.Stdout).Encode(value) }

func main() {
	for _, argument := range os.Args[1:] {
		if argument == "--version" || argument == "-v" {
			fmt.Println("1.3.0")
			return
		}
	}
	sessionID := "pi-native-session"
	for index, argument := range os.Args[1:] {
		if argument == "--session" && index+2 <= len(os.Args[1:]) {
			sessionID = os.Args[index+2]
		}
	}
	turn := 0
	active := false
	manual := os.Getenv("PI_NATIVE_HELPER_MANUAL") == "1"
	exitBeforeHandshake := os.Getenv("PI_NATIVE_HELPER_EXIT_BEFORE_HANDSHAKE") == "1"
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var command map[string]any
		if json.Unmarshal(scanner.Bytes(), &command) != nil {
			os.Exit(3)
		}
		id, _ := command["id"].(string)
		typeName, _ := command["type"].(string)
		response := func(data any) {
			value := map[string]any{"id": id, "type": "response", "command": typeName, "success": true}
			if data != nil {
				value["data"] = data
			}
			emit(value)
		}
		switch typeName {
		case "get_state":
			if exitBeforeHandshake {
				fmt.Fprintln(os.Stderr, "fixture exited before handshake token=fixture-sensitive-value /tmp/pi-private/config.json")
				os.Exit(17)
			}
			response(map[string]any{"sessionId": sessionID, "thinkingLevel": "medium", "isStreaming": active, "model": map[string]any{"id": "model-1", "name": "Model One", "provider": "fixture", "reasoning": true}})
		case "get_available_models":
			response(map[string]any{"models": []any{map[string]any{"id": "model-1", "name": "Model One", "provider": "fixture", "reasoning": true}}})
		case "get_commands":
			response(map[string]any{"commands": []any{map[string]any{"name": "review", "description": "Review changes", "source": "extension"}}})
		case "prompt":
			message, _ := command["message"].(string)
			if strings.TrimSpace(message) == "reject" {
				emit(map[string]any{"id": id, "type": "response", "command": typeName, "success": false, "error": "rejected by native fixture"})
				continue
			}
			turn++
			active = true
			if strings.TrimSpace(message) == "noack" {
				emit(map[string]any{"type": "agent_start"})
				continue
			}
			response(nil)
			emit(map[string]any{"type": "agent_start"})
			if manual || strings.TrimSpace(message) == "hold" {
				continue
			}
			finishTurn(turn, fmt.Sprintf("turn %d complete", turn), "stop")
			active = false
		case "steer":
			if !active {
				emit(map[string]any{"id": id, "type": "response", "command": typeName, "success": false, "error": "not streaming"})
				continue
			}
			response(nil)
			finishTurn(turn, "steered complete", "stop")
			active = false
		case "abort":
			response(nil)
			if active {
				finishTurn(turn, "aborted", "aborted")
				active = false
			}
		case "set_model", "set_thinking_level":
			response(nil)
		case "get_session_stats":
			used := uint64(42)
			response(map[string]any{"tokens": map[string]any{"input": 12, "output": 5, "cacheRead": 2, "cacheWrite": 1, "total": 20}, "cost": 0.125, "contextUsage": map[string]any{"tokens": used, "contextWindow": 1000}})
		case "extension_ui_response":
		default:
			emit(map[string]any{"id": id, "type": "response", "command": typeName, "success": false, "error": "unsupported command"})
		}
	}
}

func finishTurn(turn int, text, stopReason string) {
	emit(map[string]any{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_delta", "delta": text}})
	message := map[string]any{
		"role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}, "provider": "fixture", "model": "model-1",
		"usage": map[string]any{"input": 10 + turn, "output": 5, "cacheRead": 2, "cacheWrite": 1}, "stopReason": stopReason,
	}
	emit(map[string]any{"type": "message_end", "message": message})
	emit(map[string]any{"type": "turn_end", "message": message, "toolResults": []any{}})
	emit(map[string]any{"type": "agent_end", "messages": []any{message}, "willRetry": false})
	emit(map[string]any{"type": "agent_settled", "aborted": stopReason == "aborted"})
}
