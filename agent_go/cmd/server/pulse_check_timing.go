package server

import "time"

const (
	pulseCheckMinGap        = 6 * time.Hour
	pulseCheckMaxGap        = 7 * 24 * time.Hour
	pulseCheckDefaultGap    = 24 * time.Hour
	pulseTimingInstructions = "Choose the next useful check within 6 hours and 7 days (one day when unspecified). Pursue one improvement change set per cycle. A failed run can wake this same Pulse; there is no pace setting or separate reviewer clock. When Pulse is off, no Pulse turn or report starts."
)
