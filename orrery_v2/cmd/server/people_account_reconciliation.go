package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/peopleaccount"
	"orrery_v2/internal/app/userauth"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/db"
)

type peopleAccountReconciliationProfiles interface {
	QueryDirectory(ctx context.Context, query agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error)
	GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error)
}

type peopleAccountReconciliationStates interface {
	ProjectionState(ctx context.Context, personID string) (peopleaccount.ProjectionState, error)
	EnsureProfileProjection(ctx context.Context, profile agentcore.AgentProfile) (bool, error)
}

type peopleAccountReconciliationKratos interface {
	GetIdentity(ctx context.Context, identityID string) (peopleaccount.KratosIdentity, bool, error)
	FindIdentityByUsername(ctx context.Context, username string) (peopleaccount.KratosIdentity, bool, error)
}

type peopleAccountReconciliationSubjects interface {
	ListSubjects(ctx context.Context, query identity.SubjectQuery) (identity.SubjectPage, error)
}

type peopleAccountReconciliationProjector interface {
	ProjectLatest(ctx context.Context, personID string) (peopleaccount.ProjectionResult, error)
}

type peopleAccountReconciler struct {
	profiles  peopleAccountReconciliationProfiles
	states    peopleAccountReconciliationStates
	kratos    peopleAccountReconciliationKratos
	subjects  peopleAccountReconciliationSubjects
	projector peopleAccountReconciliationProjector
}

type peopleAccountReconciliationReport struct {
	Mode               string                                         `json:"mode"`
	Profiles           int                                            `json:"profiles"`
	Skipped            int                                            `json:"skipped"`
	ProjectionStates   int                                            `json:"projection_states"`
	KratosIdentities   int                                            `json:"kratos_identities"`
	IdentityBindings   int                                            `json:"identity_bindings"`
	Ready              int                                            `json:"ready"`
	Applied            int                                            `json:"applied"`
	Actions            []peopleAccountReconciliationAction            `json:"actions,omitempty"`
	Issues             []peopleAccountReconciliationIssue             `json:"issues,omitempty"`
	InitialCredentials []peopleAccountReconciliationInitialCredential `json:"initial_credentials,omitempty"`
}

type peopleAccountReconciliationAction struct {
	PersonID string `json:"person_id"`
	Code     string `json:"code"`
}

type peopleAccountReconciliationIssue struct {
	PersonID string `json:"person_id"`
	Code     string `json:"code"`
	Detail   string `json:"detail"`
}

