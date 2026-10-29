package discussionsession

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/operations"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func readLeadStreamEvent(t *testing.T, events <-chan StreamEventDTO) StreamEventDTO {
	t.Helper()
	select {
	case event, ok := <-events:
		if !ok {
			t.Fatal("stream closed before event")
		}
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stream event")
		return StreamEventDTO{}
	}
}

func readLeadStreamKindsUntil(t *testing.T, events <-chan StreamEventDTO, terminal string) []string {
	t.Helper()
	var kinds []string
	for {
		event := readLeadStreamEvent(t, events)
		kinds = append(kinds, event.Kind)
		if event.Kind == terminal {
			return kinds
		}
	}
}

func readLeadStreamKindsUntilSnapshotAfterRunCompleted(t *testing.T, events <-chan StreamEventDTO) []string {
	t.Helper()
	var kinds []string
	sawCompleted := false
	for {
		event := readLeadStreamEvent(t, events)
		kinds = append(kinds, event.Kind)
		if event.Kind == StreamEventRunCompleted {
			sawCompleted = true
			continue
		}
		if sawCompleted && event.Kind == StreamEventSnapshot {
			return kinds
		}
	}
}

func drainLeadStream(events <-chan StreamEventDTO) {
	for range events {
	}
}

var testTime = time.Date(2026, 6, 2, 1, 2, 3, 0, time.UTC)

type recordingRealtimePublisher struct {
	mu     sync.Mutex
	events []RealtimeEvent
	seen   map[string]bool
	err    error
}

func (p *recordingRealtimePublisher) AppendRealtimeEvent(_ context.Context, event RealtimeEvent) error {
	if p.err != nil {
		return p.err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.seen == nil {
		p.seen = map[string]bool{}
	}
	if p.seen[event.EventID] {
		return nil
	}
	p.seen[event.EventID] = true
	p.events = append(p.events, event)
	return nil
}

func (p *recordingRealtimePublisher) eventTypes() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	types := make([]string, 0, len(p.events))
	for _, event := range p.events {
		types = append(types, event.Type)
	}
	return types
}

func (p *recordingRealtimePublisher) eventsByType(eventType string) []RealtimeEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]RealtimeEvent, 0)
	for _, event := range p.events {
		if event.Type == eventType {
			out = append(out, event)
		}
	}
	return out
}

func realtimePayloadString(t *testing.T, event RealtimeEvent, key string) string {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("payload for %s is not json: %v", event.Type, err)
	}
	value, _ := payload[key].(string)
	return value
}

func newMemoryRepository(t *testing.T) *Repository {
	t.Helper()
	return NewRepository(newRepositoryTestRouter(t))
}

func newTestService(opts ServiceOptions) *Service {
	if opts.AgentDirectory == nil {
		opts.AgentDirectory = defaultTestAgentDirectory()
	}
	if opts.HumanProfiles == nil {
		opts.HumanProfiles = &testHumanProfiles{}
	}
	if opts.InitialAgentMembers == nil {
		opts.InitialAgentMembers = defaultTestConversationAgentMembers()
	}
	return NewService(opts)
}

type testEvaluationReviewUsagePort struct {
	store interface {
		GetEvaluationReview(context.Context, string) (operations.EvaluationReviewDTO, error)
		UpsertEvaluationReview(context.Context, operations.EvaluationReviewDTO) error
	}
}

func (p testEvaluationReviewUsagePort) RecordEvaluationReviewUsage(ctx context.Context, reviewID string, tokenConsumed int64) error {
	review, err := p.store.GetEvaluationReview(ctx, reviewID)
	if err != nil {
		return err
	}
	review.EvaluationTokenConsumed = tokenConsumed
	return p.store.UpsertEvaluationReview(ctx, review)
}

func defaultTestConversationAgentMembers() []InitialAgentMemberSeed {
	return defaultTestConversationAgentMembersFor("lead-agent")
}

func defaultTestConversationAgentMembersFor(primaryAgentID string) []InitialAgentMemberSeed {
	primaryAgentID = strings.TrimSpace(primaryAgentID)
	if primaryAgentID == "" {
		primaryAgentID = "lead-agent"
	}
	return []InitialAgentMemberSeed{
		{AgentID: primaryAgentID, Role: "Lead"},
		{AgentID: "staffing-agent", Role: "Staffing"},
	}
}

