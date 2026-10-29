package pageindex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	knowledgecore "orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/provider"
	memcore "orrery_v2/internal/memory/core"
	memretrieval "orrery_v2/internal/memory/retrieval"
	memstore "orrery_v2/internal/memory/store"
)

const (
	DocumentStatusIndexed = "indexed"
	DocumentStatusDeleted = "deleted"
	DocumentStatusFailed  = "failed"
)

type Repository interface {
	GetProfile(context.Context, agentcore.MemoryProfileID) (memcore.MemoryProfile, error)
	GetRecord(context.Context, string) (memcore.MemoryRecord, error)
	FindRecords(context.Context, memstore.RecordQuery) ([]memcore.MemoryRecord, error)
}

type Provider interface {
	EnsureKnowledgeBase(context.Context, provider.EnsureKnowledgeBaseRequest) (provider.EnsureKnowledgeBaseResult, error)
	UploadDocument(context.Context, provider.UploadDocumentRequest) (provider.UploadDocumentResult, error)
	DeleteDocument(context.Context, provider.DeleteDocumentRequest) error
	RefreshDocumentStatus(context.Context, provider.DocumentStatusRequest) (provider.DocumentStatusResult, error)
	Search(context.Context, provider.SearchRequest) (provider.SearchResultPage, error)
}

type ProviderFunc func() Provider

type Service struct {
	memory        Repository
	state         State
	provider      ProviderFunc
	now           func() time.Time
	observeRecall RecallObserver
}

type ServiceOptions struct {
	Memory        Repository
	State         State
	Provider      ProviderFunc
	Now           func() time.Time
	ObserveRecall RecallObserver
}

// RecallObserver sees only the final authorized records. It must not mutate
// them or wait for inference; its return value cannot affect retrieval.
type RecallObserver func(context.Context, RecallRequest, []memcore.MemoryRecord)

type ReconcileReport struct {
	Scanned        int
	Indexed        int
	Deleted        int
	Skipped        int
	Failed         int
	OrphansScanned int
}

type RecallRequest struct {
	Query       string
	Subject     memcore.AccessSubject
	ProfileRefs []agentcore.MemoryProfileRef
	Limit       int
}

func NewService(options ServiceOptions) *Service {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Service{memory: options.Memory, state: options.State, provider: options.Provider, now: now, observeRecall: options.ObserveRecall}
}

func (s *Service) Reconcile(ctx context.Context, limit int) (ReconcileReport, error) {
	report := ReconcileReport{}
	if err := s.requireConfigured(); err != nil {
		return report, err
	}
	records, err := s.memory.FindRecords(ctx, memstore.RecordQuery{IncludeArchived: true, Limit: limit})
	if err != nil {
		return report, err
	}
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		report.Scanned++
		changed, deleted, err := s.SyncRecord(ctx, record)
		if err != nil {
			report.Failed++
			continue
		}
		switch {
		case deleted:
			report.Deleted++
		case changed:
			report.Indexed++
		default:
			report.Skipped++
		}
	}
	if err := s.reconcileOrphanBindings(ctx, limit, &report); err != nil {
		return report, err
	}
	return report, nil
}

func (s *Service) reconcileOrphanBindings(ctx context.Context, limit int, report *ReconcileReport) error {
	state, ok := s.state.(DocumentBindingState)
	if !ok {
		return nil
	}
	bindings, err := state.ListDocumentBindings(ctx, limit)
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		if err := ctx.Err(); err != nil {
			return err
		}
		report.OrphansScanned++
		if _, err := s.memory.GetRecord(ctx, binding.MemoryID); err == nil {
			continue
		} else if !errors.Is(err, memcore.ErrNotFound) {
			report.Failed++
			continue
		}
		profile, err := s.memory.GetProfile(ctx, binding.ProfileID)
		if err != nil {
			report.Failed++
			continue
		}
		if strings.TrimSpace(binding.ProviderDocumentID) != "" {
			base := memoryKnowledgeBase(profile, binding.FolderID, s.now().UTC())
			err = s.pageIndex().DeleteDocument(ctx, provider.DeleteDocumentRequest{
				Base: base,
				Document: knowledgecore.KnowledgeDocument{
					ID:                 knowledgecore.KnowledgeDocumentID(binding.MemoryID),
					KnowledgeBaseID:    base.ID,
					ProviderDocumentID: binding.ProviderDocumentID,
				},
			})
			if err != nil && !errors.Is(err, knowledgecore.ErrNotFound) {
				report.Failed++
				continue
			}
		}
		if err := state.DeleteDocumentBinding(ctx, binding.MemoryID); err != nil {
			report.Failed++
			continue
		}
		report.Deleted++
	}
	return nil
}

