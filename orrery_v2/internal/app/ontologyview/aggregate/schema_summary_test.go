package aggregate

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	contract "orrery_v2/internal/app/ontologyview/contract"
	schema "orrery_v2/internal/app/ontologyview/schema"
)

type boundedSummaryReader struct {
	fakeSchemaReader
	calls       []definitionSource
	unavailable bool
	err         error
}

func (r *boundedSummaryReader) ListDefinitions(context.Context, identity.Principal, schema.DefinitionListQuery) (contract.CursorPage[schema.DefinitionItem], error) {
	panic("summary must not enumerate detail pages")
}

func (r *boundedSummaryReader) SummaryOrigin(_ context.Context, _ identity.Principal, tenant string, origin schema.DefinitionOrigin) (contract.Summary[schema.SummaryData], error) {
	r.calls = append(r.calls, definitionSource{tenant, origin})
	if r.err != nil {
		return contract.Summary[schema.SummaryData]{}, r.err
	}
	objects := contract.ExactCount(1)
	if r.unavailable && origin == schema.OriginTenant {
		objects = contract.UnavailableCount()
	}
	return contract.Summary[schema.SummaryData]{Data: schema.SummaryData{ObjectTypes: objects, LinkTypes: contract.ExactCount(0), PropertyTypes: contract.ExactCount(0), MappingRules: contract.ExactCount(0), ActionTypes: contract.ExactCount(0)}}, nil
}

func TestSchemaSummaryUsesBoundedOriginsWithoutDoubleCountingGlobals(t *testing.T) {
	catalog := &fakeScopeCatalog{pages: map[string]contract.CursorPage[ontologyscope.ScopeCatalogItem]{"": scopePage([]string{"org-1", "org-2", "system"}, false, "")}}
	r := &boundedSummaryReader{}
	s, err := NewSchemaService(newTestScopeEnumerator(t, catalog), r)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Summary(context.Background(), testPrincipal("user-1"), "")
	if err != nil || result.Data.ObjectTypes.Value == nil || *result.Data.ObjectTypes.Value != 3 || len(r.calls) != 3 {
		t.Fatalf("result=%+v calls=%+v err=%v", result, r.calls, err)
	}
	if r.calls[0].origin != schema.OriginSystem || r.calls[1].origin != schema.OriginTenant || r.calls[2].origin != schema.OriginTenant {
		t.Fatalf("global definitions repeated: %+v", r.calls)
	}
	r.unavailable = true
	result, err = s.Summary(context.Background(), testPrincipal("user-1"), "")
	if err != nil || result.Data.ObjectTypes.Status != contract.MetricUnavailable || result.Data.ObjectTypes.Value != nil {
		t.Fatalf("partial count presented as exact: %+v %v", result, err)
	}
	r.err = errors.New("scope denied")
	_, err = s.Summary(context.Background(), testPrincipal("user-1"), "")
	if !errors.Is(err, r.err) {
		t.Fatalf("authorization failure hidden: %v", err)
	}
}
