package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	agentcore "orrery_v2/internal/agent/core"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	operations "orrery_v2/internal/app/operations"
	resourceadmin "orrery_v2/internal/app/resourceadmin"
	"orrery_v2/internal/app/userauth"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/uow"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

const testInternalViewerAuthorization = "Bearer test-internal-viewer"

func init() {
	_ = os.Setenv("ORRERY_INTERNAL_VIEWER_AUTHORIZATION", testInternalViewerAuthorization)
}

type productionWorkerOrgTree struct {
	OrganizationID string
	DepartmentID   string
	PositionID     string
}

func createProductionWorkerOrgTree(t *testing.T, handler http.Handler) productionWorkerOrgTree {
	t.Helper()
	createOrg := httptest.NewRequest(http.MethodPost, "/api/org", jsonBody(`{"name":"Orrery"}`))
	createOrg.Header.Set("X-User-ID", "superadmin")
	createOrg.Header.Set("X-Viewer-Kind", "user")
	createOrg.Header.Set("X-Permission-Scope", "org.manage")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, createOrg)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create org status = %d body = %s", rec.Code, rec.Body.String())
	}
	var org operations.OrganizationDTO
	if err := json.NewDecoder(rec.Body).Decode(&org); err != nil {
		t.Fatalf("decode org: %v", err)
	}
	createUnit := httptest.NewRequest(http.MethodPost, "/api/org/units", jsonBody(`{"organization_id":"`+org.OrganizationID+`","name":"Launch Team"}`))
	createUnit.Header.Set("X-User-ID", "superadmin")
	createUnit.Header.Set("X-Viewer-Kind", "user")
	createUnit.Header.Set("X-Permission-Scope", "org.manage")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, createUnit)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create unit status = %d body = %s", rec.Code, rec.Body.String())
	}
	var unit operations.DepartmentDTO
	if err := json.NewDecoder(rec.Body).Decode(&unit); err != nil {
		t.Fatalf("decode unit: %v", err)
	}
	graphReq := httptest.NewRequest(http.MethodGet, "/api/org/graph?organization_id="+org.OrganizationID, nil)
	graphReq.Header.Set("X-User-ID", "superadmin")
	graphReq.Header.Set("X-Viewer-Kind", "user")
	graphReq.Header.Set("X-Permission-Scope", "org.read")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, graphReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("org graph status = %d body = %s", rec.Code, rec.Body.String())
	}
	var graph operations.OrgGraphDTO
	if err := json.NewDecoder(rec.Body).Decode(&graph); err != nil {
		t.Fatalf("decode org graph: %v", err)
	}
	unitFound := false
	for _, department := range graph.Departments {
		if department.DepartmentID == unit.DepartmentID && department.OrganizationID == org.OrganizationID && department.ParentDepartmentID == "" {
			unitFound = true
			break
		}
	}
	if !unitFound {
		t.Fatalf("org graph missing generated child unit %s: %+v", unit.DepartmentID, graph.Departments)
	}
	createPosition := httptest.NewRequest(http.MethodPost, "/api/org/positions", jsonBody(`{"organization_id":"`+org.OrganizationID+`","department_id":"`+unit.DepartmentID+`","name":"Launch Operator"}`))
	createPosition.Header.Set("X-User-ID", "superadmin")
	createPosition.Header.Set("X-Viewer-Kind", "user")
	createPosition.Header.Set("X-Permission-Scope", "org.manage")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, createPosition)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create position status = %d body = %s", rec.Code, rec.Body.String())
	}
	var position operations.PositionDTO
	if err := json.NewDecoder(rec.Body).Decode(&position); err != nil {
		t.Fatalf("decode position: %v", err)
	}
	return productionWorkerOrgTree{OrganizationID: org.OrganizationID, DepartmentID: unit.DepartmentID, PositionID: position.PositionID}
}

