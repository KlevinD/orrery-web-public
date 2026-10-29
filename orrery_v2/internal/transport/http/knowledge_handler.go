package httptransport

import (
	"encoding/json"
	"net/http"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/knowledgeadmin"
)

func (r *Router) handleKnowledgeBases(w http.ResponseWriter, req *http.Request, parts []string) {
	if len(parts) == 2 {
		switch req.Method {
		case http.MethodGet:
			r.handleListKnowledgeBases(w, req)
		case http.MethodPost:
			r.handleCreateKnowledgeBase(w, req)
		default:
			http.NotFound(w, req)
		}
		return
	}
	if len(parts) == 3 {
		switch req.Method {
		case http.MethodGet:
			r.handleGetKnowledgeBase(w, req, parts[2])
		case http.MethodPatch:
			r.handleUpdateKnowledgeBase(w, req, parts[2])
		case http.MethodDelete:
			r.handleDeleteKnowledgeBase(w, req, parts[2])
		default:
			http.NotFound(w, req)
		}
		return
	}
	if len(parts) == 4 && parts[3] == "bindings" {
		switch req.Method {
		case http.MethodPost:
			r.handleBindKnowledgeBase(w, req, parts[2])
		case http.MethodGet:
			r.handleListKnowledgeBindings(w, req, parts[2])
		default:
			http.NotFound(w, req)
		}
		return
	}
	if len(parts) == 5 && parts[3] == "bindings" && req.Method == http.MethodDelete {
		r.handleUnbindKnowledgeBase(w, req, parts[4])
		return
	}
	if len(parts) == 4 && parts[3] == "documents" {
		switch req.Method {
		case http.MethodGet:
			r.handleListKnowledgeDocuments(w, req, parts[2])
		case http.MethodPost:
			r.handleCreateKnowledgeDocument(w, req, parts[2])
		default:
			http.NotFound(w, req)
		}
		return
	}
	if len(parts) == 5 && parts[3] == "documents" && parts[4] == "sync" && req.Method == http.MethodPost {
		r.handleSyncKnowledgeDocuments(w, req, parts[2])
		return
	}
	if len(parts) == 6 && parts[3] == "documents" && parts[5] == "status" && req.Method == http.MethodGet {
		r.handleRefreshKnowledgeDocumentStatus(w, req, parts[2], parts[4])
		return
	}
	if len(parts) == 6 && parts[3] == "documents" && parts[5] == "reprocess" && req.Method == http.MethodPost {
		r.handleReprocessKnowledgeDocument(w, req, parts[2], parts[4])
		return
	}
	if len(parts) == 6 && parts[3] == "documents" && parts[5] == "result" && req.Method == http.MethodGet {
		r.handleGetKnowledgeDocumentProcessingResult(w, req, parts[2], parts[4])
		return
	}
	if len(parts) == 6 && parts[3] == "documents" && parts[5] == "source" {
		r.handleKnowledgeDocumentSource(w, req, parts[2], parts[4])
		return
	}
	if len(parts) == 6 && parts[3] == "documents" && parts[5] == "revisions" && req.Method == http.MethodGet {
		r.handleListKnowledgeDocumentRevisions(w, req, parts[2], parts[4])
		return
	}
	if len(parts) == 7 && parts[3] == "documents" && parts[5] == "revisions" && req.Method == http.MethodPut {
		r.handlePublishKnowledgeDocumentRevision(w, req, parts[2], parts[4], parts[6])
		return
	}
	if len(parts) == 6 && parts[3] == "documents" && parts[5] == "ontology" && req.Method == http.MethodGet {
		r.handleGetKnowledgeDocumentOntology(w, req, parts[2], parts[4])
		return
	}
	if len(parts) == 6 && parts[3] == "documents" && parts[5] == "relations" && req.Method == http.MethodGet {
		r.handleListKnowledgeDocumentRelations(w, req, parts[2], parts[4])
		return
	}
	if len(parts) == 5 && parts[3] == "documents" && req.Method == http.MethodDelete {
		r.handleDeleteKnowledgeDocument(w, req, parts[2], parts[4])
		return
	}
	if len(parts) == 5 && parts[3] == "documents" && req.Method == http.MethodGet {
		r.handleGetKnowledgeDocument(w, req, parts[2], parts[4])
		return
	}
	if len(parts) == 4 && parts[3] == "search" && req.Method == http.MethodPost {
		r.handleKnowledgeBaseSearch(w, req, parts[2])
		return
	}
	if len(parts) == 4 && parts[3] == "reindex" && req.Method == http.MethodPost {
		r.handleReindexKnowledgeBase(w, req, parts[2])
		return
	}
	if len(parts) == 4 && parts[3] == "jobs" && req.Method == http.MethodGet {
		r.handleListKnowledgeJobs(w, req, parts[2])
		return
	}
	http.NotFound(w, req)
}

