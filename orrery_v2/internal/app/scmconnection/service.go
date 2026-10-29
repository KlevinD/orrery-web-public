package scmconnection

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/platform/idgen"
)

type IDGenerator interface {
	NewIDContext() (string, error)
}

type Clock interface {
	Now() time.Time
}

type Verifier interface {
	SupportsProvider(provider string) bool
	Verify(ctx context.Context, connection Connection) (Verification, error)
	ListRepositories(ctx context.Context, connection Connection) ([]RepositoryMetadata, error)
}

type AuditRecorder interface {
	RecordSCMConnectionAudit(ctx context.Context, event AuditEvent) error
}

type SecretReferenceValidator interface {
	ValidateSecretReference(ctx context.Context, ref string) error
}

type Service struct {
	repo        Repository
	ids         IDGenerator
	clock       Clock
	access      *authorization.Service
	relations   *authorization.RelationProjector
	verifier    Verifier
	audit       AuditRecorder
	secrets     SecretReferenceValidator
	reconcileMu sync.Mutex
	reconcileAt string
}

func NewService(repo Repository, ids IDGenerator, clock Clock) *Service {
	return &Service{repo: repo, ids: ids, clock: clock}
}

func (s *Service) SetAuthorization(service *authorization.Service, projector *authorization.RelationProjector) {
	if s != nil {
		s.access = service
		s.relations = projector
	}
}

func (s *Service) SetVerifier(verifier Verifier) {
	if s != nil {
		s.verifier = verifier
	}
}

func (s *Service) SetAuditRecorder(recorder AuditRecorder) {
	if s != nil {
		s.audit = recorder
	}
}

func (s *Service) SetSecretReferenceValidator(validator SecretReferenceValidator) {
	if s != nil {
		s.secrets = validator
	}
}

func (s *Service) Create(ctx context.Context, viewer ViewerRef, cmd CreateCommand) (ConnectionDTO, error) {
	if s == nil || s.repo == nil || s.access == nil || s.relations == nil || s.audit == nil {
		return ConnectionDTO{}, ErrDependency
	}
	if err := validateViewer(viewer); err != nil {
		return ConnectionDTO{}, err
	}
	cmd = normalizeCreateCommand(cmd)
	if cmd.IdempotencyKey != "" && !validIdempotencyKey(cmd.IdempotencyKey) {
		return ConnectionDTO{}, fmt.Errorf("%w: idempotency key is invalid", ErrInvalidRequest)
	}
	if err := validateCreateCommand(viewer, cmd); err != nil {
		return ConnectionDTO{}, err
	}
	if cmd.ConnectionID != "" && !validResourceID(cmd.ConnectionID) {
		return ConnectionDTO{}, fmt.Errorf("%w: connection_id is invalid", ErrInvalidRequest)
	}
	if err := s.validateSecretReference(ctx, cmd.SecretRef); err != nil {
		return ConnectionDTO{}, err
	}
	if err := s.authorizeCreate(ctx, viewer, cmd.OrganizationID); err != nil {
		return ConnectionDTO{}, err
	}
	if viewer.ViewerKind != identity.ViewerKindUser {
		return ConnectionDTO{}, fmt.Errorf("%w: SCM connection owner must be a user", ErrInvalidRequest)
	}
	requestHash, err := scmConnectionCreateRequestHash(cmd)
	if err != nil {
		return ConnectionDTO{}, err
	}
	connectionID := cmd.ConnectionID
	if cmd.IdempotencyKey != "" && connectionID == "" {
		connectionID = deterministicResourceID("scm", viewerTenant(viewer), cmd.IdempotencyKey)
	}
	if connectionID == "" {
		var err error
		connectionID, err = s.newID("scm")
		if err != nil {
			return ConnectionDTO{}, err
		}
	}
	if !validResourceID(connectionID) {
		return ConnectionDTO{}, fmt.Errorf("%w: connection_id is invalid", ErrInvalidRequest)
	}
	now := s.now()
	status := StatusError
	if s.providerSupported(cmd.Provider) {
		status = StatusActive
	}
	connection := Connection{
		TenantID:                  viewerTenant(viewer),
		ConnectionID:              connectionID,
		OrganizationID:            cmd.OrganizationID,
		OwnerID:                   viewerSubjectID(viewer),
		Scope:                     cmd.Scope,
		Provider:                  cmd.Provider,
		BaseURL:                   cmd.BaseURL,
		InstallationOrAccountID:   cmd.InstallationOrAccountID,
		SecretRef:                 cmd.SecretRef,
		AllowedRepositoryPatterns: append([]string(nil), cmd.AllowedRepositoryPatterns...),
		Status:                    status,
		CreatedAt:                 now,
		UpdatedAt:                 now,
		Version:                   1,
		CreateIdempotencyKey:      cmd.IdempotencyKey,
		CreateRequestHash:         requestHash,
	}
	appendConnectionEffects(&connection,
		connectionCreatedEffect(connection, viewerSubject(viewer)),
		s.connectionAuditEffect(connection, viewer, "scm_connection.created", "succeeded", "created SCM connection", nil),
	)
	if err := s.repo.Create(ctx, connection.TenantID, connection); err != nil {
		if !errors.Is(err, ErrConflict) || cmd.IdempotencyKey == "" {
			return ConnectionDTO{}, err
		}
		existing, getErr := s.repo.Get(ctx, connection.TenantID, connection.ConnectionID)
		if getErr != nil {
			return ConnectionDTO{}, getErr
		}
		if existing.CreateIdempotencyKey != cmd.IdempotencyKey || existing.CreateRequestHash != requestHash {
			return ConnectionDTO{}, ErrIdempotency
		}
		connection = existing
	}
	connection, err = s.reconcileConnectionEffects(ctx, connection.TenantID, connection.ConnectionID)
	if err != nil {
		return ConnectionDTO{}, err
	}
	return s.dto(ctx, viewer, connection)
}

