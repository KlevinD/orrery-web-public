package agentkernel

import (
	"context"
	"orrery_v2/internal/agent/llm"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
)

func TestToolSchemaReasonReachesModelWithoutEnteringDisplayEvents(t *testing.T) {
	steps, executed := 0, 0
	m := &modelFunc{stream: func(_ context.Context, messages []*llm.Message, _ ...llm.Option) (*llm.MessageStream, error) {
		steps++
		switch steps {
		case 1:
			return llm.NewMessageStream([]*llm.Message{reply("", "tool_calls", toolCall("invalid", `{"kb":"private-invalid-value"}`))}), nil
		case 2:
			feedback := messages[len(messages)-1].Content
			if executed != 0 || !strings.Contains(feedback, "validation_reason") || !strings.Contains(feedback, "private-invalid-value") || !strings.Contains(feedback, "public") {
				t.Fatal("model did not receive the actionable validator reason before dispatch")
			}
			return llm.NewMessageStream([]*llm.Message{reply("", "tool_calls", toolCall("corrected", `{"kb":"public"}`))}), nil
		default:
			return llm.NewMessageStream([]*llm.Message{reply("Completed.", "stop")}), nil
		}
	}}
	runner, input := openRunner(t, m, func(c *Config, req *agentcore.RuntimeRequest) {
		checkpointConfig(t, c)
		req.ToolBindings[0].InputSchema = map[string]any{"type": "object", "required": []string{"kb"}, "properties": map[string]any{"kb": map[string]any{"type": "string", "enum": []string{"public"}}}, "additionalProperties": false}
		c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(_ context.Context, req runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
			if req.CallID != "corrected" {
				t.Fatal("invalid request executed")
			}
			executed++
			return runtimecore.RuntimeToolCallResult{Output: map[string]any{"answer": "done"}}, nil
		})
	})
	result, err := runner.Run(context.Background(), input, func(event agentcore.SessionEvent) error {
		if event.ToolResult != nil {
			for _, content := range event.ToolResult.Content {
				if strings.Contains(content.Text, "validation_reason") || strings.Contains(content.Text, "private-invalid-value") {
					t.Fatal("private validation details entered display events")
				}
			}
		}
		return event.Validate()
	})
	if err != nil || executed != 1 || steps != 3 || result.ResultText != "Completed." || runner.checkpoint.Phase != CheckpointCompleted {
		t.Fatalf("result=%+v err=%v steps=%d executed=%d", result, err, steps, executed)
	}
	found := false
	for _, message := range runner.checkpoint.Messages {
		if message.ToolCallID == "invalid" && strings.Contains(message.Content, "validation_reason") {
			found = true
		}
	}
	if !found {
		t.Fatal("private reason was not retained for checkpoint recovery")
	}
}
