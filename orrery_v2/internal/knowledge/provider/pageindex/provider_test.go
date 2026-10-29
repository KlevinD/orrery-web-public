package pageindex_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/provider"
	"orrery_v2/internal/knowledge/provider/pageindex"
)

func TestProviderCreatesFolderAndUploadsDocument(t *testing.T) {
	var seenAuth string
	var seenRequestID string
	var seenActor string
	var sawFolder bool
	var sawUpload bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		seenRequestID = r.Header.Get("X-Request-ID")
		seenActor = r.Header.Get("X-Orrery-Actor")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/folder/":
			sawFolder = true
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode folder body: %v", err)
			}
			if body["name"] != "Product Docs" {
				t.Fatalf("folder name = %v", body["name"])
			}
			_, _ = io.WriteString(w, `{"id":"folder-123","name":"Product Docs"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/doc/":
			sawUpload = true
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatalf("parse upload multipart: %v", err)
			}
			if got := r.FormValue("folder_id"); got != "folder-123" {
				t.Fatalf("folder_id = %q, want folder-123", got)
			}
			var metadata map[string]string
			if err := json.Unmarshal([]byte(r.FormValue("metadata")), &metadata); err != nil {
				t.Fatalf("decode upload metadata: %v", err)
			}
			if metadata["orrery_document_id"] != "kdoc-1" ||
				metadata["orrery_knowledge_base_id"] != "kb-1" ||
				metadata["orrery_owner_type"] != "organization" ||
				metadata["orrery_owner_id"] != "org-1" ||
				metadata["orrery_created_by"] != "operator-1" {
				t.Fatalf("metadata = %#v", metadata)
			}
			if got := r.FormValue("async"); got != "true" {
				t.Fatalf("async = %q, want true", got)
			}
			file, header, err := r.FormFile("file")
			if err != nil {
				t.Fatalf("missing file: %v", err)
			}
			defer file.Close()
			data, _ := io.ReadAll(file)
			if header.Filename != "handbook.md" || string(data) != "hello pageindex" {
				t.Fatalf("uploaded file = %q %q", header.Filename, string(data))
			}
			_, _ = io.WriteString(w, `{"id":"doc-456","folder_id":"folder-123","filename":"handbook.md","status":"indexed"}`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	base := core.KnowledgeBase{
		ID:        "kb-1",
		Name:      "Product Docs",
		OwnerType: core.OwnerOrganization,
		OwnerID:   "org-1",
		Provider:  core.ProviderPageIndex,
	}
	ctx := provider.WithRequestMetadata(context.Background(), provider.RequestMetadata{
		RequestID: "corr-knowledge-1",
		Actor:     "human:profile-operator",
	})
	ensured, err := p.EnsureKnowledgeBase(ctx, provider.EnsureKnowledgeBaseRequest{Base: base})
	if err != nil {
		t.Fatalf("EnsureKnowledgeBase: %v", err)
	}
	if ensured.ProviderResourceID != "folder-123" {
		t.Fatalf("ProviderResourceID = %q", ensured.ProviderResourceID)
	}
	base.ProviderResourceID = ensured.ProviderResourceID
	uploaded, err := p.UploadDocument(ctx, provider.UploadDocumentRequest{
		Base:        base,
		DocumentID:  "kdoc-1",
		Filename:    "handbook.md",
		ContentType: "text/markdown",
		Reader:      strings.NewReader("hello pageindex"),
		CreatedBy:   "operator-1",
	})
	if err != nil {
		t.Fatalf("UploadDocument: %v", err)
	}
	if uploaded.ProviderDocumentID != "doc-456" || uploaded.Status != core.DocumentIndexed || uploaded.Filename != "handbook.md" {
		t.Fatalf("uploaded result = %+v", uploaded)
	}
	if seenAuth != "Bearer pi-test" || !sawFolder || !sawUpload {
		t.Fatalf("auth/folder/upload not observed: auth=%q folder=%v upload=%v", seenAuth, sawFolder, sawUpload)
	}
	if seenRequestID != "corr-knowledge-1" || seenActor != "human:profile-operator" {
		t.Fatalf("audit headers = request_id=%q actor=%q", seenRequestID, seenActor)
	}
}

func TestProviderImplementsRevisionProviderContracts(t *testing.T) {
	var sawUpdate bool
	var sawPublish bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/doc/doc-1/content":
			sawUpdate = true
			_, _ = io.WriteString(w, `{"id":"rev-2","document_id":"doc-1","folder_id":"folder-1","revision":2,"object_key":"document-revisions/doc-1/2.md","content_hash":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","content_type":"text/markdown","size_bytes":12,"created_by":"user-1","created_at":"2026-07-24T01:00:00Z","publication_status":"draft","published_by":""}`)
		case r.Method == http.MethodGet && r.URL.Path == "/doc/doc-1/revisions":
			_, _ = io.WriteString(w, `[{"id":"rev-2","document_id":"doc-1","folder_id":"folder-1","revision":2,"content_hash":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","content_type":"text/markdown","size_bytes":12,"created_by":"user-1","created_at":"2026-07-24T01:00:00Z","publication_status":"draft"}]`)
		case r.Method == http.MethodGet && r.URL.Path == "/doc/doc-1/semantic-source":
			if r.URL.Query().Get("revision") != "2" {
				t.Fatalf("revision query = %q", r.URL.Query().Get("revision"))
			}
			_, _ = io.WriteString(w, `{"document_id":"doc-1","revision":2,"content_hash":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","content_type":"text/markdown","content":"# Strategy\n","editable":true}`)
		case r.Method == http.MethodPut && r.URL.Path == "/doc/doc-1/revisions/2/publication":
			sawPublish = true
			_, _ = io.WriteString(w, `{"id":"rev-2","document_id":"doc-1","folder_id":"folder-1","revision":2,"content_hash":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","content_type":"text/markdown","size_bytes":12,"created_by":"user-1","created_at":"2026-07-24T01:00:00Z","publication_status":"published","published_by":"approver-1","published_at":"2026-07-24T02:00:00Z"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/folder/folder-1/changes":
			if r.URL.Query().Get("cursor") != "7" || r.URL.Query().Get("limit") != "20" {
				t.Fatalf("change query = %q", r.URL.RawQuery)
			}
			_, _ = io.WriteString(w, `{"items":[{"sequence":8,"folder_id":"folder-1","document_id":"doc-1","revision":2,"event_type":"document.published","content_hash":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","occurred_at":"2026-07-24T02:00:00Z"}],"next_cursor":"8"}`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	var revisionProvider provider.RevisionProvider = p
	ctx := context.Background()
	updated, err := revisionProvider.UpdateDocumentContent(ctx, provider.UpdateDocumentContentRequest{
		DocumentID: "doc-1", ExpectedRevision: 1, Content: "# Strategy\n", ContentType: "text/markdown", CreatedBy: "user-1",
	})
	if err != nil || updated.Revision != 2 {
		t.Fatalf("UpdateDocumentContent = %+v, %v", updated, err)
	}
	revisions, err := revisionProvider.ListDocumentRevisions(ctx, provider.DocumentRevisionsRequest{DocumentID: "doc-1"})
	if err != nil || len(revisions) != 1 || revisions[0].Revision != 2 {
		t.Fatalf("ListDocumentRevisions = %+v, %v", revisions, err)
	}
	source, err := revisionProvider.GetSemanticSource(ctx, provider.SemanticSourceRequest{DocumentID: "doc-1", Revision: 2})
	if err != nil || source.Content != "# Strategy\n" || !source.Editable {
		t.Fatalf("GetSemanticSource = %+v, %v", source, err)
	}
	published, err := revisionProvider.PublishDocumentRevision(ctx, provider.PublishDocumentRevisionRequest{DocumentID: "doc-1", Revision: 2, PublishedBy: "approver-1"})
	if err != nil || published.PublicationStatus != "published" {
		t.Fatalf("PublishDocumentRevision = %+v, %v", published, err)
	}
	changes, err := revisionProvider.ListKnowledgeChanges(ctx, provider.KnowledgeChangesRequest{FolderID: "folder-1", Cursor: "7", Limit: 20})
	if err != nil || changes.NextCursor != "8" || len(changes.Items) != 1 || changes.Items[0].Revision != 2 {
		t.Fatalf("ListKnowledgeChanges = %+v, %v", changes, err)
	}
	if !sawUpdate || !sawPublish {
		t.Fatalf("update/publish not observed: %v/%v", sawUpdate, sawPublish)
	}
}

func TestProviderMapsExpiredKnowledgeChangeCursor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
		_, _ = io.WriteString(w, `{"code":"change_cursor_expired","message":"expired","metadata":{"earliest_cursor":"9"}}`)
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	_, err := p.ListKnowledgeChanges(context.Background(), provider.KnowledgeChangesRequest{FolderID: "folder-1", Cursor: "5"})
	if !errors.Is(err, provider.ErrChangeCursorExpired) {
		t.Fatalf("ListKnowledgeChanges error = %v", err)
	}
}

func TestProviderListsDocumentsInFolder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/folder/folder-1/documents" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer pi-test" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"id":"doc-1","folder_id":"folder-1","filename":"plan.md","content_type":"text/markdown","size_bytes":42,"checksum":"sha256:abc","doc_name":"Plan","status":"indexed","metadata":{"knowledge_key":"04-Resources/Plan/Orrery/plan.md","source":"digital-brain://04-Resources/Plan/Orrery/plan.md"}},{"id":"doc-failed","folder_id":"folder-1","filename":"broken.pdf","status":"failed"}]`)
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	documents, err := p.ListFolderDocuments(context.Background(), provider.FolderDocumentsRequest{FolderID: "folder-1"})
	if err != nil {
		t.Fatalf("ListFolderDocuments: %v", err)
	}
	if len(documents) != 1 || documents[0].DocumentID != "doc-1" || documents[0].FolderID != "folder-1" || documents[0].Status != "indexed" {
		t.Fatalf("documents = %+v", documents)
	}
	if documents[0].Filename != "plan.md" || documents[0].ContentType != "text/markdown" || documents[0].SizeBytes != 42 || documents[0].Checksum != "sha256:abc" || documents[0].Title != "Plan" {
		t.Fatalf("document metadata = %+v", documents[0])
	}
	if documents[0].KnowledgeKey != "04-Resources/Plan/Orrery/plan.md" || documents[0].Source != "digital-brain://04-Resources/Plan/Orrery/plan.md" {
		t.Fatalf("document path metadata = %+v", documents[0])
	}
	inventory, err := p.ListFolderDocumentInventory(context.Background(), provider.FolderDocumentsRequest{FolderID: "folder-1"})
	if err != nil || len(inventory) != 2 || inventory[1].DocumentID != "doc-failed" || inventory[1].Status == "indexed" {
		t.Fatalf("complete inventory lost a temporarily unindexed source: %+v %v", inventory, err)
	}
}

func TestProviderBuildsReadOnlySemanticSourceForLegacyIndexedDocument(t *testing.T) {
	now := "2026-08-21T08:00:00Z"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/doc/doc-1":
			_, _ = io.WriteString(w, `{"id":"doc-1","folder_id":"folder-1","filename":"plan.md","status":"indexed","page_count":2,"created_at":"`+now+`","updated_at":"`+now+`","indexed_at":"`+now+`"}`)
		case "/doc/doc-1/structure":
			_, _ = io.WriteString(w, `{"document_id":"doc-1","structure":[{"id":"node-1","title":"Orrery plan"}]}`)
		case "/doc/doc-1/pages":
			if r.URL.Query().Get("pages") != "1-2" {
				t.Fatalf("pages query = %q", r.URL.RawQuery)
			}
			_, _ = io.WriteString(w, `{"document_id":"doc-1","pages":[{"page":1,"text":"PageIndex navigation"}]}`)
		default:
			t.Fatalf("unexpected request %s", r.URL.String())
		}
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	snapshot, err := p.GetIndexedDocumentSource(context.Background(), provider.IndexedDocumentSourceRequest{DocumentID: "doc-1"})
	if err != nil {
		t.Fatalf("GetIndexedDocumentSource: %v", err)
	}
	if snapshot.Revision.Revision != 1 || snapshot.Revision.PublicationStatus != "published" || snapshot.Revision.ContentHash == "" {
		t.Fatalf("revision = %+v", snapshot.Revision)
	}
	if snapshot.Source.ContentHash != snapshot.Revision.ContentHash || !strings.Contains(snapshot.Source.Content, "PageIndex navigation") {
		t.Fatalf("source = %+v", snapshot.Source)
	}
}

func TestProviderRejectsOversizedPageIndexResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/folder/" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"id":"folder-123","name":"`+strings.Repeat("x", 128)+`"}`)
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{
		APIURL:           server.URL,
		APIKey:           "pi-test",
		MaxResponseBytes: 32,
	})
	_, err := p.EnsureKnowledgeBase(context.Background(), provider.EnsureKnowledgeBaseRequest{
		Base: core.KnowledgeBase{ID: "kb-1", Name: "Docs", Provider: core.ProviderPageIndex},
	})
	if !errors.Is(err, core.ErrDependencyUnavailable) || !strings.Contains(err.Error(), "response exceeds 32 bytes") {
		t.Fatalf("EnsureKnowledgeBase error = %v", err)
	}
}

