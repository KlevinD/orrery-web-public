package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/identity"
	devicedirectory "orrery_v2/internal/device/directory"
)

func TestProfilePersonalAssistantReadsAndUpdatesCurrentOwner(t *testing.T) {
	service := &fakePersonalAssistantService{view: devicedirectory.PersonalAssistantView{
		AssistantEnabled: true, AgentProfileID: "A000016", HostingMode: devicedirectory.HostingModeManaged, Revision: 0,
		OwnedAgents: []devicedirectory.OwnedAgentView{{
			OwnedAgentID: "owned_agent_1", DisplayName: "My OpenClaw", RuntimeKind: devicedirectory.RuntimeKindOpenClaw, Status: devicedirectory.AgentStatusOnline,
			WorkspaceGrants: []devicedirectory.WorkspaceGrantDescriptor{{GrantID: "grant_openclaw_001", DisplayName: "Product workspace", Available: true}},
		}},
	}}
	handler := newTestRouter(RouterOptions{PersonalAssistant: service})

	req := authedRequest(http.MethodGet, "/api/profile/personal-assistant", "")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("get status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if service.getScope != (devicedirectory.OwnerScope{TenantID: "system", OwnerID: "agent-1"}) {
		t.Fatalf("get scope = %+v", service.getScope)
	}

	body := `{"assistant_enabled":true,"hosting_mode":"owned_agent","owned_agent_id":"owned_agent_1","workspace_grant_id":"grant_openclaw_001","fallback_to_managed":false,"revision":0}`
	req = authedRequest(http.MethodPut, "/api/profile/personal-assistant", body)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("put status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if service.updateScope != service.getScope || service.input.OwnedAgentID != "owned_agent_1" || service.input.WorkspaceGrantID != "grant_openclaw_001" || service.input.FallbackToManaged {
		t.Fatalf("update scope = %+v input = %+v", service.updateScope, service.input)
	}
	var response devicedirectory.PersonalAssistantView
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil || response.HostingMode != devicedirectory.HostingModeOwnedAgent {
		t.Fatalf("response = %+v error = %v", response, err)
	}
}

func TestProfilePersonalAssistantAssignsTenantOrganizationAgent(t *testing.T) {
	service := &fakePersonalAssistantService{view: devicedirectory.PersonalAssistantView{
		AssistantEnabled: true, AgentProfileID: "A000016", HostingMode: devicedirectory.HostingModeManaged,
	}}
	directory := &fakeProfileWeixinDirectory{entries: []agentcore.AgentDirectoryEntry{{
		AgentProfileID: "organization-agent",
		AgentKind:      agentcore.AgentKindAI,
		Enabled:        true,
		OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef: agentcore.OrganizationRef{ID: "system"},
		},
	}}}
	handler := newTestRouter(RouterOptions{PersonalAssistant: service, AgentProfiles: directory})

	req := authedRequest(http.MethodPut, "/api/profile/personal-assistant", `{"assistant_enabled":true,"assigned_agent_id":"organization-agent","hosting_mode":"orrery_managed","fallback_to_managed":false,"revision":0}`)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("put status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if service.input.AssignedAgentID != "organization-agent" || directory.lastQuery.Organization != agentcore.OrganizationID("system") {
		t.Fatalf("input = %+v directory query = %+v", service.input, directory.lastQuery)
	}
}

