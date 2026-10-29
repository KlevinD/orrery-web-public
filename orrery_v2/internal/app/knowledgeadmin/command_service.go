package knowledgeadmin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/ingest"
	"orrery_v2/internal/knowledge/provider"
	localprovider "orrery_v2/internal/knowledge/provider/local"
	"orrery_v2/internal/knowledge/store"
	"orrery_v2/internal/knowledge/vector"
	doccore "orrery_v2/internal/platform/document/core"
	"orrery_v2/internal/platform/graphprotocol"
)

type IDGenerator interface {
	NewID(prefix string) string
}

type Clock interface {
	Now() time.Time
}

type ArtifactRef struct {
	ID          string
	Name        string
	ContentType string
	SizeBytes   int64
	Checksum    string
}

type ArtifactWriter interface {
	PutArtifact(ctx context.Context, name, contentType string, reader io.Reader) (ArtifactRef, error)
}

type ExecutionSubmitter interface {
	Submit(ctx context.Context, topic string, payload []byte, maxAttempts int) error
}

type KnowledgeDocumentMetadataSink interface {
	PublishKnowledgeDocumentMetadata(ctx context.Context, event graphprotocol.DomainEvent) error
}

type KnowledgePageMetadataSink interface {
	PublishKnowledgePageMetadata(ctx context.Context, event graphprotocol.DomainEvent) error
}

type KnowledgeAnchorMetadataSink interface {
	PublishKnowledgeAnchorMetadata(ctx context.Context, event graphprotocol.DomainEvent) error
}

type CatalogReconcileService interface {
	Reconcile(context.Context, core.KnowledgeBase) (CatalogReconcileResult, error)
}

type CommandService struct {
	repo               store.Repository
	vector             vector.Store
	ids                IDGenerator
	clock              Clock
	defaultVectorRoot  string
	defaultChunkPolicy doccore.ChunkPolicy
	ingestPolicy       IngestPolicy
	policy             *AccessPolicy
	artifacts          ArtifactWriter
	execution          ExecutionSubmitter
	providers          provider.Registry
	metadataSink       KnowledgeDocumentMetadataSink
	catalog            CatalogReconcileService
}

func NewCommandService(repo store.Repository, vectorStore vector.Store, ids IDGenerator, clock Clock) *CommandService {
	return &CommandService{repo: repo, vector: vectorStore, ids: ids, clock: clock, defaultChunkPolicy: defaultChunkPolicy(), policy: NewAccessPolicy(repo)}
}

func (s *CommandService) WithArtifactStore(artifacts ArtifactWriter) *CommandService {
	s.artifacts = artifacts
	return s
}

func (s *CommandService) WithExecutionQueue(execution ExecutionSubmitter) *CommandService {
	s.execution = execution
	return s
}

func (s *CommandService) WithDefaultChunkPolicy(policy doccore.ChunkPolicy) *CommandService {
	s.defaultChunkPolicy = policy
	return s
}

func (s *CommandService) WithIngestPolicy(policy IngestPolicy) *CommandService {
	s.ingestPolicy = policy
	return s
}

func (s *CommandService) WithDefaultVectorRoot(root string) *CommandService {
	s.defaultVectorRoot = root
	return s
}

func (s *CommandService) WithProviderRegistry(registry provider.Registry) *CommandService {
	s.providers = registry
	if s.catalog == nil && s.repo != nil && s.clock != nil && registry != nil {
		s.catalog = NewCatalogReconciler(s.repo, registry, s.clock)
	}
	return s
}

func (s *CommandService) WithDocumentMetadataSink(sink KnowledgeDocumentMetadataSink) *CommandService {
	s.metadataSink = sink
	return s
}

func (s *CommandService) WithCatalogReconciler(catalog CatalogReconcileService) *CommandService {
	s.catalog = catalog
	return s
}

func (s *CommandService) SyncKnowledgeDocuments(ctx context.Context, viewer ViewerRef, knowledgeBaseID string) (CatalogReconcileResult, error) {
	if err := requirePermission(viewer, PermissionKnowledgeManage); err != nil {
		return CatalogReconcileResult{}, err
	}
	if s == nil || s.repo == nil || s.policy == nil || s.catalog == nil {
		return CatalogReconcileResult{}, ErrServiceDependency
	}
	base, err := s.repo.GetKnowledgeBase(ctx, core.KnowledgeBaseID(strings.TrimSpace(knowledgeBaseID)))
	if err != nil {
		return CatalogReconcileResult{}, err
	}
	if !s.canManageBase(ctx, viewer, base) {
		return CatalogReconcileResult{}, ErrPermissionDenied
	}
	if base.Status != core.KnowledgeActive || base.EffectiveProvider() != core.ProviderPageIndex || strings.TrimSpace(base.ProviderResourceID) == "" {
		return CatalogReconcileResult{}, ErrInvalidRequest
	}
	return s.catalog.Reconcile(ctx, base)
}

func (s *CommandService) Reconcile(ctx context.Context, base core.KnowledgeBase) (CatalogReconcileResult, error) {
	if s == nil || s.catalog == nil {
		return CatalogReconcileResult{}, ErrServiceDependency
	}
	return s.catalog.Reconcile(ctx, base)
}

