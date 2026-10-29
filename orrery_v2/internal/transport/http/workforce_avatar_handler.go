package httptransport

import (
	"bytes"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/operations"
)

const (
	maxWorkforceAvatarBytes  = 5 << 20
	maxWorkforceAvatarPixels = 16_777_216
)

type WorkforceAvatarDTO struct {
	AgentProfileID string `json:"agent_profile_id"`
	AvatarURL      string `json:"avatar_url"`
	ContentType    string `json:"content_type"`
	Size           int64  `json:"size"`
	UpdatedAt      string `json:"updated_at"`
	ETag           string `json:"etag"`
}

type WorkforceAvatarDefaultDTO struct {
	ID          string `json:"id"`
	AvatarURL   string `json:"avatar_url"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	ETag        string `json:"etag"`
}

type workforceAvatarDefaultSelectionRequest struct {
	DefaultID string `json:"default_id"`
}

func (r *Router) handleListWorkforceAvatarDefaults(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	if _, ok := r.prepareOperationsQuery(w, req, cid); !ok {
		return
	}
	if r.workforceAvatars == nil {
		status, body := MapError(errServiceDependency(), cid)
		writeJSON(w, status, body)
		return
	}
	defaults, err := r.workforceAvatars.ListDefaults(req.Context())
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	result := make([]WorkforceAvatarDefaultDTO, 0, len(defaults))
	for _, item := range defaults {
		result = append(result, workforceAvatarDefaultDTO(item))
	}
	writeJSON(w, http.StatusOK, result)
}

func (r *Router) handleGetWorkforceAvatarDefault(w http.ResponseWriter, req *http.Request, defaultID string) {
	cid := correlationID(req)
	if _, ok := r.prepareOperationsQuery(w, req, cid); !ok {
		return
	}
	if r.workforceAvatars == nil {
		status, body := MapError(errServiceDependency(), cid)
		writeJSON(w, status, body)
		return
	}
	avatar, err := r.workforceAvatars.OpenDefault(req.Context(), defaultID)
	if err != nil {
		if errors.Is(err, errWorkforceAvatarNotFound) {
			status, body := MapError(operations.ErrNotFound, cid)
			writeJSON(w, status, body)
			return
		}
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	w.Header().Set("Content-Type", avatar.Info.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(int64(len(avatar.Bytes)), 10))
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("ETag", `"`+avatar.Info.ETag+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(avatar.Bytes)
}

