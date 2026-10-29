package httptransport

import (
	"net/http"

	authorizationapp "orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/operations"
)

type AuthorizationRelationBackfillDiagnosticsDTO struct {
	Mode             string                                 `json:"mode,omitempty"`
	Disabled         bool                                   `json:"disabled"`
	Projects         int                                    `json:"projects"`
	Missions         int                                    `json:"missions"`
	Discussions      int                                    `json:"discussions"`
	Organizations    int                                    `json:"organizations"`
	Departments      int                                    `json:"departments"`
	WorkforceMembers int                                    `json:"workforce_members"`
	RoleAssignments  int                                    `json:"role_assignments"`
	ResourceBindings int                                    `json:"resource_bindings"`
	Tuples           int                                    `json:"tuples"`
	DryRun           bool                                   `json:"dry_run"`
	Report           AuthorizationRelationBackfillReportDTO `json:"report"`
	Diff             AuthorizationRelationBackfillDiffDTO   `json:"diff"`
}

type AuthorizationRelationBackfillReportDTO struct {
	Projects         int            `json:"projects"`
	Missions         int            `json:"missions"`
	Discussions      int            `json:"discussions"`
	Organizations    int            `json:"organizations"`
	Departments      int            `json:"departments"`
	WorkforceMembers int            `json:"workforce_members"`
	RoleAssignments  int            `json:"role_assignments"`
	ResourceBindings int            `json:"resource_bindings"`
	Tuples           int            `json:"tuples"`
	ByObjectType     map[string]int `json:"by_object_type"`
	ByRelation       map[string]int `json:"by_relation"`
	BySubjectKind    map[string]int `json:"by_subject_kind"`
	ByTenant         map[string]int `json:"by_tenant"`
}

type AuthorizationRelationBackfillDiffDTO struct {
	Checked       int                             `json:"checked"`
	Missing       int                             `json:"missing"`
	Errors        int                             `json:"errors"`
	MissingTuples []AuthorizationRelationTupleDTO `json:"missing_tuples"`
	ErrorMessages []string                        `json:"error_messages"`
}

type AuthorizationEnforceTransitionDiagnosticsDTO struct {
	Ready             bool                                        `json:"ready"`
	BackfillValidated bool                                        `json:"backfill_validated"`
	Shadow            AuthorizationShadowDiagnosticsDTO           `json:"shadow"`
	Backfill          AuthorizationRelationBackfillDiagnosticsDTO `json:"backfill"`
}

type AuthorizationShadowDiagnosticsDTO struct {
	Total                  int                                      `json:"total"`
	Mismatches             int                                      `json:"mismatches"`
	PrimaryErrors          int                                      `json:"primary_errors"`
	ShadowErrors           int                                      `json:"shadow_errors"`
	PrimaryAllowShadowDeny int                                      `json:"primary_allow_shadow_deny"`
	PrimaryDenyShadowAllow int                                      `json:"primary_deny_shadow_allow"`
	ByObjectType           map[string]AuthorizationShadowCounterDTO `json:"by_object_type"`
	ByAction               map[string]AuthorizationShadowCounterDTO `json:"by_action"`
}

type AuthorizationShadowCounterDTO struct {
	Total                  int `json:"total"`
	Mismatches             int `json:"mismatches"`
	PrimaryErrors          int `json:"primary_errors"`
	ShadowErrors           int `json:"shadow_errors"`
	PrimaryAllowShadowDeny int `json:"primary_allow_shadow_deny"`
	PrimaryDenyShadowAllow int `json:"primary_deny_shadow_allow"`
}

type AuthorizationRelationTupleDTO struct {
	ID       string                 `json:"id,omitempty"`
	TenantID string                 `json:"tenant_id"`
	Subject  AuthorizationRefDTO    `json:"subject"`
	Relation string                 `json:"relation"`
	Object   AuthorizationObjectDTO `json:"object"`
}

type AuthorizationRefDTO struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type AuthorizationObjectDTO struct {
	Type     string `json:"type"`
	ID       string `json:"id"`
	TenantID string `json:"tenant_id,omitempty"`
}

func (r *Router) handleAuthorizationRelationBackfillDiagnostics(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	if !r.authorizeAuthorizationDiagnostics(w, req) {
		return
	}
	if r == nil || r.authorizationDiagnostics == nil {
		status, body := MapError(operations.ErrServiceDependency, cid)
		writeJSON(w, status, body)
		return
	}
	result, err := r.authorizationDiagnostics.GetAuthorizationRelationBackfillDiagnostics(req.Context())
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, authorizationRelationBackfillDiagnostics(result))
}

func (r *Router) handleAuthorizationEnforceTransitionDiagnostics(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	if !r.authorizeAuthorizationDiagnostics(w, req) {
		return
	}
	if r == nil || r.authorizationDiagnostics == nil {
		status, body := MapError(operations.ErrServiceDependency, cid)
		writeJSON(w, status, body)
		return
	}
	result, err := r.authorizationDiagnostics.GetAuthorizationEnforceTransitionDiagnostics(req.Context())
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, authorizationEnforceTransitionDiagnostics(result))
}

