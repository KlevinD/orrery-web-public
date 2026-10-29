package discussionsession

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func seedHistoryFixture(t *testing.T, repo *Repository, id, tenant, owner, kind string, people ...string) {
	t.Helper()
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: id, TenantID: tenant, OwnerID: owner, CreatedBy: owner, ConversationKind: kind, Title: "History " + id, Status: "completed", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	for _, person := range people {
		memberKind := "user"
		if strings.HasPrefix(person, "agent-") || IsPersonalAssistantAgentID(person) {
			memberKind = "agent"
		}
		if err := repo.SaveMember(ctx, ConversationMemberRecord{ConversationID: id, MemberID: person, MemberKind: memberKind, DisplayName: person, CreatedAt: testTime}); err != nil {
			t.Fatal(err)
		}
	}
}

func seedHistoryMessage(t *testing.T, repo *Repository, conversation, id, role, content string, at time.Time) {
	t.Helper()
	if err := repo.SaveMessage(context.Background(), MessageRecord{ConversationID: conversation, MessageID: id, Role: role, Content: content, AuthorID: "author", CreatedAt: at, Metadata: map[string]string{"internal": "hidden-record-metadata"}}); err != nil {
		t.Fatal(err)
	}
}

func historyFixture(t *testing.T) (*Service, *Repository, HistoryToolRequest) {
	t.Helper()
	repo := newMemoryRepository(t)
	logical := PersonalAssistantAgentID("tenant", "owner")
	seedHistoryFixture(t, repo, "current", "tenant", "owner", ConversationKindPersonalAssistant, "agent-host", logical)
	seedHistoryFixture(t, repo, "old-private", "tenant", "owner", ConversationKindPersonalAssistant, "agent-host", logical)
	seedHistoryFixture(t, repo, "owned-direct", "tenant", "owner", ConversationKindDirect, "agent-other")
	seedHistoryFixture(t, repo, "other-owner", "tenant", "other", ConversationKindPersonalAssistant, "agent-host")
	seedHistoryFixture(t, repo, "other-tenant", "other", "owner", ConversationKindPersonalAssistant, "agent-host")
	seedHistoryFixture(t, repo, "legacy", "", "owner", ConversationKindDirect, "agent-host")
	if err := repo.SaveRun(context.Background(), RunRecord{RunID: "active-run", ConversationID: "current", AgentID: "agent-host", LogicalAgentID: logical, Status: "running"}); err != nil {
		t.Fatal(err)
	}
	return NewService(ServiceOptions{Repository: repo}), repo, HistoryToolRequest{TenantID: "tenant", AgentID: "agent-host", LogicalAgentID: logical, RunID: "active-run", ToolID: "session_search", Arguments: map[string]any{"query": "keyword"}}
}

func TestSessionHistorySearchAndReadPrivateOwnerScope(t *testing.T) {
	service, repo, req := historyFixture(t)
	for i, id := range []string{"old-private", "owned-direct", "other-owner", "other-tenant", "legacy"} {
		seedHistoryMessage(t, repo, id, fmt.Sprintf("msg-%d", i), "user", "keyword original", testTime.Add(time.Duration(i)*time.Minute))
	}
	seedHistoryMessage(t, repo, "old-private", "hidden", "system", "keyword hidden instructions", testTime.Add(time.Hour))
	result, err := service.ExecuteHistoryTool(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	items := result["items"].([]map[string]any)
	if len(items) != 2 || items[0]["session_id"] != "owned-direct" || items[1]["session_id"] != "old-private" {
		t.Fatalf("scope/order incorrect: %+v", items)
	}
	req.ToolID, req.Arguments = "session_read", map[string]any{"session_id": "old-private", "message_id": "msg-0"}
	result, err = service.ExecuteHistoryTool(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	if !strings.Contains(string(raw), "keyword original") || strings.Contains(string(raw), "hidden") || !strings.Contains(string(raw), "source_ref") || !strings.Contains(string(raw), "/discussions?discussion=") {
		t.Fatalf("unsafe/incomplete projection: %s", raw)
	}
}

func TestSessionHistoryFailsClosedForForgedIdentityAndInaccessibleTargets(t *testing.T) {
	service, _, base := historyFixture(t)
	for _, mutation := range []struct {
		name string
		edit func(*HistoryToolRequest)
	}{
		{"tenant", func(r *HistoryToolRequest) { r.TenantID = "other" }},
		{"agent", func(r *HistoryToolRequest) { r.AgentID = "agent-other" }},
		{"logical", func(r *HistoryToolRequest) { r.LogicalAgentID = "" }},
		{"run", func(r *HistoryToolRequest) { r.RunID = "missing" }},
		{"missing_run", func(r *HistoryToolRequest) { r.RunID = "" }},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			req := base
			mutation.edit(&req)
			if _, err := service.ExecuteHistoryTool(context.Background(), req); !errors.Is(err, ErrForbidden) {
				t.Fatalf("expected forbidden, got %v", err)
			}
		})
	}
	for _, target := range []string{"other-owner", "other-tenant", "legacy", "nonexistent"} {
		base.ToolID, base.Arguments = "session_read", map[string]any{"session_id": target}
		if _, err := service.ExecuteHistoryTool(context.Background(), base); !errors.Is(err, ErrNotFound) {
			t.Fatalf("target %s must be indistinguishable from absent: %v", target, err)
		}
	}
}

