package std

import (
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func TestTodoToolDefinitionsArePublishedThroughSystemMCPServer(t *testing.T) {
	definitions := Definitions(time.Unix(0, 0).UTC())
	byID := definitionByID(definitions)
	system := SystemMCPServerDefinition(time.Unix(0, 0).UTC())
	exposed := map[string]bool{}
	for _, name := range system.MCP.ExposedTools {
		exposed[name] = true
	}

	cases := []struct {
		id         agentcore.ToolID
		permission string
	}{
		{TodoCreate, PermissionTodoWrite},
		{TodoUpdate, PermissionTodoWrite},
		{TodoDelete, PermissionTodoWrite},
		{TodoClaim, PermissionTodoWrite},
		{TodoMove, PermissionTodoWrite},
		{TodoComplete, PermissionTodoWrite},
		{TodoCancel, PermissionTodoWrite},
		{TodoReopen, PermissionTodoWrite},
		{TodoList, PermissionTodoRead},
		{IdeaCreate, PermissionIdeaWrite},
		{IdeaUpdate, PermissionIdeaWrite},
		{IdeaDelete, PermissionIdeaWrite},
		{IdeaArchive, PermissionIdeaWrite},
		{IdeaRestore, PermissionIdeaWrite},
		{IdeaTransition, PermissionIdeaWrite},
		{IdeaVote, PermissionIdeaWrite},
		{IdeaConvertToTodo, PermissionIdeaWrite},
		{IdeaList, PermissionIdeaRead},
	}
	for _, tc := range cases {
		definition := byID[tc.id]
		if definition.ID == "" {
			t.Fatalf("todo tool %q was not published", tc.id)
		}
		if len(definition.Permissions) != 1 || definition.Permissions[0].Scope != tc.permission {
			t.Fatalf("%s permission = %+v, want %q", tc.id, definition.Permissions, tc.permission)
		}
		if len(definition.InputSchema) == 0 || len(definition.OutputSchema) == 0 {
			t.Fatalf("%s schemas should be populated: input=%+v output=%+v", tc.id, definition.InputSchema, definition.OutputSchema)
		}
		if !exposed[string(tc.id)] {
			t.Fatalf("%s should be exposed through mcp.system", tc.id)
		}
	}
	if !schemaRequires(byID[TodoCreate].InputSchema, "title") {
		t.Fatalf("todo_create should require title: %+v", byID[TodoCreate].InputSchema)
	}
	if schemaHasProperty(byID[TodoCreate].InputSchema, "proposer_name") || !schemaHasProperty(byID[TodoCreate].InputSchema, "tags") {
		t.Fatalf("todo_create should expose tags but not proposer_name: %+v", byID[TodoCreate].InputSchema)
	}
	if !schemaRequires(byID[TodoUpdate].InputSchema, "todo_id") {
		t.Fatalf("todo_update should require todo_id: %+v", byID[TodoUpdate].InputSchema)
	}
	if schemaHasProperty(byID[TodoUpdate].InputSchema, "proposer_name") || schemaHasProperty(byID[TodoUpdate].InputSchema, "blocked_reason") || !schemaHasProperty(byID[TodoUpdate].InputSchema, "tags") {
		t.Fatalf("todo_update should expose tags but not lifecycle-owned fields: %+v", byID[TodoUpdate].InputSchema)
	}
	if !schemaRequires(byID[TodoMove].InputSchema, "todo_id") || !schemaRequires(byID[TodoMove].InputSchema, "status") {
		t.Fatalf("todo_move should require todo_id and status: %+v", byID[TodoMove].InputSchema)
	}
	if !schemaRequires(byID[IdeaCreate].InputSchema, "title") {
		t.Fatalf("idea_create should require title: %+v", byID[IdeaCreate].InputSchema)
	}
	if schemaHasProperty(byID[IdeaCreate].InputSchema, "proposer_name") || !schemaHasProperty(byID[IdeaCreate].InputSchema, "tags") {
		t.Fatalf("idea_create should expose tags but not proposer_name: %+v", byID[IdeaCreate].InputSchema)
	}
	if schemaHasProperty(byID[IdeaUpdate].InputSchema, "proposer_name") || !schemaHasProperty(byID[IdeaUpdate].InputSchema, "tags") {
		t.Fatalf("idea_update should expose tags but not proposer_name: %+v", byID[IdeaUpdate].InputSchema)
	}
	if !schemaRequires(byID[IdeaConvertToTodo].InputSchema, "idea_id") {
		t.Fatalf("idea_convert_to_todo should require idea_id: %+v", byID[IdeaConvertToTodo].InputSchema)
	}
	if !schemaRequires(byID[IdeaTransition].InputSchema, "idea_id") || !schemaRequires(byID[IdeaTransition].InputSchema, "status") {
		t.Fatalf("idea_transition should require idea_id and status: %+v", byID[IdeaTransition].InputSchema)
	}
	transitionProperties, _ := byID[IdeaTransition].InputSchema["properties"].(map[string]any)
	transitionStatus, _ := transitionProperties["status"].(map[string]any)
	if enumContains(transitionStatus, "converted") {
		t.Fatalf("idea_transition must reserve converted for idea_convert_to_todo: %+v", byID[IdeaTransition].InputSchema)
	}
	if schemaHasProperty(byID[IdeaUpdate].InputSchema, "status") {
		t.Fatalf("idea_update must not bypass lifecycle transitions: %+v", byID[IdeaUpdate].InputSchema)
	}
}