func defaultTestDiscussionBoundTools() []DiscussionAgentBoundTool {
	tools := []DiscussionAgentBoundTool{
		{ToolID: string(toolstd.MissionContextList)},
		{ToolID: string(toolstd.MissionContextSelect)},
		{ToolID: string(toolstd.MissionBriefCreate)},
		{ToolID: string(toolstd.MissionTaskCreateRoot)},
		{ToolID: string(toolstd.MissionTaskDelegate)},
		{ToolID: string(toolstd.MissionCoordinatorPlanParallel)},
		{ToolID: string(toolstd.DiscussionTalentList)},
		{ToolID: string(toolstd.DiscussionMemberAdd)},
		{ToolID: string(toolstd.DiscussionAck)},
		{ToolID: string(toolstd.DiscussionClose)},
	}
	for i := range tools {
		tools[i].RequiresPostProcess = defaultDiscussionToolPostProcess(tools[i].ToolID)
	}
	return tools
}

func TestMissionViewerMapsSystemViewerKind(t *testing.T) {
	viewer := missionViewer(ViewerRef{ViewerID: "scheduler", ViewerKind: ViewerKindSystem})

	if viewer.ViewerID != "scheduler" || string(viewer.ViewerKind) != "system" {
		t.Fatalf("mission viewer = %+v, want scheduler system viewer", viewer)
	}
}

func TestFirstHumanAgentSkipsSystemSuperadminMessage(t *testing.T) {
	human := firstHumanAgent([]MessageRecord{
		{Role: "user", AuthorID: agentcore.SystemSuperadminPrincipalID, AuthorName: "superadmin"},
		{Role: "user", AuthorID: "human-ding", AuthorName: "Ding"},
	})

	if human.AgentID != "human-ding" || human.DisplayName != "Ding" {
		t.Fatalf("first human = %+v", human)
	}
}

func testBoundTools(explicit []DiscussionAgentBoundTool, unbound bool) []DiscussionAgentBoundTool {
	if unbound {
		return nil
	}
	if explicit != nil {
		return appendDiscussionAgentBoundTools(explicit, nil)
	}
	return defaultTestDiscussionBoundTools()
}

func responseWithTestBoundTools(response DiscussionAgentResponse, explicit []DiscussionAgentBoundTool, unbound bool) DiscussionAgentResponse {
	response.BoundTools = discussionAgentBoundToolsForRequest(DiscussionAgentRequest{BoundTools: response.BoundTools}, testBoundTools(explicit, unbound))
	return response
}

func seedDiscussionMembers(t *testing.T, repo *Repository, conversationID string, userIDs ...string) {
	t.Helper()
	ctx := context.Background()
	members := []ConversationMemberRecord{
		{ConversationID: conversationID, MemberKind: "agent", MemberID: "lead-agent", DisplayName: "顾明澈", Role: "Lead", CreatedAt: testTime},
		{ConversationID: conversationID, MemberKind: "agent", MemberID: "staffing-agent", DisplayName: "秦若兰", Role: "Staffing", CreatedAt: testTime.Add(time.Nanosecond)},
	}
	for i, userID := range userIDs {
		role := "参与者"
		if i == 0 {
			role = "话题发起人"
		}
		members = append(members, ConversationMemberRecord{
			ConversationID: conversationID,
			MemberKind:     "user",
			MemberID:       userID,
			DisplayName:    testHumanDisplayName(userID),
			Role:           role,
			CreatedAt:      testTime.Add(time.Duration(i+2) * time.Nanosecond),
		})
	}
	for _, member := range members {
		if err := repo.SaveMember(ctx, member); err != nil {
			t.Fatal(err)
		}
	}
}

func mustConversationView(t *testing.T, repo *Repository, conversationID string) ConversationRecordView {
	t.Helper()
	view, err := repo.GetConversation(context.Background(), conversationID)
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func mustMessageRecord(t *testing.T, repo *Repository, conversationID string, messageID string) MessageRecord {
	t.Helper()
	view := mustConversationView(t, repo, conversationID)
	for _, message := range view.Messages {
		if message.MessageID == messageID {
			return message
		}
	}
	t.Fatalf("message %s not found in conversation %s", messageID, conversationID)
	return MessageRecord{}
}

func seedRunningDiscussionRun(t *testing.T, repo *Repository, conversationID string, runID string, at time.Time) {
	t.Helper()
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: conversationID, Title: conversationID, Status: "running", CreatedBy: "user-1", CreatedAt: at, UpdatedAt: at}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, conversationID, "user-1")
	if err := repo.SaveMember(ctx, ConversationMemberRecord{ConversationID: conversationID, MemberKind: "agent", MemberID: "lead-agent", DisplayName: "负责人", Role: "Lead", CreatedAt: at.Add(3 * time.Nanosecond)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "user-" + conversationID, ConversationID: conversationID, Role: "user", AuthorID: "user-1", AuthorName: "本地用户", Content: "开始", CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{RunID: runID, ConversationID: conversationID, AgentID: "lead-agent", Status: "running", CreatedAt: at, StartedAt: at}); err != nil {
		t.Fatal(err)
	}
}