func (s *Service) List(ctx context.Context, viewer ViewerRef, query ListQuery) (ConnectionPage, error) {
	if s == nil || s.repo == nil || s.access == nil {
		return ConnectionPage{}, ErrDependency
	}
	if err := validateViewer(viewer); err != nil {
		return ConnectionPage{}, err
	}
	query.OrganizationID = strings.TrimSpace(query.OrganizationID)
	query.Provider = strings.ToLower(strings.TrimSpace(query.Provider))
	query.Status = strings.ToLower(strings.TrimSpace(query.Status))
	if (query.OrganizationID != "" && !validSubjectID(query.OrganizationID)) ||
		(query.Provider != "" && !oneOf(query.Provider, ProviderGitHub, ProviderGitLab, ProviderGitea, ProviderManagedLocal, ProviderGenericGit)) ||
		(query.Status != "" && !oneOf(query.Status, StatusActive, StatusExpired, StatusRevoked, StatusError)) || query.Offset < 0 || query.Limit < 0 {
		return ConnectionPage{}, ErrInvalidRequest
	}
	connections, err := s.repo.List(ctx, viewerTenant(viewer))
	if err != nil {
		return ConnectionPage{}, err
	}
	items := make([]ConnectionDTO, 0, len(connections))
	for _, connection := range connections {
		if query.OrganizationID != "" && connection.OrganizationID != strings.TrimSpace(query.OrganizationID) {
			continue
		}
		if query.Provider != "" && connection.Provider != strings.ToLower(strings.TrimSpace(query.Provider)) {
			continue
		}
		if query.Status != "" && connection.Status != strings.ToLower(strings.TrimSpace(query.Status)) {
			continue
		}
		capabilities, err := s.capabilities(ctx, viewer, connection)
		if err != nil {
			return ConnectionPage{}, err
		}
		if !capabilities.CanView {
			continue
		}
		items = append(items, mapConnectionDTO(connection, capabilities))
	}
	offset, limit := pageBounds(query.Offset, query.Limit, len(items))
	pageItems := append([]ConnectionDTO(nil), items[offset:offset+limit]...)
	return ConnectionPage{Items: pageItems, Page: PageDTO{Limit: limit, Offset: offset, Total: len(items), Returned: len(pageItems), HasMore: offset+limit < len(items)}}, nil
}

func (s *Service) Get(ctx context.Context, viewer ViewerRef, connectionID string) (ConnectionDTO, error) {
	connection, err := s.getAuthorized(ctx, viewer, connectionID, "scm_connection.view")
	if err != nil {
		return ConnectionDTO{}, err
	}
	return s.dto(ctx, viewer, connection)
}

