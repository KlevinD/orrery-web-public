package projection

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

var replayMutationFields = []string{"details", "reason_code", "source_event_id", "source_event_type", "status"}
var acknowledgementMutationFields = []string{"reason_code", "status"}

type CommandService struct {
	gate       *ontologyscope.ScopeAccessGate
	audits     AuditCommandStore
	visibility VisibilityPolicy
	executor   CommandExecutor
}

func NewCommandService(gate *ontologyscope.ScopeAccessGate, audits AuditCommandStore, visibility VisibilityPolicy, executor CommandExecutor) (*CommandService, error) {
	if gate == nil || audits == nil || visibility == nil || executor == nil {
		return nil, ontologyscope.ErrDependencyUnavailable
	}
	return &CommandService{gate: gate, audits: audits, visibility: visibility, executor: executor}, nil
}

func (s *CommandService) Replay(ctx context.Context, principal identity.Principal, request ReplayRequest) (MutationResult, error) {
	request.TenantID = strings.TrimSpace(request.TenantID)
	request.AuditID = strings.TrimSpace(request.AuditID)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if !validID(request.TenantID) || !validID(request.AuditID) || !validProjectionIdempotencyKey(request.IdempotencyKey) {
		return MutationResult{}, ontologycontract.ErrInvalidRequest
	}
	principal, actor, err := projectionCommandPrincipal(principal)
	if err != nil {
		return MutationResult{}, err
	}
	authorized, err := s.authorizeCurrentAudit(ctx, principal, request.TenantID, request.AuditID, "ontology.projection.replay", replayMutationFields)
	if err != nil {
		return MutationResult{}, err
	}
	audit := authorized.audit
	requestHash := projectionCommandRequestHash(contextgraph.ProjectionAuditCommandReplay, "", authorization.PrincipalCacheKey(principal))
	claim, err := s.audits.ClaimProjectionAuditCommand(ctx, contextgraph.ProjectionAuditCommandClaimRequest{
		Audit: audit, Operation: contextgraph.ProjectionAuditCommandReplay, IdempotencyKey: request.IdempotencyKey, RequestHash: requestHash,
	})
	if err != nil {
		return MutationResult{}, projectionCommandStoreError(err)
	}
	if !claim.Acquired {
		return projectionMutationResultFromClaim(audit.ID, claim)
	}
	if _, err := s.reauthorizeClaimedAudit(ctx, principal, audit, "ontology.projection.replay", replayMutationFields); err != nil {
		return MutationResult{}, s.abortClaimedCommand(ctx, audit, claim.LeaseToken, contextgraph.ProjectionAuditCommandReplay, request.IdempotencyKey, requestHash, err)
	}
	prepared, err := s.executor.PrepareProjectionAuditReplay(ctx, audit, actor)
	if err != nil {
		if failErr := s.failClaimedCommand(ctx, principal, audit, actor, claim.LeaseToken, contextgraph.ProjectionAuditCommandReplay, request.IdempotencyKey, requestHash, err); failErr != nil {
			return MutationResult{}, failErr
		}
		return MutationResult{}, projectionCommandDependencyError(err)
	}
	result := prepared.Result
	if prepared.Effect == nil {
		err := errors.New("projection executor returned no transactional effect")
		if failErr := s.failClaimedCommand(ctx, principal, audit, actor, claim.LeaseToken, contextgraph.ProjectionAuditCommandReplay, request.IdempotencyKey, requestHash, err); failErr != nil {
			return MutationResult{}, failErr
		}
		return MutationResult{}, projectionCommandDependencyError(err)
	}
	safe, err := safeProjectionMutationResult(audit, result, "replayed")
	if err != nil {
		if failErr := s.failClaimedCommand(ctx, principal, audit, actor, claim.LeaseToken, contextgraph.ProjectionAuditCommandReplay, request.IdempotencyKey, requestHash, err); failErr != nil {
			return MutationResult{}, failErr
		}
		return MutationResult{}, err
	}
	fence, err := s.reauthorizeClaimedAudit(ctx, principal, audit, "ontology.projection.replay", replayMutationFields)
	if err != nil {
		return MutationResult{}, s.abortClaimedCommand(ctx, audit, claim.LeaseToken, contextgraph.ProjectionAuditCommandReplay, request.IdempotencyKey, requestHash, err)
	}
	if err := s.audits.CompleteProjectionAuditCommand(ctx, contextgraph.ProjectionAuditCommandCompletion{
		TenantID: audit.TenantID, AuditID: audit.ID, Operation: contextgraph.ProjectionAuditCommandReplay,
		IdempotencyKey: request.IdempotencyKey, RequestHash: requestHash, LeaseToken: claim.LeaseToken, Result: result,
		History: projectionCommandReplayHistory(audit, result, actor, request.IdempotencyKey, requestHash, result.Status, ""),
		Effect:  prepared.Effect,
		Fence:   fence,
	}); err != nil {
		if errors.Is(err, contextgraph.ErrProjectionAuditCommandFence) {
			return MutationResult{}, s.abortClaimedCommand(ctx, audit, claim.LeaseToken, contextgraph.ProjectionAuditCommandReplay, request.IdempotencyKey, requestHash, fmt.Errorf("%w: projection authorization changed before commit", ontologycontract.ErrStateConflict))
		}
		if errors.Is(err, contextgraph.ErrProjectionAuditCommandEffect) {
			if failErr := s.failClaimedCommand(ctx, principal, audit, actor, claim.LeaseToken, contextgraph.ProjectionAuditCommandReplay, request.IdempotencyKey, requestHash, err); failErr != nil {
				return MutationResult{}, failErr
			}
		}
		return MutationResult{}, projectionCommandStoreError(err)
	}
	return safe, nil
}

