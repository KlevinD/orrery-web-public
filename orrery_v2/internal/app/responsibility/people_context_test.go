package responsibility

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPContextProviderCreatesBoundedRuntimeProjection(t *testing.T) {
	var received peopleResponsibilityContextSnapshotRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/responsibility-context/snapshots" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer people-token" {
			t.Fatalf("Authorization = %q", got)
		}
		snapshot := peopleResponsibilityContextSnapshot{
			ID: "snapshot-1",
			SourceVersions: map[string]string{
				"company_goal:company-goal-1":              "2",
				"org_goal:org-goal-1":                      "3",
				"position_goal:goal-1":                     "4",
				"member_assignment_goal:assignment-goal-1": "5",
				"position_responsibility:resp-1":           "3",
			},
			ContextMarkdown: "## Responsibility context\nOwn delivery quality.",
			CapturedAt:      "2026-08-05T10:00:00.123456789Z",
			EffectiveAt:     "2026-08-05T09:00:00Z",
			ExpiresAt:       "2026-08-05T12:00:00Z",
		}
		snapshot.Context.Actor = peopleResponsibilityActor{MemberID: "member-1", AgentProfileID: "profile-1"}
		snapshot.Context.Company.ActiveGoals = []peopleGoal{{ID: "company-goal-1", Title: "Company outcome"}}
		snapshot.Context.Organization = peopleResponsibilityOrganization{
			OrgNodeID: "org-1", OrgName: "Delivery",
			Responsibilities: []peopleResponsibilityFact{{ID: "org-resp-1", MissionStatement: "Keep delivery reliable", AccountableOutputs: []string{"Release readiness"}}},
			ActiveGoals:      []peopleGoal{{ID: "org-goal-1", Title: "Org outcome"}},
		}
		snapshot.Context.Position = peopleResponsibilityPosition{
			PositionID: "position-1", PositionName: "Delivery Agent",
			Responsibilities: []peopleResponsibilityFact{{ID: "resp-1", ResponsibilitySummary: "Own delivery quality", CoreDuties: []string{"Review evidence"}, EvidenceExpectations: []string{"test log"}}},
			ActiveGoals:      []peopleGoal{{ID: "goal-1", Title: "Position outcome", MetricTargets: []string{"99% pass rate"}}},
		}
		snapshot.Context.Assignment.AssignmentGoals = []peopleAssignmentGoal{{ID: "assignment-goal-1", PersonalOutcome: "Close delivery gaps"}}
		snapshot.Context.Boundaries = peopleResponsibilityBoundaries{
			AllowedTools: []string{"repo.read"}, AllowedDataScopes: []string{"mission"}, ForbiddenActions: []string{"deploy.production"}, HILTriggers: []string{"external commitment"},
		}
		snapshot.Context.Evidence = peopleResponsibilityEvidence{
			ExpectedEvidence: []string{"review record"}, SourceRefs: []string{"position_responsibility:resp-1"}, ContextSnapshotID: "snapshot-1",
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(snapshot); err != nil {
			t.Fatalf("encode snapshot: %v", err)
		}
	}))
	defer server.Close()

	provider := NewHTTPContextProviderWithOptions(HTTPContextProviderOptions{BaseURL: server.URL, Token: "people-token"})
	result, err := provider.ResolveResponsibilityContext(context.Background(), ContextRequest{
		ActorMemberID: "member-1", AgentProfileID: "profile-1", MissionID: "mission-1", TaskID: "task-1",
		ResponsibilityID: "responsibility-1", AssignmentVersion: 2,
	})
	if err != nil {
		t.Fatalf("ResolveResponsibilityContext returned error: %v", err)
	}
	if received.ActorMemberID != "member-1" || received.AgentProfileID != "profile-1" || received.MissionID != "mission-1" || received.TaskID != "task-1" || received.ResponsibilityID != "responsibility-1" || received.ResponsibilityVersion != 2 {
		t.Fatalf("snapshot request = %+v", received)
	}
	if result.ContextID != "snapshot-1" || result.ActorMemberID != "member-1" || result.AgentProfileID != "profile-1" || result.OrganizationName != "Delivery" || result.PositionName != "Delivery Agent" {
		t.Fatalf("runtime responsibility identity = %+v", result)
	}
	joinedGoals := strings.Join(result.Goals, "\n")
	for _, expected := range []string{"Company outcome", "Org outcome", "Position outcome", "Close delivery gaps", "Own delivery quality", "Release readiness"} {
		if !strings.Contains(joinedGoals, expected) {
			t.Fatalf("goals %q missing %q", joinedGoals, expected)
		}
	}
	joinedBoundaries := strings.Join(result.Boundaries, "\n")
	for _, expected := range []string{"allowed tool: repo.read", "allowed data: mission", "deploy.production", "external commitment"} {
		if !strings.Contains(joinedBoundaries, expected) {
			t.Fatalf("boundaries %q missing %q", joinedBoundaries, expected)
		}
	}
	if len(result.AllowedTools) != 1 || result.AllowedTools[0] != "repo.read" || len(result.AllowedDataScopes) != 1 || result.AllowedDataScopes[0] != "mission" {
		t.Fatalf("structured allowed boundaries = tools:%+v data:%+v", result.AllowedTools, result.AllowedDataScopes)
	}
	if len(result.ForbiddenActions) != 1 || result.ForbiddenActions[0] != "deploy.production" || len(result.HumanInLoopTriggers) != 1 || result.HumanInLoopTriggers[0] != "external commitment" {
		t.Fatalf("structured denied boundaries = forbidden:%+v hil:%+v", result.ForbiddenActions, result.HumanInLoopTriggers)
	}
	if len(result.EvidenceRequirements) != 2 || result.EvidenceRequirements[0] != "review record" || result.EvidenceRequirements[1] != "test log" {
		t.Fatalf("evidence requirements = %+v", result.EvidenceRequirements)
	}
	if result.ResponsibilityVersion != 5 || result.ExpiresAt.IsZero() || !result.ExpiresAt.Equal(time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("version/expiry = %d/%v", result.ResponsibilityVersion, result.ExpiresAt)
	}
	if result.CapturedAt.Format(time.RFC3339Nano) != "2026-08-05T10:00:00.123456789Z" || result.EffectiveAt.Format(time.RFC3339) != "2026-08-05T09:00:00Z" {
		t.Fatalf("captured/effective time = %s/%s", result.CapturedAt, result.EffectiveAt)
	}
	if len(result.SourceRefs) == 0 || result.SourceRefs[0] != "position_responsibility:resp-1" {
		t.Fatalf("source refs = %+v", result.SourceRefs)
	}

	goalContext, err := provider.ResolveAutoplanGoalContext(context.Background(), ContextRequest{
		ActorMemberID: "member-1", AgentProfileID: "profile-1", ResponsibilityID: "responsibility-1", AssignmentVersion: 2,
	})
	if err != nil {
		t.Fatalf("ResolveAutoplanGoalContext returned error: %v", err)
	}
	if goalContext.ContextID != "snapshot-1" || goalContext.ResponsibilityID != "responsibility-1" || len(goalContext.Goals) != 4 {
		t.Fatalf("autoplan goal context = %+v", goalContext)
	}
	joinedAutoplanGoals := ""
	for _, goal := range goalContext.Goals {
		joinedAutoplanGoals += goal.Goal + "\n"
	}
	for _, expected := range []string{"Company outcome", "Org outcome", "Position outcome", "Close delivery gaps"} {
		if !strings.Contains(joinedAutoplanGoals, expected) {
			t.Fatalf("autoplan goals %q missing %q", joinedAutoplanGoals, expected)
		}
	}
	for _, responsibilityText := range []string{"Own delivery quality", "Release readiness", "Review evidence"} {
		if strings.Contains(joinedAutoplanGoals, responsibilityText) {
			t.Fatalf("autoplan goals %q include responsibility text %q", joinedAutoplanGoals, responsibilityText)
		}
	}
	wantRefs := []string{
		"people:member_assignment_goal:assignment-goal-1:v5",
		"people:position_goal:goal-1:v4",
		"people:org_goal:org-goal-1:v3",
		"people:company_goal:company-goal-1:v2",
	}
	for index, want := range wantRefs {
		if goalContext.Goals[index].Ref != want {
			t.Fatalf("autoplan goal ref[%d] = %q, want %q", index, goalContext.Goals[index].Ref, want)
		}
	}
}

