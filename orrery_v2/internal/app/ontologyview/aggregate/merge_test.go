package aggregate

import (
	"testing"
	"time"

	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

func TestMergeCountsRequiresEveryInputToBeExact(t *testing.T) {
	got := mergeCounts(ontologycontract.ExactCount(2), ontologycontract.ExactCount(3))
	if got.Status != ontologycontract.MetricExact || got.Value == nil || *got.Value != 5 {
		t.Fatalf("exact merge = %+v", got)
	}
	got = mergeCounts(ontologycontract.ExactCount(2), ontologycontract.UnavailableCount())
	if got.Status != ontologycontract.MetricUnavailable || got.Value != nil {
		t.Fatalf("unavailable merge = %+v", got)
	}
	got = mergeCounts()
	if got.Status != ontologycontract.MetricExact || got.Value == nil || *got.Value != 0 {
		t.Fatalf("empty merge = %+v", got)
	}
}

func TestMergeRatiosRecomputesFromSummedComponents(t *testing.T) {
	got := mergeRatios(ontologycontract.NewRatio(1, 2), ontologycontract.NewRatio(2, 3))
	if got.Status != ontologycontract.MetricExact || got.Numerator == nil || *got.Numerator != 3 || got.Denominator == nil || *got.Denominator != 5 || got.Value == nil || *got.Value != 0.6 {
		t.Fatalf("exact ratio merge = %+v", got)
	}
	got = mergeRatios(ontologycontract.NewRatio(1, 2), ontologycontract.RatioMetric{Status: ontologycontract.MetricUnavailable})
	if got.Status != ontologycontract.MetricUnavailable || got.Value != nil {
		t.Fatalf("unavailable ratio merge = %+v", got)
	}
	if got = mergeRatios(); got.Status != ontologycontract.MetricUnavailable {
		t.Fatalf("empty ratio merge = %+v", got)
	}
	empty := ontologycontract.NewRatio(0, 0)
	sampled := ontologycontract.NewRatio(2, 4)
	sampled.Status = ontologycontract.MetricSampled
	got = mergeRatios(empty, sampled)
	if got.Status != ontologycontract.MetricSampled || got.Numerator == nil || *got.Numerator != 2 || got.Denominator == nil || *got.Denominator != 4 {
		t.Fatalf("sampled ratio merge = %+v", got)
	}
}

func TestAggregateTimestampBoundsAndSummaryMetaAreStable(t *testing.T) {
	first := time.Date(2026, 8, 1, 1, 0, 0, 0, time.FixedZone("offset", 8*60*60))
	second := time.Date(2026, 8, 2, 2, 0, 0, 0, time.UTC)
	if got := earliest(nil, &second, &first); got == nil || !got.Equal(first) || got.Location() != time.UTC {
		t.Fatalf("earliest = %v", got)
	}
	if got := latest(&first, nil, &second); got == nil || !got.Equal(second) || got.Location() != time.UTC {
		t.Fatalf("latest = %v", got)
	}
	authOne := "auth-1"
	authTwo := "auth-2"
	meta := mergeSummaryMeta(
		ontologycontract.SummaryMeta{AsOf: first, DataRevision: "data-1", AuthorizationRevision: &authOne},
		ontologycontract.SummaryMeta{AsOf: second, DataRevision: "data-2", AuthorizationRevision: &authTwo},
	)
	if !meta.AsOf.Equal(second) || meta.DataRevision == "" || meta.DataRevision == "data-1" || meta.AuthorizationRevision == nil || *meta.AuthorizationRevision == "" {
		t.Fatalf("meta = %+v", meta)
	}
	metaAgain := mergeSummaryMeta(
		ontologycontract.SummaryMeta{AsOf: first, DataRevision: "data-1", AuthorizationRevision: &authOne},
		ontologycontract.SummaryMeta{AsOf: second, DataRevision: "data-2", AuthorizationRevision: &authTwo},
	)
	if meta.DataRevision != metaAgain.DataRevision || *meta.AuthorizationRevision != *metaAgain.AuthorizationRevision {
		t.Fatalf("meta hashes are unstable: %+v / %+v", meta, metaAgain)
	}
	withoutAuth := mergeSummaryMeta(ontologycontract.SummaryMeta{AsOf: first, DataRevision: "data", AuthorizationRevision: nil})
	if withoutAuth.AuthorizationRevision != nil {
		t.Fatalf("authorization revision = %v", withoutAuth.AuthorizationRevision)
	}
}

func TestQualifyIDKeepsEqualLocalIDsDistinctAcrossTenants(t *testing.T) {
	first := qualifyID("org:a", "object:b")
	second := qualifyID("org", "a:object:b")
	third := qualifyID("org:b", "object:b")
	if first == second || first == third || first == "" || second == "" || third == "" {
		t.Fatalf("qualified ids = %q %q %q", first, second, third)
	}
}
