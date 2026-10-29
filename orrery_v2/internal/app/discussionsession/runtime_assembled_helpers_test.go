package discussionsession

import (
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func externalCLIUserPrompt(t *testing.T, messages []agentcore.RuntimeChatMessage) string {
	t.Helper()
	if len(messages) != 1 || messages[0].Role != "user" {
		t.Fatalf("external CLI prompt must be one user message: %+v", messages)
	}
	if !strings.Contains(messages[0].Content, "<current_user_input") ||
		!strings.Contains(messages[0].Content, "<discussion_context>") && !strings.Contains(messages[0].Content, "<discussion_history") {
		t.Fatalf("external CLI prompt is not structured:\n%s", messages[0].Content)
	}
	return messages[0].Content
}

func runtimeDiscussionToolBindingNames(bindings []agentcore.RuntimeToolBinding) []string {
	result := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		name := string(binding.ToolID)
		switch name {
		case "", "list_skills", "load_skill", "list_skill_resources", "load_resource":
			continue
		default:
			result = append(result, name)
		}
	}
	return result
}

func runtimeHasToolBinding(bindings []agentcore.RuntimeToolBinding, id agentcore.ToolID) bool {
	for _, binding := range bindings {
		if binding.ToolID == id {
			return true
		}
	}
	return false
}