func TestProviderCreatesPageIndexFolderWithOrreryMetadata(t *testing.T) {
	var sawFolder bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/folder/" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		sawFolder = true
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode folder body: %v", err)
		}
		metadata, ok := body["metadata"].(map[string]any)
		if !ok {
			t.Fatalf("metadata = %#v, want object", body["metadata"])
		}
		if metadata["orrery_knowledge_base_id"] != "kb-org-1" ||
			metadata["orrery_owner_type"] != "organization" ||
			metadata["orrery_owner_id"] != "org-1" ||
			metadata["orrery_created_by"] != "operator-1" {
			t.Fatalf("folder metadata = %#v", metadata)
		}
		_, _ = io.WriteString(w, `{"id":"folder-org-1","name":"Org Docs"}`)
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	ensured, err := p.EnsureKnowledgeBase(context.Background(), provider.EnsureKnowledgeBaseRequest{
		Base: core.KnowledgeBase{
			ID:        "kb-org-1",
			Name:      "Org Docs",
			OwnerType: core.OwnerOrganization,
			OwnerID:   "org-1",
			Provider:  core.ProviderPageIndex,
			CreatedBy: "operator-1",
		},
	})
	if err != nil {
		t.Fatalf("EnsureKnowledgeBase: %v", err)
	}
	if !sawFolder || ensured.ProviderResourceID != "folder-org-1" {
		t.Fatalf("folder creation not observed: saw=%v ensured=%+v", sawFolder, ensured)
	}
}

