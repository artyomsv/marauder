package topics

// ReplaceDefaults is the replace-on-update policy a new topic inherits
// when its creator sends no explicit choice.
type ReplaceDefaults struct {
	ReplaceOnUpdate   bool
	ReplaceDeleteData bool
}

// BuiltinReplaceDefaults is the behaviour before any default is configured.
var BuiltinReplaceDefaults = ReplaceDefaults{ReplaceOnUpdate: false, ReplaceDeleteData: true}

// Resolve honours an explicit choice, including false, before the fallback.
func Resolve(override *bool, fallback bool) bool {
	if override != nil {
		return *override
	}
	return fallback
}
