package resourceadmin

import (
	"context"
	"sync"

	"orrery_v2/internal/app/authorization"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

type CommandService struct {
	tools            toolres.Registry
	skills           skillres.Registry
	skillStore       *skillres.PackageStore
	skillBundles     skillres.BundleRegistry
	profiles         AgentProfileBindingStorePort
	audit            AuditRecorderPort
	mcpRuntime       MCPRuntimePort
	ids              ResourceIDGeneratorPort
	authz            *authorization.RelationProjector
	skillEvaluations SkillVersionEvaluationStore
	skillVersionMu   sync.Mutex
	sourceDeletion   SourceDeletionLifecyclePort
	skillReferences  sync.Mutex
}

type SourceDeletionLifecyclePort interface {
	PrepareSourceDeletion(ctx context.Context, sourceType, sourceID string, canonicalTenantIDs []string, changedBy string) (func(context.Context) error, error)
}

func NewCommandService(tools toolres.Registry, skills skillres.Registry) *CommandService {
	return &CommandService{tools: tools, skills: skills, ids: NewPrefixedResourceIDGenerator()}
}

func (s *CommandService) SetAuditRecorder(recorder AuditRecorderPort) {
	if s != nil {
		s.audit = recorder
	}
}

func (s *CommandService) SetAgentProfileBindingStore(store AgentProfileBindingStorePort) {
	if s != nil {
		s.profiles = store
	}
}

func (s *CommandService) SetMCPRuntime(runtime MCPRuntimePort) {
	if s != nil {
		s.mcpRuntime = runtime
	}
}

func (s *CommandService) SetIDGenerator(generator ResourceIDGeneratorPort) {
	if s != nil {
		s.ids = generator
	}
}

func (s *CommandService) SetSkillPackageStore(store *skillres.PackageStore) {
	if s != nil {
		s.skillStore = store
	}
}

func (s *CommandService) SetSkillBundleRegistry(registry skillres.BundleRegistry) {
	if s != nil {
		s.skillBundles = registry
	}
}

func (s *CommandService) SetAuthorizationRelations(projector *authorization.RelationProjector) {
	if s != nil {
		s.authz = projector
	}
}

func (s *CommandService) SetSourceDeletionLifecycle(lifecycle SourceDeletionLifecyclePort) {
	if s != nil {
		s.sourceDeletion = lifecycle
	}
}
