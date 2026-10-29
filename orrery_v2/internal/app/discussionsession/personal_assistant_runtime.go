package discussionsession

import "strings"

// Missions remain group conversations; this marker carries only the
// user-facing personal-assistant runtime origin.
func isPersonalAssistantRuntimeConversation(record ConversationRecord) bool {
	return isPersonalAssistantConversationKind(record.ConversationKind) ||
		(strings.TrimSpace(record.PersonalAssistantOriginOwnerID) != "" &&
			strings.TrimSpace(record.PersonalAssistantOriginSubjectID) != "")
}

func isPersonalAssistantRuntimeRequest(request DiscussionAgentRequest) bool {
	return isPersonalAssistantConversationKind(request.ConversationKind) ||
		(strings.TrimSpace(request.PersonalAssistantOriginOwnerID) != "" &&
			strings.TrimSpace(request.PersonalAssistantOriginSubjectID) != "")
}

// IsPersonalAssistantRuntimeRequest reports whether a request carries a
// personal-assistant runtime marker, including a PA-origin Mission group.
func IsPersonalAssistantRuntimeRequest(request *DiscussionAgentRequest) bool {
	return request != nil && isPersonalAssistantRuntimeRequest(*request)
}

func isPersonalAssistantRuntimeRun(run RunRecord) bool {
	if !IsPersonalAssistantAgentID(run.LogicalAgentID) {
		return false
	}
	// A PA logical id is host provenance, but an approval/mention target can
	// carry the blocked run's id while still being dispatched as a worker.
	// Only the default group host may present as the assistant.
	switch strings.ToLower(strings.TrimSpace(run.Mode)) {
	case "", string(discussionTurnTargetGroup), discussionApprovalContinuationMode:
		return true
	default:
		return false
	}
}

func personalAssistantRuntimeOwner(record ConversationRecord) string {
	return firstNonBlank(record.PersonalAssistantOriginOwnerID, record.OwnerID, record.CreatedBy)
}
