package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/filestore"
)

func TestProductionSchedulerArtifactStoreArchivesImmutableRunArtifact(t *testing.T) {
	baseDir := t.TempDir()
	workspace := filepath.Join(baseDir, "workspaces", "A000006")
	sourcePath := filepath.Join(workspace, "reports", "weekly.md")
	writeSchedulerArtifactTestFile(t, sourcePath, "# original weekly report")
	fileRoot := filepath.Join(baseDir, "filestore")
	files, err := filestore.OpenLocalStorage(filestore.LocalConfig{Root: fileRoot})
	if err != nil {
		t.Fatal(err)
	}
	store := newProductionSchedulerArtifactStore(baseDir, config.FileStoreConfig{Backend: "local", Root: fileRoot}, files, nil)
	contract, err := json.Marshal(schedulerapp.AgentResultContract{Artifacts: []schedulerapp.ArtifactRequirement{{
		ID: "report/weekly", Kind: "report", PathPattern: sourcePath,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	runtimeRun := runtimedaemon.AgentRun{
		RunID: agentcore.AgentRunID("runtime-run-1"),
		Request: agentcore.RuntimeRequest{
			TenantID: "tenant-a",
			RuntimeOptions: agentcore.RuntimeOptions{
				WorkspacePolicy:  agentcore.RuntimeWorkspacePolicy{WorkDir: workspace},
				ProviderSpecific: map[string]string{schedulerResultContractProviderKey: string(contract)},
			},
		},
	}
	result, err := store.ArchiveRunArtifacts(context.Background(), runtimeRun, "scheduled-run-1", schedulerapp.RunResult{
		Artifacts: []schedulerapp.RunArtifact{{ID: "scheduled-artifact:runtime-run-1:report/weekly", Kind: "report", URI: sourcePath, SizeBytes: int64(len("# original weekly report"))}},
	})
	if err != nil {
		t.Fatal(err)
	}
	artifact := result.Artifacts[0]
	if !strings.HasPrefix(artifact.URI, "artifact://scheduled-runs/v1/") || artifact.FileName != "weekly.md" || artifact.SHA256 == "" {
		t.Fatalf("archived artifact = %+v", artifact)
	}
	writeSchedulerArtifactTestFile(t, sourcePath, "# overwritten later")
	access := schedulerapp.RunArtifactAccess{
		Task:     schedulerapp.ScheduledTask{TenantID: "tenant-a"},
		Run:      schedulerapp.ScheduledRun{ScheduledRunID: "scheduled-run-1"},
		Artifact: artifact,
	}
	content, err := store.OpenArtifact(context.Background(), access)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(content.Reader)
	content.Reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "# original weekly report" || content.FileName != "weekly.md" || content.SizeBytes != artifact.SizeBytes {
		t.Fatalf("content=%+v body=%q", content, body)
	}

	for _, mutate := range []func(*schedulerapp.RunArtifactAccess){
		func(candidate *schedulerapp.RunArtifactAccess) { candidate.Task.TenantID = "tenant-b" },
		func(candidate *schedulerapp.RunArtifactAccess) { candidate.Run.ScheduledRunID = "scheduled-run-2" },
		func(candidate *schedulerapp.RunArtifactAccess) { candidate.Artifact.ID = "another-artifact" },
	} {
		candidate := access
		mutate(&candidate)
		if _, err := store.OpenArtifact(context.Background(), candidate); !errors.Is(err, schedulerapp.ErrNotFound) {
			t.Fatalf("cross-scope access error = %v, want ErrNotFound", err)
		}
	}

	key, ok := scheduledArtifactKeyFromTypedURI(artifact.URI)
	if !ok {
		t.Fatalf("archived URI = %q", artifact.URI)
	}
	writeSchedulerArtifactTestFile(t, filepath.Join(fileRoot, filepath.FromSlash(key)), strings.Repeat("x", int(artifact.SizeBytes)))
	if !store.ArtifactAvailable(context.Background(), access) {
		t.Fatal("same-size stored artifact should remain metadata-available")
	}
	if _, err := store.OpenArtifact(context.Background(), access); !errors.Is(err, schedulerapp.ErrNotFound) {
		t.Fatalf("corrupted stored artifact error = %v, want ErrNotFound", err)
	}
}

func TestProductionSchedulerArtifactStoreRejectsUnownedArchiveSources(t *testing.T) {
	baseDir := t.TempDir()
	workspace := filepath.Join(baseDir, "workspaces", "A")
	outside := filepath.Join(baseDir, "workspaces", "B", "private.md")
	writeSchedulerArtifactTestFile(t, outside, "private")
	fileRoot := filepath.Join(baseDir, "filestore")
	files, err := filestore.OpenLocalStorage(filestore.LocalConfig{Root: fileRoot})
	if err != nil {
		t.Fatal(err)
	}
	otherURI, err := files.Upload(context.Background(), "agent-runs/runtime-run-b/private.md", []byte("private"), "text/markdown", "private.md")
	if err != nil {
		t.Fatal(err)
	}
	store := newProductionSchedulerArtifactStore(baseDir, config.FileStoreConfig{Backend: "local", Root: fileRoot}, files, nil)
	run := runtimedaemon.AgentRun{
		RunID:   agentcore.AgentRunID("runtime-run-a"),
		Request: agentcore.RuntimeRequest{TenantID: "tenant-a", RuntimeOptions: agentcore.RuntimeOptions{WorkspacePolicy: agentcore.RuntimeWorkspacePolicy{Root: workspace}}},
	}
	for _, uri := range []string{outside, otherURI, "https://example.test/private.md"} {
		_, err := store.ArchiveRunArtifacts(context.Background(), run, "scheduled-run-a", schedulerapp.RunResult{
			Artifacts: []schedulerapp.RunArtifact{{ID: "artifact", URI: uri}},
		})
		if err == nil {
			t.Fatalf("archive source %q unexpectedly succeeded", uri)
		}
	}
}

func TestProductionSchedulerArtifactStoreRecognizesConfiguredOriginsOnly(t *testing.T) {
	store := newProductionSchedulerArtifactStore("", config.FileStoreConfig{
		Backend: "s3",
		S3:      config.S3Config{Bucket: "artifacts", Region: "us-east-1", CDNDomain: "cdn.example.test"},
	}, nil, nil)
	for _, raw := range []string{
		"https://cdn.example.test/agent-runs/run-1/report.md",
		"https://artifacts.s3.us-east-1.amazonaws.com/agent-runs/run-1/report.md",
	} {
		if key, ok := store.artifactKeyFromURI(raw); !ok || key != "agent-runs/run-1/report.md" {
			t.Fatalf("URI %q parsed as %q, %v", raw, key, ok)
		}
	}
	if _, ok := store.artifactKeyFromURI("https://example.test/agent-runs/run-1/report.md"); ok {
		t.Fatal("unconfigured origin was accepted")
	}
}

func TestProductionSchedulerArtifactStoreArchivesUnknownSizeS3Artifact(t *testing.T) {
	files := &memoryScheduledArtifactStore{objects: map[string][]byte{
		"agent-runs/runtime-run-s3/agent-result.md": []byte("# stored result"),
	}}
	store := newProductionSchedulerArtifactStore("", config.FileStoreConfig{Backend: "s3"}, files, nil)
	run := runtimedaemon.AgentRun{
		RunID:   agentcore.AgentRunID("runtime-run-s3"),
		Request: agentcore.RuntimeRequest{TenantID: "tenant-a"},
	}
	result, err := store.ArchiveRunArtifacts(context.Background(), run, "scheduled-run-s3", schedulerapp.RunResult{
		Artifacts: []schedulerapp.RunArtifact{{
			ID: "agent-runs/runtime-run-s3/agent-result.md", Kind: "report",
			URI: "artifact://agent-runs/runtime-run-s3/agent-result.md",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	artifact := result.Artifacts[0]
	if artifact.SizeBytes != int64(len("# stored result")) || artifact.SHA256 == "" {
		t.Fatalf("archived artifact = %+v", artifact)
	}
	if !files.lastUploadSeekable {
		t.Fatal("scheduled S3 upload body was not seekable")
	}
	content, err := store.OpenArtifact(context.Background(), schedulerapp.RunArtifactAccess{
		Task:     schedulerapp.ScheduledTask{TenantID: "tenant-a"},
		Run:      schedulerapp.ScheduledRun{ScheduledRunID: "scheduled-run-s3"},
		Artifact: artifact,
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(content.Reader)
	content.Reader.Close()
	if err != nil || string(body) != "# stored result" {
		t.Fatalf("body=%q error=%v", body, err)
	}
}

func TestProductionSchedulerArtifactStoreRejectsSameSizeTamperedLocalSnapshot(t *testing.T) {
	baseDir := t.TempDir()
	fileRoot := filepath.Join(baseDir, "filestore")
	files, err := filestore.OpenLocalStorage(filestore.LocalConfig{Root: fileRoot})
	if err != nil {
		t.Fatal(err)
	}
	store := newProductionSchedulerArtifactStore(baseDir, config.FileStoreConfig{Backend: "local", Root: fileRoot}, files, nil)
	artifact := archiveFilestoreArtifactForTest(t, store, files, "runtime-tamper-local", "scheduled-tamper-local", "tenant-a", "original")
	key, ok := scheduledArtifactKeyFromTypedURI(artifact.URI)
	if !ok {
		t.Fatalf("archived URI = %q", artifact.URI)
	}
	writeSchedulerArtifactTestFile(t, filepath.Join(fileRoot, filepath.FromSlash(key)), "tampered")

	_, err = store.OpenArtifact(context.Background(), schedulerapp.RunArtifactAccess{
		Task: schedulerapp.ScheduledTask{TenantID: "tenant-a"},
		Run:  schedulerapp.ScheduledRun{ScheduledRunID: "scheduled-tamper-local"}, Artifact: artifact,
	})
	if !errors.Is(err, schedulerapp.ErrNotFound) {
		t.Fatalf("tampered local snapshot error = %v, want ErrNotFound", err)
	}
}

func TestProductionSchedulerArtifactStoreRejectsSameSizeTamperedS3Snapshot(t *testing.T) {
	baseDir := t.TempDir()
	files := &memoryScheduledArtifactStore{objects: map[string][]byte{}}
	store := newProductionSchedulerArtifactStore(baseDir, config.FileStoreConfig{Backend: "s3"}, files, nil)
	artifact := archiveFilestoreArtifactForTest(t, store, files, "runtime-tamper-s3", "scheduled-tamper-s3", "tenant-a", "original")
	key, ok := scheduledArtifactKeyFromTypedURI(artifact.URI)
	if !ok {
		t.Fatalf("archived URI = %q", artifact.URI)
	}
	files.objects[key] = []byte("tampered")

	_, err := store.OpenArtifact(context.Background(), schedulerapp.RunArtifactAccess{
		Task: schedulerapp.ScheduledTask{TenantID: "tenant-a"},
		Run:  schedulerapp.ScheduledRun{ScheduledRunID: "scheduled-tamper-s3"}, Artifact: artifact,
	})
	if !errors.Is(err, schedulerapp.ErrNotFound) {
		t.Fatalf("tampered S3 snapshot error = %v, want ErrNotFound", err)
	}
}

func TestProductionSchedulerArtifactStoreRejectsSpoofedMigratedWorkspaceAccess(t *testing.T) {
	baseDir := t.TempDir()
	workDir := filepath.Join(baseDir, "runs", "runtime-spoof")
	profileRoot := filepath.Join(baseDir, "profiles", "A000005")
	artifactPath := filepath.Join(profileRoot, "private.md")
	writeSchedulerArtifactTestFile(t, artifactPath, "private")
	fileRoot := filepath.Join(baseDir, "filestore")
	files, err := filestore.OpenLocalStorage(filestore.LocalConfig{Root: fileRoot})
	if err != nil {
		t.Fatal(err)
	}
	profiles := fixedScheduledArtifactProfiles{
		"A000005": {ID: "A000005", WorkspaceRoot: profileRoot},
	}
	store := newProductionSchedulerArtifactStore(baseDir, config.FileStoreConfig{Backend: "local", Root: fileRoot}, files, profiles)
	contract, err := json.Marshal(schedulerapp.AgentResultContract{Artifacts: []schedulerapp.ArtifactRequirement{{
		ID: "private", Kind: "report", PathPattern: artifactPath,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	run := runtimedaemon.AgentRun{
		RunID: "runtime-spoof", AgentID: "A000005", AgentProfileID: "A000005",
		Request: agentcore.RuntimeRequest{TenantID: "tenant-a", RuntimeOptions: agentcore.RuntimeOptions{
			WorkspacePolicy: agentcore.RuntimeWorkspacePolicy{WorkDir: workDir},
			ProviderSpecific: map[string]string{
				"scheduled_task_id":                migratedMarketingScheduledTaskID,
				schedulerResultContractProviderKey: string(contract),
			},
		}},
	}
	_, err = store.ArchiveRunArtifacts(context.Background(), run, "scheduled-spoof", schedulerapp.RunResult{Artifacts: []schedulerapp.RunArtifact{{
		ID: "scheduled-artifact:runtime-spoof:private", Kind: "report", URI: artifactPath, SizeBytes: int64(len("private")),
	}}})
	if err == nil || !strings.Contains(err.Error(), "outside the runtime workspace") {
		t.Fatalf("spoofed migrated archive error = %v", err)
	}
}

func archiveFilestoreArtifactForTest(
	t *testing.T,
	store *productionSchedulerArtifactStore,
	files filestore.Store,
	runtimeRunID string,
	scheduledRunID string,
	tenantID string,
	body string,
) schedulerapp.RunArtifact {
	t.Helper()
	key := "agent-runs/" + runtimeRunID + "/result.md"
	uri, err := files.Upload(context.Background(), key, []byte(body), "text/markdown", "result.md")
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.ArchiveRunArtifacts(context.Background(), runtimedaemon.AgentRun{
		RunID: agentcore.AgentRunID(runtimeRunID), Request: agentcore.RuntimeRequest{TenantID: tenantID},
	}, scheduledRunID, schedulerapp.RunResult{Artifacts: []schedulerapp.RunArtifact{{ID: key, Kind: "report", URI: uri}}})
	if err != nil {
		t.Fatal(err)
	}
	return result.Artifacts[0]
}

type fixedScheduledArtifactProfiles map[agentcore.AgentProfileID]agentcore.AgentProfile

func (p fixedScheduledArtifactProfiles) GetProfile(_ context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	profile, ok := p[id]
	if !ok {
		return agentcore.AgentProfile{}, agentcore.ErrNotFound
	}
	return profile, nil
}

func writeSchedulerArtifactTestFile(t *testing.T, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

type memoryScheduledArtifactStore struct {
	objects            map[string][]byte
	lastUploadSeekable bool
}

func (s *memoryScheduledArtifactStore) Put(ctx context.Context, name, contentType string, reader io.Reader) (filestore.ArtifactRef, error) {
	key := "put/" + name
	uri, err := s.UploadFromReader(ctx, key, reader, contentType, name)
	return filestore.ArtifactRef{ID: key, Name: name, ContentType: contentType, URL: uri}, err
}

func (s *memoryScheduledArtifactStore) Get(ctx context.Context, key string) (filestore.Artifact, error) {
	reader, err := s.GetReader(ctx, key)
	if err != nil {
		return filestore.Artifact{}, err
	}
	defer reader.Close()
	body, err := io.ReadAll(reader)
	return filestore.Artifact{Ref: filestore.ArtifactRef{ID: key, Size: int64(len(body))}, Content: body}, err
}

func (s *memoryScheduledArtifactStore) GetReader(_ context.Context, key string) (io.ReadCloser, error) {
	body, ok := s.objects[key]
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(strings.NewReader(string(body))), nil
}

func (s *memoryScheduledArtifactStore) Upload(ctx context.Context, key string, data []byte, _ string, _ string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s.objects[key] = append([]byte(nil), data...)
	return "artifact://" + key, nil
}

func (s *memoryScheduledArtifactStore) UploadFromReader(ctx context.Context, key string, reader io.Reader, contentType, fileName string) (string, error) {
	_, s.lastUploadSeekable = reader.(io.Seeker)
	body, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	return s.Upload(ctx, key, body, contentType, fileName)
}

func (s *memoryScheduledArtifactStore) Delete(_ context.Context, key string) error {
	delete(s.objects, key)
	return nil
}

func (s *memoryScheduledArtifactStore) DeleteKeys(ctx context.Context, keys []string) error {
	for _, key := range keys {
		_ = s.Delete(ctx, key)
	}
	return nil
}

func (s *memoryScheduledArtifactStore) DeletePrefix(_ context.Context, prefix string) error {
	for key := range s.objects {
		if strings.HasPrefix(key, prefix) {
			delete(s.objects, key)
		}
	}
	return nil
}

func (s *memoryScheduledArtifactStore) URL(_ context.Context, key string) (string, error) {
	return "artifact://" + key, nil
}

func (s *memoryScheduledArtifactStore) KeyFromURL(raw string) string {
	return strings.TrimPrefix(raw, "artifact://")
}

func (*memoryScheduledArtifactStore) CdnDomain() string { return "" }