func TestProviderReplacesUnsafeRootFolderBinding(t *testing.T) {
	var sawFolder bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/folder/" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		sawFolder = true
		_, _ = io.WriteString(w, `{"id":"folder-user-1","name":"User Docs"}`)
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	ensured, err := p.EnsureKnowledgeBase(context.Background(), provider.EnsureKnowledgeBaseRequest{
		Base: core.KnowledgeBase{
			ID:                 "kb-user-1",
			Name:               "User Docs",
			OwnerType:          core.OwnerHuman,
			OwnerID:            "user-1",
			Provider:           core.ProviderPageIndex,
			ProviderResourceID: "root",
		},
	})
	if err != nil {
		t.Fatalf("EnsureKnowledgeBase: %v", err)
	}
	if !sawFolder {
		t.Fatal("expected provider to create an isolated folder instead of reusing root")
	}
	if ensured.ProviderResourceID == "root" {
		t.Fatalf("ProviderResourceID = %q, want isolated folder id", ensured.ProviderResourceID)
	}
}

func TestProviderRequiresAPIKeyForSelfHostedPageIndex(t *testing.T) {
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		t.Fatalf("request should not reach PageIndex without an API key")
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL})
	_, err := p.EnsureKnowledgeBase(context.Background(), provider.EnsureKnowledgeBaseRequest{
		Base: core.KnowledgeBase{ID: "kb-local", Name: "Local Docs", Provider: core.ProviderPageIndex},
	})
	if err == nil || !strings.Contains(err.Error(), "pageindex api key is required") {
		t.Fatalf("EnsureKnowledgeBase error = %v", err)
	}
	if called {
		t.Fatal("unexpected PageIndex request")
	}
}

func TestProviderRequiresResolvedAPIKeyWhenReferenceIsConfigured(t *testing.T) {
	p := pageindex.New(pageindex.Config{APIURL: "http://127.0.0.1:1", APIKeyRef: "secret://knowledge/pageindex"})
	_, err := p.EnsureKnowledgeBase(context.Background(), provider.EnsureKnowledgeBaseRequest{
		Base: core.KnowledgeBase{ID: "kb-local", Name: "Local Docs", Provider: core.ProviderPageIndex},
	})
	if err == nil || !strings.Contains(err.Error(), "pageindex api key is required") {
		t.Fatalf("EnsureKnowledgeBase error = %v", err)
	}
}

func TestProviderStoresPageIndexUploadJobID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/doc/" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("parse upload multipart: %v", err)
		}
		_, _ = io.WriteString(w, `{
			"id":"doc-456",
			"folder_id":"folder-123",
			"filename":"handbook.md",
			"content_type":"text/markdown",
			"size_bytes":15,
			"checksum":"abc123",
			"status":"queued",
			"job_id":"job-upload-123"
		}`)
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	uploaded, err := p.UploadDocument(context.Background(), provider.UploadDocumentRequest{
		Base:        core.KnowledgeBase{ID: "kb-1", Provider: core.ProviderPageIndex, ProviderResourceID: "folder-123"},
		Filename:    "handbook.md",
		ContentType: "text/markdown",
		Reader:      strings.NewReader("hello pageindex"),
	})
	if err != nil {
		t.Fatalf("UploadDocument: %v", err)
	}
	if uploaded.ProviderMetadata["pageindex_job_id"] != "job-upload-123" {
		t.Fatalf("provider metadata = %#v, want pageindex upload job id", uploaded.ProviderMetadata)
	}
}

func TestProviderStoresPageIndexUploadMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/doc/" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("parse upload multipart: %v", err)
		}
		_, _ = io.WriteString(w, `{
			"id":"doc-456",
			"folder_id":"folder-123",
			"filename":"handbook.pdf",
			"content_type":"application/pdf",
			"size_bytes":15,
			"checksum":"abc123",
			"status":"queued",
			"job_id":"job-upload-123"
		}`)
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	uploaded, err := p.UploadDocument(context.Background(), provider.UploadDocumentRequest{
		Base:        core.KnowledgeBase{ID: "kb-1", Provider: core.ProviderPageIndex, ProviderResourceID: "folder-123"},
		Filename:    "handbook.pdf",
		ContentType: "application/pdf",
		Reader:      strings.NewReader("hello pageindex"),
	})
	if err != nil {
		t.Fatalf("UploadDocument: %v", err)
	}
	metadata := uploaded.ProviderMetadata
	if metadata["pageindex_folder_id"] != "folder-123" ||
		metadata["pageindex_status"] != "queued" ||
		metadata["pageindex_upload_status"] != "queued" ||
		metadata["pageindex_job_id"] != "job-upload-123" ||
		metadata["pageindex_job_progress"] != "0" {
		t.Fatalf("provider metadata = %#v", metadata)
	}
}

func TestProviderRejectsUploadResponseForDifferentFolder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/doc/" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"id":"doc-456","folder_id":"folder-other","filename":"handbook.md"}`)
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	_, err := p.UploadDocument(context.Background(), provider.UploadDocumentRequest{
		Base:     core.KnowledgeBase{ID: "kb-1", Provider: core.ProviderPageIndex, ProviderResourceID: "folder-123"},
		Filename: "handbook.md",
		Reader:   strings.NewReader("hello pageindex"),
	})
	if !errors.Is(err, core.ErrDependencyUnavailable) || !strings.Contains(err.Error(), "folder id mismatch") {
		t.Fatalf("UploadDocument error = %v", err)
	}
}

func TestProviderTreatsAsyncUploadWithoutStatusAsProcessing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/doc/" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("parse upload multipart: %v", err)
		}
		if got := r.FormValue("async"); got != "true" {
			t.Fatalf("async = %q, want true", got)
		}
		_, _ = io.WriteString(w, `{"id":"doc-queued","folder_id":"folder-123","filename":"handbook.md"}`)
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	uploaded, err := p.UploadDocument(context.Background(), provider.UploadDocumentRequest{
		Base:     core.KnowledgeBase{ID: "kb-1", Provider: core.ProviderPageIndex, ProviderResourceID: "folder-123"},
		Filename: "handbook.md",
		Reader:   strings.NewReader("hello pageindex"),
	})
	if err != nil {
		t.Fatalf("UploadDocument: %v", err)
	}
	if uploaded.Status != core.DocumentParsing {
		t.Fatalf("uploaded status = %q, want %q", uploaded.Status, core.DocumentParsing)
	}
}

