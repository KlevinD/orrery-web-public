package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"orrery_v2/internal/platform/db"
)

const platformBundleRegistryTable = "resource_skill_bundles"

type PlatformBundleRegistry struct {
	store *db.Store
}

func NewPlatformBundleRegistry(store *db.Store) *PlatformBundleRegistry {
	return &PlatformBundleRegistry{store: store}
}

func (r *PlatformBundleRegistry) Create(ctx context.Context, bundle SkillBundle) error {
	normalized, err := normalizeSkillBundle(bundle)
	if err != nil {
		return err
	}
	if _, err := r.Get(ctx, normalized.ID); err == nil {
		return fmt.Errorf("%w: %s", ErrAlreadyExists, normalized.ID)
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	return r.put(ctx, normalized)
}

func (r *PlatformBundleRegistry) Update(ctx context.Context, bundle SkillBundle) error {
	normalized, err := normalizeSkillBundle(bundle)
	if err != nil {
		return err
	}
	if _, err := r.Get(ctx, normalized.ID); err != nil {
		return err
	}
	return r.put(ctx, normalized)
}

func (r *PlatformBundleRegistry) Delete(ctx context.Context, id string) error {
	if _, err := r.Get(ctx, id); err != nil {
		return err
	}
	return r.store.PutRecord(ctx, platformBundleRegistryTable, id, db.Record{"deleted": true})
}

func (r *PlatformBundleRegistry) Get(ctx context.Context, id string) (SkillBundle, error) {
	record, ok, err := r.store.GetRecord(ctx, platformBundleRegistryTable, id)
	if err != nil {
		return SkillBundle{}, err
	}
	if !ok {
		return SkillBundle{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if deleted, _ := record["deleted"].(bool); deleted {
		return SkillBundle{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return decodeSkillBundle(record)
}

func (r *PlatformBundleRegistry) List(ctx context.Context) ([]SkillBundle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]SkillBundle, 0)
	afterID := ""
	for {
		page, err := r.store.ListRecords(ctx, platformBundleRegistryTable, afterID, db.MaxRecordPageLimit)
		if err != nil {
			return nil, err
		}
		for _, item := range page.Items {
			afterID = item.Key
			if deleted, _ := item.Record["deleted"].(bool); deleted {
				continue
			}
			bundle, err := decodeSkillBundle(item.Record)
			if err != nil {
				return nil, err
			}
			out = append(out, bundle)
		}
		if !page.HasMore {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (r *PlatformBundleRegistry) put(ctx context.Context, bundle SkillBundle) error {
	payload, err := json.Marshal(bundle.Clone())
	if err != nil {
		return fmt.Errorf("encode skill bundle: %w", err)
	}
	return r.store.PutRecord(ctx, platformBundleRegistryTable, bundle.ID, db.Record{"payload": string(payload)})
}

func decodeSkillBundle(record db.Record) (SkillBundle, error) {
	raw, ok := record["payload"].(string)
	if !ok || raw == "" {
		return SkillBundle{}, fmt.Errorf("skill bundle payload missing")
	}
	var bundle SkillBundle
	if err := json.Unmarshal([]byte(raw), &bundle); err != nil {
		return SkillBundle{}, fmt.Errorf("decode skill bundle: %w", err)
	}
	normalized, err := normalizeSkillBundle(bundle)
	if err != nil {
		return SkillBundle{}, err
	}
	return normalized.Clone(), nil
}
