package improvement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

type replayTestModel struct {
	identity GeneratorIdentity
	calls    int
	err      error
	request  GenerationInput
}

func (m *replayTestModel) Identity() GeneratorIdentity { return m.identity }
func (m *replayTestModel) Generate(_ context.Context, input GenerationInput) (CodeProposal, error) {
	m.calls++
	m.request = input
	if m.err != nil {
		return CodeProposal{}, m.err
	}
	edits := []CandidateEdit{}
	for _, target := range input.Targets {
		edits = append(edits, CandidateEdit{Target: ResourceKey(target.Resource), Content: "candidate " + string(target.Resource.Kind)})
	}
	return CodeProposal{Diagnosis: "任务处理和工具选择共同造成失败", Edits: edits}, nil
}

// This double validates host coordination only. Real Docker/model evidence is
// collected separately; these synthetic outputs are not optimization evidence.
type replayTestRunner struct {
	calls         int
	failCandidate bool
	failBaseline  bool
}

func (r *replayTestRunner) Run(_ context.Context, sources []SourceFile, p ReplayPolicy, task ReplayTask) (TaskExecution, error) {
	r.calls++
	inputHash, _ := canonicalJSON(task.Input)
	output := string(task.Expected)
	if task.Split == "repair" && !strings.HasPrefix(sources[0].Content, "candidate") {
		output = `{"ok":false}`
	}
	if r.failCandidate && strings.HasPrefix(sources[0].Content, "candidate") && task.Split == "held_out" {
		output = `{"ok":false}`
	}
	if r.failBaseline && !strings.HasPrefix(sources[0].Content, "candidate") {
		output = `{"ok":false}`
	}
	return TaskExecution{TaskID: task.ID, InputHash: inputHash, SourceHash: Digest(sources), PolicyHash: Digest(p), ContainerID: Digest(fmt.Sprintf("container:%d", r.calls)), Output: output, ExitCode: 0, DurationMS: 10}, nil
}

func replayTestSpec() ReplaySpec {
	s := ReplaySpec{Observation: testObservation("code-replay"), Goal: Goal{Metric: "task_pass_rate", Direction: "higher", MinimumGain: .1, MaxCost: 10, MaxDurationMS: 10000}, Budget: 10,
		ExecutorSHA256: Digest("test-executor"),
		Sources:        []SourceFile{{Path: "loop.go", Content: "baseline agent_loop"}, {Path: "tool.go", Content: "baseline tool"}},
		Policy:         ReplayPolicy{Image: "sha256:" + Digest("image"), Entrypoint: "/usr/local/bin/driver", TimeoutMS: 1000, ModelTimeoutMS: 1000, MemoryMB: 128, CPUs: 1, MaxOutputBytes: 4096, ModelCallUnits: 1, TaskRunUnits: 1},
		Generator:      GeneratorIdentity{ProfileID: "test-profile", Model: "test-model", Endpoint: "https://model.invalid/v1", Version: "test-v1", PromptSHA256: Digest("test-prompt")}}
	for i, kind := range []TargetKind{TargetAgentLoop, TargetTool} {
		s.Targets = append(s.Targets, ReplayTarget{Resource: ResourceVersion{Kind: kind, ID: string(kind), Version: "v0", SHA256: Digest(s.Sources[i].Content)}, Path: s.Sources[i].Path})
	}
	for i, split := range []string{"repair", "regression", "held_out"} {
		s.Tasks = append(s.Tasks, ReplayTask{ID: split, Input: json.RawMessage(fmt.Sprintf(`{"input":%d}`, i)), Expected: json.RawMessage(`{"ok":true}`), Split: split, Safety: true, Business: true})
	}
	return s
}

func replayTestWorker(t *testing.T) (*ReplayCoordinator, *replayTestModel, *replayTestRunner, ReplaySpec) {
	t.Helper()
	store := testStore(t)
	vault, err := NewArtifactVault(store.core, []byte(strings.Repeat("test-seal-key", 4)))
	if err != nil {
		t.Fatal(err)
	}
	spec := replayTestSpec()
	model := &replayTestModel{identity: spec.Generator}
	runner := &replayTestRunner{}
	return &ReplayCoordinator{Store: store, Vault: vault, Model: model, Runner: runner}, model, runner, spec
}