func (s *CommandService) Acknowledge(ctx context.Context, principal identity.Principal, request AcknowledgeRequest) (MutationResult, error) {
	request.TenantID = strings.TrimSpace(request.TenantID)
	request.AuditID = strings.TrimSpace(request.AuditID)
	request.Reason = strings.TrimSpace(request.Reason)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if !validID(request.TenantID) || !validID(request.AuditID) || request.Reason == "" || !utf8.ValidString(request.Reason) || utf8.RuneCountInString(request.Reason) > 2000 || !validProjectionIdempotencyKey(request.IdempotencyKey) {
		return MutationResult{}, ontologycontract.ErrInvalidRequest
	}
	principal, actor, err := projectionCommandPrincipal(principal)
	if err != nil {
		return MutationResult{}, err
	}
	authorized, err := s.authorizeCurrentAudit(ctx, principal, request.TenantID, request.AuditID, "ontology.projection.acknowledge", acknowledgementMutationFields)
	if err != nil {
		return MutationResult{}, err
	}
	audit := authorized.audit
	requestHash := projectionCommandRequestHash(contextgraph.ProjectionAuditCommandAcknowledge, request.Reason, authorization.PrincipalCacheKey(principal))
	claim, err := s.audits.ClaimProjectionAuditCommand(ctx, contextgraph.ProjectionAuditCommandClaimRequest{
		Audit: audit, Operation: contextgraph.ProjectionAuditCommandAcknowledge, IdempotencyKey: request.IdempotencyKey, RequestHash: requestHash,
	})
	if err != nil {
		return MutationResult{}, projectionCommandStoreError(err)
	}
	if !claim.Acquired {
		return projectionMutationResultFromClaim(audit.ID, claim)
	}
	if _, err := s.reauthorizeClaimedAudit(ctx, principal, audit, "ontology.projection.acknowledge", acknowledgementMutationFields); err != nil {
		return MutationResult{}, s.abortClaimedCommand(ctx, audit, claim.LeaseToken, contextgraph.ProjectionAuditCommandAcknowledge, request.IdempotencyKey, requestHash, err)
	}
	result, err := s.executor.AcknowledgeProjectionAudit(ctx, audit, actor, request.Reason)
	if err != nil {
		if failErr := s.failClaimedCommand(ctx, principal, audit, actor, claim.LeaseToken, contextgraph.ProjectionAuditCommandAcknowledge, request.IdempotencyKey, requestHash, err); failErr != nil {
			return MutationResult{}, failErr
		}
		return MutationResult{}, projectionCommandDependencyError(err)
	}
	safe, err := safeProjectionMutationResult(audit, result, "acknowledged")
	if err != nil {
		if failErr := s.failClaimedCommand(ctx, principal, audit, actor, claim.LeaseToken, contextgraph.ProjectionAuditCommandAcknowledge, request.IdempotencyKey, requestHash, err); failErr != nil {
			return MutationResult{}, failErr
		}
		return MutationResult{}, err
	}
	fence, err := s.reauthorizeClaimedAudit(ctx, principal, audit, "ontology.projection.acknowledge", acknowledgementMutationFields)
	if err != nil {
		return MutationResult{}, s.abortClaimedCommand(ctx, audit, claim.LeaseToken, contextgraph.ProjectionAuditCommandAcknowledge, request.IdempotencyKey, requestHash, err)
	}
	if err := s.audits.CompleteProjectionAuditCommand(ctx, contextgraph.ProjectionAuditCommandCompletion{
		TenantID: audit.TenantID, AuditID: audit.ID, Operation: contextgraph.ProjectionAuditCommandAcknowledge,
		IdempotencyKey: request.IdempotencyKey, RequestHash: requestHash, LeaseToken: claim.LeaseToken, Result: result,
		Fence: fence,
	}); err != nil {
		if errors.Is(err, contextgraph.ErrProjectionAuditCommandFence) {
			return MutationResult{}, s.abortClaimedCommand(ctx, audit, claim.LeaseToken, contextgraph.ProjectionAuditCommandAcknowledge, request.IdempotencyKey, requestHash, fmt.Errorf("%w: projection authorization changed before commit", ontologycontract.ErrStateConflict))
		}
		return MutationResult{}, projectionCommandStoreError(err)
	}
	return safe, nil
}

