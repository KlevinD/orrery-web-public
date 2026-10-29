package httptransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orrery_v2/internal/app/memorydistill"
)

type fakeMemoryDistillService struct {
	viewer   memorydistill.ViewerRef
	req      memorydistill.DistillExperienceRequest
	worldReq memorydistill.DistillWorldRequest
	err      error
}

func (f *fakeMemoryDistillService) DistillExperience(ctx context.Context, viewer memorydistill.ViewerRef, req memorydistill.DistillExperienceRequest) (memorydistill.DistillExperienceResponse, error) {
	f.viewer = viewer
	f.req = req
	if f.err != nil {
		return memorydistill.DistillExperienceResponse{}, f.err
	}
	return memorydistill.DistillExperienceResponse{
		Candidates:      []memorydistill.ExperienceCandidateDTO{{CandidateID: "candidate-experience-1", Topic: "qa", Confidence: 0.7}},
		SourceMemoryIDs: []string{"memory-qa-1", "memory-qa-2"},
	}, nil
}

func (f *fakeMemoryDistillService) DistillWorld(ctx context.Context, viewer memorydistill.ViewerRef, req memorydistill.DistillWorldRequest) (memorydistill.DistillWorldResponse, error) {
	f.viewer = viewer
	f.worldReq = req
	if f.err != nil {
		return memorydistill.DistillWorldResponse{}, f.err
	}
	return memorydistill.DistillWorldResponse{
		Candidates:      []memorydistill.WorldCandidateDTO{{CandidateID: "candidate-world-1", Topic: "quality", Confidence: 0.78}},
		SourceMemoryIDs: []string{"memory-exp-1", "memory-exp-2"},
	}, nil
}

func TestMemoryDistillRouteForwardsViewerAndReturnsAccepted(t *testing.T) {
	service := &fakeMemoryDistillService{}
	router := newTestRouter(RouterOptions{MemoryDistill: service})
	req := authedHTTPTestRequest(http.MethodPost, "/api/memory/distill/experience", strings.NewReader(`{"target_scope":{"kind":"project","id":"project-1"},"source_scope":{"kind":"mission","id":"mission-1"},"topic":"qa","limit":25}`))
	req.Header.Set("X-User-ID", "operator-1")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Permission-Scope", "memory.distill")
	req.Header.Set("X-Project-ID", "project-1")
	req.Header.Set("X-Mission-ID", "mission-1")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if service.viewer.ViewerID != "operator-1" || service.viewer.Language != "en-US" || !hasString(service.viewer.ProjectIDs, "project-1") || !hasString(service.viewer.MissionIDs, "mission-1") {
		t.Fatalf("viewer = %+v", service.viewer)
	}
	if service.req.Topic != "qa" || service.req.TargetScope.ID != "project-1" || service.req.SourceScope.ID != "mission-1" || service.req.Limit != 25 {
		t.Fatalf("request = %+v", service.req)
	}
}

func TestMemoryWorldDistillRouteForwardsViewerAndReturnsAccepted(t *testing.T) {
	service := &fakeMemoryDistillService{}
	router := newTestRouter(RouterOptions{MemoryDistill: service})
	req := authedHTTPTestRequest(http.MethodPost, "/api/memory/distill/world", strings.NewReader(`{"target_scope":{"kind":"organization","id":"org-1"},"source_scope":{"kind":"project","id":"project-1"},"topic":"quality","limit":40}`))
	req.Header.Set("X-User-ID", "operator-1")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Permission-Scope", "memory.distill")
	req.Header.Set("X-Organization-ID", "org-1")
	req.Header.Set("X-Project-ID", "project-1")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if service.viewer.ViewerID != "operator-1" || !hasString(service.viewer.OrganizationIDs, "org-1") || !hasString(service.viewer.ProjectIDs, "project-1") {
		t.Fatalf("viewer = %+v", service.viewer)
	}
	if service.worldReq.Topic != "quality" || service.worldReq.TargetScope.ID != "org-1" || service.worldReq.SourceScope.ID != "project-1" || service.worldReq.Limit != 40 {
		t.Fatalf("world request = %+v", service.worldReq)
	}
}

func TestMemoryDistillRouteReturnsDependencyUnavailableWhenServiceMissing(t *testing.T) {
	router := newTestRouter(RouterOptions{})
	req := authedHTTPTestRequest(http.MethodPost, "/api/memory/distill/experience", strings.NewReader(`{"target_scope":{"kind":"project","id":"project-1"},"source_scope":{"kind":"mission","id":"mission-1"},"topic":"qa"}`))
	req.Header.Set("X-User-ID", "operator-1")
	req.Header.Set("X-Permission-Scope", "memory.distill")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "memory_distillation_dependency_unavailable") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