func writeCodexRuntimeFixture(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		path := filepath.Join(t.TempDir(), "codex-fixture.sh")
		body := `#!/bin/sh
if [ "$1" = "--version" ]; then
  printf '%s\n' 'codex-cli 0.145.0'
  exit 0
fi
while IFS= read -r line; do
  id=$(printf '%s\n' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  case "$line" in
  *'"method":"initialize"'*)
    printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id"
    ;;
  *'"method":"thread/start"'*)
    printf '{"jsonrpc":"2.0","id":%s,"result":{"threadId":"codex-thread-1"}}\n' "$id"
    ;;
  *'"method":"thread/resume"'*)
    resumed=1
    printf '{"jsonrpc":"2.0","id":%s,"result":{"threadId":"codex-thread-1"}}\n' "$id"
    ;;
  *'"method":"model/list"'*)
    printf '%s\n' '{"jsonrpc":"2.0","id":3,"result":{"data":[]}}'
    ;;
  *'"method":"permissionProfile/list"'*)
    printf '%s\n' '{"jsonrpc":"2.0","id":4,"result":{"data":[]}}'
    ;;
  *'"method":"turn/start"'*)
    printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id"
    printf '%s\n' '{"jsonrpc":"2.0","method":"turn/started","params":{"threadId":"codex-thread-1","turn":{"id":"turn-1"}}}'
    if [ "$resumed" != "1" ]; then
    printf '%s\n' '{"jsonrpc":"2.0","method":"item/started","params":{"threadId":"codex-thread-1","turnId":"turn-1","item":{"type":"dynamicToolCall","id":"tool-mission","tool":"mission_prepare","arguments":{"title":"Launch plan","final_goal":"Coordinate v2 launch","root_goal":"Coordinate v2 launch execution","business_direction":"Growth","expected_deliverables":"Launch brief;Rollout checklist","idempotency_key":"mission-from-codex-runtime"}}}}'
    printf '%s\n' '{"jsonrpc":"2.0","method":"item/completed","params":{"threadId":"codex-thread-1","turnId":"turn-1","item":{"type":"dynamicToolCall","id":"tool-mission","tool":"mission_prepare","status":"completed","contentItems":[]}}}'
    printf '%s\n' '{"jsonrpc":"2.0","method":"item/started","params":{"threadId":"codex-thread-1","turnId":"turn-1","item":{"type":"dynamicToolCall","id":"tool-coordinator","tool":"mission_coordinator_plan_parallel","arguments":{"goal":"Continue execution with the launch worker","worker_lanes_json":"[{\"role\":\"launch worker\",\"agent_id\":\"agent-worker\",\"goal\":\"Coordinate v2 launch execution\",\"idempotency_key\":\"lane-from-codex-runtime\"}]","idempotency_key":"coordinator-plan-from-codex-runtime"}}}}'
    printf '%s\n' '{"jsonrpc":"2.0","method":"item/completed","params":{"threadId":"codex-thread-1","turnId":"turn-1","item":{"type":"dynamicToolCall","id":"tool-coordinator","tool":"mission_coordinator_plan_parallel","status":"completed","contentItems":[]}}}'
    fi
    printf '%s\n' '{"jsonrpc":"2.0","method":"item/started","params":{"threadId":"codex-thread-1","turnId":"turn-1","item":{"type":"agentMessage","id":"message-1","text":""}}}'
    printf '%s\n' '{"jsonrpc":"2.0","method":"item/agentMessage/delta","params":{"threadId":"codex-thread-1","turnId":"turn-1","itemId":"message-1","delta":"What deadline should I optimize for?"}}'
    printf '%s\n' '{"jsonrpc":"2.0","method":"item/completed","params":{"threadId":"codex-thread-1","turnId":"turn-1","item":{"type":"agentMessage","id":"message-1","text":"What deadline should I optimize for?"}}}'
    printf '%s\n' '{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"codex-thread-1","turn":{"id":"turn-1","status":"completed"}}}'
    ;;
  esac
done
`
		if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
			t.Fatalf("write codex fixture: %v", err)
		}
		return path
	}
	return writeWindowsCodexRuntimeFixture(t, true)
}

