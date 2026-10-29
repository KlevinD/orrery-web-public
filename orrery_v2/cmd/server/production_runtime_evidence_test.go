package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/improvement"
	"orrery_v2/internal/platform/db"
)

func TestProductionRuntimeEvidenceScopes(t *testing.T) {
	for _, tt := range []struct {
		name, tenant, mission, scheduled, want string
		invalid                                bool
	}{
		{"organization", "org-a", "", "", "org-a", false},
		{"mission", "org-a", "mission-a", "", "mission:org-a/mission-a", false},
		{"already mission", "mission:org-a/mission-a", "mission-a", "", "mission:org-a/mission-a", false},
		{"scheduled", "org-a", "mission-a", "schedule-a", "org-a", false},
		{"wrong mission", "mission:org-a/mission-b", "mission-a", "", "", true},
		{"missing tenant", "", "mission-a", "", "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := agentcore.RuntimeRequest{TenantID: tt.tenant, RunID: "run-a", TaskID: "task-a", MissionID: tt.mission, RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{"scheduled_task_id": tt.scheduled}}}
			scope, err := productionRuntimeEvidenceScope(req)
			if tt.invalid {
				if err == nil {
					t.Fatal("invalid scope accepted")
				}
				return
			}
			if err != nil || scope.TenantID != tt.want || scope.Source.TenantID != tt.want || scope.Source.ID != "run-a" || scope.Run.ID != ontologyAgentRunID("run-a") {
				t.Fatalf("scope %+v %v", scope, err)
			}
		})
	}
}

func TestProductionRuntimeEvidenceTracksResolvedBaseAgentRequest(t *testing.T) {
	for _, scenario := range []string{"success", "capture failure", "post-commit failure"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			dbConfig := db.Config{Driver: db.DriverFile, URL: "file://" + filepath.Join(t.TempDir(), "evidence.db")}
			core, err := db.Open(ctx, dbConfig)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = core.Close() })
			journalRoot := t.TempDir()
			journal, err := improvement.NewFileRuntimeFeedbackJournal(journalRoot)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = journal.Close() })
			deps := newProductionDiscussionAgentDependencies(nil, productionTraceStore{store: nil, evidenceCore: core, evidenceJournal: journal}, nil)
			// Use the same production factory with a storage implementation that
			// writes trace objects without requiring a running file service.
			deps.TraceRecorder = productionRuntimeEvidenceRecorder(agentcore.StoreTraceRecorder{Store: runtimeEvidenceTraceObjects{}}, productionTraceStore{evidenceCore: core, evidenceJournal: journal})
			if _, ok := deps.TraceRecorder.(improvement.RuntimeEvidenceRecorder); !ok {
				t.Fatal("production RSI recorder missing")
			}
			evidence := improvement.NewRuntimeEvidenceStore(core)
			provider := &runtimeEvidenceProvider{invoke: func(req agentcore.RuntimeRequest) {
				if req.RuntimeOptions.MaxTurns != 17 {
					t.Fatal("diary preparation was not captured")
				}
				page, err := core.ListRecords(ctx, "ontology_improvement_runtime_evidence", "", 10)
				if err != nil || len(page.Items) != 1 {
					t.Fatalf("provider invoked without durable request evidence: %v", err)
				}
				var captured improvement.RuntimeRequestEvidence
				if err := json.Unmarshal([]byte(page.Items[0].Record["payload"].(string)), &captured); err != nil {
					t.Fatal(err)
				}
				scope, err := productionRuntimeEvidenceScope(req)
				if err != nil {
					t.Fatal(err)
				}
				expected, err := improvement.BuildRuntimeRequestEvidence(req, scope, captured.AttemptID, captured.TraceRef, captured.CapturedAt)
				if err != nil || captured.ID != expected.ID {
					t.Fatalf("manifest differs from provider request: %v", err)
				}
				if scenario == "post-commit failure" {
					_ = core.Close()
				}
			}}
			delivery := &runtimeEvidenceDelivery{}
			agent := agentcore.NewBaseAgent(agentcore.BaseAgentConfig{
				Identity: agentcore.AgentIdentity{AgentID: "agent-a", AgentProfileID: "profile-a", InstanceRef: agentcore.AgentInstanceRef{ID: "instance-a"}},
				Profile:  agentcore.AgentProfile{ID: "profile-a", RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}},
				Runtime:  provider, Trace: deps.TraceRecorder, Delivery: delivery, Notifier: agentcore.NoopNotifier{}, DiaryFlush: runtimeEvidencePreparation{},
				Validator: agentcore.ResultValidatorFunc(func(agentcore.AgentInput, agentcore.AgentResult) error { return nil }),
			})
			input := agentcore.AgentInput{TenantID: "org-a", RunID: "run-a", Task: agentcore.TaskInput{ID: "task-a", MissionID: "mission-a", Goal: "deliver result"}, ContextPack: agentcore.ContextPack{
				ID: "context-a", AgentID: "agent-a", TaskID: "task-a", MissionID: "mission-a", DiscussionGroupID: "discussion-a", MissionFinalGoal: "deliver result", DiscussionGroupGoal: "deliver result", WorkerPersonalGoal: "deliver result", TaskContractRef: agentcore.TaskContractRef{ID: "task-a"}, OverridePolicy: agentcore.OverridePolicyPreserveProtected,
				SourceRefs: []agentcore.ContextSourceRef{{Kind: agentcore.ContextSourceKindTaskContract, Ref: agentcore.ContextSourceTypedRef{TaskContractRef: &agentcore.TaskContractRef{ID: "task-a"}}, Priority: agentcore.SourcePriorityContract, Immutable: true}},
			}}
			if scenario == "capture failure" {
				_ = core.Close()
			}
			_, err = agent.Handle(ctx, input)
			if scenario == "capture failure" {
				if err == nil || provider.calls != 0 || delivery.commits != 0 {
					t.Fatalf("capture error allowed dispatch: %v", err)
				}
				return
			}
			if scenario == "post-commit failure" {
				var post agentcore.PostCommitPipelineError
				if !errors.As(err, &post) || post.Stage != "durable_trace_finalize" || provider.calls != 1 || delivery.commits != 1 {
					t.Fatalf("post-commit semantics lost: %v", err)
				}
				reopened, err := db.Open(ctx, dbConfig)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				recovery := &productionRuntimeFeedbackRecovery{store: improvement.NewRuntimeEvidenceStore(reopened), journal: journal, limit: 10}
				defer recovery.Close()
				if err := recovery.Sweep(ctx); err != nil {
					t.Fatal("production recovery failed", err)
				}
				cases, err := improvement.NewStore(reopened).List(ctx, "mission:org-a/mission-a", "", 10)
				if err != nil || len(cases.Items) != 1 || cases.Items[0].State != improvement.Observed {
					t.Fatalf("recovered feedback missing: %+v %v", cases, err)
				}
				if provider.calls != 1 || delivery.commits != 1 {
					t.Fatal("recovery repeated business execution")
				}
				files, err := os.ReadDir(journalRoot)
				if err != nil || len(files) != 0 {
					t.Fatalf("recovered terminal not acknowledged: %d %v", len(files), err)
				}
				return
			}
			if err != nil || provider.calls != 1 || delivery.commits != 1 {
				t.Fatalf("runtime pipeline: %v", err)
			}
			page, err := improvement.NewStore(core).List(ctx, "mission:org-a/mission-a", "", 10)
			if err != nil || len(page.Items) != 1 {
				t.Fatalf("terminal not observed: %+v %v", page, err)
			}
			c := page.Items[0]
			if delivery.result.RuntimeAttemptID == "" || delivery.result.RuntimeAttemptID != c.Observation.RelatedObjects[0].ID {
				t.Fatal("formal result did not retain the host runtime attempt")
			}
			end, err := evidence.GetTerminal(ctx, c.TenantID, c.Observation.RelatedObjects[0].ID)
			if err != nil || end.Status != agentcore.RuntimeStatusCompleted || c.State != improvement.Observed {
				t.Fatalf("terminal status: %+v %v", end, err)
			}
		})
	}
}

