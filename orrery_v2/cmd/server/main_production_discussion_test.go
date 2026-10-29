package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	discussionsession "orrery_v2/internal/app/discussionsession"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

func TestProductionDiscussionAgentCanAddDiscussionMembersDirectly(t *testing.T) {
	root := t.TempDir()
	memberAgentIDs := defaultCoordinatorAgentID + ",agent-worker"
	executable := writePiDiscussionMemberRuntimeFixture(t, memberAgentIDs)
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", productionTestDatabaseURLs(root))
	setTestConfigOverride(t, "discussion.agent.timeout", "2s")
	setTestConfigOverride(t, "discussion.agent.runtime_kind", "pi")
	setTestConfigOverride(t, "discussion.agent.runtime_config", `{"executable":"`+jsonPath(executable)+`"}`)
	setTestConfigOverride(t, "auth.local.enabled", "true")
	selectTestServerConfig(t)
	handler, cleanup, err := newServerHandler(context.Background(), false)
	if err != nil {
		t.Fatalf("newServerHandler production: %v", err)
	}
	defer cleanup()
	configureRuntimeFixtureLLMProfile(t, handler, "pi-test")

	org := createProductionWorkerOrgTree(t, handler)
	loginCookie := loginProductionSuperadmin(t, handler)
	worker := httptest.NewRequest(http.MethodPut, "/api/workforce/members/agent-worker", jsonBody(`{"agent_instance_id":"agent-worker","real_name":"执行专员","display_name":"执行专员","agent_kind":"ai","employment_type":"ai_agent","organization_id":"`+org.OrganizationID+`","department_id":"`+org.DepartmentID+`","position_id":"`+org.PositionID+`","persona":{"backstory":"负责发布协调执行。","responsibility_boundary":"负责发布执行任务并提交证据。"},"enabled":true}`))
	worker.AddCookie(loginCookie)
	worker.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, worker)
	if rec.Code != http.StatusOK {
		t.Fatalf("create worker status = %d body = %s", rec.Code, rec.Body.String())
	}
	listWorkers := httptest.NewRequest(http.MethodGet, "/api/workforce/members?enabled_only=true&limit=20", nil)
	listWorkers.Header.Set("X-User-ID", "superadmin")
	listWorkers.Header.Set("X-Viewer-Kind", "user")
	listWorkers.Header.Set("X-Permission-Scope", "workforce.read")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, listWorkers)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "agent-worker") {
		t.Fatalf("workforce after creating agent-worker status = %d body = %s", rec.Code, rec.Body.String())
	}

	send := httptest.NewRequest(http.MethodPost, "/api/discussions/messages", jsonBody(`{"content":"帮我拉两个人进来"}`))
	send.Header.Set("X-User-ID", "superadmin")
	send.Header.Set("X-Viewer-Kind", "user")
	send.Header.Set("X-User-Display-Name", "superadmin")
	send.Header.Set("Idempotency-Key", "discussion-lead-add-members-send-1")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, send)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("discussion mention send status = %d body = %s", rec.Code, rec.Body.String())
	}

	var conversation discussionsession.ConversationDTO
	if err := json.NewDecoder(rec.Body).Decode(&conversation); err != nil {
		t.Fatalf("decode discussion: %v", err)
	}
	if len(conversation.Messages) != 2 {
		t.Fatalf("messages = %+v events = %+v runs = %+v, want user and lead reply", conversation.Messages, conversation.Events, conversation.Runs)
	}
	if conversation.Messages[1].AuthorID != defaultLeadAgentID ||
		!strings.Contains(conversation.Messages[1].Content, "mention://user/system-superadmin") ||
		!strings.Contains(conversation.Messages[1].Content, "Members added through native PI tools") {
		t.Fatalf("lead reply = %+v", conversation.Messages[1])
	}
	if len(conversation.Runs) != 1 || conversation.Runs[0].AgentID != defaultLeadAgentID {
		t.Fatalf("runs = %+v, want one lead run", conversation.Runs)
	}
	if conversation.Runs[0].RuntimeKind != string(agentcore.RuntimeKindPi) {
		t.Fatalf("runs should use the native PI runtime: %+v", conversation.Runs)
	}
	if !conversation.Messages[0].CreatedAt.Before(conversation.Messages[1].CreatedAt) {
		t.Fatalf("message order timestamps = %+v", conversation.Messages)
	}
	if len(conversation.Members) != 3 {
		t.Fatalf("lead member add tool should add two enabled agents to the discussion group: %+v", conversation.Members)
	}
	if len(conversation.Runs[0].ToolCalls) != 2 {
		t.Fatalf("lead run tool calls = %+v, want talent list and member add", conversation.Runs[0].ToolCalls)
	}
	eligibleAgentIDs := conversation.Runs[0].ToolCalls[0].Result["eligible_agent_ids"]
	if conversation.Runs[0].ToolCalls[0].Name != "discussion_talent_list" ||
		!strings.Contains(eligibleAgentIDs, defaultCoordinatorAgentID) ||
		!strings.Contains(eligibleAgentIDs, "agent-worker") ||
		strings.Contains(eligibleAgentIDs, defaultQAPlanAgentID) ||
		strings.Contains(eligibleAgentIDs, defaultQACheckAgentID) ||
		conversation.Runs[0].ToolCalls[1].Name != "discussion_member_add" ||
		conversation.Runs[0].ToolCalls[1].Result["added_count"] != "2" ||
		conversation.Runs[0].ToolCalls[1].Arguments["agent_ids"] != memberAgentIDs {
		t.Fatalf("lead run tool calls = %+v", conversation.Runs[0].ToolCalls)
	}
}

