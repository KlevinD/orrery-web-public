package httptransport

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/notification"
)

func (r *Router) notificationViewer(w http.ResponseWriter, req *http.Request) (notification.Viewer, bool) {
	v, ok := discussionViewerFromRequest(w, req)
	if !ok {
		return notification.Viewer{}, false
	}
	out := notification.Viewer{TenantID: v.TenantID, HumanID: v.AgentProfileID, Manage: viewerHasScope(v.PermissionScope, "scheduler.write", "operations.manage"), System: identity.TrustedInternalServiceFromContext(req.Context()) && string(v.ViewerKind) == "system"}
	if out.TenantID == "" {
		writeNotificationError(w, req, notification.ErrForbidden)
		return out, false
	}
	return out, true
}
func writeNotificationError(w http.ResponseWriter, req *http.Request, err error) {
	code, message, status := "notification_unavailable", "通知服务暂不可用", 503
	switch {
	case errors.Is(err, notification.ErrNotFound):
		code, message, status = "not_found", "通知或任务不存在", 404
	case errors.Is(err, notification.ErrForbidden):
		code, message, status = "forbidden", "你没有访问这项通知的权限", 403
	case errors.Is(err, notification.ErrInvalid):
		code, message, status = "invalid_request", "请检查接收范围和通知配置", 400
	case errors.Is(err, notification.ErrConflict):
		code, message, status = "conflict", "配置已更新，请刷新后重试", 409
	}
	writeJSON(w, status, ErrorResponse{Code: code, Message: message, CorrelationID: correlationID(req)})
}
func decodeNotificationBody(w http.ResponseWriter, req *http.Request, out any) bool {
	req.Body = http.MaxBytesReader(w, req.Body, 64<<10)
	dec := json.NewDecoder(req.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		writeNotificationError(w, req, notification.ErrInvalid)
		return false
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		writeNotificationError(w, req, notification.ErrInvalid)
		return false
	}
	return true
}
func (r *Router) handleNotifications(w http.ResponseWriter, req *http.Request, parts []string) {
	w.Header().Set("Cache-Control", "private, no-store")
	if r.taskNotifications == nil {
		writeNotificationError(w, req, nil)
		return
	}
	v, ok := r.notificationViewer(w, req)
	if !ok {
		return
	}
	s := r.taskNotifications
	var result any
	var err error
	get := req.Method == http.MethodGet
	switch {
	case len(parts) == 3 && parts[2] == "channels" && get:
		result, err = s.Channels(req.Context(), v)
	case len(parts) == 2 && get:
		limit, offset := 25, 0
		if raw := req.URL.Query().Get("limit"); raw != "" {
			limit, err = strconv.Atoi(raw)
			if err != nil {
				writeNotificationError(w, req, notification.ErrInvalid)
				return
			}
		}
		if raw := req.URL.Query().Get("offset"); raw != "" {
			offset, err = strconv.Atoi(raw)
			if err != nil {
				writeNotificationError(w, req, notification.ErrInvalid)
				return
			}
		}
		result, err = s.Inbox(req.Context(), v, req.URL.Query().Get("unread") == "true", limit, offset)
	case len(parts) == 3 && parts[2] == "summary" && get:
		var p notification.Page
		p, err = s.Inbox(req.Context(), v, false, 1, 0)
		result = map[string]int{"unread": p.Unread, "total": p.Total}
	case len(parts) == 3 && parts[2] == "preferences" && get:
		result, err = s.Preferences(req.Context(), v)
	case len(parts) == 3 && parts[2] == "preferences" && req.Method == http.MethodPut:
		var p notification.Preferences
		if !decodeNotificationBody(w, req, &p) {
			return
		}
		result, err = s.SavePreferences(req.Context(), v, p)
	case len(parts) == 3 && parts[2] == "sources" && get:
		result, err = s.Sources(req.Context(), v)
	case len(parts) == 3 && parts[2] == "sources" && req.Method == http.MethodPost:
		var p notification.Source
		if !decodeNotificationBody(w, req, &p) {
			return
		}
		result, err = s.SaveSource(req.Context(), v, p)
	case len(parts) == 3 && parts[2] == "members" && get:
		result, err = s.Members(req.Context(), v)
	case len(parts) == 4 && parts[2] == "sources" && parts[3] == "candidates" && get:
		result, err = s.Candidates(req.Context(), v)
	case len(parts) == 4 && parts[2] == "sources" && get:
		result, err = s.Source(req.Context(), v, parts[3])
	case len(parts) == 4 && parts[2] == "sources" && req.Method == http.MethodPut:
		// Resolve the route identity first; caller fields cannot overwrite another rule.
		current, e := s.Source(req.Context(), v, parts[3])
		if e != nil {
			writeNotificationError(w, req, e)
			return
		}
		var p notification.Source
		if !decodeNotificationBody(w, req, &p) {
			return
		}
		if p.TaskID != current.TaskID || p.TenantID != current.TenantID || p.OriginTenant != current.OriginTenant {
			writeNotificationError(w, req, notification.ErrInvalid)
			return
		}
		result, err = s.SaveSource(req.Context(), v, p)
	case len(parts) == 5 && parts[2] == "sources" && parts[4] == "subscription" && req.Method == http.MethodPut:
		var sub notification.Subscription
		if !decodeNotificationBody(w, req, &sub) {
			return
		}
		result, err = s.Subscribe(req.Context(), v, parts[3], sub)
	case len(parts) == 5 && parts[2] == "sources" && parts[4] == "preview" && get:
		result, err = s.Preview(req.Context(), v, parts[3])
	case len(parts) == 5 && parts[2] == "sources" && parts[4] == "events" && get:
		result, err = s.Events(req.Context(), v, parts[3])
	case len(parts) == 4 && parts[2] == "items" && get:
		result, err = s.Item(req.Context(), v, parts[3])
	case len(parts) == 4 && parts[2] == "items" && req.Method == http.MethodPatch:
		var body struct {
			Read bool `json:"read"`
		}
		if !decodeNotificationBody(w, req, &body) {
			return
		}
		err = s.MarkRead(req.Context(), v, parts[3], body.Read)
		result = map[string]bool{"read": body.Read}
	case len(parts) == 6 && parts[2] == "items" && parts[4] == "artifacts" && get:
		c, e := s.OpenArtifact(req.Context(), v, parts[3], parts[5])
		if e != nil {
			writeNotificationError(w, req, e)
			return
		}
		defer c.Reader.Close()
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Type", "application/octet-stream")
		name := strings.ReplaceAll(strings.ReplaceAll(c.Name, "\r", ""), "\n", "")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
		if c.Size >= 0 {
			w.Header().Set("Content-Length", strconv.FormatInt(c.Size, 10))
		}
		_, _ = io.Copy(w, c.Reader)
		return
	case len(parts) == 5 && parts[2] == "events" && parts[4] == "deliveries" && get:
		result, err = s.Receipts(req.Context(), v, parts[3])
	default:
		writeNotificationError(w, req, notification.ErrNotFound)
		return
	}
	if err != nil {
		writeNotificationError(w, req, err)
		return
	}
	writeJSON(w, 200, result)
}