func (r *Router) handleListKnowledgeBases(w http.ResponseWriter, req *http.Request) {
	if r.knowledgeQuery == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "knowledge service unavailable", CorrelationID: correlationID(req), Retryable: true})
		return
	}
	viewer := r.knowledgeViewerFromRequest(req)
	page, err := r.knowledgeQuery.ListKnowledgeBases(req.Context(), viewer, knowledgeadmin.ListKnowledgeBasesQuery{
		OwnerType: req.URL.Query().Get("owner_type"),
		OwnerID:   req.URL.Query().Get("owner_id"),
	})
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (r *Router) handleGetKnowledgeBase(w http.ResponseWriter, req *http.Request, id string) {
	if r.knowledgeQuery == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "knowledge service unavailable", CorrelationID: correlationID(req), Retryable: true})
		return
	}
	dto, err := r.knowledgeQuery.GetKnowledgeBase(req.Context(), r.knowledgeViewerFromRequest(req), id)
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

func (r *Router) handleCreateKnowledgeBase(w http.ResponseWriter, req *http.Request) {
	if r.knowledgeCommand == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "knowledge service unavailable", CorrelationID: correlationID(req), Retryable: true})
		return
	}
	var cmd knowledgeadmin.CreateKnowledgeBaseCommand
	if err := json.NewDecoder(req.Body).Decode(&cmd); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: ErrorInvalidRequest, Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	dto, err := r.knowledgeCommand.CreateKnowledgeBase(req.Context(), r.knowledgeViewerFromRequest(req), cmd)
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusCreated, dto)
}

func (r *Router) handleUpdateKnowledgeBase(w http.ResponseWriter, req *http.Request, id string) {
	if r.knowledgeCommand == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "knowledge service unavailable", CorrelationID: correlationID(req), Retryable: true})
		return
	}
	var cmd knowledgeadmin.UpdateKnowledgeBaseCommand
	mask, err := decodeJSONWithUpdateMask(req.Body, &cmd)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: ErrorInvalidRequest, Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	cmd.UpdateMask = mask
	cmd.ID = id
	dto, err := r.knowledgeCommand.UpdateKnowledgeBase(req.Context(), r.knowledgeViewerFromRequest(req), cmd)
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

func (r *Router) handleDeleteKnowledgeBase(w http.ResponseWriter, req *http.Request, id string) {
	if r.knowledgeCommand == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "knowledge service unavailable", CorrelationID: correlationID(req), Retryable: true})
		return
	}
	if err := r.knowledgeCommand.DeleteKnowledgeBase(req.Context(), r.knowledgeViewerFromRequest(req), id); err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "deleting"})
}

func (r *Router) handleBindKnowledgeBase(w http.ResponseWriter, req *http.Request, knowledgeBaseID string) {
	var cmd knowledgeadmin.BindKnowledgeBaseCommand
	if err := json.NewDecoder(req.Body).Decode(&cmd); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: ErrorInvalidRequest, Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	cmd.KnowledgeBaseID = knowledgeBaseID
	dto, err := r.knowledgeCommand.BindKnowledgeBase(req.Context(), r.knowledgeViewerFromRequest(req), cmd)
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusCreated, dto)
}

