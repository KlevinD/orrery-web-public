package httptransport

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"orrery_v2/internal/app/accesscontrol"
	"orrery_v2/internal/app/actioncontract"
	authorizationapp "orrery_v2/internal/app/authorization"
	developmentproject "orrery_v2/internal/app/developmentproject"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/app/knowledgeprovider"
	"orrery_v2/internal/app/memoryadmin"
	"orrery_v2/internal/app/memorydistill"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/ontologyreasoning"
	"orrery_v2/internal/app/operations"
	projectadmin "orrery_v2/internal/app/projectadmin"
	resourceadmin "orrery_v2/internal/app/resourceadmin"
	"orrery_v2/internal/app/responsibility"
	"orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/app/scmconnection"
	"orrery_v2/internal/app/todo"
	"orrery_v2/internal/app/userauth"
	knowledgecore "orrery_v2/internal/knowledge/core"
	knowledgevector "orrery_v2/internal/knowledge/vector"
	missiondiscussion "orrery_v2/internal/mission/discussion"
	missiontask "orrery_v2/internal/mission/task"
	doccore "orrery_v2/internal/platform/document/core"
	"orrery_v2/internal/retrievalevidence"
)

func MapError(err error, correlationID string) (int, ErrorResponse) {
	if err == nil {
		return http.StatusOK, ErrorResponse{}
	}
	response := ErrorResponse{
		cause:         err,
		Code:          ErrorInternal,
		Message:       "internal server error",
		CorrelationID: correlationID,
		Retryable:     false,
	}
	if errors.Is(err, retrievalevidence.ErrInputChanged) {
		response.Code, response.Message, response.Retryable = ErrorConflict, retrievalevidence.ErrInputChanged.Error(), true
		return http.StatusConflict, response
	}
	if status, mapped, ok := mapKnowledgeError(err, response); ok {
		return status, mapped
	}
	if status, mapped, ok := mapMemoryError(err, response); ok {
		return status, mapped
	}
	if isJSONDecodeError(err) {
		response.Code = ErrorInvalidRequest
		response.Message = "request body is invalid"
		response.Details = map[string]string{"field": "body"}
		return http.StatusBadRequest, response
	}
	if errors.Is(err, identity.ErrFlowCookieInvalid) {
		response.Code = ErrorFlowCookieInvalid
		response.Message = "login flow cookie is invalid"
		return http.StatusForbidden, response
	}
	if errors.Is(err, identity.ErrFlowNotFound) {
		response.Code = ErrorFlowNotFound
		response.Message = "login flow was not found"
		return http.StatusNotFound, response
	}
	if errors.Is(err, identity.ErrFlowExpired) {
		response.Code = ErrorFlowExpired
		response.Message = "login flow expired"
		return http.StatusGone, response
	}
	if errors.Is(err, identity.ErrServiceUnavailable) {
		response.Code = ErrorResourceUnavailable
		response.Message = "identity service unavailable"
		response.Retryable = true
		return http.StatusServiceUnavailable, response
	}
	if errors.Is(err, identity.ErrConflict) {
		response.Code = ErrorConflict
		response.Message = "identity is already bound to another account"
		return http.StatusConflict, response
	}
	if errors.Is(err, identity.ErrInvalidRequest) {
		response.Code = ErrorInvalidRequest
		response.Message = err.Error()
		return http.StatusBadRequest, response
	}
	if errors.Is(err, identity.ErrUnauthorized) {
		response.Code = ErrorUnauthorized
		response.Message = "authorization is required"
		return http.StatusUnauthorized, response
	}
	if status, ok := userauthStatus(err); ok {
		response.Message = err.Error()
		switch status {
		case http.StatusBadRequest:
			response.Code = ErrorInvalidRequest
		case http.StatusUnauthorized:
			response.Code = ErrorUnauthorized
			response.Message = "authorization is required"
		case http.StatusTooManyRequests:
			response.Code = ErrorRateLimited
			response.Message = err.Error()
		case http.StatusPreconditionRequired:
			response.Code = ErrorFailedPrecondition
			response.Message = err.Error()
		case http.StatusServiceUnavailable:
			response.Code = ErrorResourceUnavailable
			response.Message = "service dependency unavailable"
			response.Retryable = true
		}
		return status, response
	}
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, missioncontrol.ErrInvalidViewer), errors.Is(err, resourceadmin.ErrInvalidViewer), errors.Is(err, userauth.ErrUnauthorized):
		status = http.StatusUnauthorized
		response.Code = ErrorUnauthorized
		response.Message = "authorization is required"
	case errors.Is(err, resourceadmin.ErrPermissionDenied), errors.Is(err, operations.ErrPermissionDenied), errors.Is(err, missiondiscussion.ErrPermissionDenied), errors.Is(err, discussionsession.ErrForbidden), errors.Is(err, authorizationapp.ErrDenied), errors.Is(err, todo.ErrForbidden), errors.Is(err, hil.ErrParticipantRequired):
		status = http.StatusForbidden
		response.Code = ErrorForbidden
		response.Message = "permission denied"
	case errors.Is(err, missioncontrol.ErrProjectionNotFound), errors.Is(err, missiontask.ErrNotFound), errors.Is(err, operations.ErrNotFound), errors.Is(err, projectadmin.ErrNotFound), errors.Is(err, developmentproject.ErrNotFound), errors.Is(err, scmconnection.ErrNotFound), errors.Is(err, discussionsession.ErrNotFound), errors.Is(err, missiondiscussion.ErrNotFound), errors.Is(err, resourceadmin.ErrNotFound), errors.Is(err, responsibility.ErrNotFound), errors.Is(err, ontology.ErrNotFound), errors.Is(err, ontologyreasoning.ErrNotFound), errors.Is(err, actioncontract.ErrNotFound), errors.Is(err, hil.ErrNotFound):
		status = http.StatusNotFound
		response.Code = ErrorNotFound
		response.Message = "resource not found"
	case errors.Is(err, scheduler.ErrNotFound), errors.Is(err, todo.ErrNotFound):
		status = http.StatusNotFound
		response.Code = ErrorNotFound
		response.Message = "resource not found"
	case errors.Is(err, missioncontrol.ErrInvalidRequest), errors.Is(err, resourceadmin.ErrInvalidRequest), errors.Is(err, operations.ErrInvalidRequest), errors.Is(err, projectadmin.ErrInvalidRequest), errors.Is(err, developmentproject.ErrInvalidRequest), errors.Is(err, scmconnection.ErrInvalidRequest), errors.Is(err, discussionsession.ErrInvalidRequest), errors.Is(err, discussionsession.ErrConversationMember), errors.Is(err, discussionsession.ErrOrchestrationIncomplete), errors.Is(err, missiondiscussion.ErrInvalidArgument), errors.Is(err, accesscontrol.ErrInvalidRequest), errors.Is(err, authorizationapp.ErrInvalidRequest), errors.Is(err, scheduler.ErrInvalidRequest), errors.Is(err, scheduler.ErrInvalidSchedule), errors.Is(err, scheduler.ErrInvalidTarget), errors.Is(err, todo.ErrInvalidRequest), errors.Is(err, todo.ErrInvalidTransition), errors.Is(err, responsibility.ErrInvalidContract), errors.Is(err, responsibility.ErrInvalidTransition), errors.Is(err, ontology.ErrInvalidDefinition), errors.Is(err, ontologyreasoning.ErrInvalidRequest), errors.Is(err, ontologyreasoning.ErrValidationFailed), errors.Is(err, actioncontract.ErrInvalidRequest), errors.Is(err, hil.ErrInvalidRequest):
		status = http.StatusBadRequest
		response.Code = ErrorInvalidRequest
		response.Message = err.Error()
	case errors.Is(err, ontology.ErrVersionConflict), errors.Is(err, operations.ErrVersionConflict):
		status = http.StatusConflict
		response.Code = ErrorVersionConflict
		response.Message = err.Error()
	case errors.Is(err, todo.ErrConflict):
		status = http.StatusConflict
		response.Code = ErrorVersionConflict
		response.Message = err.Error()
		response.Retryable = true
	case errors.Is(err, missioncontrol.ErrConflict), errors.Is(err, missioncontrol.ErrAgentRunAlreadyActive):
		status = http.StatusConflict
		response.Code = ErrorConflict
		response.Message = err.Error()
	case errors.Is(err, hil.ErrIdempotencyConflict):
		status = http.StatusConflict
		response.Code = ErrorIdempotencyConflict
		response.Message = err.Error()
	case errors.Is(err, hil.ErrConflict), errors.Is(err, hil.ErrVoteImmutable):
		status = http.StatusConflict
		response.Code = ErrorConflict
		response.Message = err.Error()
	case errors.Is(err, responsibility.ErrConflict), errors.Is(err, responsibility.ErrCapacityLimit):
		status = http.StatusConflict
		response.Code = ErrorConflict
		response.Message = err.Error()
		response.Retryable = errors.Is(err, responsibility.ErrCapacityLimit)
	case errors.Is(err, developmentproject.ErrConflict), errors.Is(err, developmentproject.ErrArchived), errors.Is(err, developmentproject.ErrRepositoryPolicy), errors.Is(err, developmentproject.ErrWorkspaceLease), errors.Is(err, developmentproject.ErrWorkspaceConflict), errors.Is(err, developmentproject.ErrInvalidState), errors.Is(err, developmentproject.ErrApprovalStale), errors.Is(err, scmconnection.ErrConflict):
		status = http.StatusConflict
		response.Code = ErrorVersionConflict
		response.Message = err.Error()
		response.Retryable = errors.Is(err, developmentproject.ErrConflict) || errors.Is(err, scmconnection.ErrConflict)
	case errors.Is(err, developmentproject.ErrMergeForbidden):
		status = http.StatusForbidden
		response.Code = ErrorForbidden
		response.Message = err.Error()
	case errors.Is(err, scmconnection.ErrVerificationFailed):
		status = http.StatusUnprocessableEntity
		response.Code = ErrorFailedPrecondition
		response.Message = "SCM connection verification failed"
	case errors.Is(err, ontology.ErrIdempotencyConflict):
		status = http.StatusConflict
		response.Code = ErrorIdempotencyConflict
		response.Message = err.Error()
	case errors.Is(err, developmentproject.ErrIdempotency), errors.Is(err, scmconnection.ErrIdempotency):
		status = http.StatusConflict
		response.Code = ErrorIdempotencyConflict
		response.Message = "idempotency key was already used for a different request"
	case errors.Is(err, discussionsession.ErrRevisionConflict), errors.Is(err, scheduler.ErrRevisionConflict):
		status = http.StatusConflict
		response.Code = ErrorVersionConflict
		response.Message = err.Error()
		response.Retryable = true
	case errors.Is(err, discussionsession.ErrIdempotencyMismatch):
		status = http.StatusConflict
		response.Code = ErrorIdempotencyConflict
		response.Message = err.Error()
	case errors.Is(err, discussionsession.ErrCommandInProgress):
		status = http.StatusConflict
		response.Code = ErrorConflict
		response.Message = err.Error()
		response.Retryable = true
	case errors.Is(err, discussionsession.ErrRuntimeApprovalPending):
		status = http.StatusConflict
		response.Code = ErrorConflict
		response.Message = err.Error()
	case errors.Is(err, discussionsession.ErrRunNotActive), errors.Is(err, discussionsession.ErrRuntimeControl):
		status = http.StatusConflict
		response.Code = ErrorConflict
		response.Message = err.Error()
	case errors.Is(err, resourceadmin.ErrConflict):
		status = http.StatusConflict
		response.Code = ErrorConflict
		response.Message = err.Error()
	case errors.Is(err, missiondiscussion.ErrConflict):
		status = http.StatusConflict
		response.Code = ErrorConflict
		response.Message = err.Error()
	case errors.Is(err, discussionsession.ErrRuntimeUnavailable):
		status = http.StatusServiceUnavailable
		response.Code = ErrorRuntimeUnavailable
		response.Message = "discussion runtime is not configured"
		response.Details = discussionRuntimeUnavailableDetails(err)
		response.Retryable = true
	case errors.Is(err, missioncontrol.ErrServiceDependency), errors.Is(err, resourceadmin.ErrServiceDependency), errors.Is(err, operations.ErrServiceDependency), errors.Is(err, projectadmin.ErrServiceDependency), errors.Is(err, developmentproject.ErrDependency), errors.Is(err, scmconnection.ErrDependency), errors.Is(err, discussionsession.ErrServiceDependency), errors.Is(err, accesscontrol.ErrServiceDependency), errors.Is(err, authorizationapp.ErrServiceDependency), errors.Is(err, identity.ErrServiceDependency), errors.Is(err, scheduler.ErrServiceDependency), errors.Is(err, todo.ErrServiceDependency), errors.Is(err, hil.ErrContextUnavailable):
		status = http.StatusServiceUnavailable
		response.Code = ErrorResourceUnavailable
		response.Message = "service dependency unavailable"
		response.Retryable = true
	default:
		var validation missiontask.ValidationError
		var conflict missiontask.ConflictError
		var resultValidation missiontask.ResultValidationError
		switch {
		case errors.As(err, &validation):
			status = http.StatusBadRequest
			response.Code = ErrorInvalidRequest
			response.Message = validation.Message
			response.Details = map[string]string{"field": validation.Field, "validation_code": string(validation.Code)}
			response.Retryable = validation.Retryable
		case errors.As(err, &conflict):
			status = http.StatusConflict
			response.Code = ErrorConflict
			response.Message = conflict.Message
			response.Details = map[string]string{"field": conflict.Field, "conflict_code": conflict.ConflictCode}
			response.Retryable = conflict.Retryable
		case errors.As(err, &resultValidation):
			status = http.StatusBadRequest
			response.Code = ErrorAppRuleDenied
			response.Message = resultValidation.Error()
		}
	}
	return status, response
}

