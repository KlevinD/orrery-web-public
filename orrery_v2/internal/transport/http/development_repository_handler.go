package httptransport

import (
	"net/http"
	"strings"

	developmentproject "orrery_v2/internal/app/developmentproject"
)

func (r *Router) handleDevelopmentProjectRepositories(w http.ResponseWriter, req *http.Request, viewer developmentproject.ViewerRef, projectID string, parts []string) {
	if len(parts) == 4 {
		switch req.Method {
		case http.MethodGet:
			repositories, err := r.developmentProjects.ListRepositories(req.Context(), viewer, projectID)
			if err != nil {
				writeMappedError(w, req, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": repositories})
		case http.MethodPost:
			key, err := requiredIdempotencyKey(req)
			if err != nil {
				writeInvalidIdempotencyKey(w, req)
				return
			}
			var body developmentproject.BindRepositoryCommand
			if err := decodeStrictJSON(req, &body); err != nil {
				writeInvalidBody(w, req)
				return
			}
			version, err := requiredExpectedVersion(req)
			if err != nil {
				writeInvalidVersion(w, req)
				return
			}
			body.DevelopmentProjectID = projectID
			body.ExpectedProjectVersion = version
			body.IdempotencyKey = key
			repository, project, err := r.developmentProjects.BindRepository(req.Context(), viewer, body)
			if err != nil {
				writeMappedError(w, req, err)
				return
			}
			writeJSON(w, http.StatusCreated, map[string]any{"repository": repository, "project": project})
		default:
			http.NotFound(w, req)
		}
		return
	}
	if len(parts) < 5 {
		http.NotFound(w, req)
		return
	}
	bindingID := strings.TrimSpace(parts[4])
	if len(parts) == 6 && parts[5] == "verify" && req.Method == http.MethodPost {
		key, err := requiredIdempotencyKey(req)
		if err != nil {
			writeInvalidIdempotencyKey(w, req)
			return
		}
		var body struct {
			ExpectedBindingVersion int64 `json:"expected_binding_version"`
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
		repository, project, err := r.developmentProjects.VerifyRepository(req.Context(), viewer, developmentproject.VerifyRepositoryCommand{
			IdempotencyKey: key, DevelopmentProjectID: projectID, BindingID: bindingID,
			ExpectedProjectVersion: projectVersion, ExpectedBindingVersion: body.ExpectedBindingVersion,
		})
		if err != nil {
			writeMappedError(w, req, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"repository": repository, "project": project})
		return
	}
	if len(parts) != 5 {
		http.NotFound(w, req)
		return
	}
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
		ExpectedBindingVersion int64     `json:"expected_binding_version"`
		SourceSubdirectory     *string   `json:"source_subdirectory"`
		BuildCommands          *[]string `json:"build_commands"`
		TestCommands           *[]string `json:"test_commands"`
		BranchPrefix           *string   `json:"branch_prefix"`
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
		repository, project, err := r.developmentProjects.UpdateRepository(req.Context(), viewer, developmentproject.UpdateRepositoryCommand{
			IdempotencyKey: key, DevelopmentProjectID: projectID, BindingID: bindingID, ExpectedProjectVersion: projectVersion,
			ExpectedBindingVersion: body.ExpectedBindingVersion, SourceSubdirectory: body.SourceSubdirectory,
			BuildCommands: body.BuildCommands, TestCommands: body.TestCommands, BranchPrefix: body.BranchPrefix,
		})
		if err != nil {
			writeMappedError(w, req, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"repository": repository, "project": project})
	case http.MethodDelete:
		project, err := r.developmentProjects.RemoveRepository(req.Context(), viewer, developmentproject.RemoveRepositoryCommand{
			IdempotencyKey: key, DevelopmentProjectID: projectID, BindingID: bindingID, ExpectedProjectVersion: projectVersion,
			ExpectedBindingVersion: body.ExpectedBindingVersion,
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
