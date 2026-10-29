package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

func TestProductionMissionStreamSSESendsDiscussionAndInboxPatchAfterSnapshot(t *testing.T) {
	root := t.TempDir()
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", productionTestDatabaseURLs(root))
	selectTestServerConfig(t)
	handler, cleanup, err := newServerHandler(context.Background(), false)
	if err != nil {
		t.Fatalf("newServerHandler production: %v", err)
	}
	defer cleanup()
	server := httptest.NewServer(handler)
	defer server.Close()

	create := httptest.NewRequest(http.MethodPost, "/api/missions", jsonBody(`{"title":"Stream discussion","final_goal":"Push discussion patches through production SSE"}`))
	create.Header.Set("X-User-ID", "system-superadmin")
	create.Header.Set("X-Viewer-Kind", "user")
	create.Header.Set("X-Permission-Scope", "mission.read,mission.write,discussion.read,discussion.write")
	create.Header.Set("Idempotency-Key", "mission-stream-discussion")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, create)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create mission status = %d body = %s", rec.Code, rec.Body.String())
	}
	var mission missioncontrol.MissionDTO
	if err := json.NewDecoder(rec.Body).Decode(&mission); err != nil {
		t.Fatalf("decode mission: %v", err)
	}

	createTask := httptest.NewRequest(http.MethodPost, "/api/missions/"+mission.MissionID+"/tasks", jsonBody(`{"assignee_id":"`+defaultCoordinatorAgentID+`","goal":"Respond to stream mention","worker_personal_goal":"Watch the stream"}`))
	createTask.Header.Set("X-User-ID", "system-superadmin")
	createTask.Header.Set("X-Viewer-Kind", "user")
	createTask.Header.Set("X-Permission-Scope", "mission.read,mission.write,discussion.read,discussion.write")
	createTask.Header.Set("Idempotency-Key", "task-stream-discussion")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, createTask)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create task status = %d body = %s", rec.Code, rec.Body.String())
	}

	discussionReq := httptest.NewRequest(http.MethodGet, "/api/missions/"+mission.MissionID+"/discussion", nil)
	discussionReq.Header.Set("X-User-ID", "system-superadmin")
	discussionReq.Header.Set("X-Viewer-Kind", "user")
	discussionReq.Header.Set("X-Permission-Scope", "mission.read,mission.write,discussion.read,discussion.write")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, discussionReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("discussion status = %d body = %s", rec.Code, rec.Body.String())
	}
	var discussion missioncontrol.DiscussionDTO
	if err := json.NewDecoder(rec.Body).Decode(&discussion); err != nil {
		t.Fatalf("decode discussion: %v", err)
	}

	query := url.Values{}
	query.Set("viewer_id", defaultCoordinatorAgentID)
	query.Set("viewer_kind", "agent")
	query.Set("permission_scope", "mission.read,mission.write,discussion.read,discussion.write")
	streamReq, err := http.NewRequest(http.MethodGet, server.URL+"/api/missions/"+mission.MissionID+"/stream?"+query.Encode(), nil)
	if err != nil {
		t.Fatalf("new stream request: %v", err)
	}
	streamReq.Header.Set("Accept", "text/event-stream")
	authorizeServerTestRequest(streamReq)
	resp, err := http.DefaultClient.Do(streamReq)
	if err != nil {
		t.Fatalf("open mission stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("stream status = %d body = %s", resp.StatusCode, body)
	}
	reader := bufio.NewReader(resp.Body)
	first := readProductionSSEEvent(t, reader)
	if !strings.Contains(first, "event: snapshot") || !strings.Contains(first, `"mission_id":"`+mission.MissionID+`"`) {
		t.Fatalf("first SSE event = %s", first)
	}

	content := "Patch this discussion [@Coordinator](mention://agent/" + defaultCoordinatorAgentID + ")"
	post := httptest.NewRequest(http.MethodPost, "/api/missions/"+mission.MissionID+"/discussion/messages", jsonBody(`{"discussion_group_id":"`+discussion.DiscussionGroupID+`","content":"`+content+`","message_kind":"request","visibility":"mission","required_response":true}`))
	post.Header.Set("X-User-ID", "system-superadmin")
	post.Header.Set("X-Viewer-Kind", "user")
	post.Header.Set("X-Permission-Scope", "mission.read,mission.write,discussion.read,discussion.write")
	post.Header.Set("Idempotency-Key", "stream-discussion-mention")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, post)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("post mention status = %d body = %s", rec.Code, rec.Body.String())
	}

	controlReq := httptest.NewRequest(http.MethodGet, "/api/missions/"+mission.MissionID+"/control", nil)
	controlReq.Header.Set("X-User-ID", "system-superadmin")
	controlReq.Header.Set("X-Viewer-Kind", "system")
	controlReq.Header.Set("X-Permission-Scope", "mission.read")
	authorizeServerTestRequest(controlReq)
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, controlReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("control status = %d body = %s", rec.Code, rec.Body.String())
	}
	var control missioncontrol.MissionControlViewDTO
	if err := json.NewDecoder(rec.Body).Decode(&control); err != nil {
		t.Fatalf("decode control view: %v", err)
	}
	if len(control.InboxSummary.Items) != 1 || control.InboxSummary.Items[0].OwnerID != defaultCoordinatorAgentID {
		t.Fatalf("canonical inbox owner mismatch: %+v", control.InboxSummary)
	}

	second := readProductionSSEEvent(t, reader)
	for _, want := range []string{"event: patch", `"type":"replace_discussion"`, `"type":"replace_inbox_summary"`, content} {
		if !strings.Contains(second, want) {
			t.Fatalf("discussion stream patch missing %q: %s", want, second)
		}
	}
}

