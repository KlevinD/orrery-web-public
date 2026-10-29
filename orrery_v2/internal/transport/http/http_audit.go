package httptransport

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/operations"
)

const (
	httpAuditEntityType = "http_request"
	httpAuditActorAnon  = "anonymous"
)

type httpAuditActorContextKey struct{}

type httpAuditActor struct {
	ActorID         string
	ViewerID        string
	DisplayName     string
	ViewerKind      string
	SubjectProvider string
	Subject         string
}

func (r *Router) shouldAuditHTTPRequest(req *http.Request) bool {
	if r == nil || r.auditRecorder == nil || !r.httpAuditEnabled || req == nil || req.URL == nil {
		return false
	}
	path := strings.TrimSpace(req.URL.Path)
	if path == "" || path == "/api/health" {
		return false
	}
	return strings.HasPrefix(path, "/api/") || path == "/mcp" || strings.HasPrefix(path, "/mcp/")
}

func (r *Router) serveHTTPWithAudit(w http.ResponseWriter, req *http.Request) {
	startedAt := time.Now().UTC()
	recorder := &loggingResponseWriter{ResponseWriter: w}
	r.serveHTTP(recorder, req)
	status := recorder.status
	if status == 0 {
		status = http.StatusOK
	}
	r.recordHTTPRequestAudit(req, status, recorder.bytes, startedAt)
}

func (r *Router) recordHTTPRequestAudit(req *http.Request, status int, bytesWritten int, startedAt time.Time) {
	if r == nil || r.auditRecorder == nil || req == nil || req.URL == nil {
		return
	}
	actor := auditActorFromRequest(req)
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = "UNKNOWN"
	}
	path := auditCleanValue(req.URL.Path, 512)
	routePath := path
	metadata := map[string]string{
		"method":      method,
		"path":        path,
		"status":      strconv.Itoa(status),
		"bytes":       strconv.Itoa(bytesWritten),
		"duration_ms": strconv.FormatInt(time.Since(startedAt).Milliseconds(), 10),
		"source_ip":   auditCleanValue(clientIPFromRequest(req), 128),
		"remote_addr": auditCleanValue(req.RemoteAddr, 256),
		"user_agent":  auditCleanValue(req.UserAgent(), 512),
	}
	if query := auditSafeRawQuery(req.URL.Query()); query != "" {
		metadata["query"] = auditCleanValue(query, 1024)
	}
	if forwardedFor := strings.TrimSpace(req.Header.Get("X-Forwarded-For")); forwardedFor != "" {
		metadata["forwarded_for_header"] = auditCleanValue(forwardedFor, 512)
	}
	if realIP := strings.TrimSpace(req.Header.Get("X-Real-IP")); realIP != "" {
		metadata["real_ip_header"] = auditCleanValue(realIP, 128)
	}
	if actor.ViewerKind != "" {
		metadata["viewer_kind"] = actor.ViewerKind
	}
	if actor.DisplayName != "" {
		metadata["display_name"] = actor.DisplayName
	}
	if actor.SubjectProvider != "" {
		metadata["subject_provider"] = actor.SubjectProvider
	}
	if actor.Subject != "" {
		metadata["subject"] = actor.Subject
	}
	if capability, ok := r.permissionForRequest(req); ok {
		routePath = auditCleanValue(capability.Path, 512)
		metadata["route_path"] = routePath
		metadata["capability_id"] = auditCleanValue(capability.CapabilityID, 256)
		metadata["capability_scope"] = auditCleanValue(capability.Scope, 256)
		metadata["capability_domain"] = auditCleanValue(capability.Domain, 128)
		metadata["capability_action"] = auditCleanValue(capability.Action, 128)
	}
	if requestID := correlationID(req); requestID != "" {
		metadata["request_id"] = auditCleanValue(requestID, 256)
	}
	entityID := method + " " + routePath
	event := operations.AuditEventDTO{
		ActorID:       actor.ActorID,
		ViewerID:      actor.ViewerID,
		EntityType:    httpAuditEntityType,
		EntityID:      entityID,
		Action:        "http." + strings.ToLower(method) + " " + routePath,
		Result:        httpAuditResult(status),
		Severity:      httpAuditSeverity(status),
		CorrelationID: correlationID(req),
		Summary:       method + " " + path + " -> " + strconv.Itoa(status),
		Metadata:      metadata,
		OccurredAt:    startedAt,
	}
	if err := r.auditRecorder.RecordAuditEvent(context.WithoutCancel(req.Context()), event); err != nil && r.logger != nil {
		r.logger.Error(req.Context(), "http audit record failed", map[string]any{
			"operation":      "http.audit.record",
			"correlation_id": correlationID(req),
			"method":         method,
			"path":           path,
			"status":         status,
			"error":          err.Error(),
		})
	}
}

