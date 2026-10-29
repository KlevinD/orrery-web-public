package knowledgeinstances

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata"

	"orrery_v2/internal/app/contextgraph"
)

const TemporalProtocol = "document-assertion-time-v1"

// Validity is the model's source-grounded business window. Recorded/withdrawn
// times are deliberately absent: only the host may supply knowledge time.
// Until is exclusive unless UntilInclusive is set for a calendar-day bound.
type Validity struct {
	From           string `json:"from,omitempty"`
	Until          string `json:"until,omitempty"`
	UntilInclusive bool   `json:"until_inclusive,omitempty"`
	Timezone       string `json:"timezone,omitempty"`
	Quote          string `json:"quote"`
}

func literalDate(quote, value string) bool {
	if regexp.MustCompile(`(?:^|[^0-9])` + regexp.QuoteMeta(value) + `(?:$|[^0-9])`).MatchString(quote) {
		return true
	}
	date, err := time.Parse("2006-01-02", value)
	if err != nil {
		return false
	}
	pattern := fmt.Sprintf(`(?:^|[^0-9])%d年0?%d月0?%d日(?:$|[^0-9])`, date.Year(), int(date.Month()), date.Day())
	return regexp.MustCompile(pattern).MatchString(quote)
}

func validityBound(value, zone string, inclusive bool) (time.Time, error) {
	if len(value) == len("2006-01-02") {
		if zone == "" {
			return time.Time{}, fmt.Errorf("%w: calendar dates require an explicit timezone", ErrInvalid)
		}
		location, err := time.LoadLocation(zone)
		if err != nil {
			return time.Time{}, fmt.Errorf("%w: unknown timezone", ErrInvalid)
		}
		at, err := time.ParseInLocation("2006-01-02", value, location)
		if err != nil {
			return time.Time{}, ErrInvalid
		}
		if inclusive {
			at = at.AddDate(0, 0, 1)
		}
		if at.Year() < 1678 || at.Year() > 2261 {
			return time.Time{}, ErrInvalid
		}
		return at.UTC(), nil
	}
	if inclusive {
		return time.Time{}, fmt.Errorf("%w: inclusive end is only valid for a calendar day", ErrInvalid)
	}
	at, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || at.Year() < 1678 || at.Year() > 2261 {
		return time.Time{}, fmt.Errorf("%w: time bounds require RFC3339 with an offset", ErrInvalid)
	}
	return at.UTC(), nil
}

func (v *Validity) Bounds() (time.Time, *time.Time, error) {
	if v == nil {
		return time.Time{}, nil, nil
	}
	var start time.Time
	var end *time.Time
	var err error
	if v.From != "" {
		start, err = validityBound(v.From, v.Timezone, false)
		if err != nil {
			return start, nil, err
		}
	}
	if v.Until != "" {
		at, err := validityBound(v.Until, v.Timezone, v.UntilInclusive)
		if err != nil {
			return start, nil, err
		}
		end = &at
	}
	if (v.From == "" && v.Until == "") || (v.UntilInclusive && v.Until == "") || (end != nil && !start.IsZero() && !end.After(start)) {
		return start, end, fmt.Errorf("%w: empty or reversed business window", ErrInvalid)
	}
	return start, end, nil
}

func validateValidity(source Source, v *Validity) error {
	if v == nil {
		return nil
	}
	if !quoted(source, v.Quote) || len(v.From) > 64 || len(v.Until) > 64 || len(v.Timezone) > 128 {
		return fmt.Errorf("%w: business window requires a literal source excerpt", ErrInvalid)
	}
	if _, _, err := v.Bounds(); err != nil {
		return err
	}
	if len(v.From) == 10 || len(v.Until) == 10 {
		grounded := strings.Contains(source.Content, v.Timezone)
		aliases := map[string][]string{"Asia/Shanghai": {"北京时间", "中国标准时间", "UTC+08:00"}, "UTC": {"协调世界时", "世界标准时间", "UTC"}}
		for _, text := range aliases[v.Timezone] {
			grounded = grounded || strings.Contains(source.Content, text)
		}
		if !grounded {
			return fmt.Errorf("%w: calendar timezone is not grounded in source", ErrInvalid)
		}
	}
	for _, value := range []string{v.From, v.Until} {
		if value != "" && !literalDate(v.Quote, value) {
			return fmt.Errorf("%w: time bound is not present in the source quote", ErrInvalid)
		}
	}
	return nil
}

func validityKey(v *Validity) string {
	if v == nil {
		return "unspecified"
	}
	start, end, _ := v.Bounds()
	return Digest([]any{start, end})
}

func claimActiveAt(c Claim, at time.Time) bool {
	start, end, err := c.Validity.Bounds()
	return err == nil && (start.IsZero() || !at.Before(start)) && (end == nil || at.Before(*end))
}

func activeClaimsAt(claims []Claim, at time.Time) []Claim {
	var active []Claim
	for _, c := range claims {
		if claimActiveAt(c, at) {
			active = append(active, c)
		}
	}
	return active
}

func activeSourceClaimsAt(claims []SourceClaim, at time.Time) []SourceClaim {
	var active []SourceClaim
	for _, c := range claims {
		if claimActiveAt(c.Claim, at) {
			active = append(active, c)
		}
	}
	return active
}

func claimTemporal(c Claim, recorded time.Time) contextgraph.TemporalMetadata {
	start, end, _ := c.Validity.Bounds()
	return contextgraph.TemporalMetadata{Mode: "state_interval", BusinessTimeUnspecified: start.IsZero(), BusinessValidFrom: start, BusinessValidTo: end, SourceRecordedAt: recorded.UTC(), OrreryIngestedAt: recorded.UTC()}
}

func claimStatus(c Claim, at time.Time) string {
	if claimActiveAt(c, at) {
		return "active"
	}
	start, _, _ := c.Validity.Bounds()
	if !start.IsZero() && at.Before(start) {
		return "scheduled"
	}
	return "expired"
}
