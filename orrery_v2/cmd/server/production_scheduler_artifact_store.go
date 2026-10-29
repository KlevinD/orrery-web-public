package main

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/filestore"
)

const (
	maxScheduledArtifactBytes              int64 = 64 << 20
	maxScheduledArtifactTraceBytes         int64 = 16 << 20
	maxScheduledArtifactRecoveryRulesBytes int64 = 64 << 10
	historicalArtifactRecoveryRulesFile          = "scheduled-artifact-trace-recovery-v1.json"
	historicalArtifactRecoveryPublicKeyDER       = "MCowBQYDK2VwAyEAKHxkQ78ZspTjaYIBET6pzIcyRqbcE9eHQvkNG9CQz8I="
	migratedMarketingScheduledTaskID             = "orrery-native:79697eee-14fd-499a-bcc6-7b0ca5dbed6d"
	migratedMarketingScheduledTaskAgentID        = "A000005"
)

type scheduledArtifactProfileStore interface {
	GetProfile(context.Context, agentcore.AgentProfileID) (agentcore.AgentProfile, error)
}

type scheduledArtifactStreamUploader interface {
	UploadFromReader(context.Context, string, io.Reader, string, string) (string, error)
}

type productionSchedulerArtifactStore struct {
	baseDir           string
	config            config.FileStoreConfig
	files             filestore.Store
	profiles          scheduledArtifactProfileStore
	recoveryPublicKey ed25519.PublicKey
}

func newProductionSchedulerArtifactStore(baseDir string, fileConfig config.FileStoreConfig, files filestore.Store, profiles scheduledArtifactProfileStore) *productionSchedulerArtifactStore {
	return &productionSchedulerArtifactStore{
		baseDir: strings.TrimSpace(baseDir), config: fileConfig, files: files, profiles: profiles,
		recoveryPublicKey: historicalArtifactRecoveryPublicKey(),
	}
}

func (s *productionSchedulerArtifactStore) OpenArtifact(ctx context.Context, access schedulerapp.RunArtifactAccess) (schedulerapp.RunArtifactContent, error) {
	if err := ctx.Err(); err != nil {
		return schedulerapp.RunArtifactContent{}, err
	}
	artifact := access.Artifact
	uri := strings.TrimSpace(artifact.URI)
	if uri == "" {
		return schedulerapp.RunArtifactContent{}, schedulerapp.ErrNotFound
	}
	key, ok := scheduledArtifactKeyFromTypedURI(uri)
	if !ok {
		return schedulerapp.RunArtifactContent{}, schedulerapp.ErrNotFound
	}
	expected := scheduledArtifactStorageKey(
		access.Task.TenantID, access.Run.ScheduledRunID, artifact.ID,
		artifact.SizeBytes, artifact.SHA256, artifact.FileName,
	)
	if key != expected {
		return schedulerapp.RunArtifactContent{}, schedulerapp.ErrNotFound
	}
	return s.openStoredArtifact(ctx, key, artifact, true)
}

func (s *productionSchedulerArtifactStore) ArtifactAvailable(ctx context.Context, access schedulerapp.RunArtifactAccess) bool {
	if err := ctx.Err(); err != nil {
		return false
	}
	artifact := access.Artifact
	if key, ok := scheduledArtifactKeyFromTypedURI(strings.TrimSpace(artifact.URI)); ok {
		expected := scheduledArtifactStorageKey(
			access.Task.TenantID, access.Run.ScheduledRunID, artifact.ID,
			artifact.SizeBytes, artifact.SHA256, artifact.FileName,
		)
		if key != expected {
			return false
		}
		content, err := s.openStoredArtifact(ctx, key, artifact, false)
		if err != nil || content.Reader == nil {
			return false
		}
		return content.Reader.Close() == nil
	}
	provenance, ok := historicalScheduledArtifactProvenanceFor(access)
	if !ok {
		return false
	}
	workspaceRoots, err := s.historicalWorkspaceRoots(ctx, access)
	if err != nil {
		return false
	}
	return s.historicalArtifactAvailable(ctx, access, workspaceRoots, provenance)
}