func (r *Router) handleUnbindKnowledgeBase(w http.ResponseWriter, req *http.Request, bindingID string) {
	if err := r.knowledgeCommand.UnbindKnowledgeBase(req.Context(), r.knowledgeViewerFromRequest(req), bindingID); err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "unbound"})
}

func (r *Router) handleListKnowledgeBindings(w http.ResponseWriter, req *http.Request, knowledgeBaseID string) {
	if r.knowledgeQuery == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "knowledge service unavailable", CorrelationID: correlationID(req), Retryable: true})
		return
	}
	page, err := r.knowledgeQuery.ListBindings(req.Context(), r.knowledgeViewerFromRequest(req), knowledgeBaseID)
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (r *Router) handleListKnowledgeDocuments(w http.ResponseWriter, req *http.Request, knowledgeBaseID string) {
	page, err := r.knowledgeQuery.ListDocuments(req.Context(), r.knowledgeViewerFromRequest(req), knowledgeBaseID)
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (r *Router) handleSyncKnowledgeDocuments(w http.ResponseWriter, req *http.Request, knowledgeBaseID string) {
	if r.knowledgeCommand == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "knowledge service unavailable", CorrelationID: correlationID(req), Retryable: true})
		return
	}
	result, err := r.knowledgeCommand.SyncKnowledgeDocuments(req.Context(), r.knowledgeViewerFromRequest(req), knowledgeBaseID)
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (r *Router) handleGetKnowledgeDocument(w http.ResponseWriter, req *http.Request, knowledgeBaseID string, documentID string) {
	if r.knowledgeQuery == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "knowledge service unavailable", CorrelationID: correlationID(req), Retryable: true})
		return
	}
	dto, err := r.knowledgeQuery.GetDocument(req.Context(), r.knowledgeViewerFromRequest(req), knowledgeBaseID, documentID)
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

func (r *Router) handleCreateKnowledgeDocument(w http.ResponseWriter, req *http.Request, knowledgeBaseID string) {
	if strings.HasPrefix(req.Header.Get("Content-Type"), "multipart/form-data") {
		r.handleUploadKnowledgeDocument(w, req, knowledgeBaseID)
		return
	}
	var cmd knowledgeadmin.CreateDocumentCommand
	if err := json.NewDecoder(req.Body).Decode(&cmd); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: ErrorInvalidRequest, Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	cmd.KnowledgeBaseID = knowledgeBaseID
	dto, err := r.knowledgeCommand.CreateDocumentMetadata(req.Context(), r.knowledgeViewerFromRequest(req), cmd)
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusAccepted, dto)
}

func (r *Router) handleUploadKnowledgeDocument(w http.ResponseWriter, req *http.Request, knowledgeBaseID string) {
	if r.knowledgeCommand == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "knowledge service unavailable", CorrelationID: correlationID(req), Retryable: true})
		return
	}
	file, header, err := req.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: ErrorInvalidRequest, Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	defer file.Close()
	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	dto, err := r.knowledgeCommand.UploadDocument(req.Context(), r.knowledgeViewerFromRequest(req), knowledgeadmin.UploadDocumentCommand{
		KnowledgeBaseID: knowledgeBaseID,
		Filename:        header.Filename,
		ContentType:     contentType,
		Reader:          file,
	})
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusAccepted, dto)
}

func (r *Router) handleDeleteKnowledgeDocument(w http.ResponseWriter, req *http.Request, knowledgeBaseID string, documentID string) {
	if r.knowledgeCommand == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "knowledge service unavailable", CorrelationID: correlationID(req), Retryable: true})
		return
	}
	if err := r.knowledgeCommand.DeleteDocument(req.Context(), r.knowledgeViewerFromRequest(req), knowledgeadmin.DeleteDocumentCommand{
		KnowledgeBaseID: knowledgeBaseID,
		DocumentID:      documentID,
	}); err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "deleting"})
}

