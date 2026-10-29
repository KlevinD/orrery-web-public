package operations

import (
	"context"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/knowledgeadmin"
	knowledgecore "orrery_v2/internal/knowledge/core"
	memcore "orrery_v2/internal/memory/core"
	"orrery_v2/internal/platform/idgen"
	skillres "orrery_v2/internal/resource/skill"
)

type WorkforceProfileStorePort interface {
	GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error)
	SaveProfile(ctx context.Context, profile agentcore.AgentProfile) error
	SaveAvailability(ctx context.Context, availability agentcore.AgentAvailability) error
}

type WorkforceAvailabilityLookupPort interface {
	GetAvailability(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentAvailability, error)
}

type WorkforceProfileDirectoryPort interface {
	QueryDirectory(ctx context.Context, q agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error)
}

type WorkforceProfileDeletePort interface {
	DeleteProfile(ctx context.Context, id agentcore.AgentProfileID, reason string) error
}

type WorkforceSessionRevokerPort interface {
	RevokeSessionsForUsername(ctx context.Context, username string) error
}

type WorkforceAccountProjectionResult struct {
	Status           string
	Revision         int64
	KratosIdentityID string
	InitialPassword  string
}

type WorkforceAccountProjectorPort interface {
	ProjectLatest(ctx context.Context, personID string) (WorkforceAccountProjectionResult, error)
}

type MemoryProfileStorePort interface {
	SaveProfile(ctx context.Context, profile memcore.MemoryProfile) error
	GetProfile(ctx context.Context, id agentcore.MemoryProfileID) (memcore.MemoryProfile, error)
}

type CommandService struct {
	profiles               WorkforceProfileStorePort
	directory              WorkforceProfileDirectoryPort
	audit                  OperationsAuditRecorderPort
	orgs                   OrganizationCatalogStorePort
	roles                  RoleStorePort
	ids                    EntityIDGeneratorPort
	skills                 skillres.Registry
	skillStore             *skillres.PackageStore
	memory                 MemoryProfileStorePort
	knowledge              KnowledgeLifecyclePort
	sessionRevoker         WorkforceSessionRevokerPort
	accountProjector       WorkforceAccountProjectorPort
	peopleSSOEnabled       bool
	authorizationRelations authorizationRelationWriter
	authorizationChecker   AuthorizationCheckerPort
	sourceDeletion         SourceDeletionLifecyclePort
}

func NewCommandService(profiles WorkforceProfileStorePort) *CommandService {
	service := &CommandService{profiles: profiles, ids: NewPrefixedIDGenerator()}
	if directory, ok := profiles.(WorkforceProfileDirectoryPort); ok {
		service.directory = directory
	}
	return service
}

func (s *CommandService) NewWorkforceMemberID() (string, error) {
	if s == nil {
		return "", ErrServiceDependency
	}
	return s.newID("profile")
}

type OperationsAuditRecorderPort interface {
	RecordAuditEvent(ctx context.Context, event AuditEventDTO) error
}

type OrganizationCatalogStorePort interface {
	SaveOrganization(ctx context.Context, organization OrganizationDTO) error
	SaveDepartment(ctx context.Context, department DepartmentDTO) error
	SavePosition(ctx context.Context, position PositionDTO) error
	DeleteOrganization(ctx context.Context, organizationID string) error
	DeleteDepartment(ctx context.Context, departmentID string) error
	DeletePosition(ctx context.Context, positionID string) error
	ListOrganizations(ctx context.Context, query OrgGraphQuery) (OrgGraphDTO, error)
}

type EntityIDGeneratorPort interface {
	NewID(prefix string) (string, error)
}

type KnowledgeLifecyclePort interface {
	CreateKnowledgeBase(ctx context.Context, viewer knowledgeadmin.ViewerRef, cmd knowledgeadmin.CreateKnowledgeBaseCommand) (knowledgeadmin.KnowledgeBaseDTO, error)
	DeleteKnowledgeBase(ctx context.Context, viewer knowledgeadmin.ViewerRef, id string) error
}

type SourceDeletionLifecyclePort interface {
	PrepareSourceDeletion(ctx context.Context, sourceType, sourceID string, canonicalTenantIDs []string, changedBy string) (func(context.Context) error, error)
}

type PrefixedIDGenerator struct{}

func NewPrefixedIDGenerator() PrefixedIDGenerator {
	return PrefixedIDGenerator{}
}

func (PrefixedIDGenerator) NewID(prefix string) (string, error) {
	return idgen.NewRandom(prefix).NewIDContext()
}

func (s *CommandService) SetAuditRecorder(recorder OperationsAuditRecorderPort) {
	if s != nil {
		s.audit = recorder
	}
}

func (s *CommandService) SetOrganizationCatalogStore(store OrganizationCatalogStorePort) {
	if s != nil {
		s.orgs = store
	}
}

func (s *CommandService) SetIDGenerator(generator EntityIDGeneratorPort) {
	if s != nil {
		s.ids = generator
	}
}

func (s *CommandService) SetSkillRegistry(registry skillres.Registry) {
	if s != nil {
		s.skills = registry
	}
}

func (s *CommandService) SetSkillPackageStore(store *skillres.PackageStore) {
	if s != nil {
		s.skillStore = store
	}
}

func (s *CommandService) SetMemoryProfileStore(store MemoryProfileStorePort) {
	if s != nil {
		s.memory = store
	}
}

func (s *CommandService) SetKnowledgeLifecycle(lifecycle KnowledgeLifecyclePort) {
	if s != nil {
		s.knowledge = lifecycle
	}
}

func (s *CommandService) SetWorkforceSessionRevoker(revoker WorkforceSessionRevokerPort) {
	if s != nil {
		s.sessionRevoker = revoker
	}
}

func (s *CommandService) SetWorkforceAccountProjector(projector WorkforceAccountProjectorPort) {
	if s != nil {
		s.accountProjector = projector
	}
}

func (s *CommandService) SetPeopleSSOEnabled(enabled bool) {
	if s != nil {
		s.peopleSSOEnabled = enabled
	}
}

func (s *CommandService) SetSourceDeletionLifecycle(lifecycle SourceDeletionLifecyclePort) {
	if s != nil {
		s.sourceDeletion = lifecycle
	}
}

func (s *CommandService) SetProfileDirectory(directory WorkforceProfileDirectoryPort) {
	if s != nil {
		s.directory = directory
	}
}

func operationsKnowledgeViewer(viewer ViewerRef, ownerType knowledgecore.OwnerType, ownerID string) knowledgeadmin.ViewerRef {
	kviewer := knowledgeadmin.ViewerRef{
		ViewerID:        firstNonEmpty(viewer.ViewerID, "operations-lifecycle"),
		PermissionScope: []string{knowledgeadmin.PermissionKnowledgeManage},
		RequestID:       viewer.RequestID,
		IdempotencyKey:  viewer.IdempotencyKey,
	}
	switch ownerType {
	case knowledgecore.OwnerAgent:
		kviewer.AgentIDs = []string{ownerID}
	case knowledgecore.OwnerHuman:
		kviewer.HumanIDs = []string{ownerID}
	case knowledgecore.OwnerOrganization:
		kviewer.OrganizationIDs = []string{ownerID}
	case knowledgecore.OwnerPosition:
		kviewer.PositionIDs = []string{ownerID}
	}
	return kviewer
}
