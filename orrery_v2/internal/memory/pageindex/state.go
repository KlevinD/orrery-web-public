package pageindex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	"orrery_v2/internal/platform/db"
)

const (
	profileBindingTable  = "memory_pageindex_profiles"
	documentBindingTable = "memory_pageindex_documents"
)

type ProfileBinding struct {
	ProfileID agentcore.MemoryProfileID `json:"profile_id"`
	FolderID  string                    `json:"folder_id"`
	UpdatedAt time.Time                 `json:"updated_at"`
}

type DocumentBinding struct {
	MemoryID           string                    `json:"memory_id"`
	ProfileID          agentcore.MemoryProfileID `json:"profile_id"`
	FolderID           string                    `json:"folder_id"`
	ProviderDocumentID string                    `json:"provider_document_id"`
	Revision           int                       `json:"revision"`
	ContentHash        string                    `json:"content_hash"`
	Status             string                    `json:"status"`
	ErrorMessage       string                    `json:"error_message,omitempty"`
	VerifiedAt         time.Time                 `json:"verified_at,omitempty"`
	UpdatedAt          time.Time                 `json:"updated_at"`
}

type State interface {
	GetProfileBinding(context.Context, agentcore.MemoryProfileID) (ProfileBinding, error)
	SaveProfileBinding(context.Context, ProfileBinding) error
	GetDocumentBinding(context.Context, string) (DocumentBinding, error)
	FindDocumentBindingByProviderID(context.Context, string) (DocumentBinding, error)
	SaveDocumentBinding(context.Context, DocumentBinding) error
}

type DocumentBindingState interface {
	ListDocumentBindings(context.Context, int) ([]DocumentBinding, error)
	DeleteDocumentBinding(context.Context, string) error
}

type PlatformState struct {
	store *db.Store
}

type ProfileConsolidationReport struct {
	SourceFolderID          string
	TargetFolderID          string
	DocumentBindingsMoved   int
	DocumentBindingsRemoved int
}

func NewPlatformState(store *db.Store) *PlatformState {
	return &PlatformState{store: store}
}

func (s *PlatformState) GetProfileBinding(ctx context.Context, profileID agentcore.MemoryProfileID) (ProfileBinding, error) {
	return getBinding[ProfileBinding](ctx, s.store, profileBindingTable, string(profileID), "memory PageIndex profile binding")
}

func (s *PlatformState) SaveProfileBinding(ctx context.Context, binding ProfileBinding) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("memory PageIndex state is not configured")
	}
	if binding.ProfileID == "" || strings.TrimSpace(binding.FolderID) == "" {
		return fmt.Errorf("%w: memory PageIndex profile and folder are required", memcore.ErrInvalid)
	}
	return putBinding(ctx, s.store, profileBindingTable, string(binding.ProfileID), binding)
}

func (s *PlatformState) GetDocumentBinding(ctx context.Context, memoryID string) (DocumentBinding, error) {
	return getBinding[DocumentBinding](ctx, s.store, documentBindingTable, strings.TrimSpace(memoryID), "memory PageIndex document binding")
}

func (s *PlatformState) FindDocumentBindingByProviderID(ctx context.Context, providerDocumentID string) (DocumentBinding, error) {
	if s == nil || s.store == nil {
		return DocumentBinding{}, fmt.Errorf("memory PageIndex state is not configured")
	}
	providerDocumentID = strings.TrimSpace(providerDocumentID)
	if providerDocumentID == "" {
		return DocumentBinding{}, fmt.Errorf("%w: provider document id is required", memcore.ErrInvalid)
	}
	afterKey := ""
	for {
		page, err := s.store.ListRecords(ctx, documentBindingTable, afterKey, db.MaxRecordPageLimit)
		if err != nil {
			return DocumentBinding{}, err
		}
		for _, item := range page.Items {
			binding, err := decodeBinding[DocumentBinding](item.Record, "memory PageIndex document binding")
			if err != nil {
				return DocumentBinding{}, err
			}
			if strings.TrimSpace(binding.ProviderDocumentID) == providerDocumentID {
				return binding, nil
			}
			afterKey = item.Key
		}
		if !page.HasMore || len(page.Items) == 0 {
			break
		}
	}
	return DocumentBinding{}, fmt.Errorf("%w: PageIndex document %s", memcore.ErrNotFound, providerDocumentID)
}

func (s *PlatformState) SaveDocumentBinding(ctx context.Context, binding DocumentBinding) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("memory PageIndex state is not configured")
	}
	if strings.TrimSpace(binding.MemoryID) == "" || binding.ProfileID == "" || strings.TrimSpace(binding.FolderID) == "" {
		return fmt.Errorf("%w: memory PageIndex document identity is incomplete", memcore.ErrInvalid)
	}
	return putBinding(ctx, s.store, documentBindingTable, binding.MemoryID, binding)
}

func (s *PlatformState) ListDocumentBindings(ctx context.Context, limit int) ([]DocumentBinding, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("memory PageIndex state is not configured")
	}
	out := make([]DocumentBinding, 0)
	afterKey := ""
	for {
		pageLimit := db.MaxRecordPageLimit
		if limit > 0 && limit-len(out) < pageLimit {
			pageLimit = limit - len(out)
		}
		if pageLimit <= 0 {
			return out, nil
		}
		page, err := s.store.ListRecords(ctx, documentBindingTable, afterKey, pageLimit)
		if err != nil {
			return nil, err
		}
		for _, item := range page.Items {
			binding, err := decodeBinding[DocumentBinding](item.Record, "memory PageIndex document binding")
			if err != nil {
				return nil, err
			}
			out = append(out, binding)
			afterKey = item.Key
		}
		if !page.HasMore || len(page.Items) == 0 || limit > 0 && len(out) >= limit {
			return out, nil
		}
	}
}

