package delivery

import (
	"context"
	"errors"
	"io"
	"time"

	deviceidentity "orrery_v2/internal/device/identity"
)

var (
	ErrArtifactInvalidRequest = errors.New("device artifact upload request is invalid")
	ErrArtifactUnauthorized   = errors.New("device artifact upload is unauthorized")
	ErrArtifactExpired        = errors.New("device artifact upload ticket is expired")
	ErrArtifactUnavailable    = errors.New("device artifact upload is temporarily unavailable")
)

type ArtifactManifest struct {
	WorkOrderID string `json:"work_order_id"`
	ArtifactID  string `json:"artifact_id"`
	Class       string `json:"class"`
	DisplayName string `json:"display_name"`
	MediaType   string `json:"media_type"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256"`
}

type ArtifactUploadTicket struct {
	Ticket    string
	ExpiresAt time.Time
}

type ArtifactUploadService interface {
	AuthorizeArtifactUpload(context.Context, deviceidentity.ConnectionIdentity, ArtifactManifest) (ArtifactUploadTicket, error)
	UploadArtifact(context.Context, string, string, int64, io.Reader) error
}
