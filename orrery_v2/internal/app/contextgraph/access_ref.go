package contextgraph

import (
	"errors"
	"strings"
)

var ErrAccessRefConflict = errors.New("context graph access reference conflict")

const (
	MaxAccessRefKindBytes     = 64
	MaxAccessRefIDBytes       = 512
	MaxAccessRefTenantIDBytes = 256
)

type AccessState string

const (
	AccessStateResolved   AccessState = "resolved"
	AccessStateUnresolved AccessState = "unresolved"
)

type AccessRef struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
}

func NormalizeAccessRef(ref AccessRef) AccessRef {
	ref.Kind = strings.ToLower(strings.TrimSpace(ref.Kind))
	ref.ID = strings.TrimSpace(ref.ID)
	ref.TenantID = strings.TrimSpace(ref.TenantID)
	return ref
}

func (ref AccessRef) Resolved() bool {
	ref = NormalizeAccessRef(ref)
	return ref.Kind != "" && len(ref.Kind) <= MaxAccessRefKindBytes &&
		ref.ID != "" && len(ref.ID) <= MaxAccessRefIDBytes &&
		ref.TenantID != "" && len(ref.TenantID) <= MaxAccessRefTenantIDBytes
}

func accessStateFor(ref AccessRef) AccessState {
	if ref.Resolved() {
		return AccessStateResolved
	}
	return AccessStateUnresolved
}

func sameAccessRef(left AccessRef, right AccessRef) bool {
	return NormalizeAccessRef(left) == NormalizeAccessRef(right)
}

func normalizedAccessRefOrEmpty(ref AccessRef) AccessRef {
	ref = NormalizeAccessRef(ref)
	if !ref.Resolved() {
		return AccessRef{}
	}
	return ref
}
