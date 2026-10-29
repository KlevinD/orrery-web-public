package knowledgeprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"path/filepath"
	"strings"
	"time"

	knowledgecore "orrery_v2/internal/knowledge/core"
	knowledgestore "orrery_v2/internal/knowledge/store"
	doccore "orrery_v2/internal/platform/document/core"
)

type BindingStore interface {
	SaveBinding(context.Context, Binding) error
	GetBinding(context.Context, string) (Binding, error)
	ListBindings(context.Context) ([]Binding, error)
	SaveMapping(context.Context, ExternalMapping) error
	FindMapping(context.Context, string, ResourceKind, string) (ExternalMapping, error)
}

type ExternalCatalog interface {
	ListFolders(context.Context, Binding, string) ([]ExternalFolder, error)
	ListDocuments(context.Context, Binding, string, string) ([]ExternalDocument, error)
}

type CapabilityDetector interface {
	DetectCapabilities(context.Context, Binding) (map[Capability]string, error)
}

type SyncRequest struct {
	PrincipalID string
	OwnerID     string
}

type SyncResult struct {
	FoldersSynced   int       `json:"folders_synced"`
	DocumentsSynced int       `json:"documents_synced"`
	CompletedAt     time.Time `json:"completed_at"`
}

type Service struct {
	bindings  BindingStore
	catalog   ExternalCatalog
	knowledge knowledgestore.Repository
	now       func() time.Time
}

func NewService(bindings BindingStore, catalog ExternalCatalog, knowledge knowledgestore.Repository, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{bindings: bindings, catalog: catalog, knowledge: knowledge, now: now}
}

func (s *Service) ListBindings(ctx context.Context) ([]Binding, error) {
	if s == nil || s.bindings == nil {
		return nil, fmt.Errorf("knowledge provider service is not configured")
	}
	return s.bindings.ListBindings(ctx)
}

func (s *Service) GetBinding(ctx context.Context, id string) (Binding, error) {
	if s == nil || s.bindings == nil {
		return Binding{}, fmt.Errorf("knowledge provider service is not configured")
	}
	return s.bindings.GetBinding(ctx, strings.TrimSpace(id))
}

func (s *Service) CreateBinding(ctx context.Context, binding Binding, principalID string) (Binding, error) {
	if s == nil || s.bindings == nil {
		return Binding{}, fmt.Errorf("knowledge provider service is not configured")
	}
	if err := validateBinding(binding); err != nil {
		return Binding{}, err
	}
	if _, err := s.bindings.GetBinding(ctx, binding.ID); err == nil {
		return Binding{}, ErrAlreadyExists
	} else if !errors.Is(err, ErrNotFound) {
		return Binding{}, err
	}
	now := s.now().UTC()
	binding.ToolMapping = resolvedToolMapping(binding)
	binding.Status = StatusActive
	binding.CreatedAt = now
	binding.UpdatedAt = now
	binding.CreatedBy = strings.TrimSpace(principalID)
	binding.UpdatedBy = strings.TrimSpace(principalID)
	if err := s.bindings.SaveBinding(ctx, binding); err != nil {
		return Binding{}, err
	}
	return binding, nil
}

func (s *Service) UpdateBinding(ctx context.Context, binding Binding, principalID string) (Binding, error) {
	if s == nil || s.bindings == nil {
		return Binding{}, fmt.Errorf("knowledge provider service is not configured")
	}
	existing, err := s.bindings.GetBinding(ctx, binding.ID)
	if err != nil {
		return Binding{}, err
	}
	if err := validateBinding(binding); err != nil {
		return Binding{}, err
	}
	binding.ToolMapping = resolvedToolMapping(binding)
	binding.CreatedAt = existing.CreatedAt
	binding.CreatedBy = existing.CreatedBy
	binding.LastSyncedAt = existing.LastSyncedAt
	binding.LastSyncCursor = existing.LastSyncCursor
	binding.LastError = existing.LastError
	binding.UpdatedAt = s.now().UTC()
	binding.UpdatedBy = strings.TrimSpace(principalID)
	if err := s.bindings.SaveBinding(ctx, binding); err != nil {
		return Binding{}, err
	}
	return binding, nil
}

