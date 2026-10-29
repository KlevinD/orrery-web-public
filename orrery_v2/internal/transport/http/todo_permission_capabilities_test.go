package httptransport

import (
	"net/http"
	"testing"
)

func TestDefaultPermissionCapabilitiesExposeTodoRoutes(t *testing.T) {
	capabilities := defaultPermissionCapabilities()

	assertCapability(t, capabilities, http.MethodGet, "/api/todos", "todo.read")
	assertCapability(t, capabilities, http.MethodPost, "/api/todo-drafts/suggest", "todo.write")
	assertCapability(t, capabilities, http.MethodPost, "/api/todos", "todo.write")
	assertCapability(t, capabilities, http.MethodPost, "/api/todos/{todo_id}/attachments", "todo.write")
	assertCapability(t, capabilities, http.MethodGet, "/api/todos/{todo_id}/attachments/{attachment_id}", "todo.read")
	assertCapability(t, capabilities, http.MethodDelete, "/api/todos/{todo_id}/attachments/{attachment_id}", "todo.write")
	assertCapability(t, capabilities, http.MethodGet, "/api/todos/{todo_id}", "todo.read")
	assertCapability(t, capabilities, http.MethodPatch, "/api/todos/{todo_id}", "todo.write")
	assertCapability(t, capabilities, http.MethodDelete, "/api/todos/{todo_id}", "todo.write")
	assertCapability(t, capabilities, http.MethodGet, "/api/todos/{todo_id}/comments", "todo.read")
	assertCapability(t, capabilities, http.MethodPost, "/api/todos/{todo_id}/comments", "todo.write")
	assertCapability(t, capabilities, http.MethodGet, "/api/todos/{todo_id}/missions", "todo.read")
	assertCapability(t, capabilities, http.MethodGet, "/api/todos/{todo_id}/progress", "todo.read")
	assertCapability(t, capabilities, http.MethodGet, "/api/todos/{todo_id}/discussion", "todo.read")
	assertCapability(t, capabilities, http.MethodGet, "/api/todos/{todo_id}/execution-plans", "todo.read")
	assertCapability(t, capabilities, http.MethodPost, "/api/todos/{todo_id}/automation/check", "todo.write")
	assertCapability(t, capabilities, http.MethodPost, "/api/todos/{todo_id}/automation/start", "todo.write")
	assertCapability(t, capabilities, http.MethodPost, "/api/todos/{todo_id}/execution-attempts", "todo.write")
	assertCapability(t, capabilities, http.MethodPost, "/api/todos/{todo_id}/claim", "todo.write")
	assertCapability(t, capabilities, http.MethodPost, "/api/todos/{todo_id}/move", "todo.write")
	assertCapability(t, capabilities, http.MethodPost, "/api/todos/{todo_id}/complete", "todo.write")
	assertCapability(t, capabilities, http.MethodPost, "/api/todos/{todo_id}/cancel", "todo.write")
	assertCapability(t, capabilities, http.MethodPost, "/api/todos/{todo_id}/reopen", "todo.write")
	assertCapability(t, capabilities, http.MethodGet, "/api/ideas", "idea.read")
	assertCapability(t, capabilities, http.MethodPost, "/api/ideas", "idea.write")
	assertCapability(t, capabilities, http.MethodGet, "/api/ideas/{idea_id}", "idea.read")
	assertCapability(t, capabilities, http.MethodPatch, "/api/ideas/{idea_id}", "idea.write")
	assertCapability(t, capabilities, http.MethodDelete, "/api/ideas/{idea_id}", "idea.write")
	assertCapability(t, capabilities, http.MethodGet, "/api/ideas/{idea_id}/comments", "idea.read")
	assertCapability(t, capabilities, http.MethodPost, "/api/ideas/{idea_id}/comments", "idea.write")
	assertCapability(t, capabilities, http.MethodPost, "/api/ideas/{idea_id}/archive", "idea.write")
	assertCapability(t, capabilities, http.MethodPost, "/api/ideas/{idea_id}/restore", "idea.write")
	assertCapability(t, capabilities, http.MethodPost, "/api/ideas/{idea_id}/convert-to-todo", "idea.write")

	ideaList := findCapabilityInCatalog(t, capabilities, http.MethodGet, "/api/ideas", "idea.read")
	if ideaList.CategoryZh != "想法" || ideaList.CategoryEn != "Ideas" || ideaList.LabelZh != "想法列表" {
		t.Fatalf("idea list capability labels = %+v", ideaList)
	}
	ideaUpdate := findCapabilityInCatalog(t, capabilities, http.MethodPatch, "/api/ideas/{idea_id}", "idea.write")
	if ideaUpdate.LabelZh != "编辑想法" {
		t.Fatalf("idea update capability label = %+v", ideaUpdate)
	}
	ideaConvert := findCapabilityInCatalog(t, capabilities, http.MethodPost, "/api/ideas/{idea_id}/convert-to-todo", "idea.write")
	if ideaConvert.LabelZh != "转为待办" {
		t.Fatalf("idea convert capability label = %+v", ideaConvert)
	}
}
