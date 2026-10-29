package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	resourceadmin "orrery_v2/internal/app/resourceadmin"
)

func (r *Router) handleResources(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, err := resourceViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if r.resourceQuery == nil {
		status, body := MapError(errServiceDependency(), cid)
		writeJSON(w, status, body)
		return
	}
	if strings.TrimSpace(req.URL.Query().Get("agent_id")) != "" {
		status, body := invalidRequest(cid, "agent_id", "agent_id is not a resource list filter; use bound_agent_id for agent bindings")
		writeJSON(w, status, body)
		return
	}
	limit, offset := listPage(req)
	enabled, enabledSet := parseOptionalBool(req.URL.Query().Get("enabled"))
	var enabledFilter *bool
	if enabledSet {
		enabledFilter = &enabled
	}
	view, err := r.resourceQuery.ListResourcesFiltered(req.Context(), viewer, resourceadmin.ListResourcesRequest{
		Search:          req.URL.Query().Get("search"),
		Kind:            resourceKind(req.URL.Path),
		Category:        req.URL.Query().Get("category"),
		RiskLevel:       req.URL.Query().Get("risk_level"),
		Enabled:         enabledFilter,
		ResourceID:      req.URL.Query().Get("resource_id"),
		BoundAgentID:    req.URL.Query().Get("bound_agent_id"),
		RequiredToolID:  req.URL.Query().Get("required_tool_id"),
		IncludeUsage:    parseBoolQuery(req.URL.Query().Get("include_usage")),
		IncludeBindings: parseBoolQuery(req.URL.Query().Get("include_bindings")),
		Limit:           limit,
		Offset:          offset,
	})
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (r *Router) handleGetMemoryProfile(w http.ResponseWriter, req *http.Request, profileID string) {
	cid := correlationID(req)
	viewer, err := resourceViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if r.resourceQuery == nil {
		status, body := MapError(errServiceDependency(), cid)
		writeJSON(w, status, body)
		return
	}
	profile, err := r.resourceQuery.GetMemoryProfile(req.Context(), viewer, profileID)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

type memoryProfileUpdateService interface {
	UpdateMemoryProfile(ctx context.Context, viewer resourceadmin.ViewerRef, profileID string, req resourceadmin.UpdateMemoryProfileRequest) (resourceadmin.MemoryProfileDTO, error)
}

func (r *Router) handleUpdateMemoryProfile(w http.ResponseWriter, req *http.Request, profileID string) {
	cid := correlationID(req)
	viewer, err := resourceViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	service, ok := r.resourceQuery.(memoryProfileUpdateService)
	if !ok {
		status, body := MapError(errServiceDependency(), cid)
		writeJSON(w, status, body)
		return
	}
	var body resourceadmin.UpdateMemoryProfileRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "invalid JSON body")
		writeJSON(w, status, response)
		return
	}
	profile, err := service.UpdateMemoryProfile(req.Context(), viewer, profileID, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func (r *Router) handleGetPersonalMemoryProfile(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, err := resourceViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if r.resourceQuery == nil {
		status, body := MapError(errServiceDependency(), cid)
		writeJSON(w, status, body)
		return
	}
	profile, err := r.resourceQuery.GetPersonalMemoryProfileByAgent(req.Context(), viewer, req.URL.Query().Get("agent_id"))
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func (r *Router) handleBuildMemoryDigest(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, err := resourceViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if r.resourceQuery == nil {
		status, body := MapError(errServiceDependency(), cid)
		writeJSON(w, status, body)
		return
	}
	var body resourceadmin.BuildMemoryDigestRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "invalid JSON body")
		writeJSON(w, status, response)
		return
	}
	digest, err := r.resourceQuery.BuildMemoryDigest(req.Context(), viewer, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, digest)
}

func (r *Router) handleDiscoverMCPServers(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, err := resourceViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if r.resourceQuery == nil {
		status, body := MapError(errServiceDependency(), cid)
		writeJSON(w, status, body)
		return
	}
	limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
	response, err := r.resourceQuery.DiscoverMCPServers(req.Context(), viewer, resourceadmin.DiscoverMCPRequest{
		Query: req.URL.Query().Get("query"),
		Limit: limit,
	})
	if err != nil {
		if errors.Is(err, resourceadmin.ErrServiceDependency) {
			status, body := failedPrecondition(cid, "discovery", "not_configured", "mcp server discovery is not configured")
			writeJSON(w, status, body)
			return
		}
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (r *Router) handleGetMCPServer(w http.ResponseWriter, req *http.Request, toolID string) {
	cid := correlationID(req)
	viewer, err := resourceViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if r.resourceQuery == nil {
		status, body := MapError(errServiceDependency(), cid)
		writeJSON(w, status, body)
		return
	}
	server, err := r.resourceQuery.GetMCPServer(req.Context(), viewer, toolID)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, server)
}

func (r *Router) handleGetSkillDetail(w http.ResponseWriter, req *http.Request, skillID string) {
	cid := correlationID(req)
	viewer, err := resourceViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if r.resourceQuery == nil {
		status, body := MapError(errServiceDependency(), cid)
		writeJSON(w, status, body)
		return
	}
	detail, err := r.resourceQuery.GetSkillDetail(req.Context(), viewer, skillID)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func resourceKind(path string) string {
	switch path {
	case "/api/resources/tools":
		return "tool"
	case "/api/resources/skills":
		return "skill"
	case "/api/resources/mcp-servers":
		return "mcp"
	case "/api/resources/memory-profiles":
		return "memory_profile"
	default:
		return ""
	}
}

func parseOptionalBool(raw string) (bool, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return false, false
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, false
	}
	return parsed, true
}

func parseBoolQuery(raw string) bool {
	parsed, _ := strconv.ParseBool(strings.TrimSpace(raw))
	return parsed
}
