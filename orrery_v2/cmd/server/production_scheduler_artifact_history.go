package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	agentcore "orrery_v2/internal/agent/core"
	agentidentity "orrery_v2/internal/agent/identity"
	schedulerapp "orrery_v2/internal/app/scheduler"
)

func (s *productionSchedulerArtifactStore) ArchiveHistoricalArtifact(ctx context.Context, access schedulerapp.RunArtifactAccess) (schedulerapp.RunArtifact, error) {
	if err := ctx.Err(); err != nil {
		return schedulerapp.RunArtifact{}, err
	}
	provenance, ok := historicalScheduledArtifactProvenanceFor(access)
	if !ok {
		return schedulerapp.RunArtifact{}, schedulerapp.ErrNotFound
	}
	uploader, ok := s.files.(scheduledArtifactStreamUploader)
	if !ok || s.files == nil {
		return schedulerapp.RunArtifact{}, fmt.Errorf("archive historical scheduled artifact: streaming filestore is not configured")
	}
	workspaceRoots, err := s.historicalWorkspaceRoots(ctx, access)
	if err != nil {
		return schedulerapp.RunArtifact{}, err
	}
	source, err := s.openHistoricalArtifactSource(ctx, access, workspaceRoots, provenance.RuntimeRunID)
	if err != nil {
		return schedulerapp.RunArtifact{}, err
	}
	if provenance.Requirement.MinBytes > 0 && source.SizeBytes < provenance.Requirement.MinBytes {
		source.Reader.Close()
		return schedulerapp.RunArtifact{}, schedulerapp.ErrNotFound
	}
	artifact := access.Artifact
	fileName := cleanArtifactFileName(artifact.FileName, source.FileName, artifact.ID)
	contentType := schedulerArtifactContentType(fileName, firstNonBlank(artifact.ContentType, source.ContentType), nil)
	archived, _, err := s.archiveArtifactReader(
		ctx, uploader, access.Task.TenantID, access.Run.ScheduledRunID, artifact, source, fileName, contentType,
	)
	if err != nil {
		return schedulerapp.RunArtifact{}, fmt.Errorf("archive historical scheduled artifact %q: %w", artifact.ID, err)
	}
	if artifact.SizeBytes > 0 && archived.SizeBytes != artifact.SizeBytes {
		return schedulerapp.RunArtifact{}, schedulerapp.ErrNotFound
	}
	if artifact.SHA256 != "" && !strings.EqualFold(strings.TrimSpace(artifact.SHA256), archived.SHA256) {
		return schedulerapp.RunArtifact{}, schedulerapp.ErrNotFound
	}
	return archived, nil
}

func (s *productionSchedulerArtifactStore) historicalWorkspaceRoots(ctx context.Context, access schedulerapp.RunArtifactAccess) ([]string, error) {
	if s == nil || s.profiles == nil || !filepath.IsAbs(strings.TrimSpace(s.baseDir)) {
		return nil, schedulerapp.ErrNotFound
	}
	agentID := strings.TrimSpace(access.Run.AgentID)
	canonicalRoot := filepath.Clean(filepath.Join(s.baseDir, "workspaces", agentID))
	profile, err := s.profiles.GetProfile(ctx, agentcore.AgentProfileID(agentID))
	if err != nil || !sameScheduledArtifactPath(profile.WorkspaceRoot, canonicalRoot) {
		return nil, schedulerapp.ErrNotFound
	}
	roots := []string{canonicalRoot}
	if access.Task.ScheduledTaskID == migratedMarketingScheduledTaskID && agentID == migratedMarketingScheduledTaskAgentID {
		roots = append(roots, s.migratedMarketingArtifactRoots()...)
	}
	return normalizedAbsoluteRoots(roots), nil
}

