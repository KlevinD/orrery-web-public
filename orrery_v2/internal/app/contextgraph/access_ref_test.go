package contextgraph

import (
	"strings"
	"testing"
)

func TestAccessRefResolvedHonorsVisibilityPolicyBounds(t *testing.T) {
	valid := AccessRef{
		Kind:     strings.Repeat("k", MaxAccessRefKindBytes),
		ID:       strings.Repeat("i", MaxAccessRefIDBytes),
		TenantID: strings.Repeat("t", MaxAccessRefTenantIDBytes),
	}
	if !valid.Resolved() || accessStateFor(valid) != AccessStateResolved {
		t.Fatal("access ref at the visibility policy bounds must remain resolved")
	}

	for name, ref := range map[string]AccessRef{
		"kind":      {Kind: strings.Repeat("k", MaxAccessRefKindBytes+1), ID: "id", TenantID: "tenant"},
		"id":        {Kind: "kind", ID: strings.Repeat("i", MaxAccessRefIDBytes+1), TenantID: "tenant"},
		"tenant_id": {Kind: "kind", ID: "id", TenantID: strings.Repeat("t", MaxAccessRefTenantIDBytes+1)},
		"utf8_kind": {Kind: strings.Repeat("界", MaxAccessRefKindBytes/3+1), ID: "id", TenantID: "tenant"},
	} {
		t.Run(name, func(t *testing.T) {
			if ref.Resolved() || accessStateFor(ref) != AccessStateUnresolved {
				t.Fatal("oversized access ref must be unresolved")
			}
			if normalizedAccessRefOrEmpty(ref) != (AccessRef{}) {
				t.Fatal("oversized access ref must not be persisted as an authorization input")
			}
		})
	}
}
