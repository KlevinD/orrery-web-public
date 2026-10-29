package agentkernel

import (
	"encoding/json"
	"orrery_v2/internal/agent/llm"
	"strings"
	"unicode/utf8"
)

const compactionProgressVersion = 1

// Progress is private working state, never live conversation or executable
// tool input. The unchanged source history remains in the same checkpoint.
type CompactionProgress struct {
	Version         int    `json:"version"`
	Generation      int    `json:"generation"`
	SourceHash      string `json:"source_hash"`
	PolicyHash      string `json:"policy_hash"`
	ArchiveMessages int    `json:"archive_messages"`
	PayloadBytes    int    `json:"payload_bytes"`
	Offset          int    `json:"offset"`
	PendingBytes    int    `json:"pending_bytes,omitempty"`
	FragmentCap     int    `json:"fragment_cap"`
	SummaryLimit    int    `json:"summary_limit"`
	Summary         string `json:"summary"`
}

func cloneCompactionProgress(p *CompactionProgress) *CompactionProgress {
	if p == nil {
		return nil
	}
	next := *p
	return &next
}

func (r *Runner) compactionPolicy() string {
	budget, advertised := r.contextUsage(nil, nil)
	hash, _ := digest([]any{compactionProgressVersion, compactionInstruction,
		r.config.MaxContextBytes, r.config.MaxResponseBytes, r.config.CompactAtBytes,
		r.config.CompactKeepMessages, r.summaryLimitBytes(), advertised, budget.InputTokenLimit})
	return hash
}

func validateCompactionProgress(c Checkpoint) error {
	p := c.CompactionProgress
	if p == nil {
		return nil
	}
	source, err := digest(c.Messages)
	if err != nil || c.Phase != CheckpointReady || p.Version != compactionProgressVersion || p.Generation != c.Compactions || !validDigest(p.SourceHash) || p.SourceHash != source || !validDigest(p.PolicyHash) {
		return ErrCheckpoint
	}
	prefix := 0
	for prefix < len(c.Messages) && c.Messages[prefix].Role == llm.System {
		prefix++
	}
	if p.ArchiveMessages <= 0 || p.ArchiveMessages >= len(c.Messages)-prefix {
		return ErrCheckpoint
	}
	archived := c.Messages[prefix : prefix+p.ArchiveMessages]
	if _, err := contextBlocks(archived); err != nil {
		return ErrCheckpoint
	}
	if _, err := contextBlocks(c.Messages[prefix+p.ArchiveMessages:]); err != nil {
		return ErrCheckpoint
	}
	payload, err := json.Marshal(archived)
	if err != nil || p.PayloadBytes != len(payload) || p.Offset < 0 || p.Offset > len(payload) || (p.Offset < len(payload) && !utf8.RuneStart(payload[p.Offset])) || p.FragmentCap < 1 || p.FragmentCap > len(payload) || p.SummaryLimit < 1 || p.SummaryLimit > 2*1024*1024 || len(p.Summary) > p.SummaryLimit || !utf8.ValidString(p.Summary) {
		return ErrCheckpoint
	}
	if (p.Offset == 0 && p.Summary != "") || (p.Offset > 0 && strings.TrimSpace(p.Summary) == "") {
		return ErrCheckpoint
	}
	if p.PendingBytes < 0 || p.PendingBytes > p.FragmentCap || p.PendingBytes > len(payload)-p.Offset || (p.Offset+p.PendingBytes < len(payload) && !utf8.RuneStart(payload[p.Offset+p.PendingBytes])) || (c.ModelCalls == 0 && (p.Offset > 0 || p.PendingBytes > 0)) {
		return ErrCheckpoint
	}
	return nil
}

// Progress cannot disappear, rewind, change its source, or advance to a tool
// boundary. Clearing it requires the final atomic history/archive transaction.
func validateCompactionTransition(old, next Checkpoint) error {
	a, b := old.CompactionProgress, next.CompactionProgress
	if a == nil && b == nil {
		return nil // Checkpoints written before progress support remain readable.
	}
	if old.Phase != CheckpointReady || next.Phase != CheckpointReady || next.Turn != old.Turn || next.RunID != old.RunID || next.ToolCalls != old.ToolCalls {
		return ErrCheckpoint
	}
	if a == nil {
		if next.Compactions != old.Compactions || b.Offset != 0 || b.PendingBytes != 0 || b.Summary != "" {
			return ErrCheckpoint
		}
		source, _ := digest(old.Messages)
		if b.SourceHash != source {
			return ErrCheckpoint
		}
		return nil
	}
	if next.Compactions == old.Compactions+1 {
		if b != nil || a.Offset != a.PayloadBytes || a.PendingBytes != 0 || next.ModelCalls != old.ModelCalls {
			return ErrCheckpoint
		}
		return nil
	}
	if b == nil || next.Compactions != old.Compactions || b.Offset < a.Offset || b.FragmentCap > a.FragmentCap {
		return ErrCheckpoint
	}
	left, right := *a, *b
	left.Offset, right.Offset = 0, 0
	left.PendingBytes, right.PendingBytes = 0, 0
	left.FragmentCap, right.FragmentCap = 0, 0
	left.Summary, right.Summary = "", ""
	if left != right || (b.Offset == a.Offset && b.Summary != a.Summary) {
		return ErrCheckpoint
	}
	if b.Offset > a.Offset && (a.PendingBytes == 0 || b.Offset != a.Offset+a.PendingBytes || b.PendingBytes != 0 || next.ModelCalls != old.ModelCalls) {
		return ErrCheckpoint
	}
	if b.Offset == a.Offset && b.PendingBytes > 0 && b.PendingBytes != a.PendingBytes && next.ModelCalls <= old.ModelCalls {
		return ErrCheckpoint
	}
	return nil
}