func writeTestConfigYAML(t *testing.T, body string) string {
	t.Helper()
	templatePath := filepath.Join("..", "..", "config", "orrery.development.yaml")
	templateBody, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("read test config template: %v", err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(templateBody, &document); err != nil {
		t.Fatalf("parse test config template: %v", err)
	}
	setTestConfigPath(t, document, "paths.base_dir", ".")
	setTestConfigPath(t, document, "database.password_env", "")
	setTestDatabaseURL(t, document, "memory://core")
	var overlay map[string]any
	if err := yaml.Unmarshal([]byte(body), &overlay); err != nil {
		t.Fatalf("parse test config overlay: %v", err)
	}
	mergeTestConfig(document, overlay)
	if environment, _ := document["environment"].(string); environment == "production" {
		document["profile"] = "production"
	}
	if database, ok := overlay["database"].(map[string]any); ok {
		if defaultURL, ok := database["default_url"].(string); ok {
			if _, hasURLs := database["urls"]; !hasURLs {
				setTestDatabaseURL(t, document, defaultURL)
			}
		}
	}
	encoded, err := yaml.Marshal(document)
	if err != nil {
		t.Fatalf("marshal test config: %v", err)
	}
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatalf("write config yaml: %v", err)
	}
	return path
}

func mergeTestConfig(target, overlay map[string]any) {
	for key, value := range overlay {
		overlayMap, overlayIsMap := value.(map[string]any)
		targetMap, targetIsMap := target[key].(map[string]any)
		if overlayIsMap && targetIsMap {
			mergeTestConfig(targetMap, overlayMap)
			continue
		}
		target[key] = value
	}
}

func writeCodexTextRuntimeFixture(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		path := filepath.Join(t.TempDir(), "codex-text-fixture.sh")
		body := `#!/bin/sh
if [ "$1" = "--version" ]; then
  printf '%s\n' 'codex-cli 0.145.0'
  exit 0
fi
while IFS= read -r line; do
  case "$line" in
  *'"method":"initialize"'*)
    printf '%s\n' '{"jsonrpc":"2.0","id":1,"result":{}}'
    ;;
  *'"method":"thread/start"'*)
    printf '%s\n' '{"jsonrpc":"2.0","id":2,"result":{"threadId":"codex-thread-1"}}'
    ;;
  *'"method":"thread/resume"'*)
    printf '%s\n' '{"jsonrpc":"2.0","id":2,"result":{"threadId":"codex-thread-1"}}'
    ;;
  *'"method":"model/list"'*)
    printf '%s\n' '{"jsonrpc":"2.0","id":3,"result":{"data":[]}}'
    ;;
  *'"method":"permissionProfile/list"'*)
    printf '%s\n' '{"jsonrpc":"2.0","id":4,"result":{"data":[]}}'
    ;;
  *'"method":"turn/start"'*)
    printf '%s\n' '{"jsonrpc":"2.0","id":5,"result":{"turn":{"id":"turn-1","status":"inProgress"}}}'
    printf '%s\n' '{"jsonrpc":"2.0","method":"turn/started","params":{"threadId":"codex-thread-1","turn":{"id":"turn-1"}}}'
    printf '%s\n' '{"jsonrpc":"2.0","method":"item/started","params":{"threadId":"codex-thread-1","turnId":"turn-1","item":{"type":"agentMessage","id":"message-1","text":""}}}'
    printf '%s\n' '{"jsonrpc":"2.0","method":"item/agentMessage/delta","params":{"threadId":"codex-thread-1","turnId":"turn-1","itemId":"message-1","delta":"What deadline should I optimize for?"}}'
    printf '%s\n' '{"jsonrpc":"2.0","method":"item/completed","params":{"threadId":"codex-thread-1","turnId":"turn-1","item":{"type":"agentMessage","id":"message-1","text":"What deadline should I optimize for?"}}}'
    printf '%s\n' '{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"codex-thread-1","turn":{"id":"turn-1","status":"completed"}}}'
    ;;
  esac
done
`
		if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
			t.Fatalf("write codex text fixture: %v", err)
		}
		return path
	}
	return writeWindowsCodexRuntimeFixture(t, false)
}

