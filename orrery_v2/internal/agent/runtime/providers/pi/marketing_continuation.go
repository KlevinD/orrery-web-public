package pi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/providers/toolbridge"
)

const (
	marketingContinuationTool        = "marketing_wechat_draft_create"
	marketingContinuationMaxJSON     = 1 << 20
	marketingContinuationMaxMarkdown = 128 * 1024
	marketingContinuationMaxImage    = 20 * 1024 * 1024
)

type marketingContinuationCheckpoint struct {
	IntentID           string   `json:"intent_id"`
	AccountID          string   `json:"account_id"`
	Status             string   `json:"status"`
	Operation          string   `json:"operation"`
	IntentHash         string   `json:"intent_hash"`
	Title              string   `json:"title"`
	Author             string   `json:"author"`
	Digest             string   `json:"digest"`
	ArticleMDPath      string   `json:"article_md_path"`
	CoverPath          string   `json:"cover_path"`
	ShareThumbnailPath string   `json:"share_thumbnail_path"`
	BodyImagePaths     []string `json:"body_image_paths"`
	DraftOnly          bool     `json:"draft_only"`
	WechatMediaID      any      `json:"wechat_media_id"`
	Receipt            any      `json:"receipt"`
}

type marketingContinuationMaterial struct {
	Version      int                                     `json:"material_version"`
	Kind         string                                  `json:"material_kind"`
	Title        string                                  `json:"title"`
	Summary      string                                  `json:"summary"`
	Content      string                                  `json:"content"`
	ContentHash  string                                  `json:"content_hash"`
	MaterialHash string                                  `json:"material_hash"`
	Fields       map[string]string                       `json:"fields"`
	Artifacts    []marketingContinuationMaterialArtifact `json:"artifacts"`
}

type marketingContinuationMaterialArtifact struct {
	Role      string `json:"role"`
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	MediaType string `json:"media_type"`
	SHA256    string `json:"sha256"`
}

type marketingContinuationWorkspace struct {
	logicalRoot  string
	resolvedRoot string
}

func isDeterministicMarketingContinuation(opts runtimecore.ExecOptions) bool {
	return validateDeterministicMarketingContinuation(opts) == nil
}

func isMarketingContinuationRequested(opts runtimecore.ExecOptions) bool {
	values := opts.ProviderSpecific
	return parseBool(values["approval_continuation"]) &&
		(strings.TrimSpace(values["approval_material_kind"]) == "wechat_draft" ||
			isMarketingWechatDraftTool(values[runtimecore.ApprovalMetadataToolName]))
}

func validateDeterministicMarketingContinuation(opts runtimecore.ExecOptions) error {
	values := opts.ProviderSpecific
	checks := []bool{
		parseBool(values["approval_continuation"]),
		values["approval_decision"] == string(runtimecore.ApprovalDecisionApproved),
		values["approval_action_id"] == "approve_once",
		values["hil_receipt_effect"] == "authorize",
		strings.TrimSpace(values["approval_material_kind"]) == "wechat_draft",
		isMarketingWechatDraftTool(values[runtimecore.ApprovalMetadataToolName]),
	}
	for _, valid := range checks {
		if !valid {
			return fmt.Errorf("approved marketing continuation metadata is incomplete")
		}
	}
	return nil
}

func openDeterministicMarketingContinuationSession(parent context.Context, native NativeConfig, runtimeConfig Config, spec agentcore.SessionSpec, config agentcore.SessionConfig, workspace NativeWorkspace, opts runtimecore.ExecOptions) *NativeSession {
	ctx, cancel := context.WithCancel(parent)
	session := &NativeSession{
		sessionID: spec.SessionID, spec: spec, config: config, workspace: workspace, native: native, runtime: runtimeConfig,
		opts: opts, version: firstNonEmpty(native.Version, supportedNativePiVersion), now: native.Now, ctx: ctx, cancel: cancel,
		done: make(chan struct{}), events: make(chan agentcore.SessionEnvelope, native.EventCapacity+2),
		eventIn: make(chan agentcore.SessionEnvelope, native.EventCapacity), eventAbort: make(chan struct{}), eventPumpDone: make(chan struct{}),
		terminated: make(map[uint64]bool), clientIDs: make(map[string]uint64), models: make(map[string]piNativeModel),
		pending: make(map[string]piPendingRequest), approvals: make(map[string]piNativeApproval), ui: make(map[string]piNativeUIRequest),
		capability:                         deterministicMarketingContinuationCapabilities(firstNonEmpty(native.Version, supportedNativePiVersion)),
		deterministicMarketingContinuation: true,
	}
	session.capability.CurrentModel = strings.TrimSpace(config.Model)
	session.mapper.reset(0)
	go session.runEventPump()
	return session
}