func (s *Service) SyncRecord(ctx context.Context, record memcore.MemoryRecord) (changed bool, deleted bool, err error) {
	if err := s.requireConfigured(); err != nil {
		return false, false, err
	}
	record = record.Normalized().Clone()
	if strings.TrimSpace(record.ID) == "" || record.ProfileID == "" {
		return false, false, fmt.Errorf("%w: memory identity is incomplete", memcore.ErrInvalid)
	}
	document, documentErr := s.state.GetDocumentBinding(ctx, record.ID)
	if documentErr != nil && !errors.Is(documentErr, memcore.ErrNotFound) {
		return false, false, documentErr
	}
	if memoryRecordExcluded(record) && (errors.Is(documentErr, memcore.ErrNotFound) || document.Status == DocumentStatusDeleted) {
		return false, false, nil
	}
	profile, err := s.memory.GetProfile(ctx, record.ProfileID)
	if err != nil {
		return false, false, err
	}
	binding, base, err := s.ensureProfileBinding(ctx, profile)
	if err != nil {
		return false, false, err
	}
	if memoryRecordExcluded(record) {
		if err := s.deleteDocument(ctx, base, record, document); err != nil {
			return false, false, s.saveDocumentFailure(ctx, record, binding, document, err)
		}
		document.Status = DocumentStatusDeleted
		document.ErrorMessage = ""
		document.ContentHash = ""
		document.UpdatedAt = s.now().UTC()
		if err := s.state.SaveDocumentBinding(ctx, document); err != nil {
			return false, false, err
		}
		return false, true, nil
	}

	content := RenderDocument(record)
	hash := contentHash(content)
	if documentErr == nil && document.Status != DocumentStatusDeleted && document.ContentHash == hash {
		changed, needsUpload, refreshErr := s.refreshDocumentBinding(ctx, base, document)
		if refreshErr != nil || !needsUpload {
			return changed, false, refreshErr
		}
		document = DocumentBinding{}
	}
	if documentErr == nil && strings.TrimSpace(document.ProviderDocumentID) != "" && document.Status != DocumentStatusDeleted {
		if revisionProvider, ok := s.pageIndex().(provider.RevisionProvider); ok {
			updated, updateErr := updateDocumentRevision(ctx, revisionProvider, document, content, string(record.CreatedBy))
			if updateErr == nil {
				document.Revision = updated.Revision
				document.ContentHash = hash
				document.Status = string(knowledgecore.DocumentParsing)
				document.ErrorMessage = ""
				document.VerifiedAt = time.Time{}
				document.UpdatedAt = s.now().UTC()
				if err := s.state.SaveDocumentBinding(ctx, document); err != nil {
					return false, false, err
				}
				return true, false, nil
			}
		}
		if err := s.deleteDocument(ctx, base, record, document); err != nil {
			return false, false, s.saveDocumentFailure(ctx, record, binding, document, err)
		}
	}

	uploaded, err := s.pageIndex().UploadDocument(ctx, provider.UploadDocumentRequest{
		Base:        base,
		DocumentID:  knowledgecore.KnowledgeDocumentID(record.ID),
		Filename:    memoryFilename(record),
		ContentType: "text/markdown",
		Reader:      bytes.NewBufferString(content),
		CreatedBy:   firstNonBlank(string(record.CreatedBy), "orrery-memory"),
	})
	if err != nil {
		return false, false, s.saveDocumentFailure(ctx, record, binding, document, err)
	}
	document = DocumentBinding{
		MemoryID:           record.ID,
		ProfileID:          record.ProfileID,
		FolderID:           binding.FolderID,
		ProviderDocumentID: uploaded.ProviderDocumentID,
		Revision:           1,
		ContentHash:        hash,
		Status:             memoryDocumentStatus(uploaded.Status),
		ErrorMessage:       strings.TrimSpace(uploaded.ErrorMessage),
		UpdatedAt:          s.now().UTC(),
	}
	if err := s.state.SaveDocumentBinding(ctx, document); err != nil {
		return false, false, err
	}
	return true, false, nil
}