func writeWindowsCodexRuntimeFixture(t *testing.T, withTools bool) string {
	t.Helper()
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "codex-fixture.go")
	executablePath := filepath.Join(dir, "codex-fixture.exe")
	toolEvents := "\n\t\t\t_ = resumed"
	if withTools {
		toolEvents = `
			if !resumed {
			fmt.Fprintln(writer, "{\"jsonrpc\":\"2.0\",\"method\":\"item/started\",\"params\":{\"threadId\":\"codex-thread-1\",\"turnId\":\"turn-1\",\"item\":{\"type\":\"dynamicToolCall\",\"id\":\"tool-mission\",\"tool\":\"mission_prepare\",\"arguments\":{\"title\":\"Launch plan\",\"final_goal\":\"Coordinate v2 launch\",\"root_goal\":\"Coordinate v2 launch execution\",\"business_direction\":\"Growth\",\"expected_deliverables\":\"Launch brief;Rollout checklist\",\"idempotency_key\":\"mission-from-codex-runtime\"}}}}")
			fmt.Fprintln(writer, "{\"jsonrpc\":\"2.0\",\"method\":\"item/completed\",\"params\":{\"threadId\":\"codex-thread-1\",\"turnId\":\"turn-1\",\"item\":{\"type\":\"dynamicToolCall\",\"id\":\"tool-mission\",\"tool\":\"mission_prepare\",\"status\":\"completed\",\"contentItems\":[]}}}")
			fmt.Fprintln(writer, "{\"jsonrpc\":\"2.0\",\"method\":\"item/started\",\"params\":{\"threadId\":\"codex-thread-1\",\"turnId\":\"turn-1\",\"item\":{\"type\":\"dynamicToolCall\",\"id\":\"tool-coordinator\",\"tool\":\"mission_coordinator_plan_parallel\",\"arguments\":{\"goal\":\"Continue execution with the launch worker\",\"worker_lanes_json\":\"[{\\\"role\\\":\\\"launch worker\\\",\\\"agent_id\\\":\\\"agent-worker\\\",\\\"goal\\\":\\\"Coordinate v2 launch execution\\\",\\\"idempotency_key\\\":\\\"lane-from-codex-runtime\\\"}]\",\"idempotency_key\":\"coordinator-plan-from-codex-runtime\"}}}}")
			fmt.Fprintln(writer, "{\"jsonrpc\":\"2.0\",\"method\":\"item/completed\",\"params\":{\"threadId\":\"codex-thread-1\",\"turnId\":\"turn-1\",\"item\":{\"type\":\"dynamicToolCall\",\"id\":\"tool-coordinator\",\"tool\":\"mission_coordinator_plan_parallel\",\"status\":\"completed\",\"contentItems\":[]}}}")
			}`
	}
	source := `package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

type request struct {
	ID     json.RawMessage ` + "`json:\"id\"`" + `
	Method string          ` + "`json:\"method\"`" + `
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println("codex-cli 0.145.0")
		return
	}
	decoder := json.NewDecoder(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	defer writer.Flush()
	resumed := false
	for {
		var message request
		if decoder.Decode(&message) != nil {
			return
		}
		switch message.Method {
		case "initialize":
			fmt.Fprintf(writer, "{\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{}}\n", message.ID)
		case "thread/start":
			fmt.Fprintf(writer, "{\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"threadId\":\"codex-thread-1\"}}\n", message.ID)
		case "thread/resume":
			resumed = true
			fmt.Fprintf(writer, "{\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"threadId\":\"codex-thread-1\"}}\n", message.ID)
		case "model/list", "permissionProfile/list":
			fmt.Fprintf(writer, "{\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"data\":[]}}\n", message.ID)
		case "turn/start":
			fmt.Fprintf(writer, "{\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"turn\":{\"id\":\"turn-1\",\"status\":\"inProgress\"}}}\n", message.ID)
			fmt.Fprintln(writer, "{\"jsonrpc\":\"2.0\",\"method\":\"turn/started\",\"params\":{\"threadId\":\"codex-thread-1\",\"turn\":{\"id\":\"turn-1\"}}}")` + toolEvents + `
			fmt.Fprintln(writer, "{\"jsonrpc\":\"2.0\",\"method\":\"item/started\",\"params\":{\"threadId\":\"codex-thread-1\",\"turnId\":\"turn-1\",\"item\":{\"type\":\"agentMessage\",\"id\":\"message-1\",\"text\":\"\"}}}")
			fmt.Fprintln(writer, "{\"jsonrpc\":\"2.0\",\"method\":\"item/agentMessage/delta\",\"params\":{\"threadId\":\"codex-thread-1\",\"turnId\":\"turn-1\",\"itemId\":\"message-1\",\"delta\":\"What deadline should I optimize for?\"}}")
			fmt.Fprintln(writer, "{\"jsonrpc\":\"2.0\",\"method\":\"item/completed\",\"params\":{\"threadId\":\"codex-thread-1\",\"turnId\":\"turn-1\",\"item\":{\"type\":\"agentMessage\",\"id\":\"message-1\",\"text\":\"What deadline should I optimize for?\"}}}")
			fmt.Fprintln(writer, "{\"jsonrpc\":\"2.0\",\"method\":\"turn/completed\",\"params\":{\"threadId\":\"codex-thread-1\",\"turn\":{\"id\":\"turn-1\",\"status\":\"completed\"}}}")
		}
		writer.Flush()
	}
}
`
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		t.Fatalf("write codex fixture source: %v", err)
	}
	cmd := exec.Command("go", "build", "-o", executablePath, sourcePath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build codex fixture: %v\n%s", err, output)
	}
	removeWindowsRuntimeFixtureBeforeTempDirCleanup(t, executablePath)
	return executablePath
}

