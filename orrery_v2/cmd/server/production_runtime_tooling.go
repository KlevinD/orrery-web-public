package main

import (
	"orrery_v2/internal/app/discussionsession"
	memorydiaryapp "orrery_v2/internal/app/memorydiary"
	"orrery_v2/internal/decision"
	"orrery_v2/internal/platform"
)

func newProductionRuntimeTooling(rt *platform.Runtime, repo *discussionsession.Repository, tools discussionsession.MissionToolingPort, agents discussionsession.AgentDirectoryPort, skills productionSkillToolExecutor, diary *memorydiaryapp.Coordinator, shadow ...*decision.Observer) (*productionMCPGatewayRef, productionRuntimeToolDispatcher) {
	gateway := &productionMCPGatewayRef{}
	dispatcher := productionRuntimeToolDispatcher{
		repo:          repo,
		tools:         tools,
		agents:        agents,
		skills:        skills,
		personalShell: productionPersonalAssistantShellExecutor{},
		mcpGateway:    gateway,
		diary:         diary,
	}
	if rt != nil {
		dispatcher.clock = rt.Clock
		dispatcher.ids = rt.IDGenerator
	}
	if len(shadow) > 0 {
		gateway.layaShadow = shadow[0]
		dispatcher.layaShadow = shadow[0]
		if shadow[0] != nil && rt != nil {
			dispatcher.rsiSamples, _ = productionRSISampleStore(rt)
			dispatcher.rsiLogger = rt.Logger
		}
	}
	return gateway, dispatcher
}
