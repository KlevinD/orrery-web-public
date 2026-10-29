package responsibility

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

const peopleContextResponseLimit = 2 << 20

// HTTPContextProvider consumes the read-only People responsibility snapshot
// endpoint. It never sends mutations to People and returns a bounded runtime
// projection for the Agent prompt and audit trail.
type HTTPContextProvider struct {
	baseURL string
	token   string
	client  *http.Client
}

type HTTPContextProviderOptions struct {
	BaseURL string
	Token   string
	Client  *http.Client
}

func NewHTTPContextProvider(baseURL string) *HTTPContextProvider {
	return NewHTTPContextProviderWithOptions(HTTPContextProviderOptions{BaseURL: baseURL})
}

func NewHTTPContextProviderWithOptions(opts HTTPContextProviderOptions) *HTTPContextProvider {
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if baseURL == "" {
		return nil
	}
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &HTTPContextProvider{baseURL: baseURL, token: strings.TrimSpace(opts.Token), client: client}
}

func (p *HTTPContextProvider) ResolveResponsibilityContext(ctx context.Context, request ContextRequest) (agentcore.ResponsibilityContext, error) {
	snapshot, err := p.resolveResponsibilitySnapshot(ctx, request)
	if err != nil {
		return agentcore.ResponsibilityContext{}, err
	}
	return snapshot.runtimeContext(request), nil
}

func (p *HTTPContextProvider) ResolveAutoplanGoalContext(ctx context.Context, request ContextRequest) (AutoplanGoalContext, error) {
	snapshot, err := p.resolveResponsibilitySnapshot(ctx, request)
	if err != nil {
		return AutoplanGoalContext{}, err
	}
	goalContext := snapshot.autoplanGoalContext(request)
	if err := goalContext.Validate(request); err != nil {
		return AutoplanGoalContext{}, err
	}
	return goalContext, nil
}

