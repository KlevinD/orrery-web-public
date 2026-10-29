package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"orrery_v2/internal/device/delivery"
	deviceidentity "orrery_v2/internal/device/identity"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/filestore"
)

func TestProductionDeviceArtifactUploadsAuthorizeValidateAndStore(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Exec(ctx, `CREATE TABLE runtime_device_work_orders (
work_order_id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, device_id TEXT NOT NULL,
payload_json TEXT NOT NULL, state TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(ctx, `INSERT INTO runtime_device_work_orders
(work_order_id, tenant_id, device_id, payload_json, state) VALUES (?, ?, ?, ?, ?)`,
		"work_order_001", "tenant-a", "device_test_001", `{"requested_artifact_classes":["summary"]}`, "running"); err != nil {
		t.Fatal(err)
	}
	files := filestore.NewLocal(filestore.LocalConfig{Root: t.TempDir()})
	now := time.Date(2026, 7, 26, 14, 0, 0, 0, time.UTC)
	uploads, err := newProductionDeviceArtifactUploads(store, files, func() time.Time { return now }, 1024)
	if err != nil {
		t.Fatal(err)
	}
	identity := deviceidentity.ConnectionIdentity{DeviceID: "device_test_001", TenantID: "tenant-a", OwnerID: "user-1", CredentialRevision: 1}
	content := []byte("completed")
	digest := sha256.Sum256(content)
	manifest := delivery.ArtifactManifest{
		WorkOrderID: "work_order_001", ArtifactID: "artifact_001", Class: "summary", DisplayName: "summary.md",
		MediaType: "text/markdown", SizeBytes: int64(len(content)), SHA256: fmt.Sprintf("%x", digest),
	}
	ticket, err := uploads.AuthorizeArtifactUpload(ctx, identity, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := uploads.UploadArtifact(ctx, ticket.Ticket, manifest.MediaType, manifest.SizeBytes, bytes.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	tenantDigest := sha256.Sum256([]byte(identity.TenantID))
	stored, err := files.GetReader(ctx, fmt.Sprintf("device-artifacts/%x/%s/%s", tenantDigest[:12], manifest.WorkOrderID, manifest.ArtifactID))
	if err != nil {
		t.Fatal(err)
	}
	storedContent, _ := io.ReadAll(stored)
	_ = stored.Close()
	if !bytes.Equal(storedContent, content) {
		t.Fatalf("stored content = %q", storedContent)
	}
	if err := uploads.UploadArtifact(ctx, ticket.Ticket, manifest.MediaType, manifest.SizeBytes, bytes.NewReader(content)); !errors.Is(err, delivery.ErrArtifactExpired) {
		t.Fatalf("replayed ticket error = %v", err)
	}

	manifest.SHA256 = fmt.Sprintf("%064x", 1)
	badTicket, err := uploads.AuthorizeArtifactUpload(ctx, identity, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := uploads.UploadArtifact(ctx, badTicket.Ticket, manifest.MediaType, manifest.SizeBytes, bytes.NewReader(content)); !errors.Is(err, delivery.ErrArtifactInvalidRequest) {
		t.Fatalf("digest mismatch error = %v", err)
	}
	if err := uploads.UploadArtifact(ctx, badTicket.Ticket, manifest.MediaType, manifest.SizeBytes, bytes.NewReader(content)); !errors.Is(err, delivery.ErrArtifactExpired) {
		t.Fatalf("ticket after failed upload error = %v", err)
	}
}

func TestProductionDeviceArtifactUploadsRejectCrossDeviceAndPolicyMismatch(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Exec(ctx, `CREATE TABLE runtime_device_work_orders (
work_order_id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, device_id TEXT NOT NULL,
payload_json TEXT NOT NULL, state TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(ctx, `INSERT INTO runtime_device_work_orders
(work_order_id, tenant_id, device_id, payload_json, state) VALUES (?, ?, ?, ?, ?)`,
		"work_order_001", "tenant-a", "device_other_001", `{"requested_artifact_classes":["patch"]}`, "running"); err != nil {
		t.Fatal(err)
	}
	uploads, err := newProductionDeviceArtifactUploads(store, filestore.NewLocal(filestore.LocalConfig{Root: t.TempDir()}), time.Now, 1024)
	if err != nil {
		t.Fatal(err)
	}
	identity := deviceidentity.ConnectionIdentity{DeviceID: "device_test_001", TenantID: "tenant-a", OwnerID: "user-1", CredentialRevision: 1}
	manifest := delivery.ArtifactManifest{
		WorkOrderID: "work_order_001", ArtifactID: "artifact_001", Class: "summary", DisplayName: "summary.md",
		MediaType: "text/markdown", SizeBytes: 1, SHA256: fmt.Sprintf("%064x", 1),
	}
	if _, err := uploads.AuthorizeArtifactUpload(ctx, identity, manifest); !errors.Is(err, delivery.ErrArtifactUnauthorized) {
		t.Fatalf("cross-device authorization error = %v", err)
	}
}

func TestProductionDeviceArtifactUploadsBoundOutstandingTicketsAndPruneExpired(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Exec(ctx, `CREATE TABLE runtime_device_work_orders (
work_order_id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, device_id TEXT NOT NULL,
payload_json TEXT NOT NULL, state TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(ctx, `INSERT INTO runtime_device_work_orders
(work_order_id, tenant_id, device_id, payload_json, state) VALUES (?, ?, ?, ?, ?)`,
		"work_order_001", "tenant-a", "device_test_001", `{"requested_artifact_classes":["summary"]}`, "running"); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 26, 14, 0, 0, 0, time.UTC)
	uploads, err := newProductionDeviceArtifactUploads(store, filestore.NewLocal(filestore.LocalConfig{Root: t.TempDir()}), func() time.Time { return now }, 1024)
	if err != nil {
		t.Fatal(err)
	}
	uploads.maxTickets = 1
	identity := deviceidentity.ConnectionIdentity{DeviceID: "device_test_001", TenantID: "tenant-a", OwnerID: "user-1", CredentialRevision: 1}
	manifest := delivery.ArtifactManifest{
		WorkOrderID: "work_order_001", ArtifactID: "artifact_001", Class: "summary", DisplayName: "summary.md",
		MediaType: "text/markdown", SizeBytes: 1, SHA256: fmt.Sprintf("%064x", 1),
	}
	if _, err := uploads.AuthorizeArtifactUpload(ctx, identity, manifest); err != nil {
		t.Fatal(err)
	}
	manifest.ArtifactID = "artifact_002"
	if _, err := uploads.AuthorizeArtifactUpload(ctx, identity, manifest); !errors.Is(err, delivery.ErrArtifactUnavailable) {
		t.Fatalf("ticket capacity error = %v", err)
	}
	now = now.Add(deviceArtifactUploadTTL)
	if _, err := uploads.AuthorizeArtifactUpload(ctx, identity, manifest); err != nil {
		t.Fatalf("authorize after expired ticket pruning: %v", err)
	}
}
