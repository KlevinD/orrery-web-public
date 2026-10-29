package ontology

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestProductionCoverageGateWebAPIReportsExternalMCPSeparately(t *testing.T) {
	asOf := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	manifest := DefaultProductionCoverageManifest()
	full := BuildProductionCoverageAuditReport(manifest, asOf)
	scoped := BuildWebAPIProductionCoverageAuditReport(manifest, asOf)
	if full.TotalRecords != 142 || len(full.ExpiredExemptions) != 19 {
		t.Fatalf("full audit must retain external integration gaps: %+v", full)
	}
	if scoped.TotalRecords != 110 || len(scoped.ExpiredExemptions)+len(scoped.ActiveExemptions)+len(scoped.UndatedExemptions) != 0 {
		t.Fatalf("Web/API audit must select only the approved release scope: %+v", scoped)
	}
	if scoped.RequiredModuleCount != 10 {
		t.Fatalf("Web/API required modules = %d, want 10", scoped.RequiredModuleCount)
	}
	for _, record := range scoped.Records {
		if strings.HasPrefix(record.EventType, "people.") {
			t.Fatalf("the independent People system must not be a Web/API release requirement: %+v", record)
		}
	}
	gate := ValidateWebAPIProductionCoverageGate(defaultMappingRules(), manifest, KnownProductionOntologyPublishers(), asOf)
	if !gate.OK() {
		t.Fatalf("external MCP readiness must not block Web/API: %s", gate.Error())
	}
	strict := ValidateProductionCoverageGateWithPublishers(defaultMappingRules(), manifest, KnownProductionOntologyPublishers(), asOf)
	if strict.OK() || len(strict.Issues) != 19 {
		t.Fatalf("strict external acceptance must retain all original failures: %+v", strict)
	}
	external := externalMCPProductionCoverageEvents()
	for _, issue := range strict.Issues {
		if issue.Code != "expired_exemption" || !external[issue.EventType] {
			t.Fatalf("unexpected strict coverage issue: %+v", issue)
		}
	}
}

func TestProductionCoverageGateWebAPIRetainsCurrentCorePublishers(t *testing.T) {
	for _, eventType := range []string{
		"resource.tool.registered", "resource.skill.registered", "resource.mcp_server.registered",
		"knowledge.base.created", "memory.profile.created", "todo.created",
	} {
		t.Run(eventType, func(t *testing.T) {
			manifest := DefaultProductionCoverageManifest()
			record := findProductionCoverageRecord(t, manifest.Records, eventType)
			record.ProductionPublishers = nil
			manifest.Records = replaceProductionCoverageRecord(t, manifest.Records, record)
			report := ValidateWebAPIProductionCoverageGate(defaultMappingRules(), manifest, KnownProductionOntologyPublishers(), time.Now().UTC())
			for _, issue := range report.Issues {
				if issue.EventType == eventType && issue.Code == "missing_production_publisher" {
					return
				}
			}
			t.Fatalf("current core publisher must remain required in Web/API: %+v", report)
		})
	}
}

func TestProductionCoverageGateWebAPIRejectsMissingCoreRecord(t *testing.T) {
	manifest := DefaultProductionCoverageManifest()
	manifest.Records = removeProductionCoverageRecord(manifest.Records, "mission.created")
	report := ValidateWebAPIProductionCoverageGate(defaultMappingRules(), manifest, KnownProductionOntologyPublishers(), time.Now().UTC())
	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/mission.created missing 0618 coverage record")
}

func TestProductionCoverageGateWebAPIRejectsMissingPublisherAndTestEvidence(t *testing.T) {
	for _, removePublisher := range []bool{true, false} {
		manifest := DefaultProductionCoverageManifest()
		record := findProductionCoverageRecord(t, manifest.Records, "mission.created")
		if removePublisher {
			record.ProductionPublishers = nil
		} else {
			record.TestEvidence = nil
		}
		manifest.Records = replaceProductionCoverageRecord(t, manifest.Records, record)
		report := ValidateWebAPIProductionCoverageGate(defaultMappingRules(), manifest, KnownProductionOntologyPublishers(), time.Now().UTC())
		message := "missing production test evidence"
		if removePublisher {
			message = "missing production publisher"
		}
		assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/mission.created "+message)
	}
}

func TestProductionCoverageGateWebAPIRejectsExpiredCoreExemption(t *testing.T) {
	asOf := time.Now().UTC()
	manifest := DefaultProductionCoverageManifest()
	record := findProductionCoverageRecord(t, manifest.Records, "mission.created")
	record.Status = ProductionCoverageSeededOnly
	record.ProductionPublishers = nil
	record.TestEvidence = nil
	record.ContextGraphAssertions = nil
	record.Exemption = &ProductionCoverageExemption{Owner: "orrery", Reason: "core is incomplete", ExpiresAt: asOf.Add(-time.Nanosecond)}
	manifest.Records = replaceProductionCoverageRecord(t, manifest.Records, record)
	report := ValidateWebAPIProductionCoverageGate(defaultMappingRules(), manifest, nil, asOf)
	for _, issue := range report.Issues {
		if issue.EventType == "mission.created" && issue.Code == "expired_exemption" {
			return
		}
	}
	t.Fatalf("scope selection must not bypass core exemption expiry: %+v", report)
}

func TestProductionCoverageGateWebAPIDoesNotExcludeUnknownEvents(t *testing.T) {
	rules := append(defaultMappingRules(), MappingRule{ID: "resource.tool.unreviewed", EventType: "resource.tool.unreviewed", Enabled: true})
	report := ValidateWebAPIProductionCoverageGate(rules, DefaultProductionCoverageManifest(), KnownProductionOntologyPublishers(), time.Now().UTC())
	assertProductionCoverageIssue(t, report, "context graph production gate: resource.tool.unreviewed missing coverage record")
	publishers := append(KnownProductionOntologyPublishers(), ProductionEventPublisherEvidence{EventType: "people.unknown", Path: "cmd/server/production_api.go"})
	report = ValidateWebAPIProductionCoverageGate(defaultMappingRules(), DefaultProductionCoverageManifest(), publishers, time.Now().UTC())
	assertProductionCoverageIssue(t, report, "context graph production gate: people.unknown production publisher missing coverage record")
}

func TestProductionCoverageGateWebAPIDoesNotMutateInputsOrDeadlines(t *testing.T) {
	rules := defaultMappingRules()
	manifest := DefaultProductionCoverageManifest()
	publishers := KnownProductionOntologyPublishers()
	before, err := json.Marshal([]any{rules, manifest, publishers})
	if err != nil {
		t.Fatal(err)
	}
	ValidateWebAPIProductionCoverageGate(rules, manifest, publishers, time.Now().UTC())
	BuildWebAPIProductionCoverageAuditReport(manifest, time.Now().UTC())
	after, err := json.Marshal([]any{rules, manifest, publishers})
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("Web/API scope must preserve full integration evidence, statuses and original deadlines")
	}
}

func TestProductionCoverageAuditReportWebAPIReleaseScope(t *testing.T) {
	asOf := time.Now().UTC()
	full := BuildProductionCoverageAuditReport(DefaultProductionCoverageManifest(), asOf)
	scoped := BuildWebAPIProductionCoverageAuditReport(DefaultProductionCoverageManifest(), asOf)
	encoded, err := json.Marshal(struct {
		Scope                 string
		WebAPI                ProductionCoverageAuditReport
		ExternalMCPEventTypes map[string]bool
		FullIntegration       ProductionCoverageAuditReport
	}{"web-api", scoped, externalMCPProductionCoverageEvents(), full})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("context graph release scope audit JSON: %s", encoded)
}