func (p *HTTPContextProvider) resolveResponsibilitySnapshot(ctx context.Context, request ContextRequest) (peopleResponsibilityContextSnapshot, error) {
	if p == nil || strings.TrimSpace(p.baseURL) == "" {
		return peopleResponsibilityContextSnapshot{}, fmt.Errorf("people responsibility context provider is not configured")
	}
	if strings.TrimSpace(request.ActorMemberID) == "" {
		return peopleResponsibilityContextSnapshot{}, fmt.Errorf("people responsibility context requires actor member id")
	}
	parsedURL, err := url.Parse(p.baseURL)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		if err == nil {
			err = fmt.Errorf("absolute http(s) URL is required")
		}
		return peopleResponsibilityContextSnapshot{}, fmt.Errorf("invalid People responsibility context base URL: %w", err)
	}
	payload := peopleResponsibilityContextSnapshotRequest{
		ActorMemberID:         strings.TrimSpace(request.ActorMemberID),
		AgentProfileID:        strings.TrimSpace(request.AgentProfileID),
		MissionID:             strings.TrimSpace(request.MissionID),
		TaskID:                strings.TrimSpace(request.TaskID),
		ResponsibilityID:      strings.TrimSpace(request.ResponsibilityID),
		ResponsibilityVersion: request.AssignmentVersion,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return peopleResponsibilityContextSnapshot{}, fmt.Errorf("encode People responsibility context request: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/responsibility-context/snapshots", strings.NewReader(string(body)))
	if err != nil {
		return peopleResponsibilityContextSnapshot{}, fmt.Errorf("create People responsibility context request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	if p.token != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+p.token)
	}
	client := p.client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return peopleResponsibilityContextSnapshot{}, fmt.Errorf("fetch People responsibility context: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		errorBody, _ := io.ReadAll(io.LimitReader(response.Body, 16<<10))
		return peopleResponsibilityContextSnapshot{}, fmt.Errorf("fetch People responsibility context: status %d: %s", response.StatusCode, strings.TrimSpace(string(errorBody)))
	}
	var snapshot peopleResponsibilityContextSnapshot
	decoder := json.NewDecoder(io.LimitReader(response.Body, peopleContextResponseLimit))
	if err := decoder.Decode(&snapshot); err != nil {
		return peopleResponsibilityContextSnapshot{}, fmt.Errorf("decode People responsibility context snapshot: %w", err)
	}
	if err := snapshot.validateIdentity(request); err != nil {
		return peopleResponsibilityContextSnapshot{}, err
	}
	return snapshot, nil
}

type peopleResponsibilityContextSnapshotRequest struct {
	ActorMemberID         string `json:"actor_member_id"`
	AgentProfileID        string `json:"agent_profile_id,omitempty"`
	MissionID             string `json:"mission_id,omitempty"`
	TaskID                string `json:"task_id,omitempty"`
	ResponsibilityID      string `json:"responsibility_id,omitempty"`
	ResponsibilityVersion int64  `json:"responsibility_version,omitempty"`
}

type peopleResponsibilityContextSnapshot struct {
	ID                    string                          `json:"id"`
	ActorMemberID         string                          `json:"actor_member_id"`
	AgentProfileID        string                          `json:"agent_profile_id"`
	MissionID             string                          `json:"mission_id"`
	TaskID                string                          `json:"task_id"`
	ResponsibilityID      string                          `json:"responsibility_id"`
	ResponsibilityVersion int64                           `json:"responsibility_version"`
	SourceVersions        map[string]string               `json:"source_versions"`
	ContextMarkdown       string                          `json:"context_markdown"`
	Context               peopleResponsibilityContextBody `json:"context"`
	CapturedAt            string                          `json:"captured_at,omitempty"`
	EffectiveAt           string                          `json:"effective_at,omitempty"`
	ExpiresAt             string                          `json:"expires_at,omitempty"`
}

func (s peopleResponsibilityContextSnapshot) validateIdentity(request ContextRequest) error {
	expectedMemberID := strings.TrimSpace(request.ActorMemberID)
	if responseMemberID := firstContextValue(s.ActorMemberID, s.Context.Actor.MemberID); responseMemberID != "" && responseMemberID != expectedMemberID {
		return fmt.Errorf("People responsibility context actor member %q does not match assignment member %q", responseMemberID, expectedMemberID)
	}
	if s.ActorMemberID != "" && s.Context.Actor.MemberID != "" && strings.TrimSpace(s.ActorMemberID) != strings.TrimSpace(s.Context.Actor.MemberID) {
		return fmt.Errorf("People responsibility context has inconsistent actor member identity")
	}
	expectedProfileID := strings.TrimSpace(request.AgentProfileID)
	if expectedProfileID != "" {
		if responseProfileID := firstContextValue(s.AgentProfileID, s.Context.Actor.AgentProfileID); responseProfileID != "" && responseProfileID != expectedProfileID {
			return fmt.Errorf("People responsibility context Agent profile %q does not match assignment profile %q", responseProfileID, expectedProfileID)
		}
		if s.AgentProfileID != "" && s.Context.Actor.AgentProfileID != "" && strings.TrimSpace(s.AgentProfileID) != strings.TrimSpace(s.Context.Actor.AgentProfileID) {
			return fmt.Errorf("People responsibility context has inconsistent Agent profile identity")
		}
	}
	if expectedMissionID := strings.TrimSpace(request.MissionID); expectedMissionID != "" && strings.TrimSpace(s.MissionID) != "" && strings.TrimSpace(s.MissionID) != expectedMissionID {
		return fmt.Errorf("People responsibility context mission %q does not match assignment mission %q", s.MissionID, expectedMissionID)
	}
	if expectedTaskID := strings.TrimSpace(request.TaskID); expectedTaskID != "" && strings.TrimSpace(s.TaskID) != "" && strings.TrimSpace(s.TaskID) != expectedTaskID {
		return fmt.Errorf("People responsibility context task %q does not match assignment task %q", s.TaskID, expectedTaskID)
	}
	return nil
}

type peopleResponsibilityContextBody struct {
	Actor        peopleResponsibilityActor        `json:"actor"`
	Organization peopleResponsibilityOrganization `json:"organization"`
	Position     peopleResponsibilityPosition     `json:"position"`
	Assignment   peopleResponsibilityAssignment   `json:"assignment"`
	Boundaries   peopleResponsibilityBoundaries   `json:"boundaries"`
	Evidence     peopleResponsibilityEvidence     `json:"evidence"`
	Company      struct {
		ActiveGoals []peopleGoal `json:"active_goals"`
	} `json:"company"`
}

type peopleResponsibilityActor struct {
	MemberID       string `json:"member_id"`
	AgentProfileID string `json:"agent_profile_id"`
}

type peopleResponsibilityOrganization struct {
	OrgNodeID        string                     `json:"org_node_id"`
	OrgName          string                     `json:"org_name"`
	Responsibilities []peopleResponsibilityFact `json:"responsibilities"`
	ActiveGoals      []peopleGoal               `json:"active_goals"`
}

type peopleResponsibilityPosition struct {
	PositionID       string                     `json:"position_id"`
	PositionName     string                     `json:"position_name"`
	Responsibilities []peopleResponsibilityFact `json:"responsibilities"`
	ActiveGoals      []peopleGoal               `json:"active_goals"`
}

type peopleResponsibilityAssignment struct {
	AssignmentGoals []peopleAssignmentGoal `json:"assignment_goals"`
}

type peopleResponsibilityBoundaries struct {
	AllowedTools      []string `json:"allowed_tools"`
	AllowedDataScopes []string `json:"allowed_data_scopes"`
	ForbiddenActions  []string `json:"forbidden_actions"`
	HILTriggers       []string `json:"hil_triggers"`
}

type peopleResponsibilityEvidence struct {
	ExpectedEvidence  []string `json:"expected_evidence"`
	SourceRefs        []string `json:"source_refs"`
	ContextSnapshotID string   `json:"context_snapshot_id"`
}

type peopleResponsibilityFact struct {
	ID                    string   `json:"id"`
	MissionStatement      string   `json:"mission_statement"`
	ResponsibilitySummary string   `json:"responsibility_summary"`
	AccountableOutputs    []string `json:"accountable_outputs"`
	CoreDuties            []string `json:"core_duties"`
	ForbiddenActions      []string `json:"forbidden_actions"`
	EvidenceExpectations  []string `json:"evidence_expectations"`
}

type peopleGoal struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Description     string   `json:"description"`
	ExpectedOutcome string   `json:"expected_outcome"`
	Outcome         string   `json:"outcome"`
	Metrics         []string `json:"metrics"`
	MetricTargets   []string `json:"metric_targets"`
	SuccessMetrics  []string `json:"success_metrics"`
}