func TestProviderRejectsUploadWithoutPageIndexFolderID(t *testing.T) {
	var sawUpload bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/doc/" {
			sawUpload = true
			t.Fatal("upload request should not reach PageIndex without folder_id")
		}
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	_, err := p.UploadDocument(context.Background(), provider.UploadDocumentRequest{
		Base:     core.KnowledgeBase{ID: "kb-1", Provider: core.ProviderPageIndex},
		Filename: "handbook.md",
		Reader:   strings.NewReader("hello pageindex"),
	})
	if err == nil || !strings.Contains(err.Error(), "pageindex folder_id is required") {
		t.Fatalf("expected missing folder_id error, got %v", err)
	}
	if sawUpload {
		t.Fatal("unexpected PageIndex upload request")
	}
}

func TestProviderRejectsUploadWithRootFolderID(t *testing.T) {
	var sawUpload bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawUpload = true
		t.Fatal("upload request should not reach PageIndex with root folder id")
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	_, err := p.UploadDocument(context.Background(), provider.UploadDocumentRequest{
		Base:     core.KnowledgeBase{ID: "kb-1", Provider: core.ProviderPageIndex, ProviderResourceID: "root"},
		Filename: "handbook.md",
		Reader:   strings.NewReader("hello pageindex"),
	})
	if err == nil || !strings.Contains(err.Error(), "root folder id is not allowed") {
		t.Fatalf("expected root folder id error, got %v", err)
	}
	if sawUpload {
		t.Fatal("unexpected PageIndex upload request")
	}
}

func TestProviderMapsPageIndexUnsupportedDocumentError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/doc/" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"code":"pageindex_document_unsupported","message":"Unsupported document type","field":"file"}`)
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	_, err := p.UploadDocument(context.Background(), provider.UploadDocumentRequest{
		Base:     core.KnowledgeBase{ID: "kb-1", Provider: core.ProviderPageIndex, ProviderResourceID: "folder-123"},
		Filename: "notes.txt",
		Reader:   strings.NewReader("hello pageindex"),
	})
	if !errors.Is(err, core.ErrDocumentUnsupported) {
		t.Fatalf("expected ErrDocumentUnsupported, got %v", err)
	}
	if !strings.Contains(err.Error(), "Unsupported document type") || !strings.Contains(err.Error(), "file") {
		t.Fatalf("expected PageIndex message and field in error, got %v", err)
	}
}

func TestProviderRefreshesDocumentStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		switch r.URL.Path {
		case "/doc/doc-456":
			_, _ = io.WriteString(w, `{"id":"doc-456","folder_id":"folder-123","filename":"handbook.md","status":"queued"}`)
		case "/doc/doc-456/job":
			_, _ = io.WriteString(w, `{
			"id":"job-789",
			"folder_id":"folder-123",
			"document_id":"doc-456",
			"topic":"document.index",
			"status":"running",
			"progress":30,
			"stage":"processing",
			"error_message":"",
			"created_at":"2026-06-16T10:00:00Z",
			"updated_at":"2026-06-16T10:01:00Z"
		}`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	result, err := p.RefreshDocumentStatus(context.Background(), provider.DocumentStatusRequest{
		Base:     core.KnowledgeBase{ID: "kb-1", Provider: core.ProviderPageIndex, ProviderResourceID: "folder-123"},
		Document: core.KnowledgeDocument{ID: "kdoc-1", ProviderDocumentID: "doc-456", Filename: "handbook.md"},
	})
	if err != nil {
		t.Fatalf("RefreshDocumentStatus: %v", err)
	}
	if result.ProviderDocumentID != "doc-456" || result.Status != core.DocumentParsing || result.Job == nil || result.Job.JobID != "job-789" || result.Job.Progress != 30 {
		t.Fatalf("status result = %+v", result)
	}
}

func TestProviderRefreshStatusStoresCurrentPageIndexDocumentMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		switch r.URL.Path {
		case "/doc/doc-456":
			_, _ = io.WriteString(w, `{
			"id":"doc-456",
			"folder_id":"folder-123",
			"filename":"handbook.pdf",
			"content_type":"application/pdf",
			"size_bytes":15,
			"checksum":"abc123",
			"status":"indexed",
			"upload_path":"uploads/folder-123/doc-456.pdf",
			"workspace_doc_id":"workspace_doc_1",
			"page_count":7,
			"line_count":42,
			"doc_name":"Release Handbook",
			"doc_description":"Release process"
		}`)
		case "/doc/doc-456/job":
			_, _ = io.WriteString(w, `{"id":"job-789","folder_id":"folder-123","document_id":"doc-456"}`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	result, err := p.RefreshDocumentStatus(context.Background(), provider.DocumentStatusRequest{
		Base: core.KnowledgeBase{ID: "kb-1", Provider: core.ProviderPageIndex, ProviderResourceID: "folder-123"},
		Document: core.KnowledgeDocument{
			ID:                 "kdoc-1",
			ProviderDocumentID: "doc-456",
			Filename:           "handbook.pdf",
			Status:             core.DocumentParsing,
			ProviderMetadata:   map[string]string{"existing": "keep"},
		},
	})
	if err != nil {
		t.Fatalf("RefreshDocumentStatus: %v", err)
	}
	metadata := result.ProviderMetadata
	if metadata["existing"] != "keep" ||
		metadata["pageindex_upload_path"] != "uploads/folder-123/doc-456.pdf" ||
		metadata["pageindex_workspace_doc_id"] != "workspace_doc_1" ||
		metadata["pageindex_page_count"] != "7" ||
		metadata["pageindex_line_count"] != "42" ||
		metadata["pageindex_doc_name"] != "Release Handbook" ||
		metadata["pageindex_doc_description"] != "Release process" {
		t.Fatalf("provider metadata = %#v", metadata)
	}
	if result.Status != core.DocumentIndexed || result.ContentType != "application/pdf" || result.SizeBytes != 15 {
		t.Fatalf("status result = %+v", result)
	}
}

func TestProviderRejectsRefreshStatusWithRootFolderID(t *testing.T) {
	var sawRefresh bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRefresh = true
		t.Fatal("refresh request should not reach PageIndex with root folder id")
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	_, err := p.RefreshDocumentStatus(context.Background(), provider.DocumentStatusRequest{
		Base:     core.KnowledgeBase{ID: "kb-1", Provider: core.ProviderPageIndex, ProviderResourceID: "root"},
		Document: core.KnowledgeDocument{ID: "kdoc-1", ProviderDocumentID: "doc-456", Filename: "handbook.md"},
	})
	if err == nil || !strings.Contains(err.Error(), "root folder id is not allowed") {
		t.Fatalf("expected root folder id error, got %v", err)
	}
	if sawRefresh {
		t.Fatal("unexpected PageIndex refresh request")
	}
}

func TestProviderRejectsStatusResponseForDifferentFolder(t *testing.T) {
	var requestedJob bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/doc/doc-456":
			_, _ = io.WriteString(w, `{"id":"doc-456","folder_id":"folder-other"}`)
		case "/doc/doc-456/job":
			requestedJob = true
			t.Fatal("job must not be requested after a document folder mismatch")
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	_, err := p.RefreshDocumentStatus(context.Background(), provider.DocumentStatusRequest{
		Base:     core.KnowledgeBase{ID: "kb-1", Provider: core.ProviderPageIndex, ProviderResourceID: "folder-123"},
		Document: core.KnowledgeDocument{ID: "kdoc-1", ProviderDocumentID: "doc-456"},
	})
	if !errors.Is(err, core.ErrDependencyUnavailable) || !strings.Contains(err.Error(), "folder id mismatch") {
		t.Fatalf("RefreshDocumentStatus error = %v", err)
	}
	if requestedJob {
		t.Fatal("unexpected PageIndex job request")
	}
}

func TestProviderKeepsExistingStatusWhenRefreshOmitsStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		switch r.URL.Path {
		case "/doc/doc-456":
			_, _ = io.WriteString(w, `{"id":"doc-456","folder_id":"folder-123","filename":"handbook.md"}`)
		case "/doc/doc-456/job":
			_, _ = io.WriteString(w, `{"id":"job-789","folder_id":"folder-123","document_id":"doc-456"}`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	result, err := p.RefreshDocumentStatus(context.Background(), provider.DocumentStatusRequest{
		Base:     core.KnowledgeBase{ID: "kb-1", Provider: core.ProviderPageIndex, ProviderResourceID: "folder-123"},
		Document: core.KnowledgeDocument{ID: "kdoc-1", ProviderDocumentID: "doc-456", Filename: "handbook.md", Status: core.DocumentIndexed},
	})
	if err != nil {
		t.Fatalf("RefreshDocumentStatus: %v", err)
	}
	if result.Status != core.DocumentIndexed {
		t.Fatalf("status = %q, want %q", result.Status, core.DocumentIndexed)
	}
}

func TestProviderRetriesDocumentProcessing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/doc/doc-456" {
			_, _ = io.WriteString(w, `{"id":"doc-456","folder_id":"folder-123"}`)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/doc/doc-456/retry" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{
			"id":"job-789",
			"folder_id":"folder-123",
			"document_id":"doc-456",
			"topic":"document.retry",
			"status":"queued",
			"progress":0,
			"stage":"queued",
			"created_at":"2026-06-16T10:00:00Z",
			"updated_at":"2026-06-16T10:00:00Z"
		}`)
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	result, err := p.ReprocessDocument(context.Background(), provider.ReprocessDocumentRequest{
		Base:     core.KnowledgeBase{ID: "kb-1", Provider: core.ProviderPageIndex, ProviderResourceID: "folder-123"},
		Document: core.KnowledgeDocument{ID: "kdoc-1", ProviderDocumentID: "doc-456", Filename: "handbook.md"},
	})
	if err != nil {
		t.Fatalf("ReprocessDocument: %v", err)
	}
	if result.Status != core.DocumentParsing || result.Job.JobID != "job-789" || result.Job.Progress != 0 {
		t.Fatalf("reprocess result = %+v", result)
	}
}

func TestProviderRejectsReprocessWithRootFolderID(t *testing.T) {
	var sawReprocess bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawReprocess = true
		t.Fatal("reprocess request should not reach PageIndex with root folder id")
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	_, err := p.ReprocessDocument(context.Background(), provider.ReprocessDocumentRequest{
		Base:     core.KnowledgeBase{ID: "kb-1", Provider: core.ProviderPageIndex, ProviderResourceID: "root"},
		Document: core.KnowledgeDocument{ID: "kdoc-1", ProviderDocumentID: "doc-456", Filename: "handbook.md"},
	})
	if err == nil || !strings.Contains(err.Error(), "root folder id is not allowed") {
		t.Fatalf("expected root folder id error, got %v", err)
	}
	if sawReprocess {
		t.Fatal("unexpected PageIndex reprocess request")
	}
}

func TestProviderReindexesPageIndexFolder(t *testing.T) {
	var sawReindex bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/folder/folder-123/reindex" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		sawReindex = true
		_, _ = io.WriteString(w, `{
			"folder_id":"folder-123",
			"queued":2,
			"job_ids":["job-reindex-123","job-reindex-456"]
		}`)
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	result, err := p.ReindexKnowledgeBase(context.Background(), provider.ReindexKnowledgeBaseRequest{
		Base: core.KnowledgeBase{ID: "kb-1", Provider: core.ProviderPageIndex, ProviderResourceID: "folder-123"},
	})
	if err != nil {
		t.Fatalf("ReindexKnowledgeBase: %v", err)
	}
	if !sawReindex || !result.Supported || !result.Queued || result.LocalIngestJob || result.JobID != "job-reindex-123" {
		t.Fatalf("reindex result = %+v saw=%v", result, sawReindex)
	}
}

func TestProviderTreatsEmptyPageIndexReindexAsNoop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/folder/folder-123/reindex" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"folder_id":"folder-123","queued":0,"job_ids":[]}`)
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	result, err := p.ReindexKnowledgeBase(context.Background(), provider.ReindexKnowledgeBaseRequest{
		Base: core.KnowledgeBase{ID: "kb-1", Provider: core.ProviderPageIndex, ProviderResourceID: "folder-123"},
	})
	if err != nil {
		t.Fatalf("ReindexKnowledgeBase: %v", err)
	}
	if !result.Supported || result.Queued || result.LocalIngestJob || result.JobID != "" {
		t.Fatalf("noop reindex result = %+v", result)
	}
}

func TestProviderRejectsReindexWithRootFolderID(t *testing.T) {
	var sawReindex bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawReindex = true
		t.Fatal("reindex request should not reach PageIndex with root folder id")
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	_, err := p.ReindexKnowledgeBase(context.Background(), provider.ReindexKnowledgeBaseRequest{
		Base: core.KnowledgeBase{ID: "kb-1", Provider: core.ProviderPageIndex, ProviderResourceID: "root"},
	})
	if err == nil || !strings.Contains(err.Error(), "root folder id is not allowed") {
		t.Fatalf("expected root folder id error, got %v", err)
	}
	if sawReindex {
		t.Fatal("unexpected PageIndex reindex request")
	}
}