func writePiTextRuntimeFixture(t *testing.T) string {
	t.Helper()
	return buildPiRuntimeFixture(t, piRuntimeFixtureSpec{MessageEndText: "What deadline should I optimize for?"})
}

type piRuntimeFixtureSpec struct {
	RequiredEnvKey   string
	RequiredEnvValue string
	RequiredProvider string
	RequiredModel    string
	MessageEndText   string
	AgentEndText     string
}

func buildPiRuntimeFixture(t *testing.T, spec piRuntimeFixtureSpec) string {
	t.Helper()
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "pi-fixture.go")
	executablePath := filepath.Join(dir, "pi-fixture")
	if runtime.GOOS == "windows" {
		executablePath += ".exe"
	}
	quote := func(value string) string { return fmt.Sprintf("%q", value) }
	source := `package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

const requiredEnvKey = ` + quote(spec.RequiredEnvKey) + `
const requiredEnvValue = ` + quote(spec.RequiredEnvValue) + `
const requiredProvider = ` + quote(spec.RequiredProvider) + `
const requiredModel = ` + quote(spec.RequiredModel) + `
const messageEndText = ` + quote(spec.MessageEndText) + `
const agentEndText = ` + quote(spec.AgentEndText) + `

type request struct {
	ID   string ` + "`json:\"id\"`" + `
	Type string ` + "`json:\"type\"`" + `
}

func hasArgPair(name, value string) bool {
	for i := 1; i+1 < len(os.Args); i++ {
		if os.Args[i] == name && os.Args[i+1] == value {
			return true
		}
	}
	return false
}

func emit(writer *bufio.Writer, value any) {
	_ = json.NewEncoder(writer).Encode(value)
	_ = writer.Flush()
}

func argValue(name, fallback string) string {
	for i := 1; i+1 < len(os.Args); i++ {
		if os.Args[i] == name {
			return os.Args[i+1]
		}
	}
	return fallback
}

func main() {
	for _, argument := range os.Args[1:] {
		if argument == "--version" || argument == "-v" {
			fmt.Println("1.3.0")
			return
		}
	}
	if requiredEnvKey != "" && os.Getenv(requiredEnvKey) != requiredEnvValue {
		os.Exit(2)
	}
	if requiredProvider != "" && !hasArgPair("--provider", requiredProvider) {
		os.Exit(3)
	}
	if requiredModel != "" && !hasArgPair("--model", requiredModel) {
		os.Exit(4)
	}
	provider := argValue("--provider", "fixture")
	modelID := argValue("--model", "pi-test")
	model := map[string]any{"id": modelID, "name": modelID, "provider": provider, "reasoning": true}
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
			if messageEndText != "" {
				emit(writer, map[string]any{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_delta", "delta": messageEndText}})
				emit(writer, map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant", "provider": provider, "model": modelID, "content": []any{map[string]any{"type": "text", "text": messageEndText}}}, "stopReason": "stop"})
			}
			messages := []any{}
			if agentEndText != "" {
				messages = append(messages, map[string]any{"role": "assistant", "provider": provider, "model": modelID, "content": []any{map[string]any{"type": "text", "text": agentEndText}}})
			}
			emit(writer, map[string]any{"type": "agent_end", "messages": messages})
			emit(writer, map[string]any{"type": "agent_settled", "aborted": false})
		case "get_state":
			emit(writer, map[string]any{"id": message.ID, "type": "response", "command": "get_state", "success": true, "data": map[string]any{"sessionId": "pi-session-1", "thinkingLevel": "medium", "model": model}})
		case "get_available_models":
			emit(writer, map[string]any{"id": message.ID, "type": "response", "command": "get_available_models", "success": true, "data": map[string]any{"models": []any{model}}})
		case "get_commands":
			emit(writer, map[string]any{"id": message.ID, "type": "response", "command": "get_commands", "success": true, "data": map[string]any{"commands": []any{}}})
		}
	}
}
`
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		t.Fatalf("write pi fixture source: %v", err)
	}
	cmd := exec.Command("go", "build", "-o", executablePath, sourcePath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build pi fixture: %v\n%s", err, output)
	}
	removeWindowsRuntimeFixtureBeforeTempDirCleanup(t, executablePath)
	return executablePath
}