func (r *Router) handleRefreshKnowledgeDocumentStatus(w http.ResponseWriter, req *http.Request, knowledgeBaseID string, documentID string) {
	if r.knowledgeCommand == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "knowledge service unavailable", CorrelationID: correlationID(req), Retryable: true})
		return
	}
	dto, err := r.knowledgeCommand.RefreshDocumentStatus(req.Context(), r.knowledgeViewerFromRequest(req), knowledgeadmin.DocumentActionCommand{
		KnowledgeBaseID: knowledgeBaseID,
		DocumentID:      documentID,
	})
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

func (r *Router) handleReprocessKnowledgeDocument(w http.ResponseWriter, req *http.Request, knowledgeBaseID string, documentID string) {
	if r.knowledgeCommand == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "knowledge service unavailable", CorrelationID: correlationID(req), Retryable: true})
		return
	}
	dto, err := r.knowledgeCommand.ReprocessDocument(req.Context(), r.knowledgeViewerFromRequest(req), knowledgeadmin.DocumentActionCommand{
		KnowledgeBaseID: knowledgeBaseID,
		DocumentID:      documentID,
	})
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusAccepted, dto)
}

func (r *Router) handleGetKnowledgeDocumentProcessingResult(w http.ResponseWriter, req *http.Request, knowledgeBaseID string, documentID string) {
	if r.knowledgeCommand == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "knowledge service unavailable", CorrelationID: correlationID(req), Retryable: true})
		return
	}
	dto, err := r.knowledgeCommand.GetDocumentProcessingResult(req.Context(), r.knowledgeViewerFromRequest(req), knowledgeadmin.DocumentProcessingResultCommand{
		KnowledgeBaseID: knowledgeBaseID,
		DocumentID:      documentID,
		Pages:           req.URL.Query().Get("pages"),
	})
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

func (r *Router) handleReindexKnowledgeBase(w http.ResponseWriter, req *http.Request, knowledgeBaseID string) {
	if r.knowledgeCommand == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "knowledge service unavailable", CorrelationID: correlationID(req), Retryable: true})
		return
	}
	dto, err := r.knowledgeCommand.CreateReindexJob(req.Context(), r.knowledgeViewerFromRequest(req), knowledgeadmin.ReindexKnowledgeBaseCommand{KnowledgeBaseID: knowledgeBaseID})
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusAccepted, dto)
}

func (r *Router) handleListKnowledgeJobs(w http.ResponseWriter, req *http.Request, knowledgeBaseID string) {
	if r.knowledgeQuery == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "knowledge service unavailable", CorrelationID: correlationID(req), Retryable: true})
		return
	}
	page, err := r.knowledgeQuery.ListJobs(req.Context(), r.knowledgeViewerFromRequest(req), knowledgeBaseID)
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (r *Router) handleKnowledgeJob(w http.ResponseWriter, req *http.Request, id string) {
	if r.knowledgeQuery == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "knowledge service unavailable", CorrelationID: correlationID(req), Retryable: true})
		return
	}
	dto, err := r.knowledgeQuery.GetKnowledgeJob(req.Context(), r.knowledgeViewerFromRequest(req), id)
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

func (r *Router) handleKnowledgeSearch(w http.ResponseWriter, req *http.Request) {
	r.handleSearchKnowledge(w, req, "")
}

func (r *Router) handleKnowledgeBaseSearch(w http.ResponseWriter, req *http.Request, knowledgeBaseID string) {
	r.handleSearchKnowledge(w, req, knowledgeBaseID)
}