func (s *Service) Update(ctx context.Context, viewer ViewerRef, cmd UpdateCommand) (ConnectionDTO, error) {
	cmd.IdempotencyKey = strings.TrimSpace(cmd.IdempotencyKey)
	if cmd.IdempotencyKey != "" && !validIdempotencyKey(cmd.IdempotencyKey) {
		return ConnectionDTO{}, fmt.Errorf("%w: idempotency key is invalid", ErrInvalidRequest)
	}
	hashCommand := cmd
	hashCommand.IdempotencyKey = ""
	requestHash, err := connectionMutationRequestHash("update", hashCommand)
	if err != nil {
		return ConnectionDTO{}, err
	}
	connection, err := s.getAuthorized(ctx, viewer, cmd.ConnectionID, "scm_connection.manage")
	if err != nil {
		return ConnectionDTO{}, err
	}
	if receipt, replay, err := connectionMutationReplay(connection, cmd.IdempotencyKey, "update", requestHash); err != nil {
		return ConnectionDTO{}, err
	} else if replay {
		_ = receipt
		connection, err = s.reconcileConnectionEffects(ctx, connection.TenantID, connection.ConnectionID)
		if err != nil {
			return ConnectionDTO{}, err
		}
		return s.dto(ctx, viewer, connection)
	}
	if connection.Status == StatusRevoked {
		return ConnectionDTO{}, fmt.Errorf("%w: revoked connections cannot be updated", ErrInvalidRequest)
	}
	if cmd.ExpectedVersion <= 0 || connection.Version != cmd.ExpectedVersion {
		return ConnectionDTO{}, ErrConflict
	}
	if cmd.BaseURL != nil {
		connection.BaseURL = strings.TrimSpace(*cmd.BaseURL)
		if connection.Provider == ProviderManagedLocal && connection.BaseURL != "" {
			connection.BaseURL = filepath.Clean(connection.BaseURL)
		}
	}
	if cmd.InstallationOrAccountID != nil {
		connection.InstallationOrAccountID = strings.TrimSpace(*cmd.InstallationOrAccountID)
	}
	if cmd.AllowedRepositoryPatterns != nil {
		connection.AllowedRepositoryPatterns = normalizePatterns(*cmd.AllowedRepositoryPatterns)
	}
	if err := validateConnectionMetadata(connection.Provider, connection.InstallationOrAccountID, connection.AllowedRepositoryPatterns); err != nil {
		return ConnectionDTO{}, err
	}
	if err := validateConnectionLocation(viewer, connection.Provider, connection.BaseURL); err != nil {
		return ConnectionDTO{}, err
	}
	connection.Version++
	connection.UpdatedAt = s.now()
	appendConnectionMutationReceipt(&connection, MutationReceipt{IdempotencyKey: cmd.IdempotencyKey, Operation: "update", RequestHash: requestHash})
	appendConnectionEffects(&connection, s.connectionAuditEffect(connection, viewer, "scm_connection.updated", "succeeded", "updated SCM connection", nil))
	if err := s.repo.Update(ctx, connection.TenantID, connection, cmd.ExpectedVersion); err != nil {
		return ConnectionDTO{}, err
	}
	connection, err = s.reconcileConnectionEffects(ctx, connection.TenantID, connection.ConnectionID)
	if err != nil {
		return ConnectionDTO{}, err
	}
	return s.dto(ctx, viewer, connection)
}

func (s *Service) Rotate(ctx context.Context, viewer ViewerRef, cmd RotateCommand) (ConnectionDTO, error) {
	cmd.IdempotencyKey = strings.TrimSpace(cmd.IdempotencyKey)
	if cmd.IdempotencyKey != "" && !validIdempotencyKey(cmd.IdempotencyKey) {
		return ConnectionDTO{}, fmt.Errorf("%w: idempotency key is invalid", ErrInvalidRequest)
	}
	hashCommand := cmd
	hashCommand.IdempotencyKey = ""
	requestHash, err := connectionMutationRequestHash("rotate", hashCommand)
	if err != nil {
		return ConnectionDTO{}, err
	}
	connection, err := s.getAuthorized(ctx, viewer, cmd.ConnectionID, "scm_connection.manage")
	if err != nil {
		return ConnectionDTO{}, err
	}
	if _, replay, err := connectionMutationReplay(connection, cmd.IdempotencyKey, "rotate", requestHash); err != nil {
		return ConnectionDTO{}, err
	} else if replay {
		connection, err = s.reconcileConnectionEffects(ctx, connection.TenantID, connection.ConnectionID)
		if err != nil {
			return ConnectionDTO{}, err
		}
		return s.dto(ctx, viewer, connection)
	}
	if connection.Provider == ProviderManagedLocal {
		return ConnectionDTO{}, fmt.Errorf("%w: managed_local connections do not use credentials", ErrInvalidRequest)
	}
	if connection.Status == StatusRevoked {
		return ConnectionDTO{}, fmt.Errorf("%w: revoked connections cannot be rotated", ErrInvalidRequest)
	}
	secretRef := strings.TrimSpace(cmd.SecretRef)
	if !validSecretRef(secretRef) {
		return ConnectionDTO{}, fmt.Errorf("%w: secret_ref is invalid", ErrInvalidRequest)
	}
	if err := s.validateSecretReference(ctx, secretRef); err != nil {
		return ConnectionDTO{}, err
	}
	if cmd.ExpectedVersion <= 0 || connection.Version != cmd.ExpectedVersion {
		return ConnectionDTO{}, ErrConflict
	}
	connection.SecretRef = secretRef
	if s.providerSupported(connection.Provider) {
		connection.Status = StatusActive
	} else {
		connection.Status = StatusError
	}
	connection.Version++
	connection.UpdatedAt = s.now()
	appendConnectionMutationReceipt(&connection, MutationReceipt{IdempotencyKey: cmd.IdempotencyKey, Operation: "rotate", RequestHash: requestHash})
	appendConnectionEffects(&connection, s.connectionAuditEffect(connection, viewer, "scm_connection.rotated", "succeeded", "rotated SCM connection secret reference", nil))
	if err := s.repo.Update(ctx, connection.TenantID, connection, cmd.ExpectedVersion); err != nil {
		return ConnectionDTO{}, err
	}
	connection, err = s.reconcileConnectionEffects(ctx, connection.TenantID, connection.ConnectionID)
	if err != nil {
		return ConnectionDTO{}, err
	}
	return s.dto(ctx, viewer, connection)
}

