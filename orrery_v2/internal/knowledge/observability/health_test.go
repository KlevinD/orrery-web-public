package observability_test

import (
	"context"
	"strings"
	"testing"

	"orrery_v2/internal/knowledge/observability"
	"orrery_v2/internal/knowledge/vector"
)

func TestHealthProbeReportsVectorAndMetadataWithoutPathLeak(t *testing.T) {
	probe := observability.NewHealthProbe(observability.HealthDeps{
		MetadataReady: func(context.Context) error { return nil },
		VectorReady:   func(context.Context) error { return nil },
	})

	status := probe.Check(context.Background())
	if !status.OK {
		t.Fatalf("expected OK status: %#v", status)
	}
	if strings.Contains(status.Message, "D:/") || strings.Contains(status.Message, "/tmp/") {
		t.Fatalf("health message leaked path: %q", status.Message)
	}
}

func TestHealthProbeReportsDependencyFailure(t *testing.T) {
	probe := observability.NewHealthProbe(observability.HealthDeps{
		MetadataReady: func(context.Context) error { return assertErr("metadata down") },
		VectorReady:   func(context.Context) error { return nil },
	})

	status := probe.Check(context.Background())
	if status.OK || !strings.Contains(status.Message, "metadata") {
		t.Fatalf("expected metadata failure, got %#v", status)
	}
}

func TestHealthProbeReportsDisabledWhenNoActiveBase(t *testing.T) {
	vectorCalled := false
	probe := observability.NewHealthProbe(observability.HealthDeps{
		MetadataReady: func(context.Context) error { return nil },
		RootReady:     func(context.Context) error { return nil },
		ActiveBases:   func(context.Context) (int64, error) { return 0, nil },
		VectorReady: func(context.Context) error {
			vectorCalled = true
			return nil
		},
	})

	status := probe.Check(context.Background())
	if !status.OK || status.Message != "knowledge disabled" {
		t.Fatalf("expected disabled ready status, got %#v", status)
	}
	if vectorCalled {
		t.Fatal("vector readiness should not run when no active knowledge base exists")
	}
}

func TestHealthProbeReportsRootFailureBeforeDisabled(t *testing.T) {
	probe := observability.NewHealthProbe(observability.HealthDeps{
		MetadataReady: func(context.Context) error { return nil },
		RootReady:     func(context.Context) error { return assertErr("D:/secret/knowledge root failed") },
		ActiveBases:   func(context.Context) (int64, error) { return 0, nil },
	})

	status := probe.Check(context.Background())
	if status.OK || status.Message != "knowledge root unavailable" {
		t.Fatalf("expected sanitized root failure, got %#v", status)
	}
}

func TestHealthProbeReportsVectorFailureWithoutPathLeak(t *testing.T) {
	probe := observability.NewHealthProbe(observability.HealthDeps{
		MetadataReady: func(context.Context) error { return nil },
		RootReady:     func(context.Context) error { return nil },
		ActiveBases:   func(context.Context) (int64, error) { return 1, nil },
		VectorReady:   func(context.Context) error { return assertErr("open D:/secret/knowledge/kb-1/lancedb/chunks.json") },
	})

	status := probe.Check(context.Background())
	if status.OK || status.Message != "knowledge vector unavailable" {
		t.Fatalf("expected sanitized vector failure, got %#v", status)
	}
	if strings.Contains(status.Message, "D:/") || strings.Contains(status.Message, "secret") {
		t.Fatalf("health message leaked path: %q", status.Message)
	}
}

func TestCheckVectorAdapterCreatesAndDropsDescriptor(t *testing.T) {
	store := &fakeHealthVectorStore{}
	descriptor := vector.Descriptor{KnowledgeBaseID: "healthcheck", StorePath: "health/store"}

	if err := observability.CheckVectorAdapter(context.Background(), store, descriptor); err != nil {
		t.Fatalf("check vector adapter: %v", err)
	}
	if !store.ensured || !store.dropped {
		t.Fatalf("expected ensure and drop calls, got ensure=%v drop=%v", store.ensured, store.dropped)
	}
}

type assertErr string

func (e assertErr) Error() string { return string(e) }

type fakeHealthVectorStore struct {
	ensured bool
	dropped bool
}

func (s *fakeHealthVectorStore) EnsureKnowledgeBase(context.Context, vector.Descriptor) error {
	s.ensured = true
	return nil
}

func (s *fakeHealthVectorStore) DropKnowledgeBase(context.Context, vector.Descriptor) error {
	s.dropped = true
	return nil
}