func (s *productionSchedulerArtifactStore) OpenFinalizedArtifact(ctx context.Context, tenantID, scheduledRunID string, artifact schedulerapp.RunArtifact) (schedulerapp.RunArtifactContent, error) {
	key, ok := scheduledArtifactKeyFromTypedURI(artifact.URI)
	if !ok {
		return schedulerapp.RunArtifactContent{}, schedulerapp.ErrNotFound
	}
	expected := scheduledArtifactStorageKey(tenantID, scheduledRunID, artifact.ID, artifact.SizeBytes, artifact.SHA256, artifact.FileName)
	if key != expected {
		return schedulerapp.RunArtifactContent{}, schedulerapp.ErrNotFound
	}
	return s.openStoredArtifact(ctx, key, artifact, true)
}

func (s *productionSchedulerArtifactStore) ArchiveRunArtifacts(ctx context.Context, run runtimedaemon.AgentRun, scheduledRunID string, result schedulerapp.RunResult) (schedulerapp.RunResult, error) {
	if len(result.Artifacts) == 0 {
		return result, nil
	}
	tenantID := canonicalScheduledArtifactTenant(run.Request.TenantID)
	scheduledRunID = strings.TrimSpace(scheduledRunID)
	if tenantID == "" || scheduledRunID == "" || run.RunID.IsZero() {
		return schedulerapp.RunResult{}, fmt.Errorf("archive scheduled artifacts: run identity is incomplete")
	}
	uploader, ok := s.files.(scheduledArtifactStreamUploader)
	if !ok || s.files == nil {
		return schedulerapp.RunResult{}, fmt.Errorf("archive scheduled artifacts: streaming filestore is not configured")
	}
	workspaceRoots, err := s.runtimeWorkspaceRoots(ctx, run)
	if err != nil {
		return schedulerapp.RunResult{}, err
	}
	seenIDs := make(map[string]bool, len(result.Artifacts))
	archived := make([]schedulerapp.RunArtifact, 0, len(result.Artifacts))
	for index, artifact := range result.Artifacts {
		artifact.ID = uniqueScheduledArtifactID(run.RunID.String(), index, artifact, seenIDs)
		seenIDs[artifact.ID] = true
		source, err := s.openArtifactSource(ctx, run, workspaceRoots, artifact)
		if err != nil {
			return schedulerapp.RunResult{}, fmt.Errorf("archive scheduled artifact %q: %w", artifact.ID, err)
		}
		fileName := cleanArtifactFileName(artifact.FileName, source.FileName, artifact.ID)
		contentType := schedulerArtifactContentType(fileName, firstNonBlank(artifact.ContentType, source.ContentType), nil)
		archivedArtifact, _, archiveErr := s.archiveArtifactReader(ctx, uploader, tenantID, scheduledRunID, artifact, source, fileName, contentType)
		if archiveErr != nil {
			return schedulerapp.RunResult{}, fmt.Errorf("archive scheduled artifact %q: %w", artifact.ID, archiveErr)
		}
		archived = append(archived, archivedArtifact)
	}
	result.Artifacts = archived
	return result, nil
}

