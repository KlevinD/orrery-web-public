package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type frame struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params map[string]any  `json:"params"`
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		if count := os.Getenv("ORRERY_CODEX_VERSION_COUNT"); count != "" {
			file, err := os.OpenFile(count, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
			if err == nil {
				_, _ = file.WriteString("1\n")
				_ = file.Close()
			}
		}
		version := os.Getenv("ORRERY_CODEX_HELPER_VERSION")
		if version == "" {
			version = "codex-cli 0.145.0"
		}
		_, _ = os.Stdout.WriteString(version + "\n")
		return
	}
	mode := os.Getenv("ORRERY_CODEX_HELPER_MODE")
	capture := os.Getenv("ORRERY_CODEX_HELPER_CAPTURE")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	turn := 0
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		captureLine(capture, line)
		var request frame
		if json.Unmarshal(line, &request) != nil {
			continue
		}
		if request.Method == "" {
			handleReverseResponse(mode, request.ID)
			continue
		}
		if len(request.ID) == 0 {
			continue
		}
		switch request.Method {
		case "initialize":
			respond(request.ID, map[string]any{"userAgent": "codex-cli-test"})
		case "thread/start":
			respond(request.ID, map[string]any{"thread": map[string]any{"id": "thread-native-1"}})
			notify("thread/started", map[string]any{"thread": map[string]any{"id": "thread-native-1"}})
		case "thread/resume":
			id, _ := request.Params["threadId"].(string)
			respond(request.ID, map[string]any{"thread": map[string]any{"id": id}})
			notify("thread/started", map[string]any{"thread": map[string]any{"id": id}})
		case "model/list":
			respond(request.ID, map[string]any{"data": []any{map[string]any{"id": "gpt-test", "displayName": "GPT Test", "supportedReasoningEfforts": []any{map[string]any{"reasoningEffort": "low"}, map[string]any{"reasoningEffort": "high"}}}}})
		case "permissionProfile/list":
			respond(request.ID, map[string]any{"data": []any{map[string]any{"id": ":workspace"}, map[string]any{"id": ":read-only"}}})
		case "thread/settings/update":
			respond(request.ID, map[string]any{})
			settings := map[string]any{}
			for key, value := range request.Params {
				switch key {
				case "model", "effort":
					settings[key] = value
				case "permissions":
					settings["activePermissionProfile"] = map[string]any{"id": value}
				}
			}
			notify("thread/settings/updated", map[string]any{"threadId": "thread-native-1", "threadSettings": settings})
		case "turn/start", "review/start", "thread/compact/start":
			if mode == "reject-send" {
				respondError(request.ID, -32602, "turn rejected")
				continue
			}
			turn++
			turnID := fmt.Sprintf("turn-%d", turn)
			respond(request.ID, map[string]any{"turn": map[string]any{"id": turnID, "status": "inProgress"}})
			notify("turn/started", map[string]any{"threadId": "thread-native-1", "turn": map[string]any{"id": turnID}})
			switch mode {
			case "hold":
				notify("item/agentMessage/delta", map[string]any{"threadId": "thread-native-1", "turnId": turnID, "itemId": "message-1", "delta": "working"})
			case "approval":
				reverse(700, "item/commandExecution/requestApproval", map[string]any{"threadId": "thread-native-1", "turnId": turnID, "itemId": "tool-1"})
			case "auth":
				reverse(701, "account/chatgptAuthTokens/refresh", map[string]any{"reason": "expired"})
			case "question":
				reverse(702, "item/tool/requestUserInput", map[string]any{"threadId": "thread-native-1", "turnId": turnID, "itemId": "question-1", "questions": []any{map[string]any{"id": "q1", "header": "Choice", "question": "Choose", "options": []any{map[string]any{"label": "A", "description": "first"}, map[string]any{"label": "B", "description": "second"}}}}})
			case "elicitation":
				reverse(703, "mcpServer/elicitation/request", map[string]any{"threadId": "thread-native-1", "turnId": turnID, "serverName": "docs", "mode": "form", "requestedSchema": map[string]any{"type": "object"}})
			case "crash":
				os.Exit(17)
			case "idle":
				notify("thread/status/changed", map[string]any{"threadId": "thread-native-1", "status": map[string]any{"type": "idle"}})
				return
			case "manual":
			default:
				emitNormalTurn(turnID, turn)
			}
		case "turn/steer":
			respond(request.ID, map[string]any{})
			turnID, _ := request.Params["expectedTurnId"].(string)
			notify("item/agentMessage/delta", map[string]any{"threadId": "thread-native-1", "turnId": turnID, "itemId": "message-1", "delta": " steered"})
			complete(turnID, "completed")
		case "turn/interrupt":
			respond(request.ID, map[string]any{})
			turnID, _ := request.Params["turnId"].(string)
			complete(turnID, "interrupted")
		case "thread/rollback":
			respond(request.ID, map[string]any{"thread": map[string]any{"id": "thread-native-1"}})
		case "thread/turns/list":
			respond(request.ID, map[string]any{"data": []any{map[string]any{"id": "turn-1", "status": "completed"}, map[string]any{"id": "turn-2", "status": "failed"}}})
		case "account/logout":
			respond(request.ID, map[string]any{})
		default:
			respondError(request.ID, -32601, "unknown helper method")
		}
	}
}