func isJSONDecodeError(err error) bool {
	if err == nil {
		return false
	}
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		return true
	}
	var unmarshalType *json.UnmarshalTypeError
	if errors.As(err, &unmarshalType) {
		return true
	}
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

func discussionRuntimeUnavailableDetails(err error) map[string]string {
	details := map[string]string{
		"setup_action":  "configure_discussion_runtime",
		"settings_path": "/settings",
		"guidance":      "Configure a discussion runtime before starting a chat. Open Settings, add or enable an LLM profile with provider, base URL, model, and API key, then select a discussion runtime profile.",
		"env_guidance":  "For local CLI runtimes, install the runtime and configure the discussion runtime in Settings or discussion.agent in the selected YAML profile.",
	}
	var setup discussionsession.RuntimeSetupError
	if errors.As(err, &setup) {
		if runtimeKind := strings.TrimSpace(setup.RuntimeKind); runtimeKind != "" {
			details["runtime_kind"] = runtimeKind
		}
		if cause := strings.TrimSpace(setup.Cause); cause != "" {
			details["cause"] = cause
		}
		return details
	}
	if message := strings.TrimSpace(err.Error()); message != "" && message != discussionsession.ErrRuntimeUnavailable.Error() {
		details["cause"] = message
	}
	return details
}

