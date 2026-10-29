package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/centrifugal/centrifuge"

	"orrery_v2/internal/app/developmentproject"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/platform/realtime"
)

type RealtimeConnectionInfo struct {
	ViewerID               string   `json:"viewer_id"`
	ViewerKind             string   `json:"viewer_kind"`
	AuthorizationSubjectID string   `json:"authorization_subject_id"`
	TenantID               string   `json:"tenant_id"`
	PermissionScope        []string `json:"permission_scope,omitempty"`
}

func (r *Router) handleRealtimeConnect(w http.ResponseWriter, req *http.Request) {
	if r == nil || r.realtime == nil {
		status, body := MapError(errServiceDependency(), correlationID(req))
		writeJSON(w, status, body)
		return
	}
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	info, err := json.Marshal(RealtimeConnectionInfo{
		ViewerID:               viewer.ViewerID,
		ViewerKind:             string(viewer.ViewerKind),
		AuthorizationSubjectID: viewer.AuthorizationSubjectID,
		TenantID:               viewer.Tenant(),
		PermissionScope:        append([]string(nil), viewer.PermissionScope...),
	})
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	ctx := centrifuge.SetCredentials(req.Context(), &centrifuge.Credentials{
		UserID: viewer.ViewerID,
		Info:   info,
	})
	r.realtime.ServeHTTP(w, req.WithContext(ctx))
}

type RealtimeDiscussionAccess interface {
	GetConversation(ctx context.Context, viewer discussionsession.ViewerRef, conversationID string) (discussionsession.ConversationDTO, error)
}

type RealtimeDevelopmentProjectAccess interface {
	Get(context.Context, developmentproject.ViewerRef, string) (developmentproject.ProjectDTO, error)
}

type RealtimeSubscriptionAuthorizer struct {
	Discussions         RealtimeDiscussionAccess
	DevelopmentProjects RealtimeDevelopmentProjectAccess
}

func (a RealtimeSubscriptionAuthorizer) AuthorizeSubscribe(ctx context.Context, request realtime.SubscriptionRequest) error {
	info, err := realtimeConnectionInfo(request.Info)
	if err != nil {
		return realtime.ErrSubscriptionUnauthorized
	}
	if request.UserID == "" || info.ViewerID == "" || request.UserID != info.ViewerID {
		return realtime.ErrSubscriptionUnauthorized
	}
	prefix, id, ok := splitRealtimeChannel(request.Channel)
	if !ok {
		return realtime.ErrSubscriptionUnauthorized
	}
	switch prefix {
	case "conversation":
		if a.Discussions == nil {
			return realtime.ErrSubscriptionUnauthorized
		}
		discussionViewerID := strings.TrimSpace(info.ViewerID)
		agentProfileID := ""
		if info.ViewerKind == string(discussionsession.ViewerKindUser) || info.ViewerKind == string(discussionsession.ViewerKindHuman) {
			agentProfileID = strings.TrimSpace(info.AuthorizationSubjectID)
			discussionViewerID = agentProfileID
		}
		if discussionViewerID == "" {
			return realtime.ErrSubscriptionUnauthorized
		}
		viewer := discussionsession.ViewerRef{
			ViewerID:       discussionViewerID,
			AgentProfileID: agentProfileID,
			ViewerKind:     discussionsession.ViewerKind(info.ViewerKind),
			TenantID:       info.TenantID,
		}
		if _, err := a.Discussions.GetConversation(ctx, viewer, id); err != nil {
			return realtime.ErrSubscriptionUnauthorized
		}
		return nil
	case "user":
		if id != info.ViewerID && id != strings.TrimSpace(info.AuthorizationSubjectID) {
			return realtime.ErrSubscriptionUnauthorized
		}
		return nil
	case "project":
		if a.DevelopmentProjects == nil {
			return realtime.ErrSubscriptionUnauthorized
		}
		channelTenantID, projectID, err := realtime.ParseProjectChannel(request.Channel)
		if err != nil || channelTenantID != strings.TrimSpace(info.TenantID) {
			return realtime.ErrSubscriptionUnauthorized
		}
		viewer := developmentproject.ViewerRef{
			ViewerID: info.ViewerID, ViewerKind: info.ViewerKind, TenantID: info.TenantID,
			AuthorizationSubjectID: info.AuthorizationSubjectID, PermissionScope: append([]string(nil), info.PermissionScope...),
		}
		if _, err := a.DevelopmentProjects.Get(ctx, viewer, projectID); err != nil {
			return realtime.ErrSubscriptionUnauthorized
		}
		return nil
	default:
		return realtime.ErrSubscriptionUnauthorized
	}
}

func realtimeConnectionInfo(data []byte) (RealtimeConnectionInfo, error) {
	var info RealtimeConnectionInfo
	if len(data) == 0 {
		return info, errors.New("realtime credentials info is empty")
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return info, err
	}
	if strings.TrimSpace(info.ViewerID) == "" || strings.TrimSpace(info.ViewerKind) == "" {
		return info, errors.New("realtime credentials info is incomplete")
	}
	return info, nil
}

func splitRealtimeChannel(channel string) (string, string, bool) {
	prefix, id, ok := strings.Cut(strings.TrimSpace(channel), ":")
	if !ok || strings.TrimSpace(prefix) == "" || strings.TrimSpace(id) == "" || strings.Contains(id, ":") {
		return "", "", false
	}
	return prefix, id, true
}
