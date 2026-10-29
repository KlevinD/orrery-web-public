package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/memoryadmin"
)

func TestHTTPRoutesMemoryAdminCandidatesAndRecords(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	service := &fakeMemoryAdminService{
		candidatePage: memoryadmin.MemoryCandidatePageDTO{
			Candidates: []memoryadmin.MemoryCandidateDTO{{CandidateID: "candidate-1", ProposedLayer: "fact", Summary: "Candidate one.", Status: "proposed"}},
			Total:      1,
			Limit:      5,
		},
		record:       memoryadmin.MemoryRecordDTO{MemoryID: "memory-1", CandidateID: "candidate-1", Layer: "fact", Summary: "Accepted.", Sensitivity: "normal", CreatedAt: now, UpdatedAt: now},
		recordPage:   memoryadmin.MemoryRecordPageDTO{Records: []memoryadmin.MemoryRecordDTO{{MemoryID: "memory-1", CandidateID: "candidate-1", Layer: "fact", Summary: "Accepted.", CreatedAt: now, UpdatedAt: now}}, Total: 1, Limit: 5},
		receipt:      memoryadmin.CommandReceiptDTO{Status: "rejected", CandidateID: "candidate-1", UpdatedAt: now},
		mergeReceipt: memoryadmin.CommandReceiptDTO{Status: "merged", CandidateID: "candidate-1", TargetID: "memory-existing", UpdatedAt: now},
		lifeReceipt:  memoryadmin.CommandReceiptDTO{Status: "archived", MemoryID: "memory-1", UpdatedAt: now},
		sweepResult:  memoryadmin.MemoryRetentionSweepResultDTO{Status: "swept", CheckedCount: 5, ArchivedCount: 1, ArchivedMemoryIDs: []string{"memory-old"}, UpdatedAt: now},
	}
	handler := newTestRouter(RouterOptions{MemoryQuery: service, MemoryCommand: service})

	rec := httptest.NewRecorder()
	list := authedMemoryRequest(http.MethodGet, "/api/memory/candidates?mission_id=mission-1&status=proposed&review_required=true&limit=5", "")
	handler.ServeHTTP(rec, list)
	if rec.Code != http.StatusOK {
		t.Fatalf("list candidates status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.lastCandidateQuery.MissionID != "mission-1" || service.lastCandidateQuery.Status != "proposed" || service.lastCandidateQuery.ReviewRequired == nil || !*service.lastCandidateQuery.ReviewRequired {
		t.Fatalf("candidate query = %+v", service.lastCandidateQuery)
	}

	rec = httptest.NewRecorder()
	create := authedMemoryRequest(http.MethodPost, "/api/memory/candidates", `{"candidate_id":"manual-1","source_type":"summary","source_ref":"manual-source","proposed_kind":"mission","proposed_layer":"fact","proposed_scope":{"kind":"mission","id":"mission-1"},"summary":"Manual memory."}`)
	create.Header.Set("Idempotency-Key", "idem-memory-create")
	handler.ServeHTTP(rec, create)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create candidate status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.createReq.CandidateID != "manual-1" || service.lastCommandViewer.IdempotencyKey != "idem-memory-create" {
		t.Fatalf("create req/viewer = %+v / %+v", service.createReq, service.lastCommandViewer)
	}

	rec = httptest.NewRecorder()
	accept := authedMemoryRequest(http.MethodPost, "/api/memory/candidates/candidate-1/accept", `{"profile_id":"profile-1","memory_id":"memory-1","reason":"approved"}`)
	handler.ServeHTTP(rec, accept)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("accept status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.acceptID != "candidate-1" || service.acceptReq.ProfileID != "profile-1" {
		t.Fatalf("accept = %q %+v", service.acceptID, service.acceptReq)
	}

	rec = httptest.NewRecorder()
	reject := authedMemoryRequest(http.MethodPost, "/api/memory/candidates/candidate-1/reject", `{"reason":"duplicate"}`)
	handler.ServeHTTP(rec, reject)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("reject status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.rejectID != "candidate-1" || service.rejectReq.Reason != "duplicate" {
		t.Fatalf("reject = %q %+v", service.rejectID, service.rejectReq)
	}

	rec = httptest.NewRecorder()
	merge := authedMemoryRequest(http.MethodPost, "/api/memory/candidates/candidate-1/merge", `{"target_memory_id":"memory-existing","reason":"same fact"}`)
	handler.ServeHTTP(rec, merge)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("merge status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.mergeID != "candidate-1" || service.mergeReq.TargetMemoryID != "memory-existing" {
		t.Fatalf("merge = %q %+v", service.mergeID, service.mergeReq)
	}

	rec = httptest.NewRecorder()
	records := authedMemoryRequest(http.MethodGet, "/api/memory/records?profile_id=profile-1&layer=fact&limit=5", "")
	handler.ServeHTTP(rec, records)
	if rec.Code != http.StatusOK {
		t.Fatalf("list records status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.lastRecordQuery.ProfileID != "profile-1" || service.lastRecordQuery.Layer != "fact" {
		t.Fatalf("record query = %+v", service.lastRecordQuery)
	}

	rec = httptest.NewRecorder()
	detail := authedMemoryRequest(http.MethodGet, "/api/memory/records/memory-1", "")
	handler.ServeHTTP(rec, detail)
	if rec.Code != http.StatusOK {
		t.Fatalf("get record status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.getRecordID != "memory-1" {
		t.Fatalf("get record id = %q", service.getRecordID)
	}
	var body memoryadmin.MemoryRecordDTO
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode record: %v", err)
	}
	if body.MemoryID != "memory-1" {
		t.Fatalf("record body = %+v", body)
	}

	rec = httptest.NewRecorder()
	touch := authedMemoryRequest(http.MethodPost, "/api/memory/records/touch", `{"memory_ids":["memory-1"],"reason":"context injected"}`)
	handler.ServeHTTP(rec, touch)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("touch status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(service.touchReq.MemoryIDs) != 1 || service.touchReq.MemoryIDs[0] != "memory-1" {
		t.Fatalf("touch req = %+v", service.touchReq)
	}

	rec = httptest.NewRecorder()
	sweep := authedMemoryRequest(http.MethodPost, "/api/memory/records/sweep-retention", `{"limit":50,"reason":"nightly"}`)
	handler.ServeHTTP(rec, sweep)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("sweep status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.sweepReq.Limit != 50 || service.sweepReq.Reason != "nightly" {
		t.Fatalf("sweep req = %+v", service.sweepReq)
	}

	rec = httptest.NewRecorder()
	archive := authedMemoryRequest(http.MethodPost, "/api/memory/records/memory-1/archive", `{"reason":"superseded"}`)
	handler.ServeHTTP(rec, archive)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("archive status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.archiveID != "memory-1" || service.archiveReq.Reason != "superseded" {
		t.Fatalf("archive = %q %+v", service.archiveID, service.archiveReq)
	}

	rec = httptest.NewRecorder()
	anonymize := authedMemoryRequest(http.MethodPost, "/api/memory/records/memory-1/anonymize", `{"reason":"remove identity"}`)
	handler.ServeHTTP(rec, anonymize)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("anonymize status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.anonymizeID != "memory-1" || service.anonymizeReq.Reason != "remove identity" {
		t.Fatalf("anonymize = %q %+v", service.anonymizeID, service.anonymizeReq)
	}

	rec = httptest.NewRecorder()
	deleteReq := authedMemoryRequest(http.MethodPost, "/api/memory/records/memory-1/delete", `{"reason":"retention expired"}`)
	handler.ServeHTTP(rec, deleteReq)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("delete status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.deleteID != "memory-1" || service.deleteReq.Reason != "retention expired" {
		t.Fatalf("delete = %q %+v", service.deleteID, service.deleteReq)
	}
}

func TestHTTPRoutesMemoryAdminMissingServiceReturnsDependencyError(t *testing.T) {
	handler := newTestRouter(RouterOptions{})
	rec := httptest.NewRecorder()
	req := authedMemoryRequest(http.MethodGet, "/api/memory/candidates", "")

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var body ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body.Code != ErrorMemoryDependencyUnavailable {
		t.Fatalf("error = %+v", body)
	}
}

func TestMemoryViewerFromRequestCapturesIdentityScopes(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/memory/records", nil)
	req.Header.Set("X-User-ID", "operator")
	req.Header.Set("X-Agent-ID", "agent-1")
	req.Header.Set("X-Organization-ID", "org-1")
	req.Header.Set("X-Organization-IDs", "org-2, org-3")
	req.Header.Set("X-Department-ID", "dept-1")
	req.Header.Set("X-Department-IDs", "dept-2")
	req.Header.Set("X-Project-ID", "project-1")
	req.Header.Set("X-Project-IDs", "project-2")
	req.Header.Set("X-Mission-ID", "mission-1")
	req.Header.Set("X-Mission-IDs", "mission-2")
	req.Header.Set("X-Discussion-ID", "discussion-1")
	req.Header.Set("X-Discussion-IDs", "discussion-2")
	req.Header.Set("X-Permission-Scope", "memory.read")
	authorizeInternalViewerTestRequest(req)

	viewer, err := memoryViewerFromRequest(req)
	if err != nil {
		t.Fatalf("memoryViewerFromRequest: %v", err)
	}
	if viewer.AgentID != "agent-1" {
		t.Fatalf("agent id = %q", viewer.AgentID)
	}
	if !hasString(viewer.OrganizationIDs, "org-1") || !hasString(viewer.OrganizationIDs, "org-2") || !hasString(viewer.OrganizationIDs, "org-3") {
		t.Fatalf("organization ids = %+v", viewer.OrganizationIDs)
	}
	if !hasString(viewer.DepartmentIDs, "dept-1") || !hasString(viewer.DepartmentIDs, "dept-2") {
		t.Fatalf("department ids = %+v", viewer.DepartmentIDs)
	}
	if !hasString(viewer.ProjectIDs, "project-1") || !hasString(viewer.ProjectIDs, "project-2") {
		t.Fatalf("project ids = %+v", viewer.ProjectIDs)
	}
	if !hasString(viewer.MissionIDs, "mission-1") || !hasString(viewer.MissionIDs, "mission-2") {
		t.Fatalf("mission ids = %+v", viewer.MissionIDs)
	}
	if !hasString(viewer.DiscussionIDs, "discussion-1") || !hasString(viewer.DiscussionIDs, "discussion-2") {
		t.Fatalf("discussion ids = %+v", viewer.DiscussionIDs)
	}
}

func TestMemoryViewerFromRequestIgnoresUntrustedIdentityScopes(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/memory/records", nil)
	req.Header.Set("X-User-ID", "operator")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Permission-Scope", "memory.read")
	req.Header.Set("X-Agent-ID", "attacker-agent")
	req.Header.Set("X-Organization-IDs", "attacker-org")
	req.Header.Set("X-Project-IDs", "attacker-project")
	req.Header.Set("X-Mission-IDs", "attacker-mission")

	viewer, err := memoryViewerFromRequest(req)
	if err != nil {
		t.Fatalf("memoryViewerFromRequest: %v", err)
	}
	if viewer.AgentID != "" || len(viewer.OrganizationIDs) != 0 || len(viewer.ProjectIDs) != 0 || len(viewer.MissionIDs) != 0 {
		t.Fatalf("untrusted identity scopes were accepted: %+v", viewer)
	}
}

func TestMemoryViewerFromRequestUsesAuthenticatedProfileMembership(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/memory/records", nil)
	req.Header.Set("X-User-ID", "operator")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Permission-Scope", "memory.read")
	req.Header.Set("X-Organization-IDs", "attacker-org")
	req.Header.Set("X-Project-IDs", "attacker-project")
	req = req.WithContext(identity.ContextWithPrincipal(req.Context(), identity.Principal{
		SubjectProvider: identity.ProviderLocal,
		Subject:         "user-1",
		Username:        "operator",
		AgentProfileID:  "human-operator",
		ViewerKind:      identity.ViewerKindUser,
		CoarseScopes:    []string{"memory.read"},
	}))
	router := &Router{knowledgeProfiles: fakeKnowledgeProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"human-operator": {
			ID: "human-operator",
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef: agentcore.OrganizationRef{ID: "org-product"},
				DepartmentRef:   agentcore.DepartmentRef{ID: "dept-product"},
			},
		},
	}}}

	viewer, err := router.memoryViewerFromRequest(req)
	if err != nil {
		t.Fatalf("memoryViewerFromRequest: %v", err)
	}
	if viewer.AgentID != "human-operator" || !hasString(viewer.OrganizationIDs, "org-product") || !hasString(viewer.DepartmentIDs, "dept-product") {
		t.Fatalf("authenticated profile membership missing: %+v", viewer)
	}
	if hasString(viewer.OrganizationIDs, "attacker-org") || hasString(viewer.ProjectIDs, "attacker-project") {
		t.Fatalf("untrusted membership survived profile enrichment: %+v", viewer)
	}
}

