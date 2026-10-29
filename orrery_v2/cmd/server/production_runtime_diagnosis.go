package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"orrery_v2/internal/app/improvement/einomodel"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/execution"
)

func registerProductionRuntimeDiagnosisSweeper(rt *platform.Runtime, sink *productionAgentExecutionOntologySink) error {
	if strings.TrimSpace(os.Getenv("ORRERY_RSI_MODE")) != "llm" {
		return nil
	}
	mode := strings.TrimSpace(os.Getenv("ORRERY_RSI_CASE_DIAGNOSIS"))
	if mode == "off" {
		return nil
	}
	if mode != "" && mode != "on" {
		return fmt.Errorf("RSI case diagnosis must be on or off")
	}
	limit := 4
	if value := strings.TrimSpace(os.Getenv("ORRERY_RSI_CASE_DIAGNOSIS_MAX_CALLS_PER_HOUR")); value != "" {
		var err error
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 100 {
			return fmt.Errorf("RSI case diagnosis hourly limit must be 1..100")
		}
	}
	if rt == nil || sink == nil || sink.improvements == nil {
		return fmt.Errorf("RSI case diagnosis store is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_, err := sink.improvements.EnableRuntimeDiagnoses(ctx)
	cancel()
	if err != nil {
		return err
	}
	model := &einomodel.RuntimeDiagnoser{Profiles: productionSettingsStore{router: rt.DBRouter, config: rt.Config}, Secrets: platformSecretResolver{provider: rt.Secrets}, ProfileID: strings.TrimSpace(os.Getenv("ORRERY_RSI_CASE_DIAGNOSIS_PROFILE")), Client: &http.Client{Timeout: 45 * time.Second}}
	worker := platform.SweeperFunc(func(ctx context.Context) error {
		_, err := sink.improvements.DiagnoseNextRuntime(ctx, model, limit)
		return err
	})
	return rt.RegisterSweeper("ontology.runtime_case_diagnosis", execution.NewBackgroundSweeper("ontology.runtime_case_diagnosis", worker, time.Minute, rt.Logger))
}