func (s *Service) Recall(ctx context.Context, request RecallRequest) ([]memcore.MemoryRecord, error) {
	if err := s.requireConfigured(); err != nil {
		return nil, err
	}
	query := strings.TrimSpace(request.Query)
	if query == "" || len(request.ProfileRefs) == 0 {
		return nil, nil
	}
	allowed := map[agentcore.MemoryProfileID]struct{}{}
	bases := make([]knowledgecore.KnowledgeBase, 0, len(request.ProfileRefs))
	for _, ref := range request.ProfileRefs {
		if ref.MemoryProfileID == "" {
			continue
		}
		profile, err := s.memory.GetProfile(ctx, ref.MemoryProfileID)
		if err != nil {
			if errors.Is(err, memcore.ErrNotFound) {
				continue
			}
			return nil, err
		}
		if !profile.CanRead(request.Subject) {
			continue
		}
		binding, err := s.state.GetProfileBinding(ctx, profile.ID)
		if err != nil {
			if errors.Is(err, memcore.ErrNotFound) {
				continue
			}
			return nil, err
		}
		allowed[profile.ID] = struct{}{}
		bases = append(bases, memoryKnowledgeBase(profile, binding.FolderID, s.now().UTC()))
	}
	if len(bases) == 0 {
		return nil, nil
	}
	limit := request.Limit
	if limit <= 0 {
		limit = 20
	}
	page, err := s.pageIndex().Search(ctx, provider.SearchRequest{
		Query: query, Bases: bases, Limit: min(50, max(20, limit*4)), RetrievalMode: provider.RetrievalModeAuto,
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(page.Results, func(i, j int) bool { return page.Results[i].Score > page.Results[j].Score })
	out := make([]memcore.MemoryRecord, 0, min(limit, len(page.Results)))
	seen := map[string]struct{}{}
	for _, result := range page.Results {
		binding, err := s.resolveDocumentBinding(ctx, result.DocumentID)
		if err != nil || binding.Status != DocumentStatusIndexed {
			continue
		}
		if _, ok := allowed[binding.ProfileID]; !ok {
			continue
		}
		if _, ok := seen[binding.MemoryID]; ok {
			continue
		}
		record, err := s.memory.GetRecord(ctx, binding.MemoryID)
		if err != nil || !memcore.RetrievalEligible(record, s.now()) || record.ProfileID != binding.ProfileID {
			continue
		}
		seen[binding.MemoryID] = struct{}{}
		out = append(out, record.Normalized().Clone())
		if len(out) >= limit {
			break
		}
	}
	if _, err := memretrieval.ValidateRecords(ctx, s.memory, request.Subject, out, s.now()); err != nil {
		return nil, err
	}
	if s.observeRecall != nil && len(out) > 0 {
		s.observeRecall(ctx, request, out)
	}
	if _, err := memretrieval.ValidateRecords(ctx, s.memory, request.Subject, out, s.now()); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) refreshDocumentBinding(ctx context.Context, base knowledgecore.KnowledgeBase, binding DocumentBinding) (changed bool, needsUpload bool, err error) {
	now := s.now().UTC()
	if binding.Status == DocumentStatusIndexed && !binding.VerifiedAt.IsZero() {
		return false, false, nil
	}
	if binding.Status == DocumentStatusFailed && !binding.VerifiedAt.IsZero() && now.Sub(binding.VerifiedAt) < 5*time.Minute {
		return false, false, fmt.Errorf("PageIndex document processing failed: %s", firstNonBlank(binding.ErrorMessage, "unknown provider error"))
	}
	if strings.TrimSpace(binding.ProviderDocumentID) == "" {
		return false, true, nil
	}
	result, err := s.pageIndex().RefreshDocumentStatus(ctx, provider.DocumentStatusRequest{
		Base: base,
		Document: knowledgecore.KnowledgeDocument{
			ID:                 knowledgecore.KnowledgeDocumentID(binding.MemoryID),
			KnowledgeBaseID:    base.ID,
			ProviderDocumentID: binding.ProviderDocumentID,
		},
	})
	if err != nil {
		if errors.Is(err, knowledgecore.ErrNotFound) {
			return false, true, nil
		}
		return false, false, err
	}
	binding.Status = memoryDocumentStatus(result.Status)
	binding.ErrorMessage = strings.TrimSpace(result.ErrorMessage)
	binding.VerifiedAt = now
	binding.UpdatedAt = now
	if err := s.state.SaveDocumentBinding(ctx, binding); err != nil {
		return false, false, err
	}
	if binding.Status == DocumentStatusFailed {
		return true, false, fmt.Errorf("PageIndex document processing failed: %s", firstNonBlank(binding.ErrorMessage, "unknown provider error"))
	}
	return binding.Status == DocumentStatusIndexed, false, nil
}

func memoryDocumentStatus(status knowledgecore.DocumentStatus) string {
	switch status {
	case knowledgecore.DocumentIndexed:
		return DocumentStatusIndexed
	case knowledgecore.DocumentFailed:
		return DocumentStatusFailed
	case knowledgecore.DocumentUploaded, knowledgecore.DocumentParsing, knowledgecore.DocumentChunking, knowledgecore.DocumentEmbedding, knowledgecore.DocumentDeleting:
		return string(status)
	default:
		return string(knowledgecore.DocumentParsing)
	}
}

func (s *Service) ensureProfileBinding(ctx context.Context, profile memcore.MemoryProfile) (ProfileBinding, knowledgecore.KnowledgeBase, error) {
	binding, err := s.state.GetProfileBinding(ctx, profile.ID)
	if err == nil && strings.TrimSpace(binding.FolderID) != "" {
		return binding, memoryKnowledgeBase(profile, binding.FolderID, binding.UpdatedAt), nil
	}
	if err != nil && !errors.Is(err, memcore.ErrNotFound) {
		return ProfileBinding{}, knowledgecore.KnowledgeBase{}, err
	}
	base := memoryKnowledgeBase(profile, "", s.now().UTC())
	ensured, err := s.pageIndex().EnsureKnowledgeBase(ctx, provider.EnsureKnowledgeBaseRequest{Base: base})
	if err != nil {
		return ProfileBinding{}, knowledgecore.KnowledgeBase{}, err
	}
	if strings.TrimSpace(ensured.ProviderResourceID) == "" {
		return ProfileBinding{}, knowledgecore.KnowledgeBase{}, fmt.Errorf("PageIndex returned an empty memory folder id")
	}
	binding = ProfileBinding{ProfileID: profile.ID, FolderID: ensured.ProviderResourceID, UpdatedAt: s.now().UTC()}
	if err := s.state.SaveProfileBinding(ctx, binding); err != nil {
		return ProfileBinding{}, knowledgecore.KnowledgeBase{}, err
	}
	base.ProviderResourceID = binding.FolderID
	return binding, base, nil
}

func (s *Service) deleteDocument(ctx context.Context, base knowledgecore.KnowledgeBase, record memcore.MemoryRecord, binding DocumentBinding) error {
	if strings.TrimSpace(binding.ProviderDocumentID) == "" {
		return nil
	}
	return s.pageIndex().DeleteDocument(ctx, provider.DeleteDocumentRequest{
		Base: base,
		Document: knowledgecore.KnowledgeDocument{
			ID:                 knowledgecore.KnowledgeDocumentID(record.ID),
			KnowledgeBaseID:    base.ID,
			ProviderDocumentID: binding.ProviderDocumentID,
		},
	})
}

func (s *Service) saveDocumentFailure(ctx context.Context, record memcore.MemoryRecord, profile ProfileBinding, document DocumentBinding, failure error) error {
	if document.MemoryID == "" {
		document.MemoryID = record.ID
		document.ProfileID = record.ProfileID
		document.FolderID = profile.FolderID
	}
	document.Status = DocumentStatusFailed
	document.ErrorMessage = failure.Error()
	document.UpdatedAt = s.now().UTC()
	if err := s.state.SaveDocumentBinding(ctx, document); err != nil {
		return errors.Join(failure, err)
	}
	return failure
}

func (s *Service) resolveDocumentBinding(ctx context.Context, documentID string) (DocumentBinding, error) {
	binding, err := s.state.GetDocumentBinding(ctx, documentID)
	if err == nil {
		return binding, nil
	}
	if !errors.Is(err, memcore.ErrNotFound) {
		return DocumentBinding{}, err
	}
	return s.state.FindDocumentBindingByProviderID(ctx, documentID)
}

func (s *Service) pageIndex() Provider {
	if s == nil || s.provider == nil {
		return nil
	}
	return s.provider()
}

func (s *Service) requireConfigured() error {
	if s == nil || s.memory == nil || s.state == nil || s.pageIndex() == nil {
		return fmt.Errorf("memory PageIndex service is not configured")
	}
	return nil
}

func memoryKnowledgeBase(profile memcore.MemoryProfile, folderID string, at time.Time) knowledgecore.KnowledgeBase {
	ownerType := knowledgecore.OwnerOrganization
	ownerID := firstNonBlank(profile.OwnerRef.OrganizationID, profile.DefaultScope.ID)
	visibility := knowledgecore.VisibilityScoped
	if profile.MemoryKind == agentcore.MemoryKindPersonal || profile.DefaultScope.Kind == memcore.ScopeAgent {
		ownerType = knowledgecore.OwnerAgent
		ownerID = firstNonBlank(profile.OwnerRef.AgentID.String(), profile.DefaultScope.ID)
		visibility = knowledgecore.VisibilityPrivate
	}
	return knowledgecore.KnowledgeBase{
		ID:                 knowledgecore.KnowledgeBaseID("memory-index-" + shortHash(string(profile.ID))),
		Name:               "Memory " + string(profile.ID),
		Description:        "Orrery managed memory index",
		OwnerType:          ownerType,
		OwnerID:            ownerID,
		Visibility:         visibility,
		Provider:           knowledgecore.ProviderPageIndex,
		ProviderResourceID: folderID,
		Status:             knowledgecore.KnowledgeActive,
		CreatedBy:          "orrery-memory",
		CreatedAt:          at,
		UpdatedAt:          at,
	}
}

func updateDocumentRevision(ctx context.Context, revisions provider.RevisionProvider, binding DocumentBinding, content, actor string) (provider.DocumentRevision, error) {
	expected := binding.Revision
	if expected < 1 {
		items, err := revisions.ListDocumentRevisions(ctx, provider.DocumentRevisionsRequest{DocumentID: binding.ProviderDocumentID})
		if err != nil {
			return provider.DocumentRevision{}, err
		}
		for _, item := range items {
			if item.Revision > expected {
				expected = item.Revision
			}
		}
	}
	updated, err := revisions.UpdateDocumentContent(ctx, provider.UpdateDocumentContentRequest{
		DocumentID:       binding.ProviderDocumentID,
		ExpectedRevision: expected,
		Content:          content,
		ContentType:      "text/markdown",
		CreatedBy:        firstNonBlank(actor, "orrery-memory"),
	})
	if err != nil {
		return provider.DocumentRevision{}, err
	}
	if updated.Revision > 0 {
		published, err := revisions.PublishDocumentRevision(ctx, provider.PublishDocumentRevisionRequest{
			DocumentID:  binding.ProviderDocumentID,
			Revision:    updated.Revision,
			PublishedBy: firstNonBlank(actor, "orrery-memory"),
		})
		if err != nil {
			return provider.DocumentRevision{}, err
		}
		if published.Revision > 0 {
			updated = published
		}
	}
	return updated, nil
}

func memoryRecordExcluded(record memcore.MemoryRecord) bool {
	switch memcore.NormalizeMemoryLifecycleStatus(record.LifecycleStatus) {
	case memcore.MemoryLifecycleArchived, memcore.MemoryLifecycleDeleted:
		return true
	default:
		return record.DisabledAt != nil
	}
}

func memoryFilename(record memcore.MemoryRecord) string {
	return "memory-" + shortHash(record.ID) + ".md"
}

func contentHash(content string) string {
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}

func shortHash(value string) string {
	hash := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(hash[:8])
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