func testHumanDisplayName(userID string) string {
	switch userID {
	case "owner-1":
		return "话题发起人"
	case "user-2":
		return "协作者"
	default:
		return "本地用户"
	}
}

type capturingDiscussionAgentRunner struct {
	agentID      string
	unboundTools bool
	boundTools   []DiscussionAgentBoundTool
	called       bool
	request      DiscussionAgentRequest
	requests     []DiscussionAgentRequest
	response     DiscussionAgentResponse
	responses    []DiscussionAgentResponse
	debugPrompt  *DiscussionAgentPromptDebug
	err          error
}

type capturingConversationArtifactCleaner struct {
	deletedConversations []string
	deletedRuns          []string
}

type capturingDiscussionRuntimeRecordCleaner struct {
	deletedRuns []string
	err         error
}

type runtimeCleaningDiscussionAgentRunner struct {
	capturingDiscussionAgentRunner
	cleaner capturingDiscussionRuntimeRecordCleaner
}

func (c *capturingConversationArtifactCleaner) DeleteConversationArtifacts(_ context.Context, conversationID string) error {
	c.deletedConversations = append(c.deletedConversations, conversationID)
	return nil
}

func (c *capturingConversationArtifactCleaner) DeleteRunArtifacts(_ context.Context, conversationID string, runID string) error {
	c.deletedRuns = append(c.deletedRuns, conversationID+"|"+runID)
	return nil
}

func (c *capturingDiscussionRuntimeRecordCleaner) DeleteDiscussionRuntimeRecord(_ context.Context, runID string) error {
	c.deletedRuns = append(c.deletedRuns, runID)
	return c.err
}

func (r *runtimeCleaningDiscussionAgentRunner) DeleteDiscussionRuntimeRecord(ctx context.Context, runID string) error {
	return r.cleaner.DeleteDiscussionRuntimeRecord(ctx, runID)
}

type anonymousDiscussionAgentRunner struct {
	called   bool
	request  DiscussionAgentRequest
	response DiscussionAgentResponse
	err      error
}

type anonymousIdentityDiscussionAgentRunner struct {
	anonymousDiscussionAgentRunner
}

type capturingDiscussionAgentMentionResponder struct {
	called       bool
	request      DiscussionAgentMentionRequest
	requests     []DiscussionAgentMentionRequest
	response     DiscussionAgentResponse
	responses    []DiscussionAgentResponse
	unboundTools bool
	boundTools   []DiscussionAgentBoundTool
	err          error
	errors       []error
}

type fixedLeadMentionLoopConfig struct {
	pairLoopLimit int
	err           error
}

type sequenceDiscussionAgentMentionResponder struct {
	requests     []DiscussionAgentMentionRequest
	responses    []DiscussionAgentResponse
	unboundTools bool
	boundTools   []DiscussionAgentBoundTool
	err          error
}

type recordingDiscussionAgentMentionResponder struct {
	mu           sync.Mutex
	requests     []DiscussionAgentMentionRequest
	responses    map[string]DiscussionAgentResponse
	unboundTools bool
	boundTools   []DiscussionAgentBoundTool
	err          error
}

type barrierDiscussionAgentMentionResponder struct {
	mu           sync.Mutex
	requests     []DiscussionAgentMentionRequest
	responses    map[string]DiscussionAgentResponse
	unboundTools bool
	boundTools   []DiscussionAgentBoundTool
	err          error
	waitFor      int
	ready        chan struct{}
	released     bool
}

type blockingDiscussionAgentRunner struct {
	mu             sync.Mutex
	requests       []DiscussionAgentRequest
	streamRequests []DiscussionAgentRequest
	streamEvents   <-chan DiscussionAgentStreamEvent
	response       DiscussionAgentResponse
	unboundTools   bool
	boundTools     []DiscussionAgentBoundTool
	entered        chan struct{}
	streamStarted  chan struct{}
	release        chan struct{}
	once           sync.Once
	streamOnce     sync.Once
	releaseOnce    sync.Once
}

