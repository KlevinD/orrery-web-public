package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"orrery_v2/internal/app/improvement"
	"orrery_v2/internal/platform/db"
)

// Host-only, read-only inspection. No evaluation flags, resource apply calls,
// lease claims or state transitions can be submitted through this command.
func runImprovements(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("improvements", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dbURL := flags.String("db", os.Getenv("ORRERY_RSI_DB_URL"), "core database URL or ORRERY_RSI_DB_URL")
	tenant := flags.String("tenant", "", "required ontology tenant scope")
	id := flags.String("id", "", "read one improvement case")
	attempt := flags.String("attempt", "", "inspect captured runtime attempt evidence")
	after := flags.String("after", "", "page cursor")
	limit := flags.Int("limit", 20, "page size, 1-100")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *dbURL == "" || *tenant == "" || *limit < 1 || *limit > 100 || (*id != "" && *attempt != "") {
		return fmt.Errorf("database, explicit tenant and valid limit are required")
	}
	registry, err := db.OpenRegistryFromURLs(ctx, map[string]string{db.RoleCore: *dbURL})
	if err != nil {
		return fmt.Errorf("open improvement database failed")
	}
	defer registry.Close()
	core, err := registry.Store(db.RoleCore)
	if err != nil {
		return err
	}
	store := improvement.NewStore(core)
	encoder := json.NewEncoder(out)
	if *attempt != "" {
		evidence := improvement.NewRuntimeEvidenceStore(core)
		request, err := evidence.GetRequest(ctx, *tenant, *attempt)
		if err != nil {
			return err
		}
		terminal, err := evidence.GetTerminal(ctx, *tenant, *attempt)
		var end *improvement.RuntimeTerminalEvidence
		if err == nil {
			end = &terminal
		} else if !errors.Is(err, improvement.ErrNotFound) {
			return err
		}
		review, persisted, err := evidence.GetReview(ctx, *tenant, *attempt)
		if err != nil {
			return err
		}
		var execution *improvement.RuntimeExecutionEvidence
		actual, err := evidence.GetExecution(ctx, *tenant, *attempt)
		if err == nil {
			execution = &actual
		} else if !errors.Is(err, improvement.ErrNotFound) {
			return err
		}
		executionID := ""
		if execution != nil {
			executionID = execution.ID
		}
		terminalID := ""
		if end != nil {
			terminalID = end.ID
		}
		// Completion can commit between the independent read-only lookups.
		// Refuse a mixed snapshot rather than print contradictory task states.
		if review.RequestID != request.ID || review.TerminalID != terminalID || review.ExecutionID != executionID {
			return fmt.Errorf("%w: runtime feedback changed during inspection; retry", improvement.ErrConflict)
		}
		var binding *improvement.BusinessBinding
		var business []improvement.BusinessAssessment
		bound, err := evidence.GetBusinessBinding(ctx, *tenant, *attempt)
		if err == nil {
			binding = &bound
			business, err = evidence.BusinessAssessments(ctx, *tenant, *attempt)
		}
		if err != nil && !errors.Is(err, improvement.ErrNotFound) {
			return err
		}
		for _, assessment := range business {
			if end == nil || assessment.RuntimeReviewID != review.ID {
				return fmt.Errorf("%w: business evidence changed during inspection; retry", improvement.ErrConflict)
			}
		}
		artifacts, err := evidence.TaskArtifactVerifications(ctx, *tenant, *attempt)
		if err != nil {
			return err
		}
		for _, receipt := range artifacts {
			if receipt.RuntimeReviewID != review.ID {
				return fmt.Errorf("%w: artifact evidence changed during inspection; retry", improvement.ErrConflict)
			}
		}
		var admission *improvement.RuntimeExperimentAdmission
		admissionPersisted := false
		assessed, saved, err := store.RuntimeExperimentAdmission(ctx, *tenant, *attempt)
		if err == nil {
			admission, admissionPersisted = &assessed, saved
		} else if !errors.Is(err, improvement.ErrNotFound) && !errors.Is(err, improvement.ErrUnavailable) {
			return err
		}
		return encoder.Encode(struct {
			Request               improvement.RuntimeRequestEvidence      `json:"request"`
			Terminal              *improvement.RuntimeTerminalEvidence    `json:"terminal"`
			Review                improvement.RuntimeReview               `json:"review"`
			ReviewPersisted       bool                                    `json:"review_persisted"`
			Execution             *improvement.RuntimeExecutionEvidence   `json:"execution,omitempty"`
			BusinessBinding       *improvement.BusinessBinding            `json:"business_binding,omitempty"`
			BusinessAssessments   []improvement.BusinessAssessment        `json:"business_assessments,omitempty"`
			ArtifactVerifications []improvement.TaskArtifactVerification  `json:"artifact_verifications,omitempty"`
			ExperimentAdmission   *improvement.RuntimeExperimentAdmission `json:"experiment_admission,omitempty"`
			AdmissionPersisted    bool                                    `json:"experiment_admission_persisted"`
		}{request, end, review, persisted, execution, binding, business, artifacts, admission, admissionPersisted})
	}
	if *id != "" {
		c, err := store.Get(ctx, *tenant, *id)
		if err != nil {
			return err
		}
		c.Lease = nil // Fencing tokens are never included in operator read output.
		return encoder.Encode(c)
	}
	page, err := store.List(ctx, *tenant, *after, *limit)
	if err != nil {
		return err
	}
	for i := range page.Items {
		page.Items[i].Lease = nil
	}
	return encoder.Encode(page)
}