func TestProductionMissionStreamSSESendsInboxClosurePatchAfterReply(t *testing.T) {
	root := t.TempDir()
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", productionTestDatabaseURLs(root))
	selectTestServerConfig(t)
	handler, cleanup, err := newServerHandler(context.Background(), false)
	if err != nil {
		t.Fatalf("newServerHandler production: %v", err)
	}
	defer cleanup()
	server := httptest.NewServer(handler)
	defer server.Close()

	create := httptest.NewRequest(http.MethodPost, "/api/missions", jsonBody(`{"title":"Stream reply close","final_goal":"Close inbox through streamed discussion reply"}`))
	create.Header.Set("X-User-ID", "system-superadmin")
	create.Header.Set("X-Viewer-Kind", "user")
	create.Header.Set("X-Permission-Scope", "mission.read,mission.write,discussion.read,discussion.write")
	create.Header.Set("Idempotency-Key", "mission-stream-reply-close")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, create)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create mission status = %d body = %s", rec.Code, rec.Body.String())
	}
	var mission missioncontrol.MissionDTO
	if err := json.NewDecoder(rec.Body).Decode(&mission); err != nil {
		t.Fatalf("decode mission: %v", err)
	}

	createTask := httptest.NewRequest(http.MethodPost, "/api/missions/"+mission.MissionID+"/tasks", jsonBody(`{"assignee_id":"`+defaultCoordinatorAgentID+`","goal":"Reply to streamed mention","worker_personal_goal":"Close mention through reply"}`))
	createTask.Header.Set("X-User-ID", "system-superadmin")
	createTask.Header.Set("X-Viewer-Kind", "user")
	createTask.Header.Set("X-Permission-Scope", "mission.read,mission.write,discussion.read,discussion.write")
	createTask.Header.Set("Idempotency-Key", "task-stream-reply-close")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, createTask)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create task status = %d body = %s", rec.Code, rec.Body.String())
	}

	discussionReq := httptest.NewRequest(http.MethodGet, "/api/missions/"+mission.MissionID+"/discussion", nil)
	discussionReq.Header.Set("X-User-ID", "system-superadmin")
	discussionReq.Header.Set("X-Viewer-Kind", "user")
	discussionReq.Header.Set("X-Permission-Scope", "mission.read,mission.write,discussion.read,discussion.write")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, discussionReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("discussion status = %d body = %s", rec.Code, rec.Body.String())
	}
	var discussion missioncontrol.DiscussionDTO
	if err := json.NewDecoder(rec.Body).Decode(&discussion); err != nil {
		t.Fatalf("decode discussion: %v", err)
	}

	query := url.Values{}
	query.Set("viewer_id", defaultCoordinatorAgentID)
	query.Set("viewer_kind", "agent")
	query.Set("permission_scope", "mission.read,mission.write,discussion.read,discussion.write")
	streamReq, err := http.NewRequest(http.MethodGet, server.URL+"/api/missions/"+mission.MissionID+"/stream?"+query.Encode(), nil)
	if err != nil {
		t.Fatalf("new stream request: %v", err)
	}
	streamReq.Header.Set("Accept", "text/event-stream")
	authorizeServerTestRequest(streamReq)
	resp, err := http.DefaultClient.Do(streamReq)
	if err != nil {
		t.Fatalf("open mission stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("stream status = %d body = %s", resp.StatusCode, body)
	}
	reader := bufio.NewReader(resp.Body)
	if first := readProductionSSEEvent(t, reader); !strings.Contains(first, "event: snapshot") {
		t.Fatalf("first SSE event = %s", first)
	}

	content := "Close this on stream [@Coordinator](mention://agent/" + defaultCoordinatorAgentID + ")"
	post := httptest.NewRequest(http.MethodPost, "/api/missions/"+mission.MissionID+"/discussion/messages", jsonBody(`{"discussion_group_id":"`+discussion.DiscussionGroupID+`","content":"`+content+`","message_kind":"request","visibility":"mission","required_response":true}`))
	post.Header.Set("X-User-ID", "system-superadmin")
	post.Header.Set("X-Viewer-Kind", "user")
	post.Header.Set("X-Permission-Scope", "mission.read,mission.write,discussion.read,discussion.write")
	post.Header.Set("Idempotency-Key", "stream-reply-close-mention")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, post)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("post mention status = %d body = %s", rec.Code, rec.Body.String())
	}
	mentionPatch := readProductionSSEEvent(t, reader)
	if !strings.Contains(mentionPatch, `"type":"replace_inbox_summary"`) || !strings.Contains(mentionPatch, `"status":"open"`) {
		t.Fatalf("mention stream patch did not expose open inbox item: %s", mentionPatch)
	}

	inboxReq := httptest.NewRequest(http.MethodGet, "/api/inbox?mission_id="+mission.MissionID, nil)
	inboxReq.Header.Set("X-Agent-ID", defaultCoordinatorAgentID)
	inboxReq.Header.Set("X-Viewer-Kind", "agent")
	inboxReq.Header.Set("X-Permission-Scope", "mission.read,mission.write,discussion.read,discussion.write")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, inboxReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("inbox status = %d body = %s", rec.Code, rec.Body.String())
	}
	var inbox missioncontrol.InboxPageDTO
	if err := json.NewDecoder(rec.Body).Decode(&inbox); err != nil {
		t.Fatalf("decode inbox: %v", err)
	}
	if len(inbox.Items) != 1 {
		t.Fatalf("mention inbox = %+v", inbox)
	}

	replyContent := "Acknowledged through the live stream"
	reply := httptest.NewRequest(http.MethodPost, "/api/missions/"+mission.MissionID+"/discussion/messages/"+inbox.Items[0].MessageID+"/reply", jsonBody(`{"discussion_group_id":"`+discussion.DiscussionGroupID+`","content":"`+replyContent+`","inbox_item_id":"`+inbox.Items[0].InboxItemID+`"}`))
	reply.Header.Set("X-Agent-ID", defaultCoordinatorAgentID)
	reply.Header.Set("X-Viewer-Kind", "agent")
	reply.Header.Set("X-Permission-Scope", "mission.read,mission.write,discussion.read,discussion.write")
	reply.Header.Set("Idempotency-Key", "stream-reply-close")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, reply)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("reply status = %d body = %s", rec.Code, rec.Body.String())
	}

	closePatch := readProductionSSEEventContaining(t, reader, `"status":"closed"`)
	for _, want := range []string{"event: patch", `"type":"replace_discussion"`, `"type":"replace_inbox_summary"`, `"status":"closed"`} {
		if !strings.Contains(closePatch, want) {
			t.Fatalf("reply close stream patch missing %q: %s", want, closePatch)
		}
	}
	reloadedDiscussion := waitProductionDiscussionMessage(t, handler, mission.MissionID, replyContent)
	if len(reloadedDiscussion.PendingMentions) != 0 {
		t.Fatalf("reply close should clear pending mention in projected discussion: %+v", reloadedDiscussion.PendingMentions)
	}
}

