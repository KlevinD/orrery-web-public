package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/centrifugal/centrifuge"

	"orrery_v2/internal/app/developmentproject"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/platform/realtime"
)

func TestRealtimeRouteRequiresAuthenticatedViewer(t *testing.T) {
	handler := newTestRouter(RouterOptions{Realtime: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		t.Fatalf("realtime handler should not be called for unauthenticated request")
	})})
	req := httptest.NewRequest(http.MethodGet, "/api/realtime/connect", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %s, want unauthorized", rec.Code, rec.Body.String())
	}
}

func TestRealtimeRouteMountsCentrifugeHandler(t *testing.T) {
	var credentials *centrifuge.Credentials
	handler := newTestRouter(RouterOptions{Realtime: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		cred, ok := centrifuge.GetCredentials(req.Context())
		if !ok {
			t.Fatalf("request context does not include Centrifuge credentials")
		}
		credentials = cred
		w.WriteHeader(http.StatusNoContent)
	})})
	req := authedRequest(http.MethodGet, "/api/realtime/connect", "")
	req.Header.Set("X-Permission-Scope", "discussion.read,mission.read")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if credentials == nil || credentials.UserID != "agent-1" {
		t.Fatalf("credentials = %+v, want user id from authenticated viewer", credentials)
	}
	var info RealtimeConnectionInfo
	if err := json.Unmarshal(credentials.Info, &info); err != nil {
		t.Fatalf("decode credentials info: %v", err)
	}
	if info.ViewerID != "agent-1" || info.ViewerKind != "agent" || info.AuthorizationSubjectID != "agent-1" {
		t.Fatalf("credentials info = %+v", info)
	}
	if len(info.PermissionScope) != 2 || info.PermissionScope[0] != "discussion.read" || info.PermissionScope[1] != "mission.read" {
		t.Fatalf("permission scope = %#v", info.PermissionScope)
	}
}

func TestRealtimeRouteAcceptsQueryCredentialsForWebsocketHandshake(t *testing.T) {
	var credentials *centrifuge.Credentials
	handler := newTestRouter(RouterOptions{Realtime: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		cred, ok := centrifuge.GetCredentials(req.Context())
		if !ok {
			t.Fatalf("request context does not include Centrifuge credentials")
		}
		credentials = cred
		w.WriteHeader(http.StatusNoContent)
	})})
	req := httptest.NewRequest(http.MethodGet, "/api/realtime/connect?viewer_id=user-1&viewer_kind=user&permission_scope=discussion.read", nil)
	authorizeInternalViewerTestRequest(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if credentials == nil || credentials.UserID != "user-1" {
		t.Fatalf("credentials = %+v, want user id from query viewer", credentials)
	}
}

func TestRealtimeSubscriptionRejectsUnauthorizedConversation(t *testing.T) {
	service := &fakeRealtimeDiscussionAccess{err: discussionsession.ErrNotFound}
	authorizer := RealtimeSubscriptionAuthorizer{Discussions: service}
	err := authorizer.AuthorizeSubscribe(context.Background(), realtime.SubscriptionRequest{
		UserID:  "agent-1",
		Info:    mustRealtimeInfo(t, RealtimeConnectionInfo{ViewerID: "agent-1", ViewerKind: "agent", PermissionScope: []string{"discussion.read"}}),
		Channel: "conversation:conv-secret",
	})

	if !errors.Is(err, realtime.ErrSubscriptionUnauthorized) {
		t.Fatalf("AuthorizeSubscribe error = %v, want realtime unauthorized", err)
	}
	if service.conversationID != "conv-secret" {
		t.Fatalf("conversation id = %q", service.conversationID)
	}
}

func TestRealtimeSubscriptionAllowsAuthorizedConversation(t *testing.T) {
	service := &fakeRealtimeDiscussionAccess{conversation: discussionsession.ConversationDTO{ConversationID: "conv-1"}}
	authorizer := RealtimeSubscriptionAuthorizer{Discussions: service}
	err := authorizer.AuthorizeSubscribe(context.Background(), realtime.SubscriptionRequest{
		UserID:  "agent-1",
		Info:    mustRealtimeInfo(t, RealtimeConnectionInfo{ViewerID: "agent-1", ViewerKind: "agent", PermissionScope: []string{"discussion.read"}}),
		Channel: "conversation:conv-1",
	})

	if err != nil {
		t.Fatalf("AuthorizeSubscribe returned error: %v", err)
	}
	if service.viewer.ViewerID != "agent-1" || service.viewer.ViewerKind != discussionsession.ViewerKindAgent {
		t.Fatalf("viewer = %+v", service.viewer)
	}
}

func TestRealtimeSubscriptionUsesBoundHumanProfileForDiscussionAndUserChannels(t *testing.T) {
	service := &fakeRealtimeDiscussionAccess{conversation: discussionsession.ConversationDTO{ConversationID: "todo-conversation"}}
	authorizer := RealtimeSubscriptionAuthorizer{Discussions: service}
	info := RealtimeConnectionInfo{
		ViewerID: "operator", ViewerKind: "user", AuthorizationSubjectID: "human-operator", TenantID: "tenant-a",
		PermissionScope: []string{"discussion.read"},
	}
	if err := authorizer.AuthorizeSubscribe(context.Background(), realtime.SubscriptionRequest{
		UserID: "operator", Info: mustRealtimeInfo(t, info), Channel: "conversation:todo-conversation",
	}); err != nil {
		t.Fatalf("conversation subscription: %v", err)
	}
	if service.viewer.ViewerID != "human-operator" || service.viewer.AgentProfileID != "human-operator" || service.viewer.TenantID != "tenant-a" {
		t.Fatalf("discussion authorization viewer = %+v", service.viewer)
	}
	if err := authorizer.AuthorizeSubscribe(context.Background(), realtime.SubscriptionRequest{
		UserID: "operator", Info: mustRealtimeInfo(t, info), Channel: "user:human-operator",
	}); err != nil {
		t.Fatalf("bound human profile user channel: %v", err)
	}
	if err := authorizer.AuthorizeSubscribe(context.Background(), realtime.SubscriptionRequest{
		UserID: "operator", Info: mustRealtimeInfo(t, info), Channel: "user:human-someone-else",
	}); !errors.Is(err, realtime.ErrSubscriptionUnauthorized) {
		t.Fatalf("unrelated human profile user channel error = %v", err)
	}
}

