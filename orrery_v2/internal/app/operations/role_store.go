package operations

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

const (
	roleDefinitionTable         = "core.operations_role_definitions"
	roleAssignmentTable         = "core.operations_role_assignments"
	permissionPolicyStateTable  = "core.operations_permission_policy_state"
	permissionPolicyRevisionKey = "revision"
)

type RoleDefinitionRecordPage struct {
	Items   []RoleDefinitionDTO
	HasMore bool
}

type RoleAssignmentRecordPage struct {
	Items   []RoleAssignmentDTO
	HasMore bool
}

type RoleStore struct {
	router *db.Router
}

func NewRoleStore(router *db.Router) *RoleStore {
	return &RoleStore{router: router}
}

func (s *RoleStore) SaveRoleDefinition(ctx context.Context, role RoleDefinitionDTO) error {
	return s.SaveRoleDefinitions(ctx, []RoleDefinitionDTO{role})
}

func (s *RoleStore) SaveRoleDefinitions(ctx context.Context, roles []RoleDefinitionDTO) error {
	if s == nil || s.router == nil {
		return ErrServiceDependency
	}
	for _, role := range roles {
		if _, builtIn := builtInRoleDefinition(role.RoleID); builtIn || role.BuiltIn {
			return fmt.Errorf("%w: built-in roles are managed by the server", ErrInvalidRequest)
		}
	}
	if len(roles) == 0 {
		return nil
	}
	store, table, err := s.router.StoreForTable(roleDefinitionTable)
	if err != nil {
		return err
	}
	tx, err := store.BeginTx(ctx)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	stateTable, err := s.permissionPolicyStateTable(store)
	if err != nil {
		return err
	}
	if err := db.LockRecordKeyTx(ctx, tx, stateTable, permissionPolicyRevisionKey); err != nil {
		return err
	}
	revision, err := permissionPolicyRevisionTx(ctx, tx, stateTable)
	if err != nil {
		return err
	}
	for _, role := range roles {
		if err := db.PutRecordTx(ctx, tx, table, role.RoleID, roleDefinitionRecord(role)); err != nil {
			return fmt.Errorf("save role definition %s: %w", role.RoleID, err)
		}
	}
	if err := db.PutRecordTx(ctx, tx, stateTable, permissionPolicyRevisionKey, db.Record{"revision": revision + 1}); err != nil {
		return fmt.Errorf("save permission policy revision: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit role definitions: %w", err)
	}
	committed = true
	return nil
}

func (s *RoleStore) ApplyPermissionRoleDefinitions(ctx context.Context, expectedRevision int64, roles []RoleDefinitionDTO) (int64, error) {
	if s == nil || s.router == nil {
		return 0, ErrServiceDependency
	}
	if expectedRevision < 0 {
		return 0, fmt.Errorf("%w: expected revision cannot be negative", ErrInvalidRequest)
	}
	if len(roles) == 0 {
		revision, err := s.PermissionPolicyRevision(ctx)
		if err != nil {
			return 0, err
		}
		if revision != expectedRevision {
			return 0, fmt.Errorf("%w: expected revision %d, current revision %d", ErrVersionConflict, expectedRevision, revision)
		}
		return revision, nil
	}
	for _, role := range roles {
		if _, builtIn := builtInRoleDefinition(role.RoleID); builtIn || role.BuiltIn {
			return 0, fmt.Errorf("%w: built-in roles are managed by the server", ErrInvalidRequest)
		}
	}
	store, table, err := s.router.StoreForTable(roleDefinitionTable)
	if err != nil {
		return 0, err
	}
	stateTable, err := s.permissionPolicyStateTable(store)
	if err != nil {
		return 0, err
	}
	tx, err := store.BeginTx(ctx)
	if err != nil {
		return 0, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := db.LockRecordKeyTx(ctx, tx, stateTable, permissionPolicyRevisionKey); err != nil {
		return 0, err
	}
	currentRevision, err := permissionPolicyRevisionTx(ctx, tx, stateTable)
	if err != nil {
		return 0, err
	}
	if currentRevision != expectedRevision {
		return 0, fmt.Errorf("%w: expected revision %d, current revision %d", ErrVersionConflict, expectedRevision, currentRevision)
	}
	for _, role := range roles {
		if err := db.PutRecordTx(ctx, tx, table, role.RoleID, roleDefinitionRecord(role)); err != nil {
			return 0, fmt.Errorf("save role definition %s: %w", role.RoleID, err)
		}
	}
	nextRevision := currentRevision + 1
	if err := db.PutRecordTx(ctx, tx, stateTable, permissionPolicyRevisionKey, db.Record{"revision": nextRevision}); err != nil {
		return 0, fmt.Errorf("save permission policy revision: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit permission policy batch: %w", err)
	}
	committed = true
	return nextRevision, nil
}

func (s *RoleStore) PermissionPolicyRevision(ctx context.Context) (int64, error) {
	if s == nil || s.router == nil {
		return 0, ErrServiceDependency
	}
	record, found, err := s.router.GetRecord(ctx, permissionPolicyStateTable, permissionPolicyRevisionKey)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, nil
	}
	return recordInt64(record, "revision"), nil
}

