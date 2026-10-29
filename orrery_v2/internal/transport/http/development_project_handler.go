package httptransport

import (
	"net/http"
	"strconv"
	"strings"

	developmentproject "orrery_v2/internal/app/developmentproject"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

func (r *Router) handleDevelopmentProjects(w http.ResponseWriter, req *http.Request, parts []string) {
	if r.developmentProjects == nil {
		writeMappedError(w, req, developmentproject.ErrDependency)
		return
	}
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		writeMappedError(w, req, err)
		return
	}
	projectViewer := developmentProjectViewer(viewer)
	if len(parts) == 2 {
		r.handleDevelopmentProjectCollection(w, req, projectViewer)
		return
	}
	projectID := strings.TrimSpace(parts[2])
	if len(parts) == 3 {
		r.handleDevelopmentProjectItem(w, req, projectViewer, projectID)
		return
	}
	if len(parts) == 4 && parts[3] == "archive" && req.Method == http.MethodPost {
		key, err := requiredIdempotencyKey(req)
		if err != nil {
			writeInvalidIdempotencyKey(w, req)
			return
		}
		version, err := requiredExpectedVersion(req)
		if err != nil {
			writeInvalidVersion(w, req)
			return
		}
		project, err := r.developmentProjects.Archive(req.Context(), projectViewer, developmentproject.ArchiveCommand{IdempotencyKey: key, DevelopmentProjectID: projectID, ExpectedVersion: version})
		if err != nil {
			writeMappedError(w, req, err)
			return
		}
		writeJSON(w, http.StatusOK, project)
		return
	}
	if len(parts) >= 4 && parts[3] == "members" {
		r.handleDevelopmentProjectMembers(w, req, projectViewer, projectID, parts)
		return
	}
	if len(parts) >= 4 && parts[3] == "repositories" {
		r.handleDevelopmentProjectRepositories(w, req, projectViewer, projectID, parts)
		return
	}
	if len(parts) == 4 && parts[3] == "executions" && req.Method == http.MethodGet {
		r.handleDevelopmentProjectExecutions(w, req, projectViewer, projectID)
		return
	}
	if len(parts) == 4 && parts[3] == "workspaces" && req.Method == http.MethodGet {
		executions, ok := r.developmentProjects.(DevelopmentExecutionService)
		if !ok {
			writeMappedError(w, req, developmentproject.ErrDependency)
			return
		}
		limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(req.URL.Query().Get("offset"))
		page, err := executions.ListWorkspaces(req.Context(), projectViewer, projectID, developmentproject.ListExecutionArtifactsQuery{TodoID: strings.TrimSpace(req.URL.Query().Get("todo_id")), Status: strings.TrimSpace(req.URL.Query().Get("status")), Limit: limit, Offset: offset})
		if err != nil {
			writeMappedError(w, req, err)
			return
		}
		writeJSON(w, http.StatusOK, page)
		return
	}
	if len(parts) == 4 && parts[3] == "runs" && req.Method == http.MethodGet {
		executions, ok := r.developmentProjects.(DevelopmentExecutionService)
		if !ok {
			writeMappedError(w, req, developmentproject.ErrDependency)
			return
		}
		limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(req.URL.Query().Get("offset"))
		page, err := executions.ListRuns(req.Context(), projectViewer, projectID, developmentproject.ListExecutionArtifactsQuery{TodoID: strings.TrimSpace(req.URL.Query().Get("todo_id")), Status: strings.TrimSpace(req.URL.Query().Get("status")), Limit: limit, Offset: offset})
		if err != nil {
			writeMappedError(w, req, err)
			return
		}
		writeJSON(w, http.StatusOK, page)
		return
	}
	http.NotFound(w, req)
}

