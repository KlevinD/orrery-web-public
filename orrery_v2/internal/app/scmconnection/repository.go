package scmconnection

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/platform/db"
)

const connectionTable = "core.scm_connections"

type Repository interface {
	Create(ctx context.Context, tenantID string, connection Connection) error
	Update(ctx context.Context, tenantID string, connection Connection, expectedVersion int64) error
	Get(ctx context.Context, tenantID, connectionID string) (Connection, error)
	List(ctx context.Context, tenantID string) ([]Connection, error)
	ListCatalog(ctx context.Context, afterKey string, limit int) ([]Connection, bool, error)
	Delete(ctx context.Context, tenantID, connectionID string) error
}

type PlatformRepository struct {
	router *db.Router
}

func NewPlatformRepository(router *db.Router) *PlatformRepository {
	return &PlatformRepository{router: router}
}

func (r *PlatformRepository) Create(ctx context.Context, tenantID string, connection Connection) error {
	if r == nil || r.router == nil {
		return ErrDependency
	}
	connection.TenantID = normalizeTenantID(tenantID)
	if !validTenantKeySegment(connection.TenantID) || !validResourceID(connection.ConnectionID) {
		return ErrInvalidRequest
	}
	created, err := r.router.CreateRecord(ctx, connectionTable, connectionKey(connection.TenantID, connection.ConnectionID), connectionRecord(connection))
	if err != nil {
		return err
	}
	if !created {
		return ErrConflict
	}
	return nil
}

func (r *PlatformRepository) Update(ctx context.Context, tenantID string, connection Connection, expectedVersion int64) error {
	if r == nil || r.router == nil {
		return ErrDependency
	}
	tenantID = normalizeTenantID(tenantID)
	if !validTenantKeySegment(tenantID) || !validResourceID(connection.ConnectionID) {
		return ErrInvalidRequest
	}
	key := connectionKey(tenantID, connection.ConnectionID)
	current, ok, err := r.router.GetRecord(ctx, connectionTable, key)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	if expectedVersion <= 0 || recordInt64(current["version"]) != expectedVersion {
		return ErrConflict
	}
	connection.TenantID = tenantID
	swapped, err := r.router.CompareAndSwapRecord(ctx, connectionTable, key, current, connectionRecord(connection))
	if err != nil {
		return err
	}
	if !swapped {
		return ErrConflict
	}
	return nil
}

func (r *PlatformRepository) Get(ctx context.Context, tenantID, connectionID string) (Connection, error) {
	if r == nil || r.router == nil {
		return Connection{}, ErrDependency
	}
	tenantID = normalizeTenantID(tenantID)
	connectionID = strings.TrimSpace(connectionID)
	if !validTenantKeySegment(tenantID) || !validResourceID(connectionID) {
		return Connection{}, ErrInvalidRequest
	}
	record, ok, err := r.router.GetRecord(ctx, connectionTable, connectionKey(tenantID, connectionID))
	if err != nil {
		return Connection{}, err
	}
	if !ok {
		return Connection{}, ErrNotFound
	}
	return connectionFromRecord(record, tenantID, connectionID)
}

