package missioncontrol

import (
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/mission/projection"
)

func validateTaskExecutionEnvelopeAssignee(envelope *agentcore.ExecutionEnvelope, assignee taskAssigneeBinding) error {
	if envelope == nil {
		return nil
	}
	if !hasTaskAssignee(assignee) {
		if strings.TrimSpace(envelope.AgentID) != "" || strings.TrimSpace(envelope.AgentProfileID) != "" {
			return ErrInvalidRequest
		}
		return nil
	}
	if agentID := strings.TrimSpace(envelope.AgentID); agentID != "" && agentID != strings.TrimSpace(assignee.StableAgentID.String()) {
		return ErrInvalidRequest
	}
	if profileID := strings.TrimSpace(envelope.AgentProfileID); profileID != "" && profileID != strings.TrimSpace(assignee.ProfileID.String()) {
		return ErrInvalidRequest
	}
	return nil
}

// validateTaskExecutionEnvelopeRevision keeps a durable task snapshot
// immutable while still allowing legacy tasks without a snapshot to be
// upgraded on their next draft revision.
func validateTaskExecutionEnvelopeRevision(existing, incoming *agentcore.ExecutionEnvelope) error {
	if existing == nil {
		return nil
	}
	if err := existing.Verify(); err != nil {
		return fmt.Errorf("%w: stored task execution envelope is invalid", ErrConflict)
	}
	if incoming == nil {
		return fmt.Errorf("%w: task execution envelope is required when revising a snapshotted task", ErrInvalidRequest)
	}
	if err := incoming.Verify(); err != nil {
		return ErrInvalidRequest
	}
	if strings.TrimSpace(existing.EnvelopeDigest) != strings.TrimSpace(incoming.EnvelopeDigest) {
		return fmt.Errorf("%w: task execution envelope cannot be replaced", ErrConflict)
	}
	return nil
}

