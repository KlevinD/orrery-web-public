package retrieval

import (
	"context"
	"errors"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	"orrery_v2/internal/retrievalevidence"
)

// ValidateRecords checks the exact records used to build context, then checks
// their profile grants again. A changed source is retried by the caller; old
// content never receives a stamp for its replacement. It performs no writes.
func ValidateRecords(ctx context.Context, repository Repository, subject memcore.AccessSubject, records []memcore.MemoryRecord, now time.Time) ([]retrievalevidence.Item, error) {
	profiles := map[agentcore.MemoryProfileID]memcore.MemoryProfile{}
	items := make([]retrievalevidence.Item, 0, len(records))
	for _, expected := range records {
		profile, ok := profiles[expected.ProfileID]
		if !ok {
			var err error
			profile, err = repository.GetProfile(ctx, expected.ProfileID)
			if errors.Is(err, memcore.ErrNotFound) {
				return nil, retrievalevidence.ErrInputChanged
			}
			if err != nil {
				return nil, err
			}
			if !profile.CanRead(subject) {
				return nil, retrievalevidence.ErrInputChanged
			}
			profiles[expected.ProfileID] = profile
		}
		current, err := repository.GetRecord(ctx, expected.ID)
		if errors.Is(err, memcore.ErrNotFound) {
			return nil, retrievalevidence.ErrInputChanged
		}
		if err != nil {
			return nil, err
		}
		if current.ProfileID != expected.ProfileID || !memcore.RetrievalEligible(current, now) ||
			retrievalevidence.Fingerprint(current.Normalized()) != retrievalevidence.Fingerprint(expected.Normalized()) {
			return nil, retrievalevidence.ErrInputChanged
		}
		item := memcore.RetrievalEvidence(current.Normalized(), now)
		item.SourceState = "record_verified"
		item.AccessHash = retrievalevidence.Fingerprint(struct {
			Profile memcore.MemoryProfile
			Subject memcore.AccessSubject
		}{profile, subject})
		items = append(items, retrievalevidence.Seal(item, now))
	}
	for id, expected := range profiles {
		current, err := repository.GetProfile(ctx, id)
		if errors.Is(err, memcore.ErrNotFound) {
			return nil, retrievalevidence.ErrInputChanged
		}
		if err != nil {
			return nil, err
		}
		if !current.CanRead(subject) || retrievalevidence.Fingerprint(current) != retrievalevidence.Fingerprint(expected) {
			return nil, retrievalevidence.ErrInputChanged
		}
	}
	return items, nil
}