func TestProductionHandlerRefreshesProjectionAfterStandaloneInboxClose(t *testing.T) {
	root := t.TempDir()
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", productionTestDatabaseURLs(root))
	selectTestServerConfig(t)
	handler, cleanup, err := newServerHandler(context.Background(), false)
	if err != nil {
		t.Fatalf("newServerHandler production: %v", err)
	}
	defer cleanup()

	create := httptest.NewRequest(http.MethodPost, "/api/missions", jsonBody(`{"title":"Standalone close","final_goal":"Close mention inbox outside reply endpoint"}`))
	create.Header.Set("X-User-ID", "system-superadmin")
	create.Header.Set("X-Viewer-Kind", "user")
	create.Header.Set("X-Permission-Scope", "mission.read,mission.write,discussion.read,discussion.write")
	create.Header.Set("Idempotency-Key", "mission-standalone-close")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, create)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create mission status = %d body = %s", rec.Code, rec.Body.String())
	}
	var mission missioncontrol.MissionDTO
	if err := json.NewDecoder(rec.Body).Decode(&mission); err != nil {
		t.Fatalf("decode mission: %v", err)
	}

	createTask := httptest.NewRequest(http.MethodPost, "/api/missions/"+mission.MissionID+"/tasks", jsonBody(`{"assignee_id":"`+defaultCoordinatorAgentID+`","goal":"Respond to standalone close","worker_personal_goal":"Own standalone close"}`))
	createTask.Header.Set("X-User-ID", "system-superadmin")
	createTask.Header.Set("X-Viewer-Kind", "user")
	createTask.Header.Set("X-Permission-Scope", "mission.read,mission.write,discussion.read,discussion.write")
	createTask.Header.Set("Idempotency-Key", "task-standalone-close")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, createTask)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create task status = %d body = %s", rec.Code, rec.Body.String())
	}

	discussionReq := httptest.NewRequest(http.MethodGet, "/api/missions/"+mission.MissionID+"/discussion", nil)
	discussionReq.Header.Set("X-User-ID", "system-superadmin")
	discussionReq.Header.Set("X-Viewer-Kind", "user")
	discussionReq.Header.Set("X-Permission-Scope", "mission.read,mission.write,discussion.read,discussion.write")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, discussionReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("discussion status = %d body = %s", rec.Code, rec.Body.String())
	}
	var discussion missioncontrol.DiscussionDTO
	if err := json.NewDecoder(rec.Body).Decode(&discussion); err != nil {
		t.Fatalf("decode discussion: %v", err)
	}

	content := "Please close directly [@Coordinator](mention://agent/" + defaultCoordinatorAgentID + ")"
	post := httptest.NewRequest(http.MethodPost, "/api/missions/"+mission.MissionID+"/discussion/messages", jsonBody(`{"discussion_group_id":"`+discussion.DiscussionGroupID+`","content":"`+content+`","message_kind":"request","visibility":"mission","required_response":true}`))
	post.Header.Set("X-User-ID", "system-superadmin")
	post.Header.Set("X-Viewer-Kind", "user")
	post.Header.Set("X-Permission-Scope", "mission.read,mission.write,discussion.read,discussion.write")
	post.Header.Set("Idempotency-Key", "mention-standalone-close")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, post)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("post mention status = %d body = %s", rec.Code, rec.Body.String())
	}

	inboxReq := httptest.NewRequest(http.MethodGet, "/api/inbox?mission_id="+mission.MissionID, nil)
	inboxReq.Header.Set("X-Agent-ID", defaultCoordinatorAgentID)
	inboxReq.Header.Set("X-Viewer-Kind", "agent")
	inboxReq.Header.Set("X-Permission-Scope", "mission.read,mission.write,discussion.read,discussion.write")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, inboxReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("inbox status = %d body = %s", rec.Code, rec.Body.String())
	}
	var inbox missioncontrol.InboxPageDTO
	if err := json.NewDecoder(rec.Body).Decode(&inbox); err != nil {
		t.Fatalf("decode inbox: %v", err)
	}
	if len(inbox.Items) != 1 || inbox.Items[0].Status != "pending" {
		t.Fatalf("mention inbox = %+v", inbox)
	}

	closeReq := httptest.NewRequest(http.MethodPost, "/api/inbox/"+inbox.Items[0].InboxItemID+"/close", jsonBody(`{"reply_message_id":"manual-close-reply"}`))
	closeReq.Header.Set("X-Agent-ID", defaultCoordinatorAgentID)
	closeReq.Header.Set("X-Viewer-Kind", "agent")
	closeReq.Header.Set("X-Permission-Scope", "mission.read,mission.write,discussion.read,discussion.write")
	closeReq.Header.Set("Idempotency-Key", "standalone-inbox-close")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, closeReq)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("standalone close status = %d body = %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, discussionReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("discussion reload status = %d body = %s", rec.Code, rec.Body.String())
	}
	if err := json.NewDecoder(rec.Body).Decode(&discussion); err != nil {
		t.Fatalf("decode reloaded discussion: %v", err)
	}
	if len(discussion.PendingMentions) != 0 {
		t.Fatalf("standalone inbox close should clear pending mention in projected discussion: %+v", discussion.PendingMentions)
	}

	controlReq := httptest.NewRequest(http.MethodGet, "/api/missions/"+mission.MissionID+"/control", nil)
	controlReq.Header.Set("X-Agent-ID", defaultCoordinatorAgentID)
	controlReq.Header.Set("X-Viewer-Kind", "agent")
	controlReq.Header.Set("X-Permission-Scope", "mission.read,mission.write,discussion.read,discussion.write")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, controlReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("control status = %d body = %s", rec.Code, rec.Body.String())
	}
	var control missioncontrol.MissionControlViewDTO
	if err := json.NewDecoder(rec.Body).Decode(&control); err != nil {
		t.Fatalf("decode control: %v", err)
	}
	if control.InboxSummary.OpenCount != 0 || len(control.InboxSummary.Items) != 1 || control.InboxSummary.Items[0].Status != "closed" {
		t.Fatalf("standalone close should refresh projected inbox summary: %+v", control.InboxSummary)
	}
}

