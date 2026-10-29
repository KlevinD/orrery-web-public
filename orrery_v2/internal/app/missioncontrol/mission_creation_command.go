package missioncontrol

import "strings"

type CreateMissionCommand struct {
	MissionID                             string
	RequestID                             string
	IdempotencyKey                        string
	Title                                 string
	FinalGoal                             string
	BusinessDirection                     string
	RequesterID                           string
	InitialHumanAgentID                   string
	InitialHumanDisplayName               string
	InitialAgentProfileID                 string
	OriginKind                            string
	OriginID                              string
	OriginTodoID                          string
	ExecutionAttempt                      int
	DiscussionMode                        string
	OriginDevelopmentProjectID            string
	ExistingConversationID                string
	SourcePersonalAssistantConversationID string
}

// PersonalAssistantMissionOrigin carries provenance that has already been
// resolved from a persisted discussion by server-side code. It is deliberately
// separate from CreateMissionCommand, which remains a transport-facing value.
type PersonalAssistantMissionOrigin struct {
	ownerID                string
	authorizationSubjectID string
}

func NewPersonalAssistantMissionOrigin(ownerID, authorizationSubjectID string) (PersonalAssistantMissionOrigin, error) {
	ownerID = strings.TrimSpace(ownerID)
	authorizationSubjectID = strings.TrimSpace(authorizationSubjectID)
	if ownerID == "" || authorizationSubjectID == "" {
		return PersonalAssistantMissionOrigin{}, ErrInvalidRequest
	}
	return PersonalAssistantMissionOrigin{ownerID: ownerID, authorizationSubjectID: authorizationSubjectID}, nil
}

func (o PersonalAssistantMissionOrigin) owner() string {
	return strings.TrimSpace(o.ownerID)
}

func (o PersonalAssistantMissionOrigin) subject() string {
	return strings.TrimSpace(o.authorizationSubjectID)
}

func (o PersonalAssistantMissionOrigin) isZero() bool {
	return o.owner() == "" && o.subject() == ""
}

func (o PersonalAssistantMissionOrigin) valid() bool {
	return o.owner() != "" && o.subject() != ""
}
