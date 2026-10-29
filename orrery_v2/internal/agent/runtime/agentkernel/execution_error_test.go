package agentkernel

import (
	"errors"
	"orrery_v2/internal/agent/llm"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestPrepareCallsDistinguishesInvalidToolArgumentsSchemaAndDeniedBinding(t *testing.T) {
	for _, test := range []struct {
		name, arguments, toolName, code string
		cause                           error
	}{
		{"json", "invalid secret=private", "knowledge_search", "tool_arguments_invalid", ErrProtocol},
		{"schema", `{"unknown":"private"}`, "knowledge_search", "tool_schema_invalid", ErrProtocol},
		{"unbound", `{}`, "unbound", "tool_unbound", ErrToolDenied},
	} {
		t.Run(test.name, func(t *testing.T) {
			tools, _, err := prepareTools([]agentcore.RuntimeToolBinding{searchBinding()})
			if err != nil {
				t.Fatal(err)
			}
			runner := &Runner{tools: tools}
			call := toolCall("call-1", test.arguments)
			call.Function.Name = test.toolName
			_, err = runner.prepareCalls([]llm.ToolCall{call}, map[string]bool{})
			if !errors.Is(err, test.cause) || !strings.HasSuffix(err.Error(), ": "+test.code) || strings.Contains(err.Error(), "private") {
				t.Fatalf("failure = %v", err)
			}
		})
	}
}
