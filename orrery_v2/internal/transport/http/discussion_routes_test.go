package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/userauth"
)

func TestDiscussionRoutesSendMessageThroughService(t *testing.T) {
	service := &fakeDiscussionService{conversation: discussionsession.ConversationDTO{
		ConversationID: "conv-1",
		Messages: []discussionsession.MessageDTO{{
			MessageID:      "msg-1",
			ConversationID: "conv-1",
			Role:           "user",
			Content:        "Plan launch",
			Mentions:       []discussionsession.MessageMentionDTO{{TargetKind: "agent", TargetID: "reviewer-agent", DisplayName: "Reviewer"}},
			CreatedAt:      time.Date(2026, 6, 2, 1, 2, 3, 0, time.UTC),
		}},
		Turns: []discussionsession.TurnDTO{{TurnID: "turn-1", ConversationID: "conv-1", RootMessageID: "msg-1", Mode: "mention", Status: "completed", CreatedAt: time.Date(2026, 6, 2, 1, 2, 3, 0, time.UTC), UpdatedAt: time.Date(2026, 6, 2, 1, 2, 4, 0, time.UTC)}},
	}}
	handler := newTestRouter(RouterOptions{Discussions: service})
	req := authedRequest(http.MethodPost, "/api/discussions/messages", `{"content":"@Reviewer Plan launch","attachment_ids":["att-1","att-2"],"mentions":[{"target_kind":"agent","target_id":"reviewer-agent","display_name":"Reviewer"}]}`)
	req.Header.Set("Idempotency-Key", "send-1")
	req.Header.Set("X-User-ID", "operator")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-User-Display-Name", "%E6%9C%AC%E5%9C%B0%E7%94%A8%E6%88%B7")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !service.called || service.command.Content != "@Reviewer Plan launch" || service.command.IdempotencyKey != "send-1" || strings.Join(service.command.AttachmentIDs, ",") != "att-1,att-2" {
		t.Fatalf("service command = %+v", service.command)
	}
	if len(service.command.Mentions) != 1 || service.command.Mentions[0].TargetKind != "agent" || service.command.Mentions[0].TargetID != "reviewer-agent" || service.command.Mentions[0].DisplayName != "Reviewer" {
		t.Fatalf("service mentions = %+v", service.command.Mentions)
	}
	if service.viewer.ViewerID != "operator" || service.viewer.DisplayName != "本地用户" || service.viewer.Language != "zh-CN" {
		t.Fatalf("viewer = %+v", service.viewer)
	}
	assertDiscussionBodyUsesNeutralIDs(t, rec.Body.String())
	if !strings.Contains(rec.Body.String(), `"mentions":[{"target_kind":"agent","target_id":"reviewer-agent"`) || !strings.Contains(rec.Body.String(), `"turn_id":"turn-1","discussion_id":"conv-1"`) {
		t.Fatalf("discussion response missing structured mention/turn correlation: %s", rec.Body.String())
	}
}

func TestDiscussionViewerTreatsOnlySystemSuperadminAsSystemPrincipal(t *testing.T) {
	request := func(profileID, username string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/api/discussions/messages", nil)
		principal := identity.Principal{
			SubjectProvider: identity.ProviderLocal,
			Subject:         username,
			UserID:          username,
			Username:        username,
			AgentProfileID:  profileID,
			ViewerKind:      identity.ViewerKindUser,
		}
		return req.WithContext(identity.ContextWithPrincipal(req.Context(), principal))
	}

	system, ok := discussionViewerFromRequest(httptest.NewRecorder(), request(agentcore.SystemSuperadminPrincipalID, "superadmin"))
	if !ok || system.ViewerKind != discussionsession.ViewerKindSystem || system.ViewerID != agentcore.SystemSuperadminPrincipalID || system.AgentProfileID != "" {
		t.Fatalf("system discussion viewer = %+v ok=%v", system, ok)
	}

	legacy, ok := discussionViewerFromRequest(httptest.NewRecorder(), request(agentcore.LegacyLocalAdminAgentProfileID, "local-admin"))
	if !ok || legacy.ViewerKind != discussionsession.ViewerKindUser || legacy.AgentProfileID != agentcore.LegacyLocalAdminAgentProfileID {
		t.Fatalf("legacy placeholder must not become a system viewer: %+v ok=%v", legacy, ok)
	}
}

func TestDiscussionRoutesSendMessagePassesPersonalAssistantKind(t *testing.T) {
	service := &fakeDiscussionService{conversation: discussionsession.ConversationDTO{
		ConversationID:   "conv-assistant",
		ConversationKind: discussionsession.ConversationKindPersonalAssistant,
		Title:            "My assistant",
		Status:           "completed",
	}}
	handler := newTestRouter(RouterOptions{Discussions: service})
	req := authedRequest(http.MethodPost, "/api/discussions/messages", `{"content":"Handle this","conversation_kind":"personal_assistant"}`)
	req.Header.Set("Idempotency-Key", "send-assistant-1")
	req.Header.Set("X-User-ID", "operator")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !service.called || service.command.ConversationKind != discussionsession.ConversationKindPersonalAssistant {
		t.Fatalf("service command = %+v called=%v", service.command, service.called)
	}
}

