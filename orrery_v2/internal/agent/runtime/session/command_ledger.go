package session

import (
	"context"
	"errors"
	"sync"

	agentcore "orrery_v2/internal/agent/core"
)

type CommandLedger struct {
	mu         sync.Mutex
	entries    map[string]*commandLedgerEntry
	order      []string
	maxEntries int
	hits       uint64
	conflicts  uint64
}

type commandLedgerEntry struct {
	fingerprint string
	done        chan struct{}
	receipt     agentcore.CommandReceipt
	err         *agentcore.RuntimeBridgeError
}

type CommandLedgerStats struct {
	Entries   int
	Hits      uint64
	Conflicts uint64
}

func NewCommandLedger(maxEntries int) *CommandLedger {
	if maxEntries < 1 {
		maxEntries = 4096
	}
	return &CommandLedger{entries: make(map[string]*commandLedgerEntry), maxEntries: maxEntries}
}

func (l *CommandLedger) Execute(ctx context.Context, command agentcore.RuntimeCommand, dispatch func(context.Context) (agentcore.CommandReceipt, error)) (agentcore.CommandReceipt, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := command.Validate(); err != nil {
		return agentcore.CommandReceipt{}, err
	}
	fingerprint, err := command.Fingerprint()
	if err != nil {
		return agentcore.CommandReceipt{}, err
	}

	l.mu.Lock()
	if existing := l.entries[command.CommandID]; existing != nil {
		if existing.fingerprint != fingerprint {
			l.conflicts++
			l.mu.Unlock()
			return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "command id was already used with a different payload", false)
		}
		l.hits++
		done := existing.done
		l.mu.Unlock()
		select {
		case <-done:
			return existing.receipt, cloneBridgeError(existing.err)
		case <-ctx.Done():
			return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCancelled, "duplicate command wait cancelled", false)
		}
	}
	entry := &commandLedgerEntry{fingerprint: fingerprint, done: make(chan struct{})}
	l.entries[command.CommandID] = entry
	l.order = append(l.order, command.CommandID)
	l.mu.Unlock()

	receipt, dispatchErr := dispatch(ctx)
	storedErr := sanitizeDispatchError(dispatchErr)
	if storedErr == nil {
		if validationErr := receipt.Validate(); validationErr != nil {
			storedErr = sanitizeDispatchError(validationErr)
			receipt = agentcore.CommandReceipt{}
		}
	}

	l.mu.Lock()
	entry.receipt = receipt
	entry.err = storedErr
	close(entry.done)
	l.evictCompletedLocked()
	l.mu.Unlock()
	return receipt, cloneBridgeError(storedErr)
}

func (l *CommandLedger) Stats() CommandLedgerStats {
	l.mu.Lock()
	defer l.mu.Unlock()
	return CommandLedgerStats{Entries: len(l.entries), Hits: l.hits, Conflicts: l.conflicts}
}

func (l *CommandLedger) evictCompletedLocked() {
	for len(l.entries) > l.maxEntries && len(l.order) != 0 {
		id := l.order[0]
		entry := l.entries[id]
		select {
		case <-entry.done:
			delete(l.entries, id)
			l.order = l.order[1:]
		default:
			return
		}
	}
}

func sanitizeDispatchError(err error) *agentcore.RuntimeBridgeError {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCancelled, "command dispatch cancelled", false)
	}
	var bridgeErr *agentcore.RuntimeBridgeError
	if errors.As(err, &bridgeErr) {
		return agentcore.NewRuntimeBridgeError(bridgeErr.Code, string(bridgeErr.Code), bridgeErr.Retryable)
	}
	return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeBackendRejected, "backend rejected command", false)
}

func cloneBridgeError(err *agentcore.RuntimeBridgeError) error {
	if err == nil {
		return nil
	}
	return agentcore.NewRuntimeBridgeError(err.Code, err.Message, err.Retryable)
}