func (s *Service) Revoke(ctx context.Context, viewer ViewerRef, cmd ActionCommand) (ConnectionDTO, error) {
	cmd.IdempotencyKey = strings.TrimSpace(cmd.IdempotencyKey)
	if cmd.IdempotencyKey != "" && !validIdempotencyKey(cmd.IdempotencyKey) {
		return ConnectionDTO{}, fmt.Errorf("%w: idempotency key is invalid", ErrInvalidRequest)
	}
	hashCommand := cmd
	hashCommand.IdempotencyKey = ""
	requestHash, err := connectionMutationRequestHash("revoke", hashCommand)
	if err != nil {
		return ConnectionDTO{}, err
	}
	connection, err := s.getAuthorized(ctx, viewer, cmd.ConnectionID, "scm_connection.manage")
	if err != nil {
		return ConnectionDTO{}, err
	}
	if _, replay, err := connectionMutationReplay(connection, cmd.IdempotencyKey, "revoke", requestHash); err != nil {
		return ConnectionDTO{}, err
	} else if replay {
		connection, err = s.reconcileConnectionEffects(ctx, connection.TenantID, connection.ConnectionID)
		if err != nil {
			return ConnectionDTO{}, err
		}
		return s.dto(ctx, viewer, connection)
	}
	if connection.Status == StatusRevoked {
		return ConnectionDTO{}, fmt.Errorf("%w: connection is already revoked", ErrInvalidRequest)
	}
	if cmd.ExpectedVersion <= 0 || connection.Version != cmd.ExpectedVersion {
		return ConnectionDTO{}, ErrConflict
	}
	connection.Status = StatusRevoked
	connection.Version++
	connection.UpdatedAt = s.now()
	appendConnectionMutationReceipt(&connection, MutationReceipt{IdempotencyKey: cmd.IdempotencyKey, Operation: "revoke", RequestHash: requestHash})
	appendConnectionEffects(&connection, s.connectionAuditEffect(connection, viewer, "scm_connection.revoked", "succeeded", "revoked SCM connection", nil))
	if err := s.repo.Update(ctx, connection.TenantID, connection, cmd.ExpectedVersion); err != nil {
		return ConnectionDTO{}, err
	}
	connection, err = s.reconcileConnectionEffects(ctx, connection.TenantID, connection.ConnectionID)
	if err != nil {
		return ConnectionDTO{}, err
	}
	return s.dto(ctx, viewer, connection)
}

