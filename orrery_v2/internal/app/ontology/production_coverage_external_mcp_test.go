//go:build external_mcp_acceptance

package ontology

import (
	"testing"
	"time"
)

// Run explicitly when accepting the complete external business integrations:
// go test -tags external_mcp_acceptance ./internal/app/ontology -run '^TestExternalMCPProductionCoverageGate$' -v -count=1
func TestExternalMCPProductionCoverageGate(t *testing.T) {
	asOf := time.Now().UTC()
	manifest := DefaultProductionCoverageManifest()
	t.Log(BuildProductionCoverageAuditReport(manifest, asOf).LogLine())
	report := ValidateProductionCoverageGateWithPublishers(defaultMappingRules(), manifest, KnownProductionOntologyPublishers(), asOf)
	if !report.OK() {
		t.Fatalf("external MCP integration coverage is incomplete:\n%s", report.Error())
	}
}