func authorizationRelationBackfillDiagnostics(result authorizationapp.RelationBackfillResult) AuthorizationRelationBackfillDiagnosticsDTO {
	return AuthorizationRelationBackfillDiagnosticsDTO{
		Mode:             result.Mode,
		Disabled:         result.Disabled,
		Projects:         result.Projects,
		Missions:         result.Missions,
		Discussions:      result.Discussions,
		Organizations:    result.Organizations,
		Departments:      result.Departments,
		WorkforceMembers: result.WorkforceMembers,
		RoleAssignments:  result.RoleAssignments,
		ResourceBindings: result.ResourceBindings,
		Tuples:           result.Tuples,
		DryRun:           result.DryRun,
		Report:           authorizationRelationBackfillReport(result.Report),
		Diff:             authorizationRelationBackfillDiff(result.Diff),
	}
}

func authorizationEnforceTransitionDiagnostics(result authorizationapp.EnforceTransitionDiagnostics) AuthorizationEnforceTransitionDiagnosticsDTO {
	return AuthorizationEnforceTransitionDiagnosticsDTO{
		Ready:             result.Ready,
		BackfillValidated: result.BackfillValidated,
		Shadow:            authorizationShadowDiagnostics(result.Shadow),
		Backfill:          authorizationRelationBackfillDiagnostics(result.Backfill),
	}
}

func (r *Router) authorizeAuthorizationDiagnostics(w http.ResponseWriter, req *http.Request) bool {
	cid := correlationID(req)
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return false
	}
	if !viewerHasScope(viewer.PermissionScope, operations.PermissionPermissionsRead, operations.PermissionPermissionsManage) {
		status, body := MapError(operations.ErrPermissionDenied, cid)
		writeJSON(w, status, body)
		return false
	}
	return true
}

func authorizationShadowDiagnostics(stats authorizationapp.ShadowDecisionStats) AuthorizationShadowDiagnosticsDTO {
	return AuthorizationShadowDiagnosticsDTO{
		Total:                  stats.Total,
		Mismatches:             stats.Mismatches,
		PrimaryErrors:          stats.PrimaryErrors,
		ShadowErrors:           stats.ShadowErrors,
		PrimaryAllowShadowDeny: stats.PrimaryAllowShadowDeny,
		PrimaryDenyShadowAllow: stats.PrimaryDenyShadowAllow,
		ByObjectType:           authorizationShadowCounterMap(stats.ByObjectType),
		ByAction:               authorizationShadowCounterMap(stats.ByAction),
	}
}

func authorizationShadowCounterMap(in map[string]authorizationapp.ShadowDecisionCounter) map[string]AuthorizationShadowCounterDTO {
	if len(in) == 0 {
		return map[string]AuthorizationShadowCounterDTO{}
	}
	out := make(map[string]AuthorizationShadowCounterDTO, len(in))
	for key, value := range in {
		out[key] = authorizationShadowCounter(value)
	}
	return out
}

func authorizationShadowCounter(counter authorizationapp.ShadowDecisionCounter) AuthorizationShadowCounterDTO {
	return AuthorizationShadowCounterDTO{
		Total:                  counter.Total,
		Mismatches:             counter.Mismatches,
		PrimaryErrors:          counter.PrimaryErrors,
		ShadowErrors:           counter.ShadowErrors,
		PrimaryAllowShadowDeny: counter.PrimaryAllowShadowDeny,
		PrimaryDenyShadowAllow: counter.PrimaryDenyShadowAllow,
	}
}

func authorizationRelationBackfillReport(report authorizationapp.RelationBackfillReport) AuthorizationRelationBackfillReportDTO {
	return AuthorizationRelationBackfillReportDTO{
		Projects:         report.Projects,
		Missions:         report.Missions,
		Discussions:      report.Discussions,
		Organizations:    report.Organizations,
		Departments:      report.Departments,
		WorkforceMembers: report.WorkforceMembers,
		RoleAssignments:  report.RoleAssignments,
		ResourceBindings: report.ResourceBindings,
		Tuples:           report.Tuples,
		ByObjectType:     copyStringIntMap(report.ByObjectType),
		ByRelation:       copyStringIntMap(report.ByRelation),
		BySubjectKind:    copyStringIntMap(report.BySubjectKind),
		ByTenant:         copyStringIntMap(report.ByTenant),
	}
}

func authorizationRelationBackfillDiff(diff authorizationapp.RelationBackfillDiff) AuthorizationRelationBackfillDiffDTO {
	missing := make([]AuthorizationRelationTupleDTO, 0, len(diff.MissingTuples))
	for _, tuple := range diff.MissingTuples {
		missing = append(missing, authorizationRelationTuple(tuple))
	}
	return AuthorizationRelationBackfillDiffDTO{
		Checked:       diff.Checked,
		Missing:       diff.Missing,
		Errors:        diff.Errors,
		MissingTuples: missing,
		ErrorMessages: append([]string(nil), diff.ErrorMessages...),
	}
}

func authorizationRelationTuple(tuple authorizationapp.RelationTuple) AuthorizationRelationTupleDTO {
	return AuthorizationRelationTupleDTO{
		ID:       tuple.ID,
		TenantID: tuple.TenantID,
		Subject:  authorizationSubjectRef(tuple.Subject),
		Relation: tuple.Relation,
		Object:   authorizationObjectRef(tuple.Object),
	}
}

func authorizationSubjectRef(ref authorizationapp.SubjectRef) AuthorizationRefDTO {
	return AuthorizationRefDTO{Kind: ref.Kind, ID: ref.ID}
}

func authorizationObjectRef(ref identity.ObjectRef) AuthorizationObjectDTO {
	return AuthorizationObjectDTO{Type: ref.Type, ID: ref.ID, TenantID: ref.TenantID}
}

func copyStringIntMap(in map[string]int) map[string]int {
	if len(in) == 0 {
		return map[string]int{}
	}
	out := make(map[string]int, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
