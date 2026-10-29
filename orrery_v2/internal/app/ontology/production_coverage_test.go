package ontology

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProductionCoverageGateRequiresEveryWebAPIMappingRuleToBeClassified(t *testing.T) {
	report := ValidateWebAPIProductionCoverageGate(defaultMappingRules(), DefaultProductionCoverageManifest(), KnownProductionOntologyPublishers(), time.Now().UTC())
	if !report.OK() {
		t.Fatalf("Web/API production coverage manifest should pass the hard gate, got:\n%s", report.Error())
	}
}

func TestProductionCoverageReleaseGateUsesCurrentTimeForExemptions(t *testing.T) {
	source := parseProductionCoverageTestSource(t)
	target := findTestFunction(t, source, "TestProductionCoverageGateRequiresEveryWebAPIMappingRuleToBeClassified")

	usesCurrentTime := false
	usesFrozenTime := false
	ast.Inspect(target.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if isSelectorCall(call, "time", "Date") || isIdentCall(call, "gateTestTime") {
			usesFrozenTime = true
		}
		if isTimeNowUTCCall(call) {
			usesCurrentTime = true
		}
		return true
	})

	if usesFrozenTime {
		t.Fatal("context graph production release gate must not validate exemptions against a frozen test date")
	}
	if !usesCurrentTime {
		t.Fatal("context graph production release gate must validate exemptions against time.Now().UTC()")
	}
}

func TestProductionCoverageReleaseExemptionReportUsesCurrentTime(t *testing.T) {
	source := parseProductionCoverageTestSource(t)
	target := findTestFunction(t, source, "TestProductionCoverageExemptionReport")

	usesCurrentTime := false
	usesFrozenTime := false
	ast.Inspect(target.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if isSelectorCall(call, "time", "Date") || isIdentCall(call, "gateTestTime") {
			usesFrozenTime = true
		}
		if isTimeNowUTCCall(call) {
			usesCurrentTime = true
		}
		return true
	})

	if usesFrozenTime {
		t.Fatal("context graph production exemption report must not use a frozen test date")
	}
	if !usesCurrentTime {
		t.Fatal("context graph production exemption report must use time.Now().UTC()")
	}
}

func TestProductionCoverageGateActiveExemptionsAreReportable(t *testing.T) {
	exemptions := ActiveProductionCoverageExemptions(DefaultProductionCoverageManifest(), gateTestTime())
	if len(exemptions) == 0 {
		t.Fatal("context graph production gate exemptions must be reportable, got none")
	}

	assertProductionCoverageExemption(t, exemptions, "discussion", "discussion.message.created", ProductionCoverageNotConnected, "platform-ontology", "0618 discussion scope", "2026-09-15")
	assertProductionCoverageExemption(t, exemptions, "legal", "legal.entity.created", ProductionCoverageSeededOnly, "platform-ontology", "legal entity production module", "2026-09-30")
	for _, exemption := range exemptions {
		if strings.TrimSpace(exemption.Owner) == "" || strings.TrimSpace(exemption.Reason) == "" || exemption.ExpiresAt.IsZero() {
			t.Fatalf("context graph production gate exemption must include owner, reason, and expiry: %+v", exemption)
		}
	}
}

func TestProductionCoverageExemptionReport(t *testing.T) {
	asOf := time.Now().UTC()
	manifest := DefaultProductionCoverageManifest()
	report := BuildProductionCoverageAuditReport(manifest, asOf)
	t.Log(report.LogLine())
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal production coverage audit report: %v", err)
	}
	t.Logf("context graph production gate audit JSON: %s", encoded)
	want := 0
	for _, record := range manifest.Records {
		if record.Exemption != nil {
			want++
		}
	}
	if got := len(report.ActiveExemptions) + len(report.ExpiredExemptions) + len(report.UndatedExemptions); got != want {
		t.Fatalf("audit report exemptions = %d, want %d", got, want)
	}
}

func TestProductionCoverageAuditReportSummarizesManifestAndExemptions(t *testing.T) {
	report := BuildProductionCoverageAuditReport(DefaultProductionCoverageManifest(), gateTestTime())
	if report.GeneratedAt.IsZero() {
		t.Fatal("context graph production gate audit report must include generated_at")
	}
	if report.TotalRecords != len(DefaultProductionCoverageManifest().Records) {
		t.Fatalf("audit report total records = %d, want %d", report.TotalRecords, len(DefaultProductionCoverageManifest().Records))
	}
	if report.RequiredModuleCount != len(required0618ProductionModules()) {
		t.Fatalf("audit report required module count = %d, want %d", report.RequiredModuleCount, len(required0618ProductionModules()))
	}
	if report.StatusCounts[ProductionCoveragePartialConnected] == 0 {
		t.Fatal("audit report must count partial_connected records")
	}
	if report.StatusCounts[ProductionCoverageSeededOnly] == 0 {
		t.Fatal("audit report must count seeded_only records")
	}
	if report.StatusCounts[ProductionCoverageNotConnected] == 0 {
		t.Fatal("audit report must count not_connected records")
	}
	if len(report.ActiveExemptions) == 0 {
		t.Fatal("audit report must include active exemptions")
	}
	if !auditReportContainsRecord(report, "legal", "legal.entity.created", ProductionCoverageSeededOnly) {
		t.Fatal("audit report must include legal/legal.entity.created seeded_only record")
	}
	if !auditReportContainsRecord(report, "todo", "todo.created", ProductionCoveragePartialConnected) {
		t.Fatal("audit report must include todo/todo.created partial_connected record")
	}
}

func TestProductionCoverageAuditReportLogsMachineReadableSummary(t *testing.T) {
	report := BuildProductionCoverageAuditReport(DefaultProductionCoverageManifest(), gateTestTime())
	line := report.LogLine()
	for _, want := range []string{
		"context graph production gate audit:",
		"total_records=",
		"partial_connected=",
		"seeded_only=",
		"not_connected=",
		"active_exemptions=",
		"expired_exemptions=",
		"undated_exemptions=",
		"required_modules=",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("audit report log line missing %q: %s", want, line)
		}
	}
	t.Log(line)
}

