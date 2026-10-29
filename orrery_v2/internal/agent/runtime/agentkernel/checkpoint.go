package agentkernel

import (
	"context"
	"encoding/json"
	"orrery_v2/internal/agent/llm"
	"strings"
	"time"

	"github.com/google/uuid"
	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/db"
)

const checkpointTable = "runtime_agent_kernel_checkpoints"
const compactionArchiveTable = "runtime_agent_kernel_compaction_archives"

type CheckpointPhase string

const (
	CheckpointReady        CheckpointPhase = "ready"
	CheckpointToolsPending CheckpointPhase = "tools_pending"
	CheckpointCompleted    CheckpointPhase = "completed"
)

// Checkpoints contain authorized conversation data, unlike the digest-only tool
// journal. They remain in the host runtime database and are never globally cached.
type Checkpoint struct {
	ID                    string               `json:"id"`
	TenantID              string               `json:"tenant_id"`
	AgentID               agentcore.AgentID    `json:"agent_id"`
	RunID                 agentcore.AgentRunID `json:"run_id"`
	MissionID             string               `json:"mission_id,omitempty"`
	TaskID                string               `json:"task_id,omitempty"`
	ScopeHash             string               `json:"scope_hash"`
	ModelIdentity         string               `json:"model_identity,omitempty"`
	Owner                 string               `json:"owner"`
	Revision              uint64               `json:"revision"`
	Phase                 CheckpointPhase      `json:"phase"`
	Messages              []*llm.Message       `json:"messages"`
	FirstPrompt           string               `json:"first_prompt"`
	InitialMemoryEvidence string               `json:"initial_memory_evidence,omitempty"`
	Turn                  uint64               `json:"turn"`
	ModelCalls            int                  `json:"model_calls"`
	ToolCalls             int                  `json:"tool_calls"`
	Compactions           int                  `json:"compactions"`
	WindowRecoveries      int                  `json:"window_recoveries,omitempty"`
	Usage                 KernelUsage          `json:"usage,omitempty"`
	Observation           ContextObservation   `json:"context_observation,omitempty"`
	CompactionProgress    *CompactionProgress  `json:"compaction_progress,omitempty"`
	ApprovalWait          *ApprovalWait        `json:"approval_wait,omitempty"`
	Deadline              time.Time            `json:"deadline,omitempty"`
	UpdatedAt             time.Time            `json:"updated_at"`

	BlockedTaskReviewed      bool                 `json:"blocked_task_reviewed,omitempty"`
	BlockedResultReviewRunID agentcore.AgentRunID `json:"blocked_result_review_run_id,omitempty"`
}

// Claim fences the previous owner atomically. Safe model boundaries and proven
// unexecuted approval proposals may resume. Other pending batches stay blocked.
type CheckpointStore interface {
	Create(context.Context, Checkpoint) (Checkpoint, error)
	Claim(context.Context, string, agentcore.AgentID, string, string, string, agentcore.AgentRunID) (Checkpoint, error)
	Save(context.Context, Checkpoint) (Checkpoint, error)
}

type SQLCheckpointStore struct{ store *db.Store }