func (s *CommandService) reauthorizeClaimedAudit(
	ctx context.Context,
	principal identity.Principal,
	expected contextgraph.ProjectionAudit,
	purpose string,
	fields []string,
) (contextgraph.ProjectionAuditCommandFence, error) {
	current, err := s.authorizeCurrentAudit(ctx, principal, expected.TenantID, expected.ID, purpose, fields)
	if err != nil {
		return contextgraph.ProjectionAuditCommandFence{}, err
	}
	if !sameProjectionAuditRecord(expected, current.audit) {
		return contextgraph.ProjectionAuditCommandFence{}, fmt.Errorf("%w: projection audit changed before command commit", ontologycontract.ErrStateConflict)
	}
	return current.fence, nil
}

func (s *CommandService) failClaimedCommand(
	ctx context.Context,
	principal identity.Principal,
	audit contextgraph.ProjectionAudit,
	actor CommandActor,
	leaseToken string,
	operation string,
	idempotencyKey string,
	requestHash string,
	commandErr error,
) error {
	purpose, fields := "ontology.projection.replay", replayMutationFields
	if operation == contextgraph.ProjectionAuditCommandAcknowledge {
		purpose, fields = "ontology.projection.acknowledge", acknowledgementMutationFields
	}
	fence, err := s.reauthorizeClaimedAudit(ctx, principal, audit, purpose, fields)
	if err != nil {
		return s.abortClaimedCommand(ctx, audit, leaseToken, operation, idempotencyKey, requestHash, err)
	}
	failureCode := projectionCommandFailureCode(commandErr)
	failure := contextgraph.ProjectionAuditCommandFailure{
		TenantID: audit.TenantID, AuditID: audit.ID, Operation: operation, IdempotencyKey: idempotencyKey,
		RequestHash: requestHash, LeaseToken: leaseToken, FailureCode: failureCode, Fence: &fence,
	}
	if operation == contextgraph.ProjectionAuditCommandReplay {
		attemptedAt := time.Now().UTC()
		result := audit
		result.Details = maps.Clone(audit.Details)
		if result.Details == nil {
			result.Details = map[string]any{}
		}
		result.Details["last_replay_status"] = "failed"
		result.Details["last_replay_error_code"] = failureCode
		result.Details["last_replay_at"] = attemptedAt.Format(time.RFC3339Nano)
		result.Details["last_replay_attempts"] = projectionCommandAttemptCount(result.Details["last_replay_attempts"]) + 1
		failure.Result = &result
		failure.History = projectionCommandReplayHistory(audit, result, actor, idempotencyKey, requestHash, "failed", failureCode)
		failure.History.AttemptedAt = attemptedAt
	}
	err = s.audits.FailProjectionAuditCommand(ctx, failure)
	if err != nil {
		if errors.Is(err, contextgraph.ErrProjectionAuditCommandFence) {
			return s.abortClaimedCommand(ctx, audit, leaseToken, operation, idempotencyKey, requestHash, fmt.Errorf("%w: projection authorization changed before failure commit", ontologycontract.ErrStateConflict))
		}
		return projectionCommandStoreError(err)
	}
	return nil
}

