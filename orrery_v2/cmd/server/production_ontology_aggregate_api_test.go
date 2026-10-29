package main

import "testing"

func TestProductionOntologyAggregateAPIsFailClosedWithoutDependencies(t *testing.T) {
	if _, err := newProductionOntologyAggregateAPIs(nil, nil, nil, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("expected missing dependency error")
	}
}