func (s *Service) Verify(ctx context.Context, viewer ViewerRef, cmd ActionCommand) (ConnectionDTO, Verification, error) {
	cmd.IdempotencyKey = strings.TrimSpace(cmd.IdempotencyKey)
	if cmd.IdempotencyKey != "" && !validIdempotencyKey(cmd.IdempotencyKey) {
		return ConnectionDTO{}, Verification{}, fmt.Errorf("%w: idempotency key is invalid", ErrInvalidRequest)
	}
	hashCommand := cmd
	hashCommand.IdempotencyKey = ""
	requestHash, err := connectionMutationRequestHash("verify", hashCommand)
	if err != nil {
		return ConnectionDTO{}, Verification{}, err
	}
	connection, err := s.getAuthorized(ctx, viewer, cmd.ConnectionID, "scm_connection.manage")
	if err != nil {
		return ConnectionDTO{}, Verification{}, err
	}
	if receipt, replay, err := connectionMutationReplay(connection, cmd.IdempotencyKey, "verify", requestHash); err != nil {
		return ConnectionDTO{}, Verification{}, err
	} else if replay {
		connection, err = s.reconcileConnectionEffects(ctx, connection.TenantID, connection.ConnectionID)
		if err != nil {
			return ConnectionDTO{}, Verification{}, err
		}
		dto, err := s.dto(ctx, viewer, connection)
		if err != nil {
			return ConnectionDTO{}, Verification{}, err
		}
		verification := Verification{}
		if receipt.Verification != nil {
			verification = *receipt.Verification
		}
		if receipt.Failure != "" {
			return dto, Verification{}, fmt.Errorf("%w: %s", ErrVerificationFailed, receipt.Failure)
		}
		return dto, verification, nil
	}
	if s.verifier == nil {
		return ConnectionDTO{}, Verification{}, ErrDependency
	}
	if connection.Status == StatusRevoked {
		return ConnectionDTO{}, Verification{}, fmt.Errorf("%w: revoked connections cannot be verified", ErrInvalidRequest)
	}
	if !s.providerSupported(connection.Provider) {
		return ConnectionDTO{}, Verification{}, fmt.Errorf("%w: provider is not available", ErrInvalidRequest)
	}
	if cmd.ExpectedVersion <= 0 || connection.Version != cmd.ExpectedVersion {
		return ConnectionDTO{}, Verification{}, ErrConflict
	}
	verification, verifyErr := s.verifier.Verify(ctx, connection)
	if verifyErr == nil {
		if validationErr := validatePersistedVerification(verification); validationErr != nil {
			verifyErr = fmt.Errorf("%w: provider returned invalid verification metadata", ErrVerificationFailed)
		}
	}
	connection.Version++
	connection.UpdatedAt = s.now()
	if verifyErr != nil {
		connection.Status = StatusError
	} else {
		connection.Status = StatusActive
		connection.LastVerifiedAt = verification.VerifiedAt.UTC()
	}
	result := "succeeded"
	if verifyErr != nil {
		result = "failed"
	}
	receipt := MutationReceipt{IdempotencyKey: cmd.IdempotencyKey, Operation: "verify", RequestHash: requestHash}
	if verifyErr != nil {
		receipt.Failure = verifyErr.Error()
	} else {
		verificationCopy := verification
		receipt.Verification = &verificationCopy
	}
	appendConnectionMutationReceipt(&connection, receipt)
	appendConnectionEffects(&connection, s.connectionAuditEffect(connection, viewer, "scm_connection.verified", result, "verified SCM connection", nil))
	if err := s.repo.Update(ctx, connection.TenantID, connection, cmd.ExpectedVersion); err != nil {
		return ConnectionDTO{}, Verification{}, err
	}
	connection, err = s.reconcileConnectionEffects(ctx, connection.TenantID, connection.ConnectionID)
	if err != nil {
		return ConnectionDTO{}, Verification{}, err
	}
	dto, err := s.dto(ctx, viewer, connection)
	if err != nil {
		return ConnectionDTO{}, Verification{}, err
	}
	if verifyErr != nil {
		return dto, Verification{}, fmt.Errorf("%w: %v", ErrVerificationFailed, verifyErr)
	}
	return dto, verification, nil
}

func (s *Service) ListRepositories(ctx context.Context, viewer ViewerRef, connectionID string) ([]RepositoryMetadata, error) {
	connection, err := s.getAuthorized(ctx, viewer, connectionID, "scm_connection.use")
	if err != nil {
		return nil, err
	}
	if connection.Status != StatusActive {
		return nil, fmt.Errorf("%w: connection is not active", ErrInvalidRequest)
	}
	if s.verifier == nil {
		return nil, ErrDependency
	}
	if !s.providerSupported(connection.Provider) {
		return nil, fmt.Errorf("%w: provider is not available", ErrInvalidRequest)
	}
	return s.verifier.ListRepositories(ctx, connection)
}

func (s *Service) GetConnectionForUse(ctx context.Context, viewer ViewerRef, connectionID string) (Connection, error) {
	connection, err := s.getAuthorized(ctx, viewer, connectionID, "scm_connection.use")
	if err != nil {
		return Connection{}, err
	}
	if connection.Status != StatusActive || !s.providerSupported(connection.Provider) {
		return Connection{}, fmt.Errorf("%w: connection is not available for use", ErrInvalidRequest)
	}
	return connection, nil
}

// GetConnectionForExecution is a trusted, tenant-scoped orchestration port.
// End-user authorization is checked before an approved Todo reaches this path;
// the returned secret remains internal to the SCM adapter.
func (s *Service) GetConnectionForExecution(ctx context.Context, tenantID, connectionID string) (Connection, error) {
	if s == nil || s.repo == nil {
		return Connection{}, ErrDependency
	}
	tenantID = normalizeTenantID(tenantID)
	connectionID = strings.TrimSpace(connectionID)
	if !validTenantKeySegment(tenantID) || !validResourceID(connectionID) {
		return Connection{}, ErrInvalidRequest
	}
	connection, err := s.repo.Get(ctx, tenantID, connectionID)
	if err != nil {
		return Connection{}, err
	}
	if connection.Status != StatusActive || !s.providerSupported(connection.Provider) {
		return Connection{}, fmt.Errorf("%w: connection is not available for execution", ErrInvalidRequest)
	}
	return connection, nil
}

