package httptransport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	governanceview "orrery_v2/internal/app/ontologyview/governance"
	"orrery_v2/internal/app/operations"
)

type OntologyGovernanceService interface {
	Summary(context.Context, identity.Principal, string) (ontologycontract.Summary[governanceview.SummaryData], error)
	ListProposals(context.Context, identity.Principal, governanceview.ProposalListQuery) (ontologycontract.CursorPage[governanceview.ProposalItem], error)
	GetProposal(context.Context, identity.Principal, string, string) (governanceview.ProposalDetail, error)
	AnalyzeProposal(context.Context, identity.Principal, string, string) (governanceview.AnalysisReport, error)
	ListReceipts(context.Context, identity.Principal, governanceview.ReceiptListQuery) (ontologycontract.CursorPage[governanceview.ReceiptItem], error)
	ListAuditEvents(context.Context, identity.Principal, governanceview.AuditEventListQuery) (ontologycontract.CursorPage[governanceview.AuditEventItem], error)
}

type OntologyGovernanceCommandService interface {
	DecideProposal(context.Context, identity.Principal, governanceview.DecideProposalRequest) (ontology.OntologyMetadataProposal, error)
	CreatePromotionPlan(context.Context, identity.Principal, governanceview.CreatePromotionPlanRequest) (governanceview.PromotionPlanResult, error)
	ApplyPromotionPlan(context.Context, identity.Principal, governanceview.ApplyPromotionPlanRequest) (ontology.MetadataProposalPromotionReceipt, error)
	RetryProjectionRefresh(context.Context, identity.Principal, governanceview.RetryProjectionRefreshRequest) (governanceview.ProjectionRefreshResult, error)
}

type ontologyGovernanceDecisionBody struct {
	Decision         ontology.MetadataProposalStatus    `json:"decision"`
	Reason           string                             `json:"reason"`
	ExpectedRevision int64                              `json:"expected_revision"`
	EvidenceRefs     []governanceview.EvidenceReference `json:"evidence_refs,omitempty"`
}

type ontologyGovernancePlanBody struct {
	ExpectedRevision int64 `json:"expected_revision"`
}

type ontologyGovernanceApplyBody struct {
	IdempotencyKey string `json:"idempotency_key"`
	Confirmed      bool   `json:"confirmed"`
}

type ontologyGovernanceDecisionResult struct {
	ProposalID string                          `json:"proposal_id"`
	Revision   int64                           `json:"revision"`
	Status     ontology.MetadataProposalStatus `json:"status"`
	ReviewedAt time.Time                       `json:"reviewed_at"`
}

type ontologyGovernanceAppliedChange struct {
	Operation      ontology.PromotionOperation `json:"operation"`
	TargetKind     string                      `json:"target_kind"`
	TargetID       string                      `json:"target_id"`
	Field          string                      `json:"field"`
	AppliedVersion int                         `json:"applied_version,omitempty"`
}

type ontologyGovernanceApplyResult struct {
	ReceiptID               string                            `json:"receipt_id"`
	ProposalID              string                            `json:"proposal_id"`
	ProposalRevision        int64                             `json:"proposal_revision"`
	Status                  ontology.PromotionReceiptStatus   `json:"status"`
	Applied                 bool                              `json:"applied"`
	AppliedAt               time.Time                         `json:"applied_at"`
	Changes                 []ontologyGovernanceAppliedChange `json:"changes"`
	ProjectionRefreshStatus string                            `json:"projection_refresh_status"`
}

