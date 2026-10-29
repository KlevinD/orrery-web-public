package actioncontract

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Service struct {
	repository Repository
	now        func() time.Time
	authorizer AuthorizationVerifier
	lifecycle  RunLifecycleSink
}

type AuthorizationVerifier interface {
	VerifyActionAuthorization(context.Context, StartRunRequest, ActionType) error
}

type RunLifecycleSink interface {
	ProjectActionRun(context.Context, ActionRun) error
}

type WritebackLifecycleSink interface {
	ProjectWritebackReceipt(context.Context, ActionRun, WritebackReceipt) error
}

type Option func(*Service)

func WithClock(now func() time.Time) Option {
	return func(service *Service) {
		if now != nil {
			service.now = now
		}
	}
}

func WithAuthorizationVerifier(verifier AuthorizationVerifier) Option {
	return func(service *Service) {
		service.authorizer = verifier
	}
}

func WithRunLifecycleSink(sink RunLifecycleSink) Option {
	return func(service *Service) {
		service.lifecycle = sink
	}
}

func NewService(repository Repository, options ...Option) *Service {
	service := &Service{
		repository: repository,
		now:        func() time.Time { return time.Now().UTC() },
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *Service) RegisterActionType(ctx context.Context, actionType ActionType) error {
	if s.repository == nil {
		return fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	if err := validateActionType(actionType); err != nil {
		return err
	}
	now := s.now()
	if actionType.TenantID == "" {
		actionType.TenantID = "system"
	}
	if actionType.CreatedAt.IsZero() {
		actionType.CreatedAt = now
	}
	if actionType.UpdatedAt.IsZero() {
		actionType.UpdatedAt = now
	}
	return s.repository.SaveActionType(ctx, actionType)
}

func (s *Service) ListActionTypes(ctx context.Context) ([]ActionType, error) {
	if s.repository == nil {
		return nil, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	return s.repository.ListActionTypes(ctx)
}

func (s *Service) GetActionRun(ctx context.Context, runID ActionRunID) (ActionRun, error) {
	if s.repository == nil {
		return ActionRun{}, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	if runID == "" {
		return ActionRun{}, fmt.Errorf("%w: run_id is required", ErrInvalidRequest)
	}
	return s.repository.GetActionRun(ctx, runID)
}

func (s *Service) ListActionRuns(ctx context.Context, query ActionRunQuery) (ActionRunPage, error) {
	if s.repository == nil {
		return ActionRunPage{}, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	query.TenantID = strings.TrimSpace(query.TenantID)
	if query.TenantID == "" {
		return ActionRunPage{}, fmt.Errorf("%w: tenant_id is required", ErrInvalidRequest)
	}
	return s.repository.ListActionRuns(ctx, query)
}

func (s *Service) StartActionRun(ctx context.Context, req StartRunRequest) (ActionRun, error) {
	if s.repository == nil {
		return ActionRun{}, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	if err := validateStartRunRequest(req); err != nil {
		return ActionRun{}, err
	}
	actionType, err := s.repository.GetActionType(ctx, req.ActionTypeID)
	if err != nil {
		return ActionRun{}, err
	}
	if !actionType.Enabled {
		return ActionRun{}, fmt.Errorf("%w: action type %s is disabled", ErrInvalidRequest, actionType.ID)
	}
	if actionType.TenantID != "" && actionType.TenantID != "system" && strings.TrimSpace(actionType.TenantID) != strings.TrimSpace(req.TenantID) {
		return ActionRun{}, fmt.Errorf("%w: action type %s is not visible to tenant", ErrInvalidRequest, actionType.ID)
	}
	executionMode, err := normalizeExecutionMode(req.ExecutionMode)
	if err != nil {
		return ActionRun{}, err
	}
	if executionMode == ExecutionModeDryRun && !actionType.DryRunSupported {
		return ActionRun{}, fmt.Errorf("%w: action type %s does not support dry_run", ErrInvalidRequest, actionType.ID)
	}
	req.ExecutionMode = executionMode
	if err := validateRunObjectRefs(req.ObjectRefs, actionType); err != nil {
		return ActionRun{}, err
	}
	if err := validateRequiredRunObjectRefs(req.ObjectRefs, actionType); err != nil {
		return ActionRun{}, err
	}
	if actionType.RiskLevel == RiskHigh || actionType.RiskLevel == RiskCritical {
		if strings.TrimSpace(req.Approval.ID) == "" || strings.TrimSpace(req.PolicyDecisionID) == "" || strings.TrimSpace(req.HILTokenID) == "" {
			return ActionRun{}, fmt.Errorf("%w: high-risk action requires approval, policy decision, and HIL token", ErrInvalidRequest)
		}
		if s.authorizer == nil {
			return ActionRun{}, fmt.Errorf("%w: high-risk action authorization verifier is unavailable", ErrInvalidRequest)
		}
		if err := s.authorizer.VerifyActionAuthorization(ctx, req, actionType); err != nil {
			return ActionRun{}, fmt.Errorf("%w: high-risk action authorization failed: %v", ErrInvalidRequest, err)
		}
	}
	run := ActionRun{
		ID:               req.RunID,
		TenantID:         req.TenantID,
		WorkspaceID:      req.WorkspaceID,
		ActionTypeID:     actionType.ID,
		ActionCategory:   actionType.Category,
		Actor:            req.Actor,
		EffectiveUser:    req.EffectiveUser,
		Delegation:       req.Delegation,
		Runtime:          req.Runtime,
		Session:          req.Session,
		HumanIntent:      req.HumanIntent,
		Approval:         req.Approval,
		Purpose:          req.Purpose,
		ObjectRefs:       append([]ObjectRef(nil), req.ObjectRefs...),
		RequestedFields:  append([]string(nil), req.RequestedFields...),
		ArgsHash:         req.ArgsHash,
		RiskLevel:        actionType.RiskLevel,
		ExecutionMode:    executionMode,
		PolicyDecisionID: req.PolicyDecisionID,
		HILTokenID:       req.HILTokenID,
		Status:           RunStatusStarted,
		EvidenceRefs:     append([]Ref(nil), req.EvidenceRefs...),
		AuditRef:         req.AuditRef,
		CreatedAt:        s.now(),
	}
	if err := s.repository.SaveActionRun(ctx, run); err != nil {
		return ActionRun{}, err
	}
	if s.lifecycle != nil {
		if err := s.lifecycle.ProjectActionRun(ctx, run); err != nil {
			failedAt := s.now()
			run.Status = RunStatusFailed
			run.ErrorCode = "lifecycle_projection_failed"
			run.ErrorMessage = "Action run start projection failed"
			run.CompletedAt = &failedAt
			if saveErr := s.repository.SaveActionRun(ctx, run); saveErr != nil {
				return ActionRun{}, errors.Join(err, fmt.Errorf("persist failed action run after projection error: %w", saveErr))
			}
			return ActionRun{}, err
		}
	}
	return run, nil
}

func (s *Service) CompleteActionRun(ctx context.Context, req CompleteRunRequest) (ActionRun, *WritebackReceipt, error) {
	if s.repository == nil {
		return ActionRun{}, nil, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	if req.RunID == "" {
		return ActionRun{}, nil, fmt.Errorf("%w: run_id is required", ErrInvalidRequest)
	}
	run, err := s.repository.GetActionRun(ctx, req.RunID)
	if err != nil {
		return ActionRun{}, nil, err
	}
	if run.Status != RunStatusStarted {
		return ActionRun{}, nil, fmt.Errorf("%w: action run %s is not started", ErrInvalidRequest, run.ID)
	}
	actionType, err := s.repository.GetActionType(ctx, run.ActionTypeID)
	if err != nil {
		return ActionRun{}, nil, err
	}
	evidenceRefs, err := prepareTerminalEvidence(run, req.EvidenceRefs, actionType, false)
	if err != nil {
		return ActionRun{}, nil, err
	}
	completedAt := s.now()
	var receipt *WritebackReceipt
	if req.Writeback != nil {
		prepared := *req.Writeback
		if prepared.ID == "" {
			return ActionRun{}, nil, fmt.Errorf("%w: writeback receipt id is required", ErrInvalidRequest)
		}
		if prepared.Status != WritebackStatusSucceeded && prepared.Status != WritebackStatusFailed {
			return ActionRun{}, nil, fmt.Errorf("%w: writeback receipt status is invalid", ErrInvalidRequest)
		}
		if strings.TrimSpace(prepared.TargetSystem) == "" {
			return ActionRun{}, nil, fmt.Errorf("%w: writeback target_system is required", ErrInvalidRequest)
		}
		prepared.TenantID = run.TenantID
		prepared.ActionRunID = run.ID
		if prepared.CreatedAt.IsZero() {
			prepared.CreatedAt = completedAt
		}
		receipt = &prepared
	}
	run.Status = RunStatusCompleted
	run.ProviderCallRef = req.ProviderCallRef
	run.ResultRef = req.ResultRef
	run.EvidenceRefs = evidenceRefs
	run.CompletedAt = &completedAt
	if err := s.repository.SaveActionRun(ctx, run); err != nil {
		return ActionRun{}, nil, err
	}
	if receipt != nil {
		if err := s.repository.SaveWritebackReceipt(ctx, *receipt); err != nil {
			return ActionRun{}, nil, err
		}
	}
	if s.lifecycle != nil {
		if err := s.lifecycle.ProjectActionRun(ctx, run); err != nil {
			return ActionRun{}, nil, err
		}
	}
	if receipt == nil {
		return run, nil, nil
	}
	if sink, ok := s.lifecycle.(WritebackLifecycleSink); ok {
		if err := sink.ProjectWritebackReceipt(ctx, run, *receipt); err != nil {
			return ActionRun{}, nil, err
		}
	}
	return run, receipt, nil
}

func (s *Service) FailActionRun(ctx context.Context, req FailRunRequest) (ActionRun, error) {
	if s.repository == nil {
		return ActionRun{}, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	if req.RunID == "" {
		return ActionRun{}, fmt.Errorf("%w: run_id is required", ErrInvalidRequest)
	}
	req.ErrorCode = strings.TrimSpace(req.ErrorCode)
	req.ErrorMessage = strings.TrimSpace(req.ErrorMessage)
	if req.ErrorCode == "" || len(req.ErrorCode) > 128 {
		return ActionRun{}, fmt.Errorf("%w: error_code is required and must not exceed 128 characters", ErrInvalidRequest)
	}
	if len(req.ErrorMessage) > 1024 {
		return ActionRun{}, fmt.Errorf("%w: error_message must not exceed 1024 characters", ErrInvalidRequest)
	}
	run, err := s.repository.GetActionRun(ctx, req.RunID)
	if err != nil {
		return ActionRun{}, err
	}
	if run.Status != RunStatusStarted {
		return ActionRun{}, fmt.Errorf("%w: action run %s is not started", ErrInvalidRequest, run.ID)
	}
	actionType, err := s.repository.GetActionType(ctx, run.ActionTypeID)
	if err != nil {
		return ActionRun{}, err
	}
	evidenceRefs, err := prepareTerminalEvidence(run, req.EvidenceRefs, actionType, true)
	if err != nil {
		return ActionRun{}, err
	}
	completedAt := s.now()
	run.Status = RunStatusFailed
	run.ProviderCallRef = req.ProviderCallRef
	run.EvidenceRefs = evidenceRefs
	run.ErrorCode = req.ErrorCode
	run.ErrorMessage = req.ErrorMessage
	run.CompletedAt = &completedAt
	if err := s.repository.SaveActionRun(ctx, run); err != nil {
		return ActionRun{}, err
	}
	if s.lifecycle != nil {
		if err := s.lifecycle.ProjectActionRun(ctx, run); err != nil {
			return ActionRun{}, err
		}
	}
	return run, nil
}

func (s *Service) ListWritebackReceipts(ctx context.Context, runID ActionRunID) ([]WritebackReceipt, error) {
	if s.repository == nil {
		return nil, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	if runID == "" {
		return nil, fmt.Errorf("%w: run_id is required", ErrInvalidRequest)
	}
	return s.repository.ListWritebackReceipts(ctx, runID)
}

func validateActionType(actionType ActionType) error {
	if actionType.ID == "" {
		return fmt.Errorf("%w: action type id is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(actionType.Name) == "" {
		return fmt.Errorf("%w: action type name is required", ErrInvalidRequest)
	}
	if !actionType.Category.isValid() {
		return fmt.Errorf("%w: action category %q is invalid", ErrInvalidRequest, actionType.Category)
	}
	if !actionType.ProviderKind.isValid() {
		return fmt.Errorf("%w: provider kind %q is invalid", ErrInvalidRequest, actionType.ProviderKind)
	}
	if actionType.RiskLevel == "" {
		return fmt.Errorf("%w: risk level is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(actionType.Version) == "" {
		return fmt.Errorf("%w: version is required", ErrInvalidRequest)
	}
	if err := validateActionAuditPolicy(actionType); err != nil {
		return err
	}
	return nil
}

func validateActionAuditPolicy(actionType ActionType) error {
	required, err := actionPolicyStrings(actionType.AuditPolicy["required_object_types"])
	if err != nil {
		return fmt.Errorf("%w: action type %s has invalid required object policy", ErrInvalidRequest, actionType.ID)
	}
	allowed := make(map[string]struct{}, len(actionType.ObjectTypeRefs))
	for _, objectType := range actionType.ObjectTypeRefs {
		if normalized := normalizeActionToken(objectType); normalized != "" {
			allowed[normalized] = struct{}{}
		}
	}
	seen := make(map[string]struct{}, len(required))
	for _, objectType := range required {
		normalized := normalizeActionToken(objectType)
		if normalized == "" {
			return fmt.Errorf("%w: action type %s has an empty required object type", ErrInvalidRequest, actionType.ID)
		}
		if _, exists := seen[normalized]; exists {
			return fmt.Errorf("%w: action type %s repeats required object type %q", ErrInvalidRequest, actionType.ID, normalized)
		}
		seen[normalized] = struct{}{}
		if _, exists := allowed[normalized]; !exists {
			return fmt.Errorf("%w: action type %s requires undeclared object type %q", ErrInvalidRequest, actionType.ID, normalized)
		}
	}
	for _, key := range []string{"unique_object_ref_types", "terminal_evidence_required", "failure_run_is_evidence"} {
		if value, exists := actionType.AuditPolicy[key]; exists {
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("%w: action type %s audit policy %q must be boolean", ErrInvalidRequest, actionType.ID, key)
			}
		}
	}
	if value, exists := actionType.AuditPolicy["terminal_evidence_kind"]; exists {
		kind, ok := value.(string)
		if !ok || normalizeActionToken(kind) == "" {
			return fmt.Errorf("%w: action type %s terminal evidence kind is invalid", ErrInvalidRequest, actionType.ID)
		}
	}
	return nil
}

func validateStartRunRequest(req StartRunRequest) error {
	if req.RunID == "" {
		return fmt.Errorf("%w: run_id is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(req.TenantID) == "" {
		return fmt.Errorf("%w: tenant_id is required", ErrInvalidRequest)
	}
	if req.ActionTypeID == "" {
		return fmt.Errorf("%w: action_type_id is required", ErrInvalidRequest)
	}
	if !req.Actor.isValid() {
		return fmt.Errorf("%w: actor_ref is required", ErrInvalidRequest)
	}
	if !req.Runtime.isValid() {
		return fmt.Errorf("%w: runtime_ref is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(req.ArgsHash) == "" {
		return fmt.Errorf("%w: args_hash is required", ErrInvalidRequest)
	}
	if len(req.EvidenceRefs) > 100 {
		return fmt.Errorf("%w: no more than 100 evidence_refs are allowed", ErrInvalidRequest)
	}
	for _, ref := range req.EvidenceRefs {
		if normalizeActionToken(ref.Kind) == "" || strings.TrimSpace(ref.ID) == "" {
			return fmt.Errorf("%w: evidence_ref kind and id are required", ErrInvalidRequest)
		}
	}
	return nil
}

func validateRunObjectRefs(refs []ObjectRef, actionType ActionType) error {
	if len(refs) > 100 {
		return fmt.Errorf("%w: no more than 100 object_refs are allowed", ErrInvalidRequest)
	}
	allowed := map[string]struct{}{}
	for _, objectType := range actionType.ObjectTypeRefs {
		objectType = normalizeActionToken(objectType)
		if objectType == "" {
			continue
		}
		allowed[objectType] = struct{}{}
	}
	for _, ref := range refs {
		if strings.TrimSpace(ref.ID) == "" {
			return fmt.Errorf("%w: object_ref id is required", ErrInvalidRequest)
		}
		kind := normalizeActionToken(ref.Kind)
		if kind == "" {
			return fmt.Errorf("%w: object_ref kind is required", ErrInvalidRequest)
		}
		if len(allowed) > 0 {
			if _, ok := allowed[kind]; ok {
				continue
			}
			return fmt.Errorf("%w: object_ref kind %q is not allowed for action type %s", ErrInvalidRequest, ref.Kind, actionType.ID)
		}
	}
	return nil
}

func validateRequiredRunObjectRefs(refs []ObjectRef, actionType ActionType) error {
	required, err := actionPolicyStrings(actionType.AuditPolicy["required_object_types"])
	if err != nil {
		return fmt.Errorf("%w: action type %s has invalid required object policy", ErrInvalidRequest, actionType.ID)
	}
	counts := make(map[string]int, len(refs))
	for _, ref := range refs {
		counts[normalizeActionToken(ref.Kind)]++
	}
	for _, objectType := range required {
		if counts[normalizeActionToken(objectType)] == 0 {
			return fmt.Errorf("%w: action type %s requires object_ref kind %q", ErrInvalidRequest, actionType.ID, objectType)
		}
	}
	unique, _ := actionType.AuditPolicy["unique_object_ref_types"].(bool)
	if unique {
		for objectType, count := range counts {
			if count > 1 {
				return fmt.Errorf("%w: action type %s allows only one object_ref of kind %q", ErrInvalidRequest, actionType.ID, objectType)
			}
		}
	}
	return nil
}

func prepareTerminalEvidence(run ActionRun, provided []Ref, actionType ActionType, failed bool) ([]Ref, error) {
	if len(provided) > 100 || len(run.EvidenceRefs)+len(provided) > 200 {
		return nil, fmt.Errorf("%w: too many terminal evidence references", ErrInvalidRequest)
	}
	required, _ := actionType.AuditPolicy["terminal_evidence_required"].(bool)
	expectedKind, _ := actionType.AuditPolicy["terminal_evidence_kind"].(string)
	expectedKind = normalizeActionToken(expectedKind)
	result := append([]Ref(nil), run.EvidenceRefs...)
	seen := make(map[string]struct{}, len(result)+len(provided))
	for _, ref := range result {
		seen[normalizeActionToken(ref.Kind)+"\x00"+strings.TrimSpace(ref.ID)] = struct{}{}
	}
	added := 0
	for _, ref := range provided {
		ref.Kind = normalizeActionToken(ref.Kind)
		ref.ID = strings.TrimSpace(ref.ID)
		if ref.Kind == "" || ref.ID == "" {
			return nil, fmt.Errorf("%w: terminal evidence kind and id are required", ErrInvalidRequest)
		}
		if expectedKind != "" && ref.Kind != expectedKind {
			return nil, fmt.Errorf("%w: terminal evidence kind must be %q", ErrInvalidRequest, expectedKind)
		}
		key := ref.Kind + "\x00" + ref.ID
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, ref)
		added++
	}
	if required && added == 0 {
		failureRunIsEvidence, _ := actionType.AuditPolicy["failure_run_is_evidence"].(bool)
		if failed && failureRunIsEvidence {
			if expectedKind == "" {
				expectedKind = "evidence_item"
			}
			result = append(result, Ref{Kind: expectedKind, ID: "action-run-failure-evidence:" + string(run.ID)})
			return result, nil
		}
		return nil, fmt.Errorf("%w: action type %s requires new terminal verification evidence", ErrInvalidRequest, actionType.ID)
	}
	return result, nil
}

func actionPolicyStrings(value any) ([]string, error) {
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case []string:
		return append([]string(nil), typed...), nil
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			value, ok := item.(string)
			if !ok || strings.TrimSpace(value) == "" {
				return nil, fmt.Errorf("invalid string list")
			}
			values = append(values, value)
		}
		return values, nil
	default:
		return nil, fmt.Errorf("invalid string list")
	}
}

func normalizeExecutionMode(mode ExecutionMode) (ExecutionMode, error) {
	switch ExecutionMode(strings.ToLower(strings.TrimSpace(string(mode)))) {
	case "", ExecutionModeApply:
		return ExecutionModeApply, nil
	case ExecutionModeDryRun:
		return ExecutionModeDryRun, nil
	default:
		return "", fmt.Errorf("%w: execution_mode is invalid", ErrInvalidRequest)
	}
}

func (c ActionCategory) isValid() bool {
	switch c {
	case CategoryHuman, CategoryAgent, CategoryTool, CategoryHybrid, CategorySystem:
		return true
	default:
		return false
	}
}

func (k ProviderKind) isValid() bool {
	switch k {
	case ProviderHuman, ProviderMCPTool, ProviderInternalService, ProviderConnector, ProviderWorker, ProviderApproval:
		return true
	default:
		return false
	}
}

func (s ActionSubject) isValid() bool {
	return s.Kind != "" && strings.TrimSpace(s.ID) != ""
}

func (r RuntimeRef) isValid() bool {
	return r.Kind != "" && strings.TrimSpace(r.ID) != ""
}