func (s *Service) TestBinding(ctx context.Context, id, principalID string) (CapabilityTestResult, error) {
	binding, err := s.GetBinding(ctx, id)
	if err != nil {
		return CapabilityTestResult{}, err
	}
	detector, ok := s.catalog.(CapabilityDetector)
	if !ok {
		return CapabilityTestResult{}, ErrDependencyUnavailable
	}
	capabilities, err := detector.DetectCapabilities(ctx, binding)
	if err != nil {
		return CapabilityTestResult{}, err
	}
	return CapabilityTestResult{BindingID: binding.ID, Capabilities: capabilities}, nil
}

func (s *Service) Sync(ctx context.Context, bindingID string, request SyncRequest) (SyncResult, error) {
	if s == nil || s.bindings == nil || s.catalog == nil || s.knowledge == nil {
		return SyncResult{}, fmt.Errorf("knowledge provider service is not configured")
	}
	if strings.TrimSpace(request.PrincipalID) == "" || strings.TrimSpace(request.OwnerID) == "" {
		return SyncResult{}, fmt.Errorf("principal and owner are required")
	}
	binding, err := s.bindings.GetBinding(ctx, strings.TrimSpace(bindingID))
	if err != nil {
		return SyncResult{}, err
	}
	if binding.Status == StatusDisabled {
		return SyncResult{}, fmt.Errorf("knowledge provider binding is disabled")
	}

	folders, err := s.catalog.ListFolders(ctx, binding, request.PrincipalID)
	if err != nil {
		return SyncResult{}, s.recordSyncFailure(ctx, binding, err)
	}
	result := SyncResult{}
	for _, folder := range folders {
		if !folderSelected(folder.Name, binding.FolderInclude, binding.FolderExclude) {
			continue
		}
		baseID, err := s.localID(ctx, binding.ID, ResourceFolder, folder.ID, "kb")
		if err != nil {
			return SyncResult{}, s.recordSyncFailure(ctx, binding, err)
		}
		now := s.now().UTC()
		base := knowledgecore.KnowledgeBase{
			ID: knowledgecore.KnowledgeBaseID(baseID), Name: folder.Name,
			Description: stringMetadata(folder.Metadata, "description"),
			OwnerType:   knowledgecore.OwnerOrganization, OwnerID: request.OwnerID,
			Visibility: knowledgecore.VisibilityScoped, Provider: knowledgecore.ProviderMCP,
			ProviderResourceID: folder.ID,
			ProviderMetadata:   providerMetadata(binding, folder.Metadata),
			ChunkPolicy:        doccore.ChunkPolicy{MaxRunes: 2400, OverlapRunes: 240, MinRunes: 400, PreserveHeads: true},
			Status:             knowledgecore.KnowledgeActive, CreatedBy: request.PrincipalID, CreatedAt: now, UpdatedAt: now,
		}
		if existing, getErr := s.knowledge.GetKnowledgeBase(ctx, base.ID); getErr == nil {
			preserveManagedKnowledgeBaseConfiguration(&base, existing)
		} else if !errors.Is(getErr, knowledgecore.ErrNotFound) {
			return SyncResult{}, s.recordSyncFailure(ctx, binding, getErr)
		}
		if err := s.knowledge.SaveKnowledgeBase(ctx, base); err != nil {
			return SyncResult{}, s.recordSyncFailure(ctx, binding, err)
		}
		if err := s.bindings.SaveMapping(ctx, ExternalMapping{BindingID: binding.ID, Kind: ResourceFolder, ExternalID: folder.ID, LocalID: baseID, UpdatedAt: now}); err != nil {
			return SyncResult{}, s.recordSyncFailure(ctx, binding, err)
		}
		result.FoldersSynced++

		documents, err := s.catalog.ListDocuments(ctx, binding, request.PrincipalID, folder.ID)
		if err != nil {
			return SyncResult{}, s.recordSyncFailure(ctx, binding, err)
		}
		existingDocuments, err := s.documentsByProviderID(ctx, base.ID)
		if err != nil {
			return SyncResult{}, s.recordSyncFailure(ctx, binding, err)
		}
		for _, external := range documents {
			documentID, err := s.localID(ctx, binding.ID, ResourceDocument, external.ID, "doc")
			if err != nil {
				return SyncResult{}, s.recordSyncFailure(ctx, binding, err)
			}
			existing, found := existingDocuments[strings.TrimSpace(external.ID)]
			if found {
				documentID = string(existing.ID)
				if existing.ProviderMetadata["binding_id"] != binding.ID {
					if err := s.bindings.SaveMapping(ctx, ExternalMapping{BindingID: binding.ID, Kind: ResourceDocument, ExternalID: external.ID, LocalID: documentID, UpdatedAt: now}); err != nil {
						return SyncResult{}, s.recordSyncFailure(ctx, binding, err)
					}
					result.DocumentsSynced++
					continue
				}
			}
			document := knowledgecore.KnowledgeDocument{
				ID: knowledgecore.KnowledgeDocumentID(documentID), KnowledgeBaseID: base.ID,
				ProviderDocumentID: external.ID, ProviderMetadata: providerMetadata(binding, external.Metadata),
				Filename: external.Name, ContentType: contentType(external.Name), Status: knowledgecore.DocumentIndexed,
				CreatedBy: request.PrincipalID, CreatedAt: now, UpdatedAt: now, IndexedAt: now,
			}
			if occupied, getErr := s.knowledge.GetDocument(ctx, document.ID); getErr == nil {
				if strings.TrimSpace(occupied.ProviderDocumentID) != strings.TrimSpace(external.ID) {
					return SyncResult{}, s.recordSyncFailure(ctx, binding, fmt.Errorf("%w: external document mapping collides with an existing document", knowledgecore.ErrConflict))
				}
				document.CreatedAt = occupied.CreatedAt
				document.CreatedBy = occupied.CreatedBy
			} else if !errors.Is(getErr, knowledgecore.ErrNotFound) {
				return SyncResult{}, s.recordSyncFailure(ctx, binding, getErr)
			}
			if err := s.knowledge.SaveDocument(ctx, document); err != nil {
				return SyncResult{}, s.recordSyncFailure(ctx, binding, err)
			}
			existingDocuments[strings.TrimSpace(external.ID)] = document
			if err := s.bindings.SaveMapping(ctx, ExternalMapping{BindingID: binding.ID, Kind: ResourceDocument, ExternalID: external.ID, LocalID: documentID, UpdatedAt: now}); err != nil {
				return SyncResult{}, s.recordSyncFailure(ctx, binding, err)
			}
			result.DocumentsSynced++
		}
	}
	result.CompletedAt = s.now().UTC()
	binding.Status = StatusActive
	binding.LastError = ""
	binding.LastSyncedAt = result.CompletedAt
	binding.UpdatedAt = result.CompletedAt
	binding.UpdatedBy = request.PrincipalID
	if err := s.bindings.SaveBinding(ctx, binding); err != nil {
		return SyncResult{}, err
	}
	return result, nil
}