func (s *NativeSession) dispatchDeterministicMarketingContinuation(_ context.Context, command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	if _, _, err := piPromptPayload(command.Send.Content); err != nil {
		return agentcore.CommandReceipt{}, err
	}
	s.mu.Lock()
	if s.active || s.deterministicContinuationUsed {
		s.mu.Unlock()
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "approved marketing continuation is single-use", false)
	}
	generation := s.generation + 1
	s.generation = generation
	s.active = true
	s.cancelled = false
	s.terminated[generation] = false
	s.deterministicContinuationUsed = true
	s.mu.Unlock()

	clientMsgID := strings.TrimSpace(command.Send.Metadata.ClientMsgID)
	go s.runDeterministicMarketingContinuation(s.ctx, generation, clientMsgID)
	return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionStarted, TurnGeneration: generation}, nil
}

func deterministicMarketingContinuationCapabilities(version string) agentcore.Capabilities {
	return agentcore.Capabilities{
		Tier:              agentcore.CapabilityParsed,
		SupportedCommands: agentcore.CommandSet{Send: true},
		PromptBlocks:      agentcore.BlockSet{Text: true},
		PromptAccepted:    agentcore.PromptAcceptedNative,
		Signals:           agentcore.SignalSet{ToolLifecycle: true, TerminalResult: true},
		PendingLimit:      1,
		PayloadLimitBytes: piRPCMaxRecordBytes,
		Transport: agentcore.TransportEvidence{
			Provider: "pi", Protocol: "orrery-approved-continuation", Version: strings.TrimSpace(version),
		},
	}
}

func (s *NativeSession) runDeterministicMarketingContinuation(dispatchCtx context.Context, generation uint64, clientMsgID string) {
	if clientMsgID != "" {
		s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventPromptAccepted, PromptAccepted: &agentcore.PromptAcceptedEvent{ClientMsgID: clientMsgID}})
	}
	input, err := deterministicMarketingContinuationInput(s.opts)
	callID := "hil-wechat-draft-" + strings.TrimSpace(s.opts.ProviderSpecific["hil_receipt_id"])
	callID = safePiIdentifier(callID, "hil-wechat-draft")
	if err == nil {
		raw, _ := json.Marshal(input)
		s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventItemStarted, ItemStarted: &agentcore.ItemStartedEvent{ItemID: callID, Kind: agentcore.ItemTool}})
		s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventToolCall, ToolCall: &agentcore.ToolCallEvent{ToolUseID: callID, Name: marketingContinuationTool, Input: raw}})
	}

	var output map[string]any
	if err == nil {
		output, err = s.callDeterministicMarketingContinuation(dispatchCtx, callID, input)
	}
	failed := err != nil || strings.EqualFold(strings.TrimSpace(fmt.Sprint(output["status"])), "failed")
	if err == nil && failed {
		err = fmt.Errorf("%s", firstNonEmpty(strings.TrimSpace(fmt.Sprint(output["error"])), "marketing draft tool failed"))
	}
	resultText := deterministicMarketingContinuationResultText(output, err)
	if input != nil {
		s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventToolResult, ToolResult: &agentcore.ToolResultEvent{
			ToolUseID: callID, IsError: failed, Content: []agentcore.ToolResultContent{{Kind: agentcore.ToolResultText, Text: resultText}},
		}})
		s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventItemCompleted, ItemCompleted: &agentcore.ItemCompletedEvent{ItemID: callID}})
	}

	s.mu.Lock()
	cancelled := s.cancelled
	s.active = false
	s.terminated[generation] = true
	s.mu.Unlock()
	outcome := agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeEndTurn}
	if failed {
		outcome.Kind = agentcore.TurnOutcomeFailed
	}
	if cancelled {
		failed = true
		outcome = agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeCancelled, CancelReason: agentcore.CancelReasonUserCancel}
	}
	s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventTurnResult, TurnResult: &agentcore.TurnResultEvent{
		Epoch: generation, IsError: failed, ResultText: resultText, Outcome: outcome,
	}})
}