type peopleAssignmentGoal struct {
	ID              string `json:"id"`
	PersonalOutcome string `json:"personal_outcome"`
	ReviewPolicy    string `json:"review_policy"`
}

func (s peopleResponsibilityContextSnapshot) runtimeContext(request ContextRequest) agentcore.ResponsibilityContext {
	contextID := strings.TrimSpace(s.ID)
	if contextID == "" {
		contextID = strings.TrimSpace(s.Context.Evidence.ContextSnapshotID)
	}
	version := s.ResponsibilityVersion
	if version < 1 {
		version = maxContextVersion(request.AssignmentVersion, s.SourceVersions)
	}
	result := agentcore.ResponsibilityContext{
		ContextID:             contextID,
		ResponsibilityID:      firstContextValue(s.ResponsibilityID, request.ResponsibilityID),
		ResponsibilityVersion: version,
		Source:                "people.responsibility_context",
		ActorMemberID:         firstContextValue(s.ActorMemberID, s.Context.Actor.MemberID, request.ActorMemberID),
		AgentProfileID:        firstContextValue(s.AgentProfileID, s.Context.Actor.AgentProfileID, request.AgentProfileID),
		MissionID:             firstContextValue(s.MissionID, request.MissionID),
		TaskID:                firstContextValue(s.TaskID, request.TaskID),
		OrganizationID:        strings.TrimSpace(s.Context.Organization.OrgNodeID),
		OrganizationName:      strings.TrimSpace(s.Context.Organization.OrgName),
		PositionID:            strings.TrimSpace(s.Context.Position.PositionID),
		PositionName:          strings.TrimSpace(s.Context.Position.PositionName),
		Summary:               strings.TrimSpace(s.ContextMarkdown),
		Goals:                 contextGoals(s.Context),
		Boundaries:            contextBoundaries(s.Context.Boundaries),
		AllowedTools:          uniqueContextValues(s.Context.Boundaries.AllowedTools),
		AllowedDataScopes:     uniqueContextValues(s.Context.Boundaries.AllowedDataScopes),
		ForbiddenActions:      uniqueContextValues(s.Context.Boundaries.ForbiddenActions),
		HumanInLoopTriggers:   uniqueContextValues(s.Context.Boundaries.HILTriggers),
		EvidenceRequirements:  contextEvidence(s.Context),
		SourceRefs:            contextSourceRefs(s.SourceVersions, s.Context.Evidence.SourceRefs),
	}
	result.CapturedAt = parseContextTime(s.CapturedAt)
	result.EffectiveAt = parseContextTime(s.EffectiveAt)
	if result.EffectiveAt.IsZero() {
		result.EffectiveAt = result.CapturedAt
	}
	if expiresAt, err := time.Parse(time.RFC3339, strings.TrimSpace(s.ExpiresAt)); err == nil {
		result.ExpiresAt = expiresAt
	}
	return result
}