func (s *productionSchedulerArtifactStore) archiveArtifactReader(
	ctx context.Context,
	uploader scheduledArtifactStreamUploader,
	tenantID string,
	scheduledRunID string,
	artifact schedulerapp.RunArtifact,
	source schedulerapp.RunArtifactContent,
	fileName string,
	contentType string,
) (schedulerapp.RunArtifact, string, error) {
	defer source.Reader.Close()
	if source.SizeBytes > maxScheduledArtifactBytes {
		return schedulerapp.RunArtifact{}, "", fmt.Errorf("artifact exceeds %d byte limit", maxScheduledArtifactBytes)
	}
	tempDir := s.artifactWorkTempDir()
	if err := os.MkdirAll(tempDir, 0o700); err != nil {
		return schedulerapp.RunArtifact{}, "", fmt.Errorf("prepare scheduled artifact staging: %w", err)
	}
	tempFile, err := os.CreateTemp(tempDir, "scheduled-artifact-upload-*")
	if err != nil {
		return schedulerapp.RunArtifact{}, "", fmt.Errorf("create scheduled artifact staging file: %w", err)
	}
	tempPath := tempFile.Name()
	defer func() {
		tempFile.Close()
		_ = os.Remove(tempPath)
	}()

	digest := sha256.New()
	counter := &schedulerArtifactByteCounter{}
	reader := io.TeeReader(io.LimitReader(source.Reader, maxScheduledArtifactBytes+1), io.MultiWriter(digest, counter))
	if _, err := io.Copy(tempFile, reader); err != nil {
		return schedulerapp.RunArtifact{}, "", err
	}
	if counter.total > maxScheduledArtifactBytes {
		return schedulerapp.RunArtifact{}, "", fmt.Errorf("artifact exceeds %d byte limit", maxScheduledArtifactBytes)
	}
	if source.SizeBytes >= 0 && counter.total != source.SizeBytes {
		return schedulerapp.RunArtifact{}, "", fmt.Errorf("artifact changed while it was archived")
	}
	if err := verifyScheduledArtifactReaderUnchanged(source.Reader); err != nil {
		return schedulerapp.RunArtifact{}, "", fmt.Errorf("artifact changed while it was archived: %w", err)
	}
	if _, err := tempFile.Seek(0, io.SeekStart); err != nil {
		return schedulerapp.RunArtifact{}, "", fmt.Errorf("rewind scheduled artifact staging file: %w", err)
	}
	digestHex := hex.EncodeToString(digest.Sum(nil))
	key := scheduledArtifactStorageKey(tenantID, scheduledRunID, artifact.ID, counter.total, digestHex, fileName)
	artifact.URI = "artifact://" + key
	artifact.FileName = fileName
	artifact.ContentType = contentType
	artifact.SizeBytes = counter.total
	artifact.SHA256 = digestHex
	if _, err := uploader.UploadFromReader(ctx, key, tempFile, contentType, fileName); err != nil {
		// A remote store may commit the object and lose only the response. Treat
		// the content-addressed object as successful when it can be verified.
		existing, openErr := s.openStoredArtifact(context.WithoutCancel(ctx), key, artifact, true)
		if openErr != nil {
			return schedulerapp.RunArtifact{}, "", err
		}
		existing.Reader.Close()
	}
	return artifact, key, nil
}

func (s *productionSchedulerArtifactStore) openArtifactSource(ctx context.Context, run runtimedaemon.AgentRun, workspaceRoots []string, artifact schedulerapp.RunArtifact) (schedulerapp.RunArtifactContent, error) {
	uri := strings.TrimSpace(artifact.URI)
	if key, ok := s.artifactKeyFromURI(uri); ok {
		legacyOwned := strings.HasPrefix(key, "agent-runs/"+run.RunID.String()+"/")
		scopedOwned := strings.TrimSpace(run.Request.TenantID) != "" && strings.HasPrefix(key, missionResultArtifactPrefix(run.Request.TenantID, run.RunID.String()))
		if !legacyOwned && !scopedOwned {
			return schedulerapp.RunArtifactContent{}, fmt.Errorf("filestore artifact is not owned by runtime run")
		}
		return s.openStoredArtifact(ctx, key, artifact, false)
	}
	if !filepath.IsAbs(uri) || strings.Contains(uri, "://") {
		return schedulerapp.RunArtifactContent{}, fmt.Errorf("unsupported artifact URI")
	}
	if !scheduledArtifactPathAuthorized(run, artifact) {
		return schedulerapp.RunArtifactContent{}, fmt.Errorf("workspace artifact is not declared by this run")
	}
	for _, root := range workspaceRoots {
		content, matched, err := openScheduledArtifactInRoot(root, uri)
		if err != nil {
			return schedulerapp.RunArtifactContent{}, err
		}
		if matched {
			if artifact.SizeBytes > 0 && content.SizeBytes != artifact.SizeBytes {
				content.Reader.Close()
				return schedulerapp.RunArtifactContent{}, fmt.Errorf("artifact size changed before archival")
			}
			return content, nil
		}
	}
	return schedulerapp.RunArtifactContent{}, fmt.Errorf("artifact is outside the runtime workspace")
}

