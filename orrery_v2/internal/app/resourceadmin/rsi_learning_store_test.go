package resourceadmin

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/decision/laya"
	"orrery_v2/internal/platform/db"
)

func rsiStoreSample(t *testing.T, tenant, agent, ref string) RSILearningSample {
	t.Helper()
	scope, err := RSILearningScope(tenant, agent)
	if err != nil {
		t.Fatal(err)
	}
	return RSILearningSample{Scope: scope, EvidenceRef: ref, Tool: "query_order", ErrorCode: "invalid_arguments", Summary: strings.Repeat("错误信息", 90) + "完整结尾", SkillRefs: []string{`{"skill_id":"order","version":"1","digest":"original"}`}, Evidence: json.RawMessage(`{"input":{"customer_id":"U123"},"output":{"status":"failed","nested":{"full":"DO_NOT_SEND_TO_MODEL"}}}`)}
}

func rsiAssessment(observation RSILearningObservation, at time.Time, choice, fallback string) laya.Record {
	job := BuildLayaRSISampleJob(observation.Sample)
	record := laya.Record{ObservationID: observation.ID, InputTruncated: job.InputTruncated, At: at.UTC(), Kind: job.Kind, ScopeHash: laya.Hash(job.Scope), ModelRevision: laya.ModelRevision, PromptVersion: job.PromptVersion, Sources: job.Sources, InputHashes: job.InputHashes, Fallback: fallback, Mode: "shadow"}
	if fallback == "" {
		record.Decisions = []laya.Decision{{Choice: choice, Confidence: .5, AnswerConfidence: .8, Probabilities: map[string]float64{choice: .8, "insufficient": .2}}}
	}
	return record
}

func TestRSILearningSamplesSurviveReopenWithFullEvidenceAndScopeIsolation(t *testing.T) {
	ctx := context.Background()
	config := db.Config{Driver: db.DriverFile, URL: "file://" + filepath.Join(t.TempDir(), "rsi.db")}
	core, err := db.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	samples := NewPlatformRSILearningSampleStore(core)
	sample := rsiStoreSample(t, "tenant-a", "agent-a", "run:one:call:one")
	observation, err := samples.Save(ctx, sample)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := samples.Save(ctx, sample)
	if err != nil || !duplicate.At.Equal(observation.At) {
		t.Fatalf("duplicate changed observation: %v", err)
	}
	job := BuildLayaRSISampleJob(sample)
	if strings.Contains(job.States[0], "完整结尾") || strings.Contains(job.States[0], "DO_NOT_SEND_TO_MODEL") {
		t.Fatal("full evidence leaked into model input")
	}
	record := rsiAssessment(observation, time.Now(), "tool_usage", "")
	if err := samples.SaveAssessment(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err := samples.SaveAssessment(ctx, record); err != nil {
		t.Fatalf("idempotent assessment: %v", err)
	}
	if err := core.Close(); err != nil {
		t.Fatal(err)
	}
	core, err = db.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	samples = NewPlatformRSILearningSampleStore(core)
	got, err := samples.Get(ctx, sample.Scope, observation.ID)
	if err != nil || !reflect.DeepEqual(got.Sample, sample) || got.Assessment.Decisions[0].Choice != "tool_usage" {
		t.Fatalf("reopened evidence changed: %+v, %v", got, err)
	}
	for _, other := range []RSILearningSample{rsiStoreSample(t, "tenant-b", "agent-a", sample.EvidenceRef), rsiStoreSample(t, "tenant-a", "agent-b", sample.EvidenceRef)} {
		if _, err := samples.Get(ctx, other.Scope, observation.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("cross-scope get: %v", err)
		}
		page, err := samples.List(ctx, other.Scope, "", 10)
		if err != nil || len(page.Items) != 0 {
			t.Fatalf("cross-scope list: %+v, %v", page, err)
		}
	}
	// Full content, even beyond the model excerpt, changes sample identity.
	changed := sample
	changed.Summary += "changed tail"
	next, err := samples.Save(ctx, changed)
	if err != nil || next.ID == observation.ID {
		t.Fatal("full content was not included in identity")
	}
}

func TestRSILearningAssessmentChecksProvenanceAndRetainsPendingSamples(t *testing.T) {
	ctx := context.Background()
	core, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	samples := NewPlatformRSILearningSampleStore(core)
	observation, err := samples.Save(ctx, rsiStoreSample(t, "tenant", "agent", "run:call"))
	if err != nil {
		t.Fatal(err)
	}
	page, err := samples.List(ctx, observation.Sample.Scope, "", 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].Assessment != nil {
		t.Fatal("unclassified sample was lost")
	}
	now := time.Now().UTC()
	bad := rsiAssessment(observation, now, "skill_defect", "")
	bad.InputHashes = []string{strings.Repeat("0", 64)}
	if err := samples.SaveAssessment(ctx, bad); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("accepted incorrect provenance: %v", err)
	}
	unknown := rsiAssessment(observation, now, "promote", "")
	if err := samples.SaveAssessment(ctx, unknown); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("accepted unknown classification")
	}
	fallback := rsiAssessment(observation, now, "", "queue_full")
	if err := samples.SaveAssessment(ctx, fallback); err != nil {
		t.Fatal(err)
	}
	retry := rsiAssessment(observation, now.Add(time.Second), "environment", "")
	if err := samples.SaveAssessment(ctx, retry); err != nil {
		t.Fatal(err)
	}
	if err := samples.SaveAssessment(ctx, fallback); err != nil {
		t.Fatal(err)
	}
	got, err := samples.Get(ctx, observation.Sample.Scope, observation.ID)
	if err != nil || got.Assessment.Fallback != "" || got.Assessment.Decisions[0].Choice != "environment" {
		t.Fatalf("old callback replaced retry: %+v %v", got, err)
	}
	conflict := retry
	conflict.ElapsedMS++
	if err := samples.SaveAssessment(ctx, conflict); !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting identical-time assessment overwritten")
	}
}

func TestRSILearningSamplePaginationIsBoundedAndScoped(t *testing.T) {
	ctx := context.Background()
	core, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	samples := NewPlatformRSILearningSampleStore(core)
	sample := rsiStoreSample(t, "tenant", "agent", "a")
	for _, ref := range []string{"a", "b", "c", "d", "e"} {
		sample.EvidenceRef = ref
		if _, err := samples.Save(ctx, sample); err != nil {
			t.Fatal(err)
		}
		if _, err := samples.Save(ctx, rsiStoreSample(t, "other", "agent", ref)); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 5 {
			t.Fatal("pagination did not terminate")
		}
		page, err := samples.List(ctx, sample.Scope, cursor, 2)
		if err != nil || len(page.Items) > 2 {
			t.Fatalf("page: %+v %v", page, err)
		}
		for _, item := range page.Items {
			if seen[item.ID] || item.Sample.Scope != sample.Scope {
				t.Fatal("duplicate or foreign scope")
			}
			seen[item.ID] = true
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(seen) != 5 {
		t.Fatalf("lost samples: %d", len(seen))
	}
	if _, err := samples.List(ctx, "", "", 2); err == nil {
		t.Fatal("allowed unscoped enumeration")
	}
}