func (s *productionSchedulerArtifactStore) openHistoricalArtifactSource(ctx context.Context, access schedulerapp.RunArtifactAccess, workspaceRoots []string, runtimeRunID string) (schedulerapp.RunArtifactContent, error) {
	artifact := access.Artifact
	uri := strings.TrimSpace(artifact.URI)
	if key, ok := s.artifactKeyFromURI(uri); ok {
		runtimePrefix := "agent-runs/" + strings.TrimSpace(runtimeRunID) + "/"
		if !strings.HasPrefix(key, runtimePrefix) || strings.TrimSpace(artifact.ID) != key {
			return schedulerapp.RunArtifactContent{}, schedulerapp.ErrNotFound
		}
		return s.openStoredArtifact(ctx, key, artifact, false)
	}
	if !filepath.IsAbs(uri) || strings.Contains(uri, "://") {
		return schedulerapp.RunArtifactContent{}, schedulerapp.ErrNotFound
	}
	workspaceOwned := false
	for _, root := range workspaceRoots {
		content, matched, err := openScheduledArtifactInRoot(root, uri)
		if !matched {
			continue
		}
		workspaceOwned = true
		if err != nil {
			continue
		}
		if artifact.SizeBytes > 0 && content.SizeBytes != artifact.SizeBytes {
			content.Reader.Close()
			continue
		}
		if !historicalArtifactModificationTimeMatches(access.Run, content.ModifiedAt) {
			content.Reader.Close()
			continue
		}
		return content, nil
	}
	if !workspaceOwned {
		return schedulerapp.RunArtifactContent{}, schedulerapp.ErrNotFound
	}
	return s.recoverHistoricalArtifactFromRuntimeTrace(ctx, access, workspaceRoots, runtimeRunID)
}

func (s *productionSchedulerArtifactStore) historicalArtifactAvailable(
	ctx context.Context,
	access schedulerapp.RunArtifactAccess,
	workspaceRoots []string,
	provenance historicalScheduledArtifactProvenance,
) bool {
	artifact := access.Artifact
	uri := strings.TrimSpace(artifact.URI)
	if key, ok := s.artifactKeyFromURI(uri); ok {
		runtimePrefix := "agent-runs/" + strings.TrimSpace(provenance.RuntimeRunID) + "/"
		if !strings.HasPrefix(key, runtimePrefix) || strings.TrimSpace(artifact.ID) != key {
			return false
		}
		content, err := s.openStoredArtifact(ctx, key, artifact, false)
		if err != nil || content.Reader == nil {
			return false
		}
		return content.Reader.Close() == nil
	}
	if !filepath.IsAbs(uri) || strings.Contains(uri, "://") {
		return false
	}
	workspaceOwned := false
	for _, root := range workspaceRoots {
		content, matched, err := openScheduledArtifactInRoot(root, uri)
		if !matched {
			continue
		}
		workspaceOwned = true
		if err != nil || content.Reader == nil {
			continue
		}
		available := (artifact.SizeBytes <= 0 || content.SizeBytes == artifact.SizeBytes) &&
			(provenance.Requirement.MinBytes <= 0 || content.SizeBytes >= provenance.Requirement.MinBytes) &&
			historicalArtifactModificationTimeMatches(access.Run, content.ModifiedAt) &&
			verifyScheduledArtifactReaderUnchanged(content.Reader) == nil
		closeErr := content.Reader.Close()
		if available && closeErr == nil {
			return true
		}
	}
	if !workspaceOwned {
		return false
	}
	if _, ok := s.approvedHistoricalTraceRecoveryDigest(access, provenance.RuntimeRunID); !ok || s.files == nil {
		return false
	}
	trace, err := s.files.GetReader(ctx, "agent-runs/"+strings.TrimSpace(provenance.RuntimeRunID)+"/runtime-trace.json")
	if err != nil || trace == nil {
		return false
	}
	return trace.Close() == nil
}