func (s *CommandService) abortClaimedCommand(
	ctx context.Context,
	audit contextgraph.ProjectionAudit,
	leaseToken string,
	operation string,
	idempotencyKey string,
	requestHash string,
	commandErr error,
) error {
	failure := contextgraph.ProjectionAuditCommandFailure{
		TenantID: audit.TenantID, AuditID: audit.ID, Operation: operation, IdempotencyKey: idempotencyKey,
		RequestHash: requestHash, LeaseToken: leaseToken, FailureCode: projectionCommandFailureCode(commandErr),
	}
	if err := s.audits.FailProjectionAuditCommand(ctx, failure); err != nil {
		return projectionCommandStoreError(err)
	}
	return commandErr
}

func projectionCommandReplayHistory(audit, result contextgraph.ProjectionAudit, actor CommandActor, idempotencyKey, requestHash, status, failureCode string) *contextgraph.ProjectionAuditReplayHistory {
	sum := sha256.Sum256([]byte(audit.TenantID + "\x00" + audit.ID + "\x00" + idempotencyKey + "\x00" + requestHash))
	attemptedAt := result.ObservedAt
	if attemptedAt.IsZero() {
		attemptedAt = time.Now().UTC()
	}
	details := map[string]any{"audit_status": result.Status, "reason_code": result.ReasonCode}
	if actor.ID != "" {
		details["actor_id"] = actor.ID
	}
	if actor.Kind != "" {
		details["actor_kind"] = actor.Kind
	}
	if attempts, ok := result.Details["last_replay_attempts"]; ok {
		details["last_replay_attempts"] = attempts
	}
	return &contextgraph.ProjectionAuditReplayHistory{
		ID: "projection-command-history:" + hex.EncodeToString(sum[:]), AuditID: audit.ID, TenantID: audit.TenantID,
		SourceEventID: audit.SourceEventID, SourceEventType: audit.SourceEventType, Status: status, Error: failureCode,
		Details: details, SourceAccessRef: audit.SourceAccessRef, AccessState: contextgraph.AccessStateResolved, AttemptedAt: attemptedAt,
	}
}

func projectionCommandAttemptCount(raw any) int64 {
	switch value := raw.(type) {
	case int:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	default:
		return 0
	}
}

type authorizedProjectionAudit struct {
	audit contextgraph.ProjectionAudit
	fence contextgraph.ProjectionAuditCommandFence
}