func TestProviderGetsDocumentProcessingPreview(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/doc/doc-456/preview" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("pages"); got != "1" {
			t.Fatalf("pages query = %q, want 1", got)
		}
		_, _ = io.WriteString(w, `{
			"document":{
				"id":"doc-456",
				"folder_id":"folder-123",
				"filename":"handbook.md",
				"status":"indexed",
				"created_at":"2026-06-16T10:00:00Z",
				"updated_at":"2026-06-16T10:02:00Z"
			},
			"job":{
				"id":"job-789",
				"folder_id":"folder-123",
				"document_id":"doc-456",
				"topic":"document.retry",
				"status":"completed",
				"progress":100,
				"stage":"indexed",
				"created_at":"2026-06-16T10:00:00Z",
				"updated_at":"2026-06-16T10:02:00Z"
			},
			"status":"indexed",
			"progress":100,
			"stage":"indexed",
			"structure":[{"title":"Overview","level":1}],
			"pages":[{"page":1,"text":"release process"}]
		}`)
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	result, err := p.GetDocumentProcessingResult(context.Background(), provider.DocumentProcessingResultRequest{
		Base:     core.KnowledgeBase{ID: "kb-1", Provider: core.ProviderPageIndex, ProviderResourceID: "folder-123"},
		Document: core.KnowledgeDocument{ID: "kdoc-1", ProviderDocumentID: "doc-456", Filename: "handbook.md"},
		Pages:    "1",
	})
	if err != nil {
		t.Fatalf("GetDocumentProcessingResult: %v", err)
	}
	if result.Status != core.DocumentIndexed || result.Progress != 100 || len(result.Pages) != 1 || len(result.Structure) != 1 {
		t.Fatalf("processing result = %+v", result)
	}
}

func TestProviderRejectsPreviewResponseForDifferentFolder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/doc/doc-456/preview" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"document":{"id":"doc-456","folder_id":"folder-other"},"pages":[{"page":1,"text":"private"}]}`)
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	_, err := p.GetDocumentProcessingResult(context.Background(), provider.DocumentProcessingResultRequest{
		Base:     core.KnowledgeBase{ID: "kb-1", Provider: core.ProviderPageIndex, ProviderResourceID: "folder-123"},
		Document: core.KnowledgeDocument{ID: "kdoc-1", ProviderDocumentID: "doc-456"},
		Pages:    "1",
	})
	if !errors.Is(err, core.ErrDependencyUnavailable) || !strings.Contains(err.Error(), "folder id mismatch") {
		t.Fatalf("GetDocumentProcessingResult error = %v", err)
	}
}

