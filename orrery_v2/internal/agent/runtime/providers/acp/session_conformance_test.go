package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/conformance"
)

type acpConformanceHarness struct {
	backend *Session
	events  chan agentcore.SessionEnvelope
	count   atomic.Int64
}

func newACPConformanceHarness(t *testing.T) conformance.Harness {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	connection, err := NewConnection(Config{
		HandshakeTimeout: 5 * time.Second,
		RequestTimeout:   5 * time.Second,
		ResolveWorkspace: WorkspaceResolverFunc(func(context.Context, string) (Workspace, error) {
			return Workspace{
				Directory:  workspace,
				Executable: executable,
				Args:       []string{"-test.run=TestACPHelperProcess"},
				Environment: map[string]string{
					"GO_WANT_ACP_HELPER": "1",
					"ACP_HELPER_MODE":    "conformance",
				},
				Version: "fixture-1",
			}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	opened, err := connection.OpenSession(context.Background(), agentcore.SessionSpec{
		Mode: agentcore.SessionOpenFresh, SessionID: conformance.SessionID,
	}, agentcore.SessionConfig{WorkspaceRef: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	backend := opened.(*Session)
	harness := &acpConformanceHarness{backend: backend, events: make(chan agentcore.SessionEnvelope, 512)}
	go func() {
		defer close(harness.events)
		for event := range backend.Events() {
			switch event.Event.Kind {
			case agentcore.EventBackendBound, agentcore.EventCatalogUpdated, agentcore.EventPromptAccepted:
				continue
			}
			harness.events <- event
		}
	}()
	return harness
}

func (h *acpConformanceHarness) Dispatch(ctx context.Context, command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	h.count.Add(1)
	return h.backend.Dispatch(ctx, command)
}

func (h *acpConformanceHarness) Events() <-chan agentcore.SessionEnvelope { return h.events }
func (h *acpConformanceHarness) Capabilities() agentcore.Capabilities {
	return h.backend.Capabilities()
}
func (h *acpConformanceHarness) PendingPermissionRequests() []agentcore.PendingPermissionView {
	return h.backend.PendingPermissionRequests()
}
func (h *acpConformanceHarness) DispatchCount() int { return int(h.count.Load()) }

func (h *acpConformanceHarness) Emit(generation uint64, event agentcore.SessionEvent) {
	switch event.Kind {
	case agentcore.EventMessageDelta:
		raw, err := json.Marshal(map[string]any{
			"sessionId": h.backend.boundSession(),
			"update": map[string]any{
				"sessionUpdate": "agent_message_chunk",
				"content":       map[string]any{"type": "text", "text": event.MessageDelta.Text},
			},
		})
		if err != nil || h.backend.handleSessionUpdate(raw) != nil {
			panic("ACP conformance message fixture could not be translated")
		}
	case agentcore.EventTurnResult:
		h.backend.completeTurn(generation, *event.TurnResult)
	case agentcore.EventPermission, agentcore.EventPermissionResolved, agentcore.EventNotice:
		h.backend.emit(generation, event)
	default:
		panic(fmt.Sprintf("ACP conformance fixture cannot translate %s", event.Kind))
	}
}

func (h *acpConformanceHarness) EndEvents() { h.backend.closeEvents() }

func TestACPProviderPassesSharedConformance(t *testing.T) {
	conformance.RunSharedSuite(t, newACPConformanceHarness)
}
