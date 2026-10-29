package ontology

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

type StateRefreshStatus string

const (
	StateRefreshRunning   StateRefreshStatus = "running"
	StateRefreshSucceeded StateRefreshStatus = "succeeded"
	StateRefreshFailed    StateRefreshStatus = "failed"
)

const stateRefreshManifestErrorLimit = 4000

type StateRefreshManifest struct {
	RefreshKind       string
	Fingerprint       string
	SnapshotDate      string
	MappingRevision   int64
	SourceCount       int
	ChangedCount      int
	SnapshotOnlyCount int
	NoopCount         int
	StaleCount        int
	Status            StateRefreshStatus
	StartedAt         time.Time
	CompletedAt       time.Time
	LastError         string
}

type PlatformStateRefreshManifestStore struct {
	store *db.Store
}

func NewPlatformStateRefreshManifestStore(store *db.Store) *PlatformStateRefreshManifestStore {
	return &PlatformStateRefreshManifestStore{store: store}
}

func (s *PlatformStateRefreshManifestStore) Claim(ctx context.Context, manifest StateRefreshManifest) (bool, error) {
	manifest, err := canonicalStateRefreshManifest(manifest, true, false)
	if err != nil {
		return false, err
	}
	if s == nil || s.store == nil {
		return false, fmt.Errorf("ontology state refresh manifest store is required")
	}
	claimed := false
	err = s.store.WithTx(ctx, func(tx db.Tx) error {
		if err := lockStateRefreshManifest(ctx, tx, manifest); err != nil {
			return err
		}
		current, found, err := getStateRefreshManifestTx(ctx, tx, manifest.RefreshKind, manifest.Fingerprint)
		if err != nil {
			return err
		}
		if found && current.Status != StateRefreshFailed {
			return nil
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO ontology_refresh_manifests (
	refresh_kind, fingerprint, snapshot_date, mapping_revision, source_count,
	changed_count, snapshot_only_count, noop_count, stale_count,
	status, started_at, completed_at, last_error
) VALUES (?, ?, ?, ?, ?, 0, 0, 0, 0, ?, ?, 0, '')
ON CONFLICT(refresh_kind, fingerprint) DO UPDATE SET
	snapshot_date = excluded.snapshot_date,
	mapping_revision = excluded.mapping_revision,
	source_count = excluded.source_count,
	changed_count = 0,
	snapshot_only_count = 0,
	noop_count = 0,
	stale_count = 0,
	status = excluded.status,
	started_at = excluded.started_at,
	completed_at = 0,
	last_error = ''`,
			manifest.RefreshKind,
			manifest.Fingerprint,
			manifest.SnapshotDate,
			manifest.MappingRevision,
			manifest.SourceCount,
			StateRefreshRunning,
			manifest.StartedAt.UnixNano(),
		)
		if err != nil {
			return fmt.Errorf("claim ontology state refresh manifest: %w", err)
		}
		claimed = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return claimed, nil
}

func (s *PlatformStateRefreshManifestStore) Complete(ctx context.Context, manifest StateRefreshManifest) error {
	manifest, err := canonicalStateRefreshManifest(manifest, false, true)
	if err != nil {
		return err
	}
	return s.finish(ctx, manifest, StateRefreshSucceeded)
}

func (s *PlatformStateRefreshManifestStore) Fail(ctx context.Context, manifest StateRefreshManifest) error {
	manifest, err := canonicalStateRefreshManifest(manifest, false, true)
	if err != nil {
		return err
	}
	manifest.LastError = boundStateRefreshManifestError(manifest.LastError)
	return s.finish(ctx, manifest, StateRefreshFailed)
}

func (s *PlatformStateRefreshManifestStore) finish(ctx context.Context, manifest StateRefreshManifest, status StateRefreshStatus) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("ontology state refresh manifest store is required")
	}
	return s.store.WithTx(ctx, func(tx db.Tx) error {
		if err := lockStateRefreshManifest(ctx, tx, manifest); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
UPDATE ontology_refresh_manifests
SET source_count = ?, changed_count = ?, snapshot_only_count = ?, noop_count = ?, stale_count = ?,
	status = ?, completed_at = ?, last_error = ?
WHERE refresh_kind = ? AND fingerprint = ? AND status = ?`,
			manifest.SourceCount,
			manifest.ChangedCount,
			manifest.SnapshotOnlyCount,
			manifest.NoopCount,
			manifest.StaleCount,
			status,
			manifest.CompletedAt.UnixNano(),
			manifest.LastError,
			manifest.RefreshKind,
			manifest.Fingerprint,
			StateRefreshRunning,
		)
		if err != nil {
			return fmt.Errorf("finish ontology state refresh manifest: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("inspect ontology state refresh manifest completion: %w", err)
		}
		if rows != 1 {
			return fmt.Errorf("ontology state refresh manifest is not running")
		}
		return nil
	})
}

func (s *PlatformStateRefreshManifestStore) Get(ctx context.Context, refreshKind, fingerprint string) (StateRefreshManifest, bool, error) {
	key, err := canonicalStateRefreshManifest(StateRefreshManifest{RefreshKind: refreshKind, Fingerprint: fingerprint}, false, false)
	if err != nil {
		return StateRefreshManifest{}, false, err
	}
	if s == nil || s.store == nil {
		return StateRefreshManifest{}, false, fmt.Errorf("ontology state refresh manifest store is required")
	}
	var manifest StateRefreshManifest
	var found bool
	err = s.store.WithTx(ctx, func(tx db.Tx) error {
		if err := lockStateRefreshManifest(ctx, tx, key); err != nil {
			return err
		}
		var err error
		manifest, found, err = getStateRefreshManifestTx(ctx, tx, key.RefreshKind, key.Fingerprint)
		return err
	})
	if err != nil {
		return StateRefreshManifest{}, false, err
	}
	return manifest, found, nil
}

func getStateRefreshManifestTx(ctx context.Context, tx db.Tx, refreshKind, fingerprint string) (StateRefreshManifest, bool, error) {
	var manifest StateRefreshManifest
	var startedAt, completedAt int64
	err := tx.QueryRowContext(ctx, `
SELECT refresh_kind, fingerprint, snapshot_date, mapping_revision, source_count,
	changed_count, snapshot_only_count, noop_count, stale_count,
	status, started_at, completed_at, last_error
FROM ontology_refresh_manifests
WHERE refresh_kind = ? AND fingerprint = ?`, refreshKind, fingerprint).Scan(
		&manifest.RefreshKind,
		&manifest.Fingerprint,
		&manifest.SnapshotDate,
		&manifest.MappingRevision,
		&manifest.SourceCount,
		&manifest.ChangedCount,
		&manifest.SnapshotOnlyCount,
		&manifest.NoopCount,
		&manifest.StaleCount,
		&manifest.Status,
		&startedAt,
		&completedAt,
		&manifest.LastError,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return StateRefreshManifest{}, false, nil
	}
	if err != nil {
		return StateRefreshManifest{}, false, fmt.Errorf("read ontology state refresh manifest: %w", err)
	}
	if !validStateRefreshStatus(manifest.Status) {
		return StateRefreshManifest{}, false, fmt.Errorf("invalid ontology state refresh status %q", manifest.Status)
	}
	manifest.StartedAt = stateRefreshManifestTime(startedAt)
	manifest.CompletedAt = stateRefreshManifestTime(completedAt)
	return manifest, true, nil
}

func canonicalStateRefreshManifest(manifest StateRefreshManifest, requireStartedAt, requireCompletedAt bool) (StateRefreshManifest, error) {
	manifest.RefreshKind = strings.TrimSpace(manifest.RefreshKind)
	manifest.Fingerprint = strings.TrimSpace(manifest.Fingerprint)
	manifest.SnapshotDate = strings.TrimSpace(manifest.SnapshotDate)
	if manifest.RefreshKind == "" || manifest.Fingerprint == "" {
		return StateRefreshManifest{}, fmt.Errorf("ontology state refresh kind and fingerprint are required")
	}
	if manifest.SnapshotDate != "" {
		date, err := time.Parse("2006-01-02", manifest.SnapshotDate)
		if err != nil || date.Format("2006-01-02") != manifest.SnapshotDate {
			return StateRefreshManifest{}, fmt.Errorf("ontology state refresh snapshot date must be UTC YYYY-MM-DD")
		}
	}
	if requireStartedAt && manifest.StartedAt.IsZero() {
		return StateRefreshManifest{}, fmt.Errorf("ontology state refresh start time is required")
	}
	if requireCompletedAt && manifest.CompletedAt.IsZero() {
		return StateRefreshManifest{}, fmt.Errorf("ontology state refresh completion time is required")
	}
	if manifest.MappingRevision < 0 || manifest.SourceCount < 0 || manifest.ChangedCount < 0 ||
		manifest.SnapshotOnlyCount < 0 || manifest.NoopCount < 0 || manifest.StaleCount < 0 {
		return StateRefreshManifest{}, fmt.Errorf("ontology state refresh revision and counts cannot be negative")
	}
	manifest.StartedAt = manifest.StartedAt.UTC()
	manifest.CompletedAt = manifest.CompletedAt.UTC()
	return manifest, nil
}

func lockStateRefreshManifest(ctx context.Context, tx db.Tx, manifest StateRefreshManifest) error {
	return db.LockRecordKeyTx(ctx, tx, "ontology_refresh_manifests", manifest.RefreshKind+"\x00"+manifest.Fingerprint)
}

func boundStateRefreshManifestError(message string) string {
	runes := []rune(message)
	if len(runes) > stateRefreshManifestErrorLimit {
		runes = runes[:stateRefreshManifestErrorLimit]
	}
	return string(runes)
}

func stateRefreshManifestTime(nanos int64) time.Time {
	if nanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, nanos).UTC()
}

func validStateRefreshStatus(status StateRefreshStatus) bool {
	switch status {
	case StateRefreshRunning, StateRefreshSucceeded, StateRefreshFailed:
		return true
	default:
		return false
	}
}
