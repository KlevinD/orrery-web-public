package permissionscope

import "testing"

func TestAllowsNormalizesAndAppliesReadHierarchy(t *testing.T) {
	tests := []struct {
		name     string
		granted  string
		required string
		allowed  bool
	}{
		{name: "legacy prefix and plural resource", granted: " ORRERY:RESOURCES.MANAGE ", required: "resource.read", allowed: true},
		{name: "manage includes read", granted: "knowledge.manage", required: "knowledge.read", allowed: true},
		{name: "ontology reads enterprise graph", granted: OntologyRead, required: EnterpriseGraphRead, allowed: true},
		{name: "enterprise graph reads ontology", granted: EnterpriseGraphRead, required: OntologyRead, allowed: true},
		{name: "legacy operations reads ontology", granted: OperationsRead, required: OntologyRead, allowed: true},
		{name: "operations does not call graph tool", granted: OperationsRead, required: EnterpriseGraphRead, allowed: false},
		{name: "read does not include manage", granted: OntologyRead, required: OntologyManage, allowed: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Allows(test.granted, test.required); got != test.allowed {
				t.Fatalf("Allows(%q, %q) = %v, want %v", test.granted, test.required, got, test.allowed)
			}
		})
	}
}