func authedMemoryRequest(method, path, body string) *http.Request {
	req := authedRequest(method, path, body)
	req.Header.Set("X-Permission-Scope", "memory.read,memory.write,memory.review,memory.audit.read")
	return req
}

type fakeMemoryAdminService struct {
	candidatePage      memoryadmin.MemoryCandidatePageDTO
	recordPage         memoryadmin.MemoryRecordPageDTO
	record             memoryadmin.MemoryRecordDTO
	receipt            memoryadmin.CommandReceiptDTO
	mergeReceipt       memoryadmin.CommandReceiptDTO
	lifeReceipt        memoryadmin.CommandReceiptDTO
	sweepResult        memoryadmin.MemoryRetentionSweepResultDTO
	lastQueryViewer    memoryadmin.ViewerRef
	lastCommandViewer  memoryadmin.ViewerRef
	lastCandidateQuery memoryadmin.ListCandidatesQuery
	lastRecordQuery    memoryadmin.ListRecordsQuery
	createReq          memoryadmin.CreateCandidateRequest
	acceptID           string
	acceptReq          memoryadmin.AcceptCandidateRequest
	rejectID           string
	rejectReq          memoryadmin.RejectCandidateRequest
	mergeID            string
	mergeReq           memoryadmin.MergeCandidateRequest
	getRecordID        string
	touchReq           memoryadmin.TouchMemoryRecordsRequest
	sweepReq           memoryadmin.MemoryRetentionSweepRequest
	archiveID          string
	archiveReq         memoryadmin.MemoryLifecycleRequest
	anonymizeID        string
	anonymizeReq       memoryadmin.MemoryLifecycleRequest
	deleteID           string
	deleteReq          memoryadmin.MemoryLifecycleRequest
}

