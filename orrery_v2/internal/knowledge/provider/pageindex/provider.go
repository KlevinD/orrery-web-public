package pageindex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/provider"
)

const (
	defaultAPIURL                    = "https://api.pageindex.ai"
	defaultMaxPageIndexResponseBytes = 64 << 20
	maxConcurrentSearchRequests      = 8
)

type Config struct {
	Disabled         bool
	APIURL           string
	APIKey           string
	APIKeyRef        string
	Timeout          time.Duration
	MaxResponseBytes int64
	HTTPClient       *http.Client
}

type Provider struct {
	cfg    Config
	client *http.Client
}

func New(cfg Config) *Provider {
	client := cfg.HTTPClient
	if client == nil {
		timeout := cfg.Timeout
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		client = &http.Client{Timeout: timeout}
	}
	if strings.TrimSpace(cfg.APIURL) == "" {
		cfg.APIURL = defaultAPIURL
	}
	return &Provider{cfg: cfg, client: client}
}

func (p *Provider) Name() core.KnowledgeProvider {
	return core.ProviderPageIndex
}

func (p *Provider) EnsureKnowledgeBase(ctx context.Context, req provider.EnsureKnowledgeBaseRequest) (provider.EnsureKnowledgeBaseResult, error) {
	if err := p.ready(); err != nil {
		return provider.EnsureKnowledgeBaseResult{}, err
	}
	if folderID := strings.TrimSpace(req.Base.ProviderResourceID); folderID != "" && !isUnsafePageIndexFolderID(folderID) {
		return provider.EnsureKnowledgeBaseResult{ProviderResourceID: folderID, Metadata: cloneMetadata(req.Base.ProviderMetadata)}, nil
	}
	body := map[string]any{
		"name":        strings.TrimSpace(req.Base.Name),
		"description": strings.TrimSpace(req.Base.Description),
		"metadata":    pageIndexFolderMetadata(req.Base),
	}
	var out pageIndexFolder
	if err := p.doJSON(ctx, http.MethodPost, "/folder/", body, &out); err != nil {
		return provider.EnsureKnowledgeBaseResult{}, err
	}
	if strings.TrimSpace(out.ID) == "" {
		return provider.EnsureKnowledgeBaseResult{}, fmt.Errorf("%w: pageindex folder id missing", core.ErrDependencyUnavailable)
	}
	return provider.EnsureKnowledgeBaseResult{ProviderResourceID: out.ID, Metadata: map[string]string{"pageindex_folder_name": out.Name}}, nil
}

func (p *Provider) DeleteKnowledgeBase(ctx context.Context, req provider.DeleteKnowledgeBaseRequest) error {
	if err := p.ready(); err != nil {
		return err
	}
	folderID := strings.TrimSpace(req.Base.ProviderResourceID)
	if folderID == "" {
		return fmt.Errorf("%w: pageindex folder id is required for knowledge base delete", core.ErrInvalid)
	}
	if isUnsafePageIndexFolderID(folderID) {
		return unsafePageIndexRootFolderError("knowledge base delete")
	}
	return p.doJSON(ctx, http.MethodDelete, "/folder/"+url.PathEscape(folderID), nil, nil)
}