func TestProductionCoverageAuditReportRetainsExpiredAndUndatedExemptions(t *testing.T) {
	asOf := gateTestTime()
	manifest := ProductionCoverageManifest{}
	for _, input := range []struct {
		eventType string
		expiry    time.Time
	}{
		{"z.expired", asOf.Add(-time.Nanosecond)},
		{"a.expires_now", asOf.In(time.FixedZone("UTC+8", 8*60*60))},
		{"active", asOf.Add(time.Nanosecond)},
		{"undated", time.Time{}},
	} {
		manifest.Records = append(manifest.Records, ProductionCoverageRecord{
			Domain: " example ", Feature: " feature ", EventType: input.eventType,
			Status: ProductionCoverageNotConnected,
			Exemption: &ProductionCoverageExemption{
				Owner: " owner ", Reason: " missing publisher ", ExpiresAt: input.expiry,
			},
		})
	}
	manifest.Records = append(manifest.Records, ProductionCoverageRecord{EventType: "no.exemption"})
	report := BuildProductionCoverageAuditReport(manifest, asOf)
	if len(report.ActiveExemptions) != 1 || len(report.ExpiredExemptions) != 2 || len(report.UndatedExemptions) != 1 {
		t.Fatalf("exemptions must partition into active, expired and undated: %+v", report)
	}
	if report.ActiveExemptions[0].EventType != "active" || report.UndatedExemptions[0].EventType != "undated" {
		t.Fatalf("incorrect active or undated exemptions: %+v", report)
	}
	if report.ExpiredExemptions[0].EventType != "a.expires_now" || report.ExpiredExemptions[1].EventType != "z.expired" {
		t.Fatalf("expiry boundary must be expired and output must be sorted: %+v", report.ExpiredExemptions)
	}
	for _, exemption := range append(append(report.ActiveExemptions, report.ExpiredExemptions...), report.UndatedExemptions...) {
		if exemption.Domain != "example" || exemption.Feature != "feature" || exemption.Owner != "owner" || exemption.Reason != "missing publisher" {
			t.Fatalf("audit must preserve normalized exemption details: %+v", exemption)
		}
	}
	if !manifest.Records[1].Exemption.ExpiresAt.Equal(asOf) || manifest.Records[1].Exemption.Owner != " owner " {
		t.Fatal("audit must not mutate manifest or extend expiry")
	}
}

func TestProductionCoverageAuditReportHandlesNoExemptions(t *testing.T) {
	for _, manifest := range []ProductionCoverageManifest{
		{},
		{Records: []ProductionCoverageRecord{{Domain: "example", EventType: "connected", Status: ProductionCoverageProductionConnected}}},
	} {
		report := BuildProductionCoverageAuditReport(manifest, gateTestTime())
		if len(report.ActiveExemptions)+len(report.ExpiredExemptions)+len(report.UndatedExemptions) != 0 {
			t.Fatalf("audit must not invent exemptions: %+v", report)
		}
		if !strings.Contains(report.LogLine(), "active_exemptions=0 expired_exemptions=0 undated_exemptions=0") {
			t.Fatalf("audit must explicitly report zero counts: %s", report.LogLine())
		}
	}
}

func TestProductionCoverageAuditReportDoesNotAuthorizeExpiredOrUndatedExemptions(t *testing.T) {
	asOf := gateTestTime()
	for _, expiry := range []time.Time{asOf, {}} {
		record := notConnectedCoverage("todo", "todo missing", "todo.created", businessSources("internal/app/todo/service.go"), ProductionCoverageExemption{
			Owner: "platform-ontology", Reason: "no publisher", ExpiresAt: expiry,
		})
		manifest := ProductionCoverageManifest{Records: []ProductionCoverageRecord{record}}
		audit := BuildProductionCoverageAuditReport(manifest, asOf)
		if len(audit.ActiveExemptions) != 0 || len(audit.ExpiredExemptions)+len(audit.UndatedExemptions) != 1 {
			t.Fatalf("audit must retain the invalid exemption without activating it: %+v", audit)
		}
		gate := ValidateProductionCoverageGate(nil, manifest, asOf)
		code := "expired_exemption"
		if expiry.IsZero() {
			code = "missing_exemption_expiry"
		}
		found := false
		for _, issue := range gate.Issues {
			if issue.EventType == record.EventType && issue.Code == code {
				found = true
			}
		}
		if !found || gate.OK() {
			t.Fatalf("audit must leave hard gate rejection intact: %+v", gate)
		}
	}
}

func TestProductionCoverageGateRequiresEveryKnownProductionPublisherToBeClassified(t *testing.T) {
	report := ValidateProductionCoverageGateWithPublishers(defaultMappingRules(), DefaultProductionCoverageManifest(), KnownProductionOntologyPublishers(), gateTestTime())
	if !report.OK() {
		t.Fatalf("known production ontology publishers should pass the hard gate, got:\n%s", report.Error())
	}
}

func TestProductionCoverageGateRequiresEvery0618CoverageRecord(t *testing.T) {
	manifest := DefaultProductionCoverageManifest()
	manifest.Records = removeProductionCoverageRecord(manifest.Records, "todo.created")

	report := ValidateProductionCoverageGate(defaultMappingRules(), manifest, gateTestTime())

	assertProductionCoverageIssue(t, report, "context graph production gate: todo/todo.created missing 0618 coverage record")
}

func TestProductionCoverageGateRequires0618CoverageRecordDomainToMatchCatalog(t *testing.T) {
	manifest := DefaultProductionCoverageManifest()
	for i := range manifest.Records {
		if manifest.Records[i].EventType == "todo.created" {
			manifest.Records[i].Domain = "misc"
			break
		}
	}

	report := ValidateProductionCoverageGate(defaultMappingRules(), manifest, gateTestTime())

	assertProductionCoverageIssue(t, report, "context graph production gate: misc/todo.created expected 0618 coverage domain todo")
}

func TestProductionCoverageRequired0618ModulesCoverEveryCatalogRecord(t *testing.T) {
	modules := required0618ProductionModulesByEventType()
	for _, required := range required0618CoverageRecords() {
		module, ok := modules[required.EventType]
		if !ok {
			t.Fatalf("context graph production gate: %s/%s missing required 0618 production module catalog entry", required.Domain, required.EventType)
		}
		if module.Domain != required.Domain {
			t.Fatalf("context graph production gate: %s/%s production module domain = %s, want %s", required.Domain, required.EventType, module.Domain, required.Domain)
		}
		if len(module.BusinessSourcePrefixes) == 0 {
			t.Fatalf("context graph production gate: %s/%s production module must declare business source prefixes", required.Domain, required.EventType)
		}
	}
}

func TestProductionCoverageGateKnownPublishersMatchSourceDomainEvents(t *testing.T) {
	sourceEvents := collectOntologyProductionEventTypesFromSource(t)
	known := make(map[string]bool)
	for _, publisher := range KnownProductionOntologyPublishers() {
		known[publisher.EventType] = true
	}

	for eventType := range sourceEvents {
		if !known[eventType] {
			t.Fatalf("context graph production gate: %s production publisher missing from KnownProductionOntologyPublishers", eventType)
		}
	}
}

