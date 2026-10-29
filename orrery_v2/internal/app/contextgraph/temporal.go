package contextgraph

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	temporalResourceObject = "object"
	temporalResourceLink   = "link"
	temporalResourceFact   = "fact"
)

type temporalResourceVersion struct {
	AssertionID    string
	Kind           string
	ID             string
	Object         *Object
	Link           *Link
	Fact           *Fact
	Temporal       TemporalMetadata
	SourceStreamID string
	SourceVersion  int64
	UpdatedAt      time.Time
}

func canonicalTemporalMetadata(value TemporalMetadata, fallback time.Time) TemporalMetadata {
	value.Mode = strings.ToLower(strings.TrimSpace(value.Mode))
	if value.Mode == "" {
		value.Mode = "static"
	}
	if !fallback.IsZero() {
		fallback = fallback.UTC()
	}
	if value.Mode != "static" && value.BusinessValidFrom.IsZero() && !value.BusinessTimeUnspecified {
		value.BusinessValidFrom = fallback
	}
	if !value.BusinessValidFrom.IsZero() {
		value.BusinessValidFrom = value.BusinessValidFrom.UTC()
	}
	if value.BusinessValidTo != nil {
		validTo := value.BusinessValidTo.UTC()
		value.BusinessValidTo = &validTo
	}
	if value.SourceRecordedAt.IsZero() {
		value.SourceRecordedAt = fallback
	} else {
		value.SourceRecordedAt = value.SourceRecordedAt.UTC()
	}
	if value.SourceSupersededAt != nil {
		supersededAt := value.SourceSupersededAt.UTC()
		value.SourceSupersededAt = &supersededAt
	}
	if value.OrreryIngestedAt.IsZero() {
		value.OrreryIngestedAt = value.SourceRecordedAt
	} else {
		value.OrreryIngestedAt = value.OrreryIngestedAt.UTC()
	}
	return value
}

func keepsTemporalHistory(value TemporalMetadata) bool {
	mode := strings.ToLower(strings.TrimSpace(value.Mode))
	return mode != "" && mode != "static"
}

func temporalResourceVersionIdentity(version temporalResourceVersion, accessRef AccessRef, accessState AccessState) (string, string, error) {
	var tenantID, sourceStreamID string
	var sourceVersion int64
	var payload, identityPayload any
	switch {
	case version.Object != nil:
		stored := cloneObject(*version.Object)
		identity := cloneObject(stored)
		tenantID, sourceStreamID, sourceVersion = stored.TenantID, stored.SourceStreamID, stored.SourceVersion
		if sourceVersion > 0 {
			identity.Temporal.OrreryIngestedAt = time.Time{}
		}
		payload, identityPayload = stored, identity
	case version.Link != nil:
		stored := cloneLink(*version.Link)
		identity := cloneLink(stored)
		tenantID, sourceStreamID, sourceVersion = stored.TenantID, stored.SourceStreamID, stored.SourceVersion
		if sourceVersion > 0 {
			identity.Temporal.OrreryIngestedAt = time.Time{}
		}
		payload, identityPayload = stored, identity
	case version.Fact != nil:
		stored := cloneFact(*version.Fact)
		identity := cloneFact(stored)
		tenantID, sourceStreamID, sourceVersion = stored.TenantID, stored.SourceStreamID, stored.SourceVersion
		if sourceVersion > 0 {
			identity.Temporal.OrreryIngestedAt = time.Time{}
		}
		payload, identityPayload = stored, identity
	default:
		return "", "", fmt.Errorf("unsupported temporal context graph resource %q", version.Kind)
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", "", fmt.Errorf("marshal context graph temporal resource payload: %w", err)
	}
	identityPayloadBytes, err := json.Marshal(identityPayload)
	if err != nil {
		return "", "", fmt.Errorf("marshal context graph temporal resource identity: %w", err)
	}
	identity := strings.Join([]string{
		"context-graph-resource-version:v2",
		tenantID,
		version.Kind,
		version.ID,
		string(identityPayloadBytes),
		accessRef.Kind,
		accessRef.ID,
		accessRef.TenantID,
		string(accessState),
		sourceStreamID,
		fmt.Sprintf("%d", sourceVersion),
	}, "\x1f")
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(identity))), string(payloadBytes), nil
}