func setRequestAuditActor(req *http.Request, actor httpAuditActor) {
	if req == nil {
		return
	}
	*req = *req.WithContext(context.WithValue(req.Context(), httpAuditActorContextKey{}, normalizeHTTPAuditActor(actor)))
}

func auditActorFromRequest(req *http.Request) httpAuditActor {
	if req == nil {
		return normalizeHTTPAuditActor(httpAuditActor{})
	}
	if actor, ok := req.Context().Value(httpAuditActorContextKey{}).(httpAuditActor); ok {
		return normalizeHTTPAuditActor(actor)
	}
	if principal, ok := identity.PrincipalFromContext(req.Context()); ok && principal.Authenticated() {
		return auditActorFromPrincipal(principal)
	}
	if internalViewerAuthorizationMatches(req) {
		return auditActorFromTrustedViewerHeaders(req)
	}
	return normalizeHTTPAuditActor(httpAuditActor{})
}

func auditActorFromTrustedViewerHeaders(req *http.Request) httpAuditActor {
	if req == nil {
		return normalizeHTTPAuditActor(httpAuditActor{})
	}
	actor := httpAuditActor{
		ActorID:    req.Header.Get("X-User-ID"),
		ViewerID:   req.Header.Get("X-User-ID"),
		ViewerKind: req.Header.Get("X-Viewer-Kind"),
	}
	if displayName := req.Header.Get("X-User-Display-Name"); displayName != "" {
		if decoded, err := url.QueryUnescape(displayName); err == nil {
			actor.DisplayName = decoded
		} else {
			actor.DisplayName = displayName
		}
	}
	return normalizeHTTPAuditActor(actor)
}

func auditActorFromPrincipal(principal identity.Principal) httpAuditActor {
	principal = identity.NormalizePrincipal(principal)
	return normalizeHTTPAuditActor(httpAuditActor{
		ActorID:         firstNonEmpty(principal.Username, principal.UserID, principal.Subject),
		ViewerID:        firstNonEmpty(principal.UserID, principal.Username, principal.Subject),
		DisplayName:     principal.DisplayName,
		ViewerKind:      principal.ViewerKind,
		SubjectProvider: principal.SubjectProvider,
		Subject:         principal.Subject,
	})
}

func normalizeHTTPAuditActor(actor httpAuditActor) httpAuditActor {
	actor.ActorID = auditCleanValue(actor.ActorID, 256)
	actor.ViewerID = auditCleanValue(actor.ViewerID, 256)
	actor.DisplayName = auditCleanValue(actor.DisplayName, 256)
	actor.ViewerKind = auditCleanValue(actor.ViewerKind, 64)
	actor.SubjectProvider = auditCleanValue(actor.SubjectProvider, 128)
	actor.Subject = auditCleanValue(actor.Subject, 256)
	if actor.ActorID == "" {
		actor.ActorID = httpAuditActorAnon
	}
	if actor.ViewerID == "" && actor.ActorID != httpAuditActorAnon {
		actor.ViewerID = actor.ActorID
	}
	return actor
}

func httpAuditResult(status int) string {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusTooManyRequests:
		return "denied"
	case status >= http.StatusInternalServerError:
		return "failed"
	case status >= http.StatusBadRequest:
		return "failed"
	default:
		return "succeeded"
	}
}

func httpAuditSeverity(status int) string {
	switch {
	case status >= http.StatusInternalServerError:
		return "error"
	case status >= http.StatusBadRequest:
		return "warning"
	default:
		return "info"
	}
}

func auditSafeRawQuery(values url.Values) string {
	if len(values) == 0 {
		return ""
	}
	safe := make(url.Values, len(values))
	for key, rawValues := range values {
		if auditSensitiveQueryKey(key) {
			safe[key] = []string{"[redacted]"}
			continue
		}
		copied := make([]string, len(rawValues))
		copy(copied, rawValues)
		safe[key] = copied
	}
	return safe.Encode()
}

func auditSensitiveQueryKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return strings.Contains(key, "token") ||
		strings.Contains(key, "password") ||
		strings.Contains(key, "secret") ||
		key == "code" ||
		key == "state" ||
		key == "challenge"
}

func auditCleanValue(value string, maxRunes int) string {
	value = strings.TrimSpace(strings.ToValidUTF8(value, ""))
	if value == "" {
		return ""
	}
	if maxRunes <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes])
}