func (s *NativeSession) callDeterministicMarketingContinuation(ctx context.Context, callID string, input map[string]any) (map[string]any, error) {
	dispatcher := s.runtime.ToolDispatcher
	if dispatcher == nil {
		return nil, fmt.Errorf("runtime tool dispatcher is not configured")
	}
	bindingName := firstNonEmpty(
		strings.TrimSpace(s.opts.ProviderSpecific[runtimecore.ApprovalMetadataToolName]),
		marketingContinuationTool,
	)
	binding, ok := toolbridge.RuntimeToolBindingForCall(bindingName, s.opts.ToolBindings)
	if !ok || !isMarketingWechatDraftTool(firstNonEmpty(binding.Alias, string(binding.ToolID), binding.DownstreamToolName)) {
		return nil, fmt.Errorf("approved marketing draft tool is not bound")
	}
	bridge := &piBridgeServer{now: s.now}
	response, handled, err := bridge.consumeMarketingContinuationApproval(marketingContinuationTool, input, s.opts)
	if err != nil {
		return nil, err
	}
	if !handled || response.Decision != runtimecore.ApprovalDecisionApproved {
		return nil, fmt.Errorf("approved marketing continuation was not accepted")
	}
	if err := bridge.storeMarketingApprovalGrant(callID, marketingContinuationTool, input, response); err != nil {
		return nil, err
	}
	requestMeta, err := bridge.consumeMarketingApprovalGrant(callID, marketingContinuationTool, input)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = s.ctx
	}
	result, err := dispatcher.CallRuntimeTool(ctx, runtimecore.RuntimeToolCallRequest{
		TenantID: strings.TrimSpace(s.opts.TenantID), RunID: s.opts.RunID, AgentID: s.opts.AgentID,
		ExecutionEnvelope:  agentcore.CloneExecutionEnvelope(s.opts.ExecutionEnvelope),
		ExecutionPrincipal: s.opts.ExecutionPrincipal, Name: firstNonEmpty(string(binding.ToolID), marketingContinuationTool), CallID: callID,
		Input: cloneInput(input), RequestMeta: requestMeta, ToolBindings: cloneToolBindings(s.opts.ToolBindings),
		SkillBindings:         append([]agentcore.RuntimeSkillBinding(nil), s.opts.SkillBindings...),
		ResponsibilityContext: agentcore.CloneResponsibilityContext(s.opts.ContextPack.ResponsibilityContext),
		ProviderSpecific:      cloneStringMap(s.opts.ProviderSpecific), WorkspaceRoot: strings.TrimSpace(s.opts.WorkspacePolicy.Root),
		WorkingDirectory: firstNonEmpty(s.opts.Cwd, s.opts.WorkspacePolicy.WorkDir), WorkspacePolicy: cloneWorkspacePolicy(s.opts.WorkspacePolicy),
	})
	if err != nil {
		return nil, err
	}
	if result.Output == nil {
		return map[string]any{}, nil
	}
	return result.Output, nil
}

