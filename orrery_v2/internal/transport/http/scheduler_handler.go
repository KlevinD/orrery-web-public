package httptransport

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/scheduler"
)

const schedulerMaxListPageSize = 100

func (r *Router) handleScheduledRuns(w http.ResponseWriter, req *http.Request, parts []string) {
	if r.scheduler == nil {
		writeSchedulerError(w, req, scheduler.ErrServiceDependency)
		return
	}
	if req.Method == http.MethodGet && len(parts) == 2 {
		r.handleListScheduledRunInbox(w, req)
		return
	}
	if req.Method == http.MethodPatch && len(parts) == 4 && parts[3] == "read" {
		r.handleSetScheduledRunReadState(w, req, strings.TrimSpace(parts[2]))
		return
	}
	writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "not_found", Message: "route not found", CorrelationID: correlationID(req)})
}

func (r *Router) handleListScheduledRunInbox(w http.ResponseWriter, req *http.Request) {
	viewer, ok := schedulerViewerFromRequest(w, req)
	if !ok {
		return
	}
	page, err := r.scheduler.ListRunInbox(req.Context(), viewer, scheduler.ListRunInboxQuery{
		ScheduledTaskID: strings.TrimSpace(req.URL.Query().Get("task_id")),
		ScheduledRunID:  strings.TrimSpace(req.URL.Query().Get("run_id")),
		Status:          scheduler.RunStatus(strings.TrimSpace(req.URL.Query().Get("status"))),
		ReadState:       scheduler.RunInboxReadState(strings.TrimSpace(req.URL.Query().Get("read_state"))),
		AttentionOnly:   boolQuery(req, "attention"),
		GroupByTask:     boolQuery(req, "group_by_task"),
		Limit:           schedulerBoundedPositiveQueryInt(req, "limit", 30),
		Offset:          schedulerNonNegativeQueryInt(req, "offset", 0),
	})
	if err != nil {
		writeSchedulerError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, toScheduledRunInboxPageDTO(page))
}

