package lifecycle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSignalRecorderRecordsSignalOrder(t *testing.T) {
	bus, recorder := NewTestSignalBus()
	for _, name := range []SignalName{BeforeTaskReceived, AfterTaskReceived, BeforeTaskAccepted} {
		mustSubscribe(t, bus, name, recorder, HandlerOptions{FailurePolicy: FailurePolicyContinue})
	}

	for _, name := range []SignalName{BeforeTaskReceived, AfterTaskReceived, BeforeTaskAccepted} {
		signal := validSignal()
		signal.Name = name
		if _, err := bus.Emit(context.Background(), signal); err != nil {
			t.Fatalf("emit %s failed: %v", name, err)
		}
	}

	got := recorder.Names()
	want := []SignalName{BeforeTaskReceived, AfterTaskReceived, BeforeTaskAccepted}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("signal order drifted: want %#v got %#v", want, got)
		}
	}
}

func TestLifecycleTestkitHandlers(t *testing.T) {
	called := false
	recording := NewRecordingHandler("recording", func(signal SignalEnvelope) {
		called = signal.Name == BeforeTaskAccepted
	})
	if err := recording.HandleSignal(context.Background(), validSignal()); err != nil {
		t.Fatalf("recording handler failed: %v", err)
	}
	if !called {
		t.Fatal("recording handler callback was not called")
	}

	failing := NewFailingHandler("failing", errors.New("boom"))
	err := failing.HandleSignal(context.Background(), validSignal())
	if err == nil || err.Error() != "boom" {
		t.Fatalf("expected configured failure, got %v", err)
	}
}

func TestLifecyclePackageDoesNotDependOnForbiddenModules(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate lifecycle package directory")
	}
	pkgDir := filepath.Dir(file)
	forbidden := []string{
		"internal/mission/" + "event",
		"internal/agent/" + "runtime",
		"internal/" + "transport",
	}
	var matches []string
	if err := filepath.WalkDir(pkgDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		source := string(data)
		for _, item := range forbidden {
			if strings.Contains(source, item) {
				rel, _ := filepath.Rel(pkgDir, path)
				matches = append(matches, rel+":"+item)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("dependency scan failed unexpectedly: %v", err)
	}
	if len(matches) > 0 {
		t.Fatalf("forbidden dependency found:\n%s", strings.Join(matches, "\n"))
	}
}