func TestProductionHandlerWiresMissionDiscussionAndInboxPorts(t *testing.T) {
	root := t.TempDir()
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", productionTestDatabaseURLs(root))
	selectTestServerConfig(t)
	handler, cleanup, err := newServerHandler(context.Background(), false)
	if err != nil {
		t.Fatalf("newServerHandler production: %v", err)
	}
	defer cleanup()

	inbox := httptest.NewRequest(http.MethodGet, "/api/inbox?limit=10", nil)
	inbox.Header.Set("X-User-ID", "superadmin")
	inbox.Header.Set("X-Viewer-Kind", "user")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, inbox)
	if rec.Code != http.StatusOK {
		t.Fatalf("production inbox should be backed by the discussion service, status = %d body = %s", rec.Code, rec.Body.String())
	}

	post := httptest.NewRequest(http.MethodPost, "/api/missions/mission-missing/discussion/messages", jsonBody(`{"discussion_group_id":"discussion-missing","content":"hello","message_kind":"request","visibility":"mission"}`))
	post.Header.Set("X-User-ID", "superadmin")
	post.Header.Set("X-Viewer-Kind", "user")
	post.Header.Set("Idempotency-Key", "discussion-post-1")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, post)
	if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "resource_unavailable") {
		t.Fatalf("production discussion should reach the real discussion service, status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestProductionHandlerCreatesReadableDiscussionAndPersistsMessages(t *testing.T) {
	root := t.TempDir()
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", productionTestDatabaseURLs(root))
	selectTestServerConfig(t)
	handler, cleanup, err := newServerHandler(context.Background(), false)
	if err != nil {
		t.Fatalf("newServerHandler production: %v", err)
	}
	defer cleanup()

	create := httptest.NewRequest(http.MethodPost, "/api/missions", jsonBody(`{"title":"Discuss launch","final_goal":"Coordinate launch discussion"}`))
	create.Header.Set("X-User-ID", "superadmin")
	create.Header.Set("X-Viewer-Kind", "user")
	create.Header.Set("X-Permission-Scope", "mission.read,mission.write")
	create.Header.Set("Idempotency-Key", "mission-discussion-chain")
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
	discussionReq.Header.Set("X-Permission-Scope", "mission.read,mission.write")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, discussionReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("discussion status = %d body = %s", rec.Code, rec.Body.String())
	}
	var discussion missioncontrol.DiscussionDTO
	if err := json.NewDecoder(rec.Body).Decode(&discussion); err != nil {
		t.Fatalf("decode discussion: %v", err)
	}
	if discussion.DiscussionGroupID == "" {
		t.Fatalf("created mission did not expose discussion group: %+v", discussion)
	}

	post := httptest.NewRequest(http.MethodPost, "/api/missions/"+mission.MissionID+"/discussion/messages", jsonBody(`{"discussion_group_id":"`+discussion.DiscussionGroupID+`","content":"Launch coordination starts here","message_kind":"request","visibility":"mission"}`))
	post.Header.Set("X-User-ID", "superadmin")
	post.Header.Set("X-Viewer-Kind", "user")
	post.Header.Set("X-Permission-Scope", "mission.read,mission.write")
	post.Header.Set("Idempotency-Key", "discussion-message-chain")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, post)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("post discussion status = %d body = %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, discussionReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("discussion reload status = %d body = %s", rec.Code, rec.Body.String())
	}
	if err := json.NewDecoder(rec.Body).Decode(&discussion); err != nil {
		t.Fatalf("decode reloaded discussion: %v", err)
	}
	if len(discussion.Messages) != 1 || discussion.Messages[0].Body != "Launch coordination starts here" || discussion.Messages[0].AuthorID != agentcore.SystemSuperadminPrincipalID || discussion.Messages[0].AuthorType != "system" {
		t.Fatalf("discussion message was not projected back to the readable view: %+v", discussion)
	}
}