func (s *productionSchedulerArtifactStore) recoverHistoricalArtifactFromRuntimeTrace(
	ctx context.Context,
	access schedulerapp.RunArtifactAccess,
	workspaceRoots []string,
	runtimeRunID string,
) (schedulerapp.RunArtifactContent, error) {
	artifact := access.Artifact
	if s == nil || s.files == nil || strings.TrimSpace(runtimeRunID) == "" || artifact.SizeBytes <= 0 ||
		artifact.SizeBytes > maxScheduledArtifactBytes || !historicalArtifactTraceTextType(artifact) {
		return schedulerapp.RunArtifactContent{}, schedulerapp.ErrNotFound
	}
	expectedDigest, ok := s.approvedHistoricalTraceRecoveryDigest(access, runtimeRunID)
	if !ok {
		return schedulerapp.RunArtifactContent{}, schedulerapp.ErrNotFound
	}
	reader, err := s.files.GetReader(ctx, "agent-runs/"+strings.TrimSpace(runtimeRunID)+"/runtime-trace.json")
	if err != nil {
		return schedulerapp.RunArtifactContent{}, schedulerapp.ErrNotFound
	}
	defer reader.Close()
	body, err := io.ReadAll(io.LimitReader(reader, maxScheduledArtifactTraceBytes+1))
	if err != nil || int64(len(body)) > maxScheduledArtifactTraceBytes {
		return schedulerapp.RunArtifactContent{}, schedulerapp.ErrNotFound
	}
	var trace historicalScheduledArtifactRuntimeTrace
	if err := json.Unmarshal(body, &trace); err != nil || strings.TrimSpace(trace.RunID) != strings.TrimSpace(runtimeRunID) ||
		agentidentity.CanonicalProfileID(trace.AgentID) != strings.TrimSpace(access.Run.AgentID) ||
		(strings.TrimSpace(access.Run.TaskID) != "" && strings.TrimSpace(trace.TaskID) != strings.TrimSpace(access.Run.TaskID)) ||
		(strings.TrimSpace(access.Run.MissionID) != "" && strings.TrimSpace(trace.MissionID) != strings.TrimSpace(access.Run.MissionID)) ||
		!historicalArtifactModificationTimeMatches(access.Run, trace.RecordedAt) {
		return schedulerapp.RunArtifactContent{}, schedulerapp.ErrNotFound
	}
	readPaths := historicalArtifactTraceReadPaths(s.baseDir, access.Run.AgentID, artifact.URI, workspaceRoots)
	if len(readPaths) == 0 {
		return schedulerapp.RunArtifactContent{}, schedulerapp.ErrNotFound
	}
	var recovered string
	found := false
	for _, call := range trace.ToolCalls {
		if call.Name != "Shell" || !historicalArtifactTraceCommandMatches(call.Input.Command, readPaths) ||
			call.Input.MaxOutputLen < artifact.SizeBytes {
			continue
		}
		var output historicalScheduledArtifactShellOutput
		if err := json.Unmarshal([]byte(call.Output), &output); err != nil || output.ExitCode != 0 || output.Truncated || output.Stderr != "" ||
			int64(len([]byte(output.Stdout))) != artifact.SizeBytes || scheduledArtifactDigest([]byte(output.Stdout)) != expectedDigest {
			continue
		}
		if found && recovered != output.Stdout {
			return schedulerapp.RunArtifactContent{}, schedulerapp.ErrNotFound
		}
		recovered = output.Stdout
		found = true
	}
	if !found {
		return schedulerapp.RunArtifactContent{}, schedulerapp.ErrNotFound
	}
	fileName := cleanArtifactFileName(artifact.FileName, filepath.Base(artifact.URI), artifact.ID)
	header := []byte(recovered)
	if len(header) > 512 {
		header = header[:512]
	}
	return schedulerapp.RunArtifactContent{
		Reader: io.NopCloser(strings.NewReader(recovered)), FileName: fileName,
		ContentType: schedulerArtifactContentType(fileName, artifact.ContentType, header),
		SizeBytes:   int64(len([]byte(recovered))), ModifiedAt: trace.RecordedAt,
	}, nil
}

type historicalScheduledArtifactRecoveryManifest struct {
	Version    int                                       `json:"version"`
	Recoveries []historicalScheduledArtifactRecoveryRule `json:"recoveries"`
	Signature  string                                    `json:"signature"`
}

type historicalScheduledArtifactRecoveryPayload struct {
	Version    int                                       `json:"version"`
	Recoveries []historicalScheduledArtifactRecoveryRule `json:"recoveries"`
}

