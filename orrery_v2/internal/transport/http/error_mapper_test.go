package httptransport

import (
	"encoding/json"
	"errors"
	"io"
	"testing"

	"orrery_v2/internal/app/discussionsession"
	knowledgeadmin "orrery_v2/internal/app/knowledgeadmin"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/ontologyreasoning"
	resourceadmin "orrery_v2/internal/app/resourceadmin"
	"orrery_v2/internal/app/scheduler"
	knowledgecore "orrery_v2/internal/knowledge/core"
	doccore "orrery_v2/internal/platform/document/core"
	"orrery_v2/internal/retrievalevidence"
)

func TestMapErrorUsesStableCodesAndDoesNotLeakInternalError(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		code   string
		status int
	}{
		{name: "projection not found", err: missioncontrol.ErrProjectionNotFound, code: "not_found", status: 404},
		{name: "ontology not found", err: ontology.ErrNotFound, code: "not_found", status: 404},
		{name: "ontology invalid", err: ontology.ErrInvalidDefinition, code: "invalid_request", status: 400},
		{name: "ontology reasoning not found", err: ontologyreasoning.ErrNotFound, code: "not_found", status: 404},
		{name: "ontology reasoning invalid", err: ontologyreasoning.ErrInvalidRequest, code: "invalid_request", status: 400},
		{name: "json syntax", err: &json.SyntaxError{Offset: 1}, code: "invalid_request", status: 400},
		{name: "json unexpected eof", err: io.ErrUnexpectedEOF, code: "invalid_request", status: 400},
		{name: "ontology version conflict", err: ontology.ErrVersionConflict, code: "version_conflict", status: 409},
		{name: "ontology idempotency conflict", err: ontology.ErrIdempotencyConflict, code: "idempotency_conflict", status: 409},
		{name: "resource conflict", err: resourceadmin.ErrConflict, code: "conflict", status: 409},
		{name: "retrieval changed", err: retrievalevidence.ErrInputChanged, code: "conflict", status: 409},
		{name: "mission conflict", err: missioncontrol.ErrConflict, code: "conflict", status: 409},
		{name: "mission task run active", err: missioncontrol.ErrAgentRunAlreadyActive, code: "conflict", status: 409},
		{name: "invalid viewer", err: missioncontrol.ErrInvalidViewer, code: "unauthorized", status: 401},
		{name: "service dependency", err: missioncontrol.ErrServiceDependency, code: "resource_unavailable", status: 503},
		{name: "unknown", err: errors.New("sql: secret raw stack"), code: "internal_error", status: 500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := MapError(tc.err, "corr-1")
			if status != tc.status || body.Code != tc.code {
				t.Fatalf("status/body = %d/%+v", status, body)
			}
			if body.CorrelationID != "corr-1" {
				t.Fatalf("correlation id = %q", body.CorrelationID)
			}
			if tc.code == "internal_error" && body.Message == "sql: secret raw stack" {
				t.Fatal("internal error leaked raw message")
			}
		})
	}
}

func TestMapErrorMarksPendingRuntimeApprovalAsNonRetryableConflict(t *testing.T) {
	status, body := MapError(discussionsession.ErrRuntimeApprovalPending, "corr-approval")
	if status != 409 || body.Code != ErrorConflict || body.Retryable {
		t.Fatalf("status/body = %d/%+v, want non-retryable conflict", status, body)
	}
}

func TestMapErrorMarksSchedulerRevisionConflictAsRetryable(t *testing.T) {
	status, body := MapError(scheduler.ErrRevisionConflict, "corr-scheduler")
	if status != 409 || body.Code != ErrorVersionConflict || !body.Retryable {
		t.Fatalf("status/body = %d/%+v, want retryable version conflict", status, body)
	}
}

func TestStableErrorCodesIncludeKnowledgeCodes(t *testing.T) {
	codes := StableErrorCodes()
	for _, code := range []string{
		ErrorKnowledgeInvalidRequest,
		ErrorKnowledgeNotFound,
		ErrorKnowledgePermissionDenied,
		ErrorKnowledgeConflict,
		ErrorKnowledgeDependencyUnavailable,
		ErrorKnowledgeIngestFailed,
		ErrorKnowledgeVectorStoreUnavailable,
		ErrorKnowledgeEmbeddingFailed,
		ErrorKnowledgeDocumentUnsupported,
		ErrorKnowledgeDeleteFailed,
	} {
		if !codes[code] {
			t.Fatalf("stable error codes missing %s", code)
		}
	}
}

func TestMapErrorUsesKnowledgeStableCodes(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		code      string
		status    int
		retryable bool
	}{
		{name: "invalid app request", err: knowledgeadmin.ErrInvalidRequest, code: ErrorKnowledgeInvalidRequest, status: 400},
		{name: "invalid core request", err: knowledgecore.ErrInvalid, code: ErrorKnowledgeInvalidRequest, status: 400},
		{name: "not found", err: knowledgecore.ErrNotFound, code: ErrorKnowledgeNotFound, status: 404},
		{name: "permission", err: knowledgecore.ErrPermissionDenied, code: ErrorKnowledgePermissionDenied, status: 403},
		{name: "conflict", err: knowledgecore.ErrConflict, code: ErrorKnowledgeConflict, status: 409},
		{name: "dependency", err: knowledgecore.ErrDependencyUnavailable, code: ErrorKnowledgeDependencyUnavailable, status: 503, retryable: true},
		{name: "ingest", err: knowledgecore.ErrIngestFailed, code: ErrorKnowledgeIngestFailed, status: 500},
		{name: "vector", err: knowledgecore.ErrVectorStoreUnavailable, code: ErrorKnowledgeVectorStoreUnavailable, status: 503, retryable: true},
		{name: "embedding", err: knowledgecore.ErrEmbeddingFailed, code: ErrorKnowledgeEmbeddingFailed, status: 502, retryable: true},
		{name: "document unsupported", err: knowledgecore.ErrDocumentUnsupported, code: ErrorKnowledgeDocumentUnsupported, status: 415},
		{name: "platform document unsupported", err: doccore.ErrUnsupported, code: ErrorKnowledgeDocumentUnsupported, status: 415},
		{name: "delete", err: knowledgecore.ErrDeleteFailed, code: ErrorKnowledgeDeleteFailed, status: 503, retryable: true},
		{name: "service dependency", err: knowledgeadmin.ErrServiceDependency, code: ErrorKnowledgeDependencyUnavailable, status: 503, retryable: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := MapError(tc.err, "corr-knowledge")
			if status != tc.status || body.Code != tc.code || body.Retryable != tc.retryable {
				t.Fatalf("status/body = %d/%+v", status, body)
			}
			if body.CorrelationID != "corr-knowledge" {
				t.Fatalf("correlation id = %q", body.CorrelationID)
			}
		})
	}
}