func (s *CommandService) authorizeCurrentAudit(
	ctx context.Context,
	principal identity.Principal,
	tenantID string,
	auditID string,
	purpose string,
	fields []string,
) (authorizedProjectionAudit, error) {
	authorized, err := s.gate.AuthorizeManage(ctx, principal, tenantID)
	if err != nil {
		return authorizedProjectionAudit{}, err
	}
	audit, err := s.loadAudit(ctx, tenantID, auditID)
	if err != nil {
		return authorizedProjectionAudit{}, err
	}
	if err := s.authorizeAuditPolicy(ctx, principal, authorized, audit, purpose, fields); err != nil {
		return authorizedProjectionAudit{}, err
	}

	fresh, err := s.gate.AuthorizeManage(ctx, principal, tenantID)
	if err != nil {
		return authorizedProjectionAudit{}, err
	}
	if !sameProjectionAuthorization(authorized, fresh) {
		return authorizedProjectionAudit{}, fmt.Errorf("%w: projection authorization changed during the command", ontologycontract.ErrStateConflict)
	}
	current, err := s.loadAudit(ctx, tenantID, auditID)
	if err != nil {
		return authorizedProjectionAudit{}, err
	}
	if !sameProjectionAuditRecord(audit, current) {
		return authorizedProjectionAudit{}, fmt.Errorf("%w: projection audit changed during the command", ontologycontract.ErrStateConflict)
	}
	if err := s.authorizeAuditPolicy(ctx, principal, fresh, current, purpose, fields); err != nil {
		return authorizedProjectionAudit{}, err
	}
	fence, err := projectionCommandFence(fresh)
	if err != nil {
		return authorizedProjectionAudit{}, err
	}
	return authorizedProjectionAudit{audit: current, fence: fence}, nil
}

func projectionCommandFence(authorized ontologyscope.AuthorizedScopeContext) (contextgraph.ProjectionAuditCommandFence, error) {
	binding, snapshot := authorized.Binding(), authorized.Snapshot()
	if binding.TenantID == "" || binding.BindingVersion <= 0 || binding.PersistedBindingVersion < 0 || binding.PersistedBindingVersion > binding.BindingVersion ||
		binding.PersistenceRequired && binding.PersistedBindingVersion == 0 ||
		!validProjectionCommandRevision(snapshot.AuthorizationRevision, "authorization:") || !validProjectionCommandRevision(snapshot.PolicyRevision, "policy:") {
		return contextgraph.ProjectionAuditCommandFence{}, ontologyscope.ErrDependencyUnavailable
	}
	return contextgraph.ProjectionAuditCommandFence{
		TenantID: binding.TenantID, BindingVersion: binding.BindingVersion,
		PersistedBindingVersion: binding.PersistedBindingVersion, PersistenceRequired: binding.PersistenceRequired,
		AuthorizationRevision: snapshot.AuthorizationRevision, PolicyRevision: snapshot.PolicyRevision,
	}, nil
}

func validProjectionCommandRevision(value, prefix string) bool {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	revision, err := strconv.ParseInt(strings.TrimPrefix(value, prefix), 10, 64)
	return err == nil && revision >= 0
}

func (s *CommandService) loadAudit(ctx context.Context, tenantID, auditID string) (contextgraph.ProjectionAudit, error) {
	audit, err := s.audits.GetProjectionAudit(ctx, tenantID, auditID)
	if errors.Is(err, contextgraph.ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
		return contextgraph.ProjectionAudit{}, ErrUnavailable
	}
	if err != nil {
		return contextgraph.ProjectionAudit{}, projectionCommandDependencyError(err)
	}
	audit.SourceAccessRef = contextgraph.NormalizeAccessRef(audit.SourceAccessRef)
	if audit.TenantID != tenantID || audit.ID != auditID || audit.AccessState != contextgraph.AccessStateResolved || !audit.SourceAccessRef.Resolved() || audit.ObservedAt.IsZero() {
		return contextgraph.ProjectionAudit{}, ErrUnavailable
	}
	return audit, nil
}

