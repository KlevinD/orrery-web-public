package main

import (
	"strings"

	"orrery_v2/internal/agent/runtime/agentkernel"
	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/discussionsession"
	memorydiaryapp "orrery_v2/internal/app/memorydiary"
	memaudit "orrery_v2/internal/memory/audit"
	memcapture "orrery_v2/internal/memory/capture"
	memstore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
)

func firstNonEmptyText(values ...string) string {
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			return text
		}
	}
	return ""
}

func configureProductionDiscussionAgentDependencies(
	deps productionDiscussionAgentDependencies,
	rt *platform.Runtime,
	settings productionSettingsStore,
	dispatcher productionRuntimeToolDispatcher,
	memory *memstore.PlatformStore,
	audit *memaudit.EventRecorder,
	metadata memcapture.RecordMetadataSink,
	approvals *discussionsession.RuntimeApprovalCoordinator,
	diary *memorydiaryapp.Coordinator,
) productionDiscussionAgentDependencies {
	deps.Config = rt.Config
	deps.LLMProfiles = settings
	deps.RuntimeProfiles = settings
	deps.WorkspaceRoot = settings
	deps.PromptBlocks = settings
	if core, err := rt.Databases.Store(platform.DatabaseRoleCore); err == nil {
		deps.ActionSemantics = actioncontract.NewSemanticInterpreter(actioncontract.NewPlatformRepository(core))
	}
	deps.MemorySettings = settings
	deps.Secrets = platformSecretResolver{provider: rt.Secrets}
	deps.ToolDispatcher = dispatcher
	runtimeStore, err := rt.Databases.Store(platform.DatabaseRoleRuntime)
	if err != nil {
		panic(err)
	}
	if runtimeStore.Driver() != db.DriverMemory {
		deps.KernelJournal, err = agentkernel.NewSQLToolJournal(runtimeStore)
		if err != nil {
			panic(err)
		}
		deps.KernelCheckpoints, err = agentkernel.NewSQLCheckpointStore(runtimeStore)
		if err != nil {
			panic(err)
		}
	}
	deps.MemoryRepository = memory
	deps.MemoryAudit = audit
	deps.MemoryMetadata = metadata
	deps.ApprovalBroker = approvals
	deps.DiaryFlush = diary
	return deps
}
