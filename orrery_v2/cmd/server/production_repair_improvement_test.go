package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/improvement"
	"orrery_v2/internal/app/reliability"
)

func TestProductionAutomaticRepairFeedbackConnectsFailureRunAndImprovement(t *testing.T) {
	for _, status := range []string{"needs_attention", "technical_recovered", "resolved"} {
		t.Run(status, func(t *testing.T) {
			ctx := context.Background()
			core := newOntologyEventLogTestCore(t, "repair-feedback-"+status)
			runner, err := newProductionOntologyProjectionRunner(ctx, core)
			if err != nil {
				t.Fatal(err)
			}
			sink := newProductionAgentExecutionOntologyTestSink(core, runner, time.Now)
			sink.improvements = improvement.NewStore(core)
			repository := reliability.NewMemoryRepository()
			service := reliability.NewService(repository, reliability.WithEventSink(sink))
			i, err := service.ObserveFailure(ctx, reliability.FailureObservation{
				ID: "fault", TenantID: "tenant-a", Source: "server_log", Component: "api", ErrorCode: "timeout", Message: "timeout", OccurredAt: time.Unix(100, 0).UTC(),
			})
			if err != nil {
				t.Fatal(err)
			}
			r := reliability.AutomaticRepairReceipt{
				Version: 1, TenantID: i.TenantID, IncidentID: i.ID, OccurrenceCount: i.OccurrenceCount, LastSeenNS: i.LastSeenAt.UnixNano(),
				JobID: "repair-job", BatchID: "repair-batch", RepairRunID: "actual-repair-run", Status: status, Verified: status == "resolved",
				Evidence: []string{"verification-tool"}, Summary: "PRIVATE_AGENT_DIAGNOSIS", UpdatedAt: time.Unix(103, 0).UTC(),
			}
			for replay := 0; replay < 2; replay++ {
				if err := service.ApplyAutomaticRepairReceipt(ctx, r); err != nil {
					t.Fatal(err)
				}
			}
			page, err := sink.improvements.List(ctx, r.TenantID, "", 10)
			if err != nil || len(page.Items) != 1 {
				t.Fatalf("feedback missing or duplicated: %+v %v", page, err)
			}
			c := page.Items[0]
			want := improvement.Observed
			if status == "needs_attention" {
				want = improvement.Blocked
			}
			if c.State != want || c.ChangeSet != nil || c.Release != nil || strings.Contains(c.Observation.Summary, "PRIVATE") {
				t.Fatalf("feedback promoted or exposed diagnosis: %+v", c)
			}
			feedback, err := sink.improvements.GetManagedRepairFeedback(ctx, r.TenantID, c.Observation.EvidenceIDs[0])
			if err != nil || feedback.RepairRunID != r.RepairRunID || feedback.Verified != r.Verified || feedback.Status != status || feedback.Summary != r.Summary {
				t.Fatalf("immutable acceptance evidence: %+v %v", feedback, err)
			}
			if _, err := sink.improvements.GetManagedRepairFeedback(ctx, "other-tenant", c.Observation.EvidenceIDs[0]); err == nil {
				t.Fatal("repair evidence leaked across tenants")
			}
			for _, event := range improvement.OntologyEvents(c) {
				if event.Type != "improvement.managed_repair.upserted" || event.Payload["failure_incident_id"] != r.IncidentID {
					t.Fatalf("wrong repair feedback event: %+v", event)
				}
				delete(event.Payload, "id")
				if _, err := runner.ApplyEvent(ctx, event); err == nil {
					t.Fatal("incomplete repair feedback bypassed graph conformance")
				}
			}
			wantProjections := 1
			if status == "needs_attention" {
				wantProjections = 2
			}
			if n, err := sink.improvements.ProjectPending(ctx, sink, 10); err != nil || n != wantProjections {
				t.Fatalf("project feedback: %d %v", n, err)
			}
			objects, links, _, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, r.TenantID)
			if err != nil {
				t.Fatal(err)
			}
			caseCount, faultLinks, runLinks := 0, 0, 0
			for _, object := range objects {
				if object.ObjectType == "improvement_case" {
					caseCount++
					if object.OwnerAccessRef.Kind != "failure_incident" || object.OwnerAccessRef.ID != r.IncidentID || object.OwnerAccessRef.TenantID != r.TenantID {
						t.Fatalf("feedback source access lost: %+v", object)
					}
				}
			}
			for _, link := range links {
				switch link.LinkType {
				case "improvement_case_from_failure_incident":
					faultLinks++
				case "improvement_case_observed_in_agent_run":
					runLinks++
				}
			}
			if caseCount != 1 || faultLinks != 1 || runLinks != 1 {
				t.Fatalf("failure/repair/improvement graph: %d/%d/%d", caseCount, faultLinks, runLinks)
			}
		})
	}
}

func TestProductionAutomaticRepairFailureEntersDiagnosisWithOriginalAgentConclusion(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "current-repair-diagnosis")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	sink := newProductionAgentExecutionOntologyTestSink(core, runner, time.Now)
	sink.improvements = improvement.NewStore(core)
	if _, err := sink.improvements.EnableRuntimeDiagnoses(ctx); err != nil {
		t.Fatal(err)
	}
	service := reliability.NewService(reliability.NewMemoryRepository(), reliability.WithEventSink(sink))
	i, err := service.ObserveFailure(ctx, reliability.FailureObservation{ID: "current-fault", TenantID: "tenant-a", Source: "server_log", Component: "api", ErrorCode: "provider_rejected", Message: "provider rejected", OccurredAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	r := reliability.AutomaticRepairReceipt{Version: 1, TenantID: i.TenantID, IncidentID: i.ID, OccurrenceCount: i.OccurrenceCount, LastSeenNS: i.LastSeenAt.UnixNano(), JobID: "job", BatchID: "batch", RepairRunID: "actual-run", Status: "needs_attention", Summary: "原 Agent 已查明外部拒绝，但供应商错误码含义仍待证实。", Evidence: []string{"actual-trace"}, UpdatedAt: time.Now().UTC()}
	if err := service.ApplyAutomaticRepairReceipt(ctx, r); err != nil {
		t.Fatal(err)
	}
	page, err := sink.improvements.List(ctx, r.TenantID, "", 10)
	if err != nil || len(page.Items) != 1 || page.Items[0].State != improvement.PendingDiagnosis {
		t.Fatal("accepted fault not routed for diagnosis", page, err)
	}
	c := page.Items[0]
	f, err := sink.improvements.GetManagedRepairFeedback(ctx, r.TenantID, c.Observation.EvidenceIDs[0])
	if err != nil || f.Summary != r.Summary || f.SourceAccessRef.ID != r.IncidentID || f.RepairRunID != r.RepairRunID {
		t.Fatal("original Agent evidence lost", f, err)
	}
	queue, err := core.ListRecords(ctx, "ontology_improvement_runtime_diagnosis_queue", "", 10)
	if err != nil || len(queue.Items) != 1 {
		t.Fatal("diagnosis handoff not durable", queue, err)
	}
}
