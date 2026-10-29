package aggregate

import (
	"encoding/base64"
	"time"

	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

func mergeCounts(values ...ontologycontract.CountMetric) ontologycontract.CountMetric {
	var total int64
	for _, value := range values {
		if value.Status != ontologycontract.MetricExact || value.Value == nil {
			return ontologycontract.UnavailableCount()
		}
		total += *value.Value
	}
	return ontologycontract.ExactCount(total)
}

func mergeRatios(values ...ontologycontract.RatioMetric) ontologycontract.RatioMetric {
	if len(values) == 0 {
		return ontologycontract.RatioMetric{Status: ontologycontract.MetricUnavailable}
	}
	var numerator int64
	var denominator int64
	status := ontologycontract.MetricExact
	for _, value := range values {
		if value.Status == ontologycontract.MetricUnavailable && value.Numerator != nil && *value.Numerator == 0 && value.Denominator != nil && *value.Denominator == 0 {
			continue
		}
		if (value.Status != ontologycontract.MetricExact && value.Status != ontologycontract.MetricSampled) || value.Numerator == nil || value.Denominator == nil {
			return ontologycontract.RatioMetric{Status: ontologycontract.MetricUnavailable}
		}
		if value.Status == ontologycontract.MetricSampled {
			status = ontologycontract.MetricSampled
		}
		numerator += *value.Numerator
		denominator += *value.Denominator
	}
	merged := ontologycontract.NewRatio(numerator, denominator)
	if merged.Status == ontologycontract.MetricExact {
		merged.Status = status
	}
	return merged
}

func earliest(values ...*time.Time) *time.Time {
	var selected *time.Time
	for _, value := range values {
		if value == nil {
			continue
		}
		candidate := value.UTC()
		if selected == nil || candidate.Before(*selected) {
			selected = &candidate
		}
	}
	return selected
}

func latest(values ...*time.Time) *time.Time {
	var selected *time.Time
	for _, value := range values {
		if value == nil {
			continue
		}
		candidate := value.UTC()
		if selected == nil || candidate.After(*selected) {
			selected = &candidate
		}
	}
	return selected
}

func mergeSummaryMeta(values ...ontologycontract.SummaryMeta) ontologycontract.SummaryMeta {
	dataRevisions := make([]string, len(values))
	authorizationRevisions := make([]string, len(values))
	authorizationExact := len(values) > 0
	var asOf *time.Time
	for index, value := range values {
		dataRevisions[index] = value.DataRevision
		candidate := value.AsOf
		asOf = latest(asOf, &candidate)
		if value.AuthorizationRevision == nil || *value.AuthorizationRevision == "" {
			authorizationExact = false
			continue
		}
		authorizationRevisions[index] = *value.AuthorizationRevision
	}
	dataRevision, _ := ontologycontract.CanonicalQueryHash(dataRevisions)
	meta := ontologycontract.SummaryMeta{DataRevision: dataRevision}
	if asOf != nil {
		meta.AsOf = *asOf
	}
	if authorizationExact {
		revision, _ := ontologycontract.CanonicalQueryHash(authorizationRevisions)
		meta.AuthorizationRevision = &revision
	}
	return meta
}

func qualifyID(tenantID, localID string) string {
	return "q1." + base64.RawURLEncoding.EncodeToString([]byte(tenantID)) + "." + base64.RawURLEncoding.EncodeToString([]byte(localID))
}
