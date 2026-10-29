package improvement

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/db"
)

func feedbackFixture(t *testing.T, request RuntimeRequestEvidence) RuntimeFeedback {
	t.Helper()
	end, err := buildRuntimeTerminal(request, runtimeEvidenceTerminal(), "succeeded")
	if err != nil {
		t.Fatal(err)
	}
	return RuntimeFeedback{TenantID: request.Scope.TenantID, Terminal: end}
}

func feedbackJournal(t *testing.T, root string) *FileRuntimeFeedbackJournal {
	t.Helper()
	j, err := NewFileRuntimeFeedbackJournal(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	return j
}

func TestRuntimeFeedbackJournalConcurrentImmutabilityAndBounds(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	_, req := runtimeEvidenceFixture(t, "attempt-a")
	entry := feedbackFixture(t, req)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			journal, err := NewFileRuntimeFeedbackJournal(root)
			if err == nil {
				defer journal.Close()
				err = journal.Save(ctx, entry)
			}
			errs <- err
		}()
	}
	wg.Wait()
	for range 8 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	j := feedbackJournal(t, root)
	other := entry
	other.Terminal.Status = agentcore.RuntimeStatusFailed
	other.Terminal.ID = ""
	other.Terminal.ID = "runtime-terminal-evidence:" + Digest(other.Terminal)
	if err := j.Save(ctx, other); !errors.Is(err, ErrConflict) {
		t.Fatalf("overwrote first durable terminal: %v", err)
	}
	if err := j.Acknowledge(ctx, other); !errors.Is(err, ErrConflict) {
		t.Fatalf("deleted conflicting terminal: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, runtimeFeedbackName(entry)))
	if err != nil || len(data) > maxRuntimeFeedbackBytes || strings.Contains(string(data), "private") {
		t.Fatalf("invalid or sensitive journal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "oversized.json"), []byte(strings.Repeat("x", maxRuntimeFeedbackBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	items, err := j.Scan(ctx, 100)
	if !errors.Is(err, ErrInvalid) || len(items) != 1 || Digest(items[0]) != Digest(entry) {
		t.Fatalf("bad record hid valid feedback: %d %v", len(items), err)
	}
	if err := j.Acknowledge(ctx, entry); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, runtimeFeedbackName(entry))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("acknowledged journal retained: %v", err)
	}
	if err := j.Acknowledge(ctx, entry); err != nil {
		t.Fatal("acknowledgement was not idempotent", err)
	}
}

func TestRuntimeFeedbackRecoversAfterDatabaseOutageAndRestart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	url := "file://" + filepath.Join(root, "core.db")
	open := func() *db.Store {
		t.Helper()
		core, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: url})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = core.Close() })
		return core
	}
	core := open()
	j := feedbackJournal(t, filepath.Join(root, "journal"))
	base := &runtimeEvidenceTestTrace{}
	req, fixture := runtimeEvidenceFixture(t, "ignored")
	identity := executionFixture(t, fixture).Implementation
	recorder := RuntimeEvidenceRecorder{Recorder: base, Evidence: NewRuntimeEvidenceStore(core), Journal: j, Implementation: &identity, Scope: func(agentcore.RuntimeRequest) (RuntimeEvidenceScope, error) { return fixture.Scope, nil }, Now: func() time.Time { return fixture.CapturedAt }}
	session, err := recorder.BeginTrace(ctx, agentcore.AgentInput{})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.RecordRequest(ctx, req); err != nil {
		t.Fatal(err)
	}
	attempt := session.(*runtimeEvidenceTraceSession).attempt
	s := session.(*runtimeEvidenceTraceSession)
	snapshot := executionFixture(t, *s.captured).Snapshot
	snapshot.DependencyProtocol = agentcore.RuntimeDependencyProtocol
	if err := s.RecordExecution(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	s.RecordDependency(dependencyModelObservation(*s.captured))
	if err := core.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.Finish(ctx, runtimeEvidenceTerminal()); err == nil || base.finishes != 1 {
		t.Fatalf("database failure hidden or trace not finalized: %v", err)
	}
	_ = j.Close()
	// Recreate both database connection and journal scanner, as a new process does.
	j = feedbackJournal(t, filepath.Join(root, "journal"))
	store := NewRuntimeEvidenceStore(open())
	if _, err := store.GetTerminal(ctx, fixture.Scope.TenantID, attempt); !errors.Is(err, ErrNotFound) {
		t.Fatalf("uncommitted terminal appeared: %v", err)
	}
	result, err := store.RecoverFeedback(ctx, j, 100)
	if err != nil || result.Recovered != 1 || result.Retained != 0 {
		t.Fatalf("restart recovery: %+v %v", result, err)
	}
	end, err := store.GetTerminal(ctx, fixture.Scope.TenantID, attempt)
	if err != nil || end.CompletedAt != runtimeEvidenceTerminal().CompletedAt || end.TraceFinalization != "succeeded" || end.Usage["model-a"].InputTokens != 15 {
		t.Fatalf("recovery invented terminal data: %+v %v", end, err)
	}
	if end.Dependencies == nil || len(end.Dependencies.Entries) != 1 || end.Dependencies.RunnerReturned || end.Dependencies.ExecutionID != s.execution.ID {
		t.Fatal("restart lost original dependency receipt", end.Dependencies)
	}
	// Simulate death after DB commit and before journal acknowledgement.
	entry := RuntimeFeedback{TenantID: fixture.Scope.TenantID, Terminal: end}
	if err := j.Save(ctx, entry); err != nil {
		t.Fatal(err)
	}
	_ = j.Close()
	if result, err := store.RecoverFeedback(ctx, j, 100); err != nil || result.Recovered != 1 {
		t.Fatalf("committed terminal could not acknowledge journal: %+v %v", result, err)
	}
	for _, table := range []string{caseTable, historyTable, outboxTable} {
		page, err := store.store.core.ListRecords(ctx, table, "", 100)
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("recovery duplicated %s: %d %v", table, len(page.Items), err)
		}
	}
	files, err := os.ReadDir(j.dir)
	if err != nil || len(files) != 0 {
		t.Fatalf("committed feedback not cleaned: %d %v", len(files), err)
	}
}