func (r *Router) handleOntologyGovernance(w http.ResponseWriter, req *http.Request, parts []string) {
	setOntologyPrivateNoStore(w)
	cid := correlationID(req)
	principal, ok := ontologyPrincipal(req)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Code: ErrorUnauthorized, Message: "authorization is required", CorrelationID: cid})
		return
	}
	tenantID := strings.TrimSpace(req.URL.Query().Get("tenant_id"))
	if !validOptionalOntologyTenantID(tenantID) {
		writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
		return
	}
	if req.Method != http.MethodGet && req.Method != http.MethodPost {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: ErrorNotFound, Message: "resource not found", CorrelationID: cid})
		return
	}
	if req.Method == http.MethodPost {
		if tenantID == "" {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		r.handleOntologyGovernanceMutation(w, req, principal, tenantID, parts, cid)
		return
	}
	if r.ontologyGovernance == nil {
		writeOntologyViewError(w, ontologyscope.ErrDependencyUnavailable, cid)
		return
	}
	switch {
	case len(parts) == 4 && parts[3] == "summary":
		result, err := r.ontologyGovernance.Summary(req.Context(), principal, tenantID)
		writeOntologyGovernanceResult(w, result, err, cid)
	case len(parts) == 4 && parts[3] == "proposals":
		query, ok := ontologyGovernanceProposalListQuery(req, tenantID)
		if !ok {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologyGovernance.ListProposals(req.Context(), principal, query)
		writeOntologyGovernanceResult(w, result, err, cid)
	case len(parts) == 5 && parts[3] == "proposals":
		if tenantID == "" || !validOntologyGovernanceID(parts[4]) {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologyGovernance.GetProposal(req.Context(), principal, tenantID, parts[4])
		writeOntologyGovernanceResult(w, result, err, cid)
	case len(parts) == 6 && parts[3] == "proposals" && parts[5] == "analysis":
		if tenantID == "" || !validOntologyGovernanceID(parts[4]) {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologyGovernance.AnalyzeProposal(req.Context(), principal, tenantID, parts[4])
		writeOntologyGovernanceResult(w, result, err, cid)
	case len(parts) == 6 && parts[3] == "proposals" && parts[5] == "receipts":
		if tenantID == "" {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		query, ok := ontologyGovernanceReceiptListQuery(req, tenantID, parts[4])
		if !ok {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologyGovernance.ListReceipts(req.Context(), principal, query)
		writeOntologyGovernanceResult(w, result, err, cid)
	case len(parts) == 6 && parts[3] == "proposals" && parts[5] == "audit-events":
		if tenantID == "" {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		query, ok := ontologyGovernanceAuditListQuery(req, tenantID, parts[4])
		if !ok {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologyGovernance.ListAuditEvents(req.Context(), principal, query)
		writeOntologyGovernanceResult(w, result, err, cid)
	default:
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: ErrorNotFound, Message: "resource not found", CorrelationID: cid})
	}
}

func (r *Router) handleOntologyGovernanceMutation(w http.ResponseWriter, req *http.Request, principal identity.Principal, tenantID string, parts []string, cid string) {
	if r.ontologyGovernanceCommand == nil {
		writeOntologyViewError(w, ontologyscope.ErrDependencyUnavailable, cid)
		return
	}
	if !ontologyMutationOriginAllowed(req) {
		writeJSON(w, http.StatusForbidden, ErrorResponse{Code: ErrorForbidden, Message: "request origin is not allowed", CorrelationID: cid})
		return
	}
	switch {
	case len(parts) == 6 && parts[3] == "proposals" && parts[5] == "decisions" && validOntologyGovernanceID(parts[4]):
		var body ontologyGovernanceDecisionBody
		if err := decodeOntologyMutationBody(req, &body); err != nil {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		evidence := make([]ontology.MetadataEvidenceRef, 0, len(body.EvidenceRefs))
		for _, ref := range body.EvidenceRefs {
			evidence = append(evidence, ontology.MetadataEvidenceRef{Kind: ref.Kind, ID: ref.ID})
		}
		result, err := r.ontologyGovernanceCommand.DecideProposal(req.Context(), principal, governanceview.DecideProposalRequest{
			TenantID: tenantID, ProposalID: parts[4], ExpectedRevision: body.ExpectedRevision,
			Decision: body.Decision, Reason: body.Reason, EvidenceRefs: evidence,
		})
		r.recordOntologyGovernanceCommandAudit(req.Context(), principal, cid, "decision", tenantID, parts[4], "", ontology.MetadataProposalPromotionReceipt{}, err)
		if err != nil {
			writeOntologyViewError(w, err, cid)
			return
		}
		writeJSON(w, http.StatusOK, ontologyGovernanceDecisionResult{
			ProposalID: result.ID, Revision: result.Revision, Status: result.Status, ReviewedAt: result.ReviewedAt,
		})
	case len(parts) == 6 && parts[3] == "proposals" && parts[5] == "plans" && validOntologyGovernanceID(parts[4]):
		var body ontologyGovernancePlanBody
		if err := decodeOntologyMutationBody(req, &body); err != nil {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologyGovernanceCommand.CreatePromotionPlan(req.Context(), principal, governanceview.CreatePromotionPlanRequest{
			TenantID: tenantID, ProposalID: parts[4], ExpectedRevision: body.ExpectedRevision,
		})
		r.recordOntologyGovernanceCommandAudit(req.Context(), principal, cid, "plan", tenantID, parts[4], result.PlanID, ontology.MetadataProposalPromotionReceipt{}, err)
		if err != nil {
			writeOntologyViewError(w, err, cid)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	case len(parts) == 6 && parts[3] == "plans" && parts[5] == "apply" && validOntologyGovernanceID(parts[4]):
		var body ontologyGovernanceApplyBody
		if err := decodeOntologyMutationBody(req, &body); err != nil {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		receipt, err := r.ontologyGovernanceCommand.ApplyPromotionPlan(req.Context(), principal, governanceview.ApplyPromotionPlanRequest{
			TenantID: tenantID, PlanID: parts[4], IdempotencyKey: body.IdempotencyKey, Confirmed: body.Confirmed,
		})
		r.recordOntologyGovernanceCommandAudit(req.Context(), principal, cid, "apply", tenantID, receipt.ProposalID, parts[4], receipt, err)
		if err != nil {
			writeOntologyViewError(w, err, cid)
			return
		}
		writeJSON(w, http.StatusOK, safeOntologyGovernanceApplyResult(receipt))
	case len(parts) == 6 && parts[3] == "receipts" && parts[5] == "projection-refresh-retries" && validOntologyGovernanceID(parts[4]):
		commandKey := idempotencyKey(req)
		if commandKey == "" || len(commandKey) > 256 || !utf8.ValidString(commandKey) {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologyGovernanceCommand.RetryProjectionRefresh(req.Context(), principal, governanceview.RetryProjectionRefreshRequest{
			TenantID: tenantID, ReceiptID: parts[4], IdempotencyKey: commandKey,
		})
		if !result.IdempotentReplay {
			r.recordOntologyGovernanceCommandAudit(req.Context(), principal, cid, "projection_refresh_retry", tenantID, result.ProposalID, "", ontology.MetadataProposalPromotionReceipt{
				ReceiptID: result.ReceiptID, ProposalID: result.ProposalID, ProjectionRefreshStatus: result.Status,
			}, err)
		}
		if err != nil {
			writeOntologyViewError(w, err, cid)
			return
		}
		writeJSON(w, http.StatusOK, result)
	default:
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: ErrorNotFound, Message: "resource not found", CorrelationID: cid})
	}
}

func safeOntologyGovernanceApplyResult(receipt ontology.MetadataProposalPromotionReceipt) ontologyGovernanceApplyResult {
	changes := make([]ontologyGovernanceAppliedChange, 0, len(receipt.Changes))
	for _, change := range receipt.Changes {
		changes = append(changes, ontologyGovernanceAppliedChange{
			Operation: change.Operation, TargetKind: change.TargetKind, TargetID: change.TargetID, Field: change.Field, AppliedVersion: change.AppliedVersion,
		})
	}
	return ontologyGovernanceApplyResult{
		ReceiptID: receipt.ReceiptID, ProposalID: receipt.ProposalID, ProposalRevision: receipt.ProposalRevision,
		Status: receipt.Status, Applied: receipt.Applied, AppliedAt: receipt.AppliedAt, Changes: changes,
		ProjectionRefreshStatus: receipt.ProjectionRefreshStatus,
	}
}

func (r *Router) recordOntologyGovernanceCommandAudit(ctx context.Context, principal identity.Principal, cid, operation, tenantID, proposalID, planID string, receipt ontology.MetadataProposalPromotionReceipt, commandErr error) {
	if r == nil || r.auditRecorder == nil {
		return
	}
	principal = identity.NormalizePrincipal(principal)
	actorID := firstNonEmpty(principal.UserID, principal.AgentProfileID, principal.Username, principal.Subject)
	if actorID == "" {
		return
	}
	entityType := "ontology_metadata_proposal"
	entityID := strings.TrimSpace(proposalID)
	if entityID == "" {
		entityType = "ontology_governance_plan"
		entityID = hashAuditValue(planID)
	}
	if entityID == "" {
		return
	}
	event := operations.AuditEventDTO{
		ActorID: actorID, ViewerID: actorID, EntityType: entityType, EntityID: entityID,
		Action: "ontology_governance." + strings.TrimSpace(operation), Result: "succeeded", Severity: "info",
		CorrelationID: cid, Summary: "Ontology governance command processed",
		Metadata: map[string]string{
			"tenant_id": strings.TrimSpace(tenantID), "actor_kind": strings.TrimSpace(principal.ViewerKind),
			"proposal_id": strings.TrimSpace(proposalID), "plan_id_hash": hashAuditValue(planID),
			"receipt_id": strings.TrimSpace(receipt.ReceiptID), "proposal_revision": fmt.Sprint(receipt.ProposalRevision),
			"projection_refresh_status": strings.TrimSpace(receipt.ProjectionRefreshStatus),
		},
	}
	if receipt.ProposalRevision == 0 {
		delete(event.Metadata, "proposal_revision")
	}
	if strings.TrimSpace(receipt.ProjectionRefreshStatus) == "" {
		delete(event.Metadata, "projection_refresh_status")
	}
	if commandErr != nil {
		event.Result, event.Severity = "failed", "warning"
		event.Metadata["error_code"] = ontologyGovernanceAuditErrorCode(commandErr)
	}
	_ = r.auditRecorder.RecordAuditEvent(ctx, event)
}

func ontologyGovernanceAuditErrorCode(err error) string {
	switch {
	case errors.Is(err, ontologyscope.ErrAccessDenied):
		return ErrorForbidden
	case errors.Is(err, ontologycontract.ErrStateConflict):
		return ErrorStateConflict
	case errors.Is(err, ontology.ErrVersionConflict):
		return ErrorVersionConflict
	case errors.Is(err, ontologycontract.ErrInvalidRequest), errors.Is(err, ontology.ErrInvalidDefinition):
		return ErrorInvalidRequest
	default:
		return ErrorInternal
	}
}

func ontologyGovernanceProposalListQuery(req *http.Request, tenantID string) (governanceview.ProposalListQuery, bool) {
	limit, err := optionalOntologyLimit(req.URL.Query().Get("limit"))
	if err != nil {
		return governanceview.ProposalListQuery{}, false
	}
	query := governanceview.ProposalListQuery{
		TenantID:   tenantID,
		Queue:      governanceview.ProposalQueue(strings.ToLower(strings.TrimSpace(req.URL.Query().Get("queue")))),
		Status:     ontology.MetadataProposalStatus(strings.ToLower(strings.TrimSpace(req.URL.Query().Get("status")))),
		TargetKind: strings.ToLower(strings.TrimSpace(req.URL.Query().Get("kind"))),
		Search:     strings.TrimSpace(req.URL.Query().Get("search")),
		Limit:      limit,
		Cursor:     strings.TrimSpace(req.URL.Query().Get("cursor")),
	}
	if query.Queue != "" && query.Queue != governanceview.QueueActionable && query.Queue != governanceview.QueueAll {
		return governanceview.ProposalListQuery{}, false
	}
	if query.Status != "" {
		switch query.Status {
		case ontology.MetadataProposalStatusProposed, ontology.MetadataProposalStatusAccepted, ontology.MetadataProposalStatusRejected:
		default:
			return governanceview.ProposalListQuery{}, false
		}
	}
	if len(query.TargetKind) > 128 || !utf8.ValidString(query.Search) || utf8.RuneCountInString(query.Search) > 128 || len(query.Cursor) > 4096 {
		return governanceview.ProposalListQuery{}, false
	}
	return query, true
}

func ontologyGovernanceReceiptListQuery(req *http.Request, tenantID, proposalID string) (governanceview.ReceiptListQuery, bool) {
	if !validOntologyGovernanceID(proposalID) {
		return governanceview.ReceiptListQuery{}, false
	}
	limit, cursor, ok := ontologyGovernancePageWire(req)
	if !ok {
		return governanceview.ReceiptListQuery{}, false
	}
	return governanceview.ReceiptListQuery{TenantID: tenantID, ProposalID: proposalID, Limit: limit, Cursor: cursor}, true
}

func ontologyGovernanceAuditListQuery(req *http.Request, tenantID, proposalID string) (governanceview.AuditEventListQuery, bool) {
	if !validOntologyGovernanceID(proposalID) {
		return governanceview.AuditEventListQuery{}, false
	}
	limit, cursor, ok := ontologyGovernancePageWire(req)
	if !ok {
		return governanceview.AuditEventListQuery{}, false
	}
	return governanceview.AuditEventListQuery{TenantID: tenantID, ProposalID: proposalID, Limit: limit, Cursor: cursor}, true
}

func ontologyGovernancePageWire(req *http.Request) (int, string, bool) {
	limit, err := optionalOntologyLimit(req.URL.Query().Get("limit"))
	if err != nil {
		return 0, "", false
	}
	cursor := strings.TrimSpace(req.URL.Query().Get("cursor"))
	return limit, cursor, len(cursor) <= 4096
}

func validOntologyGovernanceID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 256
}

func writeOntologyGovernanceResult(w http.ResponseWriter, result any, err error, cid string) {
	if err != nil {
		writeOntologyViewError(w, err, cid)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func hashAuditValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