func NewSQLCheckpointStore(store *db.Store) (*SQLCheckpointStore, error) {
	if store == nil || (store.Driver() != db.DriverFile && store.Driver() != db.DriverPostgres) {
		return nil, ErrConfiguration
	}
	return &SQLCheckpointStore{store: store}, nil
}
func checkpointKey(tenant string, agent agentcore.AgentID, id string) string {
	value, _ := digest([]string{tenant, agent.String(), id})
	return value
}
func checkpointRecord(c Checkpoint) (db.Record, error) {
	raw, err := json.Marshal(c)
	textBytes, mediaErr := contextMessageBytes(c.Messages, len(raw))
	if err != nil || mediaErr != nil || textBytes > 2*1024*1024 || c.ID == "" || c.TenantID == "" || c.AgentID.IsZero() || c.RunID.IsZero() || c.Owner == "" || !validDigest(c.ScopeHash) || c.Revision == 0 || c.ModelCalls < 0 || c.ToolCalls < 0 || c.Compactions < 0 {
		return nil, ErrCheckpoint
	}
	switch c.Phase {
	case CheckpointReady, CheckpointToolsPending, CheckpointCompleted:
	default:
		return nil, ErrCheckpoint
	}
	if c.Phase != CheckpointToolsPending {
		if _, err := contextBlocks(c.Messages); err != nil {
			return nil, ErrCheckpoint
		}
	}
	if c.ModelIdentity != "" && !validDigest(c.ModelIdentity) {
		return nil, ErrCheckpoint
	}
	if c.InitialMemoryEvidence != "" {
		if _, ok := memoryEvidenceIdentity(c.InitialMemoryEvidence); !ok || strings.Count(c.FirstPrompt, c.InitialMemoryEvidence) != 1 {
			return nil, ErrCheckpoint
		}
	}
	if c.WindowRecoveries < 0 || c.WindowRecoveries > 1 || c.Usage.CachedInputTokens > c.Usage.InputTokens || c.Usage.ReasoningTokens > c.Usage.OutputTokens || c.Observation.Tokens < 0 || c.Observation.Messages < 0 || c.Observation.Messages > len(c.Messages) || c.Observation.Compaction < 0 || c.Observation.Compaction > c.Compactions {
		return nil, ErrCheckpoint
	}
	if err := validateCompactionProgress(c); err != nil {
		return nil, err
	}
	if err := validateApprovalWait(c); err != nil {
		return nil, err
	}
	var out db.Record
	if json.Unmarshal(raw, &out) != nil {
		return nil, ErrCheckpoint
	}
	return out, nil
}
func decodeCheckpoint(record db.Record) (Checkpoint, error) {
	raw, err := json.Marshal(record)
	var c Checkpoint
	if err != nil || json.Unmarshal(raw, &c) != nil {
		return c, ErrCheckpoint
	}
	_, err = checkpointRecord(c)
	return c, err
}
func (s *SQLCheckpointStore) Create(ctx context.Context, c Checkpoint) (Checkpoint, error) {
	if c.ApprovalWait != nil {
		return Checkpoint{}, ErrCheckpoint
	}
	c.Revision = 1
	c.UpdatedAt = time.Now().UTC()
	record, err := checkpointRecord(c)
	if err != nil {
		return Checkpoint{}, err
	}
	err = s.store.WithTx(ctx, func(tx db.Tx) error {
		if db.LockRecordKeyTx(ctx, tx, checkpointTable, checkpointKey(c.TenantID, c.AgentID, c.ID)) != nil {
			return ErrCheckpoint
		}
		if err := maintainPendingTask(ctx, tx, Checkpoint{}, c); err != nil {
			return err
		}
		created, err := db.InsertRecordTx(ctx, tx, checkpointTable, checkpointKey(c.TenantID, c.AgentID, c.ID), record)
		if err != nil || !created {
			return ErrCheckpoint
		}
		return nil
	})
	if err != nil {
		return Checkpoint{}, checkpointError(ctx, err)
	}
	return c, nil
}
func (s *SQLCheckpointStore) Claim(ctx context.Context, tenant string, agent agentcore.AgentID, id, scope, owner string, runID agentcore.AgentRunID) (claimed Checkpoint, err error) {
	if strings.TrimSpace(owner) == "" || !validDigest(scope) || runID.IsZero() {
		return claimed, ErrCheckpoint
	}
	key := checkpointKey(tenant, agent, id)
	err = s.store.WithTx(ctx, func(tx db.Tx) error {
		if db.LockRecordKeyTx(ctx, tx, checkpointTable, key) != nil {
			return ErrCheckpoint
		}
		raw, found, e := db.GetRecordTx(ctx, tx, checkpointTable, key)
		if e != nil || !found {
			return ErrCheckpoint
		}
		c, e := decodeCheckpoint(raw)
		if e != nil || c.TenantID != tenant || c.AgentID != agent || c.ID != id || c.ScopeHash != scope {
			return ErrCheckpoint
		}
		if c.Phase == CheckpointToolsPending {
			if err := claimApprovalWait(ctx, tx, c); err != nil {
				return err
			}
		}
		if c.Phase != CheckpointCompleted && c.RunID != runID {
			return ErrCheckpoint
		}
		if c.Phase == CheckpointReady && !c.Deadline.IsZero() && !time.Now().Before(c.Deadline) {
			return context.DeadlineExceeded
		}
		c.Owner = owner
		c.Revision++
		c.UpdatedAt = time.Now().UTC()
		record, e := checkpointRecord(c)
		if e != nil {
			return e
		}
		if db.PutRecordTx(ctx, tx, checkpointTable, key, record) != nil {
			return ErrCheckpoint
		}
		claimed = c
		return nil
	})
	if err != nil {
		return Checkpoint{}, checkpointError(ctx, err)
	}
	return claimed, nil
}
func (s *SQLCheckpointStore) Save(ctx context.Context, c Checkpoint) (saved Checkpoint, err error) {
	key := checkpointKey(c.TenantID, c.AgentID, c.ID)
	err = s.store.WithTx(ctx, func(tx db.Tx) error {
		if db.LockRecordKeyTx(ctx, tx, checkpointTable, key) != nil {
			return ErrCheckpoint
		}
		raw, found, e := db.GetRecordTx(ctx, tx, checkpointTable, key)
		if e != nil || !found {
			return ErrCheckpoint
		}
		old, e := decodeCheckpoint(raw)
		if e != nil || old.ScopeHash != c.ScopeHash || old.ModelIdentity != c.ModelIdentity || old.Owner != c.Owner || old.Revision != c.Revision || old.ID != c.ID || old.TenantID != c.TenantID || old.AgentID != c.AgentID || old.MissionID != c.MissionID || old.TaskID != c.TaskID {
			return ErrCheckpoint
		}
		if c.RunID == old.RunID && ((c.Turn == old.Turn && old.FirstPrompt != c.FirstPrompt) || old.InitialMemoryEvidence != c.InitialMemoryEvidence) {
			return ErrCheckpoint
		}
		if c.Compactions < old.Compactions || c.Compactions > old.Compactions+1 {
			return ErrCheckpoint
		}
		if err := validateCompactionTransition(old, c); err != nil {
			return err
		}
		if c.Usage.InputTokens < old.Usage.InputTokens || c.Usage.OutputTokens < old.Usage.OutputTokens || c.Usage.CachedInputTokens < old.Usage.CachedInputTokens || c.Usage.ReasoningTokens < old.Usage.ReasoningTokens {
			return ErrCheckpoint
		}
		if c.Turn < old.Turn || c.Turn > old.Turn+1 || (c.RunID != old.RunID && old.Phase != CheckpointCompleted) {
			return ErrCheckpoint
		}
		if c.Turn > old.Turn && old.Phase != CheckpointCompleted && (old.Turn != 0 || old.ModelCalls != 0 || old.ToolCalls != 0) {
			return ErrCheckpoint
		}
		if c.Turn == old.Turn && (c.ModelCalls < old.ModelCalls || c.ToolCalls < old.ToolCalls || (!old.Deadline.IsZero() && !c.Deadline.Equal(old.Deadline))) {
			return ErrCheckpoint
		}
		if c.Turn == old.Turn && c.WindowRecoveries < old.WindowRecoveries {
			return ErrCheckpoint
		}
		if c.Turn == old.Turn && old.BlockedTaskReviewed && !c.BlockedTaskReviewed {
			return ErrCheckpoint
		}
		if old.Phase == CheckpointCompleted && (c.Phase != CheckpointReady || c.Turn != old.Turn+1) {
			return ErrCheckpoint
		}
		if old.Phase == CheckpointToolsPending && (c.Phase != CheckpointReady || c.Turn != old.Turn) {
			if old.ApprovalWait == nil || c.Phase != CheckpointToolsPending || c.Turn != old.Turn {
				return ErrCheckpoint
			}
		}
		if err := validateApprovalTransition(old, c); err != nil {
			return err
		}
		if err := maintainPendingTask(ctx, tx, old, c); err != nil {
			return err
		}
		if c.Compactions > old.Compactions {
			if old.Phase != CheckpointReady || c.Phase != CheckpointReady {
				return ErrCheckpoint
			}
			archiveKey, _ := digest([]any{key, old.Revision})
			created, e := db.InsertRecordTx(ctx, tx, compactionArchiveTable, archiveKey, raw)
			if e != nil || !created {
				return ErrCheckpoint
			}
		}
		c.Revision++
		c.UpdatedAt = time.Now().UTC()
		record, e := checkpointRecord(c)
		if e != nil {
			return e
		}
		if db.PutRecordTx(ctx, tx, checkpointTable, key, record) != nil {
			return ErrCheckpoint
		}
		saved = c
		return nil
	})
	if err != nil {
		return Checkpoint{}, checkpointError(ctx, err)
	}
	return saved, nil
}
func checkpointError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == ErrUnsafeResume || err == ErrTaskInputChanged || err == context.DeadlineExceeded {
		return err
	}
	return ErrCheckpoint
}