func TestDiscussionRoutesUseBoundHumanProfileAsConversationIdentity(t *testing.T) {
	service := &fakeDiscussionService{conversation: discussionsession.ConversationDTO{
		ConversationID: "conv-human",
		Messages: []discussionsession.MessageDTO{
			{MessageID: "msg-profile", ConversationID: "conv-human", Role: "user", AuthorID: "human-operator", Content: "profile"},
			{MessageID: "msg-account", ConversationID: "conv-human", Role: "user", AuthorID: "account-operator", Content: "account"},
			{MessageID: "msg-username", ConversationID: "conv-human", Role: "user", AuthorID: "operator", Content: "username"},
			{MessageID: "msg-subject", ConversationID: "conv-human", Role: "user", AuthorID: "kratos-operator", Content: "subject"},
			{MessageID: "msg-other", ConversationID: "conv-human", Role: "user", AuthorID: "human-other", Content: "other"},
			{MessageID: "msg-agent", ConversationID: "conv-human", Role: "assistant", AuthorID: "human-operator", Content: "agent"},
		},
	}}
	handler := newTestRouter(RouterOptions{Discussions: service})
	principal := identity.Principal{
		SubjectProvider: "kratos", Subject: "kratos-operator", UserID: "account-operator", Username: "operator",
		DisplayName: "Operator", AgentProfileID: "human-operator", ViewerKind: identity.ViewerKindUser, TenantID: "tenant-a",
		CoarseScopes: []string{"discussion.read"},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/discussions/conv-human", nil)
	req = req.WithContext(identity.ContextWithPrincipal(req.Context(), principal))
	applyPrincipalViewer(req, principal)
	authorizeInternalViewerTestRequest(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if service.viewer.ViewerID != principal.AgentProfileID || service.viewer.AgentProfileID != principal.AgentProfileID || service.viewer.OwnerID != principal.UserID || service.viewer.TenantID != principal.TenantID {
		t.Fatalf("discussion viewer = %+v, want canonical profile %q", service.viewer, principal.AgentProfileID)
	}
	if !containsExactString(service.viewer.PermissionScope, "discussion.read") {
		t.Fatalf("discussion viewer permissions = %+v", service.viewer.PermissionScope)
	}
	for _, identityID := range []string{principal.AgentProfileID, principal.UserID, principal.Username, principal.Subject} {
		if !containsExactString(service.viewer.IdentityIDs, identityID) {
			t.Fatalf("discussion viewer identities = %+v, missing %q", service.viewer.IdentityIDs, identityID)
		}
	}
	var body discussionDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Messages) != 6 {
		t.Fatalf("messages = %+v", body.Messages)
	}
	wantCurrentViewer := []bool{true, true, true, true, false, false}
	for index, want := range wantCurrentViewer {
		if body.Messages[index].IsCurrentViewer == nil || *body.Messages[index].IsCurrentViewer != want {
			t.Fatalf("message %q is_current_viewer = %v, want %v", body.Messages[index].MessageID, body.Messages[index].IsCurrentViewer, want)
		}
	}
}

func TestDiscussionRoutesKeepSystemSuperadminOutsideWorkforceOrganizationScope(t *testing.T) {
	service := &fakeDiscussionService{}
	store := userauth.NewMemoryAccountStore()
	if err := userauth.EnsureBootstrapSuperadmin(context.Background(), store, userauth.SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	auth := userauth.NewService(store)
	challenge, err := auth.BeginLogin(context.Background(), userauth.BeginLoginRequest{Username: userauth.SuperadminUsername})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	session, err := auth.CompleteLogin(context.Background(), userauth.CompleteLoginRequest{
		Username: userauth.SuperadminUsername, ChallengeID: challenge.ChallengeID, Password: userauth.SuperadminPassword(),
	})
	if err != nil {
		t.Fatalf("CompleteLogin: %v", err)
	}
	profiles := &fakeProfileWeixinDirectory{entries: []agentcore.AgentDirectoryEntry{{
		AgentProfileID: userauth.SuperadminAgentProfileID,
		AgentKind:      agentcore.AgentKindHuman,
		Enabled:        true,
		OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef: agentcore.OrganizationRef{ID: "minvia"},
		},
	}}}
	handler := NewRouter(RouterOptions{Auth: auth, Discussions: service, AgentProfiles: profiles})
	req := httptest.NewRequest(http.MethodGet, "/api/discussions", nil)
	req.AddCookie(&http.Cookie{Name: authAccessTokenCookieName, Value: session.AccessToken})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if service.viewer.ViewerID != userauth.SuperadminAgentProfileID || service.viewer.OwnerID != userauth.SuperadminUsername || service.viewer.ViewerKind != discussionsession.ViewerKindSystem || service.viewer.AgentProfileID != "" || service.viewer.TenantID != "global" {
		t.Fatalf("discussion viewer = %+v", service.viewer)
	}
	if req.Header.Get("X-Organization-ID") != "" || req.Header.Get("X-Tenant-ID") != "" {
		t.Fatalf("resolved scope headers: organization=%q tenant=%q", req.Header.Get("X-Organization-ID"), req.Header.Get("X-Tenant-ID"))
	}
}

func TestDiscussionRoutesPrewarmCreatesDraftAndWarmsExistingDiscussion(t *testing.T) {
	service := &fakeDiscussionService{conversation: discussionsession.ConversationDTO{ConversationID: "conv-draft", Title: "New chat", Status: "draft"}}
	handler := newTestRouter(RouterOptions{Discussions: service})

	create := authedRequest(http.MethodPost, "/api/discussions/prewarm", `{"title":"My assistant","conversation_kind":"personal_assistant"}`)
	create.Header.Set("Idempotency-Key", "prewarm-1")
	create.Header.Set("X-User-ID", "operator")
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, create)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", createRec.Code, createRec.Body.String())
	}
	if !service.prewarmCalled || service.prewarmCommand.Title != "My assistant" || service.prewarmCommand.ConversationKind != discussionsession.ConversationKindPersonalAssistant || service.prewarmCommand.IdempotencyKey != "prewarm-1" || service.prewarmCommand.ConversationID != "" {
		t.Fatalf("create prewarm command = %+v", service.prewarmCommand)
	}

	service.prewarmCalled = false
	existing := authedRequest(http.MethodPost, "/api/discussions/conv-existing/prewarm", ``)
	existing.Header.Set("X-User-ID", "operator")
	existingRec := httptest.NewRecorder()
	handler.ServeHTTP(existingRec, existing)
	if existingRec.Code != http.StatusAccepted {
		t.Fatalf("existing status = %d body=%s", existingRec.Code, existingRec.Body.String())
	}
	if !service.prewarmCalled || service.prewarmCommand.ConversationID != "conv-existing" {
		t.Fatalf("existing prewarm command = %+v", service.prewarmCommand)
	}
}

func TestDiscussionRoutesDecideRuntimeApprovalThroughService(t *testing.T) {
	service := &fakeDiscussionService{conversation: discussionsession.ConversationDTO{
		ConversationID: "conv-1",
		Status:         "completed",
	}}
	handler := newTestRouter(RouterOptions{Discussions: service})
	req := authedRequest(http.MethodPost, "/api/discussions/conv-1/approvals/cmd-1/decision", `{"action_id":"approve_session","message":"ok","remember":true}`)
	req.Header.Set("Idempotency-Key", "test-idempotency-key")
	req.Header.Set("X-User-ID", "operator")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !service.approvalDecisionCalled {
		t.Fatal("approval decision service was not called")
	}
	if service.approvalDecision.ConversationID != "conv-1" || service.approvalDecision.ApprovalID != "cmd-1" || service.approvalDecision.ActionID != "approve_session" || service.approvalDecision.IdempotencyKey != "test-idempotency-key" || !service.approvalDecision.Remember {
		t.Fatalf("approval decision command = %+v", service.approvalDecision)
	}
}

func TestDiscussionRoutesRejectsLegacyDecisionForHILBackedApproval(t *testing.T) {
	service := &fakeDiscussionService{conversation: discussionsession.ConversationDTO{
		ConversationID: "conv-hil",
		Events: []discussionsession.EventDTO{{
			EventID: "approval-hil", Kind: "discussion_runtime_approval_requested", Status: "pending",
			Metadata: map[string]string{"approval_request_id": "approval-hil", "hil_decision_id": "hil-decision-1"},
		}},
	}}
	handler := newTestRouter(RouterOptions{Discussions: service})
	req := authedRequest(http.MethodPost, "/api/discussions/conv-hil/approvals/approval-hil/decision", `{"action_id":"approve_once"}`)
	req.Header.Set("Idempotency-Key", "test-idempotency-key")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.approvalDecisionCalled {
		t.Fatal("legacy decision service was called for HIL-backed approval")
	}
	var body ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Code != ErrorConflict || body.Details["hil_decision_id"] != "hil-decision-1" {
		t.Fatalf("error body=%+v", body)
	}
}

func TestDiscussionRoutesSendMessagePassesDirectAgentID(t *testing.T) {
	service := &fakeDiscussionService{conversation: discussionsession.ConversationDTO{
		ConversationID:   "conv-direct",
		ConversationKind: "direct",
		DirectAgentID:    "staffing-agent",
		DirectTargetKind: "agent",
		DirectTargetID:   "staffing-agent",
		Title:            "秦若兰",
		Status:           "completed",
	}}
	handler := newTestRouter(RouterOptions{Discussions: service})
	req := authedRequest(http.MethodPost, "/api/discussions/messages", `{"content":"Can you staff this?","force_new_conversation":true,"direct_agent_id":"staffing-agent"}`)
	req.Header.Set("Idempotency-Key", "send-direct-1")
	req.Header.Set("X-User-ID", "operator")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !service.called || service.command.DirectAgentID != "staffing-agent" || !service.command.ForceNewConversation {
		t.Fatalf("service command = %+v called=%v", service.command, service.called)
	}
	var body struct {
		DiscussionID     string `json:"discussion_id"`
		ConversationKind string `json:"conversation_kind"`
		DirectAgentID    string `json:"direct_agent_id"`
		DirectTargetKind string `json:"direct_target_kind"`
		DirectTargetID   string `json:"direct_target_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.DiscussionID != "conv-direct" || body.ConversationKind != "direct" || body.DirectAgentID != "staffing-agent" || body.DirectTargetKind != "agent" || body.DirectTargetID != "staffing-agent" {
		t.Fatalf("body = %+v", body)
	}
	assertDiscussionBodyUsesNeutralIDs(t, rec.Body.String())
}

func TestDiscussionRoutesSendMessagePassesDirectTarget(t *testing.T) {
	service := &fakeDiscussionService{conversation: discussionsession.ConversationDTO{
		ConversationID:   "conv-direct-human",
		ConversationKind: "direct",
		DirectTargetKind: "user",
		DirectTargetID:   "human-reviewer",
		Title:            "陆清岚",
		Status:           "completed",
	}}
	handler := newTestRouter(RouterOptions{Discussions: service})
	req := authedRequest(http.MethodPost, "/api/discussions/messages", `{"content":"Can you review this?","direct_target_id":"human-reviewer"}`)
	req.Header.Set("Idempotency-Key", "send-direct-human-1")
	req.Header.Set("X-User-ID", "operator")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !service.called || service.command.DirectTargetKind != "" || service.command.DirectTargetID != "human-reviewer" || service.command.DirectAgentID != "" {
		t.Fatalf("service command = %+v called=%v", service.command, service.called)
	}
	var body struct {
		DiscussionID     string `json:"discussion_id"`
		ConversationKind string `json:"conversation_kind"`
		DirectTargetKind string `json:"direct_target_kind"`
		DirectTargetID   string `json:"direct_target_id"`
		DirectAgentID    string `json:"direct_agent_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.DiscussionID != "conv-direct-human" || body.ConversationKind != "direct" || body.DirectTargetKind != "user" || body.DirectTargetID != "human-reviewer" || body.DirectAgentID != "" {
		t.Fatalf("body = %+v", body)
	}
	assertDiscussionBodyUsesNeutralIDs(t, rec.Body.String())
}

