package permissionscope

import "strings"

const (
	EnterpriseGraphRead = "enterprise_graph.read"
	OntologyRead        = "ontology.read"
	OntologyManage      = "ontology.manage"
	OperationsRead      = "operations.read"
	OperationsManage    = "operations.manage"
)

// Normalize keeps permission comparisons consistent across HTTP, runtime, and
// MCP boundaries while retaining compatibility with legacy scope spellings.
func Normalize(scope string) string {
	scope = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(scope)), "orrery:")
	switch scope {
	case "resources.read":
		return "resource.read"
	case "resources.manage":
		return "resource.manage"
	default:
		return scope
	}
}

// Allows applies the common permission hierarchy. Ontology and enterprise
// graph read scopes describe the same read plane, while operations scopes are
// retained as a backwards-compatible grant for the legacy ontology API.
func Allows(granted, required string) bool {
	granted = Normalize(granted)
	required = Normalize(required)
	if granted == "" || required == "" {
		return false
	}
	if granted == required {
		return true
	}
	if strings.HasSuffix(required, ".read") && granted == strings.TrimSuffix(required, ".read")+".manage" {
		return true
	}
	switch required {
	case OntologyRead:
		return granted == EnterpriseGraphRead || granted == OperationsRead || granted == OperationsManage
	case EnterpriseGraphRead:
		return granted == OntologyRead || granted == OntologyManage
	case OntologyManage:
		return granted == OperationsManage
	default:
		return false
	}
}

func AnyAllows(granted []string, required string) bool {
	for _, scope := range granted {
		if Allows(scope, required) {
			return true
		}
	}
	return false
}