func deterministicMarketingContinuationInput(opts runtimecore.ExecOptions) (map[string]any, error) {
	values := opts.ProviderSpecific
	intentID := strings.TrimSpace(values["approval_material_intent_id"])
	if intentID == "" || filepath.Base(intentID) != intentID || strings.ContainsAny(intentID, `/\`) {
		return nil, fmt.Errorf("approved marketing intent id is invalid")
	}
	workspace, err := newMarketingContinuationWorkspace(firstNonEmpty(opts.WorkspacePolicy.Root, opts.Cwd, opts.WorkspacePolicy.WorkDir))
	if err != nil {
		return nil, err
	}
	checkpointPath := filepath.Join(workspace.logicalRoot, "wechat", "publish-intents", intentID+".json")
	resolvedCheckpoint, _, info, err := workspace.resolveFile(checkpointPath)
	if err != nil {
		return nil, fmt.Errorf("resolve approved marketing checkpoint: %w", err)
	}
	if info.Size() > marketingContinuationMaxJSON {
		return nil, fmt.Errorf("approved marketing checkpoint is too large")
	}
	raw, err := os.ReadFile(resolvedCheckpoint)
	if err != nil {
		return nil, fmt.Errorf("read approved marketing checkpoint: %w", err)
	}
	var checkpoint marketingContinuationCheckpoint
	if err := json.Unmarshal(raw, &checkpoint); err != nil {
		return nil, fmt.Errorf("decode approved marketing checkpoint: %w", err)
	}
	if !checkpoint.DraftOnly || checkpoint.Operation != "create_draft" || checkpoint.WechatMediaID != nil || checkpoint.Receipt != nil {
		return nil, fmt.Errorf("approved marketing checkpoint is not an unconsumed draft-only operation")
	}
	for actual, key := range map[string]string{
		checkpoint.AccountID: "approval_material_account_id", checkpoint.IntentID: "approval_material_intent_id", checkpoint.IntentHash: "approval_material_intent_hash",
	} {
		if actual == "" || actual != strings.TrimSpace(values[key]) {
			return nil, fmt.Errorf("approved marketing checkpoint binding does not match")
		}
	}
	material, err := buildMarketingContinuationMaterial(workspace, checkpoint)
	if err != nil {
		return nil, err
	}
	if material.MaterialHash != strings.TrimSpace(values["approval_material_hash"]) {
		return nil, fmt.Errorf("approved marketing material hash does not match current files")
	}
	toolsRoot, err := marketingContinuationToolsRoot(opts, workspace.logicalRoot)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"tools_root": toolsRoot, "account_id": checkpoint.AccountID, "intent_id": checkpoint.IntentID, "intent_hash": checkpoint.IntentHash,
		"article_md_path": checkpoint.ArticleMDPath, "cover_path": checkpoint.CoverPath, "share_thumbnail_path": checkpoint.ShareThumbnailPath,
		"body_image_paths": append([]string(nil), checkpoint.BodyImagePaths...), "title": checkpoint.Title, "author": checkpoint.Author, "digest": checkpoint.Digest,
	}, nil
}

func buildMarketingContinuationMaterial(workspace marketingContinuationWorkspace, checkpoint marketingContinuationCheckpoint) (marketingContinuationMaterial, error) {
	content, articlePath, err := workspace.readMarkdown(checkpoint.ArticleMDPath)
	if err != nil {
		return marketingContinuationMaterial{}, fmt.Errorf("read approved marketing article: %w", err)
	}
	refs := [][2]string{{"cover", checkpoint.CoverPath}}
	if strings.TrimSpace(checkpoint.ShareThumbnailPath) != "" {
		refs = append(refs, [2]string{"share_thumbnail", checkpoint.ShareThumbnailPath})
	}
	for _, path := range checkpoint.BodyImagePaths {
		refs = append(refs, [2]string{"body_image", path})
	}
	if len(refs) < 1 || len(refs) > 16 {
		return marketingContinuationMaterial{}, fmt.Errorf("approved marketing asset manifest is invalid")
	}
	artifacts := make([]marketingContinuationMaterialArtifact, 0, len(refs))
	for _, ref := range refs {
		artifact, err := workspace.imageArtifact(ref[0], ref[1])
		if err != nil {
			return marketingContinuationMaterial{}, fmt.Errorf("verify approved marketing %s: %w", ref[0], err)
		}
		artifacts = append(artifacts, artifact)
	}
	sort.Slice(artifacts, func(i, j int) bool {
		if artifacts[i].Path == artifacts[j].Path {
			return artifacts[i].Role < artifacts[j].Role
		}
		return artifacts[i].Path < artifacts[j].Path
	})
	material := marketingContinuationMaterial{
		Version: 1, Kind: "wechat_draft", Title: strings.TrimSpace(checkpoint.Title), Summary: strings.TrimSpace(checkpoint.Digest),
		Content: content, ContentHash: marketingContinuationHash([]byte(content)),
		Fields: map[string]string{"channel": "wechat", "operation": "draft_create", "account_id": strings.TrimSpace(checkpoint.AccountID),
			"intent_id": strings.TrimSpace(checkpoint.IntentID), "intent_hash": strings.TrimSpace(checkpoint.IntentHash), "article_md_path": articlePath},
		Artifacts: artifacts,
	}
	if author := strings.TrimSpace(checkpoint.Author); author != "" {
		material.Fields["author"] = author
	}
	raw, err := json.Marshal(material)
	if err != nil {
		return marketingContinuationMaterial{}, fmt.Errorf("encode approved marketing material: %w", err)
	}
	material.MaterialHash = marketingContinuationHash(raw)
	return material, nil
}

func newMarketingContinuationWorkspace(root string) (marketingContinuationWorkspace, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return marketingContinuationWorkspace{}, fmt.Errorf("approved marketing workspace root is required")
	}
	logical, err := filepath.Abs(root)
	if err != nil {
		return marketingContinuationWorkspace{}, fmt.Errorf("resolve approved marketing workspace: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(logical)
	if err != nil {
		return marketingContinuationWorkspace{}, fmt.Errorf("resolve approved marketing workspace: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return marketingContinuationWorkspace{}, fmt.Errorf("approved marketing workspace is unavailable")
	}
	return marketingContinuationWorkspace{logicalRoot: filepath.Clean(logical), resolvedRoot: filepath.Clean(resolved)}, nil
}

func (workspace marketingContinuationWorkspace) resolveFile(path string) (string, string, os.FileInfo, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", "", nil, fmt.Errorf("path is required")
	}
	logical := path
	if !filepath.IsAbs(logical) {
		logical = filepath.Join(workspace.logicalRoot, logical)
	}
	logical = filepath.Clean(logical)
	relative, err := filepath.Rel(workspace.logicalRoot, logical)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", "", nil, fmt.Errorf("path is outside the runtime workspace")
	}
	expected := filepath.Join(workspace.resolvedRoot, relative)
	info, err := os.Lstat(expected)
	if err != nil {
		return "", "", nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", "", nil, fmt.Errorf("path is not a regular non-symlink file")
	}
	resolved, err := filepath.EvalSymlinks(expected)
	if err != nil || filepath.Clean(resolved) != filepath.Clean(expected) {
		return "", "", nil, fmt.Errorf("symlinked paths are not allowed")
	}
	return resolved, filepath.ToSlash(relative), info, nil
}

func (workspace marketingContinuationWorkspace) readMarkdown(path string) (string, string, error) {
	resolved, relative, info, err := workspace.resolveFile(path)
	if err != nil {
		return "", "", err
	}
	if info.Size() > marketingContinuationMaxMarkdown {
		return "", "", fmt.Errorf("Markdown is too large")
	}
	file, err := os.Open(resolved)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, marketingContinuationMaxMarkdown+1))
	if err != nil || len(raw) > marketingContinuationMaxMarkdown || !utf8.Valid(raw) {
		return "", "", fmt.Errorf("Markdown is invalid")
	}
	return string(raw), relative, nil
}

func (workspace marketingContinuationWorkspace) imageArtifact(role, path string) (marketingContinuationMaterialArtifact, error) {
	resolved, relative, info, err := workspace.resolveFile(path)
	if err != nil {
		return marketingContinuationMaterialArtifact{}, err
	}
	if info.Size() > marketingContinuationMaxImage {
		return marketingContinuationMaterialArtifact{}, fmt.Errorf("image is too large")
	}
	mediaType := mime.TypeByExtension(strings.ToLower(filepath.Ext(relative)))
	if separator := strings.IndexByte(mediaType, ';'); separator >= 0 {
		mediaType = mediaType[:separator]
	}
	if !strings.HasPrefix(mediaType, "image/") {
		return marketingContinuationMaterialArtifact{}, fmt.Errorf("artifact is not a recognized image")
	}
	file, err := os.Open(resolved)
	if err != nil {
		return marketingContinuationMaterialArtifact{}, err
	}
	defer file.Close()
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, marketingContinuationMaxImage+1))
	if err != nil || written > marketingContinuationMaxImage {
		return marketingContinuationMaterialArtifact{}, fmt.Errorf("hash approved image")
	}
	return marketingContinuationMaterialArtifact{Role: role, Path: relative, SizeBytes: written, MediaType: mediaType, SHA256: "sha256:" + hex.EncodeToString(hash.Sum(nil))}, nil
}

func marketingContinuationToolsRoot(opts runtimecore.ExecOptions, workspaceRoot string) (string, error) {
	if configured := strings.TrimSpace(opts.Environment["ORRERY_MARKETING_TOOLS_ROOT"]); configured != "" {
		if info, err := os.Stat(filepath.Clean(configured)); err == nil && info.IsDir() {
			return filepath.Clean(configured), nil
		}
	}
	path := filepath.Clean(workspaceRoot)
	for {
		if strings.EqualFold(filepath.Base(path), "workspaces") {
			root := filepath.Dir(path)
			if info, err := os.Stat(root); err == nil && info.IsDir() {
				return root, nil
			}
		}
		parent := filepath.Dir(path)
		if parent == path {
			break
		}
		path = parent
	}
	return "", fmt.Errorf("approved marketing tools root could not be resolved")
}

func deterministicMarketingContinuationResultText(output map[string]any, err error) string {
	if err != nil {
		return sanitizePiText(err.Error())
	}
	raw, marshalErr := json.Marshal(output)
	if marshalErr != nil {
		return "WeChat draft created"
	}
	return sanitizePiText("WeChat draft created: " + string(raw))
}

func marketingContinuationHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