func (s peopleResponsibilityContextSnapshot) autoplanGoalContext(request ContextRequest) AutoplanGoalContext {
	contextID := firstContextValue(s.ID, s.Context.Evidence.ContextSnapshotID)
	version := s.ResponsibilityVersion
	if version < 1 {
		version = maxContextVersion(request.AssignmentVersion, s.SourceVersions)
	}
	goals := make([]AutoplanGoalAnchor, 0,
		len(s.Context.Assignment.AssignmentGoals)+len(s.Context.Position.ActiveGoals)+
			len(s.Context.Organization.ActiveGoals)+len(s.Context.Company.ActiveGoals))
	for _, goal := range s.Context.Assignment.AssignmentGoals {
		appendAutoplanAssignmentGoal(&goals, goal, s.SourceVersions)
	}
	for _, goal := range s.Context.Position.ActiveGoals {
		appendAutoplanPeopleGoal(&goals, "position", goal, s.SourceVersions)
	}
	for _, goal := range s.Context.Organization.ActiveGoals {
		appendAutoplanPeopleGoal(&goals, "organization", goal, s.SourceVersions)
	}
	for _, goal := range s.Context.Company.ActiveGoals {
		appendAutoplanPeopleGoal(&goals, "company", goal, s.SourceVersions)
	}
	refs := make([]string, 0, len(goals))
	for _, goal := range goals {
		refs = append(refs, goal.Ref)
	}
	return (AutoplanGoalContext{
		ContextID: contextID, ResponsibilityID: firstContextValue(s.ResponsibilityID, request.ResponsibilityID),
		ResponsibilityVersion: version,
		AgentProfileID:        firstContextValue(s.AgentProfileID, s.Context.Actor.AgentProfileID, request.AgentProfileID),
		Goals:                 goals,
		SourceRefs:            refs,
		CapturedAt:            parseContextTime(s.CapturedAt),
	}).Normalized()
}

func appendAutoplanPeopleGoal(out *[]AutoplanGoalAnchor, scope string, goal peopleGoal, versions map[string]string) {
	text := firstContextValue(goal.ExpectedOutcome, goal.Outcome, goal.Title, goal.Description)
	if text == "" {
		return
	}
	ref := peopleAutoplanGoalRef(scope, goal.ID, versions)
	if ref == "" {
		return
	}
	criteria := append(append(append([]string(nil), goal.Metrics...), goal.MetricTargets...), goal.SuccessMetrics...)
	*out = append(*out, AutoplanGoalAnchor{
		Ref: ref, Scope: scope, Goal: text, SuccessCriteria: criteria,
	})
}

func appendAutoplanAssignmentGoal(out *[]AutoplanGoalAnchor, goal peopleAssignmentGoal, versions map[string]string) {
	text := strings.TrimSpace(goal.PersonalOutcome)
	if text == "" {
		return
	}
	ref := peopleAutoplanGoalRef("assignment", goal.ID, versions)
	if ref == "" {
		return
	}
	criteria := []string(nil)
	if reviewPolicy := strings.TrimSpace(goal.ReviewPolicy); reviewPolicy != "" {
		criteria = append(criteria, reviewPolicy)
	}
	*out = append(*out, AutoplanGoalAnchor{
		Ref: ref, Scope: "assignment", Goal: text, SuccessCriteria: criteria,
	})
}

