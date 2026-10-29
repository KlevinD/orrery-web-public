package discussionsession

import "strings"

func runtimeHILApprovalAssignee(explicit string, conversation ConversationRecordView) string {
	if assignedTo := strings.TrimSpace(explicit); assignedTo != "" {
		return assignedTo
	}
	if subjectID := strings.TrimSpace(conversation.PersonalAssistantOriginSubjectID); subjectID != "" {
		return subjectID
	}
	creatorID := strings.TrimSpace(conversation.CreatedBy)
	if creatorID == "" {
		return ""
	}
	for _, member := range conversation.Members {
		if !strings.EqualFold(strings.TrimSpace(member.MemberID), creatorID) {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(member.MemberKind)) {
		case string(ViewerKindUser), string(ViewerKindHuman):
			return strings.TrimSpace(member.MemberID)
		}
	}
	return ""
}
