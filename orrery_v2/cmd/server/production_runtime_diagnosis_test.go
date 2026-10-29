package main

import (
	"context"
	"testing"

	"orrery_v2/internal/app/improvement"
	"orrery_v2/internal/platform"
)

func TestProductionRuntimeDiagnosisRegistration(t *testing.T) {
	t.Setenv("ORRERY_RSI_MODE", "llm")
	t.Setenv("ORRERY_RSI_CASE_DIAGNOSIS", "off")
	if err := registerProductionRuntimeDiagnosisSweeper(nil, nil); err != nil {
		t.Fatal("off still required a worker", err)
	}
	t.Setenv("ORRERY_RSI_CASE_DIAGNOSIS", "invalid")
	if err := registerProductionRuntimeDiagnosisSweeper(nil, nil); err == nil {
		t.Fatal("invalid activation accepted")
	}
	t.Setenv("ORRERY_RSI_CASE_DIAGNOSIS", "")
	t.Setenv("ORRERY_RSI_CASE_DIAGNOSIS_MAX_CALLS_PER_HOUR", "0")
	if err := registerProductionRuntimeDiagnosisSweeper(nil, nil); err == nil {
		t.Fatal("invalid paid-call budget accepted")
	}
	t.Setenv("ORRERY_RSI_CASE_DIAGNOSIS_MAX_CALLS_PER_HOUR", "4")
	core := newOntologyEventLogTestCore(t, "runtime-diagnosis-registration")
	rt := &platform.Runtime{}
	defer rt.Close()
	sink := &productionAgentExecutionOntologySink{improvements: improvement.NewStore(core)}
	if err := registerProductionRuntimeDiagnosisSweeper(rt, sink); err != nil {
		t.Fatal(err)
	}
	if _, exists, err := core.GetRecord(context.Background(), "ontology_improvement_runtime_diagnosis_control", improvement.RuntimeDiagnosisProtocol); err != nil || !exists {
		t.Fatal("activation not persisted", err)
	}
	if err := rt.RegisterSweeper("ontology.runtime_case_diagnosis", platform.SweeperFunc(func(context.Context) error { return nil })); err == nil {
		t.Fatal("diagnosis worker was not registered in platform lifecycle")
	}
}