func TestDiscussionRoutesAddMembersPassesTargetIDs(t *testing.T) {
	service := &fakeDiscussionService{conversation: discussionsession.ConversationDTO{
		ConversationID: "conv-direct-human",
		Title:          "聊天",
		Status:         "completed",
		Members: []discussionsession.ConversationMemberDTO{
			{Kind: "user", ID: "human-reviewer", DisplayName: "陆清岚", Role: "私聊对象"},
			{Kind: "user", ID: "operator", DisplayName: "Operator", Role: "话题发起人"},
			{Kind: "agent", ID: "lead-agent", DisplayName: "顾明澈", Role: "Lead"},
		},
	}}
	handler := newTestRouter(RouterOptions{Discussions: service})
	req := authedRequest(http.MethodPost, "/api/discussions/conv-direct-human/members", `{"target_ids":["lead-agent"]}`)
	req.Header.Set("Idempotency-Key", "add-lead")
	req.Header.Set("X-User-ID", "operator")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !service.addMembersCalled || service.addMembersCommand.ConversationID != "conv-direct-human" || strings.Join(service.addMembersCommand.TargetIDs, ",") != "lead-agent" || service.addMembersCommand.IdempotencyKey != "add-lead" {
		t.Fatalf("add members command = %+v called=%v", service.addMembersCommand, service.addMembersCalled)
	}
	var body struct {
		DiscussionID     string `json:"discussion_id"`
		ConversationKind string `json:"conversation_kind"`
		DirectTargetID   string `json:"direct_target_id"`
		Members          []struct {
			ID string `json:"id"`
		} `json:"members"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.DiscussionID != "conv-direct-human" || body.ConversationKind != "" || body.DirectTargetID != "" || len(body.Members) != 3 {
		t.Fatalf("body = %+v", body)
	}
	assertDiscussionBodyUsesNeutralIDs(t, rec.Body.String())
}

func TestDiscussionRoutesSetWorkspacePassesServerDirectory(t *testing.T) {
	service := &fakeDiscussionService{conversation: discussionsession.ConversationDTO{
		ConversationID:   "conv-1",
		Title:            "聊天",
		Status:           "completed",
		WorkspaceRootDir: "/srv/orrery/workspaces/launch",
	}}
	handler := newTestRouter(RouterOptions{Discussions: service})
	req := authedRequest(http.MethodPatch, "/api/discussions/conv-1/workspace", `{"workspace_root_dir":"/srv/orrery/workspaces/launch"}`)
	req.Header.Set("Idempotency-Key", "workspace-1")
	req.Header.Set("X-User-ID", "operator")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !service.setWorkspaceCalled || service.setWorkspaceCommand.ConversationID != "conv-1" || service.setWorkspaceCommand.WorkspaceRootDir != "/srv/orrery/workspaces/launch" || service.setWorkspaceCommand.IdempotencyKey != "workspace-1" {
		t.Fatalf("workspace command = %+v called=%v", service.setWorkspaceCommand, service.setWorkspaceCalled)
	}
	var body struct {
		DiscussionID     string `json:"discussion_id"`
		WorkspaceRootDir string `json:"workspace_root_dir"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.DiscussionID != "conv-1" || body.WorkspaceRootDir != "/srv/orrery/workspaces/launch" {
		t.Fatalf("body = %+v", body)
	}
	assertDiscussionBodyUsesNeutralIDs(t, rec.Body.String())
}

func TestDiscussionRoutesSetHILMode(t *testing.T) {
	service := &fakeDiscussionService{conversation: discussionsession.ConversationDTO{
		ConversationID: "conv-1",
		Revision:       7,
		HILMode:        discussionsession.DiscussionHILModeNone,
		Title:          "聊天",
		Status:         "completed",
	}}
	handler := newTestRouter(RouterOptions{Discussions: service})
	req := authedRequest(http.MethodPatch, "/api/discussions/conv-1/hil-mode", `{"mode":"none"}`)
	req.Header.Set("Idempotency-Key", "hil-mode-1")
	req.Header.Set("X-User-ID", "operator")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !service.setHILModeCalled || service.setHILModeCommand.ConversationID != "conv-1" || service.setHILModeCommand.Mode != "none" || service.setHILModeCommand.IdempotencyKey != "hil-mode-1" {
		t.Fatalf("HIL mode command = %+v called=%v", service.setHILModeCommand, service.setHILModeCalled)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["discussion_id"] != "conv-1" || body["hil_mode"] != "none" || body["revision"] != float64(7) {
		t.Fatalf("body = %+v", body)
	}
	if len(body) != 4 {
		t.Fatalf("HIL mode response fields = %v, want only lightweight update fields", body)
	}
}

func TestDiscussionRoutesRejectRemovedDiscussionPath(t *testing.T) {
	service := &fakeDiscussionService{conversation: discussionsession.ConversationDTO{ConversationID: "conv-1"}}
	handler := newTestRouter(RouterOptions{Discussions: service})
	req := authedRequest(http.MethodPost, "/api/pm/conversations/messages", `{"content":"Plan launch"}`)
	req.Header.Set("Idempotency-Key", "send-1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("removed discussion route status = %d body=%s", rec.Code, rec.Body.String())
	}
	if service.called || service.streamCalled || service.listCalled {
		t.Fatalf("removed discussion route must not call discussion service")
	}
}

func TestDiscussionRoutesListConversationsPassesPagination(t *testing.T) {
	service := &fakeDiscussionService{page: discussionsession.ConversationPageDTO{
		Conversations: []discussionsession.ConversationSummaryDTO{{ConversationID: "conv-2", Title: "Second"}},
		Page:          discussionsession.PageDTO{Page: 2, PageSize: 5, Total: 12},
	}}
	handler := newTestRouter(RouterOptions{Discussions: service})
	req := authedRequest(http.MethodGet, "/api/discussions?page=2&page_size=5", ``)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !service.listCalled || service.listQuery.Page != 2 || service.listQuery.PageSize != 5 {
		t.Fatalf("list query = %+v called=%v", service.listQuery, service.listCalled)
	}
	var body struct {
		Discussions []struct {
			DiscussionID string `json:"discussion_id"`
		} `json:"discussions"`
		Page discussionsession.PageDTO `json:"page"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Page.Total != 12 || len(body.Discussions) != 1 || body.Discussions[0].DiscussionID != "conv-2" {
		t.Fatalf("body = %+v", body)
	}
	assertDiscussionBodyUsesNeutralIDs(t, rec.Body.String())
}

func TestDiscussionRoutesGetDiscussionUsesRecentMessagePageByDefault(t *testing.T) {
	service := &fakeDiscussionService{conversation: discussionsession.ConversationDTO{
		ConversationID: "conv-1",
		MessagePage:    &discussionsession.MessagePageMetaDTO{Limit: 20, Offset: 0, Total: 32, Returned: 20, HasMore: true},
		Messages: []discussionsession.MessageDTO{{
			MessageID:      "msg-32",
			ConversationID: "conv-1",
			Role:           "assistant",
			Content:        "latest",
			CreatedAt:      time.Date(2026, 6, 2, 1, 2, 3, 0, time.UTC),
		}},
	}}
	handler := newTestRouter(RouterOptions{Discussions: service})
	req := authedRequest(http.MethodGet, "/api/discussions/conv-1", ``)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !service.getPageCalled || service.getCalled {
		t.Fatalf("getPageCalled=%v getCalled=%v", service.getPageCalled, service.getCalled)
	}
	if service.getConversationID != "conv-1" || service.messageQuery.Limit != 20 || service.messageQuery.Offset != 0 {
		t.Fatalf("get page id=%q query=%+v", service.getConversationID, service.messageQuery)
	}
	var body struct {
		DiscussionID string                               `json:"discussion_id"`
		Messages     []discussionMessageDTO               `json:"messages"`
		MessagePage  discussionsession.MessagePageMetaDTO `json:"message_page"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.DiscussionID != "conv-1" || len(body.Messages) != 1 || body.MessagePage.Total != 32 || !body.MessagePage.HasMore {
		t.Fatalf("body = %+v", body)
	}
	assertDiscussionBodyUsesNeutralIDs(t, rec.Body.String())
}

func TestDiscussionRoutesListDiscussionMessagesPassesPagination(t *testing.T) {
	service := &fakeDiscussionService{messagePage: discussionsession.MessagePageDTO{
		Messages: []discussionsession.MessageDTO{{
			MessageID:      "msg-21",
			ConversationID: "conv-1",
			Role:           "assistant",
			Content:        "older",
			CreatedAt:      time.Date(2026, 6, 2, 1, 2, 3, 0, time.UTC),
		}},
		Page: discussionsession.MessagePageMetaDTO{Limit: 20, Offset: 20, Total: 21, Returned: 1, HasMore: false},
	}}
	handler := newTestRouter(RouterOptions{Discussions: service})
	req := authedRequest(http.MethodGet, "/api/discussions/conv-1/messages?limit=20&offset=20", ``)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !service.listMessagesCalled || service.getCalled || service.getPageCalled {
		t.Fatalf("listMessagesCalled=%v getCalled=%v getPageCalled=%v", service.listMessagesCalled, service.getCalled, service.getPageCalled)
	}
	if service.getConversationID != "conv-1" || service.messageQuery.Limit != 20 || service.messageQuery.Offset != 20 {
		t.Fatalf("message page id=%q query=%+v", service.getConversationID, service.messageQuery)
	}
	var body struct {
		Messages []discussionMessageDTO               `json:"messages"`
		Page     discussionsession.MessagePageMetaDTO `json:"page"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Messages) != 1 || body.Messages[0].DiscussionID != "conv-1" || body.Page.Offset != 20 || body.Page.HasMore {
		t.Fatalf("body = %+v", body)
	}
	assertDiscussionBodyUsesNeutralIDs(t, rec.Body.String())
}

func TestDiscussionRoutesUploadAttachmentPassesMultipartFile(t *testing.T) {
	service := &fakeDiscussionService{attachment: discussionsession.AttachmentDTO{
		AttachmentID:   "att-1",
		ConversationID: "conv-1",
		FileName:       "brief.txt",
		ContentType:    "text/plain",
		SizeBytes:      12,
		UploadedBy:     "operator",
		DownloadURL:    "/api/discussions/conv-1/attachments/att-1/download",
		CreatedAt:      time.Date(2026, 6, 2, 1, 2, 3, 0, time.UTC),
	}}
	handler := newTestRouter(RouterOptions{Discussions: service})
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "brief.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("hello world!")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := authedRequest(http.MethodPost, "/api/discussions/conv-1/attachments", "")
	req.Body = io.NopCloser(&body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Idempotency-Key", "upload-1")
	req.Header.Set("X-User-ID", "operator")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !service.uploadAttachmentCalled || service.uploadCommand.ConversationID != "conv-1" || service.uploadCommand.FileName != "brief.txt" || service.uploadCommand.IdempotencyKey != "upload-1" {
		t.Fatalf("upload command = %+v called=%v", service.uploadCommand, service.uploadAttachmentCalled)
	}
	uploaded, err := io.ReadAll(service.uploadCommand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(uploaded) != "hello world!" {
		t.Fatalf("uploaded bytes = %q", uploaded)
	}
	var response struct {
		AttachmentID string `json:"attachment_id"`
		DiscussionID string `json:"discussion_id"`
		DownloadURL  string `json:"download_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.AttachmentID != "att-1" || response.DiscussionID != "conv-1" || response.DownloadURL == "" {
		t.Fatalf("response = %+v", response)
	}
	assertDiscussionBodyUsesNeutralIDs(t, rec.Body.String())
}

func TestDiscussionRoutesStartAttachmentDraftPassesMultipartFileAndDirectTarget(t *testing.T) {
	service := &fakeDiscussionService{attachmentDraft: discussionsession.AttachmentDraftDTO{
		Conversation: discussionsession.ConversationDTO{
			ConversationID:   "conv-direct",
			ConversationKind: "direct",
			DirectAgentID:    "staffing-agent",
			DirectTargetKind: "agent",
			DirectTargetID:   "staffing-agent",
			Title:            "秦若兰",
			Status:           "completed",
			CreatedAt:        time.Date(2026, 6, 2, 1, 2, 3, 0, time.UTC),
			UpdatedAt:        time.Date(2026, 6, 2, 1, 2, 3, 0, time.UTC),
		},
		Attachment: discussionsession.AttachmentDTO{
			AttachmentID:   "att-1",
			ConversationID: "conv-direct",
			FileName:       "brief.txt",
			ContentType:    "text/plain",
			SizeBytes:      12,
			UploadedBy:     "operator",
			DownloadURL:    "/api/discussions/conv-direct/attachments/att-1/download",
			CreatedAt:      time.Date(2026, 6, 2, 1, 2, 3, 0, time.UTC),
		},
	}}
	handler := newTestRouter(RouterOptions{Discussions: service})
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("direct_target_id", "staffing-agent"); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("file", "brief.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("hello world!")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := authedRequest(http.MethodPost, "/api/discussions/attachments", "")
	req.Body = io.NopCloser(&body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Idempotency-Key", "draft-upload-1")
	req.Header.Set("X-User-ID", "operator")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !service.startAttachmentDraftCalled || service.uploadCommand.ConversationID != "" || service.uploadCommand.DirectTargetID != "staffing-agent" || service.uploadCommand.FileName != "brief.txt" || service.uploadCommand.IdempotencyKey != "draft-upload-1" {
		t.Fatalf("draft command = %+v called=%v", service.uploadCommand, service.startAttachmentDraftCalled)
	}
	uploaded, err := io.ReadAll(service.uploadCommand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(uploaded) != "hello world!" {
		t.Fatalf("uploaded bytes = %q", uploaded)
	}
	var response struct {
		Discussion struct {
			DiscussionID     string `json:"discussion_id"`
			ConversationKind string `json:"conversation_kind"`
			DirectTargetID   string `json:"direct_target_id"`
		} `json:"discussion"`
		Attachment struct {
			AttachmentID string `json:"attachment_id"`
			DiscussionID string `json:"discussion_id"`
		} `json:"attachment"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Discussion.DiscussionID != "conv-direct" || response.Discussion.ConversationKind != "direct" || response.Discussion.DirectTargetID != "staffing-agent" || response.Attachment.DiscussionID != "conv-direct" {
		t.Fatalf("response = %+v", response)
	}
	assertDiscussionBodyUsesNeutralIDs(t, rec.Body.String())
}

func TestDiscussionRoutesListAndDownloadAttachments(t *testing.T) {
	service := &fakeDiscussionService{
		attachmentPage: discussionsession.AttachmentPageDTO{Attachments: []discussionsession.AttachmentDTO{{
			AttachmentID:   "att-1",
			ConversationID: "conv-1",
			FileName:       "brief.txt",
			ContentType:    "text/plain",
			SizeBytes:      12,
			UploadedBy:     "operator",
			StoragePath:    "/data/private/brief.txt",
			DownloadURL:    "/api/discussions/conv-1/attachments/att-1/download",
			PreviewURL:     "/api/discussions/conv-1/attachments/att-1/preview",
			CreatedAt:      time.Date(2026, 6, 2, 1, 2, 3, 0, time.UTC),
		}}},
		download: discussionsession.AttachmentDownloadDTO{
			Attachment:  discussionsession.AttachmentDTO{AttachmentID: "att-1", ConversationID: "conv-1", FileName: "brief.txt", ContentType: "text/plain", SizeBytes: 12},
			Reader:      io.NopCloser(strings.NewReader("hello world!")),
			FileName:    "brief.txt",
			ContentType: "text/plain",
			SizeBytes:   12,
		},
	}
	handler := newTestRouter(RouterOptions{Discussions: service})
	listReq := authedRequest(http.MethodGet, "/api/discussions/conv-1/attachments", "")
	listReq.Header.Set("X-User-ID", "operator")
	listRec := httptest.NewRecorder()

	handler.ServeHTTP(listRec, listReq)

	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", listRec.Code, listRec.Body.String())
	}
	if !service.listAttachmentsCalled || service.getConversationID != "conv-1" {
		t.Fatalf("list attachment called=%v conversation=%q", service.listAttachmentsCalled, service.getConversationID)
	}
	assertDiscussionBodyUsesNeutralIDs(t, listRec.Body.String())
	if strings.Contains(listRec.Body.String(), "storage_path") || !strings.Contains(listRec.Body.String(), `"preview_url"`) {
		t.Fatalf("attachment response must expose preview URL without storage path: %s", listRec.Body.String())
	}

	downloadReq := authedRequest(http.MethodGet, "/api/discussions/conv-1/attachments/att-1/download", "")
	downloadReq.Header.Set("X-User-ID", "operator")
	downloadRec := httptest.NewRecorder()

	handler.ServeHTTP(downloadRec, downloadReq)

	if downloadRec.Code != http.StatusOK || downloadRec.Body.String() != "hello world!" {
		t.Fatalf("download status=%d body=%q", downloadRec.Code, downloadRec.Body.String())
	}
	if !service.downloadAttachmentCalled || service.downloadAttachmentID != "att-1" || service.getConversationID != "conv-1" {
		t.Fatalf("download called=%v conversation=%q attachment=%q", service.downloadAttachmentCalled, service.getConversationID, service.downloadAttachmentID)
	}
	if got := downloadRec.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment;") || !strings.Contains(got, "brief.txt") {
		t.Fatalf("content disposition = %q", got)
	}

	service.download = discussionsession.AttachmentDownloadDTO{
		Attachment:  discussionsession.AttachmentDTO{AttachmentID: "att-voice", ConversationID: "conv-1", FileName: "voice.webm", ContentType: "audio/webm", SizeBytes: 5},
		Reader:      io.NopCloser(strings.NewReader("voice")),
		FileName:    "voice.webm",
		ContentType: "audio/webm",
		SizeBytes:   5,
	}
	voiceReq := authedRequest(http.MethodGet, "/api/discussions/conv-1/attachments/att-voice/download", "")
	voiceReq.Header.Set("X-User-ID", "operator")
	voiceRec := httptest.NewRecorder()

	handler.ServeHTTP(voiceRec, voiceReq)

	if voiceRec.Code != http.StatusOK || voiceRec.Body.String() != "voice" {
		t.Fatalf("voice download status=%d body=%q", voiceRec.Code, voiceRec.Body.String())
	}
	if got := voiceRec.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "inline;") || !strings.Contains(got, "voice.webm") {
		t.Fatalf("voice content disposition = %q", got)
	}

	service.download = discussionsession.AttachmentDownloadDTO{
		Attachment:  discussionsession.AttachmentDTO{AttachmentID: "att-game", ConversationID: "conv-1", FileName: "game.html", ContentType: "text/html", SizeBytes: 86},
		Reader:      io.NopCloser(strings.NewReader(`<html><head></head><body><script>localStorage.setItem("score","1")</script></body></html>`)),
		FileName:    "game.html",
		ContentType: "text/html",
	}
	previewReq := authedRequest(http.MethodGet, "/api/discussions/conv-1/attachments/att-game/preview", "")
	previewReq.Header.Set("X-User-ID", "operator")
	previewRec := httptest.NewRecorder()

	handler.ServeHTTP(previewRec, previewReq)

	if previewRec.Code != http.StatusOK || !strings.Contains(previewRec.Body.String(), "__orreryPreviewLocalStorage.setItem") || strings.Contains(previewRec.Body.String(), ">localStorage.setItem") {
		t.Fatalf("preview status=%d body=%q", previewRec.Code, previewRec.Body.String())
	}
	if got := previewRec.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "inline;") || !strings.Contains(got, "game.html") {
		t.Fatalf("preview content disposition = %q", got)
	}
	if csp := previewRec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox allow-scripts") || !strings.Contains(csp, "connect-src 'none'") {
		t.Fatalf("preview CSP = %q", csp)
	}
	if previewRec.Header().Get("X-Content-Type-Options") != "nosniff" || previewRec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("preview security headers = %+v", previewRec.Header())
	}
}

func TestDiscussionRoutesDeleteConversationThroughService(t *testing.T) {
	service := &fakeDiscussionService{}
	handler := newTestRouter(RouterOptions{Discussions: service})
	req := authedRequest(http.MethodDelete, "/api/discussions/conv-delete", ``)
	req.Header.Set("X-User-ID", "operator")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !service.deleteCalled || service.deleteConversationID != "conv-delete" || service.viewer.ViewerID != "operator" {
		t.Fatalf("delete called=%v id=%q viewer=%+v", service.deleteCalled, service.deleteConversationID, service.viewer)
	}
}

func TestDiscussionRoutesAbortConversationThroughService(t *testing.T) {
	service := &fakeDiscussionService{conversation: discussionsession.ConversationDTO{ConversationID: "conv-abort", Status: "cancelled"}}
	handler := newTestRouter(RouterOptions{Discussions: service})
	req := authedRequest(http.MethodPost, "/api/discussions/conv-abort/abort", `{}`)
	req.Header.Set("X-User-ID", "operator")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !service.abortCalled || service.abortConversationID != "conv-abort" || service.viewer.ViewerID != "operator" {
		t.Fatalf("abort called=%v id=%q viewer=%+v", service.abortCalled, service.abortConversationID, service.viewer)
	}
	assertDiscussionBodyUsesNeutralIDs(t, rec.Body.String())
}

func TestDiscussionRoutesInterjectsIntoActiveAgentRun(t *testing.T) {
	service := &fakeDiscussionService{message: discussionsession.MessageDTO{
		MessageID: "msg-interject", ConversationID: "conv-active", TurnID: "turn-1", RunID: "run-1",
		Role: "user", AuthorID: "operator", Content: "@Lead change direction", CreatedAt: time.Now(),
	}}
	handler := newTestRouter(RouterOptions{Discussions: service})
	req := authedRequest(http.MethodPost, "/api/discussions/conv-active/interjections", `{"content":"@Lead change direction","expected_turn_id":"turn-1","expected_run_id":"run-1","mentions":[{"target_kind":"agent","target_id":"lead-agent"}]}`)
	req.Header.Set("X-User-ID", "operator")
	req.Header.Set("Idempotency-Key", "interject-1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !service.interjectCalled || service.interjectCommand.ConversationID != "conv-active" || service.interjectCommand.ExpectedTurnID != "turn-1" || service.interjectCommand.ExpectedRunID != "run-1" || service.interjectCommand.IdempotencyKey != "interject-1" || len(service.interjectCommand.Mentions) != 1 {
		t.Fatalf("interject called=%v command=%+v", service.interjectCalled, service.interjectCommand)
	}
	assertDiscussionBodyUsesNeutralIDs(t, rec.Body.String())
}

func TestDiscussionRoutesMapRuntimeUnavailable(t *testing.T) {
	service := &fakeDiscussionService{err: discussionsession.NewRuntimeSetupError("pi", "pi executable not found at \"pi\"")}
	handler := newTestRouter(RouterOptions{Discussions: service})
	req := authedRequest(http.MethodPost, "/api/discussions/messages", `{"content":"Plan launch"}`)
	req.Header.Set("Idempotency-Key", "send-1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if body.Code != ErrorRuntimeUnavailable || !body.Retryable {
		t.Fatalf("body = %+v", body)
	}
	if body.Details["setup_action"] != "configure_discussion_runtime" ||
		body.Details["settings_path"] != "/settings" ||
		!strings.Contains(body.Details["guidance"], "Configure a discussion runtime") ||
		!strings.Contains(body.Details["cause"], "pi executable not found") {
		t.Fatalf("runtime setup details = %+v", body.Details)
	}
}

func TestDiscussionRoutesRejectCanonicalDiscussionStreamRoute(t *testing.T) {
	service := &fakeDiscussionService{}
	handler := newTestRouter(RouterOptions{Discussions: service})
	req := authedRequest(http.MethodGet, "/api/discussions/conv-1/stream", ``)
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if service.getCalled || service.getPageCalled || service.streamCalled {
		t.Fatalf("removed discussion stream route called service: get=%v getPage=%v stream=%v", service.getCalled, service.getPageCalled, service.streamCalled)
	}
}

func TestDiscussionRoutesSendMessageStreamsSSEEvents(t *testing.T) {
	events := make(chan discussionsession.StreamEventDTO, 16)
	events <- discussionsession.StreamEventDTO{Kind: discussionsession.StreamEventSnapshot, Conversation: &discussionsession.ConversationDTO{
		ConversationID: "conv-1",
		Status:         "running",
		Messages: []discussionsession.MessageDTO{{
			MessageID: "msg-1", ConversationID: "conv-1", Role: "user", AuthorID: "operator", Content: "Plan launch",
		}},
	}}
	events <- discussionsession.StreamEventDTO{Kind: discussionsession.StreamEventRunStarted, Run: &discussionsession.RunDTO{RunID: "run-1", ConversationID: "conv-1", Status: "running"}}
	events <- discussionsession.StreamEventDTO{Kind: discussionsession.StreamEventThinkingDelta, RunID: "run-1", Sequence: 1, Delta: "Checking the request."}
	events <- discussionsession.StreamEventDTO{Kind: discussionsession.StreamEventTokenDelta, RunID: "run-1", Sequence: 2, Delta: "First."}
	events <- discussionsession.StreamEventDTO{Kind: discussionsession.StreamEventToolCall, ToolCall: &discussionsession.ToolCallDTO{ToolCallID: "tool-1", Name: "mission_brief_create", Status: "running"}}
	events <- discussionsession.StreamEventDTO{Kind: discussionsession.StreamEventToolResult, ToolCall: &discussionsession.ToolCallDTO{ToolCallID: "tool-1", Name: "mission_brief_create", Status: "completed"}}
	events <- discussionsession.StreamEventDTO{Kind: discussionsession.StreamEventApprovalRequested, RunID: "run-1", Approval: &discussionsession.EventDTO{
		EventID: "approval-1", ConversationID: "conv-1", Kind: "discussion_runtime_approval_requested", Status: "pending",
		Metadata: map[string]string{"approval_request_id": "call-1", "approval_tool_name": "bash"}, CreatedAt: time.Date(2026, 8, 18, 5, 0, 0, 0, time.UTC),
	}}
	events <- discussionsession.StreamEventDTO{Kind: discussionsession.StreamEventPlan, RunID: "run-1", Plan: &agentcore.RuntimePlanState{
		PlanID: "plan-1",
		Status: "active",
		Items:  []agentcore.RuntimePlanItem{{ID: "step-1", Text: "Inspect runtime bridge", Status: "in_progress"}},
	}}
	events <- discussionsession.StreamEventDTO{Kind: discussionsession.StreamEventMessageCompleted, Message: &discussionsession.MessageDTO{MessageID: "msg-2", ConversationID: "conv-1", Role: "assistant", Content: "What deadline?"}}
	events <- discussionsession.StreamEventDTO{Kind: discussionsession.StreamEventRunCompleted, Run: &discussionsession.RunDTO{RunID: "run-1", ConversationID: "conv-1", Status: "completed"}}
	close(events)
	service := &fakeDiscussionService{streamEvents: events}
	handler := newTestRouter(RouterOptions{Discussions: service})
	req := authedRequest(http.MethodPost, "/api/discussions/messages", `{"content":"Plan launch","force_new_conversation":true}`)
	req.Header.Set("Idempotency-Key", "send-1")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-User-ID", "operator")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Agent-Profile-ID", "human-operator")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "text/event-stream; charset=utf-8" {
		t.Fatalf("content type = %q", rec.Header().Get("Content-Type"))
	}
	body := rec.Body.String()
	for _, want := range []string{"event: snapshot", "event: run_started", "event: thinking_delta", "event: token_delta", "event: tool_call", "event: tool_result", "event: approval_requested", "event: plan", "event: message_completed", "event: run_completed"} {
		if !strings.Contains(body, want) {
			t.Fatalf("sse body missing %q: %s", want, body)
		}
	}
	if !strings.Contains(body, `"kind":"thinking_delta"`) || !strings.Contains(body, `"kind":"token_delta"`) || !strings.Contains(body, `"run_id":"run-1"`) || !strings.Contains(body, `"sequence":2`) {
		t.Fatalf("SSE body missing structured token identity: %s", body)
	}
	if !strings.Contains(body, `"kind":"plan"`) || !strings.Contains(body, `"plan_id":"plan-1"`) || !strings.Contains(body, `"status":"in_progress"`) {
		t.Fatalf("SSE body missing structured plan payload: %s", body)
	}
	if !strings.Contains(body, `"kind":"approval_requested"`) || !strings.Contains(body, `"approval":{"event_id":"approval-1"`) || !strings.Contains(body, `"approval_request_id":"call-1"`) {
		t.Fatalf("SSE body missing structured approval payload: %s", body)
	}
	if !strings.Contains(body, `"message_id":"msg-1"`) || !strings.Contains(body, `"is_current_viewer":true`) {
		t.Fatalf("SSE snapshot missing current viewer ownership: %s", body)
	}
	if !service.streamCalled || service.command.Content != "Plan launch" || !service.command.ForceNewConversation || service.command.IdempotencyKey != "send-1" {
		t.Fatalf("stream command = %+v called=%v", service.command, service.streamCalled)
	}
	assertDiscussionBodyUsesNeutralIDs(t, body)
}

func TestDiscussionRoutesRetryMessageThroughService(t *testing.T) {
	service := &fakeDiscussionService{conversation: discussionsession.ConversationDTO{
		ConversationID: "conv-1",
		Status:         "retry_pending",
	}}
	handler := newTestRouter(RouterOptions{Discussions: service})
	req := authedRequest(http.MethodPost, "/api/discussions/conv-1/messages/msg-1/retry", `{}`)
	req.Header.Set("Idempotency-Key", "retry-1")
	req.Header.Set("X-User-ID", "user-1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !service.retryCalled || service.retryCommand.ConversationID != "conv-1" || service.retryCommand.MessageID != "msg-1" || service.retryCommand.IdempotencyKey != "retry-1" || service.retryCommand.Approve {
		t.Fatalf("retry command = %+v called=%v", service.retryCommand, service.retryCalled)
	}
	assertDiscussionBodyUsesNeutralIDs(t, rec.Body.String())
}

func TestDiscussionRoutesRetryApprovalStreamsSSEEvents(t *testing.T) {
	events := make(chan discussionsession.StreamEventDTO, 2)
	events <- discussionsession.StreamEventDTO{Kind: discussionsession.StreamEventSnapshot, Conversation: &discussionsession.ConversationDTO{ConversationID: "conv-1", Status: "running"}}
	events <- discussionsession.StreamEventDTO{Kind: discussionsession.StreamEventRunCompleted, Run: &discussionsession.RunDTO{RunID: "run-retry", ConversationID: "conv-1", Status: "completed"}}
	close(events)
	service := &fakeDiscussionService{retryEvents: events}
	handler := newTestRouter(RouterOptions{Discussions: service})
	req := authedRequest(http.MethodPost, "/api/discussions/conv-1/messages/msg-1/retry", `{"approve":true}`)
	req.Header.Set("Idempotency-Key", "retry-approve-1")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-User-ID", "owner-1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !service.retryStreamCalled || !service.retryCommand.Approve || service.retryCommand.MessageID != "msg-1" {
		t.Fatalf("retry stream command = %+v called=%v", service.retryCommand, service.retryStreamCalled)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: snapshot") || !strings.Contains(body, "event: run_completed") {
		t.Fatalf("sse body = %s", body)
	}
	assertDiscussionBodyUsesNeutralIDs(t, body)
}

func TestDiscussionRoutesGetLatestRunDebug(t *testing.T) {
	service := &fakeDiscussionService{debug: discussionsession.RunDebugDTO{
		ConversationID:  "conv-1",
		RunID:           "run-1",
		OutboundPrompt:  "sent prompt",
		RuntimeResponse: "raw response",
		ProviderMetadata: map[string]string{
			"pi_debug_runtime_input_path": "D:/Orrery/pi-debug/run-1/runtime-input.json",
		},
		Selected: discussionsession.RunDebugSelectedDTO{AgentID: "agent-1", RuntimeKind: "codex", Model: "gpt-5-codex"},
		Redacted: false,
	}}
	handler := newTestRouter(RouterOptions{Discussions: service})
	req := authedRequest(http.MethodGet, "/api/discussions/conv-1/debug/latest", ``)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		DiscussionID     string            `json:"discussion_id"`
		RunID            string            `json:"run_id"`
		ProviderMetadata map[string]string `json:"provider_metadata"`
		Selected         struct {
			RuntimeKind string `json:"runtime_kind"`
		} `json:"selected"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !service.debugCalled || service.getConversationID != "conv-1" || body.DiscussionID != "conv-1" || body.RunID != "run-1" || body.Selected.RuntimeKind != "codex" {
		t.Fatalf("debug called=%v id=%q body=%+v", service.debugCalled, service.getConversationID, body)
	}
	if body.ProviderMetadata["pi_debug_runtime_input_path"] != "D:/Orrery/pi-debug/run-1/runtime-input.json" {
		t.Fatalf("debug provider metadata = %+v", body.ProviderMetadata)
	}
	if strings.Contains(rec.Body.String(), "system_prompt") || strings.Contains(rec.Body.String(), "runtime_prompt") || strings.Contains(rec.Body.String(), "tool_contract") {
		t.Fatalf("debug response should only expose raw outbound prompt and runtime response: %s", rec.Body.String())
	}
	assertDiscussionBodyUsesNeutralIDs(t, rec.Body.String())
}

func TestDiscussionRoutesGetRunDebug(t *testing.T) {
	service := &fakeDiscussionService{debug: discussionsession.RunDebugDTO{
		ConversationID:  "conv-1",
		RunID:           "run-earlier",
		OutboundPrompt:  "sent prompt",
		RuntimeResponse: "raw response",
		Selected:        discussionsession.RunDebugSelectedDTO{AgentID: "agent-1", RuntimeKind: "codex", Model: "gpt-5-codex"},
		Redacted:        false,
	}}
	handler := newTestRouter(RouterOptions{Discussions: service})
	req := authedRequest(http.MethodGet, "/api/discussions/conv-1/runs/run-earlier/debug", ``)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		DiscussionID string `json:"discussion_id"`
		RunID        string `json:"run_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !service.runDebugCalled || service.getConversationID != "conv-1" || service.getRunID != "run-earlier" || body.DiscussionID != "conv-1" || body.RunID != "run-earlier" {
		t.Fatalf("run debug called=%v conversation=%q run=%q body=%+v", service.runDebugCalled, service.getConversationID, service.getRunID, body)
	}
	assertDiscussionBodyUsesNeutralIDs(t, rec.Body.String())
}

func assertDiscussionBodyUsesNeutralIDs(t *testing.T, body string) {
	t.Helper()
	if !strings.Contains(body, "discussion_id") {
		t.Fatalf("discussion response missing discussion_id: %s", body)
	}
	if strings.Contains(body, "conversation_id") || strings.Contains(body, `"conversation":`) || strings.Contains(body, `"conversations":`) {
		t.Fatalf("discussion response leaked legacy conversation fields: %s", body)
	}
}

type fakeDiscussionService struct {
	listCalled                 bool
	approvalListCalled         bool
	called                     bool
	deleteCalled               bool
	abortCalled                bool
	interjectCalled            bool
	streamCalled               bool
	approvalDecisionCalled     bool
	retryCalled                bool
	retryStreamCalled          bool
	getCalled                  bool
	getPageCalled              bool
	listMessagesCalled         bool
	debugCalled                bool
	runDebugCalled             bool
	addMembersCalled           bool
	setWorkspaceCalled         bool
	setHILModeCalled           bool
	listAttachmentsCalled      bool
	uploadAttachmentCalled     bool
	startAttachmentDraftCalled bool
	prewarmCalled              bool
	downloadAttachmentCalled   bool
	viewer                     discussionsession.ViewerRef
	listQuery                  discussionsession.ListConversationsQuery
	messageQuery               discussionsession.ListMessagesQuery
	command                    discussionsession.SendMessageCommand
	approvalDecision           discussionsession.RuntimeApprovalDecisionCommand
	retryCommand               discussionsession.RetryMessageCommand
	interjectCommand           discussionsession.InterjectMessageCommand
	addMembersCommand          discussionsession.AddConversationMembersCommand
	setWorkspaceCommand        discussionsession.SetConversationWorkspaceCommand
	setHILModeCommand          discussionsession.SetConversationHILModeCommand
	uploadCommand              discussionsession.UploadAttachmentCommand
	prewarmCommand             discussionsession.PrewarmConversationCommand
	getConversationID          string
	getRunID                   string
	downloadAttachmentID       string
	deleteConversationID       string
	abortConversationID        string
	page                       discussionsession.ConversationPageDTO
	approvalConversations      []discussionsession.ConversationDTO
	conversation               discussionsession.ConversationDTO
	messagePage                discussionsession.MessagePageDTO
	message                    discussionsession.MessageDTO
	attachment                 discussionsession.AttachmentDTO
	attachmentDraft            discussionsession.AttachmentDraftDTO
	attachmentPage             discussionsession.AttachmentPageDTO
	download                   discussionsession.AttachmentDownloadDTO
	debug                      discussionsession.RunDebugDTO
	streamEvents               <-chan discussionsession.StreamEventDTO
	retryEvents                <-chan discussionsession.StreamEventDTO
	err                        error
}

func (f *fakeDiscussionService) ListConversations(_ context.Context, viewer discussionsession.ViewerRef, query discussionsession.ListConversationsQuery) (discussionsession.ConversationPageDTO, error) {
	f.listCalled = true
	f.viewer = viewer
	f.listQuery = query
	return f.page, nil
}

func (f *fakeDiscussionService) ListApprovalConversations(_ context.Context, viewer discussionsession.ViewerRef) ([]discussionsession.ConversationDTO, error) {
	f.approvalListCalled = true
	f.viewer = viewer
	if f.err != nil {
		return nil, f.err
	}
	if f.approvalConversations != nil {
		return f.approvalConversations, nil
	}
	if f.conversation.ConversationID != "" {
		return []discussionsession.ConversationDTO{f.conversation}, nil
	}
	return nil, nil
}

func (f *fakeDiscussionService) GetConversation(_ context.Context, _ discussionsession.ViewerRef, conversationID string) (discussionsession.ConversationDTO, error) {
	f.getCalled = true
	f.getConversationID = conversationID
	return f.conversation, nil
}

func (f *fakeDiscussionService) PrewarmConversation(_ context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.PrewarmConversationCommand) (discussionsession.ConversationDTO, error) {
	f.prewarmCalled = true
	f.viewer = viewer
	f.prewarmCommand = cmd
	if f.err != nil {
		return discussionsession.ConversationDTO{}, f.err
	}
	return f.conversation, nil
}

func (f *fakeDiscussionService) GetConversationPage(_ context.Context, viewer discussionsession.ViewerRef, conversationID string, query discussionsession.ListMessagesQuery) (discussionsession.ConversationDTO, error) {
	f.getPageCalled = true
	f.viewer = viewer
	f.getConversationID = conversationID
	f.messageQuery = query
	if f.err != nil {
		return discussionsession.ConversationDTO{}, f.err
	}
	return f.conversation, nil
}

func (f *fakeDiscussionService) ListMessages(_ context.Context, viewer discussionsession.ViewerRef, conversationID string, query discussionsession.ListMessagesQuery) (discussionsession.MessagePageDTO, error) {
	f.listMessagesCalled = true
	f.viewer = viewer
	f.getConversationID = conversationID
	f.messageQuery = query
	if f.err != nil {
		return discussionsession.MessagePageDTO{}, f.err
	}
	return f.messagePage, nil
}

func (f *fakeDiscussionService) SendMessage(_ context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.SendMessageCommand) (discussionsession.ConversationDTO, error) {
	f.called = true
	f.viewer = viewer
	f.command = cmd
	if f.err != nil {
		return discussionsession.ConversationDTO{}, f.err
	}
	return f.conversation, nil
}

func (f *fakeDiscussionService) SendMessageStream(_ context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.SendMessageCommand) (<-chan discussionsession.StreamEventDTO, error) {
	f.streamCalled = true
	f.viewer = viewer
	f.command = cmd
	if f.err != nil {
		return nil, f.err
	}
	return f.streamEvents, nil
}

func (f *fakeDiscussionService) InterjectMessage(_ context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.InterjectMessageCommand) (discussionsession.MessageDTO, error) {
	f.interjectCalled = true
	f.viewer = viewer
	f.interjectCommand = cmd
	if f.err != nil {
		return discussionsession.MessageDTO{}, f.err
	}
	return f.message, nil
}

func (f *fakeDiscussionService) DecideRuntimeApproval(_ context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.RuntimeApprovalDecisionCommand) (discussionsession.ConversationDTO, error) {
	f.approvalDecisionCalled = true
	f.viewer = viewer
	f.approvalDecision = cmd
	if f.err != nil {
		return discussionsession.ConversationDTO{}, f.err
	}
	return f.conversation, nil
}

func (f *fakeDiscussionService) RetryMessage(_ context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.RetryMessageCommand) (discussionsession.ConversationDTO, error) {
	f.retryCalled = true
	f.viewer = viewer
	f.retryCommand = cmd
	if f.err != nil {
		return discussionsession.ConversationDTO{}, f.err
	}
	return f.conversation, nil
}

func (f *fakeDiscussionService) RetryMessageStream(_ context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.RetryMessageCommand) (<-chan discussionsession.StreamEventDTO, error) {
	f.retryStreamCalled = true
	f.viewer = viewer
	f.retryCommand = cmd
	if f.err != nil {
		return nil, f.err
	}
	return f.retryEvents, nil
}

func (f *fakeDiscussionService) GetLatestRunDebug(_ context.Context, _ discussionsession.ViewerRef, conversationID string) (discussionsession.RunDebugDTO, error) {
	f.debugCalled = true
	f.getConversationID = conversationID
	if f.err != nil {
		return discussionsession.RunDebugDTO{}, f.err
	}
	return f.debug, nil
}

func (f *fakeDiscussionService) GetRunDebug(_ context.Context, _ discussionsession.ViewerRef, conversationID string, runID string) (discussionsession.RunDebugDTO, error) {
	f.runDebugCalled = true
	f.getConversationID = conversationID
	f.getRunID = runID
	if f.err != nil {
		return discussionsession.RunDebugDTO{}, f.err
	}
	return f.debug, nil
}

func (f *fakeDiscussionService) DeleteConversation(_ context.Context, viewer discussionsession.ViewerRef, conversationID string) error {
	f.deleteCalled = true
	f.viewer = viewer
	f.deleteConversationID = conversationID
	return f.err
}

func (f *fakeDiscussionService) AbortConversation(_ context.Context, viewer discussionsession.ViewerRef, conversationID string) (discussionsession.ConversationDTO, error) {
	f.abortCalled = true
	f.viewer = viewer
	f.abortConversationID = conversationID
	if f.err != nil {
		return discussionsession.ConversationDTO{}, f.err
	}
	return f.conversation, nil
}

func (f *fakeDiscussionService) AddConversationMembers(_ context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.AddConversationMembersCommand) (discussionsession.ConversationDTO, error) {
	f.addMembersCalled = true
	f.viewer = viewer
	f.addMembersCommand = cmd
	if f.err != nil {
		return discussionsession.ConversationDTO{}, f.err
	}
	return f.conversation, nil
}

func (f *fakeDiscussionService) SetConversationWorkspace(_ context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.SetConversationWorkspaceCommand) (discussionsession.ConversationDTO, error) {
	f.setWorkspaceCalled = true
	f.viewer = viewer
	f.setWorkspaceCommand = cmd
	if f.err != nil {
		return discussionsession.ConversationDTO{}, f.err
	}
	return f.conversation, nil
}

func (f *fakeDiscussionService) SetConversationHILMode(_ context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.SetConversationHILModeCommand) (discussionsession.ConversationHILModeDTO, error) {
	f.setHILModeCalled = true
	f.viewer = viewer
	f.setHILModeCommand = cmd
	if f.err != nil {
		return discussionsession.ConversationHILModeDTO{}, f.err
	}
	return discussionsession.ConversationHILModeDTO{
		ConversationID: f.conversation.ConversationID,
		Revision:       f.conversation.Revision,
		HILMode:        f.conversation.HILMode,
		UpdatedAt:      f.conversation.UpdatedAt,
	}, nil
}

func (f *fakeDiscussionService) ListAttachments(_ context.Context, viewer discussionsession.ViewerRef, conversationID string) (discussionsession.AttachmentPageDTO, error) {
	f.listAttachmentsCalled = true
	f.viewer = viewer
	f.getConversationID = conversationID
	if f.err != nil {
		return discussionsession.AttachmentPageDTO{}, f.err
	}
	return f.attachmentPage, nil
}

func (f *fakeDiscussionService) UploadAttachment(_ context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.UploadAttachmentCommand) (discussionsession.AttachmentDTO, error) {
	f.uploadAttachmentCalled = true
	f.viewer = viewer
	f.uploadCommand = cmd
	if f.err != nil {
		return discussionsession.AttachmentDTO{}, f.err
	}
	return f.attachment, nil
}

func (f *fakeDiscussionService) StartAttachmentDraft(_ context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.UploadAttachmentCommand) (discussionsession.AttachmentDraftDTO, error) {
	f.startAttachmentDraftCalled = true
	f.viewer = viewer
	f.uploadCommand = cmd
	if f.err != nil {
		return discussionsession.AttachmentDraftDTO{}, f.err
	}
	return f.attachmentDraft, nil
}

func (f *fakeDiscussionService) DownloadAttachment(_ context.Context, viewer discussionsession.ViewerRef, conversationID string, attachmentID string) (discussionsession.AttachmentDownloadDTO, error) {
	f.downloadAttachmentCalled = true
	f.viewer = viewer
	f.getConversationID = conversationID
	f.downloadAttachmentID = attachmentID
	if f.err != nil {
		return discussionsession.AttachmentDownloadDTO{}, f.err
	}
	return f.download, nil
}
