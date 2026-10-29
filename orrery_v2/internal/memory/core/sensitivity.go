package core

type Sensitivity string

const (
	SensitivityNormal       Sensitivity = "normal"
	SensitivityPersonal     Sensitivity = "personal"
	SensitivityCompensation Sensitivity = "compensation"
	SensitivityCandidate    Sensitivity = "candidate"
	SensitivityPerformance  Sensitivity = "performance"
	SensitivityLegal        Sensitivity = "legal"
	SensitivityExecutive    Sensitivity = "executive"
	SensitivitySecret       Sensitivity = "secret"
)

func (s Sensitivity) IsValid() bool {
	switch s {
	case SensitivityNormal,
		SensitivityPersonal,
		SensitivityCompensation,
		SensitivityCandidate,
		SensitivityPerformance,
		SensitivityLegal,
		SensitivityExecutive,
		SensitivitySecret:
		return true
	default:
		return false
	}
}

func NormalizeSensitivity(sensitivity Sensitivity) Sensitivity {
	if sensitivity.IsValid() {
		return sensitivity
	}
	return SensitivityNormal
}

func HighestSensitivity(values ...Sensitivity) Sensitivity {
	best := SensitivityNormal
	bestRank := sensitivityRank(best)
	for _, value := range values {
		value = NormalizeSensitivity(value)
		if rank := sensitivityRank(value); rank > bestRank {
			best, bestRank = value, rank
		}
	}
	return best
}

func sensitivityRank(value Sensitivity) int {
	switch NormalizeSensitivity(value) {
	case SensitivitySecret:
		return 7
	case SensitivityExecutive:
		return 6
	case SensitivityLegal:
		return 5
	case SensitivityCompensation:
		return 4
	case SensitivityCandidate:
		return 3
	case SensitivityPerformance:
		return 2
	case SensitivityPersonal:
		return 1
	default:
		return 0
	}
}

type DeIDLevel string

const (
	DeIDRaw                   DeIDLevel = "raw"
	DeIDTeamSafe              DeIDLevel = "team_safe"
	DeIDOrganizationAnonymous DeIDLevel = "organization_anonymous"
)

func (l DeIDLevel) IsValid() bool {
	switch l {
	case DeIDRaw, DeIDTeamSafe, DeIDOrganizationAnonymous:
		return true
	default:
		return false
	}
}

func NormalizeDeIDLevel(level DeIDLevel) DeIDLevel {
	if level.IsValid() {
		return level
	}
	return DeIDRaw
}

type ConsentState string

const (
	ConsentNotRequired ConsentState = "not_required"
	ConsentRequested   ConsentState = "requested"
	ConsentGranted     ConsentState = "granted"
	ConsentDenied      ConsentState = "denied"
)

func (s ConsentState) IsValid() bool {
	switch s {
	case ConsentNotRequired, ConsentRequested, ConsentGranted, ConsentDenied:
		return true
	default:
		return false
	}
}

func NormalizeConsentState(state ConsentState) ConsentState {
	if state.IsValid() {
		return state
	}
	return ConsentNotRequired
}