func mapMemoryError(err error, response ErrorResponse) (int, ErrorResponse, bool) {
	switch {
	case errors.Is(err, memorydistill.ErrInvalidViewer):
		response.Code = ErrorUnauthorized
		response.Message = "authorization is required"
		return http.StatusUnauthorized, response, true
	case errors.Is(err, memorydistill.ErrInvalidRequest):
		response.Code = ErrorMemoryDistillationInvalidRequest
		response.Message = err.Error()
		return http.StatusBadRequest, response, true
	case errors.Is(err, memorydistill.ErrPermissionDenied):
		response.Code = ErrorMemoryDistillationPermissionDenied
		response.Message = "memory distillation permission denied"
		return http.StatusForbidden, response, true
	case errors.Is(err, memorydistill.ErrNoCandidates):
		response.Code = ErrorMemoryDistillationNoCandidates
		response.Message = "memory distillation produced no candidates"
		return http.StatusUnprocessableEntity, response, true
	case errors.Is(err, memorydistill.ErrServiceDependency):
		response.Code = ErrorMemoryDistillationDependencyUnavailable
		response.Message = "memory distillation dependency unavailable"
		response.Retryable = true
		return http.StatusServiceUnavailable, response, true
	case errors.Is(err, memoryadmin.ErrInvalidViewer):
		response.Code = ErrorUnauthorized
		response.Message = "authorization is required"
		return http.StatusUnauthorized, response, true
	case errors.Is(err, memoryadmin.ErrInvalidRequest):
		response.Code = ErrorMemoryInvalidRequest
		response.Message = err.Error()
		return http.StatusBadRequest, response, true
	case errors.Is(err, memoryadmin.ErrPermissionDenied):
		response.Code = ErrorMemoryPermissionDenied
		response.Message = "memory permission denied"
		return http.StatusForbidden, response, true
	case errors.Is(err, memoryadmin.ErrNotFound):
		response.Code = ErrorMemoryNotFound
		response.Message = "memory resource not found"
		return http.StatusNotFound, response, true
	case errors.Is(err, memoryadmin.ErrConflict):
		response.Code = ErrorMemoryConflict
		response.Message = err.Error()
		return http.StatusConflict, response, true
	case errors.Is(err, memoryadmin.ErrServiceDependency):
		response.Code = ErrorMemoryDependencyUnavailable
		response.Message = "memory dependency unavailable"
		response.Retryable = true
		return http.StatusServiceUnavailable, response, true
	default:
		return 0, ErrorResponse{}, false
	}
}