func objectTemporalVersion(value Object) temporalResourceVersion {
	cloned := cloneObject(value)
	return temporalResourceVersion{
		Kind: temporalResourceObject, ID: value.ID, Object: &cloned,
		Temporal: value.Temporal, SourceStreamID: value.SourceStreamID, SourceVersion: value.SourceVersion, UpdatedAt: value.UpdatedAt,
	}
}

func linkTemporalVersion(value Link) temporalResourceVersion {
	cloned := cloneLink(value)
	return temporalResourceVersion{
		Kind: temporalResourceLink, ID: value.ID, Link: &cloned,
		Temporal: value.Temporal, SourceStreamID: value.SourceStreamID, SourceVersion: value.SourceVersion, UpdatedAt: value.UpdatedAt,
	}
}

func factTemporalVersion(value Fact) temporalResourceVersion {
	cloned := cloneFact(value)
	return temporalResourceVersion{
		Kind: temporalResourceFact, ID: value.ID, Fact: &cloned,
		Temporal: value.Temporal, SourceStreamID: value.SourceStreamID, SourceVersion: value.SourceVersion, UpdatedAt: value.UpdatedAt,
	}
}

func temporalSnapshot(
	baseObjects []Object,
	baseLinks []Link,
	baseFacts []Fact,
	evidence []Evidence,
	versions []temporalResourceVersion,
	businessAsOf time.Time,
	knownAt time.Time,
) ([]Object, []Link, []Fact, []Evidence, error) {
	if businessAsOf.IsZero() || knownAt.IsZero() {
		return nil, nil, nil, nil, fmt.Errorf("business as-of and known-at times are required")
	}
	businessAsOf = businessAsOf.UTC()
	knownAt = knownAt.UTC()
	selected, historical := selectTemporalVersions(versions, businessAsOf, knownAt)

	objects := make(map[string]Object, len(baseObjects))
	for _, value := range baseObjects {
		objects[value.ID] = cloneObject(value)
	}
	links := make(map[string]Link, len(baseLinks))
	for _, value := range baseLinks {
		links[value.ID] = cloneLink(value)
	}
	facts := make(map[string]Fact, len(baseFacts))
	for _, value := range baseFacts {
		facts[value.ID] = cloneFact(value)
	}
	for key := range historical {
		kind, id, ok := strings.Cut(key, "")
		if !ok {
			continue
		}
		switch kind {
		case temporalResourceObject:
			delete(objects, id)
		case temporalResourceLink:
			delete(links, id)
		case temporalResourceFact:
			delete(facts, id)
		}
	}
	for _, version := range selected {
		switch version.Kind {
		case temporalResourceObject:
			if version.Object != nil {
				objects[version.ID] = cloneObject(*version.Object)
			}
		case temporalResourceLink:
			if version.Link != nil {
				links[version.ID] = cloneLink(*version.Link)
			}
		case temporalResourceFact:
			if version.Fact != nil {
				facts[version.ID] = cloneFact(*version.Fact)
			}
		}
	}

	objectValues := make([]Object, 0, len(objects))
	for _, value := range objects {
		objectValues = append(objectValues, value)
	}
	linkValues := make([]Link, 0, len(links))
	for _, value := range links {
		linkValues = append(linkValues, value)
	}
	factValues := make([]Fact, 0, len(facts))
	for _, value := range facts {
		factValues = append(factValues, value)
	}
	evidenceValues := make([]Evidence, 0, len(evidence))
	for _, value := range evidence {
		if value.ObservedAt.IsZero() || !value.ObservedAt.After(knownAt) {
			evidenceValues = append(evidenceValues, value)
		}
	}
	sort.Slice(objectValues, func(i, j int) bool { return objectValues[i].ID < objectValues[j].ID })
	sort.Slice(linkValues, func(i, j int) bool { return linkValues[i].ID < linkValues[j].ID })
	sort.Slice(factValues, func(i, j int) bool { return factValues[i].ID < factValues[j].ID })
	sort.Slice(evidenceValues, func(i, j int) bool { return evidenceValues[i].ID < evidenceValues[j].ID })
	return objectValues, linkValues, factValues, evidenceValues, nil
}

