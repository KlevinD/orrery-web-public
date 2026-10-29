package contextgraph

import (
	"context"
	"fmt"
	"strings"

	"orrery_v2/internal/platform/db"
)

// Objects reads the entity dictionary without loading facts, links or evidence.
func (s *PlatformStore) Objects(ctx context.Context, tenantID string) ([]Object, error) {
	if !s.ready() || strings.TrimSpace(tenantID) == "" {
		return nil, fmt.Errorf("graph store and tenant id are required")
	}
	return s.listObjects(ctx, strings.TrimSpace(tenantID))
}

// ObjectsByID uses exact tenant-scoped lookups for catalog and endpoint checks.
func (s *PlatformStore) ObjectsByID(ctx context.Context, tenantID string, ids []string) ([]Object, error) {
	if !s.ready() || strings.TrimSpace(tenantID) == "" {
		return nil, fmt.Errorf("graph store and tenant id are required")
	}
	objects := []Object{}
	for start := 0; start < len(ids); start += 500 {
		batch, err := s.listSelectedObjects(ctx, strings.TrimSpace(tenantID), ids[start:min(start+500, len(ids))])
		if err != nil {
			return nil, err
		}
		objects = append(objects, batch...)
	}
	return objects, nil
}

func (s *PlatformStore) SnapshotObjectsLinks(ctx context.Context, tenantID string) ([]Object, []Link, error) {
	if !s.ready() || strings.TrimSpace(tenantID) == "" {
		return nil, nil, fmt.Errorf("graph store and tenant id are required")
	}
	var objects []Object
	var links []Link
	read := func(store *PlatformStore) error {
		var err error
		objects, err = store.listObjects(ctx, strings.TrimSpace(tenantID))
		if err != nil {
			return err
		}
		links, err = store.listLinks(ctx, strings.TrimSpace(tenantID))
		return err
	}
	if s.tx != nil {
		err := read(s)
		return objects, links, err
	}
	err := s.store.WithReadTx(ctx, func(tx db.Tx) error { return read(NewPlatformStoreTx(tx)) })
	return objects, links, err
}