func (r *Router) handleSetScheduledRunReadState(w http.ResponseWriter, req *http.Request, scheduledRunID string) {
	viewer, ok := schedulerViewerFromRequest(w, req)
	if !ok {
		return
	}
	if scheduledRunID == "" {
		writeSchedulerError(w, req, scheduler.ErrInvalidRequest)
		return
	}
	var body struct {
		Read *bool `json:"read"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.Read == nil {
		writeSchedulerError(w, req, scheduler.ErrInvalidRequest)
		return
	}
	state, err := r.scheduler.SetRunReadState(req.Context(), viewer, scheduler.SetRunReadStateRequest{
		ScheduledRunID: scheduledRunID,
		Read:           *body.Read,
	})
	if err != nil {
		writeSchedulerError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, toScheduledRunReadStateDTO(state))
}

func (r *Router) handleScheduledTasks(w http.ResponseWriter, req *http.Request, parts []string) {
	if r.scheduler == nil {
		writeSchedulerError(w, req, scheduler.ErrServiceDependency)
		return
	}
	if req.Method == http.MethodGet && len(parts) == 2 {
		r.handleListScheduledTasks(w, req)
		return
	}
	if req.Method == http.MethodPost && len(parts) == 2 {
		r.handleCreateScheduledTask(w, req)
		return
	}
	if len(parts) < 3 {
		writeSchedulerError(w, req, scheduler.ErrInvalidRequest)
		return
	}
	scheduledTaskID := strings.TrimSpace(parts[2])
	if scheduledTaskID == "" {
		writeSchedulerError(w, req, scheduler.ErrInvalidRequest)
		return
	}
	if req.Method == http.MethodGet && len(parts) == 3 {
		if scheduledTaskID == "summary" {
			r.handleScheduledTaskStats(w, req)
			return
		}
		r.handleGetScheduledTask(w, req, scheduledTaskID)
		return
	}
	if req.Method == http.MethodPatch && len(parts) == 3 {
		r.handleUpdateScheduledTask(w, req, scheduledTaskID)
		return
	}
	if req.Method == http.MethodPost && len(parts) == 4 && parts[3] == "cancel" {
		r.handleCancelScheduledTask(w, req, scheduledTaskID)
		return
	}
	if req.Method == http.MethodPost && len(parts) == 4 && parts[3] == "run-now" {
		r.handleRunScheduledTaskNow(w, req, scheduledTaskID)
		return
	}
	if req.Method == http.MethodGet && len(parts) == 4 && parts[3] == "runs" {
		r.handleListScheduledTaskRuns(w, req, scheduledTaskID)
		return
	}
	if req.Method == http.MethodGet && len(parts) == 6 && parts[3] == "runs" && parts[5] == "responsibility-evaluations" {
		r.handleListResponsibilityEvaluationItems(w, req, scheduledTaskID, parts[4])
		return
	}
	if req.Method == http.MethodGet && len(parts) == 7 && parts[3] == "runs" && parts[5] == "artifacts" && parts[6] == "content" {
		r.handleScheduledRunArtifact(w, req, scheduledTaskID, parts[4], strings.TrimSpace(req.URL.Query().Get("artifact_id")))
		return
	}
	writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "not_found", Message: "route not found", CorrelationID: correlationID(req)})
}

func (r *Router) handleListScheduledTasks(w http.ResponseWriter, req *http.Request) {
	viewer, ok := schedulerViewerFromRequest(w, req)
	if !ok {
		return
	}
	from, ok := schedulerTimeQuery(w, req, "from")
	if !ok {
		return
	}
	to, ok := schedulerTimeQuery(w, req, "to")
	if !ok {
		return
	}
	page, err := r.scheduler.ListTasks(req.Context(), scheduler.ListTasksQuery{
		TenantID:           viewer.TenantID,
		IncludeArchived:    boolQuery(req, "include_archived"),
		ExpandOccurrences:  boolQuery(req, "expand_occurrences"),
		OccurrenceTimezone: strings.TrimSpace(req.URL.Query().Get("occurrence_timezone")),
		Limit:              schedulerBoundedPositiveQueryInt(req, "limit", 20),
		Offset:             schedulerNonNegativeQueryInt(req, "offset", 0),
		Q:                  strings.TrimSpace(req.URL.Query().Get("q")),
		Status:             scheduler.TaskStatus(strings.TrimSpace(req.URL.Query().Get("status"))),
		From:               from,
		To:                 to,
		Sort:               scheduler.TaskSort(strings.TrimSpace(req.URL.Query().Get("sort"))),
	})
	if err != nil {
		writeSchedulerError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (r *Router) handleScheduledTaskStats(w http.ResponseWriter, req *http.Request) {
	viewer, ok := schedulerViewerFromRequest(w, req)
	if !ok {
		return
	}
	stats, err := r.scheduler.GetTaskStats(req.Context(), viewer.TenantID)
	if err != nil {
		writeSchedulerError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (r *Router) handleCreateScheduledTask(w http.ResponseWriter, req *http.Request) {
	viewer, ok := schedulerViewerFromRequest(w, req)
	if !ok {
		return
	}
	var body scheduler.CreateTaskRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeSchedulerError(w, req, err)
		return
	}
	if body.IdempotencyKey == "" {
		body.IdempotencyKey = idempotencyKey(req)
	}
	task, err := r.scheduler.CreateTask(req.Context(), viewer, body)
	if err != nil {
		writeSchedulerError(w, req, err)
		return
	}
	writeJSON(w, http.StatusCreated, task)
}

func (r *Router) handleGetScheduledTask(w http.ResponseWriter, req *http.Request, scheduledTaskID string) {
	viewer, ok := schedulerViewerFromRequest(w, req)
	if !ok {
		return
	}
	task, err := r.scheduler.GetTask(req.Context(), scheduledTaskID)
	if err != nil {
		writeSchedulerError(w, req, err)
		return
	}
	if !schedulerTaskVisibleToViewer(task, viewer) {
		writeSchedulerError(w, req, scheduler.ErrNotFound)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (r *Router) handleUpdateScheduledTask(w http.ResponseWriter, req *http.Request, scheduledTaskID string) {
	viewer, ok := schedulerViewerFromRequest(w, req)
	if !ok {
		return
	}
	var body scheduler.UpdateTaskRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeSchedulerError(w, req, err)
		return
	}
	body.ScheduledTaskID = scheduledTaskID
	if body.IdempotencyKey == "" {
		body.IdempotencyKey = idempotencyKey(req)
	}
	task, err := r.scheduler.UpdateTask(req.Context(), viewer, body)
	if err != nil {
		writeSchedulerError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (r *Router) handleCancelScheduledTask(w http.ResponseWriter, req *http.Request, scheduledTaskID string) {
	viewer, ok := schedulerViewerFromRequest(w, req)
	if !ok {
		return
	}
	task, err := r.scheduler.CancelTask(req.Context(), viewer, scheduler.CancelTaskRequest{ScheduledTaskID: scheduledTaskID})
	if err != nil {
		writeSchedulerError(w, req, err)
		return
	}
	writeJSON(w, http.StatusAccepted, task)
}

func (r *Router) handleRunScheduledTaskNow(w http.ResponseWriter, req *http.Request, scheduledTaskID string) {
	viewer, ok := schedulerViewerFromRequest(w, req)
	if !ok {
		return
	}
	task, err := r.scheduler.GetTask(req.Context(), scheduledTaskID)
	if err != nil {
		writeSchedulerError(w, req, err)
		return
	}
	if !schedulerTaskVisibleToViewer(task, viewer) {
		writeSchedulerError(w, req, scheduler.ErrNotFound)
		return
	}
	body, err := decodeRunNowRequest(req.Body)
	if err != nil {
		writeSchedulerError(w, req, err)
		return
	}
	body.ScheduledTaskID = scheduledTaskID
	if body.IdempotencyKey == "" {
		body.IdempotencyKey = idempotencyKey(req)
	}
	run, err := r.scheduler.PrepareManualRun(req.Context(), viewer, body)
	if err != nil {
		writeSchedulerError(w, req, err)
		return
	}
	if run.Status == scheduler.RunStatusQueued {
		if r.schedulerDispatcher == nil {
			writeSchedulerError(w, req, scheduler.ErrServiceDependency)
			return
		}
		run, err = r.schedulerDispatcher.DispatchRun(req.Context(), task, run)
		if err != nil {
			writeSchedulerError(w, req, err)
			return
		}
	}
	writeJSON(w, http.StatusAccepted, toScheduledRunDTO(run))
}

func (r *Router) handleListScheduledTaskRuns(w http.ResponseWriter, req *http.Request, scheduledTaskID string) {
	viewer, ok := schedulerViewerFromRequest(w, req)
	if !ok {
		return
	}
	page, err := r.scheduler.ListRuns(req.Context(), scheduler.ListRunsQuery{
		TenantID:        viewer.TenantID,
		ScheduledTaskID: scheduledTaskID,
		ScheduledRunID:  strings.TrimSpace(req.URL.Query().Get("run_id")),
		Status:          scheduler.RunStatus(strings.TrimSpace(req.URL.Query().Get("status"))),
		Limit:           schedulerBoundedPositiveQueryInt(req, "limit", 20),
		Offset:          schedulerNonNegativeQueryInt(req, "offset", 0),
	})
	if err != nil {
		writeSchedulerError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, toScheduledRunPageDTO(page))
}

func (r *Router) handleListResponsibilityEvaluationItems(w http.ResponseWriter, req *http.Request, scheduledTaskID, scheduledRunID string) {
	viewer, ok := schedulerViewerFromRequest(w, req)
	if !ok {
		return
	}
	page, err := r.scheduler.ListResponsibilityEvaluationItems(req.Context(), scheduler.ListResponsibilityEvaluationItemsQuery{
		TenantID:        viewer.TenantID,
		ScheduledTaskID: strings.TrimSpace(scheduledTaskID),
		ScheduledRunID:  strings.TrimSpace(scheduledRunID),
		Status:          strings.TrimSpace(req.URL.Query().Get("status")),
		Cursor:          strings.TrimSpace(req.URL.Query().Get("cursor")),
		Limit:           schedulerBoundedPositiveQueryInt(req, "limit", 50),
	})
	if err != nil {
		writeSchedulerError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (r *Router) handleScheduledRunArtifact(w http.ResponseWriter, req *http.Request, scheduledTaskID, scheduledRunID, artifactID string) {
	viewer, ok := schedulerViewerFromRequest(w, req)
	if !ok {
		return
	}
	disposition := strings.TrimSpace(req.URL.Query().Get("disposition"))
	if disposition == "" {
		disposition = "attachment"
	}
	if disposition != "attachment" && disposition != "inline" {
		writeSchedulerError(w, req, scheduler.ErrInvalidRequest)
		return
	}
	content, err := r.scheduler.OpenRunArtifact(req.Context(), scheduler.OpenRunArtifactQuery{
		TenantID:        viewer.TenantID,
		ScheduledTaskID: scheduledTaskID,
		ScheduledRunID:  strings.TrimSpace(scheduledRunID),
		ArtifactID:      strings.TrimSpace(artifactID),
	})
	if err != nil {
		writeSchedulerError(w, req, err)
		return
	}
	defer content.Reader.Close()
	contentType := strings.TrimSpace(content.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if disposition == "inline" && (content.SizeBytes < 0 || content.SizeBytes > 8<<20) {
		writeSchedulerError(w, req, scheduler.ErrInvalidRequest)
		return
	}
	if disposition == "inline" && !scheduledArtifactInlineContentType(contentType) {
		disposition = "attachment"
	}
	fileName := path.Base(strings.ReplaceAll(strings.TrimSpace(content.FileName), "\\", "/"))
	fileName = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, fileName)
	if fileName == "" || fileName == "." {
		fileName = "artifact"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": fileName}))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if content.SizeBytes >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(content.SizeBytes, 10))
	}
	if digest, err := hex.DecodeString(strings.TrimSpace(content.SHA256)); err == nil && len(digest) == sha256.Size {
		w.Header().Set("Digest", "sha-256="+base64.StdEncoding.EncodeToString(digest))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, content.Reader)
}

func scheduledArtifactInlineContentType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	switch strings.ToLower(mediaType) {
	case "text/plain", "text/markdown", "text/csv", "application/json", "application/pdf",
		"image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

func decodeRunNowRequest(reader io.Reader) (scheduler.RunNowRequest, error) {
	var body scheduler.RunNowRequest
	if reader == nil {
		return body, nil
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		return scheduler.RunNowRequest{}, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return body, nil
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return scheduler.RunNowRequest{}, err
	}
	return body, nil
}

func schedulerViewerFromRequest(w http.ResponseWriter, req *http.Request) (scheduler.ViewerRef, bool) {
	viewerID := strings.TrimSpace(req.Header.Get("X-User-ID"))
	kind := scheduler.ViewerKind(strings.TrimSpace(req.Header.Get("X-Viewer-Kind")))
	permissionScope := splitHeader(req.Header.Get("X-Permission-Scope"))
	principal, authenticated := identity.PrincipalFromContext(req.Context())
	tenantID := strings.TrimSpace(firstNonEmpty(req.Header.Get("X-Tenant-ID"), req.Header.Get("X-Tenant")))
	if tenantID == "" {
		if authenticated && !identity.TrustedInternalServiceFromContext(req.Context()) {
			writeSchedulerError(w, req, operations.ErrPermissionDenied)
			return scheduler.ViewerRef{}, false
		}
		tenantID = "system"
	}
	mayManageSystemTenant := authenticated &&
		viewerHasScope(principal.CoarseScopes, permissionSchedulerWrite, permissionSchedulerWrite) &&
		viewerHasScope(principal.CoarseScopes, operations.PermissionOperationsManage, operations.PermissionOperationsManage)
	if derivedTenantID, ok := derivedOrganizationTenantFromContext(req.Context()); ok && tenantID == derivedTenantID && mayManageSystemTenant {
		tenantID = "system"
	}
	if viewerID == "" {
		viewerID = strings.TrimSpace(req.Header.Get("X-Agent-ID"))
	}
	if kind == "" {
		if req.Header.Get("X-Agent-ID") != "" {
			kind = scheduler.ViewerKindAgent
		} else {
			kind = scheduler.ViewerKindUser
		}
	}
	if viewerID == "" {
		writeSchedulerError(w, req, scheduler.ErrInvalidRequest)
		return scheduler.ViewerRef{}, false
	}
	return scheduler.ViewerRef{
		TenantID:        tenantID,
		ViewerID:        viewerID,
		ViewerKind:      kind,
		DisplayName:     displayNameFromRequest(req),
		ProfileID:       strings.TrimSpace(req.Header.Get("X-Agent-Profile-ID")),
		PermissionScope: permissionScope,
	}, true
}

func schedulerTaskVisibleToViewer(task scheduler.ScheduledTask, viewer scheduler.ViewerRef) bool {
	taskTenant := strings.TrimSpace(task.TenantID)
	if taskTenant == "" {
		taskTenant = "system"
	}
	viewerTenant := strings.TrimSpace(viewer.TenantID)
	if viewerTenant == "" {
		viewerTenant = "system"
	}
	return taskTenant == viewerTenant
}

func writeSchedulerError(w http.ResponseWriter, req *http.Request, err error) {
	status, response := MapError(err, correlationID(req))
	if errors.Is(err, scheduler.ErrInvalidSchedule) || errors.Is(err, scheduler.ErrInvalidTarget) {
		response.Code = ErrorInvalidRequest
	}
	writeJSON(w, status, response)
}

func boolQuery(req *http.Request, name string) bool {
	value, err := strconv.ParseBool(strings.TrimSpace(req.URL.Query().Get(name)))
	return err == nil && value
}

func schedulerNonNegativeQueryInt(req *http.Request, name string, fallback int) int {
	raw := strings.TrimSpace(req.URL.Query().Get(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func schedulerBoundedPositiveQueryInt(req *http.Request, name string, fallback int) int {
	value := positiveQueryInt(req, name, fallback)
	if value > schedulerMaxListPageSize {
		return schedulerMaxListPageSize
	}
	return value
}

func schedulerTimeQuery(w http.ResponseWriter, req *http.Request, name string) (time.Time, bool) {
	raw := strings.TrimSpace(req.URL.Query().Get(name))
	if raw == "" {
		return time.Time{}, true
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		writeSchedulerError(w, req, scheduler.ErrInvalidRequest)
		return time.Time{}, false
	}
	return parsed.UTC(), true
}