func TestReplayCoordinatorExecutesBothArmsAndIndependentOracle(t *testing.T) {
	w, model, runner, spec := replayTestWorker(t)
	c, err := w.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if c.State != Evaluated || c.Evaluation.CandidateScore != 1 || c.Evaluation.BaselineScore != 2.0/3 || c.Evaluation.Cost != 7 || len(c.ChangeSet.Changes) != 2 || runner.calls != 6 || model.calls != 1 {
		t.Fatalf("case=%+v, executions=%d, calls=%d", c, runner.calls, model.calls)
	}
	if len(model.request.Failures) != 1 || model.request.Failures[0].ID != "repair" {
		t.Fatalf("hidden fixtures leaked: %+v", model.request)
	}
	if _, err := w.Run(context.Background(), spec); err != nil || runner.calls != 6 || model.calls != 1 {
		t.Fatalf("completed replay repeated operations: %v", err)
	}
	changed := privateCopy(spec)
	changed.Tasks[2].Expected = json.RawMessage(`{"ok":false}`)
	if _, err := w.Run(context.Background(), changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("mutable oracle accepted: %v", err)
	}
	if err := NewReplayVerifier(w.Vault).Verify(context.Background(), c, Transition{To: Monitoring}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("experiment granted apply authority: %v", err)
	}
}

func TestReplayRejectsHeldOutRegressionAndRecomputesScores(t *testing.T) {
	w, _, runner, spec := replayTestWorker(t)
	runner.failCandidate = true
	c, err := w.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if c.State != Rejected || c.Evaluation.RegressionCount != 1 || c.Evaluation.HeldOutPassed || c.Evaluation.SafetyPassed || c.Evaluation.BusinessConstraintsPassed {
		t.Fatalf("regressed candidate accepted: %+v", c)
	}
	forged := *c.Evaluation
	forged.CandidateScore = 1
	forged.HeldOutPassed = true
	if err := NewReplayVerifier(w.Vault).Verify(context.Background(), c, Transition{To: Evaluated, Evaluation: &forged}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("self-scored candidate accepted: %v", err)
	}
}

func TestReplayIndeterminateModelCallDoesNotSpendAgain(t *testing.T) {
	w, model, runner, spec := replayTestWorker(t)
	model.err = errors.New("provider disconnected after request")
	if _, err := w.Run(context.Background(), spec); err == nil {
		t.Fatal("missing model receipt accepted")
	}
	w.Store.now = func() time.Time { return time.Now().Add(11 * time.Minute) }
	if _, err := w.Run(context.Background(), spec); !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "indeterminate") {
		t.Fatalf("unknown call repeated: %v", err)
	}
	if model.calls != 1 || runner.calls != 3 {
		t.Fatalf("budget reservation repeated: model=%d, runner=%d", model.calls, runner.calls)
	}
}

func TestReplayDoesNotCallModelBeforeBaselineControlRuns(t *testing.T) {
	w, model, runner, spec := replayTestWorker(t)
	runner.failBaseline = true
	_, err := w.Run(context.Background(), spec)
	if !errors.Is(err, ErrUnavailable) || model.calls != 0 || runner.calls != len(spec.Tasks) {
		t.Fatalf("broken driver triggered model generation: calls=%d runs=%d err=%v", model.calls, runner.calls, err)
	}
}