func (s *productionSchedulerArtifactStore) openStoredArtifact(ctx context.Context, key string, artifact schedulerapp.RunArtifact, immutable bool) (schedulerapp.RunArtifactContent, error) {
	if s.files == nil || !validScheduledArtifactKey(key) {
		return schedulerapp.RunArtifactContent{}, schedulerapp.ErrNotFound
	}
	var content schedulerapp.RunArtifactContent
	if strings.EqualFold(strings.TrimSpace(s.config.Backend), "s3") {
		reader, err := s.files.GetReader(ctx, key)
		if err != nil {
			return schedulerapp.RunArtifactContent{}, schedulerapp.ErrNotFound
		}
		buffered := bufio.NewReader(reader)
		header, _ := buffered.Peek(512)
		fileName := cleanArtifactFileName(artifact.FileName, path.Base(key), artifact.ID)
		sizeBytes := artifact.SizeBytes
		if sizeBytes == 0 && strings.TrimSpace(artifact.SHA256) == "" {
			sizeBytes = -1
		}
		content = schedulerapp.RunArtifactContent{
			Reader: newSchedulerArtifactBoundedReadCloser(buffered, reader, maxScheduledArtifactBytes), FileName: fileName,
			ContentType: schedulerArtifactContentType(fileName, artifact.ContentType, header), SizeBytes: sizeBytes, SHA256: artifact.SHA256,
		}
	} else {
		root := strings.TrimSpace(s.config.Root)
		if root == "" {
			root = filepath.Join(s.baseDir, "data", "artifacts")
		}
		absolutePath := filepath.Join(root, filepath.FromSlash(key))
		opened, matched, err := openScheduledArtifactInRoot(root, absolutePath)
		if err != nil || !matched {
			return schedulerapp.RunArtifactContent{}, schedulerapp.ErrNotFound
		}
		content = opened
		content.FileName = cleanArtifactFileName(artifact.FileName, content.FileName, artifact.ID)
		content.ContentType = schedulerArtifactContentType(content.FileName, artifact.ContentType, nil)
		content.SHA256 = artifact.SHA256
	}
	if immutable {
		verified, err := s.verifyImmutableArtifact(content, artifact)
		if err != nil {
			return schedulerapp.RunArtifactContent{}, err
		}
		content = verified
	}
	if err := validateOpenedScheduledArtifact(&content, artifact); err != nil {
		content.Reader.Close()
		return schedulerapp.RunArtifactContent{}, err
	}
	return content, nil
}

func (s *productionSchedulerArtifactStore) verifyImmutableArtifact(content schedulerapp.RunArtifactContent, artifact schedulerapp.RunArtifact) (schedulerapp.RunArtifactContent, error) {
	expectedDigest := strings.ToLower(strings.TrimSpace(artifact.SHA256))
	decodedDigest, err := hex.DecodeString(expectedDigest)
	if err != nil || len(decodedDigest) != sha256.Size || content.Reader == nil || artifact.SizeBytes < 0 || artifact.SizeBytes > maxScheduledArtifactBytes {
		if content.Reader != nil {
			content.Reader.Close()
		}
		return schedulerapp.RunArtifactContent{}, schedulerapp.ErrNotFound
	}
	defer content.Reader.Close()

	tempDir := s.artifactWorkTempDir()
	if err := os.MkdirAll(tempDir, 0o700); err != nil {
		return schedulerapp.RunArtifactContent{}, fmt.Errorf("prepare scheduled artifact verification: %w", err)
	}
	tempFile, err := os.CreateTemp(tempDir, "scheduled-artifact-*")
	if err != nil {
		return schedulerapp.RunArtifactContent{}, fmt.Errorf("create scheduled artifact verification file: %w", err)
	}
	tempPath := tempFile.Name()
	keepTemp := false
	defer func() {
		if !keepTemp {
			tempFile.Close()
			_ = os.Remove(tempPath)
		}
	}()

	digest := sha256.New()
	written, err := io.Copy(tempFile, io.TeeReader(io.LimitReader(content.Reader, maxScheduledArtifactBytes+1), digest))
	if err != nil {
		return schedulerapp.RunArtifactContent{}, fmt.Errorf("verify scheduled artifact: %w", err)
	}
	if err := verifyScheduledArtifactReaderUnchanged(content.Reader); err != nil {
		return schedulerapp.RunArtifactContent{}, schedulerapp.ErrNotFound
	}
	if written > maxScheduledArtifactBytes || written != artifact.SizeBytes || !strings.EqualFold(hex.EncodeToString(digest.Sum(nil)), expectedDigest) {
		return schedulerapp.RunArtifactContent{}, schedulerapp.ErrNotFound
	}
	if _, err := tempFile.Seek(0, io.SeekStart); err != nil {
		return schedulerapp.RunArtifactContent{}, fmt.Errorf("rewind scheduled artifact verification file: %w", err)
	}
	keepTemp = true
	content.Reader = &schedulerArtifactVerifiedReadCloser{File: tempFile, path: tempPath}
	content.SizeBytes = written
	content.SHA256 = expectedDigest
	return content, nil
}