func (s *RoleStore) GetRoleDefinition(ctx context.Context, roleID string) (RoleDefinitionDTO, bool, error) {
	if s == nil || s.router == nil {
		return RoleDefinitionDTO{}, false, ErrServiceDependency
	}
	record, found, err := s.router.GetRecord(ctx, roleDefinitionTable, strings.TrimSpace(roleID))
	if err != nil || !found {
		return RoleDefinitionDTO{}, found, err
	}
	return roleDefinitionFromRecord(record), true, nil
}

func (s *RoleStore) ListRoleDefinitionsPage(ctx context.Context, afterRoleID string, limit int) (RoleDefinitionRecordPage, error) {
	if s == nil || s.router == nil {
		return RoleDefinitionRecordPage{}, ErrServiceDependency
	}
	if limit <= 0 || limit > db.MaxRecordPageLimit {
		return RoleDefinitionRecordPage{}, fmt.Errorf("%w: role page limit is invalid", ErrInvalidRequest)
	}
	page, err := s.router.ListRecords(ctx, roleDefinitionTable, strings.TrimSpace(afterRoleID), limit)
	if err != nil {
		return RoleDefinitionRecordPage{}, err
	}
	roles := make([]RoleDefinitionDTO, 0, len(page.Items))
	for _, item := range page.Items {
		roles = append(roles, roleDefinitionFromRecord(item.Record))
	}
	return RoleDefinitionRecordPage{Items: roles, HasMore: page.HasMore}, nil
}

func (s *RoleStore) permissionPolicyStateTable(store *db.Store) (string, error) {
	stateStore, table, err := s.router.StoreForTable(permissionPolicyStateTable)
	if err != nil {
		return "", err
	}
	if stateStore != store {
		return "", fmt.Errorf("%w: role definitions and policy revision must share a database", ErrServiceDependency)
	}
	return table, nil
}

func permissionPolicyRevisionTx(ctx context.Context, tx db.Tx, stateTable string) (int64, error) {
	record, found, err := db.GetRecordTx(ctx, tx, stateTable, permissionPolicyRevisionKey)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, nil
	}
	return recordInt64(record, "revision"), nil
}

func roleDefinitionRecord(role RoleDefinitionDTO) db.Record {
	role.PermissionRefs = canonicalPermissionScopes(role.PermissionRefs)
	return db.Record{
		"role_id":                role.RoleID,
		"name":                   role.Name,
		"description":            role.Description,
		"category":               role.Category,
		"status":                 firstNonEmpty(role.Status, organizationStatusActive),
		"assignable_actor_types": stringSliceToJSON(role.AssignableActorTypes),
		"built_in":               false,
		"permission_refs":        stringSliceToJSON(role.PermissionRefs),
		"approval_capability":    role.ApprovalCapability,
	}
}