func writePiAgentEndTextRuntimeFixture(t *testing.T) string {
	t.Helper()
	return buildPiRuntimeFixture(t, piRuntimeFixtureSpec{AgentEndText: "Agent-end reply."})
}

func writePiEmptyRuntimeFixture(t *testing.T) string {
	t.Helper()
	return buildPiRuntimeFixture(t, piRuntimeFixtureSpec{})
}

func writePiRequiresZAIProfileFixture(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		path := filepath.Join(t.TempDir(), "pi-requires-zai-profile.sh")
		body := `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "-v" ]; then
  printf '%s\n' '1.3.0'
  exit 0
fi
if [ "$ZAI_API_KEY" != "token-glm" ]; then
  echo "missing zai key" >&2
  exit 2
fi
case " $* " in
  *" --provider zai "* ) ;;
  *) echo "missing zai provider" >&2; exit 3 ;;
esac
case " $* " in
  *" --model glm-5.2 "* ) ;;
  *) echo "missing glm model" >&2; exit 4 ;;
esac
while IFS= read -r command; do
  id=$(printf '%s' "$command" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
  case "$command" in
    *'"type":"prompt"'*)
      printf '%s\n' "{\"id\":\"$id\",\"type\":\"response\",\"command\":\"prompt\",\"success\":true}"
      printf '%s\n' '{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"glm key ok"}]}}'
      printf '%s\n' '{"type":"agent_end","messages":[]}'
      printf '%s\n' '{"type":"agent_settled","aborted":false}'
      ;;
    *'"type":"get_state"'*)
      printf '%s\n' "{\"id\":\"$id\",\"type\":\"response\",\"command\":\"get_state\",\"success\":true,\"data\":{\"sessionId\":\"pi-session-zai\",\"thinkingLevel\":\"medium\",\"model\":{\"id\":\"glm-5.2\",\"name\":\"GLM 5.2\",\"provider\":\"zai\",\"reasoning\":true}}}"
      ;;
    *'"type":"get_available_models"'*)
      printf '%s\n' "{\"id\":\"$id\",\"type\":\"response\",\"command\":\"get_available_models\",\"success\":true,\"data\":{\"models\":[{\"id\":\"glm-5.2\",\"name\":\"GLM 5.2\",\"provider\":\"zai\",\"reasoning\":true}]}}"
      ;;
    *'"type":"get_commands"'*)
      printf '%s\n' "{\"id\":\"$id\",\"type\":\"response\",\"command\":\"get_commands\",\"success\":true,\"data\":{\"commands\":[]}}"
      ;;
    *'"type":"get_available_models"'*)
      printf '%s\n' "{\"id\":\"$id\",\"type\":\"response\",\"command\":\"get_available_models\",\"success\":true,\"data\":{\"models\":[{\"id\":\"glm-5.2\",\"name\":\"GLM 5.2\",\"provider\":\"zai\",\"reasoning\":true}]}}"
      ;;
    *'"type":"get_commands"'*)
      printf '%s\n' "{\"id\":\"$id\",\"type\":\"response\",\"command\":\"get_commands\",\"success\":true,\"data\":{\"commands\":[]}}"
      ;;
  esac
done
`
		if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
			t.Fatalf("write pi zai fixture: %v", err)
		}
		return path
	}
	return buildPiRuntimeFixture(t, piRuntimeFixtureSpec{
		RequiredEnvKey:   "ZAI_API_KEY",
		RequiredEnvValue: "token-glm",
		RequiredProvider: "zai",
		RequiredModel:    "glm-5.2",
		MessageEndText:   "glm key ok",
	})
}