func (s *productionSchedulerArtifactStore) artifactWorkTempDir() string {
	if root := strings.TrimSpace(s.config.Root); root != "" {
		return filepath.Join(root, ".tmp", "scheduled-artifact-verification")
	}
	if baseDir := strings.TrimSpace(s.baseDir); baseDir != "" {
		return filepath.Join(baseDir, "data", "tmp", "scheduled-artifact-verification")
	}
	return filepath.Join(os.TempDir(), "orrery-scheduled-artifact-verification")
}

func (s *productionSchedulerArtifactStore) runtimeWorkspaceRoots(ctx context.Context, run runtimedaemon.AgentRun) ([]string, error) {
	roots := []string{
		run.Request.RuntimeOptions.WorkspacePolicy.WorkDir,
	}
	profileID := run.AgentProfileID
	if profileID.IsZero() {
		profileID = agentcore.AgentProfileID(run.AgentID)
	}
	if s.profiles != nil && !profileID.IsZero() && trustedMigratedScheduledRun(run) {
		profile, err := s.profiles.GetProfile(ctx, profileID)
		if err != nil {
			return nil, fmt.Errorf("resolve scheduled artifact workspace: %w", err)
		}
		roots = append(roots, profile.WorkspaceRoot)
		if migratedMarketingScheduledRun(run) {
			roots = append(roots, s.migratedMarketingArtifactRoots()...)
		}
	}
	return normalizedAbsoluteRoots(roots), nil
}

func (s *productionSchedulerArtifactStore) migratedMarketingArtifactRoots() []string {
	if s == nil || strings.TrimSpace(s.baseDir) == "" {
		return nil
	}
	return []string{filepath.Join(s.baseDir, "data", "marketing", "visual-assets")}
}

func trustedMigratedScheduledRun(run runtimedaemon.AgentRun) bool {
	return strings.TrimSpace(run.Request.TenantID) == "system" &&
		strings.HasPrefix(strings.TrimSpace(run.Request.RuntimeOptions.ProviderSpecific["scheduled_task_id"]), "orrery-native:")
}

func migratedMarketingScheduledRun(run runtimedaemon.AgentRun) bool {
	return trustedMigratedScheduledRun(run) &&
		strings.TrimSpace(run.Request.RuntimeOptions.ProviderSpecific["scheduled_task_id"]) == migratedMarketingScheduledTaskID &&
		strings.TrimSpace(run.AgentID.String()) == migratedMarketingScheduledTaskAgentID
}

func scheduledArtifactPathAuthorized(run runtimedaemon.AgentRun, artifact schedulerapp.RunArtifact) bool {
	raw := strings.TrimSpace(run.Request.RuntimeOptions.ProviderSpecific[schedulerResultContractProviderKey])
	if raw == "" {
		return false
	}
	var contract schedulerapp.AgentResultContract
	if err := json.Unmarshal([]byte(raw), &contract); err != nil {
		return false
	}
	for _, requirement := range contract.Artifacts {
		expectedID := "scheduled-artifact:" + run.RunID.String() + ":" + strings.TrimSpace(requirement.ID)
		matched, err := filepath.Match(strings.TrimSpace(requirement.PathPattern), strings.TrimSpace(artifact.URI))
		if err == nil && matched && strings.TrimSpace(artifact.ID) == expectedID {
			return true
		}
	}
	return false
}

