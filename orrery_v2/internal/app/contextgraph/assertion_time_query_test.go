package contextgraph

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestPolicyQueriesPreserveAssertionTime(t *testing.T) {
	f := newPlatformQueryFixture(t)
	ctx := context.Background()
	start, end, recorded := queryTestTime(100), queryTestTime(200), queryTestTime(110)
	temporal := TemporalMetadata{Mode: "state_interval", BusinessValidFrom: start, BusinessValidTo: &end, SourceRecordedAt: recorded, OrreryIngestedAt: recorded}
	o, err := f.graph.GetVisibleObject(ctx, f.tenant, "root", f.visibility())
	if err != nil {
		t.Fatal(err)
	}
	o.Temporal, o.UpdatedAt = temporal, recorded
	if err := f.graph.SaveObject(ctx, o); err != nil {
		t.Fatal(err)
	}
	if err := f.graph.SaveLink(ctx, Link{ID: "link-root-child", TenantID: f.tenant, LinkType: "has_task", FromObjectID: "root", ToObjectID: "child", SourceAccessRef: f.allowed, Temporal: temporal, UpdatedAt: recorded}); err != nil {
		t.Fatal(err)
	}
	if err := f.graph.SaveFact(ctx, Fact{ID: "fact-visible", TenantID: f.tenant, Subject: Ref{Kind: "mission", ID: "root"}, Predicate: "status", Value: "active", Provenance: Ref{Kind: "test_fixture", ID: "fact-visible"}, SourceAccessRef: f.allowed, Temporal: temporal, UpdatedAt: recorded}); err != nil {
		t.Fatal(err)
	}
	check := func(name string, got TemporalMetadata) {
		t.Helper()
		if !reflect.DeepEqual(got, temporal) {
			t.Fatalf("%s lost assertion time: %+v", name, got)
		}
	}
	o, err = f.graph.GetVisibleObject(ctx, f.tenant, "root", f.visibility())
	if err != nil {
		t.Fatal(err)
	}
	check("object", o.Temporal)
	relations, err := f.graph.ListVisibleRelations(ctx, RelationQuery{TenantID: f.tenant, Visibility: f.visibility(), ObjectID: "root", Direction: RelationBoth, Limit: 10})
	if err != nil || len(relations.Items) != 1 {
		t.Fatalf("relations: %v", err)
	}
	check("relation", relations.Items[0].Link.Temporal)
	facts, err := f.graph.ListVisibleFacts(ctx, FactQuery{TenantID: f.tenant, Visibility: f.visibility(), ObjectType: "mission", ObjectID: "root", Limit: 10})
	if err != nil || len(facts.Items) != 1 {
		t.Fatalf("facts: %v", err)
	}
	check("facts", facts.Items[0].Temporal)
	detail, err := f.graph.GetVisibleFact(ctx, f.tenant, "fact-visible", f.visibility())
	if err != nil {
		t.Fatal(err)
	}
	check("fact detail", detail.Fact.Temporal)
	check("subject", detail.Subject.Temporal)
	lineage, err := f.graph.ListVisibleLineageFacts(ctx, VisibleFactQuery{TenantID: f.tenant, Visibility: f.visibility(), Limit: 10})
	if err != nil || len(lineage.Items) != 1 {
		t.Fatalf("lineage: %v", err)
	}
	check("lineage", lineage.Items[0].Temporal)
	assertions, err := f.graph.ListVisibleFactAssertions(ctx, FactAssertionQuery{TenantID: f.tenant, FactID: "fact-visible", Visibility: f.visibility(), Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range assertions.Items {
		if a.ObservedAt.Equal(recorded) {
			check("assertion", a.Fact.Temporal)
			found = true
		}
	}
	if !found {
		t.Fatal("new assertion missing")
	}
}

func TestConflictChoiceOnlyDisputesOverlappingBusinessWindow(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	f := Fact{Status: "disputed", Provenance: Ref{Kind: "knowledge_instances"}, Properties: map[string]any{"adjudication": map[string]any{"business_valid_from": start, "business_valid_to": end}}}
	for _, tc := range []struct {
		at   time.Time
		want string
	}{{start.Add(-time.Second), "active"}, {start, "disputed"}, {end.Add(-time.Second), "disputed"}, {end, "active"}} {
		if got := AssertionStatusAt(f, tc.at); got != tc.want {
			t.Fatalf("at %v: %s != %s", tc.at, got, tc.want)
		}
	}
}