func TestProductionRuntimeFeedbackDisabledModeDoesNotCreateJournal(t *testing.T) {
	root := filepath.Join(t.TempDir(), "must-not-exist")
	journal, err := newProductionRuntimeFeedbackJournal(root, true)
	if err != nil || journal != nil {
		t.Fatalf("disabled journal is not a nil interface: %v %v", journal, err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("disabled background mode wrote to disk: %v", err)
	}
}

type runtimeEvidenceTraceObjects struct{}

func (runtimeEvidenceTraceObjects) PutTrace(_ context.Context, key string, _ []byte) (string, error) {
	return "artifact://" + key, nil
}

type runtimeEvidencePreparation struct{}

func (runtimeEvidencePreparation) PrepareRuntimeRequest(_ context.Context, req agentcore.RuntimeRequest) agentcore.RuntimeRequest {
	req.RuntimeOptions.MaxTurns = 17
	return req
}
func (runtimeEvidencePreparation) FlushTerminal(context.Context, agentcore.DiaryFlushRequest) agentcore.DiaryFlushOutcome {
	return agentcore.DiaryFlushOutcome{Status: agentcore.DiaryFlushSkipped}
}

type runtimeEvidenceDelivery struct {
	commits int
	result  agentcore.AgentResult
}

func (s *runtimeEvidenceDelivery) SubmitAgentResult(_ context.Context, result agentcore.AgentResult, _ agentcore.CommitOptions) error {
	s.commits++
	s.result = result
	return nil
}

type runtimeEvidenceProvider struct {
	calls  int
	invoke func(agentcore.RuntimeRequest)
}

func (r *runtimeEvidenceProvider) RunSession(_ context.Context, req agentcore.RuntimeRequest, _ agentcore.SessionEventObserver) (agentcore.RuntimeResponse, error) {
	r.calls++
	r.invoke(req)
	return agentcore.RuntimeResponse{RunID: req.RunID, Status: agentcore.RuntimeStatusCompleted, Messages: []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Content: "completed"}}}, nil
}
func (*runtimeEvidenceProvider) CancelSession(context.Context, agentcore.AgentRunID, string) error {
	return nil
}
func (*runtimeEvidenceProvider) SteerSession(context.Context, agentcore.AgentRunID, string) error {
	return nil
}
func (*runtimeEvidenceProvider) PrewarmSession(context.Context, agentcore.RuntimeRequest) error {
	return nil
}
func (*runtimeEvidenceProvider) ReleaseRuntimeWorkspace(context.Context, string) error { return nil }
func (*runtimeEvidenceProvider) Capabilities(context.Context) agentcore.Capabilities {
	return agentcore.Capabilities{Signals: agentcore.SignalSet{ToolLifecycle: true, TerminalResult: true}}
}