func TestProviderNormalizesProcessingResultStructureAndPages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/doc/doc-456/preview" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{
			"document":{"id":"doc-456","folder_id":"folder-123","filename":"handbook.md","status":"indexed"},
			"status":"indexed",
			"progress":100,
			"stage":"indexed",
			"structure":[{
				"node_id":"root-1",
				"text":"Release Handbook",
				"page_index":1,
				"nodes":[{
					"node_id":"node-7",
					"text":"Gray release checklist",
					"page_index":2,
					"bbox":{"x":0.1,"y":0.2,"width":0.3,"height":0.4}
				}]
			}],
			"pages":[{
				"page_index":2,
				"text":"Gray release checklist",
				"node_id":"node-7",
				"path":["Release Handbook","Gray release checklist"],
				"bbox":{"x":0.1,"y":0.2,"width":0.3,"height":0.4},
				"page_image_uri":"pageindex://folder-123/doc-456#page=2&image=preview"
			}]
		}`)
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	result, err := p.GetDocumentProcessingResult(context.Background(), provider.DocumentProcessingResultRequest{
		Base:     core.KnowledgeBase{ID: "kb-1", Provider: core.ProviderPageIndex, ProviderResourceID: "folder-123"},
		Document: core.KnowledgeDocument{ID: "kdoc-1", ProviderDocumentID: "doc-456", Filename: "handbook.md"},
		Pages:    "2",
	})
	if err != nil {
		t.Fatalf("GetDocumentProcessingResult: %v", err)
	}
	if len(result.StructureNodes) != 1 || result.StructureNodes[0].ProviderNodeID != "root-1" {
		t.Fatalf("structure nodes = %#v", result.StructureNodes)
	}
	child := result.StructureNodes[0].Children[0]
	if child.ProviderNodeID != "node-7" || child.PageNumber != 2 || child.StructurePath[0] != "Release Handbook" || child.StructurePath[1] != "Gray release checklist" {
		t.Fatalf("child node = %#v", child)
	}
	if len(result.PagePreviews) != 1 {
		t.Fatalf("page previews = %#v", result.PagePreviews)
	}
	page := result.PagePreviews[0]
	if page.PageNumber != 2 ||
		page.ProviderNodeID != "node-7" ||
		page.Text != "Gray release checklist" ||
		page.ImageRef != "pageindex://folder-123/doc-456#page=2&image=preview" ||
		page.SnippetHash == "" {
		t.Fatalf("normalized page = %#v", page)
	}
	if page.BBox["x"] != float64(0.1) || page.StructurePath[0] != "Release Handbook" {
		t.Fatalf("normalized page metadata = %#v", page)
	}
}

func TestProviderRejectsDocumentProcessingPreviewWithRootFolderID(t *testing.T) {
	var sawPreview bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPreview = true
		t.Fatal("preview request should not reach PageIndex with root folder id")
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	_, err := p.GetDocumentProcessingResult(context.Background(), provider.DocumentProcessingResultRequest{
		Base:     core.KnowledgeBase{ID: "kb-1", Provider: core.ProviderPageIndex, ProviderResourceID: "root"},
		Document: core.KnowledgeDocument{ID: "kdoc-1", ProviderDocumentID: "doc-456", Filename: "handbook.md"},
	})
	if err == nil || !strings.Contains(err.Error(), "root folder id is not allowed") {
		t.Fatalf("expected root folder id error, got %v", err)
	}
	if sawPreview {
		t.Fatal("unexpected PageIndex preview request")
	}
}

func TestProviderDeletesKnowledgeBaseFolder(t *testing.T) {
	var seenAuth string
	var sawDelete bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		if r.Method != http.MethodDelete || r.URL.Path != "/folder/folder-123" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		sawDelete = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	err := p.DeleteKnowledgeBase(context.Background(), provider.DeleteKnowledgeBaseRequest{
		Base: core.KnowledgeBase{ID: "kb-1", Provider: core.ProviderPageIndex, ProviderResourceID: "folder-123"},
	})
	if err != nil {
		t.Fatalf("DeleteKnowledgeBase: %v", err)
	}
	if seenAuth != "Bearer pi-test" || !sawDelete {
		t.Fatalf("delete not observed: auth=%q delete=%v", seenAuth, sawDelete)
	}
}

func TestProviderRejectsDeleteKnowledgeBaseWithoutFolderID(t *testing.T) {
	var sawDelete bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawDelete = true
		t.Fatal("delete request should not reach PageIndex without folder id")
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	err := p.DeleteKnowledgeBase(context.Background(), provider.DeleteKnowledgeBaseRequest{
		Base: core.KnowledgeBase{ID: "kb-1", Provider: core.ProviderPageIndex},
	})
	if err == nil || !strings.Contains(err.Error(), "pageindex folder id is required") {
		t.Fatalf("expected missing folder id error, got %v", err)
	}
	if sawDelete {
		t.Fatal("unexpected PageIndex delete request")
	}
}

func TestProviderRejectsDeleteKnowledgeBaseWithRootFolderID(t *testing.T) {
	var sawDelete bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawDelete = true
		t.Fatal("delete request should not reach PageIndex with root folder id")
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	err := p.DeleteKnowledgeBase(context.Background(), provider.DeleteKnowledgeBaseRequest{
		Base: core.KnowledgeBase{ID: "kb-1", Provider: core.ProviderPageIndex, ProviderResourceID: "root"},
	})
	if err == nil || !strings.Contains(err.Error(), "root folder id is not allowed") {
		t.Fatalf("expected root folder id error, got %v", err)
	}
	if sawDelete {
		t.Fatal("unexpected PageIndex delete request")
	}
}

func TestProviderDeletesDocument(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/doc/doc-456" {
			_, _ = io.WriteString(w, `{"id":"doc-456","folder_id":"folder-123"}`)
			return
		}
		if r.Method != http.MethodDelete || r.URL.Path != "/doc/doc-456" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	err := p.DeleteDocument(context.Background(), provider.DeleteDocumentRequest{
		Base:     core.KnowledgeBase{ID: "kb-1", Provider: core.ProviderPageIndex, ProviderResourceID: "folder-123"},
		Document: core.KnowledgeDocument{ID: "kdoc-1", KnowledgeBaseID: "kb-1", ProviderDocumentID: "doc-456", Filename: "handbook.md", Status: core.DocumentIndexed},
	})
	if err != nil {
		t.Fatalf("DeleteDocument: %v", err)
	}
}

func TestProviderDoesNotDeleteDocumentFromDifferentFolder(t *testing.T) {
	var sawDelete bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/doc/doc-456" {
			_, _ = io.WriteString(w, `{"id":"doc-456","folder_id":"folder-other"}`)
			return
		}
		if r.Method == http.MethodDelete {
			sawDelete = true
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	err := p.DeleteDocument(context.Background(), provider.DeleteDocumentRequest{
		Base:     core.KnowledgeBase{ID: "kb-1", Provider: core.ProviderPageIndex, ProviderResourceID: "folder-123"},
		Document: core.KnowledgeDocument{ID: "kdoc-1", ProviderDocumentID: "doc-456"},
	})
	if !errors.Is(err, core.ErrDependencyUnavailable) || !strings.Contains(err.Error(), "folder id mismatch") {
		t.Fatalf("DeleteDocument error = %v", err)
	}
	if sawDelete {
		t.Fatal("unexpected PageIndex delete request")
	}
}

func TestProviderRejectsDeleteDocumentWithRootFolderID(t *testing.T) {
	var sawDelete bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawDelete = true
		t.Fatal("document delete request should not reach PageIndex with root folder id")
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	err := p.DeleteDocument(context.Background(), provider.DeleteDocumentRequest{
		Base:     core.KnowledgeBase{ID: "kb-1", Provider: core.ProviderPageIndex, ProviderResourceID: "root"},
		Document: core.KnowledgeDocument{ID: "kdoc-1", ProviderDocumentID: "doc-456", Filename: "handbook.md"},
	})
	if err == nil || !strings.Contains(err.Error(), "root folder id is not allowed") {
		t.Fatalf("expected root folder id error, got %v", err)
	}
	if sawDelete {
		t.Fatal("unexpected PageIndex document delete request")
	}
}

func TestProviderSearchesPageIndexFolderHTTP(t *testing.T) {
	var seenAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		if r.Method != http.MethodPost || r.URL.Path != "/folder/folder-123/search" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode search body: %v", err)
		}
		if body["query"] != "release" || int(body["limit"].(float64)) != 3 || body["include_content"] != true {
			t.Fatalf("search body = %+v", body)
		}
		_, _ = io.WriteString(w, `{
			"results":[{
				"folder_id":"folder-123",
				"document_id":"doc-456",
				"orrery_document_id":"kdoc-1",
				"chunk_id":"chunk-1",
				"score":0.75,
				"title":"handbook.md",
				"filename":"handbook.md",
				"content":"release process",
				"snippet":"release process",
				"heading_path":["Runbooks","Release"]
			}]
		}`)
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})

	page, err := p.Search(context.Background(), provider.SearchRequest{
		Query: "release",
		Bases: []core.KnowledgeBase{{ID: "kb-1", Provider: core.ProviderPageIndex, ProviderResourceID: "folder-123"}},
		Limit: 3,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if seenAuth != "Bearer pi-test" {
		t.Fatalf("authorization = %q", seenAuth)
	}
	if len(page.Results) != 1 ||
		page.Results[0].KnowledgeBaseID != "kb-1" ||
		page.Results[0].DocumentID != "kdoc-1" ||
		page.Results[0].ChunkID != "chunk-1" ||
		page.Results[0].DocumentName != "handbook.md" ||
		page.Results[0].Snippet != "release process" ||
		len(page.Results[0].HeadingPath) != 2 {
		t.Fatalf("search page = %+v", page)
	}
}

func TestProviderFiltersSearchResultsFromDifferentFolders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/folder/folder-123/search" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"results":[
			{"folder_id":"folder-123","orrery_document_id":"kdoc-allowed","chunk_id":"allowed"},
			{"folder_id":"folder-other","orrery_document_id":"kdoc-other","chunk_id":"other"}
		]}`)
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	page, err := p.Search(context.Background(), provider.SearchRequest{
		Query: "release",
		Bases: []core.KnowledgeBase{{ID: "kb-1", Provider: core.ProviderPageIndex, ProviderResourceID: "folder-123"}},
		Limit: 3,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(page.Results) != 1 || page.Results[0].DocumentID != "kdoc-allowed" {
		t.Fatalf("search page = %+v", page)
	}
}

func TestProviderSearchSendsModeAndReturnsExecutionReceipt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["retrieval_mode"] != "agentic" {
			t.Fatalf("search body = %+v", body)
		}
		_, _ = io.WriteString(w, `{
			"results":[],
			"retrieval":{"requested_mode":"agentic","mode":"fast_structure_search"},
			"retrieval_trace":{"fallback_reason":"agentic_timeout_fast_fallback"}
		}`)
	}))
	defer server.Close()
	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})

	page, err := p.Search(context.Background(), provider.SearchRequest{
		Query: "release", RetrievalMode: provider.RetrievalModeAgentic,
		Bases: []core.KnowledgeBase{{
			ID: "kb-1", Provider: core.ProviderPageIndex,
			ProviderResourceID: "folder-1",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Executions) != 1 {
		t.Fatalf("executions = %+v", page.Executions)
	}
	receipt := page.Executions[0]
	if receipt.Provider != core.ProviderPageIndex || receipt.KnowledgeBaseID != "kb-1" ||
		receipt.RequestedMode != provider.RetrievalModeAgentic ||
		receipt.ActualMode != "fast_structure_search" ||
		receipt.FallbackReason != "agentic_timeout_fast_fallback" {
		t.Fatalf("execution receipt = %+v", receipt)
	}
}

func TestProviderRejectsSearchWithRootFolderID(t *testing.T) {
	var sawSearch bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawSearch = true
		t.Fatal("search request should not reach PageIndex with root folder id")
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	_, err := p.Search(context.Background(), provider.SearchRequest{
		Query: "release",
		Bases: []core.KnowledgeBase{{ID: "kb-1", Provider: core.ProviderPageIndex, ProviderResourceID: "root"}},
		Limit: 3,
	})
	if err == nil || !strings.Contains(err.Error(), "root folder id is not allowed") {
		t.Fatalf("expected root folder id error, got %v", err)
	}
	if sawSearch {
		t.Fatal("unexpected PageIndex search request")
	}
}

func TestProviderSearchMapsPageIndexResponseAliases(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/folder/folder-123/search" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{
			"data":{
					"items":[{
						"folder_id":"folder-123",
						"doc_id":"doc-from-alias",
					"id":"node-from-alias",
					"score":0.5,
					"name":"aliased.md",
					"text":"aliased search text"
				}]
			}
		}`)
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	page, err := p.Search(context.Background(), provider.SearchRequest{
		Query: "release",
		Bases: []core.KnowledgeBase{{ID: "kb-1", Provider: core.ProviderPageIndex, ProviderResourceID: "folder-123"}},
		Limit: 1,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(page.Results) != 1 ||
		page.Results[0].DocumentID != "doc-from-alias" ||
		page.Results[0].DocumentName != "aliased.md" ||
		page.Results[0].Snippet != "aliased search text" {
		t.Fatalf("aliased search page = %+v", page)
	}
}

func TestProviderSearchPreservesSelectionOrderWhenScoresAreMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/folder/folder-123/search" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{
			"results":[
				{"folder_id":"folder-123","document_id":"doc-rank-1","chunk_id":"chunk-z","score":null,"selection_order":1},
				{"folder_id":"folder-123","document_id":"doc-rank-2","chunk_id":"chunk-y","selection_order":2},
				{"folder_id":"folder-123","document_id":"doc-explicit-zero","chunk_id":"chunk-x","score":0,"selection_order":1},
				{"folder_id":"folder-123","document_id":"doc-unranked","chunk_id":"chunk-w"}
			]
		}`)
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	page, err := p.Search(context.Background(), provider.SearchRequest{
		Query: "Bain AI组织与运营模型 20260528",
		Bases: []core.KnowledgeBase{{
			ID: "kb-1", Provider: core.ProviderPageIndex,
			ProviderResourceID: "folder-123",
		}},
		Limit: 8,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(page.Results) != 4 {
		t.Fatalf("results = %+v", page.Results)
	}
	scores := map[string]float32{}
	for _, result := range page.Results {
		scores[result.DocumentID] = result.Score
	}
	if scores["doc-rank-1"] != 1 || scores["doc-rank-2"] != 0.5 {
		t.Fatalf("selection scores = %+v", scores)
	}
	if scores["doc-explicit-zero"] != 0 || scores["doc-unranked"] != 0 {
		t.Fatalf("fallback scores = %+v", scores)
	}
}

func TestProviderSearchesPageIndexFoldersInParallelWithoutProviderLimitTruncation(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		var folderID string
		switch r.URL.Path {
		case "/folder/folder-a/search":
			folderID = "folder-a"
		case "/folder/folder-b/search":
			folderID = "folder-b"
		default:
			t.Fatalf("unexpected request path %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode search body: %v", err)
		}
		if int(body["limit"].(float64)) != 1 {
			t.Fatalf("search limit = %v, want 1", body["limit"])
		}
		started <- folderID
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, `{
			"results":[{
				"folder_id":"`+folderID+`",
				"document_id":"doc-`+folderID+`",
				"chunk_id":"chunk-`+folderID+`",
				"score":0.75,
				"filename":"`+folderID+`.md",
				"snippet":"release `+folderID+`"
			}]
		}`)
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	done := make(chan struct {
		page provider.SearchResultPage
		err  error
	}, 1)
	go func() {
		page, err := p.Search(context.Background(), provider.SearchRequest{
			Query: "release",
			Bases: []core.KnowledgeBase{
				{ID: "kb-a", Provider: core.ProviderPageIndex, ProviderResourceID: "folder-a"},
				{ID: "kb-b", Provider: core.ProviderPageIndex, ProviderResourceID: "folder-b"},
			},
			Limit: 1,
		})
		done <- struct {
			page provider.SearchResultPage
			err  error
		}{page: page, err: err}
	}()

	seen := map[string]bool{}
	timeout := time.After(2 * time.Second)
	for len(seen) < 2 {
		select {
		case folderID := <-started:
			seen[folderID] = true
		case result := <-done:
			t.Fatalf("search completed before both folder requests started: page=%+v err=%v", result.page, result.err)
		case <-timeout:
			t.Fatalf("timed out waiting for parallel folder searches, saw %#v", seen)
		}
	}
	close(release)

	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("Search: %v", result.err)
		}
		if len(result.page.Results) != 2 {
			t.Fatalf("provider should return candidates from all folders before rerank truncation, got %+v", result.page.Results)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for search result")
	}
}

