package main

import (
	"context"
	"testing"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/testkit"
)

func TestProductionOntologyMetadataSinksShareProjectionRunner(t *testing.T) {
	ctx := context.Background()
	runtime := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := runtime.Databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatalf("new projection runner: %v", err)
	}
	memorySink, err := newProductionOntologyMemoryRecordMetadataSinkWithRunner(core, runner)
	if err != nil {
		t.Fatalf("new memory sink: %v", err)
	}
	documentSink, err := newProductionOntologyDocumentMetadataSinkWithRunner(core, runner)
	if err != nil {
		t.Fatalf("new document sink: %v", err)
	}
	memory, ok := memorySink.(productionMemoryRecordOntologySink)
	if !ok || memory.runner != runner || documentSink.runner != runner {
		t.Fatalf("metadata sinks do not share the supplied runner")
	}
}
