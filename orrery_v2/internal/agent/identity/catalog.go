package identity

import "strings"

const (
	TiebingProfileID                     = "A000001"
	BianbianProfileID                    = "A000002"
	DafuProfileID                        = "A000003"
	FafaProfileID                        = "A000004"
	JordanBelfortProfileID               = "A000005"
	MarlonBrandoProfileID                = "A000006"
	TangdouProfileID                     = "A000007"
	TiedanProfileID                      = "A000008"
	XiaodoubaoProfileID                  = "A000009"
	LeadProfileID                        = "A000010"
	CoordinatorProfileID                 = "A000011"
	QAPlanProfileID                      = "A000012"
	QACheckProfileID                     = "A000013"
	WebsitePublicReceptionistProfileID   = "A000014"
	WebsiteCustomerReceptionistProfileID = "A000015"
)

var legacyProfileIDs = map[string]string{
	"gangbeng":                      TiebingProfileID,
	"bianbian":                      BianbianProfileID,
	"dafu":                          DafuProfileID,
	"fafa":                          FafaProfileID,
	"jordan-belfort":                JordanBelfortProfileID,
	"marlon-brando":                 MarlonBrandoProfileID,
	"tangdou":                       TangdouProfileID,
	"tiedan":                        TiedanProfileID,
	"xiaodoubao":                    XiaodoubaoProfileID,
	"lead-agent":                    LeadProfileID,
	"coordinator-agent":             CoordinatorProfileID,
	"qa-plan-agent":                 QAPlanProfileID,
	"qa-check-agent":                QACheckProfileID,
	"website-receptionist-public":   WebsitePublicReceptionistProfileID,
	"website-receptionist-customer": WebsiteCustomerReceptionistProfileID,
}

func CanonicalProfileID(value string) string {
	value = strings.TrimSpace(value)
	if canonical := legacyProfileIDs[strings.ToLower(value)]; canonical != "" {
		return canonical
	}
	return value
}

func LegacyProfileID(value string) string {
	value = strings.TrimSpace(value)
	for legacy, canonical := range legacyProfileIDs {
		if strings.EqualFold(value, canonical) {
			return legacy
		}
	}
	return value
}