func (s *CommandService) authorizeAuditPolicy(
	ctx context.Context,
	principal identity.Principal,
	authorized ontologyscope.AuthorizedScopeContext,
	audit contextgraph.ProjectionAudit,
	purpose string,
	fields []string,
) error {
	descriptor := datavisibility.ResourceDescriptor{
		Key:        "projection-audit-command:" + audit.ID,
		Kind:       datavisibility.ResourceAudit,
		TenantID:   audit.TenantID,
		Type:       audit.SourceEventType,
		FieldPaths: append([]string(nil), fields...),
		AccessRefs: projectionAccessRefs(authorized, audit.SourceAccessRef),
		Payload:    audit,
	}
	decision, err := s.visibility.Evaluate(ctx, datavisibility.EvaluateRequest{
		Principal: identity.NormalizePrincipal(principal), Purpose: purpose, Resources: []datavisibility.ResourceDescriptor{descriptor},
	})
	if err != nil {
		return projectionCommandDependencyError(err)
	}
	resource, ok := decision.Resource(descriptor.Key)
	if !ok {
		return projectionCommandDependencyError(errors.New("projection command visibility decision is incomplete"))
	}
	if hiddenDecision(resource) || !projectionFieldsAllowed(resource, fields) {
		return ErrUnavailable
	}
	return nil
}

func projectionFieldsAllowed(decision datavisibility.ResourceDecision, fields []string) bool {
	allowed := allowedFields(decision)
	for _, field := range fields {
		if !allowed[field] {
			return false
		}
	}
	return true
}

func projectionCommandPrincipal(principal identity.Principal) (identity.Principal, CommandActor, error) {
	principal = identity.NormalizePrincipal(principal)
	if !principal.Authenticated() || authorization.PrincipalCacheKey(principal) == "" {
		return identity.Principal{}, CommandActor{}, fmt.Errorf("%w: authenticated principal is required", ontologyscope.ErrAccessDenied)
	}
	actor := CommandActor{Kind: strings.TrimSpace(principal.ViewerKind), ID: strings.TrimSpace(principal.Subject)}
	switch actor.Kind {
	case identity.ViewerKindUser:
		actor.ID = firstProjectionValue(principal.UserID, principal.Username, principal.Subject)
	case identity.ViewerKindAgent:
		actor.ID = firstProjectionValue(principal.AgentProfileID, principal.Subject, principal.UserID)
	case identity.ViewerKindSystem:
		actor.ID = firstProjectionValue(principal.Subject, principal.UserID, "system")
	default:
		return identity.Principal{}, CommandActor{}, fmt.Errorf("%w: projection actor kind is unsupported", ontologyscope.ErrAccessDenied)
	}
	if actor.ID == "" {
		return identity.Principal{}, CommandActor{}, fmt.Errorf("%w: projection actor is unavailable", ontologyscope.ErrAccessDenied)
	}
	return principal, actor, nil
}

func sameProjectionAuthorization(left, right ontologyscope.AuthorizedScopeContext) bool {
	leftSnapshot, rightSnapshot := left.Snapshot(), right.Snapshot()
	return left.Manage() && right.Manage() &&
		left.Binding().TenantID == right.Binding().TenantID &&
		left.Binding().BindingVersion == right.Binding().BindingVersion &&
		leftSnapshot.PrincipalKey == rightSnapshot.PrincipalKey &&
		leftSnapshot.AuthorizationRevision == rightSnapshot.AuthorizationRevision &&
		leftSnapshot.PolicyRevision == rightSnapshot.PolicyRevision
}

