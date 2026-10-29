package improvement

import (
	"context"
	"fmt"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/db"
)

// RuntimeImplementation is supplied by the host, not the model or request.
// BinarySHA256 identifies local code; it does not attest a remote model/service.
type RuntimeImplementation struct {
	BinarySHA256 string `json:"binary_sha256"`
	SourceSHA256 string `json:"source_sha256,omitempty"`
	BuildVersion string `json:"build_version,omitempty"`
}

type RuntimeExecutionEvidence struct {
	ID             string                             `json:"id"`
	Protocol       string                             `json:"protocol"`
	TenantID       string                             `json:"tenant_id"`
	AttemptID      string                             `json:"attempt_id"`
	RequestID      string                             `json:"request_id"`
	Implementation RuntimeImplementation              `json:"implementation"`
	Snapshot       agentcore.RuntimeExecutionSnapshot `json:"snapshot"`
	PolicySHA256   string                             `json:"policy_sha256"`
	CapturedAt     time.Time                          `json:"captured_at"`
}

func BuildRuntimeExecutionEvidence(request RuntimeRequestEvidence, implementation RuntimeImplementation, snapshot agentcore.RuntimeExecutionSnapshot, at time.Time) (RuntimeExecutionEvidence, error) {
	e := RuntimeExecutionEvidence{Protocol: "orrery-runtime-execution-v1", TenantID: request.Scope.TenantID,
		AttemptID: request.AttemptID, RequestID: request.ID, Implementation: implementation,
		Snapshot: privateCopy(snapshot), PolicySHA256: Digest(snapshot.Policy), CapturedAt: at.UTC()}
	e.ID = "runtime-execution:" + Digest(e)
	return e, validateRuntimeExecutionEvidence(request, e)
}

func validateRuntimeExecutionEvidence(request RuntimeRequestEvidence, e RuntimeExecutionEvidence) error {
	s, p := e.Snapshot, e.Snapshot.Policy
	if validateRuntimeEvidence(request) != nil || e.Protocol != "orrery-runtime-execution-v1" ||
		e.TenantID != request.Scope.TenantID || e.AttemptID != request.AttemptID || e.RequestID != request.ID ||
		e.CapturedAt.IsZero() || e.CapturedAt.Before(request.CapturedAt) ||
		!hashOK(e.Implementation.BinarySHA256) || (e.Implementation.SourceSHA256 != "" && !hashOK(e.Implementation.SourceSHA256)) ||
		(s.DependencyProtocol != "" && s.DependencyProtocol != agentcore.RuntimeDependencyProtocol) ||
		(e.Implementation.BuildVersion != "" && !textOK(e.Implementation.BuildVersion, 256)) ||
		s.Engine != "orrery-agentcore" || s.TenantID != request.ExecutionTenantID || s.RunID != request.RunID || s.AgentID != request.AgentID ||
		(s.ModelIdentity != "" && !hashOK(s.ModelIdentity)) || (s.BackendSessionID != "" && !textOK(s.BackendSessionID, 256)) ||
		s.ModelCalls < 0 || s.ToolCalls < 0 || s.Compactions < 0 ||
		p.Protocol != "orrery-agentcore-policy-v1" || e.PolicySHA256 != Digest(p) ||
		p.MaxModelCalls < 1 || p.MaxToolCalls < 1 || p.MaxParallelTools < 1 || p.MaxParallelTools > 16 || p.MaxConcurrentCallbacks < 1 ||
		p.MaxContextBytes < 1 || p.MaxResponseBytes < 1 || p.ModelTimeoutNS < 1 || p.ToolTimeoutNS < 1 || p.JournalTimeoutNS < 1 ||
		p.ApprovalTimeoutNS < 1 || p.DefaultLongTimeoutNS < 1 || p.MaxTransportTimeoutNS < 1 || s.TransportTimeoutNS < 1 || s.TransportTimeoutNS > p.MaxTransportTimeoutNS ||
		(p.AutoCompact && (!p.Checkpoints || p.CompactAtBytes < 1 || p.CompactAtBytes >= p.MaxContextBytes || p.CompactKeepMessages < 1 || p.CompactSummaryBytes < 1)) ||
		len(p.ParallelToolIDs) > 512 || len(p.ApprovalToolIDs) > 512 {
		return ErrInvalid
	}
	for _, values := range [][]string{p.ParallelToolIDs, p.ApprovalToolIDs} {
		for i, value := range values {
			if !textOK(value, 512) || (i > 0 && values[i-1] >= value) {
				return ErrInvalid
			}
		}
	}
	copy := e
	copy.ID = ""
	if e.ID != "runtime-execution:"+Digest(copy) {
		return ErrInvalid
	}
	return nil
}

func executionEvidenceTx(ctx context.Context, tx db.Tx, request RuntimeRequestEvidence) (*RuntimeExecutionEvidence, error) {
	record, exists, err := db.GetRecordTx(ctx, tx, runtimeEvidenceTable, runtimeEvidenceKey(request.Scope.TenantID, request.AttemptID, "execution"))
	if err != nil || !exists {
		return nil, err
	}
	e, err := decode[RuntimeExecutionEvidence](record)
	if err != nil || validateRuntimeExecutionEvidence(request, e) != nil {
		return nil, ErrInvalid
	}
	return &e, nil
}

func (s *RuntimeEvidenceStore) CaptureExecution(ctx context.Context, e RuntimeExecutionEvidence) error {
	if s == nil || s.store.ready() != nil {
		return ErrUnavailable
	}
	if !validScope(e.TenantID, e.AttemptID) {
		return ErrInvalid
	}
	return s.store.core.WithTx(ctx, func(tx db.Tx) error {
		key := runtimeEvidenceKey(e.TenantID, e.AttemptID, "request")
		if err := db.LockRecordKeyTx(ctx, tx, runtimeEvidenceTable, key); err != nil {
			return err
		}
		raw, exists, err := db.GetRecordTx(ctx, tx, runtimeEvidenceTable, key)
		if err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		request, err := decode[RuntimeRequestEvidence](raw)
		if err != nil || validateRuntimeExecutionEvidence(request, e) != nil {
			return ErrInvalid
		}
		old, err := executionEvidenceTx(ctx, tx, request)
		if err != nil {
			return err
		}
		if old != nil {
			if old.ID != e.ID {
				return ErrConflict
			}
			return nil
		}
		_, terminal, err := db.GetRecordTx(ctx, tx, runtimeEvidenceTable, runtimeEvidenceKey(e.TenantID, e.AttemptID, "terminal"))
		if err != nil {
			return err
		}
		if terminal {
			return fmt.Errorf("%w: execution evidence cannot be attached after a terminal", ErrConflict)
		}
		return immutableRuntimeEvidenceTx(ctx, tx, runtimeEvidenceKey(e.TenantID, e.AttemptID, "execution"), e)
	})
}

func (s *RuntimeEvidenceStore) GetExecution(ctx context.Context, tenant, attempt string) (RuntimeExecutionEvidence, error) {
	request, err := s.GetRequest(ctx, tenant, attempt)
	if err != nil {
		return RuntimeExecutionEvidence{}, err
	}
	raw, exists, err := s.store.core.GetRecord(ctx, runtimeEvidenceTable, runtimeEvidenceKey(tenant, attempt, "execution"))
	if err != nil {
		return RuntimeExecutionEvidence{}, err
	}
	if !exists {
		return RuntimeExecutionEvidence{}, ErrNotFound
	}
	e, err := decode[RuntimeExecutionEvidence](raw)
	if err == nil {
		err = validateRuntimeExecutionEvidence(request, e)
	}
	return e, err
}
