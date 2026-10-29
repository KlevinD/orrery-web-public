package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/eventstore"
	"orrery_v2/internal/platform/graphprotocol"
	toolres "orrery_v2/internal/resource/tool"
)

type productionAccessRefBackfillResolver struct {
	events *eventstore.Store
	tools  *toolres.PlatformRegistry
}

func (r productionAccessRefBackfillResolver) ResolveAccessRef(ctx context.Context, candidate contextgraph.AccessRefBackfillCandidate) (contextgraph.AccessRef, bool, error) {
	if r.events != nil && strings.TrimSpace(candidate.SourceID) != "" {
		envelope, err := r.events.GetByID(ctx, candidate.SourceID)
		if err == nil {
			if !strings.HasPrefix(envelope.EventType, ontologyEventTypePrefix) {
				return contextgraph.AccessRef{}, false, nil
			}
			event, err := ontologyEventFromEnvelope(envelope)
			if err != nil {
				return contextgraph.AccessRef{}, false, err
			}
			if event.ID != candidate.SourceID || event.TenantID != candidate.TenantID || !candidateMatchesOntologyEvent(candidate, event) {
				return contextgraph.AccessRef{}, false, nil
			}
			ref := contextGraphAccessRef(event.SourceAccessRef)
			return ref, ref.Resolved(), nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return contextgraph.AccessRef{}, false, err
		}
	}
	if r.tools != nil && candidate.TenantID == "system" && candidate.SourceKind == "resource.mcp" && strings.TrimSpace(candidate.SourceID) != "" {
		definition, err := r.tools.Get(ctx, agentcore.ToolID(candidate.SourceID))
		if err == nil && definition.Kind == toolres.KindMCPServer {
			return systemResourceOntologySourceRef(string(definition.ID)), true, nil
		}
		if err != nil && !errors.Is(err, toolres.ErrNotFound) {
			return contextgraph.AccessRef{}, false, err
		}
	}
	return contextgraph.AccessRef{}, false, nil
}

func candidateMatchesOntologyEvent(candidate contextgraph.AccessRefBackfillCandidate, event graphprotocol.DomainEvent) bool {
	sourceKind := strings.TrimSpace(candidate.SourceKind)
	switch candidate.ResourceKind {
	case contextgraph.AccessRefResourceObject,
		contextgraph.AccessRefResourceLink,
		contextgraph.AccessRefResourceFact,
		contextgraph.AccessRefResourceFactAssertion:
		return sourceKind == "event"
	case contextgraph.AccessRefResourceEvidence,
		contextgraph.AccessRefResourceAudit,
		contextgraph.AccessRefResourceReplayHistory:
		return sourceKind == "event" || sourceKind == event.Type
	default:
		return false
	}
}

type productionAccessRefBackfillOptions struct {
	Apply     bool
	Limit     int
	After     contextgraph.AccessRefBackfillCursor
	ChangedBy string
}

func runProductionAccessRefBackfill(ctx context.Context, cfg config.Config, opts productionAccessRefBackfillOptions, out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	runtime, err := platform.OpenWithModules(ctx, cfg, productionModules()...)
	if err != nil {
		return err
	}
	defer runtime.Close()
	if err := runtime.Initialize(ctx); err != nil {
		return err
	}
	core, err := runtime.Databases.Store(db.RoleCore)
	if err != nil {
		return err
	}
	runID := ""
	if opts.Apply {
		runID, err = newAccessRefBackfillRunID()
		if err != nil {
			return err
		}
	}
	report, err := contextgraph.BackfillAccessRefs(ctx, core, productionAccessRefBackfillResolver{
		events: eventstore.New(core),
		tools:  toolres.NewPlatformRegistry(core),
	}, contextgraph.AccessRefBackfillOptions{
		Apply:     opts.Apply,
		Limit:     opts.Limit,
		After:     opts.After,
		RunID:     runID,
		ChangedBy: opts.ChangedBy,
	})
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(report)
}

func newAccessRefBackfillRunID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate access ref backfill run id: %w", err)
	}
	return "ontology-access-ref-backfill:" + hex.EncodeToString(value[:]), nil
}
