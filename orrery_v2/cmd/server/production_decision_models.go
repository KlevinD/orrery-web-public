package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"orrery_v2/internal/app/resourceadmin"
	"orrery_v2/internal/app/resourceadmin/rsillm"
	"orrery_v2/internal/decision"
	"orrery_v2/internal/decision/laya"
	decisionllm "orrery_v2/internal/decision/llm"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/logging"
)

func newProductionDecisionShadow(ctx context.Context, rt *platform.Runtime) (*decision.Observer, func(), error) {
	rsiMode := strings.TrimSpace(os.Getenv("ORRERY_RSI_MODE"))
	if rsiMode == "" {
		rsiMode = "laya"
	}
	if rsiMode != "laya" && rsiMode != "llm" {
		return nil, func() {}, fmt.Errorf("RSI mode must be laya or llm")
	}
	mode, explicit := os.LookupEnv("ORRERY_DECISION_MODE")
	if !explicit {
		mode = os.Getenv("ORRERY_LAYA_MODE")
	}
	mode = strings.TrimSpace(mode)
	if mode == "" || mode == "off" {
		if rsiMode == "llm" {
			return nil, func() {}, fmt.Errorf("RSI LLM mode requires the decision observer")
		}
		return nil, func() {}, nil
	}
	if mode != "shadow" {
		return nil, func() {}, fmt.Errorf("decision mode must be off or shadow")
	}
	if rt == nil {
		return nil, func() {}, decision.ErrConfiguration
	}
	path := strings.TrimSpace(os.Getenv("ORRERY_DECISION_PROFILES_FILE"))
	config := decision.ConfigProvider(func(context.Context) (decision.Config, error) {
		return decision.Config{DefaultProfileID: "decision-laya", Profiles: []decision.Profile{{ProfileID: "decision-laya", Adapter: "laya", Enabled: true, TimeoutMS: 5000}}}, nil
	})
	if path != "" {
		config = decision.FileConfig(path)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	client := &http.Client{Transport: transport, Timeout: 60 * time.Second}
	settings := productionSettingsStore{router: rt.DBRouter, config: rt.Config}
	llm := decisionllm.Adapter{Profiles: settings, Secrets: platformSecretResolver{provider: rt.Secrets}, Client: client}
	factories := map[string]decision.Factory{
		"llm": llm.Resolve,
		"laya": func(ctx context.Context, p decision.Profile) (decision.Resolved, error) {
			key, err := laya.ReadKeyFile(os.Getenv("ORRERY_LAYA_API_KEY_FILE"))
			if err != nil {
				return decision.Resolved{}, decision.ErrConfiguration
			}
			endpoint := strings.TrimSpace(os.Getenv("ORRERY_LAYA_URL"))
			if endpoint == "" {
				endpoint = "http://127.0.0.1:18490"
			}
			// A short-lived adapter shares no credentials or cache with other profiles.
			judge, err := laya.NewClient(endpoint, key, 5*time.Second)
			if err != nil {
				return decision.Resolved{}, err
			}
			return decision.Resolved{Judge: closingDecisionJudge{judge}, Timeout: 5 * time.Second, Metadata: decision.Metadata{Provider: "laya", Model: "multilingual", ModelRevision: laya.ModelRevision, ConfidenceKind: "model", CacheKey: decision.Hash(endpoint + "\x00" + key)}}, nil
		},
	}
	router := decision.NewRouter(config, factories)
	if err := router.Validate(ctx); err != nil {
		transport.CloseIdleConnections()
		return nil, func() {}, err
	}
	samples, err := productionRSISampleStore(rt)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, func() {}, err
	}
	var fallback *resourceadmin.RSIFallbackWorker
	fallbackMode := strings.TrimSpace(os.Getenv("ORRERY_RSI_LLM_FALLBACK"))
	if fallbackMode != "" && fallbackMode != "on" && fallbackMode != "off" {
		transport.CloseIdleConnections()
		return nil, func() {}, fmt.Errorf("RSI LLM fallback must be on or off")
	}
	maxCalls := 20
	if value := strings.TrimSpace(os.Getenv("ORRERY_RSI_LLM_FALLBACK_MAX_CALLS_PER_HOUR")); value != "" {
		maxCalls, err = strconv.Atoi(value)
		if err != nil || maxCalls < 1 || maxCalls > 100 {
			transport.CloseIdleConnections()
			return nil, func() {}, fmt.Errorf("RSI LLM fallback hourly limit must be 1..100")
		}
	}
	if rsiMode == "llm" || fallbackMode != "off" {
		fallback = resourceadmin.NewRSIFallbackWorker(ctx, samples, rsillm.Diagnoser{Adapter: llm, ProfileID: strings.TrimSpace(os.Getenv("ORRERY_RSI_LLM_FALLBACK_PROFILE"))}, maxCalls, func(id string) {
			if rt.Logger != nil {
				rt.Logger.Warn(context.Background(), "decision.rsi_fallback_failed", logging.String("observation_id", id))
			}
		})
	}
	sink := func(record decision.Record) {
		if record.Kind == "rsi_sample" {
			if rsiMode == "llm" {
				record.SelectionSource = "rsi_llm_primary"
			}
			writeCtx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
			err := samples.SaveAssessment(writeCtx, record)
			cancel()
			if err != nil && rt.Logger != nil {
				rt.Logger.Warn(context.Background(), "decision.rsi_persistence_failed", logging.String("observation_id", record.ObservationID), logging.String("stage", "assessment"))
			}
			if err == nil && fallback != nil && record.Adapter != "llm" && !fallback.Submit(record) && rt.Logger != nil {
				rt.Logger.Warn(context.Background(), "decision.rsi_fallback_not_queued", logging.String("observation_id", record.ObservationID))
			}
		}
		if rt.Logger != nil {
			rt.Logger.Info(context.Background(), "decision.shadow", logging.Any("assessment", record))
		}
	}
	var handlers map[string]func(decision.Job)
	if rsiMode == "llm" {
		handlers = map[string]func(decision.Job){"rsi_sample": func(job decision.Job) {
			// Preserve provenance without pretending a classifier has run. The
			// durable worker rereads the complete immutable evidence from storage.
			sink(decision.Record{ObservationID: job.ObservationID, InputTruncated: job.InputTruncated,
				At: time.Now().UTC(), Kind: job.Kind, ScopeHash: decision.Hash(job.Scope),
				Metadata:      decision.Metadata{Adapter: "unresolved", SelectionSource: "rsi_llm_primary"},
				ModelRevision: "unresolved", PromptVersion: job.PromptVersion,
				Sources: job.Sources, InputHashes: job.InputHashes, Fallback: "llm_primary", Mode: "shadow"})
		}}
	}
	observer := decision.NewObserverWithHandlers(ctx, router, sink, handlers)
	if rt.Logger != nil {
		rt.Logger.Info(ctx, "decision.rsi.enabled", logging.String("mode", rsiMode), logging.Any("max_calls_per_hour", maxCalls))
		rt.Logger.Info(ctx, "decision.shadow.enabled", logging.String("mode", mode), logging.Any("profiles_file_configured", path != ""))
	}
	return observer, func() { observer.Close(); fallback.Close(); transport.CloseIdleConnections() }, nil
}

type closingDecisionJudge struct{ *laya.Client }

func (c closingDecisionJudge) Choose(ctx context.Context, states []string, q decision.Question) ([]decision.Decision, error) {
	defer c.Close()
	return c.Client.Choose(ctx, states, q)
}
