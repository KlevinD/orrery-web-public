package httptransport

const (
	ErrorInvalidRequest        = "invalid_request"
	ErrorUnauthorized          = "unauthorized"
	ErrorForbidden             = "forbidden"
	ErrorNotFound              = "not_found"
	ErrorConflict              = "conflict"
	ErrorFailedPrecondition    = "failed_precondition"
	ErrorVersionConflict       = "version_conflict"
	ErrorIdempotencyConflict   = "idempotency_conflict"
	ErrorRateLimited           = "rate_limited"
	ErrorResourceUnavailable   = "resource_unavailable"
	ErrorAppRuleDenied         = "app_rule_denied"
	ErrorRuntimeUnavailable    = "runtime_unavailable"
	ErrorHTTPSRequired         = "https_required"
	ErrorInternal              = "internal_error"
	ErrorUnavailable           = "unavailable"
	ErrorCursorStale           = "cursor_stale"
	ErrorStateConflict         = "state_conflict"
	ErrorDependencyUnavailable = "dependency_unavailable"
	ErrorQueryBudgetExceeded   = "query_budget_exceeded"
	ErrorFlowCookieInvalid     = "flow_cookie_invalid"
	ErrorFlowNotFound          = "flow_not_found"
	ErrorFlowExpired           = "flow_expired"

	ErrorKnowledgeInvalidRequest         = "knowledge_invalid_request"
	ErrorKnowledgeNotFound               = "knowledge_not_found"
	ErrorKnowledgePermissionDenied       = "knowledge_permission_denied"
	ErrorKnowledgeConflict               = "knowledge_conflict"
	ErrorKnowledgeDependencyUnavailable  = "knowledge_dependency_unavailable"
	ErrorKnowledgeIngestFailed           = "knowledge_ingest_failed"
	ErrorKnowledgeVectorStoreUnavailable = "knowledge_vector_store_unavailable"
	ErrorKnowledgeEmbeddingFailed        = "knowledge_embedding_failed"
	ErrorKnowledgeDocumentUnsupported    = "knowledge_document_unsupported"
	ErrorKnowledgeDeleteFailed           = "knowledge_delete_failed"

	ErrorMemoryInvalidRequest        = "memory_invalid_request"
	ErrorMemoryNotFound              = "memory_not_found"
	ErrorMemoryPermissionDenied      = "memory_permission_denied"
	ErrorMemoryConflict              = "memory_conflict"
	ErrorMemoryDependencyUnavailable = "memory_dependency_unavailable"

	ErrorMemoryDistillationInvalidRequest        = "memory_distillation_invalid_request"
	ErrorMemoryDistillationPermissionDenied      = "memory_distillation_permission_denied"
	ErrorMemoryDistillationNoCandidates          = "memory_distillation_no_candidates"
	ErrorMemoryDistillationDependencyUnavailable = "memory_distillation_dependency_unavailable"
)

func StableErrorCodes() map[string]bool {
	return map[string]bool{
		ErrorInvalidRequest:        true,
		ErrorUnauthorized:          true,
		ErrorForbidden:             true,
		ErrorNotFound:              true,
		ErrorConflict:              true,
		ErrorFailedPrecondition:    true,
		ErrorVersionConflict:       true,
		ErrorIdempotencyConflict:   true,
		ErrorRateLimited:           true,
		ErrorResourceUnavailable:   true,
		ErrorAppRuleDenied:         true,
		ErrorRuntimeUnavailable:    true,
		ErrorHTTPSRequired:         true,
		ErrorInternal:              true,
		ErrorUnavailable:           true,
		ErrorCursorStale:           true,
		ErrorStateConflict:         true,
		ErrorDependencyUnavailable: true,
		ErrorQueryBudgetExceeded:   true,
		ErrorFlowCookieInvalid:     true,
		ErrorFlowNotFound:          true,
		ErrorFlowExpired:           true,

		ErrorKnowledgeInvalidRequest:         true,
		ErrorKnowledgeNotFound:               true,
		ErrorKnowledgePermissionDenied:       true,
		ErrorKnowledgeConflict:               true,
		ErrorKnowledgeDependencyUnavailable:  true,
		ErrorKnowledgeIngestFailed:           true,
		ErrorKnowledgeVectorStoreUnavailable: true,
		ErrorKnowledgeEmbeddingFailed:        true,
		ErrorKnowledgeDocumentUnsupported:    true,
		ErrorKnowledgeDeleteFailed:           true,

		ErrorMemoryInvalidRequest:        true,
		ErrorMemoryNotFound:              true,
		ErrorMemoryPermissionDenied:      true,
		ErrorMemoryConflict:              true,
		ErrorMemoryDependencyUnavailable: true,

		ErrorMemoryDistillationInvalidRequest:        true,
		ErrorMemoryDistillationPermissionDenied:      true,
		ErrorMemoryDistillationNoCandidates:          true,
		ErrorMemoryDistillationDependencyUnavailable: true,
	}
}