// Scope changes invalidate persisted data instead of silently reusing a summary
// or cache prefix under a different tenant, principal, grant, model or prompt.
func checkpointScope(req agentcore.RuntimeRequest) (string, error) {
	messages, err := initialMessages(req)
	if err != nil {
		return "", err
	}
	var system []string
	for _, m := range messages {
		if m.Role == llm.System {
			system = append(system, m.Content)
		}
	}
	values := map[string]any{
		"tenant": req.TenantID, "agent": req.AgentID, "principal": req.ExecutionPrincipal,
		"model": req.RuntimeOptions.Model, "profile": req.RuntimeOptions.ProviderSpecific["llm_profile_id"],
		"effort": req.RuntimeOptions.ProviderSpecific["reasoning_effort"], "mission": req.MissionID, "task": req.TaskID,
		"system": system, "system_context": req.RuntimeOptions.SystemContextPrompt, "prompt_blocks": req.RuntimeOptions.PromptBlocks,
		"tools": req.ToolBindings, "skills": req.SkillBindings, "responsibility": req.ContextPack.ResponsibilityContext,
		"workspace": req.RuntimeOptions.WorkspacePolicy, "sandbox": req.RuntimeOptions.SandboxProfile,
		"conversation": req.ContextPack.ConversationID, "discussion": req.ContextPack.DiscussionGroupID,
		"permission_constraints": req.ContextPack.PermissionConstraints, "permission_scope": req.ContextPack.PermissionScope,
		"tool_scope": req.ContextPack.ToolScope, "override_policy": req.ContextPack.OverridePolicy,
		"allowed_tools":   req.RuntimeOptions.ProviderSpecific[agentcore.RuntimeAllowedToolIDsProviderKey],
		"approval_mode":   req.RuntimeOptions.ProviderSpecific[agentcore.RuntimeConversationHILModeProviderKey],
		"explicit_scopes": req.RuntimeOptions.ProviderSpecific[agentcore.RuntimeRequireExplicitToolScopesProviderKey],
		"task_mode":       req.RuntimeOptions.ProviderSpecific["agentcore_task_mode"],
	}
	if policy := req.RuntimeOptions.ProviderSpecific["agentcore_task_recovery"]; policy != "" {
		values["task_recovery"] = policy
	}
	if automaticTask(req) {
		values["input_media"] = req.InputMedia
	}
	return digest(values)
}
func (r *Runner) saveCheckpoint(ctx context.Context, phase CheckpointPhase, messages []*llm.Message) error {
	return r.saveCheckpointWithApproval(ctx, phase, messages, nil)
}

func (r *Runner) saveCheckpointWithApproval(ctx context.Context, phase CheckpointPhase, messages []*llm.Message, approval *ApprovalWait) error {
	if r.checkpoint == nil {
		return nil
	}
	c := *r.checkpoint
	c.Phase = phase
	c.Messages = messages
	c.RunID = r.request.RunID
	c.Turn = r.turn
	c.ModelCalls = r.modelCalls
	c.ToolCalls = r.toolCalls
	c.Compactions = r.compactions
	c.WindowRecoveries = r.windowRecoveries
	c.BlockedTaskReviewed = r.blockedTaskReviewed
	c.Usage = r.usage
	c.Observation = r.observation
	c.CompactionProgress = cloneCompactionProgress(r.compactionProgress)
	c.ApprovalWait = approval
	c.Deadline = r.deadline
	writeCtx, cancel := context.WithTimeout(ctx, r.config.JournalTimeout)
	defer cancel()
	saved, err := r.config.Checkpoints.Save(writeCtx, c)
	if err != nil {
		return err
	}
	r.checkpoint = &saved
	return nil
}
func newCheckpointID() string { return "agentcore-" + uuid.NewString() }