func TestSessionHistoryGroupDoesNotDisclosePrivateOrDifferentAudience(t *testing.T) {
	service, repo, req := historyFixture(t)
	seedHistoryFixture(t, repo, "group", "tenant", "owner", ConversationKindGroup, "agent-host", "guest")
	seedHistoryFixture(t, repo, "shared-group", "tenant", "owner", ConversationKindGroup, "agent-host", "guest")
	seedHistoryFixture(t, repo, "private-direct", "tenant", "owner", ConversationKindDirect, "agent-host")
	seedHistoryFixture(t, repo, "missing-agent", "tenant", "owner", ConversationKindGroup, "agent-other", "guest")
	for _, id := range []string{"group", "shared-group", "private-direct", "old-private", "missing-agent"} {
		seedHistoryMessage(t, repo, id, "message-"+id, "assistant", "keyword", testTime)
	}
	if err := repo.SaveRun(context.Background(), RunRecord{RunID: "active-run", ConversationID: "group", AgentID: req.AgentID, Status: "running"}); err != nil {
		t.Fatal(err)
	}
	req.LogicalAgentID = ""
	result, err := service.ExecuteHistoryTool(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	items := result["items"].([]map[string]any)
	if len(items) != 2 {
		t.Fatalf("unexpected group scope: %+v", items)
	}
	for _, item := range items {
		if item["session_id"] != "group" && item["session_id"] != "shared-group" {
			t.Fatalf("private data leaked: %+v", item)
		}
	}
	if err := repo.DeleteMember(context.Background(), "shared-group", "user", "guest"); err != nil {
		t.Fatal(err)
	}
	req.ToolID, req.Arguments = "session_read", map[string]any{"session_id": "shared-group"}
	if _, err := service.ExecuteHistoryTool(context.Background(), req); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked audience access must be checked again: %v", err)
	}
}

