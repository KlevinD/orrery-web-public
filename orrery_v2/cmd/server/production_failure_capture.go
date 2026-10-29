package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/reliability"
	"orrery_v2/internal/app/userauth"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/execution"
	"orrery_v2/internal/platform/logging"
	httptransport "orrery_v2/internal/transport/http"
)

var diagnosticComponentPattern = regexp.MustCompile(`[^a-zA-Z0-9_.:/-]+`)
var diagnosticIdentifierPattern = regexp.MustCompile(`^[0-9]+$|^[0-9a-fA-F-]{16,}$`)

func diagnosticComponent(value string) string {
	original := value
	value = strings.ToLower(diagnosticComponentPattern.ReplaceAllString(value, "-"))
	if len(value) > 160 {
		value = value[:160]
	}
	value = strings.Trim(value, "-")
	if value == "" {
		digest := sha256.Sum256([]byte(original))
		return "event-" + hex.EncodeToString(digest[:8])
	}
	return value
}
func diagnosticPath(value string) string {
	u, err := url.Parse(value)
	if err != nil {
		return "/"
	}
	parts := strings.Split(u.EscapedPath(), "/")
	for i, p := range parts {
		if len(p) > 48 || diagnosticIdentifierPattern.MatchString(p) {
			parts[i] = ":id"
		}
	}
	return logging.RedactDiagnostic(strings.Join(parts, "/"), 256)
}

type diagnosticRate struct {
	start time.Time
	count int
}
type productionClientDiagnostics struct {
	inbox *reliability.CaptureInbox
	mu    sync.Mutex
	rates map[string]diagnosticRate
}

func registerProductionFailureCapture(rt *platform.Runtime, control *reliability.Service, disabled bool) (*productionClientDiagnostics, error) {
	if disabled {
		return nil, nil
	}
	q, err := reliability.NewCaptureInbox(filepath.Join(rt.Config.FileStore.Root, "diagnostics", "failure-inbox"), control)
	if err != nil {
		return nil, err
	}
	d := &productionClientDiagnostics{inbox: q, rates: map[string]diagnosticRate{}}
	worker := execution.NewBackgroundSweeper("reliability.failure_capture", q, 20*time.Second, rt.Logger)
	if err := rt.RegisterSweeper("reliability.failure_capture", worker); err != nil {
		return nil, err
	}
	logging.SetObserver(rt.Logger, func(ctx context.Context, event logging.Observation) {
		ctx = context.WithoutCancel(ctx)
		fields := map[string]string{}
		var metadata map[string]any
		for _, f := range event.Fields {
			fields[f.Key] = fmt.Sprint(f.Value.Resolve().Any())
			if f.Key == "metadata" {
				metadata, _ = f.Value.Resolve().Any().(map[string]any)
			}
		}
		// Gateway adapters retain their SDK diagnostics in metadata. Promote only
		// diagnostic fields; nested data cannot override tenant or actor identity.
		for _, key := range []string{"error_code", "error_cause", "error_message", "error_kind", "sdk_status_code", "account_id", "run_id", "task_id", "operation"} {
			if fields[key] == "" && metadata[key] != nil {
				fields[key] = fmt.Sprint(metadata[key])
			}
		}
		if fields["failure_captured"] == "true" {
			return
		}
		if strings.Contains(fields["sweeper_name"], "reliability.failure_capture") || strings.Contains(fields["sweeper_name"], "reliability.dependency_recovery") || strings.Contains(fields["operation"], "failure_capture") {
			return
		}
		if event.Level < logging.LevelError && fields["error"] == "" && fields["error_cause"] == "" {
			return
		}
		tenant := fields["tenant_id"]
		if tenant == "" {
			tenant = "global"
		}
		component := firstNonBlank(fields["sweeper_name"], fields["operation"], fields["component"], event.Message)
		if fields["operation"] == "http.request" {
			component += " " + fields["method"] + " " + diagnosticPath(fields["path"])
		}
		cause := firstNonBlank(fields["error_cause"], fields["error"], fields["error_message"], event.Message)
		id := reliability.NewCaptureID()
		correlation := firstNonBlank(fields["correlation_id"], fields["request_id"], logging.CorrelationID(ctx))
		message := cause
		if correlation != "" {
			message = "request " + correlation + ": " + message
		}
		err := q.Enqueue(ctx, reliability.CapturedFailure{Observation: reliability.FailureObservation{ID: id, TenantID: tenant, Source: "server_log", Component: diagnosticComponent(component), ErrorCode: firstNonBlank(fields["error_code"], "logged_error"), Message: message, CorrelationID: correlation, Actor: reliability.ActorRef{Kind: "service", ID: "failure-capture"}}, Diagnostics: map[string]string{"cause": cause, "stack": fields["stack"], "path": diagnosticPath(fields["path"]), "request_id": correlation, "run_id": fields["run_id"], "task_id": fields["task_id"], "operation": fields["operation"], "error_kind": fields["error_kind"], "sdk_status_code": fields["sdk_status_code"], "account_id": fields["account_id"], "storage_capacity": failureCapacityDiagnostics(rt.Config.FileStore.Root, cause)}})
		if err != nil {
			fmt.Fprintln(os.Stderr, "failure_capture: diagnostic queue write failed:", logging.RedactDiagnostic(err.Error(), 240))
		}
	})
	return d, nil
}