func (s *Service) getAuthorized(ctx context.Context, viewer ViewerRef, connectionID, action string) (Connection, error) {
	if s == nil || s.repo == nil {
		return Connection{}, ErrDependency
	}
	if err := validateViewer(viewer); err != nil {
		return Connection{}, err
	}
	connectionID = strings.TrimSpace(connectionID)
	if !validResourceID(connectionID) {
		return Connection{}, ErrInvalidRequest
	}
	connection, err := s.repo.Get(ctx, viewerTenant(viewer), connectionID)
	if err != nil {
		return Connection{}, err
	}
	if err := s.authorize(ctx, viewer, action, connection); err != nil {
		if errors.Is(err, authorization.ErrDenied) {
			return Connection{}, ErrNotFound
		}
		return Connection{}, err
	}
	return connection, nil
}

func (s *Service) dto(ctx context.Context, viewer ViewerRef, connection Connection) (ConnectionDTO, error) {
	capabilities, err := s.capabilities(ctx, viewer, connection)
	if err != nil {
		return ConnectionDTO{}, err
	}
	if !capabilities.CanView {
		return ConnectionDTO{}, ErrNotFound
	}
	return mapConnectionDTO(connection, capabilities), nil
}

func (s *Service) capabilities(ctx context.Context, viewer ViewerRef, connection Connection) (ConnectionCapabilities, error) {
	if s.access == nil {
		return ConnectionCapabilities{}, ErrDependency
	}
	actions := []string{"scm_connection.view", "scm_connection.use", "scm_connection.manage"}
	checks := make([]authorization.CheckRequest, 0, len(actions))
	for _, action := range actions {
		checks = append(checks, authorization.CheckRequest{Principal: viewerPrincipal(viewer), Action: action, Object: identity.ObjectRef{Type: "scm_connection", ID: connection.ConnectionID, TenantID: connection.TenantID}})
	}
	decisions, err := s.access.BatchCan(ctx, checks)
	if err != nil {
		return ConnectionCapabilities{}, err
	}
	if len(decisions) != len(checks) {
		return ConnectionCapabilities{}, ErrDependency
	}
	providerSupported := s.providerSupported(connection.Provider)
	return ConnectionCapabilities{
		CanView: decisions[0].Allowed, CanUse: decisions[1].Allowed && connection.Status == StatusActive && providerSupported, CanManage: decisions[2].Allowed,
		CanVerify: decisions[2].Allowed && connection.Status != StatusRevoked && providerSupported,
		CanRotate: decisions[2].Allowed && connection.Provider != ProviderManagedLocal && connection.Status != StatusRevoked,
		CanRevoke: decisions[2].Allowed && connection.Status != StatusRevoked,
	}, nil
}

func (s *Service) authorize(ctx context.Context, viewer ViewerRef, action string, connection Connection) error {
	if s.access == nil {
		return ErrDependency
	}
	return s.access.Can(ctx, viewerPrincipal(viewer), action, identity.ObjectRef{Type: "scm_connection", ID: connection.ConnectionID, TenantID: connection.TenantID})
}

func (s *Service) authorizeCreate(ctx context.Context, viewer ViewerRef, organizationID string) error {
	if s.access == nil {
		return ErrDependency
	}
	object := identity.ObjectRef{Type: "platform", ID: "global", TenantID: viewerTenant(viewer)}
	if organizationID != "" {
		object = identity.ObjectRef{Type: "organization", ID: organizationID, TenantID: viewerTenant(viewer)}
	}
	return s.access.Can(ctx, viewerPrincipal(viewer), "scm_connection.create", object)
}

func mapConnectionDTO(connection Connection, capabilities ConnectionCapabilities) ConnectionDTO {
	baseURL := connection.BaseURL
	if connection.Provider == ProviderManagedLocal {
		baseURL = ""
	}
	return ConnectionDTO{
		ConnectionID: connection.ConnectionID, OrganizationID: connection.OrganizationID, OwnerID: connection.OwnerID,
		Scope: connection.Scope, Provider: connection.Provider, BaseURL: baseURL,
		InstallationOrAccountID:   connection.InstallationOrAccountID,
		AllowedRepositoryPatterns: append([]string(nil), connection.AllowedRepositoryPatterns...), Status: connection.Status,
		LastVerifiedAt: connection.LastVerifiedAt, CreatedAt: connection.CreatedAt, UpdatedAt: connection.UpdatedAt,
		Version: connection.Version, Capabilities: capabilities,
	}
}

func normalizeCreateCommand(cmd CreateCommand) CreateCommand {
	cmd.IdempotencyKey = strings.TrimSpace(cmd.IdempotencyKey)
	cmd.ConnectionID = strings.TrimSpace(cmd.ConnectionID)
	cmd.OrganizationID = strings.TrimSpace(cmd.OrganizationID)
	cmd.Scope = strings.ToLower(strings.TrimSpace(cmd.Scope))
	cmd.Provider = strings.ToLower(strings.TrimSpace(cmd.Provider))
	cmd.BaseURL = strings.TrimSpace(cmd.BaseURL)
	if cmd.Provider == ProviderManagedLocal && cmd.BaseURL != "" {
		cmd.BaseURL = filepath.Clean(cmd.BaseURL)
	}
	cmd.InstallationOrAccountID = strings.TrimSpace(cmd.InstallationOrAccountID)
	cmd.SecretRef = strings.TrimSpace(cmd.SecretRef)
	cmd.AllowedRepositoryPatterns = normalizePatterns(cmd.AllowedRepositoryPatterns)
	return cmd
}

