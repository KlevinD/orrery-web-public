package httptransport

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/platform/logging"
)

const maxLoggedResponseBodyBytes = 4096

type RequestLogger interface {
	Info(ctx context.Context, msg string, fields map[string]any)
	Error(ctx context.Context, msg string, fields map[string]any)
}

func WithRequestLogger(next http.Handler, logger RequestLogger) http.Handler {
	if next == nil || logger == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		id := strings.TrimSpace(req.Header.Get("X-Request-ID"))
		if id == "" {
			id = strings.TrimSpace(req.Header.Get("X-Correlation-ID"))
		}
		if id == "" || len(id) > 128 || strings.ContainsAny(id, "\r\n\t") {
			var b [16]byte
			_, _ = rand.Read(b[:])
			id = "http-" + hex.EncodeToString(b[:])
		}
		req.Header.Set("X-Request-ID", id)
		w.Header().Set("X-Request-ID", id)
		start := time.Now()
		recorder := &loggingResponseWriter{ResponseWriter: w}
		defer func() {
			var aborted bool
			if recovered := recover(); recovered != nil {
				if recovered == http.ErrAbortHandler {
					aborted = true
				} else {
					recorder.RecordError(fmt.Errorf("http handler panic: %v", recovered))
					recorder.stack = logging.RedactDiagnostic(string(debug.Stack()), 8192)
					if recorder.status == 0 {
						writeJSON(recorder, http.StatusInternalServerError, ErrorResponse{Code: ErrorInternal, Message: "internal server error", CorrelationID: correlationID(req)})
					} else {
						aborted = true
					}
				}
			}
			status := recorder.status
			if status == 0 {
				status = http.StatusOK
			}
			fields := map[string]any{
				"operation":      "http.request",
				"method":         req.Method,
				"path":           req.URL.Path,
				"status":         status,
				"duration_ms":    time.Since(start).Milliseconds(),
				"bytes":          recorder.bytes,
				"request_id":     correlationID(req),
				"correlation_id": correlationID(req),
				"remote_addr":    req.RemoteAddr,
				"user_agent":     req.UserAgent(),
			}
			if principal, ok := identity.PrincipalFromContext(req.Context()); ok && principal.Authenticated() {
				fields["tenant_id"] = principal.TenantID
			}
			if recorder.cause != nil {
				fields["error_cause"] = logging.RedactDiagnostic(recorder.cause.Error(), 4096)
			}
			if recorder.stack != "" {
				fields["stack"] = recorder.stack
			}
			if aborted {
				fields["error_code"] = "http_response_aborted"
			}
			if status >= http.StatusBadRequest {
				code, message := loggedErrorResponse(recorder.body.Bytes())
				if code != "" {
					fields["error_code"] = code
				}
				if message != "" {
					fields["error_message"] = message
				}
			}
			if status >= http.StatusInternalServerError || recorder.stack != "" || aborted {
				logger.Error(req.Context(), "http request failed", fields)
			} else {
				logger.Info(req.Context(), "http request completed", fields)
			}
			if aborted {
				panic(http.ErrAbortHandler)
			}
		}()
		next.ServeHTTP(recorder, req)
	})
}

type loggingResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int
	body   bytes.Buffer
	cause  error
	stack  string
}

func (w *loggingResponseWriter) RecordError(err error) {
	w.cause = err
	if next, ok := w.ResponseWriter.(interface{ RecordError(error) }); ok {
		next.RecordError(err)
	}
}
func (w *loggingResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *loggingResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *loggingResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(data)
	w.bytes += n
	if remaining := maxLoggedResponseBodyBytes - w.body.Len(); remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
		}
		_, _ = w.body.Write(data)
	}
	return n, err
}

func (w *loggingResponseWriter) Flush() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *loggingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

func loggedErrorResponse(raw []byte) (string, string) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return "", ""
	}
	var response ErrorResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return "", ""
	}
	return response.Code, response.Message
}