func TestProfilePersonalAssistantRequiresExplicitCurrentOwnerCreate(t *testing.T) {
	service := &fakePersonalAssistantService{
		view:            devicedirectory.PersonalAssistantView{AssistantEnabled: true, HostingMode: devicedirectory.HostingModeManaged},
		createProfileID: "A000016",
	}
	handler := newTestRouter(RouterOptions{PersonalAssistant: service})

	update := authedRequest(http.MethodPut, "/api/profile/personal-assistant", `{"assistant_enabled":true,"hosting_mode":"orrery_managed","fallback_to_managed":false,"revision":0}`)
	updateRecorder := httptest.NewRecorder()
	handler.ServeHTTP(updateRecorder, update)
	if updateRecorder.Code != http.StatusNotFound || service.updateCalls != 0 {
		t.Fatalf("implicit update status=%d calls=%d body=%s", updateRecorder.Code, service.updateCalls, updateRecorder.Body.String())
	}

	create := authedRequest(http.MethodPost, "/api/profile/personal-assistant", "")
	createRecorder := httptest.NewRecorder()
	handler.ServeHTTP(createRecorder, create)
	if createRecorder.Code != http.StatusCreated || service.updateCalls != 1 {
		t.Fatalf("explicit create status=%d calls=%d body=%s", createRecorder.Code, service.updateCalls, createRecorder.Body.String())
	}
	wantScope := devicedirectory.OwnerScope{TenantID: "system", OwnerID: "agent-1"}
	if service.updateScope != wantScope || service.input.HostingMode != devicedirectory.HostingModeManaged || !service.input.AssistantEnabled {
		t.Fatalf("create scope=%+v input=%+v", service.updateScope, service.input)
	}
	var response devicedirectory.PersonalAssistantView
	if err := json.NewDecoder(createRecorder.Body).Decode(&response); err != nil || response.AgentProfileID != "A000016" {
		t.Fatalf("create response=%+v error=%v", response, err)
	}

	duplicate := authedRequest(http.MethodPost, "/api/profile/personal-assistant", "")
	duplicateRecorder := httptest.NewRecorder()
	handler.ServeHTTP(duplicateRecorder, duplicate)
	if duplicateRecorder.Code != http.StatusConflict || service.updateCalls != 1 {
		t.Fatalf("duplicate create status=%d calls=%d body=%s", duplicateRecorder.Code, service.updateCalls, duplicateRecorder.Body.String())
	}
}

func TestProfilePersonalAssistantAvatarIsPrivateToCurrentOwner(t *testing.T) {
	service := &fakePersonalAssistantService{}
	handler := newTestRouter(RouterOptions{PersonalAssistant: service})
	content := testWorkforceAvatarPNG(t)
	upload := workforceAvatarUploadRequest(t, "/api/profile/personal-assistant/avatar", "assistant.png", content)
	uploadRecorder := httptest.NewRecorder()

	handler.ServeHTTP(uploadRecorder, upload)

	if uploadRecorder.Code != http.StatusOK {
		t.Fatalf("upload status = %d body = %s", uploadRecorder.Code, uploadRecorder.Body.String())
	}
	wantScope := devicedirectory.OwnerScope{TenantID: "system", OwnerID: "agent-1"}
	if service.updateScope != wantScope || !bytes.Equal(service.avatar.Content, content) {
		t.Fatalf("upload scope = %+v content_matches=%v", service.updateScope, bytes.Equal(service.avatar.Content, content))
	}
	var uploaded personalAssistantAvatarDTO
	if err := json.NewDecoder(uploadRecorder.Body).Decode(&uploaded); err != nil || uploaded.ETag != "avatar-etag" || uploaded.AvatarURL == "" {
		t.Fatalf("upload response = %+v error = %v", uploaded, err)
	}

	read := authedRequest(http.MethodGet, "/api/profile/personal-assistant/avatar", "")
	readRecorder := httptest.NewRecorder()
	handler.ServeHTTP(readRecorder, read)
	if readRecorder.Code != http.StatusOK || readRecorder.Header().Get("Content-Type") != "image/png" || !bytes.Equal(readRecorder.Body.Bytes(), content) {
		t.Fatalf("read status = %d type = %q content_matches=%v", readRecorder.Code, readRecorder.Header().Get("Content-Type"), bytes.Equal(readRecorder.Body.Bytes(), content))
	}
	if service.getScope != wantScope || readRecorder.Header().Get("Cache-Control") != "private, max-age=300" {
		t.Fatalf("read scope = %+v cache = %q", service.getScope, readRecorder.Header().Get("Cache-Control"))
	}

	notModified := authedRequest(http.MethodGet, "/api/profile/personal-assistant/avatar", "")
	notModified.Header.Set("If-None-Match", `"avatar-etag"`)
	notModifiedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(notModifiedRecorder, notModified)
	if notModifiedRecorder.Code != http.StatusNotModified {
		t.Fatalf("conditional read status = %d", notModifiedRecorder.Code)
	}

	remove := authedRequest(http.MethodDelete, "/api/profile/personal-assistant/avatar", "")
	removeRecorder := httptest.NewRecorder()
	handler.ServeHTTP(removeRecorder, remove)
	if removeRecorder.Code != http.StatusNoContent || service.updateScope != wantScope || len(service.avatar.Content) != 0 {
		t.Fatalf("delete status = %d scope = %+v avatar = %+v", removeRecorder.Code, service.updateScope, service.avatar.Info)
	}
}