func (s *Service) documentsByProviderID(ctx context.Context, knowledgeBaseID knowledgecore.KnowledgeBaseID) (map[string]knowledgecore.KnowledgeDocument, error) {
	page, err := s.knowledge.ListDocuments(ctx, knowledgecore.DocumentQuery{KnowledgeBaseID: knowledgeBaseID})
	if err != nil {
		return nil, err
	}
	documents := make(map[string]knowledgecore.KnowledgeDocument, len(page.Items))
	for _, document := range page.Items {
		providerID := strings.TrimSpace(document.ProviderDocumentID)
		if providerID == "" {
			continue
		}
		if _, duplicate := documents[providerID]; duplicate {
			return nil, fmt.Errorf("%w: local catalog contains a duplicate provider document id", knowledgecore.ErrConflict)
		}
		documents[providerID] = document
	}
	return documents, nil
}

func preserveManagedKnowledgeBaseConfiguration(discovered *knowledgecore.KnowledgeBase, existing knowledgecore.KnowledgeBase) {
	if discovered == nil {
		return
	}
	discovered.OwnerType = existing.OwnerType
	discovered.OwnerID = existing.OwnerID
	discovered.IsDefault = existing.IsDefault
	discovered.Visibility = existing.Visibility
	discovered.Provider = existing.Provider
	discovered.ProviderResourceID = existing.ProviderResourceID
	discovered.ProviderMetadata = cloneStringMetadata(existing.ProviderMetadata)
	discovered.VectorBackend = existing.VectorBackend
	discovered.VectorStorePath = existing.VectorStorePath
	discovered.EmbeddingProfileRef = existing.EmbeddingProfileRef
	discovered.EmbeddingModel = existing.EmbeddingModel
	discovered.EmbeddingDimensions = existing.EmbeddingDimensions
	discovered.ChunkPolicy = existing.ChunkPolicy
	discovered.Status = existing.Status
	discovered.CreatedBy = existing.CreatedBy
	discovered.CreatedAt = existing.CreatedAt
}