func TestReplayFencingRejectsStaleExternalOperationReceipt(t *testing.T) {
	w, _, _, spec := replayTestWorker(t)
	ctx := context.Background()
	c, err := w.Store.Observe(ctx, spec.Observation)
	if err != nil {
		t.Fatal(err)
	}
	c, err = w.Store.Claim(ctx, c.TenantID, c.ID, "worker-a", c.Revision, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.step(ctx, c, Digest(spec), "fenced-operation", func() (ArtifactRef, error) {
		w.Store.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
		if _, err := w.Store.Claim(ctx, c.TenantID, c.ID, "worker-b", c.Revision, time.Minute); err != nil {
			t.Fatal(err)
		}
		return w.Vault.put(ctx, c.TenantID, "task-receipt", "stale completion", true)
	})
	if !errors.Is(err, ErrLease) {
		t.Fatalf("old worker completed a reserved operation after takeover: %v", err)
	}
	record, exists, err := w.Store.core.GetRecord(ctx, replayWorkTable, caseKey(c.TenantID, c.ID)+"/fenced-operation")
	if err != nil || !exists {
		t.Fatalf("missing reservation: %v", err)
	}
	checkpoint, err := decode[replayCheckpoint](record)
	if err != nil || artifactOK(checkpoint.Result) {
		t.Fatalf("stale receipt entered durable checkpoint: %+v %v", checkpoint, err)
	}
}

func TestReplayArtifactsRequireTenantDigestAndExecutorSeal(t *testing.T) {
	w, _, _, spec := replayTestWorker(t)
	c, err := w.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	var proof replayProof
	if err := w.Vault.read(context.Background(), "other-tenant", c.Experiment.Result, "replay-proof", true, &proof); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign artifact readable: %v", err)
	}
	if err := w.Vault.read(context.Background(), c.TenantID, c.Experiment.Result, "replay-proof", true, &proof); err != nil {
		t.Fatal(err)
	}
	unsigned, err := w.Vault.put(context.Background(), c.TenantID, "replay-proof", proof, false)
	if err != nil {
		t.Fatal(err)
	}
	forged := *c.Experiment
	forged.Result = unsigned
	forged.ID = "code-experiment:" + unsigned.SHA256
	if err := NewReplayVerifier(w.Vault).Verify(context.Background(), c, Transition{To: Tested, Experiment: &forged}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unsigned model proof accepted: %v", err)
	}
	wrongKey, _ := NewArtifactVault(w.Store.core, []byte(strings.Repeat("different-key", 4)))
	if err := NewReplayVerifier(wrongKey).Verify(context.Background(), c, Transition{To: Tested, Experiment: c.Experiment}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wrong executor key accepted: %v", err)
	}
	ref := c.Experiment.Result
	record, _, _ := w.Store.core.GetRecord(context.Background(), artifactTable, tenantPrefix(c.TenantID)+ref.SHA256)
	record["sha256"] = Digest("tampered")
	if err := w.Store.core.PutRecord(context.Background(), artifactTable, tenantPrefix(c.TenantID)+ref.SHA256, record); err != nil {
		t.Fatal(err)
	}
	if err := NewReplayVerifier(w.Vault).Verify(context.Background(), c, Transition{To: Tested, Experiment: c.Experiment}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mutated evidence accepted: %v", err)
	}
}

