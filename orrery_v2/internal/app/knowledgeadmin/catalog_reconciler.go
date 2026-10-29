package knowledgeadmin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	pathpkg "path"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/provider"
	"orrery_v2/internal/knowledge/store"
)

const remoteCatalogOrigin = "pageindex_remote"

type CatalogReconcileResult struct {
	Created   int `json:"created"`
	Updated   int `json:"updated"`
	Unchanged int `json:"unchanged"`
	Removed   int `json:"removed"`
	Rejected  int `json:"rejected"`
}

type CatalogReconciler struct {
	repo     store.Repository
	registry provider.Registry
	source   provider.FolderDocumentLister
	clock    Clock
	gateOnce sync.Once
	gate     chan struct{}
}

func NewCatalogReconciler(repo store.Repository, registry provider.Registry, clock Clock) *CatalogReconciler {
	return &CatalogReconciler{repo: repo, registry: registry, clock: clock}
}

func newCatalogReconcilerWithSource(repo store.Repository, source provider.FolderDocumentLister, clock Clock) *CatalogReconciler {
	return &CatalogReconciler{repo: repo, source: source, clock: clock}
}

func (r *CatalogReconciler) Reconcile(ctx context.Context, base core.KnowledgeBase) (CatalogReconcileResult, error) {
	if r == nil || r.repo == nil || r.clock == nil {
		return CatalogReconcileResult{}, ErrServiceDependency
	}
	r.gateOnce.Do(func() { r.gate = make(chan struct{}, 1) })
	if err := ctx.Err(); err != nil {
		return CatalogReconcileResult{}, err
	}
	select {
	case r.gate <- struct{}{}:
		defer func() { <-r.gate }()
	case <-ctx.Done():
		return CatalogReconcileResult{}, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return CatalogReconcileResult{}, err
	}
	source, err := r.sourceForBase(base)
	if err != nil {
		return CatalogReconcileResult{}, err
	}
	remote, err := source.ListFolderDocuments(ctx, provider.FolderDocumentsRequest{FolderID: base.ProviderResourceID})
	if err != nil {
		return CatalogReconcileResult{}, err
	}

	result := CatalogReconcileResult{}
	type catalogProjection struct {
		item     provider.FolderDocument
		relative string
		folder   string
		filename string
	}
	projections := make([]catalogProjection, 0, len(remote))
	remoteIDs := make(map[string]bool, len(remote))
	remotePaths := make(map[string]bool, len(remote))
	for _, item := range remote {
		if strings.TrimSpace(item.FolderID) != strings.TrimSpace(base.ProviderResourceID) {
			return CatalogReconcileResult{}, fmt.Errorf("%w: PageIndex catalog document belongs to a different folder", core.ErrDependencyUnavailable)
		}
		item.DocumentID = strings.TrimSpace(item.DocumentID)
		if item.DocumentID == "" || remoteIDs[item.DocumentID] {
			return CatalogReconcileResult{}, fmt.Errorf("%w: PageIndex catalog contains a duplicate or empty document id", core.ErrDependencyUnavailable)
		}
		remoteIDs[item.DocumentID] = true
		relative, folder, filename, err := normalizeCatalogKnowledgeKey(item.KnowledgeKey)
		if err != nil || strings.TrimSpace(item.Status) != string(core.DocumentIndexed) {
			result.Rejected++
			continue
		}
		if remotePaths[relative] {
			return CatalogReconcileResult{}, fmt.Errorf("%w: PageIndex catalog contains a duplicate knowledge key", core.ErrDependencyUnavailable)
		}
		remotePaths[relative] = true
		projections = append(projections, catalogProjection{item: item, relative: relative, folder: folder, filename: filename})
	}

	localPage, err := r.repo.ListDocuments(ctx, core.DocumentQuery{KnowledgeBaseID: base.ID})
	if err != nil {
		return CatalogReconcileResult{}, err
	}
	localByProviderID := make(map[string]core.KnowledgeDocument, len(localPage.Items))
	localByID := make(map[core.KnowledgeDocumentID]core.KnowledgeDocument, len(localPage.Items))
	for _, document := range localPage.Items {
		providerID := strings.TrimSpace(document.ProviderDocumentID)
		if providerID != "" {
			if _, duplicate := localByProviderID[providerID]; duplicate {
				return CatalogReconcileResult{}, fmt.Errorf("%w: local catalog contains a duplicate provider document id", core.ErrConflict)
			}
			localByProviderID[providerID] = document
		}
		localByID[document.ID] = document
	}

	now := r.clock.Now().UTC()
	for _, projection := range projections {
		item := projection.item
		existing, found := localByProviderID[item.DocumentID]
		if !found {
			id := remoteCatalogDocumentID(base.ID, item.DocumentID)
			if occupied, exists := localByID[id]; exists && strings.TrimSpace(occupied.ProviderDocumentID) != item.DocumentID {
				return result, fmt.Errorf("%w: remote catalog document id collision", core.ErrConflict)
			}
			document := remoteCatalogDocument(base, item, projection.relative, projection.folder, projection.filename, now)
			if err := r.repo.SaveDocument(ctx, document); err != nil {
				return result, err
			}
			result.Created++
			continue
		}

		document := mergeRemoteCatalogDocument(existing, item, projection.relative, projection.folder, projection.filename)
		if equalRemoteCatalogDocument(existing, document) {
			result.Unchanged++
			continue
		}
		document.UpdatedAt = now
		if document.Status == core.DocumentIndexed && document.IndexedAt.IsZero() {
			document.IndexedAt = now
		}
		if err := r.repo.SaveDocument(ctx, document); err != nil {
			return result, err
		}
		result.Updated++
	}

	for _, document := range localPage.Items {
		if document.ProviderMetadata["catalog_origin"] != remoteCatalogOrigin || remoteIDs[strings.TrimSpace(document.ProviderDocumentID)] {
			continue
		}
		if err := r.repo.DeleteDocumentMetadata(ctx, document.ID); err != nil {
			return result, err
		}
		result.Removed++
	}
	return result, nil
}

