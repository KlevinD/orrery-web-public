package main

import (
	"orrery_v2/internal/app/discussionsession"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

type productionDiscussionServiceRepository interface {
	SetDiscussionService(*discussionsession.Service)
}

type productionCanonicalConversationMembershipSetter interface {
	SetCanonicalConversationMembership(missioncontrol.CanonicalConversationMembershipPort)
}

func bindProductionDiscussionSession(
	api *productionAPI,
	approvalWiring productionHILAutoplanWiring,
	session *discussionsession.Service,
	missionRepo productionDiscussionServiceRepository,
	lifecycle productionCanonicalConversationMembershipSetter,
	notifier *productionAgentResultNotifier,
) {
	if api != nil {
		api.discussions = session
		if session != nil {
			session.SetExecutionDeleter(productionDiscussionExecutionDeleter{api: *api})
		}
	}
	approvalWiring.AttachDiscussionService(session)
	if missionRepo != nil {
		missionRepo.SetDiscussionService(session)
	}
	if lifecycle != nil {
		lifecycle.SetCanonicalConversationMembership(session)
	}
	if notifier != nil {
		notifier.SetCanonical(session)
	}
}
