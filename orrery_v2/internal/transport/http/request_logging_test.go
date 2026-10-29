package httptransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestLoggerWritesNormalAndErrorRequests(t *testing.T) {
	logger := &captureRequestLogger{}
	handler := WithRequestLogger(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/ok":
			writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
		case "/fail":
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{
				Code:          ErrorInternal,
				Message:       "internal server error",
				CorrelationID: correlationID(req),
			})
		default:
			writeJSON(w, http.StatusNotFound, ErrorResponse{
				Code:          ErrorNotFound,
				Message:       "route not found",
				CorrelationID: correlationID(req),
			})
		}
	}), logger)

	okReq := httptest.NewRequest(http.MethodGet, "/ok", nil)
	okReq.Header.Set("X-Request-ID", "req-ok")
	handler.ServeHTTP(httptest.NewRecorder(), okReq)

	failReq := httptest.NewRequest(http.MethodPost, "/fail", nil)
	failReq.Header.Set("X-Request-ID", "req-fail")
	handler.ServeHTTP(httptest.NewRecorder(), failReq)

	if len(logger.records) != 2 {
		t.Fatalf("log records = %+v, want 2", logger.records)
	}
	okRecord := logger.records[0]
	if okRecord.level != "info" || okRecord.fields["method"] != http.MethodGet || okRecord.fields["path"] != "/ok" || okRecord.fields["status"] != http.StatusOK || okRecord.fields["correlation_id"] != "req-ok" {
		t.Fatalf("normal request log = %+v", okRecord)
	}
	if _, exists := okRecord.fields["duration_ms"]; !exists {
		t.Fatalf("normal request log missing duration_ms: %+v", okRecord.fields)
	}

	fail := logger.records[1]
	if fail.level != "error" || fail.fields["method"] != http.MethodPost || fail.fields["path"] != "/fail" || fail.fields["status"] != http.StatusInternalServerError || fail.fields["correlation_id"] != "req-fail" {
		t.Fatalf("error request log = %+v", fail)
	}
	if fail.fields["error_code"] != ErrorInternal || fail.fields["error_message"] != "internal server error" {
		t.Fatalf("error details missing from request log: %+v", fail.fields)
	}
}

type captureRequestLogger struct {
	records []captureRequestLogRecord
}

type captureRequestLogRecord struct {
	level  string
	msg    string
	fields map[string]any
}

func (l *captureRequestLogger) Info(ctx context.Context, msg string, fields map[string]any) {
	l.records = append(l.records, captureRequestLogRecord{level: "info", msg: msg, fields: fields})
}

func (l *captureRequestLogger) Error(ctx context.Context, msg string, fields map[string]any) {
	l.records = append(l.records, captureRequestLogRecord{level: "error", msg: msg, fields: fields})
}