func (f *fakeMemoryAdminService) ListCandidates(_ context.Context, viewer memoryadmin.ViewerRef, query memoryadmin.ListCandidatesQuery) (memoryadmin.MemoryCandidatePageDTO, error) {
	f.lastQueryViewer = viewer
	f.lastCandidateQuery = query
	return f.candidatePage, nil
}

func (f *fakeMemoryAdminService) CreateCandidate(_ context.Context, viewer memoryadmin.ViewerRef, req memoryadmin.CreateCandidateRequest) (memoryadmin.MemoryCandidateDTO, error) {
	f.lastCommandViewer = viewer
	f.createReq = req
	return memoryadmin.MemoryCandidateDTO{CandidateID: req.CandidateID, ProposedLayer: req.ProposedLayer, Summary: req.Summary, Status: "proposed"}, nil
}

func (f *fakeMemoryAdminService) AcceptCandidate(_ context.Context, viewer memoryadmin.ViewerRef, candidateID string, req memoryadmin.AcceptCandidateRequest) (memoryadmin.MemoryRecordDTO, error) {
	f.lastCommandViewer = viewer
	f.acceptID = candidateID
	f.acceptReq = req
	return f.record, nil
}

func (f *fakeMemoryAdminService) RejectCandidate(_ context.Context, viewer memoryadmin.ViewerRef, candidateID string, req memoryadmin.RejectCandidateRequest) (memoryadmin.CommandReceiptDTO, error) {
	f.lastCommandViewer = viewer
	f.rejectID = candidateID
	f.rejectReq = req
	return f.receipt, nil
}