func (r *Router) handleSearchKnowledge(w http.ResponseWriter, req *http.Request, knowledgeBaseID string) {
	if r.knowledgeSearch == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "knowledge service unavailable", CorrelationID: correlationID(req), Retryable: true})
		return
	}
	var cmd knowledgeadmin.SearchKnowledgeCommand
	if err := json.NewDecoder(req.Body).Decode(&cmd); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: ErrorInvalidRequest, Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	if knowledgeBaseID != "" {
		cmd.KnowledgeBaseIDs = []string{knowledgeBaseID}
	}
	page, err := r.knowledgeSearch.SearchKnowledge(req.Context(), r.knowledgeViewerFromRequest(req), cmd)
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func knowledgeViewerFromRequest(req *http.Request) knowledgeadmin.ViewerRef {
	viewer, _ := ViewerFromRequest(req)
	result := knowledgeadmin.ViewerRef{
		ViewerID:        viewer.ViewerID,
		PermissionScope: viewer.PermissionScope,
		RequestID:       correlationID(req),
		IdempotencyKey:  req.Header.Get("Idempotency-Key"),
	}
	if internalViewerAuthorizationMatches(req) {
		result.AgentIDs = knowledgeIdentityIDs(req, "X-Agent-ID", "X-Agent-IDs")
		result.HumanIDs = knowledgeIdentityIDs(req, "X-Human-ID", "X-Human-IDs")
		result.OrganizationIDs = knowledgeIdentityIDs(req, "X-Organization-ID", "X-Organization-IDs")
		result.PositionIDs = knowledgeIdentityIDs(req, "X-Position-ID", "X-Position-IDs")
		result.RoleIDs = knowledgeIdentityIDs(req, "X-Role-ID", "X-Role-IDs")
	}
	return result
}

func (r *Router) knowledgeViewerFromRequest(req *http.Request) knowledgeadmin.ViewerRef {
	viewer := knowledgeViewerFromRequest(req)
	if r == nil || r.knowledgeProfiles == nil || req == nil {
		return viewer
	}
	profileID := ""
	if principal, ok := identity.PrincipalFromContext(req.Context()); ok {
		profileID = principal.AgentProfileID
	} else if internalViewerAuthorizationMatches(req) {
		profileID = strings.TrimSpace(req.Header.Get("X-Agent-Profile-ID"))
	}
	if profileID == "" {
		return viewer
	}
	profile, err := r.knowledgeProfiles.GetProfile(req.Context(), agentcore.AgentProfileID(profileID))
	if err != nil {
		return viewer
	}
	return enrichKnowledgeViewerWithProfile(viewer, profile)
}

func enrichKnowledgeViewerWithProfile(viewer knowledgeadmin.ViewerRef, profile agentcore.AgentProfile) knowledgeadmin.ViewerRef {
	profileID := strings.TrimSpace(profile.ID.String())
	if profileID == "" {
		return viewer
	}
	viewer.AgentIDs = appendUniqueKnowledgeIDs(viewer.AgentIDs, []string{profileID})
	if profile.AgentKind == agentcore.AgentKindHuman {
		viewer.HumanIDs = appendUniqueKnowledgeIDs(viewer.HumanIDs, []string{profileID})
	}
	if orgID := strings.TrimSpace(string(profile.OrgIdentity.OrganizationRef.ID)); orgID != "" {
		viewer.OrganizationIDs = appendUniqueKnowledgeIDs(viewer.OrganizationIDs, []string{orgID})
	}
	if positionID := strings.TrimSpace(string(profile.OrgIdentity.PositionRef.ID)); positionID != "" {
		viewer.PositionIDs = appendUniqueKnowledgeIDs(viewer.PositionIDs, []string{positionID})
	}
	roleIDs := make([]string, 0, len(profile.OrgIdentity.RoleRefs))
	for _, ref := range profile.OrgIdentity.RoleRefs {
		if id := strings.TrimSpace(string(ref.ID)); id != "" {
			roleIDs = append(roleIDs, id)
		}
	}
	viewer.RoleIDs = appendUniqueKnowledgeIDs(viewer.RoleIDs, roleIDs)
	return viewer
}

func knowledgeIdentityIDs(req *http.Request, singleHeader, listHeader string) []string {
	if req == nil {
		return nil
	}
	return appendUniqueKnowledgeIDs(splitHeader(req.Header.Get(singleHeader)), splitHeader(req.Header.Get(listHeader)))
}

func appendUniqueKnowledgeIDs(groups ...[]string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, group := range groups {
		for _, id := range group {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}
