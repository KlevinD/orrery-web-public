package authorization

import (
	"strings"
	"time"

	"orrery_v2/internal/app/rebac"
)

const maxReBACRelationDepth = 16

type rebacRelationEvaluator struct {
	tuples []rebac.RelationTuple
	at     time.Time
}

func newReBACRelationEvaluator(tuples []rebac.RelationTuple, at time.Time) rebacRelationEvaluator {
	if at.IsZero() {
		at = time.Now().UTC()
	} else {
		at = at.UTC()
	}
	return rebacRelationEvaluator{tuples: tuples, at: at}
}

func (e rebacRelationEvaluator) check(tenantID string, subject rebac.Ref, relation string, object rebac.Ref) bool {
	return e.checkDepth(strings.TrimSpace(tenantID), normalizeReBACRef(subject), normalizeRelation(relation), normalizeReBACRef(object), 0, map[string]struct{}{})
}

func (e rebacRelationEvaluator) checkDepth(tenantID string, subject rebac.Ref, relation string, object rebac.Ref, depth int, visiting map[string]struct{}) bool {
	if depth > maxReBACRelationDepth || tenantID == "" || subject.Kind == "" || subject.ID == "" || relation == "" || object.Kind == "" || object.ID == "" {
		return false
	}
	visitKey := tenantID + "|" + subject.Kind + ":" + subject.ID + "|" + relation + "|" + object.Kind + ":" + object.ID
	if _, exists := visiting[visitKey]; exists {
		return false
	}
	visiting[visitKey] = struct{}{}
	defer delete(visiting, visitKey)

	if e.direct(tenantID, subject, relation, object) {
		return true
	}
	any := func(relations ...string) bool {
		for _, candidate := range relations {
			if e.checkDepth(tenantID, subject, candidate, object, depth+1, visiting) {
				return true
			}
		}
		return false
	}
	from := func(targetRelation, linkRelation string) bool {
		for _, target := range e.relatedObjects(tenantID, object, linkRelation) {
			if e.checkDepth(tenantID, subject, targetRelation, target, depth+1, visiting) {
				return true
			}
		}
		return false
	}

	switch object.Kind {
	case "platform":
		return relation == "member" && from("assignee", "assigned_role")
	case "organization":
		return relation == "viewer" && any("admin", "member")
	case "department":
		switch relation {
		case "admin":
			return from("admin", "org")
		case "viewer":
			return any("member", "manager", "admin")
		case "editor":
			return any("manager", "admin")
		}
	case "role":
		switch relation {
		case "admin":
			return from("admin", "org")
		case "viewer":
			return any("assignee", "admin")
		}
	case "mission":
		switch relation {
		case "viewer":
			return any("owner", "participant", "automation") || from("reader", "origin_todo") || from("monitor", "origin_development_project") || from("manager", "department") || from("admin", "department")
		case "editor":
			return any("owner", "automation") || from("assignee", "origin_todo") || from("manager", "department") || from("admin", "department")
		case "manager":
			return any("owner") || from("admin", "department")
		case "qa_approver":
			return any("owner") || from("reviewer", "origin_todo") || from("manager", "department") || from("admin", "department")
		}
	case "discussion":
		switch relation {
		case "viewer":
			return any("owner", "member")
		case "poster":
			return any("owner", "member")
		case "manager":
			return any("owner")
		}
	case "project":
		switch relation {
		case "viewer":
			return any("owner", "participant") || from("member", "org") || from("admin", "org")
		case "editor":
			return any("owner") || from("admin", "org")
		}
	case "development_project":
		switch relation {
		case "reader":
			return any("viewer", "contributor", "maintainer", "owner") || from("admin", "org")
		case "monitor":
			return any("maintainer", "owner") || from("admin", "org")
		case "writer":
			return any("contributor", "maintainer", "owner") || from("admin", "org")
		case "configurer":
			return any("maintainer", "owner") || from("admin", "org")
		case "runner", "submitter":
			return any("contributor", "maintainer", "owner", "executor") || from("admin", "org")
		case "approver":
			return any("maintainer", "owner") || from("admin", "org")
		case "manager":
			return any("owner") || from("admin", "org")
		case "merge_authorizer":
			return any("merger", "owner") || from("admin", "org")
		}
	case "scm_connection":
		switch relation {
		case "user", "manager":
			return from("admin", "org")
		}
	case "todo":
		switch relation {
		case "reader":
			return any("proposer", "assignee", "reviewer", "collaborator")
		case "editor":
			return any("proposer", "assignee", "reviewer")
		}
	case "resource":
		switch relation {
		case "viewer":
			return any("owner") || from("viewer", "bound_mission")
		case "operator":
			return any("owner") || from("editor", "bound_mission")
		}
	}
	return false
}

func (e rebacRelationEvaluator) direct(tenantID string, subject rebac.Ref, relation string, object rebac.Ref) bool {
	for _, tuple := range e.tuples {
		if strings.TrimSpace(tuple.TenantID) != tenantID || normalizeReBACRef(tuple.Subject) != subject || normalizeRelation(tuple.Relation) != relation || normalizeReBACRef(tuple.Object) != object {
			continue
		}
		if tuple.ValidFrom != nil && e.at.Before(tuple.ValidFrom.UTC()) {
			continue
		}
		if tuple.ValidTo != nil && !e.at.Before(tuple.ValidTo.UTC()) {
			continue
		}
		return true
	}
	return false
}

func (e rebacRelationEvaluator) relatedObjects(tenantID string, object rebac.Ref, relation string) []rebac.Ref {
	out := make([]rebac.Ref, 0, 1)
	seen := map[rebac.Ref]struct{}{}
	for _, tuple := range e.tuples {
		if strings.TrimSpace(tuple.TenantID) != tenantID || normalizeRelation(tuple.Relation) != relation || normalizeReBACRef(tuple.Object) != object {
			continue
		}
		if tuple.ValidFrom != nil && e.at.Before(tuple.ValidFrom.UTC()) {
			continue
		}
		if tuple.ValidTo != nil && !e.at.Before(tuple.ValidTo.UTC()) {
			continue
		}
		target := normalizeReBACRef(tuple.Subject)
		if target.Kind == "" || target.ID == "" {
			continue
		}
		if _, exists := seen[target]; exists {
			continue
		}
		seen[target] = struct{}{}
		out = append(out, target)
	}
	return out
}

func normalizeReBACRef(ref rebac.Ref) rebac.Ref {
	ref.Kind = normalizeKind(ref.Kind)
	ref.ID = strings.TrimSpace(ref.ID)
	return ref
}
