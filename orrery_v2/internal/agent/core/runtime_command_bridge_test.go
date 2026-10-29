package core

import (
	"encoding/json"
	"testing"
)

// The wire contract is owned by Orrery. Exercise every command through JSON
// and reject crossed union payloads before a provider can receive them.
func TestRuntimeCommandWireContract(t *testing.T) {
	payloads := map[string]string{
		"send":               `{"content":[{"kind":"text","text":"review the release"}],"metadata":{"client_msg_id":"web-release-21"}}`,
		"cancel":             `{"target":"session","reason":"operator stopped the release"}`,
		"steer":              `{"content":[{"kind":"text","text":"include license evidence"}]}`,
		"answer_permission":  `{"request_id":"approve-21","decision":"denied"}`,
		"answer_auth":        `{"request_id":"auth-21","method_id":"device"}`,
		"acknowledge":        `{"node_id":"notice-21"}`,
		"set_mode":           `{"mode":"review"}`,
		"set_model":          `{"model":"release-reviewer"}`,
		"set_config_option":  `{"option_id":"effort","value":"medium"}`,
		"rewind":             `{"num_turns":3}`,
		"list_checkpoints":   `{}`,
		"query_session_info": `{"kind":"session_cost"}`,
	}
	for kind, payload := range payloads {
		t.Run(kind, func(t *testing.T) {
			wire := map[string]json.RawMessage{
				"command_id": json.RawMessage(`"web-release-21"`),
				"kind":       json.RawMessage(`"` + kind + `"`),
				kind:         json.RawMessage(payload),
			}
			decode := func(fields map[string]json.RawMessage) RuntimeCommand {
				t.Helper()
				data, err := json.Marshal(fields)
				if err != nil {
					t.Fatal(err)
				}
				var command RuntimeCommand
				if err := json.Unmarshal(data, &command); err != nil {
					t.Fatal(err)
				}
				return command
			}
			command := decode(wire)
			if kind == "answer_auth" {
				if err := command.Validate(); !IsRuntimeBridgeError(err, RuntimeBridgeInvalidPayload) {
					t.Fatalf("serialized auth must require process-local credentials: %v", err)
				}
				command.AnswerAuth.Credentials = json.RawMessage(`{"token":"process-local-21"}`)
			}
			if err := command.Validate(); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(command)
			if err != nil {
				t.Fatal(err)
			}
			var roundtrip map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &roundtrip); err != nil {
				t.Fatal(err)
			}
			if len(roundtrip) != 3 || string(roundtrip["kind"]) != `"`+kind+`"` || roundtrip[kind] == nil {
				t.Fatalf("wire shape changed: %s", encoded)
			}
			if jsonContainsString(encoded, "process-local-21") {
				t.Fatal("auth credentials leaked")
			}
			delete(wire, kind)
			if err := decode(wire).Validate(); !IsRuntimeBridgeError(err, RuntimeBridgeInvalidPayload) {
				t.Fatalf("missing payload accepted: %v", err)
			}
			for other, foreign := range payloads {
				if other == kind {
					continue
				}
				wire[other] = json.RawMessage(foreign)
				if err := decode(wire).Validate(); !IsRuntimeBridgeError(err, RuntimeBridgeInvalidPayload) {
					t.Fatalf("%s payload accepted for %s: %v", other, kind, err)
				}
				wire[kind] = json.RawMessage(payload)
				if err := decode(wire).Validate(); !IsRuntimeBridgeError(err, RuntimeBridgeInvalidPayload) {
					t.Fatalf("mixed %s/%s union accepted: %v", kind, other, err)
				}
				delete(wire, kind)
				delete(wire, other)
			}
		})
	}
}

func TestRuntimeBridgeCommandValidationFailsClosed(t *testing.T) {
	tests := []RuntimeCommand{
		{},
		{CommandID: "c", Kind: "invoke", Send: &SendCommand{}},
		{CommandID: "c", Kind: RuntimeCommandSend, Send: &SendCommand{}, Cancel: &CancelCommand{}},
		{CommandID: "c", Kind: RuntimeCommandSend, Cancel: &CancelCommand{}},
		{CommandID: "c", Kind: RuntimeCommandCancel, Cancel: &CancelCommand{Target: CancelTargetTool}},
		{CommandID: "c", Kind: RuntimeCommandRewind, Rewind: &RewindCommand{}},
		{CommandID: "c", Kind: RuntimeCommandAnswerAuth, AnswerAuth: &AnswerAuthCommand{RequestID: "a", MethodID: "m", Credentials: []byte("not-json")}},
	}
	for i, command := range tests {
		if err := command.Validate(); !IsRuntimeBridgeError(err, RuntimeBridgeInvalidCommand) && !IsRuntimeBridgeError(err, RuntimeBridgeInvalidPayload) {
			t.Fatalf("case %d error = %v", i, err)
		}
	}
}