func selectTemporalVersions(
	versions []temporalResourceVersion,
	businessAsOf time.Time,
	knownAt time.Time,
) (map[string]temporalResourceVersion, map[string]struct{}) {
	historical := make(map[string]struct{})
	byResourceAndStart := make(map[string]map[string]temporalResourceVersion)
	for _, version := range versions {
		resourceKey := version.Kind + "" + version.ID
		historical[resourceKey] = struct{}{}
		recordedAt := temporalRecordedAt(version)
		if recordedAt.After(knownAt) {
			continue
		}
		start := temporalBusinessStart(version)
		startKey := start.UTC().Format(time.RFC3339Nano)
		if start.IsZero() {
			startKey = "static"
		}
		group := byResourceAndStart[resourceKey]
		if group == nil {
			group = make(map[string]temporalResourceVersion)
			byResourceAndStart[resourceKey] = group
		}
		current, exists := group[startKey]
		if !exists || temporalVersionNewer(version, current) {
			group[startKey] = version
		}
	}

	selected := make(map[string]temporalResourceVersion)
	for resourceKey, byStart := range byResourceAndStart {
		var candidate temporalResourceVersion
		var candidateStart time.Time
		found := false
		for _, version := range byStart {
			start := temporalBusinessStart(version)
			if !start.IsZero() && start.After(businessAsOf) {
				continue
			}
			if !found || start.After(candidateStart) {
				candidate, candidateStart, found = version, start, true
			}
		}
		if !found {
			continue
		}
		if candidate.Temporal.SourceSupersededAt != nil && !knownAt.Before(candidate.Temporal.SourceSupersededAt.UTC()) {
			continue
		}
		if candidate.Temporal.BusinessValidTo != nil && !businessAsOf.Before(candidate.Temporal.BusinessValidTo.UTC()) {
			continue
		}
		selected[resourceKey] = candidate
	}
	return selected, historical
}

func temporalBusinessStart(version temporalResourceVersion) time.Time {
	if !version.Temporal.BusinessValidFrom.IsZero() {
		return version.Temporal.BusinessValidFrom.UTC()
	}
	if version.Temporal.Mode != "" && version.Temporal.Mode != "static" && !version.Temporal.BusinessTimeUnspecified {
		return version.UpdatedAt.UTC()
	}
	return time.Time{}
}

func temporalRecordedAt(version temporalResourceVersion) time.Time {
	if !version.Temporal.SourceRecordedAt.IsZero() {
		return version.Temporal.SourceRecordedAt.UTC()
	}
	if !version.UpdatedAt.IsZero() {
		return version.UpdatedAt.UTC()
	}
	return version.Temporal.OrreryIngestedAt.UTC()
}

func temporalVersionNewer(left, right temporalResourceVersion) bool {
	leftRecorded, rightRecorded := temporalRecordedAt(left), temporalRecordedAt(right)
	if !leftRecorded.Equal(rightRecorded) {
		return leftRecorded.After(rightRecorded)
	}
	if left.SourceVersion != right.SourceVersion {
		return left.SourceVersion > right.SourceVersion
	}
	leftIngested, rightIngested := left.Temporal.OrreryIngestedAt.UTC(), right.Temporal.OrreryIngestedAt.UTC()
	if !leftIngested.Equal(rightIngested) {
		return leftIngested.After(rightIngested)
	}
	if !left.UpdatedAt.Equal(right.UpdatedAt) {
		return left.UpdatedAt.After(right.UpdatedAt)
	}
	return left.AssertionID > right.AssertionID
}
