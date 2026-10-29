package httptransport

import (
	"context"
	"net/http"
	"strings"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type OntologyReliabilityIncidentDTO struct {
	ID              string `json:"id"`
	TenantID        string `json:"tenant_id"`
	Fingerprint     string `json:"fingerprint"`
	Source          string `json:"source"`
	Component       string `json:"component"`
	Category        string `json:"category"`
	Severity        string `json:"severity"`
	Status          string `json:"status"`
	ErrorCode       string `json:"error_code"`
	RedactedSummary string `json:"redacted_summary"`
	Retryable       bool   `json:"retryable"`
	OccurrenceCount int64  `json:"occurrence_count"`
	Version         int64  `json:"version"`
	FirstSeenAt     string `json:"first_seen_at"`
	LastSeenAt      string `json:"last_seen_at"`
	UpdatedAt       string `json:"updated_at"`
}

type OntologyReliabilityRepairTargetDTO struct {
	ObservationID         string `json:"observation_id"`
	Kind                  string `json:"kind"`
	AuthorizationTenantID string `json:"authorization_tenant_id"`
	MissionID             string `json:"mission_id"`
	RunID                 string `json:"run_id"`
	OccurredAt            string `json:"occurred_at"`
}

type OntologyReliabilityRepairRuleDTO struct {
	ID                   string                `json:"id"`
	DiagnosisCaseID      string                `json:"diagnosis_case_id,omitempty"`
	Strategy             string                `json:"strategy"`
	ActionTypeID         string                `json:"action_type_id"`
	RiskLevel            string                `json:"risk_level"`
	Status               string                `json:"status"`
	Confidence           float64               `json:"confidence"`
	SupportCount         int64                 `json:"support_count"`
	SuccessCount         int64                 `json:"success_count"`
	FailureCount         int64                 `json:"failure_count"`
	Reversible           bool                  `json:"reversible"`
	Idempotent           bool                  `json:"idempotent"`
	AutoApply            bool                  `json:"auto_apply"`
	Version              int64                 `json:"version"`
	ArgsHash             string                `json:"args_hash"`
	DryRunReady          bool                  `json:"dry_run_ready"`
	ActivationReady      bool                  `json:"activation_ready"`
	ActivationBlockers   []string              `json:"activation_blockers"`
	ActivationDecisionID string                `json:"activation_decision_id,omitempty"`
	ActivatedBy          *OntologyActionRefDTO `json:"activated_by,omitempty"`
	ActivatedAt          string                `json:"activated_at,omitempty"`
	ApplyReady           bool                  `json:"apply_ready"`
	ApplyBlockers        []string              `json:"apply_blockers"`
	LastVerifiedAt       string                `json:"last_verified_at,omitempty"`
	ExpiresAt            string                `json:"expires_at,omitempty"`
	UpdatedAt            string                `json:"updated_at"`
}

type OntologyReliabilityEvidenceDTO struct {
	ID         string  `json:"id"`
	Label      string  `json:"label"`
	Status     string  `json:"status,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
	UpdatedAt  string  `json:"updated_at,omitempty"`
}

type OntologyReliabilityDiagnosisDTO struct {
	ID                string                `json:"id"`
	IncidentID        string                `json:"incident_id"`
	IncidentVersion   int64                 `json:"incident_version"`
	RootCauseCategory string                `json:"root_cause_category"`
	RedactedSummary   string                `json:"redacted_summary"`
	Confidence        float64               `json:"confidence"`
	Status            string                `json:"status"`
	EvidenceIDs       []string              `json:"evidence_ids"`
	ProposedBy        OntologyActionRefDTO  `json:"proposed_by"`
	ReviewedBy        *OntologyActionRefDTO `json:"reviewed_by,omitempty"`
	ReviewReason      string                `json:"review_reason,omitempty"`
	Version           int64                 `json:"version"`
	CreatedAt         string                `json:"created_at"`
	UpdatedAt         string                `json:"updated_at"`
}

type OntologyReliabilityPreflightDTO struct {
	EvidenceID          string `json:"evidence_id"`
	ActionRunID         string `json:"action_run_id"`
	IncidentID          string `json:"incident_id"`
	IncidentVersion     int64  `json:"incident_version"`
	RuleID              string `json:"rule_id"`
	RuleVersion         int64  `json:"rule_version"`
	Strategy            string `json:"strategy"`
	ArgsHash            string `json:"args_hash"`
	TargetObservationID string `json:"target_observation_id,omitempty"`
	TargetKind          string `json:"target_kind,omitempty"`
	TargetRunID         string `json:"target_run_id,omitempty"`
	Passed              bool   `json:"passed"`
	Current             bool   `json:"current"`
	CheckedAt           string `json:"checked_at"`
	ExpiresAt           string `json:"expires_at"`
}

type OntologyReliabilityExecutionDTO struct {
	ActionRunID         string `json:"action_run_id"`
	IncidentID          string `json:"incident_id"`
	RuleID              string `json:"rule_id"`
	PreflightEvidenceID string `json:"preflight_evidence_id"`
	Strategy            string `json:"strategy"`
	OriginalRunID       string `json:"original_run_id"`
	RetryRunID          string `json:"retry_run_id"`
	Status              string `json:"status"`
	FailureCode         string `json:"failure_code,omitempty"`
	Version             int64  `json:"version"`
	StartedAt           string `json:"started_at"`
	DeadlineAt          string `json:"deadline_at"`
	UpdatedAt           string `json:"updated_at"`
	CompletedAt         string `json:"completed_at,omitempty"`
}

type OntologyReliabilityAuthorizationDTO struct {
	ID            string               `json:"id"`
	ExecutionMode string               `json:"execution_mode"`
	RuleID        string               `json:"rule_id"`
	Status        string               `json:"status"`
	RouteMode     string               `json:"route_mode"`
	Requester     OntologyActionRefDTO `json:"requester"`
	ArgsHash      string               `json:"args_hash"`
	Purpose       string               `json:"purpose,omitempty"`
	Current       bool                 `json:"current"`
	CreatedAt     string               `json:"created_at"`
	ExpiresAt     string               `json:"expires_at,omitempty"`
}

type OntologyReliabilityActivationReviewDTO struct {
	ID                  string               `json:"id"`
	RuleID              string               `json:"rule_id"`
	RuleVersion         int64                `json:"rule_version"`
	IncidentID          string               `json:"incident_id"`
	DiagnosisCaseID     string               `json:"diagnosis_case_id"`
	PreflightEvidenceID string               `json:"preflight_evidence_id"`
	Status              string               `json:"status"`
	RouteMode           string               `json:"route_mode"`
	Requester           OntologyActionRefDTO `json:"requester"`
	ArgsHash            string               `json:"args_hash"`
	Purpose             string               `json:"purpose,omitempty"`
	Current             bool                 `json:"current"`
	Activated           bool                 `json:"activated"`
	CreatedAt           string               `json:"created_at"`
	ExpiresAt           string               `json:"expires_at,omitempty"`
}

type OntologyReliabilityDiagnosticDTO struct {
	ID            string            `json:"id"`
	Source        string            `json:"source"`
	OccurredAt    string            `json:"occurred_at"`
	CorrelationID string            `json:"correlation_id,omitempty"`
	Details       map[string]string `json:"details"`
}

type OntologyReliabilityIncidentDetailDTO struct {
	AutomaticRepair    *OntologyReliabilityAutomaticRepairDTO   `json:"automatic_repair,omitempty"`
	Diagnostics        []OntologyReliabilityDiagnosticDTO       `json:"diagnostics,omitempty"`
	DiagnosticsWarning string                                   `json:"diagnostics_warning,omitempty"`
	Incident           OntologyReliabilityIncidentDTO           `json:"incident"`
	Target             *OntologyReliabilityRepairTargetDTO      `json:"target,omitempty"`
	AvailableEvidence  []OntologyReliabilityEvidenceDTO         `json:"available_evidence"`
	Diagnoses          []OntologyReliabilityDiagnosisDTO        `json:"diagnoses"`
	Rules              []OntologyReliabilityRepairRuleDTO       `json:"rules"`
	Preflights         []OntologyReliabilityPreflightDTO        `json:"preflights"`
	Executions         []OntologyReliabilityExecutionDTO        `json:"executions"`
	ActivationReviews  []OntologyReliabilityActivationReviewDTO `json:"activation_reviews"`
	Authorizations     []OntologyReliabilityAuthorizationDTO    `json:"authorizations"`
}

type OntologyReliabilityAutomaticRepairDTO struct {
	JobID       string   `json:"job_id"`
	Status      string   `json:"status"`
	Summary     string   `json:"summary"`
	Evidence    []string `json:"evidence"`
	RepairRunID string   `json:"repair_run_id,omitempty"`
	UpdatedAt   string   `json:"updated_at"`
	Verified    bool     `json:"verified"`
	Current     bool     `json:"current"`
}

type OntologyReliabilityDiagnosisCreateRequest struct {
	RootCauseCategory string   `json:"root_cause_category"`
	RedactedSummary   string   `json:"redacted_summary"`
	Confidence        float64  `json:"confidence"`
	EvidenceIDs       []string `json:"evidence_ids"`
	IdempotencyKey    string   `json:"-"`
}

type OntologyReliabilityDiagnosisDecisionRequest struct {
	Decision       string `json:"decision"`
	Reason         string `json:"reason"`
	IdempotencyKey string `json:"-"`
}

type OntologyReliabilityRuleProposalRequest struct {
	Strategy       string `json:"strategy"`
	Reversible     bool   `json:"reversible"`
	Idempotent     bool   `json:"idempotent"`
	IdempotencyKey string `json:"-"`
}

type OntologyReliabilityAuthorizationRequest struct {
	ExecutionMode  string `json:"execution_mode"`
	Purpose        string `json:"purpose,omitempty"`
	IdempotencyKey string `json:"-"`
}

type OntologyReliabilityActivationReviewRequest struct {
	Purpose        string `json:"purpose,omitempty"`
	IdempotencyKey string `json:"-"`
}

type OntologyReliabilityAuthorizationDecisionRequest struct {
	Decision       string `json:"decision"`
	Reason         string `json:"reason,omitempty"`
	IdempotencyKey string `json:"-"`
}

type OntologyReliabilityAuthorizationResolutionDTO struct {
	Authorization OntologyReliabilityAuthorizationDTO `json:"authorization"`
	Effect        string                              `json:"effect"`
}

type OntologyReliabilityActivationReviewResolutionDTO struct {
	Review OntologyReliabilityActivationReviewDTO `json:"review"`
	Effect string                                 `json:"effect"`
}

type OntologyReliabilityActivationRequest struct {
	IdempotencyKey string `json:"-"`
}

type OntologyReliabilityInvokeRequest struct {
	IdempotencyKey string `json:"-"`
}

type OntologyReliabilityService interface {
	GetIncident(context.Context, identity.Principal, string, string) (OntologyReliabilityIncidentDetailDTO, error)
	CreateDiagnosis(context.Context, identity.Principal, string, string, OntologyReliabilityDiagnosisCreateRequest) (OntologyReliabilityDiagnosisDTO, error)
	DecideDiagnosis(context.Context, identity.Principal, string, string, string, OntologyReliabilityDiagnosisDecisionRequest) (OntologyReliabilityDiagnosisDTO, error)
	ProposeRepairRule(context.Context, identity.Principal, string, string, string, OntologyReliabilityRuleProposalRequest) (OntologyReliabilityRepairRuleDTO, error)
	RequestRuleActivationReview(context.Context, identity.Principal, string, string, OntologyReliabilityActivationReviewRequest) (OntologyReliabilityActivationReviewDTO, error)
	ResolveRuleActivationReview(context.Context, identity.Principal, string, string, string, OntologyReliabilityAuthorizationDecisionRequest) (OntologyReliabilityActivationReviewResolutionDTO, error)
	ActivateRepairRule(context.Context, identity.Principal, string, string, string, OntologyReliabilityActivationRequest) (OntologyReliabilityRepairRuleDTO, error)
	RequestRepairAuthorization(context.Context, identity.Principal, string, string, string, OntologyReliabilityAuthorizationRequest) (OntologyReliabilityAuthorizationDTO, error)
	ResolveRepairAuthorization(context.Context, identity.Principal, string, string, string, string, OntologyReliabilityAuthorizationDecisionRequest) (OntologyReliabilityAuthorizationResolutionDTO, error)
	InvokeAuthorizedRepair(context.Context, identity.Principal, string, string, string, string, OntologyReliabilityInvokeRequest) (OntologyActionRunDTO, error)
}

func (r *Router) handleOntologyReliability(w http.ResponseWriter, req *http.Request, parts []string) {
	setOntologyPrivateNoStore(w)
	cid := correlationID(req)
	principal, ok := ontologyPrincipal(req)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Code: ErrorUnauthorized, Message: "authorization is required", CorrelationID: cid})
		return
	}
	if r.ontologyReliability == nil {
		writeOntologyViewError(w, ontologyscope.ErrDependencyUnavailable, cid)
		return
	}
	tenantID := strings.TrimSpace(req.URL.Query().Get("tenant_id"))
	if !validOntologyContextID(tenantID) {
		writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
		return
	}

	switch {
	case req.Method == http.MethodGet && len(parts) == 5 && parts[3] == "incidents" && validOntologyContextID(parts[4]):
		result, err := r.ontologyReliability.GetIncident(req.Context(), principal, tenantID, parts[4])
		writeOntologyContextResult(w, result, err, cid)
	case req.Method == http.MethodPost && len(parts) == 6 && parts[3] == "incidents" && parts[5] == "diagnoses" && validOntologyContextID(parts[4]):
		if !ontologyMutationOriginAllowed(req) {
			writeJSON(w, http.StatusForbidden, ErrorResponse{Code: ErrorForbidden, Message: "permission denied", CorrelationID: cid})
			return
		}
		var body OntologyReliabilityDiagnosisCreateRequest
		if err := decodeOntologyMutationBody(req, &body); err != nil || idempotencyKey(req) == "" {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		body.IdempotencyKey = idempotencyKey(req)
		result, err := r.ontologyReliability.CreateDiagnosis(req.Context(), principal, tenantID, parts[4], body)
		if err != nil {
			writeOntologyViewError(w, err, cid)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	case req.Method == http.MethodPost && len(parts) == 8 && parts[3] == "incidents" && parts[5] == "diagnoses" && parts[7] == "decision" && validOntologyContextID(parts[4]) && validOntologyContextID(parts[6]):
		if !ontologyMutationOriginAllowed(req) {
			writeJSON(w, http.StatusForbidden, ErrorResponse{Code: ErrorForbidden, Message: "permission denied", CorrelationID: cid})
			return
		}
		var body OntologyReliabilityDiagnosisDecisionRequest
		if err := decodeOntologyMutationBody(req, &body); err != nil || idempotencyKey(req) == "" {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		body.IdempotencyKey = idempotencyKey(req)
		result, err := r.ontologyReliability.DecideDiagnosis(req.Context(), principal, tenantID, parts[4], parts[6], body)
		if err != nil {
			writeOntologyViewError(w, err, cid)
			return
		}
		writeJSON(w, http.StatusOK, result)
	case req.Method == http.MethodPost && len(parts) == 8 && parts[3] == "incidents" && parts[5] == "diagnoses" && parts[7] == "rules" && validOntologyContextID(parts[4]) && validOntologyContextID(parts[6]):
		if !ontologyMutationOriginAllowed(req) {
			writeJSON(w, http.StatusForbidden, ErrorResponse{Code: ErrorForbidden, Message: "permission denied", CorrelationID: cid})
			return
		}
		var body OntologyReliabilityRuleProposalRequest
		if err := decodeOntologyMutationBody(req, &body); err != nil || idempotencyKey(req) == "" {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		body.IdempotencyKey = idempotencyKey(req)
		result, err := r.ontologyReliability.ProposeRepairRule(req.Context(), principal, tenantID, parts[4], parts[6], body)
		if err != nil {
			writeOntologyViewError(w, err, cid)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	case req.Method == http.MethodPost && len(parts) == 6 && parts[3] == "rules" && parts[5] == "activation-reviews" && validOntologyContextID(parts[4]):
		if !ontologyMutationOriginAllowed(req) {
			writeJSON(w, http.StatusForbidden, ErrorResponse{Code: ErrorForbidden, Message: "permission denied", CorrelationID: cid})
			return
		}
		var body OntologyReliabilityActivationReviewRequest
		if err := decodeOntologyMutationBody(req, &body); err != nil || idempotencyKey(req) == "" {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		body.IdempotencyKey = idempotencyKey(req)
		result, err := r.ontologyReliability.RequestRuleActivationReview(req.Context(), principal, tenantID, parts[4], body)
		if err != nil {
			writeOntologyViewError(w, err, cid)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	case req.Method == http.MethodPost && len(parts) == 8 && parts[3] == "rules" && parts[5] == "activation-reviews" && parts[7] == "decision" && validOntologyContextID(parts[4]) && validOntologyContextID(parts[6]):
		if !ontologyMutationOriginAllowed(req) {
			writeJSON(w, http.StatusForbidden, ErrorResponse{Code: ErrorForbidden, Message: "permission denied", CorrelationID: cid})
			return
		}
		var body OntologyReliabilityAuthorizationDecisionRequest
		if err := decodeOntologyMutationBody(req, &body); err != nil || idempotencyKey(req) == "" {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		body.IdempotencyKey = idempotencyKey(req)
		result, err := r.ontologyReliability.ResolveRuleActivationReview(req.Context(), principal, tenantID, parts[4], parts[6], body)
		if err != nil {
			writeOntologyViewError(w, err, cid)
			return
		}
		writeJSON(w, http.StatusOK, result)
	case req.Method == http.MethodPost && len(parts) == 8 && parts[3] == "rules" && parts[5] == "activation-reviews" && parts[7] == "activate" && validOntologyContextID(parts[4]) && validOntologyContextID(parts[6]):
		if !ontologyMutationOriginAllowed(req) {
			writeJSON(w, http.StatusForbidden, ErrorResponse{Code: ErrorForbidden, Message: "permission denied", CorrelationID: cid})
			return
		}
		if idempotencyKey(req) == "" {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		var body struct{}
		if err := decodeOntologyMutationBody(req, &body); err != nil {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologyReliability.ActivateRepairRule(req.Context(), principal, tenantID, parts[4], parts[6], OntologyReliabilityActivationRequest{IdempotencyKey: idempotencyKey(req)})
		if err != nil {
			writeOntologyViewError(w, err, cid)
			return
		}
		writeJSON(w, http.StatusOK, result)
	case req.Method == http.MethodPost && len(parts) == 8 && parts[3] == "incidents" && parts[5] == "rules" && parts[7] == "authorizations" && validOntologyContextID(parts[4]) && validOntologyContextID(parts[6]):
		if !ontologyMutationOriginAllowed(req) {
			writeJSON(w, http.StatusForbidden, ErrorResponse{Code: ErrorForbidden, Message: "permission denied", CorrelationID: cid})
			return
		}
		var body OntologyReliabilityAuthorizationRequest
		if err := decodeOntologyMutationBody(req, &body); err != nil || idempotencyKey(req) == "" {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		body.IdempotencyKey = idempotencyKey(req)
		result, err := r.ontologyReliability.RequestRepairAuthorization(req.Context(), principal, tenantID, parts[4], parts[6], body)
		if err != nil {
			writeOntologyViewError(w, err, cid)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	case req.Method == http.MethodPost && len(parts) == 10 && parts[3] == "incidents" && parts[5] == "rules" && parts[7] == "authorizations" && parts[9] == "decision" && validOntologyContextID(parts[4]) && validOntologyContextID(parts[6]) && validOntologyContextID(parts[8]):
		if !ontologyMutationOriginAllowed(req) {
			writeJSON(w, http.StatusForbidden, ErrorResponse{Code: ErrorForbidden, Message: "permission denied", CorrelationID: cid})
			return
		}
		var body OntologyReliabilityAuthorizationDecisionRequest
		if err := decodeOntologyMutationBody(req, &body); err != nil || idempotencyKey(req) == "" {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		body.IdempotencyKey = idempotencyKey(req)
		result, err := r.ontologyReliability.ResolveRepairAuthorization(req.Context(), principal, tenantID, parts[4], parts[6], parts[8], body)
		if err != nil {
			writeOntologyViewError(w, err, cid)
			return
		}
		writeJSON(w, http.StatusOK, result)
	case req.Method == http.MethodPost && len(parts) == 10 && parts[3] == "incidents" && parts[5] == "rules" && parts[7] == "authorizations" && parts[9] == "invoke" && validOntologyContextID(parts[4]) && validOntologyContextID(parts[6]) && validOntologyContextID(parts[8]):
		if !ontologyMutationOriginAllowed(req) {
			writeJSON(w, http.StatusForbidden, ErrorResponse{Code: ErrorForbidden, Message: "permission denied", CorrelationID: cid})
			return
		}
		if idempotencyKey(req) == "" {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		var body struct{}
		if err := decodeOntologyMutationBody(req, &body); err != nil {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologyReliability.InvokeAuthorizedRepair(req.Context(), principal, tenantID, parts[4], parts[6], parts[8], OntologyReliabilityInvokeRequest{IdempotencyKey: idempotencyKey(req)})
		if err != nil {
			writeOntologyViewError(w, err, cid)
			return
		}
		writeJSON(w, http.StatusAccepted, result)
	default:
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: ErrorNotFound, Message: "resource not found", CorrelationID: cid})
	}
}
