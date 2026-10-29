package discussionsession

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

const personalAssistantAgentIDPrefix = "personal-assistant-"

func RegisterPersonalAssistantAgentID(tenantID, ownerID, agentID string) error {
	return agentcore.RegisterPersonalAssistantProfileID(tenantID, ownerID, agentID)
}

func PersonalAssistantAgentID(tenantID, ownerID string) string {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return ""
	}
	tenantID = firstNonBlank(tenantID, "global")
	if registered := agentcore.PersonalAssistantProfileIDForOwner(tenantID, ownerID); registered != "" {
		return registered
	}
	digest := sha256.Sum256([]byte(tenantID + "\x00" + ownerID))
	return personalAssistantAgentIDPrefix + hex.EncodeToString(digest[:])
}

func PersonalAssistantMemoryProfileID(agentID string) string {
	agentID = strings.TrimSpace(agentID)
	if !IsPersonalAssistantAgentID(agentID) {
		return ""
	}
	return "memory-" + agentID
}

func IsPersonalAssistantAgentID(agentID string) bool {
	return agentcore.IsPersonalAssistantProfileID(agentID)
}

func ensurePersonalAssistantRequestIdentity(request *DiscussionAgentRequest) string {
	if request == nil || !isPersonalAssistantRuntimeRequest(*request) {
		return ""
	}
	if IsPersonalAssistantAgentID(request.LogicalAgentID) {
		return request.LogicalAgentID
	}
	request.LogicalAgentID = PersonalAssistantAgentID(
		firstNonBlank(request.ConversationTenantID, request.Viewer.TenantID, "global"),
		firstNonBlank(request.PersonalAssistantOriginOwnerID, request.ConversationOwnerID, request.Viewer.OwnerID, request.Viewer.ViewerID),
	)
	return request.LogicalAgentID
}
