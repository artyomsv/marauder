package topics

// ReplaceDefaults is the replace-on-update policy a new topic inherits
// when its creator sends no explicit choice.
type ReplaceDefaults struct {
	ReplaceOnUpdate   bool
	ReplaceDeleteData bool
}

// BuiltinReplaceDefaults is the behaviour before any default is configured.
var BuiltinReplaceDefaults = ReplaceDefaults{ReplaceOnUpdate: false, ReplaceDeleteData: true}

// ResolveCheckInterval returns the creator's interval, or the instance-wide
// default when the creator sent none (0). An out-of-range explicit value is
// passed through so BuildAndCreate refuses it; an out-of-range fallback (only
// possible if settings were read wrong) falls back to the built-in rather
// than failing the create (issue #250).
func ResolveCheckInterval(explicit, fallback int) int {
	if explicit != 0 {
		return explicit
	}
	if ValidCheckInterval(fallback) {
		return fallback
	}
	return DefaultCheckIntervalSec
}

// Resolve honours an explicit choice, including false, before the fallback.
func Resolve(override *bool, fallback bool) bool {
	if override != nil {
		return *override
	}
	return fallback
}