func jsonBody(body string) *strings.Reader {
	return strings.NewReader(body)
}

func resourceToolIDs(tools []resourceadmin.ToolDTO) map[string]bool {
	ids := map[string]bool{}
	for _, tool := range tools {
		ids[tool.ToolID] = true
	}
	return ids
}

func seededTestResourceCatalog(t *testing.T, ctx context.Context) (toolres.Registry, skillres.Registry) {
	t.Helper()
	tools := toolres.NewMemoryRegistry()
	skills := skillres.NewMemoryRegistry()
	if err := ensureDefaultResourceCatalog(ctx, tools, skills); err != nil {
		t.Fatalf("ensure default resource catalog: %v", err)
	}
	return tools, skills
}

func saveAvailableAgentProfile(t *testing.T, ctx context.Context, profiles *agentcore.PlatformProfileRepository, profile agentcore.AgentProfile) {
	t.Helper()
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save profile %s: %v", profile.ID, err)
	}
	if err := profiles.SaveAvailability(ctx, agentcore.AgentAvailability{
		AgentProfileID:  profile.ID,
		AgentInstanceID: testRuntimeInstanceID(profile.ID),
		Status:          agentcore.AvailabilityAvailable,
		UpdatedAt:       time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save availability %s: %v", profile.ID, err)
	}
}

func testRuntimeInstanceID(profileID agentcore.AgentProfileID) agentcore.AgentInstanceID {
	return agentcore.AgentInstanceID(profileID.String() + ":runtime")
}

func agentToolRefs(profile agentcore.AgentProfile) map[string]bool {
	out := map[string]bool{}
	for _, ref := range profile.ToolRefs {
		out[string(ref.ToolID)] = true
	}
	return out
}