func TestAutoplanGoalContextRejectsResponsibilityDisguisedAsAssignmentGoal(t *testing.T) {
	snapshot := peopleResponsibilityContextSnapshot{
		ID:                    "compatibility-snapshot",
		ResponsibilityID:      "responsibility-1",
		ResponsibilityVersion: 3,
		AgentProfileID:        "profile-1",
		SourceVersions:        map[string]string{"responsibility:responsibility-1": "3"},
	}
	snapshot.Context.Assignment.AssignmentGoals = []peopleAssignmentGoal{{
		ID: "responsibility-1", PersonalOutcome: "Responsibility text is not an explicit goal",
	}}

	context := snapshot.autoplanGoalContext(ContextRequest{
		ResponsibilityID: "responsibility-1", AssignmentVersion: 3, AgentProfileID: "profile-1",
	})
	if len(context.Goals) != 0 || len(context.SourceRefs) != 0 {
		t.Fatalf("compatibility responsibility became an autonomous goal: %+v", context)
	}
}

func TestHTTPContextProviderRejectsNonHTTPBaseURL(t *testing.T) {
	provider := NewHTTPContextProvider("people-context")
	if _, err := provider.ResolveResponsibilityContext(context.Background(), ContextRequest{ActorMemberID: "member-1"}); err == nil {
		t.Fatal("ResolveResponsibilityContext accepted a non-HTTP base URL")
	}
}

func TestHTTPContextProviderRejectsIdentityMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(peopleResponsibilityContextSnapshot{
			ID: "snapshot-1", ActorMemberID: "member-other",
		}); err != nil {
			t.Errorf("encode snapshot: %v", err)
		}
	}))
	defer server.Close()

	provider := NewHTTPContextProvider(server.URL)
	if _, err := provider.ResolveResponsibilityContext(context.Background(), ContextRequest{ActorMemberID: "member-1"}); err == nil {
		t.Fatal("ResolveResponsibilityContext accepted an actor identity mismatch")
	}
}

var _ ContextProvider = (*HTTPContextProvider)(nil)