func runtimeExecutionEnvelope(cmd StartAgentRunCommand, runID string, agentID agentcore.AgentID, profileID agentcore.AgentProfileID, model projection.ReadModel, task projection.TaskCardView, contextPack agentcore.ContextPack, at time.Time) *agentcore.ExecutionEnvelope {
	permissionScopes, ok := boundedExecutionSelectors(cmd.PermissionScope, task.ExecutionPermissionScopes)
	if !ok {
		return nil
	}
	envelope := &agentcore.ExecutionEnvelope{
		TenantID:              missionModelTenantID(model),
		RunID:                 runID,
		MissionID:             cmd.MissionID.String(),
		TaskID:                cmd.TaskID.String(),
		AgentID:               agentID.String(),
		AgentProfileID:        profileID.String(),
		PrincipalID:           agentID.String(),
		PrincipalKind:         agentcore.ExecutionPrincipalAgent,
		PermissionScopes:      permissionScopes,
		AllowedToolIDs:        append([]string(nil), task.ExecutionAllowedToolIDs...),
		DataScopes:            responsibilityContextAllowedDataScopes(contextPack.ResponsibilityContext),
		ResponsibilityContext: agentcore.CloneResponsibilityContext(contextPack.ResponsibilityContext),
	}
	if strings.TrimSpace(task.SourceKind) == "scheduler_agent_task" {
		envelope.ScheduledTaskID = strings.TrimSpace(task.SourceID)
		envelope.ScheduledRunID = strings.TrimSpace(task.SourceStepID)
	}
	if parent := agentcore.CloneExecutionEnvelope(task.ExecutionEnvelope); parent != nil {
		if err := parent.Verify(); err != nil || (!parent.ExpiresAt.IsZero() && !parent.ExpiresAt.After(at.UTC())) {
			return nil
		}
		if strings.TrimSpace(parent.TenantID) != strings.TrimSpace(envelope.TenantID) ||
			(parent.MissionID != "" && strings.TrimSpace(parent.MissionID) != strings.TrimSpace(envelope.MissionID)) ||
			(parent.TaskID != "" && strings.TrimSpace(parent.TaskID) != strings.TrimSpace(envelope.TaskID)) ||
			(parent.AgentID != "" && strings.TrimSpace(parent.AgentID) != strings.TrimSpace(envelope.AgentID)) ||
			(parent.AgentProfileID != "" && strings.TrimSpace(parent.AgentProfileID) != strings.TrimSpace(envelope.AgentProfileID)) {
			return nil
		}
		permissionScopes, ok := boundedExecutionSelectors(parent.PermissionScopes, envelope.PermissionScopes)
		if !ok {
			return nil
		}
		dataScopes, ok := boundedExecutionSelectors(parent.DataScopes, envelope.DataScopes)
		if !ok {
			return nil
		}
		allowedTools, ok := boundedExecutionSelectors(parent.AllowedToolIDs, envelope.AllowedToolIDs)
		if !ok {
			return nil
		}
		envelope.PermissionScopes = permissionScopes
		envelope.DataScopes = dataScopes
		envelope.AllowedToolIDs = allowedTools
		envelope.ForbiddenActions = unionExecutionSelectors(parent.ForbiddenActions, envelope.ForbiddenActions)
		responsibilityContext, ok := boundedExecutionResponsibilityContext(parent.ResponsibilityContext, envelope.ResponsibilityContext)
		if !ok {
			return nil
		}
		if responsibilityContext != nil &&
			((parent.OrganizationID != "" && responsibilityContext.OrganizationID != "" && parent.OrganizationID != responsibilityContext.OrganizationID) ||
				(parent.OrganizationName != "" && responsibilityContext.OrganizationName != "" && parent.OrganizationName != responsibilityContext.OrganizationName)) {
			return nil
		}
		envelope.ResponsibilityContext = responsibilityContext
		envelope.ScheduledTaskID = firstString(parent.ScheduledTaskID, envelope.ScheduledTaskID)
		envelope.ScheduledRunID = firstString(parent.ScheduledRunID, envelope.ScheduledRunID)
		envelope.OrganizationID = parent.OrganizationID
		envelope.OrganizationName = parent.OrganizationName
		envelope.PeopleTenantID = parent.PeopleTenantID
		envelope.PortfolioID = parent.PortfolioID
		envelope.PortfolioBindingID = parent.PortfolioBindingID
		envelope.PortfolioVersionID = parent.PortfolioVersionID
		envelope.ResponsibilityID = parent.ResponsibilityID
		envelope.ResponsibilityVersion = parent.ResponsibilityVersion
		envelope.PolicyID = parent.PolicyID
		envelope.PolicyVersion = parent.PolicyVersion
		envelope.PrincipalID = firstString(parent.PrincipalID, envelope.PrincipalID)
		envelope.PrincipalKind = parent.PrincipalKind
		if envelope.PrincipalKind == "" {
			envelope.PrincipalKind = agentcore.ExecutionPrincipalAgent
		}
		if parent.ExpiresAt.IsZero() == false && (envelope.ExpiresAt.IsZero() || parent.ExpiresAt.Before(envelope.ExpiresAt)) {
			envelope.ExpiresAt = parent.ExpiresAt
		}
	}
	if err := envelope.SealAt(at); err != nil {
		return nil
	}
	return envelope
}

// responsibilityContextAllowedDataScopes keeps the projection access local to
// this package while avoiding a second mutable copy of the context.
func responsibilityContextAllowedDataScopes(value *agentcore.ResponsibilityContext) []string {
	if value == nil {
		return nil
	}
	return append([]string(nil), value.AllowedDataScopes...)
}

func boundedExecutionSelectors(parent, child []string) ([]string, bool) {
	if len(parent) == 0 {
		return append([]string(nil), child...), true
	}
	if len(child) == 0 {
		return append([]string(nil), parent...), true
	}
	result := make([]string, 0, len(child))
	for _, candidate := range child {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		for _, allowed := range parent {
			allowed = strings.TrimSpace(allowed)
			if executionEnvelopeSelectorWithin(candidate, allowed) {
				result = append(result, candidate)
				break
			}
			if executionEnvelopeSelectorWithin(allowed, candidate) {
				result = append(result, allowed)
				break
			}
		}
	}
	return result, len(result) > 0
}

