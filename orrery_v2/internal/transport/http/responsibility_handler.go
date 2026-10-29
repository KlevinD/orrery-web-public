package httptransport

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/responsibility"
)

func (r *Router) handleResponsibilityProfiles(w http.ResponseWriter, req *http.Request, parts []string) {
	if r.responsibilities == nil || req.Method != http.MethodGet || len(parts) != 4 {
		http.NotFound(w, req)
		return
	}
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		writeMappedError(w, req, err)
		return
	}
	tenantID := strings.TrimSpace(viewer.TenantID)
	if tenantID == "" {
		tenantID = "system"
	}
	versionLimit := responsibilityProfileHistoryLimit(req)
	manager := requestHasAuthorizedScope(req, operations.PermissionOperationsRead) || requestHasAuthorizedScope(req, operations.PermissionOperationsManage)
	scopeID := strings.TrimSpace(parts[3])
	if scopeID == "" {
		writeMappedError(w, req, responsibility.ErrInvalidContract)
		return
	}
	var profile responsibility.PeopleResponsibilityProfileDTO
	switch parts[2] {
	case "agents":
		canReadWorkforce := requestHasAuthorizedScope(req, operations.PermissionWorkforceRead)
		isSelf := viewer.ViewerKind == "agent" && viewer.ViewerID == scopeID
		if !manager && !canReadWorkforce && !isSelf {
			writeMappedError(w, req, operations.ErrPermissionDenied)
			return
		}
		profile, err = r.responsibilities.GetPeopleAgentResponsibilityProfile(req.Context(), tenantID, scopeID, versionLimit)
	case "organizations":
		if !manager && !requestHasAuthorizedScope(req, operations.PermissionOrgRead) {
			writeMappedError(w, req, operations.ErrPermissionDenied)
			return
		}
		profile, err = r.responsibilities.GetPeopleOrganizationResponsibilityProfile(req.Context(), tenantID, scopeID, versionLimit)
	default:
		http.NotFound(w, req)
		return
	}
	if err != nil {
		writeMappedError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func responsibilityProfileHistoryLimit(req *http.Request) int {
	for _, key := range []string{"version_limit", "history_limit"} {
		raw := strings.TrimSpace(req.URL.Query().Get(key))
		if raw == "" {
			continue
		}
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 {
			continue
		}
		if limit > 100 {
			return 100
		}
		return limit
	}
	return 10
}

func (r *Router) handleResponsibilities(w http.ResponseWriter, req *http.Request, parts []string) {
	if r.responsibilities == nil {
		writeMappedError(w, req, responsibility.ErrInvalidContract)
		return
	}
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		writeMappedError(w, req, err)
		return
	}
	manager := requestHasAuthorizedScope(req, operations.PermissionOperationsRead) || requestHasAuthorizedScope(req, operations.PermissionOperationsManage)
	if len(parts) == 2 && req.Method == http.MethodGet {
		limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
		agentID := strings.TrimSpace(req.URL.Query().Get("agent_id"))
		if !manager {
			agentID = viewer.ViewerID
		}
		states, err := parseResponsibilityStates(req.URL.Query().Get("state"))
		if err != nil {
			writeMappedError(w, req, err)
			return
		}
		page, err := r.responsibilities.ListInbox(req.Context(), viewer.TenantID, agentID, states, limit)
		if err != nil {
			writeMappedError(w, req, err)
			return
		}
		writeJSON(w, http.StatusOK, page)
		return
	}
	if len(parts) < 3 {
		http.NotFound(w, req)
		return
	}
	responsibilityID := strings.TrimSpace(parts[2])
	if len(parts) == 3 && req.Method == http.MethodGet {
		detail, err := r.responsibilities.Get(req.Context(), viewer.TenantID, responsibilityID)
		if err != nil {
			writeMappedError(w, req, err)
			return
		}
		if !manager && detail.AccountableAgent != viewer.ViewerID {
			writeMappedError(w, req, operations.ErrPermissionDenied)
			return
		}
		writeJSON(w, http.StatusOK, detail)
		return
	}
	if len(parts) != 4 || req.Method != http.MethodPost {
		http.NotFound(w, req)
		return
	}
	if !requestHasAuthorizedScope(req, operations.PermissionOperationsManage) {
		writeMappedError(w, req, operations.ErrPermissionDenied)
		return
	}
	idempotencyKey, err := requiredIdempotencyKey(req)
	if err != nil {
		writeInvalidIdempotencyKey(w, req)
		return
	}
	version, err := requiredExpectedVersion(req)
	if err != nil {
		writeInvalidVersion(w, req)
		return
	}
	var body struct {
		Reason         string `json:"reason,omitempty"`
		AgentProfileID string `json:"agent_profile_id,omitempty"`
		AcceptBy       string `json:"accept_by,omitempty"`
	}
	if err := decodeStrictJSON(req, &body); err != nil {
		writeInvalidBody(w, req)
		return
	}
	command := responsibility.AssignmentControl{TenantID: viewer.TenantID, AssignmentID: responsibilityID, ActorID: viewer.ViewerID,
		Reason: body.Reason, AgentProfileID: body.AgentProfileID, ExpectedVersion: version, IdempotencyKey: idempotencyKey}
	if strings.TrimSpace(body.AcceptBy) != "" {
		command.AcceptBy, err = time.Parse(time.RFC3339, body.AcceptBy)
		if err != nil {
			writeInvalidBody(w, req)
			return
		}
	}
	var detail responsibility.ResponsibilityDetailDTO
	switch parts[3] {
	case "pause":
		detail, err = r.responsibilities.Pause(req.Context(), command)
	case "resume":
		detail, err = r.responsibilities.Resume(req.Context(), command)
	case "retry":
		detail, err = r.responsibilities.Retry(req.Context(), command)
	case "terminate":
		detail, err = r.responsibilities.Terminate(req.Context(), command)
	case "reassign":
		detail, err = r.responsibilities.Reassign(req.Context(), command)
	default:
		http.NotFound(w, req)
		return
	}
	if err != nil {
		writeMappedError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (r *Router) handleResponsibilityPolicies(w http.ResponseWriter, req *http.Request, parts []string) {
	if r.responsibilities == nil || len(parts) != 3 {
		http.NotFound(w, req)
		return
	}
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		writeMappedError(w, req, err)
		return
	}
	agentID := strings.TrimSpace(parts[2])
	kind := responsibility.WorkKind(strings.TrimSpace(req.URL.Query().Get("work_kind")))
	if kind == "" {
		kind = responsibility.WorkKindMissionTask
	}
	switch req.Method {
	case http.MethodGet:
		policy, err := r.responsibilities.GetPolicy(req.Context(), viewer.TenantID, agentID, kind)
		if err != nil {
			writeMappedError(w, req, err)
			return
		}
		writeJSON(w, http.StatusOK, policy)
	case http.MethodPut:
		if !requestHasAuthorizedScope(req, operations.PermissionOperationsManage) {
			writeMappedError(w, req, operations.ErrPermissionDenied)
			return
		}
		version, err := requiredExpectedVersion(req)
		if err != nil {
			writeInvalidVersion(w, req)
			return
		}
		idempotencyKey, err := requiredIdempotencyKey(req)
		if err != nil {
			writeInvalidIdempotencyKey(w, req)
			return
		}
		var body responsibility.ResponsibilityPolicyDTO
		if err := decodeStrictJSON(req, &body); err != nil {
			writeInvalidBody(w, req)
			return
		}
		policy, err := r.responsibilities.SavePolicy(req.Context(), responsibility.PolicyUpdate{
			TenantID: viewer.TenantID, ActorID: viewer.ViewerID, IdempotencyKey: idempotencyKey, AgentProfileID: agentID, WorkKind: kind, AutonomyLevel: body.AutonomyLevel,
			MaxParallel: body.MaxParallel, AcceptTimeoutSeconds: body.AcceptTimeoutSeconds,
			HeartbeatTimeoutSeconds: body.HeartbeatTimeoutSeconds, MaxAttempts: body.MaxAttempts,
			RetryBackoffSeconds: body.RetryBackoffSeconds, MaxCostPerTask: body.MaxCostPerTask,
			MaxTokenBudget: body.MaxTokenBudget, ApprovalPolicy: body.ApprovalPolicy,
			AllowedRiskLevels: body.AllowedRiskLevels, ToolScopes: body.ToolScopes, DataScopes: body.DataScopes,
			EscalationChain: body.EscalationChain, RecheckIntervalSeconds: body.RecheckIntervalSeconds,
			DeadlineWarningSeconds: body.DeadlineWarningSeconds, Enabled: body.Enabled, ExpectedVersion: version,
		})
		if err != nil {
			writeMappedError(w, req, err)
			return
		}
		writeJSON(w, http.StatusOK, policy)
	default:
		http.NotFound(w, req)
	}
}

func (r *Router) handleResponsibilityBlocker(w http.ResponseWriter, req *http.Request, parts []string) {
	if r.responsibilities == nil || len(parts) != 4 || parts[3] != "resolve" || req.Method != http.MethodPost {
		http.NotFound(w, req)
		return
	}
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		writeMappedError(w, req, err)
		return
	}
	if !requestHasAuthorizedScope(req, operations.PermissionOperationsManage) {
		writeMappedError(w, req, operations.ErrPermissionDenied)
		return
	}
	version, err := requiredExpectedVersion(req)
	if err != nil {
		writeInvalidVersion(w, req)
		return
	}
	idempotencyKey, err := requiredIdempotencyKey(req)
	if err != nil {
		writeInvalidIdempotencyKey(w, req)
		return
	}
	var body struct {
		Resolution string `json:"resolution"`
	}
	if err := decodeStrictJSON(req, &body); err != nil {
		writeInvalidBody(w, req)
		return
	}
	detail, err := r.responsibilities.ResolveBlocker(req.Context(), viewer.TenantID, parts[2], viewer.ViewerID, body.Resolution, idempotencyKey, version)
	if err != nil {
		writeMappedError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (r *Router) handleResponsibilitySignal(w http.ResponseWriter, req *http.Request) {
	if r.responsibilities == nil || req.Method != http.MethodPost {
		http.NotFound(w, req)
		return
	}
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		writeMappedError(w, req, err)
		return
	}
	if !requestHasAuthorizedScope(req, operations.PermissionOperationsManage) {
		writeMappedError(w, req, operations.ErrPermissionDenied)
		return
	}
	idempotencyKey, err := requiredIdempotencyKey(req)
	if err != nil {
		writeInvalidIdempotencyKey(w, req)
		return
	}
	var body struct {
		ResponsibilityID string                     `json:"responsibility_id,omitempty"`
		BlockerKind      responsibility.BlockerKind `json:"blocker_kind,omitempty"`
		RequestedFrom    string                     `json:"requested_from,omitempty"`
		SourceRef        string                     `json:"source_ref"`
		Summary          string                     `json:"summary"`
	}
	if err := decodeStrictJSON(req, &body); err != nil {
		writeInvalidBody(w, req)
		return
	}
	resolved, err := r.responsibilities.ObserveExternalSignal(req.Context(), responsibility.ExternalSignal{TenantID: viewer.TenantID, ActorID: viewer.ViewerID, IdempotencyKey: idempotencyKey,
		ResponsibilityID: body.ResponsibilityID, BlockerKind: body.BlockerKind, RequestedFrom: body.RequestedFrom, SourceRef: body.SourceRef, Summary: body.Summary})
	if err != nil {
		writeMappedError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"resolved": resolved})
}

func parseResponsibilityStates(raw string) ([]responsibility.AssignmentState, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	states := make([]responsibility.AssignmentState, 0)
	for _, value := range strings.Split(raw, ",") {
		state := responsibility.AssignmentState(strings.TrimSpace(value))
		if state == "" {
			continue
		}
		states = append(states, state)
	}
	if len(states) == 0 {
		return nil, responsibility.ErrInvalidContract
	}
	return states, nil
}

func requestHasAuthorizedScope(req *http.Request, scope string) bool {
	for _, value := range authorizedRoutePermissionScopes(req) {
		if strings.TrimSpace(value) == strings.TrimSpace(scope) {
			return true
		}
	}
	return false
}
