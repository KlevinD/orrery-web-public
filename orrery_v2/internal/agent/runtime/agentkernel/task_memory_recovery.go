package agentkernel

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/retrievalevidence"
)

var ErrTaskInputChanged = fmt.Errorf("%w: initial task input changed", ErrCheckpoint)

// Only the host-rendered memory message is eligible for audit-time equivalence.
// User/tool text with a checked_at key is ordinary task input and stays exact.
func taskMemoryEvidence(req agentcore.RuntimeRequest) string {
	memory := agentcore.BuildMemoryContextPrompt(req.ContextPack)
	evidence := agentcore.BuildMemoryEvidencePrompt(req.ContextPack)
	if memory == "" || evidence == "" {
		return ""
	}
	found := 0
	for _, message := range req.InputMessages {
		if message.Role == "user" && message.Content == memory {
			found++
		}
	}
	if found != 1 || strings.Count(requestPrompt(req), evidence) != 1 {
		return ""
	}
	if _, ok := memoryEvidenceIdentity(evidence); !ok {
		return ""
	}
	return evidence
}

// The stored messages, prompt, counters and deadline remain the execution
// snapshot. A fresh access check may change CheckedAt, which Seal explicitly
// excludes from InputHash, but no source, truth/access fingerprint or text may
// change. Scope/model checks still run independently before this comparison.
func sameTaskPrompt(stored, candidate Checkpoint) bool {
	if stored.FirstPrompt == candidate.FirstPrompt {
		return true
	}
	current := candidate.InitialMemoryEvidence
	if current == "" || strings.Count(candidate.FirstPrompt, current) != 1 {
		return false
	}
	previous := stored.InitialMemoryEvidence
	if previous == "" {
		// Legacy checkpoints retain the full initial prompt through compaction.
		// Recover only its metadata line; ambiguous legacy data stays fenced.
		prefix := retrievalevidence.Guidance + "\nRetrieval evidence: "
		if strings.Count(stored.FirstPrompt, prefix) != 1 {
			return false
		}
		start := strings.Index(stored.FirstPrompt, prefix)
		tail := stored.FirstPrompt[start:]
		previous, _, _ = strings.Cut(tail, "\n- ")
	}
	if strings.Count(stored.FirstPrompt, previous) != 1 {
		return false
	}
	left, ok := memoryEvidenceIdentity(previous)
	if !ok {
		return false
	}
	right, ok := memoryEvidenceIdentity(current)
	if !ok || left != right {
		return false
	}
	// Every other byte, including memory summaries and user instructions, stays
	// exact. Only one metadata span produced by the trusted host is replaced.
	const marker = "\x00orrery-memory-retrieval-audit\x00"
	return strings.Replace(stored.FirstPrompt, previous, marker, 1) == strings.Replace(candidate.FirstPrompt, current, marker, 1)
}

func memoryEvidenceIdentity(prompt string) (string, bool) {
	prefix := retrievalevidence.Guidance + "\nRetrieval evidence: "
	body, ok := strings.CutPrefix(prompt, prefix)
	if !ok {
		return "", false
	}
	// RawMessage preserves all other fields, including future fields. Only the
	// top-level audit timestamp is normalized, never nested/source timestamps.
	var items []map[string]json.RawMessage
	if json.Unmarshal([]byte(body), &items) != nil || len(items) == 0 {
		return "", false
	}
	for _, item := range items {
		var checkedAt string
		if json.Unmarshal(item["checked_at"], &checkedAt) != nil {
			return "", false
		}
		if _, err := time.Parse(time.RFC3339Nano, checkedAt); err != nil {
			return "", false
		}
		var inputHash string
		if json.Unmarshal(item["input_hash"], &inputHash) != nil || !validDigest(inputHash) {
			return "", false
		}
		delete(item, "checked_at")
	}
	value, err := json.Marshal(items)
	return string(value), err == nil
}