func sameProjectionAuditRecord(left, right contextgraph.ProjectionAudit) bool {
	return left.ID == right.ID && left.TenantID == right.TenantID && left.SourceEventID == right.SourceEventID &&
		left.SourceEventType == right.SourceEventType && left.Status == right.Status && left.ReasonCode == right.ReasonCode &&
		left.Reason == right.Reason && left.SourceAccessRef == right.SourceAccessRef && left.AccessState == right.AccessState &&
		left.ObservedAt.Equal(right.ObservedAt) && left.Revision == right.Revision && reflect.DeepEqual(left.Details, right.Details)
}

func validProjectionIdempotencyKey(value string) bool {
	return value != "" && len(value) <= 256 && utf8.ValidString(value)
}

func projectionCommandRequestHash(operation, reason, principalKey string) string {
	sum := sha256.Sum256([]byte(operation + "\x00" + reason + "\x00" + principalKey))
	return hex.EncodeToString(sum[:])
}

func projectionMutationResultFromClaim(auditID string, claim contextgraph.ProjectionAuditCommandClaim) (MutationResult, error) {
	switch claim.State {
	case contextgraph.ProjectionAuditCommandSucceeded:
		if strings.TrimSpace(claim.ResultStatus) == "" {
			return MutationResult{}, projectionCommandDependencyError(errors.New("projection idempotency result is incomplete"))
		}
		return MutationResult{AuditID: auditID, Status: claim.ResultStatus, ReasonCode: claim.ReasonCode, ObservedAt: claim.ObservedAt}, nil
	case contextgraph.ProjectionAuditCommandClaimed, contextgraph.ProjectionAuditCommandFailed:
		return MutationResult{}, fmt.Errorf("%w: projection command is already %s", ontologycontract.ErrStateConflict, claim.State)
	default:
		return MutationResult{}, projectionCommandDependencyError(errors.New("projection idempotency state is invalid"))
	}
}

func projectionCommandFailureCode(err error) string {
	switch {
	case errors.Is(err, ontologyscope.ErrAccessDenied):
		return "access_revoked"
	case errors.Is(err, ontologycontract.ErrStateConflict):
		return "state_conflict"
	case errors.Is(err, ontologycontract.ErrUnavailable):
		return "unavailable"
	default:
		return "dependency_failure"
	}
}

func projectionCommandStoreError(err error) error {
	if errors.Is(err, contextgraph.ErrProjectionAuditCommandConflict) {
		return fmt.Errorf("%w: projection audit is no longer actionable", ontologycontract.ErrStateConflict)
	}
	return projectionCommandDependencyError(err)
}

func safeProjectionMutationResult(expected, result contextgraph.ProjectionAudit, expectedStatus string) (MutationResult, error) {
	result.SourceAccessRef = contextgraph.NormalizeAccessRef(result.SourceAccessRef)
	if strings.TrimSpace(result.Status) != expectedStatus {
		return MutationResult{}, fmt.Errorf("%w: projection audit did not reach %s", ontologycontract.ErrStateConflict, expectedStatus)
	}
	if result.ID != expected.ID || result.TenantID != expected.TenantID || result.SourceEventID != expected.SourceEventID ||
		result.SourceEventType != expected.SourceEventType || result.SourceAccessRef != expected.SourceAccessRef ||
		result.AccessState != contextgraph.AccessStateResolved || result.ObservedAt.IsZero() {
		return MutationResult{}, projectionCommandDependencyError(errors.New("projection executor returned an invalid or cross-tenant audit"))
	}
	return MutationResult{
		AuditID: result.ID, Status: result.Status, ReasonCode: result.ReasonCode, ObservedAt: visibleTime(result.ObservedAt),
	}, nil
}

func projectionCommandDependencyError(err error) error {
	if err == nil || errors.Is(err, ontologycontract.ErrInvalidRequest) || errors.Is(err, ontologycontract.ErrStateConflict) ||
		errors.Is(err, ontologycontract.ErrUnavailable) || errors.Is(err, ontologyscope.ErrAccessDenied) || errors.Is(err, ontologyscope.ErrDependencyUnavailable) {
		return err
	}
	return dependencyError(err)
}

func firstProjectionValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