func TestProductionCreateMissionBootstrapsAuditedLeadOnly(t *testing.T) {
	root := t.TempDir()
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", productionTestDatabaseURLs(root))
	selectTestServerConfig(t)
	handler, cleanup, err := newServerHandler(context.Background(), false)
	if err != nil {
		t.Fatalf("newServerHandler production: %v", err)
	}
	defer cleanup()

	create := httptest.NewRequest(http.MethodPost, "/api/missions", jsonBody(`{"title":"Audited system roles","final_goal":"Verify Lead bootstrap"}`))
	create.Header.Set("X-User-ID", "superadmin")
	create.Header.Set("X-Viewer-Kind", "user")
	create.Header.Set("X-Permission-Scope", "mission.read,mission.write")
	create.Header.Set("Idempotency-Key", "mission-system-role-bootstrap")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, create)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create mission status = %d body = %s", rec.Code, rec.Body.String())
	}
	var mission missioncontrol.MissionDTO
	if err := json.NewDecoder(rec.Body).Decode(&mission); err != nil {
		t.Fatalf("decode mission: %v", err)
	}

	controlReq := httptest.NewRequest(http.MethodGet, "/api/missions/"+mission.MissionID+"/control", nil)
	controlReq.Header.Set("X-User-ID", "superadmin")
	controlReq.Header.Set("X-Viewer-Kind", "user")
	controlReq.Header.Set("X-Permission-Scope", "mission.read,mission.write")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, controlReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("control status = %d body = %s", rec.Code, rec.Body.String())
	}
	var control missioncontrol.MissionControlViewDTO
	if err := json.NewDecoder(rec.Body).Decode(&control); err != nil {
		t.Fatalf("decode control: %v", err)
	}
	memberRoles := map[string]string{}
	memberInstances := map[string]string{}
	for _, member := range control.Team.Members {
		memberRoles[member.AgentID] = member.MissionRole
		memberInstances[member.AgentID] = member.AgentInstanceID
	}
	if memberRoles[defaultLeadAgentID] != "Lead" {
		t.Fatalf("Lead member missing from audited team: %+v", control.Team.Members)
	}
	if _, ok := memberRoles["staffing-agent"]; ok {
		t.Fatalf("dedicated Staffing member should not be bootstrapped: %+v", control.Team.Members)
	}
	assignmentRoles := map[string]string{}
	assignmentInstances := map[string]string{}
	for _, assignment := range control.Team.Assignments {
		assignmentRoles[assignment.AgentID] = assignment.MissionRole
		assignmentInstances[assignment.AgentID] = assignment.AgentInstanceID
	}
	if assignmentRoles[defaultLeadAgentID] != "Lead" {
		t.Fatalf("Lead assignment missing from audited team: %+v", control.Team.Assignments)
	}
	if _, ok := assignmentRoles["staffing-agent"]; ok {
		t.Fatalf("dedicated Staffing assignment should not be bootstrapped: %+v", control.Team.Assignments)
	}
	if memberInstances[defaultLeadAgentID] != defaultLeadAgentID ||
		assignmentInstances[defaultLeadAgentID] != defaultLeadAgentID {
		t.Fatalf("audited team did not keep stable agent identities: members=%+v assignments=%+v", control.Team.Members, control.Team.Assignments)
	}

	discussionReq := httptest.NewRequest(http.MethodGet, "/api/missions/"+mission.MissionID+"/discussion", nil)
	discussionReq.Header.Set("X-User-ID", "superadmin")
	discussionReq.Header.Set("X-Viewer-Kind", "user")
	discussionReq.Header.Set("X-Permission-Scope", "mission.read,mission.write")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, discussionReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("discussion status = %d body = %s", rec.Code, rec.Body.String())
	}
	var discussion missioncontrol.DiscussionDTO
	if err := json.NewDecoder(rec.Body).Decode(&discussion); err != nil {
		t.Fatalf("decode discussion: %v", err)
	}
	post := httptest.NewRequest(http.MethodPost, "/api/missions/"+mission.MissionID+"/discussion/messages", jsonBody(`{"discussion_group_id":"`+discussion.DiscussionGroupID+`","content":"Lead is in the audited mission room","message_kind":"request","visibility":"mission"}`))
	post.Header.Set("X-Agent-ID", defaultLeadAgentID)
	post.Header.Set("X-Viewer-Kind", "agent")
	post.Header.Set("X-Permission-Scope", "mission.read,mission.write")
	post.Header.Set("Idempotency-Key", "lead-system-role-discussion-message")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, post)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("lead agent post discussion status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestProductionHandlerAllowsTaskAssigneeToPostDiscussionMessage(t *testing.T) {
	root := t.TempDir()
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", productionTestDatabaseURLs(root))
	selectTestServerConfig(t)
	handler, cleanup, err := newServerHandler(context.Background(), false)
	if err != nil {
		t.Fatalf("newServerHandler production: %v", err)
	}
	defer cleanup()

	create := httptest.NewRequest(http.MethodPost, "/api/missions", jsonBody(`{"title":"Assignee discussion","final_goal":"Coordinate assigned worker"}`))
	create.Header.Set("X-User-ID", "superadmin")
	create.Header.Set("X-Viewer-Kind", "user")
	create.Header.Set("X-Permission-Scope", "mission.read,mission.write")
	create.Header.Set("Idempotency-Key", "mission-assignee-discussion")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, create)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create mission status = %d body = %s", rec.Code, rec.Body.String())
	}
	var mission missioncontrol.MissionDTO
	if err := json.NewDecoder(rec.Body).Decode(&mission); err != nil {
		t.Fatalf("decode mission: %v", err)
	}

	createTask := httptest.NewRequest(http.MethodPost, "/api/missions/"+mission.MissionID+"/tasks", jsonBody(`{"assignee_id":"`+defaultCoordinatorAgentID+`","goal":"Prepare execution plan","worker_personal_goal":"Own the execution plan"}`))
	createTask.Header.Set("X-User-ID", "superadmin")
	createTask.Header.Set("X-Viewer-Kind", "user")
	createTask.Header.Set("X-Permission-Scope", "mission.read,mission.write")
	createTask.Header.Set("Idempotency-Key", "task-assignee-discussion")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, createTask)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create task status = %d body = %s", rec.Code, rec.Body.String())
	}
	var task missioncontrol.TaskCardDTO
	if err := json.NewDecoder(rec.Body).Decode(&task); err != nil {
		t.Fatalf("decode task: %v", err)
	}

	discussionReq := httptest.NewRequest(http.MethodGet, "/api/missions/"+mission.MissionID+"/discussion", nil)
	discussionReq.Header.Set("X-User-ID", "superadmin")
	discussionReq.Header.Set("X-Viewer-Kind", "user")
	discussionReq.Header.Set("X-Permission-Scope", "mission.read,mission.write")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, discussionReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("discussion status = %d body = %s", rec.Code, rec.Body.String())
	}
	var discussion missioncontrol.DiscussionDTO
	if err := json.NewDecoder(rec.Body).Decode(&discussion); err != nil {
		t.Fatalf("decode discussion: %v", err)
	}

	post := httptest.NewRequest(http.MethodPost, "/api/missions/"+mission.MissionID+"/discussion/messages", jsonBody(`{"discussion_group_id":"`+discussion.DiscussionGroupID+`","content":"Assigned worker has joined the discussion","message_kind":"request","visibility":"mission"}`))
	post.Header.Set("X-Agent-ID", defaultCoordinatorAgentID)
	post.Header.Set("X-Viewer-Kind", "agent")
	post.Header.Set("X-Permission-Scope", "mission.read,mission.write")
	post.Header.Set("Idempotency-Key", "assignee-discussion-message")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, post)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("assignee post discussion status = %d body = %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, discussionReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("discussion reload status = %d body = %s", rec.Code, rec.Body.String())
	}
	if err := json.NewDecoder(rec.Body).Decode(&discussion); err != nil {
		t.Fatalf("decode reloaded discussion: %v", err)
	}
	if len(discussion.Messages) != 1 || discussion.Messages[0].AuthorID != defaultCoordinatorAgentID || discussion.Messages[0].AuthorType != "agent" {
		t.Fatalf("assignee message was not projected back to discussion: %+v", discussion)
	}

	controlReq := httptest.NewRequest(http.MethodGet, "/api/missions/"+mission.MissionID+"/control", nil)
	controlReq.Header.Set("X-User-ID", "superadmin")
	controlReq.Header.Set("X-Viewer-Kind", "user")
	controlReq.Header.Set("X-Permission-Scope", "mission.read,mission.write")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, controlReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("control reload status = %d body = %s", rec.Code, rec.Body.String())
	}
	var control missioncontrol.MissionControlViewDTO
	if err := json.NewDecoder(rec.Body).Decode(&control); err != nil {
		t.Fatalf("decode control: %v", err)
	}
	if len(control.TaskBoard.Tasks) != 1 || control.TaskBoard.Tasks[0].TaskID != task.TaskID || control.TaskBoard.Tasks[0].Assignee != defaultCoordinatorAgentID {
		t.Fatalf("discussion refresh should preserve task board: %+v", control.TaskBoard)
	}
	foundMessageProgress := false
	for _, node := range control.ProgressGraph.Nodes {
		if node.EventType == "MessagePosted" {
			foundMessageProgress = true
			break
		}
	}
	if !foundMessageProgress {
		t.Fatalf("discussion refresh should expose message progress node: %+v", control.ProgressGraph)
	}
}