func resourceSkillIDs(skills []resourceadmin.SkillDTO) map[string]bool {
	ids := map[string]bool{}
	for _, skill := range skills {
		ids[skill.SkillID] = true
	}
	return ids
}

func jsonPath(path string) string {
	return strings.ReplaceAll(path, `\`, `\\`)
}

func authorizeServerTestRequest(req *http.Request) {
	if req == nil {
		return
	}
	if req.Header.Get("X-Orrery-Internal-Viewer-Authorization") != "" {
		return
	}
	hasHeaderViewer := req.Header.Get("X-User-ID") != "" || req.Header.Get("X-Agent-ID") != ""
	hasQueryViewer := req.URL != nil && (req.URL.Query().Get("viewer_id") != "" || req.URL.Query().Get("agent_id") != "")
	if !hasHeaderViewer && !hasQueryViewer {
		return
	}
	req.Header.Set("X-Orrery-Internal-Viewer-Authorization", testInternalViewerAuthorization)
	req.RemoteAddr = "127.0.0.1:12345"
	if req.Header.Get("X-Permission-Scope") == "" {
		req.Header.Set("X-Permission-Scope", strings.Join(bootstrapAdminScopes(), ","))
	}
	viewerKind := strings.TrimSpace(req.Header.Get("X-Viewer-Kind"))
	if (viewerKind == "user" || viewerKind == "human") && req.Header.Get("X-User-ID") == userauth.SuperadminUsername {
		req.Header.Set("X-User-ID", userauth.SuperadminAgentProfileID)
		req.Header.Set("X-Viewer-Kind", "system")
		req.Header.Del("X-Agent-Profile-ID")
	}
}

func loginProductionSuperadmin(t testing.TB, handler http.Handler) *http.Cookie {
	t.Helper()
	begin := httptest.NewRequest(http.MethodPost, "/api/auth/login/begin", jsonBody(`{"username":"superadmin"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, begin)
	if rec.Code != http.StatusOK {
		t.Fatalf("begin superadmin login status = %d body = %s", rec.Code, rec.Body.String())
	}
	var challenge userauth.LoginChallengeDTO
	if err := json.NewDecoder(rec.Body).Decode(&challenge); err != nil {
		t.Fatalf("decode superadmin login challenge: %v", err)
	}
	complete := httptest.NewRequest(http.MethodPost, "/api/auth/login/complete", jsonBody(`{"username":"superadmin","challenge_id":"`+challenge.ChallengeID+`","password":"`+userauth.SuperadminPassword()+`"}`))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, complete)
	if rec.Code != http.StatusOK {
		t.Fatalf("complete superadmin login status = %d body = %s", rec.Code, rec.Body.String())
	}
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == "orrery_access_token" && cookie.Value != "" {
			return cookie
		}
	}
	t.Fatal("superadmin login did not issue an access cookie")
	return nil
}

func serveServerTestHTTP(t testing.TB, handler http.Handler, rec *httptest.ResponseRecorder, req *http.Request) {
	t.Helper()
	authorizeServerTestRequest(req)
	handler.ServeHTTP(rec, req)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func missionListContains(page missioncontrol.MissionListDTO, missionID string) bool {
	for _, mission := range page.Missions {
		if mission.MissionID == missionID {
			return true
		}
	}
	return false
}

func saveRawAgentProfile(ctx context.Context, unit *uow.UnitOfWork, profile agentcore.AgentProfile) error {
	if unit == nil {
		return fmt.Errorf("unit of work is required")
	}
	payload, err := json.Marshal(profile)
	if err != nil {
		return err
	}
	return unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		tx.Put("agent_profiles", profile.ID.String(), db.Record{
			"profile_id": profile.ID.String(),
			"enabled":    profile.Enabled,
			"kind":       string(profile.AgentKind),
			"payload":    string(payload),
		})
		return nil
	})
}

func opaqueID(prefix string) *regexp.Regexp {
	return regexp.MustCompile("^" + regexp.QuoteMeta(prefix) + "-[0-9a-f]{32}$")
}