type firstRunBlockingDiscussionAgentRunner struct {
	mu        sync.Mutex
	requests  []DiscussionAgentRequest
	entered   chan struct{}
	cancelled chan struct{}
	once      sync.Once
	response  DiscussionAgentResponse
}

type ignoringCancelFirstRunDiscussionAgentRunner struct {
	mu       sync.Mutex
	requests []DiscussionAgentRequest
	entered  chan struct{}
	release  chan struct{}
	response DiscussionAgentResponse
}

func newBlockingDiscussionAgentRunner(response DiscussionAgentResponse) *blockingDiscussionAgentRunner {
	return &blockingDiscussionAgentRunner{
		response:      response,
		entered:       make(chan struct{}),
		streamStarted: make(chan struct{}),
		release:       make(chan struct{}),
	}
}

func newFirstRunBlockingDiscussionAgentRunner(response DiscussionAgentResponse) *firstRunBlockingDiscussionAgentRunner {
	return &firstRunBlockingDiscussionAgentRunner{
		entered:   make(chan struct{}),
		cancelled: make(chan struct{}),
		response:  response,
	}
}

func newIgnoringCancelFirstRunDiscussionAgentRunner(response DiscussionAgentResponse) *ignoringCancelFirstRunDiscussionAgentRunner {
	return &ignoringCancelFirstRunDiscussionAgentRunner{
		entered:  make(chan struct{}),
		release:  make(chan struct{}),
		response: response,
	}
}

func (r *capturingDiscussionAgentMentionResponder) RespondToDiscussionMention(_ context.Context, request DiscussionAgentMentionRequest) (DiscussionAgentResponse, error) {
	r.called = true
	r.request = request
	r.requests = append(r.requests, request)
	err := r.err
	if len(r.errors) > 0 {
		err = r.errors[0]
		r.errors = r.errors[1:]
	}
	if len(r.responses) > 0 {
		response := r.responses[0]
		r.responses = r.responses[1:]
		return responseWithTestBoundTools(response, r.boundTools, r.unboundTools), err
	}
	return responseWithTestBoundTools(r.response, r.boundTools, r.unboundTools), err
}

func (c fixedLeadMentionLoopConfig) DiscussionAgentPairLoopLimit(context.Context) (int, error) {
	return c.pairLoopLimit, c.err
}

func (r *sequenceDiscussionAgentMentionResponder) RespondToDiscussionMention(_ context.Context, request DiscussionAgentMentionRequest) (DiscussionAgentResponse, error) {
	r.requests = append(r.requests, request)
	if r.err != nil {
		return DiscussionAgentResponse{}, r.err
	}
	if len(r.responses) == 0 {
		return DiscussionAgentResponse{}, nil
	}
	response := r.responses[0]
	r.responses = r.responses[1:]
	return responseWithTestBoundTools(response, r.boundTools, r.unboundTools), nil
}

func (r *recordingDiscussionAgentMentionResponder) RespondToDiscussionMention(_ context.Context, request DiscussionAgentMentionRequest) (DiscussionAgentResponse, error) {
	r.mu.Lock()
	r.requests = append(r.requests, request)
	response := r.responses[strings.TrimSpace(request.TargetAgent.ID)]
	err := r.err
	r.mu.Unlock()
	return responseWithTestBoundTools(response, r.boundTools, r.unboundTools), err
}

func (r *recordingDiscussionAgentMentionResponder) Requests() []DiscussionAgentMentionRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]DiscussionAgentMentionRequest(nil), r.requests...)
}

func (r *barrierDiscussionAgentMentionResponder) RespondToDiscussionMention(_ context.Context, request DiscussionAgentMentionRequest) (DiscussionAgentResponse, error) {
	r.mu.Lock()
	if r.ready == nil {
		r.ready = make(chan struct{})
	}
	ready := r.ready
	r.requests = append(r.requests, request)
	if !r.released && len(r.requests) >= r.waitFor {
		r.released = true
		close(ready)
	}
	response := r.responses[strings.TrimSpace(request.TargetAgent.ID)]
	response = responseWithTestBoundTools(response, r.boundTools, r.unboundTools)
	err := r.err
	r.mu.Unlock()

	select {
	case <-ready:
	case <-time.After(100 * time.Millisecond):
		return DiscussionAgentResponse{}, fmt.Errorf("mention batch was not started in parallel")
	}
	return response, err
}

