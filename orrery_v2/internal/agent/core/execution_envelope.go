package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ExecutionEnvelope is the server-owned authorization and identity snapshot
// for one Agent execution. Callers may carry it, but they cannot broaden it.
type ExecutionEnvelope struct {
	Version        int       `json:"version"`
	EnvelopeID     string    `json:"envelope_id"`
	EnvelopeDigest string    `json:"envelope_digest"`
	IssuedAt       time.Time `json:"issued_at"`
	ExpiresAt      time.Time `json:"expires_at,omitempty"`

	TenantID        string                 `json:"tenant_id"`
	RunID           string                 `json:"run_id"`
	ScheduledTaskID string                 `json:"scheduled_task_id,omitempty"`
	ScheduledRunID  string                 `json:"scheduled_run_id,omitempty"`
	MissionID       string                 `json:"mission_id,omitempty"`
	TaskID          string                 `json:"task_id,omitempty"`
	AgentID         string                 `json:"agent_id"`
	AgentProfileID  string                 `json:"agent_profile_id"`
	PrincipalID     string                 `json:"principal_id,omitempty"`
	PrincipalKind   ExecutionPrincipalKind `json:"principal_kind,omitempty"`

	OrganizationID     string `json:"organization_id,omitempty"`
	OrganizationName   string `json:"organization_name,omitempty"`
	PeopleTenantID     string `json:"people_tenant_id,omitempty"`
	PortfolioID        string `json:"portfolio_id,omitempty"`
	PortfolioBindingID string `json:"portfolio_binding_id,omitempty"`
	PortfolioVersionID string `json:"portfolio_version_id,omitempty"`

	ResponsibilityID      string   `json:"responsibility_id,omitempty"`
	ResponsibilityVersion int64    `json:"responsibility_version,omitempty"`
	PolicyID              string   `json:"policy_id,omitempty"`
	PolicyVersion         int64    `json:"policy_version,omitempty"`
	PermissionScopes      []string `json:"permission_scopes,omitempty"`
	DataScopes            []string `json:"data_scopes,omitempty"`
	AllowedToolIDs        []string `json:"allowed_tool_ids,omitempty"`
	ForbiddenActions      []string `json:"forbidden_actions,omitempty"`
	// BoundToolPermissionScopes carries host-resolved grants for individual
	// bound resources. It never changes the human principal's global scopes.
	BoundToolPermissionScopes map[string][]string `json:"bound_tool_permission_scopes,omitempty"`

	ResponsibilityContext *ResponsibilityContext `json:"responsibility_context,omitempty"`
}

const ExecutionEnvelopeVersion = 1

// CloneExecutionEnvelope returns a detached envelope suitable for handing to
// another layer.
func CloneExecutionEnvelope(value *ExecutionEnvelope) *ExecutionEnvelope {
	if value == nil {
		return nil
	}
	clone := *value
	clone.PermissionScopes = append([]string(nil), value.PermissionScopes...)
	clone.DataScopes = append([]string(nil), value.DataScopes...)
	clone.AllowedToolIDs = append([]string(nil), value.AllowedToolIDs...)
	clone.ForbiddenActions = append([]string(nil), value.ForbiddenActions...)
	if value.BoundToolPermissionScopes != nil {
		clone.BoundToolPermissionScopes = make(map[string][]string, len(value.BoundToolPermissionScopes))
		for toolID, scopes := range value.BoundToolPermissionScopes {
			clone.BoundToolPermissionScopes[toolID] = append([]string(nil), scopes...)
		}
	}
	clone.ResponsibilityContext = CloneResponsibilityContext(value.ResponsibilityContext)
	return &clone
}

