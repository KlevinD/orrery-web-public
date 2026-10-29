package ontologyscope

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"orrery_v2/internal/platform/db"
)

const MaxBindingBackfillBatch = 500

var bindingBackfillSources = []string{
	"context_graph_objects",
	"context_graph_links",
	"context_graph_facts",
	"context_graph_fact_assertions",
	"context_graph_evidence",
	"context_graph_projection_audits",
	"context_graph_projection_audit_replay_history",
	"ontology_object_types",
	"ontology_link_types",
	"ontology_property_types",
	"ontology_mapping_rules",
	"ontology_metadata_proposals",
	"ontology_model_runs",
}

type BindingBackfillCursor struct {
	Source   string `json:"source,omitempty"`
	TenantID string `json:"tenant_id,omitempty"`
}

type BindingBackfillOptions struct {
	Apply     bool
	Limit     int
	After     BindingBackfillCursor
	ChangedBy string
}

type BindingBackfillReport struct {
	Apply               bool                  `json:"apply"`
	Scanned             int                   `json:"scanned"`
	Resolved            int                   `json:"resolved"`
	Applied             int                   `json:"applied"`
	Unresolved          int                   `json:"unresolved"`
	UnresolvedTenantIDs []string              `json:"unresolved_tenant_ids,omitempty"`
	HasMore             bool                  `json:"has_more"`
	Next                BindingBackfillCursor `json:"next,omitempty"`
}

func BackfillBindings(ctx context.Context, database *db.Store, resolver BindingResolver, store BindingStore, opts BindingBackfillOptions) (BindingBackfillReport, error) {
	if ctx == nil || database == nil || resolver == nil || store == nil {
		return BindingBackfillReport{}, ErrDependencyUnavailable
	}
	limit := opts.Limit
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > MaxBindingBackfillBatch {
		return BindingBackfillReport{}, fmt.Errorf("%w: binding backfill limit must be between 1 and %d", ErrInvalidBinding, MaxBindingBackfillBatch)
	}
	opts.ChangedBy = strings.TrimSpace(opts.ChangedBy)
	if opts.Apply && opts.ChangedBy == "" {
		return BindingBackfillReport{}, fmt.Errorf("%w: changed_by is required in apply mode", ErrInvalidBinding)
	}
	start, err := bindingBackfillStartIndex(opts.After.Source)
	if err != nil {
		return BindingBackfillReport{}, err
	}
	if (strings.TrimSpace(opts.After.Source) == "") != (strings.TrimSpace(opts.After.TenantID) == "") {
		return BindingBackfillReport{}, fmt.Errorf("%w: binding backfill cursor is incomplete", ErrInvalidBinding)
	}
	report := BindingBackfillReport{Apply: opts.Apply}
	for sourceIndex := start; sourceIndex < len(bindingBackfillSources); sourceIndex++ {
		if err := ctx.Err(); err != nil {
			return BindingBackfillReport{}, err
		}
		source := bindingBackfillSources[sourceIndex]
		exists, err := database.TableExists(ctx, source)
		if err != nil {
			return BindingBackfillReport{}, err
		}
		if !exists {
			continue
		}
		afterTenant := ""
		if sourceIndex == start {
			afterTenant = strings.TrimSpace(opts.After.TenantID)
		}
		remaining := limit - report.Scanned
		candidates, moreInSource, err := bindingTenantCandidates(ctx, database, source, afterTenant, remaining)
		if err != nil {
			return BindingBackfillReport{}, err
		}
		for _, tenantID := range candidates {
			report.Scanned++
			report.Next = BindingBackfillCursor{Source: source, TenantID: tenantID}
			binding, resolveErr := resolver.Resolve(ctx, tenantID)
			if resolveErr != nil {
				if errors.Is(resolveErr, ErrBindingNotFound) || errors.Is(resolveErr, ErrBindingConflict) || errors.Is(resolveErr, ErrInvalidBinding) {
					report.Unresolved++
					report.UnresolvedTenantIDs = append(report.UnresolvedTenantIDs, tenantID)
					continue
				}
				return BindingBackfillReport{}, resolveErr
			}
			report.Resolved++
			if opts.Apply && binding.TenantID != "system" {
				changed, saveErr := persistBackfilledBinding(ctx, store, binding, opts.ChangedBy)
				if saveErr != nil {
					return BindingBackfillReport{}, saveErr
				}
				if changed {
					report.Applied++
				}
			}
		}
		if report.Scanned == limit {
			report.HasMore = moreInSource
			if !report.HasMore {
				report.HasMore, err = bindingBackfillHasCandidate(ctx, database, sourceIndex+1)
				if err != nil {
					return BindingBackfillReport{}, err
				}
			}
			if !report.HasMore {
				report.Next = BindingBackfillCursor{}
			}
			return report, nil
		}
	}
	report.Next = BindingBackfillCursor{}
	return report, nil
}

func bindingBackfillHasCandidate(ctx context.Context, database *db.Store, start int) (bool, error) {
	for sourceIndex := start; sourceIndex < len(bindingBackfillSources); sourceIndex++ {
		table := bindingBackfillSources[sourceIndex]
		exists, err := database.TableExists(ctx, table)
		if err != nil {
			return false, err
		}
		if !exists {
			continue
		}
		candidates, _, err := bindingTenantCandidates(ctx, database, table, "", 1)
		if err != nil {
			return false, err
		}
		if len(candidates) > 0 {
			return true, nil
		}
	}
	return false, nil
}

func bindingBackfillStartIndex(source string) (int, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return 0, nil
	}
	for index, candidate := range bindingBackfillSources {
		if source == candidate {
			return index, nil
		}
	}
	return 0, fmt.Errorf("%w: unknown binding backfill source", ErrInvalidBinding)
}

func bindingTenantCandidates(ctx context.Context, database *db.Store, table, afterTenant string, limit int) ([]string, bool, error) {
	rows, err := database.Query(ctx, `SELECT DISTINCT tenant_id FROM `+table+` WHERE tenant_id > ? ORDER BY tenant_id ASC LIMIT ?`, afterTenant, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	values := make([]string, 0, limit+1)
	for rows.Next() {
		var tenantID string
		if err := rows.Scan(&tenantID); err != nil {
			return nil, false, err
		}
		tenantID = strings.TrimSpace(tenantID)
		if tenantID != "" {
			values = append(values, tenantID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(values) > limit
	if more {
		values = values[:limit]
	}
	return values, more, nil
}

func persistBackfilledBinding(ctx context.Context, store BindingStore, binding ScopeBinding, changedBy string) (bool, error) {
	current, err := store.GetBinding(ctx, binding.TenantID)
	expectedVersion := int64(0)
	if err == nil {
		expectedVersion = current.BindingVersion
	} else if !errors.Is(err, ErrBindingNotFound) {
		return false, err
	}
	saved, err := store.SaveBinding(ctx, binding, expectedVersion, changedBy)
	if err != nil {
		return false, err
	}
	return expectedVersion == 0 || saved.BindingVersion != expectedVersion, nil
}