func TestProductionHandlerProjectsMentionInboxAndReplyClosure(t *testing.T) {
	root := t.TempDir()
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", productionTestDatabaseURLs(root))
	selectTestServerConfig(t)
	handler, cleanup, err := newServerHandler(context.Background(), false)
	if err != nil {
		t.Fatalf("newServerHandler production: %v", err)
	}
	defer cleanup()

	create := httptest.NewRequest(http.MethodPost, "/api/missions", jsonBody(`{"title":"Mention discussion","final_goal":"Close mention inbox through reply"}`))
	create.Header.Set("X-User-ID", "superadmin")
	create.Header.Set("X-Viewer-Kind", "user")
	create.Header.Set("X-Permission-Scope", "mission.read,mission.write")
	create.Header.Set("Idempotency-Key", "mission-mention-discussion")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, create)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create mission status = %d body = %s", rec.Code, rec.Body.String())
	}
	var mission missioncontrol.MissionDTO
	if err := json.NewDecoder(rec.Body).Decode(&mission); err != nil {
		t.Fatalf("decode mission: %v", err)
	}

	createTask := httptest.NewRequest(http.MethodPost, "/api/missions/"+mission.MissionID+"/tasks", jsonBody(`{"assignee_id":"`+defaultCoordinatorAgentID+`","goal":"Respond to mention","worker_personal_goal":"Reply to discussion mention"}`))
	createTask.Header.Set("X-User-ID", "superadmin")
	createTask.Header.Set("X-Viewer-Kind", "user")
	createTask.Header.Set("X-Permission-Scope", "mission.read,mission.write")
	createTask.Header.Set("Idempotency-Key", "task-mention-discussion")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, createTask)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create task status = %d body = %s", rec.Code, rec.Body.String())
	}

	discussionReq := httptest.NewRequest(http.MethodGet, "/api/missions/"+mission.MissionID+"/discussion", nil)
	discussionReq.Header.Set("X-User-ID", "superadmin")
	discussionReq.Header.Set("X-Viewer-Kind", "user")
	discussionReq.Header.Set("X-Permission-Scope", "mission.read,mission.write")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, discussionReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("discussion status = %d body = %s", rec.Code, rec.Body.String())
	}
	var discussion missioncontrol.DiscussionDTO
	if err := json.NewDecoder(rec.Body).Decode(&discussion); err != nil {
		t.Fatalf("decode discussion: %v", err)
	}

	content := "Please respond [@Coordinator](mention://agent/" + defaultCoordinatorAgentID + ")"
	post := httptest.NewRequest(http.MethodPost, "/api/missions/"+mission.MissionID+"/discussion/messages", jsonBody(`{"discussion_group_id":"`+discussion.DiscussionGroupID+`","content":"`+content+`","message_kind":"request","visibility":"mission","required_response":true}`))
	post.Header.Set("X-User-ID", "superadmin")
	post.Header.Set("X-Viewer-Kind", "user")
	post.Header.Set("X-Permission-Scope", "mission.read,mission.write")
	post.Header.Set("Idempotency-Key", "mention-discussion-message")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, post)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("post mention status = %d body = %s", rec.Code, rec.Body.String())
	}

	inboxReq := httptest.NewRequest(http.MethodGet, "/api/inbox?mission_id="+mission.MissionID, nil)
	inboxReq.Header.Set("X-Agent-ID", defaultCoordinatorAgentID)
	inboxReq.Header.Set("X-Viewer-Kind", "agent")
	inboxReq.Header.Set("X-Permission-Scope", "mission.read,mission.write")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, inboxReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("inbox status = %d body = %s", rec.Code, rec.Body.String())
	}
	var inbox missioncontrol.InboxPageDTO
	if err := json.NewDecoder(rec.Body).Decode(&inbox); err != nil {
		t.Fatalf("decode inbox: %v", err)
	}
	if len(inbox.Items) != 1 || inbox.Items[0].OwnerID != defaultCoordinatorAgentID || inbox.Items[0].Status != "pending" {
		t.Fatalf("mention inbox = %+v", inbox)
	}
	inboxID := inbox.Items[0].InboxItemID
	parentMessageID := inbox.Items[0].MessageID

	reply := httptest.NewRequest(http.MethodPost, "/api/missions/"+mission.MissionID+"/discussion/messages/"+parentMessageID+"/reply", jsonBody(`{"discussion_group_id":"`+discussion.DiscussionGroupID+`","content":"Acknowledged from the assigned coordinator","inbox_item_id":"`+inboxID+`"}`))
	reply.Header.Set("X-Agent-ID", defaultCoordinatorAgentID)
	reply.Header.Set("X-Viewer-Kind", "agent")
	reply.Header.Set("X-Permission-Scope", "mission.read,mission.write")
	reply.Header.Set("Idempotency-Key", "mention-discussion-reply")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, reply)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("reply status = %d body = %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, discussionReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("discussion reload status = %d body = %s", rec.Code, rec.Body.String())
	}
	if err := json.NewDecoder(rec.Body).Decode(&discussion); err != nil {
		t.Fatalf("decode reloaded discussion: %v", err)
	}
	if len(discussion.Messages) != 2 || discussion.Messages[1].ReplyToMessageID != parentMessageID || discussion.Messages[1].AuthorID != defaultCoordinatorAgentID {
		t.Fatalf("reply was not projected into discussion: %+v", discussion)
	}
	if len(discussion.PendingMentions) != 0 {
		t.Fatalf("reply should clear pending mention: %+v", discussion.PendingMentions)
	}

	inboxReq = httptest.NewRequest(http.MethodGet, "/api/inbox?mission_id="+mission.MissionID+"&include_closed=true", nil)
	inboxReq.Header.Set("X-Agent-ID", defaultCoordinatorAgentID)
	inboxReq.Header.Set("X-Viewer-Kind", "agent")
	inboxReq.Header.Set("X-Permission-Scope", "mission.read,mission.write")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, inboxReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("closed inbox status = %d body = %s", rec.Code, rec.Body.String())
	}
	if err := json.NewDecoder(rec.Body).Decode(&inbox); err != nil {
		t.Fatalf("decode closed inbox: %v", err)
	}
	if len(inbox.Items) != 1 || inbox.Items[0].Status != "responded" {
		t.Fatalf("reply should close inbox item: %+v", inbox)
	}
}