func boundedExecutionResponsibilityContext(parent, child *agentcore.ResponsibilityContext) (*agentcore.ResponsibilityContext, bool) {
	if parent == nil {
		return agentcore.CloneResponsibilityContext(child), true
	}
	if child == nil {
		return agentcore.CloneResponsibilityContext(parent), true
	}
	if !executionEnvelopeStringWithin(child.ContextID, parent.ContextID) && strings.TrimSpace(parent.ContextID) != "" ||
		!executionEnvelopeStringWithin(child.ResponsibilityID, parent.ResponsibilityID) && strings.TrimSpace(parent.ResponsibilityID) != "" ||
		!executionEnvelopeStringWithin(child.PolicyID, parent.PolicyID) && strings.TrimSpace(parent.PolicyID) != "" ||
		!executionEnvelopeStringWithin(child.AgentProfileID, parent.AgentProfileID) && strings.TrimSpace(parent.AgentProfileID) != "" ||
		!executionEnvelopeStringWithin(child.OrganizationID, parent.OrganizationID) && strings.TrimSpace(parent.OrganizationID) != "" ||
		!executionEnvelopeStringWithin(child.OrganizationName, parent.OrganizationName) && strings.TrimSpace(parent.OrganizationName) != "" ||
		(child.ResponsibilityVersion != 0 && parent.ResponsibilityVersion != 0 && child.ResponsibilityVersion != parent.ResponsibilityVersion) ||
		(child.PolicyVersion != 0 && parent.PolicyVersion != 0 && child.PolicyVersion != parent.PolicyVersion) {
		return nil, false
	}
	result := agentcore.CloneResponsibilityContext(parent)
	if result == nil {
		return nil, false
	}
	if result.ContextID == "" {
		result.ContextID = child.ContextID
	}
	if result.ResponsibilityID == "" {
		result.ResponsibilityID = child.ResponsibilityID
		result.ResponsibilityVersion = child.ResponsibilityVersion
	}
	if result.PolicyID == "" {
		result.PolicyID = child.PolicyID
		result.PolicyVersion = child.PolicyVersion
	}
	if result.AgentProfileID == "" {
		result.AgentProfileID = child.AgentProfileID
	}
	if result.OrganizationID == "" {
		result.OrganizationID = child.OrganizationID
	}
	if result.OrganizationName == "" {
		result.OrganizationName = child.OrganizationName
	}
	if result.MissionID == "" {
		result.MissionID = child.MissionID
	}
	if result.TaskID == "" {
		result.TaskID = child.TaskID
	}
	selectors := []struct {
		parent []string
		child  []string
		out    *[]string
	}{
		{result.AllowedTools, child.AllowedTools, &result.AllowedTools},
		{result.AllowedDataScopes, child.AllowedDataScopes, &result.AllowedDataScopes},
		{result.PolicyToolScopes, child.PolicyToolScopes, &result.PolicyToolScopes},
		{result.PolicyDataScopes, child.PolicyDataScopes, &result.PolicyDataScopes},
		{result.TaskPermissionScopes, child.TaskPermissionScopes, &result.TaskPermissionScopes},
	}
	for _, selector := range selectors {
		bounded, ok := boundedExecutionSelectors(selector.parent, selector.child)
		if !ok {
			return nil, false
		}
		*selector.out = bounded
	}
	result.ForbiddenActions = unionExecutionSelectors(result.ForbiddenActions, child.ForbiddenActions)
	result.RequireExplicitToolScopes = result.RequireExplicitToolScopes || child.RequireExplicitToolScopes
	if result.ExpiresAt.IsZero() || (!child.ExpiresAt.IsZero() && child.ExpiresAt.Before(result.ExpiresAt)) {
		result.ExpiresAt = child.ExpiresAt
	}
	return result, true
}