func TestProductionCoverageGateKnownPublishersMatchSourceDomainEventPaths(t *testing.T) {
	sourcePublishers := collectOntologyProductionPublishersFromSource(t)
	known := make(map[string]bool)
	for _, publisher := range KnownProductionOntologyPublishers() {
		known[publisher.EventType+" "+evidenceFilePath(publisher.Path)] = true
	}

	for _, publisher := range sourcePublishers {
		key := publisher.EventType + " " + evidenceFilePath(publisher.Path)
		if !known[key] {
			t.Fatalf("context graph production gate: %s production publisher path missing from KnownProductionOntologyPublishers: %s", publisher.EventType, evidenceFilePath(publisher.Path))
		}
	}
}

func TestProductionCoverageGateReportsMissingCoverageRecord(t *testing.T) {
	report := ValidateProductionCoverageGate([]MappingRule{
		{
			ID:        "project.created",
			EventType: "project.created",
			Enabled:   true,
		},
	}, ProductionCoverageManifest{}, time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC))
	if report.OK() {
		t.Fatal("expected missing coverage record to fail")
	}
	if !strings.Contains(report.Error(), "context graph production gate: project.created missing coverage record") {
		t.Fatalf("missing coverage failure should name event and reason, got %q", report.Error())
	}
}

func TestProductionCoverageGateReportsMalformedCoverageRecord(t *testing.T) {
	record := minimalConnectedCoverageRecord()
	record.Domain = ""
	record.Feature = ""

	report := ValidateProductionCoverageGate([]MappingRule{minimalCoverageMappingRule()}, ProductionCoverageManifest{Records: []ProductionCoverageRecord{record}}, gateTestTime())

	assertProductionCoverageIssue(t, report, "context graph production gate: project.created missing domain")
	assertProductionCoverageIssue(t, report, "context graph production gate: project.created missing feature")
}

func TestProductionCoverageGateReportsDuplicateCoverageEvidence(t *testing.T) {
	record := minimalConnectedCoverageRecord()
	record.ProductionPublishers = append(record.ProductionPublishers, record.ProductionPublishers[0])
	record.TestEvidence = append(record.TestEvidence, record.TestEvidence[0])
	record.ContextGraphAssertions = append(record.ContextGraphAssertions, record.ContextGraphAssertions[0])

	report := ValidateProductionCoverageGate([]MappingRule{minimalCoverageMappingRule()}, ProductionCoverageManifest{Records: []ProductionCoverageRecord{record}}, gateTestTime())

	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/project.created duplicate production publisher evidence cmd/server/production_project_ontology_api.go:76")
	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/project.created duplicate production test evidence success cmd/server/production_project_ontology_test.go")
	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/project.created duplicate contextgraph assertion evidence object:project")
}

func TestProductionCoverageGateReportsPublisherWithoutCoverageRecord(t *testing.T) {
	report := ValidateProductionCoverageGateWithPublishers([]MappingRule{minimalCoverageMappingRule()}, ProductionCoverageManifest{}, []ProductionEventPublisherEvidence{
		{
			EventType: "project.created",
			Path:      "cmd/server/production_project_api.go:20",
		},
	}, gateTestTime())

	assertProductionCoverageIssue(t, report, "context graph production gate: project.created production publisher missing coverage record")
}

func TestProductionCoverageGateReportsKnownPublisherInventoryMissingFile(t *testing.T) {
	report := ValidateProductionCoverageGateWithPublishers([]MappingRule{minimalCoverageMappingRule()}, ProductionCoverageManifest{Records: []ProductionCoverageRecord{minimalConnectedCoverageRecord()}}, []ProductionEventPublisherEvidence{
		{
			EventType: "project.created",
			Path:      "cmd/server/missing_known_publisher.go:20",
		},
	}, gateTestTime())

	assertProductionCoverageIssue(t, report, "context graph production gate: project.created known production publisher file missing cmd/server/missing_known_publisher.go")
}

func TestProductionCoverageGateReportsKnownPublisherNotDeclaredByManifest(t *testing.T) {
	record := minimalConnectedCoverageRecord()
	record.ProductionPublishers = []string{"cmd/server/production_project_api.go:20"}

	report := ValidateProductionCoverageGateWithPublishers([]MappingRule{minimalCoverageMappingRule()}, ProductionCoverageManifest{Records: []ProductionCoverageRecord{record}}, []ProductionEventPublisherEvidence{
		{EventType: "project.created", Path: "cmd/server/production_project_api.go:20"},
		{EventType: "project.created", Path: "cmd/server/production_project_ontology_api.go:76"},
	}, gateTestTime())

	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/project.created known production publisher not declared cmd/server/production_project_ontology_api.go")
}

func TestProductionCoverageGateReportsPublisherWithoutMappingRule(t *testing.T) {
	report := ValidateProductionCoverageGateWithPublishers(nil, ProductionCoverageManifest{Records: []ProductionCoverageRecord{minimalConnectedCoverageRecord()}}, []ProductionEventPublisherEvidence{
		{
			EventType: "project.created",
			Path:      "cmd/server/production_project_api.go:20",
		},
	}, gateTestTime())

	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/project.created missing mapping rule")
}

