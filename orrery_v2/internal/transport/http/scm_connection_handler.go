package httptransport

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/scmconnection"
)

func (r *Router) handleSCMConnections(w http.ResponseWriter, req *http.Request, parts []string) {
	if r.scmConnections == nil {
		writeMappedError(w, req, scmconnection.ErrDependency)
		return
	}
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		writeMappedError(w, req, err)
		return
	}
	scmViewer := scmConnectionViewer(viewer)
	if len(parts) == 2 {
		switch req.Method {
		case http.MethodGet:
			limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
			offset, _ := strconv.Atoi(req.URL.Query().Get("offset"))
			page, err := r.scmConnections.List(req.Context(), scmViewer, scmconnection.ListQuery{
				OrganizationID: req.URL.Query().Get("organization_id"), Provider: req.URL.Query().Get("provider"),
				Status: req.URL.Query().Get("status"), Limit: limit, Offset: offset,
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
				OrganizationID            string   `json:"organization_id,omitempty"`
				Scope                     string   `json:"scope"`
				Provider                  string   `json:"provider"`
				BaseURL                   string   `json:"base_url,omitempty"`
				InstallationOrAccountID   string   `json:"installation_or_account_id,omitempty"`
				SecretRef                 string   `json:"secret_ref,omitempty"`
				AllowedRepositoryPatterns []string `json:"allowed_repository_patterns,omitempty"`
			}
			if err := decodeStrictJSON(req, &body); err != nil {
				writeInvalidBody(w, req)
				return
			}
			created, err := r.scmConnections.Create(req.Context(), scmViewer, scmconnection.CreateCommand{
				IdempotencyKey: key, OrganizationID: body.OrganizationID, Scope: body.Scope, Provider: body.Provider,
				BaseURL: body.BaseURL, InstallationOrAccountID: body.InstallationOrAccountID, SecretRef: body.SecretRef,
				AllowedRepositoryPatterns: body.AllowedRepositoryPatterns,
			})
			if err != nil {
				writeMappedError(w, req, err)
				return
			}
			writeJSON(w, http.StatusCreated, created)
		default:
			http.NotFound(w, req)
		}
		return
	}
	connectionID := strings.TrimSpace(parts[2])
	if len(parts) == 3 {
		switch req.Method {
		case http.MethodGet:
			connection, err := r.scmConnections.Get(req.Context(), scmViewer, connectionID)
			if err != nil {
				writeMappedError(w, req, err)
				return
			}
			writeJSON(w, http.StatusOK, connection)
		case http.MethodPatch:
			key, err := requiredIdempotencyKey(req)
			if err != nil {
				writeInvalidIdempotencyKey(w, req)
				return
			}
			var body struct {
				BaseURL                   *string   `json:"base_url"`
				InstallationOrAccountID   *string   `json:"installation_or_account_id"`
				AllowedRepositoryPatterns *[]string `json:"allowed_repository_patterns"`
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
			connection, err := r.scmConnections.Update(req.Context(), scmViewer, scmconnection.UpdateCommand{
				IdempotencyKey: key, ConnectionID: connectionID, ExpectedVersion: version, BaseURL: body.BaseURL,
				InstallationOrAccountID: body.InstallationOrAccountID, AllowedRepositoryPatterns: body.AllowedRepositoryPatterns,
			})
			if err != nil {
				writeMappedError(w, req, err)
				return
			}
			writeJSON(w, http.StatusOK, connection)
		default:
			http.NotFound(w, req)
		}
		return
	}
	if len(parts) != 4 {
		http.NotFound(w, req)
		return
	}
	switch parts[3] {
	case "repositories":
		if req.Method != http.MethodGet {
			http.NotFound(w, req)
			return
		}
		repositories, err := r.scmConnections.ListRepositories(req.Context(), scmViewer, connectionID)
		if err != nil {
			writeMappedError(w, req, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": repositories})
	case "verify":
		if req.Method != http.MethodPost {
			http.NotFound(w, req)
			return
		}
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
		connection, verification, err := r.scmConnections.Verify(req.Context(), scmViewer, scmconnection.ActionCommand{IdempotencyKey: key, ConnectionID: connectionID, ExpectedVersion: version})
		if err != nil {
			writeMappedError(w, req, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"connection": connection, "verification": verification})
	case "rotate":
		if req.Method != http.MethodPost {
			http.NotFound(w, req)
			return
		}
		key, err := requiredIdempotencyKey(req)
		if err != nil {
			writeInvalidIdempotencyKey(w, req)
			return
		}
		var body struct {
			SecretRef string `json:"secret_ref"`
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
		connection, err := r.scmConnections.Rotate(req.Context(), scmViewer, scmconnection.RotateCommand{IdempotencyKey: key, ConnectionID: connectionID, SecretRef: body.SecretRef, ExpectedVersion: version})
		if err != nil {
			writeMappedError(w, req, err)
			return
		}
		writeJSON(w, http.StatusOK, connection)
	case "revoke":
		if req.Method != http.MethodPost {
			http.NotFound(w, req)
			return
		}
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
		connection, err := r.scmConnections.Revoke(req.Context(), scmViewer, scmconnection.ActionCommand{IdempotencyKey: key, ConnectionID: connectionID, ExpectedVersion: version})
		if err != nil {
			writeMappedError(w, req, err)
			return
		}
		writeJSON(w, http.StatusOK, connection)
	default:
		http.NotFound(w, req)
	}
}

func scmConnectionViewer(viewer missioncontrol.ViewerRef) scmconnection.ViewerRef {
	return scmconnection.ViewerRef{
		ViewerID: viewer.ViewerID, ViewerKind: string(viewer.ViewerKind), TenantID: viewer.TenantID,
		AuthorizationSubjectID: viewer.AuthorizationSubjectID, PermissionScope: append([]string(nil), viewer.PermissionScope...),
	}
}

func decodeStrictJSON(req *http.Request, target any) error {
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("request body must contain exactly one JSON value")
		}
		return err
	}
	return nil
}

func requiredExpectedVersion(req *http.Request) (int64, error) {
	version, err := expectedVersion(req)
	if err != nil || version <= 0 {
		return 0, scmconnection.ErrInvalidRequest
	}
	return int64(version), nil
}

func writeInvalidBody(w http.ResponseWriter, req *http.Request) {
	status, body := invalidRequest(correlationID(req), "body", "request body is invalid")
	writeJSON(w, status, body)
}

func writeInvalidVersion(w http.ResponseWriter, req *http.Request) {
	status, body := invalidRequest(correlationID(req), "If-Match", "If-Match must contain the current version")
	writeJSON(w, status, body)
}

func writeInvalidIdempotencyKey(w http.ResponseWriter, req *http.Request) {
	status, body := invalidRequest(correlationID(req), "Idempotency-Key", "Idempotency-Key is required and must be at most 256 characters")
	writeJSON(w, status, body)
}