func readProductionSSEEvent(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	lines := make(chan struct {
		event string
		err   error
	}, 1)
	go func() {
		event, err := readProductionSSEEventNoTimeout(reader)
		lines <- struct {
			event string
			err   error
		}{event: event, err: err}
	}()
	select {
	case result := <-lines:
		if result.err != nil {
			t.Fatalf("read production SSE event: %v", result.err)
		}
		return result.event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for production SSE event")
		return ""
	}
}

func readProductionSSEEventContaining(t *testing.T, reader *bufio.Reader, values ...string) string {
	t.Helper()
	events := make(chan struct {
		event string
		err   error
	}, 1)
	go func() {
		for {
			event, err := readProductionSSEEventNoTimeout(reader)
			events <- struct {
				event string
				err   error
			}{event: event, err: err}
			if err != nil {
				return
			}
		}
	}()
	var seen []string
	deadline := time.After(10 * time.Second)
	for {
		select {
		case result := <-events:
			if result.err != nil {
				t.Fatalf("read production SSE event while waiting for %v: %v; seen: %s", values, result.err, strings.Join(seen, "\n---\n"))
			}
			seen = append(seen, result.event)
			matched := true
			for _, value := range values {
				if !strings.Contains(result.event, value) {
					matched = false
					break
				}
			}
			if matched {
				return result.event
			}
		case <-deadline:
			t.Fatalf("timed out waiting for production SSE event containing %v; seen: %s", values, strings.Join(seen, "\n---\n"))
			return ""
		}
	}
}