func TestPersonalAssistantScopeUsesAccountOwnerInsteadOfHumanProfile(t *testing.T) {
	principal := identity.Principal{
		SubjectProvider: "local", Subject: "subject-1", UserID: "account-1", Username: "operator",
		AgentProfileID: "human-1", ViewerKind: identity.ViewerKindUser, TenantID: "tenant-a",
	}
	req := httptest.NewRequest(http.MethodGet, "/api/profile/personal-assistant", nil)
	req = req.WithContext(identity.ContextWithPrincipal(req.Context(), principal))
	recorder := httptest.NewRecorder()

	scope, _, ok := personalAssistantScope(recorder, req)

	if !ok || scope != (devicedirectory.OwnerScope{TenantID: "tenant-a", OwnerID: "account-1"}) {
		t.Fatalf("scope = %+v ok=%v", scope, ok)
	}
}

func TestPersonalAssistantScopeRejectsReservedSystemIdentities(t *testing.T) {
	for _, profileID := range []string{
		agentcore.SystemSuperadminPrincipalID,
		agentcore.LegacySuperadminAgentProfileID,
		agentcore.LegacyLocalAdminAgentProfileID,
		agentcore.LegacyLocalUserAgentProfileID,
	} {
		t.Run(profileID, func(t *testing.T) {
			principal := identity.Principal{
				SubjectProvider: "local", Subject: profileID, UserID: profileID, Username: "reserved",
				AgentProfileID: profileID, ViewerKind: identity.ViewerKindUser, TenantID: "system",
			}
			req := httptest.NewRequest(http.MethodGet, "/api/profile/personal-assistant", nil)
			req = req.WithContext(identity.ContextWithPrincipal(req.Context(), principal))
			recorder := httptest.NewRecorder()

			_, _, ok := personalAssistantScope(recorder, req)

			if ok || recorder.Code != http.StatusForbidden {
				t.Fatalf("profile %q: ok=%v status=%d body=%s", profileID, ok, recorder.Code, recorder.Body.String())
			}
		})
	}
}

type fakePersonalAssistantService struct {
	view            devicedirectory.PersonalAssistantView
	notifications   devicedirectory.PersonalAssistantNotificationPreferences
	avatar          devicedirectory.PersonalAssistantAvatar
	getScope        devicedirectory.OwnerScope
	updateScope     devicedirectory.OwnerScope
	input           devicedirectory.UpdatePersonalAssistantInput
	updateCalls     int
	createProfileID string
}

func TestProfilePersonalAssistantNotificationsAreCurrentOwnerOnly(t *testing.T) {
	service := &fakePersonalAssistantService{notifications: devicedirectory.PersonalAssistantNotificationPreferences{
		ApprovalNotificationsEnabled: true, Channel: "weixin", Revision: 2,
	}}
	handler := newTestRouter(RouterOptions{PersonalAssistant: service})

	read := authedRequest(http.MethodGet, "/api/profile/personal-assistant/notifications", "")
	readRecorder := httptest.NewRecorder()
	handler.ServeHTTP(readRecorder, read)
	if readRecorder.Code != http.StatusOK {
		t.Fatalf("get status = %d body = %s", readRecorder.Code, readRecorder.Body.String())
	}
	wantScope := devicedirectory.OwnerScope{TenantID: "system", OwnerID: "agent-1"}
	if service.getScope != wantScope {
		t.Fatalf("get scope = %+v, want %+v", service.getScope, wantScope)
	}

	update := authedRequest(http.MethodPut, "/api/profile/personal-assistant/notifications", `{"approval_notifications_enabled":false,"revision":2}`)
	updateRecorder := httptest.NewRecorder()
	handler.ServeHTTP(updateRecorder, update)
	if updateRecorder.Code != http.StatusOK {
		t.Fatalf("put status = %d body = %s", updateRecorder.Code, updateRecorder.Body.String())
	}
	if service.updateScope != wantScope || service.notifications.ApprovalNotificationsEnabled || service.notifications.Revision != 3 {
		t.Fatalf("update scope=%+v notifications=%+v", service.updateScope, service.notifications)
	}
}