func TestSessionHistoryLiteralUnicodeDatesPaginationAndOriginalContext(t *testing.T) {
	service, repo, req := historyFixture(t)
	for i := 0; i < 8; i++ {
		content := fmt.Sprintf("message %d account-example 100%%_!", i)
		if i == 3 {
			content = "\u94f6\u884c\u8d26\u53f7 test-only 100%_!"
		}
		seedHistoryMessage(t, repo, "old-private", fmt.Sprintf("m-%02d", i), "user", content, testTime.Add(time.Duration(i)*time.Minute))
	}
	req.Arguments = map[string]any{"query": "100%_!", "limit": 2, "after": testTime.Add(2 * time.Minute).Format(time.RFC3339), "before": testTime.Add(6 * time.Minute).Format(time.RFC3339)}
	result, err := service.ExecuteHistoryTool(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	items := result["items"].([]map[string]any)
	if len(items) != 2 || items[0]["message_id"] != "m-05" || result["has_more"] != true {
		t.Fatalf("search page: %+v", result)
	}
	req.Arguments["offset"] = result["next_offset"]
	result, err = service.ExecuteHistoryTool(context.Background(), req)
	if err != nil || result["has_more"] != false || result["items"].([]map[string]any)[1]["message_id"] != "m-02" {
		t.Fatalf("second page: %+v %v", result, err)
	}
	req.Arguments = map[string]any{"query": "\u94f6\u884c\u8d26\u53f7"}
	result, err = service.ExecuteHistoryTool(context.Background(), req)
	if err != nil || len(result["items"].([]map[string]any)) != 1 {
		t.Fatalf("unicode search: %+v %v", result, err)
	}
	req.ToolID, req.Arguments = "session_read", map[string]any{"session_id": "old-private", "message_id": "m-03", "context_before": 1, "limit": 3}
	result, err = service.ExecuteHistoryTool(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	items = result["items"].([]map[string]any)
	if len(items) != 3 || items[0]["message_id"] != "m-02" || items[2]["message_id"] != "m-04" || result["has_more"] != true {
		t.Fatalf("context window: %+v", result)
	}
	req.Arguments["offset"] = result["next_offset"]
	result, err = service.ExecuteHistoryTool(context.Background(), req)
	if err != nil || result["items"].([]map[string]any)[0]["message_id"] != "m-05" {
		t.Fatalf("context pagination: %+v %v", result, err)
	}
	req.Arguments = map[string]any{"session_id": "old-private", "message_id": "m-03", "limit": 1}
	result, err = service.ExecuteHistoryTool(context.Background(), req)
	if err != nil || result["items"].([]map[string]any)[0]["message_id"] != "m-03" {
		t.Fatalf("small context window omitted the requested anchor: %+v %v", result, err)
	}
	req.ToolID, req.Arguments = "session_search", map[string]any{"query": "' OR 1=1 --"}
	result, err = service.ExecuteHistoryTool(context.Background(), req)
	if err != nil || len(result["items"].([]map[string]any)) != 0 {
		t.Fatalf("query must remain literal: %+v %v", result, err)
	}
}

func TestSessionHistoryLongMessageCanBeReadWithoutLosingCharacters(t *testing.T) {
	service, repo, req := historyFixture(t)
	content := strings.Repeat("\u4e2d", 4501)
	seedHistoryMessage(t, repo, "old-private", "long", "assistant", content, testTime)
	req.ToolID = "session_read"
	var full strings.Builder
	for offset := 0; ; {
		req.Arguments = map[string]any{"session_id": "old-private", "message_id": "long", "context_before": 0, "content_offset": offset}
		result, err := service.ExecuteHistoryTool(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		item := result["items"].([]map[string]any)[0]
		full.WriteString(item["content"].(string))
		if item["content_truncated"] == false {
			break
		}
		offset = item["next_content_offset"].(int)
	}
	if full.String() != content {
		t.Fatal("long original text was lost or duplicated")
	}
}

func TestSessionHistoryRejectsMalformedOrAuthorityArguments(t *testing.T) {
	for _, input := range []map[string]any{
		{"query": "keyword", "tenant_id": "other"}, {"query": "keyword", "owner_id": "other"},
		{"query": "keyword", "limit": 21}, {"query": "keyword", "limit": 1.2},
		{"query": "keyword", "offset": -1}, {"query": ""}, {"query": "keyword", "after": "2026-09-07"},
		{"query": "keyword", "after": "2026-09-08T00:00:00Z", "before": "2026-09-07T00:00:00Z"},
	} {
		if _, err := parseHistoryArguments("session_search", input); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("invalid search accepted: %+v %v", input, err)
		}
	}
	for _, input := range []map[string]any{{}, {"session_id": "old", "limit": 11}, {"session_id": "old", "content_offset": 1}, {"session_id": "old", "message_id": "m", "context_before": 6}} {
		if _, err := parseHistoryArguments("session_read", input); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("invalid read accepted: %+v %v", input, err)
		}
	}
}

func TestSessionHistoryRejectsTerminalRunsAndDeletedSources(t *testing.T) {
	service, repo, req := historyFixture(t)
	run, err := repo.GetRun(context.Background(), req.RunID)
	if err != nil {
		t.Fatal(err)
	}
	run.Status = "completed"
	if err := repo.SaveRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ExecuteHistoryTool(context.Background(), req); !errors.Is(err, ErrForbidden) {
		t.Fatalf("completed run can no longer fetch private history: %v", err)
	}
	run.Status = "running"
	if err := repo.SaveRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteConversation(context.Background(), "current"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ExecuteHistoryTool(context.Background(), req); !errors.Is(err, ErrForbidden) {
		t.Fatalf("deleted source still authorized a lookup: %v", err)
	}
}

func TestSessionHistoryPersonalOwnerSurvivesHistoricalCreatorAlias(t *testing.T) {
	service, repo, req := historyFixture(t)
	view, err := repo.GetConversationRecord(context.Background(), "old-private")
	if err != nil {
		t.Fatal(err)
	}
	view.CreatedBy = "historical-human-profile"
	if err := repo.SaveConversation(context.Background(), view); err != nil {
		t.Fatal(err)
	}
	seedHistoryMessage(t, repo, "old-private", "alias-message", "user", "keyword", testTime)
	result, err := service.ExecuteHistoryTool(context.Background(), req)
	if err != nil || len(result["items"].([]map[string]any)) != 1 {
		t.Fatalf("same owner's old private history lost after identity migration: %+v %v", result, err)
	}
}
