package memoryontology

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
)

type referenceOnlyGraph struct {
	*contextgraph.MemoryStore
	reads int
}

func (g *referenceOnlyGraph) Objects(context.Context, string) ([]contextgraph.Object, error) {
	g.reads++
	return []contextgraph.Object{{ID: "person", TenantID: "parent", ObjectType: "person"}}, nil
}
func (g *referenceOnlyGraph) Snapshot(context.Context, string) ([]contextgraph.Object, []contextgraph.Link, []contextgraph.Fact, []contextgraph.Evidence, error) {
	return nil, nil, nil, nil, errors.New("reference lookup should not load unrelated graph tables")
}

func TestProcessorReferenceDictionaryReadsOnlyObjectsAndReusesThem(t *testing.T) {
	graph := &referenceOnlyGraph{MemoryStore: contextgraph.NewMemoryStore()}
	processor := &Processor{applier: NewGraphApplier(graph, nil, time.Now)}
	snapshots := make(map[string]tenantGraphSnapshot)
	for i := 0; i < 2; i++ {
		snapshot, err := processor.referenceSnapshot(context.Background(), "parent", snapshots)
		if err != nil || len(snapshot.objects) != 1 || len(snapshot.links) != 0 {
			t.Fatalf("snapshot=%+v err=%v", snapshot, err)
		}
	}
	if graph.reads != 1 {
		t.Fatalf("dictionary reads=%d", graph.reads)
	}
}