// CloneExecutionEnvelopeForRun derives a child envelope while retaining the
// parent's organization, responsibility, and permission bounds.
func CloneExecutionEnvelopeForRun(value *ExecutionEnvelope, runID string, now time.Time) (*ExecutionEnvelope, error) {
	if value == nil {
		return nil, nil
	}
	if err := value.Verify(); err != nil {
		return nil, fmt.Errorf("verify parent execution envelope: %w", err)
	}
	clone := CloneExecutionEnvelope(value)
	clone.RunID = strings.TrimSpace(runID)
	clone.EnvelopeID = ""
	clone.EnvelopeDigest = ""
	clone.IssuedAt = time.Time{}
	if err := clone.SealAt(now); err != nil {
		return nil, err
	}
	return clone, nil
}

// Seal normalizes the envelope and computes its immutable digest.
func (e *ExecutionEnvelope) Seal() error {
	return e.SealAt(time.Now().UTC())
}

// SealAt is deterministic for callers that own a clock.
func (e *ExecutionEnvelope) SealAt(now time.Time) error {
	if e == nil {
		return fmt.Errorf("execution envelope is nil")
	}
	e.normalize()
	if e.Version == 0 {
		e.Version = ExecutionEnvelopeVersion
	}
	if e.IssuedAt.IsZero() {
		e.IssuedAt = now.UTC()
	} else {
		e.IssuedAt = e.IssuedAt.UTC()
	}
	if e.ExpiresAt.IsZero() && e.ResponsibilityContext != nil {
		e.ExpiresAt = e.ResponsibilityContext.ExpiresAt.UTC()
	}
	if e.EnvelopeID == "" {
		e.EnvelopeID = "execution-envelope:" + firstExecutionEnvelopeNonBlank(e.RunID, e.TaskID, e.AgentID)
	}
	if err := e.validateUnsigned(); err != nil {
		return err
	}
	e.EnvelopeDigest = e.digest()
	return e.Verify()
}

// Verify checks that the serialized authorization snapshot has not changed.
func (e ExecutionEnvelope) Verify() error {
	e.normalize()
	if err := e.validateUnsigned(); err != nil {
		return err
	}
	if strings.TrimSpace(e.EnvelopeDigest) == "" || e.digest() != strings.TrimSpace(e.EnvelopeDigest) {
		return fmt.Errorf("execution envelope digest mismatch")
	}
	return nil
}

func (e ExecutionEnvelope) validateUnsigned() error {
	if e.Version != ExecutionEnvelopeVersion {
		return fmt.Errorf("unsupported execution envelope version %d", e.Version)
	}
	if strings.TrimSpace(e.EnvelopeID) == "" {
		return fmt.Errorf("execution envelope id is required")
	}
	if e.IssuedAt.IsZero() {
		return fmt.Errorf("execution envelope issued_at is required")
	}
	if strings.TrimSpace(e.TenantID) == "" || strings.TrimSpace(e.RunID) == "" ||
		strings.TrimSpace(e.AgentID) == "" || strings.TrimSpace(e.AgentProfileID) == "" {
		return fmt.Errorf("execution envelope identity is incomplete")
	}
	if e.ResponsibilityContext != nil {
		if strings.TrimSpace(e.ResponsibilityContext.AgentProfileID) != "" &&
			strings.TrimSpace(e.ResponsibilityContext.AgentProfileID) != strings.TrimSpace(e.AgentProfileID) {
			return fmt.Errorf("execution envelope profile does not match responsibility context")
		}
		if strings.TrimSpace(e.ResponsibilityContext.ContextID) == "" ||
			strings.TrimSpace(e.ResponsibilityContext.ResponsibilityID) == "" ||
			strings.TrimSpace(e.ResponsibilityContext.PolicyID) == "" ||
			e.ResponsibilityContext.ResponsibilityVersion < 1 ||
			e.ResponsibilityContext.PolicyVersion < 1 {
			return fmt.Errorf("execution envelope responsibility context is incomplete")
		}
	}
	return nil
}