func TestRuntimeFeedbackRecoveryRetainsUnknownConflictingAndInvalidResults(t *testing.T) {
	for _, scenario := range []string{"unknown request", "wrong tenant", "different request", "invalid usage", "conflicting terminal"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			store := NewRuntimeEvidenceStore(testStore(t).core)
			j := feedbackJournal(t, t.TempDir())
			_, req := runtimeEvidenceFixture(t, "attempt-a")
			if scenario != "unknown request" {
				if err := store.Capture(ctx, req); err != nil {
					t.Fatal(err)
				}
			}
			entry := feedbackFixture(t, req)
			switch scenario {
			case "wrong tenant":
				entry.TenantID = "tenant-b"
			case "different request":
				entry.Terminal.RequestID = "runtime-request-evidence:" + Digest("another request")
			case "invalid usage":
				entry.Terminal.Usage["model-a"] = agentcore.TokenUsage{InputTokens: -1}
			case "conflicting terminal":
				if _, err := store.completeTerminal(ctx, req, entry.Terminal); err != nil {
					t.Fatal(err)
				}
				entry.Terminal.Status = agentcore.RuntimeStatusFailed
			}
			entry.Terminal.ID = ""
			entry.Terminal.ID = "runtime-terminal-evidence:" + Digest(entry.Terminal)
			if err := j.Save(ctx, entry); err != nil {
				t.Fatal(err)
			}
			result, err := store.RecoverFeedback(ctx, j, 100)
			if err == nil || result.Retained != 1 || result.Recovered != 0 {
				t.Fatalf("untrusted result accepted: %+v %v", result, err)
			}
			if _, err := os.Stat(filepath.Join(j.dir, runtimeFeedbackName(entry))); err != nil {
				t.Fatal("rejected evidence was removed", err)
			}
			end, err := store.GetTerminal(ctx, req.Scope.TenantID, req.AttemptID)
			if scenario == "conflicting terminal" {
				if err != nil || end.Status != agentcore.RuntimeStatusCompleted {
					t.Fatalf("existing terminal overwritten: %+v %v", end, err)
				}
			} else if !errors.Is(err, ErrNotFound) {
				t.Fatalf("untrusted terminal persisted: %+v %v", end, err)
			}
		})
	}
}