func peopleAutoplanGoalRef(scope, id string, versions map[string]string) string {
	scope, id = strings.ToLower(strings.TrimSpace(scope)), strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	kind := peopleAutoplanGoalKind(scope)
	if kind == "" {
		return ""
	}
	for _, candidate := range peopleAutoplanGoalVersionKeys(scope, id) {
		if version := strings.TrimSpace(versions[candidate]); version != "" && version != "0" {
			return "people:" + kind + ":" + id + ":v" + version
		}
	}
	return ""
}

func peopleAutoplanGoalVersionKeys(scope, id string) []string {
	if id == "" {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "company":
		return []string{"company_goal:" + id}
	case "organization":
		return []string{"org_goal:" + id, "organization_goal:" + id}
	case "position":
		return []string{"position_goal:" + id}
	case "assignment":
		return []string{"member_assignment_goal:" + id, "assignment_goal:" + id}
	default:
		return nil
	}
}

func peopleAutoplanGoalKind(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "company":
		return "company_goal"
	case "organization":
		return "org_goal"
	case "position":
		return "position_goal"
	case "assignment":
		return "member_assignment_goal"
	default:
		return ""
	}
}

func parseContextTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

func contextGoals(value peopleResponsibilityContextBody) []string {
	goals := make([]string, 0)
	for _, goal := range append(append(append([]peopleGoal{}, value.Company.ActiveGoals...), value.Organization.ActiveGoals...), value.Position.ActiveGoals...) {
		appendContextValue(&goals, firstContextValue(goal.Title, goal.ExpectedOutcome, goal.Outcome, goal.Description))
		for _, metric := range append(append(append([]string(nil), goal.Metrics...), goal.MetricTargets...), goal.SuccessMetrics...) {
			appendContextValue(&goals, metric)
		}
	}
	for _, goal := range value.Assignment.AssignmentGoals {
		appendContextValue(&goals, goal.PersonalOutcome)
	}
	for _, responsibility := range append(append([]peopleResponsibilityFact{}, value.Organization.Responsibilities...), value.Position.Responsibilities...) {
		appendContextValue(&goals, firstContextValue(responsibility.MissionStatement, responsibility.ResponsibilitySummary))
		for _, output := range append(append([]string(nil), responsibility.AccountableOutputs...), responsibility.CoreDuties...) {
			appendContextValue(&goals, output)
		}
	}
	return goals
}

func contextBoundaries(value peopleResponsibilityBoundaries) []string {
	boundaries := make([]string, 0)
	for _, item := range value.AllowedTools {
		appendContextValue(&boundaries, "allowed tool: "+item)
	}
	for _, item := range value.AllowedDataScopes {
		appendContextValue(&boundaries, "allowed data: "+item)
	}
	for _, item := range append(append([]string(nil), value.ForbiddenActions...), value.HILTriggers...) {
		appendContextValue(&boundaries, item)
	}
	return boundaries
}

func contextEvidence(value peopleResponsibilityContextBody) []string {
	evidence := append([]string(nil), value.Evidence.ExpectedEvidence...)
	for _, responsibility := range append(append([]peopleResponsibilityFact{}, value.Organization.Responsibilities...), value.Position.Responsibilities...) {
		evidence = append(evidence, responsibility.EvidenceExpectations...)
	}
	return uniqueContextValues(evidence)
}

func contextSourceRefs(versions map[string]string, refs []string) []string {
	result := append([]string(nil), refs...)
	keys := make([]string, 0, len(versions))
	for key := range versions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		result = append(result, "people:"+key+":v"+strings.TrimSpace(versions[key]))
	}
	return uniqueContextValues(result)
}

func maxContextVersion(fallback int64, versions map[string]string) int64 {
	if fallback < 1 {
		fallback = 1
	}
	for _, value := range versions {
		var version int64
		if _, err := fmt.Sscan(strings.TrimSpace(value), &version); err == nil && version > fallback {
			fallback = version
		}
	}
	return fallback
}

func firstContextValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func appendContextValue(values *[]string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	for _, existing := range *values {
		if existing == value {
			return
		}
	}
	*values = append(*values, value)
}

func uniqueContextValues(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		appendContextValue(&result, value)
	}
	return result
}