func TestRuntimeBridgeAuthFingerprintDetectsCredentialConflictWithoutSerializingCredential(t *testing.T) {
	command := func(token string) RuntimeCommand {
		return RuntimeCommand{CommandID: "c", Kind: RuntimeCommandAnswerAuth, AnswerAuth: &AnswerAuthCommand{RequestID: "a", MethodID: "oauth", Credentials: json.RawMessage(`{"token":"` + token + `"}`)}}
	}
	first, err := command("one").Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	second, err := command("two").Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("different credentials must conflict")
	}
	wire, err := json.Marshal(command("one"))
	if err != nil {
		t.Fatal(err)
	}
	if string(wire) == "" || jsonContainsString(wire, "one") {
		t.Fatalf("credential leaked into serialized command: %s", wire)
	}
}

func TestRuntimeBridgeUnknownCapabilitiesDefaultUnsupported(t *testing.T) {
	command := RuntimeCommand{CommandID: "c", Kind: RuntimeCommandSend, Send: &SendCommand{Content: []ContentBlock{{Kind: ContentBlockText, Text: "hello"}}}}
	if (Capabilities{}).Supports(command) {
		t.Fatal("zero capabilities must not optimistically support send")
	}
	capabilities := Capabilities{SupportedCommands: CommandSet{Send: true}, PromptBlocks: BlockSet{Text: true}}
	if !capabilities.Supports(command) {
		t.Fatal("advertised text send should be supported")
	}
	invalid := RuntimeCommand{CommandID: "c", Kind: RuntimeCommandSend}
	if capabilities.Supports(invalid) {
		t.Fatal("invalid command must fail closed without panic")
	}
}

func TestRuntimeBridgeSessionConfigRejectsPathsCredentialsAndArgv(t *testing.T) {
	valid := SessionConfig{WorkspaceRef: "workspace-1", MCPServers: []MCPServerSpec{{Name: "tools", Transport: MCPTransportStdio, CommandRef: "mcp-launch-1"}}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	invalid := []SessionConfig{
		{WorkspaceRef: `C:\\workspace`},
		{WorkspaceRef: "workspace-1", MCPServers: []MCPServerSpec{{Name: "tools", Transport: MCPTransportStdio, CommandRef: `C:\\mcp.exe`}}},
		{WorkspaceRef: "workspace-1", MCPServers: []MCPServerSpec{{Name: "tools", Transport: MCPTransportStdio, CommandRef: "mcp --flag"}}},
		{WorkspaceRef: "workspace-1", MCPServers: []MCPServerSpec{{Name: "tools", Transport: MCPTransportHTTP, Endpoint: "https://user:secret@example.test/mcp"}}},
	}
	for i, config := range invalid {
		if err := config.Validate(); !IsRuntimeBridgeError(err, RuntimeBridgeInvalidPayload) {
			t.Fatalf("case %d error = %v", i, err)
		}
	}
}

func TestRuntimeBridgeReceiptSemantics(t *testing.T) {
	valid := []CommandReceipt{{}, {Accepted: true, Admission: AdmissionStarted, TurnGeneration: 1}, {Accepted: true, Admission: AdmissionQueued, TurnGeneration: 2}, {Accepted: true, Admission: AdmissionNoTurn}}
	for _, receipt := range valid {
		if err := receipt.Validate(); err != nil {
			t.Fatalf("valid receipt %#v: %v", receipt, err)
		}
	}
	invalid := []CommandReceipt{{Admission: AdmissionNoTurn}, {Accepted: true, Admission: AdmissionStarted}, {Accepted: true, Admission: "future"}}
	for _, receipt := range invalid {
		if err := receipt.Validate(); err == nil {
			t.Fatalf("invalid receipt accepted: %#v", receipt)
		}
	}
}

func jsonContainsString(data []byte, value string) bool {
	var decoded any
	if json.Unmarshal(data, &decoded) != nil {
		return false
	}
	return containsString(decoded, value)
}

func containsString(value any, want string) bool {
	switch typed := value.(type) {
	case string:
		return typed == want
	case []any:
		for _, child := range typed {
			if containsString(child, want) {
				return true
			}
		}
	case map[string]any:
		for _, child := range typed {
			if containsString(child, want) {
				return true
			}
		}
	}
	return false
}