func remoteCatalogDocument(base core.KnowledgeBase, item provider.FolderDocument, relative, folder, filename string, now time.Time) core.KnowledgeDocument {
	return core.KnowledgeDocument{
		ID:                 remoteCatalogDocumentID(base.ID, item.DocumentID),
		KnowledgeBaseID:    base.ID,
		ProviderDocumentID: item.DocumentID,
		ProviderMetadata: map[string]string{
			"catalog_origin": remoteCatalogOrigin,
			"folder_path":    folder,
			"relative_path":  relative,
			"source_uri":     strings.TrimSpace(item.Source),
			"title":          strings.TrimSpace(item.Title),
		},
		Filename:    filename,
		ContentType: strings.TrimSpace(item.ContentType),
		SizeBytes:   item.SizeBytes,
		Checksum:    strings.TrimSpace(item.Checksum),
		Status:      core.DocumentIndexed,
		CreatedBy:   "pageindex-catalog-reconciler",
		CreatedAt:   now,
		UpdatedAt:   now,
		IndexedAt:   now,
	}
}

func mergeRemoteCatalogDocument(existing core.KnowledgeDocument, item provider.FolderDocument, relative, folder, filename string) core.KnowledgeDocument {
	document := existing
	document.ProviderDocumentID = item.DocumentID
	document.Filename = filename
	document.ContentType = strings.TrimSpace(item.ContentType)
	document.SizeBytes = item.SizeBytes
	document.Checksum = strings.TrimSpace(item.Checksum)
	document.Status = core.DocumentIndexed
	document.ErrorMessage = ""
	document.ProviderMetadata = maps.Clone(existing.ProviderMetadata)
	if document.ProviderMetadata == nil {
		document.ProviderMetadata = map[string]string{}
	}
	if existing.ProviderMetadata["catalog_origin"] == remoteCatalogOrigin {
		document.ProviderMetadata["catalog_origin"] = remoteCatalogOrigin
	}
	document.ProviderMetadata["folder_path"] = folder
	document.ProviderMetadata["relative_path"] = relative
	document.ProviderMetadata["source_uri"] = strings.TrimSpace(item.Source)
	document.ProviderMetadata["title"] = strings.TrimSpace(item.Title)
	return document
}

func equalRemoteCatalogDocument(left, right core.KnowledgeDocument) bool {
	return left.ID == right.ID &&
		left.KnowledgeBaseID == right.KnowledgeBaseID &&
		left.ProviderDocumentID == right.ProviderDocumentID &&
		left.Filename == right.Filename &&
		left.ContentType == right.ContentType &&
		left.SizeBytes == right.SizeBytes &&
		left.Checksum == right.Checksum &&
		left.Status == right.Status &&
		left.ErrorMessage == right.ErrorMessage &&
		maps.Equal(left.ProviderMetadata, right.ProviderMetadata)
}

func (r *CatalogReconciler) sourceForBase(base core.KnowledgeBase) (provider.FolderDocumentLister, error) {
	if r.source != nil {
		return r.source, nil
	}
	if r.registry == nil {
		return nil, ErrServiceDependency
	}
	resolved := r.registry.ForBase(base)
	source, ok := resolved.(provider.FolderDocumentLister)
	if !ok || source == nil {
		return nil, ErrServiceDependency
	}
	return source, nil
}

func normalizeCatalogKnowledgeKey(value string) (relative string, folder string, filename string, err error) {
	relative = strings.TrimSpace(value)
	if relative == "" || strings.HasPrefix(relative, "/") || strings.Contains(relative, "\\") || strings.Contains(relative, "://") || strings.ContainsRune(relative, 0) {
		return "", "", "", fmt.Errorf("%w: unsafe catalog knowledge key", core.ErrInvalid)
	}
	segments := strings.Split(relative, "/")
	for index, segment := range segments {
		if segment == "" || segment == "." || segment == ".." || (index == 0 && len(segment) == 2 && segment[1] == ':') {
			return "", "", "", fmt.Errorf("%w: unsafe catalog knowledge key", core.ErrInvalid)
		}
	}
	if pathpkg.Clean(relative) != relative {
		return "", "", "", fmt.Errorf("%w: unsafe catalog knowledge key", core.ErrInvalid)
	}
	filename = pathpkg.Base(relative)
	if filename == "" || filename == "." || filename == "/" {
		return "", "", "", fmt.Errorf("%w: unsafe catalog knowledge key", core.ErrInvalid)
	}
	folder = pathpkg.Dir(relative)
	if folder == "." {
		folder = ""
	}
	return relative, folder, filename, nil
}

func remoteCatalogDocumentID(baseID core.KnowledgeBaseID, providerDocumentID string) core.KnowledgeDocumentID {
	sum := sha256.Sum256([]byte(string(baseID) + "\x00" + strings.TrimSpace(providerDocumentID)))
	return core.KnowledgeDocumentID("kdoc-remote-" + hex.EncodeToString(sum[:12]))
}