func (r *Router) handleGetWorkforceAvatar(w http.ResponseWriter, req *http.Request, agentProfileID string) {
	cid := correlationID(req)
	if _, ok := r.prepareOperationsQuery(w, req, cid); !ok {
		return
	}
	if r.workforceAvatars == nil {
		status, body := MapError(errServiceDependency(), cid)
		writeJSON(w, status, body)
		return
	}
	if err := r.ensureWorkforceAvatarProfile(req, agentProfileID); err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	avatar, err := r.workforceAvatars.Open(req.Context(), agentProfileID)
	if err != nil {
		if errors.Is(err, errWorkforceAvatarNotFound) {
			w.Header().Set("Cache-Control", "private, max-age=300")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	w.Header().Set("Content-Type", avatar.Info.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(int64(len(avatar.Bytes)), 10))
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("ETag", `"`+avatar.Info.ETag+`"`)
	if !avatar.Info.UpdatedAt.IsZero() {
		w.Header().Set("Last-Modified", avatar.Info.UpdatedAt.UTC().Format(http.TimeFormat))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(avatar.Bytes)
}

func (r *Router) handlePutWorkforceAvatar(w http.ResponseWriter, req *http.Request, agentProfileID string) {
	cid := correlationID(req)
	if _, ok := r.prepareOperationsQuery(w, req, cid); !ok {
		return
	}
	if r.workforceAvatars == nil {
		status, body := MapError(errServiceDependency(), cid)
		writeJSON(w, status, body)
		return
	}
	if err := r.ensureWorkforceAvatarProfile(req, agentProfileID); err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	req.Body = http.MaxBytesReader(w, req.Body, maxWorkforceAvatarBytes+(512<<10))
	if err := req.ParseMultipartForm(maxWorkforceAvatarBytes + (256 << 10)); err != nil {
		writeWorkforceAvatarUploadError(w, cid, err)
		return
	}
	if req.MultipartForm != nil {
		defer req.MultipartForm.RemoveAll()
	}
	file, _, err := req.FormFile("file")
	if err != nil {
		status, body := invalidRequest(cid, "file", "avatar file is required")
		writeJSON(w, status, body)
		return
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxWorkforceAvatarBytes+1))
	if err != nil {
		writeWorkforceAvatarUploadError(w, cid, err)
		return
	}
	if len(content) > maxWorkforceAvatarBytes {
		writeWorkforceAvatarTooLarge(w, cid)
		return
	}
	contentType, err := validateWorkforceAvatar(content)
	if err != nil {
		status, body := invalidRequest(cid, "file", err.Error())
		writeJSON(w, status, body)
		return
	}
	info, err := r.workforceAvatars.Save(req.Context(), agentProfileID, contentType, content)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, workforceAvatarDTO(info))
}

func (r *Router) handlePutWorkforceAvatarDefault(w http.ResponseWriter, req *http.Request, agentProfileID string) {
	cid := correlationID(req)
	if _, ok := r.prepareOperationsQuery(w, req, cid); !ok {
		return
	}
	if r.workforceAvatars == nil {
		status, body := MapError(errServiceDependency(), cid)
		writeJSON(w, status, body)
		return
	}
	if err := r.ensureWorkforceAvatarProfile(req, agentProfileID); err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	var body workforceAvatarDefaultSelectionRequest
	if err := decodeStrictJSON(req, &body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	if strings.TrimSpace(body.DefaultID) == "" {
		status, response := invalidRequest(cid, "default_id", "default avatar is required")
		writeJSON(w, status, response)
		return
	}
	info, err := r.workforceAvatars.SetDefault(req.Context(), agentProfileID, body.DefaultID)
	if err != nil {
		if errors.Is(err, errWorkforceAvatarNotFound) {
			status, response := MapError(operations.ErrNotFound, cid)
			writeJSON(w, status, response)
			return
		}
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, workforceAvatarDTO(info))
}

func (r *Router) ensureWorkforceAvatarProfile(req *http.Request, agentProfileID string) error {
	if r.agentProfiles == nil {
		return nil
	}
	if strings.TrimSpace(agentProfileID) == "" {
		return operations.ErrInvalidRequest
	}
	profile, err := r.agentProfiles.GetProfile(req.Context(), agentcore.AgentProfileID(agentProfileID))
	if err != nil {
		if errors.Is(err, agentcore.ErrNotFound) {
			return operations.ErrNotFound
		}
		return err
	}
	if profile.IsPersonalAssistant() {
		return operations.ErrNotFound
	}
	return nil
}

func (r *Router) handleDeleteWorkforceAvatar(w http.ResponseWriter, req *http.Request, agentProfileID string) {
	cid := correlationID(req)
	if _, ok := r.prepareOperationsQuery(w, req, cid); !ok {
		return
	}
	if r.workforceAvatars == nil {
		status, body := MapError(errServiceDependency(), cid)
		writeJSON(w, status, body)
		return
	}
	if err := r.ensureWorkforceAvatarProfile(req, agentProfileID); err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if err := r.workforceAvatars.Delete(req.Context(), agentProfileID); err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validateWorkforceAvatar(content []byte) (string, error) {
	if len(content) == 0 {
		return "", errors.New("avatar file is empty")
	}
	contentType := strings.ToLower(strings.TrimSpace(http.DetectContentType(content)))
	switch contentType {
	case "image/jpeg", "image/png":
		config, _, err := image.DecodeConfig(bytes.NewReader(content))
		if err != nil || config.Width <= 0 || config.Height <= 0 {
			return "", errors.New("avatar image is invalid")
		}
		if config.Width > 4096 || config.Height > 4096 || int64(config.Width)*int64(config.Height) > maxWorkforceAvatarPixels {
			return "", errors.New("avatar dimensions must not exceed 4096 x 4096")
		}
	case "image/webp":
		if len(content) < 12 || string(content[:4]) != "RIFF" || string(content[8:12]) != "WEBP" {
			return "", errors.New("avatar image is invalid")
		}
	default:
		return "", errors.New("avatar must be a PNG, JPEG, or WebP image")
	}
	return contentType, nil
}

func writeWorkforceAvatarUploadError(w http.ResponseWriter, correlationID string, err error) {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) || strings.Contains(strings.ToLower(err.Error()), "request body too large") {
		writeWorkforceAvatarTooLarge(w, correlationID)
		return
	}
	status, body := invalidRequest(correlationID, "file", "avatar upload is invalid")
	writeJSON(w, status, body)
}

func writeWorkforceAvatarTooLarge(w http.ResponseWriter, correlationID string) {
	writeJSON(w, http.StatusRequestEntityTooLarge, ErrorResponse{
		Code:          ErrorInvalidRequest,
		Message:       "avatar must not exceed 5 MB",
		Details:       map[string]string{"field": "file"},
		CorrelationID: correlationID,
	})
}

func workforceAvatarDTO(info WorkforceAvatarInfo) WorkforceAvatarDTO {
	updatedAt := ""
	if !info.UpdatedAt.IsZero() {
		updatedAt = info.UpdatedAt.UTC().Format(timeRFC3339Nano)
	}
	return WorkforceAvatarDTO{
		AgentProfileID: info.AgentProfileID,
		AvatarURL:      "/api/workforce/members/" + info.AgentProfileID + "/avatar",
		ContentType:    info.ContentType,
		Size:           info.Size,
		UpdatedAt:      updatedAt,
		ETag:           info.ETag,
	}
}

func workforceAvatarDefaultDTO(info WorkforceAvatarDefaultInfo) WorkforceAvatarDefaultDTO {
	return WorkforceAvatarDefaultDTO{
		ID:          info.ID,
		AvatarURL:   "/api/workforce/avatar-defaults/" + url.PathEscape(info.ID),
		ContentType: info.ContentType,
		Size:        info.Size,
		ETag:        info.ETag,
	}
}

const timeRFC3339Nano = "2006-01-02T15:04:05.999999999Z07:00"