func (r *barrierDiscussionAgentMentionResponder) Requests() []DiscussionAgentMentionRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]DiscussionAgentMentionRequest(nil), r.requests...)
}

func (r *capturingDiscussionAgentRunner) RunDiscussion(_ context.Context, request DiscussionAgentRequest) (DiscussionAgentResponse, error) {
	r.called = true
	r.request = request
	r.requests = append(r.requests, request)
	if len(r.responses) > 0 {
		response := r.responses[0]
		r.responses = r.responses[1:]
		return responseWithTestBoundTools(response, r.boundTools, r.unboundTools), r.err
	}
	return responseWithTestBoundTools(r.response, r.boundTools, r.unboundTools), r.err
}

func (r *anonymousDiscussionAgentRunner) RunDiscussion(_ context.Context, request DiscussionAgentRequest) (DiscussionAgentResponse, error) {
	r.called = true
	r.request = request
	return r.response, r.err
}

func (r *anonymousIdentityDiscussionAgentRunner) DiscussionAgentID() string {
	return "lead-agent"
}

func (r *blockingDiscussionAgentRunner) RunDiscussion(ctx context.Context, request DiscussionAgentRequest) (DiscussionAgentResponse, error) {
	r.mu.Lock()
	r.requests = append(r.requests, request)
	r.mu.Unlock()
	r.once.Do(func() { close(r.entered) })
	select {
	case <-ctx.Done():
		return DiscussionAgentResponse{}, ctx.Err()
	case <-r.release:
		return r.response, nil
	}
}

func (r *firstRunBlockingDiscussionAgentRunner) RunDiscussion(ctx context.Context, request DiscussionAgentRequest) (DiscussionAgentResponse, error) {
	r.mu.Lock()
	r.requests = append(r.requests, request)
	count := len(r.requests)
	r.mu.Unlock()
	if count > 1 {
		return r.response, nil
	}
	r.once.Do(func() { close(r.entered) })
	<-ctx.Done()
	close(r.cancelled)
	return DiscussionAgentResponse{}, ctx.Err()
}

func (r *ignoringCancelFirstRunDiscussionAgentRunner) RunDiscussion(_ context.Context, request DiscussionAgentRequest) (DiscussionAgentResponse, error) {
	r.mu.Lock()
	r.requests = append(r.requests, request)
	count := len(r.requests)
	r.mu.Unlock()
	if count > 1 {
		return r.response, nil
	}
	close(r.entered)
	<-r.release
	return DiscussionAgentResponse{AssistantMessage: "旧回复不应复活", Status: "completed", RuntimeKind: "managed"}, nil
}

func (r *blockingDiscussionAgentRunner) RunDiscussionStream(_ context.Context, request DiscussionAgentRequest) (<-chan DiscussionAgentStreamEvent, error) {
	r.mu.Lock()
	r.streamRequests = append(r.streamRequests, request)
	streamEvents := r.streamEvents
	r.mu.Unlock()
	r.streamOnce.Do(func() { close(r.streamStarted) })
	if streamEvents != nil {
		return streamEvents, nil
	}
	closed := make(chan DiscussionAgentStreamEvent)
	close(closed)
	return closed, nil
}

func (r *blockingDiscussionAgentRunner) Release() {
	r.releaseOnce.Do(func() { close(r.release) })
}

func (r *blockingDiscussionAgentRunner) DiscussionAgentID() string {
	return "lead-agent"
}

func (r *blockingDiscussionAgentRunner) DiscussionAgentBoundTools() []DiscussionAgentBoundTool {
	return testBoundTools(r.boundTools, r.unboundTools)
}

func (r *firstRunBlockingDiscussionAgentRunner) DiscussionAgentID() string {
	return "lead-agent"
}

func (r *ignoringCancelFirstRunDiscussionAgentRunner) DiscussionAgentID() string {
	return "lead-agent"
}

func (r *blockingDiscussionAgentRunner) RequestCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func (r *firstRunBlockingDiscussionAgentRunner) RequestCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func (r *ignoringCancelFirstRunDiscussionAgentRunner) RequestCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func (r *blockingDiscussionAgentRunner) StreamRequestCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.streamRequests)
}

func sameConversationMembers(got []ConversationMemberDTO, want []ConversationMemberDTO) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func containsDiscussionTool(tools []DiscussionAgentBoundTool, toolID string) bool {
	toolID = strings.TrimSpace(toolID)
	for _, tool := range tools {
		if strings.TrimSpace(tool.ToolID) == toolID || strings.TrimSpace(tool.Alias) == toolID {
			return true
		}
	}
	return false
}