func (f *fakeMemoryAdminService) MergeCandidate(_ context.Context, viewer memoryadmin.ViewerRef, candidateID string, req memoryadmin.MergeCandidateRequest) (memoryadmin.CommandReceiptDTO, error) {
	f.lastCommandViewer = viewer
	f.mergeID = candidateID
	f.mergeReq = req
	return f.mergeReceipt, nil
}

func (f *fakeMemoryAdminService) ListRecords(_ context.Context, viewer memoryadmin.ViewerRef, query memoryadmin.ListRecordsQuery) (memoryadmin.MemoryRecordPageDTO, error) {
	f.lastQueryViewer = viewer
	f.lastRecordQuery = query
	return f.recordPage, nil
}

func (f *fakeMemoryAdminService) GetRecord(_ context.Context, viewer memoryadmin.ViewerRef, memoryID string) (memoryadmin.MemoryRecordDTO, error) {
	f.lastQueryViewer = viewer
	f.getRecordID = memoryID
	return f.record, nil
}

func (f *fakeMemoryAdminService) TouchRecords(_ context.Context, viewer memoryadmin.ViewerRef, req memoryadmin.TouchMemoryRecordsRequest) (memoryadmin.CommandReceiptDTO, error) {
	f.lastCommandViewer = viewer
	f.touchReq = req
	return memoryadmin.CommandReceiptDTO{Status: "referenced", UpdatedAt: f.lifeReceipt.UpdatedAt}, nil
}