func (s *RoleStore) ListRoleDefinitions(ctx context.Context) ([]RoleDefinitionDTO, error) {
	if s == nil || s.router == nil {
		return nil, ErrServiceDependency
	}
	records, err := s.records(ctx, roleDefinitionTable)
	if err != nil {
		return nil, err
	}
	roles := make([]RoleDefinitionDTO, 0, len(records))
	for _, record := range records {
		roles = append(roles, roleDefinitionFromRecord(record))
	}
	sort.SliceStable(roles, func(i, j int) bool { return roles[i].RoleID < roles[j].RoleID })
	return roles, nil
}

func (s *RoleStore) SaveRoleAssignment(ctx context.Context, assignment RoleAssignmentDTO) error {
	if s == nil || s.router == nil {
		return ErrServiceDependency
	}
	return s.router.PutRecord(ctx, roleAssignmentTable, assignment.AssignmentID, db.Record{
		"assignment_id":        assignment.AssignmentID,
		"actor_type":           assignment.Actor.ActorType,
		"actor_id":             assignment.Actor.ActorID,
		"role_id":              assignment.RoleID,
		"organization_node_id": assignment.OrganizationNodeID,
		"organization_code":    normalizeOrganizationCodeForQuery(assignment.OrganizationCode),
		"scope_mode":           firstNonEmpty(assignment.ScopeMode, RoleScopeSelf),
		"source":               firstNonEmpty(assignment.Source, RoleSourceDirect),
		"starts_at":            timeToRecordString(assignment.StartsAt),
		"expires_at":           timeToRecordString(assignment.ExpiresAt),
		"assigned_by":          assignment.AssignedBy,
	})
}

func (s *RoleStore) DeleteRoleAssignment(ctx context.Context, assignmentID string) error {
	if s == nil || s.router == nil {
		return ErrServiceDependency
	}
	return s.router.DeleteRecord(ctx, roleAssignmentTable, assignmentID)
}

func (s *RoleStore) ListRoleAssignments(ctx context.Context, query RoleAssignmentQuery) ([]RoleAssignmentDTO, error) {
	if s == nil || s.router == nil {
		return nil, ErrServiceDependency
	}
	records, err := s.records(ctx, roleAssignmentTable)
	if err != nil {
		return nil, err
	}
	assignments := make([]RoleAssignmentDTO, 0, len(records))
	for _, record := range records {
		assignment := roleAssignmentFromRecord(record)
		if !matchesRoleAssignmentQuery(assignment, query) {
			continue
		}
		assignments = append(assignments, assignment)
	}
	sort.SliceStable(assignments, func(i, j int) bool {
		if assignments[i].OrganizationCode != assignments[j].OrganizationCode {
			return assignments[i].OrganizationCode < assignments[j].OrganizationCode
		}
		return assignments[i].AssignmentID < assignments[j].AssignmentID
	})
	return assignments, nil
}

func (s *RoleStore) GetRoleAssignment(ctx context.Context, assignmentID string) (RoleAssignmentDTO, bool, error) {
	if s == nil || s.router == nil {
		return RoleAssignmentDTO{}, false, ErrServiceDependency
	}
	record, found, err := s.router.GetRecord(ctx, roleAssignmentTable, strings.TrimSpace(assignmentID))
	if err != nil || !found {
		return RoleAssignmentDTO{}, found, err
	}
	return roleAssignmentFromRecord(record), true, nil
}

func (s *RoleStore) ListRoleAssignmentsPage(ctx context.Context, afterAssignmentID string, limit int) (RoleAssignmentRecordPage, error) {
	if s == nil || s.router == nil {
		return RoleAssignmentRecordPage{}, ErrServiceDependency
	}
	if limit <= 0 || limit > 100 {
		return RoleAssignmentRecordPage{}, fmt.Errorf("%w: role assignment page limit must be between 1 and 100", ErrInvalidRequest)
	}
	page, err := s.router.ListRecords(ctx, roleAssignmentTable, strings.TrimSpace(afterAssignmentID), limit)
	if err != nil {
		return RoleAssignmentRecordPage{}, err
	}
	assignments := make([]RoleAssignmentDTO, 0, len(page.Items))
	for _, item := range page.Items {
		assignments = append(assignments, roleAssignmentFromRecord(item.Record))
	}
	return RoleAssignmentRecordPage{Items: assignments, HasMore: page.HasMore}, nil
}

