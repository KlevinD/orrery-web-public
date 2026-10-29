package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	"orrery_v2/internal/app/reliability"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/logging"
	httptransport "orrery_v2/internal/transport/http"
)

func TestProductionGatewayDiagnosticsReachRepairInbox(t *testing.T) {
	var output bytes.Buffer
	logger, closeLog, err := logging.NewConfiguredLogger(logging.Config{Level: "info", Console: true}, &output)
	if err != nil {
		t.Fatal(err)
	}
	defer closeLog()
	rt := &platform.Runtime{Logger: logger}
	rt.Config.FileStore.Root = t.TempDir()
	repo := reliability.NewMemoryRepository()
	diagnostics, err := registerProductionFailureCapture(rt, reliability.NewService(repo), false)
	if err != nil {
		t.Fatal(err)
	}
	productionGatewayChannelLogger{logger: logger}.Error("feishu SDK error", map[string]any{
		"error_code": "sdk_error_websocket", "error_kind": "websocket", "sdk_status_code": "503",
		"error_cause": "websocket connection reset token=private", "account_id": "account-a",
		"tenant_id": "other-tenant", "failure_captured": true,
	})
	if diagnostics.inbox.Status().Pending != 1 {
		t.Fatal("SDK error was dropped")
	}
	if err = diagnostics.inbox.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(rt.Config.FileStore.Root, "diagnostics", "failure-inbox", "history", "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("history=%v err=%v", files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var captured reliability.CapturedFailure
	if err = json.Unmarshal(data, &captured); err != nil {
		t.Fatal(err)
	}
	if captured.Observation.TenantID != "global" || captured.Observation.ErrorCode != "sdk_error_websocket" ||
		captured.Diagnostics["error_kind"] != "websocket" || captured.Diagnostics["account_id"] != "account-a" ||
		captured.Diagnostics["sdk_status_code"] != "503" || strings.Contains(string(data), "private") {
		t.Fatalf("diagnostics lost or unsafe: %s", data)
	}
	incident, err := repo.GetIncident(context.Background(), "global", captured.IncidentID)
	if err != nil || incident.Category != reliability.FailureCategoryTransient || !incident.Retryable {
		t.Fatalf("incident=%+v err=%v", incident, err)
	}
}

func TestProductionCaptureBridgesLogsAndClientReports(t *testing.T) {
	var output bytes.Buffer
	logger, closeLog, err := logging.NewConfiguredLogger(logging.Config{Level: "info", Console: true}, &output)
	if err != nil {
		t.Fatal(err)
	}
	defer closeLog()
	rt := &platform.Runtime{Logger: logger}
	rt.Config.FileStore.Root = t.TempDir()
	service := reliability.NewService(reliability.NewMemoryRepository())
	diagnostics, err := registerProductionFailureCapture(rt, service, false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Expired request contexts must still produce a durable diagnostic.
	logger.Error(ctx, "http request failed", logging.String("operation", "http.request"), logging.String("method", "GET"), logging.String("path", "/api/runs/123?token=private"), logging.String("error_cause", "database timeout token=private"), logging.String("tenant_id", "tenant-a"), logging.String("request_id", "req-1"))
	logger.Warn(ctx, "slow but successful", logging.Int("duration_ms", 3000))
	logger.Warn(ctx, "outbox relay runner error", logging.Any("failure_captured", true), logging.String("error", "already durably captured provider failure"))
	logger.Warn(ctx, "sweeper failed", logging.String("sweeper_name", "reliability.failure_capture"), logging.String("error", "do not recurse"))
	if diagnostics.inbox.Status().Pending != 1 {
		t.Fatalf("capture missing or noisy: %+v", diagnostics.inbox.Status())
	}
	principal := identity.Principal{SubjectProvider: "test", Subject: "user-a", ViewerKind: identity.ViewerKindUser, UserID: "user-a", Username: "alice", TenantID: "tenant-a"}
	report := httptransport.ClientErrorReport{ID: "client-one", ActorID: "alice", Kind: "javascript", Name: "TypeError", Message: "holdings failed password=private", Path: "/holdings?token=private"}
	first, err := diagnostics.ReportClientError(context.Background(), principal, report)
	if err != nil {
		t.Fatal(err)
	}
	second, err := diagnostics.ReportClientError(context.Background(), principal, report)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || diagnostics.inbox.Status().Pending != 2 {
		t.Fatal("client replay duplicated")
	}
	report.ActorID = "bob"
	if _, err = diagnostics.ReportClientError(context.Background(), principal, report); !errors.Is(err, httptransport.ErrDiagnosticActorMismatch) {
		t.Fatalf("stale actor accepted: %v", err)
	}
	if err = diagnostics.inbox.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if diagnostics.inbox.Status().Pending != 0 || diagnostics.inbox.Status().Delivered != 2 {
		t.Fatal(diagnostics.inbox.Status())
	}
	if strings.Contains(diagnosticPath("/api/runs/123?token=private"), "private") || diagnosticPath("/api/runs/123") != "/api/runs/:id" {
		t.Fatal("path not canonicalized")
	}
	if diagnosticComponent("异常") == "" || diagnosticComponent("异常") == diagnosticComponent("失败") {
		t.Fatal("non-Latin events lose their identity")
	}
}

type diagnosticsReadOnlyAccess struct {
	reliabilityTestAccessAuthorizer
}

func (diagnosticsReadOnlyAccess) AuthorizeFeature(_ context.Context, _ identity.Principal, scope string) error {
	if strings.HasSuffix(scope, "manage") {
		return authorization.ErrDenied
	}
	return nil
}

func TestProductionCapturedDiagnosticsRequireScopeManagementAccess(t *testing.T) {
	ctx := context.Background()
	repository := reliability.NewMemoryRepository()
	control := reliability.NewService(repository)
	observation := reliability.FailureObservation{ID: "captured", TenantID: "tenant-a", Source: "web_client", Component: "javascript:/holdings", ErrorCode: "TypeError", Message: "holdings unavailable", OccurredAt: time.Now().UTC()}
	q, err := reliability.NewCaptureInbox(t.TempDir(), control)
	if err != nil {
		t.Fatal(err)
	}
	if err = q.Enqueue(ctx, reliability.CapturedFailure{Observation: observation, Diagnostics: map[string]string{"cause": "holdings unavailable", "stack": "at renderer"}}); err != nil {
		t.Fatal(err)
	}
	if err = q.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	incident, err := control.ObserveFailure(ctx, observation)
	if err != nil {
		t.Fatal(err)
	}
	api := newProductionOntologyReliabilityAPI(repository, control, &reliabilityEvidenceContext{}, reliabilityTestGate(), newReliabilityTestDecisions(), nil, nil)
	api.capture = q
	detail, err := api.GetIncident(ctx, reliabilityTestPrincipal(), "tenant-a", incident.ID)
	if err != nil || len(detail.Diagnostics) != 1 || detail.Diagnostics[0].Details["stack"] != "at renderer" {
		t.Fatalf("manager diagnostics = %+v, %v", detail, err)
	}
	api.gate = ontologyscope.NewScopeAccessGate(reliabilityTestBindingResolver{}, diagnosticsReadOnlyAccess{}, reliabilityTestSnapshots{})
	detail, err = api.GetIncident(ctx, reliabilityTestPrincipal(), "tenant-a", incident.ID)
	if err != nil || detail.Incident.ID != incident.ID || len(detail.Diagnostics) != 0 || detail.DiagnosticsWarning != "manage_access_required" {
		t.Fatalf("read-only diagnostic access = %+v, %v", detail, err)
	}
}