func TestProviderSearchReturnsCompletedFolderResultsWhenAnotherFolderFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/folder/folder-good/search":
			_, _ = io.WriteString(w, `{"results":[{"folder_id":"folder-good","document_id":"doc-good","chunk_id":"chunk-good","score":0.75,"filename":"good.md","snippet":"release"}]}`)
		case "/folder/folder-bad/search":
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"code":"pageindex_unavailable","message":"unavailable"}`)
		default:
			t.Fatalf("unexpected request path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	p := pageindex.New(pageindex.Config{APIURL: server.URL, APIKey: "pi-test"})
	page, err := p.Search(context.Background(), provider.SearchRequest{
		Query: "release",
		Bases: []core.KnowledgeBase{
			{ID: "kb-good", Provider: core.ProviderPageIndex, ProviderResourceID: "folder-good"},
			{ID: "kb-bad", Provider: core.ProviderPageIndex, ProviderResourceID: "folder-bad"},
		},
		Limit: 3,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(page.Results) != 1 || page.Results[0].KnowledgeBaseID != "kb-good" || page.Results[0].DocumentID != "doc-good" {
		t.Fatalf("partial search page = %+v", page)
	}
}

func TestProviderRejectsUploadWhenDisabled(t *testing.T) {
	p := pageindex.New(pageindex.Config{Disabled: true, APIURL: "https://api.pageindex.ai", APIKey: "pi-test"})
	_, err := p.UploadDocument(context.Background(), provider.UploadDocumentRequest{
		Base:     core.KnowledgeBase{ID: "kb-1", Provider: core.ProviderPageIndex},
		Filename: "handbook.md",
		Reader:   bytes.NewReader([]byte("body")),
	})
	if err == nil {
		t.Fatal("expected disabled provider error")
	}
}
