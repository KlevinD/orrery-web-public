package ontologyscope

import (
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/app/identity"
)

type Kind string
type BindingState string

const (
	KindOrganization Kind = "organization"
	KindProject      Kind = "project"
	KindMission      Kind = "mission"
	KindResource     Kind = "resource"
	KindSystem       Kind = "system"
	KindCustom       Kind = "custom"
)

const (
	BindingStateActive     BindingState = "active"
	BindingStateDisabled   BindingState = "disabled"
	BindingStateUnresolved BindingState = "unresolved"
)

type ScopeBinding struct {
	TenantID              string
	Kind                  Kind
	SourceObject          identity.ObjectRef
	AuthorizationTenantID string
	ReadAction            string
	ManageAction          string
	LabelRef              identity.ObjectRef
	BindingVersion        int64
	State                 BindingState
	CreatedAt             time.Time
	UpdatedAt             time.Time
	// PersistedBindingVersion records the row that contributed to this resolved
	// binding. It is internal authorization metadata and is never client input.
	PersistedBindingVersion int64
	// PersistenceRequired is true when the resolved binding cannot be reproduced
	// from a deterministic source without its persisted row.
	PersistenceRequired bool
}

type BindingQuery struct {
	Kind          Kind
	State         BindingState
	AfterTenantID string
	Limit         int
}

func NormalizeBinding(binding ScopeBinding) ScopeBinding {
	binding.TenantID = strings.TrimSpace(binding.TenantID)
	binding.Kind = Kind(strings.ToLower(strings.TrimSpace(string(binding.Kind))))
	binding.SourceObject.Type = strings.ToLower(strings.TrimSpace(binding.SourceObject.Type))
	binding.SourceObject.ID = strings.TrimSpace(binding.SourceObject.ID)
	binding.SourceObject.TenantID = ""
	binding.AuthorizationTenantID = strings.TrimSpace(binding.AuthorizationTenantID)
	binding.ReadAction = strings.ToLower(strings.TrimSpace(binding.ReadAction))
	binding.ManageAction = strings.ToLower(strings.TrimSpace(binding.ManageAction))
	binding.LabelRef.Type = strings.ToLower(strings.TrimSpace(binding.LabelRef.Type))
	binding.LabelRef.ID = strings.TrimSpace(binding.LabelRef.ID)
	binding.LabelRef.TenantID = strings.TrimSpace(binding.LabelRef.TenantID)
	binding.State = BindingState(strings.ToLower(strings.TrimSpace(string(binding.State))))
	if binding.State == "" {
		binding.State = BindingStateActive
	}
	if !binding.CreatedAt.IsZero() {
		binding.CreatedAt = binding.CreatedAt.UTC()
	}
	if !binding.UpdatedAt.IsZero() {
		binding.UpdatedAt = binding.UpdatedAt.UTC()
	}
	return binding
}

func ValidateBinding(binding ScopeBinding) error {
	binding = NormalizeBinding(binding)
	if binding.TenantID == "" || len(binding.TenantID) > 256 {
		return fmt.Errorf("%w: canonical tenant_id is required and must not exceed 256 bytes", ErrInvalidBinding)
	}
	if !validKind(binding.Kind) {
		return fmt.Errorf("%w: unsupported scope kind %q", ErrInvalidBinding, binding.Kind)
	}
	if binding.SourceObject.Type == "" || binding.SourceObject.ID == "" {
		return fmt.Errorf("%w: source object type and id are required", ErrInvalidBinding)
	}
	if binding.AuthorizationTenantID == "" {
		return fmt.Errorf("%w: authorization tenant_id is required", ErrInvalidBinding)
	}
	if binding.ReadAction == "" || binding.ManageAction == "" {
		return fmt.Errorf("%w: read and manage actions are required", ErrInvalidBinding)
	}
	if err := validateKnownBindingMapping(binding); err != nil {
		return err
	}
	if (binding.LabelRef.Type == "") != (binding.LabelRef.ID == "") {
		return fmt.Errorf("%w: label ref type and id must be provided together", ErrInvalidBinding)
	}
	if !validState(binding.State) {
		return fmt.Errorf("%w: unsupported binding state %q", ErrInvalidBinding, binding.State)
	}
	return nil
}

func validateKnownBindingMapping(binding ScopeBinding) error {
	type expectedMapping struct {
		objectType   string
		readAction   string
		manageAction string
	}
	expected := map[Kind]expectedMapping{
		KindOrganization: {objectType: "organization", readAction: "organization.read", manageAction: "organization.manage"},
		KindProject:      {objectType: "project", readAction: "project.view", manageAction: "project.manage"},
		KindMission:      {objectType: "mission", readAction: "mission.view", manageAction: "mission.manage"},
		KindResource:     {objectType: "resource", readAction: "resource.view", manageAction: "resource.manage"},
		KindSystem:       {objectType: "platform", readAction: "operations.read", manageAction: "operations.manage"},
	}
	mapping, known := expected[binding.Kind]
	if !known {
		return nil
	}
	if binding.SourceObject.Type != mapping.objectType || binding.ReadAction != mapping.readAction || binding.ManageAction != mapping.manageAction {
		return fmt.Errorf("%w: %s binding must target %s with %s/%s", ErrInvalidBinding, binding.Kind, mapping.objectType, mapping.readAction, mapping.manageAction)
	}
	if binding.Kind == KindSystem && (binding.TenantID != "system" || binding.SourceObject.ID != "global" || binding.AuthorizationTenantID != "global") {
		return fmt.Errorf("%w: system binding is reserved", ErrInvalidBinding)
	}
	return nil
}

func SystemBinding() ScopeBinding {
	return ScopeBinding{
		TenantID:              "system",
		Kind:                  KindSystem,
		SourceObject:          identity.ObjectRef{Type: "platform", ID: "global", TenantID: "global"},
		AuthorizationTenantID: "global",
		ReadAction:            "operations.read",
		ManageAction:          "operations.manage",
		LabelRef:              identity.ObjectRef{Type: "platform", ID: "global", TenantID: "global"},
		BindingVersion:        1,
		State:                 BindingStateActive,
	}
}

func validKind(kind Kind) bool {
	switch kind {
	case KindOrganization, KindProject, KindMission, KindResource, KindSystem, KindCustom:
		return true
	default:
		return false
	}
}

func validState(state BindingState) bool {
	switch state {
	case BindingStateActive, BindingStateDisabled, BindingStateUnresolved:
		return true
	default:
		return false
	}
}

func bindingsEquivalent(left ScopeBinding, right ScopeBinding) bool {
	left = NormalizeBinding(left)
	right = NormalizeBinding(right)
	return left.TenantID == right.TenantID &&
		left.Kind == right.Kind &&
		left.SourceObject == right.SourceObject &&
		left.AuthorizationTenantID == right.AuthorizationTenantID &&
		left.ReadAction == right.ReadAction &&
		left.ManageAction == right.ManageAction &&
		left.LabelRef == right.LabelRef &&
		left.State == right.State
}