func (s *PlatformState) DeleteDocumentBinding(ctx context.Context, memoryID string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("memory PageIndex state is not configured")
	}
	memoryID = strings.TrimSpace(memoryID)
	if memoryID == "" {
		return fmt.Errorf("%w: memory id is required", memcore.ErrInvalid)
	}
	return s.store.DeleteRecord(ctx, documentBindingTable, memoryID)
}

// ReassignProfile preserves the PageIndex folder and document identities when
// an organization-scoped memory profile moves to its canonical tenant ID.
func (s *PlatformState) ReassignProfile(ctx context.Context, fromID, toID agentcore.MemoryProfileID) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("memory PageIndex state is not configured")
	}
	if fromID == "" || toID == "" || fromID == toID {
		return nil
	}
	binding, err := s.GetProfileBinding(ctx, fromID)
	if err == nil {
		if _, targetErr := s.GetProfileBinding(ctx, toID); errors.Is(targetErr, memcore.ErrNotFound) {
			binding.ProfileID = toID
			if err := s.SaveProfileBinding(ctx, binding); err != nil {
				return err
			}
		} else if targetErr != nil {
			return targetErr
		}
		if err := s.store.DeleteRecord(ctx, profileBindingTable, string(fromID)); err != nil {
			return err
		}
	} else if !errors.Is(err, memcore.ErrNotFound) {
		return err
	}
	afterKey := ""
	for {
		page, err := s.store.ListRecords(ctx, documentBindingTable, afterKey, db.MaxRecordPageLimit)
		if err != nil {
			return err
		}
		for _, item := range page.Items {
			binding, err := decodeBinding[DocumentBinding](item.Record, "memory PageIndex document binding")
			if err != nil {
				return err
			}
			if binding.ProfileID == fromID {
				binding.ProfileID = toID
				if err := s.SaveDocumentBinding(ctx, binding); err != nil {
					return err
				}
			}
			afterKey = item.Key
		}
		if !page.HasMore || len(page.Items) == 0 {
			return nil
		}
	}
}

// ConsolidateProfile preserves the canonical folder when both profile IDs
// already have PageIndex state. Source document bindings are removed in that
// case so the reconciler uploads those records into the canonical folder.
func (s *PlatformState) ConsolidateProfile(ctx context.Context, fromID, toID agentcore.MemoryProfileID) (ProfileConsolidationReport, error) {
	report := ProfileConsolidationReport{}
	if s == nil || s.store == nil {
		return report, fmt.Errorf("memory PageIndex state is not configured")
	}
	if fromID == "" || toID == "" || fromID == toID {
		return report, nil
	}
	source, err := s.GetProfileBinding(ctx, fromID)
	if errors.Is(err, memcore.ErrNotFound) {
		return report, nil
	}
	if err != nil {
		return report, err
	}
	report.SourceFolderID = source.FolderID
	target, targetErr := s.GetProfileBinding(ctx, toID)
	if errors.Is(targetErr, memcore.ErrNotFound) {
		source.ProfileID = toID
		if err := s.SaveProfileBinding(ctx, source); err != nil {
			return report, err
		}
		report.TargetFolderID = source.FolderID
	} else if targetErr != nil {
		return report, targetErr
	} else {
		report.TargetFolderID = target.FolderID
	}
	afterKey := ""
	for {
		page, err := s.store.ListRecords(ctx, documentBindingTable, afterKey, db.MaxRecordPageLimit)
		if err != nil {
			return report, err
		}
		for _, item := range page.Items {
			binding, err := decodeBinding[DocumentBinding](item.Record, "memory PageIndex document binding")
			if err != nil {
				return report, err
			}
			if binding.ProfileID == fromID {
				if targetErr == nil {
					if err := s.store.DeleteRecord(ctx, documentBindingTable, binding.MemoryID); err != nil {
						return report, err
					}
					report.DocumentBindingsRemoved++
				} else {
					binding.ProfileID = toID
					if err := s.SaveDocumentBinding(ctx, binding); err != nil {
						return report, err
					}
					report.DocumentBindingsMoved++
				}
			}
			afterKey = item.Key
		}
		if !page.HasMore || len(page.Items) == 0 {
			break
		}
	}
	if err := s.store.DeleteRecord(ctx, profileBindingTable, string(fromID)); err != nil {
		return report, err
	}
	return report, nil
}

func getBinding[T any](ctx context.Context, store *db.Store, table, key, label string) (T, error) {
	var zero T
	if store == nil {
		return zero, fmt.Errorf("memory PageIndex state is not configured")
	}
	record, ok, err := store.GetRecord(ctx, table, strings.TrimSpace(key))
	if err != nil {
		return zero, err
	}
	if !ok {
		return zero, fmt.Errorf("%w: %s", memcore.ErrNotFound, key)
	}
	return decodeBinding[T](record, label)
}

func putBinding(ctx context.Context, store *db.Store, table, key string, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return store.PutRecord(ctx, table, strings.TrimSpace(key), db.Record{"payload": string(payload)})
}

func decodeBinding[T any](record db.Record, label string) (T, error) {
	var value T
	raw, ok := record["payload"].(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return value, fmt.Errorf("%w: %s payload missing", memcore.ErrInvalid, label)
	}
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return value, fmt.Errorf("decode %s: %w", label, err)
	}
	return value, nil
}
