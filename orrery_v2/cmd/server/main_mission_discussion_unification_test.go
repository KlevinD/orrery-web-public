package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	discussionsession "orrery_v2/internal/app/discussionsession"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

func TestProductionCreateMissionLinksUnifiedDiscussionSession(t *testing.T) {
	root := t.TempDir()
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", productionTestDatabaseURLs(root))
	selectTestServerConfig(t)
	handler, cleanup, err := newServerHandler(context.Background(), false)
	if err != nil {
		t.Fatalf("newServerHandler production: %v", err)
	}
	defer cleanup()

	create := httptest.NewRequest(http.MethodPost, "/api/missions", jsonBody(`{"title":"Unified mission discussion","final_goal":"Use the shared discussion workspace"}`))
	create.Header.Set("X-User-ID", "superadmin")
	create.Header.Set("X-Viewer-Kind", "user")
	create.Header.Set("X-Permission-Scope", "mission.read,mission.write,discussion.read")
	create.Header.Set("Idempotency-Key", "mission-unified-discussion-session")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, create)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create mission status = %d body = %s", rec.Code, rec.Body.String())
	}
	var mission missioncontrol.MissionDTO
	if err := json.NewDecoder(rec.Body).Decode(&mission); err != nil {
		t.Fatalf("decode mission: %v", err)
	}

	discussionReq := httptest.NewRequest(http.MethodGet, "/api/missions/"+mission.MissionID+"/discussion", nil)
	discussionReq.Header.Set("X-User-ID", "superadmin")
	discussionReq.Header.Set("X-Viewer-Kind", "user")
	discussionReq.Header.Set("X-Permission-Scope", "mission.read,mission.write,discussion.read")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, discussionReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("mission discussion status = %d body = %s", rec.Code, rec.Body.String())
	}
	var discussion struct {
		DiscussionGroupID string `json:"discussion_group_id"`
		ConversationID    string `json:"conversation_id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&discussion); err != nil {
		t.Fatalf("decode mission discussion: %v", err)
	}
	if discussion.DiscussionGroupID == "" {
		t.Fatalf("mission discussion did not keep legacy discussion group: %+v", discussion)
	}
	if discussion.ConversationID == "" {
		t.Fatalf("mission discussion did not expose unified conversation_id: %+v", discussion)
	}

	unifiedReq := httptest.NewRequest(http.MethodGet, "/api/discussions/"+discussion.ConversationID, nil)
	unifiedReq.Header.Set("X-User-ID", "superadmin")
	unifiedReq.Header.Set("X-Viewer-Kind", "user")
	unifiedReq.Header.Set("X-Permission-Scope", "discussion.read,mission.read")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, unifiedReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("unified discussion status = %d body = %s", rec.Code, rec.Body.String())
	}
	var unified struct {
		DiscussionID   string                                    `json:"discussion_id"`
		Title          string                                    `json:"title"`
		LinkedMissions []discussionsession.LinkedMissionDTO      `json:"linked_missions"`
		Members        []discussionsession.ConversationMemberDTO `json:"members"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&unified); err != nil {
		t.Fatalf("decode unified discussion: %v", err)
	}
	if unified.DiscussionID != discussion.ConversationID {
		t.Fatalf("unified discussion id = %q, want %q", unified.DiscussionID, discussion.ConversationID)
	}
	if unified.Title != "Unified mission discussion" {
		t.Fatalf("unified discussion title = %q", unified.Title)
	}
	if len(unified.LinkedMissions) != 1 || unified.LinkedMissions[0].MissionID != mission.MissionID || unified.LinkedMissions[0].Title != mission.Title {
		t.Fatalf("unified discussion mission links = %+v, want mission %s", unified.LinkedMissions, mission.MissionID)
	}
	foundSystemPrincipal := false
	for _, member := range unified.Members {
		if member.ID == agentcore.SystemSuperadminPrincipalID {
			foundSystemPrincipal = true
			break
		}
	}
	if foundSystemPrincipal {
		t.Fatalf("system principal must not be persisted as a discussion member: %+v", unified.Members)
	}
}
