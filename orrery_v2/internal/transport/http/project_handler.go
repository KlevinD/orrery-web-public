package httptransport

import (
	"net/http"
	"strconv"
	"strings"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
	projectadmin "orrery_v2/internal/app/projectadmin"
)

func (r *Router) handleListProjects(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if r.projectCommand == nil {
		status, body := MapError(projectadmin.ErrServiceDependency, cid)
		writeJSON(w, status, body)
		return
	}
	limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(req.URL.Query().Get("offset"))
	page, err := r.projectCommand.ListProjects(req.Context(), projectViewerRef(viewer), projectadmin.ListProjectsQuery{
		OrganizationID: req.URL.Query().Get("organization_id"),
		Status:         req.URL.Query().Get("status"),
		Limit:          limit,
		Offset:         offset,
	})
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (r *Router) handleGetProject(w http.ResponseWriter, req *http.Request, projectID string) {
	cid := correlationID(req)
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if r.projectCommand == nil {
		status, body := MapError(projectadmin.ErrServiceDependency, cid)
		writeJSON(w, status, body)
		return
	}
	view, err := r.projectCommand.GetProjectView(req.Context(), projectViewerRef(viewer), strings.TrimSpace(projectID))
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (r *Router) handleCreateProject(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if r.projectCommand == nil {
		status, body := MapError(projectadmin.ErrServiceDependency, cid)
		writeJSON(w, status, body)
		return
	}
	cmd, err := decodeCreateProject(req)
	if err != nil {
		status, body := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, body)
		return
	}
	project, err := r.projectCommand.CreateProject(req.Context(), projectadmin.ViewerRef{
		ViewerID:               viewer.ViewerID,
		ViewerKind:             projectadmin.ViewerKind(viewer.ViewerKind),
		TenantID:               viewer.TenantID,
		PermissionScope:        append([]string(nil), viewer.PermissionScope...),
		AuthorizationSubjectID: viewer.AuthorizationSubjectID,
	}, cmd)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusCreated, project)
}

func (r *Router) handleUpdateProject(w http.ResponseWriter, req *http.Request, projectID string) {
	cid := correlationID(req)
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if r.projectCommand == nil {
		status, body := MapError(projectadmin.ErrServiceDependency, cid)
		writeJSON(w, status, body)
		return
	}
	cmd, err := decodeUpdateProject(req)
	if err != nil {
		status, body := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, body)
		return
	}
	cmd.ProjectID = strings.TrimSpace(projectID)
	if version, versionErr := expectedVersion(req); versionErr != nil {
		status, body := invalidRequest(cid, "If-Match", "If-Match must contain the current project version")
		writeJSON(w, status, body)
		return
	} else {
		cmd.ExpectedVersion = int64(version)
	}
	project, err := r.projectCommand.UpdateProject(req.Context(), projectViewerRef(viewer), cmd)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, project)
}

func (r *Router) handleArchiveProject(w http.ResponseWriter, req *http.Request, projectID string) {
	cid := correlationID(req)
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if r.projectCommand == nil {
		status, body := MapError(projectadmin.ErrServiceDependency, cid)
		writeJSON(w, status, body)
		return
	}
	version, err := expectedVersion(req)
	if err != nil {
		status, body := invalidRequest(cid, "If-Match", "If-Match must contain the current project version")
		writeJSON(w, status, body)
		return
	}
	project, err := r.projectCommand.ArchiveProject(req.Context(), projectViewerRef(viewer), projectadmin.ArchiveProjectCommand{ProjectID: strings.TrimSpace(projectID), ExpectedVersion: int64(version)})
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, project)
}

func projectViewerRef(viewer missioncontrol.ViewerRef) projectadmin.ViewerRef {
	return projectadmin.ViewerRef{
		ViewerID:               viewer.ViewerID,
		ViewerKind:             projectadmin.ViewerKind(viewer.ViewerKind),
		TenantID:               viewer.TenantID,
		PermissionScope:        append([]string(nil), viewer.PermissionScope...),
		AuthorizationSubjectID: viewer.AuthorizationSubjectID,
	}
}