func TestReplayAdmissionRejectsMutableTargetsAndBudgetOverrun(t *testing.T) {
	for name, mutate := range map[string]func(*ReplaySpec){
		"path escape":              func(s *ReplaySpec) { s.Sources[0].Path = "../production.go" },
		"same path different case": func(s *ReplaySpec) { s.Sources[1].Path = "LOOP.go" },
		"unknown observed version": func(s *ReplaySpec) {
			s.Observation.Dependencies = []ResourceVersion{{Kind: TargetTool, ID: "missing", Version: "v2"}}
		},
		"wrong frozen source":         func(s *ReplaySpec) { s.Targets[0].Resource.SHA256 = Digest("different source") },
		"mutable image":               func(s *ReplaySpec) { s.Policy.Image = "golang:latest" },
		"insufficient budget":         func(s *ReplaySpec) { s.Budget = 6 },
		"aggregate output budget":     func(s *ReplaySpec) { s.Policy.MaxOutputBytes = 1 << 20 },
		"holdout input contamination": func(s *ReplaySpec) { s.Tasks[2].Input = s.Tasks[0].Input },
		"no safety oracle": func(s *ReplaySpec) {
			for i := range s.Tasks {
				s.Tasks[i].Safety = false
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := replayTestSpec()
			mutate(&s)
			if err := validateReplaySpec(s); err == nil {
				t.Fatal("unsafe/incomplete replay admitted")
			}
		})
	}
	s := replayTestSpec()
	if _, err := proposalSources(s, CodeProposal{Diagnosis: "reason", Edits: []CandidateEdit{{Target: "ontology:not-admitted", Content: "malicious"}}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unadmitted edit accepted: %v", err)
	}
}

func TestReplayOracleRejectsScoreClaimsInvalidOutputAndForgedRuns(t *testing.T) {
	s := replayTestSpec()
	task := s.Tasks[0]
	for _, run := range []TaskExecution{{Output: `{"score":1,"passed":true}`}, {Output: `{"ok":true}`, TimedOut: true}, {Output: `{"ok":true}`, Truncated: true}, {Output: `{"ok":true}`, ExitCode: 1}, {Output: `{"ok":true} garbage`}} {
		if taskPassed(task, run) {
			t.Fatalf("false success accepted: %+v", run)
		}
	}
	w, _, _, _ := replayTestWorker(t)
	c, err := w.Run(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	var proof replayProof
	if err := w.Vault.read(context.Background(), c.TenantID, c.Experiment.Result, "replay-proof", true, &proof); err != nil {
		t.Fatal(err)
	}
	proof.Candidate.Executions[0].ContainerID = proof.Baseline.Executions[0].ContainerID
	if _, err := evaluateReplay(s, proof, *c.Experiment); !errors.Is(err, ErrInvalid) {
		t.Fatalf("reused baseline receipt accepted: %v", err)
	}
}

func TestDockerReplayRunsWithoutHostCredentialsOrWritableSources(t *testing.T) {
	image := os.Getenv("ORRERY_TEST_REPLAY_IMAGE")
	if image == "" {
		t.Skip("requires an explicitly pinned disposable Docker image")
	}
	root := t.TempDir()
	runner := &DockerTaskRunner{WorkRoot: root}
	// Standard /bin/sh image; no oracle is mounted. Verify the source boundary,
	// network namespace and environment from inside an actual candidate process.
	sources := []SourceFile{{Path: "task.sh", Content: "#!/bin/sh\nread input\nif touch /src/forbidden 2>/dev/null; then exit 10; fi\nif test -n \"$ORRERY_RSI_MODEL_API_KEY\"; then exit 11; fi\nif test -S /var/run/docker.sock; then exit 12; fi\nif test -f /src/expected.json; then exit 13; fi\nif test $(wc -l < /proc/net/route) -gt 1; then exit 14; fi\nprintf '{\"ok\":true}'\n"}}
	p := replayTestSpec().Policy
	p.Image = image
	p.Entrypoint = "/bin/sh"
	p.Args = []string{"/src/task.sh"}
	p.TimeoutMS = 5000
	task := ReplayTask{ID: "isolation", Input: json.RawMessage("{\"input\":1}\n"), Expected: json.RawMessage(`{"ok":true}`)}
	run, err := runner.Run(context.Background(), sources, p, task)
	if err != nil {
		t.Fatal(err)
	}
	if !taskPassed(task, run) {
		t.Fatalf("isolation failed: %+v", run)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("task workspace not cleaned: %v %v", entries, err)
	}
}

func TestDockerReplayCompilesAndExecutesCandidateBinary(t *testing.T) {
	image := os.Getenv("ORRERY_TEST_REPLAY_GO_IMAGE")
	if image == "" {
		t.Skip("requires an explicitly pinned Go task image")
	}
	sources := []SourceFile{{Path: "go.mod", Content: "module compiled_task\n\ngo 1.25.0\n"}, {Path: "main.go", Content: "package main\nimport \"fmt\"\nfunc main(){fmt.Print(`{\"ok\":true}`)}\n"}}
	p := replayTestSpec().Policy
	p.Image = image
	p.Entrypoint = "/usr/local/go/bin/go"
	p.Args = []string{"run", "."}
	p.TimeoutMS = 30000
	p.MemoryMB = 512
	task := ReplayTask{ID: "compiled-task", Input: json.RawMessage(`{"input":1}`), Expected: json.RawMessage(`{"ok":true}`)}
	run, err := (&DockerTaskRunner{WorkRoot: t.TempDir()}).Run(context.Background(), sources, p, task)
	if err != nil || !taskPassed(task, run) {
		t.Fatalf("compiled task failed to execute in scratch filesystem: %+v %v", run, err)
	}
}
