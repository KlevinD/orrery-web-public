package improvement

import (
	"encoding/json"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

const maxRuntimeDependencyEntries = 512
const maxRuntimeDependencyBytes = 512 * 1024

// RuntimeDependencies is an immutable, hash-only observation of actual callback
// entry. It is not a replay package or a complete nine-domain baseline. Entries
// omitted by the bound, invalid host observations and premature finalization
// remain explicit. No current registry is reread to reconstruct past usage.
type RuntimeDependencies struct {
	ID             string                                   `json:"id"`
	Protocol       string                                   `json:"protocol"`
	TenantID       string                                   `json:"tenant_id"`
	AttemptID      string                                   `json:"attempt_id"`
	RequestID      string                                   `json:"request_id"`
	ExecutionID    string                                   `json:"execution_evidence_id"`
	Entries        []agentcore.RuntimeDependencyObservation `json:"entries"`
	Dropped        int                                      `json:"dropped"`
	Invalid        int                                      `json:"invalid"`
	RunnerReturned bool                                     `json:"runner_returned"`
	CapturedAt     time.Time                                `json:"captured_at"`
}

func validDependencyObservation(o agentcore.RuntimeDependencyObservation) bool {
	if !textOK(o.TenantID, 256) || !textOK(o.RunID, 256) || !textOK(o.AgentID, 256) {
		return false
	}
	switch o.Kind {
	case "model_dispatch":
		return hashOK(o.InputSHA256) && hashOK(o.ToolsSHA256) && (o.ModelIdentity == "" || hashOK(o.ModelIdentity)) && o.ToolID == "" && o.Version == "" && o.BindingSHA256 == "" && o.CallSHA256 == ""
	case "tool_dispatch":
		return hashOK(o.InputSHA256) && textOK(o.ToolID, 512) && (o.Version == "" || textOK(o.Version, 256)) && hashOK(o.BindingSHA256) && hashOK(o.CallSHA256) && o.ToolsSHA256 == "" && o.ModelIdentity == ""
	case "runner_returned":
		return o.InputSHA256 == "" && o.ToolsSHA256 == "" && o.ModelIdentity == "" && o.ToolID == "" && o.Version == "" && o.BindingSHA256 == "" && o.CallSHA256 == ""
	}
	return false
}

func (s *runtimeEvidenceTraceSession) RecordDependency(o agentcore.RuntimeDependencyObservation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// A deadline can finalize the adapter while an uncooperative callback is
	// still unwinding. Never mutate the frozen receipt or journal after Finish.
	if s.dependenciesFinished || s.execution == nil || s.execution.Snapshot.DependencyProtocol != agentcore.RuntimeDependencyProtocol {
		return
	}
	if !validDependencyObservation(o) || o.TenantID != s.captured.ExecutionTenantID || o.RunID != s.captured.RunID || o.AgentID != s.captured.AgentID {
		if s.dependencyInvalid < 1_000_000 {
			s.dependencyInvalid++
		}
		return
	}
	if o.Kind == "runner_returned" {
		s.dependencyRunnerReturned = true
		return
	}
	raw, _ := json.Marshal(o)
	if len(s.dependencyEntries) >= maxRuntimeDependencyEntries || s.dependencyBytes+len(raw)+1 > maxRuntimeDependencyBytes-8192 {
		if s.dependencyDropped < 1_000_000 {
			s.dependencyDropped++
		}
		return
	}
	s.dependencyEntries = append(s.dependencyEntries, o)
	s.dependencyBytes += len(raw) + 1
}

func (s *runtimeEvidenceTraceSession) freezeDependencies(at time.Time) *RuntimeDependencies {
	if s.execution == nil || s.execution.Snapshot.DependencyProtocol != agentcore.RuntimeDependencyProtocol {
		return nil
	}
	d := RuntimeDependencies{Protocol: agentcore.RuntimeDependencyProtocol, TenantID: s.captured.Scope.TenantID,
		AttemptID: s.attempt, RequestID: s.captured.ID, ExecutionID: s.execution.ID,
		Entries: append([]agentcore.RuntimeDependencyObservation{}, s.dependencyEntries...), Dropped: s.dependencyDropped,
		Invalid: s.dependencyInvalid, RunnerReturned: s.dependencyRunnerReturned, CapturedAt: at.UTC()}
	// Long tenant/tool identifiers can reach the byte bound before the count
	// bound. Drop the tail explicitly, retaining a valid bounded receipt.
	for {
		raw, _ := json.Marshal(d)
		if len(raw) <= maxRuntimeDependencyBytes-1024 {
			break
		}
		d.Entries = d.Entries[:len(d.Entries)-1]
		d.Dropped++
	}
	d.ID = "runtime-dependencies:" + Digest(d)
	return &d
}

func validateRuntimeDependencies(request RuntimeRequestEvidence, terminal RuntimeTerminalEvidence) error {
	d := terminal.Dependencies
	if d == nil {
		return nil
	}
	if d.Protocol != agentcore.RuntimeDependencyProtocol || d.TenantID != request.Scope.TenantID || d.AttemptID != request.AttemptID || d.RequestID != request.ID ||
		!textOK(d.ExecutionID, 512) || !d.CapturedAt.Equal(terminal.CompletedAt) || d.Dropped < 0 || d.Dropped > 1_001_000 || d.Invalid < 0 || d.Invalid > 1_000_000 || len(d.Entries) > maxRuntimeDependencyEntries {
		return ErrInvalid
	}
	for _, o := range d.Entries {
		if !validDependencyObservation(o) || o.Kind == "runner_returned" || o.TenantID != request.ExecutionTenantID || o.RunID != request.RunID || o.AgentID != request.AgentID {
			return ErrInvalid
		}
	}
	copy := *d
	copy.ID = ""
	raw, err := json.Marshal(d)
	if err != nil || len(raw) > maxRuntimeDependencyBytes || d.ID != "runtime-dependencies:"+Digest(copy) {
		return ErrInvalid
	}
	return nil
}