func (s *CommandService) CreateKnowledgeBase(ctx context.Context, viewer ViewerRef, cmd CreateKnowledgeBaseCommand) (KnowledgeBaseDTO, error) {
	if err := requirePermission(viewer, PermissionKnowledgeManage); err != nil {
		return KnowledgeBaseDTO{}, err
	}
	ctx = withProviderRequestMetadata(ctx, viewer)
	if err := s.requireDeps(); err != nil {
		return KnowledgeBaseDTO{}, err
	}
	now := s.clock.Now()
	if cmd.ChunkPolicy.MaxRunes == 0 {
		cmd.ChunkPolicy = s.configuredDefaultChunkPolicy()
	}
	vectorRoot := s.configuredVectorRoot(cmd.VectorRoot)
	providerName := core.KnowledgeProvider(strings.TrimSpace(cmd.Provider))
	if providerName == "" {
		providerName = s.defaultProviderName()
	}
	base := core.KnowledgeBase{
		ID:                  core.KnowledgeBaseID(s.ids.NewID("kb")),
		Name:                cmd.Name,
		Description:         cmd.Description,
		OwnerType:           core.OwnerType(cmd.OwnerType),
		OwnerID:             ownerIDForCreate(viewer, core.OwnerType(cmd.OwnerType), cmd.OwnerID),
		Visibility:          core.Visibility(cmd.Visibility),
		Provider:            providerName,
		EmbeddingProfileRef: cmd.EmbeddingProfileRef,
		EmbeddingModel:      cmd.EmbeddingModel,
		EmbeddingDimensions: cmd.EmbeddingDimensions,
		ChunkPolicy:         cmd.ChunkPolicy,
		Status:              core.KnowledgeActive,
		CreatedBy:           viewer.ViewerID,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	kp := s.providerForBase(base)
	if kp == nil {
		return KnowledgeBaseDTO{}, ErrServiceDependency
	}
	ensured, err := kp.EnsureKnowledgeBase(ctx, provider.EnsureKnowledgeBaseRequest{Base: base, VectorRoot: vectorRoot})
	if err != nil {
		return KnowledgeBaseDTO{}, err
	}
	base.ProviderResourceID = ensured.ProviderResourceID
	base.ProviderMetadata = ensured.Metadata
	base.VectorStorePath = ensured.VectorStorePath
	if base.EffectiveProvider() == core.ProviderLocal && base.VectorBackend == "" {
		base.VectorBackend = core.VectorBackendLanceDB
	}
	if err := s.repo.SaveKnowledgeBase(ctx, base); err != nil {
		return KnowledgeBaseDTO{}, err
	}
	return mapBase(base), nil
}

func ownerIDForCreate(viewer ViewerRef, ownerType core.OwnerType, explicit string) string {
	if strings.TrimSpace(explicit) != "" || ownerType == core.OwnerGlobal {
		return strings.TrimSpace(explicit)
	}
	switch ownerType {
	case core.OwnerAgent:
		return firstIdentity(viewer.AgentIDs)
	case core.OwnerHuman:
		return firstCreateOwnerID(firstIdentity(viewer.HumanIDs), viewer.ViewerID)
	case core.OwnerOrganization:
		return firstIdentity(viewer.OrganizationIDs)
	case core.OwnerPosition:
		return firstIdentity(viewer.PositionIDs)
	default:
		return ""
	}
}

func firstCreateOwnerID(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstIdentity(values []string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (s *CommandService) DeleteKnowledgeBase(ctx context.Context, viewer ViewerRef, id string) error {
	if err := requirePermission(viewer, PermissionKnowledgeManage); err != nil {
		return err
	}
	ctx = withProviderRequestMetadata(ctx, viewer)
	if err := s.requireDeps(); err != nil {
		return err
	}
	base, err := s.repo.GetKnowledgeBase(ctx, core.KnowledgeBaseID(id))
	if err != nil {
		return err
	}
	if !s.canManageBase(ctx, viewer, base) {
		return ErrPermissionDenied
	}
	if err := s.repo.MarkKnowledgeBaseDeleting(ctx, base.ID, s.clock.Now()); err != nil {
		return err
	}
	kp := s.providerForBase(base)
	if kp == nil {
		return ErrServiceDependency
	}
	if err := kp.DeleteKnowledgeBase(ctx, provider.DeleteKnowledgeBaseRequest{Base: base}); err != nil {
		return err
	}
	return s.repo.DeleteKnowledgeBaseMetadata(ctx, base.ID)
}

func (s *CommandService) UpdateKnowledgeBase(ctx context.Context, viewer ViewerRef, cmd UpdateKnowledgeBaseCommand) (KnowledgeBaseDTO, error) {
	if err := requirePermission(viewer, PermissionKnowledgeManage); err != nil {
		return KnowledgeBaseDTO{}, err
	}
	if err := s.requireDeps(); err != nil {
		return KnowledgeBaseDTO{}, err
	}
	base, err := s.repo.GetKnowledgeBase(ctx, core.KnowledgeBaseID(cmd.ID))
	if err != nil {
		return KnowledgeBaseDTO{}, err
	}
	if !s.canManageBase(ctx, viewer, base) {
		return KnowledgeBaseDTO{}, ErrPermissionDenied
	}
	if cmd.updatesField("name") && cmd.Name != "" {
		base.Name = cmd.Name
	}
	if cmd.updatesField("description") {
		base.Description = cmd.Description
	}
	if cmd.updatesField("visibility") && cmd.Visibility != "" {
		base.Visibility = core.Visibility(cmd.Visibility)
	}
	if cmd.updatesField("status") && cmd.Status != "" {
		base.Status = core.KnowledgeStatus(cmd.Status)
	}
	base.UpdatedAt = s.clock.Now()
	if err := base.Validate(); err != nil {
		return KnowledgeBaseDTO{}, err
	}
	if err := s.repo.SaveKnowledgeBase(ctx, base); err != nil {
		return KnowledgeBaseDTO{}, err
	}
	return mapBase(base), nil
}

func (cmd UpdateKnowledgeBaseCommand) updatesField(field string) bool {
	if cmd.UpdateMask == nil {
		return true
	}
	field = strings.TrimSpace(field)
	if field == "" {
		return false
	}
	for _, item := range cmd.UpdateMask {
		if strings.TrimSpace(item) == field {
			return true
		}
	}
	return false
}

func (s *CommandService) BindKnowledgeBase(ctx context.Context, viewer ViewerRef, cmd BindKnowledgeBaseCommand) (KnowledgeBindingDTO, error) {
	if err := requirePermission(viewer, PermissionKnowledgeManage); err != nil {
		return KnowledgeBindingDTO{}, err
	}
	if err := s.requireDeps(); err != nil {
		return KnowledgeBindingDTO{}, err
	}
	base, err := s.repo.GetKnowledgeBase(ctx, core.KnowledgeBaseID(cmd.KnowledgeBaseID))
	if err != nil {
		return KnowledgeBindingDTO{}, err
	}
	if !s.canManageBase(ctx, viewer, base) {
		return KnowledgeBindingDTO{}, ErrPermissionDenied
	}
	binding := core.KnowledgeBinding{
		ID:              core.KnowledgeBindingID(s.ids.NewID("kbind")),
		KnowledgeBaseID: core.KnowledgeBaseID(cmd.KnowledgeBaseID),
		BoundToType:     core.BindingSubjectType(cmd.BoundToType),
		BoundToID:       cmd.BoundToID,
		Access:          core.AccessLevel(cmd.Access),
		CreatedBy:       viewer.ViewerID,
		CreatedAt:       s.clock.Now(),
	}
	if err := binding.Validate(); err != nil {
		return KnowledgeBindingDTO{}, err
	}
	if err := s.repo.SaveBinding(ctx, binding); err != nil {
		return KnowledgeBindingDTO{}, err
	}
	return mapBinding(binding), nil
}

func (s *CommandService) UnbindKnowledgeBase(ctx context.Context, viewer ViewerRef, bindingID string) error {
	if err := requirePermission(viewer, PermissionKnowledgeManage); err != nil {
		return err
	}
	if err := s.requireDeps(); err != nil {
		return err
	}
	binding, err := s.repo.GetBinding(ctx, core.KnowledgeBindingID(bindingID))
	if err != nil {
		return err
	}
	base, err := s.repo.GetKnowledgeBase(ctx, binding.KnowledgeBaseID)
	if err != nil {
		return err
	}
	if !s.canManageBase(ctx, viewer, base) {
		return ErrPermissionDenied
	}
	return s.repo.DeleteBinding(ctx, core.KnowledgeBindingID(bindingID))
}

func (s *CommandService) EnsureDefaultPersonalKnowledgeBase(ctx context.Context, viewer ViewerRef, cmd EnsureDefaultCommand) (KnowledgeBaseDTO, error) {
	if err := requirePermission(viewer, PermissionKnowledgeManage); err != nil {
		return KnowledgeBaseDTO{}, err
	}
	page, err := s.repo.ListKnowledgeBases(ctx, core.KnowledgeBaseQuery{OwnerType: core.OwnerType(cmd.OwnerType), OwnerID: cmd.OwnerID})
	if err != nil {
		return KnowledgeBaseDTO{}, err
	}
	for _, base := range page.Items {
		if base.IsDefault {
			return mapBase(base), nil
		}
	}
	dto, err := s.CreateKnowledgeBase(ctx, viewer, CreateKnowledgeBaseCommand{
		Name:        fmt.Sprintf("%s default knowledge", cmd.OwnerID),
		OwnerType:   cmd.OwnerType,
		OwnerID:     cmd.OwnerID,
		Visibility:  string(core.VisibilityPrivate),
		VectorRoot:  cmd.VectorRoot,
		ChunkPolicy: s.configuredDefaultChunkPolicy(),
	})
	if err != nil {
		return KnowledgeBaseDTO{}, err
	}
	base, err := s.repo.GetKnowledgeBase(ctx, core.KnowledgeBaseID(dto.ID))
	if err != nil {
		return KnowledgeBaseDTO{}, err
	}
	base.IsDefault = true
	if err := s.repo.SaveKnowledgeBase(ctx, base); err != nil {
		return KnowledgeBaseDTO{}, err
	}
	return mapBase(base), nil
}

func (s *CommandService) CreateDocumentMetadata(ctx context.Context, viewer ViewerRef, cmd CreateDocumentCommand) (KnowledgeDocumentDTO, error) {
	if err := requirePermission(viewer, PermissionKnowledgeWrite); err != nil {
		return KnowledgeDocumentDTO{}, err
	}
	ctx = withProviderRequestMetadata(ctx, viewer)
	if err := s.requireDeps(); err != nil {
		return KnowledgeDocumentDTO{}, err
	}
	base, err := s.repo.GetKnowledgeBase(ctx, core.KnowledgeBaseID(cmd.KnowledgeBaseID))
	if err != nil {
		return KnowledgeDocumentDTO{}, err
	}
	if !s.canWriteBase(ctx, viewer, base) {
		return KnowledgeDocumentDTO{}, ErrPermissionDenied
	}
	if err := s.validateDocumentSize(cmd.SizeBytes); err != nil {
		return KnowledgeDocumentDTO{}, err
	}
	now := s.clock.Now()
	doc := core.KnowledgeDocument{
		ID:              core.KnowledgeDocumentID(s.ids.NewID("kdoc")),
		KnowledgeBaseID: core.KnowledgeBaseID(cmd.KnowledgeBaseID),
		ArtifactID:      cmd.ArtifactID,
		Filename:        cmd.Filename,
		ContentType:     cmd.ContentType,
		SizeBytes:       cmd.SizeBytes,
		Checksum:        cmd.Checksum,
		Status:          core.DocumentUploaded,
		CreatedBy:       viewer.ViewerID,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	kp := s.providerForBase(base)
	if kp == nil {
		return KnowledgeDocumentDTO{}, ErrServiceDependency
	}
	indexed, err := kp.CreateDocumentMetadata(ctx, provider.CreateDocumentMetadataRequest{Base: base, Document: doc})
	if err != nil {
		return KnowledgeDocumentDTO{}, err
	}
	applyDocumentProviderResult(&doc, indexed)
	if err := s.repo.SaveDocument(ctx, doc); err != nil {
		return KnowledgeDocumentDTO{}, err
	}
	if err := s.publishDocumentMetadata(ctx, base, doc); err != nil {
		return KnowledgeDocumentDTO{}, err
	}
	var jobID string
	if indexed.IngestJobQueued {
		job := core.KnowledgeIngestJob{
			ID:              indexed.IngestJobID,
			KnowledgeBaseID: doc.KnowledgeBaseID,
			DocumentID:      doc.ID,
			Topic:           ingest.TopicIngestDocument,
			Status:          core.JobQueued,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		if err := s.repo.SaveIngestJob(ctx, job); err != nil {
			return KnowledgeDocumentDTO{}, err
		}
		jobID = string(job.ID)
	}
	dto := mapDocument(doc)
	if jobID != "" {
		dto.JobID = jobID
	}
	return dto, nil
}

func (s *CommandService) DeleteDocument(ctx context.Context, viewer ViewerRef, cmd DeleteDocumentCommand) error {
	if err := requirePermission(viewer, PermissionKnowledgeManage); err != nil {
		return err
	}
	ctx = withProviderRequestMetadata(ctx, viewer)
	if err := s.requireDeps(); err != nil {
		return err
	}
	base, err := s.repo.GetKnowledgeBase(ctx, core.KnowledgeBaseID(cmd.KnowledgeBaseID))
	if err != nil {
		return err
	}
	if !s.canManageBase(ctx, viewer, base) {
		return ErrPermissionDenied
	}
	doc, err := s.repo.GetDocument(ctx, core.KnowledgeDocumentID(cmd.DocumentID))
	if err != nil {
		return err
	}
	if doc.KnowledgeBaseID != base.ID {
		return core.ErrNotFound
	}
	kp := s.providerForBase(base)
	if kp == nil {
		return ErrServiceDependency
	}
	if err := kp.DeleteDocument(ctx, provider.DeleteDocumentRequest{Base: base, Document: doc}); err != nil {
		return err
	}
	doc.Status = core.DocumentDeleting
	doc.UpdatedAt = s.clock.Now()
	if err := s.repo.DeleteDocumentMetadata(ctx, doc.ID); err != nil {
		return err
	}
	return s.publishDocumentStatus(ctx, base, doc)
}

func (s *CommandService) RefreshDocumentStatus(ctx context.Context, viewer ViewerRef, cmd DocumentActionCommand) (KnowledgeDocumentDTO, error) {
	if err := requirePermission(viewer, PermissionKnowledgeRead); err != nil {
		return KnowledgeDocumentDTO{}, err
	}
	ctx = withProviderRequestMetadata(ctx, viewer)
	base, doc, err := s.documentForAction(ctx, viewer, cmd, false)
	if err != nil {
		return KnowledgeDocumentDTO{}, err
	}
	kp := s.providerForBase(base)
	if kp == nil {
		return KnowledgeDocumentDTO{}, ErrServiceDependency
	}
	status, err := kp.RefreshDocumentStatus(ctx, provider.DocumentStatusRequest{Base: base, Document: doc})
	if err != nil {
		return KnowledgeDocumentDTO{}, err
	}
	applyDocumentStatusResult(&doc, status)
	if status.Job != nil {
		applyDocumentProcessingJob(&doc, *status.Job)
	}
	doc.UpdatedAt = s.clock.Now()
	if doc.Status == core.DocumentIndexed && doc.IndexedAt.IsZero() {
		doc.IndexedAt = doc.UpdatedAt
	}
	if err := s.repo.SaveDocument(ctx, doc); err != nil {
		return KnowledgeDocumentDTO{}, err
	}
	if err := s.publishDocumentStatus(ctx, base, doc); err != nil {
		return KnowledgeDocumentDTO{}, err
	}
	dto := mapDocument(doc)
	if status.Job != nil {
		dto.JobID = status.Job.JobID
	}
	return dto, nil
}

func (s *CommandService) ReprocessDocument(ctx context.Context, viewer ViewerRef, cmd DocumentActionCommand) (KnowledgeDocumentDTO, error) {
	if err := requirePermission(viewer, PermissionKnowledgeManage); err != nil {
		return KnowledgeDocumentDTO{}, err
	}
	ctx = withProviderRequestMetadata(ctx, viewer)
	base, doc, err := s.documentForAction(ctx, viewer, cmd, true)
	if err != nil {
		return KnowledgeDocumentDTO{}, err
	}
	kp := s.providerForBase(base)
	if kp == nil {
		return KnowledgeDocumentDTO{}, ErrServiceDependency
	}
	result, err := kp.ReprocessDocument(ctx, provider.ReprocessDocumentRequest{Base: base, Document: doc})
	if err != nil {
		return KnowledgeDocumentDTO{}, err
	}
	if result.Status != "" {
		doc.Status = result.Status
	} else {
		doc.Status = core.DocumentParsing
	}
	doc.ErrorMessage = result.ErrorMessage
	applyDocumentProcessingJob(&doc, result.Job)
	doc.UpdatedAt = s.clock.Now()
	if err := s.repo.SaveDocument(ctx, doc); err != nil {
		return KnowledgeDocumentDTO{}, err
	}
	if err := s.publishDocumentStatus(ctx, base, doc); err != nil {
		return KnowledgeDocumentDTO{}, err
	}
	dto := mapDocument(doc)
	dto.JobID = result.Job.JobID
	return dto, nil
}

func (s *CommandService) GetDocumentProcessingResult(ctx context.Context, viewer ViewerRef, cmd DocumentProcessingResultCommand) (KnowledgeDocumentProcessingResultDTO, error) {
	if err := requirePermission(viewer, PermissionKnowledgeRead); err != nil {
		return KnowledgeDocumentProcessingResultDTO{}, err
	}
	ctx = withProviderRequestMetadata(ctx, viewer)
	base, doc, err := s.documentForAction(ctx, viewer, DocumentActionCommand{KnowledgeBaseID: cmd.KnowledgeBaseID, DocumentID: cmd.DocumentID}, false)
	if err != nil {
		return KnowledgeDocumentProcessingResultDTO{}, err
	}
	kp := s.providerForBase(base)
	if kp == nil {
		return KnowledgeDocumentProcessingResultDTO{}, ErrServiceDependency
	}
	result, err := kp.GetDocumentProcessingResult(ctx, provider.DocumentProcessingResultRequest{Base: base, Document: doc, Pages: cmd.Pages})
	if err != nil {
		return KnowledgeDocumentProcessingResultDTO{}, err
	}
	if result.Status != "" || result.ErrorMessage != "" || result.Job != nil || result.Progress > 0 || result.Stage != "" {
		applyDocumentStatusResult(&doc, provider.DocumentStatusResult{
			Filename:     result.Filename,
			Status:       result.Status,
			ErrorMessage: result.ErrorMessage,
		})
		if result.Job != nil {
			applyDocumentProcessingJob(&doc, *result.Job)
		} else if result.Progress > 0 || result.Stage != "" {
			applyDocumentProcessingSnapshot(&doc, result.Progress, result.Stage)
		}
		doc.UpdatedAt = s.clock.Now()
		if doc.Status == core.DocumentIndexed && doc.IndexedAt.IsZero() {
			doc.IndexedAt = doc.UpdatedAt
		}
		if err := s.repo.SaveDocument(ctx, doc); err != nil {
			return KnowledgeDocumentProcessingResultDTO{}, err
		}
		if err := s.publishDocumentStatus(ctx, base, doc); err != nil {
			return KnowledgeDocumentProcessingResultDTO{}, err
		}
	}
	if err := s.publishPageMetadata(ctx, base, doc, result); err != nil {
		return KnowledgeDocumentProcessingResultDTO{}, err
	}
	return mapProcessingResult(doc, result), nil
}

func (s *CommandService) CreateReindexJob(ctx context.Context, viewer ViewerRef, cmd ReindexKnowledgeBaseCommand) (KnowledgeJobDTO, error) {
	if err := requirePermission(viewer, PermissionKnowledgeManage); err != nil {
		return KnowledgeJobDTO{}, err
	}
	ctx = withProviderRequestMetadata(ctx, viewer)
	if err := s.requireDeps(); err != nil {
		return KnowledgeJobDTO{}, err
	}
	base, err := s.repo.GetKnowledgeBase(ctx, core.KnowledgeBaseID(cmd.KnowledgeBaseID))
	if err != nil {
		return KnowledgeJobDTO{}, err
	}
	if !s.canManageBase(ctx, viewer, base) {
		return KnowledgeJobDTO{}, ErrPermissionDenied
	}
	now := s.clock.Now()
	kp := s.providerForBase(base)
	if kp == nil {
		return KnowledgeJobDTO{}, ErrServiceDependency
	}
	reindex, err := kp.ReindexKnowledgeBase(ctx, provider.ReindexKnowledgeBaseRequest{Base: base})
	if err != nil {
		return KnowledgeJobDTO{}, err
	}
	if !reindex.Supported {
		return KnowledgeJobDTO{}, fmt.Errorf("%w: provider %s does not support reindex", core.ErrInvalid, base.EffectiveProvider())
	}
	if !reindex.LocalIngestJob {
		return KnowledgeJobDTO{
			ID:              string(reindex.JobID),
			KnowledgeBaseID: string(base.ID),
			Topic:           ingest.TopicReindexBase,
			Status:          string(core.JobQueued),
		}, nil
	}
	job := core.KnowledgeIngestJob{
		ID:              reindex.JobID,
		KnowledgeBaseID: base.ID,
		Topic:           ingest.TopicReindexBase,
		Status:          core.JobQueued,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.repo.SaveIngestJob(ctx, job); err != nil {
		return KnowledgeJobDTO{}, err
	}
	return mapJob(job), nil
}

func (s *CommandService) UploadDocument(ctx context.Context, viewer ViewerRef, cmd UploadDocumentCommand) (KnowledgeDocumentDTO, error) {
	if err := requirePermission(viewer, PermissionKnowledgeWrite); err != nil {
		return KnowledgeDocumentDTO{}, err
	}
	ctx = withProviderRequestMetadata(ctx, viewer)
	if err := s.requireDeps(); err != nil {
		return KnowledgeDocumentDTO{}, err
	}
	base, err := s.repo.GetKnowledgeBase(ctx, core.KnowledgeBaseID(cmd.KnowledgeBaseID))
	if err != nil {
		return KnowledgeDocumentDTO{}, err
	}
	if !s.canWriteBase(ctx, viewer, base) {
		return KnowledgeDocumentDTO{}, ErrPermissionDenied
	}
	if strings.TrimSpace(cmd.Filename) == "" {
		return KnowledgeDocumentDTO{}, fmt.Errorf("%w: filename is required", core.ErrInvalid)
	}
	if cmd.Reader == nil {
		return KnowledgeDocumentDTO{}, fmt.Errorf("%w: document reader is required", core.ErrInvalid)
	}
	reader := cmd.Reader
	if s.ingestPolicy.MaxDocumentBytes > 0 {
		limited, err := uploadReaderWithinLimit(reader, s.ingestPolicy.MaxDocumentBytes)
		if err != nil {
			return KnowledgeDocumentDTO{}, err
		}
		reader = limited
	}
	now := s.clock.Now()
	docID := core.KnowledgeDocumentID(s.ids.NewID("kdoc"))
	kp := s.providerForBase(base)
	if kp == nil {
		return KnowledgeDocumentDTO{}, ErrServiceDependency
	}
	uploaded, err := kp.UploadDocument(ctx, provider.UploadDocumentRequest{
		Base:        base,
		DocumentID:  docID,
		Filename:    cmd.Filename,
		ContentType: cmd.ContentType,
		Reader:      reader,
		CreatedBy:   viewer.ViewerID,
	})
	if err != nil {
		return KnowledgeDocumentDTO{}, err
	}
	doc := core.KnowledgeDocument{
		ID:              docID,
		KnowledgeBaseID: base.ID,
		ArtifactID:      uploaded.ArtifactID,
		Filename:        uploaded.Filename,
		ContentType:     uploaded.ContentType,
		SizeBytes:       uploaded.SizeBytes,
		Checksum:        uploaded.Checksum,
		Status:          uploaded.Status,
		ErrorMessage:    uploaded.ErrorMessage,
		CreatedBy:       viewer.ViewerID,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	applyDocumentProviderResult(&doc, uploaded)
	if err := s.repo.SaveDocument(ctx, doc); err != nil {
		return KnowledgeDocumentDTO{}, err
	}
	if err := s.publishDocumentMetadata(ctx, base, doc); err != nil {
		return KnowledgeDocumentDTO{}, err
	}
	var jobID string
	if uploaded.IngestJobQueued {
		job := core.KnowledgeIngestJob{
			ID:              uploaded.IngestJobID,
			KnowledgeBaseID: doc.KnowledgeBaseID,
			DocumentID:      doc.ID,
			Topic:           ingest.TopicIngestDocument,
			Status:          core.JobQueued,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		if err := s.repo.SaveIngestJob(ctx, job); err != nil {
			return KnowledgeDocumentDTO{}, err
		}
		jobID = string(job.ID)
	}
	dto := mapDocument(doc)
	if jobID != "" {
		dto.JobID = jobID
	}
	return dto, nil
}

func (s *CommandService) requireDeps() error {
	if s == nil || s.repo == nil || s.ids == nil || s.clock == nil {
		return ErrServiceDependency
	}
	return nil
}

func (s *CommandService) submitKnowledgeJob(ctx context.Context, topic string, payload any, maxAttempts int) error {
	if s.execution == nil {
		return nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return s.execution.Submit(ctx, topic, data, maxAttempts)
}

func (s *CommandService) publishDocumentMetadata(ctx context.Context, base core.KnowledgeBase, doc core.KnowledgeDocument) error {
	if s == nil || s.metadataSink == nil {
		return nil
	}
	revision, err := s.nextKnowledgeDocumentEventRevision(ctx, doc.ID)
	if err != nil {
		return err
	}
	return s.metadataSink.PublishKnowledgeDocumentMetadata(ctx, graphprotocol.DomainEvent{
		ID:         "knowledge-document-metadata:" + string(doc.ID) + ":rev-" + strconv.FormatInt(revision, 10),
		TenantID:   metadataTenantID(base),
		Type:       "knowledge.document.metadata.updated",
		StreamID:   string(doc.ID),
		Version:    revision,
		OccurredAt: doc.UpdatedAt,
		Payload: map[string]any{
			"id":                   string(doc.ID),
			"knowledge_base_id":    string(doc.KnowledgeBaseID),
			"title":                doc.Filename,
			"source_uri":           documentSourceURI(base, doc),
			"provider":             string(base.EffectiveProvider()),
			"provider_document_id": doc.ProviderDocumentID,
			"content_type":         doc.ContentType,
			"size_bytes":           doc.SizeBytes,
			"checksum":             doc.Checksum,
			"status":               string(doc.Status),
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge"},
	})
}

func (s *CommandService) publishDocumentStatus(ctx context.Context, base core.KnowledgeBase, doc core.KnowledgeDocument) error {
	if s == nil || s.metadataSink == nil {
		return nil
	}
	revision, err := s.nextKnowledgeDocumentEventRevision(ctx, doc.ID)
	if err != nil {
		return err
	}
	return s.metadataSink.PublishKnowledgeDocumentMetadata(ctx, graphprotocol.DomainEvent{
		ID:         "knowledge-document-status:" + string(doc.ID) + ":" + string(doc.Status) + ":rev-" + strconv.FormatInt(revision, 10),
		TenantID:   metadataTenantID(base),
		Type:       "knowledge.document.status.updated",
		StreamID:   string(doc.ID),
		Version:    revision,
		OccurredAt: doc.UpdatedAt,
		Payload: map[string]any{
			"id":     string(doc.ID),
			"status": string(doc.Status),
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge"},
	})
}

func (s *CommandService) publishPageMetadata(ctx context.Context, base core.KnowledgeBase, doc core.KnowledgeDocument, result provider.DocumentProcessingResult) error {
	if s == nil || s.metadataSink == nil || (len(result.Pages) == 0 && len(result.PagePreviews) == 0 && len(result.StructureNodes) == 0) {
		return nil
	}
	sink, ok := s.metadataSink.(KnowledgePageMetadataSink)
	if !ok {
		return nil
	}
	publishedPages := map[string]bool{}
	for index, page := range processingPagesForEvents(result) {
		pageNumber := pageNumberFromProcessingPage(page, index)
		if pageNumber <= 0 {
			continue
		}
		pageID := knowledgePageID(doc.ID, pageNumber)
		if !publishedPages[pageID] {
			revision, err := s.nextKnowledgeDocumentEventRevision(ctx, doc.ID)
			if err != nil {
				return err
			}
			payload := map[string]any{
				"id":                   pageID,
				"knowledge_base_id":    string(doc.KnowledgeBaseID),
				"document_id":          string(doc.ID),
				"page_number":          pageNumber,
				"provider":             string(base.EffectiveProvider()),
				"provider_document_id": doc.ProviderDocumentID,
				"heading_path":         headingPathFromProcessingPage(page),
				"source_uri":           pageSourceURI(base, doc, pageNumber),
			}
			if err := sink.PublishKnowledgePageMetadata(ctx, graphprotocol.DomainEvent{
				ID:          "knowledge-page-metadata:" + pageID + ":rev-" + strconv.FormatInt(revision, 10),
				TenantID:    metadataTenantID(base),
				Type:        "knowledge.page.metadata.updated",
				StreamID:    string(doc.ID),
				Version:     revision,
				OccurredAt:  doc.UpdatedAt,
				Payload:     payload,
				PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge"},
			}); err != nil {
				return err
			}
			publishedPages[pageID] = true
		}
		if err := s.publishAnchorMetadata(ctx, base, doc, page, pageID, pageNumber); err != nil {
			return err
		}
	}
	return nil
}

func processingPagesForEvents(result provider.DocumentProcessingResult) []map[string]any {
	if len(result.PagePreviews) > 0 {
		return processingPagesFromPagePreviews(result.PagePreviews)
	}
	if len(result.Pages) > 0 {
		return result.Pages
	}
	return processingPagesFromStructureNodes(result.StructureNodes)
}

func processingPagesFromPagePreviews(previews []provider.DocumentPagePreview) []map[string]any {
	out := make([]map[string]any, 0, len(previews))
	for _, page := range previews {
		item := map[string]any{
			"page_number":      page.PageNumber,
			"provider_node_id": page.ProviderNodeID,
			"heading_path":     append([]string(nil), page.HeadingPath...),
			"structure_path":   append([]string(nil), page.StructurePath...),
			"bbox":             cloneMetadataAny(page.BBox),
			"image_ref":        page.ImageRef,
			"content_hash":     page.ContentHash,
			"snippet_hash":     page.SnippetHash,
		}
		if page.Text != "" {
			item["text"] = page.Text
		}
		out = append(out, item)
	}
	return out
}

func processingPagesFromStructureNodes(nodes []provider.DocumentStructureNode) []map[string]any {
	if len(nodes) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(nodes))
	var walk func([]provider.DocumentStructureNode)
	walk = func(items []provider.DocumentStructureNode) {
		for _, node := range items {
			if node.PageNumber > 0 {
				item := map[string]any{
					"page_number":      node.PageNumber,
					"provider_node_id": node.ProviderNodeID,
					"heading_path":     append([]string(nil), node.HeadingPath...),
					"structure_path":   append([]string(nil), node.StructurePath...),
					"bbox":             cloneMetadataAny(node.BBox),
				}
				if node.Text != "" {
					item["text"] = node.Text
				}
				out = append(out, item)
			}
			if len(node.Children) > 0 {
				walk(node.Children)
			}
		}
	}
	walk(nodes)
	return out
}

func (s *CommandService) publishAnchorMetadata(ctx context.Context, base core.KnowledgeBase, doc core.KnowledgeDocument, page map[string]any, pageID string, pageNumber int) error {
	sink, ok := s.metadataSink.(KnowledgeAnchorMetadataSink)
	if !ok {
		return nil
	}
	payload, ok := anchorPayloadFromProcessingPage(base, doc, page, pageID, pageNumber)
	if !ok {
		return nil
	}
	anchorID, _ := stringFromAny(payload["id"])
	revision, err := s.nextKnowledgeDocumentEventRevision(ctx, doc.ID)
	if err != nil {
		return err
	}
	return sink.PublishKnowledgeAnchorMetadata(ctx, graphprotocol.DomainEvent{
		ID:          "knowledge-anchor-metadata:" + anchorID + ":rev-" + strconv.FormatInt(revision, 10),
		TenantID:    metadataTenantID(base),
		Type:        "knowledge.anchor.metadata.updated",
		StreamID:    string(doc.ID),
		Version:     revision,
		OccurredAt:  doc.UpdatedAt,
		Payload:     payload,
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge"},
	})
}

func (s *CommandService) nextKnowledgeDocumentEventRevision(ctx context.Context, documentID core.KnowledgeDocumentID) (int64, error) {
	if s == nil || s.repo == nil {
		return 0, ErrServiceDependency
	}
	return s.repo.NextEventRevision(ctx, knowledgeDocumentEventStreamID(documentID))
}

func knowledgeDocumentEventStreamID(documentID core.KnowledgeDocumentID) string {
	return "knowledge_document:" + string(documentID)
}

func knowledgePageID(documentID core.KnowledgeDocumentID, pageNumber int) string {
	return string(documentID) + ":page:" + strconv.Itoa(pageNumber)
}

func knowledgeAnchorID(documentID core.KnowledgeDocumentID, pageNumber int, anchorKey string) string {
	return knowledgePageID(documentID, pageNumber) + ":anchor:" + sanitizeAnchorKey(anchorKey)
}

func metadataTenantID(base core.KnowledgeBase) string {
	if base.OwnerID != "" {
		return base.OwnerID
	}
	return string(base.ID)
}

func documentSourceURI(base core.KnowledgeBase, doc core.KnowledgeDocument) string {
	providerName := base.EffectiveProvider()
	switch providerName {
	case core.ProviderPageIndex:
		if base.ProviderResourceID != "" && doc.ProviderDocumentID != "" {
			return "pageindex://" + base.ProviderResourceID + "/" + doc.ProviderDocumentID
		}
	case core.ProviderLocal:
		if doc.ArtifactID != "" {
			return "artifact://" + doc.ArtifactID
		}
	}
	if doc.ProviderDocumentID != "" {
		return string(providerName) + "://" + doc.ProviderDocumentID
	}
	if doc.ArtifactID != "" {
		return "artifact://" + doc.ArtifactID
	}
	return "knowledge-document://" + string(doc.ID)
}

func pageSourceURI(base core.KnowledgeBase, doc core.KnowledgeDocument, pageNumber int) string {
	return documentSourceURI(base, doc) + "#page=" + strconv.Itoa(pageNumber)
}

func anchorSourceURI(base core.KnowledgeBase, doc core.KnowledgeDocument, pageNumber int, anchorKey string) string {
	return pageSourceURI(base, doc, pageNumber) + "&anchor=" + url.QueryEscape(anchorKey)
}

func anchorPayloadFromProcessingPage(base core.KnowledgeBase, doc core.KnowledgeDocument, page map[string]any, pageID string, pageNumber int) (map[string]any, bool) {
	anchorKey, ok := anchorKeyFromProcessingPage(page, pageNumber)
	if !ok {
		return nil, false
	}
	providerNodeID, _ := providerNodeIDFromProcessingPage(page)
	payload := map[string]any{
		"id":                   knowledgeAnchorID(doc.ID, pageNumber, anchorKey),
		"knowledge_base_id":    string(doc.KnowledgeBaseID),
		"document_id":          string(doc.ID),
		"page_id":              pageID,
		"page_number":          pageNumber,
		"provider":             string(base.EffectiveProvider()),
		"provider_document_id": doc.ProviderDocumentID,
		"provider_node_id":     providerNodeID,
		"heading_path":         headingPathFromProcessingPage(page),
		"structure_path":       structurePathFromProcessingPage(page),
		"bbox":                 map[string]any{},
		"image_ref":            "",
		"content_hash":         "",
		"snippet_hash":         "",
		"source_uri":           anchorSourceURI(base, doc, pageNumber, anchorKey),
	}
	if bbox, ok := bboxFromProcessingPage(page); ok {
		payload["bbox"] = bbox
	}
	if imageRef, ok := imageRefFromProcessingPage(page); ok {
		payload["image_ref"] = imageRef
	}
	if contentHash, ok := contentHashFromProcessingPage(page); ok {
		payload["content_hash"] = contentHash
	}
	if snippetHash, ok := snippetHashFromProcessingPage(page); ok {
		payload["snippet_hash"] = snippetHash
	}
	return payload, true
}

func anchorKeyFromProcessingPage(page map[string]any, pageNumber int) (string, bool) {
	if value, ok := providerNodeIDFromProcessingPage(page); ok {
		return value, true
	}
	if value, ok := firstStringFromMap(page, "anchor_id", "anchorId", "id"); ok {
		return value, true
	}
	if hash, ok := snippetHashFromProcessingPage(page); ok {
		return "snippet-" + shortHash(hash), true
	}
	if path := structurePathFromProcessingPage(page); len(path) > 0 {
		return strings.Join(path, "-"), true
	}
	if path := headingPathFromProcessingPage(page); len(path) > 0 {
		return strings.Join(path, "-"), true
	}
	if bbox, ok := bboxFromProcessingPage(page); ok {
		if key, ok := stableMetadataKey("bbox", bbox); ok {
			return key, true
		}
	}
	if pageNumber > 0 {
		return "page-" + strconv.Itoa(pageNumber), true
	}
	return "", false
}

func providerNodeIDFromProcessingPage(page map[string]any) (string, bool) {
	return firstStringFromMap(page, "provider_node_id", "providerNodeID", "node_id", "nodeId")
}

func pageNumberFromProcessingPage(page map[string]any, index int) int {
	for _, key := range []string{"page", "page_number", "pageNumber", "number", "index"} {
		if value, ok := numberFromAny(page[key]); ok && value > 0 {
			return value
		}
	}
	return index + 1
}

func headingPathFromProcessingPage(page map[string]any) []string {
	for _, key := range []string{"heading_path", "headingPath", "path", "headings"} {
		if values := stringSliceFromAny(page[key]); len(values) > 0 {
			return values
		}
	}
	for _, key := range []string{"title", "heading", "section"} {
		if value, ok := stringFromAny(page[key]); ok {
			return []string{value}
		}
	}
	return nil
}

func structurePathFromProcessingPage(page map[string]any) []string {
	for _, key := range []string{"structure_path", "structurePath", "tree_path", "treePath"} {
		if values := stringSliceFromAny(page[key]); len(values) > 0 {
			return values
		}
	}
	return nil
}

func bboxFromProcessingPage(page map[string]any) (any, bool) {
	for _, key := range []string{"bbox", "bounding_box", "boundingBox"} {
		if value, ok := lightweightObjectFromAny(page[key]); ok {
			return value, true
		}
	}
	return nil, false
}

func imageRefFromProcessingPage(page map[string]any) (string, bool) {
	return firstStringFromMap(page, "image_ref", "imageRef", "page_image_uri", "pageImageURI", "image_uri", "imageURI")
}

func contentHashFromProcessingPage(page map[string]any) (string, bool) {
	return firstStringFromMap(page, "content_hash", "contentHash")
}

func snippetHashFromProcessingPage(page map[string]any) (string, bool) {
	if value, ok := firstStringFromMap(page, "snippet_hash", "snippetHash"); ok {
		return value, true
	}
	for _, key := range []string{"snippet", "text", "content"} {
		if value, ok := stringFromAny(page[key]); ok {
			return "sha256:" + sha256Hex(value), true
		}
	}
	return "", false
}

func firstStringFromMap(values map[string]any, keys ...string) (string, bool) {
	for _, key := range keys {
		if value, ok := stringFromAny(values[key]); ok {
			return value, true
		}
	}
	return "", false
}

func lightweightObjectFromAny(value any) (any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		if len(typed) == 0 {
			return nil, false
		}
		clone := make(map[string]any, len(typed))
		for key, value := range typed {
			clone[key] = value
		}
		return clone, true
	case []any:
		if len(typed) == 0 {
			return nil, false
		}
		return append([]any(nil), typed...), true
	default:
		return nil, false
	}
}

func cloneMetadataAny(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]any, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

func stableMetadataKey(prefix string, value any) (string, bool) {
	data, err := json.Marshal(value)
	if err != nil || len(data) == 0 {
		return "", false
	}
	return prefix + "-" + shortHash("sha256:"+sha256Hex(string(data))), true
}

func sanitizeAnchorKey(value string) string {
	var out strings.Builder
	for _, r := range strings.TrimSpace(value) {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			out.WriteRune(r)
			continue
		}
		out.WriteByte('-')
	}
	text := strings.Trim(out.String(), "-")
	if text == "" {
		return "anchor"
	}
	return text
}

func shortHash(value string) string {
	value = strings.TrimPrefix(value, "sha256:")
	if len(value) > 12 {
		return value[:12]
	}
	return value
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func numberFromAny(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case float32:
		return int(typed), true
	case float64:
		return int(typed), true
	case json.Number:
		integer, err := typed.Int64()
		if err == nil {
			return int(integer), true
		}
		floatValue, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		return int(floatValue), true
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

func stringSliceFromAny(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if value, ok := stringFromAny(item); ok {
				out = append(out, value)
			}
		}
		return out
	default:
		return nil
	}
}

func stringFromAny(value any) (string, bool) {
	text, ok := value.(string)
	if !ok {
		return "", false
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}
	return text, true
}

func (s *CommandService) configuredDefaultChunkPolicy() doccore.ChunkPolicy {
	if s == nil || s.defaultChunkPolicy.MaxRunes == 0 {
		return defaultChunkPolicy()
	}
	return s.defaultChunkPolicy
}

func (s *CommandService) configuredVectorRoot(root string) string {
	if root != "" {
		return root
	}
	if s == nil {
		return ""
	}
	return s.defaultVectorRoot
}

func (s *CommandService) defaultProviderName() core.KnowledgeProvider {
	if s == nil || s.providers == nil || s.providers.Default() == nil {
		return core.ProviderLocal
	}
	return s.providers.Default().Name()
}

func (s *CommandService) providerForBase(base core.KnowledgeBase) provider.Provider {
	if s == nil {
		return nil
	}
	if s.providers != nil {
		if kp := s.providers.ForBase(base); kp != nil {
			return kp
		}
	}
	return localprovider.New(localprovider.Options{
		Repository: s.repo,
		Vector:     s.vector,
		IDs:        s.ids,
		Clock:      s.clock,
		Artifacts:  adaptArtifactWriter{s.artifacts},
		Execution:  s.execution,
		MaxBytes:   s.ingestPolicy.MaxDocumentBytes,
	})
}

type adaptArtifactWriter struct {
	next ArtifactWriter
}

func (w adaptArtifactWriter) PutArtifact(ctx context.Context, name, contentType string, reader io.Reader) (localprovider.ArtifactRef, error) {
	if w.next == nil {
		return localprovider.ArtifactRef{}, ErrServiceDependency
	}
	ref, err := w.next.PutArtifact(ctx, name, contentType, reader)
	if err != nil {
		return localprovider.ArtifactRef{}, err
	}
	return localprovider.ArtifactRef(ref), nil
}

func applyDocumentProviderResult(doc *core.KnowledgeDocument, result provider.UploadDocumentResult) {
	if doc == nil {
		return
	}
	if result.ArtifactID != "" {
		doc.ArtifactID = result.ArtifactID
	}
	if result.ProviderDocumentID != "" {
		doc.ProviderDocumentID = result.ProviderDocumentID
	}
	if result.Filename != "" {
		doc.Filename = result.Filename
	}
	if result.ContentType != "" {
		doc.ContentType = result.ContentType
	}
	if result.SizeBytes > 0 {
		doc.SizeBytes = result.SizeBytes
	}
	if result.Checksum != "" {
		doc.Checksum = result.Checksum
	}
	if result.Status != "" {
		doc.Status = result.Status
	}
	if result.ErrorMessage != "" {
		doc.ErrorMessage = result.ErrorMessage
	}
	if len(result.ProviderMetadata) > 0 {
		doc.ProviderMetadata = result.ProviderMetadata
	}
}

func applyDocumentStatusResult(doc *core.KnowledgeDocument, result provider.DocumentStatusResult) {
	if doc == nil {
		return
	}
	if result.ProviderDocumentID != "" {
		doc.ProviderDocumentID = result.ProviderDocumentID
	}
	if result.Filename != "" {
		doc.Filename = result.Filename
	}
	if result.ContentType != "" {
		doc.ContentType = result.ContentType
	}
	if result.SizeBytes > 0 {
		doc.SizeBytes = result.SizeBytes
	}
	if result.Checksum != "" {
		doc.Checksum = result.Checksum
	}
	if result.Status != "" {
		doc.Status = result.Status
	}
	doc.ErrorMessage = result.ErrorMessage
	if len(result.ProviderMetadata) > 0 {
		doc.ProviderMetadata = result.ProviderMetadata
	}
}

func applyDocumentProcessingJob(doc *core.KnowledgeDocument, job provider.DocumentProcessingJob) {
	if doc == nil {
		return
	}
	metadata := ensureDocumentProviderMetadata(doc)
	if job.JobID != "" {
		metadata["pageindex_job_id"] = job.JobID
	}
	if job.Status != "" {
		metadata["pageindex_job_status"] = job.Status
	}
	if job.Stage != "" {
		metadata["pageindex_job_stage"] = job.Stage
	}
	metadata["pageindex_job_progress"] = strconv.Itoa(clampedProgress(job.Progress))
}

func applyDocumentProcessingSnapshot(doc *core.KnowledgeDocument, progress int, stage string) {
	if doc == nil {
		return
	}
	metadata := ensureDocumentProviderMetadata(doc)
	metadata["pageindex_job_progress"] = strconv.Itoa(clampedProgress(progress))
	if stage != "" {
		metadata["pageindex_job_stage"] = stage
	}
}

func ensureDocumentProviderMetadata(doc *core.KnowledgeDocument) map[string]string {
	if doc.ProviderMetadata == nil {
		doc.ProviderMetadata = map[string]string{}
	}
	return doc.ProviderMetadata
}

func clampedProgress(progress int) int {
	if progress < 0 {
		return 0
	}
	if progress > 100 {
		return 100
	}
	return progress
}

func (s *CommandService) documentForAction(ctx context.Context, viewer ViewerRef, cmd DocumentActionCommand, manage bool) (core.KnowledgeBase, core.KnowledgeDocument, error) {
	if err := s.requireDeps(); err != nil {
		return core.KnowledgeBase{}, core.KnowledgeDocument{}, err
	}
	base, err := s.repo.GetKnowledgeBase(ctx, core.KnowledgeBaseID(cmd.KnowledgeBaseID))
	if err != nil {
		return core.KnowledgeBase{}, core.KnowledgeDocument{}, err
	}
	if manage {
		if !s.canManageBase(ctx, viewer, base) {
			return core.KnowledgeBase{}, core.KnowledgeDocument{}, ErrPermissionDenied
		}
	} else if !s.policy.CanRead(ctx, viewer.AccessSubject(), base) {
		return core.KnowledgeBase{}, core.KnowledgeDocument{}, core.ErrNotFound
	}
	doc, err := s.repo.GetDocument(ctx, core.KnowledgeDocumentID(cmd.DocumentID))
	if err != nil {
		return core.KnowledgeBase{}, core.KnowledgeDocument{}, err
	}
	if doc.KnowledgeBaseID != base.ID {
		return core.KnowledgeBase{}, core.KnowledgeDocument{}, core.ErrNotFound
	}
	return base, doc, nil
}

func (s *CommandService) canWriteBase(ctx context.Context, viewer ViewerRef, base core.KnowledgeBase) bool {
	if s == nil || s.policy == nil {
		return false
	}
	return s.policy.CanWrite(ctx, viewer.AccessSubject(), base)
}

func (s *CommandService) canManageBase(ctx context.Context, viewer ViewerRef, base core.KnowledgeBase) bool {
	if s == nil || s.policy == nil {
		return false
	}
	return s.policy.CanManage(ctx, viewer.AccessSubject(), base)
}

func defaultChunkPolicy() doccore.ChunkPolicy {
	return doccore.ChunkPolicy{MaxRunes: 2400, OverlapRunes: 240, MinRunes: 400, PreserveHeads: true}
}

func (s *CommandService) validateDocumentSize(sizeBytes int64) error {
	if s == nil || s.ingestPolicy.MaxDocumentBytes <= 0 || sizeBytes <= 0 {
		return nil
	}
	if sizeBytes > s.ingestPolicy.MaxDocumentBytes {
		return fmt.Errorf("%w: document exceeds max_document_bytes", core.ErrInvalid)
	}
	return nil
}

func uploadReaderWithinLimit(reader io.Reader, maxBytes int64) (io.Reader, error) {
	if maxBytes <= 0 {
		return reader, nil
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read document: %v", core.ErrInvalid, err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%w: document exceeds max_document_bytes", core.ErrInvalid)
	}
	return bytes.NewReader(data), nil
}
