package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/device/delivery"
	deviceidentity "orrery_v2/internal/device/identity"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/filestore"
)

const (
	defaultDeviceArtifactMaxBytes = 10 * 1024 * 1024
	deviceArtifactUploadTTL       = 5 * time.Minute
	deviceArtifactTicketLimit     = 4096
)

var (
	deviceArtifactIdentifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]{2,159}$`)
	deviceArtifactDigest     = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

type productionDeviceArtifactUploads struct {
	workOrders *db.Store
	files      filestore.Store
	now        func() time.Time
	random     io.Reader
	maxBytes   int64
	maxTickets int

	mu      sync.Mutex
	tickets map[string]productionDeviceArtifactTicket
}

type productionDeviceArtifactTicket struct {
	manifest  delivery.ArtifactManifest
	objectKey string
	expiresAt time.Time
}

func newProductionDeviceArtifactUploads(workOrders *db.Store, files filestore.Store, now func() time.Time, maxBytes int64) (*productionDeviceArtifactUploads, error) {
	if workOrders == nil || files == nil {
		return nil, fmt.Errorf("production device artifact dependencies are incomplete")
	}
	if now == nil {
		now = time.Now
	}
	if maxBytes == 0 {
		maxBytes = defaultDeviceArtifactMaxBytes
	}
	if maxBytes < 1 || maxBytes > 100*1024*1024 {
		return nil, fmt.Errorf("device delivery max artifact bytes is outside the allowed range")
	}
	return &productionDeviceArtifactUploads{
		workOrders: workOrders, files: files, now: now, random: rand.Reader, maxBytes: maxBytes,
		maxTickets: deviceArtifactTicketLimit, tickets: map[string]productionDeviceArtifactTicket{},
	}, nil
}

func (uploads *productionDeviceArtifactUploads) AuthorizeArtifactUpload(ctx context.Context, identity deviceidentity.ConnectionIdentity, manifest delivery.ArtifactManifest) (delivery.ArtifactUploadTicket, error) {
	if err := uploads.validateManifest(manifest); err != nil {
		return delivery.ArtifactUploadTicket{}, err
	}
	if !deviceArtifactIdentifier.MatchString(identity.DeviceID) || strings.TrimSpace(identity.TenantID) == "" || strings.TrimSpace(identity.OwnerID) == "" || identity.CredentialRevision < 1 {
		return delivery.ArtifactUploadTicket{}, delivery.ErrArtifactUnauthorized
	}
	var payload []byte
	var state string
	err := uploads.workOrders.QueryRow(ctx, `