func TestRuntimeFeedbackRecoveryAtomicFailureAndRetry(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	store := NewRuntimeEvidenceStore(s.core)
	j := feedbackJournal(t, t.TempDir())
	_, req := runtimeEvidenceFixture(t, "attempt-a")
	if err := store.Capture(ctx, req); err != nil {
		t.Fatal(err)
	}
	entry := feedbackFixture(t, req)
	if err := j.Save(ctx, entry); err != nil {
		t.Fatal(err)
	}
	c := Case{ID: caseID(Observation{TenantID: req.Scope.TenantID, ID: "captured-runtime-result:" + Digest(entry.Terminal)}), TenantID: req.Scope.TenantID, Revision: 1}
	conflict := encode("existing immutable revision")
	if err := s.core.WithTx(ctx, func(tx db.Tx) error {
		_, err := db.InsertRecordTx(ctx, tx, historyTable, revisionKey(c), conflict)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	result, err := store.RecoverFeedback(ctx, j, 100)
	if !errors.Is(err, ErrConflict) || result.Retained != 1 {
		t.Fatalf("expected atomic failure: %+v %v", result, err)
	}
	if _, err := store.GetTerminal(ctx, req.Scope.TenantID, req.AttemptID); !errors.Is(err, ErrNotFound) {
		t.Fatal("terminal committed without case", err)
	}
	if ok, err := s.core.CompareAndDeleteRecord(ctx, historyTable, revisionKey(c), conflict); err != nil || !ok {
		t.Fatal(err)
	}
	_ = j.Close()
	result, err = store.RecoverFeedback(ctx, j, 100)
	if err != nil || result.Recovered != 1 {
		t.Fatalf("journal did not survive failed transaction: %+v %v", result, err)
	}
}

func TestRuntimeFeedbackScanDoesNotStarveBehindPoisonOrIncompleteAttempts(t *testing.T) {
	ctx := context.Background()
	store := NewRuntimeEvidenceStore(testStore(t).core)
	j := feedbackJournal(t, t.TempDir())
	_, req := runtimeEvidenceFixture(t, "finished-attempt")
	_, incomplete := runtimeEvidenceFixture(t, "prepared-only-attempt")
	for _, request := range []RuntimeRequestEvidence{req, incomplete} {
		if err := store.Capture(ctx, request); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(j.dir, "poison.json"), []byte("{invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := j.Save(ctx, feedbackFixture(t, req)); err != nil {
		t.Fatal(err)
	}
	recovered, rejected := 0, false
	for range 8 {
		result, err := store.RecoverFeedback(ctx, j, 1)
		recovered += result.Recovered
		rejected = rejected || errors.Is(err, ErrInvalid)
	}
	if recovered != 1 || !rejected {
		t.Fatalf("bounded scan starvation: recovered=%d rejected=%v", recovered, rejected)
	}
	if _, err := store.GetTerminal(ctx, incomplete.Scope.TenantID, incomplete.AttemptID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("prepared-only attempt was invented as complete: %v", err)
	}
}

type failingFeedbackJournal struct{ err error }

func (j failingFeedbackJournal) Save(context.Context, RuntimeFeedback) error { return j.err }
func (j failingFeedbackJournal) Scan(context.Context, int) ([]RuntimeFeedback, error) {
	return nil, j.err
}
func (j failingFeedbackJournal) Acknowledge(context.Context, RuntimeFeedback) error { return j.err }

func TestRuntimeFeedbackTraceUsesDatabaseFallbackButRejectsConflicts(t *testing.T) {
	for _, journalErr := range []error{errors.New("journal disk unavailable"), ErrConflict} {
		t.Run(journalErr.Error(), func(t *testing.T) {
			ctx := context.Background()
			store := NewRuntimeEvidenceStore(testStore(t).core)
			req, e := runtimeEvidenceFixture(t, "ignored")
			base := &runtimeEvidenceTestTrace{}
			r := RuntimeEvidenceRecorder{Recorder: base, Evidence: store, Journal: failingFeedbackJournal{journalErr}, Scope: func(agentcore.RuntimeRequest) (RuntimeEvidenceScope, error) { return e.Scope, nil }, Now: func() time.Time { return e.CapturedAt }}
			session, err := r.BeginTrace(ctx, agentcore.AgentInput{})
			if err != nil {
				t.Fatal(err)
			}
			if err := session.RecordRequest(ctx, req); err != nil {
				t.Fatal(err)
			}
			err = session.Finish(ctx, runtimeEvidenceTerminal())
			_, terminalErr := store.GetTerminal(ctx, e.Scope.TenantID, session.(*runtimeEvidenceTraceSession).attempt)
			if errors.Is(journalErr, ErrConflict) {
				if !errors.Is(err, ErrConflict) || !errors.Is(terminalErr, ErrNotFound) {
					t.Fatalf("journal conflict ignored: %v", err)
				}
			} else if err != nil || terminalErr != nil {
				t.Fatalf("journal failure rejected durable database commit: %v", err)
			}
			if base.finishes != 1 {
				t.Fatal("underlying trace did not finalize")
			}
		})
	}
}

func TestRuntimeFeedbackJournalRejectsSymlinksAndCancelledAcknowledgement(t *testing.T) {
	ctx := context.Background()
	j := feedbackJournal(t, t.TempDir())
	_, req := runtimeEvidenceFixture(t, "attempt-a")
	entry := feedbackFixture(t, req)
	if err := j.Save(ctx, entry); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := j.Acknowledge(cancelled, entry); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(j.dir, runtimeFeedbackName(entry))); err != nil {
		t.Fatal("cancelled acknowledgement removed evidence", err)
	}
	if err := j.Acknowledge(ctx, entry); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		return // Windows link privilege varies; Linux production exercises the link guard.
	}
	outside := filepath.Join(t.TempDir(), "private.json")
	if err := os.WriteFile(outside, []byte("private external file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(j.dir, runtimeFeedbackName(entry))); err != nil {
		t.Fatal(err)
	}
	if err := j.Save(ctx, entry); !errors.Is(err, ErrInvalid) {
		t.Fatalf("followed terminal symlink: %v", err)
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "private external file" {
		t.Fatal("external file changed", err)
	}
}
