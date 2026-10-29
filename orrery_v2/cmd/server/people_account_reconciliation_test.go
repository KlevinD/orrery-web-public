package main

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/peopleaccount"
)

func TestPeopleAccountReconciliationCLIRejectsConflictingModes(t *testing.T) {
	err := run(context.Background(), []string{"-people-account-reconcile-dry-run", "-people-account-reconcile-apply"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("run error = %v", err)
	}
}

func TestPeopleAccountReconciliationDryRunInventoriesConflictsWithoutMutation(t *testing.T) {
	profiles := &fakePeopleAccountReconciliationProfiles{profiles: []agentcore.AgentProfile{
		reconciliationProfile("human-quzheng", "quzheng", "$2a$legacy-quzheng"),
		reconciliationProfile("human-lipi", "lipi", "$2a$legacy-lipi"),
		reconciliationProfile("human-gaochuang", "gaochuang", "$2a$legacy-gaochuang"),
		reconciliationProfile("human-superadmin", "superadmin", "$2a$break-glass"),
		reconciliationProfile("human-local-admin", "local-admin", "$2a$placeholder-admin"),
		reconciliationProfile("human-local-user", "local-user", "$2a$placeholder-user"),
		reconciliationProfile("system-superadmin", "superadmin", "$2a$system"),
	}}
	states := &fakePeopleAccountReconciliationStates{states: map[string]peopleaccount.ProjectionState{
		"human-quzheng": {
			Projection: peopleaccount.Projection{PersonID: "human-quzheng", Revision: 3, Username: "quzheng", Enabled: true},
			Status:     peopleaccount.ProjectionStatusApplied, AppliedRevision: 3, KratosIdentityID: "kratos-quzheng",
		},
		"human-gaochuang": {
			Projection: peopleaccount.Projection{PersonID: "human-gaochuang", Revision: 2, Username: "gaochuang", Enabled: true},
			Status:     peopleaccount.ProjectionStatusPending, KratosIdentityID: "kratos-old-gaochuang",
		},
	}}
	kratos := &fakePeopleAccountReconciliationKratos{
		identities: map[string]peopleaccount.KratosIdentity{
			"quzheng":   {ID: "kratos-quzheng", PersonID: "human-quzheng"},
			"gaochuang": {ID: "kratos-gaochuang", PersonID: "human-other"},
		},
		errors: map[string]error{"lipi": peopleaccount.ErrProjectionConflict},
	}
	subjects := &fakePeopleAccountReconciliationSubjects{subjects: []identity.IdentitySubject{
		{Provider: identity.ProviderKratos, Subject: "kratos-quzheng", AgentProfileID: "human-quzheng", Status: identity.SubjectStatusActive},
		{Provider: identity.ProviderKratos, Subject: "kratos-gaochuang", AgentProfileID: "human-other", Status: identity.SubjectStatusActive},
	}}
	projector := &fakePeopleAccountReconciliationProjector{}

	report, err := (&peopleAccountReconciler{
		profiles: profiles, states: states, kratos: kratos, subjects: subjects, projector: projector,
	}).Reconcile(context.Background(), false)
	if err != nil {
		t.Fatalf("Reconcile dry-run: %v", err)
	}
	if report.Mode != "dry_run" || report.Profiles != 3 || report.Skipped != 4 || report.ProjectionStates != 2 || report.KratosIdentities != 2 || report.IdentityBindings != 2 {
		t.Fatalf("inventory report = %+v", report)
	}
	if got := reconciliationIssueCodes(report); !reflect.DeepEqual(got, []string{"duplicate_kratos_username", "kratos_person_conflict", "projection_identity_conflict", "identity_binding_conflict"}) {
		t.Fatalf("issue codes = %v", got)
	}
	if !reflect.DeepEqual(report.Actions, []peopleAccountReconciliationAction{{PersonID: "human-quzheng", Code: "project_account"}}) {
		t.Fatalf("actions = %+v", report.Actions)
	}
	if len(states.ensureCalls) != 0 || len(projector.calls) != 0 {
		t.Fatalf("dry-run mutated state: ensure=%v project=%v", states.ensureCalls, projector.calls)
	}
}

func TestPeopleAccountReconciliationApplySeedsAndProjectsOnce(t *testing.T) {
	profile := reconciliationProfile("human-lipi", "lipi", "$2a$legacy-lipi")
	profiles := &fakePeopleAccountReconciliationProfiles{profiles: []agentcore.AgentProfile{profile}}
	states := &fakePeopleAccountReconciliationStates{states: map[string]peopleaccount.ProjectionState{}}
	projector := &fakePeopleAccountReconciliationProjector{results: map[string]peopleaccount.ProjectionResult{
		"human-lipi": {
			PersonID: "human-lipi", Revision: 1, Status: peopleaccount.ProjectionStatusApplied,
			KratosIdentityID: "kratos-lipi", InitialPassword: "initial-password-once",
		},
	}}
	reconciler := &peopleAccountReconciler{
		profiles:  profiles,
		states:    states,
		kratos:    &fakePeopleAccountReconciliationKratos{identities: map[string]peopleaccount.KratosIdentity{}},
		subjects:  &fakePeopleAccountReconciliationSubjects{},
		projector: projector,
	}

	first, err := reconciler.Reconcile(context.Background(), true)
	if err != nil {
		t.Fatalf("Reconcile apply: %v", err)
	}
	if !reflect.DeepEqual(states.ensureCalls, []string{"human-lipi"}) || !reflect.DeepEqual(projector.calls, []string{"human-lipi"}) {
		t.Fatalf("apply calls: ensure=%v project=%v", states.ensureCalls, projector.calls)
	}
	if len(states.legacyHashes) != 1 || states.legacyHashes[0] != "$2a$legacy-lipi" {
		t.Fatalf("legacy hashes passed to seeding = %v", states.legacyHashes)
	}
	if len(first.InitialCredentials) != 1 || first.InitialCredentials[0].PersonID != "human-lipi" || first.InitialCredentials[0].Password != "initial-password-once" {
		t.Fatalf("initial credentials = %+v", first.InitialCredentials)
	}

	projector.results["human-lipi"] = peopleaccount.ProjectionResult{
		PersonID: "human-lipi", Revision: 1, Status: peopleaccount.ProjectionStatusApplied, KratosIdentityID: "kratos-lipi",
	}
	second, err := reconciler.Reconcile(context.Background(), true)
	if err != nil {
		t.Fatalf("Reconcile second apply: %v", err)
	}
	if len(states.ensureCalls) != 1 {
		t.Fatalf("projection was seeded more than once: %v", states.ensureCalls)
	}
	if len(second.InitialCredentials) != 0 {
		t.Fatalf("initial credentials repeated: %+v", second.InitialCredentials)
	}
}

func TestPeopleAccountReconciliationDoesNotOverwriteExistingPersonID(t *testing.T) {
	profiles := &fakePeopleAccountReconciliationProfiles{profiles: []agentcore.AgentProfile{
		reconciliationProfile("human-quzheng", "quzheng", ""),
	}}
	states := &fakePeopleAccountReconciliationStates{states: map[string]peopleaccount.ProjectionState{}}
	projector := &fakePeopleAccountReconciliationProjector{}
	report, err := (&peopleAccountReconciler{
		profiles: profiles,
		states:   states,
		kratos: &fakePeopleAccountReconciliationKratos{identities: map[string]peopleaccount.KratosIdentity{
			"quzheng": {ID: "kratos-existing", PersonID: "human-someone-else"},
		}},
		subjects:  &fakePeopleAccountReconciliationSubjects{},
		projector: projector,
	}).Reconcile(context.Background(), true)
	if err != nil {
		t.Fatalf("Reconcile conflict: %v", err)
	}
	if got := reconciliationIssueCodes(report); !reflect.DeepEqual(got, []string{"kratos_person_conflict"}) {
		t.Fatalf("issue codes = %v", got)
	}
	if len(states.ensureCalls) != 0 || len(projector.calls) != 0 {
		t.Fatalf("conflict caused mutation: ensure=%v project=%v", states.ensureCalls, projector.calls)
	}
}

func TestPeopleAccountReconciliationAcceptsBoundIdentityWithoutPersonID(t *testing.T) {
	profiles := &fakePeopleAccountReconciliationProfiles{profiles: []agentcore.AgentProfile{
		reconciliationProfile("human-ding", "ding", ""),
	}}
	projector := &fakePeopleAccountReconciliationProjector{}
	report, err := (&peopleAccountReconciler{
		profiles: profiles,
		states:   &fakePeopleAccountReconciliationStates{states: map[string]peopleaccount.ProjectionState{}},
		kratos: &fakePeopleAccountReconciliationKratos{identities: map[string]peopleaccount.KratosIdentity{
			"ding": {ID: "kratos-ding"},
		}},
		subjects: &fakePeopleAccountReconciliationSubjects{subjects: []identity.IdentitySubject{
			{Provider: identity.ProviderKratos, Subject: "kratos-ding", AgentProfileID: "human-ding", Status: identity.SubjectStatusActive},
		}},
		projector: projector,
	}).Reconcile(context.Background(), false)
	if err != nil {
		t.Fatalf("Reconcile legacy identity: %v", err)
	}
	if len(report.Issues) != 0 || report.Ready != 1 {
		t.Fatalf("report = %+v", report)
	}
	if !reflect.DeepEqual(report.Actions, []peopleAccountReconciliationAction{
		{PersonID: "human-ding", Code: "seed_projection"},
		{PersonID: "human-ding", Code: "project_account"},
	}) {
		t.Fatalf("actions = %+v", report.Actions)
	}
}

func TestPeopleAccountReconciliationRejectsUnboundIdentityWithoutPersonID(t *testing.T) {
	report, err := (&peopleAccountReconciler{
		profiles: &fakePeopleAccountReconciliationProfiles{profiles: []agentcore.AgentProfile{
			reconciliationProfile("human-ding", "ding", ""),
		}},
		states: &fakePeopleAccountReconciliationStates{states: map[string]peopleaccount.ProjectionState{}},
		kratos: &fakePeopleAccountReconciliationKratos{identities: map[string]peopleaccount.KratosIdentity{
			"ding": {ID: "kratos-ding"},
		}},
		subjects:  &fakePeopleAccountReconciliationSubjects{},
		projector: &fakePeopleAccountReconciliationProjector{},
	}).Reconcile(context.Background(), false)
	if err != nil {
		t.Fatalf("Reconcile unbound identity: %v", err)
	}
	if got := reconciliationIssueCodes(report); !reflect.DeepEqual(got, []string{"unbound_kratos_identity"}) {
		t.Fatalf("issue codes = %v", got)
	}
	if report.Ready != 0 || len(report.Actions) != 0 {
		t.Fatalf("report = %+v", report)
	}
}

func TestPeopleAccountReconciliationDetectsUsernameIdentityDifferentFromProjection(t *testing.T) {
	profiles := &fakePeopleAccountReconciliationProfiles{profiles: []agentcore.AgentProfile{
		reconciliationProfile("human-quzheng", "quzheng", ""),
	}}
	states := &fakePeopleAccountReconciliationStates{states: map[string]peopleaccount.ProjectionState{
		"human-quzheng": {
			Projection: peopleaccount.Projection{PersonID: "human-quzheng", Revision: 1, Username: "quzheng", Enabled: true},
			Status:     peopleaccount.ProjectionStatusApplied, AppliedRevision: 1, KratosIdentityID: "kratos-old",
		},
	}}
	projector := &fakePeopleAccountReconciliationProjector{}
	report, err := (&peopleAccountReconciler{
		profiles: profiles,
		states:   states,
		kratos: &fakePeopleAccountReconciliationKratos{identities: map[string]peopleaccount.KratosIdentity{
			"old-identity": {ID: "kratos-old", PersonID: "human-quzheng"},
			"quzheng":      {ID: "kratos-new", PersonID: "human-quzheng"},
		}},
		subjects:  &fakePeopleAccountReconciliationSubjects{},
		projector: projector,
	}).Reconcile(context.Background(), true)
	if err != nil {
		t.Fatalf("Reconcile conflict: %v", err)
	}
	if got := reconciliationIssueCodes(report); !reflect.DeepEqual(got, []string{"projection_identity_conflict"}) {
		t.Fatalf("issue codes = %v", got)
	}
	if report.KratosIdentities != 2 {
		t.Fatalf("Kratos identities = %d, want 2", report.KratosIdentities)
	}
	if len(states.ensureCalls) != 0 || len(projector.calls) != 0 {
		t.Fatalf("conflict caused mutation: ensure=%v project=%v", states.ensureCalls, projector.calls)
	}
}

func TestPeopleAccountReconciliationApplyPreservesEarlierCredentialWhenLaterProjectionFails(t *testing.T) {
	profiles := &fakePeopleAccountReconciliationProfiles{profiles: []agentcore.AgentProfile{
		reconciliationProfile("human-quzheng", "quzheng", ""),
		reconciliationProfile("human-lipi", "lipi", ""),
	}}
	states := &fakePeopleAccountReconciliationStates{states: map[string]peopleaccount.ProjectionState{}}
	projector := &fakePeopleAccountReconciliationProjector{
		results: map[string]peopleaccount.ProjectionResult{
			"human-quzheng": {
				PersonID: "human-quzheng", Revision: 1, Status: peopleaccount.ProjectionStatusApplied,
				KratosIdentityID: "kratos-quzheng", InitialPassword: "initial-password-once",
			},
		},
		errors: map[string]error{"human-lipi": errors.New("Kratos unavailable")},
	}
	report, err := (&peopleAccountReconciler{
		profiles:  profiles,
		states:    states,
		kratos:    &fakePeopleAccountReconciliationKratos{identities: map[string]peopleaccount.KratosIdentity{}},
		subjects:  &fakePeopleAccountReconciliationSubjects{},
		projector: projector,
	}).Reconcile(context.Background(), true)
	if err != nil {
		t.Fatalf("Reconcile apply: %v", err)
	}
	if report.Applied != 1 || len(report.InitialCredentials) != 1 || report.InitialCredentials[0].PersonID != "human-quzheng" {
		t.Fatalf("apply report = %+v", report)
	}
	if got := reconciliationIssueCodes(report); !reflect.DeepEqual(got, []string{"projection_failed"}) {
		t.Fatalf("issue codes = %v", got)
	}
}

func reconciliationProfile(personID, username, passwordVerifier string) agentcore.AgentProfile {
	return agentcore.AgentProfile{
		ID: agentcore.AgentProfileID(personID), RealName: username, DisplayName: username, AgentKind: agentcore.AgentKindHuman, Enabled: true,
		UserAccount: agentcore.UserAccountSpec{Username: username, AccountEnabled: true, PasswordVerifier: passwordVerifier},
	}
}

func reconciliationIssueCodes(report peopleAccountReconciliationReport) []string {
	codes := make([]string, 0, len(report.Issues))
	for _, issue := range report.Issues {
		codes = append(codes, issue.Code)
	}
	return codes
}

type fakePeopleAccountReconciliationProfiles struct {
	profiles []agentcore.AgentProfile
}

func (f *fakePeopleAccountReconciliationProfiles) QueryDirectory(context.Context, agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	entries := make([]agentcore.AgentDirectoryEntry, 0, len(f.profiles))
	for _, profile := range f.profiles {
		entries = append(entries, agentcore.AgentDirectoryEntry{AgentProfileID: profile.ID, AgentKind: profile.AgentKind})
	}
	return agentcore.AgentDirectoryPage{Entries: entries, Total: len(entries)}, nil
}

func (f *fakePeopleAccountReconciliationProfiles) GetProfile(_ context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	for _, profile := range f.profiles {
		if profile.ID == id {
			return profile, nil
		}
	}
	return agentcore.AgentProfile{}, agentcore.ErrNotFound
}

type fakePeopleAccountReconciliationStates struct {
	states       map[string]peopleaccount.ProjectionState
	ensureCalls  []string
	legacyHashes []string
}

func (f *fakePeopleAccountReconciliationStates) ProjectionState(_ context.Context, personID string) (peopleaccount.ProjectionState, error) {
	state, ok := f.states[personID]
	if !ok {
		return peopleaccount.ProjectionState{}, peopleaccount.ErrProjectionNotFound
	}
	return state, nil
}

func (f *fakePeopleAccountReconciliationStates) EnsureProfileProjection(_ context.Context, profile agentcore.AgentProfile) (bool, error) {
	if _, ok := f.states[profile.ID.String()]; ok {
		return false, nil
	}
	f.ensureCalls = append(f.ensureCalls, profile.ID.String())
	f.legacyHashes = append(f.legacyHashes, profile.UserAccount.PasswordVerifier)
	f.states[profile.ID.String()] = peopleaccount.ProjectionState{
		Projection: peopleaccount.Projection{PersonID: profile.ID.String(), Revision: 1, Username: profile.UserAccount.Username, Enabled: true},
		Status:     peopleaccount.ProjectionStatusPending,
	}
	return true, nil
}

type fakePeopleAccountReconciliationKratos struct {
	identities map[string]peopleaccount.KratosIdentity
	errors     map[string]error
}

func (f *fakePeopleAccountReconciliationKratos) GetIdentity(_ context.Context, identityID string) (peopleaccount.KratosIdentity, bool, error) {
	for _, value := range f.identities {
		if value.ID == identityID {
			return value, true, nil
		}
	}
	return peopleaccount.KratosIdentity{}, false, nil
}

func (f *fakePeopleAccountReconciliationKratos) FindIdentityByUsername(_ context.Context, username string) (peopleaccount.KratosIdentity, bool, error) {
	if err := f.errors[username]; err != nil {
		return peopleaccount.KratosIdentity{}, false, err
	}
	value, ok := f.identities[username]
	return value, ok, nil
}

type fakePeopleAccountReconciliationSubjects struct {
	subjects []identity.IdentitySubject
}

func (f *fakePeopleAccountReconciliationSubjects) ListSubjects(context.Context, identity.SubjectQuery) (identity.SubjectPage, error) {
	return identity.SubjectPage{Subjects: f.subjects, Total: len(f.subjects), Returned: len(f.subjects)}, nil
}

type fakePeopleAccountReconciliationProjector struct {
	results map[string]peopleaccount.ProjectionResult
	errors  map[string]error
	calls   []string
}

func (f *fakePeopleAccountReconciliationProjector) ProjectLatest(_ context.Context, personID string) (peopleaccount.ProjectionResult, error) {
	f.calls = append(f.calls, personID)
	if err := f.errors[personID]; err != nil {
		return peopleaccount.ProjectionResult{}, err
	}
	if result, ok := f.results[personID]; ok {
		return result, nil
	}
	return peopleaccount.ProjectionResult{}, errors.New("unexpected projection call")
}