func (s *RoleStore) records(ctx context.Context, logicalTable string) ([]db.Record, error) {
	if s == nil || s.router == nil {
		return nil, ErrServiceDependency
	}
	return listOperationRecords(ctx, s.router, logicalTable)
}

func roleDefinitionFromRecord(record db.Record) RoleDefinitionDTO {
	return RoleDefinitionDTO{
		RoleID:               recordString(record, "role_id"),
		Name:                 recordString(record, "name"),
		Description:          recordString(record, "description"),
		Category:             recordString(record, "category"),
		Status:               firstNonEmpty(recordString(record, "status"), organizationStatusActive),
		AssignableActorTypes: stringSliceFromJSON(recordString(record, "assignable_actor_types")),
		BuiltIn:              recordBool(record, "built_in"),
		PermissionRefs:       canonicalPermissionScopes(stringSliceFromJSON(recordString(record, "permission_refs"))),
		ApprovalCapability:   recordBool(record, "approval_capability"),
	}
}

func roleAssignmentFromRecord(record db.Record) RoleAssignmentDTO {
	return RoleAssignmentDTO{
		AssignmentID: recordString(record, "assignment_id"),
		Actor: ActorRefDTO{
			ActorType: recordString(record, "actor_type"),
			ActorID:   recordString(record, "actor_id"),
		},
		RoleID:             recordString(record, "role_id"),
		OrganizationNodeID: recordString(record, "organization_node_id"),
		OrganizationCode:   recordString(record, "organization_code"),
		ScopeMode:          firstNonEmpty(recordString(record, "scope_mode"), RoleScopeSelf),
		Source:             firstNonEmpty(recordString(record, "source"), RoleSourceDirect),
		StartsAt:           recordTime(record, "starts_at"),
		ExpiresAt:          recordTime(record, "expires_at"),
		AssignedBy:         recordString(record, "assigned_by"),
	}
}

func matchesRoleAssignmentQuery(assignment RoleAssignmentDTO, query RoleAssignmentQuery) bool {
	if query.ActorType != "" && assignment.Actor.ActorType != query.ActorType {
		return false
	}
	if query.ActorID != "" && assignment.Actor.ActorID != query.ActorID {
		return false
	}
	if query.RoleID != "" && assignment.RoleID != query.RoleID {
		return false
	}
	if query.OrganizationCode != "" {
		code := normalizeOrganizationCodeForQuery(query.OrganizationCode)
		if assignment.OrganizationCode != code && !roleAssignmentAppliesToCode(assignment, code) {
			return false
		}
	}
	return true
}

func roleAssignmentAppliesToCode(assignment RoleAssignmentDTO, targetCode string) bool {
	code := normalizeOrganizationCodeForQuery(assignment.OrganizationCode)
	switch assignment.ScopeMode {
	case RoleScopeSelf:
		return code == targetCode
	case RoleScopeDescendants:
		return strings.HasPrefix(targetCode, code) && targetCode != code
	case RoleScopeSelfAndDescendants:
		return code == targetCode || strings.HasPrefix(targetCode, code)
	default:
		return code == targetCode
	}
}

func normalizeOrganizationCodeForQuery(code string) string {
	return strings.ReplaceAll(strings.TrimSpace(code), ".", "")
}

func stringSliceToJSON(values []string) string {
	payload, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(payload)
}

func stringSliceFromJSON(raw string) []string {
	if raw == "" {
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil
	}
	return values
}

func timeToRecordString(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func recordTime(record db.Record, key string) time.Time {
	if at, err := time.Parse(time.RFC3339Nano, recordString(record, key)); err == nil {
		return at
	}
	return time.Time{}
}

func recordBool(record db.Record, key string) bool {
	if value, ok := record[key].(bool); ok {
		return value
	}
	return false
}
