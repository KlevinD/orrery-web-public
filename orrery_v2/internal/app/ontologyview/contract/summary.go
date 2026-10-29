package contract

import "time"

type MetricStatus string

const (
	MetricExact       MetricStatus = "exact"
	MetricSampled     MetricStatus = "sampled"
	MetricUnavailable MetricStatus = "unavailable"
)

type CountMetric struct {
	Status MetricStatus `json:"status"`
	Value  *int64       `json:"value,omitempty"`
}

type RatioMetric struct {
	Status      MetricStatus `json:"status"`
	Numerator   *int64       `json:"numerator,omitempty"`
	Denominator *int64       `json:"denominator,omitempty"`
	Value       *float64     `json:"value,omitempty"`
}

type SummaryMeta struct {
	AsOf                  time.Time `json:"as_of"`
	DataRevision          string    `json:"data_revision"`
	AuthorizationRevision *string   `json:"authorization_revision"`
}

type Summary[T any] struct {
	Data T           `json:"data"`
	Meta SummaryMeta `json:"meta"`
}

func ExactCount(value int64) CountMetric {
	return CountMetric{Status: MetricExact, Value: &value}
}

func UnavailableCount() CountMetric {
	return CountMetric{Status: MetricUnavailable}
}

func NewRatio(numerator, denominator int64) RatioMetric {
	if denominator <= 0 {
		return RatioMetric{Status: MetricUnavailable, Numerator: &numerator, Denominator: &denominator}
	}
	value := float64(numerator) / float64(denominator)
	return RatioMetric{Status: MetricExact, Numerator: &numerator, Denominator: &denominator, Value: &value}
}