func readProductionSSEEventNoTimeout(reader *bufio.Reader) (string, error) {
	var eventLines []string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		eventLines = append(eventLines, strings.TrimRight(line, "\r\n"))
		if strings.TrimSpace(line) == "" {
			return strings.Join(eventLines, "\n"), nil
		}
	}
}

func waitProductionDiscussionMessage(t *testing.T, handler http.Handler, missionID string, body string) missioncontrol.DiscussionDTO {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last string
	for {
		req := httptest.NewRequest(http.MethodGet, "/api/missions/"+missionID+"/discussion", nil)
		req.Header.Set("X-Agent-ID", defaultCoordinatorAgentID)
		req.Header.Set("X-Viewer-Kind", "agent")
		req.Header.Set("X-Permission-Scope", "mission.read,mission.write,discussion.read,discussion.write")
		rec := httptest.NewRecorder()
		serveServerTestHTTP(t, handler, rec, req)
		last = rec.Body.String()
		if rec.Code == http.StatusOK {
			var discussion missioncontrol.DiscussionDTO
			if err := json.NewDecoder(strings.NewReader(last)).Decode(&discussion); err != nil {
				t.Fatalf("decode discussion while waiting for reply: %v body=%s", err, last)
			}
			for _, message := range discussion.Messages {
				if strings.Contains(message.Body, body) {
					return discussion
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for projected discussion message %q; last response: %s", body, last)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