type historicalScheduledArtifactRecoveryRule struct {
	ScheduledTaskID string `json:"scheduled_task_id"`
	ScheduledRunID  string `json:"scheduled_run_id"`
	RuntimeRunID    string `json:"runtime_run_id"`
	ArtifactID      string `json:"artifact_id"`
	EvidenceID      string `json:"evidence_id"`
	SourceURI       string `json:"source_uri"`
	SizeBytes       int64  `json:"size_bytes"`
	SHA256          string `json:"sha256"`
}

func (s *productionSchedulerArtifactStore) approvedHistoricalTraceRecoveryDigest(access schedulerapp.RunArtifactAccess, runtimeRunID string) (string, bool) {
	if s == nil || !filepath.IsAbs(strings.TrimSpace(s.baseDir)) {
		return "", false
	}
	configRoot := filepath.Join(filepath.Clean(s.baseDir), "config")
	manifestPath := filepath.Join(configRoot, historicalArtifactRecoveryRulesFile)
	content, matched, err := openScheduledArtifactInRoot(configRoot, manifestPath)
	if err != nil || !matched || content.Reader == nil || content.SizeBytes < 0 || content.SizeBytes > maxScheduledArtifactRecoveryRulesBytes {
		return "", false
	}
	defer content.Reader.Close()
	if _, ok := content.Reader.(*schedulerArtifactFileReadCloser); !ok {
		return "", false
	}
	body, err := io.ReadAll(io.LimitReader(content.Reader, maxScheduledArtifactRecoveryRulesBytes+1))
	if err != nil || int64(len(body)) > maxScheduledArtifactRecoveryRulesBytes || verifyScheduledArtifactReaderUnchanged(content.Reader) != nil {
		return "", false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var manifest historicalScheduledArtifactRecoveryManifest
	if err := decoder.Decode(&manifest); err != nil || manifest.Version != 1 {
		return "", false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return "", false
	}
	payload, err := json.Marshal(historicalScheduledArtifactRecoveryPayload{
		Version: manifest.Version, Recoveries: manifest.Recoveries,
	})
	if err != nil {
		return "", false
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(manifest.Signature))
	if err != nil || len(s.recoveryPublicKey) != ed25519.PublicKeySize || len(signature) != ed25519.SignatureSize ||
		!ed25519.Verify(s.recoveryPublicKey, payload, signature) {
		return "", false
	}

	matchedDigest := ""
	seen := map[string]bool{}
	for _, rule := range manifest.Recoveries {
		digest := strings.ToLower(strings.TrimSpace(rule.SHA256))
		decoded, decodeErr := hex.DecodeString(digest)
		fields := []string{
			strings.TrimSpace(rule.ScheduledTaskID), strings.TrimSpace(rule.ScheduledRunID), strings.TrimSpace(rule.RuntimeRunID),
			strings.TrimSpace(rule.ArtifactID), strings.TrimSpace(rule.EvidenceID), strings.TrimSpace(rule.SourceURI),
		}
		identity := strings.Join([]string{
			fields[0], fields[1], fields[2], fields[3], fields[4], fields[5],
		}, "\x00")
		validFields := true
		for _, field := range fields {
			if field == "" || strings.ContainsRune(field, '\x00') {
				validFields = false
				break
			}
		}
		if decodeErr != nil || len(decoded) != sha256.Size || rule.SizeBytes <= 0 || rule.SizeBytes > maxScheduledArtifactBytes ||
			!validFields || !filepath.IsAbs(fields[5]) || seen[identity] {
			return "", false
		}
		seen[identity] = true
		if fields[0] == strings.TrimSpace(access.Task.ScheduledTaskID) &&
			fields[1] == strings.TrimSpace(access.Run.ScheduledRunID) &&
			fields[2] == strings.TrimSpace(runtimeRunID) &&
			fields[3] == strings.TrimSpace(access.Artifact.ID) &&
			fields[4] == strings.TrimSpace(access.Artifact.EvidenceID) &&
			fields[5] == strings.TrimSpace(access.Artifact.URI) &&
			rule.SizeBytes == access.Artifact.SizeBytes {
			if matchedDigest != "" {
				return "", false
			}
			matchedDigest = digest
		}
	}
	return matchedDigest, matchedDigest != ""
}

func historicalArtifactRecoveryPublicKey() ed25519.PublicKey {
	der, err := base64.StdEncoding.Strict().DecodeString(historicalArtifactRecoveryPublicKeyDER)
	if err != nil {
		panic("decode historical artifact recovery public key: " + err.Error())
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		panic("parse historical artifact recovery public key: " + err.Error())
	}
	publicKey, ok := parsed.(ed25519.PublicKey)
	if !ok || len(publicKey) != ed25519.PublicKeySize {
		panic("historical artifact recovery public key is not Ed25519")
	}
	return append(ed25519.PublicKey(nil), publicKey...)
}

func scheduledArtifactDigest(body []byte) string {
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

type historicalScheduledArtifactRuntimeTrace struct {
	RunID      string                                `json:"run_id"`
	AgentID    string                                `json:"agent_id"`
	MissionID  string                                `json:"mission_id"`
	TaskID     string                                `json:"task_id"`
	RecordedAt time.Time                             `json:"recorded_at"`
	ToolCalls  []historicalScheduledArtifactToolCall `json:"tool_calls"`
}

type historicalScheduledArtifactToolCall struct {
	Name  string `json:"Name"`
	Input struct {
		Command      string `json:"command"`
		MaxOutputLen int64  `json:"max_output_len"`
	} `json:"Input"`
	Output string `json:"Output"`
}

type historicalScheduledArtifactShellOutput struct {
	ExitCode  int    `json:"exit_code"`
	Stderr    string `json:"stderr"`
	Stdout    string `json:"stdout"`
	Truncated bool   `json:"truncated"`
}

func historicalArtifactTraceReadPaths(baseDir, agentID, artifactURI string, workspaceRoots []string) []string {
	seen := map[string]bool{}
	var paths []string
	add := func(candidate string) {
		candidate = filepath.Clean(strings.TrimSpace(candidate))
		if !filepath.IsAbs(candidate) || unsafeHistoricalArtifactTracePath(candidate) || seen[candidate] {
			return
		}
		seen[candidate] = true
		paths = append(paths, candidate)
	}
	for _, root := range workspaceRoots {
		relative, ok := scheduledArtifactRelativePath(root, artifactURI)
		if !ok {
			continue
		}
		add(artifactURI)
		legacyAgentID := agentidentity.LegacyProfileID(agentID)
		if legacyAgentID != "" && !strings.EqualFold(legacyAgentID, strings.TrimSpace(agentID)) {
			add(filepath.Join(baseDir, "workspaces", legacyAgentID, relative))
		}
	}
	return paths
}

func unsafeHistoricalArtifactTracePath(candidate string) bool {
	if strings.ContainsAny(candidate, "\r\n\x00\"$`") || (filepath.Separator == '/' && strings.Contains(candidate, "\\")) {
		return true
	}
	for _, char := range candidate {
		if unicode.IsControl(char) {
			return true
		}
	}
	return false
}

func historicalArtifactTraceTextType(artifact schedulerapp.RunArtifact) bool {
	extension := strings.ToLower(filepath.Ext(cleanArtifactFileName(artifact.FileName, artifact.URI)))
	switch extension {
	case ".md", ".markdown", ".txt", ".log", ".json", ".csv", ".yaml", ".yml":
		return true
	default:
		return false
	}
}

func historicalArtifactTraceCommandMatches(command string, paths []string) bool {
	command = strings.TrimSpace(command)
	for _, candidate := range paths {
		if command == "cat \""+candidate+"\"" {
			return true
		}
	}
	return false
}

func scheduledArtifactRelativePath(rootDir, absolutePath string) (string, bool) {
	rootDir = filepath.Clean(strings.TrimSpace(rootDir))
	absolutePath = filepath.Clean(strings.TrimSpace(absolutePath))
	if !filepath.IsAbs(rootDir) || !filepath.IsAbs(absolutePath) {
		return "", false
	}
	relative, err := filepath.Rel(rootDir, absolutePath)
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return relative, true
}

type historicalScheduledArtifactProvenance struct {
	Requirement  schedulerapp.ArtifactRequirement
	RuntimeRunID string
}

func historicalScheduledArtifactProvenanceFor(access schedulerapp.RunArtifactAccess) (historicalScheduledArtifactProvenance, bool) {
	task := access.Task
	run := access.Run
	artifact := access.Artifact
	tenantID := canonicalScheduledArtifactTenant(task.TenantID)
	if tenantID != "system" || !strings.HasPrefix(strings.TrimSpace(task.ScheduledTaskID), "orrery-native:") ||
		strings.TrimSpace(run.ScheduledTaskID) != strings.TrimSpace(task.ScheduledTaskID) ||
		run.Status != schedulerapp.RunStatusCompleted || run.TargetMode != schedulerapp.TargetModeAgentTask ||
		strings.TrimSpace(run.AgentID) == "" ||
		canonicalScheduledArtifactTenant(run.TenantID) != tenantID || strings.TrimSpace(artifact.ID) == "" || strings.TrimSpace(artifact.URI) == "" {
		return historicalScheduledArtifactProvenance{}, false
	}
	if _, durable := scheduledArtifactKeyFromTypedURI(artifact.URI); durable {
		return historicalScheduledArtifactProvenance{}, false
	}
	configuration, err := schedulerapp.ResolveScheduledRunConfiguration(task, run)
	if err != nil || configuration.Target.Mode != schedulerapp.TargetModeAgentTask ||
		strings.TrimSpace(configuration.Target.AgentID) != strings.TrimSpace(run.AgentID) {
		return historicalScheduledArtifactProvenance{}, false
	}
	for _, requirement := range configuration.Target.ResultContract.Artifacts {
		requirementID := strings.TrimSpace(requirement.ID)
		artifactSuffix := strings.TrimPrefix(strings.TrimSpace(artifact.ID), "scheduled-artifact:")
		evidenceSuffix := strings.TrimPrefix(strings.TrimSpace(artifact.EvidenceID), "scheduled-evidence:")
		requirementSuffix := ":" + requirementID
		if artifactSuffix == strings.TrimSpace(artifact.ID) || evidenceSuffix == strings.TrimSpace(artifact.EvidenceID) ||
			artifactSuffix != evidenceSuffix || requirementID == "" || !strings.HasSuffix(artifactSuffix, requirementSuffix) {
			continue
		}
		runtimeRunID := strings.TrimSuffix(artifactSuffix, requirementSuffix)
		if runtimeRunID == "" || (strings.TrimSpace(run.RuntimeRunID) != "" && strings.TrimSpace(run.RuntimeRunID) != runtimeRunID) {
			continue
		}
		if pattern := strings.TrimSpace(requirement.PathPattern); pattern != "" {
			matched, err := filepath.Match(pattern, strings.TrimSpace(artifact.URI))
			if err != nil || !matched {
				continue
			}
		}
		if kind := strings.TrimSpace(requirement.Kind); kind != "" && kind != strings.TrimSpace(artifact.Kind) {
			continue
		}
		return historicalScheduledArtifactProvenance{Requirement: requirement, RuntimeRunID: runtimeRunID}, true
	}
	return historicalScheduledArtifactProvenance{}, false
}

func historicalArtifactModificationTimeMatches(run schedulerapp.ScheduledRun, modifiedAt time.Time) bool {
	if modifiedAt.IsZero() || run.CompletedAt.IsZero() {
		return false
	}
	if modifiedAt.After(run.CompletedAt.Add(2 * time.Second)) {
		return false
	}
	return run.StartedAt.IsZero() || !modifiedAt.Before(run.StartedAt.Add(-2*time.Second))
}

func sameScheduledArtifactPath(left, right string) bool {
	left = filepath.Clean(strings.TrimSpace(left))
	right = filepath.Clean(strings.TrimSpace(right))
	if left == "." || right == "." || !filepath.IsAbs(left) || !filepath.IsAbs(right) {
		return false
	}
	return strings.EqualFold(left, right)
}
