package memorypreference

import (
	"context"
	"errors"
	"testing"
	"time"

	memcore "orrery_v2/internal/memory/core"
)

type preferenceMetadataSink struct {
	fail    bool
	records []string
}

func (s *preferenceMetadataSink) PublishMaterializedMemoryRecord(_ context.Context, record memcore.MemoryRecord) error {
	if s.fail {
		return errors.New("metadata unavailable")
	}
	s.records = append(s.records, record.ID)
	return nil
}

func TestPreferenceDeliveryRetryRepairsSavedRecordWithoutNewModelFacts(t *testing.T) {
	ctx := context.Background()
	f := newPreferenceFixture(t)
	request := f.source(t, "source", "以后默认用中文回答", f.now.Add(-time.Minute))
	sink := &preferenceMetadataSink{fail: true}
	service := f.service([]Fact{{Key: "response_language", Value: "默认用中文回答", SourceQuote: request.UserText}})
	service.options.Metadata = sink
	if err := service.Extract(ctx, request); err == nil {
		t.Fatal("delivery failure not returned")
	}
	sink.fail = false
	service.options.Extractor = extractorFunc(func(context.Context, ExtractionInput) ([]Fact, error) { return nil, nil })
	if err := service.Extract(ctx, request); err != nil {
		t.Fatal(err)
	}
	if len(sink.records) != 1 || sink.records[0] != preferenceID(f.profile.ID, "response_language") {
		t.Fatalf("saved preference was not republished: %+v", sink.records)
	}
}

func TestFreshUserStatementRenewsExpiredPreference(t *testing.T) {
	ctx := context.Background()
	f := newPreferenceFixture(t)
	first := f.source(t, "first", "以后默认用中文回答", f.now.Add(-3*time.Minute))
	fact := Fact{Key: "response_language", Value: "默认用中文回答", SourceQuote: first.UserText}
	if err := f.service([]Fact{fact}).Extract(ctx, first); err != nil {
		t.Fatal(err)
	}
	record, err := f.repo.GetRecord(ctx, preferenceID(f.profile.ID, fact.Key))
	if err != nil {
		t.Fatal(err)
	}
	record.ExpiresAt = f.now.Add(-2 * time.Minute)
	if err := f.repo.SaveRecord(ctx, record); err != nil {
		t.Fatal(err)
	}
	second := f.source(t, "second", first.UserText, f.now.Add(-time.Minute))
	if err := f.service([]Fact{fact}).Extract(ctx, second); err != nil {
		t.Fatal(err)
	}
	digest, err := Context(ctx, f.repo, f.profile, first.AgentID, f.now)
	if err != nil || len(digest.Preferences) != 1 {
		t.Fatalf("fresh preference not restored: %+v %v", digest, err)
	}
}

func TestPreferenceDeliveryRetryDoesNotRepublishPendingReview(t *testing.T) {
	ctx := context.Background()
	f := newPreferenceFixture(t)
	request := f.source(t, "source", "以后默认用中文回答", f.now.Add(-time.Minute))
	service := f.service([]Fact{{Key: "response_language", Value: "默认用中文回答", SourceQuote: request.UserText}})
	if err := service.Extract(ctx, request); err != nil {
		t.Fatal(err)
	}
	record, err := f.repo.GetRecord(ctx, preferenceID(f.profile.ID, "response_language"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.UpdateCandidateStatus(ctx, record.CandidateID, memcore.CandidateProposed); err != nil {
		t.Fatal(err)
	}
	sink := &preferenceMetadataSink{}
	service.options.Metadata = sink
	service.options.Extractor = extractorFunc(func(context.Context, ExtractionInput) ([]Fact, error) { return nil, nil })
	if err := service.Extract(ctx, request); err != nil {
		t.Fatal(err)
	}
	if len(sink.records) != 0 {
		t.Fatal("pending preference was republished as an accepted record")
	}
}
