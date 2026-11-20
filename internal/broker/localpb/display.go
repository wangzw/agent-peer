package localpb

// DegradedLevelName returns the name tool results carry for level:
// "local_only", "diagnostics_only", or "unspecified" for an unset or unknown
// value.
func DegradedLevelName(level DegradedLevel) string {
	switch level {
	case DegradedLevel_DEGRADED_LEVEL_LOCAL_ONLY:
		return "local_only"
	case DegradedLevel_DEGRADED_LEVEL_DIAGNOSTICS_ONLY:
		return "diagnostics_only"
	default:
		return "unspecified"
	}
}

// DegradedLevelLabel returns the words terminal output uses for level.
func DegradedLevelLabel(level DegradedLevel) string {
	switch level {
	case DegradedLevel_DEGRADED_LEVEL_LOCAL_ONLY:
		return "local only"
	case DegradedLevel_DEGRADED_LEVEL_DIAGNOSTICS_ONLY:
		return "diagnostics only"
	default:
		return "unspecified"
	}
}
