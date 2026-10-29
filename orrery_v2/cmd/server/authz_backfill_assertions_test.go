package main

import (
	"testing"

	authorizationapp "orrery_v2/internal/app/authorization"
)

func authzRelationTupleContains(tuples []authorizationapp.RelationTuple, want authorizationapp.RelationTuple) bool {
	for _, tuple := range tuples {
		if tuple.TenantID == want.TenantID && tuple.Subject == want.Subject && tuple.Relation == want.Relation && tuple.Object == want.Object {
			return true
		}
	}
	return false
}

func assertAuthzTuple(t *testing.T, tuple authorizationapp.RelationTuple, subjectKind string, subjectID string, relation string, objectType string, objectID string) {
	t.Helper()
	if tuple.Subject.Kind != subjectKind || tuple.Subject.ID != subjectID || tuple.Relation != relation || tuple.Object.Type != objectType || tuple.Object.ID != objectID {
		t.Fatalf("tuple = %+v", tuple)
	}
}