func (r *capturingDiscussionAgentRunner) DiscussionAgentID() string {
	if strings.TrimSpace(r.agentID) == "" {
		return "lead-agent"
	}
	return r.agentID
}

func (r *capturingDiscussionAgentRunner) PromptDebug(request DiscussionAgentRequest) *DiscussionAgentPromptDebug {
	r.request = request
	return r.debugPrompt
}

func (r *capturingDiscussionAgentRunner) DiscussionAgentBoundTools() []DiscussionAgentBoundTool {
	return testBoundTools(r.boundTools, r.unboundTools)
}

type streamingDiscussionAgentRunner struct {
	agentID      string
	request      DiscussionAgentRequest
	events       []DiscussionAgentStreamEvent
	response     DiscussionAgentResponse
	boundTools   []DiscussionAgentBoundTool
	unboundTools bool
	err          error
}
type anonymousStreamingDiscussionAgentRunner struct {
	request DiscussionAgentRequest
	events  []DiscussionAgentStreamEvent
	err     error
}

type delayedStreamingDiscussionAgentRunner struct {
	agentID      string
	unboundTools bool
	boundTools   []DiscussionAgentBoundTool
	request      DiscussionAgentRequest
	streamEvents chan DiscussionAgentStreamEvent
	response     DiscussionAgentResponse
	err          error
}

func (r *streamingDiscussionAgentRunner) RunDiscussion(_ context.Context, request DiscussionAgentRequest) (DiscussionAgentResponse, error) {
	r.request = request
	return responseWithTestBoundTools(r.response, r.boundTools, r.unboundTools), r.err
}

func (r *streamingDiscussionAgentRunner) RunDiscussionStream(_ context.Context, request DiscussionAgentRequest) (<-chan DiscussionAgentStreamEvent, error) {
	r.request = request
	if r.err != nil {
		return nil, r.err
	}
	events := make(chan DiscussionAgentStreamEvent, len(r.events))
	for _, event := range r.events {
		if event.Kind == DiscussionAgentStreamRunCompleted {
			event.Response = responseWithTestBoundTools(event.Response, r.boundTools, r.unboundTools)
		}
		events <- event
	}
	close(events)
	return events, nil
}

func (r *streamingDiscussionAgentRunner) DiscussionAgentID() string {
	if strings.TrimSpace(r.agentID) == "" {
		return "lead-agent"
	}
	return r.agentID
}

func (r *streamingDiscussionAgentRunner) DiscussionAgentBoundTools() []DiscussionAgentBoundTool {
	return testBoundTools(r.boundTools, r.unboundTools)
}

func (r *anonymousStreamingDiscussionAgentRunner) RunDiscussion(_ context.Context, request DiscussionAgentRequest) (DiscussionAgentResponse, error) {
	r.request = request
	return DiscussionAgentResponse{}, r.err
}

func (r *anonymousStreamingDiscussionAgentRunner) RunDiscussionStream(_ context.Context, request DiscussionAgentRequest) (<-chan DiscussionAgentStreamEvent, error) {
	r.request = request
	if r.err != nil {
		return nil, r.err
	}
	events := make(chan DiscussionAgentStreamEvent, len(r.events))
	for _, event := range r.events {
		events <- event
	}
	close(events)
	return events, nil
}

func (r *delayedStreamingDiscussionAgentRunner) RunDiscussion(_ context.Context, request DiscussionAgentRequest) (DiscussionAgentResponse, error) {
	r.request = request
	return responseWithTestBoundTools(r.response, r.boundTools, r.unboundTools), r.err
}

func (r *delayedStreamingDiscussionAgentRunner) RunDiscussionStream(_ context.Context, request DiscussionAgentRequest) (<-chan DiscussionAgentStreamEvent, error) {
	r.request = request
	if r.err != nil {
		return nil, r.err
	}
	return r.streamEvents, nil
}

func (r *delayedStreamingDiscussionAgentRunner) DiscussionAgentID() string {
	if strings.TrimSpace(r.agentID) == "" {
		return "lead-agent"
	}
	return r.agentID
}

func (r *delayedStreamingDiscussionAgentRunner) DiscussionAgentBoundTools() []DiscussionAgentBoundTool {
	return testBoundTools(r.boundTools, r.unboundTools)
}
