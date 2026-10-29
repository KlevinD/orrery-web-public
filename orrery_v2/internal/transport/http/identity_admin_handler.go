package httptransport

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/operations"
)

type identitySubjectPageDTO struct {
	Subjects []identitySubjectDTO `json:"subjects"`
	Page     identityPageDTO      `json:"page"`
}

type identityPageDTO struct {
	Limit    int  `json:"limit"`
	Offset   int  `json:"offset"`
	Total    int  `json:"total"`
	Returned int  `json:"returned"`
	HasMore  bool `json:"has_more"`
}

type identitySubjectDTO struct {
	ID             string `json:"id"`
	Provider       string `json:"provider"`
	Subject        string `json:"subject"`
	UserID         string `json:"user_id,omitempty"`
	Username       string `json:"username,omitempty"`
	Email          string `json:"email,omitempty"`
	DisplayName    string `json:"display_name,omitempty"`
	AgentProfileID string `json:"agent_profile_id"`
	Status         string `json:"status"`
	LastLoginAt    string `json:"last_login_at,omitempty"`
	CreatedAt      string `json:"created_at,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
}

type identityLinkSubjectRequest struct {
	Provider       string `json:"provider"`
	Subject        string `json:"subject"`
	AgentProfileID string `json:"agent_profile_id"`
	Username       string `json:"username,omitempty"`
	Email          string `json:"email,omitempty"`
	DisplayName    string `json:"display_name,omitempty"`
	Status         string `json:"status,omitempty"`
}

func (r *Router) handleListIdentitySubjects(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	if !r.authorizeIdentityAdmin(w, req, false) {
		return
	}
	if r == nil || r.identityAdmin == nil {
		status, body := MapError(operations.ErrServiceDependency, cid)
		writeJSON(w, status, body)
		return
	}
	limit, offset := listPage(req)
	page, err := r.identityAdmin.ListSubjects(req.Context(), identity.SubjectQuery{
		Provider:       req.URL.Query().Get("provider"),
		Subject:        req.URL.Query().Get("subject"),
		AgentProfileID: req.URL.Query().Get("agent_profile_id"),
		Status:         req.URL.Query().Get("status"),
		Limit:          limit,
		Offset:         offset,
	})
	if err != nil {
		status, body := MapError(identityAdminError(err), cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, identitySubjectPage(page))
}

func (r *Router) handleLinkIdentitySubject(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.identityAdminViewer(w, req, true)
	if !ok {
		return
	}
	if r == nil || r.identityAdmin == nil {
		status, body := MapError(operations.ErrServiceDependency, cid)
		writeJSON(w, status, body)
		return
	}
	var body identityLinkSubjectRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	subject, err := r.identityAdmin.LinkSubject(req.Context(), identity.LinkSubjectRequest{
		Provider:       body.Provider,
		Subject:        body.Subject,
		AgentProfileID: body.AgentProfileID,
		Username:       body.Username,
		Email:          body.Email,
		DisplayName:    body.DisplayName,
		Status:         body.Status,
	})
	if err != nil {
		status, response := MapError(identityAdminError(err), cid)
		writeJSON(w, status, response)
		return
	}
	r.recordIdentitySubjectAudit(req, viewer, subject, "identity.subject.link")
	writeJSON(w, http.StatusCreated, identitySubject(subject))
}

func (r *Router) handleDisableIdentitySubject(w http.ResponseWriter, req *http.Request, rawID string) {
	r.handleSetIdentitySubjectEnabled(w, req, rawID, false)
}

func (r *Router) handleEnableIdentitySubject(w http.ResponseWriter, req *http.Request, rawID string) {
	r.handleSetIdentitySubjectEnabled(w, req, rawID, true)
}

func (r *Router) handleSetIdentitySubjectEnabled(w http.ResponseWriter, req *http.Request, rawID string, enabled bool) {
	cid := correlationID(req)
	viewer, ok := r.identityAdminViewer(w, req, true)
	if !ok {
		return
	}
	if r == nil || r.identityAdmin == nil {
		status, body := MapError(operations.ErrServiceDependency, cid)
		writeJSON(w, status, body)
		return
	}
	id, err := url.PathUnescape(rawID)
	if err != nil || strings.TrimSpace(id) == "" {
		status, response := invalidRequest(cid, "subject_id", "subject id is invalid")
		writeJSON(w, status, response)
		return
	}
	var subject identity.IdentitySubject
	if enabled {
		subject, err = r.identityAdmin.EnableSubject(req.Context(), id)
	} else {
		subject, err = r.identityAdmin.DisableSubject(req.Context(), id)
	}
	if err != nil {
		status, response := MapError(identityAdminError(err), cid)
		writeJSON(w, status, response)
		return
	}
	action := "identity.subject.disable"
	if enabled {
		action = "identity.subject.enable"
	}
	r.recordIdentitySubjectAudit(req, viewer, subject, action)
	writeJSON(w, http.StatusAccepted, identitySubject(subject))
}

func (r *Router) authorizeIdentityAdmin(w http.ResponseWriter, req *http.Request, manage bool) bool {
	_, ok := r.identityAdminViewer(w, req, manage)
	return ok
}

func (r *Router) identityAdminViewer(w http.ResponseWriter, req *http.Request, manage bool) (operations.ViewerRef, bool) {
	cid := correlationID(req)
	rawViewer, err := ViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return operations.ViewerRef{}, false
	}
	viewer := operations.ViewerRef{
		ViewerID:        rawViewer.ViewerID,
		ViewerKind:      string(rawViewer.ViewerKind),
		DisplayName:     displayNameFromRequest(req),
		PermissionScope: append([]string(nil), rawViewer.PermissionScope...),
		RequestID:       cid,
	}
	required := operations.PermissionPermissionsRead
	scope := operations.PermissionPermissionsManage
	if manage {
		required = operations.PermissionPermissionsManage
	}
	if !viewerHasScope(viewer.PermissionScope, required, scope) {
		status, body := MapError(operations.ErrPermissionDenied, cid)
		writeJSON(w, status, body)
		return operations.ViewerRef{}, false
	}
	return viewer, true
}

func (r *Router) recordIdentitySubjectAudit(req *http.Request, viewer operations.ViewerRef, subject identity.IdentitySubject, action string) {
	if r == nil || r.auditRecorder == nil || req == nil {
		return
	}
	_ = r.auditRecorder.RecordAuditEvent(req.Context(), operations.AuditEventDTO{
		ActorID:       firstNonEmpty(viewer.ViewerID, "unknown"),
		ViewerID:      viewer.ViewerID,
		EntityType:    "identity_subject",
		EntityID:      subject.ID,
		Action:        action,
		Result:        "succeeded",
		Severity:      "info",
		CorrelationID: correlationID(req),
		Summary:       action + " " + subject.Provider + "/" + subject.Subject,
		Metadata: map[string]string{
			"provider":         subject.Provider,
			"subject":          subject.Subject,
			"agent_profile_id": subject.AgentProfileID,
			"status":           subject.Status,
		},
	})
}

func identityAdminError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, identity.ErrInvalidRequest):
		return operations.ErrInvalidRequest
	case errors.Is(err, identity.ErrSubjectNotFound):
		return operations.ErrNotFound
	default:
		return err
	}
}

func identitySubjectPage(page identity.SubjectPage) identitySubjectPageDTO {
	out := identitySubjectPageDTO{
		Subjects: make([]identitySubjectDTO, 0, len(page.Subjects)),
		Page: identityPageDTO{
			Limit:    page.Limit,
			Offset:   page.Offset,
			Total:    page.Total,
			Returned: page.Returned,
			HasMore:  page.HasMore,
		},
	}
	for _, subject := range page.Subjects {
		out.Subjects = append(out.Subjects, identitySubject(subject))
	}
	return out
}

func identitySubject(subject identity.IdentitySubject) identitySubjectDTO {
	subject = identity.NormalizeIdentitySubject(subject)
	return identitySubjectDTO{
		ID:             subject.ID,
		Provider:       subject.Provider,
		Subject:        subject.Subject,
		UserID:         subject.UserID,
		Username:       subject.Username,
		Email:          subject.Email,
		DisplayName:    subject.DisplayName,
		AgentProfileID: subject.AgentProfileID,
		Status:         subject.Status,
		LastLoginAt:    identityTimeString(subject.LastLoginAt),
		CreatedAt:      identityTimeString(subject.CreatedAt),
		UpdatedAt:      identityTimeString(subject.UpdatedAt),
	}
}

func identityTimeString(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}