func TestProductionDiscussionMentionTriggersPiAgentReplyWithoutFrontend(t *testing.T) {
	root, err := os.MkdirTemp("", "orrery-pi-mention-")
	if err != nil {
		t.Fatalf("create short runtime root: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	t.Setenv("ORRERY_SECRET_LLM_UNUSED", "test-unused-key")
	executable := writePiTextRuntimeFixture(t)
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", productionTestDatabaseURLs(root))
	setTestConfigOverride(t, "discussion.agent.runtime_kind", "pi")
	setTestConfigOverride(t, "discussion.agent.runtime_config", `{"executable":"`+jsonPath(executable)+`"}`)
	selectTestServerConfig(t)
	handler, cleanup, err := newServerHandler(context.Background(), false)
	if err != nil {
		t.Fatalf("newServerHandler production: %v", err)
	}
	defer cleanup()

	putSettings := httptest.NewRequest(http.MethodPut, "/api/settings", jsonBody(`{"platform_name":"Orrery Production","organization_name":"Acme Production","language":"zh-CN","llm_profiles":[{"profile_id":"llm-unused","display_name":"Unused LLM","provider":"openai","base_url":"http://127.0.0.1:1/v1","api_key_ref":"secret://llm/unused","model":"glm-test","temperature":0.2,"context_length":128000,"enabled":true,"global_default":true}]}`))
	putSettings.Header.Set("X-User-ID", "superadmin")
	putSettings.Header.Set("X-Viewer-Kind", "user")
	putSettings.Header.Set("X-Permission-Scope", "settings.manage")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, putSettings)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /api/settings status = %d body = %s", rec.Code, rec.Body.String())
	}

	create := httptest.NewRequest(http.MethodPost, "/api/missions", jsonBody(`{"title":"Auto mention","final_goal":"Verify backend agent mention dispatch"}`))
	create.Header.Set("X-User-ID", "superadmin")
	create.Header.Set("X-Viewer-Kind", "user")
	create.Header.Set("X-Permission-Scope", "mission.read,mission.write")
	create.Header.Set("Idempotency-Key", "mission-auto-mention")
	rec = httptest.NewRecorder()
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
	discussionReq.Header.Set("X-Permission-Scope", "mission.read,mission.write")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, discussionReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("discussion status = %d body = %s", rec.Code, rec.Body.String())
	}
	var discussion missioncontrol.DiscussionDTO
	if err := json.NewDecoder(rec.Body).Decode(&discussion); err != nil {
		t.Fatalf("decode discussion: %v", err)
	}
	content := "请处理 [@Lead](mention://agent/" + defaultLeadAgentID + ")"
	post := httptest.NewRequest(http.MethodPost, "/api/missions/"+mission.MissionID+"/discussion/messages", jsonBody(`{"discussion_group_id":"`+discussion.DiscussionGroupID+`","content":"`+content+`","message_kind":"request","visibility":"mission"}`))
	post.Header.Set("X-User-ID", "superadmin")
	post.Header.Set("X-Viewer-Kind", "user")
	post.Header.Set("X-Permission-Scope", "mission.read,mission.write")
	post.Header.Set("Idempotency-Key", "auto-mention-message")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, post)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("post mention status = %d body = %s", rec.Code, rec.Body.String())
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		rec = httptest.NewRecorder()
		serveServerTestHTTP(t, handler, rec, discussionReq)
		if rec.Code != http.StatusOK {
			t.Fatalf("discussion reload status = %d body = %s", rec.Code, rec.Body.String())
		}
		discussion = missioncontrol.DiscussionDTO{}
		if err := json.NewDecoder(rec.Body).Decode(&discussion); err != nil {
			t.Fatalf("decode reloaded discussion: %v", err)
		}
		if len(discussion.Messages) >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for backend mention reply, discussion=%+v", discussion)
		}
		time.Sleep(50 * time.Millisecond)
	}

	reply := discussion.Messages[len(discussion.Messages)-1]
	if reply.AuthorID != defaultLeadAgentID || reply.AuthorType != "agent" || reply.Body != "What deadline should I optimize for?" || reply.ReplyToMessageID == "" {
		t.Fatalf("backend mention reply not projected correctly: %+v", discussion.Messages)
	}
	if len(discussion.PendingMentions) != 0 {
		t.Fatalf("backend reply should close pending mention: %+v", discussion.PendingMentions)
	}
}
