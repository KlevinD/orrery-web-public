package accesscontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"orrery_v2/internal/platform/db"
)

const PolicyRecordTable = "core.permission_policy"

var ErrServiceDependency = errors.New("access control service dependency is not configured")
var ErrInvalidRequest = errors.New("access control request is invalid")

type CapabilityDTO struct {
	CapabilityID string `json:"capability_id"`
	Domain       string `json:"domain"`
	Action       string `json:"action"`
	AccessMode   string `json:"access_mode"`
	Scope        string `json:"scope"`
	Method       string `json:"method"`
	Path         string `json:"path"`
	LabelZh      string `json:"label_zh"`
	LabelEn      string `json:"label_en"`
	CategoryZh   string `json:"category_zh"`
	CategoryEn   string `json:"category_en"`
	Description  string `json:"description,omitempty"`
}

type RoleDTO struct {
	RoleID  string `json:"role_id"`
	LabelZh string `json:"label_zh"`
	LabelEn string `json:"label_en"`
	BuiltIn bool   `json:"built_in,omitempty"`
}

type RoleAssignmentDTO struct {
	PrincipalKind string `json:"principal_kind"`
	PrincipalID   string `json:"principal_id"`
	RoleID        string `json:"role_id"`
}

type PermissionGrantDTO struct {
	GrantID      string `json:"grant_id,omitempty"`
	SubjectKind  string `json:"subject_kind"`
	SubjectID    string `json:"subject_id"`
	CapabilityID string `json:"capability_id,omitempty"`
	Scope        string `json:"scope,omitempty"`
	ResourceKind string `json:"resource_kind,omitempty"`
	ResourceID   string `json:"resource_id,omitempty"`
}

type EffectiveGrantDTO struct {
	PrincipalKind string `json:"principal_kind"`
	PrincipalID   string `json:"principal_id"`
	CapabilityID  string `json:"capability_id"`
	Scope         string `json:"scope"`
	Source        string `json:"source"`
	SourceID      string `json:"source_id,omitempty"`
}

type PolicyDTO struct {
	Roles           []RoleDTO            `json:"roles"`
	RoleAssignments []RoleAssignmentDTO  `json:"role_assignments"`
	Grants          []PermissionGrantDTO `json:"grants"`
}

type PolicyViewDTO struct {
	Capabilities    []CapabilityDTO     `json:"capabilities"`
	Policy          PolicyDTO           `json:"policy"`
	Effective       []EffectiveGrantDTO `json:"effective"`
	EnforcementMode string              `json:"enforcement_mode"`
	PolicyMutable   bool                `json:"policy_mutable"`
}

type AuthorizeRequest struct {
	PrincipalKind string
	PrincipalID   string
	CapabilityID  string
	Scope         string
}

type Store interface {
	LoadPolicy(ctx context.Context) (PolicyDTO, error)
	SavePolicy(ctx context.Context, policy PolicyDTO) error
}

type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

func RootRole() RoleDTO {
	return RoleDTO{RoleID: "root", LabelZh: "超级管理员", LabelEn: "Root"}
}

func RootRoleAssignments() []RoleAssignmentDTO {
	return []RoleAssignmentDTO{
		{PrincipalKind: "user", PrincipalID: "superadmin", RoleID: "root"},
	}
}

func (s *Service) GetPolicy(ctx context.Context) (PolicyDTO, error) {
	if s == nil || s.store == nil {
		return PolicyDTO{}, ErrServiceDependency
	}
	return s.store.LoadPolicy(ctx)
}

func (s *Service) SavePolicy(ctx context.Context, policy PolicyDTO) (PolicyDTO, error) {
	if s == nil || s.store == nil {
		return PolicyDTO{}, ErrServiceDependency
	}
	normalized, err := NormalizePolicy(policy)
	if err != nil {
		return PolicyDTO{}, err
	}
	if err := s.store.SavePolicy(ctx, normalized); err != nil {
		return PolicyDTO{}, err
	}
	return normalized, nil
}

