package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/contextgraph"
	todoapp "orrery_v2/internal/app/todo"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/graphprotocol"
	"orrery_v2/internal/platform/testkit"
)

func TestProductionTodoAndIdeaWritesPlanningOntologyContextGraph(t *testing.T) {
	root := t.TempDir()
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", persistentProductionTestDatabaseURLs(root))
	selectTestServerConfig(t)
	handler, cleanup, err := newServerHandler(context.Background(), false)
	if err != nil {
		t.Fatalf("newServerHandler production: %v", err)
	}
	t.Cleanup(cleanup)

	todoReq := httptest.NewRequest(http.MethodPost, "/api/todos", strings.NewReader(`{"title":"Track ontology todo","description":"Project direct todo lifecycle","status":"inbox","tags":["ontology"]}`))
	todoReq.Header.Set("X-User-ID", "superadmin")
	todoReq.Header.Set("X-Viewer-Kind", "user")
	todoReq.Header.Set("X-Display-Name", "Super Admin")
	todoRec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, todoRec, todoReq)
	if todoRec.Code != http.StatusCreated {
		t.Fatalf("create todo status = %d body = %s", todoRec.Code, todoRec.Body.String())
	}
	var createdTodo todoapp.TodoItem
	if err := json.NewDecoder(todoRec.Body).Decode(&createdTodo); err != nil {
		t.Fatalf("decode created todo: %v", err)
	}
	todoCommentReq := httptest.NewRequest(http.MethodPost, "/api/todos/"+createdTodo.TodoID+"/comments", strings.NewReader(`{"body":"Direct todo comment"}`))
	todoCommentReq.Header.Set("X-User-ID", "superadmin")
	todoCommentReq.Header.Set("X-Viewer-Kind", "user")
	todoCommentReq.Header.Set("X-Display-Name", "Super Admin")
	todoCommentRec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, todoCommentRec, todoCommentReq)
	if todoCommentRec.Code != http.StatusCreated {
		t.Fatalf("create todo comment status = %d body = %s", todoCommentRec.Code, todoCommentRec.Body.String())
	}
	var todoComment todoapp.IssueComment
	if err := json.NewDecoder(todoCommentRec.Body).Decode(&todoComment); err != nil {
		t.Fatalf("decode todo comment: %v", err)
	}

	deleteIdeaCreateReq := httptest.NewRequest(http.MethodPost, "/api/ideas", strings.NewReader(`{"title":"Delete ontology idea","content":"Deleted ideas still go through ontology","status":"draft"}`))
	deleteIdeaCreateReq.Header.Set("X-User-ID", "superadmin")
	deleteIdeaCreateReq.Header.Set("X-Viewer-Kind", "user")
	deleteIdeaCreateReq.Header.Set("X-Display-Name", "Super Admin")
	deleteIdeaCreateRec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, deleteIdeaCreateRec, deleteIdeaCreateReq)
	if deleteIdeaCreateRec.Code != http.StatusCreated {
		t.Fatalf("create deleted idea status = %d body = %s", deleteIdeaCreateRec.Code, deleteIdeaCreateRec.Body.String())
	}
	var deletedIdea todoapp.IdeaItem
	if err := json.NewDecoder(deleteIdeaCreateRec.Body).Decode(&deletedIdea); err != nil {
		t.Fatalf("decode deleted idea: %v", err)
	}
	deleteIdeaReq := httptest.NewRequest(http.MethodDelete, "/api/ideas/"+deletedIdea.IdeaID, nil)
	deleteIdeaReq.Header.Set("X-User-ID", "superadmin")
	deleteIdeaReq.Header.Set("X-Viewer-Kind", "user")
	deleteIdeaReq.Header.Set("X-Display-Name", "Super Admin")
	deleteIdeaRec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, deleteIdeaRec, deleteIdeaReq)
	if deleteIdeaRec.Code != http.StatusNoContent {
		t.Fatalf("delete idea status = %d body = %s", deleteIdeaRec.Code, deleteIdeaRec.Body.String())
	}

	req := httptest.NewRequest(http.MethodPost, "/api/ideas", strings.NewReader(`{"title":"Capture ontology idea","content":"Use planning ontology for ideas","tags":["ontology","planning"]}`))
	req.Header.Set("X-User-ID", "superadmin")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Display-Name", "Super Admin")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create idea status = %d body = %s", rec.Code, rec.Body.String())
	}
	var created todoapp.IdeaItem
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode created idea: %v", err)
	}
	ideaCommentReq := httptest.NewRequest(http.MethodPost, "/api/ideas/"+created.IdeaID+"/comments", strings.NewReader(`{"body":"Idea comment for conversion"}`))
	ideaCommentReq.Header.Set("X-User-ID", "superadmin")
	ideaCommentReq.Header.Set("X-Viewer-Kind", "user")
	ideaCommentReq.Header.Set("X-Display-Name", "Super Admin")
	ideaCommentRec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, ideaCommentRec, ideaCommentReq)
	if ideaCommentRec.Code != http.StatusCreated {
		t.Fatalf("create idea comment status = %d body = %s", ideaCommentRec.Code, ideaCommentRec.Body.String())
	}
	var ideaComment todoapp.IssueComment
	if err := json.NewDecoder(ideaCommentRec.Body).Decode(&ideaComment); err != nil {
		t.Fatalf("decode idea comment: %v", err)
	}
	directConvertedReq := httptest.NewRequest(http.MethodPost, "/api/ideas/"+created.IdeaID+"/transition", strings.NewReader(`{"status":"converted","decision_reason":"Bypass conversion"}`))
	directConvertedReq.Header.Set("X-User-ID", "superadmin")
	directConvertedReq.Header.Set("X-Viewer-Kind", "user")
	directConvertedReq.Header.Set("X-Display-Name", "Super Admin")
	directConvertedRec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, directConvertedRec, directConvertedReq)
	if directConvertedRec.Code != http.StatusBadRequest {
		t.Fatalf("direct converted transition status = %d body = %s", directConvertedRec.Code, directConvertedRec.Body.String())
	}
	unapprovedConvertReq := httptest.NewRequest(http.MethodPost, "/api/ideas/"+created.IdeaID+"/convert-to-todo", strings.NewReader(`{"decision_reason":"Bypass governance"}`))
	unapprovedConvertReq.Header.Set("X-User-ID", "superadmin")
	unapprovedConvertReq.Header.Set("X-Viewer-Kind", "user")
	unapprovedConvertReq.Header.Set("X-Display-Name", "Super Admin")
	unapprovedConvertRec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, unapprovedConvertRec, unapprovedConvertReq)
	if unapprovedConvertRec.Code != http.StatusBadRequest {
		t.Fatalf("unapproved conversion status = %d body = %s", unapprovedConvertRec.Code, unapprovedConvertRec.Body.String())
	}
	acceptReq := httptest.NewRequest(http.MethodPost, "/api/ideas/"+created.IdeaID+"/transition", strings.NewReader(`{"status":"accepted","decision_reason":"Approved for ontology execution"}`))
	acceptReq.Header.Set("X-User-ID", "superadmin")
	acceptReq.Header.Set("X-Viewer-Kind", "user")
	acceptReq.Header.Set("X-Display-Name", "Super Admin")
	acceptRec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, acceptRec, acceptReq)
	if acceptRec.Code != http.StatusOK {
		t.Fatalf("accept idea status = %d body = %s", acceptRec.Code, acceptRec.Body.String())
	}

	convertReq := httptest.NewRequest(http.MethodPost, "/api/ideas/"+created.IdeaID+"/convert-to-todo", nil)
	convertReq.Header.Set("X-User-ID", "superadmin")
	convertReq.Header.Set("X-Viewer-Kind", "user")
	convertReq.Header.Set("X-Display-Name", "Super Admin")
	convertRec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, convertRec, convertReq)
	if convertRec.Code != http.StatusOK {
		t.Fatalf("convert idea status = %d body = %s", convertRec.Code, convertRec.Body.String())
	}
	var converted todoapp.IdeaConversionResult
	if err := json.NewDecoder(convertRec.Body).Decode(&converted); err != nil {
		t.Fatalf("decode converted idea: %v", err)
	}
	if converted.Todo.TodoID == created.IdeaID || converted.Todo.SourceIdeaID != created.IdeaID || converted.Todo.IssueNumber == created.IssueNumber {
		t.Fatalf("converted identity = %+v, created = %+v", converted, created)
	}
	restoreConvertedReq := httptest.NewRequest(http.MethodPost, "/api/ideas/"+created.IdeaID+"/restore", nil)
	restoreConvertedReq.Header.Set("X-User-ID", "superadmin")
	restoreConvertedReq.Header.Set("X-Viewer-Kind", "user")
	restoreConvertedReq.Header.Set("X-Display-Name", "Super Admin")
	restoreConvertedRec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, restoreConvertedRec, restoreConvertedReq)
	if restoreConvertedRec.Code != http.StatusBadRequest {
		t.Fatalf("restore converted idea status = %d body = %s", restoreConvertedRec.Code, restoreConvertedRec.Body.String())
	}

	databases, err := db.OpenRegistry(context.Background(), db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverFile, URL: "file://" + filepath.Join(root, "core.db")},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	t.Cleanup(func() {
		if err := databases.Close(); err != nil {
			t.Fatalf("close database registry: %v", err)
		}
	})
	core, err := databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core database: %v", err)
	}
	objects, links, facts, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(context.Background(), createdTodo.TenantID)
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	createdTodoObjectID := "todo:" + createdTodo.TodoID
	convertedTodoObjectID := "todo:" + converted.Todo.TodoID
	assertContextGraphObject(t, objects, "todo", createdTodoObjectID)
	assertContextGraphObject(t, objects, "todo", convertedTodoObjectID)
	assertContextGraphLink(t, links, "idea_converted_to_todo", created.IdeaID, convertedTodoObjectID)
	assertContextGraphFact(t, facts, "todo", createdTodoObjectID, "title", "Track ontology todo")
	assertContextGraphFact(t, facts, "todo", createdTodoObjectID, "status", "inbox")
	assertContextGraphFact(t, facts, "todo", createdTodoObjectID, "source", "todo.create")
	assertContextGraphFact(t, facts, "todo", createdTodoObjectID, "created_at", createdTodo.CreatedAt.UTC().Format(time.RFC3339Nano))
	assertContextGraphFact(t, facts, "todo", createdTodoObjectID, "creation_actor_id", createdTodo.CreatedBy)
	assertContextGraphFact(t, facts, "todo", createdTodoObjectID, "creation_source", "todo.create")
	assertContextGraphFact(t, facts, "todo", convertedTodoObjectID, "created_at", converted.Todo.CreatedAt.UTC().Format(time.RFC3339Nano))
	assertContextGraphFact(t, facts, "todo", convertedTodoObjectID, "creation_source", "todo.idea_conversion")
	assertContextGraphFact(t, facts, "issue_comment", "issue_comment:"+todoComment.CommentID, "issue_kind", "todo")
	assertContextGraphFact(t, facts, "issue_comment", "issue_comment:"+todoComment.CommentID, "issue_object_id", createdTodoObjectID)
	assertContextGraphFact(t, facts, "issue_comment", "issue_comment:"+ideaComment.CommentID, "issue_kind", "idea")
	assertContextGraphFact(t, facts, "issue_comment", "issue_comment:"+ideaComment.CommentID, "issue_object_id", created.IdeaID)
	assertContextGraphFact(t, facts, "idea", deletedIdea.IdeaID, "title", "Delete ontology idea")
	assertContextGraphFact(t, facts, "idea", deletedIdea.IdeaID, "status", "deleted")
	assertContextGraphFact(t, facts, "idea", deletedIdea.IdeaID, "source", "idea.delete")
	assertContextGraphFact(t, facts, "idea", created.IdeaID, "title", "Capture ontology idea")
	assertContextGraphFact(t, facts, "idea", created.IdeaID, "summary", "Use planning ontology for ideas")
	assertContextGraphFact(t, facts, "idea", created.IdeaID, "status", "converted")
	assertContextGraphFact(t, facts, "idea", created.IdeaID, "source", "idea.convert_to_todo")
	assertContextGraphFact(t, facts, "idea", created.IdeaID, "captured_by", agentcore.SystemSuperadminPrincipalID)
	assertContextGraphFact(t, facts, "idea", created.IdeaID, "issue_number", created.IssueNumber)
	assertContextGraphFact(t, facts, "idea", created.IdeaID, "proposer_name", agentcore.SystemSuperadminPrincipalID)
	assertContextGraphFact(t, facts, "idea", created.IdeaID, "updated_by", agentcore.SystemSuperadminPrincipalID)
	assertContextGraphFact(t, facts, "idea", created.IdeaID, "downstream_todo_id", converted.Todo.TodoID)
	assertContextGraphFact(t, facts, "todo", convertedTodoObjectID, "upstream_idea_id", created.IdeaID)
	assertContextGraphEvidencePrefix(t, evidence, "planning-idea-captured:"+created.IdeaID+":idea.create:")
	assertContextGraphEvidencePrefix(t, evidence, "planning-idea-captured:"+created.IdeaID+":idea.convert_to_todo:")
	assertContextGraphEvidencePrefix(t, evidence, "planning-idea-captured:"+deletedIdea.IdeaID+":idea.delete:")
	assertContextGraphEvidence(t, evidence, "planning-idea-converted-to-todo:"+created.IdeaID+":"+converted.Todo.TodoID)

	events, err := newProductionOntologyEventLog(core).List(context.Background())
	if err != nil {
		t.Fatalf("list ontology events: %v", err)
	}
	assertOntologyEventTypePayload(t, events, "planning.todo.changed", "id", createdTodo.TodoID)
	assertOntologyEventTypePayload(t, events, "todo.created", "id", createdTodo.TodoID)
	assertOntologyEventTypePayload(t, events, "todo.created", "id", converted.Todo.TodoID)
	assertOntologyEventTypePayload(t, events, "planning.todo.comment.linked", "comment_id", todoComment.CommentID)
	assertOntologyEventTypePayload(t, events, "planning.idea.comment.linked", "comment_id", ideaComment.CommentID)
	assertOntologyEventTypePayload(t, events, "planning.idea.captured", "id", deletedIdea.IdeaID)
	assertOntologyEventTypePayload(t, events, "planning.idea.captured", "status", "deleted")
	assertOntologyEventTypePayload(t, events, "planning.idea.captured", "source", "idea.convert_to_todo")
	assertOntologyEvent(t, events, "planning-idea-converted-to-todo:"+created.IdeaID+":"+converted.Todo.TodoID, "planning.idea.converted_to_todo")
}