type peopleAccountReconciliationInitialCredential struct {
	PersonID string `json:"person_id"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type peopleAccountReconciliationCandidate struct {
	profile    agentcore.AgentProfile
	personID   string
	username   string
	stateFound bool
}

type peopleAccountKratosLookup struct {
	identities           []peopleaccount.KratosIdentity
	duplicateUsername    bool
	projectionIDConflict bool
}

func (r *peopleAccountReconciler) Reconcile(ctx context.Context, apply bool) (peopleAccountReconciliationReport, error) {
	report := peopleAccountReconciliationReport{Mode: "dry_run"}
	if apply {
		report.Mode = "apply"
	}
	if r == nil || r.profiles == nil || r.states == nil || r.kratos == nil || r.subjects == nil || r.projector == nil {
		return report, fmt.Errorf("People account reconciliation is not configured")
	}
	bindings, err := listPeopleAccountIdentityBindings(ctx, r.subjects)
	if err != nil {
		return report, err
	}
	report.IdentityBindings = len(bindings)
	bindingsByProfile, bindingsBySubject := indexPeopleAccountIdentityBindings(bindings)
	page, err := r.profiles.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{
		AgentKind: agentcore.AgentKindHuman, Placement: agentcore.TalentPlacementFilterAll, IncludeHumans: true,
	})
	if err != nil {
		return report, err
	}
	identityIDs := map[string]struct{}{}
	candidates := make([]peopleAccountReconciliationCandidate, 0, len(page.Entries))
	for _, entry := range page.Entries {
		profile, err := r.profiles.GetProfile(ctx, entry.AgentProfileID)
		if err != nil {
			return report, err
		}
		personID := strings.TrimSpace(profile.ID.String())
		if userauth.IsReservedWorkforceProfileID(personID) {
			report.Skipped++
			continue
		}
		report.Profiles++
		issueStart := len(report.Issues)
		username := strings.TrimSpace(profile.UserAccount.Username)
		if username == "" {
			report.addIssue(personID, "missing_username", "People profile has no login username")
		}
		state, stateFound, err := r.projectionState(ctx, personID)
		if err != nil {
			return report, err
		}
		if stateFound {
			report.ProjectionStates++
		}
		lookup, err := r.lookupKratosIdentity(ctx, state, stateFound, username)
		if err != nil {
			return report, err
		}
		if lookup.duplicateUsername {
			report.addIssue(personID, "duplicate_kratos_username", "multiple Kratos identities use the People username")
		}
		for _, kratosIdentity := range lookup.identities {
			identityIDs[kratosIdentity.ID] = struct{}{}
			existingPersonID := strings.TrimSpace(kratosIdentity.PersonID)
			if existingPersonID != "" && existingPersonID != personID {
				report.addIssue(personID, "kratos_person_conflict", "Kratos identity belongs to a different person_id")
			} else if existingPersonID == "" && !hasActiveIdentityBinding(personID, kratosIdentity.ID, bindingsByProfile) {
				report.addIssue(personID, "unbound_kratos_identity", "Kratos identity has no person_id or active Orrery identity binding")
			}
		}
		if lookup.projectionIDConflict {
			report.addIssue(personID, "projection_identity_conflict", "projection points to a different Kratos identity")
		}
		for _, kratosIdentity := range lookup.identities {
			report.checkIdentityBindings(personID, kratosIdentity.ID, bindingsByProfile, bindingsBySubject)
		}
		if len(lookup.identities) == 0 && len(bindingsByProfile[personID]) > 0 {
			report.addIssue(personID, "orphan_identity_binding", "Orrery binding points to a missing Kratos identity")
		}
		if len(report.Issues) != issueStart {
			continue
		}
		report.Ready++
		if !stateFound {
			report.Actions = append(report.Actions, peopleAccountReconciliationAction{PersonID: personID, Code: "seed_projection"})
		}
		if !apply {
			report.Actions = append(report.Actions, peopleAccountReconciliationAction{PersonID: personID, Code: "project_account"})
			continue
		}
		candidates = append(candidates, peopleAccountReconciliationCandidate{
			profile: profile, personID: personID, username: username, stateFound: stateFound,
		})
	}
	report.KratosIdentities = len(identityIDs)
	for _, candidate := range candidates {
		if !candidate.stateFound {
			if _, err := r.states.EnsureProfileProjection(ctx, candidate.profile); err != nil {
				report.addIssue(candidate.personID, "projection_seed_failed", "People account projection could not be seeded")
				continue
			}
		}
		result, err := r.projector.ProjectLatest(ctx, candidate.personID)
		if err != nil {
			report.addIssue(candidate.personID, "projection_failed", "People account projection could not be applied")
			continue
		}
		report.Applied++
		if result.InitialPassword != "" {
			report.InitialCredentials = append(report.InitialCredentials, peopleAccountReconciliationInitialCredential{
				PersonID: candidate.personID, Username: candidate.username, Password: result.InitialPassword,
			})
		}
	}
	return report, nil
}

func (r *peopleAccountReconciler) projectionState(ctx context.Context, personID string) (peopleaccount.ProjectionState, bool, error) {
	state, err := r.states.ProjectionState(ctx, personID)
	if errors.Is(err, peopleaccount.ErrProjectionNotFound) {
		return peopleaccount.ProjectionState{}, false, nil
	}
	return state, err == nil, err
}

func (r *peopleAccountReconciler) lookupKratosIdentity(ctx context.Context, state peopleaccount.ProjectionState, stateFound bool, username string) (peopleAccountKratosLookup, error) {
	var lookup peopleAccountKratosLookup
	if stateFound && strings.TrimSpace(state.KratosIdentityID) != "" {
		foundIdentity, found, err := r.kratos.GetIdentity(ctx, state.KratosIdentityID)
		if err != nil {
			return lookup, err
		}
		if found {
			lookup.identities = append(lookup.identities, foundIdentity)
		}
	}
	if username == "" {
		return lookup, nil
	}
	foundIdentity, found, err := r.kratos.FindIdentityByUsername(ctx, username)
	if errors.Is(err, peopleaccount.ErrProjectionConflict) {
		lookup.duplicateUsername = true
		return lookup, nil
	}
	if err != nil {
		return lookup, err
	}
	if !found {
		return lookup, nil
	}
	for _, existing := range lookup.identities {
		if existing.ID == foundIdentity.ID {
			return lookup, nil
		}
	}
	if stateFound && strings.TrimSpace(state.KratosIdentityID) != "" && state.KratosIdentityID != foundIdentity.ID {
		lookup.projectionIDConflict = true
	}
	lookup.identities = append(lookup.identities, foundIdentity)
	return lookup, nil
}

func (r *peopleAccountReconciliationReport) checkIdentityBindings(personID, identityID string, byProfile, bySubject map[string][]identity.IdentitySubject) {
	if len(byProfile[personID]) > 1 {
		r.addIssue(personID, "duplicate_identity_binding", "People profile has multiple Kratos login bindings")
	}
	for _, binding := range byProfile[personID] {
		if binding.Subject != identityID {
			r.addIssue(personID, "identity_binding_conflict", "People profile is bound to a different Kratos identity")
			break
		}
	}
	for _, binding := range bySubject[identityID] {
		if binding.AgentProfileID != personID {
			r.addIssue(personID, "identity_binding_conflict", "Kratos identity is bound to a different People profile")
			break
		}
	}
}

func (r *peopleAccountReconciliationReport) addIssue(personID, code, detail string) {
	for _, issue := range r.Issues {
		if issue.PersonID == personID && issue.Code == code {
			return
		}
	}
	r.Issues = append(r.Issues, peopleAccountReconciliationIssue{PersonID: personID, Code: code, Detail: detail})
}

func listPeopleAccountIdentityBindings(ctx context.Context, subjects peopleAccountReconciliationSubjects) ([]identity.IdentitySubject, error) {
	const limit = 500
	var bindings []identity.IdentitySubject
	for offset := 0; ; {
		page, err := subjects.ListSubjects(ctx, identity.SubjectQuery{Provider: identity.ProviderKratos, Limit: limit, Offset: offset})
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, page.Subjects...)
		if !page.HasMore {
			return bindings, nil
		}
		if page.Returned == 0 {
			return nil, fmt.Errorf("Kratos identity binding pagination did not advance")
		}
		offset += page.Returned
	}
}

func indexPeopleAccountIdentityBindings(bindings []identity.IdentitySubject) (map[string][]identity.IdentitySubject, map[string][]identity.IdentitySubject) {
	byProfile := map[string][]identity.IdentitySubject{}
	bySubject := map[string][]identity.IdentitySubject{}
	for _, binding := range bindings {
		personID := strings.TrimSpace(binding.AgentProfileID)
		subject := strings.TrimSpace(binding.Subject)
		if personID != "" {
			byProfile[personID] = append(byProfile[personID], binding)
		}
		if subject != "" {
			bySubject[subject] = append(bySubject[subject], binding)
		}
	}
	return byProfile, bySubject
}

func hasActiveIdentityBinding(personID, identityID string, byProfile map[string][]identity.IdentitySubject) bool {
	for _, binding := range byProfile[personID] {
		if binding.Subject == identityID && binding.Status == identity.SubjectStatusActive {
			return true
		}
	}
	return false
}

func runProductionPeopleAccountReconciliation(ctx context.Context, cfg config.Config, apply bool, out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	auth, err := identityAuthConfigFromPlatform(cfg)
	if err != nil {
		return err
	}
	if auth.IdentityMode != identityModePeopleSSO || auth.KratosAdmin == nil {
		return fmt.Errorf("People account reconciliation requires people_sso mode and Kratos Admin")
	}
	runtime, err := platform.OpenWithModules(ctx, cfg, productionModules()...)
	if err != nil {
		return err
	}
	defer runtime.Close()
	if err := runtime.Initialize(ctx); err != nil {
		return err
	}
	core, err := runtime.Databases.Store(db.RoleCore)
	if err != nil {
		return err
	}
	profiles := agentcore.NewPlatformProfileRepository(runtime.UnitOfWork)
	states := peopleaccount.NewStore(core)
	projector := peopleaccount.NewProjector(states, auth.KratosAdmin).
		WithLegacyCredentialSource(productionPeopleLegacyCredentialSource{profiles: profiles}).
		WithLegacyIdentityAdoption()
	report, err := (&peopleAccountReconciler{
		profiles: profiles, states: states, kratos: auth.KratosAdmin,
		subjects: identity.NewPlatformSubjectStore(core), projector: projector,
	}).Reconcile(ctx, apply)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(report)
}
