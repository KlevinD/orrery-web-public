package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strconv"
	"strings"
	"time"
)

type ArtifactStore interface {
	OpenArtifact(context.Context, RunArtifactAccess) (RunArtifactContent, error)
}

// ArtifactAvailabilityChecker reports whether the exact authorized artifact can
// be opened now. List APIs use it instead of inferring availability from a URI.
type ArtifactAvailabilityChecker interface {
	ArtifactAvailable(context.Context, RunArtifactAccess) bool
}

// HistoricalArtifactArchiver upgrades an authorized historical run artifact
// into the immutable scheduled-run store before it is served.
type HistoricalArtifactArchiver interface {
	ArchiveHistoricalArtifact(context.Context, RunArtifactAccess) (RunArtifact, error)
}

type RunArtifactAccess struct {
	Task     ScheduledTask
	Run      ScheduledRun
	Artifact RunArtifact
}

type OpenRunArtifactQuery struct {
	TenantID        string
	ScheduledTaskID string
	ScheduledRunID  string
	ArtifactID      string
}

type RunArtifactContent struct {
	Reader      io.ReadCloser
	FileName    string
	ContentType string
	SizeBytes   int64
	SHA256      string
	ModifiedAt  time.Time
}

// PublicRunArtifactID keeps storage keys and runtime artifact references out of API payloads.
func PublicRunArtifactID(scheduledRunID string, index int, artifact RunArtifact) string {
	seed := strings.TrimSpace(scheduledRunID) + "\x00" + strconv.Itoa(index) + "\x00" + strings.TrimSpace(artifact.ID)
	digest := sha256.Sum256([]byte(seed))
	return "output-" + hex.EncodeToString(digest[:16])
}
