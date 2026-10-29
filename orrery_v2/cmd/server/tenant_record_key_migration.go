package main

import (
	"context"
	"fmt"
	"strings"

	"orrery_v2/internal/platform/db"
)

const legacyTenantRecordMigrationPageSize = 200

type legacyTenantRecordTable struct {
	logicalTable string
	idField      string
}

func migrateLegacyTenantRecordKeys(ctx context.Context, router *db.Router) error {
	if router == nil {
		return fmt.Errorf("database router is required for tenant record migration")
	}
	for _, table := range []legacyTenantRecordTable{
		{logicalTable: "core.projects", idField: "project_id"},
		{logicalTable: "core.mission_control_projection", idField: "mission_id"},
	} {
		if err := migrateLegacyTenantRecordTable(ctx, router, table); err != nil {
			return err
		}
	}
	return nil
}

func migrateLegacyTenantRecordTable(ctx context.Context, router *db.Router, definition legacyTenantRecordTable) error {
	store, physicalTable, err := router.StoreForTable(definition.logicalTable)
	if err != nil {
		return err
	}
	after := ""
	for {
		page, err := router.ListRecords(ctx, definition.logicalTable, after, legacyTenantRecordMigrationPageSize)
		if err != nil {
			return fmt.Errorf("list legacy records from %s: %w", definition.logicalTable, err)
		}
		for _, item := range page.Items {
			after = item.Key
			id, _ := item.Record[definition.idField].(string)
			id = strings.TrimSpace(id)
			if id == "" {
				return fmt.Errorf("legacy %s record %q has an invalid %s", definition.logicalTable, item.Key, definition.idField)
			}
			tenant, _ := item.Record["tenant_id"].(string)
			tenant = strings.TrimSpace(tenant)
			if tenant == "" {
				tenant = "global"
			}
			if item.Key == tenant+"/"+id {
				continue
			}
			if id != item.Key {
				return fmt.Errorf("legacy %s record %q has an invalid %s", definition.logicalTable, item.Key, definition.idField)
			}
			if tenant != "global" {
				return fmt.Errorf("legacy %s record %q has non-global tenant %q", definition.logicalTable, item.Key, tenant)
			}
			newKey := "global/" + id
			if err := store.WithTx(ctx, func(tx db.Tx) error {
				if err := db.LockRecordKeyTx(ctx, tx, physicalTable, item.Key); err != nil {
					return err
				}
				if err := db.LockRecordKeyTx(ctx, tx, physicalTable, newKey); err != nil {
					return err
				}
				current, found, err := db.GetRecordTx(ctx, tx, physicalTable, item.Key)
				if err != nil || !found {
					return err
				}
				if _, exists, err := db.GetRecordTx(ctx, tx, physicalTable, newKey); err != nil {
					return err
				} else if exists {
					return fmt.Errorf("tenant record migration collision in %s for %q", definition.logicalTable, newKey)
				}
				current["tenant_id"] = "global"
				if err := db.PutRecordTx(ctx, tx, physicalTable, newKey, current); err != nil {
					return err
				}
				return db.DeleteRecordTx(ctx, tx, physicalTable, item.Key)
			}); err != nil {
				return fmt.Errorf("migrate %s record %q: %w", definition.logicalTable, item.Key, err)
			}
		}
		if !page.HasMore {
			return nil
		}
		if len(page.Items) == 0 {
			return fmt.Errorf("legacy tenant record migration for %s returned an empty continuation page", definition.logicalTable)
		}
	}
}
