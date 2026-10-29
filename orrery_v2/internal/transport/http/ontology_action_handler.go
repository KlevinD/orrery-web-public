package httptransport

import (
	"context"
	"net/http"
	"strings"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type OntologyActionRefDTO struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type OntologyActionInvokeRequest struct {
	TenantID        string                 `json:"-"`
	ExecutionMode   string                 `json:"execution_mode,omitempty"`
	RuntimeKind     string                 `json:"runtime_kind"`
	RuntimeID       string                 `json:"runtime_id"`
	Purpose         string                 `json:"purpose,omitempty"`
	ObjectRefs      []OntologyActionRefDTO `json:"object_refs"`
	RequestedFields []string               `json:"requested_fields,omitempty"`
	ArgsHash        string                 `json:"args_hash"`
	ApprovalID      string                 `json:"approval_id,omitempty"`
	HILTokenID      string                 `json:"hil_token_id,omitempty"`
	EvidenceRefs    []OntologyActionRefDTO `json:"evidence_refs,omitempty"`
}

type OntologyActionRunDTO struct {
	ID               string                 `json:"id"`
	ActionTypeID     string                 `json:"action_type_id"`
	Status           string                 `json:"status"`
	RiskLevel        string                 `json:"risk_level"`
	ExecutionMode    string                 `json:"execution_mode"`
	Runtime          OntologyActionRefDTO   `json:"runtime"`
	ObjectRefs       []OntologyActionRefDTO `json:"object_refs,omitempty"`
	PolicyDecisionID string                 `json:"policy_decision_id,omitempty"`
	ApprovalID       string                 `json:"approval_id,omitempty"`
	ErrorCode        string                 `json:"error_code,omitempty"`
	CreatedAt        string                 `json:"created_at"`
}

type OntologyActionService interface {
	InvokeAction(context.Context, identity.Principal, string, OntologyActionInvokeRequest) (OntologyActionRunDTO, error)
}

func (r *Router) handleOntologyActions(w http.ResponseWriter, req *http.Request, parts []string) {
	setOntologyPrivateNoStore(w)
	cid := correlationID(req)
	principal, ok := ontologyPrincipal(req)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Code: ErrorUnauthorized, Message: "authorization is required", CorrelationID: cid})
		return
	}
	if req.Method != http.MethodPost || len(parts) != 5 || parts[4] != "invoke" || !validOntologyContextID(parts[3]) {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: ErrorNotFound, Message: "resource not found", CorrelationID: cid})
		return
	}
	if !ontologyMutationOriginAllowed(req) {
		writeJSON(w, http.StatusForbidden, ErrorResponse{Code: ErrorForbidden, Message: "permission denied", CorrelationID: cid})
		return
	}
	if r.ontologyActions == nil {
		writeOntologyViewError(w, ontologyscope.ErrDependencyUnavailable, cid)
		return
	}
	tenantID := strings.TrimSpace(req.URL.Query().Get("tenant_id"))
	if !validOntologyContextID(tenantID) {
		writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
		return
	}
	var body OntologyActionInvokeRequest
	if err := decodeOntologyMutationBody(req, &body); err != nil {
		writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
		return
	}
	body.TenantID = tenantID
	result, err := r.ontologyActions.InvokeAction(req.Context(), principal, parts[3], body)
	if err != nil {
		writeOntologyViewError(w, err, cid)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}