func TestProductionCoverageGateReportsMissingProductionPublisher(t *testing.T) {
	record := minimalConnectedCoverageRecord()
	record.ProductionPublishers = nil

	report := ValidateProductionCoverageGate([]MappingRule{minimalCoverageMappingRule()}, ProductionCoverageManifest{Records: []ProductionCoverageRecord{record}}, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/project.created missing production publisher")
}

func TestProductionCoverageGateReportsProductionPublisherNotInKnownPublisherInventory(t *testing.T) {
	record := minimalConnectedCoverageRecord()
	record.ProductionPublishers = []string{"cmd/server/production_project_ontology_test.go"}

	report := ValidateProductionCoverageGateWithPublishers([]MappingRule{minimalCoverageMappingRule()}, ProductionCoverageManifest{Records: []ProductionCoverageRecord{record}}, []ProductionEventPublisherEvidence{
		{EventType: "project.created", Path: "cmd/server/production_project_api.go:20"},
	}, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/project.created production publisher evidence not registered cmd/server/production_project_ontology_test.go")
}

func TestProductionCoverageGateReportsMissingMappingRuleForProductionPublisher(t *testing.T) {
	record := minimalConnectedCoverageRecord()

	report := ValidateProductionCoverageGate(nil, ProductionCoverageManifest{Records: []ProductionCoverageRecord{record}}, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/project.created missing mapping rule")
}

func TestProductionCoverageGateReportsMissingMappingRuleID(t *testing.T) {
	record := minimalConnectedCoverageRecord()
	record.MappingRuleID = ""

	report := ValidateProductionCoverageGate([]MappingRule{minimalCoverageMappingRule()}, ProductionCoverageManifest{Records: []ProductionCoverageRecord{record}}, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/project.created missing mapping rule id")
}

func TestProductionCoverageGateReportsMappingRuleIDMismatch(t *testing.T) {
	record := minimalConnectedCoverageRecord()
	record.MappingRuleID = "project.renamed"

	report := ValidateProductionCoverageGate([]MappingRule{minimalCoverageMappingRule()}, ProductionCoverageManifest{Records: []ProductionCoverageRecord{record}}, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/project.created mapping rule id project.renamed does not match event type project.created")
}

func TestProductionCoverageGateReportsConnectedStatusWithExemption(t *testing.T) {
	record := minimalConnectedCoverageRecord()
	record.Exemption = &ProductionCoverageExemption{
		Owner:     "platform-ontology",
		Reason:    "connected path should not need exemption",
		ExpiresAt: time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
	}

	report := ValidateProductionCoverageGate([]MappingRule{minimalCoverageMappingRule()}, ProductionCoverageManifest{Records: []ProductionCoverageRecord{record}}, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/project.created status production_connected cannot declare exemption")
}

func TestProductionCoverageGateReportsProductionConnectedWithKnownGaps(t *testing.T) {
	record := minimalConnectedCoverageRecord()
	record.KnownGaps = []string{"task relationships are not connected"}

	report := ValidateProductionCoverageGate([]MappingRule{minimalCoverageMappingRule()}, ProductionCoverageManifest{Records: []ProductionCoverageRecord{record}}, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/project.created status production_connected cannot declare known gaps")
}

func TestProductionCoverageGateReportsMissingProductionTestEvidence(t *testing.T) {
	record := minimalConnectedCoverageRecord()
	record.TestEvidence = nil

	report := ValidateProductionCoverageGate([]MappingRule{minimalCoverageMappingRule()}, ProductionCoverageManifest{Records: []ProductionCoverageRecord{record}}, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/project.created missing production test evidence")
}

func TestProductionCoverageGateReportsMissingRequiredTestEvidenceKind(t *testing.T) {
	record := minimalConnectedCoverageRecord()
	record.TestEvidence = []ProductionTestEvidence{
		{Kind: ProductionTestEvidenceSuccess, Path: "cmd/server/production_project_ontology_test.go"},
		{Kind: ProductionTestEvidenceTenantIsolation, Path: "cmd/server/production_project_ontology_test.go"},
		{Kind: ProductionTestEvidenceReplay, Path: "cmd/server/production_ontology_projection_api_test.go"},
	}

	report := ValidateProductionCoverageGate([]MappingRule{minimalCoverageMappingRule()}, ProductionCoverageManifest{Records: []ProductionCoverageRecord{record}}, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/project.created missing production test evidence kind failure")
}

func TestProductionCoverageGateReportsMissingRequiredTestEvidenceReason(t *testing.T) {
	record := minimalConnectedCoverageRecord()
	record.TestEvidence = []ProductionTestEvidence{
		{Kind: ProductionTestEvidenceSuccess, Path: "cmd/server/production_project_ontology_test.go"},
		{Kind: ProductionTestEvidenceFailure, Path: "cmd/server/production_ontology_projection_api_test.go"},
		{Kind: ProductionTestEvidenceReplay, Path: "cmd/server/production_ontology_projection_api_test.go"},
		{Kind: ProductionTestEvidenceTenantIsolation, Path: "cmd/server/production_project_ontology_test.go"},
	}

	report := ValidateProductionCoverageGate([]MappingRule{minimalCoverageMappingRule()}, ProductionCoverageManifest{Records: []ProductionCoverageRecord{record}}, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/project.created production test evidence kind failure missing reason")
}

func TestProductionCoverageGateReportsMissingPublisherEvidenceFile(t *testing.T) {
	record := minimalConnectedCoverageRecord()
	record.ProductionPublishers = []string{"cmd/server/missing_ontology_publisher.go:20"}

	report := ValidateProductionCoverageGate([]MappingRule{minimalCoverageMappingRule()}, ProductionCoverageManifest{Records: []ProductionCoverageRecord{record}}, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/project.created production publisher evidence file missing cmd/server/missing_ontology_publisher.go")
}

func TestProductionCoverageGateReportsPublisherEvidenceFileWithoutEventType(t *testing.T) {
	record := minimalConnectedCoverageRecord()
	record.ProductionPublishers = []string{"cmd/server/production_resource_tool_ontology_test.go"}

	report := ValidateProductionCoverageGate([]MappingRule{minimalCoverageMappingRule()}, ProductionCoverageManifest{Records: []ProductionCoverageRecord{record}}, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/project.created production publisher evidence does not contain event type project.created")
}

func TestProductionCoverageGateReportsMissingTestEvidenceFile(t *testing.T) {
	record := minimalConnectedCoverageRecord()
	record.TestEvidence = []ProductionTestEvidence{
		{Kind: ProductionTestEvidenceSuccess, Path: "cmd/server/missing_ontology_test.go"},
		{Kind: ProductionTestEvidenceFailure, Path: "cmd/server/production_ontology_projection_api_test.go", Reason: "projection audit missing target paths are replayable and reported"},
		{Kind: ProductionTestEvidenceReplay, Path: "cmd/server/production_ontology_projection_api_test.go", Reason: "projection audit replay verifies skipped event recovery"},
		{Kind: ProductionTestEvidenceTenantIsolation, Path: "cmd/server/production_project_ontology_test.go", Reason: "project graph assertions are scoped by project tenant"},
	}

	report := ValidateProductionCoverageGate([]MappingRule{minimalCoverageMappingRule()}, ProductionCoverageManifest{Records: []ProductionCoverageRecord{record}}, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/project.created production test evidence file missing cmd/server/missing_ontology_test.go")
}

func TestProductionCoverageGateReportsSuccessTestEvidenceWithoutEventType(t *testing.T) {
	record := minimalConnectedCoverageRecord()
	record.TestEvidence = []ProductionTestEvidence{
		{Kind: ProductionTestEvidenceSuccess, Path: "cmd/server/production_ontology_governance_command_api_test.go"},
		{Kind: ProductionTestEvidenceFailure, Path: "cmd/server/production_ontology_projection_api_test.go", Reason: "projection audit missing target paths are replayable and reported"},
		{Kind: ProductionTestEvidenceReplay, Path: "cmd/server/production_ontology_projection_api_test.go", Reason: "projection audit replay verifies skipped event recovery"},
		{Kind: ProductionTestEvidenceTenantIsolation, Path: "cmd/server/production_project_ontology_test.go", Reason: "project graph assertions are scoped by project tenant"},
	}
	manifest := DefaultProductionCoverageManifest()
	manifest.Records = replaceProductionCoverageRecord(t, manifest.Records, record)

	report := ValidateProductionCoverageGate(defaultMappingRules(), manifest, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/project.created production success test evidence does not contain event type project.created")
}

func TestProductionCoverageGateReportsNonSuccessTestEvidenceWithoutEventType(t *testing.T) {
	record := minimalConnectedCoverageRecord()
	record.TestEvidence = []ProductionTestEvidence{
		{Kind: ProductionTestEvidenceSuccess, Path: "cmd/server/production_project_ontology_test.go"},
		{Kind: ProductionTestEvidenceFailure, Path: "cmd/server/production_ontology_governance_command_api_test.go", Reason: "projection audit missing target paths are replayable and reported"},
		{Kind: ProductionTestEvidenceReplay, Path: "cmd/server/production_ontology_governance_command_api_test.go", Reason: "projection audit replay verifies skipped event recovery"},
		{Kind: ProductionTestEvidenceTenantIsolation, Path: "cmd/server/production_ontology_governance_command_api_test.go", Reason: "project graph assertions are scoped by project tenant"},
	}
	manifest := DefaultProductionCoverageManifest()
	manifest.Records = replaceProductionCoverageRecord(t, manifest.Records, record)

	report := ValidateProductionCoverageGate(defaultMappingRules(), manifest, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/project.created production failure test evidence does not contain event type project.created")
	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/project.created production replay test evidence does not contain event type project.created")
	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/project.created production tenant_isolation test evidence does not contain event type project.created")
}

func TestProductionCoverageGateReportsProductionTestEvidenceNotTestFile(t *testing.T) {
	record := minimalConnectedCoverageRecord()
	record.TestEvidence = []ProductionTestEvidence{
		{Kind: ProductionTestEvidenceSuccess, Path: "cmd/server/production_project_ontology_api.go"},
		{Kind: ProductionTestEvidenceFailure, Path: "cmd/server/production_ontology_projection_api_test.go", Reason: "projection audit missing target paths are replayable and reported"},
		{Kind: ProductionTestEvidenceReplay, Path: "cmd/server/production_ontology_projection_api_test.go", Reason: "projection audit replay verifies skipped event recovery"},
		{Kind: ProductionTestEvidenceTenantIsolation, Path: "cmd/server/production_project_ontology_test.go", Reason: "project graph assertions are scoped by project tenant"},
	}
	manifest := DefaultProductionCoverageManifest()
	manifest.Records = replaceProductionCoverageRecord(t, manifest.Records, record)

	report := ValidateProductionCoverageGate(defaultMappingRules(), manifest, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/project.created production test evidence must point to a _test.go file cmd/server/production_project_ontology_api.go")
}

func TestProductionCoverageGateReportsMissingContextGraphAssertionEvidence(t *testing.T) {
	record := minimalConnectedCoverageRecord()
	record.ContextGraphAssertions = nil

	report := ValidateProductionCoverageGate([]MappingRule{minimalCoverageMappingRule()}, ProductionCoverageManifest{Records: []ProductionCoverageRecord{record}}, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/project.created missing contextgraph assertion evidence")
}

func TestProductionCoverageGateReportsMissingContextGraphEvidenceAssertion(t *testing.T) {
	record := minimalConnectedCoverageRecord()
	record.ContextGraphAssertions = []string{"object:project", "fact:project.title", "fact:project.status"}
	manifest := DefaultProductionCoverageManifest()
	manifest.Records = replaceProductionCoverageRecord(t, manifest.Records, record)

	report := ValidateProductionCoverageGate(defaultMappingRules(), manifest, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/project.created missing contextgraph evidence assertion")
}

func TestProductionCoverageGateReportsMissingContextGraphObjectAssertion(t *testing.T) {
	manifest := DefaultProductionCoverageManifest()
	record := findProductionCoverageRecord(t, manifest.Records, "project.created")
	record.ContextGraphAssertions = []string{"fact:project.title", "evidence:project-created"}
	manifest.Records = replaceProductionCoverageRecord(t, manifest.Records, record)

	report := ValidateProductionCoverageGate(defaultMappingRules(), manifest, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/project.created missing contextgraph object assertion")
}

func TestProductionCoverageGateReportsMissingContextGraphFactAssertion(t *testing.T) {
	manifest := DefaultProductionCoverageManifest()
	record := findProductionCoverageRecord(t, manifest.Records, "project.created")
	record.ContextGraphAssertions = []string{"object:project", "evidence:project-created"}
	manifest.Records = replaceProductionCoverageRecord(t, manifest.Records, record)

	report := ValidateProductionCoverageGate(defaultMappingRules(), manifest, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/project.created missing contextgraph fact assertion")
}

func TestProductionCoverageGateReportsMissingContextGraphLinkAssertion(t *testing.T) {
	manifest := DefaultProductionCoverageManifest()
	record := findProductionCoverageRecord(t, manifest.Records, "project.organization.linked")
	record.ContextGraphAssertions = []string{"object:project", "fact:project.title", "evidence:project-organization-linked"}
	manifest.Records = replaceProductionCoverageRecord(t, manifest.Records, record)

	report := ValidateProductionCoverageGate(defaultMappingRules(), manifest, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/project.organization.linked missing contextgraph link assertion")
}

func TestProductionCoverageGateReportsMissingDefinitionGovernanceEvidence(t *testing.T) {
	record := minimalConnectedCoverageRecord()
	record.DefinitionGovernanceEvidence = nil

	report := ValidateProductionCoverageGate([]MappingRule{minimalCoverageMappingRule()}, ProductionCoverageManifest{Records: []ProductionCoverageRecord{record}}, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/project.created missing definition governance evidence")
}

func TestProductionCoverageGateReportsDefinitionGovernanceEvidenceWithoutPromotionDefinition(t *testing.T) {
	record := minimalConnectedCoverageRecord()
	record.DefinitionGovernanceEvidence = []string{"cmd/server/production_project_ontology_test.go"}

	report := ValidateProductionCoverageGate([]MappingRule{minimalCoverageMappingRule()}, ProductionCoverageManifest{Records: []ProductionCoverageRecord{record}}, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/project.created definition governance evidence does not prove 621 metadata proposal definition promotion")
}

func TestProductionCoverageGateReportsSeededOnlyMissingDefinitionGovernanceEvidence(t *testing.T) {
	manifest := DefaultProductionCoverageManifest()
	record := findProductionCoverageRecord(t, manifest.Records, "legal.entity.created")
	record.DefinitionGovernanceEvidence = nil
	manifest.Records = replaceProductionCoverageRecord(t, manifest.Records, record)

	report := ValidateProductionCoverageGate(defaultMappingRules(), manifest, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: legal/legal.entity.created missing definition governance evidence")
}

func TestProductionCoverageGateReportsSeededOnlyDefinitionGovernanceEvidenceWithoutPromotionDefinition(t *testing.T) {
	manifest := DefaultProductionCoverageManifest()
	record := findProductionCoverageRecord(t, manifest.Records, "legal.entity.created")
	record.DefinitionGovernanceEvidence = []string{"cmd/server/production_project_ontology_test.go"}
	manifest.Records = replaceProductionCoverageRecord(t, manifest.Records, record)

	report := ValidateProductionCoverageGate(defaultMappingRules(), manifest, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: legal/legal.entity.created definition governance evidence does not prove 621 metadata proposal definition promotion")
}

func TestProductionCoverageGateReportsMissingEventMetadata(t *testing.T) {
	record := minimalConnectedCoverageRecord()
	record.RequiredEventMetadata = []string{"actor", "stream_id", "version", "idempotency_or_correlation"}

	report := ValidateProductionCoverageGate([]MappingRule{minimalCoverageMappingRule()}, ProductionCoverageManifest{Records: []ProductionCoverageRecord{record}}, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/project.created missing required event metadata tenant_id")
}

func TestProductionCoverageGateReportsSeededOnlyMissingEventMetadata(t *testing.T) {
	manifest := DefaultProductionCoverageManifest()
	record := findProductionCoverageRecord(t, manifest.Records, "legal.entity.created")
	record.RequiredEventMetadata = []string{"actor", "stream_id", "version", "idempotency_or_correlation"}
	manifest.Records = replaceProductionCoverageRecord(t, manifest.Records, record)

	report := ValidateProductionCoverageGate(defaultMappingRules(), manifest, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: legal/legal.entity.created missing required event metadata tenant_id")
}

func TestProductionCoverageGateReportsMissingPayloadField(t *testing.T) {
	record := minimalConnectedCoverageRecord()
	record.RequiredPayloadFields = []string{"id", "status"}

	report := ValidateProductionCoverageGate([]MappingRule{minimalCoverageMappingRule()}, ProductionCoverageManifest{Records: []ProductionCoverageRecord{record}}, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: mission_control/project.created missing required payload field title")
}

func TestProductionCoverageGateRequiresExplicitExemptionForSeededOnlyStatus(t *testing.T) {
	record := seededOnlyCoverage("resource_admin", "tool rule", "resource.tool.registered", payload("id", "name"), ProductionCoverageExemption{})
	record.Exemption = nil

	report := ValidateProductionCoverageGate([]MappingRule{{
		ID:        "resource.tool.registered",
		EventType: "resource.tool.registered",
		Enabled:   true,
		Rule: map[string]any{
			"objects": []any{map[string]any{"type": "tool", "id": "$.id", "canonical_key": "tool:{id}", "label": "$.name"}},
		},
	}}, ProductionCoverageManifest{Records: []ProductionCoverageRecord{record}}, gateTestTime())

	assertProductionCoverageIssue(t, report, "context graph production gate: resource_admin/resource.tool.registered requires explicit exemption for status seeded_only")
}

func TestProductionCoverageGateReportsSeededOnlyWithProductionEvidence(t *testing.T) {
	manifest := DefaultProductionCoverageManifest()
	record := findProductionCoverageRecord(t, manifest.Records, "legal.entity.created")
	record.ProductionPublishers = []string{"cmd/server/production_project_ontology_api.go:76"}
	record.TestEvidence = []ProductionTestEvidence{{Kind: ProductionTestEvidenceSuccess, Path: "cmd/server/production_project_ontology_test.go"}}
	record.ContextGraphAssertions = []string{"object:legal_entity", "evidence:legal-entity-created"}
	manifest.Records = replaceProductionCoverageRecord(t, manifest.Records, record)

	report := ValidateProductionCoverageGate(defaultMappingRules(), manifest, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: legal/legal.entity.created status seeded_only cannot declare production publisher")
	assertProductionCoverageIssue(t, report, "context graph production gate: legal/legal.entity.created status seeded_only cannot declare production test evidence")
	assertProductionCoverageIssue(t, report, "context graph production gate: legal/legal.entity.created status seeded_only cannot declare contextgraph assertions")
}

func TestProductionCoverageGateReportsExpiredExemptionWithOwnerAndExpiry(t *testing.T) {
	record := notConnectedCoverage("todo", "todo missing", "todo.created", businessSources("internal/app/todo/service.go"), ProductionCoverageExemption{
		Owner:     "platform-ontology",
		Reason:    "todo graph producer not connected",
		ExpiresAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	})

	report := ValidateProductionCoverageGate(nil, ProductionCoverageManifest{Records: []ProductionCoverageRecord{record}}, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: todo/todo.created exemption expired owner=platform-ontology expires_at=2026-06-01")
}

func TestProductionCoverageGateReportsIncompleteExemptionOwnerAndReason(t *testing.T) {
	record := notConnectedCoverage("todo", "todo missing", "todo.created", businessSources("internal/app/todo/service.go"), ProductionCoverageExemption{
		ExpiresAt: time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
	})

	report := ValidateProductionCoverageGate(nil, ProductionCoverageManifest{Records: []ProductionCoverageRecord{record}}, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: todo/todo.created exemption missing owner")
	assertProductionCoverageIssue(t, report, "context graph production gate: todo/todo.created exemption missing reason")
}

func TestProductionCoverageGateReportsMissingBusinessSourceEvidenceForNotConnectedStatus(t *testing.T) {
	record := notConnectedCoverage("todo", "todo missing", "todo.created", nil, ProductionCoverageExemption{
		Owner:     "platform-ontology",
		Reason:    "todo graph producer not connected",
		ExpiresAt: time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
	})

	report := ValidateProductionCoverageGate(nil, ProductionCoverageManifest{Records: []ProductionCoverageRecord{record}}, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: todo/todo.created missing business source evidence")
}

func TestProductionCoverageGateReportsMissingBusinessSourceEvidenceFile(t *testing.T) {
	record := notConnectedCoverage("todo", "todo missing", "todo.created", businessSources("internal/app/todo/missing_source.go"), ProductionCoverageExemption{
		Owner:     "platform-ontology",
		Reason:    "todo graph producer not connected",
		ExpiresAt: time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
	})

	report := ValidateProductionCoverageGate(nil, ProductionCoverageManifest{Records: []ProductionCoverageRecord{record}}, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: todo/todo.created business source evidence file missing internal/app/todo/missing_source.go")
}

func TestProductionCoverageGateReportsBusinessSourceEvidenceOutsideRequired0618Module(t *testing.T) {
	record := notConnectedCoverage("todo", "todo missing", "todo.created", businessSources("cmd/server/default_agents.go"), ProductionCoverageExemption{
		Owner:     "platform-ontology",
		Reason:    "todo graph producer not connected",
		ExpiresAt: time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
	})
	manifest := DefaultProductionCoverageManifest()
	manifest.Records = replaceProductionCoverageRecord(t, manifest.Records, record)

	report := ValidateProductionCoverageGate(defaultMappingRules(), manifest, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: todo/todo.created business source evidence cmd/server/default_agents.go outside required 0618 production module todo")
}

func TestProductionCoverageGateReportsNotConnectedWithMappingOrProductionEvidence(t *testing.T) {
	record := notConnectedCoverage("todo", "todo missing", "todo.created", businessSources("internal/app/todo/service.go"), ProductionCoverageExemption{
		Owner:     "platform-ontology",
		Reason:    "todo graph producer not connected",
		ExpiresAt: time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
	})
	record.MappingRuleID = "todo.created"
	record.RequiredEventMetadata = []string{"tenant_id"}
	record.RequiredPayloadFields = []string{"id"}
	record.DefinitionGovernanceEvidence = defaultDefinitionGovernanceEvidence()
	record.ProductionPublishers = []string{"internal/app/todo/service.go"}
	record.TestEvidence = []ProductionTestEvidence{{Kind: ProductionTestEvidenceSuccess, Path: "internal/app/todo/service_test.go"}}
	record.ContextGraphAssertions = []string{"object:todo", "evidence:todo-created"}

	report := ValidateProductionCoverageGate(nil, ProductionCoverageManifest{Records: []ProductionCoverageRecord{record}}, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: todo/todo.created status not_connected cannot declare mapping rule id")
	assertProductionCoverageIssue(t, report, "context graph production gate: todo/todo.created status not_connected cannot declare required event metadata")
	assertProductionCoverageIssue(t, report, "context graph production gate: todo/todo.created status not_connected cannot declare required payload fields")
	assertProductionCoverageIssue(t, report, "context graph production gate: todo/todo.created status not_connected cannot declare definition governance evidence")
	assertProductionCoverageIssue(t, report, "context graph production gate: todo/todo.created status not_connected cannot declare production publisher")
	assertProductionCoverageIssue(t, report, "context graph production gate: todo/todo.created status not_connected cannot declare production test evidence")
	assertProductionCoverageIssue(t, report, "context graph production gate: todo/todo.created status not_connected cannot declare contextgraph assertions")
}

func TestProductionCoverageGateReportsConsumerDeferredFor0618ProductionEvent(t *testing.T) {
	record := notConnectedCoverage("todo", "todo missing", "todo.created", nil, ProductionCoverageExemption{
		Owner:     "platform-ontology",
		Reason:    "consumer contract is deferred",
		ExpiresAt: time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
	})
	record.Status = ProductionCoverageConsumerDeferred

	manifest := DefaultProductionCoverageManifest()
	manifest.Records = replaceProductionCoverageRecord(t, manifest.Records, record)

	report := ValidateProductionCoverageGate(defaultMappingRules(), manifest, gateTestTime())
	assertProductionCoverageIssue(t, report, "context graph production gate: todo/todo.created status consumer_deferred cannot classify 0618 production coverage")
}

func TestProductionCoverageGateAllowsValidNotConnectedExemption(t *testing.T) {
	record := notConnectedCoverage("discussion", "discussion missing", "discussion.message.created", businessSources("internal/app/discussionsession/service.go"), ProductionCoverageExemption{
		Owner:     "platform-ontology",
		Reason:    "discussion graph producer not connected",
		ExpiresAt: time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
	})
	manifest := DefaultProductionCoverageManifest()
	manifest.Records = replaceProductionCoverageRecord(t, manifest.Records, record)

	report := ValidateProductionCoverageGate(defaultMappingRules(), manifest, gateTestTime())
	if !report.OK() {
		t.Fatalf("valid not-connected exemption should pass, got:\n%s", report.Error())
	}
}

func minimalConnectedCoverageRecord() ProductionCoverageRecord {
	return ProductionCoverageRecord{
		Domain:                "mission_control",
		Feature:               "project creation",
		EventType:             "project.created",
		MappingRuleID:         "project.created",
		Status:                ProductionCoverageProductionConnected,
		RequiredEventMetadata: []string{"tenant_id", "actor", "stream_id", "version", "idempotency_or_correlation"},
		RequiredPayloadFields: []string{"id", "title", "status"},
		ProductionPublishers:  []string{"cmd/server/production_project_ontology_api.go:76"},
		DefinitionGovernanceEvidence: []string{
			"internal/app/ontology/metadata_proposal_promotion_test.go",
			"cmd/server/production_ontology_governance_command_api_test.go",
		},
		TestEvidence: []ProductionTestEvidence{
			{Kind: ProductionTestEvidenceSuccess, Path: "cmd/server/production_project_ontology_test.go"},
			{Kind: ProductionTestEvidenceFailure, Path: "cmd/server/production_ontology_projection_api_test.go", Reason: "projection audit missing target paths are replayable and reported"},
			{Kind: ProductionTestEvidenceReplay, Path: "cmd/server/production_ontology_projection_api_test.go", Reason: "projection audit replay verifies skipped event recovery"},
			{Kind: ProductionTestEvidenceTenantIsolation, Path: "cmd/server/production_project_ontology_test.go", Reason: "project graph assertions are scoped by project tenant"},
		},
		ContextGraphAssertions: []string{"object:project", "fact:project.title", "fact:project.status", "evidence:project-created"},
	}
}

func minimalCoverageMappingRule() MappingRule {
	return MappingRule{
		ID:          "project.created",
		EventType:   "project.created",
		ObjectTypes: []string{"project"},
		Enabled:     true,
		Rule: map[string]any{
			"objects": []any{
				map[string]any{"type": "project", "id": "$.id", "canonical_key": "project:{id}"},
			},
			"properties": []any{
				map[string]any{"object_type": "project", "object_id": "$.id", "property_id": "title", "value": "$.title"},
				map[string]any{"object_type": "project", "object_id": "$.id", "property_id": "status", "value": "$.status"},
			},
		},
	}
}

func assertProductionCoverageIssue(t *testing.T, report ProductionCoverageGateReport, want string) {
	t.Helper()
	if report.OK() {
		t.Fatalf("expected production coverage gate failure containing %q", want)
	}
	if !strings.Contains(report.Error(), want) {
		t.Fatalf("production coverage gate failure should contain %q, got:\n%s", want, report.Error())
	}
}

func assertProductionCoverageExemption(t *testing.T, exemptions []ProductionCoverageExemptionSummary, domain string, eventType string, status ProductionCoverageStatus, owner string, reasonContains string, expiresAt string) {
	t.Helper()
	for _, exemption := range exemptions {
		if exemption.Domain != domain || exemption.EventType != eventType {
			continue
		}
		if exemption.Status != status || exemption.Owner != owner || !strings.Contains(exemption.Reason, reasonContains) || exemption.ExpiresAt.Format("2006-01-02") != expiresAt {
			t.Fatalf("context graph production gate exemption for %s/%s = %+v", domain, eventType, exemption)
		}
		return
	}
	t.Fatalf("context graph production gate exemption for %s/%s not found in %+v", domain, eventType, exemptions)
}

func gateTestTime() time.Time {
	return time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC)
}

func removeProductionCoverageRecord(records []ProductionCoverageRecord, eventType string) []ProductionCoverageRecord {
	out := make([]ProductionCoverageRecord, 0, len(records))
	for _, record := range records {
		if record.EventType == eventType {
			continue
		}
		out = append(out, record)
	}
	return out
}

func replaceProductionCoverageRecord(t *testing.T, records []ProductionCoverageRecord, replacement ProductionCoverageRecord) []ProductionCoverageRecord {
	t.Helper()
	out := append([]ProductionCoverageRecord(nil), records...)
	for i, record := range out {
		if record.EventType == replacement.EventType {
			out[i] = replacement
			return out
		}
	}
	t.Fatalf("coverage record %s not found", replacement.EventType)
	return nil
}

func findProductionCoverageRecord(t *testing.T, records []ProductionCoverageRecord, eventType string) ProductionCoverageRecord {
	t.Helper()
	for _, record := range records {
		if record.EventType == eventType {
			return record
		}
	}
	t.Fatalf("coverage record %s not found", eventType)
	return ProductionCoverageRecord{}
}

func auditReportContainsRecord(report ProductionCoverageAuditReport, domain string, eventType string, status ProductionCoverageStatus) bool {
	for _, record := range report.Records {
		if record.Domain == domain && record.EventType == eventType && record.Status == status {
			return true
		}
	}
	return false
}

func collectOntologyProductionEventTypesFromSource(t *testing.T) map[string]bool {
	t.Helper()
	eventTypes := map[string]bool{}
	for _, publisher := range collectOntologyProductionPublishersFromSource(t) {
		eventTypes[publisher.EventType] = true
	}
	return eventTypes
}

func collectOntologyProductionPublishersFromSource(t *testing.T) []ProductionEventPublisherEvidence {
	t.Helper()
	moduleRoot := productionCoverageTestModuleRoot(t)
	publishers := []ProductionEventPublisherEvidence{}
	for _, root := range ontologyProductionSourceRoots(moduleRoot) {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			relativePath, err := filepath.Rel(moduleRoot, path)
			if err != nil {
				t.Fatalf("rel %s: %v", path, err)
			}
			for eventType := range collectOntologyProductionEventTypesFromFile(t, path) {
				publishers = append(publishers, ProductionEventPublisherEvidence{
					EventType: eventType,
					Path:      filepath.ToSlash(relativePath),
				})
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	return publishers
}

func ontologyProductionSourceRoots(moduleRoot string) []string {
	return []string{
		filepath.Join(moduleRoot, "cmd", "server"),
		filepath.Join(moduleRoot, "internal", "app", "knowledgeadmin"),
		filepath.Join(moduleRoot, "internal", "knowledge", "ingest"),
		filepath.Join(moduleRoot, "internal", "app", "memoryadmin"),
		filepath.Join(moduleRoot, "internal", "app", "todo"),
		filepath.Join(moduleRoot, "internal", "app", "discussionsession"),
		filepath.Join(moduleRoot, "internal", "app", "scheduler"),
		filepath.Join(moduleRoot, "internal", "app", "operations"),
		filepath.Join(moduleRoot, "internal", "app", "rebac"),
		filepath.Join(moduleRoot, "internal", "app", "missioncontrol"),
		filepath.Join(moduleRoot, "internal", "agent", "runtime"),
	}
}

func collectOntologyProductionEventTypesFromFile(t *testing.T, path string) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	eventTypes := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		lit, ok := node.(*ast.CompositeLit)
		if !ok || !isGraphProtocolDomainEvent(lit.Type) {
			return true
		}
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			ident, ok := kv.Key.(*ast.Ident)
			if !ok || ident.Name != "Type" {
				continue
			}
			if value, ok := kv.Value.(*ast.BasicLit); ok && value.Kind == token.STRING {
				eventType := strings.Trim(value.Value, `"`)
				if isOntologyProductionEventType(eventType) {
					eventTypes[eventType] = true
				}
			}
		}
		return true
	})
	return eventTypes
}

func isGraphProtocolDomainEvent(expr ast.Expr) bool {
	switch typed := expr.(type) {
	case *ast.SelectorExpr:
		return typed.Sel.Name == "DomainEvent"
	case *ast.Ident:
		return typed.Name == "DomainEvent"
	default:
		return false
	}
}

func isOntologyProductionEventType(eventType string) bool {
	for _, prefix := range []string{
		"mission.",
		"project.",
		"organization.",
		"resource.",
		"people.",
		"action.",
		"knowledge.",
		"memory.",
		"planning.",
		"todo.",
		"discussion.",
		"scheduler.",
		"agent.runtime.",
		"permission.",
		"rebac.",
		"legal.",
		"development.",
		"value.",
	} {
		if strings.HasPrefix(eventType, prefix) {
			return true
		}
	}
	return false
}

func productionCoverageTestModuleRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			t.Fatal("go.mod not found")
		}
		wd = parent
	}
}

func parseProductionCoverageTestSource(t *testing.T) *ast.File {
	t.Helper()
	path := filepath.Join(productionCoverageTestModuleRoot(t), "internal", "app", "ontology", "production_coverage_test.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return file
}

func findTestFunction(t *testing.T, source *ast.File, name string) *ast.FuncDecl {
	t.Helper()
	for _, declaration := range source.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == name {
			return function
		}
	}
	t.Fatalf("test function %s not found", name)
	return nil
}

func isIdentCall(call *ast.CallExpr, name string) bool {
	ident, ok := call.Fun.(*ast.Ident)
	return ok && ident.Name == name
}

func isSelectorCall(call *ast.CallExpr, packageName string, selectorName string) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != selectorName {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && ident.Name == packageName
}

func isTimeNowUTCCall(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "UTC" {
		return false
	}
	inner, ok := selector.X.(*ast.CallExpr)
	return ok && isSelectorCall(inner, "time", "Now")
}