func (r *Router) handleDevelopmentProjectExecutions(w http.ResponseWriter, req *http.Request, viewer developmentproject.ViewerRef, projectID string) {
	limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(req.URL.Query().Get("offset"))
	page, err := r.developmentProjects.ListExecutions(req.Context(), viewer, projectID, developmentproject.ListExecutionsQuery{
		TodoID: strings.TrimSpace(req.URL.Query().Get("todo_id")), TodoStatus: strings.TrimSpace(req.URL.Query().Get("todo_status")), MissionStatus: strings.TrimSpace(req.URL.Query().Get("mission_status")),
		AssigneeID: strings.TrimSpace(req.URL.Query().Get("assignee_id")), Blocked: optionalProjectQueryBool(req, "blocked"),
		PendingApproval: optionalProjectQueryBool(req, "pending_approval"), Limit: limit, Offset: offset,
	})
	if err != nil {
		writeMappedError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func optionalProjectQueryBool(req *http.Request, name string) *bool {
	raw := strings.TrimSpace(req.URL.Query().Get(name))
	if raw == "" {
		return nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return nil
	}
	return &value
}

func (r *Router) handleDevelopmentProjectCollection(w http.ResponseWriter, req *http.Request, viewer developmentproject.ViewerRef) {
	switch req.Method {
	case http.MethodGet:
		limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(req.URL.Query().Get("offset"))
		page, err := r.developmentProjects.List(req.Context(), viewer, developmentproject.ListQuery{
			OrganizationID: req.URL.Query().Get("organization_id"), Status: req.URL.Query().Get("status"), Limit: limit, Offset: offset,
		})
		if err != nil {
			writeMappedError(w, req, err)
			return
		}
		writeJSON(w, http.StatusOK, page)
	case http.MethodPost:
		key, err := requiredIdempotencyKey(req)
		if err != nil {
			writeInvalidIdempotencyKey(w, req)
			return
		}
		var body struct {
			OrganizationID        string                             `json:"organization_id,omitempty"`
			Name                  string                             `json:"name"`
			Description           string                             `json:"description,omitempty"`
			DefaultAutomationMode string                             `json:"default_automation_mode"`
			MaxConcurrentRuns     int                                `json:"max_concurrent_runs"`
			ExecutionPolicy       developmentproject.ExecutionPolicy `json:"execution_policy"`
		}
		if err := decodeStrictJSON(req, &body); err != nil {
			writeInvalidBody(w, req)
			return
		}
		project, err := r.developmentProjects.Create(req.Context(), viewer, developmentproject.CreateCommand{
			IdempotencyKey: key, OrganizationID: body.OrganizationID, Name: body.Name, Description: body.Description,
			DefaultAutomationMode: body.DefaultAutomationMode, MaxConcurrentRuns: body.MaxConcurrentRuns, ExecutionPolicy: body.ExecutionPolicy,
		})
		if err != nil {
			writeMappedError(w, req, err)
			return
		}
		writeJSON(w, http.StatusCreated, project)
	default:
		http.NotFound(w, req)
	}
}

func (r *Router) handleDevelopmentProjectItem(w http.ResponseWriter, req *http.Request, viewer developmentproject.ViewerRef, projectID string) {
	switch req.Method {
	case http.MethodGet:
		project, err := r.developmentProjects.Get(req.Context(), viewer, projectID)
		if err != nil {
			writeMappedError(w, req, err)
			return
		}
		writeJSON(w, http.StatusOK, project)
	case http.MethodPatch:
		key, err := requiredIdempotencyKey(req)
		if err != nil {
			writeInvalidIdempotencyKey(w, req)
			return
		}
		var body struct {
			Name                  *string                                  `json:"name"`
			Description           *string                                  `json:"description"`
			DefaultAutomationMode *string                                  `json:"default_automation_mode"`
			MaxConcurrentRuns     *int                                     `json:"max_concurrent_runs"`
			ExecutionPolicy       *developmentproject.ExecutionPolicyPatch `json:"execution_policy"`
		}
		if err := decodeStrictJSON(req, &body); err != nil {
			writeInvalidBody(w, req)
			return
		}
		version, err := requiredExpectedVersion(req)
		if err != nil {
			writeInvalidVersion(w, req)
			return
		}
		project, err := r.developmentProjects.Update(req.Context(), viewer, developmentproject.UpdateCommand{
			IdempotencyKey: key, DevelopmentProjectID: projectID, ExpectedVersion: version, Name: body.Name, Description: body.Description,
			DefaultAutomationMode: body.DefaultAutomationMode, MaxConcurrentRuns: body.MaxConcurrentRuns, ExecutionPolicy: body.ExecutionPolicy,
		})
		if err != nil {
			writeMappedError(w, req, err)
			return
		}
		writeJSON(w, http.StatusOK, project)
	default:
		http.NotFound(w, req)
	}
}

func (r *Router) handleDevelopmentProjectMembers(w http.ResponseWriter, req *http.Request, viewer developmentproject.ViewerRef, projectID string, parts []string) {
	if len(parts) == 4 {
		switch req.Method {
		case http.MethodGet:
			members, err := r.developmentProjects.ListMembers(req.Context(), viewer, projectID)
			if err != nil {
				writeMappedError(w, req, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": members})
		case http.MethodPost:
			key, err := requiredIdempotencyKey(req)
			if err != nil {
				writeInvalidIdempotencyKey(w, req)
				return
			}
			var body struct {
				SubjectKind string `json:"subject_kind"`
				SubjectID   string `json:"subject_id"`
				Role        string `json:"role"`
			}
			if err := decodeStrictJSON(req, &body); err != nil {
				writeInvalidBody(w, req)
				return
			}
			version, err := requiredExpectedVersion(req)
			if err != nil {
				writeInvalidVersion(w, req)
				return
			}
			member, project, err := r.developmentProjects.AddMember(req.Context(), viewer, developmentproject.AddMemberCommand{
				IdempotencyKey: key, DevelopmentProjectID: projectID, ExpectedProjectVersion: version,
				SubjectKind: body.SubjectKind, SubjectID: body.SubjectID, Role: body.Role,
			})
			if err != nil {
				writeMappedError(w, req, err)
				return
			}
			writeJSON(w, http.StatusCreated, map[string]any{"member": member, "project": project})
		default:
			http.NotFound(w, req)
		}
		return
	}
	if len(parts) != 5 {
		http.NotFound(w, req)
		return
	}
	memberID := strings.TrimSpace(parts[4])
	key := ""
	if req.Method == http.MethodPatch || req.Method == http.MethodDelete {
		var err error
		key, err = requiredIdempotencyKey(req)
		if err != nil {
			writeInvalidIdempotencyKey(w, req)
			return
		}
	}
	var body struct {
		Role                  string `json:"role,omitempty"`
		ExpectedMemberVersion int64  `json:"expected_member_version"`
	}
	if err := decodeStrictJSON(req, &body); err != nil {
		writeInvalidBody(w, req)
		return
	}
	projectVersion, err := requiredExpectedVersion(req)
	if err != nil {
		writeInvalidVersion(w, req)
		return
	}
	switch req.Method {
	case http.MethodPatch:
		member, project, err := r.developmentProjects.UpdateMember(req.Context(), viewer, developmentproject.UpdateMemberCommand{
			IdempotencyKey: key, DevelopmentProjectID: projectID, MemberID: memberID, ExpectedProjectVersion: projectVersion,
			ExpectedMemberVersion: body.ExpectedMemberVersion, Role: body.Role,
		})
		if err != nil {
			writeMappedError(w, req, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"member": member, "project": project})
	case http.MethodDelete:
		project, err := r.developmentProjects.RemoveMember(req.Context(), viewer, developmentproject.RemoveMemberCommand{
			IdempotencyKey: key, DevelopmentProjectID: projectID, MemberID: memberID, ExpectedProjectVersion: projectVersion,
			ExpectedMemberVersion: body.ExpectedMemberVersion,
		})
		if err != nil {
			writeMappedError(w, req, err)
			return
		}
		writeJSON(w, http.StatusOK, project)
	default:
		http.NotFound(w, req)
	}
}

func developmentProjectViewer(viewer missioncontrol.ViewerRef) developmentproject.ViewerRef {
	return developmentproject.ViewerRef{
		ViewerID: viewer.ViewerID, ViewerKind: string(viewer.ViewerKind), TenantID: viewer.TenantID,
		AuthorizationSubjectID: viewer.AuthorizationSubjectID, PermissionScope: append([]string(nil), viewer.PermissionScope...),
	}
}
