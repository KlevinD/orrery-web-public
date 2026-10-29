package memorypreference

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	memcore "orrery_v2/internal/memory/core"
)

func TestPreferenceContextHasWholeEntryBudgetAndOwnerIsolation(t *testing.T) {
	ctx := context.Background()
	f := newPreferenceFixture(t)
	for index, key := range preferenceKeys {
		value := strings.Repeat("简", 180)
		request := f.source(t, fmt.Sprint(index), "以后"+value, f.now.Add(time.Duration(index-10)*time.Minute))
		if err := f.service([]Fact{{Key: key, Value: value, SourceQuote: request.UserText}}).Extract(ctx, request); err != nil {
			t.Fatal(err)
		}
	}
	digest, err := Context(ctx, f.repo, f.profile, f.profile.OwnerRef.AgentID, f.now)
	if err != nil {
		t.Fatal(err)
	}
	if len(digest.Summary) > MaxContextBytes || len(digest.Preferences) == 0 || len(digest.Preferences) >= len(preferenceKeys) || !utf8.ValidString(digest.Summary) {
		t.Fatalf("context budget failed: bytes=%d facts=%d", len(digest.Summary), len(digest.Preferences))
	}
	if !strings.Contains(digest.Summary, "currency:") {
		t.Fatal("recent preferences not prioritized")
	}
	for _, value := range digest.Preferences {
		if utf8.RuneCountInString(value) != 180 {
			t.Fatal("preference was truncated")
		}
	}
	if _, err := Context(ctx, f.repo, f.profile, "agent-b", f.now); err == nil {
		t.Fatal("other owner received private preferences")
	}
}

func TestPreferenceContextHonorsLifecycleAndSourceDeletion(t *testing.T) {
	for _, mode := range []string{"preference proposed", "archived", "deleted", "anonymized", "disabled", "expired", "future", "source deleted", "source rejected"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			f := newPreferenceFixture(t)
			request := f.source(t, "source", "以后默认用中文回答", f.now.Add(-time.Minute))
			if err := f.service([]Fact{{Key: "response_language", Value: "默认用中文回答", SourceQuote: request.UserText}}).Extract(ctx, request); err != nil {
				t.Fatal(err)
			}
			record, err := f.repo.GetRecord(ctx, preferenceID(f.profile.ID, "response_language"))
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "preference proposed":
				if err := f.repo.UpdateCandidateStatus(ctx, record.CandidateID, memcore.CandidateProposed); err != nil {
					t.Fatal(err)
				}
			case "archived":
				record.LifecycleStatus = memcore.MemoryLifecycleArchived
			case "deleted":
				record.LifecycleStatus = memcore.MemoryLifecycleDeleted
			case "anonymized":
				record.LifecycleStatus = memcore.MemoryLifecycleAnonymized
			case "disabled":
				record.DisabledAt = &f.now
			case "expired":
				record.ExpiresAt = f.now
			case "future":
				record.ValidFrom = f.now.Add(time.Hour)
			case "source deleted":
				if err := f.repo.UpdateRecordLifecycle(ctx, request.SourceRecordID, memcore.MemoryLifecycleDeleted); err != nil {
					t.Fatal(err)
				}
			case "source rejected":
				if err := f.repo.UpdateCandidateStatus(ctx, "source", memcore.CandidateRejected); err != nil {
					t.Fatal(err)
				}
			}
			if mode != "preference proposed" {
				if err := f.repo.SaveRecord(ctx, record); err != nil {
					t.Fatal(err)
				}
			}
			digest, err := Context(ctx, f.repo, f.profile, request.AgentID, f.now)
			if err != nil || digest.Summary != "" {
				t.Fatalf("unreadable preference injected: %+v %v", digest, err)
			}
		})
	}
}
