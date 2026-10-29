package httptransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/agenttrace"
)

func TestAgentTraceRoutesRequireReadPermissionAndUseViewerTenant(t *testing.T) {
	service := &fakeAgentTraceService{metadata: agenttrace.MetadataDTO{RunID: "run-1", TraceVersion: 2, TraceAvailable: true, TraceStatus: agenttrace.TraceHot}}
	handler := newTestRouter(RouterOptions{AgentTraces: service})

	denied := authedRequest(http.MethodGet, "/api/agent-runs/run-1/trace", "")
	denied.Header.Set("X-Tenant-ID", "tenant-a")
	denied.Header.Set("X-Permission-Scope", "mission.read")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, denied)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("denied status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	allowed := authedRequest(http.MethodGet, "/api/agent-runs/run-1/trace?tenant_id=tenant-b", "")
	allowed.Header.Set("X-Tenant-ID", "tenant-a")
	allowed.Header.Set("X-Permission-Scope", "agent.trace.read")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, allowed)
	if recorder.Code != http.StatusOK || service.tenantID != "tenant-a" || strings.Contains(recorder.Body.String(), "trace_ref") {
		t.Fatalf("status=%d tenant=%q body=%s", recorder.Code, service.tenantID, recorder.Body.String())
	}
}

func TestAgentTraceLegalHoldRequiresManagePermission(t *testing.T) {
	service := &fakeAgentTraceService{}
	handler := newTestRouter(RouterOptions{AgentTraces: service})
	request := authedRequest(http.MethodPost, "/api/agent-runs/run-1/trace/legal-hold", `{"reason":"incident review"}`)
	request.Header.Set("X-Tenant-ID", "tenant-a")
	request.Header.Set("X-Permission-Scope", "agent.trace.manage")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted || service.holdReason != "incident review" || service.actor == "" {
		t.Fatalf("status=%d actor=%q reason=%q body=%s", recorder.Code, service.actor, service.holdReason, recorder.Body.String())
	}
}

type fakeAgentTraceService struct {
	metadata   agenttrace.MetadataDTO
	tenantID   string
	actor      string
	holdReason string
}

func (s *fakeAgentTraceService) Metadata(_ context.Context, tenantID, _ string) (agenttrace.MetadataDTO, error) {
	s.tenantID = tenantID
	return s.metadata, nil
}
func (*fakeAgentTraceService) Manifest(context.Context, string, string) (any, error) {
	return map[string]any{"status": "completed"}, nil
}
func (*fakeAgentTraceService) Events(context.Context, string, string, uint64, int) (agenttrace.EventPageDTO, error) {
	return agenttrace.EventPageDTO{}, nil
}
func (s *fakeAgentTraceService) ApplyLegalHold(_ context.Context, _, _, actor, reason string, _ time.Time) error {
	s.actor, s.holdReason = actor, reason
	return nil
}
func (*fakeAgentTraceService) ReleaseLegalHold(context.Context, string, string) error { return nil }