func (service *fakePersonalAssistantService) GetPersonalAssistantNotifications(_ context.Context, scope devicedirectory.OwnerScope) (devicedirectory.PersonalAssistantNotificationPreferences, error) {
	service.getScope = scope
	if service.notifications.Channel == "" {
		service.notifications = devicedirectory.PersonalAssistantNotificationPreferences{ApprovalNotificationsEnabled: true, Channel: "weixin"}
	}
	return service.notifications, nil
}

func (service *fakePersonalAssistantService) UpdatePersonalAssistantNotifications(_ context.Context, scope devicedirectory.OwnerScope, input devicedirectory.UpdatePersonalAssistantNotificationsInput) (devicedirectory.PersonalAssistantNotificationPreferences, error) {
	service.updateScope = scope
	service.notifications = devicedirectory.PersonalAssistantNotificationPreferences{
		ApprovalNotificationsEnabled: input.ApprovalNotificationsEnabled, Channel: "weixin", Revision: input.Revision + 1,
	}
	return service.notifications, nil
}

func (service *fakePersonalAssistantService) GetPersonalAssistant(_ context.Context, scope devicedirectory.OwnerScope) (devicedirectory.PersonalAssistantView, error) {
	service.getScope = scope
	return service.view, nil
}

func (service *fakePersonalAssistantService) UpdatePersonalAssistant(_ context.Context, scope devicedirectory.OwnerScope, input devicedirectory.UpdatePersonalAssistantInput) (devicedirectory.PersonalAssistantView, error) {
	service.updateCalls++
	service.updateScope, service.input = scope, input
	service.view.AssistantEnabled = input.AssistantEnabled
	service.view.AssignedAgentID = input.AssignedAgentID
	service.view.HostingMode = input.HostingMode
	service.view.OwnedAgentID = input.OwnedAgentID
	service.view.WorkspaceGrantID = input.WorkspaceGrantID
	service.view.FallbackToManaged = input.FallbackToManaged
	if service.view.AgentProfileID == "" {
		service.view.AgentProfileID = service.createProfileID
	}
	service.view.Revision++
	return service.view, nil
}

func (service *fakePersonalAssistantService) GetPersonalAssistantAvatar(_ context.Context, scope devicedirectory.OwnerScope) (devicedirectory.PersonalAssistantAvatar, error) {
	service.getScope = scope
	if len(service.avatar.Content) == 0 {
		return devicedirectory.PersonalAssistantAvatar{}, devicedirectory.ErrNotFound
	}
	return service.avatar, nil
}

func (service *fakePersonalAssistantService) SavePersonalAssistantAvatar(_ context.Context, scope devicedirectory.OwnerScope, contentType string, content []byte) (devicedirectory.PersonalAssistantAvatarInfo, error) {
	service.updateScope = scope
	service.avatar = devicedirectory.PersonalAssistantAvatar{
		Info:    devicedirectory.PersonalAssistantAvatarInfo{TenantID: scope.TenantID, OwnerID: scope.OwnerID, ContentType: contentType, Size: int64(len(content)), ETag: "avatar-etag", UpdatedAt: time.Now().UTC()},
		Content: append([]byte(nil), content...),
	}
	return service.avatar.Info, nil
}

func (service *fakePersonalAssistantService) DeletePersonalAssistantAvatar(_ context.Context, scope devicedirectory.OwnerScope) error {
	service.updateScope = scope
	service.avatar = devicedirectory.PersonalAssistantAvatar{}
	return nil
}
