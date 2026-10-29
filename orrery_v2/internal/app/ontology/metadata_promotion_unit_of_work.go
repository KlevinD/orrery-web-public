package ontology

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"orrery_v2/internal/platform/db"
)

type MetadataPromotionProposalStore interface {
	GetMetadataProposalForTenant(context.Context, string, string) (OntologyMetadataProposal, error)
	DecideMetadataProposalForTenant(context.Context, string, MetadataProposalDecision) (OntologyMetadataProposal, error)
}

type MetadataPromotionTransactionStores struct {
	Plans             MetadataPromotionPlanStore
	Proposals         MetadataPromotionProposalStore
	Registry          Registry
	Receipts          MetadataProposalPromotionReceiptStore
	Refreshes         MappingProjectionRefreshStore
	SecurityRevisions MetadataPromotionSecurityRevisionFencer
	ScopeBindings     MetadataPromotionScopeBindingFencer
}

type MetadataPromotionCommitGuard func(context.Context) error

type MetadataPromotionSecurityRevisions struct {
	Authorization string
	Policy        string
}

type MetadataPromotionSecurityRevisionFencer interface {
	FenceSecurityRevisions(context.Context, MetadataPromotionSecurityRevisions) error
}

type MetadataPromotionScopeBinding struct {
	TenantID                string
	BindingVersion          int64
	PersistedBindingVersion int64
	PersistenceRequired     bool
}

type MetadataPromotionScopeBindingFencer interface {
	FenceScopeBinding(context.Context, MetadataPromotionScopeBinding) error
}

type MetadataPromotionUnitOfWork interface {
	WithinMetadataPromotionTransaction(context.Context, MetadataPromotionCommitGuard, func(MetadataPromotionTransactionStores) error) error
}

type PlatformMetadataPromotionUnitOfWork struct {
	store *db.Store
}

func NewPlatformMetadataPromotionUnitOfWork(store *db.Store) *PlatformMetadataPromotionUnitOfWork {
	return &PlatformMetadataPromotionUnitOfWork{store: store}
}

func (u *PlatformMetadataPromotionUnitOfWork) WithinMetadataPromotionTransaction(ctx context.Context, guard MetadataPromotionCommitGuard, fn func(MetadataPromotionTransactionStores) error) error {
	if u == nil || u.store == nil || fn == nil {
		return fmt.Errorf("%w: metadata promotion transaction dependencies are required", ErrInvalidDefinition)
	}
	if guard != nil {
		if err := guard(ctx); err != nil {
			return err
		}
	}
	return u.store.WithTx(ctx, func(tx db.Tx) error {
		return fn(MetadataPromotionTransactionStores{
			Plans:             NewPlatformMetadataPromotionPlanStoreTx(tx),
			Proposals:         NewPlatformMetadataProposalStoreTx(tx),
			Registry:          NewPlatformRegistryTx(tx),
			Receipts:          NewPlatformMetadataProposalPromotionReceiptStoreTx(tx),
			Refreshes:         NewPlatformMappingProjectionRefreshStoreTx(tx),
			SecurityRevisions: metadataPromotionSecurityRevisionTx{tx: tx},
			ScopeBindings:     metadataPromotionScopeBindingTx{tx: tx},
		})
	})
}

type metadataPromotionSecurityRevisionTx struct {
	tx db.Tx
}

// FenceMutationAuthorization shares the transaction fences used by definition
// promotion with governed instance commands.
func FenceMutationAuthorization(ctx context.Context, tx db.Tx, revisions MetadataPromotionSecurityRevisions, binding MetadataPromotionScopeBinding) error {
	if err := (metadataPromotionSecurityRevisionTx{tx: tx}).FenceSecurityRevisions(ctx, revisions); err != nil {
		return err
	}
	return (metadataPromotionScopeBindingTx{tx: tx}).FenceScopeBinding(ctx, binding)
}

func (r metadataPromotionSecurityRevisionTx) FenceSecurityRevisions(ctx context.Context, expected MetadataPromotionSecurityRevisions) error {
	if r.tx == nil {
		return fmt.Errorf("%w: metadata promotion security revision fencer is required", ErrInvalidDefinition)
	}
	authorizationRevision, err := parseMetadataPromotionRevision(expected.Authorization, "authorization:")
	if err != nil {
		return err
	}
	policyRevision, err := parseMetadataPromotionRevision(expected.Policy, "policy:")
	if err != nil {
		return err
	}
	if err := fenceMetadataPromotionRevision(ctx, r.tx, "rebac_authorization_revision", authorizationRevision); err != nil {
		return fmt.Errorf("fence metadata promotion authorization revision: %w", err)
	}
	if err := fenceMetadataPromotionRevision(ctx, r.tx, "data_policy_revision", policyRevision); err != nil {
		return fmt.Errorf("fence metadata promotion policy revision: %w", err)
	}
	return nil
}

type metadataPromotionScopeBindingTx struct {
	tx db.Tx
}

func (r metadataPromotionScopeBindingTx) FenceScopeBinding(ctx context.Context, expected MetadataPromotionScopeBinding) error {
	if r.tx == nil {
		return fmt.Errorf("%w: metadata promotion scope binding fencer is required", ErrInvalidDefinition)
	}
	expected.TenantID = strings.TrimSpace(expected.TenantID)
	if expected.TenantID == "" || len(expected.TenantID) > 256 || expected.BindingVersion <= 0 || expected.PersistedBindingVersion < 0 || expected.PersistedBindingVersion > expected.BindingVersion {
		return fmt.Errorf("%w: metadata promotion scope binding fence is invalid", ErrInvalidDefinition)
	}
	if expected.PersistedBindingVersion == 0 {
		if expected.PersistenceRequired {
			return fmt.Errorf("%w: persisted scope binding is unavailable", ErrVersionConflict)
		}
		return nil
	}
	result, err := r.tx.ExecContext(ctx, `UPDATE ontology_scope_bindings
SET binding_version = binding_version
WHERE tenant_id = ? AND binding_version = ? AND state = 'active'`, expected.TenantID, expected.PersistedBindingVersion)
	if err != nil {
		return fmt.Errorf("fence metadata promotion scope binding: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("%w: metadata promotion scope binding changed", ErrVersionConflict)
	}
	return nil
}

func parseMetadataPromotionRevision(value, prefix string) (int64, error) {
	raw := strings.TrimSpace(value)
	if !strings.HasPrefix(raw, prefix) {
		return 0, fmt.Errorf("%w: metadata promotion security revision is invalid", ErrInvalidDefinition)
	}
	revision, err := strconv.ParseInt(strings.TrimPrefix(raw, prefix), 10, 64)
	if err != nil || revision < 0 {
		return 0, fmt.Errorf("%w: metadata promotion security revision is invalid", ErrInvalidDefinition)
	}
	return revision, nil
}

func fenceMetadataPromotionRevision(ctx context.Context, tx db.Tx, table string, revision int64) error {
	if table != "rebac_authorization_revision" && table != "data_policy_revision" {
		return fmt.Errorf("%w: metadata promotion security revision table is invalid", ErrInvalidDefinition)
	}
	result, err := tx.ExecContext(ctx, `UPDATE `+table+` SET revision = revision WHERE id = 'global' AND revision = ?`, revision)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("%w: metadata promotion security revision changed", ErrVersionConflict)
	}
	return nil
}