func (s *Service) localID(ctx context.Context, bindingID string, kind ResourceKind, externalID, prefix string) (string, error) {
	mapping, err := s.bindings.FindMapping(ctx, bindingID, kind, externalID)
	if err == nil {
		return mapping.LocalID, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return "", err
	}
	return externalLocalID(prefix, bindingID, externalID), nil
}

func (s *Service) recordSyncFailure(ctx context.Context, binding Binding, cause error) error {
	now := s.now().UTC()
	binding.Status = StatusError
	binding.LastError = cause.Error()
	binding.UpdatedAt = now
	_ = s.bindings.SaveBinding(ctx, binding)
	if errors.Is(cause, ErrDependencyUnavailable) {
		return cause
	}
	return fmt.Errorf("%w: %v", ErrDependencyUnavailable, cause)
}

func externalLocalID(prefix, bindingID, externalID string) string {
	sum := sha256.Sum256([]byte(bindingID + "\x00" + externalID))
	return prefix + "-" + hex.EncodeToString(sum[:16])
}

func folderSelected(name string, includes, excludes []string) bool {
	name = strings.TrimSpace(name)
	for _, excluded := range excludes {
		if name == strings.TrimSpace(excluded) {
			return false
		}
	}
	if len(includes) == 0 {
		return true
	}
	for _, included := range includes {
		if name == strings.TrimSpace(included) {
			return true
		}
	}
	return false
}

func providerMetadata(binding Binding, external map[string]any) map[string]string {
	metadata := map[string]string{"binding_id": binding.ID, "provider_display_name": binding.Name}
	if len(external) > 0 {
		if payload, err := json.Marshal(external); err == nil {
			metadata["external_metadata"] = string(payload)
		}
	}
	return metadata
}

func stringMetadata(metadata map[string]any, key string) string {
	value, _ := metadata[key].(string)
	return strings.TrimSpace(value)
}

func contentType(filename string) string {
	value := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename)))
	if value == "" {
		return "application/octet-stream"
	}
	return value
}

func validateBinding(binding Binding) error {
	if strings.TrimSpace(binding.ID) == "" || strings.TrimSpace(binding.Name) == "" || strings.TrimSpace(binding.MCPServerID) == "" {
		return fmt.Errorf("%w: binding id, name, and MCP server id are required", ErrInvalid)
	}
	if binding.Mode != ModeReadOnly && binding.Mode != ModeReadWrite {
		return fmt.Errorf("%w: binding mode is invalid", ErrInvalid)
	}
	if binding.Status != "" && binding.Status != StatusActive && binding.Status != StatusDisabled && binding.Status != StatusError {
		return fmt.Errorf("%w: binding status is invalid", ErrInvalid)
	}
	return nil
}
