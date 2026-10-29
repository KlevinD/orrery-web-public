package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"orrery_v2/internal/platform/config"
)

func TestOntologyStewardPeopleClientResumesAndIsIdempotent(t *testing.T) {
	people := &ontologyStewardPeopleServer{
		t:                t,
		changeSets:       make(map[string]*ontologyStewardPeopleChangeSet),
		failPositionOnce: true,
	}
	server := httptest.NewServer(people)
	defer server.Close()
	client, err := newOntologyStewardPeopleClient(server.URL, "test-token", server.Client())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := client.ensure(t.Context()); err == nil {
		t.Fatal("expected the injected People failure")
	}
	workforce := people.changeSets[defaultOntologyStewardPeopleWorkforceChangeSetID]
	if workforce == nil || len(workforce.Operations) != 2 || workforce.Status != "draft" {
		t.Fatalf("partial workforce ChangeSet = %+v", workforce)
	}

	projection, err := client.ensure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if projection.SubjectID != "subject-generated" || projection.PositionID != "position-generated" ||
		projection.ResponsibilityID != "responsibility-generated" {
		t.Fatalf("projection = %+v", projection)
	}
	mutations := people.mutations
	second, err := client.ensure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if second != projection || people.mutations != mutations {
		t.Fatalf("idempotent ensure = %+v mutations=%d, want %+v mutations=%d", second, people.mutations, projection, mutations)
	}

	cfg := config.Config{}
	if err := bindDefaultOntologyStewardPeopleSubject(&cfg, projection.SubjectID); err != nil {
		t.Fatal(err)
	}
	if got := cfg.Integrations.People.AgentBindings[projection.SubjectID]; got != defaultOntologyStewardAgentID {
		t.Fatalf("binding = %q, want %q", got, defaultOntologyStewardAgentID)
	}
}

func TestBindDefaultOntologyStewardPeopleSubjectRejectsConflicts(t *testing.T) {
	for _, bindings := range []map[string]string{
		{"subject-other": defaultOntologyStewardAgentID},
		{"subject-generated": "another-agent"},
	} {
		cfg := config.Config{}
		cfg.Integrations.People.AgentBindings = bindings
		if err := bindDefaultOntologyStewardPeopleSubject(&cfg, "subject-generated"); err == nil {
			t.Fatalf("expected binding conflict for %+v", bindings)
		}
	}
}

func TestOntologyStewardPeopleClientStopsWhenApprovalIsRequired(t *testing.T) {
	people := &ontologyStewardPeopleServer{
		t: t, changeSets: make(map[string]*ontologyStewardPeopleChangeSet), requireApproval: true,
	}
	server := httptest.NewServer(people)
	defer server.Close()
	client, err := newOntologyStewardPeopleClient(server.URL, "test-token", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ensure(t.Context()); err == nil || !strings.Contains(err.Error(), "approval_required") {
		t.Fatalf("ensure error = %v, want approval_required", err)
	}
	workforce := people.changeSets[defaultOntologyStewardPeopleWorkforceChangeSetID]
	if workforce == nil || workforce.Status != "submitted" || people.changeSets[defaultOntologyStewardPeopleResponsibilityChangeSetID] != nil {
		t.Fatalf("approval-gated projection workforce=%+v responsibilities=%+v", workforce, people.changeSets[defaultOntologyStewardPeopleResponsibilityChangeSetID])
	}
}

type ontologyStewardPeopleServer struct {
	t                *testing.T
	changeSets       map[string]*ontologyStewardPeopleChangeSet
	mutations        int
	failPositionOnce bool
	requireApproval  bool
}

func (s *ontologyStewardPeopleServer) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Authorization") != "Bearer test-token" {
		s.problem(response, http.StatusUnauthorized, "unauthenticated", "missing token")
		return
	}
	if request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/api/v2/subjects/") {
		s.writeResponsibilities(response, request.URL.Path)
		return
	}
	if request.URL.Path == "/api/v2/change-sets" && request.Method == http.MethodPost {
		s.createChangeSet(response, request)
		return
	}
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(parts) < 4 || parts[0] != "api" || parts[1] != "v2" || parts[2] != "change-sets" {
		s.problem(response, http.StatusNotFound, "not_found", "unknown path")
		return
	}
	id := parts[3]
	current := s.changeSets[id]
	if current == nil {
		s.problem(response, http.StatusNotFound, "change_set_not_found", "missing ChangeSet")
		return
	}
	if request.Method == http.MethodGet && len(parts) == 4 {
		s.writeJSON(response, http.StatusOK, current)
		return
	}
	if request.Method != http.MethodPost || len(parts) != 5 {
		s.problem(response, http.StatusNotFound, "not_found", "unknown ChangeSet action")
		return
	}
	s.requireMutationHeaders(request, current.Revision)
	s.mutations++
	switch parts[4] {
	case "operations":
		s.addOperation(response, request, current)
	case "validate":
		current.Status = "validated"
		current.Revision++
		s.writeJSON(response, http.StatusOK, current)
	case "submit":
		current.Status = "submitted"
		current.Revision++
		s.writeJSON(response, http.StatusOK, current)
	case "apply":
		if s.requireApproval {
			s.problem(response, http.StatusForbidden, "approval_required", "approval is pending")
			return
		}
		current.Status = "applied"
		current.Revision++
		s.writeJSON(response, http.StatusOK, current)
	default:
		s.problem(response, http.StatusNotFound, "not_found", "unknown ChangeSet action")
	}
}