func (s *productionSchedulerArtifactStore) artifactKeyFromURI(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	var key string
	switch {
	case strings.HasPrefix(raw, "artifact://"):
		key = strings.TrimPrefix(raw, "artifact://")
	case strings.HasPrefix(raw, "/uploads/"):
		key = strings.TrimPrefix(raw, "/uploads/")
	case strings.HasPrefix(raw, "/artifacts/"):
		key = strings.TrimPrefix(raw, "/artifacts/")
	default:
		parsed, err := url.Parse(raw)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", false
		}
		for _, prefix := range scheduledArtifactURLPrefixes(s.config) {
			if strings.HasPrefix(raw, prefix) {
				key = strings.TrimPrefix(raw, prefix)
				break
			}
		}
	}
	key = strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(key)), "/")
	if !validScheduledArtifactKey(key) {
		return "", false
	}
	return key, true
}

func scheduledArtifactURLPrefixes(cfg config.FileStoreConfig) []string {
	var prefixes []string
	if base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"); base != "" {
		prefixes = append(prefixes, base+"/uploads/", base+"/artifacts/")
	}
	if cdn := strings.Trim(strings.TrimSpace(cfg.S3.CDNDomain), "/"); cdn != "" {
		prefixes = append(prefixes, "https://"+cdn+"/")
	}
	if endpoint, bucket := strings.TrimRight(strings.TrimSpace(cfg.S3.EndpointURL), "/"), strings.Trim(strings.TrimSpace(cfg.S3.Bucket), "/"); endpoint != "" && bucket != "" {
		prefixes = append(prefixes, endpoint+"/"+bucket+"/")
	}
	if bucket, region := strings.TrimSpace(cfg.S3.Bucket), strings.TrimSpace(cfg.S3.Region); bucket != "" && region != "" {
		if strings.Contains(bucket, ".") {
			prefixes = append(prefixes, "https://s3."+region+".amazonaws.com/"+bucket+"/")
		} else {
			prefixes = append(prefixes, "https://"+bucket+".s3."+region+".amazonaws.com/")
		}
	}
	return prefixes
}

func scheduledArtifactKeyFromTypedURI(uri string) (string, bool) {
	if !strings.HasPrefix(strings.TrimSpace(uri), "artifact://scheduled-runs/") {
		return "", false
	}
	key := strings.TrimPrefix(strings.TrimSpace(uri), "artifact://")
	return key, validScheduledArtifactKey(key)
}

func scheduledArtifactStorageKey(tenantID, scheduledRunID, artifactID string, sizeBytes int64, sha256Digest, fileName string) string {
	return path.Join(
		scheduledArtifactRunPrefix(tenantID, scheduledRunID),
		scheduledArtifactHash(strings.TrimSpace(artifactID), 32),
		strconv.FormatInt(sizeBytes, 10)+"-"+strings.ToLower(strings.TrimSpace(sha256Digest)),
		cleanArtifactFileName(fileName, artifactID),
	)
}

func scheduledArtifactMetadataFromTypedURI(uri string) (fileName string, sizeBytes int64, sha256Digest string, ok bool) {
	key, ok := scheduledArtifactKeyFromTypedURI(uri)
	if !ok {
		return "", 0, "", false
	}
	parts := strings.Split(key, "/")
	if len(parts) < 2 {
		return "", 0, "", false
	}
	metadata := parts[len(parts)-2]
	separator := strings.IndexByte(metadata, '-')
	if separator <= 0 {
		return "", 0, "", false
	}
	sizeBytes, err := strconv.ParseInt(metadata[:separator], 10, 64)
	sha256Digest = strings.ToLower(strings.TrimSpace(metadata[separator+1:]))
	decoded, decodeErr := hex.DecodeString(sha256Digest)
	if err != nil || sizeBytes < 0 || sizeBytes > maxScheduledArtifactBytes || decodeErr != nil || len(decoded) != sha256.Size {
		return "", 0, "", false
	}
	return cleanArtifactFileName(parts[len(parts)-1]), sizeBytes, sha256Digest, true
}