func unionExecutionSelectors(groups ...[]string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0)
	for _, group := range groups {
		for _, value := range group {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	return result
}

func intersectExecutionPermissionScopes(left, right []string) []string {
	leftSet := make(map[string]struct{}, len(left))
	for _, value := range left {
		if value = strings.TrimSpace(value); value != "" {
			leftSet[value] = struct{}{}
		}
	}
	rightSet := make(map[string]struct{}, len(right))
	for _, value := range right {
		if value = strings.TrimSpace(value); value != "" {
			rightSet[value] = struct{}{}
		}
	}
	if len(leftSet) == 0 {
		return append([]string(nil), right...)
	}
	if len(rightSet) == 0 {
		return append([]string(nil), left...)
	}
	out := make([]string, 0, len(leftSet))
	for value := range leftSet {
		if _, ok := rightSet[value]; ok {
			out = append(out, value)
		}
	}
	return out
}

func executionEnvelopeMatchesRun(value *agentcore.ExecutionEnvelope, tenantID, runID string, agentID agentcore.AgentID, profileID agentcore.AgentProfileID, missionID, taskID string, responsibility *agentcore.ResponsibilityContext) bool {
	if value == nil {
		return true
	}
	if strings.TrimSpace(value.TenantID) != strings.TrimSpace(tenantID) ||
		strings.TrimSpace(value.RunID) != strings.TrimSpace(runID) ||
		strings.TrimSpace(value.AgentID) != strings.TrimSpace(agentID.String()) ||
		strings.TrimSpace(value.AgentProfileID) != strings.TrimSpace(profileID.String()) ||
		strings.TrimSpace(value.MissionID) != strings.TrimSpace(missionID) ||
		strings.TrimSpace(value.TaskID) != strings.TrimSpace(taskID) {
		return false
	}
	if responsibility == nil {
		return value.ResponsibilityContext == nil
	}
	claimed := value.ResponsibilityContext
	return claimed != nil && strings.TrimSpace(claimed.ContextID) == strings.TrimSpace(responsibility.ContextID) &&
		strings.TrimSpace(claimed.ResponsibilityID) == strings.TrimSpace(responsibility.ResponsibilityID) &&
		claimed.ResponsibilityVersion == responsibility.ResponsibilityVersion &&
		strings.TrimSpace(claimed.PolicyID) == strings.TrimSpace(responsibility.PolicyID) &&
		claimed.PolicyVersion == responsibility.PolicyVersion
}

func executionEnvelopeWithinBounds(value, bounds *agentcore.ExecutionEnvelope) bool {
	if value == nil {
		return true
	}
	if bounds == nil {
		return false
	}
	if err := value.Verify(); err != nil {
		return false
	}
	if strings.TrimSpace(value.TenantID) != strings.TrimSpace(bounds.TenantID) ||
		strings.TrimSpace(value.RunID) != strings.TrimSpace(bounds.RunID) ||
		strings.TrimSpace(value.AgentID) != strings.TrimSpace(bounds.AgentID) ||
		strings.TrimSpace(value.AgentProfileID) != strings.TrimSpace(bounds.AgentProfileID) ||
		strings.TrimSpace(value.MissionID) != strings.TrimSpace(bounds.MissionID) ||
		strings.TrimSpace(value.TaskID) != strings.TrimSpace(bounds.TaskID) ||
		!executionEnvelopeLineageWithin(value.ScheduledTaskID, bounds.ScheduledTaskID) ||
		!executionEnvelopeLineageWithin(value.ScheduledRunID, bounds.ScheduledRunID) ||
		!executionEnvelopeStringWithin(value.OrganizationID, bounds.OrganizationID) ||
		!executionEnvelopeStringWithin(value.OrganizationName, bounds.OrganizationName) ||
		!executionEnvelopeStringWithin(value.PeopleTenantID, bounds.PeopleTenantID) ||
		!executionEnvelopeStringWithin(value.PortfolioID, bounds.PortfolioID) ||
		!executionEnvelopeStringWithin(value.PortfolioBindingID, bounds.PortfolioBindingID) ||
		!executionEnvelopeStringWithin(value.PortfolioVersionID, bounds.PortfolioVersionID) ||
		!executionEnvelopeStringWithin(value.ResponsibilityID, bounds.ResponsibilityID) ||
		!executionEnvelopeStringWithin(value.PolicyID, bounds.PolicyID) ||
		!executionEnvelopeStringWithin(string(value.PrincipalKind), string(bounds.PrincipalKind)) ||
		!executionEnvelopeStringWithin(value.PrincipalID, bounds.PrincipalID) {
		return false
	}
	if bounds.ExpiresAt.IsZero() == false && (value.ExpiresAt.IsZero() || value.ExpiresAt.After(bounds.ExpiresAt)) {
		return false
	}
	if !executionEnvelopeSelectorsWithin(value.PermissionScopes, bounds.PermissionScopes) ||
		!executionEnvelopeSelectorsWithin(value.DataScopes, bounds.DataScopes) ||
		!executionEnvelopeSelectorsWithin(value.AllowedToolIDs, bounds.AllowedToolIDs) ||
		!executionEnvelopeSelectorsWithin(bounds.ForbiddenActions, value.ForbiddenActions) {
		return false
	}
	if bounds.ResponsibilityID != "" && (value.ResponsibilityID != bounds.ResponsibilityID || value.ResponsibilityVersion != bounds.ResponsibilityVersion) {
		return false
	}
	if bounds.PolicyID != "" && (value.PolicyID != bounds.PolicyID || value.PolicyVersion != bounds.PolicyVersion) {
		return false
	}
	return executionResponsibilityContextWithinBounds(value.ResponsibilityContext, bounds.ResponsibilityContext)
}

func executionResponsibilityContextWithinBounds(value, bounds *agentcore.ResponsibilityContext) bool {
	if value == nil {
		return true
	}
	if bounds == nil {
		return false
	}
	if strings.TrimSpace(value.ContextID) != strings.TrimSpace(bounds.ContextID) ||
		strings.TrimSpace(value.ResponsibilityID) != strings.TrimSpace(bounds.ResponsibilityID) ||
		value.ResponsibilityVersion != bounds.ResponsibilityVersion ||
		strings.TrimSpace(value.PolicyID) != strings.TrimSpace(bounds.PolicyID) ||
		value.PolicyVersion != bounds.PolicyVersion ||
		strings.TrimSpace(value.AgentProfileID) != strings.TrimSpace(bounds.AgentProfileID) ||
		!executionEnvelopeStringWithin(value.OrganizationID, bounds.OrganizationID) ||
		!executionEnvelopeStringWithin(value.OrganizationName, bounds.OrganizationName) ||
		!executionEnvelopeStringWithin(value.MissionID, bounds.MissionID) ||
		!executionEnvelopeStringWithin(value.TaskID, bounds.TaskID) ||
		(!bounds.ExpiresAt.IsZero() && (value.ExpiresAt.IsZero() || value.ExpiresAt.After(bounds.ExpiresAt))) {
		return false
	}
	return executionEnvelopeSelectorsWithin(value.AllowedTools, bounds.AllowedTools) &&
		executionEnvelopeSelectorsWithin(value.AllowedDataScopes, bounds.AllowedDataScopes) &&
		executionEnvelopeSelectorsWithin(value.PolicyToolScopes, bounds.PolicyToolScopes) &&
		executionEnvelopeSelectorsWithin(value.PolicyDataScopes, bounds.PolicyDataScopes) &&
		executionEnvelopeSelectorsWithin(value.TaskPermissionScopes, bounds.TaskPermissionScopes) &&
		executionEnvelopeSelectorsWithin(bounds.ForbiddenActions, value.ForbiddenActions) &&
		(!bounds.RequireExplicitToolScopes || value.RequireExplicitToolScopes)
}

func executionEnvelopeStringWithin(value, bounds string) bool {
	value = strings.TrimSpace(value)
	bounds = strings.TrimSpace(bounds)
	return value == bounds
}

func executionEnvelopeLineageWithin(value, bounds string) bool {
	value = strings.TrimSpace(value)
	bounds = strings.TrimSpace(bounds)
	return bounds == "" || value == bounds
}

func executionEnvelopeSelectorsWithin(values, bounds []string) bool {
	if len(bounds) == 0 {
		return len(values) == 0
	}
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		allowed := false
		for _, bound := range bounds {
			if executionEnvelopeSelectorWithin(strings.TrimSpace(value), strings.TrimSpace(bound)) {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	return true
}

func executionEnvelopeSelectorWithin(value, bound string) bool {
	if value == "" || bound == "" {
		return false
	}
	if bound == "*" || value == bound {
		return true
	}
	if strings.HasSuffix(bound, "*") && strings.HasPrefix(value, strings.TrimSuffix(bound, "*")) {
		return true
	}
	return strings.HasSuffix(value, "*") == false && strings.HasPrefix(value, bound+".")
}
