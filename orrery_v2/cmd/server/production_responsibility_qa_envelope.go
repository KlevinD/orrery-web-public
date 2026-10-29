package main

import (
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/responsibility"
	missiontask "orrery_v2/internal/mission/task"
)

// qaChildExecutionContract carries the parent's immutable execution boundary
// into a QA task while giving that task its own Agent identity and responsibility
// lifecycle. A missing parent envelope remains compatible with legacy tasks.
func qaChildExecutionContract(parent missiontask.TaskRecord, tenantID, missionID, childTaskID, agentID, sourceKind string) ([]string, []string, *agentcore.ExecutionEnvelope, error) {
	tenantID = strings.TrimSpace(tenantID)
	missionID = strings.TrimSpace(missionID)
	childTaskID = strings.TrimSpace(childTaskID)
	agentID = strings.TrimSpace(agentID)
	sourceKind = strings.TrimSpace(sourceKind)
	if tenantID == "" || missionID == "" || childTaskID == "" || agentID == "" || sourceKind == "" {
		return nil, nil, nil, responsibility.ErrInvalidContract
	}

	parentEnvelope := agentcore.CloneExecutionEnvelope(parent.Contract.ExecutionEnvelope)
	scopes := append([]string(nil), parent.Contract.ExecutionPermissionScopes...)
	tools := append([]string(nil), parent.Contract.ExecutionAllowedToolIDs...)
	if parentEnvelope != nil {
		if err := parentEnvelope.Verify(); err != nil {
			return nil, nil, nil, fmt.Errorf("%w: invalid parent QA execution envelope: %v", responsibility.ErrInvalidContract, err)
		}
		if parentEnvelope.TenantID != tenantID ||
			(parentEnvelope.MissionID != "" && parentEnvelope.MissionID != missionID) ||
			(parentEnvelope.TaskID != "" && parentEnvelope.TaskID != parent.Contract.ID.String()) {
			return nil, nil, nil, fmt.Errorf("%w: parent QA execution envelope identity mismatch", responsibility.ErrInvalidContract)
		}
		var ok bool
		scopes, ok = qaBoundedExecutionSelectors(parentEnvelope.PermissionScopes, scopes)
		if !ok {
			return nil, nil, nil, fmt.Errorf("%w: QA permission scopes exceed parent execution envelope", responsibility.ErrInvalidContract)
		}
		tools, ok = qaBoundedExecutionSelectors(parentEnvelope.AllowedToolIDs, tools)
		if !ok {
			return nil, nil, nil, fmt.Errorf("%w: QA tool scopes exceed parent execution envelope", responsibility.ErrInvalidContract)
		}

		child := parentEnvelope
		child.RunID = "qa:" + sourceKind + ":" + childTaskID
		child.EnvelopeID = ""
		child.EnvelopeDigest = ""
		child.MissionID = missionID
		child.TaskID = childTaskID
		child.AgentID = agentID
		child.AgentProfileID = agentID
		child.PrincipalID = agentID
		child.PrincipalKind = agentcore.ExecutionPrincipalAgent
		child.PermissionScopes = append([]string(nil), scopes...)
		child.AllowedToolIDs = append([]string(nil), tools...)
		child.ResponsibilityID = ""
		child.ResponsibilityVersion = 0
		child.PolicyID = ""
		child.PolicyVersion = 0
		child.ResponsibilityContext = nil
		if err := child.SealAt(parentEnvelope.IssuedAt); err != nil {
			return nil, nil, nil, fmt.Errorf("%w: seal QA child execution envelope: %v", responsibility.ErrInvalidContract, err)
		}
		return scopes, tools, child, nil
	}

	return scopes, tools, nil, nil
}

func qaBoundedExecutionSelectors(parent, child []string) ([]string, bool) {
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
			if qaExecutionSelectorWithin(candidate, allowed) {
				result = append(result, candidate)
				break
			}
			if qaExecutionSelectorWithin(allowed, candidate) {
				result = append(result, allowed)
				break
			}
		}
	}
	return result, len(result) > 0
}

func qaExecutionSelectorWithin(value, bound string) bool {
	value, bound = strings.TrimSpace(value), strings.TrimSpace(bound)
	if value == "" || bound == "" {
		return false
	}
	if bound == "*" || value == bound {
		return true
	}
	if strings.HasSuffix(bound, "*") && strings.HasPrefix(value, strings.TrimSuffix(bound, "*")) {
		return true
	}
	return !strings.HasSuffix(value, "*") && strings.HasPrefix(value, bound+".")
}