func TestRealtimeSubscriptionAuthorizesDevelopmentProjectWithoutGrantingConversationAccess(t *testing.T) {
	channel, err := realtime.ProjectChannel("tenant-a", "project-1")
	if err != nil {
		t.Fatal(err)
	}
	projects := &fakeRealtimeDevelopmentProjectAccess{project: developmentproject.ProjectDTO{DevelopmentProjectID: "project-1", ExecutionRealtimeChannel: channel}}
	authorizer := RealtimeSubscriptionAuthorizer{DevelopmentProjects: projects}
	info := RealtimeConnectionInfo{
		ViewerID: "operator", ViewerKind: "user", AuthorizationSubjectID: "profile-operator", TenantID: "tenant-a",
		PermissionScope: []string{"development.read"},
	}
	if err := authorizer.AuthorizeSubscribe(context.Background(), realtime.SubscriptionRequest{
		UserID: "operator", Info: mustRealtimeInfo(t, info), Channel: channel,
	}); err != nil {
		t.Fatalf("AuthorizeSubscribe returned error: %v", err)
	}
	if projects.projectID != "project-1" || projects.viewer.AuthorizationSubjectID != "profile-operator" || projects.viewer.TenantID != "tenant-a" {
		t.Fatalf("project authorization request = viewer:%+v project:%q", projects.viewer, projects.projectID)
	}
	if err := authorizer.AuthorizeSubscribe(context.Background(), realtime.SubscriptionRequest{
		UserID: "operator", Info: mustRealtimeInfo(t, info), Channel: "conversation:todo-conversation",
	}); !errors.Is(err, realtime.ErrSubscriptionUnauthorized) {
		t.Fatalf("project access granted conversation subscription: %v", err)
	}

	projects.err = developmentproject.ErrNotFound
	secretChannel, err := realtime.ProjectChannel("tenant-a", "project-secret")
	if err != nil {
		t.Fatal(err)
	}
	if err := authorizer.AuthorizeSubscribe(context.Background(), realtime.SubscriptionRequest{
		UserID: "operator", Info: mustRealtimeInfo(t, info), Channel: secretChannel,
	}); !errors.Is(err, realtime.ErrSubscriptionUnauthorized) {
		t.Fatalf("unauthorized project error = %v", err)
	}
}

func TestRealtimeSubscriptionRejectsSameProjectIDFromAnotherTenant(t *testing.T) {
	tenantAChannel, err := realtime.ProjectChannel("tenant-a", "shared-project")
	if err != nil {
		t.Fatal(err)
	}
	tenantBChannel, err := realtime.ProjectChannel("tenant-b", "shared-project")
	if err != nil {
		t.Fatal(err)
	}
	projects := &fakeRealtimeDevelopmentProjectAccess{project: developmentproject.ProjectDTO{DevelopmentProjectID: "shared-project", ExecutionRealtimeChannel: tenantAChannel}}
	authorizer := RealtimeSubscriptionAuthorizer{DevelopmentProjects: projects}
	info := RealtimeConnectionInfo{ViewerID: "operator", ViewerKind: "user", TenantID: "tenant-a"}

	if err := authorizer.AuthorizeSubscribe(context.Background(), realtime.SubscriptionRequest{
		UserID: "operator", Info: mustRealtimeInfo(t, info), Channel: tenantBChannel,
	}); !errors.Is(err, realtime.ErrSubscriptionUnauthorized) {
		t.Fatalf("cross-tenant same-project subscription error = %v, want unauthorized", err)
	}
	if projects.projectID != "" {
		t.Fatalf("cross-tenant channel reached project authorization with id %q", projects.projectID)
	}
	if err := authorizer.AuthorizeSubscribe(context.Background(), realtime.SubscriptionRequest{
		UserID: "operator", Info: mustRealtimeInfo(t, info), Channel: tenantAChannel,
	}); err != nil {
		t.Fatalf("same-tenant project subscription: %v", err)
	}
}

func mustRealtimeInfo(t *testing.T, info RealtimeConnectionInfo) []byte {
	t.Helper()
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal realtime info: %v", err)
	}
	return data
}

type fakeRealtimeDiscussionAccess struct {
	viewer         discussionsession.ViewerRef
	conversationID string
	conversation   discussionsession.ConversationDTO
	err            error
}

type fakeRealtimeDevelopmentProjectAccess struct {
	viewer    developmentproject.ViewerRef
	projectID string
	project   developmentproject.ProjectDTO
	err       error
}

func (f *fakeRealtimeDevelopmentProjectAccess) Get(_ context.Context, viewer developmentproject.ViewerRef, projectID string) (developmentproject.ProjectDTO, error) {
	f.viewer = viewer
	f.projectID = projectID
	return f.project, f.err
}

func (f *fakeRealtimeDiscussionAccess) GetConversation(ctx context.Context, viewer discussionsession.ViewerRef, conversationID string) (discussionsession.ConversationDTO, error) {
	f.viewer = viewer
	f.conversationID = conversationID
	if f.err != nil {
		return discussionsession.ConversationDTO{}, f.err
	}
	return f.conversation, nil
}
