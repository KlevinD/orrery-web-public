package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/ontologyreasoning"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	"orrery_v2/internal/retrievalevidence"
)

type OntologyInferenceService interface {
	Evaluate(context.Context, identity.Principal, ontologyreasoning.EvaluateRequest) (ontologyreasoning.ReasoningRun, error)
	ExportSchema(context.Context, identity.Principal, string) (ontologyreasoning.SchemaExport, error)
}

type ontologyInferenceWorkflowService interface {
	CandidateWorkflow(context.Context, identity.Principal, string, ontologyreasoning.WorkflowRequest) (ontologyreasoning.CandidateState, error)
}

type ontologyGroundedContextService interface {
	GroundedContext(context.Context, identity.Principal, ontologyreasoning.GroundedContextRequest) (ontologyreasoning.GroundedContext, error)
}

func (r *Router) handleOntologyInference(w http.ResponseWriter, req *http.Request, parts []string) {
	setOntologyPrivateNoStore(w)
	cid := correlationID(req)
	principal, ok := ontologyPrincipal(req)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Code: ErrorUnauthorized, Message: "authorization is required", CorrelationID: cid})
		return
	}
	if r.ontologyInference == nil {
		writeOntologyViewError(w, ontologyscope.ErrDependencyUnavailable, cid)
		return
	}
	var result any
	var err error
	switch {
	case req.Method == http.MethodPost && len(parts) == 4 && parts[3] == "context":
		reader, ok := r.ontologyInference.(ontologyGroundedContextService)
		if !ok {
			writeOntologyViewError(w, ontologyscope.ErrDependencyUnavailable, cid)
			return
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, 256*1024))
		decoder.DisallowUnknownFields()
		var input ontologyreasoning.GroundedContextRequest
		if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF || ontologyreasoning.ValidateGroundedContextRequest(input) != nil || !validOntologyContextID(input.TenantID) || !validOntologyContextID(input.RootID) {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err = reader.GroundedContext(req.Context(), principal, input)
	case req.Method == http.MethodPost && len(parts) == 4 && (parts[3] == "review" || parts[3] == "plan" || parts[3] == "apply"):
		workflow, ok := r.ontologyInference.(ontologyInferenceWorkflowService)
		if !ok {
			writeOntologyViewError(w, ontologyscope.ErrDependencyUnavailable, cid)
			return
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, 16*1024))
		decoder.DisallowUnknownFields()
		var input ontologyreasoning.WorkflowRequest
		if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF || !validOntologyContextID(input.TenantID) || !validOntologyContextID(input.RunID) || !validOntologyContextID(input.CandidateID) || input.ExpectedRevision < 1 || len(input.SupportHash) > 128 {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err = workflow.CandidateWorkflow(req.Context(), principal, parts[3], input)
	case req.Method == http.MethodPost && len(parts) == 4 && parts[3] == "evaluate":
		decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, 2*1024*1024))
		decoder.DisallowUnknownFields()
		var input ontologyreasoning.EvaluateRequest
		if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF || !validOntologyContextID(input.TenantID) || !validOntologyContextID(input.RootID) {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err = r.ontologyInference.Evaluate(req.Context(), principal, input)
	case req.Method == http.MethodGet && len(parts) == 4 && parts[3] == "schema-export":
		tenant := strings.TrimSpace(req.URL.Query().Get("tenant_id"))
		if !validOntologyContextID(tenant) {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err = r.ontologyInference.ExportSchema(req.Context(), principal, tenant)
	default:
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: ErrorNotFound, Message: "resource not found", CorrelationID: cid})
		return
	}
	if err != nil {
		if errors.Is(err, ontologyreasoning.ErrStateConflict) || errors.Is(err, ontology.ErrVersionConflict) || errors.Is(err, retrievalevidence.ErrInputChanged) {
			writeJSON(w, http.StatusConflict, ErrorResponse{Code: ErrorConflict, Message: "candidate or execution plan changed; evaluate again", CorrelationID: cid})
			return
		}
		if errors.Is(err, ontologyreasoning.ErrInvalidRequest) || errors.Is(err, ontologyreasoning.ErrValidationFailed) {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: ErrorInvalidRequest, Message: err.Error(), CorrelationID: cid})
			return
		}
		if errors.Is(err, ontologyreasoning.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Code: ErrorNotFound, Message: "reasoning run or current candidate unavailable", CorrelationID: cid})
			return
		}
		writeOntologyViewError(w, err, cid)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