func (r *PlatformRepository) List(ctx context.Context, tenantID string) ([]Connection, error) {
	if r == nil || r.router == nil {
		return nil, ErrDependency
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	store, table, err := r.router.StoreForTable(connectionTable)
	if err != nil {
		return nil, err
	}
	records, err := store.ListRecordTable(ctx, table)
	if err != nil {
		return nil, err
	}
	tenantID = normalizeTenantID(tenantID)
	out := make([]Connection, 0)
	for key, record := range records {
		connection, err := connectionFromRecord(record, "", "")
		if err != nil {
			return nil, err
		}
		if connectionKey(connection.TenantID, connection.ConnectionID) != key {
			return nil, fmt.Errorf("%w: SCM connection record key does not match its identity", ErrDependency)
		}
		if connection.TenantID == tenantID {
			out = append(out, connection)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].ConnectionID < out[j].ConnectionID
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

func (r *PlatformRepository) ListCatalog(ctx context.Context, afterKey string, limit int) ([]Connection, bool, error) {
	if r == nil || r.router == nil {
		return nil, false, ErrDependency
	}
	if limit <= 0 {
		return nil, false, nil
	}
	page, err := r.router.ListRecords(ctx, connectionTable, strings.TrimSpace(afterKey), limit)
	if err != nil {
		return nil, false, err
	}
	out := make([]Connection, 0, len(page.Items))
	for _, item := range page.Items {
		connection, err := connectionFromRecord(item.Record, "", "")
		if err != nil {
			return nil, false, err
		}
		if connectionKey(connection.TenantID, connection.ConnectionID) != item.Key {
			return nil, false, fmt.Errorf("%w: SCM connection record key does not match its identity", ErrDependency)
		}
		out = append(out, connection)
	}
	return out, page.HasMore, nil
}

func (r *PlatformRepository) Delete(ctx context.Context, tenantID, connectionID string) error {
	if r == nil || r.router == nil {
		return ErrDependency
	}
	return r.router.DeleteRecord(ctx, connectionTable, connectionKey(tenantID, connectionID))
}

func connectionRecord(connection Connection) db.Record {
	payload, _ := json.Marshal(connection)
	return db.Record{
		"tenant_id":       normalizeTenantID(connection.TenantID),
		"connection_id":   strings.TrimSpace(connection.ConnectionID),
		"organization_id": strings.TrimSpace(connection.OrganizationID),
		"owner_id":        strings.TrimSpace(connection.OwnerID),
		"provider":        connection.Provider,
		"status":          connection.Status,
		"version":         connection.Version,
		"payload":         string(payload),
	}
}

func connectionFromRecord(record db.Record, expectedTenantID, expectedConnectionID string) (Connection, error) {
	payload, _ := record["payload"].(string)
	if payload == "" {
		return Connection{}, fmt.Errorf("%w: scm connection payload is missing", ErrDependency)
	}
	var connection Connection
	if err := json.Unmarshal([]byte(payload), &connection); err != nil {
		return Connection{}, fmt.Errorf("decode scm connection: %w", err)
	}
	connection.TenantID = normalizeTenantID(connection.TenantID)
	connection.ConnectionID = strings.TrimSpace(connection.ConnectionID)
	recordTenantID, _ := record["tenant_id"].(string)
	recordTenantID = normalizeTenantID(recordTenantID)
	recordConnectionID, _ := record["connection_id"].(string)
	recordConnectionID = strings.TrimSpace(recordConnectionID)
	if !validTenantKeySegment(connection.TenantID) || !validResourceID(connection.ConnectionID) ||
		connection.TenantID != recordTenantID || connection.ConnectionID != recordConnectionID ||
		strings.TrimSpace(connection.OrganizationID) != recordString(record, "organization_id") ||
		strings.TrimSpace(connection.OwnerID) != recordString(record, "owner_id") ||
		connection.Provider != recordString(record, "provider") || connection.Status != recordString(record, "status") ||
		connection.Version <= 0 || connection.Version != recordInt64(record["version"]) ||
		(expectedTenantID != "" && connection.TenantID != normalizeTenantID(expectedTenantID)) ||
		(expectedConnectionID != "" && connection.ConnectionID != strings.TrimSpace(expectedConnectionID)) {
		return Connection{}, fmt.Errorf("%w: SCM connection record identity is inconsistent", ErrDependency)
	}
	if !validSubjectID(connection.OwnerID) || (connection.OrganizationID != "" && !validSubjectID(connection.OrganizationID)) {
		return Connection{}, fmt.Errorf("%w: SCM connection ownership identity is inconsistent", ErrDependency)
	}
	if validatePersistedConnection(connection) != nil {
		return Connection{}, fmt.Errorf("%w: SCM connection aggregate is invalid", ErrDependency)
	}
	effectIDs := make(map[string]struct{}, len(connection.PendingEffects))
	for _, effect := range connection.PendingEffects {
		if validateConnectionPendingEffect(connection, effect) != nil {
			return Connection{}, fmt.Errorf("%w: SCM connection pending effect is invalid", ErrDependency)
		}
		if _, exists := effectIDs[effect.EffectID]; exists {
			return Connection{}, fmt.Errorf("%w: SCM connection pending effect is duplicated", ErrDependency)
		}
		effectIDs[effect.EffectID] = struct{}{}
	}
	if len(connection.MutationReceipts) > 128 {
		return Connection{}, fmt.Errorf("%w: SCM connection has too many mutation receipts", ErrDependency)
	}
	receiptKeys := make(map[string]struct{}, len(connection.MutationReceipts))
	for _, receipt := range connection.MutationReceipts {
		if validateConnectionMutationReceipt(receipt) != nil {
			return Connection{}, fmt.Errorf("%w: SCM connection mutation receipt is invalid", ErrDependency)
		}
		if _, exists := receiptKeys[receipt.IdempotencyKey]; exists {
			return Connection{}, fmt.Errorf("%w: SCM connection mutation receipt is duplicated", ErrDependency)
		}
		receiptKeys[receipt.IdempotencyKey] = struct{}{}
	}
	if (connection.CreateRequestHash != "" && !validRequestHash(connection.CreateRequestHash)) ||
		(connection.CreateIdempotencyKey != "" && (!validIdempotencyKey(connection.CreateIdempotencyKey) || connection.CreateRequestHash == "")) {
		return Connection{}, fmt.Errorf("%w: SCM connection create receipt is invalid", ErrDependency)
	}
	return connection, nil
}

func validatePersistedConnection(connection Connection) error {
	if !oneOf(connection.Scope, ScopePersonal, ScopeOrganization, ScopePlatform) ||
		!oneOf(connection.Provider, ProviderGitHub, ProviderGitLab, ProviderGitea, ProviderManagedLocal, ProviderGenericGit) ||
		!oneOf(connection.Status, StatusActive, StatusExpired, StatusRevoked, StatusError) ||
		connection.Version <= 0 || connection.CreatedAt.IsZero() || connection.UpdatedAt.Before(connection.CreatedAt) {
		return ErrDependency
	}
	if (connection.Scope == ScopeOrganization) != (connection.OrganizationID != "") ||
		(connection.Provider == ProviderManagedLocal) != (connection.Scope == ScopePlatform) {
		return ErrDependency
	}
	if validateConnectionMetadata(connection.Provider, connection.InstallationOrAccountID, connection.AllowedRepositoryPatterns) != nil ||
		validatePersistedConnectionLocation(connection.Provider, connection.BaseURL) != nil {
		return ErrDependency
	}
	if connection.Provider == ProviderManagedLocal {
		if connection.SecretRef != "" {
			return ErrDependency
		}
	} else if connection.SecretRef != "" && !validSecretRef(connection.SecretRef) {
		return ErrDependency
	}
	return nil
}

func validatePersistedConnectionLocation(provider, baseURL string) error {
	if provider == ProviderManagedLocal {
		if baseURL == "" || !filepath.IsAbs(baseURL) {
			return ErrDependency
		}
		return nil
	}
	if baseURL == "" || len(baseURL) > 2048 || strings.ContainsAny(baseURL, "\r\n\x00") {
		return ErrDependency
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "ssh") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ErrDependency
	}
	return nil
}

func validateConnectionPendingEffect(connection Connection, effect PendingEffect) error {
	if strings.TrimSpace(effect.EffectID) == "" || len(effect.EffectID) > 2048 || containsControl(effect.EffectID) {
		return ErrDependency
	}
	switch effect.Kind {
	case effectWriteConnectionCreated:
		if !oneOf(effect.SubjectKind, identity.ViewerKindUser, identity.ViewerKindAgent) || !validSubjectID(effect.SubjectID) || effect.Audit != nil {
			return ErrDependency
		}
	case effectRecordAudit:
		if effect.SubjectKind != "" || effect.SubjectID != "" || validateConnectionAudit(connection, effect) != nil {
			return ErrDependency
		}
	default:
		return ErrDependency
	}
	return nil
}

func validateConnectionAudit(connection Connection, effect PendingEffect) error {
	if effect.Audit == nil {
		return ErrDependency
	}
	audit := effect.Audit
	if audit.AuditID != effect.EffectID || normalizeTenantID(audit.TenantID) != connection.TenantID ||
		audit.EntityID != connection.ConnectionID || !validSubjectID(audit.ActorID) ||
		strings.TrimSpace(audit.Action) == "" || strings.TrimSpace(audit.Result) == "" ||
		strings.TrimSpace(audit.Summary) == "" || audit.OccurredAt.IsZero() {
		return ErrDependency
	}
	for key, value := range audit.Metadata {
		if strings.TrimSpace(key) == "" || containsControl(key) || containsControl(value) {
			return ErrDependency
		}
	}
	return nil
}

func validateConnectionMutationReceipt(receipt MutationReceipt) error {
	if !validIdempotencyKey(receipt.IdempotencyKey) || !validRequestHash(receipt.RequestHash) {
		return ErrDependency
	}
	switch receipt.Operation {
	case "update", "rotate", "revoke":
		if receipt.Verification != nil || receipt.Failure != "" {
			return ErrDependency
		}
	case "verify":
		if (receipt.Verification == nil) == (receipt.Failure == "") {
			return ErrDependency
		}
		if receipt.Verification != nil && validatePersistedVerification(*receipt.Verification) != nil {
			return ErrDependency
		}
	default:
		return ErrDependency
	}
	return nil
}

func validatePersistedVerification(verification Verification) error {
	repository := verification.Repository
	if verification.VerifiedAt.IsZero() || repository.RepositoryLocator == "" || len(repository.RepositoryLocator) > 256 || containsControl(repository.RepositoryLocator) ||
		repository.DisplayName == "" || len(repository.DisplayName) > 256 || containsControl(repository.DisplayName) ||
		!validGitRefPart(repository.DefaultBranch) || !validGitObjectID(repository.HeadCommit) {
		return ErrDependency
	}
	return nil
}

func validGitRefPart(value string) bool {
	if value == "" || len(value) > 255 || strings.HasPrefix(value, "-") || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") || strings.HasSuffix(strings.ToLower(value), ".lock") || strings.Contains(value, "..") || strings.Contains(value, "@{") || strings.Contains(value, "//") || strings.ContainsAny(value, " ~^:?*[\\\x00") {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".") || strings.HasSuffix(strings.ToLower(component), ".lock") {
			return false
		}
	}
	return true
}

func validGitObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validRequestHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func connectionKey(tenantID, connectionID string) string {
	return normalizeTenantID(tenantID) + "/" + strings.TrimSpace(connectionID)
}

func normalizeTenantID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "global"
	}
	return value
}

func validTenantKeySegment(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 256 && !strings.ContainsAny(value, "/\x00\r\n")
}

func recordInt64(value any) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	default:
		return 0
	}
}

func recordString(record db.Record, field string) string {
	value, _ := record[field].(string)
	return strings.TrimSpace(value)
}
