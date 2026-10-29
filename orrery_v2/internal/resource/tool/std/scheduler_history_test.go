package std

import (
	"testing"
	"time"
)

func TestSchedulerHistoryIsPublishedToExistingReadTool(t *testing.T) {
	definition := definitionByID(Definitions(time.Unix(0, 0).UTC()))[ScheduledTaskList]
	if len(definition.Permissions) != 1 || definition.Permissions[0].Scope != PermissionSchedulerRead {
		t.Fatalf("history permission=%+v", definition.Permissions)
	}
	properties := definition.InputSchema["properties"].(map[string]any)
	view := properties["view"].(map[string]any)
	for _, value := range []string{"tasks", "runs", "artifact"} {
		if !enumContains(view, value) {
			t.Fatalf("view %s not published", value)
		}
	}
	for _, field := range []string{"scheduled_task_id", "scheduled_run_id", "created_from", "created_before", "artifact_id"} {
		if properties[field] == nil {
			t.Fatalf("missing published argument %s", field)
		}
	}
	output := definition.OutputSchema["properties"].(map[string]any)
	for _, field := range []string{"runs", "active_runs", "content", "has_more"} {
		if output[field] == nil {
			t.Fatalf("missing published output %s", field)
		}
	}
}