func (e *ExecutionEnvelope) normalize() {
	if e == nil {
		return
	}
	e.EnvelopeID = strings.TrimSpace(e.EnvelopeID)
	e.EnvelopeDigest = strings.TrimSpace(e.EnvelopeDigest)
	e.TenantID = strings.TrimSpace(e.TenantID)
	e.RunID = strings.TrimSpace(e.RunID)
	e.ScheduledTaskID = strings.TrimSpace(e.ScheduledTaskID)
	e.ScheduledRunID = strings.TrimSpace(e.ScheduledRunID)
	e.MissionID = strings.TrimSpace(e.MissionID)
	e.TaskID = strings.TrimSpace(e.TaskID)
	e.AgentID = strings.TrimSpace(e.AgentID)
	e.AgentProfileID = strings.TrimSpace(e.AgentProfileID)
	e.PrincipalID = strings.TrimSpace(e.PrincipalID)
	e.OrganizationID = strings.TrimSpace(e.OrganizationID)
	e.OrganizationName = strings.TrimSpace(e.OrganizationName)
	e.PeopleTenantID = strings.TrimSpace(e.PeopleTenantID)
	e.PortfolioID = strings.TrimSpace(e.PortfolioID)
	e.PortfolioBindingID = strings.TrimSpace(e.PortfolioBindingID)
	e.PortfolioVersionID = strings.TrimSpace(e.PortfolioVersionID)
	e.ResponsibilityID = strings.TrimSpace(e.ResponsibilityID)
	e.PolicyID = strings.TrimSpace(e.PolicyID)
	e.PermissionScopes = normalizeExecutionEnvelopeList(e.PermissionScopes)
	e.DataScopes = normalizeExecutionEnvelopeList(e.DataScopes)
	e.AllowedToolIDs = normalizeExecutionEnvelopeList(e.AllowedToolIDs)
	e.ForbiddenActions = normalizeExecutionEnvelopeList(e.ForbiddenActions)
	if len(e.BoundToolPermissionScopes) > 0 {
		grants := make(map[string][]string, len(e.BoundToolPermissionScopes))
		for toolID, scopes := range e.BoundToolPermissionScopes {
			if toolID == strings.TrimSpace(toolID) && toolID != "" {
				grants[toolID] = normalizeExecutionEnvelopeList(scopes)
			}
		}
		e.BoundToolPermissionScopes = grants
	}
	if e.ResponsibilityContext != nil {
		e.ResponsibilityContext = CloneResponsibilityContext(e.ResponsibilityContext)
		e.ResponsibilityContext = normalizeExecutionResponsibilityContext(e.ResponsibilityContext)
		if e.OrganizationID == "" {
			e.OrganizationID = strings.TrimSpace(e.ResponsibilityContext.OrganizationID)
		}
		if e.OrganizationName == "" {
			e.OrganizationName = strings.TrimSpace(e.ResponsibilityContext.OrganizationName)
		}
		if e.ResponsibilityID == "" {
			e.ResponsibilityID = strings.TrimSpace(e.ResponsibilityContext.ResponsibilityID)
		}
		if e.ResponsibilityVersion == 0 {
			e.ResponsibilityVersion = e.ResponsibilityContext.ResponsibilityVersion
		}
		if e.PolicyID == "" {
			e.PolicyID = strings.TrimSpace(e.ResponsibilityContext.PolicyID)
		}
		if e.PolicyVersion == 0 {
			e.PolicyVersion = e.ResponsibilityContext.PolicyVersion
		}
		if len(e.PermissionScopes) == 0 {
			e.PermissionScopes = normalizeExecutionEnvelopeList(e.ResponsibilityContext.TaskPermissionScopes)
		}
		if len(e.DataScopes) == 0 {
			e.DataScopes = normalizeExecutionEnvelopeList(e.ResponsibilityContext.AllowedDataScopes)
		}
		if len(e.AllowedToolIDs) == 0 {
			e.AllowedToolIDs = normalizeExecutionEnvelopeList(e.ResponsibilityContext.AllowedTools)
		}
		if len(e.ForbiddenActions) == 0 {
			e.ForbiddenActions = normalizeExecutionEnvelopeList(e.ResponsibilityContext.ForbiddenActions)
		}
	}
}