func (d *productionClientDiagnostics) ReportClientError(ctx context.Context, principal identity.Principal, report httptransport.ClientErrorReport) (httptransport.ClientErrorReceipt, error) {
	if d == nil || d.inbox == nil {
		return httptransport.ClientErrorReceipt{}, userauth.ErrServiceDependency
	}
	if !principal.Authenticated() {
		return httptransport.ClientErrorReceipt{}, userauth.ErrUnauthorized
	}
	if report.ActorID != "" && report.ActorID != principal.Username && report.ActorID != principal.UserID && report.ActorID != principal.Subject {
		return httptransport.ClientErrorReceipt{}, httptransport.ErrDiagnosticActorMismatch
	}
	if report.ID == "" || len(report.ID) > 128 || len(report.RequestID) > 128 || len(report.Name) > 128 || len(report.Path) > 2048 || len(report.Message) > 8192 || len(report.Stack) > 8192 || report.Message == "" {
		return httptransport.ClientErrorReceipt{}, userauth.ErrInvalidRequest
	}
	switch report.Kind {
	case "javascript", "unhandled_rejection", "api_network", "api_response":
	default:
		return httptransport.ClientErrorReceipt{}, userauth.ErrInvalidRequest
	}
	tenant := strings.TrimSpace(principal.TenantID)
	if tenant == "" {
		tenant = "global"
	}
	actor := firstNonBlank(principal.UserID, principal.Subject, principal.Username)
	key := tenant + "\x00" + actor
	d.mu.Lock()
	if d.rates == nil {
		d.rates = map[string]diagnosticRate{}
	}
	now := time.Now()
	rate := d.rates[key]
	if now.Sub(rate.start) >= time.Minute {
		rate = diagnosticRate{start: now}
	}
	if rate.count >= 60 {
		d.mu.Unlock()
		return httptransport.ClientErrorReceipt{}, httptransport.ErrDiagnosticRateLimited
	}
	if len(d.rates) > 4096 {
		for k, v := range d.rates {
			if now.Sub(v.start) >= time.Minute {
				delete(d.rates, k)
			}
		}
		if len(d.rates) > 4096 {
			d.mu.Unlock()
			return httptransport.ClientErrorReceipt{}, httptransport.ErrDiagnosticRateLimited
		}
	}
	rate.count++
	d.rates[key] = rate
	d.mu.Unlock()
	h := sha256.Sum256([]byte(key + "\x00" + report.ID))
	id := "client-" + hex.EncodeToString(h[:16])
	path := diagnosticPath(report.Path)
	err := d.inbox.Enqueue(ctx, reliability.CapturedFailure{Observation: reliability.FailureObservation{ID: id, TenantID: tenant, Source: "web_client", Component: diagnosticComponent(report.Kind + ":" + path), ErrorCode: diagnosticComponent(firstNonBlank(report.Name, "client_error")), Message: logging.RedactDiagnostic(report.Message, 4096), CorrelationID: report.RequestID, Actor: reliability.ActorRef{Kind: "human", ID: actor}}, Diagnostics: map[string]string{"cause": report.Message, "stack": report.Stack, "path": path, "request_id": report.RequestID}})
	if err != nil {
		return httptransport.ClientErrorReceipt{}, fmt.Errorf("capture client diagnostic: %w", err)
	}
	return httptransport.ClientErrorReceipt{ID: id, Status: "queued"}, nil
}