SELECT payload_json, state FROM runtime_device_work_orders
WHERE work_order_id = ? AND tenant_id = ? AND device_id = ?`, manifest.WorkOrderID, identity.TenantID, identity.DeviceID).Scan(&payload, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return delivery.ArtifactUploadTicket{}, delivery.ErrArtifactUnauthorized
	}
	if err != nil {
		return delivery.ArtifactUploadTicket{}, fmt.Errorf("authorize device artifact work order: %w", err)
	}
	if state == "offered" || state == "rejected" {
		return delivery.ArtifactUploadTicket{}, delivery.ErrArtifactUnauthorized
	}
	var order struct {
		RequestedArtifactClasses []string `json:"requested_artifact_classes"`
	}
	if err := json.Unmarshal(payload, &order); err != nil {
		return delivery.ArtifactUploadTicket{}, fmt.Errorf("decode device artifact work order: %w", err)
	}
	if !containsExact(order.RequestedArtifactClasses, manifest.Class) {
		return delivery.ArtifactUploadTicket{}, delivery.ErrArtifactUnauthorized
	}

	secret := make([]byte, 32)
	if _, err := io.ReadFull(uploads.random, secret); err != nil {
		return delivery.ArtifactUploadTicket{}, fmt.Errorf("generate device artifact upload ticket: %w", err)
	}
	ticket := base64.RawURLEncoding.EncodeToString(secret)
	now := uploads.now().UTC()
	expiresAt := now.Add(deviceArtifactUploadTTL)
	tenantDigest := sha256.Sum256([]byte(identity.TenantID))
	objectKey := fmt.Sprintf("device-artifacts/%x/%s/%s", tenantDigest[:12], manifest.WorkOrderID, manifest.ArtifactID)

	uploads.mu.Lock()
	defer uploads.mu.Unlock()
	for candidate, record := range uploads.tickets {
		if !record.expiresAt.After(now) {
			delete(uploads.tickets, candidate)
		}
	}
	if len(uploads.tickets) >= uploads.maxTickets {
		return delivery.ArtifactUploadTicket{}, delivery.ErrArtifactUnavailable
	}
	if _, exists := uploads.tickets[ticket]; exists {
		return delivery.ArtifactUploadTicket{}, delivery.ErrArtifactUnavailable
	}
	uploads.tickets[ticket] = productionDeviceArtifactTicket{
		manifest: manifest, objectKey: objectKey, expiresAt: expiresAt,
	}
	return delivery.ArtifactUploadTicket{Ticket: ticket, ExpiresAt: expiresAt}, nil
}

func (uploads *productionDeviceArtifactUploads) UploadArtifact(ctx context.Context, ticket, contentType string, contentLength int64, reader io.Reader) error {
	ticket = strings.TrimSpace(ticket)
	uploads.mu.Lock()
	record, found := uploads.tickets[ticket]
	if found {
		delete(uploads.tickets, ticket)
	}
	uploads.mu.Unlock()
	if !found || !record.expiresAt.After(uploads.now().UTC()) {
		return delivery.ErrArtifactExpired
	}
	if reader == nil || contentLength != record.manifest.SizeBytes || strings.TrimSpace(contentType) != record.manifest.MediaType {
		return delivery.ErrArtifactInvalidRequest
	}
	content, err := io.ReadAll(io.LimitReader(reader, record.manifest.SizeBytes+1))
	if err != nil {
		return fmt.Errorf("read device artifact upload: %w", err)
	}
	if int64(len(content)) != record.manifest.SizeBytes {
		return delivery.ErrArtifactInvalidRequest
	}
	digest := sha256.Sum256(content)
	presented, err := hex.DecodeString(record.manifest.SHA256)
	if err != nil || subtle.ConstantTimeCompare(digest[:], presented) != 1 {
		return delivery.ErrArtifactInvalidRequest
	}
	if _, err := uploads.files.Upload(ctx, record.objectKey, content, record.manifest.MediaType, record.manifest.DisplayName); err != nil {
		return fmt.Errorf("store device artifact upload: %w", err)
	}
	return nil
}

func (uploads *productionDeviceArtifactUploads) validateManifest(manifest delivery.ArtifactManifest) error {
	if !deviceArtifactIdentifier.MatchString(manifest.WorkOrderID) || !deviceArtifactIdentifier.MatchString(manifest.ArtifactID) ||
		!deviceArtifactDigest.MatchString(manifest.SHA256) || manifest.SizeBytes < 0 || manifest.SizeBytes > uploads.maxBytes ||
		!validDeviceArtifactDisplayName(manifest.DisplayName) {
		return delivery.ErrArtifactInvalidRequest
	}
	switch manifest.Class {
	case "summary":
		if manifest.MediaType != "text/markdown" {
			return delivery.ErrArtifactInvalidRequest
		}
	case "patch":
		if manifest.MediaType != "text/x-diff" {
			return delivery.ErrArtifactInvalidRequest
		}
	case "selected":
		if !containsExact([]string{"application/json", "text/markdown", "text/plain", "text/x-diff"}, manifest.MediaType) {
			return delivery.ErrArtifactInvalidRequest
		}
	default:
		return delivery.ErrArtifactInvalidRequest
	}
	return nil
}

func validDeviceArtifactDisplayName(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 160 || value == "." || value == ".." || strings.ContainsAny(value, `/\\`) {
		return false
	}
	for _, character := range value {
		if character < 32 || character == 127 {
			return false
		}
	}
	return true
}

func containsExact(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

var _ delivery.ArtifactUploadService = (*productionDeviceArtifactUploads)(nil)