func (s *Service) EnsureBootstrapAdminPolicy(ctx context.Context, role RoleDTO, assignments []RoleAssignmentDTO, scopes []string) (PolicyDTO, error) {
	policy, err := s.GetPolicy(ctx)
	if err != nil {
		return PolicyDTO{}, err
	}
	role.RoleID = strings.TrimSpace(role.RoleID)
	if role.RoleID == "" {
		return PolicyDTO{}, fmt.Errorf("%w: bootstrap role_id is required", ErrInvalidRequest)
	}
	seed := policy
	if !roleExists(seed.Roles, role.RoleID) {
		seed.Roles = append(seed.Roles, role)
	}
	for _, assignment := range assignments {
		assignment.PrincipalKind = normalizeKind(assignment.PrincipalKind)
		assignment.PrincipalID = strings.TrimSpace(assignment.PrincipalID)
		if strings.TrimSpace(assignment.RoleID) == "" {
			assignment.RoleID = role.RoleID
		}
		assignment.RoleID = strings.TrimSpace(assignment.RoleID)
		if assignment.PrincipalKind == "" || assignment.PrincipalID == "" || assignment.RoleID == "" {
			continue
		}
		if !assignmentExists(seed.RoleAssignments, assignment.PrincipalKind, assignment.PrincipalID, assignment.RoleID) {
			seed.RoleAssignments = append(seed.RoleAssignments, assignment)
		}
	}
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		if !scopeGrantExists(seed.Grants, "role", role.RoleID, scope) {
			seed.Grants = append(seed.Grants, PermissionGrantDTO{
				SubjectKind: "role",
				SubjectID:   role.RoleID,
				Scope:       scope,
			})
		}
	}
	return s.SavePolicy(ctx, seed)
}

func (s *Service) BuildView(ctx context.Context, capabilities []CapabilityDTO) (PolicyViewDTO, error) {
	policy, err := s.GetPolicy(ctx)
	if err != nil {
		return PolicyViewDTO{}, err
	}
	policy, err = NormalizePolicy(policy)
	if err != nil {
		return PolicyViewDTO{}, err
	}
	return PolicyViewDTO{
		Capabilities: append([]CapabilityDTO(nil), capabilities...),
		Policy:       policy,
		Effective:    EffectiveGrants(policy, capabilities),
	}, nil
}

func (s *Service) Authorize(ctx context.Context, req AuthorizeRequest) (bool, error) {
	policy, err := s.GetPolicy(ctx)
	if err != nil {
		return false, err
	}
	return PolicyAllows(policy, req), nil
}

func NormalizePolicy(policy PolicyDTO) (PolicyDTO, error) {
	out := PolicyDTO{}
	seenRoles := map[string]struct{}{}
	for _, role := range policy.Roles {
		role.RoleID = strings.TrimSpace(role.RoleID)
		role.LabelZh = strings.TrimSpace(role.LabelZh)
		role.LabelEn = strings.TrimSpace(role.LabelEn)
		if role.RoleID == "" {
			return PolicyDTO{}, fmt.Errorf("%w: role_id is required", ErrInvalidRequest)
		}
		if _, ok := seenRoles[role.RoleID]; ok {
			continue
		}
		seenRoles[role.RoleID] = struct{}{}
		out.Roles = append(out.Roles, role)
	}
	seenAssignments := map[string]struct{}{}
	for _, assignment := range policy.RoleAssignments {
		assignment.PrincipalKind = normalizeKind(assignment.PrincipalKind)
		assignment.PrincipalID = strings.TrimSpace(assignment.PrincipalID)
		assignment.RoleID = strings.TrimSpace(assignment.RoleID)
		if assignment.PrincipalKind == "" || assignment.PrincipalID == "" || assignment.RoleID == "" {
			return PolicyDTO{}, fmt.Errorf("%w: role assignment principal and role are required", ErrInvalidRequest)
		}
		key := assignment.PrincipalKind + ":" + assignment.PrincipalID + ":" + assignment.RoleID
		if _, ok := seenAssignments[key]; ok {
			continue
		}
		seenAssignments[key] = struct{}{}
		out.RoleAssignments = append(out.RoleAssignments, assignment)
	}
	seenGrants := map[string]struct{}{}
	for _, grant := range policy.Grants {
		grant.SubjectKind = normalizeKind(grant.SubjectKind)
		grant.SubjectID = strings.TrimSpace(grant.SubjectID)
		grant.CapabilityID = strings.TrimSpace(grant.CapabilityID)
		grant.Scope = strings.TrimSpace(grant.Scope)
		grant.ResourceKind = strings.TrimSpace(grant.ResourceKind)
		grant.ResourceID = strings.TrimSpace(grant.ResourceID)
		if grant.SubjectKind == "" || grant.SubjectID == "" || (grant.CapabilityID == "" && grant.Scope == "") {
			return PolicyDTO{}, fmt.Errorf("%w: grant subject and capability or scope are required", ErrInvalidRequest)
		}
		key := grant.SubjectKind + ":" + grant.SubjectID + ":" + grant.CapabilityID + ":" + grant.Scope + ":" + grant.ResourceKind + ":" + grant.ResourceID
		if _, ok := seenGrants[key]; ok {
			continue
		}
		seenGrants[key] = struct{}{}
		out.Grants = append(out.Grants, grant)
	}
	return out, nil
}