func normalizeExecutionResponsibilityContext(value *ResponsibilityContext) *ResponsibilityContext {
	if value == nil {
		return nil
	}
	value.ContextID = strings.TrimSpace(value.ContextID)
	value.ResponsibilityID = strings.TrimSpace(value.ResponsibilityID)
	value.PolicyID = strings.TrimSpace(value.PolicyID)
	value.AgentProfileID = strings.TrimSpace(value.AgentProfileID)
	value.OrganizationID = strings.TrimSpace(value.OrganizationID)
	value.OrganizationName = strings.TrimSpace(value.OrganizationName)
	value.AllowedTools = normalizeExecutionEnvelopeList(value.AllowedTools)
	value.AllowedDataScopes = normalizeExecutionEnvelopeList(value.AllowedDataScopes)
	value.PolicyToolScopes = normalizeExecutionEnvelopeList(value.PolicyToolScopes)
	value.PolicyDataScopes = normalizeExecutionEnvelopeList(value.PolicyDataScopes)
	value.TaskPermissionScopes = normalizeExecutionEnvelopeList(value.TaskPermissionScopes)
	value.ForbiddenActions = normalizeExecutionEnvelopeList(value.ForbiddenActions)
	return value
}

func normalizeExecutionEnvelopeList(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func (e ExecutionEnvelope) digest() string {
	unsigned := e
	unsigned.EnvelopeDigest = ""
	raw, _ := json.Marshal(unsigned)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// AllowsTool applies the envelope's explicit tool and permission fences.
func (e ExecutionEnvelope) AllowsTool(toolID string, requiredScopes []string) bool {
	if err := e.Verify(); err != nil {
		return false
	}
	toolID = strings.TrimSpace(toolID)
	if toolID == "" {
		return false
	}
	forbidden := e.ForbiddenActions
	for _, selector := range forbidden {
		if executionEnvelopeSelectorAllows([]string{selector}, toolID) {
			return false
		}
		for _, required := range requiredScopes {
			if executionEnvelopeSelectorAllows([]string{selector}, required) {
				return false
			}
		}
	}
	if len(e.AllowedToolIDs) > 0 && !executionEnvelopeSelectorAllows(e.AllowedToolIDs, toolID) {
		return false
	}
	scopes := e.PermissionScopes
	if len(scopes) == 0 && e.ResponsibilityContext != nil {
		// Task scopes are the narrowest authority. A policy scope is only a
		// fallback for legacy responsibility snapshots without task scopes.
		scopes = e.ResponsibilityContext.TaskPermissionScopes
		if len(scopes) == 0 {
			scopes = e.ResponsibilityContext.PolicyToolScopes
		}
	}
	var boundScopes []string
	if e.PrincipalKind == ExecutionPrincipalUser && e.ResponsibilityContext == nil &&
		(e.MissionID == "" || (e.TaskID != "" && e.MissionID == "discussion-"+e.TaskID)) && e.ScheduledTaskID == "" && e.ScheduledRunID == "" &&
		e.ResponsibilityID == "" && e.PolicyID == "" {
		boundScopes = e.BoundToolPermissionScopes[toolID]
	}
	for _, scope := range requiredScopes {
		if len(scopes) > 0 && !executionEnvelopeSelectorAllows(scopes, scope) &&
			!executionEnvelopeSelectorAllows(boundScopes, scope) {
			return false
		}
	}
	return true
}

func executionEnvelopeSelectorAllows(selectors []string, required string) bool {
	required = strings.TrimSpace(required)
	if required == "" {
		return false
	}
	for _, selector := range selectors {
		selector = strings.TrimSpace(selector)
		if selector == "*" || selector == required {
			return true
		}
		if strings.HasSuffix(selector, "*") && strings.HasPrefix(required, strings.TrimSuffix(selector, "*")) {
			return true
		}
		if strings.HasPrefix(required, selector+".") {
			return true
		}
	}
	return false
}

func firstExecutionEnvelopeNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
