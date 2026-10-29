package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"orrery_v2/internal/app/resourceadmin"
	"orrery_v2/internal/decision/laya"
	"orrery_v2/internal/platform/db"
)

type rsiSampleSummary struct {
	ID          string                                `json:"id"`
	At          time.Time                             `json:"at"`
	EvidenceRef string                                `json:"evidence_ref"`
	Tool        string                                `json:"tool"`
	Status      string                                `json:"status"`
	Assessment  *laya.Record                          `json:"assessment,omitempty"`
	Observation *resourceadmin.RSILearningObservation `json:"observation,omitempty"`
}

func summarizeRSISample(observation resourceadmin.RSILearningObservation, includeEvidence bool) rsiSampleSummary {
	result := rsiSampleSummary{ID: observation.ID, At: observation.At, EvidenceRef: observation.Sample.EvidenceRef, Tool: observation.Sample.Tool, Status: "pending", Assessment: observation.Assessment}
	if observation.Assessment != nil {
		result.Status = "assessed"
		if observation.Assessment.Fallback != "" {
			result.Status = "fallback"
		}
	}
	if includeEvidence {
		result.Observation = &observation
	}
	return result
}

// This is a host administration command, not a public sample API. Reading
// requires database access and explicit tenant/agent scope. It cannot execute
// tools, generate candidates, or promote a skill.
func runRSISamples(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("rsi-samples", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dbURL := flags.String("db", os.Getenv("ORRERY_RSI_DB_URL"), "core database URL (or ORRERY_RSI_DB_URL)")
	tenant := flags.String("tenant", "", "required tenant id")
	agent := flags.String("agent", "", "required agent id")
	id := flags.String("id", "", "read one observation")
	after := flags.String("after", "", "page cursor")
	limit := flags.Int("limit", 20, "page size, 1-100")
	include := flags.Bool("include-evidence", false, "include complete private observation evidence")
	retry := flags.Bool("retry", false, "classify the selected observation and save the latest attempt")
	keyFile := flags.String("key-file", "", "local Laya credential file, required with retry")
	endpoint := flags.String("url", "http://127.0.0.1:18490", "local Laya endpoint")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *dbURL == "" || *limit < 1 || *limit > 100 || (*retry && (*id == "" || *keyFile == "")) {
		return fmt.Errorf("database and valid limit are required; retry requires id and key-file")
	}
	scope, err := resourceadmin.RSILearningScope(*tenant, *agent)
	if err != nil {
		return err
	}
	registry, err := db.OpenRegistryFromURLs(ctx, map[string]string{db.RoleCore: *dbURL})
	if err != nil {
		return fmt.Errorf("open RSI sample database failed")
	}
	defer registry.Close()
	core, err := registry.Store(db.RoleCore)
	if err != nil {
		return err
	}
	samples := resourceadmin.NewPlatformRSILearningSampleStore(core)
	encoder := json.NewEncoder(out)
	if *id != "" {
		observation, err := samples.Get(ctx, scope, *id)
		if err != nil {
			return err
		}
		if *retry {
			key, err := laya.ReadKeyFile(*keyFile)
			if err != nil {
				return err
			}
			client, err := laya.NewClient(*endpoint, key, 5*time.Second)
			if err != nil {
				return err
			}
			defer client.Close()
			records := make(chan laya.Record, 1)
			observer := laya.NewObserver(ctx, client, func(record laya.Record) { records <- record })
			defer observer.Close()
			if !observer.Submit(ctx, resourceadmin.BuildLayaRSISampleJob(observation.Sample)) {
				return fmt.Errorf("RSI sample could not be submitted")
			}
			select {
			case record := <-records:
				if err := samples.SaveAssessment(ctx, record); err != nil {
					return err
				}
				observation.Assessment = &record
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(7 * time.Second):
				return fmt.Errorf("RSI classification timed out")
			}
		}
		if err := encoder.Encode(summarizeRSISample(observation, *include)); err != nil {
			return err
		}
		if *retry && observation.Assessment.Fallback != "" {
			return fmt.Errorf("RSI classification failed: %s", observation.Assessment.Fallback)
		}
		return nil
	}
	page, err := samples.List(ctx, scope, *after, *limit)
	if err != nil {
		return err
	}
	items := make([]rsiSampleSummary, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, summarizeRSISample(item, *include))
	}
	return encoder.Encode(struct {
		Items      []rsiSampleSummary `json:"items"`
		NextCursor string             `json:"next_cursor,omitempty"`
	}{Items: items, NextCursor: page.NextCursor})
}
