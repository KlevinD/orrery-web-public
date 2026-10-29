package httptransport

import (
	"encoding/json"
	"net/http"

	"orrery_v2/internal/app/knowledgeprovider"
)

type knowledgeProviderRequest struct {
	ID            string                                  `json:"id"`
	Name          string                                  `json:"name"`
	MCPServerID   string                                  `json:"mcp_server_id"`
	Mode          knowledgeprovider.Mode                  `json:"mode"`
	Status        knowledgeprovider.Status                `json:"status"`
	ToolMapping   map[knowledgeprovider.Capability]string `json:"tool_mapping"`
	FolderInclude []string                                `json:"folder_include"`
	FolderExclude []string                                `json:"folder_exclude"`
}

type knowledgeProviderPatchRequest struct {
	Name          *string                                  `json:"name"`
	MCPServerID   *string                                  `json:"mcp_server_id"`
	Mode          *knowledgeprovider.Mode                  `json:"mode"`
	Status        *knowledgeprovider.Status                `json:"status"`
	ToolMapping   *map[knowledgeprovider.Capability]string `json:"tool_mapping"`
	FolderInclude *[]string                                `json:"folder_include"`
	FolderExclude *[]string                                `json:"folder_exclude"`
}

func (r *Router) handleKnowledgeProviders(w http.ResponseWriter, req *http.Request, parts []string) {
	if r.knowledgeProviders == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "knowledge provider service unavailable", CorrelationID: correlationID(req), Retryable: true})
		return
	}
	if len(parts) == 2 {
		switch req.Method {
		case http.MethodGet:
			bindings, err := r.knowledgeProviders.ListBindings(req.Context())
			if err != nil {
				r.writeKnowledgeProviderError(w, req, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": bindings, "total": len(bindings)})
		case http.MethodPost:
			r.handleCreateKnowledgeProvider(w, req)
		default:
			http.NotFound(w, req)
		}
		return
	}
	if len(parts) == 3 {
		switch req.Method {
		case http.MethodGet:
			binding, err := r.knowledgeProviders.GetBinding(req.Context(), parts[2])
			if err != nil {
				r.writeKnowledgeProviderError(w, req, err)
				return
			}
			writeJSON(w, http.StatusOK, binding)
		case http.MethodPatch:
			r.handleUpdateKnowledgeProvider(w, req, parts[2])
		default:
			http.NotFound(w, req)
		}
		return
	}
	if len(parts) == 4 && req.Method == http.MethodPost {
		switch parts[3] {
		case "test":
			result, err := r.knowledgeProviders.TestBinding(req.Context(), parts[2], requestViewerID(req))
			if err != nil {
				r.writeKnowledgeProviderError(w, req, err)
				return
			}
			writeJSON(w, http.StatusOK, result)
		case "sync":
			r.handleSyncKnowledgeProvider(w, req, parts[2])
		default:
			http.NotFound(w, req)
		}
		return
	}
	http.NotFound(w, req)
}

func (r *Router) handleCreateKnowledgeProvider(w http.ResponseWriter, req *http.Request) {
	var body knowledgeProviderRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: ErrorInvalidRequest, Message: "request body is invalid", CorrelationID: correlationID(req)})
		return
	}
	binding, err := r.knowledgeProviders.CreateBinding(req.Context(), knowledgeprovider.Binding{
		ID:            body.ID,
		Name:          body.Name,
		MCPServerID:   body.MCPServerID,
		Mode:          body.Mode,
		Status:        body.Status,
		ToolMapping:   body.ToolMapping,
		FolderInclude: body.FolderInclude,
		FolderExclude: body.FolderExclude,
	}, requestViewerID(req))
	if err != nil {
		r.writeKnowledgeProviderError(w, req, err)
		return
	}
	writeJSON(w, http.StatusCreated, binding)
}

func (r *Router) handleUpdateKnowledgeProvider(w http.ResponseWriter, req *http.Request, id string) {
	binding, err := r.knowledgeProviders.GetBinding(req.Context(), id)
	if err != nil {
		r.writeKnowledgeProviderError(w, req, err)
		return
	}
	var body knowledgeProviderPatchRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: ErrorInvalidRequest, Message: "request body is invalid", CorrelationID: correlationID(req)})
		return
	}
	if body.Name != nil {
		binding.Name = *body.Name
	}
	if body.MCPServerID != nil {
		binding.MCPServerID = *body.MCPServerID
	}
	if body.Mode != nil {
		binding.Mode = *body.Mode
	}
	if body.Status != nil {
		binding.Status = *body.Status
	}
	if body.ToolMapping != nil {
		binding.ToolMapping = *body.ToolMapping
	}
	if body.FolderInclude != nil {
		binding.FolderInclude = *body.FolderInclude
	}
	if body.FolderExclude != nil {
		binding.FolderExclude = *body.FolderExclude
	}
	updated, err := r.knowledgeProviders.UpdateBinding(req.Context(), binding, requestViewerID(req))
	if err != nil {
		r.writeKnowledgeProviderError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (r *Router) handleSyncKnowledgeProvider(w http.ResponseWriter, req *http.Request, id string) {
	var body struct {
		OwnerID string `json:"owner_id"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: ErrorInvalidRequest, Message: "request body is invalid", CorrelationID: correlationID(req)})
		return
	}
	result, err := r.knowledgeProviders.Sync(req.Context(), id, knowledgeprovider.SyncRequest{PrincipalID: requestViewerID(req), OwnerID: body.OwnerID})
	if err != nil {
		r.writeKnowledgeProviderError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (r *Router) writeKnowledgeProviderError(w http.ResponseWriter, req *http.Request, err error) {
	status, body := MapError(err, correlationID(req))
	writeJSON(w, status, body)
}

func requestViewerID(req *http.Request) string {
	viewer, _ := ViewerFromRequest(req)
	return viewer.ViewerID
}
