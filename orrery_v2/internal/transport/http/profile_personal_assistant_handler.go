package httptransport

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/operations"
	devicedirectory "orrery_v2/internal/device/directory"
)

const maxPersonalAssistantRequestBytes = 16 << 10

type personalAssistantAvatarDTO struct {
	AvatarURL   string `json:"avatar_url"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	UpdatedAt   string `json:"updated_at"`
	ETag        string `json:"etag"`
}

func (r *Router) handleGetPersonalAssistant(w http.ResponseWriter, req *http.Request) {
	if r.personalAssistant == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "personal assistant configuration is unavailable", CorrelationID: correlationID(req), Retryable: true})
		return
	}
	scope, _, ok := personalAssistantScope(w, req)
	if !ok {
		return
	}
	view, err := r.personalAssistant.GetPersonalAssistant(req.Context(), scope)
	if err != nil {
		writePersonalAssistantError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, personalAssistantViewDTO(view))
}

func (r *Router) handlePostPersonalAssistant(w http.ResponseWriter, req *http.Request) {
	if r.personalAssistant == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "personal assistant configuration is unavailable", CorrelationID: correlationID(req), Retryable: true})
		return
	}
	scope, principal, ok := personalAssistantScope(w, req)
	if !ok {
		return
	}
	current, err := r.personalAssistant.GetPersonalAssistant(req.Context(), scope)
	if err != nil {
		writePersonalAssistantError(w, req, err)
		return
	}
	if strings.TrimSpace(current.AgentProfileID) != "" {
		writeJSON(w, http.StatusConflict, ErrorResponse{Code: ErrorVersionConflict, Message: "personal assistant already exists", CorrelationID: correlationID(req)})
		return
	}
	view, err := r.personalAssistant.UpdatePersonalAssistant(req.Context(), scope, devicedirectory.UpdatePersonalAssistantInput{
		AssistantEnabled: true,
		HostingMode:      devicedirectory.HostingModeManaged,
		Revision:         current.Revision,
	})
	if err != nil {
		writePersonalAssistantError(w, req, err)
		return
	}
	r.recordPersonalAssistantProfileAudit(req, scope, principal, "personal_assistant.created", map[string]string{
		"agent_profile_id": view.AgentProfileID,
		"hosting_mode":     view.HostingMode,
	})
	writeJSON(w, http.StatusCreated, personalAssistantViewDTO(view))
}

func (r *Router) handlePutPersonalAssistant(w http.ResponseWriter, req *http.Request) {
	if r.personalAssistant == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "personal assistant configuration is unavailable", CorrelationID: correlationID(req), Retryable: true})
		return
	}
	scope, principal, ok := personalAssistantScope(w, req)
	if !ok {
		return
	}
	current, err := r.personalAssistant.GetPersonalAssistant(req.Context(), scope)
	if err != nil {
		writePersonalAssistantError(w, req, err)
		return
	}
	if strings.TrimSpace(current.AgentProfileID) == "" {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: ErrorNotFound, Message: "personal assistant has not been created", CorrelationID: correlationID(req)})
		return
	}
	req.Body = http.MaxBytesReader(w, req.Body, maxPersonalAssistantRequestBytes)
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	var input devicedirectory.UpdatePersonalAssistantInput
	if err := decoder.Decode(&input); err != nil {
		writePersonalAssistantError(w, req, devicedirectory.ErrInvalidRequest)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writePersonalAssistantError(w, req, devicedirectory.ErrInvalidRequest)
		return
	}
	if assignedAgentID := strings.TrimSpace(input.AssignedAgentID); assignedAgentID != "" {
		if _, found, err := r.profileWeixinBotByID(req, scope.TenantID, assignedAgentID); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "organization Agent directory is unavailable", CorrelationID: correlationID(req), Retryable: true})
			return
		} else if !found {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: ErrorInvalidRequest, Message: "assigned organization Agent is not available in this tenant", CorrelationID: correlationID(req)})
			return
		}
	}
	view, err := r.personalAssistant.UpdatePersonalAssistant(req.Context(), scope, input)
	if err != nil {
		writePersonalAssistantError(w, req, err)
		return
	}
	if r.auditRecorder != nil {
		_ = r.auditRecorder.RecordAuditEvent(req.Context(), operations.AuditEventDTO{
			TenantID: scope.TenantID, ActorID: scope.OwnerID, ViewerID: scope.OwnerID,
			EntityType: "personal_assistant", EntityID: scope.OwnerID,
			Action: "personal_assistant.hosting.updated", Result: "succeeded", Severity: "info",
			CorrelationID: correlationID(req), OccurredAt: time.Now().UTC(),
			Metadata: map[string]string{
				"hosting_mode": view.HostingMode, "assigned_agent_id": view.AssignedAgentID, "owned_agent_id": view.OwnedAgentID,
				"workspace_grant_id": view.WorkspaceGrantID,
				"subject_provider":   principal.SubjectProvider,
			},
		})
	}
	writeJSON(w, http.StatusOK, personalAssistantViewDTO(view))
}

func (r *Router) handleGetPersonalAssistantNotifications(w http.ResponseWriter, req *http.Request) {
	if r.personalAssistant == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "personal assistant configuration is unavailable", CorrelationID: correlationID(req), Retryable: true})
		return
	}
	scope, _, ok := personalAssistantScope(w, req)
	if !ok {
		return
	}
	view, err := r.personalAssistant.GetPersonalAssistantNotifications(req.Context(), scope)
	if err != nil {
		writePersonalAssistantError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (r *Router) handlePutPersonalAssistantNotifications(w http.ResponseWriter, req *http.Request) {
	if r.personalAssistant == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "personal assistant configuration is unavailable", CorrelationID: correlationID(req), Retryable: true})
		return
	}
	scope, principal, ok := personalAssistantScope(w, req)
	if !ok {
		return
	}
	req.Body = http.MaxBytesReader(w, req.Body, maxPersonalAssistantRequestBytes)
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	var input devicedirectory.UpdatePersonalAssistantNotificationsInput
	if err := decoder.Decode(&input); err != nil {
		writePersonalAssistantError(w, req, devicedirectory.ErrInvalidRequest)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writePersonalAssistantError(w, req, devicedirectory.ErrInvalidRequest)
		return
	}
	view, err := r.personalAssistant.UpdatePersonalAssistantNotifications(req.Context(), scope, input)
	if err != nil {
		writePersonalAssistantError(w, req, err)
		return
	}
	r.recordPersonalAssistantProfileAudit(req, scope, principal, "personal_assistant.notifications.updated", map[string]string{
		"approval_notifications_enabled": strconv.FormatBool(view.ApprovalNotificationsEnabled),
		"channel":                        view.Channel,
	})
	writeJSON(w, http.StatusOK, view)
}

func (r *Router) handleGetPersonalAssistantAvatar(w http.ResponseWriter, req *http.Request) {
	if r.personalAssistant == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "personal assistant configuration is unavailable", CorrelationID: correlationID(req), Retryable: true})
		return
	}
	scope, _, ok := personalAssistantScope(w, req)
	if !ok {
		return
	}
	avatar, err := r.personalAssistant.GetPersonalAssistantAvatar(req.Context(), scope)
	if err != nil {
		writePersonalAssistantError(w, req, err)
		return
	}
	if match := strings.TrimSpace(req.Header.Get("If-None-Match")); match == `"`+avatar.Info.ETag+`"` {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", avatar.Info.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(int64(len(avatar.Content)), 10))
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("ETag", `"`+avatar.Info.ETag+`"`)
	if !avatar.Info.UpdatedAt.IsZero() {
		w.Header().Set("Last-Modified", avatar.Info.UpdatedAt.UTC().Format(http.TimeFormat))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(avatar.Content)
}

func (r *Router) handlePutPersonalAssistantAvatar(w http.ResponseWriter, req *http.Request) {
	if r.personalAssistant == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "personal assistant configuration is unavailable", CorrelationID: correlationID(req), Retryable: true})
		return
	}
	scope, principal, ok := personalAssistantScope(w, req)
	if !ok {
		return
	}
	req.Body = http.MaxBytesReader(w, req.Body, maxWorkforceAvatarBytes+(512<<10))
	if err := req.ParseMultipartForm(maxWorkforceAvatarBytes + (256 << 10)); err != nil {
		writeWorkforceAvatarUploadError(w, correlationID(req), err)
		return
	}
	if req.MultipartForm != nil {
		defer req.MultipartForm.RemoveAll()
	}
	file, _, err := req.FormFile("file")
	if err != nil {
		status, body := invalidRequest(correlationID(req), "file", "avatar file is required")
		writeJSON(w, status, body)
		return
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxWorkforceAvatarBytes+1))
	if err != nil {
		writeWorkforceAvatarUploadError(w, correlationID(req), err)
		return
	}
	if len(content) > maxWorkforceAvatarBytes {
		writeWorkforceAvatarTooLarge(w, correlationID(req))
		return
	}
	contentType, err := validateWorkforceAvatar(content)
	if err != nil {
		status, body := invalidRequest(correlationID(req), "file", err.Error())
		writeJSON(w, status, body)
		return
	}
	info, err := r.personalAssistant.SavePersonalAssistantAvatar(req.Context(), scope, contentType, content)
	if err != nil {
		writePersonalAssistantError(w, req, err)
		return
	}
	r.recordPersonalAssistantProfileAudit(req, scope, principal, "personal_assistant.avatar.updated", map[string]string{
		"content_type": info.ContentType,
		"size":         strconv.FormatInt(info.Size, 10),
		"etag":         info.ETag,
	})
	writeJSON(w, http.StatusOK, personalAssistantAvatarResponse(info))
}

func (r *Router) handleDeletePersonalAssistantAvatar(w http.ResponseWriter, req *http.Request) {
	if r.personalAssistant == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "personal assistant configuration is unavailable", CorrelationID: correlationID(req), Retryable: true})
		return
	}
	scope, principal, ok := personalAssistantScope(w, req)
	if !ok {
		return
	}
	if err := r.personalAssistant.DeletePersonalAssistantAvatar(req.Context(), scope); err != nil {
		writePersonalAssistantError(w, req, err)
		return
	}
	r.recordPersonalAssistantProfileAudit(req, scope, principal, "personal_assistant.avatar.deleted", nil)
	w.WriteHeader(http.StatusNoContent)
}

func personalAssistantViewDTO(view devicedirectory.PersonalAssistantView) devicedirectory.PersonalAssistantView {
	if strings.TrimSpace(view.AvatarETag) != "" {
		view.AvatarURL = "/api/profile/personal-assistant/avatar?v=" + view.AvatarETag
	}
	return view
}

func personalAssistantAvatarResponse(info devicedirectory.PersonalAssistantAvatarInfo) personalAssistantAvatarDTO {
	updatedAt := ""
	if !info.UpdatedAt.IsZero() {
		updatedAt = info.UpdatedAt.UTC().Format(timeRFC3339Nano)
	}
	return personalAssistantAvatarDTO{
		AvatarURL:   "/api/profile/personal-assistant/avatar?v=" + info.ETag,
		ContentType: info.ContentType, Size: info.Size, UpdatedAt: updatedAt, ETag: info.ETag,
	}
}

func (r *Router) recordPersonalAssistantProfileAudit(req *http.Request, scope devicedirectory.OwnerScope, principal identity.Principal, action string, metadata map[string]string) {
	if r.auditRecorder == nil {
		return
	}
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata["subject_provider"] = principal.SubjectProvider
	metadata["execution_identity"] = "user"
	_ = r.auditRecorder.RecordAuditEvent(req.Context(), operations.AuditEventDTO{
		TenantID: scope.TenantID, ActorID: scope.OwnerID, ViewerID: scope.OwnerID,
		EntityType: "personal_assistant", EntityID: scope.OwnerID,
		Action: action, Result: "succeeded", Severity: "info",
		CorrelationID: correlationID(req), OccurredAt: time.Now().UTC(), Metadata: metadata,
	})
}

func personalAssistantScope(w http.ResponseWriter, req *http.Request) (devicedirectory.OwnerScope, identity.Principal, bool) {
	principal, ok := identity.PrincipalFromContext(req.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Code: ErrorUnauthorized, Message: "authorization is required", CorrelationID: correlationID(req)})
		return devicedirectory.OwnerScope{}, identity.Principal{}, false
	}
	principal = identity.NormalizePrincipal(principal)
	if agentcore.IsReservedNonWorkforceProfileID(principal.AgentProfileID) ||
		agentcore.IsReservedNonWorkforceProfileID(principal.UserID) {
		writeJSON(w, http.StatusForbidden, ErrorResponse{Code: ErrorForbidden, Message: "system principals cannot own personal assistants", CorrelationID: correlationID(req)})
		return devicedirectory.OwnerScope{}, principal, false
	}
	ownerID := accountOwnerIDFromPrincipal(principal)
	if ownerID == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: ErrorInvalidRequest, Message: "personal assistant owner identity is missing", CorrelationID: correlationID(req)})
		return devicedirectory.OwnerScope{}, principal, false
	}
	return devicedirectory.OwnerScope{TenantID: strings.TrimSpace(principal.TenantID), OwnerID: ownerID}, principal, true
}

func writePersonalAssistantError(w http.ResponseWriter, req *http.Request, err error) {
	response := ErrorResponse{Code: ErrorInternal, Message: "internal server error", CorrelationID: correlationID(req)}
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, devicedirectory.ErrInvalidRequest):
		status, response.Code, response.Message = http.StatusBadRequest, ErrorInvalidRequest, "personal assistant configuration is invalid"
	case errors.Is(err, devicedirectory.ErrNotFound):
		status, response.Code, response.Message = http.StatusNotFound, ErrorNotFound, "personal assistant resource was not found"
	case errors.Is(err, devicedirectory.ErrConflict):
		status, response.Code, response.Message, response.Retryable = http.StatusConflict, ErrorVersionConflict, "personal assistant configuration changed; refresh and retry", true
	case errors.Is(err, devicedirectory.ErrDependency):
		status, response.Code, response.Message, response.Retryable = http.StatusServiceUnavailable, ErrorResourceUnavailable, "personal assistant configuration is unavailable", true
	}
	writeJSON(w, status, response)
}