func mapKnowledgeError(err error, response ErrorResponse) (int, ErrorResponse, bool) {
	switch {
	case errors.Is(err, knowledgeprovider.ErrInvalid):
		response.Code = ErrorKnowledgeInvalidRequest
		response.Message = err.Error()
		return http.StatusBadRequest, response, true
	case errors.Is(err, knowledgeprovider.ErrNotFound):
		response.Code = ErrorKnowledgeNotFound
		response.Message = "knowledge provider not found"
		return http.StatusNotFound, response, true
	case errors.Is(err, knowledgeprovider.ErrAlreadyExists):
		response.Code = ErrorKnowledgeConflict
		response.Message = err.Error()
		return http.StatusConflict, response, true
	case errors.Is(err, knowledgeprovider.ErrDependencyUnavailable):
		response.Code = ErrorKnowledgeDependencyUnavailable
		response.Message = "knowledge provider dependency unavailable"
		response.Retryable = true
		return http.StatusServiceUnavailable, response, true
	case errors.Is(err, knowledgeadmin.ErrInvalidViewer):
		response.Code = ErrorKnowledgeInvalidRequest
		response.Message = "authorization is required"
		return http.StatusUnauthorized, response, true
	case errors.Is(err, knowledgeadmin.ErrInvalidRequest), errors.Is(err, knowledgecore.ErrInvalid):
		response.Code = ErrorKnowledgeInvalidRequest
		response.Message = err.Error()
		return http.StatusBadRequest, response, true
	case errors.Is(err, knowledgecore.ErrNotFound):
		response.Code = ErrorKnowledgeNotFound
		response.Message = "knowledge resource not found"
		return http.StatusNotFound, response, true
	case errors.Is(err, knowledgeadmin.ErrPermissionDenied), errors.Is(err, knowledgecore.ErrPermissionDenied):
		response.Code = ErrorKnowledgePermissionDenied
		response.Message = "knowledge permission denied"
		return http.StatusForbidden, response, true
	case errors.Is(err, knowledgecore.ErrConflict):
		response.Code = ErrorKnowledgeConflict
		response.Message = err.Error()
		return http.StatusConflict, response, true
	case errors.Is(err, knowledgeadmin.ErrServiceDependency), errors.Is(err, knowledgecore.ErrDependencyUnavailable):
		response.Code = ErrorKnowledgeDependencyUnavailable
		response.Message = "knowledge dependency unavailable"
		response.Retryable = true
		return http.StatusServiceUnavailable, response, true
	case errors.Is(err, knowledgecore.ErrVectorStoreUnavailable), errors.Is(err, knowledgevector.ErrStoreUnavailable), errors.Is(err, knowledgevector.ErrUnsupportedSearch), errors.Is(err, knowledgevector.ErrUnsafePath):
		response.Code = ErrorKnowledgeVectorStoreUnavailable
		response.Message = "knowledge vector store unavailable"
		response.Retryable = true
		return http.StatusServiceUnavailable, response, true
	case errors.Is(err, knowledgecore.ErrEmbeddingFailed):
		response.Code = ErrorKnowledgeEmbeddingFailed
		response.Message = "knowledge embedding failed"
		response.Retryable = true
		return http.StatusBadGateway, response, true
	case errors.Is(err, knowledgecore.ErrDocumentUnsupported), errors.Is(err, doccore.ErrUnsupported):
		response.Code = ErrorKnowledgeDocumentUnsupported
		response.Message = "knowledge document unsupported"
		return http.StatusUnsupportedMediaType, response, true
	case errors.Is(err, knowledgecore.ErrDeleteFailed):
		response.Code = ErrorKnowledgeDeleteFailed
		response.Message = "knowledge delete failed"
		response.Retryable = true
		return http.StatusServiceUnavailable, response, true
	case errors.Is(err, knowledgecore.ErrIngestFailed):
		response.Code = ErrorKnowledgeIngestFailed
		response.Message = "knowledge ingest failed"
		return http.StatusInternalServerError, response, true
	default:
		return 0, ErrorResponse{}, false
	}
}

func invalidRequest(correlationID, field, message string) (int, ErrorResponse) {
	return http.StatusBadRequest, ErrorResponse{
		Code:          ErrorInvalidRequest,
		Message:       message,
		Details:       map[string]string{"field": field},
		CorrelationID: correlationID,
	}
}

func failedPrecondition(correlationID, field, reason, message string) (int, ErrorResponse) {
	return http.StatusConflict, ErrorResponse{
		Code:          ErrorFailedPrecondition,
		Message:       message,
		Details:       map[string]string{"field": field, "reason": reason},
		CorrelationID: correlationID,
	}
}