func roleExists(roles []RoleDTO, roleID string) bool {
	for _, role := range roles {
		if strings.TrimSpace(role.RoleID) == roleID {
			return true
		}
	}
	return false
}

func assignmentExists(assignments []RoleAssignmentDTO, kind, id, roleID string) bool {
	for _, assignment := range assignments {
		if normalizeKind(assignment.PrincipalKind) == kind && strings.TrimSpace(assignment.PrincipalID) == id && strings.TrimSpace(assignment.RoleID) == roleID {
			return true
		}
	}
	return false
}

func scopeGrantExists(grants []PermissionGrantDTO, subjectKind, subjectID, scope string) bool {
	for _, grant := range grants {
		if normalizeKind(grant.SubjectKind) == subjectKind && strings.TrimSpace(grant.SubjectID) == subjectID && strings.TrimSpace(grant.Scope) == scope {
			return true
		}
	}
	return false
}

func PolicyAllows(policy PolicyDTO, req AuthorizeRequest) bool {
	req.PrincipalKind = normalizeKind(req.PrincipalKind)
	req.PrincipalID = strings.TrimSpace(req.PrincipalID)
	req.CapabilityID = strings.TrimSpace(req.CapabilityID)
	req.Scope = strings.TrimSpace(req.Scope)
	if req.PrincipalKind == "" || req.PrincipalID == "" || (req.CapabilityID == "" && req.Scope == "") {
		return false
	}
	if grantsAllow(policy.Grants, req.PrincipalKind, req.PrincipalID, req.CapabilityID, req.Scope, "direct") {
		return true
	}
	for _, assignment := range policy.RoleAssignments {
		if normalizeKind(assignment.PrincipalKind) != req.PrincipalKind || strings.TrimSpace(assignment.PrincipalID) != req.PrincipalID {
			continue
		}
		if grantsAllow(policy.Grants, "role", strings.TrimSpace(assignment.RoleID), req.CapabilityID, req.Scope, "role") {
			return true
		}
	}
	return false
}

func EffectiveGrants(policy PolicyDTO, capabilities []CapabilityDTO) []EffectiveGrantDTO {
	capByID := map[string]CapabilityDTO{}
	capByScope := map[string][]CapabilityDTO{}
	for _, capability := range capabilities {
		capByID[capability.CapabilityID] = capability
		capByScope[capability.Scope] = append(capByScope[capability.Scope], capability)
	}
	var out []EffectiveGrantDTO
	add := func(principalKind, principalID, capabilityID, scope, source, sourceID string) {
		out = append(out, EffectiveGrantDTO{
			PrincipalKind: principalKind,
			PrincipalID:   principalID,
			CapabilityID:  capabilityID,
			Scope:         scope,
			Source:        source,
			SourceID:      sourceID,
		})
	}
	for _, grant := range policy.Grants {
		kind := normalizeKind(grant.SubjectKind)
		id := strings.TrimSpace(grant.SubjectID)
		if kind == "role" {
			for _, assignment := range policy.RoleAssignments {
				if strings.TrimSpace(assignment.RoleID) != id {
					continue
				}
				emitGrant(add, capByID, capByScope, normalizeKind(assignment.PrincipalKind), strings.TrimSpace(assignment.PrincipalID), grant, "role", id)
			}
			continue
		}
		emitGrant(add, capByID, capByScope, kind, id, grant, "direct", "")
	}
	return out
}