func scmConnectionCreateRequestHash(cmd CreateCommand) (string, error) {
	canonical := cmd
	canonical.IdempotencyKey = ""
	canonical.ConnectionID = ""
	payload, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("%w: encode create request", ErrDependency)
	}
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("%x", digest[:]), nil
}

func connectionMutationRequestHash(operation string, command any) (string, error) {
	payload, err := json.Marshal(struct {
		Operation string `json:"operation"`
		Command   any    `json:"command"`
	}{Operation: operation, Command: command})
	if err != nil {
		return "", fmt.Errorf("%w: encode mutation request", ErrDependency)
	}
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("%x", digest[:]), nil
}

func connectionMutationReplay(connection Connection, idempotencyKey, operation, requestHash string) (MutationReceipt, bool, error) {
	if idempotencyKey == "" {
		return MutationReceipt{}, false, nil
	}
	for _, receipt := range connection.MutationReceipts {
		if receipt.IdempotencyKey != idempotencyKey {
			continue
		}
		if receipt.Operation != operation || receipt.RequestHash != requestHash {
			return MutationReceipt{}, false, ErrIdempotency
		}
		return receipt, true, nil
	}
	return MutationReceipt{}, false, nil
}

func appendConnectionMutationReceipt(connection *Connection, receipt MutationReceipt) {
	if connection == nil || receipt.IdempotencyKey == "" {
		return
	}
	if receipt.Verification != nil {
		verification := *receipt.Verification
		receipt.Verification = &verification
	}
	connection.MutationReceipts = append(connection.MutationReceipts, receipt)
	const maxReceipts = 128
	if len(connection.MutationReceipts) > maxReceipts {
		connection.MutationReceipts = append([]MutationReceipt(nil), connection.MutationReceipts[len(connection.MutationReceipts)-maxReceipts:]...)
	}
}

func deterministicResourceID(prefix, tenantID, idempotencyKey string) string {
	digest := sha256.Sum256([]byte(prefix + "\x00" + tenantID + "\x00" + idempotencyKey))
	return fmt.Sprintf("%s_%x", prefix, digest[:16])
}

func validateCreateCommand(viewer ViewerRef, cmd CreateCommand) error {
	if !oneOf(cmd.Scope, ScopePersonal, ScopeOrganization, ScopePlatform) || !oneOf(cmd.Provider, ProviderGitHub, ProviderGitLab, ProviderGitea, ProviderManagedLocal, ProviderGenericGit) {
		return fmt.Errorf("%w: scope and provider are invalid", ErrInvalidRequest)
	}
	if cmd.Scope == ScopeOrganization && cmd.OrganizationID == "" {
		return fmt.Errorf("%w: organization_id is required", ErrInvalidRequest)
	}
	if cmd.Scope != ScopeOrganization && cmd.OrganizationID != "" {
		return fmt.Errorf("%w: organization_id is only valid for organization connections", ErrInvalidRequest)
	}
	if cmd.Provider == ProviderManagedLocal && cmd.Scope != ScopePlatform {
		return fmt.Errorf("%w: managed_local connections must use platform scope", ErrInvalidRequest)
	}
	if cmd.SecretRef != "" && !validSecretRef(cmd.SecretRef) {
		return fmt.Errorf("%w: secret_ref is invalid", ErrInvalidRequest)
	}
	if cmd.Provider == ProviderManagedLocal && cmd.SecretRef != "" {
		return fmt.Errorf("%w: managed_local connections must not reference a secret", ErrInvalidRequest)
	}
	if err := validateConnectionMetadata(cmd.Provider, cmd.InstallationOrAccountID, cmd.AllowedRepositoryPatterns); err != nil {
		return err
	}
	if err := validateConnectionLocation(viewer, cmd.Provider, cmd.BaseURL); err != nil {
		return err
	}
	return nil
}

func validateConnectionMetadata(provider, installationOrAccountID string, patterns []string) error {
	if len(installationOrAccountID) > 256 || containsControl(installationOrAccountID) {
		return fmt.Errorf("%w: installation_or_account_id is invalid", ErrInvalidRequest)
	}
	if provider == ProviderManagedLocal && installationOrAccountID != "" {
		return fmt.Errorf("%w: managed_local connections must not identify a remote account", ErrInvalidRequest)
	}
	if len(patterns) > 128 {
		return fmt.Errorf("%w: too many allowed repository patterns", ErrInvalidRequest)
	}
	for _, pattern := range patterns {
		if pattern == "" || len(pattern) > 256 || containsControl(pattern) || strings.ContainsAny(pattern, "?@") || strings.Contains(pattern, "://") || strings.Count(pattern, "*") > 1 || (strings.Contains(pattern, "*") && !strings.HasSuffix(pattern, "*")) {
			return fmt.Errorf("%w: allowed repository pattern is invalid", ErrInvalidRequest)
		}
	}
	return nil
}

func containsControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}

func validResourceID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 256 && !containsControl(value) && !strings.ContainsAny(value, "/\\")
}

func validSubjectID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 256 && !containsControl(value)
}

func validIdempotencyKey(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 256 && !containsControl(value)
}

func validateConnectionLocation(viewer ViewerRef, provider, baseURL string) error {
	if provider == ProviderManagedLocal {
		if !hasScope(viewer.PermissionScope, "connection.manage") || !hasScope(viewer.PermissionScope, "settings.manage") {
			return fmt.Errorf("%w: managed_local requires platform administration", authorization.ErrDenied)
		}
		if baseURL == "" || !filepath.IsAbs(baseURL) {
			return fmt.Errorf("%w: managed_local base_url must be an absolute path", ErrInvalidRequest)
		}
		return nil
	}
	if baseURL == "" {
		return fmt.Errorf("%w: base_url is required", ErrInvalidRequest)
	}
	if len(baseURL) > 2048 || strings.ContainsAny(baseURL, "\r\n\x00") {
		return fmt.Errorf("%w: base_url is invalid", ErrInvalidRequest)
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "ssh") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("%w: base_url must be an HTTPS or SSH URL without embedded credentials, query, or fragment", ErrInvalidRequest)
	}
	return nil
}

func validSecretRef(value string) bool {
	if value == "" || len(value) > 256 || strings.ContainsAny(value, "\r\n\t ") {
		return false
	}
	if strings.Contains(value, "@") || strings.Contains(value, "?") {
		return false
	}
	return strings.HasPrefix(value, "secret://") || strings.HasPrefix(value, "settings://")
}

func normalizePatterns(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func viewerPrincipal(viewer ViewerRef) identity.Principal {
	subjectID := viewerSubjectID(viewer)
	return identity.NormalizePrincipal(identity.Principal{SubjectProvider: "internal", Subject: subjectID, UserID: subjectID, Username: subjectID, ViewerKind: viewer.ViewerKind, TenantID: viewerTenant(viewer), CoarseScopes: append([]string(nil), viewer.PermissionScope...)})
}

func viewerSubject(viewer ViewerRef) authorization.SubjectRef {
	return authorization.SubjectRef{Kind: viewer.ViewerKind, ID: viewerSubjectID(viewer)}
}

func viewerSubjectID(viewer ViewerRef) string {
	if value := strings.TrimSpace(viewer.AuthorizationSubjectID); value != "" {
		return value
	}
	return strings.TrimSpace(viewer.ViewerID)
}

func viewerTenant(viewer ViewerRef) string { return normalizeTenantID(viewer.TenantID) }

func validateViewer(viewer ViewerRef) error {
	if !oneOf(strings.ToLower(strings.TrimSpace(viewer.ViewerKind)), identity.ViewerKindUser, identity.ViewerKindAgent) ||
		!validSubjectID(strings.TrimSpace(viewer.ViewerID)) ||
		!validSubjectID(viewerSubjectID(viewer)) ||
		!validTenantKeySegment(viewerTenant(viewer)) {
		return ErrInvalidRequest
	}
	return nil
}

func hasScope(scopes []string, wanted string) bool {
	wanted = strings.ToLower(strings.TrimSpace(wanted))
	for _, scope := range scopes {
		if strings.ToLower(strings.TrimSpace(scope)) == wanted {
			return true
		}
	}
	return false
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func pageBounds(offset, limit, total int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset+limit > total {
		limit = total - offset
	}
	return offset, limit
}

func (s *Service) validateSecretReference(ctx context.Context, ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil
	}
	if s == nil || s.secrets == nil {
		return ErrDependency
	}
	if err := s.secrets.ValidateSecretReference(ctx, ref); err != nil {
		return fmt.Errorf("%w: secret reference cannot be resolved: %v", ErrInvalidRequest, err)
	}
	return nil
}

func (s *Service) providerSupported(provider string) bool {
	return s != nil && s.verifier != nil && s.verifier.SupportsProvider(strings.ToLower(strings.TrimSpace(provider)))
}

func (s *Service) newID(prefix string) (string, error) {
	if s.ids == nil {
		return idgen.NewRandom(prefix).NewIDContext()
	}
	return s.ids.NewIDContext()
}

func (s *Service) now() time.Time {
	if s.clock == nil {
		return time.Now().UTC()
	}
	return s.clock.Now().UTC()
}
