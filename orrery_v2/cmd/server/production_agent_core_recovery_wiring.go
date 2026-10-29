package main

import (
	runtimesession "orrery_v2/internal/agent/runtime/session"
	"orrery_v2/internal/app/missioncontrol"
)

// Wire the task guard and result commands before registering any worker. The
// API inspector and every AgentCore runner use the same persistent store.
func configureProductionRuntimeTaskCommands(deps *productionDiscussionAgentDependencies, command *missioncontrol.CommandService) (*runtimesession.ConnectionRegistry, agentCoreRecoveryStore) {
	var recovery agentCoreRecoveryStore
	if store, ok := deps.KernelCheckpoints.(agentCoreRecoveryStore); ok {
		recovery = store
		command.SetRuntimeTaskRestartGuard(productionAgentCoreRestartGuard{store: store})
	}
	if committer, ok := deps.Committer.(*discussionAgentConversationResultCommitter); ok {
		committer.commands = command
	}
	registry := newProductionRuntimeRegistry(*deps)
	deps.RuntimeRegistry = registry
	return registry, recovery
}