func TestProductionTodoOntologyServiceRejectsMutationWithoutOntologyPublisher(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := todoapp.NewRepository(env.DBRouter)
	inner := todoapp.NewService(todoapp.ServiceOptions{Repository: repo})
	service := newProductionTodoOntologyService(inner, &productionAPI{})

	_, err := service.CreateTodo(context.Background(), todoapp.ViewerRef{ViewerID: "user-1"}, todoapp.CreateTodoRequest{Title: "Must not bypass ontology"})
	if !errors.Is(err, todoapp.ErrServiceDependency) {
		t.Fatalf("CreateTodo error = %v, want ErrServiceDependency", err)
	}
	page, err := inner.ListTodos(context.Background(), todoapp.ViewerRef{ViewerID: "user-1"}, todoapp.ListTodosQuery{})
	if err != nil {
		t.Fatalf("ListTodos: %v", err)
	}
	if page.Total != 0 {
		t.Fatalf("todo mutation bypassed missing ontology publisher: %+v", page)
	}
}

func assertOntologyEventTypePayload(t *testing.T, events []graphprotocol.DomainEvent, eventType string, key string, want any) {
	t.Helper()
	for _, event := range events {
		if event.Type == eventType && event.Payload[key] == want {
			return
		}
	}
	t.Fatalf("ontology event type %s payload[%s]=%#v missing in %+v", eventType, key, want, events)
}

func assertContextGraphEvidencePrefix(t *testing.T, evidence []contextgraph.Evidence, prefix string) {
	t.Helper()
	for _, item := range evidence {
		if strings.HasPrefix(item.ID, prefix) {
			return
		}
	}
	t.Fatalf("evidence prefix %s missing in %+v", prefix, evidence)
}