func (p *Provider) UploadDocument(ctx context.Context, req provider.UploadDocumentRequest) (provider.UploadDocumentResult, error) {
	if err := p.ready(); err != nil {
		return provider.UploadDocumentResult{}, err
	}
	if req.Reader == nil {
		return provider.UploadDocumentResult{}, fmt.Errorf("%w: document reader is required", core.ErrInvalid)
	}
	folderID := strings.TrimSpace(req.Base.ProviderResourceID)
	if folderID == "" {
		return provider.UploadDocumentResult{}, fmt.Errorf("%w: pageindex folder_id is required for document upload", core.ErrInvalid)
	}
	if isUnsafePageIndexFolderID(folderID) {
		return provider.UploadDocumentResult{}, unsafePageIndexRootFolderError("document upload")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	fileWriter, err := writer.CreateFormFile("file", strings.TrimSpace(req.Filename))
	if err != nil {
		return provider.UploadDocumentResult{}, err
	}
	size, err := io.Copy(fileWriter, req.Reader)
	if err != nil {
		return provider.UploadDocumentResult{}, err
	}
	if err := writer.WriteField("folder_id", folderID); err != nil {
		return provider.UploadDocumentResult{}, err
	}
	if contentType := strings.TrimSpace(req.ContentType); contentType != "" {
		if err := writer.WriteField("content_type", contentType); err != nil {
			return provider.UploadDocumentResult{}, err
		}
	}
	uploadMetadata := map[string]string{}
	if documentID := strings.TrimSpace(string(req.DocumentID)); documentID != "" {
		uploadMetadata["orrery_document_id"] = documentID
	}
	if knowledgeBaseID := strings.TrimSpace(string(req.Base.ID)); knowledgeBaseID != "" {
		uploadMetadata["orrery_knowledge_base_id"] = knowledgeBaseID
	}
	if ownerType := strings.TrimSpace(string(req.Base.OwnerType)); ownerType != "" {
		uploadMetadata["orrery_owner_type"] = ownerType
	}
	if ownerID := strings.TrimSpace(req.Base.OwnerID); ownerID != "" {
		uploadMetadata["orrery_owner_id"] = ownerID
	}
	if createdBy := strings.TrimSpace(req.CreatedBy); createdBy != "" {
		uploadMetadata["orrery_created_by"] = createdBy
	}
	metadataJSON, err := json.Marshal(uploadMetadata)
	if err != nil {
		return provider.UploadDocumentResult{}, err
	}
	if err := writer.WriteField("metadata", string(metadataJSON)); err != nil {
		return provider.UploadDocumentResult{}, err
	}
	if err := writer.WriteField("async", "true"); err != nil {
		return provider.UploadDocumentResult{}, err
	}
	if err := writer.Close(); err != nil {
		return provider.UploadDocumentResult{}, err
	}
	request, err := p.newRequest(ctx, http.MethodPost, "/doc/", &body)
	if err != nil {
		return provider.UploadDocumentResult{}, err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	var out pageIndexDocument
	if err := p.do(request, &out); err != nil {
		return provider.UploadDocumentResult{}, err
	}
	if err := validatePageIndexDocument(out, folderID, "", "document upload"); err != nil {
		return provider.UploadDocumentResult{}, err
	}
	filename := firstNonBlank(out.Filename, req.Filename)
	status := documentStatusFromPageIndex(out.Status, core.DocumentParsing)
	return provider.UploadDocumentResult{
		ProviderDocumentID: out.ID,
		Filename:           filename,
		ContentType:        firstNonBlank(out.ContentType, req.ContentType),
		SizeBytes:          firstPositiveInt64(out.SizeBytes, size),
		Checksum:           strings.TrimSpace(out.Checksum),
		Status:             status,
		ProviderMetadata:   pageIndexUploadMetadata(out),
	}, nil
}

func (p *Provider) CreateDocumentMetadata(ctx context.Context, req provider.CreateDocumentMetadataRequest) (provider.UploadDocumentResult, error) {
	if err := ctx.Err(); err != nil {
		return provider.UploadDocumentResult{}, err
	}
	return provider.UploadDocumentResult{
		ArtifactID:         req.Document.ArtifactID,
		ProviderDocumentID: req.Document.ProviderDocumentID,
		Filename:           req.Document.Filename,
		ContentType:        req.Document.ContentType,
		SizeBytes:          req.Document.SizeBytes,
		Checksum:           req.Document.Checksum,
		Status:             req.Document.Status,
		ProviderMetadata:   cloneMetadata(req.Document.ProviderMetadata),
	}, nil
}

func (p *Provider) DeleteDocument(ctx context.Context, req provider.DeleteDocumentRequest) error {
	if err := p.ready(); err != nil {
		return err
	}
	if err := p.rejectUnsafeBaseFolder(req.Base, "document delete"); err != nil {
		return err
	}
	docID := strings.TrimSpace(req.Document.ProviderDocumentID)
	if docID == "" {
		docID = strings.TrimSpace(req.Document.ArtifactID)
	}
	if docID == "" {
		return fmt.Errorf("%w: pageindex document id is required", core.ErrInvalid)
	}
	if _, err := p.getValidatedDocument(ctx, docID, req.Base.ProviderResourceID, "document delete"); err != nil {
		return err
	}
	return p.doJSON(ctx, http.MethodDelete, "/doc/"+url.PathEscape(docID), nil, nil)
}

func (p *Provider) RefreshDocumentStatus(ctx context.Context, req provider.DocumentStatusRequest) (provider.DocumentStatusResult, error) {
	if err := p.ready(); err != nil {
		return provider.DocumentStatusResult{}, err
	}
	if err := p.rejectUnsafeBaseFolder(req.Base, "document status refresh"); err != nil {
		return provider.DocumentStatusResult{}, err
	}
	docID := pageIndexDocumentID(req.Document)
	if docID == "" {
		return provider.DocumentStatusResult{}, fmt.Errorf("%w: pageindex document id is required", core.ErrInvalid)
	}
	var doc pageIndexDocument
	if err := p.doJSON(ctx, http.MethodGet, "/doc/"+url.PathEscape(docID), nil, &doc); err != nil {
		return provider.DocumentStatusResult{}, err
	}
	if err := validatePageIndexDocument(doc, req.Base.ProviderResourceID, docID, "document status refresh"); err != nil {
		return provider.DocumentStatusResult{}, err
	}
	var out pageIndexJob
	if err := p.doJSON(ctx, http.MethodGet, "/doc/"+url.PathEscape(docID)+"/job", nil, &out); err != nil {
		return provider.DocumentStatusResult{}, err
	}
	if err := validatePageIndexJob(out, req.Base.ProviderResourceID, docID, "document status refresh"); err != nil {
		return provider.DocumentStatusResult{}, err
	}
	job := mapPageIndexJob(out)
	metadata := cloneMetadata(req.Document.ProviderMetadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	for key, value := range pageIndexDocumentMetadata(doc) {
		metadata[key] = value
	}
	if job.JobID != "" {
		metadata["pageindex_job_id"] = job.JobID
	}
	if out.Status != "" {
		metadata["pageindex_job_status"] = strings.TrimSpace(out.Status)
	}
	if out.Stage != "" {
		metadata["pageindex_job_stage"] = strings.TrimSpace(out.Stage)
	}
	metadata["pageindex_job_progress"] = strconv.Itoa(clampedProgress(out.Progress))
	return provider.DocumentStatusResult{
		ProviderDocumentID: firstNonBlank(doc.ID, out.DocumentID, req.Document.ProviderDocumentID, docID),
		Filename:           firstNonBlank(doc.Filename, req.Document.Filename),
		ContentType:        firstNonBlank(doc.ContentType, req.Document.ContentType),
		SizeBytes:          firstPositiveInt64(doc.SizeBytes, req.Document.SizeBytes),
		Checksum:           firstNonBlank(doc.Checksum, req.Document.Checksum),
		Status:             documentStatusFromJob(out, documentStatusFromPageIndex(doc.Status, req.Document.Status)),
		ErrorMessage:       firstNonBlank(out.ErrorMessage, doc.ErrorMessage, req.Document.ErrorMessage),
		ProviderMetadata:   metadata,
		Job:                &job,
	}, nil
}

func (p *Provider) ReprocessDocument(ctx context.Context, req provider.ReprocessDocumentRequest) (provider.ReprocessDocumentResult, error) {
	if err := p.ready(); err != nil {
		return provider.ReprocessDocumentResult{}, err
	}
	if err := p.rejectUnsafeBaseFolder(req.Base, "document reprocess"); err != nil {
		return provider.ReprocessDocumentResult{}, err
	}
	docID := pageIndexDocumentID(req.Document)
	if docID == "" {
		return provider.ReprocessDocumentResult{}, fmt.Errorf("%w: pageindex document id is required", core.ErrInvalid)
	}
	if _, err := p.getValidatedDocument(ctx, docID, req.Base.ProviderResourceID, "document reprocess"); err != nil {
		return provider.ReprocessDocumentResult{}, err
	}
	var out pageIndexJob
	if err := p.doJSON(ctx, http.MethodPost, "/doc/"+url.PathEscape(docID)+"/retry", nil, &out); err != nil {
		return provider.ReprocessDocumentResult{}, err
	}
	if err := validatePageIndexJob(out, req.Base.ProviderResourceID, docID, "document reprocess"); err != nil {
		return provider.ReprocessDocumentResult{}, err
	}
	return provider.ReprocessDocumentResult{
		Status:       documentStatusFromJob(out, core.DocumentParsing),
		ErrorMessage: strings.TrimSpace(out.ErrorMessage),
		Job:          mapPageIndexJob(out),
	}, nil
}

func (p *Provider) GetDocumentProcessingResult(ctx context.Context, req provider.DocumentProcessingResultRequest) (provider.DocumentProcessingResult, error) {
	if err := p.ready(); err != nil {
		return provider.DocumentProcessingResult{}, err
	}
	if err := p.rejectUnsafeBaseFolder(req.Base, "document processing result"); err != nil {
		return provider.DocumentProcessingResult{}, err
	}
	docID := pageIndexDocumentID(req.Document)
	if docID == "" {
		return provider.DocumentProcessingResult{}, fmt.Errorf("%w: pageindex document id is required", core.ErrInvalid)
	}
	pages := strings.TrimSpace(req.Pages)
	if pages == "" {
		pages = "1"
	}
	requestPath := "/doc/" + url.PathEscape(docID) + "/preview?pages=" + url.QueryEscape(pages)
	var out pageIndexDocumentPreview
	if err := p.doJSON(ctx, http.MethodGet, requestPath, nil, &out); err != nil {
		return provider.DocumentProcessingResult{}, err
	}
	if err := validatePageIndexDocument(out.Document, req.Base.ProviderResourceID, docID, "document processing result"); err != nil {
		return provider.DocumentProcessingResult{}, err
	}
	if out.Job != nil {
		if err := validatePageIndexJob(*out.Job, req.Base.ProviderResourceID, docID, "document processing result"); err != nil {
			return provider.DocumentProcessingResult{}, err
		}
	}
	var job *provider.DocumentProcessingJob
	if out.Job != nil {
		mapped := mapPageIndexJob(*out.Job)
		job = &mapped
	}
	return provider.DocumentProcessingResult{
		Filename:       firstNonBlank(out.Document.Filename, req.Document.Filename),
		Status:         documentStatusFromPageIndex(out.Status, documentStatusFromPageIndex(out.Document.Status, req.Document.Status)),
		Progress:       out.Progress,
		Stage:          strings.TrimSpace(out.Stage),
		ErrorMessage:   firstNonBlank(out.ErrorMessage, out.Document.ErrorMessage, req.Document.ErrorMessage),
		Job:            job,
		Structure:      cloneAnyMaps(out.Structure),
		Pages:          cloneAnyMaps(out.Pages),
		StructureNodes: normalizeStructureNodes(out.Structure),
		PagePreviews:   normalizePagePreviews(out.Pages),
	}, nil
}

func (p *Provider) ReindexKnowledgeBase(ctx context.Context, req provider.ReindexKnowledgeBaseRequest) (provider.ReindexKnowledgeBaseResult, error) {
	if err := p.ready(); err != nil {
		return provider.ReindexKnowledgeBaseResult{}, err
	}
	folderID := strings.TrimSpace(req.Base.ProviderResourceID)
	if folderID == "" {
		return provider.ReindexKnowledgeBaseResult{}, fmt.Errorf("%w: pageindex folder id is required for reindex", core.ErrInvalid)
	}
	if isUnsafePageIndexFolderID(folderID) {
		return provider.ReindexKnowledgeBaseResult{}, unsafePageIndexRootFolderError("reindex")
	}
	var out pageIndexReindexResponse
	if err := p.doJSON(ctx, http.MethodPost, "/folder/"+url.PathEscape(folderID)+"/reindex", nil, &out); err != nil {
		return provider.ReindexKnowledgeBaseResult{}, err
	}
	if err := validatePageIndexResourceID(folderID, out.FolderID, "folder", "reindex"); err != nil {
		return provider.ReindexKnowledgeBaseResult{}, err
	}
	jobID := out.firstJobID()
	if jobID == "" {
		if out.Queued <= 0 {
			return provider.ReindexKnowledgeBaseResult{Supported: true}, nil
		}
		return provider.ReindexKnowledgeBaseResult{}, fmt.Errorf("%w: pageindex reindex job id missing", core.ErrDependencyUnavailable)
	}
	return provider.ReindexKnowledgeBaseResult{JobID: core.KnowledgeJobID(jobID), Queued: true, Supported: true}, nil
}

func (p *Provider) Search(ctx context.Context, req provider.SearchRequest) (provider.SearchResultPage, error) {
	if err := p.ready(); err != nil {
		return provider.SearchResultPage{}, err
	}
	query := strings.TrimSpace(req.Query)
	limit := req.Limit
	if limit <= 0 {
		limit = 8
	} else if limit > 50 {
		limit = 50
	}
	type searchBase struct {
		base     core.KnowledgeBase
		folderID string
	}
	targets := make([]searchBase, 0, len(req.Bases))
	for _, base := range req.Bases {
		folderID := strings.TrimSpace(base.ProviderResourceID)
		if folderID == "" {
			return provider.SearchResultPage{}, fmt.Errorf("%w: pageindex folder id is required for search", core.ErrInvalid)
		}
		if isUnsafePageIndexFolderID(folderID) {
			return provider.SearchResultPage{}, unsafePageIndexRootFolderError("search")
		}
		targets = append(targets, searchBase{base: base, folderID: folderID})
	}
	results := make([]provider.SearchResult, 0)
	executions := make([]provider.SearchExecution, 0, len(targets))
	var mu sync.Mutex
	var wg sync.WaitGroup
	var firstErr error
	succeeded := 0
	searchSlots := make(chan struct{}, maxConcurrentSearchRequests)
	for _, target := range targets {
		target := target
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case searchSlots <- struct{}{}:
				defer func() { <-searchSlots }()
			case <-ctx.Done():
				mu.Lock()
				if firstErr == nil {
					firstErr = ctx.Err()
				}
				mu.Unlock()
				return
			}
			body := pageIndexSearchRequest{
				Query:          query,
				Limit:          limit,
				IncludeContent: true,
				RetrievalMode:  string(req.RetrievalMode),
			}
			var out pageIndexSearchResponse
			if err := p.doJSON(ctx, http.MethodPost, "/folder/"+url.PathEscape(target.folderID)+"/search", body, &out); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			mapped := mapPageIndexSearchResults(target.base, target.folderID, out.allResults())
			execution, executionErr := pageIndexSearchExecution(target.base, req.RetrievalMode, out)
			mu.Lock()
			if executionErr != nil {
				if firstErr == nil {
					firstErr = executionErr
				}
				mu.Unlock()
				return
			}
			results = append(results, mapped...)
			executions = append(executions, execution)
			succeeded++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if firstErr != nil && succeeded == 0 {
		return provider.SearchResultPage{}, firstErr
	}
	return provider.SearchResultPage{Results: results, Executions: executions}, nil
}

func (p *Provider) UpdateDocumentContent(ctx context.Context, req provider.UpdateDocumentContentRequest) (provider.DocumentRevision, error) {
	if err := p.ready(); err != nil {
		return provider.DocumentRevision{}, err
	}
	documentID := strings.TrimSpace(req.DocumentID)
	if documentID == "" || req.ExpectedRevision < 0 || strings.TrimSpace(req.ContentType) == "" || strings.TrimSpace(req.CreatedBy) == "" {
		return provider.DocumentRevision{}, fmt.Errorf("%w: document id, content type, created by, and non-negative expected revision are required", core.ErrInvalid)
	}
	body := map[string]any{
		"expected_revision": req.ExpectedRevision,
		"content":           req.Content,
		"content_type":      strings.TrimSpace(req.ContentType),
		"created_by":        strings.TrimSpace(req.CreatedBy),
	}
	var out provider.DocumentRevision
	err := p.doJSON(ctx, http.MethodPut, "/doc/"+url.PathEscape(documentID)+"/content", body, &out)
	return out, err
}

func (p *Provider) ListDocumentRevisions(ctx context.Context, req provider.DocumentRevisionsRequest) ([]provider.DocumentRevision, error) {
	if err := p.ready(); err != nil {
		return nil, err
	}
	documentID := strings.TrimSpace(req.DocumentID)
	if documentID == "" {
		return nil, fmt.Errorf("%w: document id is required", core.ErrInvalid)
	}
	var out []provider.DocumentRevision
	err := p.doJSON(ctx, http.MethodGet, "/doc/"+url.PathEscape(documentID)+"/revisions", nil, &out)
	return out, err
}

func (p *Provider) GetDocumentRevision(ctx context.Context, req provider.DocumentRevisionRequest) (provider.DocumentRevision, error) {
	if err := p.ready(); err != nil {
		return provider.DocumentRevision{}, err
	}
	documentID := strings.TrimSpace(req.DocumentID)
	if documentID == "" || req.Revision < 1 {
		return provider.DocumentRevision{}, fmt.Errorf("%w: document id and positive revision are required", core.ErrInvalid)
	}
	var out provider.DocumentRevision
	err := p.doJSON(ctx, http.MethodGet, "/doc/"+url.PathEscape(documentID)+"/revisions/"+strconv.Itoa(req.Revision), nil, &out)
	return out, err
}

func (p *Provider) GetSemanticSource(ctx context.Context, req provider.SemanticSourceRequest) (provider.SemanticSource, error) {
	if err := p.ready(); err != nil {
		return provider.SemanticSource{}, err
	}
	documentID := strings.TrimSpace(req.DocumentID)
	if documentID == "" || req.Revision < 1 {
		return provider.SemanticSource{}, fmt.Errorf("%w: document id and positive revision are required", core.ErrInvalid)
	}
	requestPath := "/doc/" + url.PathEscape(documentID) + "/semantic-source?revision=" + strconv.Itoa(req.Revision)
	var out provider.SemanticSource
	err := p.doJSON(ctx, http.MethodGet, requestPath, nil, &out)
	return out, err
}

func (p *Provider) PublishDocumentRevision(ctx context.Context, req provider.PublishDocumentRevisionRequest) (provider.DocumentRevision, error) {
	if err := p.ready(); err != nil {
		return provider.DocumentRevision{}, err
	}
	documentID := strings.TrimSpace(req.DocumentID)
	publishedBy := strings.TrimSpace(req.PublishedBy)
	if documentID == "" || req.Revision < 1 || publishedBy == "" {
		return provider.DocumentRevision{}, fmt.Errorf("%w: document id, positive revision, and publisher are required", core.ErrInvalid)
	}
	requestPath := "/doc/" + url.PathEscape(documentID) + "/revisions/" + strconv.Itoa(req.Revision) + "/publication"
	var out provider.DocumentRevision
	err := p.doJSON(ctx, http.MethodPut, requestPath, map[string]string{"published_by": publishedBy}, &out)
	return out, err
}

func (p *Provider) ListKnowledgeChanges(ctx context.Context, req provider.KnowledgeChangesRequest) (provider.KnowledgeChangePage, error) {
	if err := p.ready(); err != nil {
		return provider.KnowledgeChangePage{}, err
	}
	folderID := strings.TrimSpace(req.FolderID)
	if folderID == "" || isUnsafePageIndexFolderID(folderID) {
		return provider.KnowledgeChangePage{}, fmt.Errorf("%w: safe folder id is required", core.ErrInvalid)
	}
	values := url.Values{}
	if cursor := strings.TrimSpace(req.Cursor); cursor != "" {
		values.Set("cursor", cursor)
	}
	if req.Limit > 0 {
		values.Set("limit", strconv.Itoa(req.Limit))
	}
	requestPath := "/folder/" + url.PathEscape(folderID) + "/changes"
	if encoded := values.Encode(); encoded != "" {
		requestPath += "?" + encoded
	}
	var out provider.KnowledgeChangePage
	err := p.doJSON(ctx, http.MethodGet, requestPath, nil, &out)
	return out, err
}

func (p *Provider) ListFolderDocuments(ctx context.Context, req provider.FolderDocumentsRequest) ([]provider.FolderDocument, error) {
	return p.listFolderDocuments(ctx, req, false)
}

func (p *Provider) ListFolderDocumentInventory(ctx context.Context, req provider.FolderDocumentsRequest) ([]provider.FolderDocument, error) {
	return p.listFolderDocuments(ctx, req, true)
}

func (p *Provider) listFolderDocuments(ctx context.Context, req provider.FolderDocumentsRequest, complete bool) ([]provider.FolderDocument, error) {
	if err := p.ready(); err != nil {
		return nil, err
	}
	folderID := strings.TrimSpace(req.FolderID)
	if folderID == "" {
		return nil, fmt.Errorf("%w: pageindex folder id is required", core.ErrInvalid)
	}
	if isUnsafePageIndexFolderID(folderID) {
		return nil, unsafePageIndexRootFolderError("folder document listing")
	}
	var documents []pageIndexDocument
	if err := p.doJSON(ctx, http.MethodGet, "/folder/"+url.PathEscape(folderID)+"/documents", nil, &documents); err != nil {
		return nil, err
	}
	out := make([]provider.FolderDocument, 0, len(documents))
	for _, document := range documents {
		if err := validatePageIndexDocument(document, folderID, document.ID, "folder document listing"); err != nil {
			return nil, err
		}
		status := documentStatusFromPageIndex(document.Status, "")
		if !complete && status != core.DocumentIndexed {
			continue
		}
		knowledgeKey, _ := firstStringFromAnyMap(document.Metadata, "knowledge_key")
		source, _ := firstStringFromAnyMap(document.Metadata, "source")
		title, _ := firstStringFromAnyMap(document.Metadata, "title")
		out = append(out, provider.FolderDocument{
			DocumentID: document.ID, FolderID: document.FolderID,
			Filename: document.Filename, ContentType: document.ContentType,
			SizeBytes: document.SizeBytes, Checksum: document.Checksum,
			Status: string(status), KnowledgeKey: knowledgeKey,
			Source: source, Title: firstNonBlank(title, document.DocName),
		})
	}
	return out, nil
}

func (p *Provider) GetIndexedDocumentSource(ctx context.Context, req provider.IndexedDocumentSourceRequest) (provider.IndexedDocumentSource, error) {
	if err := p.ready(); err != nil {
		return provider.IndexedDocumentSource{}, err
	}
	documentID := strings.TrimSpace(req.DocumentID)
	if documentID == "" {
		return provider.IndexedDocumentSource{}, fmt.Errorf("%w: document id is required", core.ErrInvalid)
	}
	var document pageIndexDocument
	if err := p.doJSON(ctx, http.MethodGet, "/doc/"+url.PathEscape(documentID), nil, &document); err != nil {
		return provider.IndexedDocumentSource{}, err
	}
	if strings.TrimSpace(document.ID) != documentID || documentStatusFromPageIndex(document.Status, "") != core.DocumentIndexed {
		return provider.IndexedDocumentSource{}, fmt.Errorf("%w: indexed PageIndex document is required", core.ErrInvalid)
	}
	var structure pageIndexStructureResponse
	if err := p.doJSON(ctx, http.MethodGet, "/doc/"+url.PathEscape(documentID)+"/structure", nil, &structure); err != nil {
		return provider.IndexedDocumentSource{}, err
	}
	if strings.TrimSpace(structure.DocumentID) != documentID {
		return provider.IndexedDocumentSource{}, fmt.Errorf("%w: PageIndex structure document id mismatch", core.ErrDependencyUnavailable)
	}
	selectionLimit := document.PageCount
	if selectionLimit <= 0 {
		selectionLimit = document.LineCount
	}
	if selectionLimit <= 0 {
		selectionLimit = 1
	}
	if selectionLimit > 100 {
		selectionLimit = 100
	}
	selection := "1"
	if selectionLimit > 1 {
		selection = "1-" + strconv.Itoa(selectionLimit)
	}
	var pages pageIndexPagesResponse
	requestPath := "/doc/" + url.PathEscape(documentID) + "/pages?pages=" + url.QueryEscape(selection)
	if err := p.doJSON(ctx, http.MethodGet, requestPath, nil, &pages); err != nil {
		return provider.IndexedDocumentSource{}, err
	}
	if strings.TrimSpace(pages.DocumentID) != documentID {
		return provider.IndexedDocumentSource{}, fmt.Errorf("%w: PageIndex pages document id mismatch", core.ErrDependencyUnavailable)
	}
	content := renderIndexedDocumentSource(document, structure.Structure, pages.Pages)
	digest := sha256.Sum256([]byte(content))
	contentHash := "sha256:" + hex.EncodeToString(digest[:])
	publishedAt := document.IndexedAt
	if publishedAt.IsZero() {
		publishedAt = document.UpdatedAt
	}
	if publishedAt.IsZero() {
		publishedAt = document.CreatedAt
	}
	revision := provider.DocumentRevision{
		ID: "indexed:" + documentID + ":1", DocumentID: documentID, FolderID: document.FolderID,
		Revision: 1, ContentHash: contentHash, ContentType: "text/markdown", SizeBytes: int64(len(content)),
		CreatedBy: "pageindex-legacy-import", CreatedAt: document.CreatedAt,
		PublicationStatus: "published", PublishedBy: "pageindex-indexer", PublishedAt: &publishedAt,
	}
	return provider.IndexedDocumentSource{
		Revision: revision,
		Source: provider.SemanticSource{
			DocumentID: documentID, Revision: 1, ContentHash: contentHash,
			ContentType: "text/markdown", Content: content, Editable: false,
		},
	}, nil
}

func renderIndexedDocumentSource(document pageIndexDocument, structure, pages []map[string]any) string {
	var builder strings.Builder
	title := firstNonBlank(document.DocName, document.Filename, document.ID)
	if title != "" {
		builder.WriteString("# ")
		builder.WriteString(title)
		builder.WriteString("\n\n")
	}
	var appendNodes func([]provider.DocumentStructureNode)
	appendNodes = func(nodes []provider.DocumentStructureNode) {
		for _, node := range nodes {
			if text := strings.TrimSpace(node.Text); text != "" {
				builder.WriteString(text)
				builder.WriteByte('\n')
			}
			appendNodes(node.Children)
		}
	}
	appendNodes(normalizeStructureNodes(structure))
	for _, page := range normalizePagePreviews(pages) {
		if text := strings.TrimSpace(page.Text); text != "" {
			builder.WriteString(text)
			builder.WriteByte('\n')
		}
	}
	if strings.TrimSpace(builder.String()) == "" {
		builder.WriteString(document.ID)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func (p *Provider) ready() error {
	if p == nil {
		return fmt.Errorf("%w: pageindex provider is not configured", core.ErrDependencyUnavailable)
	}
	if p.cfg.Disabled {
		return fmt.Errorf("%w: pageindex provider is disabled", core.ErrDependencyUnavailable)
	}
	if strings.TrimSpace(p.cfg.APIKey) == "" {
		return fmt.Errorf("%w: pageindex api key is required", core.ErrDependencyUnavailable)
	}
	return nil
}

func (p *Provider) doJSON(ctx context.Context, method string, requestPath string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	request, err := p.newRequest(ctx, method, requestPath, reader)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	return p.do(request, out)
}

func (p *Provider) newRequest(ctx context.Context, method string, requestPath string, body io.Reader) (*http.Request, error) {
	base, err := url.Parse(strings.TrimRight(p.cfg.APIURL, "/"))
	if err != nil {
		return nil, err
	}
	relative, err := url.Parse(requestPath)
	if err != nil {
		return nil, err
	}
	base.Path = path.Join(base.Path, relative.Path)
	if strings.HasSuffix(relative.Path, "/") && !strings.HasSuffix(base.Path, "/") {
		base.Path += "/"
	}
	base.RawQuery = relative.RawQuery
	request, err := http.NewRequestWithContext(ctx, method, base.String(), body)
	if err != nil {
		return nil, err
	}
	if key := strings.TrimSpace(p.cfg.APIKey); key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	requestMetadata := provider.RequestMetadataFromContext(ctx)
	if requestMetadata.RequestID != "" {
		request.Header.Set("X-Request-ID", requestMetadata.RequestID)
	}
	if requestMetadata.Actor != "" {
		request.Header.Set("X-Orrery-Actor", requestMetadata.Actor)
	}
	return request, nil
}

func (p *Provider) do(request *http.Request, out any) error {
	response, err := p.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		if err := mapPageIndexError(response.StatusCode, data); err != nil {
			return fmt.Errorf("%w: pageindex %s %s returned %d: %s", err, request.Method, request.URL.Path, response.StatusCode, strings.TrimSpace(string(data)))
		}
		return fmt.Errorf("%w: pageindex %s %s returned %d: %s", core.ErrDependencyUnavailable, request.Method, request.URL.Path, response.StatusCode, strings.TrimSpace(string(data)))
	}
	if out == nil || response.StatusCode == http.StatusNoContent {
		return nil
	}
	limit := p.cfg.MaxResponseBytes
	if limit <= 0 {
		limit = defaultMaxPageIndexResponseBytes
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return fmt.Errorf("read pageindex response: %w", err)
	}
	if int64(len(data)) > limit {
		return fmt.Errorf("%w: pageindex response exceeds %d bytes", core.ErrDependencyUnavailable, limit)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode pageindex response: %w", err)
	}
	return nil
}

func mapPageIndexError(status int, data []byte) error {
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Field   string `json:"field"`
		Detail  []struct {
			Loc  []any  `json:"loc"`
			Msg  string `json:"msg"`
			Type string `json:"type"`
		} `json:"detail"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		if status == http.StatusUnprocessableEntity {
			return core.ErrInvalid
		}
		return nil
	}
	if status == http.StatusGone && strings.TrimSpace(body.Code) == "change_cursor_expired" {
		return provider.ErrChangeCursorExpired
	}
	if status == http.StatusConflict {
		return core.ErrConflict
	}
	if status == http.StatusNotFound {
		return core.ErrNotFound
	}
	if status != http.StatusUnprocessableEntity {
		return nil
	}
	switch strings.TrimSpace(body.Code) {
	case "pageindex_document_unsupported":
		return fmt.Errorf("%w: %s%s", core.ErrDocumentUnsupported, firstNonBlank(body.Message, "unsupported document type"), formattedField(body.Field))
	case "pageindex_invalid_request":
		return fmt.Errorf("%w: %s%s", core.ErrInvalid, firstNonBlank(body.Message, "invalid PageIndex request"), formattedField(body.Field))
	}
	if len(body.Detail) > 0 {
		return fmt.Errorf("%w: %s", core.ErrInvalid, pageIndexValidationMessage(body.Detail))
	}
	return core.ErrInvalid
}

func formattedField(field string) string {
	if strings.TrimSpace(field) == "" {
		return ""
	}
	return " (" + strings.TrimSpace(field) + ")"
}

func pageIndexValidationMessage(details []struct {
	Loc  []any  `json:"loc"`
	Msg  string `json:"msg"`
	Type string `json:"type"`
}) string {
	for _, detail := range details {
		if strings.TrimSpace(detail.Msg) != "" {
			if len(detail.Loc) > 0 {
				return strings.TrimSpace(detail.Msg) + " (" + strings.TrimSpace(fmt.Sprint(detail.Loc...)) + ")"
			}
			return strings.TrimSpace(detail.Msg)
		}
	}
	return "invalid PageIndex request"
}

type pageIndexFolder struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func isUnsafePageIndexFolderID(folderID string) bool {
	return strings.EqualFold(strings.TrimSpace(folderID), "root")
}

func (p *Provider) rejectUnsafeBaseFolder(base core.KnowledgeBase, operation string) error {
	folderID := strings.TrimSpace(base.ProviderResourceID)
	if folderID == "" {
		return fmt.Errorf("%w: pageindex folder id is required for %s", core.ErrInvalid, operation)
	}
	if isUnsafePageIndexFolderID(folderID) {
		return unsafePageIndexRootFolderError(operation)
	}
	return nil
}

func (p *Provider) getValidatedDocument(ctx context.Context, documentID string, folderID string, operation string) (pageIndexDocument, error) {
	var document pageIndexDocument
	if err := p.doJSON(ctx, http.MethodGet, "/doc/"+url.PathEscape(documentID), nil, &document); err != nil {
		return pageIndexDocument{}, err
	}
	if err := validatePageIndexDocument(document, folderID, documentID, operation); err != nil {
		return pageIndexDocument{}, err
	}
	return document, nil
}

func validatePageIndexDocument(document pageIndexDocument, folderID string, documentID string, operation string) error {
	if err := validatePageIndexResourceID(folderID, document.FolderID, "folder", operation); err != nil {
		return err
	}
	if documentID == "" {
		if strings.TrimSpace(document.ID) == "" {
			return fmt.Errorf("%w: pageindex document id missing for %s", core.ErrDependencyUnavailable, operation)
		}
		return nil
	}
	return validatePageIndexResourceID(documentID, document.ID, "document", operation)
}

func validatePageIndexJob(job pageIndexJob, folderID string, documentID string, operation string) error {
	if err := validatePageIndexResourceID(folderID, job.FolderID, "job folder", operation); err != nil {
		return err
	}
	return validatePageIndexResourceID(documentID, job.DocumentID, "job document", operation)
}

func validatePageIndexResourceID(expected string, actual string, resource string, operation string) error {
	expected = strings.TrimSpace(expected)
	actual = strings.TrimSpace(actual)
	if expected == "" || actual != expected {
		return fmt.Errorf(
			"%w: pageindex %s id mismatch for %s (expected %q, got %q)",
			core.ErrDependencyUnavailable,
			resource,
			operation,
			expected,
			actual,
		)
	}
	return nil
}

func unsafePageIndexRootFolderError(operation string) error {
	if operation = strings.TrimSpace(operation); operation != "" {
		return fmt.Errorf("%w: pageindex root folder id is not allowed for %s", core.ErrInvalid, operation)
	}
	return fmt.Errorf("%w: pageindex root folder id is not allowed", core.ErrInvalid)
}

func pageIndexFolderMetadata(base core.KnowledgeBase) map[string]any {
	metadata := map[string]any{}
	if id := strings.TrimSpace(string(base.ID)); id != "" {
		metadata["orrery_knowledge_base_id"] = id
	}
	if ownerType := strings.TrimSpace(string(base.OwnerType)); ownerType != "" {
		metadata["orrery_owner_type"] = ownerType
	}
	if ownerID := strings.TrimSpace(base.OwnerID); ownerID != "" {
		metadata["orrery_owner_id"] = ownerID
	}
	if createdBy := strings.TrimSpace(base.CreatedBy); createdBy != "" {
		metadata["orrery_created_by"] = createdBy
	}
	return metadata
}

type pageIndexDocument struct {
	ID           string         `json:"id"`
	FolderID     string         `json:"folder_id"`
	Filename     string         `json:"filename"`
	ContentType  string         `json:"content_type"`
	SizeBytes    int64          `json:"size_bytes"`
	Checksum     string         `json:"checksum"`
	Type         string         `json:"type"`
	Status       string         `json:"status"`
	ErrorMessage string         `json:"error_message"`
	PageCount    int            `json:"page_count"`
	LineCount    int            `json:"line_count"`
	DocName      string         `json:"doc_name"`
	Description  string         `json:"doc_description"`
	UploadPath   string         `json:"upload_path"`
	WorkspaceID  string         `json:"workspace_doc_id"`
	Metadata     map[string]any `json:"metadata"`
	JobID        string         `json:"job_id"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	IndexedAt    time.Time      `json:"indexed_at"`
}

type pageIndexStructureResponse struct {
	DocumentID string           `json:"document_id"`
	Structure  []map[string]any `json:"structure"`
}

type pageIndexPagesResponse struct {
	DocumentID string           `json:"document_id"`
	Pages      []map[string]any `json:"pages"`
}

type pageIndexJob struct {
	ID           string `json:"id"`
	FolderID     string `json:"folder_id"`
	DocumentID   string `json:"document_id"`
	Topic        string `json:"topic"`
	Status       string `json:"status"`
	Progress     int    `json:"progress"`
	Stage        string `json:"stage"`
	ErrorMessage string `json:"error_message"`
}

type pageIndexDocumentPreview struct {
	Document     pageIndexDocument `json:"document"`
	Job          *pageIndexJob     `json:"job"`
	Status       string            `json:"status"`
	Progress     int               `json:"progress"`
	Stage        string            `json:"stage"`
	ErrorMessage string            `json:"error_message"`
	Structure    []map[string]any  `json:"structure"`
	Pages        []map[string]any  `json:"pages"`
}

type pageIndexReindexResponse struct {
	FolderID string   `json:"folder_id"`
	Queued   int      `json:"queued"`
	JobIDs   []string `json:"job_ids"`
}

type pageIndexSearchRequest struct {
	Query          string `json:"query"`
	Limit          int    `json:"limit,omitempty"`
	IncludeContent bool   `json:"include_content"`
	RetrievalMode  string `json:"retrieval_mode,omitempty"`
}

type pageIndexSearchResponse struct {
	Results        []pageIndexSearchResult      `json:"results"`
	Items          []pageIndexSearchResult      `json:"items"`
	Chunks         []pageIndexSearchResult      `json:"chunks"`
	Data           *pageIndexSearchResponseData `json:"data"`
	Retrieval      pageIndexRetrievalInfo       `json:"retrieval"`
	RetrievalTrace pageIndexRetrievalTrace      `json:"retrieval_trace"`
}

type pageIndexRetrievalInfo struct {
	RequestedMode string `json:"requested_mode"`
	Mode          string `json:"mode"`
}

type pageIndexRetrievalTrace struct {
	FallbackReason string `json:"fallback_reason"`
}

func pageIndexSearchExecution(base core.KnowledgeBase, requested provider.RetrievalMode, response pageIndexSearchResponse) (provider.SearchExecution, error) {
	if value := strings.TrimSpace(response.Retrieval.RequestedMode); value != "" {
		normalized, err := provider.NormalizeRetrievalMode(value)
		if err != nil {
			return provider.SearchExecution{}, fmt.Errorf("%w: invalid PageIndex requested retrieval mode", core.ErrDependencyUnavailable)
		}
		requested = normalized
	}
	if requested == "" {
		requested = provider.RetrievalModeAgentic
	}
	actual := strings.TrimSpace(response.Retrieval.Mode)
	if actual == "" {
		actual = "agentic_tree_search"
	}
	if actual != "agentic_tree_search" && actual != "fast_structure_search" {
		return provider.SearchExecution{}, fmt.Errorf("%w: invalid PageIndex actual retrieval mode", core.ErrDependencyUnavailable)
	}
	return provider.SearchExecution{
		Provider: core.ProviderPageIndex, KnowledgeBaseID: string(base.ID),
		RequestedMode: requested, ActualMode: actual,
		FallbackReason: strings.TrimSpace(response.RetrievalTrace.FallbackReason),
	}, nil
}

type pageIndexSearchResponseData struct {
	Results []pageIndexSearchResult `json:"results"`
	Items   []pageIndexSearchResult `json:"items"`
	Chunks  []pageIndexSearchResult `json:"chunks"`
}

type pageIndexSearchResult struct {
	FolderID         string   `json:"folder_id"`
	DocumentID       string   `json:"document_id"`
	OrreryDocumentID string   `json:"orrery_document_id"`
	DocID            string   `json:"doc_id"`
	ID               string   `json:"id"`
	ChunkID          string   `json:"chunk_id"`
	NodeID           string   `json:"node_id"`
	Score            *float64 `json:"score"`
	SelectionOrder   int      `json:"selection_order"`
	Title            string   `json:"title"`
	Name             string   `json:"name"`
	Filename         string   `json:"filename"`
	Content          string   `json:"content"`
	Snippet          string   `json:"snippet"`
	Text             string   `json:"text"`
	HeadingPath      []string `json:"heading_path"`
	Pages            any      `json:"pages"`
	StartIndex       int      `json:"start_index"`
	EndIndex         int      `json:"end_index"`
}

func (r pageIndexSearchResponse) allResults() []pageIndexSearchResult {
	if len(r.Results) > 0 {
		return r.Results
	}
	if len(r.Items) > 0 {
		return r.Items
	}
	if len(r.Chunks) > 0 {
		return r.Chunks
	}
	if r.Data == nil {
		return nil
	}
	if len(r.Data.Results) > 0 {
		return r.Data.Results
	}
	if len(r.Data.Items) > 0 {
		return r.Data.Items
	}
	return r.Data.Chunks
}

func mapPageIndexSearchResults(base core.KnowledgeBase, folderID string, raw []pageIndexSearchResult) []provider.SearchResult {
	out := make([]provider.SearchResult, 0, len(raw))
	for index, item := range raw {
		if strings.TrimSpace(item.FolderID) != strings.TrimSpace(folderID) {
			continue
		}
		docID := firstNonBlank(item.OrreryDocumentID, item.DocumentID, item.DocID, item.ID, item.NodeID)
		if docID == "" {
			continue
		}
		out = append(out, provider.SearchResult{
			KnowledgeBaseID: string(base.ID),
			DocumentID:      docID,
			ChunkID:         firstNonBlank(item.ChunkID, item.NodeID, "pageindex:"+strconv.Itoa(index)),
			Score:           pageIndexSearchScore(item),
			Snippet:         firstNonBlank(item.Snippet, item.Content, item.Text),
			HeadingPath:     append([]string(nil), item.HeadingPath...),
			DocumentName:    firstNonBlank(item.Title, item.Name, item.Filename, docID),
		})
	}
	return out
}

func pageIndexSearchScore(item pageIndexSearchResult) float32 {
	if item.Score != nil {
		return float32(*item.Score)
	}
	if item.SelectionOrder > 0 {
		return 1 / float32(item.SelectionOrder)
	}
	return 0
}

func pageIndexUploadMetadata(out pageIndexDocument) map[string]string {
	metadata := pageIndexDocumentMetadata(out)
	metadata["pageindex_status"] = strings.TrimSpace(out.Status)
	if jobID := strings.TrimSpace(out.JobID); jobID != "" {
		metadata["pageindex_job_id"] = jobID
		metadata["pageindex_job_progress"] = "0"
		if status := strings.TrimSpace(out.Status); status != "" {
			metadata["pageindex_upload_status"] = status
		}
	}
	return metadata
}

func pageIndexDocumentMetadata(out pageIndexDocument) map[string]string {
	metadata := map[string]string{}
	addMetadataString(metadata, "pageindex_folder_id", out.FolderID)
	addMetadataString(metadata, "pageindex_upload_path", out.UploadPath)
	addMetadataString(metadata, "pageindex_workspace_doc_id", out.WorkspaceID)
	addMetadataString(metadata, "pageindex_doc_name", out.DocName)
	addMetadataString(metadata, "pageindex_doc_description", out.Description)
	if out.PageCount > 0 {
		metadata["pageindex_page_count"] = strconv.Itoa(out.PageCount)
	}
	if out.LineCount > 0 {
		metadata["pageindex_line_count"] = strconv.Itoa(out.LineCount)
	}
	return metadata
}

func addMetadataString(metadata map[string]string, key string, value string) {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		metadata[key] = trimmed
	}
}

func (r pageIndexReindexResponse) firstJobID() string {
	for _, id := range r.JobIDs {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func firstPositiveInt64(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func pageIndexDocumentID(doc core.KnowledgeDocument) string {
	return firstNonBlank(doc.ProviderDocumentID, doc.ArtifactID, string(doc.ID))
}

func documentStatusFromPageIndex(raw string, fallback core.DocumentStatus) core.DocumentStatus {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		if fallback != "" {
			return fallback
		}
		return core.DocumentParsing
	}
	switch normalized {
	case "indexed", "completed", "complete", "done", "success", "succeeded":
		return core.DocumentIndexed
	case "failed", "error":
		return core.DocumentFailed
	case "deleting", "deleted":
		return core.DocumentDeleting
	case "embedding":
		return core.DocumentEmbedding
	case "chunking":
		return core.DocumentChunking
	case "queued", "processing", "running", "parsing", "pending":
		return core.DocumentParsing
	default:
		if fallback != "" {
			return fallback
		}
		return core.DocumentUploaded
	}
}

func documentStatusFromJob(job pageIndexJob, fallback core.DocumentStatus) core.DocumentStatus {
	switch strings.ToLower(strings.TrimSpace(job.Status)) {
	case "completed":
		return core.DocumentIndexed
	case "failed", "cancelled":
		return core.DocumentFailed
	case "queued", "running":
		return core.DocumentParsing
	}
	return documentStatusFromPageIndex(job.Stage, fallback)
}

func mapPageIndexJob(job pageIndexJob) provider.DocumentProcessingJob {
	return provider.DocumentProcessingJob{
		JobID:        strings.TrimSpace(job.ID),
		DocumentID:   strings.TrimSpace(job.DocumentID),
		Topic:        strings.TrimSpace(job.Topic),
		Status:       strings.TrimSpace(job.Status),
		Progress:     job.Progress,
		Stage:        strings.TrimSpace(job.Stage),
		ErrorMessage: strings.TrimSpace(job.ErrorMessage),
	}
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

func cloneAnyMaps(src []map[string]any) []map[string]any {
	if len(src) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(src))
	for _, item := range src {
		next := make(map[string]any, len(item))
		for key, value := range item {
			next[key] = value
		}
		out = append(out, next)
	}
	return out
}

func normalizeStructureNodes(items []map[string]any) []provider.DocumentStructureNode {
	if len(items) == 0 {
		return nil
	}
	out := make([]provider.DocumentStructureNode, 0, len(items))
	for _, item := range items {
		if node, ok := normalizeStructureNode(item, nil); ok {
			out = append(out, node)
		}
	}
	return out
}

func normalizeStructureNode(item map[string]any, parentPath []string) (provider.DocumentStructureNode, bool) {
	nodeID, _ := firstStringFromAnyMap(item, "provider_node_id", "providerNodeID", "node_id", "nodeId", "id")
	text, _ := firstStringFromAnyMap(item, "title", "text", "heading", "name")
	pageNumber, _ := firstNumberFromAnyMap(item, "page", "page_number", "pageNumber", "page_index", "pageIndex", "number", "index")
	structurePath := firstStringSliceFromAnyMap(item, "structure_path", "structurePath", "tree_path", "treePath", "path")
	if len(structurePath) == 0 {
		structurePath = append([]string(nil), parentPath...)
		if text != "" {
			structurePath = append(structurePath, text)
		}
	}
	headingPath := firstStringSliceFromAnyMap(item, "heading_path", "headingPath", "headings")
	if len(headingPath) == 0 && text != "" {
		headingPath = []string{text}
	}
	bbox := firstObjectFromAnyMap(item, "bbox", "bounding_box", "boundingBox")
	children := normalizeChildStructureNodes(item, structurePath)
	if nodeID == "" && text == "" && pageNumber <= 0 && len(children) == 0 {
		return provider.DocumentStructureNode{}, false
	}
	return provider.DocumentStructureNode{
		ProviderNodeID: nodeID,
		Text:           text,
		PageNumber:     pageNumber,
		HeadingPath:    headingPath,
		StructurePath:  structurePath,
		BBox:           bbox,
		Children:       children,
	}, true
}

func normalizeChildStructureNodes(item map[string]any, parentPath []string) []provider.DocumentStructureNode {
	for _, key := range []string{"nodes", "children", "items"} {
		raw, ok := item[key].([]any)
		if !ok || len(raw) == 0 {
			continue
		}
		out := make([]provider.DocumentStructureNode, 0, len(raw))
		for _, child := range raw {
			childMap, ok := child.(map[string]any)
			if !ok {
				continue
			}
			if node, ok := normalizeStructureNode(childMap, parentPath); ok {
				out = append(out, node)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func normalizePagePreviews(items []map[string]any) []provider.DocumentPagePreview {
	if len(items) == 0 {
		return nil
	}
	out := make([]provider.DocumentPagePreview, 0, len(items))
	for index, item := range items {
		pageNumber, _ := firstNumberFromAnyMap(item, "page", "page_number", "pageNumber", "page_index", "pageIndex", "number", "index")
		if pageNumber <= 0 {
			pageNumber = index + 1
		}
		nodeID, _ := firstStringFromAnyMap(item, "provider_node_id", "providerNodeID", "node_id", "nodeId", "id")
		text, _ := firstStringFromAnyMap(item, "text", "content", "snippet")
		imageRef, _ := firstStringFromAnyMap(item, "image_ref", "imageRef", "page_image_uri", "pageImageURI", "image_uri", "imageURI")
		contentHash, _ := firstStringFromAnyMap(item, "content_hash", "contentHash")
		snippetHash, _ := firstStringFromAnyMap(item, "snippet_hash", "snippetHash")
		if snippetHash == "" && text != "" {
			snippetHash = "sha256:" + sha256Text(text)
		}
		out = append(out, provider.DocumentPagePreview{
			PageNumber:     pageNumber,
			ProviderNodeID: nodeID,
			HeadingPath:    firstStringSliceFromAnyMap(item, "heading_path", "headingPath", "headings"),
			StructurePath:  firstStringSliceFromAnyMap(item, "structure_path", "structurePath", "tree_path", "treePath", "path"),
			BBox:           firstObjectFromAnyMap(item, "bbox", "bounding_box", "boundingBox"),
			ImageRef:       imageRef,
			Text:           text,
			ContentHash:    contentHash,
			SnippetHash:    snippetHash,
			Raw:            cloneAnyMap(item),
		})
	}
	return out
}

func firstStringFromAnyMap(values map[string]any, keys ...string) (string, bool) {
	for _, key := range keys {
		text, ok := values[key].(string)
		if !ok {
			continue
		}
		text = strings.TrimSpace(text)
		if text != "" {
			return text, true
		}
	}
	return "", false
}

func firstNumberFromAnyMap(values map[string]any, keys ...string) (int, bool) {
	for _, key := range keys {
		switch typed := values[key].(type) {
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
			if err == nil {
				return int(floatValue), true
			}
		case string:
			parsed, err := strconv.Atoi(strings.TrimSpace(typed))
			if err == nil {
				return parsed, true
			}
		}
	}
	return 0, false
}

func firstStringSliceFromAnyMap(values map[string]any, keys ...string) []string {
	for _, key := range keys {
		switch typed := values[key].(type) {
		case []string:
			if len(typed) > 0 {
				return append([]string(nil), typed...)
			}
		case []any:
			out := make([]string, 0, len(typed))
			for _, item := range typed {
				if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
					out = append(out, strings.TrimSpace(text))
				}
			}
			if len(out) > 0 {
				return out
			}
		case string:
			if text := strings.TrimSpace(typed); text != "" {
				return []string{text}
			}
		}
	}
	return nil
}

func firstObjectFromAnyMap(values map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if object, ok := values[key].(map[string]any); ok && len(object) > 0 {
			return cloneAnyMap(object)
		}
	}
	return nil
}

func cloneAnyMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]any, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

func sha256Text(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func cloneMetadata(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	out := make(map[string]string, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}