func emitGrant(add func(string, string, string, string, string, string), capByID map[string]CapabilityDTO, capByScope map[string][]CapabilityDTO, principalKind, principalID string, grant PermissionGrantDTO, source, sourceID string) {
	if grant.CapabilityID != "" {
		capability := capByID[grant.CapabilityID]
		add(principalKind, principalID, grant.CapabilityID, firstNonEmpty(grant.Scope, capability.Scope), source, sourceID)
		return
	}
	for _, capability := range capByScope[grant.Scope] {
		add(principalKind, principalID, capability.CapabilityID, capability.Scope, source, sourceID)
	}
	if len(capByScope[grant.Scope]) == 0 {
		add(principalKind, principalID, "", grant.Scope, source, sourceID)
	}
}

func grantsAllow(grants []PermissionGrantDTO, subjectKind, subjectID, capabilityID, scope, _ string) bool {
	for _, grant := range grants {
		if normalizeKind(grant.SubjectKind) != subjectKind || strings.TrimSpace(grant.SubjectID) != subjectID {
			continue
		}
		if capabilityID != "" && strings.TrimSpace(grant.CapabilityID) == capabilityID {
			return true
		}
		if scope != "" && strings.TrimSpace(grant.Scope) == scope {
			return true
		}
	}
	return false
}

func normalizeKind(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "user", "human":
		return "user"
	case "agent":
		return "agent"
	case "role":
		return "role"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

type MemoryStore struct {
	mu     sync.RWMutex
	policy PolicyDTO
}

func NewMemoryStore(policy PolicyDTO) *MemoryStore {
	normalized, _ := NormalizePolicy(policy)
	return &MemoryStore{policy: normalized}
}

func (s *MemoryStore) LoadPolicy(ctx context.Context) (PolicyDTO, error) {
	if err := ctx.Err(); err != nil {
		return PolicyDTO{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return clonePolicy(s.policy), nil
}

func (s *MemoryStore) SavePolicy(ctx context.Context, policy PolicyDTO) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	normalized, err := NormalizePolicy(policy)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.policy = normalized
	return nil
}

type PlatformStore struct {
	router *db.Router
}

func NewPlatformStore(router *db.Router) *PlatformStore {
	return &PlatformStore{router: router}
}

func (s *PlatformStore) LoadPolicy(ctx context.Context) (PolicyDTO, error) {
	if s == nil || s.router == nil {
		return PolicyDTO{}, ErrServiceDependency
	}
	record, ok, err := s.router.GetRecord(ctx, PolicyRecordTable, "default")
	if err != nil {
		return PolicyDTO{}, err
	}
	if !ok {
		return PolicyDTO{}, nil
	}
	payload, _ := record["payload"].(PolicyDTO)
	if payload.Roles != nil || payload.Grants != nil || payload.RoleAssignments != nil {
		return clonePolicy(payload), nil
	}
	if raw, ok := record["payload"].(map[string]any); ok {
		return policyFromMap(raw), nil
	}
	return PolicyDTO{}, nil
}

func (s *PlatformStore) SavePolicy(ctx context.Context, policy PolicyDTO) error {
	if s == nil || s.router == nil {
		return ErrServiceDependency
	}
	normalized, err := NormalizePolicy(policy)
	if err != nil {
		return err
	}
	return s.router.PutRecord(ctx, PolicyRecordTable, "default", db.Record{"payload": normalized})
}

func clonePolicy(policy PolicyDTO) PolicyDTO {
	return PolicyDTO{
		Roles:           append([]RoleDTO(nil), policy.Roles...),
		RoleAssignments: append([]RoleAssignmentDTO(nil), policy.RoleAssignments...),
		Grants:          append([]PermissionGrantDTO(nil), policy.Grants...),
	}
}

func policyFromMap(raw map[string]any) PolicyDTO {
	var policy PolicyDTO
	convert := func(value any, out any) {
		bytes, _ := json.Marshal(value)
		_ = json.Unmarshal(bytes, out)
	}
	convert(raw["roles"], &policy.Roles)
	convert(raw["role_assignments"], &policy.RoleAssignments)
	convert(raw["grants"], &policy.Grants)
	normalized, _ := NormalizePolicy(policy)
	return normalized
}