func (s *ontologyStewardPeopleServer) createChangeSet(response http.ResponseWriter, request *http.Request) {
	s.requireMutationHeaders(request, 0)
	var input struct {
		ID          string `json:"id"`
		Reason      string `json:"reason"`
		EffectiveAt string `json:"effective_at"`
		EvidenceRef string `json:"evidence_ref"`
	}
	if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
		s.t.Fatal(err)
	}
	if s.changeSets[input.ID] != nil {
		s.t.Fatalf("duplicate ChangeSet create %q", input.ID)
	}
	current := &ontologyStewardPeopleChangeSet{
		ID: input.ID, Reason: input.Reason, EffectiveAt: input.EffectiveAt,
		EvidenceRef: input.EvidenceRef, Status: "draft", Revision: 1,
		Operations: []ontologyStewardPeopleChangeSetOperation{},
	}
	s.changeSets[input.ID] = current
	s.mutations++
	s.writeJSON(response, http.StatusCreated, current)
}

func (s *ontologyStewardPeopleServer) addOperation(response http.ResponseWriter, request *http.Request, current *ontologyStewardPeopleChangeSet) {
	var input struct {
		OperationType string         `json:"operation_type"`
		Payload       map[string]any `json:"payload"`
	}
	if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
		s.t.Fatal(err)
	}
	if input.OperationType == "position.create" && s.failPositionOnce {
		s.failPositionOnce = false
		s.problem(response, http.StatusServiceUnavailable, "temporarily_unavailable", "retry")
		return
	}
	serverID, aggregateType := ontologyStewardPeopleTestOperationIdentity(input.OperationType)
	input.Payload[ontologyStewardPeopleServerIDField(input.OperationType)] = serverID
	payload, err := json.Marshal(input.Payload)
	if err != nil {
		s.t.Fatal(err)
	}
	current.Operations = append(current.Operations, ontologyStewardPeopleChangeSetOperation{
		Index: len(current.Operations), OperationType: input.OperationType,
		AggregateType: aggregateType, AggregateID: serverID, Payload: payload,
	})
	current.Status = "draft"
	current.Revision++
	s.writeJSON(response, http.StatusOK, current)
}

func ontologyStewardPeopleTestOperationIdentity(operationType string) (string, string) {
	switch operationType {
	case "subject.create":
		return "subject-generated", "subject"
	case "job_profile.create":
		return "job-profile-generated", "job_profile"
	case "position.create":
		return "position-generated", "position"
	case "assignment.start":
		return "assignment-generated", "assignment"
	case "reporting.set":
		return "reporting-relationship-generated", "reporting_relationship"
	case "responsibility.create":
		return "responsibility-generated", "responsibility"
	case "responsibility.bind":
		return "responsibility-binding-generated", "responsibility_binding"
	default:
		return "unknown", "unknown"
	}
}

func (s *ontologyStewardPeopleServer) writeResponsibilities(response http.ResponseWriter, path string) {
	subjectID := strings.TrimPrefix(path, "/api/v2/subjects/")
	subjectID = strings.TrimSuffix(subjectID, "/responsibilities")
	workforce := s.changeSets[defaultOntologyStewardPeopleWorkforceChangeSetID]
	responsibilities := s.changeSets[defaultOntologyStewardPeopleResponsibilityChangeSetID]
	if workforce == nil || responsibilities == nil || workforce.Status != "applied" || responsibilities.Status != "applied" {
		s.problem(response, http.StatusNotFound, "subject_not_found", "projection incomplete")
		return
	}
	s.writeJSON(response, http.StatusOK, map[string]any{
		"subject_id": subjectID,
		"items": []any{map[string]any{
			"id": "responsibility-generated",
			"sources": []any{map[string]any{
				"responsibility_id": "responsibility-generated", "target_type": "position", "target_id": "position-generated",
			}},
		}},
	})
}

func (s *ontologyStewardPeopleServer) requireMutationHeaders(request *http.Request, revision int64) {
	idempotencyKey := request.Header.Get("Idempotency-Key")
	if idempotencyKey == "" || request.Header.Get("X-Correlation-ID") != idempotencyKey {
		s.t.Fatalf("invalid mutation identity headers: %v", request.Header)
	}
	wantIfMatch := ""
	if revision > 0 {
		wantIfMatch = strconv.Quote(strconv.FormatInt(revision, 10))
	}
	if got := request.Header.Get("If-Match"); got != wantIfMatch {
		s.t.Fatalf("If-Match = %q, want %q", got, wantIfMatch)
	}
}

func (s *ontologyStewardPeopleServer) problem(response http.ResponseWriter, status int, code, detail string) {
	s.writeJSON(response, status, map[string]string{"code": code, "detail": detail})
}

func (s *ontologyStewardPeopleServer) writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	if err := json.NewEncoder(response).Encode(value); err != nil {
		s.t.Fatal(fmt.Errorf("encode response: %w", err))
	}
}