func scheduledArtifactRunPrefix(tenantID, scheduledRunID string) string {
	return path.Join(
		"scheduled-runs", "v1",
		scheduledArtifactHash(canonicalScheduledArtifactTenant(tenantID), 16),
		scheduledArtifactHash(strings.TrimSpace(scheduledRunID), 32),
	)
}

func scheduledArtifactHash(value string, length int) string {
	sum := sha256.Sum256([]byte(value))
	encoded := hex.EncodeToString(sum[:])
	if length > 0 && length < len(encoded) {
		return encoded[:length]
	}
	return encoded
}

func canonicalScheduledArtifactTenant(value string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return "system"
}

func uniqueScheduledArtifactID(runtimeRunID string, index int, artifact schedulerapp.RunArtifact, seen map[string]bool) string {
	id := strings.TrimSpace(artifact.ID)
	if id != "" && !seen[id] {
		return id
	}
	seed := runtimeRunID + "\x00" + strconv.Itoa(index) + "\x00" + id + "\x00" + strings.TrimSpace(artifact.URI)
	return "scheduled-output-" + scheduledArtifactHash(seed, 24)
}

func validScheduledArtifactKey(key string) bool {
	key = filepath.ToSlash(strings.TrimSpace(key))
	return key != "" && key != "." && path.Clean(key) == key && !strings.HasPrefix(key, "/") &&
		!strings.HasPrefix(key, "../") && !strings.Contains(key, "\\") && !strings.HasSuffix(key, ".meta.json")
}

func validateOpenedScheduledArtifact(content *schedulerapp.RunArtifactContent, artifact schedulerapp.RunArtifact) error {
	if content == nil || content.Reader == nil || content.SizeBytes < -1 || content.SizeBytes > maxScheduledArtifactBytes {
		return schedulerapp.ErrNotFound
	}
	if (artifact.SizeBytes > 0 || strings.TrimSpace(artifact.SHA256) != "") && content.SizeBytes != artifact.SizeBytes {
		return schedulerapp.ErrNotFound
	}
	content.FileName = cleanArtifactFileName(artifact.FileName, content.FileName, artifact.ID)
	content.ContentType = schedulerArtifactContentType(content.FileName, firstNonBlank(artifact.ContentType, content.ContentType), nil)
	content.SHA256 = strings.TrimSpace(artifact.SHA256)
	return nil
}

func normalizedAbsoluteRoots(values []string) []string {
	seen := map[string]bool{}
	var roots []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if !filepath.IsAbs(value) {
			continue
		}
		value = filepath.Clean(value)
		key := strings.ToLower(value)
		if seen[key] {
			continue
		}
		seen[key] = true
		roots = append(roots, value)
	}
	return roots
}

func schedulerArtifactContentType(fileName, declared string, header []byte) string {
	if declared = strings.TrimSpace(declared); declared != "" {
		return declared
	}
	switch strings.ToLower(filepath.Ext(fileName)) {
	case ".md", ".markdown":
		return "text/markdown; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".log", ".txt", ".csv":
		return "text/plain; charset=utf-8"
	}
	if detected := mime.TypeByExtension(strings.ToLower(filepath.Ext(fileName))); detected != "" {
		return detected
	}
	if len(header) > 0 {
		return http.DetectContentType(header)
	}
	return "application/octet-stream"
}

func cleanArtifactFileName(candidates ...string) string {
	for _, candidate := range candidates {
		candidate = path.Base(strings.ReplaceAll(strings.TrimSpace(candidate), "\\", "/"))
		candidate = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, candidate)
		if candidate != "" && candidate != "." && candidate != "/" {
			return candidate
		}
	}
	return "artifact"
}

var _ schedulerapp.ArtifactStore = (*productionSchedulerArtifactStore)(nil)
var _ schedulerapp.ArtifactAvailabilityChecker = (*productionSchedulerArtifactStore)(nil)
