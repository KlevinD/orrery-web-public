package std

import "testing"

func TestTodoFeedbackOutputSchemaIncludesProtectedAttachments(t *testing.T) {
	properties := todoItemProperties()
	for _, key := range []string{"feedback", "attachments"} {
		if properties[key] == nil {
			t.Fatalf("missing output property %s", key)
		}
	}
	feedback := properties["feedback"].(map[string]any)["properties"].(map[string]any)
	if feedback["attachments"] == nil || feedback["body"] == nil {
		t.Fatal("feedback schema incomplete")
	}
	file := properties["attachments"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	if file["storage_path"] != nil || file["download_url"] == nil {
		t.Fatal("attachment schema leaks storage or omits protected URL")
	}
}
