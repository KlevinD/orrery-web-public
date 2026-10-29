package process

type BlockedArgMode int

const (
	BlockedWithValue BlockedArgMode = iota
	BlockedStandalone
)

func FilterArgs(args []string, blocked map[string]BlockedArgMode) []string {
	if len(args) == 0 || len(blocked) == 0 {
		return append([]string(nil), args...)
	}
	filtered := make([]string, 0, len(args))
	skipNext := false
	for _, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		flag := arg
		hasInlineValue := false
		for i, r := range arg {
			if r == '=' {
				flag = arg[:i]
				hasInlineValue = true
				break
			}
		}
		mode, isBlocked := blocked[flag]
		if isBlocked {
			if mode == BlockedWithValue && !hasInlineValue {
				skipNext = true
			}
			continue
		}
		filtered = append(filtered, arg)
	}
	return filtered
}