func (f *fakeMemoryAdminService) SweepRetention(_ context.Context, viewer memoryadmin.ViewerRef, req memoryadmin.MemoryRetentionSweepRequest) (memoryadmin.MemoryRetentionSweepResultDTO, error) {
	f.lastCommandViewer = viewer
	f.sweepReq = req
	return f.sweepResult, nil
}

func (f *fakeMemoryAdminService) ArchiveRecord(_ context.Context, viewer memoryadmin.ViewerRef, memoryID string, req memoryadmin.MemoryLifecycleRequest) (memoryadmin.CommandReceiptDTO, error) {
	f.lastCommandViewer = viewer
	f.archiveID = memoryID
	f.archiveReq = req
	return f.lifeReceipt, nil
}

func (f *fakeMemoryAdminService) AnonymizeRecord(_ context.Context, viewer memoryadmin.ViewerRef, memoryID string, req memoryadmin.MemoryLifecycleRequest) (memoryadmin.MemoryRecordDTO, error) {
	f.lastCommandViewer = viewer
	f.anonymizeID = memoryID
	f.anonymizeReq = req
	return f.record, nil
}

func (f *fakeMemoryAdminService) DeleteRecord(_ context.Context, viewer memoryadmin.ViewerRef, memoryID string, req memoryadmin.MemoryLifecycleRequest) (memoryadmin.CommandReceiptDTO, error) {
	f.lastCommandViewer = viewer
	f.deleteID = memoryID
	f.deleteReq = req
	return memoryadmin.CommandReceiptDTO{Status: "deleted", MemoryID: memoryID, UpdatedAt: f.lifeReceipt.UpdatedAt}, nil
}