func emitNormalTurn(turnID string, turn int) {
	notify("item/started", map[string]any{"threadId": "thread-native-1", "turnId": turnID, "item": map[string]any{"type": "agentMessage", "id": "message-1", "text": ""}})
	notify("item/agentMessage/delta", map[string]any{"threadId": "thread-native-1", "turnId": turnID, "itemId": "message-1", "delta": fmt.Sprintf("reply-%d", turn)})
	notify("item/completed", map[string]any{"threadId": "thread-native-1", "turnId": turnID, "item": map[string]any{"type": "agentMessage", "id": "message-1", "text": fmt.Sprintf("reply-%d", turn)}})
	notify("thread/tokenUsage/updated", map[string]any{"threadId": "thread-native-1", "turnId": turnID, "tokenUsage": map[string]any{"last": map[string]any{"inputTokens": 3, "outputTokens": 2, "totalTokens": 5}}})
	notify("thread/status/changed", map[string]any{"threadId": "thread-native-1", "status": map[string]any{"type": "idle"}})
	complete(turnID, "completed")
}

func handleReverseResponse(mode string, id json.RawMessage) {
	switch strings.TrimSpace(string(id)) {
	case "700":
		notify("serverRequest/resolved", map[string]any{"requestId": 700})
		complete("turn-1", "completed")
	case "701":
		notify("serverRequest/resolved", map[string]any{"requestId": 701})
		complete("turn-1", "completed")
	case "702":
		notify("serverRequest/resolved", map[string]any{"requestId": 702})
		complete("turn-1", "completed")
	case "703":
		notify("serverRequest/resolved", map[string]any{"requestId": 703})
		complete("turn-1", "completed")
	default:
		_ = mode
	}
}

func complete(turnID, status string) {
	notify("turn/completed", map[string]any{"threadId": "thread-native-1", "turn": map[string]any{"id": turnID, "status": status}})
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

func respond(id json.RawMessage, result any) {
	write(map[string]any{"jsonrpc": "2.0", "id": rawID(id), "result": result})
}

func respondError(id json.RawMessage, code int, message string) {
	write(map[string]any{"jsonrpc": "2.0", "id": rawID(id), "error": map[string]any{"code": code, "message": message}})
}

func notify(method string, params any) {
	write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func reverse(id int, method string, params any) {
	write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
}

func rawID(raw json.RawMessage) any {
	var id any
	_ = json.Unmarshal(raw, &id)
	return id
}

func write(value any) {
	encoded, _ := json.Marshal(value)
	_, _ = os.Stdout.Write(append(encoded, '\n'))
}
