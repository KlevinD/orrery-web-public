package contextview

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
)

type historicalContextGraph struct {
	*contextTestGraph
	historyObjects  []contextgraph.Object
	historyLinks    []contextgraph.Link
	historyFacts    []contextgraph.Fact
	historyEvidence []contextgraph.Evidence
	errHistory      error
}

func (g historicalContextGraph) SnapshotSubgraphAsOf(_ context.Context, tenant string, refs []contextgraph.Ref, depth int, limits contextgraph.CurrentSubgraphLimits, business, known time.Time) ([]contextgraph.Object, []contextgraph.Link, []contextgraph.Fact, []contextgraph.Evidence, error) {
	if tenant != "tenant-1" || len(refs) != 1 || refs[0].ID != "alpha" || depth != 2 || limits.Objects != 25 || limits.Links != 100 || business.IsZero() || known.IsZero() {
		return nil, nil, nil, nil, errors.New("unbounded or missing timeline")
	}
	return g.historyObjects, g.historyLinks, g.historyFacts, g.historyEvidence, g.errHistory
}

func TestHistoricalInferenceSnapshotRechecksCurrentAccessAndMasks(t *testing.T) {
	for _, mode := range []string{"visible", "hidden-current-root", "masked-value", "masked-time", "masked-link", "hidden-evidence", "legacy", "hidden-current-source", "history-budget"} {
		t.Run(mode, func(t *testing.T) {
			graph := contextGraphFixture()
			policy := contextTestPolicy{hidden: map[string]bool{}, masked: map[string][]string{}}
			switch mode {
			case "hidden-current-root":
				policy.hidden["object:alpha"] = true
			case "masked-value":
				policy.masked["fact:fact-1"] = []string{"value"}
			case "masked-time":
				policy.masked["fact:fact-1"] = []string{"temporal.business_valid_to"}
			case "masked-link":
				policy.masked["link:link-visible"] = []string{"status"}
			case "hidden-evidence":
				policy.hidden["evidence:evidence-1"] = true
			case "hidden-current-source":
				policy.hidden["object:zeta"] = true
			}
			service := newContextServiceForTest(t, graph, contextTestAuthorizer{}, policy)
			g := historicalContextGraph{contextTestGraph: graph, historyObjects: graph.neighborhood.Objects, historyLinks: graph.neighborhood.Links, historyFacts: append([]contextgraph.Fact(nil), graph.facts...), historyEvidence: graph.evidence}
			now := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
			g.historyFacts[0].Temporal = contextgraph.TemporalMetadata{Mode: "state_interval", BusinessTimeUnspecified: true, SourceRecordedAt: now.Add(-time.Hour)}
			g.historyFacts[0].Properties = map[string]any{"quote": "raw secret must never escape projected fields"}
			g.historyFacts[0].EvidenceRefs = []contextgraph.Ref{{Kind: "evidence", ID: "evidence-1"}}
			if mode == "hidden-current-source" {
				g.historyFacts[0].Provenance.Kind = "knowledge_instances"
				g.historyFacts[0].Properties["source_root_id"] = "zeta"
			}
			if mode == "legacy" {
				g.historyFacts[0].Provenance.Kind = "knowledge_instances"
				g.historyFacts[0].Temporal.Mode = "static"
			}
			if mode == "history-budget" {
				g.errHistory = contextgraph.ErrQueryBudgetExceeded
			}
			service.graph = g
			result, err := service.InferenceSnapshot(context.Background(), contextPrincipal("user-1"), InferenceSnapshotQuery{TenantID: "tenant-1", RootID: "alpha", BusinessAsOf: now, KnownAt: now})
			if mode == "hidden-current-root" || mode == "history-budget" {
				if err == nil {
					t.Fatal("access/budget failure admitted history")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "masked-value" || mode == "masked-time" || mode == "legacy" || mode == "hidden-current-source" {
				if len(result.Facts) != 0 {
					t.Fatal("masked/unversioned facts entered inference")
				}
			} else {
				if len(result.Facts) != 1 || result.Facts[0].Properties != nil || !result.Facts[0].Temporal.BusinessTimeUnspecified {
					t.Fatalf("projected historical fact: %+v", result.Facts)
				}
				if mode == "hidden-evidence" && len(result.Facts[0].EvidenceRefs) != 0 {
					t.Fatal("hidden evidence reference escaped")
				}
			}
			if mode == "masked-link" {
				for _, l := range result.Links {
					if l.ID == "link-visible" {
						t.Fatal("masked status became an eligible relation")
					}
				}
			}
			if mode == "legacy" && len(result.Limitations) == 0 {
				t.Fatal("missing history limitation")
			}
		})
	}
}
