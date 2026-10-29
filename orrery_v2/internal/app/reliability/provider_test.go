package reliability

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDefaultRepairProviderRegistryRunsDeterministicPreflightWithoutMutation(t *testing.T) {
	now := time.Date(2026, 9, 1, 16, 0, 0, 0, time.UTC)
	request := RepairDryRunRequest{
		ActionRunID: "run-1", TenantID: "tenant-a", ArgsHash: "sha256:args", ObservedAt: now,
		Incident: FailureIncident{
			ID: "incident-1", TenantID: "tenant-a", Fingerprint: "sha256:fingerprint", Component: "agent-runtime",
			Status: IncidentStatusOpen, Retryable: true, Version: 2,
		},
		Rule: RepairRule{
			ID: "rule-1", TenantID: "tenant-a", SelectorFingerprint: "sha256:fingerprint",
			Strategy: RepairStrategyRetry, ActionTypeID: "reliability.repair.execute", Status: RepairRuleStatusProposed,
			Reversible: true, Idempotent: true, Version: 3,
		},
		Target: &RepairTarget{
			TenantID: "tenant-a", ObservationID: "observation-1", IncidentID: "incident-1",
			Kind: RepairTargetMissionAgentRun, AuthorizationTenantID: "tenant-a", MissionID: "mission-1", RunID: "failed-run-1", OccurredAt: now,
		},
	}
	registry := NewDefaultRepairProviderRegistry()
	first, err := registry.DryRun(context.Background(), request)
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	second, err := registry.DryRun(context.Background(), request)
	if err != nil {
		t.Fatalf("DryRun replay: %v", err)
	}
	if first.ProviderCallID == "" || first.ResultID == "" || first.EvidenceID == "" || first.ResultID != second.ResultID || first.EvidenceID != second.EvidenceID {
		t.Fatalf("dry-run results are not stable: first=%+v second=%+v", first, second)
	}
	if request.Incident.Status != IncidentStatusOpen || request.Rule.Status != RepairRuleStatusProposed {
		t.Fatalf("dry-run mutated request aggregates: incident=%+v rule=%+v", request.Incident, request.Rule)
	}
}

func TestDefaultRepairProviderRegistryFailsClosed(t *testing.T) {
	now := time.Date(2026, 9, 1, 16, 0, 0, 0, time.UTC)
	request := RepairDryRunRequest{
		ActionRunID: "run-1", TenantID: "tenant-a", ArgsHash: "sha256:args", ObservedAt: now,
		Incident: FailureIncident{ID: "incident-1", TenantID: "tenant-a", Fingerprint: "sha256:fingerprint", Status: IncidentStatusOpen, Retryable: true},
		Rule: RepairRule{
			ID: "rule-1", TenantID: "tenant-a", SelectorFingerprint: "sha256:fingerprint", Strategy: RepairStrategyPatch,
			ActionTypeID: "reliability.repair.execute", Status: RepairRuleStatusProposed, Reversible: true, Idempotent: true,
		},
	}
	if _, err := NewDefaultRepairProviderRegistry().DryRun(context.Background(), request); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("patch dry-run error = %v, want ErrProviderUnavailable", err)
	}
	request.Rule.Strategy = RepairStrategyRetry
	request.Incident.Retryable = false
	if _, err := NewDefaultRepairProviderRegistry().DryRun(context.Background(), request); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("non-retryable dry-run error = %v, want ErrInvalidRequest", err)
	}
}

type incompleteRepairDryRunProvider struct{}

func (incompleteRepairDryRunProvider) Strategy() RepairStrategy { return RepairStrategyRetry }

func (incompleteRepairDryRunProvider) DryRun(context.Context, RepairDryRunRequest) (RepairDryRunResult, error) {
	return RepairDryRunResult{ProviderCallID: "provider-1", ResultID: "result-1", EvidenceID: "evidence-1"}, nil
}

func TestRepairProviderRegistryRejectsEvidenceWithoutChecks(t *testing.T) {
	registry, err := NewRepairProviderRegistry(incompleteRepairDryRunProvider{})
	if err != nil {
		t.Fatal(err)
	}
	request := RepairDryRunRequest{Rule: RepairRule{Strategy: RepairStrategyRetry}}
	if _, err := registry.DryRun(context.Background(), request); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("incomplete provider evidence error = %v, want ErrInvalidRequest", err)
	}
}
