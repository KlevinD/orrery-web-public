package agenttrace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"time"
)

const archiveRetention = 365 * 24 * time.Hour

type RetentionRepository interface {
	ListRetentionCandidates(context.Context, time.Time, int) ([]TraceRecord, error)
	Save(context.Context, TraceRecord) error
}

type RetentionObjectStore interface {
	ReadTrace(context.Context, string) ([]ArchiveEntry, error)
	WriteArchive(context.Context, TraceRecord, []byte) (string, error)
	ReadObject(context.Context, string) ([]byte, error)
	DeleteHot(context.Context, string) error
	DeleteArchive(context.Context, string) error
}

type RetentionReport struct {
	Failed   int
	Missing  int
	Scanned  int
	Archived int
	Purged   int
	Bytes    int64
}

type RetentionService struct {
	repository RetentionRepository
	objects    RetentionObjectStore
	now        func() time.Time
}

func NewRetentionService(repository RetentionRepository, objects RetentionObjectStore, now func() time.Time) *RetentionService {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &RetentionService{repository: repository, objects: objects, now: now}
}

func (s *RetentionService) Run(ctx context.Context, limit int) (RetentionReport, error) {
	if s == nil || s.repository == nil || s.objects == nil {
		return RetentionReport{}, fmt.Errorf("trace retention dependencies are required")
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	now := s.now().UTC()
	records, err := s.repository.ListRetentionCandidates(ctx, now, limit)
	if err != nil {
		return RetentionReport{}, err
	}
	report := RetentionReport{Scanned: len(records)}
	var resultErr error
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return report, errors.Join(resultErr, err)
		}
		err := func() error {
			switch record.Status {
			case TraceHot, TraceMissing:
				entries, err := s.objects.ReadTrace(ctx, record.TraceRef)
				if err != nil {
					if errors.Is(err, fs.ErrNotExist) {
						record.Status = TraceMissing
						if saveErr := s.repository.Save(ctx, record); saveErr != nil {
							return errors.Join(err, saveErr)
						}
						report.Missing++
					}
					return err
				}
				archive, err := BuildArchive(entries)
				if err != nil {
					return err
				}
				archiveRef, err := s.objects.WriteArchive(ctx, record, archive)
				if err != nil {
					return err
				}
				readback, err := s.objects.ReadObject(ctx, archiveRef)
				if err != nil || !equalBytes(readback, archive) {
					return fmt.Errorf("trace archive readback mismatch: %w", err)
				}
				if err := VerifyArchive(readback, len(entries)); err != nil {
					return err
				}
				digest := sha256.Sum256(archive)
				record.Status = TraceArchived
				record.ArchiveRef = archiveRef
				record.ArchiveSHA256 = hex.EncodeToString(digest[:])
				record.ArchivedAt = now
				record.PurgeAfter = now.Add(archiveRetention)
				if err := s.repository.Save(ctx, record); err != nil {
					return err
				}
				record.Revision++
				if err := s.objects.DeleteHot(ctx, record.TraceRef); err != nil {
					return err
				}
				record.HotDeletedAt = now
				if err := s.repository.Save(ctx, record); err != nil {
					return err
				}
				report.Archived++
				report.Bytes += int64(len(archive))
			case TraceArchived:
				if record.HotDeletedAt.IsZero() {
					if err := s.objects.DeleteHot(ctx, record.TraceRef); err != nil {
						return err
					}
					record.HotDeletedAt = now
					if err := s.repository.Save(ctx, record); err != nil {
						return err
					}
					return nil
				}
				if !record.LegalHoldAt.IsZero() {
					return nil
				}
				if err := s.objects.DeleteArchive(ctx, record.ArchiveRef); err != nil {
					return err
				}
				record.Status = TracePurged
				record.PurgedAt = now
				if err := s.repository.Save(ctx, record); err != nil {
					return err
				}
				report.Purged++
			}
			return nil
		}()
		if err != nil {
			report.Failed++
			resultErr = errors.Join(resultErr, fmt.Errorf("retain trace %s/%s: %w", record.TenantID, record.RunID, err))
		}
	}
	return report, resultErr
}

func hotRetentionForStatus(status string) time.Duration {
	if status == "completed" {
		return completedTraceHotRetention
	}
	return abnormalTraceHotRetention
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
