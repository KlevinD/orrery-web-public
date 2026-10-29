package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/filestore"
)

func TestProductionSchedulerArtifactStoreRejectsUnsnapshottedHistoricalArtifacts(t *testing.T) {
	baseDir := t.TempDir()
	workspace := filepath.Join(baseDir, "workspaces", "A000006")
	artifactPath := filepath.Join(workspace, "report", "weekly.md")
	writeSchedulerArtifactTestFile(t, artifactPath, "# weekly")
	fileRoot := filepath.Join(baseDir, "filestore")
	files, err := filestore.OpenLocalStorage(filestore.LocalConfig{Root: fileRoot})
	if err != nil {
		t.Fatal(err)
	}
	legacyKey := "agent-runs/responsibility-run-1/result.json"
	if _, err := files.Upload(context.Background(), legacyKey, []byte(`{"ok":true}`), "application/json", "result.json"); err != nil {
		t.Fatal(err)
	}
	legacyURI, err := files.URL(context.Background(), legacyKey)
	if err != nil {
		t.Fatal(err)
	}
	profiles := fixedScheduledArtifactProfiles{
		"A000006": {ID: "A000006", WorkspaceRoot: workspace},
	}
	store := newProductionSchedulerArtifactStore(baseDir, config.FileStoreConfig{Backend: "local", Root: fileRoot}, files, profiles)
	historicalAccess := schedulerapp.RunArtifactAccess{
		Task: schedulerapp.ScheduledTask{TenantID: "tenant-a", Target: schedulerapp.Target{AgentID: "A000006"}},
		Run:  schedulerapp.ScheduledRun{ScheduledRunID: "run-old", AgentID: "A000006", CompletedAt: time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)},
	}

	tests := []struct {
		name     string
		artifact schedulerapp.RunArtifact
	}{
		{name: "raw absolute workspace path", artifact: schedulerapp.RunArtifact{ID: "artifact-1", URI: artifactPath, SizeBytes: int64(len("# weekly"))}},
		{name: "legacy filestore URI", artifact: schedulerapp.RunArtifact{ID: legacyKey, URI: legacyURI}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			access := historicalAccess
			access.Artifact = test.artifact
			if _, err := store.OpenArtifact(context.Background(), access); !errors.Is(err, schedulerapp.ErrNotFound) {
				t.Fatalf("OpenArtifact() error = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestProductionSchedulerArtifactStoreArchivesAuthorizedHistoricalWorkspaceArtifact(t *testing.T) {
	baseDir := t.TempDir()
	workspace := filepath.Join(baseDir, "workspaces", "A000006")
	artifactPath := filepath.Join(workspace, "reports", "weekly.md")
	body := "# historical weekly report"
	writeSchedulerArtifactTestFile(t, artifactPath, body)
	fileRoot := filepath.Join(baseDir, "filestore")
	files, err := filestore.OpenLocalStorage(filestore.LocalConfig{Root: fileRoot})
	if err != nil {
		t.Fatal(err)
	}
	store := newProductionSchedulerArtifactStore(
		baseDir, config.FileStoreConfig{Backend: "local", Root: fileRoot}, files,
		fixedScheduledArtifactProfiles{"A000006": {ID: "A000006", WorkspaceRoot: workspace}},
	)
	access := historicalScheduledArtifactTestAccess(artifactPath, int64(len(body)))

	archived, err := store.ArchiveHistoricalArtifact(context.Background(), access)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(archived.URI, "artifact://scheduled-runs/v1/") || archived.ID != access.Artifact.ID ||
		archived.EvidenceID != access.Artifact.EvidenceID || archived.SizeBytes != int64(len(body)) || archived.SHA256 == "" {
		t.Fatalf("archived artifact = %+v", archived)
	}
	legacyIdentityAccess := historicalScheduledArtifactTestAccess(artifactPath, int64(len(body)))
	legacyIdentityAccess.Run.ScheduledRunID = "scheduled-historical-derived-identity"
	legacyIdentityAccess.Run.RuntimeRunID = ""
	legacyIdentityAccess.Run.ResultContract = schedulerapp.AgentResultContract{}
	derivedIdentityArtifact, err := store.ArchiveHistoricalArtifact(context.Background(), legacyIdentityAccess)
	if err != nil || !strings.HasPrefix(derivedIdentityArtifact.URI, "artifact://scheduled-runs/v1/") {
		t.Fatalf("archive with derived legacy identity artifact=%+v error=%v", derivedIdentityArtifact, err)
	}
	writeSchedulerArtifactTestFile(t, artifactPath, "# source changed after migration")
	access.Artifact = archived
	content, err := store.OpenArtifact(context.Background(), access)
	if err != nil {
		t.Fatal(err)
	}
	archivedBody, readErr := io.ReadAll(content.Reader)
	content.Reader.Close()
	if readErr != nil || string(archivedBody) != body {
		t.Fatalf("archived body=%q error=%v", archivedBody, readErr)
	}
}

func TestProductionSchedulerArtifactStoreRecoversOverwrittenHistoricalArtifactFromOwnedTrace(t *testing.T) {
	baseDir := t.TempDir()
	workspace := filepath.Join(baseDir, "workspaces", "A000006")
	artifactPath := filepath.Join(workspace, "reports", "daily.md")
	historicalBody := "historical policy report"
	writeSchedulerArtifactTestFile(t, artifactPath, strings.Repeat("x", len(historicalBody)))
	fileRoot := filepath.Join(baseDir, "filestore")
	files, err := filestore.OpenLocalStorage(filestore.LocalConfig{Root: fileRoot})
	if err != nil {
		t.Fatal(err)
	}
	store := newProductionSchedulerArtifactStore(
		baseDir, config.FileStoreConfig{Backend: "local", Root: fileRoot}, files,
		fixedScheduledArtifactProfiles{"A000006": {ID: "A000006", WorkspaceRoot: workspace}},
	)
	trustedPublicKey, trustedPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	store.recoveryPublicKey = trustedPublicKey
	_, untrustedPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	access := historicalScheduledArtifactTestAccess(artifactPath, int64(len(historicalBody)))
	access.Run.RuntimeRunID = ""
	access.Run.ResultContract = schedulerapp.AgentResultContract{}
	if err := os.Chtimes(artifactPath, access.Run.CompletedAt.Add(time.Hour), access.Run.CompletedAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(baseDir, "workspaces", "marlon-brando", "reports", "daily.md")
	writeHistoricalScheduledArtifactTrace(t, files, "runtime-historical-1", map[string]any{
		"run_id": "runtime-historical-1", "agent_id": "marlon-brando", "recorded_at": access.Run.CompletedAt,
		"tool_calls": []any{map[string]any{
			"Name": "Shell", "Input": map[string]any{"command": "cat \"" + legacyPath + "\"", "max_output_len": 65536},
			"Output": historicalScheduledArtifactShellOutputJSON(t, historicalBody, false),
		}},
	})

	if _, err := store.ArchiveHistoricalArtifact(context.Background(), access); !errors.Is(err, schedulerapp.ErrNotFound) {
		t.Fatalf("unapproved trace recovery error = %v, want ErrNotFound", err)
	}
	writeHistoricalScheduledArtifactRecoveryManifest(t, baseDir, access, "runtime-historical-1", historicalBody, untrustedPrivateKey)
	if store.ArtifactAvailable(context.Background(), access) {
		t.Fatal("untrusted trace recovery should not be available")
	}
	if _, err := store.ArchiveHistoricalArtifact(context.Background(), access); !errors.Is(err, schedulerapp.ErrNotFound) {
		t.Fatalf("untrusted recovery signature error = %v, want ErrNotFound", err)
	}
	writeHistoricalScheduledArtifactRecoveryManifest(t, baseDir, access, "runtime-historical-1", strings.Repeat("z", len(historicalBody)), trustedPrivateKey)
	if _, err := store.ArchiveHistoricalArtifact(context.Background(), access); !errors.Is(err, schedulerapp.ErrNotFound) {
		t.Fatalf("wrong recovery digest error = %v, want ErrNotFound", err)
	}
	writeHistoricalScheduledArtifactRecoveryManifest(t, baseDir, access, "runtime-historical-1", historicalBody, trustedPrivateKey)
	if !store.ArtifactAvailable(context.Background(), access) {
		t.Fatal("signed trace recovery should be metadata-available")
	}
	archived, err := store.ArchiveHistoricalArtifact(context.Background(), access)
	if err != nil {
		t.Fatal(err)
	}
	access.Artifact = archived
	content, err := store.OpenArtifact(context.Background(), access)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(content.Reader)
	content.Reader.Close()
	if readErr != nil || string(body) != historicalBody {
		t.Fatalf("recovered body=%q error=%v", body, readErr)
	}
	digest := sha256.Sum256([]byte(historicalBody))
	if archived.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("archived digest = %q", archived.SHA256)
	}
}

func writeHistoricalScheduledArtifactRecoveryManifest(
	t *testing.T,
	baseDir string,
	access schedulerapp.RunArtifactAccess,
	runtimeRunID string,
	body string,
	privateKey ed25519.PrivateKey,
) {
	t.Helper()
	payload := historicalScheduledArtifactRecoveryPayload{
		Version: 1,
		Recoveries: []historicalScheduledArtifactRecoveryRule{{
			ScheduledTaskID: access.Task.ScheduledTaskID,
			ScheduledRunID:  access.Run.ScheduledRunID,
			RuntimeRunID:    runtimeRunID,
			ArtifactID:      access.Artifact.ID,
			EvidenceID:      access.Artifact.EvidenceID,
			SourceURI:       access.Artifact.URI,
			SizeBytes:       access.Artifact.SizeBytes,
			SHA256:          scheduledArtifactDigest([]byte(body)),
		}},
	}
	signed, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	manifest := historicalScheduledArtifactRecoveryManifest{
		Version: payload.Version, Recoveries: payload.Recoveries,
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, signed)),
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(baseDir, "config", historicalArtifactRecoveryRulesFile)
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestHistoricalArtifactTraceRecoveryRequiresExactReadOnlyProvenance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.md")
	if !historicalArtifactTraceCommandMatches("cat \""+path+"\"", []string{path}) {
		t.Fatal("exact cat command was not accepted")
	}
	for _, command := range []string{
		"cat " + path,
		"cat \"" + path + "\"; cat /private",
		"head -c 10 \"" + path + "\"",
		"cat \"" + filepath.Join(filepath.Dir(path), "other.md") + "\"",
	} {
		if historicalArtifactTraceCommandMatches(command, []string{path}) {
			t.Fatalf("untrusted trace command %q was accepted", command)
		}
	}
	for _, candidate := range []string{
		"/workspace/$(cat /private).md",
		"/workspace/`cat /private`.md",
		"/workspace/report\nother.md",
		"/workspace/report\".md",
	} {
		if !unsafeHistoricalArtifactTracePath(candidate) {
			t.Fatalf("unsafe trace path %q was accepted", candidate)
		}
	}
	if unsafeHistoricalArtifactTracePath("/workspace/legal-vault/Legal&Policy/report.md") {
		t.Fatal("safe quoted trace path was rejected")
	}
	if historicalArtifactTraceTextType(schedulerapp.RunArtifact{URI: "/workspace/image.png", Kind: "visual"}) {
		t.Fatal("binary trace artifact type was accepted")
	}
}

func writeHistoricalScheduledArtifactTrace(t *testing.T, files filestore.Store, runtimeRunID string, trace map[string]any) {
	t.Helper()
	body, err := json.Marshal(trace)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := files.Upload(context.Background(), "agent-runs/"+runtimeRunID+"/runtime-trace.json", body, "application/json", "runtime-trace.json"); err != nil {
		t.Fatal(err)
	}
}

func historicalScheduledArtifactShellOutputJSON(t *testing.T, stdout string, truncated bool) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"exit_code": 0, "stderr": "", "stdout": stdout, "truncated": truncated,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestProductionSchedulerArtifactStoreRejectsUnauthorizedHistoricalMigration(t *testing.T) {
	baseDir := t.TempDir()
	workspace := filepath.Join(baseDir, "workspaces", "A000006")
	artifactPath := filepath.Join(workspace, "reports", "weekly.md")
	writeSchedulerArtifactTestFile(t, artifactPath, "# weekly")
	outsidePath := filepath.Join(baseDir, "private", "outside.md")
	writeSchedulerArtifactTestFile(t, outsidePath, "private")
	fileRoot := filepath.Join(baseDir, "filestore")
	files, err := filestore.OpenLocalStorage(filestore.LocalConfig{Root: fileRoot})
	if err != nil {
		t.Fatal(err)
	}
	profiles := fixedScheduledArtifactProfiles{"A000006": {ID: "A000006", WorkspaceRoot: workspace}}
	store := newProductionSchedulerArtifactStore(baseDir, config.FileStoreConfig{Backend: "local", Root: fileRoot}, files, profiles)

	tests := []struct {
		name   string
		mutate func(*schedulerapp.RunArtifactAccess)
	}{
		{name: "non-migrated task", mutate: func(access *schedulerapp.RunArtifactAccess) {
			access.Task.ScheduledTaskID = "scheduled-user-task"
			access.Run.ScheduledTaskID = "scheduled-user-task"
		}},
		{name: "cross tenant", mutate: func(access *schedulerapp.RunArtifactAccess) { access.Run.TenantID = "tenant-b" }},
		{name: "task agent mismatch", mutate: func(access *schedulerapp.RunArtifactAccess) { access.Task.Target.AgentID = "A000007" }},
		{name: "artifact identity mismatch", mutate: func(access *schedulerapp.RunArtifactAccess) {
			access.Artifact.ID = "scheduled-artifact:runtime-other:weekly"
		}},
		{name: "evidence identity mismatch", mutate: func(access *schedulerapp.RunArtifactAccess) {
			access.Artifact.EvidenceID = "scheduled-evidence:runtime-other:weekly"
		}},
		{name: "contract path mismatch", mutate: func(access *schedulerapp.RunArtifactAccess) {
			access.Task.Target.ResultContract.Artifacts[0].PathPattern = filepath.Join(workspace, "reports", "other.md")
		}},
		{name: "outside canonical workspace", mutate: func(access *schedulerapp.RunArtifactAccess) {
			access.Artifact.URI = outsidePath
			access.Task.Target.ResultContract.Artifacts[0].PathPattern = outsidePath
		}},
		{name: "non-completed run", mutate: func(access *schedulerapp.RunArtifactAccess) { access.Run.Status = schedulerapp.RunStatusRunning }},
		{name: "missing completion time", mutate: func(access *schedulerapp.RunArtifactAccess) { access.Run.CompletedAt = time.Time{} }},
		{name: "source modified after run", mutate: func(access *schedulerapp.RunArtifactAccess) {
			access.Run.CompletedAt = time.Now().UTC().Add(-24 * time.Hour)
		}},
		{name: "snapshot contract mismatch", mutate: func(access *schedulerapp.RunArtifactAccess) {
			access.Run.ConfigurationSnapshotVersion = 1
			snapshot := access.Task
			snapshot.Target.ResultContract.Artifacts = append([]schedulerapp.ArtifactRequirement(nil), access.Run.ResultContract.Artifacts...)
			snapshot.Target.ResultContract.Artifacts[0].ID = "other"
			access.Run.ConfigurationSnapshot = &snapshot
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			access := historicalScheduledArtifactTestAccess(artifactPath, int64(len("# weekly")))
			test.mutate(&access)
			if _, err := store.ArchiveHistoricalArtifact(context.Background(), access); !errors.Is(err, schedulerapp.ErrNotFound) {
				t.Fatalf("ArchiveHistoricalArtifact() error = %v, want ErrNotFound", err)
			}
		})
	}

	changedProfiles := fixedScheduledArtifactProfiles{"A000006": {ID: "A000006", WorkspaceRoot: filepath.Join(baseDir, "other-root")}}
	changedRootStore := newProductionSchedulerArtifactStore(baseDir, config.FileStoreConfig{Backend: "local", Root: fileRoot}, files, changedProfiles)
	if _, err := changedRootStore.ArchiveHistoricalArtifact(context.Background(), historicalScheduledArtifactTestAccess(artifactPath, int64(len("# weekly")))); !errors.Is(err, schedulerapp.ErrNotFound) {
		t.Fatalf("changed profile root error = %v, want ErrNotFound", err)
	}
}

func historicalScheduledArtifactTestAccess(artifactPath string, sizeBytes int64) schedulerapp.RunArtifactAccess {
	taskID := "orrery-native:historical-test"
	runtimeRunID := "runtime-historical-1"
	requirement := schedulerapp.ArtifactRequirement{ID: "weekly", Kind: "report", PathPattern: artifactPath, MinBytes: 1}
	return schedulerapp.RunArtifactAccess{
		Task: schedulerapp.ScheduledTask{
			TenantID: "system", ScheduledTaskID: taskID,
			Target: schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask, AgentID: "A000006", ResultContract: schedulerapp.AgentResultContract{Artifacts: []schedulerapp.ArtifactRequirement{requirement}}},
		},
		Run: schedulerapp.ScheduledRun{
			ScheduledRunID: "scheduled-historical-1", ScheduledTaskID: taskID, Status: schedulerapp.RunStatusCompleted,
			TargetMode: schedulerapp.TargetModeAgentTask, TenantID: "system", AgentID: "A000006", RuntimeRunID: runtimeRunID,
			ResultContract: schedulerapp.AgentResultContract{Artifacts: []schedulerapp.ArtifactRequirement{requirement}},
			StartedAt:      time.Now().UTC().Add(-time.Minute), CompletedAt: time.Now().UTC(),
		},
		Artifact: schedulerapp.RunArtifact{
			ID: "scheduled-artifact:" + runtimeRunID + ":weekly", Kind: "report", URI: artifactPath,
			FileName: "weekly.md", SizeBytes: sizeBytes, EvidenceID: "scheduled-evidence:" + runtimeRunID + ":weekly",
		},
	}
}
